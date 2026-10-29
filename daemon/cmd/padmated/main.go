// Command padmated is the padmate daemon: gamepad input, the mapping
// engine, keyboard output, scripting, the tray icon and IPC.
//
// It maps up to 4 gamepads live (DMN-02, DMN-03, IN-08): SDL input on
// T-input, the supervisor and its slot loops, and T-output with the
// Resolver. With --dry-run the printer is the output.Sender and nothing
// is injected (OUT-09); only --inject presses keys, through SendInput,
// which only sender_windows.go constructs (CLAUDE.md invariants 3 and 6,
// checked by TestNoSendInput and forbidigo). Saving the profile reloads
// it (PRO-03): a watcher (package watch) polls the file on a goroutine of
// its own, and Supervisor.Reload switches the slots to the new program.
// The panic combo pauses the mapping and resumes it (DMN-07): T-input
// finds it and lets go of every key through output.Loop.Block, and the
// ComboHeld it sends toggles the supervisor's pause. The session's events
// suspend the mapping (DMN-09): winsession sees the lock, a disconnected
// session, sleep, the end of the session and the secure desktop, and
// Supervisor.Suspend lets go of every key; the end of the session and
// WM_CLOSE stop padmated as Ctrl+C does. --fake-input replays recorded
// input in dry-run mode instead (DMN-01), with the ComboHeld events that
// input.ExpandCombos inserts, and watches nothing: neither the profile
// nor the session.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/autostart"
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

