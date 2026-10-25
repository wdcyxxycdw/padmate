package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// "padmatectl send" (OUT-01) injects key chords directly with SendInput.
// It is a development and debugging command: it bypasses the daemon, and
// with it the Output Arbiter, which is the only writer of keyboard output
// inside the daemon (CLAUDE.md invariant 3). Each chord still goes through
// output.Tap, so no key is left held, and every event carries the padmate
// tag with this process's id as the nonce.

const sendUsage = `usage: padmatectl send [--delay D] [--hold D] [--dry-run] [--json] <spec>...

Taps each key spec in order, such as "Ctrl+Shift+Z" or "F13": the
modifiers go down, then the key, then all of them come up in reverse
order. The keys go to the foreground window as US-layout scan codes,
through SendInput; every event carries padmate's dwExtraInfo tag with
this process's id as the nonce.

Every spec is parsed first: if any is invalid, nothing is sent and the
exit code is 1. A mouse key such as WheelUp or MouseLeft is invalid here:
send is a keyboard tool. This is a development tool; it does not go through the
padmate daemon.

flags:
  --delay D  wait D before the first chord, such as 3s, to switch to the
             target window (Ctrl+C aborts without sending)
  --hold D   hold each chord for D between the presses and the releases
             (default 0: presses and releases go out in one SendInput
             call). Ctrl+C, or closing the console window, during a
             hold still releases the keys.
  --dry-run  print the events that would be sent, one per line, and send
             nothing
  --json     with --dry-run, print one JSON object per event
`

// newSender returns the Sender that injects the keys: SendInput on
// Windows. Tests replace it.
var newSender = platformSender

// errUnsupported is returned by platformSender on systems without
// SendInput.
var errUnsupported = errors.New("sending keys is only supported on Windows; use --dry-run to print the events")

// sendChord is one parsed spec and the events that tap it.
type sendChord struct {
	chord  keys.Chord
	stroke output.Stroke
}

// stopSignals cancel "padmatectl send", "monitor" and "record": Ctrl+C,
// and SIGTERM, which is also what Go delivers on Windows when the console
// window is closed. The Go runtime only waits for the program when
// SIGTERM is being notified, so without it a close during a hold would
// end the process with the keys still down, and monitor and record would
// not close SDL. Logoff and shutdown give no signal: Windows sends those
// console events only to programs that have not loaded user32 or gdi32,
// and send loads user32 for SendInput, monitor and record through SDL.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// runSend implements "padmatectl send". The stopSignals cancel it.
func runSend(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	return sendMain(ctx, args, stdout, stderr)
}

func sendMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	delay := fs.Duration("delay", 0, "wait before the first chord")
	hold := fs.Duration("hold", 0, "time between the presses and the releases")
	dryRun := fs.Bool("dry-run", false, "print the events instead of sending them")
	asJSON := fs.Bool("json", false, "with --dry-run, print JSON objects")
	if code, ok := parseFlags(fs, args, sendUsage, stdout, stderr); !ok {
		return code
	}
	var bad string
	switch {
	case *delay < 0:
		bad = "--delay must not be negative"
	case *hold < 0:
		bad = "--hold must not be negative"
	case *asJSON && !*dryRun:
		bad = "--json needs --dry-run"
	case fs.NArg() == 0:
		bad = "no key specs given"
	}
	if bad != "" {
		fmt.Fprintln(stderr, "padmatectl send:", bad)
		fmt.Fprint(stderr, sendUsage)
		return 2
	}

	chords, ok := parseChords(fs.Args(), stderr)
	if !ok {
		return 1
	}

	// senderFor returns the Sender that taps chord c.
	var senderFor func(c keys.Chord) output.Sender
	if *dryRun {
		senderFor = func(c keys.Chord) output.Sender {
			return &printSender{w: stdout, json: *asJSON, chord: c}
		}
	} else {
		s, err := newSender()
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl send:", err)
			return 1
		}
		senderFor = func(keys.Chord) output.Sender { return s }
	}

	if *delay > 0 {
		what := "sending"
		if *dryRun {
			what = "dry run"
		}
		fmt.Fprintf(stderr, "padmatectl send: %s in %v; switch to the target window now (Ctrl+C aborts)\n", what, *delay)
		t := time.NewTimer(*delay)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			fmt.Fprintln(stderr, "padmatectl send: interrupted; nothing was sent")
			return 1
		}
	}

	for i, c := range chords {
		// Tap sends nothing once ctx is done, and when ctx ends a hold it
		// still sends the releases before returning ctx.Err(), joined with
		// any release error.
		err := output.Tap(ctx, senderFor(c.chord), c.stroke, *hold)
		switch {
		case err == nil:
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			fmt.Fprintf(stderr, "padmatectl send: interrupted at chord %d of %d (%s): %v\n", i+1, len(chords), c.chord, err)
			return 1
		default:
			fmt.Fprintf(stderr, "padmatectl send: %s: %v\n", c.chord, err)
			return 1
		}
	}
	return 0
}

// parseChords parses every spec and builds its stroke. It reports every
// invalid spec, in the format of "keys parse", and returns false if there
// was any.
func parseChords(specs []string, stderr io.Writer) ([]sendChord, bool) {
	chords := make([]sendChord, 0, len(specs))
	ok := true
	for _, spec := range specs {
		c, err := keys.Parse(spec)
		if err != nil {
			printKeySpecError(stderr, "padmatectl send: ", spec, err)
			ok = false
			continue
		}
		// send is a keyboard tool: a wheel notch or a button goes out only through
		// the daemon (ADR-0062).
		if _, isMouse := c.Key().Mouse(); isMouse {
			fmt.Fprintf(stderr, "padmatectl send: %s: %s is a mouse key; send only taps keyboard keys\n", spec, c.Key())
			ok = false
			continue
		}
		st, err := output.ChordStroke(c)
		if err != nil {
			fmt.Fprintf(stderr, "padmatectl send: %s: %v\n", spec, err)
			ok = false
			continue
		}
		chords = append(chords, sendChord{chord: c, stroke: st})
	}
	return chords, ok
}

// printSender is the Sender of --dry-run: it prints the events instead of
// injecting them.
type printSender struct {
	w     io.Writer
	json  bool
	chord keys.Chord // the chord it taps, for JSON
}

// sendEventJSON is one event in "send --dry-run --json".
type sendEventJSON struct {
	Chord string `json:"chord"`
	Up    bool   `json:"up"`
	Key   string `json:"key"`
	Scan  uint8  `json:"scan"`
	Ext   bool   `json:"ext"`
	ByVK  bool   `json:"byVK"`
	VK    uint16 `json:"vk"`
}

func (p *printSender) Send(events []output.KeyEvent) (int, error) {
	for i, e := range events {
		var err error
		if p.json {
			var b []byte
			b, err = json.Marshal(sendEventJSON{
				Chord: p.chord.String(), Up: e.Up, Key: e.Key.String(),
				Scan: e.Scan, Ext: e.Ext, ByVK: e.ByVK, VK: e.VK,
			})
			if err == nil {
				_, err = fmt.Fprintf(p.w, "%s\n", b)
			}
		} else {
			_, err = fmt.Fprintln(p.w, e)
		}
		if err != nil {
			return i, err
		}
	}
	return len(events), nil
}
