//go:build !windows

package main

import (
	"fmt"
	"io"
)

func runKeyboardCapture(_ []string, _ io.Writer, stderr io.Writer) int {
	fmt.Fprintln(stderr, "native keyboard capture requires Windows")
	return 1
}
