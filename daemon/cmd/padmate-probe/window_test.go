package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"strings"
	"testing"
)

func TestWindowFlagErrors(t *testing.T) {
	// Every case fails while the flags are read, before a window reader is
	// opened, so none touches Windows.
	oldOpen := openWindowReader
	t.Cleanup(func() { openWindowReader = oldOpen })
	openWindowReader = func() (windowReader, error) {
		t.Error("a flag error must not open the window reader")
		return nil, errUnsupported
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"--hwnd"}, "flag needs an argument: -hwnd"},
		{[]string{"--json=maybe"}, "invalid boolean value"},
		{[]string{"--hwnd", ""}, "--hwnd: want 0x and hex digits"},
		{[]string{"--hwnd", "40B8E"}, `--hwnd: want 0x and hex digits, such as 0x40B8E, not "40B8E"`},
		{[]string{"--hwnd", "263054"}, "--hwnd: want 0x and hex digits"},
		{[]string{"--hwnd", "0x"}, "--hwnd: want 0x and hex digits"},
		{[]string{"--hwnd", "0xZZ"}, "--hwnd: want 0x and hex digits"},
		{[]string{"--hwnd", "0x-1"}, "--hwnd: want 0x and hex digits"},
		{[]string{"--hwnd", "0x10000000000000000"}, `--hwnd: "0x10000000000000000" does not fit in a window handle`},
		{[]string{"--hwnd", "0x12", "extra"}, `unexpected argument "extra"`},
		{[]string{"0x12"}, `unexpected argument "0x12"`},
	} {
		code, stdout, stderr := runProbe(t, append([]string{"window"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: padmate-probe window") {
			t.Errorf("window %q = %d, stdout %q, stderr %q; want 2 and %q with the usage", tc.args, code, stdout, stderr, tc.want)
		}
	}
}

func TestParseHWND(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uintptr
		ok   bool
	}{
		{"0x40B8E", 0x40B8E, true},
		{"0x40b8e", 0x40B8E, true},
		{"0X40B8E", 0x40B8E, true},
		{"0x0", 0, true},
		{"0x00000010", 0x10, true},
		{"0xFFFFFFFF", 0xFFFFFFFF, true},
		{"", 0, false},
		{"0x", 0, false},
		{"40B8E", 0, false},
		{"263054", 0, false},
		{"0xG1", 0, false},
		{"0x1 ", 0, false},
		{" 0x1", 0, false},
		{"0x1_0", 0, false},
		{"0x+1", 0, false},
		{"-0x1", 0, false},
		{"0x0x1", 0, false},
		{"0x10000000000000000", 0, false}, // 17 digits: more than 64 bits
	} {
		got, err := parseHWND(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseHWND(%q) = %#x, %v; want %#x, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestHexHWND(t *testing.T) {
	for _, tc := range []struct {
		in   uintptr
		want string
	}{
		{0, "0x0"},
		{0x40B8E, "0x40B8E"},
		{0xFFFFFFFF, "0xFFFFFFFF"},
	} {
		if got := hexHWND(tc.in); got != tc.want {
			t.Errorf("hexHWND(%#x) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExStyleValues pins the WS_EX_* values the overlay checks (SPK-09)
// against the Win32 headers.
func TestExStyleValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"WS_EX_TOPMOST", wsExTopmost, 0x8},
		{"WS_EX_TRANSPARENT", wsExTransparent, 0x20},
		{"WS_EX_TOOLWINDOW", wsExToolWindow, 0x80},
		{"WS_EX_APPWINDOW", wsExAppWindow, 0x40000},
		{"WS_EX_LAYERED", wsExLayered, 0x80000},
		{"WS_EX_NOACTIVATE", wsExNoActivate, 0x08000000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#x, want %#x", tc.name, tc.got, tc.want)
		}
	}
}

func TestExStyleNamesTable(t *testing.T) {
	var prev uint32
	seen := map[string]bool{}
	for _, s := range exStyleNames {
		if bits.OnesCount32(s.bit) != 1 {
			t.Errorf("%s = %#x: want exactly one bit", s.name, s.bit)
		}
		if s.bit <= prev {
			t.Errorf("%s = %#x is not above the previous bit %#x: the table must ascend", s.name, s.bit, prev)
		}
		if seen[s.name] {
			t.Errorf("%s is listed twice", s.name)
		}
		seen[s.name] = true
		prev = s.bit
	}
}

func TestExStyleString(t *testing.T) {
	for _, tc := range []struct {
		style uint32
		want  string
	}{
		{0, ""},
		{wsExNoActivate, "NOACTIVATE"},
		{wsExTopmost | wsExTransparent | wsExLayered | wsExNoActivate, "TOPMOST TRANSPARENT LAYERED NOACTIVATE"},
		{wsExWindowEdge | wsExAppWindow, "WINDOWEDGE APPWINDOW"},
		{wsExTopmost | 0x01000000, "TOPMOST other=0x1000000"},
		{0x10000000, "other=0x10000000"},
		{0xFFFFFFFF, "DLGMODALFRAME NOPARENTNOTIFY TOPMOST ACCEPTFILES TRANSPARENT MDICHILD TOOLWINDOW WINDOWEDGE CLIENTEDGE CONTEXTHELP " +
			"RIGHT RTLREADING LEFTSCROLLBAR CONTROLPARENT STATICEDGE APPWINDOW LAYERED NOINHERITLAYOUT NOREDIRECTIONBITMAP LAYOUTRTL " +
			"COMPOSITED NOACTIVATE other=0xF5808802"},
	} {
		if got := exStyleString(tc.style); got != tc.want {
			t.Errorf("exStyleString(%#x) = %q, want %q", tc.style, got, tc.want)
		}
	}
}

// fakeWindowReader serves window state from memory.
type fakeWindowReader struct {
	fg      uintptr
	fgPID   uint32
	fgErr   error
	windows map[uintptr]windowInfo
	err     error // returned by Window for every handle that is in windows
	asked   []uintptr
}

func (f *fakeWindowReader) Foreground() (uintptr, uint32, error) {
	return f.fg, f.fgPID, f.fgErr
}

func (f *fakeWindowReader) Window(hwnd uintptr) (windowInfo, error) {
	f.asked = append(f.asked, hwnd)
	w, ok := f.windows[hwnd]
	switch {
	case !ok:
		return windowInfo{}, fmt.Errorf("window %#x: %w", hwnd, errNotWindow)
	case f.err != nil:
		return windowInfo{}, f.err
	}
	return w, nil
}

// overlayLike is a window that looks like the overlay of ADR-0063: topmost,
// click-through, layered and never activated.
var overlayLike = windowInfo{
	HWND:    0x40B8E,
	ExStyle: wsExTopmost | wsExTransparent | wsExLayered | wsExNoActivate, // 0x08080028
	Visible: true,
	PID:     4120,
}

func newFakeReader() *fakeWindowReader {
	return &fakeWindowReader{
		fg:      0x30A12,
		fgPID:   4120,
		windows: map[uintptr]windowInfo{overlayLike.HWND: overlayLike},
	}
}

func TestReportWindowJSON(t *testing.T) {
	hidden := windowInfo{HWND: 0x77, ExStyle: 0xF8000000, Visible: false, PID: 9}
	for _, tc := range []struct {
		name       string
		reader     func() *fakeWindowReader
		hwnd       uintptr
		withWindow bool
		want       string
	}{
		{
			name: "foreground only", reader: newFakeReader,
			want: `{"foreground":"0x30A12","foregroundPid":4120}` + "\n",
		},
		{
			name: "no foreground window", reader: func() *fakeWindowReader { r := newFakeReader(); r.fg, r.fgPID = 0, 0; return r },
			want: `{"foreground":"0x0","foregroundPid":0}` + "\n",
		},
		{
			name: "with hwnd", reader: newFakeReader, hwnd: 0x40B8E, withWindow: true,
			// 0x08080028 is 134742056
			want: `{"foreground":"0x30A12","foregroundPid":4120,"window":{"hwnd":"0x40B8E","exStyle":134742056,"visible":true,"pid":4120}}` + "\n",
		},
		{
			name: "hidden window with the top style bits", reader: func() *fakeWindowReader {
				r := newFakeReader()
				r.windows[hidden.HWND] = hidden
				return r
			}, hwnd: 0x77, withWindow: true,
			// 0xF8000000 is 4160749568: unsigned, never negative
			want: `{"foreground":"0x30A12","foregroundPid":4120,"window":{"hwnd":"0x77","exStyle":4160749568,"visible":false,"pid":9}}` + "\n",
		},
	} {
		var out, errOut bytes.Buffer
		code := reportWindow(tc.reader(), tc.hwnd, tc.withWindow, true, &out, &errOut)
		if code != 0 || out.String() != tc.want || errOut.Len() != 0 {
			t.Errorf("%s: code %d, stdout %q, stderr %q; want 0, %q and no stderr", tc.name, code, out.String(), errOut.String(), tc.want)
		}
	}
}

// TestReportWindowJSONShape checks the contract with the E2E spec: exactly one
// JSON object on one line, with these fields and no others.
func TestReportWindowJSONShape(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := reportWindow(newFakeReader(), 0x40B8E, true, true, &out, &errOut); code != 0 {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
	if n := strings.Count(out.String(), "\n"); n != 1 || !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("output %q: want one line", out.String())
	}
	dec := json.NewDecoder(&out)
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("more than one JSON value on stdout")
	}
	keys := func(m map[string]json.RawMessage) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		return ks
	}
	if got, want := len(top), 3; got != want {
		t.Errorf("top-level keys %v, want foreground, foregroundPid and window", keys(top))
	}
	for _, k := range []string{"foreground", "foregroundPid", "window"} {
		if _, ok := top[k]; !ok {
			t.Errorf("missing top-level key %q in %v", k, keys(top))
		}
	}
	var win map[string]any
	if err := json.Unmarshal(top["window"], &win); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"hwnd": "0x40B8E", "exStyle": float64(0x08080028), "visible": true, "pid": float64(4120)}
	if !reflect.DeepEqual(win, want) {
		t.Errorf("window = %v, want %v", win, want)
	}
	// Without --hwnd the window key is absent, not null.
	out.Reset()
	if code := reportWindow(newFakeReader(), 0, false, true, &out, &errOut); code != 0 {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "window") {
		t.Errorf("output %q: want no window key without --hwnd", out.String())
	}
}

