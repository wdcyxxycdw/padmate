package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// "padmatectl record" (ENG-08) records the events of one gamepad in the
// format of "monitor --json" (input.WriteEvent), for fake input and the
// traces of daemon/testdata/traces.

const recordUsageHead = `usage: padmatectl record [-o FILE] [--force] [--device ID] [--virtual]
                         [--duration D] [--count N] [--preset NAME]
                         [--hint NAME=VALUE]... [--direct]
                         [--ipc-dir DIR] [--timeout D]

Records the events of one gamepad as JSON lines, in the format of
"padmatectl monitor --json" (see "padmatectl monitor -h"). The gamepad
is the first one reported, or the device of --device. By default this
requires a running daemon; it never falls back to SDL. For an already
connected device, its metadata and first observed transition both
have time 0, with later transitions relative to the first. Earlier
held state is not reconstructed; start with all controls released.

With --direct, SDL is loaded as for monitor; the header names the SDL
version, preset and hints. The first event is the gamepad's "added"
event, with its info, at time 0; times are rebased to it.
Then come its button and axis events, until Ctrl+C,
--duration, --count or its removal, whose "removed" line ends the
recording. A time before the one of the line above is raised to it, so
the recording always reads back. Devices and a summary go to stderr;
direct mode also reports the SDL library, version and hints.

flags:
  -o FILE         write to FILE instead of stdout. FILE is created by the
                  first line, so it is not created if nothing is
                  recorded. Use -o rather than the ">" of Windows
                  PowerShell 5.1, which writes UTF-16.
  --force         overwrite FILE if it exists
  --device ID     record device ID, as monitor prints it, even if it is
                  not the first gamepad (default: the first gamepad)
  --direct        read SDL in this process without a daemon
  --ipc-dir DIR   use this daemon discovery directory
  --timeout D     IPC setup timeout (default 10s, maximum 1m)
  --virtual       record an SDL virtual gamepad driven as by
                  "monitor --virtual", which needs no hardware. Fails
                  unless all of its events are recorded within
                  --duration (default 5s). Implies --direct.
  --duration D    stop after D, such as 10s (default: until Ctrl+C)
  --count N       stop after recording N events
  --preset NAME   direct SDL hint preset (default "default"; see below)
  --hint N=V      direct SDL hint, after the preset; repeatable

Exits 0 once a recording ends, 1 if nothing was recorded, if --virtual
did not record all of its events, if SDL or IPC dropped events or on an
error, and 2 for bad flags.

presets:
`

// recordUsage lists the presets after recordUsageHead.
var recordUsage = recordUsageHead + input.PresetUsage()

// recordOptions are the parsed flags of "padmatectl record".
type recordOptions struct {
	inputConnectionOptions
	out      string // "" for stdout
	force    bool
	device   input.DeviceID // 0 for the first gamepad
	duration time.Duration
	count    int
	virtual  bool
	preset   string
	hints    []input.Hint // the preset's, then the --hint flags
}

// runRecord implements "padmatectl record". The stopSignals end the
// recording.
func runRecord(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	return recordMain(ctx, args, stdout, stderr)
}

func recordMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseRecordFlags(args, stdout, stderr)
	if !ok {
		return code
	}
	var file *traceFile
	w := stdout
	if opts.out != "" {
		if err := checkOutput(opts.out, opts.force); err != nil {
			fmt.Fprintf(stderr, "padmatectl record: %v\n", err)
			return 1
		}
		file = &traceFile{path: opts.out, force: opts.force}
		w = file
	}
	if !opts.direct {
		code = recordIPC(ctx, opts, w, stderr)
		if file != nil {
			if err := file.Close(); err != nil {
				fmt.Fprintln(stderr, "padmatectl record:", err)
				code = 1
			}
		}
		return code
	}
	s := openSource("record", opts.hints, opts.preset, stderr)
	if s == nil {
		return 1
	}
	var g *virtualRun
	if opts.virtual {
		var err error
		if g, err = startVirtual(ctx, s); err != nil {
			fmt.Fprintf(stderr, "padmatectl record: %v\n", err)
			closeSource("record", s, stderr)
			return 1
		}
	}
	header := traceHeader(version.String(), s.Version(), opts.preset, s.Hints())
	code = recordEvents(ctx, s.Events(), g, opts, header, w, stderr)
	if g != nil {
		g.stop()
	}
	if closeSource("record", s, stderr) > 0 {
		fmt.Fprintln(stderr, "padmatectl record: the recording may be missing events")
		code = 1
	}
	if file != nil {
		if err := file.Close(); err != nil {
			fmt.Fprintf(stderr, "padmatectl record: %v\n", err)
			code = 1
		}
	}
	return code
}

