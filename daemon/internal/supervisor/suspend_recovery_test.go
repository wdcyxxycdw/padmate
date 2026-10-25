package supervisor_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestSuspendNeutralUsesReloadedProfile(t *testing.T) {
	for _, tc := range []struct {
		name, axis, analog string
		candidate          int16
		neutral            bool
	}{
		{"smaller stick deadzone", "leftx", `"ls":{"deadzone":0.05}`, 3276, false},
		{"larger stick deadzone", "leftx", `"ls":{"deadzone":0.3}`, 6553, true},
		{"smaller trigger release", "lefttrigger", `"lt":{"press":0.8,"release":0.05}`, 3276, false},
		{"larger trigger release", "lefttrigger", `"lt":{"press":0.8,"release":0.4}`, 9830, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := `"` + tc.axis + `+":{"press":{"type":"keys","keys":"Space","mode":"hold"}}`
			initial, err := compileBase(binding)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"reload","analog":{` + tc.analog + `},"layers":{"base":{` + binding + `}}}`))
			if parsed == nil {
				t.Fatalf("parse: %v", ds)
			}
			updated, err := engine.Compile(parsed)
			if err != nil {
				t.Fatal(err)
			}
			h := start(t, opts{program: initial})
			h.send(added(padA))
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			h.expect("+Space@0")
			h.set(10)
			h.sup.Suspend(winsession.Sleep)
			h.sync()
			h.send(axis(1, tc.axis, 0)) // synthetic neutral while asleep
			h.send(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
			h.sync()
			h.sup.Suspend(0)
			h.sync()
			h.reload(updated, 2) // real reload barriers, not direct router mutation
			h.send(axis(1, tc.axis, 28000))
			h.send(axis(1, tc.axis, tc.candidate))
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			want := "+Space@0 -Space@10"
			if tc.neutral {
				want += " +Space@10"
			}
			h.expect(want)
			h.send(axis(1, tc.axis, 0))
			if !tc.neutral {
				h.send(axis(1, tc.axis, 28000))
				h.sync()
				h.expect(want + " +Space@10")
				h.send(axis(1, tc.axis, 0))
			}
			h.sync()
			h.expect("+Space@0 -Space@10 +Space@10 -Space@10")
		})
	}
}

// SDL may report an artificial release when Windows disables XInput on
// the locked desktop. Its first wake sample may still be empty, followed
// by a held down after recovery has opened the mapping gate.
func TestSuspendSyntheticReleaseEmptySnapshotLateDown(t *testing.T) {
	for _, heldBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "held before suspend", false: "pressed during suspend"}[heldBefore], func(t *testing.T) {
			h := start(t, opts{})
			h.send(added(padA))
			if heldBefore {
				h.send(button(1, "east", true))
				h.sync()
				h.expect("+Space@0")
			}
			h.set(10)
			h.sup.Suspend(winsession.Lock | winsession.Sleep)
			h.sync()
			if !heldBefore {
				h.send(button(1, "east", true))
			}
			h.send(button(1, "east", false)) // synthetic, not owner release
			h.send(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
			h.sync()
			h.sup.Suspend(0)
			h.sync()
			// A newer system hold must not let an old empty scan or
			// the previous resume cycle discard the quarantine.
			h.sup.Suspend(winsession.Lock)
			h.sync()
			h.send(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
			h.sup.Suspend(0)
			h.sync()
			h.set(20)
			h.send(button(1, "east", true)) // late state of the same held button
			h.sync()
			want := ""
			if heldBefore {
				want = "+Space@0 -Space@10"
			}
			h.expect(want)
			h.set(1000000) // time alone cannot make a held button safe
			h.sync()
			h.expect(want)
			h.send(button(1, "east", false)) // owner release after fresh down
			h.send(button(1, "east", true))
			h.sync()
			if want != "" {
				want += " "
			}
			want += "+Space@1000000"
			h.expect(want)
			h.send(button(1, "east", false))
			h.sync()
			h.expect(want + " -Space@1000000")
		})
	}
}

func TestSuspendSyntheticCenterLateAxis(t *testing.T) {
	for _, scan := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlock without scan", true: "wake scan"}[scan], func(t *testing.T) {
			prog, err := compileBase(`"leftx+":{"press":{"type":"keys","keys":"Space","mode":"hold"}}`)
			if err != nil {
				t.Fatal(err)
			}
			h := start(t, opts{program: prog})
			h.send(added(padA))
			h.send(axis(1, "leftx", 28000))
			h.sync()
			h.expect("+Space@0")
			h.set(10)
			h.sup.Suspend(winsession.Sleep)
			h.sync()
			h.expect("+Space@0 -Space@10")
			h.send(axis(1, "leftx", 0)) // synthetic center
			if scan {
				h.send(input.Event{Kind: input.DeviceState, Device: 1, Snapshot: &input.GamepadState{}})
			}
			h.sync()
			h.sup.Suspend(0)
			h.sync()
			h.send(axis(1, "leftx", 28000))
			h.sync()
			h.expect("+Space@0 -Space@10")
			h.send(axis(1, "leftx", 1000)) // biased neutral inside profile deadzone
			h.send(axis(1, "leftx", 28000))
			h.sync()
			h.expect("+Space@0 -Space@10 +Space@10")
			h.send(axis(1, "leftx", 1000))
			h.sync()
			h.expect("+Space@0 -Space@10 +Space@10 -Space@10")
		})
	}
}

// A stick or trigger that moves while the system is suspended is active
// like a button pressed then, also for a gamepad that connected meanwhile
// (ADR-0033 decision 1): the resume masks it, so the router's state of the
// gamepad has the axis at 0 and nothing is mapped from it, whatever is sent
// while it stays deflected. It is released again by a neutral report, and
// the next deflection counts. FuzzRouter's model lacked this for axes; its
// regression seeds are 4c9f911a09dcb8b0 and 5937f17fcec5632d.
func TestSuspendAxisMovedDuringSuspendStaysMasked(t *testing.T) {
	for _, tc := range []struct {
		name, axis string
		a          control.Axis
	}{
		{"stick", "leftx", control.LeftX},
		{"trigger", "lefttrigger", control.LeftTrigger},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := compileBase(`"` + tc.axis + `+":{"press":{"type":"keys","keys":"Space","mode":"hold"}}`)
			if err != nil {
				t.Fatal(err)
			}
			h := start(t, opts{program: prog})
			routerAxis := func() int16 {
				t.Helper()
				states := h.states()
				if len(states) != 1 || !states[0].Assigned {
					t.Fatalf("sync states %+v", states)
				}
				if states[0].Got != states[0].Want {
					t.Fatalf("slot state %+v, router state %+v", states[0].Got, states[0].Want)
				}
				return states[0].Want.Axes[tc.a]
			}
			h.sup.Suspend(winsession.Lock)
			h.sync()
			h.send(added(padA))
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			h.expect("")
			h.sup.Suspend(0)
			h.sync()
			h.expect("")
			if got := routerAxis(); got != 0 {
				t.Fatalf("%s is %d after the resume, want it masked", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 28000)) // still deflected: swallowed
			h.send(axis(1, tc.axis, 30000))
			h.sync()
			h.expect("")
			if got := routerAxis(); got != 0 {
				t.Fatalf("%s is %d while it stays deflected, want it masked", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 0)) // neutral: the quarantine ends
			h.sync()
			h.expect("")
			if got := routerAxis(); got != 0 {
				t.Fatalf("%s is %d at neutral, want 0", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			h.expect("+Space@0")
			if got := routerAxis(); got != 28000 {
				t.Fatalf("%s is %d after a new deflection, want 28000", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 0))
			h.sync()
			h.expect("+Space@0 -Space@0")
		})
	}
}

// A stick or trigger that the suspension saw centered must be deflected
// again before a neutral report can end its quarantine (ADR-0033 decision 1):
// SDL's center while the input desktop is unavailable is synthetic, not
// proof that the owner let go, so the first reports after the resume are
// neutral or the late value of the same deflection, and neither maps. The
// existing TestSuspendSyntheticCenterLateAxis only sends the late deflection
// first, which clears the rule's flag by itself; here a neutral report comes
// first, which would end the quarantine if the suspension had not set it.
func TestSuspendCenteredDuringSuspendNeedsDeflectionFirst(t *testing.T) {
	for _, tc := range []struct {
		name, axis string
		a          control.Axis
		neutral    int16 // a value the quarantine counts as neutral, not 0
	}{
		{"stick", "leftx", control.LeftX, 1000},
		{"trigger", "lefttrigger", control.LeftTrigger, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := compileBase(`"` + tc.axis + `+":{"press":{"type":"keys","keys":"Space","mode":"hold"}}`)
			if err != nil {
				t.Fatal(err)
			}
			h := start(t, opts{program: prog})
			routerAxis := func() int16 {
				t.Helper()
				states := h.states()
				if len(states) != 1 || !states[0].Assigned {
					t.Fatalf("sync states %+v", states)
				}
				if states[0].Got != states[0].Want {
					t.Fatalf("slot state %+v, router state %+v", states[0].Got, states[0].Want)
				}
				return states[0].Want.Axes[tc.a]
			}
			h.send(added(padA))
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			h.expect("+Space@0")
			h.set(10)
			h.sup.Suspend(winsession.Lock)
			h.sync()
			h.expect("+Space@0 -Space@10")
			h.send(axis(1, tc.axis, 0)) // synthetic center while locked
			h.sync()
			h.sup.Suspend(0)
			h.sync()
			h.expect("+Space@0 -Space@10")
			// The first reports after the resume are neutral: they do not
			// end the quarantine, the center was not the owner's.
			h.send(axis(1, tc.axis, 0))
			h.send(axis(1, tc.axis, tc.neutral))
			h.send(axis(1, tc.axis, 28000)) // the deflection that was never let go of
			h.sync()
			h.expect("+Space@0 -Space@10")
			if got := routerAxis(); got != 0 {
				t.Fatalf("%s is %d, want it masked: it was centered during the suspension and not deflected again", tc.axis, got)
			}
			// Seen deflected, a neutral report now ends the quarantine, and
			// the next deflection maps.
			h.send(axis(1, tc.axis, tc.neutral))
			h.sync()
			h.expect("+Space@0 -Space@10")
			if got := routerAxis(); got != tc.neutral {
				t.Fatalf("%s is %d, want the neutral report to be taken", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 28000))
			h.sync()
			h.expect("+Space@0 -Space@10 +Space@10")
			if got := routerAxis(); got != 28000 {
				t.Fatalf("%s is %d after the new deflection, want 28000", tc.axis, got)
			}
			h.send(axis(1, tc.axis, 0))
			h.sync()
			h.expect("+Space@0 -Space@10 +Space@10 -Space@10")
		})
	}
}
