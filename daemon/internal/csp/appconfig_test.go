package csp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/csp/csptest"
)

func TestReadUseToolShift(t *testing.T) {
	for v, want := range map[int]bool{0: false, 1: true} {
		p := csptest.MakeAppConfig(t, t.TempDir(), v)
		if got, err := csp.ReadUseToolShift(p); err != nil || got != want {
			t.Errorf("UseToolShift=%d: %v %v", v, got, err)
		}
	}
	// Another database without the table is not an error.
	other := csptest.MakeKHC(t, t.TempDir(), nil)
	if got, err := csp.ReadUseToolShift(other); err != nil || got {
		t.Errorf("no table: %v %v", got, err)
	}
	// A file that is no database is an error; a missing file too.
	junk := filepath.Join(t.TempDir(), "Config.sqlite")
	if err := os.WriteFile(junk, []byte("not a database, not even close to one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := csp.ReadUseToolShift(junk); err == nil {
		t.Error("junk accepted")
	}
	if _, err := csp.ReadUseToolShift(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Error("missing file accepted")
	}
}
