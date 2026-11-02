package profile

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
)

// TestConfigPadMembers: devices.<DeviceKey> may hold the controller's own
// function key, feedback switches and emergency combo (ADR-0017).
// devPtr is the JSON Pointer of a member inside the entry of devices for key.
func devPtr(key, rest string) string { return childPointer("/devices", key) + rest }

func TestConfigPadMembers(t *testing.T) {
	c := mustConfig(t, fmt.Sprintf(`{"schemaVersion":0,"devices":{
		%q:{"functionKey":"back","feedback":{"rumble":false,"led":true},"panic":{"buttons":["start","north"],"holdMs":700},"ignored":true},
		%q:{"functionKey":"rightshoulder"}}}`, keyXbox, keyJoyConR))
	x := c.Devices[keyXbox]
	if x.FunctionKey != "back" || !x.Ignored || x.Panic == nil || x.Panic.HoldMs != 700 || x.Feedback == nil ||
		x.Feedback.Rumble == nil || *x.Feedback.Rumble || x.Feedback.Led == nil || !*x.Feedback.Led || x.Feedback.Pause != nil {
		t.Errorf("xbox entry: %+v", x)
	}
	if c.Devices[keyJoyConR].FunctionKey != "rightshoulder" {
		t.Errorf("joycon entry: %+v", c.Devices[keyJoyConR])
	}
	if c.Legacy.Any() {
		t.Errorf("Legacy = %+v; the file has none of the retired members", c.Legacy)
	}
}

func TestConfigLegacyPadMembers(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want LegacyPad
	}{
		{`{"schemaVersion":0}`, LegacyPad{}},
		{`{"schemaVersion":0,"functionKey":"start"}`, LegacyPad{FunctionKey: true}},
		{`{"schemaVersion":0,"functionKey":"guide"}`, LegacyPad{FunctionKey: true}},
		{`{"schemaVersion":0,"feedback":{}}`, LegacyPad{Feedback: true}},
		{`{"schemaVersion":0,"safety":{}}`, LegacyPad{}},
		{`{"schemaVersion":0,"safety":{"panic":{}}}`, LegacyPad{}},
		{`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":2000}}}}`, LegacyPad{Panic: true}},
		{`{"schemaVersion":0,"functionKey":"back","feedback":{"led":false},"safety":{"panic":{"joyconLeft":{"buttons":["south","east"],"holdMs":500}}}}`, LegacyPad{true, true, true}},
	} {
		if got := mustConfig(t, tc.doc).Legacy; got != tc.want {
			t.Errorf("%s: Legacy = %+v; want %+v", tc.doc, got, tc.want)
		}
	}
}

