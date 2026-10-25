package input

import (
	"errors"
	"fmt"
	"strings"
)

// Hint is one SDL hint: a name from SDL_hints.h and its value.
type Hint struct {
	Name, Value string
}

// String returns "NAME=VALUE".
func (h Hint) String() string { return h.Name + "=" + h.Value }

// The SDL 3.4.16 hint names padmate sets or reads back
// (include/SDL3/SDL_hints.h).
const (
	// HintBackgroundEvents lets joystick events through while the process
	// has no focused window. SDL only drops them when the process has SDL
	// windows and none of them is focused, but padmate always sets it.
	HintBackgroundEvents = "SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS"
	// HintNoSignalHandlers keeps SDL_Init from installing SIGINT and
	// SIGTERM handlers (src/events/SDL_quit.c), which turn the signals
	// into SDL_EVENT_QUIT. padmate handles the signals with os/signal. On
	// Windows the handler that SDL installs with the C runtime's signal()
	// also sits in front of Go's console control handler and takes Ctrl+C,
	// so Go would never see it.
	HintNoSignalHandlers = "SDL_NO_SIGNAL_HANDLERS"
	// HintCombineJoyCons is set to "0" by Open, after the two hints above
	// and before any other (IN-07, ADR-0022): each Joy-Con is its own
	// gamepad, whether or not its partner is connected. A lone Joy-Con is
	// a gamepad under either value; with "1", which
	// "--hint SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1" restores, SDL
	// replaces the two by one pair device when both are connected
	// (SDL_hidapijoystick.c, HIDAPI_CreateCombinedJoyCons).
	HintCombineJoyCons = "SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS"
	// HintVerticalJoyCons is not set by padmate, so SDL's default "0"
	// (sideways) applies. SDL reads it when it opens a Joy-Con
	// (SDL_hidapi_switch.c). Source reads it back so that headers record
	// it.
	HintVerticalJoyCons = "SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS"

	hintHIDAPI      = "SDL_JOYSTICK_HIDAPI"      // default 1
	hintRawInput    = "SDL_JOYSTICK_RAWINPUT"    // default 0
	hintXInput      = "SDL_XINPUT_ENABLED"       // default 1
	hintDirectInput = "SDL_JOYSTICK_DIRECTINPUT" // default 1
	hintWGI         = "SDL_JOYSTICK_WGI"         // default 0
	hintGameInput   = "SDL_JOYSTICK_GAMEINPUT"   // default 0 outside GDK builds
)

// backgroundHint is the first hint of every preset.
var backgroundHint = Hint{HintBackgroundEvents, "1"}

// openHints are set by Open first, before the hints of Options, whatever
// they are. A hint of Options with the same name overrides one of them,
// since SDL keeps the value set last. No preset does, apart from the
// background hint that every preset repeats with the same value
// (TestPresets).
var openHints = []Hint{backgroundHint, {HintNoSignalHandlers, "1"}, {HintCombineJoyCons, "0"}}

// BackendHints are the hints that select SDL's joystick backends. Source
// reads them back after SDL_Init, whether or not they were set.
var BackendHints = []string{hintHIDAPI, hintRawInput, hintXInput, hintDirectInput, hintWGI, hintGameInput}

// JoyConHints are the Joy-Con hints that Source reads back after SDL_Init,
// whether or not they were set (IN-07).
var JoyConHints = []string{HintCombineJoyCons, HintVerticalJoyCons}

// Preset is a named list of hints for the backend experiments (SPK-01).
type Preset struct {
	Name  string
	About string
	Hints []Hint
}

// backends returns the backend hints that enable exactly the backends
// named in on and disable the others.
func backends(on ...string) []Hint {
	hints := []Hint{backgroundHint}
	for _, name := range BackendHints {
		v := "0"
		for _, o := range on {
			if o == name {
				v = "1"
			}
		}
		hints = append(hints, Hint{name, v})
	}
	return hints
}

// Presets are the hint presets, "default" first. Every one sets the
// background hint; all but "default" set every backend hint explicitly,
// so that they do not depend on SDL's defaults.
var Presets = []Preset{
	{"default", "SDL's default backends (HIDAPI, XInput, DirectInput); what padmated uses",
		[]Hint{backgroundHint}},
	{"xinput", "XInput only",
		backends(hintXInput)},
	{"rawinput", "Raw Input, with XInput for trigger correlation",
		backends(hintRawInput, hintXInput)},
	{"hidapi-off", "SDL's defaults without HIDAPI: XInput and DirectInput",
		backends(hintXInput, hintDirectInput)},
	{"wgi", "Windows.Gaming.Input only",
		backends(hintWGI)},
	{"gameinput", "GameInput only",
		backends(hintGameInput)},
}

// PresetHints returns the hints of the preset named name.
func PresetHints(name string) ([]Hint, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return append([]Hint(nil), p.Hints...), true
		}
	}
	return nil, false
}

// PresetUsage lists the presets for a usage text: one line per preset,
// with its name and description, indented by two spaces.
func PresetUsage() string {
	var b strings.Builder
	for _, p := range Presets {
		fmt.Fprintf(&b, "  %-11s %s\n", p.Name, p.About)
	}
	return b.String()
}

// PresetNames returns the preset names, "default" first.
func PresetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}

// ParseHint parses "NAME=VALUE". NAME must be made of ASCII letters,
// digits and underscores; VALUE may be empty. Neither may contain NUL,
// which C strings cannot carry.
func ParseHint(s string) (Hint, error) {
	name, value, ok := strings.Cut(s, "=")
	if !ok {
		return Hint{}, fmt.Errorf("hint %q: want NAME=VALUE", s)
	}
	if err := checkHintName(name); err != nil {
		return Hint{}, fmt.Errorf("hint %q: %w", s, err)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return Hint{}, fmt.Errorf("hint %q: the value contains NUL", s)
	}
	return Hint{name, value}, nil
}

// HintList is a flag.Value for a repeatable NAME=VALUE flag: each Set
// appends the hint that ParseHint returns.
type HintList []Hint

// String returns the hints separated by spaces.
func (l *HintList) String() string {
	s := make([]string, len(*l))
	for i, h := range *l {
		s[i] = h.String()
	}
	return strings.Join(s, " ")
}

// Set parses s with ParseHint and appends the hint.
func (l *HintList) Set(s string) error {
	h, err := ParseHint(s)
	if err != nil {
		return err
	}
	*l = append(*l, h)
	return nil
}

func checkHintName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	for _, r := range name {
		if !isHintNameRune(r) {
			return fmt.Errorf("invalid character %q in the name", r)
		}
	}
	return nil
}

// isHintNameRune reports whether r may appear in a hint name: SDL's hint
// names are ASCII letters, digits and underscores.
func isHintNameRune(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
}
