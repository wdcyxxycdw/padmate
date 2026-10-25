package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"
)

// This file is what T-input reads about a gamepad's power (IN-12, ADR-0065):
// the battery state and charge, as the states of the script API. A value is
// not part of DeviceInfo, which is a static snapshot (and the info of
// traces): it changes while the gamepad is connected, and travels as
// DevicePower events.

// powerPollEvery is how often T-input reads the power of every gamepad by
// default (Options.PowerPollEvery), in
// case a driver sends no battery event. SDL sends the event when a driver
// finds a change (SDL_SendJoystickPowerInfo in SDL_joystick.c), for a gamepad
// as well, which opens a joystick; the poll costs one locked read of two
// fields, and sends nothing when the value is the one sent last.
const powerPollEvery = 30 * time.Second

// PowerState is the power source of a gamepad, in the four states of the
// script API's DeviceInfo.battery (SCRIPTING §2.2).
type PowerState uint8

// The power states. The zero value is PowerUnknown, which is what SDL says
// when it cannot tell, and what a driver that reports nothing leaves.
const (
	PowerUnknown PowerState = iota
	// PowerDischarging is SDL_POWERSTATE_ON_BATTERY: not plugged in.
	PowerDischarging
	// PowerCharging is SDL_POWERSTATE_CHARGING, except for a driver whose
	// battery comes from XInput, where SDL says it for a pad that is plugged in
	// or wireless behind a receiver alike (powerFromSDL).
	PowerCharging
	// PowerWired is SDL_POWERSTATE_NO_BATTERY and SDL_POWERSTATE_CHARGED:
	// plugged in, with nothing left to charge; and, for a driver whose battery
	// comes from XInput, SDL_POWERSTATE_CHARGING, which XInput's wired type
	// becomes.
	PowerWired
)

var powerStateNames = [...]string{
	PowerUnknown:     "unknown",
	PowerDischarging: "discharging",
	PowerCharging:    "charging",
	PowerWired:       "wired",
}

// String returns "unknown", "discharging", "charging" or "wired", the
// states of the script API; a value out of range is "unknown".
func (s PowerState) String() string {
	if int(s) < len(powerStateNames) {
		return powerStateNames[s]
	}
	return powerStateNames[PowerUnknown]
}

// ParsePowerState is the inverse of PowerState.String.
func ParsePowerState(name string) (PowerState, bool) {
	for s, n := range powerStateNames {
		if n == name {
			return PowerState(s), true
		}
	}
	return PowerUnknown, false
}

// Power is what a gamepad reports about its power. It is comparable.
type Power struct {
	State PowerState
	// Percent is the charge, 0..100, or -1 when the gamepad does not report
	// it. Drivers report it coarsely: XInput in four levels (10, 40, 70,
	// 100), the Switch protocol in quarters (0, 25, 50, 75, 100)
	// (SDL_xinputjoystick.c, SDL_hidapi_switch.c).
	Percent int
}

// powerWire is the JSON form of Power: devices.list's "battery".
type powerWire struct {
	State   string `json:"state"`
	Percent *int   `json:"percent,omitempty"`
}

