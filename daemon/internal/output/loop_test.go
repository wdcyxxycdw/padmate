package output_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// waitLimit bounds every real-time wait of the loop tests.
const waitLimit = 5 * time.Second

// releaseDown releases every key the Recorder has down, for a test that
// left keys down on purpose, and checks that none is left.
func releaseDown(t *testing.T, s *simtest.SyncRecorder) {
	t.Helper()
	s.With(func(r *simtest.Recorder) {
		for _, e := range slices.Backward(r.Down()) {
			e.Up = true
			_, _ = r.Send([]output.KeyEvent{e})
		}
		simtest.AssertNoneHeld(t, nil, r)
	})
}

// deafClock is a Fake whose timers never fire, so that only the due work
// a Loop runs before each command can end a tap.
type deafClock struct{ *clock.Fake }

func (deafClock) NewTimer(time.Duration) clock.Timer { return deafTimer{make(chan clock.Time)} }

type deafTimer struct{ c chan clock.Time }

func (t deafTimer) C() <-chan clock.Time   { return t.c }
func (deafTimer) Stop() bool               { return false }
func (deafTimer) Reset(time.Duration) bool { return false }

func deaf(f *clock.Fake) clock.Clock { return deafClock{f} }

// loopTest is a Loop on a fake clock that sends to a simtest.SyncRecorder.
type loopTest struct {
	t   *testing.T
	l   *output.Loop
	clk *clock.Fake
	rec *simtest.SyncRecorder
}

func startLoop(t *testing.T, opts output.LoopOptions) *loopTest {
	t.Helper()
	return startLoopOn(t, nil, opts)
}

// startLoopOn starts a Loop on wrap(fake), or on fake itself when wrap is
// nil. When the test ends it stops the Loop and checks that the Recorder
// has no key down.
func startLoopOn(t *testing.T, wrap func(*clock.Fake) clock.Clock, opts output.LoopOptions) *loopTest {
	t.Helper()
	fake := clock.NewFake()
	var clk clock.Clock = fake
	if wrap != nil {
		clk = wrap(fake)
	}
	lt := &loopTest{t: t, clk: fake, rec: simtest.NewSyncRecorder(fake)}
	lt.l = output.StartLoop(clk, lt.rec, opts)
	t.Cleanup(func() {
		_ = lt.stop()
		lt.rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	return lt
}

// do runs f on the loop and fails the test on its error.
func (lt *loopTest) do(f func(*output.Arbiter) error) {
	lt.t.Helper()
	if err := lt.l.Do(f); err != nil {
		lt.t.Fatalf("Do: %v", err)
	}
}

// sync returns once the loop has finished what it was doing, re-arming
// its timer included, so that the test can move the clock.
func (lt *loopTest) sync() {
	lt.t.Helper()
	lt.do(func(*output.Arbiter) error { return nil })
}

// stop calls Stop on another goroutine and moves the clock to each timer
// deadline in turn, so that Stop's retries run, and returns Stop's error.
// With taps pending it would end them by the timer, not by Stop.
func (lt *loopTest) stop() error {
	lt.t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- lt.l.Stop() }()
	limit := time.Now().Add(waitLimit)
	for {
		select {
		case err := <-errc:
			return err
		default:
		}
		if time.Now().After(limit) {
			lt.t.Fatal("Stop did not return")
		}
		if d, ok := lt.clk.Next(); ok {
			lt.clk.Set(max(d, lt.clk.Now()))
		} else {
			runtime.Gosched()
		}
	}
}

// waitDone waits until l has ended.
func waitDone(t *testing.T, l *output.Loop) {
	t.Helper()
	select {
	case <-l.Done():
	case <-time.After(waitLimit):
		t.Fatal("the loop did not end")
	}
}

func down(o output.Owner, spec string) func(*output.Arbiter) error {
	return func(a *output.Arbiter) error { return a.Down(o, keys.MustParse(spec)) }
}

func up(o output.Owner, spec string) func(*output.Arbiter) error {
	return func(a *output.Arbiter) error { return a.Up(o, keys.MustParse(spec)) }
}

func tap(o output.Owner, spec string, hold time.Duration) func(*output.Arbiter) error {
	return func(a *output.Arbiter) error { return a.Tap(o, keys.MustParse(spec), hold) }
}

func noop(*output.Arbiter) error { return nil }

// at is a Time in milliseconds.
func at(msec int) clock.Time { return clock.Time(time.Duration(msec) * ms) }

func TestStartLoopPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		clk  clock.Clock
		s    output.Sender
	}{
		{"no clock", nil, simtest.NewRecorder(clock.NewFake())},
		{"no sender", clock.NewFake(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("StartLoop did not panic")
				}
			}()
			output.StartLoop(tc.clk, tc.s, output.LoopOptions{})
		})
	}
}

func TestLoopDoNil(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	if err := lt.l.Do(nil); err == nil {
		t.Error("Do(nil) succeeded")
	}
	lt.sync()
}

