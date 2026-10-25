package injecttest

import (
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The tests in this file inject nothing and install no hook: they run the
// checks of the pointer moves on events made up as a hook would report them,
// and the refusals that stop InjectFencedMoves and InjectFencedMixed before
// anything is sent.

// relMoves returns the events of relative moves, each a {dx, dy}.
func relMoves(ds ...[2]int32) []output.KeyEvent {
	var out []output.KeyEvent
	for _, d := range ds {
		out = append(out, PointerMove(d[0], d[1]))
	}
	return out
}

// The mouse canary is a pair of pointer moves of the plain form: the Arbiter's
// own Move event, which PointerMove makes.
func TestPointerMoveIsTheMoveOfTheCanary(t *testing.T) {
	events := MouseCanaryEvents()
	if want := relMoves([2]int32{1, 0}, [2]int32{-1, 0}); !slices.Equal(events, want) {
		t.Errorf("MouseCanaryEvents() = %v, want PointerMove(+1, 0) and PointerMove(-1, 0)", events)
	}
	if p := UnpairedMoves(events); p != "" {
		t.Errorf("UnpairedMoves(MouseCanaryEvents()) = %q, want none: the canary is a batch like any", p)
	}
}

// A batch of pointer moves must add up to no distance, however it goes about
// it, and be small: a batch that got past the hook would then take the cursor
// out and bring it back within one SendInput call.
func TestUnpairedMoves(t *testing.T) {
	steps := func(n int, dx, dy int32) [][2]int32 {
		out := make([][2]int32, n)
		for i := range out {
			out[i] = [2]int32{dx, dy}
		}
		return out
	}
	// repeat returns n times the moves ds, one after the other.
	repeat := func(n int, ds ...[2]int32) [][2]int32 {
		var out [][2]int32
		for range n {
			out = append(out, ds...)
		}
		return out
	}
	jump := output.KeyEvent{Mouse: keys.MouseJump, Dx: 100, Dy: 100}
	release := PointerMove(5, 5)
	release.Up = true
	withKey := PointerMove(5, 5)
	withKey.Scan = 0x1E
	wheel := output.KeyEvent{Mouse: keys.WheelUp}
	for _, tt := range []struct {
		name   string
		events []output.KeyEvent
		want   string // a part of the problem, or "" for none
	}{
		{"a pair along x", relMoves([2]int32{1, 0}, [2]int32{-1, 0}), ""},
		{"a pair along y", relMoves([2]int32{0, 7}, [2]int32{0, -7}), ""},
		{"a diagonal pair", relMoves([2]int32{5, 3}, [2]int32{-5, -3}), ""},
		{"out and back another way", relMoves([2]int32{10, 0}, [2]int32{0, 10}, [2]int32{-10, -10}), ""},
		{"a large pair", relMoves([2]int32{MaxPointerStep, -MaxPointerStep}, [2]int32{-MaxPointerStep, MaxPointerStep}), ""},
		{"a stick's steps out and back", relMoves(slices.Concat(steps(10, 2, 1), steps(10, -2, -1))...), ""},
		{"the most moves", relMoves(repeat(MaxPointerMoves/2, [2]int32{1, 0}, [2]int32{-1, 0})...), ""},
		{"the farthest excursion", relMoves(slices.Concat(steps(2, MaxPointerExcursion/2, 0), steps(2, -MaxPointerExcursion/2, 0))...), ""},

		{"nothing", nil, "no moves"},
		{"one move", relMoves([2]int32{1, 0}), "add up to (+1, +0)"},
		{"a pair that does not cancel", relMoves([2]int32{4, 0}, [2]int32{-3, 0}), "add up to (+1, +0)"},
		{"y does not cancel", relMoves([2]int32{4, 4}, [2]int32{-4, -3}), "add up to (+0, +1)"},
		{"only x cancels", relMoves([2]int32{4, 4}, [2]int32{-4, 4}), "add up to (+0, +8)"},
		{"too many moves", relMoves(repeat(MaxPointerMoves/2+1, [2]int32{1, 0}, [2]int32{-1, 0})...), "at most"},
		{"a move of no distance", relMoves([2]int32{1, 0}, [2]int32{0, 0}, [2]int32{-1, 0}), "move 1 is of no distance"},
		{"a step too far", relMoves([2]int32{MaxPointerStep + 1, 0}, [2]int32{-MaxPointerStep - 1, 0}), "farther than"},
		{"a step of the largest size", relMoves([2]int32{1 << 30, 0}, [2]int32{-1 << 30, 0}), "farther than"},
		{"the extreme values", relMoves([2]int32{-1 << 31, 0}, [2]int32{1<<31 - 1, 0}), "farther than"},
		{"out too far", relMoves(slices.Concat(steps(3, MaxPointerExcursion/2, 0), steps(3, -MaxPointerExcursion/2, 0))...), "after move 2 the cursor would be"},
		{"a jump", []output.KeyEvent{jump}, "absolute jump"},
		{"a jump among the moves", slices.Concat(relMoves([2]int32{1, 0}), []output.KeyEvent{jump}, relMoves([2]int32{-1, 0})), "absolute jump"},
		{"a release flag", []output.KeyEvent{release}, "not a plain relative pointer move"},
		{"a scan code", []output.KeyEvent{withKey}, "not a plain relative pointer move"},
		{"a wheel notch", []output.KeyEvent{wheel}, "not a plain relative pointer move"},
		{"a key", []output.KeyEvent{CanaryEvent()}, "not a plain relative pointer move"},
	} {
		got := UnpairedMoves(tt.events)
		switch {
		case tt.want == "" && got != "":
			t.Errorf("%s: UnpairedMoves = %q, want none", tt.name, got)
		case tt.want != "" && !strings.Contains(got, tt.want):
			t.Errorf("%s: UnpairedMoves = %q, want one with %q", tt.name, got, tt.want)
		}
	}
}

// A pointer event in a batch of InjectFencedMixed is noted by the plan, which
// the method refuses; the other mouse events are not pointer events.
func TestPlanBatchPointer(t *testing.T) {
	ev := func(spec string, up bool) output.KeyEvent { return chordEvent(t, spec, up) }
	for _, tt := range []struct {
		name   string
		events []output.KeyEvent
		want   bool
	}{
		{"nothing", nil, false},
		{"keyboard", []output.KeyEvent{ev("Z", false), ev("Z", true)}, false},
		{"a wheel key", []output.KeyEvent{ev("WheelUp", false), ev("WheelUp", true)}, false},
		{"a button", []output.KeyEvent{ev("MouseLeft", false), ev("MouseLeft", true)}, false},
		{"a move", relMoves([2]int32{3, 0}), true},
		{"the moves of the canary", MouseCanaryEvents(), true},
		{"a jump", []output.KeyEvent{{Mouse: keys.MouseJump, Dx: 1, Dy: 1}}, true},
		{"a move in a chord", slices.Concat([]output.KeyEvent{ev("LCtrl", false)}, relMoves([2]int32{3, 0}), []output.KeyEvent{ev("LCtrl", true)}), true},
	} {
		plan := planBatch(tt.events)
		if plan.pointer != tt.want {
			t.Errorf("%s: plan.pointer = %v, want %v", tt.name, plan.pointer, tt.want)
		}
		if tt.want && !plan.hasMouse {
			t.Errorf("%s: a pointer move is a mouse event, but plan.hasMouse = false", tt.name)
		}
	}
}

// pointerSeen is what the mouse hook sees of a move of padmated or of a test
// with nonce, if it works as WinUser.h says: WM_MOUSEMOVE, LLMHF_INJECTED, the
// low 32 bits of the extra.
func pointerSeen(seq uint64, nonce uint32, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Msg: winhook.WMMouseMove, X: 640, Y: 360, Flags: winhook.MouseFlagInjected,
		Extra: delivered(nonce), Swallowed: swallowed, Seq: seq,
	}
}

