package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

const (
	// enumerateWait is how long spk01 collects the connected devices
	// before the header. SDL reports most of them during SDL_Init, the
	// others once its backends have detected them.
	enumerateWait = 500 * time.Millisecond
	// drainWait bounds the wait for the virtual gamepad's last events
	// after the window, and then for its removal.
	drainWait = time.Second
	// foregroundEvery is how often the window checks which process owns
	// the foreground window.
	foregroundEvery = 100 * time.Millisecond
	// virtualName is the name of the --virtual-rate gamepad.
	virtualName = "padmate spk01 virtual gamepad"
)

// spike is one run of spk01, without the tray.
type spike struct {
	s      *input.Source // closed by run
	opts   options
	log    io.Writer // the --log file, or nil
	stderr io.Writer
	stop   *stopper
	// trayReady is closed once the tray is set up; nil without a tray.
	trayReady  <-chan struct{}
	mainThread uint32
}

// run records the events, drives the virtual gamepad, closes the Source
// and writes the summary. It returns the exit code.
func (sp *spike) run() int {
	sum := summaryJSON{
		Type:        "summary",
		LogicalCPUs: logicalCPUs(),
		MainThread:  sp.mainThread,
		InputThread: sp.s.ThreadID(),
	}
	fail := func(format string, args ...any) {
		sum.Errors = append(sum.Errors, fmt.Sprintf(format, args...))
	}
	var w *bufio.Writer
	if sp.log != nil {
		w = bufio.NewWriter(sp.log)
	}
	rec := newRecorder(w)

	early := sp.enumerate()
	rec.writeJSON(sp.header(early, sum.LogicalCPUs))
	sp.printHeader()
	for _, e := range early {
		rec.record(e)
	}

	var v *input.Virtual
	if sp.opts.rate > 0 {
		var err error
		if v, err = sp.s.AttachVirtual(virtualName); err != nil {
			fail("%v", err)
		} else {
			rec.virtual = v.ID()
		}
	}

	c0, err0 := processTimes()
	t0 := time.Now()
	var dres <-chan driveResult
	dstop := make(chan struct{})
	if v != nil {
		c := make(chan driveResult, 1)
		dres = c
		go func() { c <- drive(v, sp.opts.rate, dstop) }()
	}
	sum.Reason, sum.Foreground = sp.window(rec, fail)
	close(dstop)
	var dr driveResult
	if dres != nil {
		dr = <-dres
	}
	t1 := time.Now()
	c1, err1 := processTimes()
	sum.WindowSec = round(t1.Sub(t0).Seconds())
	if err := firstErr(err0, err1); err != nil {
		fail("%v", err)
	} else {
		sum.CPU = newCPUJSON(c1.user-c0.user, c1.kernel-c0.kernel, t1.Sub(t0), sum.LogicalCPUs)
	}

	if v != nil {
		if dr.err != nil {
			fail("virtual gamepad: %v", dr.err)
		}
		sp.finishVirtual(rec, v, dr.sent, fail)
		sum.Virtual = &virtualJSON{
			Rate: sp.opts.rate, Sent: dr.sent, Received: rec.virtualInputs,
			AchievedHz: round(float64(dr.paced) / dr.elapsed.Seconds()), Removed: rec.virtualRemoved,
		}
		if rec.virtualInputs != dr.sent {
			fail("virtual gamepad: %d state changes sent, %d events received", dr.sent, rec.virtualInputs)
		}
		if !rec.virtualRemoved {
			fail("virtual gamepad: no removal event within %v of Detach", drainWait)
		}
	}
	sp.s.Close()
	for e := range sp.s.Events() {
		rec.record(e)
	}

	if c, err := processTimes(); err != nil {
		fail("%v", err)
	} else {
		sum.ProcessCPU = newCPUJSON(c.user, c.kernel, time.Since(c.created), sum.LogicalCPUs)
	}
	sum.Events = rec.events
	sum.ByKind = rec.byKind()
	sum.Dropped = sp.s.Dropped()
	if sum.Dropped > 0 {
		fail("%d events dropped", sum.Dropped)
	}
	sum.Devices = rec.devices.list()
	select {
	case <-sp.trayReady:
		sum.TrayReady = true
	default:
	}
	sum.OK = len(sum.Errors) == 0
	rec.writeJSON(sum)
	if err := rec.flush(); err != nil {
		sum.OK = false
		sum.Errors = append(sum.Errors, fmt.Sprintf("--log: %v", err))
	}
	printSummary(sp.stderr, &sum)
	if !sum.OK {
		return 1
	}
	return 0
}

