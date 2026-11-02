package profile

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
)

// padGUID lays out an SDL GUID: bus, CRC of the name, vendor, 0, product, 0.
func padGUID(bus, vendor, product uint16) string {
	return fmt.Sprintf("%02x%02x0000%02x%02x0000%02x%02x000000000000", bus&0xff, bus>>8, vendor&0xff, vendor>>8, product&0xff, product>>8)
}

// The DeviceKeys of the tests: an Xbox pad and a PlayStation pad, and Joy-Con 2s.
var (
	keyXbox       = padGUID(0x03, 0x045e, 0x0b13) + "/s:XB1"
	keyPS         = padGUID(0x03, 0x054c, 0x0ce6) + "/s:PS1"
	keyJoyConL    = padGUID(0x05, 0x057e, 0x2067) + "/s:XKW1"
	keyJoyConR    = padGUID(0x05, 0x057e, 0x2066) + "/s:XKW2"
	keyJoyConPair = padGUID(0x05, 0x057e, 0x2068) + "/s:XKW1+XKW2"
)

func mustConfig(t *testing.T, doc string) *Config {
	t.Helper()
	c, ds := ParseConfig([]byte(doc))
	if c == nil || ds.HasErrors() {
		t.Fatalf("%s: %v", doc, ds)
	}
	return c
}

func buttons(p PanicCombo) string { return strings.Join(p.Buttons, "+") }

// TestPadSettingsDefaults: with no config, each family has its default
// function key (the owner's choice for the Joy-Cons), all switches on and
// the built-in emergency combo.
func TestPadSettingsDefaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      *Config
		key      string
		family   padfamily.Family
		wantKey  control.Control
		wantPane string
	}{
		{"xbox", DefaultConfig(), keyXbox, padfamily.Standard, control.Start, "back+start"},
		{"nil config", nil, keyXbox, padfamily.Standard, control.Start, "back+start"},
		{"joycon L", DefaultConfig(), keyJoyConL, padfamily.JoyConLeft, control.LT, "paddle2+paddle4"},
		{"joycon R", DefaultConfig(), keyJoyConR, padfamily.JoyConRight, control.RT, "paddle3+paddle1"},
		{"pair", DefaultConfig(), keyJoyConPair, padfamily.JoyConPair, control.Back, "back+start"},
		// A family that is not known is read from the key.
		{"unknown family, xbox key", DefaultConfig(), keyXbox, padfamily.Unknown, control.Start, "back+start"},
		{"unknown family, joycon R key", DefaultConfig(), keyJoyConR, padfamily.Unknown, control.RT, "paddle3+paddle1"},
		{"unknown family, pair key", DefaultConfig(), keyJoyConPair, padfamily.Unknown, control.Back, "back+start"},
		{"unknown family, no key", DefaultConfig(), "", padfamily.Unknown, control.Start, "back+start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.cfg.PadSettings(tc.key, tc.family)
			if s.FunctionKey != tc.wantKey || s.FunctionKeySource != SourceDefault {
				t.Errorf("FunctionKey = %s (%s); want %s (default)", s.FunctionKey, s.FunctionKeySource, tc.wantKey)
			}
			if s.Feedback != DefaultFeedbackSwitches() || s.FeedbackSource != SourceDefault {
				t.Errorf("Feedback = %+v (%s); want all on (default)", s.Feedback, s.FeedbackSource)
			}
			if buttons(s.Panic) != tc.wantPane || s.Panic.HoldMs != 2000 || s.PanicSource != SourceDefault {
				t.Errorf("Panic = %v hold %d (%s); want %s, 2000 (default)", s.Panic.Buttons, s.Panic.HoldMs, s.PanicSource, tc.wantPane)
			}
		})
	}
}

// TestPadSettingsZeroConfig: a Config built by hand has no function key set,
// which means the default, as FunctionKeyOrDefault does.
func TestPadSettingsZeroConfig(t *testing.T) {
	if s := (&Config{}).PadSettings(keyPS, padfamily.Standard); s.FunctionKey != control.Start || s.FunctionKeySource != SourceDefault {
		t.Errorf("FunctionKey = %s (%s)", s.FunctionKey, s.FunctionKeySource)
	}
}

