package output_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// injectRow is one row of TestInjectLatency: n0 Sends at scale 1, pressing
// and releasing key in turn, with gap of idle before each.
type injectRow struct {
	name   string
	gap    time.Duration
	n0     int
	key    output.Stroke
	events string // events per Send, for the report
}

// injectSend is one timed Send: its events, the stamps around the call
// and the hook stamps of its first and last event.
type injectSend struct {
	ev          []output.KeyEvent
	t0, t1      latency.Stamp
	first, last latency.Stamp
}

// injectMargin is the time TestInjectLatency keeps free of its estimate
// before the test deadline, up front and before each later row: a test
// that times out panics without running its cleanups, which would leave
// keys down (CLAUDE.md invariant 1).
const (
	injectMarginStart = time.Minute
	injectMarginRow   = 30 * time.Second
)

// TestInjectLatency times SendInput on its own (ENG-11): one call per
// press or release of F15 (1 event) or Ctrl+Shift+F16 (3 events), from a
// goroutine locked to its thread, in a test process with no Loop and no
// SDL. This process's hook swallows every event (CLAUDE.md invariant 6),
// and each Send waits for the hook to have seen its events (WaitOwn)
// before the next one goes out, so that the calls do not queue behind
// each other. It reports the time of the call, t_ret − t_send, and from
// the hook's stamps send→hook (the first event's stamp − t_send),
// hook→ret (t_ret − the last event's stamp) and how often the hook ran
// before SendInput returned. It is informational: the report has no gate.
//
// It runs with PADMATE_BENCH=1 and PADMATE_TEST_INJECT=1 (task bench), not
// under -short, and writes the report to the test log and, when
// PADMATE_BENCH_DIR is set, to sendinput.md there. PADMATE_BENCH_SCALE
// scales the sample counts; it takes about 20 s at scale 1 on the dev machine.
func TestInjectLatency(t *testing.T) {
	if os.Getenv(latency.EnvBench) != "1" {
		t.Skipf("a latency benchmark; set %s=1 (task bench)", latency.EnvBench)
	}
	injecttest.Require(t)
	if testing.Short() {
		t.Skip("a latency benchmark; not with -short")
	}
	scale, err := latency.ParseScale(os.Getenv(latency.EnvScale))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := latency.ParseDir(os.Getenv(latency.EnvDir))
	if err != nil {
		t.Fatal(err)
	}
	stroke := func(spec string) output.Stroke {
		t.Helper()
		st, err := output.ChordStroke(keys.MustParse(spec))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	f15, csf16 := stroke("F15"), stroke("Ctrl+Shift+F16")
	rows := []injectRow{
		{name: "M1", n0: 1000, key: f15, events: "1"},
		{name: "M2", n0: 1000, key: csf16, events: "3"},
		{name: "M3", gap: 50 * time.Millisecond, n0: 200, key: f15, events: "1"},
		{name: "M4", gap: 50 * time.Millisecond, n0: 200, key: csf16, events: "3"},
	}
	ns := make([]int, len(rows))
	total := 0
	for i, r := range rows {
		ns[i] = latency.Scaled(r.n0, scale)
		total += ns[i]
	}
	// estimate is the time rows[from:] take: the gaps, and 1 ms for each
	// Send and its fence, which is generous.
	estimate := func(from int) time.Duration {
		var d time.Duration
		for i := from; i < len(rows); i++ {
			d += time.Duration(ns[i]) * (rows[i].gap + time.Millisecond)
		}
		return d
	}
	checkDeadline(t, estimate(0)+injectMarginStart)

	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	nonce := base | 0x4c
	rec.Canary(t, nonce)
	want := rec.OwnCount()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := output.SendInput{Nonce: nonce}

	// held is the presses injected and not released yet, in order. The
	// cleanup, registered before the first Send so that it runs before the
	// Recorder's Close, releases them after a failure, and then checks
	// that every owned key that went down went up (CLAUDE.md invariant 1).
	var held []output.KeyEvent
	t.Cleanup(func() {
		if len(held) > 0 {
			ups := make([]output.KeyEvent, 0, len(held))
			for i := len(held) - 1; i >= 0; i-- {
				e := held[i]
				e.Up = true
				ups = append(ups, e)
			}
			n, err := s.Send(ups)
			if n < len(ups) {
				m, err2 := s.Send(ups[n:])
				n += m
				if n < len(ups) {
					t.Errorf("releasing the held keys: SendInput injected %d of %d events (%v, then %v): keys may be held", n, len(ups), err, err2)
				}
			}
			want += n
			held = nil
		}
		if err := rec.WaitOwn(want, injecttest.CanaryTimeout); err != nil {
			t.Errorf("waiting for the releases: %v", err)
			return
		}
		checkBalanced(t, ownKeys(rec.Own(), nonce))
	})

	sends := make([]injectSend, 0, total)
	result := make([]latency.Row, 0, len(rows))
	for i, r := range rows {
		if i > 0 {
			checkDeadline(t, estimate(i)+injectMarginRow)
		}
		gc0 := gcCycles()
		for j := range ns[i] {
			ev := r.key.Down
			if j%2 == 1 {
				ev = r.key.Up
			}
			if r.gap > 0 {
				time.Sleep(r.gap)
			}
			t0 := latency.Now()
			n, err := s.Send(ev)
			t1 := latency.Now()
			// Counted before the check, so that after a partial Send the
			// cleanup releases its presses and waits for its events too.
			n = min(max(n, 0), len(ev))
			held = track(held, ev[:n])
			want += n
			if n != len(ev) || err != nil {
				t.Fatalf("%s, Send %d: SendInput injected %d of %d events: %v", r.name, j, n, len(ev), err)
			}
			if err := rec.WaitOwn(want, injecttest.CanaryTimeout); err != nil {
				t.Fatalf("%s, Send %d: %v", r.name, j, err)
			}
			sends = append(sends, injectSend{ev: ev, t0: t0, t1: t1})
		}
		gcs := gcCycles() - gc0
		result = append(result, latency.Row{Name: r.name, Gap: r.gap, Sender: "SendInput", Events: r.events, GCs: gcs})
	}
	if len(held) > 0 {
		t.Fatalf("keys held after the rows: %v", held)
	}

	matchHookStamps(t, sends, ownKeys(rec.Own(), nonce))
	from := 0
	for i := range result {
		rs := sends[from : from+ns[i]]
		from += ns[i]
		lat := make([]time.Duration, len(rs))
		toHook := make([]time.Duration, len(rs))
		fromHook := make([]time.Duration, len(rs))
		hookFirst := 0
		for j, sd := range rs {
			lat[j] = sd.t1.Sub(sd.t0)
			toHook[j] = sd.first.Sub(sd.t0)
			fromHook[j] = sd.t1.Sub(sd.last)
			if sd.last < sd.t1 {
				hookFirst++
			}
		}
		result[i].Total = latency.Summarize(lat)
		result[i].Stages = []latency.Stage{
			{Name: "send→hook", Summary: latency.Summarize(toHook)},
			{Name: "hook→ret", Summary: latency.Summarize(fromHook)},
		}
		result[i].HookFirst = hookFirst
	}

	env := latency.ReadEnvironment()
	env.Extra = []string{
		"SDL not loaded in this process",
		"test process; nothing here calls timeBeginPeriod",
		fmt.Sprintf("scale %g (%s)", scale, latency.EnvScale),
	}
	rep := latency.Report{
		Title:      "SendInput 单独（ENG-11）",
		Env:        env,
		Gate:       latency.GateOff,
		GateReason: "info only",
		Rows:       result,
		Notes: []string{
			"Total is t_ret − t_send of one SendInput call from a goroutine locked to its thread, in a test process whose own low-level hook swallows every event; each Send waits until the hook has seen its events before the next one.",
			"send→hook is the hook stamp of the Send's first event − t_send; hook→ret is t_ret − the stamp of its last event, negative when the hook ran after SendInput returned.",
			"M3 and M4 idle 50 ms before each Send, so that the threads park.",
		},
	}
	var md strings.Builder
	if err := rep.WriteMarkdown(&md); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + md.String())
	if dir != "" {
		path := filepath.Join(dir, "sendinput.md")
		if err := os.WriteFile(path, []byte(md.String()), 0o644); err != nil {
			t.Errorf("writing the report: %v", err)
		} else {
			t.Logf("wrote %s", path)
		}
	}
}

