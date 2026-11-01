package installersmoke

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
)

// The language dialog and the page of the desktop shortcut of the installer's
// wizard (ADR-0018), run in a wizard of their own and driven through their
// windows. The functions are the ones of app/build/padmate-options.nsh that the
// installer expands too (PadmateOptionFunctions): which language the dialog
// starts on, that the wizard then speaks the language chosen and writes it as
// the window's language, that Cancel ends the run, that --language= skips the
// dialog, the box and what unticking it does, and a silent run with and
// without the flags. TestInstallerWizard checks electron-builder's part on the
// real installer.
//
// Like TestWizardPage it builds a program with the makensis that
// electron-builder downloaded, and runs it: a window for a moment, and no file
// outside the test's folder.
//
//	PADMATE_TEST_NSIS=1 go test -run TestOptionsPage ./internal/installersmoke

// langDLLName is how the language dialog lists a language of the window.
var langDLLName = map[string]string{installerpkg.LanguageChinese: "中文(简体)", installerpkg.LanguageEnglish: "English"}

// optionsScript is a wizard with the page of the desktop shortcut and the
// progress, whose .onInit chooses the language and the shortcut as the
// installer's does. It writes "$LANGUAGE|$padmateDesktop" to the file
// PADMATE_TOY_OUT, and the window's language into the folder PADMATE_TOY_UI.
func optionsScript(build, exe string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Unicode true\n!include \"MUI2.nsh\"\n!include \"LogicLib.nsh\"\n!include \"nsDialogs.nsh\"\n!include \"FileFunc.nsh\"\n!addincludedir %s\n", nsisString(build))
	b.WriteString(`!define PADMATE_LANGDLL_TITLE "padmate"
!define PADMATE_LANGDLL_TEXT "请选择 padmate 使用的语言。$\r$\nChoose the language for padmate."
!define PADMATE_DESKTOP_LINK "$out.lnk"
!define INSTALL_REGISTRY_KEY "Software\padmate-options-page-test"
!macro _isUpdated _a _b _t _f
  StrCmp "0" "1" ` + "`${_t}` `${_f}`" + `
!macroend
!define isUpdated ` + "`\"\" isUpdated \"\"`" + `
!macro _isNoDesktopShortcut _a _b _t _f
  Push $0
  Push $1
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "--no-desktop-shortcut" $1
  Pop $1
  Pop $0
  IfErrors ` + "`${_f}` `${_t}`" + `
!macroend
!define isNoDesktopShortcut ` + "`\"\" isNoDesktopShortcut \"\"`" + `
!include "padmate-options.nsh"
Name "padmate options test"
`)
	fmt.Fprintf(&b, "OutFile %s\n", nsisString(exe))
	b.WriteString(`RequestExecutionLevel user
ShowInstDetails nevershow
AutoCloseWindow true
Var out

!insertmacro PadmateOptionsPage
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"
LangString padmateOptionsTitle ${LANG_ENGLISH} "Options"
LangString padmateOptionsTitle ${LANG_SIMPCHINESE} "安装选项"
LangString padmateOptionsText ${LANG_ENGLISH} "padmate is always added to the Start menu."
LangString padmateOptionsText ${LANG_SIMPCHINESE} "开始菜单里总会添加 padmate。"
`)
	fmt.Fprintf(&b, "LangString padmateDesktopShortcut ${LANG_ENGLISH} %s\n", nsisString(installerpkg.DesktopShortcutText[1]))
	fmt.Fprintf(&b, "LangString padmateDesktopShortcut ${LANG_SIMPCHINESE} %s\n", nsisString(installerpkg.DesktopShortcutText[0]))
	b.WriteString(`!insertmacro PadmateOptionFunctions

Function .onInit
  ReadEnvStr $out PADMATE_TOY_OUT
  Call PadmateChooseLanguage
  Call PadmateDesktopInit
FunctionEnd

Section
  ReadEnvStr $1 PADMATE_TOY_UI
  StrCpy $R8 $LANGUAGE
  Call PadmateUILanguage
  StrCpy $R9 $R8
  StrCpy $R8 $1
  Call PadmateWriteLanguage
  Pop $2
  FileOpen $0 "$out" w
  FileWriteUTF16LE /BOM $0 "$LANGUAGE|$padmateDesktop"
  FileClose $0
SectionEnd
`)
	return b.String()
}

