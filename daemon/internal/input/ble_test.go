package input

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// These tests need no SDL. A Source that was never opened gets a stand-in for
// T-input, a goroutine that runs its commands, and a fake clock for the
// ticks; any call into SDL would panic, which is what shows that no
// Joy-Con 2 id is ever passed to it.

// bleSource is a Source with the Joy-Con 2 backend on a fake transport.
type bleSource struct {
	t     *testing.T
	s     *Source
	tr    *fakeBLE
	clock atomic.Int64
	mu    sync.Mutex
	known []KnownJoyCon2 // OnRemember's calls
	got   []Event        // events taken from s.events
}

// newBLESource starts the stand-in T-input. mod may change the options and
// the Source before it starts; opts nil means no backend.
func newBLESource(t *testing.T, opts *JoyCon2Options, mod func(*Source)) *bleSource {
	t.Helper()
	b := &bleSource{t: t, tr: newFakeBLE()}
	b.clock.Store(int64(time.Second))
	s := &Source{
		events: make(chan Event, 1024),
		cmds:   make(chan func()),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
		log:    slog.New(slog.DiscardHandler),
		ticks:  func() uint64 { return uint64(b.clock.Load()) },
	}
	if opts != nil {
		o := *opts
		if o.Transport == nil {
			o.Transport = func(*slog.Logger) (JoyCon2Transport, error) { return b.tr, nil }
		}
		o.OnRemember = func(k KnownJoyCon2) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.known = append(b.known, k)
		}
		s.joyOpts = &o
	}
	if mod != nil {
		mod(s)
	}
	b.s = s
	started := make(chan struct{})
	go func() {
		defer close(s.done)
		s.startJoyCon2()
		close(started)
		for {
			select {
			case <-s.quit:
				s.closeJoyCon2()
				return
			case f := <-s.cmds:
				f()
			}
		}
	}()
	<-started
	t.Cleanup(func() { s.RequestClose(); <-s.done })
	return b
}

// pump runs one turn's share of the loop on the stand-in.
func (b *bleSource) pump() {
	b.t.Helper()
	if err := b.s.do(func() error { b.s.pumpJoyCon2(); return nil }); err != nil {
		b.t.Fatal(err)
	}
}

// advance moves the clock and pumps.
func (b *bleSource) advance(d time.Duration) {
	b.clock.Add(int64(d))
	b.pump()
}

// events returns the events that arrived since the last call.
func (b *bleSource) events() []Event {
	for {
		select {
		case e := <-b.s.events:
			b.got = append(b.got, e)
		default:
			out := b.got
			b.got = nil
			return out
		}
	}
}

func (b *bleSource) remembered() []KnownJoyCon2 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.known)
}

// connect brings a controller up through the transport and returns its link.
func (b *bleSource) connect(addr uint64, side jc2.Side, serial string) jc2win.LinkID {
	b.t.Helper()
	n := len(b.tr.connectList())
	b.tr.push(advertEvent(addr, side, true))
	b.pump()
	cs := b.tr.connectList()
	if len(cs) == n {
		b.t.Fatalf("no connect to %x", addr)
	}
	b.tr.push(connectedEvent(cs[n].id, side, serial, nil))
	b.pump()
	return cs[n].id
}

func (b *bleSource) report(id jc2win.LinkID, bits uint16) {
	b.tr.push(reportEvent(id, 1, bits, 2048, 2048, 0x24))
	b.pump()
}

var bleKnown = []KnownJoyCon2{{addrL1, serL1, jc2.SideL}, {addrR1, serR1, jc2.SideR}}

func TestSourceJoyCon2Off(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts *JoyCon2Options
	}{
		{"not asked for", nil},
		{"transport does not start", &JoyCon2Options{Transport: func(*slog.Logger) (JoyCon2Transport, error) {
			return nil, errors.New("no radio")
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBLESource(t, tc.opts, nil)
			s := b.s
			calls := map[string]error{
				"Available":  s.JoyCon2Available(),
				"Pair":       s.JoyCon2Pair(0),
				"CancelPair": s.JoyCon2CancelPair(),
				"SetKnown":   s.JoyCon2SetKnown(bleKnown),
				"AutoConn":   s.JoyCon2SetAutoConnect(true),
				"Forget":     s.JoyCon2Forget(serL1),
				"Disconnect": s.JoyCon2Disconnect(joyCon2IDBase),
			}
			_, calls["Status"] = s.JoyCon2Status()
			for name, err := range calls {
				if !errors.Is(err, ErrJoyCon2Unavailable) {
					t.Errorf("%s: error %v, want ErrJoyCon2Unavailable", name, err)
				}
			}
			if tc.opts != nil && !errors.Is(s.JoyCon2Available(), s.joyErr) {
				t.Errorf("error %v does not carry why: %v", s.JoyCon2Available(), s.joyErr)
			}
			b.pump() // nothing to pump, nothing to crash
			if s.isJoyCon2(joyCon2IDBase) {
				t.Error("a device without the backend")
			}
		})
	}
}

