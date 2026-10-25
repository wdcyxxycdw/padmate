package inputtest

import "errors"

// systemLibrary is never used on Windows, where padmate loads only the
// pinned DLL.
func systemLibrary() (string, error) {
	return "", errors.New("no system SDL on Windows")
}
