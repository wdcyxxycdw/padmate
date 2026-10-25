// Package keycapture converts short-lived native keyboard observations into
// sided KeySpec chords. It never injects or suppresses keyboard input.
package keycapture

import (
	"fmt"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

type Event struct {
	Active        bool   `json:"active"`
	Chord         string `json:"chord,omitempty"`
	PhysicalChord string `json:"physicalChord,omitempty"`
	VK            uint32 `json:"vk,omitempty"`
	ScanCode      uint32 `json:"scanCode,omitempty"`
	Extended      bool   `json:"extended,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type State struct {
	modifiers keys.Mods
	down      map[uint32]bool
}

func keyFor(e winhook.Event) (keys.Key, bool) {
	// Distinguish keypad Enter before using the VK lookup.
	if e.VK == 0x0d && e.Ext() {
		return keys.Lookup("NumEnter")
	}
	for _, k := range keys.Table() {
		if k.Kind() == keys.KindPhysical || k.Kind() == keys.KindMouse || k.US().Shift {
			continue
		}
		if k.IsModifier() && (k.String() == "Ctrl" || k.String() == "Alt" || k.String() == "Shift" || k.String() == "Win") {
			continue
		}
		if uint32(k.US().VK) == e.VK {
			return k, true
		}
	}
	return keys.Key{}, false
}

// Step emits only newly pressed physical keys, ignoring repeat and injected
// output. Key-up changes modifier state but never overwrites the captured chord.
func (s *State) Step(e winhook.Event) (Event, bool) {
	if e.Injected() {
		return Event{}, false
	}
	if s.down == nil {
		s.down = make(map[uint32]bool)
	}
	k, ok := keyFor(e)
	if e.Up() {
		delete(s.down, e.VK)
		if ok {
			s.modifiers &^= k.Mod()
		}
		return Event{}, false
	}
	if s.down[e.VK] {
		return Event{}, false
	}
	s.down[e.VK] = true
	if ok && k.IsModifier() {
		s.modifiers |= k.Mod()
	}
	parts := []string{}
	for _, m := range s.modifiers.Keys() {
		parts = append(parts, m.String())
	}
	scan := e.Scan
	if e.Ext() {
		scan |= 0xe000
	}
	physical := fmt.Sprintf("sc:0x%02X", scan)
	name := physical
	if ok {
		name = k.String()
	}
	if !ok || !k.IsModifier() {
		parts = append(parts, name)
	}
	chord, err := keys.Parse(strings.Join(parts, "+"))
	if err != nil {
		return Event{}, false
	}
	physicalParts := append([]string{}, parts...)
	if !ok || !k.IsModifier() {
		physicalParts[len(physicalParts)-1] = physical
	}
	return Event{Active: true, Chord: chord.String(), PhysicalChord: strings.Join(physicalParts, "+"), VK: e.VK, ScanCode: e.Scan, Extended: e.Ext()}, true
}
