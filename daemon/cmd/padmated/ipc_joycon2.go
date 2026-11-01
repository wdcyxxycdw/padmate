package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// The Joy-Con 2 methods (Bluetooth LE, ADR-0002) and the topic
// joycon2.changed. The backend itself is in package input (ble.go); this file
// is padmated's side: the IPC, the state in state.json, the tray's item.
//
//	joycon2.status        → status
//	joycon2.pair {seconds?}  opens the pairing window (30 s; 1..600) → status
//	joycon2.cancelPair    closes it → status
//	joycon2.forget {serial}  forgets a remembered controller, state.json too → status
//	joycon2.disconnect {id}  closes the connection of a gamepad (both
//	                      controllers of a pair); the controller stays remembered → status
//
// A source without the backend answers status with available:false and every
// other method with -32027. A serial or an id nobody has is -32004.
//
// The status is pushed as the topic joycon2.changed whenever it changes, with
// the same shape as the result of joycon2.status.

// rpcCodeJoyCon2Unavailable is the error code of a Joy-Con 2 request when the
// backend is not there (this is not Windows, the transport did not start, or
// the input is a fake without it).
const rpcCodeJoyCon2Unavailable = -32027

// joyCon2Topic is the topic that carries the status when it changes.
const joyCon2Topic = "joycon2.changed"

// joyCon2Source is the part of a source that the Joy-Con 2 backend needs: an
// *input.Source, the fake input's source, a test's fake. The tray, the IPC
// and the config's hot apply all go through it.
type joyCon2Source interface {
	// JoyCon2Available returns nil when the backend is there, and otherwise
	// why not.
	JoyCon2Available() error
	JoyCon2Pair(window time.Duration) error
	JoyCon2CancelPair() error
	JoyCon2SetAutoConnect(on bool) error
	JoyCon2Forget(serial string) error
	JoyCon2Disconnect(id input.DeviceID) error
	JoyCon2Status() (input.JoyCon2Status, error)
}

// rpcJoyCon2Status is the result of joycon2.status and the data of the
// joycon2.changed event.
type rpcJoyCon2Status struct {
	// Available says that the backend is there. When it is not,
	// Unavailable says why (for logs; not for the user) and the rest is
	// empty.
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
	// AutoConnect is the setting joycon2.autoConnect as the backend has it.
	AutoConnect bool `json:"autoConnect"`
	// WindowOpen says that the pairing window is open; WindowEndsAt is when
	// it closes (Unix milliseconds) and WindowLeftMs how long it stays open.
	WindowOpen   bool  `json:"windowOpen"`
	WindowEndsAt int64 `json:"windowEndsAt,omitempty"`
	WindowLeftMs int64 `json:"windowLeftMs,omitempty"`
	// Scanning says that the Bluetooth scan is on.
	Scanning bool `json:"scanning"`
	// Known are the remembered controllers, Links the connections.
	Known []rpcJoyCon2Known `json:"known"`
	Links []rpcJoyCon2Link  `json:"links"`
}

// rpcJoyCon2Known is a remembered controller.
type rpcJoyCon2Known struct {
	Addr   string `json:"addr"`
	Serial string `json:"serial"`
	// Side is "L" or "R".
	Side      string `json:"side"`
	Connected bool   `json:"connected"`
}

// rpcJoyCon2Link is one connection.
type rpcJoyCon2Link struct {
	Addr   string `json:"addr"`
	Serial string `json:"serial,omitempty"`
	Side   string `json:"side"`
	Player int    `json:"player"`
	// Firmware is "major.minor.patch", absent when unknown.
	Firmware string `json:"firmware,omitempty"`
	// ParamsRequest is what became of the request for the
	// throughput-optimized connection parameters: "accepted", "unavailable
	// (Windows 10)" or "failed: <why>"; absent until the link is up.
	ParamsRequest string `json:"paramsRequest,omitempty"`
	// IntervalMs is the current connection interval in milliseconds, absent
	// when unknown (Windows 10). Latency (connection events) and TimeoutMs
	// (supervision timeout) are the other two current parameters; read them
	// only when intervalMs is present, as 0 is left out.
	IntervalMs float64 `json:"intervalMs,omitempty"`
	Latency    int     `json:"latency,omitempty"`
	TimeoutMs  int     `json:"timeoutMs,omitempty"`
	// State is "connecting", "connected", "dormant" (silent; its gamepad is
	// reported removed) or "closing".
	State string `json:"state"`
	// Device is the connection ID of its gamepad (what devices.list and
	// joycon2.disconnect take), absent when it has none reported.
	Device input.DeviceID `json:"device,omitempty"`
	Known  bool           `json:"known"`
}

