package installerpkg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMainBundleRequires(t *testing.T) {
	for _, c := range []struct {
		name string
		code string
		want []string
	}{
		{"nothing", "console.log(1)", nil},
		{"node's own modules and electron", `const a = require("fs"), b = require('node:path'), c = require("fs/promises"), d = require("electron"), e = require("child_process"); require("node:test/reporters")`, nil},
		{"a package", `const u = require("electron-updater");`, []string{"electron-updater"}},
		{"each package once, in order", `require("fs-extra"); require("os"); require("electron-updater"); require("fs-extra")`, []string{"fs-extra", "electron-updater"}},
		{"a relative file", `require("./chunk.js")`, []string{"./chunk.js"}},
		{"a scoped package", "require(`@scope/pkg`)", []string{"@scope/pkg"}},
		{"a dynamic require", `require(name)`, []string{"<dynamic>"}},
		{"a concatenated require", `require("fs" + x)`, []string{"<dynamic>"}},
		{"spaces inside", `require( "left-pad" )`, []string{"left-pad"}},
		{"a method named require", `module.require("x"); prerequire("y"); obj.require("z"); $require("w")`, nil},
		{"at the start of the file", `require("lodash")`, []string{"lodash"}},
		{"a node: name that is not a builtin", `require("node:left-pad")`, []string{"node:left-pad"}},
	} {
		if got := MainBundleRequires([]byte(c.code)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The bundle electron-vite built from this working tree, when there is one.
func TestMainBundleOfTheApp(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "app", "out", "main", "index.js"))
	if err != nil {
		t.Skip("run task app:build first")
	}
	if got := MainBundleRequires(data); len(got) != 0 {
		t.Errorf("the main process requires what the installed application does not have: %v (add it to externalizeDepsPlugin's exclude in electron.vite.config.ts)", got)
	}
}

func FuzzMainBundleRequires(f *testing.F) {
	for _, seed := range []string{"", `require("fs")`, `require(`, `require("`, "require(`x`)", `x.require("a")require(b)`, "require(\"a\"\n)", `require(()))`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, code []byte) {
		got := MainBundleRequires(code)
		seen := map[string]bool{}
		for _, name := range got {
			if seen[name] {
				t.Fatalf("%q listed twice", name)
			}
			seen[name] = true
			if name != "<dynamic>" && (isNodeModule(name) || name == "electron") {
				t.Fatalf("%q is allowed but listed", name)
			}
		}
	})
}

