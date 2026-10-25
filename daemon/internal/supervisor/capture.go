package supervisor

import (
	"context"
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

type captureRequest struct {
	owner uint64
	on    bool
	reply chan struct{}
}

// Capture changes a client's capture lease. Capturing cancels mapping cycles
// while routing input continues. Releasing the last lease restores only the
// capture gate; user pause and system suspension retain their own gates.
// The client must release its nonzero owner on disconnect. Once accepted by
// the router, a request commits even if ctx is canceled while awaiting output.
func (s *Supervisor) Capture(ctx context.Context, owner uint64, on bool) error {
	if owner == 0 {
		return errors.New("supervisor: capture owner must be nonzero")
	}
	req := captureRequest{owner: owner, on: on, reply: make(chan struct{}, 1)}
	select {
	case s.captures <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStopped
	}
	select {
	case <-req.reply:
	case <-s.done:
		return ErrStopped
	}
	return s.cfg.Output.Do(func(*output.Arbiter) error { return nil })
}

func (r *router) setCapture(req captureRequest) {
	wasHeld := r.held()
	wasCapturing := len(r.captures) > 0
	if req.on {
		r.captures[req.owner] = struct{}{}
	} else {
		delete(r.captures, req.owner)
	}
	if on := len(r.captures) > 0; on != wasCapturing {
		r.s.cfg.Output.BlockFor(output.BlockCapture, on)
		r.storeHold()
		r.holdAll(wasHeld)
	}
	req.reply <- struct{}{}
}