// TestPadSettingsResolution: the controller's own entry beats the retired
// top-level members, which beat the family's defaults; and each controller
// has its own.
func TestPadSettingsResolution(t *testing.T) {
	cfg := mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,
		"functionKey":"back",
		"feedback":{"rumble":false,"lowBattery":false},
		"safety":{"panic":{"standard":{"buttons":["back","south"],"holdMs":1500},"joyconRight":{"buttons":["south","east"],"holdMs":900}}},
		"devices":{
			%q:{"functionKey":"start","feedback":{"rumble":true,"led":false},"panic":{"buttons":["start","north"],"holdMs":700}},
			%q:{"functionKey":"rightshoulder"},
			%q:{"functionKey":"start"}
		}}`, keyXbox, keyJoyConR, keyJoyConPair))

	t.Run("controller with its own entry", func(t *testing.T) {
		s := cfg.PadSettings(keyXbox, padfamily.Standard)
		if s.FunctionKey != control.Start || s.FunctionKeySource != SourceDevice {
			t.Errorf("FunctionKey = %s (%s)", s.FunctionKey, s.FunctionKeySource)
		}
		// rumble on again by the entry, led off by the entry, the pause
		// switch untouched, lowBattery off by the retired top-level member.
		if want := (FeedbackSwitches{Rumble: true, LED: false, Pause: true, LowBattery: false}); s.Feedback != want || s.FeedbackSource != SourceDevice {
			t.Errorf("Feedback = %+v (%s); want %+v (device)", s.Feedback, s.FeedbackSource, want)
		}
		if buttons(s.Panic) != "start+north" || s.Panic.HoldMs != 700 || s.PanicSource != SourceDevice {
			t.Errorf("Panic = %v %d (%s)", s.Panic.Buttons, s.Panic.HoldMs, s.PanicSource)
		}
	})
	t.Run("standard controller with no entry takes the retired members", func(t *testing.T) {
		s := cfg.PadSettings(keyPS, padfamily.Standard)
		if s.FunctionKey != control.Back || s.FunctionKeySource != SourceLegacy {
			t.Errorf("FunctionKey = %s (%s); want back (legacy)", s.FunctionKey, s.FunctionKeySource)
		}
		if want := (FeedbackSwitches{Rumble: false, LED: true, Pause: true, LowBattery: false}); s.Feedback != want || s.FeedbackSource != SourceLegacy {
			t.Errorf("Feedback = %+v (%s); want %+v (legacy)", s.Feedback, s.FeedbackSource, want)
		}
		if buttons(s.Panic) != "back+south" || s.Panic.HoldMs != 1500 || s.PanicSource != SourceLegacy {
			t.Errorf("Panic = %v %d (%s)", s.Panic.Buttons, s.Panic.HoldMs, s.PanicSource)
		}
	})
	t.Run("a joycon keeps its family function key, not the retired one", func(t *testing.T) {
		s := cfg.PadSettings(keyJoyConL, padfamily.JoyConLeft)
		if s.FunctionKey != control.LT || s.FunctionKeySource != SourceDefault {
			t.Errorf("FunctionKey = %s (%s); want lt (default)", s.FunctionKey, s.FunctionKeySource)
		}
		// But the feedback switches are for every controller, and the combo
		// of its family is not the standard one.
		if s.Feedback.Rumble || s.FeedbackSource != SourceLegacy {
			t.Errorf("Feedback = %+v (%s)", s.Feedback, s.FeedbackSource)
		}
		if buttons(s.Panic) != "paddle2+paddle4" || s.PanicSource != SourceDefault {
			t.Errorf("Panic = %v (%s); the joyconRight entry is not the left one's", s.Panic.Buttons, s.PanicSource)
		}
	})
	t.Run("right joycon", func(t *testing.T) {
		s := cfg.PadSettings(keyJoyConR, padfamily.JoyConRight)
		if s.FunctionKey != control.RightShoulder || s.FunctionKeySource != SourceDevice {
			t.Errorf("FunctionKey = %s (%s); want rightshoulder (device)", s.FunctionKey, s.FunctionKeySource)
		}
		if buttons(s.Panic) != "south+east" || s.Panic.HoldMs != 900 || s.PanicSource != SourceLegacy {
			t.Errorf("Panic = %v %d (%s); want the joyconRight entry", s.Panic.Buttons, s.Panic.HoldMs, s.PanicSource)
		}
	})
	t.Run("a pair is a controller of its own; standard combo entry", func(t *testing.T) {
		s := cfg.PadSettings(keyJoyConPair, padfamily.JoyConPair)
		if s.FunctionKey != control.Start || s.FunctionKeySource != SourceDevice {
			t.Errorf("FunctionKey = %s (%s)", s.FunctionKey, s.FunctionKeySource)
		}
		if buttons(s.Panic) != "back+south" || s.PanicSource != SourceLegacy {
			t.Errorf("Panic = %v (%s); the standard entry applied to a pair before", s.Panic.Buttons, s.PanicSource)
		}
	})
}

// TestPadSettingsLegacyFunctionKeyOfAPair: the retired top-level functionKey,
// which is start unless the file says back, never moves a pair off its
// default: Joy-Cons have their own defaults.
func TestPadSettingsLegacyFunctionKeyOfAPair(t *testing.T) {
	for _, doc := range []string{`{"schemaVersion":0}`, `{"schemaVersion":0,"functionKey":"start"}`, `{"schemaVersion":0,"functionKey":"back"}`, `{"schemaVersion":0,"functionKey":"guide"}`} {
		c := mustConfig(t, doc)
		if s := c.PadSettings(keyJoyConPair, padfamily.JoyConPair); s.FunctionKey != control.Back || s.FunctionKeySource != SourceDefault {
			t.Errorf("%s: pair function key %s (%s); want back (default)", doc, s.FunctionKey, s.FunctionKeySource)
		}
	}
	// The default is not the legacy source for a pad with the standard layout.
	c := mustConfig(t, `{"schemaVersion":0,"functionKey":"start"}`)
	if s := c.PadSettings(keyXbox, padfamily.Standard); s.FunctionKey != control.Start || s.FunctionKeySource != SourceDefault {
		t.Errorf("explicit start: %s (%s)", s.FunctionKey, s.FunctionKeySource)
	}
	// guide was read as start.
	c = mustConfig(t, `{"schemaVersion":0,"functionKey":"guide"}`)
	if s := c.PadSettings(keyXbox, padfamily.Standard); s.FunctionKey != control.Start {
		t.Errorf("guide: %s", s.FunctionKey)
	}
}

// TestPadSettingsStaleFunctionKey: a function key the controller's family does
// not allow (a value an older version wrote: a paddle, − or +, or the other
// Joy-Con's) is not used: the family's default applies.
func TestPadSettingsStaleFunctionKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string
		family padfamily.Family
		stored string
		want   control.Control
		source Source
	}{
		{"right: ZR", keyJoyConR, padfamily.JoyConRight, "rt", control.RT, SourceDevice},
		{"right: R", keyJoyConR, padfamily.JoyConRight, "rightshoulder", control.RightShoulder, SourceDevice},
		{"left: L", keyJoyConL, padfamily.JoyConLeft, "leftshoulder", control.LeftShoulder, SourceDevice},
		{"right: the old sideways ZR", keyJoyConR, padfamily.JoyConRight, "paddle3", control.RT, SourceDefault},
		{"right: the old sideways R", keyJoyConR, padfamily.JoyConRight, "paddle1", control.RT, SourceDefault},
		{"left: the old sideways ZL", keyJoyConL, padfamily.JoyConLeft, "paddle4", control.LT, SourceDefault},
		{"left: the old upright minus", keyJoyConL, padfamily.JoyConLeft, "back", control.LT, SourceDefault},
		{"right: the old upright plus", keyJoyConR, padfamily.JoyConRight, "start", control.RT, SourceDefault},
		{"left: the right one's", keyJoyConL, padfamily.JoyConLeft, "rt", control.LT, SourceDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ds := ParseConfig([]byte(fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"functionKey":%q}}}`, tc.key, tc.stored)))
			if c == nil {
				t.Fatalf("the file must still load: %v", ds)
			}
			if s := c.PadSettings(tc.key, tc.family); s.FunctionKey != tc.want || s.FunctionKeySource != tc.source {
				t.Errorf("%s as %s: %s (%s); want %s (%s)", tc.key, tc.family, s.FunctionKey, s.FunctionKeySource, tc.want, tc.source)
			}
		})
	}
}

