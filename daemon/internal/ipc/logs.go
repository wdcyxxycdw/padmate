package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
)

const maxLogRecordBytes = 16 << 10
const logHistorySize = 256

type LogRecord struct {
	Sequence  uint64          `json:"sequence"`
	Record    json.RawMessage `json:"record"`
	Truncated bool            `json:"truncated,omitempty"`
}

// LogHistory retains bounded immutable JSON records for reconnecting clients.
// It never waits for a pipe writer and never recursively logs an error.
type LogHistory struct {
	mu   sync.Mutex
	rows [logHistorySize]LogRecord
	next uint64
	hub  *Hub
}

func (h *LogHistory) Write(data []byte) (int, error) {
	row := LogRecord{}
	if len(data) > maxLogRecordBytes || !json.Valid(data) {
		row.Truncated = true
		row.Record = json.RawMessage(`{"level":"WARN","msg":"log record exceeded IPC limits; see daemon log file"}`)
	} else {
		row.Record = bytes.Clone(bytes.TrimSpace(data))
	}
	h.mu.Lock()
	h.next++
	row.Sequence = h.next
	h.rows[(h.next-1)%logHistorySize] = row
	// Publish only enqueues bounded snapshots. Keep sequence assignment and
	// publication ordered even when different logger handlers write in parallel.
	h.hub.Publish("log", row)
	h.mu.Unlock()
	return len(data), nil
}

// Snapshot returns retained entries after a cursor and the oldest retained
// sequence, so a reader can recognize a gap. Returned bytes belong to caller.
func (h *LogHistory) Snapshot(after uint64) ([]LogRecord, uint64) {
	if h == nil {
		return []LogRecord{}, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	first := uint64(1)
	if h.next > logHistorySize {
		first = h.next - logHistorySize + 1
	}
	rows := make([]LogRecord, 0, logHistorySize)
	for n := first; n <= h.next; n++ {
		if n <= after {
			continue
		}
		row := h.rows[(n-1)%logHistorySize]
		row.Record = bytes.Clone(row.Record)
		rows = append(rows, row)
	}
	if h.next == 0 {
		first = 0
	}
	return rows, first
}

type logObserver struct {
	next, observer slog.Handler
}

// ObserveLogs keeps the configured file/stderr handler and its level filter.
// The second handler writes complete JSON records only to bounded memory.
func ObserveLogs(log *slog.Logger, hub *Hub) (*slog.Logger, *LogHistory) {
	history := &LogHistory{hub: hub}
	handler := &logObserver{next: log.Handler(), observer: slog.NewJSONHandler(history, nil)}
	return slog.New(handler), history
}

func (h *logObserver) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *logObserver) Handle(ctx context.Context, r slog.Record) error {
	observed := h.observer.Handle(ctx, r)
	return errors.Join(observed, h.next.Handle(ctx, r))
}

func (h *logObserver) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logObserver{next: h.next.WithAttrs(attrs), observer: h.observer.WithAttrs(attrs)}
}

func (h *logObserver) WithGroup(name string) slog.Handler {
	return &logObserver{next: h.next.WithGroup(name), observer: h.observer.WithGroup(name)}
}
