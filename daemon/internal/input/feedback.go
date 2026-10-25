package input

import (
	"errors"
	"fmt"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
)

// This file connects the effect channel (internal/haptic, ADR-0065) to the
// gamepads: T-input owns a haptic.Mixer, registers each open gamepad with it
// through an Effector that wraps the *sdl.Gamepad and the capabilities SDL
// reports, and drives it from its loop with the wall clock. The three ways
// to ask for an effect all end there:
//
//   - SetFeedback, the ambient state of the layer and the pause (M3);
//   - Play, a preset of the Event, Script or Alert class (MAP-25, SCR-23);
//   - Identify, the cues that tell a user which controller is which (IN-13).
//
// A request is addressed to a DeviceID, the id the events of a gamepad carry
// (so a gamepad that came back from the receiver watchdog is reached by its
// new id only). Calls that SDL cannot do for a gamepad are not made: the
// Mixer reads its capabilities.

// ErrNoDevice is returned for an effect request for a gamepad that is not
// connected: unknown, removed, or reported removed by the receiver watchdog.
var ErrNoDevice = errors.New("input: no such gamepad")

// Feedback describes a best-effort physical controller indication: the
// ambient state of the layer and the pause (ambient class of the Mixer).
type Feedback struct {
	// Device is the gamepad the indication is for; 0 means every open one.
	Device DeviceID
	// Color is the LED color "#RRGGBB" of the layer. "" means the layer has
	// none: the ambient color is released, which sets #000000 on a color LED
	// that padmate colored and leaves any other LED alone.
	Color string
	// Pulse starts a train of Pulses pulses of PulseMs, GapMs apart, at
	// Strength on both motors, in place of the one that runs. A request
	// without Pulse leaves a running train alone.
	Pulse    bool
	Pulses   int
	PulseMs  int
	GapMs    int
	Strength uint16
	// PlayerMode is the mode number of the layer, for the player lights
	// (HUD-08): mode n from 1 to MaxPlayerMode lights the light of player n
	// (index n-1) on a gamepad that has them, and any other value, 0 among
	// them, gives the ambient claim of the lights back, which restores the
	// index the gamepad had. The caller sends 0 when the global led switch is
	// off.
	PlayerMode int
	// Paused says that the mapping is paused; Source does not read it.
	Paused bool
}

// MaxPlayerMode is the highest mode that has a player light: gamepads have
// four.
const MaxPlayerMode = 4

// Effect is a haptic preset for one gamepad.
type Effect struct {
	// Device is the gamepad; 0 is not a gamepad, and an error.
	Device DeviceID
	// Class is haptic.Event, haptic.Script or haptic.Alert. The ambient state
	// has SetFeedback, and the identify cues Identify.
	Class haptic.Class
	// Preset is the rumble to play. It may be 0 (no preset) for an effect that
	// only Blinks, or that plays Steps.
	Preset haptic.Preset
	// Steps, when there are any, are the rumble program to play in place of a
	// preset: a script's pad.rumble (SCR-23). The Mixer limits them (a step to
	// haptic.MaxStepMs, a program to haptic.MaxSteps); the caller does not
	// change them afterwards. An effect has a Preset or Steps, not both.
	Steps []haptic.Step
	// Strength is the strength of the preset; 0 means haptic.DefaultStrength.
	Strength uint16
	// ShortMs and GapMs are the pulse and the gap of the presets that
	// repeat them (haptic.Preset.Steps); not positive means the default.
	ShortMs, GapMs int
	// Blink, for the Alert class only, also flashes the gamepad's lights: red
	// on a color LED, or all four player lights when it has no color LED
	// (haptic.Mixer.AlertBlink). A gamepad that can flash neither ignores it.
	// The low-battery alert plays the preset and the lights side by side, and
	// either alone when the global rumble or led switch is off.
	Blink bool
}

// Light is a request for the LED of a gamepad (SCR-23): the class claims the
// color, which shows while no stronger class claims it (haptic.Mixer.SetLED),
// or, with Release, gives its claim up, and the color of the class below shows
// again.
type Light struct {
	// Device is the gamepad; 0 is not a gamepad, and an error.
	Device DeviceID
	// Class is haptic.Script or haptic.Alert. The layer color has
	// SetFeedback, and the identify cues Identify.
	Class haptic.Class
	// Color is the color to claim; it is not read with Release.
	Color haptic.RGB
	// Release gives up the claim of Class.
	Release bool
}

// Stop is a request to end what a class has going on a gamepad (SCR-23): its
// rumble program if that is the one running, and with Everything also its LED
// and player-light claims (haptic.Mixer.StopRumble, Cancel).
type Stop struct {
	// Device is the gamepad; 0 is not a gamepad, and an error.
	Device DeviceID
	// Class is haptic.Event, haptic.Script or haptic.Alert.
	Class haptic.Class
	// Everything also ends the LED and player-light claims of Class.
	Everything bool
}

