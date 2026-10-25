package output_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// numLockOnVK is the VK of a numpad scan code while NumLock is on; with
// NumLock off the code gives the navigation VK that MapVirtualKeyEx
// returns. Injected scan codes follow the same rule as the physical keys.
var numLockOnVK = map[uint8]uint32{
	0x47: 0x67, 0x48: 0x68, 0x49: 0x69,
	0x4B: 0x64, 0x4C: 0x65, 0x4D: 0x66,
	0x4F: 0x61, 0x50: 0x62, 0x51: 0x63,
	0x52: 0x60, 0x53: 0x6E,
}

// extraScans are raw scan code keys injected on top of the key table:
// plain and E0 codes, numpad codes that keys.Table sends by VK, SysRq and
// codes that have no VK on most layouts. Codes with side effects outside
// the keyboard (power, sleep, media) are left out even though every event
// is swallowed.
var extraScans = []string{
	"sc:0x01", "sc:0x2C", "sc:0x47", "sc:0x4C", "sc:0x52", "sc:0x53", "sc:0x54",
	"sc:0x70", "sc:0x73", "sc:0x79", "sc:0x7B", "sc:0x7D",
	"sc:0xE01C", "sc:0xE01D", "sc:0xE035", "sc:0xE037", "sc:0xE038",
	"sc:0xE047", "sc:0xE053", "sc:0xE05B", "sc:0xE05D",
}

// layoutDependent are the named keys whose VK comes from the layout that
// has them (kbd106 for the JIS keys), so that the US VK of the key table
// is only a reference.
var layoutDependent = map[string]bool{
	"IntlBackslash": true, "IntlRo": true, "IntlYen": true,
	"Convert": true, "NonConvert": true, "KanaMode": true,
}

type injectCase struct {
	key   keys.Key
	event output.KeyEvent
}

// TestInjectKeys (SPK-02) injects presses and releases of every key of
// keys.Table and of extraScans, and checks what a low-level hook sees:
// scan code, E0 flag, LLKHF_INJECTED, the tag and the VK. Every event is
// swallowed by the hook, so nothing reaches the foreground application.
//
// Each key goes out as one SendInput batch with a canary behind it
// (injecttest.InjectFenced): the canary says exactly which events the hook
// saw, and proves the hook still live before the next key. The test stops
// at the first batch the hook does not fully swallow.
//
// The mouse keys of the table (the wheel keys and the buttons, ADR-0062) are
// mouse inputs. They go out the same way, after a mouse canary has proven the
// mouse hook live, as batches of injecttest.InjectFencedMixed, and the mouse
// hook checks what it sees: the message, the delta or the side button number,
// LLMHF_INJECTED and the nonce. Each wheel key is tapped twice and so turns
// the wheel two notches, which the mouse hook swallows; the release of a wheel
// key injects nothing. A button is clicked once, its press and its release in
// the same batch (InjectFencedMixed refuses any other): a second click would
// be a double click if the hook ever let the batch through, and a button has
// no state to restore.
//
// A swallowed CapsLock, NumLock or ScrollLock still flips the lock state
// the system translates input with (measured in M0b: after one swallowed
// NumLock tap an injected sc 0x47 changed from VK_NUMPAD7 to VK_HOME,
// while GetKeyState in a new process still reported the old state). So
// every batch taps its key twice and ends in the state it started in.
func TestInjectKeys(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)

	hkl := injecttest.ForegroundLayout()
	numLock := probeNumLock(t, rec, base+1)
	t.Logf("foreground layout %#x, NumLock on: %v", hkl, numLock)

	var cases []injectCase
	hasMouse := false
	for _, k := range keys.Table() {
		cases = append(cases, injectCase{key: k})
		hasMouse = hasMouse || k.Kind() == keys.KindMouse
	}
	for _, spec := range extraScans {
		cases = append(cases, injectCase{key: keys.MustParse(spec).Key()})
	}
	for i := range cases {
		e, err := output.KeyEventFor(cases[i].key, false)
		if err != nil {
			t.Fatalf("%s: %v", cases[i].key, err)
		}
		cases[i].event = e
	}

	// The nonces: base the keyboard canary, base+1 the NumLock probe, the
	// cases from base+2, and the mouse canary behind them.
	if len(cases) > 0xFFFF-3 {
		t.Fatalf("%d cases do not fit in the nonces of one base", len(cases))
	}
	if hasMouse {
		// Before the first mouse event of the test, and so before the keys.
		rec.MouseCanary(t, base+2+uint32(len(cases)))
	}
	// injecting names the key whose batch is out, for when InjectFenced
	// stops the test.
	var injecting string
	defer func() {
		if injecting != "" {
			t.Logf("stopped at %s", injecting)
		}
	}()
	for i, c := range cases {
		nonce := base + 2 + uint32(i)
		up := c.event
		up.Up = true
		injecting = fmt.Sprintf("%s (%v, nonce %#x)", c.key, c.event, nonce)
		if c.event.Mouse != keys.MouseNone {
			if c.event.Mouse.IsButton() {
				got := rec.InjectFencedMixed(t, nonce, []output.KeyEvent{c.event, up})
				injecting = ""
				checkInjectedButton(t, c.event.Mouse, nonce, 1, got)
				continue
			}
			got := rec.InjectFencedMixed(t, nonce, []output.KeyEvent{c.event, up, c.event, up})
			injecting = ""
			checkInjectedWheel(t, c.event.Mouse, nonce, 2, got)
			continue
		}
		got := rec.InjectFenced(t, nonce, []output.KeyEvent{c.event, up, c.event, up})
		injecting = ""
		checkInjected(t, c, hkl, numLock, nonce, got)
	}
	t.Logf("%d keys injected, each tapped twice (a mouse button is clicked once)", len(cases))
}

