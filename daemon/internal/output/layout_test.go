package output_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// layoutDir holds the layout fixtures ("task layouts" regenerates them).
const layoutDir = "../../testdata/layouts"

// The fixture layouts and the handles they were recorded with.
const (
	klidUS = "00000409"
	klidFR = "0000040C"
	klidJP = "00000411"

	hklUS output.HKL = 0x04090409
	hklFR output.HKL = 0x040C040C
	hklJP output.HKL = 0x04110411
)

var fixtureKLIDs = []string{klidUS, klidFR, klidJP}

func loadLayoutFixture(t testing.TB, klid string) *kblayout.Fixture {
	t.Helper()
	f, err := kblayout.Load(filepath.Join(layoutDir, kblayout.FileName(klid)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

// fakeLayouts is a LayoutSource with a settable foreground layout. It
// counts its calls.
type fakeLayouts struct {
	hkl     output.HKL
	fgErr   error
	layouts map[output.HKL]*output.Layout
	errs    map[output.HKL]error

	fgCalls int
	calls   map[output.HKL]int // Layout calls per HKL
}

// newFakeLayouts returns a fakeLayouts with the three fixtures, US in the
// foreground.
func newFakeLayouts(t testing.TB) *fakeLayouts {
	t.Helper()
	return &fakeLayouts{
		hkl: hklUS,
		layouts: map[output.HKL]*output.Layout{
			hklUS: output.FixtureLayout(loadLayoutFixture(t, klidUS)),
			hklFR: output.FixtureLayout(loadLayoutFixture(t, klidFR)),
			hklJP: output.FixtureLayout(loadLayoutFixture(t, klidJP)),
		},
		errs:  map[output.HKL]error{},
		calls: map[output.HKL]int{},
	}
}

func (f *fakeLayouts) Foreground() (output.HKL, error) {
	f.fgCalls++
	if f.fgErr != nil {
		return 0, f.fgErr
	}
	return f.hkl, nil
}

func (f *fakeLayouts) Layout(hkl output.HKL) (*output.Layout, error) {
	f.calls[hkl]++
	if err := f.errs[hkl]; err != nil {
		return nil, err
	}
	l, ok := f.layouts[hkl]
	if !ok {
		return nil, fmt.Errorf("test: no layout %v", hkl)
	}
	return l, nil
}

// warnLog collects the warnings of a Resolver.
type warnLog struct{ w []output.ResolveWarning }

func (l *warnLog) add(w output.ResolveWarning) { l.w = append(l.w, w) }

// take returns the warnings since the last take.
func (l *warnLog) take() []output.ResolveWarning {
	w := l.w
	l.w = nil
	return w
}

func newResolver(src output.LayoutSource) (*output.Resolver, *warnLog) {
	log := &warnLog{}
	return output.NewResolver(src, output.ResolverOptions{Warn: log.add}), log
}

// downText formats the presses of st: "down LCtrl sc=0x1D; down Z sc=0x11".
func downText(st output.Stroke) string {
	s := make([]string, len(st.Down))
	for i, e := range st.Down {
		s[i] = e.String()
	}
	return strings.Join(s, "; ")
}

// checkStrokeShape checks that st presses each physical key once and that
// Up is the exact reverse of Down.
func checkStrokeShape(t *testing.T, spec string, st output.Stroke) {
	t.Helper()
	if len(st.Down) == 0 || len(st.Down) != len(st.Up) {
		t.Fatalf("%s: %d presses, %d releases", spec, len(st.Down), len(st.Up))
	}
	for i, d := range st.Down {
		u := st.Up[len(st.Up)-1-i]
		want := d
		want.Up = true
		if d.Up || u != want {
			t.Errorf("%s: press %v is released by %v", spec, d, u)
		}
		for _, o := range st.Down[:i] {
			if o.ID() == d.ID() {
				t.Errorf("%s: %v pressed twice", spec, d)
			}
		}
	}
}

func strokesEqual(a, b output.Stroke) bool {
	return slices.Equal(a.Down, b.Down) && slices.Equal(a.Up, b.Up)
}

func mustResolve(t *testing.T, r *output.Resolver, c keys.Chord) output.Stroke {
	t.Helper()
	st, err := r.Resolve(c)
	if err != nil {
		t.Fatalf("Resolve(%v): %v", c, err)
	}
	return st
}

// charKeys returns the KindChar keys of the table.
func charKeys() []keys.Key {
	var ks []keys.Key
	for _, k := range keys.Table() {
		if k.Kind() == keys.KindChar {
			ks = append(ks, k)
		}
	}
	return ks
}

// chordOf returns the chord mods+k, parsed from its spelling.
func chordOf(t *testing.T, mods string, k keys.Key) keys.Chord {
	t.Helper()
	c, err := keys.Parse(mods + k.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", mods+k.String(), err)
	}
	return c
}

// On the US layout every character key resolves as ChordStroke does.
func TestResolverUSMatchesChordStroke(t *testing.T) {
	src := newFakeLayouts(t)
	r, warns := newResolver(src)
	for _, k := range charKeys() {
		for _, m := range []string{"", "Ctrl+", "Shift+", "Ctrl+Shift+", "Alt+", "RShift+", "RCtrl+RAlt+"} {
			generic := chordOf(t, m, k)
			for _, c := range []keys.Chord{generic.Sided(), generic} {
				got := mustResolve(t, r, c)
				want, err := output.ChordStroke(c)
				if err != nil {
					t.Fatalf("ChordStroke(%v): %v", c, err)
				}
				if !strokesEqual(got, want) {
					t.Errorf("Resolve(%v) on US = %s\nwant %s", c, downText(got), downText(want))
				}
			}
		}
	}
	if w := warns.take(); len(w) > 0 {
		t.Errorf("warnings on US: %v", w)
	}
	if src.calls[hklUS] != 1 {
		t.Errorf("Layout(US) called %d times, want 1", src.calls[hklUS])
	}
}

// letterDigitVK returns the VK of a letter or digit character: VK_A–VK_Z
// and VK_0–VK_9 are the upper-case letters and the digits.
func letterDigitVK(ch rune) (uint8, bool) {
	switch {
	case ch >= 'a' && ch <= 'z':
		return uint8(ch - 'a' + 'A'), true
	case ch >= '0' && ch <= '9':
		return uint8(ch), true
	}
	return 0, false
}

// TestResolverFixtures resolves every character key under a few modifier
// sets on each fixture and checks the result against the fixture's
// tables: letters and digits by VK with nothing added, punctuation and
// Plus by character with the modifiers the layout needs added.
func TestResolverFixtures(t *testing.T) {
	hkls := map[string]output.HKL{klidUS: hklUS, klidFR: hklFR, klidJP: hklJP}
	for _, klid := range fixtureKLIDs {
		t.Run(klid, func(t *testing.T) {
			f := loadLayoutFixture(t, klid)
			src := newFakeLayouts(t)
			src.hkl = hkls[klid]
			r, warns := newResolver(src)
			var moved []string
			for _, k := range charKeys() {
				ch, _ := k.Char()
				for _, m := range []string{"", "Ctrl+", "Shift+", "Alt+", "RCtrl+", "RShift+RAlt+"} {
					c := chordOf(t, m, k).Sided()
					st := mustResolve(t, r, c)
					checkStrokeShape(t, c.String(), st)
					mods := c.Mods()
					prefix := len(mods.Keys())
					for i, mk := range mods.Keys() {
						if want, _ := output.KeyEventFor(mk, false); st.Down[i] != want {
							t.Errorf("%s: press %d is %v, want the chord's %v", c, i, st.Down[i], want)
						}
					}
					main := st.Down[len(st.Down)-1]
					var added []string
					for _, e := range st.Down[prefix : len(st.Down)-1] {
						added = append(added, e.Key.String())
					}
					var (
						scan uint8
						ext  bool
						want []string // the modifiers the layout adds
					)
					if vk, ok := letterDigitVK(ch); ok {
						e, ok := f.VK(vk)
						if !ok {
							t.Fatalf("%s: the fixture has no scan code for VK 0x%02X", c, vk)
						}
						scan, ext = uint8(e.Scan), e.Ext
					} else {
						e, ok := f.Char(ch)
						if !ok || e.Absent {
							t.Fatalf("%s: the fixture does not type %q", c, ch)
						}
						scan, ext = uint8(e.Scan), e.Ext
						// What the layout adds, by the entry's shift state:
						// Shift → LShift, Ctrl+Alt (AltGr) → LCtrl and LAlt,
						// each only when the chord has none of either side.
						if e.Shift && mods&(keys.ModShift|keys.ModLShift|keys.ModRShift) == 0 {
							want = append(want, "LShift")
						}
						if e.Ctrl && e.Alt {
							if mods&(keys.ModCtrl|keys.ModLCtrl|keys.ModRCtrl) == 0 {
								want = append(want, "LCtrl")
							}
							if mods&(keys.ModAlt|keys.ModLAlt|keys.ModRAlt) == 0 {
								want = append(want, "LAlt")
							}
						}
					}
					if main.Key != k || main.ByVK || main.Scan != scan || main.Ext != ext {
						t.Errorf("%s: main key %v, want %v sc=0x%02X ext=%v", c, main, k, scan, ext)
					}
					if !slices.Equal(added, want) {
						t.Errorf("%s: the layout adds %q, want %q", c, added, want)
					}
					if us := k.US(); m == "" && (main.Scan != us.Scan || main.Ext != us.Ext || (len(added) > 0) != us.Shift) {
						moved = append(moved, fmt.Sprintf("%s → %s", k, downText(st)))
					}
				}
			}
			// No character key of the three fixtures falls back.
			if w := warns.take(); len(w) > 0 {
				t.Errorf("warnings: %v", w)
			}
			t.Logf("%s: %d character keys differ from US: %s", klid, len(moved), strings.Join(moved, ", "))
		})
	}
}

// TestResolverFrench spells out the AZERTY cases.
func TestResolverFrench(t *testing.T) {
	src := newFakeLayouts(t)
	src.hkl = hklFR
	r, warns := newResolver(src)
	for _, tt := range []struct{ spec, want string }{
		{"Ctrl+Z", "down LCtrl sc=0x1D; down Z sc=0x11"},
		{"Z", "down Z sc=0x11"},
		{"A", "down A sc=0x10"},
		{"W", "down W sc=0x2C"},
		{"M", "down M sc=0x27"},
		// Digits go by VK: Ctrl+1 is VK_1 with Ctrl, no Shift, although
		// typing "1" on AZERTY needs Shift.
		{"Ctrl+1", "down LCtrl sc=0x1D; down 1 sc=0x02"},
		{"1", "down 1 sc=0x02"},
		{"Plus", "down LShift sc=0x2A; down Plus sc=0x0D"},
		// A Shift of the chord is not added twice.
		{"Shift+.", "down LShift sc=0x2A; down . sc=0x33"},
		{"RShift+.", "down RShift sc=0x36; down . sc=0x33"},
		{".", "down LShift sc=0x2A; down . sc=0x33"},
		// AltGr characters get LCtrl and LAlt, unless the chord has a Ctrl
		// or an Alt of either side.
		{"Ctrl+[", "down LCtrl sc=0x1D; down LAlt sc=0x38; down [ sc=0x06"},
		{"[", "down LCtrl sc=0x1D; down LAlt sc=0x38; down [ sc=0x06"},
		{"RCtrl+[", "down RCtrl sc=E0 1D; down LAlt sc=0x38; down [ sc=0x06"},
		{"RAlt+[", "down RAlt sc=E0 38; down LCtrl sc=0x1D; down [ sc=0x06"},
		{"Ctrl+Alt+[", "down LCtrl sc=0x1D; down LAlt sc=0x38; down [ sc=0x06"},
		{"Ctrl+Shift+Alt+\\", "down LCtrl sc=0x1D; down LShift sc=0x2A; down LAlt sc=0x38; down \\ sc=0x09"},
		// AZERTY types "-" with the 6 key, unshifted.
		{"-", "down - sc=0x07"},
		{"Ctrl+-", "down LCtrl sc=0x1D; down - sc=0x07"},
	} {
		c := keys.MustParse(tt.spec).Sided()
		st := mustResolve(t, r, c)
		checkStrokeShape(t, tt.spec, st)
		if got := downText(st); got != tt.want {
			t.Errorf("Resolve(%s) on 040C:\n got %s\nwant %s", tt.spec, got, tt.want)
		}
	}
	if w := warns.take(); len(w) > 0 {
		t.Errorf("warnings: %v", w)
	}
}

// Physical, raw and named keys never go through the layout.
func TestResolverIgnoresLayoutForOtherKeys(t *testing.T) {
	for _, hkl := range []output.HKL{hklUS, hklFR, hklJP} {
		src := newFakeLayouts(t)
		src.hkl = hkl
		r, warns := newResolver(src)
		for _, tt := range []struct{ spec, want string }{
			{"KeyZ", "down KeyZ sc=0x2C"},
			{"sc:0x2C", "down sc:0x2C sc=0x2C"},
			{"Ctrl+KeyZ", "down LCtrl sc=0x1D; down KeyZ sc=0x2C"},
			{"Key1", "down Key1 sc=0x02"},
		} {
			if got := downText(mustResolve(t, r, keys.MustParse(tt.spec).Sided())); got != tt.want {
				t.Errorf("Resolve(%s) on %v = %s, want %s", tt.spec, hkl, got, tt.want)
			}
		}
		var specs []keys.Chord
		for _, s := range []string{"F13", "Space", "Num0", "Pause", "Ctrl+Pause", "sc:0xE01D", "Ctrl+Shift", "RAlt"} {
			specs = append(specs, keys.MustParse(s))
		}
		for _, k := range keys.Table() {
			if k.Kind() == keys.KindChar {
				continue
			}
			for _, m := range []string{"", "Ctrl+"} {
				if c, err := keys.Parse(m + k.String()); err == nil { // not "Ctrl+Ctrl"
					specs = append(specs, c)
				}
			}
		}
		for _, generic := range specs {
			for _, c := range []keys.Chord{generic, generic.Sided()} {
				got := mustResolve(t, r, c)
				want, _ := output.ChordStroke(c)
				if !strokesEqual(got, want) {
					t.Errorf("Resolve(%v) on %v = %s, want ChordStroke's %s", c, hkl, downText(got), downText(want))
				}
			}
		}
		if src.fgCalls != 0 || len(src.calls) != 0 {
			t.Errorf("on %v: %d Foreground and %v Layout calls for keys that are not characters, want none", hkl, src.fgCalls, src.calls)
		}
		if w := warns.take(); len(w) > 0 {
			t.Errorf("warnings: %v", w)
		}
	}
}

// editedLayout returns the 040C tables changed by edit, as a Layout.
func editedLayout(t *testing.T, edit func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry) *output.Layout {
	t.Helper()
	f := loadLayoutFixture(t, klidFR)
	vks := slices.Clone(f.VKToScan)
	chars := slices.Clone(f.Chars)
	vks = edit(vks, chars)
	return output.NewLayout(vks, chars, nil)
}

// editVK changes the vkToScan entry of vk.
func editVK(vks []kblayout.VKEntry, vk uint8, edit func(*kblayout.VKEntry)) {
	for i := range vks {
		if vks[i].VK == kblayout.Code(vk) {
			edit(&vks[i])
		}
	}
}

// editChar changes the chars entry of c.
func editChar(chars []kblayout.CharEntry, c rune, edit func(*kblayout.CharEntry)) {
	edit(&chars[c-0x20])
}

func TestResolverFallback(t *testing.T) {
	const hklEdited output.HKL = 0x1234040C
	errLayout := errors.New("test: bad layout")
	errChars := errors.New("test: VkKeyScanExW failed")
	for _, tt := range []struct {
		name   string
		layout func(t *testing.T) *output.Layout
		spec   string
		reason string // part of the warning's Reason
	}{
		{"vk missing", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, _ []kblayout.CharEntry) []kblayout.VKEntry {
				return slices.DeleteFunc(vks, func(e kblayout.VKEntry) bool { return e.VK == 'Z' })
			})
		}, "Ctrl+Z", "no scan code for VK 0x5A"},
		{"vk E1", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, _ []kblayout.CharEntry) []kblayout.VKEntry {
				editVK(vks, '1', func(e *kblayout.VKEntry) { e.E1 = true })
				return vks
			})
		}, "1", "E1"},
		{"vk numpad", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, _ []kblayout.CharEntry) []kblayout.VKEntry {
				editVK(vks, 'A', func(e *kblayout.VKEntry) { e.Scan = 0x47 })
				return vks
			})
		}, "A", "NumLock"},
		{"vk break code", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, _ []kblayout.CharEntry) []kblayout.VKEntry {
				editVK(vks, 'A', func(e *kblayout.VKEntry) { e.Scan = 0x9E })
				return vks
			})
		}, "A", "cannot be injected"},
		{"vk on a modifier", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, _ []kblayout.CharEntry) []kblayout.VKEntry {
				editVK(vks, 'A', func(e *kblayout.VKEntry) { e.Scan = 0x1D })
				return vks
			})
		}, "Ctrl+A", "key of a modifier"},
		{"char on a modifier", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '[', func(e *kblayout.CharEntry) { e.Scan = 0x38 })
				return vks
			})
		}, "[", "key of a modifier"},
		{"char absent", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '[', func(e *kblayout.CharEntry) { *e = kblayout.CharEntry{Char: "[", Code: '[', Absent: true} })
				return vks
			})
		}, "[", `no key for "["`},
		{"char missing", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				clear(chars)
				return vks
			})
		}, "Plus", `no key for "+"`},
		{"hankaku", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '[', func(e *kblayout.CharEntry) { e.Hankaku = true })
				return vks
			})
		}, "[", "Hankaku"},
		{"ctrl only", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '[', func(e *kblayout.CharEntry) { e.Alt = false })
				return vks
			})
		}, "Ctrl+[", "Ctrl"},
		{"alt only", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, ']', func(e *kblayout.CharEntry) { e.Ctrl = false })
				return vks
			})
		}, "]", "Alt"},
		{"char numpad", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '.', func(e *kblayout.CharEntry) { e.VK, e.Scan, e.Shift = 0x6E, 0x53, false })
				return vks
			})
		}, ".", "NumLock"},
		{"char zero scan", func(t *testing.T) *output.Layout {
			return editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
				editChar(chars, '-', func(e *kblayout.CharEntry) { e.Scan = 0 })
				return vks
			})
		}, "-", "no scan code"},
		{"chars error", func(t *testing.T) *output.Layout {
			f := loadLayoutFixture(t, klidFR)
			return output.NewLayout(f.VKToScan, nil, errChars)
		}, "RShift+/", errChars.Error()},
		{"layout error", nil, "Ctrl+Z", errLayout.Error()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := newFakeLayouts(t)
			src.hkl = hklEdited
			if tt.layout != nil {
				src.layouts[hklEdited] = tt.layout(t)
			} else {
				src.errs[hklEdited] = errLayout
			}
			r, warns := newResolver(src)
			c := keys.MustParse(tt.spec).Sided()
			want, _ := output.ChordStroke(c)
			for range 2 {
				if got := mustResolve(t, r, c); !strokesEqual(got, want) {
					t.Errorf("Resolve(%v) = %s, want ChordStroke's %s", c, downText(got), downText(want))
				}
			}
			// One warning for the two presses.
			w := warns.take()
			if len(w) != 1 || w[0].Spec != c.String() || w[0].HKL != hklEdited ||
				!strings.Contains(w[0].Reason, tt.reason) || !strings.HasSuffix(w[0].Reason, "; sending the US key") {
				t.Fatalf("warnings %+v, want one for %s on %v about %q", w, c, hklEdited, tt.reason)
			}
			// The same spec on another layout warns again, then no more.
			src.layouts[hklEdited+1] = src.layouts[hklEdited]
			src.errs[hklEdited+1] = src.errs[hklEdited]
			src.hkl = hklEdited + 1
			mustResolve(t, r, c)
			mustResolve(t, r, c)
			if w := warns.take(); len(w) != 1 || w[0].HKL != hklEdited+1 {
				t.Errorf("warnings on another layout %+v, want one", w)
			}
			// Tables are read once; a failed read is tried on each press.
			wantCalls := 1
			if tt.layout == nil {
				wantCalls = 2
			}
			if src.calls[hklEdited] != wantCalls {
				t.Errorf("Layout(%v) called %d times for two presses, want %d", hklEdited, src.calls[hklEdited], wantCalls)
			}
		})
	}

	t.Run("chars error keeps letters", func(t *testing.T) {
		f := loadLayoutFixture(t, klidFR)
		src := newFakeLayouts(t)
		src.layouts[hklFR] = output.NewLayout(f.VKToScan, f.Chars, errChars)
		src.hkl = hklFR
		r, warns := newResolver(src)
		if got, want := downText(mustResolve(t, r, keys.MustParse("LCtrl+Z"))), "down LCtrl sc=0x1D; down Z sc=0x11"; got != want {
			t.Errorf("Resolve(LCtrl+Z) = %s, want %s", got, want)
		}
		if w := warns.take(); len(w) > 0 {
			t.Errorf("warnings for a letter: %+v", w)
		}
		// The chars given with the error are not used.
		if got, want := downText(mustResolve(t, r, keys.MustParse("["))), "down [ sc=0x1A"; got != want {
			t.Errorf("Resolve([) = %s, want the US key: %s", got, want)
		}
		if w := warns.take(); len(w) != 1 {
			t.Errorf("warnings %+v, want one", w)
		}
	})

	t.Run("foreground error", func(t *testing.T) {
		src := newFakeLayouts(t)
		src.fgErr = errors.New("test: no foreground")
		r, warns := newResolver(src)
		for _, spec := range []string{"LCtrl+Z", "LCtrl+Z", "Plus"} {
			c := keys.MustParse(spec)
			want, _ := output.ChordStroke(c)
			if got := mustResolve(t, r, c); !strokesEqual(got, want) {
				t.Errorf("Resolve(%v) = %s, want %s", c, downText(got), downText(want))
			}
		}
		w := warns.take()
		if len(w) != 2 || w[0].Spec != "LCtrl+Z" || w[1].Spec != "Plus" || w[0].HKL != 0 || w[1].HKL != 0 ||
			!strings.Contains(w[0].Reason, "test: no foreground") {
			t.Errorf("warnings %+v, want one for LCtrl+Z and one for Plus, with HKL 0", w)
		}
		if len(src.calls) != 0 {
			t.Errorf("Layout calls %v without a foreground layout", src.calls)
		}
	})

	t.Run("nil layout", func(t *testing.T) {
		src := newFakeLayouts(t)
		src.layouts[hklFR] = nil
		src.hkl = hklFR
		r, warns := newResolver(src)
		want, _ := output.ChordStroke(keys.MustParse("Z"))
		if got := mustResolve(t, r, keys.MustParse("Z")); !strokesEqual(got, want) {
			t.Errorf("Resolve(Z) = %s, want %s", downText(got), downText(want))
		}
		if w := warns.take(); len(w) != 1 {
			t.Errorf("warnings %+v, want one", w)
		}
	})

	t.Run("nil Warn", func(t *testing.T) {
		src := newFakeLayouts(t)
		src.fgErr = errors.New("test: no foreground")
		r := output.NewResolver(src, output.ResolverOptions{})
		want, _ := output.ChordStroke(keys.MustParse("Z"))
		if got := mustResolve(t, r, keys.MustParse("Z")); !strokesEqual(got, want) {
			t.Errorf("Resolve(Z) = %s, want %s", downText(got), downText(want))
		}
	})
}

