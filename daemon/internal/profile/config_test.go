package profile

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

func TestConfigDefaults(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0}`))
	if ds.HasErrors() || c == nil {
		t.Fatal(ds)
	}
	if !c.Enabled || !c.RestoreLayerState || c.ReleaseOnSwitch != "profileChange" || len(c.Rules) != 0 {
		t.Fatalf("defaults: %+v", c)
	}
	c, ds = ParseConfig([]byte(`{"schemaVersion":0,"enabled":false,"restoreLayerState":false,"rules":[{"exe":"paint.exe","profile":"paint"}]}`))
	if ds.HasErrors() || c.Enabled || c.RestoreLayerState || !c.Rules[0].Enabled {
		t.Fatalf("explicit false/default rule: %+v %v", c, ds)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, data := range []string{
		`{}`, `null`, `{"schemaVersion":1}`, `{"schemaVersion":0,"enabled":null}`,
		`{"schemaVersion":0,"onUnmatched":"missing"}`,
		`{"schemaVersion":0,"enabled":true,"enabled":false}`,
		`{"schemaVersion":0,"rules":[{"exe":"paint.exe"}]}`,
		`{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"../escape"}]}`,
		`{"schemaVersion":0,"rules":[{"exe":"*.exe","profile":null}]}`,
		`{"schemaVersion":0,"rules":[{"exe":"relative/paint.exe","profile":"paint"}]}`,
		`{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint","device":"id:abc"}]}`,
		`{"schemaVersion":0,"ignore":[""]}`, `{"schemaVersion":0,"defaultProfile":""}`,
		`{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":""}]}`,
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["south","south"],"holdMs":2000}}}}`,
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["leftx+","south"],"holdMs":2000}}}}`,
		// UI-19 (ADR-0052): at least two buttons and 500 ms, so that one
		// button held a little long never pauses.
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":2000}}}}`,
		`{"schemaVersion":0,"safety":{"panic":{"joyconLeft":{"buttons":["back","start"],"holdMs":499}}}}`,
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":100}}}}`,
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":[],"holdMs":2000}}}}`,
		`{"schemaVersion":0,"timing":{"holdMs":-1}}`,
		`{"schemaVersion":0,"output":{"eventGapMs":-1}}`, `{"schemaVersion":0,"output":{"eventGapMs":51}}`,
		`{"schemaVersion":0,"output":{"eventGapMs":2.5}}`, `{"schemaVersion":0,"output":{"eventGapMs":"10"}}`,
		`{"schemaVersion":0,"output":{"eventGapMs":null}}`,
	} {
		t.Run(data, func(t *testing.T) {
			c, ds := ParseConfig([]byte(data))
			if c != nil || !ds.HasErrors() || ds[0].Line == 0 {
				t.Fatalf("accepted or unlocated: %+v %v", c, ds)
			}
		})
	}
}

