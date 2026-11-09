package cspnames

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteSyntheticInstall writes a synthetic CSP install in the layout of a
// real CSP 5 under root: <root>\CLIP STUDIO 1.5\CLIP STUDIO PAINT with the
// executable and the main resources of both languages (the menu names are
// those of synthResource: New, Undo, PNG, ...). It returns the folder that
// holds the executable. For tests of other packages (cspnames_test).
func WriteSyntheticInstall(t testing.TB, root string) string {
	t.Helper()
	exeDir := filepath.Join(root, "CLIP STUDIO 1.5", "CLIP STUDIO PAINT")
	langs := twoLanguages()
	for l, data := range langs {
		p := filepath.Join(exeDir, "resource", l, MainResourceName)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(exeDir, "CLIPStudioPaint.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	return exeDir
}
