//go:build !windows

package main

import (
	"errors"
	"testing"
)

func TestPlatformSenderUnsupported(t *testing.T) {
	s, err := platformSender()
	if s != nil || !errors.Is(err, errUnsupported) {
		t.Errorf("platformSender() = %v, %v; want nil, errUnsupported", s, err)
	}
}