// TestPadSettingsStalePanic: a combo that names a button the Joy-Con does not
// have, which is what an older version's combo for the sideways grip does, is
// not used, whether the controller's own or the family's retired entry; and the
// retired families joyconLeftVertical and joyconRightVertical are read by
// nobody.
func TestPadSettingsStalePanic(t *testing.T) {
	c := mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,
		"safety":{"panic":{
			"joyconLeft":{"buttons":["leftshoulder","rightshoulder"],"holdMs":900},
			"joyconRight":{"buttons":["south","east"],"holdMs":800},
			"joyconLeftVertical":{"buttons":["paddle2","back"],"holdMs":700},
			"joyconRightVertical":{"buttons":["south","start"],"holdMs":700}}},
		"devices":{%q:{"panic":{"buttons":["leftshoulder","rightshoulder"],"holdMs":1000}}}}`, keyJoyConL))
	for _, tc := range []struct {
		name   string
		key    string
		family padfamily.Family
		want   string
		hold   int
		source Source
	}{
		{"left, own sideways combo", keyJoyConL, padfamily.JoyConLeft, "paddle2+paddle4", 2000, SourceDefault},
		{"right, retired family entry that fits", keyJoyConR, padfamily.JoyConRight, "south+east", 800, SourceLegacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := c.PadSettings(tc.key, tc.family)
			if buttons(s.Panic) != tc.want || s.Panic.HoldMs != tc.hold || s.PanicSource != tc.source {
				t.Errorf("Panic = %v hold %d (%s); want %s, %d (%s)", s.Panic.Buttons, s.Panic.HoldMs, s.PanicSource, tc.want, tc.hold, tc.source)
			}
		})
	}
	// The sideways combo of the family's entry is no good either.
	c = mustConfig(t, `{"schemaVersion":0,"safety":{"panic":{"joyconLeft":{"buttons":["leftshoulder","rightshoulder"],"holdMs":900}}}}`)
	if s := c.PadSettings(keyJoyConL, padfamily.JoyConLeft); buttons(s.Panic) != "paddle2+paddle4" || s.PanicSource != SourceDefault {
		t.Errorf("Panic = %v (%s); want the built-in combo", s.Panic.Buttons, s.PanicSource)
	}
	// A pair is not a single Joy-Con: it takes the standard entry, whatever buttons.
	c = mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"panic":{"buttons":["leftshoulder","rightshoulder"],"holdMs":1000}}}}`, keyJoyConPair))
	if s := c.PadSettings(keyJoyConPair, padfamily.JoyConPair); buttons(s.Panic) != "leftshoulder+rightshoulder" || s.PanicSource != SourceDevice {
		t.Errorf("pair Panic = %v (%s)", s.Panic.Buttons, s.PanicSource)
	}
}

