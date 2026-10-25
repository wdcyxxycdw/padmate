//go:build !windows

package output

import "os"

// openHeldFileOS opens the held-key log as an *os.File. It takes no lock:
// padmated injects only on Windows, and this serves the tests elsewhere,
// so ErrHeldInUse happens only on Windows.
func openHeldFileOS(path string) (heldFileHandle, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err // not a typed nil
	}
	return f, nil
}
