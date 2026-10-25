package profile

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"reflect"
	"testing"
)

// extendsSeeds are a child, its parent and its parent's parent.
var extendsSeeds = [][3]string{
	{
		`{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"south":{"press":{"type":"keys","keys":"Q"}}}}}`,
		`{"schemaVersion":1,"name":"P","extends":"g","timing":{"holdMs":300},"layers":{"base":{"south":{"label":"Undo","press":{"type":"keys","keys":"Ctrl+Z"}},"east":{"press":{"type":"keys","keys":"B"}}},"paint":{}}}`,
		`{"schemaVersion":1,"name":"G","analog":{"ls":{"mode":"fourWay","deadzone":0.2}},"script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"a":{"label":"x"}}}}`,
	},
	{
		`{"schemaVersion":1,"name":"C","extends":"p","analog":{"ls":{"deadzone":0.6}},"layers":{"base":{"a":{"press":{"type":"layer","layer":"paint","op":"toggle"}}}}}`,
		`{"schemaVersion":1,"name":"P","analog":{"ls":{"saturation":0.5}},"layers":{"base":{},"paint":{}}}`,
		``,
	},
	{
		`{"schemaVersion":0,"name":"C","extends":"c","layers":{"base":{}}}`,
		`{"schemaVersion":1,"name":"P","layers":{"base":{}}}`,
		``,
	},
	{
		`{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}},"functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}}}}`,
		`{"schemaVersion":1,"name":"P","extends":"g","layers":{"base":{}},"functionKeys":{"back":{"east":{"press":{"type":"keys","keys":"B","mode":"tap"}}},"start":{}}}`,
		`{"schemaVersion":1,"name":"G","extends":"p","layers":{"base":{}}}`,
	},
	{
		"\xEF\xBB\xBF" + `{"schemaVersion":-0e0,"name":"C","extends":"p","common":{"lt":{"block":true}},"layers":{"base":{"a":{}}}}`,
		`{"schemaVersion":1,"name":"P","controlNames":{"south":"Under"},"common":{"rt":{"block":true}},"feedback":{"shortMs":50},"layers":{"base":{"south":{"press":{"type":"macro","file":"a.ts"}}}}}`,
		``,
	},
	{
		`{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"a":{},"south":{}}}}`,
		`{"schemaVersion":1,"name":"P","layers":{"base":{"b":{"press":{"type":"keys","keys":"B"}},"west":{}}}}`,
		``,
	},
	{
		`{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{}}}`,
		`{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"Hyper+"}}}}}`,
		``,
	},
	{ // whole numbers written 1e2 and 2.0: an error in the file that has them
		`{"schemaVersion":1,"name":"C","extends":"p","timing":{"holdMs":1e2},"layers":{"base":{}}}`,
		`{"schemaVersion":1,"name":"P","extends":"g","layers":{"base":{"a":{"press":{"type":"sequence","steps":[{"wait":2.0}]}}}}}`,
		`{"schemaVersion":1,"name":"G","feedback":{"shortMs":5.0},"layers":{"base":{}}}`,
	},
	{ // numbers no float64 holds
		`{"schemaVersion":1,"name":"C","extends":"p","analog":{"ls":{"deadzone":1E1000001}},"layers":{"base":{}}}`,
		`{"schemaVersion":1,"name":"P","analog":{"ls":{"deadzone":1E400}},"layers":{"base":{}}}`,
		``,
	},
}

func FuzzResolve(f *testing.F) {
	for _, s := range extendsSeeds {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, child, parent, grand string) {
		files := map[string]string{"c": child, "p": parent, "g": grand}
		lib := &Library{ID: "c", Load: func(id string) ([]byte, error) {
			s, ok := files[id]
			if !ok || id == "c" {
				return nil, fs.ErrNotExist
			}
			return []byte(s), nil
		}}
		r, ds := Resolve([]byte(child), lib)
		checkResolve(t, files, r, ds)
		r2, ds2 := Resolve([]byte(child), lib)
		if (r == nil) != (r2 == nil) || !reflect.DeepEqual(ds, ds2) || r != nil && !bytes.Equal(r.Merged, r2.Merged) {
			t.Fatal("Resolve is not deterministic")
		}
	})
}

