package portablezip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror/mirrortest"
)

// The public code mirror has no documentation to pack: Pack and Collect refuse
// at once, before they check the version, the binaries or the files, and write
// nothing.
func TestPackRefusesInTheMirror(t *testing.T) {
	root := mirrortest.Root(t, true)
	out := filepath.Join(t.TempDir(), "dist")
	opts := Options{Version: "not-a-version", Root: root, BinDir: filepath.Join(root, "bin"), OutDir: out}

	if _, err := Pack(context.Background(), opts); err == nil || err.Error() != publicmirror.PackagingRefusal {
		t.Errorf("Pack = %v", err)
	}
	if _, err := Collect(context.Background(), opts); err == nil || err.Error() != publicmirror.PackagingRefusal {
		t.Errorf("Collect = %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused Pack made %s", out)
	}
}

// Outside the mirror the same options get to the real checks, which fail on
// the version.
func TestPackGoesOnOutsideTheMirror(t *testing.T) {
	root := mirrortest.Root(t, false)
	_, err := Collect(context.Background(), Options{Version: "not-a-version", Root: root, BinDir: filepath.Join(root, "bin")})
	if err == nil || err.Error() == publicmirror.PackagingRefusal {
		t.Errorf("Collect = %v, want the error of the version check", err)
	}
}
