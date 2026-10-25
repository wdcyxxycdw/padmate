package tray

import (
	"fmt"
	"testing"
)

func TestNotificationOncePerRunSurvivesHistoryEviction(t *testing.T) {
	q := NewNotifications(2)
	if !q.Add(Notification{Key: "steam", OncePerRun: true}) {
		t.Fatal("notice rejected")
	}
	q.Take()
	for i := 0; i < 300; i++ {
		q.Add(Notification{Key: fmt.Sprint(i)})
		q.Take()
	}
	if q.Add(Notification{Key: "steam", OncePerRun: true}) {
		t.Fatal("one-time warning repeated after history eviction")
	}
	if len(q.seen) > 272 {
		t.Fatal("unbounded history")
	}
}

func TestNotificationQueueBoundedAndDeduplicated(t *testing.T) {
	q := NewNotifications(2)
	if !q.Add(Notification{Key: "steam", Title: "Steam"}) || q.Add(Notification{Key: "steam"}) {
		t.Fatal("duplicate notice queued")
	}
	if !q.Add(Notification{Key: "elevated"}) {
		t.Fatal("second notice rejected")
	}
	if q.Add(Notification{Key: "missing"}) {
		t.Fatal("full queue accepted notice")
	}
	n, ok := q.Take()
	if !ok || n.Key != "steam" {
		t.Fatalf("take = %+v, %v", n, ok)
	}
	if q.Add(Notification{Key: "steam"}) {
		t.Fatal("previously displayed notice repeated")
	}
	if !q.Add(Notification{Key: "missing"}) {
		t.Fatal("dropped notice was marked seen")
	}
	q.Take()
	q.Take()
	if _, ok := q.Take(); ok {
		t.Fatal("empty queue returned notice")
	}
}

func TestNotificationSanitizesAndBoundsNativeStrings(t *testing.T) {
	n := Notification{Title: "hi\x00\nthere", Body: "正文"}
	title, body := n.Text()
	if title != "hi  there" || body != "正文" {
		t.Fatalf("text = %q %q", title, body)
	}
}
