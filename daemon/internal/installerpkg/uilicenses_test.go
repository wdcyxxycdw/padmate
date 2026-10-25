package installerpkg

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectUILicenses(t *testing.T) {
	app := t.TempDir()
	writeTree(t, app, map[string]string{
		"package.json": `{"name":"padmate","dependencies":{"a":"1","@s/b":"2"},"devDependencies":{"dev":"1"}}`,
		// a depends on c, which has no license file; b has two files.
		"node_modules/a/package.json":           `{"name":"a","version":"1.2.3","license":"MIT","dependencies":{"c":"^1"}}`,
		"node_modules/a/LICENSE":                "MIT for a",
		"node_modules/a/README.md":              "not a license",
		"node_modules/@s/b/package.json":        `{"name":"@s/b","version":"2.0.0","license":{"type":"ISC"}}`,
		"node_modules/@s/b/LICENSE.md":          "ISC for b",
		"node_modules/@s/b/NOTICE":              "notice of b",
		"node_modules/c/package.json":           `{"name":"c","version":"0.1.0"}`,
		"node_modules/dev/package.json":         `{"name":"dev","version":"9.9.9","license":"MIT"}`,
		"node_modules/dev/LICENSE":              "only the build needs dev",
		"node_modules/a/node_modules/x/LICENSE": "nested and not asked for",
	})
	files, err := collectUILicenses(app)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var names []string
	for _, f := range files {
		got[f.Name] = string(f.Data)
		names = append(names, f.Name)
	}
	if !slices.IsSorted(names) {
		t.Errorf("not in order: %v", names)
	}
	for name, want := range map[string]string{
		"licenses/ui/a@1.2.3/LICENSE":       "MIT for a",
		"licenses/ui/@s/b@2.0.0/LICENSE.md": "ISC for b",
		"licenses/ui/@s/b@2.0.0/NOTICE":     "notice of b",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	for name := range got {
		if strings.Contains(name, "dev@") || strings.Contains(name, "README") || strings.Contains(name, "/x/") {
			t.Errorf("unexpected file %s", name)
		}
	}
	index := got["licenses/THIRD-PARTY-UI.txt"]
	for _, want := range []string{"a@1.2.3", "license: MIT", "@s/b@2.0.0", "license: ISC", "c@0.1.0", "no license file"} {
		if !strings.Contains(index, want) {
			t.Errorf("index lacks %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, "dev@") {
		t.Errorf("a development dependency is listed:\n%s", index)
	}
}

func TestCollectUILicensesNeedsInstalledPackages(t *testing.T) {
	app := t.TempDir()
	writeTree(t, app, map[string]string{"package.json": `{"dependencies":{"missing":"1"}}`})
	if _, err := collectUILicenses(app); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("an uninstalled dependency: %v", err)
	}
	if _, err := collectUILicenses(t.TempDir()); err == nil {
		t.Error("no package.json passed")
	}
	writeTree(t, app, map[string]string{"package.json": `{"dependencies":{"../evil":"1"}}`})
	if _, err := collectUILicenses(app); err == nil {
		t.Error("a dependency name that leaves node_modules passed")
	}
}

// The real application: the four runtime dependencies are bundled into the
// window, and their licenses come with it.
func TestCollectUILicensesOfTheApp(t *testing.T) {
	app := filepath.Join("..", "..", "..", "app")
	if _, err := os.Stat(filepath.Join(app, "node_modules", "react")); err != nil {
		t.Skip("run task app:install first")
	}
	files, err := collectUILicenses(app)
	if err != nil {
		t.Fatal(err)
	}
	index := ""
	for _, f := range files {
		if f.Name == "licenses/THIRD-PARTY-UI.txt" {
			index = string(f.Data)
		}
	}
	for _, name := range []string{"react@", "react-dom@", "three@", "zustand@", "scheduler@", "electron-updater@"} {
		if !strings.Contains(index, name) {
			t.Errorf("the UI's runtime dependency %s is not listed:\n%s", name, index)
		}
	}
}

// npm keeps a copy of a package next to the package that needs another version
// of it (electron-updater wants semver 7 where the tools of the build have
// semver 6 at the top). The window bundles the copy its package finds, as
// Node would: its license is the one that ships.
func TestCollectUILicensesResolvesNestedCopies(t *testing.T) {
	app := t.TempDir()
	writeTree(t, app, map[string]string{
		"package.json":                                     `{"dependencies":{"up":"1","other":"1"}}`,
		"node_modules/up/package.json":                     `{"name":"up","version":"1.0.0","license":"MIT","dependencies":{"semver":"~7","shared":"1"}}`,
		"node_modules/up/LICENSE":                          "MIT for up",
		"node_modules/up/node_modules/semver/package.json": `{"name":"semver","version":"7.7.4","license":"ISC"}`,
		"node_modules/up/node_modules/semver/LICENSE":      "seven",
		"node_modules/semver/package.json":                 `{"name":"semver","version":"6.3.1","license":"ISC"}`,
		"node_modules/semver/LICENSE":                      "six, a dependency of a tool of the build",
		"node_modules/shared/package.json":                 `{"name":"shared","version":"2.0.0","license":"MIT"}`,
		"node_modules/shared/LICENSE":                      "shared at the top",
		// other needs the same two: its own copy of semver 7.7.4 (same version
		// as up's) and the top-level shared.
		"node_modules/other/package.json":                     `{"name":"other","version":"1.0.0","license":"MIT","dependencies":{"semver":"~7","shared":"1"}}`,
		"node_modules/other/node_modules/semver/package.json": `{"name":"semver","version":"7.7.4","license":"ISC"}`,
		"node_modules/other/node_modules/semver/LICENSE":      "seven",
	})
	files, err := collectUILicenses(app)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	count := map[string]int{}
	for _, f := range files {
		got[f.Name] = string(f.Data)
		count[f.Name]++
	}
	if got["licenses/ui/semver@7.7.4/LICENSE"] != "seven" {
		t.Errorf("the semver that up needs is not the one listed: %v", got)
	}
	for name := range got {
		if strings.Contains(name, "semver@6.3.1") {
			t.Errorf("the top-level semver, which no package of the window needs, is listed: %s", name)
		}
	}
	if got["licenses/ui/shared@2.0.0/LICENSE"] != "shared at the top" {
		t.Errorf("a package found at the top is not listed: %v", got)
	}
	for name, n := range count {
		if n > 1 {
			t.Errorf("%s is listed %d times", name, n)
		}
	}
	index := got["licenses/THIRD-PARTY-UI.txt"]
	if strings.Count(index, "\nsemver@7.7.4\n") != 1 || strings.Contains(index, "semver@6.3.1") {
		t.Errorf("the index lists semver wrongly:\n%s", index)
	}
}

// electron-updater is bundled into the main process, so its license and those
// of what it needs are shipped (PKG-10).
func TestCollectUILicensesListsTheUpdater(t *testing.T) {
	app := filepath.Join("..", "..", "..", "app")
	if _, err := os.Stat(filepath.Join(app, "node_modules", "electron-updater")); err != nil {
		t.Skip("run task app:install first")
	}
	files, err := collectUILicenses(app)
	if err != nil {
		t.Fatal(err)
	}
	index := ""
	for _, f := range files {
		if f.Name == "licenses/THIRD-PARTY-UI.txt" {
			index = string(f.Data)
		}
	}
	for _, name := range []string{"electron-updater@6.8.9", "builder-util-runtime@", "fs-extra@", "js-yaml@", "lazy-val@", "semver@7."} {
		if !strings.Contains(index, name) {
			t.Errorf("%s is bundled into the main process and not listed:\n%s", name, index)
		}
	}
	if strings.Contains(index, "semver@6.") {
		t.Errorf("the semver of the build tools is listed, not the one electron-updater bundles:\n%s", index)
	}
}
