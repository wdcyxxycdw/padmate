package cspfile

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func attrs(t *testing.T, path string) uint32 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := windows.GetFileAttributes(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// ReplaceFileW keeps the replaced file's attributes: a hidden target stays
// hidden after Update.
func TestReplacePreservesHidden(t *testing.T) {
	target := filepath.Join(t.TempDir(), "default.khc")
	writeFile(t, target, "orig")
	p, _ := windows.UTF16PtrFromString(target)
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	if err := Update(target, appendEdit("+new"), Options{}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, target) != "orig+new" {
		t.Error("content not replaced")
	}
	if attrs(t, target)&windows.FILE_ATTRIBUTE_HIDDEN == 0 {
		t.Error("hidden attribute lost")
	}
}

func TestReplaceFallsBackWhenTargetMissing(t *testing.T) {
	dir := t.TempDir()
	target, tmp := filepath.Join(dir, "t"), filepath.Join(dir, "t.padmate-tmp")
	writeFile(t, tmp, "new")
	if err := replaceAtomic(target, tmp); err != nil {
		t.Fatal(err)
	}
	if readFile(t, target) != "new" || exists(tmp) {
		t.Error("fallback did not move the file")
	}
}

func TestSystemRootsAndRunning(t *testing.T) {
	r := SystemRoots()
	if r.AppData == "" {
		t.Error("no AppData")
	}
	if _, err := Running(); err != nil {
		t.Fatal(err)
	}
}
