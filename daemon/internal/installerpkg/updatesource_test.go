package installerpkg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tsConstant reads `export const NAME = "value";` from a TypeScript file of the
// application.
func tsConstant(t *testing.T, file, name string) string {
	t.Helper()
	text := string(readApp(t, file))
	m := regexp.MustCompile(`(?m)^export const ` + regexp.QuoteMeta(name) + ` = "([^"]*)";`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%s has no `export const %s = \"...\";`", file, name)
	}
	return m[1]
}

// The update source is written twice, in the publish block of
// electron-builder.yml (which becomes resources\app-update.yml) and as the
// constants Main builds the release page from. They must not drift apart
// (PKG-10, ADR-0073).
func TestElectronBuilderPublishesToTheUpdateSource(t *testing.T) {
	config := string(readApp(t, "electron-builder.yml"))
	owner := tsConstant(t, "src/main/update-config.ts", "UPDATE_OWNER")
	repo := tsConstant(t, "src/main/update-config.ts", "UPDATE_REPO")

	m := regexp.MustCompile(`(?m)^publish:\s*\n((?:[ \t]+.*\n|\s*\n)+)`).FindStringSubmatch(config)
	if m == nil {
		t.Fatal("electron-builder.yml has no top-level publish: block")
	}
	block := m[1]
	for _, want := range []string{"- provider: github", "owner: " + owner, "repo: " + repo} {
		if !strings.Contains(block, want) {
			t.Errorf("the publish block lacks %q:\n%s", want, block)
		}
	}
	if n := strings.Count(block, "- provider:"); n != 1 {
		t.Errorf("the publish block has %d sources, want the one the application reads", n)
	}
	// The application's updates are not checked against less than the file's
	// own word: the signature check stays on, and the installer is a whole one.
	for _, forbidden := range []string{"verifyUpdateCodeSignature", "differentialPackage: true", "forceDevUpdateConfig"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("electron-builder.yml has %s", forbidden)
		}
	}
	if got := configValue(t, config, "differentialPackage"); got != "false" {
		t.Errorf("differentialPackage is %q, want false: PKG-05 deletes the copy a differential download needs", got)
	}
}

// The folder electron-updater downloads to is named in four places: the
// application, the NSIS script that deletes it, the Go check of an
// installation, and the file electron-builder writes. They are one name.
func TestUpdaterCacheDirIsOneName(t *testing.T) {
	if got := tsConstant(t, "src/main/update-config.ts", "UPDATER_CACHE_DIR"); got != UpdaterCacheDir {
		t.Errorf("update-config.ts UPDATER_CACHE_DIR is %q, the Go constant %q", got, UpdaterCacheDir)
	}
	nsh := string(readApp(t, "build/installer.nsh"))
	if !strings.Contains(nsh, `!define PADMATE_UPDATER_DIR "`+UpdaterCacheDir+`"`) {
		t.Errorf("installer.nsh does not define PADMATE_UPDATER_DIR as %q", UpdaterCacheDir)
	}
}

// A full uninstall deletes what the update downloaded, pending included; an
// install does not, as the installer of an update is the file in pending\.
func TestFullUninstallDeletesTheUpdateCache(t *testing.T) {
	text := string(readApp(t, "build/installer.nsh"))
	_, afterClean, found := strings.Cut(text, "Function un.PadmateClean")
	clean, _, ended := strings.Cut(afterClean, "FunctionEnd")
	if !found || !ended {
		t.Fatal("installer.nsh has no function un.PadmateClean")
	}
	recursive := `RMDir /r "$LOCALAPPDATA\${PADMATE_UPDATER_DIR}"`
	if !strings.Contains(clean, recursive) {
		t.Errorf("un.PadmateClean lacks %s", recursive)
	}
	// A test root has its own folder, so that the test never deletes the
	// user's real one (the real update may be in it).
	if !strings.Contains(clean, `RMDir /r "$padmateLocal\${PADMATE_UPDATER_DIR}"`) {
		t.Error("un.PadmateClean does not delete the test root's folder under the test override")
	}
	if !strings.Contains(clean, `${If} $padmateTestRoot == ""`) {
		t.Error("the real folder is deleted also under the test override")
	}
	// Nowhere else is that folder deleted with what is in it.
	if n := strings.Count(text, `RMDir /r "$LOCALAPPDATA\${PADMATE_UPDATER_DIR}"`); n != 1 {
		t.Errorf("the real update folder is deleted recursively in %d places, want the one in un.PadmateClean", n)
	}
	for _, macro := range []string{"customInstall", "customUnInstall", "customInit", "customCheckAppRunning"} {
		if body := customMacro(t, text, macro); strings.Contains(body, "RMDir /r") && macro != "customUnInstall" {
			t.Errorf("%s deletes recursively; an update's installer runs from the folder", macro)
		}
	}
	// The upgrade's uninstaller (--updated) does not clean up.
	if uninstall := customMacro(t, text, "customUnInstall"); !strings.Contains(uninstall, "${ifNot} ${isUpdated}") {
		t.Error("customUnInstall cleans up also in an upgrade")
	}
}

// The smoke test never starts a new padmate.exe, and so never runs an
// installer with --force-run: that start is made by Explorer as the user, with
// the real data folder and --inject (ADR-0073).
func TestSmokeTestNeverForcesARun(t *testing.T) {
	dir := filepath.Join("..", "installersmoke")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(line, "--force-run") && !strings.HasPrefix(trimmed, "//") && !strings.Contains(line, "refuses") && !strings.Contains(line, "forceRunFlag") {
				t.Errorf("%s:%d runs or builds a command with --force-run: %s", e.Name(), i+1, trimmed)
			}
		}
	}
}