func TestSourceJoyCon2PairingFlow(t *testing.T) {
	b := newBLESource(t, &JoyCon2Options{}, nil)
	s := b.s
	if err := s.JoyCon2Available(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{-1, MaxJoyCon2Window + 1} {
		if err := s.JoyCon2Pair(d); err == nil {
			t.Errorf("JoyCon2Pair(%v) was accepted", d)
		}
	}
	st, err := s.JoyCon2Status()
	if err != nil || st.WindowOpen || st.Scanning {
		t.Fatalf("status %+v, %v before the window", st, err)
	}
	before := time.Now()
	if err := s.JoyCon2Pair(0); err != nil {
		t.Fatal(err)
	}
	st, _ = s.JoyCon2Status()
	if !st.WindowOpen || !st.Scanning || st.WindowLeft != DefaultJoyCon2Window ||
		st.WindowDeadline.Before(before.Add(DefaultJoyCon2Window)) || st.WindowDeadline.After(time.Now().Add(DefaultJoyCon2Window)) {
		t.Fatalf("status %+v with the window open", st)
	}

	id := b.connect(addrL1, jc2.SideL, serL1)
	evs := b.events()
	if len(evs) != 1 || evs[0].Kind != DeviceAdded || evs[0].Info.Type != "joyconleft" || evs[0].Device < joyCon2IDBase {
		t.Fatalf("events %v, want the Joy-Con 2 gamepad", descs(evs))
	}
	dev := evs[0].Device
	if got := b.remembered(); !slices.Equal(got, []KnownJoyCon2{{addrL1, serL1, jc2.SideL}}) {
		t.Fatalf("remembered %v", got)
	}
	st, _ = s.JoyCon2Status()
	if st.WindowOpen || st.Scanning || len(st.Links) != 1 || len(st.Known) != 1 || !st.Known[0].Connected || st.Links[0].Device != dev {
		t.Fatalf("status %+v after the controller connected", st)
	}

	b.report(id, bLLeft)
	evs = b.events()
	if len(evs) != 1 || evs[0].Kind != ButtonDown || evs[0].Control != "dpad.left" || evs[0].Device != dev || evs[0].Stamp != 1234 {
		t.Fatalf("events %v, want the dpad.left press with the report's stamp", descs(evs))
	}

	// Auto-connect on and a remembered controller: scanning, and the
	// controller comes back by itself.
	if err := s.JoyCon2SetAutoConnect(true); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.JoyCon2Status(); !st.Scanning || !st.AutoConnect {
		t.Fatalf("status %+v with auto-connect on", st)
	}
	if err := s.JoyCon2Disconnect(dev + 5); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Disconnect of an unknown id: %v", err)
	}
	if err := s.JoyCon2Forget("nothing"); !errors.Is(err, ErrJoyCon2Unknown) {
		t.Errorf("Forget of an unknown serial: %v", err)
	}
	if err := s.JoyCon2Forget(serL1); err != nil {
		t.Fatal(err)
	}
	evs = b.events()
	if len(evs) != 1 || evs[0].Kind != DeviceRemoved || evs[0].Device != dev {
		t.Fatalf("events %v, want the removal", descs(evs))
	}
	if d := b.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{id}) {
		t.Errorf("disconnects %v", d)
	}

	// Cancel, and set the list.
	if err := s.JoyCon2Pair(time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.JoyCon2CancelPair(); err != nil {
		t.Fatal(err)
	}
	if err := s.JoyCon2SetKnown(bleKnown); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.JoyCon2Status(); st.WindowOpen || len(st.Known) != 2 {
		t.Fatalf("status %+v", st)
	}
	b.connect(addrR1, jc2.SideR, serR1) // remembered and auto-connect on
	if evs = b.events(); len(evs) != 1 || evs[0].Info.Type != "joyconright" {
		t.Fatalf("events %v", descs(evs))
	}
	if err := s.JoyCon2Disconnect(evs[0].Device); err != nil {
		t.Fatal(err)
	}
	if evs = b.events(); len(evs) != 1 || evs[0].Kind != DeviceRemoved {
		t.Fatalf("events %v after Disconnect", descs(evs))
	}
}

