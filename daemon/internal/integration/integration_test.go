package integration

import (
	"reflect"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func rule(exe, prof string, enabled bool) profile.Rule {
	return profile.Rule{Exe: exe, Profile: &prof, Enabled: enabled}
}

func TestBound(t *testing.T) {
	exes := []string{"CLIPStudioPaint.exe"}
	rules := []profile.Rule{
		rule("clipstudiopaint.EXE", "csp", true),
		rule(`D:\CELSYS\CLIP STUDIO 1.5\CLIP STUDIO PAINT\CLIPStudioPaint.exe`, "csp2", true),
		rule("CLIPStudioPaint.exe", "off", false),
		rule("game.exe", "game", true),
		{Exe: "CLIPStudioPaint.exe", Enabled: true}, // no profile
		rule("CLIPStudioPaint.exe", "csp", true),    // again
	}
	for id, want := range map[string]bool{"csp": true, "csp2": true, "off": false, "game": false, "none": false} {
		if got := Bound(rules, id, exes); got != want {
			t.Errorf("Bound(%s) = %v", id, got)
		}
	}
	if got := BoundProfiles(rules, exes); !reflect.DeepEqual(got, []string{"csp", "csp2"}) {
		t.Errorf("BoundProfiles = %v", got)
	}
	if got := BoundProfiles(nil, exes); len(got) != 0 {
		t.Errorf("BoundProfiles(nil) = %v", got)
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
	name, path, cat, ok := n.CommandName("basiccommand/x", "en")
	if name != "basiccommand/x" || path != nil || cat != OtherCategory || !ok {
		t.Errorf("fallback = %q %v %q %v", name, path, cat, ok)
	}
	if c := n.Categories("zh-CN"); len(c) != 1 || c[0].ID != "other" || c[0].Name != "其他" {
		t.Errorf("categories = %+v", c)
	}
}
