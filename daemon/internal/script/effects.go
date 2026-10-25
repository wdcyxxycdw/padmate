package script

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// The limits of the controller effects of a script (SCR-23, API 0.17).
const (
	// effectsPerSecond is how many effect calls a runtime may make in a second
	// of the slot clock. A call that stops something is not counted.
	effectsPerSecond = 20
	// rumbleMinMs and rumbleMaxMs limit the time of pad.rumble and
	// pad.rumbleTriggers; 0 is not a time but stops the rumble. The longest is
	// the Mixer's longest step: nothing a script starts outlasts it.
	rumbleMinMs = 1
	rumbleMaxMs = haptic.MaxStepMs
)

// Effects is where the rumble and LED calls of a script go (SCR-23): the
// effect queue of padmated. The host calls it on the slot goroutine within
// the script's budget, so no method may block; the gamepad is named by the
// connection id its slot has (PadState.Info.ID).
//
// The Mixer behind it plays a script's effects in the Script class, which never
// cuts the pulses of a layer. A script's effects are all stopped when its
// runtime stops being the active one (Cancel), so none outlives the script.
type Effects interface {
	// Switches reports whether the global rumble switch (with a strength above 0
	// in the profile that the slot runs) and the global led switch are on
	// (ADR-0067). A script's calls reach the gamepad only through them.
	Switches(slot uint8) (rumble, led bool)
	// Rumble plays steps on the gamepad as the script's rumble, in place of the
	// one it played before.
	Rumble(slot uint8, dev input.DeviceID, steps []haptic.Step)
	// Haptic plays a preset, with the strength and the pulses of the profile's
	// feedback, as the script's rumble.
	Haptic(slot uint8, dev input.DeviceID, preset haptic.Preset)
	// StopRumble ends the script's rumble on the gamepad.
	StopRumble(slot uint8, dev input.DeviceID)
	// Light makes the LED of the gamepad show color for the script, over the
	// layer's color; ReleaseLight gives the claim up.
	Light(slot uint8, dev input.DeviceID, color haptic.RGB)
	ReleaseLight(slot uint8, dev input.DeviceID)
	// Cancel ends everything the script has on the gamepad: its rumble and its
	// LED claim.
	Cancel(slot uint8, dev input.DeviceID)
}

// effectsHolder lets Environment keep an interface in an atomic.Pointer.
type effectsHolder struct{ effects Effects }

// SetEffects connects the rumble and LED calls of every host using e to
// effects; nil disconnects them. Hosts read it on each call.
func (e *Environment) SetEffects(effects Effects) {
	if effects == nil {
		e.effects.Store(nil)
		return
	}
	e.effects.Store(&effectsHolder{effects: effects})
}

func (e *Environment) loadEffects() Effects {
	if e == nil {
		return nil
	}
	if holder := e.effects.Load(); holder != nil {
		return holder.effects
	}
	return nil
}

// rateWindow counts the calls of one second of the slot clock against a limit,
// as the toasts of the hud API and the effect calls of a script are counted.
type rateWindow struct {
	start   clock.Time
	count   int
	dropped bool // a call of this window was dropped and reported
}

// allow counts one call against limit calls per second. It returns whether the
// call may go on, and, for the first call that a window drops, warn.
func (w *rateWindow) allow(now clock.Time, limit int) (ok, warn bool) {
	if w.count == 0 || now-w.start >= clock.Time(time.Second) || now < w.start {
		*w = rateWindow{start: now}
	}
	if w.count < limit {
		w.count++
		return true, false
	}
	if !w.dropped {
		w.dropped = true
		return false, true
	}
	return false, false
}

// rumbleLevel is what one rumble channel of a script, the motors or the
// triggers, was last asked to do: both strengths until a time. The script
// asks for the two channels in separate calls, and the Mixer plays one
// program, so the host merges what is still running into the program it sends.
type rumbleLevel struct {
	a, b  uint16
	until clock.Time
}

