package keycapture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

func TestObserveLifecycle(t *testing.T) {
	for _, reason := range []string{"eof", "timeout", "disconnect", "blur", "overflow", "closed-hook", "output-error"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make(chan winhook.Event, 1)
			poll := make(chan time.Time, 1)
			allowed := reason != "blur" && reason != "overflow"
			switch reason {
			case "eof", "timeout", "disconnect":
				cancel()
			case "blur", "overflow":
				poll <- time.Time{}
			case "closed-hook":
				close(events)
			case "output-error":
				events <- winhook.Event{VK: 'A', Scan: 0x1e}
			}
			calls := 0
			done := make(chan error, 1)
			go func() {
				done <- Observe(ctx, events, poll, func() bool { return allowed }, func(Event) error { calls++; return errors.New("closed writer") })
			}()
			select {
			case err := <-done:
				if (err != nil) != (reason == "output-error") {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("capture did not stop")
			}
			if calls != 0 && reason != "output-error" {
				t.Fatal("keys emitted after ending")
			}
		})
	}
}

func TestObserveChecksForegroundBeforeEveryKey(t *testing.T) {
	events := make(chan winhook.Event, 1)
	events <- winhook.Event{VK: 'A', Scan: 0x1e}
	if err := Observe(context.Background(), events, nil, func() bool { return false }, func(Event) error { t.Fatal("captured another application's key"); return nil }); err != nil {
		t.Fatal(err)
	}
}
