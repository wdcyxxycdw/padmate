package haptic

import (
	"errors"
	"fmt"
	"time"
)

// Device names one controller to a Mixer: internal/input uses the DeviceID
// that the events of the controller carry.
type Device uint32

// Effector is what the Mixer drives: one controller's motors, LED and
// player lights. internal/input wraps an *sdl.Gamepad in one. Every method
// may block on the device and may fail; the Mixer never calls one that the
// controller's Caps say it cannot do.
type Effector interface {
	// Rumble runs the motors at these strengths for ms milliseconds, then
	// SDL stops them. A call with both strengths 0 stops them at once.
	Rumble(low, high uint16, ms uint32) error
	// RumbleTriggers does the same for the triggers' motors.
	RumbleTriggers(left, right uint16, ms uint32) error
	// SetLED sets the color of the LED.
	SetLED(r, g, b uint8) error
	// SetPlayerIndex lights the player lights for index (0 is the first
	// player), or none for -1.
	SetPlayerIndex(index int) error
	// PlayerIndex returns the player index the controller has now, -1 when
	// it has none. The Mixer reads it before the first change, to restore it.
	PlayerIndex() int
	// PlayerIndexTaken reports whether another controller has player index
	// index, or whether that cannot be told. Player indices are one table for
	// every controller (SDL), and giving one to a controller moves the holder
	// to a free slot, so the Mixer asks before it takes one for a pattern.
	PlayerIndexTaken(index int) bool
}

// Caps is what a controller can do, as SDL's properties report it
// (input.Capabilities). The Mixer does nothing a controller cannot: a
// request for an effect it lacks is ignored, not an error.
type Caps struct {
	Rumble        bool
	TriggerRumble bool
	// LED is a color LED or a mono one; RGBLED only a color one. A mono LED
	// takes colors, as SDL maps them to a brightness, but has no "off" that
	// the Mixer may assume (see Mixer.ReleaseLED).
	LED, RGBLED bool
	PlayerLED   bool
}

// Class is the priority class of a request. A higher class wins the motors
// from a lower one, and owns a level effect over it.
type Class uint8

// The classes, from the lowest. For rumble the order is the one the
// constants give. For the LED and the player lights it is Identify, Alert,
// Script, Ambient: the layer color is the weakest there (levelOrder).
const (
	// Event is a haptic that a binding or a knob tick asks for.
	Event Class = iota + 1
	// Script is what a script asks for.
	Script
	// Ambient is the state of the layer and of the pause: its pulses, its
	// color and the player lights of the mode.
	Ambient
	// Alert is a warning the daemon raises, such as a low battery.
	Alert
	// Identify answers "which controller is this" and wins everything.
	Identify

	numClasses = int(Identify) + 1 // the classes and the unused 0
)

var classNames = [...]string{Event: "event", Script: "script", Ambient: "ambient", Alert: "alert", Identify: "identify"}

// Valid reports whether c is one of the classes.
func (c Class) Valid() bool { return c >= Event && c <= Identify }

func (c Class) String() string {
	if c.Valid() {
		return classNames[c]
	}
	return fmt.Sprintf("class(%d)", uint8(c))
}

// Timing constants of the Mixer.
const (
	// EventMinInterval is the shortest time between the starts of two
	// Event-class programs on one controller, so that a burst of ticks does
	// not flood a device. It is the 30 ms at which SDL's Switch driver
	// coalesces rumble writes, and above the 25 Hz of a knob.
	EventMinInterval = 30 * time.Millisecond
	// ErrorCooldown is how long the motors or the player lights of a
	// controller are left alone after a call to them failed: a write that
	// blocks on a receiver that ignores output reports would hold up every
	// controller's input (PITFALLS "2.4G 接收器"), so the Mixer does not
	// hammer it. It is not shorter than MaxStepMs: whatever was running when
	// the call failed has expired by itself when it ends. The motors (rumble
	// and trigger rumble), the LED and the player lights each have a
	// cooldown of their own, so that a failing HOME-LED write does not take
	// the layer pulses with it.
	ErrorCooldown = 5 * time.Second
	// LEDCooldown is the cooldown of the LED, which is longer. SDL keeps the
	// color it was given whether or not the driver call worked, and for
	// sdlLEDRepeat after that call returned it does not call the driver for the
	// same color again (SDL_SetJoystickLED). A retry of a failed write inside
	// that window would be taken for a repeat, report success and change
	// nothing. The Mixer's time is the start of the call, and a write that
	// times out can last up to a second (hid_write), so ledWriteSlack comes on
	// top.
	LEDCooldown = sdlLEDRepeat + ledWriteSlack

	// sdlLEDRepeat is SDL_LED_MIN_REPEAT_MS, 5 s: the window in which SDL
	// drops a repeat of the color it has. The constant is defined in a header
	// that the cached sources do not have (PITFALLS "效果通道"), so the value
	// is not checked.
	sdlLEDRepeat = 5 * time.Second
	// ledWriteSlack is how long a failing LED write may take, and some more.
	ledWriteSlack = 2 * time.Second

	// expirySlack is how long after SDL should have expired a rumble the
	// Mixer still counts it as running. SDL expires it in SDL_UpdateJoysticks,
	// which T-input runs at least every 2 ms, so a rumble that starts within
	// the slack of the end of the previous one may meet the same strengths
	// there, which SDL takes for a no-op that only extends the expiry.
	expirySlack = 4 * time.Millisecond
)

