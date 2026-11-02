package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shoulder button or a trigger can be the function key (ADR-0017): validate
// compiles with it and leaves out its bindings, a trigger's second stage too.
func TestValidateFunctionKeyJoyCon(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "joycon.json")
	const prof = `{
  "schemaVersion": 1, "name": "joycon",
  "layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}, "rt": {"press": {"type": "keys", "keys": "B"}}, "rt.full": {"press": {"type": "keys", "keys": "C"}}, "leftshoulder": {"press": {"type": "keys", "keys": "D"}}}}
}
`
	if err := os.WriteFile(profilePath, []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flag         string
		listed, nots []string
	}{
		{"start", []string{"/layers/base/rt", "/layers/base/rt.full", "/layers/base/leftshoulder"}, nil},
		{"rt", []string{"/layers/base/south", "/layers/base/leftshoulder"}, []string{"/layers/base/rt ", "/layers/base/rt.full"}},
		{"leftshoulder", []string{"/layers/base/rt", "/layers/base/rt.full"}, []string{"/layers/base/leftshoulder"}},
		{"lt", []string{"/layers/base/rt", "/layers/base/rt.full"}, nil},
	} {
		code, stdout, stderr := runCmd(t, "validate", "--function-key", tc.flag, profilePath)
		if code != 0 || stderr != "" {
			t.Fatalf("--function-key %s = %d, stderr %q:\n%s", tc.flag, code, stderr, stdout)
		}
		for _, id := range tc.listed {
			if !strings.Contains(stdout, id) {
				t.Errorf("--function-key %s: want %s listed:\n%s", tc.flag, id, stdout)
			}
		}
		for _, id := range tc.nots {
			if strings.Contains(stdout, id) {
				t.Errorf("--function-key %s: %s listed, the function key's:\n%s", tc.flag, id, stdout)
			}
		}
	}
	// The retired paddles are no function key.
	code, _, stderr := runCmd(t, "validate", "--function-key", "paddle3", profilePath)
	if code != 2 || !strings.Contains(stderr, "--function-key must be start, back, leftshoulder, rightshoulder, lt or rt") {
		t.Errorf("--function-key paddle3 = %d, stderr %q; want exit 2 and the message", code, stderr)
	}
}

// trace follows the function key of the controller in the trace (ADR-0017): a
// single Joy-Con has its trigger (ZR here, a right one), a pair back, another
// controller start; --config gives a controller its own, and --function-key
// overrides all.
func TestTraceFunctionKeyPerController(t *testing.T) {
	dir := t.TempDir()
	const prof = `{
  "schemaVersion": 1, "name": "fn",
  "layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}, "rt": {"press": {"type": "keys", "keys": "C"}}, "rightshoulder": {"press": {"type": "keys", "keys": "D"}}}},
  "functionSet": {"south": {"press": {"type": "keys", "keys": "F1"}}}
}
`
	const joyGUID = "030000007e0500006620000000000000"
	const pairGUID = "030000007e0500006820000000000000"
	// events presses and releases the control around a press of south; a
	// trigger is an axis in a trace.
	events := func(guid, typ, control string) string {
		down := `{"timeNs":10000000,"kind":"down","device":1,"control":"` + control + `"}`
		up := `{"timeNs":300000000,"kind":"up","device":1,"control":"` + control + `"}`
		if control == "rt" {
			down = `{"timeNs":10000000,"kind":"axis","device":1,"control":"righttrigger","value":20000}`
			up = `{"timeNs":300000000,"kind":"axis","device":1,"control":"righttrigger","value":0}`
		}
		return `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad","guid":"` + guid + `","type":"` + typ + `","gamepad":true}}
` + down + `
{"timeNs":100000000,"kind":"down","device":1,"control":"south"}
{"timeNs":200000000,"kind":"up","device":1,"control":"south"}
` + up + "\n"
	}
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	profilePath := write("fn.json", prof)
	joy := write("joy.jsonl", events(joyGUID, "joyconright", "rt"))
	joyShoulder := write("joy-shoulder.jsonl", events(joyGUID, "joyconright", "rightshoulder"))
	pair := write("pair.jsonl", events(pairGUID, "joyconpair", "back"))
	pairStart := write("pair-start.jsonl", events(pairGUID, "joyconpair", "start"))
	config := write("config.json", `{"schemaVersion":0,"devices":{"`+pairGUID+`":{"functionKey":"start"},"`+joyGUID+`":{"functionKey":"rightshoulder"}}}`)
	for _, tc := range []struct {
		name      string
		args      []string
		trace     string
		want, not string
	}{
		{"a Joy-Con has its trigger", nil, joy, "output   +F1\n", "+A"},
		{"a Joy-Con's shoulder is ordinary", nil, joyShoulder, "output   +A\n", "+F1"},
		{"a pair has back", nil, pair, "output   +F1\n", "+A"},
		{"a pair, start is ordinary", nil, pairStart, "output   +A\n", "+F1"},
		{"the config's choice", []string{"--config", config}, pairStart, "output   +F1\n", "+A"},
		{"the config's choice, back is ordinary", []string{"--config", config}, pair, "output   +A\n", "+F1"},
		{"the config's shoulder for a Joy-Con", []string{"--config", config}, joyShoulder, "output   +F1\n", "+A"},
		{"the config's shoulder, the trigger is ordinary", []string{"--config", config}, joy, "output   +C\n", "+F1"},
		{"forced to start", []string{"--function-key", "start"}, joy, "output   +C\n", "+F1"},
		{"forced to a trigger", []string{"--function-key", "rt"}, joy, "output   +F1\n", "+A"},
		{"forced to a shoulder", []string{"--function-key", "rightshoulder"}, joyShoulder, "output   +F1\n", "+A"},
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
