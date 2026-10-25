// Package buildtool stages Go binaries and their Windows resources before
// publishing complete artifacts. Builds never leave .syso files in the tree.
package buildtool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
)

// Options selects the binaries and padmated's Windows subsystem.
type Options struct {
	Version, OutDir, ModuleDir string
	Targets                    []string
	GUI                        bool
	Log                        io.Writer
}

// Build compiles every selected command into a private output-directory
// staging folder. A failed compile or resource check preserves all previous
// binaries. Successful files replace their predecessors by rename.
func Build(ctx context.Context, opts Options) error {
	if _, err := exeresources.FixedVersion(opts.Version); err != nil {
		return err
	}
	if len(opts.Targets) == 0 {
		return errors.New("build: no targets")
	}
	seen := make(map[string]bool)
	for _, target := range opts.Targets {
		if target != "padmated" && target != "padmatectl" && target != "padmate-probe" {
			return fmt.Errorf("build: unknown target %q", target)
		}
		if seen[target] {
			return fmt.Errorf("build: duplicate target %q", target)
		}
		seen[target] = true
	}
	if opts.ModuleDir == "" {
		opts.ModuleDir = "."
	}
	out, err := filepath.Abs(opts.OutDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(out, ".padmate-build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, target := range opts.Targets {
		name := target
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		raw := filepath.Join(staging, name+".raw")
		flags := "-X github.com/wdcyxxycdw/padmate/daemon/internal/version.Version=" + opts.Version
		gui := opts.GUI && target == "padmated"
		if gui {
			if runtime.GOOS != "windows" {
				return errors.New("build: GUI subsystem requires Windows")
			}
			flags += " -H=windowsgui"
		}
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", flags, "-o", raw, "./cmd/"+target)
		cmd.Dir = opts.ModuleDir
		HideWindow(cmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %w\n%s", target, err, output)
		}
		final := filepath.Join(staging, name)
		if runtime.GOOS == "windows" {
			if err := addResources(raw, final, name, opts.Version, gui); err != nil {
				return err
			}
		} else if err := os.Rename(raw, final); err != nil {
			return err
		}
	}
	for _, target := range opts.Targets {
		name := target
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.Rename(filepath.Join(staging, name), filepath.Join(out, name)); err != nil {
			return fmt.Errorf("publish %s (close a running copy before rebuilding): %w", name, err)
		}
		if opts.Log != nil {
			_, _ = fmt.Fprintf(opts.Log, "%s: built %s\n", filepath.Join(out, name), opts.Version)
		}
	}
	return nil
}

func addResources(raw, final, filename, label string, gui bool) error {
	src, err := os.Open(raw)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		_ = src.Close()
		return err
	}
	writeErr := exeresources.Write(dst, src, filename, label)
	err = errors.Join(writeErr, src.Close(), dst.Close())
	if err != nil {
		return err
	}
	check, err := os.Open(final)
	if err != nil {
		return err
	}
	err = exeresources.Check(check, filename, label, gui)
	return errors.Join(err, check.Close())
}
