package installerpkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/portablezip"
)

// UIIndexFile is the list of the npm packages bundled into the window, in the
// installation's resources folder.
const UIIndexFile = "licenses/THIRD-PARTY-UI.txt"

// maxLicenseFile bounds a license file read from node_modules.
const maxLicenseFile = 1 << 20

type npmPackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	License      json.RawMessage   `json:"license"`
	Dependencies map[string]string `json:"dependencies"`
}

func readNPMPackage(dir string) (npmPackage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return npmPackage{}, err
	}
	var pkg npmPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return npmPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, "package.json"), err)
	}
	return pkg, nil
}

// licenseName is the license of a package as it names it: an SPDX string, or
// the type of the older object form.
func (p npmPackage) licenseName() string {
	var name string
	if json.Unmarshal(p.License, &name) == nil && name != "" {
		return name
	}
	var object struct{ Type string }
	if json.Unmarshal(p.License, &object) == nil && object.Type != "" {
		return object.Type
	}
	return "not stated"
}

// resolveNPMPackage finds the folder of the package name that the package in
// from (the application itself, or the folder of a package) requires, the way
// Node does: in from's own node_modules, then in the node_modules of each
// folder above it up to appDir. npm keeps a copy next to the package that needs
// another version of it than the one at the top.
func resolveNPMPackage(appDir, from, name string) (string, bool) {
	rel := filepath.FromSlash(name)
	for dir := from; ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) != "node_modules" {
			candidate := filepath.Join(dir, "node_modules", rel)
			if info, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil && info.Mode().IsRegular() {
				return candidate, true
			}
		}
		if dir == appDir || filepath.Dir(dir) == dir {
			return "", false
		}
	}
}

// collectUILicenses returns the license files of the npm packages that the
// window bundles: the dependencies of app/package.json (the development
// dependencies are tools of the build) and, in turn, theirs, as they are
// installed in app/node_modules, each the copy its requirer finds
// (resolveNPMPackage). The files go to licenses/ui/NAME@VERSION/ and an index
// to UIIndexFile, which names a package without a license file as such; no
// license is assumed.
func collectUILicenses(appDir string) ([]portablezip.File, error) {
	root, err := readNPMPackage(appDir)
	if err != nil {
		return nil, fmt.Errorf("the application's package.json: %w", err)
	}
	type requirement struct{ name, from string }
	var queue []requirement
	for _, name := range sortedKeys(root.Dependencies) {
		queue = append(queue, requirement{name, appDir})
	}
	seenDir := map[string]bool{}
	seenLabel := map[string]bool{}
	var files []portablezip.File
	var lines []string
	for len(queue) > 0 {
		need := queue[0]
		queue = queue[1:]
		name := need.name
		if name == "" || strings.Contains(name, "..") || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("dependency name %q is not a package name", name)
		}
		dir, exists := resolveNPMPackage(appDir, need.from, name)
		if !exists {
			return nil, fmt.Errorf("runtime dependency %s is not installed (run task app:install): %w", name, os.ErrNotExist)
		}
		if seenDir[dir] {
			continue
		}
		seenDir[dir] = true
		pkg, err := readNPMPackage(dir)
		if err != nil {
			return nil, fmt.Errorf("runtime dependency %s is not installed (run task app:install): %w", name, err)
		}
		label := name + "@" + pkg.Version
		// Two copies of one version, in two places, are one entry; what they
		// need is still followed.
		if seenLabel[label] {
			for _, dep := range sortedKeys(pkg.Dependencies) {
				queue = append(queue, requirement{dep, dir})
			}
			continue
		}
		seenLabel[label] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var found []string
		for _, e := range entries {
			base, _, _ := strings.Cut(strings.ToUpper(e.Name()), ".")
			if !e.Type().IsRegular() || (base != "LICENSE" && base != britishLicense && base != "COPYING" && base != "NOTICE") {
				continue
			}
			data, err := readLimited(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			files = append(files, portablezip.File{Name: "licenses/ui/" + label + "/" + e.Name(), Data: data})
			found = append(found, e.Name())
		}
		line := fmt.Sprintf("%s\n  license: %s\n", label, pkg.licenseName())
		if len(found) == 0 {
			line += "  no license file in the package; redistribution terms are those it states above\n"
		}
		for _, f := range found {
			line += "  licenses/ui/" + label + "/" + f + "\n"
		}
		lines = append(lines, line)
		for _, dep := range sortedKeys(pkg.Dependencies) {
			queue = append(queue, requirement{dep, dir})
		}
	}
	slices.Sort(lines)
	index := "Third-party components bundled into the padmate window (the renderer): the\n" +
		"dependencies of app/package.json and theirs, as installed. Electron and Chromium\n" +
		"come with their own notices, LICENSE.electron.txt and LICENSES.chromium.html at the\n" +
		"installation root.\n\n" + strings.Join(lines, "\n")
	files = append(files, portablezip.File{Name: UIIndexFile, Data: []byte(index)})
	slices.SortFunc(files, func(a, b portablezip.File) int { return strings.Compare(a.Name, b.Name) })
	return files, nil
}

// britishLicense is the spelling some packages use for their license file.
const britishLicense = "LICENCE" //nolint:misspell // Upstream license files also use British spelling.

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func readLimited(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxLicenseFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLicenseFile {
		return nil, errors.New(file + " is larger than 1 MiB")
	}
	return data, nil
}