// Shift+Ctrl+Alt characters get all three modifiers.
func TestResolverShiftAltGr(t *testing.T) {
	src := newFakeLayouts(t)
	src.layouts[hklFR] = editedLayout(t, func(vks []kblayout.VKEntry, chars []kblayout.CharEntry) []kblayout.VKEntry {
		editChar(chars, '[', func(e *kblayout.CharEntry) { e.Shift = true })
		return vks
	})
	src.hkl = hklFR
	r, warns := newResolver(src)
	for _, tt := range []struct{ spec, want string }{
		{"[", "down LShift sc=0x2A; down LCtrl sc=0x1D; down LAlt sc=0x38; down [ sc=0x06"},
		{"RShift+RAlt+[", "down RShift sc=0x36; down RAlt sc=E0 38; down LCtrl sc=0x1D; down [ sc=0x06"},
		{"LCtrl+LShift+LAlt+[", "down LCtrl sc=0x1D; down LShift sc=0x2A; down LAlt sc=0x38; down [ sc=0x06"},
	} {
		st := mustResolve(t, r, keys.MustParse(tt.spec))
		checkStrokeShape(t, tt.spec, st)
		if got := downText(st); got != tt.want {
			t.Errorf("Resolve(%s):\n got %s\nwant %s", tt.spec, got, tt.want)
		}
	}
	if w := warns.take(); len(w) > 0 {
		t.Errorf("warnings: %v", w)
	}
}

