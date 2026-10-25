package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

type ownRecorder struct {
	mu     sync.Mutex
	events []output.KeyEvent
}

func (s *ownRecorder) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return len(events), nil
}
func (s *ownRecorder) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.events) }

func ownTestConfig(foreground, alive *atomic.Bool) func(*liveConfig) {
	return func(cfg *liveConfig) {
		cfg.verifyUI = func(uint32, uint32) (string, error) { return "verified-electron-image", nil }
		cfg.ownInstance = func(uint32) (uint64, error) { return 7, nil }
		cfg.ownAlive = func(uint32, string, uint64) bool { return alive.Load() }
		cfg.ownForeground = func(uint32, string) bool { return foreground.Load() }
	}
}

func waitOwnMode(t *testing.T, tl *testLive, want bool) {
	t.Helper()
	tl.waitUntil(t, "own-window mode", func() bool { return tl.ownMode.Load() == want && tl.loop.Blocking()&output.BlockOwnWindow == 0 })
}

func TestOwnWindowLifecycleReleasePreviewAndBridgeFailure(t *testing.T) {
	var foreground, alive atomic.Bool
	alive.Store(true)
	rec := new(ownRecorder)
	tl, src := sessionLive(t, ownTestConfig(&foreground, &alive), func() (output.Sender, error) { return rec, nil }, "--inject", "--watch=false")
	previews := make(chan string, 64)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == "output.preview" {
			previews <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := func(method string, p any) {
		t.Helper()
		if err := client.CallResult(ctx, method, p, new(any)); err != nil {
			t.Fatal(method, err)
		}
	}
	call("subscribe", map[string]any{"topics": []string{"output.preview"}})
	call("ui.register", map[string]uint32{"pid": 42})
	button(src, "east", true)
	tl.waitUntil(t, "real hold", func() bool { return rec.count() == 1 })
	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, true)
	if rec.count() != 2 {
		t.Fatal("entering UI did not release physical hold", rec.count())
	}
	// Already-held control is canceled, then a fresh tap reaches virtual
	// output and the exact same resolver/Arbiter preview path.
	button(src, "east", false)
	button(src, "south", true)
	button(src, "south", false)
	tl.waitUntil(t, "mapping preview", func() bool { return len(previews) > 1 })
	before := rec.count()
	_ = client.Close()
	// A bridge failure while its parent UI remains foreground is fail closed.
	if err := tl.loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !tl.ownMode.Load() {
		t.Fatal("bridge disconnect reopened real injection")
	}
	button(src, "east", true)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != before {
		t.Fatal("UI foreground injected after bridge failure")
	}
	// UI process death (including a reused PID with another creation time)
	// ends that process-instance lease. A held virtual key is never replayed.
	alive.Store(false)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, false)
	button(src, "east", false)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != before {
		t.Fatal("leaving UI replayed held preview")
	}
	button(src, "east", true)
	tl.waitUntil(t, "fresh real press", func() bool { return rec.count() == before+1 })
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Fatal(code)
	}
	if rec.count() != before+2 {
		t.Fatal("exit left a physical key held")
	}
}

func TestIPCConfigOwnOutputAppliesWithoutRestart(t *testing.T) {
	tl, _ := startFocusLive(t, t.TempDir(), "", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	var saved rpcSaved
	if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": `{"schemaVersion":0,"output":{"sendToOwnWindow":true}}`}, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Applied || saved.RestartRequired || !tl.ownSend.Load() {
		t.Fatalf("own output setting did not apply: %+v", saved)
	}
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || doc.RestartRequired {
		t.Fatal(doc, err)
	}
}

