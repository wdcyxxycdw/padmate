package installerpkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// AppFile is the Electron executable at the root of an installation.
const AppFile = ProductName + ".exe"

// binFolder is where the daemon, the CLI and SDL3.dll sit below the root of an
// installation. The Electron app's runtime options and the daemon's
// ownwindow.TrustedPaths expect exactly this layout.
const binFolder = "resources/bin"

// RequiredResources are the files an installation has in its resources
// folder besides the three in binFolder: padmate's own license, the
// third-party notices, the examples and the schema. The documents are checked
// apart (documentProblems), since an earlier build has others.
var RequiredResources = []string{
	"LICENSE", "README.md", "README.en.md",
	"licenses/README.md", "licenses/THIRD-PARTY.txt", UIIndexFile, "licenses/SDL3-LICENSE.txt", "licenses/Go/LICENSE",
	"schema/profile.schema.json", "examples/profiles/basic.json", "examples/traces/basic.jsonl",
}

// ForbiddenFiles must not be in an installation: developer tools.
var ForbiddenFiles = []string{"resources/bin/padmate-probe.exe", "padmate-probe.exe"}

// TreeOptions says what CheckTree reads.
type TreeOptions struct {
	// Root is the folder that holds padmate.exe: electron-builder's
	// win-unpacked, or an installation.
	Root string
	Tag  Tag
	// Run also runs `padmated -version` and `padmatectl version` from the
	// tree (Windows; they open no window and send no keys).
	Run bool
	// EarlierBuild says that the tree was built from an earlier source than this
	// one (the first installer of an upgrade test): the imports and Windows
	// functions of padmated.exe and padmatectl.exe are then not compared with
	// the goldens of this source (PKG-11), which a release between the two may
	// have changed, and its documents are not checked (v1.0.0 and earlier
	// installed all of docs/). Its resources\app-update.yml may also name the
	// source of its time, GitHub up to v1.0.2 (CheckEarlierUpdateConfig). The
	// rest of the PE check still applies.
	EarlierBuild bool
	// NoUpdateConfig leaves out resources\app-update.yml, which a release
	// from before the automatic update (PKG-10) does not have: the first
	// installer of an upgrade test may be one. A file that is there is still read.
	NoUpdateConfig bool
}

