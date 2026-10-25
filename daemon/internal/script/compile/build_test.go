package compile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// run executes a bundle in a fresh goja Runtime. RunScript drains the
// promise jobs before it returns.
func run(t *testing.T, js []byte, setup func(*goja.Runtime)) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	if setup != nil {
		setup(vm)
	}
	if _, err := vm.RunScript("bundle.js", string(js)); err != nil {
		t.Fatalf("goja: %v", err)
	}
	return vm
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		root  string
		entry string
		// check runs the bundle and inspects the result.
		check func(t *testing.T, js []byte)
	}{
		{
			name: "sibling import", root: "sibling", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				if got := run(t, js, nil).Get("result").Export(); got != int64(42) {
					t.Errorf("result = %v, want 42", got)
				}
			},
		},
		{
			name: "absolute entry", root: "sibling", entry: "ABS:main.ts",
			check: func(t *testing.T, js []byte) {
				if got := run(t, js, nil).Get("result").Export(); got != int64(42) {
					t.Errorf("result = %v, want 42", got)
				}
			},
		},
		{
			name: "padmate stub", root: "padmate", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				var handlers []goja.Callable
				var taps []string
				vm := run(t, js, func(vm *goja.Runtime) {
					key := vm.NewObject()
					_ = key.Set("tap", func(spec string) { taps = append(taps, spec) })
					host := vm.NewObject()
					_ = host.Set("key", key)
					_ = host.Set("on", func(control, gesture string, fn goja.Callable) { handlers = append(handlers, fn) })
					_ = host.Set("sleep", func(ms int) *goja.Promise {
						p, resolve, _ := vm.NewPromise()
						_ = resolve(goja.Undefined())
						return p
					})
					_ = vm.Set(HostGlobal, host)
				})
				if len(handlers) != 1 {
					t.Fatalf("%d handlers registered, want 1", len(handlers))
				}
				// A handler call runs until its first await; the rest runs
				// when the jobs are drained, which RunString does.
				if _, err := handlers[0](goja.Undefined()); err != nil {
					t.Fatal(err)
				}
				if _, err := vm.RunString(""); err != nil {
					t.Fatal(err)
				}
				if want := []string{"Ctrl+Z", "Ctrl+Y"}; !slices.Equal(taps, want) {
					t.Errorf("taps = %q, want %q", taps, want)
				}
			},
		},
		{
			// goja has neither; the feature table makes esbuild lower them.
			name: "async generator and for await are lowered", root: "lowered", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				if bytes.Contains(js, []byte("async function*")) || bytes.Contains(js, []byte("for await")) {
					t.Error("bundle still contains async generator or for await syntax")
				}
				if got := run(t, js, nil).Get("result").Export(); got != int64(6) {
					t.Errorf("result = %v, want 6", got)
				}
			},
		},
		{
			name: "decorators are lowered", root: "decorators", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				if bytes.Contains(js, []byte("@double")) {
					t.Error("bundle still contains decorator syntax")
				}
				if got := run(t, js, nil).Get("result").Export(); got != int64(6) {
					t.Errorf("result = %v, want 6", got)
				}
			},
		},
		{
			// A known limitation (ADR-0012): the lowered code needs
			// Symbol.dispose, which goja lacks.
			name: "using is lowered but fails in goja", root: "using", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				_, err := goja.New().RunScript("bundle.js", string(js))
				if err == nil || !strings.Contains(err.Error(), "not disposable") {
					t.Errorf("err = %v, want a TypeError about a non-disposable object", err)
				}
			},
		},
		{
			// The inline source map reaches goja's stack traces.
			name: "stack trace uses the source map", root: "throws", entry: "main.ts",
			check: func(t *testing.T, js []byte) {
				_, err := goja.New().RunScript("bundle.js", string(js))
				var ex *goja.Exception
				if !errors.As(err, &ex) {
					t.Fatalf("err = %v, want a goja exception", err)
				}
				stack := ex.String()
				for _, want := range []string{"failed with 1", "at fail (fail.ts:2:", "at main.ts:6:"} {
					if !strings.Contains(stack, want) {
						t.Errorf("stack does not contain %q:\n%s", want, stack)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join("testdata", tt.root)
			entry := tt.entry
			if rel, ok := strings.CutPrefix(entry, "ABS:"); ok {
				abs, err := filepath.Abs(filepath.Join(root, rel))
				if err != nil {
					t.Fatal(err)
				}
				entry = abs
			}
			r, err := Build(BuildOptions{Root: root, Entry: entry})
			if err != nil {
				t.Fatal(err)
			}
			if r.Size != len(r.JS) || r.Size == 0 {
				t.Errorf("Size = %d, len(JS) = %d", r.Size, len(r.JS))
			}
			if len(r.Diagnostics) > 0 {
				t.Errorf("unexpected diagnostics: %v", r.Diagnostics)
			}
			tt.check(t, r.JS)
		})
	}
}

