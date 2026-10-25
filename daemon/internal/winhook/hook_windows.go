package winhook

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

const (
	whKeyboardLL = 13     // WH_KEYBOARD_LL
	whMouseLL    = 14     // WH_MOUSE_LL
	hcAction     = 0      // HC_ACTION
	wmQuit       = 0x0012 // WM_QUIT
	wmUser       = 0x0400 // WM_USER
	wmApp        = 0x8000 // WM_APP
	pmNoRemove   = 0x0000 // PM_NOREMOVE

	// wmInstallMouse asks the hook thread to install the mouse hook: a
	// low-level hook is called on the thread that installed it, so the thread
	// that pumps the hook's messages must install it. The tests post their
	// own messages at WM_APP (TestHookStackGrowth), so this one sits clear of
	// that.
	wmInstallMouse = wmApp + 0x100
)

// eventBuffer is the capacity of the Events channel. The callback never
// blocks: when the channel is full it drops the event and counts it.
const eventBuffer = 4096

// mouseEventBuffer is the capacity of the MouseEvents channel, under the same
// rule. A mouse hook queues only the events its Watch predicate accepts, which
// for padmate's tests is the few it injected itself.
const mouseEventBuffer = 1024

// kbdllhookstruct is the Win32 KBDLLHOOKSTRUCT.
type kbdllhookstruct struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// msllhookstruct is the Win32 MSLLHOOKSTRUCT: a POINT, then mouseData, flags
// and time, and dwExtraInfo after 4 bytes of padding on amd64 (32 bytes).
type msllhookstruct struct {
	x, y        int32
	mouseData   uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

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

// newMsg returns a MSG on the heap. The hook thread's MSG must not live on
// the goroutine stack: GetMessageW keeps the pointer while it calls the
// hook procedure, whose Go code may grow the stack, which moves it;
// GetMessageW would then write the next message into the freed old stack
// (TestHookStackGrowth). SyscallN keeps its uintptr arguments alive but
// does not make them escape, and a pointer a function that is not inlined
// returns always points to the heap.
//
//go:noinline
func newMsg() *msg { return new(msg) }

// ErrActive is returned by Start while another hook of this process is
// installed.
var ErrActive = errors.New("winhook: a keyboard hook is already active in this process")

// ErrMouseActive is returned by EnableMouse when the hook already has its
// mouse hook.
var ErrMouseActive = errors.New("winhook: the mouse hook is already installed")

// ErrStopped is returned by EnableMouse when the hook has been closed, or its
// thread has ended.
var ErrStopped = errors.New("winhook: the hook has stopped")

var (
	// callback is the one hook procedure of the process. NewCallback
	// allocations are never freed and limited in number, so it is created
	// once and shared by every Start (CLAUDE.md invariant 5).
	callback     uintptr
	callbackOnce sync.Once

	// mouseCallback is the one procedure of the mouse hook, made once like
	// callback, the first time a hook enables the mouse.
	mouseCallback     uintptr
	mouseCallbackOnce sync.Once

	// startMu serializes Start so that at most one hook is installed.
	startMu sync.Mutex
	// active is the hook the callback feeds, or nil.
	active atomic.Pointer[Hook]
)

// Hook is an installed low-level keyboard hook, and optionally a low-level
// mouse hook on the same thread (EnableMouse).
type Hook struct {
	swallow func(Event) bool
	events  chan Event
	dropped atomic.Uint64
	seq     atomic.Uint64 // the last Seq given to a keyboard or a mouse event

	// mouse is the mouse hook's state, set by the hook thread once it has
	// installed the hook (and kept after the thread has gone, for
	// MouseDropped); nil before EnableMouse. mouseReq is the request
	// EnableMouse posts to the thread; mouseMu serializes EnableMouse.
	mouse    atomic.Pointer[mouseState]
	mouseReq atomic.Pointer[mouseRequest]
	mouseMu  sync.Mutex

	tid     uint32        // hook thread, for PostThreadMessage
	done    chan struct{} // closed when the hook thread has unhooked
	loopErr error         // set by the hook thread before done is closed

	closing  atomic.Bool // set by Close: from then on nothing is swallowed
	closeMu  sync.Mutex
	closed   bool // Close has removed the hook; closeErr is final
	closeErr error
}

// Start installs the hook. swallow, when not nil, is called for every
// event on the hook thread; returning true discards the event so that no
// application receives it. It must be fast and must not block or panic:
// the system skips a hook that does not return within its timeout, and
// then the event is not swallowed. Swallow only events you injected
// yourself, never the physical keyboard.
//
// Start returns ErrActive while another hook of this process is active,
// including one whose Close failed.
func Start(swallow func(Event) bool) (*Hook, error) {
	startMu.Lock()
	defer startMu.Unlock()
	if active.Load() != nil {
		return nil, ErrActive
	}
	callbackOnce.Do(func() {
		// The first Now finds QueryPerformanceCounter in kernel32, which
		// allocates: here rather than in the callback.
		_ = latency.Now()
		callback = windows.NewCallback(hookProc)
	})

	h := &Hook{
		swallow: swallow,
		events:  make(chan Event, eventBuffer),
		done:    make(chan struct{}),
	}
	active.Store(h)
	ready := make(chan error, 1)
	go h.run(ready)
	if err := <-ready; err != nil {
		<-h.done
		active.Store(nil)
		return nil, err
	}
	return h, nil
}

// run is the hook thread. It stays locked to its OS thread for its whole
// life, so the thread exits with the goroutine.
func (h *Hook) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(h.done)

	// Create the thread message queue before anyone can PostThreadMessage
	// to it.
	m := newMsg()
	// Its return value only says whether a message was waiting.
	_ = peekMessage(m, 0, wmUser, wmUser, pmNoRemove)
	h.tid = windows.GetCurrentThreadId()

	var module windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &module); err != nil {
		ready <- fmt.Errorf("winhook: GetModuleHandleEx: %w", err)
		return
	}
	hhk, err := setWindowsHookEx(whKeyboardLL, callback, module, 0)
	if err != nil {
		ready <- fmt.Errorf("winhook: SetWindowsHookExW: %w", err)
		return
	}
	ready <- nil

	var mouseHook windows.Handle // 0 until the mouse hook is installed
	loopErr := h.messageLoop(m, module, &mouseHook)
	// The mouse hook goes first: both are gone before either channel closes.
	if mouseHook != 0 {
		if err := unhookWindowsHookEx(mouseHook); err != nil {
			loopErr = errors.Join(loopErr, fmt.Errorf("winhook: UnhookWindowsHookEx (mouse): %w", err))
		}
	}
	if err := unhookWindowsHookEx(hhk); err != nil {
		loopErr = errors.Join(loopErr, fmt.Errorf("winhook: UnhookWindowsHookEx: %w", err))
	}
	// The callbacks only run on this thread while it waits for messages,
	// so after unhooking nothing can send on the channels any more.
	active.CompareAndSwap(h, nil)
	close(h.events)
	if ms := h.mouse.Load(); ms != nil {
		close(ms.events)
	}
	h.loopErr = loopErr
}

