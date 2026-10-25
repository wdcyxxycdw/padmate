//go:build !windows

package main

import "errors"

// newSystemStartup: only Windows has a login startup entry.
func newSystemStartup(string, []string) (startupSetting, error) {
	return nil, errors.ErrUnsupported
}
