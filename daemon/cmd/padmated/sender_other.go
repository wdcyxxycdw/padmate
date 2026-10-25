//go:build !windows

package main

import (
	"errors"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

var errInjectUnsupported = errors.New("--inject needs Windows")

// newInjectSender fails: padmated injects only on Windows.
func newInjectSender() (output.Sender, error) { return nil, errInjectUnsupported }
