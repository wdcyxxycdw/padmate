package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

func newActionHarness(t *testing.T, source, binding string) (*clock.Fake, *simtest.Recorder, *slot.Slot, *scriptRuntime) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"actions","script":{"entry":"main.ts","api":"0.21","params":{"amount":9}},"layers":{"base":{"south":` + binding + `}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile: %v", ds)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: 20 * time.Millisecond})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), p.Script, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: clk, Program: program, Arbiter: arb, Script: r})
	if err != nil {
		_ = r.Dispose()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
		if held := recorder.Down(); len(held) != 0 {
			t.Errorf("held after disposal: %v", held)
		}
	})
	return clk, recorder, s, r
}

func TestDeclarativeScriptActionContextAndHeldLifecycle(t *testing.T) {
	const source = `import {defineScript,defineAction,param} from "padmate";
globalThis.seen=[];
export default defineScript({params:{amount:param.number({default:1,max:4})},actions:{grip:defineAction({args:{dir:param.number({default:1,min:-1,max:1})},run(args,ctx,e){seen.push([args.dir,ctx.params.amount,e.control,e.gesture,e.held]);e.holdKeys("Ctrl");e.onEnd(()=>seen.push("ended"));}})}});`
	clk, rec, s, r := newActionHarness(t, source, `{"press":{"type":"script","name":"grip","args":{"dir":9}}}`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if held := rec.Down(); len(held) != 1 || held[0].Key.String() != "LCtrl" {
		t.Fatalf("action hold: %v", held)
	}
	clk.Advance(30 * time.Millisecond)
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	s.Advance()
	if held := rec.Down(); len(held) != 0 {
		t.Fatalf("action release: %v", held)
	}
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
	v, err := r.lifecycle.Current().VM.RunString(`JSON.stringify(seen)`)
	if err != nil || v.String() != `[[1,4,"south","press",true],"ended"]` {
		t.Fatalf("context and end: %v, %v", v, err)
	}
}

func TestDeclarativeScriptActionCancellationAndErrors(t *testing.T) {
	const source = `import {defineScript,defineAction,param,key,sleep} from "padmate";
export default defineScript({params:{amount:param.number({default:1})},actions:{later:defineAction({async run(args,ctx,e){e.holdKeys("Shift");await sleep(100);key.down("X");}})}});`
	clk, rec, s, _ := newActionHarness(t, source, `{"press":{"type":"script","name":"later"}}`)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	s.Advance()
	if held := rec.Down(); len(held) != 0 {
		t.Fatalf("late output after safety release: %v", held)
	}
	for _, e := range rec.Records() {
		if e.Event.Key.String() == "X" {
			t.Fatal("canceled action continuation emitted X")
		}
	}
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}

	_, _, bad, _ := newActionHarness(t, source, `{"press":{"type":"script","name":"missing"}}`)
	bad.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if errs := bad.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "unknown script action") {
		t.Fatalf("missing action diagnostic: %v", errs)
	}
}
