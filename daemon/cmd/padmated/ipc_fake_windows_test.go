package main

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
)

func TestFakeIPCEntrypointAndCLI(t *testing.T) {
	ctl := injecttest.BuildTool(t, "../padmatectl")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	ready := make(chan *liveRun, 1)
	done := make(chan int, 1)
	go func() {
		done <- runContextUI(ctx, []string{"--profile", basicProfile, "--dry-run", "--fake-input", "--watch=false"}, stdout, stderr, func(r *liveRun) int { ready <- r; return r.wait() })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("fake daemon exit %d: %s", code, stderr.String())
			}
		case <-time.After(3 * time.Second):
			t.Error("fake daemon did not exit")
		}
	})
	var live *liveRun
	select {
	case live = <-ready:
	case <-ctx.Done():
		t.Fatal("fake daemon did not start", stderr.String())
	}
	for _, op := range []string{"press", "release"} {
		cmd := exec.CommandContext(ctx, ctl, "fake", op, "--ipc-dir", ipcDir, "east")
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("CLI %s: %v %s", op, err, data)
		}
	}
	// The shipped basic profile binds east to Space. Both edges must travel
	// through the real CLI, authenticated pipe, supervisor and dry-run sender.
	tl := &testLive{liveRun: live, stdout: stdout, stderr: stderr, log: stderr}
	tl.waitFor(t, stdout, "send +Space\n")
	tl.waitFor(t, stdout, "send -Space\n")
	checkBalanced(t, stdout.String())
}