// MarshalJSON writes {"state":"discharging","percent":80}; the percent is
// left out when it is not reported.
func (p Power) MarshalJSON() ([]byte, error) {
	w := powerWire{State: p.State.String()}
	if p.Percent >= 0 {
		pct := p.Percent
		w.Percent = &pct
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads MarshalJSON's form strictly: a known state, an
// absent percent (-1) or one in 0..100, and no other member.
func (p *Power) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w powerWire
	if err := dec.Decode(&w); err != nil {
		return fmt.Errorf("input: power: %w", err)
	}
	if dec.More() {
		return errors.New("input: power: data after the object")
	}
	state, ok := ParsePowerState(w.State)
	if !ok {
		return fmt.Errorf("input: power: unknown state %q", w.State)
	}
	pct := -1
	if w.Percent != nil {
		if *w.Percent < 0 || *w.Percent > 100 {
			return fmt.Errorf("input: power: percent %d is not in 0..100", *w.Percent)
		}
		pct = *w.Percent
	}
	*p = Power{State: state, Percent: pct}
	return nil
}

// powerFromSDL maps SDL's power state and percent for a gamepad of the SDL
// backend driver (DeviceInfo.Driver). SDL_POWERSTATE_ERROR is
// 4294967295 in go-sdl3 (ADR-0010); it and SDL_POWERSTATE_UNKNOWN, and a
// state that a newer SDL adds, are PowerUnknown, with no percent: nothing is
// known then. Otherwise a percent below 0 is not reported (-1) and one above
// 100 is 100.
//
// SDL reports XInput's BATTERY_TYPE_WIRED as SDL_POWERSTATE_CHARGING at 100 %,
// and XInput cannot tell a pad that charges from one that is wired: a 2.4G
// receiver for a wireless pad in Xbox mode says it for ever. So for a driver
// whose battery comes from XInput (batteryFromXInput) charging is PowerWired.
func powerFromSDL(state sdl.PowerState, percent int32, driver string) Power {
	p := Power{Percent: -1}
	switch state {
	case sdl.POWERSTATE_ON_BATTERY:
		p.State = PowerDischarging
	case sdl.POWERSTATE_CHARGING:
		p.State = PowerCharging
		if batteryFromXInput(driver) {
			p.State = PowerWired
		}
	case sdl.POWERSTATE_NO_BATTERY, sdl.POWERSTATE_CHARGED:
		p.State = PowerWired
	default:
		return p
	}
	if percent >= 0 {
		p.Percent = int(min(percent, 100))
	}
	return p
}

// batteryFromXInput reports whether the SDL backend driver takes the battery of
// its gamepads from XInput: "xinput" (UpdateXInputJoystickBatteryInformation in
// SDL_xinputjoystick.c) and "rawinput", which reads XInput's battery for a
// device it correlates with an XInput slot (RAWINPUT_UpdateOtherAPIs in
// SDL_rawinputjoystick.c); both map BATTERY_TYPE_WIRED to charging (SDL
// 3.4.16). "wgi" reads real battery reports and keeps its charging.
func batteryFromXInput(driver string) bool { return driver == "xinput" || driver == "rawinput" }

// readPower reads the power of the gamepad d, when power events are on, and
// sends it as of time t when it is not the one sent last. It does nothing
// for a device without a gamepad, which has no power to read, or a dormant
// one, which is reported removed (IN-30).
func (s *Source) readPower(d *device, t uint64) {
	if !s.power || d.powerFn == nil || d.dormant {
		return
	}
	state, percent := d.powerFn()
	s.setPower(d, powerFromSDL(state, percent, d.driver), t)
}

// rereadPower reads the power of d again, after it came back as a new
// device: the consumer dropped what it knew with the removal, so the value
// is sent even if it is the one that was sent.
func (s *Source) rereadPower(d *device, t uint64) {
	d.powerSent = false
	s.readPower(d, t)
}

// setPower sends p as the power of d at t, unless it is the one sent
// last. The first value of a device is always sent, unknown too, so that a
// consumer can tell that it was read.
func (s *Source) setPower(d *device, p Power, t uint64) {
	if d.powerSent && d.power == p {
		return
	}
	d.power, d.powerSent = p, true
	s.send(Event{Time: t, Device: d.pub, Kind: DevicePower, Power: &p})
}

// pollPower reads the power of every gamepad at now, once every poll
// interval (Options.PowerPollEvery, powerPollEvery by default). The first
// call only starts the clock: a gamepad is read when it connects.
func (s *Source) pollPower(now time.Time) {
	if !s.power {
		return
	}
	every := s.powerPoll
	if every <= 0 {
		every = powerPollEvery
	}
	if s.powerNext.IsZero() {
		s.powerNext = now.Add(every)
		return
	}
	if now.Before(s.powerNext) {
		return
	}
	s.powerNext = now.Add(every)
	var t uint64
	if s.ticks != nil {
		t = s.ticks()
	}
	for _, d := range s.devices {
		s.readPower(d, t)
	}
}

// handlePower handles SDL's battery event: its state and percent are the new
// value. The event of a device T-input does not hold, or that has no
// gamepad, or is dormant, is ignored.
func (s *Source) handlePower(e *sdl.JoyBatteryEvent) {
	d := s.devices[e.Which]
	if !s.power || d == nil || d.powerFn == nil || d.dormant {
		return
	}
	s.setPower(d, powerFromSDL(e.State, e.Percent, d.driver), e.Timestamp)
}

// Readout is what SDL says about an open gamepad besides its DeviceInfo,
// read without sending it anything: "padmatectl monitor --list" prints it.
type Readout struct {
	Power Power
	// Connection is SDL's connection state of the gamepad: "wired",
	// "wireless", or "unknown" (SDL_JOYSTICK_CONNECTION_UNKNOWN, or one that
	// could not be read). A USB receiver is reported "wired" whatever is
	// behind it (PITFALLS "2.4G 接收器").
	Connection string
	// PlayerIndex is SDL's player index of the gamepad, or -1 when it has
	// none.
	PlayerIndex int
}

// Readout reads Readout of the open gamepad id on T-input. It returns an
// error for a device that is not an open gamepad of this Source, and
// ErrClosed once the Source is closed. The getters are cached reads of
// fields, so it makes no I/O on the device.
func (s *Source) Readout(id DeviceID) (Readout, error) {
	var r Readout
	err := s.do(func() error {
		for _, d := range s.devices {
			if d.pub != id {
				continue
			}
			if d.gp == nil {
				break
			}
			state, percent := d.gp.PowerInfo()
			r.Power = powerFromSDL(state, percent, d.driver)
			r.Connection = connectionName(d.gp)
			r.PlayerIndex = int(d.gp.PlayerIndex())
			return nil
		}
		return fmt.Errorf("input: device %d is not an open gamepad", id)
	})
	return r, err
}

// connectionName names the connection state of gp, "unknown" when SDL
// cannot tell.
func connectionName(gp *sdl.Gamepad) string {
	switch state, _ := gp.ConnectionState(); state {
	case sdl.JOYSTICK_CONNECTION_WIRED:
		return "wired"
	case sdl.JOYSTICK_CONNECTION_WIRELESS:
		return "wireless"
	}
	return "unknown"
}
