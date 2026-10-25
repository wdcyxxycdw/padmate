package profile

import (
	"fmt"
	"testing"
)

func TestScriptTickHzDefaultsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
		valid bool
	}{
		{"", 125, true}, {`,"tickHz":20`, 20, true}, {`,"tickHz":1`, 1, true}, {`,"tickHz":1000`, 1000, true},
		{`,"tickHz":0`, 0, false}, {`,"tickHz":1001`, 0, false}, {`,"tickHz":1.5`, 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			p, ds := Parse(fmt.Appendf(nil, `{"schemaVersion":0,"name":"tick","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.21"%s}}`, tc.value))
			if !tc.valid {
				if p != nil || !ds.HasErrors() {
					t.Fatalf("invalid tickHz accepted: %+v", p)
				}
				return
			}
			if ds.HasErrors() || p == nil || p.Script.TickHz != tc.want {
				t.Fatalf("profile=%+v errors=%v", p, ds)
			}
		})
	}
}
