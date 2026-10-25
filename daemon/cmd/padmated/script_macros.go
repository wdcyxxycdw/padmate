package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// buildMacroPrograms builds the macros files, names in the macros folder of
// root (the data folder's script library, SCR-51, or the folder of a
// --profile file), with the script API api (SCR-50, design §5.1). A
// file that is not in the folder under exactly its name is
// script.ErrMacroNotFound: a binding never runs a file whose name differs
// in case, or an 8.3 short name of another. Each program is compiled with
// the bare file name, so goja finds the sources of its source map, and its
// Version is the sha256 of the file, which re-enables a macro disabled
// after an interruption once the file changes. A file that fails does not
// stop the others.
func buildMacroPrograms(ctx context.Context, root string, files []string, api string) []script.MacroProgram {
	out := make([]script.MacroProgram, len(files))
	present := macroDirFiles(root)
	byFile := make(map[string]*script.MacroProgram, len(files))
	var build []string
	for i, file := range files {
		out[i].File = file
		byFile[file] = &out[i]
		if present[file] {
			build = append(build, file)
		} else {
			out[i].Err = script.ErrMacroNotFound
		}
	}
	if len(build) == 0 || ctx.Err() != nil {
		for _, file := range build {
			byFile[file].Err = ctx.Err()
		}
		return out
	}
	for _, res := range compile.BuildMacros(filepath.Join(root, profile.MacroDir), build, api) {
		mp := byFile[res.File]
		if mp == nil {
			continue
		}
		mp.Version = res.Key
		if data, err := os.ReadFile(filepath.Join(root, profile.MacroDir, res.File)); err == nil {
			sum := sha256.Sum256(data)
			mp.Version = hex.EncodeToString(sum[:])
		}
		if res.Err != nil {
			mp.Err = res.Err
			continue
		}
		mp.Program, mp.Err = goja.Compile(res.File, string(res.Result.JS), false)
	}
	return out
}

// macroDirFiles returns the regular files at the top of the macros folder
// of root, by their exact names. A missing or unreadable folder has none.
func macroDirFiles(root string) map[string]bool {
	entries, err := os.ReadDir(filepath.Join(root, profile.MacroDir))
	if err != nil {
		return nil
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			files[e.Name()] = true
		}
	}
	return files
}
