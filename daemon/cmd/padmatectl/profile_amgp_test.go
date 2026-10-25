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

	"github.com/wdcyxxycdw/padmate/daemon/internal/amgp"
)

func TestProfileImportAmgp(t *testing.T) {
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	var method string
	var params map[string]any
	var sawCompact bool
	called := false
	rpcInvoke = func(_ context.Context, _ string, m string, p any, out io.Writer, compact bool) error {
		called, method, sawCompact = true, m, compact
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		params = nil
		if err := json.Unmarshal(data, &params); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{
			"id": "pad", "name": "Pad", "revision": "r", "applied": false,
			"report": []amgp.ReportLine{
				{Severity: amgp.SeverityDropped, Code: "function.key", Set: 1, Control: "back", Message: "View"},
				{Severity: amgp.SeverityInfo, Code: "file.device", Message: "device"},
			},
		})
	}
	file := filepath.Join(t.TempDir(), "My Pad.gamecontroller.amgp")
	content := "<gamecontroller configversion=\"19\"/>"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	// By default: the report as text.
	code, stdout, stderr := runCmd(t, "profile", "import-amgp", file)
	if code != 0 || method != "profiles.importAmgp" || !reflect.DeepEqual(params, map[string]any{"data": encoded, "fileName": "My Pad.gamecontroller.amgp"}) {
		t.Fatal(code, stderr, method, params)
	}
	want := "导入为配置“Pad”（ID：pad）。丢弃 1 条，近似 0 条，信息 1 条。\n\n丢弃  set 1 / back：View\n信息  device\n"
	if stdout != want {
		t.Fatalf("got\n%s\nwant\n%s", stdout, want)
	}
	if !sawCompact {
		t.Error("the text is made from compact JSON")
	}

	// --name goes to the daemon; --json prints what the daemon answered.
	code, stdout, stderr = runCmd(t, "profile", "import-amgp", "--name", "画画", "--json", file)
	if code != 0 || !reflect.DeepEqual(params, map[string]any{"data": encoded, "fileName": "My Pad.gamecontroller.amgp", "name": "画画"}) {
		t.Fatal(code, stderr, params)
	}
	var result struct {
		ID     string
		Report []amgp.ReportLine
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.ID != "pad" || len(result.Report) != 2 || strings.Contains(stdout, "导入为配置") {
		t.Fatal(stdout, err)
	}

	// Usage mistakes and files that cannot be read stop before the daemon.
	called = false
	for _, args := range [][]string{
		{"profile", "import-amgp"},
		{"profile", "import-amgp", file, "extra"},
		{"profile", "import-amgp", "--replace=x", file},
		{"profile", "import-amgp", "--macros=keep", file},
	} {
		if code, _, _ := runCmd(t, args...); code != 2 || called {
			t.Fatalf("%v: code %d, called %v", args, code, called)
		}
	}
	if code, _, stderr := runCmd(t, "profile", "import-amgp", filepath.Join(t.TempDir(), "none.amgp")); code != 1 || called || stderr == "" {
		t.Fatal(code, called, stderr)
	}
	big := filepath.Join(t.TempDir(), "big.amgp")
	if err := os.WriteFile(big, make([]byte, amgp.MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCmd(t, "profile", "import-amgp", big); code != 1 || called || !strings.Contains(stderr, "at most 1 MiB") {
		t.Fatal(code, called, stderr)
	}
}
