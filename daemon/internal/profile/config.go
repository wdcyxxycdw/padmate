package profile

import (
	"bytes"
	_ "embed" // config.schema.json
	"errors"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

//go:generate go run ../../tools/schemacopy -src ../../../schema/config.schema.json -dst config.schema.json

//go:embed config.schema.json
var configSchemaJSON []byte

// DefaultProfileID is the library ID of the fixed default profile (PRO-28,
// ADR-0053): automatic selection uses it for every application no rule
// binds. The library creates it, with no mapping, when it is missing; it
// cannot be deleted or renamed, but it can be edited like any profile.
const DefaultProfileID = "default"

// Config contains global settings. Enabled controls automatic selection only;
// the user's pause and system suspension are separate runtime state.
//
// config.json members that ADR-0053 retired (defaultProfile, onUnmatched,
// ignore) still parse, so older files load, but Config does not keep them.
type Config struct {
	Enabled           bool
	ReleaseOnSwitch   string
	RestoreLayerState bool
	Rules             []Rule
	Devices           map[string]DeviceSettings
	Timing            Timing
	Output            OutputSettings
	Safety            SafetySettings
	// Feedback holds the four controller feedback switches (UI-27, ADR-0067),
	// global for every profile and controller.
	Feedback FeedbackSwitches
}

// FeedbackSwitches are the switches of config.json's feedback object: rumble
// (the pulses, the haptics of bindings and knob ticks, the low-battery buzz,
// and rumble from scripts), led (layer and pause colors, the player lights,
// the low-battery flash, and lights from scripts), pause (the pause indication,
// which shows only through rumble and led) and lowBattery (the low-battery
// alert, whose controller part also needs rumble and led). Each is on unless
// config.json says false. The profile's feedback.strength of 0 still silences
// everything padmate makes by itself.
type FeedbackSwitches struct {
	Rumble     bool
	LED        bool
	Pause      bool
	LowBattery bool
}

// DefaultFeedbackSwitches is every switch on.
func DefaultFeedbackSwitches() FeedbackSwitches {
	return FeedbackSwitches{Rumble: true, LED: true, Pause: true, LowBattery: true}
}

// Rule binds an application to a profile. A nil Profile is a rule an older
// file wrote to disable mapping; selection ignores it, but it keeps its
// index, which references such as "rules/3" count.
type Rule struct {
	Exe     string  `json:"exe"`
	Profile *string `json:"profile"`
	Enabled bool    `json:"enabled"`
}

// Matches reports whether the rule's exe pattern matches the executable
// path exe as Select does, ignoring Enabled: a bare file name matches the
// path's base name, an absolute path the whole path, both
// case-insensitively.
func (r Rule) Matches(exe string) bool { return matchExe(r.Exe, exe) }

// DefaultConfig is the config of a missing config.json: automatic selection
// on, without rules, so every application uses the fixed default profile.
func DefaultConfig() *Config {
	return &Config{Enabled: true, RestoreLayerState: true, ReleaseOnSwitch: "profileChange", Feedback: DefaultFeedbackSwitches()}
}

func ConfigSchema() []byte { return bytes.Clone(configSchemaJSON) }

var compiledConfigSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(configSchemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	if err := c.AddResource("urn:padmate:config.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("urn:padmate:config.schema.json")
})

// ParseConfig shares the strict JSON scanner and diagnostic coordinates with
// profiles. Schema defaults are applied explicitly; null is not an omission.
func ParseConfig(data []byte) (*Config, Diagnostics) {
	root, scanErr := scan(data)
	if scanErr != nil {
		return nil, place(data, Diagnostics{{Severity: SeverityError, Offset: scanErr.off, Message: scanErr.msg}})
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		return nil, place(data, Diagnostics{internalError(err)})
	}
	if err := schema.Validate(root.val); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return nil, place(data, Diagnostics{internalError(err)})
		}
		var ds Diagnostics
		for _, e := range leaves(ve, nil) {
			n, ptr := (&flattener{root: root}).at(e.InstanceLocation)
			ds = append(ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: n.start, Message: fallbackMessage(e)})
		}
		return nil, place(data, ds)
	}
	var w wireConfig
	if ds := decodeWire(data, root, &w); len(ds) > 0 {
		return nil, place(data, ds)
	}
	c := DefaultConfig()
	if w.Enabled != nil {
		c.Enabled = *w.Enabled
	}
	if w.RestoreLayerState != nil {
		c.RestoreLayerState = *w.RestoreLayerState
	}
	if w.ReleaseOnSwitch != "" {
		c.ReleaseOnSwitch = w.ReleaseOnSwitch
	}
	c.Devices = w.Devices
	c.Timing, c.Output, c.Safety = resolveTiming(w.Timing), w.Output, w.Safety
	if f := w.Feedback; f != nil {
		if f.Rumble != nil {
			c.Feedback.Rumble = *f.Rumble
		}
		if f.Led != nil {
			c.Feedback.LED = *f.Led
		}
		if f.Pause != nil {
			c.Feedback.Pause = *f.Pause
		}
		if f.LowBattery != nil {
			c.Feedback.LowBattery = *f.LowBattery
		}
	}
	var ds Diagnostics
	fail := func(ptr, msg string) {
		n := root.lookupPointer(ptr)
		if n == nil {
			n = root
		}
		ds = append(ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: n.start, Message: msg})
	}
	// The retired members only need the shape the schema checks: nothing
	// reads them any more.
	for i, rule := range w.Rules {
		ptr := childPointer("/rules", strconv.Itoa(i))
		if !validExe(rule.Exe) {
			fail(ptr+"/exe", "expected an exe name or absolute Windows path")
		}
		if rule.Profile != nil && !ValidLibraryID(*rule.Profile) {
			fail(ptr+"/profile", "invalid profile library id")
		}
		on := rule.Enabled == nil || *rule.Enabled
		c.Rules = append(c.Rules, Rule{Exe: rule.Exe, Profile: rule.Profile, Enabled: on})
	}
	checkCalibration(c.Devices, fail)
	checkMappings(c.Devices, fail)
	for family, combo := range c.Safety.Panic {
		for i, name := range combo.Buttons {
			ctl, ok := control.Parse(name)
			if !ok || ctl.String() != name || ctl.IsVirtual() {
				fail(childPointer(childPointer("/safety/panic", family)+"/buttons", strconv.Itoa(i)), "expected a canonical physical button name")
			}
		}
	}
	if ds.HasErrors() {
		return nil, place(data, ds)
	}
	return c, nil
}