// gamepadEffector is the haptic.Effector of an open gamepad, whose SDL
// instance id is id.
type gamepadEffector struct {
	gp *sdl.Gamepad
	id sdl.JoystickID
}

func (e gamepadEffector) Rumble(low, high uint16, ms uint32) error {
	return e.gp.Rumble(low, high, ms)
}

func (e gamepadEffector) RumbleTriggers(left, right uint16, ms uint32) error {
	return e.gp.RumbleTriggers(left, right, ms)
}

func (e gamepadEffector) SetLED(r, g, b uint8) error { return e.gp.SetLED(r, g, b) }

// SetPlayerIndex does nothing for a joystick that SDL no longer lists. SDL
// checks only that the handle is valid, which it is until it closes, and a
// write for a removed joystick puts its dead instance id into the global table
// of player indices, where nothing ever clears it (SDL_PrivateJoystickRemoved
// has cleared its entry already). Source.remove forgets the gamepad without a
// call, so this guards the paths that still reach one in the short time between
// SDL removing it and T-input reading the event, such as the shutdown. T-input
// runs both SDL's update and this call, so the check and the write are not
// separated by a removal.
func (e gamepadEffector) SetPlayerIndex(i int) error {
	if !listed(e.id) {
		return nil
	}
	return e.gp.SetPlayerIndex(int32(i))
}

func (e gamepadEffector) PlayerIndex() int { return int(e.gp.PlayerIndex()) }

// PlayerIndexTaken reports whether a joystick other than this one has player
// index index in SDL's table (SDL_GetJoystickPlayerIndexForID), whether or
// not padmate has it open: SDL gives every gamepad an index when it is added.
// When the list cannot be read it says yes, so that nothing is moved.
func (e gamepadEffector) PlayerIndexTaken(index int) bool {
	ids, err := sdl.GetJoysticks()
	if err != nil {
		return true
	}
	for _, id := range ids {
		if id != e.id && int(id.JoystickPlayerIndexForID()) == index {
			return true
		}
	}
	return false
}

// HapticCaps returns what the Mixer may do with a gamepad that reports c.
// When the capabilities are not known (a gamepad whose properties could not
// be read), it tries rumble and the LED, as M3 did for every gamepad, but
// does not release a color or touch the player lights, which need to be
// sure.
func (c Capabilities) HapticCaps() haptic.Caps {
	if !c.Known {
		return haptic.Caps{Rumble: true, LED: true}
	}
	return haptic.Caps{Rumble: c.Rumble, TriggerRumble: c.TriggerRumble, LED: c.LED, RGBLED: c.RGBLED, PlayerLED: c.PlayerLED}
}

// Cues returns the identify cues a gamepad with these capabilities can give.
func (c Capabilities) Cues() haptic.Cues { return haptic.CuesFor(c.HapticCaps()) }

// effects returns the Mixer of the Source, which T-input makes at its first
// use.
func (s *Source) effects() *haptic.Mixer {
	if s.mix == nil {
		s.mix = haptic.NewMixer()
	}
	return s.mix
}

// registerEffects adds the open gamepad d, described by info, to the Mixer
// under its current public id. A device without a gamepad has no effects.
func (s *Source) registerEffects(d *device, info *DeviceInfo) {
	if d.gp != nil {
		s.effects().Add(haptic.Device(d.pub), gamepadEffector{d.gp, d.id}, info.Capabilities.HapticCaps())
	}
}

// attachGamepad gives the device d its open gamepad gp, whether gp opened when
// d was reported (addGamepad) or later, as one that SDL lists but failed to
// open does once it can (reenumerate): d can be read for its power, and its
// effects are driven through the Mixer. describe reads d's DeviceInfo with gp
// open (Source.describe). It returns that DeviceInfo.
func (s *Source) attachGamepad(d *device, gp *sdl.Gamepad, describe func(*device) *DeviceInfo) *DeviceInfo {
	d.gp, d.kind = gp, kindGamepad
	d.powerFn = gp.PowerInfo
	info := describe(d)
	d.driver = info.Driver
	s.registerEffects(d, info)
	return info
}

// stopEffects ends every effect on d and forgets it, under the public id it
// has now: the motors stop, a color that padmate set is released, the player
// lights are restored. It does what it can and logs a failure at debug level.
// It is for a gamepad that SDL still lists: one reported removed by the
// receiver watchdog (lose), which stays open and dormant. A gamepad that SDL
// has removed gets forgetEffects.
func (s *Source) stopEffects(d *device) {
	if s.mix == nil {
		return
	}
	if err := s.mix.Remove(haptic.Device(d.pub), time.Now()); err != nil {
		s.log.Debug("controller effects not stopped cleanly", "id", d.pub, "err", err)
	}
}

