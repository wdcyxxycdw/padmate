package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRPCCommandRouting(t *testing.T) {
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	var method string
	var params map[string]any
	rpcInvoke = func(_ context.Context, _ string, m string, p any, _ io.Writer, _ bool) error {
		method = m
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		params = nil
		return json.Unmarshal(data, &params)
	}
	for _, tc := range []struct {
		args   []string
		method string
		params map[string]any
	}{
		{[]string{"status", "--device", "1"}, "status.get", map[string]any{"device": "1"}},
		{[]string{"devices"}, "devices.list", map[string]any{}},
		{[]string{"pause"}, "engine.pause", map[string]any{}},
		{[]string{"fake", "press", "south"}, "fake.press", map[string]any{"control": "south"}},
		{[]string{"fake", "release", "south"}, "fake.release", map[string]any{"control": "south"}},
		{[]string{"fake", "axis", "rightx", "-32768"}, "fake.axis", map[string]any{"axis": "rightx", "value": float64(-32768)}},
		{[]string{"fake", "effects"}, "fake.effects", map[string]any{}},
		{[]string{"fake", "effects", "--clear"}, "fake.effects", map[string]any{"clear": true}},
		{[]string{"fake", "battery", "discharging", "15"}, "fake.battery", map[string]any{"state": "discharging", "percent": float64(15)}},
		{[]string{"fake", "battery", "charging", "100"}, "fake.battery", map[string]any{"state": "charging", "percent": float64(100)}},
		{[]string{"fake", "battery", "discharging", "0"}, "fake.battery", map[string]any{"state": "discharging", "percent": float64(0)}},
		{[]string{"fake", "battery", "wired"}, "fake.battery", map[string]any{"state": "wired"}},
		{[]string{"fake", "battery", "unknown"}, "fake.battery", map[string]any{"state": "unknown"}},
		{[]string{"joycon2", "status"}, "joycon2.status", map[string]any{}},
		{[]string{"joycon2", "pair"}, "joycon2.pair", map[string]any{}},
		{[]string{"joycon2", "pair", "-seconds", "45"}, "joycon2.pair", map[string]any{"seconds": float64(45)}},
		{[]string{"joycon2", "pair", "--seconds=600"}, "joycon2.pair", map[string]any{"seconds": float64(600)}},
		{[]string{"joycon2", "pair", "-seconds=1"}, "joycon2.pair", map[string]any{"seconds": float64(1)}},
		{[]string{"joycon2", "cancel"}, "joycon2.cancelPair", map[string]any{}},
		{[]string{"joycon2", "forget", "XKW1234567890"}, "joycon2.forget", map[string]any{"serial": "XKW1234567890"}},
		{[]string{"joycon2", "disconnect", "5001"}, "joycon2.disconnect", map[string]any{"id": float64(5001)}},
		{[]string{"fake", "joycon2"}, "fake.joycon2", map[string]any{}},
		{[]string{"fake", "joycon2", "R"}, "fake.joycon2", map[string]any{"side": "R"}},
		{[]string{"identify", "--device", "1"}, "devices.identify", map[string]any{"device": "1"}},
		{[]string{"identify", "--device=0300/p:x y"}, "devices.identify", map[string]any{"device": "0300/p:x y"}},
		{[]string{"resume"}, "engine.resume", map[string]any{}},
		{[]string{"layer", "--device=1", "toggle", "alt"}, "engine.setLayer", map[string]any{"device": "1", "op": "toggle", "layer": "alt"}},
		{[]string{"profile", "list"}, "profiles.list", map[string]any{}},
		{[]string{"profile", "get", "paint"}, "profiles.get", map[string]any{"id": "paint"}},
		{[]string{"profile", "resolve", "paint"}, "profiles.resolve", map[string]any{"id": "paint"}},
		{[]string{"profile", "activate", "paint"}, "profiles.activate", map[string]any{"id": "paint"}},
		{[]string{"profile", "deactivate"}, "profiles.activate", map[string]any{"id": nil}},
		{[]string{"profile", "explain", "--device=1", "south"}, "profiles.explain", map[string]any{"device": "1", "control": "south"}},
		{[]string{"config", "get"}, "config.get", map[string]any{}},
		{[]string{"script", "status", "--device=1"}, "script.status", map[string]any{"device": "1"}},
		{[]string{"script", "registrations"}, "script.registrations", map[string]any{}},
		{[]string{"script", "reload"}, "script.reload", map[string]any{}},
		{[]string{"script", "describe", "--id=paint"}, "script.describe", map[string]any{"id": "paint"}},
		{[]string{"script", "logs", "--after=7", "--limit=20"}, "script.logs", map[string]any{"after": float64(7), "limit": float64(20)}},
		{[]string{"rpc", "engine.releaseAll", `{}`}, "engine.releaseAll", map[string]any{}},
	} {
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || method != tc.method || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%v: code=%d %s %s %+v", tc.args, code, stderr, method, params)
		}
	}
}

