package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// These tests are the daemon half of the mapping wizard and the custom
// gamepad mappings (IN-17, IN-18, IN-19, ADR-0072). A fake input stands in for
// SDL; the SDL side is tested in internal/input and in
// TestMappingWizardWithVirtualJoystick.

const mapGUID = "ff00d0df317374207261772070007600"

// mappingFake is a fakeSource that takes custom mappings and raw events.
type mappingFake struct {
	*fakeSource
	mu      sync.Mutex
	calls   [][]string
	report  input.MappingReport
	err     error
	raw     chan input.RawEvent
	started []input.DeviceID
	stopped []input.DeviceID
	info    input.RawInfo
	// watching are the devices whose raw events are on, as the input has them.
	watching map[input.DeviceID]bool
	// stopHook, if set, runs in RawStop before it does anything.
	stopHook func(input.DeviceID)
}

func newMappingFake() *mappingFake {
	return &mappingFake{fakeSource: newFakeSource(), raw: make(chan input.RawEvent, 16)}
}

func (f *mappingFake) SetMappings(m []string) (input.MappingReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(m))
	return f.report, f.err
}

func (f *mappingFake) RawEvents() <-chan input.RawEvent { return f.raw }

func (f *mappingFake) RawStart(id input.DeviceID) (input.RawInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
	if f.watching == nil {
		f.watching = map[input.DeviceID]bool{}
	}
	f.watching[id] = true
	info := f.info
	info.Device = id
	return info, nil
}

func (f *mappingFake) RawStop(id input.DeviceID) error {
	if f.stopHook != nil {
		f.stopHook(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	delete(f.watching, id)
	return nil
}

func (f *mappingFake) isWatching(id input.DeviceID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watching[id]
}

func (f *mappingFake) setCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *mappingFake) rawStopped() []input.DeviceID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stopped)
}

func mapPad(id input.DeviceID, gamepad bool) input.Event {
	return input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{ID: id, GUID: mapGUID, Name: "Cheap Pad", IsGamepad: gamepad}}
}

// startMappingLive starts a library daemon with config as config.json and a
// mappingFake as its input; open sees the Options the input was opened with.
func startMappingLive(t *testing.T, config string, open *input.Options) (*testLive, *mappingFake) {
	t.Helper()
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	src := newMappingFake()
	tl := startLibraryLiveOpen(t, config, options{dataDir: dir, dryRun: true}, func(o input.Options) (eventSource, error) {
		if open != nil {
			*open = o
		}
		return src, nil
	})
	return tl, src
}

func mappingCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	t.Cleanup(cancel)
	return ctx
}

func rpcCode(err error) int {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return int(rpcErr.Code)
	}
	return 0
}

