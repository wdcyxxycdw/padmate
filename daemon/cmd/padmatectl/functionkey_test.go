package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validate and trace read no config.json, so the function key (ADR-0075) is a
// flag, start by default, like config.json's functionKey: which bindings are
// ignored, and so listed, depends on it.
func TestValidateFunctionKeyFlag(t *testing.T) {
	// testdata/function-set.json binds back and start in the base layer and
	// in the function set, besides south and others.
	for _, tc := range []struct {
		args          []string
		listed, unlit []string
	}{
		{nil, []string{"/layers/base/back", "/functionSet/south", "/functionSet/back"}, []string{"/layers/base/start", "/functionSet/start"}},
		{[]string{"--function-key", "start"}, []string{"/layers/base/back", "/functionSet/south"}, []string{"/layers/base/start", "/functionSet/start"}},
		{[]string{"--function-key=back"}, []string{"/layers/base/start", "/functionSet/start", "/functionSet/south"}, []string{"/layers/base/back", "/functionSet/back"}},
		{[]string{"--function-key", "guide"}, []string{"/layers/base/back", "/layers/base/start", "/functionSet/back", "/functionSet/start"}, nil},
	} {
		args := append(append([]string{"validate"}, tc.args...), "testdata/function-set.json")
		code, stdout, stderr := runCmd(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%q = %d, stderr %q:\n%s", args, code, stderr, stdout)
		}
		for _, id := range tc.listed {
			if !strings.Contains(stdout, "  "+id+" ") && !strings.Contains(stdout, "  "+id+":") {
				t.Errorf("%q does not list %s:\n%s", args, id, stdout)
			}
		}
		for _, id := range tc.unlit {
			if strings.Contains(stdout, "  "+id+" ") || strings.Contains(stdout, "  "+id+":") {
				t.Errorf("%q lists %s, the function key's:\n%s", args, id, stdout)
			}
		}
	}
	for _, bad := range []string{"south", "select", "", "Start"} {
		code, _, stderr := runCmd(t, "validate", "--function-key", bad, "testdata/function-set.json")
		if code != 2 || !strings.Contains(stderr, "--function-key must be start, back or guide") {
			t.Errorf("--function-key %q = %d, stderr %q; want exit 2 and the message", bad, code, stderr)
		}
	}
}

// trace replays with the function key the flag names: a press of south while
// guide is held runs the function set's binding when guide is the function key,
// and the layer's when it is not (the default start is).
func TestTraceFunctionKeyFlag(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "fn.json")
	tracePath := filepath.Join(dir, "fn.jsonl")
	const prof = `{
  "schemaVersion": 1, "name": "fn",
  "layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}}},
  "functionSet": {"south": {"press": {"type": "keys", "keys": "F1"}}}
}
`
	const events = `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad","guid":"030000000a0000000a00000000000000","gamepad":true}}
{"timeNs":10000000,"kind":"down","device":1,"control":"guide"}
{"timeNs":100000000,"kind":"down","device":1,"control":"south"}
{"timeNs":200000000,"kind":"up","device":1,"control":"south"}
{"timeNs":300000000,"kind":"up","device":1,"control":"guide"}
`
	for path, text := range map[string]string{profilePath: prof, tracePath: events} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		flag      []string
		want, not string
	}{
		{nil, "output   +A\n", "+F1"},
		{[]string{"--function-key", "start"}, "output   +A\n", "+F1"},
		{[]string{"--function-key", "guide"}, "output   +F1\n", "+A"},
	} {
		args := append(append([]string{"trace"}, tc.flag...), "--profile", profilePath, "--input", tracePath)
		code, stdout, stderr := runCmd(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%q = %d, stderr %q:\n%s", args, code, stderr, stdout)
		}
		if !strings.Contains(stdout, tc.want) || strings.Contains(stdout, tc.not) {
			t.Errorf("%q: want %q and not %q:\n%s", args, tc.want, tc.not, stdout)
		}
	}
	code, _, stderr := runCmd(t, "trace", "--function-key", "south", "--profile", profilePath, "--input", tracePath)
	if code != 2 || !strings.Contains(stderr, "--function-key must be start, back or guide") {
		t.Errorf("--function-key south = %d, stderr %q; want exit 2 and the message", code, stderr)
	}
}
