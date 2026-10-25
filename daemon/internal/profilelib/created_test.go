package profilelib

import (
	"slices"
	"testing"
)

// MacroEntries sorts by the name without its extension first, so "a b.ts"
// does not land before "a.ts" because a space sorts before a dot (Macros
// keeps the byte order of the files); the UI sorts for display, in natural
// order, and this is only a sensible order on the wire.
func TestMacrosSortByNameWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	putMacroFiles(t, dir, map[string]string{
		"复制全部 2.ts":  "2",
		"复制全部.ts":    "1",
		"复制全部 10.ts": "10",
		"b.ts":       "b",
		"a.js":       "a",
		"a.ts":       "a",
	})
	l := New(dir)
	want := []string{"a.js", "a.ts", "b.ts", "复制全部.ts", "复制全部 10.ts", "复制全部 2.ts"}
	if got := macroNames(t, l); !slices.Equal(got, []string{"a.js", "a.ts", "b.ts", "复制全部 10.ts", "复制全部 2.ts", "复制全部.ts"}) {
		t.Fatalf("Macros %q", got)
	}
	entries, err := l.MacroEntries()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name
	}
	if !slices.Equal(got, want) {
		t.Fatalf("MacroEntries %q, want %q", got, want)
	}
}

// A library without a macros folder has no entries.
func TestMacroEntriesMissingFolder(t *testing.T) {
	entries, err := New(t.TempDir()).MacroEntries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("%+v, %v", entries, err)
	}
}