func TestConfigPadValidation(t *testing.T) {
	dev := func(key, member string) string {
		return fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{%s}}}`, key, member)
	}
	for _, tc := range []struct {
		name    string
		doc     string
		pointer string // of the first error; empty when the config is accepted
	}{
		{"standard: start", dev(keyXbox, `"functionKey":"start"`), ""},
		{"standard: back", dev(keyPS, `"functionKey":"back"`), ""},
		{"standard: a paddle", dev(keyXbox, `"functionKey":"paddle1"`), devPtr(keyXbox, "/functionKey")},
		{"pair: back", dev(keyJoyConPair, `"functionKey":"back"`), ""},
		{"pair: start", dev(keyJoyConPair, `"functionKey":"start"`), ""},
		{"pair: a paddle", dev(keyJoyConPair, `"functionKey":"paddle3"`), devPtr(keyJoyConPair, "/functionKey")},
		{"pair: a trigger", dev(keyJoyConPair, `"functionKey":"lt"`), devPtr(keyJoyConPair, "/functionKey")},
		{"standard: a trigger", dev(keyXbox, `"functionKey":"lt"`), devPtr(keyXbox, "/functionKey")},
		{"left: ZL", dev(keyJoyConL, `"functionKey":"lt"`), ""},
		{"left: L", dev(keyJoyConL, `"functionKey":"leftshoulder"`), ""},
		{"right: ZR", dev(keyJoyConR, `"functionKey":"rt"`), ""},
		{"right: R", dev(keyJoyConR, `"functionKey":"rightshoulder"`), ""},
		// A key that tells nothing of the family accepts every function key
		// that some family has, and a retired paddle with a warning.
		{"no guid: a trigger", dev("k", `"functionKey":"rt"`), ""},
		{"no guid: a shoulder", dev("k", `"functionKey":"leftshoulder"`), ""},
		{"no guid: back", dev("k", `"functionKey":"back"`), ""},
		{"no guid: a retired paddle", dev("k", `"functionKey":"paddle2"`), ""},
		{"guide is retired", dev(keyXbox, `"functionKey":"guide"`), devPtr(keyXbox, "/functionKey")},
		{"a button that is no function key", dev(keyXbox, `"functionKey":"south"`), devPtr(keyXbox, "/functionKey")},
		{"an alias", dev(keyXbox, `"functionKey":"select"`), devPtr(keyXbox, "/functionKey")},
		{"not a string", dev(keyXbox, `"functionKey":1`), devPtr(keyXbox, "/functionKey")},
		{"null", dev(keyXbox, `"functionKey":null`), devPtr(keyXbox, "/functionKey")},
		{"feedback ok", dev(keyXbox, `"feedback":{"rumble":false,"led":false,"pause":false,"lowBattery":false}`), ""},
		{"feedback unknown switch", dev(keyXbox, `"feedback":{"sound":false}`), devPtr(keyXbox, "/feedback")},
		{"feedback not a bool", dev(keyXbox, `"feedback":{"rumble":"no"}`), devPtr(keyXbox, "/feedback/rumble")},
		{"feedback null", dev(keyXbox, `"feedback":null`), devPtr(keyXbox, "/feedback")},
		{"panic ok", dev(keyXbox, `"panic":{"buttons":["back","start"],"holdMs":500}`), ""},
		{"panic with a paddle for a joycon", dev(keyJoyConL, `"panic":{"buttons":["paddle2","paddle4"],"holdMs":2000}`), ""},
		{"panic with one button", dev(keyXbox, `"panic":{"buttons":["back"],"holdMs":2000}`), devPtr(keyXbox, "/panic/buttons")},
		{"panic held too short", dev(keyXbox, `"panic":{"buttons":["back","start"],"holdMs":499}`), devPtr(keyXbox, "/panic/holdMs")},
		{"panic with a virtual button", dev(keyXbox, `"panic":{"buttons":["leftx+","start"],"holdMs":2000}`), devPtr(keyXbox, "/panic/buttons/0")},
		{"panic with an alias", dev(keyXbox, `"panic":{"buttons":["select","start"],"holdMs":2000}`), devPtr(keyXbox, "/panic/buttons/0")},
		{"panic without holdMs", dev(keyXbox, `"panic":{"buttons":["back","start"]}`), devPtr(keyXbox, "/panic")},
		{"panic twice the same button", dev(keyXbox, `"panic":{"buttons":["back","back"],"holdMs":2000}`), devPtr(keyXbox, "/panic/buttons")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ds := ParseConfig([]byte(tc.doc))
			if tc.pointer == "" {
				if c == nil || ds.HasErrors() {
					t.Fatalf("rejected: %v", ds)
				}
				return
			}
			if c != nil || !ds.HasErrors() {
				t.Fatalf("accepted: %+v", c)
			}
			if ds[0].Pointer != tc.pointer || ds[0].Line == 0 {
				t.Fatalf("diagnostic %+v; want pointer %s, located", ds[0], tc.pointer)
			}
		})
	}
}

// TestConfigOldJoyConMembers: a config.json written when a single Joy-Con was
// held sideways, or either way, still loads whole (no error, so the rest of the
// file applies), with a warning on each value that no longer fits: a function
// key a single Joy-Con no longer has (a paddle, back or start), the retired
// panic families, and a combo with a button the Joy-Con does not have. The
// controller then has the default of its family, and no key is lost.
func TestConfigOldJoyConMembers(t *testing.T) {
	doc := fmt.Sprintf(`{"schemaVersion":0,"enabled":false,
		"safety":{"panic":{
			"standard":{"buttons":["back","south"],"holdMs":1500},
			"joyconLeft":{"buttons":["leftshoulder","rightshoulder"],"holdMs":2000},
			"joyconLeftVertical":{"buttons":["paddle2","paddle4"],"holdMs":2000},
			"joyconRightVertical":{"buttons":["paddle3","paddle1"],"holdMs":2000}}},
		"devices":{
			%q:{"functionKey":"paddle4","panic":{"buttons":["leftshoulder","rightshoulder"],"holdMs":2000},"feedback":{"rumble":false}},
			%q:{"functionKey":"start"},
			%q:{"functionKey":"paddle1"},
			%q:{"functionKey":"back"}}}`, keyJoyConL, keyJoyConR, "k", keyXbox)
	c, ds := ParseConfig([]byte(doc))
	if c == nil || ds.HasErrors() {
		t.Fatalf("an older file must load: %v", ds)
	}
	if c.Enabled {
		t.Error("the rest of the file does not apply")
	}
	warned := map[string]bool{}
	for _, d := range ds {
		if d.Severity != SeverityWarning {
			t.Errorf("diagnostic %v is not a warning", d)
		}
		warned[d.Pointer] = true
	}
	for _, ptr := range []string{
		devPtr(keyJoyConL, "/functionKey"), devPtr(keyJoyConL, "/panic/buttons"), devPtr(keyJoyConR, "/functionKey"), devPtr("k", "/functionKey"),
		"/safety/panic/joyconLeft/buttons", "/safety/panic/joyconLeftVertical", "/safety/panic/joyconRightVertical",
	} {
		if !warned[ptr] {
			t.Errorf("no warning on %s; warned: %v", ptr, ds)
		}
	}
	if warned[devPtr(keyXbox, "/functionKey")] || warned["/safety/panic/standard/buttons"] {
		t.Errorf("a warning on a value that is still good: %v", ds)
	}
	left := c.PadSettings(keyJoyConL, padfamily.JoyConLeft)
	if left.FunctionKey != control.LT || left.FunctionKeySource != SourceDefault || buttons(left.Panic) != "paddle2+paddle4" || left.PanicSource != SourceDefault {
		t.Errorf("left: %+v; want the defaults", left)
	}
	if left.Feedback.Rumble {
		t.Error("left: the rest of its entry (feedback) was lost")
	}
	right := c.PadSettings(keyJoyConR, padfamily.JoyConRight)
	if right.FunctionKey != control.RT || right.FunctionKeySource != SourceDefault || buttons(right.Panic) != "paddle3+paddle1" || right.PanicSource != SourceDefault {
		t.Errorf("right: %+v; want the defaults", right)
	}
	if x := c.PadSettings(keyXbox, padfamily.Standard); x.FunctionKey != control.Back || x.FunctionKeySource != SourceDevice {
		t.Errorf("xbox: %+v; its own good entry applies", x)
	}
}

// TestConfigPadPanicWithGuide: a combo with guide loads with a warning, as
// the retired family combos do, and is not used.
func TestConfigPadPanicWithGuide(t *testing.T) {
	c, ds := ParseConfig([]byte(fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"panic":{"buttons":["guide","start"],"holdMs":2000}}}}`, keyXbox)))
	if c == nil || ds.HasErrors() {
		t.Fatal(ds)
	}
	if len(ds) != 1 || ds[0].Severity != SeverityWarning || ds[0].Pointer != devPtr(keyXbox, "/panic/buttons/0") {
		t.Fatalf("diagnostics %v; want one warning on guide", ds)
	}
	if c.Devices[keyXbox].Panic.Usable() {
		t.Error("a combo with guide is usable")
	}
}