// Selection includes the release decision for a debounced, non-shell
// foreground change. Reason is "rule" (Rule is its index), "default" (no
// rule binds the application) or "disabled" (automatic selection is off,
// Profile is current).
type Selection struct {
	Profile string
	Reason  string
	Rule    int
	Release bool
}

// Select evaluates rules in file order: the first enabled rule that matches
// exe and names a profile selects it, and an application no rule binds
// gets DefaultProfileID (ADR-0053). A disabled automatic selector preserves
// the current profile without releasing it.
func (c *Config) Select(exe, current string) Selection {
	r := Selection{Profile: current, Reason: "disabled", Rule: -1}
	if !c.Enabled {
		return r
	}
	r.Profile, r.Reason = DefaultProfileID, "default"
	for i, rule := range c.Rules {
		if rule.Enabled && rule.Profile != nil && matchExe(rule.Exe, exe) {
			r.Profile, r.Reason, r.Rule = *rule.Profile, "rule", i
			break
		}
	}
	r.Release = r.Profile != current || c.ReleaseOnSwitch == "always"
	return r
}

func normalizeExe(s string) string {
	return path.Clean(strings.ToLower(strings.ReplaceAll(s, `\`, "/")))
}

func validExe(s string) bool {
	if strings.TrimSpace(s) != s || s == "" || strings.ContainsAny(s, "\x00*?<>|\"") {
		return false
	}
	s = strings.ReplaceAll(s, `\`, "/")
	if !strings.Contains(s, "/") {
		return !strings.Contains(s, ":") && s != "." && s != ".."
	}
	if strings.HasSuffix(s, "/") {
		return false
	}
	return len(s) > 3 && s[1] == ':' && s[2] == '/' && (s[0] >= 'A' && s[0] <= 'Z' || s[0] >= 'a' && s[0] <= 'z') || strings.HasPrefix(s, "//") && len(strings.Split(s[2:], "/")) >= 3
}

func matchExe(pattern, exe string) bool {
	if exe == "" {
		return false
	}
	pattern, exe = normalizeExe(pattern), normalizeExe(exe)
	if !strings.Contains(pattern, "/") {
		exe = path.Base(exe)
	}
	return pattern == exe
}

// WithDefaults returns a shallow profile copy with global timing beneath its
// explicit overrides. Nested profile data remains immutable and shared.
func (c *Config) WithDefaults(p *Profile) *Profile {
	copyProfile := *p
	t := &copyProfile.Timing
	if !t.HoldSet && t.HoldMs == 0 {
		t.HoldMs, t.HoldSet = c.Timing.HoldMs, c.Timing.HoldSet
	}
	if !t.MultiTapSet && t.MultiTapMs == 0 {
		t.MultiTapMs, t.MultiTapSet = c.Timing.MultiTapMs, c.Timing.MultiTapSet
	}
	if !t.TapPressSet && t.TapPressMs == 0 {
		t.TapPressMs, t.TapPressSet = c.Timing.TapPressMs, c.Timing.TapPressSet
	}
	if !t.TapGapSet && t.TapGapMs == 0 {
		t.TapGapMs, t.TapGapSet = c.Timing.TapGapMs, c.Timing.TapGapSet
	}
	return &copyProfile
}