func joyCon2Unavailable(reason string) rpcJoyCon2Status {
	return rpcJoyCon2Status{Unavailable: reason, Known: []rpcJoyCon2Known{}, Links: []rpcJoyCon2Link{}}
}

// newRPCJoyCon2Status converts the backend's status.
func newRPCJoyCon2Status(st input.JoyCon2Status) rpcJoyCon2Status {
	out := rpcJoyCon2Status{Available: true, AutoConnect: st.AutoConnect, WindowOpen: st.WindowOpen, Scanning: st.Scanning,
		Known: []rpcJoyCon2Known{}, Links: []rpcJoyCon2Link{}}
	if st.WindowOpen {
		out.WindowEndsAt, out.WindowLeftMs = st.WindowDeadline.UnixMilli(), st.WindowLeft.Milliseconds()
	}
	for _, k := range st.Known {
		out.Known = append(out.Known, rpcJoyCon2Known{Addr: k.AddrText(), Serial: k.Serial, Side: k.Side.String(), Connected: k.Connected})
	}
	for _, l := range st.Links {
		out.Links = append(out.Links, rpcJoyCon2Link{Addr: input.KnownJoyCon2{Addr: l.Addr}.AddrText(), Serial: l.Serial, Side: l.Side.String(),
			Player: l.Player, Firmware: l.Firmware, ParamsRequest: l.ParamsRequest, IntervalMs: l.IntervalMs, Latency: l.Latency, TimeoutMs: l.TimeoutMs,
			State: l.State, Device: l.Device, Known: l.Known})
	}
	return out
}

// sameJoyCon2 reports whether a and b are the same status for the purpose of
// the event: the time left does not count, and the end of the window only when
// it moved (the window was opened again), as the clock read for each status
// differs a little.
func sameJoyCon2(a, b rpcJoyCon2Status) bool {
	const slack = 500 // ms
	if d := a.WindowEndsAt - b.WindowEndsAt; d > slack || d < -slack {
		return false
	}
	a.WindowEndsAt, b.WindowEndsAt, a.WindowLeftMs, b.WindowLeftMs = 0, 0, 0, 0
	return jsonEqual(a, b)
}

func jsonEqual(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
}

// joyCon2Run is the Joy-Con 2 part of a liveRun.
type joyCon2Run struct {
	// pubMu serializes taking a status and publishing it, so that an older
	// status never overtakes a newer one.
	pubMu sync.Mutex
	// mu guards last.
	mu sync.Mutex
	// last is the status last published, nil before the first one.
	last *rpcJoyCon2Status
	// pairing follows the window the tray opened.
	pairing tray.JoyCon2Pairing
	// kick asks the watcher to read the status now (capacity 1).
	kick chan struct{}
	// stop ends the watcher and done is closed once it has returned; both
	// are nil when it does not run.
	stop context.CancelFunc
	done chan struct{}
}

// joyCon2Src returns the Joy-Con 2 part of the source, nil when it has none.
func (r *liveRun) joyCon2Src() joyCon2Source {
	src, _ := r.src.(joyCon2Source)
	return src
}

// joyCon2Available reports whether there is Joy-Con 2 support: the tray's
// item is shown exactly then.
func (r *liveRun) joyCon2Available() bool {
	src := r.joyCon2Src()
	return src != nil && src.JoyCon2Available() == nil
}

// joyCon2Kick asks the watcher to read the status soon. It never blocks.
func (r *liveRun) joyCon2Kick() {
	select {
	case r.joy.kick <- struct{}{}:
	default:
	}
}

// joyCon2Snapshot reads the status from the source.
func (r *liveRun) joyCon2Snapshot() rpcJoyCon2Status {
	src := r.joyCon2Src()
	if src == nil {
		return joyCon2Unavailable("this input has no Joy-Con 2 support")
	}
	if err := src.JoyCon2Available(); err != nil {
		return joyCon2Unavailable(err.Error())
	}
	st, err := src.JoyCon2Status()
	if err != nil {
		return joyCon2Unavailable(err.Error())
	}
	return newRPCJoyCon2Status(st)
}