// enumerate returns the events of the first enumerateWait: mostly the
// devices that are already connected.
func (sp *spike) enumerate() []input.Event {
	var evs []input.Event
	timer := time.NewTimer(enumerateWait)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-sp.s.Events():
			if !ok {
				return evs
			}
			evs = append(evs, e)
		case <-timer.C:
			return evs
		case <-sp.stop.c:
			return evs
		}
	}
}

// window records events for --duration, or until the stopper stops it,
// and samples the foreground window's process meanwhile. It returns why
// it ended.
func (sp *spike) window(rec *recorder, fail func(string, ...any)) (string, foregroundJSON) {
	var fg foregroundJSON
	pid := uint32(os.Getpid())
	timer := time.NewTimer(sp.opts.duration)
	defer timer.Stop()
	ticker := time.NewTicker(foregroundEvery)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-sp.s.Events():
			if !ok {
				fail("the SDL event loop stopped")
				return "events closed", fg
			}
			rec.record(e)
		case <-ticker.C:
			fg.Samples++
			if foregroundPID() == pid {
				fg.Own++
			}
		case <-timer.C:
			return "duration", fg
		case <-sp.stop.c:
			return sp.stop.reason, fg
		}
	}
}

// finishVirtual waits until the events of the sent state changes have
// arrived, detaches v and waits for its removal.
func (sp *spike) finishVirtual(rec *recorder, v *input.Virtual, sent uint64, fail func(string, ...any)) {
	sp.recordUntil(rec, func() bool { return rec.virtualInputs >= sent })
	if err := v.Detach(); err != nil {
		fail("virtual gamepad: Detach: %v", err)
		return
	}
	sp.recordUntil(rec, func() bool { return rec.virtualRemoved })
}

// recordUntil records events until done returns true, for at most
// drainWait.
func (sp *spike) recordUntil(rec *recorder, done func() bool) {
	timer := time.NewTimer(drainWait)
	defer timer.Stop()
	for !done() {
		select {
		case e, ok := <-sp.s.Events():
			if !ok {
				return
			}
			rec.record(e)
		case <-timer.C:
			return
		}
	}
}

// header describes the run, with the devices reported by the events of
// the enumeration.
func (sp *spike) header(early []input.Event, cpus int) headerJSON {
	h := headerJSON{
		Type: "header", Tool: "spk01", Version: version.String(), PID: os.Getpid(),
		SDL: sp.s.Version(), Library: sp.s.Library(), Preset: sp.opts.preset,
		MainThread: sp.mainThread, InputThread: sp.s.ThreadID(), LogicalCPUs: cpus,
		DurationNs: sp.opts.duration.Nanoseconds(), VirtualRate: sp.opts.rate,
		Devices: []input.DeviceInfo{},
	}
	for _, hint := range sp.s.Hints() {
		h.Hints = append(h.Hints, hintJSON{hint.Name, hint.Value})
	}
	var ds deviceSet
	for _, e := range early {
		ds.apply(e)
	}
	for _, d := range ds.list() {
		if !d.Removed {
			h.Devices = append(h.Devices, d.DeviceInfo)
		}
	}
	return h
}

// printHeader prints the SDL version, the library, the hints in effect
// and the threads.
func (sp *spike) printHeader() {
	w := sp.stderr
	// Like the other diagnostics on stderr, a failed write is ignored.
	_ = sp.s.WriteHeader(w, "spk01: ", sp.opts.preset)
	fmt.Fprintf(w, "spk01: main (tray) thread %d, T-input thread %d; recording for %v\n",
		sp.mainThread, sp.s.ThreadID(), sp.opts.duration)
}

