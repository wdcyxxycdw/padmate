package installerpkg

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror/mirrortest"
)

// The public code mirror has no documentation to stage: Stage refuses at once
// and makes nothing.
func TestStageRefusesInTheMirror(t *testing.T) {
	root := mirrortest.Root(t, true)
	out := filepath.Join(t.TempDir(), "stage")
	err := Stage(context.Background(), StageOptions{Root: root, BinDir: filepath.Join(root, "bin"), Out: out})
	if err == nil || err.Error() != publicmirror.PackagingRefusal {
		t.Errorf("Stage = %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("a refused Stage made %s", out)
	}
}
