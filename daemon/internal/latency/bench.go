package latency

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The environment of the latency tests (`task bench`, ENG-11).
const (
	EnvBench = "PADMATE_BENCH"       // "1" runs the latency tests
	EnvScale = "PADMATE_BENCH_SCALE" // sample-count multiplier, (0, 10]; "" is 1
	EnvGate  = "PADMATE_BENCH_GATE"  // "0" off; "" or "auto"; "1" strict
	EnvDir   = "PADMATE_BENCH_DIR"   // absolute existing directory for the reports; "" writes none
	MaxScale = 10
)

// ParseScale parses EnvScale.
func ParseScale(s string) (float64, error) {
	if s == "" {
		return 1, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("latency: %s=%q is not a number", EnvScale, s)
	}
	if !(f > 0 && f <= MaxScale) { // also rejects NaN
		return 0, fmt.Errorf("latency: %s=%q is not in (0, %d]", EnvScale, s, MaxScale)
	}
	return f, nil
}

// Scaled returns max(4, ⌈n0·scale⌉ rounded up to a multiple of 4). A
// product within 1e-9 of an integer counts as that integer, so that
// Scaled(200, 1.1) is 220, not 224 (200·1.1 is 220.00000000000003).
func Scaled(n0 int, scale float64) int {
	x := float64(n0) * scale
	if !(x > 0) {
		return 4
	}
	c := math.Ceil(x)
	if r := math.Round(x); math.Abs(x-r) <= 1e-9*r {
		c = r
	}
	return max(4, (int(c)+3)/4*4)
}

// GateMode is EnvGate: whether a latency test evaluates its p99 gate.
type GateMode int

const (
	GateAuto   GateMode = iota // evaluate when possible, else skip with a reason
	GateNone                   // "0": never evaluate
	GateStrict                 // "1": fail when the gate cannot be evaluated
)

// ParseGateMode parses EnvGate.
func ParseGateMode(s string) (GateMode, error) {
	switch s {
	case "", "auto":
		return GateAuto, nil
	case "0":
		return GateNone, nil
	case "1":
		return GateStrict, nil
	}
	return 0, fmt.Errorf(`latency: %s=%q is not "", "auto", "0" or "1"`, EnvGate, s)
}

// Decide says whether the gate of rows is evaluated. err is non-nil only in
// GateStrict when it cannot be.
//
//   - GateNone gives GateOff.
//   - rows with no gated row give GateOff: there is nothing to evaluate.
//   - Without injection, the gate is skipped, whatever the sample counts:
//     the no-op sender measures no SendInput.
//   - A gated row with fewer samples than its MinN skips the gate, and the
//     reason names every such row, as in "A has N=40 < 2000".
//   - Otherwise it is evaluated.
func Decide(mode GateMode, inject bool, rows []Row) (g Gate, reason string, err error) {
	if mode == GateNone {
		return GateOff, EnvGate + "=0", nil
	}
	var gated bool
	var short []string
	for _, r := range rows {
		if !r.Gated() {
			continue
		}
		gated = true
		if !r.Enough() {
			short = append(short, fmt.Sprintf("%s has N=%d < %d", r.Name, r.Total.N, r.MinN))
		}
	}
	switch {
	case !gated:
		return GateOff, "no gated rows", nil
	case !inject:
		reason = "no injection (PADMATE_TEST_INJECT unset)"
	case len(short) > 0:
		reason = strings.Join(short, ", ")
	default:
		return GateEvaluated, "", nil
	}
	if mode == GateStrict {
		err = fmt.Errorf("latency: %s=1, but the gate cannot be evaluated: %s", EnvGate, reason)
	}
	return GateSkipped, reason, err
}

// ParseDir checks EnvDir: "" gives ""; otherwise it must be an absolute
// path of an existing directory (go test runs in the package directory).
// It returns the path cleaned.
func ParseDir(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("latency: %s=%q is not an absolute path", EnvDir, s)
	}
	fi, err := os.Stat(s)
	if err != nil {
		return "", fmt.Errorf("latency: %s: %w", EnvDir, err)
	}
	if !fi.IsDir() {
		return "", errors.New("latency: " + EnvDir + "=" + strconv.Quote(s) + " is not a directory")
	}
	return filepath.Clean(s), nil
}
