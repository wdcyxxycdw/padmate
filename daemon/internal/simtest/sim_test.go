package simtest_test

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

const milli = time.Millisecond

var owner = output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1}

// fakeTB collects the failures of the code under test instead of failing
// the test. Its Fatalf ends the calling goroutine like testing's does, so
// that code runs in run. Methods other than these panic.
type fakeTB struct {
	testing.TB
	errs     []string
	fatal    bool
	cleanups []func()
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
	f.fatal = true
	runtime.Goexit()
}

func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

// run calls fn in its own goroutine and waits for it to end.
func (f *fakeTB) run(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

// cleanup runs the registered cleanups, last first.
func (f *fakeTB) cleanup() {
	for _, fn := range slices.Backward(f.cleanups) {
		f.run(fn)
	}
	f.cleanups = nil
}

// expect checks that f failed with fatal, in one message per entry of
// subs, each containing all its strings.
func (f *fakeTB) expect(t *testing.T, fatal bool, subs ...[]string) {
	t.Helper()
	if f.fatal != fatal {
		t.Errorf("fatal = %v, want %v", f.fatal, fatal)
	}
	if len(f.errs) != len(subs) {
		t.Fatalf("got %d failures %q, want %d", len(f.errs), f.errs, len(subs))
	}
	for i, msg := range f.errs {
		for _, sub := range subs[i] {
			if !strings.Contains(msg, sub) {
				t.Errorf("failure %q does not contain %q", msg, sub)
			}
		}
	}
	f.errs, f.fatal = nil, false
}

func event(t *testing.T, spec string, up bool) output.KeyEvent {
	t.Helper()
	e, err := output.KeyEventFor(keys.MustParse(spec).Key(), up)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordEdge(t *testing.T) {
	tests := []struct {
		e    output.KeyEvent
		want string
	}{
		{event(t, "LCtrl", false), "+LCtrl@5"},
		{event(t, "KeyZ", true), "-KeyZ@5"},
		{event(t, "sc:0xE01D", false), "+sc:0xE01D@5"},
		{event(t, "Pause", false), "+Pause@5"},
		{output.KeyEvent{Scan: 0x2C}, "+sc:0x2C@5"},
		{output.KeyEvent{Scan: 0x1D, Ext: true, Up: true}, "-sc:0xE01D@5"},
		{output.KeyEvent{VK: 0x13, Scan: 0x45, ByVK: true}, "+vk:0x13@5"},
	}
	for _, tt := range tests {
		e := simtest.Record{Event: tt.e, T: clock.Time(5 * milli)}.Edge()
		if got := e.String(); got != tt.want {
			t.Errorf("Record{%v}.Edge() = %s, want %s", tt.e, got, tt.want)
		}
	}
}

func TestRecorder(t *testing.T) {
	clk := clock.NewFake()
	r := simtest.NewRecorder(clk)
	z, zUp := event(t, "Z", false), event(t, "Z", true)
	ctrl, ctrlUp := event(t, "LCtrl", false), event(t, "LCtrl", true)
	x := event(t, "X", false)
	errWarn := errors.New("test: warning")
	errOther := errors.New("test: other")

	send := func(wantN int, wantErr error, events ...output.KeyEvent) {
		t.Helper()
		n, err := r.Send(events)
		if n != wantN || !errors.Is(err, wantErr) || (wantErr == nil) != (err == nil) {
			t.Errorf("Send(%v) = %d, %v, want %d, %v", events, n, err, wantN, wantErr)
		}
	}
	send(2, nil, ctrl, z)
	clk.Advance(5 * milli)
	r.FailNext(1, nil)
	r.FailNext(0, errOther)
	r.WarnNext(errWarn)
	send(1, simtest.ErrBlocked, x, zUp)
	send(0, errOther, zUp)
	send(1, errWarn, zUp)
	send(0, nil) // an empty call
	if got := r.Calls(); got != 5 {
		t.Errorf("Calls() = %d, want 5", got)
	}
	if got, want := simtest.FormatTrace(r.Edges()), "+LCtrl@0 +Z@0 +X@5 -Z@5"; got != want {
		t.Errorf("Edges() = %s, want %s", got, want)
	}
	var got []string
	for _, rec := range r.Records() {
		got = append(got, fmt.Sprintf("%d:%s:%v", rec.Batch, rec.Edge(), rec.Injected))
	}
	want := []string{"0:+LCtrl@0:true", "0:+Z@0:true", "1:+X@5:true", "1:-Z@5:false", "2:-Z@5:false", "3:-Z@5:true"}
	if !slices.Equal(got, want) {
		t.Errorf("Records() = %q, want %q", got, want)
	}
	if got := r.Down(); !slices.Equal(got, []output.KeyEvent{ctrl, x}) {
		t.Errorf("Down() = %v, want LCtrl X", got)
	}

	// Inject decides for calls without a plan; its counts are clamped.
	var hook []int
	r.Inject = func(events []output.KeyEvent) (int, error) {
		hook = append(hook, len(events))
		if events[0].Up {
			return -3, errOther
		}
		return 9, nil
	}
	r.FailNext(0, nil)
	send(0, simtest.ErrBlocked, ctrlUp)
	send(0, errOther, ctrlUp)
	send(1, nil, ctrl)
	if !slices.Equal(hook, []int{1, 1}) {
		t.Errorf("Inject called with %v events, want [1 1]", hook)
	}
	r.Inject = nil

	// Keys are physical: a repeated press keeps its place, a press or
	// release by another name of the key counts, a stray release is
	// ignored.
	kz, sc2c := event(t, "KeyZ", false), output.KeyEvent{Scan: 0x2C, Up: true}
	send(3, nil, kz, x, ctrl)
	if got := r.Down(); !slices.Equal(got, []output.KeyEvent{ctrl, x, kz}) {
		t.Errorf("Down() = %v, want LCtrl X KeyZ", got)
	}
	send(4, nil, sc2c, sc2c, ctrlUp, event(t, "X", true))
	if got := r.Down(); len(got) != 0 {
		t.Errorf("Down() = %v, want none", got)
	}
	// Records and Down return copies.
	r.Records()[0].Injected = false
	if !r.Records()[0].Injected {
		t.Error("Records() shares its slice")
	}
}

// TestAdvanceOrder: Advance stops at every Arbiter deadline and fake timer
// in order, at their exact times; at a tie the timer fires first.
func TestAdvanceOrder(t *testing.T) {
	s := simtest.New(t)
	tie := s.Clock.NewTimer(10 * milli)
	mid := s.Clock.NewTimer(15 * milli)
	late := s.Clock.NewTimer(40 * milli)
	var seen []string
	s.Out.Inject = func(events []output.KeyEvent) (int, error) {
		seen = append(seen, fmt.Sprintf("%v tie=%d mid=%d late=%d", s.Clock.Now(), len(tie.C()), len(mid.C()), len(late.C())))
		return len(events), nil
	}
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 10*milli))
	must(t, s.Arb.Tap(owner, keys.MustParse("Z"), 20*milli))
	s.Advance(30 * milli)
	want := []string{
		"0s tie=0 mid=0 late=0",
		"0s tie=0 mid=0 late=0",
		"10ms tie=1 mid=0 late=0",
		"20ms tie=1 mid=1 late=0",
	}
	if !slices.Equal(seen, want) {
		t.Errorf("Send calls saw\n%q, want\n%q", seen, want)
	}
	if now := s.Clock.Now(); now != clock.Time(30*milli) {
		t.Errorf("Now() = %v after Advance, want 30ms", now)
	}
	s.ExpectOutput("+X@0 +Z@0 -X@10 -Z@20")

	// AdvanceTo an exact deadline runs it.
	must(t, s.Arb.Tap(owner, keys.MustParse("Q"), 5*milli))
	s.AdvanceTo(clock.Time(35 * milli))
	s.ExpectOutput("+Q@30 -Q@35")
	s.Advance(0)
	s.Advance(5 * milli)
	if len(late.C()) != 1 {
		t.Error("the timer at 40ms did not fire")
	}
	if got := s.Errors(); got != nil {
		t.Errorf("Errors() = %v", got)
	}
}

