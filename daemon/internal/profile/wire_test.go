package profile

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestWireMatchesSchema checks that each wire struct has exactly the
// properties of its schema object, so that json.Unmarshal of a document
// that passed the schema drops nothing. The generated decoding structures
// preserve the semantic parser's wire contract. The layer's control names are checked against
// the control package by TestSchemaLayerControls.
func TestWireMatchesSchema(t *testing.T) {
	var s struct {
		Properties json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		t.Fatal(err)
	}
	var layers struct {
		Layers struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(s.Properties, &layers); err != nil {
		t.Fatal(err)
	}
	props := func(raw ...json.RawMessage) []string {
		var names []string
		for _, r := range raw {
			keys, err := objectKeys(r)
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, keys...)
		}
		slices.Sort(names)
		return slices.Compact(names)
	}
	for _, tc := range []struct {
		name   string
		typ    any
		schema []string
	}{
		{"profile", wireProfile{}, props(s.Properties)},
		{"script", wireScript{}, props(s.Defs["script"].Properties)},
		{"feedback", wireFeedback{}, props(s.Defs["feedback"].Properties)},
		{"functionKeys", wireFunctionKeys{}, props(s.Defs["functionKeys"].Properties)},
		{"layers", nil, props(layers.Layers.Properties)},
		{"binding", wireBinding{}, props(s.Defs["binding"].Properties)},
		// wireAction holds the properties of every action type.
		{"action", wireAction{}, props(s.Defs["action"].Properties, s.Defs["keysAction"].Properties, s.Defs["scriptAction"].Properties, s.Defs["macroAction"].Properties, s.Defs["mouseAction"].Properties, s.Defs["hudAction"].Properties, s.Defs["sequenceAction"].Properties, s.Defs["textAction"].Properties)},
		{"sequence step", wireStep{}, props(s.Defs["sequenceStep"].Properties)},
	} {
		if tc.typ == nil {
			continue
		}
		var tags []string
		rt := reflect.TypeOf(tc.typ)
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			tags = append(tags, name)
		}
		slices.Sort(tags)
		if !slices.Equal(tags, tc.schema) {
			t.Errorf("%s: wire fields %q, schema properties %q", tc.name, tags, tc.schema)
		}
	}
	if len(s.Defs) != 23 {
		t.Errorf("the schema has %d definitions; check the new ones here", len(s.Defs))
	}
	if reflect.TypeOf(wireLayers{}).Kind() != reflect.Map || reflect.TypeOf(wireLayers{}).Elem() != reflect.TypeOf(wireLayer{}) {
		t.Error("wireLayers is not a map of wireLayer")
	}
	if reflect.TypeOf(wireLayer{}).Kind() != reflect.Struct {
		t.Error("wireLayer is not a struct with metadata and bindings")
	}
}
