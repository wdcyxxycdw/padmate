package latency

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// osVersion is the real Windows version: RtlGetVersion, unlike
// GetVersionEx, does not depend on the executable's manifest.
func osVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
