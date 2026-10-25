package script

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// key.sequence and key.type (SCR-26, API 0.21, ADR-0070).

// seqSender records the key and the Unicode events a Loop injects, as "+Z",
// "-Z" and "'a'" for a typed unit.
type seqSender struct {
	mu     sync.Mutex
	events []string
}

func (s *seqSender) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		switch {
		case e.Unicode && e.Up:
		case e.Unicode && e.Unit >= 0xD800 && e.Unit < 0xE000:
			s.events = append(s.events, fmt.Sprintf("u%04X", e.Unit))
		case e.Unicode:
			s.events = append(s.events, fmt.Sprintf("%q", rune(e.Unit)))
		case e.Key.IsValid():
			prefix := "+"
			if e.Up {
				prefix = "-"
			}
			s.events = append(s.events, prefix+e.Key.String())
		}
	}
	return len(events), nil
}

func (s *seqSender) trace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "[" + strings.Join(s.events, " ") + "]"
}

func (s *seqSender) want(t *testing.T, want string) {
	t.Helper()
	if got := s.trace(); got != want {
		t.Fatalf("output = %s, want %s", got, want)
	}
}

// seqRig is a Host over a Loop on a fake clock, with a sender that traces.
type seqRig struct {
	t    *testing.T
	clk  *clock.Fake
	rec  *seqSender
	loop *output.Loop
	h    *Host
	// budgetErrors counts the RunDue errors that were the output limiter's.
	budgetErrors int
}

func newSeqRig(t *testing.T, opts output.LoopOptions) *seqRig {
	t.Helper()
	clk := clock.NewFake()
	rec := new(seqSender)
	loop := output.StartLoop(clk, rec, opts)
	r := &seqRig{t: t, clk: clk, rec: rec, loop: loop}
	r.h = NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	r.h.active = true
	t.Cleanup(func() {
		if err := loop.Stop(); err != nil {
			t.Error(err)
		}
		if got := rec.trace(); strings.Count(got, "+") != strings.Count(got, "-") {
			t.Errorf("keys remain held: %s", got)
		}
	})
	return r
}

// run runs source and returns the Promise it ends with.
func (r *seqRig) run(source string) *goja.Promise {
	r.t.Helper()
	v, err := r.h.VM.RunString(source)
	if err != nil {
		r.t.Fatal(err)
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		r.t.Fatalf("%s is not a Promise", source)
	}
	return p
}

// advance moves the clock by d and lets the output loop and the host run what is
// due: the loop reports its notifications, and the host runs its timers and what
// they started, until nothing is due.
func (r *seqRig) advance(d time.Duration) {
	r.t.Helper()
	r.clk.Advance(d)
	r.settle()
}

func (r *seqRig) settle() {
	r.t.Helper()
	for range 20 {
		if err := r.loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
			r.t.Fatal(err)
		}
		// The output limiter refusing a step is reported, and some tests want it.
		if err := r.h.RunDue(); errors.Is(err, ErrOutputBudget) {
			r.budgetErrors++
		} else if err != nil {
			r.t.Fatal(err)
		}
		if d, ok := r.h.NextDeadline(); !ok || d > r.clk.Now() {
			return
		}
	}
	r.t.Fatal("the host is still busy after 20 turns")
}

func (r *seqRig) wantPromise(p *goja.Promise, state goja.PromiseState, dropped bool) {
	r.t.Helper()
	if p.State() != state {
		r.t.Fatalf("Promise state = %v, want %v", p.State(), state)
	}
	if state == goja.PromiseStateFulfilled {
		if got := p.Result().ToObject(r.h.VM).Get("dropped").ToBoolean(); got != dropped {
			r.t.Fatalf("dropped = %v, want %v", got, dropped)
		}
	}
}

