package profile

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSetPadSettings(t *testing.T) {
	f := false
	on := true
	combo := &PanicCombo{Buttons: []string{"start", "north"}, HoldMs: 700}
	for _, tc := range []struct {
		name   string
		in     string
		key    string
		change PadChange
		want   string // compact JSON of the result
	}{
		{"function key of a pad that has no entry", `{"schemaVersion":0}`, "K1",
			PadChange{SetFunctionKey: true, FunctionKey: "back"},
			`{"schemaVersion":0,"devices":{"K1":{"functionKey":"back"}}}`},
		{"into an entry, after its members", `{"schemaVersion":0,"devices":{"K1":{"ignored":true},"K2":{"mapping":"x"}}}`, "K1",
			PadChange{SetFunctionKey: true, FunctionKey: "paddle3"},
			`{"schemaVersion":0,"devices":{"K1":{"ignored":true,"functionKey":"paddle3"},"K2":{"mapping":"x"}}}`},
		{"replaces in place", `{"schemaVersion":0,"devices":{"K1":{"functionKey":"back","ignored":true}}}`, "K1",
			PadChange{SetFunctionKey: true, FunctionKey: "start"},
			`{"schemaVersion":0,"devices":{"K1":{"functionKey":"start","ignored":true}}}`},
		{"takes out the function key, and the entry that is left empty", `{"schemaVersion":0,"devices":{"K1":{"functionKey":"back"}}}`, "K1",
			PadChange{SetFunctionKey: true},
			`{"schemaVersion":0}`},
		{"takes out the function key, the entry stays", `{"schemaVersion":0,"devices":{"K1":{"functionKey":"back","ignored":true},"K2":{}}}`, "K1",
			PadChange{SetFunctionKey: true},
			`{"schemaVersion":0,"devices":{"K1":{"ignored":true},"K2":{}}}`},
		{"feedback", `{"schemaVersion":0}`, "K1",
			PadChange{SetFeedback: true, Feedback: &FeedbackOverrides{Rumble: &f, Led: &on, LowBattery: &f}},
			`{"schemaVersion":0,"devices":{"K1":{"feedback":{"rumble":false,"led":true,"lowBattery":false}}}}`},
		{"feedback with no switch is taken out", `{"schemaVersion":0,"devices":{"K1":{"feedback":{"rumble":false},"ignored":true}}}`, "K1",
			PadChange{SetFeedback: true, Feedback: &FeedbackOverrides{}},
			`{"schemaVersion":0,"devices":{"K1":{"ignored":true}}}`},
		{"panic", `{"schemaVersion":0,"devices":{"K1":{}}}`, "K1",
			PadChange{SetPanic: true, Panic: combo},
			`{"schemaVersion":0,"devices":{"K1":{"panic":{"buttons":["start","north"],"holdMs":700}}}}`},
		{"all three at once, the retired members untouched", `{"schemaVersion":0,"functionKey":"back","feedback":{"led":false},"safety":{"panic":{}}}`, "K1",
			PadChange{SetFunctionKey: true, FunctionKey: "start", SetFeedback: true, Feedback: &FeedbackOverrides{Pause: &f}, SetPanic: true, Panic: combo},
			`{"schemaVersion":0,"functionKey":"back","feedback":{"led":false},"safety":{"panic":{}},"devices":{"K1":{"functionKey":"start","feedback":{"pause":false},"panic":{"buttons":["start","north"],"holdMs":700}}}}`},
		{"a change that changes nothing keeps the file's shape", "{\n  \"schemaVersion\": 0,\n  \"rules\": [{\"exe\": \"a.exe\", \"profile\": \"a\"}]\n}", "K1",
			PadChange{},
			`{"schemaVersion":0,"rules":[{"exe":"a.exe","profile":"a"}]}`},
		{"a key with a slash and a quote", `{"schemaVersion":0}`, `a/s:"b"`,
			PadChange{SetFunctionKey: true, FunctionKey: "back"},
			`{"schemaVersion":0,"devices":{"a/s:\"b\"":{"functionKey":"back"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SetPadSettings([]byte(tc.in), tc.key, tc.change)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(out), "}\n") || strings.HasPrefix(string(out), "\uFEFF") {
				t.Errorf("not indented as the UI writes the file: %q", out)
			}
			if got := compact(t, out); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func compact(t *testing.T, data []byte) string {
	t.Helper()
	var b strings.Builder
	in := false
	esc := false
	for _, c := range string(data) {
		switch {
		case in:
			b.WriteRune(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				in = false
			}
		case c == '"':
			in = true
			b.WriteRune(c)
		case c == ' ' || c == '\n' || c == '\t' || c == '\r':
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func TestSetPadSettingsRefuses(t *testing.T) {
	change := PadChange{SetFunctionKey: true, FunctionKey: "back"}
	for _, in := range []string{``, `null`, `[]`, `{"a":}`, `{"schemaVersion":0,"devices":[]}`, `{"schemaVersion":0,"devices":{"K1":5}}`, `{"schemaVersion":0,"a":1,"a":2}`} {
		if out, err := SetPadSettings([]byte(in), "K1", change); err == nil {
			t.Errorf("%q: accepted: %s", in, out)
		}
	}
}

// TestSetPadSettingsResolves: what a change writes is what PadSettings reads
// back, and the file stays valid.
func TestSetPadSettingsResolves(t *testing.T) {
	f := false
	in := []byte(fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"ignored":true}}}`, keyJoyConR))
	out, err := SetPadSettings(in, keyJoyConR, PadChange{SetFunctionKey: true, FunctionKey: "rightshoulder", SetFeedback: true, Feedback: &FeedbackOverrides{Rumble: &f},
		SetPanic: true, Panic: &PanicCombo{Buttons: []string{"south", "east"}, HoldMs: 900}})
	if err != nil {
		t.Fatal(err)
	}
	c := mustConfig(t, string(out))
	s := c.PadSettings(keyJoyConR, 0)
	if s.FunctionKey.String() != "rightshoulder" || s.Feedback.Rumble || !s.Feedback.LED || buttons(s.Panic) != "south+east" || s.Panic.HoldMs != 900 {
		t.Errorf("read back %+v", s)
	}
	if !c.Devices[keyJoyConR].Ignored {
		t.Error("ignored was lost")
	}
}