// TestPadSettingsUnusableCombo: a combo with guide is not used, whether the
// controller's own or the retired family entry; the family's built-in one
// applies.
func TestPadSettingsUnusableCombo(t *testing.T) {
	c := mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,
		"safety":{"panic":{"standard":{"buttons":["guide","start"],"holdMs":2000}}},
		"devices":{%q:{"panic":{"buttons":["guide","back"],"holdMs":2000}}}}`, keyPS))
	for _, key := range []string{keyPS, keyXbox} {
		s := c.PadSettings(key, padfamily.Standard)
		if buttons(s.Panic) != "back+start" || s.PanicSource != SourceDefault {
			t.Errorf("%s: Panic = %v (%s); want the built-in combo", key, s.Panic.Buttons, s.PanicSource)
		}
	}
}

// TestPadSettingsDoesNotShareStorage: the result is the caller's.
func TestPadSettingsDoesNotShareStorage(t *testing.T) {
	c := mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"panic":{"buttons":["start","north"],"holdMs":700}}}}`, keyXbox))
	s := c.PadSettings(keyXbox, padfamily.Standard)
	s.Panic.Buttons[0] = "south"
	if c.Devices[keyXbox].Panic.Buttons[0] != "start" {
		t.Error("PadSettings shares the config's buttons")
	}
	d := DefaultConfig().PadSettings(keyXbox, padfamily.Standard)
	d.Panic.Buttons[0] = "south"
	if DefaultConfig().PadSettings(keyXbox, padfamily.Standard).Panic.Buttons[0] != "back" {
		t.Error("the built-in combo is shared")
	}
}

