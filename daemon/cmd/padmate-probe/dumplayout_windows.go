package main

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
)

// winDumper reads layouts with the Win32 keyboard functions.
type winDumper struct{}

func newLayoutDumper() (layoutDumper, error) {
	return winDumper{}, nil
}

func (winDumper) LayoutList() (string, error) {
	list, err := kblayout.LayoutList()
	if err != nil {
		return "", err
	}
	return kblayout.FormatList(list), nil
}

func (winDumper) Dump(klid string) (*kblayout.Fixture, error) {
	return kblayout.Dump(klid)
}
