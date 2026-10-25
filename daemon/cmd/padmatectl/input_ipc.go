package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

type inputConnectionOptions struct {
	direct  bool
	ipcDir  string
	timeout time.Duration
}

func (o *inputConnectionOptions) flags(fs *flag.FlagSet) {
	fs.BoolVar(&o.direct, "direct", false, "read SDL directly instead of daemon IPC")
	fs.StringVar(&o.ipcDir, "ipc-dir", ipcDiscoveryDir, "daemon discovery directory")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "IPC setup timeout")
}

func (o *inputConnectionOptions) validate(fs *flag.FlagSet, virtual bool) string {
	explicitDirect, sdlFlags, ipcDir := false, false, false
	fs.Visit(func(f *flag.Flag) {
		explicitDirect = explicitDirect || f.Name == "direct"
		sdlFlags = sdlFlags || f.Name == "preset" || f.Name == "hint"
		ipcDir = ipcDir || f.Name == "ipc-dir"
	})
	if virtual && explicitDirect && !o.direct {
		return "--virtual and --direct=false cannot be combined"
	}
	o.direct = o.direct || virtual
	if !o.direct && sdlFlags {
		return "--preset and --hint require --direct (or --virtual)"
	}
	if o.direct && ipcDir {
		return "--ipc-dir cannot be combined with --direct or --virtual"
	}
	if o.timeout <= 0 || o.timeout > time.Minute {
		return "--timeout must be greater than zero and at most 1m"
	}
	return ""
}

func decodeIPCInput(row ipc.Notification) (input.Event, error) {
	if row.Topic == "dropped" {
		return input.Event{}, fmt.Errorf("daemon dropped input events (%s); stream is incomplete", row.Data)
	}
	if row.Topic != "input" {
		return input.Event{}, fmt.Errorf("unexpected input topic %q", row.Topic)
	}
	if bytes.Contains(row.Data, []byte(`"remapped"`)) {
		if e, ok, err := decodeRemapped(row.Data); ok || err != nil {
			return e, err
		}
	}
	events, err := input.ReadEvents(bytes.NewReader(row.Data))
	if err != nil {
		return input.Event{}, err
	}
	if len(events) != 1 {
		return input.Event{}, errors.New("expected one input event")
	}
	return events[0], nil
}

