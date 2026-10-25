//go:build windows

package emergency

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var testEvents atomic.Int64

// testName returns an event name of the test's own.
func testName() string {
	return fmt.Sprintf(`Local\padmate.emergency-test-%d-%d`, os.Getpid(), testEvents.Add(1))
}

// serve runs l.Serve on a goroutine and returns the channel of the calls
// of on and the channel of its result.
func serve(ctx context.Context, l *Listener) (<-chan struct{}, <-chan error) {
	got := make(chan struct{}, 4)
	errc := make(chan error, 1)
	go func() { errc <- l.Serve(ctx, func() { got <- struct{}{} }) }()
	return got, errc
}

func waitSignal(t *testing.T, got <-chan struct{}) {
	t.Helper()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("Serve did not call on within 1 s")
	}
}

// TestListenTrigger: Listen creates the event, so a Trigger right after it
// reaches the Listener before Serve runs, with no sleep; Trigger fails
// without a listener; Serve returns nil soon after its context ends.
func TestListenTrigger(t *testing.T) {
	name := testName()
	if err := Trigger(name); err == nil {
		t.Error("Trigger with no listener succeeded")
	}
	l, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := Trigger(name); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, errc := serve(ctx, l)
	waitSignal(t, got)
	if err := Trigger(name); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, got)
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Serve = %v after the cancel, want nil", err)
		}
	case <-time.After(2 * waitInterval):
		t.Fatalf("Serve did not return within %v of the cancel", 2*waitInterval)
	}
	if err := Trigger(name); err == nil {
		t.Error("Trigger after Serve returned succeeded: the event is still open")
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close after Serve = %v, want nil", err)
	}
}

// TestListenTwice: a second Listen on the same name opens the existing
// event (CreateEvent's ERROR_ALREADY_EXISTS) and keeps its handle: after
// the first Listener is closed, the second still gets a Trigger.
func TestListenTwice(t *testing.T) {
	name := testName()
	first, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Listen(name)
	if err != nil {
		t.Fatalf("second Listen: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, errc := serve(ctx, second)
	if err := Trigger(name); err != nil {
		t.Fatalf("Trigger with the second Listener: %v", err)
	}
	waitSignal(t, got)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Serve = %v after Close, want nil", err)
		}
	case <-time.After(2 * waitInterval):
		t.Fatalf("Serve did not return within %v of Close", 2*waitInterval)
	}
}

// TestListenBadName: a name Windows cannot take fails Listen and Trigger.
func TestListenBadName(t *testing.T) {
	if _, err := Listen("bad\x00name"); err == nil {
		t.Error("Listen with a NUL in the name succeeded")
	}
	if err := Trigger("bad\x00name"); err == nil {
		t.Error("Trigger with a NUL in the name succeeded")
	}
	l, err := Listen(testName())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if err := l.Serve(context.Background(), nil); err == nil {
		t.Error("Serve with a nil callback succeeded")
	}
}

// The mandatory label ACE of a SACL (winnt.h), which x/sys does not have.
const (
	systemMandatoryLabelACEType = 0x11
	mandatoryLabelNoWriteUp     = 0x1
)

// TestListenSecurity: Listen creates the event with a DACL of one ACE,
// EVENT_ALL_ACCESS for the user of the process token, and a Medium
// mandatory label with no-write-up, so that the same user's padmatectl
// opens it with EVENT_MODIFY_STATE whether padmated runs elevated or not
// (ADR-0028). Elevation itself is not tested.
func TestListenSecurity(t *testing.T) {
	l, err := Listen(testName())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	sd, err := windows.GetSecurityInfo(l.h, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("security descriptor %s", sd)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("DACL %v, want one ACE", dacl)
	}
	ace := aceAt(t, dacl, 0)
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != eventAllAccess || !aceSID(ace).Equals(user.User.Sid) {
		t.Errorf("DACL ACE type %d, mask %#x, SID %s; want allowed, %#x, %s",
			ace.Header.AceType, ace.Mask, aceSID(ace), eventAllAccess, user.User.Sid)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		t.Fatal(err)
	}
	if sacl == nil || sacl.AceCount != 1 {
		t.Fatalf("SACL %v, want the label only", sacl)
	}
	medium, err := windows.CreateWellKnownSid(windows.WinMediumLabelSid)
	if err != nil {
		t.Fatal(err)
	}
	label := aceAt(t, sacl, 0)
	if label.Header.AceType != systemMandatoryLabelACEType || label.Mask != mandatoryLabelNoWriteUp || !aceSID(label).Equals(medium) {
		t.Errorf("label ACE type %#x, mask %#x, SID %s; want %#x, no-write-up, %s",
			label.Header.AceType, label.Mask, aceSID(label), systemMandatoryLabelACEType, medium)
	}
	// What padmatectl asks for.
	p, err := windows.UTF16PtrFromString(l.name)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.SYNCHRONIZE, false, p)
	if err != nil {
		t.Fatalf("OpenEvent(EVENT_MODIFY_STATE|SYNCHRONIZE): %v", err)
	}
	_ = windows.CloseHandle(h)
}

// aceAt returns ACE i of acl; a label ACE has the layout of an allowed
// one.
func aceAt(t *testing.T, acl *windows.ACL, i uint32) *windows.ACCESS_ALLOWED_ACE {
	t.Helper()
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, i, &ace); err != nil {
		t.Fatal(err)
	}
	return ace
}

// aceSID is the SID of ace, which starts at SidStart.
func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}
