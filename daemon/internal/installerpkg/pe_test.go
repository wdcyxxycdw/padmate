package installerpkg

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
)

// tinyExe cross-builds the small program of pecheck's tests for Windows x64,
// with the version resource padmate's own executables get.
func tinyExe(t *testing.T) string {
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
	src, err := os.Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	final := filepath.Join(dir, "padmated.exe")
	dst, err := os.Create(final)
	if err != nil {
		t.Fatal(err)
	}
	err = exeresources.Write(dst, src, "padmated.exe", "v0.0.1")
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	return final
}

// A program that is not padmated binds other functions than padmated's golden
// lists: the check says which, and says it is the PE check.
func TestCheckGoExecutableHoldsTheFileToTheGolden(t *testing.T) {
	got := joined(checkGoExecutable(tinyExe(t), "padmated", false))
	for _, want := range []string{
		`pecheck: new Windows function (proc variable) "main.SendInput" is not in the golden`,
		`pecheck: Windows function (proc variable) "internal/output.SendInput" is in the golden but no longer in the file`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in\n%s", want, got)
		}
	}
	if got := checkGoExecutable(tinyExe(t), "no-such-program", false); len(got) != 1 || !strings.Contains(got[0], "golden") {
		t.Errorf("an executable without a golden: %v", got)
	}
	if got := checkGoExecutable(filepath.Join(t.TempDir(), "missing.exe"), "padmated", false); len(got) != 1 || !strings.Contains(got[0], "pecheck:") {
		t.Errorf("a missing file: %v", got)
	}
}

// The first installer of an upgrade test is an earlier release. What it binds is
// that release's, not the goldens' of this source: the lists are not compared,
// but the layout, the denylist and the packers are still checked.
func TestCheckGoExecutableOfAnEarlierBuild(t *testing.T) {
	file := tinyExe(t)
	if got := joined(checkGoExecutable(file, "padmated", true)); strings.Contains(got, "golden") {
		t.Errorf("an earlier build was compared with the goldens:\n%s", got)
	}
	if got := checkGoExecutable(file, "padmated", true); len(got) != 0 {
		t.Errorf("a clean file of an earlier build: %v", got)
	}
	// Still a PE file that is read, and a name that has goldens.
	if got := checkGoExecutable(filepath.Join(t.TempDir(), "missing.exe"), "padmated", true); len(got) != 1 || !strings.Contains(got[0], "pecheck:") {
		t.Errorf("a missing file: %v", got)
	}
	packed := filepath.Join(t.TempDir(), "padmated.exe")
	putFile(t, file, packed, true)
	if got := joined(checkGoExecutable(packed, "padmated", true)); !strings.Contains(got, `section "UPX0" is the name of an executable packer`) {
		t.Errorf("a packed file of an earlier build passed:\n%s", got)
	}
}

func TestCheckShippedExecutable(t *testing.T) {
	file := tinyExe(t)
	full := map[string]string{"CompanyName": "padmate contributors", "ProductName": "padmate", "FileDescription": "padmate", "LegalCopyright": "Copyright (c) 2026"}
	if got := checkShippedExecutable(file, full); len(got) != 0 {
		t.Errorf("a complete version resource: %v", got)
	}
	short := map[string]string{"ProductName": "padmate", "FileDescription": "padmate"}
	got := joined(checkShippedExecutable(file, short))
	for _, want := range []string{"pecheck: version resource: CompanyName is empty", "pecheck: version resource: LegalCopyright is empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in %q", want, got)
		}
	}
	// A file that is not there is the caller's to report; one that is not a PE
	// file is ours.
	if got := checkShippedExecutable(filepath.Join(t.TempDir(), "missing.exe"), full); len(got) != 0 {
		t.Errorf("a missing file: %v", got)
	}
	junk := filepath.Join(t.TempDir(), "junk.exe")
	if err := os.WriteFile(junk, []byte("not a PE file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checkShippedExecutable(junk, full); len(got) != 1 || !strings.HasPrefix(got[0], "pecheck:") {
		t.Errorf("a file that is not a PE file: %v", got)
	}
}

