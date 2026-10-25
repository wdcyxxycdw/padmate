package installersmoke

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// The folder page of the installer's wizard (PKG-13, ADR-0068), run in a
// wizard of its own and driven through its windows. The page's functions are
// the ones of app/build/padmate-wizard.nsh that the installer expands too
// (PadmatePageFunctions), so what is checked here is the code of the installer
// as it runs on a real page: reading the field, rewriting it, staying on the
// page, the messages (a folder of other files, a text that is no folder, a path
// too long, a folder that cannot be written), that a pasted path and a drive
// root get as far as the page's check at all, the Back button, the finish
// page's "run" box, and that a silent run never calls the page. What it leaves
// out is electron-builder's part of the installer, which TestInstallerWizard
// checks on the real installer.
//
// It builds a program with the makensis that electron-builder downloaded, and
// runs it: a window for a moment, and no file outside the test's folder. It is
// not the installer and installs nothing.
//
//	PADMATE_TEST_NSIS=1 go test -run TestWizardPage ./internal/installersmoke
const (
	envTestNSIS = "PADMATE_TEST_NSIS"
	envMakensis = "PADMATE_MAKENSIS"
)

// findMakensis returns the makensis.exe electron-builder downloaded the first
// time it built the installer, or the one named by PADMATE_MAKENSIS.
func findMakensis(t *testing.T) string {
	t.Helper()
	if exe := os.Getenv(envMakensis); exe != "" {
		return exe
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		t.Skip("no LOCALAPPDATA to look for electron-builder's NSIS in")
	}
	found, _ := filepath.Glob(filepath.Join(local, "electron-builder", "Cache", "nsis*", "nsis*", "makensis.exe"))
	if len(found) == 0 {
		t.Skip("electron-builder has not downloaded NSIS yet (run task package:installer), or set " + envMakensis)
	}
	return found[len(found)-1]
}

// nsisString quotes s for a double-quoted NSIS string.
func nsisString(s string) string {
	return `"` + strings.NewReplacer(`$`, `$$`, `"`, `$\"`).Replace(s) + `"`
}

// pageScript is a wizard with a skipped first page (as the install-mode page
// is), the folder page, the progress and a finish page with "run" ticked. It
// starts in the folder PADMATE_TOY_START names, writes the folder it was
// given to the file PADMATE_TOY_OUT, and a file next to that one when the
// "run" box was ticked.
func pageScript(build, exe string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Unicode true\n!include \"MUI2.nsh\"\n!include \"LogicLib.nsh\"\n!include \"nsDialogs.nsh\"\n!addincludedir %s\n", nsisString(build))
	b.WriteString(`!define APP_FILENAME "padmate"
!define APP_EXECUTABLE_FILENAME "padmate.exe"
!define UNINSTALL_FILENAME "Uninstall padmate.exe"
!define INSTALL_REGISTRY_KEY "Software\padmate-wizard-page-test"
!macro _isUpdated _a _b _t _f
  StrCmp "0" "1" ` + "`${_t}` `${_f}`" + `
!macroend
!define isUpdated ` + "`\"\" isUpdated \"\"`" + `
!include "padmate-wizard.nsh"
Name "padmate page test"
`)
	fmt.Fprintf(&b, "OutFile %s\n", nsisString(exe))
	b.WriteString(`RequestExecutionLevel user
ShowInstDetails nevershow
Var out

Page custom HiddenPre
Function HiddenPre
  Abort
FunctionEnd

!insertmacro PadmateDirPage
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Run padmate"
!define MUI_FINISHPAGE_RUN_FUNCTION PageRun
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_LANGUAGE "English"
LangString padmateDirForeign ${LANG_ENGLISH} "The folder $INSTDIR already holds other files, and uninstalling padmate would remove them. Choose another location."
LangString padmateDirInvalid ${LANG_ENGLISH} "Enter the full path of a folder, such as D:\Apps."
LangString padmateDirTooLong ${LANG_ENGLISH} "The path of the folder $INSTDIR is too long. Choose a shorter location."
LangString padmateDirNoWrite ${LANG_ENGLISH} "Cannot write to the folder $INSTDIR. Choose another location."

!insertmacro PadmatePathFunctions
!insertmacro PadmatePageFunctions

Function .onInit
  ReadEnvStr $INSTDIR PADMATE_TOY_START
  ReadEnvStr $out PADMATE_TOY_OUT
FunctionEnd

Function PageRun
  FileOpen $0 "$out.run" w
  FileClose $0
FunctionEnd

Section
  FileOpen $0 "$out" w
  FileWriteUTF16LE /BOM $0 "$INSTDIR"
  FileClose $0
SectionEnd
`)
	return b.String()
}

