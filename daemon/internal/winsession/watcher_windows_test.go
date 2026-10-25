package winsession_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsessiontest"
)

// wait bounds every wait of these tests for the session thread.
const wait = 5 * time.Second

// fakeSystem is the Querier of the watcher tests: the state the queries
// read, which the test changes while the session thread reads it.
type fakeSystem struct {
	mu        sync.Mutex
	info      ws.SessionInfo
	sessErr   error
	other     bool
	deskErr   error
	panicking bool // both queries panic
	sessPanic bool // the session query panics

	sessions, desktops int
	inSend             int           // queries made inside a sent message
	hold               chan struct{} // when not nil, queries wait until it is closed
}

func newFake() *fakeSystem {
	return &fakeSystem{info: ws.SessionInfo{ID: 1, Active: true, LockKnown: true}}
}

// enter records whether the query runs inside a sent message and waits
// for the hold.
func (f *fakeSystem) enter() {
	sent := winsessiontest.InSendMessage()
	f.mu.Lock()
	if sent {
		f.inSend++
	}
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
}

func (f *fakeSystem) Session() (ws.SessionInfo, error) {
	f.enter()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions++
	if f.panicking || f.sessPanic {
		panic("fake session query")
	}
	return f.info, f.sessErr
}

func (f *fakeSystem) Desktop() (bool, error) {
	f.enter()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desktops++
	if f.panicking {
		panic("fake desktop query")
	}
	return f.other, f.deskErr
}

// set changes the world under the lock.
func (f *fakeSystem) set(change func(*fakeSystem)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// counts returns how often each query ran.
func (f *fakeSystem) counts() (sessions, desktops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions, f.desktops
}

// sent returns how many queries ran inside a sent message.
func (f *fakeSystem) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inSend
}

// holdQueries makes the queries wait until the returned channel is closed.
func (f *fakeSystem) holdQueries() chan struct{} {
	hold := make(chan struct{})
	f.set(func(f *fakeSystem) { f.hold = hold })
	return hold
}

// logs keeps the records of a Logger.
type logs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logs) Enabled(context.Context, slog.Level) bool { return true }
func (l *logs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logs) WithGroup(string) slog.Handler            { return l }

func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}

// find returns the records of level with message msg.
func (l *logs) find(level slog.Level, msg string) []slog.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []slog.Record
	for _, r := range l.records {
		if r.Level == level && r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

// attr returns the value of key in r.
func attr(r slog.Record, key string) string {
	var v string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.Resolve().String()
			return false
		}
		return true
	})
	return v
}

// record keeps what the callbacks saw, in order.
type record struct {
	mu   sync.Mutex
	list []string
}

func (r *record) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, s)
}

// take returns what was recorded since the last take.
func (r *record) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.list
	r.list = nil
	return out
}

// waitFor waits until s was recorded, and returns and forgets everything
// recorded up to it.
func (r *record) waitFor(t *testing.T, s string) []string {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		r.mu.Lock()
		if i := slices.Index(r.list, s); i >= 0 {
			out := slices.Clone(r.list[:i+1])
			r.list = slices.Delete(r.list, 0, i+1)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("%q not recorded; recorded %q", s, r.take())
		}
		time.Sleep(time.Millisecond)
	}
}

func change(c ws.Change) string { return fmt.Sprintf("%s %s>%s", c.Signal, c.Was, c.Now) }

// Ways the dispatched seam ends the session thread.
const (
	dieError int32 = iota + 1
	diePanic
)

const (
	// dieMarker is the wParam of the WM_NULL that ends the thread.
	dieMarker = 0xDEAD
	// flushBase is added to the flush markers, the wParam of the WM_NULLs
	// flush posts.
	flushBase = 1 << 32
)

var errDie = errors.New("test: the session thread dies")

// harness is a started Watcher with fakes around it.
type harness struct {
	t    *testing.T
	w    *ws.Watcher
	f    *fakeSystem
	logs *logs
	rec  *record

	awake   atomic.Int64 // the awake seam, in ns
	die     atomic.Int32
	markers atomic.Uint64
	flushed atomic.Uint64
}

// start starts a Watcher on a fake Querier with a registration that
// succeeds, the awake seam, no desktop switch hook and a Poll of an hour,
// so that nothing ticks or checks the desktop unless the test posts a
// WM_TIMER or a desktop switch: the machine's own desktop switches would
// add queries. world, when not nil, sets the world before Start; mod, when
// not nil, changes the Config.
func start(t *testing.T, world func(*fakeSystem), mod func(*ws.Config)) *harness {
	t.Helper()
	h := &harness{t: t, f: newFake(), logs: &logs{}, rec: &record{}}
	if world != nil {
		h.f.set(world)
	}
	cfg := ws.Config{
		Logger:   slog.New(h.logs),
		OnChange: func(c ws.Change) { h.rec.add(change(c)) },
		OnStop:   func(cause string) { h.rec.add("stop " + cause) },
		Barrier: func(context.Context) error {
			h.rec.add("barrier")
			return nil
		},
		Query: h.f,
		Poll:  time.Hour,
	}
	cfg.SetRegister(func(uintptr) error { return nil })
	cfg.SetAwake(func() time.Duration { return time.Duration(h.awake.Load()) })
	cfg.SetDispatched(h.dispatched)
	cfg.SetHook(false)
	if mod != nil {
		mod(&cfg)
	}
	w, err := ws.Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.w = w
	t.Cleanup(func() {
		err := w.Close()
		select {
		case <-w.Degraded():
		default:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		}
		if n := h.f.sent(); n != 0 {
			s, d := h.f.counts()
			t.Errorf("%d queries ran inside a sent message (of %d session, %d desktop)", n, s, d)
		}
	})
	return h
}

