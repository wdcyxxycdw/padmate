package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

const bridgeUsage = `usage: padmatectl ui-bridge --ui-pid PID [--ipc-dir DIR] [--timeout 10s]
Long-lived private Electron main-process bridge. Input: {id,method,params}.
Output: {ready:true,pid,protocol}, {id,result}, {id,error}, or {topic,data}.
EOF disconnects the bridge; the daemon continues running. The discovery token
is never written to stdout. Only the UI's fixed method allowlist is accepted.`

func bridgeMethod(method string) bool {
	return slices.Contains([]string{
		"status.get", "devices.list", "engine.pause", "engine.resume", "engine.releaseAll",
		// M17 identify a controller (IN-13): the daemon lets the controller buzz
		// and flash; the selector is the DeviceKey of a row of the settings page.
		"devices.identify",
		// M25 the mapping wizard (IN-17, IN-18, IN-19, ADR-0072): the elements of a
		// controller, and a mapping for it that the daemon saves in config.json and
		// gives to SDL.
		"devices.rawInfo", "devices.setMapping", "devices.resetMapping",
		// M22 the latency statistics of the diagnostics page (DMN-15, ADR-0071).
		"latency.get", "latency.reset",
		"config.get", "config.put",
		// M13 a repaired config.json (PRO-29): the UI has shown the dialog.
		"config.repairSeen",
		"profiles.list", "profiles.get", "profiles.put", "profiles.validate", "profiles.explain",
		// M21 profile inheritance (PRO-19): a profile with its parents merged in.
		"profiles.resolve",
		// M13 library management (UI-17/18) and window capture (PRO-16).
		"profiles.activate", "profiles.delete", "profiles.copy", "apps.recent",
		// M13 saved versions of profile.json (PRO-17).
		"profiles.history", "profiles.restore",
		// M13 back up one profile as an archive and add one (PRO-20): the
		// daemon reads and writes the files, Electron main moves the bytes.
		"profiles.export", "profiles.import",
		// M25 import of an AntiMicroX profile (PRO-21, ADR-0072): Electron main
		// reads the file and passes its bytes; the daemon reads and checks it.
		"profiles.importAmgp",
		// M13 scripts page: new and import script, open the profile folder.
		// profiles.folder returns a path for Electron main only.
		"script.init", "script.import", "profiles.folder",
		"script.status", "script.reload", "script.logs", "script.describe", "script.registrations",
		// M13 macros (SCR-50): the scripts page, its menus and the editor.
		// macros.path and script.path return a path for Electron main only.
		"macros.list", "macros.create", "macros.import", "macros.rename", "macros.delete", "macros.path",
		"script.path", "script.detach",
		"subscribe", "ui.setOutput",
		// M13 tray exit (DMN-22): the UI has saved its last edit and quits.
		"ui.exitReady",
		// M13 settings (UI-19): recording a new emergency combo pauses the
		// mapping while input events still flow; the bridge connection owns
		// the capture, which ends when it closes.
		"capture.start", "capture.stop",
		// M13 start at sign-in (UI-19, ADR-0052): the daemon writes its own
		// command; neither method takes an executable or arguments.
		"startup.get", "startup.set",
	}, method)
}

type bridgeRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func runUIBridge(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), stopSignals...)
	defer cancel()
	return uiBridgeMain(ctx, args, os.Stdin, stdout, stderr)
}

func uiBridgeMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ui-bridge", flag.ContinueOnError)
	dir := fs.String("ipc-dir", ipcDiscoveryDir, "private daemon discovery directory")
	timeout := fs.Duration("timeout", 10*time.Second, "handshake timeout")
	uiPID := fs.Uint("ui-pid", 0, "Electron main process PID")
	if code, ok := parseFlags(fs, args, bridgeUsage, stdout, stderr); !ok {
		return code
	}
	if fs.NArg() != 0 || *uiPID == 0 || uint64(*uiPID) > uint64(^uint32(0)) || *timeout <= 0 || *timeout > time.Minute {
		fmt.Fprintln(stderr, "padmatectl: invalid UI bridge arguments")
		return 2
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	setup, endSetup := context.WithTimeout(ctx, *timeout)
	defer endSetup()
	conn, d, err := ipc.Dial(setup, *dir)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl: UI bridge connection unavailable:", err)
		return 1
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	rows := make(chan any, 128)
	stopped := make(chan error, 1)
	client, err := ipc.NewClient(setup, conn, d, "padmate-ui", &jrpc2.ClientOptions{
		OnNotify: func(req *jrpc2.Request) {
			n := ipc.Notification{Topic: req.Method(), Data: json.RawMessage(req.ParamString())}
			select {
			case rows <- n:
			default:
				select {
				case stopped <- ipc.ErrEventOverflow:
				default:
				}
				cancel()
			}
		},
		OnStop: func(_ *jrpc2.Client, err error) {
			select {
			case stopped <- err:
			default:
			}
			cancel()
		},
	})
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl: UI bridge authentication failed")
		return 1
	}
	defer client.Close()
	if err := client.CallResult(setup, "ui.register", map[string]uint32{"pid": uint32(*uiPID)}, new(any)); err != nil {
		fmt.Fprintln(stderr, "padmatectl: UI bridge registration failed:", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"ready": true, "pid": d.PID, "protocol": d.Protocol}); err != nil {
		return 1
	}
	requests := make(chan []byte, 1)
	inputDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdin)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			data := bytes.Clone(scanner.Bytes())
			select {
			case requests <- data:
			case <-ctx.Done():
				return
			}
		}
		inputDone <- scanner.Err()
		// Disconnect cleanup must not wait for a blocked stdout writer.
		cancel()
	}()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-inputDone:
				if err != nil {
					fmt.Fprintln(stderr, "padmatectl: UI bridge input:", err)
					return 1
				}
				return 0
			default:
			}
			// The daemon closed the connection, or a signal stops the bridge:
			// what the daemon sent before, daemon.exiting above all (DMN-22),
			// still goes out. The client delivers each notification on a
			// goroutine of its own, which may run after OnStop; Close waits
			// for them.
			_ = client.Close()
			for drained := false; !drained; {
				select {
				case row := <-rows:
					if err := encoder.Encode(row); err != nil {
						return 1
					}
				default:
					drained = true
				}
			}
			select {
			case err := <-stopped:
				if err != nil {
					fmt.Fprintln(stderr, "padmatectl: UI bridge disconnected:", err)
				}
				return 1
			default:
				return 0
			}
		case row := <-rows:
			if err := encoder.Encode(row); err != nil {
				return 1
			}
		case data := <-requests:
			var req bridgeRequest
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&req)
			if err == nil {
				var extra any
				if decoder.Decode(&extra) != io.EOF {
					err = errors.New("one bridge request per line required")
				}
			}
			if len(req.ID) == 0 || bytes.Equal(req.ID, []byte("null")) || len(req.ID) > 64 {
				err = errors.New("bridge request requires a bounded id")
			}
			var result json.RawMessage
			if err == nil && !bridgeMethod(req.Method) {
				err = &jrpc2.Error{Code: jrpc2.MethodNotFound, Message: "method is not allowed by the UI bridge"}
			}
			if err == nil {
				call, stop := context.WithTimeout(ctx, 10*time.Second)
				err = client.CallResult(call, req.Method, req.Params, &result)
				stop()
			}
			reply := map[string]any{"id": req.ID}
			if err != nil {
				rpcErr := &jrpc2.Error{Code: jrpc2.InvalidParams, Message: err.Error()}
				var remote *jrpc2.Error
				if errors.As(err, &remote) {
					rpcErr = remote
				}
				reply["error"] = rpcErr
			} else {
				reply["result"] = result
			}
			if err := encoder.Encode(reply); err != nil {
				return 1
			}
		}
	}
}
