package compile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// writeTree writes files, by slash-separated path relative to root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// macroVM runs a macro bundle in a runtime whose bridge for file records
// key.tap and sleep calls, and returns the runtime and the macro's
// default export.
func macroVM(t *testing.T, js []byte, file string, calls *[]string) (*goja.Runtime, goja.Callable) {
	t.Helper()
	vm := run(t, js, func(vm *goja.Runtime) {
		bridge := vm.NewObject()
		key := vm.NewObject()
		_ = key.Set("tap", func(spec string) { *calls = append(*calls, "tap "+spec) })
		_ = bridge.Set("key", key)
		_ = bridge.Set("sleep", func(ms int) { *calls = append(*calls, "sleep") })
		session := vm.NewObject()
		_ = session.Set("get", func(k string) string { return "v:" + k })
		_ = bridge.Set("session", session)
		api := vm.NewObject()
		_ = api.Set(file, bridge)
		_ = api.Set("other.ts", vm.NewObject())
		_ = vm.Set(MacroAPIGlobal, api)
		host := vm.NewObject()
		_ = host.Set("tickMs", 8)
		_ = vm.Set(HostGlobal, host)
	})
	exports, ok := vm.Get(MacroGlobal).(*goja.Object)
	if !ok {
		t.Fatalf("%s = %v", MacroGlobal, vm.Get(MacroGlobal))
	}
	fn, ok := goja.AssertFunction(exports.Get("default"))
	if !ok {
		t.Fatalf("default export %v is not a function", exports.Get("default"))
	}
	if v := vm.Get(BundleGlobal); v != nil {
		t.Errorf("a macro set %s", BundleGlobal)
	}
	return vm, fn
}

// A macro's padmate module reads the bridge of its own file, by the name
// written into the bundle; padmate/math and helper modules work. The root
// is the macros folder.
func TestBuildMacro(t *testing.T) {
	root := t.TempDir()
	const file = "复制全部 (1).ts"
	writeTree(t, root, map[string]string{
		file: `import { key, sleep, session } from "padmate";
import { clamp } from "padmate/math";
import { twice } from "./lib/util";
export default async function () {
  key.tap(twice("A") + clamp(9, 0, 1) + session.get("k"));
  sleep(5);
}
`,
		"lib/util.ts": "export const twice = (s: string) => s + s;\n",
	})
	r, err := Build(BuildOptions{Root: root, Entry: file, API: "0.8", Macro: true})
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	_, fn := macroVM(t, r.JS, file, &calls)
	if _, err := fn(goja.Undefined()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tap AA1v:k", "sleep"}; !slices.Equal(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
	// The source map names the sources relative to the root; esbuild
	// percent-encodes the characters outside ASCII and the parentheses,
	// so a stack frame's file needs url.PathUnescape.
	if sources := sourceMapSources(t, r.JS); !slices.Contains(sources, "%E5%A4%8D%E5%88%B6%E5%85%A8%E9%83%A8 %281%29.ts") {
		t.Errorf("source map sources %q", sources)
	}
	// export * re-exports the macro's members only.
	writeTree(t, root, map[string]string{"all.ts": `export * from "padmate"; export default () => {};`})
	r, err = Build(BuildOptions{Root: root, Entry: "all.ts", Macro: true})
	if err != nil {
		t.Fatal(err)
	}
	vm := run(t, r.JS, func(vm *goja.Runtime) {
		api := vm.NewObject()
		_ = api.Set("all.ts", vm.NewObject())
		_ = vm.Set(MacroAPIGlobal, api)
	})
	keys := vm.Get(MacroGlobal).ToObject(vm).Keys()
	slices.Sort(keys)
	want := append([]string{"default"}, MacroExports...)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("export * gives %q, want %q", keys, want)
	}
}

