//go:build windows

package main

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestReleaseAllSignalsNamedEvent: release-all signals releaseAllEvent,
// which TestMain sets to a name of the test's own, and fails when nothing
// holds the event.
func TestReleaseAllSignalsNamedEvent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runReleaseAll(nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), releaseAllEvent) {
		t.Errorf("runReleaseAll with no listener: exit %d, stderr %q; want 1 and the event's name", code, stderr.String())
	}
	name, err := windows.UTF16PtrFromString(releaseAllEvent)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenEvent(windows.EVENT_ALL_ACCESS, false, name)
	if err != nil {
		h, err = windows.CreateEvent(nil, 0, 0, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	stdout.Reset()
	stderr.Reset()
	if code := runReleaseAll(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("runReleaseAll exit %d, stderr %q", code, stderr.String())
	}
	got, err := windows.WaitForSingleObject(h, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got != windows.WAIT_OBJECT_0 {
		t.Fatalf("WaitForSingleObject = %#x, want signaled", got)
	}
}
