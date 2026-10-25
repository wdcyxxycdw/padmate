package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func TestTraceReplay(t *testing.T) {
	code, stdout, stderr := runCmd(t, "trace", "--profile", "../../../examples/profiles/basic.json", "--input", "../../../examples/traces/basic.jsonl")
	if code != 0 || stderr != "" {
		t.Fatalf("trace = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"input", "decision", "output", "+B", "-B"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("trace output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestTraceJSON(t *testing.T) {
	code, stdout, stderr := runCmd(t, "trace", "--json", "--profile", "../../../examples/profiles/basic.json", "--input", "../../../examples/traces/basic.jsonl")
	if code != 0 || stderr != "" {
		t.Fatalf("trace --json = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, `"kind":"decision"`) || !strings.Contains(stdout, `"output":"+B"`) {
		t.Fatalf("trace JSON lacks decision/output:\n%s", stdout)
	}
}

// padmatectl trace prints every move of a stick that moves the pointer
// (ADR-0062), where padmated's dry run sums them, and the jump of a mouse
// action.
func TestTracePointer(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "pointer.json")
	tracePath := filepath.Join(dir, "pointer.jsonl")
	const prof = `{
  "schemaVersion": 1, "name": "pointer",
  "analog": {"ls": {"mode": "mouse", "mouseSpeed": 1000, "deadzone": 0, "saturation": 1}},
  "layers": {"base": {"south": {"press": {"type": "mouse", "op": "jump", "x": 0.5, "y": 0.5}}}}
}
`
	const events = `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad","guid":"030000000a0000000a00000000000000","gamepad":true}}
{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":32767}
{"timeNs":0,"kind":"axis","device":1,"control":"lefty","value":0}
{"timeNs":20000000,"kind":"axis","device":1,"control":"leftx","value":0}
{"timeNs":100000000,"kind":"down","device":1,"control":"south"}
{"timeNs":110000000,"kind":"up","device":1,"control":"south"}
`
	for path, text := range map[string]string{profilePath: prof, tracePath: events} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "trace", "--profile", profilePath, "--input", tracePath)
	if code != 0 || stderr != "" {
		t.Fatalf("trace = %d, stderr %q:\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"@8 output   move +8,+0\n",
		"@16 output   move +8,+0\n",
		"@24 output   move +4,+0\n",
		"@100 output   jump 32768,32768\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("trace output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "sc:0x") {
		t.Errorf("trace printed a move as a scan code:\n%s", stdout)
	}
	code, stdout, stderr = runCmd(t, "trace", "--json", "--profile", profilePath, "--input", tracePath)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"output":"move +8,+0"`) || !strings.Contains(stdout, `"output":"jump 32768,32768"`) {
		t.Fatalf("trace --json = %d, stderr %q:\n%s", code, stderr, stdout)
	}
}

// A trace may end with the stick still pushed, like a key held at the end: the
// replay does not wait for the pointer, which would never stop, and the
// ReleaseAll after it ends it (ADR-0062).
func TestTracePointerEndingPushed(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "pointer.json")
	tracePath := filepath.Join(dir, "pointer.jsonl")
	const prof = `{
  "schemaVersion": 1, "name": "pointer",
  "analog": {"ls": {"mode": "mouse", "mouseSpeed": 1000, "deadzone": 0, "saturation": 1}},
  "layers": {"base": {}}
}
`
	const events = `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad","guid":"030000000a0000000a00000000000000","gamepad":true}}
{"timeNs":0,"kind":"axis","device":1,"control":"leftx","value":32767}
{"timeNs":0,"kind":"axis","device":1,"control":"lefty","value":0}
{"timeNs":50000000,"kind":"axis","device":1,"control":"rightx","value":0}
`
	for path, text := range map[string]string{profilePath: prof, tracePath: events} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "trace", "--profile", profilePath, "--input", tracePath)
	if code != 0 || stderr != "" {
		t.Fatalf("trace of a stick left pushed = %d, stderr %q:\n%s", code, stderr, stdout)
	}
	for _, want := range []string{"@8 output   move +8,+0\n", "@48 output   move +8,+0\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("trace output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "@56 ") {
		t.Errorf("the pointer went on after the last event:\n%s", stdout)
	}
}

// A typed text (OUT-21) is one output line a character, with the character
// quoted, where the keys are one line an event: `+B`, `text "你"`. The units of a
// surrogate pair and the release of a unit are not lines of their own.
func TestTraceSenderTypedText(t *testing.T) {
	var buf strings.Builder
	s := &traceSender{clk: clock.NewFake(), w: &traceWriter{w: &buf}}
	for _, batch := range [][]output.KeyEvent{
		{output.UnicodeEvent('a', false), output.UnicodeEvent('a', true)},
		{output.UnicodeEvent(0xD83D, false), output.UnicodeEvent(0xD83D, true), output.UnicodeEvent(0xDE00, false), output.UnicodeEvent(0xDE00, true)},
		{output.UnicodeEvent('"', false), output.UnicodeEvent('"', true)},
		{{Scan: 0x1C}, {Scan: 0x1C, Up: true}},
	} {
		if n, err := s.Send(batch); n != len(batch) || err != nil {
			t.Fatalf("Send = %d, %v", n, err)
		}
	}
	want := "@0 output   text \"a\"\n" +
		"@0 output   text \"😀\"\n" +
		"@0 output   text \"\\\"\"\n" +
		"@0 output   +sc:0x1C\n" +
		"@0 output   -sc:0x1C\n"
	if got := buf.String(); got != want {
		t.Errorf("trace output:\n%s\nwant:\n%s", got, want)
	}

	buf.Reset()
	js := &traceSender{clk: clock.NewFake(), w: &traceWriter{w: &buf, json: true}}
	if _, err := js.Send([]output.KeyEvent{output.UnicodeEvent('你', false), output.UnicodeEvent('你', true)}); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "{\"t\":0,\"kind\":\"output\",\"output\":\"text \\\"你\\\"\"}\n"; got != want {
		t.Errorf("trace JSON: %s, want %s", got, want)
	}
}
