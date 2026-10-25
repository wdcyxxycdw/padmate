package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

const recordUsage = `usage: padmate-probe record [--json] [--mouse [--nonce N]] [--swallow none|padmate|injected] [--duration D] [--count N]

Installs a low-level keyboard hook and prints every keyboard event it sees,
physical or injected, one per line, until Ctrl+C, the duration or the count
ends it. Prints "ready" to stderr once the hook is installed, so scripts
can wait for it before injecting.

A line shows the hook time in milliseconds, down or up, the scan code
("E0 1D" for an E0-extended key), the VK, the flags (sys for
WM_SYSKEYDOWN/UP, injected, lowerIL, alt), the nonce of padmate-tagged
events or any other dwExtraInfo, and whether the hook swallowed the event.

With --mouse a low-level mouse hook on the same thread prints the mouse
events too, in the order the hook thread saw them. A wheel event shows as
"wheel v +120" (vertical, up is positive) or "wheel h -120" (horizontal,
right is positive), a button as "mouse WM_LBUTTONDOWN", and so on, with the
flags, extra and swallowed of a key. The physical mouse's moves are never
printed (an injected move is); its wheel and buttons are. A mouse hook sees
only the low 32 bits of dwExtraInfo (Windows drops the rest): of the events
padmate injects it sees the nonce, not the tag, so a mouse event never shows
as "padmate". Its JSON "nonce" is the low 32 bits of the extra of an injected
mouse event.

flags:
  --json        print one JSON object per event with the fields seq, time,
                msg, vk, scan, ext, up, injected, lowerIL, altDown, extra,
                padmate, nonce and swallowed; a mouse event has the fields
                seq, kind ("mouse"), time, msg, delta (the wheel delta, 0
                for any other event), mouseData ("0x..."), xButton (the
                number of an extra button, else 0), injected, lowerIL,
                extra, padmate (the whole tag arrived), nonce and swallowed
  --mouse       also hook the mouse (see above)
  --nonce N     with --mouse and --swallow padmate: the nonce of the mouse
                events to swallow (see below)
  --swallow M   discard events so no application receives them:
                none (the default), padmate (events that padmate tagged)
                or injected (every injected event). The physical keyboard
                and mouse are never swallowed. With --mouse, padmate also
                discards the injected mouse events whose nonce, the low 32
                bits of their extra, is N: a mouse hook cannot see the tag,
                so --nonce is required, and injected mouse events of other
                nonces, other programs' too, are not discarded.
  --duration D  stop after D, such as 30s (default: run until Ctrl+C)
  --count N     stop after N events, of either kind (default: no limit)
`

// eventSource is a running hook: *winhook.Hook on Windows, a fake in
// tests. A successful Close closes the Events channel and the MouseEvents
// channel, which is nil without --mouse; after a failed one the channels stay
// open. Dropped counts the events of both.
type eventSource interface {
	Events() <-chan winhook.Event
	MouseEvents() <-chan winhook.MouseEvent
	Dropped() uint64
	Close() error
}

// recordOptions are the parsed flags of "record".
type recordOptions struct {
	json     bool
	duration time.Duration
	count    int
}

// swallowModes maps the --swallow values to hook predicates.
var swallowModes = map[string]func(winhook.Event) bool{
	"none":     nil,
	"padmate":  func(e winhook.Event) bool { return output.IsPadmate(e.Extra) },
	"injected": func(e winhook.Event) bool { return e.Injected() },
}

// mouseSwallow returns the predicate of the mouse hook for a --swallow mode.
// Both predicates discard injected events only: the extra of a physical event
// is the driver's to set, and the physical mouse must keep working whatever it
// says. A mouse hook gets the low 32 bits of dwExtraInfo, the nonce, and not
// the tag (measured 2026-10-19), so "padmate" can only mean the injected events
// whose nonce is the one asked for: the extra of another injector's event has
// a zero high half too, so the high half, 0 or the whole tag, is not a test.
func mouseSwallow(mode string, nonce uint32) func(winhook.MouseEvent) bool {
	switch mode {
	case "padmate":
		return func(e winhook.MouseEvent) bool {
			return e.Injected() && output.MayBePadmateMouse(e.Extra) && output.Nonce(e.Extra) == nonce
		}
	case "injected":
		return func(e winhook.MouseEvent) bool { return e.Injected() }
	}
	return nil
}

// watchMouse is the Watch predicate of the probe's mouse hook: every mouse
// event but a physical move, which the hook passes on at once without a copy.
func watchMouse(e winhook.MouseEvent) bool {
	return e.Msg != winhook.WMMouseMove || e.Injected()
}

func runRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON objects")
	swallow := fs.String("swallow", "none", "none, padmate or injected")
	duration := fs.Duration("duration", 0, "stop after this duration")
	count := fs.Int("count", 0, "stop after this many events")
	mouse := fs.Bool("mouse", false, "also hook the mouse")
	nonce := fs.Uint64("nonce", 0, "with --mouse --swallow padmate: the nonce of the mouse events to swallow")
	if code, ok := parseFlags(fs, args, recordUsage, stdout, stderr); !ok {
		return code
	}
	pred, known := swallowModes[*swallow]
	nonceSet := false
	fs.Visit(func(f *flag.Flag) { nonceSet = nonceSet || f.Name == "nonce" })
	mouseNonce := *mouse && *swallow == "padmate" // the mode that takes a nonce
	var bad string
	switch {
	case !known:
		bad = fmt.Sprintf("--swallow must be none, padmate or injected, not %q", *swallow)
	case *nonce > math.MaxUint32:
		bad = "--nonce must fit in 32 bits"
	case mouseNonce && !nonceSet:
		bad = "--mouse with --swallow padmate needs --nonce N: a mouse hook sees the nonce of padmate's events, not their tag"
	case nonceSet && !mouseNonce:
		bad = "--nonce only goes with --mouse and --swallow padmate"
	case *duration < 0:
		bad = "--duration must not be negative"
	case *count < 0:
		bad = "--count must not be negative"
	case fs.NArg() > 0:
		bad = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	}
	if bad != "" {
		fmt.Fprintln(stderr, "padmate-probe record:", bad)
		fmt.Fprint(stderr, recordUsage)
		return 2
	}

	var mouseOpts *winhook.MouseOptions
	if *mouse {
		mouseOpts = &winhook.MouseOptions{Watch: watchMouse, Swallow: mouseSwallow(*swallow, uint32(*nonce))}
	}
	src, err := startHook(pred, mouseOpts)
	switch {
	case errors.Is(err, errUnsupported):
		fmt.Fprintln(stderr, "padmate-probe: record is only supported on Windows")
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "padmate-probe record:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	opts := recordOptions{json: *asJSON, duration: *duration, count: *count}
	fmt.Fprintln(stderr, "ready")
	return record(ctx, src, opts, stdout, stderr)
}

// record prints the events of src until ctx is done, opts.duration has
// passed, opts.count events were printed or src closes its channels. It
// closes src and returns the exit code.
func record(ctx context.Context, src eventSource, opts recordOptions, stdout, stderr io.Writer) int {
	if opts.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.duration)
		defer cancel()
	}
	p := eventPrinter{w: stdout, json: opts.json}
	status := 0
	events := &stream{keys: src.Events(), mouse: src.MouseEvents()}
	for opts.count == 0 || p.seq < uint64(opts.count) {
		if ctx.Err() != nil {
			break
		}
		e, ok := events.next(ctx)
		if !ok {
			break
		}
		if err := p.print(e); err != nil {
			fmt.Fprintln(stderr, "padmate-probe record:", err)
			status = 1
			break
		}
	}
	if err := src.Close(); err != nil {
		// The hook may still be installed, swallowing nothing, and its
		// channels stay open: draining them would never end.
		fmt.Fprintln(stderr, "padmate-probe record:", err)
		return 1
	}
	// Print what the hook saw before it was removed, unless the count is
	// reached or printing failed. Close closes the channels, so next ends.
	for {
		e, ok := events.next(context.Background())
		if !ok {
			break
		}
		if status != 0 || opts.count > 0 && p.seq >= uint64(opts.count) {
			continue
		}
		if err := p.print(e); err != nil {
			fmt.Fprintln(stderr, "padmate-probe record:", err)
			status = 1
		}
	}
	if n := src.Dropped(); n > 0 {
		fmt.Fprintf(stderr, "padmate-probe record: the hook dropped %d events because the output could not keep up\n", n)
	}
	return status
}

// hookEvent is an event of either hook: exactly one of key and mouse is set.
type hookEvent struct {
	key   *winhook.Event
	mouse *winhook.MouseEvent
}

// stream merges the keyboard and mouse channels of a source into the order
// the hook thread saw the events, which their Seq numbers tell. Without
// --mouse the mouse channel is nil and the stream is the keyboard's.
type stream struct {
	keys  <-chan winhook.Event
	mouse <-chan winhook.MouseEvent
	// pk and pm hold an event received, and not yet returned, of each kind.
	pk *winhook.Event
	pm *winhook.MouseEvent
}

