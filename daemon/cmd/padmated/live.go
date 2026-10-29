package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/instance"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// liveMaxMs is the longest time the time column of live mode fits: 24 h,
// in ms. A later time widens its line only.
const liveMaxMs = uint64(24 * time.Hour / time.Millisecond)

// emergencyEvent is the named event that padmatectl release-all signals
// (DMN-20, ADR-0028). The tests' TestMain sets a name of their own, so that
// no test takes a release-all meant for a padmated that runs.
var emergencyEvent = emergency.Name

// listenEmergency is emergency.Listen; TestMain wraps it to catch a test
// that listens on the real event.
var listenEmergency = emergency.Listen

// watchStopWait is how long the exit path waits for the watcher to stop,
// by default: a stat or a read of the profile that hangs, on a network
// share, must not hold the keys (CLAUDE.md invariant 1).
const watchStopWait = time.Second

// instanceName is empty in production (the current user and session).
// TestMain gives it an isolated name; acquireInstance guards that seam.
var instanceName string
var acquireInstance = instance.Acquire

// live runs live mode (DMN-02) until ctx is done, the session ends or the
// SDL input or the output stops, and returns the exit code. When it does
// not start it returns exitStartup, after writing why to --startup-report
// (UI-45), also when starting panics: startLive has released what it
// started, and the GUI build has no stderr for the panic.
func live(ctx context.Context, opts options, stdout, stderr io.Writer) (code int) {
	started := false
	defer func() {
		if started {
			return
		}
		if v := recover(); v != nil {
			fmt.Fprintf(stderr, "padmated: panic while starting: %v\n%s", v, debug.Stack())
			code = startupFailed(opts, stderr, startupPanic(v))
		}
	}()
	lease, err := acquireInstance(instanceName)
	if err != nil {
		if errors.Is(err, instance.ErrAlreadyRunning) {
			forwardErr := forwardInstance(ctx, opts)
			if forwardErr == nil {
				fmt.Fprintln(stdout, "padmated: request forwarded to the running instance")
				return 0
			}
			fmt.Fprintln(stderr, "padmated:", err, "; forwarding failed:", forwardErr)
			return startupFailed(opts, stderr, forwardFailure(forwardErr))
		}
		fmt.Fprintln(stderr, "padmated:", err)
		return startupFailed(opts, stderr, &startupError{kind: "instance", err: err})
	}
	// Registered first, so the lease outlives logs, SDL and every release.
	defer func() {
		if err := lease.Close(); err != nil {
			fmt.Fprintln(stderr, "padmated: closing the instance lease:", err)
			if code == 0 {
				code = 1
			}
		}
	}()
	if opts.library == nil && (opts.profileID != "" || opts.dataDir != "") {
		if err := resolveLibrary(&opts); err != nil {
			fmt.Fprintln(stderr, "padmated:", err)
			return startupFailed(opts, stderr, err)
		}
	}
	p, prog, sum, err := loadStartupProfile(opts, stderr)
	if err != nil {
		return startupFailed(opts, stderr, err)
	}
	log, closeLog, err := openStartupLog(opts, stderr)
	if err != nil {
		return startupFailed(opts, stderr, err)
	}
	// The exit path closes the log itself, before the session watcher; the
	// defer closes it when startLive fails before there is one.
	closed := false
	closeOnce := func(code *int) {
		if !closed {
			closed = true
			closeLog(code)
		}
	}
	defer closeOnce(&code)
	endpoint, err := listenIPC(ipcPipe, ipcDir)
	if err != nil {
		fmt.Fprintln(stderr, "padmated: IPC:", err)
		return startupFailed(opts, stderr, &startupError{kind: "ipc", err: err})
	}
	defer endpoint.Close()
	heldFile, heldFileErr := heldFilePath()
	r, err := startLive(ctx, liveConfig{opts: opts, profile: p, prog: prog, sum: sum, log: log, closeLog: closeOnce,
		heldFile: heldFile, heldFileErr: heldFileErr, emergencyName: emergencyEvent,
		stdout: stdout, stderr: stderr, endpoint: endpoint})
	if err != nil {
		return startupFailed(opts, stderr, err)
	}
	started = true
	if opts.tray && opts.ui != nil {
		return opts.ui(r)
	}
	return r.wait()
}

// liveConfig is what live mode runs with.
type liveConfig struct {
	// Test seams do not accept process identities through production flags.
	verifyUI      func(uint32, uint32) (string, error)
	ownForeground func(uint32, string) bool
	ownInstance   func(uint32) (uint64, error)
	ownAlive      func(uint32, string, uint64) bool
	ownBarrier    func(func() error) error
	// endpoint is acquired before opening input. Unit tests leave it nil or
	// supply a pipe and discovery directory of their own.
	endpoint       *ipc.Endpoint
	opts           options
	profile        *profile.Profile
	prog           *engine.Program
	sum            [32]byte // sha256 of the bytes profile was loaded from
	log            *slog.Logger
	stdout, stderr io.Writer
	// closeLog closes log. The exit path calls it after the last line,
	// "padmated stopped", which comes after the session watcher has closed,
	// so that what the watcher logs on its way out is kept; when the
	// session ends, the watcher closes after it instead, as its Close may
	// let the session end, and the process with it. It may set the exit
	// code to 1. nil leaves the log open.
	closeLog func(code *int)
	// heldFile is the path of the held-key log of --inject (OUT-14,
	// ADR-0027), which T-output sweeps at the start; empty keeps none. live
	// sets it to %LOCALAPPDATA%\padmate\run\held.bin. The dry run never
	// uses it. Most tests leave it empty, so that they never touch the
	// user's log; TestLatencyLive and the held-key tests put it in a
	// TempDir.
	heldFile string
	// heldFileErr is why live found no path for heldFile: with --inject,
	// startLive then reports the log off, as when T-output cannot open it.
	heldFileErr error
	// keyDown is the LoopOptions.KeyDown of the sweep; nil means
	// output.AsyncKeyDown. The tests that set heldFile set it too.
	keyDown func(output.KeyEvent) bool
	// heldIdentity is the LoopOptions.Identity of the held-key log; nil
	// means output.SystemHeldIdentity. The held-key tests set one of their
	// own.
	heldIdentity func() (output.HeldIdentity, error)
	// emergencyName is the named event of padmatectl release-all (DMN-20);
	// empty means emergencyEvent. testConfig gives each test a name of its
	// own.
	emergencyName string
	// watch configures the watcher of --watch; startLive sets its Logger.
	// The zero value is watch's defaults; the tests poll faster.
	watch watch.Options
	// watchStop is how long the exit path waits for the watcher to stop;
	// zero means watchStopWait.
	watchStop time.Duration
	// exitReadyWait is how long an exit waits for the UIs to save
	// (DMN-22); zero means exitReadyWait.
	exitReadyWait time.Duration
	// open opens SDL; nil means input.Open. The tests set the library, or
	// return a fake.
	open func(input.Options) (eventSource, error)
	// newInject returns the Sender of --inject; nil means newInjectSender.
	newInject func() (output.Sender, error)
	// slots is how many gamepads are mapped at once; 0 means
	// supervisor.MaxSlots. The tests of the line set 1.
	slots int
	// onEvent, when set, receives every event a slot handles, before the
	// printer does (supervisor.Config.OnEvent). Only tests set it: the
	// latency benchmark stamps t_slot there (ENG-11). It runs on the slot's
	// goroutine and must not block or wake anything.
	onEvent func(slot uint8, e input.Event)
	// processes is a read-only diagnostic source; nil uses the OS snapshot.
	processes func() ([]string, error)
	// session starts the watcher of the session's events (DMN-09); nil
	// means winsession.Start. The tests pass a fake, or the real one with
	// a Config of their own.
	session func(winsession.Config) (sessionWatcher, error)
	// startup returns the login startup entry of the tray and the startup
	// RPCs (UI-19); nil means systemStartup, which gives a development or
	// test build none. Only tests set it, with a fake entry.
	startup func(options) (startupSetting, error)
	// openWindow opens padmate's window for a double click on the tray
	// icon and returns its path; nil means systemWindowOpener, which opens
	// none in a development or test build. Only tests set it, with a fake.
	openWindow func() (string, error)
}

