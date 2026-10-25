package output_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

const ms = time.Millisecond

var (
	ownerA = output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1}
	ownerB = output.Owner{Slot: 1, Gen: 1, Source: output.SourceDecl, Cycle: 2}
	ownerS = output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript}

	ownerNames = map[output.Owner]string{ownerA: "A", ownerB: "B", ownerS: "S"}

	errBlocked = errors.New("test: blocked")
	errWarn    = errors.New("test: warning")
)

// tsim is a simtest.Sim with shorthands that fail the test on errors.
type tsim struct {
	*simtest.Sim
	t *testing.T
}

func newSim(t *testing.T, opts ...simtest.Option) tsim {
	return tsim{simtest.New(t, opts...), t}
}

// withRetry sets the Arbiter's RetryInterval.
func withRetry(d time.Duration) simtest.Option {
	return simtest.WithArbiterOptions(output.ArbiterOptions{RetryInterval: d})
}

func (s tsim) ok(op string, o output.Owner, spec string, err error) {
	s.t.Helper()
	if err != nil {
		s.t.Fatalf("%s(%v, %s): %v", op, o, spec, err)
	}
}

func (s tsim) down(o output.Owner, spec string) {
	s.t.Helper()
	s.ok("Down", o, spec, s.Arb.Down(o, keys.MustParse(spec)))
}

func (s tsim) up(o output.Owner, spec string) {
	s.t.Helper()
	s.ok("Up", o, spec, s.Arb.Up(o, keys.MustParse(spec)))
}

func (s tsim) tap(o output.Owner, spec string, hold time.Duration) {
	s.t.Helper()
	s.ok("Tap", o, spec, s.Arb.Tap(o, keys.MustParse(spec), hold))
}

func (s tsim) repeat(o output.Owner, spec string) {
	s.t.Helper()
	s.ok("Repeat", o, spec, s.Arb.Repeat(o, keys.MustParse(spec)))
}

func TestArbiterMinPress(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.down(ownerA, "F13")
	s.Clock.Set(clock.Time(5 * ms))
	s.up(ownerA, "F13")
	if got := s.Output(); strings.Contains(got, "-F13") {
		t.Fatalf("release sent too early: %s", got)
	}
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(20*ms) {
		t.Fatalf("NextDeadline = %v, %v; want 20ms", d, ok)
	}
	s.Clock.Set(clock.Time(20 * ms))
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Output(), "-F13") {
		t.Fatalf("release missing: %s", s.Output())
	}
	if got := s.Arb.Stats().Retries; got != 0 {
		t.Fatalf("minimum-press release counted as retry: %d", got)
	}
}

func TestArbiterMinPressTap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.tap(ownerA, "F13", 0)
	if got := s.Output(); got != "+F13@0" {
		t.Fatalf("tap output = %s; want only the press before the minimum duration", got)
	}
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(20*ms) {
		t.Fatalf("NextDeadline = %v, %v; want 20ms", d, ok)
	}
	s.Clock.Set(clock.Time(20 * ms))
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	if got := s.Output(); got != "+F13@0 -F13@20" {
		t.Fatalf("tap after minimum = %s; want +F13 -F13", got)
	}
}

func TestArbiterMinPressQueuesOverlappingTaps(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.tap(ownerA, "F13", 0)
	s.Clock.Set(clock.Time(5 * ms))
	s.tap(ownerB, "F13", 0)
	if got := s.Output(); got != "+F13@0" {
		t.Fatalf("overlapping taps = %s; want one continuous press", got)
	}
	s.Clock.Set(clock.Time(20 * ms))
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	if got := s.Output(); got != "+F13@0 -F13@20 +F13@20 -F13@40" {
		t.Fatalf("queued tap release = %s; want two complete taps", got)
	}
}

// expectHeld compares Arbiter.Held with want, written as "LCtrl(A B)
// Z(pending)": each key with the names of its owners, or "pending".
func (s tsim) expectHeld(want string) {
	s.t.Helper()
	if got := heldString(s.Arb.Held()); got != want {
		s.t.Errorf("Held() = %q, want %q", got, want)
	}
}

func heldString(held []output.HeldKey) string {
	var parts []string
	for _, h := range held {
		var who []string
		for _, o := range h.Owners {
			who = append(who, ownerName(o))
		}
		if h.Pending {
			who = append(who, "pending")
		}
		parts = append(parts, keyName(h.Press)+"("+strings.Join(who, " ")+")")
	}
	return strings.Join(parts, " ")
}

func ownerName(o output.Owner) string {
	if n, ok := ownerNames[o]; ok {
		return n
	}
	return o.String()
}

// keyName names the key of e the way simtest traces do.
func keyName(e output.KeyEvent) string {
	return simtest.Record{Event: e}.Edge().Name
}

func (s tsim) expectStats(want output.ArbiterStats) {
	s.t.Helper()
	if got := s.Arb.Stats(); got != want {
		s.t.Errorf("Stats() = %+v, want %+v", got, want)
	}
}

// expectDeadline checks NextDeadline against want; a negative want means
// no deadline.
func (s tsim) expectDeadline(want time.Duration) {
	s.t.Helper()
	got, ok := s.Arb.NextDeadline()
	switch {
	case want < 0 && ok:
		s.t.Errorf("NextDeadline() = %v, want none", got)
	case want >= 0 && (!ok || got != clock.Time(want)):
		s.t.Errorf("NextDeadline() = %v, %v, want %v", got, ok, clock.Time(want))
	}
}

// expectBatches compares the Send calls from call index from on with
// want, written as "[+LCtrl +Z!] [-Z]": each call in brackets, with "!"
// after the events the call did not inject.
func (s tsim) expectBatches(from int, want string) {
	s.t.Helper()
	if got := batches(s.Out, from); got != want {
		s.t.Errorf("Send calls:\n got %s\nwant %s", got, want)
	}
}

func batches(r *simtest.Recorder, from int) string {
	var b strings.Builder
	last := -1
	for _, rec := range r.Records() {
		if rec.Batch < from {
			continue
		}
		switch {
		case last < 0:
			b.WriteByte('[')
		case rec.Batch != last:
			b.WriteString("] [")
		default:
			b.WriteByte(' ')
		}
		last = rec.Batch
		if rec.Event.Up {
			b.WriteByte('-')
		} else {
			b.WriteByte('+')
		}
		b.WriteString(keyName(rec.Event))
		if !rec.Injected {
			b.WriteByte('!')
		}
	}
	if last >= 0 {
		b.WriteByte(']')
	}
	return b.String()
}

// wantErr fails unless err is non-nil, wraps target (when not nil) and
// contains every one of subs.
func wantErr(t *testing.T, err, target error, subs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one wrapping %v", target)
	}
	if target != nil && !errors.Is(err, target) {
		t.Errorf("error %q does not wrap %v", err, target)
	}
	for _, sub := range subs {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not contain %q", err, sub)
		}
	}
}

// failRelease makes the next release batch lose its last event twice, so
// that key's release is left pending. It needs a hold whose release
// batch has at least two events if keep > 0.
func failRelease(r *simtest.Recorder, keep int) {
	r.FailNext(keep, errBlocked)
	r.FailNext(0, errBlocked)
}

func TestArbiterDownUp(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Z")
	s.ExpectOutput("+Z@0")
	s.expectHeld("Z(A)")
	s.Advance(10 * ms)
	s.up(ownerA, "Z")
	s.ExpectOutput("-Z@10")
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{})
}