func TestPointerProblems(t *testing.T) {
	const nonce = mouseTestBase | 11
	good := pointerSeen(1, nonce, true)
	with := func(f func(e *winhook.MouseEvent)) winhook.MouseEvent { e := good; f(&e); return e }
	for _, tt := range []struct {
		name string
		e    winhook.MouseEvent
		want string // a part of the problems, or "" for none
	}{
		{"a move as Windows delivers it", good, ""},
		{"the whole tag kept", with(func(e *winhook.MouseEvent) { e.Extra = output.ExtraInfo(nonce) }), ""},
		{"somewhere else", with(func(e *winhook.MouseEvent) { e.X, e.Y = 1, 2 }), ""},
		{"not swallowed", with(func(e *winhook.MouseEvent) { e.Swallowed = false }), "not swallowed"},
		{"another nonce", with(func(e *winhook.MouseEvent) { e.Extra++ }), "want the nonce"},
		{"another high half", with(func(e *winhook.MouseEvent) { e.Extra |= 0x1_0000_0000 }), "want the nonce"},
		{"not injected", with(func(e *winhook.MouseEvent) { e.Flags = 0 }), "want LLMHF_INJECTED only"},
		{"from a lower integrity level", with(func(e *winhook.MouseEvent) { e.Flags |= winhook.MouseFlagLowerILInjected }), "want LLMHF_INJECTED only"},
		{"a notch", with(func(e *winhook.MouseEvent) { e.Msg, e.MouseData = winhook.WMMouseWheel, 120<<16 }), "message WM_MOUSEWHEEL"},
		{"a button", with(func(e *winhook.MouseEvent) { e.Msg = winhook.WMLButtonDown }), "message WM_LBUTTONDOWN"},
	} {
		problems := strings.Join(PointerProblems(tt.e, nonce), "; ")
		switch {
		case tt.want == "" && problems != "":
			t.Errorf("%s: problems %q, want none", tt.name, problems)
		case tt.want != "" && !strings.Contains(problems, tt.want):
			t.Errorf("%s: problems %q, want one with %q", tt.name, problems, tt.want)
		}
	}
}