func TestSourceJoyCon2WatchdogOnTheLoop(t *testing.T) {
	b := newBLESource(t, &JoyCon2Options{Known: bleKnown, AutoConnect: true}, nil)
	id := b.connect(addrL1, jc2.SideL, serL1)
	b.events()
	b.report(id, bLLeft)
	b.events()
	b.advance(jc2.DefaultSilence)
	if evs := b.events(); len(evs) != 1 || evs[0].Kind != DeviceRemoved {
		t.Fatalf("events %v, want the removal after the silence", descs(evs))
	}
	b.report(id, bLLeft)
	evs := b.events()
	if len(evs) != 2 || evs[0].Kind != DeviceAdded || evs[1].Kind != ButtonDown {
		t.Fatalf("events %v, want the gamepad again with its press", descs(evs))
	}
	b.advance(jc2.DefaultLinkGiveUp)
	b.advance(0)
	if d := b.tr.disconnectList(); len(d) == 0 {
		t.Error("the silent link was not closed")
	}
}

func TestSourceJoyCon2DeviceMethods(t *testing.T) {
	b := newBLESource(t, &JoyCon2Options{Known: bleKnown, AutoConnect: true}, nil)
	s := b.s
	id := b.connect(addrL1, jc2.SideL, serL1)
	evs := b.events()
	if len(evs) != 1 {
		t.Fatalf("events %v", descs(evs))
	}
	dev := evs[0].Device
	b.report(id, bLLeft)
	b.events()
	b.tr.push(reportEvent(id, 2, 0, 2048, 2048, 7<<2|0x02))
	b.pump()

	// Effects: nothing is driven and nothing fails.
	if err := s.SetFeedback(Feedback{Device: dev, Color: "#ff0000", PlayerMode: 1, Pulse: true, Pulses: 2, PulseMs: 50, GapMs: 50, Strength: 1000}); err != nil {
		t.Errorf("SetFeedback: %v", err)
	}
	if err := s.SetFeedback(Feedback{Color: "#00ff00"}); err != nil {
		t.Errorf("SetFeedback for all: %v", err)
	}
	if err := s.Play(Effect{Device: dev, Class: haptic.Event, Preset: haptic.Tick}); err != nil {
		t.Errorf("Play: %v", err)
	}
	if err := s.Play(Effect{Device: dev, Class: haptic.Alert, Blink: true}); err != nil {
		t.Errorf("Play with a blink: %v", err)
	}
	if err := s.SetLight(Light{Device: dev, Class: haptic.Script, Color: haptic.RGB{1, 0, 0}}); err != nil {
		t.Errorf("SetLight: %v", err)
	}
	if err := s.Stop(Stop{Device: dev, Class: haptic.Script, Everything: true}); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if cues, err := s.Identify(dev); err != nil || cues != (haptic.Cues{}) {
		t.Errorf("Identify = %+v, %v; want no cues", cues, err)
	}
	// A request that is wrong is still wrong.
	if err := s.Play(Effect{Device: dev, Class: haptic.Ambient}); err == nil {
		t.Error("Play accepted the ambient class")
	}
	if err := s.Play(Effect{Class: haptic.Event, Preset: haptic.Tick}); err == nil {
		t.Error("Play accepted device 0")
	}
	// The Mixer never got the device.
	_ = s.do(func() error {
		if s.mix != nil && s.mix.Has(haptic.Device(dev)) {
			t.Error("a Joy-Con 2 is registered with the effect Mixer")
		}
		return nil
	})

	// Power and the rest.
	ro, err := s.Readout(dev)
	if err != nil || ro.Power != (Power{PowerCharging, 80}) || ro.Connection != "wireless" || ro.PlayerIndex != 0 {
		t.Errorf("Readout = %+v, %v", ro, err)
	}
	if _, err := s.RawStart(dev); !errors.Is(err, ErrUnsupported) {
		t.Errorf("RawStart: %v, want ErrUnsupported", err)
	}
	if err := s.RawStop(dev); err != nil {
		t.Errorf("RawStop: %v", err)
	}
	if _, err := s.GamepadMapping(dev); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GamepadMapping: %v, want ErrUnsupported", err)
	}

	// An id that is not live gets what a gamepad that is gone gets.
	gone := dev + 100
	for name, err := range map[string]error{
		"Play":     s.Play(Effect{Device: gone, Class: haptic.Event, Preset: haptic.Tick}),
		"SetLight": s.SetLight(Light{Device: gone, Class: haptic.Script, Release: true}),
		"Stop":     s.Stop(Stop{Device: gone, Class: haptic.Event}),
	} {
		if !errors.Is(err, ErrNoDevice) {
			t.Errorf("%s of an unknown id: %v, want ErrNoDevice", name, err)
		}
	}
	if _, err := s.Identify(gone); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Identify of an unknown id: %v", err)
	}
	if _, err := s.RawStart(gone); !errors.Is(err, ErrNoDevice) {
		t.Errorf("RawStart of an unknown id: %v", err)
	}
	if _, err := s.Readout(gone); err == nil {
		t.Error("Readout of an unknown id succeeded")
	}
	// And so does the id of a Joy-Con 2 that was removed.
	b.lostLink(id)
	if err := s.Play(Effect{Device: dev, Class: haptic.Event, Preset: haptic.Tick}); !errors.Is(err, ErrNoDevice) {
		t.Errorf("Play on a removed Joy-Con 2: %v", err)
	}
}

