//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/instance"
)

func TestLiveInstanceBeforeProfileAndLog(t *testing.T) {
	lease, err := acquireInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	path := filepath.Join(t.TempDir(), "padmated.log")
	report := filepath.Join(t.TempDir(), "startup-error.json")
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	code := live(ctx, options{profile: "missing-profile.json", logFile: path, dryRun: true, startupReport: report}, &stdout, &stderr)
	if code != exitStartup || !strings.Contains(stderr.String(), instance.ErrAlreadyRunning.Error()) || strings.Contains(stderr.String(), "missing-profile") {
		t.Fatalf("second instance=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second instance touched log: %v", err)
	}
	// The running instance did not answer (UI-45).
	if data, err := os.ReadFile(report); err != nil || !strings.Contains(string(data), `"kind":"instance","cause":"unavailable"`) {
		t.Fatalf("report %s: %v", data, err)
	}
}

func TestLiveInstanceReleasedAfterStartupError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := live(context.Background(), options{profile: filepath.Join(t.TempDir(), "missing-profile.json"), dryRun: true}, &stdout, &stderr); code != exitStartup {
		t.Fatalf("invalid profile returned %d", code)
	}
	lease, err := acquireInstance(instanceName)
	if err != nil {
		t.Fatalf("startup failure leaked instance: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