// A sequence taps a shortcut, waits and types, a step at a time, and the Promise
// resolves when the last has ended.
func TestSequenceRunsTheSteps(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{keys: "Z"}, {wait: 100}, {text: "ab"}, {keys: "X"}])`)
	r.settle()
	r.rec.want(t, "[+Z -Z]")
	r.wantPromise(p, goja.PromiseStatePending, false)
	// The wait counts from the release at 0.
	r.advance(99 * time.Millisecond)
	r.rec.want(t, "[+Z -Z]")
	r.advance(1 * time.Millisecond)
	r.rec.want(t, "[+Z -Z 'a']")
	// A character a gap (20 ms) after the one before.
	r.advance(19 * time.Millisecond)
	r.rec.want(t, "[+Z -Z 'a']")
	r.advance(1 * time.Millisecond)
	r.rec.want(t, "[+Z -Z 'a' 'b']")
	r.wantPromise(p, goja.PromiseStatePending, false)
	// Then the shortcut, a gap after the last character.
	r.advance(20 * time.Millisecond)
	r.rec.want(t, "[+Z -Z 'a' 'b' +X -X]")
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// key.type is a sequence of one text.
func TestTypeTypesTheText(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.type("你好😀")`)
	r.settle()
	r.rec.want(t, "['你']")
	r.advance(20 * time.Millisecond)
	r.advance(20 * time.Millisecond)
	// The pair goes out as its two units.
	r.rec.want(t, "['你' '好' uD83D uDE00]")
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// A newline is an Enter tap and a tab a Tab tap.
func TestTypeNewlineAndTab(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.type("a\n\tb")`)
	for range 10 {
		r.advance(20 * time.Millisecond)
	}
	r.rec.want(t, "['a' +Enter -Enter +Tab -Tab 'b']")
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// A leading wait counts from the start, and a wait last keeps the Promise
// pending until it is over.
func TestSequenceLeadingAndTrailingWait(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{wait: 50}, {keys: "Z"}, {wait: 70}])`)
	r.settle()
	r.rec.want(t, "[]")
	r.advance(50 * time.Millisecond)
	r.rec.want(t, "[+Z -Z]")
	r.wantPromise(p, goja.PromiseStatePending, false)
	r.advance(69 * time.Millisecond)
	r.wantPromise(p, goja.PromiseStatePending, false)
	r.advance(1 * time.Millisecond)
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// A wait shorter than the gap does not shorten it, and waits add up.
func TestSequenceWaitAndGap(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{keys: "Z"}, {wait: 5}, {keys: "X"}, {wait: 30}, {wait: 30}, {keys: "C"}])`)
	r.settle()
	r.advance(19 * time.Millisecond)
	r.rec.want(t, "[+Z -Z]")
	r.advance(1 * time.Millisecond)
	r.rec.want(t, "[+Z -Z +X -X]")
	r.advance(59 * time.Millisecond)
	r.rec.want(t, "[+Z -Z +X -X]")
	r.advance(1 * time.Millisecond)
	r.rec.want(t, "[+Z -Z +X -X +C -C]")
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// The owner's release ends a sequence in its wait, in its text and in a tap,
// resolves its Promise (for a script's own releaseAll) or abandons it (a safety
// release), and nothing it had not sent is sent.
func TestSequenceCanceledByReleaseAll(t *testing.T) {
	for _, tc := range []struct {
		name    string
		steps   string
		advance time.Duration
		sent    string
	}{
		{"in a wait", `[{keys: "Z"}, {wait: 100}, {text: "ab"}]`, 50 * time.Millisecond, "[+Z -Z]"},
		{"in a text", `[{text: "abc"}, {keys: "Z"}]`, 25 * time.Millisecond, "['a' 'b']"},
		{"in the gap", `[{keys: "Z"}, {keys: "X"}]`, 10 * time.Millisecond, "[+Z -Z]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSeqRig(t, output.LoopOptions{})
			p := r.run(`__padmate.key.sequence(` + tc.steps + `)`)
			r.settle()
			r.advance(tc.advance)
			r.rec.want(t, tc.sent)
			r.h.releaseAll(true)
			r.settle()
			r.advance(10 * time.Second)
			r.rec.want(t, tc.sent)
			r.wantPromise(p, goja.PromiseStateFulfilled, true)
			if d, ok := r.h.NextDeadline(); ok {
				t.Errorf("NextDeadline = %v after the release", d)
			}
		})
	}
}

// A safety release (ReleaseKeys: pause, reload, the gate) abandons the
// continuation like it does for key.tap: the Promise is never settled and
// nothing more is sent.
func TestSequenceCanceledBySafetyRelease(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{keys: "Z"}, {wait: 100}, {text: "ab"}])`)
	r.settle()
	r.advance(10 * time.Millisecond)
	r.h.ReleaseKeys()
	r.settle()
	r.advance(10 * time.Second)
	r.rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending {
		t.Fatalf("safety release settled the old Promise: %v", p.State())
	}
}

