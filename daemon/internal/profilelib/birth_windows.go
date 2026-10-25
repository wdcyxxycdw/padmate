package profilelib

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// birthTime is when the file or folder info describes was created, from
// NTFS's creation time; ok is false when info does not carry one. A rename
// keeps it, a replacement by rename does not (setBirthTime).
func birthTime(info fs.FileInfo) (created time.Time, ok bool) {
	data, isData := info.Sys().(*syscall.Win32FileAttributeData)
	if !isData || data == nil || data.CreationTime.HighDateTime == 0 && data.CreationTime.LowDateTime == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, data.CreationTime.Nanoseconds()), true
}

// setBirthTime gives the open file f the creation time created. The file
// must be open for writing, as atomicWrite's temporary file is.
func setBirthTime(f *os.File, created time.Time) error {
	ft := windows.NsecToFiletime(created.UnixNano())
	return windows.SetFileTime(windows.Handle(f.Fd()), &ft, nil, nil)
}

// setFolderBirthTime gives the folder name in root the creation time
// created. A folder needs its own handle: os.File cannot open one for
// writing attributes through os.Root, so it is opened by path with the
// backup semantics a directory handle takes. root is the library's own
// profiles folder and name a folder made by this process.
func setFolderBirthTime(root *os.Root, name string, created time.Time) error {
	path, err := windows.UTF16PtrFromString(filepath.Join(root.Name(), name))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(path, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ft := windows.NsecToFiletime(created.UnixNano())
	return windows.SetFileTime(h, &ft, nil, nil)
}
