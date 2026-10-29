package profile_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Profile inheritance (PRO-19, ADR-0070).

// extLib is a library of profile files by ID.
type extLib map[string]string

func (l extLib) load(id string) ([]byte, error) {
	s, ok := l[id]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(s), nil
}

func (l extLib) resolve(id string) (*profile.Resolved, profile.Diagnostics) {
	return profile.Resolve([]byte(l[id]), &profile.Library{ID: id, Load: l.load})
}

// mustResolve resolves profile id of l and fails the test on errors. Every
// merged profile must also be valid by itself: Merged parses as a profile
// that is written whole, to the same profile.
func (l extLib) mustResolve(t *testing.T, id string) *profile.Resolved {
	t.Helper()
	r, ds := l.resolve(id)
	if r == nil || ds.HasErrors() {
		t.Fatalf("resolving %q: %v", id, ds)
	}
	checkFlat(t, r)
	return r
}

// checkFlat checks that the merged text of r is a profile on its own: it
// parses, without extends, to the profile r has.
func checkFlat(t *testing.T, r *profile.Resolved) {
	t.Helper()
	if r.Merged == nil {
		return
	}
	flat, ds := profile.Parse(r.Merged)
	if flat == nil || ds.HasErrors() {
		t.Fatalf("the merged profile does not parse by itself: %v\n%s", ds, r.Merged)
	}
	if strings.Contains(string(r.Merged), `"extends"`) {
		t.Errorf("the merged profile still extends:\n%s", r.Merged)
	}
	want := *r.Profile
	want.Extends, want.Parents = "", nil
	if want.Script != nil {
		s := *want.Script
		s.Origin = ""
		want.Script = &s
	}
	if !reflect.DeepEqual(&want, flat) {
		t.Errorf("the merged profile parses to another profile than the resolved one:\n got %+v\nwant %+v", flat, &want)
	}
	if !bytes.HasSuffix(r.Merged, []byte("\n")) {
		t.Errorf("the merged text has no final newline")
	}
}

// sameJSON reports whether a and b are the same JSON value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