// parseRecordFlags parses and checks the flags. When ok is false, the
// command exits with code.
func parseRecordFlags(args []string, stdout, stderr io.Writer) (opts recordOptions, code int, ok bool) {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	opts.flags(fs)
	fs.StringVar(&opts.out, "o", "", "write to this file")
	fs.BoolVar(&opts.force, "force", false, "overwrite the file")
	device := fs.Uint64("device", 0, "record this device")
	fs.DurationVar(&opts.duration, "duration", 0, "stop after this time")
	fs.IntVar(&opts.count, "count", 0, "stop after this many events")
	fs.BoolVar(&opts.virtual, "virtual", false, "record a virtual gamepad")
	var sf input.SDLFlags
	sf.AddFlags(fs)
	if code, ok := parseFlags(fs, args, recordUsage, stdout, stderr); !ok {
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
	case *device > math.MaxUint32:
		problem = fmt.Sprintf("--device must be at most %d", uint64(math.MaxUint32))
	case *device != 0 && opts.virtual:
		problem = "--device and --virtual cannot be combined"
	case opts.force && opts.out == "":
		problem = "--force needs -o"
	case opts.duration < 0, opts.count < 0:
		problem = "--duration and --count cannot be negative"
	default:
		problem = opts.validate(fs, opts.virtual)
	}
	if problem != "" {
		fmt.Fprintf(stderr, "padmatectl record: %s\n", problem)
		fmt.Fprint(stderr, recordUsage)
		return opts, 2, false
	}
	opts.device = input.DeviceID(*device)
	opts.hints = hints
	return opts, 0, true
}

// checkOutput reports why the -o file cannot be written, before SDL is
// loaded.
func checkOutput(path string, force bool) error {
	st, err := os.Stat(path)
	switch {
	case err == nil && st.IsDir():
		return fmt.Errorf("%s is a directory", path)
	case err == nil && !force:
		return fmt.Errorf("%s exists; use --force to overwrite it", path)
	case err == nil:
		return nil
	}
	dir := filepath.Dir(path)
	switch dst, derr := os.Stat(dir); {
	case errors.Is(derr, fs.ErrNotExist):
		return fmt.Errorf("the directory %s does not exist", dir)
	case derr == nil && !dst.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	}
	return err
}

// traceFile is the -o file. The first write creates it, so that a run
// that records nothing leaves no file, and with --force the old one.
type traceFile struct {
	path  string
	force bool
	f     *os.File
}

func (w *traceFile) Write(p []byte) (int, error) {
	if w.f == nil {
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if w.force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(w.path, flags, 0o666)
		if errors.Is(err, fs.ErrExist) {
			return 0, fmt.Errorf("%s exists; use --force to overwrite it", w.path)
		}
		if err != nil {
			return 0, err
		}
		w.f = f
	}
	return w.f.Write(p)
}

// Close closes the file, if the first write created it.
func (w *traceFile) Close() error {
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// recordEvents records the chosen device's events to w, after header,
// until the end that opts set, with the virtual gamepad g or nil, and
// returns the exit code.
func recordEvents(ctx context.Context, events <-chan input.Event, g *virtualRun, opts recordOptions, header string, w, stderr io.Writer) int {
	r := &recorder{w: w, stderr: stderr, want: opts.device, count: opts.count, header: header, keys: input.NewRegistry()}
	wait := opts.duration
	if g != nil {
		r.want = g.id
		if wait == 0 {
			wait = monitorVirtualWait
		}
	}
	end, err := pump(ctx, events, g, wait, r.handle)
	code := 0
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "padmatectl record: %v\n", err)
		code = 1
	case g != nil && end != endComplete:
		why := end.String()
		if end == endStopped {
			why = "stopped by --count"
		}
		fmt.Fprintf(stderr, "padmatectl record: %s\n", g.incomplete(why))
		code = 1
	case r.n == 0:
		what := "a gamepad"
		if r.want != 0 {
			what = fmt.Sprintf("device %d", r.want)
		}
		fmt.Fprintf(stderr, "padmatectl record: %s before %s appeared\n", end, what)
		code = 1
	}
	if r.n > 0 {
		to := opts.out
		if to == "" {
			to = "stdout"
		}
		fmt.Fprintf(stderr, "padmatectl record: %d events in %.3fs, written to %s\n", r.n, float64(r.last-r.t0)/1e9, to)
	}
	if r.raised > 0 {
		fmt.Fprintf(stderr, "padmatectl record: raised the times of %d events to that of the event before them\n", r.raised)
	}
	return code
}

