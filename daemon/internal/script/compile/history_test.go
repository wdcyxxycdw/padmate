package compile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestBuildRefusesHistory checks that the profile library's folder of saved
// profile.json versions (PRO-17) is inside the script root but never script
// source: no import, computed import or entry may reach it.
func TestBuildRefusesHistory(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".history/20261007-153012-0123abcd.json", `{"schemaVersion":0,"name":"old","layers":{"base":{}}}`)
	write(".history/old.ts", "export const old = 1;\n")
	write("lib/util.ts", "export const util = 1;\n")
	write("static.ts", "import snapshot from \"./.history/20261007-153012-0123abcd.json\";\nexport default snapshot;\n")
	write("upper.ts", "import {old} from \"./.HISTORY/old.ts\";\nexport default old;\n")
	write("dotdot.ts", "import {old} from \"./lib/../.history/old\";\nexport default old;\n")
	write("computed.ts", "const name = \"old\";\nexport default import(`./.history/${name}.ts`);\n")
	write("fine.ts", "import {util} from \"./lib/util\";\nexport default util;\n")

	tests := []struct {
		entry string
		want  []string
	}{
		{"static.ts", []string{`static.ts:1:22: error: "./.history/20261007-153012-0123abcd.json" is in the profile's .history folder of saved versions, not script source`}},
		{"dotdot.ts", []string{`dotdot.ts:1:19: error: "./lib/../.history/old" is in the profile's .history folder of saved versions, not script source`}},
		{"computed.ts", []string{`computed.ts:2:23: error: ".history/old.ts" is in the profile's .history folder of saved versions; a computed import() or require() path matches it`}},
	}
	if runtime.GOOS == "windows" {
		// Windows finds the folder whatever the case of its name.
		tests = append(tests, struct {
			entry string
			want  []string
		}{"upper.ts", []string{`upper.ts:1:19: error: "./.HISTORY/old.ts" is in the profile's .history folder of saved versions, not script source`}})
	}
	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			_, err := Build(BuildOptions{Root: root, Entry: tt.entry})
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
		})
	}
	if _, err := Build(BuildOptions{Root: root, Entry: "fine.ts"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{".history/old.ts", filepath.Join(root, ".history", "old.ts")} {
		_, err := Build(BuildOptions{Root: root, Entry: entry})
		var be *BuildError
		if err == nil || errors.As(err, &be) || !strings.Contains(err.Error(), "is in the profile's .history folder") {
			t.Errorf("entry %s: err = %v, want a plain error naming the history folder", entry, err)
		}
	}
}

// TestCacheKeyIgnoresHistory checks that the cache key leaves out the
// profile library's saved versions (PRO-17), which Build never reads: a
// save that adds, prunes or replaces one does not change the key, and the
// key's walk does not hold a snapshot open while the save deletes it. A
// folder of that name below the root is ordinary source.
func TestCacheKeyIgnoresHistory(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.ts", "export default 1;\n")
	opts := BuildOptions{Root: root, Entry: "main.ts", API: "0.8"}
	key := func() string {
		t.Helper()
		croot, _, err := resolvePaths(opts)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := hashTree(croot, false)
		if err != nil {
			t.Fatal(err)
		}
		return tr.files + "+" + tr.profile
	}
	want := key()
	write(".history/20261007-153012-0123abcd.json", `{"schemaVersion":0,"name":"old","layers":{"base":{}}}`)
	write(".HISTORY/20261007-153013-89abcdef.json", `{"schemaVersion":0,"name":"older","layers":{"base":{}}}`)
	if got := key(); got != want {
		t.Error("a snapshot changed the cache key")
	}
	write("lib/.history/util.ts", "export const util = 1;\n")
	if got := key(); got == want {
		t.Error("a source file in lib/.history left the cache key unchanged")
	}
}
