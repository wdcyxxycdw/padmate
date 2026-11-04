package integration

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// UsedKeys returns the set of canonical chords (CanonicalKeys spelling) that
// the bindings of p send. p is the merged profile, so a parent's bindings are
// already in it. Items whose keys are not in the set are ignored when an
// integration is compared (ARCHITECTURE section 16.2).
//
// Which chords count: every chord a binding can type or hold, in the common
// layer, in every layer and in the function sets (both the old format's and
// the new one), for every gesture:
//   - a keys action: its Keys and each chord of its Sequence;
//   - a sequence action: each step that is a shortcut (waits and texts send no
//     chord; a typed text is not a shortcut of the software);
//   - not macros, scripts, layer, profile, mouse, HUD or text actions: a
//     macro's file and a script's code are not read here, and none of the
//     others sends a chord on its own.
//
// A chord counts however its action is used (hold or tap, with or without
// repeat): the software sees the same key.
func UsedKeys(p *profile.Profile) map[string]bool {
	used := map[string]bool{}
	if p == nil {
		return used
	}
	add := func(c keys.Chord) {
		if c.IsValid() {
			used[c.String()] = true
		}
	}
	visit := func(l *profile.Layer) {
		if l == nil {
			return
		}
		for _, b := range l.Bindings {
			for g := profile.GesturePress; int(g) < profile.GestureCount; g++ {
				a := b.Action(g)
				if a == nil {
					continue
				}
				switch a.Type {
				case profile.ActionKeys:
					add(a.Keys)
					for _, c := range a.Sequence {
						add(c)
					}
				case profile.ActionSequence:
					for _, s := range a.Steps {
						if s.Kind == keys.StepKeys {
							add(s.Chord)
						}
					}
				}
			}
		}
	}
	visit(p.Common)
	for _, l := range p.Layers {
		visit(l)
	}
	for _, l := range p.FunctionSets() {
		visit(l)
	}
	return used
}

// UsedItems returns the items whose keys are in used, in order. A nil used
// (no profile to look at) keeps every item.
func UsedItems(items []Item, used map[string]bool) []Item {
	if used == nil {
		return items
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		canon, err := CanonicalKeys(it.Keys)
		if err != nil {
			canon = it.Keys
		}
		if used[canon] {
			out = append(out, it)
		}
	}
	return out
}
