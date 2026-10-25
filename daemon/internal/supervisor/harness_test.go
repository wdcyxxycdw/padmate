package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// waitLimit bounds every real-time wait of the tests.
const waitLimit = 5 * time.Second

const ms = time.Millisecond

const (
	eastHold = `"east": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}}`
	southTap = `"south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`
	// For a Reload.
	eastShift = `"east": {"press": {"type": "keys", "keys": "Shift", "mode": "hold"}}`
	southX    = `"south": {"press": {"type": "keys", "keys": "X", "mode": "tap"}}`
	// For several slots, each holding a key of its own.
	westShift = `"west": {"press": {"type": "keys", "keys": "Shift", "mode": "hold"}}`
)

// compileBase compiles a profile whose base layer has bindings.
func compileBase(bindings ...string) (*engine.Program, error) {
	src := `{"schemaVersion": 0, "name": "t", "layers": {"base": {` + strings.Join(bindings, ", ") + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		return nil, fmt.Errorf("profile.Parse: %v", ds)
	}
	return engine.Compile(p)
}

var (
	// program is eastHold and southTap, compiled.
	program = sync.OnceValues(func() (*engine.Program, error) { return compileBase(eastHold, southTap) })
	// program2 is eastShift and southX, compiled.
	program2 = sync.OnceValues(func() (*engine.Program, error) { return compileBase(eastShift, southX) })
	// multiProgram is eastHold, westShift and southTap, compiled.
	multiProgram = sync.OnceValues(func() (*engine.Program, error) { return compileBase(eastHold, westShift, southTap) })
)

