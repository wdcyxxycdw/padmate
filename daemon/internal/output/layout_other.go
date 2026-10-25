//go:build !windows

package output

import "errors"

var errNoLayouts = errors.New("output: no keyboard layouts on this platform")

// SystemLayouts returns a LayoutSource without layouts: every character
// key falls back to its US key, with a warning.
func SystemLayouts() LayoutSource { return noLayouts{} }

type noLayouts struct{}

func (noLayouts) Foreground() (HKL, error)    { return 0, errNoLayouts }
func (noLayouts) Layout(HKL) (*Layout, error) { return nil, errNoLayouts }
