package main

import (
	"os"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestNewInjectSender: --inject injects with SendInput, whose events carry
// padmated's process id as their nonce. It constructs the Sender only.
func TestNewInjectSender(t *testing.T) {
	s, err := newInjectSender()
	if err != nil {
		t.Fatal(err)
	}
	si, ok := s.(output.SendInput)
	if !ok || si.Nonce != uint32(os.Getpid()) {
		t.Errorf("newInjectSender() = %#v, want output.SendInput{Nonce: %d}", s, os.Getpid())
	}
}
