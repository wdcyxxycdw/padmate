package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func TestCanonicalDeclarationsParse(t *testing.T) {
	result := api.Transform(string(defaultDeclarations), api.TransformOptions{Loader: api.LoaderTS, LogLevel: api.LogLevelSilent})
	if len(result.Errors) != 0 {
		t.Fatalf("canonical padmate.d.ts does not parse: %v", result.Errors)
	}
	for _, apiVersion := range []Version{{Major: 0, Minor: 5}, {Major: 0, Minor: 6}, HostAPI, {Major: 1, Minor: 3}} {
		filtered, err := Filter(defaultDeclarations, apiVersion)
		if err != nil {
			t.Fatal(err)
		}
		result = api.Transform(string(filtered), api.TransformOptions{Loader: api.LoaderTS, LogLevel: api.LogLevelSilent})
		if len(result.Errors) != 0 {
			t.Fatalf("filtered declarations for %s do not parse: %v", apiVersion, result.Errors)
		}
	}
}

func TestFilteredV05DeclarationsMatchRuntimeArityTable(t *testing.T) {
	filtered, err := Filter(defaultDeclarations, Version{Major: 0, Minor: 5})
	if err != nil {
		t.Fatal(err)
	}
	text := string(filtered)
	for _, future := range []string{"defineAction", "padmate/math", "stick(s:", "hud:", "AppInfo", "sequence(steps:"} {
		if strings.Contains(text, future) {
			t.Errorf("v0.5 declarations contain future member %q", future)
		}
	}
	for _, current := range []string{
		"export function defineScript(",
		"export const host:",
		"export const pad:",
		"export const key:",
		"export function global(): Scope",
		"export function now(): number",
	} {
		if !strings.Contains(text, current) {
			t.Errorf("v0.5 declarations are missing %q", current)
		}
	}
}

func TestGestureSurfaceFollowsM5M6Boundary(t *testing.T) {
	for _, tc := range []struct {
		api      Version
		advanced bool
	}{
		{Version{Major: 0, Minor: 5}, false},
		{Version{Major: 0, Minor: 6}, true},
	} {
		filtered, err := Filter(defaultDeclarations, tc.api)
		if err != nil {
			t.Fatal(err)
		}
		s := string(filtered)
		if strings.Count(s, "export type Gesture =") != 1 || !strings.Contains(s, `export type Gesture = keyof GestureMap;`) {
			t.Fatalf("%s has an invalid Gesture declaration", tc.api)
		}
		for _, member := range []string{"tap: true", "hold: true", "holdKeys(", "holdLayer(", "onEnd(", "multiTapMs?:"} {
			if got := strings.Contains(s, member); got != tc.advanced {
				t.Errorf("%s contains %q = %v, want %v", tc.api, member, got, tc.advanced)
			}
		}
	}
}

// Set PADMATE_TEST_TSC to TypeScript's tsc.js to check the declarations with
// the real compiler. CI without Node keeps the hermetic parser tests above.
func TestFilteredGestureTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to tsc.js for the TypeScript type check")
	}
	for _, tc := range []struct {
		api   Version
		probe string
	}{
		{Version{Major: 0, Minor: 5}, `import { on } from "padmate";
on("south", "press", e => { const g: "press" | "release" = e.gesture; void g; });
// @ts-expect-error hold starts in API 0.6
on("south", "hold", () => {});
on("south", "press", e => {
  // @ts-expect-error holdKeys starts in API 0.6
  e.holdKeys("Z");
});
`},
		{Version{Major: 0, Minor: 6}, `import { on } from "padmate";
on("south", "hold", e => {
  if (e.gesture === "hold") e.holdKeys("Z");
});
on("south", "doubleTap", e => {
  if (e.gesture === "doubleTap") e.onEnd(() => {});
});
`},
		{Version{Major: 0, Minor: 7}, `import { on } from "padmate";
on("south", "hold", e => {
  if (e.gesture === "hold") e.holdKeys("Z");
});
on("south", "doubleTap", e => {
  if (e.gesture === "doubleTap") e.onEnd(() => {});
});
`},
	} {
		t.Run(tc.api.String(), func(t *testing.T) {
			filtered, err := Filter(defaultDeclarations, tc.api)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "padmate.d.ts"), filtered, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "probe.ts"), []byte(tc.probe), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("node", tsc, "--strict", "--noEmit", "--skipLibCheck", "--target", "es2022", "--module", "esnext", "--moduleResolution", "bundler", "probe.ts", "padmate.d.ts")
			cmd.Dir = dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tsc %s: %v\n%s", tc.api, err, output)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"0.5", Version{Major: 0, Minor: 5}},
		{"1.0", Version{Major: 1, Minor: 0}},
		{"v0.12", Version{Major: 0, Minor: 12}},
	}
	for _, tc := range cases {
		got, err := ParseVersion(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseVersion(%q) = %#v, %v; want %#v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "0", "0.x", "-1.2", "0.5.1", "999999999999999999999999999999.5", "0.999999999999999999999999999999"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded", in)
		}
	}
}

