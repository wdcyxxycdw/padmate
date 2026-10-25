package profile

import (
	"strings"
	"testing"
)

const (
	mapGUIDA = "ff00d0df317374207261772070007600"
	mapGUIDB = "ff008234326e64207261772070007600"
)

func mapText(guid string, fields ...string) string {
	return guid + ",*," + strings.Join(fields, ",") + ",platform:Windows"
}

func TestKeyGUID(t *testing.T) {
	for key, want := range map[string]string{
		mapGUIDA:                       mapGUIDA,
		mapGUIDA + "#2":                mapGUIDA,
		mapGUIDA + "/s:ABC":            mapGUIDA,
		mapGUIDA + "#1/p:\\\\?\\HID#x": mapGUIDA,
		"":                             "",
	} {
		if got := KeyGUID(key); got != want {
			t.Errorf("KeyGUID(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestConfigMappings(t *testing.T) {
	a := mapText(mapGUIDA, "a:b0", "b:b1")
	b := mapText(mapGUIDB, "a:b2")
	jsonString := func(s string) string { return `"` + s + `"` }
	tests := []struct {
		name    string
		devices string
		wantErr string // a part of the diagnostic, "" for a valid config
	}{
		{"one device", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}}`, ""},
		{"two keys of one GUID agree", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}, "` + mapGUIDA + `#1": {"mapping": ` + jsonString(a) + `, "ignored": true}}`, ""},
		{"keys agree in another order of fields", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}, "` + mapGUIDA + `/s:X": {"mapping": ` + jsonString(mapText(mapGUIDA, "b:b1", "a:b0")) + `}}`, ""},
		{"two GUIDs", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}, "` + mapGUIDB + `": {"mapping": ` + jsonString(b) + `}}`, ""},
		{"a key without a mapping beside one with", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}, "` + mapGUIDA + `#1": {"ignored": true}}`, ""},
		{"not a mapping", `{"` + mapGUIDA + `": {"mapping": "nonsense"}}`, "/devices/" + mapGUIDA + "/mapping"},
		{"binds nothing", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(mapText(mapGUIDA)) + `}}`, "binds no element"},
		{"another GUID", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(b) + `}}`, "not for the device key"},
		{"keys disagree", `{"` + mapGUIDA + `": {"mapping": ` + jsonString(a) + `}, "` + mapGUIDA + `#1": {"mapping": ` + jsonString(mapText(mapGUIDA, "a:b5")) + `}}`, "same as the mapping of"},
		{"a key without a GUID", `{"": {"mapping": ` + jsonString(a) + `}}`, "not for the device key"},
		{"too long", `{"` + mapGUIDA + `": {"mapping": "` + strings.Repeat("a", 2049) + `"}}`, "mapping"},
		{"empty", `{"` + mapGUIDA + `": {"mapping": ""}}`, "mapping"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, ds := ParseConfig([]byte(`{"schemaVersion": 0, "devices": ` + tc.devices + `}`))
			if tc.wantErr == "" {
				if c == nil {
					t.Fatalf("ParseConfig: %v", ds)
				}
				return
			}
			if c != nil || !strings.Contains(ds.Err().Error(), tc.wantErr) {
				t.Errorf("ParseConfig = %v, %v; want an error with %q", c, ds, tc.wantErr)
			}
		})
	}
}

func TestDeviceMappings(t *testing.T) {
	a := mapText(mapGUIDA, "b:b1", "a:b0")
	b := mapText(mapGUIDB, "a:b2")
	c, ds := ParseConfig([]byte(`{"schemaVersion": 0, "devices": {
		"` + mapGUIDB + `": {"mapping": "` + b + `"},
		"` + mapGUIDA + `#1": {"mapping": "` + a + `"},
		"` + mapGUIDA + `": {"mapping": "` + a + `", "ignored": true},
		"` + strings.Repeat("0", 32) + `": {"ignored": true}
	}}`))
	if c == nil {
		t.Fatal(ds)
	}
	want := []string{mapGUIDB + ",*,a:b2,platform:Windows", mapGUIDA + ",*,b:b1,a:b0,platform:Windows"} // GUIDB sorts first
	got := DeviceMappings(c.Devices)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("DeviceMappings = %q, want %q", got, want)
	}
	if got := DeviceMappings(nil); len(got) != 0 {
		t.Errorf("DeviceMappings(nil) = %q", got)
	}
}

func TestSetDeviceMappings(t *testing.T) {
	a := mapText(mapGUIDA, "a:b0")
	b := mapText(mapGUIDB, "a:b2")
	const head = "{\n  \"schemaVersion\": 0,\n"
	tests := []struct {
		name string
		in   string
		set  map[string]string
		want string
	}{
		{
			"adds devices to a file without them",
			`{"schemaVersion": 0, "enabled": false}`,
			map[string]string{mapGUIDA: a},
			head + "  \"enabled\": false,\n  \"devices\": {\n    \"" + mapGUIDA + "\": {\n      \"mapping\": \"" + a + "\"\n    }\n  }\n}\n",
		},
		{
			"keeps the other members, in their places",
			`{"devices": {"x": {"ignored": true}}, "schemaVersion": 0, "timing": {"holdMs": 300}}`,
			map[string]string{mapGUIDA: a},
			"{\n  \"devices\": {\n    \"x\": {\n      \"ignored\": true\n    },\n    \"" + mapGUIDA + "\": {\n      \"mapping\": \"" + a + "\"\n    }\n  },\n  \"schemaVersion\": 0,\n  \"timing\": {\n    \"holdMs\": 300\n  }\n}\n",
		},
		{
			"sets the mapping of a device that has other settings",
			`{"schemaVersion": 0, "devices": {"` + mapGUIDA + `": {"ignored": true, "mapping": "old"}}}`,
			map[string]string{mapGUIDA: a},
			head + "  \"devices\": {\n    \"" + mapGUIDA + "\": {\n      \"ignored\": true,\n      \"mapping\": \"" + a + "\"\n    }\n  }\n}\n",
		},
		{
			"takes a mapping out and keeps the rest of the device",
			`{"schemaVersion": 0, "devices": {"` + mapGUIDA + `": {"mapping": "` + a + `", "ignored": true}}}`,
			map[string]string{mapGUIDA: ""},
			head + "  \"devices\": {\n    \"" + mapGUIDA + "\": {\n      \"ignored\": true\n    }\n  }\n}\n",
		},
		{
			"removes a device that has nothing else",
			`{"schemaVersion": 0, "devices": {"` + mapGUIDA + `": {"mapping": "` + a + `"}, "y": {"ignored": true}}}`,
			map[string]string{mapGUIDA: ""},
			head + "  \"devices\": {\n    \"y\": {\n      \"ignored\": true\n    }\n  }\n}\n",
		},
		{
			"writes new keys in order after the old ones",
			`{"schemaVersion": 0, "devices": {"` + mapGUIDA + `": {"mapping": "old"}}}`,
			map[string]string{mapGUIDB + "#1": b, mapGUIDB: b, mapGUIDA: a},
			head + "  \"devices\": {\n    \"" + mapGUIDA + "\": {\n      \"mapping\": \"" + a + "\"\n    },\n    \"" + mapGUIDB + "\": {\n      \"mapping\": \"" + b + "\"\n    },\n    \"" + mapGUIDB + "#1\": {\n      \"mapping\": \"" + b + "\"\n    }\n  }\n}\n",
		},
		{
			"taking out a mapping of a key the file does not have changes nothing",
			`{"schemaVersion": 0}`,
			map[string]string{mapGUIDA: ""},
			head[:len(head)-2] + "\n}\n",
		},
		{
			"a BOM and comments of a hand-written file do not survive, the values do",
			"\ufeff{\"schemaVersion\":0,\"safety\":{\"panic\":{\"standard\":{\"buttons\":[\"back\",\"start\"],\"holdMs\":1500}}}}",
			map[string]string{mapGUIDA: a},
			"{\n  \"schemaVersion\": 0,\n  \"safety\": {\n    \"panic\": {\n      \"standard\": {\n        \"buttons\": [\n          \"back\",\n          \"start\"\n        ],\n        \"holdMs\": 1500\n      }\n    }\n  },\n  \"devices\": {\n    \"" + mapGUIDA + "\": {\n      \"mapping\": \"" + a + "\"\n    }\n  }\n}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SetDeviceMappings([]byte(tc.in), tc.set)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("SetDeviceMappings =\n%s\nwant\n%s", got, tc.want)
			}
			if c, ds := ParseConfig(got); c == nil {
				t.Errorf("the result does not parse: %v", ds)
			}
		})
	}
}

func TestSetDeviceMappingsErrors(t *testing.T) {
	for _, in := range []string{"", "{", `{"a": 1,}`, "[]", `"x"`, "\xff\xfe"} {
		if got, err := SetDeviceMappings([]byte(in), map[string]string{mapGUIDA: "x"}); err == nil {
			t.Errorf("SetDeviceMappings(%q) = %q, want an error", in, got)
		}
	}
	// A devices member that is no object is left to the parser.
	got, err := SetDeviceMappings([]byte(`{"schemaVersion":0,"devices":5}`), map[string]string{mapGUIDA: "x"})
	if err != nil || !strings.Contains(string(got), `"devices": 5`) {
		t.Errorf("SetDeviceMappings = %q, %v", got, err)
	}
}

func FuzzSetDeviceMappings(f *testing.F) {
	f.Add([]byte(`{"schemaVersion":0,"devices":{"a":{"mapping":"x","ignored":true}}}`), "a", "m")
	f.Add([]byte(`{}`), "", "")
	f.Fuzz(func(t *testing.T, data []byte, key, mapping string) {
		out, err := SetDeviceMappings(data, map[string]string{key: mapping})
		if err != nil {
			return
		}
		// Whatever it writes is JSON that the scanner reads again.
		if _, serr := scan(out); serr != nil {
			t.Fatalf("SetDeviceMappings(%q, %q, %q) = %q, which does not scan: %v", data, key, mapping, out, serr.msg)
		}
	})
}

// TestRepairConfigDropsABadMapping: a mapping that cannot work resets that
// member only (PRO-29), so one bad string does not cost the device's other
// settings.
func TestRepairConfigDropsABadMapping(t *testing.T) {
	a := mapText(mapGUIDA, "a:b0")
	in := `{"schemaVersion": 0, "devices": {"` + mapGUIDA + `": {"ignored": true, "mapping": "nonsense"}, "` + mapGUIDA + `#1": {"mapping": "` + a + `"}, "` + mapGUIDA + `#2": {"mapping": "` + mapText(mapGUIDA, "a:b9") + `"}}}`
	out, fix := RepairConfig([]byte(in), func(string) bool { return true })
	if fix.Reset {
		t.Fatalf("the whole file was reset: %s", out)
	}
	c, ds := ParseConfig(out)
	if c == nil {
		t.Fatalf("the repair does not parse: %v\n%s", ds, out)
	}
	if !c.Devices[mapGUIDA].Ignored || c.Devices[mapGUIDA].Mapping != "" || c.Devices[mapGUIDA+"#1"].Mapping != a {
		t.Errorf("devices after the repair: %+v", c.Devices)
	}
}
