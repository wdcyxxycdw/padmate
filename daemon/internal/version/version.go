// Package version reports the padmate build version.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the application version. Release builds set it with
//
//	-ldflags "-X github.com/wdcyxxycdw/padmate/daemon/internal/version.Version=v0.0.1"
var Version = "dev"

// String returns Version, followed by the short VCS revision when the
// binary was built from a git checkout and Version does not already name
// it (as `git describe` output such as "v0.0.1-3-gde58d1d" does).
func String() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	return format(Version, info.Settings)
}

func format(version string, settings []debug.BuildSetting) string {
	var rev string
	var dirty bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if rev == "" || strings.Contains(version, rev) {
		return version
	}
	if dirty {
		rev += "-dirty"
	}
	return version + " (" + rev + ")"
}
