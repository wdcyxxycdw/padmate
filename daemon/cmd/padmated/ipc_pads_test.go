package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// These tests are the daemon half of the settings of each controller
// (ADR-0017): pads.list, pads.set and pads.migrate, and the list of the
// controllers that have been connected in state.json.

// padGUID lays out an SDL GUID: bus, CRC of the name, vendor, 0, product, 0.
func padGUID(bus, vendor, product uint16) string {
	return fmt.Sprintf("%02x%02x0000%02x%02x0000%02x%02x000000000000", bus&0xff, bus>>8, vendor&0xff, vendor>>8, product&0xff, product>>8)
}

var (
	padXboxGUID   = padGUID(0x03, 0x045e, 0x0b13)
	padXboxKey    = padXboxGUID + "/s:XB1"
	padJoyConGUID = padGUID(0x05, 0x057e, 0x2066)
	padJoyConKey  = padJoyConGUID + "/s:XKW2"
	padOldKey     = padGUID(0x03, 0x054c, 0x0ce6) + "/s:OLD"
)

func buttonMaskOf(names []string) uint32 {
	var mask uint32
	for b := range uint8(32) {
		if slices.Contains(names, input.ButtonName(b)) {
			mask |= 1 << b
		}
	}
	return mask
}

func padAdded(id input.DeviceID, guid, serial, name, typ string, buttons []string) input.Event {
	return input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{
		ID: id, GUID: guid, Serial: serial, Name: name, Type: typ, IsGamepad: true, ButtonMask: buttonMaskOf(buttons),
	}}
}

// The owner's configuration before ADR-0017: the global members are set, and
// a controller that is not connected has calibration.
var legacyPadConfig = `{
  "schemaVersion": 0,
  "functionKey": "back",
  "feedback": { "rumble": false },
  "safety": { "panic": { "standard": { "buttons": ["back", "south"], "holdMs": 1500 } } },
  "devices": { "` + padOldKey + `": { "calibration": { "ls": { "deadzone": 0.2 } } } }
}`

func startPadsLive(t *testing.T, config string) (*testLive, *mappingFake, *rpcTestConn) {
	t.Helper()
	tl, src := startMappingLive(t, config, nil)
	src.events <- padAdded(1, padXboxGUID, "XB1", "Xbox Wireless Controller", "xboxone", []string{"south", "east", "west", "north", "back", "start", "leftshoulder", "rightshoulder", "leftstick", "rightstick"})
	src.events <- padAdded(2, padJoyConGUID, "XKW2", "Joy-Con 2 (R)", "joyconright", inputtest.JoyConRight("r").Buttons)
	tl.sync()
	return tl, src, &rpcTestConn{tl: tl, client: rpcTestClient(t, tl.liveRun), ctx: mappingCtx(t), t: t}
}

type rpcTestConn struct {
	tl     *testLive
	client *jrpc2.Client
	ctx    context.Context
	t      *testing.T
}

func (c *rpcTestConn) call(method string, params any, result any) error {
	c.t.Helper()
	return c.client.CallResult(c.ctx, method, params, result)
}

func (c *rpcTestConn) pads() rpcPads {
	c.t.Helper()
	var res rpcPads
	if err := c.call("pads.list", nil, &res); err != nil {
		c.t.Fatal(err)
	}
	return res
}

func (c *rpcTestConn) pad(key string) rpcPad {
	c.t.Helper()
	for _, p := range c.pads().Pads {
		if p.Key == key {
			return p
		}
	}
	c.t.Fatalf("pads.list has no %s", key)
	return rpcPad{}
}

func (c *rpcTestConn) config() *profile.Config {
	c.t.Helper()
	var doc rpcDocument
	if err := c.call("config.get", nil, &doc); err != nil {
		c.t.Fatal(err)
	}
	cfg, ds := profile.ParseConfig([]byte(doc.Text))
	if cfg == nil {
		c.t.Fatalf("config.json does not parse: %v\n%s", ds, doc.Text)
	}
	return cfg
}

func options2(p rpcPad) []string {
	var out []string
	for _, o := range p.FunctionKey.Options {
		out = append(out, o.Control+"="+o.Label)
	}
	return out
}

