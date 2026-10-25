package kblayout

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// layoutsEnv enables the tests that load the real keyboard layouts of
// this machine (ADR-0003).
const layoutsEnv = "PADMATE_TEST_LAYOUTS"

// TestDumpMatchesFixtures dumps the layouts of the fixtures on this
// machine and compares the result with the fixtures, leaving out what
// records the tool and the Windows version. Dump loads each layout with
// KLF_NOTELLSHELL without activating it and unloads it afterwards; the
// test also checks that the user's layout list is unchanged. That list is
// shared by the session, so no other process may load layouts meanwhile;
// "task test:layouts" runs this package and output one at a time.
//
// A difference means that Dump changed or that this Windows lays the
// keyboard out differently from the one that recorded the fixture; "task
// layouts" regenerates the fixtures.
func TestDumpMatchesFixtures(t *testing.T) {
	if os.Getenv(layoutsEnv) != "1" {
		t.Skipf("loads the real keyboard layouts; set %s=1 to run", layoutsEnv)
	}
	before, err := LayoutList()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := LayoutList()
		switch {
		case err != nil:
			t.Error(err)
		case !slices.Equal(before, after):
			t.Errorf("the keyboard layout list changed from %s to %s", FormatList(before), FormatList(after))
		}
	})

	for _, klid := range []string{klidUS, klidFR, klidJP} {
		t.Run(klid, func(t *testing.T) {
			want := loadFixture(t, klid)
			got, err := Dump(klid)
			if err != nil {
				t.Fatalf("Dump: %v", err)
			}
			if !strings.EqualFold(got.LayerDriver, want.LayerDriver) {
				t.Skipf("this machine's layer driver is %q and the fixture's %q: their tables differ by design", got.LayerDriver, want.LayerDriver)
			}
			got.Generator, got.Version = Generator, "test"
			if err := got.Validate(); err != nil {
				t.Errorf("the dump is not a valid fixture: %v", err)
			}
			if diffs := fixtureDiff(want, got); len(diffs) > 0 {
				if len(diffs) > 20 {
					diffs = append(diffs[:20], fmt.Sprintf("... and %d more", len(diffs)-20))
				}
				t.Errorf("Dump(%s) on Windows %s differs from the fixture recorded on Windows %s:\n  %s",
					klid, got.Windows, want.Windows, strings.Join(diffs, "\n  "))
			}
		})
	}
}

// A bad KLID fails before anything is loaded.
func TestWithLoadedBadKLID(t *testing.T) {
	for _, klid := range []string{"", "409", "0000040G", "000004090"} {
		called := false
		err := WithLoaded(klid, func(windows.Handle) error { called = true; return nil })
		if err == nil || called || !strings.Contains(err.Error(), "bad KLID") {
			t.Errorf("WithLoaded(%q) = %v, called f: %v; want a bad KLID error without calling f", klid, err, called)
		}
		if _, err := Dump(klid); err == nil || !strings.Contains(err.Error(), "bad KLID") {
			t.Errorf("Dump(%q) = %v, want a bad KLID error", klid, err)
		}
	}
}

func TestLayerDriver(t *testing.T) {
	for _, klid := range []string{klidUS, klidFR, "", "0000041G"} {
		if got := LayerDriver(klid); got != "" {
			t.Errorf("LayerDriver(%q) = %q, want \"\"", klid, got)
		}
	}
	t.Logf("LayerDriver(%s) = %q", klidJP, LayerDriver(klidJP))
}
