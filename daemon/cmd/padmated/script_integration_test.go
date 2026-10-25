package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeReplayRunsProfileScriptAlongsideEngine(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	profile := `{"schemaVersion":0,"name":"script","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { on, key } from "padmate";
on("south", "press", async () => { await key.tap("Z"); });
`), 0o600); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(tracePath, []byte(`{"timeNs":1,"kind":"down","device":1,"control":"south"}
{"timeNs":2,"kind":"up","device":1,"control":"south"}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--profile", profilePath, "--fake-input", tracePath, "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "+Z") || !strings.Contains(stdout.String(), "-Z") {
		t.Fatalf("script output missing Z tap:\n%s", stdout.String())
	}
}

func TestFakeReplayScriptLogsRespectDebugLevel(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	profile := `{"schemaVersion":0,"name":"script-log","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `import { log, on } from "padmate";
log.info("setup message", 1);
on("south", "press", () => log.error("handler message", 2));`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(tracePath, []byte(`{"timeNs":1,"kind":"down","device":1,"control":"south"}
{"timeNs":2,"kind":"up","device":1,"control":"south"}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		level string
		want  bool
	}{{"info", false}, {"debug", true}} {
		t.Run(tc.level, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"--profile", profilePath, "--fake-input", tracePath, "--dry-run", "--log-level", tc.level, "--log-file", "-"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("replay exit %d: %s", code, stderr.String())
			}
			for _, message := range []string{`"msg":"setup message 1"`, `"msg":"handler message 2"`} {
				if got := strings.Contains(stderr.String(), message); got != tc.want {
					t.Errorf("%s replay contains %s = %v, want %v: %s", tc.level, message, got, tc.want, stderr.String())
				}
			}
			if tc.want && !strings.Contains(stderr.String(), `"script_level":"error"`) {
				t.Errorf("debug replay lost original script severity: %s", stderr.String())
			}
		})
	}
}