func TestResolverCache(t *testing.T) {
	z := keys.MustParse("Z")
	t.Run("per HKL", func(t *testing.T) {
		src := newFakeLayouts(t)
		r, _ := newResolver(src)
		mustResolve(t, r, z)
		mustResolve(t, r, z)
		if src.fgCalls != 2 || src.calls[hklUS] != 1 {
			t.Errorf("%d Foreground and %d Layout calls for two presses, want 2 and 1", src.fgCalls, src.calls[hklUS])
		}
		// A switch inside one window sends no event: the next press
		// already uses the new layout.
		src.hkl = hklFR
		if got := downText(mustResolve(t, r, z)); got != "down Z sc=0x11" {
			t.Errorf("Z after switching to 040C = %s, want sc=0x11", got)
		}
		src.hkl = hklUS
		if got := downText(mustResolve(t, r, z)); got != "down Z sc=0x2C" {
			t.Errorf("Z after switching back = %s, want sc=0x2C", got)
		}
		if src.calls[hklUS] != 1 || src.calls[hklFR] != 1 {
			t.Errorf("Layout calls %v, want one per HKL", src.calls)
		}
	})

	t.Run("least recently used", func(t *testing.T) {
		src := newFakeLayouts(t)
		r, _ := newResolver(src)
		us := src.layouts[hklUS]
		const n = 16 // the cache size
		for h := output.HKL(1); h <= n+1; h++ {
			src.layouts[h] = us
		}
		press := func(h output.HKL) {
			t.Helper()
			src.hkl = h
			mustResolve(t, r, z)
		}
		for h := output.HKL(1); h <= n; h++ {
			press(h)
		}
		press(1)     // 2 is now the least recently used
		press(n + 1) // evicts 2
		press(1)
		press(3)
		for h := output.HKL(1); h <= n+1; h++ {
			if src.calls[h] != 1 {
				t.Errorf("Layout(%v) called %d times, want 1", h, src.calls[h])
			}
		}
		press(2) // reloaded, evicts 4
		press(4)
		if src.calls[2] != 2 || src.calls[4] != 2 {
			t.Errorf("Layout calls after eviction: 2 → %d, 4 → %d; want 2 each", src.calls[2], src.calls[4])
		}
	})

	// A layout that could not be read for a moment, such as one that was
	// not loaded yet, resolves on the next press.
	t.Run("failed layout read again", func(t *testing.T) {
		src := newFakeLayouts(t)
		r, warns := newResolver(src)
		src.hkl = hklFR
		src.errs[hklFR] = errors.New("test: not loaded")
		for range 2 {
			if got := downText(mustResolve(t, r, z)); got != "down Z sc=0x2C" {
				t.Errorf("Z while 040C fails = %s, want the US key", got)
			}
		}
		if w := warns.take(); len(w) != 1 {
			t.Errorf("warnings %+v, want one", w)
		}
		delete(src.errs, hklFR)
		mustResolve(t, r, z)
		if got := downText(mustResolve(t, r, z)); got != "down Z sc=0x11" {
			t.Errorf("Z once 040C reads = %s, want sc=0x11", got)
		}
		if src.calls[hklFR] != 3 {
			t.Errorf("Layout(040C) called %d times, want 3: twice failing, then once", src.calls[hklFR])
		}
		// A failure takes no cache slot: the 16 layouts read before it
		// are all still cached.
		for h := output.HKL(1); h <= 15; h++ {
			src.layouts[h] = src.layouts[hklUS]
			src.hkl = h
			mustResolve(t, r, z)
		}
		src.hkl = 0xBAD
		mustResolve(t, r, z)
		for h := output.HKL(1); h <= 15; h++ {
			src.hkl = h
			mustResolve(t, r, z)
		}
		src.hkl = hklFR
		mustResolve(t, r, z)
		for h := output.HKL(1); h <= 15; h++ {
			if src.calls[h] != 1 {
				t.Errorf("Layout(%v) called %d times, want 1", h, src.calls[h])
			}
		}
		if src.calls[hklFR] != 3 {
			t.Errorf("Layout(040C) called %d times after a failure elsewhere, want still 3", src.calls[hklFR])
		}
	})

	t.Run("warnings bounded", func(t *testing.T) {
		mods := []string{"", "LCtrl+", "LShift+", "LAlt+", "LWin+", "LCtrl+LShift+", "LCtrl+LAlt+", "RCtrl+", "RShift+", "RAlt+"}
		n := len(charKeys()) * len(mods) // 480 specs
		src := newFakeLayouts(t)
		r, warns := newResolver(src)
		round := func(hkls ...output.HKL) int {
			t.Helper()
			for _, h := range hkls {
				src.errs[h] = errors.New("test: bad layout")
				src.hkl = h
				for _, k := range charKeys() {
					for _, m := range mods {
						mustResolve(t, r, chordOf(t, m, k))
					}
				}
			}
			return len(warns.take())
		}
		// Within the bound each (spec, HKL) warns once.
		if got := round(1); got != n {
			t.Errorf("first round: %d warnings, want %d", got, n)
		}
		if got := round(1); got != 0 {
			t.Errorf("second round: %d warnings, want 0", got)
		}
		// Beyond it the warned set is forgotten, so warnings come again
		// instead of the set growing without bound.
		if got := round(2, 3, 4); got != 3*n {
			t.Errorf("%d warnings for %d new (spec, HKL) pairs, want %d", got, 3*n, 3*n)
		}
		if got := round(1); got == 0 {
			t.Error("no warnings again after the warned set overflowed")
		}
	})
}

