package diagnostics

import (
	"slices"
	"testing"
)

func TestConflictingProcesses(t *testing.T) {
	got := ConflictingProcesses([]string{"STEAM.EXE", "steam.exe", "steamwebhelper.exe", "DS4Windows.exe", "HidHideClient.exe", "HidHideCLI.exe", "my-steam.exe", "padmated.exe"})
	want := []string{"DS4Windows", "HidHide", "Steam"}
	if !slices.Equal(got, want) {
		t.Fatalf("conflicts = %v, want %v", got, want)
	}
	if got := ConflictingProcesses(nil); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
}
