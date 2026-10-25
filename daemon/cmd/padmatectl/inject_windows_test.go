package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
)

// wantEvent is one expected event of padmatectl.
type wantEvent struct {
	up   bool
	scan uint32
	vk   uint32
}

// TestSendRecordedByProbe runs "padmatectl send" with "padmate-probe
// record" swallowing its events and checks what the hook saw (OUT-01,
// ENG-06).
//
// A backstop hook in the test process swallows every padmate-tagged event
// and records it. It is installed before the probe, so the probe's hook
// runs first and the backstop only sees what the probe let through, which
// must be nothing. After each send the test injects a canary, which the
// probe must swallow too: the hook sees events in the order they were
// injected, so once the probe has reported that canary it has reported
// every event of the send.
func TestSendRecordedByProbe(t *testing.T) {
	injecttest.Require(t)
	ctl := injecttest.BuildTool(t, ".")
	probePath := injecttest.BuildTool(t, "../padmate-probe")

	base := injecttest.NonceBase() | 0x80000000 // far from any process id
	// The backstop is the only hook of this test so far: it must swallow
	// its canary before the probe starts. Once the probe has stopped, it
	// checks that nothing got past the probe.
	back := injecttest.StartBackstop(t)
	back.Canary(t, base+1)

	// The probe's canaries: the backstop must not see them.
	p := injecttest.StartProbe(t, probePath, "--duration", "2m")
	p.Canary(t, base+2)

	zVK := injecttest.ScanVK(0x2C, false, injecttest.ForegroundLayout())
	for i, tc := range []struct {
		spec string
		want []wantEvent
	}{
		{"Ctrl+Shift+Z", []wantEvent{
			{false, 0x1D, 0xA2}, {false, 0x2A, 0xA0}, {false, 0x2C, zVK},
			{true, 0x2C, zVK}, {true, 0x2A, 0xA0}, {true, 0x1D, 0xA2},
		}},
		{"F13", []wantEvent{{false, 0x64, 0x7C}, {true, 0x64, 0x7C}}},
	} {
		cmd := exec.Command(ctl, "send", tc.spec)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		// The fence goes out whatever the result: it tells whether the
		// probe is still swallowing, and when it has reported the send.
		p.Canary(t, base+3+uint32(i))
		if err != nil || stdout.Len() > 0 || stderr.Len() > 0 {
			t.Fatalf("padmatectl send %s: %v, stdout %q, stderr %q", tc.spec, err, stdout.String(), stderr.String())
		}
		nonce := uint32(cmd.Process.Pid)
		got := p.WithNonce(nonce)
		for i, e := range got {
			t.Logf("%s #%d: %v", tc.spec, i, e)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s: the probe saw %d events with nonce %d, want %d", tc.spec, len(got), nonce, len(tc.want))
		}
		for i, w := range tc.want {
			e := got[i]
			wantMsg := "WM_KEYDOWN"
			if w.up {
				wantMsg = "WM_KEYUP"
			}
			if e.Up != w.up || e.Scan != w.scan || e.Ext || e.VK != w.vk || e.Msg != wantMsg || !e.Injected || !e.Swallowed {
				t.Errorf("%s #%d = %v, want up=%v sc=0x%02X ext=false vk=0x%02X %s injected swallowed",
					tc.spec, i, e, w.up, w.scan, w.vk, wantMsg)
			}
		}
	}

	for _, line := range p.Stderr() {
		t.Errorf("probe stderr: %s", line)
	}
}
