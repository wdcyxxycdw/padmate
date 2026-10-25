package profile_test

import (
	"fmt"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// A mouse button is held like a key but must never be pressed again while it
// is down, so a binding that holds one with typematic repeat is an error that
// suggests mode "tap" (repeated clicks), and a tap repeat is fine. A wheel key
// repeats like any key: one notch per repeat.
func TestMouseButtonRepeat(t *testing.T) {
	const message = `a mouse button cannot use typematic repeat, which presses it again while it is down: use mode "tap" to repeat clicks`
	for _, tc := range []struct {
		name    string
		members string
		keys    string
		gesture string
		err     string // "" when the profile is valid
		at      string // the JSON pointer of the error
	}{
		// Holding with a repeat is the typematic case, written or by default.
		{"hold with repeat", `"mode":"hold","repeat":{"delayMs":100}`, "MouseLeft", "press", message, "/layers/base/south/press/repeat"},
		{"default mode with repeat", `"repeat":{"rateHz":5}`, "LButton", "press", message, "/layers/base/south/press/repeat"},
		{"explicit style", `"mode":"hold","repeat":{"style":"typematic"}`, "RButton", "press", message, "/layers/base/south/press/repeat/style"},
		{"default style", `"repeat":{}`, "MouseMiddle", "hold", message, "/layers/base/south/hold/repeat"},
		{"chord", `"repeat":{"delayMs":0}`, "Ctrl+Shift+MouseX1", "press", message, "/layers/base/south/press/repeat"},
		{"side button 2", `"repeat":{"rateHz":30}`, "XButton2", "press", message, "/layers/base/south/press/repeat"},
		// Tap repeat clicks again and again.
		{"tap mode", `"mode":"tap","repeat":{"delayMs":100,"rateHz":8}`, "MouseLeft", "press", "", ""},
		{"tap style", `"repeat":{"style":"tap"}`, "RButton", "press", "", ""},
		{"tap style in a chord", `"mode":"tap","repeat":{"style":"tap","rateHz":4}`, "Shift+MouseMiddle", "press", "", ""},
		// No repeat, no problem: a hold, a tap, a drag.
		{"hold alone", `"mode":"hold"`, "MouseLeft", "press", "", ""},
		{"tap alone", `"mode":"tap"`, "MouseX1", "press", "", ""},
		// The existing rules come first, as the keyboard keys get them.
		{"typematic with tap mode", `"mode":"tap","repeat":{"style":"typematic"}`, "MouseLeft", "press", `typematic repeat needs mode "hold"`, "/layers/base/south/press/repeat/style"},
		{"tap style with hold mode", `"mode":"hold","repeat":{"style":"tap"}`, "MouseLeft", "press", `tap repeat needs mode "tap"`, "/layers/base/south/press/repeat/style"},
		// The wheel keys and keyboard keys repeat as ever.
		{"wheel hold with repeat", `"mode":"hold","repeat":{"delayMs":100}`, "WheelUp", "press", "", ""},
		{"wheel chord", `"repeat":{"rateHz":5}`, "Ctrl+WheelDown", "press", "", ""},
		{"key hold with repeat", `"mode":"hold","repeat":{"delayMs":100}`, "Z", "press", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`{"schemaVersion":0,"name":"buttons","layers":{"base":{"south":{%q:{"type":"keys","keys":%q,%s}}}}}`, tc.gesture, tc.keys, tc.members))
			p, diags := profile.Parse(data)
			if tc.err == "" {
				if p == nil || diags.HasErrors() {
					t.Fatalf("a valid binding was rejected: %+v", diags)
				}
				return
			}
			if p != nil || !diags.HasErrors() {
				t.Fatalf("the binding parsed: %+v", diags)
			}
			var errs []profile.Diagnostic
			for _, d := range diags {
				if d.Severity == profile.SeverityError {
					errs = append(errs, d)
				}
			}
			if len(errs) != 1 || errs[0].Message != tc.err || errs[0].Pointer != tc.at {
				t.Errorf("errors %+v, want one %q at %s", errs, tc.err, tc.at)
			}
		})
	}
}

func TestRepeatStyleModeAndExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		members string
		mode    profile.Mode
		style   profile.RepeatStyle
		valid   bool
	}{
		{`"repeat":{"style":"tap","delayMs":0}`, profile.ModeTap, profile.RepeatTap, true},
		{`"mode":"tap","repeat":{"delayMs":0}`, profile.ModeTap, profile.RepeatTap, true},
		{`"repeat":{"delayMs":0}`, profile.ModeHold, profile.RepeatTypematic, true},
		{`"mode":"hold","repeat":{"style":"tap"}`, profile.ModeHold, profile.RepeatTap, false},
		{`"mode":"tap","repeat":{"style":"typematic"}`, profile.ModeTap, profile.RepeatTypematic, false},
	} {
		t.Run(tc.members, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`{"schemaVersion":0,"name":"repeat","layers":{"base":{"south":{"press":{"type":"keys","keys":"A",%s}}}}}`, tc.members))
			p, diags := profile.Parse(data)
			if !tc.valid {
				if p != nil {
					t.Fatalf("invalid repeat parsed: %+v", diags)
				}
				return
			}
			if p == nil {
				t.Fatalf("parse failed: %+v", diags)
			}
			a := p.Layers[0].Bindings[0].Press
			if a.Mode != tc.mode || a.Repeat.Style != tc.style || !a.Repeat.DelaySet {
				t.Fatalf("action=%+v repeat=%+v", a, a.Repeat)
			}
		})
	}
}
