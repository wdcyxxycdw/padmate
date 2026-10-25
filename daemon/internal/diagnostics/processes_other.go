//go:build !windows

package diagnostics

import "errors"

func Processes() ([]string, error) { return nil, errors.ErrUnsupported }