const usageHead = `usage: padmated --profile FILE (--dry-run | --inject) [--decisions]
                [--device S] [--preset NAME] [--hint NAME=VALUE]...
                [--watch=false] [--tray=false] [--log-level L] [--log-file F]
       padmated --profile FILE --fake-input FILE --dry-run [--decisions]
                [--log-level L] [--log-file F]
       padmated --profile FILE --fake-input --dry-run [--tray=false]
       padmated --profile-id ID [--data-dir DIR] (--dry-run | --inject)
       padmated --data-dir DIR (--dry-run | --inject)
       padmated --startup-report FILE <live mode flags>
       padmated -version

Live mode (DMN-02) maps the gamepads with the profile until Ctrl+C. It
needs one of --dry-run, which prints the keys padmated would press and
injects nothing, or --inject, which presses them in the foreground
window. When it stops, it releases every key still held.
On Windows the desktop entry also shows a tray icon. Its menu pauses or
resumes mapping, enables startup at login using the current profile and
output mode, or exits: it tells the IPC clients first (daemon.exiting,
DMN-22), so that the UI quits too, then releases all keys. --tray=false
hides this UI.
Startup is off until enabled from the menu; it uses absolute paths.
If an instance is already running, this invocation authenticates to its
IPC endpoint and forwards the profile selection, then exits. Output and
input modes must match; explicit conflicting startup settings fail.
--profile-id selects in the running library (an explicit --data-dir must
match); --profile switches files in a standalone instance. A data directory
without a profile only acknowledges the existing instance. File paths are
made absolute before forwarding. No second input or output loop starts.
Up to 4 gamepads are mapped at once (IN-08), each on a slot of its own,
all with the profile; an idle one is mapped too, so leave it out with
--device. They share one keyboard: a key two of them hold goes up when
the last lets go, and a modifier one holds combines with the keys of
another. More wait in line (IN-05): one that connects goes to the back,
one that loses its slot to the front. A mapped gamepad keeps its slot
once it presses a button, until it leaves. Before that, a waiting
gamepad takes the slot over with a button press, which is mapped; of
several such slots, the one assigned longest ago, so the gamepad in
hand wins over an idle one that connected first. Sticks and triggers
(LT, RT) are not buttons: moving them neither keeps nor takes a slot.
When a mapped gamepad leaves, the first in line stands in at once, with
the buttons it holds. A gamepad that reconnects takes a free slot when
there is one, and a stand-in on its old slot that has not pressed a
button yet keeps that slot as its own; otherwise it takes the slot back
from that stand-in (a slot belongs to the last gamepad that found it
free or pressed a button on it, or to the stand-in that kept it).

Saving the profile reloads it (PRO-03), unless --watch=false: padmated
looks at the file every 100 ms, and once its size and modification time
have stayed the same for 200 ms it reads it and maps with it from then
on. The reload releases every key held,
once; a control held through it does nothing until it is pressed again.
A profile with errors is not loaded: padmated prints them, logs "profile
not reloaded" and maps on with the profile it has. A save that leaves
the content as it runs does nothing. padmated also watches the scripts
at the top of the profile's macros folder: saving one the profile runs
rebuilds the script runtimes, which ends the scripts running.

Holding back and start together for 2 s (SL and SR on a single Joy-Con)
pauses the mapping (DMN-07): padmated releases every key held, and
presses map to nothing until the combo is held for 2 s again, which
resumes it; a control held through the resume does nothing until it is
pressed again. Any gamepad can hold it, also one that waits for a slot
or that --device leaves out. A replay pauses the same way, with the
combo of the device it replays.

Locking the computer, disconnecting the session, sleep, signing out or
shutting down, and the secure desktop (such as a UAC prompt) suspend
the mapping in live mode (DMN-09): padmated releases every key held,
and presses map to nothing until they are over; a control held through
the end does nothing until it is pressed again. A pause stays on after
it. When the session ends, or taskkill without /f asks padmated to
close, it stops as for Ctrl+C.

With --fake-input FILE padmated replays recorded input instead (DMN-01): JSON
lines as "padmatectl monitor --json" and "padmatectl record" write them,
mapped in virtual time. It replays one device, the first with a button
or axis event, with its times rebased to its first event. At the end of
the input it exits the way padmated always exits: it releases every key
still held.

A bare --fake-input (or --fake-input=ipc) starts a persistent IPC-driven
synthetic controller instead of SDL. Use padmatectl fake press/release
CONTROL and fake axis AXIS VALUE to drive it. This mode requires --dry-run
and rejects --inject. It shares normal mapping, pause, profile, script and
shutdown behavior; it does not create a virtual controller in Windows.
The reserved name ipc can be read as a trace with --fake-input ./ipc.

Problems in the profile are printed to stderr as "padmatectl validate"
prints them, at the start and at each reload; warnings do not stop
padmated. In live mode stderr also lists, at the start, the SDL library
and the SDL hints in effect.

The output of --dry-run is a header, then one line per engine decision
and one per batch of key events sent, as padmated handles them (the
rows of several slots interleave), with times in ms since the first
event of a replay, or since padmated started in live mode. The columns
are as wide as the profile needs; a replay with a
profile that binds north and south:

      time  input               reason    action      label   binding             owner
   512.304  north down          dispatch  tap B       "画笔"  /layers/base/north  decl/1
   512.304    send +B
   532.304    send -B
   583.911  north up            end                   "画笔"  /layers/base/north

input is the control and its edge, with the value for lt and rt. reason
is dispatch (the press ran its action), release (the release let go of
the chord it held), unbound (no action), end (the release of a press
that holds nothing), cancel (a removed device, a reload of the profile,
a pause, a suspension or the exit ended the press), canceled (the
release of a press that a cancel ended, or that was pressed while
paused or suspended), spurious (a press of a control already down, a
release of one already up) or paused (a press while the mapping is
paused or suspended, which it ends at once). action is
the mode and the chord, label the binding's label, binding its JSON
pointer and owner the owner of the keys sent. A send line is one batch:
+Key presses, -Key releases; vk:0xE8 is the menu mask, pressed and
released right before the release of an Alt or Win key that nothing was
pressed with (OUT-08); in live mode the keys are those --inject would
press for the keyboard layout of the foreground window. "device N
added", "device N removed: release all", "profile reloaded: release
all", "mapping paused: release all", "mapping resumed", "mapping
suspended (lock): release all", "mapping unsuspended" and "exit:
release all" mark those moments. A suspension names its reasons: lock,
disconnect, desktop, sleep and end, joined with commas. When the
mapping is suspended or paused already, nothing is released, and the
notes are "mapping suspended (lock,desktop)", "mapping paused (still
suspended: lock)", "mapping resumed (still suspended: lock)" and
"mapping unsuspended (still paused)". The send lines of the release of
a pause or a suspension may come before its note. In live mode the
device lines name the gamepad and its slot, such as 'device 3 "Xbox
Controller" added to slot 0'; a gamepad that takes no slot shows only
in the log. With several slots, a column after the time gives each
decision's slot, and with --decisions each decision starts with it, as
{"slot":0,"t":20,...}; a send has no slot, as the slots share the keys.
A stick that moves the pointer prints "move +dx,+dy", the sum of the moves
of 100 ms, at the time of the first of them, and the jump action "jump x,y"
(0 to 65535 across the desktop); with --decisions {"t":8,"move":[104,0]}
and {"t":400,"jump":[32768,32768]}.
A typed text (OUT-21) prints its characters as one line, text "你好", at the
time of the first (a character within 250 ms of the one before joins the
line, up to 80 characters; control characters and quotes are escaped);
with --decisions {"t":8,"text":"你好"}. A newline and a tab in the text are
Enter and Tab taps and print as send lines. The media and volume keys print
by name, such as +VolumeUp.
With --inject padmated prints only the header.

The log (DMN-03) is JSON lines, to stderr, or, when padmated has no
stderr, to %APPDATA%\padmate\logs\padmated.log. --log-file F appends to
F instead, or with "-" writes to stderr always; the file only grows
until DMN-11 rotates it. A replay logs only at debug level.

Exits 0, also when Ctrl+C, the end of the session or the tray's exit
stops live mode, or when the running instance took the request; 1 if
the profile, the fake input or the log of a replay has errors or cannot
be opened, the SDL input or the output stops, the output cannot be
written, or a replay is interrupted; 2 for a usage error; 3 when live
mode does not start (UI-45): the data folder, config.json, state.json,
the profile or the log has errors or cannot be opened, another instance
runs and does not take the request, SDL does not start, the IPC
endpoint or the output cannot be created, or starting panics.

--startup-report FILE is for the program that starts padmated, such as
the UI, which cannot read stderr. padmated removes FILE when its flags
are read, and writes it when live mode then does not start, or for a
usage error once the flag is read (so put it first): one JSON object,
{"version":1,"kind":...,"cause":...,"file":...,"line":...,"col":...,
"pointer":...,"message":...,"dataDir":...,"errors":[{"line":...,
"col":...,"pointer":...,"message":...}]}. kind is usage (exit 2),
instance, library, config, state, profile, log, ipc, sdl, output or
internal (exit 3); cause is invalid (file has the errors listed),
missing, denied, refused (by the running instance), unavailable (it did
not answer) or other; file is the file at fault, absolute; line, col
and pointer locate its first error; message is in English, without
file. Every field is always there: "" and 0 mean unknown. FILE's folder
is created when missing. No FILE once padmated has exited means that
live mode started, that the running instance took the request, or that
padmated was killed first.

flags:
  --profile FILE     the profile to map with
  --profile-id ID    load profiles/ID/profile.json from the profile library
  --data-dir DIR     library and settings directory (default user config/padmate)
                     without --profile/--profile-id, restore the last selection
  --dry-run          print the keys instead of pressing them
  --inject           press the keys in the foreground window (live mode)
  --decisions        with --dry-run, print JSON lines instead, and the
                     header to stderr: each decision as
                     {"t":20,"control":"south",...} (ENG-20), with
                     several slots {"slot":0,"t":20,...}, and each
                     batch as {"t":20,"send":["-Z"]}, t in ms
  --fake-input [FILE] replay FILE; without FILE, accept fake input over IPC
  --device S         map only the gamepads whose name or device key, as
                     "padmatectl monitor" shows them, contains S, in any
                     case (live mode)
  --preset NAME      the SDL hint preset (live mode; default "default";
                     see below)
  --hint N=V         set SDL hint N to V, after the preset; repeatable
                     (live mode)
  --watch            reload the profile when its file changes, and rebuild
                     the script runtimes when a script it runs in its
                     macros folder changes (live mode; default true:
                     --watch=false turns it off)
  --log-level L      debug, info, warn or error (default info)
  --log-file F       the log file; "-" for stderr
  --startup-report FILE
                     when live mode does not start, write why to FILE as
                     JSON (see above); put it first
  -version           print the version and exit

presets:
`

