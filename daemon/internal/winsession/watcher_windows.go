package winsession

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// Window messages and styles of the session window.
const (
	wmNull  = 0x0000 // WM_NULL
	wmClose = 0x0010 // WM_CLOSE
	wmTimer = 0x0113 // WM_TIMER
	wmApp   = 0x8000 // WM_APP

	// The session window's private messages. WM_APP+8 and up, and timer
	// IDs 2 and up, are left to the foreground watcher (winfocus, M8),
	// which will share this window.
	wmDesktopSwitch = wmApp + 1 // the WinEvent callback: the input desktop changed
	wmStop          = wmApp + 2 // Close: end the message loop
	wmRecheck       = wmApp + 3 // clearing Signal in wParam; reason Gen and wake ID in lParam

	timerTick = 1 // the ID of the Poll timer

	wsExToolWindow = 0x00000080 // WS_EX_TOOLWINDOW: no taskbar button
	wsPopup        = 0x80000000 // WS_POPUP

	notifyForThisSession     = 0      // NOTIFY_FOR_THIS_SESSION
	deviceNotifyWindowHandle = 0      // DEVICE_NOTIFY_WINDOW_HANDLE
	eventSystemDesktopSwitch = 0x0020 // EVENT_SYSTEM_DESKTOPSWITCH
	wineventOutOfContext     = 0      // WINEVENT_OUTOFCONTEXT

	userTimerMinimum = 0x0000000A // USER_TIMER_MINIMUM, in ms
	userTimerMaximum = 0x7FFFFFFF // USER_TIMER_MAXIMUM, in ms

	// stopTries and stopRetry bound Close's PostMessageW of wmStop, which
	// fails while the thread's queue is full (ERROR_NOT_ENOUGH_QUOTA).
	stopTries = 50
	stopRetry = 10 * time.Millisecond
)

// className is the session window's class. Every Start registers it
// again, on the session thread: Windows destroys a process's window
// classes when its last GUI thread exits, which the session thread may be
// (TestSessionClassLastGUIThread; in cmd/padmated's tests the other GUI
// threads were SDL's and T-input, which end with the SDL input). It is
// never unregistered: it holds the window procedure's callback, which
// lives as long as the process anyway.
const className = "padmate.winsession"

// msg is the Win32 MSG structure.
type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       struct{ x, y int32 }
	lPrivate uint32
}

// wndClassEx is the Win32 WNDCLASSEXW structure.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

var (
	// The process-wide setup: the two callbacks (NewCallback allocations
	// are never freed and limited in number, CLAUDE.md invariant 5), the
	// module handle and the class name. It runs once; its error sticks.
	setupOnce  sync.Once
	setupErr   error
	wndProcCB  uintptr
	winEventCB uintptr
	instance   windows.Handle
	classUTF16 *uint16

	// startMu serializes Start so that at most one Watcher runs.
	startMu sync.Mutex
	// active is the Watcher the callbacks serve, or nil. It is set until
	// Close has ended everything, including a fallback.
	active atomic.Pointer[Watcher]
)

func setup() {
	wndProcCB = windows.NewCallback(wndProc)
	winEventCB = windows.NewCallback(winEventProc)
	if err := windows.GetModuleHandleEx(0, nil, &instance); err != nil {
		setupErr = fmt.Errorf("winsession: GetModuleHandleEx: %w", err)
		return
	}
	name, err := windows.UTF16PtrFromString(className)
	if err != nil {
		setupErr = fmt.Errorf("winsession: class name: %w", err)
		return
	}
	classUTF16 = name
}

