package latency

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReadEnvironment(t *testing.T) {
	e := ReadEnvironment()
	t.Logf("%+v", e)
	if _, err := time.Parse(time.RFC3339, e.Date); err != nil || !strings.HasSuffix(e.Date, "Z") {
		t.Errorf("Date %q is not RFC 3339 UTC: %v", e.Date, err)
	}
	if e.GoVersion != runtime.Version() || e.NumCPU < 1 || e.GOMAXPROCS < 1 || e.Extra != nil {
		t.Errorf("unexpected environment %+v", e)
	}
	if e.Freq != Freq() {
		t.Errorf("Freq = %d, want Freq() = %d", e.Freq, Freq())
	}
	if !strings.HasPrefix(e.OS, runtime.GOOS) {
		t.Errorf("OS %q does not start with %q", e.OS, runtime.GOOS)
	}
	if runtime.GOOS == "windows" {
		var major, minor, build int
		if _, err := fmt.Sscanf(e.OS, "windows %d.%d.%d", &major, &minor, &build); err != nil || major < 10 || build == 0 {
			t.Errorf("OS %q is not a Windows version: %v", e.OS, err)
		}
	}
}
