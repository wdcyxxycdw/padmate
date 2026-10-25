package script

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestOutputBudgetBurstAndRate(t *testing.T) {
	c := clock.NewFake()
	b := NewOutputBudget(c)

	for i := 0; i < OutputBurst; i++ {
		if !b.Allow(1) {
			t.Fatalf("burst operation %d was rejected", i)
		}
	}
	if b.Allow(1) {
		t.Fatal("operation after the burst was accepted")
	}

	// 500 operations/s is one token per 2 ms. The budget keeps fractional
	// refill progress, so a sub-token interval alone does not allow a press.
	c.Advance(time.Millisecond)
	if b.Allow(1) {
		t.Fatal("half-token refill was accepted")
	}
	c.Advance(time.Millisecond)
	if !b.Allow(1) {
		t.Fatal("one full token refill was rejected")
	}

	// Once a full second has elapsed, the bucket is capped at its burst size.
	c.Advance(time.Second)
	for i := 0; i < OutputBurst; i++ {
		if !b.Allow(1) {
			t.Fatalf("refilled burst operation %d was rejected", i)
		}
	}
	if b.Allow(1) {
		t.Fatal("operation beyond the refilled burst was accepted")
	}
}

func TestOutputBudgetTapPendingPerKey(t *testing.T) {
	c := clock.NewFake()
	b := NewOutputBudget(c)
	z := keys.MustParse("Ctrl+Z")
	x := keys.MustParse("X")

	if !b.AllowTap(z, 100*time.Millisecond) {
		t.Fatal("first timed tap was rejected")
	}
	if b.AllowTap(z, time.Millisecond) {
		t.Fatal("second pending tap of the same key was accepted")
	}
	if !b.AllowTap(x, time.Millisecond) {
		t.Fatal("a different key's tap was rejected")
	}

	c.Advance(99 * time.Millisecond)
	if b.AllowTap(z, time.Millisecond) {
		t.Fatal("tap before the first deadline was accepted")
	}
	c.Advance(time.Millisecond)
	if b.AllowTap(z, 0) {
		t.Fatal("tap was accepted before the Arbiter completion")
	}
	b.CompleteTap(z)
	if !b.AllowTap(z, 0) {
		t.Fatal("tap after the Arbiter completion was rejected")
	}
}

func TestOutputBudgetChordTapReservesEveryKeyRelease(t *testing.T) {
	b := NewOutputBudget(clock.NewFake())
	chord := keys.MustParse("Ctrl+Z")
	for i := 0; i < OutputBurst/4; i++ {
		if !b.AllowTap(chord, 0) {
			t.Fatalf("two-key tap %d was rejected", i)
		}
		b.CompleteTap(chord)
	}
	if b.AllowTap(chord, 0) {
		t.Fatal("26th two-key tap exceeded the 100-change burst")
	}
}

func TestOutputBudgetReleaseClearsPending(t *testing.T) {
	c := clock.NewFake()
	b := NewOutputBudget(c)
	z := keys.MustParse("Z")

	if !b.AllowTap(z, time.Second) {
		t.Fatal("first timed tap was rejected")
	}
	b.ReleaseAll()
	if !b.AllowTap(z, time.Second) {
		t.Fatal("tap after releaseAll was rejected")
	}
}

func TestOutputBudgetRejectsInvalidChanges(t *testing.T) {
	b := NewOutputBudget(clock.NewFake())
	for _, n := range []int{0, -1} {
		if b.Allow(n) {
			t.Errorf("Allow(%d) = true, want false", n)
		}
	}
	if b.Allow(OutputBurst + 1) {
		t.Fatal("operation larger than the burst was accepted")
	}
}