// putFile copies src to dst, creating the folders; with a packer's section
// name patched into its first section when packer is set.
func putFile(t *testing.T, src, dst string, packer bool) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if packer {
		// The first section header follows the optional header; its name is
		// the first 8 bytes. .text is renamed, .rsrc (the version resource) is
		// left alone.
		lfanew := int(binary.LittleEndian.Uint32(data[0x3c:]))
		optional := int(binary.LittleEndian.Uint16(data[lfanew+20:]))
		first := lfanew + 24 + optional
		copy(data[first:first+8], "UPX0\x00\x00\x00\x00")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// PKG-11: package:installer's verify and the installer smoke test both go
// through CheckTree and CheckInstaller, so those are where the PE check has to
// be called. Each of the four calls is held by a message only it can make.
func TestCheckTreeAndCheckInstallerRunThePECheck(t *testing.T) {
	const packer = `pecheck: section "UPX0" is the name of an executable packer`
	tiny := tinyExe(t)
	tag := mustTag("v0.0.1")

	root := filepath.Join(t.TempDir(), "win-unpacked")
	putFile(t, tiny, filepath.Join(root, "padmate.exe"), true)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmated.exe"), false)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmatectl.exe"), false)
	got := joined(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag}))
	// The same tree as the tree of an earlier build: the lists of the daemon and
	// the CLI are not compared, the packer in padmate.exe is still found.
	earlier := joined(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag, EarlierBuild: true}))
	if strings.Contains(earlier, "is not in the golden") || strings.Contains(earlier, "in the golden but no longer") {
		t.Errorf("CheckTree of an earlier build compared the Go executables with the goldens:\n%s", earlier)
	}
	if !strings.Contains(earlier, "padmate.exe: "+packer) {
		t.Errorf("CheckTree of an earlier build no longer runs the PE check:\n%s", earlier)
	}
	for _, want := range []string{
		// The Electron executable: the light check.
		"padmate.exe: " + packer,
		// The daemon: the full check against its golden (the tiny program binds
		// main.SendInput, which padmated's golden does not list).
		`resources/bin/padmated.exe: pecheck: new Windows function (proc variable) "main.SendInput" is not in the golden`,
		// The CLI: its own golden, not the daemon's.
		`resources/bin/padmatectl.exe: pecheck: new Windows function (proc variable) "main.SendInput" is not in the golden`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CheckTree does not report %q in\n%s", want, got)
		}
	}
	// The light check is not applied to the Go executables, the full one is not
	// applied to the Electron executable.
	if strings.Contains(got, "resources/bin/padmated.exe: "+packer) {
		t.Errorf("a clean padmated.exe has a packer section: %s", got)
	}

	installer := filepath.Join(t.TempDir(), tag.InstallerName())
	putFile(t, tiny, installer, true)
	got = joined(CheckInstaller(installer, tag))
	if want := tag.InstallerName() + ": " + packer; !strings.Contains(got, want) {
		t.Errorf("CheckInstaller does not report %q in\n%s", want, got)
	}

	// And a file the check is content with reports nothing of it.
	clean := filepath.Join(t.TempDir(), tag.InstallerName())
	putFile(t, tiny, clean, false)
	if got := joined(CheckInstaller(clean, tag)); strings.Contains(got, "pecheck:") {
		t.Errorf("a clean installer is reported by the PE check:\n%s", got)
	}
	cleanRoot := filepath.Join(t.TempDir(), "win-unpacked")
	putFile(t, tiny, filepath.Join(cleanRoot, "padmate.exe"), false)
	if got := joined(CheckTree(context.Background(), TreeOptions{Root: cleanRoot, Tag: tag})); strings.Contains(got, "padmate.exe: pecheck:") {
		t.Errorf("a clean padmate.exe is reported by the PE check:\n%s", got)
	}
}