// next returns the next event, waiting for one until ctx is done. It reports
// false when ctx is done with none held, or both channels are closed and
// everything has been returned. Both channels are closed together by the
// hook's Close, so after a successful Close next never waits.
func (s *stream) next(ctx context.Context) (hookEvent, bool) {
	for s.pk == nil && s.pm == nil {
		if s.keys == nil && s.mouse == nil {
			return hookEvent{}, false
		}
		select {
		case <-ctx.Done():
			return hookEvent{}, false
		case e, ok := <-s.keys:
			if ok {
				s.pk = &e
			} else {
				s.keys = nil
			}
		case e, ok := <-s.mouse:
			if ok {
				s.pm = &e
			} else {
				s.mouse = nil
			}
		}
	}
	// An event of the other kind that came earlier is in its channel already:
	// the hook thread sent it before the one in hand. Look, without waiting.
	if s.pk != nil && s.pm == nil && s.mouse != nil {
		select {
		case e, ok := <-s.mouse:
			if ok {
				s.pm = &e
			} else {
				s.mouse = nil
			}
		default:
		}
	}
	if s.pm != nil && s.pk == nil && s.keys != nil {
		select {
		case e, ok := <-s.keys:
			if ok {
				s.pk = &e
			} else {
				s.keys = nil
			}
		default:
		}
	}
	if s.pm == nil || s.pk != nil && s.pk.Seq <= s.pm.Seq {
		e := s.pk
		s.pk = nil
		return hookEvent{key: e}, true
	}
	e := s.pm
	s.pm = nil
	return hookEvent{mouse: e}, true
}

// eventPrinter writes events in the human or the JSON format and numbers
// them.
type eventPrinter struct {
	w    io.Writer
	json bool
	seq  uint64
}

