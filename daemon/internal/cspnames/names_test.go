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
		typ, id string
		name    string
		path    []string
		group   string
		ok      bool
	}{
		// 1. The menu resource, case-insensitive; the first menu wins. The
		// Chinese test resource has English menu titles, which match no
		// group of the table.
		{"advancedcommand", "FileNew", "New", []string{"File"}, "", true},
		{"advancedcommand", "exportpng", "PNG", []string{"File", "Export"}, "", true},
		{"basiccommand", "undo", "Undo", []string{"Edit"}, "", true},
		// A menu without a name: no path and no category.
		{"advancedcommand", "noname", "Orphan", nil, "", true},
		// 2. The bundled table.
		{"advancedcommand", "canvasscrolldown", "向下滚动", nil, "", true},
		{"advancedcommand", "CANVASSCROLLDOWN", "向下滚动", nil, "", true},
		// 3. A plug-in DLL, by the GUID with or without a suffix.
		{"advancedcommand", testGUID, "马赛克", []string{"滤镜", "效果"}, FilterGroup, true},
		{"advancedcommand", strings.ToLower(testGUID) + "-2FB51A87-53F9-44D0-92A0-B0E0B2AE3AD7", "马赛克", []string{"滤镜", "效果"}, FilterGroup, true},
		// Not found: the caller shows the id.
		{"advancedcommand", "nosuchcommand", "", nil, "", false},
		{"advancedcommand", "3D617F46-7D99-44b2-80C1-2BEF6F9EBE33", ".psd(Photoshop 文档)", nil, "", true}, // the table overrides the plug-in's "psd|psd"
		{"advancedcommand", "00000000-0000-0000-0000-000000000000", "", nil, "", false},
		{"advancedcommand", "", "", nil, "", false},
	}
	for _, tt := range tests {
		name, path, cat, ok := n.Command(tt.typ, tt.id)
		if name != tt.name || !reflect.DeepEqual(path, tt.path) || cat != tt.group || ok != tt.ok {
			t.Errorf("Command(%q) = %q, %v, %q, %v; want %q, %v, %q, %v", tt.id, name, path, cat, ok, tt.name, tt.path, tt.group, tt.ok)
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
	if name, path, _, ok := n.Command("advancedcommand", "canvasscrolldown"); !ok || name != "Scroll to down" || path != nil {
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
			if len(n.Layout()) == 0 {
				t.Error("the bundled tree is missing")
			}
		})
	}
}

func TestLayout(t *testing.T) {
	n, err := LoadBundled("en")
	if err != nil {
		t.Fatal(err)
	}
	areas := n.Layout()
	if len(areas) < 3 || areas[0].ID != "mainmenu" || areas[0].Name != "Main menu" {
		t.Fatalf("areas = %+v", areas)
	}
	var file *Node
	for i, g := range areas[0].Children {
		if g.ID == "fileparent" {
			file = &areas[0].Children[i]
		}
	}
	if file == nil || file.Name != "File" || len(file.Children) < 10 || file.Children[0].ID != "filenew" || file.Children[0].Name != "" {
		t.Fatalf("file group = %+v", file)
	}
	if g, ok := n.Group("mainmenu"); !ok || g != "Main menu" {
		t.Errorf("Group = %q, %v", g, ok)
	}
	zh, _ := LoadBundled("zh-CN")
	if g, _ := zh.Group("fileparent"); zh.Layout()[0].Name != "主菜单" || g != "文件" {
		t.Errorf("zh areas = %q / %q", zh.Layout()[0].Name, g)
	}
}

// TestLayoutPlacesEachCommandOnce: the bundled tree lists a command once.
func TestLayoutPlacesEachCommandOnce(t *testing.T) {
	n, _ := LoadBundled("en")
	seen := map[string]bool{}
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, x := range nodes {
			if len(x.Children) == 0 {
				if seen[x.ID] {
					t.Errorf("%s placed twice", x.ID)
				}
				seen[x.ID] = true
				continue
			}
			if x.Name == "" {
				t.Errorf("group %s has no name", x.ID)
			}
			walk(x.Children)
		}
	}
	walk(n.Layout())
	if len(seen) < 700 {
		t.Errorf("only %d commands in the tree", len(seen))
	}
}