func (h *harness) dispatched(message uint32, wParam uintptr) error {
	if message != ws.WMNull || wParam == 0 {
		return nil
	}
	if wParam == dieMarker {
		switch h.die.Load() {
		case dieError:
			return errDie
		case diePanic:
			panic("test: the session thread panics")
		}
		return nil
	}
	h.flushed.Store(uint64(wParam))
	return nil
}

// flush waits until the session thread has handled every message posted
// to it before: it posts a WM_NULL behind them and waits for its dispatch.
func (h *harness) flush() {
	h.t.Helper()
	m := flushBase + h.markers.Add(1)
	h.post(ws.WMNull, uintptr(m), 0)
	deadline := time.Now().Add(wait)
	for h.flushed.Load() < m {
		if time.Now().After(deadline) {
			h.t.Fatal("the session thread did not dispatch the flush")
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) send(message uint32, wParam, lParam uintptr) uintptr {
	return winsessiontest.Send(h.w.Window(), message, wParam, lParam)
}

func (h *harness) post(message uint32, wParam, lParam uintptr) {
	h.t.Helper()
	if err := winsessiontest.Post(h.w.Window(), message, wParam, lParam); err != nil {
		h.t.Fatal(err)
	}
}

// tick posts one tick and waits for it.
func (h *harness) tick() {
	h.t.Helper()
	h.post(ws.WMTimer, ws.TimerTick, 0)
	h.flush()
}

func TestStructSizes(t *testing.T) {
	if ws.MsgSize != 48 {
		t.Errorf("msg is %d bytes, want 48", ws.MsgSize)
	}
	if ws.WndClassExSize != 80 {
		t.Errorf("wndClassEx is %d bytes, want 80", ws.WndClassExSize)
	}
}

// TestSessionMessages sends the messages the system sends and checks the
// Changes, the barriers, the return values and the queries: the signals
// that set a reason query nothing; the ones that clear one query from a
// posted recheck, whose answer wins.
func TestSessionMessages(t *testing.T) {
	h := start(t, nil, nil)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("Init in an unlocked world: %q, want nothing", got)
	}
	steps := []struct {
		name           string
		world          func(*fakeSystem)
		msg            uint32
		wParam, lParam uintptr
		ret            uintptr
		queries        bool
		want           []string
	}{
		{"lock", nil, ws.WMWTSSessionChange, ws.WTSSessionLock, 0, 0, false, []string{"lock none>lock"}},
		{"unlock", nil, ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0, 0, true, []string{"unlock lock>none"}},
		{"console disconnect", nil, ws.WMWTSSessionChange, ws.WTSConsoleDisconnect, 0, 0, false, []string{"disconnect none>disconnect"}},
		{"console connect", nil, ws.WMWTSSessionChange, ws.WTSConsoleConnect, 0, 0, true, []string{"connect disconnect>none"}},
		{"remote disconnect", nil, ws.WMWTSSessionChange, ws.WTSRemoteDisconnect, 0, 0, false, []string{"disconnect none>disconnect"}},
		{"remote connect", nil, ws.WMWTSSessionChange, ws.WTSRemoteConnect, 0, 0, true, []string{"connect disconnect>none"}},
		{"suspend", nil, ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0, 1, false, []string{"sleep none>sleep", "barrier"}},
		{"resume automatic", nil, ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, 0, 1, true, []string{"wake sleep>none"}},
		{"suspend 2", nil, ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0, 1, false, []string{"sleep none>sleep", "barrier"}},
		{"resume suspend", nil, ws.WMPowerBroadcast, ws.PBTAPMResumeSuspend, 0, 1, true, []string{"wake sleep>none"}},
		{"suspend 3", nil, ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0, 1, false, []string{"sleep none>sleep", "barrier"}},
		{"resume critical", nil, ws.WMPowerBroadcast, ws.PBTAPMResumeCritical, 0, 1, true, []string{"wake sleep>none"}},
		{"query end", nil, ws.WMQueryEndSession, 0, ws.EndSessionLogoff, 1, false, []string{"queryend none>end", "barrier"}},
		{"end canceled", nil, ws.WMEndSession, 0, ws.EndSessionLogoff, 0, true, []string{"endcanceled end>none"}},
		// The world stays locked: the unlock's recheck keeps Lock, and a
		// tick clears it once the world unlocks.
		{"lock in a locked world", func(f *fakeSystem) { f.info.Locked = true }, ws.WMWTSSessionChange, ws.WTSSessionLock, 0, 0, false, []string{"lock none>lock"}},
		{"unlock while locked", nil, ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0, 0, true, nil},
		{"connect while disconnected", func(f *fakeSystem) { f.info.Active = false }, ws.WMWTSSessionChange, ws.WTSConsoleConnect, 0, 0, true, []string{"connect lock>lock,disconnect"}},
	}
	for _, s := range steps {
		if s.world != nil {
			h.f.set(s.world)
		}
		sessions, desktops := h.f.counts()
		if got := h.send(s.msg, s.wParam, s.lParam); got != s.ret {
			t.Errorf("%s returned %d, want %d", s.name, got, s.ret)
		}
		h.flush()
		want := 0
		if s.queries {
			want = 1
		}
		if s2, d2 := h.f.counts(); s2-sessions != want || d2-desktops != want {
			t.Errorf("%s: %d session and %d desktop queries, want %d of each", s.name, s2-sessions, d2-desktops, want)
		}
		if got := h.rec.take(); !slices.Equal(got, s.want) {
			t.Errorf("%s: %q, want %q", s.name, got, s.want)
		}
	}
	if got := h.w.Reasons(); got != ws.Lock|ws.Disconnect {
		t.Errorf("Reasons() = %s, want lock,disconnect", got)
	}

	h.f.set(func(f *fakeSystem) { f.info.Locked, f.info.Active = false, true })
	h.tick()
	h.f.set(func(f *fakeSystem) { f.other = true })
	h.post(ws.WMDesktopSwitch, 0, 0)
	h.flush()
	h.f.set(func(f *fakeSystem) { f.other = false })
	h.post(ws.WMDesktopSwitch, 0, 0)
	h.flush()
	want := []string{"tick lock,disconnect>none", "desktop none>desktop", "desktop desktop>none"}
	if got := h.rec.take(); !slices.Equal(got, want) {
		t.Errorf("tick and desktop switches: %q, want %q", got, want)
	}
}

// TestSessionStackGrowth grows the session thread's stack in every sent
// message, deeper each time, and checks that the next posted message is
// dispatched as posted: GetMessageW writes it through a pointer it kept
// across the window procedure, which a MSG on the moved stack would miss.
func TestSessionStackGrowth(t *testing.T) {
	var depth atomic.Int32
	h := start(t, nil, func(c *ws.Config) {
		c.OnChange = func(ws.Change) { grow(int(depth.Load())) }
	})
	for i := range 6 {
		depth.Store(int32(64 << i))
		sig := uintptr(ws.WTSSessionLock)
		if i%2 == 1 {
			sig = ws.WTSConsoleDisconnect
		}
		h.send(ws.WMWTSSessionChange, sig, 0)
		h.flush()
		h.send(ws.WMWTSSessionChange, sig+1, 0) // unlock, connect: posted
		h.flush()
	}
}

// grow uses about n KB of stack.
//
//go:noinline
func grow(n int) byte {
	var frame [1024]byte
	frame[n%len(frame)] = byte(n)
	if n == 0 {
		return frame[0]
	}
	return grow(n-1) + frame[n%len(frame)]
}

// TestSessionRecheckPosted sends the messages whose steps query and checks
// that SendMessageW returns before the query runs: every query runs in a
// posted message, never inside a sent one, whose sender would wait for it.
func TestSessionRecheckPosted(t *testing.T) {
	h := start(t, nil, nil)
	cases := []struct {
		name           string
		msg            uint32
		wParam, lParam uintptr
	}{
		{"unlock", ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0},
		{"connect", ws.WMWTSSessionChange, ws.WTSConsoleConnect, 0},
		{"wake", ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, 0},
		{"end canceled", ws.WMEndSession, 0, 0},
		{"desktop switch", ws.WMDesktopSwitch, 0, 0},
		{"recheck", ws.WMRecheck, uintptr(ws.SigUnlock), 0},
		{"tick", ws.WMTimer, ws.TimerTick, 0},
	}
	for _, c := range cases {
		_, desktops := h.f.counts()
		hold := h.f.holdQueries()
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			h.send(c.msg, c.wParam, c.lParam)
		}()
		select {
		case <-sent:
		case <-time.After(wait):
			close(hold)
			t.Fatalf("%s: SendMessageW waited for a query", c.name)
		}
		close(hold)
		h.f.set(func(f *fakeSystem) { f.hold = nil })
		h.flush()
		if _, d := h.f.counts(); d != desktops+1 {
			t.Errorf("%s: %d desktop queries after the send, want 1", c.name, d-desktops)
		}
	}
	if got := h.rec.take(); len(got) != 0 {
		t.Errorf("changes: %q, want none", got)
	}
}

