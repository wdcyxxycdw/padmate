package injecttest

import (
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

const unicodeTestBase = 0x5543_0000 // "UC"

// unicodeEvent is an injected padmate Unicode event with nonce, as a keyboard
// hook sees it: VK_PACKET with the unit in the scan code.
func unicodeEvent(seq uint64, nonce uint32, unit uint16, up, swallowed bool) winhook.Event {
	e := keyEvent(seq, nonce, VKPacket, up, swallowed)
	e.Scan = uint32(unit)
	if up {
		e.Msg = winhook.WMKeyUp
	} else {
		e.Msg = winhook.WMKeyDown
	}
	return e
}

// The Unicode canary is the release of a unit that is not a character: nothing
// an application would type if the hook let it through, and the one thing a test
// needs to prove that the hook is handed the tag of a Unicode event (OUT-21).
func TestUnicodeCanaryEvent(t *testing.T) {
	c := UnicodeCanaryEvent()
	if !c.Unicode || !c.Up || c.Unit != UnicodeCanaryUnit || c.Key.IsValid() || c.Mouse != 0 || c.ByVK {
		t.Fatalf("UnicodeCanaryEvent() = %+v, want the release of U+FFFF", c)
	}
	if msg := unpairedUnicode([]output.KeyEvent{c}); msg != "" {
		t.Errorf("the canary is refused as unpaired: %s", msg)
	}
	const nonce = unicodeTestBase | 7
	for _, tt := range []struct {
		name string
		e    winhook.Event
		want bool
	}{
		{"the canary", unicodeEvent(1, nonce, UnicodeCanaryUnit, true, true), true},
		{"a press of the unit", unicodeEvent(1, nonce, UnicodeCanaryUnit, false, true), false},
		{"another unit", unicodeEvent(1, nonce, 'A', true, true), false},
		{"another nonce", unicodeEvent(1, nonce+1, UnicodeCanaryUnit, true, true), false},
		{"not tagged", func() winhook.Event {
			e := unicodeEvent(1, nonce, UnicodeCanaryUnit, true, true)
			e.Extra = uintptr(nonce)
			return e
		}(), false},
		{"the key canary", keyEvent(1, nonce, CanaryVK, true, true), false},
	} {
		if got := IsUnicodeCanary(tt.e, nonce); got != tt.want {
			t.Errorf("IsUnicodeCanary(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
	if !IsUnicode(unicodeEvent(1, nonce, 'A', false, true)) || IsUnicode(keyEvent(1, nonce, CanaryVK, true, true)) {
		t.Error("IsUnicode: VK_PACKET is a Unicode event and the canary key is not")
	}
	if VKPacket != 0xE7 {
		t.Errorf("VKPacket = %#x, want 0xE7 (VK_PACKET in WinUser.h)", VKPacket)
	}
}

// UnicodeProblems names what a hook's report of a Unicode event gets wrong.
func TestUnicodeProblems(t *testing.T) {
	const nonce = unicodeTestBase | 9
	press := output.UnicodeEvent(0x4F60, false)
	release := output.UnicodeEvent(0x4F60, true)
	good := unicodeEvent(1, nonce, 0x4F60, false, true)
	goodUp := unicodeEvent(1, nonce, 0x4F60, true, true)
	if p := UnicodeProblems(good, press, nonce); p != nil {
		t.Errorf("a good press has problems %q", p)
	}
	if p := UnicodeProblems(goodUp, release, nonce); p != nil {
		t.Errorf("a good release has problems %q", p)
	}
	sys := good
	sys.Msg = winhook.WMSysKeyDown
	if p := UnicodeProblems(sys, press, nonce); p != nil {
		t.Errorf("a press with Alt held (WM_SYSKEYDOWN) has problems %q", p)
	}
	mod := func(f func(*winhook.Event)) winhook.Event { e := good; f(&e); return e }
	for _, tt := range []struct {
		name string
		got  winhook.Event
		e    output.KeyEvent
		want string
	}{
		{"wrong VK", mod(func(e *winhook.Event) { e.VK = 0x41 }), press, "want VK_PACKET"},
		{"wrong unit", mod(func(e *winhook.Event) { e.Scan = 0x4F61 }), press, "want the unit 0x4F60"},
		{"unit lost", mod(func(e *winhook.Event) { e.Scan = 0 }), press, "want the unit 0x4F60"},
		{"a press for a release", good, release, "LLKHF_UP=false, want true"},
		{"a release for a press", goodUp, press, "LLKHF_UP=true, want false"},
		{"wrong message", mod(func(e *winhook.Event) { e.Msg = winhook.WMKeyUp }), press, "message WM_KEYUP, want WM_KEYDOWN"},
		{"not injected", mod(func(e *winhook.Event) { e.Flags = 0 }), press, "want LLKHF_INJECTED only"},
		{"lower IL", mod(func(e *winhook.Event) { e.Flags |= winhook.FlagLowerILInjected }), press, "want LLKHF_INJECTED only"},
		{"no tag", mod(func(e *winhook.Event) { e.Extra = uintptr(nonce) }), press, "want 0x"},
		{"other nonce", mod(func(e *winhook.Event) { e.Extra = output.ExtraInfo(nonce + 1) }), press, "want 0x"},
		{"not swallowed", mod(func(e *winhook.Event) { e.Swallowed = false }), press, "not swallowed"},
		{"not a Unicode event", good, output.KeyEvent{Scan: 0x1E}, "is not a Unicode event"},
	} {
		p := UnicodeProblems(tt.got, tt.e, nonce)
		if len(p) == 0 || !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, tt.want) }) {
			t.Errorf("%s: problems %q, want one with %q", tt.name, p, tt.want)
		}
	}
}

// A batch the Recorder injects must press each unit and release it, in that
// order, in the same batch; releases alone are fine (the canary is one).
func TestUnpairedUnicode(t *testing.T) {
	u := output.UnicodeEvent
	key := func(up bool) output.KeyEvent { return output.KeyEvent{Scan: 0x1E, Up: up} }
	for _, tt := range []struct {
		name   string
		events []output.KeyEvent
		want   string
	}{
		{"none", nil, ""},
		{"keys only", []output.KeyEvent{key(false), key(true)}, ""},
		{"a character", []output.KeyEvent{u('a', false), u('a', true)}, ""},
		{"a pair", []output.KeyEvent{u(0xD83D, false), u(0xD83D, true), u(0xDE00, false), u(0xDE00, true)}, ""},
		{"nested units", []output.KeyEvent{u('a', false), u('b', false), u('b', true), u('a', true)}, ""},
		{"same unit twice in turn", []output.KeyEvent{u('a', false), u('a', true), u('a', false), u('a', true)}, ""},
		{"with keys between", []output.KeyEvent{key(false), u('a', false), u('a', true), key(true)}, ""},
		{"a release alone", []output.KeyEvent{u('a', true)}, ""},
		{"the canary", []output.KeyEvent{UnicodeCanaryEvent()}, ""},
		{"a press alone", []output.KeyEvent{u('a', false)}, "pressed and not released"},
		{"the last release missing", []output.KeyEvent{u('a', false), u('a', true), u('b', false)}, "unit 0x0062 is pressed and not released"},
		{"pressed twice", []output.KeyEvent{u('a', false), u('a', false), u('a', true)}, "event 1 presses unit 0x0061 twice"},
	} {
		got := unpairedUnicode(tt.events)
		if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("%s: unpairedUnicode = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// With the hook proven live, a Unicode canary is swallowed and the batch comes
// back with the canary's own report; the helpers refuse a lone press before any
// SendInput. The press that a regressed guard would let through is swallowed by
// the Recorder's hook all the same, which is why this test is gated.
func TestUnicodeCanaryAndRefusals(t *testing.T) {
	Require(t)
	base := NonceBase()
	rec := Start(t, base)
	rec.Canary(t, base)
	if rec.UnicodeLive() {
		t.Fatal("UnicodeLive() before any Unicode canary")
	}
	rec.UnicodeCanary(t, base+1)
	if !rec.UnicodeLive() {
		t.Fatal("UnicodeLive() = false after a Unicode canary the hook swallowed")
	}

	f := &fakeTB{TB: t}
	press := output.UnicodeEvent(UnicodeCanaryUnit, false)
	failures := f.run(func() { rec.InjectFenced(f, base+2, []output.KeyEvent{press}) })
	if len(failures) != 1 || !strings.Contains(failures[0], "pressed and not released") {
		t.Errorf("InjectFenced of a lone press: failures %q, want one about the unpaired unit", failures)
	}
	f = &fakeTB{TB: t}
	failures = f.run(func() { rec.InjectFencedMixed(f, base+3, []output.KeyEvent{press}) })
	if len(failures) != 1 || !strings.Contains(failures[0], "pressed and released in the same batch") {
		t.Errorf("InjectFencedMixed of a lone press: failures %q, want one about the unpaired unit", failures)
	}
	// A pair is fine, and the report of both events is as UnicodeProblems wants.
	got := rec.InjectFenced(t, base+4, []output.KeyEvent{press, output.UnicodeEvent(UnicodeCanaryUnit, true)})
	if len(got) != 2 {
		t.Fatalf("the hook saw %d events, want 2", len(got))
	}
	for i, ev := range got {
		if p := UnicodeProblems(ev, output.UnicodeEvent(UnicodeCanaryUnit, i == 1), base+4); p != nil {
			t.Errorf("event %d: %q", i, p)
		}
	}
}

// A batch with a Unicode press is refused until a Unicode canary has proven the
// hook live, as a batch with a mouse event is until a mouse canary has: it is
// only a convention otherwise, and a test that forgot UnicodeCanary would type
// characters into the foreground window if Windows did not hand the hook the tag
// of a Unicode event. The canary itself, a release, and keys are not held up.
func TestUnicodeRefusal(t *testing.T) {
	u := output.UnicodeEvent
	key, keyUp := output.KeyEvent{Scan: 0x1E}, output.KeyEvent{Scan: 0x1E, Up: true}
	for _, tt := range []struct {
		name   string
		events []output.KeyEvent
		live   bool
		refuse bool
	}{
		{"no events", nil, false, false},
		{"keys only", []output.KeyEvent{key, keyUp}, false, false},
		{"a character", []output.KeyEvent{u('a', false), u('a', true)}, false, true},
		{"a character after the canary", []output.KeyEvent{u('a', false), u('a', true)}, true, false},
		{"a pair", []output.KeyEvent{u(0xD83D, false), u(0xD83D, true), u(0xDE00, false), u(0xDE00, true)}, false, true},
		{"a character among keys", []output.KeyEvent{key, u('a', false), u('a', true), keyUp}, false, true},
		{"the canary", []output.KeyEvent{UnicodeCanaryEvent()}, false, false},
		{"a release alone", []output.KeyEvent{u('a', true)}, false, false},
	} {
		r := newRecorder(func(uint32) bool { return true })
		r.unicodeLive = tt.live
		got := r.unicodeRefusal(tt.events)
		if (got != "") != tt.refuse {
			t.Errorf("%s: unicodeRefusal = %q, want refused %v", tt.name, got, tt.refuse)
		}
		if tt.refuse && !strings.Contains(got, "UnicodeCanary") {
			t.Errorf("%s: the refusal %q does not name UnicodeCanary", tt.name, got)
		}
		if r.UnicodeLive() != tt.live {
			t.Errorf("%s: UnicodeLive() = %v, want %v", tt.name, r.UnicodeLive(), tt.live)
		}
	}
}

// Both helpers stop at the refusal, before any SendInput, when no Unicode canary
// has proven the hook live. The recorder here has no hook and has had no canary;
// the pair is of U+FFFF, which is no character, so that a regressed check would
// type nothing anyone sees. After the canary the same batch is not refused for
// that reason (the check is only unicodeRefusal, which TestUnicodeRefusal runs).
func TestUnicodeRefusalStopsInjection(t *testing.T) {
	pair := []output.KeyEvent{output.UnicodeEvent(UnicodeCanaryUnit, false), output.UnicodeEvent(UnicodeCanaryUnit, true)}
	const nonce = unicodeTestBase | 11
	for name, inject := range map[string]func(f *fakeTB, r *Recorder){
		"InjectFenced":      func(f *fakeTB, r *Recorder) { r.InjectFenced(f, nonce, pair) },
		"InjectFencedMixed": func(f *fakeTB, r *Recorder) { r.InjectFencedMixed(f, nonce, pair) },
	} {
		f := &fakeTB{TB: t}
		r := newRecorder(func(n uint32) bool { return n == nonce })
		failures := f.run(func() { inject(f, r) })
		if len(failures) != 1 || !strings.Contains(failures[0], "call UnicodeCanary first") || !strings.Contains(failures[0], name) {
			t.Errorf("%s: failures %q, want one from %s that names UnicodeCanary", name, failures, name)
		}
		if n := r.OwnCount(); n != 0 {
			t.Errorf("%s: the recorder saw %d events: something was injected", name, n)
		}
	}
}