func FuzzSetPadSettings(f *testing.F) {
	for _, seed := range []string{
		`{"schemaVersion":0}`, `{"schemaVersion":0,"devices":{"K":{"functionKey":"back"}}}`, `{"schemaVersion":0,"devices":{"K":5}}`,
		`{"schemaVersion":0,"functionKey":"back","feedback":{"rumble":false},"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":2000}}}}`,
		"\uFEFF{\"schemaVersion\":0}", `{`, ``,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		change := PadChange{SetFunctionKey: true, FunctionKey: "back", SetFeedback: true, Feedback: &FeedbackOverrides{Rumble: new(bool)}, SetPanic: true, Panic: &PanicCombo{Buttons: []string{"back", "start"}, HoldMs: 500}}
		before, bds := ParseConfig(data)
		out, err := SetPadSettings(data, "030000005e040000130b000000000000", change)
		if err != nil {
			return
		}
		after, ads := ParseConfig(out)
		if bds.HasErrors() {
			return // not ours to repair; no panic is all that is asked
		}
		if ads.HasErrors() {
			// Only a device entry that is not an object may stop it.
			if !strings.Contains(string(data), `"devices"`) {
				t.Fatalf("valid %q became invalid: %v\n%s", data, ads, out)
			}
			return
		}
		// Everything but the entry that changed stays.
		before.Devices, after.Devices = nil, nil
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%q: the rest changed:\n%+v\n%+v", data, before, after)
		}
	})
}
