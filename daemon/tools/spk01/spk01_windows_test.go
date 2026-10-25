package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

func TestParseFlags(t *testing.T) {
	defaultHints, _ := input.PresetHints("default")
	xinputHints, _ := input.PresetHints("xinput")
	tests := []struct {
		args []string
		want options
	}{
		{nil, options{preset: "default", hints: defaultHints, duration: 10 * time.Second}},
		{
			[]string{"--preset", "xinput", "--hint", "SDL_JOYSTICK_THREAD=0", "--duration", "2s", "--log", "x.jsonl", "--virtual-rate", "1000"},
			options{
				preset: "xinput", hints: append(xinputHints, input.Hint{Name: "SDL_JOYSTICK_THREAD", Value: "0"}),
				duration: 2 * time.Second, log: "x.jsonl", rate: 1000,
			},
		},
		{[]string{"--virtual-rate", "10000"}, options{preset: "default", hints: defaultHints, duration: 10 * time.Second, rate: 10000}},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		got, code, ok := parseFlags(tt.args, &stdout, &stderr)
		if !ok || code != 0 {
			t.Errorf("%q: code %d, ok %t; stderr:\n%s", tt.args, code, ok, stderr.String())
			continue
		}
		if got.preset != tt.want.preset || !slices.Equal(got.hints, tt.want.hints) ||
			got.duration != tt.want.duration || got.log != tt.want.log || got.rate != tt.want.rate {
			t.Errorf("%q: %+v, want %+v", tt.args, got, tt.want)
		}
		if stdout.Len()+stderr.Len() > 0 {
			t.Errorf("%q: output %q %q", tt.args, stdout.String(), stderr.String())
		}
	}
}

func TestParseFlagsBad(t *testing.T) {
	for _, tt := range []struct {
		args []string
		msg  string // in stderr
	}{
		{[]string{"extra"}, `unexpected argument "extra"`},
		{[]string{"--preset", "bogus"}, `unknown preset "bogus"`},
		{[]string{"--duration", "0"}, "--duration must be positive"},
		{[]string{"--duration", "-1s"}, "--duration must be positive"},
		{[]string{"--virtual-rate", "-1"}, "--virtual-rate must be between 0 and 10000"},
		{[]string{"--virtual-rate", "10001"}, "--virtual-rate must be between 0 and 10000"},
		{[]string{"--hint", "no-equals"}, "-hint"},
		{[]string{"--bogus"}, "flag provided but not defined"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tt.args, &stdout, &stderr); code != 2 {
			t.Errorf("%q: code %d, want 2", tt.args, code)
		}
		if !strings.Contains(stderr.String(), tt.msg) || !strings.Contains(stderr.String(), "usage: spk01") {
			t.Errorf("%q: stderr %q, want %q and the usage", tt.args, stderr.String(), tt.msg)
		}
		if stdout.Len() > 0 {
			t.Errorf("%q: stdout %q", tt.args, stdout.String())
		}
	}
}

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-h"}, &stdout, &stderr); code != 0 {
		t.Errorf("code %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "usage: spk01") || stderr.Len() > 0 {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	for _, name := range input.PresetNames() {
		if !strings.Contains(stdout.String(), "\n  "+name+" ") {
			t.Errorf("the usage does not list the preset %s", name)
		}
	}
}

