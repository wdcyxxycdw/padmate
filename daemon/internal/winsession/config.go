package winsession

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// Defaults of the Config durations.
const (
	// DefaultPoll is how often the session thread's timer checks the
	// desktop, and the session when it needs to (Tracker.Step, SigTick).
	DefaultPoll = 2 * time.Second
	// DefaultBarrierWait bounds how long the handlers of PBT_APMSUSPEND,
	// WM_QUERYENDSESSION and WM_ENDSESSION wait for Config.Barrier.
	DefaultBarrierWait = 1 * time.Second
	// DefaultEndWait bounds how long the WM_ENDSESSION handler waits for
	// Close after Config.OnStop. The system ends the process after its
	// handler returns, or kills it after about 5 s.
	DefaultEndWait = 3 * time.Second
)

// ErrActive is returned by Start while another Watcher of this process has
// not ended; a process has one session window.
var ErrActive = errors.New("winsession: a watcher is already running")

// Config configures Start. Every field is optional.
type Config struct {
	// Logger receives the watcher's log. nil discards it.
	Logger *slog.Logger
	// Focus optionally observes foreground executables on this same thread.
	Focus *winfocus.Config
	// OnChange is called with every Change that changed the reasons, in
	// order: on the session thread, and after the thread ended by itself
	// on the fallback's goroutine (Watcher.Degraded). Its Was is the
	// previous call's Now, none before the first call. The first call, for
	// the SigInit step, returns before Start does. It must not block or
	// call Close; padmated passes the reasons on to Supervisor.Suspend,
	// which never waits.
	OnChange func(Change)
	// OnSignal receives each raw session/power signal before applying it or
	// posting its recheck, including repeated sleep, lock and wake signals.
	// It must promptly return without waiting or calling Close.
	OnSignal func(Signal)
	// OnWake receives each successful current wake recheck, even when
	// reasons did not change. Failed queries or queries superseded by a raw
	// signal do not notify it. It runs after OnChange and must not wait or
	// call Close.
	OnWake func(Change)
	// OnStop is called on the session thread when the process should stop:
	// "session-end" when the session ends (WM_ENDSESSION with TRUE), and
	// "close" for WM_CLOSE. It must not block or call Close; the caller
	// stops and then calls Close, which the WM_ENDSESSION handler waits
	// for up to EndWait.
	OnStop func(cause string)
	// Barrier, when not nil, returns once the output has sent what the
	// reasons so far asked for (padmated: a Loop.Do behind the release).
	// The sleep and session-end handlers call it, bounded by BarrierWait,
	// before they return to the system, which then sleeps or ends the
	// session. It runs on its own goroutine, so it may block past ctx.
	Barrier func(ctx context.Context) error
	// Query reads the system's state. nil means the system's: the
	// session's WTSINFOEX and the input desktop.
	Query Querier
	// Poll is the timer's period; 0 means DefaultPoll.
	Poll time.Duration
	// StaleAfter is Tracker's; 0 means DefaultStaleAfter.
	StaleAfter time.Duration
	// BarrierWait bounds Barrier; 0 means DefaultBarrierWait.
	BarrierWait time.Duration
	// EndWait bounds the wait for Close at session end; 0 means
	// DefaultEndWait.
	EndWait time.Duration

	// Test seams, set through export_windows_test.go. They use only
	// portable types, so that Config builds everywhere.

	// register replaces WTSRegisterSessionNotification(hwnd).
	register func(hwnd uintptr) error
	// awake replaces the awake time (QueryUnbiasedInterruptTime).
	awake func() time.Duration
	// dispatched is called after each message the session thread
	// dispatched; an error or a panic ends the thread as a failure would.
	dispatched func(msg uint32, wParam uintptr) error
	// trace is told the result of each registration and teardown call:
	// "wts-register", "wts-unregister", "power-unregister", "hook-unhook",
	// "timer-kill", "ticker-stop" and "window-destroy".
	trace func(op string, err error)
	// noHook skips SetWinEventHook, so that the real machine's desktop
	// switches post nothing to a test that counts the queries.
	noHook bool
	// noTimer fails SetTimer, so that the goroutine ticks instead.
	noTimer bool
}

// withDefaults returns c with the zero durations and the nil Logger
// replaced.
func (c Config) withDefaults() Config {
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.Poll <= 0 {
		c.Poll = DefaultPoll
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = DefaultStaleAfter
	}
	if c.BarrierWait <= 0 {
		c.BarrierWait = DefaultBarrierWait
	}
	if c.EndWait <= 0 {
		c.EndWait = DefaultEndWait
	}
	return c
}