func compile(t testing.TB) *engine.Program {
	t.Helper()
	prog, err := program()
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func compile2(t testing.TB) *engine.Program {
	t.Helper()
	prog, err := program2()
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func compileMulti(t testing.TB) *engine.Program {
	t.Helper()
	prog, err := multiProgram()
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// opts configures a harness.
type opts struct {
	program  *engine.Program // nil means eastHold and southTap
	slots    int             // Config.Slots; 0 means 1
	device   string
	buffer   int
	dropped  func() uint64
	trace    func(uint8, engine.Decision)
	onEvent  func(uint8, input.Event)
	onInput  func(input.Event)
	onReload func(uint8, uint32)
	onPause  func(bool, string)
	// ignore is Config.IgnoreDevice.
	ignore func(input.DeviceKey) bool
	// onUnrouted is Config.OnUnrouted.
	onUnrouted func(input.Event, string)
	// onHaptic is Config.OnHapticDevice.
	onHaptic func(uint8, input.DeviceID, engine.Haptic)
	// onSuspend is Config.OnSuspend.
	onSuspend func(was, now winsession.Reason)
	// beforeRun runs after New, before Run starts.
	beforeRun func(*supervisor.Supervisor)
	// wrap wraps the SyncRecorder as the output.Loop's Sender.
	wrap  func(output.Sender) output.Sender
	quiet bool // no log lines in the test output
	// stopErr accepts an error of output.Loop.Stop in the cleanup.
	stopErr bool
	// queued makes Events a buffered channel that holds these events
	// when Run starts.
	queued []input.Event
	// capacity makes Events a buffered channel of at least this
	// capacity; send then returns before the router has taken the event.
	capacity int
	// logHook runs on the goroutine that logs, after each line is stored.
	logHook func(msg string)
	// hooks, when set, is OnEvent and Trace; the cleanup unstalls every
	// slot first.
	hooks         *slotHooks
	scriptFactory func(uint8, uint32, clock.Clock, slot.Arbiter) (slot.ScriptRuntime, error)
	// calibration is Config.Calibration.
	calibration func(key string) *profile.Calibration
	// switches is Config.Switches, onFeedback Config.OnFeedbackDevice.
	switches   func() profile.FeedbackSwitches
	onFeedback func(uint8, input.DeviceID, engine.Feedback)
}

// harness runs a Supervisor of opts.program on a fake clock,
// with an output.Loop sending to a simtest.SyncRecorder, fed by an event
// channel that is unbuffered unless opts.queued is set. Its cleanup stops
// Run, then the Loop, and checks that nothing is held.
type harness struct {
	t      testing.TB
	clk    *clock.Fake
	rec    *simtest.SyncRecorder
	out    *output.Loop
	events chan input.Event
	sup    *supervisor.Supervisor
	logs   *logStore
	cancel context.CancelFunc
	result chan error
	ended  bool
	err    error // Run's, once ended
}

func start(t testing.TB, o opts) *harness {
	t.Helper()
	h := &harness{
		t: t, clk: clock.NewFake(), events: make(chan input.Event, max(len(o.queued), o.capacity)),
		logs: newLogStore(), result: make(chan error, 1),
	}
	for _, e := range o.queued {
		h.events <- e
	}
	h.rec = simtest.NewSyncRecorder(h.clk)
	var sender output.Sender = h.rec
	if o.wrap != nil {
		sender = o.wrap(h.rec)
	}
	h.out = output.StartLoop(h.clk, sender, output.LoopOptions{})
	handler := &captureHandler{store: h.logs, hook: o.logHook}
	if !o.quiet {
		handler.inner = logging.New(t.Output(), slog.LevelDebug).Handler()
	}
	prog := o.program
	if prog == nil {
		prog = compile(t)
	}
	if o.hooks != nil {
		if o.onEvent != nil || o.trace != nil {
			t.Fatal("opts: hooks with onEvent or trace")
		}
		o.onEvent, o.trace = o.hooks.onEvent, o.hooks.trace
	}
	sup, err := supervisor.New(supervisor.Config{
		Clock: h.clk, Events: h.events, Dropped: o.dropped, Output: h.out, Program: prog,
		Logger: slog.New(handler), Device: o.device, EdgeBuffer: o.buffer,
		Trace: o.trace, OnEvent: o.onEvent, OnInput: o.onInput, OnUnrouted: o.onUnrouted, IgnoreDevice: o.ignore, OnReload: o.onReload, OnPause: o.onPause,
		OnSuspend: o.onSuspend, Slots: max(o.slots, 1),
		ScriptFactory: o.scriptFactory, Calibration: o.calibration, OnHapticDevice: o.onHaptic,
		Switches: o.switches, OnFeedbackDevice: o.onFeedback,
	})
	if err != nil {
		_ = h.out.Stop()
		t.Fatal(err)
	}
	h.sup = sup
	if o.beforeRun != nil {
		o.beforeRun(sup)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.result <- sup.Run(ctx) }()
	t.Cleanup(func() {
		h.cancel()
		_ = h.wait() // the test checks the error it expects
		if err := h.out.Stop(); err != nil && !o.stopErr {
			t.Errorf("output.Loop.Stop: %v", err)
		}
		h.rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	if o.hooks != nil {
		t.Cleanup(o.hooks.unstallAll) // before the one above
	}
	return h
}

// wait waits for Run to return and returns its error.
func (h *harness) wait() error {
	h.t.Helper()
	if !h.ended {
		select {
		case h.err = <-h.result:
		case <-time.After(waitLimit):
			h.t.Fatal("Run did not return")
		}
		h.ended = true
	}
	return h.err
}

// send hands e to Run: the router has taken it when send returns, unless
// opts.capacity made Events buffered.
func (h *harness) send(e input.Event) {
	h.t.Helper()
	select {
	case h.events <- e:
	case <-time.After(waitLimit):
		h.t.Fatalf("Run takes no events (%v)", e.Kind)
	}
}

// sync waits for Sync.
func (h *harness) sync() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.Sync(ctx); err != nil {
		h.t.Fatalf("Sync: %v", err)
	}
}

// states is SyncStates.
func (h *harness) states() []supervisor.SyncState {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	st, err := h.sup.SyncStates(ctx)
	if err != nil {
		h.t.Fatalf("Sync: %v", err)
	}
	return st
}

// checkReset checks that Sync finds every slot at the router's state, with
// every control up: a drop reset them all.
func (h *harness) checkReset() {
	h.t.Helper()
	for i, st := range h.states() {
		if st.Got != st.Want || st.Got != (slot.State{}) {
			h.t.Errorf("slot %d: Sync state %+v, want both reset", i, st)
		}
	}
}

// statesTicking is states for a slot that is behind: while it waits, the
// clock moves on by RetryEvery each millisecond, for the router's retries.
func (h *harness) statesTicking() []supervisor.SyncState {
	h.t.Helper()
	var st []supervisor.SyncState
	h.ticking("Sync", func(ctx context.Context) (err error) {
		st, err = h.sup.SyncStates(ctx)
		return err
	})
	return st
}

// ticking runs f, a wait for a slot that is behind: meanwhile the clock
// moves on by RetryEvery each millisecond, for the router's retries. what
// names f in a failure.
func (h *harness) ticking(what string, f func(context.Context) error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f(ctx) }()
	tick := time.NewTicker(ms)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				h.t.Fatalf("%s: %v", what, err)
			}
			return
		case <-tick.C:
			h.clk.Advance(supervisor.RetryEvery)
		}
	}
}

