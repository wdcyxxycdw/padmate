package main

import (
	"strings"
	"testing"
)

func TestRunUsage(t *testing.T) {
	tests := []struct {
		args       []string
		code       int
		wantStdout string // substring; "" means empty
		wantStderr string // substring; "" means empty
	}{
		{nil, 2, "", "usage: padmatectl <command>"},
		{[]string{"-h"}, 0, "usage: padmatectl <command>", ""},
		{[]string{"help"}, 0, "keys", ""},
		{[]string{"version"}, 0, "padmatectl ", ""},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
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
