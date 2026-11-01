package supervisor

import (
	"context"
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// RefreshFunctionKeys has every slot ask Config.FunctionKey again for the
// function key of its controller and apply it: padmated calls it after a
// committed config.json changed a controller's function key (ADR-0017). Each
// slot does it behind the edges delivered so far (slot.Slot.RefreshFunctionKey).
// A slot whose controller keeps its function key does nothing: no key is let
// go, and a function key held stays held, so changing one controller's setting
// never disturbs another. A slot whose function key changes lets go of its keys
// first, through the normal release, and switches. A slot that is disabled or
// restarting asks when it gets its controller. It returns once every slot has
// applied it, with ctx.Err() or ErrStopped like Sync, and does nothing without
// Config.FunctionKey. It must not be called from a slot's goroutine.
func (s *Supervisor) RefreshFunctionKeys(ctx context.Context) error {
	if s.cfg.FunctionKey == nil {
		return nil
	}
	bs, err := s.syncRequest(ctx, syncRequest{action: func(sl *slot.Slot) error {
		sl.RefreshFunctionKey()
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
