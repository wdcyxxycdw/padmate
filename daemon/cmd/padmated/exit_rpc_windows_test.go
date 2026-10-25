//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// TestDaemonExitRepliesBeforeStopping: over the real pipe, the caller of
// daemon.exit gets its reply before padmated closes the connection and takes
// the exit path, whether the caller closes the connection after the reply
// (padmatectl quit does) or keeps it open; the discovery file is gone once
// padmated has stopped, and every key has been released.
func TestDaemonExitRepliesBeforeStopping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close bool
	}{{"caller closes", true}, {"caller stays", false}} {
		t.Run(tc.name, func(t *testing.T) {
			// A few rounds: the reply must not lose a race with the shutdown.
			for round := range 4 {
				e, err := ipc.Listen(`\\.\pipe\padmate-exit-rpc-`+rand.Text(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = e.Close() })
				tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
				src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
				tl.waitFor(t, tl.stdout, "send +Space\n")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				conn, d, err := ipc.Dial(ctx, e.Dir)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				client, err := ipc.NewClient(ctx, conn, d, "exit-rpc-test", nil)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				stopped := stoppedCode(tl)
				got, err := callExit(client, map[string]string{"reason": "installer"})
				if err != nil || got["exiting"] != true {
					cancel()
					t.Fatalf("round %d: daemon.exit = %v, %v", round, got, err)
				}
				if tc.close {
					_ = client.Close()
				}
				select {
				case code := <-stopped:
					if code != 0 {
						t.Fatalf("round %d: exit code %d\n%s", round, code, tl.stderr)
					}
				case <-time.After(liveWait):
					t.Fatalf("round %d: daemon.exit did not stop padmated", round)
				}
				if _, err := os.Stat(filepath.Join(e.Dir, "daemon.json")); !os.IsNotExist(err) {
					t.Fatalf("round %d: discovery left after the exit: %v", round, err)
				}
				checkBalanced(t, tl.stdout.String())
				_ = client.Close()
				cancel()
			}
		})
	}
}