// messageLoop pumps the thread's messages until WM_QUIT. The system calls
// the hook procedures from inside GetMessageW. The thread has no windows,
// so there is nothing to translate or dispatch. The one message it acts on
// is wmInstallMouse, which installs the mouse hook (into *mouseHook).
func (h *Hook) messageLoop(m *msg, module windows.Handle, mouseHook *windows.Handle) error {
	for {
		r, err := getMessage(m, 0, 0, 0)
		switch r {
		case 0:
			return nil // WM_QUIT
		case -1:
			return fmt.Errorf("winhook: GetMessageW: %w", err)
		}
		if m.message == wmInstallMouse {
			h.installMouse(module, mouseHook)
		}
		if afterGet != nil {
			afterGet(m.message, m.wParam)
		}
	}
}

// installMouse serves the request EnableMouse posted: it installs the mouse
// hook on this thread and reports the result. The state goes in first, so
// that the hook procedure finds it from its first call.
func (h *Hook) installMouse(module windows.Handle, mouseHook *windows.Handle) {
	req := h.mouseReq.Swap(nil)
	if req == nil || *mouseHook != 0 {
		return // answered already
	}
	h.mouse.Store(req.state)
	hhk, err := setWindowsHookEx(whMouseLL, mouseCallback, module, 0)
	if err != nil {
		h.mouse.Store(nil)
		req.result <- fmt.Errorf("winhook: SetWindowsHookExW (mouse): %w", err)
		return
	}
	*mouseHook = hhk
	req.result <- nil
}

