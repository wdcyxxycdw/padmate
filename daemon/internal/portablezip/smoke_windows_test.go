package portablezip

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// TestExtractedPreviewSmoke verifies the actual package produced by Taskfile.
// It is opt-in because normal tests do not rebuild or download release files.
func TestExtractedPreviewSmoke(t *testing.T) {
	file := os.Getenv("PADMATE_TEST_PACKAGE")
	if file == "" {
		t.Skip("set PADMATE_TEST_PACKAGE to the package:zip archive")
	}
	if !filepath.IsAbs(file) {
		t.Fatal("PADMATE_TEST_PACKAGE must be an absolute path")
	}
	z, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	prefix := strings.TrimSuffix(filepath.Base(file), ".zip")
	label := strings.TrimSuffix(strings.TrimPrefix(prefix, "padmate-"), "-windows-amd64")
	if _, err := exeresources.FixedVersion(label); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "便携预览 with spaces")
	contents := make(map[string][]byte)
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, prefix+"/") || !safeArchivePath(f.Name) || !f.Mode().IsRegular() {
			t.Fatalf("unsafe package entry %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(r)
		if err := r.Close(); readErr != nil || err != nil {
			t.Fatalf("extract %s: %v, %v", f.Name, readErr, err)
		}
		name := strings.TrimPrefix(f.Name, prefix+"/")
		if _, exists := contents[name]; exists {
			t.Fatalf("duplicate package file %s", name)
		}
		contents[name] = data
		writeFixture(t, dir, name, string(data))
	}
	for _, name := range append([]string{"padmated.exe", "padmatectl.exe", "SDL3.dll", "licenses/THIRD-PARTY.txt", "licenses/Go/LICENSE"}, requiredRepoFiles...) {
		if _, exists := contents[name]; !exists {
			t.Errorf("missing package file: %s", name)
		}
	}
	if _, exists := contents["padmate-probe.exe"]; exists {
		t.Error("developer probe should not be in the portable preview")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(contents["SHA256SUMS.txt"])), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		want, err := hex.DecodeString(sum)
		got := sha256.Sum256(contents[name])
		if !ok || err != nil || string(want) != string(got[:]) {
			t.Fatalf("package hash mismatch: %s", line)
		}
		delete(contents, name)
	}
	if len(contents) != 1 || contents["SHA256SUMS.txt"] == nil {
		t.Fatalf("hash manifest is incomplete: %v", len(contents))
	}
	for _, filename := range []string{"padmated.exe", "padmatectl.exe"} {
		f, err := os.Open(filepath.Join(dir, filename))
		if err != nil {
			t.Fatal(err)
		}
		err = exeresources.Check(f, filename, label, filename == "padmated.exe")
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	dll, err := os.ReadFile(filepath.Join(dir, sdlpin.DLLName))
	if err != nil || hex.EncodeToString(sha256Sum(dll)) != sdlpin.DLLSHA256 {
		t.Fatalf("extracted SDL differs from pin: %v", err)
	}
	for _, command := range []struct {
		exe  string
		args []string
		want string
	}{
		{"padmated.exe", []string{"-help"}, "usage: padmated"},
		{"padmated.exe", []string{"-version"}, "padmated " + label},
		{"padmatectl.exe", []string{"help"}, "usage: padmatectl"},
		{"padmatectl.exe", []string{"version"}, "padmatectl " + label},
		{"padmatectl.exe", []string{"validate", "examples/profiles/basic.json", "examples/scripts/stick-rate/profile.json", "examples/scripts/tabmate/profile.json", "examples/scripts/cycle-brush/profile.json"}, ""},
		{"padmated.exe", []string{"--profile", "examples/profiles/basic.json", "--fake-input", "examples/traces/basic.jsonl", "--dry-run"}, "send"},
	} {
		t.Run(command.exe+"_"+command.args[0], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(dir, command.exe), command.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "APPDATA="+filepath.Join(dir, "user-data"), "LOCALAPPDATA="+filepath.Join(dir, "local-data"))
			buildtool.HideWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(strings.ToLower(string(output)), strings.ToLower(command.want)) {
				t.Fatalf("extracted command: %v\n%s", err, output)
			}
		})
	}
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
