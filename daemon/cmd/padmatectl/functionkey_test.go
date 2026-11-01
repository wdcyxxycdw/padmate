package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validate and trace read no config.json, so the function key (ADR-0012) is a
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
	// guide was the third choice until padmate stopped mapping it (ADR-0012).
	for _, bad := range []string{"south", "select", "", "Start", "guide"} {
		code, _, stderr := runCmd(t, "validate", "--function-key", bad, "testdata/function-set.json")
		if code != 2 || !strings.Contains(stderr, "--function-key must be start, back or paddle1 to paddle4") {
			t.Errorf("--function-key %q = %d, stderr %q; want exit 2 and the message", bad, code, stderr)
		}
	}
}

// trace replays with the function key the flag names: a press of south while
// back is held runs the function set's binding when back is the function key,
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
{"timeNs":10000000,"kind":"down","device":1,"control":"back"}
{"timeNs":100000000,"kind":"down","device":1,"control":"south"}
{"timeNs":200000000,"kind":"up","device":1,"control":"south"}
{"timeNs":300000000,"kind":"up","device":1,"control":"back"}
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
		{[]string{"--function-key", "back"}, "output   +F1\n", "+A"},
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
	for _, bad := range []string{"south", "guide"} {
		code, _, stderr := runCmd(t, "trace", "--function-key", bad, "--profile", profilePath, "--input", tracePath)
		if code != 2 || !strings.Contains(stderr, "--function-key must be start, back or paddle1 to paddle4") {
			t.Errorf("--function-key %s = %d, stderr %q; want exit 2 and the message", bad, code, stderr)
		}
	}
}

// A paddle can be the function key (ADR-0017): validate compiles with it and
// leaves out its bindings.
func TestValidateFunctionKeyPaddle(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "paddle.json")
	const prof = `{
  "schemaVersion": 1, "name": "paddle",
  "layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}, "paddle3": {"press": {"type": "keys", "keys": "B"}}}}
}
`
	if err := os.WriteFile(profilePath, []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flag        string
		listed, not string
	}{
		{"start", "/layers/base/paddle3", ""},
		{"paddle3", "/layers/base/south", "/layers/base/paddle3"},
		{"paddle1", "/layers/base/paddle3", ""},
	} {
		code, stdout, stderr := runCmd(t, "validate", "--function-key", tc.flag, profilePath)
		if code != 0 || stderr != "" {
			t.Fatalf("--function-key %s = %d, stderr %q:\n%s", tc.flag, code, stderr, stdout)
		}
		if !strings.Contains(stdout, tc.listed) || tc.not != "" && strings.Contains(stdout, tc.not) {
			t.Errorf("--function-key %s: want %s and not %q:\n%s", tc.flag, tc.listed, tc.not, stdout)
		}
	}
}

// trace follows the function key of the controller in the trace (ADR-0017): a
// single Joy-Con held sideways has paddle3, a pair back, another controller
// start; --config gives a controller its own, and --function-key overrides all.
func TestTraceFunctionKeyPerController(t *testing.T) {
	dir := t.TempDir()
	const prof = `{
  "schemaVersion": 1, "name": "fn",
  "layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}, "paddle3": {"press": {"type": "keys", "keys": "C"}}}},
  "functionSet": {"south": {"press": {"type": "keys", "keys": "F1"}}}
}
`
	const joyGUID = "030000007e0500006620000000000000"
	const pairGUID = "030000007e0500006820000000000000"
	events := func(guid, typ, control string) string {
		return `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad","guid":"` + guid + `","type":"` + typ + `","gamepad":true}}
{"timeNs":10000000,"kind":"down","device":1,"control":"` + control + `"}
{"timeNs":100000000,"kind":"down","device":1,"control":"south"}
{"timeNs":200000000,"kind":"up","device":1,"control":"south"}
{"timeNs":300000000,"kind":"up","device":1,"control":"` + control + `"}
`
	}
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	profilePath := write("fn.json", prof)
	joy := write("joy.jsonl", events(joyGUID, "joyconright", "paddle3"))
	pair := write("pair.jsonl", events(pairGUID, "joyconpair", "back"))
	pairStart := write("pair-start.jsonl", events(pairGUID, "joyconpair", "start"))
	config := write("config.json", `{"schemaVersion":0,"devices":{"`+pairGUID+`":{"functionKey":"start"}}}`)
	for _, tc := range []struct {
		name      string
		args      []string
		trace     string
		want, not string
	}{
		{"a Joy-Con has paddle3", nil, joy, "output   +F1\n", "+A"},
		{"a pair has back", nil, pair, "output   +F1\n", "+A"},
		{"a pair, start is ordinary", nil, pairStart, "output   +A\n", "+F1"},
		{"the config's choice", []string{"--config", config}, pairStart, "output   +F1\n", "+A"},
		{"the config's choice, back is ordinary", []string{"--config", config}, pair, "output   +A\n", "+F1"},
		{"forced to start", []string{"--function-key", "start"}, joy, "output   +A\n", "+F1"},
		{"forced to a paddle", []string{"--function-key", "paddle3"}, joy, "output   +F1\n", "+A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{"trace"}, tc.args...), "--profile", profilePath, "--input", tc.trace)
			code, stdout, stderr := runCmd(t, args...)
			if code != 0 || stderr != "" {
				t.Fatalf("%q = %d, stderr %q:\n%s", args, code, stderr, stdout)
			}
			if !strings.Contains(stdout, tc.want) || strings.Contains(stdout, tc.not) {
				t.Errorf("want %q and not %q:\n%s", tc.want, tc.not, stdout)
			}
		})
	}
	code, _, stderr := runCmd(t, "trace", "--config", config, "--function-key", "back", "--profile", profilePath, "--input", joy)
	if code != 2 || !strings.Contains(stderr, "cannot be used together") {
		t.Errorf("--config with --function-key = %d, stderr %q", code, stderr)
	}
}
