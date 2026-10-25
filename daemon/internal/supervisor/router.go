package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

const (
	// retryEvery is how often the router retries to catch up a slot that
	// is behind.
	retryEvery = 2 * time.Millisecond
	// maxIgnored bounds the gamepads whose "ignored" or "waiting" line was
	// logged.
	maxIgnored = 256
)

// router is the goroutine of Run: it reads the events, keeps the state of
// every connected gamepad and hands each slot the edges of its gamepad.
// It owns all of its fields and never waits for a slot.
type router struct {
	captures map[uint64]struct{}
	s        *Supervisor
	log      *slog.Logger
	reg      *input.Registry
	devices  map[input.DeviceID]*device
	unmapped map[input.DeviceID]input.DeviceInfo
	routes   []*route // by slot index
	alarm    *clock.Alarm
	dropped  uint64 // Dropped() at the last check
	// dropIn counts the events until the reset for a drop is due; 0 when
	// none is.
	dropIn int
	// queue holds the matching gamepads without a slot, which exist only
	// while every slot is taken: one that connects goes to the back, and a
	// holder that loses its slot to the front. A freed slot goes to the
	// first.
	queue      []*device
	ignored    map[input.DeviceKey]bool // gamepads whose "ignored" or "waiting" line was logged
	gen        uint32                   // the profile generation of the last Reload, firstGen before, for the logs
	profileKey string
	program    *engine.Program // router-owned current settings, ordered with reload barriers
	// seq counts the assignments; route.seq is the count at a route's
	// current one (IN-08).
	seq uint64
	// paused is the user's pause (DMN-06); Supervisor.paused mirrors it.
	paused bool
	// suspended is the reasons of the system's suspension the router
	// applied (DMN-09), and suspCount the count of Supervisor.susp it
	// applied them at; Supervisor.suspended mirrors suspended. Every slot
	// is paused while the mapping is held: paused, or suspended for any
	// reason (held); Supervisor.held mirrors that.
	suspended     winsession.Reason
	suspCount     uint64
	recoveryCount uint64
}

// device is a connected gamepad.
type device struct {
	id    input.DeviceID
	key   input.DeviceKey
	info  input.DeviceInfo // of its DeviceAdded, ID set
	back  bool             // the Registry knew its key: it reconnected
	state slot.State       // what its events left the controls in
	// A complete resume sample covers all losses through snapshotDrops.
	snapshotDrops uint64
	hasSnapshot   bool
	recovery      suspendControls
	// power is the latest DevicePower (IN-12), nil until one came. It goes
	// with the device: a gamepad that comes back has a new id and a new
	// record, which T-input reads again.
	power *input.Power
}

// route is the router's side of a slot.
type route struct {
	index uint8
	run   *runner
	dev   *device // the assigned gamepad, or nil
	// home is the key of the gamepad the slot belongs to, zero while dev
	// is nil; provisional is set while dev pressed no button on the
	// enabled slot since it got the slot, but for a take-over. See the
	// package doc.
	home        input.DeviceKey
	provisional bool
	disabled    bool // its runner disabled it: its edges are discarded
	// behind is set while the edge channel had no room: edges are
	// skipped until pending and a Resync are delivered.
	behind bool
	// pending holds the edges to deliver before the Resync, in order: a
	// removal or reset, then the DeviceAdded of the slot's gamepad; at
	// most one of each (see deliver).
	pending []slot.Edge
	skipped int        // edges skipped while behind
	waiting []*barrier // Sync and Reload barriers for when it has caught up
	// pausedBehind is set when a hold reached the router while the slot
	// was behind: the slot pauses before its Resync even when the mapping
	// was let go meanwhile (see flush).
	pausedBehind bool
	// seq orders the route's current assignment among all: a take-over
	// picks the provisional route of the least.
	seq uint64
}

func newRouter(s *Supervisor) *router {
	r := &router{
		captures: map[uint64]struct{}{},
		s:        s, log: s.log, reg: input.NewRegistry(),
		devices:  map[input.DeviceID]*device{},
		unmapped: map[input.DeviceID]input.DeviceInfo{},
		alarm:    clock.NewAlarm(s.cfg.Clock),
		ignored:  map[input.DeviceKey]bool{},
		gen:      firstGen,
		program:  s.cfg.Program,
	}
	for _, run := range s.runners {
		r.routes = append(r.routes, &route{index: run.index, run: run})
	}
	return r
}

