package installerpkg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// languageNamedCases are values of --language= and the installer language
// each names, "" for none.
var languageNamedCases = []struct{ value, want string }{
	{"zh-CN", "2052"}, {"ZH-cn", "2052"}, {"en", "1033"}, {"EN", "1033"},
	{"en-US", ""}, {"zh", ""}, {"fr", ""}, {"", ""},
}

// installerLanguageID is the NSIS language id of a language of the window.
func installerLanguageID(language string) string {
	if language == LanguageChinese {
		return "2052"
	}
	return "1033"
}

// TestOptionFunctionsNSIS compiles the rules of the wizard's language and
// desktop shortcut (app/build/padmate-options.nsh) into a small silent program
// of its own, as electron-builder's makensis does, and runs every case of
// TestInstallerLanguage, TestWritesLanguage and TestDesktopDefault through it,
// with the language a command line names. The program writes only under the
// test's folder; it is not the installer and touches nothing else.
//
//	PADMATE_TEST_NSIS=1 go test -run TestOptionFunctionsNSIS ./internal/installerpkg
func TestOptionFunctionsNSIS(t *testing.T) {
	if os.Getenv(envTestNSIS) != "1" {
		t.Skip("set " + envTestNSIS + "=1 to compile and run the NSIS option rules")
	}
	makensis := findMakensis(t)
	build, err := filepath.Abs(filepath.Join("..", "..", "..", "app", "build"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "options.exe")

	// The folders of the language file: one per case, with the file as the
	// case has it, and one that is not there yet.
	folder := func(i int) string { return filepath.Join(dir, "ui", fmt.Sprintf("case%d", i)) }
	for i, c := range languageFileCases {
		if err := os.MkdirAll(folder(i), 0o755); err != nil {
			t.Fatal(err)
		}
		if c.exists {
			if err := os.WriteFile(filepath.Join(folder(i), LanguageFile), []byte(c.old), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	missing := filepath.Join(dir, "ui", "not there", "ui")
	// The language each case writes: the two, in turn.
	writes := func(i int) string {
		if i%2 == 0 {
			return LanguageEnglish
		}
		return LanguageChinese
	}

	var script strings.Builder
	fmt.Fprintf(&script, "Unicode true\n!include \"MUI2.nsh\"\n!include \"LogicLib.nsh\"\n!include \"nsDialogs.nsh\"\n!include \"FileFunc.nsh\"\n!addincludedir %s\n", nsisString(build))
	script.WriteString(`!define PADMATE_LANGDLL_TITLE "padmate"
!define PADMATE_LANGDLL_TEXT "Choose"
!define PADMATE_DESKTOP_LINK "$TEMP\padmate-options-test-no-such.lnk"
!define INSTALL_REGISTRY_KEY "Software\padmate-options-rule-test"
!macro _isUpdated _a _b _t _f
  StrCmp "0" "1" ` + "`${_t}` `${_f}`" + `
!macroend
!define isUpdated ` + "`\"\" isUpdated \"\"`" + `
!macro _isNoDesktopShortcut _a _b _t _f
  StrCmp "0" "1" ` + "`${_t}` `${_f}`" + `
!macroend
!define isNoDesktopShortcut ` + "`\"\" isNoDesktopShortcut \"\"`" + `
!include "padmate-options.nsh"
Name "options"
`)
	fmt.Fprintf(&script, "OutFile %s\nRequestExecutionLevel user\nSilentInstall silent\nShowInstDetails nevershow\n", nsisString(exe))
	script.WriteString(`!insertmacro PadmateOptionsPage
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"
LangString padmateOptionsTitle ${LANG_ENGLISH} "Options"
LangString padmateOptionsTitle ${LANG_SIMPCHINESE} "Options"
LangString padmateOptionsText ${LANG_ENGLISH} "Text"
LangString padmateOptionsText ${LANG_SIMPCHINESE} "Text"
LangString padmateDesktopShortcut ${LANG_ENGLISH} "Box"
LangString padmateDesktopShortcut ${LANG_SIMPCHINESE} "Box"
!insertmacro PadmateOptionFunctions
Function .onInit
  Call PadmateChooseLanguage
  Call PadmateDesktopInit
FunctionEnd
Section
`)
	fmt.Fprintf(&script, "  Call PadmateLanguageOption\n  ReadEnvStr $1 PADMATE_TOY_OUT\n  FileOpen $0 $1 w\n  FileWriteUTF16LE /BOM $0 \"\"\n  FileWriteUTF16LE $0 \"$LANGUAGE|$padmateDesktop|$R8$\\r$\\n\"\n")
	for _, c := range languageIDCases {
		fmt.Fprintf(&script, "  StrCpy $R8 %d\n  Call PadmateLanguageFor\n  StrCpy $1 $R8\n  Call PadmateUILanguage\n  FileWriteUTF16LE $0 \"$1|$R8$\\r$\\n\"\n", c.id)
	}
	for _, c := range languageNamedCases {
		fmt.Fprintf(&script, "  StrCpy $R8 %s\n  Call PadmateLanguageNamed\n  FileWriteUTF16LE $0 \"$R8$\\r$\\n\"\n", nsisString(c.value))
	}
	for i := range languageFileCases {
		fmt.Fprintf(&script, "  StrCpy $R8 %s\n  StrCpy $R9 %s\n  Call PadmateWriteLanguage\n  Pop $1\n  FileWriteUTF16LE $0 \"$1$\\r$\\n\"\n",
			nsisString(folder(i)), nsisString(writes(i)))
	}
	fmt.Fprintf(&script, "  StrCpy $R8 %s\n  StrCpy $R9 en\n  Call PadmateWriteLanguage\n  Pop $1\n  FileWriteUTF16LE $0 \"$1$\\r$\\n\"\n", nsisString(missing))
	desktopCases := []struct{ installed, exists bool }{{false, false}, {false, true}, {true, true}, {true, false}}
	for _, c := range desktopCases {
		recorded, exists := "", "0"
		if c.installed {
			recorded = `D:\Apps\padmate`
		}
		if c.exists {
			exists = "1"
		}
		fmt.Fprintf(&script, "  StrCpy $R8 %s\n  StrCpy $R9 %s\n  Call PadmateDesktopDefault\n  Pop $1\n  FileWriteUTF16LE $0 \"$1$\\r$\\n\"\n", nsisString(recorded), exists)
	}
	script.WriteString("  FileClose $0\nSectionEnd\n")
	nsi := filepath.Join(dir, "options.nsi")
	if err := os.WriteFile(nsi, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	compile := exec.CommandContext(ctx, makensis, "-WX", "-INPUTCHARSET", "UTF8", nsi)
	compile.Dir = dir
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("makensis: %v\n%s", err, output)
	}

	system := InstallerLanguage(userDefaultUILanguage(t))
	// The language each command line leads to, and the value of --language=
	// the program reads in it.
	runs := []struct {
		args, language, value string
	}{
		{"", system, ""},
		{"--language=en", LanguageEnglish, "en"},
		{"--language=zh-CN", LanguageChinese, "zh-CN"},
		{`/S --language="zh-CN" /D=C:\x y`, LanguageChinese, "zh-CN"},
		{"--LANGUAGE=En --no-desktop-shortcut", LanguageEnglish, "En"},
		{"--language=fr", system, "fr"},
		{"x--language=en", system, ""},
		{"--language=", system, ""},
	}
	for n, r := range runs {
		// Every run but the first writes the files again: what the first wrote
		// is the installer's own, so later runs only check the language.
		lines := runOptions(ctx, t, exe, filepath.Join(dir, fmt.Sprintf("out%d.txt", n)), r.args)
		if want := installerLanguageID(r.language) + "|1|" + r.value; lines[0] != want {
			t.Errorf("%q: the program chose %q (language|desktop|--language=), want %q", r.args, lines[0], want)
		}
		if n == 0 {
			checkOptionLines(t, lines[1:], folder, writes, missing)
		}
	}
}

// runOptions runs the option program with args and returns the lines it wrote.
func runOptions(ctx context.Context, t *testing.T, exe, out, args string) []string {
	t.Helper()
	run := exec.CommandContext(ctx, exe)
	run.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: strings.TrimSpace(`"` + exe + `" ` + args)}
	run.Env = append(os.Environ(), "PADMATE_TOY_OUT="+out)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", exe, args, err, output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(decodeUTF16LE(t, data), "\r\n"), "\r\n")
}

// checkOptionLines compares what the first run wrote with the Go rules.
func checkOptionLines(t *testing.T, lines []string, folder func(int) string, writes func(int) string, missing string) {
	t.Helper()
	want := len(languageIDCases) + len(languageNamedCases) + len(languageFileCases) + 1 + 4
	if len(lines) != want {
		t.Fatalf("the program wrote %d lines for %d cases:\n%q", len(lines), want, lines)
	}
	for _, c := range languageIDCases {
		if got, want := lines[0], installerLanguageID(c.want)+"|"+c.want; got != want {
			t.Errorf("language id %#04x: NSIS gives %q, InstallerLanguage %q", c.id, got, want)
		}
		lines = lines[1:]
	}
	for _, c := range languageNamedCases {
		if lines[0] != c.want {
			t.Errorf("--language=%s: NSIS gives %q, want %q", c.value, lines[0], c.want)
		}
		lines = lines[1:]
	}
	for i, c := range languageFileCases {
		flag := "0"
		if c.writes {
			flag = "1"
		}
		if lines[0] != flag {
			t.Errorf("%s: NSIS gives %q, WritesLanguage %v", c.name, lines[0], c.writes)
		}
		lines = lines[1:]
		data, err := os.ReadFile(filepath.Join(folder(i), LanguageFile))
		switch {
		case c.writes && (err != nil || string(data) != LanguageLine(writes(i))):
			t.Errorf("%s: the file holds %q, %v; want %q", c.name, data, err, LanguageLine(writes(i)))
		case !c.writes && (err != nil || string(data) != c.old):
			t.Errorf("%s: the file was changed to %q, %v; want it kept, %q", c.name, data, err, c.old)
		}
	}
	if data, err := os.ReadFile(filepath.Join(missing, LanguageFile)); lines[0] != "1" || err != nil || string(data) != LanguageLine(LanguageEnglish) {
		t.Errorf("a folder that is not there: NSIS gives %q and the file %q, %v", lines[0], data, err)
	}
	lines = lines[1:]
	for i, c := range []struct{ installed, exists bool }{{false, false}, {false, true}, {true, true}, {true, false}} {
		want := "0"
		if DesktopDefault(c.installed, c.exists) {
			want = "1"
		}
		if lines[i] != want {
			t.Errorf("desktop default (installed %v, link %v): NSIS gives %q, DesktopDefault %q", c.installed, c.exists, lines[i], want)
		}
	}
}

// userDefaultUILanguage is GetUserDefaultUILanguage, what the installer asks
// Windows for.
func userDefaultUILanguage(t *testing.T) uint16 {
	t.Helper()
	r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage").Call()
	return uint16(r)
}