func TestFilterSinceRemovesFutureDeclarationsAndMembers(t *testing.T) {
	src := `declare module "padmate" {
  /** @since 0.5 */ export function old(a: string): void;
  /** @since 0.6 */ export function newer(): void;
  export interface Thing {
    /** @since 0.5 */ readonly old: string;
    /** @since 0.10 */ readonly future: number;
    /** no version */ readonly stable: boolean;
  }
}
/** @since 0.6 */
declare module "padmate/math" {
  export function lerp(a: number, b: number, t: number): number;
}
`
	got, err := FilterString(src, Version{Major: 0, Minor: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"old(a: string)", "readonly old", "readonly stable"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered output missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"newer", "future", "padmate/math", "lerp"} {
		if strings.Contains(got, absent) {
			t.Errorf("filtered output contains future %q:\n%s", absent, got)
		}
	}
}

func TestFilterKeepsNestedDeclarationWhenVersionAllowed(t *testing.T) {
	src := "/** @since 0.5 */\ndeclare module \"padmate\" {\n  /** @since 0.6 */ export function newer(): void;\n}\n"
	got, err := FilterString(src, Version{Major: 0, Minor: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "newer") {
		t.Fatalf("allowed declaration missing:\n%s", got)
	}
}

func TestFilterIgnoresBracesInsideComments(t *testing.T) {
	src := `/** @since 0.6 */
// this comment has a misleading { brace
declare module "future" {
  /** @since 0.6 */ export const value: { nested: true };
}
/** @since 0.5 */
declare const stable: true;
`
	got, err := FilterString(src, Version{Major: 0, Minor: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "future") || strings.Contains(got, "value") {
		t.Fatalf("future declaration survived comment brace filtering:\n%s", got)
	}
	if !strings.Contains(got, "stable") {
		t.Fatalf("stable declaration was lost:\n%s", got)
	}
}

func TestScaffoldWritesFilteredTypesAndTSConfig(t *testing.T) {
	dir := t.TempDir()
	src := []byte("/** @since 0.5 */ declare module \"padmate\" { export const ok: true; }\n/** @since 0.99 */ declare const future: false;\n")
	if err := Scaffold(dir, HostAPI, src, nil); err != nil {
		t.Fatal(err)
	}
	dts, err := os.ReadFile(filepath.Join(dir, "padmate.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dts), "ok") || strings.Contains(string(dts), "future") {
		t.Fatalf("d.ts = %s", dts)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"**/*.ts"`) || !strings.Contains(string(cfg), `"lib": []`) || !strings.Contains(string(cfg), `"noEmit": true`) {
		t.Fatalf("tsconfig = %s", cfg)
	}
	globals, err := os.ReadFile(filepath.Join(dir, "padmate-globals.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(globals), "console") {
		t.Fatalf("globals = %s", globals)
	}
	lib, err := os.ReadFile(filepath.Join(dir, "lib.padmate-goja.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) == 0 {
		t.Fatal("scaffold wrote an empty goja lib")
	}
	main, err := os.ReadFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), `from "padmate"`) {
		t.Fatalf("main.ts = %s", main)
	}
	if err := Scaffold(dir, HostAPI, src, nil); err == nil {
		t.Fatal("second scaffold overwrote existing files")
	}
}

func TestFilteredV05SurfaceRunsThroughVirtualPadmateModule(t *testing.T) {
	root := t.TempDir()
	source := `import { host, key, on, pad } from "padmate";
if (host.has("hud")) throw new Error("unexpected hud");
if (pad.has("south")) throw new Error("unexpected pad control");
if (!key.parse("Ctrl+Z").ok) throw new Error("key parser unavailable");
on("south", "press", () => key.tap("Ctrl+Z"));
`
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	program, err := goja.Compile("bundle.js", string(bundle.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	host := script.NewHost(clock.NewFake())
	if err := host.Activate(program); err != nil {
		t.Fatal(err)
	}
	if got := len(host.Registrations()); got != 1 {
		t.Fatalf("virtual padmate registration count = %d, want 1", got)
	}
}