// run routes until ctx is done, Events is closed, Output ends or a
// runner panics.
func (r *router) run(ctx context.Context) error {
	defer r.alarm.Stop()
	// A Suspend before Run: the slots started held (Run), and their edges
	// come first.
	if !r.syncSuspend() {
		return nil
	}
	r.arm()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-r.s.cfg.Events:
			if !ok {
				return nil
			}
			r.take(e)
		case <-r.alarm.C():
			r.alarm.Fired()
			r.checkDropped()
			r.flushAll()
		case <-r.s.suspWake:
			if !r.syncSuspend() {
				return nil
			}
		case req := <-r.s.syncs:
			if !r.drain() {
				return nil
			}
			r.sync(req)
		case reply := <-r.s.deviceQueries:
			if !r.drain() {
				return nil
			}
			reply <- r.deviceViews()
		case req := <-r.s.releases:
			if !r.drain() {
				return nil
			}
			r.release(req)
		case req := <-r.s.reloads:
			if !r.drain() {
				return nil
			}
			r.reload(req)
		case req := <-r.s.params:
			if !r.drain() {
				return nil
			}
			r.updateParams(req)
		case req := <-r.s.pauses:
			if !r.drain() {
				return nil
			}
			r.setPaused(req.on, "api", 0)
			req.reply <- struct{}{}
		case req := <-r.s.captures:
			if !r.drain() {
				return nil
			}
			r.setCapture(req)
		case req := <-r.s.layers:
			if !r.drain() {
				return nil
			}
			r.setLayer(req)
		case i := <-r.s.notes:
			r.disable(r.routes[i])
		case err := <-r.s.fatal:
			return err
		case <-r.s.cfg.Output.Done():
			err := r.s.cfg.Output.Err()
			r.log.Error("output loop ended", "err", err)
			if err == nil {
				return errors.New("supervisor: the output loop ended")
			}
			return fmt.Errorf("supervisor: the output loop ended: %w", err)
		}
		r.arm()
	}
}

// drain routes the events still queued in Events, for a Sync, a Reload or
// a Pause: a select picks at random, so the events sent before it may
// still be queued. They come first, then the suspension, whose wake may
// not have been taken yet (syncSuspend); then drops are checked and the
// slots that are behind caught up, as far as they can be. It returns false
// when Events is closed.
func (r *router) drain() bool {
	if !r.takeQueued() || !r.syncSuspend() {
		return false
	}
	r.checkDropped()
	r.flushAll()
	return true
}

// takeQueued routes the events queued in Events now. It returns false
// when Events is closed.
func (r *router) takeQueued() bool {
	for n := len(r.s.cfg.Events); n > 0; n-- {
		e, ok := <-r.s.cfg.Events
		if !ok {
			return false
		}
		r.take(e)
	}
	return true
}

// take routes one event from Events, then delivers a reset for a drop
// that is due and catches up the slots that are behind.
func (r *router) take(e input.Event) {
	r.event(e)
	// A DevicePower is routing metadata, as for traces: OnInput does not see it.
	if f := r.s.cfg.OnInput; f != nil && e.Kind != input.DevicePower {
		f(e)
	}
	if r.dropIn > 0 {
		if r.dropIn--; r.dropIn == 0 {
			r.resetAll()
		}
	}
	r.checkDropped()
	r.flushAll()
}

// event handles one input event.
func (r *router) event(e input.Event) {
	systemHeld := r.recoverySuspended()
	switch e.Kind {
	case input.DeviceAdded:
		r.added(e)
	case input.DeviceRemoved:
		r.removed(e)
	case input.DeviceState:
		r.deviceState(e)
	case input.DevicePower:
		r.devicePower(e)
	case input.DeviceRemapped:
		r.remapped(e)
	case input.ComboHeld:
		// The combo of any gamepad T-input opened toggles the pause
		// (DMN-07), also of one that waits, one the filter leaves out, or
		// one whose DeviceAdded went missing. It is no control: no slot
		// gets it, and it neither takes a slot over nor confirms one.
		r.setPaused(!r.paused, "combo", e.Device)
	default:
		d := r.devices[e.Device]
		if d == nil {
			// Not a gamepad, or its DeviceAdded went missing.
			if _, ok := r.unmapped[e.Device]; ok {
				r.unrouted(e, UnroutedNotGamepad)
			}
			return
		}
		if d.recovery.filter(e, systemHeld, r.program) {
			return
		}
		rt := r.routeOf(d)
		if rt == nil && e.Kind == input.ButtonDown {
			// Before Apply: the Resync of the take-over is to the state
			// before the press, which follows it.
			rt = r.takeOver(d, e)
		}
		d.state.Apply(e)
		if rt == nil {
			r.unroutedDevice(e, d)
			return
		}
		if rt.disabled {
			r.unrouted(e, UnroutedDisabled)
		}
		// Only a press confirms an assignment: an idle gamepad's sticks
		// and triggers jitter. A disabled slot maps nothing, so a press
		// there does not count: the home can still take the slot back, and
		// a gamepad that waits take it over, which enables it again.
		if rt.provisional && !rt.disabled && e.Kind == input.ButtonDown {
			r.confirm(rt)
		}
		r.deliver(rt, slot.Edge{Event: e})
	}
}

