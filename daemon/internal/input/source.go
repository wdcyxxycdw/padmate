package input

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// eventBuffer is the capacity of the Events channel. T-input never blocks
// on it: when it is full, a button or axis event is dropped and counted,
// and a device event waits in line (see send).
const eventBuffer = 1024

// waitMS bounds how long T-input waits for an SDL event before it looks
// for commands, so it is also the longest a command waits. Without a
// video subsystem SDL_WaitEventTimeout polls every millisecond anyway
// (SDL_WaitEventTimeoutNS in src/events/SDL_events.c), so a short timeout
// costs nothing extra. Waking T-input with SDL_PushEvent instead would
// race with SDL_Quit, which destroys the event queue.
const waitMS = 2

var (
	// ErrActive is returned by Open while another Source of this process
	// is open: SDL has one global state per process.
	ErrActive = errors.New("input: another Source is open in this process")
	// ErrClosed is returned by the methods of a closed Source.
	ErrClosed = errors.New("input: the Source is closed")
)

// active is set while a Source exists, from Open until its T-input has
// called SDL_Quit.
var active atomic.Bool

// Options configures Open.
type Options struct {
	// Library is the SDL library to load. "" means ResolveLibrary.
	Library string
	// Hints are set in order before SDL_Init, after openHints:
	// SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS=1, SDL_NO_SIGNAL_HANDLERS=1 and
	// SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0, which Open always sets first.
	// A hint here with one of those names overrides it. nil means the
	// "default" preset. A hint that is also an environment variable keeps
	// the environment's value, which SDL gives priority.
	Hints []Hint
	// OnCombo turns on combo detection (DMN-07): when a gamepad has held
	// its DefaultCombo for the hold time, T-input calls OnCombo, then
	// sends a ComboHeld event, which is never dropped. The grip of a
	// single Joy-Con comes from SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS as
	// in effect (Hints). OnCombo runs on T-input, so it must return at
	// once and must not call the Source. nil turns detection off, as
	// "padmatectl monitor" and "record" leave it.
	OnCombo func()
	// ComboFor optionally selects an immutable, configured emergency combo.
	// It runs on T-input and must not block; nil uses DefaultCombo. It may
	// give another combo after a config change, which RepickCombos applies.
	ComboFor func(DeviceInfo, bool) Combo
	// ComboHold replaces ComboHold as the hold time of the combos, for
	// tests; 0 or less keeps ComboHold.
	ComboHold time.Duration
	// ReceiverTimeout turns on the receiver watchdog (IN-30, ADR-0029)
	// with this timeout: T-input reads the HID reports of every Switch Pro
	// protocol device on USB (057e:2009, SDL's HIDAPI driver), and once one
	// has proven that it reports while its input does not change, reports
	// it removed when its reports and its controls have been silent for
	// the timeout; it comes back as a new device, with a new DeviceID, at
	// its next SDL event. padmated passes DefaultReceiverTimeout. 0 leaves
	// the watchdog off, as "padmatectl monitor", "record" and spk01 do,
	// which then show what SDL reports; a timeout below 100ms is an error.
	ReceiverTimeout time.Duration
	// PowerEvents turns on DevicePower events (IN-12): T-input reads the
	// power of every gamepad when it connects and when it comes back from
	// the receiver watchdog's removal, sends SDL's battery events on, and
	// reads every 30 s in case a driver sends none; it sends a DevicePower
	// for the first read and each change. Only padmated sets it: the other
	// consumers of Events (monitor, record, spk01) get no such event.
	// Source.Readout reads on request without it.
	PowerEvents bool
	// PowerPollEvery is how often T-input reads the power with
	// PowerEvents on; 0 means every 30 s. Tests shorten it.
	PowerPollEvery time.Duration
	// Mappings are the custom gamepad mappings (IN-18, ADR-0072): SDL mapping
	// strings, at most one for each GUID, that SDL is to read joysticks with.
	// nil leaves them alone, as "padmatectl monitor" and "record" do. A non-nil
	// list, also an empty one, makes Open write them to a database file in the
	// temporary folder and set SDL_GAMECONTROLLERCONFIG_FILE to it, so that SDL
	// has them before it enumerates the devices and a mapped joystick is a
	// gamepad from its first report; the file stays until Close and is
	// rewritten by SetMappings, which SDL's own reload reads again. If the
	// environment has its own SDL_GAMECONTROLLERCONFIG_FILE, SDL uses that one
	// and Open adds the mappings through the API instead.
	Mappings []string
	// Log gets T-input's records: the receiver watchdog's (ARCHITECTURE
	// §5, "日志"), at the watchdog's transitions, and at debug level once
	// a second per watched device. T-input never waits for Log: it queues
	// each record, up to 256, and a goroutine of the Source writes them
	// to Log's handler in order. A record that does not fit is dropped;
	// once the goroutine has written what waits, it logs how many were
	// dropped, a warn "input log records dropped" with dropped. Close
	// waits up to 1 s for the queue to be written. A handler panic stops
	// the log worker and discards further records. nil discards them.
	Log *slog.Logger
}

