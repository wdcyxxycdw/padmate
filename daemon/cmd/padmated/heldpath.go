package main

import (
	"errors"
	"path/filepath"
)

// heldPathIn returns padmate\run\held.bin in the folder root, the value of
// LOCALAPPDATA, or "" and why when root is empty. Like the log's folder
// (os.UserConfigDir), it comes from the environment only: padmated does
// not call Windows itself outside sender_windows.go (TestNoSendInput).
func heldPathIn(root string) (string, error) {
	if root == "" {
		return "", errors.New("padmated: no folder for the held-key log: LOCALAPPDATA is not set")
	}
	return filepath.Join(root, "padmate", "run", "held.bin"), nil
}
