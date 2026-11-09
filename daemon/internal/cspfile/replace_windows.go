package cspfile

import "golang.org/x/sys/windows"

const replacefileIgnoreMergeErrors = 0x2

// replaceAtomic swaps tmp in for target. ReplaceFileW keeps the target's
// attributes and ACL; if it fails (for example because the target is on a
// file system without support), MoveFileExW replaces the bytes and writes
// through to disk.
func replaceAtomic(target, tmp string) error {
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	s, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	if err := replaceFile(t, s, nil, replacefileIgnoreMergeErrors, 0, 0); err == nil {
		return nil
	}
	return windows.MoveFileEx(s, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
