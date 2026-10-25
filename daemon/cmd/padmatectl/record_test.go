package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// TestRecordFlags checks the flags that are rejected before SDL is
// loaded, so it needs no SDL library.
func TestRecordFlags(t *testing.T) {
	tests := []struct {
		args       []string
		wantStderr string
	}{
		{[]string{"--hint", "bogus"}, `invalid value "bogus" for flag -hint`},
		{[]string{"--preset", "nope"}, `unknown preset "nope"; want one of default,`},
		{[]string{"extra"}, `unexpected argument "extra"`},
		{[]string{"--device", "-1"}, `invalid value "-1" for flag -device`},
		{[]string{"--device", "4294967296"}, "--device must be at most 4294967295"},
		{[]string{"--device", "1", "--virtual"}, "--device and --virtual cannot be combined"},
		{[]string{"--force"}, "--force needs -o"},
		{[]string{"--count", "-1"}, "cannot be negative"},
		{[]string{"--duration", "-1s"}, "cannot be negative"},
		{[]string{"-o"}, "flag needs an argument"},
		{[]string{"--bogus"}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCmd(t, append([]string{"record"}, tt.args...)...)
		if code != 2 || stdout != "" {
			t.Errorf("record %q: exit %d, stdout %q; want 2 and nothing", tt.args, code, stdout)
		}
		if !strings.Contains(stderr, tt.wantStderr) || !strings.Contains(stderr, "usage: padmatectl record") {
			t.Errorf("record %q: stderr %q, want %q and the usage", tt.args, stderr, tt.wantStderr)
		}
	}
}

func TestRecordHelp(t *testing.T) {
	code, stdout, stderr := runCmd(t, "record", "-h")
	if code != 0 || stderr != "" {
		t.Fatalf("record -h: exit %d, stderr %q", code, stderr)
	}
	for _, want := range append([]string{"usage: padmatectl record", "-o FILE", "--force", "--device", "--virtual", "monitor --json", "UTF-16"}, input.PresetNames()...) {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
	if _, stdout, _ := runCmd(t, "help"); !strings.Contains(stdout, "record") {
		t.Errorf("padmatectl help does not list record:\n%s", stdout)
	}
}

// TestRecordOutputChecks checks the -o file before SDL is loaded, so it
// needs no SDL library.
func TestRecordOutputChecks(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(existing, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args       []string
		wantStderr string
	}{
		{[]string{"-o", existing}, "old.jsonl exists; use --force to overwrite it"},
		{[]string{"-o", dir}, "is a directory"},
		{[]string{"-o", dir, "--force"}, "is a directory"},
		{[]string{"-o", filepath.Join(dir, "missing", "t.jsonl")}, "the directory " + filepath.Join(dir, "missing") + " does not exist"},
		{[]string{"-o", filepath.Join(existing, "t.jsonl")}, "old.jsonl is not a directory"},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCmd(t, append([]string{"record"}, tt.args...)...)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "padmatectl record: ") || !strings.Contains(stderr, tt.wantStderr) {
			t.Errorf("record %q: exit %d, stdout %q, stderr %q; want 1 and %q", tt.args, code, stdout, stderr, tt.wantStderr)
		}
		if strings.Contains(stderr, "SDL ") {
			t.Errorf("record %q loaded SDL before checking -o:\n%s", tt.args, stderr)
		}
	}
	if b, err := os.ReadFile(existing); err != nil || string(b) != "old\n" {
		t.Errorf("the existing file was changed: %q, %v", b, err)
	}
}

// Events of the fake sources of the recorder tests.
var (
	joystick = input.Event{Time: 100, Device: 1, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 1, Name: "Wheel", Type: "unknown", Driver: "dinput"}}
	pad2     = input.Event{Time: 200, Device: 2, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 2, Name: "Pad two", Type: "xboxone", Driver: "xinput", IsGamepad: true}}
	pad3     = input.Event{Time: 260, Device: 3, Kind: input.DeviceAdded, Info: pad3Info(3)}
	// pad3 connected again.
	pad3back = input.Event{Time: 400, Device: 4, Kind: input.DeviceAdded, Info: pad3Info(4)}
)

