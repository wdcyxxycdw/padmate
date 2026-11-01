package jc2

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata/golden")

// A capture is the input reports of a real Joy-Con 2 with the steps the owner
// was asked to do (testdata/capture-*.txt). The reports are decoded with Map
// in horizontal and in vertical mode, and the events that Diff makes from
// them are compared with testdata/golden/<case>.txt.
//
// In the right capture the steps "button-x" and "button-y" were pressed
// swapped: during "button-x" the owner pressed Y, and during "button-y" X. The
// data is right, the labels are not, and wantButton below says so.

// capLine is a line of a capture: a report, or a mark.
type capLine struct {
	ms     string // the time as written, for the golden files
	at     time.Duration
	report []byte // nil for a mark
	mark   string // "button-a start" for a mark
}

// readCapture reads a capture file; # lines are comments.
func readCapture(t *testing.T, path string) []capLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []capLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s: bad line %q", path, line)
		}
		ms, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			t.Fatalf("%s: bad time in %q", path, line)
		}
		l := capLine{ms: fields[0], at: time.Duration(ms * float64(time.Millisecond))}
		switch fields[1] {
		case "MARK":
			l.mark = strings.Join(fields[2:], " ")
		case "side":
			if len(fields) != 3+ReportLen || fields[2] != strconv.Itoa(ReportLen) {
				t.Fatalf("%s: bad report line %q", path, line)
			}
			for _, h := range fields[3:] {
				v, err := strconv.ParseUint(h, 16, 8)
				if err != nil {
					t.Fatalf("%s: bad byte %q", path, h)
				}
				l.report = append(l.report, byte(v))
			}
		default:
			t.Fatalf("%s: unexpected line kind in %q", path, line)
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// event formats a change the way the golden files have it.
func event(c Change) string {
	switch {
	case c.Control.IsAxis():
		return fmt.Sprintf("%s=%d", c.Control.Name(), c.Value)
	case c.Down:
		return "+" + c.Control.Name()
	}
	return "-" + c.Control.Name()
}

var captures = []struct {
	name string
	side Side
	cal  StickCal
	file string
}{
	{"R", SideR, calR, "testdata/capture-R.txt"},
	{"L", SideL, calL, "testdata/capture-L.txt"},
}

func TestGolden(t *testing.T) {
	for _, c := range captures {
		lines := readCapture(t, c.file)
		for _, mode := range []Mode{Horizontal, Vertical} {
			name := c.name + "-" + mode.String()
			t.Run(name, func(t *testing.T) {
				var sb strings.Builder
				var prev State
				for _, l := range lines {
					if l.report == nil {
						fmt.Fprintf(&sb, "# %s\n", l.mark)
						continue
					}
					r, err := ParseReport(c.side, l.report)
					if err != nil {
						t.Fatal(err)
					}
					next := Map(mode, c.side, r, c.cal)
					changes := Diff(prev, next)
					if apply(prev, changes) != next {
						t.Fatalf("%s: the changes do not make the state", l.ms)
					}
					for _, ch := range changes {
						fmt.Fprintf(&sb, "%s %s\n", l.ms, event(ch))
					}
					prev = next
				}
				// The last state lets go of everything.
				for _, ch := range Released(prev) {
					fmt.Fprintf(&sb, "end %s\n", event(ch))
				}
				compareGolden(t, filepath.Join("testdata", "golden", name+".txt"), sb.String())
			})
		}
	}
}

// compareGolden compares got with the file, or rewrites it with -update.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to make it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs; run with -update and look at the diff", path)
	}
}

// wantButton is the physical button that each step of a capture should show.
// The right capture's "button-x" and "button-y" are swapped (see above).
var wantButton = map[Side]map[string]Button{
	SideR: {
		"button-a": ButtonA, "button-b": ButtonB,
		"button-x": ButtonY, "button-y": ButtonX, // pressed swapped
		"button-r": ButtonR, "button-zr": ButtonZR, "button-plus": ButtonPlus,
		"button-stick-click": ButtonRStick, "button-home": ButtonHome, "button-c": ButtonC,
		"button-sl": ButtonSL, "button-sr": ButtonSR,
	},
	SideL: {
		"button-up": ButtonUp, "button-down": ButtonDown, "button-left": ButtonLeft, "button-right": ButtonRight,
		"button-l": ButtonL, "button-zl": ButtonZL, "button-minus": ButtonMinus,
		"button-stick-click": ButtonLStick, "button-capture": ButtonCapture,
		"button-sl": ButtonSL, "button-sr": ButtonSR,
	},
}

