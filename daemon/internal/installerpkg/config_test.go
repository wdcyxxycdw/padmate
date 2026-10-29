package installerpkg

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func readApp(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "app", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func configValue(t *testing.T, config, key string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(.+?)\s*$`).FindStringSubmatch(config)
	if m == nil {
		t.Fatalf("electron-builder.yml has no %s", key)
	}
	return strings.Trim(m[1], `"'`)
}

// The GUID, the product name and the installer's file name are written in
// electron-builder.yml and read by Go; they must not drift apart.
func TestElectronBuilderConfig(t *testing.T) {
	config := string(readApp(t, "electron-builder.yml"))
	if got := configValue(t, config, "guid"); got != GUID {
		t.Errorf("nsis.guid is %q, the Go constant GUID %q", got, GUID)
	}
	if got := configValue(t, config, "productName"); got != ProductName {
		t.Errorf("productName is %q, want %q", got, ProductName)
	}
	if got := configValue(t, config, "executableName"); got != ProductName {
		t.Errorf("executableName is %q, want %q (AppFile)", got, ProductName)
	}
	// The installer is named as Tag.InstallerName says, with ${version} the app's.
	want := strings.Replace(mustTag("v1.2.3").InstallerName(), "1.2.3", "${version}", 1)
	want = strings.TrimSuffix(want, ".exe") + ".${ext}"
	if got := configValue(t, config, "artifactName"); got != want {
		t.Errorf("artifactName is %q, want %q", got, want)
	}
	// A wizard (PKG-13), per user, no administrator rights (PKG-03). It
	// finishes with "run padmate" ticked, makes a Start Menu and a desktop
	// shortcut (ADR-0074), and has no license page (TestNoLicensePage).
	for key, value := range map[string]string{
		"oneClick": "false", "perMachine": "false", "allowElevation": "false", "runAfterFinish": "true",
		"createDesktopShortcut": "always", "createStartMenuShortcut": "true",
		"requestedExecutionLevel": "asInvoker", "asar": "true", "include": "build/installer.nsh",
	} {
		if got := configValue(t, config, key); got != value {
			t.Errorf("%s is %q, want %q", key, got, value)
		}
	}
	// The folder page is padmate's own (build/installer.nsh): this option would
	// add electron-builder's, and so a second one.
	if regexp.MustCompile(`(?m)^\s*allowToChangeInstallationDirectory:`).MatchString(config) {
		t.Error("allowToChangeInstallationDirectory is set: electron-builder would add a folder page of its own next to padmate's")
	}
	if !strings.Contains(config, "from: ../dist/installer-stage/resources") || !strings.Contains(config, "icon: ../dist/installer-stage/icon.ico") {
		t.Error("extraResources and the icon do not read the stage folder Stage writes")
	}
	// The GUID must be a lower-case GUID, as the registry key it names.
	if !regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(GUID) {
		t.Errorf("GUID %q is not a GUID", GUID)
	}
}

// electron-builder writes padmate.exe's numeric file version from the string
// "0.15.1-rc.1" as 0.15.0.1; the afterSign hook rewrites it to the tag's three
// numbers, which checkElectronVersion wants (PKG-06). Without the hook only a
// prerelease of a version with PATCH 0 passes, by luck.
func TestElectronBuilderStampsFileVersion(t *testing.T) {
	config := string(readApp(t, "electron-builder.yml"))
	hook := configValue(t, config, "afterSign")
	if hook != "./scripts/stamp-exe-version.mjs" {
		t.Fatalf("afterSign is %q, want the hook that stamps padmate.exe's file version", hook)
	}
	script := string(readApp(t, strings.TrimPrefix(hook, "./")))
	// The hook writes the version into a file electron-builder has already signed,
	// which drops the signature, and signs again (PKG-09).
	for _, want := range []string{"export default", "fileVersionMS", "fileVersionLS", "ignoreCert", "signIf"} {
		if !strings.Contains(script, want) {
			t.Errorf("%s does not contain %q", hook, want)
		}
	}
}

// The NSIS include is read as Unicode only with a byte order mark, which an
// editor or a formatter may drop; the Chinese prompt then turns to garbage.
func TestInstallerScript(t *testing.T) {
	nsh := readApp(t, "build/installer.nsh")
	if !bytes.HasPrefix(nsh, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("app/build/installer.nsh must be UTF-8 with a byte order mark")
	}
	text := string(nsh)
	for _, want := range []string{
		"!macro customHeader", "!macro customInit", "!macro customCheckAppRunning", "!macro customUnInstall",
		// The wizard (PKG-13): padmate's own folder page after the skipped
		// install-mode page, and the rule for the folder in its own file.
		"!macro customInstallMode", "!macro customPageAfterChangeDir", "!insertmacro PadmateDirPage",
		`!include "padmate-wizard.nsh"`, "!insertmacro PadmatePathFunctions", "!insertmacro PadmatePageFunctions",
		"请选择别的位置", "Choose another location.",
		// The messages of the other checks of the folder page, in both languages.
		"LangString padmateDirInvalid ${LANG_ENGLISH}", "请输入完整的文件夹路径", "LangString padmateDirTooLong ${LANG_ENGLISH}",
		"路径太长", "LangString padmateDirNoWrite ${LANG_ENGLISH}", "无法写入文件夹",
		// quit is limited to this installation's folder, so that a padmated of
		// another folder that owns the discovery file is not asked to exit.
		"padmatectl.exe\" quit --timeout 10s --under \"$padmateDir\"", "release-all", "PADMATE_INSTALLER_TEST_ROOT",
		RunKey, TestRunKey, `\AppData\Roaming`, `\AppData\Local`, "--ipc-dir",
		"${ifNot} ${isUpdated}", "LangString padmateKeepData ${LANG_SIMPCHINESE}", "LangString padmateKeepData ${LANG_ENGLISH}",
		"是否保留配置和脚本？",
		// The folder guard names a way forward, and accepts the folder an
		// earlier installation recorded.
		"run the installer again with /D=", "用 /D= 指定一个空文件夹", `ReadRegStr $0 HKCU "${INSTALL_REGISTRY_KEY}" InstallLocation`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("installer.nsh lacks %q", want)
		}
	}
	// The stop covers the three programs and checks where each one's file is.
	for _, exe := range []string{"padmate.exe", "padmated.exe", "padmatectl.exe"} {
		if !strings.Contains(text, `"`+exe+`"`) {
			t.Errorf("installer.nsh does not look for %s", exe)
		}
	}
	// The window's browser data holds its settings: a full uninstall deletes it
	// only together with the whole data folder, after the question.
	if strings.Contains(text, `RMDir /r "$padmateData\ui"`) {
		t.Error(`installer.nsh deletes $padmateData\ui on its own, with the settings of the window`)
	}
	for _, forbidden := range []string{"taskkill", "powershell", "wmic"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("installer.nsh uses %s, which ends programs by name or needs a shell", forbidden)
		}
	}
}