// TestArbiterChordOrder checks OUT-05: modifiers go down first in
// canonical order and come up last, each direction in one batch.
func TestArbiterChordOrder(t *testing.T) {
	tests := []struct {
		chord   string
		batches string
	}{
		{"Ctrl+Shift+Z", "[+LCtrl +LShift +Z] [-Z -LShift -LCtrl]"},
		{"Win+Alt+Shift+Ctrl+F13", "[+LCtrl +LShift +LAlt +LWin +F13] [-F13 -LWin -LAlt -LShift -LCtrl]"},
		{"RCtrl+Alt+Pause", "[+RCtrl +LAlt +Pause] [-Pause -LAlt -RCtrl]"},
		{"Ctrl+Shift", "[+LCtrl +LShift] [-LShift -LCtrl]"},
		{"Plus", "[+LShift +Plus] [-Plus -LShift]"},
		{"RShift+Plus", "[+RShift +Plus] [-Plus -RShift]"},
		{"Ctrl+Num1", "[+LCtrl +Num1] [-Num1 -LCtrl]"},
	}
	for _, tt := range tests {
		t.Run(tt.chord, func(t *testing.T) {
			c := keys.MustParse(tt.chord)
			s := newSim(t)
			if err := s.Arb.Down(ownerA, c); err != nil {
				t.Fatal(err)
			}
			s.Advance(20 * ms)
			if err := s.Arb.Up(ownerA, c); err != nil {
				t.Fatal(err)
			}
			s.expectBatches(0, tt.batches)
			for _, rec := range s.Out.Records() {
				want := clock.Time(0)
				if rec.Event.Up {
					want = clock.Time(20 * ms)
				}
				if rec.T != want {
					t.Errorf("%v sent at %v, want %v", rec.Event, rec.T, want)
				}
			}
			s.ClearOutput()
		})
	}
}

// TestArbiterSided checks that generic modifiers are their left keys:
// "Ctrl" and "LCtrl" are one chord, "RCtrl" another.
func TestArbiterSided(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl")
	s.up(ownerA, "LCtrl")
	s.ExpectOutput("+LCtrl@0 -LCtrl@0")

	s.down(ownerA, "LShift+Z")
	s.down(ownerA, "Shift+Z") // the same chord: a duplicate
	s.up(ownerA, "Shift+Z")
	s.ExpectOutput("+LShift@0 +Z@0 -Z@0 -LShift@0")

	s.down(ownerA, "Ctrl+Shift")
	s.up(ownerA, "LShift+LCtrl")
	s.ExpectOutput("+LCtrl@0 +LShift@0 -LShift@0 -LCtrl@0")

	s.down(ownerA, "RCtrl+Z")
	s.up(ownerA, "Ctrl+Z") // not held
	s.ExpectOutput("+RCtrl@0 +Z@0")
	s.expectHeld("RCtrl(A) Z(A)")
	s.up(ownerA, "RCtrl+Z")
	s.ExpectOutput("-Z@0 -RCtrl@0")
	s.expectStats(output.ArbiterStats{DuplicateDowns: 1, UnmatchedUps: 1})
}

// TestArbiterSharedModifier: a key held by several owners goes up with
// its last reference, and only keys changing state get events.
func TestArbiterSharedModifier(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl")
	s.down(ownerB, "Ctrl+Z")
	s.ExpectOutput("+LCtrl@0 +Z@0")
	s.expectHeld("LCtrl(A B) Z(B)")
	s.Advance(10 * ms)
	s.up(ownerB, "Ctrl+Z")
	s.ExpectOutput("-Z@10")
	s.expectHeld("LCtrl(A)")
	s.up(ownerA, "Ctrl")
	s.ExpectOutput("-LCtrl@10")

	// The other way round: the chord goes down first.
	s.down(ownerB, "Ctrl+Z")
	s.down(ownerA, "LCtrl")
	s.up(ownerB, "Ctrl+Z")
	s.ExpectOutput("+LCtrl@10 +Z@10 -Z@10")
	s.up(ownerA, "LCtrl")
	s.ExpectOutput("-LCtrl@10")
}

// TestArbiterSharedKey: distinct chords on one physical key share it,
// and the release reuses the press that put it down.
func TestArbiterSharedKey(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Z")
	s.down(ownerB, "KeyZ")
	s.down(ownerS, "sc:0x2C")
	s.ExpectOutput("+Z@0")
	s.expectHeld("Z(A B S)")
	s.up(ownerA, "Z")
	s.up(ownerS, "sc:0x2C")
	s.ExpectOutput("")
	s.up(ownerB, "KeyZ")
	s.ExpectOutput("-Z@0")

	// A raw scan code is the modifier it names.
	s.down(ownerA, "sc:0x1D")
	s.down(ownerB, "Ctrl+X")
	s.ExpectOutput("+sc:0x1D@0 +X@0")
	s.expectHeld("sc:0x1D(A B) X(B)")
	s.up(ownerA, "sc:0x1D")
	s.ExpectOutput("")
	s.up(ownerB, "Ctrl+X")
	s.ExpectOutput("-X@0 -sc:0x1D@0")

	// Plus is Shift+= on the US layout.
	s.down(ownerA, "=")
	s.down(ownerB, "Plus")
	s.ExpectOutput("+=@0 +LShift@0")
	s.up(ownerA, "=")
	s.ExpectOutput("")
	s.up(ownerB, "Plus")
	s.ExpectOutput("-=@0 -LShift@0")
}

// TestArbiterOverlap: one owner holding overlapping chords.
func TestArbiterOverlap(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.down(ownerA, "Ctrl+X")
	s.down(ownerA, "Z")
	s.ExpectOutput("+LCtrl@0 +Z@0 +X@0")
	s.expectHeld("LCtrl(A) Z(A) X(A)")
	s.up(ownerA, "Ctrl+Z")
	s.ExpectOutput("")
	s.up(ownerA, "Ctrl+X")
	s.ExpectOutput("-X@0 -LCtrl@0")
	s.up(ownerA, "Z")
	s.ExpectOutput("-Z@0")
}

func TestArbiterNoOps(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Z")
	s.down(ownerA, "Z")
	s.ExpectOutput("+Z@0")
	s.up(ownerB, "Z")     // B does not hold Z
	s.repeat(ownerB, "Z") // nor can it repeat it
	s.tap(ownerB, "X", 10*ms)
	s.repeat(ownerB, "X") // a tap is not a hold
	s.ExpectOutput("+X@0")
	s.up(ownerB, "X") // nor can it be released by Up
	s.up(ownerA, "Z")
	s.up(ownerA, "Z")
	s.repeat(ownerA, "Z")
	s.ExpectOutput("-Z@0")
	s.Settle()
	s.ExpectOutput("-X@10")
	s.expectStats(output.ArbiterStats{DuplicateDowns: 1, UnmatchedUps: 3, InvalidRepeats: 3})
}

