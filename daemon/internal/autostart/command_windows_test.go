package autostart

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCommandRoundTripsWindowsArgv(t *testing.T) {
	exe := `D:\Programs\绘画 tools\padmated.exe`
	args := []string{"--profile", `D:\绘画\my profiles\ink.json`, "--dry-run", "--device", `name"quote\`, "", `a b\`, `& not-a-shell`}
	command, err := Command(exe, args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := windows.DecomposeCommandLine(command)
	want := append([]string{exe}, args...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows argv %q; want %q, err %v", got, want, err)
	}
}
