package output

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// HKL is a Windows keyboard layout handle (an input locale identifier).
type HKL uintptr

// String formats h as kblayout.FormatHKL does: "0x04090409".
func (h HKL) String() string {
	return fmt.Sprintf("0x%08X", uint64(h))
}

// Layout holds the tables of one keyboard layout that resolving needs.
// It is immutable.
type Layout struct {
	vks      map[uint8]kblayout.VKEntry
	chars    map[rune]kblayout.CharEntry
	charsErr error
}

// NewLayout builds a Layout from the two tables of a kblayout.Fixture, or
// the same tables read from the system (kblayout.VKToScan and
// kblayout.Chars). charsErr non-nil means the chars table could not be
// read: chars is then ignored, and punctuation falls back to US.
func NewLayout(vkToScan []kblayout.VKEntry, chars []kblayout.CharEntry, charsErr error) *Layout {
	l := &Layout{vks: make(map[uint8]kblayout.VKEntry, len(vkToScan)), charsErr: charsErr}
	for _, e := range vkToScan {
		if e.VK <= 0xFF {
			l.vks[uint8(e.VK)] = e
		}
	}
	if charsErr == nil {
		l.chars = make(map[rune]kblayout.CharEntry, len(chars))
		for _, e := range chars {
			l.chars[rune(e.Code)] = e
		}
	}
	return l
}

// FixtureLayout returns the Layout of a recorded layout.
func FixtureLayout(f *kblayout.Fixture) *Layout {
	return NewLayout(f.VKToScan, f.Chars, nil)
}

// vkScan returns the scan code entry of vk, if the layout has one.
func (l *Layout) vkScan(vk uint8) (kblayout.VKEntry, bool) {
	e, ok := l.vks[vk]
	return e, ok
}

// char returns the key that types r, if the chars table has an entry for
// it. The entry can be Absent.
func (l *Layout) char(r rune) (kblayout.CharEntry, bool) {
	e, ok := l.chars[r]
	return e, ok
}

// LayoutSource tells the Resolver the foreground layout.
type LayoutSource interface {
	// Foreground returns the layout of the foreground window's thread.
	Foreground() (HKL, error)
	// Layout returns the tables of hkl. The Resolver caches them per HKL;
	// after an error it asks again on the next press.
	Layout(hkl HKL) (*Layout, error)
}

// ResolveWarning reports a character key the foreground layout could not
// resolve, which was sent as its US key instead.
type ResolveWarning struct {
	Spec   string // the chord as Resolve got it; from the Arbiter, sided: "LCtrl+["
	HKL    HKL    // 0 when the foreground layout is unknown
	Reason string // English, for the log
}

// ResolverOptions configures a Resolver.
type ResolverOptions struct {
	// Warn is called on the Resolve goroutine, once per (Spec, HKL) until
	// 1024 pairs have warned; the Resolver then forgets them and starts
	// over. It must not block. nil means no warnings.
	Warn func(ResolveWarning)
}

// Bounds of the Resolver's state.
const (
	layoutCacheSize = 16   // layouts, least recently used evicted first
	warnedLimit     = 1024 // (spec, HKL) pairs; then the set starts over
)

// Resolver resolves chords against the foreground keyboard layout
// (OUT-04, ADR-0003, ADR-0015). Its Resolve is an ArbiterOptions.Resolve.
//
// Only character keys depend on the layout. Letters and digits are found
// by VK: VK_A–VK_Z and VK_0–VK_9 are the same on every layout, and
// applications match shortcuts on them, so "Ctrl+1" on AZERTY is VK_1 with
// Ctrl and no Shift. Punctuation and Plus are found by the character, with
// the modifiers the layout needs to type it added: LShift for Shift, LCtrl
// and LAlt for AltGr, each unless the chord has that modifier on either
// side. Every other key, including the physical keys (KeyZ) and raw scan
// codes, is ChordStroke's.
//
// When the layout cannot give a character key, Resolve falls back to
// ChordStroke, the US key, and warns (see ResolverOptions.Warn): a layout
// problem never rejects a command.
//
// Each Resolve of a character key asks the source for the foreground
// layout, since switching layouts inside one window sends no focus event;
// the tables are cached per HKL. Failed reads are not cached: a layout
// that was not readable for a moment resolves on the next press. A
// Resolver is used from one goroutine, the Arbiter's; it is not locked.
type Resolver struct {
	src    LayoutSource
	warn   func(ResolveWarning)
	cache  []cachedLayout // least recently used first
	warned map[warnKey]struct{}
}

type cachedLayout struct {
	hkl    HKL
	layout *Layout
}

type warnKey struct {
	spec string
	hkl  HKL
}

// NewResolver returns a Resolver that reads layouts from src. It panics
// if src is nil.
func NewResolver(src LayoutSource, opts ResolverOptions) *Resolver {
	if src == nil {
		panic("output: NewResolver needs a LayoutSource")
	}
	return &Resolver{src: src, warn: opts.Warn, warned: map[warnKey]struct{}{}}
}

// Resolve returns the stroke of c on the foreground layout. The Arbiter
// gives it the sided chord and uses only Stroke.Down. It returns an error
// only for an invalid chord, as ChordStroke does.
func (r *Resolver) Resolve(c keys.Chord) (Stroke, error) {
	ch, ok := c.Key().Char()
	if !ok {
		return ChordStroke(c)
	}
	hkl, err := r.src.Foreground()
	if err != nil {
		return r.fallback(c, 0, fmt.Sprintf("the foreground layout is unknown: %v", err))
	}
	l, err := r.layout(hkl)
	if err != nil {
		return r.fallback(c, hkl, fmt.Sprintf("reading the layout: %v", err))
	}
	st, reason := l.stroke(c, ch)
	if reason != "" {
		return r.fallback(c, hkl, reason)
	}
	return st, nil
}

