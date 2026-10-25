package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

const usageHead = `usage: spk01 [--preset NAME] [--hint NAME=VALUE]... [--duration D]
             [--log FILE] [--virtual-rate HZ]

The SPK-01 spike: padmated's input side in its real shape. The main
thread runs the tray (an icon with a Quit item), T-input reads SDL on its
own thread, and the process has no window that can take the focus. spk01
waits 500ms for the connected devices, records every event for
--duration and exits. SDL is loaded from $PADMATE_SDL3, or else from
SDL3.dll next to spk01 ("task spk01" builds bin/spk01.exe next to the
pinned DLL).

At the end it writes a summary: the event count, the dropped count and
the CPU time of the process (GetProcessTimes) divided by the wall time,
over the recording window and over the process lifetime, as a share of
one core and of all logical CPUs. The summary goes to stderr, which a
GUI program only has when it is redirected, and to --log.

It exits 0; 1 when events were dropped, the events of the virtual
gamepad did not all arrive, or anything failed; 2 on bad flags.

flags:
  --preset NAME      the SDL hint preset (default "default"; see below)
  --hint N=V         set SDL hint N to V, after the preset; repeatable
  --duration D       the recording window, such as 10s (default 10s)
  --log FILE         write JSON lines to FILE, each with a "type": a
                     "header" (SDL version, library, hints in effect,
                     threads, devices), an "event" per event (the fields
                     of "padmatectl monitor --json") and the "summary"
  --virtual-rate HZ  attach an SDL virtual gamepad and change its state
                     HZ times per second during the window, cycling
                     south down, south up, leftx 16384, leftx 0 and
                     ending the cycle after it; at most 10000 (default
                     0: no virtual gamepad)

presets:
`

// usage lists the presets after usageHead.
var usage = usageHead + input.PresetUsage()

const (
	defaultDuration = 10 * time.Second
	// maxRate bounds --virtual-rate. Each state change is a round trip to
	// T-input, so much higher rates are not reached anyway.
	maxRate = 10000
	// trayWait bounds how long the end waits for the tray to be set up
	// before it quits the tray loop.
	trayWait = 2 * time.Second
	// exitGrace is how long the tray loop has to end after systray.Quit
	// before the process exits anyway.
	exitGrace = 3 * time.Second
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// options are the parsed flags.
type options struct {
	preset   string
	hints    []input.Hint // the preset's, then the --hint flags
	duration time.Duration
	log      string
	rate     int
}

// parseFlags parses and checks the flags. When ok is false, spk01 exits
// with code.
func parseFlags(args []string, stdout, stderr io.Writer) (opts options, code int, ok bool) {
	fs := flag.NewFlagSet("spk01", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage is printed below, to the right stream
	var sdlFlags input.SDLFlags
	sdlFlags.AddFlags(fs)
	fs.DurationVar(&opts.duration, "duration", defaultDuration, "the recording window")
	fs.StringVar(&opts.log, "log", "", "the JSON lines file")
	fs.IntVar(&opts.rate, "virtual-rate", 0, "virtual gamepad state changes per second")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return opts, 0, false
	case err != nil:
		fmt.Fprint(stderr, usage)
		return opts, 2, false
	}
	hints, herr := sdlFlags.Hints()
	var problem string
	switch {
	case fs.NArg() > 0:
		problem = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	case herr != nil:
		problem = herr.Error()
	case opts.duration <= 0:
		problem = "--duration must be positive"
	case opts.rate < 0 || opts.rate > maxRate:
		problem = fmt.Sprintf("--virtual-rate must be between 0 and %d", maxRate)
	}
	if problem != "" {
		fmt.Fprintf(stderr, "spk01: %s\n", problem)
		fmt.Fprint(stderr, usage)
		return opts, 2, false
	}
	opts.preset, opts.hints = sdlFlags.Preset, hints
	return opts, 0, true
}

// stopper ends the recording window early, once, with a reason.
type stopper struct {
	once   sync.Once
	c      chan struct{} // closed by stop
	reason string        // set before c is closed
}

func newStopper() *stopper { return &stopper{c: make(chan struct{})} }

func (s *stopper) stop(reason string) {
	s.once.Do(func() {
		s.reason = reason
		close(s.c)
	})
}

// run is spk01. The calling goroutine must be the main one, locked to the
// main OS thread (systray's init does that), because it runs the tray
// loop.
func run(args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseFlags(args, stdout, stderr)
	if !ok {
		return code
	}
	var logf *os.File
	if opts.log != "" {
		f, err := os.Create(opts.log)
		if err != nil {
			fmt.Fprintf(stderr, "spk01: %v\n", err)
			return 1
		}
		logf = f
	}
	s, err := input.Open(input.Options{Hints: opts.hints})
	if err != nil {
		fmt.Fprintf(stderr, "spk01: %v\n", err)
		if logf != nil {
			_ = logf.Close()
		}
		return 1
	}

	st := newStopper()
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)
	go func() {
		select {
		case <-sigc:
			st.stop("signal")
		case <-st.c:
		}
	}()

	trayReady := make(chan struct{})
	sp := &spike{
		s: s, opts: opts, stderr: stderr, stop: st,
		trayReady: trayReady, mainThread: currentThreadID(),
	}
	if logf != nil {
		sp.log = logf
	}
	codec := make(chan int, 1)
	go func() {
		code := sp.run()
		if logf != nil {
			if err := logf.Close(); err != nil {
				fmt.Fprintf(stderr, "spk01: %v\n", err)
				code = 1
			}
		}
		codec <- code
		endTray(trayReady, stderr, code)
	}()
	systray.Run(func() {
		setUpTray(st)
		close(trayReady)
	}, nil)
	// The tray loop also ends when its window is closed from outside.
	st.stop("tray exited")
	return <-codec
}

// setUpTray sets the tray icon and its menu, whose Quit item ends the
// recording window.
func setUpTray(st *stopper) {
	systray.SetIcon(trayIcon)
	systray.SetTooltip("padmate SPK-01 spike")
	quit := systray.AddMenuItem("Quit", "Stop recording and exit")
	go func() {
		<-quit.ClickedCh
		st.stop("quit")
	}()
}

// endTray ends the tray loop once the tray is set up, or after trayWait.
// systray does not report a failed set-up, and its loop then never ends,
// so the process exits with code after exitGrace anyway.
func endTray(ready <-chan struct{}, stderr io.Writer, code int) {
	select {
	case <-ready:
	case <-time.After(trayWait):
	}
	systray.Quit()
	time.Sleep(exitGrace)
	fmt.Fprintf(stderr, "spk01: the tray loop did not end within %v of systray.Quit; exiting\n", exitGrace)
	os.Exit(code)
}
