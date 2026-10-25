package dtsgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// MainEntry is the entry file of a new script project.
const MainEntry = "main.ts"

// MainTemplate is main.ts of a new script project. It binds no control, so a
// profile that references it takes over no key until the user writes a
// binding; the example binding stays commented out. Its comments address the
// user, in Chinese like the UI.
const MainTemplate = `// padmate 脚本（API 0.21）。保存后自动重新加载。
// 可用的函数和类型见 padmate.d.ts；用 VS Code 打开这个文件夹可以补全和检查。
import { key, on } from "padmate";

// 示例：按下 south 键（Xbox 的 A）时发送 Ctrl+Z。
// 删掉下一行开头的 // 就会生效，之后这个键由脚本处理。
// on("south", "press", async () => { await key.tap("Ctrl+Z"); });
`

// File is one file of a script project, named relative to the script root.
type File struct {
	Name string
	Data []byte
}

// Project returns the files of a script project for api whose entry file is
// entry: entry itself with the contents main when main is not nil, then a
// hermetic tsconfig.json, which does not depend on entry, the padmate
// declarations filtered for api, a globals declaration and the goja standard
// library declarations.
// source and lib default to the embedded declarations and the generated goja
// lib; lib is copied unchanged.
func Project(api Version, entry string, main, source, lib []byte) ([]File, error) {
	if err := ValidateHostAPI(api); err != nil {
		return nil, err
	}
	// Separators of every platform: the project is also written on Linux.
	if entry == "" || entry == "." || entry == ".." || strings.ContainsAny(entry, `/\:`) {
		return nil, fmt.Errorf("dtsgen: entry %q must be a file name", entry)
	}
	if source == nil {
		source = defaultDeclarations
	}
	filtered, err := Filter(source, api)
	if err != nil {
		return nil, err
	}
	if len(lib) == 0 {
		lib = compile.GojaLib()
	}
	tsconfig, err := projectConfig()
	if err != nil {
		return nil, err
	}
	var files []File
	if main != nil {
		files = append(files, File{Name: entry, Data: main})
	}
	return append(files,
		File{Name: "tsconfig.json", Data: tsconfig},
		File{Name: "padmate.d.ts", Data: filtered},
		File{Name: "padmate-globals.d.ts", Data: []byte(globalsDeclarations)},
		File{Name: "lib.padmate-goja.d.ts", Data: lib},
	), nil
}

// projectConfig is the tsconfig.json for editors and tsc. padmate builds
// scripts with esbuild and never reads it; noEmit keeps tsc from writing
// JavaScript next to the sources. It names no entry: include covers every
// .ts and .js file in the folder and its subfolders, the declarations
// among them, and allowJs admits JavaScript entries. A folder holds any
// number of scripts, and the file is only created when missing, never
// rewritten (ScaffoldFS), so a script imported or created later must be
// covered without a change to it.
func projectConfig() ([]byte, error) {
	data, err := json.MarshalIndent(map[string]any{
		"compilerOptions": map[string]any{
			"target": "ESNext", "module": "ESNext", "moduleResolution": "Bundler", "strict": true, "noEmit": true,
			"allowJs": true, "lib": []string{}, "types": []string{},
		},
		"include": []string{"**/*.ts", "**/*.js"},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ScaffoldFS creates each of files that does not exist in root yet and
// leaves every existing one as it is; it returns the names of both, in the
// order of files. Files are created through root with O_CREATE|O_EXCL, so
// nothing is overwritten, an existing link counts as existing and is never
// followed, and no name escapes root. Each file is synced before the next.
// A file whose write fails is removed and the error returned; files created
// before it stay, and calling ScaffoldFS again completes the project.
func ScaffoldFS(root *os.Root, files []File) (created, existing []string, err error) {
	created, existing = []string{}, []string{}
	for _, f := range files {
		out, err := root.OpenFile(f.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			existing = append(existing, f.Name)
			continue
		}
		if err != nil {
			return created, existing, err
		}
		_, err = out.Write(f.Data)
		if err == nil {
			err = out.Sync()
		}
		if err := errors.Join(err, out.Close()); err != nil {
			if removeErr := root.Remove(f.Name); removeErr != nil {
				err = errors.Join(err, removeErr)
			}
			return created, existing, err
		}
		created = append(created, f.Name)
	}
	return created, existing, nil
}

// Scaffold writes a new script project with the neutral MainTemplate, its
// first line naming api, into dir, creating dir when needed. It refuses, writing nothing, when any of
// the project's files exists. lib is optional; when provided it is copied
// unchanged.
func Scaffold(dir string, api Version, source, lib []byte) error {
	if err := ValidateHostAPI(api); err != nil {
		return err
	}
	if dir == "" {
		return errors.New("dtsgen: output directory is required")
	}
	main := strings.Replace(MainTemplate, "API "+HostAPI.String(), "API "+api.String(), 1)
	files, err := Project(api, MainEntry, []byte(main), source, lib)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range files {
		if _, err := root.Lstat(f.Name); err == nil {
			return fmt.Errorf("dtsgen: refusing to overwrite %s", filepath.Join(dir, f.Name))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// Another writer may create a file between the check and the write;
	// ScaffoldFS still leaves it alone.
	_, existing, err := ScaffoldFS(root, files)
	if err == nil && len(existing) > 0 {
		err = fmt.Errorf("dtsgen: refusing to overwrite %s", filepath.Join(dir, existing[0]))
	}
	return err
}
