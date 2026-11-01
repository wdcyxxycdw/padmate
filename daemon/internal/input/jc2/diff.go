package jc2

// Change is one change between two States: a button control going down or
// up, or an axis taking a new value.
type Change struct {
	Control Control
	// Down is the new state of a button control.
	Down bool
	// Value is the new value of an axis control.
	Value int16
}

// Diff returns what changed from prev to next, in Control order (buttons,
// then axes); nil if nothing did.
func Diff(prev, next State) []Change { return AppendDiff(nil, prev, next) }

// AppendDiff appends the changes of Diff to dst and returns it.
func AppendDiff(dst []Change, prev, next State) []Change {
	if prev == next {
		return dst
	}
	for c := Control(0); c < firstAxis; c++ {
		if was, now := prev.Down(c), next.Down(c); was != now {
			dst = append(dst, Change{Control: c, Down: now})
		}
	}
	for i, v := range next.Axes {
		if v != prev.Axes[i] {
			dst = append(dst, Change{Control: firstAxis + Control(i), Value: v})
		}
	}
	return dst
}

// Released returns the changes that bring s back to rest: every button that
// is down goes up and every axis that is off zero goes to zero. T-input
// emits them when a controller goes away, so that nothing stays held.
func Released(s State) []Change { return Diff(s, State{}) }
