package input

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// The captures of the spike (jc2/testdata/capture-*.txt) are the reports of
// real Joy-Cons 2. This test replays them through the hub, as the reports of
// a fake link at the times they were captured, and holds the events to the
// golden files of package jc2, which were made by mapping and diffing the same
// reports directly: the hub must add nothing, lose nothing and reorder
// nothing. It also runs the silence watchdog over the whole capture, whose
// largest gap is 121 ms. (The R capture's "button-x" and "button-y" steps
// were pressed swapped; the data is right.)
//
// Regenerate the golden files in package jc2 (go test ./internal/input/jc2
// -update) if the mapping changes on purpose; this test follows.

// The stick calibrations that were measured on the owner's controllers (the
// calL and calR of the jc2 tests).
var (
	captureCalR = jc2.StickCal{NeutralX: 2053, NeutralY: 1999, PosX: 1225, PosY: 1205, NegX: 1242, NegY: 1209}
	captureCalL = jc2.StickCal{NeutralX: 2049, NeutralY: 2112, PosX: 1241, PosY: 1211, NegX: 1180, NegY: 1249}
)

// capLine is a line of a capture: a report, or a mark.
type capLine struct {
	ms     string
	at     time.Duration
	report []byte // nil for a mark
	mark   string
}

func readCaptureLines(t *testing.T, path string) []capLine {
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
			if len(fields) != 3+jc2.ReportLen {
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

func TestHubReplaysCaptures(t *testing.T) {
	for _, c := range []struct {
		name string
		side jc2.Side
		addr uint64
		ser  string
		cal  jc2.StickCal
	}{
		{"R", jc2.SideR, addrR1, serR1, captureCalR},
		{"L", jc2.SideL, addrL1, serL1, captureCalL},
	} {
		lines := readCaptureLines(t, filepath.Join("jc2", "testdata", "capture-"+c.name+".txt"))
		{
			mode := jc2.Vertical
			t.Run(c.name+"-"+mode.String(), func(t *testing.T) {
				r := newRig(t, knownAuto)
				n := len(r.tr.connectList())
				r.now = lines[0].at
				for _, l := range lines {
					if l.report != nil {
						r.now = l.at - time.Millisecond
						break
					}
				}
				r.advertise(c.addr, c.side, true)
				id := r.tr.connectList()[n].id
				r.feed(connectedEvent(id, c.side, c.ser, factoryBlock(c.cal)))
				if got := r.takeDesc(); len(got) != 1 || !strings.HasPrefix(got[0], "added 1") {
					t.Fatalf("connect events %q", got)
				}

				var got []string
				last := time.Duration(0)
				for _, l := range lines {
					if l.report == nil {
						got = append(got, "# "+l.mark)
						continue
					}
					r.step(l.at - r.now) // the timers run before each report, as in T-input
					var rep jc2win.Event
					rep.Kind, rep.Link = jc2win.Report, id
					copy(rep.Report[:], l.report)
					r.feed(rep)
					for _, e := range r.take() {
						switch e.Kind {
						case ButtonDown:
							got = append(got, l.ms+" +"+e.Control)
						case ButtonUp:
							got = append(got, l.ms+" -"+e.Control)
						case AxisMotion:
							got = append(got, fmt.Sprintf("%s %s=%d", l.ms, e.Control, e.Value))
						default:
							t.Fatalf("%s: unexpected %s during the capture (the watchdog must not fire)", l.ms, desc(e))
						}
					}
					last = l.at
				}

				// The golden file has "end" lines for the state being released; the
				// hub reports a removal instead, which its consumer turns into the
				// same release.
				raw, err := os.ReadFile(filepath.Join("jc2", "testdata", "golden", c.name+"-"+mode.String()+".txt"))
				if err != nil {
					t.Fatal(err)
				}
				var want []string
				for line := range strings.Lines(string(raw)) {
					line = strings.TrimRight(line, "\r\n")
					if !strings.HasPrefix(line, "end ") {
						want = append(want, line)
					}
				}
				if len(got) != len(want) {
					t.Errorf("%d events, golden has %d", len(got), len(want))
				}
				for i := 0; i < min(len(got), len(want)); i++ {
					if got[i] != want[i] {
						t.Fatalf("event %d: %q, golden %q", i, got[i], want[i])
					}
				}

				// The capture ends: dormant after 300 ms, closed after 3 s.
				r.step(last + jc2.DefaultSilence - time.Millisecond - r.now)
				if evs := r.take(); len(evs) != 0 {
					t.Fatalf("events %v before the silence timeout", descs(evs))
				}
				r.step(time.Millisecond)
				if got := r.takeDesc(); len(got) != 1 || got[0] != "removed 1" {
					t.Fatalf("events %q at the silence timeout, want the removal", got)
				}
				r.step(jc2.DefaultLinkGiveUp)
				if d := r.tr.disconnectList(); len(d) != 1 || d[0] != id {
					t.Errorf("disconnects %v after 3 s of silence", d)
				}
			})
		}
	}
}
