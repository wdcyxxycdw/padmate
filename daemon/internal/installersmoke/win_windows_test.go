package installersmoke

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The Win32 and registry helpers of the smoke test.

var programNames = []string{"padmate.exe", "padmated.exe", "padmatectl.exe"}

func isProgramName(name string) bool {
	for _, p := range programNames {
		if strings.EqualFold(name, p) {
			return true
		}
	}
	return false
}

// processImage returns the file a process runs.
func processImage(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// programsBelow lists the padmate programs whose file is inside dir.
func programsBelow(dir string) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	prefix := strings.ToLower(filepath.Clean(dir)) + `\`
	var pids []uint32
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if !isProgramName(windows.UTF16ToString(entry.ExeFile[:])) {
			continue
		}
		image, err := processImage(entry.ProcessID)
		if err == nil && strings.HasPrefix(strings.ToLower(image), prefix) {
			pids = append(pids, entry.ProcessID)
		}
	}
	return pids, nil
}

// moduleFile returns the file the module name was loaded from into a process.
func moduleFile(pid uint32, name string) (string, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	entry := windows.ModuleEntry32{Size: uint32(unsafe.Sizeof(windows.ModuleEntry32{}))}
	for err := windows.Module32First(snap, &entry); err == nil; err = windows.Module32Next(snap, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.Module[:]), name) {
			return windows.UTF16ToString(entry.ExePath[:]), true
		}
	}
	return "", false
}

// moduleFileWait is moduleFile, repeated until the module is loaded or the
// time is up.
func moduleFileWait(pid uint32, name string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if file, ok := moduleFile(pid, name); ok {
			return file, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// terminate ends the process pid. The caller makes sure the id is still the
// program's own: one that has exited may have been given to another program.
func terminate(pid uint32) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	_ = windows.TerminateProcess(h, 1)
	_, _ = windows.WaitForSingleObject(h, 5000)
}

// registryKeyExists reports whether a key of HKCU exists (64-bit view).
func registryKeyExists(path string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	_ = k.Close()
	return true
}

func registryString(path, name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(name)
	return v, err
}

func setRegistryString(path, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue(name, value)
}

// deleteRegistryTree removes a key and its subkeys; a missing key is no error.
func deleteRegistryTree(path string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return
	}
	subs, _ := k.ReadSubKeyNames(-1)
	_ = k.Close()
	for _, sub := range subs {
		deleteRegistryTree(path + `\` + sub)
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, path)
}

// splitCommand splits a Windows command line whose first word is a quoted
// path, as the uninstall strings are: `"C:\a b\Uninstall x.exe" /S`.
func splitCommand(line string) (exe string, err error) {
	if !strings.HasPrefix(line, `"`) {
		return "", fmt.Errorf("command %q does not start with a quoted path", line)
	}
	end := strings.Index(line[1:], `"`)
	if end < 0 {
		return "", fmt.Errorf("command %q has an unterminated quote", line)
	}
	return line[1 : 1+end], nil
}

// The windows of a running installer or uninstaller, found as the person would
// see them: the visible top-level dialogs of its process. A wizard is the main
// window, with the Back, Next and Cancel buttons (control ids 3, 1 and 2) and
// the page being shown in a dialog of its own; a message box has OK, or Yes
// and No.
var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetDlgItem          = user32.NewProc("GetDlgItem")
	procGetDlgCtrlID        = user32.NewProc("GetDlgCtrlID")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procFindWindowExW       = user32.NewProc("FindWindowExW")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procIsWindowEnabled     = user32.NewProc("IsWindowEnabled")
	pageClassName, _        = windows.UTF16PtrFromString("#32770")
	// Created once: callbacks of this kind are never freed. The test is
	// sequential, so one list serves both for windows and for their children.
	enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		enumerated = append(enumerated, windows.HWND(hwnd))
		return 1
	})
	enumerated []windows.HWND
)

