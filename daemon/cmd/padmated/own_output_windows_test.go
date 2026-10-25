package main

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestOwnWindowToProbe exercises the production --inject sender under the
// verified canary/fence hook. Every key is swallowed; no foreground app receives
// even the baseline real keys. Local dry-run is independent of global mode.
func TestOwnWindowToProbe(t *testing.T) {
	injecttest.Require(t)
	pid := uint32(os.Getpid())
	rec := injecttest.StartFunc(t, func(n uint32) bool { return n == pid })
	rec.Canary(t, pid)
	var foreground, alive atomic.Bool
	alive.Store(true)
	var tl *testLive
	fenced := false
	t.Cleanup(func() {
		if !fenced {
			stopFenced(t, rec, pid, tl)
		}
	})
	var src *fakeSource
	tl, src = sessionLive(t, ownTestConfig(&foreground, &alive), nil, "--inject", "--watch=false")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "ui.register", map[string]uint32{"pid": 42}, new(any)); err != nil {
		t.Fatal(err)
	}
	button(src, "east", true)
	waitKeys(t, rec, pid, 1)
	foreground.Store(true)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, true)
	waitKeys(t, rec, pid, 2)
	button(src, "east", false)
	button(src, "south", true)
	button(src, "south", false)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// Finish any delayed tap before returning to physical output.
	time.Sleep(50 * time.Millisecond)
	foreground.Store(false)
	_ = tl.loop.Do(func(*output.Arbiter) error { return nil })
	waitOwnMode(t, tl, false)
	code := stopFenced(t, rec, pid, tl)
	fenced = true
	if code != 0 {
		t.Fatal(code)
	}
	got, _ := ownKeys(rec, pid)
	if len(got) != 2 {
		t.Fatalf("own-window local dry run leaked %d extra events: %v", len(got)-2, got)
	}
}