// recorder writes the events of one device, rebased to its added event,
// after the header.
type recorder struct {
	w, stderr io.Writer
	want      input.DeviceID // 0: the first gamepad
	count     int            // 0: no limit
	header    string         // written before the first event

	dev      input.DeviceID  // the recorded device, 0 until it appears
	t0, last uint64          // the time of its added event and of its last event
	n        int             // events written
	raised   int             // events whose time was raised to the one before
	keys     *input.Registry // the keys that the device lines on stderr show
}

func (r *recorder) handle(e input.Event) (more bool, err error) {
	if e.Kind == input.DevicePower {
		return true, nil // routing metadata, not a trace record; a Source sends it only when asked
	}
	if e.Kind == input.DeviceRemapped {
		if r.dev != 0 && e.Device == r.dev {
			fmt.Fprintf(r.stderr, "padmatectl record: device %d was remapped; the recording goes on\n", e.Device)
		}
		return true, nil // not a trace record
	}
	key := deviceKey(r.keys, e)
	if e.Device != r.dev || r.dev == 0 {
		if e.Kind != input.DeviceAdded {
			return true, nil
		}
		if r.dev != 0 || !r.chooses(e) {
			fmt.Fprintf(r.stderr, "padmatectl record: not recording %s\n", addedDevice(e, key))
			return true, nil
		}
		if e.Info != nil && !e.Info.IsGamepad {
			return false, fmt.Errorf("device %d is a joystick without a gamepad mapping, whose buttons and axes are not read", e.Device)
		}
		fmt.Fprintf(r.stderr, "padmatectl record: recording %s\n", addedDevice(e, key))
		r.dev, r.t0, r.last = e.Device, e.Time, e.Time
	}
	if e.Time < r.last {
		e.Time = r.last
		r.raised++
	}
	r.last = e.Time
	e.Time -= r.t0
	if r.n == 0 && r.header != "" {
		if _, err := io.WriteString(r.w, r.header); err != nil {
			return false, err
		}
	}
	if err := input.WriteEvent(r.w, e); err != nil {
		return false, err
	}
	r.n++
	return e.Kind != input.DeviceRemoved && (r.count == 0 || r.n < r.count), nil
}

// chooses reports whether the added event e is of the device to record.
func (r *recorder) chooses(e input.Event) bool {
	if r.want != 0 {
		return e.Device == r.want
	}
	return e.Info != nil && e.Info.IsGamepad
}

// addedDevice formats the device of the added event e as monitor does,
// with the key that deviceKey returned for it.
func addedDevice(e input.Event, key string) string {
	if e.Info == nil {
		return fmt.Sprintf("+device %d", e.Device)
	}
	return formatDevice(e.Info) + key
}

// traceHeader returns the comment lines that start a recording: the
// versions of padmatectl and SDL, the preset and the hints in effect,
// such as
//
//	# padmatectl record v0.1.0; SDL 3.4.16; preset default
//	# hints in effect: SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS=1 SDL_NO_SIGNAL_HANDLERS=1 SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0
//	# hints unset (SDL's default): SDL_JOYSTICK_HIDAPI ... SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS
//
// It leaves out the library's path, which names a local directory.
func traceHeader(padmate, sdl, preset string, hints []input.Hint) string {
	var set, unset []string
	for _, h := range hints {
		if h.Value == "" {
			unset = append(unset, commentWord(h.Name))
		} else {
			set = append(set, commentWord(h.Name)+"="+commentWord(h.Value))
		}
	}
	lines := []string{fmt.Sprintf("# padmatectl record %s; SDL %s; preset %s", commentWord(padmate), commentWord(sdl), commentWord(preset))}
	if len(set) > 0 {
		lines = append(lines, "# hints in effect: "+strings.Join(set, " "))
	}
	if len(unset) > 0 {
		lines = append(lines, "# hints unset (SDL's default): "+strings.Join(unset, " "))
	}
	return strings.Join(lines, "\n") + "\n"
}

// commentWord returns s quoted as a Go string if it has a space, a
// separator of traceHeader or a character that is not printable, so that
// it stays one word of one comment line.
func commentWord(s string) string {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool {
		return r == ' ' || r == ';' || r == '=' || r == '"' || !unicode.IsPrint(r) || r == unicode.ReplacementChar
	}) {
		return strconv.Quote(s)
	}
	return s
}
