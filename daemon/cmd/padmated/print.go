package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	eawidth "golang.org/x/text/width"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func (p *printer) layerChange(slot uint8, s engine.LayerState) {
	overlays := strings.Join(s.Overlays, ",")
	if overlays == "" {
		overlays = "-"
	}
	p.text(fmt.Sprintf("layer slot=%d base=%s overlays=%s color=%s\n", slot, s.Base, overlays, valueOrDash(s.Color)))
}

// fbShown is what a feedback line shows of the state of a slot: the printer
// prints a line when a pulse starts or this changes, so that a refresh that
// changes nothing is silent.
type fbShown struct {
	paused bool
	color  string
	mode   int
	led    bool
}

// fbStart is the state a slot starts in: not paused, no color, mode 1, the
// lights on.
var fbStart = fbShown{mode: 1, led: true}

// feedback prints what slot's engine asks the controller to show (M3,
// HUD-08): the mode, which the player lights show with the global lights
// switch on, whether the mapping is paused, and the pulses and the color, as the line
// "feedback slot=0 mode=1 paused pulse x1 210ms gap=100 color=#FFAA00" under
// the decision or note that caused it, at the time of the clock. The pulse and
// the color are what the global feedback switches leave of the state
// (config.json feedback.rumble, led and pause; ADR-0067), so a pause that is
// not shown prints no pulse and the color of the layer; "led=off" says the
// lights are not padmate's. A line is printed when a pulse starts or one of these changes, so
// a refresh that changes nothing is silent. The JSON lines of --decisions have
// none.
func (p *printer) feedback(slot uint8, f engine.Feedback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := fbShown{paused: f.Paused, color: f.Color, mode: f.Mode, led: f.LED}
	if p.fb == nil {
		p.fb = map[uint8]fbShown{}
	}
	last, ok := p.fb[slot]
	if !ok {
		last = fbStart
	}
	p.fb[slot] = now
	if p.jsonl || (!f.Pulse && now == last) {
		return
	}
	line := fmt.Sprintf("feedback slot=%d mode=%d", slot, f.Mode)
	if f.Paused {
		line += " paused"
	}
	if f.Pulse {
		line += fmt.Sprintf(" pulse x%d %dms gap=%d", f.Pulses, f.PulseMs, f.GapMs)
	}
	if f.Color != "" {
		line += " color=" + f.Color
	}
	if !f.LED {
		line += " led=off"
	}
	p.flushMoves()
	// Past a blank slot cell, like a send line.
	gap := "    "
	if p.slotCol {
		gap = "          "
	}
	fmt.Fprintf(p.w, "%s%s%s\n", padLeft(formatMs(p.clk.Now()), p.timeW), gap, line)
	p.endLine()
}

