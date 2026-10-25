package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

func TestSecondInstanceForwardsProfile(t *testing.T) {
	for _, library := range []bool{false, true} {
		name := "file"
		if library {
			name = "library"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			data := `{"schemaVersion":0,"name":"first","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13","mode":"hold"}}}}}`
			second := strings.ReplaceAll(strings.ReplaceAll(data, "first", "second"), "F13", "F14")
			firstPath, nextPath := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
			if err := os.WriteFile(firstPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(nextPath, []byte(second), 0o600); err != nil {
				t.Fatal(err)
			}
			firstArgs := []string{"--profile", firstPath, "--dry-run", "--fake-input"}
			nextArgs := []string{"--profile", nextPath, "--dry-run", "--fake-input"}
			if library {
				writeLibraryProfile(t, dir, "first", data)
				writeLibraryProfile(t, dir, "second", second)
				// Switching by app is off: the real foreground, which no
				// rule binds, would select the fixed default (ADR-0053)
				// while the test checks the forwarded selection.
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schemaVersion":0,"enabled":false}`), 0o600); err != nil {
					t.Fatal(err)
				}
				firstArgs = []string{"--data-dir", dir, "--profile-id", "first", "--dry-run", "--fake-input"}
				nextArgs = []string{"--data-dir", dir, "--profile-id", "second", "--dry-run", "--fake-input"}
			}
			// A report left by an earlier start goes once a start maps, or
			// the running instance takes its request (UI-45).
			report := filepath.Join(dir, "startup-error.json")
			stale := func() {
				t.Helper()
				if err := os.WriteFile(report, []byte("stale"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gone := func() {
				t.Helper()
				if _, err := os.Stat(report); !os.IsNotExist(err) {
					t.Fatalf("stale startup report kept: %v", err)
				}
			}
			stale()
			firstArgs = append([]string{"--startup-report", report}, firstArgs...)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			out, errOut := &syncBuffer{}, &syncBuffer{}
			ready, done := make(chan *liveRun, 1), make(chan int, 1)
			go func() {
				done <- runContextUI(ctx, firstArgs, out, errOut, func(r *liveRun) int { ready <- r; return r.wait() })
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case code := <-done:
					if code != 0 {
						t.Errorf("daemon %d: %s", code, errOut.String())
					}
				case <-time.After(3 * time.Second):
					t.Error("daemon did not stop")
				}
			})
			var r *liveRun
			select {
			case r = <-ready:
			case <-ctx.Done():
				t.Fatal(errOut.String())
			}
			gone()
			conn, d, err := ipc.Dial(ctx, ipcDir)
			if err != nil {
				t.Fatal(err)
			}
			client, err := ipc.NewClient(ctx, conn, d, "forward-test", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			call := func(method string) {
				t.Helper()
				if err := client.CallResult(ctx, method, map[string]string{"control": "east"}, new(any)); err != nil {
					t.Fatal(err)
				}
			}
			tl := &testLive{liveRun: r, stdout: out, stderr: errOut, log: errOut}
			call("fake.press")
			tl.waitFor(t, out, "send +F13\n")
			var secondOut, secondErr bytes.Buffer
			stale()
			if code := runContext(ctx, append([]string{"--startup-report", report}, nextArgs...), &secondOut, &secondErr); code != 0 {
				t.Fatalf("forward exit %d: %s", code, &secondErr)
			}
			gone()
			if !strings.Contains(secondOut.String(), "forwarded") {
				t.Fatal("no forwarding acknowledgement", &secondOut)
			}
			tl.waitFor(t, out, "send -F13\n")
			if strings.Contains(out.String(), "send +F14\n") {
				t.Fatal("held input was replayed")
			}
			call("fake.release")
			call("fake.press")
			tl.waitFor(t, out, "send +F14\n")
			call("fake.release")
			tl.waitFor(t, out, "send -F14\n")
			checkBalanced(t, out.String())
			// Invalid selections retain the accepted profile and never open a
			// second local log or output loop.
			before := r.profileTarget.Load()
			badArgs := append([]string{}, nextArgs...)
			if library {
				badArgs = append(badArgs, "--profile-id=missing")
			} else {
				badArgs = append(badArgs, "--profile="+filepath.Join(dir, "missing.json"))
			}
			secondOut.Reset()
			secondErr.Reset()
			if code := runContext(ctx, badArgs, &secondOut, &secondErr); code != exitStartup || r.profileTarget.Load() != before {
				t.Fatalf("bad selection changed state: code %d", code)
			}
			// Mismatched startup options must fail without changing the profile.
			for _, extra := range [][]string{{"--inject", "--dry-run=false", "--fake-input="}, {"--watch=false"}, {"--data-dir", t.TempDir()}} {
				args := append(append([]string{"--startup-report", report}, nextArgs...), extra...)
				secondOut.Reset()
				secondErr.Reset()
				if code := runContext(ctx, args, &secondOut, &secondErr); code != exitStartup {
					t.Fatalf("incompatible args accepted: %v (%d)", extra, code)
				}
				// The report says the running instance refused, in its words,
				// and never has the IPC token.
				data, err := os.ReadFile(report)
				if err != nil || !strings.Contains(string(data), `"kind":"instance","cause":"refused"`) ||
					!strings.Contains(string(data), "differs; restart the daemon") || strings.Contains(string(data), d.Token) {
					t.Fatalf("%v: report %s: %v", extra, data, err)
				}
			}
			// A new standalone path must also become the watched path.
			if !library {
				if err := os.WriteFile(nextPath, []byte(strings.ReplaceAll(second, "F14", "F15")), 0o600); err != nil {
					t.Fatal(err)
				}
				tl.waitFor(t, errOut, "profile reloaded")
				call("fake.press")
				tl.waitFor(t, out, "send +F15\n")
				call("fake.release")
				tl.waitFor(t, out, "send -F15\n")
			}
		})
	}
}