// printSummary prints the summary as text.
func printSummary(w io.Writer, s *summaryJSON) {
	kinds := make([]string, 0, len(s.ByKind))
	for _, k := range []string{"added", "removed", "down", "up", "axis"} {
		if n := s.ByKind[k]; n > 0 {
			kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
		}
	}
	fmt.Fprintf(w, "spk01: window %.3fs, ended by %s\n", s.WindowSec, s.Reason)
	fmt.Fprintf(w, "spk01: %d events (%s), %d dropped\n", s.Events, strings.Join(kinds, ", "), s.Dropped)
	if v := s.Virtual; v != nil {
		fmt.Fprintf(w, "spk01: virtual gamepad at %d Hz: %d state changes sent (%.1f Hz), %d events received, removed %t\n",
			v.Rate, v.Sent, v.AchievedHz, v.Received, v.Removed)
	}
	for _, c := range []struct {
		what string
		cpu  cpuJSON
	}{{"window", s.CPU}, {"process lifetime", s.ProcessCPU}} {
		fmt.Fprintf(w, "spk01: CPU over the %s (%.3fs): user %.1fms, kernel %.1fms = %.3f%% of one core, %.4f%% of %d logical CPUs\n",
			c.what, c.cpu.WallSec, c.cpu.UserMs, c.cpu.KernelMs, c.cpu.OneCorePercent, c.cpu.MachinePercent, s.LogicalCPUs)
	}
	fmt.Fprintf(w, "spk01: tray ready %t; the foreground window was spk01's in %d of %d samples\n",
		s.TrayReady, s.Foreground.Own, s.Foreground.Samples)
	for _, d := range s.Devices {
		fmt.Fprintf(w, "spk01: device %d %s %q %04x:%04x driver=%s guid=%s virtual=%t removed=%t\n",
			d.ID, d.Type, d.Name, d.Vendor, d.Product, d.Driver, d.GUID, d.Virtual, d.Removed)
	}
	for _, e := range s.Errors {
		fmt.Fprintf(w, "spk01: error: %s\n", e)
	}
	if s.OK {
		fmt.Fprintln(w, "spk01: ok")
	} else {
		fmt.Fprintln(w, "spk01: FAIL")
	}
}

// driveResult is what drive did.
type driveResult struct {
	sent uint64 // all state changes made
	// paced are the state changes made at the rate, over elapsed.
	paced   uint64
	elapsed time.Duration
	err     error
}

// drive changes the state of v rate times per second until stop is
// closed, then returns it to rest: SDL would otherwise release the button
// or recenter the axis itself when v is detached, which is one more event
// than the state changes made. It paces itself by the start time, so it
// catches up after a slow change instead of drifting.
func drive(v *input.Virtual, rate int, stop <-chan struct{}) driveResult {
	period := time.Second / time.Duration(rate)
	start := time.Now()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	// wait waits until t, and reports false once stop is closed.
	wait := func(t time.Time) bool {
		d := time.Until(t)
		if d <= 0 {
			select {
			case <-stop:
				return false
			default:
				return true
			}
		}
		timer.Reset(d)
		select {
		case <-stop:
			return false
		case <-timer.C:
			return true
		}
	}
	var n uint64
	for wait(start.Add(time.Duration(n) * period)) {
		if err := driveStep(v, n); err != nil {
			return driveResult{n, n, time.Since(start), err}
		}
		n++
	}
	r := driveResult{paced: n, elapsed: time.Since(start)}
	for ; n%4 != 0; n++ {
		if err := driveStep(v, n); err != nil {
			r.err = err
			break
		}
	}
	r.sent = n
	return r
}

// driveStep makes state change n of the cycle south down, south up, leftx
// 16384, leftx 0. Each change produces exactly one event.
func driveStep(v *input.Virtual, n uint64) error {
	switch n % 4 {
	case 0:
		return v.SetButton("south", true)
	case 1:
		return v.SetButton("south", false)
	case 2:
		return v.SetAxis("leftx", 16384)
	default:
		return v.SetAxis("leftx", 0)
	}
}