// usage lists the presets after usageHead.
var usage = usageHead + input.PresetUsage()

// stopSignals end padmated: Ctrl+C, and SIGTERM, which Go also delivers
// on Windows when the console window closes. Logoff and shutdown give no
// signal: Windows sends those console events only to programs that have
// not loaded user32 or gdi32, and padmated loads user32 (SDL, SendInput);
// it learns of them from winsession's WM_ENDSESSION instead. The
// GUI-subsystem build gets no console events at all. An interrupted run
// still takes the exit path, which releases every key.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

func main() {
	os.Exit(runWithUI(os.Args[1:], desktopOutput(os.Stdout), os.Stderr, runNativeTray))
}

// Desktop GUI processes may have no stdout handle. Dry-run still uses the
// printer as its safe sender; discarded diagnostics must not break mapping.
func desktopOutput(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	if _, err := f.Stat(); err != nil {
		return io.Discard
	}
	return f
}

// run runs padmated with args and returns its exit code. The stopSignals
// interrupt it.
func run(args []string, stdout, stderr io.Writer) int {
	return runWithUI(args, stdout, stderr, nil)
}

func runWithUI(args []string, stdout, stderr io.Writer, ui func(*liveRun) int) int {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	return runContextUI(ctx, args, stdout, stderr, ui)
}