func (p *eventPrinter) print(e hookEvent) error {
	p.seq++
	if p.json {
		var v any
		if e.mouse != nil {
			v = newMouseEventJSON(p.seq, *e.mouse)
		} else {
			v = newEventJSON(p.seq, *e.key)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(p.w, "%s\n", b)
		return err
	}
	var line string
	if e.mouse != nil {
		line = formatMouseEvent(*e.mouse)
	} else {
		line = formatEvent(*e.key)
	}
	_, err := fmt.Fprintln(p.w, line)
	return err
}

// vkPacket is VK_PACKET, the VK of an event typed by KEYEVENTF_UNICODE (OUT-21).
const vkPacket = 0xE7

// formatEvent formats e in the human format, such as
// "  52830015 down sc=E0 1D vk=0xA3 injected padmate nonce=8812 swallowed".
// An event typed by KEYEVENTF_UNICODE adds " text U+4F60", the UTF-16 unit that
// the system puts in the scan code.
func formatEvent(e winhook.Event) string {
	var b strings.Builder
	dir := "down"
	if e.Up() {
		dir = "up"
	}
	fmt.Fprintf(&b, "%10d %-4s sc=%s vk=0x%02X", e.Time, dir, scanString(e), e.VK)
	if e.VK == vkPacket {
		// A key typed by KEYEVENTF_UNICODE (VK_PACKET): the scan code is the UTF-16 unit.
		fmt.Fprintf(&b, " text U+%04X", e.Scan&0xFFFF)
	}
	if e.Msg == winhook.WMSysKeyDown || e.Msg == winhook.WMSysKeyUp {
		b.WriteString(" sys")
	} else if e.Msg != winhook.WMKeyDown && e.Msg != winhook.WMKeyUp {
		b.WriteString(" msg=" + winhook.MsgName(e.Msg))
	}
	if e.Injected() {
		b.WriteString(" injected")
	}
	if e.LowerILInjected() {
		b.WriteString(" lowerIL")
	}
	if e.AltDown() {
		b.WriteString(" alt")
	}
	writeExtra(&b, e.Extra)
	if e.Swallowed {
		b.WriteString(" swallowed")
	}
	return b.String()
}

// writeExtra writes the nonce of a padmate-tagged dwExtraInfo, or any other
// non-zero value.
func writeExtra(b *strings.Builder, extra uintptr) {
	switch {
	case output.IsPadmate(extra):
		fmt.Fprintf(b, " padmate nonce=%d", output.Nonce(extra))
	case extra != 0:
		fmt.Fprintf(b, " extra=0x%X", uint64(extra))
	}
}

// formatMouseEvent formats e in the human format, such as
// "  52830015 wheel v +120 injected padmate nonce=8812 swallowed" (v is the
// vertical wheel and h the horizontal one) or
// "  52830016 mouse WM_XBUTTONDOWN x2 injected".
func formatMouseEvent(e winhook.MouseEvent) string {
	var b strings.Builder
	switch e.Msg {
	case winhook.WMMouseWheel:
		fmt.Fprintf(&b, "%10d wheel v %+d", e.Time, e.WheelDelta())
	case winhook.WMMouseHWheel:
		fmt.Fprintf(&b, "%10d wheel h %+d", e.Time, e.WheelDelta())
	default:
		fmt.Fprintf(&b, "%10d mouse %s", e.Time, winhook.MouseMsgName(e.Msg))
		if x := e.XButton(); x != 0 {
			fmt.Fprintf(&b, " x%d", x)
		}
	}
	if e.Injected() {
		b.WriteString(" injected")
	}
	if e.LowerILInjected() {
		b.WriteString(" lowerIL")
	}
	writeExtra(&b, e.Extra)
	if e.Swallowed {
		b.WriteString(" swallowed")
	}
	return b.String()
}

// scanString formats the scan code of e: "0x1D", or "E0 1D" when the
// hook reports it extended.
func scanString(e winhook.Event) string {
	if e.Ext() {
		return fmt.Sprintf("E0 %02X", e.Scan)
	}
	return fmt.Sprintf("0x%02X", e.Scan)
}

// eventJSON is one event in "record --json".
type eventJSON struct {
	Seq       uint64 `json:"seq"`
	Time      uint32 `json:"time"`
	Msg       string `json:"msg"`
	VK        uint32 `json:"vk"`
	Scan      uint32 `json:"scan"`
	Ext       bool   `json:"ext"`
	Up        bool   `json:"up"`
	Injected  bool   `json:"injected"`
	LowerIL   bool   `json:"lowerIL"`
	AltDown   bool   `json:"altDown"`
	Extra     string `json:"extra"`
	Padmate   bool   `json:"padmate"`
	Nonce     uint32 `json:"nonce"`
	Swallowed bool   `json:"swallowed"`
}

func newEventJSON(seq uint64, e winhook.Event) eventJSON {
	out := eventJSON{
		Seq:       seq,
		Time:      e.Time,
		Msg:       winhook.MsgName(e.Msg),
		VK:        e.VK,
		Scan:      e.Scan,
		Ext:       e.Ext(),
		Up:        e.Up(),
		Injected:  e.Injected(),
		LowerIL:   e.LowerILInjected(),
		AltDown:   e.AltDown(),
		Extra:     fmt.Sprintf("0x%X", uint64(e.Extra)),
		Padmate:   output.IsPadmate(e.Extra),
		Swallowed: e.Swallowed,
	}
	if out.Padmate {
		out.Nonce = output.Nonce(e.Extra)
	}
	return out
}

// mouseEventJSON is one mouse event in "record --json --mouse". It has no
// position: the probe prints wheel and button events, and a move only when it
// was injected.
type mouseEventJSON struct {
	Seq       uint64 `json:"seq"`
	Kind      string `json:"kind"`
	Time      uint32 `json:"time"`
	Msg       string `json:"msg"`
	Delta     int    `json:"delta"`
	MouseData string `json:"mouseData"`
	XButton   int    `json:"xButton"`
	Injected  bool   `json:"injected"`
	LowerIL   bool   `json:"lowerIL"`
	Extra     string `json:"extra"`
	Padmate   bool   `json:"padmate"`
	Nonce     uint32 `json:"nonce"`
	Swallowed bool   `json:"swallowed"`
}

func newMouseEventJSON(seq uint64, e winhook.MouseEvent) mouseEventJSON {
	out := mouseEventJSON{
		Seq:       seq,
		Kind:      "mouse",
		Time:      e.Time,
		Msg:       winhook.MouseMsgName(e.Msg),
		Delta:     e.WheelDelta(),
		MouseData: fmt.Sprintf("0x%X", e.MouseData),
		XButton:   e.XButton(),
		Injected:  e.Injected(),
		LowerIL:   e.LowerILInjected(),
		Extra:     fmt.Sprintf("0x%X", uint64(e.Extra)),
		Padmate:   output.IsPadmate(e.Extra), // false while Windows drops the tag
		Swallowed: e.Swallowed,
	}
	if e.Injected() {
		// A mouse hook gets the nonce only, whoever injected the event.
		out.Nonce = output.Nonce(e.Extra)
	}
	return out
}

// errUnsupported is returned by the Windows-only parts on other systems.
var errUnsupported = errors.New("only supported on Windows")
