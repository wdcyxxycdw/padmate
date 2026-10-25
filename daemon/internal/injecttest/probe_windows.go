package injecttest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// probeReady bounds the wait for the probe's hook, and probeStop the wait
// for the probe to exit once killed.
const (
	probeReady = 15 * time.Second
	probeStop  = 10 * time.Second
)

// ProbeEvent is one line of "padmate-probe record --json": a keyboard event,
// or with --mouse in the probe's arguments a mouse event, which has Kind
// "mouse" and the fields Delta, MouseData and XButton instead of VK, Scan,
// Ext, Up and AltDown.
type ProbeEvent struct {
	Seq       uint64 `json:"seq"`
	Kind      string `json:"kind"` // "mouse" for a mouse event, else ""
	Time      uint32 `json:"time"`
	Msg       string `json:"msg"`
	VK        uint32 `json:"vk"`
	Scan      uint32 `json:"scan"`
	Ext       bool   `json:"ext"`
	Up        bool   `json:"up"`
	Injected  bool   `json:"injected"`
	LowerIL   bool   `json:"lowerIL"`
	AltDown   bool   `json:"altDown"`
	Extra     string `json:"extra"` // dwExtraInfo, as 0x%X
	Padmate   bool   `json:"padmate"`
	Nonce     uint32 `json:"nonce"`
	Swallowed bool   `json:"swallowed"`
	Delta     int    `json:"delta"`     // a wheel event's delta, +-120 for a notch
	MouseData string `json:"mouseData"` // a mouse event's mouseData, as 0x%X
	XButton   int    `json:"xButton"`   // the number of an extra button
}

func (e ProbeEvent) String() string {
	if e.Kind == "mouse" {
		return fmt.Sprintf("%s delta=%d injected=%v swallowed=%v nonce=%d", e.Msg, e.Delta, e.Injected, e.Swallowed, e.Nonce)
	}
	dir := "down"
	if e.Up {
		dir = "up"
	}
	return fmt.Sprintf("%s sc=0x%02X ext=%v vk=0x%02X %s injected=%v swallowed=%v nonce=%d",
		dir, e.Scan, e.Ext, e.VK, e.Msg, e.Injected, e.Swallowed, e.Nonce)
}

// Probe is a running "padmate-probe record --json --swallow padmate": a
// hook in a process of its own that swallows every padmate-tagged event.
// Its output is collected as it arrives.
//
// A test that injects through a Probe starts a Backstop first.
type Probe struct {
	mu     sync.Mutex
	events []ProbeEvent
	errs   []string // stderr lines after "ready", and bad stdout lines
	wake   chan struct{}
	ready  chan struct{} // closed when the probe prints "ready"
	outEOF chan struct{} // closed when stdout ends
	errEOF chan struct{} // closed when stderr ends
	exited chan struct{} // closed once the probe has exited and waitErr is set
	// waitErr is the result of Wait on the probe; read it after exited.
	waitErr error
}

// StartProbe runs "exe record --json --swallow padmate" with args, where
// exe is padmate-probe (BuildTool builds it), and waits until the probe
// reports its hook installed. Start a Backstop before it; before the
// first real key the test must fence the probe with Canary.
//
// The probe is killed when t ends; t fails if it exited before that. Give
// it a --duration, which bounds it when the test process dies first.
func StartProbe(t testing.TB, exe string, args ...string) *Probe {
	t.Helper()
	cmd := exec.Command(exe, append([]string{"record", "--json", "--swallow", "padmate"}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the probe: %v", err)
	}
	p := &Probe{
		wake: make(chan struct{}, 1), ready: make(chan struct{}),
		outEOF: make(chan struct{}), errEOF: make(chan struct{}), exited: make(chan struct{}),
	}
	go p.readStdout(stdout)
	go p.readStderr(stderr)
	go func() {
		// Wait closes the pipes: only once both are read to the end.
		<-p.outEOF
		<-p.errEOF
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() { p.stop(t, cmd) })
	select {
	case <-p.ready:
	case <-p.errEOF:
		// Closed after ready, if the probe printed it and exited at once:
		// the cleanup reports that exit.
		select {
		case <-p.ready:
		default:
			t.Fatalf("the probe exited before its hook was ready: %q", p.Stderr())
		}
	case <-time.After(probeReady):
		t.Fatalf("the probe did not print ready within %v", probeReady)
	}
	return p
}

// stop kills the probe, which removes its hook, and waits for its exit. It
// fails t if the probe had exited already.
func (p *Probe) stop(t testing.TB, cmd *exec.Cmd) {
	t.Helper()
	select {
	case <-p.exited:
		t.Errorf("the probe exited before the test stopped it: %v; stderr %q", p.waitErr, p.Stderr())
		return
	default:
	}
	// Kill fails when the probe has just exited by itself.
	killed := cmd.Process.Kill() == nil
	select {
	case <-p.exited:
	case <-time.After(probeStop):
		t.Errorf("the probe did not exit within %v of being killed", probeStop)
		return
	}
	if !killed {
		t.Errorf("the probe exited before the test stopped it: %v; stderr %q", p.waitErr, p.Stderr())
	}
}

func (p *Probe) readStdout(r io.Reader) {
	defer close(p.outEOF)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var e ProbeEvent
		err := json.Unmarshal(sc.Bytes(), &e)
		p.mu.Lock()
		if err != nil {
			p.errs = append(p.errs, fmt.Sprintf("bad stdout line %q: %v", sc.Text(), err))
		} else {
			p.events = append(p.events, e)
		}
		p.mu.Unlock()
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

func (p *Probe) readStderr(r io.Reader) {
	defer close(p.errEOF)
	sc := bufio.NewScanner(r)
	ready := false
	for sc.Scan() {
		if !ready && sc.Text() == "ready" {
			ready = true
			close(p.ready)
			continue
		}
		p.mu.Lock()
		p.errs = append(p.errs, sc.Text())
		p.mu.Unlock()
	}
}

// Stderr returns what the probe printed to stderr after "ready", and the
// lines of its stdout that were not events. A probe that works prints
// nothing of either.
func (p *Probe) Stderr() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.errs...)
}

