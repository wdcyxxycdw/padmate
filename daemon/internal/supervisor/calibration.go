package supervisor

import (
	"context"
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// RefreshCalibration has every slot ask Config.Calibration again for the
// calibration of its controller and apply it (IN-14): padmated calls it after
// a committed config.json changed. Each slot does it behind the edges
// delivered so far (slot.Slot.RefreshCalibration), so a stick held across the
// change gives up what the new values no longer recognize, through the normal
// release, and presses nothing; no key of a button held is let go. A slot that
// is disabled or restarting asks when it gets its controller. It returns once
// every slot has applied it, with ctx.Err() or ErrStopped like Sync, and does
// nothing without Config.Calibration. It must not be called from a slot's
// goroutine.
func (s *Supervisor) RefreshCalibration(ctx context.Context) error {
	if s.cfg.Calibration == nil {
		return nil
	}
	bs, err := s.syncRequest(ctx, syncRequest{action: func(sl *slot.Slot) error {
		sl.RefreshCalibration()
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
