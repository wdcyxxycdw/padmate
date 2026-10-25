package portablezip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// Options identifies already-built files and the repository's distributable data.
type Options struct {
	Version, Root, BinDir, OutDir string
	Log                           io.Writer
}

// File is one regular file of a distributable tree, named by a
// slash-separated path relative to the tree's root.
type File struct {
	Name string
	Data []byte
}

// Pack validates immutable copies of the Windows binaries and pinned SDL DLL,
// adds examples, documentation and runtime licenses, and publishes by rename.
func Pack(ctx context.Context, opts Options) (string, error) {
	entries, err := collect(ctx, opts)
	if err != nil {
		return "", err
	}
	out, err := filepath.Abs(opts.OutDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	prefix := "padmate-" + opts.Version + "-windows-amd64"
	file := filepath.Join(out, prefix+".zip")
	if err := writeArchive(ctx, file, prefix, entries); err != nil {
		return "", err
	}
	if opts.Log != nil {
		_, _ = fmt.Fprintf(opts.Log, "%s: %d files, SDL %s\n", file, len(entries)+1, sdlpin.Version)
	}
	return file, nil
}

// Collect returns the files Pack archives, without SHA256SUMS.txt, in the
// archive's layout: padmated.exe, padmatectl.exe and SDL3.dll at the root,
// next to examples/, docs/, licenses/ and the repository's top-level files.
// It checks what Pack checks (build version, resources, subsystem, the
// pinned SDL hash, license sources) and reads every file once, so the bytes
// it returns are the bytes that were checked. OutDir is not used. The
// installer (PKG-03) lays the same files out under its resources folder.
func Collect(ctx context.Context, opts Options) ([]File, error) {
	entries, err := collect(ctx, opts)
	if err != nil {
		return nil, err
	}
	files := make([]File, len(entries))
	for i, e := range entries {
		files[i] = File{Name: e.name, Data: e.data}
	}
	return files, nil
}

func collect(ctx context.Context, opts Options) ([]entry, error) {
	if err := publicmirror.RefusePackagingAt(opts.Root); err != nil {
		return nil, err
	}
	if _, err := exeresources.FixedVersion(opts.Version); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	bin, err := filepath.Abs(opts.BinDir)
	if err != nil {
		return nil, err
	}
	entries, err := collectRepoFiles(root)
	if err != nil {
		return nil, err
	}
	modules := make(map[string]*debug.Module)
	goVersion := ""
	for _, filename := range []string{"padmated.exe", "padmatectl.exe"} {
		data, err := snapshot(bin, filename)
		if err != nil {
			return nil, err
		}
		if err := exeresources.Check(bytes.NewReader(data), filename, opts.Version, filename == "padmated.exe"); err != nil {
			return nil, err
		}
		info, err := buildinfo.Read(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s Go build info: %w", filename, err)
		}
		if goVersion != "" && goVersion != info.GoVersion {
			return nil, errors.New("package binaries use different Go toolchains")
		}
		goVersion = info.GoVersion
		for _, module := range info.Deps {
			if previous := modules[module.Path]; previous != nil && previous.Version != module.Version {
				return nil, fmt.Errorf("package binaries use different versions of %s", module.Path)
			}
			modules[module.Path] = module
		}
		entries = append(entries, entry{name: filename, data: data})
	}
	dll, err := snapshot(bin, sdlpin.DLLName)
	if err != nil {
		return nil, err
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(dll)); got != sdlpin.DLLSHA256 {
		return nil, fmt.Errorf("%s SHA-256=%s, want pinned SDL %s (%s)", sdlpin.DLLName, got, sdlpin.Version, sdlpin.DLLSHA256)
	}
	entries = append(entries, entry{name: sdlpin.DLLName, data: dll})
	licenses, err := collectRuntimeLicenses(ctx, filepath.Join(root, "daemon"), goVersion, modules, opts.Log)
	if err != nil {
		return nil, err
	}
	return append(entries, licenses...), nil
}
