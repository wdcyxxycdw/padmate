package buildtool

import (
	"os/exec"
	"syscall"
)

// HideWindow prevents build tools and smoke tests from opening console windows.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
