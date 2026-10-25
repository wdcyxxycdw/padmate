package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

const (
	// MaxSlots is the most slots a Supervisor has, and how many it has
	// when Config.Slots is 0: up to this many gamepads are mapped at once
	// (IN-08).
	MaxSlots = 4
	// firstGen is the profile generation of Config.Program; each Reload
	// takes the next (PRO-03).
	firstGen = 1
	// defaultEdgeBuffer is the capacity of a slot's edge channel when
	// Config.EdgeBuffer is 0.
	defaultEdgeBuffer = 256
)

// ErrStopped is returned by Sync, Reload and Pause once Run has ended.
var ErrStopped = errors.New("supervisor: stopped")

// PanicError is Run's error after a panic or runtime.Goexit on the
// goroutine that supervises a slot. A panic of the slot loop itself does
// not end Run: the slot restarts (see the package doc).
type PanicError struct {
	Slot  uint8
	Value any    // what was recovered; "runtime.Goexit" for runtime.Goexit
	Stack []byte // debug.Stack() at the recover
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("supervisor: panic in the runner of slot %d: %v", e.Slot, e.Value)
}

// Config configures a Supervisor.
type Config struct {
	Clock  clock.Clock        // all time, Output's clock too
	Events <-chan input.Event // closing it ends Run, like ctx
	// Dropped returns how many button and axis events input dropped so
	// far (input.Source.Dropped); nil means never. A DeviceAdded or
	// DeviceRemoved is never dropped.
	Dropped func() uint64
	Output  *output.Loop // T-output; the slots use Output.Remote()
	Program *engine.Program
	// ProfileKey is the stable profile identity used for in-memory session
	// state. padmated supplies the canonical profile file path until M8.
	ProfileKey string
	// LayerMemory retains only device/profile layer choices across reloads
	// and reconnections. It never performs disk I/O on slot threads.
	LayerMemory slot.LayerMemory
	// ScriptFactory creates the optional per-slot M5 runtime. It is called on
	// the runner goroutine before a slot loop starts; all runtime methods then
	// run on that slot's loop goroutine.
	ScriptFactory func(index uint8, gen uint32, clk clock.Clock, arb slot.Arbiter) (slot.ScriptRuntime, error)
	// ScriptConfigured reports whether the current profile needs its script
	// runtime: a legacy script, or an action that runs a macro (SCR-50). A
	// slot assigned while it does gets the Resync barrier that starts its
	// dormant runtime. A nil callback treats every ScriptFactory as
	// configured.
	ScriptConfigured func() bool
	Logger           *slog.Logger // nil discards
	// Device selects the gamepads that may take a slot: "" any; otherwise
	// a case-insensitive substring of the device name or of its DeviceKey
	// string.
	Device string
	// IgnoreDevice excludes a canonical identity from mapping, while the
	// router still observes its state and emergency combos. It must not block.
	IgnoreDevice func(input.DeviceKey) bool
	// Calibration returns the stick calibration (IN-14) of the gamepad with
	// the canonical DeviceKey string key, nil when it has none. A slot asks
	// when it gets a gamepad, at each profile Reload and at
	// RefreshCalibration, on its own goroutine, so it must not block and must
	// be safe for concurrent use; it returns what the last committed
	// config.json says. Nil means no gamepad is calibrated.
	Calibration func(key string) *profile.Calibration
	// Switches returns the global feedback switches (UI-27, ADR-0067) of every
	// slot's engine (engine.Options.Switches). It is called on the slots'
	// goroutines, concurrently, so it must not block and must be safe for
	// concurrent use; it returns what the last committed config.json says. A
	// change is applied to the haptics at once and to the layer and pause
	// state by RefreshFeedback. Nil means every switch on.
	Switches func() profile.FeedbackSwitches
	// EdgeBuffer is each slot's edge channel capacity; 0 means 256.
	EdgeBuffer int
	// Lock locks each slot loop goroutine to its OS thread
	// (slot.LoopConfig.Lock); padmated sets it.
	Lock bool
	// Trace and OnEvent, when set, see each slot's decisions and events on
	// the slot's goroutine, with its index (padmated's --decisions and
	// printer): the slots call them concurrently. They must not block.
	Trace   func(slot uint8, d engine.Decision)
	OnEvent func(slot uint8, e input.Event)
	// OnInput sees every source event on the router, including ignored and
	// waiting devices and input while mapping is held, but for DevicePower,
	// which is routing metadata that Devices reports (IN-12). It must not
	// block.
	OnInput func(e input.Event)
	// OnUnrouted, when set, is called on the router's goroutine with each
	// ButtonDown and ButtonUp of a gamepad that no slot maps, and why, one of
	// the Unrouted constants (padmated's diagnostics events, ADR-0071). A
	// press that takes a slot over (IN-05) is routed, so it is not one. It
	// must not block or call the Supervisor.
	OnUnrouted func(e input.Event, why string)
	// OnPower, when set, is called on the router's goroutine with each
	// DevicePower of a gamepad the router holds that passes the device filter
	// and the ignore setting (IN-12): padmated's low-battery tracker. It runs
	// after the router has stored the value for Devices. It must not block or
	// call the Supervisor.
	OnPower       func(u PowerUpdate)
	OnLayerChange func(slot uint8, s engine.LayerState)
	// ProfileSwitch must only enqueue: it runs on a slot owner thread.
	ProfileSwitch func(slot uint8, gen uint32, id string) error
	OnFeedback    func(slot uint8, f engine.Feedback)
	// OnFeedbackDevice is the device-scoped form used by padmated. The
	// DeviceID is zero when the slot has not been assigned yet.
	OnFeedbackDevice func(slot uint8, id input.DeviceID, f engine.Feedback)
	// OnHapticDevice receives the one-shot haptics of each slot (MAP-25,
	// HUD-10): the preset of a binding each time a gesture of it is dispatched,
	// and the tick of a knob step. padmated posts them to its effect queue. It
	// runs on the slot's goroutine, the slots call it concurrently, and the
	// DeviceID is zero when the slot has no controller. It must not block.
	OnHapticDevice func(slot uint8, id input.DeviceID, h engine.Haptic)
	// OnReload, when set, is called on each running slot's goroutine, with
	// its index, right before the slot lets go of its keys for a Reload to
	// generation gen (padmated's printer): once per running slot, and
	// concurrently. A slot that is not running then starts with the new
	// program and calls nothing. It must not block.
	OnReload func(slot uint8, gen uint32)
	// OnPause, when set, is called on the router's goroutine each time the
	// mapping is paused or resumed (DMN-06), with the new state and what
	// did it: "api" for Pause, "combo" for an input.ComboHeld event
	// (DMN-07) (padmated's printer). It must not block or call the
	// Supervisor.
	OnPause func(paused bool, by string)
	// OnSuspend, when set, is called on the router's goroutine each time
	// the reasons the router applied for Suspend change (DMN-09), with
	// the reasons before and after, once the slots have the edges of the
	// change (padmated's printer). It must not block or call the
	// Supervisor.
	OnSuspend func(was, now winsession.Reason)
	// Slots is how many slots there are, 1 to MaxSlots; 0 means MaxSlots.
	// Every slot maps with Program, and each has its own runner and slot
	// loop from Run on, with or without a gamepad.
	Slots int
}

