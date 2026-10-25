package output_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The child of TestHeldLogSurvivesKill runs a Loop with the held-key log
// named by heldChildEnv and the script named by heldScriptEnv.
const (
	heldChildEnv  = "PADMATE_HELD_CHILD"
	heldScriptEnv = "PADMATE_HELD_SCRIPT"
	// heldChildWait bounds each wait for the child, which Defender may
	// start slowly, and how long the child lives.
	heldChildWait = 30 * time.Second
)

// TestHeldLogSurvivesKill is the M1d acceptance "kill the process while
// Ctrl is held, and the next start releases Ctrl exactly once", with a
// fake Sender. A child process runs a Loop with a held-key log and a
// Sender that writes each batch as one line to stdout; the test kills it,
// rebuilds the keys it left down from the complete lines, and checks that
// the log has each of them, and that a new Loop on the log releases each
// once and nothing else. Round 0 is the audit's case: LCtrl down, LShift
// 20 ms later, killed 100 ms after LShift's line; the other rounds keep
// LCtrl down and press, release and tap other keys at random until they
// are killed 5-30 ms after LCtrl's line.
func TestHeldLogSurvivesKill(t *testing.T) {
	if path := os.Getenv(heldChildEnv); path != "" {
		heldChild(path, os.Getenv(heldScriptEnv))
		return
	}
	if testing.Short() {
		t.Skip("starts child processes")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewPCG(seed, 0))
	for round := range 4 {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			script := "fixed"
			if round > 0 {
				script = "random:" + strconv.FormatUint(rng.Uint64(), 10)
			}
			path := heldPath(t)
			held := killChild(t, exe, path, script, func(lines <-chan string) time.Duration {
				if round == 0 {
					waitLine(t, lines, "+"+press(t, "LShift").ID().String())
					return 100 * ms
				}
				waitLine(t, lines, "+"+press(t, "LCtrl").ID().String())
				return time.Duration(5+rng.IntN(26)) * ms
			})
			t.Logf("%s: the child left %q down", script, held)
			if !slices.Contains(held, press(t, "LCtrl").ID().String()) {
				t.Fatalf("the child left %q down, want LCtrl among them", held)
			}
			logged, ok := readHeld(t, path)
			if !ok {
				t.Fatal("no held-key log after the kill")
			}
			for _, k := range held {
				if !slices.Contains(logged, k) {
					t.Errorf("the child left %s down, and its log has only %q", k, logged)
				}
			}
			sweepKilled(t, path, held)
		})
	}
}

// killChild runs the child on path with script, calls wait with the lines
// the child writes from its ready line on, kills the child when the time
// wait returns has passed, and returns the keys that the child's complete
// lines left down, by KeyID, without the menu mask key.
func killChild(t *testing.T, exe, path, script string, wait func(<-chan string) time.Duration) []string {
	t.Helper()
	cmd := exec.Command(exe, "-test.run=^TestHeldLogSurvivesKill$", "-test.count=1")
	cmd.Env = append(os.Environ(), heldChildEnv+"="+path, heldScriptEnv+"="+script)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1024)
	var all []string
	var mu sync.Mutex
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer close(lines)
		r := bufio.NewReader(out)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return // a line the kill cut short is left out
			}
			line = strings.TrimSuffix(line, "\n")
			mu.Lock()
			all = append(all, line)
			mu.Unlock()
			select {
			case lines <- line:
			default:
			}
		}
	}()
	killed := false
	defer func() {
		if !killed {
			_ = cmd.Process.Kill()
			<-read
			_ = cmd.Wait()
		}
	}()
	waitLine(t, lines, "ready")
	d := wait(lines)
	time.Sleep(d)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed = true
	<-read // the pipe closes with the process
	_ = cmd.Wait()

	mu.Lock()
	defer mu.Unlock()
	mask := output.MenuMaskEvent(false).ID().String()
	var held []string
	for _, line := range all {
		batch, ok := strings.CutPrefix(line, "send ")
		if !ok {
			continue
		}
		for _, e := range strings.Fields(batch) {
			k := e[1:]
			switch {
			case k == mask:
			case e[0] == '+' && !slices.Contains(held, k):
				held = append(held, k)
			case e[0] == '-':
				held = slices.DeleteFunc(held, func(h string) bool { return h == k })
			}
		}
	}
	slices.Sort(held)
	return held
}

// waitLine waits for the child's line want.
func waitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	limit := time.After(heldChildWait)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the child ended before the line %q", want)
			}
			if line == want || strings.HasPrefix(line, "send ") && slices.Contains(strings.Fields(line[5:]), want) {
				return
			}
		case <-limit:
			t.Fatalf("no line %q from the child within %v", want, heldChildWait)
		}
	}
}