// recorder counts events, keeps track of the devices and writes the
// JSON lines of --log.
type recorder struct {
	w      *bufio.Writer // nil without --log
	err    error         // the first write error
	line   []byte
	events uint64
	kinds  [input.AxisMotion + 1]uint64
	// devices are all the devices seen.
	devices deviceSet
	// virtual is the virtual gamepad, 0 for none: SDL never uses 0 as a
	// joystick id.
	virtual        input.DeviceID
	virtualInputs  uint64 // button and axis events of virtual
	virtualRemoved bool
}

func newRecorder(w *bufio.Writer) *recorder {
	return &recorder{w: w}
}

// record counts e and writes its line. It formats the line by hand: at
// the rates spk01 measures, reflection would be a noticeable part of the
// CPU time.
func (r *recorder) record(e input.Event) {
	r.events++
	if int(e.Kind) < len(r.kinds) {
		r.kinds[e.Kind]++
	}
	r.devices.apply(e)
	if e.Device == r.virtual {
		switch e.Kind {
		case input.DeviceRemoved:
			r.virtualRemoved = true
		case input.ButtonDown, input.ButtonUp, input.AxisMotion:
			r.virtualInputs++
		}
	}
	if r.w == nil || r.err != nil {
		return
	}
	b := r.line[:0]
	b = append(b, `{"type":"event","timeNs":`...)
	b = strconv.AppendUint(b, e.Time, 10)
	b = append(b, `,"kind":`...)
	b = appendJSONString(b, e.Kind.String())
	b = append(b, `,"device":`...)
	b = strconv.AppendUint(b, uint64(e.Device), 10)
	if e.Control != "" {
		b = append(b, `,"control":`...)
		b = appendJSONString(b, e.Control)
	}
	if e.Kind == input.AxisMotion {
		b = append(b, `,"value":`...)
		b = strconv.AppendInt(b, int64(e.Value), 10)
	}
	if e.Info != nil {
		info, err := json.Marshal(e.Info)
		if err != nil {
			r.err = err
			return
		}
		b = append(b, `,"info":`...)
		b = append(b, info...)
	}
	b = append(b, "}\n"...)
	r.line = b
	_, r.err = r.w.Write(b)
}

// byKind returns the event counts by kind name.
func (r *recorder) byKind() map[string]uint64 {
	m := make(map[string]uint64)
	for k, n := range r.kinds {
		if n > 0 {
			m[input.EventKind(k).String()] = n
		}
	}
	return m
}

// writeJSON writes v as one line.
func (r *recorder) writeJSON(v any) {
	if r.w == nil || r.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		r.err = err
		return
	}
	if _, err := r.w.Write(append(b, '\n')); err != nil {
		r.err = err
	}
}

// flush writes out the buffered lines and returns the first error.
func (r *recorder) flush() error {
	if r.w == nil {
		return nil
	}
	if r.err == nil {
		r.err = r.w.Flush()
	}
	return r.err
}

// appendJSONString appends s as a JSON string. s must be valid UTF-8.
func appendJSONString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c < 0x20:
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		default:
			b = append(b, c)
		}
	}
	return append(b, '"')
}

// deviceSet is the devices seen, in the order they were added.
type deviceSet struct {
	order []input.DeviceID
	byID  map[input.DeviceID]*seenDeviceJSON
}

// apply notes the device added or removed by e.
func (ds *deviceSet) apply(e input.Event) {
	switch e.Kind {
	case input.DeviceAdded:
		if ds.byID == nil {
			ds.byID = make(map[input.DeviceID]*seenDeviceJSON)
		}
		if _, ok := ds.byID[e.Device]; ok {
			return
		}
		d := &seenDeviceJSON{DeviceInfo: input.DeviceInfo{ID: e.Device}}
		if e.Info != nil {
			d.DeviceInfo = *e.Info
		}
		ds.byID[e.Device] = d
		ds.order = append(ds.order, e.Device)
	case input.DeviceRemoved:
		if d, ok := ds.byID[e.Device]; ok {
			d.Removed = true
		}
	}
}

