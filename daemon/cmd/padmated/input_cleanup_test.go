package main

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

type cleanupSource struct {
	*fakeSource
	feedback func(input.Feedback) error
	closing  func()
	askExit  func()
	request  chan struct{}
	done     chan struct{}
	reqOnce  sync.Once
	doneOnce sync.Once
}

func newCleanupSource() *cleanupSource {
	return &cleanupSource{fakeSource: newFakeSource(), request: make(chan struct{}), done: make(chan struct{})}
}
func (s *cleanupSource) RequestClose() {
	s.reqOnce.Do(func() { close(s.request) })
	if s.askExit != nil {
		s.askExit()
	}
}
func (s *cleanupSource) SetFeedback(f input.Feedback) error {
	if s.feedback != nil {
		return s.feedback(f)
	}
	return s.fakeSource.SetFeedback(f)
}
func (s *cleanupSource) Close() {
	defer s.doneOnce.Do(func() { close(s.done) })
	s.RequestClose()
	if s.closing != nil {
		s.closing()
	}
	s.fakeSource.Close()
}

type cleanupSender struct {
	mu     sync.Mutex
	sent   []string
	before func([]output.KeyEvent) error
}

func (s *cleanupSender) Send(evs []output.KeyEvent) (int, error) {
	if s.before != nil {
		if err := s.before(evs); err != nil {
			return 0, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		s.sent = append(s.sent, edgeName(e))
	}
	return len(evs), nil
}
func (s *cleanupSender) edges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

func cleanupLive(t *testing.T, src *cleanupSource, sender *cleanupSender) *testLive {
	t.Helper()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", func() (output.Sender, error) { return sender, nil }, "--inject", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	tl.addPad(t, src.fakeSource)
	button(src.fakeSource, "east", true)
	tl.waitUntil(t, "Space down on the fake sender", func() bool { return slices.Contains(sender.edges(), "+Space") })
	return tl
}

// This outside guard lets the red regression report the former shutdown panic
// or Goexit without ending the test process. Production must contain it itself.
func cleanupWaitCode(tl *testLive) <-chan int {
	result := make(chan int, 1)
	go func() {
		returned := false
		defer func() {
			_ = recover()
			if !returned {
				result <- -1
			}
		}()
		code := tl.wait()
		returned = true
		result <- code
	}()
	return result
}

func checkCleanupResult(t *testing.T, tl *testLive, sender *cleanupSender, result <-chan int, want int) {
	t.Helper()
	select {
	case code := <-result:
		if code != want {
			t.Fatalf("exit %d, want %d\nstderr:\n%s", code, want, tl.stderr.String())
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("post-release input cleanup exceeded its total wait limit")
	}
	if got := sender.edges(); !slices.Equal(got, []string{"+Space", "-Space"}) {
		t.Fatalf("fake physical edges: %v; Space must be released before input cleanup", got)
	}
	if line := logLine(tl.log.String(), "padmated stopped"); !strings.Contains(line, map[int]string{0: `"code":0`, 1: `"code":1`}[want]) {
		t.Fatalf("missing final stopped record with code %d: %s", want, tl.log.String())
	}
}

func TestLiveInputCleanupBoundedAfterKeysReleased(t *testing.T) {
	for _, stage := range []string{"source", "feedback"} {
		t.Run(stage, func(t *testing.T) {
			src, sender := newCleanupSource(), &cleanupSender{}
			entered, allow := make(chan struct{}), make(chan struct{})
			if stage == "source" {
				src.closing = func() { close(entered); <-allow }
			} else {
				src.feedback = func(input.Feedback) error { close(entered); <-allow; return errors.New("late feedback") }
			}
			tl := cleanupLive(t, src, sender)
			t.Cleanup(func() { close(allow); <-src.done })
			if stage == "feedback" {
				tl.effects.post(input.Feedback{Device: 1})
				<-entered
			}
			tl.cancel()
			result := cleanupWaitCode(tl)
			if stage == "source" {
				<-entered
			}
			checkCleanupResult(t, tl, sender, result, 1)
			select {
			case <-src.request:
			default:
				t.Fatal("input exit was not requested while feedback was blocked")
			}
			if !strings.Contains(tl.stderr.String(), "input cleanup did not stop") || logLine(tl.log.String(), "input cleanup did not stop") == "" {
				t.Fatalf("missing timeout diagnostics: %s\n%s", tl.stderr.String(), tl.log.String())
			}
			// Even a late driver completion and input edge cannot revive output.
			button(src.fakeSource, "south", true)
			if err := tl.loop.Do(func(*output.Arbiter) error { return nil }); !errors.Is(err, output.ErrLoopClosed) {
				t.Fatalf("late completion can still reach output: %v", err)
			}
		})
	}
}

func TestLiveInputCleanupPreservesFeedbackThenSourceOrder(t *testing.T) {
	src, sender := newCleanupSource(), &cleanupSender{}
	entered, allow := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var feedbackOnce sync.Once
	var order []string
	src.feedback = func(input.Feedback) error {
		feedbackOnce.Do(func() {
			close(entered)
			<-allow
			mu.Lock()
			order = append(order, "feedback")
			mu.Unlock()
		})
		return nil
	}
	src.closing = func() {
		mu.Lock()
		order = append(order, "source")
		mu.Unlock()
	}
	tl := cleanupLive(t, src, sender)
	tl.effects.post(input.Feedback{Device: 1})
	<-entered
	tl.cancel()
	result := cleanupWaitCode(tl)
	close(allow)
	checkCleanupResult(t, tl, sender, result, 0)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(order, []string{"feedback", "source"}) {
		t.Fatalf("cleanup order: %v", order)
	}
}

func TestLiveInputCleanupFaultsReturnFailure(t *testing.T) {
	for _, stage := range []string{"source", "feedback", "request"} {
		for _, fault := range []string{"panic", "goexit"} {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				src, sender := newCleanupSource(), &cleanupSender{}
				entered := make(chan struct{})
				var enterOnce sync.Once
				fail := func() {
					enterOnce.Do(func() { close(entered) })
					if fault == "panic" {
						panic("cleanup driver fault")
					}
					runtime.Goexit()
				}
				switch stage {
				case "source":
					src.closing = fail
				case "request":
					src.askExit = fail
				default:
					src.feedback = func(input.Feedback) error { fail(); return nil }
				}
				tl := cleanupLive(t, src, sender)
				if stage == "feedback" {
					tl.effects.post(input.Feedback{Device: 1})
					<-entered
					<-tl.effects.done
				}
				tl.cancel()
				checkCleanupResult(t, tl, sender, cleanupWaitCode(tl), 1)
				if logLine(tl.log.String(), "input cleanup failed") == "" {
					t.Fatalf("missing cleanup failure diagnostic: %s", tl.log.String())
				}
			})
		}
	}
}

