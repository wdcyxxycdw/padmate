package portablezip

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var requiredRepoFiles = []string{
	"README.md", "README.en.md", "README-PORTABLE.md", "LICENSE", "licenses/README.md", "licenses/SDL3-LICENSE.txt",
	"schema/profile.schema.json", "examples/traces/basic.jsonl", "examples/profiles/basic.json",
	"examples/scripts/README.md",
	"examples/scripts/stick-rate/profile.json", "examples/scripts/stick-rate/main.ts", "examples/scripts/stick-rate/README.md",
	"examples/scripts/tabmate/profile.json", "examples/scripts/tabmate/main.ts", "examples/scripts/tabmate/README.md",
	"examples/scripts/cycle-brush/profile.json", "examples/scripts/cycle-brush/main.ts", "examples/scripts/cycle-brush/README.md",
}

// snapshot reads a regular file only after checking every source path component.
// Archive writers use this immutable copy, including executable/DLL validation.
func snapshot(root, name string) ([]byte, error) {
	if !safeArchivePath(name) {
		return nil, fmt.Errorf("unsafe source path %q", name)
	}
	file := root
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source root must be a directory, not a symlink: %s", root)
	}
	for _, part := range strings.Split(name, "/") {
		file = filepath.Join(file, part)
		info, err = os.Lstat(file)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("source symlink is not allowed: %s", file)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source is not a regular file: %s", file)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, fmt.Errorf("source changed while opening: %s", file)
	}
	data, readErr := io.ReadAll(f)
	return data, errors.Join(readErr, f.Close())
}

func collectRepoFiles(root string) ([]entry, error) {
	var entries []entry
	seen := make(map[string]bool)
	add := func(name string) error {
		if seen[name] {
			return nil
		}
		data, err := snapshot(root, name)
		if err != nil {
			return err
		}
		seen[name] = true
		entries = append(entries, entry{name: name, data: data})
		return nil
	}
	for _, name := range requiredRepoFiles {
		if err := add(name); err != nil {
			return nil, fmt.Errorf("required package file %s: %w", name, err)
		}
	}
	for _, tree := range []string{"examples", "docs", "licenses"} {
		base := filepath.Join(root, tree)
		err := filepath.WalkDir(base, func(file string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("package source symlink is not allowed: %s", file)
			}
			if d.IsDir() {
				return nil
			}
			name, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(name))
		})
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}
