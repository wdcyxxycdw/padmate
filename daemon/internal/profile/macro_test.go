package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// macroNames is daemon/testdata/macro-names.json, which the UI's check
// reads too.
type macroNames struct {
	Files []struct {
		File string        `json:"file"`
		Kind MacroNameKind `json:"kind"`
	} `json:"files"`
	Same []struct {
		A    string `json:"a"`
		B    string `json:"b"`
		Same bool   `json:"same"`
	} `json:"same"`
}

func readMacroNames(t testing.TB) macroNames {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "macro-names.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases macroNames
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Files) == 0 || len(cases.Same) == 0 {
		t.Fatal("macro-names.json has no cases")
	}
	return cases
}

func TestCheckMacroFile(t *testing.T) {
	cases := readMacroNames(t)
	kinds := map[MacroNameKind]bool{}
	for _, tc := range cases.Files {
		err := CheckMacroFile(tc.File)
		kinds[tc.Kind] = true
		if tc.Kind == "" {
			if err != nil {
				t.Errorf("CheckMacroFile(%q) = %v, want nil", tc.File, err)
			}
			continue
		}
		var ne *MacroNameError
		if !errors.As(err, &ne) || ne.Kind != tc.Kind || ne.Name != tc.File {
			t.Errorf("CheckMacroFile(%q) = %#v, want kind %q", tc.File, err, tc.Kind)
		}
	}
	for _, k := range []MacroNameKind{"", MacroNameExtension, MacroNameEmpty, MacroNameLength, MacroNameChar, MacroNameInvalid} {
		if !kinds[k] {
			t.Errorf("macro-names.json has no case of kind %q", k)
		}
	}
	for _, tc := range cases.Same {
		if got := SameMacroName(tc.A, tc.B); got != tc.Same {
			t.Errorf("SameMacroName(%q, %q) = %v, want %v", tc.A, tc.B, got, tc.Same)
		}
	}
	if err := CheckMacroFile("a\xff.ts"); err == nil {
		t.Error("invalid UTF-8 accepted")
	}
}

func TestMacroName(t *testing.T) {
	for file, want := range map[string]string{"复制全部.ts": "复制全部", "a.js": "a", "a.ts.js": "a.ts", "X.TS": "X", "a.Js": "a", "x": "x", "ts": "ts"} {
		if got := MacroName(file); got != want {
			t.Errorf("MacroName(%q) = %q, want %q", file, got, want)
		}
	}
}

func FuzzCheckMacroFile(f *testing.F) {
	for _, tc := range readMacroNames(f).Files {
		f.Add(tc.File)
	}
	f.Fuzz(func(t *testing.T, file string) {
		if CheckMacroFile(file) != nil {
			return
		}
		stem := MacroName(file)
		switch {
		case !strings.HasSuffix(file, ".ts") && !strings.HasSuffix(file, ".js"):
			t.Fatalf("%q accepted without a .ts or .js extension", file)
		case stem == "" || utf8.RuneCountInString(stem) > MaxMacroName:
			t.Fatalf("%q accepted with %d characters", file, utf8.RuneCountInString(stem))
		case !utf8.ValidString(file) || !norm.NFC.IsNormalString(file):
			t.Fatalf("%q accepted but not NFC UTF-8", file)
		case strings.ContainsAny(stem, `\/:*?"<>|.~`):
			t.Fatalf("%q accepted with a forbidden character", file)
		case strings.TrimSpace(stem) != stem || strings.HasPrefix(stem, "-"):
			t.Fatalf("%q accepted with a leading or trailing space or dash", file)
		case isDeviceName(stem):
			t.Fatalf("%q accepted as a device name", file)
		}
		for _, r := range stem {
			if !unicode.IsGraphic(r) || r != ' ' && unicode.IsSpace(r) {
				t.Fatalf("%q accepted with %U", file, r)
			}
		}
		// Only ASCII: strings.ToUpper makes the dotless ı an I, which NTFS
		// and SameMacroName keep apart.
		upper := strings.Map(func(r rune) rune {
			if 'a' <= r && r <= 'z' {
				return r - 'a' + 'A'
			}
			return r
		}, stem)
		if !SameMacroName(file, upper+".js") {
			t.Fatalf("%q differs from its upper case", file)
		}
	})
}

func TestInMacroDir(t *testing.T) {
	for entry, want := range map[string]bool{
		"macros/x.ts": true, "Macros/x.ts": true, `macros\x.ts`: true, "./a/../macros/x.ts": true,
		"MACROS": true, "macros/lib/util.ts": true,
		"main.ts": false, "macrosx/x.ts": false, "lib/macros/x.ts": false, "../macros/x.ts": false,
		"/macros/x.ts": false, "x.ts/../../macros/y.ts": false,
	} {
		if got := InMacroDir(entry); got != want {
			t.Errorf("InMacroDir(%q) = %v, want %v", entry, got, want)
		}
	}
}

