package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The profile format is frozen at version 1 (PRO-01); version 0 files, the
// previews' format, still validate (PRO-06) and are never rewritten.
func TestValidateVersions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, version string) string {
		path := filepath.Join(dir, name)
		text := `{"schemaVersion": ` + version + `, "name": "n", "layers": {"base": {"south": {"press": {"type": "keys", "keys": "Ctrl+Z"}}}}}` + "\n"
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, version := range []string{"1", "0", "0.0"} {
		path := write("ok.json", version)
		before, _ := os.ReadFile(path)
		code, stdout, stderr := runCmd(t, "validate", path)
		if code != 0 || strings.Contains(stdout+stderr, "error") {
			t.Errorf("schemaVersion %s: exit %d, stdout %q, stderr %q", version, code, stdout, stderr)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("schemaVersion %s: validate rewrote the file", version)
		}
	}
	code, stdout, _ := runCmd(t, "validate", write("future.json", "2"))
	if code != 1 || !strings.Contains(stdout, "schemaVersion 2 is newer than supported version 1; upgrade padmate") {
		t.Errorf("schemaVersion 2: exit %d, stdout %q", code, stdout)
	}
	// An error after a long spelling of version 0 points at the right place.
	path := filepath.Join(dir, "typo.json")
	text := `{"schemaVersion": 0.0, "name": "n", "layers": {"base": {"south": {"press": {"type": "keys", "keys": "Ctrl+Shfit"}}}}}`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runCmd(t, "validate", path)
	col := strings.Index(text, "Shfit") + 1
	if code != 1 || !strings.Contains(stdout, ":1:"+strconv.Itoa(col)+": error: unknown key") {
		t.Errorf("typo after version 0.0: exit %d, stdout %q, want column %d", code, stdout, col)
	}
}
