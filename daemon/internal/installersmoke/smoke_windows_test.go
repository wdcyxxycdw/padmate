package installersmoke

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
)

// The smoke test of the built installer (SPK-07). It installs into a folder
// the test makes, with spaces and Chinese in its name, and touches the user's
// real state only transiently and only where an installer must: the Settings >
// Apps entry and its install key under HKCU, and the Start Menu and desktop
// shortcuts, all removed again, even when the test fails. The login startup value, the
// profiles, scripts and discovery files it uses are the test override's
// (PADMATE_INSTALLER_TEST_ROOT, app/build/installer.nsh). Every padmated it
// starts is a --dry-run one with a private pipe and discovery folder. It never
// starts padmate.exe.
//
// One more file is outside the test root: the copy electron-builder's installer
// keeps of itself in the real %LOCALAPPDATA%\padmate-updater, which the
// installer deletes from v0.17.0-rc.3 on. The test may overwrite or delete what
// is there (a cache for a differential update padmate does not use), and
// deletes what is its own at the end. What the update downloads (pending\) is
// the test root's own: the full uninstall deletes that folder under the test
// override, not the real one.
//
// The update-style step runs the installer as --updated /S, as the automatic
// update does, over a running daemon (PKG-10). It never passes --force-run:
// that start is made by Explorer as the user, in the real environment, so the
// new padmate.exe would use the real data folder and send keys. runCommand
// refuses the flag. The relaunch after a real update is the owner's to check.
//
//	PADMATE_TEST_INSTALLER           the installer to test (absolute path)
//	PADMATE_TEST_INSTALLER_UPGRADE   optional: a second build, installed over the first
//	PADMATE_TEST_INSTALLER_INTERACTIVE=1  also answers the uninstaller's message
//	                                  boxes (they appear on screen for a moment)
//	PADMATE_INSTALLER_SIGNED_SUBJECT  optional: the publisher the installed files, the
//	                                  uninstaller included, must be signed by (PKG-09)
//	PADMATE_INSTALLER_SIGNED_ALLOW_UNTRUSTED_ROOT=1  accept a self-signed test certificate
//	PADMATE_INSTALLER_SIGNED_NO_TIMESTAMP=1          do not demand a timestamp
const (
	envInstaller   = "PADMATE_TEST_INSTALLER"
	envUpgrade     = "PADMATE_TEST_INSTALLER_UPGRADE"
	envInteractive = "PADMATE_TEST_INSTALLER_INTERACTIVE"

	envSignedSubject       = "PADMATE_INSTALLER_SIGNED_SUBJECT"
	envSignedUntrustedRoot = "PADMATE_INSTALLER_SIGNED_ALLOW_UNTRUSTED_ROOT"
	envSignedNoTimestamp   = "PADMATE_INSTALLER_SIGNED_NO_TIMESTAMP"
)

var installerFile = regexp.MustCompile(`^padmate-(v.+)-windows-amd64-setup\.exe$`)

// dropsCopySince is the first version whose installer deletes its copy of
// itself, and whose uninstaller deletes it too.
const dropsCopySince = "v0.17.0-rc.3"

func dropsCopy(t *testing.T, tag installerpkg.Tag) bool {
	t.Helper()
	since, err := installerpkg.ParseTag(dropsCopySince)
	if err != nil {
		t.Fatal(err)
	}
	return tag.Compare(since) >= 0
}

func tagOf(t *testing.T, file string) installerpkg.Tag {
	t.Helper()
	if !filepath.IsAbs(file) {
		t.Fatalf("%s must be an absolute path", file)
	}
	m := installerFile.FindStringSubmatch(filepath.Base(file))
	if m == nil {
		t.Fatalf("%s is not named like an installer", file)
	}
	tag, err := installerpkg.ParseTag(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return tag
}

type daemon struct {
	cmd   *exec.Cmd
	pid   uint32
	done  chan struct{}
	code  int
	label string
	log   string
}

type smoke struct {
	t                   *testing.T
	root, inst          string
	roaming, local, ipc string
	startMenuLink       string
	desktopLink         string
	// The installer's copy of itself, what was at its path before the test
	// (nil when nothing was), the installers the test ran by tag, and the file
	// the test puts at the path to see the uninstaller delete it.
	updaterCopy string
	copyBefore  *[sha256.Size]byte
	installers  map[string]string
	marker      string
	daemons     []*daemon
	env         []string
}

func newSmoke(t *testing.T) *smoke {
	t.Helper()
	root := filepath.Join(t.TempDir(), "安装 smoke 目录")
	s := &smoke{
		t: t, root: root, inst: filepath.Join(root, "Install 安装 dir"),
		roaming: filepath.Join(root, "AppData", "Roaming"),
		local:   filepath.Join(root, "AppData", "Local"),
	}
	s.ipc = filepath.Join(s.local, "padmate", "run")
	for _, dir := range []string{s.roaming, s.local, filepath.Join(root, "logs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PADMATE_") || strings.EqualFold(name, "ELECTRON_RUN_AS_NODE") {
			continue
		}
		s.env = append(s.env, kv)
	}
	s.startMenuLink = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "padmate.lnk")
	// $DESKTOP of the installer: the known folder, which may be moved (to
	// OneDrive, for one).
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		desktop = filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	}
	s.desktopLink = filepath.Join(desktop, "padmate.lnk")
	s.updaterCopy = filepath.Join(os.Getenv("LOCALAPPDATA"), "padmate-updater", "installer.exe")
	if sum, err := fileHash(s.updaterCopy); err == nil {
		s.copyBefore = &sum
	}
	s.installers = map[string]string{}
	s.marker = filepath.Join(root, "updater-marker.exe")
	return s
}