// WithNonce returns the padmate events with nonce that the probe has
// reported so far, the mouse events too when it records the mouse: a mouse hook
// sees no tag (Windows drops it), so a mouse event is the injected one with
// the nonce.
func (p *Probe) WithNonce(nonce uint32) []ProbeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	var got []ProbeEvent
	for _, e := range p.events {
		if (e.Padmate || e.Kind == "mouse" && e.Injected) && e.Nonce == nonce {
			got = append(got, e)
		}
	}
	return got
}

// Wait returns the events with nonce once n have arrived, or what arrived
// within timeout, or before the probe's output ended. Call it from one
// goroutine at a time.
func (p *Probe) Wait(nonce uint32, n int, timeout time.Duration) []ProbeEvent {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if got := p.WithNonce(nonce); len(got) >= n {
			return got
		}
		select {
		case <-p.wake:
		case <-p.outEOF:
			return p.WithNonce(nonce)
		case <-deadline.C:
			return p.WithNonce(nonce)
		}
	}
}

// TryCanary injects the canary with nonce itself, unlike
// Recorder.WaitFence, and waits up to CanaryTimeout for the probe to
// report it swallowed. It returns what the probe reported with nonce, and
// whether that is the canary, swallowed: the hook sees events in the order
// they were injected, so the probe has then reported every event injected
// before the canary. Use a nonce of its own for each canary.
func (p *Probe) TryCanary(t testing.TB, nonce uint32) ([]ProbeEvent, bool) {
	t.Helper()
	SendCanary(t, nonce)
	got := p.Wait(nonce, 1, CanaryTimeout)
	return got, len(got) == 1 && got[0].VK == CanaryVK && got[0].Up && got[0].Swallowed
}

// Canary is TryCanary that stops the test unless the probe swallowed the
// canary. Call it before injecting any real key.
func (p *Probe) Canary(t testing.TB, nonce uint32) {
	t.Helper()
	if got, ok := p.TryCanary(t, nonce); !ok {
		t.Fatalf("the probe did not swallow the canary with nonce %#x within %v (got %v, stderr %q); injecting nothing else",
			nonce, CanaryTimeout, got, p.Stderr())
	}
}

// Backstop is a Recorder that owns every nonce, for what a Probe lets
// through. Installed before the probe, its hook runs after the probe's,
// so it sees only what the probe let through, and swallows that too. It
// must see nothing but its own canaries.
type Backstop struct {
	rec *Recorder

	mu       sync.Mutex
	canaries []uint32 // the nonces of its canaries
}

// StartBackstop installs a Backstop. Call it before StartProbe, and prove
// it live with Canary while it is the test's only hook.
//
// When t ends, after the cleanups registered later, StartProbe's among
// them, the backstop is closed and t fails for each event it saw other
// than its canaries. Closed, it has recorded every event its hook saw; and
// an event that the probe let through reached it before the probe's hook
// returned, and so before the probe reported the next canary.
func StartBackstop(t testing.TB) *Backstop {
	t.Helper()
	b := &Backstop{rec: StartFunc(t, func(uint32) bool { return true })}
	t.Cleanup(func() { b.check(t) })
	return b
}

// Canary injects the canary with nonce and stops the test unless the
// backstop swallowed it within CanaryTimeout, as Recorder.Canary does. A
// probe that works swallows it first: call it before StartProbe, or once
// the probe has not swallowed a canary. Use a nonce of its own for each
// canary.
func (b *Backstop) Canary(t testing.TB, nonce uint32) {
	t.Helper()
	b.mu.Lock()
	b.canaries = append(b.canaries, nonce)
	b.mu.Unlock()
	b.rec.Canary(t, nonce)
}

// check closes the backstop and fails t for each event it saw other than
// its canaries.
func (b *Backstop) check(t testing.TB) {
	t.Helper()
	b.rec.Close(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.rec.Own() {
		if !slices.ContainsFunc(b.canaries, func(n uint32) bool { return IsCanary(e, n) }) {
			t.Errorf("an event got past the probe's hook: vk=0x%02X sc=0x%02X flags=0x%02X nonce=%d",
				e.VK, e.Scan, e.Flags, output.Nonce(e.Extra))
		}
	}
}