// captureSender is a Sender that injects nothing and keeps each batch.
type captureSender struct{ batches [][]output.KeyEvent }

func (s *captureSender) Send(events []output.KeyEvent) (int, error) {
	s.batches = append(s.batches, slices.Clone(events))
	return len(events), nil
}

// TestInjectMenuMask (OUT-08) injects what a MenuMask sends for a press
// and a release of LAlt, and checks what a low-level hook sees: LAlt as
// TestInjectKeys checks it, and the mask key with vk 0xE8, scan code 0
// and no E0 flag; each with LLKHF_INJECTED only and the tag. Every event
// is swallowed by the hook, so nothing reaches the foreground
// application.
//
// The MenuMask's two batches, [+LAlt] and [+vk:0xE8 -vk:0xE8 -LAlt], go
// out as one fenced batch, which leaves no key held however much of it
// SendInput injects (InjectFenced).
func TestInjectMenuMask(t *testing.T) {
	injecttest.Require(t)
	var s captureSender
	m := output.NewMenuMask(&s)
	for _, batch := range []string{"+LAlt", "-LAlt"} {
		if n, err := m.Send(maskEvents(t, batch)); n != 1 || err != nil {
			t.Fatalf("Send(%s) = %d, %v, want 1, nil", batch, n, err)
		}
	}
	events := slices.Concat(s.batches...)
	if got, want := eventsTrace(events), "+LAlt +vk:0xE8 -vk:0xE8 -LAlt"; got != want {
		t.Fatalf("the MenuMask sent %s, want %s", got, want)
	}

	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	hkl := injecttest.ForegroundLayout()
	nonce := base + 1
	got := rec.InjectFenced(t, nonce, events)

	lalt := keys.MustParse("LAlt").Key()
	var problems []string
	for i, ev := range got {
		e := events[i]
		t.Logf("#%d %s: vk=0x%02X sc=0x%02X flags=0x%02X %s altDown=%v",
			i, eventsTrace(events[i:i+1]), ev.VK, ev.Scan, ev.Flags, winhook.MsgName(ev.Msg), ev.AltDown())
		wantVK := uint32(e.VK)
		if !e.ByVK {
			wantVK = injecttest.ScanVK(e.Scan, e.Ext, hkl)
			// A modifier does not depend on the layout: the hook sees the
			// US VK of the key table.
			if ev.VK != uint32(lalt.US().VK) {
				problems = append(problems, fmt.Sprintf("event %d: vk=0x%02X, want the US VK 0x%02X", i, ev.VK, lalt.US().VK))
			}
		}
		switch {
		case ev.Up() != e.Up:
			problems = append(problems, fmt.Sprintf("event %d: LLKHF_UP=%v", i, ev.Up()))
		case e.Up && ev.Msg != winhook.WMKeyUp && ev.Msg != winhook.WMSysKeyUp,
			!e.Up && ev.Msg != winhook.WMKeyDown && ev.Msg != winhook.WMSysKeyDown:
			problems = append(problems, fmt.Sprintf("event %d: message %s", i, winhook.MsgName(ev.Msg)))
		}
		if ev.VK != wantVK || ev.Scan != uint32(e.Scan) || ev.Ext() != e.Ext {
			problems = append(problems, fmt.Sprintf("event %d: vk=0x%02X sc=0x%02X ext=%v, want vk=0x%02X sc=0x%02X ext=%v",
				i, ev.VK, ev.Scan, ev.Ext(), wantVK, e.Scan, e.Ext))
		}
		if !ev.Injected() || ev.LowerILInjected() {
			problems = append(problems, fmt.Sprintf("event %d: flags 0x%02X, want LLKHF_INJECTED only", i, ev.Flags))
		}
		if ev.Extra != output.ExtraInfo(nonce) || !ev.Swallowed {
			problems = append(problems, fmt.Sprintf("event %d: extra %#x swallowed %v", i, ev.Extra, ev.Swallowed))
		}
	}
	if len(problems) > 0 {
		t.Errorf("foreground layout %#x:\n%s", hkl, strings.Join(problems, "\n"))
	}
}

