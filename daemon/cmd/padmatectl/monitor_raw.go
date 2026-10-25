package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// "padmatectl monitor --raw" prints the elements of the joysticks as the
// device has them (IN-17, ADR-0072): buttons, axes and hats by index, the
// events the mapping wizard works from. They are no gamepad events: a
// joystick that SDL has no gamepad mapping for shows its elements here, and
// a gamepad shows them next to its mapped events.

// rawEvent is a raw joystick event as the monitor prints it: an input.raw
// event of the daemon, or a RawEvent of a direct Source.
type rawEvent struct {
	TimeNs uint64         `json:"timeNs"`
	Device input.DeviceID `json:"device"`
	Kind   string         `json:"kind"`
	Index  uint8          `json:"index"`
	Value  int16          `json:"value"`
}

func rawFromInput(e input.RawEvent) rawEvent {
	return rawEvent{TimeNs: e.Time, Device: e.Device, Kind: e.Kind.String(), Index: e.Index, Value: e.Value}
}

// lockedWriter lets the raw events and the other events of the monitor share
// stdout: each line is written whole.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// formatRawEvent formats e as a text line: the time, the device and "raw",
// then "button N down|up", "axis N VALUE" or "hat N MASK" (up 1, right 2, down
// 4, left 8, 0 centered).
func formatRawEvent(e rawEvent) string {
	t := fmt.Sprintf("%d.%06d", e.TimeNs/1e9, e.TimeNs%1e9/1e3)
	val := strconv.Itoa(int(e.Value))
	if e.Kind == "button" {
		val = "up"
		if e.Value != 0 {
			val = "down"
		}
	}
	return fmt.Sprintf("%s %d raw %s %d %s", t, e.Device, e.Kind, e.Index, val)
}

// printRaw prints the raw event e, as a JSON object with kind "raw" under
// --json (rawKind is button, axis or hat). The deadband hides axis moves
// smaller than it, as for the events of a gamepad. shown says whether it was
// printed.
func (p *eventPrinter) printRaw(e rawEvent) (shown bool, err error) {
	if e.Kind == "axis" && p.deadband > 0 {
		if !p.moved(axisKey{e.Device, "raw " + strconv.Itoa(int(e.Index))}, e.Value) {
			return false, nil
		}
	}
	if p.json {
		line, merr := json.Marshal(struct {
			TimeNs  uint64         `json:"timeNs"`
			Kind    string         `json:"kind"`
			Device  input.DeviceID `json:"device"`
			RawKind string         `json:"rawKind"`
			Index   uint8          `json:"index"`
			Value   int16          `json:"value"`
		}{e.TimeNs, "raw", e.Device, e.Kind, e.Index, e.Value})
		if merr != nil {
			return false, merr
		}
		_, err = fmt.Fprintf(p.w, "%s\n", line)
	} else {
		_, err = fmt.Fprintln(p.w, formatRawEvent(e))
	}
	if err != nil {
		return false, fmt.Errorf("write: %w", err)
	}
	return true, nil
}

// rawWanted reports whether the raw events of device id are wanted: all of
// them, or those of the device --device named.
func (o monitorOptions) rawWanted(id input.DeviceID) bool {
	return o.rawDevice == 0 || o.rawDevice == id
}

// rawSkipped decides what a failure to start the raw events of device id
// means. A failure of that device (deviceFailure says so: it was unplugged
// between the list and the ask, or SDL cannot open the joystick) is told on
// stderr and left out when all devices are watched, so the others go on; it
// is still an error when --device named the device, since there is nothing
// else to watch. A failure that is not the device's is always an error.
func (o monitorOptions) rawSkipped(stderr io.Writer, id input.DeviceID, err error, deviceFailure func(error) bool) error {
	if err == nil || o.rawDevice != 0 || !deviceFailure(err) {
		return err
	}
	fmt.Fprintf(stderr, "padmatectl monitor: raw events of device %d left out: %v\n", id, err)
	return nil
}

// rawLoop prints the raw events of s until ctx is done or they end.
func rawLoop(ctx context.Context, s *input.Source, p *eventPrinter, opts monitorOptions, stderr io.Writer, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-s.RawEvents():
			if !ok {
				return
			}
			if !opts.rawWanted(e.Device) {
				continue
			}
			if _, err := p.printRaw(rawFromInput(e)); err != nil {
				fmt.Fprintf(stderr, "padmatectl monitor: %v\n", err)
				return
			}
		}
	}
}

// monitorRawIPC is "monitor --raw" through the daemon: it pauses the mapping
// (capture.start, ended when the connection closes) so that the buttons it
// watches send no keys, asks for the raw events of each device it lists or
// is told of (devices.rawInfo) and prints them with the other input events.
func monitorRawIPC(ctx context.Context, opts monitorOptions, stdout, stderr io.Writer) int {
	if opts.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.duration)
		defer cancel()
	}
	p := newEventPrinter(stdout, opts)
	sub, err := ipc.Subscribe(ctx, opts.ipcDir, opts.timeout, []string{"input", "input.raw"}, true)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl monitor:", err)
		return 1
	}
	defer sub.Close()
	ask := func(id input.DeviceID) error {
		if !opts.rawWanted(id) {
			return nil
		}
		setup, cancel := context.WithTimeout(ctx, opts.timeout)
		defer cancel()
		return sub.CallResult(setup, "devices.rawInfo", map[string]string{"device": strconv.FormatUint(uint64(id), 10)}, new(any))
	}
	n := 0
	handle := func(e input.Event) (bool, error) {
		shown, err := p.print(e)
		if shown {
			n++
		}
		if err == nil && e.Kind == input.DeviceAdded {
			// The daemon's answer to the ask is the device's: the connection is
			// not, and is an error.
			err = opts.rawSkipped(stderr, e.Device, ask(e.Device), func(err error) bool {
				var refused *jrpc2.Error
				return errors.As(err, &refused)
			})
		}
		return opts.count == 0 || n < opts.count, err
	}
	setup, cancel := context.WithTimeout(ctx, opts.timeout)
	var devices []ipcDevice
	err = sub.CallResult(setup, "devices.list", nil, &devices)
	cancel()
	seen := addedSet{}
	for _, d := range devices {
		if err != nil {
			break
		}
		e := input.Event{Kind: input.DeviceAdded, Device: d.Info.ID, Info: &d.Info}
		seen.fresh(e)
		var more bool
		if more, err = handle(e); !more && err == nil {
			return 0
		}
	}
	for err == nil {
		var row ipc.Notification
		row, err = sub.Next(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			break
		}
		switch row.Topic {
		case "input.raw":
			var e rawEvent
			if err = json.Unmarshal(row.Data, &e); err != nil {
				break
			}
			if !opts.rawWanted(e.Device) {
				continue
			}
			var shown bool
			if shown, err = p.printRaw(e); shown {
				n++
			}
			if err == nil && opts.count > 0 && n >= opts.count {
				return 0
			}
		default:
			var e input.Event
			if e, err = decodeIPCInput(row); err != nil {
				break
			}
			if !seen.fresh(e) {
				continue
			}
			var more bool
			if more, err = handle(e); !more && err == nil {
				return 0
			}
		}
	}
	fmt.Fprintln(stderr, "padmatectl monitor:", err)
	return 1
}
