package publicmirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeRepo makes a directory that looks like the repository root, with a
// package directory below it, and returns both.
func fakeRepo(t *testing.T) (root, pkg string) {
	t.Helper()
	root = t.TempDir()
	for _, f := range []string{"Taskfile.yml", "daemon/go.mod"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg = filepath.Join(root, "daemon", "internal", "x")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, pkg
}

func TestRoot(t *testing.T) {
	root, pkg := fakeRepo(t)
	for _, start := range []string{root, pkg, filepath.Join(root, "daemon")} {
		got, err := Root(start)
		if err != nil || got != root {
			t.Errorf("Root(%q) = %q, %v; want %q", start, got, err, root)
		}
	}
	// A directory with only one of the two files is not the root.
	half := t.TempDir()
	if err := os.WriteFile(filepath.Join(half, "Taskfile.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Root(half); !errors.Is(err, ErrNoRoot) {
		t.Errorf("Root(half) = %q, %v; want ErrNoRoot", got, err)
	}
}

func TestIsFollowsTheMarker(t *testing.T) {
	root, _ := fakeRepo(t)
	if Is(root) {
		t.Fatal("a tree without the marker is not the mirror")
	}
	if err := RefusePackaging(root); err != nil {
		t.Fatalf("development repository must package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, Marker), []byte(MarkerText), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Is(root) {
		t.Fatal("the marker makes the tree the mirror")
	}
	err := RefusePackaging(root)
	if err == nil || !strings.Contains(err.Error(), "public code mirror") || !strings.Contains(err.Error(), "development repository") {
		t.Fatalf("RefusePackaging = %v", err)
	}
	// A directory named like the marker is not the marker.
	dir, _ := fakeRepo(t)
	if err := os.Mkdir(filepath.Join(dir, Marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if Is(dir) {
		t.Fatal("a directory is not the marker")
	}
}

// recorder is a Skipper that records what SkipIfMirror did.
type recorder struct {
	skipped string
}

func (r *recorder) Helper() {}
func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = fmt.Sprintf(format, args...)
}

func TestSkipIfMirror(t *testing.T) {
	root, pkg := fakeRepo(t)
	t.Chdir(pkg)

	var r recorder
	SkipIfMirror(&r, "docs/X.md")
	if r.skipped != "" || InMirror() {
		t.Fatalf("no marker, yet skipped: %q", r.skipped)
	}

	if err := os.WriteFile(filepath.Join(root, Marker), []byte(MarkerText), 0o644); err != nil {
		t.Fatal(err)
	}
	SkipIfMirror(&r, "docs/X.md")
	if !InMirror() || !strings.Contains(r.skipped, "docs/X.md") || !strings.Contains(r.skipped, Marker) {
		t.Fatalf("marker present, skip message %q", r.skipped)
	}
}

func TestOutsideAnyRootStaysStrict(t *testing.T) {
	t.Chdir(t.TempDir())
	if InMirror() {
		t.Fatal("outside a repository root nothing is the mirror")
	}
}

func TestMarkerTextIsOneEnglishSentenceLine(t *testing.T) {
	if !strings.HasSuffix(MarkerText, "\n") || strings.Count(MarkerText, "\n") != 1 {
		t.Fatalf("marker text must be one line: %q", MarkerText)
	}
}

func TestRefusePackagingAtAndHere(t *testing.T) {
	root, pkg := fakeRepo(t)
	t.Chdir(pkg)
	if err := RefusePackagingAt(root); err != nil {
		t.Fatalf("development repository: %v", err)
	}
	if err := RefusePackagingHere(); err != nil {
		t.Fatalf("development repository, here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, Marker), []byte(MarkerText), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"the root": root, "a folder below it": pkg, "a folder that does not exist yet": filepath.Join(root, "dist", "new")} {
		if err := RefusePackagingAt(dir); err == nil || err.Error() != PackagingRefusal {
			t.Errorf("RefusePackagingAt(%s) = %v", name, err)
		}
	}
	if err := RefusePackagingHere(); err == nil || err.Error() != PackagingRefusal {
		t.Errorf("RefusePackagingHere = %v", err)
	}
	// The temporary root of a test is in no repository: not the mirror.
	if err := RefusePackagingAt(t.TempDir()); err != nil {
		t.Errorf("a folder in no repository: %v", err)
	}
}

// The package tasks refuse before they build or read anything: a precondition
// is the first thing a task does. The Taskfile is part of the mirror, so this
// runs in both trees.
func TestPackageTasksRefuseInTheMirror(t *testing.T) {
	root, err := Root("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	taskfile := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, name := range []string{"package:zip", "package:installer", "app:installer", "test:installer", "test:sign", "release:r2"} {
		m := regexp.MustCompile(`(?ms)^  ` + regexp.QuoteMeta(name) + `:\n(.*?)(?:^  \S|\z)`).FindStringSubmatch(taskfile)
		if m == nil {
			t.Fatalf("Taskfile.yml has no task %s", name)
		}
		block := m[1]
		pre, cmds := strings.Index(block, "    preconditions:"), strings.Index(block, "    cmds:")
		if pre < 0 || cmds < 0 || pre > cmds {
			t.Errorf("task %s has no preconditions before its cmds", name)
			continue
		}
		if !strings.Contains(block, "test ! -e") || !strings.Contains(block, "{{.ROOT_DIR}}/"+Marker) {
			t.Errorf("task %s does not test for %s", name, Marker)
		}
		if !strings.Contains(block, PackagingRefusal) {
			t.Errorf("task %s does not say %q", name, PackagingRefusal)
		}
	}
}

// Go's test cache does not see git, and does not see files outside daemon/
// (the Taskfile, ROADMAP.md, the marker): a test that reads them can pass from
// the cache after the file changed. roadmap:check, a step of task ci, runs the
// tests that guard the repository's files uncached. The Taskfile is part of
// the mirror, so this runs in both trees; in the mirror the export tool is
// not there, and the command that runs its tests is guarded.
func TestRoadmapCheckRunsTheRepositoryGuardsUncached(t *testing.T) {
	root, err := Root("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	taskfile := strings.ReplaceAll(string(data), "\r\n", "\n")
	m := regexp.MustCompile(`(?ms)^  roadmap:check:\n(.*?)(?:^  \S|\z)`).FindStringSubmatch(taskfile)
	if m == nil {
		t.Fatal("Taskfile.yml has no task roadmap:check")
	}
	block := m[1]
	if !regexp.MustCompile(`(?m)^\s*- go test -count=1 .*\./internal/publicmirror\s*$`).MatchString(block) {
		t.Error("roadmap:check does not run ./internal/publicmirror with go test -count=1 (it reads the Taskfile)")
	}
	tool := regexp.MustCompile(`(?m)^\s*- cmd: go test -count=1 .*\./tools/mirror\s*\n\s*if: test -d tools/mirror\s*$`)
	if !tool.MatchString(block) {
		t.Error("roadmap:check does not run ./tools/mirror with go test -count=1 under `if: test -d tools/mirror` " +
			"(the tests of the export tool read git; the tool is not in the mirror)")
	}
}
