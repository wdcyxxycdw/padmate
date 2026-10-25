package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runCmd runs padmatectl with args and returns its exit code and output.
func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestKeysParseCanonical(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "ctrl+shift+z")
	if code != 0 || stdout != "Ctrl+Shift+Z\n" || stderr != "" {
		t.Fatalf("keys parse ctrl+shift+z = %d, stdout %q, stderr %q; want 0, %q, empty", code, stdout, stderr, "Ctrl+Shift+Z\n")
	}
}

func TestKeysParseMany(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "shift + ctrl + z", "f13", "Ctrl+Plus", "keyz", "sc:0xe01d", "Shift+Ctrl")
	want := "Ctrl+Shift+Z\nF13\nCtrl+Plus\nKeyZ\nsc:0xE01D\nCtrl+Shift\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("keys parse = %d, stdout %q, stderr %q; want 0, %q, empty", code, stdout, stderr, want)
	}
}

func TestKeysParseErrors(t *testing.T) {
	// Errors go to stderr with a caret under the offending token; the valid
	// specs around them are still printed, and the exit code is 1.
	code, stdout, stderr := runCmd(t, "keys", "parse", "Ctrl+Z", "Ctrl+Foo", "Z+Ctrl", "Ctrl++", "", "Alt+Tab")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if want := "Ctrl+Z\nAlt+Tab\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	want := strings.Join([]string{
		`padmatectl keys parse: Ctrl+Foo: unknown key "Foo"`,
		`                            ^^^`,
		`padmatectl keys parse: Z+Ctrl: "Z" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)`,
		`                       ^`,
		`padmatectl keys parse: Ctrl++: empty key name; write Plus for the + character`,
		`                            ^`,
		`padmatectl keys parse: : empty key spec`,
		``,
	}, "\n")
	if stderr != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", stderr, want)
	}
}

func TestCaretLine(t *testing.T) {
	tests := []struct {
		s      string
		off, n int
		want   string
	}{
		{"Ctrl+Foo", 5, 3, "  " + "     ^^^"},
		{"Ctrl+", 4, 1, "  " + "    ^"},
		{"Ctrl+", 5, 0, "  " + "     ^"},
		{"\tCtrl+X", 1, 4, "  " + "\t^^^^"},
		{"é+Ctrl+Z", 0, 2, "  " + "^"},
		{"Ctrl+é", 5, 2, "  " + "     ^"},
		{"é+Z+Q", 3, 1, "  " + "  ^"},
	}
	for _, tt := range tests {
		if got := caretLine(2, tt.s, tt.off, tt.n); got != tt.want {
			t.Errorf("caretLine(%q, %d, %d) = %q, want %q", tt.s, tt.off, tt.n, got, tt.want)
		}
	}
}

