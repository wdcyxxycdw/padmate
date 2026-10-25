package input

import (
	"errors"
	"testing"
)

// fakeReads returns a read function for a hidBeat that gives the reports
// in order, one per call, and then 0. A nil report makes it return -1.
func fakeReads(reports ...[]byte) func([]byte) int {
	i := 0
	return func(b []byte) int {
		if i >= len(reports) {
			return 0
		}
		r := reports[i]
		i++
		if r == nil {
			return -1
		}
		return copy(b, r)
	}
}

// full returns a 0x30 report with timer and the byte status after it,
// the rest of its 64 bytes zero but b3, the first button byte.
func full(id, timer, status, b3 byte) []byte {
	r := make([]byte, 64)
	r[0], r[1], r[2], r[3] = id, timer, status, b3
	return r
}

// with returns r with byte i set to v.
func with(r []byte, i int, v byte) []byte {
	r[i] = v
	return r
}

func TestHIDBeatDrain(t *testing.T) {
	h := &hidBeat{read: fakeReads(
		full(0x30, 1, 0x90, 0),
		full(0x30, 2, 0x90, 0),
		full(0x30, 2, 0x90, 0), // the timer froze: not a heartbeat
		full(0x30, 3, 0x8e, 0), // the battery byte changed
		full(0x31, 4, 0x8e, 0),
		[]byte{0x21, 5, 0x8e, 0x80, 0x02},
		[]byte{0x81, 0x01},
		[]byte{0x3f, 0x00, 0x00, 0x08},
	)}
	d, err := h.drain(ms(1000))
	// The first full report has none before it to repeat: every other
	// heartbeat is idle. The simple report is counted apart.
	if err != nil || d != (drained{beats: 4, idle: 3, simple: true}) {
		t.Fatalf("drain = %+v, %v; want 4 heartbeats, 3 idle, a simple report after them", d, err)
	}
	if h.reports != 8 || h.still != 1 || h.other != 3 || h.idle != 3 {
		t.Errorf("reports %d, still %d, other %d, idle %d; want 8, 1, 3, 3", h.reports, h.still, h.other, h.idle)
	}
	if got, want := h.idCounts(), "30:5,21:1,3f:1,81:1,other:0"; got != want {
		t.Errorf("idCounts = %q, want %q", got, want)
	}
	if h.status != 0x8e || h.prevTimer != 4 {
		t.Errorf("status %#x, timer %d; want 0x8e, 4", h.status, h.prevTimer)
	}
	if h.changedAt != ms(1000) {
		t.Errorf("changedAt = %d, want %d", h.changedAt, ms(1000))
	}

	// The same buttons and sticks later do not move changedAt; a changed
	// button byte does. The gaps between drains with heartbeats, and with
	// idle ones, are measured.
	h.read = fakeReads(full(0x30, 5, 0x8e, 0))
	if d, err := h.drain(ms(1100)); err != nil || d != (drained{beats: 1, idle: 1}) {
		t.Fatalf("drain = %+v, %v; want 1 idle heartbeat", d, err)
	}
	if h.changedAt != ms(1000) {
		t.Errorf("changedAt = %d after the same content, want %d", h.changedAt, ms(1000))
	}
	h.read = fakeReads(full(0x30, 6, 0x8e, 0x04))
	if d, err := h.drain(ms(1400)); err != nil || d != (drained{beats: 1}) {
		t.Fatalf("drain of a pressed button = %+v, %v; want 1 heartbeat, not idle", d, err)
	}
	if h.changedAt != ms(1400) {
		t.Errorf("changedAt = %d after a button, want %d", h.changedAt, ms(1400))
	}
	if h.beatGap.max != ms(300) || h.idleGap.max != ms(100) || h.idleGap.last != ms(1100) {
		t.Errorf("beat gap %d, idle gap %d, last idle %d; want %d, %d, %d",
			h.beatGap.max, h.idleGap.max, h.idleGap.last, ms(300), ms(100), ms(1100))
	}
	h.read = fakeReads(full(0x30, 6, 0x8e, 0x04), []byte{0x81, 0x03})
	if d, err := h.drain(ms(2000)); err != nil || d != (drained{}) {
		t.Fatalf("drain of a frozen report = %+v, %v; want nothing", d, err)
	}
	if h.beatGap.max != ms(300) || h.beatGap.last != ms(1400) {
		t.Errorf("beat gap %d, last beat %d after no heartbeat; want %d, %d", h.beatGap.max, h.beatGap.last, ms(300), ms(1400))
	}

	// A read error ends the drain with an error, after the reports before
	// it.
	h.read = fakeReads(full(0x30, 7, 0x8e, 0x04), nil, full(0x30, 8, 0x8e, 0x04))
	if d, err := h.drain(ms(2100)); !errors.Is(err, errHIDRead) || d != (drained{beats: 1, idle: 1}) {
		t.Errorf("drain with a failed read = %+v, %v; want 1 idle heartbeat, errHIDRead", d, err)
	}
	if h.idleGap.max != ms(1000) {
		t.Errorf("idle gap %d, want %d", h.idleGap.max, ms(1000))
	}
}