// ran records an installer the test runs. A program not named like one (the
// small wizard TestWizardPage compiles) makes no copy and is not recorded.
func (s *smoke) ran(installer string) {
	m := installerFile.FindStringSubmatch(filepath.Base(installer))
	if m == nil {
		return
	}
	tag, err := installerpkg.ParseTag(m[1])
	if err != nil {
		s.t.Fatal(err)
	}
	if s.installers == nil {
		s.installers = map[string]string{}
	}
	s.installers[tag.Label] = installer
}

// leftCopyOf reports whether the installer of tag left its copy of itself. The
// template writes the copy just before the delete (customInstall), over
// whatever was there, so a copy the delete missed holds that installer's
// bytes. A file at the path that equals another installer is not this one's:
// under the package virtualization of the Claude desktop app, deleting the
// test's copy uncovers the user's real one, which may be a copy of the same
// older installer the test ran first.
func (s *smoke) leftCopyOf(tag installerpkg.Tag) bool {
	installer, ok := s.installers[tag.Label]
	return ok && sameBytes(s.updaterCopy, installer)
}

// ownCopy reports whether the file at path is one the test made: equal to one
// of own (the test's installers and marker), and not what was at the path
// before the test (before, nil when nothing was), which may be the user's.
func ownCopy(path string, before *[sha256.Size]byte, own []string) bool {
	sum, err := fileHash(path)
	if err != nil || (before != nil && sum == *before) {
		return false
	}
	for _, o := range own {
		if sameBytes(path, o) {
			return true
		}
	}
	return false
}

// sameBytes reports whether the files a and b both exist and hold the same
// bytes. The test tells its own file at a path outside the test root by its
// content, not by its existence (see leftCopyOf).
func sameBytes(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil || ia.Size() != ib.Size() || !ia.Mode().IsRegular() || !ib.Mode().IsRegular() {
		return false
	}
	ha, errA := fileHash(a)
	hb, errB := fileHash(b)
	return errA == nil && errB == nil && ha == hb
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// removeOwnCopy deletes the installer's copy when the test made it (an older
// installer under test leaves it, or the marker is still there), and its
// folder when nothing else is in it.
func (s *smoke) removeOwnCopy() {
	own := []string{s.marker}
	for _, installer := range s.installers {
		own = append(own, installer)
	}
	if ownCopy(s.updaterCopy, s.copyBefore, own) {
		_ = os.Remove(s.updaterCopy)
		_ = os.Remove(filepath.Dir(s.updaterCopy))
	}
}

// requireNoRealInstall refuses to run beside an installed padmate: the
// installer would find it in the registry and upgrade or remove it.
func (s *smoke) requireNoRealInstall() {
	s.t.Helper()
	for _, key := range []string{installerpkg.UninstallKey, installerpkg.InstallKey} {
		if registryKeyExists(key) {
			s.t.Fatalf("HKCU\\%s exists: padmate is installed on this machine. The installer would upgrade or remove that installation; uninstall it first (this test is for a machine without one)", key)
		}
	}
	// The installer overwrites these shortcuts and the uninstaller deletes them.
	for _, link := range []string{s.startMenuLink, s.desktopLink} {
		if _, err := os.Lstat(link); err == nil {
			s.t.Fatalf("%s exists: the installer would overwrite it and the uninstaller delete it. Remove it first (this test is for a machine without padmate)", link)
		}
	}
}

func (s *smoke) cleanup() {
	for _, d := range s.daemons {
		// Only one that has not exited: its process id may be another
		// program's by now.
		if !d.exited() {
			terminate(d.pid)
		}
	}
	if pids, err := programsBelow(s.inst); err == nil {
		for _, pid := range pids {
			terminate(pid)
		}
	}
	if quiet, err := registryString(installerpkg.UninstallKey, "QuietUninstallString"); err == nil {
		// Best effort: the registry entry is ours, the test checked none existed.
		if exe, err := splitCommand(quiet); err == nil {
			_, _, _ = s.runCommand(exe, strings.TrimPrefix(quiet, `"`+exe+`" `), 2*time.Minute)
			s.waitGone(time.Minute)
		}
	}
	_ = removeAllRetry(s.inst)
	deleteRegistryTree(installerpkg.UninstallKey)
	deleteRegistryTree(installerpkg.InstallKey)
	deleteRegistryTree(`Software\padmate-installer-test`)
	// requireNoRealInstall made sure the shortcuts were not there before.
	_ = os.Remove(s.startMenuLink)
	_ = os.Remove(s.desktopLink)
	s.removeOwnCopy()
}

// forceRunFlag makes an installer start padmate.exe when it is done. The
// template does so through Explorer, as the user, so the new padmate.exe has
// none of the test's environment: it would use the real data folder and start a
// daemon that sends keys (ADR-0073). The smoke test never passes it.
const forceRunFlag = "--force-run"

// refuseForceRun is the error for an argument string that has forceRunFlag.
func refuseForceRun(args string) error {
	if strings.Contains(strings.ToLower(args), forceRunFlag) {
		return fmt.Errorf("refusing to run an installer with %s: it would start a real padmate.exe (the real data folder, a daemon that sends keys)", forceRunFlag)
	}
	return nil
}

// runCommand runs exe with an argument string, as the installer is given
// (/D=, which holds spaces, must come last and unquoted), under the test
// override, and returns the exit code.
func (s *smoke) runCommand(exe, args string, timeout time.Duration) (int, string, error) {
	if err := refuseForceRun(args); err != nil {
		return -1, "", err
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: `"` + exe + `" ` + args}
	cmd.Env = append(append([]string(nil), s.env...), installerpkg.TestRootEnv+"="+s.root)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return -1, "", err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err := <-finished:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), out.String(), nil
		}
		return 0, out.String(), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return -1, out.String(), fmt.Errorf("%s did not finish within %v", filepath.Base(exe), timeout)
	}
}

