package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Profile inheritance (PRO-19, ADR-0070): validate reads the parents of a
// profile of a library from the same library.

const (
	extendsParent = `{
  "schemaVersion": 1,
  "name": "绘画",
  "layers": {
    "base": {
      "south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z"}},
      "east": {"label": "复制", "press": {"type": "macro", "file": "copy.ts"}}
    }
  }
}
`
	extendsChild = `{
  "schemaVersion": 1,
  "name": "左手",
  "extends": "paint",
  "layers": {
    "base": {"north": {"label": "粘贴", "press": {"type": "keys", "keys": "Ctrl+V"}}}
  }
}
`
)

// extendsLibrary writes a data folder with profiles and returns the path of
// the profile.json of each.
func extendsLibrary(t *testing.T, profiles map[string]string) (dir string, path func(id string) string) {
	t.Helper()
	dir = t.TempDir()
	for id, text := range profiles {
		p := filepath.Join(dir, "profiles", id, "profile.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, func(id string) string { return filepath.Join(dir, "profiles", id, "profile.json") }
}

func TestValidateAChildOfTheLibrary(t *testing.T) {
	dir, path := extendsLibrary(t, map[string]string{"paint": extendsParent, "left": extendsChild})
	if err := os.MkdirAll(filepath.Join(dir, "macros"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "macros", "copy.ts"), []byte("export default () => {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "validate", path("left"))
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, stderr, stdout)
	}
	// No diagnostic; the bindings of the merge, the inherited ones too.
	for _, want := range []string{
		`/layers/base/south "撤销": press +0 ms`,
		`/layers/base/east "复制": press +0 ms`,
		`/layers/base/north "粘贴": press +0 ms`,
	} {
		if !strings.Contains(stdout, "  "+want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, path("left")+": ok") != true {
		t.Errorf("no ok line:\n%s", stdout)
	}

	// JSON: the same, and no profile member in a diagnostic that is the file's own.
	code, stdout, _ = runCmd(t, "validate", "--json", path("left"))
	var obj struct {
		OK       bool `json:"ok"`
		Bindings []struct {
			ID string `json:"id"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil || code != 0 || !obj.OK || len(obj.Bindings) != 3 {
		t.Errorf("exit %d: %s %v", code, stdout, err)
	}
}

func TestValidateAChildWhoseParentIsMissing(t *testing.T) {
	_, path := extendsLibrary(t, map[string]string{"left": extendsChild})
	code, stdout, stderr := runCmd(t, "validate", path("left"))
	want := path("left") + `:4:14: error: parent "paint" is not in the profile library (/extends)` + "\n"
	if code != 1 || stdout != want || stderr != "" {
		t.Errorf("exit %d\n%q\nwant\n%q\nstderr %q", code, stdout, want, stderr)
	}
}

func TestValidateAChildWhoseParentHasErrors(t *testing.T) {
	broken := strings.Replace(extendsParent, `"Ctrl+Z"`, `"Hyper+"`, 1)
	_, path := extendsLibrary(t, map[string]string{"paint": broken, "left": extendsChild})
	code, stdout, _ := runCmd(t, "validate", path("left"))
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	// The child's error first, then the parent's, in the parent's file and position.
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 2 ||
		!strings.HasPrefix(lines[0], path("left")+`:4:14: error: parent "paint" has errors: 6:`) ||
		!strings.HasPrefix(lines[1], path("paint")+`:6:`) || !strings.Contains(lines[1], `unknown key "Hyper"`) {
		t.Errorf("output:\n%s", stdout)
	}

	code, stdout, _ = runCmd(t, "validate", "--json", path("left"))
	var obj struct {
		File        string               `json:"file"`
		OK          bool                 `json:"ok"`
		Diagnostics []profile.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil || code != 1 || obj.OK || len(obj.Diagnostics) != 2 {
		t.Fatalf("exit %d: %s %v", code, stdout, err)
	}
	if obj.Diagnostics[0].Profile != "" || obj.Diagnostics[1].Profile != "paint" || obj.Diagnostics[1].Line != 6 {
		t.Errorf("diagnostics %+v", obj.Diagnostics)
	}
	if !strings.Contains(stdout, `"profile":"paint"`) || strings.Count(stdout, `"profile"`) != 1 {
		t.Errorf("JSON:\n%s", stdout)
	}
}

// A macro the library lacks warns where the action is, in the parent's file.
func TestValidateMissingMacrosOfAParent(t *testing.T) {
	_, path := extendsLibrary(t, map[string]string{"paint": extendsParent, "left": extendsChild})
	code, stdout, _ := runCmd(t, "validate", path("left"))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	want := path("paint") + `:7:66: warning: macro file "copy.ts" is not in the macros folder (/layers/base/east/press/file)`
	if !strings.Contains(stdout, want+"\n") {
		t.Errorf("output lacks %q:\n%s", want, stdout)
	}
	if strings.Contains(stdout, path("left")+":") && strings.Contains(stdout, path("left")+":7") {
		t.Errorf("the warning is in the child's file:\n%s", stdout)
	}
}

func TestValidateMerged(t *testing.T) {
	dir, path := extendsLibrary(t, map[string]string{"paint": extendsParent, "left": extendsChild})
	if err := os.MkdirAll(filepath.Join(dir, "macros"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "macros", "copy.ts"), []byte("export default () => {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "validate", "--merged", path("left"))
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	p, ds := profile.Parse([]byte(stdout))
	if p == nil || ds.HasErrors() || p.Name != "左手" || p.Extends != "" || len(p.Layers[0].Bindings) != 3 {
		t.Errorf("merged output does not parse to the merged profile: %v\n%s", ds, stdout)
	}
	if strings.Contains(stdout, "extends") {
		t.Errorf("extends in the merged output:\n%s", stdout)
	}
	// A profile that extends nothing is printed as it is.
	code, stdout, _ = runCmd(t, "validate", "--merged", path("paint"))
	if code != 0 || stdout != extendsParent {
		t.Errorf("exit %d\n%q", code, stdout)
	}
	// Problems go to stderr, and nothing to stdout.
	_, missing := extendsLibrary(t, map[string]string{"left": extendsChild})
	code, stdout, stderr = runCmd(t, "validate", "--merged", missing("left"))
	if code != 1 || stdout != "" || !strings.Contains(stderr, `parent "paint" is not in the profile library`) {
		t.Errorf("exit %d\nstdout %q\nstderr %q", code, stdout, stderr)
	}
	// Not with --json.
	if code, _, stderr := runCmd(t, "validate", "--json", "--merged", path("left")); code != 2 || !strings.Contains(stderr, "cannot be used together") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// Only a profile of a library can extend another.
func TestValidateExtendsOutsideTheLibrary(t *testing.T) {
	file := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(file, []byte(extendsChild), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCmd(t, "validate", file)
	if code != 1 || !strings.Contains(stdout, "extends works only in a profile of the library") || !strings.Contains(stdout, "(/extends)") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
	// Not even a profile.json in a folder that is not a library's.
	dir := filepath.Join(t.TempDir(), "profiles", "Not A Library ID")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(dir, "profile.json")
	if err := os.WriteFile(file, []byte(extendsChild), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runCmd(t, "validate", file); code != 1 || !strings.Contains(stdout, "only in a profile of the library") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
}

// trace runs the merge of a profile of a library, and refuses a file that
// extends by itself.
func TestTraceOfAChild(t *testing.T) {
	const parent = `{"schemaVersion":1,"name":"P","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`
	const child = `{"schemaVersion":1,"name":"C","extends":"p","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
	_, path := extendsLibrary(t, map[string]string{"p": parent, "c": child})
	input := "../../../examples/traces/basic.jsonl"
	code, stdout, stderr := runCmd(t, "trace", "--profile", path("c"), "--input", input)
	if code != 0 || stderr != "" {
		t.Fatalf("trace = %d, stderr %q", code, stderr)
	}
	// The south button of the trace is bound by the parent.
	if !strings.Contains(stdout, "+B") {
		t.Errorf("trace output lacks the parent's binding:\n%s", stdout)
	}
	file := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(file, []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCmd(t, "trace", "--profile", file, "--input", input)
	if code != 1 || !strings.Contains(stderr, "only in a profile of the library") {
		t.Errorf("trace of a file that extends = %d, stderr %q", code, stderr)
	}
}
