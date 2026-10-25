package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os/signal"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// "padmatectl monitor" observes daemon input through IPC. --direct opens
// internal/input in this process for standalone hardware diagnostics.

const monitorUsageHead = `usage: padmatectl monitor [--direct] [--virtual] [--json] [--list]
                          [--raw [--device ID]]
                          [--duration D] [--count N] [--preset NAME]
                          [--hint NAME=VALUE]... [--deadband N]
                          [--ipc-dir DIR] [--timeout D]

Prints daemon input, one event per line, until Ctrl+C, --duration or
--count. The default requires a running daemon and never falls back
to direct input. Connected devices are listed first with time 0;
subsequent events keep daemon timestamps. Earlier held state is not
reconstructed. Disconnection or dropped events fail the command.

With --direct, SDL is loaded from $PADMATE_SDL3, or SDL3.dll next
to padmatectl ("task sdl" fetches it into bin/); outside Windows, from
the library next to padmatectl or else the system's libSDL3. The SDL
version, the library and the hints in effect go to stderr first.

Each line starts with the event time, in seconds since SDL was
initialized, followed by one of:

  +device ID TYPE "NAME" VVVV:PPPP driver=D guid=G serial=S path=P
                          a device, with " virtual" appended for an SDL
                          virtual joystick and " joystick-only" for a
                          joystick without a gamepad mapping, whose
                          buttons and axes are not read; then, for a
                          gamepad, " key=K", and " reconnected" when it
                          was connected earlier in this run
  -device ID [key=K]      the device is gone
  ~device ID remapped buttons=M axes=M
                          SDL changed the gamepad mapping of a gamepad that
                          stays one (M are its button and axis masks now);
                          what it held counts as released. Not printed
                          with --json or --list.
  ID CONTROL down         a button, such as "1 south down"
  ID CONTROL up
  ID CONTROL VALUE        an axis, such as "1 leftx -12345": -32768..32767
                          for a stick, 0..32767 for a trigger

The ID of a device changes each time it connects; the key K of a
gamepad stays the same when it reconnects. K is its GUID, then "#1",
"#2", ... for a second, third, ... identical gamepad connected at the
same time, then "/s:SERIAL", or else "/p:PATH" (so a gamepad without a
serial is another one on another USB port). The path of an XInput
gamepad, "XInput#0", is its XInput slot and is left out. K is the same
in another run, but for the #N of identical gamepads, which follows the
order they connect in.

flags:
  --direct        read SDL in this process without a daemon
  --ipc-dir DIR   use this daemon discovery directory
  --timeout D     IPC setup timeout (default 10s, maximum 1m)
  --virtual       attach an SDL virtual gamepad and drive it: south
                  down/up, leftx 32767/0, lefttrigger 32767/0, dpad.up
                  down/up, then detach it. Exits 0 once all of its
                  events have been seen, 1 if they are not seen within
                  --duration (default 5s). Implies --direct.
  --json          print one JSON object per event, with the fields:
                    timeNs   the event time in nanoseconds since SDL was
                             initialized
                    kind     "added", "removed", "down", "up" or "axis"
                    device   the device id
                    control  the button or axis, for down, up and axis
                    value    the axis value, for axis
                    info     the device, for added: id, name, type,
                             vendor, product, version, guid, driver,
                             path, serial, gamepad (false when
                             joystick-only), virtual
  --list          print the daemon's current devices and exit; with
                  --direct, wait 500ms or --duration for devices.
                  Without --json, a block follows for each gamepad:

                    pad 3 "XInput Controller #1"
                      capabilities  rumble
                      power         charging 100%
                      connection    wired
                      player index  0

                  capabilities are what SDL reports (rgbLed implies a
                  led; led alone is a single-color one); power is
                  unknown, wired, charging or discharging, with the
                  charge when the gamepad reports it; connection is
                  SDL's (wired, wireless or unknown; a USB receiver
                  reads as wired); player index is none when SDL has
                  assigned none. With --direct they are read from
                  SDL's getters at the end of the wait, which send the
                  gamepad nothing. Through the daemon, only the
                  capabilities and the battery that it last read are
                  known: connection and player index say so.
  --raw           also print the raw elements of the joysticks, by index
                  (IN-17), which the mapping wizard works from: a joystick
                  that SDL has no gamepad mapping for shows only these.
                  A line is the time, the device ID, "raw" and
                    button N down|up
                    axis N VALUE         -32768..32767
                    hat N MASK           up 1, right 2, down 4, left 8, 0 centered
                  With --json, an object with kind "raw", device, rawKind
                  (button, axis or hat), index and value. Through the
                  daemon the mapping is paused while the monitor runs
                  (capture), so a mapped gamepad's buttons send no keys.
                  Not with --list or --virtual.
  --device ID     with --raw, only the device ID (as the +device lines show it)
  --duration D    exit after D, such as 10s (default: until Ctrl+C)
  --count N       exit after printing N events
  --preset NAME   direct SDL hint preset (default "default"; see below)
  --hint N=V      direct SDL hint, after the preset; repeatable
  --deadband N    hide axis events that move less than N from the value
                  last printed for that axis (default 0: print all)

presets:
`

