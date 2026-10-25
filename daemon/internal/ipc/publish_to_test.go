package ipc

import "testing"

// TestPublishToReachesTheAcceptedSubscribers: PublishTo gives an event only
// to the subscribers of the topic that accept says yes to, and does not
// encode it when none does. The raw joystick events (input.raw) use it to go
// to the connections that asked for the device.
func TestPublishToReachesTheAcceptedSubscribers(t *testing.T) {
	hub := NewHub()
	a, b, c := newSession(hub, 4), newSession(hub, 4), newSession(hub, 4)
	defer a.close()
	defer b.close()
	defer c.close()
	a.setTopics([]string{"input.raw"})
	b.setTopics([]string{"input.raw"})
	c.setTopics([]string{"input"}) // does not subscribe to the topic
	asked := 0
	hub.PublishTo("input.raw", map[string]int{"device": 1}, func(s *Session) bool {
		asked++
		return s == a || s == c
	})
	if len(a.events) != 1 || len(b.events) != 0 || len(c.events) != 0 {
		t.Errorf("queued %d, %d, %d; want 1, 0, 0", len(a.events), len(b.events), len(c.events))
	}
	if asked != 2 {
		t.Errorf("accept was asked %d times; only for the subscribers of the topic", asked)
	}
	// Nobody accepts: nothing is queued.
	hub.PublishTo("input.raw", map[string]int{"device": 2}, func(*Session) bool { return false })
	if len(a.events) != 1 || len(b.events) != 0 {
		t.Errorf("queued %d, %d after a refusal", len(a.events), len(b.events))
	}
	// A nil filter is Publish.
	hub.PublishTo("input.raw", 3, nil)
	if len(a.events) != 2 || len(b.events) != 1 {
		t.Errorf("queued %d, %d after an unfiltered publish; want 2, 1", len(a.events), len(b.events))
	}
}
