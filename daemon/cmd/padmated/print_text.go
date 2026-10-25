package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// The dry-run text for typed text (OUT-21, ADR-0070). TypeText sends a character
// at a time, a press and a release of its UTF-16 unit or units, which as one line
// each would bury the rest of the output, so the printer joins the characters of
// a text into one line, such as `text "你好"`, at the time of the first. The
// characters are put through strconv.Quote, so that a control character, a quote
// or an unprintable one is escaped and the line is one line. A character joins the
// line while it comes within textJoin of the one before and the line has fewer
// than textLineMax characters; the line comes out
//   - when a character arrives after textJoin, or the line is full, and starts the
//     next,
//   - before any other line, so that the lines stay in the order of their times (a
//     newline or a tab is an ordinary Enter or Tab and prints as a send line, so a
//     text with one is several lines),
//   - when the printer is flushed, at the end of a replay, and
//   - in live mode, textJoin after its last character, by a timer, so that the end
//     of a text comes out without waiting for the next event.
//
// The line shows no slot, like a send line. padmatectl trace prints a line for
// every character.
const (
	textJoin    = 250 * time.Millisecond
	textLineMax = 80
)

// typedText is the text being joined into a line.
type typedText struct {
	set   bool
	start clock.Time // the time of the first character
	last  clock.Time // the time of the latest
	chars strings.Builder
	n     int    // characters in chars
	gen   uint64 // counts the characters, for the timer of live mode
}

// wireText is the JSON form of a line of typed text.
type wireText struct {
	T    json.Number `json:"t"`
	Text string      `json:"text"`
}

// textEvent adds s, the character a Send typed, to the line being joined. The
// caller holds p.mu.
func (p *printer) textEvent(s string) {
	if s == "" {
		return // a release alone
	}
	now := p.clk.Now()
	t := &p.typed
	if t.set && (now >= t.last.Add(textJoin) || t.n >= textLineMax) {
		p.flushText()
	}
	if !t.set {
		t.set, t.start, t.n = true, now, 0
		t.chars.Reset()
	}
	t.last = now
	t.chars.WriteString(s)
	t.n += utf8.RuneCountInString(s)
	t.gen++
	if p.live {
		gen := t.gen
		time.AfterFunc(textJoin, func() { p.flushTextGen(gen) })
	}
}

// flushTextGen is the timer of live mode: it prints the line, unless a character
// came after the one that set the timer, or the line was printed already.
func (p *printer) flushTextGen(gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.typed.set && p.typed.gen == gen {
		p.flushText()
		p.endLine()
	}
}

// flushText prints the line being joined, if there is one, and before it the
// window of summed moves that began earlier (print_pointer.go). The caller holds
// p.mu.
func (p *printer) flushText() {
	if t := &p.typed; t.set && p.pointer.set && p.pointer.start <= t.start {
		p.printMoves()
	}
	p.printText()
}

// printText prints the line being joined, if there is one, at the time of its
// first character. The caller holds p.mu.
func (p *printer) printText() {
	t := &p.typed
	if !t.set {
		return
	}
	t.set = false
	text := t.chars.String()
	if p.jsonl {
		_ = p.enc.Encode(wireText{T: json.Number(t.start.AppendMs(nil)), Text: text}) // the error sticks in p.w
		p.endLine()
		return
	}
	p.writeCell(t.start, fmt.Sprintf("text %s", strconv.Quote(text)))
	p.endLine()
}
