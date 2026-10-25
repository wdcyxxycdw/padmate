package compile

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandle flags: the normalized long path with a drive
// letter. x/sys/windows does not name them; both are 0.
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
)

// canonical returns the absolute path of the existing file or directory
// path with every link resolved, junctions included. filepath.EvalSymlinks
// cannot do this: since Go 1.23 (winsymlink=1) Lstat reports a junction as
// ModeIrregular, and EvalSymlinks fails with ENOTDIR on a junction in the
// middle of a path. esbuild does not follow junctions either, so the import
// checks compare these paths, not esbuild's.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: path, Err: err}
	}
	// FILE_FLAG_BACKUP_SEMANTICS opens directories as well; the handle only
	// needs to read attributes.
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), fileNameNormalized|volumeNameDOS)
		if n == 0 {
			return "", &os.PathError{Op: "GetFinalPathNameByHandle", Path: path, Err: err}
		}
		if int(n) < len(buf) {
			return trimExtendedPrefix(windows.UTF16ToString(buf[:n])), nil
		}
		// n is the size needed, terminating NUL included.
		buf = make([]uint16, n)
	}
}

// trimExtendedPrefix turns the \\?\ form GetFinalPathNameByHandle returns
// into a plain path: \\?\C:\a into C:\a and \\?\UNC\host\share into
// \\host\share.
func trimExtendedPrefix(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}
