package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// key.sequence, key.type and SequenceStep came with API 0.21 (SCR-26, ADR-0070):
// 0.17's declarations have none of them, 0.21's have all.
func TestSequenceAPIBoundary(t *testing.T) {
	for _, tc := range []struct {
		api  Version
		want bool
	}{{Version{Major: 0, Minor: 17}, false}, {Version{Major: 0, Minor: 20}, false}, {HostAPI, true}} {
		data, err := Filter(defaultDeclarations, tc.api)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"sequence(steps: readonly SequenceStep[]): Promise<TapResult>", "type(text: string): Promise<TapResult>", "export type SequenceStep ="} {
			if strings.Contains(string(data), name) != tc.want {
				t.Errorf("API %s: has %q = %v, want %v", tc.api, name, !tc.want, tc.want)
			}
		}
		// The members of key that are older stay in every API.
		if !strings.Contains(string(data), "tap(spec: KeySpec | readonly KeySpec[]") {
			t.Errorf("API %s lost key.tap", tc.api)
		}
	}
}

// The scripts of API 0.21 type-check against the declarations of key.sequence
// and key.type: one member in a step, a text and a number where they belong; and
// 0.17's declarations have neither.
func TestSequenceTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	const probe = `import {key, on, type SequenceStep, type TapResult} from "padmate";
on("south", "press", async () => {
  const done: TapResult = await key.sequence([{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }, { keys: "VolumeMute" }]);
  const typed: TapResult = await key.type("hi\n");
  const dropped: boolean = typed.dropped && done.dropped;
  const steps: readonly SequenceStep[] = [{ keys: "A" }, { wait: 5 }];
  await key.sequence(steps);
  // @ts-expect-error a step has one member
  await key.sequence([{ keys: "A", wait: 5 }]);
  // @ts-expect-error a step is an object
  await key.sequence(["Ctrl+A"]);
  // @ts-expect-error the old placeholder name
  await key.sequence([{ waitMs: 5 }]);
  // @ts-expect-error a wait is a number
  await key.sequence([{ wait: "100" }]);
  // @ts-expect-error a text is a string
  await key.sequence([{ text: 5 }]);
  // @ts-expect-error the steps are an array
  await key.sequence({ keys: "A" });
  // @ts-expect-error a text is a string
  await key.type(5);
  // @ts-expect-error a shortcut name
  await key.sequence([{ keys: "NoSuchKey+++" }]);
  // @ts-expect-error nothing to type
  await key.type();
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
		t.Fatalf("sequence types: %v\n%s", err, output)
	}

	// With the previous API's declarations the members are not there, and the
	// older ones are.
	previous := t.TempDir()
	if err := Scaffold(previous, Version{Major: 0, Minor: 17}, nil, nil); err != nil {
		t.Fatal(err)
	}
	const old = `import {key} from "padmate";
// @ts-expect-error API 0.21
key.sequence([{ keys: "A" }]);
// @ts-expect-error API 0.21
key.type("a");
key.tap(["Ctrl+Z", "WheelUp"]);
`
	if err := os.WriteFile(filepath.Join(previous, "main.ts"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("node", tsc, "--project", previous, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sequence types of API 0.17: %v\n%s", err, output)
	}
}
