package cspnames

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
)

// TestRealInstall runs against a real CSP install folder, read-only. It is
// skipped unless PADMATE_CSP_INSTALL names the folder that holds
// CLIPStudioPaint.exe and resource\. With PADMATE_CSP_KHC set to a copy of a
// default.khc it also measures how many of its commands get a name.
func TestRealInstall(t *testing.T) {
	dir := os.Getenv("PADMATE_CSP_INSTALL")
	if dir == "" {
		t.Skip("PADMATE_CSP_INSTALL is not set")
	}
	for _, lang := range []string{"zh-CN", "en"} {
		t.Run(lang, func(t *testing.T) {
			n, err := Load(dir, lang)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if n.MenuCommands() < 500 {
				t.Errorf("menu names = %d, want at least 500", n.MenuCommands())
			}
			t.Logf("menu names: %d, table names: %d, areas: %d", n.MenuCommands(), len(n.table), len(n.Layout()))

			want := map[string][2]string{ // id: zh, en
				"filenew":          {"新建", "New"},
				"filesaveas":       {"另存为", "Save as"},
				"canvasscrolldown": {"向下滚动", "Scroll to down"},
				"toolsizeplus":     {"加大笔刷尺寸", "Increase brush size"},
				testGUID:           {"马赛克", "Mosaic"},
			}
			for id, names := range want {
				name, path, cat, ok := n.Command("advancedcommand", id)
				exp := names[0]
				if lang == "en" {
					exp = names[1]
				}
				if !ok || name != exp {
					t.Errorf("%s = %q, %v; want %q", id, name, ok, exp)
				}
				t.Logf("%s = %q path %v category %q", id, name, path, cat)
			}
			if _, path, cat, _ := n.Command("advancedcommand", "filesaveasproj"); len(path) != 2 || cat == "" {
				t.Errorf("filesaveasproj path %v category %q, want a two level menu path", path, cat)
			}
			for _, a := range n.Layout() {
				t.Logf("area %s = %s", a.ID, a.Name)
			}

			khc := os.Getenv("PADMATE_CSP_KHC")
			if khc == "" {
				return
			}
			set, err := csp.ReadKHC(khc)
			if err != nil {
				t.Fatal(err)
			}
			var menu, tab, plug, none int
			var unnamed []string
			for _, c := range set.Commands {
				key := strings.ToLower(c.Command)
				switch _, inMenu := n.menu[key]; {
				case inMenu:
					menu++
				case hasKey(n.table, key):
					tab++
				default:
					if _, _, _, ok := n.Command(c.Type, c.Command); ok {
						plug++
					} else {
						none++
						unnamed = append(unnamed, c.Command)
					}
				}
			}
			sort.Strings(unnamed)
			t.Logf("khc commands %d: menu %d, table %d, plug-ins %d, unnamed %d", len(set.Commands), menu, tab, plug, none)
			t.Logf("unnamed: %v", unnamed)
			if menu < 440 {
				t.Errorf("menu names for the khc = %d, want at least 440", menu)
			}
			if named := menu + tab + plug; named*100 < len(set.Commands)*93 {
				t.Errorf("named %d of %d", named, len(set.Commands))
			}
		})
	}
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}