// The first installer of an upgrade test can be older than this source in two
// independent ways (docs/build-installer.md): EarlierBuild leaves out the
// comparison of its executables with this source's goldens (PKG-11), and
// NoUpdateConfig leaves out resources\app-update.yml, which a release from
// before the automatic update lacks (PKG-10). Each option excuses its own
// thing only, and together they excuse both and nothing else.
func TestCheckTreeEarlierBuildAndNoUpdateConfigCompose(t *testing.T) {
	tiny := tinyExe(t)
	tag := mustTag("v0.0.1")
	root := filepath.Join(t.TempDir(), "win-unpacked")
	putFile(t, tiny, filepath.Join(root, "padmate.exe"), true)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmated.exe"), false)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmatectl.exe"), false)
	const (
		golden = `new Windows function (proc variable) "main.SendInput" is not in the golden`
		update = "resources/app-update.yml: "
		packer = `pecheck: section "UPX0" is the name of an executable packer`
	)
	for _, c := range []struct {
		name                   string
		earlier, noUpdate      bool
		wantGolden, wantUpdate bool
	}{
		{"neither", false, false, true, true},
		{"an earlier build", true, false, false, true},
		{"before the update", false, true, true, false},
		{"both", true, true, false, false},
	} {
		got := joined(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag, EarlierBuild: c.earlier, NoUpdateConfig: c.noUpdate}))
		if has := strings.Contains(got, golden); has != c.wantGolden {
			t.Errorf("%s: the golden comparison is reported=%v, want %v:\n%s", c.name, has, c.wantGolden, got)
		}
		if has := strings.Contains(got, update); has != c.wantUpdate {
			t.Errorf("%s: the missing app-update.yml is reported=%v, want %v:\n%s", c.name, has, c.wantUpdate, got)
		}
		// Neither option excuses the rest of the PE check.
		if !strings.Contains(got, "padmate.exe: "+packer) {
			t.Errorf("%s: the packer section of padmate.exe is not reported:\n%s", c.name, got)
		}
	}
}

// The builds up to v1.0.2 read GitHub (ADR-0073), this source reads the download
// site (ADR-0078). The first installer (INSTALLER) of an upgrade test (`task
// test:installer INSTALLER=<v1.0.2 installer> UPGRADE=<new>`, the migration
// test) may be such a build: EarlierBuild lets its app-update.yml be the former
// source as well,
// and nothing else. A tree of this source is held to the current source.
func TestCheckTreeEarlierBuildMayHaveTheFormerUpdateSource(t *testing.T) {
	tiny := tinyExe(t)
	tag := mustTag("v1.0.2")
	root := filepath.Join(t.TempDir(), "win-unpacked")
	putFile(t, tiny, filepath.Join(root, "padmate.exe"), false)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmated.exe"), false)
	putFile(t, tiny, filepath.Join(root, "resources", "bin", "padmatectl.exe"), false)
	update := filepath.Join(root, "resources", "app-update.yml")
	const reported = "resources/app-update.yml: "
	for _, c := range []struct {
		name              string
		text              string
		earlier, noUpdate bool
		want              bool // whether the file is reported
	}{
		{"this source, a current tree", goodUpdateConfig, false, false, false},
		{"this source, an earlier tree", goodUpdateConfig, true, false, false},
		{"GitHub, a current tree", formerUpdateConfig, false, false, true},
		{"GitHub, an earlier tree", formerUpdateConfig, true, false, false},
		{"GitHub, before the update only", formerUpdateConfig, false, true, true},
		{"GitHub, both options", formerUpdateConfig, true, true, false},
		{"another source, an earlier tree", strings.Replace(formerUpdateConfig, "github", "s3", 1), true, false, true},
		{"the download site with a wrong address, an earlier tree", strings.Replace(goodUpdateConfig, "padmate.download", "example.com", 1), true, false, true},
		{"GitHub without a repository, an earlier tree", strings.Replace(formerUpdateConfig, "repo: padmate\n", "", 1), true, false, true},
	} {
		if err := os.MkdirAll(filepath.Dir(update), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(update, []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		got := joined(CheckTree(context.Background(), TreeOptions{Root: root, Tag: tag, EarlierBuild: c.earlier, NoUpdateConfig: c.noUpdate}))
		if has := strings.Contains(got, reported); has != c.want {
			t.Errorf("%s: app-update.yml is reported=%v, want %v:\n%s", c.name, has, c.want, got)
		}
	}
}
