package supervisor

import (
	"context"
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// RefreshFeedback has every slot publish its layer and feedback state again
// (slot.Slot.RefreshFeedback): padmated calls it after a committed config.json
// changed the global feedback switches (UI-27, ADR-0067), so the lights and
// the player lights follow them at once. Each slot does it behind the edges
// delivered so far. It returns once every slot has done it, with ctx.Err() or
// ErrStopped like Sync. It must not be called from a slot's goroutine.
func (s *Supervisor) RefreshFeedback(ctx context.Context) error {
	bs, err := s.syncRequest(ctx, syncRequest{action: func(sl *slot.Slot) error {
		sl.RefreshFeedback()
		return nil
	}})
	if err != nil {
		return err
	}
	var errs []error
	for _, b := range bs {
		errs = append(errs, b.err)
	}
	return errors.Join(errs...)
}
