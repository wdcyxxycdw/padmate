package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// macro list|new|import|rename|delete go to the daemon's macros.* methods,
// which manage the script library of every profile (SCR-51), so they take
// no profile; import reads FILE itself, bounded, and sends its base name.
func TestMacroRouting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "复制 全部.ts")
	const text = "export default function () {}\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
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
		{[]string{"macro", "list"}, "macros.list", map[string]any{}},
		{[]string{"macro", "new", "复制全部"}, "macros.create", map[string]any{"name": "复制全部"}},
		{[]string{"macro", "import", file}, "macros.import", map[string]any{"name": "复制 全部.ts", "text": text, "replace": false}},
		{[]string{"macro", "import", "--replace", file}, "macros.import", map[string]any{"name": "复制 全部.ts", "text": text, "replace": true}},
		{[]string{"macro", "rename", "a.ts", "b c.ts"}, "macros.rename", map[string]any{"file": "a.ts", "to": "b c.ts"}},
		{[]string{"macro", "delete", "a.ts"}, "macros.delete", map[string]any{"file": "a.ts"}},
	} {
		called = false
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || !called || method != tc.method || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%v: code=%d %s %s %+v", tc.args, code, stderr, method, params)
		}
	}
	for _, args := range [][]string{
		{"macro"},
		{"macro", "list", "--id", "paint"},
		{"macro", "list", "extra"},
		{"macro", "new"},
		{"macro", "new", "a", "b"},
		{"macro", "import"},
		{"macro", "rename", "a.ts"},
		{"macro", "delete"},
		{"macro", "delete", "--device=1", "a.ts"},
		{"macro", "new", "--replace", "a"},
		{"macro", "run", "a.ts"},
	} {
		called = false
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("invalid command reached daemon: %v code=%d", args, code)
		}
	}
	if err := os.WriteFile(file, []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	called = false
	if code, _, _ := runCmd(t, "macro", "import", file); code != 1 || called {
		t.Fatal("invalid file reached daemon", code, called)
	}
	for _, args := range [][]string{{"macro", "--help"}, {"macro", "delete", "-h"}} {
		code, stdout, _ := runCmd(t, args...)
		if code != 0 || !strings.Contains(stdout, "macro import [--replace] FILE") || !strings.Contains(stdout, "macro rename FILE TO") {
			t.Fatal(args, code, stdout)
		}
	}
	if code, stdout, _ := runCmd(t, "--help"); code != 0 || !strings.Contains(stdout, "macro") {
		t.Fatal(code, stdout)
	}
}