// sessionWatcher watches the session's events: a *winsession.Watcher.
type sessionWatcher interface {
	Close() error
	// Degraded is closed when the session thread ended by itself; Err then
	// says why.
	Degraded() <-chan struct{}
	Err() error
}

// stopCause is why padmated stops: the session watcher's OnStop cause
// ("session-end" or "close"), or the exit that the user chose in the tray
// ("tray") or the installer asked for ("installer").
type stopCause string

func (c stopCause) Error() string { return "padmated: stop: " + string(c) }

// stopNotes are what stderr says after "padmated: stopping" for each
// stopCause.
var stopNotes = map[string]string{
	"session-end": ": the session is ending",
	"close":       ": asked to close",
	"tray":        ": tray exit",
	"installer":   ": installer exit",
}

// eventSource is where live mode reads the gamepads from: an
// *input.Source.
type eventSource interface {
	Events() <-chan input.Event
	Dropped() uint64
	WriteHeader(w io.Writer, prefix, preset string) error
	// Err says why Events closed when Close did not close it.
	Err() error
	Close()
	// SetFeedback applies the ambient indication of a layer or the pause;
	// the effect queue calls it.
	SetFeedback(input.Feedback) error
	// Play plays a one-shot haptic on one gamepad (Event, Script and Alert
	// classes); the effect queue calls it.
	Play(input.Effect) error
	// SetLight claims the LED for a class, or releases the claim (a script's
	// pad.setLed); the effect queue calls it.
	SetLight(input.Light) error
	// Stop ends what a class has going on a gamepad (a script's rumble and LED
	// claims, when its runtime stops); the effect queue calls it.
	Stop(input.Stop) error
	// Identify gives the cues of a gamepad for the user to tell it from the
	// others (IN-13), and says which it can give. It does not go through the
	// effect queue: its caller wants the answer.
	Identify(input.DeviceID) (haptic.Cues, error)
}

// liveRun is live mode running: T-input (src), T-output (loop), the
// supervisor, whose Run runs on a goroutine of its own, T-session, the
// watcher of the session's events, and with --watch the watcher of the
// profile, on another goroutine.
type liveRun struct {
	configApplyMu    sync.Mutex // config commits and immediate output-setting application
	ownMu            sync.Mutex
	ownWindows       atomic.Pointer[[]ownUI]
	ownSend          atomic.Bool
	ownMode          atomic.Bool
	ownEpoch         atomic.Uint64
	ownWake          chan struct{}
	ownDone          chan struct{}
	conflicts        atomic.Pointer[[]rpcConflict]
	events           *ipc.Hub
	ipcStop          context.CancelFunc
	ipcDone          chan struct{}
	ipcLogs          *ipc.LogHistory
	ipcClientID      atomic.Uint64
	cfg              liveConfig
	log              *slog.Logger
	profileMu        sync.Mutex // selection, hot reload and cfg.profile/sum changes
	profileTarget    atomic.Pointer[liveProfile]
	profileWatchWake chan struct{}
	// macroWatchWake tells watchMacros that the active profile changed,
	// perhaps to another folder.
	macroWatchWake  chan struct{}
	foreground      atomic.Pointer[winfocus.App]
	focusWake       chan struct{}
	focusDone       chan struct{}
	focusStop       context.CancelFunc
	profileRequests chan profileRequest
	notices         *tray.Notifications
	diagnosticsStop context.CancelFunc
	diagnosticsDone chan struct{}
	// decisions publishes the engine's decisions and the unrouted button
	// edges as engine.activity events, on a goroutine of its own
	// (decision_feed.go, ADR-0071); decisionsStop ends it and decisionsDone
	// is closed once it has returned.
	decisions     *decisionFeed
	decisionsStop context.CancelFunc
	decisionsDone chan struct{}
	// end is done when padmated is to stop: when the context of startLive
	// is done, or when the session watcher's OnStop calls endStop, with a
	// stopCause.
	end       context.Context
	endStop   context.CancelCauseFunc
	src       eventSource
	pr        *printer // the Sender and the hooks of --dry-run; nil with --inject
	loop      *output.Loop
	sup       *supervisor.Supervisor
	scriptDef *scriptDefinition
	// macroChecks keeps what macros.list found of each macro (SCR-50).
	macroChecks macroChecks
	effects     *effectQueue
	// battery is the low-battery tracker (IN-12), fed by the supervisor's
	// OnPower.
	battery *batteryTracker
	// latencyStats are the latency statistics of T-output, per DeviceKey
	// (DMN-15, ipc_latency.go).
	latencyStats *latency.Recorder
	// cancel stops Run; ran is closed once Run has returned, after runErr
	// is set.
	cancel      context.CancelFunc
	ran         chan struct{}
	runErr      error
	persistStop chan struct{}
	persistDone chan struct{}
	persistErr  error
	// stopWatching stops the watcher's Run, and watched is closed once it
	// has returned; both are nil without --watch. stopWatch sets
	// stopWatching to nil.
	stopWatching context.CancelFunc
	watched      chan struct{}
	// sum is the sha256 of the profile running. The watcher's goroutine
	// owns it, as the only caller of Reload.
	sum [32]byte
	// running is the bytes the profile running was installed or reloaded
	// from, nil for the one startLive started with; reapplyTimingLocked
	// compiles it again. It changes with sum, under profileMu.
	running []byte
	// sess is the session watcher; nil when it did not start. sessStop
	// stops watchSession, and sessWatched is closed once it has returned.
	sess        sessionWatcher
	sessStop    chan struct{}
	sessWatched chan struct{}
	// sessionEnding is set when T-session asks padmated to stop because
	// the session ends (OnStop("session-end")), also when another cause
	// began the exit path first, which end then keeps as its cause:
	// shutdown closes the session watcher last when it is set.
	sessionEnding atomic.Bool
	resume        *resumeRecovery
	// suspErrs counts the output errors while the system suspends the
	// mapping, which are logged at debug level only.
	suspErrs atomic.Int64
	// emergencyStop cancels the DMN-20 named-event watcher before T-output
	// shuts down, so no late release races its final ReleaseAll;
	// emergencyDone is closed once it has returned. Both are nil when the
	// event could not be created.
	emergencyStop context.CancelFunc
	emergencyDone chan struct{}
	// selection is the effective config: the fields of the last committed
	// config.json that apply at once over the startup-captured rest
	// (liveSettings). It is replaced, never changed, under configApplyMu;
	// read it through selectionConfig.
	selection atomic.Pointer[profile.Config]
	// selectionWake asks the foreground worker to re-evaluate the current
	// foreground after a config change, which is not a foreground switch.
	selectionWake chan struct{}
	// recent lists the latest foreground programs for apps.recent (PRO-16).
	recent recentApps
	// startup is the login startup entry, shared by the tray and the
	// startup RPCs (UI-19, ADR-0052).
	startup *startupControl
	// configNotice is the repair of config.json the UI has not shown yet
	// (PRO-29), nil when there is none: status.get reports it until
	// config.repairSeen.
	configNotice atomic.Pointer[rpcConfigRepair]
	// uiExit is the registered UIs a tray exit waits for (DMN-22).
	uiExit uiExits
	// raw keeps who asked for the raw joystick events and publishes them
	// (ipc_mapping.go, IN-17); mappingOutcome is what the last application of
	// the gamepad mappings found (IN-18).
	raw            rawFeed
	mappingOutcome atomic.Pointer[mappingOutcome]

	once sync.Once
	code int
}

