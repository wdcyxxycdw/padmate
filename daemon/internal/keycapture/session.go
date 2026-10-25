package keycapture

import (
	"context"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// Observe stops on cancellation (stdin EOF, IPC loss, parent death or timeout),
// foreground departure, overflow, or native hook closure. Foreground is checked
// for every event before decoding it, so another application's keys never leave
// the native helper even if the UI's blur message arrives late.
func Observe(ctx context.Context, events <-chan winhook.Event, poll <-chan time.Time, allowed func() bool, emit func(Event) error) error {
	var state State
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll:
			if !allowed() {
				return nil
			}
		case raw, ok := <-events:
			if !ok || !allowed() {
				return nil
			}
			if event, ok := state.Step(raw); ok {
				if err := emit(event); err != nil {
					return err
				}
			}
		}
	}
}