// TestSetMappingWritesEveryKeyOfTheGUID: SDL applies a mapping by GUID, so the
// wizard's mapping is written under every device key with the GUID: the keys
// of the connected controllers and the ones config.json has. The mapping is
// the request's with the name *, no type and platform:Windows; the input gets
// the mapping once.
func TestSetMappingWritesEveryKeyOfTheGUID(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0,"devices":{"`+mapGUID+`#2":{"ignored":true},"`+strings.Repeat("ab", 16)+`":{"ignored":true}}}`, nil)
	src.report = input.MappingReport{Devices: []input.MappingStatus{
		{Device: 1, GUID: mapGUID, Gamepad: true, Applied: true},
		{Device: 2, GUID: mapGUID, Gamepad: true, Applied: false, Got: "other"},
	}}
	// Two recognized controllers of one model are told apart by #N; joysticks
	// that SDL does not know all have the plain GUID (the registry numbers
	// gamepads only).
	src.events <- mapPad(1, true)
	src.events <- mapPad(2, true)
	tl.sync()
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)

	var res rpcMappingResult
	req := map[string]string{"device": "1", "mapping": mapGUID + ",Cheap Pad,a:b0,b:b1,type:xboxone,platform:Windows"}
	if err := client.CallResult(ctx, "devices.setMapping", req, &res); err != nil {
		t.Fatal(err)
	}
	want := mapGUID + ",*,a:b0,b:b1,platform:Windows"
	if res.Mapping != want || !res.Changed || !res.Applied || res.Problem != "" || res.GUID != mapGUID {
		t.Fatalf("result = %+v, want %q saved and applied", res, want)
	}
	if wantKeys := []string{mapGUID, mapGUID + "#1", mapGUID + "#2"}; !slices.Equal(res.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v: the connected devices' and the one config.json has", res.Keys, wantKeys)
	}
	if len(res.Notes) != 2 {
		t.Errorf("Notes = %q, want the name and the type", res.Notes)
	}
	var codes []string
	for _, w := range res.Warnings {
		codes = append(codes, w.Code)
	}
	if !slices.Equal(codes, []string{"shadowed", "noBack", "noStart"}) {
		t.Errorf("warnings = %v, want shadowed (device 2 reads another mapping), noBack and noStart", codes)
	}
	if len(res.Devices) != 2 || !res.Devices[0].Applied || res.Devices[1].Applied || res.Devices[1].Got != "other" {
		t.Errorf("Devices = %+v", res.Devices)
	}
	if calls := src.setCalls(); len(calls) != 1 || !slices.Equal(calls[0], []string{want}) {
		t.Errorf("SetMappings calls = %q, want one with the mapping", calls)
	}
	// config.json has it under all three keys, and keeps the rest.
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	c, ds := profile.ParseConfig([]byte(doc.Text))
	if c == nil {
		t.Fatalf("config.json does not parse: %v\n%s", ds, doc.Text)
	}
	for _, key := range res.Keys {
		if c.Devices[key].Mapping != want {
			t.Errorf("config.json: %s mapping = %q", key, c.Devices[key].Mapping)
		}
	}
	if !c.Devices[mapGUID+"#2"].Ignored || !c.Devices[strings.Repeat("ab", 16)].Ignored {
		t.Errorf("config.json lost other settings: %s", doc.Text)
	}
	if doc.RestartRequired {
		t.Error("config.get asks for a restart for a mapping")
	}
	// The same request again changes nothing and tells SDL nothing.
	if err := client.CallResult(ctx, "devices.setMapping", req, &res); err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(src.setCalls()) != 1 {
		t.Errorf("a repeat: Changed %v, %d SetMappings calls", res.Changed, len(src.setCalls()))
	}
	// A mapping with View and Menu has no warning.
	req["mapping"] = mapGUID + ",*,a:b0,back:b6,start:b7,platform:Windows"
	if err := client.CallResult(ctx, "devices.setMapping", req, &res); err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Warnings {
		if w.Code == "noBack" || w.Code == "noStart" {
			t.Errorf("warning %q for a mapping with back and start", w.Code)
		}
	}
}

// TestResetMapping takes the mapping from every key with the GUID and keeps
// their other settings; the input is given the mappings that remain.
func TestResetMapping(t *testing.T) {
	m := mapGUID + ",*,a:b0,platform:Windows"
	other := strings.Repeat("cd", 16)
	om := other + ",*,a:b3,platform:Windows"
	tl, src := startMappingLive(t, `{"schemaVersion":0,"devices":{"`+mapGUID+`":{"mapping":"`+m+`","ignored":true},"`+mapGUID+`#1":{"mapping":"`+m+`"},"`+other+`":{"mapping":"`+om+`"}}}`, nil)
	src.events <- mapPad(1, true)
	tl.sync()
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	var res rpcMappingResult
	if err := client.CallResult(ctx, "devices.resetMapping", map[string]string{"device": "1"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Mapping != "" || !res.Changed || !res.Applied || !slices.Equal(res.Keys, []string{mapGUID, mapGUID + "#1"}) {
		t.Fatalf("result = %+v", res)
	}
	if calls := src.setCalls(); len(calls) != 1 || !slices.Equal(calls[0], []string{om}) {
		t.Errorf("SetMappings calls = %q, want the other controller's mapping alone", calls)
	}
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	c, _ := profile.ParseConfig([]byte(doc.Text))
	if c == nil || c.Devices[mapGUID].Mapping != "" || !c.Devices[mapGUID].Ignored || c.Devices[other].Mapping != om {
		t.Fatalf("config.json after the reset:\n%s", doc.Text)
	}
	if _, ok := c.Devices[mapGUID+"#1"]; ok {
		t.Errorf("a key with nothing left stays in config.json:\n%s", doc.Text)
	}
	// A controller with no mapping: nothing to do.
	if err := client.CallResult(ctx, "devices.resetMapping", map[string]string{"device": "1"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(src.setCalls()) != 1 {
		t.Errorf("a second reset: Changed %v, %d calls", res.Changed, len(src.setCalls()))
	}
}

func TestSetMappingRejects(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.events <- mapPad(1, false)
	tl.sync()
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	other := strings.Repeat("ee", 16)
	for name, tc := range map[string]struct {
		method string
		params map[string]string
		code   int
	}{
		"no device":          {"devices.setMapping", map[string]string{"mapping": mapGUID + ",*,a:b0"}, int(jrpc2.InvalidParams)},
		"no such device":     {"devices.setMapping", map[string]string{"device": "99", "mapping": mapGUID + ",*,a:b0"}, -32004},
		"not a mapping":      {"devices.setMapping", map[string]string{"device": "1", "mapping": "nonsense"}, rpcCodeMappingInvalid},
		"empty":              {"devices.setMapping", map[string]string{"device": "1", "mapping": ""}, rpcCodeMappingInvalid},
		"binds nothing":      {"devices.setMapping", map[string]string{"device": "1", "mapping": mapGUID + ",*,platform:Windows"}, rpcCodeMappingInvalid},
		"another GUID":       {"devices.setMapping", map[string]string{"device": "1", "mapping": other + ",*,a:b0"}, rpcCodeMappingInvalid},
		"another platform":   {"devices.setMapping", map[string]string{"device": "1", "mapping": mapGUID + ",*,a:b0,platform:Linux"}, rpcCodeMappingInvalid},
		"reset: no device":   {"devices.resetMapping", map[string]string{}, int(jrpc2.InvalidParams)},
		"reset: no such one": {"devices.resetMapping", map[string]string{"device": "99"}, -32004},
	} {
		err := client.CallResult(ctx, tc.method, tc.params, new(any))
		if err == nil || rpcCode(err) != tc.code {
			t.Errorf("%s: err = %v (code %d), want code %d", name, err, rpcCode(err), tc.code)
		}
	}
	if err := client.CallResult(ctx, "devices.setMapping", map[string]any{"device": "1", "mapping": mapGUID + ",*,a:b0", "extra": 1}, new(any)); rpcCode(err) != int(jrpc2.InvalidParams) {
		t.Errorf("an unknown member: %v", err)
	}
	if len(src.setCalls()) != 0 {
		t.Errorf("SetMappings called %d times for refused requests", len(src.setCalls()))
	}
}

// TestSetMappingNeedsAnInputThatTakesMappings: the fake IPC input has no SDL.
func TestSetMappingNeedsAnInputThatTakesMappings(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true, fakeInput: "ipc"}, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	var devices []struct {
		Info input.DeviceInfo `json:"info"`
	}
	if err := client.CallResult(ctx, "devices.list", nil, &devices); err != nil || len(devices) == 0 {
		t.Fatal(devices, err)
	}
	guid := devices[0].Info.GUID
	if guid == "" {
		t.Fatal("the fake gamepad has no GUID")
	}
	for method, params := range map[string]map[string]string{
		"devices.setMapping":   {"device": "1", "mapping": guid + ",*,a:b0"},
		"devices.resetMapping": {"device": "1"},
		"devices.rawInfo":      {"device": "1"},
	} {
		err := client.CallResult(ctx, method, params, new(any))
		if rpcCode(err) != rpcCodeMappingUnsupported {
			t.Errorf("%s: %v (code %d), want %d", method, err, rpcCode(err), rpcCodeMappingUnsupported)
		}
	}
}

// TestConfigPutAppliesMappings: a change of the mappings in config.json, made
// by config.put or by hand and put, reaches the input at once; another change
// does not.
func TestConfigPutAppliesMappings(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	client := rpcTestClient(t, tl.liveRun)
	m := mapGUID + ",*,a:b0,platform:Windows"
	if saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":300}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if n := len(src.setCalls()); n != 0 {
		t.Fatalf("%d SetMappings calls for a change of the timing", n)
	}
	saved := putConfig(t, client, `{"schemaVersion":0,"devices":{"`+mapGUID+`":{"mapping":"`+m+`"}}}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("%+v", saved)
	}
	if calls := src.setCalls(); len(calls) != 1 || !slices.Equal(calls[0], []string{m}) {
		t.Fatalf("SetMappings calls = %q", calls)
	}
	// Adding the same mapping under another key of the GUID changes nothing for SDL.
	putConfig(t, client, `{"schemaVersion":0,"devices":{"`+mapGUID+`":{"mapping":"`+m+`"},"`+mapGUID+`#1":{"mapping":"`+m+`"}}}`)
	if n := len(src.setCalls()); n != 1 {
		t.Errorf("%d SetMappings calls after a second key with the same mapping", n)
	}
	putConfig(t, client, `{"schemaVersion":0}`)
	if calls := src.setCalls(); len(calls) != 2 || len(calls[1]) != 0 {
		t.Errorf("SetMappings calls = %q, want an empty list last", calls)
	}
	// Two keys of one GUID that disagree are refused by config.put.
	var doc rpcDocument
	if err := client.CallResult(ctx0(t), "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	bad := `{"schemaVersion":0,"devices":{"` + mapGUID + `":{"mapping":"` + m + `"},"` + mapGUID + `#1":{"mapping":"` + mapGUID + `,*,a:b5,platform:Windows"}}}`
	if err := client.CallResult(ctx0(t), "config.put", map[string]string{"revision": doc.Revision, "text": bad}, new(any)); err == nil {
		t.Error("config.put accepted two keys of one GUID with different mappings")
	}
}

