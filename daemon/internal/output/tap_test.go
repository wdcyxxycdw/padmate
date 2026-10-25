package output

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

var errFake = errors.New("fake: blocked")

// fakeSender records every batch and tracks which keys are held. accept
// scripts how many events call i injects; calls past the script inject
// everything. warn scripts the error that call i returns along with a full
// count.
type fakeSender struct {
	t        *testing.T
	accept   []int
	warn     []error
	calls    [][]KeyEvent
	callTime []time.Time
	held     map[KeyID]bool
}

func newFake(t *testing.T, accept ...int) *fakeSender {
	return &fakeSender{t: t, accept: accept, held: map[KeyID]bool{}}
}

func (f *fakeSender) Send(events []KeyEvent) (int, error) {
	i := len(f.calls)
	f.calls = append(f.calls, slices.Clone(events))
	f.callTime = append(f.callTime, time.Now())
	n, err := len(events), error(nil)
	if i < len(f.accept) && f.accept[i] < n {
		n, err = f.accept[i], errFake
	}
	if n == len(events) && i < len(f.warn) {
		err = f.warn[i]
	}
	for _, e := range events[:n] {
		k := e.ID()
		switch {
		case !e.Up && f.held[k]:
			f.t.Errorf("call %d: %v pressed while held", i, e)
		case e.Up && !f.held[k]:
			f.t.Errorf("call %d: %v released while not held", i, e)
		}
		f.held[k] = !e.Up
	}
	return n, err
}

// heldKeys lists the keys still held.
func (f *fakeSender) heldKeys() []KeyID {
	var ks []KeyID
	for k, h := range f.held {
		if h {
			ks = append(ks, k)
		}
	}
	return ks
}

func (f *fakeSender) assertNoneHeld() {
	f.t.Helper()
	if ks := f.heldKeys(); len(ks) > 0 {
		f.t.Errorf("keys left held: %v", ks)
	}
}

// assertCalls compares the batches offered to the sender.
func (f *fakeSender) assertCalls(want ...[]KeyEvent) {
	f.t.Helper()
	if len(f.calls) != len(want) {
		f.t.Fatalf("%d Send calls, want %d:\n%s", len(f.calls), len(want), fmtCalls(f.calls))
	}
	for i := range want {
		if !slices.Equal(f.calls[i], want[i]) {
			f.t.Errorf("Send call %d:\n%s\nwant:\n%s", i, fmtCalls(f.calls[i:i+1]), fmtCalls(want[i:i+1]))
		}
	}
}

func fmtCalls(calls [][]KeyEvent) string {
	var b strings.Builder
	for i, c := range calls {
		for _, e := range c {
			fmt.Fprintf(&b, "  %d: %s\n", i, e)
		}
	}
	return b.String()
}

func stroke(t *testing.T, spec string) Stroke {
	t.Helper()
	st, err := ChordStroke(keys.MustParse(spec))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func cat(parts ...[]KeyEvent) []KeyEvent {
	var out []KeyEvent
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func rel(downs ...KeyEvent) []KeyEvent {
	ups := make([]KeyEvent, len(downs))
	for i, d := range downs {
		ups[i] = d.release()
	}
	return ups
}

func wantErr(t *testing.T, err error, substrs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want an error containing %q", substrs)
	}
	for _, s := range substrs {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("err = %q, want it to contain %q", err, s)
		}
	}
}

func TestTapAtomic(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t)
	if err := Tap(context.Background(), f, st, 0); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(cat(st.Down, st.Up))
	f.assertNoneHeld()
}

func TestTapAtomicPartialDown(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	d := st.Down
	tests := []struct {
		name   string
		accept []int
		want   [][]KeyEvent
		errs   []string
	}{
		{"none down", []int{0}, [][]KeyEvent{cat(st.Down, st.Up)}, []string{"injected 0 of 6"}},
		{"one down", []int{1}, [][]KeyEvent{cat(st.Down, st.Up), rel(d[0])}, []string{"injected 1 of 6"}},
		{"two down", []int{2}, [][]KeyEvent{cat(st.Down, st.Up), rel(d[1], d[0])}, []string{"injected 2 of 6"}},
		// The release itself is partial: the rest is retried once.
		{"release retried", []int{2, 1}, [][]KeyEvent{cat(st.Down, st.Up), rel(d[1], d[0]), rel(d[0])}, []string{"injected 2 of 6", "injected 1 of 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.accept...)
			err := Tap(context.Background(), f, st, 0)
			wantErr(t, err, tt.errs...)
			if !errors.Is(err, errFake) {
				t.Errorf("err does not wrap the sender error: %v", err)
			}
			f.assertCalls(tt.want...)
			f.assertNoneHeld()
		})
	}
}