func (r *router) added(e input.Event) {
	if r.devices[e.Device] != nil {
		return
	}
	info := e.Info
	if info == nil || !info.IsGamepad {
		name := ""
		if info != nil {
			name = info.Name
			in := *info
			in.ID = e.Device
			r.unmapped[e.Device] = in
		}
		r.log.Debug("device ignored: not a gamepad", "id", e.Device, "name", name)
		return
	}
	delete(r.unmapped, e.Device)
	in := *info
	in.ID = e.Device
	key, back := r.reg.Connect(&in)
	d := &device{id: e.Device, key: key, info: in, back: back}
	r.devices[d.id] = d
	r.log.Info("device connected", "id", d.id, "name", in.Name, "device", d.key, "reconnected", back)
	if !r.matches(d) {
		r.logOnce(d, "gamepad ignored: it does not match the device filter")
		return
	}
	// It takes the free slot of the lowest index, if any, and a
	// provisional stand-in on the slot whose home is its key keeps that
	// slot, as its home now. When every slot is taken, it takes that slot
	// back from the stand-in instead (IN-08).
	home := r.standIn(d.key)
	if free := r.free(); free != nil {
		if home != nil {
			r.rehome(home)
		}
		r.assign(free, d, e, false, true)
		return
	}
	if home != nil {
		r.reclaim(home, d, e)
		return
	}
	// Otherwise it waits behind those connected before it.
	r.queue = append(r.queue, d)
	r.logOnce(d, "gamepad waiting: every slot is taken")
}

// matches reports whether d passes Config.Device.
func (r *router) matches(d *device) bool {
	if ignore := r.s.cfg.IgnoreDevice; ignore != nil && ignore(d.key) {
		return false
	}
	f := strings.ToLower(r.s.cfg.Device)
	return f == "" || strings.Contains(strings.ToLower(d.info.Name), f) ||
		strings.Contains(strings.ToLower(d.key.String()), f)
}

// logOnce logs msg for a gamepad without a slot, once per gamepad; its
// events then only update the router's state of it, but for a press that
// takes over a slot (takeOver).
func (r *router) logOnce(d *device, msg string) {
	if r.ignored[d.key] {
		return
	}
	if len(r.ignored) == maxIgnored {
		clear(r.ignored)
	}
	r.ignored[d.key] = true
	r.log.Info(msg, "id", d.id, "name", d.info.Name, "device", d.key)
}

// free returns the free route of the lowest index, or nil. A disabled
// route counts: assign enables it.
func (r *router) free() *route {
	for _, rt := range r.routes {
		if rt.dev == nil {
			return rt
		}
	}
	return nil
}

// standIn returns the route whose home is key and whose holder is a
// provisional stand-in with another key, or nil.
func (r *router) standIn(key input.DeviceKey) *route {
	for _, rt := range r.routes {
		if rt.provisional && rt.home == key && rt.dev != nil && rt.dev.key != rt.home {
			return rt
		}
	}
	return nil
}

// rehome makes the provisional stand-in on rt its home: the gamepad of
// the old home connected and took a free slot instead (IN-08). rt gets
// no edge, so what the stand-in holds stays held; it stays provisional,
// with the age of its assignment.
func (r *router) rehome(rt *route) {
	d, old := rt.dev, rt.home
	rt.home = d.key
	r.status(rt)
	r.log.Info("slot rehomed", "slot", rt.index, "id", d.id, "name", d.info.Name, "device", d.key, "home", old)
}

// assign gives the free slot of rt to d, enabling it again if it was
// disabled, and delivers the DeviceAdded e. d becomes the home of a slot
// that has none (a free one) and, unless provisional, of any: a slot
// handed on or given back keeps its home. A gamepad handed the slot
// (handoff) connected earlier and may hold buttons already: a Resync to
// the router's state of it follows, so that the slot presses them.
func (r *router) assign(rt *route, d *device, e input.Event, handoff, provisional bool) {
	r.seq++
	rt.seq = r.seq
	rt.dev, rt.provisional = d, provisional
	if !provisional || rt.home == (input.DeviceKey{}) {
		rt.home = d.key
	}
	if rt.disabled {
		rt.disabled = false
		rt.run.post(message{enable: true})
	}
	r.status(rt)
	r.log.Info("slot assigned", "slot", rt.index, "gen", r.gen, "id", d.id, "name", d.info.Name, "device", d.key,
		"reconnected", d.back, "handoff", handoff, "provisional", provisional)
	e.AssignedKey = d.key.String()
	r.deliver(rt, slot.Edge{Event: e})
	// A script needs a Resync barrier even for an empty initial state: its
	// dormant worker starts only after the slot has the assigned pad snapshot.
	// A handoff needs Resync to replay the waiting pad's earlier edges.
	if handoff || (r.s.cfg.ScriptFactory != nil && (r.s.cfg.ScriptConfigured == nil || r.s.cfg.ScriptConfigured())) {
		r.deliver(rt, r.resync(d.state))
	}
}

