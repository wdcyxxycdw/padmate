//go:build !windows

package buildtool

import "os/exec"

// HideWindow is unnecessary on other platforms.
func HideWindow(_ *exec.Cmd) {}