// CheckTree reads the version and the files of an installation (or of the
// tree it is installed from) and returns what is wrong with it, one
// message per problem, none when it is complete and every part has the
// version of the tag (PKG-06): padmate.exe, its package.json inside
// app.asar, and the daemon and CLI that sit in resources\bin.
func CheckTree(ctx context.Context, opts TreeOptions) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	path := func(name string) string { return filepath.Join(opts.Root, filepath.FromSlash(name)) }

	if info, err := readVersionInfoFile(path(AppFile)); err != nil {
		add("%s: %v", AppFile, err)
	} else {
		checkElectronVersion(add, AppFile, info, opts.Tag, numericProductVersion)
		for _, p := range checkShippedExecutable(path(AppFile), info.Strings) {
			add("%s: %s", AppFile, p)
		}
	}
	if asar, err := os.Open(path("resources/app.asar")); err != nil {
		add("resources/app.asar: %v", err)
	} else {
		data, err := ReadAsarFile(asar, "package.json")
		// The main process is one file that needs nothing installed (PKG-10).
		bundle, bundleErr := ReadAsarFile(asar, MainBundle)
		_ = asar.Close()
		var pkg struct{ Name, Version, Main string }
		switch {
		case err != nil:
			add("resources/app.asar: %v", err)
		case json.Unmarshal(data, &pkg) != nil:
			add("resources/app.asar: package.json is not JSON")
		default:
			if pkg.Version != opts.Tag.App {
				add("resources/app.asar: package.json version %q, want %q", pkg.Version, opts.Tag.App)
			}
			if pkg.Name != ProductName {
				add("resources/app.asar: package.json name %q, want %q", pkg.Name, ProductName)
			}
			if pkg.Main != "out/main/index.js" {
				add("resources/app.asar: package.json main %q, want out/main/index.js", pkg.Main)
			}
			for _, p := range mainBundleProblems(bundle, bundleErr) {
				add("%s", p)
			}
		}
	}
	// The source of the automatic update (the download site) and the folder the
	// installer cleans.
	updateFile := path("resources/app-update.yml")
	if _, err := os.Stat(updateFile); !opts.NoUpdateConfig || err == nil {
		for _, p := range updateConfigProblems(updateFile, opts.EarlierBuild) {
			add("%s", p)
		}
	}
	for _, exe := range []string{"padmated.exe", "padmatectl.exe"} {
		name := binFolder + "/" + exe
		f, err := os.Open(path(name))
		if err != nil {
			add("%s: %v", name, err)
			continue
		}
		err = exeresources.Check(f, exe, opts.Tag.Label, exe == "padmated.exe")
		_ = f.Close()
		if err != nil {
			add("%s: %v", name, err)
		}
		// The same check the build ran on bin/ (task pe:check), now on the file as
		// it is packed or installed, signature included.
		for _, p := range checkGoExecutable(path(name), strings.TrimSuffix(exe, ".exe"), opts.EarlierBuild) {
			add("%s: %s", name, p)
		}
	}
	if sum, err := fileSHA256(path(binFolder + "/" + sdlpin.DLLName)); err != nil {
		add("%s/%s: %v", binFolder, sdlpin.DLLName, err)
	} else if sum != sdlpin.DLLSHA256 {
		add("%s/%s: SHA-256 %s, want the pinned SDL %s (%s)", binFolder, sdlpin.DLLName, sum, sdlpin.Version, sdlpin.DLLSHA256)
	}
	for _, name := range RequiredResources {
		if info, err := os.Stat(path("resources/" + name)); err != nil || !info.Mode().IsRegular() {
			add("resources/%s: missing", name)
		}
	}
	for _, name := range ForbiddenFiles {
		if _, err := os.Lstat(path(name)); err == nil {
			add("%s: must not be installed", name)
		}
	}
	if !opts.EarlierBuild {
		problems = append(problems, documentProblems(opts.Root)...)
	}
	problems = append(problems, longPaths(opts.Root)...)
	if opts.Run {
		for _, c := range []struct{ exe, arg string }{{"padmated.exe", "-version"}, {"padmatectl.exe", "version"}} {
			out, err := runQuiet(ctx, path(binFolder+"/"+c.exe), c.arg)
			want := strings.TrimSuffix(c.exe, ".exe") + " " + opts.Tag.Label
			if err != nil {
				add("%s %s: %v", c.exe, c.arg, err)
			} else if !strings.HasPrefix(strings.TrimSpace(out), want) {
				add("%s %s printed %q, want it to start with %q", c.exe, c.arg, strings.TrimSpace(out), want)
			}
		}
	}
	return problems
}

// CheckInstaller checks the installer file of a release: its name, the
// product version NSIS wrote into its version resource, and that it asks for
// no administrator rights (PKG-03, PKG-13).
func CheckInstaller(file string, tag Tag) []string {
	var problems []string
	if got := filepath.Base(file); got != tag.InstallerName() {
		problems = append(problems, fmt.Sprintf("installer is named %q, want %q", got, tag.InstallerName()))
	}
	info, err := readVersionInfoFile(file)
	if err != nil {
		return append(problems, fmt.Sprintf("%s: %v", filepath.Base(file), err))
	}
	checkElectronVersion(func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }, filepath.Base(file), info, tag, appProductVersion)
	for _, p := range checkShippedExecutable(file, info.Strings) {
		problems = append(problems, filepath.Base(file)+": "+p)
	}
	if level, err := installerExecutionLevel(file); err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", filepath.Base(file), err))
	} else if level != "asInvoker" {
		problems = append(problems, fmt.Sprintf("%s: the manifest asks for execution level %q, want asInvoker: the installer is for the current user and never asks for administrator rights", filepath.Base(file), level))
	}
	return problems
}

