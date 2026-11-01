package profilelib

import (
	"errors"

	"golang.org/x/sys/windows"
)

// fileInUse reports whether err is Windows saying another handle has the
// file: a sharing or lock violation, or a denied access, which a file being
// deleted or replaced by another handle also gives.
func fileInUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
