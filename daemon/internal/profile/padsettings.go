package profile

import (
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
)

// Source says where a controller's setting comes from.
type Source uint8

// The sources, from the weakest. The weakest, SourceDefault, is the zero
// value.
const (
	// SourceDefault is the built-in value: the family's default function
	// key, the switches on, the family's built-in emergency combo.
	SourceDefault Source = iota
	// SourceLegacy is a retired member of config.json that is not
	// per controller: the top-level functionKey, feedback or
	// safety.panic (ADR-0017).
	SourceLegacy
	// SourceDevice is the controller's own entry in devices.<DeviceKey>.
	SourceDevice
)

func (s Source) String() string {
	switch s {
	case SourceLegacy:
		return "legacy"
	case SourceDevice:
		return "device"
	}
	return "default"
}

// LegacyPad says which of the retired global members, which moved to the
// controllers (ADR-0017), a config.json has. The daemon still reads them.
type LegacyPad struct {
	FunctionKey bool // the top-level functionKey
	Feedback    bool // the top-level feedback
	Panic       bool // safety.panic
}

// Any reports whether the file has any of them.
func (l LegacyPad) Any() bool { return l.FunctionKey || l.Feedback || l.Panic }

// PadSettings are the settings of one controller after resolution.
type PadSettings struct {
	// Family is the family the settings were resolved for.
	Family padfamily.Family
	// FunctionKey is the function key (ADR-0012): a button of the family's
	// FunctionKeys. It is never Invalid.
	FunctionKey       control.Control
	FunctionKeySource Source
	// Feedback holds the four switches (ADR-0015).
	Feedback FeedbackSwitches
	// FeedbackSource is the strongest source among the switches.
	FeedbackSource Source
	// Panic is the emergency combo as configured, or the family's built-in
	// one. It does not know the controller's buttons: the caller falls back to
	// the built-in combo when the controller lacks one of Panic's buttons
	// (input.DeviceInfo.MayHaveButton), as the retired safety.panic always did.
	Panic       PanicCombo
	PanicSource Source
}

// DefaultPanic is the built-in emergency combo of family f.
func DefaultPanic(f padfamily.Family) PanicCombo {
	return PanicCombo{Buttons: f.DefaultPanic(), HoldMs: padfamily.DefaultPanicHoldMs}
}

// PadSettings resolves the settings of the controller with DeviceKey key
// (ADR-0017). A family that is Unknown is taken from the key's GUID, and if
// that tells nothing, as Standard; the family a caller knows better,
// from a DeviceInfo, is passed as is. Each setting has the first of these:
//
//  1. devices.<key> in config.json: its functionKey if the family allows it
//     (one it does not allow, such as a stale value after the grip changed,
//     is skipped), each feedback switch that is set, its panic if Usable.
//  2. The retired top-level members, where they apply: functionKey only for
//     a controller with the standard layout (Standard or Unknown: the Joy-Cons
//     have their own defaults, a pair too), feedback for every controller,
//     the safety.panic entry of the family for every controller if Usable.
//  3. The family's default (padfamily): function key, all switches on, the
//     built-in combo.
//
// It does not look at the controller's buttons or read the clock. A nil
// Config resolves to the defaults.
func (c *Config) PadSettings(key string, family padfamily.Family) PadSettings {
	if family == padfamily.Unknown {
		family = padfamily.FromGUID(KeyGUID(key), false)
	}
	var dev DeviceSettings
	if c != nil {
		dev = c.Devices[key]
	}
	out := PadSettings{Family: family, Feedback: DefaultFeedbackSwitches(), Panic: DefaultPanic(family)}

	// The function key.
	keys := family.FunctionKeys()
	out.FunctionKey = keys.Default
	if own, ok := control.Parse(dev.FunctionKey); ok && own.String() == dev.FunctionKey && keys.Allows(own) {
		out.FunctionKey, out.FunctionKeySource = own, SourceDevice
	} else if c != nil && (family == padfamily.Standard || family == padfamily.Unknown) {
		if legacy := c.FunctionKeyOrDefault(); keys.Allows(legacy) && legacy != keys.Default {
			out.FunctionKey, out.FunctionKeySource = legacy, SourceLegacy
		}
	}

	// The feedback switches, one by one.
	out.Feedback, out.FeedbackSource = c.PadFeedback(key)

	// The emergency combo.
	if dev.Panic != nil && dev.Panic.Usable() {
		out.Panic, out.PanicSource = clonePanic(*dev.Panic), SourceDevice
	} else if c != nil {
		if legacy, ok := c.Safety.Panic[family.PanicFamily()]; ok && legacy.Usable() {
			out.Panic, out.PanicSource = clonePanic(legacy), SourceLegacy
		}
	}
	return out
}

// PadFeedback resolves only the four feedback switches of the controller with
// DeviceKey key, as PadSettings does, and the strongest source among them. The
// switches do not depend on the family, so no family is asked for. The slots'
// engines read it at every feedback state and haptic (ADR-0015), so it
// allocates nothing. A nil Config resolves to all on.
func (c *Config) PadFeedback(key string) (FeedbackSwitches, Source) {
	out, source := DefaultFeedbackSwitches(), SourceDefault
	if c == nil {
		return out, source
	}
	out = c.Feedback
	var own *FeedbackOverrides
	if dev, ok := c.Devices[key]; ok {
		own = dev.Feedback
	}
	set := func(value *bool, over *bool) {
		switch {
		case over != nil:
			*value, source = *over, SourceDevice
		case !*value && source < SourceLegacy:
			source = SourceLegacy
		}
	}
	if own == nil {
		own = &FeedbackOverrides{}
	}
	set(&out.Rumble, own.Rumble)
	set(&out.LED, own.Led)
	set(&out.Pause, own.Pause)
	set(&out.LowBattery, own.LowBattery)
	return out, source
}

func clonePanic(p PanicCombo) PanicCombo {
	p.Buttons = slices.Clone(p.Buttons)
	return p
}

// Materialized returns the entry of devices.<key> with the settings the
// retired top-level members give the controller written into it, where they
// differ from the defaults of its family: the function key if SourceLegacy,
// each feedback switch that is false by the top-level feedback, the combo if
// SourceLegacy and not the family's built-in one. What the entry sets itself
// stays. Resolving the result
// without the retired members gives the same PadSettings as resolving the
// entry with them (TestMaterializedKeepsPadSettings). It is what a
// migration writes before it drops the retired members.
func (c *Config) Materialized(key string, family padfamily.Family) DeviceSettings {
	dev := c.Devices[key]
	if family == padfamily.Unknown {
		family = padfamily.FromGUID(KeyGUID(key), false)
	}
	s := c.PadSettings(key, family)
	if s.FunctionKeySource == SourceLegacy {
		dev.FunctionKey = s.FunctionKey.String()
	}
	fb := FeedbackOverrides{}
	if dev.Feedback != nil {
		fb = *dev.Feedback
	}
	set := func(own **bool, value bool) {
		if *own == nil && !value {
			f := false
			*own = &f
		}
	}
	set(&fb.Rumble, s.Feedback.Rumble)
	set(&fb.Led, s.Feedback.LED)
	set(&fb.Pause, s.Feedback.Pause)
	set(&fb.LowBattery, s.Feedback.LowBattery)
	if fb != (FeedbackOverrides{}) {
		dev.Feedback = &fb
	}
	if def := DefaultPanic(family); s.PanicSource == SourceLegacy && (s.Panic.HoldMs != def.HoldMs || !slices.Equal(s.Panic.Buttons, def.Buttons)) {
		p := clonePanic(s.Panic)
		dev.Panic = &p
	}
	return dev
}
