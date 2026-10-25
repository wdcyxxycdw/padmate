package script

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func activateActionScript(t *testing.T, h *Host, src string) error {
	t.Helper()
	p, err := goja.Compile("actions.js", `const {defineScript,defineAction,param,key,sleep} = __padmate;
var __padmate_bundle = {default: `+src+`};`, false)
	if err != nil {
		t.Fatal(err)
	}
	return h.Activate(p)
}

func TestActionParamsContextAndUpdates(t *testing.T) {
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Profile: "profile-key", Params: map[string]any{"size": 99.0}})
	t.Cleanup(func() { _ = h.Dispose() })
	err := activateActionScript(t, h, `defineScript({
  params: {size:param.number({default:2,min:0,max:10,step:2})},
  setup(ctx) { globalThis.setupCount = 1; globalThis.setupSize = ctx.params.size; },
  actions: {cycleBrush:defineAction({
    label:"Cycle brush", args:{dir:param.number({default:1,min:-1,max:1,step:2})},
    run(args,ctx,e) { globalThis.result = [args.dir,ctx.params.size,ctx.profile,e.control,e.gesture,e.cycle];
      globalThis.readonly = !Reflect.set(ctx.params,"size",0); }
  })}
})`)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.VM.Get("setupSize").ToInteger(); got != 10 {
		t.Fatalf("setup size = %d, want clamped 10", got)
	}
	e := Event{Control: "south", Gesture: "tap", Cycle: 7}
	if err := h.InvokeAction("cycleBrush", map[string]any{"dir": -5.0}, e); err != nil {
		t.Fatal(err)
	}
	result, err := h.VM.RunString(`JSON.stringify(result)`)
	if err != nil || result.String() != `[-1,10,"profile-key","south","tap",7]` || !h.VM.Get("readonly").ToBoolean() {
		t.Fatalf("result = %v, readonly = %v, err = %v", result, h.VM.Get("readonly"), err)
	}
	if err := h.UpdateParams(map[string]any{"size": 3.0}); err != nil {
		t.Fatal(err)
	}
	if err := h.InvokeAction("cycleBrush", nil, e); err != nil {
		t.Fatal(err)
	}
	result, err = h.VM.RunString(`JSON.stringify(result)`)
	if err != nil || result.String() != `[1,4,"profile-key","south","tap",7]` || h.VM.Get("setupCount").ToInteger() != 1 {
		t.Fatalf("updated result = %v, err = %v", result, err)
	}
	if err := h.UpdateParams(map[string]any{"typo": 2.0}); err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("unknown parameter: %v", err)
	}
	if err := h.InvokeAction("missing", nil, e); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown action: %v", err)
	}
	if err := h.InvokeAction("cycleBrush", map[string]any{"dir": "bad"}, e); err == nil {
		t.Fatal("string accepted as numeric argument")
	}
}

func TestActionParamKinds(t *testing.T) {
	h := NewHost(clock.NewFake())
	t.Cleanup(func() { _ = h.Dispose() })
	err := activateActionScript(t, h, `defineScript({actions:{test:defineAction({args:{
    b:param.boolean({default:true}), c:param.choice({options:["pen","erase"],default:"pen"}),
    t:param.text({default:"笔刷abc",maxLength:3}), k:param.key({default:"shift+z"}),
    control:param.control({default:"a"}), layer:param.layer({default:"alt"})},
    run(args) { globalThis.values = JSON.stringify(args); }
  })}})`)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.InvokeAction("test", nil, Event{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"b":true`, `"c":"pen"`, `"t":"笔刷a"`, `"k":"Shift+Z"`, `"control":"south"`, `"layer":"alt"`} {
		if !strings.Contains(h.VM.Get("values").String(), want) {
			t.Errorf("values %s do not include %s", h.VM.Get("values"), want)
		}
	}
	for _, args := range []map[string]any{{"b": 1}, {"c": "unknown"}, {"control": "missing"}, {"k": "not-a-key"}, {"unexpected": true}} {
		if err := h.InvokeAction("test", args, Event{}); err == nil {
			t.Errorf("invalid args accepted: %v", args)
		}
	}
}

func TestActionInvalidDefinitions(t *testing.T) {
	for _, def := range []string{
		`defineScript({params:{x:param.number({default:1,min:2,max:0})}})`,
		`defineScript({params:{x:param.number({default:1,step:0})}})`,
		`defineScript({params:{x:param.number({default:Infinity})}})`,
		`defineScript({params:{x:param.choice({default:"x",options:["y"]})}})`,
		`defineScript({params:{x:param.text({default:"x",maxLength:-1})}})`,
		`defineScript({params:{x:{default:1}}})`,
		`defineScript({actions:{oops:defineAction({run:3})}})`,
	} {
		t.Run(def, func(t *testing.T) {
			h := NewHost(clock.NewFake())
			t.Cleanup(func() { _ = h.Dispose() })
			if err := activateActionScript(t, h, def); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
}

func TestActionAsyncContinuationAndSafety(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	t.Cleanup(func() { _ = h.Dispose() })
	if err := activateActionScript(t, h, `defineScript({actions:{later:defineAction({async run(){
  key.down("Z"); await sleep(5); key.up("Z"); key.tap("X");
}})}})`); err != nil {
		t.Fatal(err)
	}
	if err := h.InvokeAction("later", nil, Event{}); err != nil {
		t.Fatal(err)
	}
	if len(h.Output()) != 1 || h.Output()[0].Spec != "Z" {
		t.Fatalf("initial output: %v", h.Output())
	}
	h.Suspend()
	c.Advance(10 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	for _, output := range h.Output() {
		if output.Spec == "X" {
			t.Fatal("action continuation survived safety boundary")
		}
	}
}

func TestActionDescribeOnlyCollectsMetadata(t *testing.T) {
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Mode: "describe"})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := activateActionScript(t, h, `defineScript({
  setup(){key.down("Z")}, onActivate(){key.down("X")},
  actions:{paint:defineAction({label:"Paint",args:{size:param.number({default:3,min:1,max:9})},run(){key.down("Y")}})}
})`); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	if err := h.InvokeAction("paint", nil, Event{}); err == nil {
		t.Fatal("action ran in describe mode")
	}
	if len(h.Output()) != 0 {
		t.Fatalf("describe produced output: %v", h.Output())
	}
	infos := h.Actions()
	if len(infos) != 1 || infos[0].Name != "paint" || infos[0].Label != "Paint" || infos[0].Args["size"].Default != float64(3) {
		t.Fatalf("action metadata: %+v", infos)
	}
}
