package installersmoke

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
)

// The wizard of the installer (PKG-13, ADR-0068), driven through its real
// windows as a person would: the folder page, the progress and the finish page.
// The windows appear on screen for a moment, so this runs only with
// PADMATE_TEST_INSTALLER_INTERACTIVE=1, like the uninstaller's questions. It
// touches the same state as TestInstallerSmoke and cleans up the same way, and
// every installation it makes is in a folder of the test's own. "Run padmate"
// on the finish page is unticked each time: padmate.exe never starts.

// wizardRun is an installer started without /S.
type wizardRun struct {
	s    *smoke
	pid  uint32
	done chan struct{}
	code int
}

// startWizard starts the installer with its window, under the test override.
func (s *smoke) startWizard(installer string) *wizardRun {
	s.t.Helper()
	s.ran(installer)
	cmd := exec.Command(installer)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + installer + `"`}
	cmd.Env = append(append([]string(nil), s.env...), installerpkg.TestRootEnv+"="+s.root)
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	w := &wizardRun{s: s, pid: uint32(cmd.Process.Pid), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		w.code = cmd.ProcessState.ExitCode()
		close(w.done)
	}()
	// Not by the process id alone: once the installer has exited, Windows may
	// give its id to another program of the user, which must not be ended.
	// While the installer has not been waited for, its id is still its own.
	s.t.Cleanup(func() {
		if !w.exited() {
			terminate(w.pid)
		}
	})
	return w
}

func (w *wizardRun) exited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// wizard is the installer's main window.
func (w *wizardRun) wizard() (dialog, bool) {
	for _, d := range installerDialogs(w.pid) {
		if d.isWizard() {
			return d, true
		}
	}
	return dialog{}, false
}

// box is a message box of the installer.
func (w *wizardRun) box() (dialog, bool) {
	for _, d := range installerDialogs(w.pid) {
		if d.isMessageBox() {
			return d, true
		}
	}
	return dialog{}, false
}

// wait polls until f is true. It fails when the time is up, or when the
// installer has exited and f is still not true.
func (w *wizardRun) wait(what string, timeout time.Duration, f func() bool) {
	w.s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		if w.exited() {
			if f() {
				return
			}
			w.s.t.Fatalf("the installer exited with code %d before %s", w.code, what)
		}
		time.Sleep(200 * time.Millisecond)
	}
	w.s.t.Fatalf("after %v there was no %s%s", timeout, what, w.describe())
}

// describe says what the installer shows, for a failure message.
func (w *wizardRun) describe() string {
	var b strings.Builder
	for _, d := range installerDialogs(w.pid) {
		fmt.Fprintf(&b, "\n  window %x: wizard %v, folder field %q, check box %q, text %q",
			d.hwnd, d.isWizard(), windowText(d.folderField()), windowText(d.checkBox()), d.text())
	}
	return b.String()
}

// folderPage waits for the folder page, the first page, and returns the
// wizard window and the field of the folder.
func (w *wizardRun) folderPage() (dialog, windows.HWND) {
	w.s.t.Helper()
	var d dialog
	w.wait("folder page", time.Minute, func() bool {
		var ok bool
		d, ok = w.wizard()
		return ok && d.folderField() != 0
	})
	return d, d.folderField()
}

// finishPage waits for the finish page, which has the "run padmate" box, and
// returns the wizard window.
func (w *wizardRun) finishPage(timeout time.Duration) dialog {
	w.s.t.Helper()
	var d dialog
	w.wait("finish page", timeout, func() bool {
		var ok bool
		d, ok = w.wizard()
		return ok && d.checkBox() != 0
	})
	return d
}

// finish leaves the finish page with "run padmate" unticked and waits for the
// installer to end.
func (w *wizardRun) finish(d dialog) {
	w.s.t.Helper()
	box := d.checkBox()
	if !isChecked(box) {
		w.s.t.Error("the finish page does not tick \"run padmate\" by default")
	}
	if text := windowText(box); !strings.Contains(text, installerpkg.ProductName) {
		w.s.t.Errorf("the finish page's box says %q, which does not name padmate", text)
	}
	_ = postClick(box)
	w.wait("\"run padmate\" unticked", 10*time.Second, func() bool { return !isChecked(box) })
	if err := d.click(idOK); err != nil {
		w.s.t.Fatal(err)
	}
	select {
	case <-w.done:
	case <-time.After(30 * time.Second):
		w.s.t.Fatal("the installer did not close after Finish")
	}
	if w.code != 0 {
		w.s.t.Errorf("the installer exited with code %d after Finish", w.code)
	}
	// Unticked, the app is not started.
	time.Sleep(time.Second)
	if running, _ := programsBelow(w.s.inst); len(running) > 0 {
		w.s.t.Errorf("programs of the installation run although \"run padmate\" was unticked: %v", running)
	}
}