// TestLoopDoSerializes: commands from many goroutines run one at a time
// on the loop goroutine. The counter has no lock; the race detector (CI)
// checks that only the loop touches it.
func TestLoopDoSerializes(t *testing.T) {
	const goroutines, commands = 8, 100
	lt := startLoop(t, output.LoopOptions{})
	n := 0
	var wg sync.WaitGroup
	for g := range goroutines {
		o := output.Owner{Slot: uint8(g), Gen: 1, Source: output.SourceDecl, Cycle: 1}
		wg.Go(func() {
			for range commands {
				err := lt.l.Do(func(a *output.Arbiter) error {
					n++
					return errors.Join(a.Down(o, keys.MustParse("Z")), a.Up(o, keys.MustParse("Z")))
				})
				if err != nil {
					t.Errorf("Do: %v", err)
				}
			}
		})
	}
	wg.Wait()
	if n != goroutines*commands {
		t.Errorf("%d commands ran, want %d", n, goroutines*commands)
	}
	// The press and the release of each command are next to each other.
	want := strings.TrimSpace(strings.Repeat("+Z@0 -Z@0 ", goroutines*commands))
	if got := lt.rec.Trace(); got != want {
		t.Errorf("output is not %d press-release pairs:\n%s", goroutines*commands, got)
	}
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestLoopRunsDueWorkBeforeCommand: the clock's timers never fire, so
// only the due work before the next command can end the tap.
func TestLoopRunsDueWorkBeforeCommand(t *testing.T) {
	lt := startLoopOn(t, deaf, output.LoopOptions{})
	lt.do(tap(ownerA, "Z", 50*ms))
	lt.clk.Advance(50 * ms)
	if got := lt.rec.Trace(); got != "+Z@0" {
		t.Fatalf("output before the next command: %s, want +Z@0", got)
	}
	lt.do(func(a *output.Arbiter) error {
		if held := a.Held(); len(held) > 0 {
			return fmt.Errorf("the command ran before the due work: %d keys held", len(held))
		}
		return nil
	})
	if got := lt.rec.Trace(); got != "+Z@0 -Z@50" {
		t.Errorf("output: %s, want +Z@0 -Z@50", got)
	}
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestLoopTimerEndsTap: moving the clock alone ends the taps, each at its
// time.
func TestLoopTimerEndsTap(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(tap(ownerA, "Ctrl+Z", 50*ms))
	lt.clk.Advance(30 * ms)
	lt.do(tap(ownerB, "X", 5*ms)) // ends first
	lt.clk.Advance(5 * ms)
	lt.rec.WaitFor(t, "+LCtrl@0 +Z@0 +X@30 -X@35")
	lt.sync()
	lt.clk.Advance(15 * ms)
	lt.rec.WaitFor(t, "+LCtrl@0 +Z@0 +X@30 -X@35 -Z@50 -LCtrl@50")
	lt.sync()
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestLoopStopReleasesAll(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	lt.do(down(ownerB, "Ctrl+Z"))
	if err := lt.stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Non-modifiers first, then modifiers, each in reverse press order.
	if got, want := lt.rec.Trace(), "+Space@0 +LCtrl@0 +Z@0 -Z@0 -Space@0 -LCtrl@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	if err := lt.l.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	ran := false
	if err := lt.l.Do(func(*output.Arbiter) error { ran = true; return nil }); !errors.Is(err, output.ErrLoopClosed) || ran {
		t.Errorf("Do after Stop = %v, ran %v; want ErrLoopClosed without running", err, ran)
	}
	select {
	case <-lt.l.Done():
	default:
		t.Error("Done is not closed after Stop")
	}
	if err := lt.l.Err(); err != nil {
		t.Errorf("Err after Stop = %v, want nil", err)
	}
	lt.rec.With(func(r *simtest.Recorder) {
		if d := r.Down(); len(d) > 0 {
			t.Errorf("keys down after Stop: %v", d)
		}
	})
}

// TestLoopErrNil: Err is nil while the loop runs.
func TestLoopErrNil(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.sync()
	if err := lt.l.Err(); err != nil {
		t.Errorf("Err while running = %v, want nil", err)
	}
	select {
	case <-lt.l.Done():
		t.Error("Done is closed while running")
	default:
	}
}

// errorsTo returns LoopOptions whose Errors sends to a channel.
func errorsTo(t *testing.T) (output.LoopOptions, <-chan error) {
	errs := make(chan error, 8)
	return output.LoopOptions{Errors: func(err error) {
		select {
		case errs <- err:
		default:
			t.Errorf("Errors: channel full at %v", err)
		}
	}}, errs
}

// TestLoopStopRetriesPendingRelease: Stop succeeds when a retry releases
// the key, and hands the failures before it to Errors; when every retry
// fails, it returns them.
func TestLoopStopRetriesPendingRelease(t *testing.T) {
	t.Run("retry succeeds", func(t *testing.T) {
		opts, errs := errorsTo(t)
		lt := startLoop(t, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(func(r *simtest.Recorder) { failRelease(r, 0) })
		if err := lt.stop(); err != nil {
			t.Errorf("Stop = %v, want nil: the retry released Space", err)
		}
		if got := lt.rec.Trace(); got != "+Space@0 -Space@100" {
			t.Errorf("output: %s, want +Space@0 -Space@100", got)
		}
		if len(errs) != 1 {
			t.Fatalf("%d errors, want the release's", len(errs))
		}
		wantErr(t, <-errs, errBlocked, "Space", "failed twice")
	})
	t.Run("every retry fails", func(t *testing.T) {
		opts, errs := errorsTo(t)
		lt := startLoop(t, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(func(r *simtest.Recorder) {
			for range 2 + 3 {
				r.FailNext(0, errBlocked)
			}
		})
		wantErr(t, lt.stop(), errBlocked, "Space", "failed twice", "failed again")
		if len(errs) > 0 {
			t.Errorf("Errors got %v too, want Stop's error only", <-errs)
		}
		lt.rec.With(func(r *simtest.Recorder) {
			if got, want := batches(r, 0), "[+Space] [-Space!] [-Space!] [-Space!] [-Space!] [-Space!]"; got != want {
				t.Errorf("Send calls:\n got %s\nwant %s", got, want)
			}
			var times []clock.Time
			for _, rec := range r.Records()[3:] {
				times = append(times, rec.T)
			}
			if want := []clock.Time{at(100), at(200), at(300)}; !slices.Equal(times, want) {
				t.Errorf("retries at %v, want %v", times, want)
			}
			if d := r.Down(); len(d) != 1 || keyName(d[0]) != "Space" {
				t.Errorf("keys down: %v, want Space", d)
			}
		})
		releaseDown(t, lt.rec)
	})
}

// panicSender forwards to a simtest.SyncRecorder and panics on the calls in
// panics, by call number from 0: before forwarding them, or after when
// after is set. Only the loop goroutine calls it.
type panicSender struct {
	rec    *simtest.SyncRecorder
	panics []int
	after  bool
	calls  int
}

func (p *panicSender) Send(events []output.KeyEvent) (int, error) {
	i := p.calls
	p.calls++
	boom := slices.Contains(p.panics, i)
	if boom && !p.after {
		panic(fmt.Sprintf("boom %d", i))
	}
	n, err := p.rec.Send(events)
	if boom {
		panic(fmt.Sprintf("boom %d", i))
	}
	return n, err
}

// eventsTrace formats events as "+Space -Z".
func eventsTrace(events []output.KeyEvent) string {
	s := make([]string, len(events))
	for i, e := range events {
		if e.Up {
			s[i] = "-" + keyName(e)
		} else {
			s[i] = "+" + keyName(e)
		}
	}
	return strings.Join(s, " ")
}

// checkPanic checks the error err of the Do in flight at the panic, when
// it is not nil, then the Loop: ended, with Err and Stop returning the
// *PanicError, and closed to Do. Its Value must be value, its Released
// released, and its ReleaseErr pass checkReleaseErr with target and
// releaseErr.
func checkPanic(t *testing.T, l *output.Loop, err error, value, released string, target error, releaseErr ...string) {
	t.Helper()
	var pe *output.PanicError
	if err != nil {
		if !errors.Is(err, output.ErrLoopClosed) || !errors.As(err, &pe) {
			t.Fatalf("Do = %v, want ErrLoopClosed and a *PanicError", err)
		}
		select {
		case <-l.Done():
		default:
			t.Error("Done is not closed when Do returns the panic")
		}
	} else {
		waitDone(t, l)
		if !errors.As(l.Err(), &pe) {
			t.Fatalf("Err = %v, want a *PanicError", l.Err())
		}
	}
	if pe.Value != value {
		t.Errorf("Value = %v, want %v", pe.Value, value)
	}
	if !strings.Contains(pe.Error(), value) {
		t.Errorf("error %q does not name the panic %q", pe, value)
	}
	if len(pe.Stack) == 0 {
		t.Error("no Stack")
	}
	if got := eventsTrace(pe.Released); got != released {
		t.Errorf("Released = %q, want %q", got, released)
	}
	if got := l.Err(); !isPanic(got, pe) {
		t.Errorf("Err = %v, want the *PanicError", got)
	}
	if got := l.Do(noop); !errors.Is(got, output.ErrLoopClosed) {
		t.Errorf("Do after the panic = %v, want ErrLoopClosed", got)
	}
	if got := l.Stop(); !isPanic(got, pe) {
		t.Errorf("Stop = %v, want the *PanicError", got)
	}
	checkReleaseErr(t, pe, target, releaseErr...)
}

// checkReleaseErr checks the error of the release after a panic: nil when
// subs is empty, else containing each of subs and wrapping target, when
// not nil.
func checkReleaseErr(t *testing.T, pe *output.PanicError, target error, subs ...string) {
	t.Helper()
	if len(subs) == 0 {
		if pe.ReleaseErr != nil {
			t.Errorf("ReleaseErr = %v, want nil", pe.ReleaseErr)
		}
		return
	}
	wantErr(t, pe.ReleaseErr, target, subs...)
	if !strings.Contains(pe.Error(), pe.ReleaseErr.Error()) {
		t.Errorf("error %q leaves out ReleaseErr %q", pe, pe.ReleaseErr)
	}
}

// TestLoopPanicInSend: after a Send panics, the loop releases every key
// that went down, from its ledger, in reverse press order. It cannot tell
// whether the presses of the call that panicked went out, so it releases
// them too.
func TestLoopPanicInSend(t *testing.T) {
	for _, tc := range []struct {
		name        string
		after       bool  // the call that panics reaches the Recorder
		panics      []int // Send calls that panic
		failRelease bool  // the release injects only its first event
		out         string
		released    string
		releaseErr  []string
		target      error
	}{{
		name: "before sending", panics: []int{2},
		out:      "+Space@0 +LCtrl@0 -Z@0 -LCtrl@0 -Space@0",
		released: "-Z -LCtrl -Space",
	}, {
		name: "after sending", after: true, panics: []int{2},
		out:      "+Space@0 +LCtrl@0 +Z@0 -Z@0 -LCtrl@0 -Space@0",
		released: "-Z -LCtrl -Space",
	}, {
		name: "release fails", panics: []int{2}, failRelease: true,
		out:      "+Space@0 +LCtrl@0 -Z@0",
		released: "-Z", releaseErr: []string{"LCtrl, Space"}, target: errBlocked,
	}, {
		name: "release panics", panics: []int{2, 3},
		out:        "+Space@0 +LCtrl@0",
		releaseErr: []string{"boom 3", "Z, LCtrl, Space"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: tc.panics, after: tc.after}, output.LoopOptions{})
			for _, f := range []func(*output.Arbiter) error{down(ownerA, "Space"), down(ownerB, "LCtrl")} {
				if err := l.Do(f); err != nil {
					t.Fatal(err)
				}
			}
			if tc.failRelease {
				rec.With(func(r *simtest.Recorder) { r.FailNext(1, errBlocked) })
			}
			checkPanic(t, l, l.Do(down(ownerA, "Z")), "boom 2", tc.released, tc.target, tc.releaseErr...)
			if got := rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			releaseDown(t, rec)
		})
	}
}

// TestLoopPanicInCommand: a panic in a command, or a Goexit, ends the
// loop like a panic in Send.
func TestLoopPanicInCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		end        func()
		panics     []int
		value      string
		out        string
		released   string
		releaseErr []string
	}{
		{"panic", func() { panic("boom") }, nil, "boom", "+Space@0 -Space@0", "-Space", nil},
		{"Goexit", runtime.Goexit, nil, "runtime.Goexit", "+Space@0 -Space@0", "-Space", nil},
		{"release panics", func() { panic("boom") }, []int{1}, "boom", "+Space@0", "", []string{"boom 1", "Space"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: tc.panics}, output.LoopOptions{})
			err := l.Do(func(a *output.Arbiter) error {
				if err := a.Down(ownerA, keys.MustParse("Space")); err != nil {
					return err
				}
				tc.end()
				return errors.New("not reached")
			})
			checkPanic(t, l, err, tc.value, tc.released, nil, tc.releaseErr...)
			if got := rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			releaseDown(t, rec)
		})
	}
}

