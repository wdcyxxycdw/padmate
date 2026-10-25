package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestLiveEmergencyRelease: startLive creates the event of padmatectl
// release-all before it returns (DMN-20), so a Trigger right after it,
// with no gamepad yet, reaches padmated; a later one releases the key
// held. The event is the test's own (testConfig), not the real one.
func TestLiveEmergencyRelease(t *testing.T) {
	s := newGateSender("")
	tl, src := sessionStart(t, nil, func() (output.Sender, error) { return s, nil }, "--inject")
	name := tl.cfg.emergencyName
	if name == "" || name == emergency.Name {
		t.Fatalf("emergencyName = %q, want a name of the test's own", name)
	}
	if err := emergency.Trigger(name); err != nil {
		t.Fatalf("Trigger right after startLive: %v", err)
	}
	tl.waitFor(t, tl.log, `"msg":"emergency release requested"`)
	tl.addPad(t, src)
	button(src, "east", true)
	tl.waitSent(t, s, "+Space")
	if err := emergency.Trigger(name); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	tl.waitSent(t, s, "-Space")
	tl.waitForCount(t, tl.log, `"msg":"emergency release requested"`, 2)
	if got, want := s.edges(), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// TestLiveEmergencyDryRun: the dry run does not listen for padmatectl
// release-all. The event is auto-reset, so a dry run would take the
// signal meant for an --inject padmated, and it holds no key to release.
func TestLiveEmergencyDryRun(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run")
	if err := emergency.Trigger(tl.cfg.emergencyName); err == nil {
		t.Error("Trigger found the event of a dry run, want no listener")
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if strings.Contains(tl.log.String(), "emergency release") || strings.Contains(tl.stderr.String(), "release-all") {
		t.Errorf("the dry run mentions release-all:\nstderr:\n%s\nlog:\n%s", tl.stderr, tl.log)
	}
}

// TestLiveEmergencyOff: when the event cannot be created, padmated warns
// and maps as ever.
func TestLiveEmergencyOff(t *testing.T) {
	s := newGateSender("")
	tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.emergencyName = "bad\x00name" },
		func() (output.Sender, error) { return s, nil }, "--inject")
	if want := "padmated: padmatectl release-all off: "; !strings.Contains(tl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, tl.stderr)
	}
	if line := logLine(tl.log.String(), "emergency release off"); !strings.Contains(line, `"level":"WARN"`) {
		t.Errorf("emergency release off: %q, want a warning", line)
	}
	button(src, "east", true)
	tl.waitSent(t, s, "+Space")
	button(src, "east", false)
	tl.waitSent(t, s, "-Space")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}
}