// TestRepairConfigPadMembers: the repair of a bad per-controller member takes
// out that member only.
func TestRepairConfigPadMembers(t *testing.T) {
	doc := fmt.Sprintf(`{"schemaVersion":0,"devices":{%q:{"functionKey":"paddle1","feedback":{"led":false},"panic":{"buttons":["back"],"holdMs":2000},"ignored":true}}}`, keyXbox)
	fixed, repair := RepairConfig([]byte(doc), func(string) bool { return true })
	c, ds := ParseConfig(fixed)
	if c == nil || ds.HasErrors() {
		t.Fatalf("repaired file does not parse: %s %v", fixed, ds)
	}
	d := c.Devices[keyXbox]
	if d.FunctionKey != "" || d.Panic != nil || d.Feedback == nil || !d.Ignored {
		t.Errorf("entry after repair: %+v; the function key and the combo go, the rest stays", d)
	}
	want := []string{devPtr(keyXbox, "/functionKey"), devPtr(keyXbox, "/panic")}
	if len(repair.Members) != 2 || repair.Members[0] != want[0] || repair.Members[1] != want[1] {
		t.Errorf("repaired members %v; want %v", repair.Members, want)
	}
}

func TestConfigPadSchemaCurrent(t *testing.T) {
	// The retired members stay in the schema, marked.
	schema := string(ConfigSchema())
	for _, member := range []string{`"functionKey"`, `"feedback"`, `"safety"`} {
		i := strings.Index(schema, member)
		if i < 0 || !strings.Contains(schema[i:i+600], `"deprecated": true`) {
			t.Errorf("top-level %s is not marked deprecated", member)
		}
	}
}

