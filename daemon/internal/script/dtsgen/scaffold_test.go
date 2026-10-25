package dtsgen

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// registrations builds root's main.ts like a profile does and returns the
// number of handlers it registers.
func registrations(t *testing.T, root string) int {
	t.Helper()
	bundle, err := compile.Build(compile.BuildOptions{Root: root, Entry: MainEntry, API: HostAPI.String()})
	if err != nil {
		t.Fatal(err)
	}
	program, err := goja.Compile("bundle.js", string(bundle.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	host := script.NewHost(clock.NewFake())
	t.Cleanup(func() { _ = host.Dispose() })
	if err := host.Activate(program); err != nil {
		t.Fatal(err)
	}
	return len(host.Registrations())
}

// A new script must not take over a key once a profile references it; its
// commented example must still be valid when the user enables it.
func TestMainTemplateBindsNoControl(t *testing.T) {
	if !strings.Contains(MainTemplate, "API "+HostAPI.String()) || !strings.Contains(MainTemplate, `from "padmate"`) {
		t.Fatalf("template does not name API %s or import padmate:\n%s", HostAPI, MainTemplate)
	}
	root := t.TempDir()
	if err := Scaffold(root, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := registrations(t, root); n != 0 {
		t.Fatalf("template registers %d handlers", n)
	}
	enabled := strings.Replace(MainTemplate, "// on(", "on(", 1)
	if enabled == MainTemplate {
		t.Fatal("template has no commented example binding")
	}
	if err := os.WriteFile(filepath.Join(root, MainEntry), []byte(enabled), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := registrations(t, root); n != 1 {
		t.Fatalf("enabled example registers %d handlers, want 1", n)
	}
}

func scaffoldRoot(t *testing.T) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, root
}

func TestScaffoldFSCreatesOnlyMissingFiles(t *testing.T) {
	dir, root := scaffoldRoot(t)
	const work = "// user work\n"
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(work), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := Project(HostAPI, MainEntry, []byte(MainTemplate), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, existing, err := ScaffoldFS(root, files)
	if err != nil {
		t.Fatal(err)
	}
	helpers := []string{"tsconfig.json", "padmate.d.ts", "padmate-globals.d.ts", "lib.padmate-goja.d.ts"}
	if !slices.Equal(created, helpers) || !slices.Equal(existing, []string{"main.ts"}) {
		t.Fatalf("created %v, existing %v", created, existing)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "main.ts")); err != nil || string(data) != work {
		t.Fatalf("main.ts overwritten: %q %v", data, err)
	}
	for _, f := range files[1:] {
		if data, err := os.ReadFile(filepath.Join(dir, f.Name)); err != nil || string(data) != string(f.Data) {
			t.Fatalf("%s not written: %v", f.Name, err)
		}
	}
	created, existing, err = ScaffoldFS(root, files)
	if err != nil || len(created) != 0 || len(existing) != len(files) {
		t.Fatalf("second scaffold: created %v, existing %v, %v", created, existing, err)
	}
}

// A link that already has a scaffold file's name counts as existing: the
// write never follows it, even to a missing target outside the root.
func TestScaffoldFSNeverFollowsLinks(t *testing.T) {
	dir, root := scaffoldRoot(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "padmate.d.ts")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(outside, "missing.json")
	if err := os.Symlink(target, filepath.Join(dir, "padmate.d.ts")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(missing, filepath.Join(dir, "tsconfig.json")); err != nil {
		t.Fatal(err)
	}
	files, err := Project(HostAPI, MainEntry, []byte(MainTemplate), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, existing, err := ScaffoldFS(root, files)
	if err != nil || !slices.Equal(existing, []string{"tsconfig.json", "padmate.d.ts"}) {
		t.Fatalf("existing %v, %v", existing, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "outside" {
		t.Fatal("wrote through a link", err)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("created a link's target", err)
	}
}

// The tsconfig names no entry: it is the same for every project and covers
// every script in the folder, so a script imported or created after it was
// written needs no change to it (ScaffoldFS never rewrites it).
func TestProjectConfigCoversEveryScript(t *testing.T) {
	var config []byte
	for _, entry := range []string{"main.ts", "brush.ts", "brush.js"} {
		files, err := Project(HostAPI, entry, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if files[0].Name != "tsconfig.json" || slices.ContainsFunc(files, func(f File) bool { return f.Name == entry }) {
			t.Fatalf("%s: helpers only expected: %v", entry, files)
		}
		if config == nil {
			config = files[0].Data
		} else if !bytes.Equal(files[0].Data, config) {
			t.Fatalf("%s: tsconfig depends on the entry:\n%s\n%s", entry, files[0].Data, config)
		}
	}
	var cfg struct {
		CompilerOptions map[string]any `json:"compilerOptions"`
		Files           []string       `json:"files"`
		Include         []string       `json:"include"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Files != nil || !slices.Equal(cfg.Include, []string{"**/*.ts", "**/*.js"}) || cfg.CompilerOptions["noEmit"] != true || cfg.CompilerOptions["allowJs"] != true {
		t.Fatalf("tsconfig %s", config)
	}
	for _, entry := range []string{"", ".", "..", "lib/main.ts", `..\main.ts`, "C:main.ts"} {
		if _, err := Project(HostAPI, entry, nil, nil, nil); err == nil {
			t.Fatalf("entry %q accepted", entry)
		}
	}
	if _, err := Project(Version{Major: 0, Minor: 7}, MainEntry, nil, nil, nil); err == nil {
		t.Fatal("incompatible API accepted")
	}
}

// tscProject runs tsc on the tsconfig.json in dir.
func tscProject(t *testing.T, tsc, dir string) (string, error) {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), "node", tsc, "--project", dir).CombinedOutput()
	return string(output), err
}

func writeScripts(t *testing.T, dir string, scripts map[string]string) {
	t.Helper()
	for name, source := range scripts {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A project type-checks with its own tsconfig whatever its entry, scripts
// added to the folder later are checked with the same declarations, and tsc
// writes no JavaScript next to the sources.
func TestProjectWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	const brush = `import { key, on } from "padmate";
on("south", "press", async () => { await key.tap("B"); });
`
	for name, source := range map[string]string{"main.ts": MainTemplate, "brush.ts": brush, "brush.js": brush} {
		t.Run(name, func(t *testing.T) {
			dir, root := scaffoldRoot(t)
			files, err := Project(HostAPI, name, []byte(source), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ScaffoldFS(root, files); err != nil {
				t.Fatal(err)
			}
			if output, err := tscProject(t, tsc, dir); err != nil {
				t.Fatalf("tsc: %v\n%s", err, output)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != len(files) {
				t.Fatalf("tsc wrote files: %v %v", entries, err)
			}
		})
	}

	// New script first, then imports: a TypeScript script, a JavaScript one
	// and a module in a subfolder are in the project, so a type error in a
	// later script is reported.
	t.Run("later scripts", func(t *testing.T) {
		dir, root := scaffoldRoot(t)
		files, err := Project(HostAPI, MainEntry, []byte(MainTemplate), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ScaffoldFS(root, files); err != nil {
			t.Fatal(err)
		}
		writeScripts(t, dir, map[string]string{
			"brush.ts":    brush + "import { size } from \"./lib/size\";\nconst n: number = size;\nvoid n;\n",
			"tools.js":    brush,
			"lib/size.ts": "export const size = 3;\n",
		})
		if output, err := tscProject(t, tsc, dir); err != nil {
			t.Fatalf("tsc: %v\n%s", err, output)
		}
		writeScripts(t, dir, map[string]string{"bad.ts": `import { key } from "padmate";
const tap: number = key.tap;
void tap;
`})
		output, err := tscProject(t, tsc, dir)
		if err == nil || !strings.Contains(output, "bad.ts") || !strings.Contains(output, "TS2322") {
			t.Fatalf("tsc did not check bad.ts: %v\n%s", err, output)
		}
	})
}