const (
	idOK       = 1
	idCancel   = 2
	idBack     = 3
	idYes      = 6
	idNo       = 7
	bmClick    = 0x00F5
	bmGetCheck = 0x00F0
	dialogName = "#32770"

	// The folder field of NSIS's folder page.
	idFolderField = 1019

	wmGetText       = 0x000D
	wmGetTextLength = 0x000E
	wmSetText       = 0x000C
	smtoAbortIfHung = 0x0002
	bsAutoCheckBox  = 0x3
	bsTypeMask      = 0xF
	bstChecked      = 1
)

// gwlStyle is GWL_STYLE, -16.
var gwlStyle = ^uintptr(15)

type dialog struct {
	hwnd windows.HWND
	// The buttons by their control ids.
	buttons map[int]windows.HWND
}

func (d dialog) has(ids ...int) bool {
	for _, id := range ids {
		if d.buttons[id] == 0 {
			return false
		}
	}
	return true
}

func (d dialog) click(id int) error {
	b := d.buttons[id]
	if b == 0 {
		return fmt.Errorf("the dialog has no button %d", id)
	}
	return postClick(b)
}

// isMessageBox reports whether d is a message box that only wants to be
// dismissed: not a wizard, with OK (a box with OK alone gives its button the
// id of Cancel).
func (d dialog) isMessageBox() bool { return !d.isWizard() && (d.has(idOK) || d.has(idCancel)) }

// dismiss presses the OK of a message box.
func (d dialog) dismiss() error {
	if d.has(idOK) {
		return d.click(idOK)
	}
	return d.click(idCancel)
}

func postClick(button windows.HWND) error {
	r, _, err := procPostMessageW.Call(uintptr(button), bmClick, 0, 0)
	if r == 0 {
		return err
	}
	return nil
}

// isWizard reports whether d is the main window of a wizard rather than a
// message box.
func (d dialog) isWizard() bool { return d.has(idOK, idCancel, idBack) }

// enabled reports whether the button of the control id is there and not greyed.
func (d dialog) enabled(id int) bool {
	b := d.buttons[id]
	if b == 0 {
		return false
	}
	r, _, _ := procIsWindowEnabled.Call(uintptr(b))
	return r != 0
}

// page is the page a wizard shows now (the folder page, the progress, the
// finish page), 0 when d has none.
func (d dialog) page() windows.HWND {
	r, _, _ := procFindWindowExW.Call(uintptr(d.hwnd), 0, uintptr(unsafe.Pointer(pageClassName)), 0)
	return windows.HWND(r)
}

// folderField is the field of the folder page, 0 on any other page.
func (d dialog) folderField() windows.HWND {
	page := d.page()
	if page == 0 {
		return 0
	}
	r, _, _ := procGetDlgItem.Call(uintptr(page), idFolderField)
	return windows.HWND(r)
}

// checkBox is the first check box of the page (the finish page's "run
// padmate"), 0 when there is none.
func (d dialog) checkBox() windows.HWND {
	page := d.page()
	if page == 0 {
		return 0
	}
	for _, child := range childWindows(page) {
		// The pages before keep a hidden check box of their own.
		if className(child) != "Button" || !windows.IsWindowVisible(child) {
			continue
		}
		style, _, _ := procGetWindowLongPtrW.Call(uintptr(child), gwlStyle)
		if style&bsTypeMask == bsAutoCheckBox {
			return child
		}
	}
	return 0
}

