package profile

import (
	"strings"
	"testing"
)

func TestControlNames(t *testing.T) {
	for _, tc := range []struct {
		name, names string
		valid       bool
	}{
		{"standard", `{"south":"画笔","dpad.up":"放大","lt":"缩小"}`, true},
		{"empty", `{}`, true},
		{"alias", `{"a":"画笔"}`, false},
		{"unknown", `{"nonesuch":"画笔"}`, false},
		{"blank", `{"south":"   "}`, false},
		{"long", `{"south":"` + strings.Repeat("字", 81) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := Parse([]byte(`{"schemaVersion":0,"name":"test","controlNames":` + tc.names + `,"layers":{"base":{}}}`))
			if (p != nil) != tc.valid || ds.HasErrors() == tc.valid {
				t.Fatalf("profile=%v diagnostics=%v", p, ds)
			}
			if tc.name == "standard" && p.ControlNames["south"] != "画笔" {
				t.Fatal(p.ControlNames)
			}
		})
	}
}