// monitorUsage lists the presets after monitorUsageHead.
var monitorUsage = monitorUsageHead + input.PresetUsage()

// monitorListWait is how long --list waits for devices. SDL reports most
// connected devices during SDL_Init, the others once its backends have
// detected them.
const monitorListWait = 500 * time.Millisecond

// monitorOptions are the parsed flags of "padmatectl monitor".
type monitorOptions struct {
	inputConnectionOptions
	virtual   bool
	json      bool
	list      bool
	raw       bool
	rawDevice input.DeviceID
	duration  time.Duration
	count     int
	preset    string
	hints     []input.Hint // the preset's, then the --hint flags
	deadband  int
}

// runMonitor implements "padmatectl monitor". The stopSignals end it,
// through Close. SDL leaves them to Go: Open turns off its own signal
// handlers (input.HintNoSignalHandlers).
func runMonitor(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	return monitorMain(ctx, args, stdout, stderr)
}

func monitorMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseMonitorFlags(args, stdout, stderr)
	if !ok {
		return code
	}
	if !opts.direct {
		if opts.raw {
			return monitorRawIPC(ctx, opts, stdout, stderr)
		}
		return monitorIPC(ctx, opts, stdout, stderr)
	}
	s := openSource("monitor", opts.hints, opts.preset, stderr)
	if s == nil {
		return 1
	}
	code = watch(ctx, s, opts, stdout, stderr)
	closeSource("monitor", s, stderr)
	return code
}

// parseMonitorFlags parses and checks the flags. When ok is false, the
// command exits with code.
func parseMonitorFlags(args []string, stdout, stderr io.Writer) (opts monitorOptions, code int, ok bool) {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	opts.flags(fs)
	fs.BoolVar(&opts.virtual, "virtual", false, "attach and drive a virtual gamepad")
	fs.BoolVar(&opts.json, "json", false, "print JSON objects")
	fs.BoolVar(&opts.list, "list", false, "print the devices and exit")
	fs.BoolVar(&opts.raw, "raw", false, "also print the raw joystick elements")
	device := fs.Uint64("device", 0, "with --raw, only this device")
	fs.DurationVar(&opts.duration, "duration", 0, "exit after this time")
	fs.IntVar(&opts.count, "count", 0, "exit after this many events")
	var sf input.SDLFlags
	sf.AddFlags(fs)
	fs.IntVar(&opts.deadband, "deadband", 0, "hide smaller axis changes")
	if code, ok := parseFlags(fs, args, monitorUsage, stdout, stderr); !ok {
		return opts, code, false
	}
	opts.preset = sf.Preset
	hints, presetErr := sf.Hints()
	var problem string
	switch {
	case fs.NArg() > 0:
		problem = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	case presetErr != nil:
		problem = presetErr.Error()
	case opts.list && opts.virtual:
		problem = "--list and --virtual cannot be combined"
	case opts.duration < 0, opts.count < 0, opts.deadband < 0:
		problem = "--duration, --count and --deadband cannot be negative"
	case *device > math.MaxUint32:
		problem = fmt.Sprintf("--device must be at most %d", uint64(math.MaxUint32))
	case *device != 0 && !opts.raw:
		problem = "--device needs --raw"
	case opts.raw && (opts.list || opts.virtual):
		problem = "--raw cannot be combined with --list or --virtual"
	default:
		problem = opts.validate(fs, opts.virtual)
	}
	if problem != "" {
		fmt.Fprintf(stderr, "padmatectl monitor: %s\n", problem)
		fmt.Fprint(stderr, monitorUsage)
		return opts, 2, false
	}
	opts.hints = hints
	opts.rawDevice = input.DeviceID(*device)
	return opts, 0, true
}

