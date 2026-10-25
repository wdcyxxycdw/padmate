package main

import (
	"context"
	"debug/pe"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/autostart"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// Build this package with go test -c -ldflags=-H=windowsgui and supply its
// absolute path in PADMATE_TEST_GUI_STARTUP. The child uses fake input only.
func TestStartupGUIWithoutStandardHandles(t *testing.T) {
	exe := os.Getenv("PADMATE_TEST_GUI_STARTUP")
	if exe == "" {
		t.Skip("requires separately built GUI test executable")
	}
	image, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	header, ok := image.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		_ = image.Close()
		t.Fatal("requires a real GUI subsystem test executable")
	}
	if err = image.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")
	env := append(os.Environ(), "PADMATE_GUI_CHILD=1", "PADMATE_GUI_MARKER="+marker)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	proc, err := os.StartProcess(exe, []string{exe, "-test.run=^TestStartupGUIChild$"}, &os.ProcAttr{Dir: cwd, Env: env, Files: []*os.File{nil, nil, nil}, Sys: &syscall.SysProcAttr{HideWindow: true}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		state, err := proc.Wait()
		if err == nil && !state.Success() {
			err = &os.PathError{Op: "GUI child exit", Path: exe, Err: syscall.EINVAL}
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		_ = proc.Kill()
		<-done
		t.Fatal("GUI child hung")
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("GUI did not remain alive through fake mapping")
	}
}

func TestStartupGUIChild(t *testing.T) {
	if os.Getenv("PADMATE_GUI_CHILD") != "1" {
		t.Skip("isolated GUI child")
	}
	if _, err := os.Stdout.Stat(); err == nil {
		t.Fatal("child unexpectedly has usable stdout")
	}
	t.Setenv("APPDATA", t.TempDir())
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	args, err := startupArgs(cfg.opts)
	if err != nil {
		t.Fatal(err)
	}
	ref := startupFixture(t, args)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err = autostart.Expand([]string{ref}, exe)
	if err != nil {
		t.Fatal(err)
	}
	opts, _, ok := parseFlags(args, tl.stderr, tl.stderr)
	if !ok || opts.inject || !opts.dryRun {
		t.Fatal("startup changed output mode")
	}
	cfg.opts = opts
	cfg.stdout = desktopOutput(os.Stdout)
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	tl.addPad(t, src)
	button(src, "east", true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tl.end.Done():
		t.Fatal("dry-run exited on absent stdout")
	case <-time.After(300 * time.Millisecond):
	}
	button(src, "east", false)
	if err = tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Fatal(code)
	}
	if err = os.WriteFile(os.Getenv("PADMATE_GUI_MARKER"), []byte("fake dry-run survived without handles"), 0600); err != nil {
		t.Fatal(err)
	}
}
