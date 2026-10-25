//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Stderr)) }

// run fails: the spike is about SDL's Windows backends and the Windows
// tray.
func run(stderr io.Writer) int {
	fmt.Fprintln(stderr, "spk01 is only supported on Windows")
	return 1
}