// probeNumLock reports whether NumLock is on as the system translates
// input: it injects sc 0x47, which the hook swallows, and reads the VK.
// GetKeyState cannot tell: it misses lock taps that a hook swallowed.
func probeNumLock(t *testing.T, rec *injecttest.Recorder, nonce uint32) bool {
	t.Helper()
	down := output.KeyEvent{Scan: 0x47}
	up := down
	up.Up = true
	got := rec.InjectFenced(t, nonce, []output.KeyEvent{down, up})
	switch got[0].VK {
	case numLockOnVK[0x47]:
		return true
	case 0x24: // VK_HOME
		return false
	}
	t.Fatalf("NumLock probe: sc 0x47 gave vk=0x%02X", got[0].VK)
	return false
}

// checkInjected checks the four events of two taps of c, which
// InjectFenced has already counted and found swallowed.
func checkInjected(t *testing.T, c injectCase, hkl windows.Handle, numLock bool, nonce uint32, got []winhook.Event) {
	t.Helper()
	e := c.event
	name := c.key.String()

	wantScan := uint32(e.Scan)
	// The system reports RShift (0x36) as extended although it is not
	// E0-prefixed and was injected without KEYEVENTF_EXTENDEDKEY (M0b).
	wantExt := e.Ext || !e.ByVK && e.Scan == 0x36
	var wantVK uint32
	switch {
	case e.ByVK:
		wantVK = uint32(e.VK)
	case !e.Ext && numLock && numLockOnVK[e.Scan] != 0:
		wantVK = numLockOnVK[e.Scan]
	default:
		wantVK = injecttest.ScanVK(e.Scan, e.Ext, hkl)
	}

	var problems []string
	for i, ev := range got {
		up := i%2 == 1
		switch {
		case ev.Up() != up:
			problems = append(problems, fmt.Sprintf("event %d: LLKHF_UP=%v", i, ev.Up()))
		case up && ev.Msg != winhook.WMKeyUp && ev.Msg != winhook.WMSysKeyUp,
			!up && ev.Msg != winhook.WMKeyDown && ev.Msg != winhook.WMSysKeyDown:
			problems = append(problems, fmt.Sprintf("event %d: message %s", i, winhook.MsgName(ev.Msg)))
		}
		if ev.Scan != wantScan {
			problems = append(problems, fmt.Sprintf("event %d: sc=0x%02X, want 0x%02X", i, ev.Scan, wantScan))
		}
		if ev.Ext() != wantExt {
			problems = append(problems, fmt.Sprintf("event %d: ext=%v, want %v", i, ev.Ext(), wantExt))
		}
		if !ev.Injected() || ev.LowerILInjected() {
			problems = append(problems, fmt.Sprintf("event %d: flags 0x%02X, want LLKHF_INJECTED only", i, ev.Flags))
		}
		if ev.Extra != output.ExtraInfo(nonce) || !ev.Swallowed {
			problems = append(problems, fmt.Sprintf("event %d: extra %#x swallowed %v", i, ev.Extra, ev.Swallowed))
		}
		if ev.VK != wantVK {
			problems = append(problems, fmt.Sprintf("event %d: vk=0x%02X, want 0x%02X", i, ev.VK, wantVK))
		}
		// Named keys and modifiers must not depend on the layout: the hook
		// sees the US VK of the key table.
		kind := c.key.Kind()
		if (kind == keys.KindNamed || kind == keys.KindModifier) && !layoutDependent[name] && ev.VK != uint32(c.key.US().VK) {
			problems = append(problems, fmt.Sprintf("event %d: vk=0x%02X, want the US VK 0x%02X", i, ev.VK, c.key.US().VK))
		}
	}
	if len(problems) > 0 {
		t.Errorf("%s (%v): %s", name, e, strings.Join(problems, "; "))
	}
}

