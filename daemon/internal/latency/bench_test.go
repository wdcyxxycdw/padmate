package latency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseScale(t *testing.T) {
	ok := map[string]float64{"": 1, "1": 1, "0.02": 0.02, "5": 5, "10": 10, "1e-3": 0.001}
	for s, want := range ok {
		if got, err := ParseScale(s); err != nil || got != want {
			t.Errorf("ParseScale(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"0", "-1", "NaN", "Inf", "+Inf", "-Inf", "10.5", "1e9", "x", " 1", "1,5"} {
		if got, err := ParseScale(s); err == nil || !strings.Contains(err.Error(), EnvScale) {
			t.Errorf("ParseScale(%q) = %v, %v; want an error naming %s", s, got, err, EnvScale)
		}
	}
}

func TestScaled(t *testing.T) {
	cases := []struct {
		n0    int
		scale float64
		want  int
	}{
		{2000, 0.02, 40},
		{32, 0.02, 4},
		{6, 1, 8},
		{2000, 1, 2000},
		{2000, 10, 20000},
		{2001, 1, 2004},
		{200, 1.1, 220}, // 200·1.1 is 220.00000000000003
		{100, 0.005, 4}, // 0.5
		{0, 1, 4},
		{-8, 1, 4},
		{1000, 0.0413, 44}, // 41.3
	}
	for _, c := range cases {
		if got := Scaled(c.n0, c.scale); got != c.want {
			t.Errorf("Scaled(%d, %v) = %d, want %d", c.n0, c.scale, got, c.want)
		}
	}
}

func TestParseGateMode(t *testing.T) {
	ok := map[string]GateMode{"": GateAuto, "auto": GateAuto, "0": GateNone, "1": GateStrict}
	for s, want := range ok {
		if got, err := ParseGateMode(s); err != nil || got != want {
			t.Errorf("ParseGateMode(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"2", "yes", "true", "AUTO", " 1"} {
		if _, err := ParseGateMode(s); err == nil || !strings.Contains(err.Error(), EnvGate) {
			t.Errorf("ParseGateMode(%q): %v, want an error naming %s", s, err, EnvGate)
		}
	}
}

func TestParseDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ok := map[string]string{"": "", dir: dir, dir + string(filepath.Separator): dir}
	for s, want := range ok {
		if got, err := ParseDir(s); err != nil || got != want {
			t.Errorf("ParseDir(%q) = %q, %v; want %q", s, got, err, want)
		}
	}
	bad := map[string]string{
		"bin":                      "not an absolute path",
		"./bin":                    "not an absolute path",
		filepath.Join(dir, "gone"): EnvDir,
		file:                       "not a directory",
	}
	for s, want := range bad {
		if got, err := ParseDir(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseDir(%q) = %q, %v; want an error with %q", s, got, err, want)
		}
	}
}

func TestDecide(t *testing.T) {
	gated := func(name string, n, minN int) Row {
		return Row{Name: name, Total: Summary{N: n}, Target: time.Millisecond, MinN: minN}
	}
	info := Row{Name: "H", Total: Summary{N: 40}}
	full := []Row{gated("A", 2000, 2000), gated("B", 2400, 2000), info}
	short := []Row{gated("A", 40, 2000), gated("B", 2000, 2000), gated("C", 8, 400), info}
	cases := []struct {
		name    string
		mode    GateMode
		inject  bool
		rows    []Row
		gate    Gate
		reason  string
		wantErr bool
	}{
		{"off", GateNone, true, full, GateOff, "PADMATE_BENCH_GATE=0", false},
		{"off, strict rules do not apply", GateNone, false, short, GateOff, "PADMATE_BENCH_GATE=0", false},
		{"no gated rows", GateAuto, true, []Row{info}, GateOff, "no gated rows", false},
		{"no gated rows, strict", GateStrict, false, nil, GateOff, "no gated rows", false},
		{"no injection", GateAuto, false, full, GateSkipped, "no injection (PADMATE_TEST_INJECT unset)", false},
		{"no injection, strict", GateStrict, false, full, GateSkipped, "no injection (PADMATE_TEST_INJECT unset)", true},
		// No injection wins over short rows: the no-op rows measure no SendInput at all.
		{"no injection, below MinN", GateAuto, false, short, GateSkipped, "no injection (PADMATE_TEST_INJECT unset)", false},
		{"no injection, below MinN, strict", GateStrict, false, short, GateSkipped, "no injection (PADMATE_TEST_INJECT unset)", true},
		{"below MinN", GateAuto, true, short, GateSkipped, "A has N=40 < 2000, C has N=8 < 400", false},
		{"below MinN, strict", GateStrict, true, short, GateSkipped, "A has N=40 < 2000, C has N=8 < 400", true},
		{"evaluated", GateAuto, true, full, GateEvaluated, "", false},
		{"evaluated, strict", GateStrict, true, full, GateEvaluated, "", false},
	}
	for _, c := range cases {
		g, reason, err := Decide(c.mode, c.inject, c.rows)
		if g != c.gate || reason != c.reason || (err != nil) != c.wantErr {
			t.Errorf("%s: Decide = %v, %q, %v; want %v, %q, error %v", c.name, g, reason, err, c.gate, c.reason, c.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: error %q does not give the reason", c.name, err)
		}
	}
}
