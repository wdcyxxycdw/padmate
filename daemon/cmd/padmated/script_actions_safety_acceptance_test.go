package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// Named actions must use the lifecycle's interruption policy. Stack overflow
// bypasses JS finally blocks, so the host must release both event and runtime
// owners and invalidate the old continuous and asynchronous work itself.
func TestDeclarativeScriptActionOverflowReleasesAndCancelsContinuous(t *testing.T) {
	const source = `import {defineScript,defineAction,param,onTick,key,sleep} from "padmate";
export default defineScript({
  params:{amount:param.number({default:1})},
  setup(){onTick(()=>key.down("Y"));},
  actions:{fail:defineAction({run(args,ctx,e){
    e.holdKeys("LCtrl");key.down("X");
    sleep(100).then(()=>key.down("Z"));
    function recur(){recur();}recur();
  }})}
});`
	clk, rec, s, runtime := newActionHarness(t, source, `{"press":{"type":"script","name":"fail"}}`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	errs := s.Errors()
	var overflow *goja.StackOverflowError
	if len(errs) == 0 || !errors.As(errs[0], &overflow) {
		t.Fatalf("expected action stack overflow, got %v", errs)
	}
	if runtime.lifecycle.Current() != nil {
		t.Fatal("overflow left action runtime active")
	}
	trace := simtest.FormatTrace(rec.Edges())
	for _, edge := range []string{"+LCtrl@0", "+X@0", "-LCtrl@0", "-X@0"} {
		if !strings.Contains(trace, edge) {
			t.Fatalf("overflow did not press and release both owners: missing %s in %q", edge, trace)
		}
	}
	simtest.AssertNoneHeld(t, nil, rec)
	clk.Advance(time.Second)
	s.Advance()
	for _, record := range rec.Records() {
		if name := record.Event.Key.String(); name == "Y" || name == "Z" {
			t.Fatalf("old continuous or async work emitted %s after action overflow", name)
		}
	}
	simtest.AssertNoneHeld(t, nil, rec)
}
