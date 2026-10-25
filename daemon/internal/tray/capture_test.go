package tray_test

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

func TestCapturePresentationKeepsUserPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		p := tray.Present(supervisor.Status{Capturing: true, Paused: paused}, nil)
		if !p.Inactive || !strings.Contains(p.Tooltip, "捕获中") || strings.Contains(p.Tooltip, "已暂停") != paused {
			t.Fatalf("paused=%v: %+v", paused, p)
		}
	}
}