var (
	// ErrUnknownDevice is returned for a Device that was not added.
	ErrUnknownDevice = errors.New("haptic: unknown device")
	// ErrClass is returned for a class that cannot make the request.
	ErrClass = errors.New("haptic: the class cannot make this request")
)

// Outcome says what Play did with a request.
type Outcome uint8

// The outcomes of Play.
const (
	// Started: the program runs.
	Started Outcome = iota
	// Dropped: a program of a higher class holds the motors.
	Dropped
	// Limited: an Event-class program started less than EventMinInterval ago.
	Limited
	// Unsupported: the controller cannot do any step of the program.
	Unsupported
	// Cooling: the motors of the controller are in their error cooldown.
	Cooling
	// Empty: the program has no steps.
	Empty
)

var outcomeNames = [...]string{"started", "dropped", "limited", "unsupported", "cooling", "empty"}

func (o Outcome) String() string {
	if int(o) < len(outcomeNames) {
		return outcomeNames[o]
	}
	return fmt.Sprintf("outcome(%d)", uint8(o))
}

// Mixer decides, for each controller added to it, which of the requests
// that reach it owns the motors, the LED and the player lights. Rumble is
// one program at a time: a request preempts the running program only if its
// class is at least the running one's. The LED and the player lights have
// one claim per class, and the strongest claim of the moment shows (see
// levelOrder); a timed pattern gives the effect back to the claims below it
// when it ends. Nothing is endless: every step is finite, and every path that
// ends the effects of a controller (Stop, Remove, StopAll, the error
// cooldown) leaves the motors still, the LED released and the player lights
// restored (CLAUDE.md invariant 1, for haptics); Forget alone makes no call,
// for a controller that SDL has removed. A call that fails puts only its own
// group of effects, the motors, the LED or the player lights, into a cooldown;
// the others go on.
//
// A Mixer is not safe for concurrent use: input calls it from T-input only.
// Time comes in as the now of each call, so a test drives it with any clock;
// the caller should advance it, with Advance, whenever it can and at least
// every few milliseconds while a program runs.
type Mixer struct {
	devs map[Device]*dev
}

// NewMixer returns a Mixer with no controllers.
func NewMixer() *Mixer { return &Mixer{devs: make(map[Device]*dev)} }

// channel is one SDL rumble channel of a controller, the motors or the
// triggers: what was sent last and when SDL expires it by itself.
type channel struct {
	send  func(a, b uint16, ms uint32) error
	on    bool      // something non-zero was sent since the last zero
	until time.Time // when SDL expires it
}

// start runs the channel at a and b for ms. When the previous rumble still
// runs, or may not be expired yet, it first stops it: SDL takes a request
// for the strengths it has as a no-op that only extends the expiry, so two
// identical steps in a row would merge into one buzz.
func (c *channel) start(a, b uint16, ms uint32, now time.Time) error {
	if c.on && !now.After(c.until.Add(expirySlack)) {
		if err := c.send(0, 0, 0); err != nil {
			return err
		}
		c.on = false
	}
	if err := c.send(a, b, ms); err != nil {
		return err
	}
	c.on, c.until = true, now.Add(time.Duration(ms)*time.Millisecond)
	return nil
}

