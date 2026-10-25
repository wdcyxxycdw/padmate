package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestLogHistoryBoundsAttributesAndDetachedRead(t *testing.T) {
	var file bytes.Buffer
	next := slog.New(slog.NewJSONHandler(&file, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log, history := ObserveLogs(next, NewHub())
	log.Debug("hidden")
	log.With("source", "script").WithGroup("detail").Info("message", "answer", 42)
	for range 300 {
		log.Info("newer")
	}
	rows, oldest := history.Snapshot(0)
	if len(rows) != 256 || oldest != 46 || rows[0].Sequence != oldest || rows[255].Sequence != 301 {
		t.Fatalf("history rows=%d oldest=%d", len(rows), oldest)
	}
	rows[0].Record[0] = 'x'
	if again, _ := history.Snapshot(0); !json.Valid(again[0].Record) {
		t.Fatal("snapshot changed retained log")
	}
	if after, _ := history.Snapshot(300); len(after) != 1 {
		t.Fatal(after)
	}
	_, history = ObserveLogs(next, NewHub())
	if _, err := history.Write([]byte(strings.Repeat("x", maxLogRecordBytes+1))); err != nil {
		t.Fatal(err)
	}
	rows, _ = history.Snapshot(0)
	if len(rows) != 1 || !rows[0].Truncated || !json.Valid(rows[0].Record) {
		t.Fatal("oversize log was not bounded")
	}
	if !strings.Contains(file.String(), `"detail":{"answer":42}`) || strings.Contains(file.String(), "hidden") {
		t.Fatal("observer changed file log filtering or attrs")
	}
	if log.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("observer enabled suppressed levels")
	}
}

func TestLogHistoryConcurrentPublishOrder(t *testing.T) {
	hub := NewHub()
	s := newSession(hub, 128)
	defer s.close()
	s.setTopics([]string{"log"})
	history := &LogHistory{hub: hub}
	var writers sync.WaitGroup
	for range 64 {
		writers.Go(func() { _, _ = history.Write([]byte(`{"msg":"parallel"}`)) })
	}
	writers.Wait()
	for want := uint64(1); want <= 64; want++ {
		var row LogRecord
		if err := json.Unmarshal((<-s.events).data, &row); err != nil || row.Sequence != want {
			t.Fatalf("sequence = %d, want %d: %v", row.Sequence, want, err)
		}
	}
}