func TestResolverInvalid(t *testing.T) {
	src := newFakeLayouts(t)
	r, warns := newResolver(src)
	_, want := output.ChordStroke(keys.Chord{})
	if _, err := r.Resolve(keys.Chord{}); err == nil || err.Error() != want.Error() {
		t.Errorf("Resolve(zero Chord) = %v, want ChordStroke's %v", err, want)
	}
	if src.fgCalls != 0 || len(warns.take()) != 0 {
		t.Errorf("an invalid chord asked for the layout or warned")
	}
}

func TestHKLString(t *testing.T) {
	for _, tt := range []struct {
		h    output.HKL
		want string
	}{
		{0, "0x00000000"},
		{hklUS, "0x04090409"},
		{hklFR, "0x040C040C"},
		{0xF0020409, "0xF0020409"},
		{0xFFFFFFFFF0020409, "0xFFFFFFFFF0020409"},
	} {
		if got := tt.h.String(); got != tt.want {
			t.Errorf("HKL(%#x).String() = %q, want %q", uint64(tt.h), got, tt.want)
		}
	}
}

// sentLog reads the events a Recorder injected, as KeyEvent strings.
type sentLog struct {
	rec *simtest.Recorder
	n   int
}

// expect compares the events injected since the last expect with want,
// joined by "; ".
func (l *sentLog) expect(t *testing.T, step, want string) {
	t.Helper()
	recs := l.rec.Records()
	var got []string
	for _, r := range recs[l.n:] {
		if r.Injected {
			got = append(got, r.Event.String())
		}
	}
	l.n = len(recs)
	if s := strings.Join(got, "; "); s != want {
		t.Errorf("%s:\n got %s\nwant %s", step, s, want)
	}
}

