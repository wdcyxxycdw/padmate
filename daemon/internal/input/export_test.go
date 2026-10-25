package input

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// FakePower makes T-input read state and percent as the power of device id
// from now on, in place of SDL's PowerInfo, so that a test can see a read
// change the value.
func (s *Source) FakePower(id DeviceID, state sdl.PowerState, percent int32) error {
	return s.do(func() error {
		for _, d := range s.devices {
			if d.pub == id && d.powerFn != nil {
				d.powerFn = func() (sdl.PowerState, int32) { return state, percent }
				return nil
			}
		}
		return fmt.Errorf("no open gamepad %d", id)
	})
}

// AddEffectDevice adds a made-up gamepad with the public id id to the Source,
// with e to drive its effects and caps to say what it can do. SDL never sends
// an event for it, so only effect requests reach it, and the Source's
// shutdown stops it. The Mixer of a virtual gamepad has nothing to drive (it
// has no rumble or LED), so this is how a test watches what the Source does
// with effects.
func (s *Source) AddEffectDevice(id DeviceID, e haptic.Effector, caps haptic.Caps) error {
	return s.do(func() error {
		s.devices[sdl.JoystickID(id)] = &device{id: sdl.JoystickID(id), pub: id}
		s.effects().Add(haptic.Device(id), e, caps)
		return nil
	})
}

// DetachThenSetPlayerIndex detaches the virtual gamepad v and, in the same
// T-input call, so that the Source still holds the gamepad that SDL has just
// removed, writes the player index index through the gamepad's Effector, as the
// Mixer would for a gamepad whose removal it has not seen yet. SDL clears the
// player-index entry of a joystick it removes (SDL_PrivateJoystickRemoved), so
// the write must do nothing.
func (s *Source) DetachThenSetPlayerIndex(v *Virtual, index int) error {
	return s.do(func() error {
		d := s.devices[v.id]
		if d == nil || d.gp == nil {
			return fmt.Errorf("no open gamepad %d", v.id)
		}
		if err := s.detach(v); err != nil {
			return err
		}
		return gamepadEffector{d.gp, d.id}.SetPlayerIndex(index)
	})
}

// AttachVirtualJoystick attaches a virtual joystick that SDL has no
// gamepad mapping for, to test joystick-only devices. Unlike AttachVirtual
// it returns before the DeviceAdded event, which T-input sends once it
// sees SDL_EVENT_JOYSTICK_ADDED.
func (s *Source) AttachVirtualJoystick(name string) (*Virtual, error) {
	return s.attachVirtual(VirtualDesc{Name: name}, sdl.JOYSTICK_TYPE_UNKNOWN)
}

// AttachGone attaches a virtual joystick called name, a gamepad or a
// joystick that SDL has no mapping for, and detaches it at once on
// T-input without opening it, so that T-input handles its ADDED events
// after it is gone. It returns its id.
func (s *Source) AttachGone(name string, gamepad bool) (DeviceID, error) {
	typ := sdl.JOYSTICK_TYPE_UNKNOWN
	if gamepad {
		typ = sdl.JOYSTICK_TYPE_GAMEPAD
	}
	var id sdl.JoystickID
	err := s.do(func() error {
		var err error
		id, err = sdl.AttachVirtualJoystick(&sdl.VirtualJoystickDesc{
			Version:  virtualDescVersion,
			Type:     typ,
			Naxes:    uint16(sdl.GAMEPAD_AXIS_COUNT),
			Nbuttons: uint16(sdl.GAMEPAD_BUTTON_COUNT),
			Name:     name,
		})
		if err != nil {
			return fmt.Errorf("SDL_AttachVirtualJoystick: %w", err)
		}
		if err := id.DetachVirtualJoystick(); err != nil {
			return fmt.Errorf("SDL_DetachVirtualJoystick: %w", err)
		}
		return nil
	})
	return DeviceID(id), err
}

// Gone returns how many adds T-input has not reported because the device
// was already gone.
func (s *Source) Gone() (int, error) {
	var n int
	err := s.do(func() error {
		n = s.gone
		return nil
	})
	return n, err
}

// SidewaysL is the internal tests' copy of inputtest.JoyConLeft's buttons.
var SidewaysL = sidewaysL

// VirtualGUID returns the GUID that SDL gives a virtual gamepad named
// name.
func VirtualGUID(name string) string {
	return makeGUID(busVirtual, 0, 0, 0, name, 'v', byte(sdl.JOYSTICK_TYPE_GAMEPAD)).String()
}

// VirtualDescGUID returns the GUID that SDL gives a virtual gamepad named
// name with the USB ids vendor and product.
func VirtualDescGUID(name string, vendor, product uint16) string {
	return makeGUID(busVirtual, vendor, product, 0, name, 'v', byte(sdl.JOYSTICK_TYPE_GAMEPAD)).String()
}

// DisconnectedKeys returns the keys of the disconnected gamepads that r
// remembers, the earliest disconnected first.
func (r *Registry) DisconnectedKeys() []DeviceKey {
	return append([]DeviceKey(nil), r.disconnected...)
}

