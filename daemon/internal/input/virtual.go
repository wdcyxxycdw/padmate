package input

import (
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"sync/atomic"

	"github.com/Zyko0/go-sdl3/sdl"
)

// virtualDescVersion is SDL_VirtualJoystickDesc's size on 64-bit systems,
// the value SDL_INIT_INTERFACE stores in its version field. SDL rejects a
// smaller one.
const virtualDescVersion = 136

// errDetached is returned by the methods of a detached Virtual.
var errDetached = errors.New("input: the virtual gamepad is detached")

// Virtual is an SDL virtual gamepad (AttachVirtual, AttachVirtualDesc). Its
// methods run on T-input and may be called from any goroutine.
type Virtual struct {
	s    *Source
	id   sdl.JoystickID
	js   *sdl.Joystick // owned by T-input; nil once detached
	name string
	// buttons and axes hold the joystick index of each gamepad button and
	// axis, or -1 for one the gamepad does not have (packIndex).
	buttons, axes [32]int8
	// reports queues the heartbeat reports of a gamepad with
	// VirtualDesc.Heartbeat, for T-input's heartbeat source (IN-30); it is
	// nil for another. timer is the timer of the next Beat; gone is set
	// when the gamepad is detached.
	reports chan virtualReport
	timer   atomic.Uint32
	gone    atomic.Bool
	// rawRest is the layout of a joystick that a test made without a gamepad
	// layout (VirtualDesc.raw), or nil.
	rawRest *rawDesc
}

// virtualReport is a HID input report of a virtual gamepad's heartbeat.
type virtualReport struct {
	n uint8
	b [64]byte
}

// virtualReports is the capacity of Virtual.reports, the input buffer of
// a HID handle on Windows; a report that does not fit is dropped.
const virtualReports = 64

// VirtualDesc describes a virtual gamepad for AttachVirtualDesc. It is
// meant for tests and "padmatectl record --virtual"; it models what SDL
// reports for a device, not the device.
type VirtualDesc struct {
	Name string
	// Vendor and Product are the USB ids that SDL reports and puts in the
	// GUID, or 0. SDL derives the gamepad type from them: 057e:2006 is
	// joyconleft (SDL_GetGamepadTypeFromVIDPID).
	//
	// SDL finds a virtual gamepad's mapping by its GUID without the CRC of
	// the name, and with a vendor the GUID holds no name bytes. So every
	// virtual gamepad with the same nonzero Vendor and Product in one
	// Source (Open to Close) gets the first one's mapping, its name and
	// controls included (SDL_gamepad.c). Give such gamepads the same Name
	// and controls.
	Vendor, Product uint16
	// Buttons and Axes name the controls the gamepad has, as ButtonName
	// and AxisName do. nil means every button or axis; an empty slice
	// means none.
	Buttons, Axes []string
	// Heartbeat gives the gamepad a heartbeat of HID reports, which Beat
	// sends, in place of the reports that T-input reads from a receiver's
	// device (IN-30). A Source with Options.ReceiverTimeout watches a
	// gamepad with it and the ids 057e:2009 (inputtest.SwitchPro) as it
	// watches a receiver. SDL sees nothing of the reports.
	Heartbeat bool

	// raw, when set, makes the joystick a plain one with these elements and no
	// gamepad masks (tests of raw joysticks, IN-17).
	raw *rawDesc
}

// rawDesc is the elements of a virtual joystick without a gamepad layout.
type rawDesc struct {
	axes, buttons, hats int
	// axisRest is the rest position of each axis; an axis beyond its length
	// rests at 0.
	axisRest []int16
}

// AttachVirtual adds a virtual gamepad called name, with every SDL gamepad
// button and axis, and returns once its DeviceAdded event is sent: on
// Events, or waiting in line for room there. Its buttons and axes are then
// set with SetButton and SetAxis, and their events take the same path as
// those of a real gamepad. Detach detaches it. It is
// AttachVirtualDesc(VirtualDesc{Name: name}).
func (s *Source) AttachVirtual(name string) (*Virtual, error) {
	return s.AttachVirtualDesc(VirtualDesc{Name: name})
}