// silence stops the channel if it still runs; a gap after a pulse needs no
// write, since the pulse ends by itself with its step.
func (c *channel) silence(now time.Time) error {
	if c.on && now.Before(c.until) {
		if err := c.send(0, 0, 0); err != nil {
			return err
		}
		c.on = false
	}
	return nil
}

// stop sends the zero if anything was sent since the last one.
func (c *channel) stop() error {
	if !c.on {
		return nil
	}
	if err := c.send(0, 0, 0); err != nil {
		return err
	}
	c.on = false
	return nil
}

// program is the rumble program that runs on a controller.
type program struct {
	class  Class
	steps  []Step
	i      int
	end    time.Time // the end of steps[i]
	active bool
}

// dev is the state of one controller.
type dev struct {
	id   Device
	eff  Effector
	caps Caps

	motors, triggers channel
	prog             program
	eventAt          time.Time // when the last Event-class program started

	// The ends of the error cooldowns of the three groups of effects, zero
	// when a group has none: a call that fails starts the cooldown of its own
	// group only (fail*).
	coolMotors, coolLED, coolPlayer time.Time

	// The LED: what each class claims, what the Mixer last put on the LED
	// (ledShown, when ledValid), and whether it ever colored it, which the
	// release rule needs. A request that an owner makes for the color it shows
	// clears ledValid (askLED), so that the color is written again.
	led      [numClasses]claim[RGB]
	ledShown RGB
	ledValid bool
	colored  bool

	// The player lights, likewise. playerOrig is the index the controller had
	// before the first change, which is restored when no claim remains.
	player        [numClasses]claim[int]
	playerShown   int
	playerValid   bool
	playerChanged bool
	playerOrig    int
}

// Add adds the controller dv, driven through e, which can do what caps says.
// Adding a device again replaces it without touching the controller.
func (m *Mixer) Add(dv Device, e Effector, caps Caps) {
	d := &dev{id: dv, eff: e, caps: caps}
	d.motors.send = e.Rumble
	d.triggers.send = e.RumbleTriggers
	m.devs[dv] = d
}

// Has reports whether dv was added.
func (m *Mixer) Has(dv Device) bool { return m.devs[dv] != nil }

// CapsOf returns the caps dv was added with.
func (m *Mixer) CapsOf(dv Device) (Caps, bool) {
	if d := m.devs[dv]; d != nil {
		return d.caps, true
	}
	return Caps{}, false
}

// failed is the error that Play and the claims return for a failed call to the
// group of effects of d called what, which is left alone for cool.
func (d *dev) failed(what string, err error, cool time.Duration) error {
	return fmt.Errorf("haptic: device %d: %s: %w (left alone for %v)", d.id, what, err, cool)
}

// failMotors puts the motors of d into their cooldown after a call to them
// failed: the program is dropped (the motors stop by themselves, as every step
// is finite) and what the Mixer believes about them is unknown. The LED and
// the player lights go on.
func (d *dev) failMotors(err error, now time.Time) error {
	d.coolMotors = now.Add(ErrorCooldown)
	d.prog = program{}
	d.motors.on, d.triggers.on = false, false
	return d.failed("motors", err, ErrorCooldown)
}

// failLED puts the LED of d into its cooldown after a call to it failed: the
// timed patterns are dropped, the static claims stay and are applied again
// when the cooldown ends, and what the LED shows is unknown.
func (d *dev) failLED(err error, now time.Time) error {
	d.coolLED = now.Add(LEDCooldown)
	for c := range d.led {
		if d.led[c].timed() {
			d.led[c].release()
		}
	}
	d.ledValid = false
	return d.failed("LED", err, LEDCooldown)
}

// failPlayer is failLED for the player lights.
func (d *dev) failPlayer(err error, now time.Time) error {
	d.coolPlayer = now.Add(ErrorCooldown)
	for c := range d.player {
		if d.player[c].timed() {
			d.player[c].release()
		}
	}
	d.playerValid = false
	return d.failed("player lights", err, ErrorCooldown)
}

// gate checks the cooldown that ends at *until: it reports whether its group
// may be written to at now, and whether the cooldown has just ended, which
// clears it. The caller then applies the claims that were made meanwhile.
func gate(until *time.Time, now time.Time) (open, ended bool) {
	switch {
	case until.IsZero():
		return true, false
	case now.Before(*until):
		return false, false
	}
	*until = time.Time{}
	return true, true
}

