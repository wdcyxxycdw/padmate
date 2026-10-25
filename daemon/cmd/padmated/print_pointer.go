package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// The dry-run text for the pointer (ADR-0062, section 5). A stick in mode
// "mouse" sends a move every 8 ms, which as one line each would bury the rest of
// the output, so the printer sums the moves of a window of moveWindow, starting
// at the first of them, and prints one line, such as "move +46,-12", at the time
// that first move was sent. The line comes out
//   - when a move arrives after the window is over,
//   - before any other line, so that the lines stay in the order of their times,
//   - when the printer is flushed, at the end of a replay, and
//   - in live mode, when the window is over, by a timer, so that the last line
//     of a stick that was let go comes out without waiting for the next event.
//
// The sums are of every slot together: the send lines show no slot either.
// A jump (the "jump" mouse action) is not summed, it prints at once. padmatectl
// trace prints every move, for debugging.
const moveWindow = 100 * time.Millisecond

// pointerWindow is the window of moves being summed.
type pointerWindow struct {
	set    bool
	start  clock.Time
	dx, dy int64
	gen    uint64 // counts the windows, for the timer of live mode
}

// wireMove is the JSON form of the moves of a window; wireJump of a jump.
type (
	wireMove struct {
		T    json.Number `json:"t"`
		Move [2]int64    `json:"move"`
	}
	wireJump struct {
		T    json.Number `json:"t"`
		Jump [2]int32    `json:"jump"`
	}
)

// pointerEvent adds e, a move or a jump, to the output. The caller holds p.mu.
func (p *printer) pointerEvent(e output.KeyEvent) {
	now := p.clk.Now()
	if e.Mouse == keys.MouseJump {
		p.flushMoves()
		switch {
		case p.jsonl:
			_ = p.enc.Encode(wireJump{T: json.Number(now.AppendMs(nil)), Jump: [2]int32{e.Dx, e.Dy}}) // the error sticks in p.w
		default:
			p.writeCell(now, fmt.Sprintf("jump %d,%d", e.Dx, e.Dy))
		}
		p.endLine()
		return
	}
	w := &p.pointer
	if w.set && now >= w.start.Add(moveWindow) {
		p.flushPointer()
	}
	if !w.set {
		w.set, w.start, w.dx, w.dy = true, now, 0, 0
		w.gen++
		if p.live {
			gen := w.gen
			time.AfterFunc(moveWindow, func() { p.flushWindow(gen) })
		}
	}
	w.dx += int64(e.Dx)
	w.dy += int64(e.Dy)
}

// flushWindow is the timer of live mode: it prints the window gen, unless it was
// printed already.
func (p *printer) flushWindow(gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pointer.set && p.pointer.gen == gen {
		p.flushPointer()
		p.endLine()
	}
}

// flushMoves prints what is pending before another line comes out: the window of
// summed moves and the typed text being joined (print_text.go), the one that
// began first first, so that the lines stay in the order of their times. The
// caller holds p.mu.
func (p *printer) flushMoves() {
	p.flushPointer()
	p.flushText()
}

// flushPointer prints the window of summed moves, if there is one, and before it
// the typed text that began earlier. The caller holds p.mu.
func (p *printer) flushPointer() {
	if t := &p.typed; t.set && p.pointer.set && t.start <= p.pointer.start {
		p.printText()
	}
	p.printMoves()
}

// printMoves prints the window of summed moves, if there is one, at the time of
// its first move. A window whose moves cancel prints nothing. The caller holds
// p.mu.
func (p *printer) printMoves() {
	w := &p.pointer
	if !w.set {
		return
	}
	w.set = false
	if w.dx == 0 && w.dy == 0 {
		return
	}
	if p.jsonl {
		_ = p.enc.Encode(wireMove{T: json.Number(w.start.AppendMs(nil)), Move: [2]int64{w.dx, w.dy}}) // the error sticks in p.w
		return
	}
	p.writeCell(w.start, fmt.Sprintf("move %+d,%+d", w.dx, w.dy))
	p.endLine()
}

// writeCell prints text as a send line does: the time, then the text two
// columns right of the input column.
func (p *printer) writeCell(t clock.Time, text string) {
	if p.slotCol {
		fmt.Fprintf(p.w, "%s          %s\n", padLeft(formatMs(t), p.timeW), text)
		return
	}
	fmt.Fprintf(p.w, "%s    %s\n", padLeft(formatMs(t), p.timeW), text)
}