// TestMaterializedKeepsPadSettings: folding the retired members into the
// controller's entry and dropping them leaves its settings as they were, for
// every family and many configs; only a SourceLegacy turns into SourceDevice.
func TestMaterializedKeepsPadSettings(t *testing.T) {
	functionKeys := []string{"", `"functionKey":"back",`, `"functionKey":"start",`, `"functionKey":"guide",`}
	feedbacks := []string{"", `"feedback":{"rumble":false},`, `"feedback":{"led":false,"pause":false,"lowBattery":false,"rumble":false},`, `"feedback":{"rumble":true},`}
	safeties := []string{"", `"safety":{"panic":{"standard":{"buttons":["back","south"],"holdMs":1500}}},`,
		`"safety":{"panic":{"joyconLeft":{"buttons":["south","east"],"holdMs":900},"joyconRightVertical":{"buttons":["guide","east"],"holdMs":900},"standard":{"buttons":["guide","back"],"holdMs":900}}},`}
	devices := []string{"",
		fmt.Sprintf(`"devices":{%q:{"functionKey":"start","feedback":{"rumble":true},"panic":{"buttons":["south","east"],"holdMs":800}},%q:{"functionKey":"rightshoulder"}},`, keyXbox, keyJoyConR)}
	families := map[string]padfamily.Family{
		keyXbox: padfamily.Standard, keyPS: padfamily.Standard, keyJoyConPair: padfamily.JoyConPair,
		keyJoyConL: padfamily.JoyConLeft, keyJoyConR: padfamily.JoyConRight,
	}
	for _, fk := range functionKeys {
		for _, fb := range feedbacks {
			for _, sf := range safeties {
				for _, dv := range devices {
					doc := `{"schemaVersion":0,` + fk + fb + sf + dv
					doc = strings.TrimSuffix(doc, ",") + `}`
					c := mustConfig(t, doc)
					for key, family := range families {
						for _, f := range []padfamily.Family{family, padfamily.Unknown} {
							want := c.PadSettings(key, f)
							next := DefaultConfig()
							next.Devices = map[string]DeviceSettings{key: c.Materialized(key, f)}
							got := next.PadSettings(key, f)
							if got.FunctionKey != want.FunctionKey || got.Feedback != want.Feedback || got.Panic.HoldMs != want.Panic.HoldMs || !slices.Equal(got.Panic.Buttons, want.Panic.Buttons) {
								t.Fatalf("%s\n%s as %s: before %+v, after %+v", doc, key, f, want, got)
							}
							if got.FunctionKeySource == SourceLegacy || got.FeedbackSource == SourceLegacy || got.PanicSource == SourceLegacy {
								t.Fatalf("%s: a legacy source is left after folding: %+v", doc, got)
							}
						}
					}
				}
			}
		}
	}
}

// TestPadFeedback: PadFeedback is the feedback half of PadSettings for the
// slots, which ask at every feedback state: the same switches and the same source
// for any config and any key, and nothing allocated.
func TestPadFeedback(t *testing.T) {
	cfg := mustConfig(t, `{"schemaVersion":0,"feedback":{"led":false},"devices":{"`+keyXbox+`":{"feedback":{"rumble":false,"led":true}},"`+keyPS+`":{"feedback":{"lowBattery":false}}}}`)
	for _, key := range []string{keyXbox, keyPS, keyJoyConR, ""} {
		sw, source := cfg.PadFeedback(key)
		want := cfg.PadSettings(key, padfamily.Unknown)
		if sw != want.Feedback || source != want.FeedbackSource {
			t.Errorf("PadFeedback(%q) = %+v, %v; PadSettings has %+v, %v", key, sw, source, want.Feedback, want.FeedbackSource)
		}
	}
	// One controller's switches never reach another's.
	if sw, _ := cfg.PadFeedback(keyXbox); sw.Rumble || !sw.LED {
		t.Errorf("xbox = %+v; want rumble off and led on (its own beats the retired one)", sw)
	}
	if sw, source := cfg.PadFeedback(keyJoyConR); !sw.Rumble || sw.LED || source != SourceLegacy {
		t.Errorf("a controller with no entry = %+v, %v; want the retired top-level feedback", sw, source)
	}
	if sw, source := (*Config)(nil).PadFeedback(keyXbox); sw != DefaultFeedbackSwitches() || source != SourceDefault {
		t.Errorf("nil config = %+v, %v", sw, source)
	}
	if n := testing.AllocsPerRun(100, func() { cfg.PadFeedback(keyXbox) }); n != 0 {
		t.Errorf("PadFeedback allocates %v times", n)
	}
}