// runContext is run, interrupted when ctx is done.
func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runContextUI(ctx, args, stdout, stderr, nil)
}

func runContextUI(ctx context.Context, args []string, stdout, stderr io.Writer, ui func(*liveRun) int) int {
	exe, err := os.Executable()
	if err == nil {
		args, err = autostart.Expand(args, exe)
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmated: startup:", err)
		return 2
	}
	opts, code, ok := parseFlags(args, stdout, stderr)
	if !ok {
		if code == 2 {
			writeStartupReport(opts, stderr, &startupError{kind: "usage", err: errors.New(opts.usageProblem)})
		}
		return code
	}
	clearStartupReport(opts, stderr)
	opts.ui = ui
	if !opts.version && opts.fakeInput != "" && opts.fakeInput != "ipc" && (opts.profileID != "" || opts.dataDir != "") {
		if err := resolveLibrary(&opts); err != nil {
			fmt.Fprintln(stderr, "padmated:", err)
			return 1
		}
		// A replay never writes the data folder (PRO-29).
		if opts.configRepair != nil {
			fmt.Fprintln(stderr, "padmated: config.json has errors; this replay runs without them:", opts.configRepair.Err)
		}
		if opts.stateErr != nil {
			fmt.Fprintln(stderr, "padmated: state.json has errors; this replay runs without it:", opts.stateErr)
		}
	}
	switch {
	case opts.version:
		fmt.Fprintln(stdout, "padmated", version.String())
		return 0
	case opts.fakeInput != "" && opts.fakeInput != "ipc":
		return dryRun(ctx, opts, stdout, stderr)
	default:
		return live(ctx, opts, stdout, stderr)
	}
}

// options are padmated's flags. --fake-input replays the file in virtual
// time and exits. The name stays: IPC-09 (M9) builds on the --fake-input
// mode, whose fake.press, fake.release and fake.axis methods feed a
// running daemon instead of the gamepads.
type options struct {
	forwardArgs        []string // parsed startup arguments, before local library resolution
	explicitFlags      []string
	profile, fakeInput string
	profileID, dataDir string
	autoProfile        bool
	missingLastProfile string // the remembered profile resolveLibrary found gone
	// defaultCreated and defaultErr say what resolveLibrary's
	// EnsureDefault did, for live mode to log; macroMigration and
	// macroMigrationErr what its MigrateMacros did (SCR-51).
	defaultCreated     bool
	defaultErr         error
	macroMigration     profilelib.MacroMigration
	macroMigrationErr  error
	library            *profilelib.Library
	settings           *profile.Config
	memory             *profilelib.Memory
	dryRun, inject     bool
	decisions, version bool
	watch              bool           // live: reload the profile when it is saved
	device             string         // live: supervisor.Config.Device
	sdl                input.SDLFlags // live: --preset and --hint
	hints              []input.Hint   // live: the hints of sdl
	logLevel           slog.Level
	logFile            string
	tray               bool
	ui                 func(*liveRun) int
	// startupReport is where a failed start says why (UI-45); usageProblem
	// is the usage error parseFlags printed.
	startupReport, usageProblem string
	// configRepair is what resolveLibrary's ReadRepairedConfig repaired
	// (PRO-29), nil for a config.json without errors. stateErr is why
	// state.json was not used; stateReset whether it was then reset, and
	// stateResetErr why that failed.
	configRepair  *profilelib.ConfigRepair
	stateErr      error
	stateReset    bool
	stateResetErr error
}