// RawJoystickDesc describes a virtual joystick that SDL has no gamepad
// mapping for (AttachRawVirtual): a plain device with axes, buttons and hats
// of its own, such as a DirectInput pad that SDL does not know (IN-17, IN-18).
// It is meant for tests.
type RawJoystickDesc struct {
	Name                string
	Axes, Buttons, Hats int
	// AxisRest is the rest position of each axis; an axis beyond its length
	// rests at 0.
	AxisRest []int16
}

// AttachRawVirtual adds the virtual joystick d, which SDL maps to no gamepad
// until a mapping for its GUID is added (SetMappings). Unlike AttachVirtual it
// returns before the DeviceAdded event, which T-input sends once it sees
// SDL_EVENT_JOYSTICK_ADDED. The elements are set with SetRawButton, SetRawAxis
// and SetRawHat, by joystick index.
func (s *Source) AttachRawVirtual(d RawJoystickDesc) (*Virtual, error) {
	return s.attachVirtual(VirtualDesc{Name: d.Name, raw: &rawDesc{axes: d.Axes, buttons: d.Buttons, hats: d.Hats, axisRest: slices.Clone(d.AxisRest)}}, sdl.JOYSTICK_TYPE_UNKNOWN)
}

// SetRawButton presses or releases button i of a virtual joystick, by its
// joystick index.
func (v *Virtual) SetRawButton(i int, down bool) error {
	return v.set(func() error { return v.js.SetVirtualButton(int32(i), down) })
}

// SetRawAxis moves axis i of a virtual joystick, by its joystick index.
func (v *Virtual) SetRawAxis(i int, value int16) error {
	return v.set(func() error { return v.js.SetVirtualAxis(int32(i), value) })
}

// SetRawHat moves hat i of a virtual joystick to the direction mask (up 1,
// right 2, down 4, left 8).
func (v *Virtual) SetRawHat(i int, mask uint8) error {
	return v.set(func() error { return v.js.SetVirtualHat(int32(i), mask) })
}

// AttachVirtualDesc adds the virtual gamepad d, as AttachVirtual does.
// It fails, attaching nothing, for an unknown or repeated control name.
func (s *Source) AttachVirtualDesc(d VirtualDesc) (*Virtual, error) {
	return s.attachVirtual(d, sdl.JOYSTICK_TYPE_GAMEPAD)
}

