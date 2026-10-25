package profile

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

func TestParseNamedScriptAction(t *testing.T) {
	const source = `{"schemaVersion":0,"name":"tools","script":{"entry":"main.ts","api":"0.21","params":{"brush":3}},"layers":{"base":{"south":{"tap":{"type":"script","name":"cycleBrush","args":{"dir":1}}}}}}`
	p, ds := Parse([]byte(source))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	a := p.Layers[0].Binding(control.South).Tap
	if a.Type.String() != "script" || a.Name != "cycleBrush" || a.Args["dir"] != float64(1) || p.Script.Params["brush"] != float64(3) {
		t.Fatalf("action=%+v, script=%+v", a, p.Script)
	}
	for _, invalid := range []string{
		strings.Replace(source, `"name":"cycleBrush"`, `"name":" "`, 1),
		strings.Replace(source, `"args":{"dir":1}`, `"args":[]`, 1),
		strings.Replace(source, `"name":"cycleBrush"`, `"name":"cycleBrush","keys":"Z"`, 1),
		strings.Replace(source, `"script":{"entry":"main.ts","api":"0.21","params":{"brush":3}},`, "", 1),
	} {
		if _, ds := Parse([]byte(invalid)); !ds.HasErrors() {
			t.Errorf("invalid profile accepted: %s", invalid)
		}
	}
}