// registerClass registers the session window's class, unless it is
// registered (see className).
func registerClass() error {
	wc := wndClassEx{
		wndProc:   wndProcCB,
		instance:  instance,
		className: classUTF16,
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if atom, err := registerClassEx(&wc); atom == 0 && !errors.Is(err, windows.ERROR_CLASS_ALREADY_EXISTS) {
		return fmt.Errorf("winsession: RegisterClassExW: %w", err)
	}
	return nil
}

// Start starts the session thread T-session: a hidden top-level window
// that receives the session's, the power's and the end of the session's
// messages, a WinEvent hook for desktop switches and a Poll timer. It
// returns after the SigInit step, which reads the reasons from the system,
// and its OnChange. It returns ErrActive while another Watcher of this
// process has not been closed.
//
// A failure after the window exists does not fail Start: the session
// notifications are retried on every tick, the power notifications and the
// WinEvent hook are optional (the tick and the queries cover the hook; the
// messages the power notifications add on Modern Standby are not
// verified), and a goroutine posts the ticks when SetTimer fails. Only the
// window, the class and the callbacks are required.
func Start(cfg Config) (*Watcher, error) {
	startMu.Lock()
	defer startMu.Unlock()
	if active.Load() != nil {
		return nil, ErrActive
	}
	setupOnce.Do(setup)
	if setupErr != nil {
		return nil, setupErr
	}
	w := newWatcher(cfg)
	if w.cfg.register == nil {
		w.cfg.register = func(hwnd uintptr) error {
			return wtsRegisterSessionNotification(windows.HWND(hwnd), notifyForThisSession)
		}
	}
	if w.cfg.awake == nil {
		w.cfg.awake = unbiasedAwake
	}
	if w.q == nil {
		w.q = newSystemQuerier(w.log)
	}
	active.Store(w)
	ready := make(chan error, 1)
	go w.run(ready)
	if err := <-ready; err != nil {
		<-w.threadDone
		active.CompareAndSwap(w, nil)
		return nil, err
	}
	return w, nil
}

// threadState is what the session thread created, for its teardown.
type threadState struct {
	hwnd   windows.HWND
	power  windows.Handle // RegisterSuspendResumeNotification's, or 0
	hook   windows.Handle // SetWinEventHook's, or 0
	timer  bool           // SetTimer succeeded
	ticker func()         // stops the goroutine that ticks instead, or nil
}

// run is the session thread. It stays locked to its OS thread for its
// whole life, so the thread exits with the goroutine: the window, the hook
// and the timer belong to it.
func (w *Watcher) run(ready chan<- error) {
	runtime.LockOSThread()
	var s threadState
	started := false
	var err error
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("winsession: the session thread panicked: %v", p)
			w.log.Error("session thread panicked", "panic", p, "stack", string(debug.Stack()))
		}
		w.teardown(&s)
		if !started {
			if err == nil {
				err = errors.New("winsession: the session thread ended before it started")
			}
			close(w.threadDone)
			ready <- err
			return
		}
		w.ended(err)
	}()
	if err = w.setUp(&s); err != nil {
		return
	}
	w.initial()
	started = true
	ready <- nil
	err = w.loop()
}

// setUp registers the class, creates the window and asks for the
// notifications. Only a failure to register the class or to create the
// window is returned.
func (w *Watcher) setUp(s *threadState) error {
	if err := registerClass(); err != nil {
		return err
	}
	hwnd, err := createWindowEx(wsExToolWindow, classUTF16, nil, wsPopup, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("winsession: CreateWindowExW: %w", err)
	}
	s.hwnd = hwnd
	w.hwnd = uintptr(hwnd)

	w.registerNotifications()
	if h, err := registerSuspendResumeNotification(hwnd, deviceNotifyWindowHandle); h != 0 {
		s.power = h
	} else {
		w.log.Debug("session power notifications unavailable", "err", err)
	}
	if !w.cfg.noHook {
		if s.hook = setWinEventHook(eventSystemDesktopSwitch, eventSystemDesktopSwitch, 0, winEventCB, 0, 0, wineventOutOfContext); s.hook == 0 {
			w.log.Debug("session desktop hook unavailable; the timer checks the desktop")
		}
	}
	if err := w.setTimer(hwnd); err == nil {
		s.timer = true
	} else {
		w.log.Warn("session timer unavailable; ticking from a goroutine", "err", err)
		s.ticker = w.startTicker(hwnd)
	}
	// The first PostMessageW finds the function in user32, which
	// allocates: here rather than in the WinEvent callback.
	if err := postMessage(hwnd, wmNull, 0, 0); err != nil {
		w.log.Debug("session warm-up not posted", "err", err)
	}
	if w.cfg.Focus != nil {
		focus, err := winfocus.Attach(uintptr(hwnd), *w.cfg.Focus)
		if err != nil {
			w.log.Warn("foreground watcher unavailable", "err", err)
		} else {
			w.focus = focus
		}
	}
	return nil
}

