package installerpkg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagedName(t *testing.T) {
	for name, want := range map[string]string{
		"padmated.exe":                     "resources/bin/padmated.exe",
		"padmatectl.exe":                   "resources/bin/padmatectl.exe",
		"SDL3.dll":                         "resources/bin/SDL3.dll",
		"LICENSE":                          "resources/LICENSE",
		"README.md":                        "resources/README.md",
		"README.en.md":                     "resources/README.en.md",
		"docs/guide/00-index.en.md":        "resources/docs/guide/00-index.en.md",
		"examples/profiles/basic.json":     "resources/examples/profiles/basic.json",
		"licenses/go/x@v1/LICENSE":         "resources/licenses/go/x@v1/LICENSE",
		"schema/profile.schema.json":       "resources/schema/profile.schema.json",
		"docs/quickstart.en.md":            "resources/docs/quickstart.en.md",
		"licenses/THIRD-PARTY.txt":         "resources/licenses/THIRD-PARTY.txt",
		"licenses/SDL3-LICENSE.txt":        "resources/licenses/SDL3-LICENSE.txt",
		"examples/scripts/tabmate/main.ts": "resources/examples/scripts/tabmate/main.ts",
	} {
		if got, ok := stagedName(name); !ok || got != want {
			t.Errorf("stagedName(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if got, ok := stagedName("README-PORTABLE.md"); ok {
		t.Errorf("README-PORTABLE.md is staged as %q: it is about the zip", got)
	}
	for _, name := range []string{"docs/build-installer.md", "docs/adr/0060-installer.md", "examples/scripts/tabmate/README.md"} {
		if got, ok := stagedName(name); ok {
			t.Errorf("%s is staged as %q: it is not a user document", name, got)
		}
	}
}

func TestPrepareStage(t *testing.T) {
	dir := t.TempDir()
	// A new folder is made.
	fresh := filepath.Join(dir, "a", "b")
	if err := prepareStage(fresh); err != nil {
		t.Fatal(err)
	}
	// An empty folder is used.
	if err := prepareStage(fresh); err != nil {
		t.Fatal(err)
	}
	// A folder with other files is not touched.
	foreign := filepath.Join(dir, "foreign")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(foreign, "mine.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareStage(foreign); err == nil {
		t.Error("a folder that is not the stage was emptied or accepted")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the foreign file is gone: %v", err)
	}
	// A folder with the marker is emptied, its subfolders too.
	made := filepath.Join(dir, "made")
	if err := writeStaged(made, stageMarker, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writeStaged(made, "resources/bin/old.exe", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := prepareStage(made); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(made); err != nil || len(entries) != 0 {
		t.Errorf("stage not emptied: %v, %v", entries, err)
	}
}

func TestWriteStagedRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "/abs", "../up", "a/../../up", `a\b`} {
		if err := writeStaged(dir, name, []byte("x")); err == nil {
			t.Errorf("writeStaged(%q) succeeded", name)
		}
	}
}
