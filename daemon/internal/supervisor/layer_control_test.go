package supervisor_test

import (
	"context"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestExternalLayerSelectsDeviceAndKeepsPressOwnership(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"layers","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space","mode":"hold"}}},"alt":{"east":{"press":{"type":"keys","keys":"Shift","mode":"hold"}}}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := start(t, opts{program: prog, slots: 2})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.sync()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.SetLayer(ctx, "2", "switch", "alt"); err != nil {
		t.Fatal(err)
	}
	views, err := h.sup.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Mapping.Layers.Base != "base" || views[1].Mapping.Layers.Base != "alt" {
		t.Fatalf("layers=%+v", views)
	}
	h.expect("+Space@0")
	h.send(button(2, "east", false))
	h.send(button(2, "east", true))
	h.send(button(2, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@0 +LShift@0 -LShift@0")
	if err := h.sup.SetLayer(ctx, "", "switch", "missing"); err == nil {
		t.Fatal("missing layer accepted")
	}
	if err := h.sup.SetLayer(ctx, "missing", "switch", "base"); err == nil {
		t.Fatal("missing device accepted")
	}
	if err := h.sup.SetLayer(ctx, "", "hold", "alt"); err == nil {
		t.Fatal("external hold without a release owner accepted")
	}
}
