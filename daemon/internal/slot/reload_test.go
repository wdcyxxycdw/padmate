package slot_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// Bindings of the reloaded profiles.
const (
	eastShift = `"east": {"label": "移动", "press": {"type": "keys", "keys": "Shift", "mode": "hold"}}`
	southRedo = `"south": {"label": "重做", "press": {"type": "keys", "keys": "Ctrl+Y", "mode": "tap"}}`
	ltCtrl    = `"lt": {"press": {"type": "keys", "keys": "Ctrl"}}`
)

// owners returns the owners of the held keys, formatted.
func owners(arb *output.Arbiter) []string {
	var got []string
	for _, k := range arb.Held() {
		for _, o := range k.Owners {
			got = append(got, o.String())
		}
	}
	return got
}

// TestReloadWhileHeld: a reload lets go of the held keys once, the
// controls held across it do nothing until pressed again, and later
// presses use the new program with the new generation.
func TestReloadWhileHeld(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl, ltAlt)
	h.replay(t, "+east@0 +lb@10 lefttrigger=0.5@20")
	h.ExpectOutput("+Space@0 +LCtrl@10 +LAlt@20")
	h.at(t, 100)
	if err := h.s.Reload(compile(t, eastShift, ltCtrl), 2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	h.ExpectOutput("-Space@100 -LAlt@100 -LCtrl@100")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if g := h.s.Gen(); g != 2 {
		t.Errorf("Gen = %d after the reload, want 2", g)
	}
	// The releases send nothing; the trigger presses again only after it
	// falls to the release threshold, with the new binding.
	h.replay(t, "-east@200 -lb@210 lefttrigger=1@220 lefttrigger=0.2@230 lefttrigger=0.3@240")
	h.ExpectOutput("+LCtrl@240")
	// lb has no binding any more.
	h.replay(t, "+east@300 +lb@310")
	h.ExpectOutput("+LShift@300")
	if got, want := owners(h.Arb), []string{"0/2/decl/4", "0/2/decl/5"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
	h.replay(t, "-east@320 lefttrigger=0@330 -lb@340")
	h.ExpectOutput("-LShift@320 -LCtrl@330")
	h.expectDecisions(t,
		"@0 east down dispatch", "@10 leftshoulder down dispatch", "@20 lt down dispatch",
		"@100 east none cancel", "@100 leftshoulder none cancel", "@100 lt none cancel",
		"@200 east up canceled", "@210 leftshoulder up canceled", "@230 lt up canceled", "@240 lt down dispatch",
		"@300 east down dispatch", "@310 leftshoulder down unbound",
		"@320 east up release", "@330 lt up release", "@340 leftshoulder up end")
}

// TestReloadTapInProgress: a reload ends a tap at once; the control's
// next press taps the new chord.
func TestReloadTapInProgress(t *testing.T) {
	h := newHarness(t, 0, 0, southTap)
	h.s.Handle(button("south", true))
	h.at(t, 5)
	if err := h.s.Reload(compile(t, southRedo), 2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	h.ExpectOutput("+LCtrl@0 +Z@0 -Z@5 -LCtrl@5")
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the reload, want none", d)
	}
	h.replay(t, "-south@50 +south@60 -south@100")
	h.ExpectOutput("+LCtrl@60 +Y@60 -Y@80 -LCtrl@80")
	h.expectDecisions(t,
		"@0 south down dispatch", "@5 south none cancel", "@50 south up canceled",
		"@60 south down dispatch", "@100 south up end")
}

// TestReloadTwice: every reload releases the keys of the generation
// before, the same Program may come again, and a slot with nothing held
// sends nothing.
func TestReloadTwice(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	prog := compile(t, eastShift)
	h.replay(t, "+east@0")
	if err := h.s.Reload(prog, 2); err != nil {
		t.Fatal(err)
	}
	h.replay(t, "-east@10 +east@20")
	if err := h.s.Reload(prog, 3); err != nil {
		t.Fatal(err)
	}
	h.replay(t, "-east@30")
	calls := h.Out.Calls()
	if err := h.s.Reload(prog, 5); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+Space@0 -Space@0 +LShift@20 -LShift@20")
	if n := h.Out.Calls(); n != calls {
		t.Errorf("%d Send calls after the third reload, want %d: nothing is held", n, calls)
	}
	if g := h.s.Gen(); g != 5 {
		t.Errorf("Gen = %d, want 5", g)
	}
	h.replay(t, "+east@40")
	if got, want := owners(h.Arb), []string{"0/5/decl/3"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
	h.replay(t, "-east@50")
	h.ExpectOutput("+LShift@40 -LShift@50")
	h.expectDecisions(t,
		"@0 east down dispatch", "@0 east none cancel", "@10 east up canceled",
		"@20 east down dispatch", "@20 east none cancel", "@30 east up canceled",
		"@40 east down dispatch", "@50 east up release")
}

// TestReloadErrors: a reload without a program or with a generation not
// after the slot's changes nothing.
func TestReloadErrors(t *testing.T) {
	h := newHarness(t, 0, 3, eastHold)
	prog := compile(t, eastShift)
	h.replay(t, "+east@0")
	for _, tc := range []struct {
		name string
		prog *engine.Program
		gen  uint32
		want string
	}{
		{"no program", nil, 4, "program"},
		{"gen 0", prog, 0, "generation 0"},
		{"same gen", prog, 3, "generation 3"},
		{"older gen", prog, 2, "generation 2"},
	} {
		if err := h.s.Reload(tc.prog, tc.gen); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Reload = %v, want an error about %s", tc.name, err, tc.want)
		}
	}
	if g := h.s.Gen(); g != 3 {
		t.Errorf("Gen = %d after the failed reloads, want 3", g)
	}
	h.replay(t, "-east@10")
	h.ExpectOutput("+Space@0 -Space@10")
	h.expectDecisions(t, "@0 east down dispatch", "@10 east up release")
}

// TestReloadOwnSlot: a reload releases only the keys of its slot.
func TestReloadOwnSlot(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	other := h.newSlot(t, 1, 1, compile(t, lbCtrl))
	h.replay(t, "+east@0")
	other.Handle(button("lb", true))
	if err := other.Reload(compile(t, eastShift), 2); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+Space@0 +LCtrl@0 -LCtrl@0")
	if got, want := owners(h.Arb), []string{"0/1/decl/1"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
	h.replay(t, "-east@10")
	h.ExpectOutput("-Space@10")
}

// TestReloadSendFailures: the release of a reload that fails is retried
// until it goes out, and the failure goes to Errors; the reload itself
// succeeds.
func TestReloadSendFailures(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl)
	h.replay(t, "+east@0 +lb@10")
	h.at(t, 50)
	// The batch and its immediate resend fail, and so does the first retry.
	h.Out.FailNext(1, nil)
	h.Out.FailNext(0, nil)
	h.Out.FailNext(0, nil)
	if err := h.s.Reload(compile(t, eastShift, lbCtrl), 2); err != nil {
		t.Fatalf("Reload = %v, want nil", err)
	}
	h.ExpectOutput("+Space@0 +LCtrl@10 -Space@50")
	if held := h.Arb.Held(); len(held) != 1 || !held[0].Pending {
		t.Fatalf("Held = %+v, want LCtrl pending release", held)
	}
	if errs := h.s.Errors(); len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Errorf("Errors = %v, want the failed release", errs)
	}
	// The releases send nothing; the retry at 150 ms fails, the one at
	// 250 ms goes out.
	h.replay(t, "-east@60 -lb@70")
	h.ExpectOutput("-LCtrl@250")
	if errs := h.s.Errors(); len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Errorf("Errors = %v, want the failed retry", errs)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	// The new program works.
	h.replay(t, "+east@300 -east@310")
	h.ExpectOutput("+LShift@300 -LShift@310")
}
