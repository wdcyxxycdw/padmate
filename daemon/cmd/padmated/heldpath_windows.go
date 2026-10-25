//go:build windows

package main

import "os"

// heldFilePath returns the path of the held-key log of --inject (OUT-14):
// %LOCALAPPDATA%\padmate\run\held.bin, or "" and why without LOCALAPPDATA.
// The held.json of the versions before it is not read.
func heldFilePath() (string, error) { return heldPathIn(os.Getenv("LOCALAPPDATA")) }
