package winfocus

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestQueryOwnExecutableAndIntegrity(t *testing.T) {
	a := App{PID: windows.GetCurrentProcessId()}
	if err := queryProcess(&a); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(exe, a.Path) || !strings.HasSuffix(strings.ToLower(a.Exe), ".exe") {
		t.Fatal(a, exe, err)
	}
	if _, err := highIntegrity(windows.GetCurrentProcessToken()); err != nil {
		t.Fatal(err)
	}
	if err := queryProcess(&App{}); err == nil {
		t.Fatal("accepted process zero")
	}
}