// text is what a message box says: the text of its static controls.
func (d dialog) text() string {
	var parts []string
	for _, child := range childWindows(d.hwnd) {
		if className(child) == "Static" {
			if s := windowText(child); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func className(hwnd windows.HWND) string {
	var class [64]uint16
	if n, _ := windows.GetClassName(hwnd, &class[0], int32(len(class))); n == 0 {
		return ""
	}
	return windows.UTF16ToString(class[:])
}

// directChildren lists the windows that are children of hwnd itself.
func directChildren(hwnd windows.HWND) []windows.HWND {
	var children []windows.HWND
	var previous uintptr
	for {
		next, _, _ := procFindWindowExW.Call(uintptr(hwnd), previous, 0, 0)
		if next == 0 {
			return children
		}
		children = append(children, windows.HWND(next))
		previous = next
	}
}

// childWindows lists the windows below hwnd, children of children included.
func childWindows(hwnd windows.HWND) []windows.HWND {
	enumerated = nil
	windows.EnumChildWindows(hwnd, enumWindowsCallback, nil)
	return append([]windows.HWND(nil), enumerated...)
}

// sendMessage sends a message to a window of another process, giving up when
// that process hangs.
func sendMessage(hwnd windows.HWND, msg, wparam, lparam uintptr) (uintptr, bool) {
	var result uintptr
	r, _, _ := procSendMessageTimeoutW.Call(uintptr(hwnd), msg, wparam, lparam, smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&result)))
	return result, r != 0
}

// windowText reads the text of a control of another process (WM_GETTEXT:
// GetWindowText does not read the text of another process's edit control).
func windowText(hwnd windows.HWND) string {
	n, ok := sendMessage(hwnd, wmGetTextLength, 0, 0)
	if !ok || n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	if _, ok := sendMessage(hwnd, wmGetText, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0]))); !ok {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func setWindowText(hwnd windows.HWND, text string) error {
	p, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return err
	}
	if _, ok := sendMessage(hwnd, wmSetText, 0, uintptr(unsafe.Pointer(p))); !ok {
		return errors.New("the window did not take the text")
	}
	return nil
}

// isChecked reports whether a check box is ticked.
func isChecked(hwnd windows.HWND) bool {
	state, ok := sendMessage(hwnd, bmGetCheck, 0, 0)
	return ok && state == bstChecked
}

// dialogsWhere lists the visible top-level dialogs of the processes accept
// takes (their process id and the file they run).
func dialogsWhere(accept func(pid uint32, image string) bool) []dialog {
	enumerated = nil
	_ = windows.EnumWindows(enumWindowsCallback, nil)
	top := append([]windows.HWND(nil), enumerated...)
	var found []dialog
	for _, hwnd := range top {
		if className(hwnd) != dialogName || !windows.IsWindowVisible(hwnd) {
			continue
		}
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
			continue
		}
		image, err := processImage(pid)
		if err != nil || !accept(pid, image) {
			continue
		}
		d := dialog{hwnd: hwnd, buttons: map[int]windows.HWND{}}
		for _, child := range directChildren(hwnd) {
			if className(child) == "Button" {
				id, _, _ := procGetDlgCtrlID.Call(uintptr(child))
				d.buttons[int(id)] = child
			}
		}
		found = append(found, d)
	}
	return found
}

// uninstallerDialogs lists the visible dialogs of the uninstaller the test
// started (see isTestUninstaller).
func uninstallerDialogs(root string) []dialog {
	return dialogsWhere(func(_ uint32, image string) bool { return isTestUninstaller(root, image) })
}

// isTestUninstaller reports whether the program image is an uninstaller of
// this test: one of the NSIS kind, which copies itself to the temporary folder
// as Un_*.exe or runs as "Uninstall padmate.exe" in the installation folder,
// and that sits below root. The test gives its uninstaller a temporary folder
// of its own in root (uninstallInteractive), so an uninstaller of any other
// program, which may be on screen as well, is not taken for it and not clicked.
func isTestUninstaller(root, image string) bool {
	prefix := strings.ToLower(longPath(filepath.Clean(root))) + `\`
	image = strings.ToLower(longPath(image))
	base := filepath.Base(image)
	return strings.HasPrefix(image, prefix) && (strings.HasPrefix(base, "un_") || strings.HasPrefix(base, "uninstall "))
}

// longPath expands the short (8.3) names in a path of a file that exists, so
// that two spellings of one folder compare equal; any other path is returned
// as it is.
func longPath(path string) string {
	in, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return windows.UTF16ToString(buf[:n])
}

// installerDialogs lists the visible dialogs of the installer started as pid.
func installerDialogs(pid uint32) []dialog {
	return dialogsWhere(func(p uint32, _ string) bool { return p == pid })
}

// removeAllRetry removes a folder that a process released a moment ago.
func removeAllRetry(path string) error {
	var err error
	for range 20 {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.Join(err, fmt.Errorf("cannot remove %s", path))
}