// haptic prints the haptic that slot's engine asks the controller for
// (MAP-25, HUD-10), as the line "haptic slot=0 tick" under the decision that
// caused it, at the time of the clock. The JSON lines of --decisions have
// decisions and sends only, so it prints none there.
func (p *printer) haptic(slot uint8, h engine.Haptic) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jsonl {
		return
	}
	p.flushMoves()
	// Past a blank slot cell, like a send line.
	gap := "    "
	if p.slotCol {
		gap = "          "
	}
	fmt.Fprintf(p.w, "%s%shaptic slot=%d %s\n", padLeft(formatMs(p.clk.Now()), p.timeW), gap, slot, h.Preset)
	p.endLine()
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printer is the dry-run output.Sender (OUT-09). It prints each batch of
// key events the Arbiter sends through a MenuMask (OUT-08) and, through
// the slots' hooks, each decision and device event, in the order they
// happen: a table for people, or JSON lines with --decisions. Send never
// fails, so the Arbiter never retries; the first write error sticks in the
// bufio.Writer, and flush returns it.
//
// It is safe for concurrent use: in live mode T-output sends while the
// slot goroutines trace. The mutex is held only while a line is formatted
// and written, never across a call out of the printer. In a replay all
// runs on the goroutine of Replay and the mutex is uncontended.
type printer struct {
	mu    sync.Mutex
	w     *bufio.Writer
	clk   clock.Clock
	jsonl bool
	enc   *json.Encoder // the send lines of jsonl
	// live flushes each line as it is written, for a person watching.
	live bool
	// slots are the devices the slots were given, for the device lines of
	// live mode.
	slots map[uint8]device
	// widths of the columns after time: input, reason, action, label,
	// binding, and with showSlots slot before them; owner, the last, is
	// not padded.
	timeW  int
	widths []int
	// slotCol is set by showSlots: rows and JSON decisions show the slot.
	slotCol bool
	// reloaded is the last generation whose reload was noted.
	reloaded uint32
	// paused is whether the mapping is paused, for the ComboHeld events of
	// a replay, each of which toggles it.
	paused bool
	// suspended is the reasons the system suspends the mapping for
	// (DMN-09), as the supervisor's OnSuspend last gave them in live mode;
	// a replay has none.
	suspended winsession.Reason
	// moved holds each slot's last event when it moved an axis: the
	// trigger edges it causes show its value. The slots trace
	// concurrently; a replay has slot 0 only.
	moved map[uint8]axisValue
	// pointer is the window of pointer moves being summed (print_pointer.go).
	pointer pointerWindow
	// typed is the typed text being joined into a line (print_text.go).
	typed typedText
	// fb is each slot's feedback state as the last feedback line showed it.
	fb map[uint8]fbShown
}

// axisValue is an axis and its value.
type axisValue struct {
	axis  control.Axis
	value int16
}

// newPrinter returns a printer to w, at the times of clk, with columns
// wide enough for prog and for times up to maxMs ms. With prog nil the
// columns fit no binding.
func newPrinter(w *bufio.Writer, clk clock.Clock, jsonl bool, prog *engine.Program, maxMs uint64) *printer {
	p := &printer{w: w, clk: clk, jsonl: jsonl, enc: json.NewEncoder(w), slots: map[uint8]device{}, moved: map[uint8]axisValue{}}
	p.enc.SetEscapeHTML(false)
	// "." and three decimals follow the whole ms.
	p.timeW = max(10, len(strconv.FormatUint(maxMs, 10))+4)
	p.widths = []int{len("input"), 0, len("action"), len("label"), len("binding")}
	for _, c := range control.All() {
		if c.IsVirtual() {
			continue
		}
		in := c.String() + " down"
		if c.IsTrigger() {
			in += " (32767)" // ReadEvents keeps trigger values in 0..32767
		}
		p.widths[0] = max(p.widths[0], len(in))
	}
	for r := engine.ReasonDispatch; r <= engine.ReasonScript; r++ {
		p.widths[1] = max(p.widths[1], len(r.String()))
	}
	if prog == nil {
		return p
	}
	for _, b := range prog.Bindings() {
		in := b.Binding.Control.String() + " down"
		if b.Binding.Control.IsTrigger() {
			in += " (32767)"
		}
		p.widths[0] = max(p.widths[0], len(in))
		if a := b.Binding.Press; a != nil {
			p.widths[2] = max(p.widths[2], width(a.String()))
		}
		if l := b.Binding.Label; l != "" {
			p.widths[3] = max(p.widths[3], width(strconv.Quote(l)))
		}
		p.widths[4] = max(p.widths[4], width(b.Binding.ID))
	}
	return p
}

// showSlots adds the slot column, for live mode with several slots
// (IN-08): decision rows show their slot after the time, and JSON
// decisions start with it. Call it before the first line.
func (p *printer) showSlots() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.slotCol {
		p.slotCol = true
		p.widths = append([]int{len("slot")}, p.widths...)
	}
}

// columnHeader prints the names of the columns.
func (p *printer) columnHeader() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.slotCol {
		p.row("time", "slot", "input", "reason", "action", "label", "binding", "owner")
		return
	}
	p.row("time", "input", "reason", "action", "label", "binding", "owner")
}

// text prints s as it is, such as the lines of a header.
func (p *printer) text(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushMoves()
	p.w.WriteString(s) //nolint:errcheck // the error sticks in p.w
	p.endLine()
}

