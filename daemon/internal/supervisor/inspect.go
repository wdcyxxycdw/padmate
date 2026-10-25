package supervisor

import (
	"context"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Inspect reads mapping status on each slot's owner loop, after input already
// queued at the router. It never reads an engine from the caller's goroutine.
// Views are individually consistent, not one global point-in-time snapshot.
// Disabled or restarting slots without a completed owner job are omitted.
func (s *Supervisor) Inspect(ctx context.Context) ([]slot.View, error) {
	bs, err := s.syncWithViews(ctx, true)
	if err != nil {
		return nil, err
	}
	views := make([]slot.View, 0, len(bs))
	for _, b := range bs {
		if b.ran {
			views = append(views, b.view)
		}
	}
	return views, nil
}