// newMsg returns a MSG on the heap. loop's MSG must not live on the
// goroutine stack: GetMessageW keeps the pointer while it calls the window
// procedure for sent messages, whose Go code may grow the stack, which
// moves it; GetMessageW would then write the next message into the freed
// old stack, and the loop would dispatch the previous one again (seen in
// TestSessionMessages). SyscallN keeps its uintptr arguments alive but does
// not make them escape, and a pointer a function that is not inlined
// returns always points to the heap.
//
//go:noinline
func newMsg() *msg { return new(msg) }

// timerMS returns d in the milliseconds SetTimer takes.
func timerMS(d time.Duration) uint32 {
	return uint32(min(max(d.Milliseconds(), userTimerMinimum), userTimerMaximum))
}

// setTimer starts the Poll timer of the window.
func (w *Watcher) setTimer(hwnd windows.HWND) error {
	if w.cfg.noTimer {
		return errors.New("winsession: SetTimer disabled by the test")
	}
	if id, err := setTimer(hwnd, timerTick, timerMS(w.cfg.Poll), 0); id == 0 {
		return fmt.Errorf("winsession: SetTimer: %w", err)
	}
	return nil
}

// startTicker stands in for a SetTimer that failed: a goroutine posts the
// window the timer's WM_TIMER every Poll, with no TimerProc, until the
// returned function, which waits for it, stops it. The teardown stops it
// before it destroys the window, whose handle could otherwise be reused
// by then. A full queue drops a tick, as it would coalesce the timer's.
func (w *Watcher) startTicker(hwnd windows.HWND) (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(time.Duration(timerMS(w.cfg.Poll)) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				_ = postMessage(hwnd, wmTimer, timerTick, 0)
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}

// initial steps SigInit with the Querier. A panic, which only a Querier
// can cause here (notify recovers OnChange's), is logged like a handler's.
func (w *Watcher) initial() {
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	w.step(SigInit, w.q)
}

// loop pumps the thread's messages until WM_QUIT. The system calls the
// window procedure for sent messages from inside GetMessageW; posted ones
// come out of it and are dispatched. There is no TranslateMessage: the
// window takes no keyboard input.
func (w *Watcher) loop() error {
	m := newMsg()
	for {
		r, err := getMessage(m, 0, 0, 0)
		switch r {
		case 0:
			return nil // WM_QUIT
		case -1:
			return fmt.Errorf("winsession: GetMessageW: %w", err)
		}
		dispatchMessage(m)
		if w.cfg.dispatched != nil {
			if err := w.cfg.dispatched(m.message, m.wParam); err != nil {
				return err
			}
		}
	}
}

// teardown undoes setUp in the order ADR-0026 fixes: the session
// notifications before the window they are registered for, and the window
// last. A failure is logged at Debug: nothing can be done about it.
func (w *Watcher) teardown(s *threadState) {
	if s.hwnd == 0 {
		return
	}
	if w.focus != nil {
		w.cleanup("focus-close", w.focus.Close())
		w.focus = nil
	}
	if w.registered {
		w.cleanup("wts-unregister", wtsUnRegisterSessionNotification(s.hwnd))
		w.registered = false
	}
	if s.power != 0 {
		w.cleanup("power-unregister", unregisterSuspendResumeNotification(s.power))
	}
	if s.hook != 0 {
		var err error
		if !unhookWinEvent(s.hook) {
			err = errors.New("winsession: UnhookWinEvent failed")
		}
		w.cleanup("hook-unhook", err)
	}
	if s.timer {
		w.cleanup("timer-kill", killTimer(s.hwnd, timerTick))
	}
	if s.ticker != nil {
		s.ticker()
		w.cleanup("ticker-stop", nil)
	}
	w.cleanup("window-destroy", destroyWindow(s.hwnd))
}

func (w *Watcher) cleanup(op string, err error) {
	if w.cfg.trace != nil {
		w.cfg.trace(op, err)
	}
	if err != nil {
		w.log.Debug("session cleanup failed", "op", op, "err", err)
	}
}

// finish marks everything ended after Close: a later Start may run.
func (w *Watcher) finish() {
	active.CompareAndSwap(w, nil)
	close(w.done)
}

// stop asks the session thread to end, unless it has already, and waits.
func (w *Watcher) stop() error {
	var postErr error
	for range stopTries {
		select {
		case <-w.threadDone:
			return w.wait(nil)
		default:
		}
		if postErr = postMessage(windows.HWND(w.hwnd), wmStop, 0, 0); postErr == nil {
			break
		}
		time.Sleep(stopRetry)
	}
	return w.wait(postErr)
}

// wndProc is the window procedure of the class. It serves only the active
// Watcher's window; the messages of its creation and destruction, which
// come before w.hwnd is set or after the loop ended, go to DefWindowProcW.
func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if w := active.Load(); w != nil && hwnd != 0 && hwnd == w.hwnd {
		return w.handle(uint32(message), wParam, lParam)
	}
	return defWindowProc(windows.HWND(hwnd), uint32(message), wParam, lParam)
}

