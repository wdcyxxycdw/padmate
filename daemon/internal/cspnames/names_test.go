package cspnames

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

const testGUID = "EAB74AE0-BE07-4805-9B28-9D66C8131A3B"

func testPlugin() map[string][]byte {
	dll := synthPE(map[uint16]map[uint32]string{
		0x804: {96: "效果", 97: "马赛克"},
		0x009: {96: "Effect", 97: "Mosaic"},
	})
	dll = append(dll, []byte("\x00junk "+testGUID+" junk\x00")...)
	exp := synthPE(map[uint16]map[uint32]string{0x9: {100: "x", 102: "psd|psd"}})
	exp = append(exp, []byte("\x003D617F46-7D99-44b2-80C1-2BEF6F9EBE33\x00")...)
	return map[string][]byte{"Mosaic.dll": dll, "ExportPSD.dll": exp, "notes.txt": []byte(testGUID)}
}

func twoLanguages() map[string][]byte {
	return map[string][]byte{
		"chinese_sc": synthResource(),
		"english":    synthResource(func(s *synth) { s.prefix = "en " }),
	}
}

func TestNamesLookupOrder(t *testing.T) {
	n, err := loadFS(synthInstall(twoLanguages(), testPlugin()), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if n.MenuCommands() != 6 {
		t.Errorf("MenuCommands = %d, want 6", n.MenuCommands())
	}
	tests := []struct {
		typ, id  string
		name     string
		path     []string
		category string
		ok       bool
	}{
		// 1. The menu resource, case-insensitive; the first menu wins. The
		// Chinese test resource has English menu titles, which match no
		// group of the table.
		{"advancedcommand", "FileNew", "New", []string{"File"}, "menu10", true},
		{"advancedcommand", "exportpng", "PNG", []string{"File", "Export"}, "menu10", true},
		{"basiccommand", "undo", "Undo", []string{"Edit"}, "menu20", true},
		// A menu without a name: no path and no category.
		{"advancedcommand", "noname", "Orphan", nil, "", true},
		// 2. The bundled table.
		{"advancedcommand", "canvasscrolldown", "向下滚动", []string{"画布", "视图"}, "canvas", true},
		{"advancedcommand", "CANVASSCROLLDOWN", "向下滚动", []string{"画布", "视图"}, "canvas", true},
		// 3. A plug-in DLL, by the GUID with or without a suffix.
		{"advancedcommand", testGUID, "马赛克", []string{"滤镜", "效果"}, "filter", true},
		{"advancedcommand", strings.ToLower(testGUID) + "-2FB51A87-53F9-44D0-92A0-B0E0B2AE3AD7", "马赛克", []string{"滤镜", "效果"}, "filter", true},
		// Not found: the caller shows the id.
		{"advancedcommand", "nosuchcommand", "", nil, "", false},
		{"advancedcommand", "3D617F46-7D99-44b2-80C1-2BEF6F9EBE33", "", nil, "", false}, // an export format, not a name
		{"advancedcommand", "00000000-0000-0000-0000-000000000000", "", nil, "", false},
		{"advancedcommand", "", "", nil, "", false},
	}
	for _, tt := range tests {
		name, path, cat, ok := n.Command(tt.typ, tt.id)
		if name != tt.name || !reflect.DeepEqual(path, tt.path) || cat != tt.category || ok != tt.ok {
			t.Errorf("Command(%q) = %q, %v, %q, %v; want %q, %v, %q, %v", tt.id, name, path, cat, ok, tt.name, tt.path, tt.category, tt.ok)
		}
	}
}

func TestNamesEnglishAndFallback(t *testing.T) {
	// The English UI uses the english resource and the English table.
	n, err := loadFS(synthInstall(twoLanguages(), testPlugin()), "en")
	if err != nil {
		t.Fatal(err)
	}
	if name, _, _, ok := n.Command("advancedcommand", "filenew"); !ok || name != "en New" {
		t.Errorf("filenew = %q, %v", name, ok)
	}
	if name, path, _, ok := n.Command("advancedcommand", "canvasscrolldown"); !ok || name != "Scroll to down" || !reflect.DeepEqual(path, []string{"Canvas", "View"}) {
		t.Errorf("canvasscrolldown = %q, %v, %v", name, path, ok)
	}
	if name, _, _, ok := n.Command("advancedcommand", testGUID); !ok || name != "Mosaic" {
		t.Errorf("plug-in = %q, %v", name, ok)
	}

	// A language whose folder is missing falls back to English.
	only := synthInstall(map[string][]byte{"english": twoLanguages()["english"]}, nil)
	n, err = loadFS(only, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if name, _, _, ok := n.Command("advancedcommand", "filenew"); !ok || name != "en New" {
		t.Errorf("fallback filenew = %q, %v", name, ok)
	}
	if name, _, _, ok := n.Command("advancedcommand", "canvasscrolldown"); !ok || name != "向下滚动" {
		t.Errorf("table in zh = %q, %v", name, ok)
	}
}

func TestNamesBrokenResource(t *testing.T) {
	cases := map[string]fs.FS{
		"no resource":  synthInstall(nil, testPlugin()),
		"garbage":      synthInstall(map[string][]byte{"chinese_sc": []byte("garbage"), "english": []byte("garbage")}, testPlugin()),
		"no commands":  synthInstall(map[string][]byte{"chinese_sc": synthResource(func(s *synth) { s.noCommands = true })}, testPlugin()),
		"oversize":     synthInstall(map[string][]byte{"chinese_sc": make([]byte, maxResourceSize+1)}, testPlugin()),
		"empty folder": synthInstall(nil, nil),
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			n, err := loadFS(fsys, "zh-CN")
			if err == nil {
				t.Error("want the error for logging")
			}
			if n == nil {
				t.Fatal("Names is nil")
			}
			if name == "garbage" && !errors.Is(err, ErrUnknownFormat) {
				t.Errorf("err = %v, want ErrUnknownFormat", err)
			}
			if got, _, _, ok := n.Command("advancedcommand", "canvasscrolldown"); !ok || got != "向下滚动" {
				t.Errorf("table lookup = %q, %v", got, ok)
			}
			if _, _, _, ok := n.Command("advancedcommand", "filenew"); ok {
				t.Error("menu command found without the resource")
			}
			if n.MenuCommands() != 0 {
				t.Errorf("MenuCommands = %d", n.MenuCommands())
			}
			if len(n.Categories()) == 0 {
				t.Error("table categories are missing")
			}
		})
	}
}

