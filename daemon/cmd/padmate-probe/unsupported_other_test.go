//go:build !windows

package main

import (
	"strings"
	"testing"
)

func TestUnsupported(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"record"}, "padmate-probe: record is only supported on Windows\n"},
		{[]string{"record", "--json", "--swallow", "padmate"}, "padmate-probe: record is only supported on Windows\n"},
		{[]string{"record", "--mouse", "--swallow", "injected"}, "padmate-probe: record is only supported on Windows\n"},
		{[]string{"dump-layout"}, "padmate-probe: dump-layout is only supported on Windows\n"},
		{[]string{"dump-layout", "--out", t.TempDir(), "00000409"}, "padmate-probe: dump-layout is only supported on Windows\n"},
		{[]string{"window"}, "padmate-probe: window is only supported on Windows\n"},
		{[]string{"window", "--json", "--hwnd", "0x40B8E"}, "padmate-probe: window is only supported on Windows\n"},
	} {
		code, stdout, stderr := runProbe(t, tc.args...)
		if code != 1 || stdout != "" || stderr != tc.want {
			t.Errorf("%q = %d, stdout %q, stderr %q; want 1 and %q", tc.args, code, stdout, stderr, tc.want)
		}
	}
	if code, stdout, _ := runProbe(t, "--version"); code != 0 || !strings.HasPrefix(stdout, "padmate-probe ") {
		t.Errorf("--version = %d, %q", code, stdout)
	}
}
