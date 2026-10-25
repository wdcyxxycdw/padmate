package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const windowUsage = `usage: padmate-probe window [--hwnd 0xHEX] [--json]

Reads the window state and changes nothing: it installs no hook, sends no
input and does not move the foreground. It prints the foreground window
(GetForegroundWindow) and the id of the process that owns it; both are 0 when
no window has the foreground. With --hwnd it also prints, for that window,
its extended style (GWL_EXSTYLE), whether it is visible (IsWindowVisible) and
the id of the process that owns it.

Exit codes: 0 success, 1 the HWND is not a window or a query failed (a message
on stderr), 2 bad flags.

The human format is two lines, such as
  foreground 0x30A12 pid 4120
  window 0x40B8E pid 4120 visible exstyle=0x08080028 TOPMOST TRANSPARENT LAYERED NOACTIVATE
where the names after exstyle are the WS_EX_* bits that are set.

flags:
  --hwnd 0xHEX  also report this window; hex digits after a 0x prefix
  --json        print one JSON object on one line: foreground (a "0xHEX"
                string), foregroundPid and, only with --hwnd, window with the
                fields hwnd (a "0xHEX" string), exStyle (GWL_EXSTYLE as an
                unsigned number), visible and pid
`

// Extended window styles (WS_EX_*) that padmate cares about, and the names
// the human format gives them. The overlay window (ADR-0063) must have
// NOACTIVATE, TRANSPARENT and TOPMOST.
const (
	wsExDlgModalFrame     uint32 = 0x00000001
	wsExNoParentNotify    uint32 = 0x00000004
	wsExTopmost           uint32 = 0x00000008
	wsExAcceptFiles       uint32 = 0x00000010
	wsExTransparent       uint32 = 0x00000020
	wsExMDIChild          uint32 = 0x00000040
	wsExToolWindow        uint32 = 0x00000080
	wsExWindowEdge        uint32 = 0x00000100
	wsExClientEdge        uint32 = 0x00000200
	wsExContextHelp       uint32 = 0x00000400
	wsExRight             uint32 = 0x00001000
	wsExRTLReading        uint32 = 0x00002000
	wsExLeftScrollbar     uint32 = 0x00004000
	wsExControlParent     uint32 = 0x00010000
	wsExStaticEdge        uint32 = 0x00020000
	wsExAppWindow         uint32 = 0x00040000
	wsExLayered           uint32 = 0x00080000
	wsExNoInheritLayout   uint32 = 0x00100000
	wsExNoRedirectionBits uint32 = 0x00200000
	wsExLayoutRTL         uint32 = 0x00400000
	wsExComposited        uint32 = 0x02000000
	wsExNoActivate        uint32 = 0x08000000
)

// exStyleNames lists the known WS_EX_* bits in ascending order.
var exStyleNames = []struct {
	bit  uint32
	name string
}{
	{wsExDlgModalFrame, "DLGMODALFRAME"},
	{wsExNoParentNotify, "NOPARENTNOTIFY"},
	{wsExTopmost, "TOPMOST"},
	{wsExAcceptFiles, "ACCEPTFILES"},
	{wsExTransparent, "TRANSPARENT"},
	{wsExMDIChild, "MDICHILD"},
	{wsExToolWindow, "TOOLWINDOW"},
	{wsExWindowEdge, "WINDOWEDGE"},
	{wsExClientEdge, "CLIENTEDGE"},
	{wsExContextHelp, "CONTEXTHELP"},
	{wsExRight, "RIGHT"},
	{wsExRTLReading, "RTLREADING"},
	{wsExLeftScrollbar, "LEFTSCROLLBAR"},
	{wsExControlParent, "CONTROLPARENT"},
	{wsExStaticEdge, "STATICEDGE"},
	{wsExAppWindow, "APPWINDOW"},
	{wsExLayered, "LAYERED"},
	{wsExNoInheritLayout, "NOINHERITLAYOUT"},
	{wsExNoRedirectionBits, "NOREDIRECTIONBITMAP"},
	{wsExLayoutRTL, "LAYOUTRTL"},
	{wsExComposited, "COMPOSITED"},
	{wsExNoActivate, "NOACTIVATE"},
}

// exStyleString names the WS_EX_* bits set in style, in ascending order of
// the bit, such as "TOPMOST TRANSPARENT LAYERED NOACTIVATE". Bits without a
// name are collected as "other=0xHEX". It returns "" for a style of 0.
func exStyleString(style uint32) string {
	var parts []string
	rest := style
	for _, s := range exStyleNames {
		if style&s.bit != 0 {
			parts = append(parts, s.name)
			rest &^= s.bit
		}
	}
	if rest != 0 {
		parts = append(parts, fmt.Sprintf("other=0x%X", rest))
	}
	return strings.Join(parts, " ")
}

// errNotWindow is returned by a windowReader for a handle that is not a
// window (IsWindow is false), such as 0 or a window that has been destroyed.
var errNotWindow = errors.New("not a window")

// windowInfo is what the probe reports about one window.
type windowInfo struct {
	HWND    uintptr
	ExStyle uint32 // GWL_EXSTYLE
	Visible bool   // IsWindowVisible: the WS_VISIBLE bit of it and of every parent
	PID     uint32 // the process that owns the window
}

// windowReader reads window state: the Win32 API on Windows, a fake in
// tests. Every method only reads.
type windowReader interface {
	// Foreground returns the foreground window and the id of its process.
	// Both are 0 when no window has the foreground.
	Foreground() (hwnd uintptr, pid uint32, err error)
	// Window describes hwnd. It returns an error that wraps errNotWindow
	// when hwnd is not a window.
	Window(hwnd uintptr) (windowInfo, error)
}