// TestCaptureSteps checks that in each step of the captures the expected
// button was the one pressed, and that Map turns it into the control of the
// layout.
func TestCaptureSteps(t *testing.T) {
	for _, c := range captures {
		t.Run(c.name, func(t *testing.T) {
			lines := readCapture(t, c.file)
			var (
				step     string
				inStep   bool
				prevBtn  = numButtons // the button of the step before
				expect   = numButtons
				pressedN int
				seenOnce bool
				steps    int
			)
			finish := func() {
				if expect != numButtons {
					steps++
					if pressedN < 5 {
						t.Errorf("step %s: %v seen in %d reports, want at least 5", step, expect, pressedN)
					}
					prevBtn = expect
				}
				expect, pressedN, seenOnce = numButtons, 0, false
			}
			for _, l := range lines {
				if l.report == nil {
					name, what, _ := strings.Cut(l.mark, " ")
					switch what {
					case "start":
						step, inStep = name, true
						expect = numButtons
						if b, ok := wantButton[c.side][name]; ok {
							expect = b
						}
					case "end":
						finish()
						inStep = false
					}
					continue
				}
				r, err := ParseReport(c.side, l.report)
				if err != nil {
					t.Fatal(err)
				}
				for b := Button(0); b < numButtons; b++ {
					if !r.Buttons.Has(b) {
						continue
					}
					switch {
					case !inStep:
						t.Errorf("%s: %v pressed before the first step", l.ms, b)
					case b == expect:
						pressedN++
						seenOnce = true
						// Every mode maps it to the control its layout says.
						for _, mode := range []Mode{Horizontal, Vertical} {
							lay, _ := Layout(mode, c.side)
							s := Map(mode, c.side, r, c.cal)
							for _, bind := range lay.Bindings {
								if bind.From != b {
									continue
								}
								if bind.To.IsAxis() && s.Axis(bind.To) != 32767 || !bind.To.IsAxis() && !s.Down(bind.To) {
									t.Errorf("%s: %v in %v mode does not set %v", l.ms, b, mode, bind.To)
								}
							}
						}
					case b == prevBtn && !seenOnce:
						// The button of the step before, let go a moment late.
					default:
						t.Errorf("step %s (expects %v): %v pressed at %s", step, expect, b, l.ms)
					}
				}
			}
			if want := len(wantButton[c.side]); steps != want {
				t.Errorf("%d button steps, want %d", steps, want)
			}
		})
	}
}

// TestCaptureSticksAndIdle checks the stick: it reaches the ends of its travel
// in the sticks step, and rests near the center while idle.
func TestCaptureSticksAndIdle(t *testing.T) {
	for _, c := range captures {
		t.Run(c.name, func(t *testing.T) {
			lines := readCapture(t, c.file)
			step := "pre"
			lo, hi := map[string][2]int16{}, map[string][2]int16{}
			for _, l := range lines {
				if l.report == nil {
					step, _, _ = strings.Cut(l.mark, " ")
					continue
				}
				r, _ := ParseReport(c.side, l.report)
				if step == "sticks" || step == "idle" || step == "disconnect-test" {
					if r.Buttons != 0 {
						t.Errorf("%s %s: buttons %#x", step, l.ms, uint32(r.Buttons))
					}
				}
				s := Map(Vertical, c.side, r, c.cal)
				x, y := ControlLeftX, ControlLeftY
				if c.side == SideR {
					x, y = ControlRightX, ControlRightY
				}
				v := [2]int16{s.Axis(x), s.Axis(y)}
				a, b := lo[step], hi[step]
				for i := range v {
					a[i], b[i] = min(a[i], v[i]), max(b[i], v[i])
				}
				lo[step], hi[step] = a, b
			}
			// The test drew circles: the travel reaches 90% each way.
			const reach = 29490 // 0.9 * 32767
			for i, axis := range []string{"x", "y"} {
				if lo["sticks"][i] > -reach || hi["sticks"][i] < reach {
					t.Errorf("sticks: %s ranges %d..%d, want at least ±%d", axis, lo["sticks"][i], hi["sticks"][i], reach)
				}
			}
			// At rest the stick is within 5%, the left one a little off.
			const rest = 1638 // 0.05 * 32767
			for _, step := range []string{"pre", "idle", "disconnect-test"} {
				for i := range 2 {
					if lo[step][i] < -rest || hi[step][i] > rest {
						t.Errorf("%s: axis %d ranges %d..%d at rest", step, i, lo[step][i], hi[step][i])
					}
				}
			}
		})
	}
}

// TestCaptureTiming checks the counter and the timing the watchdog is built
// on: the counter counts every report, no gap passes DefaultSilence, and the
// link is dead soon after the last report.
func TestCaptureTiming(t *testing.T) {
	for _, c := range captures {
		t.Run(c.name, func(t *testing.T) {
			var (
				s     Silence
				last  time.Duration
				prevC = -1
				maxG  time.Duration
				n     int
			)
			for _, l := range readCapture(t, c.file) {
				if l.report == nil {
					continue
				}
				r, _ := ParseReport(c.side, l.report)
				if prevC >= 0 && int(r.Counter) != (prevC+1)&0xff {
					t.Errorf("%s: counter %d after %d", l.ms, r.Counter, prevC)
				}
				prevC = int(r.Counter)
				if n > 0 {
					maxG = max(maxG, l.at-last)
					if s.Dead(l.at) {
						t.Errorf("%s: watchdog fired %v after the last report", l.ms, l.at-last)
					}
				}
				s.Seen(l.at)
				last = l.at
				n++
			}
			if maxG < 100*time.Millisecond || maxG > 125*time.Millisecond {
				t.Errorf("longest gap %v, measured about 121 ms", maxG)
			}
			if s.Dead(last+DefaultSilence-time.Millisecond) || !s.Dead(last+DefaultSilence) {
				t.Errorf("the watchdog does not fire %v after the last report", DefaultSilence)
			}
		})
	}
}
