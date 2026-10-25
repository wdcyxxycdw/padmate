package installersmoke

import (
	"path/filepath"
	"strings"
	"testing"
)

// isBuildUnderTest says whether installed is the installer the test is about
// (PADMATE_INSTALLER_SIGNED_SUBJECT speaks of its signer). The first installer
// of an upgrade test is only where the upgrade starts from: the first signed
// release is preceded by unsigned ones by definition, and it must not fail
// the test of the signed one. With no second installer, or the same file named
// twice (the release workflow does that), the first installer is the build.
func isBuildUnderTest(installed, tested string) bool {
	return tested == "" || strings.EqualFold(filepath.Clean(installed), filepath.Clean(tested))
}

func TestOnlyTheBuildUnderTestIsCheckedForItsSignature(t *testing.T) {
	for _, c := range []struct {
		name              string
		installed, tested string
		want              bool
	}{
		{"no second installer", `C:\dist\padmate-v1.0.0-windows-amd64-setup.exe`, "", true},
		{"the same file named twice", `C:\dist\padmate-v1.0.0-windows-amd64-setup.exe`, `C:\dist\padmate-v1.0.0-windows-amd64-setup.exe`, true},
		{"the same file in another spelling", `C:\dist\padmate-v1.0.0-windows-amd64-setup.exe`, `c:/dist/../dist/padmate-v1.0.0-windows-amd64-setup.exe`, true},
		{"an older release starting an upgrade", `C:\old\padmate-v1.0.0-rc.1-windows-amd64-setup.exe`, `C:\new\padmate-v1.0.0-rc.2-windows-amd64-setup.exe`, false},
		{"an unsigned build of the same version", `C:\unsigned\padmate-v1.0.0-windows-amd64-setup.exe`, `C:\signed\padmate-v1.0.0-windows-amd64-setup.exe`, false},
	} {
		if got := isBuildUnderTest(c.installed, c.tested); got != c.want {
			t.Errorf("%s: isBuildUnderTest(%q, %q) = %v, want %v", c.name, c.installed, c.tested, got, c.want)
		}
	}
}