// TestArbiterInvalid: commands with an invalid owner or chord fail, send
// nothing and do not even retry a pending release.
func TestArbiterInvalid(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	failRelease(s.Out, 1)
	wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errBlocked, "releasing LCtrl failed twice")
	s.expectHeld("LCtrl(pending)")
	calls := s.Out.Calls()
	stats := s.Arb.Stats()

	z := keys.MustParse("Z")
	var zero keys.Chord
	for _, o := range []output.Owner{{}, {Slot: 1, Gen: 1, Source: output.SourceScript + 1}} {
		wantErr(t, s.Arb.Down(o, z), nil, "invalid owner")
		wantErr(t, s.Arb.Up(o, z), nil, "invalid owner")
		wantErr(t, s.Arb.Tap(o, z, 0), nil, "invalid owner")
		wantErr(t, s.Arb.Tap(o, z, 10*ms), nil, "invalid owner")
		wantErr(t, s.Arb.Repeat(o, z), nil, "invalid owner")
		wantErr(t, s.Arb.ReleaseOwner(o), nil, "invalid owner")
	}
	wantErr(t, s.Arb.Down(ownerA, zero), nil, "invalid chord")
	wantErr(t, s.Arb.Up(ownerA, zero), nil, "invalid chord")
	wantErr(t, s.Arb.Tap(ownerA, zero, 0), nil, "invalid chord")
	wantErr(t, s.Arb.Repeat(ownerA, zero), nil, "invalid chord")
	wantErr(t, s.Arb.ReleaseOwners(nil), nil, "nil")

	if got := s.Out.Calls(); got != calls {
		t.Errorf("invalid commands made %d Send calls", got-calls)
	}
	s.expectHeld("LCtrl(pending)")
	s.expectStats(stats)
	s.Settle()
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@0 -LCtrl@100")
}

func TestArbiterResolve(t *testing.T) {
	errLayout := errors.New("test: no layout")
	var (
		asked []string
		scan  uint8 = 0x2C
		fail  bool
	)
	// resolve maps Z to scan, by scan code only so traces name the code,
	// X to an error, Y to nothing, and anything else as ChordStroke does.
	resolve := func(c keys.Chord) (output.Stroke, error) {
		asked = append(asked, c.String())
		switch {
		case fail:
			return output.Stroke{}, errLayout
		case c.String() == "Z":
			e := output.KeyEvent{Scan: scan}
			return output.Stroke{Down: []output.KeyEvent{e}}, nil
		case c.String() == "X":
			return output.Stroke{}, errLayout
		case c.String() == "Y":
			return output.Stroke{}, nil
		case c.String() == "LCtrl+Q":
			// Up flags and repeated keys in Down are ignored.
			lctrl, _ := output.KeyEventFor(keys.MustParse("LCtrl").Key(), true)
			q, _ := output.KeyEventFor(keys.MustParse("Q").Key(), false)
			return output.Stroke{Down: []output.KeyEvent{lctrl, lctrl, q, lctrl}}, nil
		}
		return output.ChordStroke(c)
	}
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{Resolve: resolve}))

	// Resolve sees the sided chord.
	s.down(ownerA, "Ctrl+Shift+V")
	s.up(ownerA, "Ctrl+Shift+V")
	if want := []string{"LCtrl+LShift+V"}; !slices.Equal(asked, want) {
		t.Errorf("Resolve called with %q, want %q (Up must not resolve)", asked, want)
	}
	s.ClearOutput()

	for _, tt := range []struct {
		spec string
		err  error
		msg  string
	}{
		{"X", errLayout, "resolving X"},
		{"Y", nil, "Y resolves to no keys"},
	} {
		c := keys.MustParse(tt.spec)
		wantErr(t, s.Arb.Down(ownerA, c), tt.err, tt.msg)
		wantErr(t, s.Arb.Tap(ownerA, c, 0), tt.err, tt.msg)
		wantErr(t, s.Arb.Tap(ownerA, c, 10*ms), tt.err, tt.msg)
		s.up(ownerA, tt.spec) // nothing is held
	}
	s.ExpectOutput("")
	s.expectHeld("")
	s.expectDeadline(-1)

	s.down(ownerA, "Ctrl+Q")
	s.repeat(ownerA, "Ctrl+Q")
	s.ExpectOutput("+LCtrl@0 +Q@0 +Q@0")
	s.up(ownerA, "Ctrl+Q")
	s.ExpectOutput("-Q@0 -LCtrl@0")
	s.tap(ownerA, "Ctrl+Q", 0)
	s.ExpectOutput("+LCtrl@0 +Q@0 -Q@0 -LCtrl@0")

	// A key goes up with the code it went down with, even when the chord
	// now resolves elsewhere or not at all.
	s.down(ownerA, "Z")
	scan = 0x15
	s.down(ownerB, "Z")
	s.ExpectOutput("+sc:0x2C@0 +sc:0x15@0")
	fail = true
	s.repeat(ownerA, "Z")
	s.up(ownerA, "Z")
	s.up(ownerB, "Z")
	s.ExpectOutput("+sc:0x2C@0 -sc:0x2C@0 -sc:0x15@0")
}

func TestArbiterTapNow(t *testing.T) {
	for _, hold := range []time.Duration{0, -5 * ms} {
		s := newSim(t)
		s.tap(ownerA, "Ctrl+Z", hold)
		s.expectBatches(0, "[+LCtrl +Z -Z -LCtrl]")
		s.ExpectOutput("+LCtrl@0 +Z@0 -Z@0 -LCtrl@0")
		s.expectHeld("")
		s.expectDeadline(-1)
	}
}

func TestArbiterTapHold(t *testing.T) {
	s := newSim(t)
	s.Advance(5 * ms)
	s.tap(ownerA, "Ctrl+Z", 20*ms)
	s.ExpectOutput("+LCtrl@5 +Z@5")
	s.expectHeld("LCtrl(A) Z(A)")
	s.expectDeadline(25 * ms)
	s.Advance(19 * ms)
	if err := s.Arb.Advance(); err != nil { // not due yet
		t.Fatal(err)
	}
	s.ExpectOutput("")
	s.Advance(1 * ms)
	s.ExpectOutput("-Z@25 -LCtrl@25")
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectBatches(0, "[+LCtrl +Z] [-Z -LCtrl]")
}

// TestArbiterTapOnHeld: a tap only adds a press to a main key that is
// already down, and nothing to a modifier that is.
func TestArbiterTapOnHeld(t *testing.T) {
	for _, hold := range []time.Duration{0, 20 * ms} {
		s := newSim(t)
		s.down(ownerB, "Z")
		s.tap(ownerA, "Z", hold)
		s.ExpectOutput("+Z@0 +Z@0")
		s.expectHeld("Z(B)")
		s.expectDeadline(-1) // the tap holds nothing
		s.expectStats(output.ArbiterStats{TapsOnHeld: 1})
		s.Advance(10 * ms)
		s.up(ownerB, "Z")
		s.ExpectOutput("-Z@10")
	}

	s := newSim(t)
	s.down(ownerB, "Z")
	s.tap(ownerA, "Ctrl+Z", 20*ms)
	s.expectBatches(0, "[+Z] [+LCtrl +Z]")
	s.expectHeld("Z(B) LCtrl(A)")
	s.Advance(20 * ms)
	s.ExpectOutput("+Z@0 +LCtrl@0 +Z@0 -LCtrl@20")
	s.up(ownerB, "Z")
	s.ExpectOutput("-Z@20")

	s.down(ownerB, "Ctrl")
	s.tap(ownerA, "Ctrl+Z", 0)
	s.up(ownerB, "Ctrl")
	s.ExpectOutput("+LCtrl@20 +Z@20 -Z@20 -LCtrl@20")
	s.expectStats(output.ArbiterStats{TapsOnHeld: 1})
}

