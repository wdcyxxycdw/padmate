// Package haptic is the effect channel of a controller (M17, ADR-0065): the
// presets of the haptics a controller can give, and the Mixer that decides,
// per controller, which request owns the motors, the LED and the player
// lights at each moment.
//
// It is a pure package (invariant 2): it imports nothing of padmate, calls
// neither the clock nor SDL, and takes the time of every call as an
// argument. The Effector it drives is an interface; internal/input wraps an
// SDL gamepad in one and calls the Mixer from T-input.
package haptic

import "strconv"

// Limits and defaults. The durations are in milliseconds.
const (
	// MaxStepMs is the longest a step lasts. Every step is finite: SDL
	// treats a rumble of 0 ms as endless, so a step never has less than 1 ms
	// either.
	MaxStepMs = 5000
	// MaxPulses caps the pulses of a Train.
	MaxPulses = 64
	// MaxSteps caps the steps of a program, and the phases of a pattern.
	MaxSteps = 256

	// DefaultStrength, DefaultPulseMs and DefaultGapMs are what Train uses
	// for a value that is not positive: the defaults of profile.Feedback
	// (strength, shortMs, gapMs) that the pulse plan of M3 used.
	DefaultStrength = 0x7000
	DefaultPulseMs  = 70
	DefaultGapMs    = 100
)

// Preset is a named haptic: what a binding, a knob tick, a script or an alert
// can ask for without knowing the motors. The zero value is not a preset.
type Preset uint8

// The presets.
const (
	// Tick is a short, crisp click on the high-frequency motor.
	Tick Preset = iota + 1
	// Confirm is two short pulses on both motors.
	Confirm
	// Error is one long pulse on the low-frequency motor.
	Error
	// Pulse1 to Pulse4 are that many pulses on both motors, of the short
	// pulse and gap of the profile's feedback: the pulses of the layer
	// modes (HUD-16).
	Pulse1
	Pulse2
	Pulse3
	Pulse4
)

// presetNames are the names the profile schema, the engine and the script
// API share.
var presetNames = [...]string{
	Tick: "tick", Confirm: "confirm", Error: "error",
	Pulse1: "pulse1", Pulse2: "pulse2", Pulse3: "pulse3", Pulse4: "pulse4",
}

// Valid reports whether p is one of the presets.
func (p Preset) Valid() bool { return p >= Tick && p <= Pulse4 }

// String returns the name of p, such as "tick" or "pulse3"; an invalid
// preset is "preset(N)".
func (p Preset) String() string {
	if p.Valid() {
		return presetNames[p]
	}
	return "preset(" + strconv.Itoa(int(p)) + ")"
}

// ParsePreset is the inverse of String. Names are case sensitive.
func ParsePreset(name string) (Preset, bool) {
	for p := Tick; p <= Pulse4; p++ {
		if presetNames[p] == name {
			return p, true
		}
	}
	return 0, false
}

// Presets returns every preset, in the order of the schema's enum.
func Presets() []Preset {
	return []Preset{Tick, Confirm, Error, Pulse1, Pulse2, Pulse3, Pulse4}
}

// PresetNames returns the names of Presets, in the same order.
func PresetNames() []string {
	names := make([]string, 0, len(presetNames)-1)
	for _, p := range Presets() {
		names = append(names, p.String())
	}
	return names
}

// Step is one stretch of a rumble program: the motors and triggers run at
// these strengths for Ms milliseconds. A step whose strengths are all zero is
// a gap. The strengths are SDL's, 0 to 65535 (SDL_RumbleGamepad).
type Step struct {
	Low, High           uint16 // the low- and high-frequency motors
	LeftTrig, RightTrig uint16 // the trigger motors
	Ms                  uint16
}

// Steps returns the program of p at the strength master, with shortMs and gapMs
// for the pulses of Pulse1 to Pulse4 (a value that is not positive is
// DefaultPulseMs, DefaultGapMs). The table, which acceptance on the real
// controller may tune:
//
//	Tick      25 ms, the high motor
//	Confirm   40 ms, 60 ms gap, 40 ms; both motors
//	Error     300 ms, the low motor
//	PulseN    N pulses of shortMs with gapMs between them; both motors
//
// It returns nil for an invalid preset and for a master of 0, which is "off".
// The slice is the caller's.
func (p Preset) Steps(master uint16, shortMs, gapMs int) []Step {
	if !p.Valid() || master == 0 {
		return nil
	}
	switch p {
	case Tick:
		return []Step{{High: master, Ms: 25}}
	case Confirm:
		return []Step{{Low: master, High: master, Ms: 40}, {Ms: 60}, {Low: master, High: master, Ms: 40}}
	case Error:
		return []Step{{Low: master, Ms: 300}}
	}
	return Train(int(p-Pulse1)+1, shortMs, gapMs, master)
}

// Train returns n pulses of pulseMs on both motors at strength, with gapMs of
// silence between them and none after the last. A value that is not positive
// is the default (1 pulse, DefaultPulseMs, DefaultGapMs, DefaultStrength), as
// the pulse plan of M3 had it; n is at most MaxPulses and the times at most
// MaxStepMs.
func Train(n, pulseMs, gapMs int, strength uint16) []Step {
	n = min(max(n, 1), MaxPulses)
	if pulseMs <= 0 {
		pulseMs = DefaultPulseMs
	}
	if gapMs <= 0 {
		gapMs = DefaultGapMs
	}
	if strength == 0 {
		strength = DefaultStrength
	}
	pulse, gap := clampMs(pulseMs), clampMs(gapMs)
	steps := make([]Step, 0, 2*n-1)
	for i := range n {
		if i > 0 {
			steps = append(steps, Step{Ms: gap})
		}
		steps = append(steps, Step{Low: strength, High: strength, Ms: pulse})
	}
	return steps
}

// clampMs returns ms as a step duration: at least 1 and at most MaxStepMs.
func clampMs(ms int) uint16 { return uint16(min(max(ms, 1), MaxStepMs)) }