func (b *bleSource) lostLink(id jc2win.LinkID) {
	b.tr.push(jc2win.Event{Kind: jc2win.Lost, Link: id})
	b.pump()
}

func TestSourceJoyCon2ReenumerateSnapshot(t *testing.T) {
	var w *ComboWatch
	b := newBLESource(t, &JoyCon2Options{Known: bleKnown, AutoConnect: true}, func(s *Source) {
		s.onCombo = func() {}
		s.watch = NewComboWatch(s.comboPick())
		w = s.watch
	})
	s := b.s
	l := b.connect(addrL1, jc2.SideL, serL1)
	rr := b.connect(addrR1, jc2.SideR, serR1)
	b.report(l, bLMinus)
	b.report(rr, bRA)
	b.events()
	// A resume scan publishes a complete state of the live gamepad.
	if err := s.do(func() error { s.reenumerateJoyCon2(); return nil }); err != nil {
		t.Fatal(err)
	}
	evs := b.events()
	if len(evs) != 1 || evs[0].Kind != DeviceState || evs[0].Device != joyCon2IDBase+1 {
		t.Fatalf("events %v, want one DeviceState of the pair", descs(evs))
	}
	snap := evs[0].Snapshot
	got := slices.Clone(snap.Buttons)
	slices.Sort(got)
	if !slices.Equal(got, []string{"back", "east"}) || len(snap.Axes) != 6 {
		t.Errorf("snapshot %+v", snap)
	}
	// The link died during the sleep: it is gone before the snapshot is made.
	b.tr.push(jc2win.Event{Kind: jc2win.Lost, Link: rr})
	if err := s.do(func() error { s.reenumerateJoyCon2(); return nil }); err != nil {
		t.Fatal(err)
	}
	evs = b.events()
	if k := evKinds(evs); !slices.Equal(k, []EventKind{DeviceRemoved, DeviceAdded, ButtonDown, DeviceState}) {
		t.Fatalf("events %v, want the pair gone, the left one back and its state", descs(evs))
	}
	_ = w
}

func evKinds(evs []Event) []EventKind {
	out := make([]EventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// TestSourceJoyCon2Combo drives the emergency combo through the Source's
// emit: the events of a Joy-Con 2 reach the combo watch like any device's.
func TestSourceJoyCon2Combo(t *testing.T) {
	b := newBLESource(t, &JoyCon2Options{Known: bleKnown, AutoConnect: true}, func(s *Source) {
		s.comboHold = 100 * time.Millisecond
		s.onCombo = func() {}
	})
	s := b.s
	var combos atomic.Int32
	_ = s.do(func() error {
		s.onCombo = func() { combos.Add(1) }
		s.watch = NewComboWatch(s.comboPick())
		return nil
	})
	id := b.connect(addrL1, jc2.SideL, serL1)
	b.events()
	b.report(id, bSL|bSR)
	b.advance(150 * time.Millisecond) // the pump alone fires nothing: the loop's fireDue does
	_ = s.do(func() error {
		if s.armed() {
			s.fireDue(uint64(b.clock.Load()))
		}
		return nil
	})
	if combos.Load() != 1 {
		t.Fatalf("OnCombo called %d times, want once", combos.Load())
	}
	var held int
	for _, e := range b.events() {
		if e.Kind == ComboHeld {
			held++
			if e.Device < joyCon2IDBase {
				t.Errorf("ComboHeld for device %d", e.Device)
			}
		}
	}
	if held != 1 {
		t.Errorf("%d ComboHeld events, want one", held)
	}
}

func TestSourceJoyCon2Shutdown(t *testing.T) {
	b := newBLESource(t, &JoyCon2Options{Known: bleKnown, AutoConnect: true}, nil)
	l := b.connect(addrL1, jc2.SideL, serL1)
	b.connect(addrR1, jc2.SideR, serR1)
	b.report(l, bLL)
	b.events()
	b.s.RequestClose()
	<-b.s.done
	if !b.tr.isClosed() {
		t.Fatal("the transport was not closed")
	}
	evs := b.events()
	if len(evs) != 1 || evs[0].Kind != DeviceRemoved {
		t.Fatalf("events %v at shutdown, want the live gamepad removed", descs(evs))
	}
	if err := b.s.JoyCon2Pair(0); !errors.Is(err, ErrClosed) {
		t.Errorf("JoyCon2Pair after Close: %v, want ErrClosed", err)
	}
}