// Source reads gamepads through SDL on T-input, its own locked OS thread.
type Source struct {
	events  chan Event
	dropped atomic.Uint64
	// rawEvents carries the raw joystick events of the devices asked for
	// (raw.go); rawDropped counts those lost to a full channel.
	rawEvents  chan RawEvent
	rawDropped atomic.Uint64

	cmds      chan func()   // run on T-input between waits
	quit      chan struct{} // closed by Close
	quitOnce  sync.Once
	done      chan struct{} // closed when T-input has quit SDL
	library   string
	version   string
	hintsRead []Hint
	thread    uint32 // T-input's OS thread id; set before ready
	// failure is why T-input ended by itself; set before Events closes.
	failure atomic.Pointer[PanicError]

	onCombo   func()        // Options.OnCombo
	comboHold time.Duration // Options.ComboHold
	comboFor  func(DeviceInfo, bool) Combo
	receiver  receiverTiming // from Options.ReceiverTimeout; zero when off
	power     bool           // Options.PowerEvents
	powerPoll time.Duration  // Options.PowerPollEvery, or powerPollEvery
	// powerNext is when T-input reads the power of every gamepad next,
	// zero before the first look; ticks reads SDL's clock (SDL_GetTicksNS),
	// nil without SDL.
	powerNext time.Time
	ticks     func() uint64
	// mix drives the effects of the open gamepads: rumble, LED and player
	// lights (feedback.go). T-input makes it at its first use (effects).
	mix *haptic.Mixer
	// log is T-input's logger: through logs to Options.Log, or one that
	// discards when logs is nil.
	log  *slog.Logger
	logs *logQueue

	// Owned by T-input.
	// stamp is latency.Now as read right after SDL_WaitEventTimeout returned
	// the event that handle is turning into Events (Event.Stamp).
	stamp    latency.Stamp
	devices  map[sdl.JoystickID]*device // every reported device
	virtuals map[sdl.JoystickID]*Virtual
	// rawOn counts the devices asked for with RawStart, and rawPending the axes
	// that keep a value for the rate limit (raw.go).
	rawOn, rawPending int
	// mappings are the custom gamepad mappings SDL has; mapFile is the database
	// file of Options.Mappings, or "" without one (mapping.go).
	mappings mappingSet
	mapFile  string
	// watch finds the combos, when onCombo is set; init makes it.
	watch *ComboWatch
	// backlog holds critical device/combo/state events and publication
	// fences in order when Events is full; Close discards it.
	backlog []Event
	// told holds the devices whose DeviceAdded reached Events and whose
	// DeviceRemoved has not: the ones the consumer knows (send).
	told map[DeviceID]struct{}
	// gone counts the adds that were not reported because the device was
	// already gone; only tests read it.
	gone int
	// openHID opens the heartbeat source of a real watched device by its
	// path: openHIDPath, or a test's.
	openHID func(path string) (*hidBeat, error)
	// watched counts the devices with a heartbeat source; lastWatch is
	// when watchReceivers last drained them. aliases counts the new ids
	// given to devices that came back (alias).
	watched   int
	lastWatch uint64
	aliases   uint32
}

// deviceKind says how T-input reads a device. SDL changes it when a mapping
// is added or taken away (IN-18), so it is not fixed for a device's life.
type deviceKind uint8

const (
	// kindGamepad is a gamepad that T-input opened (device.gp is set).
	kindGamepad deviceKind = iota
	// kindFailed is a device that SDL calls a gamepad but that did not open;
	// reenumerate opens it once it can.
	kindFailed
	// kindRaw is a joystick that SDL has no gamepad mapping for. It is
	// reported, and its elements are the raw stream when asked for.
	kindRaw
)

// device is a reported device, owned by T-input.
type device struct {
	id sdl.JoystickID
	// pub is the id its events carry: DeviceID(id), or after the receiver
	// watchdog reported it removed and it came back, a new one (alias).
	pub  DeviceID
	kind deviceKind
	// raw is set while the device is asked for with RawStart. It stays when a
	// mapping turns the device into a gamepad and back.
	raw *rawWatch
	// gp is nil for a joystick without a mapping (kindRaw), and for a gamepad
	// that failed to open (kindFailed).
	gp *sdl.Gamepad
	// axes holds the last reported value of each gamepad axis. SDL sends
	// an axis event whenever the underlying joystick axis changes, and
	// sends the first value of each joystick axis twice, so T-input drops
	// events that repeat the last value.
	axes [sdl.GAMEPAD_AXIS_COUNT]int16
	// beat is the heartbeat source of a device that the receiver watchdog
	// watches, and nil for another (IN-30); dog is its watchdog. dormant is
	// set once the watchdog has reported it removed, until it comes back.
	// statsAt is when its statistics were last logged.
	beat    *hidBeat
	dog     watchdog
	dormant bool
	statsAt uint64
	// powerFn reads the power of an open gamepad (Gamepad.PowerInfo), and
	// is nil for a device without one. power is the value sent last, when
	// powerSent (readPower).
	powerFn   func() (sdl.PowerState, int32)
	power     Power
	powerSent bool
	// driver is the SDL backend of the gamepad (DeviceInfo.Driver), which the
	// power mapping needs (powerFromSDL); set when the gamepad opens.
	driver string
	// mapText is the mapping SDL read for the gamepad when it was last
	// described (SDL_GetGamepadMappingForID): a remap that leaves it as it was
	// changes nothing for the consumer (remapped).
	mapText string
}