// remaining returns how long the channel still runs at now, rounded up to
// whole milliseconds, 0 when it does not.
func (c rumbleLevel) remaining(now clock.Time) uint16 {
	if c.a|c.b == 0 || c.until <= now {
		return 0
	}
	ms := (time.Duration(c.until-now) + time.Millisecond - 1) / time.Millisecond
	return uint16(min(max(ms, rumbleMinMs), rumbleMaxMs))
}

// rumbleProgram is the program that plays the channels of a script as they
// stand at now: the motors and the triggers together for as long as both run,
// then what is left of the longer one. It is nil when neither runs.
func rumbleProgram(now clock.Time, body, trig rumbleLevel) []haptic.Step {
	bl, tl := body.remaining(now), trig.remaining(now)
	switch {
	case bl == 0 && tl == 0:
		return nil
	case tl == 0:
		return []haptic.Step{{Low: body.a, High: body.b, Ms: bl}}
	case bl == 0:
		return []haptic.Step{{LeftTrig: trig.a, RightTrig: trig.b, Ms: tl}}
	}
	common := min(bl, tl)
	steps := []haptic.Step{{Low: body.a, High: body.b, LeftTrig: trig.a, RightTrig: trig.b, Ms: common}}
	if bl > common {
		steps = append(steps, haptic.Step{Low: body.a, High: body.b, Ms: bl - common})
	}
	if tl > common {
		steps = append(steps, haptic.Step{LeftTrig: trig.a, RightTrig: trig.b, Ms: tl - common})
	}
	return steps
}

// effectCall says what an effect call does in the runtime's current state, as
// hudCall does for the hud: it throws in setup and while a macro loads, where
// nothing is on the gamepad yet, and does nothing where nothing should happen
// (describe, teardown, a runtime not active).
func (h *Host) effectCall(name string) bool {
	if h.mode == "describe" {
		return false
	}
	switch {
	case h.setup:
		panic(h.VM.NewTypeError("%s is not available in setup; call it from onActivate or a handler", name))
	case h.macroLoad:
		panic(h.VM.NewTypeError("%s is not available while the macro loads; call it from the macro's function", name))
	}
	return h.active && !h.candidate && !h.disposed
}

// effectTarget is effectCall for a call that reaches the gamepad: it also needs
// the effects connected and a gamepad in the slot.
func (h *Host) effectTarget(name string) (sink Effects, dev input.DeviceID, ok bool) {
	if !h.effectCall(name) {
		return nil, 0, false
	}
	sink = h.environment.loadEffects()
	if sink == nil || !h.padState.Connected || h.padState.Info.ID == 0 {
		return nil, 0, false
	}
	return sink, h.padState.Info.ID, true
}

// effectsAvailable is host.has("rumble") and host.has("led"): the effects are
// connected, the gamepad in the slot can do it, and the global switch
// (config.json feedback, ADR-0067) is on. A describe runtime never reaches a
// gamepad, so it has none.
func (h *Host) effectsAvailable(feature Feature) bool {
	if h.mode == "describe" || !h.padState.Connected {
		return false
	}
	sink := h.environment.loadEffects()
	if sink == nil {
		return false
	}
	caps := h.padState.Info.Capabilities.HapticCaps()
	rumble, led := sink.Switches(h.base.owner.Slot)
	if feature == FeatureLED {
		return caps.LED && led
	}
	return caps.Rumble && rumble
}

// allowEffect counts a call that starts an effect against the runtime's budget
// of effectsPerSecond a second, logging one warning for a second that drops any.
func (h *Host) allowEffect(name string) bool {
	ok, warn := h.effectRate.allow(h.clock.Now(), effectsPerSecond)
	if warn {
		h.logs.push(h.logEntry("warn", "", fmt.Sprintf("%s: more than %d effect calls in a second; the rest of this second are dropped", name, effectsPerSecond)))
	}
	return ok
}

