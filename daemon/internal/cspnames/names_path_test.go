package cspnames_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp/csptest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration/cspint"
)

// khcRows is a shortcut file of 20 commands: 4 that the synthetic menu
// resource names, and 16 of the bundled table.
func khcRows(t *testing.T) []csptest.KHCRow {
	t.Helper()
	rows := []csptest.KHCRow{
		{Type: "basiccommand", Cmd: "filenew", Key: "N", Mod: 4},
		{Type: "basiccommand", Cmd: "undo", Key: "Z", Mod: 4},
		{Type: "advancedcommand", Cmd: "exportpng", Key: "NULL"},
		{Type: "advancedcommand", Cmd: "filequit", Key: "NULL"},
	}
	bt, err := cspnames.BundledTable()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(bt.Commands))
	for id := range bt.Commands {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids[:16] {
		rows = append(rows, csptest.KHCRow{Type: "advancedcommand", Cmd: id, Key: "NULL"})
	}
	return rows
}

// TestRealLayoutNamesThroughCspint is the whole path of CSP 5's real layout:
// the uninstall entry's InstallLocation is the folder above "CLIP STUDIO 1.5"
// and DisplayIcon names the executable. The install is found, its menu
// resource read, and the listing has names, not command ids.
func TestRealLayoutNamesThroughCspint(t *testing.T) {
	celsys := t.TempDir()
	exeDir := cspnames.WriteSyntheticInstall(t, celsys)
	data := t.TempDir()
	csptest.MakeKHC(t, data, khcRows(t))

	in, ok := cspfile.InstallFromEntry("CLIP STUDIO PAINT 5.1.5", celsys, filepath.Join(exeDir, cspfile.ExeName)+",0")
	if !ok || in.Root != exeDir {
		t.Fatalf("install = %+v, %v", in, ok)
	}
	namer := &cspint.NamesNamer{InstallDir: func() (string, error) { return in.Root, nil }}
	integ := cspint.New(cspint.Options{PadmateDir: t.TempDir(), DataDir: func() string { return data }, Namer: namer})
	for _, lang := range []string{"en", "zh-CN"} {
		l, err := integ.Functions(lang)
		if err != nil {
			t.Fatal(err)
		}
		var ids int
		names := map[string]string{}
		for _, f := range l.Functions {
			names[f.ID] = f.Name
			if isID(f.Name) || f.Name == "" {
				ids++
			}
		}
		// The synthetic English resource prefixes its names with "en ".
		if want := map[string]string{"en": "en New", "zh-CN": "New"}[lang]; names["command:basiccommand/filenew"] != want {
			t.Errorf("%s: filenew = %q, want %q", lang, names["command:basiccommand/filenew"], want)
		}
		if ids != 0 {
			t.Errorf("%s: %d of %d functions are named by their ID", lang, ids, len(l.Functions))
		}
		if p := namer.NamesProblem(lang); p != "" {
			t.Errorf("%s: names problem %q", lang, p)
		}
	}
}

// TestBundledTableAloneNamesTableCommands: with no installation at all the
// bundled table still names its commands, the problem is reported, and the
// share of functions that fall back to IDs stays small. It fails if most of
// a set falls back to IDs again.
func TestBundledTableAloneNamesTableCommands(t *testing.T) {
	data := t.TempDir()
	rows := khcRows(t)
	csptest.MakeKHC(t, data, rows)
	namer := &cspint.NamesNamer{InstallDir: func() (string, error) { return "", nil }}
	integ := cspint.New(cspint.Options{PadmateDir: t.TempDir(), DataDir: func() string { return data }, Namer: namer})
	l, err := integ.Functions("en")
	if err != nil {
		t.Fatal(err)
	}
	fallback := 0
	for _, f := range l.Functions {
		if isID(f.Name) {
			fallback++
		}
	}
	// Only the four menu commands may be unnamed without an installation.
	if fallback > 4 || fallback*100 > len(l.Functions)*25 {
		t.Errorf("%d of %d functions fall back to IDs", fallback, len(l.Functions))
	}
	if p := namer.NamesProblem("en"); p != integration.NamesInstallNotFound {
		t.Errorf("problem = %q", p)
	}
}

