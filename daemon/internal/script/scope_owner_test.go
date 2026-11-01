package script

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

func TestHostScriptScopesAndPass(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	p, err := goja.Compile("main.js", `(() => {
		const b = globalThis.__padmate;
		b.on("south", "press", () => b.key.tap("Z"));
		b.observe("south", () => b.key.tap("O"));
		b.global().on("south", "press", (e) => { b.key.tap("X"); e.pass(); });
	})()`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	result, err := h.DispatchEvent(Event{Control: "south", Gesture: "press", Scope: "overlay"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Consumed {
		t.Fatal("global pass consumed overlay event")
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.Output(); len(got) != 4 || got[0].Spec != "O" || got[1].Spec != "X" || got[2].Spec != "O" || got[3].Spec != "X" || !got[3].Up {
		t.Fatalf("overlay output = %+v, want observer O and X tap", got)
	}
	result, err = h.DispatchEvent(Event{Control: "south", Gesture: "press", Scope: "base"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Consumed {
		t.Fatal("base handler did not consume after global pass")
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.Output(); len(got) != 10 || got[6].Spec != "Z" || got[9].Spec != "Z" || !got[9].Up {
		t.Fatalf("base output = %+v, want observer O then Z taps", got)
	}
}

type scriptOutputCall struct {
	kind  string
	owner output.Owner
	chord keys.Chord
	hold  time.Duration
}

type scriptOutputRecorder struct {
	calls []scriptOutputCall
}

func (r *scriptOutputRecorder) Down(o output.Owner, c keys.Chord) error {
	r.calls = append(r.calls, scriptOutputCall{kind: "down", owner: o, chord: c})
	return nil
}

func (r *scriptOutputRecorder) Up(o output.Owner, c keys.Chord) error {
	r.calls = append(r.calls, scriptOutputCall{kind: "up", owner: o, chord: c})
	return nil
}

func (r *scriptOutputRecorder) Tap(o output.Owner, c keys.Chord, hold time.Duration) error {
	r.calls = append(r.calls, scriptOutputCall{kind: "tap", owner: o, chord: c, hold: hold})
	return nil
}

func (r *scriptOutputRecorder) TapWithCompletion(o output.Owner, c keys.Chord, hold time.Duration, done func(output.TapCompletion)) error {
	if err := r.Tap(o, c, hold); err != nil {
		return err
	}
	if done != nil {
		done(output.TapCompletion{})
	}
	return nil
}

func (r *scriptOutputRecorder) ReleaseOwner(o output.Owner) error {
	r.calls = append(r.calls, scriptOutputCall{kind: "release", owner: o})
	return nil
}

func TestHostKeyOutputUsesRuntimeOwner(t *testing.T) {
	c := clock.NewFake()
	r := new(scriptOutputRecorder)
	owner := output.Owner{Slot: 2, Gen: 7, Source: output.SourceDecl}
	wantOwner := owner
	wantOwner.Source = output.SourceScript
	h := NewHostWithOptions(HostOptions{Clock: c, Output: r, Owner: owner})
	p, err := goja.Compile("main.js", `globalThis.__padmate_bundle = { default: {
		onActivate() {
			const b = globalThis.__padmate;
			b.key.down("ctrl + z");
			b.key.up("Ctrl+Z");
			b.key.tap("Shift+X", { holdMs: 15 });
			b.key.releaseAll();
		}
	} };`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 {
		t.Fatalf("arbiter calls = %+v, want down/up/tap/release", r.calls)
	}
	if r.calls[0].kind != "down" || r.calls[0].chord.String() != "LCtrl+Z" {
		t.Fatalf("down call = %+v", r.calls[0])
	}
	if r.calls[1].kind != "up" || r.calls[1].chord.String() != "LCtrl+Z" {
		t.Fatalf("up call = %+v", r.calls[1])
	}
	if r.calls[2].kind != "tap" || r.calls[2].chord.String() != "LShift+X" || r.calls[2].hold != 15*time.Millisecond {
		t.Fatalf("tap call = %+v", r.calls[2])
	}
	for i, call := range r.calls {
		if call.owner != wantOwner {
			t.Errorf("call %d owner = %v, want %v", i, call.owner, wantOwner)
		}
	}
	if got := h.Output(); len(got) != 4 || got[0].Spec != "LCtrl+Z" || got[1].Spec != "LCtrl+Z" || got[2].Spec != "LShift+X" {
		t.Fatalf("recorded output = %+v", got)
	}
}

func TestLifecycleSwapReleasesSharedRuntimeOwnerBeforeActivate(t *testing.T) {
	c := clock.NewFake()
	r := new(scriptOutputRecorder)
	l := NewLifecycle(c, HostOptions{Output: r})
	build := func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `globalThis.__padmate_bundle = { default: {
			onActivate() { globalThis.__padmate.key.down("Z"); }
		} };`, false)
	}
	first := <-l.Load(context.Background(), build)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	second := <-l.Load(context.Background(), build)
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if err := l.Swap(second.Host); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 || r.calls[0].kind != "down" || r.calls[1].kind != "down" || r.calls[2].kind != "up" || r.calls[3].kind != "release" {
		t.Fatalf("shared-owner swap calls = %+v, want down/down/up/release", r.calls)
	}
	if r.calls[0].owner.Cycle == 0 || r.calls[0].owner.Cycle == r.calls[1].owner.Cycle {
		t.Fatalf("runtime owner cycles = %d and %d, want distinct nonzero cycles", r.calls[0].owner.Cycle, r.calls[1].owner.Cycle)
	}
}

func TestLifecycleSwapActivationFailureKeepsOldRuntime(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	good := func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `globalThis.__padmate_bundle = { default: {} };`, false)
	}
	first := <-l.Load(context.Background(), good)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	bad := func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `globalThis.__padmate_bundle = { default: {
			onActivate() { throw new Error("activate failed"); }
		} };`, false)
	}
	second := <-l.Load(context.Background(), bad)
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if err := l.Swap(second.Host); err == nil {
		t.Fatal("Swap succeeded for a failing onActivate")
	}
	if l.Current() != first.Host || l.State() != StateRunning || first.Host.State() != StateRunning {
		t.Fatalf("after failed swap current/state/old = %v/%v/%v", l.Current(), l.State(), first.Host.State())
	}
}
