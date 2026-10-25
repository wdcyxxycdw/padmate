package input

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests need no SDL: they cover T-input's log queue.

// gateWriter is a log that blocks every write while its gate is shut, as
// a console does while text is selected in it. entered gets a value when
// a write starts.
type gateWriter struct {
	mu      sync.Mutex
	b       bytes.Buffer
	gate    chan struct{}
	entered chan struct{}
}

func newGateWriter() *gateWriter {
	return &gateWriter{gate: make(chan struct{}), entered: make(chan struct{}, 1024)}
}

func (w *gateWriter) Write(p []byte) (int, error) {
	w.entered <- struct{}{}
	<-w.gate
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// messages returns the msg and n of each record written.
func (w *gateWriter) messages(t *testing.T) []string {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for line := range strings.Lines(w.b.String()) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		switch {
		case r["n"] != nil:
			out = append(out, fmt.Sprintf("%v %v", r["msg"], r["n"]))
		case r["dropped"] != nil:
			out = append(out, fmt.Sprintf("%v %v %v", r["level"], r["msg"], r["dropped"]))
		default:
			out = append(out, fmt.Sprint(r["msg"]))
		}
	}
	return out
}

// TestLogQueueNeverBlocks: while the log blocks, logging returns at once;
// the records that do not fit in the queue are dropped. Once the log
// moves again the queued records are written in order, then the count of
// the dropped ones as a warn, and close waits for them. A logger of the
// queue keeps its attributes and groups; its levels are the log's.
func TestLogQueueNeverBlocks(t *testing.T) {
	w := newGateWriter()
	q := newLogQueue(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}), 4)
	log := q.logger()
	log.Info("record", "n", 0)
	<-w.entered // the goroutine holds record 0 and is blocked
	start := time.Now()
	for n := 1; n <= 10; n++ {
		log.Info("record", "n", n)
	}
	log.Debug("below the level")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("logging took %v while the log blocked", d)
	}
	if n := q.dropped.Load(); n != 6 {
		t.Errorf("dropped %d, want 6: record 0 is being written, 1 to 4 wait", n)
	}
	if !log.Enabled(t.Context(), slog.LevelInfo) || log.Enabled(t.Context(), slog.LevelDebug) {
		t.Error("the queue's logger does not take the log's level")
	}
	close(w.gate)
	q.close(5 * time.Second)
	want := []string{"record 0", "record 1", "record 2", "record 3", "record 4", "WARN input log records dropped 6"}
	if got := w.messages(t); !slices.Equal(got, want) {
		t.Errorf("written %q, want %q", got, want)
	}

	var b bytes.Buffer
	q = newLogQueue(slog.NewJSONHandler(&b, nil), 4)
	q.logger().With("id", 7).WithGroup("g").Info("grouped", "k", "v")
	q.close(5 * time.Second)
	if got := b.String(); !strings.Contains(got, `"msg":"grouped","id":7,"g":{"k":"v"}`) {
		t.Errorf("written %q, want the attribute and the group", got)
	}
}

// TestLogQueueCloseBounded: close gives up after its wait when the log
// blocks, so that a blocked log does not hold up Close.
func TestLogQueueCloseBounded(t *testing.T) {
	w := newGateWriter()
	defer close(w.gate)
	q := newLogQueue(slog.NewJSONHandler(w, nil), 4)
	q.logger().Info("record")
	<-w.entered
	start := time.Now()
	q.close(50 * time.Millisecond)
	if d := time.Since(start); d < 40*time.Millisecond || d > time.Second {
		t.Errorf("close returned after %v, want about 50ms", d)
	}
}

type panicLogHandler struct {
	slog.Handler
	where string
}

func (h panicLogHandler) Enabled(context.Context, slog.Level) bool {
	if h.where == "enabled" {
		panic("log level panic")
	}
	return true
}

func (h panicLogHandler) Handle(context.Context, slog.Record) error {
	if h.where == "handle" {
		panic("log write panic")
	}
	return nil
}

// A handler panic must only stop the log worker. It must not end the
// process before T-input and T-output can release their resources.
func TestLogQueueHandlerPanic(t *testing.T) {
	for _, name := range []string{"record", "dropped enabled", "dropped handle"} {
		t.Run(name, func(t *testing.T) {
			base := panicLogHandler{Handler: slog.DiscardHandler}
			record := base
			switch name {
			case "record":
				record.where = "handle"
			case "dropped enabled":
				base.where = "enabled"
			case "dropped handle":
				base.where = "handle"
			}
			q := newLogQueue(base, 1)
			if name != "record" {
				q.dropped.Store(1)
			}
			q.ch <- logItem{h: record, r: slog.NewRecord(time.Now(), slog.LevelInfo, "record", 0)}
			select {
			case <-q.done:
			case <-time.After(time.Second):
				t.Fatal("panicking log worker did not stop")
			}
			q.close(time.Second)
		})
	}
}