// readyMotors reports whether the motors of d may be written to at now. A
// program is dropped by the failure, so nothing is applied when the cooldown
// ends.
func (d *dev) readyMotors(now time.Time) bool {
	open, _ := gate(&d.coolMotors, now)
	return open
}

// readyLED reports whether the LED of d may be written to at now. When its
// cooldown has just ended it applies the claims that were made meanwhile.
func (d *dev) readyLED(now time.Time) (bool, error) {
	open, ended := gate(&d.coolLED, now)
	if !open || !ended {
		return open, nil
	}
	d.advanceLED(now)
	err := d.syncLED(now)
	return d.coolLED.IsZero(), err
}

// readyPlayer is readyLED for the player lights.
func (d *dev) readyPlayer(now time.Time) (bool, error) {
	open, ended := gate(&d.coolPlayer, now)
	if !open || !ended {
		return open, nil
	}
	d.advancePlayer(now)
	err := d.syncPlayer(now)
	return d.coolPlayer.IsZero(), err
}

// uses reports whether any step drives the motors, and whether any drives
// the triggers, with what d can do.
func (d *dev) uses(steps []Step) (motors, triggers bool) {
	for _, s := range steps {
		motors = motors || (d.caps.Rumble && s.Low|s.High != 0)
		triggers = triggers || (d.caps.TriggerRumble && s.LeftTrig|s.RightTrig != 0)
	}
	return motors, triggers
}

// Play starts the rumble program steps with the priority of c on dv at now.
// The steps are not copied and must not change. It returns what became of the
// request; an error only for a call to the controller that failed, which
// starts the cooldown and makes the outcome Cooling, or an unknown device or
// class. A step is capped to 1..MaxStepMs and a program to MaxSteps.
func (m *Mixer) Play(dv Device, c Class, steps []Step, now time.Time) (Outcome, error) {
	d := m.devs[dv]
	switch {
	case d == nil:
		return Dropped, ErrUnknownDevice
	case !c.Valid():
		return Dropped, ErrClass
	case len(steps) == 0:
		return Empty, nil
	}
	if len(steps) > MaxSteps {
		steps = steps[:MaxSteps]
	}
	motors, triggers := d.uses(steps)
	if !motors && !triggers {
		return Unsupported, nil
	}
	if !d.readyMotors(now) {
		return Cooling, nil
	}
	switch {
	case c == Event && !d.eventAt.IsZero() && now.Sub(d.eventAt) < EventMinInterval:
		return Limited, nil
	case d.prog.active && c < d.prog.class:
		return Dropped, nil
	}
	d.prog = program{class: c, steps: steps, active: true}
	if c == Event {
		d.eventAt = now
	}
	// What the program replaces may still run on a channel it does not use.
	var err error
	if !motors {
		err = d.motors.silence(now)
	}
	if err == nil && !triggers {
		err = d.triggers.silence(now)
	}
	if err == nil {
		err = d.runStep(now)
	} else {
		err = d.failMotors(err, now)
	}
	if err != nil {
		return Cooling, err
	}
	return Started, nil
}

// runStep starts step d.prog.i at now.
func (d *dev) runStep(now time.Time) error {
	p := &d.prog
	s := p.steps[p.i]
	ms := clampMs(int(s.Ms))
	low, high, left, right := s.Low, s.High, s.LeftTrig, s.RightTrig
	if !d.caps.Rumble {
		low, high = 0, 0
	}
	if !d.caps.TriggerRumble {
		left, right = 0, 0
	}
	var err error
	if low|high != 0 {
		err = d.motors.start(low, high, uint32(ms), now)
	} else {
		err = d.motors.silence(now)
	}
	if err == nil {
		if left|right != 0 {
			err = d.triggers.start(left, right, uint32(ms), now)
		} else {
			err = d.triggers.silence(now)
		}
	}
	if err != nil {
		return d.failMotors(err, now)
	}
	p.end = now.Add(time.Duration(ms) * time.Millisecond)
	return nil
}

// endProgram drops the running program and stops the motors it ran.
func (d *dev) endProgram(now time.Time) error {
	d.prog = program{}
	err := d.motors.stop()
	if err == nil {
		err = d.triggers.stop()
	}
	if err != nil {
		return d.failMotors(err, now)
	}
	return nil
}