// TestHIDBeatIdle: a heartbeat is idle when bytes 3 to 11 of its report,
// the buttons and the sticks, repeat those of the full report before it.
// The battery byte, the vibration code and the IMU samples may change; a
// report too short for the sticks is never idle, and the one after it has
// nothing to repeat.
func TestHIDBeatIdle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second []byte // after full(0x30, 1, 0x90, 0)
		d      drained
	}{
		{"same", full(0x30, 2, 0x90, 0), drained{beats: 1, idle: 1}},
		{"0x31 after 0x30", full(0x31, 2, 0x90, 0), drained{beats: 1, idle: 1}},
		{"battery", full(0x30, 2, 0x80, 0), drained{beats: 1, idle: 1}},
		{"vibration code", with(full(0x30, 2, 0x90, 0), 12, 0x70), drained{beats: 1, idle: 1}},
		{"IMU", with(full(0x30, 2, 0x90, 0), 20, 0x11), drained{beats: 1, idle: 1}},
		{"button", full(0x30, 2, 0x90, 0x08), drained{beats: 1}},
		{"last button byte", with(full(0x30, 2, 0x90, 0), 5, 0x40), drained{beats: 1}},
		{"left stick by one", with(full(0x30, 2, 0x90, 0), 6, 1), drained{beats: 1}},
		{"right stick by one", with(full(0x30, 2, 0x90, 0), 11, 1), drained{beats: 1}},
		{"frozen timer", full(0x30, 1, 0x90, 0), drained{}},
		{"short", full(0x30, 2, 0x90, 0)[:11], drained{beats: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &hidBeat{read: fakeReads(full(0x30, 1, 0x90, 0))}
			if d, err := h.drain(ms(10)); err != nil || d != (drained{beats: 1}) {
				t.Fatalf("first drain = %+v, %v; want 1 heartbeat, not idle", d, err)
			}
			h.read = fakeReads(tc.second)
			if d, err := h.drain(ms(20)); err != nil || d != tc.d {
				t.Errorf("drain = %+v, %v; want %+v", d, err, tc.d)
			}
		})
	}
	h := &hidBeat{read: fakeReads(full(0x30, 1, 0x90, 0), full(0x30, 2, 0x90, 0)[:11], full(0x30, 3, 0x90, 0))}
	if d, err := h.drain(ms(10)); err != nil || d != (drained{beats: 3}) {
		t.Errorf("drain after a short report = %+v, %v; want 3 heartbeats, none idle", d, err)
	}
}

// TestHIDBeatDrainBounds: a drain reads at most hidDrainMax reports, so a
// flood cannot hold T-input, and a report too short for its timer is not
// a heartbeat.
func TestHIDBeatDrainBounds(t *testing.T) {
	n := 0
	h := &hidBeat{read: func(b []byte) int {
		n++
		b[0], b[1] = 0x30, byte(n)
		return 64
	}}
	d, err := h.drain(ms(10))
	if err != nil || d.beats != hidDrainMax || n != hidDrainMax {
		t.Errorf("drain of a flood = %+v, %v after %d reads; want %d heartbeats", d, err, n, hidDrainMax)
	}
	h = &hidBeat{read: fakeReads([]byte{0x30})}
	if d, err := h.drain(ms(10)); err != nil || d != (drained{}) || h.other != 1 {
		t.Errorf("drain of a 1-byte 0x30 = %+v, %v, other %d; want nothing, nil, 1", d, err, h.other)
	}
}

func TestHIDBeatDrainAllocs(t *testing.T) {
	var timer byte
	h := &hidBeat{read: func(b []byte) int {
		if timer%4 == 3 {
			timer++
			return 0
		}
		timer++
		b[0], b[1], b[2], b[6] = 0x30, timer, 0x90, timer%3
		if timer%8 == 6 {
			b[0], b[1] = 0x3f, timer
		}
		return 64
	}}
	now := uint64(0)
	if n := testing.AllocsPerRun(100, func() {
		now += ms(5)
		if _, err := h.drain(now); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("drain allocates %v times per call, want 0", n)
	}
}

func TestHIDBeatClose(t *testing.T) {
	closed := 0
	h := &hidBeat{read: fakeReads(), closeFn: func() { closed++ }}
	h.close()
	if closed != 1 {
		t.Errorf("closeFn ran %d times, want 1", closed)
	}
	(&hidBeat{read: fakeReads()}).close() // no closeFn: nothing to do
}
