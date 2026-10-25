package script

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

func TestHostReportsCurrentAPIVersion(t *testing.T) {
	h := NewHost(clock.NewFake())
	for _, member := range []string{"version", "api"} {
		value, err := h.VM.RunString(compile.HostGlobal + ".host." + member)
		if err != nil {
			t.Fatal(err)
		}
		if value.String() != dtsgen.HostAPI.String() {
			t.Errorf("host.%s = %q, want %s", member, value.String(), dtsgen.HostAPI)
		}
	}
}

func TestHostSetupDispatchAndSleep(t *testing.T) {
	c := clock.NewFake()
	root := t.TempDir()
	src := `import { defineScript, on, key, sleep } from "padmate";
export default defineScript({
  setup() { on("south", "press", async (e) => { key.down("Z"); await sleep(10); key.up("Z"); }); }
});`
	if err := writeScript(root, "main.ts", src); err != nil {
		t.Fatal(err)
	}
	r, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("main.js", string(r.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(c)
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if len(h.Registrations()) != 1 {
		t.Fatalf("registrations = %d, want 1", len(h.Registrations()))
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press", Time: c.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := h.Output(); len(got) != 1 || got[0].Spec != "Z" || got[0].Up {
		t.Fatalf("output after press = %+v, want Z down", got)
	}
	c.Advance(10 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.Output(); len(got) != 2 || !got[1].Up {
		t.Fatalf("output after sleep = %+v, want Z up", got)
	}
}

func TestHostDisposableAndObserve(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	prog, err := goja.Compile("main.js", `(() => { const b = globalThis.__padmate; const d = b.on("south", "press", () => {}); b.observe("south", () => {}); d.dispose(); })()`, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.VM.RunProgram(prog); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	regs := h.Registrations()
	if len(regs) != 2 {
		t.Fatalf("registrations = %d, want 2", len(regs))
	}
}

func TestHostRejectsAsyncSetup(t *testing.T) {
	c := clock.NewFake()
	root := t.TempDir()
	src := `import { defineScript, sleep } from "padmate";
export default defineScript({ async setup() { await sleep(1); } });`
	if err := writeScript(root, "main.ts", src); err != nil {
		t.Fatal(err)
	}
	r, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("main.js", string(r.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	err = NewHost(c).Activate(p)
	if err == nil || err.Error() != "setup must be synchronous; start async work in onActivate" {
		t.Fatalf("Activate error = %v, want synchronous setup diagnostic", err)
	}
}

func TestHostSetupCannotEmitOutput(t *testing.T) {
	c := clock.NewFake()
	sink := new(hostOutputProbe)
	h := NewHostWithOptions(HostOptions{Clock: c, Output: sink})
	p, err := goja.Compile("setup.js", `globalThis.__padmate_bundle = { default: { setup() {
  __padmate.key.down("Z");
  __padmate.key.tap("X");
} } };`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if sink.downs != 0 || sink.taps != 0 {
		t.Fatalf("setup output calls = down %d, tap %d; want none", sink.downs, sink.taps)
	}
	if got := h.Output(); len(got) != 0 {
		t.Fatalf("setup recorded output = %+v, want none", got)
	}
}

func TestHostConsoleGlobalAndIdempotentDispose(t *testing.T) {
	h := NewHost(clock.NewFake())
	prog, err := goja.Compile("console.js", `console.log("log"); console.warn("warn"); console.error("error");`, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.VM.RunProgram(prog); err != nil {
		t.Fatalf("console global: %v", err)
	}
	if got, dropped := h.DrainLogs(); dropped != 0 || !slices.Equal(got, []LogEntry{
		{Level: "info", Message: "log"}, {Level: "warn", Message: "warn"}, {Level: "error", Message: "error"},
	}) {
		t.Fatalf("console logs = %v, dropped %d", got, dropped)
	}

	count := 0
	// Use a direct Go callback to observe teardown once while avoiding a
	// second activation solely for this lifecycle assertion.
	h.teardown = func(goja.Value, ...goja.Value) (goja.Value, error) {
		count++
		return goja.Undefined(), nil
	}
	if err := h.Dispose(); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispose(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("teardown count = %d, want 1", count)
	}
}

func TestHostLogBufferFormatsAndBoundsMessages(t *testing.T) {
	h := NewHost(clock.NewFake())
	if _, err := h.VM.RunString(`__padmate.log.info("value", {toString(){return "object"}}, 7); console.warn("warn");`); err != nil {
		t.Fatal(err)
	}
	got, dropped := h.DrainLogs()
	if dropped != 0 || len(got) != 2 || got[0].Level != "info" || got[0].Message != "value object 7" || got[1].Message != "warn" {
		t.Fatalf("logs = %+v, dropped = %d", got, dropped)
	}
	if got, dropped := h.DrainLogs(); len(got) != 0 || dropped != 0 {
		t.Fatalf("second drain = %+v, dropped = %d", got, dropped)
	}
	for range 300 {
		if _, err := h.VM.RunString(`__padmate.log.debug("` + strings.Repeat("a", 4096) + `")`); err != nil {
			t.Fatal(err)
		}
	}
	got, dropped = h.DrainLogs()
	if len(got) != 256 || dropped != 44 {
		t.Fatalf("bounded log buffer = %d entries, %d dropped", len(got), dropped)
	}
	for _, entry := range got {
		if len(entry.Message) > 2048 {
			t.Fatalf("log message length = %d", len(entry.Message))
		}
	}
}

func TestScriptLogFormattingUsesHandlerWatchdog(t *testing.T) {
	h := NewHost(clock.Real())
	h.EnableWatchdog()
	program, err := goja.Compile("log.js", `__padmate.on("south", "press", () => __padmate.log.info({toString(){while(true){}}}));`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(program); err != nil {
		t.Fatal(err)
	}
	err = h.Dispatch(Event{Control: "south", Gesture: "press"})
	var budget *BudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("log argument toString = %v, want watchdog budget error", err)
	}
}

func TestHostOutputBudgetLimitsDownAndNeverDropsUp(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	chord := keys.MustParse("Ctrl+Z")
	for i := 0; i < 24; i++ {
		h.keyDownChord(h.base, chord)
		h.keyUpChord(h.base, chord)
	}
	h.keyDownChord(h.base, chord)
	// 25 two-key down/up cycles reserve the burst exactly. A further down is rejected
	// and reports an error, while the already-held chord still releases.
	h.keyDownChord(h.base, keys.MustParse("X"))
	if !errors.Is(h.takeErrors(), ErrOutputBudget) {
		t.Fatal("budget rejection was not reported")
	}
	h.keyUpChord(h.base, chord)
	var downs, ups int
	for _, e := range h.Output() {
		if e.Up {
			ups++
		} else {
			downs++
		}
	}
	if downs != 25 || ups != 25 {
		t.Fatalf("output transitions = %d down, %d up; want 25 held chord cycles", downs, ups)
	}
}

func TestHostTightDownUpLoopCountsBothChanges(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	chord := keys.MustParse("Z")
	for i := 0; i < 1000; i++ {
		h.keyDownChord(h.base, chord)
		h.keyUpChord(h.base, chord)
		c.Advance(time.Millisecond)
	}
	var downs, ups int
	for _, e := range h.Output() {
		if e.Up {
			ups++
		} else {
			downs++
		}
	}
	// The 100-change burst plus 500 changes replenished over one second
	// permit at most 300 complete down/up cycles, not 500.
	if downs > 300 || ups != downs {
		t.Fatalf("one-second down/up loop = %d downs, %d ups", downs, ups)
	}
	if !errors.Is(h.takeErrors(), ErrOutputBudget) {
		t.Fatal("excess down/up cycles did not report the limiter")
	}
}

func TestHostTapBudgetResolvesDroppedWithoutOutput(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	h.active = true
	for i := 0; i < OutputBurst; i++ {
		if !h.budget.Allow(1) {
			t.Fatalf("reserve burst token %d", i)
		}
	}
	v, err := h.VM.RunString(`__padmate.key.tap("Z")`)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		t.Fatalf("tap result = %T, want Promise", v.Export())
	}
	if p.State() != goja.PromiseStateFulfilled {
		t.Fatalf("tap promise state = %v, want fulfilled", p.State())
	}
	if dropped := p.Result().ToObject(h.VM).Get("dropped").ToBoolean(); !dropped {
		t.Fatal("dropped tap result = false, want true")
	}
	if got := h.Output(); len(got) != 0 {
		t.Fatalf("dropped tap recorded output: %+v", got)
	}
}

func TestHostTightTapLoopHonorsBurstAndPending(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.tap("Z")`)
	if err != nil {
		t.Fatal(err)
	}
	first := v.Export().(*goja.Promise)
	v, err = h.VM.RunString(`__padmate.key.tap("Z")`)
	if err != nil {
		t.Fatal(err)
	}
	second := v.Export().(*goja.Promise)
	if first.State() != goja.PromiseStatePending || !second.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatalf("tight taps: first=%v second=%v", first.State(), second.Result())
	}
	h.ReleaseKeys()
	c.Advance(time.Second)
	v, err = h.VM.RunString(`__padmate.key.tap("Z", {holdMs:100})`)
	if err != nil {
		t.Fatal(err)
	}
	if v.Export().(*goja.Promise).State() != goja.PromiseStatePending {
		t.Fatal("timed tap did not remain pending")
	}
	h.ReleaseKeys()
	v, err = h.VM.RunString(`__padmate.key.tap("Z", {holdMs:100})`)
	if err != nil {
		t.Fatal(err)
	}
	if v.Export().(*goja.Promise).State() != goja.PromiseStatePending {
		t.Fatal("tap after releaseAll was dropped")
	}
}

func writeScript(root, name, src string) error {
	return os.WriteFile(filepath.Join(root, name), []byte(src), 0o600)
}

type hostOutputProbe struct{ downs, taps int }

func (p *hostOutputProbe) Down(output.Owner, keys.Chord) error { p.downs++; return nil }
func (p *hostOutputProbe) Up(output.Owner, keys.Chord) error   { return nil }
func (p *hostOutputProbe) Tap(output.Owner, keys.Chord, time.Duration) error {
	p.taps++
	return nil
}
func (p *hostOutputProbe) ReleaseOwner(output.Owner) error { return nil }