// Open starts T-input: it loads SDL, applies the hints and initializes the
// gamepad subsystem. Devices that are already connected are reported as
// DeviceAdded events, some of them only after a few hundred milliseconds
// while SDL's backends detect them.
func Open(opts Options) (*Source, error) {
	receiver, err := newReceiverTiming(opts.ReceiverTimeout)
	if err != nil {
		return nil, err
	}
	if !active.CompareAndSwap(false, true) {
		return nil, ErrActive
	}
	path := opts.Library
	if path == "" {
		p, err := ResolveLibrary()
		if err != nil {
			active.Store(false)
			return nil, err
		}
		path = p
	}
	hints := opts.Hints
	if hints == nil {
		hints, _ = PresetHints("default")
	}
	var mappings mappingSet
	mapFile := ""
	if opts.Mappings != nil {
		var err error
		if mappings, err = parseMappingSet(opts.Mappings); err != nil {
			active.Store(false)
			return nil, err
		}
		if mapFile, err = createMappingFile(mappings); err != nil {
			active.Store(false)
			return nil, err
		}
		// SDL reads the file in SDL_Init, before it enumerates any device. The
		// hint goes last so that no preset can replace it.
		hints = append(slices.Clone(hints), Hint{Name: sdl.HINT_GAMECONTROLLERCONFIG_FILE, Value: mapFile})
	}
	log, logs := slog.New(slog.DiscardHandler), (*logQueue)(nil)
	if opts.Log != nil {
		logs = newLogQueue(opts.Log.Handler(), logQueueLen)
		log = logs.logger()
	}
	s := &Source{
		events:    make(chan Event, eventBuffer),
		cmds:      make(chan func()),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
		library:   path,
		onCombo:   opts.OnCombo,
		comboHold: opts.ComboHold,
		comboFor:  opts.ComboFor,
		receiver:  receiver,
		power:     opts.PowerEvents,
		powerPoll: opts.PowerPollEvery,
		ticks:     sdl.TicksNS,
		log:       log,
		logs:      logs,
		openHID:   openHIDPath,
		devices:   make(map[sdl.JoystickID]*device),
		virtuals:  make(map[sdl.JoystickID]*Virtual),
		rawEvents: make(chan RawEvent, rawBuffer),
		mappings:  mappings,
		mapFile:   mapFile,
	}
	// The first latency.Now looks up QueryPerformanceCounter and the first Freq
	// QueryPerformanceFrequency: both are done here, so that T-input's stamps
	// and T-output's Sub (DMN-15) allocate nothing.
	_, _ = latency.Now(), latency.Freq()
	ready := make(chan error, 1)
	go s.run(hints, ready)
	if err := <-ready; err != nil {
		<-s.done
		return nil, err
	}
	return s, nil
}

// removeMappingFile deletes the database file of Options.Mappings, once SDL
// has quit (or never started).
func (s *Source) removeMappingFile() {
	if s.mapFile != "" {
		_ = os.Remove(s.mapFile)
	}
}

// Events returns the events of all devices, in order. The channel is
// closed by Close.
func (s *Source) Events() <-chan Event { return s.events }

// Dropped returns how many button and axis events were lost because
// Events was full, or because a device event still waited for room.
// Device events, ComboHeld, DeviceState and DevicePower are never lost:
// they wait for room, in order. The combo watch sees the lost events too.
func (s *Source) Dropped() uint64 { return s.dropped.Load() }

// Library returns the loaded SDL library, as ResolveLibrary gave it: an
// absolute path, or outside Windows possibly a name that the dynamic
// loader found, such as "libSDL3.so.0".
func (s *Source) Library() string { return s.library }

// Version returns the version of the loaded SDL library, such as "3.4.16".
func (s *Source) Version() string { return s.version }

// Hints returns the hints in effect after SDL_Init, read back with
// SDL_GetHint: the hints Open set, in order, then the other BackendHints
// and JoyConHints. For a hint that is also an environment variable, the
// value is the environment's. A value is "" when the hint is set neither
// by padmate nor in the environment, so SDL uses its default.
func (s *Source) Hints() []Hint { return append([]Hint(nil), s.hintsRead...) }