// What InjectFencedMoves decides after the canary behind the moves has come.
func TestMovesProblem(t *testing.T) {
	const nonce, fence = mouseTestBase | 12, mouseTestBase | 13
	mv := func(seq uint64, swallowed bool) winhook.MouseEvent { return pointerSeen(seq, nonce, swallowed) }
	canary := func(seq uint64, swallowed bool) Seen {
		e := move(seq, fence, swallowed)
		return Seen{Mouse: &e}
	}
	ok := []Seen{canary(10, true), canary(11, true)}
	for _, tt := range []struct {
		name     string
		got      []winhook.MouseEvent
		canaries []Seen
		want     int
		problem  string // a part of the problem, or "" for none
	}{
		{"all as it should be", []winhook.MouseEvent{mv(1, true), mv(2, true)}, ok, 2, ""},
		{"a long batch", slices.Repeat([]winhook.MouseEvent{mv(3, true)}, 100), ok, 100, ""},
		{"a move missing", []winhook.MouseEvent{mv(1, true)}, ok, 2, "saw 1 moves before the canary, want 2"},
		{"a move too many", []winhook.MouseEvent{mv(1, true), mv(2, true), mv(3, true)}, ok, 2, "saw 3 moves before the canary, want 2"},
		{"no moves seen", nil, ok, 2, "saw 0 moves before the canary, want 2"},
		{"a move that got past the hook", []winhook.MouseEvent{mv(1, true), mv(2, false)}, ok, 2, "a move got past the hook"},
		{"the first move of the canary got past", nil, []Seen{canary(10, false), canary(11, true)}, 0, "a move of the canary got past the hook"},
		{"the second move of the canary got past", nil, []Seen{canary(10, true), canary(11, false)}, 0, "a move of the canary got past the hook"},
		{"one move of the canary", []winhook.MouseEvent{mv(1, true)}, ok[:1], 1, "saw 1 moves of the canary"},
		{"a move after the canary", []winhook.MouseEvent{mv(1, true), mv(12, true)}, ok, 2, "after the canary behind it"},
	} {
		got := movesProblem(tt.got, tt.canaries, tt.want)
		switch {
		case tt.problem == "" && got != "":
			t.Errorf("%s: movesProblem = %q, want none", tt.name, got)
		case tt.problem != "" && !strings.Contains(got, tt.problem):
			t.Errorf("%s: movesProblem = %q, want one with %q", tt.name, got, tt.problem)
		}
	}
}