func TestExtendsMerge(t *testing.T) {
	tests := []struct {
		name          string
		parent, child string
		want          string // the merged profile
	}{
		{
			name:   "a control of the child replaces the parent's whole",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"label":"Undo","press":{"type":"keys","keys":"Ctrl+Z"},"hold":{"type":"keys","keys":"H"}},"east":{"press":{"type":"keys","keys":"B"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"south":{"press":{"type":"keys","keys":"Q"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{"south":{"press":{"type":"keys","keys":"Q"}},"east":{"press":{"type":"keys","keys":"B"}}}}}`,
		},
		{
			name:   "a layer the child lacks is the parent's, one it has is merged",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}},"paint":{"east":{"press":{"type":"keys","keys":"B"}},"north":{"press":{"type":"keys","keys":"C"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{},"paint":{"east":{"press":{"type":"keys","keys":"D"}}},"extra":{"west":{"press":{"type":"keys","keys":"E"}}}}}`,
			want: `{"schemaVersion":1,"name":"C","layers":{
				"base":{"south":{"press":{"type":"keys","keys":"A"}}},
				"paint":{"east":{"press":{"type":"keys","keys":"D"}},"north":{"press":{"type":"keys","keys":"C"}}},
				"extra":{"west":{"press":{"type":"keys","keys":"E"}}}}}`,
		},
		{
			name:   "the color of a layer is the child's, or else the parent's",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"color":"#112233"},"paint":{"color":"#445566","south":{"press":{"type":"keys","keys":"A"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"color":"#778899"},"paint":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{"color":"#778899"},"paint":{"color":"#445566","south":{"press":{"type":"keys","keys":"A"}}}}}`,
		},
		{
			name:   "a control is the same whatever name it is written with",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"a":{"press":{"type":"keys","keys":"A"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`,
		},
		{
			name:   "and the other way round",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}},"east":{"press":{"type":"keys","keys":"E"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"a":{"press":{"type":"keys","keys":"B"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{"a":{"press":{"type":"keys","keys":"B"}},"east":{"press":{"type":"keys","keys":"E"}}}}}`,
		},
		{
			name:   "common is merged per control like a layer",
			parent: `{"schemaVersion":1,"name":"P","common":{"leftshoulder":{"label":"L","press":{"type":"keys","keys":"A"}},"rightshoulder":{"press":{"type":"keys","keys":"B"}}},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","common":{"rightshoulder":{"press":{"type":"keys","keys":"C"}}},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","common":{"leftshoulder":{"label":"L","press":{"type":"keys","keys":"A"}},"rightshoulder":{"press":{"type":"keys","keys":"C"}}},"layers":{"base":{}}}`,
		},
		{
			name:   "common of only one of them is taken as it is",
			parent: `{"schemaVersion":1,"name":"P","common":{"lt":{"block":true}},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","common":{"lt":{"block":true}},"layers":{"base":{}}}`,
		},
		{
			name: "the old function keys are merged as the one set they stand for",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionKeys":{
				"back":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}},
				"start":{"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}}`,
			child: `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionKeys":{
				"back":{"east":{"press":{"type":"keys","keys":"X","mode":"tap"}}}}}`,
			want: `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{
				"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},
				"east":{"press":{"type":"keys","keys":"X","mode":"tap"}},
				"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}`,
		},
		{
			name: "start's set wins over back's in the one set of a side",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionKeys":{
				"back":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}},
				"start":{"a":{"press":{"type":"keys","keys":"S","mode":"tap"}}}}}`,
			child: `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionSet":{
				"east":{"press":{"type":"keys","keys":"E","mode":"tap"}}}}`,
			want: `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{
				"a":{"press":{"type":"keys","keys":"S","mode":"tap"}},
				"east":{"press":{"type":"keys","keys":"E","mode":"tap"}}}}`,
		},
		{
			name:   "functionSet is merged per control",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"back":{"press":{"type":"keys","keys":"V","mode":"tap"}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionSet":{"select":{"press":{"type":"keys","keys":"X","mode":"tap"}},"guide":{"block":true}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"select":{"press":{"type":"keys","keys":"X","mode":"tap"}},"guide":{"block":true}}}`,
		},
		{
			name:   "an old parent and a child with functionSet",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}},"start":{"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"C","mode":"tap"}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"C","mode":"tap"}},"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}}}`,
		},
		{
			name:   "a parent with functionSet and an old child",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"east":{"press":{"type":"keys","keys":"B","mode":"tap"}},"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionKeys":{"back":{"east":{"press":{"type":"keys","keys":"X","mode":"tap"}}},"start":{"south":{"press":{"type":"keys","keys":"Y","mode":"tap"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"Y","mode":"tap"}},"east":{"press":{"type":"keys","keys":"X","mode":"tap"}},"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}`,
		},
		{
			name:   "a child that writes both keeps the old one, which it ignores, as it wrote it",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionSet":{"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}},"functionKeys":{"start":{"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}},"functionKeys":{"start":{"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}}}}`,
		},
		{
			name:   "function keys only the child has",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionKeys":{"start":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionKeys":{"start":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}}`,
		},
		{
			name:   "functionSet only the parent has",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{}},"functionSet":{"back":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{}},"functionSet":{"back":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}`,
		},
		{
			name:   "timing is merged member by member",
			parent: `{"schemaVersion":1,"name":"P","timing":{"holdMs":300,"multiTapMs":200,"tapGapMs":30},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","timing":{"holdMs":400,"tapPressMs":25},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","timing":{"holdMs":400,"multiTapMs":200,"tapGapMs":30,"tapPressMs":25},"layers":{"base":{}}}`,
		},
		{
			name:   "feedback is merged member by member",
			parent: `{"schemaVersion":1,"name":"P","feedback":{"shortMs":80,"strength":20000},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","feedback":{"strength":10000,"pausedColor":"#FF0000"},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","feedback":{"shortMs":80,"strength":10000,"pausedColor":"#FF0000"},"layers":{"base":{}}}`,
		},
		{
			name:   "analog is merged member by member, a stick's settings too",
			parent: `{"schemaVersion":1,"name":"P","analog":{"ls":{"mode":"fourWay","deadzone":0.2},"rs":{"mode":"mouse"},"axisThreshold":0.4},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","analog":{"ls":{"deadzone":0.1},"dpad":{"mode":"eightWay"},"axisThreshold":0.5},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","analog":{"ls":{"mode":"fourWay","deadzone":0.1},"rs":{"mode":"mouse"},"dpad":{"mode":"eightWay"},"axisThreshold":0.5},"layers":{"base":{}}}`,
		},
		{
			name:   "analog of only one of them is taken as it is",
			parent: `{"schemaVersion":1,"name":"P","analog":{"lt":{"press":0.4,"release":0.3}},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","analog":{"lt":{"press":0.4,"release":0.3}},"layers":{"base":{}}}`,
		},
		{
			name:   "the default layer and the name are the child's if it has them",
			parent: `{"schemaVersion":1,"name":"P","defaultLayer":"paint","layers":{"base":{},"paint":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","defaultLayer":"paint","layers":{"base":{},"paint":{}}}`,
		},
		{
			name:   "a default layer of the child",
			parent: `{"schemaVersion":1,"name":"P","defaultLayer":"paint","layers":{"base":{},"paint":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","defaultLayer":"base","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","defaultLayer":"base","layers":{"base":{},"paint":{}}}`,
		},
		{
			name:   "control names are merged per control",
			parent: `{"schemaVersion":1,"name":"P","controlNames":{"south":"Under","east":"Right"},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","controlNames":{"east":"Far","north":"Top"},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","controlNames":{"south":"Under","east":"Far","north":"Top"},"layers":{"base":{}}}`,
		},
		{
			name:   "the script of the child replaces the parent's as a whole",
			parent: `{"schemaVersion":1,"name":"P","script":{"entry":"main.ts","api":"0.17","params":{"a":1,"b":2},"tickHz":60},"layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","script":{"entry":"mine.ts","api":"0.17","params":{"b":3}},"layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","script":{"entry":"mine.ts","api":"0.17","params":{"b":3}},"layers":{"base":{}}}`,
		},
		{
			name:   "a child without a script has the parent's",
			parent: `{"schemaVersion":1,"name":"P","script":{"entry":"main.ts","api":"0.17","params":{"a":[1,{"x":null}]}},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","script":{"entry":"main.ts","api":"0.17","params":{"a":[1,{"x":null}]}},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`,
		},
		{
			name:   "$schema is the child's, or else the parent's",
			parent: `{"$schema":"https://example.test/p.json","schemaVersion":1,"name":"P","layers":{"base":{}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"$schema":"https://example.test/p.json","schemaVersion":1,"name":"C","layers":{"base":{}}}`,
		},
		{
			name:   "bindings of every kind survive the merge byte for byte",
			parent: `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"label":"复制 \u00e9 \"q\"","tap":{"type":"keys","keys":["Ctrl+C","Ctrl+V"],"mode":"tap"},"timing":{"holdMs":100},"haptic":"tick"},"lt":{"press":{"type":"keys","keys":"A","repeat":{"minRateHz":2,"rateHz":10}}}}}}`,
			child:  `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
			want:   `{"schemaVersion":1,"name":"C","layers":{"base":{"south":{"label":"复制 \u00e9 \"q\"","tap":{"type":"keys","keys":["Ctrl+C","Ctrl+V"],"mode":"tap"},"timing":{"holdMs":100},"haptic":"tick"},"lt":{"press":{"type":"keys","keys":"A","repeat":{"minRateHz":2,"rateHz":10}}}}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := extLib{"p": tt.parent, "c": tt.child}
			r := l.mustResolve(t, "c")
			if !sameJSON(t, r.Merged, []byte(tt.want)) {
				t.Errorf("merged profile\n%s\nwant\n%s", r.Merged, tt.want)
			}
			if r.Profile.Extends != "p" {
				t.Errorf("Extends = %q", r.Profile.Extends)
			}
			if len(r.Parents) != 1 || r.Parents[0].ID != "p" {
				t.Errorf("Parents = %v", r.Parents)
			}
		})
	}
}