// WriteHeader writes the SDL version and library of s, the preset named
// preset and the hints in effect, one per line, each line starting with
// prefix. It is the header that "padmatectl monitor" and spk01 print
// before their events.
func (s *Source) WriteHeader(w io.Writer, prefix, preset string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%sSDL %s from %s\n", prefix, s.version, s.library)
	fmt.Fprintf(&b, "%spreset %s; hints in effect:\n", prefix, preset)
	for _, h := range s.hintsRead {
		if h.Value == "" {
			fmt.Fprintf(&b, "%s  %s unset (SDL's default)\n", prefix, h.Name)
		} else {
			fmt.Fprintf(&b, "%s  %s\n", prefix, h)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// ThreadID returns the id of T-input's OS thread, the thread that
// initialized SDL and pumps its events (GetCurrentThreadId), for
// diagnostics. It is 0 outside Windows.
func (s *Source) ThreadID() uint32 { return s.thread }

// maxTickSamples bounds SampleTicks, so that it holds up T-input for
// well under a millisecond.
const maxTickSamples = 1024

// SampleTicks reads SDL_GetTicksNS, the clock of Event.Time, n times on
// T-input, each between two latency.Now reads, and returns the readings
// with SDL_GetPerformanceFrequency. It lets a caller map Event.Time onto
// latency Stamps with latency.CalibrateSDL (ENG-11). n must be in
// 1..1024. It returns ErrClosed once the Source is closed.
func (s *Source) SampleTicks(n int) (freq int64, pairs []latency.TicksPair, err error) {
	if n < 1 || n > maxTickSamples {
		return 0, nil, fmt.Errorf("input: SampleTicks(%d): n must be in 1..%d", n, maxTickSamples)
	}
	pairs = make([]latency.TicksPair, n)
	err = s.do(func() error {
		f := sdl.GetPerformanceFrequency()
		if f > math.MaxInt64 {
			return fmt.Errorf("input: SDL counter frequency %d overflows int64", f)
		}
		freq = int64(f)
		for i := range pairs {
			before := latency.Now()
			ns := sdl.TicksNS()
			pairs[i] = latency.TicksPair{Before: before, After: latency.Now(), NS: ns}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return freq, pairs, nil
}

// RequestClose asks T-input to exit, without waiting or making SDL calls.
// T-input still owns the complete SDL teardown. It may be called repeatedly.
func (s *Source) RequestClose() {
	s.quitOnce.Do(func() { close(s.quit) })
}

// Close detaches the virtual gamepads, closes the devices, quits SDL and
// closes the Events channel. It waits for T-input to exit. Calling it
// again does nothing.
func (s *Source) Close() {
	s.RequestClose()
	<-s.done
}

// Err returns a *PanicError once a panic, or runtime.Goexit, on T-input
// has ended it, and nil otherwise: Close ends it without an error. It is
// set before Events closes, so a consumer that sees Events closed without
// having called Close learns here why.
func (s *Source) Err() error {
	if pe := s.failure.Load(); pe != nil {
		return pe
	}
	return nil
}

// PanicError is Source.Err after a panic on T-input.
type PanicError struct {
	Value any    // what was recovered; "runtime.Goexit" for runtime.Goexit
	Stack []byte // debug.Stack() at the recover
}

func (e *PanicError) Error() string { return fmt.Sprintf("input: panic on T-input: %v", e.Value) }

// do runs f on T-input and returns its error, or ErrClosed once the
// Source is closed, wrapping Err when f ended T-input. f must not call do.
func (s *Source) do(f func() error) error {
	errc := make(chan error, 1)
	select {
	case s.cmds <- func() { errc <- f() }:
	case <-s.done:
		return ErrClosed
	}
	select {
	case err := <-errc:
		return err
	case <-s.done:
		select {
		case err := <-errc: // f returned, and then Close ended T-input
			return err
		default: // f panicked
			return fmt.Errorf("%w: %w", ErrClosed, s.Err())
		}
	}
}

// run is T-input. It stays locked to its OS thread for its whole life, so
// the thread exits with the goroutine. A panic or runtime.Goexit on it
// ends it as Close does, as far as SDL still lets it: Err reports it and
// Events closes, so that the consumer takes its exit path (CLAUDE.md
// invariant 1) instead of the process dying. Last, before done, it closes
// its log queue and waits for it to be written, for logFlushWait at most.
func (s *Source) run(hints []Hint, ready chan<- error) {
	runtime.LockOSThread()
	defer close(s.done)
	if s.logs != nil {
		defer s.logs.close(logFlushWait)
	}
	defer active.Store(false)
	defer s.removeMappingFile()
	defer close(s.rawEvents)
	defer close(s.events)
	readied, ended := false, false
	defer func() {
		if !ended {
			s.fail(recover(), ready, readied)
		}
	}()

	s.thread = currentThreadID()
	err := s.init(hints)
	ready <- err
	readied = true
	if err == nil {
		s.loop()
		s.shutdown()
	}
	ended = true
}

// fail ends T-input after a panic, or runtime.Goexit when v is nil: it
// keeps the error for Err, hands it to Open if Open still waits, and
// releases SDL as shutdown does, unless that panics too.
func (s *Source) fail(v any, ready chan<- error, readied bool) {
	if v == nil {
		v = "runtime.Goexit" // recover returns nil for it
	}
	pe := &PanicError{Value: v, Stack: debug.Stack()}
	s.failure.Store(pe)
	if !readied {
		ready <- pe
	}
	defer func() { _ = recover() }() // SDL may be what panicked
	s.shutdown()
}

// init loads SDL and initializes its gamepad subsystem. When it fails
// after loading, SDL has been quit again.
func (s *Source) init(hints []Hint) error {
	v, err := loadLibrary(s.library)
	if err != nil {
		return err
	}
	s.version = v.String()
	hints = slices.Concat(openHints, hints)
	names := make([]string, 0, len(hints))
	for _, h := range hints {
		names = append(names, h.Name)
		if err := sdl.SetHint(h.Name, h.Value); err != nil {
			if _, env := os.LookupEnv(h.Name); env {
				// SDL_SetHint fails for a hint that is also an
				// environment variable, whose value SDL uses instead.
				continue
			}
			sdl.Quit()
			return fmt.Errorf("SDL_SetHint %s: %w", h, err)
		}
	}
	if err := sdl.Init(sdl.INIT_GAMEPAD); err != nil {
		sdl.Quit()
		return fmt.Errorf("SDL_Init(SDL_INIT_GAMEPAD): %w", err)
	}
	seen := make(map[string]bool)
	for _, name := range slices.Concat(names, BackendHints, JoyConHints) {
		if !seen[name] {
			seen[name] = true
			s.hintsRead = append(s.hintsRead, Hint{name, sdl.GetHint(name)})
		}
	}
	if err := s.applyInitialMappings(); err != nil {
		sdl.Quit()
		return err
	}
	if s.onCombo != nil {
		s.watch = NewComboWatch(s.comboPick())
	}
	return nil
}

// VerticalJoyCons reports whether SDL reports a single Joy-Con held
// vertically, as the hint SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS in effect
// says: the grip ComboFor gets.
func (s *Source) VerticalJoyCons() bool {
	vertical := false
	for _, h := range s.hintsRead {
		if h.Name == HintVerticalJoyCons {
			vertical = hintTrue(h.Value)
		}
	}
	return vertical
}

// RepickCombos asks the combo watch on T-input to pick every device's
// combo again (ComboWatch.Repick), through ComboFor, which now gives
// another combo: padmated calls it once config.json changed them (UI-19).
// A hold under way does not fire; the new combo fires once all its
// members are pressed again. It does nothing without combo detection, and
// returns ErrClosed once the Source is closed.
func (s *Source) RepickCombos() error {
	return s.do(func() error {
		if s.watch != nil {
			s.watch.Repick()
		}
		return nil
	})
}

// comboPick returns the pick of s's ComboWatch: DefaultCombo with the grip
// of the hint in effect, and Options.ComboHold.
func (s *Source) comboPick() func(DeviceInfo) Combo {
	vertical := s.VerticalJoyCons()
	hold := s.comboHold
	return func(info DeviceInfo) Combo {
		c := DefaultCombo(info, vertical)
		if s.comboFor != nil {
			c = s.comboFor(info, vertical)
		}
		if hold > 0 {
			c.Hold = hold
		}
		return c
	}
}

// hintTrue reports whether SDL reads the hint value v as true, as
// SDL_GetHintBoolean does with a default of false (SDL_GetStringBoolean in
// src/SDL_utils.c): "" is the default, and a value that starts with "0" or
// is "false" in any case is false.
func hintTrue(v string) bool {
	return v != "" && v[0] != '0' && !strings.EqualFold(v, "false")
}

// loop pumps SDL events and runs commands until Close. Each turn also
// sends what waits in the backlog, so it goes out within waitMS of room
// on Events even when no new event comes.
//
// With combo detection on, a combo fires when an event comes at or after
// its due time (emit), or when none has come within waitMS: SDL's queue
// is then empty, so the events up to about now are handled, and a member
// released before the due time has been seen. An event that handle
// ignores fires the combos due by its time as well, so that a device that
// floods SDL with such events delays no combo.
//
// Each turn also watches the receivers (watchReceivers), at the time that
// the turn has given fireDue: the time of the event it handled, or now
// when none came. A removal that the watchdog makes carries that time, so
// it fires no combo that an event still queued in SDL might disarm; and a
// device that floods SDL does not hold up the watchdog either.
func (s *Source) loop() {
	var ev sdl.Event
	for {
		select {
		case <-s.quit:
			return
		case f := <-s.cmds:
			f()
			continue
		default:
		}
		s.advanceEffects(time.Now())
		s.flush()
		s.flushRaw()
		if sdl.WaitEventTimeout(&ev, waitMS) {
			s.stamp = latency.Now() // the start of the latency statistics (DMN-15)
			s.handle(&ev)
			now := time.Now()
			s.advanceEffects(now)
			s.pollPower(now)
			t := ev.CommonEvent().Timestamp
			if s.armed() {
				s.fireDue(t)
			}
			if s.watched > 0 { // no clock read on the hot path without one
				s.watchReceivers(sdl.TicksNS(), t)
			}
		} else {
			wall := time.Now()
			s.advanceEffects(wall)
			s.pollPower(wall)
			now := sdl.TicksNS()
			s.watchReceivers(now, now)
			if s.armed() {
				s.fireDue(now)
			}
		}
	}
}

// armed reports whether a combo is armed.
func (s *Source) armed() bool { return s.watch != nil && s.watch.armed > 0 }

// fireDue fires the combos due at or before now, on SDL's clock: for each,
// it calls OnCombo, then sends its ComboHeld.
func (s *Source) fireDue(now uint64) {
	if due, ok := s.watch.Due(); !ok || due > now {
		return
	}
	for _, e := range s.watch.Fire(now) {
		s.onCombo()
		s.send(e)
	}
}

// emit sends e, an event of a device. With combo detection on, it first
// fires the combos due by e.Time, so that they come before e as they came
// before it in time, and shows e to the watch, which so sees the button
// events that send drops.
func (s *Source) emit(e Event) {
	if s.watch != nil {
		s.fireDue(e.Time)
		s.watch.Observe(e)
	}
	s.send(e)
}

// shutdown releases every SDL object and quits SDL. SDL_Quit would also do
// it; releasing them first keeps the order explicit.
func (s *Source) shutdown() {
	for _, v := range s.virtuals {
		// The removal is not reported: the Events channel closes next.
		_ = s.detach(v)
	}
	// Every effect ends before the gamepads close (invariant 1, for haptics).
	s.stopAllEffects()
	for id, d := range s.devices {
		if d.beat != nil {
			d.beat.close()
			d.beat = nil
		}
		s.rawClose(d)
		if d.gp != nil {
			d.gp.Close()
		}
		delete(s.devices, id)
	}
	sdl.Quit()
}

// handle turns one SDL event into Events. It ignores the other events,
// SDL_EVENT_QUIT among them: SDL raises that one for SIGINT and SIGTERM
// only with its signal handlers, which Open turns off.
func (s *Source) handle(ev *sdl.Event) {
	switch ev.Type {
	case sdl.EVENT_GAMEPAD_ADDED:
		e := ev.GamepadDeviceEvent()
		s.addGamepad(e.Which, e.Timestamp)
	case sdl.EVENT_JOYSTICK_ADDED:
		e := ev.JoyDeviceEvent()
		// A gamepad is reported by its EVENT_GAMEPAD_ADDED, which SDL
		// queues right after this one. A joystick that SDL no longer lists
		// is not reported, as in addGamepad.
		if _, ok := s.devices[e.Which]; !ok && !e.Which.IsGamepad() {
			if !listed(e.Which) {
				s.gone++
				return
			}
			s.devices[e.Which] = &device{id: e.Which, pub: DeviceID(e.Which), kind: kindRaw}
			s.emit(Event{Time: e.Timestamp, Device: DeviceID(e.Which), Kind: DeviceAdded, Info: deviceInfo(e.Which, false)})
		}
	case sdl.EVENT_JOYSTICK_BATTERY_UPDATED:
		s.handlePower(ev.JoyBatteryEvent())
	case sdl.EVENT_GAMEPAD_REMOVED:
		// SDL sends this when the device is gone, and also when a mapping is
		// taken away (IN-18): then SDL still lists the joystick. Whether it is a
		// gamepad is looked at now, not when SDL queued the event: a mapping that
		// was taken away and added again before T-input got here, as
		// SetMappings does for the mappings that stay, is a remap (and SDL
		// sends REMAPPED, not ADDED, for it); one that stays away leaves a
		// joystick; and a joystick that is one already has nothing to lose.
		e := ev.GamepadDeviceEvent()
		if d := s.devices[e.Which]; d != nil && listed(e.Which) {
			switch {
			case d.kind == kindRaw:
			case e.Which.IsGamepad():
				s.remapped(e.Which, e.Timestamp)
			default:
				s.downgrade(d, e.Timestamp)
			}
			return
		}
		s.remove(e.Which, e.Timestamp)
	case sdl.EVENT_JOYSTICK_REMOVED:
		e := ev.JoyDeviceEvent()
		s.remove(e.Which, e.Timestamp)
	case sdl.EVENT_GAMEPAD_REMAPPED:
		e := ev.GamepadDeviceEvent()
		s.remapped(e.Which, e.Timestamp)
	case sdl.EVENT_JOYSTICK_AXIS_MOTION:
		if s.rawOn > 0 {
			e := ev.JoyAxisEvent()
			s.rawAxis(e.Which, e.Axis, e.Value, e.Timestamp)
		}
	case sdl.EVENT_JOYSTICK_BUTTON_DOWN, sdl.EVENT_JOYSTICK_BUTTON_UP:
		if s.rawOn > 0 {
			e := ev.JoyButtonEvent()
			s.rawButton(e.Which, e.Button, e.Down, e.Timestamp)
		}
	case sdl.EVENT_JOYSTICK_HAT_MOTION:
		if s.rawOn > 0 {
			e := ev.JoyHatEvent()
			s.rawHat(e.Which, e.Hat, e.Value, e.Timestamp)
		}
	case sdl.EVENT_GAMEPAD_BUTTON_DOWN, sdl.EVENT_GAMEPAD_BUTTON_UP:
		e := ev.GamepadButtonEvent()
		id := DeviceID(e.Which)
		if d := s.devices[e.Which]; d != nil {
			if d.kind == kindRaw {
				return // queued before a mapping was taken away
			}
			if d.dormant && !s.wake(d, e.Timestamp) {
				return
			}
			id = d.pub
			if d.beat != nil {
				d.dog.control(e.Timestamp, true, d.axes)
			}
		}
		kind := ButtonUp
		if e.Down {
			kind = ButtonDown
		}
		s.emit(Event{Time: e.Timestamp, Stamp: s.stamp, Device: id, Kind: kind, Control: ButtonName(e.Button)})
	case sdl.EVENT_GAMEPAD_AXIS_MOTION:
		e := ev.GamepadAxisEvent()
		id := DeviceID(e.Which)
		if d := s.devices[e.Which]; d != nil && int(e.Axis) < len(d.axes) {
			if d.kind == kindRaw || d.axes[e.Axis] == e.Value {
				return
			}
			if d.dormant && !s.wake(d, e.Timestamp) {
				return
			}
			id = d.pub
			d.axes[e.Axis] = e.Value
			if d.beat != nil {
				d.dog.control(e.Timestamp, false, d.axes)
			}
		}
		s.emit(Event{Time: e.Timestamp, Stamp: s.stamp, Device: id, Kind: AxisMotion, Control: AxisName(e.Axis), Value: e.Value})
	}
}

// addGamepad opens the gamepad id and reports it, once. SDL only sends
// gamepad button and axis events for an open gamepad. A joystick that was
// reported without a mapping and has one now (IN-18) is reported again, as a
// gamepad with the same id: the consumer took the first report for a device
// that no slot maps, and learns that it is a gamepad now. The raw events it
// was asked for go on.
func (s *Source) addGamepad(id sdl.JoystickID, t uint64) {
	var raw *rawWatch
	if old, ok := s.devices[id]; ok {
		if old.kind != kindRaw {
			return
		}
		raw = old.raw
	}
	gp, err := id.OpenGamepad()
	if err != nil && !listed(id) {
		// id was removed before T-input handled its EVENT_GAMEPAD_ADDED,
		// as a Joy-Con is when SDL folds it into a pair in the same update
		// (SDL_hidapijoystick.c, HIDAPI_CreateCombinedJoyCons). SDL still
		// calls it a gamepad and keeps its name, but has no GUID, ids or
		// path for it, so it would get the zero DeviceKey. Nothing of it
		// is reported; its removal events find no device.
		s.gone++
		return
	}
	d := &device{id: id, pub: DeviceID(id), raw: raw}
	// A gamepad that SDL still lists but fails to open is still reported;
	// its removal, if it is going, follows. reenumerate opens it once it can.
	var info *DeviceInfo
	if err == nil {
		info = s.attachGamepad(d, gp, s.describe)
	} else {
		d.kind = kindFailed
		info = s.describe(d)
	}
	if d.gp != nil {
		d.readAxes()
		// The state fails only for a gamepad that has just gone.
		state, _ := d.gp.ConnectionState()
		s.openBeat(d, info, state, t)
	}
	s.devices[id] = d
	s.emit(Event{Time: t, Device: d.pub, Kind: DeviceAdded, Info: info})
	s.readPower(d, t)
}

// describe returns the DeviceInfo of the gamepad d, with the id d.pub.
// The serial and the masks come from the open gamepad; they stay empty
// when it failed to open.
// It also notes the mapping that SDL reads for d (device.mapText).
func (s *Source) describe(d *device) *DeviceInfo {
	info := deviceInfo(d.id, true)
	info.ID = d.pub
	gp := d.gp
	if gp == nil {
		return info
	}
	d.mapText = d.id.GamepadMapping()
	info.Serial = gp.Serial()
	info.Capabilities = readCapabilities(gp)
	for b := uint8(0); b < uint8(sdl.GAMEPAD_BUTTON_COUNT); b++ {
		if gp.HasButton(sdl.GamepadButton(b)) {
			info.ButtonMask |= uint32(1) << b
			label := buttonLabelName(gp.ButtonLabel(sdl.GamepadButton(b)))
			switch sdl.GamepadButton(b) {
			case sdl.GAMEPAD_BUTTON_SOUTH:
				info.ButtonLabels.South = label
			case sdl.GAMEPAD_BUTTON_EAST:
				info.ButtonLabels.East = label
			case sdl.GAMEPAD_BUTTON_WEST:
				info.ButtonLabels.West = label
			case sdl.GAMEPAD_BUTTON_NORTH:
				info.ButtonLabels.North = label
			}
		}
	}
	for a := uint8(0); a < uint8(sdl.GAMEPAD_AXIS_COUNT); a++ {
		if gp.HasAxis(sdl.GamepadAxis(a)) {
			info.AxisMask |= uint8(1) << a
		}
	}
	return info
}

// readAxes reads the position of the axes of the open gamepad d as it
// connects, so that the first value of each, which SDL sends twice, is
// dropped as a repeat.
func (d *device) readAxes() {
	for a := range d.axes {
		d.axes[a] = d.gp.Axis(sdl.GamepadAxis(a))
	}
}

// listed reports whether SDL still lists the joystick id
// (SDL_GetJoysticks). When the list cannot be read it reports true, so
// that the device is reported as before.
func listed(id sdl.JoystickID) bool {
	ids, err := sdl.GetJoysticks()
	return err != nil || slices.Contains(ids, id)
}

// downgrade turns the gamepad d, which SDL no longer reads as one because a
// mapping was taken away (IN-18), into a joystick without a mapping that
// is still connected: the consumer is told of its removal and then of the
// joystick, with the same id. The raw events it was asked for go on.
func (s *Source) downgrade(d *device, t uint64) {
	if d.beat != nil {
		s.endBeat(d)
	}
	// The joystick is still there, but nothing else may touch the gamepad
	// that closes below; its effects end with SDL_CloseJoystick (forgetEffects).
	s.forgetEffects(d)
	// Closing the gamepad of a USB Switch device that the receiver watchdog
	// gave up puts it in simple mode, where it does not come back (PITFALLS,
	// "2.4G 接收器"); a dormant device keeps its handle until SDL_Quit.
	if d.gp != nil && !d.dormant {
		d.gp.Close()
	}
	nd := &device{id: d.id, pub: DeviceID(d.id), kind: kindRaw, raw: d.raw}
	s.devices[d.id] = nd
	if d.dormant {
		return // its removal was reported when the receiver watchdog lost it
	}
	s.emit(Event{Time: t, Device: d.pub, Kind: DeviceRemoved})
	info := deviceInfo(d.id, false)
	s.emit(Event{Time: t, Device: nd.pub, Kind: DeviceAdded, Info: info})
	s.log.Info("gamepad mapping taken away: reported as a joystick again", "id", nd.pub, "name", info.Name)
}

// remapped reports that SDL changed the mapping of the gamepad id, which is
// still a gamepad: the consumer gets the device as it is now, and resets it
// (DeviceRemapped). SDL releases nothing when it remaps: a button that is held
// may be bound to another element now and never send its release, so every
// control of the device must count as released (CLAUDE.md invariant 1). The
// last axis values are read again, so that the first event of each is not
// taken for a repeat.
//
// A remap that leaves the mapping SDL reads for the device as it was (SDL
// loading its database again for another device) is not reported: the controls
// and bindings are the same, and the reset would release what is held on a
// device that was not touched.
func (s *Source) remapped(id sdl.JoystickID, t uint64) {
	d := s.devices[id]
	if d == nil || d.kind == kindRaw {
		// SDL sends REMAPPED, not ADDED, for a joystick that got a mapping after
		// it lost one that T-input has not seen it lose yet.
		if listed(id) && id.IsGamepad() {
			s.addGamepad(id, t)
		}
		return
	}
	if d.kind != kindGamepad || d.dormant {
		// A gamepad that did not open is reopened by reenumerate; a dormant one
		// is read again when it comes back.
		return
	}
	// SDL loading its database again for another device's sake remaps every
	// open gamepad, and puts the mappings that stay back. A gamepad that reads
	// the same mapping as before has the same controls and binds the same
	// elements, so a held button still sends its own release: nothing to reset,
	// and a reset would release what the user holds on a pad that was not
	// touched.
	if text := d.id.GamepadMapping(); text != "" && text == d.mapText {
		return
	}
	d.readAxes()
	info := s.describe(d)
	s.emit(Event{Time: t, Device: d.pub, Kind: DeviceRemapped, Info: info})
	s.log.Info("gamepad mapping changed", "id", d.pub, "name", info.Name)
}

// remove closes the device id and reports its removal, once: SDL sends
// both EVENT_JOYSTICK_REMOVED and EVENT_GAMEPAD_REMOVED for a gamepad. A
// dormant device's removal was reported when the receiver watchdog lost
// it.
func (s *Source) remove(id sdl.JoystickID, t uint64) {
	d, ok := s.devices[id]
	if !ok {
		return
	}
	if d.beat != nil {
		s.endBeat(d)
	}
	// SDL has removed the gamepad (for a rescan, it no longer lists it): no
	// call may reach it, the player index above all (forgetEffects). A dormant
	// device's effects were stopped when it was lost.
	s.forgetEffects(d)
	s.rawClose(d)
	if d.gp != nil {
		d.gp.Close()
	}
	delete(s.devices, id)
	if !d.dormant {
		s.emit(Event{Time: t, Device: d.pub, Kind: DeviceRemoved})
	}
}

// send delivers e without blocking, after what waits in the backlog, so
// that no event overtakes an earlier one. DeviceAdded, DeviceRemoved,
// ComboHeld, DeviceState and DevicePower wait in the backlog if they do
// not fit: the consumer must learn of each connection, disconnection and
// combo, which T-input found from the events it dropped as well. A button
// or axis event that does not fit, or that would overtake a waiting event,
// is dropped and counted.
//
// A device is reported with one id for as long as it is connected, but more
// than once when its mapping changes (IN-18): a joystick that gets one is
// reported again as a gamepad, and a gamepad that loses it is removed and
// reported as a joystick. A DeviceRemoved takes the DeviceAdded events of its
// device that still wait out: they are of no use to a consumer that is told
// the device is gone. If the consumer never got a DeviceAdded of the device
// (told), nothing of it reached it and the DeviceRemoved is not sent either;
// otherwise it waits in line, once: a DeviceRemoved of the device already
// waits for the consumer, so another adds nothing. A ComboHeld of that device
// stays: the gamepad did hold its combo. A DevicePower or DeviceRemapped
// replaces the one of its device that still waits, and a DeviceRemoved takes
// the ones of its device out. So the backlog holds at most a DeviceRemoved, a
// joystick's and a gamepad's DeviceAdded per device, however often a device
// reconnects or changes its mapping while the consumer does not read, a
// DevicePower and a DeviceRemapped per connected device, and a ComboHeld per
// hold time that a gamepad held its combo for.
func (s *Source) send(e Event) {
	s.flush()
	if len(s.backlog) == 0 {
		select {
		case s.events <- e:
			if e.Kind == DeviceAdded || e.Kind == DeviceRemoved {
				s.delivered(e.Kind, e.Device)
			}
			return
		default:
		}
	}
	switch e.Kind {
	case DeviceState:
		// A later scan replaces a still queued snapshot of this device.
		// This bounds the backlog during canceled recovery retries.
		if i := slices.IndexFunc(s.backlog, func(b Event) bool { return b.Kind == DeviceState && b.Device == e.Device }); i >= 0 {
			s.backlog[i] = e
			return
		}
	case DevicePower:
		// A newer value replaces one of this device that still waits, in its
		// place: the line never holds two of a device.
		if i := slices.IndexFunc(s.backlog, func(b Event) bool { return b.Kind == DevicePower && b.Device == e.Device }); i >= 0 {
			s.backlog[i] = e
			return
		}
	case DeviceRemapped:
		// A newer report of the mapping replaces one that still waits, in its
		// place: the line never holds two of a device (Reload remaps every
		// gamepad that is open).
		if i := slices.IndexFunc(s.backlog, func(b Event) bool { return b.Kind == DeviceRemapped && b.Device == e.Device }); i >= 0 {
			s.backlog[i] = e
			return
		}
	case DeviceAdded, ComboHeld:
	case DeviceRemoved:
		// What a device said about its power or its mapping is of no use once
		// it is gone, nor is a report of it as a device that is there.
		var waitingAdded, waitingRemoved bool
		s.backlog = slices.DeleteFunc(s.backlog, func(b Event) bool {
			if b.Device != e.Device {
				return false
			}
			switch b.Kind {
			case DevicePower, DeviceRemapped:
				return true
			case DeviceAdded:
				waitingAdded = true
				return true
			case DeviceRemoved:
				waitingRemoved = true
			}
			return false
		})
		if _, known := s.told[e.Device]; waitingRemoved || (waitingAdded && !known) {
			return
		}
	default:
		s.dropped.Add(1)
		return
	}
	s.backlog = append(s.backlog, e)
}

// delivered notes that a DeviceAdded or a DeviceRemoved (kind) of the device
// id reached Events: the consumer knows the device from the one until the
// other (send).
func (s *Source) delivered(kind EventKind, id DeviceID) {
	switch kind {
	case DeviceAdded:
		if s.told == nil {
			s.told = make(map[DeviceID]struct{})
		}
		s.told[id] = struct{}{}
	case DeviceRemoved:
		delete(s.told, id)
	}
}

// flush sends the events of the backlog that fit, in order.
func (s *Source) flush() {
	for len(s.backlog) > 0 {
		if p := s.backlog[0].publication; p != nil {
			close(p.done)
			s.backlog = slices.Delete(s.backlog, 0, 1)
			continue
		}
		select {
		case s.events <- s.backlog[0]:
			if k := s.backlog[0].Kind; k == DeviceAdded || k == DeviceRemoved {
				s.delivered(k, s.backlog[0].Device)
			}
			s.backlog = slices.Delete(s.backlog, 0, 1)
		default:
			return
		}
	}
}

// deviceInfo describes the joystick id from SDL's per-id queries, which
// work without opening the device.
func deviceInfo(id sdl.JoystickID, gamepad bool) *DeviceInfo {
	info := &DeviceInfo{
		ID:        DeviceID(id),
		Type:      TypeName(int(sdl.GAMEPAD_TYPE_UNKNOWN)),
		Vendor:    id.JoystickVendorForID(),
		Product:   id.JoystickProductForID(),
		Version:   id.JoystickProductVersionForID(),
		IsGamepad: gamepad,
		Virtual:   id.IsJoystickVirtual(),
	}
	// The queries fail only for an id that has just been disconnected;
	// the fields then stay empty.
	if gamepad {
		// The gamepad name comes from the mapping, and is the one games
		// show.
		info.Name, _ = id.GamepadName()
		info.Type = TypeName(int(id.GamepadType()))
	} else {
		info.Name, _ = id.JoystickNameForID()
	}
	info.Path, _ = id.JoystickPathForID()
	if g, ok := deviceGUID(id); ok {
		info.GUID = g.String()
		info.Driver = driverName(g)
	}
	return info
}