func (s *smoke) install(installer string) {
	s.t.Helper()
	s.ran(installer)
	start := time.Now()
	if code, out, err := s.runCommand(installer, `/S /D=`+s.inst, 5*time.Minute); err != nil || code != 0 {
		s.t.Fatalf("%s /S: exit code %d, %v\n%s", filepath.Base(installer), code, err, out)
	}
	s.t.Logf("installed %s in %v", filepath.Base(installer), time.Since(start).Round(100*time.Millisecond))
}

// uninstall runs the quiet uninstall command of the registry, as Settings
// does, and waits until the installation is gone.
func (s *smoke) uninstall() {
	s.t.Helper()
	quiet, err := registryString(installerpkg.UninstallKey, "QuietUninstallString")
	if err != nil {
		s.t.Fatalf("no QuietUninstallString: %v", err)
	}
	exe, err := splitCommand(quiet)
	if err != nil {
		s.t.Fatal(err)
	}
	if !strings.EqualFold(exe, filepath.Join(s.inst, "Uninstall padmate.exe")) {
		s.t.Fatalf("the uninstaller is %s, not in %s", exe, s.inst)
	}
	start := time.Now()
	if code, out, err := s.runCommand(exe, strings.TrimPrefix(quiet, `"`+exe+`" `), 2*time.Minute); err != nil || code != 0 {
		s.t.Fatalf("uninstaller: exit code %d, %v\n%s", code, err, out)
	}
	s.waitGoneOrFail(2 * time.Minute)
	s.t.Logf("uninstalled in %v", time.Since(start).Round(100*time.Millisecond))
}

// uninstallInteractive runs the uninstall command with a window and drives
// it as a person would: Next on the wizard's welcome page (an uninstaller of
// the one-click installer asks with a message box instead, answered with OK),
// the question about the profiles and scripts with answer (idYes keeps them,
// idNo deletes them), Finish on the last page.
func (s *smoke) uninstallInteractive(answer int) {
	s.t.Helper()
	command, err := registryString(installerpkg.UninstallKey, "UninstallString")
	if err != nil {
		s.t.Fatal(err)
	}
	exe, err := splitCommand(command)
	if err != nil {
		s.t.Fatal(err)
	}
	// The NSIS uninstaller copies itself to the temporary folder and runs from
	// there. This one is the test's own, so that its windows are told from the
	// windows of any other uninstaller (isTestUninstaller).
	temp := filepath.Join(s.root, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: command}
	cmd.Env = append(append([]string(nil), s.env...), installerpkg.TestRootEnv+"="+s.root, "TEMP="+temp, "TMP="+temp)
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	asked, confirmed := false, false
	deadline := time.Now().Add(2 * time.Minute)
	quiet := 0
	for time.Now().Before(deadline) {
		dialogs := uninstallerDialogs(s.root)
		// A message box is answered before the wizard behind it is touched:
		// the wizard's buttons take clicks while the box is up.
		boxed := false
		for _, d := range dialogs {
			switch {
			case d.has(idYes, idNo):
				asked, boxed = true, true
				_ = d.click(answer)
			case d.isMessageBox():
				confirmed, boxed = true, true
				_ = d.dismiss()
			}
		}
		if !boxed {
			for _, d := range dialogs {
				if d.isWizard() && d.enabled(idOK) {
					confirmed = true
					_ = d.click(idOK)
				}
			}
		}
		// Done when the installation is gone and the last window has closed.
		if _, err := os.Stat(s.inst); err != nil && !registryKeyExists(installerpkg.UninstallKey) && len(dialogs) == 0 {
			quiet++
		} else {
			quiet = 0
		}
		if quiet >= 5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.waitGoneOrFail(time.Minute)
	if !confirmed || !asked {
		s.t.Fatalf("the uninstaller showed its welcome page or confirmation: %v, its question about the profiles and scripts: %v", confirmed, asked)
	}
}

func (s *smoke) waitGone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(s.inst)
		if err != nil && !registryKeyExists(installerpkg.UninstallKey) && !registryKeyExists(installerpkg.InstallKey) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func (s *smoke) waitGoneOrFail(timeout time.Duration) {
	s.t.Helper()
	if !s.waitGone(timeout) {
		_, statErr := os.Stat(s.inst)
		s.t.Fatalf("after %v the installation is still there: folder %v, uninstall key %v, install key %v",
			timeout, statErr == nil, registryKeyExists(installerpkg.UninstallKey), registryKeyExists(installerpkg.InstallKey))
	}
}

func (s *smoke) bin(name string) string { return filepath.Join(s.inst, "resources", "bin", name) }

// childEnv is the environment of a padmated or padmatectl of this test: its
// own user folders, pipe and discovery folder.
func (s *smoke) childEnv(local, roaming, ipc, pipe string) []string {
	return append(append([]string(nil), s.env...),
		"LOCALAPPDATA="+local, "APPDATA="+roaming, "PADMATE_PIPE="+pipe, "PADMATE_IPC_DIR="+ipc)
}

func randomPipe() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return `\\.\pipe\padmate-installer-smoke-` + hex.EncodeToString(b[:])
}

// startDaemon starts a dry-run padmated without tray, with a data folder, user
// folders, pipe and discovery folder of its own.
func (s *smoke) startDaemon(exe, label, local, roaming, ipc string) *daemon {
	s.t.Helper()
	data := filepath.Join(s.root, "data-"+label)
	return s.launch(exe, label, s.childEnv(local, roaming, ipc, randomPipe()), "--dry-run", "--tray=false", "--data-dir", data,
		"--startup-report", filepath.Join(s.root, "logs", label+"-report.json"))
}