// reload runs Reload of prog and checks the generation it took.
func (h *harness) reload(prog *engine.Program, want uint32) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	gen, err := h.sup.Reload(ctx, prog)
	if err != nil || gen != want {
		h.t.Fatalf("Reload = %d, %v; want %d, nil", gen, err, want)
	}
}

// pause runs Pause.
func (h *harness) pause(on bool) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.Pause(ctx, on); err != nil {
		h.t.Fatalf("Pause(%v): %v", on, err)
	}
}

// blocked returns the presses the Arbiter's gate dropped so far
// (ArbiterStats.Blocked).
func (h *harness) blocked() uint64 {
	h.t.Helper()
	var n uint64
	if err := h.out.Do(func(a *output.Arbiter) error {
		n = a.Stats().Blocked
		return nil
	}); err != nil {
		h.t.Fatalf("output.Loop.Do: %v", err)
	}
	return n
}

// owners returns the owners of the keys the Arbiter holds, in the order of
// Held.
func (h *harness) owners() []output.Owner {
	h.t.Helper()
	var out []output.Owner
	if err := h.out.Do(func(a *output.Arbiter) error {
		for _, k := range a.Held() {
			out = append(out, k.Owners...)
		}
		return nil
	}); err != nil {
		h.t.Fatalf("output.Loop.Do: %v", err)
	}
	return out
}

// routerDisabled waits until the router knows that slot 0 is disabled.
func (h *harness) routerDisabled() {
	h.t.Helper()
	h.routerDisabledSlot(0)
}

// routerDisabledSlot waits until the router knows that slot i is disabled:
// the runner tells it after it logs so, and the router then finishes the
// barrier of a Sync itself, with no gamepad assigned.
func (h *harness) routerDisabledSlot(i int) {
	h.t.Helper()
	limit := time.After(waitLimit)
	for h.states()[i].Assigned {
		select {
		case <-limit:
			h.t.Fatalf("the router did not learn that slot %d is disabled", i)
		case <-time.After(ms):
		}
	}
}

// untimed drops the times of a simtest trace.
func untimed(trace string) string {
	fs := strings.Fields(trace)
	for i, f := range fs {
		fs[i], _, _ = strings.Cut(f, "@")
	}
	return strings.Join(fs, " ")
}

// set syncs, then moves the clock to msec ms.
func (h *harness) set(msec int) {
	h.t.Helper()
	h.sync()
	h.clk.Set(clock.Time(time.Duration(msec) * ms))
}

