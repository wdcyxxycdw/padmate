package output

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Stroke is a chord as keyboard events: the presses, then the releases.
// Up releases exactly the keys of Down, in reverse order.
type Stroke struct {
	Down []KeyEvent
	Up   []KeyEvent
}

// Modifier bits of either side, and the generic one.
const (
	anyShift = keys.ModShift | keys.ModLShift | keys.ModRShift
	anyCtrl  = keys.ModCtrl | keys.ModLCtrl | keys.ModRCtrl
	anyAlt   = keys.ModAlt | keys.ModLAlt | keys.ModRAlt
)

// The modifiers a stroke may add to type a character.
var lshiftKey, lctrlKey, laltKey = mustLookup("LShift"), mustLookup("LCtrl"), mustLookup("LAlt")

func mustLookup(name string) keys.Key {
	k, ok := keys.Lookup(name)
	if !ok {
		panic("output: no key " + name)
	}
	return k
}

// ChordStroke returns the events that tap c on the US layout (OUT-05): the
// modifiers go down in canonical order, then the key; Up is the exact
// reverse. When the key needs Shift on the US layout (only Plus, which is
// Shift+=) and c holds no Shift of either side, LShift is pressed around
// the key. A chord made only of modifiers ("Ctrl+Shift") presses each of
// them once.
//
// No physical key is pressed twice: a key that maps to the same scan code
// as an earlier one, such as the raw sc:0x1D after Ctrl, is skipped.
//
// It is the stroke of every key that is not a character, and of a
// character when the Resolver cannot find it on the foreground layout.
func ChordStroke(c keys.Chord) (Stroke, error) {
	if !c.IsValid() {
		return Stroke{}, fmt.Errorf("output: invalid chord")
	}
	ks := c.Mods().Keys()
	if c.Key().US().Shift && c.Mods()&anyShift == 0 {
		ks = append(ks, lshiftKey)
	}
	down, err := addKeys(nil, append(ks, c.Key())...)
	if err != nil {
		return Stroke{}, err
	}
	return strokeOf(down), nil
}

// addKeys appends the US presses of ks to down, as addPress does.
func addKeys(down []KeyEvent, ks ...keys.Key) ([]KeyEvent, error) {
	for _, k := range ks {
		e, err := KeyEventFor(k, false)
		if err != nil {
			return nil, err
		}
		down = addPress(down, e)
	}
	return down, nil
}

// addPress appends e to down unless down already presses its physical
// key.
func addPress(down []KeyEvent, e KeyEvent) []KeyEvent {
	if pressed(down, e) {
		return down
	}
	return append(down, e)
}

// pressed reports whether down presses the physical key of e.
func pressed(down []KeyEvent, e KeyEvent) bool {
	for _, d := range down {
		if d.sameKey(e) {
			return true
		}
	}
	return false
}

// strokeOf returns the stroke that presses down and releases it in
// reverse.
func strokeOf(down []KeyEvent) Stroke {
	up := make([]KeyEvent, len(down))
	for i, d := range down {
		up[len(down)-1-i] = d.release()
	}
	return Stroke{Down: down, Up: up}
}