// bindEffectDevice notes that the script's effects go to dev. A script whose
// slot got another gamepad since it last made one leaves nothing on the first.
func (h *Host) bindEffectDevice(sink Effects, dev input.DeviceID) {
	if h.effectDev != 0 && h.effectDev != dev {
		h.cancelEffectsOn(sink, h.effectDev)
	}
	h.effectDev = dev
}

// cancelEffectsOn ends everything the script has on dev, if it has anything.
func (h *Host) cancelEffectsOn(sink Effects, dev input.DeviceID) {
	if h.rumbling || h.lit {
		sink.Cancel(h.base.owner.Slot, dev)
	}
	h.rumbling, h.lit = false, false
	h.rumbleBody, h.rumbleTrig = rumbleLevel{}, rumbleLevel{}
}

// cancelEffects ends what the script's effects have going on its gamepad, at
// each way its runtime stops being the active one: a pause, a release, a
// restart after an error, a new runtime for a reload, the end. It is the
// hold-the-motors counterpart of releasing the keys (invariant 1): no effect of
// a script outlives it. Calling it twice is harmless.
func (h *Host) cancelEffects() {
	dev := h.effectDev
	if dev != 0 {
		if sink := h.environment.loadEffects(); sink != nil {
			h.cancelEffectsOn(sink, dev)
		}
	}
	h.effectDev = 0
	h.rumbling, h.lit = false, false
	h.rumbleBody, h.rumbleTrig = rumbleLevel{}, rumbleLevel{}
}

// CancelEffects ends what the script has going on its gamepad now. The owner
// of a runtime calls it before the runtime that replaces this one can act, so
// that its cancel does not come after (and end) the effects of the new one:
// Lifecycle.Swap does, and padmated before it swaps in a new Lifecycle. Calling
// it again, or after ReleaseAll, Suspend or Dispose, is harmless. It must run on
// the Host owner goroutine.
func (h *Host) CancelEffects() { h.cancelEffects() }

// effectLevel reads a strength from 0 to 1 that a call gave: any number, with
// the values outside the range limited to it, as SDL's strengths are 16 bits.
func (h *Host) effectLevel(name, what string, v goja.Value) uint16 {
	n, ok := exportNumber(v)
	if !ok {
		panic(h.VM.NewTypeError("%s: %s must be a number from 0 to 1", name, what))
	}
	return uint16(math.Round(min(max(n, 0), 1) * math.MaxUint16))
}

// effectMs reads the time in ms that a call gave, rounded: 0 means stop the
// rumble, anything else is limited to rumbleMinMs through rumbleMaxMs.
func (h *Host) effectMs(name string, v goja.Value) (ms uint16, stop bool) {
	n, ok := exportNumber(v)
	if !ok {
		panic(h.VM.NewTypeError("%s: ms must be a finite number", name))
	}
	n = math.Round(min(max(n, -rumbleMaxMs), rumbleMaxMs))
	if n == 0 {
		return 0, true
	}
	return uint16(min(max(n, rumbleMinMs), rumbleMaxMs)), false
}

func (h *Host) padRumble(call goja.FunctionCall) goja.Value {
	low := h.effectLevel("pad.rumble", "low", call.Argument(0))
	high := h.effectLevel("pad.rumble", "high", call.Argument(1))
	ms, stop := h.effectMs("pad.rumble", call.Argument(2))
	h.rumbleChannel("pad.rumble", &h.rumbleBody, low, high, ms, stop, func(c haptic.Caps) bool { return c.Rumble })
	return goja.Undefined()
}

func (h *Host) padRumbleTriggers(call goja.FunctionCall) goja.Value {
	left := h.effectLevel("pad.rumbleTriggers", "left", call.Argument(0))
	right := h.effectLevel("pad.rumbleTriggers", "right", call.Argument(1))
	ms, stop := h.effectMs("pad.rumbleTriggers", call.Argument(2))
	h.rumbleChannel("pad.rumbleTriggers", &h.rumbleTrig, left, right, ms, stop, func(c haptic.Caps) bool { return c.TriggerRumble })
	return goja.Undefined()
}

