package script

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	filewatch "github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

func TestReloaderDebouncesAndSuccessfulSwapReleasesOldOwner(t *testing.T) {
	c := clock.NewFake()
	recorder := new(scriptOutputRecorder)
	l := NewLifecycle(c, HostOptions{Output: recorder})
	build := programWithAction(t, "Z")
	first := <-l.Load(context.Background(), build)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	r := NewReloader(l, ReloadOptions{Clock: c, Debounce: 50 * time.Millisecond, Build: func(ctx context.Context) (*goja.Program, error) {
		close(started)
		return programWithAction(t, "X")(ctx)
	}})
	r.Request(context.Background())
	r.Request(context.Background())
	c.Advance(49 * time.Millisecond)
	r.RunDue()
	if r.Loading() {
		t.Fatal("reload started before debounce elapsed")
	}
	c.Advance(time.Millisecond)
	r.RunDue()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reload did not start after debounce")
	}
	awaitReload(t, r, c)
	if l.Current() == first.Host || l.State() != StateRunning {
		t.Fatalf("current/state = %v/%v, want swapped running runtime", l.Current(), l.State())
	}
	got := recorder.calls
	if len(got) != 4 || got[0].kind != "down" || got[1].kind != "down" || got[2].kind != "up" || got[3].kind != "release" {
		t.Fatalf("calls = %+v, want old release after new activation", got)
	}
	if got[0].owner.Cycle == got[1].owner.Cycle || got[2].owner.Cycle != got[0].owner.Cycle {
		t.Fatalf("owner cycles = %d, %d, %d; want distinct new and old release", got[0].owner.Cycle, got[1].owner.Cycle, got[2].owner.Cycle)
	}
}

