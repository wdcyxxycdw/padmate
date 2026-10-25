package spike

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

//go:embed bench/*.ts
var benchFS embed.FS

// ScriptEntry is the benchmark script's entry, relative to its root.
const ScriptEntry = "main.ts"

// Script is the benchmark script written out to a temporary directory,
// since esbuild reads real files.
type Script struct {
	Root string
}

// NewScript writes the benchmark script to a new temporary directory.
func NewScript() (*Script, error) {
	root, err := os.MkdirTemp("", "padmate-spk03-")
	if err != nil {
		return nil, fmt.Errorf("spike: %w", err)
	}
	s := &Script{Root: root}
	err = fs.WalkDir(benchFS, "bench", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := benchFS.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFile(root, d.Name(), string(b))
	})
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("spike: write script: %w", err)
	}
	return s, nil
}

// Build bundles the script.
func (s *Script) Build() (compile.Result, error) {
	return compile.Build(compile.BuildOptions{Root: s.Root, Entry: ScriptEntry})
}

// Close removes the temporary directory.
func (s *Script) Close() error {
	return os.RemoveAll(s.Root)
}

func writeFile(root, name, src string) error {
	return os.WriteFile(filepath.Join(root, name), []byte(src), 0o600)
}
