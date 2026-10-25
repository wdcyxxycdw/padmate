//go:build windows

package output

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// winHeldFile is the held-key log opened with CreateFile, shared for
// reading only: a second padmated cannot open it for writing
// (ErrHeldInUse), and a test can read it while the Loop has it open. It
// reads and writes with one ReadFile or WriteFile at the OVERLAPPED's
// offset, which the handle, opened without FILE_FLAG_OVERLAPPED, runs
// synchronously; os.File's WriteAt would seek, write and seek back
// (docs/PITFALLS.md). The OVERLAPPED and the count are fields, so that a
// write does not allocate.
type winHeldFile struct {
	h    windows.Handle
	ov   windows.Overlapped
	done uint32
}

func openHeldFileOS(path string) (heldFileHandle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, fmt.Errorf("%w: %s", ErrHeldInUse, path)
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return &winHeldFile{h: h}, nil
}

func (f *winHeldFile) at(off int64) *windows.Overlapped {
	f.ov = windows.Overlapped{Offset: uint32(off), OffsetHigh: uint32(off >> 32)}
	f.done = 0
	return &f.ov
}

// ReadAt reads once at off; it returns io.EOF when off is at the end of
// the file.
func (f *winHeldFile) ReadAt(p []byte, off int64) (int, error) {
	err := windows.ReadFile(f.h, p, &f.done, f.at(off))
	if errors.Is(err, windows.ERROR_HANDLE_EOF) {
		return int(f.done), io.EOF
	}
	return int(f.done), err
}

// WriteAt writes once at off.
func (f *winHeldFile) WriteAt(p []byte, off int64) (int, error) {
	err := windows.WriteFile(f.h, p, &f.done, f.at(off))
	return int(f.done), err
}

func (f *winHeldFile) Close() error { return windows.CloseHandle(f.h) }