// The merged profile is the profile the engine runs: bindings from both, with
// the pointers of the merged document.
func TestExtendsProfileModel(t *testing.T) {
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","defaultLayer":"paint","timing":{"holdMs":333},"layers":{"base":{"south":{"label":"Undo","press":{"type":"keys","keys":"Ctrl+Z"}},"east":{"label":"Pan","press":{"type":"keys","keys":"Space"}}},"paint":{}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"east":{"label":"Brush","press":{"type":"keys","keys":"B"}}}}}`,
	}
	r := l.mustResolve(t, "c")
	p := r.Profile
	if p.Name != "C" || p.DefaultLayer != "paint" || !p.Timing.HoldSet || p.Timing.HoldMs != 333 {
		t.Errorf("name %q default layer %q timing %+v", p.Name, p.DefaultLayer, p.Timing)
	}
	base := p.Layer("base")
	if base == nil || p.Layer("paint") == nil {
		t.Fatalf("layers %v", p.Layers)
	}
	south, east := base.Binding(control.South), base.Binding(control.East)
	if south == nil || south.Label != "Undo" || south.ID != "/layers/base/south" {
		t.Errorf("south: %+v", south)
	}
	if east == nil || east.Label != "Brush" || east.Press == nil || east.Press.Keys.String() != "B" {
		t.Errorf("east: %+v", east)
	}
	if p.Extends != "p" || len(p.Parents) != 1 {
		t.Errorf("extends %q parents %v", p.Extends, p.Parents)
	}
}

func TestExtendsTextIsLaidOutForPeople(t *testing.T) {
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":["Ctrl+C","Ctrl+V"],"mode":"tap"}}}}}`,
		"c": `{"schemaVersion":0,"name":"C","extends":"p","layers":{"base":{"east":{}}}}`,
	}
	r := l.mustResolve(t, "c")
	const want = `{
  "schemaVersion": 1,
  "name": "C",
  "layers": {
    "base": {
      "south": {
        "press": {
          "type": "keys",
          "keys": ["Ctrl+C", "Ctrl+V"],
          "mode": "tap"
        }
      },
      "east": {}
    }
  }
}
`
	if string(r.Merged) != want {
		t.Errorf("merged text\n%s\nwant\n%s", r.Merged, want)
	}
}

func TestExtendsChain(t *testing.T) {
	l := extLib{
		"root": `{"schemaVersion":1,"name":"R","timing":{"holdMs":111,"multiTapMs":222},"layers":{"base":{"south":{"label":"Root south","press":{"type":"keys","keys":"A"}},"east":{"label":"Root east","press":{"type":"keys","keys":"B"}},"north":{"press":{"type":"keys","keys":"N"}}}}}`,
		"mid":  `{"schemaVersion":1,"name":"M","extends":"root","timing":{"holdMs":112},"layers":{"base":{"east":{"label":"Mid east","press":{"type":"keys","keys":"C"}}}}}`,
		"leaf": `{"schemaVersion":1,"name":"L","extends":"mid","layers":{"base":{"west":{"label":"Leaf west","press":{"type":"keys","keys":"D"}}}}}`,
	}
	r := l.mustResolve(t, "leaf")
	if ids := parentIDs(r); !slices.Equal(ids, []string{"mid", "root"}) {
		t.Errorf("parents %v, want nearest first", ids)
	}
	if r.Parents[0].Sum == r.Parents[1].Sum || r.Parents[0].Sum == [32]byte{} {
		t.Errorf("sums %x", r.Parents)
	}
	base := r.Profile.Layer("base")
	labels := map[control.Control]string{}
	for _, b := range base.Bindings {
		labels[b.Control] = b.Label
	}
	want := map[control.Control]string{control.South: "Root south", control.East: "Mid east", control.West: "Leaf west", control.North: ""}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels %v, want %v", labels, want)
	}
	if tm := r.Profile.Timing; tm.HoldMs != 112 || tm.MultiTapMs != 222 {
		t.Errorf("timing %+v", tm)
	}
	// Where the members come from.
	got := map[string]string{}
	for _, o := range r.Inherited() {
		got[o.Pointer] = o.Profile
	}
	wantOrigins := map[string]string{
		"/layers/base/south": "root",
		"/layers/base/north": "root",
		"/layers/base/east":  "mid",
		"/timing/holdMs":     "mid",
		"/timing/multiTapMs": "root",
	}
	if !reflect.DeepEqual(got, wantOrigins) {
		t.Errorf("inherited %v, want %v", got, wantOrigins)
	}
	// The middle one on its own has its own parent.
	mid := l.mustResolve(t, "mid")
	if ids := parentIDs(mid); !slices.Equal(ids, []string{"root"}) {
		t.Errorf("mid parents %v", ids)
	}
}

