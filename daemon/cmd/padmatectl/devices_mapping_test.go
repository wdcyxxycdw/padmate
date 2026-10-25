package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const mapGUID = "ff00d0df317374207261772070007600"

// TestDevicesMappingRouting (IN-18): "devices mapping set|reset" call
// devices.setMapping and devices.resetMapping with the controller and the
// mapping; without --device the only connected controller is the one.
func TestDevicesMappingRouting(t *testing.T) {
	text := mapGUID + ",*,a:b0,b:b1,platform:Windows"
	file := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(file, append([]byte{0xEF, 0xBB, 0xBF}, "  "+text+" \r\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args    []string
		methods []string
		params  map[string]any
	}{
		{[]string{"devices", "mapping", "set", "--device", "3", text}, []string{"devices.setMapping"}, map[string]any{"device": "3", "mapping": text}},
		{[]string{"devices", "mapping", "set", "--device=3", "@" + file}, []string{"devices.setMapping"}, map[string]any{"device": "3", "mapping": text}},
		{[]string{"devices", "mapping", "reset", "--device", "3"}, []string{"devices.resetMapping"}, map[string]any{"device": "3"}},
		{[]string{"devices", "mapping", "reset", "--device", "0300/p:x y"}, []string{"devices.resetMapping"}, map[string]any{"device": "0300/p:x y"}},
		{[]string{"devices", "mapping", "set", text}, []string{"devices.list", "devices.setMapping"}, map[string]any{"device": "3", "mapping": text}},
		{[]string{"devices", "mapping", "reset"}, []string{"devices.list", "devices.resetMapping"}, map[string]any{"device": "3"}},
	} {
		rig := newIdentifyRig(t, onePad)
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || !reflect.DeepEqual(rig.methods, tc.methods) || !reflect.DeepEqual(rig.params[len(rig.params)-1], tc.params) {
			t.Errorf("%v: code %d, stderr %q, calls %v %v; want %v %v", tc.args, code, stderr, rig.methods, rig.params, tc.methods, tc.params)
		}
	}
}

func TestDevicesMappingRefuses(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), maxMappingFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.txt")
	if err := os.WriteFile(bad, []byte{0xff, 0xfe, 'x'}, 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"devices", "mapping"}, 2, "invalid daemon command"},
		{[]string{"devices", "mapping", "set"}, 2, "invalid daemon command"},
		{[]string{"devices", "mapping", "set", "--device", "3", "a", "b"}, 2, "invalid daemon command"},
		{[]string{"devices", "mapping", "reset", "--device", "3", "extra"}, 2, "invalid daemon command"},
		{[]string{"devices", "mapping", "clear", "--device", "3"}, 2, "invalid daemon command"},
		{[]string{"devices", "mapping", "set", "--device", "3", "@" + filepath.Join(t.TempDir(), "missing")}, 1, "missing"},
		{[]string{"devices", "mapping", "set", "--device", "3", "@" + big}, 1, "longer than"},
		{[]string{"devices", "mapping", "set", "--device", "3", "@" + bad}, 1, "not utf-8"},
		{[]string{"devices", "mapping", "set", "--device", "3", "@" + empty}, 1, "is empty"},
	} {
		rig := newIdentifyRig(t, onePad)
		code, stdout, stderr := runCmd(t, tc.args...)
		if code != tc.code || stdout != "" || !strings.Contains(strings.ToLower(stderr), tc.want) || len(rig.methods) != 0 {
			t.Errorf("%v: code %d, stdout %q, stderr %q, calls %v; want %d, %q and no call", tc.args, code, stdout, stderr, rig.methods, tc.code, tc.want)
		}
	}
	// With several controllers and no --device, nothing is chosen.
	rig := newIdentifyRig(t, `[{"info":{"id":1}},{"info":{"id":2}}]`)
	if code, _, stderr := runCmd(t, "devices", "mapping", "reset"); code != 2 || !strings.Contains(stderr, "--device") || !reflect.DeepEqual(rig.methods, []string{"devices.list"}) {
		t.Errorf("several controllers: code %d, stderr %q, calls %v", code, stderr, rig.methods)
	}
	// The plain "devices" still lists.
	rig = newIdentifyRig(t, onePad)
	if code, _, _ := runCmd(t, "devices"); code != 0 || !reflect.DeepEqual(rig.methods, []string{"devices.list"}) {
		t.Errorf("devices: calls %v", rig.methods)
	}
}

func TestDevicesMappingUsage(t *testing.T) {
	_, stdout, _ := runCmd(t, "devices", "--help")
	for _, want := range []string{"devices mapping set [--device ID] MAPPING|@FILE", "devices mapping reset [--device ID]", "noBack"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}
