package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Notify and ShowMenu use private parts of the pinned fyne.io/systray: the
// class of its hidden window, the icon's callback message and what it does
// with each button. An upgrade that changes them must fail here, not leave
// the tray without its menu (a left click shows it only through ShowMenu).
func TestSystrayPrivateABI(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "fyne.io/systray").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "systray_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		`className = "SystrayClass"`,
		`const WM_USER = 0x0400`,
		`t.wmSystrayMessage = WM_USER + 1`,
		`WM_RBUTTONUP = 0x0205`,
		`case t.wmSystrayMessage: switch lParam { case WM_LBUTTONUP: systrayLeftClick() case WM_RBUTTONUP: systrayRightClick() }`,
		`func systrayRightClick() { if fn := tappedRight; fn != nil { fn() return } wt.showMenu() }`,
		`func systrayLeftClick() { if fn := tappedLeft; fn != nil { fn() return } wt.showMenu() }`,
		`ID: 100,`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("fyne.io/systray no longer has %q", want)
		}
	}
	if systrayMessage != 0x0400+1 || rightButtonUp != 0x0205 {
		t.Errorf("ShowMenu posts %#x, %#x", systrayMessage, rightButtonUp)
	}
}
