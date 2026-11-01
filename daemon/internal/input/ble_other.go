//go:build !windows

package input

import (
	"errors"
	"log/slog"
)

// startJoyCon2Transport has no transport outside Windows.
func startJoyCon2Transport(*slog.Logger) (JoyCon2Transport, error) {
	return nil, errors.New("the Bluetooth LE transport needs Windows")
}