// rumbleChannel sets a channel of the script's rumble, the motors or the
// triggers, to a and b for ms, or ends it, and sends the program of what runs
// as a result. can says whether the gamepad has the channel.
func (h *Host) rumbleChannel(name string, ch *rumbleLevel, a, b, ms uint16, stop bool, can func(haptic.Caps) bool) {
	sink, dev, ok := h.effectTarget(name)
	if !ok {
		return
	}
	now := h.clock.Now()
	if stop || a|b == 0 {
		// Ending the channel is always allowed and costs no budget. It sends
		// only what remains of the other channel, to the gamepad the script's
		// rumble is on.
		*ch = rumbleLevel{}
		if h.rumbling {
			h.sendRumble(sink, h.effectDev, now)
		}
		return
	}
	rumble, _ := sink.Switches(h.base.owner.Slot)
	if !rumble || !can(h.padState.Info.Capabilities.HapticCaps()) || !h.allowEffect(name) {
		return
	}
	h.bindEffectDevice(sink, dev)
	*ch = rumbleLevel{a: a, b: b, until: now.Add(time.Duration(ms) * time.Millisecond)}
	h.sendRumble(sink, dev, now)
}

// sendRumble sends the merged program of the script's two channels at now, or
// stops the script's rumble when neither runs.
func (h *Host) sendRumble(sink Effects, dev input.DeviceID, now clock.Time) {
	slot := h.base.owner.Slot
	if steps := rumbleProgram(now, h.rumbleBody, h.rumbleTrig); steps != nil {
		h.rumbling = true
		sink.Rumble(slot, dev, steps)
		return
	}
	h.rumbling = false
	sink.StopRumble(slot, dev)
}

func (h *Host) padHaptic(call goja.FunctionCall) goja.Value {
	preset, ok := call.Argument(0).Export().(string)
	p, known := haptic.ParsePreset(preset)
	if !ok {
		panic(h.VM.NewTypeError("pad.haptic: preset must be one of %s", presetList()))
	}
	if !known {
		panic(h.VM.NewTypeError("pad.haptic: unknown preset %q; use one of %s", preset, presetList()))
	}
	sink, dev, ok := h.effectTarget("pad.haptic")
	if !ok {
		return goja.Undefined()
	}
	if rumble, _ := sink.Switches(h.base.owner.Slot); !rumble || !h.padState.Info.Capabilities.HapticCaps().Rumble || !h.allowEffect("pad.haptic") {
		return goja.Undefined()
	}
	h.bindEffectDevice(sink, dev)
	// A preset is a program of its own: it replaces what the channels had.
	h.rumbleBody, h.rumbleTrig = rumbleLevel{}, rumbleLevel{}
	h.rumbling = true
	sink.Haptic(h.base.owner.Slot, dev, p)
	return goja.Undefined()
}

// presetList is the names of the presets for an error message.
func presetList() string { return strings.Join(haptic.PresetNames(), ", ") }

func (h *Host) padSetLed(call goja.FunctionCall) goja.Value {
	arg := call.Argument(0)
	var color haptic.RGB
	release := goja.IsNull(arg)
	if !release {
		text, ok := arg.Export().(string)
		if ok {
			color, ok = haptic.ParseColor(text)
		}
		if !ok {
			panic(h.VM.NewTypeError(`pad.setLed: color must be "#RRGGBB" or null`))
		}
	}
	sink, dev, ok := h.effectTarget("pad.setLed")
	if !ok {
		return goja.Undefined()
	}
	if release {
		// Giving the claim up is always allowed and costs no budget.
		if h.lit {
			h.lit = false
			sink.ReleaseLight(h.base.owner.Slot, dev)
		}
		return goja.Undefined()
	}
	if _, led := sink.Switches(h.base.owner.Slot); !led || !h.padState.Info.Capabilities.HapticCaps().LED || !h.allowEffect("pad.setLed") {
		return goja.Undefined()
	}
	h.bindEffectDevice(sink, dev)
	h.lit = true
	sink.Light(h.base.owner.Slot, dev, color)
	return goja.Undefined()
}