// Status is the state of every slot, in index order, the pause and the
// suspension.
type Status struct {
	Capturing bool
	Slots     []SlotStatus
	// Paused is set while the user's pause is on (DMN-06): from when the
	// router has closed Output's gate for a pause until it opens it again.
	// A suspension still holds the mapping when it is not set.
	Paused bool
	// Suspended is the reasons the system suspends the mapping for
	// (DMN-09), as the router last applied them; none while it maps.
	Suspended winsession.Reason
}

// Mapping reports whether the mapping runs: neither paused nor suspended.
func (st Status) Mapping() bool { return !st.Paused && !st.Capturing && st.Suspended == 0 }

// SlotStatus is the state of one slot.
type SlotStatus struct {
	Index uint8
	Key   input.DeviceKey // of the assigned gamepad; zero when none
	ID    input.DeviceID  // 0 when no gamepad is assigned
	Name  string
	// Home is the key of the gamepad the slot belongs to: the assigned
	// one's, or, while Provisional, the one the slot was first handed away
	// from (a chain of hand-offs keeps it); zero when none is assigned.
	// When that gamepad reconnects, it takes a free slot if there is one,
	// and this slot's Home becomes the assigned one's (IN-08); otherwise it
	// takes this slot back.
	Home input.DeviceKey
	// Provisional is set while the assigned gamepad pressed no button on
	// the enabled slot since it got the slot, but for a take-over (IN-05):
	// a waiting gamepad's press can take the slot over.
	Provisional bool
	// Gen is the profile generation of the running slot: it changes once
	// the slot has switched for a Reload. While the slot waits to restart
	// or is disabled, it is the generation the slot starts with, which a
	// Reload changes at once.
	Gen      uint32
	Restarts int // restarts after a panic since the slot was last enabled
	Disabled bool
}

