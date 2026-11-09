package cspfile

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// ExeName is CSP's executable.
	ExeName = "CLIPStudioPaint.exe"

	dataDirPrefix = "clipstudiopaintver"
	legacyDirName = "CLIPStudioPaintVer1_5_0"

	// maxPackageDirs bounds the Microsoft Store probe.
	maxPackageDirs = 400
)

// Roots are the folders FindDataDirs looks under. An empty one is skipped.
type Roots struct {
	// AppData is %APPDATA% (Roaming).
	AppData string
	// Documents is the user's Documents folder.
	Documents string
	// LocalAppData is %LOCALAPPDATA%, used for the Microsoft Store probe.
	LocalAppData string
}

// DataDir is one CSP user-data folder (CLIPStudioPaintVer*).
type DataDir struct {
	// Path is the folder.
	Path string
	// Name is the folder's name, e.g. CLIPStudioPaintVer1_5_0.
	Name string
	// Shortcut is the path of Shortcut\default.khc; HasShortcut says it exists.
	Shortcut    string
	HasShortcut bool
	// Tool is the path of Tool\EditImageTool.todb; HasTool says it exists.
	Tool    string
	HasTool bool
	// ShortcutModTime is the modification time of Shortcut; zero if missing.
	ShortcutModTime time.Time
}

// FindDataDirs returns the CSP data folders under roots that hold a
// shortcut file or a tool file, the one whose Shortcut\default.khc changed
// last first (folders without one last, then by path). The Microsoft Store
// probe is best effort and bounded: it reads at most a few hundred package
// folders and looks nowhere else inside them.
func FindDataDirs(roots Roots) []DataDir {
	var cands []string
	if roots.AppData != "" {
		cands = append(cands, versionDirs(filepath.Join(roots.AppData, "CELSYSUserData", "CELSYS"))...)
	}
	if roots.Documents != "" {
		cands = append(cands, filepath.Join(roots.Documents, "CELSYS", legacyDirName))
	}
	if roots.LocalAppData != "" {
		pkgs := filepath.Join(roots.LocalAppData, "Packages")
		for _, name := range readDirNames(pkgs, maxPackageDirs) {
			base := filepath.Join(pkgs, name, "LocalCache", "Roaming", "CELSYSUserData", "CELSYS")
			cands = append(cands, versionDirs(base)...)
		}
	}

	seen := map[string]bool{}
	var out []DataDir
	for _, c := range cands {
		key := strings.ToLower(filepath.Clean(c))
		if seen[key] {
			continue
		}
		seen[key] = true
		if d, ok := probeDataDir(c); ok {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.ShortcutModTime.Equal(b.ShortcutModTime) {
			return a.ShortcutModTime.After(b.ShortcutModTime)
		}
		return a.Path < b.Path
	})
	return out
}

// versionDirs lists dir's CLIPStudioPaintVer* subfolders.
func versionDirs(dir string) []string {
	var out []string
	for _, name := range readDirNames(dir, 64) {
		if strings.HasPrefix(strings.ToLower(name), dataDirPrefix) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// readDirNames returns up to limit entry names of dir, sorted; nil when dir
// cannot be read.
func readDirNames(dir string, limit int) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(limit)
	sort.Strings(names)
	return names
}

func probeDataDir(path string) (DataDir, bool) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return DataDir{}, false
	}
	d := DataDir{
		Path:     path,
		Name:     filepath.Base(path),
		Shortcut: filepath.Join(path, "Shortcut", "default.khc"),
		Tool:     filepath.Join(path, "Tool", "EditImageTool.todb"),
	}
	if fi, err := os.Stat(d.Shortcut); err == nil && fi.Mode().IsRegular() {
		d.HasShortcut = true
		d.ShortcutModTime = fi.ModTime()
	}
	if fi, err := os.Stat(d.Tool); err == nil && fi.Mode().IsRegular() {
		d.HasTool = true
	}
	return d, d.HasShortcut || d.HasTool
}

