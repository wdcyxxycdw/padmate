package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func TestProfileArchiveCommands(t *testing.T) {
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	archive := []byte("PK\x03\x04 archive bytes")
	var method string
	var params map[string]any
	called := false
	rpcInvoke = func(_ context.Context, _ string, m string, p any, out io.Writer, compact bool) error {
		called, method = true, m
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		params = nil
		if err := json.Unmarshal(data, &params); err != nil {
			return err
		}
		if m == "profiles.export" {
			if !compact {
				t.Error("export result not compact")
			}
			return json.NewEncoder(out).Encode(map[string]string{"name": "画画", "data": base64.StdEncoding.EncodeToString(archive)})
		}
		_, err = io.WriteString(out, "{}\n")
		return err
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "画画.padmate")
	code, stdout, stderr := runCmd(t, "profile", "export", "--json", "paint", file)
	if code != 0 || method != "profiles.export" || !reflect.DeepEqual(params, map[string]any{"id": "paint"}) {
		t.Fatal(code, stderr, method, params)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(stdout), &summary); err != nil || !reflect.DeepEqual(summary, map[string]any{"id": "paint", "name": "画画", "file": file, "bytes": float64(len(archive))}) {
		t.Fatal(stdout, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != string(archive) {
		t.Fatal(data, err)
	}
	// An existing file needs --force, and is checked before the daemon.
	called = false
	if code, _, stderr := runCmd(t, "profile", "export", "paint", file); code != 1 || called || !strings.Contains(stderr, "--force") {
		t.Fatal(code, called, stderr)
	}
	if err := os.WriteFile(file, []byte("older and longer than the archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCmd(t, "profile", "export", "--force", "paint", file); code != 0 {
		t.Fatal(code, stderr)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != string(archive) {
		t.Fatal(string(data), err)
	}

	encoded := base64.StdEncoding.EncodeToString(archive)
	for _, tc := range []struct {
		args   []string
		params map[string]any
	}{
		{[]string{"profile", "import", file}, map[string]any{"data": encoded}},
		{[]string{"profile", "import", "--name", "画画 2", file}, map[string]any{"data": encoded, "name": "画画 2"}},
		{[]string{"profile", "import", "--replace=paint", file}, map[string]any{"data": encoded, "replace": "paint"}},
		{[]string{"profile", "import", "--macros", "keep", file}, map[string]any{"data": encoded, "macros": "keep"}},
		{[]string{"profile", "import", "--macros=replace", "--name", "画画 2", file}, map[string]any{"data": encoded, "name": "画画 2", "macros": "replace"}},
	} {
		code, _, stderr := runCmd(t, tc.args...)
		if code != 0 || method != "profiles.import" || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%v: code=%d %s %s %+v", tc.args, code, stderr, method, params)
		}
	}
	for _, args := range [][]string{
		{"profile", "export", "paint"},
		{"profile", "export", "paint", file, "extra"},
		{"profile", "export", "--name=x", "paint", file},
		{"profile", "import"},
		{"profile", "import", "--name", "x", "--replace", "paint", file},
		{"profile", "import", "--force", file},
		{"profile", "import", "--macros", "both", file},
		{"profile", "import", "--macros=", file},
		{"profile", "export", "--macros", "keep", "paint", file},
		{"profile", "import", file, "extra"},
	} {
		called = false
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("invalid command reached daemon: %v code=%d", args, code)
		}
	}
	big := filepath.Join(dir, "big.padmate")
	if err := os.WriteFile(big, make([]byte, profilelib.MaxArchiveBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	called = false
	if code, _, stderr := runCmd(t, "profile", "import", big); code != 1 || called || !strings.Contains(stderr, "4 MiB") {
		t.Fatal(code, called, stderr)
	}
	code, stdout, _ = runCmd(t, "profile", "--help")
	if code != 0 || !strings.Contains(stdout, "profile export [--force] ID FILE") || !strings.Contains(stdout, "profile import [--name NAME | --replace ID] [--macros replace|keep] FILE") {
		t.Fatal(code, stdout)
	}
}