func TestCheckUpdateConfig(t *testing.T) {
	good := "provider: github\nowner: wdcyxxycdw\nrepo: padmate\nupdaterCacheDirName: padmate-updater\n"
	if got := CheckUpdateConfig([]byte(good)); len(got) != 0 {
		t.Errorf("the file electron-builder writes: %v", got)
	}
	// A signed build adds publisherName, an unsigned one does not.
	if got := CheckUpdateConfig([]byte(good + "publisherName:\n  - CN=padmate\n")); len(got) != 0 {
		t.Errorf("with publisherName: %v", got)
	}
	for name, c := range map[string]struct{ text, want string }{
		"empty":           {"", "provider"},
		"another source":  {strings.Replace(good, "github", "generic", 1), "provider"},
		"no owner":        {strings.Replace(good, "owner: wdcyxxycdw\n", "", 1), "owner"},
		"an empty repo":   {strings.Replace(good, "repo: padmate", "repo: ", 1), "repo"},
		"another folder":  {strings.Replace(good, "padmate-updater", "other-updater", 1), "updaterCacheDirName"},
		"no folder":       {strings.Replace(good, "updaterCacheDirName: padmate-updater\n", "", 1), "updaterCacheDirName"},
		"a quoted source": {strings.Replace(good, "github", `"github"`, 1), ""},
	} {
		got := strings.Join(CheckUpdateConfig([]byte(c.text)), "\n")
		if c.want == "" {
			if got != "" {
				t.Errorf("%s: %s", name, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not name %q", name, got, c.want)
		}
	}
}

func FuzzCheckUpdateConfig(f *testing.F) {
	f.Add([]byte("provider: github\nowner: a\nrepo: b\nupdaterCacheDirName: padmate-updater\n"))
	f.Add([]byte("\x00\xff:\n: \n"))
	f.Fuzz(func(t *testing.T, data []byte) { _ = CheckUpdateConfig(data) })
}

// CheckTree reads the update parts of an installation: the main process in
// app.asar must need nothing installed, and resources\app-update.yml must name
// the source and the cache folder the installer cleans up.
func TestCheckTreeReadsTheUpdateParts(t *testing.T) {
	tag := mustTag("v0.14.0")
	build := func(bundle, updateYML string) string {
		root := t.TempDir()
		files := map[string]string{
			"package.json":      `{"name":"padmate","version":"0.14.0","main":"out/main/index.js"}`,
			"out/main/index.js": bundle,
		}
		writeFile := func(name string, data []byte) {
			t.Helper()
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeFile("resources/app.asar", buildAsar(t, files, []string{"package.json", "out/main/index.js"}))
		if updateYML != "" {
			writeFile("resources/app-update.yml", []byte(updateYML))
		}
		return root
	}
	good := "provider: github\nowner: o\nrepo: r\nupdaterCacheDirName: padmate-updater\n"
	problems := func(root string) string {
		return strings.Join(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag}), "\n")
	}

	ok := problems(build(`const e = require("electron"); require("node:fs");`, good))
	for _, bad := range []string{"app.asar", "app-update.yml", "requires"} {
		if strings.Contains(ok, bad) {
			t.Errorf("a good main process and update file were reported (%s):\n%s", bad, ok)
		}
	}

	got := problems(build(`require("electron-updater")`, good))
	if !strings.Contains(got, `resources/app.asar: out/main/index.js requires "electron-updater"`) {
		t.Errorf("a bare require of a package was not reported:\n%s", got)
	}

	got = problems(build(`require("fs")`, ""))
	if !strings.Contains(got, "resources/app-update.yml") {
		t.Errorf("a missing app-update.yml was not reported:\n%s", got)
	}

	got = problems(build(`require("fs")`, strings.Replace(good, "github", "generic", 1)))
	if !strings.Contains(got, "resources/app-update.yml: provider") {
		t.Errorf("another update source was not reported:\n%s", got)
	}
}

// An installation from before the automatic update has no
// resources\app-update.yml: the first installer of an upgrade test is such a
// release (the update-style step and `task test:installer INSTALLER=<old>
// UPGRADE=<new>` check both trees). NoUpdateConfig leaves out that one file;
// the main process is held to the same rule, as an old one needs nothing
// installed either.
func TestCheckTreeOfAReleaseBeforeTheUpdate(t *testing.T) {
	tag := mustTag("v0.14.0")
	build := func(bundle string) string {
		root := t.TempDir()
		files := map[string]string{
			"package.json":      `{"name":"padmate","version":"0.14.0","main":"out/main/index.js"}`,
			"out/main/index.js": bundle,
		}
		path := filepath.Join(root, "resources", "app.asar")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buildAsar(t, files, []string{"package.json", "out/main/index.js"}), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	problems := func(root string, old bool) string {
		return strings.Join(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag, NoUpdateConfig: old}), "\n")
	}

	root := build(`require("electron")`)
	if got := problems(root, false); !strings.Contains(got, "resources/app-update.yml") {
		t.Errorf("a missing app-update.yml was not reported for a current release:\n%s", got)
	}
	if got := problems(root, true); strings.Contains(got, "app-update.yml") {
		t.Errorf("a release before the update was asked for app-update.yml:\n%s", got)
	}

	// It does not excuse the rest.
	if got := problems(build(`require("react")`), true); !strings.Contains(got, `requires "react"`) {
		t.Errorf("a bare require was not reported for a release before the update:\n%s", got)
	}
	// And a file that is there is still read.
	if err := os.WriteFile(filepath.Join(root, "resources", "app-update.yml"), []byte("provider: generic\nowner: o\nrepo: r\nupdaterCacheDirName: padmate-updater\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := problems(root, true); !strings.Contains(got, "resources/app-update.yml: provider") {
		t.Errorf("an app-update.yml that is there was not read:\n%s", got)
	}
}