func sourceMapSources(t *testing.T, js []byte) []string {
	t.Helper()
	const prefix = "//# sourceMappingURL=data:application/json;base64,"
	s := strings.TrimRight(string(js), "\n")
	data, ok := strings.CutPrefix(s[strings.LastIndex(s, "\n")+1:], prefix)
	if !ok {
		t.Fatal("no inline source map")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	var sm struct {
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatal(err)
	}
	return sm.Sources
}

// A macro cannot import what takes over input or outlives a run: named
// imports and members of a namespace import get the same error.
func TestBuildMacroRestricted(t *testing.T) {
	const tail = " is not available in macros; a macro can use host, key, log, now, pad, session, sleep and padmate/math"
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"named", `import { key, on } from "padmate";` + "\nexport default () => { on(); key; };\n", []string{`m.ts:1:15: error: "on"` + tail}},
		{"renamed", `import { layers as l } from "padmate";` + "\nexport default () => l;\n", []string{`m.ts:1:10: error: "layers"` + tail}},
		{"namespace", `import * as p from "padmate";` + "\nexport default () => { p.key; p.claim(); p.profiles; };\n", []string{
			`m.ts:2:33: error: "claim"` + tail, `m.ts:2:44: error: "profiles"` + tail,
		}},
		{"helper", `import { go } from "./lib/h";` + "\nexport default go;\n", []string{`lib/h.ts:1:10: error: "every"` + tail}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{"m.ts": tc.source, "lib/h.ts": `import { every } from "padmate"; export const go = () => every(1, () => {});`})
			_, err := Build(BuildOptions{Root: root, Entry: "m.ts", Macro: true})
			var be *BuildError
			if !errors.As(err, &be) {
				t.Fatalf("err = %v", err)
			}
			var got []string
			for _, d := range be.Diagnostics {
				got = append(got, d.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("diagnostics\n %q\nwant %q", got, tc.want)
			}
		})
	}
	// The legacy module still has everything.
	root := t.TempDir()
	writeTree(t, root, map[string]string{"main.ts": `import { on, layers } from "padmate"; on; layers;`})
	if _, err := Build(BuildOptions{Root: root, Entry: "main.ts"}); err != nil {
		t.Errorf("legacy build: %v", err)
	}
}

// A macro is a top-level file of the macros folder, its root, named as on
// disk with a valid name; a legacy entry is never in its profile's macros
// folder, whatever the path says.
func TestBuildMacroEntry(t *testing.T) {
	macros := t.TempDir()
	writeTree(t, macros, map[string]string{
		"a.ts": "export default () => {};\n", "v1.2.ts": "export default () => {};\n", "lib/b.ts": "export default () => {};\n",
	})
	profile := t.TempDir()
	writeTree(t, profile, map[string]string{
		"macros/a.ts": "export default () => {};\n", "macros/lib/b.ts": "export default () => {};\n", "c.ts": "export default () => {};\n",
	})
	for _, tc := range []struct {
		opts BuildOptions
		want string
	}{
		{BuildOptions{Root: macros, Entry: "a.ts", Macro: true}, ""},
		{BuildOptions{Root: macros, Entry: filepath.Join(macros, "a.ts"), Macro: true}, ""},
		{BuildOptions{Root: macros, Entry: "lib/b.ts", Macro: true}, "is not a file of the macros folder"},
		{BuildOptions{Root: macros, Entry: "v1.2.ts", Macro: true}, `the name contains '.'`},
		{BuildOptions{Root: macros, Entry: filepath.Join(profile, "c.ts"), Macro: true}, "outside the script root"},
		{BuildOptions{Root: profile, Entry: "macros/a.ts", Macro: true}, "is not a file of the macros folder"},
		{BuildOptions{Root: profile, Entry: "c.ts"}, ""},
		{BuildOptions{Root: profile, Entry: "macros/a.ts"}, "where each file is a macro"},
		{BuildOptions{Root: profile, Entry: `macros\a.ts`}, "where each file is a macro"},
		{BuildOptions{Root: profile, Entry: "lib/../macros/lib/b.ts"}, "where each file is a macro"},
	} {
		_, err := Build(tc.opts)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: %v, want %q", tc.opts, err, tc.want)
		}
	}
	// Windows finds the folder and the file whatever the case; the
	// canonical path does not change the folder's checks, and a macro must
	// be named as it is.
	if _, err := os.Stat(filepath.Join(profile, "MACROS", "A.TS")); err == nil {
		if _, err := Build(BuildOptions{Root: profile, Entry: "MACROS/a.ts"}); err == nil || !strings.Contains(err.Error(), "each file is a macro") {
			t.Errorf("legacy entry in MACROS: %v", err)
		}
		if _, err := Build(BuildOptions{Root: macros, Entry: "A.ts", Macro: true}); err == nil || !strings.Contains(err.Error(), "as named") {
			t.Errorf("macro A.ts for a.ts: %v", err)
		}
	}
	// A link from outside the folder into it is a legacy entry in it.
	if err := os.Symlink(filepath.Join(profile, "macros", "a.ts"), filepath.Join(profile, "link.ts")); err != nil {
		t.Log("symlinks unavailable:", err)
	} else if _, err := Build(BuildOptions{Root: profile, Entry: "link.ts"}); err == nil || !strings.Contains(err.Error(), "each file is a macro") {
		t.Errorf("legacy entry linked into macros: %v", err)
	}
}