// TestSessionIgnored sends the codes that suspend nothing and posts the
// private messages with arguments nobody posts: nothing changes, nothing
// is queried, and the window stays.
func TestSessionIgnored(t *testing.T) {
	h := start(t, nil, nil)
	h.rec.take()
	sessions, desktops := h.f.counts()
	for _, code := range []uintptr{
		ws.WTSSessionLogon, ws.WTSSessionLogoff, ws.WTSRemoteControl, ws.WTSSessionCreate, ws.WTSSessionTerminate, 0x42,
	} {
		if got := h.send(ws.WMWTSSessionChange, code, 0); got != 0 {
			t.Errorf("WTS %#x returned %d, want 0", code, got)
		}
	}
	for _, code := range []uintptr{ws.PBTAPMPowerStatusChange, ws.PBTPowerSettingChange, 0x42} {
		if got := h.send(ws.WMPowerBroadcast, code, 0); got != 1 {
			t.Errorf("PBT %#x returned %d, want TRUE", code, got)
		}
	}
	h.post(ws.WMTimer, ws.TimerTick+1, 0)        // another timer's
	h.post(ws.WMRecheck, uintptr(ws.SigLock), 0) // not a clearing signal
	h.post(ws.WMRecheck, 0, 0)
	h.post(ws.WMStop, 0, 0) // not Close's
	h.flush()
	if got := h.rec.take(); len(got) != 0 {
		t.Errorf("changes: %q, want none", got)
	}
	if s, d := h.f.counts(); s != sessions || d != desktops {
		t.Errorf("%d session and %d desktop queries, want none", s-sessions, d-desktops)
	}
	if !winsessiontest.IsWindow(h.w.Window()) {
		t.Error("the session window is gone")
	}
}

// TestSessionInitial starts in a locked world on the secure desktop: the
// Init change is reported before Start returns.
func TestSessionInitial(t *testing.T) {
	h := start(t, func(f *fakeSystem) { f.info.Locked, f.other = true, true }, nil)
	if got, want := h.rec.take(), []string{"init none>lock,desktop"}; !slices.Equal(got, want) {
		t.Errorf("before Start returned: %q, want %q", got, want)
	}
	if got := h.w.Reasons(); got != ws.Lock|ws.Desktop {
		t.Errorf("Reasons() = %s, want lock,desktop", got)
	}
	if s, d := h.f.counts(); s != 1 || d != 1 {
		t.Errorf("Init made %d session and %d desktop queries, want 1 and 1", s, d)
	}
}

