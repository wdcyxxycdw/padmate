//go:build windows

package instance

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func testName() string { return `Local\padmate.instance-test-` + rand.Text() }

func TestLeaseExclusiveAndClose(t *testing.T) {
	name := testName()
	first, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if second, err := Acquire(name); !errors.Is(err, ErrAlreadyRunning) || second != nil {
		t.Fatalf("second lease=%v/%v, want already running", second, err)
	}
	for range 2 {
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Acquire(name)
	if err != nil {
		t.Fatalf("lease after close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNameIdentity(t *testing.T) {
	a := identityName("S-1-5-21-123", 2)
	if a == identityName("S-1-5-21-456", 2) || a == identityName("S-1-5-21-123", 3) {
		t.Fatal("instance name does not isolate users and sessions")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		t.Fatal(err)
	}
	got, err := SystemName()
	if err != nil || got != identityName(user.User.Sid.String(), session) {
		t.Fatalf("SystemName=%q/%v", got, err)
	}
	// A supplied test name always bypasses the production identity.
	if _, err := Acquire("bad\x00name"); err == nil {
		t.Fatal("NUL name accepted")
	}
}

func TestLeaseSecurity(t *testing.T) {
	l, err := Acquire(testName())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	sd, err := windows.GetSecurityInfo(l.h, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("DACL=%v/%v", dacl, err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != windows.MUTEX_ALL_ACCESS || !sid.Equals(user.User.Sid) {
		t.Fatalf("DACL grants %#x to %s, want mutex access for %s", ace.Mask, sid, user.User.Sid)
	}
	// Medium integrity permits the same user's unelevated and elevated
	// processes to open the object; elevation itself remains manual QA.
	if !strings.Contains(sd.String(), "S:") || !strings.Contains(sd.String(), "(ML;;NW;;;ME)") {
		t.Fatalf("missing Medium no-write-up label: %s", sd)
	}
}

func TestLeaseProcess(t *testing.T) {
	name := testName()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseChild$")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(), "PADMATE_INSTANCE_TEST_NAME="+name, "PADMATE_INSTANCE_TEST_HOLD=1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(pipe)
		if s.Scan() {
			ready <- s.Text()
		}
	}()
	select {
	case got := <-ready:
		if got != "ready" {
			t.Fatalf("child: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first child did not acquire lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLeaseChild$")
	second.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	second.Env = append(os.Environ(), "PADMATE_INSTANCE_TEST_NAME="+name, "PADMATE_INSTANCE_TEST_HOLD=0")
	if out, err := second.CombinedOutput(); err == nil {
		t.Fatalf("second child succeeded: %s", out)
	} else if !strings.Contains(string(out), ErrAlreadyRunning.Error()) {
		t.Fatalf("second child failed for a different reason: %v, %s", err, out)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	l, err := Acquire(name)
	if err != nil {
		t.Fatalf("lease after killed process: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseChild(t *testing.T) {
	name := os.Getenv("PADMATE_INSTANCE_TEST_NAME")
	if name == "" {
		t.Skip("subprocess helper")
	}
	l, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if os.Getenv("PADMATE_INSTANCE_TEST_HOLD") == "1" {
		fmt.Println("ready")
		<-time.After(30 * time.Second)
	}
}
