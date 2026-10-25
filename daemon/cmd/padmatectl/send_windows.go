package main

import (
	"os"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// platformSender injects with SendInput, tagging every event with this
// process's id as the nonce so that a hook can tell its events apart.
func platformSender() (output.Sender, error) {
	return output.SendInput{Nonce: uint32(os.Getpid())}, nil
}