// OnInstall tells the owner that the reloader installed a runtime (SCR-23: the
// owner ends a failure episode with it) and only then. A candidate that does
// not build, or whose onActivate fails, installs nothing; the automatic
// restart after an interruption installs a runtime through Swap as well, but
// it is not the reloader's.
func TestReloaderOnInstallRunsOnlyForAnInstall(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	var installs int
	var current []*Host // l.Current() seen by each call
	var reported []error
	build := programWithAction(t, "X")
	r := NewReloader(l, ReloadOptions{
		Clock: c, Debounce: time.Millisecond,
		Build:   func(ctx context.Context) (*goja.Program, error) { return build(ctx) },
		OnError: func(err error) { reported = append(reported, err) },
		OnInstall: func() {
			installs++
			current = append(current, l.Current())
		},
	})
	reload := func() {
		t.Helper()
		r.Request(context.Background())
		c.Advance(time.Millisecond)
		r.RunDue()
		awaitReload(t, r, c)
	}

	build = func(context.Context) (*goja.Program, error) { return nil, errors.New("broken script") }
	reload()
	if installs != 0 || len(reported) != 1 {
		t.Fatalf("a build failure: %d installs, errors %v", installs, reported)
	}
	build = func(context.Context) (*goja.Program, error) {
		return goja.Compile("reload.js", `globalThis.__padmate_bundle = { default: { onActivate() { throw new Error("no"); } } };`, false)
	}
	reload()
	if installs != 0 || len(reported) != 2 || l.Current() != first.Host {
		t.Fatalf("an onActivate failure: %d installs, errors %v", installs, reported)
	}

	build = programWithAction(t, "X")
	reload()
	if installs != 1 || len(reported) != 2 || l.Current() == first.Host || current[0] != l.Current() {
		t.Fatalf("a good candidate: %d installs, errors %v, installed %v", installs, reported, current)
	}

	// The restart after an interruption is the lifecycle's own, not a reload.
	l.RecordInterruption(c.Now())
	c.Advance(time.Minute)
	deadline := time.Now().Add(time.Second)
	for l.Current() == nil && time.Now().Before(deadline) {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if l.Current() == nil || l.State() != StateRunning {
		t.Fatalf("the lifecycle did not restart: %v", l.State())
	}
	r.RunDue()
	if installs != 1 {
		t.Fatalf("a restart after an interruption counted as a reload: %d installs", installs)
	}
}

// A saved script that installs while the lifecycle waits out the backoff of its
// automatic restart is the recovery: the restart timer must not install a second
// runtime over it later (the onActivate of both would run, and the restart
// would count).
func TestReloaderInstallCancelsThePendingRestart(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	l.RecordInterruption(c.Now())
	if l.Current() != nil {
		t.Fatal("the interrupted runtime is still current")
	}
	build := programWithAction(t, "X")
	r := NewReloader(l, ReloadOptions{
		Clock: c, Debounce: time.Millisecond,
		Build: func(ctx context.Context) (*goja.Program, error) { return build(ctx) },
	})
	r.Request(context.Background())
	c.Advance(time.Millisecond)
	r.RunDue()
	awaitReload(t, r, c)
	saved := l.Current()
	if saved == nil || saved == first.Host || l.State() != StateRunning {
		t.Fatalf("the saved script was not installed: %v, %v", saved, l.State())
	}

	// The backoff elapses; the lifecycle's own restart has nothing to do.
	c.Advance(time.Minute)
	for range 50 {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if count, _ := l.RestartStatus(); l.Current() != saved || l.State() != StateRunning || count != 0 {
		t.Fatalf("the restart replaced the saved runtime: same %v, %v, %d restarts", l.Current() == saved, l.State(), count)
	}
}

func TestReloaderBuildFailureKeepsOldRuntime(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	want := errors.New("broken script")
	var reported error
	r := NewReloader(l, ReloadOptions{Clock: c, Debounce: time.Millisecond, Build: func(context.Context) (*goja.Program, error) { return nil, want }, OnError: func(err error) { reported = err }})
	r.Request(context.Background())
	c.Advance(time.Millisecond)
	r.RunDue()
	awaitReload(t, r, c)
	if l.Current() != first.Host || l.State() != StateRunning {
		t.Fatalf("failed reload changed current/state = %v/%v", l.Current(), l.State())
	}
	if !errors.Is(reported, want) {
		t.Fatalf("reported = %v, want %v", reported, want)
	}
}

func TestReloaderCancelDropsPendingDebounceAndWorker(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	r := NewReloader(l, ReloadOptions{Clock: c, Debounce: 10 * time.Millisecond, Build: func(context.Context) (*goja.Program, error) {
		close(started)
		<-finish
		return goja.Compile("reload.js", "", false)
	}})
	r.Request(context.Background())
	r.Cancel()
	c.Advance(time.Second)
	r.RunDue()
	select {
	case <-started:
		t.Fatal("canceled debounce started a build")
	default:
	}
	if l.Current() != first.Host {
		t.Fatal("cancel changed active runtime")
	}
}

func TestReloaderCancelInFlightKeepsActiveOwner(t *testing.T) {
	c := clock.NewFake()
	sink := &releaseSignalSink{released: make(chan struct{}, 1)}
	l := NewLifecycle(c, HostOptions{Output: sink})
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	discarded := make(chan struct{}, 1)
	l.beforeSetup = func(h *Host) {
		_ = h.VM.Set("signalDiscard", func() { discarded <- struct{}{} })
	}
	r := NewReloader(l, ReloadOptions{Clock: c, Debounce: time.Millisecond, Build: func(context.Context) (*goja.Program, error) {
		close(started)
		<-finish
		return goja.Compile("reload.js", `globalThis.__padmate_bundle = { default: { teardown() { signalDiscard(); } } };`, false)
	}})
	r.Request(context.Background())
	c.Advance(time.Millisecond)
	r.RunDue()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reload worker did not start")
	}
	r.Cancel()
	close(finish)
	select {
	case <-discarded:
	case <-time.After(time.Second):
		t.Fatal("canceled candidate was not disposed")
	}
	select {
	case <-sink.released:
		t.Fatal("canceled candidate touched the live output sink")
	default:
	}
	if l.Current() != first.Host {
		t.Fatal("canceled candidate replaced active runtime")
	}
}

func TestDiscardCandidateDoesNotCallArbiter(t *testing.T) {
	sink := &releaseSignalSink{released: make(chan struct{}, 1)}
	l := NewLifecycle(clock.NewFake(), HostOptions{Output: sink})
	result := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("discard.js", `globalThis.__padmate_bundle = {default: {teardown() {__padmate.key.down("Y"); __padmate.key.releaseAll()}}};`, false)
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if err := result.Host.Discard(); err != nil {
		t.Fatal(err)
	}
	if result.Host.State() != StateDisposed {
		t.Fatalf("candidate state = %v, want disposed", result.Host.State())
	}
	select {
	case <-sink.released:
		t.Fatal("discarded candidate called the arbiter")
	default:
	}
}

func TestReloaderWorkerHasOwnerWakeDeadline(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	r := NewReloader(l, ReloadOptions{Clock: c, Debounce: time.Nanosecond, Build: func(context.Context) (*goja.Program, error) {
		close(started)
		<-finish
		return goja.Compile("reload.js", "", false)
	}})
	r.Request(context.Background())
	c.Advance(time.Nanosecond)
	r.RunDue()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reload worker did not start")
	}
	if d, ok := r.NextDeadline(); !ok || d != c.Now()+clock.Time(workerPollInterval) {
		t.Fatalf("NextDeadline while worker runs = %v, %v; want one worker poll interval", d, ok)
	}
	firstDeadline, _ := r.NextDeadline()
	c.Advance(workerPollInterval)
	if next, ok := r.NextDeadline(); !ok || next != firstDeadline {
		t.Fatalf("worker poll deadline moved without RunDue: %v, %v; want %v", next, ok, firstDeadline)
	}
	close(finish)
	r.Cancel()
}

