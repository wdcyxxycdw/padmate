package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

func runTool(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// binDir cross-builds the program of pecheck's tests twice, as padmated.exe and
// padmatectl.exe, with the version resource padmate's builds have.
func binDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	raw := filepath.Join(dir, "tiny.raw")
	build := exec.Command("go", "build", "-trimpath", "-o", raw, "./internal/pecheck/testdata/tiny")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	buildtool.HideWindow(build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cross-build: %v\n%s", err, out)
	}
	for _, name := range []string{"padmated.exe", "padmatectl.exe"} {
		src, err := os.Open(raw)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = exeresources.Write(dst, src, name, "v0.0.1")
		_ = src.Close()
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestUpdateThenCheck(t *testing.T) {
	bin, golden := binDir(t), t.TempDir()
	// No golden yet: the check fails and names the missing file.
	if code, _, errOut := runTool("-bin", bin, "-golden", golden); code != 1 || !strings.Contains(errOut, "padmated.imports.txt") {
		t.Fatalf("no golden: exit %d, %q", code, errOut)
	}
	code, out, errOut := runTool("-update", "-bin", bin, "-golden", golden)
	if code != 0 || !strings.Contains(out, "padmated.exe:") || !strings.Contains(out, "(+") || errOut != "" {
		t.Fatalf("update: exit %d, %q, %q", code, out, errOut)
	}
	for _, name := range []string{"padmated", "padmatectl"} {
		for _, suffix := range []string{".imports.txt", ".procs.txt"} {
			if _, err := os.Stat(filepath.Join(golden, name+suffix)); err != nil {
				t.Error(err)
			}
		}
	}
	if code, out, errOut := runTool("-bin", bin, "-golden", golden); code != 0 || !strings.Contains(out, "as the goldens list them") || errOut != "" {
		t.Fatalf("check after update: exit %d, %q, %q", code, out, errOut)
	}

	// A second update with nothing new changes nothing.
	if code, out, _ := runTool("-update", "-bin", bin, "-golden", golden); code != 0 || !strings.Contains(out, "(+0 -0)") {
		t.Errorf("update again: exit %d, %q", code, out)
	}

	// A function the golden does not know is a failure with a way forward.
	procs := filepath.Join(golden, "padmated.procs.txt")
	data, err := os.ReadFile(procs)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimSuffix(data, []byte("windows.OpenProcess\n"))
	if bytes.Equal(trimmed, data) {
		trimmed = bytes.Replace(data, []byte("windows.OpenProcess\n"), nil, 1)
	}
	if err := os.WriteFile(procs, trimmed, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runTool("-bin", bin, "-golden", golden)
	if code != 1 || !strings.Contains(errOut, `new Windows function (proc variable) "windows.OpenProcess" is not in the golden`) || !strings.Contains(errOut, "-update") {
		t.Errorf("a new function: exit %d, %q", code, errOut)
	}
}

func TestMissingExecutable(t *testing.T) {
	code, _, errOut := runTool("-bin", t.TempDir(), "-golden", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "padmated.exe") || !strings.Contains(errOut, "padmatectl.exe") {
		t.Errorf("exit %d, %q", code, errOut)
	}
	if code, _, _ := runTool("-update", "-bin", t.TempDir(), "-golden", t.TempDir()); code != 1 {
		t.Errorf("update without executables: exit %d", code)
	}
}

func TestUsage(t *testing.T) {
	if code, _, _ := runTool("extra"); code != 2 {
		t.Errorf("positional argument: exit %d", code)
	}
	if code, _, _ := runTool("-nope"); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
}

// Nothing but the check of a real build keeps the goldens true, so the check has
// to be where the builds are: the local `task ci` right after `build`, and the
// remote workflow (not run while the owner keeps it off, but it must not have
// forgotten the check when it is turned on again).
func TestPECheckRunsWhereTheBuildsAre(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	read := func(name ...string) string {
		data, err := os.ReadFile(filepath.Join(append([]string{root}, name...)...))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	taskfile := read("Taskfile.yml")
	block := func(name string) string {
		m := regexp.MustCompile(`(?ms)^  ` + regexp.QuoteMeta(name) + `:\n(.*?)(?:^  \S|\z)`).FindStringSubmatch(taskfile)
		if m == nil {
			t.Fatalf("Taskfile.yml has no task %s", name)
		}
		return m[1]
	}

	var steps []string
	for _, m := range regexp.MustCompile(`(?m)^\s+- task: (\S+)\s*$`).FindAllStringSubmatch(block("ci"), -1) {
		steps = append(steps, m[1])
	}
	at := slices.Index(steps, "pe:check")
	if at < 1 || steps[at-1] != "build" {
		t.Errorf("task ci runs %v: pe:check must come right after build, which makes the files it reads", steps)
	}
	if !strings.Contains(block("pe:check"), "go run ./tools/pecheck -bin ../bin") {
		t.Error("task pe:check does not run this command on bin/")
	}

}

// The remote workflow is not part of the public code mirror.
func TestPECheckRunsInTheRemoteWorkflow(t *testing.T) {
	publicmirror.SkipIfMirror(t, ".github/workflows/ci.yml")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := strings.ReplaceAll(string(data), "\r\n", "\n")
	build, check := strings.Index(workflow, "go run ./tools/build "), strings.Index(workflow, "go run ./tools/pecheck -bin ../bin")
	if build < 0 || check < build {
		t.Error("the go-windows job of .github/workflows/ci.yml does not build the daemon and the CLI and then run tools/pecheck on ../bin")
	}
}