// A batch of moves is refused before anything is sent, whatever the mouse hook
// has proven: one that does not add up to nothing, one with a nonce that is not
// the recorder's, one that shares its nonce with its canary (the hook could not
// tell them apart), and one before MouseCanary has run. The pointer moves of
// InjectFencedMixed are refused too, even after the canary. The events of the
// refused batches are one move of a pixel, or the +1 and -1 of the canary, so
// that a refusal that failed would leave the cursor within a pixel of where it
// was.
func TestInjectFencedMovesRefuses(t *testing.T) {
	const base = mouseTestBase
	owns := func(nonce uint32) bool { return Owns(base, nonce) }
	pair := relMoves([2]int32{1, 0}, [2]int32{-1, 0})
	lone := relMoves([2]int32{0, 1})

	for _, tt := range []struct {
		name string
		live bool // a mouse canary has proven the mouse hook live
		run  func(f *fakeTB, r *Recorder)
		want string
	}{
		// The checks before the one for the mouse canary run with a recorder
		// that has not had one, so that a check that stopped working would be
		// caught by that one, with another message, and nothing would be sent.
		{"unpaired", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base|1, base|2, lone) },
			"add up to (+0, +1)"},
		{"no moves", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base|1, base|2, nil) },
			"there are no moves"},
		{"a jump", false, func(f *fakeTB, r *Recorder) {
			r.InjectFencedMoves(f, base|1, base|2, []output.KeyEvent{{Mouse: keys.MouseJump, Dx: 1, Dy: 1}})
		}, "absolute jump"},
		{"the nonce of the moves is not the recorder's", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base+0x10000|1, base|2, pair) },
			"not both owned by the recorder"},
		{"the nonce of the canary is not the recorder's", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base|1, base+0x10000|2, pair) },
			"not both owned by the recorder"},
		{"one nonce for both", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base|1, base|1, pair) },
			"share the nonce"},
		{"no mouse canary yet", false, func(f *fakeTB, r *Recorder) { r.InjectFencedMoves(f, base|1, base|2, pair) },
			"call MouseCanary first"},
		{"InjectFencedMixed, with the hook live", true, func(f *fakeTB, r *Recorder) { r.InjectFencedMixed(f, base|1, pair) },
			"use InjectFencedMoves"},
		{"InjectFencedMixed, the moves in a chord", true, func(f *fakeTB, r *Recorder) {
			// Keyboard canaries, not real keys: the release of a key that is not
			// down does nothing even if a regressed guard let it through.
			r.InjectFencedMixed(f, base|1, slices.Concat([]output.KeyEvent{CanaryEvent()}, pair, []output.KeyEvent{CanaryEvent()}))
		}, "use InjectFencedMoves"},
	} {
		f := &fakeTB{TB: t}
		r := newRecorder(owns)
		r.mouseLive = tt.live
		failures := f.run(func() { tt.run(f, r) })
		if len(failures) != 1 || !strings.Contains(failures[0], tt.want) {
			t.Errorf("%s: failures %q, want one containing %q", tt.name, failures, tt.want)
		}
		if n := r.OwnCount(); n != 0 {
			t.Errorf("%s: the recorder saw %d events: something was injected", tt.name, n)
		}
	}
}
