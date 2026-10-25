package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// typeEvents returns the Send batches of typing text: a press and a release of each
// UTF-16 unit, a surrogate pair together, as TypeText sends them.
func typeEvents(text string) [][]output.KeyEvent {
	var batches [][]output.KeyEvent
	for _, r := range text {
		var units []uint16
		if r >= 0x10000 {
			r1 := 0xD800 + (r-0x10000)>>10
			r2 := 0xDC00 + (r-0x10000)&0x3FF
			units = []uint16{uint16(r1), uint16(r2)}
		} else {
			units = []uint16{uint16(r)}
		}
		var batch []output.KeyEvent
		for _, u := range units {
			batch = append(batch, output.UnicodeEvent(u, false), output.UnicodeEvent(u, true))
		}
		batches = append(batches, batch)
	}
	return batches
}

// typeAt sends the characters of text one by one, step ms apart from msec.
func typeAt(t *testing.T, pr *printer, clk *clock.Fake, msec, step int, text string) int {
	t.Helper()
	for _, batch := range typeEvents(text) {
		sendAt(t, pr, clk, msec, batch...)
		msec += step
	}
	return msec
}

// The characters of a text are one line at the time of the first, quoted so
// that it stays one line; the line comes out at the flush of a replay.
func TestPrinterJoinsTypedText(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	typeAt(t, pr, clk, 5, 20, "Hi 你好 😀!")
	if out.Len() != 0 {
		t.Fatalf("a replay wrote %q before the flush", out.String())
	}
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	if want := "       5.000    text \"Hi 你好 😀!\"\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}

// A pause of textJoin or more starts the next line; a line that is full ends.
func TestPrinterTypedTextLines(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	end := typeAt(t, pr, clk, 0, 100, "abc") // the last character at 200
	typeAt(t, pr, clk, end-100+int(textJoin/time.Millisecond), 10, "de")
	long := strings.Repeat("x", textLineMax+5)
	typeAt(t, pr, clk, 2000, 1, long)
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       0.000    text \"abc\"\n" +
		"     450.000    text \"de\"\n" +
		"    2000.000    text \"" + strings.Repeat("x", textLineMax) + "\"\n" +
		"    2080.000    text \"xxxxx\"\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Just under textJoin joins; exactly textJoin does not.
func TestPrinterTypedTextJoinEdge(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	join := int(textJoin / time.Millisecond)
	sendAt(t, pr, clk, 0, typeEvents("a")[0]...)
	sendAt(t, pr, clk, join-1, typeEvents("b")[0]...)
	sendAt(t, pr, clk, 2*join-1, typeEvents("c")[0]...)
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       0.000    text \"ab\"\n     499.000    text \"c\"\n"
	if out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}

// Any other line ends the text first, so the lines stay in the order of their
// times: a key line (a newline is an Enter tap, a send of its own), a decision, a
// note; and the text goes on in a new line afterwards.
func TestPrinterTypedTextKeepsOrder(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	typeAt(t, pr, clk, 0, 10, "ab")
	sendAt(t, pr, clk, 20, keyEvent(t, "Enter", false), keyEvent(t, "Enter", true))
	typeAt(t, pr, clk, 30, 10, "cd")
	clk.Set(clock.Time(50 * time.Millisecond))
	pr.exit() // a note
	typeAt(t, pr, clk, 60, 10, "e")
	// A text and a key in one send keep the order of their events.
	sendAt(t, pr, clk, 70, append(append([]output.KeyEvent{keyEvent(t, "Z", false)}, typeEvents("f")[0]...), keyEvent(t, "Z", true))...)
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       0.000    text \"ab\"\n" +
		"      20.000    send +Enter -Enter\n" +
		"      30.000    text \"cd\"\n" +
		"      50.000  exit: release all\n" +
		"      60.000    text \"e\"\n" +
		"      70.000    send +Z\n" +
		"      70.000    text \"f\"\n" +
		"      70.000    send -Z\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A character the Sender was given with nothing else is a line of text only; a
// release alone prints nothing; and the quoting leaves nothing that could break
// the line.
func TestPrinterTypedTextQuoting(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 1, output.UnicodeEvent('a', true)) // a release alone
	sendAt(t, pr, clk, 2, typeEvents("a\"b\\c")[0]...)
	for i, batch := range typeEvents("\"\\\u200b\x7f") {
		sendAt(t, pr, clk, 3+i, batch...)
	}
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       2.000    text \"a\\\"\\\\\\u200b\\x7f\"\n"
	if out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("the text made %d lines", strings.Count(out.String(), "\n"))
	}
}

// With the slot column the line lines up like the send lines.
func TestPrinterTypedTextWithSlots(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	pr.showSlots()
	typeAt(t, pr, clk, 8, 10, "hi")
	sendAt(t, pr, clk, 40, keyEvent(t, "Z", false))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       8.000          text \"hi\"\n      40.000          send +Z\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// The JSON lines carry the text, with the time of its first character, and keep
// the order among the sends.
func TestPrinterTypedTextJSON(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, true, false)
	typeAt(t, pr, clk, 8, 10, "a\"你")
	sendAt(t, pr, clk, 30, keyEvent(t, "Enter", false))
	typeAt(t, pr, clk, 40, 10, "😀")
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := `{"t":8,"text":"a\"你"}` + "\n" + `{"t":30,"send":["+Enter"]}` + "\n" + `{"t":40,"text":"😀"}` + "\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Moves and typed text each wait for their own end; when both are pending the one
// that began first is printed first, whichever ends the other.
func TestPrinterTypedTextAndMoves(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 4, move(5, 0))
	typeAt(t, pr, clk, 10, 10, "ab")
	sendAt(t, pr, clk, 30, move(5, 0))
	// The window of moves is over at 104: a move after it ends it, and the text
	// that began later is still being joined.
	sendAt(t, pr, clk, 110, move(1, 1))
	typeAt(t, pr, clk, 120, 10, "c")
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       4.000    move +10,+0\n" +
		"      10.000    text \"abc\"\n" +
		"     110.000    move +1,+1\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}

	// The other way round: the text began first, so it is printed before the
	// moves that the next line ends.
	out.Reset()
	clk = clock.NewFake()
	pr = testPrinter(t, &out, clk, false, false)
	typeAt(t, pr, clk, 4, 10, "x")
	sendAt(t, pr, clk, 6, move(2, 0))
	sendAt(t, pr, clk, 7, move(2, 0))
	sendAt(t, pr, clk, 200, move(1, 0)) // ends the window of moves, which began at 6
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want = "       4.000    text \"x\"\n" +
		"       6.000    move +4,+0\n" +
		"     200.000    move +1,+0\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// // In live mode the end of a text comes out textJoin after its last character,
// with no event to push it, and a character that comes meanwhile postpones it.
func TestPrinterTypedTextLiveTimer(t *testing.T) {
	var w writes
	clk := clock.NewFake()
	pr := testPrinter(t, &w, clk, false, true)
	// The writes happen under the printer's lock, so the test reads them under it.
	chunks := func() []string {
		pr.mu.Lock()
		defer pr.mu.Unlock()
		return append([]string(nil), w.chunks...)
	}
	sendAt(t, pr, clk, 8, typeEvents("a")[0]...)
	time.Sleep(textJoin / 2)
	sendAt(t, pr, clk, 18, typeEvents("b")[0]...)
	if got := chunks(); len(got) != 0 {
		t.Fatalf("a line printed at once: %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(chunks()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got, want := strings.Join(chunks(), ""), "       8.000    text \"ab\"\n"; got != want {
		t.Errorf("the timer printed %q, want %q", got, want)
	}
	// A flush after the timer has nothing left to print, and the timer of the
	// first character, which fired before the second came, printed nothing.
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(textJoin / 2)
	if got := chunks(); len(got) != 1 {
		t.Errorf("%d writes after the flush, want 1: %q", len(got), got)
	}
}

// The media and volume keys (OUT-20) print by name, as every key does: the
// press with +, the release with -, a chord's modifiers around them.
func TestPrinterMediaKeys(t *testing.T) {
	var out bytes.Buffer
	clk := clock.NewFake()
	pr := testPrinter(t, &out, clk, false, false)
	sendAt(t, pr, clk, 5, keyEvent(t, "VolumeUp", false), keyEvent(t, "VolumeUp", true))
	sendAt(t, pr, clk, 9, keyEvent(t, "LCtrl", false), keyEvent(t, "MediaNext", false))
	sendAt(t, pr, clk, 12, keyEvent(t, "MediaNext", true), keyEvent(t, "LCtrl", true))
	if err := pr.flush(); err != nil {
		t.Fatal(err)
	}
	want := "       5.000    send +VolumeUp -VolumeUp\n" +
		"       9.000    send +LCtrl +MediaNext\n" +
		"      12.000    send -MediaNext -LCtrl\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}
