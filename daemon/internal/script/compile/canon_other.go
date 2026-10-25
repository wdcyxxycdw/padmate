//go:build !windows

package compile

import "path/filepath"

// canonical returns the absolute path of the existing file or directory
// path with every symlink resolved.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
