package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Level is the seam between gesture recognition and a layer registry
// (ENG-21). Binding returns the binding of c on this level, or nil.
//
// The engine uses that binding as it is: it never passes through Compile.
// It must satisfy the same rules, which CheckBinding checks. In particular
// a gesture that runs after the control is released (release, tap and the
// multi-taps) only taps keys: a committed multi-tap runs on a copy of the
// control's state, so keys or a layer it held would never be released
// (MAP-07, invariant 1).
type Level interface {
	Name() string
	Binding(control.Control) *profile.Binding
}

// Registry returns active levels in bottom-to-top order. Every binding its
// levels return must pass CheckBinding (see Level).
type Registry interface{ Levels() []Level }