// BuildMacros builds each file on its own: one with an error does not
// stop the others, and the cache serves an unchanged file.
func TestBuildMacros(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"好.ts":  `import { key } from "padmate"; export default () => key;`,
		"坏.ts":  "export default () => {;\n",
		"也好.js": "export default function () {}\n",
		"受限.ts": `import { on } from "padmate"; export default on;`,
	})
	c := NewCache()
	files := []string{"好.ts", "坏.ts", "也好.js", "受限.ts", "不在.ts", "a.TS"}
	results := c.BuildMacros(root, files, "0.8")
	for i, r := range results {
		if r.File != files[i] {
			t.Fatalf("result %d is %q", i, r.File)
		}
	}
	var be *BuildError
	if results[0].Err != nil || results[0].Result.Size == 0 || results[2].Err != nil || results[0].Key == "" {
		t.Errorf("good macros: %+v %+v", results[0].Err, results[2].Err)
	}
	if !errors.As(results[1].Err, &be) || !errors.As(results[3].Err, &be) || !strings.Contains(results[3].Err.Error(), `"on" is not available`) {
		t.Errorf("bad macros: %v; %v", results[1].Err, results[3].Err)
	}
	if results[4].Err == nil || errors.As(results[4].Err, &be) || results[5].Err == nil {
		t.Errorf("missing or invalid file: %v; %v", results[4].Err, results[5].Err)
	}
	if got := c.Stats(); got.Misses != 4 || c.Len() != 2 {
		t.Errorf("stats %+v, %d entries", got, c.Len())
	}
	again := c.BuildMacros(root, files[:1], "0.8")
	if again[0].Err != nil || again[0].Key != results[0].Key || string(again[0].Result.JS) != string(results[0].Result.JS) || c.Stats().Hits != 1 {
		t.Errorf("rebuild: %v, key %q then %q, stats %+v", again[0].Err, results[0].Key, again[0].Key, c.Stats())
	}
	if r := c.BuildMacros(filepath.Join(root, "nope"), files[:1], "0.8"); r[0].Err == nil {
		t.Error("missing root accepted")
	}
	if r := (*Cache)(nil).BuildMacros(root, files[:1], "0.8"); r[0].Err != nil || r[0].Key != results[0].Key {
		t.Errorf("nil cache: %v", r[0].Err)
	}
}