// TestAdvancePast: an Arbiter deadline that the clock passed without the
// Sim runs at once.
func TestAdvancePast(t *testing.T) {
	s := simtest.New(t)
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 10*milli))
	s.Clock.Advance(20 * milli)
	s.Advance(0)
	s.ExpectOutput("+X@0 -X@20")
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 10*milli))
	s.Clock.Advance(20 * milli)
	s.Settle()
	s.ExpectOutput("+X@20 -X@40")
}

func TestAdvanceToBackwards(t *testing.T) {
	f := &fakeTB{}
	fs := simtest.New(f)
	fs.Advance(10 * milli)
	f.run(func() { fs.AdvanceTo(clock.Time(5 * milli)) })
	f.expect(t, true, []string{"AdvanceTo(5ms)", "before now, 10ms"})
	f.cleanup()
	f.expect(t, false)
}

func TestSettle(t *testing.T) {
	s := simtest.New(t, simtest.WithArbiterOptions(output.ArbiterOptions{RetryInterval: 30 * milli}))
	s.Settle() // nothing to do
	if now := s.Clock.Now(); now != 0 {
		t.Errorf("Settle with nothing to do moved the clock to %v", now)
	}
	timer := s.Clock.NewTimer(90 * milli)
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 50*milli))
	must(t, s.Arb.Down(owner, keys.MustParse("Ctrl+Z")))
	s.Out.FailNext(1, nil)
	s.Out.FailNext(0, nil)
	if err := s.Arb.Up(owner, keys.MustParse("Ctrl+Z")); !errors.Is(err, simtest.ErrBlocked) {
		t.Fatalf("Up: %v, want ErrBlocked", err)
	}
	s.Settle()
	s.ExpectOutput("+X@0 +LCtrl@0 +Z@0 -Z@0 -LCtrl@30 -X@50")
	if now := s.Clock.Now(); now != clock.Time(90*milli) {
		t.Errorf("Now() = %v after Settle, want the timer's 90ms", now)
	}
	if len(timer.C()) != 1 {
		t.Error("the timer did not fire")
	}
}

