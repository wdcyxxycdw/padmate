package cspint

import (
	"errors"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// TestNamesNamerWithoutInstallation: without an installation the bundled
// table still names its commands and lays out the tree, the problem is
// reported, and the lookup is repeated only after the retry interval.
func TestNamesNamerWithoutInstallation(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	n := &NamesNamer{
		InstallDir: func() (string, error) { calls++; return "", errors.New("not installed") },
		Now:        func() time.Time { return now },
	}
	for range 3 {
		info, ok := n.CommandName("advancedcommand/canvasscrolldown", "zh-CN")
		if !ok || info.Name != "向下滚动" {
			t.Fatalf("table command = %+v, %v", info, ok)
		}
		if _, ok := n.CommandName("basiccommand/filenew", "zh-CN"); ok {
			t.Fatal("named a menu command without an installation")
		}
		if got := n.Layout("zh-CN"); len(got) == 0 || got[0].ID != "mainmenu" {
			t.Fatalf("layout = %v", got)
		}
		if p := n.NamesProblem("zh-CN"); p != integration.NamesInstallNotFound {
			t.Fatalf("problem = %q", p)
		}
	}
	if calls != 1 {
		t.Fatalf("InstallDir called %d times within the interval", calls)
	}
	now = now.Add(namesRetryEvery + time.Second)
	n.CommandName("advancedcommand/canvasscrolldown", "zh-CN")
	if calls != 2 {
		t.Fatalf("InstallDir called %d times after the interval", calls)
	}
	if _, ok := n.CommandName("noslash", "en"); ok {
		t.Fatal("named an id without a type")
	}
	if calls != 3 {
		t.Fatalf("InstallDir called %d times for two languages", calls)
	}
}

// TestNamesNamerPicksUpLaterInstall: a CSP installed after the first lookup
// is used once the interval has passed.
func TestNamesNamerPicksUpLaterInstall(t *testing.T) {
	dir := ""
	now := time.Unix(0, 0)
	n := &NamesNamer{InstallDir: func() (string, error) { return dir, nil }, Now: func() time.Time { return now }}
	if p := n.NamesProblem("en"); p != integration.NamesInstallNotFound {
		t.Fatalf("problem = %q", p)
	}
	dir = t.TempDir() // an install without a readable resource
	now = now.Add(namesRetryEvery)
	if p := n.NamesProblem("en"); p != integration.NamesResourceUnreadable {
		t.Fatalf("problem = %q", p)
	}
}