// checkDeadline stops t, before it injects anything more, unless need fits
// before the test deadline.
func checkDeadline(t *testing.T, need time.Duration) {
	t.Helper()
	if dl, ok := t.Deadline(); ok {
		if left := time.Until(dl); left < need {
			t.Fatalf("%v left before the test deadline, and the rest needs up to %v; raise -timeout or lower %s",
				left.Round(time.Second), need.Round(time.Second), latency.EnvScale)
		}
	}
}

// gcCycles returns the GC cycles so far.
func gcCycles() uint64 {
	s := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// track returns held after the events sent: a press is added, and a
// release removes the press of its key.
func track(held, sent []output.KeyEvent) []output.KeyEvent {
	for _, e := range sent {
		if !e.Up {
			held = append(held, e)
			continue
		}
		for i := len(held) - 1; i >= 0; i-- {
			if held[i].ID() == e.ID() {
				held = append(held[:i], held[i+1:]...)
				break
			}
		}
	}
	return held
}

// ownKeys returns the events of own with nonce that are not its canary.
func ownKeys(own []winhook.Event, nonce uint32) []winhook.Event {
	var ks []winhook.Event
	for _, e := range own {
		if output.Nonce(e.Extra) == nonce && !injecttest.IsCanary(e, nonce) {
			ks = append(ks, e)
		}
	}
	return ks
}

// checkBalanced fails t unless every VK of events that went down went up,
// in order: a key goes down at most once before it goes up, and the keys
// go up in the reverse order of going down.
func checkBalanced(t *testing.T, events []winhook.Event) {
	t.Helper()
	var down []uint32
	for i, e := range events {
		if !e.Up() {
			if slices.Contains(down, e.VK) {
				t.Errorf("event %d: vk 0x%02X went down while down (CLAUDE.md invariant 1)", i, e.VK)
			}
			down = append(down, e.VK)
			continue
		}
		if len(down) == 0 || down[len(down)-1] != e.VK {
			t.Errorf("event %d: vk 0x%02X went up, want the last key down of %s (CLAUDE.md invariant 1)", i, e.VK, vkList(down))
			continue
		}
		down = down[:len(down)-1]
	}
	if len(down) > 0 {
		t.Errorf("keys still down after the test: %s (CLAUDE.md invariant 1)", vkList(down))
	}
}

func vkList(vks []uint32) string {
	if len(vks) == 0 {
		return "none"
	}
	parts := make([]string, len(vks))
	for i, v := range vks {
		parts[i] = fmt.Sprintf("0x%02X", v)
	}
	return strings.Join(parts, " ")
}

// matchHookStamps gives each Send of sends its events of hooked, which are
// the owned non-canary events in the order the hook saw them, and sets its
// first and last hook stamps. It stops t unless every event matches the
// one sent, press or release and scan code, and the counts agree.
func matchHookStamps(t *testing.T, sends []injectSend, hooked []winhook.Event) {
	t.Helper()
	sent := 0
	for _, sd := range sends {
		sent += len(sd.ev)
	}
	if len(hooked) != sent {
		t.Fatalf("the hook saw %d events of the Sends, want %d", len(hooked), sent)
	}
	k := 0
	for i := range sends {
		sd := &sends[i]
		for j, ev := range sd.ev {
			e := hooked[k]
			if e.Up() != ev.Up || (!ev.ByVK && e.Scan != uint32(ev.Scan)) {
				t.Fatalf("Send %d, event %d: the hook saw sc=0x%02X up=%v, want sc=0x%02X up=%v", i, j, e.Scan, e.Up(), ev.Scan, ev.Up)
			}
			if j == 0 {
				sd.first = e.Stamp
			}
			sd.last = e.Stamp
			k++
		}
	}
}

// BenchmarkLatencyResolve measures Resolver.Resolve on the system's
// layouts (ENG-11): F13 has no character, so it is ChordStroke; Z asks
// for the foreground window's layout, whose tables the Resolver caches
// after the first Resolve. A warning means that Resolve fell back to the
// US key instead, which would time the wrong path, so it fails the
// benchmark. It only reads: it neither loads nor activates a layout, and
// it sends nothing.
func BenchmarkLatencyResolve(b *testing.B) {
	for _, spec := range []string{"F13", "Z"} {
		b.Run(spec, func(b *testing.B) {
			r, warns := newResolver(output.SystemLayouts())
			c := keys.MustParse(spec)
			// Untimed: the first Resolve reads the layout's tables, and the
			// first Now finds QueryPerformanceCounter, which allocates.
			_ = latency.Now()
			st, err := r.Resolve(c)
			if err != nil {
				b.Fatal(err)
			}
			if w := warns.take(); len(w) > 0 {
				b.Fatalf("Resolve(%s) fell back to the US key: %+v", spec, w)
			}
			if len(st.Down) == 0 || len(st.Down) != len(st.Up) {
				b.Fatalf("Resolve(%s) = %d presses and %d releases, want as many of each", spec, len(st.Down), len(st.Up))
			}
			lat := make([]time.Duration, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				t0 := latency.Now()
				_, err := r.Resolve(c)
				lat[i] = latency.Now().Sub(t0)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if w := warns.take(); len(w) > 0 {
				b.Fatalf("Resolve(%s) fell back to the US key: %+v", spec, w)
			}
			reportLatency(b, lat)
		})
	}
}