// TestSessionBarrierTimeout checks that the sleep and end-of-session
// handlers wait for the Barrier no longer than BarrierWait, even for one
// that ignores its context, and log what went wrong.
func TestSessionBarrierTimeout(t *testing.T) {
	var mode atomic.Int32 // 0 blocks, 1 fails, 2 panics
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h := start(t, nil, func(c *ws.Config) {
		c.BarrierWait = 50 * time.Millisecond
		c.Barrier = func(context.Context) error {
			switch mode.Load() {
			case 1:
				return errors.New("test: the barrier fails")
			case 2:
				panic("test: the barrier panics")
			}
			<-release
			return nil
		}
	})
	began := time.Now()
	if got := h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0); got != 1 {
		t.Errorf("PBT_APMSUSPEND returned %d, want TRUE", got)
	}
	if took := time.Since(began); took < 50*time.Millisecond || took > time.Second {
		t.Errorf("PBT_APMSUSPEND took %v, want the 50 ms BarrierWait", took)
	}
	if got := h.send(ws.WMQueryEndSession, 0, 0); got != 1 {
		t.Errorf("WM_QUERYENDSESSION returned %d, want TRUE", got)
	}
	timedOut := h.logs.find(slog.LevelWarn, "session barrier timed out")
	if len(timedOut) != 2 || attr(timedOut[0], "signal") != "sleep" || attr(timedOut[1], "signal") != "queryend" {
		t.Errorf("timeouts logged: %v, want sleep and queryend", timedOut)
	}
	mode.Store(1)
	h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0)
	mode.Store(2)
	h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0)
	failed := h.logs.find(slog.LevelWarn, "session barrier failed")
	if len(failed) != 2 || !strings.Contains(attr(failed[0], "err"), "fails") || !strings.Contains(attr(failed[1], "err"), "panicked") {
		t.Errorf("failures logged: %v, want the error and the panic", failed)
	}
	if got := h.w.Reasons(); got != ws.Sleep|ws.End {
		t.Errorf("Reasons() = %s, want sleep,end", got)
	}
}

// TestSessionEndWaitsForClose ends the session: the handler releases,
// asks the process to stop and does not return until Close.
func TestSessionEndWaitsForClose(t *testing.T) {
	h := start(t, nil, func(c *ws.Config) { c.EndWait = time.Minute })
	h.rec.take()
	returned := make(chan uintptr, 1)
	go func() { returned <- h.send(ws.WMEndSession, 1, ws.EndSessionLogoff) }()
	got := h.rec.waitFor(t, "stop session-end")
	if want := []string{"end none>end", "barrier", "stop session-end"}; !slices.Equal(got, want) {
		t.Errorf("at session end: %q, want %q", got, want)
	}
	select {
	case <-returned:
		t.Fatal("WM_ENDSESSION returned before Close")
	case <-time.After(100 * time.Millisecond):
	}
	if err := h.w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case r := <-returned:
		if r != 0 {
			t.Errorf("WM_ENDSESSION returned %d, want 0", r)
		}
	case <-time.After(wait):
		t.Fatal("WM_ENDSESSION did not return after Close")
	}
	ending := h.logs.find(slog.LevelInfo, "session ending")
	if len(ending) != 1 || attr(ending[0], "kind") != "logoff" {
		t.Errorf("session ending logged: %v, want kind=logoff", ending)
	}
	if winsessiontest.IsWindow(h.w.Window()) {
		t.Error("the session window outlived Close")
	}
}

// TestSessionEndTimeout ends the session with nobody closing the watcher:
// the handler returns after EndWait.
func TestSessionEndTimeout(t *testing.T) {
	h := start(t, nil, func(c *ws.Config) { c.EndWait = 50 * time.Millisecond })
	began := time.Now()
	if got := h.send(ws.WMEndSession, 1, 0); got != 0 {
		t.Errorf("WM_ENDSESSION returned %d, want 0", got)
	}
	if took := time.Since(began); took < 50*time.Millisecond || took > time.Second {
		t.Errorf("WM_ENDSESSION took %v, want the 50 ms EndWait", took)
	}
	if n := len(h.logs.find(slog.LevelWarn, "session end wait timed out")); n != 1 {
		t.Errorf("%d end wait timeouts logged, want 1", n)
	}
	ending := h.logs.find(slog.LevelInfo, "session ending")
	if len(ending) != 1 || attr(ending[0], "kind") != "shutdown" {
		t.Errorf("session ending logged: %v, want kind=shutdown", ending)
	}
	if got := h.w.Reasons(); got != ws.End {
		t.Errorf("Reasons() = %s, want end", got)
	}
	h.flush() // the thread still runs
}

// TestSessionClose posts WM_CLOSE: the watcher asks the process to stop
// and keeps its window until Close.
func TestSessionClose(t *testing.T) {
	h := start(t, nil, nil)
	h.post(ws.WMClose, 0, 0)
	h.flush()
	if got := h.rec.take(); !slices.Equal(got, []string{"stop close"}) {
		t.Errorf("after WM_CLOSE: %q, want stop close", got)
	}
	if !winsessiontest.IsWindow(h.w.Window()) {
		t.Fatal("WM_CLOSE destroyed the session window")
	}
	if err := h.w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-h.w.Done():
	default:
		t.Error("Done is open after Close")
	}
	if winsessiontest.IsWindow(h.w.Window()) {
		t.Error("the session window outlived Close")
	}
}