// TestLoopPanicOnTimer: a panic in a Send of the loop's own work, with no
// command in flight, ends the loop too.
func TestLoopPanicOnTimer(t *testing.T) {
	fake := clock.NewFake()
	rec := simtest.NewSyncRecorder(fake)
	l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}}, output.LoopOptions{})
	if err := l.Do(tap(ownerA, "Z", 50*ms)); err != nil {
		t.Fatal(err)
	}
	fake.Advance(50 * ms)
	checkPanic(t, l, nil, "boom 1", "-Z", nil)
	if got := rec.Trace(); got != "+Z@0 -Z@50" {
		t.Errorf("output: %s, want +Z@0 -Z@50", got)
	}
}

func TestLoopErrors(t *testing.T) {
	// failAll makes the release of Space fail, its resend and one retry.
	failAll := func(r *simtest.Recorder) {
		for range 3 {
			r.FailNext(0, errBlocked)
		}
	}

	t.Run("timer", func(t *testing.T) {
		opts, errs := errorsTo(t)
		lt := startLoop(t, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(failAll)
		// The command's own error goes to the caller.
		wantErr(t, lt.l.Do(up(ownerA, "Space")), errBlocked, "Space", "failed twice")
		lt.clk.Set(at(100))
		select {
		case err := <-errs:
			wantErr(t, err, errBlocked, "Space", "failed again")
		case <-time.After(waitLimit):
			t.Fatal("no error from the retry at 100ms")
		}
		lt.sync()
		lt.clk.Set(at(200))
		lt.rec.WaitFor(t, "+Space@0 -Space@200")
		lt.sync()
		if err := lt.stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
		if len(errs) > 0 {
			t.Errorf("more errors: %v", <-errs)
		}
	})
	t.Run("before a command", func(t *testing.T) {
		opts, errs := errorsTo(t)
		lt := startLoopOn(t, deaf, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(failAll)
		wantErr(t, lt.l.Do(up(ownerA, "Space")), errBlocked, "Space", "failed twice")
		lt.clk.Set(at(100))
		lt.sync()
		if len(errs) != 1 {
			t.Fatalf("%d errors before the command returned, want 1", len(errs))
		}
		wantErr(t, <-errs, errBlocked, "Space", "failed again")
		lt.clk.Set(at(200))
		lt.sync()
		if got := lt.rec.Trace(); got != "+Space@0 -Space@200" {
			t.Errorf("output: %s, want +Space@0 -Space@200", got)
		}
		if err := lt.stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
		if len(errs) > 0 {
			t.Errorf("more errors: %v", <-errs)
		}
	})
}

// stall starts a Do on another goroutine whose function waits, and returns
// once the loop is in it, busy. resume lets the function return and waits
// for the Do; the test's cleanup resumes it too, before the Loop stops.
func stall(t *testing.T, l *output.Loop) (resume func()) {
	t.Helper()
	in, out := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(out) })
	t.Cleanup(release)
	errc := make(chan error, 1)
	go func() {
		errc <- l.Do(func(*output.Arbiter) error {
			close(in)
			<-out
			return nil
		})
	}()
	select {
	case <-in:
	case <-time.After(waitLimit):
		t.Fatal("the loop did not run the stalling Do")
	}
	return func() {
		t.Helper()
		release()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("stalling Do: %v", err)
			}
		case <-time.After(waitLimit):
			t.Fatal("the stalling Do did not return")
		}
	}
}

