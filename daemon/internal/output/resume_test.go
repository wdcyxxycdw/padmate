package output_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func TestResumeGateIsIndependentAndReleasesHeldKeys(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	lt.l.BlockFor(output.BlockResume, true)
	lt.do(blocked(true))
	if got := lt.rec.Trace(); got != "+Space@0 -Space@0" {
		t.Fatalf("resume did not release held Space: %s", got)
	}
	lt.l.BlockFor(output.BlockSystem|output.BlockUser, true)
	lt.l.BlockFor(output.BlockResume, false)
	blocking(t, lt.l, output.BlockSystem|output.BlockUser)
	lt.do(blocked(true))
	lt.l.BlockFor(output.BlockUser, false)
	blocking(t, lt.l, output.BlockSystem)
	lt.l.BlockFor(output.BlockSystem, false)
	lt.do(blocked(false))
}

// TestExitGateStaysClosed: the gate a tray exit closes (DMN-22) lets go
// of every key at once, and no other owner's open reopens it.
func TestExitGateStaysClosed(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.do(down(ownerA, "Space"))
	lt.l.BlockFor(output.BlockExit, true)
	lt.do(blocked(true))
	if got := lt.rec.Trace(); got != "+Space@0 -Space@0" {
		t.Fatalf("exit did not release held Space: %s", got)
	}
	lt.l.BlockFor(output.BlockUser, true)
	lt.l.BlockFor(output.BlockUser|output.BlockSystem|output.BlockEmergency|output.BlockResume|output.BlockCapture|output.BlockOwnWindow, false)
	blocking(t, lt.l, output.BlockExit)
	lt.do(blocked(true))
}