// TestConfigPanicComboBounds: the smallest and largest emergency combos
// config.json accepts (UI-19, ADR-0052).
func TestConfigPanicComboBounds(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"safety":{"panic":{
		"standard":{"buttons":["back","start"],"holdMs":500},
		"joyconLeft":{"buttons":["south","east","west","north","leftshoulder","rightshoulder","back","start"],"holdMs":60000}
	}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	if got := c.Safety.Panic["standard"]; len(got.Buttons) != 2 || got.HoldMs != 500 {
		t.Fatalf("standard %+v", got)
	}
	if got := c.Safety.Panic["joyconLeft"]; len(got.Buttons) != 8 || got.HoldMs != 60000 {
		t.Fatalf("joyconLeft %+v", got)
	}
	for _, combo := range []string{
		`{"buttons":["back","start","south","east","west","north","leftshoulder","rightshoulder","guide"],"holdMs":2000}`,
		`{"buttons":["back","start"],"holdMs":60001}`,
	} {
		if _, ds := ParseConfig([]byte(`{"schemaVersion":0,"safety":{"panic":{"standard":` + combo + `}}}`)); !ds.HasErrors() {
			t.Errorf("accepted %s", combo)
		}
	}
}

// TestConfigSelection: the first enabled rule that names a profile wins;
// an application no rule binds gets the fixed default profile (PRO-28,
// ADR-0053).
func TestConfigSelection(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"rules":[
		{"exe":"paint.exe","profile":"off","enabled":false},
		{"exe":"C:\\Art\\paint.exe","profile":"paint"},
		{"exe":"paint.exe","profile":"other"},
		{"exe":"game.exe","profile":"game"}
	]}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	for _, tc := range []struct {
		path, current, want, reason string
		rule                        int
		release                     bool
	}{
		{`c:\Art\PAINT.EXE`, "old", "paint", "rule", 1, true},
		{`C:/Art/paint.exe`, "paint", "paint", "rule", 1, false},
		{`D:\Other\paint.exe`, "old", "other", "rule", 2, true},
		{`C:\game.exe`, "paint", "game", "rule", 3, true},
		{`C:\unmatched.exe`, "paint", DefaultProfileID, "default", -1, true},
		{`C:\unmatched.exe`, DefaultProfileID, DefaultProfileID, "default", -1, false},
		// The mapping disabled (profiles.activate null) is no profile: an
		// unbound application selects the default.
		{`C:\unmatched.exe`, "", DefaultProfileID, "default", -1, true},
	} {
		got := c.Select(tc.path, tc.current)
		if got.Profile != tc.want || got.Reason != tc.reason || got.Rule != tc.rule || got.Release != tc.release {
			t.Errorf("%s from %q: %+v", tc.path, tc.current, got)
		}
	}
	c.Enabled = false
	if got := c.Select(`C:\game.exe`, "paint"); got.Profile != "paint" || got.Reason != "disabled" || got.Release {
		t.Fatal(got)
	}
	if got := c.Select(`C:\unmatched.exe`, "paint"); got.Profile != "paint" || got.Release {
		t.Fatal("disabled selection fell back to the default", got)
	}
	c.Enabled, c.ReleaseOnSwitch = true, "always"
	if got := c.Select(`C:\Art\paint.exe`, "paint"); !got.Release {
		t.Fatal(got)
	}
	if got := c.Select(`C:\unmatched.exe`, DefaultProfileID); !got.Release {
		t.Fatal("always did not release in the default", got)
	}
	c.ReleaseOnSwitch = "never"
	if got := c.Select(`C:\unmatched.exe`, "paint"); !got.Release {
		t.Fatal("a profile change must release even with never", got)
	}
	if got := c.Select(`C:\unmatched.exe`, DefaultProfileID); got.Release {
		t.Fatal("never released in the same profile", got)
	}
}

// TestConfigLegacyMembersIgnored: config.json members that ADR-0053
// retired still load, so that an older file never stops padmated, but
// select nothing: defaultProfile, onUnmatched and ignore are dropped, and
// a rule with profile null is skipped, keeping its index.
func TestConfigLegacyMembersIgnored(t *testing.T) {
	for _, data := range []string{
		`{"schemaVersion":0,"defaultProfile":"paint","onUnmatched":"default","ignore":["explorer.exe","C:\\Games\\game.exe"]}`,
		`{"schemaVersion":0,"onUnmatched":"none"}`,
		`{"schemaVersion":0,"onUnmatched":"keep"}`,
		// Checks of the retired members are gone with them.
		`{"schemaVersion":0,"onUnmatched":"default"}`,
		`{"schemaVersion":0,"defaultProfile":"nul","ignore":["relative/game.exe"]}`,
	} {
		c, ds := ParseConfig([]byte(data))
		if ds.HasErrors() {
			t.Fatalf("%s: %v", data, ds)
		}
		for _, exe := range []string{`C:\Windows\explorer.exe`, `C:\Games\game.exe`, `C:\unmatched.exe`} {
			if got := c.Select(exe, "paint"); got.Profile != DefaultProfileID || got.Reason != "default" {
				t.Errorf("%s: %s selected %+v", data, exe, got)
			}
		}
	}
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"rules":[
		{"exe":"game.exe","profile":null},
		{"exe":"game.exe","profile":"game"},
		{"exe":"other.exe","profile":null}
	]}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	if len(c.Rules) != 3 || c.Rules[0].Profile != nil || c.Rules[2].Profile != nil {
		t.Fatalf("rules %+v", c.Rules)
	}
	if got := c.Select(`C:\game.exe`, "paint"); got.Profile != "game" || got.Rule != 1 {
		t.Fatalf("null rule not skipped: %+v", got)
	}
	if got := c.Select(`C:\other.exe`, "paint"); got.Profile != DefaultProfileID || got.Rule != -1 {
		t.Fatalf("null rule disabled mapping: %+v", got)
	}
}

func TestRuleMatches(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"paint.exe", `C:\Art\PAINT.EXE`, true},
		{"paint.exe", `C:/Art/paint.exe`, true},
		{`C:\Art\paint.exe`, `c:\art\Paint.exe`, true},
		{`C:\Art\paint.exe`, `D:\Art\paint.exe`, false},
		{"paint.exe", `C:\Art\paint2.exe`, false},
		{"paint.exe", "", false},
	} {
		// Enabled does not take part: Select checks it separately.
		if got := (Rule{Exe: tc.pattern}).Matches(tc.path); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestConfigTimingAndPanic(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"timing":{"holdMs":400,"multiTapMs":0},"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":3000}}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	p := &Profile{Timing: Timing{HoldMs: 250, HoldSet: true}}
	got := c.WithDefaults(p)
	if got == p || got.Timing.HoldMs != 250 || !got.Timing.MultiTapSet || got.Timing.MultiTapMs != 0 || p.Timing.MultiTapSet {
		t.Fatalf("merge/mutation: %+v / %+v", got, p)
	}
	if c.Safety.Panic["standard"].HoldMs != 3000 {
		t.Fatal(c.Safety)
	}
}