// wheelWant returns the window message and the delta a mouse hook must report
// for one notch of m (ADR-0062): WM_MOUSEWHEEL for up (+120) and down (-120),
// WM_MOUSEHWHEEL for right (+120) and left (-120).
func wheelWant(m keys.Mouse) (msg uint32, delta int) {
	switch m {
	case keys.WheelUp:
		return winhook.WMMouseWheel, 120
	case keys.WheelDown:
		return winhook.WMMouseWheel, -120
	case keys.WheelLeft:
		return winhook.WMMouseHWheel, -120
	case keys.WheelRight:
		return winhook.WMMouseHWheel, 120
	}
	panic("no wheel notch for " + m.String())
}

// checkInjectedWheel checks what the hooks saw of wheel notches of m: exactly
// n events, all mouse events, each with the message and delta of a notch of m,
// LLMHF_INJECTED and not LLMHF_LOWER_IL_INJECTED, the nonce of the batch, and
// swallowed. InjectFencedMixed has already counted them swallowed. The raw
// mouseData is in every message, for the day the system reports a notch
// differently from what the documentation says (the run of 2026-10-19 found
// it did not). A mouse hook gets the low 32 bits of dwExtraInfo only
// (measured 2026-10-19): the tag cannot be checked, and the high half is
// accepted as MayBePadmateMouse has it, with the nonce.
func checkInjectedWheel(t *testing.T, m keys.Mouse, nonce uint32, n int, got []injecttest.Seen) {
	t.Helper()
	wantMsg, wantDelta := wheelWant(m)
	if len(got) != n {
		t.Errorf("%v: the hooks saw %d events, want %d notches: %v", m, len(got), n, got)
		return
	}
	var problems []string
	for i, s := range got {
		e := s.Mouse
		if e == nil {
			problems = append(problems, fmt.Sprintf("event %d is a keyboard event: %v", i, s))
			continue
		}
		if e.Msg != wantMsg || e.WheelDelta() != wantDelta {
			problems = append(problems, fmt.Sprintf("event %d: %s delta %d (mouseData %#x), want %s delta %d",
				i, winhook.MouseMsgName(e.Msg), e.WheelDelta(), e.MouseData, winhook.MouseMsgName(wantMsg), wantDelta))
		}
		if !e.Injected() || e.LowerILInjected() {
			problems = append(problems, fmt.Sprintf("event %d: flags 0x%02X, want LLMHF_INJECTED only", i, e.Flags))
		}
		if !output.MayBePadmateMouse(e.Extra) || output.Nonce(e.Extra) != nonce || !e.Swallowed {
			problems = append(problems, fmt.Sprintf("event %d: extra %#x swallowed %v, want the nonce %#x", i, uint64(e.Extra), e.Swallowed, nonce))
		}
	}
	if len(problems) > 0 {
		t.Errorf("%v: %s", m, strings.Join(problems, "; "))
	}
}