func TestReportWindowHuman(t *testing.T) {
	hidden := windowInfo{HWND: 0x77, ExStyle: 0, Visible: false, PID: 9}
	for _, tc := range []struct {
		name       string
		reader     func() *fakeWindowReader
		hwnd       uintptr
		withWindow bool
		want       string
	}{
		{name: "foreground only", reader: newFakeReader, want: "foreground 0x30A12 pid 4120\n"},
		{
			name: "overlay-like", reader: newFakeReader, hwnd: 0x40B8E, withWindow: true,
			want: "foreground 0x30A12 pid 4120\n" +
				"window 0x40B8E pid 4120 visible exstyle=0x08080028 TOPMOST TRANSPARENT LAYERED NOACTIVATE\n",
		},
		{
			name: "hidden window without styles", reader: func() *fakeWindowReader {
				r := newFakeReader()
				r.windows[hidden.HWND] = hidden
				return r
			}, hwnd: 0x77, withWindow: true,
			want: "foreground 0x30A12 pid 4120\nwindow 0x77 pid 9 hidden exstyle=0x00000000\n",
		},
	} {
		var out, errOut bytes.Buffer
		code := reportWindow(tc.reader(), tc.hwnd, tc.withWindow, false, &out, &errOut)
		if code != 0 || out.String() != tc.want || errOut.Len() != 0 {
			t.Errorf("%s: code %d, stdout %q, stderr %q; want 0, %q and no stderr", tc.name, code, out.String(), errOut.String(), tc.want)
		}
	}
}

func TestReportWindowFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader func() *fakeWindowReader
		hwnd   uintptr
		want   string
	}{
		{
			name: "not a window", reader: newFakeReader, hwnd: 0x1234,
			want: "padmate-probe window: 0x1234 is not a window\n",
		},
		{
			name: "handle 0", reader: newFakeReader, hwnd: 0,
			want: "padmate-probe window: 0x0 is not a window\n",
		},
		{
			name: "foreground query fails", reader: func() *fakeWindowReader {
				r := newFakeReader()
				r.fgErr = errors.New("foreground window: boom")
				return r
			}, hwnd: 0x40B8E,
			want: "padmate-probe window: foreground window: boom\n",
		},
		{
			name: "window query fails", reader: func() *fakeWindowReader {
				r := newFakeReader()
				r.err = errors.New("boom")
				return r
			}, hwnd: 0x40B8E,
			want: "padmate-probe window: 0x40B8E: boom\n",
		},
	} {
		for _, asJSON := range []bool{false, true} {
			var out, errOut bytes.Buffer
			code := reportWindow(tc.reader(), tc.hwnd, true, asJSON, &out, &errOut)
			if code != 1 || out.Len() != 0 || errOut.String() != tc.want {
				t.Errorf("%s (json %v): code %d, stdout %q, stderr %q; want 1, no stdout and %q", tc.name, asJSON, code, out.String(), errOut.String(), tc.want)
			}
		}
	}
}