func TestKeysParseJSON(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "--json", "ctrl+shift+z", "Ctrl+Plus", "Pause", "sc:0xE01D", "Shift", "Foo")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `padmatectl keys parse: Foo: unknown key "Foo"`) {
		t.Errorf("stderr = %q, want the error for Foo", stderr)
	}
	type us struct {
		Scan  uint8  `json:"scan"`
		Ext   bool   `json:"ext"`
		E1    bool   `json:"e1"`
		VK    uint16 `json:"vk"`
		Shift bool   `json:"shift"`
	}
	type object struct {
		Spec      string   `json:"spec"`
		Canonical string   `json:"canonical"`
		Modifiers []string `json:"modifiers"`
		Key       string   `json:"key"`
		Kind      string   `json:"kind"`
		US        us       `json:"us"`
		Error     string   `json:"error"`
		Offset    int      `json:"offset"`
		Length    int      `json:"length"`
	}
	want := []object{
		{Spec: "ctrl+shift+z", Canonical: "Ctrl+Shift+Z", Modifiers: []string{"Ctrl", "Shift"}, Key: "Z", Kind: "char", US: us{Scan: 0x2C, VK: 0x5A}},
		{Spec: "Ctrl+Plus", Canonical: "Ctrl+Plus", Modifiers: []string{"Ctrl"}, Key: "Plus", Kind: "char", US: us{Scan: 0x0D, VK: 0xBB, Shift: true}},
		{Spec: "Pause", Canonical: "Pause", Modifiers: []string{}, Key: "Pause", Kind: "named", US: us{Scan: 0x45, E1: true, VK: 0x13}},
		{Spec: "sc:0xE01D", Canonical: "sc:0xE01D", Modifiers: []string{}, Key: "sc:0xE01D", Kind: "scan", US: us{Scan: 0x1D, Ext: true}},
		{Spec: "Shift", Canonical: "Shift", Modifiers: []string{}, Key: "Shift", Kind: "modifier", US: us{Scan: 0x2A, VK: 0xA0}},
		{Spec: "Foo", Error: `unknown key "Foo"`, Offset: 0, Length: 3},
	}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	i := 0
	for ; sc.Scan(); i++ {
		line := sc.Text()
		if i >= len(want) {
			t.Fatalf("extra line %q", line)
		}
		var got object
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("line %d %q: %v", i, line, err)
		}
		if !equalObjects(got.Modifiers, want[i].Modifiers) || got.Spec != want[i].Spec ||
			got.Canonical != want[i].Canonical || got.Key != want[i].Key || got.Kind != want[i].Kind ||
			got.US != want[i].US || got.Error != want[i].Error || got.Offset != want[i].Offset || got.Length != want[i].Length {
			t.Errorf("line %d = %+v, want %+v", i, got, want[i])
		}
	}
	if i != len(want) {
		t.Errorf("got %d lines, want %d", i, len(want))
	}
	// A chord without modifiers must have "modifiers":[] rather than null,
	// so consumers can iterate without a nil check.
	if !strings.Contains(stdout, `"spec":"Pause","canonical":"Pause","modifiers":[]`) {
		t.Errorf("stdout lacks an empty modifiers array for Pause:\n%s", stdout)
	}
}

// A mouse key has no scan code and VK: "keys parse --json" gives it "mouse"
// and no "us" (ADR-0062).
func TestKeysParseJSONMouse(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "--json", "ctrl+wheelup", "WheelRight", "Z", "WheelUp+Ctrl")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for WheelUp+Ctrl", code)
	}
	if !strings.Contains(stderr, `"WheelUp" is not a modifier`) {
		t.Errorf("stderr = %q, want the error for WheelUp+Ctrl", stderr)
	}
	want := []string{
		`{"spec":"ctrl+wheelup","canonical":"Ctrl+WheelUp","modifiers":["Ctrl"],"key":"WheelUp","kind":"mouse","mouse":"WheelUp"}`,
		`{"spec":"WheelRight","canonical":"WheelRight","modifiers":[],"key":"WheelRight","kind":"mouse","mouse":"WheelRight"}`,
		`{"spec":"Z","canonical":"Z","modifiers":[],"key":"Z","kind":"char","us":{"scan":44,"ext":false,"vk":90}}`,
		`{"spec":"WheelUp+Ctrl","error":"\"WheelUp\" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)","offset":0,"length":7}`,
	}
	if got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); !equalObjects(got, want) {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, strings.Join(want, "\n"))
	}
}

