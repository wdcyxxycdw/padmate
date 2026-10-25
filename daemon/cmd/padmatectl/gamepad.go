package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// The SDL plumbing that "padmatectl monitor" and "padmatectl record"
// share besides their SDL flags (input.SDLFlags): opening and closing
// SDL, the --virtual gamepad and the event loop.

const (
	// monitorVirtualWait bounds --virtual without --duration.
	monitorVirtualWait = 5 * time.Second
	// monitorStep separates the steps of --virtual, so that each change
	// is its own event.
	monitorStep = 5 * time.Millisecond
	// monitorVirtualName is the name of the --virtual gamepad.
	monitorVirtualName = "padmate virtual gamepad"
)

// openSource opens SDL for the command cmd and writes its header to
// stderr. It prints the error and returns nil when SDL does not open.
func openSource(cmd string, hints []input.Hint, preset string, stderr io.Writer) *input.Source {
	s, err := input.Open(input.Options{Hints: hints})
	if err != nil {
		fmt.Fprintf(stderr, "padmatectl %s: %v\n", cmd, err)
		return nil
	}
	// Like the other diagnostics on stderr, a failed write is ignored.
	_ = s.WriteHeader(stderr, "padmatectl "+cmd+": ", preset)
	return s
}

// closeSource closes s and reports the events it dropped, whose number it
// returns.
func closeSource(cmd string, s *input.Source, stderr io.Writer) uint64 {
	s.Close()
	n := s.Dropped()
	if n > 0 {
		fmt.Fprintf(stderr, "padmatectl %s: %d events dropped: the output was too slow\n", cmd, n)
	}
	return n
}

// virtualStep is one step of --virtual: a button press or release, or an
// axis move.
type virtualStep struct {
	control string
	axis    bool
	down    bool  // for a button
	value   int16 // for an axis
}

// virtualScript is what --virtual does with its gamepad.
var virtualScript = []virtualStep{
	{control: "south", down: true},
	{control: "south"},
	{control: "leftx", axis: true, value: 32767},
	{control: "leftx", axis: true},
	{control: "lefttrigger", axis: true, value: 32767},
	{control: "lefttrigger", axis: true},
	{control: "dpad.up", down: true},
	{control: "dpad.up"},
}

func (st virtualStep) apply(v *input.Virtual) error {
	if st.axis {
		return v.SetAxis(st.control, st.value)
	}
	return v.SetButton(st.control, st.down)
}

// virtualEvents returns the events --virtual waits for, as eventKey
// formats them.
func virtualEvents() []string {
	want := []string{"added"}
	for _, st := range virtualScript {
		switch {
		case st.axis:
			want = append(want, fmt.Sprintf("%s %d", st.control, st.value))
		case st.down:
			want = append(want, st.control+" down")
		default:
			want = append(want, st.control+" up")
		}
	}
	return append(want, "removed")
}

// eventKey formats e without its time and device.
func eventKey(e input.Event) string {
	switch e.Kind {
	case input.ButtonDown, input.ButtonUp:
		return e.Control + " " + e.Kind.String()
	case input.AxisMotion:
		return fmt.Sprintf("%s %d", e.Control, e.Value)
	default:
		return e.Kind.String()
	}
}

// virtualRun is a running --virtual gamepad: its id, the events still
// expected from it, and the goroutine that drives it.
type virtualRun struct {
	id   input.DeviceID
	want []string
	errc <-chan error // the result of driveVirtual
	stop func()       // stops driveVirtual and waits for it
}

// startVirtual attaches the --virtual gamepad to s and starts driving it
// with virtualScript. Call stop when done.
func startVirtual(ctx context.Context, s *input.Source) (*virtualRun, error) {
	v, err := s.AttachVirtual(monitorVirtualName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	errc := make(chan error, 1)
	wg.Go(func() { errc <- driveVirtual(ctx, v) })
	return &virtualRun{id: v.ID(), want: virtualEvents(), errc: errc, stop: func() { cancel(); wg.Wait() }}, nil
}

// incomplete says that the run ended, for the reason why, before all the
// events of the gamepad.
func (g *virtualRun) incomplete(why string) string {
	all := len(virtualEvents())
	return fmt.Sprintf("%s after %d of the %d events of the virtual gamepad; next: %s", why, all-len(g.want), all, g.want[0])
}

// driveVirtual runs virtualScript on v, one step every monitorStep, and
// then detaches v.
func driveVirtual(ctx context.Context, v *input.Virtual) error {
	for _, st := range virtualScript {
		if err := st.apply(v); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(monitorStep):
		}
	}
	return v.Detach()
}

// pumpEnd is why pump returned without an error.
type pumpEnd uint8

const (
	endStopped     pumpEnd = iota // handle returned false
	endComplete                   // every event of the virtual gamepad was seen
	endInterrupted                // the context was done
	endTimedOut                   // the wait passed
)

func (e pumpEnd) String() string {
	return [...]string{"stopped", "complete", "interrupted", "timed out"}[e]
}

// pump passes events to handle until handle returns false, ctx is done,
// wait passes (0: no limit) or, with a virtual gamepad g, all of g's
// events have been seen: it checks them against the script after handle.
// The error is handle's, the script's, or the end of events.
func pump(ctx context.Context, events <-chan input.Event, g *virtualRun, wait time.Duration,
	handle func(input.Event) (more bool, err error),
) (pumpEnd, error) {
	var timeout <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	var scriptc <-chan error
	if g != nil {
		scriptc = g.errc
	}
	for {
		select {
		case <-ctx.Done():
			return endInterrupted, nil
		case <-timeout:
			return endTimedOut, nil
		case err := <-scriptc:
			if err != nil {
				return 0, fmt.Errorf("virtual gamepad: %w", err)
			}
			scriptc = nil
		case e, ok := <-events:
			if !ok {
				return 0, errors.New("the SDL event loop stopped")
			}
			more, err := handle(e)
			if err != nil {
				return 0, err
			}
			if g != nil && e.Device == g.id {
				if got := eventKey(e); got != g.want[0] {
					return 0, fmt.Errorf("virtual gamepad: got %q, want %q", got, g.want[0])
				}
				if g.want = g.want[1:]; len(g.want) == 0 {
					return endComplete, nil
				}
			}
			if !more {
				return endStopped, nil
			}
		}
	}
}