// electron-builder's installer keeps a copy of itself, as large as it is, in
// %LOCALAPPDATA%\padmate-updater for a differential update, which padmate does
// not use (PKG-10 downloads whole installers), and its uninstaller leaves it.
// The install deletes it at its end, and a full uninstall again. The shared
// function removes the folder only when it is empty; what an update downloaded
// is deleted by a full uninstall alone (TestFullUninstallDeletesTheUpdateCache).
func TestInstallerDropsItsCopy(t *testing.T) {
	text := string(readApp(t, "build/installer.nsh"))
	if install := customMacro(t, text, "customInstall"); !strings.Contains(install, "Call PadmateDropInstallerCopy") {
		t.Error("customInstall does not delete the installer's copy of itself")
	}
	_, afterClean, found := strings.Cut(text, "Function un.PadmateClean")
	clean, _, ended := strings.Cut(afterClean, "FunctionEnd")
	if !found || !ended {
		t.Fatal("installer.nsh has no function un.PadmateClean")
	}
	if !strings.Contains(clean, "Call un.PadmateDropInstallerCopy") {
		t.Error("a full uninstall does not delete the installer's copy of itself")
	}
	_, afterDrop, found := strings.Cut(text, "Function ${PADMATE_UN}PadmateDropInstallerCopy")
	drop, _, ended := strings.Cut(afterDrop, "FunctionEnd")
	if !found || !ended {
		t.Fatal("installer.nsh has no function PadmateDropInstallerCopy")
	}
	for _, want := range []string{`Delete "$LOCALAPPDATA\${APP_INSTALLER_STORE_FILE}"`, `RMDir "$LOCALAPPDATA\${PADMATE_UPDATER_DIR}"`} {
		if !strings.Contains(drop, want) {
			t.Errorf("PadmateDropInstallerCopy lacks %q", want)
		}
	}
	if strings.Contains(drop, "RMDir /r") {
		t.Error("PadmateDropInstallerCopy removes the folder with whatever is in it")
	}
	// The build fails when electron-builder names the copy otherwise.
	for _, want := range []string{`!define PADMATE_UPDATER_DIR "padmate-updater"`, `!if "${APP_INSTALLER_STORE_FILE}" != "${PADMATE_UPDATER_DIR}\installer.exe"`} {
		if !strings.Contains(text, want) {
			t.Errorf("installer.nsh lacks %q", want)
		}
	}
}