// handOff gives the free slot of rt to d, which waited, at t. Its
// DeviceAdded reached no slot, so the slot gets a copy.
func (r *router) handOff(rt *route, d *device, t uint64, provisional bool) {
	r.queue = slices.DeleteFunc(r.queue, func(q *device) bool { return q == d })
	info := d.info
	r.assign(rt, d, input.Event{Time: t, Device: d.id, Kind: input.DeviceAdded, Info: &info}, true, provisional)
}

// evict takes the slot of rt from its provisional holder for d, at t; msg
// is the log line. The slot gets a removal of the holder, which releases
// what it holds and forgets it. The holder waits again, first in line.
func (r *router) evict(rt *route, d *device, t uint64, msg string) {
	holder := rt.dev
	r.log.Info(msg, "slot", rt.index, "id", d.id, "name", d.info.Name, "device", d.key,
		"holder", holder.id, "holderDevice", holder.key)
	rt.dev = nil
	r.deliver(rt, slot.Edge{Event: input.Event{Time: t, Device: holder.id, Kind: input.DeviceRemoved}})
	r.queue = slices.Insert(r.queue, 0, holder)
}

// reclaim gives the slot of rt back to d, whose key is its home, from the
// stand-in there, which pressed no button since it got the slot; then the
// slot gets the DeviceAdded e. d just connected, so it holds nothing yet
// and needs no Resync. Like every assignment but a take-over, it is
// provisional until d presses a button. No slot is free (see added), so
// the stand-in waits again, first.
func (r *router) reclaim(rt *route, d *device, e input.Event) {
	r.evict(rt, d, e.Time, "slot reclaimed")
	r.assign(rt, d, e, false, true)
}

// takeOver gives d, which waits, the provisional slot assigned longest
// ago, whatever its home, at d's press e: the press shows which gamepad
// is in use, so the slot becomes d's own at once. The oldest holder is
// the likeliest to be idle, such as one that took a slot at startup, and
// a stand-in just handed a slot the likeliest to be in hand (IN-08). It
// returns the route, or nil when d does not wait or no slot is
// provisional.
func (r *router) takeOver(d *device, e input.Event) *route {
	if !slices.Contains(r.queue, d) {
		return nil // it does not match the device filter
	}
	var rt *route
	for _, c := range r.routes {
		if c.provisional && (rt == nil || c.seq < rt.seq) {
			rt = c
		}
	}
	if rt == nil {
		return nil
	}
	r.evict(rt, d, e.Time, "slot taken over")
	r.handOff(rt, d, e.Time, false)
	return rt
}

// confirm makes the assignment of rt to its gamepad final, at the first
// press the gamepad routes there: the slot becomes its own.
func (r *router) confirm(rt *route) {
	d := rt.dev
	rt.home, rt.provisional = d.key, false
	r.status(rt)
	r.log.Info("slot confirmed", "slot", rt.index, "id", d.id, "name", d.info.Name, "device", d.key)
}

// status publishes the gamepad and the home of rt in Status.
func (r *router) status(rt *route) {
	var st SlotStatus
	if d := rt.dev; d != nil {
		st.Key, st.ID, st.Name = d.key, d.id, d.info.Name
	}
	st.Home, st.Provisional = rt.home, rt.provisional
	r.s.update(rt.index, func(s *SlotStatus) {
		s.Key, s.ID, s.Name, s.Home, s.Provisional = st.Key, st.ID, st.Name, st.Home, st.Provisional
	})
}

// remapped handles DeviceRemapped (IN-18): SDL changed the mapping of a
// gamepad that stays one. SDL releases nothing when it does, and a control
// held under the old mapping may never send its release, so the device is
// reset like after a loss of events (Edge.Reset): the slot releases every key
// of it and counts every control as up, and so does the router's state. A
// control that is physically held counts again only after it was released and
// pressed. The device keeps its slot or its place in line; only its Info is
// the new one, since the masks of its controls may have changed. A joystick
// without a mapping is only listed again with the new Info.
func (r *router) remapped(e input.Event) {
	d := r.devices[e.Device]
	if d == nil {
		if _, ok := r.unmapped[e.Device]; ok && e.Info != nil {
			in := *e.Info
			in.ID = e.Device
			r.unmapped[e.Device] = in
		}
		return
	}
	if e.Info != nil {
		in := *e.Info
		in.ID = e.Device
		d.info = in
	}
	// A resume sample taken before the remap no longer says what the controls
	// are, and the losses it covered do not matter any more.
	d.state, d.hasSnapshot = slot.State{}, false
	r.log.Info("device remapped", "id", d.id, "name", d.info.Name, "device", d.key)
	if rt := r.routeOf(d); rt != nil {
		r.deliver(rt, slot.Edge{Reset: true})
	}
}