// Filler is the event that FillEvents puts on Events; its Device is 0,
// which SDL gives no device.
var Filler = Event{Kind: ButtonDown, Control: "filler"}

// FillEvents fills Events with Filler on T-input, as a consumer that
// stopped reading would leave it, and returns how many it added. It fails
// while events wait in the backlog, which fillers would overtake.
func (s *Source) FillEvents() (int, error) {
	n := 0
	err := s.do(func() error {
		if len(s.backlog) > 0 {
			return fmt.Errorf("%d events wait in the backlog", len(s.backlog))
		}
		for {
			select {
			case s.events <- Filler:
				n++
			default:
				return nil
			}
		}
	})
	return n, err
}

// Panic panics with v on T-input, as a bug there would, and returns what
// the call on T-input returns then.
func (s *Source) Panic(v any) error { return s.do(func() error { panic(v) }) }

// Goexit calls runtime.Goexit on T-input.
func (s *Source) Goexit() error {
	return s.do(func() error {
		runtime.Goexit()
		return nil
	})
}

// ErrDetached and ErrNoHIDBinding are the internal tests' copies of the
// errors of a detached Virtual and of a missing SDL_hid_* binding.
var (
	ErrDetached     = errDetached
	ErrNoHIDBinding = errNoHIDBinding
)

// ReceiverProve returns how long idle heartbeats must come at rest to arm
// the receiver watchdog with the timeout ReceiverTimeout.
func ReceiverProve(timeout time.Duration) time.Duration {
	tm, err := newReceiverTiming(timeout)
	if err != nil {
		panic(err)
	}
	return time.Duration(tm.prove)
}

// ReceiverStatus is the receiver watchdog's state of a device.
type ReceiverStatus struct {
	// Watched is set while the device has a heartbeat source.
	Watched bool
	// Armed and Dormant are the watchdog's.
	Armed, Dormant bool
	// Pub is the id its events carry now.
	Pub DeviceID
}

// ReceiverState returns the receiver watchdog's state of the gamepad v,
// read on T-input.
func (s *Source) ReceiverState(v *Virtual) (ReceiverStatus, error) {
	var st ReceiverStatus
	err := s.do(func() error {
		d := s.devices[v.id]
		if d == nil {
			return fmt.Errorf("device %d is not reported", v.id)
		}
		st = ReceiverStatus{Watched: d.beat != nil, Armed: d.beat != nil && d.dog.armed, Dormant: d.dormant, Pub: d.pub}
		return nil
	})
	return st, err
}

// Report queues the raw HID report p, at most 64 bytes, for the heartbeat
// source of v, as Beat queues its reports.
func (v *Virtual) Report(p []byte) error {
	var r virtualReport
	if len(p) > len(r.b) {
		return fmt.Errorf("a report of %d bytes, want at most %d", len(p), len(r.b))
	}
	r.n = uint8(copy(r.b[:], p))
	return v.report(r)
}

// SetAxes moves the axes named in values, as SetAxis moves one, in one
// update: SDL then queues all their events at once, as it does for the
// axes that one report of a real gamepad moves.
func (v *Virtual) SetAxes(values map[string]int16) error {
	type move struct {
		j   int32
		raw int16
	}
	var moves []move
	for control, value := range values {
		a, ok := axisIndex(control)
		if !ok || v.axes[a] < 0 {
			return fmt.Errorf("input: the virtual gamepad %q has no axis %q", v.name, control)
		}
		raw := value
		if isTrigger(a) {
			raw = triggerRaw(value)
		}
		moves = append(moves, move{int32(v.axes[a]), raw})
	}
	return v.set(func() error {
		for _, m := range moves {
			if err := v.js.SetVirtualAxis(m.j, m.raw); err != nil {
				return err
			}
		}
		return nil
	})
}

// FillEventBuffer creates backpressure without synthesizing SDL input.
func (s *Source) FillEventBuffer() error {
	return s.do(func() error {
		for {
			select {
			case s.events <- Event{Kind: AxisMotion}:
			default:
				return nil
			}
		}
	})
}

// SetStateWithoutEvents simulates state changing across sleep without SDL
// edges. It discards only this test virtual pad's queued gamepad events.
func (v *Virtual) SetStateWithoutEvents(buttons map[string]bool, axes map[string]int16) error {
	return v.s.do(func() error {
		if v.js == nil {
			return errDetached
		}
		for name, down := range buttons {
			b, ok := buttonIndex(name)
			if !ok || v.buttons[b] < 0 {
				return fmt.Errorf("unknown button %q", name)
			}
			if err := v.js.SetVirtualButton(int32(v.buttons[b]), down); err != nil {
				return err
			}
		}
		for name, value := range axes {
			a, ok := axisIndex(name)
			if !ok || v.axes[a] < 0 {
				return fmt.Errorf("unknown axis %q", name)
			}
			if isTrigger(a) {
				value = triggerRaw(value)
			}
			if err := v.js.SetVirtualAxis(int32(v.axes[a]), value); err != nil {
				return err
			}
		}
		sdl.UpdateJoysticks()
		var ev sdl.Event
		for sdl.PollEvent(&ev) {
			if (ev.Type == sdl.EVENT_GAMEPAD_BUTTON_DOWN || ev.Type == sdl.EVENT_GAMEPAD_BUTTON_UP) && ev.GamepadButtonEvent().Which == v.id {
				continue
			}
			if ev.Type == sdl.EVENT_GAMEPAD_AXIS_MOTION && ev.GamepadAxisEvent().Which == v.id {
				continue
			}
			v.s.handle(&ev)
		}
		return nil
	})
}