func TestLifecycleRestartWorkerHasOwnerWakeDeadline(t *testing.T) {
	c := clock.NewFake()
	started := make(chan struct{})
	finish := make(chan struct{})
	l := NewLifecycle(c)
	first := <-l.Load(context.Background(), programWithAction(t, "Z"))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	l.build = programsOf(func(context.Context) (*goja.Program, error) {
		close(started)
		<-finish
		return goja.Compile("restart.js", "", false)
	})
	l.RecordInterruption(c.Now())
	c.Advance(time.Second)
	if err := l.RunDue(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("restart worker did not start")
	}
	if d, ok := l.NextDeadline(); !ok || d != c.Now()+clock.Time(workerPollInterval) {
		t.Fatalf("NextDeadline while restart worker runs = %v, %v; want one worker poll interval", d, ok)
	}
	firstDeadline, _ := l.NextDeadline()
	c.Advance(workerPollInterval)
	if next, ok := l.NextDeadline(); !ok || next != firstDeadline {
		t.Fatalf("restart poll deadline moved without RunDue: %v, %v; want %v", next, ok, firstDeadline)
	}
	close(finish)
	_ = l.Dispose()
}

func TestScriptWatcherUsesFileWatchDebounceSeam(t *testing.T) {
	c := clock.NewFake()
	path := t.TempDir() + "/main.ts"
	if err := os.WriteFile(path, []byte("initial"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := NewLifecycle(c)
	w, err := NewScriptWatcher(l, ScriptWatchOptions{
		Clock: c, Path: path, Build: programWithAction(t, "X"),
		Watch: filewatch.Options{Interval: time.Millisecond, Debounce: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Cancel()
}

func programWithAction(t *testing.T, spec string) BuildFunc {
	t.Helper()
	return func(context.Context) (*goja.Program, error) {
		return goja.Compile("reload.js", `globalThis.__padmate_bundle = { default: { onActivate() { globalThis.__padmate.key.down("`+spec+`"); } } };`, false)
	}
}

func awaitReload(t *testing.T, r *Reloader, c *clock.Fake) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for r.Loading() && time.Now().Before(deadline) {
		r.RunDue()
		time.Sleep(time.Millisecond)
	}
	if r.Loading() {
		t.Fatal("reload worker did not finish")
	}
}

// Keep these imports and the recorder's chord shape checked by this file so
// future output changes cannot silently bypass owner release assertions.
var _ = keys.Chord{}
var _ output.Owner

type releaseSignalSink struct{ released chan struct{} }

func (s *releaseSignalSink) Down(output.Owner, keys.Chord) error { return nil }
func (s *releaseSignalSink) Up(output.Owner, keys.Chord) error   { return nil }
func (s *releaseSignalSink) Tap(output.Owner, keys.Chord, time.Duration) error {
	return nil
}
func (s *releaseSignalSink) ReleaseOwner(output.Owner) error {
	select {
	case s.released <- struct{}{}:
	default:
	}
	return nil
}
