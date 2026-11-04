package main

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames/tablegen"
)

func TestCSPNamesUsage(t *testing.T) {
	code, stdout, _ := runProbe(t, "csp-names", "-h")
	if code != 0 || !strings.Contains(stdout, "usage: padmate-probe csp-names") {
		t.Errorf("-h: code %d, stdout %q", code, stdout)
	}
	for _, args := range [][]string{
		{"csp-names"},
		{"csp-names", "--install", "x"},
		{"csp-names", "--out", "y"},
		{"csp-names", "--install", "x", "--out", "y", "extra"},
		{"csp-names", "--install", "x", "--out", "y", "--region-a", "nonsense"},
		{"csp-names", "--install", "x", "--out", "y", "--region-b", "0x20-0x10"},
		{"csp-names", "--bogus"},
	} {
		if code, _, stderr := runProbe(t, args...); code != 2 || stderr == "" {
			t.Errorf("%q: code %d, stderr %q; want 2 and a message", args, code, stderr)
		}
	}
}

func TestCSPNamesMissingInstall(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runProbe(t, "csp-names", "--install", dir, "--out", dir+"/t.json")
	if code != 1 || !strings.Contains(stderr, "padmate-probe csp-names:") {
		t.Errorf("code %d, stderr %q", code, stderr)
	}
}

func TestParseRegion(t *testing.T) {
	tests := []struct {
		in      string
		want    tablegen.Region
		wantErr bool
	}{
		{"", tablegen.Region{}, false},
		{"0x140f80000-0x140fb0000", tablegen.Region{Start: 0x140f80000, End: 0x140fb0000}, false},
		{"140f80000-140fb0000", tablegen.Region{Start: 0x140f80000, End: 0x140fb0000}, false},
		{"0x20-0x10", tablegen.Region{}, true},
		{"0x10-0x10", tablegen.Region{}, true},
		{"abc", tablegen.Region{}, true},
		{"0x10", tablegen.Region{}, true},
	}
	for _, tt := range tests {
		got, err := parseRegion(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseRegion(%q) = %+v, %v; want %+v, error=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