// Supervisor routes input events to the slots and supervises the slot
// loops. Its methods are safe for concurrent use.
type Supervisor struct {
	layers        chan layerRequest
	captures      chan captureRequest
	capturing     atomic.Bool
	deviceQueries chan chan []DeviceView
	cfg           Config
	log           *slog.Logger
	runners       []*runner // by slot index
	syncs         chan syncRequest
	reloads       chan reloadRequest
	pauses        chan pauseRequest
	releases      chan releaseRequest
	params        chan *paramsRequest
	notes         chan uint8    // the index of a slot its runner disabled
	fatal         chan error    // a runner that panicked; one send per runner at most
	stop          chan struct{} // closed by Run after the edge channels
	done          chan struct{} // closed when Run has returned
	started       atomic.Bool
	wg            sync.WaitGroup // the runners
	loops         sync.WaitGroup // the slot loops the runners started
	reload        sync.Mutex     // held by Reload
	emergency     sync.Mutex     // serializes out-of-band release gates
	// paused mirrors the router's user pause for Status; suspended mirrors
	// the reasons the router applied, for Status; held mirrors whether
	// either holds the mapping, for the slots the runners create: the router
	// stores them before it delivers the edges of a change, so a slot
	// created after that starts with it.
	paused    atomic.Bool
	suspended atomic.Uint32
	held      atomic.Bool

	// powers is the latest power of each connected gamepad (IN-12), by connection
	// id, an immutable table that the router replaces when one changes and the
	// slots read for pad.info.battery (powerOf).
	powers atomic.Pointer[map[input.DeviceID]input.Power]

	// The suspension (DMN-09), set by Suspend under susMu: susp holds the
	// reasons last set in its bits 0-7, and from bit 8 up counts the calls
	// that added a reason; suspSeen is every reason set since the router
	// last took them (takeSuspend); suspWake wakes the router.
	susMu    sync.Mutex
	susp     atomic.Uint64
	suspSeen winsession.Reason
	suspWake chan struct{}

	mu       sync.Mutex
	status   []SlotStatus
	sessions script.SessionStore
}

// syncRequest asks the router for the barriers of a Sync.
type syncRequest struct {
	reply                          chan []*barrier
	inspect                        bool
	script, metadata, reloadScript bool
	device                         string
	// action, when set, is run by every slot as its barrier's action
	// (RefreshCalibration).
	action func(*slot.Slot) error
}

// reloadRequest asks the router to reload prog: it answers with the
// generation it took and the slots' barriers.
type reloadRequest struct {
	prog       *engine.Program
	profileKey *string
	reply      chan reloadReply
}

type reloadReply struct {
	gen uint32
	bs  []*barrier
}

// pauseRequest asks the router to pause, or with on false to resume; it
// answers once it has closed or opened Output's gate.
type pauseRequest struct {
	on    bool
	reply chan struct{}
}

type releaseRequest struct{ reply chan []*barrier }

