package integration

import (
	"reflect"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func rule(exe, prof string, enabled bool) profile.Rule {
	return profile.Rule{Exe: exe, Profile: &prof, Enabled: enabled}
}

func cfgOf(rules ...profile.Rule) *profile.Config {
	c := profile.DefaultConfig()
	c.Rules = rules
	return c
}

func TestBound(t *testing.T) {
	exes := []string{"CLIPStudioPaint.exe"}
	cfg := cfgOf(
		rule("clipstudiopaint.EXE", "csp", true),
		rule(`D:\CELSYS\CLIP STUDIO 1.5\CLIP STUDIO PAINT\CLIPStudioPaint.exe`, "csp2", true), // shadowed by the rule above
		rule("CLIPStudioPaint.exe", "off", false),
		rule("game.exe", "game", true),
		profile.Rule{Exe: "CLIPStudioPaint.exe", Enabled: true}, // no profile
		rule("CLIPStudioPaint.exe", "csp", true),                // again
	)
	for id, want := range map[string]bool{"csp": true, "csp2": false, "off": false, "game": false, "none": false} {
		if got := Bound(cfg, id, exes); got != want {
			t.Errorf("Bound(%s) = %v, want %v", id, got, want)
		}
	}
	if got := BoundProfiles(cfg, exes); !reflect.DeepEqual(got, []string{"csp"}) {
		t.Errorf("BoundProfiles = %v", got)
	}
	if got := BoundProfiles(cfgOf(), exes); len(got) != 0 {
		t.Errorf("BoundProfiles(no rules) = %v", got)
	}
	if Bound(nil, "csp", exes) || BoundProfiles(nil, exes) != nil {
		t.Error("nil config binds something")
	}
}

func TestBoundRuleOrder(t *testing.T) {
	exes := []string{"CLIPStudioPaint.exe"}
	cfg := cfgOf(rule("CLIPStudioPaint.exe", "first", true), rule("CLIPStudioPaint.exe", "second", true))
	if !Bound(cfg, "first", exes) || Bound(cfg, "second", exes) {
		t.Error("only the first matching rule selects a profile")
	}
	// A disabled rule in front does not shadow the next one.
	cfg = cfgOf(rule("CLIPStudioPaint.exe", "first", false), rule("CLIPStudioPaint.exe", "second", true))
	if Bound(cfg, "first", exes) || !Bound(cfg, "second", exes) {
		t.Error("a disabled rule must be skipped")
	}
}

func TestBoundFullPath(t *testing.T) {
	exes := []string{"CLIPStudioPaint.exe"}
	full := `D:\Apps\CSP\clipstudiopaint.exe`
	// A full-path rule in front of the bare-name one wins for that location,
	// and the bare-name one for every other location: both are bound.
	cfg := cfgOf(rule(full, "here", true), rule("CLIPStudioPaint.exe", "anywhere", true))
	if !Bound(cfg, "here", exes) || !Bound(cfg, "anywhere", exes) {
		t.Error("both profiles can be selected for CSP")
	}
	// Behind the bare-name rule it never wins.
	cfg = cfgOf(rule("CLIPStudioPaint.exe", "anywhere", true), rule(full, "here", true))
	if Bound(cfg, "here", exes) || !Bound(cfg, "anywhere", exes) {
		t.Error("a full-path rule behind a bare-name rule is shadowed")
	}
	// A full-path rule alone binds, ignoring case; another program's does not.
	cfg = cfgOf(rule(full, "here", true), rule(`D:\Apps\other.exe`, "other", true))
	if !Bound(cfg, "here", exes) || Bound(cfg, "other", exes) {
		t.Error("full-path rule alone")
	}
	// Disabled full-path rule.
	if Bound(cfgOf(rule(full, "here", false)), "here", exes) {
		t.Error("disabled full-path rule binds")
	}
}

func TestBoundAutoSwitchOff(t *testing.T) {
	exes := []string{"CLIPStudioPaint.exe"}
	cfg := cfgOf(rule("CLIPStudioPaint.exe", "csp", true))
	cfg.Enabled = false
	if Bound(cfg, "csp", exes) || len(BoundProfiles(cfg, exes)) != 0 {
		t.Error("with automatic selection off no rule selects anything, so nothing is bound")
	}
}

func TestDifferenceID(t *testing.T) {
	a := ItemStatus{Keys: "A", Function: "f", State: StateChanged, Current: []string{"B", "C"}}
	b := ItemStatus{Keys: "D", Function: "g", State: StateRemoved}
	ok := ItemStatus{Keys: "E", Function: "h", State: StateConsistent}
	if DifferenceID([]ItemStatus{ok}) != "" || !Consistent([]ItemStatus{ok}) {
		t.Error("consistent statuses have no difference ID")
	}
	id := DifferenceID([]ItemStatus{a, ok, b})
	if len(id) != 64 {
		t.Fatalf("id = %q", id)
	}
	if DifferenceID([]ItemStatus{b, a}) != id {
		t.Error("order matters")
	}
	a2 := a
	a2.Current = []string{"C", "B"}
	a2.Owners = []Owner{{ID: "x"}}
	if DifferenceID([]ItemStatus{a2, b}) != id {
		t.Error("current order or owners matter")
	}
	a2.Current = []string{"B"}
	if DifferenceID([]ItemStatus{a2, b}) == id {
		t.Error("different content, same ID")
	}
	if Consistent([]ItemStatus{a}) {
		t.Error("Consistent")
	}
}

type fake struct{ Integration }

func (fake) ID() string { return "fake" }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(fake{})
	if _, ok := r.Get("fake"); !ok {
		t.Error("not found")
	}
	if _, ok := r.Get("csp"); ok {
		t.Error("found a stranger")
	}
	if len(r.All()) != 1 {
		t.Error("All")
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration did not panic")
		}
	}()
	r.Register(fake{})
}

func TestFallbackNamer(t *testing.T) {
	n := NamerOrFallback(nil)
	info, ok := n.CommandName("basiccommand/x", "en")
	if info.Name != "basiccommand/x" || info.Path != nil || info.Group != "" || !ok {
		t.Errorf("fallback = %+v %v", info, ok)
	}
	if n.Layout("zh-CN") != nil {
		t.Error("fallback has a layout")
	}
	if OtherName("zh-CN") != "其他" || OtherName("en") != "Other" {
		t.Error("OtherName")
	}
}
