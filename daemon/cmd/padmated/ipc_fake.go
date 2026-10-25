package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// rpcInputSource is a single synthetic controller, never an OS virtual pad.
// It shares the production router/slots/output lifecycle but is dry-run only.
// Calls from different clients serialize state updates and event publication.
type rpcInputSource struct {
	mu           sync.Mutex
	events       chan input.Event
	closed       bool
	buttons      map[string]bool
	axes         map[string]int16
	combo        *input.ComboWatch
	onCombo      func()
	timer        *time.Timer
	timerSeq     uint64
	pendingCombo *input.Event
	// caps is what the fake controller says it can do (Identify's cues), and
	// effects are the effect calls it received. The fake never reaches a real
	// controller: tests and the fake input daemon read what was asked of it.
	caps    input.Capabilities
	effects effectLog
}

func newRPCInputSource(opts input.Options) *rpcInputSource {
	return newRPCInputSourceWith(opts, defaultFakeCaps)
}

// newRPCInputSourceWith is newRPCInputSource for a fake gamepad that can do
// caps.
func newRPCInputSourceWith(opts input.Options, caps input.Capabilities) *rpcInputSource {
	s := &rpcInputSource{events: make(chan input.Event, 256), buttons: map[string]bool{}, axes: map[string]int16{}}
	pick := opts.ComboFor
	if pick == nil {
		pick = input.DefaultCombo
	}
	s.combo, s.onCombo = input.NewComboWatch(func(info input.DeviceInfo) input.Combo { return pick(info, false) }), opts.OnCombo
	for a := control.LeftX; a <= control.RightTrigger; a++ {
		s.axes[a.String()] = 0
	}
	info := input.DeviceInfo{ID: 1, Name: "padmate IPC fake gamepad", Type: "standard", GUID: "ff007061646d61746566616b65007600", Driver: "virtual", IsGamepad: true, Virtual: true,
		ButtonMask: (1 << 26) - 1, AxisMask: (1 << 6) - 1, Capabilities: caps}
	s.caps = info.Capabilities
	added := input.Event{Time: uint64(clock.Real().Now()), Kind: input.DeviceAdded, Device: 1, Info: &info}
	s.events <- added
	s.combo.Observe(added)
	return s
}

func (s *rpcInputSource) Events() <-chan input.Event { return s.events }
func (*rpcInputSource) Dropped() uint64              { return 0 }
func (*rpcInputSource) Err() error                   { return nil }

// SetFeedback, Play and Identify record the call, since the fake controller
// has nothing to buzz or light.
func (s *rpcInputSource) SetFeedback(f input.Feedback) error {
	s.effects.add(effectCall{Kind: "feedback", Device: f.Device, Feedback: f})
	return nil
}

func (s *rpcInputSource) Play(e input.Effect) error {
	s.effects.add(effectCall{Kind: "play", Device: e.Device, Effect: e})
	return nil
}

func (s *rpcInputSource) SetLight(l input.Light) error {
	s.effects.add(effectCall{Kind: "light", Device: l.Device, Light: l})
	return nil
}

func (s *rpcInputSource) Stop(st input.Stop) error {
	s.effects.add(effectCall{Kind: "stop", Device: st.Device, Stop: st})
	return nil
}

// Identify finds the one fake controller, device 1, and says what its
// capabilities allow.
func (s *rpcInputSource) Identify(id input.DeviceID) (haptic.Cues, error) {
	if id != 1 {
		s.effects.add(effectCall{Kind: "identify", Device: id})
		return haptic.Cues{}, input.ErrNoDevice
	}
	cues := s.caps.Cues()
	s.effects.add(effectCall{Kind: "identify", Device: id, Cues: cues})
	return cues, nil
}

func (*rpcInputSource) WriteHeader(w io.Writer, prefix, _ string) error {
	_, err := fmt.Fprintln(w, prefix+"IPC fake input; no SDL devices opened")
	return err
}
func (s *rpcInputSource) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.timerSeq++
		if s.timer != nil {
			s.timer.Stop()
		}
		close(s.events)
	}
}

func (s *rpcInputSource) emit(e input.Event) error {
	if s.closed {
		return errors.New("fake input is closed")
	}
	if e.Time == 0 {
		e.Time = uint64(clock.Real().Now())
	}
	// T-input stamps the edges of a real gamepad as SDL returns them (DMN-15); the
	// fake gamepad stamps them as they are queued, so that the E2E measures too.
	if e.Stamp == 0 && (e.Kind == input.ButtonDown || e.Kind == input.ButtonUp || e.Kind == input.AxisMotion) {
		e.Stamp = latency.Now()
	}
	e.Device = 1
	select {
	case s.events <- e:
		return nil
	default:
		return errors.New("fake input queue is full; retry the event")
	}
}

