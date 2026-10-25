package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// None of these tests installs a hook: they run record on a fakeSource.

// notch is a wheel event of delta, injected with a nonce, as a hook sees the
// notch that SendInput makes (the delta is the high word of mouseData). The
// hook gets the nonce of dwExtraInfo only, not the tag (measured 2026-10-19).
func notch(seq uint64, time uint32, msg uint32, delta int16, nonce uint32, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Time: time, Msg: msg, MouseData: uint32(uint16(delta)) << 16, Flags: winhook.MouseFlagInjected,
		Extra: uintptr(nonce), Swallowed: swallowed, Seq: seq,
	}
}

// A mouse hook sees the nonce and not the tag, so "padmate" swallows the
// injected events of the nonce it was given, and no others: not the physical
// mouse's, and not another program's injected events, which have a zero high
// half too.
func TestMouseSwallow(t *testing.T) {
	const nonce = 42
	padmate := notch(0, 0, winhook.WMMouseWheel, 120, nonce, false)
	tagged := padmate
	tagged.Extra = output.ExtraInfo(nonce) // in case some Windows keeps the tag
	otherNonce := notch(0, 0, winhook.WMMouseWheel, 120, nonce+1, false)
	other := winhook.MouseEvent{Flags: winhook.MouseFlagInjected, Extra: 0x1234}
	zero := winhook.MouseEvent{Flags: winhook.MouseFlagInjected}
	highHalf := padmate
	highHalf.Extra |= 0x1_0000_0000
	physical := winhook.MouseEvent{Extra: uintptr(nonce)} // not injected, the nonce is right
	if mouseSwallow("none", nonce) != nil {
		t.Error("--swallow none installs a mouse predicate")
	}
	if mouseSwallow("bogus", nonce) != nil {
		t.Error("an unknown mode installs a mouse predicate")
	}
	for _, tc := range []struct {
		mode string
		e    winhook.MouseEvent
		want bool
	}{
		{"padmate", padmate, true},
		{"padmate", tagged, true},
		{"padmate", otherNonce, false},
		{"padmate", other, false},
		{"padmate", zero, false},
		{"padmate", highHalf, false},
		{"padmate", physical, false},
		{"padmate", winhook.MouseEvent{}, false},
		{"injected", padmate, true},
		{"injected", other, true},
		{"injected", zero, true},
		{"injected", winhook.MouseEvent{}, false},
		{"injected", physical, false},
	} {
		if got := mouseSwallow(tc.mode, nonce)(tc.e); got != tc.want {
			t.Errorf("mouse swallow %s(%+v) = %v, want %v", tc.mode, tc.e, got, tc.want)
		}
	}
	for mode := range swallowModes {
		if mode != "none" && mouseSwallow(mode, nonce) == nil {
			t.Errorf("mouseSwallow has no predicate for %q, which --swallow accepts", mode)
		}
	}
}