// A media key has the scan code and the VK of its key, like any keyboard key,
// and the kind "media" (OUT-20); an alias is shown as the canonical name.
func TestKeysParseJSONMedia(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "--json", "volumeup", "Ctrl+AudioVolumeMute", "MediaTrackNext", "VolumeDown+Ctrl")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for VolumeDown+Ctrl", code)
	}
	if !strings.Contains(stderr, `"VolumeDown" is not a modifier`) {
		t.Errorf("stderr = %q, want the error for VolumeDown+Ctrl", stderr)
	}
	want := []string{
		`{"spec":"volumeup","canonical":"VolumeUp","modifiers":[],"key":"VolumeUp","kind":"media","us":{"scan":48,"ext":true,"vk":175}}`,
		`{"spec":"Ctrl+AudioVolumeMute","canonical":"Ctrl+VolumeMute","modifiers":["Ctrl"],"key":"VolumeMute","kind":"media","us":{"scan":32,"ext":true,"vk":173}}`,
		`{"spec":"MediaTrackNext","canonical":"MediaNext","modifiers":[],"key":"MediaNext","kind":"media","us":{"scan":25,"ext":true,"vk":176}}`,
		`{"spec":"VolumeDown+Ctrl","error":"\"VolumeDown\" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)","offset":0,"length":10}`,
	}
	if got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); !equalObjects(got, want) {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, strings.Join(want, "\n"))
	}
	if code, stdout, _ := runCmd(t, "keys", "parse", "audiovolumeup", "shift+mediastop"); code != 0 || stdout != "VolumeUp\nShift+MediaStop\n" {
		t.Errorf("keys parse = %d, %q, want the canonical names", code, stdout)
	}
}

// The mouse buttons are shown like the wheel keys, with the canonical name
// whatever alias was written (ADR-0062).
func TestKeysParseJSONButtons(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "parse", "--json", "lbutton", "Shift+MouseRight", "xbutton2", "MouseLeft+Ctrl")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for MouseLeft+Ctrl", code)
	}
	if !strings.Contains(stderr, `"MouseLeft" is not a modifier`) {
		t.Errorf("stderr = %q, want the error for MouseLeft+Ctrl", stderr)
	}
	want := []string{
		`{"spec":"lbutton","canonical":"MouseLeft","modifiers":[],"key":"MouseLeft","kind":"mouse","mouse":"MouseLeft"}`,
		`{"spec":"Shift+MouseRight","canonical":"Shift+MouseRight","modifiers":["Shift"],"key":"MouseRight","kind":"mouse","mouse":"MouseRight"}`,
		`{"spec":"xbutton2","canonical":"MouseX2","modifiers":[],"key":"MouseX2","kind":"mouse","mouse":"MouseX2"}`,
		`{"spec":"MouseLeft+Ctrl","error":"\"MouseLeft\" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)","offset":0,"length":9}`,
	}
	if got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); !equalObjects(got, want) {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, strings.Join(want, "\n"))
	}
	// The plain form prints the canonical name.
	if code, stdout, _ := runCmd(t, "keys", "parse", "ctrl+rbutton", "MBUTTON"); code != 0 || stdout != "Ctrl+MouseRight\nMouseMiddle\n" {
		t.Errorf("keys parse = %d, %q, want the canonical names", code, stdout)
	}
}

func TestKeysParseJSONNoHTMLEscape(t *testing.T) {
	code, stdout, _ := runCmd(t, "keys", "parse", "--json", "Shift+<")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout, `"spec":"Shift+<"`) {
		t.Errorf("stdout = %q, want the spec with a literal <", stdout)
	}
}

