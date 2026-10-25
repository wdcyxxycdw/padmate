package input

import (
	"errors"
	"fmt"

	"github.com/Zyko0/go-sdl3/sdl"
)

// The raw joystick stream (IN-17, ADR-0072). SDL gives a joystick that it
// has no gamepad mapping for no gamepad events, and the controls of a
// gamepad are the ones of its mapping. The mapping wizard and "padmatectl
// monitor --raw" need the elements as the device has them: buttons, axes and
// hats by index. So a device that a caller asked for with RawStart sends
// RawEvents, on a channel of their own: they never reach Events, no slot or
// combo watch sees them, and traces do not record them.
//
// A device is opened as a joystick only while it is asked for. SDL counts
// the references to a joystick, so this does not disturb the gamepad that
// padmate opens on the same device.

// RawKind is the kind of a RawEvent.
type RawKind uint8

// The kinds of raw events.
const (
	RawButton RawKind = iota + 1
	RawAxis
	RawHat
)

// String returns "button", "axis" or "hat".
func (k RawKind) String() string {
	switch k {
	case RawButton:
		return "button"
	case RawAxis:
		return "axis"
	case RawHat:
		return "hat"
	}
	return fmt.Sprintf("rawkind%d", uint8(k))
}

// RawEvent is a change of one element of a joystick, by index.
type RawEvent struct {
	// Time is SDL's event timestamp (SDL_GetTicksNS), as Event.Time is.
	Time   uint64
	Device DeviceID
	Kind   RawKind
	// Index is the index of the button, axis or hat on the joystick.
	Index uint8
	// Value is 1 (down) or 0 (up) for a button, the position -32768..32767
	// for an axis, and the direction mask for a hat: up 1, right 2, down 4,
	// left 8, 0 for centered.
	Value int16
}

// RawInfo describes the elements of a joystick that RawStart opened.
type RawInfo struct {
	Device DeviceID
	GUID   string
	// Gamepad is true when SDL has a mapping for the device now.
	Gamepad             bool
	Axes, Buttons, Hats int
	// AxisRest is the rest value of each axis, as SDL_GetJoystickAxisInitialState
	// gives it (0 when it gives none).
	AxisRest []int16
}

// rawBuffer is the capacity of the channel of raw events. T-input never
// waits for it: when it is full an event is dropped and counted (RawDropped).
const rawBuffer = 256

// rawAxisInterval is the least time between two raw events of one axis, in
// nanoseconds: 8 ms, 125 a second. The newest value of an axis that moved
// meanwhile is sent when the time is up, so the last position always comes
// out, which the wizard needs to see an axis come back.
const rawAxisInterval = 8_000_000

// rawWatch is a device asked for with RawStart. T-input owns it.
type rawWatch struct {
	js   *sdl.Joystick // our reference
	axes []rawAxis
}

// rawAxis is the state of the rate limit of one axis.
type rawAxis struct {
	has     bool // a value was sent
	last    int16
	sentAt  uint64 // SDL ns of the last send
	pending bool   // a newer value waits for the time to be up
	value   int16
	at      uint64 // SDL ns of the pending value
}

// RawEvents returns the raw events of the devices asked for with RawStart.
// The channel is closed by Close.
func (s *Source) RawEvents() <-chan RawEvent { return s.rawEvents }

// RawDropped returns how many raw events were lost because RawEvents was
// full.
func (s *Source) RawDropped() uint64 { return s.rawDropped.Load() }

// RawStart asks for the raw events of device id, a joystick or a gamepad,
// and returns what its elements are. Asking again returns the same.
func (s *Source) RawStart(id DeviceID) (RawInfo, error) {
	var info RawInfo
	err := s.do(func() error {
		d := s.byPub(id)
		if d == nil || d.dormant {
			return fmt.Errorf("%w: %d", ErrNoDevice, id)
		}
		if d.raw == nil {
			js, err := d.id.OpenJoystick()
			if err != nil {
				return fmt.Errorf("SDL_OpenJoystick: %w", err)
			}
			axes, err1 := js.NumAxes()
			_, err2 := js.NumButtons()
			_, err3 := js.NumHats()
			if err := errors.Join(err1, err2, err3); err != nil {
				js.Close()
				return err
			}
			d.raw = &rawWatch{js: js, axes: make([]rawAxis, axes)}
			s.rawOn++
		}
		info = s.rawInfo(d)
		return nil
	})
	return info, err
}

