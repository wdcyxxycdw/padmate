package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func TestForwardStartupCompatibility(t *testing.T) {
	dir := t.TempDir()
	current, _, ok := parseFlags([]string{"--profile-id=paint", "--data-dir=" + dir, "--dry-run", "--device=Nova", "--preset=xinput", "--hint=SDL_TEST=1", "--watch=false", "--tray=false", "--log-file=-"}, io.Discard, io.Discard)
	if !ok {
		t.Fatal("invalid fixture")
	}
	current.library = profilelib.New(dir)
	r := &liveRun{cfg: liveConfig{opts: current}}
	for _, tc := range []struct {
		name   string
		extra  []string
		wantOK bool
	}{
		{"target only", nil, true},
		{"same settings", []string{"--watch=false", "--device=Nova", "--preset=xinput", "--hint=SDL_TEST=1", "--tray=false", "--log-file=-"}, true},
		{"output mode", []string{"--dry-run=false", "--inject"}, false},
		{"fake mode", []string{"--fake-input"}, false},
		{"device", []string{"--device=Other"}, false},
		{"preset", []string{"--preset=default"}, false},
		{"hints", []string{"--hint=SDL_TEST=2"}, false},
		{"watch", []string{"--watch"}, false},
		{"tray", []string{"--tray"}, false},
		{"decisions", []string{"--decisions"}, false},
		{"logging", []string{"--log-level=debug"}, false},
		{"file logging", []string{"--log-file=" + filepath.Join(dir, "new.log")}, false},
		{"data directory", []string{"--data-dir=" + t.TempDir()}, false},
		{"same directory", []string{"--data-dir=" + dir}, true},
		{"file to library", []string{"--profile-id=", "--profile=" + filepath.Join(dir, "other.json")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _, ok := parseFlags(append([]string{"--profile-id=other", "--dry-run"}, tc.extra...), io.Discard, io.Discard)
			if !ok {
				t.Fatal("invalid fixture")
			}
			if err := r.checkForwardOptions(o); (err == nil) != tc.wantOK {
				t.Fatalf("compatibility %v, want OK=%v", err, tc.wantOK)
			}
		})
	}
}
