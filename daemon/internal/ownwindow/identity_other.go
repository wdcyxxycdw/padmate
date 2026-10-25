//go:build !windows

package ownwindow

import "errors"

func Verify(uint32, uint32) (string, error) { return "", errors.ErrUnsupported }
func ForegroundPID() uint32                 { return 0 }
func Matches(uint32, string) bool           { return false }
func Instance(uint32) (uint64, error)       { return 0, errors.ErrUnsupported }
func Alive(uint32, string, uint64) bool     { return false }
