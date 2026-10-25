package output_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// layoutsEnv enables the tests that load the real keyboard layouts of
// this machine (ADR-0003), as in kblayout.
const layoutsEnv = "PADMATE_TEST_LAYOUTS"

// TestSystemLayoutsMatchFixtures loads the layout of each fixture with
// kblayout.WithLoaded, which never activates it and unloads it again,
// reads its tables through SystemLayouts and checks that every character
// key, alone and with Ctrl, resolves on them as on the fixture. The test
// also checks that the user's layout list is unchanged. That list is
// shared by the session, so no other process may load layouts meanwhile;
// "task test:layouts" runs this package and kblayout one at a time.
func TestSystemLayoutsMatchFixtures(t *testing.T) {
	if os.Getenv(layoutsEnv) != "1" {
		t.Skipf("loads the real keyboard layouts; set %s=1 to run", layoutsEnv)
	}
	before, err := kblayout.LayoutList()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := kblayout.LayoutList()
		switch {
		case err != nil:
			t.Error(err)
		case !slices.Equal(before, after):
			t.Errorf("the keyboard layout list changed from %s to %s", kblayout.FormatList(before), kblayout.FormatList(after))
		}
	})
	if hkl, err := output.SystemLayouts().Foreground(); err == nil {
		t.Logf("the foreground layout is %v", hkl)
	} else {
		t.Logf("no foreground layout: %v", err)
	}

	t.Run("error from f", func(t *testing.T) {
		errF := errors.New("test: f failed")
		err := kblayout.WithLoaded(klidUS, func(hkl windows.Handle) error {
			if got := uint16(hkl); got != 0x0409 {
				t.Errorf("WithLoaded(%s) gave %s", klidUS, kblayout.FormatHKL(hkl))
			}
			return errF
		})
		if !errors.Is(err, errF) {
			t.Errorf("WithLoaded = %v, want f's error", err)
		}
	})

	for _, klid := range fixtureKLIDs {
		t.Run(klid, func(t *testing.T) {
			f := loadLayoutFixture(t, klid)
			if got := kblayout.LayerDriver(klid); !strings.EqualFold(got, f.LayerDriver) {
				t.Skipf("this machine's layer driver is %q and the fixture's %q: their tables differ by design", got, f.LayerDriver)
			}
			var (
				sys    *output.Layout
				loaded windows.Handle
			)
			err := kblayout.WithLoaded(klid, func(hkl windows.Handle) error {
				loaded = hkl
				var err error
				sys, err = output.SystemLayouts().Layout(output.HKL(hkl))
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := kblayout.FormatHKL(loaded); got != f.HKL {
				t.Logf("loaded as %s, recorded as %s", got, f.HKL)
			}
			// The same HKL for both, so that the warnings compare too.
			hkl := output.HKL(loaded)
			gotSrc := &fakeLayouts{hkl: hkl, layouts: map[output.HKL]*output.Layout{hkl: sys}, calls: map[output.HKL]int{}}
			wantSrc := &fakeLayouts{hkl: hkl, layouts: map[output.HKL]*output.Layout{hkl: output.FixtureLayout(f)}, calls: map[output.HKL]int{}}
			gotR, gotW := newResolver(gotSrc)
			wantR, wantW := newResolver(wantSrc)
			n := 0
			for _, k := range charKeys() {
				for _, m := range []string{"", "Ctrl+"} {
					c := chordOf(t, m, k).Sided()
					got, want := mustResolve(t, gotR, c), mustResolve(t, wantR, c)
					if !strokesEqual(got, want) {
						t.Errorf("%s on this machine: %s\n  on the fixture: %s", c, downText(got), downText(want))
					}
					n++
				}
			}
			g, w := gotW.take(), wantW.take()
			if !slices.Equal(g, w) {
				t.Errorf("warnings on this machine %+v, on the fixture %+v", g, w)
			}
			t.Logf("%d chords resolve alike; %d fall back", n, len(w))
		})
	}
}