// liveOnly are the flags of live mode only.
var liveOnly = []string{"inject", "device", "preset", "hint", "watch", "tray"}

// chooseMode is the end of the usage errors about the mode.
const chooseMode = "choose --dry-run to print the keys or --inject to press them"

// parseFlags parses and checks the flags. When ok is false, padmated
// exits with code: 0 after printing help, 2 after a usage error.
func parseFlags(args []string, stdout, stderr io.Writer) (opts options, code int, ok bool) {
	// Keep the original --fake-input FILE syntax. A bare flag starts the
	// persistent IPC source; an actual file called ipc can use ./ipc.
	args = slices.Clone(args)
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if (arg == "--fake-input" || arg == "-fake-input") && (i+1 == len(args) || strings.HasPrefix(args[i+1], "-")) {
			args[i] = "--fake-input=ipc"
		}
	}
	fs := flag.NewFlagSet("padmated", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage is printed below, to the right stream
	fs.StringVar(&opts.profile, "profile", "", "the profile to map with")
	fs.StringVar(&opts.profileID, "profile-id", "", "the profile library id")
	fs.StringVar(&opts.dataDir, "data-dir", "", "the library and settings directory")
	fs.StringVar(&opts.fakeInput, "fake-input", "", "the input to replay")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the keys instead of pressing them")
	fs.BoolVar(&opts.inject, "inject", false, "press the keys")
	fs.BoolVar(&opts.decisions, "decisions", false, "print JSON lines")
	fs.BoolVar(&opts.version, "version", false, "print the version and exit")
	fs.StringVar(&opts.device, "device", "", "the gamepads to map")
	fs.BoolVar(&opts.watch, "watch", true, "reload the profile when its file changes")
	fs.BoolVar(&opts.tray, "tray", true, "show the desktop tray in live mode")
	opts.sdl.AddFlags(fs)
	fs.Func("log-level", "debug, info, warn or error", func(s string) (err error) {
		opts.logLevel, err = logging.ParseLevel(s)
		return err
	})
	fs.StringVar(&opts.logFile, "log-file", "", "the log file")
	fs.StringVar(&opts.startupReport, "startup-report", "", "where a failed start says why")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return opts, 0, false
	case err != nil:
		// fs printed err already.
		opts.usageProblem = err.Error()
		fmt.Fprint(stderr, usage)
		return opts, 2, false
	}
	var set []string
	fs.Visit(func(f *flag.Flag) {
		opts.explicitFlags = append(opts.explicitFlags, f.Name)
		if slices.Contains(liveOnly, f.Name) {
			set = append(set, f.Name)
		}
	})
	opts.forwardArgs = slices.Clone(args)
	switch {
	case opts.version:
		return opts, 0, true
	case fs.NArg() > 0:
		opts.usageProblem = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	case opts.profile != "" && opts.profileID != "":
		opts.usageProblem = "--profile and --profile-id cannot be combined"
	case opts.profileID != "" && !profilelib.ValidID(opts.profileID):
		opts.usageProblem = "invalid --profile-id"
	case opts.profile == "" && opts.profileID == "" && opts.dataDir == "":
		opts.usageProblem = "--profile is required"
	case opts.fakeInput != "" && opts.fakeInput != "ipc" && len(set) > 0:
		opts.usageProblem = fmt.Sprintf("--%s is only for live mode, not with --fake-input", set[0])
	case opts.fakeInput != "" && (!opts.dryRun || opts.inject):
		opts.usageProblem = "--fake-input needs --dry-run"
	case opts.fakeInput != "" && opts.fakeInput != "ipc":
		return opts, 0, true
	case !opts.dryRun && !opts.inject:
		opts.usageProblem = "live mode needs --dry-run or --inject: " + chooseMode
	case opts.dryRun && opts.inject:
		opts.usageProblem = "--dry-run and --inject cannot be combined: " + chooseMode
	case opts.decisions && !opts.dryRun:
		opts.usageProblem = "--decisions needs --dry-run"
	default:
		hints, err := opts.sdl.Hints()
		if err == nil {
			opts.hints = hints
			return opts, 0, true
		}
		opts.usageProblem = err.Error()
	}
	fmt.Fprintln(stderr, "padmated: "+opts.usageProblem)
	fmt.Fprint(stderr, usage)
	return opts, 2, false
}

