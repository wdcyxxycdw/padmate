package main

import (
	"os"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// newInjectSender returns the Sender of --inject: SendInput, with
// padmated's process id as the nonce of its events. It is the only place
// padmated constructs output.SendInput, and only --inject calls it
// (CLAUDE.md invariant 6; TestNoSendInput and forbidigo check it).
func newInjectSender() (output.Sender, error) {
	return output.SendInput{Nonce: uint32(os.Getpid())}, nil
}