// New returns a Supervisor of cfg, with every slot and its runner. It
// fails when Clock, Events, Output or Program is nil, EdgeBuffer is
// negative, or Slots is not 0 to MaxSlots.
func New(cfg Config) (*Supervisor, error) {
	switch {
	case cfg.Clock == nil:
		return nil, errors.New("supervisor: no clock")
	case cfg.Events == nil:
		return nil, errors.New("supervisor: no event channel")
	case cfg.Output == nil:
		return nil, errors.New("supervisor: no output loop")
	case cfg.Program == nil:
		return nil, errors.New("supervisor: no program")
	case cfg.EdgeBuffer < 0:
		return nil, fmt.Errorf("supervisor: edge buffer %d is negative", cfg.EdgeBuffer)
	case cfg.Slots < 0 || cfg.Slots > MaxSlots:
		return nil, fmt.Errorf("supervisor: %d slots, want 1 to %d", cfg.Slots, MaxSlots)
	}
	n := cfg.Slots
	if n == 0 {
		n = MaxSlots
	}
	if cfg.EdgeBuffer == 0 {
		cfg.EdgeBuffer = defaultEdgeBuffer
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Supervisor{
		layers:        make(chan layerRequest),
		captures:      make(chan captureRequest),
		deviceQueries: make(chan chan []DeviceView),
		cfg:           cfg, log: log,
		syncs:    make(chan syncRequest),
		reloads:  make(chan reloadRequest),
		pauses:   make(chan pauseRequest),
		releases: make(chan releaseRequest),
		params:   make(chan *paramsRequest),
		notes:    make(chan uint8, n),
		suspWake: make(chan struct{}, 1),
		fatal:    make(chan error, n),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	for i := range n {
		index := uint8(i)
		s.status = append(s.status, SlotStatus{Index: index, Gen: firstGen})
		s.runners = append(s.runners, newRunner(s, index))
	}
	return s, nil
}

// Run starts every slot, routes the events to the slots and supervises the
// slot loops until ctx is done or Events is closed, which return nil, until
// Output ends, which returns an error wrapping Output.Err(), or until the
// goroutine that supervises a slot panics, which returns a *PanicError.
// Before it returns, every slot loop has released its keys through Output
// and ended (see the package doc for what the caller does next). Run may
// be called once.
func (s *Supervisor) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("supervisor: Run called twice")
	}
	// The first slots start held by a Suspend before Run; the router's
	// first syncSuspend delivers the edges.
	s.held.Store(s.paused.Load() || s.level() != 0)
	for _, r := range s.runners {
		s.wg.Go(r.supervise)
	}
	// Also after a panic of the router: the slot loops release and end.
	defer func() {
		// The router has returned, so nothing sends on the edge channels.
		for _, r := range s.runners {
			close(r.edges)
		}
		close(s.stop)
		s.wg.Wait()
		s.loops.Wait() // also those of a runner that panicked
		close(s.done)
	}()
	return newRouter(s).run(ctx)
}

// Sync returns once every event sent on Config.Events before Sync was
// called, the ones still queued included, has been handled by its slot,
// and the output commands that caused have run on Output: a barrier for
// tests. It sends a barrier to every slot and waits for them all. A slot
// that is behind is caught up first; a disabled slot has nothing to wait
// for, so its barrier finishes at once; a slot that waits to restart
// holds Sync until it runs again. Before Run starts, Sync waits. It
// returns ctx.Err(), ErrStopped once Run has ended, or, when Output has
// ended, the error of Output.Do (wrapping output.ErrLoopClosed).
func (s *Supervisor) Sync(ctx context.Context) error {
	_, err := s.sync(ctx)
	return err
}

// sync is Sync, returning the slots' barriers.
func (s *Supervisor) sync(ctx context.Context) ([]*barrier, error) {
	return s.syncWithViews(ctx, false)
}

func (s *Supervisor) syncWithViews(ctx context.Context, inspect bool) ([]*barrier, error) {
	return s.syncRequest(ctx, syncRequest{inspect: inspect})
}

func (s *Supervisor) syncRequest(ctx context.Context, req syncRequest) ([]*barrier, error) {
	req.reply = make(chan []*barrier, 1)
	select {
	case s.syncs <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, ErrStopped
	}
	var bs []*barrier
	select {
	case bs = <-req.reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, ErrStopped
	}
	if err := s.awaitAll(ctx, bs, "sync"); err != nil {
		return nil, err
	}
	return bs, nil
}

