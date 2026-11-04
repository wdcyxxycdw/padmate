package cspint

import (
	"errors"
	"testing"
)

// TestNamesNamerWithoutInstallation: no installation is no names, and the
// installation is looked for once per language.
func TestNamesNamerWithoutInstallation(t *testing.T) {
	calls := 0
	n := &NamesNamer{InstallDir: func() (string, error) { calls++; return "", errors.New("not installed") }}
	for range 3 {
		if _, _, _, ok := n.CommandName("basiccommand/editundo", "zh-CN"); ok {
			t.Fatal("named a command without an installation")
		}
		if got := n.Categories("zh-CN"); len(got) != 0 {
			t.Fatalf("categories = %v", got)
		}
	}
	if calls != 1 {
		t.Fatalf("InstallDir called %d times for one language", calls)
	}
	if _, _, _, ok := n.CommandName("noslash", "en"); ok {
		t.Fatal("named an id without a type")
	}
	if calls != 2 {
		t.Fatalf("InstallDir called %d times for two languages", calls)
	}
}