// A layout switch between a press and its release must not change what is
// released (CLAUDE.md invariant 1, ADR-0003).
func TestResolverLayoutSwitchReleasesPressedKey(t *testing.T) {
	src := newFakeLayouts(t)
	r, warns := newResolver(src)
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{Resolve: r.Resolve}))
	sent := &sentLog{rec: s.Out}

	// Held across a switch: the release is the pressed scan code.
	s.down(ownerA, "Ctrl+Z")
	sent.expect(t, "Down(Ctrl+Z) on US", "down LCtrl sc=0x1D; down Z sc=0x2C")
	src.hkl = hklFR
	s.up(ownerA, "Ctrl+Z")
	sent.expect(t, "Up(Ctrl+Z) on 040C", "up   Z sc=0x2C; up   LCtrl sc=0x1D")

	// Z on US and W on 040C are one physical key: it goes up with its
	// last reference, once.
	src.hkl = hklUS
	s.down(ownerA, "Z")
	sent.expect(t, "Down(A, Z) on US", "down Z sc=0x2C")
	src.hkl = hklFR
	s.down(ownerB, "W")
	sent.expect(t, "Down(B, W) on 040C", "")
	s.up(ownerA, "Z")
	sent.expect(t, "Up(A, Z)", "")
	s.up(ownerB, "W")
	sent.expect(t, "Up(B, W)", "up   Z sc=0x2C")

	// A tap on 040C presses and releases 040C's key.
	if err := s.Arb.Tap(ownerA, keys.MustParse("Ctrl+Z"), 0); err != nil {
		t.Fatal(err)
	}
	sent.expect(t, "Tap(Ctrl+Z) on 040C", "down LCtrl sc=0x1D; down Z sc=0x11; up   Z sc=0x11; up   LCtrl sc=0x1D")

	// ReleaseAll after a switch releases what was pressed: an AltGr
	// character and a timed tap.
	s.down(ownerA, "[")
	sent.expect(t, "Down([) on 040C", "down LCtrl sc=0x1D; down LAlt sc=0x38; down [ sc=0x06")
	if err := s.Arb.Tap(ownerB, keys.MustParse("Ctrl+Z"), 50*ms); err != nil {
		t.Fatal(err)
	}
	sent.expect(t, "Tap(B, Ctrl+Z, 50ms) on 040C", "down Z sc=0x11")
	src.hkl = hklUS
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	sent.expect(t, "ReleaseAll on US", "up   Z sc=0x11; up   [ sc=0x06; up   LAlt sc=0x38; up   LCtrl sc=0x1D")
	if held := s.Arb.Held(); len(held) != 0 {
		t.Errorf("held after ReleaseAll: %v", held)
	}
	s.Settle()
	sent.expect(t, "Settle", "")
	if w := warns.take(); len(w) > 0 {
		t.Errorf("warnings: %v", w)
	}
}

func TestNewResolverNilSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewResolver(nil) did not panic")
		}
	}()
	output.NewResolver(nil, output.ResolverOptions{})
}

// A mouse button is no character either: it bypasses the layout like the wheel
// keys, and its release is a real event, not a silent one (ADR-0062).
func TestResolveButtons(t *testing.T) {
	for _, tt := range []struct {
		spec string // the sided chord the Arbiter resolves
		want string
	}{
		{"MouseLeft", "down MouseLeft"},
		{"MouseRight", "down MouseRight"},
		{"MouseMiddle", "down MouseMiddle"},
		{"MouseX1", "down MouseX1"},
		{"MouseX2", "down MouseX2"},
		{"LCtrl+MouseLeft", "down LCtrl sc=0x1D; down MouseLeft"},
		{"LCtrl+LShift+MouseRight", "down LCtrl sc=0x1D; down LShift sc=0x2A; down MouseRight"},
		{"RAlt+XButton1", "down RAlt sc=E0 38; down MouseX1"},
	} {
		src := newFakeLayouts(t)
		src.fgErr = errors.New("test: no foreground layout")
		r, warns := newResolver(src)
		c := keys.MustParse(tt.spec)
		st := mustResolve(t, r, c)
		checkStrokeShape(t, tt.spec, st)
		if got := downText(st); got != tt.want {
			t.Errorf("Resolve(%s) = %s, want %s", tt.spec, got, tt.want)
		}
		if want, err := output.ChordStroke(c); err != nil || !strokesEqual(st, want) {
			t.Errorf("Resolve(%s) = %s, want ChordStroke's %s (%v)", tt.spec, downText(st), downText(want), err)
		}
		if src.fgCalls != 0 || len(src.calls) != 0 {
			t.Errorf("%s: %d Foreground and %v Layout calls, want none: a button is not a character", tt.spec, src.fgCalls, src.calls)
		}
		if w := warns.take(); len(w) > 0 {
			t.Errorf("%s: warnings %v, want none", tt.spec, w)
		}
		m, ok := c.Key().Mouse()
		last := st.Down[len(st.Down)-1]
		if !ok || !m.IsButton() || last.Mouse != m || last.Scan != 0 || last.ByVK || last.Key != c.Key() || last.Up {
			t.Errorf("%s: last press %+v, want the bare press of the button %v", tt.spec, last, m)
		}
		if up := st.Up[0]; up.Silent() || up.Mouse != m || !up.Up {
			t.Errorf("%s: first release %+v, want the real release of %v", tt.spec, up, m)
		}
	}
}