// winEventProc is the WinEventProc of EVENT_SYSTEM_DESKTOPSWITCH. It only
// posts to the session window and returns: the query runs in the posted
// message. WINEVENT_OUTOFCONTEXT calls it on the session thread, from
// inside GetMessageW.
func winEventProc(hook, event, hwnd, idObject, idChild, thread, ms uintptr) uintptr {
	if w := active.Load(); w != nil && w.hwnd != 0 {
		// A full queue drops the switch; the timer checks the desktop too.
		_ = postMessage(windows.HWND(w.hwnd), wmDesktopSwitch, 0, 0)
	}
	return 0
}

// handle is the session window's procedure. The signals that set a reason
// step inline, without queries, so the release waits for no RPC; the
// signals that clear one, and the desktop and the timer, query only from
// posted messages, never inside a message another thread sent (and waits
// for). A panic is recovered and logged; the return value is then TRUE
// for WM_POWERBROADCAST and WM_QUERYENDSESSION and 0 otherwise, so the
// handlers never veto sleep or the end of the session.
func (w *Watcher) handle(message uint32, wParam, lParam uintptr) (ret uintptr) {
	if message == WMPowerBroadcast || message == WMQueryEndSession {
		ret = 1
	}
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	if w.focus != nil && w.focus.Handle(message, wParam, lParam) {
		return 0
	}
	switch message {
	case WMWTSSessionChange, WMPowerBroadcast:
		sig, ok := FromMessage(message, wParam, lParam)
		if ok {
			w.rawSignal(sig)
		}
		switch {
		case !ok:
		case sig.Clears():
			w.recheck(sig)
		default:
			w.step(sig, nil)
			if sig == SigSleep {
				w.barrier(sig)
			}
		}
		return ret
	case WMQueryEndSession:
		w.rawSignal(SigQueryEnd)
		w.step(SigQueryEnd, nil)
		w.barrier(SigQueryEnd)
		return ret
	case WMEndSession:
		if wParam == 0 {
			w.rawSignal(SigEndCanceled)
			w.recheck(SigEndCanceled)
		} else {
			w.rawSignal(SigEnd)
			w.end(lParam)
		}
		return 0
	case wmClose:
		// Not DefWindowProcW, which would destroy the window.
		w.stopFor("close")
		return 0
	case wmDesktopSwitch:
		if w.posted(message, wParam, lParam) {
			w.step(SigDesktopSwitch, w.q)
		}
		return 0
	case wmRecheck:
		if sig := Signal(wParam); sig.Clears() && w.posted(message, wParam, lParam) {
			w.recheckStep(sig, uint32(lParam), uint32(uint64(lParam)>>32))
		}
		return 0
	case wmTimer:
		if wParam != timerTick {
			break
		}
		if w.posted(message, wParam, lParam) {
			w.tick()
		}
		return 0
	case wmStop:
		// Only Close's: anyone may post to a top-level window.
		if w.isClosing() {
			postQuitMessage(0)
		}
		return 0
	}
	return defWindowProc(windows.HWND(w.hwnd), message, wParam, lParam)
}