// Reload switches every slot, a free one too, to prog, the next profile
// generation (PRO-03), one for all slots, and returns that generation.
// For each slot it is a ReleaseAll (slot.Slot.Reload): the engine ends
// every press cycle, the slot lets go of all its keys, of every
// generation, and its taps, and the controls down stay down and do
// nothing until pressed again; later presses map with prog and own their
// keys with the new generation. The events sent on Config.Events before
// Reload was called, the ones still queued included, are handled with the
// program before, as for Sync, and those sent after it returned with
// prog. A slot that is behind is caught up first, with the program
// before: its Resync also covers the presses skipped until then, which
// the switch then cancels. A slot that waits to restart, or panics before
// it switches, or is disabled starts with prog when it runs again, with
// nothing to let go; the edges queued for it meanwhile, those sent before
// Reload included, map with prog.
//
// Reload returns once every running slot has switched, each calling
// Config.OnReload first on its own goroutine, and the releases have run
// on Output. Reloads run one at a time; before Run starts, Reload waits.
// Once the router has taken the reload, every slot switches, and the
// generation is spent, even when ctx ends first: Reload then returns
// the generation with ctx.Err(). It returns an error for a nil prog,
// ErrStopped once Run has ended, or the error of Output.Do. It must not
// be called from a slot's goroutine (Config's Trace, OnEvent and
// OnReload), since it waits for the slots.
func (s *Supervisor) Reload(ctx context.Context, prog *engine.Program) (uint32, error) {
	return s.reloadProfile(ctx, prog, nil)
}

// ReloadProfile switches the profile and its session identity atomically on
// every slot. Reload keeps the existing key, even if its display name changes.
func (s *Supervisor) ReloadProfile(ctx context.Context, prog *engine.Program, key string) (uint32, error) {
	return s.reloadProfile(ctx, prog, &key)
}

func (s *Supervisor) reloadProfile(ctx context.Context, prog *engine.Program, key *string) (uint32, error) {
	if prog == nil {
		return 0, errors.New("supervisor: Reload needs a program")
	}
	s.reload.Lock()
	defer s.reload.Unlock()
	gen, bs, err := s.startReloadProfile(ctx, prog, key)
	if err != nil {
		return gen, err
	}
	return gen, s.awaitAll(ctx, bs, "reload")
}

// startReload hands prog to the router and returns the generation it took
// and the slots' barriers.
func (s *Supervisor) startReload(ctx context.Context, prog *engine.Program) (uint32, []*barrier, error) {
	return s.startReloadProfile(ctx, prog, nil)
}

func (s *Supervisor) startReloadProfile(ctx context.Context, prog *engine.Program, key *string) (uint32, []*barrier, error) {
	req := reloadRequest{prog: prog, profileKey: key, reply: make(chan reloadReply, 1)}
	select {
	case s.reloads <- req:
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-s.done:
		return 0, nil, ErrStopped
	}
	// The router answers without waiting for anything, and the reload goes
	// on whatever ctx does.
	select {
	case rep := <-req.reply:
		return rep.gen, rep.bs, nil
	case <-s.done:
		return 0, nil, ErrStopped // Events closed before the router answered
	}
}

// Pause pauses the mapping of every slot, or with on false resumes it
// (DMN-06); a Pause to the state the mapping is in does nothing. The
// events sent on Config.Events before Pause was called, the ones still
// queued included, are routed first, as for Sync. When Pause returns,
// Output has let go of every key and drops every press
// (output.Loop.BlockFor with output.BlockUser), so no slot presses
// anything until a resume, and Config.OnPause has been called. The slots
// follow without Pause waiting for them: each pauses in order with its
// edges (slot.Slot.SetPaused), one that is behind with its Resync, and one
// that waits to restart or is disabled starts paused. A resume opens the
// user's gate and presses nothing again: a control held across it does
// nothing until it is pressed again. The pause lasts across Reload,
// resets, reconnects and restarts, until a resume or the end of Run.
//
// The pause is the user's, apart from the system's suspension (Suspend):
// the mapping is held while either is on. A pause during a suspension
// changes nothing but Status.Paused and the user's gate, and a resume
// during one returns with Status.Paused false while the slots stay paused
// and Output still drops every press, until the suspension ends too.
//
// Before Run starts, Pause waits. Once the router has taken the request,
// the pause is done whatever ctx does. It returns ctx.Err(), ErrStopped
// once Run has ended, or the error of Output.Do.
func (s *Supervisor) Pause(ctx context.Context, on bool) error {
	req := pauseRequest{on: on, reply: make(chan struct{}, 1)}
	select {
	case s.pauses <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStopped
	}
	// The router answers without waiting for anything.
	select {
	case <-req.reply:
	case <-s.done:
		return ErrStopped // Events closed before the router answered
	}
	// A Do that starts after Block runs after its release.
	if err := s.cfg.Output.Do(func(*output.Arbiter) error { return nil }); err != nil {
		return fmt.Errorf("supervisor: pause: %w", err)
	}
	return nil
}