// watch prints the events of s until the end that opts set, and returns
// the exit code.
func watch(ctx context.Context, s *input.Source, opts monitorOptions, stdout, stderr io.Writer) int {
	p := newEventPrinter(stdout, opts)
	if opts.raw {
		// The raw events are printed from another goroutine.
		p.w = &lockedWriter{w: stdout}
		ctxRaw, stopRaw := context.WithCancel(ctx)
		done := make(chan struct{})
		go rawLoop(ctxRaw, s, p, opts, stderr, done)
		defer func() { stopRaw(); <-done }()
	}
	wait := opts.duration
	switch {
	case opts.list && wait == 0:
		wait = monitorListWait
	case opts.virtual && wait == 0:
		wait = monitorVirtualWait
	}
	var g *virtualRun
	if opts.virtual {
		var err error
		if g, err = startVirtual(ctx, s); err != nil {
			fmt.Fprintf(stderr, "padmatectl monitor: %v\n", err)
			return 1
		}
		defer g.stop()
	}
	// A text --list follows the gamepads to read their getters at the end
	// of the wait, when a driver that reports its battery late has done so.
	var listed *padList
	if opts.list && !opts.json {
		listed = &padList{}
	}
	printed := 0
	end, err := pump(ctx, s.Events(), g, wait, func(e input.Event) (bool, error) {
		shown, err := p.print(e)
		if err != nil {
			return false, err
		}
		if opts.raw && e.Kind == input.DeviceAdded && opts.rawWanted(e.Device) {
			// A device that went away right after it was added, or that SDL
			// cannot open, is left out; a closed Source is the end.
			_, err := s.RawStart(e.Device)
			if err = opts.rawSkipped(stderr, e.Device, err, func(err error) bool {
				return !errors.Is(err, input.ErrClosed)
			}); err != nil {
				return false, fmt.Errorf("raw events of device %d: %w", e.Device, err)
			}
		}
		if listed != nil {
			listed.observe(e)
		}
		if shown {
			printed++
		}
		return opts.count == 0 || printed < opts.count, nil
	})
	if err == nil && listed != nil {
		err = printPadDetails(stdout, padDetailsFor(s, listed.infos()))
	}
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "padmatectl monitor: %v\n", err)
		return 1
	case g == nil, end == endComplete:
		return 0
	}
	why := end.String()
	if end == endStopped {
		why = "stopped by --count"
	}
	fmt.Fprintf(stderr, "padmatectl monitor: %s\n", g.incomplete(why))
	return 1
}

// eventPrinter prints events as text or JSON lines.
type eventPrinter struct {
	w        io.Writer
	json     bool
	list     bool // print only DeviceAdded events
	deadband int
	// lastMu guards last: the raw events of a direct monitor are printed from
	// another goroutine than the gamepad events, and both use the deadband.
	lastMu sync.Mutex
	// last holds the last printed value of each axis, for --deadband.
	last map[axisKey]int16
	// keys gives the gamepads the keys that text lines show.
	keys *input.Registry
}

type axisKey struct {
	device  input.DeviceID
	control string
}