// advanceProgram moves the running program on when its step is over.
func (d *dev) advanceProgram(now time.Time) error {
	p := &d.prog
	if !p.active || now.Before(p.end) {
		return nil
	}
	p.i++
	if p.i >= len(p.steps) {
		return d.endProgram(now)
	}
	return d.runStep(now)
}

// advanceLED moves the timed LED patterns on, and reports whether any moved.
func (d *dev) advanceLED(now time.Time) bool {
	moved := false
	for c := range d.led {
		moved = d.led[c].advance(now) || moved
	}
	return moved
}

// advancePlayer moves the timed player-light patterns on, and reports whether
// any moved.
func (d *dev) advancePlayer(now time.Time) bool {
	moved := false
	for c := range d.player {
		moved = d.player[c].advance(now) || moved
	}
	return moved
}

// askLED notes that the owner c has asked for the LED, by a call of its own
// and not by a pattern moving on. If its claim is the one that shows, the next
// sync writes the color again, even when the LED shows it already: SDL drops
// a repeat inside its window (sdlLEDRepeat) and sends one after it, which is
// what M3 relied on to bring back a light that a receiver had reset behind the
// Mixer's back, as a power-cycled controller does. A claim that is not
// the winner, and a pattern that reveals the claim below it, change nothing
// that shows, and are not written.
func (d *dev) askLED(c Class) {
	if k, _, ok := winnerOf(&d.led); ok && k == c {
		d.ledValid = false
	}
}

// syncLED puts the LED where the claims say. With a claim it sets the color
// of the strongest, if the controller has an LED. With none left it applies
// the release rule: if padmate ever colored the LED, and it is a color LED,
// it sets #000000, since SDL has no way to hand an LED back; a mono LED and a
// controller that was never colored are left alone. It does not write a
// color that the LED shows, unless askLED cleared that.
func (d *dev) syncLED(now time.Time) error {
	if want, ok := winner(&d.led); ok {
		if !d.caps.LED || (d.ledValid && d.ledShown == want) {
			return nil
		}
		if err := d.eff.SetLED(want[0], want[1], want[2]); err != nil {
			return d.failLED(err, now)
		}
		d.ledShown, d.ledValid, d.colored = want, true, true
		return nil
	}
	if !d.colored {
		return nil
	}
	if d.caps.RGBLED && (!d.ledValid || d.ledShown != RGB{}) {
		if err := d.eff.SetLED(0, 0, 0); err != nil {
			return d.failLED(err, now)
		}
		d.ledShown, d.ledValid = RGB{}, true
	}
	d.colored = false
	return nil
}

// syncPlayer puts the player lights where the claims say, and restores the
// index the controller had when no claim remains. It does nothing for a
// controller without player lights: SDL's player indices are one table for
// all controllers, so setting one on a controller that cannot show it would
// only renumber the others.
//
// A claim for a light that another controller holds is not shown, and the
// lights stay as they are: giving the index to this controller would move that
// one to a free slot, and change its lights, for good. Controllers that ask
// for the same light, such as two in the same mode (HUD-08), so have it one
// after the other in the order of their requests, and the rest keep their own
// index. The claim is looked at again whenever it is asked for again, and when
// a pattern moves on; the restore is not held back, since it gives a controller
// back the index it had (the only case where that moves another controller is
// one that has taken it since).
//
// What the Mixer last wrote is not taken for what the controller has: SDL's
// table is shared, and the restore of another controller moves this one to a
// free slot without a word. So a claim that shows already is skipped only if
// the controller still has its index (PlayerIndex, a read of SDL's table and
// no write to the device); otherwise it is applied again, under the same rule
// for lights that another controller holds.
func (d *dev) syncPlayer(now time.Time) error {
	if !d.caps.PlayerLED {
		return nil
	}
	if want, ok := winner(&d.player); ok {
		if d.playerChanged && d.playerValid && d.playerShown == want && d.eff.PlayerIndex() == want {
			return nil
		}
		if want >= 0 && d.eff.PlayerIndexTaken(want) {
			return nil
		}
		if !d.playerChanged {
			d.playerOrig = d.eff.PlayerIndex()
		}
		if err := d.eff.SetPlayerIndex(want); err != nil {
			return d.failPlayer(err, now)
		}
		d.playerShown, d.playerValid, d.playerChanged = want, true, true
		return nil
	}
	if !d.playerChanged {
		return nil
	}
	if err := d.eff.SetPlayerIndex(d.playerOrig); err != nil {
		return d.failPlayer(err, now)
	}
	d.playerChanged, d.playerValid = false, false
	return nil
}