// The probe's mouse hook reports everything but the physical mouse's moves,
// which are hundreds a second.
func TestWatchMouse(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    winhook.MouseEvent
		want bool
	}{
		{"physical move", winhook.MouseEvent{Msg: winhook.WMMouseMove, X: 5, Y: 6}, false},
		{"injected move", winhook.MouseEvent{Msg: winhook.WMMouseMove, Flags: winhook.MouseFlagInjected}, true},
		{"physical wheel", winhook.MouseEvent{Msg: winhook.WMMouseWheel, MouseData: 120 << 16}, true},
		{"injected hwheel", notch(1, 1, winhook.WMMouseHWheel, -120, 1, false), true},
		{"physical button", winhook.MouseEvent{Msg: winhook.WMLButtonDown}, true},
	} {
		if got := watchMouse(tc.e); got != tc.want {
			t.Errorf("%s: watchMouse = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFormatMouseEvent(t *testing.T) {
	for _, tc := range []struct {
		e    winhook.MouseEvent
		want string
	}{
		{notch(1, 52830015, winhook.WMMouseWheel, 120, 8812, true),
			"  52830015 wheel v +120 injected extra=0x226C swallowed"},
		{notch(1, 7, winhook.WMMouseWheel, -120, 8812, false),
			"         7 wheel v -120 injected extra=0x226C"},
		{notch(1, 100, winhook.WMMouseHWheel, -120, 3, true),
			"       100 wheel h -120 injected extra=0x3 swallowed"},
		{notch(1, 100, winhook.WMMouseWheel, 0, 3, true),
			"       100 wheel v +0 injected extra=0x3 swallowed"},
		// The tag, if some Windows delivers it.
		{winhook.MouseEvent{Time: 5, Msg: winhook.WMMouseWheel, Flags: winhook.MouseFlagInjected, MouseData: 120 << 16, Extra: output.ExtraInfo(8812), Swallowed: true},
			"         5 wheel v +120 injected padmate nonce=8812 swallowed"},
		{winhook.MouseEvent{Time: 9, Msg: winhook.WMMouseWheel, MouseData: 120 << 16},
			"         9 wheel v +120"},
		{winhook.MouseEvent{Time: 9, Msg: winhook.WMMouseWheel, Flags: winhook.MouseFlagInjected | winhook.MouseFlagLowerILInjected, MouseData: 120 << 16, Extra: 0x1234},
			"         9 wheel v +120 injected lowerIL extra=0x1234"},
		{winhook.MouseEvent{Time: 1, Msg: winhook.WMLButtonDown},
			"         1 mouse WM_LBUTTONDOWN"},
		{winhook.MouseEvent{Time: 2, Msg: winhook.WMXButtonUp, MouseData: 2 << 16, Flags: winhook.MouseFlagInjected},
			"         2 mouse WM_XBUTTONUP x2 injected"},
		{winhook.MouseEvent{Time: 3, Msg: 0x0203},
			"         3 mouse 0x0203"},
	} {
		if got := formatMouseEvent(tc.e); got != tc.want {
			t.Errorf("formatMouseEvent(%+v)\n got %q\nwant %q", tc.e, got, tc.want)
		}
	}
}

func TestMouseEventJSON(t *testing.T) {
	e := notch(0, 5, winhook.WMMouseWheel, -120, 77, true)
	b, err := json.Marshal(newMouseEventJSON(3, e))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":3,"kind":"mouse","time":5,"msg":"WM_MOUSEWHEEL","delta":-120,"mouseData":"0xFF880000","xButton":0,` +
		`"injected":true,"lowerIL":false,"extra":"0x4D","padmate":false,"nonce":77,"swallowed":true}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
	x := newMouseEventJSON(1, winhook.MouseEvent{Msg: winhook.WMXButtonDown, MouseData: 1 << 16, Extra: 77})
	if x.XButton != 1 || x.Delta != 0 || x.Padmate || x.Nonce != 0 || x.Extra != "0x4D" {
		t.Errorf("x button event: %+v, want xButton 1, delta 0, not padmate, no nonce (not injected), extra 0x4D", x)
	}
	// If some Windows keeps the whole tag, the event is padmate's as well.
	tagged := e
	tagged.Extra = output.ExtraInfo(77)
	if j := newMouseEventJSON(1, tagged); !j.Padmate || j.Nonce != 77 || j.Extra != "0x7061646D0000004D" {
		t.Errorf("tagged event: %+v, want padmate, nonce 77, the whole extra", j)
	}
}

// mouseSource is a fakeSource with a mouse channel holding mouse events.
func mouseSource(keys []winhook.Event, mouse []winhook.MouseEvent) *fakeSource {
	s := newFakeSource(keys...)
	s.mouse = make(chan winhook.MouseEvent, len(mouse)+1)
	for _, e := range mouse {
		s.mouse <- e
	}
	return s
}

// The probe prints the events of both hooks in the order the hook thread saw
// them (Seq), however the two channels hold them: Ctrl down, a notch, Ctrl up
// and another notch.
func TestRecordMergesBySeq(t *testing.T) {
	ctrlDown := winhook.Event{Seq: 1, Time: 1, VK: 0xA2, Scan: 0x1D, Msg: winhook.WMKeyDown, Flags: winhook.FlagInjected}
	ctrlUp := winhook.Event{Seq: 3, Time: 3, VK: 0xA2, Scan: 0x1D, Msg: winhook.WMKeyUp, Flags: winhook.FlagInjected | winhook.FlagUp}
	first := notch(2, 2, winhook.WMMouseWheel, 120, 9, true)
	second := notch(4, 4, winhook.WMMouseWheel, -120, 9, true)
	src := mouseSource([]winhook.Event{ctrlDown, ctrlUp}, []winhook.MouseEvent{first, second})
	var out, errOut bytes.Buffer
	if code := record(context.Background(), src, recordOptions{count: 4}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	var kinds []string
	for l := range strings.Lines(out.String()) {
		switch {
		case strings.Contains(l, " wheel "):
			kinds = append(kinds, "wheel")
		case strings.Contains(l, " down "):
			kinds = append(kinds, "down")
		case strings.Contains(l, " up "):
			kinds = append(kinds, "up")
		}
	}
	if got := strings.Join(kinds, " "); got != "down wheel up wheel" {
		t.Errorf("order %q, want %q:\n%s", got, "down wheel up wheel", out.String())
	}
	if src.closes == 0 {
		t.Error("record did not close the source")
	}
}

// Merging by Seq does not depend on what is queued when: an event of the
// other kind that is already in its channel and came first is returned first.
func TestStreamOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keys  []uint64
		mouse []uint64
		want  string
	}{
		{"alternating", []uint64{1, 3, 5}, []uint64{2, 4}, "k1 m2 k3 m4 k5"},
		{"mouse first", []uint64{3}, []uint64{1, 2}, "m1 m2 k3"},
		{"keys first", []uint64{1, 2}, []uint64{3}, "k1 k2 m3"},
		{"keys only", []uint64{1, 2, 3}, nil, "k1 k2 k3"},
		{"mouse only", nil, []uint64{4, 7}, "m4 m7"},
		{"ties go to the keyboard", []uint64{0, 0}, []uint64{0}, "k0 k0 m0"},
		{"empty", nil, nil, ""},
	} {
		var ks []winhook.Event
		for _, q := range tc.keys {
			ks = append(ks, winhook.Event{Seq: q})
		}
		var ms []winhook.MouseEvent
		for _, q := range tc.mouse {
			ms = append(ms, winhook.MouseEvent{Seq: q})
		}
		src := mouseSource(ks, ms)
		_ = src.Close() // closes both channels, holding what was queued
		st := &stream{keys: src.Events(), mouse: src.MouseEvents()}
		var got []string
		for {
			e, ok := st.next(context.Background())
			if !ok {
				break
			}
			if e.key != nil {
				got = append(got, "k"+strconv.FormatUint(e.key.Seq, 10))
			} else {
				got = append(got, "m"+strconv.FormatUint(e.mouse.Seq, 10))
			}
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s: order %q, want %q", tc.name, g, tc.want)
		}
	}
}

// With nothing queued, next waits, and ctx ends the wait; with an event of one
// kind queued it does not wait for the other.
func TestStreamWaits(t *testing.T) {
	src := mouseSource(nil, nil)
	st := &stream{keys: src.Events(), mouse: src.MouseEvents()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e, ok := st.next(ctx); ok {
		t.Errorf("next on empty channels = %+v, want it to wait until ctx is done", e)
	}
	src.mouse <- winhook.MouseEvent{Seq: 5}
	if e, ok := st.next(context.Background()); !ok || e.mouse == nil || e.mouse.Seq != 5 {
		t.Errorf("next with a mouse event queued = %+v, %v, want it at once", e, ok)
	}
}

func TestRecordJSONMouseLines(t *testing.T) {
	src := mouseSource(
		[]winhook.Event{{Seq: 1, Time: 1, VK: 0x41, Msg: winhook.WMKeyDown}},
		[]winhook.MouseEvent{notch(2, 2, winhook.WMMouseHWheel, 120, 5, true)},
	)
	var out, errOut bytes.Buffer
	if code := record(context.Background(), src, recordOptions{json: true, count: 2}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines:\n%s", len(lines), out.String())
	}
	var key eventJSON
	var mouse mouseEventJSON
	if err := json.Unmarshal([]byte(lines[0]), &key); err != nil || key.Msg != "WM_KEYDOWN" || key.Seq != 1 {
		t.Errorf("line 1 = %+v (%v), want the key", key, err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &mouse); err != nil || mouse.Kind != "mouse" || mouse.Msg != "WM_MOUSEHWHEEL" ||
		mouse.Delta != 120 || mouse.Seq != 2 || mouse.Padmate || mouse.Nonce != 5 || !mouse.Swallowed {
		t.Errorf("line 2 = %+v (%v), want the mouse notch", mouse, err)
	}
	if strings.Contains(lines[0], `"kind"`) {
		t.Errorf("a key line has a kind field: %s", lines[0])
	}
}

// Dropped events of the mouse hook are reported with the keyboard's; a failed
// Close leaves both channels open, and nothing is drained.
func TestRecordMouseDroppedAndCloseError(t *testing.T) {
	src := mouseSource(nil, []winhook.MouseEvent{notch(1, 1, winhook.WMMouseWheel, 120, 1, true)})
	src.dropped = 4
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	if code := record(canceled, src, recordOptions{}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(errOut.String(), "the hook dropped 4 events") {
		t.Errorf("stdout %q, stderr %q; want the notch and the dropped report", out.String(), errOut.String())
	}

	bad := mouseSource(nil, nil)
	bad.closeErr = errors.New("unhook failed")
	out.Reset()
	errOut.Reset()
	done := make(chan int)
	go func() { done <- record(canceled, bad, recordOptions{}, &out, &errOut) }()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(errOut.String(), "unhook failed") {
			t.Errorf("exit code %d, stderr %q; want 1 and the Close error", code, errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("record waited for channels that stay open after a failed Close")
	}
}

func TestRecordMouseFlagIsDocumented(t *testing.T) {
	for _, want := range []string{"--mouse", "--nonce", "wheel v +120", `kind ("mouse")`, "low 32 bits"} {
		if !strings.Contains(recordUsage, want) {
			t.Errorf("the record usage does not mention %q", want)
		}
	}
}

// buttonEvent is an injected button event of msg with nonce, as a hook sees the
// click that SendInput makes: the side button number is the high word of
// mouseData, and the hook gets the nonce of dwExtraInfo only.
func buttonEvent(seq uint64, time uint32, msg uint32, xbutton uint32, nonce uint32, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Time: time, Msg: msg, MouseData: xbutton << 16, Flags: winhook.MouseFlagInjected,
		Extra: uintptr(nonce), Swallowed: swallowed, Seq: seq,
	}
}

// The injected mouse buttons of padmate (ADR-0062, second batch) are printed
// and swallowed like its wheel notches: the physical mouse's buttons are
// printed and never swallowed, and a side button shows its number.
func TestMouseButtonEvents(t *testing.T) {
	const nonce = 42
	for _, tc := range []struct {
		name string
		e    winhook.MouseEvent
		mode string
		want bool
	}{
		{"padmate swallows its click", buttonEvent(0, 0, winhook.WMLButtonDown, 0, nonce, false), "padmate", true},
		{"padmate swallows its release", buttonEvent(0, 0, winhook.WMRButtonUp, 0, nonce, false), "padmate", true},
		{"padmate swallows its side button", buttonEvent(0, 0, winhook.WMXButtonDown, 2, nonce, false), "padmate", true},
		{"padmate leaves another nonce", buttonEvent(0, 0, winhook.WMLButtonDown, 0, nonce+1, false), "padmate", false},
		{"padmate leaves the physical button", winhook.MouseEvent{Msg: winhook.WMLButtonDown, Extra: nonce}, "padmate", false},
		{"injected swallows any injected button", buttonEvent(0, 0, winhook.WMMButtonDown, 0, 7, false), "injected", true},
		{"injected leaves the physical button", winhook.MouseEvent{Msg: winhook.WMXButtonUp, MouseData: 1 << 16}, "injected", false},
	} {
		if got := mouseSwallow(tc.mode, nonce)(tc.e); got != tc.want {
			t.Errorf("%s: mouse swallow %s(%+v) = %v, want %v", tc.name, tc.mode, tc.e, got, tc.want)
		}
	}
	for _, e := range []winhook.MouseEvent{
		buttonEvent(1, 1, winhook.WMLButtonDown, 0, 1, false),
		buttonEvent(2, 2, winhook.WMXButtonUp, 1, 1, false),
		{Msg: winhook.WMRButtonDown},
	} {
		if !watchMouse(e) {
			t.Errorf("watchMouse(%+v) = false: the probe prints every button", e)
		}
	}

	for _, tc := range []struct {
		e    winhook.MouseEvent
		want string
	}{
		{buttonEvent(1, 6, winhook.WMLButtonDown, 0, 8812, true),
			"         6 mouse WM_LBUTTONDOWN injected extra=0x226C swallowed"},
		{buttonEvent(1, 7, winhook.WMRButtonUp, 0, 3, true),
			"         7 mouse WM_RBUTTONUP injected extra=0x3 swallowed"},
		{buttonEvent(1, 8, winhook.WMMButtonDown, 0, 3, false),
			"         8 mouse WM_MBUTTONDOWN injected extra=0x3"},
		{buttonEvent(1, 9, winhook.WMXButtonDown, 1, 8812, true),
			"         9 mouse WM_XBUTTONDOWN x1 injected extra=0x226C swallowed"},
		{buttonEvent(1, 10, winhook.WMXButtonUp, 2, 8812, true),
			"        10 mouse WM_XBUTTONUP x2 injected extra=0x226C swallowed"},
	} {
		if got := formatMouseEvent(tc.e); got != tc.want {
			t.Errorf("formatMouseEvent(%+v)\n got %q\nwant %q", tc.e, got, tc.want)
		}
	}

	b, err := json.Marshal(newMouseEventJSON(4, buttonEvent(4, 11, winhook.WMXButtonUp, 2, 77, true)))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":4,"kind":"mouse","time":11,"msg":"WM_XBUTTONUP","delta":0,"mouseData":"0x20000","xButton":2,` +
		`"injected":true,"lowerIL":false,"extra":"0x4D","padmate":false,"nonce":77,"swallowed":true}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
	b, err = json.Marshal(newMouseEventJSON(5, buttonEvent(5, 12, winhook.WMLButtonDown, 0, 77, true)))
	if err != nil {
		t.Fatal(err)
	}
	want = `{"seq":5,"kind":"mouse","time":12,"msg":"WM_LBUTTONDOWN","delta":0,"mouseData":"0x0","xButton":0,` +
		`"injected":true,"lowerIL":false,"extra":"0x4D","padmate":false,"nonce":77,"swallowed":true}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
}
