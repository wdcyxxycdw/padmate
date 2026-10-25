package main

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// profilePointer has the left stick in mode "mouse" (ADR-0062, section 5) at
// 1000 pixels a second and nothing else: no button is bound, so nothing but the
// stick can make padmated send anything. The real-injection test
// TestLiveInjectPointer and TestLivePointerStick share it.
const profilePointer = `{"schemaVersion": 1, "name": "pointer", "analog": {"ls": {"mode": "mouse", ` +
	`"mouseSpeed": 1000, "deadzone": 0, "saturation": 1}}, "layers": {"base": {}}}` + "\n"

// writePointerProfile writes profilePointer to a file of the test's directory
// and returns its path.
func writePointerProfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profilePointer), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPointerProfile: the profile the pointer tests run is a valid one, with the
// diagnostics of padmated's own loader. (The real-injection test could not say
// so without being run.)
func TestPointerProfile(t *testing.T) {
	var stderr syncBuffer
	if _, _, _, ok := loadProfile(writePointerProfile(t), &stderr); !ok {
		t.Fatalf("loading profilePointer: %s", stderr.String())
	}
	if s := stderr.String(); s != "" {
		t.Errorf("loading profilePointer wrote %q, want nothing", s)
	}
}

// pointerSender is the Sender of --inject for a test of the stick: it keeps the
// pointer moves it is asked to inject, and every other event apart. Nothing is
// injected.
type pointerSender struct {
	mu    sync.Mutex
	moves []output.KeyEvent
	other []output.KeyEvent
}

func (s *pointerSender) Send(evs []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		if e.Mouse == keys.MouseMove {
			s.moves = append(s.moves, e)
		} else {
			s.other = append(s.other, e)
		}
	}
	return len(evs), nil
}

// seen returns the moves sent so far, and the events that are not moves.
func (s *pointerSender) seen() (moves, other []output.KeyEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.moves), slices.Clone(s.other)
}

func (s *pointerSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.moves)
}

// distance returns the sum of the moves from the index from on.
func (s *pointerSender) distance(from int) (dx, dy int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.moves[from:] {
		dx += int(e.Dx)
		dy += int(e.Dy)
	}
	return dx, dy
}

// waitStill waits, at most liveWait, until s has sent no move for quiet, and
// returns how many it has sent. A pointer that is still being moved sends one
// every 8 ms, so a quiet of a few ticks says the stick has stopped it.
func (tl *testLive) waitStill(t *testing.T, s *pointerSender, quiet time.Duration) int {
	t.Helper()
	last, changed := -1, time.Now()
	tl.waitUntil(t, "no pointer move for "+quiet.String(), func() bool {
		if n := s.count(); n != last {
			last, changed = n, time.Now()
			return false
		}
		return time.Since(changed) >= quiet
	})
	return last
}

// TestLivePointerStick (ADR-0062, section 5) runs the stick of profilePointer
// through live mode with --inject, on the clock of the daemon, with a Sender that
// injects nothing: pushed to the right the stick sends moves that go right and
// no further, at the tick of the engine; back in the center it stops; pushed to
// the left it sends moves that go left; and padmated's exit, with the stick still
// pushed, stops the moves for good. The injected equivalent is
// TestLiveInjectPointer, which has the same steps, with a hook that swallows the
// moves. It needs no SDL.
func TestLivePointerStick(t *testing.T) {
	s := &pointerSender{}
	tl, src := sessionLive(t, nil, func() (output.Sender, error) { return s, nil }, "--inject", "--profile", writePointerProfile(t))

	// Pushed to the right: moves right, none up or down.
	stick(src, 1)
	tl.waitUntil(t, "8 pointer moves", func() bool { return s.count() >= 8 })
	// Back in the center: they stop, and stay stopped.
	stick(src, 0)
	n := tl.waitStill(t, s, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	if again := s.count(); again != n {
		t.Errorf("the stick is centered and the pointer moved %d more times", again-n)
	}
	moves, _ := s.seen()
	for i, e := range moves {
		// At 1000 pixels a second a tick of 8 ms is 8 pixels; a stalled one is
		// at most 50 (MouseMaxDT).
		if e.Dx < 1 || e.Dx > 50 || e.Dy != 0 || e.Up || e.Silent() {
			t.Errorf("move %d = %+v, want a move to the right of 1 to 50 pixels and no more", i, e)
		}
	}
	if dx, _ := s.distance(0); dx < 20 {
		t.Errorf("the pointer went %d pixels to the right, want some", dx)
	}

	// Pushed to the left: moves left. Then padmated stops with the stick pushed.
	stick(src, -1)
	tl.waitUntil(t, "8 more pointer moves", func() bool { return s.count() >= n+8 })
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}
	n = s.count()
	time.Sleep(100 * time.Millisecond)
	if again := s.count(); again != n {
		t.Errorf("padmated stopped and the pointer moved %d more times", again-n)
	}
	if dx, dy := s.distance(len(moves)); dx > -20 || dy != 0 {
		t.Errorf("pushed to the left the pointer went (%d, %d), want some distance to the left and none up or down", dx, dy)
	}
	moves, other := s.seen()
	if len(other) != 0 {
		t.Errorf("padmated sent %v besides pointer moves, want none", other)
	}
	for i, e := range moves[len(moves)-8:] {
		if e.Mouse != keys.MouseMove || e.Dx > -1 || e.Dx < -50 || e.Dy != 0 {
			t.Errorf("one of the last moves, %d, = %+v, want a move to the left of 1 to 50 pixels", i, e)
		}
	}
}