func TestOptionsPage(t *testing.T) {
	if os.Getenv(envTestNSIS) != "1" {
		t.Skip("set " + envTestNSIS + "=1 to build and drive the language dialog and the page of the desktop shortcut in a wizard of their own (a window appears for a moment)")
	}
	makensis := findMakensis(t)
	build, err := filepath.Abs(filepath.Join("..", "..", "..", "app", "build"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "options.exe")
	nsi := filepath.Join(dir, "options.nsi")
	if err := os.WriteFile(nsi, []byte(optionsScript(build, exe)), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	compile := exec.CommandContext(ctx, makensis, "-WX", "-INPUTCHARSET", "UTF8", nsi)
	compile.Dir = dir
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("makensis: %v\n%s", err, output)
	}

	base := os.Environ()
	n := 0
	// toy gives the paths of one run: the smoke for its environment, the file
	// it reports in and the folder of the window's language.
	toy := func(t *testing.T) (s *smoke, out, ui string) {
		t.Helper()
		n++
		out = filepath.Join(dir, fmt.Sprintf("out%d", n))
		ui = filepath.Join(dir, fmt.Sprintf("ui%d", n))
		s = &smoke{t: t, root: dir, env: append(append([]string(nil), base...), "PADMATE_TOY_OUT="+out, "PADMATE_TOY_UI="+ui)}
		return s, out, ui
	}
	read := func(t *testing.T, file string) string {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return decodeUTF16LE(t, data)
	}
	// reported checks what a run reported and the language file it wrote.
	reported := func(t *testing.T, out, ui, language string, desktop bool) {
		t.Helper()
		want := "1033|"
		if language == installerpkg.LanguageChinese {
			want = "2052|"
		}
		if desktop {
			want += "1"
		} else {
			want += "0"
		}
		if got := read(t, out); got != want {
			t.Errorf("the wizard reported %q (language|desktop), want %q", got, want)
		}
		if data, err := os.ReadFile(filepath.Join(ui, installerpkg.LanguageFile)); err != nil || string(data) != installerpkg.LanguageLine(language) {
			t.Errorf("the language file holds %q, %v; want %q", data, err, installerpkg.LanguageLine(language))
		}
	}
	// next leaves the page with Next and waits for the wizard to end.
	next := func(t *testing.T, w *wizardRun, d dialog) {
		t.Helper()
		if err := d.click(idOK); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.done:
		case <-time.After(time.Minute):
			t.Fatal("the wizard did not end after Next")
		}
		if w.code != 0 {
			t.Errorf("the wizard exited with code %d", w.code)
		}
	}
	system := installerpkg.InstallerLanguage(userDefaultUILanguage())

	for _, c := range []struct{ language, other string }{
		{installerpkg.LanguageEnglish, installerpkg.LanguageChinese},
		{installerpkg.LanguageChinese, installerpkg.LanguageEnglish},
	} {
		t.Run("the dialog offers both languages from the system's, and the wizard speaks the one chosen: "+c.language, func(t *testing.T) {
			s, out, ui := toy(t)
			w := s.startWizard(exe)
			d, list := w.languageDialog()
			if got := windowText(d.hwnd); got != "padmate" {
				t.Errorf("the language dialog is called %q", got)
			}
			if count, _ := sendMessage(list, cbGetCount, 0, 0); count != 2 {
				t.Errorf("the language dialog lists %d languages, want 2", count)
			}
			if got := windowText(list); got != langDLLName[system] {
				t.Errorf("the language dialog starts on %q, want %q, the system's", got, langDLLName[system])
			}
			w.chooseLanguage(d, list, langDLLName[c.language])
			page, box := w.optionsPage()
			text := installerpkg.DesktopShortcutText[1]
			if c.language == installerpkg.LanguageChinese {
				text = installerpkg.DesktopShortcutText[0]
			}
			if got := windowText(box); got != text {
				t.Errorf("the box says %q, want %q", got, text)
			}
			if !isChecked(box) {
				t.Error("the box is not ticked on a first installation")
			}
			next(t, w, page)
			reported(t, out, ui, c.language, true)
		})
	}

	t.Run("an unticked box makes no shortcut", func(t *testing.T) {
		s, out, ui := toy(t)
		w := s.startWizard(exe, "--language=zh-CN")
		page, box := w.optionsPage()
		if err := postClick(box); err != nil {
			t.Fatal(err)
		}
		w.wait("the box unticked", 10*time.Second, func() bool { return !isChecked(box) })
		next(t, w, page)
		reported(t, out, ui, installerpkg.LanguageChinese, false)
	})

	t.Run("--language= skips the dialog, and Cancel in the dialog ends the run", func(t *testing.T) {
		s, out, ui := toy(t)
		w := s.startWizard(exe, "--language=en")
		page, box := w.optionsPage()
		if got := windowText(box); got != installerpkg.DesktopShortcutText[1] {
			t.Errorf("the box says %q in a wizard started with --language=en", got)
		}
		next(t, w, page)
		reported(t, out, ui, installerpkg.LanguageEnglish, true)

		s, out, ui = toy(t)
		w = s.startWizard(exe)
		d, _ := w.languageDialog()
		if err := d.click(idCancel); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.done:
		case <-time.After(time.Minute):
			t.Fatal("the wizard did not end after Cancel in the language dialog")
		}
		if w.code != 1 {
			t.Errorf("Cancel in the language dialog: exit code %d, want 1", w.code)
		}
		for _, f := range []string{out, ui} {
			if _, err := os.Stat(f); err == nil {
				t.Errorf("Cancel in the language dialog went on: %s exists", f)
			}
		}
	})

	t.Run("a silent run asks nothing, takes the system's language and makes the shortcut, unless told otherwise", func(t *testing.T) {
		for _, c := range []struct {
			args     string
			language string
			desktop  bool
		}{
			{"/S", system, true},
			{"/S --language=zh-CN", installerpkg.LanguageChinese, true},
			{"/S --language=en --no-desktop-shortcut", installerpkg.LanguageEnglish, false},
		} {
			s, out, ui := toy(t)
			if code, output, err := s.runCommand(exe, c.args, time.Minute); err != nil || code != 0 {
				t.Fatalf("%s: exit code %d, %v\n%s", c.args, code, err, output)
			}
			t.Run(c.args, func(t *testing.T) { reported(t, out, ui, c.language, c.desktop) })
		}
	})
}
