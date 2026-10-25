package script

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// host.version and host.api report the current script API, the one
// profile.json, the declarations and the scaffolds call HostAPI (the
// policy of M14 is in dtsgen.SupportedAPIs).
func TestHostAPIVersionIsTheCurrentAPI(t *testing.T) {
	if apiVersion != dtsgen.HostAPI.String() {
		t.Fatalf("apiVersion = %q, dtsgen.HostAPI = %s", apiVersion, dtsgen.HostAPI)
	}
}
