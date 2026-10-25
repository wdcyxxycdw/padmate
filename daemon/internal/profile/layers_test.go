package profile

import (
	"fmt"
	"strings"
	"testing"
)

func TestLayerProfileValidation(t *testing.T) {
	for _, tc := range []struct{ name, src, problem string }{
		{"named default", `{"schemaVersion":0,"name":"t","defaultLayer":"paint","layers":{"paint":{}}}`, ""},
		{"common reserved", `{"schemaVersion":0,"name":"t","layers":{"base":{},"common":{}}}`, "reserved"},
		{"blank name", `{"schemaVersion":0,"name":"t","layers":{"base":{}," ":{}}}`, "layer name"},
		{"unknown default", `{"schemaVersion":0,"name":"t","defaultLayer":"paint","layers":{"base":{}}}`, "unknown default layer"},
		{"unknown target", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"press":{"type":"layer","layer":"missing","op":"hold"}}}}}`, "unknown layer"},
		{"tap cannot hold", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"tap":{"type":"layer","layer":"base","op":"hold"}}}}}`, "layer hold action"},
		{"release cannot hold", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"release":{"type":"layer","layer":"base","op":"hold"}}}}}`, "layer hold action"},
		{"block conflicts", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"block":true,"press":{"type":"keys","keys":"F13"}}}}}`, "block"},
		{"cycle target field", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"press":{"type":"layer","layers":["base"],"layer":"base","op":"cycle"}}}}}`, "not"},
		{"hold list field", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"press":{"type":"layer","layers":["base"],"layer":"base","op":"hold"}}}}}`, "layers"},
		{"cycle duplicates", `{"schemaVersion":0,"name":"t","layers":{"base":{"east":{"press":{"type":"layer","layers":["base","base"],"op":"cycle"}}}}}`, "repeated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := Parse([]byte(tc.src))
			if tc.problem == "" {
				if p == nil || ds.HasErrors() {
					t.Fatalf("%v", ds)
				}
				return
			}
			if p != nil || !ds.HasErrors() || !strings.Contains(fmt.Sprint(ds), tc.problem) {
				t.Fatalf("Parse = %v, %v; want %s", p, ds, tc.problem)
			}
		})
	}
}
