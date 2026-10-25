package script

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

type tapSender struct {
	mu     sync.Mutex
	events []output.KeyEvent
	calls  int
	fail   map[int]int
}

func (s *tapSender) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	n := len(events)
	var err error
	if count, ok := s.fail[s.calls]; ok {
		n = count
		err = errors.New("planned partial send")
	}
	s.calls++
	s.events = append(s.events, events[:n]...)
	s.mu.Unlock()
	return n, err
}

func TestTapArrayWaitsForFailedReleaseRetry(t *testing.T) {
	clk := clock.NewFake()
	rec := &tapSender{fail: map[int]int{0: 1, 1: 0}}
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
		if got := rec.trace(); strings.Count(got, "+") != strings.Count(got, "-") {
			t.Fatalf("keys remain held: %s", got)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.tap(["Z","X"])`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	if p.State() != goja.PromiseStatePending {
		t.Fatalf("promise before release retry = %v", p.State())
	}
	if err := h.RunDue(); err == nil {
		t.Fatal("partial Send error was not reported")
	}
	rec.want(t, "[+Z]")
	clk.Advance(output.DefaultRetryInterval)
	if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z +X -X]")
	if p.State() != goja.PromiseStateFulfilled || !p.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatalf("array after partial Send = %v %v", p.State(), p.Result())
	}
}

func (s *tapSender) trace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var edges []string
	for _, e := range s.events {
		if e.Key.IsValid() {
			prefix := "+"
			if e.Up {
				prefix = "-"
			}
			edges = append(edges, prefix+e.Key.String())
		}
	}
	return "[" + strings.Join(edges, " ") + "]"
}

func (s *tapSender) want(t *testing.T, want string) {
	t.Helper()
	if got := s.trace(); got != want {
		t.Fatalf("output = %s, want %s", got, want)
	}
}

func TestTapPromiseWaitsForOutputLoopAndArrayRunsInOrder(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
		if got := rec.trace(); strings.Count(got, "+") != strings.Count(got, "-") {
			t.Fatalf("keys remain held: %s", got)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	_, err := h.VM.RunString(`globalThis.done = false; globalThis.result = null;
		__padmate.key.tap(["Z", "X"], {holdMs: 20}).then(r => { done = true; result = r; });`)
	if err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z]")
	if h.VM.Get("done").ToBoolean() {
		t.Fatal("tap resolved before the first release")
	}
	clk.Advance(20 * time.Millisecond)
	if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z +X]")
	if h.VM.Get("done").ToBoolean() {
		t.Fatal("array resolved before its last release")
	}
	clk.Advance(20 * time.Millisecond)
	if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z +X -X]")
	if !h.VM.Get("done").ToBoolean() {
		t.Fatal("tap did not resolve after the last release")
	}
	if h.VM.Get("result").ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatal("completed tap reported dropped")
	}
}

func TestTapCancellationAndZeroHoldPendingOnOutputLoop(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
		if got := rec.trace(); strings.Count(got, "+") != strings.Count(got, "-") {
			t.Fatalf("keys remain held: %s", got)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.tap("Z", {holdMs: 100})`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	if p.State() != goja.PromiseStatePending {
		t.Fatalf("state before release = %v", p.State())
	}
	h.ReleaseKeys()
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending {
		t.Fatalf("safety release settled old promise: %v", p.State())
	}
	// hold=0 still owns its main-key reservation until the output loop's
	// completion has returned to the Host. A second tight tap is dropped.
	v, err = h.VM.RunString(`__padmate.key.tap("X")`)
	if err != nil {
		t.Fatal(err)
	}
	first := v.Export().(*goja.Promise)
	v, err = h.VM.RunString(`__padmate.key.tap("X")`)
	if err != nil {
		t.Fatal(err)
	}
	second := v.Export().(*goja.Promise)
	if first.State() != goja.PromiseStatePending {
		t.Fatalf("zero-hold first state = %v", first.State())
	}
	if !second.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatal("second zero-hold tap was not dropped")
	}
	if err := h.RunDue(); !errors.Is(err, ErrOutputBudget) {
		t.Fatalf("duplicate tap diagnostic = %v", err)
	}
	if first.State() != goja.PromiseStateFulfilled {
		t.Fatalf("zero-hold completed state = %v", first.State())
	}
	if trace := rec.trace(); !strings.Contains(trace, "+X -X") {
		t.Fatalf("trace = %s", trace)
	}
}

