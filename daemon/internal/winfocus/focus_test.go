package winfocus

import (
	"testing"
	"time"
)

func TestDebouncePreservesMappingAcrossShell(t *testing.T) {
	a := App{Window: 1, PID: 10, Exe: "paint.exe", Path: `C:\apps\paint.exe`}
	b := App{Window: 2, PID: 11, Exe: "other.exe", Path: `C:\apps\other.exe`}
	var d Debouncer
	d.Signal(0)
	if _, ok := d.Sample(49*time.Millisecond, a); ok {
		t.Fatal("published before debounce")
	}
	if got, ok := d.Sample(50*time.Millisecond, a); !ok || got != a {
		t.Fatal(got, ok)
	}
	for _, class := range []string{"MultitaskingViewFrame", "XamlExplorerHostIslandWindow", "ForegroundStaging", "Shell_TrayWnd", "#32768"} {
		d.Signal(time.Second)
		if _, ok := d.Sample(2*time.Second, App{Window: 3, Class: class, Path: `C:\Windows\explorer.exe`}); ok {
			t.Fatal("shell window replaced foreground", class)
		}
		if d.Current != a {
			t.Fatal("shell forgot previous application")
		}
	}
	d.Signal(3 * time.Second)
	d.Signal(3*time.Second + 30*time.Millisecond)
	if _, ok := d.Sample(3*time.Second+50*time.Millisecond, b); ok {
		t.Fatal("second signal did not extend debounce")
	}
	if got, ok := d.Sample(3*time.Second+80*time.Millisecond, b); !ok || got != b {
		t.Fatal(got, ok)
	}
	d.Signal(4 * time.Second)
	if _, ok := d.Sample(5*time.Second, b); ok {
		t.Fatal("same foreground published twice")
	}
	d.Signal(6 * time.Second)
	if _, ok := d.Sample(7*time.Second, App{}); ok || d.Current != b {
		t.Fatal("unknown foreground changed selection")
	}
}

func TestAppMatchesOnlyExecutable(t *testing.T) {
	a := App{Exe: "PAINT.EXE", Path: `C:\Apps\Paint.exe`}
	for _, name := range []string{"paint.exe", `c:\apps\PAINT.exe`, "C:/Apps/Paint.exe"} {
		if !a.Is(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"paint", "*.exe", `D:\Apps\Paint.exe`, ""} {
		if a.Is(name) {
			t.Fatal(name)
		}
	}
}