// RawStop ends the raw events of device id. It does nothing for a device
// that is not asked for, or gone.
func (s *Source) RawStop(id DeviceID) error {
	err := s.do(func() error {
		if d := s.byPub(id); d != nil {
			s.rawClose(d)
		}
		return nil
	})
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// byPub returns the device whose events carry the id id.
func (s *Source) byPub(id DeviceID) *device {
	for _, d := range s.devices {
		if d.pub == id {
			return d
		}
	}
	return nil
}

// rawInfo describes the elements of d, which is asked for.
func (s *Source) rawInfo(d *device) RawInfo {
	js := d.raw.js
	info := RawInfo{Device: d.pub, Gamepad: d.kind != kindRaw}
	if g, ok := deviceGUID(d.id); ok {
		info.GUID = g.String()
	}
	n, _ := js.NumAxes()
	info.Axes = int(n)
	n, _ = js.NumButtons()
	info.Buttons = int(n)
	n, _ = js.NumHats()
	info.Hats = int(n)
	info.AxisRest = make([]int16, info.Axes)
	for a := range info.AxisRest {
		if v, has := js.AxisInitialState(int32(a)); has {
			info.AxisRest[a] = v
		}
	}
	return info
}

// rawClose ends the raw events of d.
func (s *Source) rawClose(d *device) {
	r := d.raw
	if r == nil {
		return
	}
	for i := range r.axes {
		if r.axes[i].pending {
			s.rawPending--
		}
	}
	r.js.Close()
	d.raw = nil
	s.rawOn--
}

// sendRaw delivers e without blocking.
func (s *Source) sendRaw(e RawEvent) {
	select {
	case s.rawEvents <- e:
	default:
		s.rawDropped.Add(1)
	}
}

// rawButton handles a joystick button event.
func (s *Source) rawButton(id sdl.JoystickID, button uint8, down bool, t uint64) {
	d := s.devices[id]
	if d == nil || d.raw == nil {
		return
	}
	v := int16(0)
	if down {
		v = 1
	}
	s.sendRaw(RawEvent{Time: t, Device: d.pub, Kind: RawButton, Index: button, Value: v})
}

// rawHat handles a joystick hat event.
func (s *Source) rawHat(id sdl.JoystickID, hat, value uint8, t uint64) {
	d := s.devices[id]
	if d == nil || d.raw == nil {
		return
	}
	s.sendRaw(RawEvent{Time: t, Device: d.pub, Kind: RawHat, Index: hat, Value: int16(value)})
}

// rawAxis handles a joystick axis event: it sends the value now if the axis
// has been quiet for rawAxisInterval, and otherwise keeps it to send when
// the time is up (flushRaw), the newest value replacing any it has kept. A
// value equal to the one last sent is dropped, which includes the first value
// that SDL sends twice.
func (s *Source) rawAxis(id sdl.JoystickID, axis uint8, value int16, t uint64) {
	d := s.devices[id]
	if d == nil || d.raw == nil || int(axis) >= len(d.raw.axes) {
		return
	}
	a := &d.raw.axes[axis]
	if a.has && a.last == value {
		if a.pending { // it came back to what the consumer has
			a.pending = false
			s.rawPending--
		}
		return
	}
	if !a.has || t >= a.sentAt+rawAxisInterval {
		if a.pending {
			a.pending = false
			s.rawPending--
		}
		a.has, a.last, a.sentAt = true, value, t
		s.sendRaw(RawEvent{Time: t, Device: d.pub, Kind: RawAxis, Index: axis, Value: value})
		return
	}
	if !a.pending {
		a.pending = true
		s.rawPending++
	}
	a.value, a.at = value, t
}

// flushRaw sends the values that axes kept, those whose time is up at now
// (SDL_GetTicksNS). It does nothing without any kept.
func (s *Source) flushRaw() {
	if s.rawPending == 0 {
		return
	}
	now := s.ticks()
	for _, d := range s.devices {
		if d.raw == nil {
			continue
		}
		for i := range d.raw.axes {
			a := &d.raw.axes[i]
			if !a.pending || now < a.sentAt+rawAxisInterval {
				continue
			}
			a.pending = false
			s.rawPending--
			if a.value == a.last {
				continue
			}
			a.last, a.sentAt = a.value, now
			s.sendRaw(RawEvent{Time: a.at, Device: d.pub, Kind: RawAxis, Index: uint8(i), Value: a.value})
		}
	}
}