// forgetEffects forgets d under the public id it has now, without a call to its
// gamepad. It is for a gamepad that SDL has removed (SDL_EVENT_*_REMOVED, or
// not listed by a rescan): stopEffects would write the original player index of
// a joystick that is gone, and leave its dead id in SDL's table of player
// indices (haptic.Mixer.Forget). Nothing else needs stopping: SDL_CloseJoystick,
// which remove calls next, sends the zero to the motors and the triggers if
// either has an expiry set, and an LED goes with its hardware. A dormant device
// was forgotten when it was lost, which makes this a no-op.
func (s *Source) forgetEffects(d *device) {
	if s.mix != nil {
		s.mix.Forget(haptic.Device(d.pub))
	}
}

// stopAllEffects does stopEffects for every device, at shutdown. A panic of a
// driver is contained, so that the gamepads are still closed after it.
func (s *Source) stopAllEffects() {
	if s.mix == nil {
		return
	}
	defer func() { _ = recover() }()
	if err := s.mix.StopAll(time.Now()); err != nil {
		s.log.Debug("controller effects not stopped cleanly", "err", err)
	}
}

// advanceEffects moves the Mixer on to now: the next pulse of a train, the
// next phase of a blink, the end of a cooldown. A failing call has put its
// group of effects of the gamepad into the cooldown already; the failure is
// logged at debug level.
func (s *Source) advanceEffects(now time.Time) {
	if s.mix == nil {
		return
	}
	if err := s.mix.Advance(now); err != nil {
		s.log.Debug("controller effect failed", "err", err)
	}
}

// openDevice returns the connected device with the public id id: not
// dormant, which is reported removed (IN-30).
func (s *Source) openDevice(id DeviceID) *device {
	for _, d := range s.devices {
		if d.pub == id && !d.dormant {
			return d
		}
	}
	return nil
}

// SetFeedback applies an ambient indication to the open gamepad it names, or
// to every open one when f.Device is 0. SDL owns the gamepad handles on
// T-input, so the operation is marshaled there. A gamepad that cannot show
// the indication ignores it; a call that fails is returned, and puts its group
// of effects (the motors, the LED, the player lights) of that gamepad into the
// Mixer's cooldown, the others going on. A color is written even when the LED
// shows it already, as M3 did. Unknown devices are ignored.
func (s *Source) SetFeedback(f Feedback) error {
	return s.do(func() error { return s.setFeedback(f, time.Now()) })
}

