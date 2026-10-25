package spike

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func cpuName() string {
	name, mhz, err := cpuInfo()
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return fmt.Sprintf("%s (~MHz %d)", name, mhz)
}

// versionKey is where Windows keeps its display version. It is only read.
const versionKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`

func osName() string {
	v := windows.RtlGetVersion()
	s := fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, versionKey, registry.QUERY_VALUE)
	if err != nil {
		return s
	}
	defer k.Close()
	if dv, _, err := k.GetStringValue("DisplayVersion"); err == nil {
		s += " (" + dv + ")"
	}
	return s
}

func stopwatchName() string {
	return fmt.Sprintf("QueryPerformanceCounter, %d Hz", stampFreq())
}