// TestExtendsFuzzSeeds runs the checks of FuzzResolve on the seeds in every
// go test run.
func TestExtendsFuzzSeeds(t *testing.T) {
	for i, s := range extendsSeeds {
		files := map[string]string{"c": s[0], "p": s[1], "g": s[2]}
		lib := &Library{ID: "c", Load: func(id string) ([]byte, error) {
			if v, ok := files[id]; ok && id != "c" {
				return []byte(v), nil
			}
			return nil, fs.ErrNotExist
		}}
		r, ds := Resolve([]byte(s[0]), lib)
		t.Logf("seed %d: ok=%v %v", i, r != nil, ds)
		checkResolve(t, files, r, ds)
	}
}

// checkResolve checks the invariants of a Resolve result for the profiles
// c, p and g: diagnostics are in range of the file they say they are in, and
// a merged profile is a profile of its own that parses to the same profile.
func checkResolve(t *testing.T, files map[string]string, r *Resolved, ds Diagnostics) {
	t.Helper()
	if (r == nil) != ds.HasErrors() {
		t.Fatalf("resolved %v with diagnostics %v", r, ds)
	}
	last := map[string]int{}
	for _, d := range ds {
		name := d.Profile
		if name == "" {
			name = "c"
		}
		text, ok := files[name]
		if !ok || d.Profile == "c" {
			t.Fatalf("diagnostic in a file that is not one: %+v", d)
		}
		data := []byte(text)
		if d.Offset < 0 || d.Offset > len(data) || d.Line < 1 || d.Col < 1 {
			t.Fatalf("diagnostic out of range: %+v", d)
		}
		if line, col := position(data, d.Offset); line != d.Line || col != d.Col {
			t.Fatalf("diagnostic %+v: offset %d is at %d:%d", d, d.Offset, line, col)
		}
		if d.Message == "" || d.Severity > SeverityWarning {
			t.Fatalf("diagnostic %+v", d)
		}
		if _, ok := splitPointer(d.Pointer); !ok {
			t.Fatalf("diagnostic %+v: malformed pointer", d)
		}
		if prev, seen := last[name]; seen && prev > d.Offset {
			t.Fatalf("diagnostics of %q out of order: %v", name, ds)
		}
		last[name] = d.Offset
	}
	if r == nil {
		return
	}
	if r.Profile == nil || r.Profile.Layer("base") == nil {
		t.Fatalf("profile %+v", r.Profile)
	}
	if r.Merged == nil {
		if r.Parents != nil || r.Profile.Extends != "" || len(r.Inherited()) != 0 {
			t.Fatalf("a profile without extends: %+v", r)
		}
		return
	}
	if !json.Valid(r.Merged) {
		t.Fatalf("the merged profile is not JSON:\n%s", r.Merged)
	}
	flat, fds := Parse(r.Merged)
	if flat == nil || fds.HasErrors() {
		t.Fatalf("the merged profile does not parse by itself: %v\n%s", fds, r.Merged)
	}
	want := *r.Profile
	want.Extends, want.Parents = "", nil
	if want.Script != nil {
		s := *want.Script
		s.Origin = ""
		want.Script = &s
	}
	if !reflect.DeepEqual(&want, flat) {
		t.Fatalf("the merged profile parses to another profile:\n got %+v\nwant %+v\n%s", flat, &want, r.Merged)
	}
	if r.Profile.Extends == "" || len(r.Parents) == 0 || r.Parents[0].ID != r.Profile.Extends || !reflect.DeepEqual(r.Parents, r.Profile.Parents) {
		t.Fatalf("extends %q parents %v", r.Profile.Extends, r.Parents)
	}
	root, serr := scan(r.Merged)
	if serr != nil {
		t.Fatalf("scan: %s", serr.msg)
	}
	for _, o := range r.Inherited() {
		if root.lookupPointer(o.Pointer) == nil || o.Profile == "" || o.Profile == "c" {
			t.Fatalf("inherited %+v is not in the merged profile", o)
		}
	}
	// What the merged text maps back to is a position in a file.
	for _, d := range r.Locate(Diagnostics{{Message: "x", Offset: 0}, {Message: "y", Offset: len(r.Merged) / 2}, {Message: "z", Offset: len(r.Merged)}}) {
		text, ok := files[d.Profile]
		if d.Profile == "" {
			text, ok = files["c"], true
		}
		if !ok || d.Offset < 0 || d.Offset > len(text) {
			t.Fatalf("located diagnostic %+v is not in a file", d)
		}
	}
}
