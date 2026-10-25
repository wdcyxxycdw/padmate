package supervisor

import (
	"context"
	"fmt"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type paramsRequest struct {
	raw      map[string]any
	ready    chan error
	decision chan struct{}
	commit   bool // published to owner jobs by closing decision
	reply    chan []*barrier
}

// UpdateScriptParams changes contexts in place on every slot's owner loop.
// Each loop prepares its replacement and briefly waits for the caller's
// decision, so no callbacks observe a partial or rejected update. The router
// only posts these jobs and remains free to route input and safety events.
// commitConfig publishes the shared definition after every preparation has
// succeeded and before loops resume; it must be quick and must not call slots.
// Like Reload, this method must not be called by a slot callback.
func (s *Supervisor) UpdateScriptParams(ctx context.Context, raw map[string]any, commitConfig func()) error {
	s.reload.Lock()
	defer s.reload.Unlock()
	req := &paramsRequest{
		raw: raw, ready: make(chan error, len(s.runners)),
		decision: make(chan struct{}), reply: make(chan []*barrier, 1),
	}
	decide := sync.OnceFunc(func() { close(req.decision) })
	defer decide() // a canceled/stopped caller always unblocks parked loops
	select {
	case s.params <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStopped
	}
	var bs []*barrier
	select {
	case bs = <-req.reply:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop:
		return ErrStopped
	}
	for range bs {
		select {
		case err := <-req.ready:
			if err != nil {
				return fmt.Errorf("supervisor: script params: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-s.stop:
			return ErrStopped
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop:
		return ErrStopped
	default:
	}
	// Closing decision publishes both the configuration and the successful
	// decision to every owner loop, without moving any VM across threads.
	if commitConfig != nil {
		commitConfig()
	}
	req.commit = true
	decide()
	return s.awaitAll(ctx, bs, "script params")
}

func (r *router) updateParams(req *paramsRequest) {
	bs := make([]*barrier, 0, len(r.routes))
	for _, rt := range r.routes {
		b := newBarrier()
		var once sync.Once
		ready := func(err error) { once.Do(func() { req.ready <- err }) }
		b.after = func() { ready(nil) } // disabled/abandoned slots have no active VM
		b.action = func(sl *slot.Slot) error {
			commit, err := sl.PrepareScriptParams(req.raw)
			ready(err)
			<-req.decision
			if err == nil && req.commit {
				commit()
			}
			return err
		}
		bs = append(bs, b)
		r.await(rt, b)
	}
	req.reply <- bs
}