// The macros folder lists every .ts and .js file but declarations, also
// those whose names CheckMacroFile refuses.
func TestMacroCandidate(t *testing.T) {
	for name, want := range map[string]bool{
		"复制全部.ts": true, "a.js": true, "A.TS": true, "v1.2.ts": true, "x.Js": true,
		"padmate.d.ts": false, "X.D.TS": false, "tsconfig.json": false, "ts": false, ".ts": true, "a.tsx": false, "a.mjs": false,
	} {
		if got := MacroCandidate(name); got != want {
			t.Errorf("MacroCandidate(%q) = %v, want %v", name, got, want)
		}
	}
}

// Every gesture can run a macro; a file that does not exist is not the
// profile's problem, and no script section is needed.
func TestParseMacroAction(t *testing.T) {
	const source = `{"schemaVersion":0,"name":"m","layers":{"base":{"south":{"press":{"type":"macro","file":"复制全部.ts"},"release":{"type":"macro","file":"a.js"},"tap":{"type":"macro","file":"a.ts"},"doubleTap":{"type":"macro","file":"a.ts"},"tripleTap":{"type":"macro","file":"a.ts"},"quadTap":{"type":"macro","file":"a.ts"},"hold":{"type":"macro","file":"不存在.ts"},"latch":true}}},"common":{"leftstick":{"press":{"type":"macro","file":"common.ts"}}}}`
	p, ds := Parse([]byte(source))
	if ds.HasErrors() || len(ds) > 0 {
		t.Fatal(ds)
	}
	b := p.Layers[0].Binding(control.South)
	for g := GesturePress; g <= GestureHold; g++ {
		a := b.Action(g)
		if a == nil || a.Type != ActionMacro || a.File == "" || a.Keys.IsValid() || a.Mode != 0 || a.Repeat != nil {
			t.Errorf("%s: %+v", g, a)
		}
	}
	if a := b.Press; a.String() != "macro 复制全部.ts" || a.Type.String() != "macro" || a.ID != "/layers/base/south/press" {
		t.Errorf("press = %+v, %q", a, a)
	}
	if got := p.MacroFiles(); !slices.Equal(got, []string{"a.js", "a.ts", "common.ts", "不存在.ts", "复制全部.ts"}) {
		t.Errorf("MacroFiles = %q", got)
	}
	for _, invalid := range []string{
		strings.Replace(source, `"file":"a.js"`, `"file":"a.JS"`, 1),
		strings.Replace(source, `"file":"a.js"`, `"file":"a.js","keys":"Z"`, 1),
		strings.Replace(source, `"file":"a.js"`, `"file":"a.js","mode":"tap"`, 1),
		strings.Replace(source, `"file":"a.js"`, `"name":"a.js"`, 1),
		strings.Replace(source, `"name":"m",`, `"name":"m","script":{"entry":"macros\\main.ts","api":"0.21"},`, 1),
	} {
		if _, ds := Parse([]byte(invalid)); !ds.HasErrors() {
			t.Errorf("invalid profile accepted: %s", invalid)
		}
	}
	legacy := strings.Replace(source, `"name":"m",`, `"name":"m","script":{"entry":"main.ts","api":"0.21"},`, 1)
	if _, ds := Parse([]byte(legacy)); ds.HasErrors() {
		t.Errorf("legacy script with macros: %v", ds)
	}
}

// The macro files the profile refers to, by control and gesture: aliases
// count as their canonical control, layers and common are merged.
func TestMacroRefs(t *testing.T) {
	data := readFixture(t, "refs.json")
	refs, err := MacroRefs(data)
	if err != nil {
		t.Fatal(err)
	}
	checkMacroGolden(t, "refs.uses.golden", formatRefs(refs))
	if _, err := MacroRefs([]byte(`{"layers":`)); err == nil || !strings.Contains(err.Error(), "1:11") {
		t.Errorf("syntax error = %v", err)
	}
	// Only the syntax counts: a profile that fails the schema still has
	// its references.
	refs, err = MacroRefs([]byte(`{"name":1,"layers":{"base":{"south":{"press":{"type":"macro","file":"a.ts","x":1}},"bogus":{"tap":{"type":"macro","file":"b.ts"}},"east":{"tap":[]}}},"common":7}`))
	if err != nil || formatRefs(refs) != "a.ts: south press\n" {
		t.Errorf("refs = %v, %v", refs, err)
	}
}