func (r *router) removed(e input.Event) {
	delete(r.unmapped, e.Device)
	d := r.devices[e.Device]
	if d == nil {
		return
	}
	delete(r.devices, d.id)
	if d.power != nil {
		r.s.setPower(d.id, nil)
	}
	r.reg.Disconnect(d.id)
	r.log.Info("device disconnected", "id", d.id, "name", d.info.Name, "device", d.key)
	if i := slices.Index(r.queue, d); i >= 0 {
		r.queue = slices.Delete(r.queue, i, i+1)
		return
	}
	rt := r.routeOf(d)
	if rt == nil {
		return
	}
	// The slot's Handle of the removal releases everything and resets.
	rt.dev = nil
	r.deliver(rt, slot.Edge{Event: e})
	r.log.Info("slot freed", "slot", rt.index, "id", d.id, "device", d.key)
	if len(r.queue) == 0 {
		// The next matching gamepad to connect takes it and becomes its
		// home, provisionally: a free slot is kept for no one.
		rt.home, rt.provisional = input.DeviceKey{}, false
		r.status(rt)
		return
	}
	// The first in line takes the slot at once (IN-05), provisionally, at
	// the time of the removal.
	r.handOff(rt, r.queue[0], e.Time, true)
}

// routeOf returns the route d is assigned to, or nil.
func (r *router) routeOf(d *device) *route {
	for _, rt := range r.routes {
		if rt.dev == d {
			return rt
		}
	}
	return nil
}

// deliver sends e to the slot of rt without waiting. When the channel has
// no room, the slot falls behind until flush catches it up. Meanwhile the
// edges that change what the slot must know are kept as pending, in
// order, and the others are skipped, since the Resync covers them. So a
// slot that catches up gets, in order, the release of the gamepad it had
// (its removal, or a reset), the DeviceAdded of the gamepad it has now,
// if any, then the Resync; DeviceAdded and DeviceRemoved still alternate,
// and a gamepad that came and went meanwhile is left out. The removals
// delivered are all of the slot's gamepad (rt.dev), and a DeviceAdded
// comes only when it has none, so pending is at most a release and then a
// DeviceAdded:
//   - a reset is kept when nothing is pending: a pending removal resets
//     too, and a DeviceAdded pending alone means that the slot had no
//     gamepad, so it holds nothing;
//   - a removal cancels the pending DeviceAdded of its gamepad, of which
//     the slot never learned; otherwise it replaces a pending reset, so
//     that OnEvent sees it;
//   - a DeviceAdded is kept, so that the slot learns of its gamepad;
//   - a pause alone (Edge.Pause) is neither kept nor counted as skipped:
//     the slot learns of the pause with its Resync, and of a pause that
//     came and went meanwhile before it (see flush).
//
// While the mapping is held (paused or suspended), deliver marks each edge
// that has no pause PauseOn: a slot its runner creates after the edge was
// queued, which starts held as the mapping is at that moment
// (runner.newSlot), still handles the edge paused, and learns that the
// mapping is let go from the edge delivered after it.
func (r *router) deliver(rt *route, e slot.Edge) {
	if rt.disabled {
		return
	}
	if r.held() && e.Pause == slot.PauseKeep {
		e.Pause = slot.PauseOn
	}
	if !rt.behind {
		select {
		case rt.run.edges <- e:
			return
		default:
			rt.behind = true
		}
	}
	n := len(rt.pending)
	switch {
	case isPause(e):
		if e.Pause == slot.PauseOn {
			rt.pausedBehind = true
		}
	case e.Reset:
		if n == 0 {
			rt.pending = append(rt.pending, e)
		}
	case isRemoval(e):
		if n > 0 && isAdded(rt.pending[n-1]) && rt.pending[n-1].Event.Device == e.Event.Device {
			rt.pending = rt.pending[:n-1]
		} else {
			rt.pending = append(rt.pending[:0], e)
		}
	case isAdded(e):
		rt.pending = append(rt.pending, e)
	default:
		rt.skipped++
	}
}

// isRemoval reports whether the slot loop handles e as a DeviceRemoved.
func isRemoval(e slot.Edge) bool {
	return isEvent(e) && e.Event.Kind == input.DeviceRemoved
}

// isAdded reports whether the slot loop handles e as a DeviceAdded.
func isAdded(e slot.Edge) bool {
	return isEvent(e) && e.Event.Kind == input.DeviceAdded
}

// isEvent reports whether the slot loop handles e.Event, which may come
// with a pause (slot.Edge).
func isEvent(e slot.Edge) bool {
	return !e.Reset && e.Resync == nil && (e.Pause == slot.PauseKeep || e.Event.Kind != 0)
}

// isPause reports whether e is a pause alone.
func isPause(e slot.Edge) bool {
	return !e.Reset && e.Resync == nil && !isEvent(e)
}

