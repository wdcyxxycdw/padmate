package input_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestResumeVirtualReenumeratesCompleteState(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "resume virtual")
	if err := v.SetStateWithoutEvents(map[string]bool{"east": true}, map[string]int16{"leftx": 23456, "lefttrigger": 28000}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Reenumerate(ctx); err != nil {
		t.Fatal(err)
	}
	var snap *input.GamepadState
	for snap == nil {
		select {
		case e := <-s.Events():
			if e.Device == v.ID() && e.Kind == input.DeviceState {
				snap = e.Snapshot
			}
		case <-ctx.Done():
			t.Fatal("no complete resume snapshot")
		}
	}
	if !slices.Contains(snap.Buttons, "east") || snap.Axes["leftx"] != 23456 || snap.Axes["lefttrigger"] < 27999 {
		t.Fatalf("wrong snapshot: %+v", snap)
	}
	if err := v.SetStateWithoutEvents(map[string]bool{"east": false, "south": true}, map[string]int16{"leftx": 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reenumerate(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case e := <-s.Events():
			if e.Device != v.ID() || e.Kind != input.DeviceState {
				continue
			}
			if slices.Contains(e.Snapshot.Buttons, "east") || !slices.Contains(e.Snapshot.Buttons, "south") || e.Snapshot.Axes["leftx"] != 0 {
				t.Fatalf("stale state: %+v", e.Snapshot)
			}
			return
		case <-ctx.Done():
			t.Fatal("no second snapshot")
		}
	}
}

func TestResumeVirtualPublicationBackpressureDoesNotBlockInput(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "resume pressure")
	if err := s.FillEventBuffer(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Reenumerate(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("scan returned before critical publication: %v", err)
	}
	command := make(chan error, 1)
	go func() { command <- v.SetButton("east", true) }()
	select {
	case err := <-command:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication pressure blocked T-input commands")
	}
}

func TestResumeCanceledAndClosedSource(t *testing.T) {
	s := open(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Reenumerate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	s.Close()
	if err := s.Reenumerate(context.Background()); !errors.Is(err, input.ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}
