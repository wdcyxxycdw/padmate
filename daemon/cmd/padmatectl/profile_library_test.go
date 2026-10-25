package main

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestProfileLibraryRouting(t *testing.T) {
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	var method string
	var params map[string]any
	called := false
	rpcInvoke = func(_ context.Context, _ string, m string, p any, _ io.Writer, _ bool) error {
		called, method = true, m
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
		{[]string{"profile", "copy", "paint", "paint-2"}, "profiles.copy", map[string]any{"from": "paint", "to": "paint-2"}},
		{[]string{"profile", "delete", "--revision", "abc", "paint"}, "profiles.delete", map[string]any{"id": "paint", "revision": "abc"}},
		{[]string{"profile", "delete", "--revision=", "broken"}, "profiles.delete", map[string]any{"id": "broken", "revision": ""}},
		{[]string{"profile", "history", "paint"}, "profiles.history", map[string]any{"id": "paint"}},
		{[]string{"profile", "restore", "--revision", "abc", "paint", "20261007-153012-0123abcd"}, "profiles.restore", map[string]any{"id": "paint", "version": "20261007-153012-0123abcd", "revision": "abc"}},
		{[]string{"profile", "restore", "--revision=", "paint", "20261007-153012-0123abcd"}, "profiles.restore", map[string]any{"id": "paint", "version": "20261007-153012-0123abcd", "revision": ""}},
	} {
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || method != tc.method || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%v: code=%d %s %s %+v", tc.args, code, stderr, method, params)
		}
	}
	for _, args := range [][]string{
		{"profile", "delete", "paint"},
		{"profile", "delete", "--revision=abc"},
		{"profile", "delete", "--revision=abc", "paint", "extra"},
		{"profile", "copy", "paint"},
		{"profile", "copy", "--revision=abc", "paint", "paint-2"},
		{"config", "delete", "--revision=abc"},
		{"profile", "history"},
		{"profile", "history", "paint", "extra"},
		{"profile", "history", "--revision=abc", "paint"},
		{"profile", "restore", "paint", "20261007-153012-0123abcd"},
		{"profile", "restore", "--revision=abc", "paint"},
		{"profile", "restore", "--revision=abc", "paint", "v", "extra"},
	} {
		called = false
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("invalid command reached daemon: %v code=%d", args, code)
		}
	}
	code, stdout, _ := runCmd(t, "profile", "--help")
	if code != 0 || !strings.Contains(stdout, "profile copy FROM TO") || !strings.Contains(stdout, "profile delete --revision REV ID") || !strings.Contains(stdout, "permanently deletes") ||
		!strings.Contains(stdout, "profile history ID") || !strings.Contains(stdout, "profile restore --revision REV ID VERSION") {
		t.Fatal(code, stdout)
	}
}