// blocked returns a Do function that fails unless the gate is closed, or
// with want false, open.
func blocked(want bool) func(*output.Arbiter) error {
	return func(a *output.Arbiter) error {
		if got := a.Blocked(); got != want {
			return fmt.Errorf("Blocked() = %v, want %v", got, want)
		}
		return nil
	}
}

// TestLoopBlock: Block(true) releases every key on the loop, through the
// MenuMask, so a lone Alt is masked, and closes the gate: a Do after it
// finds nothing held and presses nothing. Block(false) opens the gate.
func TestLoopBlock(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	lt.do(tap(ownerB, "X", 50*ms))
	lt.do(down(ownerA, "Alt"))
	lt.l.Block(true)
	lt.do(func(a *output.Arbiter) error {
		if held := a.Held(); len(held) > 0 {
			return fmt.Errorf("a Do after Block(true) ran before its release: %d keys held", len(held))
		}
		return errors.Join(blocked(true)(a), a.Down(ownerA, keys.MustParse("Z")), a.Tap(ownerB, keys.MustParse("Y"), 0))
	})
	const released = "+Space@0 +X@0 +LAlt@0 -X@0 -Space@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0"
	if got := lt.rec.Trace(); got != released {
		t.Errorf("output:\n got %s\nwant %s", got, released)
	}
	lt.do(func(a *output.Arbiter) error {
		if got := a.Stats().Blocked; got != 2 {
			return fmt.Errorf("Stats().Blocked = %d, want 2", got)
		}
		return nil
	})
	// The tap of X ended with the release: no timer is left.
	lt.clk.Advance(50 * ms)
	lt.sync()
	if got := lt.rec.Trace(); got != released {
		t.Errorf("output after the tap's hold:\n got %s\nwant %s", got, released)
	}

	lt.l.Block(false)
	lt.do(blocked(false))
	lt.do(down(ownerA, "Z"))
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if got, want := lt.rec.Trace(), released+" +Z@50 -Z@50"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// TestLoopBlockBeforeDo: a Do that starts after Block returns runs after
// Block's release, with the gate closed, even when the loop finds the Do
// and Block's token waiting together. Each round queues the Do while the
// loop is busy, so that its select may take either first.
func TestLoopBlockBeforeDo(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	for range 20 {
		lt.l.Block(false)
		lt.do(down(ownerA, "Space"))
		resume := stall(t, lt.l)
		lt.l.Block(true)
		errc := make(chan error, 1)
		go func() {
			errc <- lt.l.Do(func(a *output.Arbiter) error {
				if held := a.Held(); len(held) > 0 {
					return fmt.Errorf("the Do ran before Block's release: %d keys held", len(held))
				}
				return blocked(true)(a)
			})
		}()
		time.Sleep(time.Millisecond) // lets the Do reach the loop's queue
		resume()
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	if got, want := lt.rec.Trace(), strings.TrimSpace(strings.Repeat("+Space@0 -Space@0 ", 20)); got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// TestLoopBlockCoalesce: calls the loop has not applied yet coalesce into
// the last state of the gate, with one release when any of them was
// Block(true). Each Block(true) the loop applies runs its own ReleaseAll,
// which resends the pending releases.
func TestLoopBlockCoalesce(t *testing.T) {
	t.Run("on then off", func(t *testing.T) {
		lt := startLoop(t, output.LoopOptions{})
		lt.do(down(ownerA, "Space"))
		resume := stall(t, lt.l)
		lt.l.Block(true)
		lt.l.Block(false)
		resume()
		lt.do(blocked(false))
		lt.do(down(ownerA, "Z"))
		lt.rec.With(func(r *simtest.Recorder) {
			if got, want := batches(r, 0), "[+Space] [-Space] [+Z]"; got != want {
				t.Errorf("Send calls:\n got %s\nwant %s", got, want)
			}
		})
	})
	t.Run("twice", func(t *testing.T) {
		lt := startLoop(t, output.LoopOptions{})
		lt.do(down(ownerA, "Space"))
		resume := stall(t, lt.l)
		lt.l.Block(true)
		lt.l.Block(true)
		resume()
		lt.do(blocked(true))
		// Applied now, with nothing held: its ReleaseAll sends nothing.
		lt.l.Block(true)
		lt.do(blocked(true))
		lt.rec.With(func(r *simtest.Recorder) {
			if got, want := batches(r, 0), "[+Space] [-Space]"; got != want {
				t.Errorf("Send calls:\n got %s\nwant %s", got, want)
			}
		})
	})
	t.Run("pending", func(t *testing.T) {
		opts, errs := errorsTo(t)
		lt := startLoopOn(t, deaf, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(func(r *simtest.Recorder) { failRelease(r, 0) })
		lt.l.Block(true)
		lt.sync()
		if len(errs) != 1 {
			t.Fatalf("%d errors, want the release's", len(errs))
		}
		wantErr(t, <-errs, errBlocked, "Space", "failed twice")
		// The retry at 100 ms never fires: the next Block(true) resends it.
		lt.clk.Advance(10 * ms)
		lt.l.Block(true)
		lt.sync()
		lt.l.Block(true)
		lt.do(blocked(true))
		if got, want := lt.rec.Trace(), "+Space@0 -Space@10"; got != want {
			t.Errorf("output:\n got %s\nwant %s", got, want)
		}
		lt.rec.With(func(r *simtest.Recorder) {
			if got, want := batches(r, 0), "[+Space] [-Space!] [-Space!] [-Space]"; got != want {
				t.Errorf("Send calls:\n got %s\nwant %s", got, want)
			}
		})
		if len(errs) > 0 {
			t.Errorf("more errors: %v", <-errs)
		}
	})
}

// TestLoopBlockNonBlocking: Block returns at once while the loop is busy,
// however many calls it makes, and after Stop; it works from inside a Do
// function too.
func TestLoopBlockNonBlocking(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	resume := stall(t, lt.l)
	returned := make(chan struct{})
	go func() {
		for range 3 {
			lt.l.Block(true)
			lt.l.Block(false)
		}
		lt.l.Block(true)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(waitLimit):
		t.Fatal("Block waited for the busy loop")
	}
	if got := lt.rec.Trace(); got != "+Space@0" {
		t.Errorf("output while the loop is busy: %s, want +Space@0", got)
	}
	resume()
	lt.rec.WaitFor(t, "+Space@0 -Space@0")
	lt.do(func(a *output.Arbiter) error {
		lt.l.Block(false)
		return blocked(true)(a)
	})
	lt.do(blocked(false))
	lt.do(down(ownerA, "Z"))
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}

	returned = make(chan struct{})
	go func() {
		lt.l.Block(true)
		lt.l.Block(false)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(waitLimit):
		t.Fatal("Block after Stop did not return")
	}
	if got, want := lt.rec.Trace(), "+Space@0 -Space@0 +Z@0 -Z@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// blocking fails the test unless the owners the callers keep on l's gate
// are want.
func blocking(t *testing.T, l *output.Loop, want output.Blocker) {
	t.Helper()
	if got := l.Blocking(); got != want {
		t.Errorf("Blocking() = %#x, want %#x", got, want)
	}
}

// statBlocked returns a Do function that fails unless the gate has
// dropped want presses so far.
func statBlocked(want uint64) func(*output.Arbiter) error {
	return func(a *output.Arbiter) error {
		if got := a.Stats().Blocked; got != want {
			return fmt.Errorf("Stats().Blocked = %d, want %d", got, want)
		}
		return nil
	}
}

// TestLoopBlockReasons: each owner keeps the gate closed on its own (DMN-09).
// Opening one owner leaves the gate closed while the other keeps it, and
// every closing call runs a ReleaseAll, which the second one shows by
// resending the release the first left pending.
func TestLoopBlockReasons(t *testing.T) {
	opts, errs := errorsTo(t)
	lt := startLoopOn(t, deaf, opts)
	lt.do(down(ownerA, "Space"))
	lt.rec.With(func(r *simtest.Recorder) { failRelease(r, 0) })
	lt.l.BlockFor(output.BlockUser, true)
	blocking(t, lt.l, output.BlockUser)
	lt.do(blocked(true))
	if len(errs) != 1 {
		t.Fatalf("%d errors, want the release's", len(errs))
	}
	wantErr(t, <-errs, errBlocked, "Space", "failed twice")
	lt.l.BlockFor(output.BlockSystem, true)
	blocking(t, lt.l, output.BlockUser|output.BlockSystem)
	lt.do(blocked(true))
	lt.l.BlockFor(output.BlockUser, false)
	blocking(t, lt.l, output.BlockSystem)
	lt.do(blocked(true))
	// Dropped by the gate the system still keeps closed.
	lt.do(down(ownerB, "Z"))
	lt.do(statBlocked(1))
	// Opening an owner that is open changes nothing.
	lt.l.BlockFor(output.BlockUser, false)
	blocking(t, lt.l, output.BlockSystem)
	lt.do(blocked(true))

	lt.l.BlockFor(output.BlockSystem, false)
	blocking(t, lt.l, 0)
	lt.do(blocked(false))
	lt.do(down(ownerB, "Z"))
	lt.do(up(ownerB, "Z"))
	lt.do(statBlocked(1))
	lt.rec.With(func(r *simtest.Recorder) {
		if got, want := batches(r, 0), "[+Space] [-Space!] [-Space!] [-Space] [+Z] [-Z]"; got != want {
			t.Errorf("Send calls:\n got %s\nwant %s", got, want)
		}
	})
	if len(errs) > 0 {
		t.Errorf("more errors: %v", <-errs)
	}
}

// TestLoopBlockReasonsCoalesce: calls the loop has not applied yet
// coalesce into the owners as they were last left, with one release when
// any of them closed an owner, and none when they only opened.
func TestLoopBlockReasonsCoalesce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls func(l *output.Loop)
		want  output.Blocker
		out   string
	}{
		{"user, system, open user", func(l *output.Loop) {
			l.BlockFor(output.BlockUser, true)
			l.BlockFor(output.BlockSystem, true)
			l.BlockFor(output.BlockUser, false)
		}, output.BlockSystem, "[+Space] [-Space]"},
		{"system on then off", func(l *output.Loop) {
			l.BlockFor(output.BlockSystem, true)
			l.BlockFor(output.BlockSystem, false)
		}, 0, "[+Space] [-Space]"},
		{"both, open both", func(l *output.Loop) {
			l.BlockFor(output.BlockSystem, true)
			l.Block(true)
			l.Block(false)
			l.BlockFor(output.BlockSystem, false)
			l.BlockFor(output.BlockSystem, true)
		}, output.BlockSystem, "[+Space] [-Space]"},
		{"opens only", func(l *output.Loop) {
			l.BlockFor(output.BlockSystem, false)
			l.Block(false)
		}, 0, "[+Space]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lt := startLoop(t, output.LoopOptions{})
			lt.do(down(ownerA, "Space"))
			resume := stall(t, lt.l)
			tc.calls(lt.l)
			blocking(t, lt.l, tc.want)
			resume()
			lt.do(blocked(tc.want != 0))
			lt.rec.With(func(r *simtest.Recorder) {
				if got := batches(r, 0); got != tc.out {
					t.Errorf("Send calls:\n got %s\nwant %s", got, tc.out)
				}
			})
			// The test's cleanup releases what is left: Space in "opens only".
		})
	}
}

// TestLoopBlockForInvalid: BlockFor ignores the bits that name no owner,
// and does nothing when none is left; Block is BlockFor for BlockUser.
func TestLoopBlockForInvalid(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	for _, b := range []output.Blocker{0, 0x80} {
		lt.l.BlockFor(b, true)
		blocking(t, lt.l, 0)
		lt.do(blocked(false))
	}
	if got := lt.rec.Trace(); got != "+Space@0" {
		t.Errorf("output after BlockFor with no owner: %s, want +Space@0", got)
	}
	lt.l.BlockFor(output.BlockUser|0x80, true)
	blocking(t, lt.l, output.BlockUser)
	lt.do(blocked(true))
	lt.l.Block(false)
	blocking(t, lt.l, 0)
	lt.do(blocked(false))
	lt.l.Block(true)
	blocking(t, lt.l, output.BlockUser)
	lt.l.BlockFor(0xE0, false)
	blocking(t, lt.l, output.BlockUser)
	lt.l.BlockFor(output.BlockUser, false)
	blocking(t, lt.l, 0)
	lt.l.BlockFor(output.BlockUser|output.BlockSystem, true)
	blocking(t, lt.l, output.BlockUser|output.BlockSystem)
	lt.l.BlockFor(output.BlockUser|output.BlockSystem, false)
	blocking(t, lt.l, 0)
	lt.do(blocked(false))
	if got, want := lt.rec.Trace(), "+Space@0 -Space@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// TestRemoteArbiterMatchesArbiter runs the same random commands, with the
// same Send failures, on an Arbiter and through a RemoteArbiter: the
// output and the errors must be the same. Some steps change the event gap
// (OUT-17) or move both clocks; the plain Arbiter then runs its due work
// as the Loop does, before each command and after each step.
func TestRemoteArbiterMatchesArbiter(t *testing.T) {
	fake := clock.NewFake()
	plainRec := simtest.NewRecorder(fake)
	plain := output.NewArbiter(fake, plainRec, output.ArbiterOptions{})
	pacedSends := 0
	plainRec.Inject = func(events []output.KeyEvent) (int, error) {
		if plain.EventGap() > 0 {
			pacedSends++
		}
		return len(events), nil
	}
	lt := startLoop(t, output.LoopOptions{})
	var remote interface {
		Down(output.Owner, keys.Chord) error
		DownFor(output.Owner, keys.Chord, time.Duration) error
		Up(output.Owner, keys.Chord) error
		Tap(output.Owner, keys.Chord, time.Duration) error
		Repeat(output.Owner, keys.Chord) error
		TapSequence(output.Owner, []keys.Chord, time.Duration, time.Duration) error
		RepeatTap(output.Owner, keys.Chord, time.Duration) error
		StopRepeat(output.Owner, keys.Chord) error
		ReleaseOwners(func(output.Owner) bool) error
		Advance() error
		NextDeadline() (clock.Time, bool)
	} = lt.l.Remote()

	owners := []output.Owner{ownerA, ownerB, ownerS, {}}
	chords := []keys.Chord{
		keys.MustParse("Z"), keys.MustParse("Ctrl+Z"), keys.MustParse("LCtrl"),
		keys.MustParse("Shift+A"), keys.MustParse("Space"), {},
	}
	errString := func(err error) string {
		if err == nil {
			return "<nil>"
		}
		return err.Error()
	}
	// due runs the work due on plain, as the Loop does before a command
	// and at its timer.
	due := func() {
		if d, ok := plain.NextDeadline(); ok && d <= fake.Now() {
			_ = plain.Advance()
		}
	}
	rng := rand.New(rand.NewPCG(16, 3))
	failed := 0
	for i := range 2000 {
		o, c := owners[rng.IntN(len(owners))], chords[rng.IntN(len(chords))]
		if rng.IntN(8) == 0 {
			n := rng.IntN(2)
			plainRec.FailNext(n, errBlocked)
			lt.rec.With(func(r *simtest.Recorder) { r.FailNext(n, errBlocked) })
		}
		due()
		var op string
		var p, r error
		switch rng.IntN(13) {
		case 0:
			op, p, r = "Down", plain.Down(o, c), remote.Down(o, c)
		case 1:
			op, p, r = "Up", plain.Up(o, c), remote.Up(o, c)
		case 2:
			op, p, r = "Tap", plain.Tap(o, c, 0), remote.Tap(o, c, 0)
		case 3:
			op, p, r = "Repeat", plain.Repeat(o, c), remote.Repeat(o, c)
		case 4:
			match := func(x output.Owner) bool { return x.Slot == o.Slot }
			op, p, r = "ReleaseOwners", plain.ReleaseOwners(match), remote.ReleaseOwners(match)
		case 5:
			d := time.Duration(rng.IntN(3)) * ms
			op, p, r = "DownFor", plain.DownFor(o, c, d), remote.DownFor(o, c, d)
		case 6:
			d := time.Duration(rng.IntN(3)) * ms
			op, p, r = "TapSequence", plain.TapSequence(o, []keys.Chord{c, c}, d, 0), remote.TapSequence(o, []keys.Chord{c, c}, d, 0)
		case 7:
			op, p, r = "RepeatTap", plain.RepeatTap(o, c, 0), remote.RepeatTap(o, c, 0)
		case 8:
			op, p, r = "StopRepeat", plain.StopRepeat(o, c), remote.StopRepeat(o, c)
		case 9:
			press, gap := time.Duration(rng.IntN(3))*ms, time.Duration(rng.IntN(3))*ms
			op, p, r = "SetTiming", plain.SetTiming(press, gap), lt.l.Do(func(a *output.Arbiter) error { return a.SetTiming(press, gap) })
		case 11:
			gap := time.Duration(rng.IntN(3)) * 5 * ms
			op, p, r = "SetEventGap", plain.SetEventGap(gap), lt.l.Do(func(a *output.Arbiter) error { return a.SetEventGap(gap) })
		case 12:
			d := time.Duration(rng.IntN(12)) * ms
			op = "clock"
			fake.Advance(d)
			lt.clk.Advance(d)
		default:
			op, p, r = "ReleaseOwners(nil)", plain.ReleaseOwners(nil), remote.ReleaseOwners(nil)
		}
		if errString(p) != errString(r) {
			t.Fatalf("command %d, %s(%v, %v): Arbiter returned %v, RemoteArbiter %v", i, op, o, c, p, r)
		}
		if p != nil {
			failed++
		}
		// Both run what is due now before the next Send failure is set.
		due()
		lt.sync()
	}
	if failed < 100 {
		t.Errorf("only %d commands failed: the sequence hardly tests the errors", failed)
	}
	if pacedSends < 30 {
		t.Errorf("only %d Send calls with an event gap: the sequence hardly tests it", pacedSends)
	}
	if err := remote.Advance(); err != nil {
		t.Errorf("RemoteArbiter.Advance = %v, want nil", err)
	}
	if d, ok := remote.NextDeadline(); ok {
		t.Errorf("RemoteArbiter.NextDeadline = %v, want none", d)
	}
	// Release everything; releases that fail stay pending, and the next
	// ReleaseAll resends them.
	for i := 0; len(plain.Held()) > 0 || plain.Queued() > 0; i++ {
		if i == 10 {
			t.Fatalf("still held after 10 ReleaseAll: %s", heldString(plain.Held()))
		}
		due()
		p := plain.ReleaseAll()
		r := lt.l.Do(func(a *output.Arbiter) error { return a.ReleaseAll() })
		if errString(p) != errString(r) {
			t.Fatalf("ReleaseAll %d: Arbiter returned %v, RemoteArbiter %v", i, p, r)
		}
	}
	lt.do(func(a *output.Arbiter) error {
		if held := a.Held(); len(held) > 0 {
			return fmt.Errorf("the loop's Arbiter still holds %s", heldString(held))
		}
		return nil
	})
	lt.rec.With(func(r *simtest.Recorder) {
		if got, want := batches(r, 0), batches(plainRec, 0); got != want {
			t.Errorf("Send calls differ:\nRemoteArbiter %s\nArbiter       %s", got, want)
		}
	})
	simtest.AssertNoneHeld(t, plain, plainRec)
	if err := lt.stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// isPanic reports whether err is pe.
func isPanic(err error, pe *output.PanicError) bool {
	var got *output.PanicError
	return errors.As(err, &got) && got == pe
}

// TestLoopMasksMenuKey: the Loop's Sender is a MenuMask, so the release of
// a lone Alt is masked and that of Alt+Tab is not (OUT-08).
func TestLoopMasksMenuKey(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Alt"))
	lt.do(up(ownerA, "Alt"))
	lt.do(down(ownerA, "Alt+Tab"))
	lt.do(up(ownerA, "Alt+Tab"))
	want := "+LAlt@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0 +LAlt@0 +Tab@0 -Tab@0 -LAlt@0"
	if got := lt.rec.Trace(); got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// TestLoopStopMasksMenuKey: Stop's release goes through the MenuMask too.
func TestLoopStopMasksMenuKey(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "LWin"))
	if err := lt.stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, want := lt.rec.Trace(), "+LWin@0 +vk:0xE8@0 -vk:0xE8@0 -LWin@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// TestLoopStopReleasesStuckMask: when Stop's release injects the mask's
// press only, the Send that carries the Arbiter's resend or retry of the
// Alt release starts with the mask's release. When every retry fails,
// Stop's error names the mask.
func TestLoopStopReleasesStuckMask(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fails []int // FailNext(n) for the Send calls from the release on
		out   string
		sends string
		stuck string // the keys left down when every retry fails
	}{{
		name: "resend", fails: []int{1},
		out:   "+LAlt@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0",
		sends: "[+LAlt] [+vk:0xE8 -vk:0xE8! -LAlt!] [-vk:0xE8 -LAlt]",
	}, {
		name: "retry", fails: []int{1, 0},
		out:   "+LAlt@0 +vk:0xE8@0 -vk:0xE8@100 -LAlt@100",
		sends: "[+LAlt] [+vk:0xE8 -vk:0xE8! -LAlt!] [-vk:0xE8! -LAlt!] [-vk:0xE8 -LAlt]",
	}, {
		name: "every retry fails", fails: []int{1, 0, 0, 0, 0}, stuck: "LAlt vk:0xE8",
		out: "+LAlt@0 +vk:0xE8@0",
		sends: "[+LAlt] [+vk:0xE8 -vk:0xE8! -LAlt!] [-vk:0xE8! -LAlt!] [-vk:0xE8! -LAlt!]" +
			" [-vk:0xE8! -LAlt!] [-vk:0xE8! -LAlt!]",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			opts, errs := errorsTo(t)
			lt := startLoop(t, opts)
			lt.do(down(ownerA, "Alt"))
			lt.rec.With(func(r *simtest.Recorder) {
				for _, n := range tc.fails {
					r.FailNext(n, errBlocked)
				}
			})
			err := lt.stop()
			if tc.stuck != "" {
				wantErr(t, err, errBlocked, "LAlt", "vk=0xE8 left down")
				if len(errs) > 0 {
					t.Errorf("Errors got %v too, want Stop's error only", <-errs)
				}
			} else {
				if err != nil {
					t.Errorf("Stop = %v, want nil", err)
				}
				if len(errs) != 1 {
					t.Fatalf("%d errors, want the release's", len(errs))
				}
				wantErr(t, <-errs, errBlocked, "vk=0xE8 left down")
			}
			if got := lt.rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			lt.rec.With(func(r *simtest.Recorder) {
				if got := batches(r, 0); got != tc.sends {
					t.Errorf("Send calls:\n got %s\nwant %s", got, tc.sends)
				}
				var held []string
				for _, e := range r.Down() {
					held = append(held, keyName(e))
				}
				if got := strings.Join(held, " "); got != tc.stuck {
					t.Errorf("keys down: %q, want %q", got, tc.stuck)
				}
			})
			if tc.stuck != "" {
				releaseDown(t, lt.rec)
			}
		})
	}
}

// TestLoopPanicMasksMenuKey: the ledger's release after a panic goes
// through the MenuMask; Released has the caller's releases only.
func TestLoopPanicMasksMenuKey(t *testing.T) {
	fake := clock.NewFake()
	rec := simtest.NewSyncRecorder(fake)
	l := output.StartLoop(fake, rec, output.LoopOptions{})
	if err := l.Do(down(ownerA, "Alt")); err != nil {
		t.Fatal(err)
	}
	checkPanic(t, l, l.Do(func(*output.Arbiter) error { panic("boom") }), "boom", "-LAlt", nil)
	if got, want := rec.Trace(), "+LAlt@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
}

// TestLoopPanicInSendMasked: the Sender injects a masked release, then
// panics. The MenuMask counts the mask as down and the ledger still has
// LAlt, so the ledger's release starts with the mask's release and masks
// LAlt again: conservative, and nothing is left down.
func TestLoopPanicInSendMasked(t *testing.T) {
	fake := clock.NewFake()
	rec := simtest.NewSyncRecorder(fake)
	l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}, after: true}, output.LoopOptions{})
	if err := l.Do(down(ownerA, "Alt")); err != nil {
		t.Fatal(err)
	}
	checkPanic(t, l, l.Do(up(ownerA, "Alt")), "boom 1", "-LAlt", nil)
	want := "+LAlt@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0 -vk:0xE8@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0"
	if got := rec.Trace(); got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
}

