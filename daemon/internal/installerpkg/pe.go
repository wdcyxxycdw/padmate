package installerpkg

import (
	"fmt"
	"os"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/pecheck"
)

// checkGoExecutable runs the hygiene check of internal/pecheck on a daemon or
// CLI executable ("padmated", "padmatectl"): layout, sections, entropy, the
// import table and the Windows functions it binds, against the committed
// goldens (PKG-11). It reads the file as it was installed, signature included.
// An executable of an earlier build (earlierBuild) is not compared with the
// goldens, which are the lists of this source: what an earlier release binds
// is its own. Its layout, its version resource and the denylist are checked.
func checkGoExecutable(file, name string, earlierBuild bool) []string {
	golden, err := pecheck.Embedded(name)
	if err != nil {
		return []string{err.Error()}
	}
	policy := pecheck.GoPolicy(golden)
	policy.NoGolden = earlierBuild
	rep, err := pecheck.InspectFile(file, pecheck.Full)
	if err != nil {
		return []string{fmt.Sprintf("pecheck: %v", err)}
	}
	return prefixed("pecheck: ", pecheck.Check(rep, policy))
}

// checkShippedExecutable runs the lighter check on a file that is not Go: the
// Electron executable and the installer have a version resource with a
// publisher, a product, a description and a copyright, and none of a packer's
// section names. version is what the caller has read from the resource with
// ReadVersionInfo, which also reads what NSIS and rcedit write.
func checkShippedExecutable(file string, version map[string]string) []string {
	rep, err := pecheck.InspectFile(file, pecheck.Light)
	if err != nil {
		if _, statErr := os.Stat(file); statErr != nil {
			return nil // reported by the caller
		}
		return []string{fmt.Sprintf("pecheck: %v", err)}
	}
	rep.Version, rep.VersionErr = version, ""
	return prefixed("pecheck: ", pecheck.Check(rep, pecheck.ShippedPolicy()))
}

func prefixed(prefix string, messages []string) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = prefix + strings.TrimSpace(m)
	}
	return out
}