// openWindowReader returns the windowReader of the platform. Tests replace
// it.
var openWindowReader = newWindowReader

func runWindow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("window", flag.ContinueOnError)
	hwndArg := fs.String("hwnd", "", "also report this window: 0x and hex digits")
	asJSON := fs.Bool("json", false, "print a JSON object")
	if code, ok := parseFlags(fs, args, windowUsage, stdout, stderr); !ok {
		return code
	}
	hwndSet := false
	fs.Visit(func(f *flag.Flag) { hwndSet = hwndSet || f.Name == "hwnd" })
	var hwnd uintptr
	var bad string
	switch {
	case fs.NArg() > 0:
		bad = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	case hwndSet:
		var err error
		if hwnd, err = parseHWND(*hwndArg); err != nil {
			bad = "--hwnd: " + err.Error()
		}
	}
	if bad != "" {
		fmt.Fprintln(stderr, "padmate-probe window:", bad)
		fmt.Fprint(stderr, windowUsage)
		return 2
	}
	r, err := openWindowReader()
	switch {
	case errors.Is(err, errUnsupported):
		fmt.Fprintln(stderr, "padmate-probe: window is only supported on Windows")
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "padmate-probe window:", err)
		return 1
	}
	return reportWindow(r, hwnd, hwndSet, *asJSON, stdout, stderr)
}

// parseHWND parses the value of --hwnd: "0x" and at least one hex digit,
// such as 0x40B8E. The prefix is required so a decimal handle is an error,
// not a different window. 0 parses; it is not a window, which reportWindow
// finds out.
func parseHWND(s string) (uintptr, error) {
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok {
		digits, ok = strings.CutPrefix(s, "0X")
	}
	if !ok || digits == "" {
		return 0, fmt.Errorf("want 0x and hex digits, such as 0x40B8E, not %q", s)
	}
	v, err := strconv.ParseUint(digits, 16, 64)
	switch {
	case errors.Is(err, strconv.ErrRange):
		return 0, fmt.Errorf("%q does not fit in a window handle", s)
	case err != nil:
		return 0, fmt.Errorf("want 0x and hex digits, such as 0x40B8E, not %q", s)
	}
	if uint64(uintptr(v)) != v {
		return 0, fmt.Errorf("%q does not fit in a window handle", s)
	}
	return uintptr(v), nil
}

// reportWindow prints the foreground window and, with withWindow, hwnd. It
// returns the exit code: 1 when a Win32 query fails or hwnd is not a window.
func reportWindow(r windowReader, hwnd uintptr, withWindow, asJSON bool, stdout, stderr io.Writer) int {
	fg, fgPID, err := r.Foreground()
	if err != nil {
		fmt.Fprintln(stderr, "padmate-probe window:", err)
		return 1
	}
	var info *windowInfo
	if withWindow {
		w, err := r.Window(hwnd)
		switch {
		case errors.Is(err, errNotWindow):
			fmt.Fprintf(stderr, "padmate-probe window: %s is not a window\n", hexHWND(hwnd))
			return 1
		case err != nil:
			fmt.Fprintf(stderr, "padmate-probe window: %s: %v\n", hexHWND(hwnd), err)
			return 1
		}
		info = &w
	}
	var line string
	if asJSON {
		b, err := json.Marshal(newWindowReportJSON(fg, fgPID, info))
		if err != nil {
			fmt.Fprintln(stderr, "padmate-probe window:", err)
			return 1
		}
		line = string(b)
	} else {
		line = formatWindowReport(fg, fgPID, info)
	}
	if _, err := fmt.Fprintln(stdout, line); err != nil {
		fmt.Fprintln(stderr, "padmate-probe window:", err)
		return 1
	}
	return 0
}

// hexHWND formats a window handle as "0x40B8E".
func hexHWND(h uintptr) string { return fmt.Sprintf("0x%X", uint64(h)) }

// formatWindowReport formats the human output: a foreground line, then a
// window line when w is not nil.
func formatWindowReport(fg uintptr, fgPID uint32, w *windowInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "foreground %s pid %d", hexHWND(fg), fgPID)
	if w != nil {
		visible := "hidden"
		if w.Visible {
			visible = "visible"
		}
		fmt.Fprintf(&b, "\nwindow %s pid %d %s exstyle=0x%08X", hexHWND(w.HWND), w.PID, visible, w.ExStyle)
		if names := exStyleString(w.ExStyle); names != "" {
			b.WriteString(" " + names)
		}
	}
	return b.String()
}

// windowReportJSON is the output of "window --json".
type windowReportJSON struct {
	Foreground    string      `json:"foreground"`
	ForegroundPID uint32      `json:"foregroundPid"`
	Window        *windowJSON `json:"window,omitempty"`
}

// windowJSON is the window of "window --hwnd H --json".
type windowJSON struct {
	HWND    string `json:"hwnd"`
	ExStyle uint32 `json:"exStyle"`
	Visible bool   `json:"visible"`
	PID     uint32 `json:"pid"`
}

func newWindowReportJSON(fg uintptr, fgPID uint32, w *windowInfo) windowReportJSON {
	out := windowReportJSON{Foreground: hexHWND(fg), ForegroundPID: fgPID}
	if w != nil {
		out.Window = &windowJSON{HWND: hexHWND(w.HWND), ExStyle: w.ExStyle, Visible: w.Visible, PID: w.PID}
	}
	return out
}
