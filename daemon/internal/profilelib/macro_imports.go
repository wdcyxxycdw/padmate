package profilelib

import (
	"path"
	"regexp"
	"strings"
)

// importPattern finds the relative module specifiers of a script: the
// quoted string after from, a bare import, import( and require(, in
// single, double or back quotes (without a ${…} in them). It reads text,
// not syntax: a specifier in a comment or string counts as well, which only
// makes an export take one file more. A computed specifier is not found.
var importPattern = regexp.MustCompile("(?:\\bfrom|\\bimport|\\brequire)\\s*\\(?\\s*" +
	"(?:\"(\\.{1,2}/[^\"\\r\\n]*)\"|'(\\.{1,2}/[^'\\r\\n]*)'|`(\\.{1,2}/[^`$\\r\\n]*)`)")

// relativeImports returns the relative specifiers ("./lib/util", "../a.js")
// that the script src imports, in order, without repeats.
func relativeImports(src []byte) []string {
	var specs []string
	seen := map[string]bool{}
	for _, m := range importPattern.FindAllSubmatch(src, -1) {
		for _, group := range m[1:] {
			if spec := string(group); spec != "" && !seen[spec] {
				seen[spec] = true
				specs = append(specs, spec)
			}
		}
	}
	return specs
}

// importExtensions are the extensions an import without one may have, in
// the order esbuild tries them, then the other script ones.
var importExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs", ".json"}

// scriptExtensions are the files whose own imports are followed.
var scriptExtensions = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mts": true, ".cts": true, ".mjs": true, ".cjs": true}

// typescriptOf maps the extension an import of TypeScript may name, as
// compiled output has it, to the source's.
var typescriptOf = map[string][]string{".js": {".ts", ".tsx"}, ".jsx": {".tsx"}, ".mjs": {".mts"}, ".cjs": {".cts"}}

// resolveImport returns the file of the macros folder that the import spec
// of the file from, a slash-separated path below the folder, means: the
// path itself, with an extension, with a TypeScript extension for a .js
// one, or the index file of a folder. isFile says whether a path is a
// regular file there. ok is false for a specifier that leaves the folder or
// names nothing.
func resolveImport(from, spec string, isFile func(string) bool) (resolved string, ok bool) {
	base := path.Join(path.Dir(from), spec)
	if base == ".." || strings.HasPrefix(base, "../") || base == "." {
		return "", false
	}
	candidates := []string{base}
	for _, ext := range importExtensions {
		candidates = append(candidates, base+ext)
	}
	if swaps, ok := typescriptOf[strings.ToLower(path.Ext(base))]; ok {
		stem := strings.TrimSuffix(base, path.Ext(base))
		for _, ext := range swaps {
			candidates = append(candidates, stem+ext)
		}
	}
	for _, ext := range importExtensions {
		candidates = append(candidates, base+"/index"+ext)
	}
	for _, c := range candidates {
		if isFile(c) {
			return c, true
		}
	}
	return "", false
}