// TestConfigEventGap: output.eventGapMs (OUT-17) is 0 when left out and
// any whole number of milliseconds from 0 to 50.
func TestConfigEventGap(t *testing.T) {
	for _, tc := range []struct {
		data string
		want int
	}{
		{`{"schemaVersion":0}`, 0},
		{`{"schemaVersion":0,"output":{}}`, 0},
		{`{"schemaVersion":0,"output":{"eventGapMs":0}}`, 0},
		{`{"schemaVersion":0,"output":{"eventGapMs":10,"sendToOwnWindow":true}}`, 10},
		{`{"schemaVersion":0,"output":{"eventGapMs":50}}`, 50},
	} {
		c, ds := ParseConfig([]byte(tc.data))
		if ds.HasErrors() {
			t.Fatalf("%s: %v", tc.data, ds)
		}
		if c.Output.EventGapMs != tc.want {
			t.Errorf("%s: eventGapMs %d, want %d", tc.data, c.Output.EventGapMs, tc.want)
		}
	}
}

func TestConfigSchemaCurrent(t *testing.T) {
	data, err := os.ReadFile("../../../schema/config.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, ConfigSchema()) {
		t.Fatal("run task gen")
	}
	if _, err := compiledConfigSchema(); err != nil {
		t.Fatal(err)
	}
}

