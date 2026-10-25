package script

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestInspectionMetadataIsDetachedAndHasNoCallbacks(t *testing.T) {
	h := NewHost(clock.NewFake())
	t.Cleanup(func() { _ = h.Dispose() })
	if err := activateActionScript(t, h, `defineScript({
params:{size:param.number({default:1,min:0,max:5})},
actions:{paint:defineAction({label:"Paint",args:{key:param.key({default:"F13"})},run(){}})},
setup(){const d=__padmate.on("east","press",()=>{});d.dispose();__padmate.on("south","press",()=>{}, {label:"South"});__padmate.onStick("rs",()=>{});}
})`); err != nil {
		t.Fatal(err)
	}
	m := h.Metadata()
	if m.Runtime == 0 || len(m.Registrations) != 1 || m.Registrations[0].Control != "south" || len(m.Actions) != 1 || len(m.Continuous) != 1 || m.Continuous[0].Control != "rs" {
		t.Fatalf("metadata=%+v", m)
	}
	data, err := json.Marshal(m)
	if err != nil || strings.Contains(string(data), "Handler") || !strings.Contains(string(data), `"kind":"number"`) {
		t.Fatal(string(data), err)
	}
	p := m.Params["size"]
	*p.Max = 999
	m.Params["size"] = p
	m.Actions[0].Args["key"] = ParamInfo{Kind: "text"}
	m.Registrations[0].Label = "changed"
	got := h.Metadata()
	if *got.Params["size"].Max != 5 || got.Actions[0].Args["key"].Kind != "key" || got.Registrations[0].Label != "South" {
		t.Fatal("inspection mutated host metadata")
	}
}
