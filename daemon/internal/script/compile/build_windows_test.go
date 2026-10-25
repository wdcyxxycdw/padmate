package compile

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// junction makes link a directory junction to target. Unlike a symlink, a
// junction needs no privilege, and Go (winsymlink=1) and esbuild do not
// treat it as a link.
func junction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBuildThroughJunction builds a root reached through a junction, as
// when a user moves the profiles directory to another drive.
func TestBuildThroughJunction(t *testing.T) {
	want, err := Build(BuildOptions{Root: "testdata/sibling", Entry: "main.ts"})
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if err := os.CopyFS(filepath.Join(base, "real", "prof"), os.DirFS("testdata/sibling")); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(base, "link"), filepath.Join(base, "real"))
	root := filepath.Join(base, "link", "prof")
	for _, entry := range []string{"main.ts", filepath.Join(root, "main.ts")} {
		r, err := Build(BuildOptions{Root: root, Entry: entry})
		if err != nil {
			t.Fatalf("entry %s: %v", entry, err)
		}
		if !bytes.Equal(r.JS, want.JS) {
			t.Errorf("entry %s: the bundle differs from the one built from testdata/sibling", entry)
		}
	}
}

// TestJunctionOutsideRoot checks that a junction inside the root does not
// let an import or the entry escape it.
func TestJunctionOutsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	writeFile(t, filepath.Join(root, "main.ts"), "import { secret } from \"./j/secret\";\n\nsecret();\n")
	writeFile(t, filepath.Join(root, "glob.ts"), "export const load = (name: string) => import(`./j/${name}.ts`);\n")
	writeFile(t, filepath.Join(base, "outside", "secret.ts"), "export function secret(): void {}\n")
	junction(t, filepath.Join(root, "j"), filepath.Join(base, "outside"))

	tests := []struct{ entry, want string }{
		{"main.ts", `main.ts:1:24: error: "./j/secret" resolves to a file outside the script root`},
		{"glob.ts", `glob.ts:1:46: error: "j/secret.ts" is outside the script root; a computed import() or require() path matches it`},
	}
	for _, tt := range tests {
		_, err := Build(BuildOptions{Root: root, Entry: tt.entry})
		var be *BuildError
		if !errors.As(err, &be) || err.Error() != tt.want {
			t.Errorf("entry %s: err = %v, want a *BuildError %q", tt.entry, err, tt.want)
		}
	}

	_, err := Build(BuildOptions{Root: root, Entry: "j/secret.ts"})
	var be *BuildError
	if err == nil || errors.As(err, &be) || !strings.Contains(err.Error(), "outside the script root") {
		t.Errorf("entry through the junction: err = %v, want a plain error about the script root", err)
	}
}