// openLog opens the log of opts (DMN-03). When it does not open, it
// prints the error and ok is false. closeLog closes the log, and sets
// *code to 1 when that fails.
func openLog(opts options, stderr io.Writer) (log *slog.Logger, closeLog func(code *int), ok bool) {
	log, closeLog, err := openStartupLog(opts, stderr)
	return log, closeLog, err == nil
}

// openStartupLog is openLog with the error: a startupError of kind log.
func openStartupLog(opts options, stderr io.Writer) (log *slog.Logger, closeLog func(code *int), err error) {
	log, c, err := logging.Open(logging.Options{Level: opts.logLevel, File: opts.logFile, Stderr: stderr})
	if err != nil {
		fmt.Fprintln(stderr, "padmated:", err) // it starts with "logging: "
		file := opts.logFile
		if file == "" || file == "-" {
			file, _ = logging.DefaultFile()
		}
		return nil, nil, &startupError{kind: "log", file: file, err: err}
	}
	return log, func(code *int) {
		if err := c.Close(); err != nil {
			fmt.Fprintln(stderr, "padmated: closing the log:", err)
			*code = 1
		}
	}, nil
}

// dryRun replays the fake input through the profile and prints what
// padmated would do. It logs only at debug level.
func dryRun(ctx context.Context, opts options, stdout, stderr io.Writer) (code int) {
	p, prog, _, ok := loadSelectedProfile(opts, stderr)
	if !ok {
		return 1
	}
	log, closeLog, ok := openLog(opts, stderr)
	if !ok {
		return 1
	}
	defer closeLog(&code)
	log.Debug("padmated starting", "version", version.String(), "profile", opts.profile, "mode", "replay")
	defer func() { log.Debug("padmated stopped", "code", code) }()
	all, err := readInput(opts.fakeInput)
	if err != nil {
		fmt.Fprintln(stderr, "padmated:", err)
		return 1
	}
	dev, evs, note := selectDevice(all)
	if note != "" {
		fmt.Fprintln(stderr, "padmated:", note)
	}
	return replayDryRun(ctx, opts, p, prog, dev, evs, stdout, stderr, log)
}

// replayDryRun prints the header and replays evs, the events of dev,
// through prog with the dry-run printer, and returns the exit code. The
// printer is below a MenuMask, as the Loop's Sender is in live mode
// (OUT-08), so the replay prints the keys live mode would press. Among
// evs go the ComboHeld events that T-input would send (DMN-07), for the
// default combo of dev's type, a single Joy-Con held sideways as without
// the hint SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS: each pauses the
// mapping, or resumes it. The header counts the events of the input only.
func replayDryRun(ctx context.Context, opts options, p *profile.Profile, prog *engine.Program, dev device, evs []input.Event, stdout, stderr io.Writer, loggers ...*slog.Logger) int {
	n := len(evs)
	comboFor := configuredCombo(opts.settings)
	if comboFor == nil {
		comboFor = input.DefaultCombo
	}
	evs = input.ExpandCombos(evs, func(info input.DeviceInfo) input.Combo { return comboFor(info, false) })
	clk := clock.NewFake()
	w := bufio.NewWriter(stdout)
	// A minute is how long Replay may settle after the last event.
	pr := newPrinter(w, clk, opts.decisions, prog, evs[len(evs)-1].Time/1e6+uint64(time.Minute/time.Millisecond))
	arb := output.NewArbiter(clk, output.NewMenuMask(pr), arbiterTiming(prog, opts.settings))
	def := newProfileScriptDefinition(opts.profile, p, false, loggers...)
	if opts.library != nil {
		def.useLibrary(opts.library.Dir())
	}
	def.debugOnly = true
	runtime, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		fmt.Fprintln(stderr, "padmated: script:", err)
		return 1
	}
	sc := slot.Config{
		Index: 0, Gen: 1, Program: prog, Clock: clk,
		Arbiter: arb, Script: runtime,
		Trace: func(d engine.Decision) { pr.decision(0, d) }, OnEvent: pr.event,
		OnHaptic: func(_ input.DeviceID, h engine.Haptic) { pr.haptic(0, h) },
		Feedback: func(f engine.Feedback) { pr.feedback(0, f) },
	}
	if st := opts.settings; st != nil {
		// The feedback switches of config.json (UI-27, ADR-0067) shape the
		// printed haptics and feedback states as they do the live ones.
		sw := st.Feedback
		sc.Switches = func() profile.FeedbackSwitches { return sw }
	}
	s, err := slot.New(sc)
	if err != nil {
		fmt.Fprintln(stderr, "padmated:", err) // not reached: the Config is complete
		return 1
	}
	header := w
	if opts.decisions {
		header = bufio.NewWriter(stderr) // stdout is only JSON lines
	}
	fmt.Fprintln(header, "padmated: dry run, nothing is injected")
	fmt.Fprintf(header, "profile:    %s, %q, %s\n", opts.profile, p.Name, count(len(prog.Bindings()), "binding"))
	fmt.Fprintf(header, "fake input: %s, %s of %s\n", opts.fakeInput, count(n, "event"), dev)
	if opts.decisions {
		header.Flush() // to stderr: nowhere to report an error
	} else {
		fmt.Fprintln(w)
		pr.columnHeader()
	}
	return replay(ctx, s, clk, evs, pr, stderr)
}