// chooseFolder types a folder in the folder page and presses Next once. The
// page stays, now showing the folder padmate goes in (WizardDir says which).
// It returns that folder.
func (w *wizardRun) chooseFolder(d dialog, field windows.HWND, typed string) string {
	w.s.t.Helper()
	offered := windowText(field)
	want, added := installerpkg.WizardDir(offered, typed)
	existed := w.s.exists(want)
	if err := setWindowText(field, typed); err != nil {
		w.s.t.Fatal(err)
	}
	if err := d.click(idOK); err != nil {
		w.s.t.Fatal(err)
	}
	if !added {
		return want
	}
	w.wait("folder page showing "+want, 30*time.Second, func() bool { return windowText(field) == want })
	if now, ok := w.wizard(); !ok || now.folderField() == 0 {
		w.s.t.Errorf("Next went on although it added the name of the folder, which the person should see first%s", w.describe())
	}
	// Nothing is made of the folder (the test of writing is in the folder
	// above it) while the person may still cancel.
	if !existed && w.s.exists(want) {
		w.s.t.Errorf("%s exists before the second Next", want)
	}
	if err := d.click(idOK); err != nil {
		w.s.t.Fatal(err)
	}
	return want
}

// The folders above which TestInstallerWizard installs, in the test's root: the
// parents the wizard is given.
const (
	parentInstall = "向导 parent 目录"
	parentGuard   = "守卫 parent"
	parentMove    = "新位置 parent"
)

// wizardSince is the first release whose installer is a wizard. The one before
// it, v0.17.0-rc.1, has no windows to drive: started without /S it installs at
// once, into the folder of the real application, writes the real uninstall
// entry and starts the real padmate.exe.
const wizardSince = "v0.17.0-rc.2"

// wizardInstaller picks the installer whose wizard the test drives: the later
// release of the two given, whatever order they were named in. A release before
// wizardSince skips the test, so that an installer of that kind is never
// started with its window.
func wizardInstaller(t *testing.T, first, second string) string {
	t.Helper()
	installer := first
	if second != "" && tagOf(t, second).Compare(tagOf(t, first)) > 0 {
		installer = second
	}
	since, err := installerpkg.ParseTag(wizardSince)
	if err != nil {
		t.Fatal(err)
	}
	if tag := tagOf(t, installer); tag.Compare(since) < 0 {
		t.Skipf("%s is older than %s, whose installer is the first wizard: name the wizard's installer in %s or %s", tag.Label, wizardSince, envInstaller, envUpgrade)
	}
	return installer
}