// joyCon2Refresh reads the status and publishes it if it changed; it returns
// it. The methods answer with it, and the watcher calls it.
func (r *liveRun) joyCon2Refresh() rpcJoyCon2Status {
	j := &r.joy
	j.pubMu.Lock()
	defer j.pubMu.Unlock()
	st := r.joyCon2Snapshot()
	j.mu.Lock()
	prev := j.last
	changed := prev != nil && !sameJoyCon2(*prev, st)
	if prev == nil || changed {
		j.last = &st
	}
	var notice []tray.Notification
	if changed && prev.WindowOpen && !st.WindowOpen {
		// The window of the tray ended without a controller (a controller
		// that connects has told joyCon2Remembered before the window closes).
		if n, ok := j.pairing.Closed(); ok {
			notice = append(notice, n)
		}
	}
	j.mu.Unlock()
	for _, n := range notice {
		r.notices.Add(n)
	}
	if changed && r.events.Listening(joyCon2Topic) {
		r.events.Publish(joyCon2Topic, st)
	}
	return st
}

// startJoyCon2 starts the watcher when the source has the backend. It also
// gives the backend the setting of the config, which a source that was not
// made from it (the fake) needs.
//
// The backend has no change callback, so the watcher reads the status when it
// is kicked (a gamepad came or went, a method ran, a controller was
// remembered) and otherwise every 250 ms while the window is open or a
// connection is being made or closed and every 2 s while nothing happens,
// which also catches the window running out.
func (r *liveRun) startJoyCon2() {
	src := r.joyCon2Src()
	if src == nil || src.JoyCon2Available() != nil {
		return
	}
	if err := src.JoyCon2SetAutoConnect(r.selectionConfig().JoyCon2.AutoConnect); err != nil {
		r.log.Warn("joy-con 2 auto-connect not set", "err", err)
	}
	ctx, cancel := context.WithCancel(r.end)
	j := &r.joy
	j.stop, j.done = cancel, make(chan struct{})
	go func() {
		defer close(j.done)
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("joy-con 2 watcher stopped", "panic", v)
			}
		}()
		for ctx.Err() == nil {
			st := r.joyCon2Refresh()
			wait := 2 * time.Second
			if joyCon2Busy(st) {
				wait = 250 * time.Millisecond
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
			case <-j.kick:
			case <-t.C:
			}
			t.Stop()
		}
	}()
}

// joyCon2Busy reports whether something is going on that changes with time.
func joyCon2Busy(st rpcJoyCon2Status) bool {
	if st.WindowOpen {
		return true
	}
	for _, l := range st.Links {
		if l.State == "connecting" || l.State == "closing" {
			return true
		}
	}
	return false
}

// stopJoyCon2 stops the watcher and waits for it, a second at most.
func (r *liveRun) stopJoyCon2() {
	j := &r.joy
	if j.stop == nil {
		return
	}
	j.stop()
	select {
	case <-j.done:
	case <-time.After(watchStopWait):
		r.log.Debug("joy-con 2 watcher did not stop before shutdown")
	}
}

// joyCon2Remembered is OnRemember of the backend: a controller was connected
// in the pairing window and is remembered. It runs on T-input and so only
// hands the entry on: Memory keeps it (no I/O) and the persistence worker
// writes state.json, which is how lastProfile is saved too.
func (r *liveRun) joyCon2Remembered(k input.KnownJoyCon2) {
	if m := r.cfg.opts.memory; m != nil {
		m.RememberJoyCon2(profile.JoyCon2Controller{Addr: k.AddrText(), Serial: k.Serial, Side: k.Side.String()})
	} else {
		r.log.Warn("joy-con 2 remembered for this run only: there is no state.json", "serial", k.Serial)
	}
	r.joyCon2Noted(k.Side.String())
}

// joyCon2Noted is what follows a controller being remembered, whether the
// backend or the fake did it: the tray's notice, if its window was the one,
// and a fresh status.
func (r *liveRun) joyCon2Noted(side string) {
	if n, ok := r.joy.pairing.Remembered(side); ok {
		r.notices.Add(n)
	}
	r.joyCon2Kick()
}

// trayJoyCon2 is a click on the tray's 连接新的 Joy-Con 2: it opens the 30
// second window and tells the user what to do.
func (r *liveRun) trayJoyCon2() {
	src := r.joyCon2Src()
	if src == nil || src.JoyCon2Available() != nil {
		return
	}
	// Started first: a controller that connects at once must find the window
	// to be the tray's.
	notice := r.joy.pairing.Start()
	if err := src.JoyCon2Pair(tray.JoyCon2WindowSeconds * time.Second); err != nil {
		r.joy.pairing.Cancel()
		r.log.Warn("joy-con 2 pairing window not opened", "err", err)
		return
	}
	r.notices.Add(notice)
	r.log.Info("joy-con 2 pairing window opened from the tray", "seconds", tray.JoyCon2WindowSeconds)
	r.joyCon2Refresh()
}