func TestIPCConfigWritesWaitForPreviousApplication(t *testing.T) {
	tl, _ := startFocusLive(t, t.TempDir(), "", `{"schemaVersion":0}`, false)
	first := rpcTestClient(t, tl.liveRun)
	second := rpcTestClient(t, tl.liveRun)
	reader := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var initial rpcDocument
	if err := first.CallResult(ctx, "config.get", nil, &initial); err != nil {
		t.Fatal(err)
	}
	// Stop T-output before the first committed config can finish applying.
	// Reads remain available; a second writer must wait for that application.
	entered, resume, stopped := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(unblock)
	go func() {
		stopped <- tl.loop.Do(func(*output.Arbiter) error {
			close(entered)
			<-resume
			return nil
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	textA := `{"schemaVersion":0,"output":{"sendToOwnWindow":false}}`
	textB := `{"schemaVersion":0,"output":{"sendToOwnWindow":true}}`
	revisionA := profilelib.Revision([]byte(textA))
	type reply struct {
		saved rpcSaved
		err   error
	}
	put := func(client *jrpc2.Client, text, revision string) <-chan reply {
		done := make(chan reply, 1)
		go func() {
			var saved rpcSaved
			err := client.CallResult(ctx, "config.put", map[string]string{"text": text, "revision": revision}, &saved)
			done <- reply{saved: saved, err: err}
		}()
		return done
	}
	doneA := put(first, textA, initial.Revision)
	var current rpcDocument
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	for current.Revision != revisionA {
		if err := second.CallResult(ctx, "config.get", nil, &current); err != nil {
			t.Fatal(err)
		}
		select {
		case <-poll.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	doneB := put(second, textB, revisionA)
	// This spans two own-window ticks, enough for an unsynchronized writer
	// to commit while T-output remains deliberately blocked.
	timer := time.NewTimer(120 * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-doneB:
		t.Fatalf("second config completed before first application: %+v", result)
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := reader.CallResult(ctx, "config.get", nil, &current); err != nil {
		t.Fatal(err)
	}
	if current.Revision != revisionA {
		t.Fatal("second config was committed before the first application finished")
	}
	unblock()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan reply{doneA, doneB} {
		select {
		case result := <-done:
			if result.err != nil || !result.saved.Applied {
				t.Fatalf("config application failed: %+v", result)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := second.CallResult(ctx, "config.get", nil, &current); err != nil {
		t.Fatal(err)
	}
	if current.Revision != profilelib.Revision([]byte(textB)) || !tl.ownSend.Load() {
		t.Fatal("final config revision and runtime output switch disagree")
	}
}

func TestOwnWindowCancelsPendingScriptTapAndContinuation(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(profilePath, []byte(`{"schemaVersion":0,"name":"script preview","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `import {defineScript,key,every} from "padmate";
export default defineScript({onActivate(){
 void key.tap(["F13","F14"],{holdMs:200}).then(()=>{key.down("F15");});
},setup(){
 every(20,()=>{key.down("F16");});
}});`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var foreground, alive atomic.Bool
	alive.Store(true)
	rec := new(ownRecorder)
	tl, src := sessionLive(t, ownTestConfig(&foreground, &alive), func() (output.Sender, error) { return rec, nil }, "--inject", "--profile", profilePath, "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "ui.register", map[string]uint32{"pid": 42}, new(any)); err != nil {
		t.Fatal(err)
	}
	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, true)
	button(src, "south", true)
	tl.waitUntil(t, "script preview tap pending", func() bool {
		pending := false
		_ = tl.loop.Do(func(a *output.Arbiter) error {
			for _, key := range a.Held() {
				if key.Press.Key.String() == "F13" {
					pending = true
				}
			}
			return nil
		})
		return pending
	})
	before := rec.count()
	foreground.Store(false)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, false)
	// This spans both the original tap deadline and several every deadlines.
	time.Sleep(250 * time.Millisecond)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(); got != before {
		t.Fatalf("old script emitted %d physical events after leaving preview", got-before)
	}
}

func TestOwnWindowFailedBarrierAndRapidReversalStayClosed(t *testing.T) {
	var foreground, alive, reject atomic.Bool
	alive.Store(true)
	reject.Store(true)
	var attempts atomic.Uint64
	rec := new(ownRecorder)
	configure := ownTestConfig(&foreground, &alive)
	tl, src := sessionLive(t, func(cfg *liveConfig) {
		configure(cfg)
		cfg.ownBarrier = func(release func() error) error {
			attempts.Add(1)
			if reject.Load() {
				return errors.New("test: owner cancellation barrier failed")
			}
			return release()
		}
	}, func() (output.Sender, error) { return rec, nil }, "--inject", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "ui.register", map[string]uint32{"pid": 42}, new(any)); err != nil {
		t.Fatal(err)
	}
	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	tl.waitUntil(t, "failed owner barrier", func() bool { return attempts.Load() > 0 })
	if tl.loop.Blocking()&output.BlockOwnWindow == 0 {
		t.Fatal("failed barrier reopened output")
	}
	// Two new transitions arrive while the old cancellation cannot finish.
	// An obsolete epoch must not release the new transition's independent gate.
	foreground.Store(false)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	button(src, "east", true)
	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if tl.ownEpoch.Load() < 3 || tl.loop.Blocking()&output.BlockOwnWindow == 0 || rec.count() != 0 {
		t.Fatal("rapid reversal escaped cancellation fence")
	}
	reject.Store(false)
	waitOwnMode(t, tl, true)
	button(src, "east", false)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Fatal("failed transition replayed held input")
	}
}

func TestOwnWindowExplicitOutputSwitchReleasesBothDestinations(t *testing.T) {
	var foreground, alive atomic.Bool
	foreground.Store(true)
	alive.Store(true)
	rec := new(ownRecorder)
	tl, src := sessionLive(t, ownTestConfig(&foreground, &alive), func() (output.Sender, error) { return rec, nil }, "--inject", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := func(method string, p any) {
		t.Helper()
		if err := client.CallResult(ctx, method, p, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	call("ui.register", map[string]uint32{"pid": 42})
	waitOwnMode(t, tl, true)
	button(src, "east", true)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	call("ui.setOutput", map[string]bool{"enabled": true})
	waitOwnMode(t, tl, false)
	if rec.count() != 0 {
		t.Fatal("enabling physical output replayed virtual held key")
	}
	button(src, "east", false)
	button(src, "east", true)
	tl.waitUntil(t, "explicit own-window physical output", func() bool { return rec.count() == 1 })
	call("ui.setOutput", map[string]bool{"enabled": false})
	waitOwnMode(t, tl, true)
	if rec.count() != 2 {
		t.Fatal("disabling own-window output failed to release physical hold")
	}
}
