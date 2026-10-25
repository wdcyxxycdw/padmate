package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestFormatMs(t *testing.T) {
	for _, tt := range []struct {
		t    clock.Time
		want string
	}{
		{0, "0.000"},
		{1, "0.000"},
		{999, "0.000"},
		{1000, "0.001"},
		{20_000_000, "20.000"},
		{512_304_100, "512.304"},
		{11_718_645_999, "11718.645"},
	} {
		if got := formatMs(tt.t); got != tt.want {
			t.Errorf("formatMs(%d) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestWidth(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{`"撤销"`, 6},
		{"ｶﾀｶﾅ", 4},     // halfwidth katakana
		{"ＡＢ", 4},       // fullwidth Latin
		{"한글", 4},       // Hangul
		{"e\u0301", 1},  // a combining accent
		{"a\u200db", 2}, // a zero width joiner
		{"😀", 2},
		{"✏", 1},
		{"✏\uFE0F", 2},        // emoji presentation
		{"😀\uFE0F", 2},        // already wide
		{"\"✏\uFE0F 画笔\"", 9}, // a label from a profile
		{"\uFE0F", 0},         // nothing before it
	} {
		if got := width(tt.s); got != tt.want {
			t.Errorf("width(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
	if got := pad(`"撤销"`, 8); got != `"撤销"  ` {
		t.Errorf("pad = %q, want two spaces after the label", got)
	}
	if got := pad("toolong", 3); got != "toolong" {
		t.Errorf("pad of a longer string = %q, want it unchanged", got)
	}
}

func TestEdgeName(t *testing.T) {
	z, ok := keys.Lookup("Z")
	if !ok {
		t.Fatal("no key Z")
	}
	for _, tt := range []struct {
		e    output.KeyEvent
		want string
	}{
		{output.KeyEvent{Key: z, Scan: 0x2C}, "+Z"},
		{output.KeyEvent{Key: z, Scan: 0x2C, Up: true}, "-Z"},
		{output.KeyEvent{Scan: 0x2C}, "+sc:0x2C"},
		{output.KeyEvent{Scan: 0x1D, Ext: true, Up: true}, "-sc:0xE01D"},
		{output.KeyEvent{VK: 0x13, Scan: 0x45, ByVK: true}, "+vk:0x13"},
	} {
		if got := edgeName(tt.e); got != tt.want {
			t.Errorf("edgeName(%v) = %q, want %q", tt.e, got, tt.want)
		}
	}
}

// testPrinter returns a printer of the basic profile to w, in live mode
// when live.
func testPrinter(t *testing.T, w io.Writer, clk clock.Clock, jsonl, live bool) *printer {
	t.Helper()
	_, prog, _, ok := loadProfile(basicProfile, io.Discard)
	if !ok {
		t.Fatalf("cannot load %s", basicProfile)
	}
	pr := newPrinter(bufio.NewWriter(w), clk, jsonl, prog, liveMaxMs)
	pr.live = live
	return pr
}

// TestPrinterTimeWidth: the time column fits the times of the run: 24 h
// in live mode, a minute after the last event in a replay.
func TestPrinterTimeWidth(t *testing.T) {
	for _, tc := range []struct {
		maxMs uint64
		want  int
	}{
		{liveMaxMs, len("86400000.000")},
		{60_000, 10},
		{11_718 + 60_000, 10},
		{1_000_000, len("1000000.000")},
	} {
		if pr := newPrinter(bufio.NewWriter(io.Discard), clock.NewFake(), false, nil, tc.maxMs); pr.timeW != tc.want {
			t.Errorf("newPrinter(maxMs %d).timeW = %d, want %d", tc.maxMs, pr.timeW, tc.want)
		}
	}
	var buf bytes.Buffer
	clk := clock.NewFake()
	clk.Set(clock.Time(24*time.Hour - time.Microsecond))
	pr := testPrinter(t, &buf, clk, false, true)
	pr.exit()
	if want := "86399999.999  exit: release all\n"; buf.String() != want {
		t.Errorf("24 h after the start: %q, want %q", buf.String(), want)
	}
}

// TestPrinterReasonWidth: the reason column fits the name of every reason
// the engine has, up to the first value without one.
func TestPrinterReasonWidth(t *testing.T) {
	pr := newPrinter(bufio.NewWriter(io.Discard), clock.NewFake(), false, nil, 0)
	last := engine.Reason(0)
	for r := engine.ReasonDispatch; !strings.HasPrefix(r.String(), "reason("); r++ {
		if w := len(r.String()); w > pr.widths[1] {
			t.Errorf("reason %s takes %d columns, the column has %d", r, w, pr.widths[1])
		}
		last = r
	}
	// newPrinter measures the reasons up to ReasonScript.
	if last != engine.ReasonScript {
		t.Errorf("the last reason is %s, not %s: newPrinter must measure it", last, engine.ReasonScript)
	}
}

// writes records each Write on its own.
type writes struct {
	mu     sync.Mutex
	chunks []string
}

func (w *writes) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunks = append(w.chunks, string(b))
	return len(b), nil
}

// TestPrinterLiveFlush: in live mode each line is written as it is
// printed; a replay writes at the flush.
func TestPrinterLiveFlush(t *testing.T) {
	z := output.KeyEvent{Key: keys.MustParse("Z").Key(), Scan: 0x2C}
	south := engine.Decision{T: clock.Time(time.Millisecond), Control: control.South, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound}
	southJSON, err := south.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		jsonl bool
		want  []string
	}{
		{"text", false, []string{
			"       1.000  south down          unbound\n",
			"       1.000    send +Z\n",
			"       1.000  device 2 \"pad\" added to slot 0\n",
		}},
		{"jsonl", true, []string{string(southJSON) + "\n", `{"t":1,"send":["+Z"]}` + "\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake()
			clk.Set(clock.Time(time.Millisecond))
			var w writes
			pr := testPrinter(t, &w, clk, tc.jsonl, true)
			pr.decision(0, south)
			if _, err := pr.Send([]output.KeyEvent{z}); err != nil {
				t.Fatal(err)
			}
			pr.slotEvent(0, input.Event{Device: 2, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2, Name: "pad"}})
			if !slices.Equal(w.chunks, tc.want) {
				t.Errorf("writes %q, want %q", w.chunks, tc.want)
			}
			if err := pr.flush(); err != nil || len(w.chunks) != len(tc.want) {
				t.Errorf("flush() = %v after %d writes, want nil and nothing left", err, len(w.chunks))
			}
		})
	}
	var w writes
	pr := testPrinter(t, &w, clock.NewFake(), false, false)
	pr.exit()
	if len(w.chunks) != 0 {
		t.Errorf("a replay wrote %q before the flush", w.chunks)
	}
	if err := pr.flush(); err != nil || len(w.chunks) != 1 {
		t.Errorf("flush() = %v, writes %q; want nil, one", err, w.chunks)
	}
}

// TestPrinterWriteError: the first write error is kept for the flush.
func TestPrinterWriteError(t *testing.T) {
	pr := testPrinter(t, failWriter{}, clock.NewFake(), false, true)
	pr.exit()
	pr.exit()
	if err := pr.flush(); err == nil || err.Error() != "disk full" {
		t.Errorf("flush() = %v, want disk full", err)
	}
}

// TestPrinterReload: a reload is a note, as the exit is: none in JSON
// lines.
func TestPrinterReload(t *testing.T) {
	for _, tc := range []struct {
		name  string
		jsonl bool
		want  string
	}{
		{"text", false, "       0.000  profile reloaded: release all\n"},
		{"jsonl", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			pr := testPrinter(t, &buf, clock.NewFake(), tc.jsonl, true)
			pr.reload(0, 2)
			if err := pr.flush(); err != nil || buf.String() != tc.want {
				t.Errorf("flush() = %v, output %q; want nil, %q", err, buf.String(), tc.want)
			}
		})
	}
}

// TestPrinterReloadOncePerGen: each slot notes the reload it switches to,
// and the first of a generation prints the marker.
func TestPrinterReloadOncePerGen(t *testing.T) {
	var buf bytes.Buffer
	pr := testPrinter(t, &buf, clock.NewFake(), false, true)
	pr.reload(0, 2)
	pr.reload(1, 2)
	pr.reload(2, 3)
	pr.reload(0, 3)
	const line = "       0.000  profile reloaded: release all\n"
	if err := pr.flush(); err != nil || buf.String() != line+line {
		t.Errorf("flush() = %v, output %q; want nil, two markers", err, buf.String())
	}
}

// TestPrinterPause: a pause and a resume (DMN-06) are notes, as a reload
// is: none in JSON lines. In live mode OnPause gives the state; in a
// replay each ComboHeld (DMN-07) toggles it, as the slot does.
func TestPrinterPause(t *testing.T) {
	const (
		paused  = "       0.000  mapping paused: release all\n"
		resumed = "       0.000  mapping resumed\n"
	)
	combo := input.Event{Device: 1, Kind: input.ComboHeld}
	for _, tc := range []struct {
		name  string
		jsonl bool
		live  bool
		do    func(*printer)
		want  string
	}{
		{"live", false, true, func(pr *printer) { pr.pause(true, "combo"); pr.pause(false, "api") }, paused + resumed},
		{"live jsonl", true, true, func(pr *printer) { pr.pause(true, "combo"); pr.pause(false, "combo") }, ""},
		{"replay", false, false, func(pr *printer) { pr.event(combo); pr.event(combo); pr.event(combo) }, paused + resumed + paused},
		{"replay jsonl", true, false, func(pr *printer) { pr.event(combo) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			pr := testPrinter(t, &buf, clock.NewFake(), tc.jsonl, tc.live)
			tc.do(pr)
			if err := pr.flush(); err != nil || buf.String() != tc.want {
				t.Errorf("flush() = %v, output %q; want nil, %q", err, buf.String(), tc.want)
			}
		})
	}
}

// TestPrinterSuspend: each change of the system's suspension (DMN-09),
// alone and with the user's pause, gives its note; a pause or a resume
// while suspended says that the suspension holds the mapping. JSON lines
// have no notes.
func TestPrinterSuspend(t *testing.T) {
	const (
		lock, desktop = winsession.Lock, winsession.Desktop
		prefix        = "       0.000  "
	)
	for _, tc := range []struct {
		name  string
		jsonl bool
		do    func(*printer)
		want  []string
	}{
		{"lock", false, func(pr *printer) { pr.suspend(0, lock); pr.suspend(lock, 0) },
			[]string{"mapping suspended (lock): release all", "mapping unsuspended"}},
		{"reasons change", false, func(pr *printer) {
			pr.suspend(0, desktop)
			pr.suspend(desktop, lock|desktop)
			pr.suspend(lock|desktop, lock)
			pr.suspend(lock, 0)
		}, []string{"mapping suspended (desktop): release all", "mapping suspended (lock,desktop)",
			"mapping suspended (lock)", "mapping unsuspended"}},
		{"collapsed", false, func(pr *printer) {
			pr.suspend(0, winsession.Sleep|winsession.End)
			pr.suspend(winsession.Sleep|winsession.End, 0)
		},
			[]string{"mapping suspended (sleep,end): release all", "mapping unsuspended"}},
		{"unchanged", false, func(pr *printer) { pr.suspend(0, 0); pr.suspend(0, lock); pr.suspend(lock, lock) },
			[]string{"mapping suspended (lock): release all"}},
		{"paused first", false, func(pr *printer) {
			pr.pause(true, "combo")
			pr.suspend(0, lock)
			pr.suspend(lock, 0)
			pr.pause(false, "combo")
		}, []string{"mapping paused: release all", "mapping suspended (lock)", "mapping unsuspended (still paused)", "mapping resumed"}},
		{"paused while suspended", false, func(pr *printer) {
			pr.suspend(0, lock)
			pr.pause(true, "combo")
			pr.suspend(lock, lock|winsession.Disconnect)
			pr.pause(false, "api")
			pr.suspend(lock|winsession.Disconnect, 0)
		}, []string{"mapping suspended (lock): release all", "mapping paused (still suspended: lock)",
			"mapping suspended (lock,disconnect)", "mapping resumed (still suspended: lock,disconnect)", "mapping unsuspended"}},
		{"resumed while suspended, unsuspended paused", false, func(pr *printer) {
			pr.pause(true, "api")
			pr.suspend(0, lock)
			pr.pause(false, "api")
			pr.pause(true, "api")
			pr.suspend(lock, 0)
		}, []string{"mapping paused: release all", "mapping suspended (lock)", "mapping resumed (still suspended: lock)",
			"mapping paused (still suspended: lock)", "mapping unsuspended (still paused)"}},
		{"jsonl", true, func(pr *printer) { pr.suspend(0, lock); pr.pause(true, "combo"); pr.suspend(lock, 0) }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			pr := testPrinter(t, &buf, clock.NewFake(), tc.jsonl, true)
			tc.do(pr)
			var want strings.Builder
			for _, l := range tc.want {
				want.WriteString(prefix + l + "\n")
			}
			if err := pr.flush(); err != nil || buf.String() != want.String() {
				t.Errorf("flush() = %v, output\n%s\nwant nil,\n%s", err, buf.String(), want.String())
			}
		})
	}
}

