package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keycapture"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ownwindow"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

func runKeyboardCapture(args []string, stdout, stderr io.Writer) int {
	encoder := json.NewEncoder(stdout)
	fail := func(reason string) int {
		// Only a fixed stage identifier crosses into Electron. Do not include
		// IPC discovery, tokens, document paths or user keyboard input.
		fmt.Fprintln(stderr, "keyboard capture:", reason)
		_ = encoder.Encode(keycapture.Event{Active: false, Reason: reason})
		return 1
	}
	fs := flag.NewFlagSet("keyboard-capture", flag.ContinueOnError)
	dir := fs.String("ipc-dir", ipcDiscoveryDir, "private discovery directory")
	pid := fs.Uint("ui-pid", 0, "foreground Electron main PID")
	if code, ok := parseFlags(fs, args, "usage: padmatectl keyboard-capture --ui-pid PID [--ipc-dir DIR]\n", stdout, stderr); !ok {
		return code
	}
	if fs.NArg() != 0 || *pid == 0 || uint64(*pid) > uint64(^uint32(0)) {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// EOF, parent death and session timeout all close this helper's own IPC
	// lease; they cannot resume a user pause or another capture owner.
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	conn, d, err := ipc.Dial(ctx, *dir)
	if err != nil {
		return fail("connection-unavailable")
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	client, err := ipc.NewClient(ctx, conn, d, "keyboard-capture", &jrpc2.ClientOptions{OnStop: func(_ *jrpc2.Client, _ error) { cancel() }})
	if err != nil {
		return fail("authentication-failed")
	}
	defer client.Close()
	// ui.register verifies helper parent, executable paths and user identity.
	if err = client.CallResult(ctx, "ui.register", map[string]uint32{"pid": uint32(*pid)}, new(any)); err != nil {
		return fail("identity-verification-failed")
	}
	if ownwindow.ForegroundPID() != uint32(*pid) {
		return fail("foreground-unavailable")
	}
	if err = client.CallResult(ctx, "capture.start", nil, new(any)); err != nil {
		return fail("mapping-capture-failed")
	}
	hook, err := winhook.Start(nil)
	if err != nil {
		return fail("hook-unavailable")
	}
	defer func() { _ = hook.Close() }()
	if err = encoder.Encode(keycapture.Event{Active: true}); err != nil {
		return 1
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	if err := keycapture.Observe(ctx, hook.Events(), ticker.C, func() bool { return ownwindow.ForegroundPID() == uint32(*pid) && hook.Dropped() == 0 }, func(event keycapture.Event) error { return encoder.Encode(event) }); err != nil {
		return 1
	}
	return 0
}