func (r *router) flushAll() {
	for _, rt := range r.routes {
		r.flush(rt)
	}
}

// flush catches up a slot that is behind, as far as its channel has room:
// the pending edges (see deliver), then a Resync to the state of its
// gamepad, with the pause (resync). Then edges flow again, and the
// barriers waiting for it go to its runner.
//
// A hold (a pause or a suspension) that reached the router while the slot
// was behind, and was let go since, still pauses the slot before the
// Resync, so that the presses the Resync brings in stay canceled, as on a
// slot that saw the hold. The slot may have handled edges meanwhile with
// Output's gate closed: the pause also lets go of what its engine then
// pressed.
func (r *router) flush(rt *route) {
	if !rt.behind || rt.disabled {
		return
	}
	for len(rt.pending) > 0 {
		select {
		case rt.run.edges <- rt.pending[0]:
			rt.pending = slices.Delete(rt.pending, 0, 1)
		default:
			return
		}
	}
	if rt.pausedBehind && !r.held() {
		select {
		case rt.run.edges <- slot.Edge{Pause: slot.PauseOn}:
			rt.pausedBehind = false
		default:
			return
		}
	}
	// Without a gamepad the pending removal or reset left the slot with
	// every control up: it gets the pause alone.
	e := slot.Edge{Pause: r.pauseValue()}
	if rt.dev != nil {
		e = r.resync(rt.dev.state)
	}
	select {
	case rt.run.edges <- e:
	default:
		return
	}
	r.log.Warn("slot behind; resyncing", "slot", rt.index, "skipped", rt.skipped)
	rt.behind, rt.skipped, rt.pausedBehind = false, 0, false
	for _, b := range rt.waiting {
		r.push(rt, b)
	}
	rt.waiting = nil
}

// arm keeps the retry alarm going while a slot is behind.
func (r *router) arm() {
	for _, rt := range r.routes {
		if rt.behind {
			if r.alarm.C() == nil {
				r.alarm.Set(r.s.cfg.Clock.Now().Add(retryEvery), true)
			}
			return
		}
	}
	r.alarm.Stop()
}

// checkDropped resets every slot when input dropped events since the last
// check: the state of every control is then unknown. The drop happened
// after the events still queued in Events were sent, so the reset is
// delivered after them: the presses among them must not come after it.
// Only the controls are reset: input drops button and axis events only,
// never a DeviceAdded or DeviceRemoved, so the devices stay as they are.
// A device event that waited in input for room may come after the reset
// although it was sent before the drop; that is harmless, since a removal
// resets anyway and a gamepad connecting after the reset starts with
// every control up.
func (r *router) checkDropped() {
	if r.s.cfg.Dropped == nil {
		return
	}
	n := r.s.cfg.Dropped()
	if n <= r.dropped {
		return
	}
	r.log.Warn("input dropped events; releasing everything", "dropped", n-r.dropped)
	r.dropped = n
	if r.dropIn = len(r.s.cfg.Events); r.dropIn == 0 {
		r.resetAll()
	}
}

// resetAll resets every slot. A complete resume sample covering this drop
// counter remains authoritative; older or partial device state is forgotten.
func (r *router) resetAll() {
	for _, d := range r.devices {
		if !d.hasSnapshot || d.snapshotDrops < r.dropped {
			d.state = slot.State{}
			d.hasSnapshot = false
		}
	}
	for _, rt := range r.routes {
		r.deliver(rt, slot.Edge{Reset: true})
		if rt.dev != nil && rt.dev.hasSnapshot {
			r.deliver(rt, r.resync(rt.dev.state))
		}
	}
}

// sync answers a Sync with a barrier per slot, sent behind the edges
// delivered so far.
func (r *router) sync(req syncRequest) {
	bs := make([]*barrier, 0, len(r.routes))
	for _, rt := range r.routes {
		if req.device != "" && (rt.dev == nil || req.device != rt.dev.key.String() && req.device != fmt.Sprint(rt.dev.id)) {
			continue
		}
		b := newBarrier()
		b.inspect = req.inspect
		b.action = req.action
		if req.script {
			b.action = func(sl *slot.Slot) error {
				var err error
				if req.reloadScript {
					err = sl.ReloadScript()
				}
				view := sl.InspectScript(req.metadata)
				b.scriptView = &view
				return err
			}
		}
		r.await(rt, b)
		bs = append(bs, b)
	}
	req.reply <- bs
}

// release posts a slot-wide safety release behind all input already routed.
func (r *router) release(req releaseRequest) {
	bs := make([]*barrier, 0, len(r.routes))
	for _, rt := range r.routes {
		b := newBarrier()
		b.action = func(s *slot.Slot) error { return s.ReleaseAll() }
		r.await(rt, b)
		bs = append(bs, b)
	}
	req.reply <- bs
}

