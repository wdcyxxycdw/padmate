package installerpkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/branding"
	"github.com/wdcyxxycdw/padmate/daemon/internal/portablezip"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

// stageMarker is the file that tells Stage a folder is one it made, and so
// may be emptied. It is the only protection against a mistyped -out.
const stageMarker = ".padmate-installer-stage"

// IconFile and ResourcesFolder are what electron-builder.yml reads from a
// stage folder: the application icon and the tree it copies into
// resources\ (extraResources).
const (
	IconFile        = "icon.ico"
	ResourcesFolder = "resources"
)

// StageOptions names the inputs of Stage.
type StageOptions struct {
	Tag Tag
	// Root is the repository root, BinDir the folder with the built
	// padmated.exe, padmatectl.exe and SDL3.dll (as `task package:zip` takes
	// them), Out the stage folder to make.
	Root, BinDir, Out string
	Log               io.Writer
}

// Stage writes what the installer packs next to the Electron app (PKG-03):
// the files of the portable preview, which portablezip checks (the tag's
// version in both executables, the pinned SDL3.dll, the license of every
// linked module), the licenses of the npm packages the window bundles
// (app/node_modules, so run task app:install first), laid out as the
// installed tree wants them, and the application icon. The three binaries go
// to resources\bin, where the app looks for them; examples, the user
// documents (shipsFile), licenses, schema and padmate's own README and LICENSE
// go beside it in resources.
//
// A folder that already exists is emptied, if Stage made it (it carries a
// marker file) or it is empty; any other folder is left alone and reported.
func Stage(ctx context.Context, opts StageOptions) error {
	if err := publicmirror.RefusePackagingAt(opts.Root); err != nil {
		return err
	}
	files, err := portablezip.Collect(ctx, portablezip.Options{Version: opts.Tag.Label, Root: opts.Root, BinDir: opts.BinDir, Log: opts.Log})
	if err != nil {
		return err
	}
	// The window bundles npm packages of its own, which the portable preview
	// does not have: their licenses come with it.
	ui, err := collectUILicenses(filepath.Join(opts.Root, "app"))
	if err != nil {
		return err
	}
	files = append(files, ui...)
	icon, err := branding.ICO()
	if err != nil {
		return err
	}
	out, err := filepath.Abs(opts.Out)
	if err != nil {
		return err
	}
	if err := prepareStage(out); err != nil {
		return err
	}
	staged := make(map[string]bool)
	for _, f := range files {
		dst, ok := stagedName(f.Name)
		if !ok {
			continue
		}
		if err := writeStaged(out, dst, f.Data); err != nil {
			return err
		}
		staged[f.Name] = true
	}
	if missing := missingUserDocuments(staged); len(missing) > 0 {
		return fmt.Errorf("the user documents %s are missing in %s", strings.Join(missing, ", "), opts.Root)
	}
	if err := writeStaged(out, IconFile, icon); err != nil {
		return err
	}
	if err := writeStaged(out, stageMarker, []byte("made by packageinstaller stage; may be emptied by it\n")); err != nil {
		return err
	}
	if opts.Log != nil {
		_, _ = fmt.Fprintf(opts.Log, "%s: %d files for %s\n", out, len(staged), opts.Tag.Label)
	}
	return nil
}

// stagedName is where a file of the portable preview goes, relative to the
// stage folder; false for the files the installer does not take (shipsFile).
func stagedName(name string) (string, bool) {
	if !shipsFile(name) {
		return "", false
	}
	switch name {
	case "padmated.exe", "padmatectl.exe", "SDL3.dll":
		return path.Join(ResourcesFolder, "bin", name), true
	}
	return path.Join(ResourcesFolder, name), true
}

func prepareStage(out string) error {
	entries, err := os.ReadDir(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.MkdirAll(out, 0o755)
	case err != nil:
		return err
	case len(entries) == 0:
		return nil
	}
	if _, err := os.Stat(filepath.Join(out, stageMarker)); err != nil {
		return fmt.Errorf("%s is not empty and was not made by this tool; refusing to empty it", out)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(out, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func writeStaged(out, name string, data []byte) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.Contains(name, `\`) {
		return fmt.Errorf("unsafe staged path %q", name)
	}
	file := filepath.Join(out, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}
