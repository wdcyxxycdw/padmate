package input

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// This file keeps T-input from blocking on its log (Options.Log,
// ARCHITECTURE §5). A log may block: a write to a console waits while
// text is selected in it (QuickEdit), and a file may be slow. T-input
// pumps SDL and finds the combos, so it hands each record to a queue
// instead, and a goroutine of the queue writes them.

const (
	// logQueueLen is the capacity of T-input's log queue. T-input logs at
	// the watchdog's transitions, and at debug level once a second per
	// watched device, so it holds seconds of records.
	logQueueLen = 256
	// logFlushWait bounds how long T-input waits at its end for the queue
	// to be written, so that a log that blocks does not hold up Close.
	logFlushWait = time.Second
)

// logQueue queues log records for its goroutine, which writes them in
// order. A record that does not fit is dropped and counted; the goroutine
// logs the count once it has written what waits.
type logQueue struct {
	ch      chan logItem
	dropped atomic.Uint64
	done    chan struct{} // closed when the goroutine has ended
	// base is the handler the count of dropped records goes to.
	base slog.Handler
}

// logItem is a queued record, with the handler that writes it.
type logItem struct {
	h slog.Handler
	r slog.Record
}

// newLogQueue starts the goroutine of a queue of n records whose records
// go to h, and returns the queue.
func newLogQueue(h slog.Handler, n int) *logQueue {
	q := &logQueue{ch: make(chan logItem, n), done: make(chan struct{}), base: h}
	go q.run()
	return q
}

// run writes the queued records until the queue is closed and empty. A
// handler panic stops this worker: logging must not end the process and
// bypass the input and output loops' resource and held-key cleanup.
func (q *logQueue) run() {
	defer close(q.done)
	defer func() { _ = recover() }()
	for it := range q.ch {
		_ = it.h.Handle(context.Background(), it.r)
		if len(q.ch) == 0 {
			q.report()
		}
	}
	q.report()
}

// report logs how many records were dropped since the last report, if
// any: they came after the records written so far.
func (q *logQueue) report() {
	n := q.dropped.Swap(0)
	if n == 0 || !q.base.Enabled(context.Background(), slog.LevelWarn) {
		return
	}
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "input log records dropped", 0)
	r.AddAttrs(slog.Uint64("dropped", n))
	_ = q.base.Handle(context.Background(), r)
}

// close closes the queue, and waits until its goroutine has written what
// waits, or for wait at most. Nothing may be logged to it after.
func (q *logQueue) close(wait time.Duration) {
	close(q.ch)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-q.done:
	case <-t.C:
	}
}

// logger returns a logger whose records go through q to its handler.
func (q *logQueue) logger() *slog.Logger { return slog.New(queuedHandler{q: q, h: q.base}) }

// queuedHandler is the slog.Handler of logQueue.logger: it hands each
// record to the queue without blocking, and the queue's goroutine writes
// it with h.
type queuedHandler struct {
	q *logQueue
	h slog.Handler
}

func (h queuedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.h.Enabled(ctx, level)
}

// Handle queues r, or drops and counts it when the queue is full. The
// record is cloned: its caller may reuse it.
func (h queuedHandler) Handle(_ context.Context, r slog.Record) error {
	select {
	case h.q.ch <- logItem{h: h.h, r: r.Clone()}:
	default:
		h.q.dropped.Add(1)
	}
	return nil
}

func (h queuedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return queuedHandler{q: h.q, h: h.h.WithAttrs(attrs)}
}

func (h queuedHandler) WithGroup(name string) slog.Handler {
	return queuedHandler{q: h.q, h: h.h.WithGroup(name)}
}