// sweepKilled starts a Loop on the log the child left, with the keys held
// reported down, and checks that its sweep releases each once and sends
// nothing else but the MenuMask's own events, and that Stop removes the
// log.
func sweepKilled(t *testing.T, path string, held []string) {
	t.Helper()
	fake := clock.NewFake()
	rec := simtest.NewSyncRecorder(fake)
	down := func(e output.KeyEvent) bool { return slices.Contains(held, e.ID().String()) }
	l := output.StartLoop(fake, rec, output.LoopOptions{HeldFile: path, KeyDown: down})
	res := l.Swept()
	if res.Err != nil || res.Off {
		t.Errorf("Swept() = %+v, want no error", res)
	}
	var released []string
	for _, e := range res.Released {
		released = append(released, e.ID().String())
	}
	slices.Sort(released)
	if !slices.Equal(released, held) {
		t.Errorf("Swept().Released = %q, want %q", released, held)
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
	rec.With(func(r *simtest.Recorder) {
		var ups []string
		for _, rec := range r.Records() {
			if output.IsMenuMask(rec.Event) {
				continue // the MenuMask's own, before a lone Alt's release
			}
			if !rec.Event.Up || !rec.Injected {
				t.Errorf("the sweep sent %v, want releases only", rec.Event)
			}
			ups = append(ups, rec.Event.ID().String())
		}
		slices.Sort(ups)
		if !slices.Equal(ups, held) {
			t.Errorf("the sweep released %q, want %q, each once", ups, held)
		}
	})
	wantNoHeld(t, path)
}

// pipeSender writes each batch to stdout as one line, "send +sc:0x1D
// -sc:0x2C", with one write, and injects all of it.
type pipeSender struct{ buf []byte }

func (p *pipeSender) Send(events []output.KeyEvent) (int, error) {
	p.buf = append(p.buf[:0], "send"...)
	for _, e := range events {
		sign := byte('+')
		if e.Up {
			sign = '-'
		}
		p.buf = append(p.buf, ' ', sign)
		p.buf = append(p.buf, e.ID().String()...)
	}
	p.buf = append(p.buf, '\n')
	if _, err := os.Stdout.Write(p.buf); err != nil {
		return 0, err
	}
	return len(events), nil
}

// heldChild is the child process of TestHeldLogSurvivesKill. It never
// returns: the test kills it, or it exits after heldChildWait.
func heldChild(path, script string) {
	time.AfterFunc(heldChildWait, func() { os.Exit(3) })
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "held child:", err)
		os.Exit(2)
	}
	l := output.StartLoop(clock.Real(), &pipeSender{}, output.LoopOptions{
		Lock: true, HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false },
	})
	if res := l.Swept(); res.Err != nil || res.Off {
		fail(fmt.Errorf("sweep: %+v", res))
	}
	if _, err := io.WriteString(os.Stdout, "ready\n"); err != nil {
		fail(err)
	}
	ownerCtrl := output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1}
	ownerRest := output.Owner{Slot: 1, Gen: 1, Source: output.SourceDecl, Cycle: 1}
	do := func(f func(*output.Arbiter) error) {
		if err := l.Do(f); err != nil {
			fail(err)
		}
	}
	do(func(a *output.Arbiter) error { return a.Down(ownerCtrl, keys.MustParse("LCtrl")) })
	seedText, random := strings.CutPrefix(script, "random:")
	if !random {
		time.Sleep(20 * ms)
		do(func(a *output.Arbiter) error { return a.Down(ownerRest, keys.MustParse("LShift")) })
		select {}
	}
	seed, err := strconv.ParseUint(seedText, 10, 64)
	if err != nil {
		fail(err)
	}
	rng := rand.New(rand.NewPCG(seed, 1))
	chords := []keys.Chord{keys.MustParse("LShift"), keys.MustParse("LAlt"), keys.MustParse("Z"), keys.MustParse("Pause"), keys.MustParse("Num1")}
	for {
		time.Sleep(time.Duration(rng.IntN(4)) * ms)
		c := chords[rng.IntN(len(chords))]
		var f func(*output.Arbiter) error
		switch rng.IntN(4) {
		case 0:
			f = func(a *output.Arbiter) error { return a.Down(ownerRest, c) }
		case 1:
			f = func(a *output.Arbiter) error { return a.Up(ownerRest, c) }
		case 2:
			hold := time.Duration(rng.IntN(3)) * ms
			f = func(a *output.Arbiter) error { return a.Tap(ownerRest, c, hold) }
		default:
			f = func(a *output.Arbiter) error {
				return a.ReleaseOwners(func(o output.Owner) bool { return o == ownerRest })
			}
		}
		if err := l.Do(f); err != nil && !errors.Is(err, output.ErrLoopClosed) {
			fail(err)
		}
	}
}
