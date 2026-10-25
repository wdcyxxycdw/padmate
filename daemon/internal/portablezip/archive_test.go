package portablezip

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArchiveContentsAndManifest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "preview.zip")
	entries := []entry{
		{name: "examples/scripts/tabmate/main.ts", data: []byte("example")},
		{name: "padmated.exe", data: []byte("gui binary")},
		{name: "licenses/SDL3-LICENSE.txt", data: []byte("SDL license")},
	}
	const prefix = "padmate-v0.7.0-preview-windows-amd64"
	if err := writeArchive(context.Background(), file, prefix, entries); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArchive(context.Background(), file, prefix, entries); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("archive is not deterministic: %v", err)
	}
	z, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	contents := make(map[string][]byte)
	var names []string
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, prefix+"/") || !safeArchivePath(f.Name) {
			t.Fatalf("unsafe archive entry %q", f.Name)
		}
		r, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(r)
		if err := r.Close(); readErr != nil || err != nil {
			t.Fatalf("read archive: %v, %v", readErr, err)
		}
		name := strings.TrimPrefix(f.Name, prefix+"/")
		contents[name] = data
		names = append(names, f.Name)
		if !f.Modified.Equal(archiveTime) {
			t.Fatalf("entry timestamp %s", f.Modified)
		}
	}
	if !slices.IsSorted(names) {
		t.Fatalf("entries are not sorted: %v", names)
	}
	if len(contents) != len(entries)+1 {
		t.Fatalf("unexpected files: %v", names)
	}
	for _, e := range entries {
		if !bytes.Equal(contents[e.name], e.data) {
			t.Errorf("wrong content for %s", e.name)
		}
		line := fmt.Sprintf("%x  %s\n", sha256.Sum256(e.data), e.name)
		if !strings.Contains(string(contents["SHA256SUMS.txt"]), line) {
			t.Errorf("missing hash: %s", e.name)
		}
	}
}

func TestSafeArchivePath(t *testing.T) {
	for _, name := range []string{
		"padmated.exe", "examples/scripts/层/main.ts", "licenses/go/golang.org/x/sys@v0.48.0/LICENSE",
	} {
		if !safeArchivePath(name) {
			t.Errorf("safe path rejected: %q", name)
		}
	}
	for _, name := range []string{
		"", ".", "..", "../outside", "/absolute", "dir/../file", "dir/./file", "dir//file", "dir/",
		`C:/outside`, `C:\outside`, `dir\file`, "bad:stream", "bad\x00name", "bad\nname", "bad*name", "bad?name",
		"dir/file.", "dir/file ", "CON", "dir/nul.txt", "COM1.log", "lpt9", "dir/aux/readme",
	} {
		if safeArchivePath(name) {
			t.Errorf("unsafe path accepted: %q", name)
		}
	}
}

func TestArchiveFailurePreservesPublishedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "preview.zip")
	for _, entries := range [][]entry{
		{{name: "../outside", data: []byte("bad")}},
		{{name: "README.md"}, {name: "readme.md"}},
		{{name: "SHA256SUMS.txt"}},
	} {
		if err := os.WriteFile(file, []byte("previous zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeArchive(context.Background(), file, "padmate-dev-windows-amd64", entries); err == nil {
			t.Fatal("unsafe input accepted")
		}
		data, err := os.ReadFile(file)
		if err != nil || string(data) != "previous zip" {
			t.Fatalf("previous zip changed: %q, %v", data, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeArchive(ctx, file, "padmate-dev-windows-amd64", []entry{{name: "README.md"}}); err == nil {
		t.Fatal("canceled archive accepted")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("failed archive left staging files: %v, %v", files, err)
	}
}

func TestCollectRepoFilesUsesAllowlist(t *testing.T) {
	root := t.TempDir()
	for _, name := range append(slices.Clone(requiredRepoFiles), "docs/SCRIPTING.md", "examples/profiles/basic.json", "daemon/private.txt", ".git/config") {
		writeFixture(t, root, name, name)
	}
	entries, err := collectRepoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.name] = true
		if strings.HasPrefix(e.name, "daemon/") || strings.HasPrefix(e.name, ".git/") {
			t.Fatalf("private file included: %s", e.name)
		}
	}
	for _, name := range append(slices.Clone(requiredRepoFiles), "docs/SCRIPTING.md", "examples/profiles/basic.json") {
		if !names[name] {
			t.Errorf("missing required file %s", name)
		}
	}
	if err := os.Remove(filepath.Join(root, "examples", "scripts", "tabmate", "main.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectRepoFiles(root); err == nil {
		t.Fatal("missing M6 example accepted")
	}
}

func TestSnapshotRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "actual/file.txt", "test")
	for _, c := range []struct{ name, target string }{
		{"file-link", filepath.Join(root, "actual", "file.txt")},
		{"dir-link", filepath.Join(root, "actual")},
	} {
		if err := os.Symlink(c.target, filepath.Join(root, c.name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		path := c.name
		if c.name == "dir-link" {
			path += "/file.txt"
		}
		if _, err := snapshot(root, path); err == nil {
			t.Fatalf("symlink accepted: %s", path)
		}
	}
}

func writeFixture(t *testing.T, root, name, data string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ENG-17: the README comes in two languages, and the documents link between
// them, so a package that has one must have the other.
func TestRequiredRepoFilesHaveBothReadmes(t *testing.T) {
	for _, name := range []string{"README.md", "README.en.md"} {
		if !slices.Contains(requiredRepoFiles, name) {
			t.Errorf("%s is not a required package file", name)
		}
	}
}
