package profile

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// anyDevice accepts every devices key, as profilelib's check does for the
// keys these tests use.
func anyDevice(string) bool { return true }

// repaired runs RepairConfig and checks what every repair must give: a
// document ParseConfig accepts, written as the UI writes config.json.
func repaired(t *testing.T, data string, validDevice func(string) bool) (string, *Config, ConfigRepair) {
	t.Helper()
	fixed, repair := RepairConfig([]byte(data), validDevice)
	c, ds := ParseConfig(fixed)
	if ds.HasErrors() {
		t.Fatalf("repair of %s does not parse: %v\n%s", data, ds, fixed)
	}
	if !strings.HasSuffix(string(fixed), "}\n") || strings.HasPrefix(string(fixed), "\uFEFF") {
		t.Fatalf("repair of %s is not one indented object: %q", data, fixed)
	}
	return string(fixed), c, repair
}

// TestRepairConfigResetsWhatDoesNotParse: a file that is not a JSON
// object at all is replaced by the defaults (PRO-29).
func TestRepairConfigResetsWhatDoesNotParse(t *testing.T) {
	for _, data := range []string{
		``, `   `, `{"schemaVersion":0,`, `{"schemaVersion":0,"enabled":true,}`, `{schemaVersion:0}`,
		`{"schemaVersion":0,"enabled":true,"enabled":false}`, "\xff\xfe{\x00}\x00", "{\"name\":\"\xff\"}",
		`[]`, `null`, `"config"`, `0`, `{"schemaVersion":0} {}`,
	} {
		fixed, c, repair := repaired(t, data, anyDevice)
		if fixed != DefaultConfigText || !repair.Reset || len(repair.Members) != 0 {
			t.Errorf("%q: %q %+v", data, fixed, repair)
		}
		if !reflect.DeepEqual(c, DefaultConfig()) {
			t.Errorf("%q: settings %+v", data, c)
		}
	}
}