// reload answers a Reload: it takes the next generation and sends each
// slot a barrier with the program behind the edges delivered so far. The
// gamepads, homes and the line stay as they are.
func (r *router) reload(req reloadRequest) {
	if r.gen == firstGen {
		r.profileKey = r.s.cfg.ProfileKey
	}
	if req.profileKey != nil {
		r.profileKey = *req.profileKey
	}
	r.gen++
	bs := make([]*barrier, 0, len(r.routes))
	for _, rt := range r.routes {
		b := newBarrier()
		b.prog, b.gen = req.prog, r.gen
		b.profileKey = r.profileKey
		r.await(rt, b)
		bs = append(bs, b)
	}
	// Later input follows these barriers on each slot, so it must use
	// their program's neutral thresholds too. Config stays immutable.
	r.program = req.prog
	req.reply <- reloadReply{gen: r.gen, bs: bs}
}

// held reports whether the mapping is held: paused or suspended. Every
// slot is paused then.
func (r *router) held() bool { return r.paused || r.suspended != 0 || len(r.captures) > 0 }

// holdAll delivers to every slot the pause of the mapping, if it changed
// from wasHeld.
func (r *router) holdAll(wasHeld bool) {
	if r.held() == wasHeld {
		return
	}
	e := slot.Edge{Pause: r.pauseValue()}
	for _, rt := range r.routes {
		r.deliver(rt, e)
	}
}

// storeHold publishes the pause, the suspension and whether they hold the
// mapping, before the edges of a change are delivered, for Status and the
// slots the runners create (see runner.newSlot).
func (r *router) storeHold() {
	r.s.capturing.Store(len(r.captures) > 0)
	r.s.paused.Store(r.paused)
	r.s.suspended.Store(uint32(r.suspended))
	r.s.held.Store(r.held())
}

// setPaused pauses the mapping, or with on false resumes it (DMN-06),
// unless the user's pause is so already; by says what did it for the log
// and OnPause, and id is the gamepad that did, 0 for none. Output's user
// gate goes first: closing it lets go of every key and drops every press
// from then on, so the slots, which pause in order with their edges,
// press nothing meanwhile. The slots get an edge only when the pause
// changes whether the mapping is held: during a suspension they stay
// paused, and the system gate stays closed.
func (r *router) setPaused(on bool, by string, id input.DeviceID) {
	if on == r.paused {
		return
	}
	wasHeld := r.held()
	r.paused = on
	r.s.cfg.Output.BlockFor(output.BlockUser, on)
	r.storeHold()
	r.holdAll(wasHeld)
	msg := "mapping resumed"
	if on {
		msg = "mapping paused"
	}
	args := []any{"by", by}
	if id != 0 {
		args = append(args, "id", id)
	}
	if r.suspended != 0 {
		args = append(args, "suspended", r.suspended)
	}
	r.log.Info(msg, args...)
	if f := r.s.cfg.OnPause; f != nil {
		f(on, by)
	}
}

// syncSuspend applies the suspension that Supervisor.Suspend set since the
// router last looked (DMN-09). It returns false when Events is closed.
//
// It goes in two steps, so that a suspension set and cleared again before
// the router looked still holds the slots for that moment. First, when a
// reason was added (the count moved), every reason set since is applied:
// the slots are held, and the log and OnSuspend say so. Then, when the
// reasons now set differ, they are applied; when they are none, the
// events queued in Events are routed first, still held: T-input may have
// queued them during the suspension, and they must not map after it.
// Once the reasons are none and the system gate is closed, the gate opens
// only after every slot has taken the edges that let it go (openAfter): a
// slot that had not could press through it what it did before them.
func (r *router) syncSuspend() bool {
	v, seen := r.s.takeSuspend()
	level := winsession.Reason(v & 0xff)
	cnt := v >> 8
	if level == r.suspended && cnt == r.suspCount {
		return true
	}
	if cnt != r.suspCount || level&^r.suspended != 0 {
		prev, wasHeld := r.suspended, r.held()
		r.captureRecovery(cnt)
		r.suspended, r.suspCount = prev|seen, cnt
		r.storeHold()
		r.holdAll(wasHeld)
		if r.suspended != prev {
			if prev == 0 {
				args := []any{"reasons", r.suspended, "paused", r.paused}
				if level == 0 {
					args = append(args, "collapsed", true) // cleared before the router looked
				}
				r.log.Info("mapping suspended", args...)
			} else {
				r.log.Info("mapping suspension changed", "reasons", r.suspended, "was", prev)
			}
			if f := r.s.cfg.OnSuspend; f != nil {
				f(prev, r.suspended)
			}
		}
	}
	if level != r.suspended {
		if level == 0 && !r.takeQueued() {
			return false
		}
		if level == 0 {
			// Unlock without a wake scan also needs a masked control
			// baseline. Publish while still held, before PauseOff can
			// rebuild a script from the previous non-neutral state.
			for _, d := range r.devices {
				changed := d.recovery.mask(&d.state)
				if rt := r.routeOf(d); rt != nil && changed {
					r.deliver(rt, r.resync(d.state))
				}
			}
		}
		prev, wasHeld := r.suspended, r.held()
		r.suspended = level
		r.storeHold()
		r.holdAll(wasHeld)
		if level == 0 {
			r.log.Info("mapping unsuspended", "was", prev, "paused", r.paused)
		} else {
			r.log.Info("mapping suspension changed", "reasons", level, "was", prev)
		}
		if f := r.s.cfg.OnSuspend; f != nil {
			f(prev, level)
		}
	}
	if r.suspended == 0 && r.s.cfg.Output.Blocking()&output.BlockSystem != 0 {
		r.openAfter(v)
	}
	return true
}