func TestLoadBundledNamesTableCommands(t *testing.T) {
	for _, lang := range []string{"zh-CN", "en"} {
		n, err := Load("", lang)
		if err != nil || n == nil {
			t.Fatalf("Load(\"\"): %v, %v", n, err)
		}
		name, _, _, ok := n.Command("advancedcommand", "canvasscrolldown")
		want := map[string]string{"zh-CN": "向下滚动", "en": "Scroll to down"}[lang]
		if !ok || name != want {
			t.Errorf("%s: canvasscrolldown = %q, %v", lang, name, ok)
		}
		if _, _, _, ok := n.Command("advancedcommand", "filenew"); ok || n.MenuCommands() != 0 {
			t.Errorf("%s: a menu command was named without an installation", lang)
		}
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
		for _, s := range []string{c.ZH, c.EN} {
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
		`{"tree":["a"]}`, // a command outside a group
		`{"tree":[{"id":"g","zh":"a","en":"a","c":[]}]}`,    // an empty group
		`{"tree":[{"id":"G","zh":"a","en":"a","c":["a"]}]}`, // upper case id
		`{"tree":[{"id":"g","zh":"a","c":["a"]}]}`,          // no English name
		`{"tree":[{"id":"g","zh":"a","en":"a","c":[""]}]}`,  // empty id
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

// TestPhotoshopExportNames: the four export-format commands that the plug-ins
// (ExportPSB.dll, ExportPSD.dll) leave unnamed are named by the table and
// sit at the end of the two export groups; the rest of the File menu is as
// the executable's tree has it.
func TestPhotoshopExportNames(t *testing.T) {
	const suffix = "-2FB51A87-53F9-44D0-92A0-B0E0B2AE3AD7"
	psb, psd := "28691CF0-C95B-4afc-A4C2-75E46D4F689D", "3D617F46-7D99-44b2-80C1-2BEF6F9EBE33"
	names := map[string][2]string{
		psb: {".psb(Photoshop 大型文档)", ".psb (Photoshop Big Document)"},
		psd: {".psd(Photoshop 文档)", ".psd (Photoshop Document)"},
	}
	for i, lang := range []string{"zh-CN", "en"} {
		n, err := loadFS(synthInstall(twoLanguages(), testPlugin()), lang)
		if err != nil {
			t.Fatal(err)
		}
		for _, base := range []string{psb, psd} {
			for _, id := range []string{base, strings.ToLower(base), base + suffix, strings.ToLower(base + suffix)} {
				if name, path, _, ok := n.Command("advancedcommand", id); !ok || name != names[base][i] || path != nil {
					t.Errorf("%s: Command(%q) = %q, %v, %v", lang, id, name, path, ok)
				}
			}
		}

		var find func(nodes []Node) *Node
		find = func(nodes []Node) *Node {
			for i := range nodes {
				if nodes[i].ID == "fileparent" {
					return &nodes[i]
				}
				if f := find(nodes[i].Children); f != nil {
					return f
				}
			}
			return nil
		}
		file := find(n.Layout())
		if file == nil {
			t.Fatal("no fileparent group")
		}
		ids := func(g string) []string {
			for _, c := range file.Children {
				if c.ID == g {
					var out []string
					for _, k := range c.Children {
						out = append(out, k.ID)
					}
					return out
				}
			}
			t.Fatalf("no group %s", g)
			return nil
		}
		want := map[string][]string{
			"filesaveasdirectparent": {"filesaveasproj", "filesaveasbmp", "filesaveasjpg", "filesaveaspng", "filesaveastif", "filesaveastga", "filesaveaswebp",
				strings.ToLower(psb), strings.ToLower(psd)},
			"filesavemergeimageparent": {"filesavemergeimagebmp", "filesavemergeimagejpg", "filesavemergeimagepng", "filesavemergeimagetif", "filesavemergeimagetga", "filesavemergeimagewebp",
				strings.ToLower(psb + suffix), strings.ToLower(psd + suffix)},
		}
		for g, w := range want {
			if got := ids(g); !reflect.DeepEqual(got, w) {
				t.Errorf("%s: %s = %v, want %v", lang, g, got, w)
			}
		}
		// The order of the other children of the File menu is unchanged.
		var top []string
		for _, c := range file.Children {
			top = append(top, c.ID)
		}
		wantTop := []string{"filenew", "filenewfromclipboard", "filenewfromphotolibrary", "filenewfrommovie", "fileopen", "fileclose",
			"filesave", "filesaveas", "filesaveasdirectparent", "filerevert", "filesavetophotolibrary", "filesavemergeimageparent",
			"fileexporttophotolibraryparent", "filebookbindingexportparent"}
		if len(top) < len(wantTop) || !reflect.DeepEqual(top[:len(wantTop)], wantTop) {
			t.Errorf("%s: File menu starts %v, want %v", lang, top, wantTop)
		}
	}
}