// TestRepairConfigKeepsValidMembers: each error resets only the smallest
// member around it to its default; every other member stays, in its
// order, with its bytes (PRO-29).
func TestRepairConfigKeepsValidMembers(t *testing.T) {
	const rules = `"rules":[{"exe":"paint.exe","profile":"paint"},{"exe":"C:\\Program Files\\Krita\\krita.exe","profile":"krita","enabled":false}]`
	for _, tc := range []struct {
		name, data, want string
		members          []string
	}{{
		// The owner's case (F17): an emergency combo of one button. Its
		// family's override goes, so the built-in combo applies again.
		name:    "panic override",
		data:    `{"schemaVersion":0,"enabled":false,"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":2000},"joyconLeft":{"buttons":["back","start"],"holdMs":3000}}},` + rules + `}`,
		want:    `{"schemaVersion":0,"enabled":false,"safety":{"panic":{"joyconLeft":{"buttons":["back","start"],"holdMs":3000}}},` + rules + `}`,
		members: []string{"/safety/panic/standard"},
	}, {
		// A wrong button name drops the whole combo, never just the
		// button: two of the three is a combo the user did not record.
		name:    "panic button",
		data:    `{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start","sl"],"holdMs":2000}}}}`,
		want:    `{"schemaVersion":0,"safety":{"panic":{}}}`,
		members: []string{"/safety/panic/standard"},
	}, {
		name:    "panic hold",
		data:    `{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":100}}}}`,
		want:    `{"schemaVersion":0,"safety":{"panic":{}}}`,
		members: []string{"/safety/panic/standard"},
	}, {
		name:    "one rule",
		data:    `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"},{"exe":"relative/sai.exe","profile":"sai"},{"exe":"krita.exe","profile":"../krita"},{"exe":"csp.exe","profile":"csp"}]}`,
		want:    `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"},{"exe":"csp.exe","profile":"csp"}]}`,
		members: []string{"/rules/1", "/rules/2"},
	}, {
		name:    "rule without profile",
		data:    `{"schemaVersion":0,"rules":[{"exe":"paint.exe"},{"exe":"csp.exe","profile":"csp"}]}`,
		want:    `{"schemaVersion":0,"rules":[{"exe":"csp.exe","profile":"csp"}]}`,
		members: []string{"/rules/0"},
	}, {
		// A member with a default goes alone: the rule stays, enabled.
		name:    "rule member with a default",
		data:    `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint","enabled":"no","device":"id:1"}]}`,
		want:    `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}]}`,
		members: []string{"/rules/0/enabled", "/rules/0/device"},
	}, {
		name:    "rules not a list",
		data:    `{"schemaVersion":0,"enabled":true,"rules":{"exe":"paint.exe"}}`,
		want:    `{"schemaVersion":0,"enabled":true}`,
		members: []string{"/rules"},
	}, {
		// ADR-0075: a function key that is none of start, back and guide goes
		// alone, so the default (start) applies; a valid one stays.
		name:    "function key",
		data:    `{"schemaVersion":0,"functionKey":"select","enabled":false}`,
		want:    `{"schemaVersion":0,"enabled":false}`,
		members: []string{"/functionKey"},
	}, {
		name:    "function key of another type",
		data:    `{"schemaVersion":0,"functionKey":3}`,
		want:    `{"schemaVersion":0}`,
		members: []string{"/functionKey"},
	}, {
		name:    "function key that is valid",
		data:    `{"schemaVersion":0,"functionKey":"guide"}`,
		want:    `{"schemaVersion":0,"functionKey":"guide"}`,
		members: nil,
	}, {
		name:    "timing",
		data:    `{"schemaVersion":0,"timing":{"holdMs":-1,"multiTapMs":250,"tapPressMs":"20"}}`,
		want:    `{"schemaVersion":0,"timing":{"multiTapMs":250}}`,
		members: []string{"/timing/holdMs", "/timing/tapPressMs"},
	}, {
		name:    "output",
		data:    `{"schemaVersion":0,"output":{"eventGapMs":51,"sendToOwnWindow":true}}`,
		want:    `{"schemaVersion":0,"output":{"sendToOwnWindow":true}}`,
		members: []string{"/output/eventGapMs"},
	}, {
		// IN-14: a calibration of a stick whose pair cannot hold goes as a
		// whole stick; the rest of the device stays, the ignored flag too.
		name:    "calibration stick",
		data:    `{"schemaVersion":0,"devices":{"k":{"ignored":true,"calibration":{"ls":{"deadzone":0.5,"saturation":0.4},"rs":{"deadzone":0.18}}}}}`,
		want:    `{"schemaVersion":0,"devices":{"k":{"ignored":true,"calibration":{"rs":{"deadzone":0.18}}}}}`,
		members: []string{"/devices/k/calibration/ls"},
	}, {
		// A value out of range goes alone; its stick keeps the other member.
		name:    "calibration value",
		data:    `{"schemaVersion":0,"devices":{"k":{"calibration":{"ls":{"deadzone":2,"saturation":0.9},"rs":{"saturation":"high"}}}}}`,
		want:    `{"schemaVersion":0,"devices":{"k":{"calibration":{"ls":{"saturation":0.9},"rs":{}}}}}`,
		members: []string{"/devices/k/calibration/ls/deadzone", "/devices/k/calibration/rs/saturation"},
	}, {
		name:    "calibration members",
		data:    `{"schemaVersion":0,"devices":{"k":{"ignored":false,"calibration":{"lt":{"deadzone":0.2},"rs":{"deadzone":0.1,"curve":1}}}}}`,
		want:    `{"schemaVersion":0,"devices":{"k":{"ignored":false,"calibration":{"rs":{"deadzone":0.1}}}}}`,
		members: []string{"/devices/k/calibration/lt", "/devices/k/calibration/rs/curve"},
	}, {
		name:    "calibration not an object",
		data:    `{"schemaVersion":0,"devices":{"k":{"ignored":true,"calibration":[0.2]}}}`,
		want:    `{"schemaVersion":0,"devices":{"k":{"ignored":true}}}`,
		members: []string{"/devices/k/calibration"},
	}, {
		name:    "unknown members",
		data:    `{"schemaVersion":0,"bogus":1,"timing":{"holdMs":300,"later":true},"enabled":false}`,
		want:    `{"schemaVersion":0,"timing":{"holdMs":300},"enabled":false}`,
		members: []string{"/bogus", "/timing/later"},
	}, {
		name:    "retired members",
		data:    `{"schemaVersion":0,"onUnmatched":"skip","defaultProfile":"paint","ignore":["explorer.exe",""]}`,
		want:    `{"schemaVersion":0,"defaultProfile":"paint"}`,
		members: []string{"/onUnmatched", "/ignore"},
	}, {
		name:    "schema version",
		data:    `{"enabled":false,"schemaVersion":1,"rules":[]}`,
		want:    `{"enabled":false,"schemaVersion":0,"rules":[]}`,
		members: []string{"/schemaVersion"},
	}, {
		name:    "missing schema version",
		data:    `{"enabled":false}`,
		want:    `{"schemaVersion":0,"enabled":false}`,
		members: []string{"/schemaVersion"},
	}, {
		name:    "null members",
		data:    `{"schemaVersion":0,"enabled":null,"devices":null,"timing":{"holdMs":null}}`,
		want:    `{"schemaVersion":0,"timing":{}}`,
		members: []string{"/enabled", "/devices", "/timing/holdMs"},
	}, {
		name:    "several at once",
		data:    "\uFEFF" + `{"$schema":"https://example.com/config.schema.json","schemaVersion":0,"releaseOnSwitch":"sometimes","devices":{"` + strings.Repeat("a0", 16) + `":{"ignored":true,"why":"x"}},` + rules + `,"timing":{"holdMs":60001}}`,
		want:    `{"$schema":"https://example.com/config.schema.json","schemaVersion":0,"devices":{"` + strings.Repeat("a0", 16) + `":{"ignored":true}},` + rules + `,"timing":{}}`,
		members: []string{"/releaseOnSwitch", "/devices/" + strings.Repeat("a0", 16) + "/why", "/timing/holdMs"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fixed, _, repair := repaired(t, tc.data, anyDevice)
			want := string(indentConfig([]byte(tc.want)))
			if fixed != want || repair.Reset || !slices.Equal(repair.Members, tc.members) {
				t.Fatalf("repair\n%s%+v\nwant\n%s%v", fixed, repair, want, tc.members)
			}
		})
	}
}

