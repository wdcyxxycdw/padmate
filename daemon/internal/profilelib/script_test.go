package profilelib

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckScriptName(t *testing.T) {
	for _, name := range []string{
		"main.ts", "brush.js", "My_Brush-2.ts", "a.b.ts", "x.ts", "console.ts", "com10.ts", "conx.js",
		strings.Repeat("a", 61) + ".ts",
	} {
		if err := CheckScriptName(name); err != nil {
			t.Errorf("%q refused: %v", name, err)
		}
	}
	for _, name := range []string{
		"", ".hidden.ts", ".ts", "../main.ts", "a/b.ts", `a\b.ts`, "C:main.ts", `C:\main.ts`, "main.ts:stream",
		"main.TS", "main.Js", "main.tsx", "main.mjs", "main", "main.ts.", "main.ts ", " main.ts",
		"profile.json", "tsconfig.json", "padmate.d.ts", "x.D.ts", "a..ts", "..ts",
		"con.ts", "CON.ts", "nul.js", "Aux.ts", "com1.ts", "COM0.js", "lpt9.ts", "con.x.ts",
		"画笔.ts", "my script.ts", "brush(1).ts", "main~1.ts", "%00.ts",
		strings.Repeat("a", 62) + ".ts",
	} {
		if err := CheckScriptName(name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

// writeScript runs WriteScript for profile id of the library in dir.
func writeScript(t *testing.T, l *Library, id, name, data string, replace bool) (bool, error) {
	t.Helper()
	var replaced bool
	err := l.UpdateFolder(id, func(folder *os.Root) error {
		var err error
		replaced, err = WriteScript(folder, id, name, []byte(data), replace)
		return err
	})
	return replaced, err
}

// assertFolder fails unless profiles/<id> holds exactly names.
func assertFolder(t *testing.T, dir, id string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "profiles", id))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(got)
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Fatalf("profiles/%s holds %v, want %v", id, got, names)
	}
}

func readScript(t *testing.T, dir, id, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "profiles", id, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteScriptCreatesThenReplaces(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	l := New(dir)
	if replaced, err := writeScript(t, l, "paint", "brush.ts", "// v1\n", false); err != nil || replaced {
		t.Fatal(replaced, err)
	}
	var exists *ScriptExistsError
	if _, err := writeScript(t, l, "paint", "brush.ts", "// v2\n", false); !errors.As(err, &exists) || exists.ID != "paint" || exists.Name != "brush.ts" {
		t.Fatal("overwrote without replace:", err)
	}
	if got := readScript(t, dir, "paint", "brush.ts"); got != "// v1\n" {
		t.Fatal(got)
	}
	if replaced, err := writeScript(t, l, "paint", "brush.ts", "// v2\n", true); err != nil || !replaced {
		t.Fatal(replaced, err)
	}
	if got := readScript(t, dir, "paint", "brush.ts"); got != "// v2\n" {
		t.Fatal(got)
	}
	// replace on a missing name simply creates it.
	if replaced, err := writeScript(t, l, "paint", "main.js", "", true); err != nil || replaced {
		t.Fatal(replaced, err)
	}
	assertFolder(t, dir, "paint", "profile.json", "brush.ts", "main.js")
	if data, err := l.Read("paint"); err != nil || string(data) != validProfile {
		t.Fatal("profile.json changed", err)
	}
}

func TestWriteScriptLimits(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	l := New(dir)
	if _, err := writeScript(t, l, "paint", "max.ts", strings.Repeat("a", MaxScriptBytes), false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, data string }{
		{"big.ts", strings.Repeat("a", MaxScriptBytes+1)},
		{"bad.ts", "ok\xff\xfe"},
		{"half.ts", "\xe7\x94"},
		{"../escape.ts", "x"},
		{"padmate.d.ts", "x"},
		{"con.ts", "x"},
	} {
		if _, err := writeScript(t, l, "paint", tc.name, tc.data, true); err == nil {
			t.Errorf("%s written", tc.name)
		}
	}
	assertFolder(t, dir, "paint", "profile.json", "max.ts")
}

// A name that is a folder, or a link, is never replaced or followed, and
// with or without replace the client learns it cannot import there.
func TestWriteScriptRefusesFoldersAndLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, dir, "paint", validProfile)
	if err := os.Mkdir(filepath.Join(dir, "profiles", "paint", "lib.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "secret.ts")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{"lib.ts"}
	if err := os.Symlink(target, filepath.Join(dir, "profiles", "paint", "link.ts")); err == nil {
		names = append(names, "link.ts")
	} else {
		t.Log("symlinks unavailable:", err)
	}
	l := New(dir)
	for _, name := range names {
		for _, replace := range []bool{false, true} {
			var unsupported *UnsupportedError
			if _, err := writeScript(t, l, "paint", name, "x", replace); !errors.As(err, &unsupported) || unsupported.Path != "paint/"+name {
				t.Fatal(name, replace, err)
			}
		}
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "secret" {
		t.Fatal("wrote through a link", err)
	}
	assertFolder(t, dir, "paint", append([]string{"profile.json"}, names...)...)
}

func TestUpdateFolderNeedsExistingProfile(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	called := false
	write := func(*os.Root) error { called = true; return nil }
	if err := l.UpdateFolder("paint", write); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("no library:", err)
	}
	fixture(t, dir, "other", validProfile)
	if err := l.UpdateFolder("paint", write); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing profile:", err)
	}
	for _, id := range []string{"", "../other", "Other", "con"} {
		if err := l.UpdateFolder(id, write); err == nil {
			t.Fatalf("invalid id %q accepted", id)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var unsupported *UnsupportedError
	if err := l.UpdateFolder("file", write); !errors.As(err, &unsupported) {
		t.Fatal("file as profile folder:", err)
	}
	if called {
		t.Fatal("write ran without a profile folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint")); !os.IsNotExist(err) {
		t.Fatal("created a profile folder", err)
	}
}

func TestFolder(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	got, err := New(dir).Folder("paint")
	if err != nil || got != filepath.Join(dir, "profiles", "paint") || !filepath.IsAbs(got) {
		t.Fatal(got, err)
	}
	if _, err := New(dir).Folder("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := New(dir).Folder("../paint"); err == nil {
		t.Fatal("invalid id accepted")
	}
	// A relative data directory still names an absolute folder.
	t.Chdir(dir)
	if got, err := New(".").Folder("paint"); err != nil || got != filepath.Join(dir, "profiles", "paint") {
		t.Fatal(got, err)
	}
}
