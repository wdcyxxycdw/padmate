package simtest

import (
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// WaitLimit bounds the real-time wait of SyncRecorder.WaitFor.
const WaitLimit = 5 * time.Second

// SyncRecorder is a Recorder that a test can read while another goroutine,
// such as an output.Loop, sends to it. Its methods are safe for
// concurrent use.
type SyncRecorder struct {
	mu   sync.Mutex
	r    *Recorder
	sent chan struct{} // signaled after every Send
}

// NewSyncRecorder returns a SyncRecorder whose Recorder stamps events with
// clk's Now.
func NewSyncRecorder(clk clock.Clock) *SyncRecorder {
	return &SyncRecorder{r: NewRecorder(clk), sent: make(chan struct{}, 1)}
}

// Send is Recorder.Send under the lock.
func (s *SyncRecorder) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	n, err := s.r.Send(events)
	s.mu.Unlock()
	select {
	case s.sent <- struct{}{}:
	default:
	}
	return n, err
}

// With runs f with the Recorder locked. f must not keep it.
func (s *SyncRecorder) With(f func(*Recorder)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.r)
}

// Trace returns the injected events as a trace.
func (s *SyncRecorder) Trace() string {
	var tr string
	s.With(func(r *Recorder) { tr = FormatTrace(r.Edges()) })
	return tr
}

// WaitFor waits until the injected events are the trace want, and fails
// the test after WaitLimit of real time. Unlike the rest of the package it
// waits in real time: for another goroutine, not for a clock.
func (s *SyncRecorder) WaitFor(t testing.TB, want string) {
	t.Helper()
	limit := time.After(WaitLimit)
	for s.Trace() != want {
		select {
		case <-s.sent:
		case <-limit:
			t.Fatalf("output:\n got %s\nwant %s", s.Trace(), want)
		}
	}
}