func TestBundleGlobalDefaultExport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte("export default { value: 42 };\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Build(BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	vm := run(t, r.JS, nil)
	bundle, ok := vm.Get(BundleGlobal).(*goja.Object)
	if !ok {
		t.Fatalf("%s = %T, want object", BundleGlobal, vm.Get(BundleGlobal))
	}
	if got := bundle.Get("default").ToObject(vm).Get("value").ToInteger(); got != 42 {
		t.Fatalf("default.value = %d, want 42", got)
	}
}

func TestBuildErrors(t *testing.T) {
	tests := []struct {
		name, root, entry string
		want              []string
	}{
		{
			name: "padmate submodule", root: "padmatesub", entry: "main.ts",
			want: []string{`main.ts:1:23: error: module "padmate/layers" does not exist; the M0d stub only provides "padmate"`},
		},
		{
			name: "node built-in", root: "builtin", entry: "main.ts",
			want: []string{`main.ts:1:30: error: Node.js built-in module "fs" is not available in padmate scripts`},
		},
		{
			name: "node: prefix", root: "builtinprefix", entry: "main.ts",
			want: []string{`main.ts:1:18: error: Node.js built-in module "node:path" is not available in padmate scripts`},
		},
		{
			name: "outside the root", root: "outside/root", entry: "main.ts",
			want: []string{`main.ts:1:24: error: "../secret" resolves to a file outside the script root`},
		},
		{
			// esbuild resolves a computed path as a glob (../**/*.ts)
			// without calling OnResolve; the OnLoad backstop rejects it.
			name: "computed import() outside the root", root: "globimport/root", entry: "main.ts",
			want: []string{`main.ts:2:17: error: "../secret.ts" is outside the script root; a computed import() or require() path matches it`},
		},
		{
			name: "computed require() outside the root", root: "globrequire/root", entry: "main.ts",
			want: []string{`main.ts:4:18: error: "../secret.ts" is outside the script root; a computed import() or require() path matches it`},
		},
		{
			name: "concatenated require() outside the root", root: "globconcat/root", entry: "main.ts",
			want: []string{`main.ts:4:18: error: "../secret.ts" is outside the script root; a computed import() or require() path matches it`},
		},
		{
			// esbuild would mark the first two external and inline the
			// third.
			name: "URLs", root: "url", entry: "main.ts",
			want: []string{
				`main.ts:1:15: error: "https://example.invalid/a.js" is a URL; padmate scripts can only import files in the script root`,
				`main.ts:2:15: error: "//example.invalid/b.js" is a URL; padmate scripts can only import files in the script root`,
				`main.ts:3:15: error: "data:text/javascript,export default 1" is a URL; padmate scripts can only import files in the script root`,
			},
		},
		{
			// Rejected before esbuild resolves them: resolving the UNC path
			// would contact the host.
			name: "absolute paths", root: "absolute", entry: "main.ts",
			want: []string{
				`main.ts:1:15: error: "\\\\localhost\\nonexistshare\\a.js" is an absolute path; import files in the script root with a relative path`,
				`main.ts:2:15: error: "/etc/b.js" is an absolute path; import files in the script root with a relative path`,
				`main.ts:3:15: error: "C:/c.js" is an absolute path; import files in the script root with a relative path`,
			},
		},
		{
			name: "top-level await", root: "tla", entry: "main.ts",
			want: []string{"main.ts:3:1: error: " + tlaText},
		},
		{
			name: "top-level await in an imported module", root: "tlaimport", entry: "main.ts",
			want: []string{"setup.ts:1:16: error: " + tlaText},
		},
		{
			name: "syntax error", root: "syntax", entry: "main.ts",
			want: []string{`main.ts:2:14: error: Unexpected ";"`},
		},
		{
			name: "missing import", root: "missing", entry: "main.ts",
			want: []string{`main.ts:1:22: error: Could not resolve "./nope"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(BuildOptions{Root: filepath.Join("testdata", tt.root), Entry: tt.entry})
			var be *BuildError
			if !errors.As(err, &be) {
				t.Fatalf("err = %v, want a *BuildError", err)
			}
			got := make([]string, len(be.Diagnostics))
			for i, d := range be.Diagnostics {
				got[i] = d.String()
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("diagnostics:\n  got  %q\n  want %q", got, tt.want)
			}
			if err.Error() != strings.Join(tt.want, "\n") {
				t.Errorf("Error() = %q", err.Error())
			}
		})
	}
}

func TestBuildWarning(t *testing.T) {
	r, err := Build(BuildOptions{Root: "testdata/warning", Entry: "main.ts"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`main.ts:2:10: warning: Comparison with -0 using the "==" operator will also match 0`}
	got := make([]string, len(r.Diagnostics))
	for i, d := range r.Diagnostics {
		got[i] = d.String()
	}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics = %q, want %q", got, want)
	}
}

func TestBuildBadOptions(t *testing.T) {
	outside, err := filepath.Abs("testdata/outside/secret.ts")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		opts BuildOptions
		want string
	}{
		{"no root", BuildOptions{Entry: "main.ts"}, "Root and Entry are required"},
		{"no entry", BuildOptions{Root: "testdata/sibling"}, "Root and Entry are required"},
		{"missing root", BuildOptions{Root: "testdata/nope", Entry: "main.ts"}, "script root"},
		{"missing entry", BuildOptions{Root: "testdata/sibling", Entry: "nope.ts"}, "entry"},
		{"entry is a directory", BuildOptions{Root: "testdata/sibling", Entry: "lib"}, "is a directory"},
		{"relative entry outside the root", BuildOptions{Root: "testdata/outside/root", Entry: "../secret.ts"}, "outside the script root"},
		{"absolute entry outside the root", BuildOptions{Root: "testdata/outside/root", Entry: outside}, "outside the script root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.opts)
			var be *BuildError
			if err == nil || errors.As(err, &be) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want a plain error containing %q", err, tt.want)
			}
		})
	}
}

// TestSourceMap checks that the bundle ends with an inline source map whose
// sources are root-relative and carry their contents.
func TestSourceMap(t *testing.T) {
	r, err := Build(BuildOptions{Root: "testdata/sibling", Entry: "main.ts"})
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "//# sourceMappingURL=data:application/json;base64,"
	js := strings.TrimRight(string(r.JS), "\n")
	i := strings.LastIndex(js, "\n")
	last := js[i+1:]
	data, ok := strings.CutPrefix(last, prefix)
	if !ok {
		t.Fatalf("last line is not an inline source map: %.80q", last)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	var sm struct {
		Version        int      `json:"version"`
		Sources        []string `json:"sources"`
		SourcesContent []string `json:"sourcesContent"`
		Mappings       string   `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatal(err)
	}
	if want := []string{"lib/util.ts", "main.ts"}; sm.Version != 3 || !slices.Equal(sm.Sources, want) {
		t.Errorf("version %d, sources %q; want 3, %q", sm.Version, sm.Sources, want)
	}
	if len(sm.SourcesContent) != 2 || !strings.Contains(sm.SourcesContent[0], "function double(x: number)") || sm.Mappings == "" {
		t.Errorf("source map lacks contents or mappings: %s", raw)
	}
}

// TestDeterministic builds the same sources twice in place and once from a
// copy elsewhere: the bytes must match, so no absolute path leaks in.
func TestDeterministic(t *testing.T) {
	build := func(root string) []byte {
		t.Helper()
		r, err := Build(BuildOptions{Root: root, Entry: "main.ts"})
		if err != nil {
			t.Fatal(err)
		}
		return r.JS
	}
	a := build("testdata/sibling")
	b := build("testdata/sibling")
	if !bytes.Equal(a, b) {
		t.Error("two builds of the same sources differ")
	}
	cp := t.TempDir()
	if err := os.CopyFS(cp, os.DirFS("testdata/sibling")); err != nil {
		t.Fatal(err)
	}
	if c := build(cp); !bytes.Equal(a, c) {
		t.Error("a build from a copy of the sources differs")
	}
}

func TestDiagnosticString(t *testing.T) {
	tests := []struct {
		d    Diagnostic
		want string
	}{
		{Diagnostic{File: "a.ts", Line: 3, Column: 7, Severity: SeverityError, Text: "x"}, "a.ts:3:7: error: x"},
		{Diagnostic{File: "a.ts", Line: 3, Severity: SeverityWarning, Text: "x"}, "a.ts:3: warning: x"},
		{Diagnostic{File: "a.ts", Severity: SeverityError, Text: "x"}, "a.ts: error: x"},
		{Diagnostic{Severity: SeverityError, Text: "x"}, "error: x"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// TestFeatureTable checks the embedded table against SCRIPTING §3.
func TestFeatureTable(t *testing.T) {
	for name, want := range map[string]bool{
		"bigint":             true,
		"optional-chain":     true,
		"nullish-coalescing": true,
		"async-generator":    false,
		"for-await":          false,
		"top-level-await":    false,
	} {
		got, ok := supported[name]
		if !ok || got != want {
			t.Errorf("supported[%q] = %v, %v; want %v", name, got, ok, want)
		}
	}
}

func TestInside(t *testing.T) {
	root := filepath.FromSlash("/r/root")
	if filepath.VolumeName(os.TempDir()) != "" {
		root = `C:\r\root`
	}
	tests := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "a.ts"), true},
		{filepath.Join(root, "..root", "a.ts"), true},
		{filepath.Join(root, "..", "a.ts"), false},
		{filepath.Join(root, "..", "rootx", "a.ts"), false},
	}
	for _, tt := range tests {
		if got := inside(root, tt.path); got != tt.want {
			t.Errorf("inside(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
		}
	}
}