// levelDev returns the controller dv for a claim of class c on the LED or the
// player lights.
func (m *Mixer) levelDev(dv Device, c Class) (*dev, error) {
	d := m.devs[dv]
	if d == nil {
		return nil, ErrUnknownDevice
	}
	if !c.Valid() || c == Event {
		return nil, ErrClass
	}
	return d, nil
}

// claimLED makes the change to an LED claim of c and applies the result. The
// change is kept while the LED is in its cooldown, and applied when that ends.
func (m *Mixer) claimLED(dv Device, c Class, now time.Time, change func(d *dev)) error {
	d, err := m.levelDev(dv, c)
	if err != nil {
		return err
	}
	change(d)
	return d.syncLEDNow(now)
}

// claimPlayer is claimLED for the player lights.
func (m *Mixer) claimPlayer(dv Device, c Class, now time.Time, change func(d *dev)) error {
	d, err := m.levelDev(dv, c)
	if err != nil {
		return err
	}
	change(d)
	return d.syncPlayerNow(now)
}

// SetLED makes c claim the LED color: it shows while no stronger class
// claims it, until ReleaseLED. It is a request for the color, so when c's
// claim is the one that shows, the color is written even if the LED shows it
// already; SDL itself drops the repeat inside its window (askLED).
func (m *Mixer) SetLED(dv Device, c Class, color RGB, now time.Time) error {
	return m.claimLED(dv, c, now, func(d *dev) { d.led[c].hold(color); d.askLED(c) })
}

// BlinkLED makes c claim the LED for the timed pattern phases, which start
// now. When it ends, or in a phase that shows nothing, the claims below
// show: the underlying color comes back by itself. Like SetLED it writes the
// first color of the pattern again if the LED shows it already.
func (m *Mixer) BlinkLED(dv Device, c Class, phases []LEDPhase, now time.Time) error {
	return m.claimLED(dv, c, now, func(d *dev) { d.led[c].play(phases, now); d.askLED(c) })
}

// ReleaseLED gives up the LED claim of c. When no claim remains the LED is
// released by the rule of syncLED: #000000 if padmate colored a color LED,
// otherwise left alone.
func (m *Mixer) ReleaseLED(dv Device, c Class, now time.Time) error {
	return m.claimLED(dv, c, now, func(d *dev) { d.led[c].release() })
}

// SetPlayer makes c claim the player lights: index 0 to 7, or -1 for none.
// While no claim remains the controller has the index it had. An index that
// another controller holds is not shown, and the lights stay as they are
// (syncPlayer): the claim is kept, and tried again at the next request.
func (m *Mixer) SetPlayer(dv Device, c Class, index int, now time.Time) error {
	index = min(max(index, -1), 7)
	return m.claimPlayer(dv, c, now, func(d *dev) { d.player[c].hold(index) })
}

// PlayerPattern makes c claim the player lights for a timed pattern.
func (m *Mixer) PlayerPattern(dv Device, c Class, phases []PlayerPhase, now time.Time) error {
	return m.claimPlayer(dv, c, now, func(d *dev) { d.player[c].play(phases, now) })
}

// ReleasePlayer gives up the player-light claim of c.
func (m *Mixer) ReleasePlayer(dv Device, c Class, now time.Time) error {
	return m.claimPlayer(dv, c, now, func(d *dev) { d.player[c].release() })
}

// Cancel ends everything that class c has going on dv: its rumble program
// if that is the one running, with the motors stopped, and its LED and
// player-light claims.
func (m *Mixer) Cancel(dv Device, c Class, now time.Time) error {
	d := m.devs[dv]
	if d == nil {
		return ErrUnknownDevice
	}
	if !c.Valid() {
		return ErrClass
	}
	d.led[c].release()
	d.player[c].release()
	errs := []error{d.stopProgram(c, now)}
	errs = append(errs, d.syncLEDNow(now), d.syncPlayerNow(now))
	return errors.Join(errs...)
}

// StopRumble ends the rumble program of class c on dv, if that is the one
// running, and stops the motors; the LED and the player-light claims of c
// stay. It is Cancel for a caller that owns the rumble and the LED
// separately, as a script does (pad.rumble with 0 ms, pad.setLed).
func (m *Mixer) StopRumble(dv Device, c Class, now time.Time) error {
	d := m.devs[dv]
	if d == nil {
		return ErrUnknownDevice
	}
	if !c.Valid() {
		return ErrClass
	}
	return d.stopProgram(c, now)
}

