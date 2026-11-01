package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
)

// The fake input has a simulated Joy-Con 2 backend, so that the UI and the E2E
// can be developed against joycon2.status and the rest without a Bluetooth
// adapter. It never scans, never touches state.json and shows no gamepad: the
// fake gamepad is the only one in devices.list. What it does:
//
//   - joycon2.pair opens a window (30 s by default, as the real one does);
//     it closes by itself when the time is up, or with joycon2.cancelPair.
//   - fake.joycon2 {side?} is the user holding SYNC on a controller of side
//     "L" (the default) or "R": while the window is open, a controller
//     is "found", connected and remembered (serial FAKEJC2L0001, FAKEJC2R0001,
//     ... and address C8:48:05:FA:<count>:<01 for L, 02 for R>), as a link in
//     state "connected"
//     with a connection ID from 1001 up, and the window closes. Outside the
//     window it does nothing ({"accepted":false}).
//   - joycon2.disconnect {id} drops the link of that connection ID; the
//     controller stays remembered and disconnected. joycon2.forget {serial}
//     forgets it.
//   - Nothing is remembered across runs, and the entries do not reach
//     state.json.
//
// The simulated controllers are not in devices.list and have no slot.

// fakeJoyCon2 is the simulated backend of rpcInputSource.
type fakeJoyCon2 struct {
	mu      sync.Mutex
	auto    bool
	autoSet bool
	// windowEnd is when the pairing window closes; zero when it is closed.
	windowEnd time.Time
	known     []fakeJoyCon2Entry
	// serial numbers count up per side.
	made map[string]int
	// nextID is the connection ID of the next simulated link, minus 1001.
	nextID input.DeviceID
}

type fakeJoyCon2Entry struct {
	input.KnownJoyCon2
	// device is the connection ID of the entry's link, 0 when it is not
	// connected.
	device input.DeviceID
	player int
}

const fakeJoyCon2FirstID input.DeviceID = 1001

// open reports whether the window is open at now and closes it if its time is
// up. The caller holds f.mu.
func (f *fakeJoyCon2) open(now time.Time) bool {
	if !f.windowEnd.IsZero() && !now.Before(f.windowEnd) {
		f.windowEnd = time.Time{}
	}
	return !f.windowEnd.IsZero()
}

func (s *rpcInputSource) JoyCon2Available() error { return nil }

func (s *rpcInputSource) JoyCon2Pair(window time.Duration) error {
	if window <= 0 {
		window = input.DefaultJoyCon2Window
	}
	if window > input.MaxJoyCon2Window {
		return fmt.Errorf("input: pairing window %v is not in 0..%v", window, input.MaxJoyCon2Window)
	}
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windowEnd = time.Now().Add(window)
	return nil
}

func (s *rpcInputSource) JoyCon2CancelPair() error {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windowEnd = time.Time{}
	return nil
}

func (s *rpcInputSource) JoyCon2SetAutoConnect(on bool) error {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auto, f.autoSet = on, true
	return nil
}

func (s *rpcInputSource) JoyCon2Forget(serial string) error {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.known)
	f.known = slices.DeleteFunc(f.known, func(e fakeJoyCon2Entry) bool { return e.Serial == serial })
	if len(f.known) == n {
		return fmt.Errorf("%w: serial %q", input.ErrJoyCon2Unknown, serial)
	}
	return nil
}

func (s *rpcInputSource) JoyCon2Disconnect(id input.DeviceID) error {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.known {
		if id != 0 && f.known[i].device == id {
			f.known[i].device = 0
			return nil
		}
	}
	return fmt.Errorf("%w: %d", input.ErrNoDevice, id)
}

func (s *rpcInputSource) JoyCon2Status() (input.JoyCon2Status, error) {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	st := input.JoyCon2Status{AutoConnect: !f.autoSet || f.auto}
	if f.open(now) {
		st.WindowOpen, st.WindowLeft, st.WindowDeadline = true, f.windowEnd.Sub(now), f.windowEnd
	}
	st.Scanning = st.WindowOpen || (st.AutoConnect && len(f.known) > 0)
	for _, e := range f.known {
		st.Known = append(st.Known, input.JoyCon2KnownStatus{KnownJoyCon2: e.KnownJoyCon2, Connected: e.device != 0})
		if e.device != 0 {
			st.Links = append(st.Links, input.JoyCon2LinkStatus{Addr: e.Addr, Side: e.Side, Serial: e.Serial, Player: e.player,
				Firmware: "2.1.4", Interval: "simulated", State: "connected", Device: e.device, Known: true})
		}
	}
	return st, nil
}

// joyCon2Sync is the user holding SYNC on a new controller of side. When the
// window is open it connects and remembers one, closes the window, and returns
// it; otherwise it returns ok false.
func (s *rpcInputSource) joyCon2Sync(side jc2.Side) (k input.KnownJoyCon2, device input.DeviceID, ok bool) {
	f := &s.joy
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.open(time.Now()) {
		return input.KnownJoyCon2{}, 0, false
	}
	if f.made == nil {
		f.made = map[string]int{}
	}
	f.made[side.String()]++
	n := f.made[side.String()]
	k = input.KnownJoyCon2{Addr: 0xC84805FA0000 | uint64(n&0xff)<<8 | uint64(side), Serial: fmt.Sprintf("FAKEJC2%s%04d", side, n), Side: side}
	f.nextID++
	device = fakeJoyCon2FirstID + f.nextID - 1
	player := 1
	for _, e := range f.known {
		if e.device != 0 {
			player++
		}
	}
	f.known = append(f.known, fakeJoyCon2Entry{KnownJoyCon2: k, device: device, player: player})
	f.windowEnd = time.Time{}
	return k, device, true
}

// fakeJoyCon2Method adds fake.joycon2 {side?}: see the comment at the top of
// this file.
func (r *liveRun) fakeJoyCon2Method(methods handler.Map) {
	methods["fake.joycon2"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		src, err := r.fakeInput()
		if err != nil {
			return nil, err
		}
		var p struct {
			Side string `json:"side"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		side := jc2.SideL
		switch p.Side {
		case "", "L":
		case "R":
			side = jc2.SideR
		default:
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: `side must be "L" or "R"`}
		}
		k, device, ok := src.joyCon2Sync(side)
		if !ok {
			return map[string]any{"accepted": false}, nil
		}
		r.joyCon2Noted(k.Side.String())
		r.joyCon2Refresh()
		return map[string]any{"accepted": true, "serial": k.Serial, "side": k.Side.String(), "device": device}, nil
	}
}
