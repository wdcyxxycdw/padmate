package haptic

import (
	"errors"
	"time"
)

// The pattern of Identify (IN-13): three buzzes on both motors, three white
// flashes of the color LED, and a chase over the four player lights for a
// controller that has those and no color LED. The programs run side by side:
// the buzzes last 750 ms (three of 150 ms, two rests of 150 ms between), the
// flashes 1250 ms (three of 250 ms, two dark phases of 250 ms between), the
// chase 1500 ms (four lights of 375 ms), so Identify is over after 1.5 s at
// the most.
const (
	identifyBuzzMs   = 150
	identifyRestMs   = 150
	identifyBuzzes   = 3
	identifyFlashMs  = 250
	identifyDarkMs   = 250
	identifyFlashes  = 3
	identifyChaseMs  = 375
	identifyChaseLen = 4
)

// IdentifySteps returns the buzzes of Identify at DefaultStrength.
func IdentifySteps() []Step {
	steps := make([]Step, 0, 2*identifyBuzzes-1)
	for i := range identifyBuzzes {
		if i > 0 {
			steps = append(steps, Step{Ms: identifyRestMs})
		}
		steps = append(steps, Step{Low: DefaultStrength, High: DefaultStrength, Ms: identifyBuzzMs})
	}
	return steps
}

// IdentifyFlashes returns the LED phases of Identify: white flashes.
func IdentifyFlashes() []LEDPhase {
	return Blink(RGB{0xFF, 0xFF, 0xFF}, identifyFlashMs, identifyDarkMs, identifyFlashes)
}

// IdentifyChase returns the player-light phases of Identify for a controller
// that has the lights of players 1 to 4 to itself: the four in turn. Identify
// itself leaves out the lights that another controller holds (Mixer.Identify).
func IdentifyChase() []PlayerPhase {
	phases := make([]PlayerPhase, identifyChaseLen)
	for i := range phases {
		phases[i] = PlayerPhase{Value: i, Show: true, Ms: identifyChaseMs}
	}
	return phases
}

// freeChase returns IdentifyChase without the lights of the player indices
// that another controller has. SDL's player indices are one table for all
// controllers: giving index n to this one moves the controller that has n to
// the first free slot, and giving this one its own index back does not always
// put that one back (with three controllers the other two end up swapped), so
// a chase over a held index would renumber the other controllers, and change
// their lights, for good. What remains is the lights that nobody holds, and
// the one that this controller has itself.
func (d *dev) freeChase() []PlayerPhase {
	var free []PlayerPhase
	for _, p := range IdentifyChase() {
		if !d.eff.PlayerIndexTaken(p.Value) {
			free = append(free, p)
		}
	}
	return free
}

// Cues says which cues a controller can give for Identify: it buzzes, it
// flashes its color LED, or, when it has no color LED, it chases its player
// lights. When none is true there is nothing it can show.
type Cues struct {
	Rumble bool `json:"rumble"`
	LED    bool `json:"led"`
	Player bool `json:"player"`
}

// Any reports whether the controller can give any cue.
func (c Cues) Any() bool { return c.Rumble || c.LED || c.Player }

// CuesFor returns the cues of a controller that can do caps, alone among the
// controllers. A mono LED gives no cue: its brightness is not a flash, and the
// Mixer does not turn it off again (see Mixer.ReleaseLED). Mixer.Identify
// returns what it started, which is fewer player lights, or none, when other
// controllers hold the player indices (freeChase).
func CuesFor(caps Caps) Cues {
	return Cues{Rumble: caps.Rumble, LED: caps.RGBLED, Player: caps.PlayerLED && !caps.RGBLED}
}

// Identify starts the Identify-class cues on dv at now and returns which it
// started. Identify is an explicit action of the user, so it ignores the
// profile (the caller never asks it of the profile's switches), wins
// everything else, and tries a controller again whose motors, LED or player
// lights are in their cooldown. The layer color and the player lights come
// back when it ends. The chase runs over the player lights that no other
// controller holds, and not at all when fewer than two are left: then Player is
// false, as the controller shows nothing that tells it from the others. An
// error is the first failing call to the controller.
func (m *Mixer) Identify(dv Device, now time.Time) (Cues, error) {
	d := m.devs[dv]
	if d == nil {
		return Cues{}, ErrUnknownDevice
	}
	cues := CuesFor(d.caps)
	d.coolMotors, d.coolLED, d.coolPlayer = time.Time{}, time.Time{}, time.Time{}
	var errs []error
	if cues.Rumble {
		if _, err := m.Play(dv, Identify, IdentifySteps(), now); err != nil {
			errs = append(errs, err)
		}
	}
	if cues.LED {
		if err := m.BlinkLED(dv, Identify, IdentifyFlashes(), now); err != nil {
			errs = append(errs, err)
		}
	}
	if cues.Player {
		if chase := d.freeChase(); len(chase) < 2 {
			cues.Player = false
		} else if err := m.PlayerPattern(dv, Identify, chase, now); err != nil {
			errs = append(errs, err)
		}
	}
	return cues, errors.Join(errs...)
}
