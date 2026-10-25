//go:build windows

package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// quitPoll is how often a wait for the process looks at its context.
const quitPoll = 50 * time.Millisecond

// winProcess is a process handle that can be waited on.
type winProcess struct {
	h    windows.Handle
	name string
	path string
}

// openQuitProcess opens the process with pid for waiting on its exit. A pid
// that no process has, or a process that has ended already, is
// errProcessGone.
func openQuitProcess(pid uint32) (quitProcess, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil, errProcessGone
	}
	if err != nil {
		return nil, err
	}
	p := &winProcess{h: h}
	// A handle that others hold keeps the object of a process that has ended.
	if event, err := windows.WaitForSingleObject(h, 0); err == nil && event == windows.WAIT_OBJECT_0 {
		p.Close()
		return nil, errProcessGone
	}
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil {
		p.path = windows.UTF16ToString(buf[:size])
		p.name = filepath.Base(p.path)
	}
	return p, nil
}

func (p *winProcess) Name() string { return p.name }

func (p *winProcess) Path() string { return p.path }

// quitLongPath returns the long form of an existing path; a path that does
// not exist, or that cannot be converted, is returned as given.
func quitLongPath(p string) string {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	n, err := windows.GetLongPathName(in, nil, 0)
	if err != nil || n == 0 {
		return p
	}
	buf := make([]uint16, n)
	n, err = windows.GetLongPathName(in, &buf[0], n)
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

func (p *winProcess) Close() { _ = windows.CloseHandle(p.h) }

// Wait polls, so that ctx ends it within quitPoll.
func (p *winProcess) Wait(ctx context.Context) error {
	for {
		event, err := windows.WaitForSingleObject(p.h, uint32(quitPoll/time.Millisecond))
		switch {
		case err != nil:
			return err
		case event == windows.WAIT_OBJECT_0:
			return nil
		case event != uint32(windows.WAIT_TIMEOUT):
			return errors.New("unexpected wait result")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