// launch starts a hidden program in its own folder, with its output in a log
// file, and watches for its exit.
func (s *smoke) launch(exe, label string, env []string, args ...string) *daemon {
	s.t.Helper()
	logFile := filepath.Join(s.root, "logs", label+".log")
	f, err := os.Create(logFile)
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		s.t.Fatalf("start %s: %v", exe, err)
	}
	d := &daemon{cmd: cmd, pid: uint32(cmd.Process.Pid), done: make(chan struct{}), label: label, log: logFile}
	go func() {
		_ = cmd.Wait()
		d.code = cmd.ProcessState.ExitCode()
		_ = f.Close()
		close(d.done)
	}()
	s.daemons = append(s.daemons, d)
	return d
}

func (d *daemon) exited() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

// waitReady waits until padmatectl status answers from the daemon's own
// discovery folder.
func (s *smoke) waitReady(d *daemon, ctl, local, roaming, ipc string) {
	s.t.Helper()
	for range 60 {
		if d.exited() {
			s.t.Fatalf("%s exited with code %d before it was ready:\n%s", d.label, d.code, tail(d.log))
		}
		cmd := exec.Command(ctl, "status", "--ipc-dir", ipc)
		cmd.Env = s.childEnv(local, roaming, ipc, "")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if out, err := cmd.CombinedOutput(); err == nil && len(out) > 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.t.Fatalf("%s was not ready within 30 s:\n%s", d.label, tail(d.log))
}

// ctl runs the installation's padmatectl against a daemon's discovery folder.
func (s *smoke) ctl(local, roaming, ipc string, args ...string) (string, error) {
	// The flags precede the positional arguments.
	cmd := exec.Command(s.bin("padmatectl.exe"), append([]string{args[0], "--ipc-dir", ipc}, args[1:]...)...)
	cmd.Env = s.childEnv(local, roaming, ipc, "")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// checkStartupOff asks a running daemon for its login startup entry: not
// available, as in a development build, and startup.set refuses.
func (s *smoke) checkStartupOff(local, roaming, ipc string) {
	s.t.Helper()
	out, err := s.ctl(local, roaming, ipc, "rpc", "startup.get", "{}")
	if err != nil || !strings.Contains(out, `"available": false`) || !strings.Contains(out, `"reason": "development"`) {
		s.t.Errorf("startup.get: %v\n%s", err, out)
	}
	if out, err := s.ctl(local, roaming, ipc, "rpc", "startup.set", `{"enabled":true}`); err == nil || !strings.Contains(out, "-32017") {
		s.t.Errorf("startup.set was not refused with -32017: %v\n%s", err, out)
	}
}

// checkFakeInput starts the daemon with the arguments the UI's test mode gives
// it (--dry-run --fake-input=ipc --data-dir) and looks for its one controller.
func (s *smoke) checkFakeInput() {
	s.t.Helper()
	dir := filepath.Join(s.root, "faked")
	local, roaming, ipc := filepath.Join(dir, "local"), filepath.Join(dir, "roaming"), filepath.Join(dir, "run")
	d := s.launch(s.bin("padmated.exe"), "faked", s.childEnv(local, roaming, ipc, randomPipe()),
		"--startup-report", filepath.Join(s.root, "logs", "faked-report.json"),
		"--dry-run", "--fake-input=ipc", "--tray=false", "--data-dir", filepath.Join(dir, "data"))
	s.waitReady(d, s.bin("padmatectl.exe"), local, roaming, ipc)
	if out, err := s.ctl(local, roaming, ipc, "devices"); err != nil || !strings.Contains(out, "padmate IPC fake gamepad") {
		s.t.Errorf("devices: %v\n%s", err, out)
	}
	s.checkStartupOff(local, roaming, ipc)
	if out, err := s.ctl(local, roaming, ipc, "quit"); err != nil {
		s.t.Errorf("padmatectl quit: %v\n%s", err, out)
	}
	select {
	case <-d.done:
	case <-time.After(5 * time.Second):
		s.t.Error("the fake-input daemon did not exit when asked")
	}
}

// realRunValue is the user's real login startup value of padmate, "" when
// there is none; it only reads.
func realRunValue() string {
	v, _ := registryString(installerpkg.RunKey, "padmate")
	return v
}

func tail(file string) string {
	data, _ := os.ReadFile(file)
	if len(data) > 2000 {
		data = data[len(data)-2000:]
	}
	return string(data)
}

func (s *smoke) write(path, content string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *smoke) exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// userFiles are the profiles, scripts and settings of the test's user, which
// a silent uninstall keeps, and the files a full uninstall removes.
type userFiles struct{ kept, removed []string }

func (s *smoke) seedUserData() userFiles {
	files := userFiles{
		kept: []string{
			filepath.Join(s.roaming, "padmate", "profiles", "paint", "profile.json"),
			filepath.Join(s.roaming, "padmate", "macros", "mine.ts"),
			filepath.Join(s.roaming, "padmate", "config.json"),
			filepath.Join(s.roaming, "padmate", "state.json"),
			// The window's own settings (language, label display, icon cache)
			// live in its browser data, so they are settings too.
			filepath.Join(s.roaming, "padmate", "ui", "Local Storage", "leveldb", "000003.log"),
		},
		removed: []string{
			filepath.Join(s.roaming, "padmate", "logs", "padmated.log"),
			filepath.Join(s.local, "padmate", "run", "held.bin"),
		},
	}
	for _, f := range append(append([]string(nil), files.kept...), files.removed...) {
		s.write(f, "test "+filepath.Base(f))
	}
	return files
}

func (s *smoke) requireFiles(files []string, want bool, what string) {
	s.t.Helper()
	for _, f := range files {
		if s.exists(f) != want {
			s.t.Errorf("%s: %s exists=%v, want %v", what, f, !want, want)
		}
	}
}

// checkInstalled is what an installation of tag has: every part with the
// tag's version, an uninstaller, the entry of Settings > Apps and a shortcut.
// earlier says that the installer is the older build of an upgrade test: it was
// not built from this source, so what its executables bind is not compared with
// the goldens of this source (installerpkg.TreeOptions.EarlierBuild).
func (s *smoke) checkInstalled(tag installerpkg.Tag, earlier bool) {
	s.t.Helper()
	s.checkInstalledAs(tag, earlier, false)
	s.checkSigned()
}

// checkInstalledAs is checkInstalled for an installation that may also be older
// than the automatic update (PKG-10): the first installer of an upgrade
// (older is true when it is named as the older of two) has no
// resources\app-update.yml, and the tree check does not ask for it
// (installerpkg.TreeOptions.NoUpdateConfig). The two options compose: an
// earlier build is not always an older version, and the other way round. It
// does not check the signatures (checkSigned): the first installer of an
// upgrade test is where the upgrade starts from, not the build under test, and
// is checked only when it is that build (isBuildUnderTest).
func (s *smoke) checkInstalledAs(tag installerpkg.Tag, earlier, older bool) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, problem := range installerpkg.CheckTree(ctx, installerpkg.TreeOptions{Root: s.inst, Tag: tag, Run: true, EarlierBuild: earlier, NoUpdateConfig: older}) {
		s.t.Errorf("installed tree: %s", problem)
	}
	if !s.exists(filepath.Join(s.inst, "Uninstall padmate.exe")) {
		s.t.Error("no Uninstall padmate.exe")
	}
	for name, want := range map[string]string{"DisplayVersion": tag.App, "DisplayName": "padmate"} {
		if got, err := registryString(installerpkg.UninstallKey, name); err != nil || got != want {
			s.t.Errorf("uninstall entry %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if got, err := registryString(installerpkg.InstallKey, "InstallLocation"); err != nil || !strings.EqualFold(got, s.inst) {
		s.t.Errorf("InstallLocation = %q, %v; want %q", got, err, s.inst)
	}
	if !s.exists(s.startMenuLink) {
		s.t.Errorf("no Start Menu shortcut %s", s.startMenuLink)
	}
	// createDesktopShortcut: always (ADR-0074).
	if !s.exists(s.desktopLink) {
		s.t.Errorf("no desktop shortcut %s", s.desktopLink)
	}
	if dropsCopy(s.t, tag) && s.leftCopyOf(tag) {
		s.t.Errorf("the installer left a copy of itself, %s", s.updaterCopy)
	}
}

// checkSigned checks the signatures of the installed files, the uninstaller
// included, when the environment names the publisher they must be signed by.
// electron-builder deletes the uninstaller from its output folder after it has
// signed it, so an installed tree is the one place it can be checked. SDL3.dll
// is not signed (installerpkg.CheckTree pins its hash).
func (s *smoke) checkSigned() {
	s.t.Helper()
	subject := os.Getenv(envSignedSubject)
	if subject == "" {
		return
	}
	opts := installerpkg.SignOptions{
		Subject:            subject,
		AllowUntrustedRoot: os.Getenv(envSignedUntrustedRoot) == "1",
		Timestamp:          os.Getenv(envSignedNoTimestamp) != "1",
	}
	for _, name := range append(append([]string(nil), installerpkg.SignedFiles...), installerpkg.UninstallerFile) {
		for _, problem := range installerpkg.CheckSignedFile(filepath.Join(s.inst, filepath.FromSlash(name)), opts) {
			s.t.Errorf("installed %s: %s", name, problem)
		}
	}
}

// checkRuns runs what an installation does without a controller or a person:
// the example profile with recorded input, as a dry run.
func (s *smoke) checkRuns() {
	s.t.Helper()
	res := filepath.Join(s.inst, "resources")
	cmd := exec.Command(s.bin("padmated.exe"), "--profile", filepath.Join(res, "examples", "profiles", "basic.json"),
		"--fake-input", filepath.Join(res, "examples", "traces", "basic.jsonl"), "--dry-run")
	cmd.Env = s.childEnv(s.local, s.roaming, s.ipc, randomPipe())
	cmd.Dir = s.inst
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "send") {
		s.t.Errorf("fake-input dry run: %v\n%s", err, out)
	}
	validate := exec.Command(s.bin("padmatectl.exe"), "validate", filepath.Join(res, "examples", "profiles", "basic.json"))
	validate.Env = s.env
	validate.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := validate.CombinedOutput(); err != nil {
		s.t.Errorf("padmatectl validate: %v\n%s", err, out)
	}
}

func TestInstallerSmoke(t *testing.T) {
	first := os.Getenv(envInstaller)
	if first == "" {
		t.Skip("set PADMATE_TEST_INSTALLER to the installer that task package:installer built")
	}
	second := os.Getenv(envUpgrade)
	tag1 := tagOf(t, first)
	// The same file is an upgrade as far as the installer is concerned: it
	// stops the daemon, runs the old uninstaller with --updated and installs.
	tag2 := tag1
	if second != "" {
		tag2 = tagOf(t, second)
	}
	s := newSmoke(t)
	s.requireNoRealInstall()
	t.Cleanup(s.cleanup)
	t.Logf("test root %s, install folder %s", s.root, s.inst)

	// Another padmated, outside the installation, that no installer step may
	// touch: a development build or a portable copy (PKG-04). It runs from a
	// copy of the first installation's daemon, so it exists once that is installed.
	var survivor *daemon

	t.Run("install", func(t *testing.T) {
		s.t = t
		// A folder of other files is refused, as the uninstaller removes the
		// whole installation folder: /D= must not name one.
		foreign := filepath.Join(s.inst, "mine", "notes.txt")
		s.write(foreign, "not padmate's")
		if code, out, err := s.runCommand(first, `/S /D=`+s.inst, 5*time.Minute); err != nil || code != 2 {
			t.Fatalf("an installer pointed at a folder of other files: exit code %d, %v\n%s", code, err, out)
		}
		if !s.exists(foreign) || registryKeyExists(installerpkg.UninstallKey) || s.exists(filepath.Join(s.inst, "padmate.exe")) {
			t.Fatal("the installer touched a folder of other files")
		}
		if err := removeAllRetry(s.inst); err != nil {
			t.Fatal(err)
		}

		s.install(first)
		// The first installer of an upgrade may be an earlier build and older than
		// the update.
		s.checkInstalledAs(tag1, second != "", tag1.Compare(tag2) < 0)
		if isBuildUnderTest(first, second) {
			s.checkSigned()
		}
		s.checkRuns()

		// SDL3.dll next to padmated.exe loads, from the installation folder: a
		// live dry run starts SDL's input subsystem with no controller attached.
		d := s.startDaemon(s.bin("padmated.exe"), "installed", s.local, s.roaming, s.ipc)
		s.waitReady(d, s.bin("padmatectl.exe"), s.local, s.roaming, s.ipc)
		got, ok := moduleFile(d.pid, "SDL3.dll")
		if want := s.bin("SDL3.dll"); !ok || !strings.EqualFold(got, want) {
			t.Errorf("padmated loaded SDL3.dll from %q (loaded: %v), want %q", got, ok, want)
		}

		// A release daemon is made private by its environment alone (the UI's
		// test mode relies on it): it is a development build to the login
		// startup entry, which stays off and unwritten.
		realBefore := realRunValue()
		s.checkStartupOff(s.local, s.roaming, s.ipc)
		s.checkFakeInput()
		if after := realRunValue(); after != realBefore {
			t.Errorf("the real login startup value changed from %q to %q", realBefore, after)
		}

		elsewhere := filepath.Join(s.root, "elsewhere")
		for _, name := range []string{"padmated.exe", "SDL3.dll"} {
			data, err := os.ReadFile(s.bin(name))
			if err != nil {
				t.Fatal(err)
			}
			s.write(filepath.Join(elsewhere, "bin", name), string(data))
		}
		survivor = s.startDaemon(filepath.Join(elsewhere, "bin", "padmated.exe"), "elsewhere",
			filepath.Join(elsewhere, "local"), filepath.Join(elsewhere, "roaming"), filepath.Join(elsewhere, "run"))
		s.waitReady(survivor, s.bin("padmatectl.exe"), filepath.Join(elsewhere, "local"), filepath.Join(elsewhere, "roaming"), filepath.Join(elsewhere, "run"))
	})
	if t.Failed() {
		t.FailNow()
	}

	if second != "" {
		t.Run("upgrade over a running daemon", func(t *testing.T) {
			s.t = t
			installed := s.daemons[0]
			files := s.seedUserData()
			s.write(filepath.Join(s.roaming, "padmate", "startup", "abc.json"), "{}")
			runValue := `"` + s.bin("padmated.exe") + `" --startup-id=abc`
			if err := setRegistryString(installerpkg.TestRunKey, "padmate", runValue); err != nil {
				t.Fatal(err)
			}
			if installed.exited() {
				t.Fatal("the installed daemon is not running")
			}

			s.install(second)

			select {
			case <-installed.done:
			case <-time.After(5 * time.Second):
				t.Fatal("the installer did not stop the running daemon")
			}
			// padmatectl quit has the daemon exit by itself; ended by force it
			// would have exit code 1 (TerminateProcess).
			if installed.code != 0 {
				t.Errorf("the daemon exited with code %d: it was ended, not asked to exit\n%s", installed.code, tail(installed.log))
			}
			if survivor.exited() {
				t.Error("the installer ended a padmated outside the installation folder")
			}
			s.checkInstalled(tag2, false)
			s.checkRuns()
			// An upgrade is not an uninstall: nothing of the user's goes, nor
			// the login startup entry or its argument files.
			s.requireFiles(files.kept, true, "after the upgrade")
			if !s.exists(filepath.Join(s.roaming, "padmate", "startup", "abc.json")) {
				t.Error("the upgrade removed the startup argument files")
			}
			if got, err := registryString(installerpkg.TestRunKey, "padmate"); err != nil || got != runValue {
				t.Errorf("the upgrade changed the startup entry: %q, %v", got, err)
			}
		})
		if t.Failed() {
			t.FailNow()
		}
	}

	// What the automatic update runs (PKG-10, ADR-0073): the downloaded
	// installer as --updated /S, silent, over the running daemon, without /D=
	// (the folder is the recorded one) and without --force-run (that start is
	// made by Explorer as the user, with the real data folder and --inject, and
	// is the owner's to check by hand; runCommand refuses the flag).
	t.Run("an update-style upgrade over a running daemon keeps the folder, the data and the startup entry and starts nothing", func(t *testing.T) {
		s.t = t
		update := chooseNewer(first, second)
		tag := tagOf(t, update)
		d := s.startDaemon(s.bin("padmated.exe"), "updating", s.local, s.roaming, s.ipc)
		s.waitReady(d, s.bin("padmatectl.exe"), s.local, s.roaming, s.ipc)
		files := s.seedUserData()
		s.write(filepath.Join(s.roaming, "padmate", "startup", "abc.json"), "{}")
		runValue := `"` + s.bin("padmated.exe") + `" --startup-id=abc`
		if err := setRegistryString(installerpkg.TestRunKey, "padmate", runValue); err != nil {
			t.Fatal(err)
		}
		// What the update downloaded is not the install's to delete: the
		// installer that runs is the file in pending\.
		pending := filepath.Join(s.local, "padmate-updater", "pending", "update-info.json")
		s.write(pending, "{}")
		survivorBefore := survivor != nil && survivor.exited()

		s.ran(update)
		start := time.Now()
		if code, out, err := s.runCommand(update, "--updated /S", 5*time.Minute); err != nil || code != 0 {
			t.Fatalf("%s --updated /S: exit code %d, %v\n%s", filepath.Base(update), code, err, out)
		}
		t.Logf("updated to %s in %v", tag.Label, time.Since(start).Round(100*time.Millisecond))

		select {
		case <-d.done:
		case <-time.After(5 * time.Second):
			t.Fatal("the installer did not stop the running daemon")
		}
		if d.code != 0 {
			t.Errorf("the daemon exited with code %d: it was ended, not asked to exit\n%s", d.code, tail(d.log))
		}
		if survivor != nil && !survivorBefore && survivor.exited() {
			t.Error("the installer ended a padmated outside the installation folder")
		}
		// The folder is the one recorded, as no /D= was given.
		if got, err := registryString(installerpkg.InstallKey, "InstallLocation"); err != nil || !strings.EqualFold(got, s.inst) {
			t.Errorf("InstallLocation = %q, %v; want %q", got, err, s.inst)
		}
		s.checkInstalled(tag, false)
		s.checkRuns()
		s.requireFiles(files.kept, true, "after the update")
		if !s.exists(filepath.Join(s.roaming, "padmate", "startup", "abc.json")) {
			t.Error("the update removed the startup argument files")
		}
		if got, err := registryString(installerpkg.TestRunKey, "padmate"); err != nil || got != runValue {
			t.Errorf("the update changed the startup entry: %q, %v", got, err)
		}
		if !s.exists(pending) {
			t.Error("the update deleted what the update downloaded")
		}
		// Without --force-run nothing starts padmate.exe. The template would
		// do it a moment after the files are written, so look for a while.
		for range 10 {
			pids, err := programsBelow(s.inst)
			if err != nil {
				t.Fatal(err)
			}
			for _, pid := range pids {
				if image, err := processImage(pid); err == nil && strings.EqualFold(filepath.Base(image), "padmate.exe") {
					t.Errorf("padmate.exe (pid %d) was started after an update without %s", pid, forceRunFlag)
					terminate(pid)
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		if err := os.RemoveAll(filepath.Join(s.local, "padmate-updater")); err != nil {
			t.Fatal(err)
		}
		deleteRegistryTree(`Software\padmate-installer-test`)
		if err := removeAllRetry(filepath.Join(s.roaming, "padmate")); err != nil {
			t.Fatal(err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	t.Run("install leaves a padmated of another folder that owns the discovery file", func(t *testing.T) {
		s.t = t
		// The installer asks the padmated that daemon.json names to exit, with
		// padmatectl quit --under the installation folder. A development build
		// or a portable copy can own that file, and must go on.
		elsewhere := filepath.Join(s.root, "elsewhere")
		if installed := s.daemons[0]; !installed.exited() {
			if out, err := s.ctl(s.local, s.roaming, s.ipc, "quit"); err != nil {
				t.Fatalf("padmatectl quit: %v\n%s", err, out)
			}
			select {
			case <-installed.done:
			case <-time.After(10 * time.Second):
				t.Fatal("the installed daemon did not exit when asked")
			}
		}
		owner := s.startDaemon(filepath.Join(elsewhere, "bin", "padmated.exe"), "owner", s.local, s.roaming, s.ipc)
		s.waitReady(owner, s.bin("padmatectl.exe"), s.local, s.roaming, s.ipc)

		s.install(chooseNewer(first, second))

		if owner.exited() {
			t.Fatalf("the installer stopped a padmated of another folder that owns the discovery file (exit code %d)\n%s", owner.code, tail(owner.log))
		}
		// Asked the same way, with the installation's own folder.
		out, err := s.ctl(s.local, s.roaming, s.ipc, "quit", "--under", s.inst)
		if err != nil || !strings.Contains(out, "left alone") || owner.exited() {
			t.Errorf("padmatectl quit --under the installation: %v\n%s", err, out)
		}
		// And with its own folder, the padmated is asked: this is how it ends.
		if out, err := s.ctl(s.local, s.roaming, s.ipc, "quit", "--under", elsewhere); err != nil {
			t.Errorf("padmatectl quit --under its folder: %v\n%s", err, out)
		}
		select {
		case <-owner.done:
		case <-time.After(10 * time.Second):
			t.Error("the padmated was not asked to exit when its folder was named")
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	t.Run("uninstall removes the startup entry of this installation", func(t *testing.T) {
		s.t = t
		files := s.seedUserData()
		s.write(filepath.Join(s.roaming, "padmate", "startup", "abc.json"), "{}")
		if err := setRegistryString(installerpkg.TestRunKey, "padmate", `"`+s.bin("padmated.exe")+`" --startup-id=abc`); err != nil {
			t.Fatal(err)
		}
		// A daemon is running: the uninstaller must stop it first. After an
		// upgrade the daemon of the first run is gone, and one starts anew.
		d := s.daemons[0]
		if d.exited() {
			d = s.startDaemon(s.bin("padmated.exe"), "uninstall", s.local, s.roaming, s.ipc)
			s.waitReady(d, s.bin("padmatectl.exe"), s.local, s.roaming, s.ipc)
		}
		// A program of the installation that does not exit when asked: a
		// standalone monitor (it reads SDL until Ctrl+C) that no padmatectl quit
		// reaches. After ten seconds the uninstaller ends it.
		stuck := s.launch(s.bin("padmatectl.exe"), "stuck", s.env, "monitor", "--direct")
		if _, ok := moduleFileWait(stuck.pid, "SDL3.dll", 10*time.Second); !ok {
			t.Fatalf("the stand-in for a program that does not exit did not start:\n%s", tail(stuck.log))
		}
		// A copy the delete after the install did not get: the uninstaller
		// deletes it.
		copyDropped := dropsCopy(t, tag2)
		if copyDropped {
			const stand = "padmate installer smoke: the installer's copy of itself"
			s.write(s.marker, stand)
			s.write(s.updaterCopy, stand)
		}

		// And an update that was downloaded and never installed (PKG-10): the
		// full uninstall deletes the folder with it. Under the test override
		// that folder is the test root's own, so no real update is at risk.
		updateCache := filepath.Join(s.local, "padmate-updater")
		s.write(filepath.Join(updateCache, "pending", "padmate-v9.9.9-windows-amd64-setup.exe"), "an update that was never installed")
		s.write(filepath.Join(updateCache, "pending", "update-info.json"), "{}")

		s.uninstall()

		if copyDropped && sameBytes(s.updaterCopy, s.marker) {
			t.Errorf("the uninstaller left the installer's copy of itself, %s", s.updaterCopy)
		}
		if s.exists(updateCache) {
			t.Errorf("the uninstaller left what the update downloaded, %s", updateCache)
		}

		select {
		case <-stuck.done:
			if stuck.code != 1 {
				t.Errorf("the program that does not exit has exit code %d, want 1 (ended by the uninstaller)", stuck.code)
			}
		case <-time.After(5 * time.Second):
			t.Error("the uninstaller left a program that does not exit running")
		}

		select {
		case <-d.done:
		case <-time.After(5 * time.Second):
			t.Error("the uninstaller did not stop the running daemon")
		}
		if survivor != nil && survivor.exited() {
			t.Error("the uninstaller ended a padmated outside the installation folder")
		}
		if _, err := registryString(installerpkg.TestRunKey, "padmate"); err == nil {
			t.Error("the startup entry of the removed installation is still there")
		}
		if registryKeyExists(`Software\padmate-installer-test`) {
			t.Error("an empty test key was left behind")
		}
		// Silent: the profiles, scripts and settings stay; what only the
		// installation needed goes.
		s.requireFiles(files.kept, true, "silent uninstall keeps the user's files")
		s.requireFiles(files.removed, false, "uninstall cleanup")
		for _, gone := range []string{
			filepath.Join(s.roaming, "padmate", "startup"), filepath.Join(s.local, "padmate", "run"),
			filepath.Join(s.roaming, "padmate", "logs"),
		} {
			if s.exists(gone) {
				t.Errorf("%s was not removed", gone)
			}
		}
		if s.exists(s.startMenuLink) {
			t.Error("the Start Menu shortcut was not removed")
		}
		if s.exists(s.desktopLink) {
			t.Error("the desktop shortcut was not removed")
		}
		if left, _ := programsBelow(s.inst); len(left) > 0 {
			t.Errorf("programs of the removed installation still run: %v", left)
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	t.Run("uninstall keeps a startup entry of another padmate", func(t *testing.T) {
		s.t = t
		s.install(chooseNewer(first, second))
		other := `"` + filepath.Join(s.root, "elsewhere", "bin", "padmated.exe") + `" --startup-id=zzz`
		if err := setRegistryString(installerpkg.TestRunKey, "padmate", other); err != nil {
			t.Fatal(err)
		}
		s.write(filepath.Join(s.roaming, "padmate", "startup", "zzz.json"), "{}")
		s.write(filepath.Join(s.local, "padmate", "run", "daemon.json"), "{}")

		s.uninstall()

		if got, err := registryString(installerpkg.TestRunKey, "padmate"); err != nil || got != other {
			t.Errorf("the startup entry of another padmate changed: %q, %v", got, err)
		}
		if !s.exists(filepath.Join(s.roaming, "padmate", "startup", "zzz.json")) {
			t.Error("the argument file that entry needs was removed")
		}
		if s.exists(filepath.Join(s.local, "padmate", "run")) {
			t.Error("the run folder was not removed")
		}
		deleteRegistryTree(`Software\padmate-installer-test`)
		if err := removeAllRetry(filepath.Join(s.roaming, "padmate")); err != nil {
			t.Fatal(err)
		}
		if survivor != nil && survivor.exited() {
			t.Error("the uninstaller ended a padmated outside the installation folder")
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	if os.Getenv(envInteractive) == "1" {
		for _, c := range []struct {
			name    string
			answer  int
			keepsIt bool
		}{{"answering No deletes the profiles and scripts", idNo, false}, {"answering Yes keeps the profiles and scripts", idYes, true}} {
			t.Run("interactive uninstall "+c.name, func(t *testing.T) {
				s.t = t
				s.install(chooseNewer(first, second))
				files := s.seedUserData()
				s.uninstallInteractive(c.answer)
				s.requireFiles(files.kept, c.keepsIt, "after answering")
				if got := s.exists(filepath.Join(s.roaming, "padmate")); got != c.keepsIt {
					t.Errorf("%s exists=%v, want %v", filepath.Join(s.roaming, "padmate"), got, c.keepsIt)
				}
				s.requireFiles(files.removed, false, "uninstall cleanup")
				if err := removeAllRetry(filepath.Join(s.roaming, "padmate")); err != nil {
					t.Fatal(err)
				}
			})
		}
	} else {
		t.Logf("set %s=1 to also answer the uninstaller's message boxes", envInteractive)
	}
	fmt.Fprintln(os.Stderr, "installer smoke: install, upgrade and uninstall paths checked")
}

// chooseNewer is the installer the later steps install: the upgrade's, when
// there is one.
func chooseNewer(first, second string) string {
	if second != "" {
		return second
	}
	return first
}
