package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// benchPads are the virtual gamepads of TestLatencyLive: the first for
// every row, all four for row H. Their names differ in the first 9 bytes,
// from each other and from liveTestPad, so that each gets a GUID and a
// name of its own (docs/PITFALLS.md "DeviceKey 的依据"), and --device bench selects them only: not
// the idle "XInput Controller #1" (docs/hardware-matrix.md), nor a key's GUID, which is hex.
var benchPads = [benchPadsMax]string{"lat1bench pad", "lat2bench pad", "lat3bench pad", "lat4bench pad"}

// benchControls are the buttons benchProfile binds to benchKeys, each to
// hold its keys. Rows A–E and the warm-up press east (1 event per Send)
// and south (3 events) of the first pad; row H presses one on each pad,
// so that no two slots hold a key and each edge is still one Send. The
// keys have no character, so resolving them reads no layout, and neither
// Alt nor Win, so no menu opens and MenuMask inserts nothing (OUT-08); a
// key that got past the hook would do nothing in almost every program.
var (
	benchControls = [benchPadsMax]string{"east", "south", "west", "north"}
	benchKeys     = [benchPadsMax]string{"F13", "Ctrl+Shift+F14", "F17", "F18"}
)

// benchProfile returns the profile of TestLatencyLive.
func benchProfile() string {
	var b strings.Builder
	b.WriteString(`{"schemaVersion":0,"name":"latency","timing":{"tapPressMs":0},"layers":{"base":{`)
	for i, c := range benchControls {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%q:{"press":{"type":"keys","keys":%q,"mode":"hold"}}`, c, benchKeys[i])
	}
	b.WriteString(`}}}`)
	return b.String()
}

// benchMacroFile and benchMacro are the macro of benchMacroProfile. No
// row presses its control; run, it would tap F19, a key without a
// character, as benchKeys are.
const (
	benchMacroFile = "bench.ts"
	benchMacro     = `import {key} from "padmate"; export default function () { key.tap("F19"); }` + "\n"
)

// benchMacroProfile returns the profile of row M: benchProfile's bindings
// and a macro on leftshoulder (SCR-50), so each slot has a runtime that
// only runs macros.
func benchMacroProfile() string {
	return strings.Replace(benchProfile(), `"base":{`, `"base":{"leftshoulder":{"press":{"type":"macro","file":"`+benchMacroFile+`"}},`, 1)
}

// slotStamp is one event a slot handled, as onEvent saw it, with t_slot.
type slotStamp struct {
	t       latency.Stamp
	time    uint64 // Event.Time: SDL's stamp, or 0 for an event of Resync
	device  input.DeviceID
	kind    input.EventKind
	control string
}

// slotStamps are the events of one slot. The slot's goroutine is the one
// writer (a restarted slot starts after the old goroutine ended); it
// publishes each record with n.Store. The driver reads up to n.Load and
// never writes.
type slotStamps struct {
	n        atomic.Int64
	at       []slotStamp // preallocated
	overflow atomic.Int64
}

// stampTable is the probe of t_slot, supervisor.Config.OnEvent through
// liveConfig.onEvent: one QPC read and one store per event, with no
// allocation, channel or wake-up (ARCHITECTURE §13).
type stampTable struct {
	slots   [supervisor.MaxSlots]slotStamps
	badSlot atomic.Int64
}

func newStampTable(size int) *stampTable {
	st := &stampTable{}
	for i := range st.slots {
		st.slots[i].at = make([]slotStamp, size)
	}
	return st
}

// onEvent records e for slot. It runs on the slot's goroutine, in
// slot.Handle before engine.Step, so an edge's record exists before the
// Send it causes.
func (st *stampTable) onEvent(slot uint8, e input.Event) {
	now := latency.Now()
	if int(slot) >= len(st.slots) {
		st.badSlot.Add(1)
		return
	}
	s := &st.slots[slot]
	i := s.n.Load()
	if i >= int64(len(s.at)) {
		s.overflow.Add(1)
		return
	}
	s.at[i] = slotStamp{t: now, time: e.Time, device: e.Device, kind: e.Kind, control: e.Control}
	s.n.Store(i + 1)
}

// sendRec is one Send, as the stampSender saw it.
type sendRec struct {
	start, ret   latency.Stamp // t_send, t_ret
	events, sent int
	evs          [3]output.KeyEvent // the first three events, to match a Send to its edge
	noop         bool
	err          error
}

// stampSender is the Loop's Sender in TestLatencyLive, which newInject
// gives live mode: under the ledger and MenuMask, it stamps t_send and
// t_ret around the inner Sender, records the Send and wakes the driver,
// after t_ret. With injection the inner Sender is newInjectSender's
// SendInput, and the no-op one for row E. Without injection it is always
// the no-op one: Send then refuses a Sender that injects (CLAUDE.md
// invariant 6), and TestLatencyLive fails.
type stampSender struct {
	inject    bool
	sendInput output.Sender // newInjectSender's, with inject; else nil
	// inner and noop belong to T-output: Send and the Loop.Do of
	// setNoop use them.
	inner output.Sender
	noop  bool
	recs  []sendRec // preallocated
	n     atomic.Int64
	extra atomic.Int64 // Sends that did not fit recs
	// unsafe counts the Sends refused because inner was set while
	// no-op, or set at all without injection.
	unsafe atomic.Int64
	done   chan struct{} // cap 1
}

var errBenchUnsafe = errors.New("latency bench: refused a Sender that injects while no-op or without " + injecttest.EnvVar + "=1")

func (s *stampSender) Send(evs []output.KeyEvent) (int, error) {
	start := latency.Now()
	var n int
	var err error
	switch {
	case s.noop && s.inner != nil, !s.inject && (s.inner != nil || !s.noop):
		s.unsafe.Add(1)
		err = errBenchUnsafe
	case s.noop:
		n = len(evs)
	case s.inner == nil:
		err = errors.New("latency bench: no Sender")
	default:
		n, err = s.inner.Send(evs)
	}
	ret := latency.Now()
	if i := s.n.Load(); i < int64(len(s.recs)) {
		r := &s.recs[i]
		*r = sendRec{start: start, ret: ret, events: len(evs), sent: n, noop: s.noop, err: err}
		copy(r.evs[:], evs)
		s.n.Store(i + 1)
	} else {
		s.extra.Add(1)
	}
	select {
	case s.done <- struct{}{}:
	default:
	}
	return n, err
}

// setNoop switches to the no-op Sender or back to SendInput. It runs on
// T-output, in a Loop.Do, while no key is held. Without injection the
// Sender stays the no-op one.
func (s *stampSender) setNoop(noop bool) {
	if !s.inject {
		return
	}
	s.noop, s.inner = noop, s.sendInput
	if noop {
		s.inner = nil
	}
}

// benchEdge is one edge: a button of a pad pressed or released.
type benchEdge struct {
	pad     int
	control string
	down    bool
}

// benchSample is one measured edge.
type benchSample struct {
	tc, tSlot, tSend, tRet latency.Stamp
	ns                     uint64 // Event.Time
	events                 int
	send                   int64 // the index of its Send in stampSender.recs
}

// benchDone is a row that finished: its samples are samples[from:to].
type benchDone struct {
	row      benchRow
	from, to int
	gcs      uint64
	note     string
}

// bench is the state of TestLatencyLive's driver, the test goroutine.
type bench struct {
	t       *testing.T
	tl      *testLive
	st      *stampTable
	ss      *stampSender
	rec     *injecttest.Recorder // nil without injection
	inject  bool
	mode    latency.GateMode
	want    int // WaitOwn's running total
	pads    []*input.Virtual
	press   map[string][]output.KeyEvent // by control: the presses of its keys
	cursor  [supervisor.MaxSlots]int64   // read cursors of st
	sends   int64                        // the stampSender's records consumed
	timer   *time.Timer
	edgeBuf [benchPadsMax]benchEdge
	stats   output.ArbiterStats // the Arbiter's, before the warm-up
	idled   bool

	samples  []benchSample
	done     []benchDone
	finished []latency.Row // the rows done, with the start calibration, for a partial report
	c0       latency.SDLClock
	envHead  []string // Env.Extra before the devices
	envTail  []string // and after them
	env      latency.Environment
}

// TestLatencyLive measures the end-to-end latency of live mode (ENG-11):
// virtual gamepads drive padmated's live mode in this process, from SDL
// through T-input, the router, the slots and the engine to T-output, the
// Arbiter and the Sender. L = t_ret − t_sdl runs from SDL's timestamp of
// an edge, mapped onto QPC by calibrating SDL's clock, to the return of
// the Send that carries its keys. The rows of benchPlan press and release
// hold bindings at gaps from 5 ms to 1 s; rows A–C have the gate p99 <
// benchTarget.
//
// With PADMATE_TEST_INJECT=1 the Sender is SendInput with the test's
// process id as the nonce, and a low-level hook of the test swallows every
// event (CLAUDE.md invariant 6): a canary proves it live before the first
// key, each edge waits until it has seen the edge's events (WaitOwn)
// before the next one goes out, and the exit path, then a canary fence,
// come before the hook goes. Without it every row uses the no-op Sender,
// nothing is injected and no hook is installed, and the gate is not
// evaluated.
//
// It runs with PADMATE_BENCH=1 (task bench), not under -short, and needs
// SDL. PADMATE_BENCH_SCALE scales the sample counts, PADMATE_BENCH_GATE
// sets the gate mode, and the report goes to the test log and, with
// PADMATE_BENCH_DIR, to latency.md there. Measurement errors fail it and
// are never reported as a latency.
func TestLatencyLive(t *testing.T) {
	if os.Getenv(latency.EnvBench) != "1" {
		t.Skipf("a latency benchmark; set %s=1 (task bench)", latency.EnvBench)
	}
	if testing.Short() {
		t.Skip("a latency benchmark; not with -short")
	}
	lib := inputtest.Library(t)
	inject := os.Getenv(injecttest.EnvVar) == "1"
	scale, err := latency.ParseScale(os.Getenv(latency.EnvScale))
	if err != nil {
		t.Fatal(err)
	}
	mode, err := latency.ParseGateMode(os.Getenv(latency.EnvGate))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := latency.ParseDir(os.Getenv(latency.EnvDir))
	if err != nil {
		t.Fatal(err)
	}
	if supervisor.MaxSlots < benchPadsMax {
		t.Fatalf("supervisor.MaxSlots = %d, row H needs %d", supervisor.MaxSlots, benchPadsMax)
	}
	rows, estimate, err := benchPlan(scale, true)
	if err != nil {
		t.Fatal(err)
	}
	benchDeadline(t, estimate, 2*time.Minute)

	path := filepath.Join(t.TempDir(), "latency.json")
	if err := os.WriteFile(path, []byte(benchProfile()), 0o644); err != nil {
		t.Fatal(err)
	}
	macros := filepath.Join(filepath.Dir(path), "macros")
	if err := os.Mkdir(macros, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macros, benchMacroFile), []byte(benchMacro), 0o644); err != nil {
		t.Fatal(err)
	}
	edges := benchWarmup + benchWarmupSteps*benchPadsMax
	for _, r := range rows {
		edges += r.n
	}
	b := &bench{
		t: t, inject: inject, mode: mode,
		st:      newStampTable(edges + 1024),
		timer:   time.NewTimer(time.Hour),
		samples: make([]benchSample, 0, edges),
	}
	b.timer.Stop()
	ss := &stampSender{inject: inject, noop: !inject, recs: make([]sendRec, edges+1024), done: make(chan struct{}, 1)}
	b.ss = ss
	// Only with injection does newInject construct SendInput.
	newInject := func() (output.Sender, error) {
		if inject {
			in, err := newInjectSender()
			if err != nil {
				return nil, err
			}
			ss.sendInput, ss.inner = in, in
		}
		return ss, nil
	}

	// Cleanups run last in first, as in TestLiveInjectVirtual: that of
	// tl.start stops live mode (its exit path releases every key), then
	// stopFenced proves with a canary that the hook swallowed everything
	// before it, then the Recorder's Close fails the test for any event
	// the hook let through or dropped.
	pid := uint32(os.Getpid())
	if inject {
		b.rec = injecttest.StartFunc(t, func(n uint32) bool { return n == pid })
		b.rec.Canary(t, pid)
		b.want = b.rec.OwnCount()
	}
	t.Cleanup(func() {
		if n := ss.unsafe.Load(); n > 0 {
			t.Errorf("the stampSender refused %d Sends: %v", n, errBenchUnsafe)
		}
	})
	var running *testLive // set once live mode has started
	stopped := false
	t.Cleanup(func() {
		if inject && !stopped {
			stopFenced(t, b.rec, pid, running)
		}
	})

	tl := &testLive{}
	b.tl = tl
	cfg := testConfig(t, tl, lib, newInject, "--profile", path, "--device", "bench", "--inject", "--watch=false")
	cfg.log = logging.New(tl.log, slog.LevelInfo) // as in production: debug lines would load the hot path
	cfg.onEvent = b.st.onEvent
	// As in production, T-output writes the held-key log before each
	// press (OUT-14), in slot->send; in a TempDir, so the user's log is
	// untouched, and with nothing reported down, so the sweep sends nothing.
	cfg.heldFile = filepath.Join(t.TempDir(), "held.bin")
	cfg.keyDown = func(output.KeyEvent) bool { return false }
	tl.start(t, cfg)
	running = tl
	if !inject && ss.inner != nil {
		t.Fatal("the stampSender has an inner Sender without injection")
	}
	resolve := keyResolver(t)
	b.press = map[string][]output.KeyEvent{}
	for i, c := range benchControls {
		b.press[c] = resolve(benchKeys[i])
	}

	b.attach(0)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	b.settle()
	if n := ss.n.Load(); n != 0 {
		t.Fatalf("%d Sends before the first edge, want 0", n)
	}
	b.c0 = b.calibrate("start")
	if w := b.c0.Width(); w > 10*time.Microsecond {
		t.Fatalf("the SDL clock calibrates to %v, want at most 10 µs: stages could not be attributed", w)
	}
	b.environment(scale)

	// The warm-up: its first press confirms the provisional slot.
	b.idle("the start", false)
	for k := range benchWarmup {
		b.step("warm-up", k, benchWarmupGap, b.edges(benchRow{pads: 1}, k), true)
	}
	b.logCount("slot confirmed", 1)
	b.idle("the warm-up", rows[0].noop)

	for i, r := range rows {
		benchDeadline(t, benchEstimate(rows[i:]), time.Minute)
		if r.pads > 1 {
			b.addPads(r)
		}
		if r.macros {
			b.useProfile(benchMacroProfile(), true)
			b.idle("the switch to the macro profile", r.noop)
			for k := range benchWarmupSteps {
				b.step(r.name+" warm-up", k, benchWarmupGap, b.edges(r, k), true)
			}
			b.idle("row "+r.name+"'s warm-up", r.noop)
		}
		logFrom := len(tl.log.String())
		gc0 := benchGCs()
		from := len(b.samples)
		for k := range r.n / r.pads {
			b.step(r.name, k, r.gap, b.edges(r, k), false)
		}
		gcs := benchGCs() - gc0
		note := b.checkLog(r.name, tl.log.String()[logFrom:])
		next := false
		if i+1 < len(rows) {
			next = rows[i+1].noop
		}
		b.idle("row "+r.name, next)
		if r.macros {
			b.useProfile(benchProfile(), false)
			b.idle("the switch back from the macro profile", next)
		}
		d := benchDone{row: r, from: from, to: len(b.samples), gcs: gcs, note: note}
		b.done = append(b.done, d)
		b.finished = append(b.finished, b.result(d, d.row, b.samples[d.from:d.to], b.c0, nil, nil))
	}

	// Nothing more came, and nothing more goes: the end calibration, the
	// stop, and the exit path sends nothing, since nothing is held.
	tl.sync()
	b.checkQuiet("after the rows")
	c1 := b.calibrate("end")
	c, err := b.c0.Merge(c1)
	if err != nil {
		b.fail("the start and end calibrations disagree: %v", err)
	}
	calib := fmt.Sprintf("SDL_GetTicksNS → QPC: tick zero within %s at the start, %s at the end, %s merged; SDL's counter %d Hz = QPC %d Hz; the start and end agree",
		latency.FormatDuration(b.c0.Width()), latency.FormatDuration(c1.Width()), latency.FormatDuration(c.Width()), latency.Freq(), latency.Freq())
	var code int
	if inject {
		code = stopFenced(t, b.rec, pid, tl)
		stopped = true
	} else {
		tl.cancel()
		code = tl.waitCode(t)
	}
	if code != 0 {
		b.fail("exit %d after the stop, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if n := ss.n.Load(); n != b.sends {
		b.fail("the exit path made %d Sends with no key held, want none", n-b.sends)
	}

	for i, s := range b.samples {
		lo, hi := c.Bounds(s.ns)
		if s.tc > hi || lo > s.tSlot || s.tSlot > s.tSend || s.tSend > s.tRet {
			b.fail("sample %d breaks tc ≤ hi, lo ≤ t_slot ≤ t_send ≤ t_ret: %+v, SDL's reading in [%d, %d]", i, s, lo, hi)
		}
	}
	var first, last []latency.Stamp
	if inject {
		first, last = b.hookStamps(pid)
	}
	var result, split []latency.Row
	for _, d := range b.done {
		s := b.samples[d.from:d.to]
		result = append(result, b.result(d, d.row, s, c, first, last))
		if !d.row.gated {
			continue
		}
		for _, ev := range []int{1, 3} {
			var part []benchSample
			for _, x := range s {
				if x.events == ev {
					part = append(part, x)
				}
			}
			sub := benchRow{name: fmt.Sprintf("%s%d", d.row.name, ev), gap: d.row.gap, n: len(part), pads: d.row.pads}
			row := b.result(d, sub, part, c, first, last)
			row.Events = fmt.Sprint(ev)
			split = append(split, row)
		}
	}

	g, reason, gateErr := latency.Decide(mode, inject, result)
	md := b.report(result, split, g, reason, calib)
	t.Log("\n" + md)
	if dir != "" {
		p := filepath.Join(dir, "latency.md")
		if err := os.WriteFile(p, []byte(md), 0o644); err != nil {
			t.Errorf("writing the report: %v", err)
		} else {
			t.Logf("wrote %s", p)
		}
	}
	if gateErr != nil {
		t.Fatalf("%s=1: %v", latency.EnvGate, gateErr)
	}
	if g == latency.GateEvaluated {
		for _, r := range result {
			if !r.Pass() {
				t.Errorf("row %s: p99 %s, want below %s (N = %d)", r.Name, latency.FormatDuration(r.Total.P99), latency.FormatDuration(r.Target), r.Total.N)
			}
		}
	}
}

// attach attaches benchPads[i].
func (b *bench) attach(i int) {
	b.t.Helper()
	v, err := b.tl.source.AttachVirtual(benchPads[i])
	if err != nil {
		b.t.Fatalf("AttachVirtual(%s): %v", benchPads[i], err)
	}
	b.pads = append(b.pads, v)
}

// settle waits for the attach records (DeviceAdded, and each axis's first
// value, which SDL sends twice) and moves the read cursors past them.
func (b *bench) settle() {
	time.Sleep(100 * time.Millisecond)
	b.tl.sync()
	for s := range b.st.slots {
		b.cursor[s] = b.st.slots[s].n.Load()
	}
}

// addPads attaches the other pads of row r, which take a slot each, and
// warms them up, which confirms their slots.
func (b *bench) addPads(r benchRow) {
	b.t.Helper()
	for i := len(b.pads); i < r.pads; i++ {
		b.attach(i)
	}
	b.tl.waitForCount(b.t, b.tl.log, `"msg":"slot assigned"`, r.pads)
	b.logCount("slot assigned", r.pads)
	b.settle()
	for k := range benchWarmupSteps {
		b.step(r.name+" warm-up", k, benchWarmupGap, b.edges(r, k), true)
	}
	b.logCount("slot confirmed", r.pads)
	b.idle("row "+r.name+"'s warm-up", r.noop)
}

// useProfile reloads the profile text, as a save does, and waits until the
// script runtime of each slot with a gamepad is running, with macros, or
// absent: row M runs beside a ready runtime, and no build is under way in
// any row.
func (b *bench) useProfile(text string, macros bool) {
	b.t.Helper()
	data := []byte(text)
	b.tl.profileMu.Lock()
	err := b.tl.reloadProfileLocked(context.Background(), nil, watch.Change{Data: data, Sum: sha256.Sum256(data)})
	b.tl.profileMu.Unlock()
	if err != nil {
		b.fail("reloading the profile (macros %v): %v", macros, err)
	}
	want := "Absent"
	if macros {
		want = "Running"
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		views, err := b.tl.sup.Scripts(ctx, "", false, false)
		cancel()
		ready, n := err == nil, 0
		for _, v := range views {
			if v.Device == 0 {
				continue // a free slot
			}
			n++
			ready = ready && v.Script.State == want && !v.Script.Pending && len(v.Script.Macros) == 0
		}
		if ready && n == len(b.pads) {
			return
		}
		if time.Now().After(deadline) {
			b.fail("the slots' scripts are not %s 10 s after the reload: %+v, %v", want, views, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// edges returns the edges of step k of r: with one pad, east down, east
// up, south down and south up in turn; with several, each pad's control of
// benchControls, all pressed or all released.
// The edges are in b.edgeBuf, valid until the next call, so that a step
// allocates nothing.
func (b *bench) edges(r benchRow, k int) []benchEdge {
	if r.pads == 1 {
		b.edgeBuf[0] = benchEdge{control: benchControls[k%4/2], down: k%2 == 0}
		return b.edgeBuf[:1]
	}
	es := b.edgeBuf[:r.pads]
	for i := range es {
		es[i] = benchEdge{pad: i, control: benchControls[i], down: k%2 == 0}
	}
	return es
}

// step drives the edges of one step, after gap, and waits until each has
// made its Send. With SendInput it then waits until the hook has seen
// their events; nothing more goes out unless it swallowed them all. It
// checks one Send per edge with the edge's keys, and one slot record per
// edge; during a warm-up it drops the samples and allows stray axis and
// device records.
func (b *bench) step(row string, k int, gap time.Duration, edges []benchEdge, warm bool) {
	b.t.Helper()
	if n := b.ss.n.Load(); n != b.sends {
		b.fail("%s, step %d: %d Sends since the last step, want none", row, k, n-b.sends)
	}
	if gap > 0 {
		time.Sleep(gap)
	}
	var tc [benchPadsMax]latency.Stamp
	for i, e := range edges {
		tc[i] = latency.Now()
		if err := b.pads[e.pad].SetButton(e.control, e.down); err != nil {
			b.fail("%s, step %d: SetButton(%s, %v) of pad %d: %v", row, k, e.control, e.down, e.pad+1, err)
		}
	}
	target := b.sends + int64(len(edges))
	b.timer.Reset(time.Second)
	for b.ss.n.Load() < target {
		select {
		case <-b.ss.done:
		case <-b.timer.C:
			b.fail("%s, step %d: edge did not reach the Sender within 1 s: %d Sends of %d (%d did not fit)",
				row, k, b.ss.n.Load()-b.sends, len(edges), b.ss.extra.Load())
		}
	}
	b.timer.Stop()
	if n := b.ss.n.Load(); n != target {
		b.fail("%s, step %d: %d Sends for %d edges", row, k, n-b.sends, len(edges))
	}
	recs := b.ss.recs[b.sends:target]
	// The fence comes first: once a key got past the hook, or the hook
	// is gone, nothing more is injected.
	if b.rec != nil {
		fence := false
		for i := range recs {
			if !recs[i].noop {
				b.want += recs[i].sent
				fence = true
			}
		}
		if fence {
			if err := b.rec.WaitOwn(b.want, injecttest.CanaryTimeout); err != nil {
				b.fail("%s, step %d: %v", row, k, err)
			}
		}
	}

	var sendOf [benchPadsMax]int
	var matched [benchPadsMax]bool
	for i := range recs {
		r := &recs[i]
		if r.err != nil || r.sent != r.events {
			b.fail("%s, step %d: Send injected %d of %d events: %v", row, k, r.sent, r.events, r.err)
		}
		m := -1
		for j, e := range edges {
			if !matched[j] && b.sameEvents(r, e) {
				m = j
				break
			}
		}
		if m < 0 {
			b.fail("%s, step %d: Send carried %d events %v, want those of %v", row, k, r.events, r.evs[:min(r.events, len(r.evs))], edges)
		}
		sendOf[m], matched[m] = i, true
	}

	var at [benchPadsMax]slotStamp
	var seen [benchPadsMax]bool
	for s := range b.st.slots {
		sl := &b.st.slots[s]
		for end := sl.n.Load(); b.cursor[s] < end; b.cursor[s]++ {
			x := sl.at[b.cursor[s]]
			if x.time == 0 || x.device == 0 {
				b.fail("%s, step %d: synthesized event (Resync) during measurement: slot %d, %+v", row, k, s, x)
			}
			m := -1
			for j, e := range edges {
				if !seen[j] && x.device == b.pads[e.pad].ID() && x.kind == benchKind(e.down) && x.control == e.control {
					m = j
					break
				}
			}
			if m < 0 {
				if warm && (x.kind == input.AxisMotion || x.kind == input.DeviceAdded) {
					continue
				}
				b.fail("%s, step %d: unexpected event %+v in slot %d", row, k, x, s)
			}
			at[m], seen[m] = x, true
		}
	}
	for j, e := range edges {
		if !seen[j] {
			b.fail("%s, step %d: no slot recorded %s %v of pad %d", row, k, e.control, e.down, e.pad+1)
		}
	}
	if !warm {
		for j := range edges {
			r := &recs[sendOf[j]]
			b.samples = append(b.samples, benchSample{
				tc: tc[j], tSlot: at[j].t, tSend: r.start, tRet: r.ret, ns: at[j].time,
				events: r.events, send: b.sends + int64(sendOf[j]),
			})
		}
	}
	b.sends = target
}

// benchKind is the event kind of an edge.
func benchKind(down bool) input.EventKind {
	if down {
		return input.ButtonDown
	}
	return input.ButtonUp
}

// sameEvents reports whether r carried the keys of e: the presses of its
// control's keys, or their releases in reverse.
func (b *bench) sameEvents(r *sendRec, e benchEdge) bool {
	want := b.press[e.control]
	if !e.down {
		want = releases(want)
	}
	if r.events != len(want) || len(want) > len(r.evs) {
		return false
	}
	for i, w := range want {
		if r.evs[i].ID() != w.ID() || r.evs[i].Up != w.Up {
			return false
		}
	}
	return true
}

// idle checks, on T-output, that no key is held and that the Arbiter's
// stats are those before the warm-up, and switches the Sender to the
// no-op one or back for what comes next.
func (b *bench) idle(after string, noop bool) {
	b.t.Helper()
	first := !b.idled
	b.idled = true
	err := b.tl.loop.Do(func(a *output.Arbiter) error {
		if h := a.Held(); len(h) > 0 {
			return fmt.Errorf("keys held after %s: %+v", after, h)
		}
		st := a.Stats()
		if first {
			b.stats = st
		} else if st != b.stats {
			return fmt.Errorf("the Arbiter's stats after %s are %+v, want %+v as before", after, st, b.stats)
		}
		b.ss.setNoop(noop)
		return nil
	})
	if err != nil {
		b.fail("%v", err)
	}
}

// checkQuiet checks that no Send and no slot event came since the last
// step, and that no probe lost a record.
func (b *bench) checkQuiet(when string) {
	b.t.Helper()
	if n := b.ss.n.Load(); n != b.sends {
		b.fail("%d Sends %s, want none", n-b.sends, when)
	}
	for s := range b.st.slots {
		sl := &b.st.slots[s]
		if n := sl.n.Load(); n != b.cursor[s] {
			b.fail("slot %d handled %d events %s, want none: %+v", s, n-b.cursor[s], when, sl.at[b.cursor[s]:n])
		}
		if n := sl.overflow.Load(); n > 0 {
			b.fail("slot %d: %d events did not fit the stamps", s, n)
		}
	}
	if n := b.st.badSlot.Load(); n > 0 {
		b.fail("%d events of slots beyond %d", n, len(b.st.slots))
	}
	if n := b.ss.extra.Load(); n > 0 {
		b.fail("%d Sends did not fit the records", n)
	}
}

// calibrate calibrates SDL's clock onto QPC.
func (b *bench) calibrate(when string) latency.SDLClock {
	b.t.Helper()
	freq, pairs, err := b.tl.source.SampleTicks(64)
	if err != nil {
		b.fail("SampleTicks at the %s: %v", when, err)
	}
	if freq != latency.Freq() {
		b.fail("SDL's performance counter runs at %d Hz and QPC at %d Hz at the %s: SDL does not read QPC", freq, latency.Freq(), when)
	}
	c, err := latency.CalibrateSDL(freq, pairs)
	if err != nil {
		b.fail("calibrating SDL's clock at the %s: %v", when, err)
	}
	return c
}

// logCount checks that the log has msg n times.
func (b *bench) logCount(msg string, n int) {
	b.t.Helper()
	if got := logLines(b.tl.log.String(), msg); len(got) != n {
		b.fail("the log has %q %d times, want %d:\n%s", msg, len(got), n, strings.Join(got, ""))
	}
}

// benchLogErrors are the log messages that make a row a measurement
// error: a slot changed hands, resynced or released everything, which
// would put synthesized events or other work into the row.
var benchLogErrors = []string{
	"slot assigned", "slot confirmed", "slot taken over", "slot reclaimed", "slot rehomed", "slot freed",
	"slot behind; resyncing", "input dropped events; releasing everything",
}

// checkLog checks the log lines of row, window. A warning or an error,
// or a message of benchLogErrors, is a measurement error; a gamepad that
// came or went is one in strict gate mode, and else the note it returns.
func (b *bench) checkLog(row, window string) (note string) {
	b.t.Helper()
	for _, msg := range benchLogErrors {
		if l := logLine(window, msg); l != "" {
			b.fail("row %s: %q during the row: %s", row, msg, l)
		}
	}
	for l := range strings.Lines(window) {
		if strings.Contains(l, `"level":"WARN"`) || strings.Contains(l, `"level":"ERROR"`) {
			b.fail("row %s: logged during the row: %s", row, l)
		}
	}
	n := len(logLines(window, "device connected")) + len(logLines(window, "device disconnected"))
	if n == 0 {
		return ""
	}
	if b.mode == latency.GateStrict {
		b.fail("row %s: %d gamepads came or went during the row, and %s=1:\n%s", row, n, latency.EnvGate, window)
	}
	return fmt.Sprintf("%d gamepads came or went during the row", n)
}

// hookStamps gives each Send with SendInput, in order, its events of those
// the hook saw with nonce, and returns the hook stamps of the first and
// last event of each, by Send. The hook must have seen exactly the events
// sent, each the one sent.
func (b *bench) hookStamps(nonce uint32) (first, last []latency.Stamp) {
	b.t.Helper()
	hooked, _ := ownKeys(b.rec, nonce)
	n := b.ss.n.Load()
	first, last = make([]latency.Stamp, n), make([]latency.Stamp, n)
	k, sent := 0, 0
	for i := range n {
		r := &b.ss.recs[i]
		if r.noop {
			continue
		}
		sent += r.sent
		if r.sent > len(r.evs) {
			b.fail("Send %d injected %d events, more than a step makes", i, r.sent)
		}
		for j := range r.sent {
			if k >= len(hooked) {
				break
			}
			e, ev := hooked[k], r.evs[j]
			if e.Up() != ev.Up || (!ev.ByVK && e.Scan != uint32(ev.Scan)) {
				b.fail("Send %d, event %d: the hook saw sc=0x%02X up=%v, want sc=0x%02X up=%v", i, j, e.Scan, e.Up(), ev.Scan, ev.Up)
			}
			if j == 0 {
				first[i] = e.Stamp
			}
			last[i] = e.Stamp
			k++
		}
	}
	if len(hooked) != sent {
		b.fail("the hook saw %d events with the test's nonce, want the %d sent", len(hooked), sent)
	}
	return first, last
}

// result is the latency.Row of samples, measured in d, as r: L and its
// stages with c, and with hook stamps the hook's stages.
func (b *bench) result(d benchDone, r benchRow, samples []benchSample, c latency.SDLClock, first, last []latency.Stamp) latency.Row {
	hooks := first != nil && b.inject && !r.noop
	n := len(samples)
	total, toSlot, toSend, toRet, cmd := make([]time.Duration, n), make([]time.Duration, n), make([]time.Duration, n), make([]time.Duration, n), make([]time.Duration, n)
	var toHook, fromHook []time.Duration
	hookFirst := -1
	if hooks {
		toHook, fromHook, hookFirst = make([]time.Duration, n), make([]time.Duration, n), 0
	}
	for i, s := range samples {
		sdl := c.Mid(s.ns)
		total[i] = s.tRet.Sub(sdl)
		toSlot[i] = s.tSlot.Sub(sdl)
		toSend[i] = s.tSend.Sub(s.tSlot)
		toRet[i] = s.tRet.Sub(s.tSend)
		cmd[i] = sdl.Sub(s.tc)
		if hooks {
			toHook[i] = first[s.send].Sub(s.tSend)
			fromHook[i] = s.tRet.Sub(last[s.send])
			if last[s.send] < s.tRet {
				hookFirst++
			}
		}
	}
	stages := []latency.Stage{
		{Name: "sdl→slot", Summary: latency.Summarize(toSlot)},
		{Name: "slot→send", Summary: latency.Summarize(toSend)},
		{Name: "send→ret", Summary: latency.Summarize(toRet)},
	}
	if hooks {
		stages = append(stages,
			latency.Stage{Name: "send→hook", Summary: latency.Summarize(toHook)},
			latency.Stage{Name: "hook→ret", Summary: latency.Summarize(fromHook)})
	}
	stages = append(stages, latency.Stage{Name: "cmd→sdl", Summary: latency.Summarize(cmd)})
	sender := "SendInput"
	if !b.inject || r.noop {
		sender = "no-op"
	}
	row := latency.Row{
		Name: r.name, Gap: r.gap, Sender: sender, Events: "1+3", Total: latency.Summarize(total),
		Stages: stages, HookFirst: hookFirst, GCs: d.gcs, Note: d.note,
	}
	if r.gated {
		row.Target, row.MinN = benchTarget, r.minN
	}
	if r.pads > 1 {
		row.Note = strings.TrimPrefix(row.Note+fmt.Sprintf("; %d pads, one edge each per step", r.pads), "; ")
	}
	if r.macros {
		row.Note = strings.TrimPrefix(row.Note+"; a macro bound, its runtime ready (SCR-50)", "; ")
	}
	return row
}

// environment reads what the report says of the run, while SDL is open.
func (b *bench) environment(scale float64) {
	b.t.Helper()
	b.env = latency.ReadEnvironment()
	var h strings.Builder
	if err := b.tl.source.WriteHeader(&h, "", b.tl.cfg.opts.sdl.Preset); err != nil {
		b.t.Fatalf("WriteHeader: %v", err)
	}
	lines := strings.Split(strings.TrimRight(h.String(), "\n"), "\n")
	b.envHead = append(b.envHead, lines[0])
	if len(lines) > 1 {
		hints := make([]string, 0, len(lines)-2)
		for _, l := range lines[2:] {
			hints = append(hints, strings.TrimSpace(l))
		}
		b.envHead = append(b.envHead, strings.TrimSpace(lines[1]+" "+strings.Join(hints, ", ")))
	}
	timer := "SDL_TIMER_RESOLUTION unset"
	if v, ok := os.LookupEnv("SDL_TIMER_RESOLUTION"); ok {
		timer = "SDL_TIMER_RESOLUTION=" + v
	}
	how := "no-op Sender: nothing injected, no hook (" + injecttest.EnvVar + " is not 1)"
	if b.inject {
		how = "SendInput (row E: no-op), nonce the test's pid; the test's low-level hook swallows every event, and each step waits for it (WaitOwn)"
	}
	gate := map[latency.GateMode]string{latency.GateAuto: "auto", latency.GateNone: "off (0)", latency.GateStrict: "strict (1)"}[b.mode]
	b.envHead = append(b.envHead, timer, how,
		fmt.Sprintf("scale %g (%s)", scale, latency.EnvScale),
		fmt.Sprintf("gate %s (%s)", gate, latency.EnvGate))
	b.envTail = []string{
		"test process (console, go test), not padmated",
		fmt.Sprintf("probes inside L: about 4 QPC reads (t_slot, t_send, t_ret and, with SendInput, the hook's), %d ns each here, and 3 stores",
			benchProbeCost().Nanoseconds()),
	}
}

// devices returns a line for each gamepad that connected during the run,
// from the router's log: its name, SDL driver and key, and whether the
// device filter ignored it.
func (b *bench) devices() []string {
	log := b.tl.log.String()
	ignored := map[uint32]bool{}
	for _, l := range logLines(log, "gamepad ignored: it does not match the device filter") {
		var f struct {
			ID uint32 `json:"id"`
		}
		if json.Unmarshal([]byte(l), &f) == nil {
			ignored[f.ID] = true
		}
	}
	var out []string
	for _, l := range logLines(log, "device connected") {
		var f struct {
			ID     uint32 `json:"id"`
			Name   string `json:"name"`
			Device string `json:"device"`
		}
		if err := json.Unmarshal([]byte(l), &f); err != nil {
			out = append(out, "unreadable log line: "+strings.TrimSpace(l))
			continue
		}
		driver := "driver unknown"
		if raw, err := hex.DecodeString(f.Device[:min(32, len(f.Device))]); err == nil && len(raw) == len(input.GUID{}) {
			driver = input.DriverName(input.GUID(raw), runtime.GOOS)
		}
		line := fmt.Sprintf("%q (%s) %s", f.Name, driver, f.Device)
		if ignored[f.ID] {
			line += ": gamepad ignored"
		}
		out = append(out, line)
	}
	return out
}

// report returns the Markdown report of rows and split.
func (b *bench) report(rows, split []latency.Row, g latency.Gate, reason, calib string) string {
	b.t.Helper()
	env := b.env
	env.Extra = append(append(append([]string(nil), b.envHead...), b.devices()...), b.envTail...)
	rep := latency.Report{
		Title: "padmate 端到端延迟（ENG-11）", Env: env, Calibration: calib, Gate: g, GateReason: reason,
		Rows: rows, Split: split,
		Notes: []string{
			"L = t_ret − t_sdl: from SDL's timestamp of a virtual gamepad's edge (SDL_GetTicksNS, mapped onto QPC by the calibration) to the return of the Send that carries its keys, through T-input, the router, the slot, the engine, T-output, the Arbiter, the ledger and MenuMask.",
			"sdl→slot = t_slot − t_sdl, with t_slot read in slot.Handle before engine.Step; slot→send = t_send − t_slot; send→ret = t_ret − t_send; send→hook = the hook stamp of the Send's first event − t_send; hook→ret = t_ret − that of its last event, negative when the hook ran after SendInput returned. cmd→sdl = t_sdl − t_cmd is not in L: T-input picks up a virtual gamepad's command between its 2 ms waits.",
			"Each edge presses or releases a button bound to hold keys, and makes one Send: east F13 (1 event), south Ctrl+Shift+F14 (3). Each step of row H presses or releases east, south, west (F17) and north (F18), one on each of four pads, in turn. The driver waits for each step's Sends and, with SendInput, until the hook has seen their events, then idles the row's gap.",
			"Row E uses the no-op Sender: the pipeline without SendInput. Row M is row B's edges after a switch to the same profile with a macro bound to leftshoulder, which no row presses (SCR-50): each slot has a script runtime that only runs macros; compare it with row B. A1/A3, B1/B3 and C1/C3 split the gated rows by events per Send, for information.",
			"The virtual gamepad's reply to SetButton wakes the driver goroutine inside sdl→slot, which a real gamepad does not: sdl→slot and L are pessimistic by that wake-up.",
			"SendInput returns only once the test's low-level hook has handled the batch (see \"hook ran before SendInput returned\"), so send→ret and L include waking the hook's thread. padmated installs no hook; with no other low-level hook in the system SendInput would not wait for one, which cannot be measured, since every injected key must be swallowed.",
		},
	}
	var md strings.Builder
	if err := rep.WriteMarkdown(&md); err != nil {
		b.t.Fatalf("writing the report: %v", err)
	}
	return md.String()
}

// fail stops the test for a measurement error. When rows have finished it
// first logs the partial report, with the start calibration and no hook
// stages, whose gate says why it was not evaluated; it is never reported
// as a latency result.
func (b *bench) fail(format string, args ...any) {
	b.t.Helper()
	msg := fmt.Sprintf(format, args...)
	if len(b.finished) > 0 {
		calib := fmt.Sprintf("SDL_GetTicksNS → QPC: tick zero within %s at the start; no end calibration", latency.FormatDuration(b.c0.Width()))
		b.t.Log("partial report:\n" + b.report(b.finished, nil, latency.GateSkipped, "measurement error: "+msg, calib))
	}
	b.t.Fatal(msg)
}

// benchDeadline stops t, before it presses anything more, unless the
// estimate need and margin fit before the test deadline: a test that times
// out panics without its cleanups, which would leave the hook's work
// undone (CLAUDE.md invariant 1).
func benchDeadline(t *testing.T, need, margin time.Duration) {
	t.Helper()
	if dl, ok := t.Deadline(); ok {
		if left := time.Until(dl); left < need+margin {
			t.Fatalf("estimated %v does not fit the test deadline (%v left, with a margin of %v); raise -timeout or lower %s",
				need.Round(time.Second), left.Round(time.Second), margin, latency.EnvScale)
		}
	}
}

// benchGCs returns the GC cycles so far.
func benchGCs() uint64 {
	s := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// benchProbeCost returns the cost of one latency.Now, over 10000.
func benchProbeCost() time.Duration {
	const n = 10000
	t0 := latency.Now()
	for range n {
		_ = latency.Now()
	}
	return latency.Now().Sub(t0) / n
}
