package profile

import (
	"strings"
	"testing"
)

func TestValidIntegrationID(t *testing.T) {
	for id, want := range map[string]bool{
		"csp": true, "a": true, "a1b2": true, strings.Repeat("a", 32): true,
		"": false, "1a": false, "A": false, "a-b": false, "a_b": false, "a.b": false, strings.Repeat("a", 33): false, "é": false,
	} {
		if got := ValidIntegrationID(id); got != want {
			t.Errorf("ValidIntegrationID(%q) = %v", id, got)
		}
	}
}

func TestDecodeStrict(t *testing.T) {
	if v, err := DecodeStrict([]byte(`{"a":[1,"x",null,true]}`)); err != nil || v == nil {
		t.Fatal(v, err)
	}
	for _, bad := range []string{``, `{"a":1,}`, `{"a":1,"a":2}`, `{} {}`, "{\"a\":\"\xff\"}", `{"a":1} // c`} {
		if _, err := DecodeStrict([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := DecodeStrict([]byte("{\n\"a\": ,}")); err == nil || !strings.HasPrefix(err.Error(), "2:") {
		t.Errorf("position: %v", err)
	}
}

func TestConfigIntegrations(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"integrations":{"csp":{"dataDir":"D:/x"},"other":{}}}`))
	if ds.Err() != nil || c.Integrations["csp"].DataDir != `D:/x` || len(c.Integrations) != 2 {
		t.Fatal(ds, c)
	}
	for _, bad := range []string{
		`{"schemaVersion":0,"integrations":{"CSP":{}}}`,
		`{"schemaVersion":0,"integrations":{"csp":{"dataDir":1}}}`,
		`{"schemaVersion":0,"integrations":{"csp":{"x":1}}}`,
		`{"schemaVersion":0,"integrations":[]}`,
	} {
		if _, ds := ParseConfig([]byte(bad)); ds.Err() == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	// Repair resets the broken member and keeps the rest.
	fixed, rep := RepairConfig([]byte(`{"schemaVersion":0,"enabled":false,"integrations":{"csp":{"dataDir":1},"x2":{"dataDir":"D:/y"}}}`), func(string) bool { return true })
	cfg, ds := ParseConfig(fixed)
	if ds.Err() != nil || rep.Reset || cfg.Enabled || cfg.Integrations["x2"].DataDir != `D:/y` {
		t.Fatalf("%s %+v %v", fixed, rep, ds)
	}
	fixed, _ = RepairConfig([]byte(`{"schemaVersion":0,"integrations":{"Bad":{}}}`), func(string) bool { return true })
	if _, ds := ParseConfig(fixed); ds.Err() != nil {
		t.Fatalf("%s %v", fixed, ds)
	}
}

func TestStateIntegrations(t *testing.T) {
	s, ds := ParseState([]byte(`{"schemaVersion":0,"integrations":{"paint":{"csp":{"dismissed":"abc"}}}}`))
	if ds.Err() != nil || s.Integrations["paint"]["csp"].Dismissed != "abc" {
		t.Fatal(ds, s)
	}
	c := s.Clone()
	c.Integrations["paint"]["csp"] = IntegrationState{Dismissed: "zzz"}
	if s.Integrations["paint"]["csp"].Dismissed != "abc" {
		t.Error("Clone shares the map")
	}
	for _, bad := range []string{
		`{"schemaVersion":0,"integrations":{"Paint":{"csp":{}}}}`,
		`{"schemaVersion":0,"integrations":{"paint":{"CSP":{}}}}`,
		`{"schemaVersion":0,"integrations":{"paint":{"csp":{"dismissed":1}}}}`,
		`{"schemaVersion":0,"integrations":{"paint":{"csp":{"other":"x"}}}}`,
	} {
		if _, ds := ParseState([]byte(bad)); ds.Err() == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