// InstallDir is a CSP installation found on disk.
type InstallDir struct {
	// Root is the folder that holds CLIPStudioPaint.exe.
	Root string
	// Exe is the path of CLIPStudioPaint.exe.
	Exe string
	// Resource is the "resource" folder next to the executable; "" if none.
	Resource string
}

// LocateInstall finds CLIPStudioPaint.exe under an install root. The
// executable may sit in the root itself or in a "CLIP STUDIO PAINT"
// subfolder of it.
func LocateInstall(root string) (InstallDir, bool) {
	for _, dir := range []string{root, filepath.Join(root, "CLIP STUDIO PAINT")} {
		exe := filepath.Join(dir, ExeName)
		if fi, err := os.Stat(exe); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		in := InstallDir{Root: dir, Exe: exe}
		res := filepath.Join(dir, "resource")
		if fi, err := os.Stat(res); err == nil && fi.IsDir() {
			in.Resource = res
		}
		return in, true
	}
	return InstallDir{}, false
}

// installDisplayPrefix is what a CSP uninstall entry's DisplayName starts with.
const installDisplayPrefix = "CLIP STUDIO PAINT"

// isInstallEntry reports whether an uninstall entry's DisplayName is CSP's.
func isInstallEntry(displayName string) bool {
	return len(displayName) >= len(installDisplayPrefix) &&
		strings.EqualFold(displayName[:len(installDisplayPrefix)], installDisplayPrefix)
}

// maxInstallProbe bounds the folders listed under an InstallLocation.
const maxInstallProbe = 64

// InstallFromEntry finds CSP from the three values of an uninstall entry.
// displayName must be CSP's. The executable named by displayIcon (the icon
// resource, possibly quoted and with a ",N" suffix) wins when its file name is
// CLIPStudioPaint.exe; otherwise installLocation is probed: itself, its
// "CLIP STUDIO PAINT" subfolder, and "<installLocation>\CLIP STUDIO*\CLIP
// STUDIO PAINT" (the layout of CSP 5, whose InstallLocation is the folder
// above "CLIP STUDIO 1.5"). Only the file system is read.
func InstallFromEntry(displayName, installLocation, displayIcon string) (InstallDir, bool) {
	if !isInstallEntry(displayName) {
		return InstallDir{}, false
	}
	if exe := iconExe(displayIcon); exe != "" {
		if in, ok := LocateInstall(filepath.Dir(exe)); ok && strings.EqualFold(filepath.Base(in.Exe), ExeName) &&
			strings.EqualFold(filepath.Clean(in.Exe), filepath.Clean(exe)) {
			return in, true
		}
	}
	if installLocation == "" {
		return InstallDir{}, false
	}
	if fi, err := os.Stat(installLocation); err != nil || !fi.IsDir() {
		return InstallDir{}, false
	}
	if in, ok := LocateInstall(installLocation); ok {
		return in, true
	}
	for _, name := range readDirNames(installLocation, maxInstallProbe) {
		if !strings.HasPrefix(strings.ToUpper(name), "CLIP STUDIO") {
			continue
		}
		if in, ok := LocateInstall(filepath.Join(installLocation, name, "CLIP STUDIO PAINT")); ok {
			return in, true
		}
	}
	return InstallDir{}, false
}

// iconExe extracts the executable path from a DisplayIcon value: quotes and
// a trailing ",index" are removed. It returns "" unless the file name is
// CLIPStudioPaint.exe.
func iconExe(icon string) string {
	icon = strings.TrimSpace(icon)
	if strings.HasPrefix(icon, `"`) {
		if end := strings.Index(icon[1:], `"`); end >= 0 {
			icon = icon[1 : 1+end]
		}
	} else if i := strings.LastIndex(icon, ","); i >= 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(icon[i+1:])); err == nil {
			icon = icon[:i]
		}
	}
	icon = strings.TrimSpace(icon)
	// The path may be written with either separator.
	base := icon
	if i := strings.LastIndexAny(base, `\/`); i >= 0 {
		base = base[i+1:]
	}
	if !strings.EqualFold(base, ExeName) {
		return ""
	}
	return icon
}