// decodeRemapped decodes the "remapped" row of the input topic (IN-18): a
// gamepad whose mapping changed, with its info as it is now. It is no trace
// record, so input.ReadEvents does not know it. ok is false for any other kind.
func decodeRemapped(data []byte) (e input.Event, ok bool, err error) {
	var kind struct {
		Kind string `json:"kind"`
	}
	// Anything that is not one JSON object of this kind is left to ReadEvents,
	// which says what is wrong with it.
	if isRemapped := json.Unmarshal(data, &kind) == nil && kind.Kind == "remapped"; !isRemapped {
		return e, false, nil
	}
	var row struct {
		TimeNs uint64            `json:"timeNs"`
		Kind   string            `json:"kind"`
		Device input.DeviceID    `json:"device"`
		Info   *input.DeviceInfo `json:"info"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&row); err != nil {
		return e, true, fmt.Errorf("remapped row: %w", err)
	}
	switch {
	case row.Device == 0:
		return e, true, errors.New("remapped row: missing device")
	case row.Info != nil && row.Info.ID != row.Device:
		return e, true, fmt.Errorf("remapped row: info.id is %d, not the device %d", row.Info.ID, row.Device)
	}
	return input.Event{Time: row.TimeNs, Device: row.Device, Kind: input.DeviceRemapped, Info: row.Info}, true, nil
}

// addedSet remembers the devices whose DeviceAdded a consumer of the input
// topic has handled and whether they were gamepads then. The topic is
// subscribed before devices.list is read, so the list and the rows queued
// meanwhile overlap and a repeated DeviceAdded is dropped; but a joystick that
// got a mapping is reported again as a gamepad with the same id (IN-18), and
// one that lost it is removed and reported again, and those are not repeats.
type addedSet map[input.DeviceID]bool

// fresh reports whether e is to be handled: false for a DeviceAdded that
// repeats the device as it was last handled.
func (a addedSet) fresh(e input.Event) bool {
	switch e.Kind {
	case input.DeviceAdded:
		gamepad := e.Info != nil && e.Info.IsGamepad
		if was, ok := a[e.Device]; ok && was == gamepad {
			return false
		}
		a[e.Device] = gamepad
	case input.DeviceRemoved:
		delete(a, e.Device)
	}
	return true
}

// Observe before querying the current device list, so input occurring during
// setup is buffered. Already-connected devices receive a synthetic added row;
// a queued added row for the same ID is then ignored. No held state is invented.
func streamIPCInput(ctx context.Context, opts inputConnectionOptions, listOnly bool, handle func(input.Event, bool) (bool, error)) error {
	return streamIPCDevices(ctx, opts, listOnly, nil, handle)
}

// streamIPCDevices is streamIPCInput, which also gives onList, when set, the
// rows of devices.list before it handles them: --list shows their battery.
func streamIPCDevices(ctx context.Context, opts inputConnectionOptions, listOnly bool, onList func([]ipcDevice), handle func(input.Event, bool) (bool, error)) error {
	sub, err := ipc.Subscribe(ctx, opts.ipcDir, opts.timeout, []string{"input"}, false)
	if err != nil {
		return err
	}
	defer sub.Close()
	setup, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	var devices []ipcDevice
	if err := sub.CallResult(setup, "devices.list", nil, &devices); err != nil {
		return err
	}
	if onList != nil {
		onList(devices)
	}
	seen := addedSet{}
	for _, d := range devices {
		e := input.Event{Kind: input.DeviceAdded, Device: d.Info.ID, Info: &d.Info}
		seen.fresh(e)
		more, err := handle(e, true)
		if err != nil || !more {
			return err
		}
	}
	if listOnly {
		return nil
	}
	for {
		row, err := sub.Next(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		e, err := decodeIPCInput(row)
		if err != nil {
			return err
		}
		if !seen.fresh(e) {
			continue
		}
		more, err := handle(e, false)
		if err != nil || !more {
			return err
		}
	}
}

func monitorIPC(ctx context.Context, opts monitorOptions, stdout, stderr io.Writer) int {
	if opts.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.duration)
		defer cancel()
	}
	p := newEventPrinter(stdout, opts)
	n := 0
	var onList func([]ipcDevice)
	var listed []ipcDevice
	if opts.list && !opts.json {
		onList = func(devices []ipcDevice) { listed = devices }
	}
	err := streamIPCDevices(ctx, opts.inputConnectionOptions, opts.list, onList, func(e input.Event, _ bool) (bool, error) {
		shown, err := p.print(e)
		if shown {
			n++
		}
		return opts.count == 0 || n < opts.count, err
	})
	if err == nil && onList != nil {
		err = printPadDetails(stdout, padDetailsFromIPC(listed))
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl monitor:", err)
		return 1
	}
	return 0
}

func recordIPC(ctx context.Context, opts recordOptions, w, stderr io.Writer) int {
	if opts.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.duration)
		defer cancel()
	}
	r := &recorder{w: w, stderr: stderr, want: opts.device, count: opts.count, keys: input.NewRegistry(), header: "# padmatectl record " + version.String() + "; daemon IPC; timestamps start at first observed transition\n"}
	var synthetic bool
	var first uint64
	var started bool
	err := streamIPCInput(ctx, opts.inputConnectionOptions, false, func(e input.Event, initial bool) (bool, error) {
		// Initial metadata has time zero. Start that device's observed
		// transition timeline at zero too, excluding daemon uptime.
		if !initial && synthetic && e.Device == r.dev {
			if !started {
				first, started = e.Time, true
			}
			if e.Time < first {
				e.Time = 0
			} else {
				e.Time -= first
			}
		}
		more, err := r.handle(e)
		if initial && r.dev == e.Device {
			synthetic = true
		}
		return more, err
	})
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl record:", err)
		return 1
	}
	if r.n == 0 {
		fmt.Fprintln(stderr, "padmatectl record: stopped before a matching gamepad appeared")
		return 1
	}
	fmt.Fprintf(stderr, "padmatectl record: %d events recorded through daemon IPC\n", r.n)
	return 0
}