// pad3Info returns the info of "Pad three" connected as device id.
func pad3Info(id input.DeviceID) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "Pad three", Type: "ps5", Driver: "hidapi", GUID: "050000004c050000e60c000000016800", Serial: "s3", IsGamepad: true}
}

func ev(t uint64, dev input.DeviceID, kind input.EventKind, control string, value int16) input.Event {
	return input.Event{Time: t, Device: dev, Kind: kind, Control: control, Value: value}
}

// rebased returns e at time t.
func rebased(e input.Event, t uint64) input.Event {
	e.Time = t
	return e
}

func TestRecordEvents(t *testing.T) {
	session := []input.Event{
		joystick,
		ev(150, 1, input.AxisMotion, "leftx", 5),
		pad2,
		ev(250, 2, input.ButtonDown, "south", 0),
		pad3,
		ev(270, 3, input.ButtonDown, "east", 0),
		ev(300, 2, input.AxisMotion, "lefttrigger", 32767),
		ev(320, 3, input.ButtonUp, "east", 0),
		ev(350, 2, input.ButtonUp, "south", 0),
		ev(400, 2, input.DeviceRemoved, "", 0),
		ev(500, 3, input.DeviceRemoved, "", 0),
	}
	tests := []struct {
		name       string
		opts       recordOptions
		events     []input.Event
		interrupt  bool // cancel the context once every event was read
		close      bool // close the channel once every event was read
		wantCode   int
		want       []input.Event
		wantStderr []string
		notStderr  []string
	}{
		{
			name: "first gamepad until removed", events: session,
			want: []input.Event{
				rebased(pad2, 0), ev(50, 2, input.ButtonDown, "south", 0), ev(100, 2, input.AxisMotion, "lefttrigger", 32767),
				ev(150, 2, input.ButtonUp, "south", 0), ev(200, 2, input.DeviceRemoved, "", 0),
			},
			wantStderr: []string{
				`padmatectl record: not recording +device 1 unknown "Wheel" 0000:0000 driver=dinput guid= serial= path= joystick-only` + "\n",
				// Without a GUID, serial and path the key is empty.
				`padmatectl record: recording +device 2 xboxone "Pad two" 0000:0000 driver=xinput guid= serial= path= key=` + "\n",
				`padmatectl record: not recording +device 3 ps5 "Pad three" 0000:0000 driver=hidapi guid=050000004c050000e60c000000016800 serial=s3 path= key=050000004c050000e60c000000016800/s:s3` + "\n",
				"padmatectl record: 5 events in 0.000s, written to stdout\n",
			},
		},
		{
			name: "reconnected", opts: recordOptions{device: 4},
			events: []input.Event{pad3, ev(300, 3, input.DeviceRemoved, "", 0), pad3back, ev(450, 4, input.DeviceRemoved, "", 0)},
			want:   []input.Event{rebased(pad3back, 0), ev(50, 4, input.DeviceRemoved, "", 0)},
			wantStderr: []string{
				`not recording +device 3 ps5 "Pad three" 0000:0000 driver=hidapi guid=050000004c050000e60c000000016800 serial=s3 path= key=050000004c050000e60c000000016800/s:s3` + "\n",
				`recording +device 4 ps5 "Pad three" 0000:0000 driver=hidapi guid=050000004c050000e60c000000016800 serial=s3 path= key=050000004c050000e60c000000016800/s:s3 reconnected` + "\n",
				"2 events",
			},
		},
		{
			name: "--device", opts: recordOptions{device: 3}, events: session,
			want: []input.Event{
				rebased(pad3, 0), ev(10, 3, input.ButtonDown, "east", 0), ev(60, 3, input.ButtonUp, "east", 0), ev(240, 3, input.DeviceRemoved, "", 0),
			},
			wantStderr: []string{"not recording +device 1 ", `not recording +device 2 xboxone "Pad two"`, `recording +device 3 ps5 "Pad three"`, "4 events"},
		},
		{
			name: "--device of a joystick", opts: recordOptions{device: 1}, events: session, wantCode: 1,
			wantStderr: []string{"padmatectl record: device 1 is a joystick without a gamepad mapping, whose buttons and axes are not read\n"},
			notStderr:  []string{"events in"},
		},
		{
			name: "--count", opts: recordOptions{count: 2}, events: session,
			want:       []input.Event{rebased(pad2, 0), ev(50, 2, input.ButtonDown, "south", 0)},
			wantStderr: []string{"2 events in 0.000s"},
		},
		{
			name: "interrupted", events: session[:5], interrupt: true,
			want:       []input.Event{rebased(pad2, 0), ev(50, 2, input.ButtonDown, "south", 0)},
			wantStderr: []string{"2 events in"},
		},
		{
			name: "interrupted before a gamepad", events: []input.Event{joystick}, interrupt: true, wantCode: 1,
			wantStderr: []string{"padmatectl record: interrupted before a gamepad appeared\n"},
			notStderr:  []string{"events in"},
		},
		{
			name: "interrupted before the device", opts: recordOptions{device: 5}, events: session[:3], interrupt: true, wantCode: 1,
			wantStderr: []string{"padmatectl record: interrupted before device 5 appeared\n"},
		},
		{
			name: "timed out before a gamepad", opts: recordOptions{duration: time.Millisecond}, wantCode: 1,
			wantStderr: []string{"padmatectl record: timed out before a gamepad appeared\n"},
		},
		{
			name: "the event loop stops", events: session[2:4], close: true, wantCode: 1,
			want:       []input.Event{rebased(pad2, 0), ev(50, 2, input.ButtonDown, "south", 0)},
			wantStderr: []string{"padmatectl record: the SDL event loop stopped\n", "2 events in"},
		},
		{
			// A time before the previous one is raised, so that ReadEvents
			// reads the recording.
			name: "times in order",
			events: []input.Event{
				pad2, ev(190, 2, input.ButtonDown, "south", 0), ev(1_500_000_300, 2, input.ButtonUp, "south", 0),
				ev(1_500_000_250, 2, input.ButtonDown, "south", 0), ev(1_500_000_400, 2, input.DeviceRemoved, "", 0),
			},
			want: []input.Event{
				rebased(pad2, 0), ev(0, 2, input.ButtonDown, "south", 0), ev(1_500_000_100, 2, input.ButtonUp, "south", 0),
				ev(1_500_000_100, 2, input.ButtonDown, "south", 0), ev(1_500_000_200, 2, input.DeviceRemoved, "", 0),
			},
			wantStderr: []string{"5 events in 1.500s", "padmatectl record: raised the times of 2 events to that of the event before them\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := recordFake(t, tt.opts, tt.events, tt.interrupt, tt.close, &out, &errOut)
			stderr := errOut.String()
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			got, err := input.ReadEvents(&out)
			if err != nil {
				t.Fatalf("the recording does not read back: %v", err)
			}
			if !slices.EqualFunc(got, tt.want, eventsEqual) {
				t.Errorf("recorded:\n%s\nwant:\n%s", formatEvents(got), formatEvents(tt.want))
			}
			for _, w := range tt.wantStderr {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr does not contain %q:\n%s", w, stderr)
				}
			}
			for _, w := range tt.notStderr {
				if strings.Contains(stderr, w) {
					t.Errorf("stderr contains %q:\n%s", w, stderr)
				}
			}
		})
	}
}

