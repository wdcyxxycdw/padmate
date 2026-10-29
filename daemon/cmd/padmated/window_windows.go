package main

import (
	"os/exec"
	"path/filepath"
)

// startWindow starts padmate.exe at exe as its shortcut in the Start menu
// does: in its installation folder, with no arguments, without waiting for
// it.
func startWindow(exe string) error {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