// Suspend sets the reasons the system suspends the mapping for (DMN-09,
// ADR-0026), none to end the suspension; bits outside
// winsession.AllReasons are ignored. The mapping is held while it is
// suspended or paused (Pause): the two are apart, and neither ends the
// other.
//
// Suspend never waits, so it may be called from a thread that must not
// block (T-session's window procedure), from any goroutine, before Run
// (the slots then start held) and after Run has ended (it then does
// nothing but close the gate). When r adds a reason, it closes Output's
// system gate at once (output.Loop.BlockFor with output.BlockSystem):
// Output lets go of every key and drops every press, whatever the router
// is doing. The router applies the rest when it next looks, at the
// latest before the next Sync, Reload or Pause goes on: it holds every
// slot, as a pause does, and once the reasons are none it routes the
// events already queued (held, so they do nothing), lets the slots go
// and, only after every slot has taken that (a barrier on each, as for
// Sync), opens the system gate. A slot that is behind or waits to
// restart so keeps the gate closed for all until it has caught up. A
// Suspend that adds a reason after that closes the gate again, and an
// open that comes too late then does nothing (openSystem).
//
// Reasons set and cleared again before the router looked still hold the
// slots for that moment: every press cycle they are in is canceled, as
// for a pause and a resume.
func (s *Supervisor) Suspend(r winsession.Reason) {
	s.suspend(r)
	select {
	case s.suspWake <- struct{}{}:
	default: // the router has a wake coming
	}
}

// ReleaseAll closes an independent output gate, releases every keyboard
// reference, then invalidates old script work on each slot before opening the
// gate. Closing the gate also releases the keys the startup sweep of the
// held-key log could not (output.Loop.BlockFor, OUT-14). It does not change the router's pause or suspension state. A fresh
// press after this call starts a new script runtime asynchronously.
func (s *Supervisor) ReleaseAll() error {
	s.emergency.Lock()
	defer s.emergency.Unlock()
	s.cfg.Output.BlockFor(output.BlockEmergency, true)
	gateClosed := true
	defer func() {
		if gateClosed {
			s.cfg.Output.BlockFor(output.BlockEmergency, false)
		}
	}()
	releaseErr := s.cfg.Output.Do(func(a *output.Arbiter) error { return a.ReleaseAll() })
	if !s.started.Load() {
		return releaseErr
	}
	req := releaseRequest{reply: make(chan []*barrier, 1)}
	select {
	case s.releases <- req:
	case <-s.done:
		return errors.Join(releaseErr, ErrStopped)
	}
	var bs []*barrier
	select {
	case bs = <-req.reply:
	case <-s.done:
		return errors.Join(releaseErr, ErrStopped)
	}
	if err := s.awaitAll(context.Background(), bs, "release-all"); err != nil {
		return errors.Join(releaseErr, err)
	}
	var errs []error
	for _, b := range bs {
		errs = append(errs, b.err)
	}
	s.cfg.Output.BlockFor(output.BlockEmergency, false)
	gateClosed = false
	return errors.Join(releaseErr, errors.Join(errs...), s.cfg.Output.Do(func(*output.Arbiter) error { return nil }))
}

