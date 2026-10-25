package installerpkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func problemsOf(info VersionInfo, tag string, product productVersion) []string {
	var got []string
	checkElectronVersion(func(format string, args ...any) {
		got = append(got, fmt.Sprintf(format, args...))
	}, "x.exe", info, mustTag(tag), product)
	return got
}

func mustTag(label string) Tag {
	t, err := ParseTag(label)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCheckElectronVersion(t *testing.T) {
	good := VersionInfo{
		File: [4]uint16{0, 14, 0, 1}, Product: [4]uint16{0, 14, 0, 0},
		Strings: map[string]string{"FileVersion": "0.14.0-rc.1", "ProductVersion": "0.14.0.0"},
	}
	if p := problemsOf(good, "v0.14.0-rc.1", numericProductVersion); len(p) != 0 {
		t.Errorf("padmate.exe as electron-builder writes it: %v", p)
	}
	// The installer has the app's version in both strings.
	installer := VersionInfo{
		File: [4]uint16{0, 14, 0, 0}, Product: [4]uint16{0, 14, 0, 0},
		Strings: map[string]string{"FileVersion": "0.14.0-rc.1", "ProductVersion": "0.14.0-rc.1"},
	}
	if p := problemsOf(installer, "v0.14.0-rc.1", appProductVersion); len(p) != 0 {
		t.Errorf("installer: %v", p)
	}
	// The four numbers do not pass for an installer, whose ProductVersion is
	// the app's version.
	if p := problemsOf(good, "v0.14.0-rc.1", appProductVersion); len(p) != 1 || !strings.Contains(p[0], "ProductVersion") {
		t.Errorf("numeric ProductVersion for an installer: %v", p)
	}
	for name, mutate := range map[string]func(*VersionInfo){
		"another file version string": func(i *VersionInfo) { i.Strings["FileVersion"] = "0.14.0" },
		"an empty product version":    func(i *VersionInfo) { i.Strings["ProductVersion"] = "" },
		"another patch number":        func(i *VersionInfo) { i.Strings["ProductVersion"] = "0.14.1.0" },
		"another numeric file":        func(i *VersionInfo) { i.File = [4]uint16{0, 13, 0, 1} },
		"another numeric product":     func(i *VersionInfo) { i.Product = [4]uint16{1, 14, 0, 0} },
	} {
		info := good
		info.Strings = map[string]string{"FileVersion": "0.14.0-rc.1", "ProductVersion": "0.14.0.0"}
		mutate(&info)
		if p := problemsOf(info, "v0.14.0-rc.1", numericProductVersion); len(p) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestCheckInstallerName(t *testing.T) {
	dir := t.TempDir()
	tag := mustTag("v0.14.0")
	wrong := filepath.Join(dir, "padmate-setup.exe")
	if err := os.WriteFile(wrong, []byte("not a PE"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(CheckInstaller(wrong, tag), "\n")
	if !strings.Contains(problems, `installer is named "padmate-setup.exe", want "padmate-v0.14.0-windows-amd64-setup.exe"`) {
		t.Errorf("name not checked:\n%s", problems)
	}
	if got := CheckInstaller(filepath.Join(dir, tag.InstallerName()), tag); len(got) == 0 {
		t.Error("a missing installer passed")
	}
}

func TestCheckTreeNamesWhatIsMissing(t *testing.T) {
	problems := strings.Join(CheckTree(context.Background(), TreeOptions{Root: t.TempDir(), Tag: mustTag("v0.14.0")}), "\n")
	for _, want := range []string{
		"padmate.exe", "resources/app.asar", "resources/bin/padmated.exe", "resources/bin/padmatectl.exe",
		"resources/bin/SDL3.dll", "resources/LICENSE", "resources/licenses/THIRD-PARTY.txt", "resources/examples/profiles/basic.json",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("an empty tree does not report %s:\n%s", want, problems)
		}
	}
}

// The installer asks for no administrator rights: its manifest says asInvoker.
func TestExecutionLevelOf(t *testing.T) {
	manifest := func(level string) []byte {
		return []byte(`MZ....<trustInfo><security><requestedPrivileges><requestedExecutionLevel level="` + level + `" uiAccess="false"/>`)
	}
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"as invoker", manifest("asInvoker"), "asInvoker"},
		{"administrator", manifest("requireAdministrator"), "requireAdministrator"},
		{"no manifest", []byte("MZ no manifest here"), ""},
		{"an unterminated level", []byte(`requestedExecutionLevel level="asInvoker`), ""},
	} {
		if got := executionLevelOf(c.data); got != c.want {
			t.Errorf("%s: level %q, want %q", c.name, got, c.want)
		}
	}
}

func TestInstallerExecutionLevelReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.exe"), filepath.Join(dir, "bad.exe")
	if err := os.WriteFile(good, []byte(`MZ requestedExecutionLevel level="asInvoker" uiAccess`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`MZ requestedExecutionLevel level="requireAdministrator" uiAccess`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := installerExecutionLevel(good); err != nil || got != "asInvoker" {
		t.Errorf("good installer: %q, %v", got, err)
	}
	if got, err := installerExecutionLevel(bad); err != nil || got != "requireAdministrator" {
		t.Errorf("elevating installer: %q, %v", got, err)
	}
	if _, err := installerExecutionLevel(filepath.Join(dir, "missing.exe")); err == nil {
		t.Error("a missing installer was read")
	}
}

// The wizard allows MaxRelativePath for the longest file of an installation,
// the backslash in front of it included: the tree is held to it.
func TestLongPaths(t *testing.T) {
	root := t.TempDir()
	// Directories count as well as files, and Chinese counts one each.
	write := func(rel string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("resources/" + strings.Repeat("a", MaxRelativePath-len(`\resources\`)))
	write("resources/licenses/" + strings.Repeat("向", MaxRelativePath-len(`\resources\licenses\`)))
	if got := longPaths(root); len(got) != 0 {
		t.Errorf("the longest paths that fit were reported: %v", got)
	}
	write("resources/licenses/" + strings.Repeat("向", MaxRelativePath-len(`\resources\licenses\`)+1))
	got := longPaths(root)
	if len(got) != 1 || !strings.Contains(got[0], "resources/licenses/") || !strings.Contains(got[0], "141") {
		t.Errorf("a path one character too long was reported as %v", got)
	}
	if got := longPaths(filepath.Join(root, "missing")); len(got) != 0 {
		t.Errorf("a folder that is not there has long paths: %v", got)
	}
}