// TestLoopShortLedgerReleaseLeavesMask: when the ledger's release after a
// panic injects the mask's press only, nothing releases the mask, and
// ReleaseErr names it next to LAlt.
func TestLoopShortLedgerReleaseLeavesMask(t *testing.T) {
	fake := clock.NewFake()
	rec := simtest.NewSyncRecorder(fake)
	l := output.StartLoop(fake, rec, output.LoopOptions{})
	if err := l.Do(down(ownerA, "Alt")); err != nil {
		t.Fatal(err)
	}
	err := l.Do(func(*output.Arbiter) error {
		rec.With(func(r *simtest.Recorder) { r.FailNext(1, errBlocked) })
		panic("boom")
	})
	checkPanic(t, l, err, "boom", "", errBlocked, "LAlt", "vk=0xE8 left down")
	if got, want := rec.Trace(), "+LAlt@0 +vk:0xE8@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	releaseDown(t, rec)
}

// TestLoopLedgerReleasePanicNamesMask: the Sender panics on the ledger's
// masked release of LAlt after a panic. The MenuMask counts the mask as
// down, so ReleaseErr names it next to LAlt.
func TestLoopLedgerReleasePanicNamesMask(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after=%v", after), func(t *testing.T) {
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}, after: after}, output.LoopOptions{})
			if err := l.Do(down(ownerA, "Alt")); err != nil {
				t.Fatal(err)
			}
			err := l.Do(func(*output.Arbiter) error { panic("boom") })
			checkPanic(t, l, err, "boom", "", nil, "panic releasing LAlt, vk=0xE8", "boom 1")
			want := "+LAlt@0"
			if after {
				want += " +vk:0xE8@0 -vk:0xE8@0 -LAlt@0"
			}
			if got := rec.Trace(); got != want {
				t.Errorf("output:\n got %s\nwant %s", got, want)
			}
			releaseDown(t, rec)
		})
	}
}

