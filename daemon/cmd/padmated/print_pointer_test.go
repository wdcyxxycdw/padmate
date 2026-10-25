package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func move(dx, dy int32) output.KeyEvent {
	return output.KeyEvent{Mouse: keys.MouseMove, Dx: dx, Dy: dy}
}

func jump(x, y int32) output.KeyEvent {
	return output.KeyEvent{Mouse: keys.MouseJump, Dx: x, Dy: y}
}

func keyEvent(t *testing.T, spec string, up bool) output.KeyEvent {
	t.Helper()
	e, err := output.KeyEventFor(keys.MustParse(spec).Key(), up)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// sendAt sends evs at msec.
func sendAt(t *testing.T, pr *printer, clk *clock.Fake, msec int, evs ...output.KeyEvent) {
	t.Helper()
	clk.Set(clock.Time(time.Duration(msec) * time.Millisecond))
	if n, err := pr.Send(evs); err != nil || n != len(evs) {
		t.Fatalf("Send = %d, %v; want all %d", n, err, len(evs))
	}
}

// The dry run sums the moves of a window of 100 ms from the first of them into
// one line at the time of that first move; a move after the window starts the
// next; the flush at the end of a replay prints the last.
func TestPrinterSumsMoves(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	for _, at := range []int{8, 16, 24, 100, 107} {
		sendAt(t, pr, clk, at, move(8, -2))
	}
	sendAt(t, pr, clk, 108, move(1, 1)) // the window of 8 ms is over at 108
	sendAt(t, pr, clk, 150, move(-1, 0))
	if out.Len() != 0 {
		t.Fatalf("a replay wrote %q before the flush", out.String())
	}
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       8.000    move +40,-10\n     108.000    move +0,+1\n"
	if out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}

// Moves that cancel print nothing.
func TestPrinterMovesThatCancel(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 8, move(4, 3))
	sendAt(t, pr, clk, 16, move(-4, -3))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("output %q, want none", out.String())
	}
}

// Any other line ends the window first, so the lines stay in the order of their
// times: a key line, a decision, a note. Keys and moves in one send keep the
// order of their events.
func TestPrinterMovesKeepOrder(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 8, move(8, 0))
	sendAt(t, pr, clk, 10, keyEvent(t, "Z", false))
	sendAt(t, pr, clk, 16, move(8, 0))
	sendAt(t, pr, clk, 17, move(8, 0))
	clk.Set(clock.Time(18 * time.Millisecond))
	pr.exit() // a note
	sendAt(t, pr, clk, 20, keyEvent(t, "Z", true), move(1, 2), keyEvent(t, "X", false), move(3, 4), move(3, 4), keyEvent(t, "X", true))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       8.000    move +8,+0\n" +
		"      10.000    send +Z\n" +
		"      16.000    move +16,+0\n" +
		"      18.000  exit: release all\n" +
		"      20.000    send -Z\n" +
		"      20.000    move +1,+2\n" +
		"      20.000    send +X\n" +
		"      20.000    move +6,+8\n" +
		"      20.000    send -X\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A jump prints at once, after the moves before it, and a wheel release prints
// nothing as ever.
func TestPrinterJump(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 8, move(8, 0))
	sendAt(t, pr, clk, 12, jump(32768, 32768))
	sendAt(t, pr, clk, 16, move(2, 0), keyEvent(t, "WheelUp", true))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       8.000    move +8,+0\n      12.000    jump 32768,32768\n      16.000    move +2,+0\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// With the slot column the lines line up like the send lines.
func TestPrinterMovesWithSlots(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	pr.showSlots()
	sendAt(t, pr, clk, 8, move(8, 0))
	sendAt(t, pr, clk, 12, jump(1, 2))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       8.000          move +8,+0\n      12.000          jump 1,2\n"
	if out.String() != want {
		t.Errorf("output:\n%q\nwant:\n%q", out.String(), want)
	}
}

// The JSON lines carry the sum and the jump.
func TestPrinterMovesJSON(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, true, false)
	sendAt(t, pr, clk, 8, move(8, 0))
	sendAt(t, pr, clk, 16, move(8, -1))
	sendAt(t, pr, clk, 20, keyEvent(t, "Z", false))
	sendAt(t, pr, clk, 24, jump(7, 9))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := `{"t":8,"move":[16,-1]}` + "\n" + `{"t":20,"send":["+Z"]}` + "\n" + `{"t":24,"jump":[7,9]}` + "\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// In live mode the last line of a stick that was let go comes out when its
// window is over, with no event to push it.
func TestPrinterMovesLiveTimer(t *testing.T) {
	var w writes
	clk := clock.NewFake()
	pr := testPrinter(t, &w, clk, false, true)
	sendAt(t, pr, clk, 8, move(8, 0))
	sendAt(t, pr, clk, 16, move(8, 0))
	if len(w.chunks) != 0 {
		t.Fatalf("a window printed at once: %q", w.chunks)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pr.mu.Lock()
		n := len(w.chunks)
		pr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	pr.mu.Lock()
	got := strings.Join(w.chunks, "")
	pr.mu.Unlock()
	if want := "       8.000    move +16,+0\n"; got != want {
		t.Errorf("the timer printed %q, want %q", got, want)
	}
	// A flush after the timer has nothing left to print.
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	pr.mu.Lock()
	n := len(w.chunks)
	pr.mu.Unlock()
	if n != 1 {
		t.Errorf("%d writes after the flush, want 1", n)
	}
}