// customMacro returns the text of an NSIS macro of installer.nsh.
func customMacro(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "!macro "+name+"\n")
	if start < 0 {
		t.Fatalf("installer.nsh has no macro %s", name)
	}
	end := strings.Index(text[start:], "!macroend")
	if end < 0 {
		t.Fatalf("macro %s has no !macroend", name)
	}
	return text[start : start+end]
}

// The wizard never asks for administrator rights (PKG-13): the page for "all
// users or only me" ends in Abort for the installer, so that the code after it,
// which elevates on /allusers, never runs, and the start of the installer
// ignores /allusers and a machine-wide record. Both need to be in the script
// for the page to stay out and no elevation to be asked for.
func TestInstallerIsPerUserOnly(t *testing.T) {
	text := string(readApp(t, "build/installer.nsh"))
	mode := customMacro(t, text, "customInstallMode")
	for _, want := range []string{
		"!ifndef BUILD_UNINSTALLER", `StrCpy $hasPerMachineInstallation "0"`, `StrCpy $hasPerUserInstallation "1"`,
		"!insertmacro setInstallModePerUser", "Abort",
	} {
		if !strings.Contains(mode, want) {
			t.Errorf("customInstallMode lacks %q", want)
		}
	}
	if strings.Contains(mode, "setInstallModePerAllUsers") || strings.Contains(mode, "UAC_RunElevated") {
		t.Error("customInstallMode installs for all users or elevates")
	}
	initMacro := customMacro(t, text, "customInit")
	for _, want := range []string{`StrCpy $hasPerMachineInstallation "0"`, `StrCpy $hasPerUserInstallation "1"`, "!insertmacro setInstallModePerUser"} {
		if !strings.Contains(initMacro, want) {
			t.Errorf("customInit lacks %q: /allusers would install for all users", want)
		}
	}
	// A silent install and one without a folder page check the folder at the
	// start, and the wizard when Next is pressed.
	for _, want := range []string{"${Silent}", "${isUpdated}", "Call PadmateCheckFolder"} {
		if !strings.Contains(initMacro, want) {
			t.Errorf("customInit lacks %q", want)
		}
	}
	if page := customMacro(t, text, "customPageAfterChangeDir"); !strings.Contains(page, "!insertmacro PadmateDirPage") {
		t.Error("customPageAfterChangeDir does not lay out the folder page")
	}
	// The page is NSIS's standard directory page with padmate's functions. NSIS
	// must not judge the text while it is typed: it would grey the Install
	// button for a quoted path or a drive root before Leave could tidy them.
	wizard := string(readApp(t, "build/padmate-wizard.nsh"))
	dirPage := customMacro(t, wizard, "PadmateDirPage")
	for _, want := range []string{
		"MUI_PAGE_CUSTOMFUNCTION_PRE PadmateDirPre", "MUI_PAGE_CUSTOMFUNCTION_SHOW PadmateDirShow",
		"MUI_PAGE_CUSTOMFUNCTION_LEAVE PadmateDirLeave", "!define MUI_DIRECTORYPAGE_VERIFYONLEAVE", "!insertmacro MUI_PAGE_DIRECTORY",
	} {
		if !strings.Contains(dirPage, want) {
			t.Errorf("PadmateDirPage lacks %q", want)
		}
	}
	// Leave looks at the folder the install will use before it goes on.
	_, afterLeave, found := strings.Cut(wizard, "Function PadmateDirLeave")
	leave, _, ended := strings.Cut(afterLeave, "FunctionEnd")
	if !found || !ended {
		t.Fatal("padmate-wizard.nsh has no function PadmateDirLeave")
	}
	for _, call := range []string{"Call PadmateWizardDir", "Call PadmateIsFullPath", "Call PadmateFitsPath", "Call PadmateFolderIsForeign", "Call PadmateCanWrite"} {
		if !strings.Contains(leave, call) {
			t.Errorf("PadmateDirLeave does not %s", call)
		}
	}
}