// startLive starts live mode: it opens SDL, starts T-output with the
// Sender of the mode, starts the session watcher, runs the supervisor on
// a goroutine of its own, and then, with --watch, the watcher of the
// profile on another. It prints the headers, and its errors to stderr.
// When it fails, or panics, it takes the exit path for what it started.
// Otherwise wait returns the exit code once live mode has stopped.
//
// Run gets a context of its own, which wait cancels after printing "exit:
// release all": the slots' releases then follow that line.
func startLive(ctx context.Context, cfg liveConfig) (run *liveRun, err error) {
	opts, log := cfg.opts, cfg.log
	events := ipc.NewHub()
	log, history := ipc.ObserveLogs(log, events)
	cfg.log = log
	mode := "dry-run"
	if opts.inject {
		mode = "inject"
	}
	// Logged before SDL opens, which may fail: the count is the one
	// supervisor.New gets.
	slots := cfg.slots
	if slots == 0 {
		slots = supervisor.MaxSlots
	}
	log.Info("padmated starting", "version", version.String(), "profile", opts.profile, "mode", mode,
		"preset", opts.sdl.Preset, "slots", slots)
	open := cfg.open
	if opts.fakeInput == "ipc" {
		if !opts.dryRun || opts.inject {
			return nil, &startupError{kind: "usage", err: errors.New("fake IPC input requires dry-run")}
		}
		open = func(o input.Options) (eventSource, error) {
			caps, err := fakeCapsFromEnv()
			if err != nil {
				return nil, err
			}
			return newRPCInputSourceWith(o, caps), nil
		}
	} else if open == nil {
		open = func(o input.Options) (eventSource, error) { return input.Open(o) }
	}
	// T-input calls OnCombo when a gamepad has held its panic combo
	// (DMN-07), before it sends the ComboHeld that pauses the mapping, or
	// resumes it: Block lets go of every key at once, without waiting for
	// the router, and never blocks T-input. A combo held before T-output
	// has started finds nothing held; the router's pause, or resume, sets
	// the gate.
	//
	// T-input also watches the receivers (IN-30, ADR-0029): a Switch Pro
	// protocol gamepad behind a USB receiver whose heartbeat stops is
	// reported removed, so its slot releases every key. It logs to log.
	//
	// ComboFor reads the effective config at each pick, so that a changed
	// safety.panic applies at the next RepickCombos (UI-19); before r
	// exists it is the startup config.
	//
	// PowerEvents makes T-input send each gamepad's battery state as
	// DevicePower events, which the supervisor keeps for devices.list and
	// status.get (IN-12); the fake IPC input has none.
	var loopRef atomic.Pointer[output.Loop]
	var runRef atomic.Pointer[liveRun]
	settings := func() *profile.Config {
		if r := runRef.Load(); r != nil {
			return r.selectionConfig()
		}
		return opts.settings
	}
	src, err := open(input.Options{Hints: opts.hints, OnCombo: func() {
		if l := loopRef.Load(); l != nil {
			l.Block(true)
		}
	}, ComboFor: liveComboFor(settings), ReceiverTimeout: input.DefaultReceiverTimeout, PowerEvents: true, Log: log,
		Mappings: startupMappings(opts.settings)})
	if err != nil {
		fmt.Fprintln(cfg.stderr, "padmated:", err)
		log.Error("SDL did not start", "err", err)
		return nil, &startupError{kind: "sdl", err: err}
	}
	if opts.defaultCreated {
		log.Info("default profile created", "profile", profilelib.DefaultID)
	}
	if opts.defaultErr != nil {
		log.Warn("default profile not created; it runs without a file until one is saved", "profile", profilelib.DefaultID, "err", opts.defaultErr)
	}
	if opts.missingLastProfile != "" {
		log.Warn("last profile no longer in the library; starting with the default profile", "profile", opts.missingLastProfile)
	}
	logMacroMigration(log, opts.macroMigration, opts.macroMigrationErr)
	logConfigRepair(log, opts.configRepair)
	if opts.stateErr != nil {
		log.Warn("state.json had errors; starting without the saved state", "err", opts.stateErr, "reset", opts.stateReset)
	}
	if opts.stateResetErr != nil {
		log.Warn("state.json not reset", "err", opts.stateResetErr)
	}
	r := &liveRun{cfg: cfg, log: log, src: src, sum: cfg.sum,
		scriptDef: newProfileScriptDefinition(opts.profile, cfg.profile, opts.watch, log)}
	if opts.library != nil {
		r.scriptDef.useLibrary(opts.library.Dir())
	}
	r.events, r.ipcLogs = events, history
	r.startup = newStartupControl(opts, cfg.startup, log)
	r.ownWindows.Store(&[]ownUI{})
	r.ownWake = make(chan struct{}, 1)
	if opts.settings != nil {
		r.ownSend.Store(opts.settings.Output.SendToOwnWindow)
		r.selection.Store(opts.settings)
	}
	runRef.Store(r)
	if opts.library != nil {
		r.configNotice.Store(configNotice(opts.library.Dir(), opts.configRepair))
	}
	r.profileRequests = make(chan profileRequest, 16)
	r.notices = tray.NewNotifications(16)
	r.scriptDef.environment = new(script.Environment)
	r.scriptDef.environment.SetHUD(hudEvents{hub: r.events})
	if opts.library != nil {
		r.scriptDef.profileSwitch = r.requestProfile
	}
	r.refreshScriptProfiles()
	r.profileWatchWake = make(chan struct{}, 1)
	r.macroWatchWake = make(chan struct{}, 1)
	r.profileTarget.Store(&liveProfile{id: opts.profileID, path: opts.profile, sum: cfg.sum, gen: 1, parents: cfg.profile.Parents})
	r.logIgnoredFunctionKeyBindings(cfg.prog, opts.profileID)
	r.end, r.endStop = context.WithCancelCause(ctx)
	defer func() {
		if run == nil {
			r.shutdown(1) // also while a panic unwinds
		}
	}()
	// Like the other diagnostics on stderr, a failed write is ignored.
	_ = src.WriteHeader(cfg.stderr, "padmated: ", opts.sdl.Preset)

	clk := clock.Real()
	if opts.dryRun {
		r.pr = newPrinter(bufio.NewWriter(cfg.stdout), clk, opts.decisions, cfg.prog, liveMaxMs)
		r.pr.live = true
	}
	sender, err := chooseSender(opts.inject, r.pr, cfg.newInject)
	if err != nil {
		fmt.Fprintln(cfg.stderr, "padmated:", err)
		log.Error("no sender for --inject", "err", err)
		return nil, &startupError{kind: "output", err: err}
	}
	heldFile := ""
	if opts.inject {
		heldFile = cfg.heldFile
	}
	// Both modes resolve the keys against the foreground layout, so that
	// the dry run prints what --inject would press.
	resolver := output.NewResolver(output.SystemLayouts(), output.ResolverOptions{
		Warn: func(w output.ResolveWarning) {
			log.Warn("key sent as its US key", "spec", w.Spec, "hkl", w.HKL.String(), "reason", w.Reason)
		},
	})
	// Errors runs on the loop goroutine only, so one Limiter serves it.
	limit := logging.NewLimiter(clk, time.Second)
	arbiterOpts := arbiterTiming(cfg.prog, opts.settings)
	arbiterOpts.Resolve = resolver.Resolve
	arbiterOpts.Preview = r.previewOutput
	// T-output records the latency of every key it sends for an input edge
	// (DMN-15, ipc_latency.go).
	r.latencyStats = latency.NewRecorder()
	arbiterOpts.Latency = r.latencyStats.Record
	r.loop = output.StartLoop(clk, sender, output.LoopOptions{
		Guard:    r.guardOwnOutput,
		Arbiter:  arbiterOpts,
		Lock:     true,
		HeldFile: heldFile,
		KeyDown:  cfg.keyDown,
		Identity: cfg.heldIdentity,
		Errors: func(err error) {
			// While the system suspends the mapping, the lock screen or the
			// secure desktop may refuse the release, which the loop resends:
			// counted, and logged at debug level only. Errors may come before
			// loopRef is set.
			if l := loopRef.Load(); l != nil && l.Blocking()&output.BlockSystem != 0 {
				r.suspErrs.Add(1)
				log.Debug("output error while suspended", "err", err)
				return
			}
			if ok, n := limit.Allow(err.Error()); ok {
				log.Warn("output error", "err", err, "suppressed", n)
			}
		},
	})
	loopRef.Store(r.loop)
	sweep := r.loop.Swept()
	if opts.inject && heldFile == "" && cfg.heldFileErr != nil {
		sweep = output.SweepResult{Off: true, Err: cfg.heldFileErr}
	}
	r.reportSweep(sweep)
	r.effects = startEffectQueue(effectSender{feedback: src.SetFeedback, play: src.Play, light: src.SetLight, stop: src.Stop}, log)
	// The rumble and LED calls of scripts, and the buzz of one that fails, go
	// through it (SCR-23). Both are set before the first slot runs.
	r.scriptDef.environment.SetEffects(scriptEffects{run: r})
	r.scriptDef.errorCue = r.scriptErrorCue
	r.battery = newBatteryTracker()
	scfg := supervisor.Config{
		OnInput: r.publishInput,
		Clock:   clk, Events: src.Events(), Dropped: src.Dropped, Output: r.loop,
		Program: cfg.prog, Logger: log, Device: opts.device, Lock: true, Slots: slots,
		ScriptFactory: r.scriptDef.Factory, ScriptConfigured: r.scriptDef.Needed,
		ProfileKey:    opts.profileKey(),
		ProfileSwitch: func(_ uint8, gen uint32, id string) error { return r.requestProfile(gen, id) },
	}
	if opts.librarySelection() {
		scfg.LayerMemory = opts.memory
	}
	if opts.settings != nil {
		scfg.IgnoreDevice = func(key input.DeviceKey) bool { return opts.settings.Devices[key.String()].Ignored }
	}
	// The stick calibration of each controller (IN-14) applies at once: a
	// slot asks for it through the effective config.
	scfg.Calibration = r.deviceCalibration
	// The feedback switches (UI-27, ADR-0067) are global and apply at once, as
	// the stick calibration does: the engines read the effective config.
	scfg.Switches = r.feedbackSwitches
	pr := r.pr
	if pr != nil {
		scfg.Trace, scfg.OnEvent, scfg.OnReload, scfg.OnPause = pr.decision, pr.slotEvent, pr.reload, pr.pause
		scfg.OnLayerChange = pr.layerChange
	}
	// The decisions and the unrouted button edges go to the IPC clients
	// through a bounded feed with a publisher goroutine, so that no slot or
	// router goroutine encodes JSON (ADR-0071).
	r.decisions = newDecisionFeed(r.events, clk, log, 0)
	printDecision, printEvent := scfg.Trace, scfg.OnEvent
	scfg.Trace = func(index uint8, decision engine.Decision) {
		if printDecision != nil {
			printDecision(index, decision)
		}
		r.decisions.decision(index, decision)
	}
	scfg.OnEvent = func(index uint8, e input.Event) {
		r.decisions.event(index, e)
		if printEvent != nil {
			printEvent(index, e)
		}
	}
	scfg.OnUnrouted = r.decisions.unrouted
	printLayer := scfg.OnLayerChange
	printFeedback := func(slot uint8, f engine.Feedback) {
		if pr != nil {
			pr.feedback(slot, f)
		}
	}
	scfg.OnLayerChange = func(slot uint8, state engine.LayerState) {
		if printLayer != nil {
			printLayer(slot, state)
		}
	}
	scfg.OnFeedbackDevice = func(slot uint8, id input.DeviceID, f engine.Feedback) {
		if printFeedback != nil {
			printFeedback(slot, f)
		}
		if id == 0 {
			return
		}
		fb := input.Feedback{
			Device: id, Color: f.Color, Pulse: f.Pulse, Pulses: f.Pulses,
			PulseMs: f.PulseMs, GapMs: f.GapMs, Strength: f.Strength,
			Paused: f.Paused,
		}
		// The player lights show the mode of the layer while the profile's
		// lights are on (HUD-08); with them off the mode is 0, which gives the
		// index back.
		if f.LED {
			fb.PlayerMode = f.Mode
		}
		r.effects.post(fb)
	}
	// The haptics of the bindings and the knob ticks (MAP-25, HUD-10) are
	// one-shot effects of the Event class: they wait in the queue's bounded
	// line, and the Mixer decides what they may interrupt. Posting never
	// blocks the slot.
	scfg.OnHapticDevice = func(slot uint8, id input.DeviceID, h engine.Haptic) {
		if pr != nil {
			pr.haptic(slot, h)
		}
		if id == 0 {
			return
		}
		r.effects.postEffect(input.Effect{
			Device: id, Class: haptic.Event, Preset: h.Preset,
			Strength: h.Strength, ShortMs: h.ShortMs, GapMs: h.GapMs,
		})
	}
	// The power of each controller (IN-12) goes to the low-battery tracker, on
	// the router's goroutine; it alerts through the effect queue, the tray's
	// notices and, with the state event's batteries, the overlay.
	scfg.OnPower = r.onPower
	scfg.OnSuspend = func(was, now winsession.Reason) {
		if pr != nil {
			pr.suspend(was, now)
		}
		if now == 0 {
			r.flushSuspendedErrors()
		}
	}
	if f := cfg.onEvent; f != nil {
		next := scfg.OnEvent
		scfg.OnEvent = func(slot uint8, e input.Event) {
			f(slot, e)
			if next != nil {
				next(slot, e)
			}
		}
	}
	if r.sup, err = supervisor.New(scfg); err != nil {
		fmt.Fprintln(cfg.stderr, "padmated:", err) // not reached: the Config is complete
		return nil, err
	}
	// Before the first line: with several slots every row names its slot.
	if slots > 1 && r.pr != nil {
		r.pr.showSlots()
	}
	r.header(slots)
	r.startPersistence()
	r.startFocus()
	r.startSession()
	if opts.inject {
		r.startEmergency()
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel, r.ran = cancel, make(chan struct{})
	go r.run(runCtx)
	r.startOwnOutput()
	r.startDiagnostics()
	r.startDecisionFeed()
	r.startRawFeed()
	if opts.watch && (opts.profile != "" || opts.librarySelection()) {
		r.watchLibrary()
	}
	r.startIPC()
	return r, nil
}

// reportSweep tells stderr and the log what the startup sweep of the
// held-key log did (OUT-14), which T-output ran before its first command.
// It never stops padmated: the keys the sweep could not release stay in
// the log, for the pause, release-all, Stop and the next start; a corrupt
// log, or one of another boot or logon session, has been reset, and what
// it had is not released.
func (r *liveRun) reportSweep(res output.SweepResult) {
	switch {
	case res.Off:
		fmt.Fprintf(r.cfg.stderr, "padmated: held-key log off: %v; keys held when padmated is killed are not released at the next start\n", res.Err)
		r.log.Warn("held-key log off", "err", res.Err)
	case res.Reset:
		fmt.Fprintf(r.cfg.stderr, "padmated: held-key log reset: %v; keys the last run held, if any, are not released\n", res.Err)
		r.log.Warn("held-key log reset", "err", res.Err)
	case res.Err != nil:
		fmt.Fprintf(r.cfg.stderr, "padmated: releasing the keys the last run held: %v; the keys not released stay in the held-key log\n", res.Err)
		r.log.Warn("startup held-key cleanup failed", "err", res.Err)
	}
	// A log of another boot or logon session: its keys were let go of when
	// that session ended, and a key down now is someone else's.
	if res.Stale && len(res.StaleKeys) > 0 {
		keys := keyNames(res.StaleKeys)
		fmt.Fprintf(r.cfg.stderr, "padmated: held-key log reset: it is from another boot or logon session (%v); its keys are not released: %s\n",
			res.StaleFrom, strings.Join(keys, ", "))
		r.log.Warn("stale held-key log reset", "from", res.StaleFrom.String(), "keys", strings.Join(keys, ","))
	} else if res.Stale {
		r.log.Info("stale held-key log reset", "from", res.StaleFrom.String())
	}
	if len(res.Released) == 0 {
		return
	}
	names := keyNames(res.Released)
	fmt.Fprintf(r.cfg.stderr, "padmated: released keys the last run held: %s\n", strings.Join(names, ", "))
	r.log.Info("held keys released", "keys", strings.Join(names, ","))
}

// keyNames names the keys of the releases evs, as the trace does, without
// the sign.
func keyNames(evs []output.KeyEvent) []string {
	names := make([]string, len(evs))
	for i, e := range evs {
		names[i] = edgeName(e)[1:]
	}
	return names
}

// startEmergency listens for padmatectl release-all (DMN-20, ADR-0028):
// it creates the named event on startLive's goroutine, so that a
// release-all right after startLive returns reaches it, and waits on it on
// a goroutine of its own. Each signal releases every key through
// Supervisor.ReleaseAll. When the event cannot be created padmated maps
// without it: on other systems quietly, on Windows with a warning.
// startLive calls it with --inject only: the event is auto-reset, so a
// dry run listening on it would take the release-all meant for an
// --inject padmated, and it holds no key to release.
func (r *liveRun) startEmergency() {
	name := r.cfg.emergencyName
	if name == "" {
		name = emergencyEvent
	}
	l, err := listenEmergency(name)
	switch {
	case errors.Is(err, emergency.ErrUnsupported):
		r.log.Debug("emergency release unsupported")
		return
	case err != nil:
		fmt.Fprintf(r.cfg.stderr, "padmated: padmatectl release-all off: %v\n", err)
		r.log.Warn("emergency release off", "err", err)
		return
	}
	ctx, cancel := context.WithCancel(r.end)
	r.emergencyStop = cancel
	r.emergencyDone = make(chan struct{})
	go func() {
		defer close(r.emergencyDone)
		err := l.Serve(ctx, func() {
			r.log.Info("emergency release requested")
			if err := r.sup.ReleaseAll(); err != nil && !errors.Is(err, output.ErrLoopClosed) {
				r.log.Warn("emergency release failed", "err", err)
			}
		})
		if err != nil {
			r.log.Warn("emergency release watcher stopped", "err", err)
		}
	}()
}

func (r *liveRun) stopEmergency() {
	if r.emergencyStop == nil {
		return
	}
	r.emergencyStop()
	<-r.emergencyDone
	r.emergencyStop = nil
}

// startSession starts the watcher of the session's events (DMN-09). Its
// changes suspend the mapping: Supervisor.Suspend lets go of every key at
// once and never waits. The end of the session and WM_CLOSE stop
// padmated as a stop signal does. Its Barrier, which the system's sleep
// and the end of the session wait for, returns once T-output has run the
// release. A session locked already suspends the mapping before Run
// starts. When the watcher cannot start, padmated maps without it: on
// other systems quietly, on Windows with a warning.
func (r *liveRun) startSession() {
	start := r.cfg.session
	if start == nil {
		start = func(c winsession.Config) (sessionWatcher, error) {
			w, err := winsession.Start(c)
			if err != nil {
				return nil, err // not a typed nil
			}
			return w, nil
		}
	}
	loop := r.loop
	r.startResume()
	sess, err := start(winsession.Config{
		Logger:   r.log,
		Focus:    &winfocus.Config{Logger: r.log, OnChange: r.onForeground},
		OnSignal: r.resume.signal,
		OnWake:   r.resume.wake,
		OnChange: r.resume.change,
		// sessionEnding is set before the cause, so that an exit path that
		// sees the cause sees it too.
		OnStop: func(cause string) {
			if cause == "session-end" {
				r.sessionEnding.Store(true)
			}
			r.endStop(stopCause(cause))
		},
		// A Do that starts after Suspend's BlockFor runs after its release.
		// winsession bounds the wait, on a goroutine of its own.
		Barrier: func(context.Context) error { return loop.Do(func(*output.Arbiter) error { return nil }) },
	})
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		r.log.Debug("session events unsupported")
		return
	case err != nil:
		fmt.Fprintf(r.cfg.stderr, "padmated: session events off: %v; locking, sleep and sign-out will not release keys\n", err)
		r.log.Warn("session events off", "err", err)
		return
	case sess == nil:
		return
	}
	r.sess, r.sessStop, r.sessWatched = sess, make(chan struct{}), make(chan struct{})
	go r.watchSession()
}

