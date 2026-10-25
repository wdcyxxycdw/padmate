//go:build !windows

package main

import (
	"bytes"
	"testing"
)

func TestRunUnsupported(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(&stderr); code != 1 {
		t.Errorf("run() = %d, want 1", code)
	}
	if got, want := stderr.String(), "spk01 is only supported on Windows\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}
