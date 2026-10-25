package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wheel keys and mouse buttons came with API 0.8 (ADR-0062): MouseKey is declared from 0.8,
// and Key is the union with it from 0.8 and the old one before, as two
// declarations of which the filter keeps one.
func TestMouseKeyAPIBoundary(t *testing.T) {
	for _, version := range []Version{{Major: 0, Minor: 7}, HostAPI} {
		data, err := Filter(defaultDeclarations, version)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		withMouse := version == HostAPI
		if got := strings.Contains(text, "export type MouseKey ="); got != withMouse {
			t.Errorf("%s: MouseKey declared = %v, want %v", version, got, withMouse)
		}
		if got := strings.Count(text, "export type Key ="); got != 1 {
			t.Errorf("%s: %d declarations of Key, want 1", version, got)
		}
		if got := strings.Contains(text, "| Modifier | MouseKey"); got != withMouse {
			t.Errorf("%s: Key has MouseKey = %v, want %v", version, got, withMouse)
		}
		for _, name := range []string{
			"WheelUp", "WheelDown", "WheelLeft", "WheelRight",
			"MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2",
			"LButton", "RButton", "MButton", "XButton1", "XButton2",
		} {
			if got := strings.Contains(text, `"`+name+`"`); got != withMouse {
				t.Errorf("%s: %s named = %v, want %v", version, name, got, withMouse)
			}
		}
	}
}

// A bare wheel key or mouse button type-checks wherever a KeySpec does, and a
// chord that ends in one; names it does not have do not (ADR-0062).
func TestMouseKeyTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	const probe = `import { key, type Key, type KeySpec, type MouseKey } from "padmate";
const a: Key = "WheelUp";
const b: KeySpec = "WheelDown";
const c: KeySpec = "Ctrl+WheelUp";
const d: MouseKey = "WheelLeft";
const e: KeySpec = "Shift+Alt+WheelRight";
key.tap("WheelRight");
key.tap(["Ctrl+WheelUp", "WheelDown", "Ctrl+Z"]);
key.down("WheelUp");
key.up("WheelUp");
const held: boolean = key.isHeld("WheelDown");
// @ts-expect-error there is no WheelMiddle
const f: MouseKey = "WheelMiddle";
// @ts-expect-error a wheel key is not a keyboard key name
const g: Key = "Wheel";
// @ts-expect-error a sequence is an array, not a chord of keys
const h: KeySpec[] = "WheelUp";
const buttons: Key[] = ["MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2"];
const aliases: KeySpec[] = ["LButton", "RButton", "MButton", "XButton1", "XButton2", "Ctrl+LButton"];
const i: MouseKey = "MouseLeft";
const j: KeySpec = "Shift+MouseRight";
key.tap("MouseLeft");
key.tap(["Ctrl+MouseLeft", "XButton1", "Ctrl+Z"]);
key.down("MouseMiddle");
key.up("MouseMiddle");
const heldButton: boolean = key.isHeld("MouseLeft");
// @ts-expect-error there is no MouseX3
const k: MouseKey = "MouseX3";
// @ts-expect-error there is no XButton3
const l: Key = "XButton3";
// @ts-expect-error a bare Mouse is no key
const m: Key = "Mouse";
`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mouse key types: %v\n%s", err, output)
	}
}