// TestInjectWheelKeys (ADR-0062) injects a notch of each wheel key and checks
// what the mouse hook sees: the message, +-120 in the high word of mouseData,
// LLMHF_INJECTED, the nonce, and that the hook swallowed it. The release of a
// wheel key injects nothing: a press and a release make one notch, a release
// alone makes none, and the keyboard hook sees no event of any of it. The
// mouse hook is proven live by a mouse canary before the first notch, and every
// batch has one behind it (injecttest.InjectFencedMixed).
func TestInjectWheelKeys(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.MouseCanary(t, base+1)
	keyboardSeen := len(rec.Own())

	nonce := base + 2
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		key := keys.MustParse(name).Key()
		down, err := output.KeyEventFor(key, false)
		if err != nil {
			t.Fatal(err)
		}
		up := down
		up.Up = true
		m, _ := key.Mouse()

		// A press and a release: one notch.
		nonce++
		got := rec.InjectFencedMixed(t, nonce, []output.KeyEvent{down, up})
		checkInjectedWheel(t, m, nonce, 1, got)

		// A release alone injects nothing: the hooks see no event but the
		// canary behind it.
		nonce++
		got = rec.InjectFencedMixed(t, nonce, []output.KeyEvent{up})
		checkInjectedWheel(t, m, nonce, 0, got)

		// Presses with no release between them are a notch each: a wheel key
		// is never "held".
		nonce++
		got = rec.InjectFencedMixed(t, nonce, []output.KeyEvent{down, down, up})
		checkInjectedWheel(t, m, nonce, 2, got)
	}
	// Only the keyboard canaries of the mouse-only batches would have shown
	// on the keyboard hook; there are none, since no batch had a keyboard event.
	if n := len(rec.Own()); n != keyboardSeen {
		t.Errorf("the keyboard hook saw %d owned events during the wheel batches, want none", n-keyboardSeen)
	}
}

// TestInjectWheelChord (ADR-0062) injects Ctrl+WheelUp and Ctrl+Shift+WheelLeft
// as the Arbiter sends them, modifiers around the notch, in one SendInput
// call, and checks the order in which the two hooks saw the events: the
// modifier presses, the notch, the modifier releases in reverse. The keyboard
// hook and the mouse hook run on one thread, so one sequence orders their
// events (winhook Seq), and the stamps do not go back.
func TestInjectWheelChord(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.MouseCanary(t, base+1)

	for i, spec := range []string{"Ctrl+WheelUp", "Ctrl+Shift+WheelLeft"} {
		nonce := base + 2 + uint32(i)
		stroke, err := output.ChordStroke(keys.MustParse(spec))
		if err != nil {
			t.Fatal(err)
		}
		events := slices.Concat(stroke.Down, stroke.Up)
		got := rec.InjectFencedMixed(t, nonce, events)

		// What the hooks must see, in order: every event but the silent one.
		var want []output.KeyEvent
		for _, e := range events {
			if !e.Silent() {
				want = append(want, e)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: the hooks saw %d events, want %d: %v", spec, len(got), len(want), got)
		}
		var prevSeq uint64
		var prevStamp uint64
		for j, w := range want {
			s := got[j]
			if s.Seq() <= prevSeq {
				t.Errorf("%s: event %d has Seq %d, not after %d", spec, j, s.Seq(), prevSeq)
			}
			prevSeq = s.Seq()
			switch {
			case w.Mouse != keys.MouseNone:
				wantMsg, wantDelta := wheelWant(w.Mouse)
				if s.Mouse == nil || s.Mouse.Msg != wantMsg || s.Mouse.WheelDelta() != wantDelta {
					t.Errorf("%s: event %d = %v, want the %v notch (%s, %d)", spec, j, s, w.Mouse, winhook.MouseMsgName(wantMsg), wantDelta)
				}
			default:
				// A modifier does not depend on the layout: the hook sees the
				// US VK of the key table.
				if s.Keyboard == nil || s.Keyboard.Scan != uint32(w.Scan) || s.Keyboard.Ext() != w.Ext ||
					s.Keyboard.Up() != w.Up || s.Keyboard.VK != uint32(w.Key.US().VK) {
					t.Errorf("%s: event %d = %v, want %v (vk 0x%02X)", spec, j, s, w, w.Key.US().VK)
				}
			}
			var stamp uint64
			if s.Mouse != nil {
				stamp = uint64(s.Mouse.Stamp)
			} else {
				stamp = uint64(s.Keyboard.Stamp)
			}
			if stamp == 0 || stamp < prevStamp {
				t.Errorf("%s: event %d has stamp %d after %d", spec, j, stamp, prevStamp)
			}
			prevStamp = stamp
		}
	}
}