// watchSession tells stderr when the session watcher's thread ends by
// itself, until stopSession: from then on only polling suspends the
// mapping. The watcher logs it.
func (r *liveRun) watchSession() {
	defer close(r.sessWatched)
	select {
	case <-r.sess.Degraded():
		fmt.Fprintf(r.cfg.stderr, "padmated: session events stopped: %v; locking and the secure desktop are still polled "+
			"every %d s, sleep and sign-out will not release keys\n", r.sess.Err(), winsession.DefaultPoll/time.Second)
	case <-r.sessStop:
	}
}

// stopSession stops watchSession and waits for it. It runs on the
// goroutine of shutdown only.
func (r *liveRun) stopSession() {
	if r.sessStop != nil {
		close(r.sessStop)
		<-r.sessWatched
		r.sessStop = nil
	}
}

// flushSuspendedErrors logs how many output errors came while the system
// suspended the mapping, if any: when the suspension ends, on the
// router's goroutine, and on the exit path.
func (r *liveRun) flushSuspendedErrors() {
	if n := r.suspErrs.Swap(0); n > 0 {
		r.log.Info("output errors while suspended", "count", n)
	}
}

// reloadProfile loads c, new content of the profile that the watcher saw,
// and switches every slot to it with Supervisor.Reload. It prints the
// diagnostics as at the start. Content with errors, or the content that
// runs, changes nothing; so does a change seen once ctx, the watcher's, is
// done, as live mode stops. It runs on the watcher's goroutine.
func (r *liveRun) reloadProfile(ctx context.Context, c watch.Change) {
	r.reloadProfileFor(ctx, nil, c)
}

