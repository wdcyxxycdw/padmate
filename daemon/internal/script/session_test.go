package script

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestSessionCopiesAndCandidateIsolation(t *testing.T) {
	c := clock.NewFake()
	session := NewSession()
	life := NewLifecycle(c, HostOptions{Session: session})
	defer func() { _ = life.Dispose() }()
	load := func(src string) LoadResult {
		return <-life.Load(context.Background(), func(context.Context) (*goja.Program, error) { return continuousProgram(t, src), nil })
	}
	first := load(`const s=__padmate.session;let value={seq:1,nested:[1]};s.set("cycle",value);value.seq=99;
	let copy=s.get("cycle");copy.nested.push(2);if(s.get("cycle").seq!==1||s.get("cycle").nested.length!==1)throw Error("not copied");`)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := life.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	failed := load(`__padmate.session.set("cycle",{seq:99});throw Error("setup failed");`)
	if failed.Err == nil {
		t.Fatal("setup should fail")
	}
	if _, err := first.Host.VM.RunString(`if(__padmate.session.get("cycle").seq!==1)throw Error("failed candidate changed session");`); err != nil {
		t.Fatal(err)
	}
	next := load(`__padmate.session.set("extra",1);`)
	if next.Err != nil {
		t.Fatal(next.Err)
	}
	if _, err := first.Host.VM.RunString(`__padmate.session.set("cycle",{seq:2});`); err != nil {
		t.Fatal(err)
	}
	if err := life.Swap(next.Host); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Host.VM.RunString(`if(__padmate.session.get("cycle").seq!==2||__padmate.session.get("extra")!==1)throw Error("swap overwrote running state");__padmate.session.delete("extra");if(__padmate.session.get("extra")!==undefined)throw Error("delete");`); err != nil {
		t.Fatal(err)
	}
}

func TestSessionFailedActivationAndOldTeardownAreIsolated(t *testing.T) {
	c := clock.NewFake()
	life := NewLifecycle(c, HostOptions{Session: NewSession()})
	defer func() { _ = life.Dispose() }()
	load := func(src string) LoadResult {
		return <-life.Load(context.Background(), func(context.Context) (*goja.Program, error) { return continuousProgram(t, src), nil })
	}
	first := load(`__padmate.session.set("seq",1);globalThis.__padmate_bundle={default:{teardown(){__padmate.session.set("seq",999)}}};`)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := life.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	failed := load(`globalThis.__padmate_bundle={default:{onActivate(){__padmate.session.set("seq",99);throw Error("activation failed")}}};`)
	if failed.Err != nil {
		t.Fatal(failed.Err)
	}
	if err := life.Swap(failed.Host); err == nil {
		t.Fatal("activation should fail")
	}
	if _, err := first.Host.VM.RunString(`if(__padmate.session.get("seq")!==1)throw Error("failed activation changed session");`); err != nil {
		t.Fatal(err)
	}
	next := load(`globalThis.__padmate_bundle={default:{onActivate(){if(__padmate.session.get("seq")!==2)throw Error("stale activation snapshot")}}};`)
	if next.Err != nil {
		t.Fatal(next.Err)
	}
	if _, err := first.Host.VM.RunString(`__padmate.session.set("seq",2);`); err != nil {
		t.Fatal(err)
	}
	if err := life.Swap(next.Host); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for life.TeardownsPending() && time.Now().Before(deadline) {
		c.Advance(time.Millisecond)
		if err := life.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if life.TeardownsPending() {
		t.Fatal("teardown did not finish")
	}
	if _, err := next.Host.VM.RunString(`if(__padmate.session.get("seq")!==2)throw Error("old teardown changed session");`); err != nil {
		t.Fatal(err)
	}
}
