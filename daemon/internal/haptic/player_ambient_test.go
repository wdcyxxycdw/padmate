package haptic

import (
	"fmt"
	"slices"
	"testing"
)

// The ambient claim of the player lights is what HUD-08 uses: the mode of the
// layer lights the player light of that number, while the profile's lights
// are on. These tests pin the Mixer's side of it: the claim follows the
// requests, is gated by the capability, is given back on every path, and
// never takes a player light that another controller holds.

func TestMixerAmbientPlayerFollowsTheMode(t *testing.T) {
	r := newRig(t, Caps{PlayerLED: true})
	r.f.player = 2 // the index SDL gave the controller
	set := func(ms, index int) {
		t.Helper()
		if err := r.m.SetPlayer(dv, Ambient, index, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	release := func(ms int) {
		t.Helper()
		if err := r.m.ReleasePlayer(dv, Ambient, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}

	release(0)
	r.expect() // nothing was changed, so there is nothing to give back

	set(10, 0) // mode 1
	r.expect("@10 player 0")
	set(20, 0) // the same mode again (a reload, a refresh)
	r.expect() // the controller still has the index: a read, and no write
	if r.f.reads != 2 {
		t.Errorf("%d reads of the player index, want two: before the first change, and for the refresh", r.f.reads)
	}
	set(30, 1) // mode 2
	r.expect("@30 player 1")
	set(40, 3) // mode 4
	r.expect("@40 player 3")
	release(50) // mode 5, or the lights are off in the profile
	r.expect("@50 player 2")
	release(60)
	r.expect()
	if r.f.reads != 2 {
		t.Errorf("%d reads of the player index, want two: a change or a restore reads nothing", r.f.reads)
	}

	// Another episode reads the index again, which has changed meanwhile.
	r.f.player = 0
	set(100, 2)
	r.expect("@100 player 2")
	release(110)
	r.expect("@110 player 0")
}

func TestMixerAmbientPlayerNeedsTheCapability(t *testing.T) {
	for _, caps := range []Caps{
		{Rumble: true, LED: true, RGBLED: true}, // Xbox mode, or a PS4 light bar
		{},
	} {
		t.Run(fmt.Sprintf("%+v", caps), func(t *testing.T) {
			r := newRig(t, caps)
			r.f.limits = &caps
			for i, index := range []int{0, 1, 3, -1} {
				if err := r.m.SetPlayer(dv, Ambient, index, r.at(10*i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.m.ReleasePlayer(dv, Ambient, r.at(100)); err != nil {
				t.Fatal(err)
			}
			if err := r.m.StopAll(r.at(110)); err != nil {
				t.Fatal(err)
			}
			// SDL's player indices are one table for all controllers: a call
			// for a controller without the lights would renumber the others.
			r.expect()
			if r.f.reads != 0 || len(r.f.violations) != 0 {
				t.Errorf("the controller was asked about its player lights (%d reads) or called: %v", r.f.reads, r.f.violations)
			}
		})
	}
}

func TestMixerAmbientPlayerIsGivenBackOnEveryPath(t *testing.T) {
	for _, tc := range []stopper{
		{"Cancel", func(r *rig, ms int) error { return r.m.Cancel(dv, Ambient, r.at(ms)) }, true},
		{"Stop", func(r *rig, ms int) error { return r.m.Stop(dv, r.at(ms)) }, true},
		{"Remove", func(r *rig, ms int) error { return r.m.Remove(dv, r.at(ms)) }, false},
		{"StopAll", func(r *rig, ms int) error { return r.m.StopAll(r.at(ms)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, Caps{PlayerLED: true})
			r.f.player = 1
			if err := r.m.SetPlayer(dv, Ambient, 2, r.at(0)); err != nil {
				t.Fatal(err)
			}
			if r.f.player != 2 {
				t.Fatalf("player index %d, want the claim 2", r.f.player)
			}
			if err := tc.stop(r, 10); err != nil {
				t.Fatal(err)
			}
			if r.f.player != 1 {
				t.Errorf("%s: player index %d, want the original 1", tc.name, r.f.player)
			}
			r.f.calls = nil
			r.run(10, 5000)
			r.expect()
		})
	}
}

// padsOnOneTable returns a Mixer with n controllers that share one table of
// player indices, as SDL's controllers do, controller i (1-based) having index
// i-1. Controller 1 is the rig's.
func padsOnOneTable(t *testing.T, n int) (*rig, *playerTable) {
	t.Helper()
	r := newRig(t, Caps{PlayerLED: true})
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i + 1
	}
	tbl := newPlayerTable(ids...)
	r.f.table, r.f.tableID = tbl, 1
	for i := 2; i <= n; i++ {
		r.m.Add(Device(i), &fakeEff{r: r, table: tbl, tableID: i}, Caps{PlayerLED: true})
	}
	return r, tbl
}

func TestMixerAmbientPlayerLeavesOtherControllersAlone(t *testing.T) {
	r, tbl := padsOnOneTable(t, 3)
	effOf := func(id int) *fakeEff { return r.m.devs[Device(id)].eff.(*fakeEff) }
	step := func(name string, id, index int, want []int, wantCalls ...string) {
		t.Helper()
		before := fmt.Sprint(tbl.slots)
		if err := r.m.SetPlayer(Device(id), Ambient, index, r.at(r.ms+10)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(tbl.slots, want) {
			t.Errorf("%s: the table is %v after %s, want %v", name, tbl.slots, before, want)
		}
		got := effOf(id).calls
		effOf(id).calls = nil
		if !slices.Equal(got, wantCalls) {
			t.Errorf("%s: calls %v, want %v", name, got, wantCalls)
		}
		for other := 1; other <= 3; other++ {
			if other != id && len(effOf(other).calls) != 0 {
				t.Errorf("%s: controller %d was called: %v", name, other, effOf(other).calls)
			}
		}
	}

	// All three are in mode 1, then 2, ... : only the one that has the light
	// of the mode shows it; the others stay where SDL put them.
	step("1 takes its own light", 1, 0, []int{1, 2, 3}, "@10 player 0")
	step("2 wants 1's light", 2, 0, []int{1, 2, 3})
	step("3 wants 1's light", 3, 0, []int{1, 2, 3})
	step("2 takes its own light", 2, 1, []int{1, 2, 3}, "@40 player 1")
	step("1 wants 2's light", 1, 1, []int{1, 2, 3})
	// A light that nobody holds is taken, and then the one it left is free.
	step("1 takes the free 4th light", 1, 3, []int{0, 2, 3, 1}, "@60 player 3")
	step("3 takes the freed 1st light", 3, 0, []int{3, 2, 0, 1}, "@70 player 0")
	step("2 wants the 1st light, 3 has it", 2, 0, []int{3, 2, 0, 1})

	// Not showing anything is not taking anything: -1 is always allowed.
	step("2 shows none", 2, -1, []int{3, 0, 0, 1}, "@90 player -1")
}

// The restore of one controller moves another to a free slot without a word,
// and the Mixer of that one still believes it shows what it wrote. A request
// for the mode it has already is looked at against SDL's table, not against
// what the Mixer wrote: it corrects the index if it may (a light that nobody
// else holds), and leaves it be if it may not.
func TestMixerAmbientPlayerRefreshCorrectsAnIndexAnotherControllerMoved(t *testing.T) {
	r := newRig(t, Caps{PlayerLED: true})
	tbl := newPlayerTable(1) // controller 1, the rig's, has index 0
	r.f.table, r.f.tableID = tbl, 1
	a := r.f
	b := &fakeEff{r: r, table: tbl, tableID: 2}
	set := func(dev Device, index, ms int) {
		t.Helper()
		if err := r.m.SetPlayer(dev, Ambient, index, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	state := func(step string, wantTable []int, wantA, wantB []string) {
		t.Helper()
		if !slices.Equal(tbl.slots, wantTable) {
			t.Errorf("%s: the table is %v, want %v", step, tbl.slots, wantTable)
		}
		if !slices.Equal(a.calls, wantA) {
			t.Errorf("%s: calls of 1 %v, want %v", step, a.calls, wantA)
		}
		if !slices.Equal(b.calls, wantB) {
			t.Errorf("%s: calls of 2 %v, want %v", step, b.calls, wantB)
		}
		a.calls, b.calls = nil, nil
	}

	set(1, 1, 0) // 1 shows mode 2 and leaves index 0
	state("1 in mode 2", []int{0, 1}, []string{"@0 player 1"}, nil)
	// 2 connects: SDL gives it the first free index, 0, and its mode 1 is the
	// light it has.
	tbl.set(2, tbl.firstFree())
	r.m.Add(2, b, Caps{PlayerLED: true})
	set(2, 0, 10)
	state("2 in mode 1", []int{2, 1}, nil, []string{"@10 player 0"})
	// 1 goes back to mode 1: 2 has the light, so 1 stays where it is.
	set(1, 0, 20)
	state("1 in mode 1", []int{2, 1}, nil, nil)
	// The lights of 1 go off (feedback.led): the restore gives 1 its index 0
	// back, not held back, and moves 2 to the free slot.
	if err := r.m.ReleasePlayer(1, Ambient, r.at(30)); err != nil {
		t.Fatal(err)
	}
	state("1 restored", []int{1, 2}, []string{"@30 player 0"}, nil)

	// 2 still believes it shows index 0. A refresh in the same mode finds that
	// it does not, and finds index 0 held by 1: the lights stay as they are,
	// and no write moves 1.
	set(2, 0, 40)
	state("refresh while 1 holds 0", []int{1, 2}, nil, nil)
	// 1 takes the light of mode 3, a free one, and index 0 is free.
	set(1, 2, 50)
	state("1 in mode 3", []int{0, 2, 1}, []string{"@50 player 2"}, nil)
	// The same refresh now corrects 2.
	set(2, 0, 60)
	state("refresh while 0 is free", []int{2, 0, 1}, nil, []string{"@60 player 0"})
	// And once it is right, a refresh writes nothing again.
	set(2, 0, 70)
	state("refresh when right", []int{2, 0, 1}, nil, nil)
}

func TestMixerAmbientPlayerSkippedClaimIsNotRestored(t *testing.T) {
	// A controller whose light was never written has nothing to give back,
	// and a skipped claim is tried again at the next request.
	r, tbl := padsOnOneTable(t, 2)
	e2 := r.m.devs[2].eff.(*fakeEff)
	if err := r.m.SetPlayer(2, Ambient, 0, r.at(0)); err != nil { // held by 1
		t.Fatal(err)
	}
	if err := r.m.ReleasePlayer(2, Ambient, r.at(10)); err != nil {
		t.Fatal(err)
	}
	if len(e2.calls) != 0 || e2.reads != 0 {
		t.Errorf("controller 2 was called (%v) or read (%d) for a claim it never showed", e2.calls, e2.reads)
	}
	if err := r.m.SetPlayer(2, Ambient, 0, r.at(20)); err != nil {
		t.Fatal(err)
	}
	// Controller 1 moves away; the same request now shows.
	if err := r.m.SetPlayer(1, Ambient, 3, r.at(30)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetPlayer(2, Ambient, 0, r.at(40)); err != nil {
		t.Fatal(err)
	}
	if want := []int{2, 0, 0, 1}; !slices.Equal(tbl.slots, want) {
		t.Errorf("table %v, want %v", tbl.slots, want)
	}
	// Giving the lights back restores each controller's own index.
	if err := r.m.StopAll(r.at(50)); err != nil {
		t.Fatal(err)
	}
	if got := tbl.indexOf(1); got != 0 {
		t.Errorf("controller 1 has index %d after StopAll, want its original 0", got)
	}
}