// TestPadsList: the controllers that are connected come first, with the
// family's function keys and the settings as they are resolved (the retired
// top-level members still apply where they do), and a controller that only
// config.json mentions follows.
func TestPadsList(t *testing.T) {
	_, _, c := startPadsLive(t, legacyPadConfig)
	res := c.pads()
	if !res.Legacy.Any || !res.Legacy.FunctionKey || !res.Legacy.Feedback || !res.Legacy.Panic {
		t.Errorf("legacy = %+v", res.Legacy)
	}
	if len(res.Pads) != 3 || res.Pads[0].Key != padXboxKey || res.Pads[1].Key != padJoyConKey || res.Pads[2].Key != padOldKey {
		t.Fatalf("pads = %+v; want the connected ones in connection order, then the one config.json has", res.Pads)
	}
	if res.Revision == "" {
		t.Error("no revision")
	}

	x := res.Pads[0]
	if !x.Known || !x.Connected || x.Device == nil || *x.Device != 1 || x.Name != "Xbox Wireless Controller" || x.Type != "xboxone" || x.Family != "standard" || x.LastSeen == 0 {
		t.Errorf("xbox = %+v", x)
	}
	if got := options2(x); !slices.Equal(got, []string{"start=Menu", "back=View"}) {
		t.Errorf("xbox function keys = %v", got)
	}
	// The retired functionKey back applies to a controller with the standard layout.
	if f := x.FunctionKey; f.Default != "start" || f.Effective != "back" || f.Source != "legacy" || f.Stored != "" {
		t.Errorf("xbox function key = %+v", f)
	}
	if f := x.Feedback; f.Effective.Rumble || !f.Effective.LED || f.Source != "legacy" || f.Stored != nil {
		t.Errorf("xbox feedback = %+v", f)
	}
	if p := x.Panic; strings.Join(p.Effective.Buttons, "+") != "back+south" || p.Effective.HoldMs != 1500 || p.Source != "legacy" || strings.Join(p.Default.Buttons, "+") != "back+start" || p.Stored != nil {
		t.Errorf("xbox panic = %+v", p)
	}

	j := res.Pads[1]
	if j.Family != "joyconRight" || j.Type != "joyconright" {
		t.Errorf("joycon = %+v", j)
	}
	// Held sideways its ZR is the default and R the other; the retired
	// functionKey does not apply to a Joy-Con.
	if got := options2(j); !slices.Equal(got, []string{"paddle3=ZR", "paddle1=R"}) {
		t.Errorf("joycon function keys = %v", got)
	}
	if f := j.FunctionKey; f.Default != "paddle3" || f.Effective != "paddle3" || f.Source != "default" {
		t.Errorf("joycon function key = %+v", f)
	}
	if p := j.Panic; strings.Join(p.Effective.Buttons, "+") != "leftshoulder+rightshoulder" || p.Source != "default" {
		t.Errorf("joycon panic = %+v; the standard entry is not a Joy-Con's", p)
	}
	if j.Feedback.Effective.Rumble {
		t.Errorf("joycon feedback = %+v; the retired feedback applies to every controller", j.Feedback)
	}

	old := res.Pads[2]
	if old.Known || old.Connected || old.Device != nil || old.Name != "" || old.Type != "unknown" || old.Family != "standard" || old.LastSeen != 0 {
		t.Errorf("config-only pad = %+v", old)
	}
}

// TestPadsListOfAFreshInstall: no controller and no settings is an empty list.
func TestPadsListOfAFreshInstall(t *testing.T) {
	_, _, c := startPadsLive(t, `{"schemaVersion":0}`)
	res := c.pads()
	if res.Legacy.Any || len(res.Pads) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, p := range res.Pads {
		if p.FunctionKey.Source != "default" || p.Feedback.Source != "default" || p.Panic.Source != "default" {
			t.Errorf("%s: %+v", p.Key, p)
		}
	}
	if c.pad(padXboxKey).FunctionKey.Effective != "start" || c.pad(padJoyConKey).FunctionKey.Effective != "paddle3" {
		t.Error("family defaults")
	}
}