// setIdle moves the clock to msec ms once the output loop has finished
// the commands sent before, its alarm re-armed. Unlike set it waits for no
// slot, so it works while one is behind or waits to restart, after a
// rec.WaitFor of another slot's output. A move while the loop re-arms
// would push its deadline back: clock.Alarm.Set reads Now, then arms.
func (h *harness) setIdle(msec int) {
	h.t.Helper()
	if err := h.out.Do(func(*output.Arbiter) error { return nil }); err != nil {
		h.t.Fatalf("output.Loop.Do: %v", err)
	}
	h.clk.Set(clock.Time(time.Duration(msec) * ms))
}

// expect checks the injected events now.
func (h *harness) expect(want string) {
	h.t.Helper()
	if got := h.rec.Trace(); got != want {
		h.t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// expectInterleaved checks that the injected events now are an
// interleaving of groups, each a trace as expect takes: the output of
// several slots at the same instant comes in no set order, but each
// slot's own keeps its order.
func (h *harness) expectInterleaved(groups ...string) {
	h.t.Helper()
	got := h.rec.Trace()
	if err := interleaved(got, groups...); err != nil {
		h.t.Errorf("output:\n got %s\nwant an interleaving of %q: %v", got, groups, err)
	}
}

// interleaved returns an error unless every edge of the trace got is the
// next edge of one of groups, in turn, and every group runs out. Edges
// match by name, kind and time. Two groups can have the same next edge,
// so it searches.
func interleaved(got string, groups ...string) error {
	edges, err := simtest.ParseTrace(got)
	if err != nil {
		return err
	}
	gs := make([][]simtest.Edge, len(groups))
	total := 0
	for i, g := range groups {
		if gs[i], err = simtest.ParseTrace(g); err != nil {
			return fmt.Errorf("group %d: %w", i, err)
		}
		total += len(gs[i])
	}
	if len(edges) != total {
		return fmt.Errorf("%d edges, want %d", len(edges), total)
	}
	failed := map[string]bool{} // positions in the groups that lead nowhere
	var match func(next []int, at int) bool
	match = func(next []int, at int) bool {
		if at == len(edges) {
			return true
		}
		key := fmt.Sprint(next)
		if failed[key] {
			return false
		}
		for i, g := range gs {
			if next[i] < len(g) && g[next[i]] == edges[at] {
				next[i]++
				ok := match(next, at+1)
				next[i]--
				if ok {
					return true
				}
			}
		}
		failed[key] = true
		return false
	}
	if !match(make([]int, len(gs)), 0) {
		return errors.New("no interleaving")
	}
	return nil
}

// slot0 returns the status of slot 0.
func (h *harness) slot0() supervisor.SlotStatus {
	h.t.Helper()
	st := h.sup.Status()
	if len(st.Slots) != 1 {
		h.t.Fatalf("Status has %d slots, want 1", len(st.Slots))
	}
	return st.Slots[0]
}

// slot returns the status of slot i.
func (h *harness) slot(i int) supervisor.SlotStatus {
	h.t.Helper()
	st := h.sup.Status()
	if len(st.Slots) <= i {
		h.t.Fatalf("Status has %d slots, want more than %d", len(st.Slots), i)
	}
	return st.Slots[i]
}

// slotHooks is an OnEvent and a Trace for several slots, set by
// opts.hooks: stall(i) makes slot i block in OnEvent until unstall(i), and
// panicOn makes the Trace of one slot panic on each press of a control.
// Its methods are safe for concurrent use.
type slotHooks struct {
	mu      sync.Mutex
	gates   [supervisor.MaxSlots]chan struct{} // set while the slot stalls; unstall closes it
	blocked [supervisor.MaxSlots]chan struct{} // closed once the slot blocks on its gate
	panics  atomic.Pointer[panicAt]
}

// panicAt is where slotHooks.trace panics.
type panicAt struct {
	slot    uint8
	control control.Control
}

// stall makes slot i block at the next event OnEvent sees, and at those
// after, until unstall(i).
func (k *slotHooks) stall(i int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gates[i], k.blocked[i] = make(chan struct{}), make(chan struct{})
}

// unstall lets slot i go on.
func (k *slotHooks) unstall(i int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gates[i] != nil {
		close(k.gates[i])
		k.gates[i] = nil
	}
}

func (k *slotHooks) unstallAll() {
	for i := range k.gates {
		k.unstall(i)
	}
}

// waitStalled waits until slot i blocks on the gate of its last stall.
func (k *slotHooks) waitStalled(t testing.TB, i int) {
	t.Helper()
	k.mu.Lock()
	b := k.blocked[i]
	k.mu.Unlock()
	select {
	case <-b:
	case <-time.After(waitLimit):
		t.Fatalf("slot %d did not stall", i)
	}
}

func (k *slotHooks) onEvent(slot uint8, _ input.Event) {
	k.mu.Lock()
	gate, b := k.gates[slot], k.blocked[slot]
	if gate != nil {
		select {
		case <-b:
		default:
			close(b)
		}
	}
	k.mu.Unlock()
	if gate != nil {
		<-gate
	}
}

// panicOn makes the Trace of slot i panic on each press of c until disarm.
func (k *slotHooks) panicOn(i uint8, c string) {
	ctl, ok := control.Parse(c)
	if !ok {
		panic("slotHooks: no control " + c)
	}
	k.panics.Store(&panicAt{slot: i, control: ctl})
}

func (k *slotHooks) disarm() { k.panics.Store(nil) }

func (k *slotHooks) trace(slot uint8, d engine.Decision) {
	if p := k.panics.Load(); p != nil && p.slot == slot && d.Control == p.control && d.Edge == engine.EdgeDown {
		panic(fmt.Sprintf("test: %v on slot %d", p.control, slot))
	}
}

// errFail is the error of failSender.
var errFail = errors.New("test: send failed")

// failSender fails the first left Send calls whose batch has the edge of
// name and kind, injecting nothing, and passes every other call to Sender.
// Only the output loop calls it.
type failSender struct {
	output.Sender
	name string
	kind simtest.EdgeKind
	left atomic.Int32
}

// newFailSender returns a failSender that fails the first k batches with
// edge, a trace token such as "-Space"; its wrap is for opts.wrap.
func newFailSender(t testing.TB, edge string, k int) *failSender {
	t.Helper()
	es, err := simtest.ParseTrace(edge)
	if err != nil || len(es) != 1 {
		t.Fatalf("failSender edge %q: %v", edge, err)
	}
	f := &failSender{name: es[0].Name, kind: es[0].Kind}
	f.left.Store(int32(k))
	return f
}

func (f *failSender) wrap(s output.Sender) output.Sender {
	f.Sender = s
	return f
}

func (f *failSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		if g := (simtest.Record{Event: e}).Edge(); g.Name == f.name && g.Kind == f.kind && f.left.Load() > 0 {
			f.left.Add(-1)
			return 0, errFail
		}
	}
	return f.Sender.Send(events)
}

// TestExpectInterleaved checks interleaved, the check of expectInterleaved.
func TestExpectInterleaved(t *testing.T) {
	space, shift := "+Space@0 -Space@5", "+LShift@0 -LShift@5"
	for _, tc := range []struct {
		name   string
		got    string
		groups []string
		ok     bool
	}{
		{"in turn", "+Space@0 -Space@5 +LShift@5 -LShift@5", []string{space, "+LShift@5 -LShift@5"}, true},
		{"interleaved", "+Space@0 +LShift@0 -LShift@5 -Space@5", []string{space, shift}, true},
		{"one group", "+Space@0 -Space@5", []string{space}, true},
		{"none", "", nil, true},
		// The first +Space is the second group's: a greedy match fails.
		{"a shared edge", "+Space@0 +X@0 +Space@0 +Z@0", []string{"+Space@0 +Z@0", "+Space@0 +X@0"}, true},
		{"swapped in a group", "-Space@0 +Space@0 +LShift@0", []string{"+Space@0 -Space@0", "+LShift@0"}, false},
		{"a missing edge", "+Space@0 +LShift@0 -LShift@5", []string{space, shift}, false},
		{"an extra edge", "+Space@0 -Space@5 +X@5", []string{space}, false},
		{"another time", "+Space@0 -Space@6", []string{space}, false},
		{"another kind", "+Space@0 +Space@5", []string{space}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := interleaved(tc.got, tc.groups...); (err == nil) != tc.ok {
				t.Errorf("interleaved(%q, %q) = %v, want ok %v", tc.got, tc.groups, err, tc.ok)
			}
		})
	}
}

