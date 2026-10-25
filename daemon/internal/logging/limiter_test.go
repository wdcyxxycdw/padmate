package logging

import (
	"strconv"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestLimiter(t *testing.T) {
	clk := clock.NewFake()
	l := NewLimiter(clk, time.Second)
	allow := func(key string, want bool, wantSup int) {
		t.Helper()
		if ok, n := l.Allow(key); ok != want || n != wantSup {
			t.Errorf("at %v Allow(%q) = %v, %d; want %v, %d", clk.Now(), key, ok, n, want, wantSup)
		}
	}
	allow("a", true, 0)
	allow("a", false, 1)
	allow("b", true, 0) // keys are independent
	clk.Set(clock.Time(999 * time.Millisecond))
	allow("a", false, 2)
	clk.Set(clock.Time(time.Second))
	allow("a", true, 2) // the refusals since it was last allowed
	allow("a", false, 1)
	allow("b", true, 0)
	clk.Advance(5 * time.Second)
	allow("a", true, 1)
	allow("", true, 0) // any string is a key
	allow("", false, 1)
}

func TestLimiterEveryZero(t *testing.T) {
	l := NewLimiter(clock.NewFake(), 0)
	for range 3 {
		if ok, n := l.Allow("a"); !ok || n != 0 {
			t.Errorf("Allow = %v, %d; want every call allowed", ok, n)
		}
	}
}

// TestLimiterBound: a full Limiter forgets the key it allowed longest ago.
func TestLimiterBound(t *testing.T) {
	clk := clock.NewFake()
	l := NewLimiter(clk, time.Second)
	key := func(i int) string { return "k" + strconv.Itoa(i) }
	for i := range maxKeys {
		if ok, _ := l.Allow(key(i)); !ok {
			t.Fatalf("Allow(%s) refused", key(i))
		}
	}
	clk.Advance(time.Second)
	if ok, _ := l.Allow(key(0)); !ok { // allowed again: now the newest
		t.Fatal("k0 refused after the interval")
	}
	if ok, _ := l.Allow("new"); !ok { // forgets k1
		t.Fatal("a new key refused")
	}
	if len(l.keys) != maxKeys {
		t.Errorf("%d keys, want %d", len(l.keys), maxKeys)
	}
	for _, k := range []string{key(0), "new", key(2), key(maxKeys - 1)} {
		if l.keys[k] == nil {
			t.Errorf("%s forgotten", k)
		}
	}
	if l.keys[key(1)] != nil {
		t.Errorf("k1 remembered, want it forgotten")
	}
	// A key refused before it was forgotten starts afresh.
	if ok, n := l.Allow(key(0)); ok || n != 1 {
		t.Errorf("Allow(k0) = %v, %d; want refused once", ok, n)
	}
	for i := range maxKeys - 1 {
		l.Allow("x" + strconv.Itoa(i)) // forget k2 to k255, then k0
	}
	if ok, n := l.Allow(key(0)); !ok || n != 0 {
		t.Errorf("Allow(k0) after it was forgotten = %v, %d; want allowed, 0 suppressed", ok, n)
	}
}
