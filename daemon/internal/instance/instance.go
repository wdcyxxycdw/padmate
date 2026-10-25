// Package instance holds padmated's Windows single-instance lease.
package instance

import "errors"

// ErrAlreadyRunning means another live padmated owns this user's session.
var ErrAlreadyRunning = errors.New("padmated is already running in this session")
