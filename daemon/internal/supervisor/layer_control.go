package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type layerRequest struct {
	device, op, name string
	reply            chan layerReply
}
type layerReply struct {
	barriers []*barrier
	err      error
}

// SetLayer changes persistent layer selection on matching slot owners, ordered
// with input. device is empty for all slots, a connection ID or a DeviceKey.
// Hold is deliberately unavailable: an RPC call has no button release owner.
func (s *Supervisor) SetLayer(ctx context.Context, device, op, name string) error {
	if !slices.Contains([]string{"switch", "enable", "disable", "toggle"}, op) {
		return fmt.Errorf("supervisor: unsupported layer operation %q", op)
	}
	s.reload.Lock()
	defer s.reload.Unlock()
	req := layerRequest{device: device, op: op, name: name, reply: make(chan layerReply, 1)}
	select {
	case s.layers <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStopped
	}
	var reply layerReply
	select {
	case reply = <-req.reply:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStopped
	}
	if reply.err != nil {
		return reply.err
	}
	if err := s.awaitAll(ctx, reply.barriers, "set layer"); err != nil {
		return err
	}
	for _, b := range reply.barriers {
		if !b.ran {
			return errors.New("supervisor: layer target restarted before the change")
		}
	}
	return nil
}

func (r *router) setLayer(req layerRequest) {
	if !slices.ContainsFunc(r.program.Profile().Layers, func(l *profile.Layer) bool { return l.Name == req.name }) {
		req.reply <- layerReply{err: fmt.Errorf("supervisor: unknown layer %q", req.name)}
		return
	}
	var targets []*route
	for _, rt := range r.routes {
		if req.device != "" && (rt.dev == nil || req.device != rt.dev.key.String() && req.device != strconv.FormatUint(uint64(rt.dev.id), 10)) {
			continue
		}
		if rt.disabled {
			req.reply <- layerReply{err: errors.New("supervisor: layer target is disabled")}
			return
		}
		targets = append(targets, rt)
	}
	if len(targets) == 0 {
		req.reply <- layerReply{err: errors.New("supervisor: device is not assigned to a slot")}
		return
	}
	reply := layerReply{barriers: make([]*barrier, 0, len(targets))}
	for _, rt := range targets {
		b := newBarrier()
		b.action = func(sl *slot.Slot) error { return sl.SetLayer(req.op, req.name) }
		reply.barriers = append(reply.barriers, b)
		r.await(rt, b)
	}
	req.reply <- reply
}
