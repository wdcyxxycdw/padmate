package latency

import (
	"bytes"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/report.golden.md from the current output")

func TestWriteMarkdown(t *testing.T) {
	env := Environment{
		Date: "2026-09-25T01:02:03Z", GoVersion: "go1.27.0", OS: "windows 10.0.26200",
		NumCPU: 16, GOMAXPROCS: 16, Freq: 10_000_000,
		Extra: []string{"SDL 3.4.16", "SDL_TIMER_RESOLUTION: unset", "Scale: 1"},
	}
	us := func(x float64) time.Duration { return time.Duration(math.Round(x * float64(time.Microsecond))) }
	sum := func(n int, p50, p90, p99, p999, top float64) Summary {
		return Summary{N: n, P50: us(p50), P90: us(p90), P99: us(p99), P999: us(p999), Max: us(top)}
	}
	stages := []Stage{
		{"sdl→slot", sum(2000, 12.34, 20, 35.06, 80, 120)},
		{"slot→send", sum(2000, 3.21, 4, 6.5, 9, 15)},
		{"send→hook", sum(2000, 40, 50, 70, 900, 1450.5)},
	}
	evaluated := Report{
		Title:       "端到端延迟（ENG-11）",
		Env:         env,
		Calibration: "width 0.1 µs; SDL 10000000 Hz = QPC 10000000 Hz; start and end agree",
		Gate:        GateEvaluated,
		Rows: []Row{
			{Name: "A", Gap: 5 * time.Millisecond, Sender: "SendInput", Events: "1+3", Total: sum(2000, 110.04, 180, 301.25, 999.96, 1234.5),
				Stages: stages, HookFirst: 1998, GCs: 3, Target: time.Millisecond, MinN: 2000},
			{Name: "B", Gap: 250 * time.Millisecond, Sender: "SendInput", Events: "1", Total: sum(2000, 900, 1500, 2345.678, 3000, 12000),
				HookFirst: 0, Target: 2 * time.Millisecond, MinN: 2000, Note: "C-state exit"},
			// p99 just below the Target: shown below it, and passes.
			{Name: "C", Gap: 50 * time.Millisecond, Sender: "SendInput", Events: "1+3", Total: sum(400, 200, 400, 1499.6, 1500, 1700),
				HookFirst: 400, Target: 1500 * time.Microsecond, MinN: 400},
			{Name: "H", Gap: time.Second, Sender: "SendInput", Events: "3", Total: sum(40, 0.04, 0.05, 999.94, 1000, 1000),
				HookFirst: -1, Note: "four pads at once; info only"},
		},
		Split: []Row{
			{Name: "A1", Gap: 5 * time.Millisecond, Sender: "SendInput", Events: "1", Total: sum(1000, 100, 150, 250, 400, 500), HookFirst: -1},
			{Name: "A3", Gap: 5 * time.Millisecond, Sender: "SendInput", Events: "3", Total: sum(1000, 120, 190, 320, 900, 1234.5), HookFirst: -1},
		},
		Notes: []string{"L is t_ret − t_sdl."},
	}
	skipped := Report{
		Title:       "端到端延迟（ENG-11）",
		Env:         env,
		Calibration: "width 0.1 µs",
		Gate:        GateSkipped,
		GateReason:  "no injection (PADMATE_TEST_INJECT unset)",
		Rows: []Row{
			{Name: "A", Gap: 5 * time.Millisecond, Sender: "no-op", Events: "1+3", Total: sum(40, 10, 20, 30, 40, 50),
				Stages: stages[:2], HookFirst: -1, GCs: 0, Target: time.Millisecond, MinN: 2000},
			{Name: "Z", Gap: 0, Sender: "no-op", Events: "1", HookFirst: -1, Target: time.Millisecond, MinN: 2000},
		},
		Notes: []string{"No SendInput: the rows time the no-op sender."},
	}
	off := Report{
		Title:      "SendInput 单独（ENG-11）",
		Env:        Environment{Date: "2026-09-25T01:02:03Z", GoVersion: "go1.27.0", OS: "linux", NumCPU: 4, GOMAXPROCS: 2, Freq: 1_000_000_000},
		Gate:       GateOff,
		GateReason: "info only",
		Rows: []Row{
			{Name: "S1", Sender: "SendInput", Events: "1", Total: sum(5000, 20, 30, 45.5, 80, 2000), HookFirst: -1},
		},
	}

	var buf bytes.Buffer
	for i, r := range []Report{evaluated, skipped, off} {
		if i > 0 {
			buf.WriteString("\n---\n\n")
		}
		if err := r.WriteMarkdown(&buf); err != nil {
			t.Fatal(err)
		}
	}
	golden := filepath.Join("testdata", "report.golden.md")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/latency -run TestWriteMarkdown -update", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("output differs from %s (rerun with -update and review the diff):\n--- got\n%s--- want\n%s", golden, buf.Bytes(), want)
	}

	// The first write error is returned, wherever it happens.
	var one bytes.Buffer
	if err := evaluated.WriteMarkdown(&one); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < one.Len(); n += 97 {
		if err := evaluated.WriteMarkdown(&limitWriter{n: n}); !errors.Is(err, errShort) {
			t.Fatalf("WriteMarkdown to a writer that fails after %d bytes: %v", n, err)
		}
	}
	if err := evaluated.WriteMarkdown(&limitWriter{n: one.Len()}); err != nil {
		t.Fatalf("WriteMarkdown of %d bytes to a writer that takes them: %v", one.Len(), err)
	}
}

var errShort = errors.New("short write")

// limitWriter accepts n bytes, then fails.
type limitWriter struct{ n int }

func (w *limitWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		k := w.n
		w.n = 0
		return k, errShort
	}
	w.n -= len(p)
	return len(p), nil
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0 µs"},
		{99, "0.0 µs"},
		{100, "0.1 µs"},
		{-99, "0.0 µs"},
		{-100, "-0.1 µs"},
		{123_456, "123.4 µs"},
		{-123_456, "-123.4 µs"},
		{999_999, "999.9 µs"},
		{time.Millisecond, "1.000 ms"},
		{1_234_567, "1.234 ms"},
		{1_499_999, "1.499 ms"}, // passes a 1.5 ms Target
		{1_500_000, "1.500 ms"}, // fails it
		{-1_234_567, "-1.234 ms"},
		{12_000_000, "12.000 ms"},
		{2 * time.Second, "2000.000 ms"},
		{math.MaxInt64, "9223372036854.775 ms"},
		{math.MinInt64, "-9223372036854.775 ms"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.d); got != c.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", int64(c.d), got, c.want)
		}
	}
}

func TestGateString(t *testing.T) {
	for g, want := range map[Gate]string{GateOff: "off", GateSkipped: "skipped", GateEvaluated: "evaluated", 7: "Gate(7)"} {
		if got := g.String(); got != want {
			t.Errorf("Gate(%d).String() = %q, want %q", int(g), got, want)
		}
	}
}