// TestArbiterTapThenDown: a pending tap and a hold share a key; it goes
// up only when both are done.
func TestArbiterTapThenDown(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "Z", 20*ms)
	s.Advance(10 * ms)
	s.down(ownerB, "Z")
	s.expectHeld("Z(A B)")
	s.Advance(10 * ms)
	s.ExpectOutput("+Z@0")
	s.expectHeld("Z(B)")
	s.Advance(10 * ms)
	s.up(ownerB, "Z")
	s.ExpectOutput("-Z@30")

	// The hold ends first.
	s.tap(ownerA, "Z", 20*ms)
	s.Advance(5 * ms)
	s.down(ownerB, "KeyZ")
	s.Advance(5 * ms)
	s.up(ownerB, "KeyZ")
	s.ExpectOutput("+Z@30")
	s.Advance(10 * ms)
	s.ExpectOutput("-Z@50")

	// The same owner holds the key its tap pressed.
	s.tap(ownerA, "Z", 20*ms)
	s.Advance(5 * ms)
	s.down(ownerA, "Z")
	s.Advance(20 * ms)
	s.ExpectOutput("+Z@50")
	s.expectHeld("Z(A)")
	s.up(ownerA, "Z")
	s.ExpectOutput("-Z@75")
}

// TestArbiterTapReleased: releasing a tap's owner ends the tap at once.
func TestArbiterTapReleased(t *testing.T) {
	tests := []struct {
		name    string
		release func(*output.Arbiter) error
		all     bool // it ends B's tap too
	}{
		{"ReleaseOwner", func(a *output.Arbiter) error { return a.ReleaseOwner(ownerA) }, false},
		{"ReleaseOwners", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Slot == 0 })
		}, false},
		{"ReleaseAll", (*output.Arbiter).ReleaseAll, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t)
			s.tap(ownerA, "Ctrl+Z", 50*ms)
			s.tap(ownerB, "X", 50*ms)
			s.Advance(10 * ms)
			if err := tt.release(s.Arb); err != nil {
				t.Fatal(err)
			}
			if tt.all {
				s.ExpectOutput("+LCtrl@0 +Z@0 +X@0 -X@10 -Z@10 -LCtrl@10")
				s.expectDeadline(-1)
				return
			}
			s.ExpectOutput("+LCtrl@0 +Z@0 +X@0 -Z@10 -LCtrl@10")
			s.expectHeld("X(B)")
			s.expectDeadline(50 * ms)
			s.Advance(50 * ms)
			s.ExpectOutput("-X@50")
		})
	}
}

// TestArbiterCycleEnd: an owner ends a press cycle with Up of its holds,
// and its pending taps still run their hold. ReleaseOwner is the cancel
// path that cuts them short (TestArbiterTapReleased).
func TestArbiterCycleEnd(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "F13", 20*ms)
	s.down(ownerA, "F14")
	s.Advance(5 * ms)
	s.up(ownerA, "F14")
	s.ExpectOutput("+F13@0 +F14@0 -F14@5")
	s.expectHeld("F13(A)")
	s.expectDeadline(20 * ms)
	s.Advance(15 * ms)
	s.ExpectOutput("-F13@20")
}

// TestArbiterTapOrder: taps end in deadline order, ties in the order they
// were made, each in its own batch.
func TestArbiterTapOrder(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "X", 30*ms)
	s.tap(ownerB, "Z", 10*ms)
	s.tap(ownerS, "Q", 30*ms)
	s.expectDeadline(10 * ms)
	s.Advance(40 * ms)
	s.ExpectOutput("+X@0 +Z@0 +Q@0 -Z@10 -X@30 -Q@30")
	s.expectBatches(3, "[-Z] [-X] [-Q]")

	// One Advance well past all deadlines.
	s.tap(ownerA, "X", 30*ms)
	s.tap(ownerB, "Z", 10*ms)
	s.tap(ownerS, "Q", 30*ms)
	s.ClearOutput()
	s.Clock.Advance(40 * ms)
	if err := s.Arb.Advance(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-Z@80 -X@80 -Q@80")

	// Overlapping taps share a modifier.
	s.tap(ownerA, "Ctrl+Z", 20*ms)
	s.Advance(10 * ms)
	s.tap(ownerB, "Ctrl+X", 20*ms)
	s.expectHeld("LCtrl(A) Z(A) X(B)")
	s.Advance(20 * ms)
	s.ExpectOutput("+LCtrl@80 +Z@80 +X@90 -Z@100 -LCtrl@100 -X@110")
}

func TestArbiterRepeat(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.Advance(5 * ms)
	s.repeat(ownerA, "Ctrl+Z")
	s.repeat(ownerA, "LCtrl+Z")
	s.ExpectOutput("+LCtrl@0 +Z@0 +Z@5 +Z@5")
	s.expectBatches(1, "[+Z] [+Z]")
	s.expectHeld("LCtrl(A) Z(A)")
	s.up(ownerA, "Ctrl+Z")
	s.repeat(ownerA, "Ctrl+Z")
	s.ExpectOutput("-Z@5 -LCtrl@5")

	// The main key of a modifier chord is its last modifier.
	s.down(ownerA, "Ctrl+Shift")
	s.repeat(ownerA, "Ctrl+Shift")
	s.up(ownerA, "Ctrl+Shift")
	s.ExpectOutput("+LCtrl@5 +LShift@5 +LShift@5 -LShift@5 -LCtrl@5")

	// A repeat presses the key as whoever put it down did.
	s.down(ownerB, "KeyZ")
	s.down(ownerA, "Z")
	s.repeat(ownerA, "Z")
	s.up(ownerA, "Z")
	s.up(ownerB, "KeyZ")
	s.ExpectOutput("+KeyZ@5 +KeyZ@5 -KeyZ@5")
	s.expectStats(output.ArbiterStats{InvalidRepeats: 1})
}

func TestArbiterReleaseOwners(t *testing.T) {
	var (
		p1g1  = output.Owner{Slot: 1, Gen: 1, Source: output.SourceDecl, Cycle: 3}
		p1g1s = output.Owner{Slot: 1, Gen: 1, Source: output.SourceScript}
		p1g2  = output.Owner{Slot: 1, Gen: 2, Source: output.SourceDecl, Cycle: 4}
		p0    = output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 5}
	)
	s := newSim(t)
	s.down(p1g1, "Ctrl+Z")
	s.down(p1g1s, "X")
	s.tap(p1g1, "F1", 50*ms)
	s.down(p1g2, "Q")
	s.down(p0, "Z")
	s.ExpectOutput("+LCtrl@0 +Z@0 +X@0 +F1@0 +Q@0")
	calls := s.Out.Calls()
	if err := s.Arb.ReleaseOwners(func(o output.Owner) bool { return o.Slot == 1 && o.Gen == 1 }); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(calls, "[-F1 -X -LCtrl]")
	s.expectHeld("Z(0/1/decl/5) Q(1/2/decl/4)")
	s.expectDeadline(-1)

	// Nothing matches: no Send call.
	calls = s.Out.Calls()
	if err := s.Arb.ReleaseOwner(p1g1); err != nil {
		t.Fatal(err)
	}
	if err := s.Arb.ReleaseOwners(func(output.Owner) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if got := s.Out.Calls(); got != calls {
		t.Errorf("releasing no key made %d Send calls", got-calls)
	}

	if err := s.Arb.ReleaseOwner(p0); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-F1@0 -X@0 -LCtrl@0 -Z@0")
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-Q@0")
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("")
}

