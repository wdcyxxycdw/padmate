//go:build !windows

package output_test

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// Without layouts every character key is its US key.
func TestSystemLayoutsOther(t *testing.T) {
	r, warns := newResolver(output.SystemLayouts())
	c := keys.MustParse("LCtrl+Z")
	want, _ := output.ChordStroke(c)
	if got := mustResolve(t, r, c); !strokesEqual(got, want) {
		t.Errorf("Resolve(%v) = %s, want %s", c, downText(got), downText(want))
	}
	if w := warns.take(); len(w) != 1 || w[0].HKL != 0 || !strings.Contains(w[0].Reason, "no keyboard layouts") {
		t.Errorf("warnings %+v, want one with HKL 0", w)
	}
}
