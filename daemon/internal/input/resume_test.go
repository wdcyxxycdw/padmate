package input

import (
	"context"
	"testing"
)

func TestResumePublicationWaitsForCriticalBacklog(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Device: 1, Kind: DeviceAdded})
	s.send(Event{Device: 1, Kind: DeviceState, Snapshot: &GamepadState{Buttons: []string{"east"}}})
	done := s.publication(context.Background())
	select {
	case <-done:
		t.Fatal("publication passed a queued state snapshot")
	default:
	}
	<-s.events
	s.flush()
	select {
	case <-done:
	default:
		t.Fatal("publication did not finish after the snapshot entered Events")
	}
	if e := <-s.events; e.Kind != DeviceState || len(e.Snapshot.Buttons) != 1 || s.Dropped() != 0 {
		t.Fatalf("critical snapshot lost: %+v, dropped=%d", e, s.Dropped())
	}
}

func TestResumePublicationCanceledMarkersArePruned(t *testing.T) {
	s := &Source{events: make(chan Event, 1)}
	s.send(Event{Kind: DeviceAdded, Device: 1})
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		s.send(Event{Kind: DeviceState, Device: 1, Snapshot: &GamepadState{}})
		s.publication(ctx)
		cancel()
	}
	if len(s.backlog) > 2 {
		t.Fatalf("canceled retries accumulated %d backlog entries", len(s.backlog))
	}
}