func TestLiveInputCleanupWaitsForOutputRelease(t *testing.T) {
	for _, stage := range []string{"Run", "LoopStopRetries"} {
		t.Run(stage, func(t *testing.T) {
			src, sender := newCleanupSource(), &cleanupSender{}
			entered, allow := make(chan struct{}), make(chan struct{})
			ready := make(chan struct{})
			var ran <-chan struct{}
			var enterOnce sync.Once
			sender.before = func(evs []output.KeyEvent) error {
				for _, e := range evs {
					if edgeName(e) == "-Space" {
						<-ready
						if stage == "LoopStopRetries" {
							// Retry counts depend on timer scheduling. Keep failing
							// until Run has actually handed off to output shutdown.
							select {
							case <-ran:
							default:
								return errors.New("retry release")
							}
						}
						enterOnce.Do(func() {
							close(entered)
							<-allow
						})
					}
				}
				return nil
			}
			tl := cleanupLive(t, src, sender)
			ran = tl.ran
			close(ready)
			var once sync.Once
			unblock := func() { once.Do(func() { close(allow) }) }
			t.Cleanup(unblock)
			tl.cancel()
			result := cleanupWaitCode(tl)
			select {
			case <-entered:
			case <-time.After(liveWait):
				t.Fatal("key release did not reach the selected shutdown stage")
			}
			if stage == "LoopStopRetries" {
				select {
				case <-tl.ran:
				default:
					t.Fatal("release retry did not reach the post-Run output stop")
				}
			} else {
				select {
				case <-tl.ran:
					t.Fatal("Run finished before its held key was released")
				default:
				}
			}
			select {
			case code := <-result:
				t.Fatalf("key release was bypassed: exit %d", code)
			case <-src.request:
				t.Fatal("input cleanup started before output release finished")
			case <-time.After(1100 * time.Millisecond):
			}
			unblock()
			checkCleanupResult(t, tl, sender, result, 0)
		})
	}
}
