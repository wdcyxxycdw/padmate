package injecttest

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProbeEnv set to 1 makes the test binary a fake padmate-probe, for
// the tests of Probe: they inject nothing and install no hook.
const fakeProbeEnv = "PADMATE_INJECTTEST_FAKE_PROBE"

func TestMain(m *testing.M) {
	if os.Getenv(fakeProbeEnv) == "1" {
		os.Exit(fakeProbe(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeProbe acts as "padmate-probe record --json --swallow padmate" with
// one more argument, what to do: "stay" prints events and waits to be
// killed, "exit" exits once ready, and "noready" fails before it.
func fakeProbe(args []string) int {
	if len(args) != 5 || !slices.Equal(args[:4], []string{"record", "--json", "--swallow", "padmate"}) {
		fmt.Fprintf(os.Stderr, "fake probe: arguments %q\n", args)
		return 2
	}
	switch args[4] {
	case "noready":
		fmt.Fprintln(os.Stderr, "no hook")
		return 1
	case "exit":
		fmt.Fprintln(os.Stderr, "ready")
		return 0
	}
	fmt.Fprintln(os.Stderr, "ready")
	fmt.Fprintln(os.Stderr, "a warning")
	for _, line := range []string{
		`{"seq":1,"msg":"WM_KEYDOWN","vk":162,"scan":29,"injected":true,"extra":"0x7061646D00000007","padmate":true,"nonce":7,"swallowed":true}`,
		`{"seq":2,"msg":"WM_KEYDOWN","vk":65,"scan":30}`,
		`{"seq":3,"msg":"WM_KEYUP","vk":151,"up":true,"injected":true,"extra":"0x7061646D00000008","padmate":true,"nonce":8,"swallowed":true}`,
		`{"seq":5,"kind":"mouse","msg":"WM_MOUSEWHEEL","delta":-120,"mouseData":"0xFF880000","injected":true,"extra":"0xB","padmate":false,"nonce":11,"swallowed":true}`,
		`{"seq":6,"kind":"mouse","msg":"WM_MOUSEWHEEL","delta":120,"mouseData":"0x780000","extra":"0xB","padmate":false,"nonce":0,"swallowed":false}`,
		`not an event`,
		`{"seq":4,"msg":"WM_KEYUP","vk":162,"scan":29,"up":true,"injected":true,"extra":"0x7061646D00000007","padmate":true,"nonce":7,"swallowed":true}`,
	} {
		fmt.Println(line)
	}
	time.Sleep(time.Minute) // until the test kills it
	return 0
}

// fakeTB records the failures and the cleanups of the code under test.
type fakeTB struct {
	testing.TB
	mu       sync.Mutex
	failures []string
	cleanups []func()
}

func (*fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatal(args ...any) {
	f.Errorf("%s", fmt.Sprint(args...))
	runtime.Goexit()
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
	runtime.Goexit()
}

func (f *fakeTB) Cleanup(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, fn)
}

// run runs fn on a goroutine of its own, which Fatal ends, and then the
// cleanups, last first. It returns the failures.
func (f *fakeTB) run(fn func()) []string {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
	f.mu.Lock()
	cleanups := f.cleanups
	f.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.failures)
}

// TestProbe runs StartProbe with a fake probe: it collects the events by
// nonce, the other lines as Stderr, and its cleanup kills the probe,
// failing the test only when the probe exited before that.
func TestProbe(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeProbeEnv, "1")

	t.Run("events", func(t *testing.T) {
		f := &fakeTB{TB: t}
		var p *Probe
		failures := f.run(func() {
			p = StartProbe(f, exe, "stay")
			got := p.Wait(7, 2, 5*time.Second)
			if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 4 {
				t.Errorf("Wait(7, 2) = %v, want events 1 and 4", got)
			} else if s, want := got[1].String(), "up sc=0x1D ext=false vk=0xA2 WM_KEYUP injected=true swallowed=true nonce=7"; s != want {
				t.Errorf("event 4 is %q, want %q", s, want)
			}
			if got := p.WithNonce(8); len(got) != 1 || got[0].VK != CanaryVK || got[0].Extra != "0x7061646D00000008" {
				t.Errorf("WithNonce(8) = %+v, want event 3", got)
			}
			if got := p.WithNonce(11); len(got) != 1 || got[0].Kind != "mouse" || got[0].Delta != -120 || got[0].MouseData != "0xFF880000" {
				t.Errorf("WithNonce(11) = %+v, want the mouse event", got)
			} else if s, want := got[0].String(), "WM_MOUSEWHEEL delta=-120 injected=true swallowed=true nonce=11"; s != want {
				t.Errorf("mouse event is %q, want %q", s, want)
			}
			if got := p.Wait(9, 1, 20*time.Millisecond); len(got) != 0 {
				t.Errorf("Wait(9, 1) = %v, want nothing", got)
			}
		})
		if len(failures) > 0 {
			t.Errorf("failures %q, want none", failures)
		}
		// Killed, the probe has ended both its outputs.
		stderr := p.Stderr()
		slices.Sort(stderr)
		if len(stderr) != 2 || stderr[0] != "a warning" || !strings.HasPrefix(stderr[1], `bad stdout line "not an event": `) {
			t.Errorf("Stderr() = %q, want the warning and the bad line", stderr)
		}
	})

	t.Run("exit", func(t *testing.T) {
		f := &fakeTB{TB: t}
		failures := f.run(func() {
			p := StartProbe(f, exe, "exit")
			select {
			case <-p.exited:
			case <-time.After(5 * time.Second):
				t.Error("the fake probe did not exit")
			}
		})
		if len(failures) != 1 || !strings.HasPrefix(failures[0], "the probe exited before the test stopped it: <nil>;") {
			t.Errorf("failures %q, want the early exit", failures)
		}
	})

	t.Run("noready", func(t *testing.T) {
		f := &fakeTB{TB: t}
		failures := f.run(func() {
			StartProbe(f, exe, "noready")
			t.Error("StartProbe returned")
		})
		// The cleanup may find it exited, or kill it as it exits.
		if len(failures) == 0 || failures[0] != `the probe exited before its hook was ready: ["no hook"]` {
			t.Errorf("failures %q, want the exit before ready first", failures)
		}
	})
}