// TestArbiterReleaseAllOrder: non-modifiers first, then modifiers, each in
// reverse press order, all in one batch.
func TestArbiterReleaseAllOrder(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.down(ownerB, "Shift+X")
	s.tap(ownerS, "Alt+Q", 50*ms)
	s.down(ownerA, "Win")
	s.down(ownerB, "sc:0xE01D") // RCtrl by scan code: a modifier too
	s.down(ownerB, "Pause")
	s.Advance(10 * ms)
	calls := s.Out.Calls()
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(calls, "[-Pause -Q -X -Z -sc:0xE01D -LWin -LAlt -LShift -LCtrl]")
	s.ClearOutput()
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{}) // no retry: nothing was pending
}

func TestArbiterPartialPress(t *testing.T) {
	s := newSim(t)
	s.Out.FailNext(1, errBlocked)
	wantErr(t, s.Arb.Down(ownerA, keys.MustParse("Ctrl+Shift+Z")), errBlocked, "injected 1 of 3")
	s.ExpectOutput("+LCtrl@0")
	// Logical state follows the command: every key is released later,
	// the ones that never went down too.
	s.expectHeld("LCtrl(A) LShift(A) Z(A)")
	s.up(ownerA, "Ctrl+Shift+Z")
	s.ExpectOutput("-Z@0 -LShift@0 -LCtrl@0")
	s.expectStats(output.ArbiterStats{SendErrors: 1})

	// A tap whose presses failed in part.
	s.Out.FailNext(1, errBlocked)
	wantErr(t, s.Arb.Tap(ownerA, keys.MustParse("Ctrl+Z"), 20*ms), errBlocked, "injected 1 of 2")
	s.expectDeadline(20 * ms)
	s.Advance(20 * ms)
	s.ExpectOutput("+LCtrl@0 -Z@20 -LCtrl@20")

	// A failed extra press changes nothing.
	s.down(ownerB, "Z")
	s.Out.FailNext(0, errBlocked)
	wantErr(t, s.Arb.Tap(ownerA, keys.MustParse("Z"), 0), errBlocked, "injected 0 of 1")
	s.Out.FailNext(0, errBlocked)
	wantErr(t, s.Arb.Repeat(ownerB, keys.MustParse("Z")), errBlocked, "injected 0 of 1")
	s.expectHeld("Z(B)")
	s.up(ownerB, "Z")
	s.ExpectOutput("+Z@20 -Z@20")

	// A press that failed is released by ReleaseOwner as well.
	s.Out.FailNext(0, errBlocked)
	wantErr(t, s.Arb.Down(ownerA, keys.MustParse("X")), errBlocked, "injected 0 of 1")
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(s.Out.Calls()-2, "[+X!] [-X]")
	s.ClearOutput()
}

func TestArbiterPartialRelease(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	calls := s.Out.Calls()
	s.Out.FailNext(1, errBlocked)
	err := s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z"))
	wantErr(t, err, errBlocked, "injected 1 of 2")
	if strings.Contains(err.Error(), "failed twice") {
		t.Errorf("error %q, but the retry worked", err)
	}
	s.expectBatches(calls, "[-Z -LCtrl!] [-LCtrl]")
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@0 -LCtrl@0")
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{SendErrors: 1, Retries: 1})
}

// TestArbiterPendingRelease: a release that fails twice is retried on
// NextDeadline until it goes out.
func TestArbiterPendingRelease(t *testing.T) {
	s := newSim(t, withRetry(30*ms))
	s.down(ownerA, "Ctrl+Z")
	s.Advance(10 * ms)
	failRelease(s.Out, 1)
	wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errBlocked, "releasing LCtrl failed twice")
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@10")
	s.expectHeld("LCtrl(pending)")
	s.expectDeadline(40 * ms)
	s.expectStats(output.ArbiterStats{SendErrors: 2, Retries: 1, PendingReleases: 1})

	// The retry at 40 ms fails too and moves the deadline.
	s.Out.FailNext(0, errBlocked)
	s.Advance(30 * ms)
	errs := s.Errors()
	if len(errs) != 1 {
		t.Fatalf("Advance errors %q, want one", errs)
	}
	wantErr(t, errs[0], errBlocked, "releasing LCtrl failed")
	s.expectDeadline(70 * ms)
	s.Advance(30 * ms)
	s.ExpectOutput("-LCtrl@70")
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{SendErrors: 3, Retries: 3})
}

// TestArbiterPendingRetriedFirst: every command that is valid, even one
// that is a no-op, and Advance, first resend pending releases.
func TestArbiterPendingRetriedFirst(t *testing.T) {
	x := keys.MustParse("X")
	tests := []struct {
		name    string
		cmd     func(*output.Arbiter) error
		batches string
		noop    output.ArbiterStats // the no-op counter the command bumps
	}{
		{"Down", func(a *output.Arbiter) error { return a.Down(ownerB, x) }, "[-LCtrl] [+X]", output.ArbiterStats{}},
		{"Up", func(a *output.Arbiter) error { return a.Up(ownerB, x) }, "[-LCtrl]", output.ArbiterStats{UnmatchedUps: 1}},
		{"Tap", func(a *output.Arbiter) error { return a.Tap(ownerB, x, 0) }, "[-LCtrl] [+X -X]", output.ArbiterStats{}},
		{"TapHold", func(a *output.Arbiter) error { return a.Tap(ownerB, x, 10*ms) }, "[-LCtrl] [+X]", output.ArbiterStats{}},
		{"Repeat", func(a *output.Arbiter) error { return a.Repeat(ownerB, x) }, "[-LCtrl]", output.ArbiterStats{InvalidRepeats: 1}},
		{"Advance", (*output.Arbiter).Advance, "[-LCtrl]", output.ArbiterStats{}},
		{"ReleaseOwner", func(a *output.Arbiter) error { return a.ReleaseOwner(ownerB) }, "[-LCtrl]", output.ArbiterStats{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t, withRetry(30*ms))
			s.down(ownerA, "Ctrl+Z")
			failRelease(s.Out, 1)
			wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errBlocked)
			s.Advance(5 * ms)
			calls := s.Out.Calls()
			if err := tt.cmd(s.Arb); err != nil {
				t.Fatal(err)
			}
			s.expectBatches(calls, tt.batches)
			want := tt.noop
			want.SendErrors, want.Retries = 2, 2
			s.expectStats(want)
			if err := s.Arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			s.Settle()
			s.ClearOutput()
		})
	}
}

// TestArbiterPendingFlushed: ReleaseAll sends pending releases in its own
// batch, in release order.
func TestArbiterPendingFlushed(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.down(ownerS, "X")
	s.down(ownerB, "Shift+F1")
	failRelease(s.Out, 0)
	wantErr(t, s.Arb.Up(ownerB, keys.MustParse("Shift+F1")), errBlocked, "releasing F1, LShift failed twice")
	s.expectHeld("LCtrl(A) Z(A) X(S) LShift(pending) F1(pending)")
	s.expectStats(output.ArbiterStats{SendErrors: 2, Retries: 1, PendingReleases: 2})
	calls := s.Out.Calls()
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.expectBatches(calls, "[-F1 -X -Z -LShift -LCtrl]")
	s.ClearOutput()
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{SendErrors: 2, Retries: 2})
}