// afterGet, when not nil, is called on the hook thread with each message
// GetMessageW returns. Tests set it while no hook is installed. It takes
// the fields rather than the MSG: passing the pointer to a function value
// would move the MSG to the heap by itself and hide TestHookStackGrowth's
// failure.
var afterGet func(message uint32, wParam uintptr)

// hookProc is the LowLevelKeyboardProc. It reads the counter first, copies
// the event, asks the predicate and returns at once; everything else
// happens off the hook thread.
func hookProc(nCode, wParam, lParam uintptr) uintptr {
	now := latency.Now() // does not allocate: Start called it first
	// nCode is a C int: only the low 32 bits of the register are defined.
	if int32(nCode) == hcAction {
		if h := active.Load(); h != nil {
			kb := *(**kbdllhookstruct)(unsafe.Pointer(&lParam))
			if h.handle(Event{
				Time:  kb.time,
				VK:    kb.vkCode,
				Scan:  kb.scanCode,
				Flags: kb.flags,
				Extra: kb.dwExtraInfo,
				Msg:   uint32(wParam),
				Stamp: now,
			}) {
				return 1
			}
		}
	}
	return callNextHookEx(0, int32(nCode), wParam, lParam)
}

// handle delivers e on Events and reports whether to swallow it: whether
// the predicate says so, unless Close has been called. It runs on the hook
// thread and never blocks.
func (h *Hook) handle(e Event) bool {
	e.Seq = h.seq.Add(1)
	e.Swallowed = h.swallow != nil && !h.closing.Load() && h.swallow(e)
	select {
	case h.events <- e:
	default:
		h.dropped.Add(1)
	}
	return e.Swallowed
}

// mouseHookProc is the LowLevelMouseProc. It follows hookProc: the counter
// first, a copy of the event, and a return at once. Every physical mouse
// move passes through it, so it does no more for an event the hook is not
// watching than hand it to CallNextHookEx (handleMouse).
func mouseHookProc(nCode, wParam, lParam uintptr) uintptr {
	now := latency.Now() // does not allocate: EnableMouse called it first
	if int32(nCode) == hcAction {
		if h := active.Load(); h != nil {
			ms := *(**msllhookstruct)(unsafe.Pointer(&lParam))
			if h.handleMouse(mouseEventOf(ms, uint32(wParam), now)) {
				return 1
			}
		}
	}
	return callNextHookEx(0, int32(nCode), wParam, lParam)
}

// mouseEventOf copies an MSLLHOOKSTRUCT and its window message into a
// MouseEvent.
func mouseEventOf(ms *msllhookstruct, msg uint32, stamp latency.Stamp) MouseEvent {
	return MouseEvent{
		X: ms.x, Y: ms.y,
		MouseData: ms.mouseData,
		Flags:     ms.flags,
		Time:      ms.time,
		Extra:     ms.dwExtraInfo,
		Msg:       msg,
		Stamp:     stamp,
	}
}

// handleMouse delivers e on the mouse channel and reports whether to swallow
// it. An event the Watch predicate does not accept, such as every physical
// mouse move, is neither delivered nor swallowed and does not take a Seq: it
// passes as if the hook were not there. Without a Watch predicate the events
// accepted are the injected ones. For a watched event the Swallow predicate
// decides, unless Close has been called. It runs on the hook thread and never
// blocks.
func (h *Hook) handleMouse(e MouseEvent) bool {
	ms := h.mouse.Load()
	if ms == nil {
		return false
	}
	if ms.watch != nil && !ms.watch(e) || ms.watch == nil && !e.Injected() {
		return false
	}
	e.Seq = h.seq.Add(1)
	e.Swallowed = ms.swallow != nil && !h.closing.Load() && ms.swallow(e)
	select {
	case ms.events <- e:
	default:
		ms.dropped.Add(1)
	}
	return e.Swallowed
}

// mouseState is the mouse hook of a Hook: what its procedure reads.
type mouseState struct {
	watch   func(MouseEvent) bool
	swallow func(MouseEvent) bool
	events  chan MouseEvent
	dropped atomic.Uint64
}

