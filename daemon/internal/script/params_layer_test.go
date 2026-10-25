package script

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestLayerParametersValidateProfileNames(t *testing.T) {
	for _, tt := range []struct {
		name, initial string
		params        map[string]any
	}{
		{"default", "missing", nil},
		{"configured", "base", map[string]any{"target": "missing"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), LayerNames: []string{"base", "ink"}, Params: tt.params})
			t.Cleanup(func() { _ = h.Dispose() })
			err := activateActionScript(t, h, `defineScript({params:{target:param.layer({default:"`+tt.initial+`"})}})`)
			if err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("unknown layer accepted: %v", err)
			}
		})
	}

	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), LayerNames: []string{"base", "ink"}})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := activateActionScript(t, h, `defineScript({
  params:{target:param.layer({default:"base"})},
  actions:{choose:defineAction({args:{target:param.layer({default:"ink"})},
    run(args,ctx){globalThis.result=ctx.params.target+":"+args.target;}
  })}
})`); err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateParams(map[string]any{"target": "ink"}); err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateParams(map[string]any{"target": "missing"}); err == nil {
		t.Fatal("unknown layer accepted during update")
	}
	if err := h.InvokeAction("choose", nil, Event{}); err != nil {
		t.Fatal(err)
	}
	if got := h.VM.Get("result").String(); got != "ink:ink" {
		t.Fatalf("failed update changed context: %s", got)
	}
	if err := h.InvokeAction("choose", map[string]any{"target": "missing"}, Event{}); err == nil {
		t.Fatal("unknown action layer accepted")
	}
	schema := h.ParamsSchema()
	if err := ValidateParams(schema, map[string]any{"target": "missing"}); err == nil {
		t.Fatal("detached schema accepted unknown layer")
	}
	// Editor metadata must not change later validation in the live host.
	schema["target"].LayerOptions[0] = "missing"
	if err := h.UpdateParams(map[string]any{"target": "missing"}); err == nil {
		t.Fatal("metadata aliases the live schema")
	}
}