// power reports p as the power of the fake gamepad, as T-input reports a real
// one's: a DevicePower event. The fake source has no battery of its own (it does
// not send them by itself), so this is how a test or the E2E sets one.
func (s *rpcInputSource) power(p input.Power) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emit(input.Event{Kind: input.DevicePower, Power: &p})
}

func (s *rpcInputSource) button(name string, down bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buttons[name] == down {
		return nil
	}
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	e := input.Event{Time: uint64(clock.Real().Now()), Device: 1, Kind: kind, Control: name}
	if err := s.emit(e); err != nil {
		return err
	}
	if down {
		s.buttons[name] = true
	} else {
		delete(s.buttons, name)
	}
	s.combo.Observe(e)
	s.armCombo()
	return nil
}

// Timers share the source lock with input publication. A full queue retains
// the combo and retries; the safety callback blocks output immediately.
func (s *rpcInputSource) armCombo() {
	s.timerSeq++
	seq := s.timerSeq
	if s.timer != nil {
		s.timer.Stop()
	}
	due, ok := s.combo.Due()
	wait := time.Duration(due) - time.Duration(clock.Real().Now())
	if s.pendingCombo != nil {
		wait, ok = time.Millisecond, true
	}
	if !ok || s.closed {
		return
	}
	s.timer = time.AfterFunc(max(0, wait), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || seq != s.timerSeq {
			return
		}
		if s.pendingCombo == nil {
			if events := s.combo.Fire(uint64(clock.Real().Now())); len(events) > 0 {
				s.pendingCombo = &events[0]
				if s.onCombo != nil {
					s.onCombo()
				}
			}
		}
		if s.pendingCombo != nil && s.emit(*s.pendingCombo) == nil {
			s.pendingCombo = nil
		}
		s.armCombo()
	})
}

// RepickCombos picks the fake gamepad's combo again, as T-input does for
// a real one (comboSource); its single Joy-Con grip is never vertical.
func (s *rpcInputSource) RepickCombos() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("fake input is closed")
	}
	s.combo.Repick()
	s.armCombo()
	return nil
}

func (*rpcInputSource) VerticalJoyCons() bool { return false }

func (s *rpcInputSource) axis(name string, value int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.axes[name] == value {
		return nil
	}
	if err := s.emit(input.Event{Kind: input.AxisMotion, Control: name, Value: value}); err != nil {
		return err
	}
	s.axes[name] = value
	return nil
}

func (s *rpcInputSource) Reenumerate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	buttons := slices.Sorted(maps.Keys(s.buttons))
	return s.emit(input.Event{Kind: input.DeviceState, Snapshot: &input.GamepadState{Buttons: buttons, Axes: maps.Clone(s.axes)}})
}

// fakeInput returns the fake gamepad's source, or -32010 when padmated does
// not run with --fake-input and --dry-run.
func (r *liveRun) fakeInput() (*rpcInputSource, error) {
	src, ok := r.src.(*rpcInputSource)
	if !ok || r.cfg.opts.fakeInput != "ipc" || !r.cfg.opts.dryRun || r.cfg.opts.inject {
		return nil, &jrpc2.Error{Code: -32010, Message: "fake input requires --fake-input --dry-run"}
	}
	return src, nil
}

func (r *liveRun) fakeMethods(methods handler.Map) {
	r.fakeEffectsMethod(methods)
	r.fakeBatteryMethod(methods)
	for _, method := range []string{"fake.press", "fake.release", "fake.axis"} {
		methods[method] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
			src, err := r.fakeInput()
			if err != nil {
				return nil, err
			}
			if method == "fake.axis" {
				var p struct {
					Axis  string `json:"axis"`
					Value *int32 `json:"value"`
				}
				if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
					return nil, err
				}
				a, valid := control.ParseAxis(p.Axis)
				if !valid || p.Value == nil || *p.Value < -32768 || *p.Value > 32767 || a >= control.LeftTrigger && *p.Value < 0 {
					return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "axis requires a raw axis and integer value (-32768..32767; triggers 0..32767)"}
				}
				err = src.axis(a.String(), int16(*p.Value))
			} else {
				var p struct {
					Control string `json:"control"`
				}
				if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
					return nil, err
				}
				c, valid := control.Parse(p.Control)
				if !valid || c > control.Touchpad {
					return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "control must be a physical gamepad button; use fake.axis for triggers and sticks"}
				}
				err = src.button(c.String(), method == "fake.press")
			}
			if err != nil {
				return nil, err
			}
			if err := r.sup.Sync(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"device": 1, "accepted": true}, nil
		}
	}
}