// mouseRequest is EnableMouse's request to the hook thread.
type mouseRequest struct {
	state  *mouseState
	result chan error // the hook thread's answer, buffered
}

// EnableMouse installs a low-level mouse hook (WH_MOUSE_LL) on the hook's
// thread, which also runs its message loop, and returns the channel its
// events arrive on, in order. The channel is closed after Close. A hook has
// at most one mouse hook, kept until Close.
//
// The mouse events share the numbering of the keyboard's (Seq), which says
// which came first. An event the hook does not watch (opts.Watch) passes
// untouched.
func (h *Hook) EnableMouse(opts MouseOptions) (<-chan MouseEvent, error) {
	h.mouseMu.Lock()
	defer h.mouseMu.Unlock()
	select {
	case <-h.done:
		return nil, ErrStopped
	default:
	}
	if h.closing.Load() {
		return nil, ErrStopped
	}
	if h.mouse.Load() != nil {
		return nil, ErrMouseActive
	}
	mouseCallbackOnce.Do(func() {
		// The first Now finds QueryPerformanceCounter in kernel32, which
		// allocates: here rather than in the callback.
		_ = latency.Now()
		mouseCallback = windows.NewCallback(mouseHookProc)
	})
	req := &mouseRequest{
		state: &mouseState{
			watch: opts.Watch, swallow: opts.Swallow,
			events: make(chan MouseEvent, mouseEventBuffer),
		},
		result: make(chan error, 1),
	}
	h.mouseReq.Store(req)
	if err := h.postInstallMouse(); err != nil {
		h.mouseReq.Store(nil)
		return nil, err
	}
	select {
	case err := <-req.result:
		if err != nil {
			return nil, err
		}
		return req.state.events, nil
	case <-h.done:
		// The thread ended before it served the request.
		return nil, ErrStopped
	}
}

// postInstallMouse posts wmInstallMouse to the hook thread. PostThreadMessage
// fails only while the queue is full; retry briefly.
func (h *Hook) postInstallMouse() error {
	var err error
	for range 50 {
		if err = postThreadMessage(h.tid, wmInstallMouse, 0, 0); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("winhook: PostThreadMessageW(install mouse hook): %w", err)
}

// MouseDropped returns how many mouse events were not delivered on the
// channel of EnableMouse because it was full. It is 0 without a mouse hook.
func (h *Hook) MouseDropped() uint64 {
	if ms := h.mouse.Load(); ms != nil {
		return ms.dropped.Load()
	}
	return 0
}

// Events returns the events the hook has seen, in order. The channel is
// closed after Close.
func (h *Hook) Events() <-chan Event { return h.events }

// Dropped returns how many events were not delivered on Events because
// the channel was full. Dropped events were still swallowed or passed on
// as the predicate decided.
func (h *Hook) Dropped() uint64 { return h.dropped.Load() }

// Close removes the hook and waits for the hook thread to exit. The hook
// swallows nothing from the moment Close is called, even if removing it
// fails. After a failure the hook stays installed, Start returns
// ErrActive, and a later Close tries again; once Close has removed the
// hook, later calls return the same result.
func (h *Hook) Close() error {
	h.closing.Store(true)
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.closed {
		return h.closeErr
	}
	err := h.stop()
	select {
	case <-h.done:
		h.closed, h.closeErr = true, err
	default:
	}
	return err
}

// postQuit posts WM_QUIT to the hook thread tid. Tests replace it to make
// Close fail.
var postQuit = func(tid uint32) error {
	if err := postThreadMessage(tid, wmQuit, 0, 0); err != nil {
		return fmt.Errorf("winhook: PostThreadMessageW(WM_QUIT): %w", err)
	}
	return nil
}

// stop ends the hook thread and returns its error, or the error of
// posting WM_QUIT when the thread is still running.
func (h *Hook) stop() error {
	// PostThreadMessage fails only while the queue is full; retry briefly.
	var postErr error
	for range 50 {
		select {
		case <-h.done:
			return h.loopErr
		default:
		}
		if postErr = postQuit(h.tid); postErr == nil {
			<-h.done
			return h.loopErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-h.done:
		return h.loopErr
	default:
		return postErr
	}
}
