package output_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// TestInjectUnicodeText (OUT-21, ADR-0070) injects what TypeText sends for a short
// text, and checks what a low-level hook sees: every Unicode event as VK_PACKET
// with the UTF-16 unit in its scan code, the right message, LLKHF_INJECTED, the tag
// and the nonce, and swallowed by the hook, so that nothing is typed into the
// foreground window; and the Enter tap of the newline as the Enter key.
//
// The text is "a", a character of the Basic Multilingual Plane ("你"), a pair of
// surrogates (U+1F600) and a newline, which covers a one-unit character, a
// two-unit character with both of its units back to back, and the tap.
//
// An Arbiter on a fake clock makes the batches, as it does in the daemon, with a
// Sender that injects nothing; the test then injects each batch as one fenced
// SendInput call (injecttest.InjectFenced): a canary behind it says exactly which
// events the hook saw, and proves the hook still live for the next batch. Before
// any press, injecttest's UnicodeCanary injects the release of a unit, which no
// application turns into a character, and stops the test unless the hook saw that
// release with the tag and swallowed it: it is how the test learns that the hook
// is handed the tag of an event typed by KEYEVENTF_UNICODE at all.
func TestInjectUnicodeText(t *testing.T) {
	injecttest.Require(t)

	// The batches TypeText sends for the text.
	const text = "a你😀\nb"
	clk := clock.NewFake()
	var s captureSender
	a := output.NewArbiter(clk, &s, output.ArbiterOptions{})
	owner := output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1}
	if err := a.TypeText(owner, text, 0); err != nil {
		t.Fatal(err)
	}
	// 5 characters, one batch each: a, 你, the pair, the Enter tap, b.
	if len(s.batches) != 5 {
		t.Fatalf("TypeText sent %d batches, want 5: %v", len(s.batches), s.batches)
	}
	var units int
	for _, batch := range s.batches {
		for _, e := range batch {
			if e.Unicode && !e.Up {
				units++
			}
		}
	}
	if units != 5 { // a, 你, the two units of the pair, b
		t.Fatalf("the batches have %d Unicode presses, want 5", units)
	}

	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.UnicodeCanary(t, base+1)

	var problems []string
	for i, batch := range s.batches {
		nonce := base + 2 + uint32(i)
		got := rec.InjectFenced(t, nonce, batch)
		for j, ev := range got {
			e := batch[j]
			if e.Unicode {
				for _, p := range injecttest.UnicodeProblems(ev, e, nonce) {
					problems = append(problems, fmt.Sprintf("batch %d event %d (%v): %s", i, j, e, p))
				}
				continue
			}
			// The Enter tap of the newline: the key, by scan code, as any key.
			if ev.VK != 0x0D || ev.Scan != uint32(e.Scan) || ev.Ext() != e.Ext || ev.Up() != e.Up ||
				!ev.Injected() || ev.Extra != output.ExtraInfo(nonce) || !ev.Swallowed || !isKeyMessage(ev, e.Up) {
				problems = append(problems, fmt.Sprintf("batch %d event %d (%v): vk=0x%02X sc=0x%02X flags=0x%02X %s extra=%#x swallowed=%v",
					i, j, e, ev.VK, ev.Scan, ev.Flags, winhook.MsgName(ev.Msg), uint64(ev.Extra), ev.Swallowed))
			}
		}
		t.Logf("batch %d: %d events, the hook saw %d", i, len(batch), len(got))
	}
	if len(problems) > 0 {
		t.Errorf("what the hook saw is not what was injected:\n%s", strings.Join(problems, "\n"))
	}
}

// isKeyMessage reports whether ev is the WM_KEYDOWN or WM_KEYUP (or the SYS
// variant) that a press or a release gives.
func isKeyMessage(ev winhook.Event, up bool) bool {
	if up {
		return ev.Msg == winhook.WMKeyUp || ev.Msg == winhook.WMSysKeyUp
	}
	return ev.Msg == winhook.WMKeyDown || ev.Msg == winhook.WMSysKeyDown
}