// decodeUTF16LE reads what FileWriteUTF16LE wrote: a byte order mark, then
// UTF-16 little endian.
func decodeUTF16LE(t *testing.T, data []byte) string {
	t.Helper()
	data = bytes.TrimPrefix(data, []byte{0xFF, 0xFE})
	if len(data)%2 != 0 {
		t.Fatalf("%d bytes are not UTF-16", len(data))
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func TestWizardPage(t *testing.T) {
	if os.Getenv(envTestNSIS) != "1" {
		t.Skip("set " + envTestNSIS + "=1 to build and drive the folder page in a wizard of its own (a window appears for a moment)")
	}
	makensis := findMakensis(t)
	build, err := filepath.Abs(filepath.Join("..", "..", "..", "app", "build"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "page.exe")
	nsi := filepath.Join(dir, "page.nsi")
	if err := os.WriteFile(nsi, []byte(pageScript(build, exe)), 0o644); err != nil {
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
	// run starts the wizard in start; out is the file it reports the folder in.
	n := 0
	run := func(t *testing.T, start string) (w *wizardRun, out string) {
		t.Helper()
		n++
		out = filepath.Join(dir, fmt.Sprintf("out%d", n))
		s := &smoke{t: t, root: dir, env: append(append([]string(nil), base...), "PADMATE_TOY_START="+start, "PADMATE_TOY_OUT="+out)}
		return s.startWizard(exe), out
	}
	read := func(t *testing.T, file string) string {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return decodeUTF16LE(t, data)
	}
	def := filepath.Join(dir, "Programs", "padmate")

	t.Run("the folder the page offers, left alone, is used as it is; run is ticked", func(t *testing.T) {
		w, out := run(t, filepath.Join(dir, "Old folder", "Foo"))
		d, field := w.folderPage()
		offered := filepath.Join(dir, "Old folder", "Foo")
		if got := windowText(field); got != offered {
			t.Errorf("the page offers %q, want %q", got, offered)
		}
		// The page before it is skipped: nothing to go back to.
		if d.enabled(idBack) {
			t.Error("the folder page lets the person go back")
		}
		if err := d.click(idOK); err != nil {
			t.Fatal(err)
		}
		fin := w.finishPage(time.Minute)
		if box := fin.checkBox(); !isChecked(box) {
			t.Error("the finish page does not tick \"run\" by default")
		}
		if err := fin.click(idOK); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.done:
		case <-time.After(30 * time.Second):
			t.Fatal("the wizard did not close after Finish")
		}
		if got := read(t, out); got != offered {
			t.Errorf("the wizard got the folder %q, want %q (an old folder not named padmate keeps its place)", got, offered)
		}
		if _, err := os.Stat(out + ".run"); err != nil {
			t.Error("the ticked box did not run the application")
		}
	})

	t.Run("a typed parent gets the folder padmate in it, shown before the install", func(t *testing.T) {
		w, out := run(t, def)
		d, field := w.folderPage()
		parent := filepath.Join(dir, "我的 软件")
		want := w.chooseFolder(d, field, parent)
		if want != filepath.Join(parent, "padmate") {
			t.Fatalf("the wizard chose %q", want)
		}
		fin := w.finishPage(time.Minute)
		w.finish(fin)
		if got := read(t, out); got != want {
			t.Errorf("the wizard got the folder %q, want %q", got, want)
		}
		if _, err := os.Stat(out + ".run"); err == nil {
			t.Error("the unticked box ran the application")
		}
	})

	t.Run("a folder named padmate, in any case, is used as it is, without stopping", func(t *testing.T) {
		w, out := run(t, def)
		d, field := w.folderPage()
		typed := filepath.Join(dir, "Apps", "PADMATE") + `\`
		if got := w.chooseFolder(d, field, typed); got != strings.TrimSuffix(typed, `\`) {
			t.Fatalf("the wizard chose %q", got)
		}
		w.finish(w.finishPage(time.Minute))
		if got := read(t, out); got != strings.TrimSuffix(typed, `\`) {
			t.Errorf("the wizard got the folder %q", got)
		}
	})

	t.Run("a folder of other files is refused with a message, on the page, and Cancel leaves it", func(t *testing.T) {
		foreign := filepath.Join(dir, "guard", "padmate")
		note := filepath.Join(foreign, "mine", "notes.txt")
		if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(note, []byte("not padmate's"), 0o644); err != nil {
			t.Fatal(err)
		}
		w, out := run(t, def)
		d, field := w.folderPage()
		if err := setWindowText(field, foreign); err != nil {
			t.Fatal(err)
		}
		if err := d.click(idOK); err != nil {
			t.Fatal(err)
		}
		w.wait("message about the folder", 30*time.Second, func() bool { _, ok := w.box(); return ok })
		box, _ := w.box()
		if say := box.text(); !strings.Contains(say, foreign) || !strings.Contains(say, "already holds other files") {
			t.Errorf("the message is %q: it should name the folder and say it holds other files", say)
		}
		if err := box.dismiss(); err != nil {
			t.Fatal(err)
		}
		w.wait("message to go", 10*time.Second, func() bool { _, ok := w.box(); return !ok })
		if now, ok := w.wizard(); !ok || now.folderField() == 0 || windowText(now.folderField()) != foreign {
			t.Errorf("the wizard left the folder page, or changed the folder, after the message%s", w.describe())
		}
		if err := d.click(idCancel); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.done:
		case <-time.After(time.Minute):
			t.Fatal("the wizard did not close after Cancel")
		}
		if _, err := os.Stat(note); err != nil {
			t.Error("the folder of other files lost its file")
		}
		if _, err := os.Stat(out); err == nil {
			t.Error("the wizard went on to install")
		}
	})

	// NSIS does not judge the text while it is typed (the page asks it to look
	// on leaving, MUI_DIRECTORYPAGE_VERIFYONLEAVE): its own check greys the
	// Install button for a quoted path, a path with a space in front and a
	// drive root, and the page would never be called to tidy them.
	t.Run("a path as it is pasted is tidied: quotes, spaces, a drive root", func(t *testing.T) {
		apps := filepath.Join(dir, "Apps 粘贴")
		want := filepath.Join(apps, "padmate")
		cases := []struct{ name, typed, want string }{
			{"quotes around it", `"` + apps + `"`, want},
			{"spaces around it", "   " + apps + "  ", want},
			{"quotes and a backslash", `"` + apps + `\"`, want},
			{"a folder that is there already", filepath.Join(dir, "Existing"), filepath.Join(dir, "Existing", "padmate")},
		}
		if err := os.MkdirAll(filepath.Join(dir, "Existing", "padmate"), 0o755); err != nil {
			t.Fatal(err)
		}
		// The root of the drive the test runs on, if a folder can be made in it.
		root := filepath.VolumeName(dir) + `\`
		if probe, err := os.MkdirTemp(root, "padmate-test-"); err == nil {
			_ = os.Remove(probe)
			cases = append(cases, struct{ name, typed, want string }{"a drive root", root, root + "padmate"})
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				w, out := run(t, def)
				d, field := w.folderPage()
				if got := w.chooseFolder(d, field, c.typed); got != c.want {
					t.Fatalf("the wizard chose %q, want %q", got, c.want)
				}
				w.finish(w.finishPage(time.Minute))
				if got := read(t, out); got != c.want {
					t.Errorf("the wizard got the folder %q, want %q", got, c.want)
				}
			})
		}
	})

	// refused types a text, presses Next and checks the page says something
	// that holds every string of says, stays and does not go on.
	refused := func(t *testing.T, typed string, says ...string) {
		t.Helper()
		w, out := run(t, def)
		d, field := w.folderPage()
		if err := setWindowText(field, typed); err != nil {
			t.Fatal(err)
		}
		if err := d.click(idOK); err != nil {
			t.Fatal(err)
		}
		w.wait("a message", 30*time.Second, func() bool { _, ok := w.box(); return ok })
		box, _ := w.box()
		say := box.text()
		for _, s := range says {
			if !strings.Contains(say, s) {
				t.Errorf("the message is %q, which lacks %q", say, s)
			}
		}
		if err := box.dismiss(); err != nil {
			t.Fatal(err)
		}
		w.wait("the message to go", 10*time.Second, func() bool { _, ok := w.box(); return !ok })
		if now, ok := w.wizard(); !ok || now.folderField() == 0 {
			t.Errorf("the wizard left the folder page%s", w.describe())
		}
		if err := d.click(idCancel); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.done:
		case <-time.After(time.Minute):
			t.Fatal("the wizard did not close after Cancel")
		}
		if _, err := os.Stat(out); err == nil {
			t.Error("the wizard went on to install")
		}
	}

	t.Run("a text that is no folder, one too long and one that cannot be written get a message", func(t *testing.T) {
		const noFolder = "full path"
		apps := filepath.Join(dir, "Apps")
		cases := []struct{ name, typed, says string }{
			{"nothing", `  ""  `, noFolder},
			{"a bare name", "padmate", noFolder},
			{"a relative path", `Apps\Sub`, noFolder},
			{"forward slashes", strings.ReplaceAll(apps, `\`, "/"), noFolder},
			{"a character Windows refuses", apps + "?", noFolder},
			{"a path too long to install into", filepath.Join(dir, strings.Repeat("长", 100)), "too long"},
			// A drive that is not there cannot be written to.
			{"a drive that is not there", unusedDrive(t) + `:\Apps`, "Cannot write"},
		}
		// The Windows folder is not for a user to write to, an administrator
		// may: the probe would succeed and the case would say nothing.
		if !windows.GetCurrentProcessToken().IsElevated() {
			cases = append(cases, struct{ name, typed, says string }{"a folder the user may not write to", os.Getenv("SystemRoot"), "Cannot write"})
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) { refused(t, c.typed, c.says) })
		}
	})

	t.Run("a silent run never calls the page: the folder it was given is used as it is", func(t *testing.T) {
		start := filepath.Join(dir, "Exact folder", "not named for the application")
		out := filepath.Join(dir, "silent")
		s := &smoke{t: t, root: dir, env: append(append([]string(nil), base...), "PADMATE_TOY_START="+start, "PADMATE_TOY_OUT="+out)}
		if code, output, err := s.runCommand(exe, "/S", time.Minute); err != nil || code != 0 {
			t.Fatalf("silent run: exit code %d, %v\n%s", code, err, output)
		}
		if got := read(t, out); got != start {
			t.Errorf("a silent run used the folder %q, want %q: the page's functions ran", got, start)
		}
	})

	// The test of writing makes a folder or a file and removes it again.
	t.Run("the test of writing leaves nothing behind", func(t *testing.T) {
		var left []string
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err == nil && d.Name() == ".padmate-write-test" {
				left = append(left, path)
			}
			return nil
		})
		if len(left) > 0 {
			t.Errorf("the page left %v behind", left)
		}
		for _, parent := range []string{filepath.VolumeName(dir) + `\`, os.Getenv("SystemRoot")} {
			if _, err := os.Stat(filepath.Join(parent, ".padmate-write-test")); err == nil {
				t.Errorf("the page left a test folder in %s", parent)
			}
		}
	})
}

// unusedDrive returns the letter of a drive this machine does not have.
func unusedDrive(t *testing.T) string {
	t.Helper()
	for c := 'Z'; c >= 'D'; c-- {
		if _, err := os.Stat(string(c) + `:\`); err != nil {
			return string(c)
		}
	}
	t.Skip("every drive letter from D to Z is in use")
	return ""
}