// recordFake runs recordEvents on the events, fed one at a time, then
// cancels the context or closes the channel as asked.
func recordFake(t *testing.T, opts recordOptions, events []input.Event, interrupt, closeCh bool, w, stderr *bytes.Buffer) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan input.Event)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(stop) // before wg.Wait
	wg.Go(func() {
		for _, e := range events {
			select {
			case ch <- e:
			case <-stop:
				return
			}
		}
		// Each send returned once recordEvents had the event, so nothing
		// competes with the cancel or the close in its select.
		switch {
		case interrupt:
			cancel()
		case closeCh:
			close(ch)
		}
	})
	if opts.duration == 0 && !interrupt && !closeCh {
		opts.duration = 10 * time.Second // a test that does not stop fails, but ends
	}
	return recordEvents(ctx, ch, nil, opts, "", w, stderr)
}

func TestRecordWriteError(t *testing.T) {
	var errOut bytes.Buffer
	ch := make(chan input.Event, 2)
	ch <- pad2
	code := recordEvents(context.Background(), ch, nil, recordOptions{duration: 10 * time.Second}, "", failWriter{}, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "padmatectl record: disk full\n") {
		t.Errorf("exit %d, stderr %q; want 1 and the error", code, errOut.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// TestRecordFile checks that the -o file is only created by the first
// event, and --force.
func TestRecordFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	// Nothing recorded: no file, not even for the header.
	w := &traceFile{path: path}
	var errOut bytes.Buffer
	if code := recordEventsTo(t, w, []input.Event{joystick}, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a recording of nothing created %s: %v", path, err)
	}

	// A recording creates it.
	if code := recordEventsTo(t, &traceFile{path: path}, []input.Event{pad2, ev(300, 2, input.ButtonDown, "south", 0)}, &errOut); code != 0 {
		t.Fatalf("exit %d; stderr:\n%s", code, errOut.String())
	}
	want := testHeader + `{"timeNs":0,"kind":"added","device":2,"info":{"id":2,"name":"Pad two","type":"xboxone","vendor":0,"product":0,"version":0,"guid":"","driver":"xinput","path":"","serial":"","gamepad":true,"virtual":false}}` + "\n" +
		`{"timeNs":100,"kind":"down","device":2,"control":"south"}` + "\n"
	if b, err := os.ReadFile(path); err != nil || string(b) != want {
		t.Fatalf("%s = %q, %v; want %q", path, b, err, want)
	}
	if !strings.Contains(errOut.String(), "written to "+path+"\n") {
		t.Errorf("stderr does not name the file:\n%s", errOut.String())
	}

	// It is not overwritten without --force, even if it appears after
	// checkOutput.
	errOut.Reset()
	if code := recordEventsTo(t, &traceFile{path: path}, []input.Event{pad3}, &errOut); code != 1 || !strings.Contains(errOut.String(), "t.jsonl") {
		t.Errorf("exit %d, stderr %q; want 1 and the error", code, errOut.String())
	}
	if b, _ := os.ReadFile(path); string(b) != want {
		t.Errorf("the file was changed without --force: %q", b)
	}

	// --force keeps it when nothing is recorded, and else replaces it.
	if code := recordEventsTo(t, &traceFile{path: path, force: true}, nil, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if b, _ := os.ReadFile(path); string(b) != want {
		t.Errorf("--force changed the file with nothing recorded: %q", b)
	}
	if code := recordEventsTo(t, &traceFile{path: path, force: true}, []input.Event{pad3}, &errOut); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if evs := readTrace(t, path); len(evs) != 1 || evs[0].Device != 3 {
		t.Errorf("--force wrote %v, want pad3's added event", evs)
	}
}

// TestTraceHeader checks the comment lines that start a recording: each
// word stays one word of one line, which ReadEvents skips.
func TestTraceHeader(t *testing.T) {
	h := func(name, value string) input.Hint { return input.Hint{Name: name, Value: value} }
	for _, tt := range []struct {
		name  string
		hints []input.Hint
		want  string
	}{
		{"set and unset", []input.Hint{h("SDL_A", "1"), h("SDL_B", ""), h("SDL_C", "0"), h("SDL_D", "")},
			"# padmatectl record v1.2.3; SDL 3.4.16; preset default\n" +
				"# hints in effect: SDL_A=1 SDL_C=0\n" +
				"# hints unset (SDL's default): SDL_B SDL_D\n"},
		{"none set", []input.Hint{h("SDL_B", "")},
			"# padmatectl record v1.2.3; SDL 3.4.16; preset default\n" +
				"# hints unset (SDL's default): SDL_B\n"},
		{"quoted", []input.Hint{h("SDL_A", "a b"), h("SDL_B", "x\ny"), h("SDL_C", "p=q;r"), h("SDL_D", "\"1\"")},
			"# padmatectl record v1.2.3; SDL 3.4.16; preset default\n" +
				`# hints in effect: SDL_A="a b" SDL_B="x\ny" SDL_C="p=q;r" SDL_D="\"1\""` + "\n"},
		{"no hints", nil, "# padmatectl record v1.2.3; SDL 3.4.16; preset default\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := traceHeader("v1.2.3", "3.4.16", "default", tt.hints)
			if got != tt.want {
				t.Errorf("traceHeader =\n%s\nwant\n%s", got, tt.want)
			}
			if evs, err := input.ReadEvents(strings.NewReader(got)); err != nil || len(evs) != 0 {
				t.Errorf("ReadEvents(header) = %v, %v; want no events", evs, err)
			}
		})
	}
	// An odd version or preset stays on its line too.
	got := traceHeader("v1\n{\"timeNs\":0}", "3.4.16", "", nil)
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, `preset ""`) {
		t.Errorf("traceHeader = %q, want one line with the version quoted and an empty preset", got)
	}
}

// testHeader is the header recordEventsTo records.
const testHeader = "# padmatectl record test\n"

// recordEventsTo records the events to the trace file w, after testHeader,
// until they are all read, and closes w.
func recordEventsTo(t *testing.T, w *traceFile, events []input.Event, stderr *bytes.Buffer) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan input.Event)
	var wg sync.WaitGroup
	wg.Go(func() {
		for _, e := range events {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
		cancel()
	})
	code := recordEvents(ctx, ch, nil, recordOptions{out: w.path}, testHeader, w, stderr)
	cancel()
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return code
}

