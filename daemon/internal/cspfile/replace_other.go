//go:build !windows

package cspfile

import "os"

// replaceAtomic renames tmp over target.
func replaceAtomic(target, tmp string) error { return os.Rename(tmp, target) }
