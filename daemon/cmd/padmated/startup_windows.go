package main

import "github.com/wdcyxxycdw/padmate/daemon/internal/autostart"

// newSystemStartup is the HKCU Run entry of exe and args. Making it reads
// and writes nothing.
func newSystemStartup(exe string, args []string) (startupSetting, error) {
	e, err := autostart.New(exe, args)
	if err != nil {
		return nil, err // not a typed nil
	}
	return e, nil
}