// TestSettleBusy: Settle gives up after a minute of virtual time.
func TestSettleBusy(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *simtest.Sim)
		subs  []string
		errs  bool // Settle's Advance calls fail
	}{
		{"long tap", func(t *testing.T, s *simtest.Sim) {
			must(t, s.Arb.Tap(owner, keys.MustParse("X"), 2*time.Minute))
		}, []string{"still busy after 1m0s", "next deadline 2m0s", "X held by 0/1/decl/1"}, false},
		{"release never goes out", func(t *testing.T, s *simtest.Sim) {
			must(t, s.Arb.Down(owner, keys.MustParse("X")))
			s.Out.Inject = func([]output.KeyEvent) (int, error) { return 0, simtest.ErrBlocked }
			if err := s.Arb.Up(owner, keys.MustParse("X")); err == nil {
				t.Fatal("Up: no error")
			}
		}, []string{"still busy after 1m0s", "next deadline 1m0.1s", "X pending release"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeTB{}
			s := simtest.New(f)
			tt.setup(t, s)
			f.run(s.Settle)
			f.expect(t, true, tt.subs)
			s.Out.Inject = nil
			must(t, s.Arb.ReleaseAll())
			if errs := s.Errors(); (len(errs) > 0) != tt.errs {
				t.Errorf("Settle kept Advance errors %q, want errors: %v", errs, tt.errs)
			}
			f.cleanup()
			f.expect(t, false)
		})
	}
}

func TestExpectOutput(t *testing.T) {
	// ExpectOutput fails with Errorf, which fakeTB lets return, so only
	// the Fatalf case needs run.
	f := &fakeTB{}
	s := simtest.New(f)
	must(t, s.Arb.Tap(owner, keys.MustParse("Ctrl+Z"), 0))
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@0 -LCtrl@0")
	s.ExpectOutput("") // consumed
	s.Advance(5 * milli)
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 0))
	s.ExpectOutput("+X@5 -X@6")
	s.ExpectOutput("") // consumed even on a mismatch
	must(t, s.Arb.Tap(owner, keys.MustParse("Q"), 0))
	s.ExpectOutput("")
	f.expect(t, false,
		[]string{"got +X@5 -X@5", "want +X@5 -X@6"},
		[]string{"got +Q@5 -Q@5", "want (none)"})

	must(t, s.Arb.Tap(owner, keys.MustParse("Y"), 0))
	s.ClearOutput()
	s.ExpectOutput("")
	f.expect(t, false)
	want := "+LCtrl@0 +Z@0 -Z@0 -LCtrl@0 +X@5 -X@5 +Q@5 -Q@5 +Y@5 -Y@5"
	if got := s.Output(); got != want {
		t.Errorf("Output() = %s, want %s", got, want)
	}

	f.run(func() { s.ExpectOutput("+X@x") })
	f.expect(t, true, []string{"ExpectOutput", `"+X@x": bad time`})
	f.cleanup()
	f.expect(t, false)
}

func TestAssertNoneHeld(t *testing.T) {
	s := simtest.New(t)
	f := &fakeTB{}
	simtest.AssertNoneHeld(f, nil, nil)
	simtest.AssertNoneHeld(f, s.Arb, s.Out)
	f.expect(t, false)

	other := output.Owner{Slot: 1, Gen: 2, Source: output.SourceScript}
	must(t, s.Arb.Down(owner, keys.MustParse("Ctrl+Z")))
	must(t, s.Arb.Down(other, keys.MustParse("Ctrl")))
	simtest.AssertNoneHeld(f, s.Arb, s.Out)
	f.expect(t, false, []string{
		"LCtrl is physically down", "Z is physically down",
		"LCtrl held by 0/1/decl/1, 1/2/script/0", "Z held by 0/1/decl/1",
	})
	simtest.AssertNoneHeld(f, nil, s.Out)
	f.expect(t, false, []string{"LCtrl is physically down", "Z is physically down"})
	simtest.AssertNoneHeld(f, s.Arb, nil)
	f.expect(t, false, []string{"Arbiter: LCtrl held by", "Arbiter: Z held by"})

	// A release that failed twice.
	must(t, s.Arb.ReleaseOwner(other))
	s.Out.FailNext(0, nil)
	s.Out.FailNext(1, nil)
	if err := s.Arb.Up(owner, keys.MustParse("Ctrl+Z")); err == nil {
		t.Fatal("Up: no error")
	}
	simtest.AssertNoneHeld(f, s.Arb, s.Out)
	f.expect(t, false, []string{"LCtrl is physically down", "Arbiter: LCtrl pending release"})
	s.Settle()

	// A key whose press failed is only logically held.
	s.Out.FailNext(0, nil)
	if err := s.Arb.Down(owner, keys.MustParse("X")); err == nil {
		t.Fatal("Down: no error")
	}
	simtest.AssertNoneHeld(f, s.Arb, s.Out)
	if strings.Contains(strings.Join(f.errs, ""), "physically") {
		t.Errorf("X reported physically down: %q", f.errs)
	}
	f.expect(t, false, []string{"Arbiter: X held by 0/1/decl/1"})
	must(t, s.Arb.Up(owner, keys.MustParse("X")))
}