// TestArbiterPendingPressed: Down may replace a pending release, while a tap
// whose main key still awaits physical release must queue.
func TestArbiterPendingPressed(t *testing.T) {
	tests := []struct {
		name    string
		cmd     func(*output.Arbiter) error
		batches string
		held    string
		pending int
		queued  int
		cleanup func(tsim)
	}{
		{
			"Down", func(a *output.Arbiter) error { return a.Down(ownerB, keys.MustParse("Ctrl+X")) },
			"[-LCtrl!] [+LCtrl +X]", "LCtrl(B) X(B)", 0, 0,
			func(s tsim) { s.up(ownerB, "Ctrl+X") },
		},
		{
			"Tap", func(a *output.Arbiter) error { return a.Tap(ownerB, keys.MustParse("Ctrl+X"), 0) },
			"[-LCtrl!] [+LCtrl +X -X -LCtrl]", "", 0, 0,
			func(tsim) {},
		},
		{
			"TapHold", func(a *output.Arbiter) error { return a.Tap(ownerB, keys.MustParse("LCtrl"), 10*ms) },
			"[-LCtrl!]", "LCtrl(pending)", 1, 1,
			func(s tsim) { s.Settle() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t, withRetry(30*ms))
			s.down(ownerA, "Ctrl+Z")
			failRelease(s.Out, 1)
			wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errBlocked)
			s.expectHeld("LCtrl(pending)")
			// The retry before the command fails again. Only a tap whose
			// main key is still pending waits for the next successful up.
			calls := s.Out.Calls()
			s.Out.FailNext(0, errBlocked)
			wantErr(t, tt.cmd(s.Arb), errBlocked, "releasing LCtrl failed")
			s.expectBatches(calls, tt.batches)
			s.expectHeld(tt.held)
			if st := s.Arb.Stats(); st.PendingReleases != tt.pending {
				t.Errorf("Stats().PendingReleases = %d, want %d", st.PendingReleases, tt.pending)
			}
			if got := s.Arb.Queued(); got != tt.queued {
				t.Errorf("Queued() = %d, want %d", got, tt.queued)
			}
			if tt.held == "" {
				s.expectDeadline(-1)
			}
			tt.cleanup(s)
			s.ClearOutput()
		})
	}
}

// TestArbiterTapOnPendingMain: a tap whose main key's release failed twice
// (a ReleaseAll left Z and LCtrl pending) waits for a release that is
// injected (OUT-10), in a queue with one tap per main key. The resend that
// comes first in the tap's command decides: if it injects the release, the
// key is free and the tap starts at once. FuzzArbiter's model once treated
// pending keys as free (corpus 58c4f94561fbda16).
func TestArbiterTapOnPendingMain(t *testing.T) {
	pending := func(t *testing.T) tsim {
		s := newSim(t, withRetry(30*ms))
		s.down(ownerA, "Ctrl+Z")
		failRelease(s.Out, 0)
		wantErr(t, s.Arb.ReleaseAll(), errBlocked, "releasing Z, LCtrl failed twice")
		s.expectHeld("LCtrl(pending) Z(pending)")
		s.expectDeadline(30 * ms)
		s.expectStats(output.ArbiterStats{SendErrors: 2, Retries: 1, PendingReleases: 2})
		s.Advance(5 * ms)
		return s
	}

	t.Run("resend fails", func(t *testing.T) {
		s := pending(t)
		calls := s.Out.Calls()
		s.Out.FailNext(0, errBlocked)
		wantErr(t, s.Arb.Tap(ownerB, keys.MustParse("Z"), 0), errBlocked, "releasing Z, LCtrl failed again")
		s.expectBatches(calls, "[-Z! -LCtrl!]")
		s.expectHeld("LCtrl(pending) Z(pending)")
		if got := s.Arb.Queued(); got != 1 {
			t.Errorf("Queued() = %d, want the tap waiting", got)
		}
		s.expectDeadline(35 * ms)
		s.expectStats(output.ArbiterStats{SendErrors: 3, Retries: 2, QueuedTaps: 1, PendingReleases: 2})

		// A tap of the same main key merges into it, whatever its modifiers.
		s.Advance(5 * ms)
		s.Out.FailNext(0, errBlocked)
		wantErr(t, s.Arb.Tap(ownerA, keys.MustParse("Shift+Z"), 10*ms), errBlocked, "failed again")
		if got := s.Arb.Queued(); got != 1 {
			t.Errorf("Queued() = %d after the merge, want 1", got)
		}
		s.expectDeadline(40 * ms)
		s.expectStats(output.ArbiterStats{SendErrors: 4, Retries: 3, QueuedTaps: 1, MergedTaps: 1, PendingReleases: 2})

		// The release goes out at the retry; the waiting tap follows.
		calls = s.Out.Calls()
		s.Advance(30 * ms)
		s.expectBatches(calls, "[-Z -LCtrl] [+Z -Z]")
		s.expectHeld("")
		s.expectDeadline(-1)
		if got := s.Arb.Queued(); got != 0 {
			t.Errorf("Queued() = %d after the release, want 0", got)
		}
		s.expectStats(output.ArbiterStats{SendErrors: 4, Retries: 4, QueuedTaps: 1, MergedTaps: 1})
		s.ClearOutput()
	})

	t.Run("resend works", func(t *testing.T) {
		s := pending(t)
		calls := s.Out.Calls()
		s.tap(ownerB, "Z", 0)
		s.expectBatches(calls, "[-Z -LCtrl] [+Z -Z]")
		s.expectHeld("")
		s.expectDeadline(-1)
		if got := s.Arb.Queued(); got != 0 {
			t.Errorf("Queued() = %d, want the tap started", got)
		}
		s.expectStats(output.ArbiterStats{SendErrors: 2, Retries: 2})
		s.ClearOutput()
	})

	// A Down of the key resends first, too: the waiting tap starts before
	// the Down takes the key, so it presses the key itself and is not a tap
	// on a held key (the corpus entry 8ff0748752119d43).
	t.Run("down after the resend", func(t *testing.T) {
		s := pending(t)
		s.Out.FailNext(0, errBlocked)
		wantErr(t, s.Arb.Tap(ownerB, keys.MustParse("Z"), 0), errBlocked, "failed again")
		calls := s.Out.Calls()
		s.down(ownerA, "Z")
		s.expectBatches(calls, "[-Z -LCtrl] [+Z -Z] [+Z]")
		s.expectHeld("Z(A)")
		if got := s.Arb.Queued(); got != 0 {
			t.Errorf("Queued() = %d, want the tap started", got)
		}
		s.expectStats(output.ArbiterStats{SendErrors: 3, Retries: 3, QueuedTaps: 1})
		s.up(ownerA, "Z")
		s.expectHeld("")
		s.ClearOutput()
	})

	// The resend frees both keys, and the waiting tap of Ctrl+Z starts first
	// and holds LCtrl for 20 ms. The waiting tap of LCtrl starts after the
	// Down, on the key the Down holds (the corpus entry acb1ba7414f89e3b).
	t.Run("down after the resend, key taken", func(t *testing.T) {
		s := pending(t)
		for _, tap := range []struct {
			o    output.Owner
			spec string
			hold time.Duration
		}{{ownerB, "Ctrl+Z", 20 * ms}, {ownerA, "LCtrl", 0}} {
			s.Out.FailNext(0, errBlocked)
			wantErr(t, s.Arb.Tap(tap.o, keys.MustParse(tap.spec), tap.hold), errBlocked, "failed again")
		}
		if got := s.Arb.Queued(); got != 2 {
			t.Fatalf("Queued() = %d, want 2 taps waiting", got)
		}
		calls := s.Out.Calls()
		s.down(ownerA, "LCtrl")
		s.expectBatches(calls, "[-Z -LCtrl] [+LCtrl +Z] [+LCtrl]")
		s.expectStats(output.ArbiterStats{SendErrors: 4, Retries: 4, TapsOnHeld: 1, QueuedTaps: 2})
		s.up(ownerA, "LCtrl")
		s.Settle()
		s.expectHeld("")
		s.ClearOutput()
	})

	// The resend injects Z's release only. The waiting tap of Z starts, and
	// its tap of Ctrl+Z lets go of LCtrl at once (a hold of 0), which frees the
	// key of the waiting tap of LCtrl, first in the queue: it starts too, before
	// the Down takes the key, so it is no tap on a held key (the corpus entry
	// b75adb93763a17e6).
	t.Run("down after the resend, key freed by a tap", func(t *testing.T) {
		s := pending(t)
		for _, tap := range []struct {
			o    output.Owner
			spec string
		}{{ownerA, "LCtrl"}, {ownerB, "Ctrl+Z"}} {
			s.Out.FailNext(0, errBlocked)
			wantErr(t, s.Arb.Tap(tap.o, keys.MustParse(tap.spec), 0), errBlocked, "failed again")
		}
		if got := s.Arb.Queued(); got != 2 {
			t.Fatalf("Queued() = %d, want 2 taps waiting", got)
		}
		calls := s.Out.Calls()
		s.Out.FailNext(1, errBlocked)
		wantErr(t, s.Arb.Down(ownerA, keys.MustParse("LCtrl")), errBlocked, "releasing LCtrl failed again")
		s.expectBatches(calls, "[-Z -LCtrl!] [+LCtrl +Z -Z -LCtrl] [+LCtrl -LCtrl] [+LCtrl]")
		s.expectHeld("LCtrl(A)")
		if got := s.Arb.Queued(); got != 0 {
			t.Errorf("Queued() = %d, want both taps started", got)
		}
		s.expectStats(output.ArbiterStats{SendErrors: 5, Retries: 4, QueuedTaps: 2})
		s.up(ownerA, "LCtrl")
		s.Settle()
		s.expectHeld("")
		s.ClearOutput()
	})

	// The resend injects Z's release only: the waiting tap of Z starts
	// first, and the next tap of Z waits behind it (hold 10 ms) or not.
	for _, tt := range []struct {
		name    string
		hold    time.Duration
		queued  int
		batches string
	}{
		{"queued tap ends at once", 0, 0, "[-Z -LCtrl!] [+Z -Z] [+Z -Z]"},
		{"queued tap holds the key", 10 * ms, 1, "[-Z -LCtrl!] [+Z]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := pending(t)
			s.Out.FailNext(0, errBlocked)
			wantErr(t, s.Arb.Tap(ownerB, keys.MustParse("Z"), tt.hold), errBlocked, "failed again")
			if got := s.Arb.Queued(); got != 1 {
				t.Fatalf("Queued() = %d, want the tap waiting", got)
			}
			calls := s.Out.Calls()
			s.Out.FailNext(1, errBlocked) // -Z goes out, -LCtrl does not
			wantErr(t, s.Arb.Tap(ownerA, keys.MustParse("Z"), 0), errBlocked, "releasing LCtrl failed again")
			s.expectBatches(calls, tt.batches)
			if got := s.Arb.Queued(); got != tt.queued {
				t.Errorf("Queued() = %d, want %d", got, tt.queued)
			}
			s.Settle()
			s.expectHeld("")
			s.ClearOutput()
		})
	}
}