// tick is the timer: it retries the session notifications while they are
// not registered, then steps SigTick with the Querier. The Tracker is told
// of a late registration only after this tick's step, which queries the
// session because it still counts as unregistered: a lock between the
// previous query and the registration is not missed.
func (w *Watcher) tick() {
	late := !w.registered && w.registerNotifications()
	w.step(SigTick, w.q)
	if late {
		w.tracker.SetRegistered(true)
	}
	if w.focus != nil {
		w.focus.Poll()
	}
}

// registerNotifications asks for the session notifications and reports
// whether that succeeded. A failure (typically RPC_S_INVALID_BINDING early
// at sign-in, before the session manager is ready) tells the Tracker, so
// that every tick queries the session; the caller tells it of a success
// once a query has followed.
func (w *Watcher) registerNotifications() bool {
	w.attempts++
	err := w.cfg.register(w.hwnd)
	if w.cfg.trace != nil {
		w.cfg.trace("wts-register", err)
	}
	if err == nil {
		w.registered = true
		if w.attempts > 1 {
			w.log.Info("session notifications registered", "attempts", w.attempts)
		}
		return true
	}
	w.tracker.SetRegistered(false)
	if w.attempts == 1 {
		w.log.Warn("session notifications unavailable; retrying", "err", err)
	} else {
		w.log.Debug("session notifications unavailable; retrying", "err", err, "attempts", w.attempts)
	}
	return false
}

// recheck posts a clearing signal to the window, with the generation of
// its reason, and the handler steps it with queries (Tracker.Recheck): a
// lock, sleep or end of the session handled in between, sent or posted,
// is not undone. If the post fails (a full queue), the signal is applied
// now without queries: the reason is cleared unchecked, and the timer's
// next session query corrects a wrong unlock or connect.
func (w *Watcher) recheck(sig Signal) {
	payload := uint64(w.tracker.Gen(sig))
	if sig == SigWake {
		payload |= uint64(w.wakeID) << 32
	}
	if err := postMessage(windows.HWND(w.hwnd), wmRecheck, uintptr(sig), uintptr(payload)); err != nil {
		w.log.Debug("session recheck not posted; applied unchecked", "signal", sig, "err", err)
		w.step(sig, nil)
	}
}

// posted reports whether the message being handled was posted, not sent
// by another thread; a sent one is posted again, so that its queries do
// not keep the sender waiting. If that post fails, the message is dropped:
// the reasons stay as they are until the next tick or recheck, the safe
// side for held keys.
func (w *Watcher) posted(message uint32, wParam, lParam uintptr) bool {
	if !inSendMessage() {
		return true
	}
	if err := postMessage(windows.HWND(w.hwnd), message, wParam, lParam); err != nil {
		w.log.Debug("session message not posted again; dropped", "msg", message, "err", err)
	}
	return false
}

// end handles WM_ENDSESSION with TRUE: the session ends, and the system
// may end the process any time after this returns. So it releases (End,
// then the barrier), asks the process to stop, and waits for Close up to
// EndWait, so that the process's whole exit path runs before it returns.
func (w *Watcher) end(lParam uintptr) {
	w.log.Info("session ending", "kind", EndKind(lParam))
	w.step(SigEnd, nil)
	w.barrier(SigEnd)
	w.stopFor("session-end")
	t := time.NewTimer(w.cfg.EndWait)
	defer t.Stop()
	select {
	case <-w.closing:
	case <-t.C:
		w.log.Warn("session end wait timed out", "wait", w.cfg.EndWait)
	}
}

// stopFor calls OnStop, recovering a panic.
func (w *Watcher) stopFor(cause string) {
	if w.cfg.OnStop == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	w.cfg.OnStop(cause)
}

// unbiasedAwake returns the time the machine has been awake: the unbiased
// interrupt time, which excludes sleep and hibernation, in 100 ns units.
func unbiasedAwake() time.Duration {
	var t uint64
	if err := queryUnbiasedInterruptTime(&t); err != nil {
		return 0
	}
	return time.Duration(t) * 100
}
