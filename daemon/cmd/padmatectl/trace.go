package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

const traceUsage = `usage: padmatectl trace --profile FILE --input FILE [--json] [--function-key KEY]

Replays one recorded gamepad trace without injecting keys and prints the
timeline from raw input through gesture decisions to keyboard output.
--json prints one JSON object per timeline item. --function-key (start,
back or guide; default start) is the function key the profile is compiled
with, as config.json's functionKey is for padmated: trace does not read it.`

type traceItem struct {
	T        json.Number     `json:"t"`
	Kind     string          `json:"kind"`
	Input    string          `json:"input,omitempty"`
	Decision json.RawMessage `json:"decision,omitempty"`
	Output   string          `json:"output,omitempty"`
}

type traceWriter struct {
	w    io.Writer
	json bool
}

func (tw *traceWriter) write(t clock.Time, kind, value string, d engine.Decision) {
	if tw.json {
		item := traceItem{T: json.Number(string(t.AppendMs(nil))), Kind: kind}
		switch kind {
		case "input":
			item.Input = value
		case "decision":
			item.Decision, _ = d.MarshalJSON()
		case "output":
			item.Output = value
		}
		b, _ := json.Marshal(item)
		fmt.Fprintln(tw.w, string(b))
		return
	}
	if kind == "decision" {
		value = d.String()
	}
	fmt.Fprintf(tw.w, "@%s %-8s %s\n", t.AppendMs(nil), kind, value)
}

type traceSender struct {
	clk *clock.Fake
	w   *traceWriter
}

func (s *traceSender) Send(events []output.KeyEvent) (int, error) {
	if text := output.TypedText(events); text != "" {
		// A typed character (OUT-21): its press and release, and the two units of a
		// surrogate pair, are one line, such as `text "你"`.
		s.w.write(s.clk.Now(), "output", "text "+strconv.Quote(text), engine.Decision{})
	}
	for _, ev := range events {
		if ev.Silent() || ev.Unicode {
			continue // a wheel release sends nothing; a typed unit is shown above
		}
		if ev.Pointer() {
			// Every move is shown, for debugging, such as "move +8,+0".
			s.w.write(s.clk.Now(), "output", ev.String(), engine.Decision{})
			continue
		}
		name := ev.Key.String()
		if !ev.Key.IsValid() {
			name = fmt.Sprintf("sc:0x%02X", ev.Scan)
		}
		edge := "+"
		if ev.Up {
			edge = "-"
		}
		s.w.write(s.clk.Now(), "output", edge+name, engine.Decision{})
	}
	return len(events), nil
}

func runTrace(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	profilePath := fs.String("profile", "", "profile to load")
	inputPath := fs.String("input", "", "recorded input trace")
	asJSON := fs.Bool("json", false, "print JSON timeline items")
	functionKey := fs.String("function-key", "start", "the function key the profile is compiled with: start, back or guide")
	if code, ok := parseFlags(fs, args, traceUsage+"\n", stdout, stderr); !ok {
		return code
	}
	fn, err := parseFunctionKey(*functionKey)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", err)
		return 2
	}
	if *profilePath == "" || *inputPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "padmatectl trace: --profile and --input are required")
		fmt.Fprintln(stderr, traceUsage)
		return 2
	}
	data, err := os.ReadFile(*profilePath)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", profile.ReadError(*profilePath, err))
		return 2
	}
	// A profile of a library may extend another (PRO-19): its parents are
	// read from the same library, and the trace runs the merge.
	var p *profile.Profile
	var ds profile.Diagnostics
	if lib, id, ok := libraryOf(*profilePath); ok {
		p, ds = lib.Parse(id, data)
	} else {
		p, ds = profile.Parse(data)
	}
	for _, d := range ds {
		fmt.Fprintf(stderr, "padmatectl trace: %s\n", diagnosticLine(d))
	}
	if ds.HasErrors() {
		return 1
	}
	prog, err := engine.Compile(p, engine.WithFunctionKey(fn))
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace: cannot compile profile:", err)
		return 1
	}
	f, err := os.Open(*inputPath)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", *inputPath+": "+err.Error())
		return 2
	}
	events, err := input.ReadEvents(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", *inputPath+": "+err.Error())
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(stderr, "padmatectl trace:", *inputPath+": no events")
		return 1
	}
	dev := events[0].Device
	for _, ev := range events {
		if ev.Kind == input.ButtonDown || ev.Kind == input.ButtonUp || ev.Kind == input.AxisMotion {
			dev = ev.Device
			break
		}
	}
	filtered := events[:0]
	var base uint64
	found := false
	for _, ev := range events {
		if ev.Device != dev {
			continue
		}
		if !found {
			base, found = ev.Time, true
		}
		ev.Time -= base
		filtered = append(filtered, ev)
	}
	if len(filtered) == 0 {
		fmt.Fprintln(stderr, "padmatectl trace:", *inputPath+": no events for selected device")
		return 1
	}

	clk := clock.NewFake()
	w := &traceWriter{w: stdout, json: *asJSON}
	sender := &traceSender{clk: clk, w: w}
	timing := prog.Timing()
	// No event gap (OUT-17): trace reads no config.json, so each command's
	// keys are one send line, as with output.eventGapMs 0.
	arb := output.NewArbiter(clk, sender, output.ArbiterOptions{MinPress: time.Duration(timing.TapPressMs) * time.Millisecond, MinGap: time.Duration(timing.TapGapMs) * time.Millisecond})
	s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: arb, Clock: clk,
		Trace: func(d engine.Decision) { w.write(d.T, "decision", "", d) },
		OnEvent: func(ev input.Event) {
			if ev.Kind != input.ButtonDown && ev.Kind != input.ButtonUp && ev.Kind != input.AxisMotion {
				return
			}
			v := ev.Control
			switch ev.Kind {
			case input.ButtonDown:
				v = "+" + v
			case input.ButtonUp:
				v = "-" + v
			case input.AxisMotion:
				v += "=" + strconv.Itoa(int(ev.Value))
			}
			w.write(clk.Now(), "input", v, engine.Decision{})
		},
	})
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", err)
		return 1
	}
	if err := s.Replay(clk, filtered); err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", err)
		return 1
	}
	if err := s.ReleaseAll(); err != nil {
		fmt.Fprintln(stderr, "padmatectl trace:", err)
		return 1
	}
	if errs := s.Errors(); len(errs) != 0 {
		parts := make([]string, len(errs))
		for i, e := range errs {
			parts[i] = e.Error()
		}
		fmt.Fprintln(stderr, "padmatectl trace:", strings.Join(parts, "; "))
		return 1
	}
	return 0
}

var _ output.Sender = (*traceSender)(nil)
