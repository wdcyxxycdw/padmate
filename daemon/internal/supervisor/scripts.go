package supervisor

import (
	"context"
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Scripts inspects or requests a script reload on the owning slots. A reload
// is asynchronous; subsequent status queries report the candidate/swap.
func (s *Supervisor) Scripts(ctx context.Context, device string, metadata, reload bool) ([]slot.ScriptView, error) {
	s.reload.Lock()
	defer s.reload.Unlock()
	bs, err := s.syncRequest(ctx, syncRequest{script: true, metadata: metadata, reloadScript: reload, device: device})
	if err != nil {
		return nil, err
	}
	views := make([]slot.ScriptView, 0, len(bs))
	for _, b := range bs {
		if b.ran && b.scriptView != nil {
			views = append(views, *b.scriptView)
		}
	}
	if device != "" && len(views) == 0 {
		return nil, errors.New("supervisor: device has no available script slot")
	}
	return views, nil
}