// The function set is inherited per control whatever format each profile of the
// chain wrote it in (ADR-0075), and Inherited says where each control comes
// from, under the pointer it has in the merged document, functionSet.
func TestExtendsFunctionSetChain(t *testing.T) {
	l := extLib{
		"root": `{"schemaVersion":1,"name":"R","layers":{"base":{}},"functionKeys":{
			"back":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}},"east":{"press":{"type":"keys","keys":"B","mode":"tap"}},"north":{"press":{"type":"keys","keys":"N","mode":"tap"}}},
			"start":{"south":{"press":{"type":"keys","keys":"S","mode":"tap"}}}}}`,
		"mid":  `{"schemaVersion":1,"name":"M","extends":"root","layers":{"base":{}},"functionSet":{"east":{"press":{"type":"keys","keys":"C","mode":"tap"}},"guide":{"block":true}}}`,
		"leaf": `{"schemaVersion":1,"name":"L","extends":"mid","layers":{"base":{}},"functionKeys":{"start":{"west":{"press":{"type":"keys","keys":"W","mode":"tap"}}}}}`,
	}
	r := l.mustResolve(t, "leaf")
	const want = `{"schemaVersion":1,"name":"L","layers":{"base":{}},"functionSet":{
		"south":{"press":{"type":"keys","keys":"S","mode":"tap"}},
		"east":{"press":{"type":"keys","keys":"C","mode":"tap"}},
		"west":{"press":{"type":"keys","keys":"W","mode":"tap"}},
		"north":{"press":{"type":"keys","keys":"N","mode":"tap"}},
		"guide":{"block":true}}}`
	if !sameJSON(t, r.Merged, []byte(want)) {
		t.Errorf("merged profile\n%s\nwant\n%s", r.Merged, want)
	}
	set := r.Profile.EffectiveFunctionSet()
	if set == nil || r.Profile.FunctionKeys != nil || set != r.Profile.FunctionSet {
		t.Fatalf("function set %+v, function keys %+v", set, r.Profile.FunctionKeys)
	}
	if b := set.Binding(control.South); b == nil || b.Press.Keys.String() != "S" || b.ID != "/functionSet/south" {
		t.Errorf("south: %+v", b)
	}
	got := map[string]string{}
	for _, o := range r.Inherited() {
		got[o.Pointer] = o.Profile
	}
	wantOrigins := map[string]string{
		"/functionSet/south": "root",
		"/functionSet/north": "root",
		"/functionSet/east":  "mid",
		"/functionSet/guide": "mid",
	}
	if !reflect.DeepEqual(got, wantOrigins) {
		t.Errorf("inherited %v, want %v", got, wantOrigins)
	}
	// The middle one on its own is the merge of the old format and its own set.
	mid := l.mustResolve(t, "mid")
	got = map[string]string{}
	for _, o := range mid.Inherited() {
		got[o.Pointer] = o.Profile
	}
	if want := map[string]string{"/functionSet/south": "root", "/functionSet/north": "root"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mid inherited %v, want %v", got, want)
	}
}

