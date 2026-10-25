package installerpkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

// The installer ships the user guide and the user documents, and no other
// document: not the rest of docs/, not the READMEs of the examples, not the
// readme of the portable preview.
func TestShipsFile(t *testing.T) {
	for name, want := range map[string]bool{
		"docs/guide/00-index.md":                 true,
		"docs/guide/08-scripts.en.md":            true,
		"docs/quickstart.md":                     true,
		"docs/quickstart.en.md":                  true,
		"docs/known-limitations.en.md":           true,
		"docs/troubleshooting-input.md":          true,
		"docs/SCRIPTING.md":                      false,
		"docs/ARCHITECTURE.md":                   false,
		"docs/development.md":                    false,
		"docs/build-installer.md":                false,
		"docs/adr/0073-m28-1-0.md":               false,
		"docs/acceptance/m28.md":                 false,
		"docs/bench/latency.md":                  false,
		"docs/guide.md":                          false,
		"examples/scripts/README.md":             false,
		"examples/scripts/tabmate/README.md":     false,
		"examples/scripts/tabmate/main.ts":       true,
		"examples/profiles/basic.json":           true,
		"README-PORTABLE.md":                     false,
		"README.md":                              true,
		"README.en.md":                           true,
		"LICENSE":                                true,
		"licenses/README.md":                     true,
		"licenses/go/x@v1/NOTICE-NO-LICENSE.txt": true,
	} {
		if got := shipsFile(name); got != want {
			t.Errorf("shipsFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// writeFiles writes the files named (slash paths below root) with some content.
func writeFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// installedDocuments are the documents of a complete installation of this
// source, as paths below the root of the installation.
func installedDocuments() []string {
	names := []string{ResourcesFolder + "/" + userGuideIndex, ResourcesFolder + "/" + UserGuide + "00-index.en.md"}
	for _, name := range UserDocuments {
		names = append(names, ResourcesFolder+"/"+name)
	}
	return append(names, ResourcesFolder+"/examples/scripts/tabmate/main.ts")
}

func TestDocumentProblems(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, installedDocuments()...)
	if got := documentProblems(root); len(got) != 0 {
		t.Fatalf("a complete installation has problems: %q", got)
	}

	// Another document of docs/ or the README of an example is reported.
	extra := []string{"resources/docs/adr/0001-x.md", "resources/docs/SCRIPTING.md", "resources/examples/scripts/README.md"}
	writeFiles(t, root, extra...)
	got := strings.Join(documentProblems(root), "\n")
	for _, name := range extra {
		if !strings.Contains(got, name+": must not be installed") {
			t.Errorf("%s is not reported:\n%s", name, got)
		}
	}

	// A missing user document or guide is reported.
	bare := t.TempDir()
	writeFiles(t, bare, ResourcesFolder+"/examples/profiles/basic.json")
	got = strings.Join(documentProblems(bare), "\n")
	for _, name := range append([]string{userGuideIndex}, UserDocuments...) {
		if !strings.Contains(got, "resources/"+name+": missing") {
			t.Errorf("a missing %s is not reported:\n%s", name, got)
		}
	}
}

// CheckTree checks the documents of a build of this source, and leaves them
// alone for an earlier build, which installed all of docs/.
func TestCheckTreeChecksTheDocumentsOfThisBuildOnly(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, append(installedDocuments(), "resources/docs/acceptance/m28.md")...)
	const want = "resources/docs/acceptance/m28.md: must not be installed"
	if got := strings.Join(CheckTree(context.Background(), TreeOptions{Root: root, Tag: mustTag("v1.0.1")}), "\n"); !strings.Contains(got, want) {
		t.Errorf("CheckTree does not report the document:\n%s", got)
	}
	if got := strings.Join(CheckTree(context.Background(), TreeOptions{Root: root, Tag: mustTag("v1.0.1"), EarlierBuild: true}), "\n"); strings.Contains(got, "resources/docs/") {
		t.Errorf("CheckTree of an earlier build checks its documents:\n%s", got)
	}
}

// Stage fails when a user document is missing, instead of building an
// installer without it.
func TestMissingUserDocuments(t *testing.T) {
	staged := map[string]bool{userGuideIndex: true}
	for _, name := range UserDocuments {
		staged[name] = true
	}
	if got := missingUserDocuments(staged); len(got) != 0 {
		t.Errorf("missingUserDocuments = %q, want none", got)
	}
	delete(staged, "docs/quickstart.en.md")
	delete(staged, userGuideIndex)
	if got := strings.Join(missingUserDocuments(staged), ","); got != userGuideIndex+",docs/quickstart.en.md" {
		t.Errorf("missingUserDocuments = %q", got)
	}
}

// Every user document that the installer ships is in the repository.
func TestUserDocumentsExist(t *testing.T) {
	publicmirror.SkipIfMirror(t, "docs/")
	root := filepath.Join("..", "..", "..")
	for _, name := range append([]string{userGuideIndex}, UserDocuments...) {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The tests of the user documents (app/tests/docs/markdown.ts) check the same
// documents that the installer ships, so that their links stay installed.
func TestUserDocumentsAreTheOnesTheDocumentTestsRead(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "app", "tests", "docs", "markdown.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range UserDocuments {
		if !strings.Contains(text, `"`+name+`"`) {
			t.Errorf("app/tests/docs/markdown.ts does not list %s", name)
		}
	}
	if !strings.Contains(text, `join(repoRoot, "docs", "guide")`) {
		t.Error("app/tests/docs/markdown.ts does not read docs/guide")
	}
}
