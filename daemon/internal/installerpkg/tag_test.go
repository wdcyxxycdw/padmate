package installerpkg

import "testing"

func TestParseTag(t *testing.T) {
	good := []struct{ in, app, name string }{
		{"v0.14.0", "0.14.0", "padmate-v0.14.0-windows-amd64-setup.exe"},
		{"v0.14.0-rc.1", "0.14.0-rc.1", "padmate-v0.14.0-rc.1-windows-amd64-setup.exe"},
		{"v1.0.0-beta.2.x", "1.0.0-beta.2.x", "padmate-v1.0.0-beta.2.x-windows-amd64-setup.exe"},
		// What `git describe` prints for a commit after a tag is a valid
		// prerelease, so a local build of a checkout works.
		{"v0.13.0-5-gde58d1d", "0.13.0-5-gde58d1d", "padmate-v0.13.0-5-gde58d1d-windows-amd64-setup.exe"},
		{"v0.13.0-5-gde58d1d-dirty", "0.13.0-5-gde58d1d-dirty", "padmate-v0.13.0-5-gde58d1d-dirty-windows-amd64-setup.exe"},
		{"v65535.65535.65535", "65535.65535.65535", "padmate-v65535.65535.65535-windows-amd64-setup.exe"},
		{"v0.0.0-0", "0.0.0-0", "padmate-v0.0.0-0-windows-amd64-setup.exe"},
	}
	for _, tt := range good {
		got, err := ParseTag(tt.in)
		if err != nil {
			t.Errorf("ParseTag(%q): %v", tt.in, err)
			continue
		}
		if got.Label != tt.in || got.App != tt.app || got.InstallerName() != tt.name {
			t.Errorf("ParseTag(%q) = %+v, installer %q; want app %q, installer %q", tt.in, got, got.InstallerName(), tt.app, tt.name)
		}
	}
	bad := []string{
		"", "dev", "de58d1d", "de58d1d-dirty", "0.14.0", "V0.14.0", "v0.14", "v0.14.0.1", "v1.2.3-",
		"v01.2.3", "v1.02.3", "v1.2.3-rc.01", "v1.2.3-rc..1", "v1.2.3-rc.", "v1.2.3+build", "v1.2.3-rc.1+build",
		"v65536.0.0", "v0.0.99999", "v1.2.3 ", " v1.2.3", "v1.2.3\n", "v1.2.3-rc/1", "v1.2.3-rc\\1", "v1.2.3-é",
		"v1.2.3-" + string(make([]byte, 70)),
	}
	for _, in := range bad {
		if got, err := ParseTag(in); err == nil {
			t.Errorf("ParseTag(%q) = %+v, want an error", in, got)
		}
	}
}

func TestParseTagLength(t *testing.T) {
	long := "v1.2.3-"
	for len(long) < maxTagLength {
		long += "a"
	}
	if _, err := ParseTag(long); err != nil {
		t.Errorf("a tag of %d characters: %v", len(long), err)
	}
	if _, err := ParseTag(long + "a"); err == nil {
		t.Errorf("a tag of %d characters is accepted", len(long)+1)
	}
}

// The order is the one semantic versioning gives, on its own example.
func TestTagCompare(t *testing.T) {
	ordered := []string{
		"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2", "v1.0.0-beta.11",
		"v1.0.0-rc.1", "v1.0.0", "v1.0.1", "v1.1.0", "v2.0.0",
		// The versions of the installer's own history.
		"v0.17.0-rc.1", "v0.17.0-rc.2", "v0.17.0", "v0.18.0-rc.1",
	}
	// The second group is its own ascending run.
	for _, run := range [][]string{ordered[:11], ordered[11:]} {
		for i, a := range run {
			for j, b := range run {
				want := 0
				switch {
				case i < j:
					want = -1
				case i > j:
					want = 1
				}
				if got := mustTag(a).Compare(mustTag(b)); got != want {
					t.Errorf("%s.Compare(%s) = %d, want %d", a, b, got, want)
				}
			}
		}
	}
}
