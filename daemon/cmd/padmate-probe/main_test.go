package main

import (
	"bytes"
	"strings"
	"testing"
)

// runProbe runs padmate-probe with args and returns the exit code and
// output. Only use it for arguments that fail before touching Windows
// (flag errors, help, version): valid record or dump-layout arguments
// would install a real hook or load real layouts. Valid window arguments are
// safe once a test has replaced openWindowReader with a fake.
func runProbe(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunUsage(t *testing.T) {
	tests := []struct {
		args       []string
		code       int
		wantStdout string // substring; "" means empty
		wantStderr string // substring; "" means empty
	}{
		{nil, 2, "", "usage: padmate-probe <command>"},
		{[]string{"-h"}, 0, "usage: padmate-probe <command>", ""},
		{[]string{"--help"}, 0, "dump-layout", ""},
		{[]string{"help"}, 0, "record", ""},
		{[]string{"version"}, 0, "padmate-probe ", ""},
		{[]string{"--version"}, 0, "padmate-probe ", ""},
		{[]string{"-version"}, 0, "padmate-probe ", ""},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"record", "-h"}, 0, "usage: padmate-probe record", ""},
		{[]string{"dump-layout", "--help"}, 0, "usage: padmate-probe dump-layout", ""},
		{[]string{"window", "-h"}, 0, "usage: padmate-probe window [--hwnd 0xHEX] [--json]", ""},
		{[]string{"help"}, 0, "window", ""},
	}
	for _, tt := range tests {
		code, stdout, stderr := runProbe(t, tt.args...)
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

func TestUsageListsCommands(t *testing.T) {
	_, stdout, _ := runProbe(t, "help")
	want := "commands:\n" +
		"  dump-layout  write keyboard layout fixtures\n" +
		"  record       print the keyboard events a low-level hook sees\n" +
		"  window       print the foreground window and the extended style of a window\n" +
		"  version      print the version\n"
	if !strings.HasSuffix(stdout, want) {
		t.Errorf("usage =\n%s\nwant it to end with\n%s", stdout, want)
	}
}