// MacroActionCounts counts each action that runs a macro, in every layer
// and common, as many as RemoveMacroRefs removes, where MacroRefs merges
// the uses of a control and gesture.
func TestMacroActionCounts(t *testing.T) {
	merged := []byte(`{"layers":{"base":{"a":{"tap":{"type":"macro","file":"x.ts"}}},
"ink":{"south":{"tap":{"type":"macro","file":"x.ts"},"press":{"type":"macro","file":"y.ts"}}}},
"common":{"south":{"tap":{"type":"macro","file":"x.ts"}}}}`)
	for _, data := range [][]byte{readFixture(t, "refs.json"), merged} {
		counts, err := MacroActionCounts(data)
		if err != nil {
			t.Fatal(err)
		}
		refs, err := MacroRefs(data)
		if err != nil {
			t.Fatal(err)
		}
		for file := range refs {
			_, removed, err := RemoveMacroRefs(data, file)
			if err != nil {
				t.Fatal(err)
			}
			if counts[file] != removed {
				t.Errorf("%s: %d actions, RemoveMacroRefs removes %d", file, counts[file], removed)
			}
		}
		if len(counts) != len(refs) {
			t.Errorf("counts %v for refs %v", counts, refs)
		}
	}
	if counts, _ := MacroActionCounts(merged); counts["x.ts"] != 3 || counts["y.ts"] != 1 {
		t.Errorf("counts %v, want x.ts 3 times for one use", counts)
	}
	if _, err := MacroActionCounts([]byte(`{"layers":`)); err == nil {
		t.Error("no syntax error")
	}
}

func formatRefs(refs map[string][]MacroUse) string {
	files := make([]string, 0, len(refs))
	for f := range refs {
		files = append(files, f)
	}
	slices.Sort(files)
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "%s:", f)
		for _, u := range refs[f] {
			fmt.Fprintf(&b, " %s %s", u.Control, u.Gesture)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// The edits change only what they must: the goldens are refs.json with
// the edits made, which a reviewer compares with it.
func TestMacroRefEdits(t *testing.T) {
	data := readFixture(t, "refs.json")
	for _, tc := range []struct {
		golden string
		n      int
		edit   func([]byte) ([]byte, int, error)
	}{
		{"refs.rename.golden", 6, func(d []byte) ([]byte, int, error) { return RenameMacroRefs(d, "复制全部.ts", "全选 & 1.ts") }},
		{"refs.remove.golden", 6, func(d []byte) ([]byte, int, error) { return RemoveMacroRefs(d, "复制全部.ts") }},
		{"refs.detach.golden", 2, DetachScript},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			got, n, err := tc.edit(slices.Clone(data))
			if err != nil || n != tc.n {
				t.Fatalf("n = %d, %v; want %d", n, err, tc.n)
			}
			if _, ds := Parse(got); ds.HasErrors() {
				t.Fatalf("edited profile does not parse:\n%s\n%v", got, ds)
			}
			checkMacroGolden(t, tc.golden, string(got))
		})
	}
	renamed, _, err := RenameMacroRefs(data, "复制全部.ts", "新.js")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := MacroRefs(renamed)
	if err != nil || refs["复制全部.ts"] != nil || len(refs["新.js"]) != 6 || len(refs["其他.ts"]) != 1 {
		t.Errorf("after rename: %s %v", formatRefs(refs), err)
	}
	// Nothing to change: the same bytes.
	for _, edit := range []func([]byte) ([]byte, int, error){
		func(d []byte) ([]byte, int, error) { return RenameMacroRefs(d, "x.ts", "y.ts") },
		func(d []byte) ([]byte, int, error) { return RemoveMacroRefs(d, "x.ts") },
	} {
		got, n, err := edit(data)
		if err != nil || n != 0 || string(got) != string(data) {
			t.Errorf("no-op edit: n = %d, %v, changed %v", n, err, string(got) != string(data))
		}
	}
	for _, edit := range []func([]byte) ([]byte, int, error){
		func(d []byte) ([]byte, int, error) { return RenameMacroRefs(d, "x.ts", "y.ts") },
		func(d []byte) ([]byte, int, error) { return RemoveMacroRefs(d, "x.ts") },
		DetachScript,
	} {
		if _, _, err := edit([]byte(`{"layers": }`)); err == nil {
			t.Error("syntax error accepted")
		}
	}
}

