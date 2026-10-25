package installersmoke

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
)

// TestRealRunKeyOpens guards the smoke test's claim that the user's real login
// startup value is left alone. A key path with a slip in it (a missing
// backslash, say) reads as an absent key, so "unchanged" would hold whatever
// the installer did. Windows creates the Run key with every profile; if it
// does not open here, the claim cannot be checked on this machine and the
// path must be looked at. It only reads.
func TestRealRunKeyOpens(t *testing.T) {
	if !registryKeyExists(installerpkg.RunKey) {
		t.Fatalf(`HKCU\%s does not open`, installerpkg.RunKey)
	}
}