// ReenumerateWithQueuedPress runs a reenumeration (Source.Reenumerate without
// the publication fence) on T-input while a press of the button control of
// the virtual gamepad waits in SDL's queue, and while Source.stamp still
// holds the reading stale of the last event the loop handled: what resuming
// from a sleep of hours looks like. The press is pushed here, on T-input, so
// that the loop cannot take it before the reenumeration does (a press set on
// the virtual joystick reaches the queue at a time that SDL decides). The
// queue is emptied first: SDL_PollEvent stops at the sentinel that an earlier
// poll left, so a press pushed behind it would wait for the next poll.
func (v *Virtual) ReenumerateWithQueuedPress(ctx context.Context, control string, stale latency.Stamp) error {
	return v.s.do(func() error {
		if v.js == nil {
			return errDetached
		}
		b, ok := buttonIndex(control)
		if !ok || v.buttons[b] < 0 {
			return fmt.Errorf("unknown button %q", control)
		}
		var ev sdl.Event
		for polled := true; polled; {
			polled = false
			for sdl.PollEvent(&ev) {
				polled = true
				v.s.handle(&ev)
			}
		}
		*(*sdl.GamepadButtonEvent)(unsafe.Pointer(&ev)) = sdl.GamepadButtonEvent{
			Type: sdl.EVENT_GAMEPAD_BUTTON_DOWN, Timestamp: sdl.TicksNS(), Which: v.id, Button: uint8(b), Down: true,
		}
		if err := sdl.PushEvent(&ev); err != nil {
			return err
		}
		v.s.stamp = stale
		return v.s.reenumerate(ctx)
	})
}

// HIDProbeResult is what HIDProbe found: SDL_hid_open_path's result, and
// SDL_hid_read_timeout's and SDL_hid_close's of the NULL device; the
// allocations of one SDL_hid_read_timeout call, and of one drain of a
// heartbeat source on the NULL device (hidSource), with its error.
type HIDProbeResult struct {
	Dev                     uintptr
	Read, Closed            int32
	ReadAllocs, DrainAllocs float64
	DrainErr                error
}

// HIDProbe calls the SDL_hid_* bindings on T-input: SDL_hid_open_path of
// path, then SDL_hid_read_timeout and SDL_hid_close of the NULL device,
// and counts the allocations of reading it. A device that opens is closed
// again. Only the Windows/amd64 bindings are meant to read without an
// allocation (sdlext_windows.go).
func (s *Source) HIDProbe(path string) (r HIDProbeResult, err error) {
	if hidOpenPath == nil || hidReadTimeout == nil || hidClose == nil {
		return r, errNoHIDBinding
	}
	err = s.do(func() error {
		r.Dev = hidOpenPath(path)
		if r.Dev != 0 {
			hidClose(r.Dev)
		}
		var buf [64]byte
		r.Read = hidReadTimeout(0, buf[:], 0)
		r.Closed = hidClose(0)
		r.ReadAllocs = testing.AllocsPerRun(100, func() { hidReadTimeout(0, buf[:], 0) })
		h := hidSource(0)
		r.DrainAllocs = testing.AllocsPerRun(100, func() { _, r.DrainErr = h.drain(0) })
		return nil
	})
	return r, err
}

// AttachRawJoystick attaches a virtual joystick that SDL has no gamepad
// mapping for, with axes, buttons and hats of its own (a DirectInput pad that
// SDL does not know), whose axes rest at rest (0 for an axis beyond it). Like
// AttachVirtualJoystick it returns before the DeviceAdded event.
func (s *Source) AttachRawJoystick(name string, axes, buttons, hats int, rest ...int16) (*Virtual, error) {
	return s.AttachRawVirtual(RawJoystickDesc{Name: name, Axes: axes, Buttons: buttons, Hats: hats, AxisRest: rest})
}

// VirtualJoystickGUID returns the GUID that SDL gives a virtual joystick
// without a gamepad layout named name (AttachRawVirtual).
func VirtualJoystickGUID(name string) string {
	return makeGUID(busVirtual, 0, 0, 0, name, 'v', byte(sdl.JOYSTICK_TYPE_UNKNOWN)).String()
}

// MappingsFile returns the path of the database file of Options.Mappings, ""
// without one.
func (s *Source) MappingsFile() (string, error) {
	var path string
	err := s.do(func() error {
		path = s.mapFile
		return nil
	})
	return path, err
}
