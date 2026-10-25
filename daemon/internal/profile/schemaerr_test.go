package profile

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// TestSchemaMessages checks that the schema locations with their own
// messages exist, so that a schema change cannot silently turn them into
// generic ones.
func TestSchemaMessages(t *testing.T) {
	for _, where := range []string{
		schemaRoot, schemaVersionProp, schemaScript, schemaLayers, schemaLayer, schemaBinding,
		schemaAction, schemaActionType, schemaKeysAction, schemaKeys, schemaMode, schemaSequenceStep,
		schemaFunctionKeys, schemaFunctionSet,
	} {
		if schemaAt(where) == nil {
			t.Errorf("schema location %q does not exist", where)
		}
	}
	for where, hints := range laterProperties {
		names := propertyNames(schemaAt(where))
		if len(names) == 0 {
			t.Errorf("schema location %q has no properties", where)
		}
		for p := range hints {
			if slices.Contains(names, p) {
				t.Errorf("%s: %q is in the schema now; drop its hint", where, p)
			}
		}
	}
	for _, p := range actionProperties {
		if !slices.Contains(propertyNames(schemaAt(schemaKeysAction)), p) {
			t.Errorf("%q is not a keys action property", p)
		}
	}
	for nick, c := range controlNicknames {
		names := propertyNames(schemaAt(schemaLayer))
		if slices.Contains(names, nick) || !slices.Contains(names, c) {
			t.Errorf("nickname %q → %q: want a name outside the schema for one inside", nick, c)
		}
	}
}

// TestControlNicknames checks that every printed name suggests its control
// alone, in any case, even when a different control is a closer spelling
// ("l1" is one edit from "lt").
func TestControlNicknames(t *testing.T) {
	for nick, c := range controlNicknames {
		for _, p := range []string{nick, strings.ToUpper(nick)} {
			want := `unknown control "` + p + `"; did you mean "` + c + `"?`
			if got := unknownProperty(schemaLayer, p); got != want {
				t.Errorf("unknownProperty(%q) = %q, want %q", p, got, want)
			}
		}
	}
}

// The binding keys ADR-0008 reserved for M20 ("combo:a+b", "a>b") are not
// planned any more (ADR-0069): they are unknown controls like any other name,
// and the message does not promise that they arrive.
func TestFormerlyReservedBindingKeysAreUnknownControls(t *testing.T) {
	for _, p := range []string{"combo:a+b", "a>b", "lb>south"} {
		want := `unknown control "` + p + `"`
		if got := unknownProperty(schemaLayer, p); got != want {
			t.Errorf("unknownProperty(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestSuggest(t *testing.T) {
	names := []string{"south", "east", "west", "north", "leftstick", "rightstick", "lt", "rt"}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"southh", []string{"south"}},
		{"South", []string{"south"}},
		{"suoth", []string{"south"}},
		{"sth", nil}, // 2 edits, but only 1 allowed for short names
		{"wset", []string{"west"}},
		{"est", []string{"east", "west"}},
		{"rigthstik", []string{"rightstick"}},
		{"lefstick", []string{"leftstick"}},
		{"l", []string{"lt"}},
		{"xt", []string{"lt", "rt"}},
		{"", nil},
		{"leftshoulder", nil},
	} {
		if got := suggest(tc.in, names); !slices.Equal(got, tc.want) {
			t.Errorf("suggest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := suggest("a", []string{"b", "c", "d", "e"}); len(got) != 3 {
		t.Errorf("suggest returned %q, want 3 names", got)
	}
}

func TestDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "acb", 1},
		{"abc", "ab", 1},
		{"", "ab", 2},
		{"ca", "abc", 3}, // optimal string alignment: no edit after a transposition
		{"abcdef", "abc", 3},
		{"kitten", "sitting", 3},
	} {
		if got := distance(tc.a, tc.b); got != tc.want {
			t.Errorf("distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := distance(tc.b, tc.a); got != tc.want {
			t.Errorf("distance(%q, %q) = %d, want %d", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestMessageHelpers(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{orList(nil), ""},
		{orList([]string{"a"}), "a"},
		{orList([]string{"a", "b"}), "a or b"},
		{orList([]string{"a", "b", "c"}), "a, b or c"},
		{withArticle("string"), "a string"},
		{withArticle("integer"), "an integer"},
		{withArticle("array"), "an array"},
		{withArticle("null"), "null"},
		{describeValue("x"), `"x"`},
		{describeValue(json.Number("1.5")), "1.5"},
		{describeValue(json.Number(strings.Repeat("9", 1000))), strings.Repeat("9", maxWord) + "…"},
		{describeValue(true), "true"},
		{describeValue(nil), "null"},
		{describeValue(map[string]any{}), "an object"},
		{describeValue([]any{}), "an array"},
		{didYouMean(nil), ""},
		{didYouMean([]string{"a", "b"}), `; did you mean "a" or "b"?`},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

// TestFlattenFallback checks that an error at a location missing from the
// index goes to the nearest indexed ancestor, and that an unknown error
// kind still gets a message.
func TestFlattenFallback(t *testing.T) {
	root, serr := scan([]byte(`{"a": {"b": 1}}`))
	if serr != nil {
		t.Fatal(serr.msg)
	}
	f := flattener{root: root}
	f.add(&jsonschema.ValidationError{
		SchemaURL:        schemaURL + "#/properties/x",
		InstanceLocation: []string{"a", "missing", "deeper"},
		ErrorKind:        &kind.MaxLength{Got: 5, Want: 3},
	})
	if len(f.ds) != 1 {
		t.Fatalf("diagnostics = %v", f.ds)
	}
	d := f.ds[0]
	if d.Pointer != "/a" || d.Offset != 6 || d.Message == "" {
		t.Errorf("diagnostic = %+v, want pointer /a at offset 6 with a message", d)
	}
}

// TestParseConcurrent runs Parse from several goroutines; the lazily
// compiled schema and the schema index are shared. CI runs it with -race.
func TestParseConcurrent(t *testing.T) {
	inputs := [][]byte{
		[]byte(`{"schemaVersion": 0, "name": "n", "layers": {"base": {"a": {"press": {"type": "keys", "keys": "Z"}}}}}`),
		[]byte(`{"schemaVersion": 0, "name": "n", "layers": {"base": {"southh": {}}}}`),
		[]byte(`{"schemaVersion": 0, "name": "n", "layers": {"base": {"south": {"press": {"type": "keys", "keys": "Ctrl+Shfit"}}}}}`),
	}
	want := make([]Diagnostics, len(inputs))
	for i, in := range inputs {
		_, want[i] = Parse(in)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 20 {
				k := (g + i) % len(inputs)
				if _, got := Parse(inputs[k]); !slices.Equal(got, want[k]) {
					t.Errorf("input %d: %v, want %v", k, got, want[k])
					return
				}
			}
		})
	}
	wg.Wait()
}
