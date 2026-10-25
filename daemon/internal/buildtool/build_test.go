package buildtool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInvalidBuildLeavesExistingArtifact(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "padmated.exe")
	if err := os.WriteFile(file, []byte("existing artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []Options{
		{Version: "../bad", OutDir: dir, Targets: []string{"padmated"}},
		{Version: "dev", OutDir: dir, Targets: []string{"../../other"}},
		{Version: "dev", OutDir: dir, Targets: []string{"padmated", "padmated"}},
	} {
		if err := Build(context.Background(), opts); err == nil {
			t.Fatal("invalid build request accepted")
		}
		data, err := os.ReadFile(file)
		if err != nil || string(data) != "existing artifact" {
			t.Fatalf("invalid build changed previous artifact: %q %v", data, err)
		}
	}
}

func TestCompileFailurePreservesExistingArtifact(t *testing.T) {
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.com/invalid\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "padmated"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte("existing artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), Options{Version: "v0.7.0", OutDir: dir, ModuleDir: module, Targets: []string{"padmated"}}); err == nil {
		t.Fatal("compile failure accepted")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "existing artifact" {
		t.Fatalf("failed compile changed previous artifact: %q, %v", data, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("failed compile left staging files: %v, %v", files, err)
	}
}