// Closing the output gate (the pause, the emergency release) ends the sequence
// in the output: the Promise resolves dropped.
func TestSequenceEndedByTheGate(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{text: "abcd"}])`)
	r.settle()
	r.rec.want(t, "['a']")
	r.loop.Block(true)
	r.settle()
	r.advance(time.Second)
	r.rec.want(t, "['a']")
	r.wantPromise(p, goja.PromiseStateFulfilled, true)
}

// A sequence started in a handler that is not active, or while the script loads,
// is lost: the Promise resolves dropped and nothing is sent.
func TestSequenceNotActive(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	r.h.active = false
	p := r.run(`__padmate.key.sequence([{keys: "Z"}, {text: "a"}])`)
	r.settle()
	r.rec.want(t, "[]")
	r.wantPromise(p, goja.PromiseStateFulfilled, true)
}

// The output limiter is asked for each step: 60 texts of one character started
// at once cost 120 key changes, over the burst of 100, so ten are refused and
// resolve dropped, with one diagnostic.
func TestTypeOutputBudget(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	v, err := r.h.VM.RunString(`const ps = []; for (let i = 0; i < 60; i++) ps.push(__padmate.key.type("a")); ps`)
	if err != nil {
		t.Fatal(err)
	}
	r.settle()
	ps := v.Export().([]any)
	dropped := 0
	for _, p := range ps {
		pr := p.(*goja.Promise)
		if pr.State() != goja.PromiseStateFulfilled {
			t.Fatalf("Promise state = %v", pr.State())
		}
		if pr.Result().ToObject(r.h.VM).Get("dropped").ToBoolean() {
			dropped++
		}
	}
	if dropped != 10 {
		t.Errorf("%d texts dropped, want 10", dropped)
	}
	if r.budgetErrors != 1 {
		t.Errorf("%d diagnostics of the limiter, want 1", r.budgetErrors)
	}
	if got := strings.Count(r.rec.trace(), "'a'"); got != 50 {
		t.Errorf("%d characters typed, want 50", got)
	}
}

// A refused character ends the text, and the sequence goes on with the next step
// once the limiter allows it.
func TestTypeBudgetRefusalEndsTheText(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	// 49 characters of 2 changes use 98 of the 100.
	r.run(`__padmate.key.type("` + strings.Repeat("x", 49) + `")`)
	p := r.run(`__padmate.key.sequence([{text: "abc"}, {keys: "Z"}])`)
	r.settle()
	// The 49 go out a gap apart, and the second text has 2 tokens, then 1 character.
	for range 60 {
		r.advance(20 * time.Millisecond)
	}
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
}

// Without an output sink the host records what it would have sent.
func TestSequenceRecordsWithoutASink(t *testing.T) {
	clk := clock.NewFake()
	h := NewHost(clk)
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.sequence([{keys: "Z"}, {text: "é"}, {wait: 10}, {keys: "X"}])`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	for range 10 {
		clk.Advance(20 * time.Millisecond)
		if err := h.RunDue(); err != nil {
			t.Fatal(err)
		}
	}
	if p.State() != goja.PromiseStateFulfilled {
		t.Fatalf("Promise state = %v", p.State())
	}
	var got []string
	for _, e := range h.Output() {
		switch {
		case e.Text != "":
			got = append(got, "text:"+e.Text)
		case e.Up:
			got = append(got, "-"+e.Spec)
		default:
			got = append(got, "+"+e.Spec)
		}
	}
	if want := "+Z -Z text:é +X -X"; strings.Join(got, " ") != want {
		t.Errorf("recorded %q, want %q", strings.Join(got, " "), want)
	}
}

