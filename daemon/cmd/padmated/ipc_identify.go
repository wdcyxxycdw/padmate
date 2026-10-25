package main

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// rpcIdentify is devices.identify {device} (IN-13): the controller the
// selector names, a connection ID or a DeviceKey as in devices.list, buzzes
// and flashes for at most a second and a half so that the user can tell which
// it is. It answers which cues the controller gave, {rumble, led, player};
// all false means it can show nothing, which is no error.
//
// The request goes to the input and not through the engine or the effect
// queue: it works for a controller that has no slot (waiting, ignored, not a
// gamepad), while the mapping is paused, and whatever the profile says about
// feedback, because the user asked for it. The selector is required: a
// request without one would have to pick a controller for the user.
func (r *liveRun) rpcIdentify(ctx context.Context, req *jrpc2.Request) (any, error) {
	var params deviceParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
		return nil, err
	}
	if params.Device == "" {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "device is required: a connection ID or a DeviceKey"}
	}
	devices, err := r.selectedDevices(ctx, params.Device)
	if err != nil {
		return nil, err
	}
	cues, err := r.src.Identify(devices[0].Info.ID)
	if errors.Is(err, input.ErrNoDevice) {
		// Gone between the lookup and the call.
		return nil, &jrpc2.Error{Code: -32004, Message: "device is not connected"}
	}
	if err != nil {
		r.log.Warn("identify failed", "device", devices[0].Key, "err", err)
		return nil, err
	}
	return cues, nil
}
