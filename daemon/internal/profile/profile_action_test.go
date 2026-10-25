package profile_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestProfileAction(t *testing.T) {
	for _, target := range []string{`"paint"`, `"mode-2"`, `"../escape"`, `""`, `null`, `"CON"`} {
		p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"switch","layers":{"base":{"south":{"press":{"type":"profile","profile":` + target + `}}}}}`))
		valid := target == `"paint"` || target == `"mode-2"`
		if !valid {
			if !ds.HasErrors() {
				t.Fatal("accepted target", target)
			}
			continue
		}
		if ds.HasErrors() {
			t.Fatal(ds)
		}
		if _, err := engine.Compile(p); err != nil {
			t.Fatal(err)
		}
	}
}