// attachVirtual adds a virtual joystick of type typ. Only a gamepad is
// reported before it returns; SDL maps no other type to a gamepad, so its
// DeviceAdded event follows when T-input sees SDL_EVENT_JOYSTICK_ADDED.
func (s *Source) attachVirtual(d VirtualDesc, typ sdl.JoystickType) (*Virtual, error) {
	var buttons, axes uint32
	var err error
	if d.raw == nil {
		if buttons, err = controlMask("button", d.Buttons, buttonIndex, int(sdl.GAMEPAD_BUTTON_COUNT)); err != nil {
			return nil, fmt.Errorf("input: the virtual gamepad %q: %w", d.Name, err)
		}
		if axes, err = controlMask("axis", d.Axes, axisIndex, int(sdl.GAMEPAD_AXIS_COUNT)); err != nil {
			return nil, fmt.Errorf("input: the virtual gamepad %q: %w", d.Name, err)
		}
	}
	v := &Virtual{s: s, name: d.Name, buttons: packIndex(buttons), axes: packIndex(axes)}
	if d.Heartbeat {
		v.reports = make(chan virtualReport, virtualReports)
	}
	err = s.do(func() error {
		// padmate passes explicit masks: SDL packs the enabled buttons, and
		// separately the enabled axes, into consecutive joystick indices in
		// SDL enum order, and its virtual mapping maps them back
		// (SDL_virtualjoystick.c). packIndex gives the joystick index of
		// each control. A zero axis mask would mean something else, axes
		// in pairs by Naxes, which padmate never relies on.
		desc := &sdl.VirtualJoystickDesc{
			Version:    virtualDescVersion,
			Type:       typ,
			VendorId:   d.Vendor,
			ProductId:  d.Product,
			Naxes:      uint16(bits.OnesCount32(axes)),
			Nbuttons:   uint16(bits.OnesCount32(buttons)),
			ButtonMask: buttons,
			AxisMask:   axes,
			Name:       d.Name,
		}
		if d.raw != nil {
			desc.Naxes, desc.Nbuttons, desc.Nhats = uint16(d.raw.axes), uint16(d.raw.buttons), uint16(d.raw.hats)
		}
		id, err := sdl.AttachVirtualJoystick(desc)
		if err != nil {
			return fmt.Errorf("SDL_AttachVirtualJoystick: %w", err)
		}
		js, err := id.OpenJoystick()
		if err != nil {
			_ = id.DetachVirtualJoystick()
			return fmt.Errorf("SDL_OpenJoystick: %w", err)
		}
		v.id, v.js, v.rawRest = id, js, d.raw
		if err := v.rest(); err != nil {
			_ = s.detach(v)
			return err
		}
		s.virtuals[id] = v
		if typ == sdl.JOYSTICK_TYPE_GAMEPAD {
			// Open the gamepad now rather than when the loop reaches the
			// queued EVENT_GAMEPAD_ADDED: SDL sends no gamepad events for a
			// gamepad that is not open, so a SetButton right after
			// AttachVirtual would be lost.
			s.addGamepad(id, sdl.TicksNS())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// controlMask returns the mask of the controls names of one kind, "button"
// or "axis", each resolved by index to a bit below count. nil means all
// count controls, and an empty slice none. An unknown or repeated name is
// an error that names it, worded as SetButton and SetAxis word theirs.
func controlMask(kind string, names []string, index func(string) (int, bool), count int) (uint32, error) {
	if names == nil {
		return 1<<count - 1, nil
	}
	var mask uint32
	for _, name := range names {
		i, ok := index(name)
		if !ok || i >= count {
			return 0, fmt.Errorf("unknown %s %q", kind, name)
		}
		if mask&(1<<i) != 0 {
			return 0, fmt.Errorf("%s %q is listed twice", kind, name)
		}
		mask |= 1 << i
	}
	return mask, nil
}

// packIndex returns, for each bit i, the index that SDL's virtual driver
// gives control i when mask enables it, the number of enabled controls
// below it (SDL_virtualjoystick.c), or -1 when mask does not enable it.
func packIndex(mask uint32) [32]int8 {
	var idx [32]int8
	for i := range idx {
		idx[i] = -1
		if mask&(1<<i) != 0 {
			idx[i] = int8(bits.OnesCount32(mask & (1<<i - 1)))
		}
	}
	return idx
}

// rest reports the rest position of every axis the gamepad has, on
// T-input, while the gamepad is still closed and so sends no events. The
// virtual driver reports nothing until something changes, and SDL takes
// the first value it sees of a joystick axis as the rest position that it
// restores on removal.
func (v *Virtual) rest() error {
	if r := v.rawRest; r != nil {
		for a := range r.axes {
			var raw int16
			if a < len(r.axisRest) {
				raw = r.axisRest[a]
			}
			if err := v.js.SetVirtualAxis(int32(a), raw); err != nil {
				return fmt.Errorf("SDL_SetJoystickVirtualAxis: %w", err)
			}
		}
		sdl.UpdateJoysticks()
		return nil
	}
	for a, j := range v.axes[:sdl.GAMEPAD_AXIS_COUNT] {
		if j < 0 {
			continue
		}
		var raw int16
		if isTrigger(a) {
			raw = triggerRaw(0)
		}
		if err := v.js.SetVirtualAxis(int32(j), raw); err != nil {
			return fmt.Errorf("SDL_SetJoystickVirtualAxis: %w", err)
		}
	}
	sdl.UpdateJoysticks()
	return nil
}

// ID returns the device id of the virtual gamepad as it was attached. A
// gamepad that the receiver watchdog reported removed comes back with a
// new id (IN-30), which its DeviceAdded event gives.
func (v *Virtual) ID() DeviceID { return DeviceID(v.id) }

// Beat sends one heartbeat report of the gamepad, a full report (0x30)
// whose timer has moved, to T-input's heartbeat source, as a live
// receiver's gamepad does every few milliseconds. Its buttons and sticks
// are always zero, so after the first each one is an idle heartbeat, as
// from a gamepad at rest (hidbeat.go). It does not wait for T-input, and
// drops the report when 64 wait. It fails for a gamepad without
// VirtualDesc.Heartbeat, and once the gamepad is detached.
func (v *Virtual) Beat() error {
	var r virtualReport
	r.n = 13
	r.b[0], r.b[1], r.b[2] = reportFull, byte(v.timer.Add(1)), 0x90
	return v.report(r)
}

// report queues the heartbeat report r.
func (v *Virtual) report(r virtualReport) error {
	if v.reports == nil {
		return fmt.Errorf("input: the virtual gamepad %q has no heartbeat", v.name)
	}
	if v.gone.Load() {
		return errDetached
	}
	select {
	case v.reports <- r:
	default:
	}
	return nil
}

// beatSource returns the heartbeat source of v, which reads the reports
// that Beat queues. It has nothing to close.
func (v *Virtual) beatSource() *hidBeat {
	return &hidBeat{read: func(b []byte) int {
		select {
		case r := <-v.reports:
			return copy(b, r.b[:r.n])
		default:
			return 0
		}
	}}
}

// SetButton presses or releases a button, named as in ButtonName. It
// fails for a button that the gamepad does not have.
func (v *Virtual) SetButton(control string, down bool) error {
	b, ok := buttonIndex(control)
	if !ok {
		return fmt.Errorf("input: unknown button %q", control)
	}
	j := v.buttons[b]
	if j < 0 {
		return fmt.Errorf("input: the virtual gamepad %q has no button %q", v.name, control)
	}
	return v.set(func() error { return v.js.SetVirtualButton(int32(j), down) })
}

// SetAxis moves an axis, named as in AxisName, to value in gamepad terms:
// -32768..32767 for a stick, 0..32767 for a trigger, where a negative
// value counts as 0. It fails for an axis that the gamepad does not have.
func (v *Virtual) SetAxis(control string, value int16) error {
	a, ok := axisIndex(control)
	if !ok {
		return fmt.Errorf("input: unknown axis %q", control)
	}
	j := v.axes[a]
	if j < 0 {
		return fmt.Errorf("input: the virtual gamepad %q has no axis %q", v.name, control)
	}
	raw := value
	if isTrigger(a) {
		raw = triggerRaw(value)
	}
	return v.set(func() error { return v.js.SetVirtualAxis(int32(j), raw) })
}

// triggerRaw converts a trigger value 0..32767 to the raw joystick axis
// value that SDL's gamepad mapping turns back into it: the mapping scales
// the full raw range -32768..32767 onto 0..32767.
func triggerRaw(v int16) int16 {
	if v < 0 {
		v = 0
	}
	return int16(2*int32(v) - 32767)
}

// set runs f on T-input, then updates the joysticks so that the change is
// sent as events now. The virtual driver sends only the latest state at
// each update, so without it a press and release set in quick succession
// would produce no event at all.
func (v *Virtual) set(f func() error) error {
	return v.s.do(func() error {
		if v.js == nil {
			return errDetached
		}
		if err := f(); err != nil {
			return err
		}
		sdl.UpdateJoysticks()
		return nil
	})
}

// Detach removes the virtual gamepad; its DeviceRemoved event follows on
// Events. Detaching it again, or after Close, does nothing.
func (v *Virtual) Detach() error {
	err := v.s.do(func() error { return v.s.detach(v) })
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// detach closes and detaches v, on T-input.
func (s *Source) detach(v *Virtual) error {
	if v.js == nil {
		return nil
	}
	v.gone.Store(true)
	v.js.Close()
	v.js = nil
	delete(s.virtuals, v.id)
	if err := v.id.DetachVirtualJoystick(); err != nil {
		return fmt.Errorf("SDL_DetachVirtualJoystick: %w", err)
	}
	return nil
}
