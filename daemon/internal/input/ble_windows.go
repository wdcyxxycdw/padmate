package input

import (
	"log/slog"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// startJoyCon2Transport starts the Windows Runtime Bluetooth LE transport.
func startJoyCon2Transport(log *slog.Logger) (JoyCon2Transport, error) {
	t, err := jc2win.Start(jc2win.Options{Log: log})
	if err != nil {
		return nil, err
	}
	return t, nil
}