// joyCon2Err maps the errors of the source to the codes of the methods.
func joyCon2Err(err error) error {
	switch {
	case errors.Is(err, input.ErrJoyCon2Unavailable):
		return &jrpc2.Error{Code: rpcCodeJoyCon2Unavailable, Message: err.Error()}
	case errors.Is(err, input.ErrJoyCon2Unknown):
		return &jrpc2.Error{Code: -32004, Message: "no such remembered Joy-Con 2"}
	case errors.Is(err, input.ErrNoDevice):
		return &jrpc2.Error{Code: -32004, Message: "device is not connected"}
	}
	return err
}

// joyCon2Required returns the source for a method that needs the backend, or
// -32027.
func (r *liveRun) joyCon2Required() (joyCon2Source, error) {
	src := r.joyCon2Src()
	if src == nil {
		return nil, &jrpc2.Error{Code: rpcCodeJoyCon2Unavailable, Message: "this input has no Joy-Con 2 support"}
	}
	if err := src.JoyCon2Available(); err != nil {
		return nil, joyCon2Err(err)
	}
	return src, nil
}

// maxJoyCon2PairSeconds is the longest window joycon2.pair opens.
const maxJoyCon2PairSeconds = int(input.MaxJoyCon2Window / time.Second)

// deviceIDParam is a connection ID in a request: a number, or a string of
// digits as the selectors of devices.list are.
type deviceIDParam struct {
	id  input.DeviceID
	set bool
}

func (d *deviceIDParam) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	} else {
		text = string(data)
	}
	n, err := strconv.ParseUint(text, 10, 32)
	if err != nil || n == 0 {
		return fmt.Errorf("id must be a connection ID, a positive integer")
	}
	d.id, d.set = input.DeviceID(n), true
	return nil
}

// joyCon2Methods registers joycon2.status, .pair, .cancelPair, .forget and
// .disconnect.
func (r *liveRun) joyCon2Methods(methods handler.Map) {
	methods["joycon2.status"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
			return nil, err
		}
		return r.joyCon2Refresh(), nil
	}
	methods["joycon2.pair"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Seconds *int `json:"seconds"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		seconds := int(input.DefaultJoyCon2Window / time.Second)
		if p.Seconds != nil {
			seconds = *p.Seconds
		}
		if seconds < 1 || seconds > maxJoyCon2PairSeconds {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "seconds must be 1 to " + strconv.Itoa(maxJoyCon2PairSeconds)}
		}
		src, err := r.joyCon2Required()
		if err != nil {
			return nil, err
		}
		// A window of somebody else is no longer the tray's: its notices
		// would say things about a window the user did not open there.
		r.joy.pairing.Cancel()
		if err := src.JoyCon2Pair(time.Duration(seconds) * time.Second); err != nil {
			return nil, joyCon2Err(err)
		}
		r.log.Info("joy-con 2 pairing window opened", "seconds", seconds)
		return r.joyCon2Refresh(), nil
	}
	methods["joycon2.cancelPair"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
			return nil, err
		}
		src, err := r.joyCon2Required()
		if err != nil {
			return nil, err
		}
		r.joy.pairing.Cancel()
		if err := src.JoyCon2CancelPair(); err != nil {
			return nil, joyCon2Err(err)
		}
		return r.joyCon2Refresh(), nil
	}
	methods["joycon2.forget"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Serial *string `json:"serial"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if p.Serial == nil || *p.Serial == "" || len(*p.Serial) > 64 {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "serial is required: the serial number of a remembered Joy-Con 2"}
		}
		src, err := r.joyCon2Required()
		if err != nil {
			return nil, err
		}
		err = src.JoyCon2Forget(*p.Serial)
		inState := false
		if m := r.cfg.opts.memory; m != nil {
			inState = m.ForgetJoyCon2(*p.Serial)
		}
		// An entry that state.json has and the backend does not (the
		// backend was given a list without it) is forgotten all the same.
		if err != nil && (!errors.Is(err, input.ErrJoyCon2Unknown) || !inState) {
			return nil, joyCon2Err(err)
		}
		r.log.Info("joy-con 2 forgotten", "serial", *p.Serial)
		return r.joyCon2Refresh(), nil
	}
	methods["joycon2.disconnect"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			ID deviceIDParam `json:"id"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if !p.ID.set {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "id is required: the connection ID of a Joy-Con 2 gamepad"}
		}
		src, err := r.joyCon2Required()
		if err != nil {
			return nil, err
		}
		if err := src.JoyCon2Disconnect(p.ID.id); err != nil {
			return nil, joyCon2Err(err)
		}
		r.log.Info("joy-con 2 disconnected", "device", p.ID.id)
		r.joyCon2Kick()
		return r.joyCon2Refresh(), nil
	}
}