// manifestScan is how much of the installer is searched for its manifest. NSIS
// writes it into the resources of its stub, which is at the start.
const manifestScan = 4 << 20

// installerExecutionLevel reads the requestedExecutionLevel of the manifest at
// the start of an installer, "" when it has none.
func installerExecutionLevel(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, manifestScan))
	if err != nil {
		return "", err
	}
	return executionLevelOf(data), nil
}

// executionLevelOf finds `requestedExecutionLevel level="..."` in data.
func executionLevelOf(data []byte) string {
	const marker = `requestedExecutionLevel level="`
	i := bytes.Index(data, []byte(marker))
	if i < 0 {
		return ""
	}
	rest := data[i+len(marker):]
	j := bytes.IndexByte(rest, 0x22)
	if j < 0 || j > 32 {
		return ""
	}
	return string(rest[:j])
}

// checkElectronVersion compares the version resource electron-builder wrote
// with the tag: the string FileVersion is the app's version, without the "v"
// (and so has the prerelease, which a number cannot), and the numeric
// versions begin with the tag's three numbers. The installer's ProductVersion
// is the app's version too, but rcedit writes padmate.exe's as the four
// numbers ("0.14.0.0"), which is what product tells.
func checkElectronVersion(add func(string, ...any), name string, info VersionInfo, tag Tag, product productVersion) {
	want, err := exeresources.FixedVersion(tag.Label)
	if err != nil {
		add("%s: %v", name, err)
		return
	}
	if got := info.Strings["FileVersion"]; got != tag.App {
		add("%s: FileVersion %q, want %q", name, got, tag.App)
	}
	got := info.Strings["ProductVersion"]
	switch {
	case product == numericProductVersion && got == fmt.Sprintf("%d.%d.%d.0", want[0], want[1], want[2]):
	case got == tag.App:
	default:
		add("%s: ProductVersion %q, want %q", name, got, tag.App)
	}
	for what, got := range map[string][4]uint16{"numeric file version": info.File, "numeric product version": info.Product} {
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			add("%s: %s %d.%d.%d.%d, want %d.%d.%d.x", name, what, got[0], got[1], got[2], got[3], want[0], want[1], want[2])
		}
	}
}

// productVersion says which ProductVersion string a file may have.
type productVersion int

const (
	// appProductVersion is the app's version, as the installer has it.
	appProductVersion productVersion = iota
	// numericProductVersion also allows MAJOR.MINOR.PATCH.0, as padmate.exe
	// has it.
	numericProductVersion
)

func readVersionInfoFile(file string) (VersionInfo, error) {
	f, err := os.Open(file)
	if err != nil {
		return VersionInfo{}, err
	}
	defer func() { _ = f.Close() }()
	return ReadVersionInfo(f)
}

// longPaths lists the files and folders below root whose path inside the
// installation, with the backslash in front of it, is longer than
// MaxRelativePath characters (as Windows counts them, in UTF-16 units). The
// wizard leaves that much room when it checks the folder a person chose, so a
// longer one would make an installation fail halfway (PKG-13).
func longPaths(root string) []string {
	var found []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if n := len(utf16.Encode([]rune(rel))) + 1; rel != "." && n > MaxRelativePath {
			found = append(found, fmt.Sprintf("%s: %d characters inside the installation, the wizard leaves room for %d (MaxRelativePath)", filepath.ToSlash(rel), n, MaxRelativePath))
		}
		return nil
	})
	// A folder that is not there is reported by the checks of its files.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		found = append(found, fmt.Sprintf("reading the paths below %s: %v", root, err))
	}
	return found
}

func fileSHA256(file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// runQuiet runs exe with one argument, hidden, and returns what it printed.
func runQuiet(ctx context.Context, exe, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, arg)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	buildtool.HideWindow(cmd)
	if err := cmd.Run(); err != nil {
		return out.String(), errors.Join(err, ctx.Err())
	}
	return out.String(), nil
}