// TestRepairConfigDevices: a devices key the caller rejects (profilelib
// checks DeviceKey syntax, which this package cannot) goes with its
// settings; the other devices stay.
func TestRepairConfigDevices(t *testing.T) {
	good := strings.Repeat("b1", 16)
	data := `{"schemaVersion":0,"devices":{"bad":{"ignored":true},"` + good + `":{"ignored":true}}}`
	fixed, c, repair := repaired(t, data, func(key string) bool { return key == good })
	if !reflect.DeepEqual(repair.Members, []string{"/devices/bad"}) || len(c.Devices) != 1 || !c.Devices[good].Ignored {
		t.Fatalf("%s %+v %+v", fixed, repair, c)
	}
}

// TestRepairConfigValid: a valid file comes back with the same settings
// and nothing reset.
func TestRepairConfigValid(t *testing.T) {
	const data = `{"schemaVersion":0,"enabled":false,"rules":[{"exe":"paint.exe","profile":"paint"}],"timing":{"holdMs":300}}`
	fixed, c, repair := repaired(t, data, anyDevice)
	want, _ := ParseConfig([]byte(data))
	if repair.Reset || len(repair.Members) != 0 || !reflect.DeepEqual(c, want) || fixed != string(indentConfig([]byte(data))) {
		t.Fatalf("%s %+v", fixed, repair)
	}
}

// TestDefaultConfigText is the file that replaces one beyond repair: the
// defaults, as the UI would write them.
func TestDefaultConfigText(t *testing.T) {
	c, ds := ParseConfig([]byte(DefaultConfigText))
	if ds.HasErrors() || !reflect.DeepEqual(c, DefaultConfig()) || DefaultConfigText != "{\n  \"schemaVersion\": 0\n}\n" {
		t.Fatal(c, ds)
	}
}