// fallback warns about c on hkl, unless it did before, and returns the US
// stroke of c.
func (r *Resolver) fallback(c keys.Chord, hkl HKL, reason string) (Stroke, error) {
	if r.warn != nil {
		k := warnKey{c.String(), hkl}
		if _, ok := r.warned[k]; !ok {
			if len(r.warned) >= warnedLimit {
				clear(r.warned)
			}
			r.warned[k] = struct{}{}
			r.warn(ResolveWarning{Spec: k.spec, HKL: hkl, Reason: reason + "; sending the US key"})
		}
	}
	return ChordStroke(c)
}

// layout returns the tables of hkl from the cache, or from the source on
// a miss. Only tables that were read are cached.
func (r *Resolver) layout(hkl HKL) (*Layout, error) {
	for i, e := range r.cache {
		if e.hkl == hkl {
			copy(r.cache[i:], r.cache[i+1:])
			r.cache[len(r.cache)-1] = e
			return e.layout, nil
		}
	}
	l, err := r.src.Layout(hkl)
	switch {
	case err != nil:
		return nil, err
	case l == nil:
		return nil, errors.New("the source returned no tables")
	}
	if len(r.cache) == layoutCacheSize {
		r.cache = append(r.cache[:0], r.cache[1:]...)
	}
	r.cache = append(r.cache, cachedLayout{hkl, l})
	return l, nil
}

// stroke returns the stroke of c, whose key types ch, on l. When l cannot
// give one it returns why.
func (l *Layout) stroke(c keys.Chord, ch rune) (Stroke, string) {
	var (
		scan  uint8
		ext   bool
		added []keys.Key
	)
	if vk, ok := letterDigitVK(ch); ok {
		e, ok := l.vkScan(vk)
		if !ok {
			return Stroke{}, fmt.Sprintf("the layout has no scan code for VK 0x%02X", vk)
		}
		if why := unusableScan(e.Scan, e.Ext, e.E1); why != "" {
			return Stroke{}, fmt.Sprintf("the layout gives VK 0x%02X %s", vk, why)
		}
		scan, ext = uint8(e.Scan), e.Ext
	} else {
		if l.charsErr != nil {
			return Stroke{}, fmt.Sprintf("the layout's character table could not be read: %v", l.charsErr)
		}
		e, ok := l.char(ch)
		if !ok || e.Absent {
			return Stroke{}, fmt.Sprintf("the layout has no key for %q", string(ch))
		}
		if e.Hankaku || e.Ctrl != e.Alt {
			return Stroke{}, fmt.Sprintf("the layout types %q with %s, which padmate does not add", string(ch), shiftState(e))
		}
		if why := unusableScan(e.Scan, e.Ext, false); why != "" {
			return Stroke{}, fmt.Sprintf("the layout types %q with VK %v, which has %s", string(ch), e.VK, why)
		}
		scan, ext = uint8(e.Scan), e.Ext
		mods := c.Mods()
		if e.Shift && mods&anyShift == 0 {
			added = append(added, lshiftKey)
		}
		if e.Ctrl && mods&anyCtrl == 0 {
			added = append(added, lctrlKey)
		}
		if e.Alt && mods&anyAlt == 0 {
			added = append(added, laltKey)
		}
	}
	down, err := addKeys(nil, append(c.Mods().Keys(), added...)...)
	if err != nil {
		return Stroke{}, err.Error()
	}
	main := KeyEvent{Key: c.Key(), Scan: scan, Ext: ext}
	if pressed(down, main) {
		return Stroke{}, fmt.Sprintf("the layout puts %v on the key of a modifier, %v", c.Key(), main.ID())
	}
	return strokeOf(append(down, main)), ""
}

// letterDigitVK returns the VK of a letter or digit: VK_A–VK_Z are the
// upper-case letters and VK_0–VK_9 the digits, on every layout.
func letterDigitVK(ch rune) (uint8, bool) {
	switch {
	case ch >= 'a' && ch <= 'z':
		return uint8(ch - 'a' + 'A'), true
	case ch >= '0' && ch <= '9':
		return uint8(ch), true
	}
	return 0, false
}

// unusableScan says why a layout's scan code cannot be injected as a
// character key, or returns "". A key injected by scan code must be one
// set-1 make code, and not a numpad code, whose VK follows NumLock.
func unusableScan(scan kblayout.Code, ext, e1 bool) string {
	switch {
	case e1:
		return fmt.Sprintf("the E1 scan code %v, which cannot be injected by scan code", scan)
	case scan == 0:
		return "no scan code"
	case scan > 0x7F:
		return fmt.Sprintf("the scan code %v, which cannot be injected", scan)
	case !ext && numLockScan(uint8(scan)):
		return fmt.Sprintf("the numpad scan code %v, which follows NumLock", scan)
	}
	return ""
}

// numLockScan reports whether scan (without E0) is a numpad code that the
// system translates by the NumLock state: 0x47–0x53 except NumSubtract
// and NumAdd.
func numLockScan(scan uint8) bool {
	return scan >= 0x47 && scan <= 0x53 && scan != 0x4A && scan != 0x4E
}

// shiftState formats the modifiers of a VkKeyScanExW result: "Ctrl+Alt".
func shiftState(e kblayout.CharEntry) string {
	var s []string
	for _, m := range []struct {
		on   bool
		name string
	}{{e.Shift, "Shift"}, {e.Ctrl, "Ctrl"}, {e.Alt, "Alt"}, {e.Hankaku, "Hankaku"}} {
		if m.on {
			s = append(s, m.name)
		}
	}
	return strings.Join(s, "+")
}
