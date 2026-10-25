// Package installerpkg prepares and checks the NSIS installer (PKG-03,
// PKG-06): it validates the version tag the one version of a release
// comes from, lays out the files electron-builder packs next to the app,
// and reads the version back from the built artifacts. It also checks what
// the build does to them: the PE hygiene of the executables (PKG-11, pe.go),
// their Authenticode signatures and the publisher in app-update.yml (PKG-09,
// sign.go), and latest.yml against the installer (updateinfo.go).
package installerpkg

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
)

// ProductName is the product and executable name of the installed app
// (electron-builder.yml: productName, executableName).
const ProductName = "padmate"

// maxTagLength keeps installer names and Windows version strings short.
const maxTagLength = 64

var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Tag is the version of one release: the tag the Go executables are stamped
// with ("v0.14.0-rc.1") and, without the "v", the version of the Electron
// app and of the installer's version info ("0.14.0-rc.1").
type Tag struct {
	// Label is the tag as given: "v" and a semantic version.
	Label string
	// App is Label without the "v", a valid semantic version.
	App string
}

// ParseTag accepts "v" followed by a semantic version (MAJOR.MINOR.PATCH,
// optionally -PRERELEASE) and nothing else: no build metadata (the "+" does
// not belong in an installer's file name), no untagged build names such as
// "dev", "de58d1d" or "de58d1d-dirty", which `git describe` yields without
// a tag. The numbers must fit a Windows version, 0 to 65535.
func ParseTag(label string) (Tag, error) {
	if len(label) > maxTagLength {
		return Tag{}, fmt.Errorf("version %q is longer than %d characters", label, maxTagLength)
	}
	parts := tagPattern.FindStringSubmatch(label)
	if parts == nil {
		return Tag{}, fmt.Errorf("version %q is not a release tag: use v and a semantic version, such as v0.14.0 or v0.14.0-rc.1", label)
	}
	for _, n := range parts[1:4] {
		if v, err := strconv.ParseUint(n, 10, 16); err != nil || v > 65535 {
			return Tag{}, fmt.Errorf("version %q: numbers must be in [0, 65535]", label)
		}
	}
	for id := range strings.SplitSeq(parts[4], ".") {
		// Semantic versioning: a numeric identifier has no leading zero.
		if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
			return Tag{}, fmt.Errorf("version %q: numeric prerelease identifier %q has a leading zero", label, id)
		}
	}
	if _, err := exeresources.FixedVersion(label); err != nil {
		return Tag{}, err
	}
	return Tag{Label: label, App: strings.TrimPrefix(label, "v")}, nil
}

// InstallerName is the file name of the installer of this release.
func (t Tag) InstallerName() string {
	return ProductName + "-" + t.Label + "-windows-amd64-setup.exe"
}

// Compare orders two release tags by the precedence of semantic versioning
// (a release is later than its own release candidates, and rc.2 is later than
// rc.1 and earlier than rc.10): -1 when t is earlier than other, 0 when they
// are the same version, 1 when t is later.
func (t Tag) Compare(other Tag) int {
	a, b := tagPattern.FindStringSubmatch(t.Label), tagPattern.FindStringSubmatch(other.Label)
	if a == nil || b == nil {
		// A Tag that ParseTag did not make: no order, but a stable one.
		return strings.Compare(t.Label, other.Label)
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.ParseUint(a[i], 10, 16)
		y, _ := strconv.ParseUint(b[i], 10, 16)
		if x != y {
			return cmpUint(x, y)
		}
	}
	// A version with a prerelease comes before the same one without.
	switch {
	case a[4] == b[4]:
		return 0
	case a[4] == "":
		return 1
	case b[4] == "":
		return -1
	}
	x, y := strings.Split(a[4], "."), strings.Split(b[4], ".")
	for i := range min(len(x), len(y)) {
		if c := comparePrerelease(x[i], y[i]); c != 0 {
			return c
		}
	}
	return cmpUint(uint64(len(x)), uint64(len(y)))
}

// comparePrerelease orders two identifiers of a prerelease: numbers by value
// and before words, words by their characters.
func comparePrerelease(a, b string) int {
	an, aerr := strconv.ParseUint(a, 10, 64)
	bn, berr := strconv.ParseUint(b, 10, 64)
	switch {
	case aerr == nil && berr == nil:
		return cmpUint(an, bn)
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
