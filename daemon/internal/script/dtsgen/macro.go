package dtsgen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// MacroTemplate is a new macro (SCR-50, ADR-0055): it runs once each time
// its key is pressed. Its comments address the user, in Chinese like the
// UI.
const MacroTemplate = `// 按下绑定的键时从头运行一次，保存后自动生效。
// 能用的函数见 .padmate/padmate.d.ts；宏结束时按着的键会松开。
import { key, sleep } from "padmate";

export default async function () {
  await key.tap("Ctrl+A");
  await sleep(50);
  await key.tap("Ctrl+C");
}
`

// MacroTypesDir is the folder of the macros folder that holds the macros'
// declarations (profile.MacroTypesDir).
const MacroTypesDir = profile.MacroTypesDir

// macroMarker marks, in the canonical declarations, the members of the
// padmate module a macro can use.
const macroMarker = "@macro"

// globalsDeclarations declares what the goja host has besides the
// standard library.
const globalsDeclarations = "// Types available in the goja script host.\ndeclare const console: { log(...args: unknown[]): void; warn(...args: unknown[]): void; error(...args: unknown[]): void };\n"

// macroTypesHeader starts the padmate declarations of MacroTypesDir.
const macroTypesHeader = "// padmate 生成的类型文件，会被重写，请不要修改。宏能用的成员（API %s）。\n"

// MacroProject returns the files of the macros folder for api, named
// relative to it with slashes: tsconfig.json, which the daemon creates when
// missing and never rewrites, and the declarations in MacroTypesDir, which
// it rewrites when they differ: padmate.d.ts, the padmate module with the
// types and interfaces of the canonical declarations and only the values
// marked @macro; math.d.ts, the padmate/math module; globals.d.ts and the
// goja standard library, as a legacy project has them.
func MacroProject(api Version) ([]File, error) {
	return macroProject(api, nil, nil)
}

func macroProject(api Version, source, lib []byte) ([]File, error) {
	if err := ValidateHostAPI(api); err != nil {
		return nil, err
	}
	if source == nil {
		source = defaultDeclarations
	}
	if len(lib) == 0 {
		lib = compile.GojaLib()
	}
	filtered, err := FilterString(string(source), api)
	if err != nil {
		return nil, err
	}
	padmate, err := moduleBody(filtered, "padmate")
	if err != nil {
		return nil, err
	}
	math, err := moduleBody(filtered, "padmate/math")
	if err != nil {
		return nil, err
	}
	tsconfig, err := macroConfig()
	if err != nil {
		return nil, err
	}
	values, _ := macroValues(padmate)
	header := fmt.Sprintf(macroTypesHeader, api)
	return []File{
		{Name: "tsconfig.json", Data: tsconfig},
		{Name: MacroTypesDir + "/padmate.d.ts", Data: []byte(header + values)},
		{Name: MacroTypesDir + "/math.d.ts", Data: []byte(math)},
		{Name: MacroTypesDir + "/globals.d.ts", Data: []byte(globalsDeclarations)},
		{Name: MacroTypesDir + "/lib.padmate-goja.d.ts", Data: lib},
	}, nil
}

// macroConfig is the tsconfig.json of the macros folder for editors and
// tsc. padmate and padmate/math resolve to the declarations in
// MacroTypesDir, which include names on its own since **/*.ts skips it.
// noEmit keeps tsc from writing JavaScript next to the sources; padmate
// builds macros with esbuild and never reads this file.
func macroConfig() ([]byte, error) {
	data, err := json.MarshalIndent(map[string]any{
		"compilerOptions": map[string]any{
			"target": "ESNext", "module": "ESNext", "moduleResolution": "Bundler", "strict": true, "noEmit": true,
			"allowJs": true, "lib": []string{}, "types": []string{},
			"paths": map[string][]string{
				"padmate":      {"./" + MacroTypesDir + "/padmate.d.ts"},
				"padmate/math": {"./" + MacroTypesDir + "/math.d.ts"},
			},
		},
		"include": []string{"**/*.ts", "**/*.js", MacroTypesDir + "/*.d.ts"},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// MacroMembers returns the names of the values of the padmate module a
// macro can use with api: those marked @macro, in declaration order.
func MacroMembers(api Version) ([]string, error) {
	filtered, err := FilterString(string(defaultDeclarations), api)
	if err != nil {
		return nil, err
	}
	body, err := moduleBody(filtered, "padmate")
	if err != nil {
		return nil, err
	}
	_, names := macroValues(body)
	return names, nil
}

// moduleBody returns the declarations of `declare module "name" { … }` in
// src without that wrapper, dedented by the two spaces of its body.
func moduleBody(src, name string) (string, error) {
	lines := strings.SplitAfter(src, "\n")
	open := `declare module "` + name + `" {`
	start := slices.IndexFunc(lines, func(l string) bool {
		stripped, _ := stripStringsAndComments(l, false)
		return strings.Contains(l, open) && strings.HasPrefix(strings.TrimSpace(stripped), "declare module")
	})
	if start < 0 {
		return "", fmt.Errorf("dtsgen: no module %q in the declarations", name)
	}
	end := skipDeclaration(lines, start)
	if end-1 <= start || strings.TrimSpace(lines[end-1]) != "}" {
		return "", fmt.Errorf("dtsgen: module %q does not end with a line of its own", name)
	}
	var b strings.Builder
	for _, l := range lines[start+1 : end-1] {
		b.WriteString(strings.TrimPrefix(l, "  "))
	}
	return b.String(), nil
}

// valueDeclaration matches the start of a value declaration at the top
// level of a module and captures its name.
var valueDeclaration = regexp.MustCompile(`^\s*export\s+(?:const|function|let|var|class|enum|namespace)\s+([A-Za-z_$][\w$]*)`)

// macroValues returns the declarations of a module body without the values
// whose JSDoc lacks @macro, and the names of the values it keeps. Types,
// interfaces and comments stay, as do the JSDoc blocks of what stays.
func macroValues(body string) (string, []string) {
	lines := strings.SplitAfter(body, "\n")
	var out strings.Builder
	var names []string
	var comments []string // JSDoc lines waiting for their declaration
	write := func(ls []string) {
		for _, l := range ls {
			out.WriteString(l)
		}
	}
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		decl, doc, text := i, "", trimmed
		switch {
		case strings.HasPrefix(trimmed, "/**"):
			end := i
			for !strings.Contains(lines[end], "*/") && end+1 < len(lines) {
				end++
			}
			before, rest, _ := strings.Cut(lines[end], "*/")
			if strings.TrimSpace(rest) == "" {
				comments = append(comments, lines[i:end+1]...)
				i = end + 1
				continue
			}
			// The declaration follows the JSDoc on its last line.
			decl, doc, text = end, strings.Join(lines[i:end], "")+before, rest
		case !strings.HasPrefix(trimmed, "export "):
			write(comments)
			comments = nil
			out.WriteString(lines[i])
			i++
			continue
		}
		end := skipDeclaration(lines, decl)
		doc = strings.Join(comments, "") + doc
		m := valueDeclaration.FindStringSubmatch(text)
		if m == nil || strings.Contains(doc, macroMarker) {
			write(comments)
			write(lines[i:end])
			if m != nil {
				names = append(names, m[1])
			}
		}
		comments = nil
		i = end
	}
	write(comments)
	return out.String(), names
}