// TestLoopPanicReleasesMaskAlone: Alt's press fails, so the ledger has
// nothing down, but the Arbiter still sends Alt's masked release, and the
// mask's press goes out without its release. After a panic, or a Goexit,
// the ledger releases the mask alone.
func TestLoopPanicReleasesMaskAlone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		panics   []int // panicSender's
		after    bool
		upFails  []int // FailNext plans for Alt's release and its resend
		failMask bool  // the mask's release after the panic fails
		goexit   bool  // the command calls runtime.Goexit instead of panicking
		value    string
		out      string
		target   error
		errs     []string // ReleaseErr's
	}{{
		name: "resend fails", upFails: []int{1, 0}, value: "boom",
		out: "+vk:0xE8@0 -vk:0xE8@0",
	}, {
		name: "Goexit", upFails: []int{1, 0}, goexit: true, value: "runtime.Goexit",
		out: "+vk:0xE8@0 -vk:0xE8@0",
	}, {
		name: "release fails", upFails: []int{1, 0}, failMask: true, value: "boom",
		out: "+vk:0xE8@0", target: errBlocked, errs: []string{"vk=0xE8 left down"},
	}, {
		name: "Sender panics", panics: []int{1}, after: true, value: "boom 1",
		out: "+vk:0xE8@0 -vk:0xE8@0 -LAlt@0 -vk:0xE8@0",
	}, {
		name: "release panics", panics: []int{1, 2}, value: "boom 1",
		errs: []string{"panic releasing vk=0xE8", "boom 2"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: tc.panics, after: tc.after}, output.LoopOptions{})
			rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
			if err := l.Do(down(ownerA, "Alt")); !errors.Is(err, errBlocked) {
				t.Fatalf("Down = %v, want one wrapping %v", err, errBlocked)
			}
			var err error
			if tc.upFails == nil {
				err = l.Do(up(ownerA, "Alt"))
			} else {
				rec.With(func(r *simtest.Recorder) {
					for _, n := range tc.upFails {
						r.FailNext(n, errBlocked)
					}
				})
				wantErr(t, l.Do(up(ownerA, "Alt")), errBlocked, "vk=0xE8 left down")
				err = l.Do(func(*output.Arbiter) error {
					if tc.failMask {
						rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
					}
					if tc.goexit {
						runtime.Goexit()
					}
					panic("boom")
				})
			}
			checkPanic(t, l, err, tc.value, "", tc.target, tc.errs...)
			if got := rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			releaseDown(t, rec)
		})
	}
}
