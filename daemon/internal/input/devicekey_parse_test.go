package input_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestParseDeviceKey(t *testing.T) {
	guid := strings.Repeat("a0", 16)
	for _, key := range []input.DeviceKey{
		{}, {GUID: guid}, {GUID: guid, N: 3},
		{GUID: guid, Serial: "序号/#2/p:path", N: 1}, {GUID: guid, Path: `\\?\HID#VID_057E&PID_2009#1`},
	} {
		got, err := input.ParseDeviceKey(key.String())
		if err != nil || got != key {
			t.Fatalf("%+v => %+v, %v", key, got, err)
		}
		data, err := json.Marshal(map[input.DeviceKey]int{key: 1})
		if err != nil {
			t.Fatal(err)
		}
		var back map[input.DeviceKey]int
		if err := json.Unmarshal(data, &back); err != nil || back[key] != 1 {
			t.Fatal(string(data), back, err)
		}
	}
	for _, text := range []string{"bad", guid + "#0", guid + "#01", guid + "#-1", guid + "#9999999999999999999999999", guid + "/q:abc", guid + "/s:", guid + "/p:", strings.ToUpper(guid), guid + "\x00"} {
		if _, err := input.ParseDeviceKey(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func FuzzParseDeviceKey(f *testing.F) {
	f.Add("")
	f.Add(strings.Repeat("a0", 16) + "#1/s:a/#2")
	f.Fuzz(func(t *testing.T, text string) {
		k, err := input.ParseDeviceKey(text)
		if err != nil {
			return
		}
		if k.String() != text {
			t.Fatalf("noncanonical %q => %q", text, k.String())
		}
	})
}
