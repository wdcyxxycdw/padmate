package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// rootSchema is the canonical schema, relative to this package's directory.
const rootSchema = "../../../schema/profile.schema.json"

// TestSchemaCopyCurrent checks that the embedded copy matches the canonical
// schema byte for byte.
func TestSchemaCopyCurrent(t *testing.T) {
	want, err := os.ReadFile(rootSchema)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s not found; this test needs the whole repository", rootSchema)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(schemaJSON, want) {
		t.Errorf("internal/profile/profile.schema.json differs from schema/profile.schema.json; run task gen")
	}
}

func TestSchemaReturnsCopy(t *testing.T) {
	s := Schema()
	if !bytes.Equal(s, schemaJSON) {
		t.Fatal("Schema() differs from the embedded schema")
	}
	s[0] = 'x'
	if Schema()[0] == 'x' {
		t.Error("Schema() returned the embedded slice")
	}
}

func TestSchemaCompiles(t *testing.T) {
	if _, err := compiledSchema(); err != nil {
		t.Fatalf("the embedded schema does not compile: %v", err)
	}
}

// TestSchemaLayerControls checks that a layer's properties are the control
// names, canonical names in control.All() order and then the aliases, so
// the schema offers exactly the names that control.Parse accepts.
func TestSchemaLayerControls(t *testing.T) {
	want := []string{"color"}
	for _, c := range control.All() {
		want = append(want, c.String())
	}
	for _, a := range control.Aliases() {
		want = append(want, a.Name)
	}

	var s struct {
		Defs struct {
			Layer struct {
				Properties json.RawMessage `json:"properties"`
			} `json:"layer"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		t.Fatal(err)
	}
	got, err := objectKeys(s.Defs.Layer.Properties)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("layer properties\n%q\nwant\n%q", got, want)
	}
}

// objectKeys returns the keys of a JSON object in document order.
func objectKeys(obj json.RawMessage) ([]string, error) {
	d := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var keys []string
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, tok.(string))
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// TestSchemaDescribed checks that every definition has a title and a
// description and every property a description, which editors show on
// hover. The if/then of a tagged union only selects a branch and is not
// shown, so it is skipped.
func TestSchemaDescribed(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(schemaJSON, &root); err != nil {
		t.Fatal(err)
	}
	var check func(path string, s map[string]any, needTitle bool)
	check = func(path string, s map[string]any, needTitle bool) {
		if d, _ := s["description"].(string); d == "" {
			t.Errorf("%s has no description", path)
		}
		if t2, _ := s["title"].(string); needTitle && t2 == "" {
			t.Errorf("%s has no title", path)
		}
		props, _ := s["properties"].(map[string]any)
		for name, p := range props {
			check(path+"/properties/"+name, p.(map[string]any), false)
		}
	}
	check("#", root, true)
	defs := root["$defs"].(map[string]any)
	if len(defs) == 0 {
		t.Fatal("the schema has no $defs")
	}
	for name, d := range defs {
		check("#/$defs/"+name, d.(map[string]any), true)
	}
}

// TestSchemaValidate checks a few documents against the schema and the
// leaf errors it reports, so that the tagged union gives one clear error
// per mistake: an unknown type is one enum error at the type, a keys action
// without keys one required error at the action.
func TestSchemaValidate(t *testing.T) {
	s, err := compiledSchema()
	if err != nil {
		t.Fatal(err)
	}
	const head = `{"schemaVersion": 1, "name": "Krita", "layers": {"base": `
	for _, tc := range []struct {
		name string
		doc  string
		want []string // sorted "pointer keyword" of the leaf errors; nil when valid
	}{
		{"empty layer", head + `{}}}`, nil},
		{"full", `{"$schema": "../schema/profile.schema.json", "schemaVersion": 1, "name": "Krita", "layers": {"base": {
			"south": {"label": "Undo", "press": {"type": "keys", "keys": "Ctrl+Z"}},
			"b": {"press": {"type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap"}},
			"lt": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}},
			"dpad.up": {"label": "Only a label"}}}}`, nil},
		{"unknown type", head + `{"south": {"press": {"type": "paste", "keys": "Z"}}}}}`,
			[]string{"/layers/base/south/press/type enum"}},
		{"missing keys", head + `{"south": {"press": {"type": "keys"}}}}}`,
			[]string{"/layers/base/south/press required"}},
		{"missing type", head + `{"south": {"press": {"keys": "Z"}}}}}`,
			[]string{"/layers/base/south/press required"}},
		{"empty keys", head + `{"south": {"press": {"type": "keys", "keys": ""}}}}}`,
			[]string{"/layers/base/south/press/keys minLength", "/layers/base/south/press/keys type"}},
		{"bad mode and extra property", head + `{"south": {"press": {"type": "keys", "keys": "Z", "mode": "toggle", "repeat": 1}}}}}`,
			[]string{"/layers/base/south/press/mode enum", "/layers/base/south/press/repeat type"}},
		{"action is a string", head + `{"south": {"press": "Ctrl+Z"}}}}`,
			[]string{"/layers/base/south/press type"}},
		{"unknown control and gesture", head + `{"southh": {}, "a": {"hold": {}, "label": 3}}}}`,
			[]string{"/layers/base additionalProperties", "/layers/base/a/hold required", "/layers/base/a/label type"}},
		{"root", `{"schemaVersion": 2, "name": "", "layers": {"alt": {}}, "extra": true}`,
			[]string{" additionalProperties", "/layers required", "/name minLength", "/schemaVersion const"}},
		{"missing root properties", `{}`, []string{" required"}},
		{"not an object", `[]`, []string{" type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.doc))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := s.Validate(v); err != nil {
				var verr *jsonschema.ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("Validate: %v", err)
				}
				got = leafKeywords(verr, nil)
				slices.Sort(got)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("leaf errors %q, want %q", got, tc.want)
			}
		})
	}
}

// leafKeywords appends "pointer keyword" for each error without causes.
// Instance keys in these tests need no JSON Pointer escaping.
func leafKeywords(e *jsonschema.ValidationError, out []string) []string {
	if len(e.Causes) == 0 {
		loc := ""
		for _, p := range e.InstanceLocation {
			loc += "/" + p
		}
		return append(out, loc+" "+strings.Join(e.ErrorKind.KeywordPath(), "/"))
	}
	for _, c := range e.Causes {
		out = leafKeywords(c, out)
	}
	return out
}

// TestSchemaConcurrent validates from several goroutines at once, for the
// race detector.
func TestSchemaConcurrent(t *testing.T) {
	docs := []string{
		`{"schemaVersion": 1, "name": "x", "layers": {"base": {"south": {"press": {"type": "keys", "keys": "Z"}}}}}`,
		`{"schemaVersion": 1, "name": "x", "layers": {"base": {"south": {"press": {"type": "text"}}}}}`,
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			s, err := compiledSchema()
			if err != nil {
				t.Error(err)
				return
			}
			for i := range 50 {
				doc := docs[(g+i)%len(docs)]
				v, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
				if err != nil {
					t.Error(err)
					return
				}
				if err := s.Validate(v); (err == nil) != (doc == docs[0]) {
					t.Errorf("Validate(%s) = %v", doc, err)
					return
				}
			}
		})
	}
	wg.Wait()
}