// TestPadsSet: one controller's settings are written into its entry of
// devices, validated against its family, and read back.
func TestPadsSet(t *testing.T) {
	_, _, c := startPadsLive(t, legacyPadConfig)
	var res rpcPadSet
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "functionKey": "paddle1"}, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.Revision == "" || res.Pad.Key != padJoyConKey || res.Pad.FunctionKey.Effective != "paddle1" || res.Pad.FunctionKey.Source != "device" || res.Pad.FunctionKey.Stored != "paddle1" {
		t.Fatalf("result = %+v", res)
	}
	cfg := c.config()
	if cfg.Devices[padJoyConKey].FunctionKey != "paddle1" {
		t.Errorf("config.json = %+v", cfg.Devices[padJoyConKey])
	}
	// The retired members and the other controllers are as they were.
	if !cfg.Legacy.Any() || cfg.FunctionKey.String() != "back" || cfg.Devices[padOldKey].Calibration.IsEmpty() {
		t.Errorf("config.json lost something: %+v", cfg)
	}

	// Feedback and the combo; a switch the file sets to true beats the
	// retired one that is false.
	if err := c.call("pads.set", map[string]any{"key": padXboxKey,
		"feedback": map[string]any{"rumble": true, "led": false},
		"panic":    map[string]any{"buttons": []string{"start", "north"}, "holdMs": 700}}, &res); err != nil {
		t.Fatal(err)
	}
	x := c.pad(padXboxKey)
	if !x.Feedback.Effective.Rumble || x.Feedback.Effective.LED || x.Feedback.Source != "device" || x.Feedback.Stored == nil || x.Feedback.Stored.Pause != nil {
		t.Errorf("xbox feedback = %+v", x.Feedback)
	}
	if strings.Join(x.Panic.Effective.Buttons, "+") != "start+north" || x.Panic.Effective.HoldMs != 700 || x.Panic.Source != "device" || x.Panic.Stored == nil {
		t.Errorf("xbox panic = %+v", x.Panic)
	}
	// null takes the controller's own value away: the retired one applies again.
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "feedback": nil, "panic": nil}, &res); err != nil {
		t.Fatal(err)
	}
	x = c.pad(padXboxKey)
	if x.Feedback.Effective.Rumble || x.Feedback.Source != "legacy" || x.Feedback.Stored != nil || x.Panic.Source != "legacy" || x.Panic.Stored != nil {
		t.Errorf("xbox after null = %+v", x)
	}
	if _, ok := c.config().Devices[padXboxKey]; ok {
		t.Error("an entry with no member is left in config.json")
	}
	// A repeat of what is stored changes nothing.
	before := c.pads().Revision
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "functionKey": "paddle1"}, &res); err != nil || res.Revision != before {
		t.Errorf("a repeat: revision %s → %s, %v", before, res.Revision, err)
	}
	// An empty request changes nothing either.
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey}, &res); err != nil || res.Revision != before {
		t.Errorf("an empty request: %v", err)
	}
}

func TestPadsSetRefuses(t *testing.T) {
	_, _, c := startPadsLive(t, legacyPadConfig)
	before := c.pads().Revision
	for _, tc := range []struct {
		name   string
		params map[string]any
		code   int
	}{
		{"no key", map[string]any{"functionKey": "back"}, -32602},
		{"a key that is no DeviceKey", map[string]any{"key": "nope", "functionKey": "back"}, -32602},
		{"a controller never seen", map[string]any{"key": padGUID(0x03, 0x1234, 0x5678) + "/s:NEW", "functionKey": "back"}, -32004},
		{"minus on a right Joy-Con held sideways", map[string]any{"key": padJoyConKey, "functionKey": "back"}, -32602},
		{"a paddle on a pad with the standard layout", map[string]any{"key": padXboxKey, "functionKey": "paddle1"}, -32602},
		{"guide", map[string]any{"key": padXboxKey, "functionKey": "guide"}, -32602},
		{"an alias", map[string]any{"key": padXboxKey, "functionKey": "select"}, -32602},
		{"a number", map[string]any{"key": padXboxKey, "functionKey": 5}, -32602},
		{"an unknown switch", map[string]any{"key": padXboxKey, "feedback": map[string]any{"sound": true}}, -32602},
		{"a switch that is no bool", map[string]any{"key": padXboxKey, "feedback": map[string]any{"rumble": "no"}}, -32602},
		{"a combo of one button", map[string]any{"key": padXboxKey, "panic": map[string]any{"buttons": []string{"back"}, "holdMs": 2000}}, -32602},
		{"a combo held too short", map[string]any{"key": padXboxKey, "panic": map[string]any{"buttons": []string{"back", "start"}, "holdMs": 100}}, -32602},
		{"a combo with a member too many", map[string]any{"key": padXboxKey, "panic": map[string]any{"buttons": []string{"back", "start"}, "holdMs": 2000, "x": 1}}, -32602},
		{"a combo with no buttons member", map[string]any{"key": padXboxKey, "panic": map[string]any{"holdMs": 2000}}, -32602},
		{"an unknown member", map[string]any{"key": padXboxKey, "ignored": true}, -32602},
		{"a stale revision", map[string]any{"key": padXboxKey, "functionKey": "back", "revision": "0000"}, -32009},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.call("pads.set", tc.params, new(any))
			if err == nil || rpcCode(err) != tc.code {
				t.Fatalf("err = %v, code %d; want %d", err, rpcCode(err), tc.code)
			}
		})
	}
	if got := c.pads().Revision; got != before {
		t.Error("a refused request changed config.json")
	}
	// A right revision is accepted.
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "functionKey": "start", "revision": before}, new(any)); err != nil {
		t.Fatal(err)
	}
}