func FuzzConfig(f *testing.F) {
	for _, seed := range []string{
		`{"schemaVersion":0}`, `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":null}]}`,
		`{"schemaVersion":1,"rules":[{"exe":"a/b.exe","profile":"x"}],"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":1}}}}`,
		`{"timing":{"holdMs":-1},"bogus":[1,{"a":"é"}],"devices":{"k":{"ignored":"yes"}}}`,
		`{"schemaVersion":0,"devices":{"k":{"ignored":true,"calibration":{"ls":{"deadzone":0.5,"saturation":0.4},"rs":{"deadzone":2},"lt":{}}}}}`,
		`{"schemaVersion":0,"timing":{"holdMs":1E1000001}}`, `{"schemaVersion":0,"timing":{"holdMs":1E-1000001}}`, `{"schemaVersion":0,"timing":{"holdMs":1E400}}`,
		`{"schemaVersion":0,"timing":{"holdMs":1e2,"tapGapMs":2.0},"enabled":false}`, `{"schemaVersion":0,"output":{"eventGapMs":99999999999999999999}}`,
		`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":1e3}}}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Every repair parses (PRO-29); a valid file keeps its settings.
		fixed, repair := RepairConfig(data, func(key string) bool { return !strings.Contains(key, "x") })
		rc, rds := ParseConfig(fixed)
		if rds.HasErrors() {
			t.Fatalf("repair %q of %q: %v", fixed, data, rds)
		}
		for key := range rc.Devices {
			if strings.Contains(key, "x") {
				t.Fatalf("repair %q kept device %q", fixed, key)
			}
		}
		c, ds := ParseConfig(data)
		if !ds.HasErrors() && !strings.Contains(string(data), `x`) && (repair.Reset || len(repair.Members) != 0 || !reflect.DeepEqual(c, rc)) {
			t.Fatalf("valid %q repaired: %q %+v", data, fixed, repair)
		}
		if ds.HasErrors() {
			if c != nil {
				t.Fatal("partial config")
			}
			return
		}
		if c == nil {
			t.Fatal("nil without error")
		}
		got := c.Select(`C:\game.exe`, "paint")
		if strings.Contains(got.Profile, "/") {
			t.Fatal("unsafe selection")
		}
	})
}

// TestConfigFeedbackSwitches: config.json feedback holds the four global
// feedback switches (UI-27, ADR-0067). Each is on unless it says false, the
// members are independent, and a missing object is all on.
func TestConfigFeedbackSwitches(t *testing.T) {
	all := DefaultFeedbackSwitches()
	if !all.Rumble || !all.LED || !all.Pause || !all.LowBattery {
		t.Fatalf("DefaultFeedbackSwitches = %+v; every switch is on by default", all)
	}
	for _, tc := range []struct {
		doc  string
		want func(*FeedbackSwitches)
	}{
		{`{"schemaVersion":0}`, func(*FeedbackSwitches) {}},
		{`{"schemaVersion":0,"feedback":{}}`, func(*FeedbackSwitches) {}},
		{`{"schemaVersion":0,"feedback":{"rumble":false}}`, func(f *FeedbackSwitches) { f.Rumble = false }},
		{`{"schemaVersion":0,"feedback":{"led":false}}`, func(f *FeedbackSwitches) { f.LED = false }},
		{`{"schemaVersion":0,"feedback":{"pause":false}}`, func(f *FeedbackSwitches) { f.Pause = false }},
		{`{"schemaVersion":0,"feedback":{"lowBattery":false}}`, func(f *FeedbackSwitches) { f.LowBattery = false }},
		{`{"schemaVersion":0,"feedback":{"rumble":true,"led":true,"pause":true,"lowBattery":true}}`, func(*FeedbackSwitches) {}},
		{`{"schemaVersion":0,"feedback":{"rumble":false,"led":false,"pause":false,"lowBattery":false}}`, func(f *FeedbackSwitches) { *f = FeedbackSwitches{} }},
	} {
		c, ds := ParseConfig([]byte(tc.doc))
		if c == nil || ds.HasErrors() {
			t.Fatalf("%s: config=%v diagnostics=%v", tc.doc, c, ds)
		}
		want := DefaultFeedbackSwitches()
		tc.want(&want)
		if c.Feedback != want {
			t.Errorf("%s: Feedback = %+v; want %+v", tc.doc, c.Feedback, want)
		}
	}
	if got := DefaultConfig().Feedback; got != DefaultFeedbackSwitches() {
		t.Errorf("DefaultConfig().Feedback = %+v; want every switch on", got)
	}
	for _, doc := range []string{
		`{"schemaVersion":0,"feedback":{"rumble":"no"}}`, `{"schemaVersion":0,"feedback":{"led":0}}`,
		`{"schemaVersion":0,"feedback":{"pause":null}}`, `{"schemaVersion":0,"feedback":{"lowBattery":1}}`,
		`{"schemaVersion":0,"feedback":{"strength":1}}`, `{"schemaVersion":0,"feedback":true}`,
	} {
		if c, ds := ParseConfig([]byte(doc)); c != nil || !ds.HasErrors() {
			t.Errorf("%s: accepted: config=%v diagnostics=%v", doc, c, ds)
		}
	}
}

// TestConfigFunctionKey: config.json functionKey picks the one function key
// (MAP-27, ADR-0075): start unless it says back or guide. Anything else, in
// type or value, is a schema error that the repair (PRO-29) resets.
func TestConfigFunctionKey(t *testing.T) {
	if got := DefaultConfig().FunctionKey; got != control.Start {
		t.Errorf("DefaultConfig().FunctionKey = %s; want start", got)
	}
	for _, tc := range []struct {
		doc  string
		want control.Control
	}{
		{`{"schemaVersion":0}`, control.Start},
		{`{"schemaVersion":0,"functionKey":"start"}`, control.Start},
		{`{"schemaVersion":0,"functionKey":"back"}`, control.Back},
		{`{"schemaVersion":0,"functionKey":"guide"}`, control.Guide},
	} {
		c, ds := ParseConfig([]byte(tc.doc))
		if c == nil || ds.HasErrors() {
			t.Fatalf("%s: config=%v diagnostics=%v", tc.doc, c, ds)
		}
		if c.FunctionKey != tc.want || c.FunctionKeyOrDefault() != tc.want {
			t.Errorf("%s: FunctionKey = %s, FunctionKeyOrDefault = %s; want %s", tc.doc, c.FunctionKey, c.FunctionKeyOrDefault(), tc.want)
		}
	}
	// A Config built by hand has no key set, and means the default.
	if got := (&Config{}).FunctionKeyOrDefault(); got != control.Start {
		t.Errorf("zero Config: %s", got)
	}
	var none *Config
	if got := none.FunctionKeyOrDefault(); got != control.Start {
		t.Errorf("nil Config: %s", got)
	}
	for _, doc := range []string{
		`{"schemaVersion":0,"functionKey":"select"}`, `{"schemaVersion":0,"functionKey":"south"}`,
		`{"schemaVersion":0,"functionKey":"Start"}`, `{"schemaVersion":0,"functionKey":""}`,
		`{"schemaVersion":0,"functionKey":null}`, `{"schemaVersion":0,"functionKey":1}`,
		`{"schemaVersion":0,"functionKey":["start"]}`, `{"schemaVersion":0,"functionKey":"start","functionKey":"back"}`,
	} {
		c, ds := ParseConfig([]byte(doc))
		if c != nil || !ds.HasErrors() || ds[0].Line == 0 {
			t.Errorf("%s: accepted or unlocated: config=%v diagnostics=%v", doc, c, ds)
		}
	}
}
