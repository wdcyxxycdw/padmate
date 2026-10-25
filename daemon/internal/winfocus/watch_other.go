//go:build !windows

package winfocus

import "errors"

func Attach(uintptr, Config) (*Watcher, error)          { return nil, errors.ErrUnsupported }
func (w *Watcher) Handle(uint32, uintptr, uintptr) bool { return false }
func (w *Watcher) Poll()                                {}
func (w *Watcher) Close() error                         { return nil }
