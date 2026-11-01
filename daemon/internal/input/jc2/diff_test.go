package jc2

import (
	"slices"
	"testing"
)

// apply plays changes onto s.
func apply(s State, changes []Change) State {
	for _, c := range changes {
		switch {
		case c.Control.IsAxis():
			s.Axes[c.Control-firstAxis] = c.Value
		case c.Down:
			s.Buttons |= 1 << c.Control
		default:
			s.Buttons &^= 1 << c.Control
		}
	}
	return s
}

func stateOf(buttons []Control, axes map[Control]int16) State {
	var s State
	for _, c := range buttons {
		s.Buttons |= 1 << c
	}
	for c, v := range axes {
		s.Axes[c-firstAxis] = v
	}
	return s
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name       string
		prev, next State
		want       []Change
	}{
		{"nothing", State{}, State{}, nil},
		{"same", stateOf([]Control{ControlSouth}, map[Control]int16{ControlLeftX: 5}),
			stateOf([]Control{ControlSouth}, map[Control]int16{ControlLeftX: 5}), nil},
		{"down", State{}, stateOf([]Control{ControlEast}, nil),
			[]Change{{Control: ControlEast, Down: true}}},
		{"up", stateOf([]Control{ControlEast}, nil), State{},
			[]Change{{Control: ControlEast}}},
		{"axis", State{}, stateOf(nil, map[Control]int16{ControlRightY: -300}),
			[]Change{{Control: ControlRightY, Value: -300}}},
		{"axis to zero", stateOf(nil, map[Control]int16{ControlLeftTrigger: 32767}), State{},
			[]Change{{Control: ControlLeftTrigger}}},
		{"buttons in Control order, then axes",
			stateOf([]Control{ControlStart, ControlSouth}, map[Control]int16{ControlRightTrigger: 7, ControlLeftX: 1}),
			stateOf([]Control{ControlMisc2, ControlStart, ControlDpadUp}, map[Control]int16{ControlLeftX: 2}),
			[]Change{
				{Control: ControlSouth},
				{Control: ControlDpadUp, Down: true},
				{Control: ControlMisc2, Down: true},
				{Control: ControlLeftX, Value: 2},
				{Control: ControlRightTrigger},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(tt.prev, tt.next)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Diff = %+v\nwant %+v", got, tt.want)
			}
			if applied := apply(tt.prev, got); applied != tt.next {
				t.Errorf("applying the changes gives %+v, want %+v", applied, tt.next)
			}
		})
	}
	// AppendDiff keeps what dst has.
	dst := []Change{{Control: ControlGuide, Down: true}}
	dst = AppendDiff(dst, State{}, stateOf([]Control{ControlSouth}, nil))
	if len(dst) != 2 || dst[0].Control != ControlGuide || dst[1].Control != ControlSouth {
		t.Errorf("AppendDiff = %+v", dst)
	}
}

func TestReleased(t *testing.T) {
	if got := Released(State{}); got != nil {
		t.Errorf("Released of rest = %+v", got)
	}
	// Every button and every axis held: everything comes back.
	var all State
	all.Buttons = 1<<firstAxis - 1
	for i := range all.Axes {
		all.Axes[i] = int16(-100 * (i + 1))
	}
	got := Released(all)
	if len(got) != int(numControls) {
		t.Fatalf("%d changes, want %d", len(got), numControls)
	}
	for i, c := range got {
		if c.Control != Control(i) || c.Down || c.Value != 0 {
			t.Errorf("change %d = %+v", i, c)
		}
	}
	if apply(all, got) != (State{}) {
		t.Error("Released does not bring the state to rest")
	}

	// A half-pushed stick and a held trigger.
	s := stateOf([]Control{ControlRightShoulder}, map[Control]int16{ControlRightX: 20000, ControlRightTrigger: 32767})
	want := []Change{{Control: ControlRightShoulder}, {Control: ControlRightX}, {Control: ControlRightTrigger}}
	if got := Released(s); !slices.Equal(got, want) {
		t.Errorf("Released = %+v, want %+v", got, want)
	}
}