// A wheel key is no character: it bypasses the layout, so the Resolver never
// asks for the foreground layout, not even when it cannot be read, and gives
// ChordStroke's stroke on every layout (ADR-0062).
func TestResolveWheelKeys(t *testing.T) {
	for _, tt := range []struct {
		spec string // the sided chord the Arbiter resolves
		want string
	}{
		{"WheelUp", "down WheelUp"},
		{"WheelDown", "down WheelDown"},
		{"WheelLeft", "down WheelLeft"},
		{"WheelRight", "down WheelRight"},
		{"LCtrl+WheelUp", "down LCtrl sc=0x1D; down WheelUp"},
		{"LCtrl+LShift+WheelDown", "down LCtrl sc=0x1D; down LShift sc=0x2A; down WheelDown"},
		{"RAlt+WheelLeft", "down RAlt sc=E0 38; down WheelLeft"},
	} {
		src := newFakeLayouts(t)
		src.fgErr = errors.New("test: no foreground layout")
		r, warns := newResolver(src)
		c := keys.MustParse(tt.spec)
		st := mustResolve(t, r, c)
		checkStrokeShape(t, tt.spec, st)
		if got := downText(st); got != tt.want {
			t.Errorf("Resolve(%s) = %s, want %s", tt.spec, got, tt.want)
		}
		if want, err := output.ChordStroke(c); err != nil || !strokesEqual(st, want) {
			t.Errorf("Resolve(%s) = %s, want ChordStroke's %s (%v)", tt.spec, downText(st), downText(want), err)
		}
		if src.fgCalls != 0 || len(src.calls) != 0 {
			t.Errorf("%s: %d Foreground and %v Layout calls, want none: a wheel key is not a character", tt.spec, src.fgCalls, src.calls)
		}
		if w := warns.take(); len(w) > 0 {
			t.Errorf("%s: warnings %v, want none", tt.spec, w)
		}
		// The press is a mouse event, and its release the silent one.
		m, ok := c.Key().Mouse()
		last := st.Down[len(st.Down)-1]
		if !ok || last.Mouse != m || last.Scan != 0 || last.ByVK || last.Key != c.Key() || last.Up {
			t.Errorf("%s: last press %+v, want the bare press of %v", tt.spec, last, m)
		}
		if up := st.Up[0]; !up.Silent() || up.Mouse != m {
			t.Errorf("%s: first release %+v, want the silent release of %v", tt.spec, up, m)
		}
	}
}

