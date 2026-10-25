package simtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

func TestLoadScriptDrivesFakeClockAndOutput(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `
import { on, key, sleep } from "padmate";
on("south", "press", async () => {
  key.down("Z");
  await sleep(10);
  key.up("Z");
});
`)

	if err := r.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+Z@0")
	s.Advance(9 * time.Millisecond)
	s.ExpectOutput("")
	s.Advance(time.Millisecond)
	s.ExpectOutput("-Z@10")
}

func TestLoadScriptSettleDrainsZeroHoldTapContinuation(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `
import { on, key } from "padmate";
on("south", "press", async () => {
  await key.tap("Z", { holdMs: 0 });
  key.tap("X", { holdMs: 0 });
});
`)
	if err := r.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+Z@0 -Z@0")
	s.Settle()
	s.ExpectOutput("+X@0 -X@0")
}

func TestLoadScriptAdvanceDrainsTapArrayAndAwaitContinuation(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `
import { on, key } from "padmate";
on("south", "press", async () => {
  await key.tap(["Z", "X"], { holdMs: 5 });
  key.tap("Y", { holdMs: 5 });
});
`)
	if err := r.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+Z@0")
	s.Advance(5 * time.Millisecond)
	s.ExpectOutput("-Z@5 +X@5")
	s.Advance(5 * time.Millisecond)
	s.ExpectOutput("-X@10 +Y@10")
	s.Settle()
	s.ExpectOutput("-Y@15")
}

func TestLoadScriptExposesFormattedLogs(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `import { host, log, on } from "padmate";
if (host.mode !== "test") throw new Error("simtest host mode");
log.info("setup", 1);
on("south", "press", () => log.warn("press", 2));`)
	if err := r.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	got, dropped := r.DrainLogs()
	if dropped != 0 || len(got) != 2 || got[0].Message != "setup 1" || got[1].Message != "press 2" {
		t.Fatalf("simtest logs = %+v, dropped %d", got, dropped)
	}
}

func TestLoadScriptRunsOnActivateOnFakeClock(t *testing.T) {
	s := simtest.New(t)
	simtest.LoadScript(t, s, `
import { defineScript, key, sleep } from "padmate";
export default defineScript({
  async onActivate() {
    key.down("A");
    await sleep(3);
    key.up("A");
  }
});
`)
	s.ExpectOutput("+A@0")
	s.Advance(3 * time.Millisecond)
	s.ExpectOutput("-A@3")
}

func TestLoadScriptReplaysInputTrace(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `
import { on, key } from "padmate";
on("south", "press", () => key.tap("X", { holdMs: 5 }));
`)

	if err := r.Replay("+south@0 -south@20"); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+X@0 -X@5")
	if got := s.Clock.Now(); got != clock.Time(20*time.Millisecond) {
		t.Fatalf("clock = %v, want 20ms", got)
	}
}

func TestLoadScriptDisposesHeldKeys(t *testing.T) {
	s := simtest.New(t)
	r := simtest.LoadScript(t, s, `
import { on, key } from "padmate";
on("south", "press", () => key.down("Ctrl+Z"));
`)

	if err := r.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+LCtrl@0 +Z@0")
	// The helper's cleanup must dispose the host before Sim's cleanup checks
	// the Arbiter and Recorder, even when the script forgot key.up().
}

func TestAdvanceReportsScriptTimerError(t *testing.T) {
	s := simtest.New(t)
	simtest.LoadScript(t, s, `
import { every } from "padmate";
every(1, () => { throw new Error("timer failed"); });
`)
	s.Advance(time.Millisecond)
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "timer failed") {
		t.Fatalf("Errors() = %v, want one script timer error", errs)
	}
}