func ctx0(t *testing.T) context.Context { return mappingCtx(t) }

// TestStartupMappings: the input is opened with the mappings of config.json,
// so that SDL has them before it enumerates the devices; always a non-nil
// list, which keeps the database file that later changes rewrite.
func TestStartupMappings(t *testing.T) {
	m := mapGUID + ",*,a:b0,platform:Windows"
	var o input.Options
	startMappingLive(t, `{"schemaVersion":0,"devices":{"`+mapGUID+`":{"mapping":"`+m+`"},"`+mapGUID+`#1":{"mapping":"`+m+`"}}}`, &o)
	if !slices.Equal(o.Mappings, []string{m}) {
		t.Errorf("Options.Mappings = %q, want the one mapping", o.Mappings)
	}
	var none input.Options
	startMappingLive(t, `{"schemaVersion":0}`, &none)
	if none.Mappings == nil || len(none.Mappings) != 0 {
		t.Errorf("Options.Mappings = %#v, want an empty list that is not nil", none.Mappings)
	}
}

func rawSubscriber(t *testing.T, tl *testLive, topics ...string) (*jrpc2.Client, <-chan ipc.Notification) {
	t.Helper()
	rows := make(chan ipc.Notification, 64)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		select {
		case rows <- ipc.Notification{Topic: r.Method(), Data: json.RawMessage(r.ParamString())}:
		default:
		}
	}})
	if err := client.CallResult(mappingCtx(t), "subscribe", map[string]any{"topics": topics}, new(any)); err != nil {
		t.Fatal(err)
	}
	return client, rows
}

