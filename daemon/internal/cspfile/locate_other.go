//go:build !windows

package cspfile

// SystemRoots is empty off Windows.
func SystemRoots() Roots { return Roots{} }

// FindSystemDataDirs finds nothing off Windows.
func FindSystemDataDirs() []DataDir { return nil }

// FindInstallDir is unsupported off Windows.
func FindInstallDir() (InstallDir, error) { return InstallDir{}, ErrUnsupported }
