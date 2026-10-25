package haptic

import "time"

// The lights of an alert (IN-12, the low battery): three red flashes of a
// color LED, or, for a controller that has only player lights, three flashes of
// all four lights, with the lights out in between. They run next to the buzz of
// the alert and give the lights back to what was showing when they end.
const (
	alertFlashMs = 250
	alertDarkMs  = 250
	alertFlashes = 3
)

// AlertFlashes returns the LED phases of an alert: red flashes.
func AlertFlashes() []LEDPhase {
	return Blink(RGB{0xFF, 0, 0}, alertFlashMs, alertDarkMs, alertFlashes)
}

// AlertPlayerFlashes returns the player-light phases of an alert for a
// controller without a color LED: all four lights (index 3 lights four of
// them) flash, and are out in between.
func AlertPlayerFlashes() []PlayerPhase {
	phases := make([]PlayerPhase, 0, 2*alertFlashes-1)
	for i := range alertFlashes {
		if i > 0 {
			phases = append(phases, PlayerPhase{Value: -1, Show: true, Ms: alertDarkMs})
		}
		phases = append(phases, PlayerPhase{Value: 3, Show: true, Ms: alertFlashMs})
	}
	return phases
}

// AlertLight says how a controller flashes for an alert.
type AlertLight uint8

// The ways a controller flashes.
const (
	// AlertNoLight: it has nothing to flash. A mono LED is not a flash, as for
	// Identify: its brightness is not a blink, and the Mixer does not turn it
	// off again (Mixer.ReleaseLED).
	AlertNoLight AlertLight = iota
	// AlertLED: it flashes its color LED red.
	AlertLED
	// AlertPlayer: it has no color LED but player lights, and flashes those.
	AlertPlayer
)

func (l AlertLight) String() string {
	switch l {
	case AlertLED:
		return "led"
	case AlertPlayer:
		return "player"
	}
	return "none"
}

// AlertLightFor returns how a controller that can do caps flashes for an alert.
func AlertLightFor(caps Caps) AlertLight {
	switch {
	case caps.RGBLED:
		return AlertLED
	case caps.PlayerLED:
		return AlertPlayer
	}
	return AlertNoLight
}

// AlertBlink starts the lights of an alert on dv at now, with the priority of
// the Alert class, and returns which it started. It claims the LED or the
// player lights for a timed pattern, so the layer color and the player lights of
// the mode come back by themselves when it ends. It is the lights only: the
// buzz of an alert is Play with the Alert class. An error is a failing call to
// the controller, which starts the cooldown of its LED or player lights.
func (m *Mixer) AlertBlink(dv Device, now time.Time) (AlertLight, error) {
	d := m.devs[dv]
	if d == nil {
		return AlertNoLight, ErrUnknownDevice
	}
	light := AlertLightFor(d.caps)
	switch light {
	case AlertLED:
		return light, m.BlinkLED(dv, Alert, AlertFlashes(), now)
	case AlertPlayer:
		return light, m.PlayerPattern(dv, Alert, AlertPlayerFlashes(), now)
	}
	return light, nil
}
