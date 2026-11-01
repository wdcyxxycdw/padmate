package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	f.Add([]byte(`{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"XKW1234567890","side":"L"}]}`))
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

// TestStateControllers: state.json keeps the controllers that have been
// connected (ADR-0017) by DeviceKey.
func TestStateControllers(t *testing.T) {
	s, ds := ParseState([]byte(`{"schemaVersion":0,"controllers":{"k":{"name":"Xbox","type":"xboxone","buttonMask":4294967295,"lastSeen":1730000000000},"j":{"name":"","type":"unknown"}}}`))
	if ds.HasErrors() || len(s.Controllers) != 2 || s.Controllers["k"].ButtonMask != 4294967295 || s.Controllers["k"].LastSeen != 1730000000000 {
		t.Fatalf("%+v %v", s, ds)
	}
	for _, doc := range []string{
		`{"schemaVersion":0,"controllers":[]}`,
		`{"schemaVersion":0,"controllers":{"k":{"type":"x"}}}`,
		`{"schemaVersion":0,"controllers":{"k":{"name":"x"}}}`,
		`{"schemaVersion":0,"controllers":{"k":{"name":"x","type":"y","buttonMask":-1}}}`,
		`{"schemaVersion":0,"controllers":{"k":{"name":"x","type":"y","buttonMask":4294967296}}}`,
		`{"schemaVersion":0,"controllers":{"k":{"name":"x","type":"y","lastSeen":-1}}}`,
		`{"schemaVersion":0,"controllers":{"k":{"name":"x","type":"y","extra":1}}}`,
	} {
		if _, ds := ParseState([]byte(doc)); !ds.HasErrors() {
			t.Errorf("accepted %s", doc)
		}
	}
	var many strings.Builder
	many.WriteString(`{"schemaVersion":0,"controllers":{`)
	for i := range 65 {
		if i > 0 {
			many.WriteByte(',')
		}
		fmt.Fprintf(&many, `"k%d":{"name":"x","type":"y"}`, i)
	}
	many.WriteString(`}}`)
	if _, ds := ParseState([]byte(many.String())); !ds.HasErrors() {
		t.Error("accepted 65 controllers")
	}
}