func TestArbiterTapNowPartial(t *testing.T) {
	tests := []struct {
		keep    []int // events each Send call injects, in order
		msg     string
		batches string
	}{
		{[]int{1}, "injected 1 of 4", "[+LCtrl +Z! -Z! -LCtrl!] [-Z -LCtrl]"},
		{[]int{3}, "injected 3 of 4", "[+LCtrl +Z -Z -LCtrl!] [-LCtrl]"},
		{[]int{0, 1}, "releasing LCtrl failed twice", "[+LCtrl! +Z! -Z! -LCtrl!] [-Z -LCtrl!] [-LCtrl]"},
	}
	for _, tt := range tests {
		s := newSim(t)
		for _, n := range tt.keep {
			s.Out.FailNext(n, errBlocked)
		}
		wantErr(t, s.Arb.Tap(ownerA, keys.MustParse("Ctrl+Z"), 0), errBlocked, tt.msg)
		s.Settle()
		s.expectBatches(0, tt.batches)
		s.ClearOutput()
	}
}

func TestArbiterTapEndPartial(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "Ctrl+Z", 20*ms)
	failRelease(s.Out, 1)
	s.Advance(20 * ms)
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@20")
	errs := s.Errors()
	if len(errs) != 1 {
		t.Fatalf("Advance errors %q, want one", errs)
	}
	wantErr(t, errs[0], errBlocked, "releasing LCtrl failed twice")
	s.expectHeld("LCtrl(pending)")
	s.expectDeadline(20*ms + output.DefaultRetryInterval)
	s.Settle()
	s.ExpectOutput("-LCtrl@120")
}

func TestArbiterReleaseAllPartial(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.tap(ownerB, "X", 50*ms)
	failRelease(s.Out, 1)
	wantErr(t, s.Arb.ReleaseAll(), errBlocked, "releasing Z, LCtrl failed twice")
	s.ExpectOutput("+LCtrl@0 +Z@0 +X@0 -X@0")
	s.expectHeld("LCtrl(pending) Z(pending)")
	s.expectDeadline(output.DefaultRetryInterval)
	s.Settle()
	s.ExpectOutput("-Z@100 -LCtrl@100")
}

func TestArbiterWarning(t *testing.T) {
	s := newSim(t)
	s.Out.WarnNext(errWarn)
	err := s.Arb.Down(ownerA, keys.MustParse("Ctrl+Z"))
	wantErr(t, err, errWarn)
	if strings.Contains(err.Error(), "injected") {
		t.Errorf("warning reported as a partial batch: %q", err)
	}
	s.expectHeld("LCtrl(A) Z(A)")
	s.Out.WarnNext(errWarn)
	wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errWarn)
	s.ExpectOutput("+LCtrl@0 +Z@0 -Z@0 -LCtrl@0")
	s.expectBatches(0, "[+LCtrl +Z] [-Z -LCtrl]")
	s.expectHeld("")
	s.expectDeadline(-1)
	s.expectStats(output.ArbiterStats{SendWarnings: 2})
}

func TestArbiterHeld(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "Ctrl+Z")
	s.down(ownerB, "Ctrl")
	s.tap(ownerS, "X", 10*ms)
	s.down(ownerS, "Z")
	s.down(ownerS, "KeyZ")
	s.expectHeld("LCtrl(A B) Z(A S) X(S)")

	held := s.Arb.Held()
	lctrl, _ := output.KeyEventFor(keys.MustParse("LCtrl").Key(), false)
	if held[0].Press != lctrl {
		t.Errorf("Held()[0].Press = %v, want %v", held[0].Press, lctrl)
	}
	// Held returns a copy.
	held[0].Owners[0] = ownerS
	s.expectHeld("LCtrl(A B) Z(A S) X(S)")

	s.Advance(10 * ms)
	s.expectHeld("LCtrl(A B) Z(A S)")
	for _, spec := range []string{"Z", "KeyZ"} {
		s.up(ownerS, spec)
	}
	s.up(ownerB, "Ctrl")
	s.up(ownerA, "Ctrl+Z")
	s.ExpectOutput("+LCtrl@0 +Z@0 +X@0 -X@10 -Z@10 -LCtrl@10")
}