// TestRawInfoAndFeed: devices.rawInfo opens the raw events of a controller;
// they come as input.raw events to whoever subscribes, and stop when the
// session that asked for them ends.
func TestRawInfoAndFeed(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.info = input.RawInfo{GUID: mapGUID, Axes: 4, Buttons: 12, Hats: 1, AxisRest: []int16{0, 0, -32768, 0}}
	src.events <- mapPad(1, false) // no gamepad mapping: nothing to pause
	tl.sync()
	client, rows := rawSubscriber(t, tl, "input.raw")
	ctx := mappingCtx(t)
	var info rpcRawInfo
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != 1 || info.Device != mapGUID || info.Axes != 4 || info.Buttons != 12 || info.Hats != 1 || !info.Watching || info.Gamepad || len(info.AxisRest) != 4 || info.AxisRest[2] != -32768 {
		t.Fatalf("rawInfo = %+v", info)
	}
	src.raw <- input.RawEvent{Time: 5, Device: 1, Kind: input.RawAxis, Index: 2, Value: -32768}
	src.raw <- input.RawEvent{Time: 6, Device: 1, Kind: input.RawButton, Index: 7, Value: 1}
	src.raw <- input.RawEvent{Time: 7, Device: 1, Kind: input.RawHat, Index: 0, Value: 4}
	for i, want := range []rpcRaw{
		{TimeNs: 5, Device: 1, Kind: "axis", Index: 2, Value: -32768},
		{TimeNs: 6, Device: 1, Kind: "button", Index: 7, Value: 1},
		{TimeNs: 7, Device: 1, Kind: "hat", Index: 0, Value: 4},
	} {
		select {
		case row := <-rows:
			var got rpcRaw
			if row.Topic != "input.raw" || json.Unmarshal(row.Data, &got) != nil || got != want {
				t.Errorf("event %d = %s %s, want input.raw %+v", i, row.Topic, row.Data, want)
			}
		case <-time.After(liveWait):
			t.Fatalf("no input.raw event %d", i)
		}
	}
	// watch:false ends it for this session.
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]any{"device": "1", "watch": false}, &info); err != nil || info.Watching {
		t.Fatalf("watch:false = %+v, %v", info, err)
	}
	tl.waitUntil(t, "RawStop", func() bool { return slices.Equal(src.rawStopped(), []input.DeviceID{1}) })
}

