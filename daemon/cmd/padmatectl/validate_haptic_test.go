package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfile writes src as profile.json in a temporary folder and returns
// its path.
func writeProfile(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestValidateHaptic (MAP-25, HUD-10): validate lists the haptic of a binding
// after its gesture, as text and as the "haptic" member of --json, accepts
// knobTick, and reports a preset that is not one, or a knobTick that is not a
// boolean, at its member.
func TestValidateHaptic(t *testing.T) {
	const good = `{"schemaVersion":1,"name":"h","analog":{"rs":{"mode":"knob","knobTick":true}},"layers":{"base":{
		"south":{"label":"撤销","haptic":"tick","press":{"type":"keys","keys":"Ctrl+Z","mode":"tap"}},
		"east":{"press":{"type":"keys","keys":"Space"}},
		"north":{"block":true,"haptic":"error"},
		"rs.cw":{"haptic":"pulse2","press":{"type":"keys","keys":"]","mode":"tap"}}}}}`
	path := writeProfile(t, good)

	code, stdout, stderr := runCmd(t, "validate", path)
	if code != 0 || stderr != "" {
		t.Fatalf("validate = %d, stderr %q; want 0", code, stderr)
	}
	for _, want := range []string{
		`/layers/base/south "撤销": press +0 ms, haptic tick` + "\n",
		"/layers/base/east: press +0 ms\n",
		"/layers/base/north: block\n",
		"/layers/base/rs.cw: press +0 ms, haptic pulse2\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = runCmd(t, "validate", "--json", path)
	if code != 0 || stderr != "" {
		t.Fatalf("validate --json = %d, stderr %q; want 0", code, stderr)
	}
	var obj struct {
		OK       bool `json:"ok"`
		Bindings []struct {
			ID     string `json:"id"`
			Haptic string `json:"haptic"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil || !obj.OK {
		t.Fatalf("validate --json: %v, %q", err, stdout)
	}
	got := map[string]string{}
	for _, b := range obj.Bindings {
		got[b.ID] = b.Haptic
	}
	for id, want := range map[string]string{
		"/layers/base/south": "tick", "/layers/base/east": "", "/layers/base/north": "", "/layers/base/rs.cw": "pulse2",
	} {
		if g, ok := got[id]; !ok || g != want {
			t.Errorf("haptic of %s = %q (listed %v); want %q", id, g, ok, want)
		}
	}
	if strings.Contains(stdout, `"haptic":""`) {
		t.Errorf("a binding without a haptic lists an empty one: %s", stdout)
	}

	for _, tc := range []struct{ name, src, want string }{
		{"unknown preset", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":"buzz","press":{"type":"keys","keys":"A"}}}}}`,
			`error: "buzz" is not a valid value; use "tick", "confirm", "error", "pulse1", "pulse2", "pulse3" or "pulse4" (/layers/base/south/haptic)`},
		{"knobTick type", `{"schemaVersion":1,"name":"h","analog":{"ls":{"mode":"knob","knobTick":"yes"}},"layers":{"base":{}}}`,
			"error: expected a boolean, got a string (/analog/ls/knobTick)"},
	} {
		path := writeProfile(t, tc.src)
		code, stdout, _ := runCmd(t, "validate", path)
		if code != 1 || !strings.Contains(stdout, tc.want) {
			t.Errorf("%s: validate = %d, %q; want 1 and %q", tc.name, code, stdout, tc.want)
		}
	}
}
