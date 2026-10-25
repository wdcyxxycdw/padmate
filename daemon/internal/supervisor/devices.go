package supervisor

import (
	"cmp"
	"context"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// DeviceView includes devices that have no mapping slot. Slot is -1 for those.
// The snapshot is detached and read on the router owner after queued input.
type DeviceView struct {
	Info     input.DeviceInfo `json:"info"`
	Key      string           `json:"key"`
	Slot     int              `json:"slot"`
	Status   string           `json:"status"`
	Disabled bool             `json:"disabled"`
	// Battery is the latest power the gamepad reported (IN-12): absent until
	// T-input has read one, and for a device without a gamepad. It is
	// dropped with the gamepad's removal, so a gamepad that comes back with
	// a new id has none until it reads again.
	Battery *input.Power `json:"battery,omitempty"`
}

func (s *Supervisor) Devices(ctx context.Context) ([]DeviceView, error) {
	reply := make(chan []DeviceView, 1)
	select {
	case s.deviceQueries <- reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, ErrStopped
	}
	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, ErrStopped
	}
}

func (r *router) deviceViews() []DeviceView {
	result := make([]DeviceView, 0, len(r.devices)+len(r.unmapped))
	for _, d := range r.devices {
		v := DeviceView{Info: d.info, Key: d.key.String(), Slot: -1, Status: "waiting"}
		if d.power != nil {
			p := *d.power
			v.Battery = &p
		}
		if !r.matches(d) {
			v.Status = "ignored"
		}
		for _, rt := range r.routes {
			if rt.dev == d {
				v.Slot = int(rt.index)
				v.Status = "mapped"
				v.Disabled = rt.disabled
				break
			}
		}
		result = append(result, v)
	}
	for _, info := range r.unmapped {
		result = append(result, DeviceView{Info: info, Key: input.KeyOf(&info).String(), Slot: -1, Status: "unmapped"})
	}
	slices.SortFunc(result, func(a, b DeviceView) int { return cmp.Compare(a.Info.ID, b.Info.ID) })
	return result
}