// openAfter opens the system gate, unless the suspension is no longer v
// by then (Supervisor.openSystem), once every slot has handled the edges
// delivered so far: a barrier goes to each, as for a Sync, and the last
// to finish opens it. A Sync after it returns only once it opened. A slot
// that is behind, or waits to restart, so keeps the gate closed for all
// until it has caught up or runs again; a disabled one finishes its
// barrier at once.
func (r *router) openAfter(v uint64) {
	g := &openGroup{s: r.s, v: v}
	g.n.Store(int32(len(r.routes)))
	for _, rt := range r.routes {
		b := newBarrier()
		b.after = g.one
		r.await(rt, b)
	}
}

// pauseValue is the Edge.Pause of the router's hold.
func (r *router) pauseValue() slot.Pause {
	if r.held() {
		return slot.PauseOn
	}
	return slot.PauseOff
}

// resync returns the edge of a Resync to st, with the router's hold: a
// slot that missed a pause or a suspension learns of it there, and the
// presses the Resync brings in stay canceled while held (slot.Edge).
func (r *router) resync(st slot.State) slot.Edge {
	return slot.Edge{Resync: &st, Pause: r.pauseValue()}
}

// await sends b to the runner of rt behind the edges delivered so far: at
// once, or once a slot that is behind caught up. A disabled slot has
// nothing to wait for, but its runner takes a Reload's program for the
// slot it starts next.
func (r *router) await(rt *route, b *barrier) {
	switch {
	case rt.disabled && !b.isReload():
		b.finish()
	case rt.disabled:
		rt.run.post(message{b: b})
	case rt.behind:
		rt.waiting = append(rt.waiting, b)
	default:
		r.push(rt, b)
	}
}

// push posts b to the runner of rt, with the state the slot then reaches.
func (r *router) push(rt *route, b *barrier) {
	if rt.dev != nil {
		b.assigned, b.want = true, rt.dev.state
	}
	rt.run.post(message{b: b})
}

// disable discards the edges of a slot its runner disabled; nothing reads
// them until the router enables it again. The router may have assigned the
// slot to another gamepad before it saw the note, handed it on, given it
// back to its home or to a gamepad that took it over: when the
// DeviceAdded of the slot's gamepad is among the edges the dead slot
// left, no edge of that gamepad reached the dead slot, so the slot is
// enabled again at once, as assign would have done. It then gets that
// DeviceAdded and a Resync to the gamepad's state (the fresh slot has
// every control up), and the barriers that waited follow them. Otherwise
// the barriers that waited finish, but for those of a Reload, which go to
// the runner.
//
// When the mapping was held since that DeviceAdded (a PauseOn on it or an
// edge after it, or a hold while the slot was behind), the slot pauses
// before the DeviceAdded, so that the presses the Resync brings in stay
// canceled as on a slot that saw the edges, also when the mapping was let
// go since (see flush).
func (r *router) disable(rt *route) {
	var added *slot.Edge
	paused := false // since added
	see := func(e slot.Edge) {
		if rt.dev != nil && isAdded(e) && e.Event.Device == rt.dev.id {
			added, paused = &e, false
		}
		paused = paused || e.Pause == slot.PauseOn
	}
	for len(rt.run.edges) > 0 {
		see(<-rt.run.edges)
	}
	for _, e := range rt.pending { // after those in the channel
		see(e)
	}
	paused = paused || rt.pausedBehind // it may have come before added
	waiting := rt.waiting
	rt.behind, rt.pending, rt.skipped, rt.waiting, rt.pausedBehind = false, nil, 0, nil, false
	if added == nil {
		rt.disabled = true
		for _, b := range waiting {
			r.await(rt, b) // a Reload's goes to the runner
		}
		return
	}
	rt.run.post(message{enable: true})
	if paused {
		added.Pause = slot.PauseOn
	}
	r.deliver(rt, *added) // the channel is empty
	r.deliver(rt, r.resync(rt.dev.state))
	for _, b := range waiting {
		r.await(rt, b) // the Resync may not fit
	}
}
