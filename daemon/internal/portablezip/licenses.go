package portablezip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

type moduleInfo struct {
	Path, Version, Dir string
	Replace            *moduleInfo
}

const britishLicense = "LICENCE" //nolint:misspell // Upstream license files also use British spelling.

func collectRuntimeLicenses(ctx context.Context, moduleDir, goVersion string, modules map[string]*debug.Module, log io.Writer) ([]entry, error) {
	// The executables, not packagezip's own imports, determine runtime notices.
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = moduleDir
	buildtool.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("list module license sources: %w\n%s", err, exit.Stderr)
		}
		return nil, err
	}
	available := make(map[string]moduleInfo)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var m moduleInfo
		if err := decoder.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		available[m.Path] = m
	}
	var paths []string
	for path := range modules {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	var entries []entry
	var index strings.Builder
	_, _ = fmt.Fprintf(&index, "Third-party runtime components in this archive\n\nGo toolchain: %s\nSDL: %s (licenses/SDL3-LICENSE.txt)\n\n", goVersion, sdlpin.Version)
	for _, path := range paths {
		built := modules[path]
		listed, ok := available[path]
		if !ok {
			return nil, fmt.Errorf("license source unavailable for built module %s@%s", path, built.Version)
		}
		source := built
		if source.Replace != nil {
			source = source.Replace
		}
		actual := &listed
		if actual.Replace != nil {
			actual = actual.Replace
		}
		if actual.Path != source.Path || actual.Version != source.Version || actual.Dir == "" {
			return nil, fmt.Errorf("license source differs from built module %s@%s", path, built.Version)
		}
		label := path + "@" + built.Version
		prefix := "licenses/go/" + label
		licenseEntries, err := moduleLicenses(actual.Dir, prefix)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(&index, "%s\n  source: %s@%s\n  module checksum: %s\n", label, source.Path, source.Version, source.Sum)
		if len(licenseEntries) == 0 {
			// Existing upstream gaps are recorded as facts, never assigned a license.
			notice := fmt.Sprintf("%s\n\nThe module source used for this build contains no root license, copying or notice file.\nNo license is asserted by this notice. Authorization for redistribution remains to be confirmed.\n\nSource: https://pkg.go.dev/%s@%s\nModule checksum: %s\n", label, source.Path, source.Version, source.Sum)
			licenseEntries = []entry{{name: prefix + "/NOTICE-NO-LICENSE.txt", data: []byte(notice)}}
			_, _ = fmt.Fprintln(&index, "  upstream license/notice missing; redistribution authorization pending")
			if log != nil {
				_, _ = fmt.Fprintf(log, "license notice: %s has no root LICENSE/NOTICE; recorded in third-party index\n", label)
			}
		}
		for _, e := range licenseEntries {
			_, _ = fmt.Fprintf(&index, "  %s\n", e.name)
		}
		_, _ = fmt.Fprintln(&index)
		entries = append(entries, licenseEntries...)
	}
	goEnv := exec.CommandContext(ctx, "go", "env", "-json", "GOROOT", "GOVERSION")
	buildtool.HideWindow(goEnv)
	envData, err := goEnv.Output()
	if err != nil {
		return nil, err
	}
	var env struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(envData, &env); err != nil {
		return nil, err
	}
	if goVersion != env.GOVERSION {
		return nil, fmt.Errorf("go license source uses %s, binaries use %s; rebuild with the current toolchain", env.GOVERSION, goVersion)
	}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		data, err := snapshot(env.GOROOT, name)
		if errors.Is(err, os.ErrNotExist) && name == "PATENTS" {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{name: "licenses/Go/" + name, data: data})
	}
	entries = append(entries, entry{name: "licenses/THIRD-PARTY.txt", data: []byte(index.String())})
	return entries, nil
}

func moduleLicenses(root, prefix string) ([]entry, error) {
	files, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, file := range files {
		upper := strings.ToUpper(file.Name())
		base, _, _ := strings.Cut(upper, ".")
		if base != "LICENSE" && base != britishLicense && base != "COPYING" && base != "NOTICE" {
			continue
		}
		data, err := snapshot(root, file.Name())
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{name: prefix + "/" + file.Name(), data: data})
	}
	return entries, nil
}
