package main

import (
	"fmt"
	"io"

	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
)

// releaseAllEvent is the named event release-all signals (DMN-20); the
// tests' TestMain sets a name of their own.
var releaseAllEvent = emergency.Name

// runReleaseAll is intentionally independent of the daemon's IPC. It is the
// last-resort path for a hand controller whose emergency combo is unavailable.
func runReleaseAll(_ []string, stdout, stderr io.Writer) int {
	if err := emergency.Trigger(releaseAllEvent); err != nil {
		fmt.Fprintln(stderr, "padmatectl release-all:", err)
		return 1
	}
	fmt.Fprintln(stdout, "padmatectl: release requested")
	return 0
}