// TestSessionTick runs the real timer: the world changes with no message,
// and the ticks correct the reasons.
func TestSessionTick(t *testing.T) {
	h := start(t, nil, func(c *ws.Config) { c.Poll = 20 * time.Millisecond })
	h.f.set(func(f *fakeSystem) { f.info.Locked = true })
	h.rec.waitFor(t, "tick none>lock")
	h.f.set(func(f *fakeSystem) { f.other = true })
	h.rec.waitFor(t, "tick lock>lock,desktop")
	// A tick may read the session before this change and the desktop
	// after it, or the other way round.
	h.f.set(func(f *fakeSystem) { f.info.Locked, f.other = false, false })
	deadline := time.Now().Add(wait)
	for h.w.Reasons() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("Reasons() = %s, want none", h.w.Reasons())
		}
		time.Sleep(time.Millisecond)
	}
	got := h.rec.take()
	if !slices.Equal(got, []string{"tick lock,desktop>none"}) &&
		!slices.Equal(got, []string{"tick lock,desktop>desktop", "tick desktop>none"}) &&
		!slices.Equal(got, []string{"tick lock,desktop>lock", "tick lock>none"}) {
		t.Errorf("unlocking: %q", got)
	}
}

// TestSessionStale checks that Sleep and End are cleared after a minute of
// awake time, with a Warn.
func TestSessionStale(t *testing.T) {
	h := start(t, nil, nil)
	h.awake.Store(int64(10 * time.Second))
	h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0)
	h.awake.Store(int64(40 * time.Second))
	h.send(ws.WMQueryEndSession, 0, 0)
	h.awake.Store(int64(69 * time.Second))
	h.tick()
	h.awake.Store(int64(70 * time.Second))
	h.tick()
	h.awake.Store(int64(100 * time.Second))
	h.tick()
	want := []string{"sleep none>sleep", "barrier", "queryend sleep>sleep,end", "barrier", "tick sleep,end>end", "tick end>none"}
	if got := h.rec.take(); !slices.Equal(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	stale := h.logs.find(slog.LevelWarn, "session reasons stale; cleared")
	if len(stale) != 2 || attr(stale[0], "reasons") != "sleep" || attr(stale[1], "reasons") != "end" {
		t.Errorf("stale logged: %v, want sleep then end", stale)
	}
}

// TestSessionRegisterFails fails the session notifications three times:
// one Warn, a retry on every tick, the session queried on every tick
// meanwhile, and an Info when they are registered.
func TestSessionRegisterFails(t *testing.T) {
	var seen []int // the session queries made before each attempt
	var f *fakeSystem
	h := start(t, nil, func(c *ws.Config) {
		f = c.Query.(*fakeSystem)
		c.SetRegister(func(uintptr) error {
			s, _ := f.counts()
			seen = append(seen, s)
			if len(seen) <= 3 {
				return syscall.Errno(1702) // RPC_S_INVALID_BINDING
			}
			return nil
		})
	})
	for range 3 {
		h.tick()
	}
	if want := []int{0, 1, 2, 3}; !slices.Equal(seen, want) {
		t.Errorf("session queries before each attempt: %v, want %v", seen, want)
	}
	if s, _ := h.f.counts(); s != 4 {
		t.Errorf("%d session queries after Init and 3 ticks, want 4", s)
	}
	h.tick() // registered: the 4th tick does not query the session
	if s, _ := h.f.counts(); s != 4 {
		t.Errorf("the 4th tick queried the session: %d queries, want 4", s)
	}
	h.tick() // the 5th does
	if s, _ := h.f.counts(); s != 5 {
		t.Errorf("the 5th tick: %d session queries, want 5", s)
	}
	if n := len(h.logs.find(slog.LevelWarn, "session notifications unavailable; retrying")); n != 1 {
		t.Errorf("%d Warns, want 1", n)
	}
	registered := h.logs.find(slog.LevelInfo, "session notifications registered")
	if len(registered) != 1 || attr(registered[0], "attempts") != "4" {
		t.Errorf("registered logged: %v, want attempts=4", registered)
	}
	if len(seen) != 4 {
		t.Errorf("%d attempts, want 4: no retry once registered", len(seen))
	}
}

// TestSessionLifecycle starts and closes the real thing three times: the
// session notifications are unregistered before the window is destroyed,
// and Close is idempotent.
func TestSessionLifecycle(t *testing.T) {
	for i := range 3 {
		var mu sync.Mutex
		var ops []string
		var registerErr error
		h := start(t, nil, func(c *ws.Config) {
			c.SetRegister(nil) // WTSRegisterSessionNotification
			c.SetHook(true)
			c.SetTrace(func(op string, err error) {
				mu.Lock()
				defer mu.Unlock()
				ops = append(ops, op)
				if op == "wts-register" {
					registerErr = err
				} else if err != nil {
					t.Errorf("cycle %d: %s: %v", i, op, err)
				}
			})
		})
		hwnd := h.w.Window()
		if !winsessiontest.IsWindow(hwnd) {
			t.Fatalf("cycle %d: no window", i)
		}
		if err := h.w.Close(); err != nil {
			t.Fatalf("cycle %d: Close: %v", i, err)
		}
		if err := h.w.Close(); err != nil {
			t.Errorf("cycle %d: the second Close: %v", i, err)
		}
		select {
		case <-h.w.Done():
		default:
			t.Errorf("cycle %d: Done is open", i)
		}
		if err := h.w.Err(); err != nil {
			t.Errorf("cycle %d: Err() = %v", i, err)
		}
		if winsessiontest.IsWindow(hwnd) {
			t.Errorf("cycle %d: the window outlived Close", i)
		}
		mu.Lock()
		t.Logf("cycle %d: %v (register: %v)", i, ops, registerErr)
		unregister := slices.Index(ops, "wts-unregister")
		destroy := slices.Index(ops, "window-destroy")
		switch {
		case destroy != len(ops)-1:
			t.Errorf("cycle %d: the window is not destroyed last: %v", i, ops)
		case registerErr == nil && (unregister < 0 || unregister > destroy):
			t.Errorf("cycle %d: the session notifications are not unregistered before the window is destroyed: %v", i, ops)
		case registerErr != nil && unregister >= 0:
			t.Errorf("cycle %d: unregistered a failed registration: %v", i, ops)
		}
		mu.Unlock()
	}
}