func TestInstallerWizard(t *testing.T) {
	first := os.Getenv(envInstaller)
	if first == "" {
		t.Skip("set PADMATE_TEST_INSTALLER to the installer that task package:installer built")
	}
	if os.Getenv(envInteractive) != "1" {
		t.Skip("set " + envInteractive + "=1 to drive the wizard through its windows (they appear on screen for a moment)")
	}
	second := os.Getenv(envUpgrade)
	tag1 := tagOf(t, first)
	installer := wizardInstaller(t, first, second)
	tag := tagOf(t, installer)
	s := newSmoke(t)
	s.requireNoRealInstall()
	t.Cleanup(s.cleanup)
	home := s.inst
	t.Logf("test root %s", s.root)
	// The wizard refuses a folder too long for the longest installed file
	// (installerpkg.FitsPath). The folders below are the test's own, below its
	// temporary folder: say so up front when that one lies too deep.
	for _, name := range []string{parentInstall, parentGuard, parentMove} {
		if dir := filepath.Join(s.root, name, installerpkg.FolderName); !installerpkg.FitsPath(dir) {
			t.Fatalf("the test's folder %s is too long for the wizard, which allows %d characters: set TMP to a shorter folder", dir,
				installerpkg.MaxPath-installerpkg.MaxRelativePath)
		}
	}

	t.Run("Next puts padmate in a folder named padmate inside the chosen one", func(t *testing.T) {
		s.t = t
		defer s.cleanup()
		// A parent with spaces and Chinese in its name.
		parent := filepath.Join(s.root, parentInstall)
		s.inst = filepath.Join(parent, installerpkg.FolderName)

		w := s.startWizard(installer)
		d, field := w.folderPage()
		offered := windowText(field)
		if !strings.HasSuffix(strings.ToLower(offered), `\`+installerpkg.FolderName) {
			t.Errorf("the folder page offers %q, which is not a folder named %s", offered, installerpkg.FolderName)
		}
		// The page before it, "all users or only me", is skipped: the folder
		// page is the first, so there is nothing to go back to.
		if d.enabled(idBack) {
			t.Error("the folder page lets the person go back")
		}

		got := w.chooseFolder(d, field, parent)
		if got != s.inst {
			t.Fatalf("the wizard chose %q, want %q", got, s.inst)
		}
		w.finish(w.finishPage(5 * time.Minute))
		s.checkInstalled(tag, false)
		s.checkRuns()
		s.uninstall()
		_ = removeAllRetry(parent)
	})
	if t.Failed() {
		t.FailNow()
	}

	t.Run("the folder page refuses a folder of other files and cancel leaves it alone", func(t *testing.T) {
		s.t = t
		defer s.cleanup()
		parent := filepath.Join(s.root, parentGuard)
		s.inst = filepath.Join(parent, installerpkg.FolderName)
		foreign := filepath.Join(s.inst, "mine", "notes.txt")
		s.write(foreign, "not padmate's")

		w := s.startWizard(installer)
		d, field := w.folderPage()
		// The folder itself: nothing is added, so Next goes straight to the check.
		if err := setWindowText(field, s.inst); err != nil {
			t.Fatal(err)
		}
		if err := d.click(idOK); err != nil {
			t.Fatal(err)
		}
		w.wait("message about the folder", 30*time.Second, func() bool { _, ok := w.box(); return ok })
		box, _ := w.box()
		say := box.text()
		if !strings.Contains(say, s.inst) || (!strings.Contains(say, "已有其他文件") && !strings.Contains(say, "already holds other files")) {
			t.Errorf("the message is %q: it should name the folder and say it holds other files", say)
		}
		if err := box.dismiss(); err != nil {
			t.Fatal(err)
		}
		w.wait("message to go", 10*time.Second, func() bool { _, ok := w.box(); return !ok })
		if now, ok := w.wizard(); !ok || now.folderField() == 0 {
			t.Errorf("the wizard left the folder page after the message%s", w.describe())
		}
		if w.exited() {
			t.Fatalf("the installer exited with code %d after the message", w.code)
		}

		// Cancel. A question about quitting, if there is one, is answered yes.
		if err := d.click(idCancel); err != nil {
			t.Fatal(err)
		}
		w.wait("installer to end", time.Minute, func() bool {
			for _, b := range installerDialogs(w.pid) {
				switch {
				case b.has(idYes, idNo):
					_ = b.click(idYes)
				case b.isMessageBox():
					_ = b.dismiss()
				}
			}
			return w.exited()
		})
		if !s.exists(foreign) {
			t.Error("the folder of other files lost its file")
		}
		if names := dirNames(t, s.inst); len(names) != 1 || names[0] != "mine" {
			t.Errorf("the folder holds %v after Cancel, want only mine", names)
		}
		if names := dirNames(t, parent); len(names) != 1 || names[0] != installerpkg.FolderName {
			t.Errorf("%s holds %v after Cancel, want only %s", parent, names, installerpkg.FolderName)
		}
		for _, key := range []string{installerpkg.UninstallKey, installerpkg.InstallKey} {
			if registryKeyExists(key) {
				t.Errorf("HKCU\\%s exists after Cancel", key)
			}
		}
		if s.exists(s.startMenuLink) {
			t.Error("a Start Menu shortcut was made")
		}
		if s.exists(s.desktopLink) {
			t.Error("a desktop shortcut was made")
		}
		_ = removeAllRetry(parent)
	})
	if t.Failed() {
		t.FailNow()
	}

	t.Run("the wizard moves an installation to another folder", func(t *testing.T) {
		s.t = t
		defer s.cleanup()
		// The old installation, made quietly, with a running daemon, a user's
		// files and a login startup value that points into it.
		s.inst = home
		s.install(first)
		// The first installer may be an earlier build and older than the automatic
		// update.
		s.checkInstalledAs(tag1, second != "", tag1.Compare(tag) < 0)
		if isBuildUnderTest(first, installer) {
			s.checkSigned()
		}
		oldDaemon := s.startDaemon(s.bin("padmated.exe"), "moved", s.local, s.roaming, s.ipc)
		s.waitReady(oldDaemon, s.bin("padmatectl.exe"), s.local, s.roaming, s.ipc)
		files := s.seedUserData()
		runValue := `"` + s.bin("padmated.exe") + `" --startup-id=abc`
		if err := setRegistryString(installerpkg.TestRunKey, "padmate", runValue); err != nil {
			t.Fatal(err)
		}

		parent := filepath.Join(s.root, parentMove)
		s.inst = filepath.Join(parent, installerpkg.FolderName)
		w := s.startWizard(installer)
		d, field := w.folderPage()
		if offered := windowText(field); !strings.EqualFold(offered, home) {
			t.Errorf("the folder page offers %q, want the folder of the installation, %q", offered, home)
		}
		w.chooseFolder(d, field, parent)
		w.finish(w.finishPage(5 * time.Minute))

		// The old daemon was asked to exit, not ended, before anything was replaced.
		select {
		case <-oldDaemon.done:
		case <-time.After(5 * time.Second):
			t.Fatal("the installer did not stop the daemon of the old folder")
		}
		if oldDaemon.code != 0 {
			t.Errorf("the daemon of the old folder exited with code %d: it was ended, not asked to exit\n%s", oldDaemon.code, tail(oldDaemon.log))
		}
		if s.exists(home) {
			t.Errorf("the old folder %s is still there", home)
		}
		s.checkInstalled(tag, false)
		s.requireFiles(files.kept, true, "after moving")
		// The login startup value is not rewritten (ADR-0060): it still names the old folder.
		if got, err := registryString(installerpkg.TestRunKey, "padmate"); err != nil || got != runValue {
			t.Errorf("the startup value is %q, %v; want it unchanged, %q", got, err, runValue)
		}
		s.uninstall()
		_ = removeAllRetry(parent)
		_ = removeAllRetry(filepath.Join(s.roaming, "padmate"))
	})
	fmt.Fprintln(os.Stderr, "installer wizard: folder page, finish page, guard and move checked")
}

// dirNames lists the names in a folder.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The installer whose wizard TestInstallerWizard drives is the later release
// of the two it is given, in either order, and never one from before the
// wizard: that one would install for real without a question.
func TestWizardInstallerChoice(t *testing.T) {
	dir := t.TempDir()
	file := func(tag string) string {
		if tag == "" {
			return ""
		}
		return filepath.Join(dir, "padmate-"+tag+"-windows-amd64-setup.exe")
	}
	for _, c := range []struct{ name, first, second, want string }{
		{"one wizard", "v0.17.0-rc.2", "", "v0.17.0-rc.2"},
		{"the same one twice", "v0.17.0-rc.2", "v0.17.0-rc.2", "v0.17.0-rc.2"},
		{"the old one first, the wizard as the upgrade", "v0.17.0-rc.1", "v0.17.0-rc.2", "v0.17.0-rc.2"},
		{"the wizard first, the old one named as the upgrade", "v0.17.0-rc.2", "v0.17.0-rc.1", "v0.17.0-rc.2"},
		{"a later release", "v0.17.0-rc.2", "v0.17.0", "v0.17.0"},
		{"a release candidate numbered ten", "v0.17.0-rc.2", "v0.17.0-rc.10", "v0.17.0-rc.10"},
		{"only the old one", "v0.17.0-rc.1", "", ""},
		{"the old one twice", "v0.17.0-rc.1", "v0.17.0-rc.1", ""},
		{"an older release", "v0.16.0", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got string
			// A skip ends the subtest of its own, as the test does.
			var inner *testing.T
			t.Run("choose", func(t *testing.T) {
				inner = t
				got = wizardInstaller(t, file(c.first), file(c.second))
			})
			if c.want == "" {
				if !inner.Skipped() {
					t.Errorf("%q and %q: the installer %q was chosen, which is not a wizard", c.first, c.second, got)
				}
				return
			}
			if inner.Skipped() || got != file(c.want) {
				t.Errorf("%q and %q: chose %q (skipped %v), want %q", c.first, c.second, got, inner.Skipped(), file(c.want))
			}
		})
	}
}

// The test clicks the windows of its own uninstaller and of no other.
func TestIsTestUninstaller(t *testing.T) {
	root := `C:\Users\me\AppData\Local\Temp\TestX\001\安装 smoke 目录`
	for _, c := range []struct {
		name, image string
		want        bool
	}{
		{"the copy in the test's temporary folder", root + `\temp\~nsuA.tmp\Un_A.exe`, true},
		{"the uninstaller in the installation", root + `\Install dir\Uninstall padmate.exe`, true},
		{"in capitals", strings.ToUpper(root) + `\TEMP\~NSUA.TMP\UN_A.EXE`, true},
		{"the copy of another program's uninstaller", `C:\Users\me\AppData\Local\Temp\~nsuB.tmp\Un_A.exe`, false},
		{"another program's uninstaller", `C:\Program Files\Other\Uninstall Other.exe`, false},
		{"a folder that starts like the root", root + `-other\temp\Un_A.exe`, false},
		{"some other program in the root", root + `\padmated.exe`, false},
	} {
		if got := isTestUninstaller(root, c.image); got != c.want {
			t.Errorf("%s: isTestUninstaller(%q) = %v, want %v", c.name, c.image, got, c.want)
		}
	}
}
