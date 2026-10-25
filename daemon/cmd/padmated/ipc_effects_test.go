package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// scriptEffect is a call of fake.effects with what a script's members leave in
// it.
type scriptEffect struct {
	Kind       string `json:"kind"`
	Device     int    `json:"device"`
	Class      string `json:"class"`
	Preset     string `json:"preset"`
	Color      string `json:"color"`
	Release    bool   `json:"release"`
	Everything bool   `json:"everything"`
	Rumble     []struct {
		Low   int `json:"low"`
		High  int `json:"high"`
		Left  int `json:"left"`
		Right int `json:"right"`
		Ms    int `json:"ms"`
	} `json:"rumble"`
}

// effectsScript is the script of the tests below: each button shows one member.
const effectsScript = `import { host, log, on, pad } from "padmate";
on("south", "press", () => { pad.haptic("tick"); });
on("east", "press", () => { pad.rumble(0.5, 1, 120); pad.setLed("#336699"); });
on("west", "press", () => { pad.rumbleTriggers(1, 0.25, 50); });
on("north", "press", () => { throw new Error("boom"); });
on("leftshoulder", "press", () => { pad.rumble(0, 0, 0); pad.setLed(null); });
on("rightshoulder", "press", () => {
  if (host.has("rumble")) pad.haptic("pulse2");
  if (host.has("led")) pad.setLed("#112233");
});
on("rightstick", "press", () => {
  const b = pad.info.battery;
  pad.haptic(!b ? "pulse1" : b.state === "discharging" && b.percent === 42 ? "pulse3" : "pulse4");
});
`

// startFakeEffectsScript starts the fake IPC daemon whose gamepad has the
// capabilities caps, with the profile text profile and the script
// effectsScript as its main.ts, and returns a client.
func startFakeEffectsScript(t *testing.T, caps, profile string, off ...string) (*testLive, *jrpc2.Client) {
	t.Helper()
	tl, client, _ := startFakeEffectsScriptIn(t, caps, profile, false, off...)
	return tl, client
}

// startFakeEffectsScriptIn is startFakeEffectsScript with the watcher of the
// script file on or off (--watch), and returns the folder of main.ts.
func startFakeEffectsScriptIn(t *testing.T, caps, profile string, watch bool, off ...string) (*testLive, *jrpc2.Client, string) {
	t.Helper()
	t.Setenv(fakeCapsEnv, caps)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(effectsScript), 0o600); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	args := []string{"--profile", filepath.Join(dir, "profile.json"), "--fake-input=ipc", "--dry-run"}
	if !watch {
		args = append(args, "--watch=false")
	}
	cfg := testConfig(t, tl, "", nil, args...)
	cfg.opts.device = ""
	cfg.open = func(input.Options) (eventSource, error) { panic("fake IPC opened SDL") }
	if len(off) > 0 {
		cfg.opts.settings = settingsOff(off...)
	}
	tl.start(t, cfg)
	return tl, rpcTestClient(t, tl.liveRun), dir
}