func TestNamesCategories(t *testing.T) {
	n, err := loadFS(synthInstall(map[string][]byte{"english": synthResource()}, nil), "en")
	if err != nil {
		t.Fatal(err)
	}
	cats := n.Categories()
	// Menus come first in resource order. File and Edit borrow the ids of
	// the table's groups of the same name; the menu with id 30 has no name.
	if len(cats) < 3 || cats[0] != (Category{"file", "File"}) || cats[1] != (Category{"edit", "Edit"}) {
		t.Fatalf("categories = %v", cats)
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if c.ID == "" || c.Name == "" || seen[c.ID] {
			t.Errorf("bad or repeated category %+v", c)
		}
		seen[c.ID] = true
		for _, r := range c.ID {
			if r > 127 {
				t.Errorf("category id %q is not ASCII", c.ID)
			}
		}
	}
	for _, id := range []string{"canvas", "tooloptionpalette", "other"} {
		if !seen[id] {
			t.Errorf("category %q missing from %v", id, cats)
		}
	}
	// The result is a copy.
	cats[0].Name = "changed"
	if n.Categories()[0].Name == "changed" {
		t.Error("Categories returns the internal slice")
	}
}

func TestMenuCategoryWithoutTableMatch(t *testing.T) {
	// A top-level menu that matches no group of the table gets a category
	// id made of the id of the menu.
	n, err := loadFS(synthInstall(map[string][]byte{"english": synthResource(func(s *synth) { s.prefix = "Zz " })}, nil), "en")
	if err != nil {
		t.Fatal(err)
	}
	cats := n.Categories()
	if len(cats) < 2 || cats[0] != (Category{"menu10", "Zz File"}) || cats[1] != (Category{"menu20", "Zz Edit"}) {
		t.Errorf("categories = %v", cats)
	}
	if _, _, cat, _ := n.Command("advancedcommand", "undo"); cat != "menu20" {
		t.Errorf("category = %q", cat)
	}
}

func TestBundledTable(t *testing.T) {
	bt, err := BundledTable()
	if err != nil {
		t.Fatal(err)
	}
	if bt.CSP != "5.1.5" {
		t.Errorf("CSP = %q", bt.CSP)
	}
	if len(bt.Commands) < 400 {
		t.Errorf("%d commands", len(bt.Commands))
	}
	for id, c := range bt.Commands {
		if len(c.PathZH) != len(c.PathEN) {
			t.Errorf("%s: paths differ in length", id)
		}
		for _, s := range append(append([]string{c.ZH, c.EN}, c.PathZH...), c.PathEN...) {
			if strings.ContainsAny(s, "\x00�") || strings.Contains(s, "^0%") {
				t.Errorf("%s: suspicious text %q", id, s)
			}
		}
	}
}

func TestParseTableErrors(t *testing.T) {
	bad := []string{
		``,
		`{"commands":{"A":{"zh":"a","en":"a"}}}`,
		`{"commands":{"a":{"zh":"a"}}}`,
		`{"categories":[{"id":"x","zh":"a","en":"a"},{"id":"x","zh":"a","en":"a"}]}`,
		`{"commands":{"a":{"zh":"a","en":"a","cat":"nope"}}}`,
	}
	for _, s := range bad {
		if _, err := ParseTable([]byte(s)); err == nil {
			t.Errorf("ParseTable(%q): want an error", s)
		}
	}
}

func TestMarshalTableRoundTrip(t *testing.T) {
	bt, err := BundledTable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalTable(bt)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseTable(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bt, back) {
		t.Error("the table changed in a round trip")
	}
	if string(data) != string(tableJSON) {
		t.Error("table.json is not in the form MarshalTable writes; regenerate it with padmate-probe csp-names")
	}
}

func TestLanguage(t *testing.T) {
	for in, want := range map[string]string{"zh-CN": "chinese_sc", "zh": "chinese_sc", "zh_CN": "chinese_sc", "en": "english", "en-US": "english", "": "english", "ja": "english"} {
		if got := Language(in); got != want {
			t.Errorf("Language(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindGUIDs(t *testing.T) {
	data := []byte("xx" + testGUID + "-" + "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" + "\x00 12345678-1234-1234-1234-12345678901 \x00-----")
	got := findGUIDs(data)
	want := []string{testGUID, "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findGUIDs = %v, want %v", got, want)
	}
	if len(findGUIDs(nil)) != 0 || len(findGUIDs([]byte("-"))) != 0 {
		t.Error("findGUIDs found something in nothing")
	}
}
