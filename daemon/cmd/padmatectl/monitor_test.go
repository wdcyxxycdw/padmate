package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// TestMonitorFlags checks the flags that are rejected before SDL is
// loaded, so it needs no SDL library.
func TestMonitorFlags(t *testing.T) {
	tests := []struct {
		args       []string
		wantStderr string
	}{
		{[]string{"--hint", "bogus"}, `invalid value "bogus" for flag -hint`},
		{[]string{"--hint", "=1"}, `invalid value "=1" for flag -hint`},
		{[]string{"--hint", "SDL HINT=1"}, `invalid value "SDL HINT=1" for flag -hint`},
		{[]string{"--hint"}, "flag needs an argument"},
		{[]string{"--preset", "nope"}, `unknown preset "nope"; want one of default, xinput, rawinput, hidapi-off, wgi, gameinput`},
		{[]string{"--preset", ""}, `unknown preset ""`},
		{[]string{"extra"}, `unexpected argument "extra"`},
		{[]string{"--list", "--virtual"}, "cannot be combined"},
		{[]string{"--deadband", "-1"}, "cannot be negative"},
		{[]string{"--count", "-1"}, "cannot be negative"},
		{[]string{"--duration", "-1s"}, "cannot be negative"},
		{[]string{"--duration", "1"}, "invalid value"},
		{[]string{"--bogus"}, "flag provided but not defined"},
		{[]string{"--direct=false", "--virtual"}, "cannot be combined"},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCmd(t, append([]string{"monitor"}, tt.args...)...)
		if code != 2 || stdout != "" {
			t.Errorf("monitor %q: exit %d, stdout %q; want 2 and nothing", tt.args, code, stdout)
		}
		if !strings.Contains(stderr, tt.wantStderr) || !strings.Contains(stderr, "usage: padmatectl monitor") {
			t.Errorf("monitor %q: stderr %q, want %q and the usage", tt.args, stderr, tt.wantStderr)
		}
	}
}