// suspend is Suspend without waking the router.
func (s *Supervisor) suspend(r winsession.Reason) {
	r &= winsession.AllReasons
	s.susMu.Lock()
	defer s.susMu.Unlock()
	old := s.susp.Load()
	added := r &^ winsession.Reason(old&0xff)
	n := old >> 8
	if added != 0 {
		n++
	}
	s.susp.Store(n<<8 | uint64(r))
	s.suspSeen |= r
	if added != 0 {
		// A CAS and a send that does not wait, so fine under susMu.
		s.cfg.Output.BlockFor(output.BlockSystem, true)
	}
}

// level is the reasons last set by Suspend.
func (s *Supervisor) level() winsession.Reason {
	return winsession.Reason(s.susp.Load() & 0xff)
}

// takeSuspend returns the suspension (susp) and every reason set since
// the last call, and starts the next set with the reasons now set.
func (s *Supervisor) takeSuspend() (v uint64, seen winsession.Reason) {
	s.susMu.Lock()
	defer s.susMu.Unlock()
	v, seen = s.susp.Load(), s.suspSeen
	s.suspSeen = winsession.Reason(v & 0xff)
	return v, seen
}

// openSystem opens Output's system gate if the suspension is still v and
// sets no reason: under susMu, the lock that suspend closes it under, so a
// newer Suspend's close always wins.
func (s *Supervisor) openSystem(v uint64) {
	s.susMu.Lock()
	defer s.susMu.Unlock()
	if s.susp.Load() == v && v&0xff == 0 {
		s.cfg.Output.BlockFor(output.BlockSystem, false)
	}
}

// awaitAll waits for the barriers bs, then for Output to run the commands
// that came before; what names the call in an error of Output.
func (s *Supervisor) awaitAll(ctx context.Context, bs []*barrier, what string) error {
	for _, b := range bs {
		select {
		case <-b.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return ErrStopped
		}
	}
	if err := s.cfg.Output.Do(func(*output.Arbiter) error { return nil }); err != nil {
		return fmt.Errorf("supervisor: %s: %w", what, err)
	}
	return nil
}

// Status returns a copy of the slots' status, one per slot in index
// order, the pause and the suspension.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Capturing: s.capturing.Load(),
		Slots:     slices.Clone(s.status),
		Paused:    s.paused.Load(),
		Suspended: winsession.Reason(s.suspended.Load()),
	}
}

// update changes the status of slot i under the lock.
func (s *Supervisor) update(i uint8, f func(*SlotStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.status[i])
}

// barrier is one slot's part of a Sync or a Reload, or of the router's
// opening of the system gate: a job that the slot loop runs after the
// edges sent before it (slot.RunLoop).
type barrier struct {
	done chan struct{}
	once sync.Once
	// after, when set, runs when the barrier finishes, before done is
	// closed (openGroup).
	after func()
	// A Reload's: the program and generation the slot switches to; prog is
	// nil for a Sync.
	prog       *engine.Program
	gen        uint32
	profileKey string
	// Set by the router before it posts the barrier to the runner.
	assigned bool
	want     slot.State // the router's state of the assigned gamepad
	// Set on the slot loop before done is closed.
	ran        bool
	got        slot.State
	action     func(*slot.Slot) error
	inspect    bool
	view       slot.View
	scriptView *slot.ScriptView
	err        error
}

func newBarrier() *barrier { return &barrier{done: make(chan struct{})} }

// isReload reports whether b is a Reload's, not a Sync's.
func (b *barrier) isReload() bool { return b.prog != nil }

// run is the job of a Sync.
func (b *barrier) run(s *slot.Slot) {
	if b.action != nil {
		b.err = b.action(s)
	}
	if b.inspect {
		b.view = s.View()
	}
	b.got, b.ran = s.State(), true
	b.finish()
}

// finish releases the Sync; only the first call counts.
func (b *barrier) finish() {
	b.once.Do(func() {
		if b.after != nil {
			b.after()
		}
		close(b.done)
	})
}

func (b *barrier) finished() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// openGroup opens the system gate once every slot has finished its
// barrier of the group, posted behind the edges that let the slots go
// when a suspension ended; v is the suspension then.
type openGroup struct {
	s *Supervisor
	v uint64
	n atomic.Int32 // the barriers not finished yet
}

// one is the after of each barrier of the group.
func (g *openGroup) one() {
	if g.n.Add(-1) == 0 {
		g.s.openSystem(g.v)
	}
}