// TestIPCScriptEffects (SCR-23): a script's members reach the fake gamepad as
// Script-class effects through the effect queue; the error of a script buzzes
// once; and everything the script has going ends when its runtime is paused.
func TestIPCScriptEffects(t *testing.T) {
	tl, client := startFakeEffectsScript(t, "rumble,trigger,rgb", fmtProfile(""))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	effects := func() []scriptEffect {
		t.Helper()
		var got struct {
			Effects []scriptEffect `json:"effects"`
		}
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		var out []scriptEffect
		for _, e := range got.Effects {
			// The ambient state of the layer is not what is looked at.
			if e.Kind != "feedback" {
				out = append(out, e)
			}
		}
		return out
	}
	press := func(control string) {
		t.Helper()
		for _, method := range []string{"fake.press", "fake.release"} {
			if err := client.CallResult(ctx, method, map[string]any{"control": control}, new(any)); err != nil {
				t.Fatal(err)
			}
		}
	}
	running := func() {
		t.Helper()
		tl.waitUntil(t, "the script running", func() bool {
			var views []slot.ScriptView
			err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views)
			return err == nil && len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending && !views[0].Script.Paused
		})
	}
	// waitFor waits until the calls since the last forget are n, and returns them.
	waitFor := func(what string, n int) []scriptEffect {
		t.Helper()
		var got []scriptEffect
		tl.waitUntil(t, what, func() bool { got = effects(); return len(got) >= n })
		return got
	}
	forget := func() {
		t.Helper()
		if err := client.CallResult(ctx, "fake.effects", map[string]bool{"clear": true}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	running()
	forget()

	// A preset: one Script-class play for the gamepad.
	press("south")
	got := waitFor("the tick", 1)
	if e := got[0]; len(got) != 1 || e.Kind != "play" || e.Device != 1 || e.Class != "script" || e.Preset != "tick" {
		t.Fatalf("pad.haptic(\"tick\"): %+v", got)
	}
	forget()

	// The triggers, which this gamepad has.
	press("west")
	got = waitFor("the trigger rumble", 1)
	if e := got[0]; e.Kind != "play" || len(e.Rumble) != 1 || e.Rumble[0].Left != 65535 || e.Rumble[0].Right != 16384 || e.Rumble[0].Ms != 50 {
		t.Fatalf("pad.rumbleTriggers(1, 0.25, 50): %+v", got[0])
	}
	forget()
	time.Sleep(150 * time.Millisecond) // the triggers' 50 ms are over: the motors' rumble is not merged with them

	// A rumble of the motors and a color: a play of steps, and a light.
	press("east")
	got = waitFor("the rumble and the color", 2)
	if e := got[0]; e.Kind != "play" || e.Class != "script" || e.Preset != "" || len(e.Rumble) != 1 ||
		e.Rumble[0].Low != 32768 || e.Rumble[0].High != 65535 || e.Rumble[0].Ms != 120 {
		t.Fatalf("pad.rumble(0.5, 1, 120): %+v", got[0])
	}
	if e := got[1]; e.Kind != "light" || e.Device != 1 || e.Class != "script" || e.Color != "#336699" || e.Release {
		t.Fatalf("pad.setLed(\"#336699\"): %+v", got[1])
	}
	forget()

	// The motors' rumble ended, and the color given back: a stop of the
	// script's rumble, a release of its claim.
	press("leftshoulder")
	got = waitFor("the stop and the release", 2)
	if e := got[0]; e.Kind != "stop" || e.Class != "script" || e.Everything {
		t.Fatalf("pad.rumble(0, 0, 0): %+v", got[0])
	}
	if e := got[1]; e.Kind != "light" || e.Class != "script" || !e.Release {
		t.Fatalf("pad.setLed(null): %+v", got[1])
	}
	forget()

	// host.has follows the gamepad and the switches: both are there.
	press("rightshoulder")
	got = waitFor("the pulses and the color", 2)
	if got[0].Preset != "pulse2" || got[1].Color != "#112233" {
		t.Fatalf("host.has: %+v", got)
	}
	forget()

	// pad.info.battery: none until the gamepad reads one.
	press("rightstick")
	if got = waitFor("a pulse", 1); got[0].Preset != "pulse1" {
		t.Fatalf("pad.info.battery before a reading: %+v", got)
	}
	forget()
	if err := client.CallResult(ctx, "fake.battery", map[string]any{"state": "discharging", "percent": 42}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the power in the snapshot", func() bool {
		forget()
		press("rightstick")
		got = effects()
		return len(got) == 1 && got[0].Preset == "pulse3"
	})
	forget()

	// The error of a script buzzes once, in the Alert class, as a failing
	// script is a warning; the same failure again does not.
	press("north")
	got = waitFor("the error cue", 1)
	if e := got[0]; e.Kind != "play" || e.Device != 1 || e.Class != "alert" || e.Preset != "error" {
		t.Fatalf("the error cue: %+v", got[0])
	}
	forget()
	press("north")
	press("north")
	press("south") // after the two errors, so that their cues would have come
	got = waitFor("the tick after the errors", 1)
	if len(got) != 1 || got[0].Preset != "tick" {
		t.Fatalf("a second error buzzed again: %+v", got)
	}
	forget()
	// A reload ends the episode: the next failure buzzes again.
	if err := client.CallResult(ctx, "script.reload", map[string]string{"device": "1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the reloaded script", func() bool {
		var views []slot.ScriptView
		err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views)
		return err == nil && len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending && views[0].Script.LastError == ""
	})
	// The runtime it replaced ended what it had going (the tick it played).
	got = waitFor("the stop at the reload", 1)
	if e := got[0]; e.Kind != "stop" || e.Device != 1 || e.Class != "script" || !e.Everything {
		t.Fatalf("at the reload: %+v", got)
	}
	forget()
	press("north")
	got = waitFor("the error cue after a reload", 1)
	if e := got[0]; e.Kind != "play" || e.Class != "alert" || e.Preset != "error" {
		t.Fatalf("the error cue after a reload: %+v", got)
	}
	forget()

	// Pausing ends the runtime, and with it what its script has going: a stop
	// of everything the script's class has, rumble and color.
	press("east")
	waitFor("the rumble and the color again", 2)
	forget()
	if err := client.CallResult(ctx, "engine.pause", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	got = waitFor("the stop at the pause", 1)
	if e := got[0]; e.Kind != "stop" || e.Device != 1 || e.Class != "script" || !e.Everything {
		t.Fatalf("at the pause: %+v", got)
	}
	forget()
	if err := client.CallResult(ctx, "engine.resume", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	running()
	// Nothing was left of the old runtime: a new one stops nothing.
	press("south")
	if got = waitFor("the tick after the resume", 1); len(got) != 1 || got[0].Kind != "play" {
		t.Fatalf("after the resume: %+v", got)
	}
}

// With the global rumble switch off nothing buzzes, not the script's members
// and not the buzz of its error, and host.has("rumble") says so; the color
// still goes. A gamepad without triggers ignores pad.rumbleTriggers. The
// profile's retired rumble member is not read (ADR-0067).
func TestIPCScriptEffectsFollowTheSwitches(t *testing.T) {
	tl, client := startFakeEffectsScript(t, "rumble,rgb", fmtProfile(`,"feedback":{"rumble":true}`), "rumble")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	press := func(control string) {
		t.Helper()
		for _, method := range []string{"fake.press", "fake.release"} {
			if err := client.CallResult(ctx, method, map[string]any{"control": control}, new(any)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tl.waitUntil(t, "the script running", func() bool {
		var views []slot.ScriptView
		err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views)
		return err == nil && len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending && !views[0].Script.Paused
	})
	// Everything that would buzz, then the one thing that still goes.
	for _, control := range []string{"south", "east", "west", "north", "rightshoulder"} {
		press(control)
	}
	var calls []scriptEffect
	tl.waitUntil(t, "the color of rightshoulder", func() bool {
		var got struct {
			Effects []scriptEffect `json:"effects"`
		}
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		calls = nil
		for _, e := range got.Effects {
			if e.Kind != "feedback" {
				calls = append(calls, e)
			}
		}
		return len(calls) >= 2
	})
	// east asked for a rumble and a color: only the color; rightshoulder asked for the
	// color, as host.has("led") is true and host.has("rumble") is false.
	if len(calls) != 2 || calls[0].Kind != "light" || calls[0].Color != "#336699" || calls[1].Kind != "light" || calls[1].Color != "#112233" {
		t.Fatalf("with the rumble switch off: %+v", calls)
	}
}

// A failure episode ends when saving main.ts installs a new runtime, as an
// explicit reload does (the watcher is on here, unlike above): script.status
// shows no error from then on, and the next failure buzzes again.
func TestIPCScriptErrorEpisodeEndsWhenTheFileIsSaved(t *testing.T) {
	tl, client, dir := startFakeEffectsScriptIn(t, "rumble,trigger,rgb", fmtProfile(""), true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status := func() slot.ScriptView {
		t.Helper()
		var views []slot.ScriptView
		if err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views); err != nil || len(views) != 1 {
			t.Fatal(views, err)
		}
		return views[0]
	}
	effects := func() []scriptEffect {
		t.Helper()
		var got struct {
			Effects []scriptEffect `json:"effects"`
		}
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		var out []scriptEffect
		for _, e := range got.Effects {
			if e.Kind != "feedback" {
				out = append(out, e)
			}
		}
		return out
	}
	forget := func() {
		t.Helper()
		if err := client.CallResult(ctx, "fake.effects", map[string]bool{"clear": true}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	press := func(control string) {
		t.Helper()
		for _, method := range []string{"fake.press", "fake.release"} {
			if err := client.CallResult(ctx, method, map[string]any{"control": control}, new(any)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// cues are the error cues among the calls since the last forget, once
	// something else (the tick of south, pressed after) shows that the calls of
	// the presses before have been made.
	cues := func() int {
		t.Helper()
		press("south")
		var got []scriptEffect
		tl.waitUntil(t, "the tick after the presses", func() bool {
			got = effects()
			for _, e := range got {
				if e.Preset == "tick" {
					return true
				}
			}
			return false
		})
		n := 0
		for _, e := range got {
			if e.Kind == "play" && e.Class == "alert" && e.Preset == "error" {
				n++
			}
		}
		forget()
		return n
	}
	tl.waitUntil(t, "the script running", func() bool {
		v := status().Script
		return v.State == "Running" && !v.Pending && !v.Paused
	})
	forget()

	// The first failure of the episode buzzes; the others do not.
	press("north")
	press("north")
	if n := cues(); n != 1 {
		t.Fatalf("one episode buzzed %d times, want once", n)
	}
	before := status().Script
	if !strings.Contains(before.LastError, "boom") {
		t.Fatalf("lastError %q", before.LastError)
	}

	// Saving the script installs a new runtime, which ends the episode.
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(effectsScript+"// saved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the saved script installed", func() bool {
		v := status().Script
		return v.Metadata.Runtime != before.Metadata.Runtime && v.State == "Running" && !v.Pending
	})
	if v := status().Script; v.LastError != "" || len(v.Macros) != 0 {
		t.Fatalf("after the save: lastError %q, macros %+v", v.LastError, v.Macros)
	}
	forget()
	press("north")
	if n := cues(); n != 1 {
		t.Fatalf("the failure after a save buzzed %d times, want once", n)
	}
	if !strings.Contains(status().Script.LastError, "boom") {
		t.Fatalf("lastError after the second episode: %q", status().Script.LastError)
	}
}

// fmtProfile is effectsProfile with extra members after script.
func fmtProfile(extra string) string {
	return `{"schemaVersion":1,"name":"fx","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.21"}` + extra + `}`
}
