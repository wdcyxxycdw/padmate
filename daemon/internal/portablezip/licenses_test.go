package portablezip

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestModuleLicensesIncludesNoticeAndMultipleLicenses(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"LICENSE", "LICENSE.MIT", "NOTICE.txt", "COPYING", "README.md", "main.go"} {
		writeFixture(t, root, name, name)
	}
	entries, err := moduleLicenses(root, "licenses/go/example.com/module@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, filepath.Base(e.name))
		if string(e.data) != filepath.Base(e.name) {
			t.Fatalf("license content changed: %s", e.name)
		}
	}
	want := []string{"COPYING", "LICENSE", "LICENSE.MIT", "NOTICE.txt"}
	if !slices.Equal(names, want) {
		t.Fatalf("licenses=%v, want %v", names, want)
	}
	if entries, err := moduleLicenses(t.TempDir(), "licenses/go/no-license@v1"); err != nil || len(entries) != 0 {
		t.Fatalf("missing license must stay missing, got %v, %v", entries, err)
	}
}
