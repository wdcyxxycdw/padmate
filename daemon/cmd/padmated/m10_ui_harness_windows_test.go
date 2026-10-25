package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// TestM10UIHarness is only present in the test executable. It cannot enable
// injection and requires explicit isolated filesystem and pipe locations.
// It starts live mode as padmated does, through runContext, on the
// harness's pipe and discovery folder, with an instance lease of that pipe
// of its own: a second harness of the same E2E fixture forwards to the
// first, as a second padmated would.
//
// PADMATE_M10_STARTUP_REPORT, an absolute path, is the --startup-report of
// the start (UI-45): the UI passes it in the environment, as a test
// executable takes no flags of padmated's. When the start fails, the
// executable exits as padmated does, 2 or exitStartup, after the report is
// written; an E2E makes it fail with a config.json in
// PADMATE_M10_DATA_DIR that cannot be read, a folder: one with errors is
// repaired (PRO-29).
func TestM10UIHarness(t *testing.T) {
	if os.Getenv("PADMATE_M10_HARNESS") != "1" {
		t.Skip("Electron E2E subprocess only")
	}
	dir, pipe, data := os.Getenv("PADMATE_M10_IPC_DIR"), os.Getenv("PADMATE_M10_PIPE"), os.Getenv("PADMATE_M10_DATA_DIR")
	report := os.Getenv("PADMATE_M10_STARTUP_REPORT")
	if !filepath.IsAbs(dir) || !filepath.IsAbs(data) || pipe == "" || report != "" && !filepath.IsAbs(report) {
		t.Fatal("private M10 harness locations required")
	}
	sum := sha256.Sum256([]byte(strings.ToLower(pipe)))
	savedPipe, savedDir, savedInstance := ipcPipe, ipcDir, instanceName
	ipcPipe, ipcDir, instanceName = pipe, dir, `Local\padmate-m10-harness-`+hex.EncodeToString(sum[:8])
	t.Cleanup(func() { ipcPipe, ipcDir, instanceName = savedPipe, savedDir, savedInstance })
	args := []string{"--data-dir", data, "--fake-input=ipc", "--dry-run", "--watch=false", "--tray=false"}
	if report != "" {
		args = append([]string{"--startup-report", report}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch code := runContext(ctx, args, os.Stdout, os.Stderr); code {
	case 0:
	case 2, exitStartup:
		harnessExit = code
	default:
		t.Fatal("harness daemon exit", code)
	}
}

// TestM10UIHarnessStartupReport runs the harness as the E2E does, in a
// process of its own (UI-45): with a config.json it cannot read (a
// folder) it exits exitStartup and writes the report; with a config.json
// that has errors, which it repairs (PRO-29), it removes the stale report
// and serves the E2E's pipe until it is killed, as the E2E's cleanup
// does.
func TestM10UIHarnessStartupReport(t *testing.T) {
	if os.Getenv("PADMATE_M10_HARNESS") == "1" {
		t.Skip("the harness itself")
	}
	root := t.TempDir()
	data, run, temp := filepath.Join(root, "data"), filepath.Join(root, "run"), filepath.Join(root, "temp")
	report := filepath.Join(root, "report", "startup-error.json")
	for _, d := range []string{data, temp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(data, "config.json")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	pipe := `\\.\pipe\padmate-m10-harness-test-` + rand.Text()
	harness := func(out *syncBuffer) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestM10UIHarness$", "-test.v")
		// The harness's own TestMain folder goes into temp, which the test
		// removes, also after the kill.
		cmd.Env = append(os.Environ(), "PADMATE_M10_HARNESS=1", "PADMATE_M10_IPC_DIR="+run, "PADMATE_M10_PIPE="+pipe,
			"PADMATE_M10_DATA_DIR="+data, "PADMATE_M10_STARTUP_REPORT="+report, "TMP="+temp, "TEMP="+temp)
		cmd.Stdout, cmd.Stderr = out, out
		return cmd
	}

	out := &syncBuffer{}
	err := harness(out).Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != exitStartup {
		t.Fatalf("config.json a folder: %v, want exit %d\n%s", err, exitStartup, out)
	}
	var rep startupReport
	if text, err := os.ReadFile(report); err != nil || json.Unmarshal(text, &rep) != nil {
		t.Fatalf("report %q: %v", text, err)
	}
	if rep.Kind != "config" || rep.Cause != "other" || rep.File != config || rep.Message == "" {
		t.Fatalf("report %+v", rep)
	}

	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":2000}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out = &syncBuffer{}
	cmd := harness(out)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var status struct {
		FakeInput bool `json:"fakeInput"`
		DryRun    bool `json:"dryRun"`
	}
	for {
		conn, d, err := ipc.Dial(ctx, run)
		if err == nil {
			client, cerr := ipc.NewClient(ctx, conn, d, "harness-test", nil)
			if cerr != nil {
				t.Fatal(cerr)
			}
			err = client.CallResult(ctx, "status.get", nil, &status)
			_ = client.Close()
			if err != nil || d.PID != uint32(cmd.Process.Pid) {
				t.Fatalf("status.get: %v; discovery PID %d, harness %d", err, d.PID, cmd.Process.Pid)
			}
			break
		}
		select {
		case err := <-exited:
			exited <- err
			t.Fatalf("harness exited: %v\n%s", err, out)
		case <-ctx.Done():
			t.Fatalf("harness never answered: %v\n%s", err, out)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !status.FakeInput || !status.DryRun {
		t.Fatalf("status %+v, want the fake input of a dry run", status)
	}
	if _, err := os.Stat(report); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale report kept: %v", err)
	}
}