// newEventPrinter returns a printer for the --json, --list and
// --deadband of opts.
func newEventPrinter(w io.Writer, opts monitorOptions) *eventPrinter {
	return &eventPrinter{
		w: w, json: opts.json, list: opts.list, deadband: opts.deadband,
		last: make(map[axisKey]int16), keys: input.NewRegistry(),
	}
}

// print prints e, unless --list or the deadband hides it; shown says
// which.
func (p *eventPrinter) print(e input.Event) (shown bool, err error) {
	if e.Kind == input.DevicePower {
		return false, nil // routing metadata, no input event; a Source sends it only when asked
	}
	if e.Kind == input.DeviceRemapped && (p.json || p.list) {
		return false, nil // not a trace record; the text shows it
	}
	key := deviceKey(p.keys, e)
	if p.list && e.Kind != input.DeviceAdded {
		return false, nil
	}
	if e.Kind == input.AxisMotion && p.deadband > 0 {
		if !p.moved(axisKey{e.Device, e.Control}, e.Value) {
			return false, nil
		}
	}
	if p.json {
		err = input.WriteEvent(p.w, e)
	} else {
		_, err = fmt.Fprintln(p.w, formatMonitorEvent(e, key))
	}
	if err != nil {
		return false, fmt.Errorf("write: %w", err)
	}
	return true, nil
}

// moved reports whether the axis k at value v moved the --deadband from the
// value last printed for it, and if so remembers v as that value.
func (p *eventPrinter) moved(k axisKey, v int16) bool {
	p.lastMu.Lock()
	defer p.lastMu.Unlock()
	if last, ok := p.last[k]; ok && abs(int(v)-int(last)) < p.deadband {
		return false
	}
	p.last[k] = v
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// formatMonitorEvent formats e as a text line, with the key that
// deviceKey returned for it.
func formatMonitorEvent(e input.Event, key string) string {
	t := fmt.Sprintf("%d.%06d", e.Time/1e9, e.Time%1e9/1e3)
	switch e.Kind {
	case input.DeviceAdded:
		if e.Info == nil {
			return fmt.Sprintf("%s +device %d", t, e.Device)
		}
		return t + " " + formatDevice(e.Info) + key
	case input.DeviceRemoved:
		return fmt.Sprintf("%s -device %d%s", t, e.Device, key)
	case input.DeviceRemapped:
		if e.Info == nil {
			return fmt.Sprintf("%s ~device %d remapped", t, e.Device)
		}
		return fmt.Sprintf("%s ~device %d remapped buttons=%#x axes=%#x", t, e.Device, e.Info.ButtonMask, e.Info.AxisMask)
	default:
		return fmt.Sprintf("%s %d %s", t, e.Device, eventKey(e))
	}
}

// formatDevice formats the "+device" part of a DeviceAdded line.
func formatDevice(d *input.DeviceInfo) string {
	s := fmt.Sprintf("+device %d %s %q %04x:%04x driver=%s guid=%s serial=%s path=%s",
		d.ID, d.Type, d.Name, d.Vendor, d.Product, d.Driver, d.GUID, d.Serial, d.Path)
	if d.Virtual {
		s += " virtual"
	}
	if !d.IsGamepad {
		s += " joystick-only"
	}
	return s
}

// deviceKey connects the gamepad of an added event e in keys, or
// disconnects it for a removed one, and returns what the device's line
// shows after it: " key=K", with " reconnected" when K is back, or "" for
// other devices and events.
func deviceKey(keys *input.Registry, e input.Event) string {
	switch e.Kind {
	case input.DeviceAdded:
		if e.Info == nil || !e.Info.IsGamepad {
			return ""
		}
		k, back := keys.Connect(e.Info)
		if back {
			return " key=" + k.String() + " reconnected"
		}
		return " key=" + k.String()
	case input.DeviceRemoved:
		if k, ok := keys.Disconnect(e.Device); ok {
			return " key=" + k.String()
		}
	}
	return ""
}
