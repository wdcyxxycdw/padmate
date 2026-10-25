package script

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func environmentProgram(t *testing.T, src string) *goja.Program {
	t.Helper()
	p, err := goja.Compile("environment.js", src, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvironmentIdleNotificationAndSafety(t *testing.T) {
	env := new(Environment)
	wake := make(chan struct{}, 1)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Wake: func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := h.Activate(environmentProgram(t, `var seen = []; const app = __padmate.app;
app.onChange(a => seen.push(a === null ? null : a.exe));
const removed = app.onChange(() => { throw new Error("disposed listener"); }); removed.dispose();`)); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.VM.RunString(`app.current === null && !app.is("paint.exe")`); !v.ToBoolean() {
		t.Fatal("initial foreground")
	}
	// Publish from another goroutine; no JS runs outside the host owner.
	var wg sync.WaitGroup
	wg.Go(func() { env.SetApp(&AppInfo{Exe: "Paint.EXE", Path: `C:\Paint.EXE`, PID: 12, Elevated: true}) })
	wg.Wait()
	select {
	case <-wake:
	default:
		t.Fatal("idle host not woken")
	}
	if v, _ := h.VM.RunString(`seen.length`); v.ToInteger() != 0 {
		t.Fatal("callback ran on publisher")
	}
	if _, ok := h.NextDeadline(); !ok {
		t.Fatal("no deadline for idle notification")
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	v, err := h.VM.RunString(`seen.length === 1 && seen[0] === "Paint.EXE" && app.is("paint.exe") && !app.is("Paint") && app.current.pid === 12 && app.current.elevated && !("title" in app.current)`)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("snapshot: %v %v", v, err)
	}
	env.SetApp(&AppInfo{Exe: "Paint.EXE", Path: `C:\Paint.EXE`, PID: 12, Elevated: true})
	if _, ok := h.NextDeadline(); ok {
		t.Fatal("duplicate foreground created work")
	}
	env.SetApp(nil)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.VM.RunString(`seen.length === 2 && seen[1] === null`); !v.ToBoolean() {
		t.Fatal("null notification")
	}
	h.Suspend()
	env.SetApp(&AppInfo{Exe: "late.exe"})
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.VM.RunString(`seen.length`); v.ToInteger() != 2 {
		t.Fatal("suspended handler ran")
	}
	if len(env.listeners) != 0 {
		t.Fatal("suspended host retained wake callback")
	}
}

func TestEnvironmentProfilesAreAsynchronousAndCandidateSafe(t *testing.T) {
	env := new(Environment)
	ids := []string{"paint", "other"}
	env.SetProfiles(ids)
	ids[0] = "mutated"
	var requests []string
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Profile: "paint", SwitchProfile: func(id string) error { requests = append(requests, id); return nil }})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := h.Activate(environmentProgram(t, `const p = __padmate.profiles;
try { p.switchTo("other"); throw new Error("unexpected setup switch"); } catch(e) { if (!String(e).includes("active handler")) throw e; }
__padmate.on("south", "press", () => p.switchTo("other"));`)); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatal("candidate made request")
	}
	v, err := h.VM.RunString(`const a=p.list(); a[0]="bad"; p.current === "paint" && p.list().join(",") === "other,paint"`)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("profiles snapshot: %v %v", v, err)
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []string{"other"}) {
		t.Fatalf("requests = %v", requests)
	}
	for _, arg := range []string{`"../escape"`, `""`, `null`, `12`, `"con"`} {
		if _, err := h.VM.RunString(`p.switchTo(` + arg + `)`); err == nil {
			t.Fatalf("accepted %s", arg)
		}
	}
	h.switchProfile = func(string) error { return errors.New("queue full") }
	if _, err := h.VM.RunString(`p.switchTo("other")`); err == nil || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("queue error = %v", err)
	}
	h.Suspend()
	if _, err := h.VM.RunString(`p.switchTo("other")`); err == nil {
		t.Fatal("suspended host accepted switch")
	}
}

func TestEnvironmentListenerErrorIsDiagnosed(t *testing.T) {
	env := new(Environment)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := h.Activate(environmentProgram(t, `__padmate.app.onChange(() => { throw new Error("foreground handler"); });`)); err != nil {
		t.Fatal(err)
	}
	env.SetApp(&AppInfo{Exe: "paint.exe"})
	if err := h.RunDue(); err == nil || !strings.Contains(err.Error(), "foreground handler") {
		t.Fatalf("handler diagnostic = %v", err)
	}
}