// TestSessionTimerFallback fails SetTimer: a goroutine posts the ticks
// instead, and the teardown stops it before it destroys the window.
func TestSessionTimerFallback(t *testing.T) {
	var mu sync.Mutex
	var ops []string
	h := start(t, nil, func(c *ws.Config) {
		c.Poll = 20 * time.Millisecond
		c.FailTimer()
		c.SetTrace(func(op string, err error) {
			mu.Lock()
			defer mu.Unlock()
			ops = append(ops, op)
		})
	})
	if n := len(h.logs.find(slog.LevelWarn, "session timer unavailable; ticking from a goroutine")); n != 1 {
		t.Errorf("%d timer failures logged, want 1", n)
	}
	h.f.set(func(f *fakeSystem) { f.info.Locked = true })
	h.rec.waitFor(t, "tick none>lock")
	h.f.set(func(f *fakeSystem) { f.info.Locked = false })
	h.rec.waitFor(t, "tick lock>none")
	if err := h.w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	stop, destroy := slices.Index(ops, "ticker-stop"), slices.Index(ops, "window-destroy")
	if stop < 0 || destroy != len(ops)-1 || stop > destroy || slices.Contains(ops, "timer-kill") {
		t.Errorf("teardown: %v, want ticker-stop before window-destroy and no timer-kill", ops)
	}
}

// TestSessionRecheckStale lets a signal that sets a reason in before the
// recheck of an older one that clears it: the recheck, which carries the
// generation it was posted at, does not undo it. A tick waiting in a held
// query keeps the thread busy until both messages are queued; the
// clearing one then queues its recheck behind the setting one.
func TestSessionRecheckStale(t *testing.T) {
	type msg struct {
		msg            uint32
		wParam, lParam uintptr
	}
	for _, c := range []struct {
		name     string
		world    func(*fakeSystem)
		first    msg // sets the reason
		clear    msg
		set      msg
		want     []string
		reasons  ws.Reason
		sessions int // the session queries of the tick and the recheck
	}{
		{"sleep after wake", nil,
			msg{ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0},
			msg{ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, 0},
			msg{ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0},
			[]string{"barrier"}, ws.Sleep, 1},
		{"end after end canceled", nil,
			msg{ws.WMQueryEndSession, 0, ws.EndSessionLogoff},
			msg{ws.WMEndSession, 0, ws.EndSessionLogoff},
			msg{ws.WMQueryEndSession, 0, ws.EndSessionLogoff},
			[]string{"barrier"}, ws.End, 1},
		// The session query fails, so only the generation keeps Lock.
		{"lock after unlock", func(f *fakeSystem) { f.sessErr = errQuery },
			msg{ws.WMWTSSessionChange, ws.WTSSessionLock, 0},
			msg{ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0},
			msg{ws.WMWTSSessionChange, ws.WTSSessionLock, 0},
			nil, ws.Lock, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := start(t, nil, nil)
			h.send(c.first.msg, c.first.wParam, c.first.lParam)
			h.flush()
			h.rec.take()
			if c.world != nil {
				h.f.set(c.world)
			}
			sessions, _ := h.f.counts()
			hold := h.f.holdQueries()
			h.post(ws.WMTimer, ws.TimerTick, 0)
			h.post(c.clear.msg, c.clear.wParam, c.clear.lParam)
			h.post(c.set.msg, c.set.wParam, c.set.lParam)
			close(hold)
			h.f.set(func(f *fakeSystem) { f.hold = nil })
			h.flush() // the recheck is posted behind this flush's WM_NULL
			h.flush()
			if got := h.rec.take(); !slices.Equal(got, c.want) {
				t.Errorf("%q, want %q", got, c.want)
			}
			if got := h.w.Reasons(); got != c.reasons {
				t.Errorf("Reasons() = %s, want %s", got, c.reasons)
			}
			if s, _ := h.f.counts(); s-sessions != c.sessions {
				t.Errorf("%d session queries, want %d: the recheck did not run", s-sessions, c.sessions)
			}
		})
	}
}

// TestSessionQueryPanicReported makes the session query panic in the
// middle of a step: what the step did before it, clearing the reason or
// reading the desktop, is reported at once, not left in the Tracker for no
// later step to report.
func TestSessionQueryPanicReported(t *testing.T) {
	t.Run("unlock", func(t *testing.T) {
		h := start(t, nil, nil)
		h.send(ws.WMWTSSessionChange, ws.WTSSessionLock, 0)
		h.f.set(func(f *fakeSystem) { f.sessPanic = true })
		h.send(ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0)
		h.flush()
		h.f.set(func(f *fakeSystem) { f.sessPanic = false })
		h.tick()
		if got, want := h.rec.take(), []string{"lock none>lock", "unlock lock>none"}; !slices.Equal(got, want) {
			t.Errorf("%q, want %q", got, want)
		}
		if got := h.w.Reasons(); got != 0 {
			t.Errorf("Reasons() = %s, want none", got)
		}
		if n := len(h.logs.find(slog.LevelError, "session handler panicked")); n != 1 {
			t.Errorf("%d panics at Error, want 1", n)
		}
	})
	t.Run("fifth tick", func(t *testing.T) {
		h := start(t, nil, nil)
		for range 4 {
			h.tick()
		}
		h.f.set(func(f *fakeSystem) { f.other, f.sessPanic = true, true })
		h.tick() // the fifth queries the session after the desktop
		h.f.set(func(f *fakeSystem) { f.sessPanic = false })
		h.tick()
		if got, want := h.rec.take(), []string{"tick none>desktop"}; !slices.Equal(got, want) {
			t.Errorf("%q, want %q", got, want)
		}
		if got := h.w.Reasons(); got != ws.Desktop {
			t.Errorf("Reasons() = %s, want desktop", got)
		}
	})
	// The stale guard runs before the queries: what it cleared is
	// reported, and warned of, once; a later step that panics clears
	// nothing, so it warns of nothing.
	t.Run("stale", func(t *testing.T) {
		h := start(t, nil, nil)
		h.awake.Store(int64(10 * time.Second))
		h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0)
		h.awake.Store(int64(80 * time.Second))
		h.f.set(func(f *fakeSystem) { f.panicking = true })
		h.tick()
		h.tick()
		h.f.set(func(f *fakeSystem) { f.panicking = false })
		if got, want := h.rec.take(), []string{"sleep none>sleep", "barrier", "tick sleep>none"}; !slices.Equal(got, want) {
			t.Errorf("%q, want %q", got, want)
		}
		if got := h.w.Reasons(); got != 0 {
			t.Errorf("Reasons() = %s, want none", got)
		}
		stale := h.logs.find(slog.LevelWarn, "session reasons stale; cleared")
		if len(stale) != 1 || attr(stale[0], "reasons") != "sleep" || attr(stale[0], "signal") != "tick" {
			t.Errorf("stale logged: %v, want sleep once, on a tick", stale)
		}
		// The first panic at Error, the next at Debug.
		if e, d := len(h.logs.find(slog.LevelError, "session handler panicked")), len(h.logs.find(slog.LevelDebug, "session handler panicked")); e != 1 || d != 1 {
			t.Errorf("%d panics at Error and %d at Debug, want 1 and 1", e, d)
		}
	})
}

