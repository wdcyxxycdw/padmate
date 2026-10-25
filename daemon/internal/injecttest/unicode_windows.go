package injecttest

import (
	"fmt"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// Text input (OUT-21, ADR-0070) is typed with KEYEVENTF_UNICODE. The system
// turns such an event into a key whose VK is VK_PACKET (0xE7) and hands the
// foreground thread the character, so a Unicode event the hook let through is a
// character typed into whatever window is in front. The Recorder's predicate
// swallows an event by the tag and the nonce in dwExtraInfo alone, VK_PACKET
// events included, but that the hook is handed the tag of a Unicode event at all
// is a fact of Windows that the first test to inject one proves, not assumes:
// UnicodeCanary sends a release of a unit, which no application turns into a
// character, and stops the test unless the hook saw it with the tag and swallowed
// it. Only then does a test inject a press.

// VKPacket is the VK the hook sees for an event injected with KEYEVENTF_UNICODE.
const VKPacket = 0xE7

// UnicodeCanaryUnit is the code unit of the Unicode canary: U+FFFF, which is no
// character. The canary is its release.
const UnicodeCanaryUnit = 0xFFFF

// UnicodeCanaryEvent is the event a Unicode canary injects: the release of a
// unit that is not down. A release is never turned into a character, so the
// event does no harm to the foreground window if the hook lets it through.
func UnicodeCanaryEvent() output.KeyEvent {
	return output.UnicodeEvent(UnicodeCanaryUnit, true)
}

// IsUnicode reports whether e, as the hook saw it, is a Unicode event (VK_PACKET).
func IsUnicode(e winhook.Event) bool { return e.VK == VKPacket }

// IsUnicodeCanary reports whether e is the Unicode canary with nonce.
func IsUnicodeCanary(e winhook.Event, nonce uint32) bool {
	return IsUnicode(e) && e.Scan == UnicodeCanaryUnit && e.Up() && output.IsPadmate(e.Extra) && output.Nonce(e.Extra) == nonce
}

// UnicodeLive reports whether a Unicode canary has proven that the hook is
// handed the tag of a Unicode event (UnicodeCanary).
func (r *Recorder) UnicodeLive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.unicodeLive
}

// unicodeRefusal returns why InjectFenced and InjectFencedMixed must not inject
// events yet, or "" when they may: events press a Unicode unit and no
// UnicodeCanary has proven the hook live. As with a mouse event and MouseCanary,
// the order is enforced, not a convention: a press the hook let through is a
// character typed into the foreground window. A release is never typed, so the
// canary itself, and a batch of releases, are not held up.
func (r *Recorder) unicodeRefusal(events []output.KeyEvent) string {
	if !slices.ContainsFunc(events, func(e output.KeyEvent) bool { return e.Unicode && !e.Up }) || r.UnicodeLive() {
		return ""
	}
	return "a batch with a typed Unicode press, and no Unicode canary has proven that the hook is handed the tag of a Unicode event; call UnicodeCanary first"
}

// UnicodeCanary injects the Unicode canary with nonce, which the recorder must
// own, fenced like any batch (InjectFenced), and stops the test unless the hook
// saw it as a VK_PACKET release with the tag and the nonce, and swallowed it. Call
// Canary first, and this before injecting any Unicode press: InjectFenced and
// InjectFencedMixed refuse a batch with one until it has succeeded. If the system did
// not hand the hook the tag of a Unicode event, InjectFenced stops the test with
// "saw 0 events", since the hook counts only the events it owns, and the canary
// a release only: nothing was typed.
func (r *Recorder) UnicodeCanary(t testing.TB, nonce uint32) {
	t.Helper()
	got := r.InjectFenced(t, nonce, []output.KeyEvent{UnicodeCanaryEvent()})
	if !IsUnicodeCanary(got[0], nonce) || !got[0].Swallowed {
		t.Fatalf("the hook saw the Unicode canary as vk=0x%02X sc=0x%04X flags=0x%02X extra=%#x swallowed=%v; injecting nothing else",
			got[0].VK, got[0].Scan, got[0].Flags, uint64(got[0].Extra), got[0].Swallowed)
	}
	r.mu.Lock()
	r.unicodeLive = true
	r.mu.Unlock()
}

// UnicodeProblems returns what is wrong with got, the hook's report of the
// injection of e, a Unicode event, by a test with nonce, or nil when nothing is.
// got must be VK_PACKET with the code unit in its scan code and the message of
// a press (WM_KEYDOWN) or a release (WM_KEYUP), injected and not
// LLKHF_LOWER_IL_INJECTED, carry the tag and the nonce, and have been swallowed.
// Nothing else of a Unicode event is checked: that the hook is handed the unit in
// the scan code is what the documentation says and the first run of the test
// measures.
func UnicodeProblems(got winhook.Event, e output.KeyEvent, nonce uint32) []string {
	var problems []string
	if !e.Unicode {
		return []string{fmt.Sprintf("%v is not a Unicode event", e)}
	}
	if got.VK != VKPacket {
		problems = append(problems, fmt.Sprintf("vk=0x%02X, want VK_PACKET 0x%02X", got.VK, VKPacket))
	}
	if got.Scan != uint32(e.Unit) {
		problems = append(problems, fmt.Sprintf("sc=0x%X, want the unit 0x%04X", got.Scan, e.Unit))
	}
	if got.Up() != e.Up {
		problems = append(problems, fmt.Sprintf("LLKHF_UP=%v, want %v", got.Up(), e.Up))
	}
	wantMsg := uint32(winhook.WMKeyDown)
	if e.Up {
		wantMsg = winhook.WMKeyUp
	}
	if got.Msg != wantMsg && got.Msg != wantMsg+4 { // WM_SYSKEYDOWN, WM_SYSKEYUP with Alt held
		problems = append(problems, fmt.Sprintf("message %s, want %s", winhook.MsgName(got.Msg), winhook.MsgName(wantMsg)))
	}
	if !got.Injected() || got.LowerILInjected() {
		problems = append(problems, fmt.Sprintf("flags 0x%02X, want LLKHF_INJECTED only", got.Flags))
	}
	if got.Extra != output.ExtraInfo(nonce) {
		problems = append(problems, fmt.Sprintf("extra %#x, want %#x", uint64(got.Extra), uint64(output.ExtraInfo(nonce))))
	}
	if !got.Swallowed {
		problems = append(problems, "not swallowed")
	}
	return problems
}

// unpairedUnicode returns what is wrong with the Unicode events of events when
// each unit is not pressed and then released, in that order, or "" when nothing
// is. A batch the Recorder injects must leave nothing typed half way: a press
// without its release in the batch is a key the application may think is held
// if the hook ever lets it through, and a character it would then type.
// Releases alone are allowed: the Unicode canary is one.
func unpairedUnicode(events []output.KeyEvent) string {
	down := map[uint16]bool{}
	for i, e := range events {
		if !e.Unicode {
			continue
		}
		switch {
		case !e.Up && down[e.Unit]:
			return fmt.Sprintf("event %d presses unit 0x%04X twice", i, e.Unit)
		case !e.Up:
			down[e.Unit] = true
		default:
			delete(down, e.Unit)
		}
	}
	for unit := range down {
		return fmt.Sprintf("unit 0x%04X is pressed and not released in the batch", unit)
	}
	return ""
}
