package winfocus

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	messageFocus = 0x8008
	messageTimer = 0x0113
	timerFocus   = 2
)

var (
	callbackOnce sync.Once
	callback     uintptr
	active       atomic.Pointer[Watcher]
)

// Attach runs on the locked session thread, after its window exists. Close,
// Poll and Handle must run on that same thread. No additional loop is started.
func Attach(window uintptr, cfg Config) (*Watcher, error) {
	if window == 0 || getWindowThreadProcessID(windows.HWND(window), nil) != windows.GetCurrentThreadId() {
		return nil, errors.New("winfocus: window must belong to the calling thread")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Query == nil {
		cfg.Query = foreground
	}
	w := &Watcher{window: window, cfg: cfg, started: time.Now()}
	if !active.CompareAndSwap(nil, w) {
		return nil, errors.New("winfocus: watcher already attached")
	}
	callbackOnce.Do(func() { callback = windows.NewCallback(focusEvent) })
	// Warm PostMessage's lazy binding before any hook callback uses it.
	if err := postMessage(windows.HWND(window), messageFocus, 0, 0); err != nil {
		active.CompareAndSwap(w, nil)
		return nil, err
	}
	for i, event := range []uint32{0x0003, 0x0017} { // FOREGROUND, MINIMIZEEND
		w.hooks[i] = uintptr(setWinEventHook(event, event, 0, callback, 0, 0, 0))
		if w.hooks[i] == 0 {
			cfg.Logger.Warn("foreground hook unavailable; polling", "event", event)
		}
	}
	return w, nil
}

func focusEvent(_, _, _, _, _, _, _ uintptr) uintptr {
	if w := active.Load(); w != nil {
		// The HWND carried by a delayed WinEvent may already be stale.
		// Resolve the actual foreground after debounce, outside the callback.
		_ = postMessage(windows.HWND(w.window), messageFocus, 0, 0)
	}
	return 0
}

func (w *Watcher) signal() {
	w.debouncer.Signal(time.Since(w.started))
	if id, err := setTimer(windows.HWND(w.window), timerFocus, uint32(Debounce/time.Millisecond), 0); id == 0 {
		w.cfg.Logger.Debug("foreground timer unavailable; polling", "err", err)
	}
}

// Handle consumes only this observer's reserved messages and timer. Calls
// made by SendMessage are reposted before querying a process or token.
func (w *Watcher) Handle(message uint32, wParam, _ uintptr) bool {
	if w.closed || message != messageFocus && (message != messageTimer || wParam != timerFocus) {
		return false
	}
	if inSendMessage() {
		_ = postMessage(windows.HWND(w.window), message, wParam, 0)
		return true
	}
	if message == messageFocus {
		w.signal()
	} else if now := time.Since(w.started); w.debouncer.Ready(now) {
		w.sample(now)
	}
	return true
}

func (w *Watcher) sample(now time.Duration) {
	// Kill first; an OnChange callback cannot leave a repeating timer behind.
	_ = killTimer(windows.HWND(w.window), timerFocus)
	app, err := w.cfg.Query()
	if err != nil {
		w.debouncer.Sample(now, App{})
		w.cfg.Logger.Debug("foreground query failed", "err", err)
		return
	}
	if app, changed := w.debouncer.Sample(now, app); changed && w.cfg.OnChange != nil {
		w.cfg.OnChange(app)
	}
}

// Poll repairs a missed event or a failed hook/timer. It is called by the
// session timer; it never publishes a just-observed change without debounce.
func (w *Watcher) Poll() {
	if w.closed {
		return
	}
	if now := time.Since(w.started); w.debouncer.Ready(now) {
		w.sample(now)
		return
	}
	if w.debouncer.pending {
		return
	}
	app, err := w.cfg.Query()
	if err == nil && app.Path != "" && !IgnoredClass(app.Class) && app != w.debouncer.Current {
		w.signal()
	}
}

func (w *Watcher) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	active.CompareAndSwap(w, nil)
	_ = killTimer(windows.HWND(w.window), timerFocus)
	var err error
	for _, hook := range w.hooks {
		if hook != 0 && !unhookWinEvent(windows.Handle(hook)) {
			err = errors.Join(err, fmt.Errorf("winfocus: UnhookWinEvent failed"))
		}
	}
	return err
}
