package sdlpin

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestVersionConsistent(t *testing.T) {
	if want := fmt.Sprintf("%d.%d.%d", Major, Minor, Patch); Version != want {
		t.Errorf("Version = %q, want %q", Version, want)
	}
	if VersionNum != 3004016 {
		t.Errorf("VersionNum = %d, want 3004016", VersionNum)
	}
	if !strings.Contains(ZipURL, "/release-"+Version+"/") || !strings.HasSuffix(ZipURL, "/"+ZipName) {
		t.Errorf("ZipURL %q does not name release %s and asset %s", ZipURL, Version, ZipName)
	}
}

func TestHashesWellFormed(t *testing.T) {
	sha256Hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for name, h := range map[string]string{"ZipSHA256": ZipSHA256, "DLLSHA256": DLLSHA256} {
		if !sha256Hex.MatchString(h) {
			t.Errorf("%s = %q, want 64 lowercase hex digits", name, h)
		}
	}
	if ZipSize <= 0 {
		t.Errorf("ZipSize = %d, want > 0", ZipSize)
	}
}