func equalObjects(a, b []string) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeysList(t *testing.T) {
	code, stdout, stderr := runCmd(t, "keys", "list")
	if code != 0 || stderr != "" {
		t.Fatalf("keys list = %d, stderr %q", code, stderr)
	}
	rows := map[string][]string{}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	for i, line := range lines {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line %d %q has trailing spaces", i, line)
		}
		f := strings.Fields(line)
		rows[f[0]] = f
	}
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "NAME KIND SCAN VK ALIASES" {
		t.Errorf("header = %q", lines[0])
	}
	if len(lines) != 1+190 {
		t.Errorf("got %d rows, want 190 keys and a header", len(lines)-1)
	}
	tests := []struct {
		name string
		want string
	}{
		{"F13", "F13 named 0x64 0x7C"},
		{"F22", "F22 named 0x6D 0x85"},
		{"F23", "F23 named 0x6E 0x86"},
		{"F24", "F24 named 0x76 0x87"},
		{"RCtrl", "RCtrl modifier E0 1D 0xA3 ControlRight"},
		{"RAlt", "RAlt modifier E0 38 0xA5 AltRight, AltGr"},
		{"Pause", "Pause named E1 1D 45 0x13"},
		{"PrintScreen", "PrintScreen named E0 37 0x2C PrtSc"},
		{"NumLock", "NumLock named 0x45 0x90"},
		{"Z", "Z char 0x2C 0x5A"},
		{"KeyZ", "KeyZ physical 0x2C 0x5A"},
		{"Plus", "Plus char 0x0D Shift+0xBB"},
		{"-", "- char 0x0C 0xBD"},
		{"Key-", "Key- physical 0x0C 0xBD Minus"},
		{"Key1", "Key1 physical 0x02 0x31 Digit1"},
		// The media and volume keys (OUT-20) are keyboard keys with an E0 scan code
		// and a VK; the aliases are the W3C names.
		{"VolumeUp", "VolumeUp media E0 30 0xAF AudioVolumeUp"},
		{"VolumeDown", "VolumeDown media E0 2E 0xAE AudioVolumeDown"},
		{"VolumeMute", "VolumeMute media E0 20 0xAD AudioVolumeMute"},
		{"MediaPlayPause", "MediaPlayPause media E0 22 0xB3"},
		{"MediaNext", "MediaNext media E0 19 0xB0 MediaTrackNext"},
		{"MediaPrevious", "MediaPrevious media E0 10 0xB1 MediaTrackPrevious"},
		{"MediaStop", "MediaStop media E0 24 0xB2"},
		// A mouse key is no keyboard key: no scan code and no VK. The wheel
		// keys have no aliases, the buttons the Windows names.
		{"WheelUp", "WheelUp mouse - -"},
		{"WheelDown", "WheelDown mouse - -"},
		{"WheelLeft", "WheelLeft mouse - -"},
		{"WheelRight", "WheelRight mouse - -"},
		{"MouseLeft", "MouseLeft mouse - - LButton"},
		{"MouseRight", "MouseRight mouse - - RButton"},
		{"MouseMiddle", "MouseMiddle mouse - - MButton"},
		{"MouseX1", "MouseX1 mouse - - XButton1"},
		{"MouseX2", "MouseX2 mouse - - XButton2"},
	}
	for _, tt := range tests {
		if got := strings.Join(rows[tt.name], " "); got != tt.want {
			t.Errorf("row %s = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestKeysUsage(t *testing.T) {
	tests := []struct {
		args       []string
		code       int
		wantStdout string // substring
		wantStderr string // substring
	}{
		{[]string{"keys"}, 2, "", "usage: padmatectl keys <subcommand>"},
		{[]string{"keys", "bogus"}, 2, "", `unknown subcommand "bogus"`},
		{[]string{"keys", "-h"}, 0, "usage: padmatectl keys <subcommand>", ""},
		{[]string{"keys", "help"}, 0, "usage: padmatectl keys <subcommand>", ""},
		{[]string{"keys", "parse"}, 2, "", "no key specs given"},
		{[]string{"keys", "parse", "--bogus", "Z"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"keys", "parse", "-h"}, 0, "usage: padmatectl keys parse", ""},
		{[]string{"keys", "parse", "--help"}, 0, "usage: padmatectl keys parse", ""},
		{[]string{"keys", "parse", "--", "--json"}, 1, "", `unknown key "--json"`},
		{[]string{"keys", "list", "extra"}, 2, "", `unexpected argument "extra"`},
		{[]string{"keys", "list", "--bogus"}, 2, "", "usage: padmatectl keys list"},
		{[]string{"keys", "list", "-h"}, 0, "usage: padmatectl keys list", ""},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCmd(t, tt.args...)
		if code != tt.code {
			t.Errorf("%q: exit code = %d, want %d", tt.args, code, tt.code)
		}
		if tt.wantStdout == "" && stdout != "" || !strings.Contains(stdout, tt.wantStdout) {
			t.Errorf("%q: stdout = %q, want %q", tt.args, stdout, tt.wantStdout)
		}
		if tt.wantStderr == "" && stderr != "" || !strings.Contains(stderr, tt.wantStderr) {
			t.Errorf("%q: stderr = %q, want %q", tt.args, stderr, tt.wantStderr)
		}
	}
}