// replay replays evs through s and returns the exit code. Whatever ends
// it, it takes the exit path.
func replay(ctx context.Context, s *slot.Slot, clk *clock.Fake, evs []input.Event, pr *printer, stderr io.Writer) (code int) {
	defer func() {
		// The exit path (CLAUDE.md invariant 1), on every return and while
		// a panic unwinds: release every key still held and run the
		// retries of releases that failed (none: the printer never fails),
		// then report the slot's errors and flush the output. Not
		// ReleaseAll: it would rebuild a macro runtime that Dispose then
		// tears down.
		pr.exit()
		errs := []error{s.ReleaseAllForExit(), s.Replay(clk, nil), s.Dispose()}
		errs = append(errs, s.Errors()...)
		for _, err := range errs {
			if err != nil {
				fmt.Fprintln(stderr, "padmated:", err)
				code = 1
			}
		}
		if err := pr.flush(); err != nil {
			fmt.Fprintln(stderr, "padmated: writing the output:", err)
			code = 1
		}
	}()
	if err := s.Replay(clk, evs); err != nil {
		fmt.Fprintln(stderr, "padmated:", err)
		code = 1
	}
	// Replay runs in virtual time and cannot be stopped halfway, so a
	// stop signal only shows here.
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "padmated: interrupted")
		code = 1
	}
	return code
}

// loadProfile reads the profile at path and parses and compiles it
// (parseProfile). sum is the sha256 of the bytes read, the content the
// watcher starts from. ok is false when the file cannot be read or has
// errors.
func loadProfile(path string, stderr io.Writer) (p *profile.Profile, prog *engine.Program, sum [32]byte, ok bool) {
	p, prog, sum, err := loadStartupProfile(options{profile: path}, stderr)
	return p, prog, sum, err == nil
}

// errProfile is parseProfile's error for a profile whose diagnostics have
// errors.
var errProfile = errors.New("the profile has errors")

// parseProfile parses and compiles data, the content of the profile at
// path, and prints its diagnostics ds to stderr as "padmatectl validate"
// does. err is errProfile when ds has errors, or the error of Compile.
func parseProfile(path string, data []byte, stderr io.Writer, settings ...*profile.Config) (p *profile.Profile, prog *engine.Program, ds profile.Diagnostics, err error) {
	return parseProfileIn(nil, "", path, data, stderr, settings...)
}