// What is wrong with the arguments is a TypeError, and nothing is sent.
func TestSequenceArgumentErrors(t *testing.T) {
	long := "[" + strings.Repeat(`{wait: 1},`, 65) + "]"
	for _, tc := range []struct{ name, call, want string }{
		{"type no argument", `key.type()`, "text must be a string"},
		{"type a number", `key.type(5)`, "text must be a string"},
		{"type empty", `key.type("")`, "empty text"},
		{"type control", `key.type("a\r")`, "U+000D"},
		{"type too long", `key.type("a".repeat(257))`, "too long"},
		{"sequence no argument", `key.sequence()`, "steps must be an array"},
		{"sequence a string", `key.sequence("Ctrl+A")`, "steps must be an array"},
		{"sequence empty", `key.sequence([])`, "1 to 64 steps, not 0"},
		{"sequence too many", `key.sequence(` + long + `)`, "1 to 64 steps, not 65"},
		{"step a string", `key.sequence(["Ctrl+A"])`, "step 0: a step is an object"},
		{"step an array", `key.sequence([["Ctrl+A"]])`, "step 0: a step is an object"},
		{"step empty", `key.sequence([{keys: "A"}, {}])`, "step 1: a step needs one of"},
		{"step two members", `key.sequence([{keys: "A", wait: 5}])`, "only one of keys, wait and text"},
		{"step unknown", `key.sequence([{waitMs: 5}])`, `unknown property "waitMs"`},
		{"keys a number", `key.sequence([{keys: 5}])`, "keys must be a string"},
		{"keys bad spec", `key.sequence([{keys: "Z+Ctrl"}])`, "step 0: keys"},
		{"keys unknown", `key.sequence([{keys: "NoSuchKey"}])`, "unknown key"},
		{"wait zero", `key.sequence([{wait: 0}])`, "1 to 10000"},
		{"wait too long", `key.sequence([{wait: 10001}])`, "1 to 10000"},
		{"wait fraction", `key.sequence([{wait: 1.5}])`, "whole number"},
		{"wait a string", `key.sequence([{wait: "100"}])`, "number of milliseconds"},
		{"wait NaN", `key.sequence([{wait: NaN}])`, "whole number"},
		{"wait Infinity", `key.sequence([{wait: Infinity}])`, "1 to 10000"},
		{"text a number", `key.sequence([{text: 5}])`, "text must be a string"},
		{"text empty", `key.sequence([{keys: "A"}, {text: ""}])`, "step 1: empty text"},
		{"text control", `key.sequence([{text: "\u0007"}])`, "U+0007"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSeqRig(t, output.LoopOptions{})
			_, err := r.h.VM.RunString(`const key = __padmate.key; ` + tc.call)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), "TypeError") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want a TypeError with %q", err, tc.want)
			}
			r.settle()
			r.rec.want(t, "[]")
			if len(r.h.base.pendingTaps) != 0 {
				t.Error("an operation is pending")
			}
		})
	}
}

// Good steps of every shape are accepted, with the member the step has.
func TestSequenceStepShapes(t *testing.T) {
	r := newSeqRig(t, output.LoopOptions{})
	p := r.run(`__padmate.key.sequence([{keys: " ctrl + a "}, {wait: 1}, {wait: 10000.0}, {text: "\n"}, {keys: "VolumeMute"}])`)
	r.settle()
	r.rec.want(t, "[+LCtrl +A -A -LCtrl]")
	r.advance(10 * time.Second)
	r.advance(time.Second)
	r.advance(time.Second)
	r.wantPromise(p, goja.PromiseStateFulfilled, false)
	r.rec.want(t, "[+LCtrl +A -A -LCtrl +Enter -Enter +VolumeMute -VolumeMute]")
}

// A sequence in a macro is the run's: it ends with the run's scope.
func TestSequenceInAMacro(t *testing.T) {
	clk := clock.NewFake()
	ps := ProgramsForTest(t, map[string]string{
		"macros/m.ts": `import { key } from "padmate";
export default async function () {
  await key.sequence([{ keys: "A" }, { wait: 10 }, { text: "x" }, { keys: "B" }]);
}`,
	}, "", "m.ts")
	h := activeMacroHost(t, clk, ps)
	if err := h.RunMacro("m.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		clk.Advance(20 * time.Millisecond)
		if err := h.RunDue(); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, e := range h.Output() {
		switch {
		case e.Text != "":
			got = append(got, "text:"+e.Text)
		case e.Up:
			got = append(got, "-"+e.Spec)
		default:
			got = append(got, "+"+e.Spec)
		}
	}
	if want := "+A -A text:x +B -B"; strings.Join(got, " ") != want {
		t.Errorf("recorded %q, want %q", strings.Join(got, " "), want)
	}
}