// TestSessionOneAtATime checks that a process runs one Watcher.
func TestSessionOneAtATime(t *testing.T) {
	h := start(t, nil, nil)
	if w, err := ws.Start(ws.Config{Query: newFake()}); !errors.Is(err, ws.ErrActive) {
		if w != nil {
			_ = w.Close()
		}
		t.Fatalf("a second Start: %v, want ErrActive", err)
	}
	if err := h.w.Close(); err != nil {
		t.Fatal(err)
	}
	start(t, nil, nil)
}

// TestSessionClassAgain unregisters the window class after each Close, as
// Windows does when the process's last GUI thread exits
// (TestSessionClassLastGUIThread): the next Start registers it again.
func TestSessionClassAgain(t *testing.T) {
	for i := range 2 {
		h := start(t, nil, nil)
		if err := h.w.Close(); err != nil {
			t.Fatalf("cycle %d: Close: %v", i, err)
		}
		if err := ws.UnregisterClass(); err != nil {
			t.Fatalf("cycle %d: UnregisterClass: %v", i, err)
		}
	}
	start(t, nil, nil)
}

// classChildEnv set to 1 makes TestSessionClassLastGUIThread the child.
const classChildEnv = "PADMATE_WINSESSION_CLASS_CHILD"

func init() {
	// In the child, main keeps the main thread, which Go never lets exit:
	// LockOSThread in init locks main to it, so the threads of
	// onExitingThread are others.
	if os.Getenv(classChildEnv) == "1" {
		runtime.LockOSThread()
	}
}

// TestSessionClassLastGUIThread runs classChild in a new process, whose
// only GUI threads are the ones the child makes. It failed with "Cannot
// find window class" when the class was registered once per process, as
// Start did in cmd/padmated's tests after the SDL input, whose threads
// were the other GUI threads, had ended.
func TestSessionClassLastGUIThread(t *testing.T) {
	if os.Getenv(classChildEnv) == "1" {
		classChild(t)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSessionClassLastGUIThread$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), classChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestSessionClassLastGUIThread") {
		t.Fatalf("the child: %v\n%s", err, out)
	}
}

