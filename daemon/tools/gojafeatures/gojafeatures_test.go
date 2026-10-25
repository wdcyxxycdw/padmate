package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// compileDir holds the committed generated files.
const compileDir = "../../internal/script/compile"

// TestGeneratedFilesCurrent fails when the committed files differ from what
// the linked goja and esbuild produce, for example after a goja bump.
func TestGeneratedFilesCurrent(t *testing.T) {
	files, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{featuresFileName, libFileName} {
		want, ok := files[name]
		if !ok {
			t.Fatalf("generate returned no %s", name)
		}
		got, err := os.ReadFile(filepath.Join(compileDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run task gen\n%s", name, firstDiff(got, want))
		}
	}
}

// firstDiff describes the first line where the committed file differs.
func firstDiff(got, want []byte) string {
	g := strings.Split(string(got), "\n")
	w := strings.Split(string(want), "\n")
	for i := 0; i < max(len(g), len(w)); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + strconv.Itoa(i+1) + ":\n  committed: " + gl + "\n  generated: " + wl
		}
	}
	return "files differ"
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	for name := range a {
		if !bytes.Equal(a[name], b[name]) {
			t.Errorf("%s differs between two runs", name)
		}
		if bytes.Contains(a[name], []byte("\r")) {
			t.Errorf("%s contains CR", name)
		}
	}
}

func TestFeatureTable(t *testing.T) {
	names := make([]string, len(features))
	supported := make(map[string]bool, len(features))
	for i, f := range features {
		names[i] = f.name
		if (f.probe == "") == (f.reason == "") {
			t.Errorf("%s: exactly one of probe and reason must be set", f.name)
		}
		if _, dup := supported[f.name]; dup {
			t.Errorf("%s: duplicate", f.name)
		}
		supported[f.name] = true
	}
	if !slices.IsSorted(names) {
		t.Error("features is not sorted by name")
	}
	// esbuild rejects unknown names, so a renamed or removed feature fails
	// here instead of in the first script build.
	if err := checkEsbuild(supported); err != nil {
		t.Error(err)
	}
}

// TestScriptingExpectations pins the features SCRIPTING §3 names.
func TestScriptingExpectations(t *testing.T) {
	results := probeFeatures(features)
	for name, want := range map[string]bool{
		"bigint":             true,
		"optional-chain":     true,
		"nullish-coalescing": true,
		"async-generator":    false,
		"for-await":          false,
		"top-level-await":    false,
	} {
		r, ok := results[name]
		if !ok {
			t.Errorf("%s: not in the table", name)
			continue
		}
		if r.supported != want {
			t.Errorf("%s: supported = %v, want %v (%s)", name, r.supported, want, r.note)
		}
	}
	// esbuild lowers async generators and for await, so tsc must accept
	// them: their lib components stay referenced although goja lacks them.
	libs := map[string]libResult{}
	for _, r := range probeLibs(libComponents) {
		libs[r.component.name] = r
	}
	for name, want := range map[string]bool{
		"es2018.asyncgenerator": true,
		"es2018.asynciterable":  true,
		"es2018.intl":           false,
	} {
		if got := libs[name].included(); got != want {
			t.Errorf("%s: referenced = %v, want %v (goja lacks %q)", name, got, want, libs[name].missing)
		}
	}
}

func TestRunProbe(t *testing.T) {
	tests := []struct {
		name, src string
		ok        bool
		note      string
	}{
		{"true", `var ok = true;`, true, ""},
		{"false", `var ok = false;`, false, "goja: the probe ran but gave the wrong result"},
		{"truthy is not true", `var ok = 1;`, false, "goja: the probe ran but gave the wrong result"},
		{"unset", `var x = 1;`, false, "goja: the probe ran but gave the wrong result"},
		{"throws", `throw new Error("boom");`, false, "goja: Error: boom"},
		{"syntax error", `var ok = ;`, false, "goja: SyntaxError: "},
		{"promise jobs drained", `var ok = false; Promise.resolve().then(function () { ok = true; });`, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, note := runProbe(tt.src)
			if ok != tt.ok || !strings.HasPrefix(note, tt.note) || (tt.note == "" && note != "") {
				t.Errorf("runProbe = %v, %q; want %v, %q...", ok, note, tt.ok, tt.note)
			}
		})
	}
}