func (r *liveRun) reloadProfileFor(ctx context.Context, expected *liveProfile, c watch.Change) {
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	_ = r.reloadProfileLocked(ctx, expected, c)
}

// reloadDependentLocked reloads the running profile when a parent of its
// chain (PRO-19) was saved: the profile's own file is read again and merged
// with the parents as they are now. It changes nothing if the chain, with
// the sums of its files, is what runs. When the file cannot be read, a
// program may hold it open for a moment, the text that the running profile
// was loaded from is merged instead, if the daemon has it (running);
// otherwise the error is an *unreadableProfileError. The watcher of the
// parent has taken the parent's new text as seen, so a reload that is given
// up here would not come again. The caller holds profileMu.
func (r *liveRun) reloadDependentLocked(ctx context.Context, expected *liveProfile) error {
	target := r.targetLocked()
	if ctx.Err() != nil || expected != nil && expected != target || r.cfg.opts.library == nil || target.id == "" {
		return ctx.Err()
	}
	data, err := r.cfg.opts.library.Read(target.id)
	if err != nil {
		if r.running == nil {
			r.log.Warn("profile not reloaded: cannot read it", "profile", target.id, "err", err)
			return &unreadableProfileError{err}
		}
		r.log.Debug("profile file unreadable; merging the text that runs with its parents", "profile", target.id, "err", err)
		data = r.running
	}
	return r.reloadProfileLocked(ctx, expected, watch.Change{Data: data, Sum: sha256.Sum256(data)})
}