// TestNewCleanup: the cleanup New registers reports held keys and Advance
// errors nobody took.
func TestNewCleanup(t *testing.T) {
	f := &fakeTB{}
	simtest.New(f)
	f.cleanup()
	f.expect(t, false)

	s := simtest.New(f)
	must(t, s.Arb.Down(owner, keys.MustParse("Z")))
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 10*milli))
	f.cleanup()
	f.expect(t, false, []string{"keys left held", "Z is physically down", "X held by 0/1/decl/1"})

	s = simtest.New(f)
	must(t, s.Arb.Tap(owner, keys.MustParse("X"), 10*milli))
	s.Out.FailNext(0, nil)
	s.Out.FailNext(0, nil)
	s.Settle()
	f.cleanup()
	f.expect(t, false, []string{"did not take with Errors", "releasing X failed twice"})
}

// TestWithMenuMask: with the option, the Recorder records the mask key's
// events and FailNext counts them; without it, the output is unchanged.
func TestWithMenuMask(t *testing.T) {
	s := simtest.New(t, simtest.WithMenuMask())
	must(t, s.Arb.Down(owner, keys.MustParse("Alt")))
	s.Advance(10 * milli)
	must(t, s.Arb.Up(owner, keys.MustParse("Alt")))
	s.ExpectOutput("+LAlt@0 +vk:0xE8@10 -vk:0xE8@10 -LAlt@10")

	// FailNext(1) on the release injects the mask's press only: the mask
	// is down until the Arbiter's resend, which starts with its release.
	must(t, s.Arb.Down(owner, keys.MustParse("Alt")))
	s.Out.FailNext(1, nil)
	var atResend []string
	s.Out.Inject = func(events []output.KeyEvent) (int, error) {
		for _, e := range s.Out.Down() {
			atResend = append(atResend, simtest.Record{Event: e}.Edge().Name)
		}
		return len(events), nil
	}
	err := s.Arb.Up(owner, keys.MustParse("Alt"))
	if !errors.Is(err, simtest.ErrBlocked) || !strings.Contains(err.Error(), "vk=0xE8 left down") {
		t.Errorf("Up = %v, want ErrBlocked and the mask left down", err)
	}
	if got, want := strings.Join(atResend, " "), "LAlt vk:0xE8"; got != want {
		t.Errorf("down at the resend: %q, want %q", got, want)
	}
	s.ExpectOutput("+LAlt@10 +vk:0xE8@10 -vk:0xE8@10 -LAlt@10")
	if calls := s.Out.Calls(); calls != 5 {
		t.Errorf("%d Send calls, want 5", calls)
	}

	plain := simtest.New(t)
	must(t, plain.Arb.Down(owner, keys.MustParse("Alt")))
	must(t, plain.Arb.Up(owner, keys.MustParse("Alt")))
	plain.ExpectOutput("+LAlt@0 -LAlt@0")
}

// A pointer move is an edge of its own: the pointer has no press and no
// release, so a move never counts as a key physically down, and the Sim's
// cleanup finds nothing held after it.
func TestRecorderPointerEdges(t *testing.T) {
	s := simtest.New(t)
	if err := s.Arb.Move(owner, 5, -3); err != nil {
		t.Fatal(err)
	}
	s.Advance(10 * milli)
	if err := s.Arb.Move(owner, -1, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Arb.Jump(owner, 100, 65535); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("move=+5,-3@0 move=-1,+2@10 jump=100,65535@10")
	if down := s.Out.Down(); len(down) != 0 {
		t.Errorf("Down() = %v after moves, want none", down)
	}
	recs := s.Out.Records()
	if len(recs) != 3 || recs[0].Event.String() != "move +5,-3" {
		t.Errorf("records %+v", recs)
	}
}