func TestProbeMembers(t *testing.T) {
	tests := []struct {
		probe string
		want  []string
	}{
		{"globalThis: Object Nope", []string{"Nope"}},
		{"Array.prototype: map nope @@iterator @@nope", []string{"Array.prototype.nope", "Array.prototype[Symbol.nope]"}},
		{"Nope.prototype: a b", []string{"Nope.prototype"}},
		{"%ArrayIteratorPrototype%: next nope", []string{"%ArrayIteratorPrototype%.nope"}},
		// Punctuation keys are quoted, not evaluated.
		{"String.prototype: $& '", []string{"String.prototype.$&", "String.prototype.'"}},
		{"js:1 === 1", nil},
		{"js:1 === 2", []string{"1 === 2"}},
		{"js:(function () { throw 1; })()", []string{"(function () { throw 1; })()"}},
	}
	for _, tt := range tests {
		t.Run(tt.probe, func(t *testing.T) {
			got := probeLibs([]libComponent{{name: "x", probes: []string{tt.probe}}})[0].missing
			if !slices.Equal(got, tt.want) {
				t.Errorf("missing = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLibTable checks that every probe in the table is well formed: the
// first run of probeLibs panics on a bad base or an unknown intrinsic.
func TestLibTable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range libComponents {
		if seen[c.name] {
			t.Errorf("%s: duplicate", c.name)
		}
		seen[c.name] = true
		if len(c.probes) == 0 {
			t.Errorf("%s: no probes", c.name)
		}
		for _, p := range slices.Concat(c.probes, c.optional) {
			if strings.HasPrefix(p, "js:") {
				continue
			}
			base, members, ok := strings.Cut(p, ": ")
			if !ok || base == "" || strings.TrimSpace(members) == "" {
				t.Errorf("%s: malformed probe %q", c.name, p)
			}
			if strings.HasPrefix(base, "%") && intrinsics[base] == "" {
				t.Errorf("%s: unknown intrinsic %s", c.name, base)
			}
		}
	}
	// Every intrinsic must resolve in goja, or probes using it would report
	// the intrinsic as missing for the wrong reason. The async generator ones
	// are expected to fail: goja has no async generators.
	for name, expr := range intrinsics {
		r := probeLibs([]libComponent{{name: "x", probes: []string{"js:(function () { var b = " + expr + "; return typeof b === 'object' || typeof b === 'function'; })()"}}})[0]
		broken := len(r.missing) > 0
		wantBroken := strings.HasPrefix(name, "%AsyncGenerator")
		if broken != wantBroken {
			t.Errorf("intrinsic %s: resolves = %v, want %v", name, !broken, !wantBroken)
		}
	}
}

func TestAddMissing(t *testing.T) {
	got := addMissing([]string{"WeakRef"}, []string{"WeakRef", "WeakRef.prototype", "WeakRef[Symbol.x]", "WeakRefs", "Atomics"})
	want := []string{"WeakRef", "WeakRefs", "Atomics"}
	if !slices.Equal(got, want) {
		t.Errorf("addMissing = %q, want %q", got, want)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("aaa bbb ccc ddd", 7)
	want := []string{"aaa bbb", "  ccc", "  ddd"}
	if !slices.Equal(got, want) {
		t.Errorf("wrap = %q, want %q", got, want)
	}
	if got := wrap("", 7); got != nil {
		t.Errorf("wrap(\"\") = %q, want nil", got)
	}
}

func TestRunFlags(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"extra"}, &stderr); code != 2 {
		t.Errorf("run(extra) = %d, want 2", code)
	}
	if code := run([]string{"-bogus"}, &stderr); code != 2 {
		t.Errorf("run(-bogus) = %d, want 2", code)
	}
	dir := t.TempDir()
	if code := run([]string{"-dir", dir}, &stderr); code != 0 {
		t.Fatalf("run(-dir) = %d: %s", code, stderr.String())
	}
	for _, name := range []string{featuresFileName, libFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
}
