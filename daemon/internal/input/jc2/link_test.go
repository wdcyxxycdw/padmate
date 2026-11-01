package jc2

import (
	"testing"
	"time"
)

func TestSilence(t *testing.T) {
	const ms = time.Millisecond
	var s Silence // the zero value uses DefaultSilence

	if DefaultSilence != 300*ms {
		t.Errorf("DefaultSilence = %v", DefaultSilence)
	}
	// Nothing seen yet: nothing to watch.
	if s.Dead(time.Hour) {
		t.Error("dead before anything was seen")
	}
	if _, ok := s.Deadline(); ok {
		t.Error("deadline before anything was seen")
	}

	s.Seen(1000 * ms)
	if d, ok := s.Deadline(); !ok || d != 1300*ms {
		t.Errorf("Deadline = %v, %v", d, ok)
	}
	for _, c := range []struct {
		now  time.Duration
		dead bool
	}{
		{1000 * ms, false},
		{1136 * ms, false}, // the longest gap ever seen
		{1299 * ms, false},
		{1300 * ms, true},
		{9700 * ms, true},
		{900 * ms, false}, // a clock that stepped back is not dead
	} {
		if got := s.Dead(c.now); got != c.dead {
			t.Errorf("Dead(%v) = %v, want %v", c.now, got, c.dead)
		}
	}

	// A report revives it; an old time does not move it back.
	s.Seen(1400 * ms)
	if s.Dead(1650 * ms) {
		t.Error("dead 250 ms after a report")
	}
	s.Seen(1200 * ms)
	if d, _ := s.Deadline(); d != 1700*ms {
		t.Errorf("Deadline after an old time = %v", d)
	}
	if !s.Dead(1700 * ms) {
		t.Error("not dead at the deadline")
	}

	s.Reset()
	if s.Dead(time.Hour) {
		t.Error("dead after Reset")
	}
}

func TestSilenceTimeout(t *testing.T) {
	s := Silence{Timeout: 100 * time.Millisecond}
	s.Seen(0)
	if s.Dead(99 * time.Millisecond) {
		t.Error("dead before its timeout")
	}
	if !s.Dead(100 * time.Millisecond) {
		t.Error("alive at its timeout")
	}
	// A negative timeout is the default.
	n := Silence{Timeout: -1}
	n.Seen(0)
	if n.Dead(DefaultSilence-1) || !n.Dead(DefaultSilence) {
		t.Error("negative timeout")
	}
}

func TestSilenceGiveUp(t *testing.T) {
	const ms = time.Millisecond
	if DefaultLinkGiveUp != 3*time.Second || DefaultLinkGiveUp <= DefaultSilence {
		t.Errorf("DefaultLinkGiveUp = %v", DefaultLinkGiveUp)
	}
	var s Silence
	if s.GaveUp(time.Hour) {
		t.Error("gave up before anything was seen")
	}
	s.Seen(1000 * ms)
	for _, c := range []struct {
		now          time.Duration
		dead, gaveUp bool
	}{
		{1000 * ms, false, false},
		{1300 * ms, true, false}, // dormant: release, keep the link
		{3999 * ms, true, false},
		{4000 * ms, true, true}, // close it for good
		{500 * ms, false, false},
	} {
		if s.Dead(c.now) != c.dead || s.GaveUp(c.now) != c.gaveUp {
			t.Errorf("at %v: Dead %v, GaveUp %v; want %v, %v", c.now, s.Dead(c.now), s.GaveUp(c.now), c.dead, c.gaveUp)
		}
	}
	// A report ends the silence again; Reset forgets it.
	s.Seen(3900 * ms)
	if s.Dead(4100*ms) || s.GaveUp(4100*ms) {
		t.Error("a report did not revive the link")
	}
	g := Silence{GiveUp: time.Second}
	g.Seen(0)
	if g.GaveUp(999*ms) || !g.GaveUp(1000*ms) {
		t.Error("custom GiveUp")
	}
	s.Reset()
	if s.GaveUp(time.Hour) {
		t.Error("gave up after Reset")
	}
}