func TestTapPromiseCanceledByOutputLoopBlock(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.tap("Z", {holdMs: 100})`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	loop.Block(true)
	if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStateFulfilled || !p.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatalf("blocked tap = %v %v", p.State(), p.Result())
	}
}

func TestTapArrayStopsAfterExternalOwnerRelease(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`__padmate.key.tap(["Z","X"], {holdMs:100})`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	if err := loop.Remote().ReleaseOwner(h.base.owner); err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStateFulfilled || !p.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatalf("owner-released array = %v %v", p.State(), p.Result())
	}
}

func TestTapDisposeDiscardsContinuationAndReleasesOwner(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`globalThis.after = false;
		globalThis.p = __padmate.key.tap("Z", {holdMs:100}).then(() => {
			after = true; __padmate.key.down("X");
		}); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	if err := h.Dispose(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending || h.VM.Get("after").ToBoolean() {
		t.Fatalf("disposed continuation ran: promise=%v after=%v", p.State(), h.VM.Get("after"))
	}
}

func TestTapDisposeAbandonsPromiseBeforeTeardownReleaseAll(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.EnableWatchdog()
	h.active = true
	v, err := h.VM.RunString(`globalThis.after=false;
		globalThis.p=__padmate.key.tap("Z",{holdMs:100}).then(() => {
			after=true; __padmate.key.down("X"); while(true){}
		}); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	if _, err := h.VM.RunString(`globalThis.teardown=() => __padmate.key.releaseAll()`); err != nil {
		t.Fatal(err)
	}
	h.teardown, _ = goja.AssertFunction(h.VM.Get("teardown"))
	rec.want(t, "[+Z]")
	start := time.Now()
	if err := h.Dispose(); err != nil {
		t.Fatalf("teardown ran stale continuation: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dispose took %s", elapsed)
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending || h.VM.Get("after").ToBoolean() {
		t.Fatalf("disposed continuation ran: promise=%v after=%v", p.State(), h.VM.Get("after"))
	}
}

func TestTapLifecycleDisposeAbandonsPromiseBeforeTeardownReleaseAll(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.EnableWatchdog()
	h.active = true
	v, err := h.VM.RunString(`globalThis.after=false;
		globalThis.p=__padmate.key.tap("Z",{holdMs:100}).then(() => {
			after=true; __padmate.key.down("X"); while(true){}
		}); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	if _, err := h.VM.RunString(`globalThis.teardown=() => __padmate.key.releaseAll()`); err != nil {
		t.Fatal(err)
	}
	h.teardown, _ = goja.AssertFunction(h.VM.Get("teardown"))
	l := NewLifecycle(clk)
	l.current, l.state = h, StateRunning
	rec.want(t, "[+Z]")
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z]")
	deadline := time.Now().Add(time.Second)
	for l.TeardownsPending() && time.Now().Before(deadline) {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	if l.TeardownsPending() {
		t.Fatal("teardown worker did not finish")
	}
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending || h.VM.Get("after").ToBoolean() {
		t.Fatalf("retired continuation ran: promise=%v after=%v", p.State(), h.VM.Get("after"))
	}
}

func TestTapSafetyReleaseDiscardsContinuation(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`globalThis.after=false;
		globalThis.p=__padmate.key.tap("Z",{holdMs:100}).then(() => {
			after=true; __padmate.key.down("X");
		}); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	h.ReleaseKeys()
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending || h.VM.Get("after").ToBoolean() {
		t.Fatalf("safety release ran old continuation: promise=%v after=%v", p.State(), h.VM.Get("after"))
	}
}

func TestTapHostReleaseAllDiscardsContinuation(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`globalThis.after=false;
		globalThis.p=__padmate.key.tap("Z",{holdMs:100}).then(() => {
			after=true; __padmate.key.down("X");
		}); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z]")
	h.ReleaseAll()
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStatePending || h.VM.Get("after").ToBoolean() {
		t.Fatalf("Host.ReleaseAll ran old continuation: promise=%v after=%v", p.State(), h.VM.Get("after"))
	}
}

func TestScriptKeyReleaseAllSettlesTapInsideHandler(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	v, err := h.VM.RunString(`globalThis.p=__padmate.key.tap("Z",{holdMs:100});
		__padmate.key.releaseAll(); p`)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Export().(*goja.Promise)
	rec.want(t, "[+Z -Z]")
	if p.State() != goja.PromiseStateFulfilled || !p.Result().ToObject(h.VM).Get("dropped").ToBoolean() {
		t.Fatalf("script releaseAll result = %v %v", p.State(), p.Result())
	}
}

func TestScriptKeyReleaseAllCanStartFreshTapFromContinuation(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	_, err := h.VM.RunString(`globalThis.next=null;
		__padmate.key.tap("Z",{holdMs:100}).then(r => {
			if (r.dropped) next=__padmate.key.tap("X");
		}); __padmate.key.releaseAll();`)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	rec.want(t, "[+Z -Z +X -X]")
	v := h.VM.Get("next")
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		t.Fatal("releaseAll continuation did not start X")
	}
	if p := v.Export().(*goja.Promise); p.State() != goja.PromiseStateFulfilled {
		t.Fatalf("fresh tap state = %v", p.State())
	}
}
