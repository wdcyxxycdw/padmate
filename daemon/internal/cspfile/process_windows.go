package cspfile

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Running reports whether CLIPStudioPaint.exe is running, from a process
// snapshot. It sees every session's processes it is allowed to list.
func Running() (bool, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	err = windows.Process32First(snap, &pe)
	for err == nil {
		if IsCSPImage(windows.UTF16ToString(pe.ExeFile[:])) {
			return true, nil
		}
		err = windows.Process32Next(snap, &pe)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return false, nil
	}
	return false, err
}