// A media or volume key is no character either (OUT-20): it bypasses the layout,
// so the Resolver never asks for the foreground layout, not even when it cannot
// be read, and gives ChordStroke's stroke on every layout: the VK with the E0
// scan code, after the modifiers.
func TestResolveMediaKeys(t *testing.T) {
	for _, tt := range []struct {
		spec string // the sided chord the Arbiter resolves
		want string
	}{
		{"VolumeUp", "down VolumeUp vk=0xAF sc=E0 30"},
		{"VolumeDown", "down VolumeDown vk=0xAE sc=E0 2E"},
		{"VolumeMute", "down VolumeMute vk=0xAD sc=E0 20"},
		{"MediaPlayPause", "down MediaPlayPause vk=0xB3 sc=E0 22"},
		{"MediaNext", "down MediaNext vk=0xB0 sc=E0 19"},
		{"MediaPrevious", "down MediaPrevious vk=0xB1 sc=E0 10"},
		{"MediaStop", "down MediaStop vk=0xB2 sc=E0 24"},
		{"LCtrl+VolumeUp", "down LCtrl sc=0x1D; down VolumeUp vk=0xAF sc=E0 30"},
		{"LCtrl+LShift+MediaNext", "down LCtrl sc=0x1D; down LShift sc=0x2A; down MediaNext vk=0xB0 sc=E0 19"},
		{"RAlt+MediaPlayPause", "down RAlt sc=E0 38; down MediaPlayPause vk=0xB3 sc=E0 22"},
	} {
		src := newFakeLayouts(t)
		src.fgErr = errors.New("test: no foreground layout")
		r, warns := newResolver(src)
		c := keys.MustParse(tt.spec)
		st := mustResolve(t, r, c)
		checkStrokeShape(t, tt.spec, st)
		if got := downText(st); got != tt.want {
			t.Errorf("Resolve(%s) = %s, want %s", tt.spec, got, tt.want)
		}
		if want, err := output.ChordStroke(c); err != nil || !strokesEqual(st, want) {
			t.Errorf("Resolve(%s) = %s, want ChordStroke's %s (%v)", tt.spec, downText(st), downText(want), err)
		}
		if src.fgCalls != 0 || len(src.calls) != 0 {
			t.Errorf("%s: %d Foreground and %v Layout calls, want none: a media key is not a character", tt.spec, src.fgCalls, src.calls)
		}
		if w := warns.take(); len(w) > 0 {
			t.Errorf("%s: warnings %v, want none", tt.spec, w)
		}
		if last := st.Down[len(st.Down)-1]; !last.ByVK || !last.Ext || last.Key != c.Key() || last.Up {
			t.Errorf("%s: last press %+v, want the VK press of %v with its E0 scan code", tt.spec, last, c.Key())
		}
	}
}