func TestTapAtomicPartialUp(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t, 4) // all presses and the release of Z
	err := Tap(context.Background(), f, st, 0)
	wantErr(t, err, "injected 4 of 6")
	f.assertCalls(cat(st.Down, st.Up), st.Up[1:])
	f.assertNoneHeld()
}

func TestTapAtomicReleaseFailsTwice(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t, 4, 0)
	err := Tap(context.Background(), f, st, 0)
	wantErr(t, err, "injected 4 of 6", "releasing Shift, Ctrl failed twice")
	f.assertCalls(cat(st.Down, st.Up), st.Up[1:])
	if got := len(f.heldKeys()); got != 2 {
		t.Errorf("%d keys held, want the 2 that could not be released", got)
	}
}

func TestTapAtomicPartialReleaseFailsTwice(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	d := st.Down
	f := newFake(t, 2, 1, 0)
	err := Tap(context.Background(), f, st, 0)
	wantErr(t, err, "injected 2 of 6", "injected 1 of 2", "releasing Ctrl failed twice")
	f.assertCalls(cat(st.Down, st.Up), rel(d[1], d[0]), rel(d[0]))
}

func TestTapHold(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t)
	const hold = 30 * time.Millisecond
	if err := Tap(context.Background(), f, st, hold); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(st.Down, st.Up)
	if gap := f.callTime[1].Sub(f.callTime[0]); gap < hold {
		t.Errorf("released after %v, want at least %v", gap, hold)
	}
	f.assertNoneHeld()
}

func TestTapHoldPartialDown(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	d := st.Down
	tests := []struct {
		name     string
		accept   []int
		want     [][]KeyEvent
		errs     []string
		leftHeld int
	}{
		{"none down", []int{0}, [][]KeyEvent{st.Down}, []string{"injected 0 of 3"}, 0},
		{"two down", []int{2}, [][]KeyEvent{st.Down, rel(d[1], d[0])}, []string{"injected 2 of 3"}, 0},
		{"release retried", []int{2, 1}, [][]KeyEvent{st.Down, rel(d[1], d[0]), rel(d[0])}, []string{"injected 2 of 3", "injected 1 of 2"}, 0},
		{"release fails twice", []int{1, 0, 0}, [][]KeyEvent{st.Down, rel(d[0]), rel(d[0])}, []string{"injected 1 of 3", "releasing Ctrl failed twice"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.accept...)
			start := time.Now()
			err := Tap(context.Background(), f, st, time.Hour)
			wantErr(t, err, tt.errs...)
			if time.Since(start) > 10*time.Second {
				t.Error("Tap waited for the hold after a failed press")
			}
			f.assertCalls(tt.want...)
			if got := len(f.heldKeys()); got != tt.leftHeld {
				t.Errorf("%d keys held, want %d", got, tt.leftHeld)
			}
		})
	}
}

func TestTapHoldPartialUp(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t, 3, 1)
	err := Tap(context.Background(), f, st, time.Millisecond)
	wantErr(t, err, "injected 1 of 3")
	f.assertCalls(st.Down, st.Up, st.Up[1:])
	f.assertNoneHeld()
}

func TestTapHoldReleaseFailsTwice(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t, 3, 1, 1)
	err := Tap(context.Background(), f, st, time.Millisecond)
	wantErr(t, err, "injected 1 of 3", "releasing Ctrl failed twice")
	f.assertCalls(st.Down, st.Up, st.Up[1:])
	if got := len(f.heldKeys()); got != 1 {
		t.Errorf("%d keys held, want 1", got)
	}
}

func TestTapHoldCanceled(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	err := Tap(ctx, f, st, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("Tap did not stop the hold on cancel")
	}
	f.assertCalls(st.Down, st.Up)
	f.assertNoneHeld()
}