// TestRunFailsEarly checks the failures before the tray starts.
func TestRunFailsEarly(t *testing.T) {
	t.Run("log", func(t *testing.T) {
		var stderr bytes.Buffer
		bad := filepath.Join(t.TempDir(), "missing", "log.jsonl")
		if code := run([]string{"--log", bad}, &bytes.Buffer{}, &stderr); code != 1 {
			t.Errorf("code %d, want 1; stderr %q", code, stderr.String())
		}
	})
	t.Run("library", func(t *testing.T) {
		t.Setenv(input.EnvLibrary, filepath.Join(t.TempDir(), "SDL3.dll"))
		var stderr bytes.Buffer
		if code := run(nil, &bytes.Buffer{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "SDL3") {
			t.Errorf("code %d, want 1; stderr %q", code, stderr.String())
		}
	})
}

func TestIcon(t *testing.T) {
	ico := trayIcon
	// 6 + 16 + 40 + 16*16*4 + 16*4 bytes.
	if len(ico) != 1150 {
		t.Fatalf("%d bytes, want 1150", len(ico))
	}
	le := binary.LittleEndian
	if le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 || le.Uint16(ico[4:]) != 1 {
		t.Errorf("ICONDIR % x", ico[:6])
	}
	e := ico[6:22]
	if e[0] != iconSize || e[1] != iconSize || le.Uint16(e[4:]) != 1 || le.Uint16(e[6:]) != 32 ||
		le.Uint32(e[8:]) != iconImageSize || le.Uint32(e[12:]) != iconHeaderSize {
		t.Errorf("ICONDIRENTRY % x", e)
	}
	bi := ico[22:62]
	if le.Uint32(bi[0:]) != 40 || int32(le.Uint32(bi[4:])) != iconSize || int32(le.Uint32(bi[8:])) != 2*iconSize ||
		le.Uint16(bi[12:]) != 1 || le.Uint16(bi[14:]) != 32 || le.Uint32(bi[16:]) != 0 {
		t.Errorf("BITMAPINFOHEADER % x", bi)
	}
	// alpha returns the alpha of pixel (x, y) from the top left; the rows
	// are stored bottom first.
	alpha := func(x, y int) byte { return ico[62+((iconSize-1-y)*iconSize+x)*4+3] }
	if a := alpha(8, 8); a != 255 {
		t.Errorf("center alpha %d, want 255", a)
	}
	for _, p := range [][2]int{{0, 0}, {15, 0}, {0, 15}, {15, 15}} {
		if a := alpha(p[0], p[1]); a != 0 {
			t.Errorf("corner %v alpha %d, want 0", p, a)
		}
	}
	if slices.ContainsFunc(ico[62+iconXORSize:], func(b byte) bool { return b != 0 }) {
		t.Error("the AND mask is not 0")
	}
}

func TestAppendJSONString(t *testing.T) {
	for _, s := range []string{"", "south", "dpad.up", `a"b\c`, "\x00\x1f\n", "Xbox 手柄"} {
		var got string
		b := appendJSONString(nil, s)
		if err := json.Unmarshal(b, &got); err != nil || got != s {
			t.Errorf("%q: %s decodes to %q, %v", s, b, got, err)
		}
	}
}

// logLines parses the JSON lines of a --log file into the header, the
// events and the summary.
func logLines(t *testing.T, data []byte) (headerJSON, []map[string]any, summaryJSON) {
	t.Helper()
	var (
		h     headerJSON
		evs   []map[string]any
		s     summaryJSON
		types []string
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q: %v", sc.Bytes(), err)
		}
		typ, _ := m["type"].(string)
		types = append(types, typ)
		switch typ {
		case "header":
			if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
				t.Fatal(err)
			}
		case "summary":
			if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
				t.Fatal(err)
			}
		case "event":
			evs = append(evs, m)
		default:
			t.Fatalf("line %q: unknown type", sc.Bytes())
		}
	}
	if len(types) < 2 || types[0] != "header" || types[len(types)-1] != "summary" ||
		slices.Contains(types[1:len(types)-1], "header") || slices.Contains(types[1:len(types)-1], "summary") {
		t.Fatalf("line types %v, want a header, events and a summary", types)
	}
	return h, evs, s
}

