package dtsgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func TestMacroProject(t *testing.T) {
	files, err := MacroProject(HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	byName := map[string]string{}
	for i, f := range files {
		names[i] = f.Name
		byName[f.Name] = string(f.Data)
	}
	want := []string{"tsconfig.json", ".padmate/padmate.d.ts", ".padmate/math.d.ts", ".padmate/globals.d.ts", ".padmate/lib.padmate-goja.d.ts"}
	if !slices.Equal(names, want) {
		t.Fatalf("files %q, want %q", names, want)
	}
	var cfg struct {
		CompilerOptions struct {
			Paths   map[string][]string `json:"paths"`
			NoEmit  bool                `json:"noEmit"`
			AllowJS bool                `json:"allowJs"`
		} `json:"compilerOptions"`
		Include []string `json:"include"`
	}
	if err := json.Unmarshal([]byte(byName["tsconfig.json"]), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.CompilerOptions.NoEmit || !cfg.CompilerOptions.AllowJS ||
		!slices.Equal(cfg.CompilerOptions.Paths["padmate"], []string{"./.padmate/padmate.d.ts"}) ||
		!slices.Equal(cfg.CompilerOptions.Paths["padmate/math"], []string{"./.padmate/math.d.ts"}) ||
		!slices.Equal(cfg.Include, []string{"**/*.ts", "**/*.js", ".padmate/*.d.ts"}) {
		t.Errorf("tsconfig.json:\n%s", byName["tsconfig.json"])
	}
	padmate := byName[".padmate/padmate.d.ts"]
	for _, s := range []string{
		"export const host:", "export const pad:", "export const key:", "export function now()", "export function sleep(",
		"export const session: KV", "export const log:", "export type KeySpec", "export interface InputEvent", "export interface KV",
		"API " + HostAPI.String(), "rumble(low: number", "haptic(preset:", "setLed(rgb:",
	} {
		if !strings.Contains(padmate, s) {
			t.Errorf("padmate.d.ts lacks %q", s)
		}
	}
	for _, s := range []string{
		"declare module", "export const on", "export const claim", "export function global", "export function layer(",
		"export function observe", "export function onAxis", "export function onStick", "export function onTick",
		"export function every", "export const layers", "export function defineScript", "export function defineAction",
		"export const param", "export const app", "export const profiles", "export const hud", "@since 1.3",
	} {
		if strings.Contains(padmate, s) {
			t.Errorf("padmate.d.ts has %q", s)
		}
	}
	math := byName[".padmate/math.d.ts"]
	if !strings.Contains(math, "export function clamp(") || !strings.HasPrefix(math, "export function clamp(") || strings.Contains(math, "declare module") {
		t.Errorf("math.d.ts:\n%s", math)
	}
	if byName[".padmate/globals.d.ts"] != globalsDeclarations || byName[".padmate/lib.padmate-goja.d.ts"] != string(compile.GojaLib()) {
		t.Error("globals or goja lib differ from a legacy project's")
	}
	legacy, err := Project(HostAPI, MainEntry, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(legacy, func(f File) bool { return f.Name == "padmate-globals.d.ts" }); i < 0 || string(legacy[i].Data) != globalsDeclarations {
		t.Error("the legacy project's globals changed")
	}
	if _, err := MacroProject(Version{Major: 0, Minor: 7}); err == nil {
		t.Error("incompatible API accepted")
	}
	if _, err := macroProject(HostAPI, []byte(`declare module "padmate" {}`), nil); err == nil {
		t.Error("declarations without padmate/math accepted")
	}
}

// The values marked @macro are what compile gives a macro's padmate module,
// and the bridge a macro's runtime installs (script, TestMacroBridgeMembers).
func TestMacroMembersMatchCompile(t *testing.T) {
	got, err := MacroMembers(HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(compile.MacroExports)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("@macro members %q, compile.MacroExports %q", got, want)
	}
}

// macroValues keeps types and the marked values, with their JSDoc, in
// every layout the canonical declarations use.
func TestMacroValues(t *testing.T) {
	const body = `/** @since 0.5 @macro */
export const a: {
  x: number;
};
/** @since 0.5 */
export const b: {
  y: number;
};
/** two JSDoc blocks */
/** @macro */
export function c(): void;
/** @since 0.5 */ export function d(): void;
/** @macro @since 0.5 */ export function e(): void;
/**
 * multi-line, @macro
 */ export const f: number;
// ---- section ----
export interface I { g(): void }
/** @since 0.5 */
export type T = "h";
export const j: number;
`
	got, names := macroValues(body)
	if !slices.Equal(names, []string{"a", "c", "e", "f"}) {
		t.Errorf("names %q", names)
	}
	for _, s := range []string{"export const b", "export function d", "export const j", "/** @since 0.5 */\nexport const b"} {
		if strings.Contains(got, s) {
			t.Errorf("kept %q:\n%s", s, got)
		}
	}
	for _, s := range []string{"/** @since 0.5 @macro */\nexport const a: {\n  x: number;\n};\n", "/** two JSDoc blocks */\n/** @macro */\nexport function c", "export interface I", "/** @since 0.5 */\nexport type T", "// ---- section ----", " */ export const f"} {
		if !strings.Contains(got, s) {
			t.Errorf("lost %q:\n%s", s, got)
		}
	}
}

// A new macro builds as a macro and its default export is a function.
func TestMacroTemplateBuilds(t *testing.T) {
	if !strings.Contains(MacroTemplate, `from "padmate"`) || !strings.Contains(MacroTemplate, "export default") {
		t.Fatalf("template:\n%s", MacroTemplate)
	}
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"macros/脚本 1.ts": MacroTemplate})
	r, err := compile.Build(compile.BuildOptions{Root: filepath.Join(root, "macros"), Entry: "脚本 1.ts", API: HostAPI.String(), Macro: true})
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	api := vm.NewObject()
	_ = api.Set("脚本 1.ts", vm.NewObject())
	_ = vm.Set(compile.MacroAPIGlobal, api)
	if _, err := vm.RunString(string(r.JS)); err != nil {
		t.Fatal(err)
	}
	if _, ok := goja.AssertFunction(vm.Get(compile.MacroGlobal).ToObject(vm).Get("default")); !ok {
		t.Error("default export is not a function")
	}
}

// writeMacroProject writes the files of MacroProject into dir/macros, and
// the legacy project with a main.ts into dir when legacy is set. It returns
// the macros folder.
func writeMacroProject(t *testing.T, dir string, legacy bool) string {
	t.Helper()
	files, err := MacroProject(HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{}
	for _, f := range files {
		scripts["macros/"+f.Name] = string(f.Data)
	}
	if legacy {
		project, err := Project(HostAPI, MainEntry, []byte(MainTemplate), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range project {
			scripts[f.Name] = string(f.Data)
		}
	}
	writeScripts(t, dir, scripts)
	return filepath.Join(dir, "macros")
}

// The macros folder type-checks with its own tsconfig: the template, a
// macro that uses pad, session, padmate/math and a helper module, and a
// JavaScript macro pass; a member macros do not have fails. A legacy
// project in the profile's folder still passes, with the macros checked
// against its ambient padmate module: it does not read .padmate, whose
// globals would clash with its own (TS2451).
func TestMacroProjectWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	const more = `import { key, pad, session, log, now, host, sleep } from "padmate";
import { clamp, deadzone } from "padmate/math";
import { twice } from "./lib/util";
export default async function () {
  const x: number = clamp(deadzone(pad.axis("leftx"), 0.1), -1, 1);
  const n = session.get<number>("n") ?? 0;
  session.set("n", n + 1);
  log.info(host.version, now(), x, twice("A"));
  if (pad.isDown("south")) await key.tap(["Ctrl+A", "Ctrl+C"]);
  key.down("Shift");
  await sleep(20);
  key.up("Shift");
}
`
	for _, legacy := range []bool{false, true} {
		dir := t.TempDir()
		macros := writeMacroProject(t, dir, legacy)
		writeScripts(t, dir, map[string]string{
			"macros/复制全部.ts":       MacroTemplate,
			"macros/用到更多 (2).ts":   more,
			"macros/lib/util.ts":   "export const twice = (s: string): string => s + s;\n",
			"macros/js 宏.js":       "import { key } from \"padmate\";\nexport default async function () { await key.tap(\"F13\"); }\n",
			"macros/.padmate/x.js": "", // not a source: tsc must not need it
		})
		if output, err := tscProject(t, tsc, macros); err != nil {
			t.Fatalf("legacy %v: tsc macros: %v\n%s", legacy, err, output)
		}
		if legacy {
			if output, err := tscProject(t, tsc, dir); err != nil {
				t.Fatalf("tsc of the legacy project with macros: %v\n%s", err, output)
			}
		}
		writeScripts(t, dir, map[string]string{"macros/bad.ts": `import { key, on } from "padmate";
export default function () { on("south", "press", () => key.tap("A")); }
`})
		output, err := tscProject(t, tsc, macros)
		if err == nil || !strings.Contains(output, "bad.ts") || !strings.Contains(output, "TS2305") {
			t.Fatalf("legacy %v: tsc did not refuse on: %v\n%s", legacy, err, output)
		}
		if strings.Contains(output, "用到更多") || strings.Contains(output, "复制全部") {
			t.Errorf("errors in the good macros:\n%s", output)
		}
	}
}