func TestRPCProfilePutReadsBoundedUTF8File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	const raw = "{\"name\":\"绘画\"}\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	called := false
	rpcInvoke = func(ctx context.Context, dir, method string, params any, _ io.Writer, _ bool) error {
		called = true
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > 11*time.Second {
			t.Fatal("RPC has no bounded deadline")
		}
		if dir != "isolated" || method != "profiles.put" || !reflect.DeepEqual(params, map[string]any{"id": "paint", "text": raw, "revision": ""}) {
			t.Fatal(dir, method, params)
		}
		return nil
	}
	if code, _, err := runCmd(t, "profile", "put", "--ipc-dir=isolated", "--revision=", "paint", path); code != 0 || !called {
		t.Fatal(code, err)
	}
	for _, args := range [][]string{
		{"profile", "put", "paint", path},
		{"config", "put", path},
		{"layer", "hold", "alt"},
		{"rpc", "hello", "{}"},
		{"rpc", "status.get", "{"},
		{"pause", "unexpected"},
		{"status", "--timeout=0"},
		{"fake", "press"},
		{"fake", "axis", "rightx", "oops"},
		{"fake", "axis", "rightx", "32768"},
		{"fake", "effects", "extra"},
		{"fake", "effects", "--clear=maybe"},
		{"fake", "battery"},
		{"fake", "battery", "full"},
		{"fake", "battery", "Discharging", "10"},
		{"fake", "battery", "discharging", "101"},
		{"fake", "battery", "discharging", "-1"},
		{"fake", "battery", "discharging", "lots"},
		{"fake", "battery", "unknown", "50"},
		{"fake", "battery", "wired", "5", "6"},
		{"joycon2"},
		{"joycon2", "scan"},
		{"joycon2", "status", "extra"},
		{"joycon2", "pair", "-seconds", "0"},
		{"joycon2", "pair", "-seconds", "601"},
		{"joycon2", "pair", "-seconds", "-5"},
		{"joycon2", "pair", "-seconds", "soon"},
		{"joycon2", "pair", "extra"},
		{"joycon2", "status", "-seconds", "5"},
		{"joycon2", "cancel", "extra"},
		{"joycon2", "forget"},
		{"joycon2", "forget", ""},
		{"joycon2", "forget", "A", "B"},
		{"joycon2", "disconnect"},
		{"joycon2", "disconnect", "0"},
		{"joycon2", "disconnect", "-1"},
		{"joycon2", "disconnect", "abc"},
		{"joycon2", "disconnect", "1", "2"},
		{"fake", "joycon2", "X"},
		{"fake", "joycon2", "l"},
		{"fake", "joycon2", "L", "R"},
		{"identify", "extra"},
		{"identify", "--device", "1", "2"},
		{"identify", "--clear"},
	} {
		called = false
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("invalid command reached daemon: %v code=%d called=%v", args, code, called)
		}
	}
	for _, data := range [][]byte{{0xff}, []byte(strings.Repeat(" ", 4<<20+1))} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		called = false
		if code, _, _ := runCmd(t, "profile", "put", "--revision=", "paint", path); code != 1 || called {
			t.Fatal("invalid file reached daemon", code, called)
		}
	}
	rpcInvoke = func(context.Context, string, string, any, io.Writer, bool) error {
		return errors.New("revision conflict")
	}
	if code, _, stderr := runCmd(t, "status"); code != 1 || stderr == "" {
		t.Fatal(code, stderr)
	}
}

// TestJoyCon2CommandPrintsTheStatus: the joycon2 commands print what the daemon
// answered, as the other commands do, and an RPC error is a failure with the
// error's data.
func TestJoyCon2CommandPrintsTheStatus(t *testing.T) {
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	const status = `{"available":true,"autoConnect":true,"windowOpen":false,"scanning":false,"known":[],"links":[]}`
	rpcInvoke = func(_ context.Context, _ string, method string, _ any, out io.Writer, compact bool) error {
		if method != "joycon2.status" || !compact {
			t.Errorf("method %s compact=%v", method, compact)
		}
		_, err := io.WriteString(out, status)
		return err
	}
	code, stdout, stderr := runCmd(t, "joycon2", "status", "--json")
	if code != 0 || stdout != status || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	rpcInvoke = func(context.Context, string, string, any, io.Writer, bool) error {
		return errors.New("Joy-Con 2 over Bluetooth LE is not available")
	}
	if code, _, stderr := runCmd(t, "joycon2", "pair"); code != 1 || !strings.Contains(stderr, "not available") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// The usage lists the commands.
	code, stdout, _ = runCmd(t, "joycon2", "--help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"joycon2 status", "joycon2 pair [-seconds N]", "joycon2 cancel", "joycon2 forget SERIAL", "joycon2 disconnect ID", "fake joycon2 [L|R]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}