// parseProfileIn is parseProfile for library profile id when lib is not
// nil: its extends is resolved with the library (PRO-19, ADR-0070), and a
// problem in a parent's file is printed with that file's path. Without a
// library, or an id (the file that --profile names, the disabled profile),
// extends is an error.
func parseProfileIn(lib *profilelib.Library, id, path string, data []byte, stderr io.Writer, settings ...*profile.Config) (p *profile.Profile, prog *engine.Program, ds profile.Diagnostics, err error) {
	data, ds = profile.Migrate(data)
	if !ds.HasErrors() {
		if lib != nil && id != "" {
			p, ds = lib.Parse(id, data)
		} else {
			p, ds = profile.Parse(data)
		}
	}
	for _, d := range ds {
		file := path
		if d.Profile != "" && lib != nil {
			if parent, err := lib.Path(d.Profile); err == nil {
				file = parent
			}
		}
		fmt.Fprintf(stderr, "%s:%s\n", file, d)
	}
	if ds.HasErrors() {
		return nil, nil, ds, errProfile
	}
	var cfg *profile.Config
	if len(settings) > 0 {
		cfg = settings[0]
	}
	if cfg != nil {
		p = cfg.WithDefaults(p)
	}
	// The function key is config.json's (ADR-0075); without settings it is the
	// default, start.
	prog, err = engine.Compile(p, engine.WithFunctionKey(cfg.FunctionKeyOrDefault()))
	if err != nil {
		fmt.Fprintf(stderr, "padmated: %s: cannot compile the profile: %v\n", path, err) // not reached for a parsed profile
		return nil, nil, ds, err
	}
	return p, prog, ds, nil
}

// problems returns the number of errors of a profile that parseProfile
// failed with err, and the first, as "line:col: message".
func problems(ds profile.Diagnostics, err error) (n int, first string) {
	for _, d := range ds {
		if d.Severity == profile.SeverityError {
			if n == 0 {
				first = fmt.Sprintf("%d:%d: %s", d.Line, d.Col, d.Message)
			}
			n++
		}
	}
	if n == 0 { // Compile failed
		return 1, err.Error()
	}
	return n, first
}

// readInput reads the fake input at path. It must have an event.
func readInput(path string) ([]input.Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New(profile.ReadError(path, err))
	}
	evs, err := input.ReadEvents(bytes.NewReader(data))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", path, err)
	case len(evs) == 0:
		return nil, fmt.Errorf("%s: no events", path)
	}
	return evs, nil
}

// device is a device of the fake input, or one a slot was given in live
// mode.
type device struct {
	id   input.DeviceID
	name string // from its added event; "" when none
}

// String returns `device 1 "name"`, or `device 1` without a name.
func (d device) String() string {
	if d.name == "" {
		return "device " + strconv.FormatUint(uint64(d.id), 10)
	}
	return fmt.Sprintf("device %d %q", d.id, d.name)
}

// selectDevice picks the device padmated replays from evs, which is not
// empty: the first with a button or axis event, or else the first. It
// returns that device, its events with the times rebased to its first
// event, and, when evs has other devices, a note naming them and the
// number of events skipped.
func selectDevice(evs []input.Event) (dev device, own []input.Event, note string) {
	var (
		ids    []input.DeviceID // in the order they appear
		n      = map[input.DeviceID]int{}
		names  = map[input.DeviceID]string{}
		active input.DeviceID // 0 is none: ReadEvents requires 1 or more
	)
	for _, e := range evs {
		if n[e.Device] == 0 {
			ids = append(ids, e.Device)
		}
		n[e.Device]++
		if e.Kind == input.DeviceAdded && e.Info != nil && names[e.Device] == "" {
			names[e.Device] = e.Info.Name
		}
		if active == 0 && (e.Kind == input.ButtonDown || e.Kind == input.ButtonUp || e.Kind == input.AxisMotion) {
			active = e.Device
		}
	}
	why := "the first with button or axis events"
	if active == 0 {
		active, why = ids[0], "the first, as none has button or axis events"
	}
	dev = device{id: active, name: names[active]}
	var skipped []string
	for _, id := range ids {
		if id != active {
			skipped = append(skipped, count(n[id], "event")+" of "+device{id: id, name: names[id]}.String())
		}
	}
	if len(skipped) > 0 {
		note = fmt.Sprintf("replaying device %d only, %s; skipped %s", active, why, strings.Join(skipped, ", "))
	}
	// ReadEvents checked that the times of a device do not decrease.
	for _, e := range evs {
		if e.Device == active {
			own = append(own, e)
		}
	}
	t0 := own[0].Time
	for i := range own {
		own[i].Time -= t0
	}
	return dev, own, note
}

// count returns "1 event" or "n events".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
