package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

const eventsUsage = `usage: padmatectl subscribe [FLAGS] [TOPIC...]
       padmatectl capture [FLAGS]

Prints JSON lines {topic,data} until Ctrl+C. Topics: input, engine.activity,
log, script.status, profile.changed, output.preview, app.foreground,
daemon.exiting, hud (subscribe defaults to all topics). Subscribing to hud
makes scripts see an overlay (host.has("hud")) while the command runs.
capture holds a mapping pause lease on this connection and prints input.
Exiting releases only this capture lease; existing user pauses stay active.
Server event loss is reported as a dropped notification; local overflow
or a disconnected daemon ends the command with exit code 1.

Flags: --ipc-dir DIR, --timeout 10s (setup), --duration D, --count N.
--json is accepted; streaming output is always one JSON object per line.`

func eventCommand(name string) func([]string, io.Writer, io.Writer) int {
	return func(args []string, stdout, stderr io.Writer) int {
		ctx, cancel := signal.NotifyContext(context.Background(), stopSignals...)
		defer cancel()
		return eventsMain(ctx, name, args, stdout, stderr)
	}
}

func eventsMain(ctx context.Context, name string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dir := fs.String("ipc-dir", ipcDiscoveryDir, "daemon discovery directory")
	timeout := fs.Duration("timeout", 10*time.Second, "connection and subscription timeout")
	duration := fs.Duration("duration", 0, "stop after this time")
	count := fs.Int("count", 0, "stop after this many events")
	fs.Bool("json", true, "JSON lines output")
	if code, ok := parseFlags(fs, args, eventsUsage, stdout, stderr); !ok {
		return code
	}
	topics := fs.Args()
	if name == "capture" && len(topics) == 0 {
		topics = []string{"input"}
	}
	valid := name != "capture" || fs.NArg() == 0
	for _, topic := range topics {
		valid = valid && slices.Contains(ipc.Topics, topic)
	}
	if !valid || len(topics) > len(ipc.Topics) || *timeout <= 0 || *timeout > time.Minute || *duration < 0 || *count < 0 {
		fmt.Fprintln(stderr, "padmatectl: invalid subscription arguments\n"+eventsUsage)
		return 2
	}
	if len(topics) == 0 {
		topics = ipc.Topics
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sub, err := ipc.Subscribe(ctx, *dir, *timeout, topics, name == "capture")
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl:", err)
		return 1
	}
	defer sub.Close()
	if name == "capture" {
		fmt.Fprintln(stderr, "padmatectl: capture active; Ctrl+C ends capture")
	}
	if *duration > 0 {
		timer := time.AfterFunc(*duration, cancel)
		defer timer.Stop()
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	for n := 0; *count == 0 || n < *count; n++ {
		row, err := sub.Next(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err == nil {
			err = encoder.Encode(row)
		}
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl:", err)
			return 1
		}
	}
	return 0
}