// pad returns a gamepad's DeviceInfo.
func pad(id input.DeviceID, name, guid string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: name, GUID: guid, IsGamepad: true}
}

func added(info *input.DeviceInfo) input.Event {
	return input.Event{Device: info.ID, Kind: input.DeviceAdded, Info: info}
}

func removed(id input.DeviceID) input.Event {
	return input.Event{Device: id, Kind: input.DeviceRemoved}
}

func button(id input.DeviceID, c string, down bool) input.Event {
	k := input.ButtonUp
	if down {
		k = input.ButtonDown
	}
	return input.Event{Device: id, Kind: k, Control: c}
}

func axis(id input.DeviceID, a string, v int16) input.Event {
	return input.Event{Device: id, Kind: input.AxisMotion, Control: a, Value: v}
}

// logRecord is a captured log line; attribute values are fmt.Sprint'ed.
type logRecord struct {
	Level slog.Level
	Msg   string
	Attrs map[string]string
}

// logStore keeps the lines of a captureHandler and its derivatives.
type logStore struct {
	mu      sync.Mutex
	records []logRecord
	changed chan struct{} // signaled after each line
}

func newLogStore() *logStore { return &logStore{changed: make(chan struct{}, 1)} }

func (s *logStore) add(r logRecord) {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// with returns the lines whose message starts with prefix.
func (s *logStore) with(prefix string) []logRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []logRecord
	for _, r := range s.records {
		if strings.HasPrefix(r.Msg, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// waitLog waits until n log lines have a message starting with prefix and
// returns them.
func (h *harness) waitLog(prefix string, n int) []logRecord {
	h.t.Helper()
	limit := time.After(waitLimit)
	for {
		if got := h.logs.with(prefix); len(got) >= n {
			return got
		}
		select {
		case <-h.logs.changed:
		case <-limit:
			h.t.Fatalf("%d log lines %q, want %d", len(h.logs.with(prefix)), prefix, n)
		}
	}
}

// captureHandler records every line to a logStore, runs hook, and, when
// inner is set, passes the line on.
type captureHandler struct {
	store *logStore
	attrs []slog.Attr
	inner slog.Handler
	hook  func(msg string)
}

func (*captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(ctx context.Context, rec slog.Record) error {
	r := logRecord{Level: rec.Level, Msg: rec.Message, Attrs: map[string]string{}}
	add := func(a slog.Attr) bool {
		r.Attrs[a.Key] = fmt.Sprint(a.Value.Resolve().Any())
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	rec.Attrs(add)
	h.store.add(r)
	if h.hook != nil {
		h.hook(r.Msg)
	}
	if h.inner != nil {
		return h.inner.Handle(ctx, rec)
	}
	return nil
}

func (h *captureHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(slices.Clip(h.attrs), as...)
	if h.inner != nil {
		c.inner = h.inner.WithAttrs(as)
	}
	return &c
}

func (h *captureHandler) WithGroup(string) slog.Handler {
	panic("captureHandler: groups are not used")
}