func TestTapHoldCanceledPartialUp(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	f := newFake(t, 3, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := Tap(ctx, f, st, time.Hour)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	wantErr(t, err, "injected 2 of 3")
	f.assertCalls(st.Down, st.Up, st.Up[2:])
	f.assertNoneHeld()
}

func TestTapContextDone(t *testing.T) {
	st := stroke(t, "Ctrl+Z")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, hold := range []time.Duration{0, time.Second} {
		f := newFake(t)
		if err := Tap(ctx, f, st, hold); !errors.Is(err, context.Canceled) {
			t.Errorf("hold %v: err = %v, want context.Canceled", hold, err)
		}
		f.assertCalls()
	}
}

func TestTapEmpty(t *testing.T) {
	for _, hold := range []time.Duration{0, time.Second} {
		f := newFake(t)
		if err := Tap(context.Background(), f, Stroke{}, hold); err != nil {
			t.Errorf("hold %v: %v", hold, err)
		}
		f.assertCalls()
	}
}

// TestTapWarnings checks that an error that comes with a full count is
// returned, and that Tap otherwise goes on as if the batch had gone out
// cleanly.
func TestTapWarnings(t *testing.T) {
	st := stroke(t, "Ctrl+Shift+Z")
	d := st.Down
	warnA := errors.New("warning A")
	warnB := errors.New("warning B")
	tests := []struct {
		name   string
		hold   time.Duration
		accept []int
		warn   []error
		want   [][]KeyEvent
		errs   []error
	}{
		{"atomic", 0, nil, []error{warnA}, [][]KeyEvent{cat(st.Down, st.Up)}, []error{warnA}},
		{"atomic release", 0, []int{2}, []error{nil, warnA}, [][]KeyEvent{cat(st.Down, st.Up), rel(d[1], d[0])}, []error{errFake, warnA}},
		{"atomic retry", 0, []int{4}, []error{nil, warnA}, [][]KeyEvent{cat(st.Down, st.Up), st.Up[1:]}, []error{errFake, warnA}},
		{"hold down", time.Millisecond, nil, []error{warnA}, [][]KeyEvent{st.Down, st.Up}, []error{warnA}},
		{"hold up", time.Millisecond, nil, []error{nil, warnB}, [][]KeyEvent{st.Down, st.Up}, []error{warnB}},
		{"hold both", time.Millisecond, nil, []error{warnA, warnB}, [][]KeyEvent{st.Down, st.Up}, []error{warnA, warnB}},
		{"hold down then partial up", time.Millisecond, []int{3, 1}, []error{warnA, nil, warnB}, [][]KeyEvent{st.Down, st.Up, st.Up[1:]}, []error{warnA, errFake, warnB}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.accept...)
			f.warn = tt.warn
			err := Tap(context.Background(), f, st, tt.hold)
			for _, want := range tt.errs {
				if !errors.Is(err, want) {
					t.Errorf("err = %v, want it to wrap %v", err, want)
				}
			}
			f.assertCalls(tt.want...)
			f.assertNoneHeld()
		})
	}
}

// oddSender returns a fixed count and error, whatever it is given.
type oddSender struct {
	n     int
	err   error
	calls int
}

func (s *oddSender) Send([]KeyEvent) (int, error) {
	s.calls++
	return s.n, s.err
}

func TestTapBadCounts(t *testing.T) {
	st := stroke(t, "Ctrl+Z")
	// A count above the batch size means everything went out.
	s := &oddSender{n: 100}
	if err := Tap(context.Background(), s, st, 0); err != nil || s.calls != 1 {
		t.Errorf("count 100: err %v, %d calls", err, s.calls)
	}
	// A negative count means nothing went out.
	s = &oddSender{n: -1, err: errFake}
	wantErr(t, Tap(context.Background(), s, st, 0), "injected 0 of 4", "fake: blocked")
	if s.calls != 1 {
		t.Errorf("negative count: %d calls, want 1", s.calls)
	}
	// A short count without an error still fails.
	s = &oddSender{n: 0}
	wantErr(t, Tap(context.Background(), s, st, 0), "injected 0 of 4", "fewer events than given")
	if s.calls != 1 {
		t.Errorf("short count: %d calls, want 1", s.calls)
	}
}