// The numbers installerpkg checks the installed files against are the ones the
// wizard leaves room for.
func TestWizardScriptDefinesMaxRelativePath(t *testing.T) {
	wizard := string(readApp(t, "build/padmate-wizard.nsh"))
	for name, want := range map[string]int{"PADMATE_MAX_REL": MaxRelativePath, "PADMATE_MAX_PATH": MaxPath} {
		m := regexp.MustCompile(`(?m)^!define ` + name + ` (\d+)\s*$`).FindStringSubmatch(wizard)
		if m == nil {
			t.Errorf("padmate-wizard.nsh does not define %s", name)
			continue
		}
		if got := m[1]; got != strconv.Itoa(want) {
			t.Errorf("padmate-wizard.nsh defines %s as %s, installerpkg has %d", name, got, want)
		}
	}
}

// padmate-wizard.nsh is read by NSIS as it is, and its tests compile it on its
// own: it holds ASCII only, so that it needs no byte order mark.
func TestWizardScriptIsASCII(t *testing.T) {
	for i, b := range readApp(t, "build/padmate-wizard.nsh") {
		if b >= 0x80 {
			t.Fatalf("padmate-wizard.nsh has the byte %#x at offset %d: it must stay ASCII", b, i)
		}
	}
}

// The installer has no license page: electron-builder adds one when the
// build folder holds a license or eula file.
func TestNoLicensePage(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "app", "build"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name := strings.ToLower(e.Name()); strings.HasPrefix(name, "license") || strings.HasPrefix(name, "eula") {
			t.Errorf("app/build/%s would give the installer a license page", e.Name())
		}
	}
}

// Whether and how padmate.exe is signed is decided by PADMATE_SIGN in the
// environment of app/scripts/package-installer.mjs, never by this file: it holds
// no certificate, thumbprint, password or account, and the default build signs
// nothing (PKG-09, ADR-0073).
func TestElectronBuilderSigningConfig(t *testing.T) {
	config := string(readApp(t, "electron-builder.yml"))
	for _, key := range []string{
		"certificateFile", "certificatePassword", "certificateSha1", "certificateSubjectName",
		"azureSignOptions", "signExts", "verifyUpdateCodeSignature", "publisherName", "CSC_LINK",
	} {
		if strings.Contains(config, key) {
			t.Errorf("electron-builder.yml mentions %s: the signing mode is chosen by PADMATE_SIGN, and publisherName by PADMATE_SIGN_SUBJECT", key)
		}
	}
	// The platform key wins over a top-level one, so the script flips this one
	// (--config.win.forceCodeSigning=true); the default build is unsigned.
	for key, value := range map[string]string{
		"forceCodeSigning": "false", "signAndEditExecutable": "true", "differentialPackage": "false",
		"rfc3161TimeStampServer": "http://timestamp.digicert.com",
	} {
		if got := configValue(t, config, key); got != value {
			t.Errorf("%s is %q, want %q", key, got, value)
		}
	}
	// SHA-256 only, as a list: the command line can set scalars only, so the
	// array has to be in this file, under win.signtoolOptions.
	if !strings.Contains(config, "  signtoolOptions:\n") || !strings.Contains(config, "    signingHashAlgorithms:\n      - sha256\n") {
		t.Error("win.signtoolOptions.signingHashAlgorithms is not [sha256]")
	}
	if strings.Index(config, "nsis:") < strings.Index(config, "  signtoolOptions:") {
		t.Error("signtoolOptions is not under win")
	}
}

// The script that signs reads the variables the installer smoke test and the
// release workflow document, and the Taskfile passes through.
func TestPackageInstallerScriptIsTheOnlyPlaceThatSigns(t *testing.T) {
	script := string(readApp(t, "scripts/package-installer.mjs"))
	for _, want := range []string{
		"PADMATE_SIGN", "PADMATE_SIGN_SHA1", "PADMATE_SIGN_SUBJECT", "PADMATE_SIGN_TIMESTAMP", "PADMATE_SIGN_TEST",
		"PADMATE_SIGN_AZURE_ENDPOINT", "--config.win.forceCodeSigning=true", "WIN_CSC_LINK", "SIGNTOOL_PATH",
		"ELECTRON_BUILDER_OFFLINE",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("package-installer.mjs does not mention %s", want)
		}
	}
}