// unreadableProfileError is why reloadDependentLocked had no text of the
// running profile to merge: its file cannot be read, and the daemon has
// not kept what it was loaded from.
type unreadableProfileError struct{ err error }

func (e *unreadableProfileError) Error() string { return e.err.Error() }
func (e *unreadableProfileError) Unwrap() error { return e.err }

// How often, and how long apart, reloadDependentFor tries to read the file
// of a running profile that it could not read.
const (
	dependentReadAttempts = 5
	dependentReadDelay    = 200 * time.Millisecond
)

// reloadDependentFor is reloadDependentLocked on the watcher's goroutine,
// for a change of a parent's file. An unreadable file of the profile is
// tried again a few times, without holding profileMu in between, and ctx,
// the watcher's, ends it.
func (r *liveRun) reloadDependentFor(ctx context.Context, expected *liveProfile) {
	for attempt := 1; ; attempt++ {
		r.profileMu.Lock()
		err := r.reloadDependentLocked(ctx, expected)
		r.profileMu.Unlock()
		var unreadable *unreadableProfileError
		if !errors.As(err, &unreadable) || attempt >= dependentReadAttempts {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(dependentReadDelay):
		}
	}
}

// reloadProfileLocked also serves IPC writes, including --watch=false. The
// caller holds profileMu across saving and applying the same revision.
func (r *liveRun) reloadProfileLocked(ctx context.Context, expected *liveProfile, c watch.Change) error {
	target := r.targetLocked()
	if ctx.Err() != nil || expected != nil && expected != target {
		return ctx.Err()
	}
	path := target.path
	if c.Sum == r.sum && len(target.parents) == 0 {
		// A save that reverts an invalid one.
		r.log.Debug("profile unchanged", "file", path)
		return nil
	}
	p, prog, ds, err := parseProfileIn(r.cfg.opts.library, target.id, path, c.Data, r.cfg.stderr, r.selectionConfig())
	if err != nil {
		n, first := problems(ds, err)
		r.log.Error("profile not reloaded", "file", path, "errors", n, "first", first)
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.Sum == r.sum && slices.Equal(p.Parents, target.parents) {
		// The profile's own text is what runs, and so are its parents (PRO-19):
		// a touch of a parent's file, or a save that reverts one.
		r.log.Debug("profile unchanged", "file", path)
		return nil
	}
	if paramsOnlyChange(r.cfg.profile, p) {
		if err := r.sup.UpdateScriptParams(context.Background(), p.Script.Params, func() {
			r.scriptDef.useProfile(p)
			r.cfg.profile = p
		}); err != nil {
			r.log.Error("script params not updated", "file", path, "err", err)
			return err
		}
		r.sum, r.running = c.Sum, c.Data
		r.publishProfile(&liveProfile{id: target.id, path: path, sum: c.Sum, gen: target.gen})
		r.log.Info("script params updated", "file", path, "warnings", len(ds))
		return nil
	}
	// Not ctx: once the watcher is stopping, stop waits for the reload
	// under way, for watchStop at most, so that its releases come before
	// the exit line. It ends, as Run does once it has ended.
	oldProfile := r.cfg.profile
	r.cfg.profile = p
	r.scriptDef.useProfile(p)
	gen, err := r.sup.Reload(context.Background(), prog)
	if err != nil {
		r.cfg.profile = oldProfile
		r.scriptDef.useProfile(oldProfile)
		// ErrStopped, or Output has ended: Run ends too, and the exit path
		// releases every key.
		r.log.Debug("profile reload stopped: padmated is stopping", "file", path, "err", err)
		return err
	}
	timing := arbiterTiming(prog, nil) // the tap timing; the event gap is config.json's
	if err := r.loop.Do(func(a *output.Arbiter) error { return a.SetTiming(timing.MinPress, timing.MinGap) }); err != nil {
		r.log.Debug("profile timing update stopped", "file", path, "err", err)
	}
	r.sum, r.running = c.Sum, c.Data
	r.cfg.prog = prog
	r.publishProfile(&liveProfile{id: target.id, path: path, sum: c.Sum, gen: gen})
	r.log.Info("profile reloaded", "file", path, "gen", gen, "warnings", len(ds))
	r.logIgnoredFunctionKeyBindings(prog, target.id)
	return nil
}

// chooseSender returns the Sender of the mode: the printer, unless
// inject, when newInject (nil: newInjectSender) constructs it. Nothing
// else in padmated constructs a Sender that injects (CLAUDE.md invariant
// 6).
func chooseSender(inject bool, pr *printer, newInject func() (output.Sender, error)) (output.Sender, error) {
	if !inject {
		return pr, nil
	}
	if newInject == nil {
		newInject = newInjectSender
	}
	return newInject()
}

// header prints the mode, the profile and the gamepads mapped on the n
// slots: to stdout, or with --decisions to stderr, as stdout is only JSON
// lines then.
func (r *liveRun) header(n int) {
	opts := r.cfg.opts
	var b strings.Builder
	if opts.inject {
		b.WriteString("padmated: injecting: the keys go to the foreground window\n")
	} else {
		b.WriteString("padmated: dry run, nothing is injected\n")
	}
	fmt.Fprintf(&b, "profile:    %s, %q, %s\n", opts.profile, r.cfg.profile.Name, count(len(r.cfg.prog.Bindings()), "binding"))
	b.WriteString("gamepads:   ")
	if opts.device != "" {
		fmt.Fprintf(&b, "of those whose name or device key contains %q, ", opts.device)
	}
	if n == 1 {
		b.WriteString("one is mapped at a time; the first to press a button keeps it; Ctrl+C stops\n")
	} else {
		fmt.Fprintf(&b, "up to %d are mapped at once, each on its own slot; the others wait; Ctrl+C stops\n", n)
	}
	switch {
	case opts.decisions:
		_, _ = io.WriteString(r.cfg.stderr, b.String()) // to stderr: nowhere to report an error
	case r.pr != nil:
		r.pr.text(b.String() + "\n")
		r.pr.columnHeader()
	default:
		// With --inject nothing else goes to stdout, and a failed write
		// stops nothing.
		_, _ = io.WriteString(r.cfg.stdout, b.String())
	}
}

// run runs the supervisor. A panic of Run's goroutine, which runs the
// router, ends it like an error, and wait takes the exit path. The other
// goroutines see to their own: the slot loops and T-output recover their
// panics, a slot's runner ends Run with a *supervisor.PanicError, and
// T-input ends its Source.
func (r *liveRun) run(ctx context.Context) {
	defer close(r.ran)
	finished := false
	defer func() {
		if finished {
			return
		}
		// recover returns nil for runtime.Goexit.
		if v := recover(); v != nil {
			r.runErr = fmt.Errorf("the supervisor panicked: %v", v)
		} else {
			r.runErr = errors.New("the supervisor goroutine exited")
		}
		r.log.Error("supervisor panicked", "err", r.runErr, "stack", string(debug.Stack()))
	}()
	r.runErr = r.sup.Run(ctx)
	finished = true
}

// wait returns the exit code once live mode has stopped: 0 when the
// context of startLive is done or the session watcher stopped it, 1 when
// the SDL input or the output stopped by itself. It takes the exit path
// first. It may be called more than once.
func (r *liveRun) wait() int {
	r.once.Do(func() { r.code = r.stop() })
	return r.code
}

// stop waits for the end of live mode and takes the exit path (CLAUDE.md
// invariant 1), deferred so that it also runs while a panic unwinds.
func (r *liveRun) stop() (code int) {
	defer func() { code = r.shutdown(code) }()
	stopping := false
	select {
	case <-r.end.Done():
		stopping = true
		cause := "signal"
		if c, ok := errors.AsType[stopCause](context.Cause(r.end)); ok {
			cause = string(c)
		}
		fmt.Fprintln(r.cfg.stderr, "padmated: stopping"+stopNotes[cause])
		r.log.Info("padmated stopping", "cause", cause)
		r.stopWatch() // a reload under way ends before the exit line
		if r.pr != nil {
			r.pr.exit()
		}
		r.cancel()
		<-r.ran
	case <-r.ran:
		// The slot loops have released their keys already.
	}
	switch {
	case r.runErr != nil:
		fmt.Fprintln(r.cfg.stderr, "padmated:", r.runErr)
		// The runner that panicked logged nothing.
		if pe, ok := errors.AsType[*supervisor.PanicError](r.runErr); ok {
			r.log.Error("slot runner panicked", "err", pe, "stack", string(pe.Stack))
		}
		return 1
	case !stopping:
		// Run ends by itself without an error only when Events closes: after
		// a panic on T-input, Err says so.
		if err := r.src.Err(); err != nil {
			fmt.Fprintln(r.cfg.stderr, "padmated: the SDL input stopped:", err)
			attrs := []any{"err", err}
			if pe, ok := errors.AsType[*input.PanicError](err); ok {
				attrs = append(attrs, "stack", string(pe.Stack))
			}
			r.log.Error("the SDL input stopped", attrs...)
		} else {
			fmt.Fprintln(r.cfg.stderr, "padmated: the SDL input stopped")
			r.log.Error("the SDL input stopped")
		}
		return 1
	}
	return 0
}

// shutdown is the exit path, in this order: the watcher stops, so no
// reload is under way; Run returns, so the slot loops have released their
// keys through T-output; T-output stops, releasing what is left; the effect queue
// and SDL close with one bounded wait; the printer is flushed; the session
// watcher closes, and its final logs are kept; "padmated stopped" is logged and the log
// closed. When the session ends (OnStop("session-end"), also after
// another cause began the exit path), the session watcher closes last
// instead: its Close lets the WM_ENDSESSION handler return to the system,
// which may then end the process at any time, so the last line and the
// log come first, and what the watcher logs after them, its teardown too,
// is lost. A session end that comes after the choice, before the Close,
// still loses the last lines; the keys are released by then. It returns
// code, or 1 when a step fails.
func (r *liveRun) shutdown(code int) int {
	// Stop accepting requests immediately, but never wait for client cleanup
	// before the engine and output have released every key.
	if r.ipcStop != nil {
		r.ipcStop()
	}
	r.resume.stop()
	r.stopFocus()
	r.stopWatch()
	r.stopEmergency()
	if r.cancel != nil {
		r.cancel()
		<-r.ran
	}
	if r.loop != nil {
		if err := r.loop.Stop(); err != nil {
			code = 1
			// After a panic Stop returns the error that ended Run, which
			// stop printed and the supervisor logged.
			if !errors.Is(r.runErr, err) {
				fmt.Fprintln(r.cfg.stderr, "padmated: stopping the output:", err)
				r.log.Error("output stopped with an error", "err", err)
			}
		}
	}
	if r.ownDone != nil {
		<-r.ownDone
	}
	r.stopPersistence()
	r.stopDiagnostics()
	r.stopDecisionFeed()
	r.stopRawFeed()
	if r.ipcDone != nil {
		<-r.ipcDone
	}
	var reportedInput error
	if code != 0 && r.src != nil {
		reportedInput = r.src.Err()
	}
	if err := r.stopInput(reportedInput); err != nil {
		code = 1
		message := "input cleanup failed"
		if errors.Is(err, context.DeadlineExceeded) {
			message = "input cleanup did not stop"
		}
		fmt.Fprintln(r.cfg.stderr, "padmated: "+message+":", err)
		r.log.Error(message, "err", err)
	}
	if r.src != nil {
		if n := r.src.Dropped(); n > 0 {
			fmt.Fprintf(r.cfg.stderr, "padmated: %d input events dropped: the engine was too slow\n", n)
			r.log.Warn("input events dropped", "dropped", n)
		}
	}
	if r.pr != nil {
		if err := r.pr.flush(); err != nil {
			fmt.Fprintln(r.cfg.stderr, "padmated: writing the output:", err)
			code = 1
		}
	}
	r.stopSession()
	ending := r.sessionEnding.Load()
	if !ending {
		r.closeSession(true)
	}
	r.flushSuspendedErrors()
	r.log.Info("padmated stopped", "code", code)
	if r.cfg.closeLog != nil {
		r.cfg.closeLog(&code)
	}
	if ending {
		r.closeSession(false) // the log is closed: stderr only
	}
	r.endStop(nil)
	return code
}

// closeSession closes the session watcher, when it started, and reports a
// failure on stderr, and in the log when logged. After the thread ended by
// itself Close returns Err, which watchSession printed and the watcher
// logged. It runs on the goroutine of shutdown only, once.
func (r *liveRun) closeSession(logged bool) {
	if r.sess == nil {
		return
	}
	err := r.sess.Close()
	if err == nil || errors.Is(err, r.sess.Err()) {
		return
	}
	fmt.Fprintln(r.cfg.stderr, "padmated: stopping the session watcher:", err)
	if logged {
		r.log.Warn("session watcher did not stop", "err", err)
	}
}

// stopWatch stops the watcher, when it runs, and waits for its Run to
// return: a reload it started has ended then. It waits cfg.watchStop at
// most: a read that hangs holds up the exit, not the release of the keys.
// The watcher reloads nothing after that, as reloadProfile checks its
// context first and Reload fails once Run has ended. It runs on the
// goroutine of stop and shutdown only, and waits once.
func (r *liveRun) stopWatch() {
	if r.stopWatching == nil {
		return
	}
	r.stopWatching()
	r.stopWatching = nil
	d := r.cfg.watchStop
	if d <= 0 {
		d = watchStopWait
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.watched:
	case <-t.C:
		fmt.Fprintf(r.cfg.stderr, "padmated: the profile watcher did not stop within %v; stopping without it\n", d)
		r.log.Warn("profile watcher did not stop", "after", d.String())
	}
}