// TestRealMachine reads this machine's CSP, read-only: the registry for the
// installation (cspfile.FindInstallDir) and its menu resource. It runs only
// with PADMATE_CSP_INSTALL_REAL=1. With PADMATE_CSP_DATA naming a copy of a
// CSP data folder (never the live one) it also lists the functions through
// cspint and reports the tree.
func TestRealMachine(t *testing.T) {
	if os.Getenv("PADMATE_CSP_INSTALL_REAL") != "1" {
		t.Skip("PADMATE_CSP_INSTALL_REAL is not 1")
	}
	in, err := cspfile.FindInstallDir()
	if err != nil {
		t.Fatalf("FindInstallDir: %v", err)
	}
	t.Logf("install: %s (resource %s)", in.Root, in.Resource)
	for _, lang := range []string{"zh-CN", "en"} {
		n, err := cspnames.Load(in.Root, lang)
		if err != nil {
			t.Fatalf("Load(%s): %v", lang, err)
		}
		t.Logf("%s: %d menu names", lang, n.MenuCommands())
		if n.MenuCommands() < 400 {
			t.Errorf("%s: %d menu names, want at least 400", lang, n.MenuCommands())
		}
	}
	data := os.Getenv("PADMATE_CSP_DATA")
	if data == "" {
		return
	}
	namer := &cspint.NamesNamer{InstallDir: func() (string, error) { return in.Root, nil }}
	integ := cspint.New(cspint.Options{PadmateDir: t.TempDir(), DataDir: func() string { return data }, Namer: namer})
	for _, lang := range []string{"zh-CN", "en"} {
		l, err := integ.Functions(lang)
		if err != nil {
			t.Fatal(err)
		}
		ids := 0
		for _, f := range l.Functions {
			if f.Kind == integration.KindCommand && isID(f.Name) {
				ids++
			}
		}
		if lang == "en" {
			inOther := map[string]bool{}
			for _, c := range l.Tree[len(l.Tree)-1].Children {
				inOther[c.Function] = true
			}
			var tree []string
			for _, f := range l.Functions {
				if f.Kind == integration.KindCommand && isID(f.Name) && !inOther[f.ID] {
					tree = append(tree, f.ID)
				}
			}
			t.Logf("placed in the tree but named by ID (%d): %v", len(tree), tree)
		}
		t.Logf("%s: %d functions, %d commands named by ID, problem %q", lang, len(l.Functions), ids, namer.NamesProblem(lang))
		var show func(ns []integration.TreeNode, depth int)
		show = func(ns []integration.TreeNode, depth int) {
			for _, n := range ns {
				if depth <= 1 {
					t.Logf("%s%s [%s] fn=%v children=%d", strings.Repeat("  ", depth), n.Name, n.ID, n.Function != "", len(n.Children))
				}
				show(n.Children, depth+1)
			}
		}
		show(l.Tree, 0)
		if ids*10 > 817 {
			t.Errorf("%s: %d commands fall back to IDs", lang, ids)
		}
		if lang == "zh-CN" {
			var file []string
			var dump func(ns []integration.TreeNode, depth int)
			dump = func(ns []integration.TreeNode, depth int) {
				for _, n := range ns {
					file = append(file, strings.Repeat("-", depth)+n.Name)
					dump(n.Children, depth+1)
				}
			}
			dump(l.Tree[0].Children[0].Children, 0)
			t.Logf("File: %v", file)
			other := l.Tree[len(l.Tree)-1]
			if other.ID == integration.OtherNode {
				var names []string
				for _, c := range other.Children {
					names = append(names, c.Name)
				}
				t.Logf("Other (%d): %v", len(names), names)
			}
		}
	}
}

// isID reports whether a function is named by its command ID: a name like
// "advancedcommand/filenew". (A real name may contain a slash.)
func isID(name string) bool {
	return strings.HasPrefix(name, "advancedcommand/") || strings.HasPrefix(name, "basiccommand/")
}
