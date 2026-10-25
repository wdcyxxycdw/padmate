package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestStateSchemaCurrent(t *testing.T) {
	data, err := os.ReadFile("../../../schema/state.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, stateSchemaJSON) {
		t.Fatal("run task gen")
	}
	if _, err := compiledStateSchema(); err != nil {
		t.Fatal(err)
	}
}

func FuzzState(f *testing.F) {
	f.Add([]byte(`{"schemaVersion":0}`))
	f.Add([]byte(`{"schemaVersion":0,"lastProfile":"paint","devices":{"a":{"profiles":{"paint":{"base":"base"}}}}}`))
	f.Add([]byte(`{"schemaVersion":0.0}`))
	f.Add([]byte(`{"schemaVersion":0,"devices":{"k":{"profiles":{"p":{"base":"base","cycles":{"a":1e0,"b":2.0}}}}}}`))
	f.Add([]byte(`{"schemaVersion":0,"devices":{"k":{"profiles":{"p":{"base":"base","cycles":{"a":1E1000001}}}}}}`))
	f.Add([]byte(`{"schemaVersion":0,"devices":{"k":{"profiles":{"p":{"base":"base","cycles":{"a":1E-1000001}}}}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, ds := ParseState(data)
		if ds.HasErrors() {
			return
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, ds := ParseState(raw); ds.HasErrors() {
			t.Fatalf("cannot roundtrip: %s %v", raw, ds)
		}
	})
}