// classChild starts and closes a Watcher twice, each time from a new
// thread that then exits. Once the session thread and that thread have
// exited, the process has no GUI thread, and Windows has destroyed its
// window classes; Start registers the class again.
func classChild(t *testing.T) {
	for i := range 2 {
		err := onExitingThread(t, func() error {
			cfg := ws.Config{Query: newFake(), Poll: time.Hour}
			cfg.SetRegister(func(uintptr) error { return nil })
			w, err := ws.Start(cfg)
			if err != nil {
				return err
			}
			return w.Close()
		})
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		// Each look is a GUI thread, which exits before the next.
		deadline := time.Now().Add(wait)
		for onExitingThread(t, func() error {
			if ws.ClassRegistered() {
				return errors.New("registered")
			}
			return nil
		}) != nil {
			if time.Now().After(deadline) {
				t.Fatalf("cycle %d: the class outlived the process's GUI threads by %v", i, wait)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// onExitingThread runs f on a new OS thread and returns f's result once
// that thread has exited.
func onExitingThread(t *testing.T, f func() error) error {
	t.Helper()
	type result struct {
		thread windows.Handle
		err    error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread exits with the goroutine
		h, err := windows.OpenThread(windows.SYNCHRONIZE, false, windows.GetCurrentThreadId())
		if err != nil {
			done <- result{err: fmt.Errorf("OpenThread: %w", err)}
			return
		}
		done <- result{h, f()}
	}()
	r := <-done
	if r.thread == 0 {
		t.Fatal(r.err)
	}
	defer func() { _ = windows.CloseHandle(r.thread) }()
	if ev, err := windows.WaitForSingleObject(r.thread, uint32(wait.Milliseconds())); ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the thread did not exit within %v: %#x %v", wait, ev, err)
	}
	return r.err
}

// TestSessionPanicRecovered makes OnChange and the Querier panic: the
// handlers return what they must, the rest of the handler still runs, the
// first panic is logged at Error and the thread keeps running.
func TestSessionPanicRecovered(t *testing.T) {
	var calls atomic.Int32
	var h *harness
	h = start(t, nil, func(c *ws.Config) {
		c.OnChange = func(ch ws.Change) {
			if calls.Add(1) <= 2 {
				panic("test: OnChange panics")
			}
			h.rec.add(change(ch))
		}
	})
	if got := h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0); got != 1 {
		t.Errorf("PBT_APMSUSPEND returned %d, want TRUE", got)
	}
	h.send(ws.WMWTSSessionChange, ws.WTSSessionLock, 0)
	h.f.set(func(f *fakeSystem) { f.panicking = true })
	h.tick()
	h.f.set(func(f *fakeSystem) { f.panicking = false })
	h.send(ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0)
	h.flush()
	if got, want := h.rec.take(), []string{"barrier", "unlock lock,sleep>sleep"}; !slices.Equal(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	errs := h.logs.find(slog.LevelError, "session handler panicked")
	if len(errs) != 1 || attr(errs[0], "stack") == "" {
		t.Errorf("panics at Error: %v, want one with a stack", errs)
	}
	if n := len(h.logs.find(slog.LevelDebug, "session handler panicked")); n != 2 {
		t.Errorf("%d panics at Debug, want 2", n)
	}
}

// TestSessionThreadDeath ends the session thread by itself three ways
// while locked and asleep: the reasons are not cleared, Sleep excepted,
// which nothing could clear any more; the fallback polls until Close.
func TestSessionThreadDeath(t *testing.T) {
	cases := []struct {
		name string
		kill func(h *harness)
		err  string
	}{
		{"error", func(h *harness) { h.die.Store(dieError); h.post(ws.WMNull, dieMarker, 0) }, errDie.Error()},
		{"panic", func(h *harness) { h.die.Store(diePanic); h.post(ws.WMNull, dieMarker, 0) }, "panicked"},
		{"quit", func(h *harness) { h.post(0x0012, 0, 0) }, "WM_QUIT"}, // WM_QUIT
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := start(t, func(f *fakeSystem) { f.info.Locked = true }, func(c *ws.Config) { c.Poll = 20 * time.Millisecond })
			h.send(ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0)
			if got, want := h.rec.take(), []string{"init none>lock", "sleep lock>lock,sleep", "barrier"}; !slices.Equal(got, want) {
				t.Fatalf("%q, want %q", got, want)
			}
			hwnd := h.w.Window()
			c.kill(h)
			select {
			case <-h.w.Degraded():
			case <-time.After(wait):
				t.Fatal("Degraded is open")
			}
			if err := h.w.Err(); err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Err() = %v, want %q", err, c.err)
			}
			h.rec.waitFor(t, "wake lock,sleep>lock")
			if got := h.w.Reasons(); got != ws.Lock {
				t.Errorf("Reasons() = %s, want lock", got)
			}
			if n := len(h.logs.find(slog.LevelError, "session thread ended; polling")); n != 1 {
				t.Errorf("%d thread ends logged, want 1", n)
			}
			if winsessiontest.IsWindow(hwnd) {
				t.Error("the session window outlived the thread")
			}
			if w, err := ws.Start(ws.Config{Query: newFake()}); !errors.Is(err, ws.ErrActive) {
				if w != nil {
					_ = w.Close()
				}
				t.Errorf("Start while polling: %v, want ErrActive", err)
			}

			h.f.set(func(f *fakeSystem) { f.info.Locked = false })
			h.rec.waitFor(t, "tick lock>none")
			select {
			case <-h.w.Done():
				t.Fatal("Done closed before Close")
			default:
			}
			if err := h.w.Close(); err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Close: %v, want the thread's error", err)
			}
			select {
			case <-h.w.Done():
			default:
				t.Error("Done is open after Close")
			}
		})
	}
}

// TestSessionFallbackPanic makes the fallback's query panic: it gives up
// and clears the reasons, since a watcher that cannot watch must not hold
// the mapping suspended.
func TestSessionFallbackPanic(t *testing.T) {
	h := start(t, func(f *fakeSystem) { f.info.Locked = true }, func(c *ws.Config) { c.Poll = 20 * time.Millisecond })
	h.die.Store(dieError)
	h.post(ws.WMNull, dieMarker, 0)
	<-h.w.Degraded()
	h.f.set(func(f *fakeSystem) { f.panicking = true })
	got := h.rec.waitFor(t, "tick lock>none")
	if want := []string{"init none>lock", "tick lock>none"}; !slices.Equal(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	if n := len(h.logs.find(slog.LevelError, "session polling panicked; reasons cleared")); n != 1 {
		t.Errorf("%d polling panics logged, want 1", n)
	}
	if got := h.w.Reasons(); got != 0 {
		t.Errorf("Reasons() = %s, want none", got)
	}
	if err := h.w.Close(); !errors.Is(err, errDie) {
		t.Errorf("Close: %v, want %v", err, errDie)
	}
}

// TestQueriesReal runs the system's Querier. It checks only what holds
// everywhere: the session is this process's, and outside session 0, where
// services run with no input desktop, the desktop query works.
func TestQueriesReal(t *testing.T) {
	var id uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &id); err != nil {
		t.Fatal(err)
	}
	q := ws.SystemQuerier(slog.New(&logs{}))
	info, err := q.Session()
	var errno syscall.Errno
	switch {
	case errors.As(err, &errno):
		t.Logf("Session: %v; not checked", err)
	case err != nil:
		t.Errorf("Session: %v", err)
	case info.ID != id:
		t.Errorf("session %d, want this process's %d", info.ID, id)
	default:
		t.Logf("session %+v", info)
	}
	other, err := q.Desktop()
	switch {
	case err != nil && id == 0:
		t.Logf("Desktop in session 0: %v", err)
	case err != nil:
		t.Errorf("Desktop: %v", err)
	default:
		t.Logf("desktop other=%v", other)
	}
}
