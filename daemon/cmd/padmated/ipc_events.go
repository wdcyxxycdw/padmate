package main

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// rpcInput uses the recording format, and "remapped" for a gamepad whose
// mapping changed (IN-18), with its info as it is now. Internal combo and wake
// snapshots are not physical edges; they are deliberately excluded from the
// input topic.
type rpcInput struct {
	TimeNs  uint64            `json:"timeNs"`
	Kind    string            `json:"kind"`
	Device  input.DeviceID    `json:"device"`
	Control string            `json:"control,omitempty"`
	Value   *int16            `json:"value,omitempty"`
	Info    *input.DeviceInfo `json:"info,omitempty"`
}

func (r *liveRun) publishInput(e input.Event) {
	if !r.events.Listening("input") {
		return
	}
	switch e.Kind {
	case input.DeviceAdded, input.DeviceRemoved, input.DeviceRemapped, input.ButtonDown, input.ButtonUp, input.AxisMotion:
	default:
		return
	}
	v := rpcInput{TimeNs: e.Time, Kind: e.Kind.String(), Device: e.Device, Control: e.Control, Info: e.Info}
	if e.Kind == input.AxisMotion {
		v.Value = &e.Value
	}
	r.events.Publish("input", v)
}

// exitAnnounceWait bounds how long announceExit waits for the clients.
const exitAnnounceWait = 250 * time.Millisecond

// exitReadyWait bounds how long an exit waits for the UIs to save their
// last edit (DMN-22): the owner's 2 s.
const exitReadyWait = 2 * time.Second

// exitReplyWait bounds how long an exit that daemon.exit asked for waits for
// the caller's connection to close, after the UIs are ready, so that the
// reply is written before padmated closes the connections.
const exitReplyWait = 500 * time.Millisecond

// exitReasonInstaller is the reason of daemon.exit: the installer replaces
// or removes padmated (PKG-04). It is the only one daemon.exit accepts.
const exitReasonInstaller = "installer"

// rpcExiting is the daemon.exiting event: why padmated stops.
type rpcExiting struct {
	Reason string `json:"reason"`
}

// announceExit tells the IPC clients that subscribe to daemon.exiting that
// padmated stops for reason, "tray" when the user chose 退出 in the tray
// (DMN-22, ADR-0056) and "installer" when daemon.exit asked for it, so that
// the UI saves its last edit and quits too instead of showing a lost
// connection. It waits until the event is written to their connections,
// exitAnnounceWait at most, and returns the sessions it was written to.
// Call it before endStop.
func (r *liveRun) announceExit(reason string) []*ipc.Session {
	if !r.events.Listening("daemon.exiting") {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), exitAnnounceWait)
	defer cancel()
	written := r.events.PublishSync(ctx, "daemon.exiting", rpcExiting{Reason: reason})
	r.log.Info("exit announced", "reason", reason, "clients", len(written))
	return written
}

// exitFromTray is the tray's 退出 (DMN-22); see exitFor.
func (r *liveRun) exitFromTray() { r.exitFor("tray", nil) }

// exitFor takes the exit that the user chose (reason "tray") or the
// installer asked for (reason "installer", daemon.exit). The sequence runs
// once: a second caller, whatever its reason, returns when the first has
// finished, and tells the clients nothing more. replied, if not nil, is
// closed once the connection of the caller of daemon.exit has closed.
func (r *liveRun) exitFor(reason string, replied <-chan struct{}) {
	r.uiExit.sequence.Do(func() { r.exitSequence(reason, replied) })
}