// checkVirtualRun checks the log of a run with a virtual gamepad.
func checkVirtualRun(t *testing.T, data []byte, rate int) summaryJSON {
	t.Helper()
	h, evs, s := logLines(t, data)
	if h.Tool != "spk01" || h.SDL == "" || h.Library == "" || h.Preset != "default" || h.VirtualRate != rate ||
		h.InputThread == 0 || h.LogicalCPUs < 1 || len(h.Hints) == 0 {
		t.Errorf("header %+v", h)
	}
	if !s.OK || len(s.Errors) > 0 || s.Dropped != 0 || s.Reason != "duration" {
		t.Errorf("summary: ok %t, errors %q, dropped %d, reason %s", s.OK, s.Errors, s.Dropped, s.Reason)
	}
	v := s.Virtual
	if v == nil || v.Rate != rate || v.Sent == 0 || v.Received != v.Sent || !v.Removed {
		t.Fatalf("virtual %+v", v)
	}
	if s.Events != uint64(len(evs)) {
		t.Errorf("summary counts %d events, the log has %d", s.Events, len(evs))
	}
	// The virtual gamepad's events are the driven cycle, in order.
	cycle := []string{"down south", "up south", "axis leftx 16384", "axis leftx 0"}
	var vid float64
	n := uint64(0)
	for _, e := range evs {
		info, _ := e["info"].(map[string]any)
		if e["kind"] == "added" && info["name"] == virtualName {
			vid = e["device"].(float64)
			continue
		}
		if e["device"] != vid || e["kind"] == "removed" || e["kind"] == "added" {
			continue
		}
		got := e["kind"].(string) + " " + e["control"].(string)
		if val, ok := e["value"]; ok {
			got += " " + jsonString(val)
		}
		if want := cycle[n%4]; got != want {
			t.Fatalf("virtual event %d: %q, want %q", n, got, want)
		}
		n++
	}
	if vid == 0 || n != v.Sent {
		t.Errorf("virtual gamepad %v: %d events in the log, %d sent", vid, n, v.Sent)
	}
	if s.CPU.WallSec <= 0 || s.ProcessCPU.WallSec < s.CPU.WallSec || s.LogicalCPUs < 1 {
		t.Errorf("cpu %+v, process %+v", s.CPU, s.ProcessCPU)
	}
	return s
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestSpike runs the recorder and the virtual gamepad driver in this
// process, without the tray.
func TestSpike(t *testing.T) {
	lib := inputtest.Library(t)
	s, err := input.Open(input.Options{Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const rate = 400
	var log, stderr bytes.Buffer
	sp := &spike{
		s: s, log: &log, stderr: &stderr, stop: newStopper(), mainThread: currentThreadID(),
		opts: options{preset: "default", duration: 500 * time.Millisecond, rate: rate},
	}
	if code := sp.run(); code != 0 {
		t.Errorf("code %d; stderr:\n%s", code, stderr.String())
	}
	sum := checkVirtualRun(t, log.Bytes(), rate)
	if sum.TrayReady {
		t.Error("trayReady without a tray")
	}
	if !strings.Contains(stderr.String(), "spk01: ok\n") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
}

// TestSpikeStop checks that the stopper ends the window early.
func TestSpikeStop(t *testing.T) {
	lib := inputtest.Library(t)
	s, err := input.Open(input.Options{Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var log, stderr bytes.Buffer
	sp := &spike{
		s: s, log: &log, stderr: &stderr, stop: newStopper(),
		opts: options{preset: "default", duration: time.Hour},
	}
	time.AfterFunc(700*time.Millisecond, func() { sp.stop.stop("quit") })
	start := time.Now()
	if code := sp.run(); code != 0 {
		t.Errorf("code %d; stderr:\n%s", code, stderr.String())
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("run took %v", d)
	}
	_, _, sum := logLines(t, log.Bytes())
	if sum.Reason != "quit" || sum.Virtual != nil {
		t.Errorf("reason %q, virtual %+v", sum.Reason, sum.Virtual)
	}
}

// TestBinary builds spk01 as a GUI-subsystem program and runs it with a
// virtual gamepad: the real shape, with the tray on the main thread and
// no window of its own that could be focused.
func TestBinary(t *testing.T) {
	lib := inputtest.Library(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "spk01.exe")
	out, err := exec.Command("go", "build", "-ldflags=-H=windowsgui", "-o", exe, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	logPath := filepath.Join(dir, "spk01.jsonl")
	const rate = 500
	cmd := exec.CommandContext(ctx, exe, "--virtual-rate", "500", "--duration", "1s", "--log", logPath)
	cmd.Env = append(os.Environ(), input.EnvLibrary+"="+lib)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("spk01: %v after %v; stderr:\n%s", err, time.Since(start), stderr.String())
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("spk01 took %v", d)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := checkVirtualRun(t, data, rate)
	if sum.MainThread == 0 || sum.MainThread == sum.InputThread {
		t.Errorf("main thread %d, T-input thread %d: want two threads", sum.MainThread, sum.InputThread)
	}
	// The tray window is hidden and never focused.
	if sum.Foreground.Samples == 0 || sum.Foreground.Own != 0 {
		t.Errorf("foreground %+v, want samples and none of them spk01's", sum.Foreground)
	}
	if strings.Contains(stderr.String(), "did not end") || !strings.Contains(stderr.String(), "spk01: ok\n") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
	if stdout.Len() > 0 {
		t.Errorf("stdout %q", stdout.String())
	}
	t.Logf("tray ready %t; stderr:\n%s", sum.TrayReady, stderr.String())
}
