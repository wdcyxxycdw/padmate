package input

import (
	"flag"
	"io"
	"slices"
	"testing"
)

func TestSDLFlags(t *testing.T) {
	xinput, _ := PresetHints("xinput")
	def, _ := PresetHints("default")
	for _, tc := range []struct {
		name    string
		args    []string
		preset  string
		hints   []Hint
		wantErr string
	}{
		{"default", nil, "default", def, ""},
		{"preset", []string{"--preset", "xinput"}, "xinput", xinput, ""},
		{"extra after the preset", []string{"--hint", "A=1", "--preset", "xinput", "--hint", "B="}, "xinput",
			slices.Concat(xinput, []Hint{{"A", "1"}, {"B", ""}}), ""},
		{"unknown preset", []string{"--preset", "nope"}, "nope", nil,
			`unknown preset "nope"; want one of default, xinput, rawinput, hidapi-off, wgi, gameinput`},
		{"empty preset", []string{"--preset", ""}, "", nil,
			`unknown preset ""; want one of default, xinput, rawinput, hidapi-off, wgi, gameinput`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			var f SDLFlags
			f.AddFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if f.Preset != tc.preset {
				t.Errorf("Preset = %q, want %q", f.Preset, tc.preset)
			}
			hints, err := f.Hints()
			switch {
			case tc.wantErr != "":
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("Hints() error = %v, want %q", err, tc.wantErr)
				}
			case err != nil:
				t.Errorf("Hints() error = %v", err)
			case !slices.Equal(hints, tc.hints):
				t.Errorf("Hints() = %v, want %v", hints, tc.hints)
			}
		})
	}
}

// TestSDLFlagsBadHint: a malformed --hint is a flag error.
func TestSDLFlagsBadHint(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f SDLFlags
	f.AddFlags(fs)
	if err := fs.Parse([]string{"--hint", "NOVALUE"}); err == nil {
		t.Error("Parse(--hint NOVALUE) succeeded")
	}
}