// readTrace reads the trace at path.
func readTrace(t *testing.T, path string) []input.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	evs, err := input.ReadEvents(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return evs
}

func eventsEqual(a, b input.Event) bool {
	return a.Time == b.Time && a.Device == b.Device && a.Kind == b.Kind && a.Control == b.Control && a.Value == b.Value &&
		(a.Info == nil) == (b.Info == nil) && (a.Info == nil || *a.Info == *b.Info)
}

func formatEvents(evs []input.Event) string {
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "  %d %d %s\n", e.Time, e.Device, eventKey(e))
	}
	return b.String()
}

// checkVirtualTrace checks that evs is a recording of "record --virtual".
func checkVirtualTrace(t *testing.T, evs []input.Event) {
	t.Helper()
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	if first := evs[0]; first.Time != 0 || first.Kind != input.DeviceAdded || first.Info == nil ||
		first.Info.Name != monitorVirtualName || !first.Info.Virtual || !first.Info.IsGamepad {
		t.Errorf("first event %+v (info %+v), want the virtual gamepad's added event at 0", first, first.Info)
	}
	keys := make([]string, len(evs))
	for i, e := range evs {
		keys[i] = eventKey(e)
		if e.Device != evs[0].Device {
			t.Errorf("event %d is of device %d, not %d", i, e.Device, evs[0].Device)
		}
	}
	if want := virtualEvents(); !slices.Equal(keys, want) {
		t.Errorf("events:\n got %q\nwant %q", keys, want)
	}
}