// The cache key leaves out profile.json, unless the bundle imports it, and
// the macros' type declarations, of a profile's macros folder and of a
// macros folder that is the root; it tells a macro from a legacy script.
// Each root, entry and kind has one entry, so saves do not grow the cache.
func TestCacheKeyMacros(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.ts":                  "export default {};\n",
		"json.ts":                  `import p from "./profile.json"; export default p;`,
		"macros/a.ts":              "export default () => {};\n",
		"macros/.padmate/types.ts": "export {};\n",
		"profile.json":             `{"n":0}`,
	})
	macros := filepath.Join(root, "macros")
	c := NewCache()
	build := func(root, entry string, macro bool) {
		t.Helper()
		if _, err := c.Build(BuildOptions{Root: root, Entry: entry, API: "0.8", Macro: macro}); err != nil {
			t.Fatal(err)
		}
	}
	all := func() {
		build(root, "main.ts", false)
		build(macros, "a.ts", true)
		build(root, "json.ts", false)
	}
	all()
	if got := c.Stats(); got.Misses != 3 || c.Len() != 3 {
		t.Fatalf("stats %+v, %d entries", got, c.Len())
	}
	for i := range 5 {
		writeTree(t, root, map[string]string{"profile.json": `{"n":` + string(rune('1'+i)) + `}`, "macros/.padmate/types.ts": "// " + string(rune('a'+i))})
		all()
	}
	// Five saves of profile.json: only the script that imports it rebuilt.
	if got := c.Stats(); got.Misses != 3+5 || got.Hits != 2*5 || c.Len() != 3 {
		t.Errorf("after saves: stats %+v, %d entries", got, c.Len())
	}
	// A source change rebuilds, and replaces the entry.
	writeTree(t, root, map[string]string{"macros/lib/new.ts": "export {};\n"})
	all()
	if got := c.Stats(); got.Misses != 3+5+3 || c.Len() != 3 {
		t.Errorf("after a new source: stats %+v, %d entries", got, c.Len())
	}
	// The macro is the same build whatever path names the folder and file.
	if _, err := c.Build(BuildOptions{Root: macros + string(filepath.Separator), Entry: filepath.Join(macros, "a.ts"), API: "0.8", Macro: true}); err != nil || c.Len() != 3 {
		t.Errorf("same macro: %v, %d entries", err, c.Len())
	}
}

// Past maxCacheEntries the least recently used entry goes.
func TestCacheLimit(t *testing.T) {
	c := NewCache()
	for i := range maxCacheEntries + 3 {
		c.store(cacheSlot{root: "r", entry: string(rune('a' + i))}, &cacheEntry{})
		if i == 10 {
			c.store(cacheSlot{root: "r", entry: "a"}, &cacheEntry{}) // a is used again
		}
	}
	if c.Len() != maxCacheEntries {
		t.Fatalf("%d entries", c.Len())
	}
	if c.entries[cacheSlot{root: "r", entry: "a"}] == nil || c.entries[cacheSlot{root: "r", entry: "d"}] != nil || c.entries[cacheSlot{root: "r", entry: "e"}] == nil {
		t.Error("evicted the wrong entries")
	}
}

// Concurrent builds of the same sources each run esbuild without the
// cache's lock and store the same bundle.
func TestCacheConcurrent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"macros/a.ts": "export default () => 1;\n", "main.ts": "export default {};\n"})
	macros := filepath.Join(root, "macros")
	c := NewCache()
	results := make(chan string, 8)
	for i := range cap(results) {
		go func() {
			if i%2 == 0 {
				r := c.BuildMacros(macros, []string{"a.ts"}, "0.8")
				results <- string(r[0].Result.JS) + errString(r[0].Err)
				return
			}
			r, err := c.Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
			results <- string(r.JS) + errString(err)
		}()
	}
	seen := map[string]bool{}
	for range cap(results) {
		seen[<-results] = true
	}
	if len(seen) != 2 || c.Len() != 2 {
		t.Errorf("%d different results, %d entries", len(seen), c.Len())
	}
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
