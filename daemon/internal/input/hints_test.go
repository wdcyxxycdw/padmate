package input

import (
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

// TestPresets checks that every preset sets the background hint first,
// and that every preset but "default" sets every backend hint explicitly,
// once. No preset sets a Joy-Con hint or repeats another hint of
// openHints, so that none silently undoes what Open always sets (IN-07).
func TestPresets(t *testing.T) {
	want := []string{"default", "xinput", "rawinput", "hidapi-off", "wgi", "gameinput"}
	if got := PresetNames(); !slices.Equal(got, want) {
		t.Fatalf("PresetNames() = %q, want %q", got, want)
	}
	if openHints[0] != backgroundHint {
		t.Fatalf("openHints %v do not start with the background hint", openHints)
	}
	reserved := make(map[string]bool)
	for _, h := range openHints {
		reserved[h.Name] = true
	}
	for _, name := range JoyConHints {
		reserved[name] = true
	}
	for _, p := range Presets {
		if len(p.Hints) == 0 || p.Hints[0] != (Hint{HintBackgroundEvents, "1"}) {
			t.Errorf("%s: hints %v do not start with %s=1", p.Name, p.Hints, HintBackgroundEvents)
		}
		if p.About == "" {
			t.Errorf("%s: no description", p.Name)
		}
		// The background hint that every preset starts with sets the same
		// value as openHints; no other hint may be one of theirs.
		for i, h := range p.Hints {
			if i == 0 && h == backgroundHint {
				continue
			}
			if reserved[h.Name] {
				t.Errorf("%s: sets %s, which Open sets or leaves to SDL", p.Name, h)
			}
		}
		if p.Name == "default" {
			if len(p.Hints) != 1 {
				t.Errorf("default: hints %v, want only the background hint", p.Hints)
			}
			continue
		}
		set := make(map[string]string)
		for _, h := range p.Hints[1:] {
			if _, dup := set[h.Name]; dup {
				t.Errorf("%s: %s is set twice", p.Name, h.Name)
			}
			if h.Value != "0" && h.Value != "1" {
				t.Errorf("%s: %s", p.Name, h)
			}
			set[h.Name] = h.Value
		}
		for _, name := range BackendHints {
			if _, ok := set[name]; !ok {
				t.Errorf("%s: %s is not set", p.Name, name)
			}
		}
		if len(set) != len(BackendHints) {
			t.Errorf("%s: sets %d backend hints, want %d", p.Name, len(set), len(BackendHints))
		}
	}
}

// TestPresetBackends checks which backends each preset enables.
func TestPresetBackends(t *testing.T) {
	for _, tc := range []struct {
		preset string
		on     []string
	}{
		{"xinput", []string{"SDL_XINPUT_ENABLED"}},
		{"rawinput", []string{"SDL_JOYSTICK_RAWINPUT", "SDL_XINPUT_ENABLED"}},
		{"hidapi-off", []string{"SDL_XINPUT_ENABLED", "SDL_JOYSTICK_DIRECTINPUT"}},
		{"wgi", []string{"SDL_JOYSTICK_WGI"}},
		{"gameinput", []string{"SDL_JOYSTICK_GAMEINPUT"}},
	} {
		hints, ok := PresetHints(tc.preset)
		if !ok {
			t.Fatalf("PresetHints(%q) not found", tc.preset)
		}
		var on []string
		for _, h := range hints[1:] {
			if h.Value == "1" {
				on = append(on, h.Name)
			}
		}
		slices.Sort(on)
		want := slices.Sorted(slices.Values(tc.on))
		if !slices.Equal(on, want) {
			t.Errorf("%s enables %q, want %q", tc.preset, on, want)
		}
	}
}

func TestPresetHintsCopy(t *testing.T) {
	h, ok := PresetHints("xinput")
	if !ok {
		t.Fatal("xinput not found")
	}
	h[0].Value = "changed"
	if again, _ := PresetHints("xinput"); again[0].Value != "1" {
		t.Errorf("PresetHints returned the preset's own slice")
	}
	if _, ok := PresetHints("bogus"); ok {
		t.Errorf("PresetHints(bogus) found a preset")
	}
}

func TestPresetUsage(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(PresetUsage(), "\n"), "\n")
	if len(lines) != len(Presets) {
		t.Fatalf("PresetUsage() has %d lines, want %d:\n%s", len(lines), len(Presets), PresetUsage())
	}
	for i, p := range Presets {
		if f := strings.Fields(lines[i]); len(f) < 2 || f[0] != p.Name || !strings.HasSuffix(lines[i], " "+p.About) {
			t.Errorf("line %q, want %s and its description", lines[i], p.Name)
		}
	}
}

func TestHintList(t *testing.T) {
	var l HintList
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&l, "hint", "")
	if err := fs.Parse([]string{"--hint", "A=1", "--hint", "B=x=y"}); err != nil {
		t.Fatal(err)
	}
	if want := (HintList{{"A", "1"}, {"B", "x=y"}}); !slices.Equal(l, want) {
		t.Errorf("hints %v, want %v", l, want)
	}
	if got := l.String(); got != "A=1 B=x=y" {
		t.Errorf("String() = %q", got)
	}
	if err := fs.Parse([]string{"--hint", "bad"}); err == nil {
		t.Error("--hint bad was accepted")
	}
}

func TestParseHint(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Hint
		ok   bool
	}{
		{"SDL_JOYSTICK_HIDAPI=0", Hint{"SDL_JOYSTICK_HIDAPI", "0"}, true},
		{"SDL_JOYSTICK_HIDAPI_PS5=1", Hint{"SDL_JOYSTICK_HIDAPI_PS5", "1"}, true},
		{"A=", Hint{"A", ""}, true},
		{"A=b=c", Hint{"A", "b=c"}, true},
		{"A= x ", Hint{"A", " x "}, true},
		{"SDL_JOYSTICK_HIDAPI", Hint{}, false},
		{"=1", Hint{}, false},
		{"", Hint{}, false},
		{"SDL HINT=1", Hint{}, false},
		{"SDL-HINT=1", Hint{}, false},
		{"SDL_ÄHINT=1", Hint{}, false},
		{"A=1\x002", Hint{}, false},
	} {
		got, err := ParseHint(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseHint(%q) = %v, %v; want %v, ok=%t", tc.in, got, err, tc.want, tc.ok)
		}
	}
	if s := (Hint{"A", "b"}).String(); s != "A=b" {
		t.Errorf("String() = %q", s)
	}
}