// TestRawWatchesEndWithTheirSession: a client that goes away without saying so
// gives its raw watches up, but another session's watch of the same
// controller stays.
func TestRawWatchesEndWithTheirSession(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.events <- mapPad(1, false)
	tl.sync()
	ctx := mappingCtx(t)
	a, b := rpcTestClient(t, tl.liveRun), rpcTestClient(t, tl.liveRun)
	for _, c := range []*jrpc2.Client{a, b} {
		if err := c.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	// a leaves; b still watches.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := src.rawStopped(); len(got) != 0 {
		t.Fatalf("RawStop called %v while another session watches", got)
	}
	if err := b.CallResult(ctx, "devices.rawInfo", map[string]any{"device": "1", "watch": false}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "RawStop", func() bool { return slices.Equal(src.rawStopped(), []input.DeviceID{1}) })
}

// TestRawInfoOfAMappedControllerNeedsTheCapture: the wizard presses buttons
// that the mapping would turn into keys, and the press of a gamepad that
// waits takes a slot over, so a gamepad is not asked for until the mapping is
// paused (capture.start); a joystick without a mapping needs nothing.
func TestRawInfoOfAMappedControllerNeedsTheCapture(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.events <- mapPad(1, true) // takes the free slot
	tl.sync()
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	err := client.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, new(any))
	if rpcCode(err) != rpcCodeMappingUnsupported {
		t.Fatalf("rawInfo of a mapped controller without the capture: %v (code %d)", err, rpcCode(err))
	}
	if err := client.CallResult(ctx, "capture.start", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	var info rpcRawInfo
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, &info); err != nil || !info.Watching {
		t.Fatalf("rawInfo with the capture = %+v, %v", info, err)
	}
	if err := client.CallResult(ctx, "capture.stop", nil, new(any)); err != nil {
		t.Fatal(err)
	}
}

func TestRawInfoNeedsADevice(t *testing.T) {
	tl, _ := startMappingLive(t, `{"schemaVersion":0}`, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx := mappingCtx(t)
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]string{}, new(any)); rpcCode(err) != int(jrpc2.InvalidParams) {
		t.Errorf("no device: %v", err)
	}
	if err := client.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "7"}, new(any)); rpcCode(err) != -32004 {
		t.Errorf("unknown device: %v", err)
	}
}

// TestLiveDevicesCarryTheMapping: the effective config has the mapping of a
// device key from config.json that was changed after the start, and keeps
// what only a restart applies.
func TestLiveDevicesCarryTheMapping(t *testing.T) {
	m := mapGUID + ",*,a:b0,platform:Windows"
	startup := &profile.Config{Devices: map[string]profile.DeviceSettings{mapGUID: {Ignored: true, Mapping: m}, "x": {Ignored: true}}}
	next := &profile.Config{Devices: map[string]profile.DeviceSettings{"x": {Ignored: false}, "y": {Mapping: m}}}
	got := liveDevices(startup.Devices, next.Devices)
	if got[mapGUID].Mapping != "" || !got[mapGUID].Ignored {
		t.Errorf("a mapping that config.json dropped: %+v", got[mapGUID])
	}
	if !got["x"].Ignored {
		t.Error("the ignored flag is the startup's")
	}
	if got["y"].Mapping != m {
		t.Errorf("a mapping that is new: %+v", got["y"])
	}
	if cfg := liveSettings(startup, next); profile.DeviceMappings(cfg.Devices)[0] != m {
		t.Errorf("DeviceMappings = %q", profile.DeviceMappings(cfg.Devices))
	}
	if s := startupFields(startup); len(s.Devices) != 2 || s.Devices[mapGUID].Mapping != "" {
		t.Errorf("startupFields keeps only the ignored flags: %+v", s.Devices)
	}
	_ = profilelib.Revision
}