// RenameMacroRefsAll renames several macros at once: a file renamed to the
// old name of another is not renamed twice (SCR-51's migration and import
// number a file "a 2.ts" while "a 2.ts" itself becomes "a 3.ts").
func TestRenameMacroRefsAll(t *testing.T) {
	data := []byte(`{"layers":{"base":{"south":{"tap":{"type":"macro","file":"a.ts"},"hold":{"type":"macro","file":"a 2.ts"}},` +
		`"east":{"press":{"type":"macro","file":"b.ts"}}}},"common":{"leftstick":{"tap":{"type":"macro","file":"a.ts"}}}}`)
	got, n, err := RenameMacroRefsAll(data, map[string]string{"a.ts": "a 2.ts", "a 2.ts": "a 3.ts", "x.ts": "y.ts"})
	want := `{"layers":{"base":{"south":{"tap":{"type":"macro","file":"a 2.ts"},"hold":{"type":"macro","file":"a 3.ts"}},` +
		`"east":{"press":{"type":"macro","file":"b.ts"}}}},"common":{"leftstick":{"tap":{"type":"macro","file":"a 2.ts"}}}}`
	if err != nil || n != 3 || string(got) != want {
		t.Fatalf("got %s, %d, %v", got, n, err)
	}
	if got, n, err := RenameMacroRefsAll(data, nil); err != nil || n != 0 || string(got) != string(data) {
		t.Fatalf("no renames: %s, %d, %v", got, n, err)
	}
	if _, _, err := RenameMacroRefsAll([]byte(`{"layers": }`), map[string]string{"a.ts": "b.ts"}); err == nil {
		t.Fatal("syntax error accepted")
	}
}

// Removal keeps the rest of the document as written: a BOM, CRLF line ends
// and the members on either side.
func TestRemoveMacroRefsBytes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{
			"\ufeff{\r\n  \"layers\": {\"base\": {\r\n    \"south\": {\"tap\": {\"type\": \"macro\", \"file\": \"a.ts\"},\r\n      \"hold\": {\"type\": \"keys\", \"keys\": \"Z\"}}\r\n  }}\r\n}\r\n",
			"\ufeff{\r\n  \"layers\": {\"base\": {\r\n    \"south\": {\"hold\": {\"type\": \"keys\", \"keys\": \"Z\"}}\r\n  }}\r\n}\r\n",
		},
		{
			`{"layers":{"base":{"south":{"hold":{"type":"keys","keys":"Z"},"tap":{"type":"macro","file":"a.ts"},"press":{"type":"macro","file":"a.ts"}}}}}`,
			`{"layers":{"base":{"south":{"hold":{"type":"keys","keys":"Z"}}}}}`,
		},
		{
			`{"layers":{"base":{"south":{"label":"x","tap":{"type":"macro","file":"a.ts"}},"east":{"tap":{"type":"macro","file":"a.ts"}}}}}`,
			`{"layers":{"base":{}}}`,
		},
		{
			`{"layers":{"base":{"color":"#FFFFFF","south":{"tap":{"type":"macro","file":"a.ts"}}}}}`,
			`{"layers":{"base":{"color":"#FFFFFF"}}}`,
		},
	} {
		got, _, err := RemoveMacroRefs([]byte(tc.in), "a.ts")
		if err != nil || string(got) != tc.want {
			t.Errorf("RemoveMacroRefs(%q)\n = %q, %v\nwant %q", tc.in, got, err, tc.want)
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "macros", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkMacroGolden(t *testing.T, name, got string) {
	t.Helper()
	checkGolden(t, filepath.Join("testdata", "macros", strings.TrimSuffix(name, ".golden")+".json"), got)
}

// MissingMacros warns at each action that runs a macro the folder does
// not list, by its exact name: in any layer, an alias and common included.
// A name CheckMacroFile refuses is Parse's error, not a warning too.
func TestMissingMacros(t *testing.T) {
	data := []byte("\ufeff" + `{"schemaVersion":0,"name":"m","layers":{
"base":{"south":{"tap":{"type":"macro","file":"有.ts"},"hold":{"type":"macro","file":"没有.ts"}}},
"a/b":{"lb":{"press":{"type":"macro","file":"Case.ts"}}}},
"common":{"leftstick":{"release":{"type":"macro","file":"没有.ts"},"press":{"type":"macro","file":"v1.2.ts"}}}}`)
	listed := []string{"有.ts", "case.ts"}
	ds := MissingMacros(data, func(file string) bool { return slices.Contains(listed, file) })
	var got []string
	for _, d := range ds {
		if d.Severity != SeverityWarning {
			t.Errorf("%v is not a warning", d)
		}
		got = append(got, fmt.Sprintf("%d:%d %s %s", d.Line, d.Col, d.Pointer, d.Message))
	}
	want := []string{
		`2:85 /layers/base/south/hold/file macro file "没有.ts" is not in the macros folder`,
		`3:45 /layers/a~1b/lb/press/file macro file "Case.ts" is not in the macros folder`,
		`4:57 /common/leftstick/release/file macro file "没有.ts" is not in the macros folder`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostics\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if ds := MissingMacros([]byte(`{`), func(string) bool { return false }); ds != nil {
		t.Errorf("a syntax error: %v", ds)
	}
}