// stopProgram ends the running rumble program if it is class c's.
func (d *dev) stopProgram(c Class, now time.Time) error {
	if !d.prog.active || d.prog.class != c {
		return nil
	}
	if !d.readyMotors(now) {
		d.prog = program{} // no write to motors that failed
		return nil
	}
	return d.endProgram(now)
}

// syncLEDNow applies the LED claims, if the LED may be written to at now.
func (d *dev) syncLEDNow(now time.Time) error {
	if ok, err := d.readyLED(now); !ok {
		return err
	}
	return d.syncLED(now)
}

// syncPlayerNow applies the player-light claims, if they may be written to at
// now.
func (d *dev) syncPlayerNow(now time.Time) error {
	if ok, err := d.readyPlayer(now); !ok {
		return err
	}
	return d.syncPlayer(now)
}

// Advance moves every controller on to now: the next step of a running
// program, the next phase of a pattern, the end of a cooldown. It returns the
// failures of the calls it made, joined; each has also put its group of
// effects, the motors, the LED or the player lights, into the cooldown.
func (m *Mixer) Advance(now time.Time) error {
	var errs []error
	for _, d := range m.devs {
		if err := d.advance(now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// advance moves the three groups of effects of d on, each on its own: a
// group that fails or cools does not hold the others up.
func (d *dev) advance(now time.Time) error {
	var errs []error
	if d.readyMotors(now) {
		errs = append(errs, d.advanceProgram(now))
	}
	if ok, err := d.readyLED(now); err != nil {
		errs = append(errs, err)
	} else if ok && d.advanceLED(now) {
		errs = append(errs, d.syncLED(now))
	}
	if ok, err := d.readyPlayer(now); err != nil {
		errs = append(errs, err)
	} else if ok && d.advancePlayer(now) {
		errs = append(errs, d.syncPlayer(now))
	}
	return errors.Join(errs...)
}

// quiet ends all the effects of d: the program, every claim, the motors, then
// the LED and the player lights by the release rules. It stops at the first
// failing call. A group in its cooldown is not written to at all: its state
// is dropped, and SDL stops its rumble when it expires or the gamepad closes.
func (d *dev) quiet(now time.Time) error {
	d.prog = program{}
	for c := range d.led {
		d.led[c].release()
		d.player[c].release()
	}
	if d.readyMotors(now) {
		if err := d.endProgram(now); err != nil {
			return err
		}
	}
	if err := d.syncLEDNow(now); err != nil {
		return err
	}
	return d.syncPlayerNow(now)
}

// Stop ends all the effects of dv, which stays added: the motors stop, the
// LED is released and the player lights restored. See Cancel for ending one
// class's.
func (m *Mixer) Stop(dv Device, now time.Time) error {
	d := m.devs[dv]
	if d == nil {
		return nil
	}
	return d.quiet(now)
}

// Remove stops dv as Stop does and forgets it. Removing a device that was
// not added does nothing.
func (m *Mixer) Remove(dv Device, now time.Time) error {
	err := m.Stop(dv, now)
	delete(m.devs, dv)
	return err
}

// Forget drops dv without a call to its controller: the program, the claims and
// what the Mixer believes about the lights are discarded as they are, and Has(dv)
// is false afterwards. It is for a controller that SDL has already removed, as
// Remove is for one that is still there. Nothing is left to stop on it: SDL
// stops the rumble itself when the handle closes (SDL_CloseJoystick sends the
// zero if an expiry was set), and the LED goes with the hardware. And no write
// may be made: the player index is an entry of SDL's global table, and
// SDL_SetJoystickPlayerIndex only checks that the handle is still valid, which
// it is until it closes. Writing the original index of a removed joystick puts
// its dead instance id back into the table (SDL_PrivateJoystickRemoved has
// cleared the entry already), nothing ever frees that slot again, and the
// controllers that connect later get higher indices. Forgetting a device that
// was not added does nothing.
func (m *Mixer) Forget(dv Device) { delete(m.devs, dv) }

// StopAll removes every controller, stopping each. It tries every one, and
// returns the failures joined.
func (m *Mixer) StopAll(now time.Time) error {
	var errs []error
	for dv := range m.devs {
		if err := m.Remove(dv, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