var updateGolden = flag.Bool("update-pads", false, "rewrite testdata/pads/*/out.json from the current output")

// TestMigratePadSettingsGolden: testdata/pads/<case>/in.json is a config.json,
// pads.txt lists the known controllers (a DeviceKey and a family name per
// line), and out.json is what MigratePadSettings returns, or the line
// "unchanged" when the file has nothing to fold. Rerun with -update-pads to
// rewrite them.
func TestMigratePadSettingsGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/pads/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			in, err := os.ReadFile(filepath.Join(dir, "in.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"{{xbox}}", "{{ps}}", "{{joyconL}}", "{{joyconR}}", "{{pair}}"} {
				in = bytes.ReplaceAll(in, []byte(name), []byte(expandKey(name)))
			}
			var pads []PadRef
			if list, err := os.ReadFile(filepath.Join(dir, "pads.txt")); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(list)), "\n") {
					key, family, _ := strings.Cut(strings.TrimSpace(line), " ")
					f, ok := padfamily.Parse(family)
					if key == "" || !ok {
						t.Fatalf("bad pads.txt line %q", line)
					}
					pads = append(pads, PadRef{Key: expandKey(key), Family: f})
				}
			}
			out, changed, err := MigratePadSettings(in, pads)
			got := "unchanged\n"
			if err != nil {
				got = "error: " + err.Error() + "\n"
			} else if changed {
				got = string(out)
				c, ds := ParseConfig(out)
				if c == nil || ds.HasErrors() {
					t.Fatalf("the result does not parse: %v\n%s", ds, out)
				}
				if c.Legacy.Any() {
					t.Errorf("the result still has the retired members: %+v", c.Legacy)
				}
				// Migrating again changes nothing.
				if _, again, err := MigratePadSettings(out, pads); again || err != nil {
					t.Errorf("a second migration changed the file: %v", err)
				}
			}
			golden := filepath.Join(dir, "out.json")
			if *updateGolden {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v; run go test ./internal/profile -update-pads", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s (rerun with -update-pads and review the diff):\n--- got\n%s--- want\n%s", golden, got, want)
			}
		})
	}
}

// expandKey replaces the names the golden cases use for the DeviceKeys of
// the tests.
func expandKey(name string) string {
	switch name {
	case "{{xbox}}":
		return keyXbox
	case "{{ps}}":
		return keyPS
	case "{{joyconL}}":
		return keyJoyConL
	case "{{joyconR}}":
		return keyJoyConR
	case "{{pair}}":
		return keyJoyConPair
	}
	return name
}