// TestRunWindow runs the subcommand end to end on a fake reader.
func TestRunWindow(t *testing.T) {
	r := newFakeReader()
	oldOpen := openWindowReader
	t.Cleanup(func() { openWindowReader = oldOpen })
	openWindowReader = func() (windowReader, error) { return r, nil }

	for _, tc := range []struct {
		args       []string
		code       int
		wantStdout string
		wantStderr string
		wantAsked  []uintptr
	}{
		{args: nil, code: 0, wantStdout: "foreground 0x30A12 pid 4120\n"},
		{args: []string{"--json"}, code: 0, wantStdout: `{"foreground":"0x30A12","foregroundPid":4120}` + "\n"},
		{
			args: []string{"--hwnd", "0x40b8e", "--json"}, code: 0, wantAsked: []uintptr{0x40B8E},
			wantStdout: `{"foreground":"0x30A12","foregroundPid":4120,"window":{"hwnd":"0x40B8E","exStyle":134742056,"visible":true,"pid":4120}}` + "\n",
		},
		{
			args: []string{"--json", "--hwnd=0X40B8E"}, code: 0, wantAsked: []uintptr{0x40B8E},
			wantStdout: `{"foreground":"0x30A12","foregroundPid":4120,"window":{"hwnd":"0x40B8E","exStyle":134742056,"visible":true,"pid":4120}}` + "\n",
		},
		{args: []string{"--hwnd", "0x9"}, code: 1, wantAsked: []uintptr{9}, wantStderr: "padmate-probe window: 0x9 is not a window\n"},
	} {
		r.asked = nil
		code, stdout, stderr := runProbe(t, append([]string{"window"}, tc.args...)...)
		if code != tc.code || stdout != tc.wantStdout || stderr != tc.wantStderr {
			t.Errorf("window %q = %d, stdout %q, stderr %q; want %d, %q, %q", tc.args, code, stdout, stderr, tc.code, tc.wantStdout, tc.wantStderr)
		}
		if !reflect.DeepEqual(r.asked, tc.wantAsked) {
			t.Errorf("window %q asked for %#x, want %#x", tc.args, r.asked, tc.wantAsked)
		}
	}
}

func TestRunWindowOpenErrors(t *testing.T) {
	oldOpen := openWindowReader
	t.Cleanup(func() { openWindowReader = oldOpen })
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errUnsupported, "padmate-probe: window is only supported on Windows\n"},
		{errors.New("no user32"), "padmate-probe window: no user32\n"},
	} {
		openWindowReader = func() (windowReader, error) { return nil, tc.err }
		code, stdout, stderr := runProbe(t, "window", "--json")
		if code != 1 || stdout != "" || stderr != tc.want {
			t.Errorf("open error %v: code %d, stdout %q, stderr %q; want 1, no stdout and %q", tc.err, code, stdout, stderr, tc.want)
		}
	}
}