// TestPadsSetAppliesAtOnce: the effective config the daemon runs with carries
// the controller settings.
func TestPadsSetAppliesAtOnce(t *testing.T) {
	tl, _, c := startPadsLive(t, legacyPadConfig)
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "feedback": map[string]any{"led": false}}, new(any)); err != nil {
		t.Fatal(err)
	}
	// The effective config the daemon runs with carries the controller's
	// settings (liveSettings), for the runtime to read.
	got := tl.selectionConfig().Devices[padJoyConKey]
	if got.Feedback == nil || got.Feedback.Led == nil || *got.Feedback.Led {
		t.Errorf("effective config = %+v", got)
	}
	if !tl.selectionConfig().Legacy.Any() {
		t.Error("the effective config lost Legacy")
	}
}

// TestPadsMigrate: the retired members are folded into the controllers the
// daemon knows and taken out, and every controller behaves as before.
func TestPadsMigrate(t *testing.T) {
	_, _, c := startPadsLive(t, legacyPadConfig)
	before := c.pads()
	var res rpcPadMigrated
	if err := c.call("pads.migrate", nil, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Applied || res.Legacy.Any || res.Revision == before.Revision {
		t.Fatalf("result = %+v", res.rpcSaved)
	}
	cfg := c.config()
	if cfg.Legacy.Any() {
		t.Errorf("config.json still has %+v", cfg.Legacy)
	}
	if d := cfg.Devices[padXboxKey]; d.FunctionKey != "back" || d.Feedback == nil || *d.Feedback.Rumble || d.Panic == nil || d.Panic.HoldMs != 1500 {
		t.Errorf("xbox entry = %+v", d)
	}
	// A Joy-Con keeps its family's function key: only the feedback switch.
	if d := cfg.Devices[padJoyConKey]; d.FunctionKey != "" || d.Feedback == nil || *d.Feedback.Rumble || d.Panic != nil {
		t.Errorf("joycon entry = %+v", d)
	}
	if cfg.Devices[padOldKey].Calibration.IsEmpty() {
		t.Error("the calibration went")
	}
	// What each controller does has not changed.
	for _, p := range before.Pads {
		a := c.pad(p.Key)
		if a.FunctionKey.Effective != p.FunctionKey.Effective || a.Feedback.Effective != p.Feedback.Effective ||
			!slices.Equal(a.Panic.Effective.Buttons, p.Panic.Effective.Buttons) || a.Panic.Effective.HoldMs != p.Panic.Effective.HoldMs {
			t.Errorf("%s: before %+v, after %+v", p.Key, p, a)
		}
		if a.FunctionKey.Source == "legacy" || a.Feedback.Source == "legacy" || a.Panic.Source == "legacy" {
			t.Errorf("%s: a legacy source is left: %+v", p.Key, a)
		}
	}
	// Once is enough.
	if err := c.call("pads.migrate", nil, &res); err != nil || res.Changed || res.Revision != c.pads().Revision {
		t.Errorf("second migration: %+v, %v", res, err)
	}
	if err := c.call("pads.migrate", map[string]any{"revision": "0000"}, new(any)); err == nil || rpcCode(err) != -32009 {
		t.Errorf("stale revision: %v", err)
	}
}

// TestPadsRememberedInState: a connected controller reaches state.json through
// the persistence worker, with what the list needs, and the list shows it when
// it is gone.
func TestPadsRememberedInState(t *testing.T) {
	tl, src, c := startPadsLive(t, `{"schemaVersion":0}`)
	lib := profilelib.New(tl.cfg.opts.dataDir)
	tl.waitUntil(t, "state.json to list the controllers", func() bool {
		state, err := lib.ReadState()
		return err == nil && len(state.Controllers) == 2
	})
	state, _ := lib.ReadState()
	k := state.Controllers[padJoyConKey]
	if k.Name != "Joy-Con 2 (R)" || k.Type != "joyconright" || k.LastSeen == 0 || uint32(k.ButtonMask) != buttonMaskOf(inputtest.JoyConRight("r").Buttons) {
		t.Errorf("state.json holds %+v", k)
	}
	// Gone, it stays in the list, after the connected ones.
	src.events <- input.Event{Kind: input.DeviceRemoved, Device: 2}
	tl.sync()
	pads := c.pads().Pads
	if len(pads) != 2 || pads[0].Key != padXboxKey || !pads[0].Connected || pads[1].Key != padJoyConKey || pads[1].Connected || !pads[1].Known || pads[1].Family != "joyconRight" {
		t.Fatalf("pads = %+v", pads)
	}
	if got := options2(pads[1]); !slices.Equal(got, []string{"paddle3=ZR", "paddle1=R"}) {
		t.Errorf("function keys of a controller that is gone = %v", got)
	}
	// Its settings can be written while it is away.
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "functionKey": "paddle1"}, new(any)); err != nil {
		t.Fatal(err)
	}
}