// Send prints evs as one line, such as "send +LCtrl +Z", or as
// {"t":20,"send":["+LCtrl","+Z"]}. It reports every event sent. A send
// shows no slot: the up of a key that slots share comes from the last to
// let go, and a retry mixes owners. A wheel key prints its press, such as
// "+WheelUp", and leaves its release out, since nothing is sent for it
// (KeyEvent.Silent); a send of nothing else prints no line. A pointer move is
// not a key: the moves are summed into a line of their own, such as
// "move +46,-12", at most one in 100 ms (print_pointer.go), and a jump prints
// as "jump 32768,32768". They keep their order among the keys of the send. A
// character of a typed text (the Unicode events of OUT-21) is not a key either:
// the characters of a text are joined into a line of their own, such as
// `text "你好"` (print_text.go), and keep their order among the keys too.
func (p *printer) Send(evs []output.KeyEvent) (int, error) {
	var names []string
	for _, e := range evs {
		if !e.Silent() && !e.Pointer() && !e.Unicode {
			names = append(names, edgeName(e))
		}
	}
	pointer := slices.ContainsFunc(evs, output.KeyEvent.Pointer)
	typed := slices.ContainsFunc(evs, func(e output.KeyEvent) bool { return e.Unicode })
	if len(names) == 0 && !pointer && !typed {
		return len(evs), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !pointer && !typed {
		p.sendLine(names)
		return len(evs), nil
	}
	// Keys, pointer events and typed characters in one send: the lines come out
	// in the order of the events. The Unicode events up to the next other event
	// are one character.
	names = names[:0]
	for i := 0; i < len(evs); i++ {
		e := evs[i]
		switch {
		case e.Silent():
		case e.Pointer():
			p.sendLine(names)
			names = names[:0]
			p.pointerEvent(e)
		case e.Unicode:
			j := i
			for j+1 < len(evs) && evs[j+1].Unicode {
				j++
			}
			p.sendLine(names)
			names = names[:0]
			p.textEvent(output.TypedText(evs[i : j+1]))
			i = j
		default:
			names = append(names, edgeName(e))
		}
	}
	p.sendLine(names)
	return len(evs), nil
}

// sendLine prints names as one send line, after the moves summed so far. It
// prints nothing for no names. The caller holds p.mu.
func (p *printer) sendLine(names []string) {
	if len(names) == 0 {
		return
	}
	p.flushMoves()
	now := p.clk.Now()
	switch {
	case p.jsonl:
		_ = p.enc.Encode(wireSend{T: json.Number(now.AppendMs(nil)), Send: names}) // the error sticks in p.w
	case p.slotCol:
		// Past a blank slot cell: "send" stays 2 columns right of the input
		// column.
		fmt.Fprintf(p.w, "%s          send %s\n", padLeft(formatMs(now), p.timeW), strings.Join(names, " "))
	default:
		fmt.Fprintf(p.w, "%s    send %s\n", padLeft(formatMs(now), p.timeW), strings.Join(names, " "))
	}
	p.endLine()
}

// wireSend is the JSON form of a batch of key events.
type wireSend struct {
	T    json.Number `json:"t"`
	Send []string    `json:"send"`
}

// decision prints d, the Trace of slot. With showSlots the row starts with
// the slot, and so does the JSON object.
func (p *printer) decision(slot uint8, d engine.Decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushMoves()
	if p.jsonl {
		b, _ := d.MarshalJSON() // no error: every field encodes
		// b is an object whose first key is "t", never empty.
		if p.slotCol && len(b) > 2 && b[0] == '{' && b[1] != '}' {
			p.w.WriteString(`{"slot":` + strconv.Itoa(int(slot)) + ",") //nolint:errcheck // the error sticks in p.w
			b = b[1:]
		}
		p.w.Write(b)        //nolint:errcheck // the error sticks in p.w
		p.w.WriteByte('\n') //nolint:errcheck // the error sticks in p.w
		p.endLine()
		return
	}
	in := d.Control.String()
	if d.Edge != engine.EdgeNone {
		in += " " + d.Edge.String()
		if m, ok := p.moved[slot]; ok && d.Control.IsTrigger() && m.axis == d.Control.Axis() {
			in += " (" + strconv.Itoa(int(m.value)) + ")"
		}
	}
	var action, label, binding, owner string
	if a := d.Action; a != nil {
		action = a.String()
	}
	if b := d.Binding; b != nil {
		binding = b.ID
		if b.Label != "" {
			label = strconv.Quote(b.Label)
		}
	}
	if d.Owner != (engine.OwnerID{}) {
		owner = d.Owner.String()
	}
	if p.slotCol {
		p.row(formatMs(d.T), strconv.Itoa(int(slot)), in, d.Reason.String(), action, label, binding, owner)
		return
	}
	p.row(formatMs(d.T), in, d.Reason.String(), action, label, binding, owner)
}

// event notes ev, the slot's OnEvent in a replay, before the slot handles
// it: "device 1 added", "device 1 removed: release all", and for a
// ComboHeld, which toggles the pause (DMN-07), the pause's note.
func (p *printer) event(ev input.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.track(0, ev)
	switch ev.Kind {
	case input.DeviceAdded:
		p.note(fmt.Sprintf("device %d added", ev.Device))
	case input.DeviceRemoved:
		p.note(fmt.Sprintf("device %d removed: release all", ev.Device))
	case input.ComboHeld:
		p.setPaused(!p.paused)
	}
}

// slotEvent notes ev, the supervisor's OnEvent in live mode, before slot
// handles it: `device 3 "Xbox Controller" added to slot 0` and, with the
// name it was added with, `device 3 "Xbox Controller" removed from slot
// 0: release all`.
func (p *printer) slotEvent(slot uint8, ev input.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.track(slot, ev)
	switch ev.Kind {
	case input.DeviceAdded:
		d := device{id: ev.Device}
		if ev.Info != nil {
			d.name = ev.Info.Name
		}
		p.slots[slot] = d
		p.note(fmt.Sprintf("%s added to slot %d", d, slot))
	case input.DeviceRemoved:
		d := device{id: ev.Device}
		if known, ok := p.slots[slot]; ok && known.id == ev.Device {
			d = known
		}
		delete(p.slots, slot)
		p.note(fmt.Sprintf("%s removed from slot %d: release all", d, slot))
	}
}

// track remembers whether ev, an event of slot, moved an axis, for the
// trigger edges it causes.
func (p *printer) track(slot uint8, ev input.Event) {
	if a, ok := control.ParseAxis(ev.Control); ok && ev.Kind == input.AxisMotion {
		p.moved[slot] = axisValue{a, ev.Value}
	} else {
		delete(p.moved, slot)
	}
}

// reload notes a reload of the profile, the supervisor's OnReload in live
// mode, before the slot lets go of every key it holds. Every slot calls it
// with the generation it switches to, and the first call of a generation
// prints the note, so it precedes every release of that reload; rows of
// slots not switched yet may follow it. The columns keep the widths of the
// profile loaded at the start.
func (p *printer) reload(_ uint8, gen uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if gen > p.reloaded {
		p.reloaded = gen
		p.note("profile reloaded: release all")
	}
}

// pause notes a pause of the mapping, "mapping paused: release all", or a
// resume, "mapping resumed" (setPaused), the supervisor's OnPause in live
// mode (DMN-06, DMN-07). The log says what did it. The router calls it
// after it has asked T-output to let go of every key (output.Loop.Block),
// and T-input asks before it sends the ComboHeld, so that release may
// come before the note or after it, as may the slots' cancels. In a
// replay the ComboHeld's note comes first (event).
func (p *printer) pause(paused bool, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setPaused(paused)
}

// setPaused notes the pause or the resume of the mapping. While the
// system suspends the mapping, the pause releases nothing and the resume
// maps nothing yet: "mapping paused (still suspended: lock)", "mapping
// resumed (still suspended: lock)".
func (p *printer) setPaused(paused bool) {
	p.paused = paused
	text := "mapping resumed"
	if paused {
		text = "mapping paused"
	}
	switch {
	case p.suspended != 0:
		text += " (still suspended: " + p.suspended.String() + ")"
	case paused:
		text += ": release all"
	}
	p.note(text)
}

// suspend notes a change of the reasons the system suspends the mapping
// for (DMN-09), was to now, the supervisor's OnSuspend in live mode:
// "mapping suspended (lock): release all" when it starts, only "mapping
// suspended (lock)" when the pause has released every key already, or
// when the reasons change, such as "mapping suspended (lock,desktop)";
// "mapping unsuspended" when it ends, or "mapping unsuspended (still
// paused)". The router calls it after Supervisor.Suspend has asked
// T-output to let go of every key, so that release may come before the
// note, as may the slots' cancels. A suspension the router saw only once
// it had ended gives both notes.
func (p *printer) suspend(was, now winsession.Reason) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.suspended = now
	switch {
	case now == was:
	case now != 0 && (was != 0 || p.paused):
		p.note("mapping suspended (" + now.String() + ")")
	case now != 0:
		p.note("mapping suspended (" + now.String() + "): release all")
	case p.paused:
		p.note("mapping unsuspended (still paused)")
	default:
		p.note("mapping unsuspended")
	}
}