// exitSequence: padmated lets go of every key and pauses the mapping at
// once (CLAUDE.md invariant 1), and nothing it maps presses a key again
// (output.BlockExit). Then it tells the clients (announceExit) and waits
// until each registered UI that got the event has saved its last edit and
// answered ui.exitReady, or has closed its connection, exitReadyWait at
// most from the announcement: meanwhile IPC serves its saves as always.
// Then it waits, exitReplyWait at most, for the connection of the caller of
// daemon.exit (replied), so that the caller has its reply, and takes the
// exit path, which releases every key again. Clients that are no UI
// (padmatectl events) are not waited for.
func (r *liveRun) exitSequence(reason string, replied <-chan struct{}) {
	r.loop.BlockFor(output.BlockExit, true)
	ctx, cancel := context.WithTimeout(r.end, time.Second)
	if err := r.sup.Pause(ctx, true); err != nil {
		r.log.Debug("mapping not paused before the exit", "err", err)
	}
	cancel()
	ready := r.uiExit.arm()
	wait := r.cfg.exitReadyWait
	if wait <= 0 {
		wait = exitReadyWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	start := time.Now()
	uis, confirmed := 0, 0
	timedOut := false
	for _, s := range r.announceExit(reason) {
		done, ok := ready[s]
		if !ok {
			continue // not a UI
		}
		uis++
		if timedOut {
			continue
		}
		select {
		case <-done:
			confirmed++
		case <-s.Done():
		case <-deadline.C:
			timedOut = true
		case <-r.end.Done():
			timedOut = true
		}
	}
	switch {
	case timedOut:
		r.log.Warn("UI did not confirm the exit in time", "reason", reason, "ui", uis, "ready", confirmed, "ms", time.Since(start).Milliseconds())
	case uis > 0:
		r.log.Info("UI ready to exit", "reason", reason, "ui", uis, "ready", confirmed, "ms", time.Since(start).Milliseconds())
	}
	if replied != nil {
		replyWait := time.NewTimer(exitReplyWait)
		defer replyWait.Stop()
		select {
		case <-replied:
		case <-replyWait.C:
		case <-r.end.Done():
		}
	}
	r.endStop(stopCause(reason))
}

// exitMethods adds daemon.exit (PKG-04): the installer asks the running
// padmated to exit as the tray's 退出 does. It replies at once and runs the
// sequence on a goroutine of its own; the sequence waits for session's
// connection to close, exitReplyWait at most, before it closes the
// connections, so the reply is written first. Only reason "installer" is
// accepted. Like every other method it needs the authenticated connection
// of the user's own process; the UI's bridge does not pass it on.
func (r *liveRun) exitMethods(methods handler.Map, session *ipc.Session) {
	methods["daemon.exit"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Reason string `json:"reason"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if p.Reason != exitReasonInstaller {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: `reason must be "` + exitReasonInstaller + `"`}
		}
		// Already stopping: the exit is under way, and there is nothing to run.
		if r.end.Err() == nil {
			go r.exitFor(p.Reason, session.Done())
		}
		return map[string]any{"exiting": true, "reason": p.Reason}, nil
	}
}

// uiExits keeps the connections of the UIs that ui.register verified, for
// an exit to wait for their ui.exitReady (DMN-22).
type uiExits struct {
	mu  sync.Mutex
	uis map[*ipc.Session]chan struct{}
	// exiting is set once an exit has begun; from then on ready closes a
	// UI's channel.
	exiting bool
	// sequence runs the exit sequence once (exitFor).
	sequence sync.Once
}

// add keeps s, the connection of a UI that ui.register verified.
func (u *uiExits) add(s *ipc.Session) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.uis == nil {
		u.uis = make(map[*ipc.Session]chan struct{})
	}
	if _, ok := u.uis[s]; !ok {
		u.uis[s] = make(chan struct{})
	}
}

// remove forgets s, a closed connection.
func (u *uiExits) remove(s *ipc.Session) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.uis, s)
}

// arm begins an exit: it returns the channel of each UI kept now, which
// ready closes.
func (u *uiExits) arm() map[*ipc.Session]chan struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.exiting = true
	return maps.Clone(u.uis)
}

// ready is ui.exitReady of s: once an exit has begun, it closes s's
// channel. It reports whether one has.
func (u *uiExits) ready(s *ipc.Session) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.exiting {
		return false
	}
	if done, ok := u.uis[s]; ok {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	return true
}

// Periodic detached snapshots complement immediate decisions/log/input. They
// also give a newly subscribed client current state without replaying actions.
func (r *liveRun) observeIPC(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		func() {
			query, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if r.events.Listening("script.status") {
				if views, err := r.sup.Scripts(query, "", false, false); err == nil {
					r.events.Publish("script.status", views)
				}
			}
			if r.events.Listening("engine.activity") {
				views, err := r.sup.Inspect(query)
				if err != nil {
					return
				}
				slots := make([]rpcSlot, 0, len(views))
				for _, v := range views {
					slots = append(slots, rpcSlotView(v))
				}
				st := r.sup.Status()
				r.events.Publish("engine.activity", map[string]any{"kind": "state", "paused": st.Paused, "capturing": st.Capturing, "suspended": st.Suspended.String(), "slots": slots, "localDryRun": r.ownMode.Load(), "sendToOwnWindow": r.ownSend.Load(), "batteries": r.batteries(query)})
			}
		}()
	}
}
