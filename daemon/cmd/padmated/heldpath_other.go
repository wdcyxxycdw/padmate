//go:build !windows

package main

// heldFilePath returns "": padmated injects only on Windows.
func heldFilePath() (string, error) { return "", nil }