// TestRawEventsGoOnlyToTheConnectionsThatAsked: the raw events of a controller
// are published to the connections that called devices.rawInfo for it (and
// subscribe to input.raw), not to every subscriber.
func TestRawEventsGoOnlyToTheConnectionsThatAsked(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.info = input.RawInfo{GUID: mapGUID, Axes: 1, Buttons: 4}
	src.events <- mapPad(1, false)
	src.events <- mapPad(2, false)
	tl.sync()
	ctx := mappingCtx(t)
	a, rowsA := rawSubscriber(t, tl, "input.raw")
	b, rowsB := rawSubscriber(t, tl, "input.raw")
	_, rowsC := rawSubscriber(t, tl, "input.raw") // subscribes, asks for nothing
	if err := a.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if err := b.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "2"}, new(any)); err != nil {
		t.Fatal(err)
	}
	src.raw <- input.RawEvent{Time: 1, Device: 1, Kind: input.RawButton, Index: 1, Value: 1}
	src.raw <- input.RawEvent{Time: 2, Device: 2, Kind: input.RawButton, Index: 2, Value: 1}
	expect := func(name string, rows <-chan ipc.Notification, device input.DeviceID) {
		t.Helper()
		select {
		case row := <-rows:
			var e rpcRaw
			if err := json.Unmarshal(row.Data, &e); err != nil || e.Device != device {
				t.Errorf("%s got %s, want an event of device %d", name, row.Data, device)
			}
		case <-time.After(liveWait):
			t.Fatalf("%s got no event", name)
		}
	}
	expect("A", rowsA, 1)
	expect("B", rowsB, 2)
	// Each got its own and no more; C asked for nothing.
	for name, rows := range map[string]<-chan ipc.Notification{"A": rowsA, "B": rowsB, "C": rowsC} {
		select {
		case row := <-rows:
			t.Errorf("%s got another event: %s", name, row.Data)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// TestInputTopicCarriesTheRemap: a client that follows the input topic keeps
// the state of every control, and SDL releases nothing when a mapping changes
// (IN-18), so it is told: a "remapped" row with the device as it is now, and
// the client resets what it holds for the device.
func TestInputTopicCarriesTheRemap(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.events <- mapPad(1, true)
	tl.sync()
	_, rows := rawSubscriber(t, tl, "input")
	info := &input.DeviceInfo{ID: 1, GUID: mapGUID, Name: "Cheap Pad", IsGamepad: true, ButtonMask: 0x15, AxisMask: 3}
	src.events <- input.Event{Time: 7, Device: 1, Kind: input.DeviceRemapped, Info: info}
	select {
	case row := <-rows:
		var got rpcInput
		if err := json.Unmarshal(row.Data, &got); err != nil || row.Topic != "input" ||
			got.Kind != "remapped" || got.Device != 1 || got.TimeNs != 7 || got.Info == nil || got.Info.ButtonMask != 0x15 || got.Info.AxisMask != 3 {
			t.Fatalf("row %s %s (%v), want a remapped row of device 1 with its masks", row.Topic, row.Data, err)
		}
	case <-time.After(liveWait):
		t.Fatal("no input row for the remap")
	}
}

// TestRawWatchSurvivesAnotherSessionLettingGo: two sessions watch a joystick.
// One lets go while the other asks for it: the device must keep sending for
// the one that asked, whichever of the two gets there first. The wizard and
// "monitor --raw" are two such sessions.
func TestRawWatchSurvivesAnotherSessionLettingGo(t *testing.T) {
	tl, src := startMappingLive(t, `{"schemaVersion":0}`, nil)
	src.info = input.RawInfo{GUID: mapGUID, Axes: 1, Buttons: 4}
	src.events <- mapPad(1, false)
	tl.sync()
	ctx := mappingCtx(t)
	a, b := rpcTestClient(t, tl.liveRun), rpcTestClient(t, tl.liveRun)
	if err := a.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	// The input is slow to stop the device: while a's RawStop runs, b asks.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	src.stopHook = func(input.DeviceID) { // only the first stop is slow
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	letGo := make(chan error, 1)
	go func() {
		letGo <- a.CallResult(ctx, "devices.rawInfo", map[string]any{"device": "1", "watch": false}, new(any))
	}()
	select {
	case <-entered:
	case <-time.After(liveWait):
		t.Fatal("a's RawStop was not called")
	}
	asked := make(chan error, 1)
	var info rpcRawInfo
	go func() {
		asked <- b.CallResult(ctx, "devices.rawInfo", map[string]string{"device": "1"}, &info)
	}()
	time.Sleep(100 * time.Millisecond) // time for b to get ahead of a, if it can
	close(release)
	for name, ch := range map[string]chan error{"a letting go": letGo, "b asking": asked} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(liveWait):
			t.Fatalf("%s did not return", name)
		}
	}
	if !info.Watching || !src.isWatching(1) {
		t.Errorf("b was told Watching=%v, and the input watches the device: %v; want both", info.Watching, src.isWatching(1))
	}
}
