package main

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// The latency statistics (DMN-15, ADR-0071): T-output measures, for each key
// it sends for an input edge, the time from T-input's receipt of the edge to
// the return of the Send call (liveRun.latencyStats, filled through
// output.ArbiterOptions.Latency), and keeps the latest 1000 per device.
// latency.get and latency.reset are how the UI's diagnostics page and
// padmatectl read and clear them.

// rpcLatency is the result of latency.get. Times are in microseconds, rounded
// down; they are the nearest-rank percentiles of the bench (ENG-11), over the
// device's latest samples.
type rpcLatency struct {
	Devices []rpcLatencyDevice `json:"devices"`
	// DryRun says the samples are of a daemon-wide --dry-run, whose Sender is
	// the printer, not SendInput: they are real, but of nothing the system sees.
	DryRun bool `json:"dryRun"`
	// WarnP99Us and WarnMinSamples are the product threshold (latency.WarnP99,
	// latency.WarnMinSamples): a device whose p99 is above the first, from the
	// second number of samples on, has Warn.
	WarnP99Us      int64 `json:"warnP99Us"`
	WarnMinSamples int   `json:"warnMinSamples"`
}

type rpcLatencyDevice struct {
	// Device is the DeviceKey, as in status.get's slots; ID the connection id
	// and Name the name SDL gave.
	Device  string         `json:"device"`
	ID      input.DeviceID `json:"id"`
	Name    string         `json:"name"`
	Samples int            `json:"samples"`
	P50     int64          `json:"p50"`
	P90     int64          `json:"p90"`
	P99     int64          `json:"p99"`
	Max     int64          `json:"max"`
	Warn    bool           `json:"warn"`
}

func (r *liveRun) latencyMethods(methods handler.Map) {
	methods["latency.get"] = r.rpcLatencyGet
	methods["latency.reset"] = r.rpcLatencyReset
}

// rpcLatencyGet is latency.get {device?}: without a selector the devices that
// have a slot, with one (a connection ID or a DeviceKey, as devices.list) that
// device, mapped or not. A device without samples has zeros.
func (r *liveRun) rpcLatencyGet(ctx context.Context, req *jrpc2.Request) (any, error) {
	var params deviceParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
		return nil, err
	}
	devices, err := r.selectedDevices(ctx, params.Device)
	if err != nil {
		return nil, err
	}
	res := rpcLatency{
		Devices: []rpcLatencyDevice{}, DryRun: r.cfg.opts.dryRun,
		WarnP99Us: latency.WarnP99.Microseconds(), WarnMinSamples: latency.WarnMinSamples,
	}
	for _, d := range devices {
		if params.Device == "" && d.Slot < 0 {
			continue
		}
		s := r.latencyStats.Summary(d.Key)
		res.Devices = append(res.Devices, rpcLatencyDevice{
			Device: d.Key, ID: d.Info.ID, Name: d.Info.Name, Samples: s.N,
			P50: s.P50.Microseconds(), P90: s.P90.Microseconds(), P99: s.P99.Microseconds(), Max: s.Max.Microseconds(),
			Warn: latency.Warn(s),
		})
	}
	return res, nil
}

// rpcLatencyReset is latency.reset {device?}: forget the samples of the device
// the selector names, or of all devices.
func (r *liveRun) rpcLatencyReset(ctx context.Context, req *jrpc2.Request) (any, error) {
	var params deviceParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
		return nil, err
	}
	if params.Device == "" {
		r.latencyStats.ResetAll()
		return map[string]bool{"reset": true}, nil
	}
	devices, err := r.selectedDevices(ctx, params.Device)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		r.latencyStats.Reset(d.Key)
	}
	return map[string]bool{"reset": true}, nil
}