// TestVirtualTraceCurrent checks testdata/traces/virtual/script.jsonl
// against the script of --virtual. When the script changes, record it
// again as testdata/traces/README.md says.
func TestVirtualTraceCurrent(t *testing.T) {
	checkVirtualTrace(t, readTrace(t, filepath.Join("..", "..", "testdata", "traces", "virtual", "script.jsonl")))
}

// TestRecordVirtual runs "record --virtual" in this process with the SDL
// library (ENG-08).
func TestRecordVirtual(t *testing.T) {
	inputtest.Unsetenv(t, input.HintCombineJoyCons, input.HintVerticalJoyCons)
	if lib := inputtest.Library(t); filepath.IsAbs(lib) {
		// Otherwise it is a system library that the loader finds.
		t.Setenv(input.EnvLibrary, lib)
	}

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v.jsonl")
		code, stdout, stderr := runCmd(t, "record", "--virtual", "--duration", "30s", "-o", path)
		if code != 0 || stdout != "" {
			t.Fatalf("exit %d, stdout %q; want 0 and nothing\nstderr:\n%s", code, stdout, stderr)
		}
		for _, want := range []string{"padmatectl record: SDL ", input.HintBackgroundEvents + "=1", `recording +device `, " virtual key=ff00", "10 events in", "written to " + path} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr does not mention %q:\n%s", want, stderr)
			}
		}
		checkVirtualTrace(t, readTrace(t, path))
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		head := "# padmatectl record " + version.String() + "; SDL "
		if !strings.HasPrefix(string(b), head) || !strings.Contains(string(b), "; preset default\n# hints in effect: ") ||
			!strings.Contains(string(b), " "+input.HintBackgroundEvents+"=1") {
			t.Errorf("the recording does not start with the header:\n%s", b)
		}
		// padmate's Joy-Con hint is set, and SDL's default of the other kept
		// (IN-07).
		for _, want := range []struct{ prefix, word string }{
			{"# hints in effect: ", input.HintCombineJoyCons + "=0"},
			{"# hints unset (SDL's default): ", input.HintVerticalJoyCons},
		} {
			var words []string
			for line := range strings.Lines(string(b)) {
				if rest, ok := strings.CutPrefix(line, want.prefix); ok {
					words = strings.Fields(rest)
					break
				}
			}
			if !slices.Contains(words, want.word) {
				t.Errorf("the header line %q has no %s:\n%s", want.prefix, want.word, b)
			}
		}
		if lib := inputtest.Library(t); strings.Contains(string(b), lib) {
			t.Errorf("the recording names the library %s:\n%s", lib, b)
		}
	})

	t.Run("stdout", func(t *testing.T) {
		code, stdout, stderr := runCmd(t, "record", "--virtual", "--duration", "30s")
		if code != 0 {
			t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
		}
		evs, err := input.ReadEvents(strings.NewReader(stdout))
		if err != nil {
			t.Fatalf("%v\n%s", err, stdout)
		}
		checkVirtualTrace(t, evs)
		if !strings.Contains(stderr, "written to stdout") {
			t.Errorf("stderr:\n%s", stderr)
		}
	})

	t.Run("count", func(t *testing.T) {
		// --count ends the run before the script is complete.
		code, stdout, stderr := runCmd(t, "record", "--virtual", "--count", "3", "--duration", "30s")
		if code != 1 || !strings.Contains(stderr, "stopped by --count after 3 of the 10 events") {
			t.Errorf("exit %d, stderr %q; want 1 and the reason", code, stderr)
		}
		if evs, err := input.ReadEvents(strings.NewReader(stdout)); err != nil || len(evs) != 3 {
			t.Errorf("%d events, %v; want 3:\n%s", len(evs), err, stdout)
		}
	})

	t.Run("interrupted", func(t *testing.T) {
		// Ctrl+C once the recording has started: the file is closed and
		// reads back.
		path := filepath.Join(t.TempDir(), "v.jsonl")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		errOut := &cancelWriter{cancel: cancel, on: "padmatectl record: recording "}
		code := recordMain(ctx, []string{"--virtual", "--duration", "30s", "-o", path}, &out, errOut)
		stderr := errOut.b.String()
		if code != 1 || !strings.Contains(stderr, "padmatectl record: interrupted after ") {
			t.Errorf("exit %d, want 1 and the reason; stderr:\n%s", code, stderr)
		}
		evs := readTrace(t, path)
		if len(evs) == 0 || evs[0].Kind != input.DeviceAdded || evs[0].Time != 0 || !strings.Contains(stderr, fmt.Sprintf("%d events in", len(evs))) {
			t.Errorf("recorded %s; stderr:\n%s", formatEvents(evs), stderr)
		}
		if err := os.Remove(path); err != nil {
			t.Errorf("the recording is still open: %v", err)
		}
	})
}

// cancelWriter collects what is written, and cancels once on becomes part
// of it.
type cancelWriter struct {
	cancel context.CancelFunc
	on     string
	b      bytes.Buffer
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	n, err := w.b.Write(p)
	if strings.Contains(w.b.String(), w.on) {
		w.cancel()
	}
	return n, err
}