func parentIDs(r *profile.Resolved) []string {
	var ids []string
	for _, p := range r.Parents {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestExtendsDepth(t *testing.T) {
	// A profile may have up to four parents.
	chain := func(n int) extLib {
		l := extLib{}
		for i := range n + 1 {
			id := fmt.Sprintf("p%d", i)
			if i == n {
				l[id] = `{"schemaVersion":1,"name":"top","layers":{"base":{}}}`
				continue
			}
			l[id] = fmt.Sprintf(`{"schemaVersion":1,"name":"n%d","extends":"p%d","layers":{"base":{}}}`, i, i+1)
		}
		return l
	}
	r := chain(profile.MaxExtendsDepth).mustResolve(t, "p0")
	if len(r.Parents) != profile.MaxExtendsDepth {
		t.Errorf("%d parents, want %d", len(r.Parents), profile.MaxExtendsDepth)
	}
	_, ds := chain(profile.MaxExtendsDepth + 1).resolve("p0")
	if !ds.HasErrors() {
		t.Fatal("five parents are fine")
	}
	// The error is in the file that has the fifth parent, and p0 says which parent.
	if ds[0].Pointer != "/extends" || ds[0].Profile != "" || !strings.Contains(ds[0].Message, `parent "p1" has errors`) {
		t.Errorf("first diagnostic %+v", ds[0])
	}
	last := ds[len(ds)-1]
	if last.Profile != "p4" || !strings.Contains(last.Message, "too many levels") || last.Pointer != "/extends" {
		t.Errorf("last diagnostic %+v", last)
	}
}

func TestExtendsLoops(t *testing.T) {
	tests := []struct {
		name string
		lib  extLib
		id   string
		file string // the diagnostic that says what is wrong is in this file
		want string
	}{
		{
			name: "itself",
			lib:  extLib{"a": `{"schemaVersion":1,"name":"A","extends":"a","layers":{"base":{}}}`},
			id:   "a", file: "", want: "cannot extend itself",
		},
		{
			name: "two",
			lib: extLib{
				"a": `{"schemaVersion":1,"name":"A","extends":"b","layers":{"base":{}}}`,
				"b": `{"schemaVersion":1,"name":"B","extends":"a","layers":{"base":{}}}`,
			},
			id: "a", file: "b", want: `extends loops: "a" → "b" → "a"`,
		},
		{
			name: "three, seen from a profile outside",
			lib: extLib{
				"x": `{"schemaVersion":1,"name":"X","extends":"a","layers":{"base":{}}}`,
				"a": `{"schemaVersion":1,"name":"A","extends":"b","layers":{"base":{}}}`,
				"b": `{"schemaVersion":1,"name":"B","extends":"c","layers":{"base":{}}}`,
				"c": `{"schemaVersion":1,"name":"C","extends":"a","layers":{"base":{}}}`,
			},
			id: "x", file: "c", want: `extends loops: "a" → "b" → "c" → "a"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ds := tt.lib.resolve(tt.id)
			if r != nil || !ds.HasErrors() {
				t.Fatalf("resolved: %v", ds)
			}
			found := false
			for _, d := range ds {
				if d.Profile == tt.file && strings.Contains(d.Message, tt.want) && d.Pointer == "/extends" {
					found = true
				}
			}
			if !found {
				t.Errorf("no error %q in %q: %v", tt.want, tt.file, ds)
			}
			if ds[0].Profile != "" || ds[0].Pointer != "/extends" {
				t.Errorf("the first diagnostic is not at the extends of %s: %+v", tt.id, ds[0])
			}
		})
	}
}

func TestExtendsMissingParent(t *testing.T) {
	l := extLib{"c": `{"schemaVersion":1,"name":"C","extends":"gone","layers":{"base":{}}}`}
	r, ds := l.resolve("c")
	if r != nil || len(ds) != 1 {
		t.Fatalf("got %v, %v", r, ds)
	}
	d := ds[0]
	if d.Severity != profile.SeverityError || d.Pointer != "/extends" || d.Profile != "" ||
		d.Message != `parent "gone" is not in the profile library` || d.Line != 1 {
		t.Errorf("diagnostic %+v", d)
	}
	// It points at the parent's name.
	if got := l["c"][d.Offset:]; !strings.HasPrefix(got, `"gone"`) {
		t.Errorf("offset %d is at %q", d.Offset, got)
	}

	// A parent that cannot be read says why.
	read := func(string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: `C:\x\profile.json`, Err: errors.New("access denied")}
	}
	_, ds = profile.Resolve([]byte(l["c"]), &profile.Library{ID: "c", Load: read})
	if len(ds) != 1 || ds[0].Message != `cannot read parent "gone": access denied` {
		t.Errorf("unreadable parent: %v", ds)
	}

	// An id that is not one.
	for _, bad := range []string{"Gone", "a b", "../x", "con", "-x"} {
		l := extLib{"c": fmt.Sprintf(`{"schemaVersion":1,"name":"C","extends":%q,"layers":{"base":{}}}`, bad)}
		_, ds := l.resolve("c")
		if len(ds) != 1 || ds[0].Message != "invalid profile library id" || ds[0].Pointer != "/extends" {
			t.Errorf("extends %q: %v", bad, ds)
		}
	}
}

func TestExtendsOnlyInTheLibrary(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`
	for name, f := range map[string]func() profile.Diagnostics{
		"Parse":   func() profile.Diagnostics { _, ds := profile.Parse([]byte(src)); return ds },
		"Resolve": func() profile.Diagnostics { _, ds := profile.Resolve([]byte(src), nil); return ds },
		"no loader": func() profile.Diagnostics {
			_, ds := profile.Resolve([]byte(src), &profile.Library{ID: "c"})
			return ds
		},
		"ParseIn no lib": func() profile.Diagnostics { _, ds := profile.ParseIn([]byte(src), profile.Library{}); return ds },
	} {
		ds := f()
		if len(ds) != 1 || ds[0].Pointer != "/extends" || !strings.Contains(ds[0].Message, "only in a profile of the library") {
			t.Errorf("%s: %v", name, ds)
		}
	}
	// Not a string: a schema error, with or without a library.
	l := extLib{"c": `{"schemaVersion":1,"name":"C","extends":3,"layers":{"base":{}}}`}
	if _, ds := l.resolve("c"); len(ds) != 1 || ds[0].Pointer != "/extends" {
		t.Errorf("extends 3: %v", ds)
	}
	l = extLib{"c": `{"schemaVersion":1,"name":"C","extends":"","layers":{"base":{}}}`}
	if _, ds := l.resolve("c"); len(ds) != 1 || ds[0].Pointer != "/extends" {
		t.Errorf("empty extends: %v", ds)
	}
}

func TestExtendsChildNeedsItsBase(t *testing.T) {
	// The child is a profile by itself as far as the schema goes: it has its
	// layers, base too, even if empty.
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","layers":{"base":{}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p"}`,
	}
	_, ds := l.resolve("c")
	if len(ds) != 1 || !strings.Contains(ds[0].Message, `missing property "layers"`) {
		t.Errorf("%v", ds)
	}
}

func TestExtendsParentWithErrors(t *testing.T) {
	const child = `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`
	tests := []struct {
		name   string
		parent string
		inP    string // text of the error in the parent
		line   int    // of that error in the parent
	}{
		{
			name:   "syntax",
			parent: "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n}",
			inP:    "trailing comma", line: 3,
		},
		{
			name:   "schema",
			parent: "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"layers\": {\"base\": {\"nope\": {}}}\n}",
			inP:    `"nope"`, line: 4,
		},
		{
			name: "semantics",
			parent: "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"layers\": {\"base\": {\n" +
				"    \"south\": {\"press\": {\"type\": \"layer\", \"layer\": \"missing\", \"op\": \"switch\"}}}}\n}",
			inP: `unknown layer "missing"`, line: 1,
		},
		{
			name:   "a newer version",
			parent: `{"schemaVersion":2,"name":"P","layers":{"base":{}}}`,
			inP:    "newer than supported", line: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := extLib{"p": tt.parent, "c": child}
			r, ds := l.resolve("c")
			if r != nil || !ds.HasErrors() {
				t.Fatalf("resolved: %v", ds)
			}
			// First in the child, which parent and what is wrong.
			first := ds[0]
			if first.Profile != "" || first.Pointer != "/extends" || first.Line != 1 ||
				!strings.Contains(first.Message, `parent "p" has errors: `+fmt.Sprint(tt.line)+":") || !strings.Contains(first.Message, tt.inP) {
				t.Errorf("first diagnostic %+v", first)
			}
			// Then the parent's, in the parent's file.
			var own profile.Diagnostic
			for _, d := range ds[1:] {
				if d.Profile != "p" {
					t.Errorf("a diagnostic after the first is not the parent's: %+v", d)
				}
				if own.Message == "" && d.Severity == profile.SeverityError {
					own = d
				}
			}
			if own.Line != tt.line || !strings.Contains(own.Message, tt.inP) {
				t.Errorf("parent's diagnostic %+v", own)
			}
			if got := ds.Local(); len(got) != 1 || got[0] != first {
				t.Errorf("Local() = %v", got)
			}
			// In a message the parent comes before the position.
			if msg := (&profile.Error{Diagnostics: profile.Diagnostics{own}}).Error(); !strings.HasPrefix(msg, `parent "p": `+fmt.Sprint(own.Line)+":") {
				t.Errorf("Error() = %q", msg)
			}
			// The error of a parent is not the child's own to fix: the child
			// is fine again as soon as the parent is.
			l["p"] = `{"schemaVersion":1,"name":"P","layers":{"base":{}}}`
			l.mustResolve(t, "c")
		})
	}
}

func TestExtendsGrandparentWithErrors(t *testing.T) {
	l := extLib{
		"g": `{"schemaVersion":1,"name":"G","layers":{"base":{"south":{"press":{"type":"keys","keys":"Hyper+"}}}}}`,
		"p": `{"schemaVersion":1,"name":"P","extends":"g","layers":{"base":{}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
	}
	_, ds := l.resolve("c")
	if !ds.HasErrors() {
		t.Fatal("no errors")
	}
	if ds[0].Profile != "" || !strings.Contains(ds[0].Message, `parent "p" has errors: 1:`) || !strings.Contains(ds[0].Message, `parent "g" has errors`) {
		t.Errorf("summary %+v", ds[0])
	}
	// p says it too, in p's file, and g's own error is in g's.
	byFile := map[string]int{}
	for _, d := range ds {
		byFile[d.Profile]++
	}
	if byFile[""] != 1 || byFile["p"] != 1 || byFile["g"] != 1 {
		t.Errorf("diagnostics by file %v: %v", byFile, ds)
	}
	for _, d := range ds {
		if d.Profile == "g" && (d.Pointer != "/layers/base/south/press/keys" || !strings.HasPrefix(l["g"][d.Offset:], "Hyper")) {
			t.Errorf("the error in g: %+v", d)
		}
	}
}

// An error that comes from what the child writes is reported in the child's
// file, at the member, even where Parse reports it for the whole file.
func TestExtendsDiagnosticsInTheChild(t *testing.T) {
	l := extLib{
		"p": "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"layers\": {\"base\": {}, \"paint\": {}}\n}",
		"c": "{\n  \"schemaVersion\": 1,\n  \"name\": \"C\",\n  \"extends\": \"p\",\n  \"layers\": {\n    \"base\": {\n" +
			"      \"south\": {\"press\": {\"type\": \"layer\", \"layer\": \"paint\", \"op\": \"toggle\"}},\n" +
			"      \"east\": {\"press\": {\"type\": \"layer\", \"layer\": \"nowhere\", \"op\": \"toggle\"}},\n" +
			"      \"west\": {\"press\": {\"type\": \"keys\", \"keys\": \"Ctrl+Bogus\"}}\n" +
			"    }\n  }\n}",
	}
	r, ds := l.resolve("c")
	if r != nil {
		t.Fatal("resolved")
	}
	if len(ds) != 2 {
		t.Fatalf("diagnostics %v", ds)
	}
	for _, d := range ds {
		if d.Profile != "" {
			t.Errorf("not in the child: %+v", d)
		}
	}
	// The layer paint is the parent's, so only nowhere is unknown, found where it is written.
	if d := ds[0]; d.Line != 8 || d.Pointer != "/layers/base/east/press/layer" || d.Message != `unknown layer "nowhere"` {
		t.Errorf("first %+v", d)
	}
	if d := ds[1]; d.Line != 9 || d.Pointer != "/layers/base/west/press/keys" || !strings.HasPrefix(l["c"][d.Offset:], "Bogus") {
		t.Errorf("second %+v", d)
	}
}

// A mistake that both files take part in is reported at the member that is
// wrong, in the file that has it, which is not always the child's.
func TestExtendsCombinedMistake(t *testing.T) {
	l := extLib{
		"p": "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"analog\": {\"ls\": {\"saturation\": 0.5}},\n  \"layers\": {\"base\": {}}\n}",
		"c": "{\n  \"schemaVersion\": 1,\n  \"name\": \"C\",\n  \"extends\": \"p\",\n  \"analog\": {\"ls\": {\"deadzone\": 0.6}},\n  \"layers\": {\"base\": {}}\n}",
	}
	r, ds := l.resolve("c")
	if r != nil || len(ds) != 2 {
		t.Fatalf("got %v %v", r, ds)
	}
	// One in the parent's file, where saturation is, and, in the child, one that says there is a problem.
	d := ds[1]
	if d.Pointer != "/analog/ls/saturation" || d.Profile != "p" || d.Line != 4 || !strings.HasPrefix(l["p"][d.Offset:], "0.5") {
		t.Errorf("%+v", d)
	}
	if s := ds[0]; s.Profile != "" || s.Pointer != "/extends" || s.Line != 4 ||
		s.Message != `merged with its parents the profile has errors: parent "p" 4:`+fmt.Sprint(d.Col)+`: saturation must be greater than deadzone` {
		t.Errorf("summary %+v", s)
	}
}

func TestExtendsWarningsOfTheParent(t *testing.T) {
	l := extLib{
		"p": "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"layers\": {\"base\": {\"south\": {\"label\": \"nothing yet\"}}}\n}",
		"c": "{\n  \"schemaVersion\": 1,\n  \"name\": \"C\",\n  \"extends\": \"p\",\n  \"layers\": {\"base\": {\"east\": {\"label\": \"too\"}}}\n}",
	}
	r, ds := l.resolve("c")
	if r == nil || ds.HasErrors() {
		t.Fatalf("got %v %v", r, ds)
	}
	if len(ds) != 2 {
		t.Fatalf("warnings %v", ds)
	}
	// The child's first, then the parent's.
	if ds[0].Profile != "" || ds[0].Line != 5 || ds[0].Pointer != "/layers/base/east" || ds[0].Severity != profile.SeverityWarning {
		t.Errorf("first %+v", ds[0])
	}
	if ds[1].Profile != "p" || ds[1].Line != 4 || ds[1].Pointer != "/layers/base/south" {
		t.Errorf("second %+v", ds[1])
	}
	if got := ds.Local(); len(got) != 1 || got[0] != ds[0] {
		t.Errorf("Local() = %v", got)
	}
	// A warning of a binding the child replaced is gone.
	l["c"] = `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`
	if _, ds := l.resolve("c"); len(ds) != 0 {
		t.Errorf("the replaced binding still warns: %v", ds)
	}
}

func TestExtendsOldVersionsAndBOM(t *testing.T) {
	l := extLib{
		"p": "\xEF\xBB\xBF" + `{"schemaVersion":0,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`,
		"c": "\xEF\xBB\xBF" + `{"schemaVersion":-0e0,"name":"C","extends":"p","layers":{"base":{"east":{"press":{"type":"keys","keys":"B"}}}}}`,
	}
	r := l.mustResolve(t, "c")
	if bytes.HasPrefix(r.Merged, []byte("\xEF\xBB\xBF")) || !strings.Contains(string(r.Merged), `"schemaVersion": 1,`) {
		t.Errorf("merged\n%s", r.Merged)
	}
	if r.Profile.SchemaVersion != profile.CurrentSchema || len(r.Profile.Layer("base").Bindings) != 2 {
		t.Errorf("profile %+v", r.Profile)
	}
	// An error is placed in the file with the BOM counted in the offset.
	l["c"] = "\xEF\xBB\xBF" + `{"schemaVersion":0,"name":"C","extends":"p","layers":{"base":{"east":{"press":{"type":"layer","layer":"x","op":"toggle"}}}}}`
	_, ds := l.resolve("c")
	if len(ds) != 1 || !strings.HasPrefix(l["c"][ds[0].Offset:], `"x"`) || ds[0].Col != strings.Index(strings.TrimPrefix(l["c"], "\xEF\xBB\xBF"), `"x"`)+1 {
		t.Errorf("%v", ds)
	}
	// A parent of a newer version.
	l["p"] = `{"schemaVersion":2,"name":"P","layers":{"base":{}}}`
	if _, ds := l.resolve("c"); !ds.HasErrors() || !strings.Contains(ds[0].Message, "newer than supported") {
		t.Errorf("newer parent: %v", ds)
	}
}

func TestExtendsScriptOrigin(t *testing.T) {
	l := extLib{
		"g": `{"schemaVersion":1,"name":"G","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`,
		"p": `{"schemaVersion":1,"name":"P","extends":"g","layers":{"base":{}}}`,
		"o": `{"schemaVersion":1,"name":"O","extends":"p","script":{"entry":"mine.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
	}
	for id, want := range map[string]string{"g": "", "p": "g", "c": "g", "o": ""} {
		r := l.mustResolve(t, id)
		if r.Profile.Script == nil || r.Profile.Script.Origin != want {
			t.Errorf("%s: script %+v, want origin %q", id, r.Profile.Script, want)
		}
	}
}

func TestExtendsLocateMergedText(t *testing.T) {
	l := extLib{
		"p": "{\n  \"schemaVersion\": 1,\n  \"name\": \"P\",\n  \"layers\": {\"base\": {\"south\": {\"press\": {\"type\": \"macro\", \"file\": \"a.ts\"}}}}\n}",
		"c": "{\n  \"schemaVersion\": 1,\n  \"name\": \"C\",\n  \"extends\": \"p\",\n  \"layers\": {\"base\": {\"east\": {\"press\": {\"type\": \"macro\", \"file\": \"b.ts\"}}}}\n}",
	}
	r := l.mustResolve(t, "c")
	// A check of the merged text, here every macro file is missing.
	missing := profile.MissingMacros(r.Merged, func(string) bool { return false })
	if len(missing) != 2 {
		t.Fatalf("missing %v", missing)
	}
	located := r.Locate(missing)
	if len(located) != 2 {
		t.Fatalf("located %v", located)
	}
	// The child's first.
	if located[0].Profile != "" || located[0].Line != 5 || !strings.HasPrefix(l["c"][located[0].Offset:], `"b.ts"`) {
		t.Errorf("first %+v", located[0])
	}
	if located[1].Profile != "p" || located[1].Line != 4 || !strings.HasPrefix(l["p"][located[1].Offset:], `"a.ts"`) {
		t.Errorf("second %+v", located[1])
	}
	// A profile without extends: nothing to map.
	flat := extLib{"f": `{"schemaVersion":1,"name":"F","layers":{"base":{}}}`}
	fr := flat.mustResolve(t, "f")
	ds := profile.Diagnostics{{Message: "x", Offset: 3}}
	if got := fr.Locate(ds); !reflect.DeepEqual(got, ds) {
		t.Errorf("Locate of a flat profile changed %v to %v", ds, got)
	}
	if fr.Merged != nil || fr.Parents != nil || fr.Inherited() != nil {
		t.Errorf("a profile without extends: %+v", fr)
	}
}

func TestExtendsOf(t *testing.T) {
	for in, want := range map[string]string{
		`{"extends":"base"}`:                  "base",
		`{"schemaVersion":1,"extends":"a_b"}`: "a_b",
		`{"extends":3}`:                       "",
		`{"extends":null}`:                    "",
		`{"name":"x"}`:                        "",
		`{"extends":"a",}`:                    "",
		``:                                    "",
		"\xEF\xBB\xBF" + `{"extends":"a"}`:    "a",
		`{"layers":{"extends":"no"}}`:         "",
	} {
		if got := profile.ExtendsOf([]byte(in)); got != want {
			t.Errorf("ExtendsOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// Parents are resolved afresh on every call, so a changed parent is seen.
func TestExtendsSeesChangedParents(t *testing.T) {
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
	}
	first := l.mustResolve(t, "c")
	l["p"] = `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`
	second := l.mustResolve(t, "c")
	if first.Parents[0].Sum == second.Parents[0].Sum {
		t.Error("the sum of the parent did not change")
	}
	if got := second.Profile.Layer("base").Binding(control.South).Press.Keys.String(); got != "B" {
		t.Errorf("south is %q", got)
	}
}

func TestDiagnosticsLocal(t *testing.T) {
	own := profile.Diagnostic{Message: "own"}
	foreign := profile.Diagnostic{Message: "foreign", Profile: "p"}
	ds := profile.Diagnostics{own}
	if got := ds.Local(); !reflect.DeepEqual(got, ds) {
		t.Errorf("Local() = %v", got)
	}
	ds = profile.Diagnostics{foreign, own, foreign}
	if got := ds.Local(); !reflect.DeepEqual(got, profile.Diagnostics{own}) {
		t.Errorf("Local() = %v", got)
	}
	if got := (profile.Diagnostics)(nil).Local(); got != nil {
		t.Errorf("Local() of nil = %v", got)
	}
	b, _ := json.Marshal(own)
	if strings.Contains(string(b), "profile") {
		t.Errorf("an own diagnostic has a profile in its JSON: %s", b)
	}
	b, _ = json.Marshal(foreign)
	if !strings.Contains(string(b), `"profile":"p"`) {
		t.Errorf("a parent's diagnostic has no profile in its JSON: %s", b)
	}
}

// A control of the child that is written under two names stays an error: the
// merge does not hide it.
func TestExtendsKeepsTheChildsDuplicates(t *testing.T) {
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"a":{"press":{"type":"keys","keys":"B"}},"south":{"press":{"type":"keys","keys":"C"}}}}}`,
	}
	_, ds := l.resolve("c")
	if len(ds) != 1 || ds[0].Profile != "" || !strings.Contains(ds[0].Message, "is already bound as") {
		t.Fatalf("%v", ds)
	}
	// The position in the message is that of the child's file, not of the merged text.
	want := fmt.Sprintf(`"a" at 1:%d (`, strings.Index(l["c"], `"a"`)+1)
	if !strings.Contains(ds[0].Message, want) {
		t.Errorf("message %q, want it to say %q", ds[0].Message, want)
	}
}

// Aliases of the parent that the child replaces under the canonical name, and
// the opposite, leave no duplicate behind (the merge is by control).
func TestExtendsMergeByControlKeepsTheWinnersName(t *testing.T) {
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","layers":{"base":{"a":{"press":{"type":"keys","keys":"A"}},"lb":{"press":{"type":"keys","keys":"L"}}}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`,
	}
	r := l.mustResolve(t, "c")
	if !strings.Contains(string(r.Merged), `"south"`) || strings.Contains(string(r.Merged), `"a"`) || !strings.Contains(string(r.Merged), `"lb"`) {
		t.Errorf("merged\n%s", r.Merged)
	}
	var got []string
	for _, o := range r.Inherited() {
		got = append(got, o.Pointer)
	}
	if !slices.Equal(got, []string{"/layers/base/lb"}) {
		t.Errorf("inherited %v", got)
	}
}

// A document that is small but deeply nested is large once it is indented;
// the merge stops at the limit instead of writing it.
func TestExtendsMergedTextIsBounded(t *testing.T) {
	chain := strings.Repeat(`{"a":`, 850) + "1" + strings.Repeat("}", 850)
	params := `{"x":[` + strings.TrimSuffix(strings.Repeat(chain+",", 30), ",") + `]}`
	l := extLib{
		"p": `{"schemaVersion":1,"name":"P","script":{"entry":"main.ts","api":"0.17","params":` + params + `},"layers":{"base":{}}}`,
		"c": `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
	}
	if _, ds := l.resolve("p"); ds.HasErrors() {
		t.Fatalf("the parent by itself: %v", ds)
	}
	r, ds := l.resolve("c")
	if r != nil || len(ds) != 1 || ds[0].Pointer != "/extends" || !strings.Contains(ds[0].Message, "the merged profile is larger than 16 MiB") {
		t.Errorf("got %v %v", r, ds)
	}
}
