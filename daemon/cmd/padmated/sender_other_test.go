//go:build !windows

package main

import (
	"errors"
	"testing"
)

// TestNewInjectSender: only Windows injects.
func TestNewInjectSender(t *testing.T) {
	if s, err := newInjectSender(); s != nil || !errors.Is(err, errInjectUnsupported) {
		t.Errorf("newInjectSender() = %v, %v; want nil, %v", s, err, errInjectUnsupported)
	}
}