func TestMonitorHelp(t *testing.T) {
	code, stdout, stderr := runCmd(t, "monitor", "-h")
	if code != 0 || stderr != "" {
		t.Fatalf("monitor -h: exit %d, stderr %q", code, stderr)
	}
	for _, want := range append([]string{"usage: padmatectl monitor", "--direct", "timeNs"}, input.PresetNames()...) {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}

func TestMonitorHintOrder(t *testing.T) {
	var out, errOut bytes.Buffer
	opts, code, ok := parseMonitorFlags([]string{
		"--direct", "--preset", "xinput", "--hint", "SDL_JOYSTICK_HIDAPI=1", "--hint", "SDL_A=b=c",
	}, &out, &errOut)
	if !ok {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	preset, _ := input.PresetHints("xinput")
	want := slices.Concat(preset, []input.Hint{{Name: "SDL_JOYSTICK_HIDAPI", Value: "1"}, {Name: "SDL_A", Value: "b=c"}})
	if !slices.Equal(opts.hints, want) {
		t.Errorf("hints = %v, want %v", opts.hints, want)
	}
}

// TestMonitorFormat prints a session of events through one printer in
// each mode. Text lines show the DeviceKey of a gamepad; JSON lines stay
// the ENG-08 format.
func TestMonitorFormat(t *testing.T) {
	info := &input.DeviceInfo{
		ID: 3, Name: "Xbox Wireless Controller", Type: "xboxone",
		Vendor: 0x045e, Product: 0x0b13, Version: 0x0509,
		GUID: "0500a1b25e040000130b000009057200", Driver: "hidapi",
		Path: `\\?\HID#VID_045E&PID_0B13`, Serial: "abc", IsGamepad: true,
	}
	back := *info // the same gamepad, connected again to another port
	back.ID, back.Path = 5, `\\?\HID#VID_045E&PID_0B13&2`
	joystick := &input.DeviceInfo{ID: 4, Name: "Wheel", Type: "unknown", Driver: "dinput", Virtual: true}
	// Identical virtual gamepads, which have no serial and no path.
	virtual := func(id input.DeviceID) *input.DeviceInfo {
		return &input.DeviceInfo{
			ID: id, Name: "exp pad", Type: "xbox360", GUID: "ff005cd5657870207061640000007601",
			Driver: "virtual", IsGamepad: true, Virtual: true,
		}
	}
	xinput := &input.DeviceInfo{
		ID: 8, Name: "Xbox Series X Controller", Type: "xboxone", Vendor: 0x045e, Product: 0x0b12,
		GUID: "0300509d5e040000120b000000007801", Driver: "xinput", Path: "XInput#1", IsGamepad: true,
	}
	tests := []struct {
		e        input.Event
		text     string
		jsonLine string
	}{
		{
			input.Event{Time: 1_234_567_890, Device: 3, Kind: input.DeviceAdded, Info: info},
			`1.234567 +device 3 xboxone "Xbox Wireless Controller" 045e:0b13 driver=hidapi guid=0500a1b25e040000130b000009057200 serial=abc path=\\?\HID#VID_045E&PID_0B13 key=0500a1b25e040000130b000009057200/s:abc`,
			`{"timeNs":1234567890,"kind":"added","device":3,"info":{"id":3,"name":"Xbox Wireless Controller","type":"xboxone","vendor":1118,"product":2835,"version":1289,"guid":"0500a1b25e040000130b000009057200","driver":"hidapi","path":"\\\\?\\HID#VID_045E&PID_0B13","serial":"abc","gamepad":true,"virtual":false}}`,
		},
		{
			input.Event{Time: 5, Device: 4, Kind: input.DeviceAdded, Info: joystick},
			`0.000000 +device 4 unknown "Wheel" 0000:0000 driver=dinput guid= serial= path= virtual joystick-only`,
			`{"timeNs":5,"kind":"added","device":4,"info":{"id":4,"name":"Wheel","type":"unknown","vendor":0,"product":0,"version":0,"guid":"","driver":"dinput","path":"","serial":"","gamepad":false,"virtual":true}}`,
		},
		{
			input.Event{Time: 2_000_001_000, Device: 3, Kind: input.ButtonDown, Control: "south"},
			`2.000001 3 south down`,
			`{"timeNs":2000001000,"kind":"down","device":3,"control":"south"}`,
		},
		{
			input.Event{Time: 2_500_000_000, Device: 3, Kind: input.ButtonUp, Control: "dpad.up"},
			`2.500000 3 dpad.up up`,
			`{"timeNs":2500000000,"kind":"up","device":3,"control":"dpad.up"}`,
		},
		{
			input.Event{Time: 3_000_000_000, Device: 3, Kind: input.AxisMotion, Control: "leftx", Value: -12345},
			`3.000000 3 leftx -12345`,
			`{"timeNs":3000000000,"kind":"axis","device":3,"control":"leftx","value":-12345}`,
		},
		{
			input.Event{Time: 3_000_000_000, Device: 3, Kind: input.AxisMotion, Control: "lefttrigger"},
			`3.000000 3 lefttrigger 0`,
			`{"timeNs":3000000000,"kind":"axis","device":3,"control":"lefttrigger","value":0}`,
		},
		{
			input.Event{Time: 61_000_000_000, Device: 3, Kind: input.DeviceRemoved},
			`61.000000 -device 3 key=0500a1b25e040000130b000009057200/s:abc`,
			`{"timeNs":61000000000,"kind":"removed","device":3}`,
		},
		{
			input.Event{Time: 62_000_000_000, Device: 4, Kind: input.DeviceRemoved},
			`62.000000 -device 4`,
			`{"timeNs":62000000000,"kind":"removed","device":4}`,
		},
		{
			input.Event{Time: 63_000_000_000, Device: 5, Kind: input.DeviceAdded, Info: &back},
			`63.000000 +device 5 xboxone "Xbox Wireless Controller" 045e:0b13 driver=hidapi guid=0500a1b25e040000130b000009057200 serial=abc path=\\?\HID#VID_045E&PID_0B13&2 key=0500a1b25e040000130b000009057200/s:abc reconnected`,
			`{"timeNs":63000000000,"kind":"added","device":5,"info":{"id":5,"name":"Xbox Wireless Controller","type":"xboxone","vendor":1118,"product":2835,"version":1289,"guid":"0500a1b25e040000130b000009057200","driver":"hidapi","path":"\\\\?\\HID#VID_045E&PID_0B13&2","serial":"abc","gamepad":true,"virtual":false}}`,
		},
		{
			input.Event{Time: 64_000_000_000, Device: 6, Kind: input.DeviceAdded, Info: virtual(6)},
			`64.000000 +device 6 xbox360 "exp pad" 0000:0000 driver=virtual guid=ff005cd5657870207061640000007601 serial= path= virtual key=ff005cd5657870207061640000007601`,
			`{"timeNs":64000000000,"kind":"added","device":6,"info":{"id":6,"name":"exp pad","type":"xbox360","vendor":0,"product":0,"version":0,"guid":"ff005cd5657870207061640000007601","driver":"virtual","path":"","serial":"","gamepad":true,"virtual":true}}`,
		},
		{
			input.Event{Time: 65_000_000_000, Device: 7, Kind: input.DeviceAdded, Info: virtual(7)},
			`65.000000 +device 7 xbox360 "exp pad" 0000:0000 driver=virtual guid=ff005cd5657870207061640000007601 serial= path= virtual key=ff005cd5657870207061640000007601#1`,
			`{"timeNs":65000000000,"kind":"added","device":7,"info":{"id":7,"name":"exp pad","type":"xbox360","vendor":0,"product":0,"version":0,"guid":"ff005cd5657870207061640000007601","driver":"virtual","path":"","serial":"","gamepad":true,"virtual":true}}`,
		},
		{
			input.Event{Time: 66_000_000_000, Device: 7, Kind: input.DeviceRemoved},
			`66.000000 -device 7 key=ff005cd5657870207061640000007601#1`,
			`{"timeNs":66000000000,"kind":"removed","device":7}`,
		},
		{
			// The path of an XInput gamepad is its slot, which the key
			// leaves out.
			input.Event{Time: 67_000_000_000, Device: 8, Kind: input.DeviceAdded, Info: xinput},
			`67.000000 +device 8 xboxone "Xbox Series X Controller" 045e:0b12 driver=xinput guid=0300509d5e040000120b000000007801 serial= path=XInput#1 key=0300509d5e040000120b000000007801`,
			`{"timeNs":67000000000,"kind":"added","device":8,"info":{"id":8,"name":"Xbox Series X Controller","type":"xboxone","vendor":1118,"product":2834,"version":0,"guid":"0300509d5e040000120b000000007801","driver":"xinput","path":"XInput#1","serial":"","gamepad":true,"virtual":false}}`,
		},
	}
	for _, asJSON := range []bool{false, true} {
		var b bytes.Buffer
		p := newEventPrinter(&b, monitorOptions{json: asJSON})
		for _, tt := range tests {
			b.Reset()
			if shown, err := p.print(tt.e); !shown || err != nil {
				t.Fatalf("print(%+v) = %t, %v", tt.e, shown, err)
			}
			want := tt.text
			if asJSON {
				want = tt.jsonLine
			}
			if got := b.String(); got != want+"\n" {
				t.Errorf("json=%t:\n got %q\nwant %q", asJSON, got, want+"\n")
			}
		}
	}
}

// TestMonitorList checks that --list prints only the added devices, yet
// sees that a gamepad is back.
func TestMonitorList(t *testing.T) {
	var b bytes.Buffer
	p := newEventPrinter(&b, monitorOptions{list: true})
	pad := func(id input.DeviceID) *input.DeviceInfo {
		return &input.DeviceInfo{ID: id, Name: "Pad", Type: "ps5", GUID: "050000004c050000e60c000000016800", Serial: "s1", IsGamepad: true}
	}
	for _, tc := range []struct {
		e     input.Event
		shown bool
	}{
		{input.Event{Device: 1, Kind: input.DeviceAdded, Info: pad(1)}, true},
		{input.Event{Device: 1, Kind: input.ButtonDown, Control: "south"}, false},
		{input.Event{Device: 1, Kind: input.DeviceRemoved}, false},
		{input.Event{Device: 2, Kind: input.DeviceAdded, Info: pad(2)}, true},
	} {
		if shown, err := p.print(tc.e); err != nil || shown != tc.shown {
			t.Errorf("print(%v of %d) = %t, %v; want %t", tc.e.Kind, tc.e.Device, shown, err, tc.shown)
		}
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " key=050000004c050000e60c000000016800/s:s1") ||
		!strings.HasSuffix(lines[1], " key=050000004c050000e60c000000016800/s:s1 reconnected") {
		t.Errorf("printed:\n%s", b.String())
	}
}

func TestMonitorDeadband(t *testing.T) {
	var b bytes.Buffer
	p := newEventPrinter(&b, monitorOptions{deadband: 100})
	axis := func(dev input.DeviceID, control string, v int16) input.Event {
		return input.Event{Device: dev, Kind: input.AxisMotion, Control: control, Value: v}
	}
	for _, tc := range []struct {
		e     input.Event
		shown bool
	}{
		{axis(1, "leftx", 50), true}, // the first value is always shown
		{axis(1, "leftx", 149), false},
		{axis(1, "leftx", -49), false},
		{axis(1, "leftx", 150), true}, // 100 from the last shown value, 50
		{axis(1, "lefty", 151), true}, // another axis
		{axis(2, "leftx", 151), true}, // another device
		{axis(1, "leftx", 51), false},
		{axis(1, "leftx", 50), true},
		{input.Event{Device: 1, Kind: input.ButtonDown, Control: "south"}, true},
	} {
		shown, err := p.print(tc.e)
		if err != nil || shown != tc.shown {
			t.Errorf("print(%s %d) = %t, %v; want %t", tc.e.Control, tc.e.Value, shown, err, tc.shown)
		}
	}
}

func TestMonitorVirtualEvents(t *testing.T) {
	want := []string{
		"added", "south down", "south up", "leftx 32767", "leftx 0",
		"lefttrigger 32767", "lefttrigger 0", "dpad.up down", "dpad.up up", "removed",
	}
	if got := virtualEvents(); !slices.Equal(got, want) {
		t.Errorf("virtualEvents() = %q, want %q", got, want)
	}
}

// monitorLine is one line of "monitor --json", decoded with the field
// names that the usage documents.
type monitorLine struct {
	TimeNs  uint64 `json:"timeNs"`
	Kind    string `json:"kind"`
	Device  uint32 `json:"device"`
	Control string `json:"control"`
	Value   *int16 `json:"value"`
	Info    *struct {
		ID      uint32 `json:"id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		GUID    string `json:"guid"`
		Driver  string `json:"driver"`
		Gamepad bool   `json:"gamepad"`
		Virtual bool   `json:"virtual"`
	} `json:"info"`
}

// key formats l like eventKey.
func (l monitorLine) key() string {
	switch {
	case l.Kind == "down" || l.Kind == "up":
		return l.Control + " " + l.Kind
	case l.Kind == "axis" && l.Value != nil:
		return fmt.Sprintf("%s %d", l.Control, *l.Value)
	default:
		return l.Kind
	}
}

// TestMonitorVirtualBinary runs the built "padmatectl monitor --virtual"
// with the SDL library (IN-03 acceptance).
func TestMonitorVirtualBinary(t *testing.T) {
	lib := inputtest.Library(t)
	ctl := injecttest.BuildTool(t, ".")
	// monitor runs "exe monitor args" with the environment env.
	monitor := func(t *testing.T, exe string, env []string, args ...string) (code int, stdout, stderr string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, append([]string{"monitor"}, args...)...)
		cmd.Env = env
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
			code = exit.ExitCode()
		default:
			t.Fatalf("run: %v", err)
		}
		return code, out.String(), errOut.String()
	}
	env := os.Environ()
	if filepath.IsAbs(lib) {
		// Otherwise it is a system library that the loader finds.
		env = append(env, input.EnvLibrary+"="+lib)
	}
	run := func(t *testing.T, args ...string) (code int, stdout, stderr string) {
		t.Helper()
		return monitor(t, ctl, env, args...)
	}

	t.Run("json", func(t *testing.T) {
		code, stdout, stderr := run(t, "--virtual", "--json", "--duration", "30s")
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		for _, want := range []string{"SDL ", lib, input.HintBackgroundEvents + "=1"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr does not mention %q:\n%s", want, stderr)
			}
		}
		var got []string
		var vid uint32
		var last uint64
		sc := bufio.NewScanner(strings.NewReader(stdout))
		for sc.Scan() {
			var l monitorLine
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				t.Fatalf("line %q: %v", sc.Text(), err)
			}
			if l.Kind == "added" && l.Info != nil && l.Info.Name == monitorVirtualName {
				vid = l.Device
				if !l.Info.Virtual || !l.Info.Gamepad || l.Info.Driver != "virtual" || l.Info.ID != l.Device {
					t.Errorf("virtual gamepad info %+v", *l.Info)
				}
			}
			if vid == 0 || l.Device != vid {
				continue // a real device
			}
			if l.TimeNs < last {
				t.Errorf("%q at %d ns, before the previous event at %d ns", l.key(), l.TimeNs, last)
			}
			last = l.TimeNs
			got = append(got, l.key())
		}
		if want := virtualEvents(); !slices.Equal(got, want) {
			t.Errorf("events of the virtual gamepad:\n got %q\nwant %q\nstdout:\n%s", got, want, stdout)
		}
	})

	t.Run("text", func(t *testing.T) {
		code, stdout, stderr := run(t, "--virtual", "--duration", "30s")
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		// The id of the virtual gamepad is 1 unless SDL saw a real device
		// first.
		var id, key string
		for line := range strings.Lines(stdout) {
			if f := strings.Fields(line); len(f) > 2 && f[1] == "+device" && strings.Contains(line, `"`+monitorVirtualName+`"`) {
				id, key = f[2], f[len(f)-1]
			}
		}
		// A virtual gamepad has no serial and no path: its key is its GUID.
		if !strings.HasPrefix(key, "key=ff00") || strings.Contains(key, "/") {
			t.Errorf("the virtual gamepad's line ends in %q, want key=ff00…", key)
		}
		for _, want := range []string{
			` +device ` + id + ` standard "padmate virtual gamepad" 0000:0000 driver=virtual guid=ff00`,
			" serial= path= virtual " + key + "\n",
			" " + id + " south down\n", " " + id + " leftx 32767\n", " " + id + " lefttrigger 0\n",
			" " + id + " dpad.up up\n", " -device " + id + " " + key + "\n",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout does not contain %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("count", func(t *testing.T) {
		// --count ends the run before the sequence is complete.
		code, stdout, stderr := run(t, "--virtual", "--count", "3", "--duration", "30s")
		if code != 1 || !strings.Contains(stderr, "stopped by --count after") {
			t.Errorf("exit %d, stderr %q; want 1 and the reason", code, stderr)
		}
		if n := strings.Count(stdout, "\n"); n != 3 {
			t.Errorf("%d lines, want 3:\n%s", n, stdout)
		}
	})

	t.Run("next to the executable", func(t *testing.T) {
		// A release has no $PADMATE_SDL3: padmatectl loads the library
		// next to it (ADR-0007).
		if !filepath.IsAbs(lib) {
			t.Skip("the SDL library is a system library")
		}
		name := sdl.Path()
		if runtime.GOOS == "windows" {
			name = sdlpin.DLLName
		}
		dir := t.TempDir()
		exe := filepath.Join(dir, filepath.Base(ctl))
		next := filepath.Join(dir, name)
		copyFile(t, ctl, exe)
		copyFile(t, lib, next)
		env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
			// Environment variable names are case-insensitive on Windows.
			return strings.HasPrefix(strings.ToUpper(kv), input.EnvLibrary+"=")
		})
		code, stdout, stderr := monitor(t, exe, env, "--virtual", "--duration", "30s")
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		// The first line is "padmatectl monitor: SDL <version> from <path>".
		first, _, _ := strings.Cut(stderr, "\n")
		_, loaded, ok := strings.Cut(first, " from ")
		if !ok {
			t.Fatalf("stderr does not name the library:\n%s", stderr)
		}
		a, errA := os.Stat(loaded)
		b, errB := os.Stat(next)
		if errA != nil || errB != nil || !os.SameFile(a, b) {
			t.Errorf("loaded %q (%v), want the copy %q next to the executable (%v)", loaded, errA, next, errB)
		}
	})
}

// TestMonitorNoLibrary checks the error when $PADMATE_SDL3 names no file:
// it says what is missing and how to fix it.
func TestMonitorNoLibrary(t *testing.T) {
	t.Setenv(input.EnvLibrary, filepath.Join(t.TempDir(), "missing", sdlpin.DLLName))
	code, stdout, stderr := runCmd(t, "monitor", "--direct", "--duration", "1s")
	if code != 1 || stdout != "" {
		t.Errorf("exit %d, stdout %q; want 1 and nothing", code, stdout)
	}
	want := []string{"padmatectl monitor: ", input.ErrNoLibrary.Error(), "$" + input.EnvLibrary}
	if runtime.GOOS == "windows" {
		want = append(want, "task sdl")
	}
	for _, w := range want {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr %q does not mention %q", stderr, w)
		}
	}
}

// copyFile copies the file src to dst.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o700); err != nil {
		t.Fatal(err)
	}
}
