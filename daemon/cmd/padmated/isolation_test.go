package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateInstance(t *testing.T) {
	const pipe = `\\.\pipe\padmate-ui-dev-0123456789abcdef`
	const temp = `D:\temp`
	// A padmated built without the linker's isolation (a release build) that
	// is given a pipe is a private instance: the mutex follows the pipe, so
	// two private instances do not meet, nor one of the user's.
	name, dir := privateInstance("", "", "", pipe, `D:\run dir`, temp)
	if !strings.HasPrefix(name, `Local\padmate.instance.pipe.`) || dir != `D:\run dir` {
		t.Fatalf("privateInstance = %q, %q", name, dir)
	}
	if other, _ := privateInstance("", "", "", pipe+"x", "", temp); other == name || other == "" {
		t.Errorf("another pipe gives %q, the same as %q or none", other, name)
	}
	if again, _ := privateInstance("", "", "", strings.ToUpper(pipe), "", temp); again != name {
		t.Errorf("a pipe name is case-insensitive in Windows, so is the instance: %q, %q", again, name)
	}
	// The user's own padmated: no variable, no change.
	if name, dir := privateInstance("", "", "", "", `D:\run`, temp); name != "" || dir != "" {
		t.Errorf("without PADMATE_PIPE: %q, %q", name, dir)
	}
	// A build whose linker flags isolate it already keeps them.
	if name, dir := privateInstance("Local\\fixed", "", `D:\linked`, pipe, `D:\env`, temp); name != `Local\fixed` || dir != `D:\linked` {
		t.Errorf("linked instance and directory: %q, %q", name, dir)
	}
	if name, dir := privateInstance("", `\\.\pipe\linked`, "", pipe, `D:\env`, temp); name != "" || dir != "" {
		t.Errorf("a linked pipe is a build isolated already, and the environment changes nothing: %q, %q", name, dir)
	}
}

// A private instance that is given no discovery directory must not publish
// daemon.json in the default one, which is the user's own padmated's: it
// would overwrite the file that padmatectl and the UI find that padmated by.
// It gets a directory of its own under the temporary folder instead, one per
// pipe.
func TestPrivateInstanceWithoutDirectoryKeepsOffTheDefault(t *testing.T) {
	const pipe = `\\.\pipe\padmate-private-no-dir`
	temp := t.TempDir()
	_, dir := privateInstance("", "", "", pipe, "", temp)
	if dir == "" {
		t.Fatal("no directory: the default one, the user's own padmated's, would be used")
	}
	if rel, err := filepath.Rel(temp, dir); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		t.Errorf("directory %q is not below the temporary folder %q", dir, temp)
	}
	if _, again := privateInstance("", "", "", strings.ToUpper(pipe), "", temp); again != dir {
		t.Errorf("the same pipe in other case: %q, want %q", again, dir)
	}
	if _, other := privateInstance("", "", "", pipe+"2", "", temp); other == dir || other == "" {
		t.Errorf("another pipe shares the directory: %q, %q", other, dir)
	}
	// A directory that was named is used as named.
	if _, named := privateInstance("", "", "", pipe, `D:\named`, temp); named != `D:\named` {
		t.Errorf("PADMATE_IPC_DIR is not used: %q", named)
	}
}

// A private instance is a development build for the startup entry: it never
// writes the user's real Run value (UI-19).
func TestPrivateInstanceIsADevelopmentBuild(t *testing.T) {
	name, _ := privateInstance("", "", "", `\\.\pipe\padmate-private`, "", t.TempDir())
	if !devBuild(name, "") {
		t.Errorf("devBuild(%q) is false", name)
	}
}
