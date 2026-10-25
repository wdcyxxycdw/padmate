//go:build !windows

package autostart

import "errors"

type systemStore struct{}

var errUnsupported = errors.New("autostart: supported only on Windows")

func (systemStore) read() (string, error) { return "", errUnsupported }
func (systemStore) write(string) error    { return errUnsupported }
func (systemStore) remove() error         { return errUnsupported }