// basicBinding returns the binding id of the basic profile.
func basicBinding(t *testing.T, id string) *profile.Binding {
	t.Helper()
	_, prog, _, ok := loadProfile(basicProfile, io.Discard)
	if !ok {
		t.Fatalf("cannot load %s", basicProfile)
	}
	for _, b := range prog.Bindings() {
		if b.Binding.ID == id {
			return b.Binding
		}
	}
	t.Fatalf("%s has no binding %s", basicProfile, id)
	return nil
}

// eastDown is the decision of a press of east in the basic profile, at
// 512 ms.
func eastDown(t *testing.T) engine.Decision {
	t.Helper()
	east := basicBinding(t, "/layers/base/east")
	return engine.Decision{
		T: clock.Time(512 * time.Millisecond), Control: control.East, Edge: engine.EdgeDown, Cycle: 1, Level: "base",
		Binding: east, Action: east.Press, Owner: engine.OwnerID{Source: engine.SourceDecl, Cycle: 1}, Reason: engine.ReasonDispatch,
	}
}

// TestPrinterSlotColumn: with showSlots the header and the decision rows of
// every slot start the input column at one offset, "send" is 2 columns
// right of it, and a note starts at the slot column. Without it the rows
// are those of one slot, as before IN-08.
func TestPrinterSlotColumn(t *testing.T) {
	d := eastDown(t)
	space := output.KeyEvent{Key: keys.MustParse("Space").Key(), Scan: 0x39}
	lines := func(slots bool) []string {
		var buf bytes.Buffer
		clk := clock.NewFake()
		clk.Set(d.T)
		pr := testPrinter(t, &buf, clk, false, true)
		if slots {
			pr.showSlots()
		}
		pr.columnHeader()
		pr.slotEvent(3, input.Event{Device: 4, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 4, Name: "pad"}})
		pr.decision(0, d)
		if _, err := pr.Send([]output.KeyEvent{space}); err != nil {
			t.Fatal(err)
		}
		pr.decision(3, d)
		if err := pr.flush(); err != nil {
			t.Fatal(err)
		}
		return slices.Collect(strings.Lines(buf.String()))
	}
	one := []string{
		"        time  input               reason    action       label   binding                     owner\n",
		"     512.000  device 4 \"pad\" added to slot 3\n",
		"     512.000  east down           dispatch  hold Space   \"抓手\"  /layers/base/east           decl/1\n",
		"     512.000    send +Space\n",
		"     512.000  east down           dispatch  hold Space   \"抓手\"  /layers/base/east           decl/1\n",
	}
	if got := lines(false); !slices.Equal(got, one) {
		t.Errorf("without showSlots:\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(one, ""))
	}
	several := []string{
		"        time  slot  input               reason    action       label   binding                     owner\n",
		"     512.000  device 4 \"pad\" added to slot 3\n",
		"     512.000  0     east down           dispatch  hold Space   \"抓手\"  /layers/base/east           decl/1\n",
		"     512.000          send +Space\n",
		"     512.000  3     east down           dispatch  hold Space   \"抓手\"  /layers/base/east           decl/1\n",
	}
	got := lines(true)
	if !slices.Equal(got, several) {
		t.Fatalf("with showSlots:\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(several, ""))
	}
	// The rows are ASCII up to the columns compared: a byte is a column.
	slotCol, inCol := strings.Index(got[0], "slot"), strings.Index(got[0], "input")
	for _, row := range []struct {
		i    int
		slot byte
	}{{2, '0'}, {4, '3'}} {
		if c := strings.Index(got[row.i], "east"); c != inCol || got[row.i][slotCol] != row.slot {
			t.Errorf("decision row %q: input at %d, want %d, and slot %c at %d", got[row.i], c, inCol, row.slot, slotCol)
		}
	}
	if c := strings.Index(got[3], "send"); c != inCol+2 {
		t.Errorf("send row %q: send at %d, want %d", got[3], c, inCol+2)
	}
	if c := strings.Index(got[1], "device"); c != slotCol {
		t.Errorf("note %q: at %d, want %d", got[1], c, slotCol)
	}
}

// TestPrinterHaptic: the haptic of a binding (MAP-25) is a line "haptic
// slot=N preset" under the decision, indented like a send line, at the
// clock's time; the JSON lines of --decisions have none.
func TestPrinterHaptic(t *testing.T) {
	lines := func(jsonl, slots bool) []string {
		var buf bytes.Buffer
		clk := clock.NewFake()
		clk.Set(clock.Time(512 * time.Millisecond))
		pr := testPrinter(t, &buf, clk, jsonl, true)
		if slots {
			pr.showSlots()
		}
		pr.haptic(0, engine.Haptic{Preset: haptic.Tick})
		clk.Set(clock.Time(530 * time.Millisecond))
		pr.haptic(2, engine.Haptic{Preset: haptic.Pulse3, Strength: 1000})
		if err := pr.flush(); err != nil {
			t.Fatal(err)
		}
		return slices.Collect(strings.Lines(buf.String()))
	}
	if got, want := lines(false, false), []string{
		"     512.000    haptic slot=0 tick\n",
		"     530.000    haptic slot=2 pulse3\n",
	}; !slices.Equal(got, want) {
		t.Errorf("one slot:\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(want, ""))
	}
	if got, want := lines(false, true), []string{
		"     512.000          haptic slot=0 tick\n",
		"     530.000          haptic slot=2 pulse3\n",
	}; !slices.Equal(got, want) {
		t.Errorf("several slots:\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(want, ""))
	}
	if got := lines(true, false); len(got) != 0 {
		t.Errorf("JSON lines: %q; want none", got)
	}
}

// TestPrinterFeedback: what a slot's engine asks the controller to show
// (M3, HUD-08, UI-27) is a line "feedback slot=N mode=N [paused] [pulse] [color]"
// under the decision that caused it, indented like a send line, at the
// clock's time. A line is printed when a pulse starts or what it shows
// changes, so a refresh that changes nothing is silent; the JSON lines of
// --decisions have none.
func TestPrinterFeedback(t *testing.T) {
	pulse := engine.Feedback{Pulse: true, Pulses: 2, PulseMs: 70, GapMs: 100, Color: "#112233", Mode: 2, LED: true}
	paused := engine.Feedback{Paused: true, Pulse: true, Pulses: 1, PulseMs: 210, GapMs: 100, Color: "#FFAA00", Mode: 2, LED: true}
	quiet := engine.Feedback{Paused: true, Color: "#112233", Mode: 2, LED: true} // the pause switched off
	resumed := engine.Feedback{Color: "#112233", Mode: 2, LED: true}
	lightsOff := engine.Feedback{Mode: 2}
	lines := func(jsonl, slots bool) []string {
		var buf bytes.Buffer
		clk := clock.NewFake()
		pr := testPrinter(t, &buf, clk, jsonl, true)
		if slots {
			pr.showSlots()
		}
		at := func(ms int, slot uint8, f engine.Feedback) {
			clk.Set(clock.Time(time.Duration(ms) * time.Millisecond))
			pr.feedback(slot, f)
		}
		at(0, 0, engine.Feedback{Mode: 1, LED: true}) // the start state: nothing to say
		at(10, 0, pulse)
		at(20, 0, resumed) // the same state again, no pulse
		at(30, 0, paused)
		at(40, 0, quiet)
		at(50, 0, quiet) // unchanged
		at(60, 0, resumed)
		at(70, 2, lightsOff) // another slot has its own state
		at(80, 2, lightsOff)
		if err := pr.flush(); err != nil {
			t.Fatal(err)
		}
		return slices.Collect(strings.Lines(buf.String()))
	}
	if got, want := lines(false, false), []string{
		"      10.000    feedback slot=0 mode=2 pulse x2 70ms gap=100 color=#112233\n",
		"      30.000    feedback slot=0 mode=2 paused pulse x1 210ms gap=100 color=#FFAA00\n",
		"      40.000    feedback slot=0 mode=2 paused color=#112233\n",
		"      60.000    feedback slot=0 mode=2 color=#112233\n",
		"      70.000    feedback slot=2 mode=2 led=off\n",
	}; !slices.Equal(got, want) {
		t.Errorf("one slot:\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(want, ""))
	}
	if got := lines(false, true); len(got) != 5 || !strings.HasPrefix(got[0], "      10.000          feedback slot=0 mode=2 pulse") {
		t.Errorf("several slots: %q; want the lines past the slot cell", got)
	}
	if got := lines(true, false); len(got) != 0 {
		t.Errorf("JSON lines: %q; want none", got)
	}
}

// TestPrinterJSONSlot: with showSlots a JSON decision starts with its
// slot, then has the keys of MarshalJSON in their order; a send line has
// no slot.
func TestPrinterJSONSlot(t *testing.T) {
	d := eastDown(t)
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	clk := clock.NewFake()
	clk.Set(d.T)
	pr := testPrinter(t, &buf, clk, true, true)
	pr.showSlots()
	pr.decision(3, d)
	if _, err := pr.Send([]output.KeyEvent{{Key: keys.MustParse("Space").Key(), Scan: 0x39}}); err != nil {
		t.Fatal(err)
	}
	pr.decision(0, d)
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := []string{`{"slot":3,` + string(b[1:]), `{"t":512,"send":["+Space"]}`, `{"slot":0,` + string(b[1:])}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	var got, plain map[string]any
	if !json.Valid([]byte(lines[0])) || json.Unmarshal([]byte(lines[0]), &got) != nil || json.Unmarshal(b, &plain) != nil {
		t.Fatalf("%s is not a JSON object", lines[0])
	}
	if got["slot"] != 3.0 {
		t.Errorf("slot = %v, want 3", got["slot"])
	}
	delete(got, "slot")
	if !reflect.DeepEqual(got, plain) {
		t.Errorf("without its slot %v, want %v", got, plain)
	}
}

// TestPrinterSlotEvent: the device lines of live mode name the device and
// its slot.
func TestPrinterSlotEvent(t *testing.T) {
	var buf bytes.Buffer
	pr := testPrinter(t, &buf, clock.NewFake(), false, true)
	info := &input.DeviceInfo{ID: 3, Name: "Xbox Controller"}
	pr.slotEvent(0, input.Event{Device: 3, Kind: input.DeviceAdded, Info: info})
	pr.slotEvent(0, input.Event{Device: 3, Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	pr.decision(0, engine.Decision{Control: control.LT, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound})
	pr.slotEvent(0, input.Event{Device: 3, Kind: input.DeviceRemoved})
	pr.slotEvent(1, input.Event{Device: 4, Kind: input.DeviceAdded}) // no Info
	pr.slotEvent(1, input.Event{Device: 5, Kind: input.DeviceRemoved})
	want := "       0.000  device 3 \"Xbox Controller\" added to slot 0\n" +
		"       0.000  lt down (20000)     unbound\n" +
		"       0.000  device 3 \"Xbox Controller\" removed from slot 0: release all\n" +
		"       0.000  device 4 added to slot 1\n" +
		"       0.000  device 5 removed from slot 1: release all\n"
	if err := pr.flush(); err != nil || buf.String() != want {
		t.Errorf("flush() = %v, output\n%s\nwant\n%s", err, buf.String(), want)
	}
}

// TestPrinterTriggerPerSlot: a trigger edge shows the value of its own
// slot's last event, however the slots interleave.
func TestPrinterTriggerPerSlot(t *testing.T) {
	var buf bytes.Buffer
	pr := testPrinter(t, &buf, clock.NewFake(), false, true)
	ltDown := engine.Decision{Control: control.LT, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound}
	pr.slotEvent(0, input.Event{Device: 1, Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000})
	pr.slotEvent(1, input.Event{Device: 2, Kind: input.AxisMotion, Control: "lefttrigger", Value: 30000})
	pr.decision(0, ltDown)
	pr.slotEvent(1, input.Event{Device: 2, Kind: input.ButtonDown, Control: "south"})
	pr.decision(0, ltDown) // slot 1's button leaves slot 0's value
	pr.decision(1, ltDown) // slot 1's last event moved no axis
	want := "       0.000  lt down (20000)     unbound\n" +
		"       0.000  lt down (20000)     unbound\n" +
		"       0.000  lt down             unbound\n"
	if err := pr.flush(); err != nil || buf.String() != want {
		t.Errorf("flush() = %v, output\n%s\nwant\n%s", err, buf.String(), want)
	}
}

// TestPrinterConcurrent: in live mode T-output sends while a slot
// goroutine traces; every line comes out whole. -race checks the rest.
func TestPrinterConcurrent(t *testing.T) {
	const n = 200
	var buf bytes.Buffer
	pr := testPrinter(t, &buf, clock.Real(), false, true)
	z := output.KeyEvent{Key: keys.MustParse("Z").Key(), Scan: 0x2C}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range n {
			pr.Send([]output.KeyEvent{z}) //nolint:errcheck // it never fails
		}
	})
	wg.Go(func() {
		for i := range n {
			pr.decision(0, engine.Decision{T: clock.Real().Now(), Control: control.South, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound})
			pr.slotEvent(0, input.Event{Device: input.DeviceID(i + 1), Kind: input.DeviceAdded})
		}
	})
	wg.Wait()
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for line := range strings.Lines(buf.String()) {
		switch f := strings.Fields(line); {
		case len(f) == 3 && f[1] == "send" && f[2] == "+Z":
			counts["send"]++
		case len(f) == 4 && f[1] == "south" && f[3] == "unbound":
			counts["decision"]++
		case len(f) == 7 && f[1] == "device" && f[3] == "added":
			counts["device"]++
		default:
			t.Errorf("mangled line %q", line)
		}
	}
	if counts["send"] != n || counts["decision"] != n || counts["device"] != n {
		t.Errorf("lines %v, want %d of each", counts, n)
	}
}

// TestPrinterConcurrentSlots: in live mode with the slot column, the 4
// slots trace and note their events, each on a goroutine of its own, while
// T-output sends and the profile is reloaded; every line comes out whole,
// each trigger row shows its own slot's value, and each generation prints
// one reload marker. As Supervisor.Reload is serialized, every slot notes
// a generation before any notes the next. -race checks the rest.
func TestPrinterConcurrentSlots(t *testing.T) {
	const (
		n     = 100
		slots = 4
		gens  = 20
	)
	// value is the trigger value of slot s.
	value := func(s int) int16 { return int16(1000 * (s + 1)) }
	ltDown := engine.Decision{Control: control.LT, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound}
	southDown := engine.Decision{Control: control.South, Edge: engine.EdgeDown, Reason: engine.ReasonUnbound}
	for _, tc := range []struct {
		name  string
		jsonl bool
	}{{"text", false}, {"jsonl", true}} {
		jsonl := tc.jsonl
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			pr := testPrinter(t, &buf, clock.Real(), jsonl, true)
			pr.showSlots()
			z := output.KeyEvent{Key: keys.MustParse("Z").Key(), Scan: 0x2C}
			var wg sync.WaitGroup
			wg.Go(func() {
				for range n {
					pr.Send([]output.KeyEvent{z}) //nolint:errcheck // it never fails
				}
			})
			for s := range slots {
				wg.Go(func() {
					for i := range n {
						d := southDown
						d.T = clock.Real().Now()
						pr.decision(uint8(s), d)
						pr.slotEvent(uint8(s), input.Event{Device: input.DeviceID(s*n + i + 1), Kind: input.DeviceAdded})
						pr.slotEvent(uint8(s), input.Event{Device: input.DeviceID(s*n + i + 1), Kind: input.AxisMotion, Control: "lefttrigger", Value: value(s)})
						d = ltDown
						d.T = clock.Real().Now()
						pr.decision(uint8(s), d)
					}
				})
			}
			wg.Go(func() {
				for g := range gens {
					var each sync.WaitGroup
					for s := range slots {
						each.Go(func() { pr.reload(uint8(s), uint32(g+2)) })
					}
					each.Wait()
				}
			})
			wg.Wait()
			if err := pr.flush(); err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for line := range strings.Lines(buf.String()) {
				if jsonl {
					counts[jsonLine(t, line)]++
				} else {
					counts[textLine(t, line, n, value)]++
				}
			}
			want := map[string]int{"send": n, "marker": gens, "device": slots * n}
			if jsonl {
				want = map[string]int{"send": n}
			}
			for s := range slots {
				for _, c := range []string{"south", "lt"} {
					want[strconv.Itoa(s)+" "+c] = n
				}
			}
			if !reflect.DeepEqual(counts, want) {
				t.Errorf("lines %v, want %v", counts, want)
			}
		})
	}
}

// textLine returns the kind of line, a row of TestPrinterConcurrentSlots
// with the slot column: "send", "marker", "device", or the slot and the
// control of a decision, such as "2 lt"; "mangled" for anything else.
func textLine(t *testing.T, line string, n int, value func(int) int16) string {
	t.Helper()
	f := strings.Fields(line)
	switch {
	case len(f) == 3 && f[1] == "send" && f[2] == "+Z":
		return "send"
	case slices.Equal(f[1:], []string{"profile", "reloaded:", "release", "all"}):
		return "marker"
	case len(f) == 7 && f[1] == "device" && f[3] == "added" && f[4] == "to" && f[5] == "slot":
		id, err1 := strconv.Atoi(f[2])
		s, err2 := strconv.Atoi(f[6])
		if err1 == nil && err2 == nil && (id-1)/n == s {
			return "device"
		}
	case len(f) == 5 && f[2] == "south" && f[3] == "down" && f[4] == "unbound":
		return f[1] + " south"
	case len(f) == 6 && f[2] == "lt" && f[3] == "down" && f[5] == "unbound":
		if s, err := strconv.Atoi(f[1]); err == nil && f[4] == "("+strconv.Itoa(int(value(s)))+")" {
			return f[1] + " lt"
		}
	}
	t.Errorf("mangled line %q", line)
	return "mangled"
}

// jsonLine returns the kind of line, a JSON line of
// TestPrinterConcurrentSlots: "send", or the slot and the control of a
// decision, such as "2 lt", which starts with its slot; "mangled" for
// anything else.
func jsonLine(t *testing.T, line string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Errorf("mangled line %q: %v", line, err)
		return "mangled"
	}
	if _, ok := m["send"]; ok {
		if _, slot := m["slot"]; !slot && strings.HasPrefix(line, `{"t":`) {
			return "send"
		}
	} else if s, ok := m["slot"].(float64); ok && strings.HasPrefix(line, `{"slot":`+strconv.Itoa(int(s))+`,"t":`) {
		if c, ok := m["control"].(string); ok {
			return strconv.Itoa(int(s)) + " " + c
		}
	}
	t.Errorf("mangled line %q", line)
	return "mangled"
}