// setFeedback is SetFeedback on T-input at now. The indication is the
// ambient class of the Mixer: the color replaces the ambient color, or
// releases it when there is none, the player lights show the mode or give
// the index back, and a pulse train replaces the ambient train, but a
// request without a pulse leaves one that runs alone.
func (s *Source) setFeedback(f Feedback, now time.Time) error {
	var color haptic.RGB
	if f.Color != "" {
		c, ok := haptic.ParseColor(f.Color)
		if !ok {
			return fmt.Errorf("input: invalid feedback color %q", f.Color)
		}
		color = c
	}
	m := s.effects()
	var errs []error
	for _, d := range s.devices {
		dv := haptic.Device(d.pub)
		if (f.Device != 0 && d.pub != f.Device) || d.dormant || !m.Has(dv) {
			continue
		}
		if f.Color != "" {
			errs = append(errs, m.SetLED(dv, haptic.Ambient, color, now))
		} else {
			errs = append(errs, m.ReleaseLED(dv, haptic.Ambient, now))
		}
		if f.PlayerMode >= 1 && f.PlayerMode <= MaxPlayerMode {
			errs = append(errs, m.SetPlayer(dv, haptic.Ambient, f.PlayerMode-1, now))
		} else {
			errs = append(errs, m.ReleasePlayer(dv, haptic.Ambient, now))
		}
		if f.Pulse {
			_, err := m.Play(dv, haptic.Ambient, haptic.Train(f.Pulses, f.PulseMs, f.GapMs, f.Strength), now)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Play plays the preset of e on its gamepad. The class decides what it may
// interrupt (haptic.Mixer): an event never cuts the pulses of a layer. A
// request that the Mixer drops or limits is not an error. It returns
// ErrNoDevice for a gamepad that is not connected.
func (s *Source) Play(e Effect) error {
	return s.do(func() error { return s.play(e, time.Now()) })
}

// play is Play on T-input at now.
func (s *Source) play(e Effect, now time.Time) error {
	switch {
	case e.Device == 0:
		return errors.New("input: an effect needs a gamepad: device 0 is none")
	case e.Class != haptic.Event && e.Class != haptic.Script && e.Class != haptic.Alert:
		return fmt.Errorf("input: effects of class %v are not played with Play", e.Class)
	case e.Blink && e.Class != haptic.Alert:
		return fmt.Errorf("input: only an alert blinks the lights, not class %v", e.Class)
	case e.Preset != 0 && len(e.Steps) > 0:
		return errors.New("input: an effect has a preset or steps, not both")
	case len(e.Steps) == 0 && !e.Preset.Valid() && (!e.Blink || e.Preset != 0):
		return fmt.Errorf("input: invalid haptic preset %d", e.Preset)
	}
	d := s.openDevice(e.Device)
	if d == nil {
		return ErrNoDevice
	}
	m := s.effects()
	dv := haptic.Device(d.pub)
	if !m.Has(dv) {
		return nil // a joystick without a gamepad has no effects
	}
	var errs []error
	if e.Preset.Valid() {
		strength := e.Strength
		if strength == 0 {
			strength = haptic.DefaultStrength
		}
		_, err := m.Play(dv, e.Class, e.Preset.Steps(strength, e.ShortMs, e.GapMs), now)
		errs = append(errs, err)
	}
	if len(e.Steps) > 0 {
		_, err := m.Play(dv, e.Class, e.Steps, now)
		errs = append(errs, err)
	}
	if e.Blink {
		_, err := m.AlertBlink(dv, now)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Identify makes the gamepad give its cues for the user to tell it from the
// others (IN-13): three buzzes (750 ms), three white flashes of a color LED
// (1250 ms), or a chase over the player lights that no other gamepad holds
// (1.5 s), whichever it can, side by side. It returns which cues the gamepad
// gives, none for one that can do nothing.
// It works for a gamepad that no slot has, and ignores the profile and the
// pause; the layer color and the player lights come back when it ends. It
// returns ErrNoDevice for a gamepad that is not connected.
func (s *Source) Identify(id DeviceID) (haptic.Cues, error) {
	var cues haptic.Cues
	err := s.do(func() error {
		var err error
		cues, err = s.identify(id, time.Now())
		return err
	})
	return cues, err
}

// identify is Identify on T-input at now.
func (s *Source) identify(id DeviceID, now time.Time) (haptic.Cues, error) {
	d := s.openDevice(id)
	if d == nil {
		return haptic.Cues{}, ErrNoDevice
	}
	m := s.effects()
	dv := haptic.Device(d.pub)
	if !m.Has(dv) {
		return haptic.Cues{}, nil
	}
	return m.Identify(dv, now)
}

// effectClass reports whether c is a class that the one-shot requests (Play,
// Light, Stop) serve: the Mixer's ambient state has SetFeedback, and the identify
// cues Identify.
func effectClass(c haptic.Class) bool {
	return c == haptic.Event || c == haptic.Script || c == haptic.Alert
}

// SetLight makes the class of l claim the LED of its gamepad, or gives the
// claim up (SCR-23). A gamepad without an LED, or whose color a stronger class
// claims, ignores it; it is not an error. It returns ErrNoDevice for a gamepad
// that is not connected.
func (s *Source) SetLight(l Light) error {
	return s.do(func() error { return s.setLight(l, time.Now()) })
}

// setLight is SetLight on T-input at now.
func (s *Source) setLight(l Light, now time.Time) error {
	switch {
	case l.Device == 0:
		return errors.New("input: a light needs a gamepad: device 0 is none")
	case l.Class != haptic.Script && l.Class != haptic.Alert:
		return fmt.Errorf("input: lights of class %v are not set with SetLight", l.Class)
	}
	d := s.openDevice(l.Device)
	if d == nil {
		return ErrNoDevice
	}
	m := s.effects()
	dv := haptic.Device(d.pub)
	if !m.Has(dv) {
		return nil // a joystick without a gamepad has no effects
	}
	if l.Release {
		return m.ReleaseLED(dv, l.Class, now)
	}
	return m.SetLED(dv, l.Class, l.Color, now)
}

// Stop ends what the class of st has going on its gamepad (SCR-23): the rumble
// program if it is that class's, and the LED and player-light claims too with
// Everything. It is not an error when there is nothing to end. It returns
// ErrNoDevice for a gamepad that is not connected.
func (s *Source) Stop(st Stop) error {
	return s.do(func() error { return s.stop(st, time.Now()) })
}

// stop is Stop on T-input at now.
func (s *Source) stop(st Stop, now time.Time) error {
	switch {
	case st.Device == 0:
		return errors.New("input: a stop needs a gamepad: device 0 is none")
	case !effectClass(st.Class):
		return fmt.Errorf("input: effects of class %v are not stopped with Stop", st.Class)
	}
	d := s.openDevice(st.Device)
	if d == nil {
		return ErrNoDevice
	}
	m := s.effects()
	dv := haptic.Device(d.pub)
	if !m.Has(dv) {
		return nil // a joystick without a gamepad has no effects
	}
	if st.Everything {
		return m.Cancel(dv, st.Class, now)
	}
	return m.StopRumble(dv, st.Class, now)
}
