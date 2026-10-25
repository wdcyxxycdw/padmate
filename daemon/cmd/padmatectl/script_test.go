package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

func TestScriptInitCreatesAndProtectsProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "script")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"script", "init", "--dir", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("script init exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "(api "+dtsgen.HostAPI.String()+")") {
		t.Fatalf("script init default API: %s", stdout.String())
	}
	for _, name := range []string{"main.ts", "tsconfig.json", "padmate.d.ts", "padmate-globals.d.ts", "lib.padmate-goja.d.ts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("// user work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"script", "init", "--dir", dir}, &stdout, &stderr); code != 1 {
		t.Fatalf("second init exit = %d, want 1", code)
	}
	if got, err := os.ReadFile(main); err != nil || string(got) != "// user work\n" {
		t.Fatalf("main.ts overwritten: %q, %v", got, err)
	}
}

// --api takes every supported script API version (dtsgen.SupportedAPIs).
func TestScriptInitAcceptsSupportedAPIs(t *testing.T) {
	for _, api := range dtsgen.SupportedNames() {
		t.Run(api, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "script")
			var stdout, stderr bytes.Buffer
			if code := run([]string{"script", "init", "--api", api, "--dir", dir}, &stdout, &stderr); code != 0 {
				t.Fatalf("script init --api %s exit %d: %s", api, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "(api "+api+")") {
				t.Fatalf("script init --api %s: %s", api, stdout.String())
			}
		})
	}
}

func TestScriptInitRejectsIncompatibleAPI(t *testing.T) {
	for _, api := range []string{"0.4", "0.5", "0.6", "0.7", "0.8", "0.9", "0.15", "0.18", "1.0"} {
		t.Run(api, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "script")
			var stdout, stderr bytes.Buffer
			if code := run([]string{"script", "init", "--api", api, "--dir", dir}, &stdout, &stderr); code == 0 {
				t.Fatal("incompatible API was accepted")
			}
			for _, want := range []string{api, "host API 0.21", "supports 0.21 and 0.17"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("error %q lacks %q", stderr.String(), want)
				}
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("incompatible API created directory: %v", err)
			}
		})
	}
}

// script init --id and script import go to the daemon, which writes into
// the library; the CLI reads the imported file itself, bounded.
func TestScriptInitAndImportRouting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "brush.ts")
	const text = "// 画笔\nexport {};\n"
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
		{[]string{"script", "init", "--id", "paint"}, "script.init", map[string]any{"id": "paint"}},
		{[]string{"script", "init", "--ipc-dir=isolated", "-id=paint"}, "script.init", map[string]any{"id": "paint"}},
		{[]string{"script", "import", "paint", file}, "script.import", map[string]any{"id": "paint", "name": "brush.ts", "text": text, "replace": false}},
		{[]string{"script", "import", "--replace", "paint", file}, "script.import", map[string]any{"id": "paint", "name": "brush.ts", "text": text, "replace": true}},
	} {
		called = false
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || !called || method != tc.method || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%v: code=%d %s %s %+v", tc.args, code, stderr, method, params)
		}
	}
	for _, args := range [][]string{
		{"script", "init", "--id=", "--json"},
		{"script", "init", "--id", "paint", "--dir", "x"},
		{"script", "init", "--id", "paint", "extra"},
		{"script", "init", "--id", "paint", "--device=1"},
		{"script", "import", "paint"},
		{"script", "import", "--device=1", "paint", file},
		{"script", "import", "--id=paint", file},
	} {
		called = false
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("invalid command reached daemon: %v code=%d", args, code)
		}
	}
	for _, data := range [][]byte{{0xff}, []byte(strings.Repeat(" ", 1<<20+1))} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		called = false
		if code, _, _ := runCmd(t, "script", "import", "paint", file); code != 1 || called {
			t.Fatal("invalid file reached daemon", code, called)
		}
	}
	for _, args := range [][]string{{"script", "--help"}, {"script", "import", "--help"}} {
		code, stdout, _ := runCmd(t, args...)
		if code != 0 || !strings.Contains(stdout, "script init --id PROFILE") || !strings.Contains(stdout, "script import [--replace] PROFILE FILE") {
			t.Fatal(args, code, stdout)
		}
	}
}
