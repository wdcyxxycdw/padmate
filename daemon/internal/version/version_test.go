package version

import (
	"runtime/debug"
	"testing"
)

func TestFormat(t *testing.T) {
	const full = "de58d1d0123456789abcdef0123456789abcdef0"
	vcs := func(rev, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: rev},
			{Key: "vcs.modified", Value: modified},
		}
	}
	tests := []struct {
		version  string
		settings []debug.BuildSetting
		want     string
	}{
		{"dev", nil, "dev"},
		{"dev", vcs(full, "false"), "dev (de58d1d)"},
		{"dev", vcs(full, "true"), "dev (de58d1d-dirty)"},
		{"v0.0.1", vcs(full, "false"), "v0.0.1 (de58d1d)"},
		// git describe output already names the revision.
		{"de58d1d-dirty", vcs(full, "true"), "de58d1d-dirty"},
		{"v0.0.1-3-gde58d1d", vcs(full, "false"), "v0.0.1-3-gde58d1d"},
	}
	for _, tt := range tests {
		if got := format(tt.version, tt.settings); got != tt.want {
			t.Errorf("format(%q, %v) = %q, want %q", tt.version, tt.settings, got, tt.want)
		}
	}
}