// TestArbiterBlocked: while the gate is closed, a valid Down, Tap or
// Repeat presses nothing: it resends the pending releases, as every
// command does, and counts in Stats().Blocked. What releases keys works as
// ever: Up, ReleaseOwners, ReleaseAll, the end of a tap in flight and the
// retries of pending releases. SetBlocked sends nothing itself, and once
// the gate opens Down presses again.
func TestArbiterBlocked(t *testing.T) {
	z, x := keys.MustParse("Z"), keys.MustParse("X")
	pending := func(s tsim) {
		s.down(ownerA, "Ctrl+Z")
		failRelease(s.Out, 1)
		wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Ctrl+Z")), errBlocked, "releasing LCtrl failed twice")
	}
	tests := []struct {
		name   string
		before func(tsim)                  // at 0, with the gate open
		cmd    func(*output.Arbiter) error // at 5 ms, with the gate closed
		err    []string                    // what cmd's error contains, if it fails
		out    string                      // the output of cmd
		held   string                      // Held() after cmd
		stats  output.ArbiterStats         // after cmd
		settle string                      // the output of Settle after cmd
	}{{
		name:  "Down",
		cmd:   func(a *output.Arbiter) error { return a.Down(ownerA, z) },
		stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:   "Down held",
		before: func(s tsim) { s.down(ownerA, "Z") },
		cmd:    func(a *output.Arbiter) error { return a.Down(ownerA, z) },
		held:   "Z(A)", stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:   "Down on held key",
		before: func(s tsim) { s.down(ownerB, "Z") },
		cmd:    func(a *output.Arbiter) error { return a.Down(ownerA, keys.MustParse("Ctrl+KeyZ")) },
		held:   "Z(B)", stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:  "Tap",
		cmd:   func(a *output.Arbiter) error { return a.Tap(ownerA, z, 0) },
		stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:  "Tap hold",
		cmd:   func(a *output.Arbiter) error { return a.Tap(ownerA, keys.MustParse("Ctrl+Z"), 20*ms) },
		stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:   "Tap on held",
		before: func(s tsim) { s.down(ownerB, "Z") },
		cmd:    func(a *output.Arbiter) error { return a.Tap(ownerA, z, 0) },
		held:   "Z(B)", stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:   "Repeat",
		before: func(s tsim) { s.down(ownerA, "Ctrl+Z") },
		cmd:    func(a *output.Arbiter) error { return a.Repeat(ownerA, keys.MustParse("Ctrl+Z")) },
		held:   "LCtrl(A) Z(A)", stats: output.ArbiterStats{Blocked: 1},
	}, {
		name:  "Repeat not held",
		cmd:   func(a *output.Arbiter) error { return a.Repeat(ownerA, z) },
		stats: output.ArbiterStats{Blocked: 1},
	}, {
		name: "invalid",
		cmd: func(a *output.Arbiter) error {
			return errors.Join(a.Down(output.Owner{}, z), a.Tap(ownerA, keys.Chord{}, 0), a.Repeat(output.Owner{}, z))
		},
		err: []string{"invalid owner", "invalid chord"},
	}, {
		name:   "Up",
		before: func(s tsim) { s.down(ownerA, "Ctrl+Z") },
		cmd:    func(a *output.Arbiter) error { return a.Up(ownerA, keys.MustParse("Ctrl+Z")) },
		out:    "-Z@5 -LCtrl@5",
	}, {
		name:  "Up not held",
		cmd:   func(a *output.Arbiter) error { return a.Up(ownerA, z) },
		stats: output.ArbiterStats{UnmatchedUps: 1},
	}, {
		name:   "ReleaseOwner",
		before: func(s tsim) { s.down(ownerA, "Z"); s.down(ownerB, "X") },
		cmd:    func(a *output.Arbiter) error { return a.ReleaseOwner(ownerA) },
		out:    "-Z@5", held: "X(B)",
	}, {
		name:   "ReleaseAll",
		before: func(s tsim) { s.down(ownerA, "Ctrl+Z"); s.tap(ownerB, "X", 50*ms) },
		cmd:    (*output.Arbiter).ReleaseAll,
		out:    "-X@5 -Z@5 -LCtrl@5",
	}, {
		name:   "tap in flight",
		before: func(s tsim) { s.tap(ownerA, "Ctrl+Z", 20*ms) },
		cmd:    func(a *output.Arbiter) error { return a.Down(ownerB, x) },
		held:   "LCtrl(A) Z(A)", stats: output.ArbiterStats{Blocked: 1},
		settle: "-Z@20 -LCtrl@20",
	}, {
		name:   "pending release",
		before: pending,
		cmd:    func(a *output.Arbiter) error { return a.Down(ownerB, x) },
		out:    "-LCtrl@5",
		stats:  output.ArbiterStats{SendErrors: 2, Retries: 2, Blocked: 1},
	}, {
		name: "pending release fails again",
		before: func(s tsim) {
			pending(s)
			s.Out.FailNext(0, errBlocked)
		},
		cmd:    func(a *output.Arbiter) error { return a.Tap(ownerB, x, 0) },
		err:    []string{"releasing LCtrl failed again"},
		held:   "LCtrl(pending)",
		stats:  output.ArbiterStats{SendErrors: 3, Retries: 2, Blocked: 1, PendingReleases: 1},
		settle: "-LCtrl@105",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t)
			if s.Arb.Blocked() {
				t.Fatal("a new Arbiter is blocked")
			}
			if tt.before != nil {
				tt.before(s)
			}
			s.ClearOutput()
			stats, calls := s.Arb.Stats(), s.Out.Calls()
			s.Arb.SetBlocked(true)
			if !s.Arb.Blocked() {
				t.Fatal("Blocked() = false after SetBlocked(true)")
			}
			if got := s.Out.Calls(); got != calls {
				t.Errorf("SetBlocked made %d Send calls", got-calls)
			}
			s.expectStats(stats)
			s.Advance(5 * ms)
			s.ExpectOutput("")

			err := tt.cmd(s.Arb)
			if len(tt.err) > 0 {
				wantErr(t, err, nil, tt.err...)
			} else if err != nil {
				t.Fatal(err)
			}
			s.ExpectOutput(tt.out)
			s.expectHeld(tt.held)
			if tt.held == "" {
				s.expectDeadline(-1)
			}
			s.expectStats(tt.stats)
			s.Settle()
			s.ExpectOutput(tt.settle)

			// Open again, the gate lets presses through.
			s.Arb.SetBlocked(false)
			if s.Arb.Blocked() {
				t.Fatal("Blocked() = true after SetBlocked(false)")
			}
			s.AdvanceTo(clock.Time(200 * ms))
			s.down(ownerS, "F13")
			s.ExpectOutput("+F13@200")
			if err := s.Arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			s.ClearOutput()
		})
	}
}

func TestArbiterNextDeadline(t *testing.T) {
	s := newSim(t, withRetry(10*ms))
	s.expectDeadline(-1)
	s.tap(ownerA, "X", 30*ms)
	s.tap(ownerA, "Q", 40*ms)
	s.expectDeadline(30 * ms)
	s.down(ownerB, "Ctrl+Z")
	s.Advance(5 * ms)
	failRelease(s.Out, 1)
	wantErr(t, s.Arb.Up(ownerB, keys.MustParse("Ctrl+Z")), errBlocked)
	s.expectDeadline(15 * ms) // the retry comes before the tap
	s.Advance(10 * ms)
	s.expectDeadline(30 * ms)
	s.Settle()
	s.ExpectOutput("+X@0 +Q@0 +LCtrl@0 +Z@0 -Z@5 -LCtrl@15 -X@30 -Q@40")
	s.expectDeadline(-1)
}
