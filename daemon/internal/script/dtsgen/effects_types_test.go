package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The rumble and LED members of pad and the battery of pad.info came with API
// 0.17 (SCR-23, ADR-0066): 0.16's declarations have none of them, 0.17's have
// all, and the gyro, the accelerometer and the touchpad stay 1.3.
func TestEffectsAPIBoundary(t *testing.T) {
	for _, tc := range []struct {
		api  Version
		want bool
	}{{Version{Major: 0, Minor: 16}, false}, {HostAPI, true}} {
		data, err := Filter(defaultDeclarations, tc.api)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"rumble(low: number", "rumbleTriggers(left: number", "haptic(preset:", "setLed(rgb:", "readonly battery?:"} {
			if strings.Contains(string(data), name) != tc.want {
				t.Errorf("API %s: has %q = %v, want %v", tc.api, name, !tc.want, tc.want)
			}
		}
		for _, name := range []string{"gyro()", "accel()", "touchpad("} {
			if strings.Contains(string(data), name) {
				t.Errorf("API %s declares %s (1.3)", tc.api, name)
			}
		}
	}
}

// The scripts of the supported APIs type-check against their own declarations:
// the members are 0.17's, and the stable pad is the same.
func TestEffectsTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	const probe = `import {host, on, pad} from "padmate";
on("south", "press", () => {
  if (host.has("rumble")) {
    pad.rumble(0.5, 1, 200);
    pad.rumble(0, 0, 0);
    pad.rumbleTriggers(1, 0.2, 5000);
  }
  pad.haptic("tick");
  pad.haptic("confirm");
  pad.haptic("error");
  pad.haptic("pulse3");
  const name: string = "pulse" + 1;
  if (host.has("led")) {
    pad.setLed("#FFAA00");
    pad.setLed(null);
  }
  const battery = pad.info.battery;
  if (battery && battery.state === "discharging" && battery.percent !== undefined && battery.percent < 20) pad.haptic("error");
  const state: "wired" | "charging" | "discharging" | "unknown" | undefined = battery?.state;
  // @ts-expect-error a strength is a number
  pad.rumble("1", 0, 100);
  // @ts-expect-error the time is required
  pad.rumble(1, 1);
  // @ts-expect-error three numbers
  pad.rumbleTriggers(1, 1);
  // @ts-expect-error a preset name
  pad.haptic("buzz");
  // @ts-expect-error pulse1 to pulse4
  pad.haptic("pulse5");
  // @ts-expect-error a color text or null
  pad.setLed(0xFFAA00);
  // @ts-expect-error null, not nothing
  pad.setLed();
  // @ts-expect-error the battery is read only
  pad.info.battery = undefined;
  // @ts-expect-error the gyro is API 1.3
  pad.gyro();
});
`
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("effects types: %v\n%s", err, output)
	}

	// The previous supported API, 0.17, is the one they came with: its declarations
	// have all of them too (those of 0.16, which is no longer supported, have none:
	// TestEffectsAPIBoundary).
	previous := t.TempDir()
	if err := Scaffold(previous, Version{Major: 0, Minor: 17}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("node", tsc, "--project", previous, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("effects types of API 0.17: %v\n%s", err, output)
	}
}