// exit notes the start of the exit path, which releases every key.
func (p *printer) exit() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.note("exit: release all")
}

// flush writes out what is buffered and returns the first write error.
func (p *printer) flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushMoves()
	return p.w.Flush()
}

// endLine ends a line: in live mode it goes out at once. The error sticks
// in p.w.
func (p *printer) endLine() {
	if p.live {
		_ = p.w.Flush()
	}
}

// note prints text on a line of its own, at the clock's Now. JSON lines
// have no notes.
func (p *printer) note(text string) {
	if !p.jsonl {
		p.row(formatMs(p.clk.Now()), text)
	}
}

// row prints one row of the table: time right-aligned, the other cells
// padded to their column, two spaces apart, with no trailing spaces.
func (p *printer) row(t string, cells ...string) {
	p.flushMoves()
	var b strings.Builder
	b.WriteString(padLeft(t, p.timeW))
	for i, c := range cells {
		b.WriteString("  ")
		if i < len(p.widths) {
			c = pad(c, p.widths[i])
		}
		b.WriteString(c)
	}
	p.w.WriteString(strings.TrimRight(b.String(), " ") + "\n") //nolint:errcheck // the error sticks in p.w
	p.endLine()
}

// edgeName names a key event as a send line shows it: + for a press, -
// for a release, then the key's name, or its code when it has none, such
// as "+LCtrl", "-Z", "+sc:0x2C", "+vk:0x13" or "+vk:0xE8", the menu mask
// key (OUT-08), or "+WheelUp" for a mouse key.
func edgeName(e output.KeyEvent) string {
	sign := "+"
	if e.Up {
		sign = "-"
	}
	if e.Key.IsValid() {
		return sign + e.Key.String()
	}
	return sign + e.ID().String()
}

// formatMs formats t, which is not negative, in milliseconds with three
// decimals, such as "512.304"; it drops what is below a microsecond.
func formatMs(t clock.Time) string {
	us := int64(t) / 1000
	return fmt.Sprintf("%d.%03d", us/1000, us%1000)
}

// width returns the number of terminal columns s takes: two for wide and
// fullwidth characters, such as CJK, none for combining marks and format
// characters, one for the rest. A character of one column followed by
// U+FE0F, the emoji presentation selector, takes two, as in "✏\uFE0F".
func width(s string) int {
	n := 0
	narrow := false // the last character counted took one column
	for _, r := range s {
		if r == '\uFE0F' && narrow {
			n++
			narrow = false
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
			continue
		}
		switch eawidth.LookupRune(r).Kind() {
		case eawidth.EastAsianWide, eawidth.EastAsianFullwidth:
			n += 2
			narrow = false
		default:
			n++
			narrow = true
		}
	}
	return n
}

// pad pads s with spaces to w columns.
func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

// padLeft pads s with spaces on the left to w columns.
func padLeft(s string, w int) string {
	return strings.Repeat(" ", max(0, w-width(s))) + s
}