// list returns the devices in the order they were added.
func (ds *deviceSet) list() []seenDeviceJSON {
	l := make([]seenDeviceJSON, 0, len(ds.order))
	for _, id := range ds.order {
		l = append(l, *ds.byID[id])
	}
	return l
}

// newCPUJSON describes the CPU time user+kernel spent over wall.
func newCPUJSON(user, kernel, wall time.Duration, cpus int) cpuJSON {
	c := cpuJSON{
		WallSec:  round(wall.Seconds()),
		UserMs:   round(float64(user) / float64(time.Millisecond)),
		KernelMs: round(float64(kernel) / float64(time.Millisecond)),
	}
	if wall > 0 {
		core := float64(user+kernel) / float64(wall) * 100
		c.OneCorePercent = round(core)
		if cpus > 0 {
			c.MachinePercent = round(core / float64(cpus))
		}
	}
	return c
}

// round rounds x to 4 decimal places, for readable JSON.
func round(x float64) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return 0
	}
	return math.Round(x*1e4) / 1e4
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// The JSON lines of --log.
type (
	headerJSON struct {
		Type        string             `json:"type"` // "header"
		Tool        string             `json:"tool"`
		Version     string             `json:"version"`
		PID         int                `json:"pid"`
		SDL         string             `json:"sdl"`
		Library     string             `json:"library"`
		Preset      string             `json:"preset"`
		Hints       []hintJSON         `json:"hints"`
		MainThread  uint32             `json:"mainThread"`
		InputThread uint32             `json:"inputThread"`
		LogicalCPUs int                `json:"logicalCPUs"`
		DurationNs  int64              `json:"durationNs"`
		VirtualRate int                `json:"virtualRate"`
		Devices     []input.DeviceInfo `json:"devices"` // connected after the enumeration
	}
	// hintJSON is a hint in effect; Value "" means unset, so SDL uses its
	// default.
	hintJSON struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	// seenDeviceJSON is a device with the fields of "padmatectl monitor
	// --json" and whether it was removed.
	seenDeviceJSON struct {
		input.DeviceInfo
		Removed bool `json:"removed"`
	}
	summaryJSON struct {
		Type      string            `json:"type"` // "summary"
		Reason    string            `json:"reason"`
		WindowSec float64           `json:"windowSec"`
		Events    uint64            `json:"events"` // all, the enumeration's included
		ByKind    map[string]uint64 `json:"byKind"`
		Dropped   uint64            `json:"dropped"`
		Virtual   *virtualJSON      `json:"virtual,omitempty"`
		// CPU is over the recording window, ProcessCPU from the start of
		// the process to the end.
		CPU         cpuJSON          `json:"cpu"`
		ProcessCPU  cpuJSON          `json:"processCpu"`
		LogicalCPUs int              `json:"logicalCPUs"`
		MainThread  uint32           `json:"mainThread"`
		InputThread uint32           `json:"inputThread"`
		TrayReady   bool             `json:"trayReady"`
		Foreground  foregroundJSON   `json:"foreground"`
		Devices     []seenDeviceJSON `json:"devices"`
		Errors      []string         `json:"errors,omitempty"`
		OK          bool             `json:"ok"`
	}
	virtualJSON struct {
		Rate       int     `json:"rate"`
		Sent       uint64  `json:"sent"`
		Received   uint64  `json:"received"`
		AchievedHz float64 `json:"achievedHz"`
		Removed    bool    `json:"removed"`
	}
	cpuJSON struct {
		WallSec        float64 `json:"wallSec"`
		UserMs         float64 `json:"userMs"`
		KernelMs       float64 `json:"kernelMs"`
		OneCorePercent float64 `json:"oneCorePercent"`
		MachinePercent float64 `json:"machinePercent"`
	}
	// foregroundJSON counts the samples of the foreground window's process
	// during the window, and those where it was spk01.
	foregroundJSON struct {
		Samples int `json:"samples"`
		Own     int `json:"own"`
	}
)
