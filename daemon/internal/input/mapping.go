package input

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
)

// Custom gamepad mappings (IN-18, ADR-0072). A mapping is an SDL mapping
// string, "GUID,name,a:b0,…": it makes SDL read a joystick that it does not
// know as a gamepad, or reads a known one differently.
//
// Facts of SDL 3.4.16 that this file relies on (docs/PITFALLS.md, "SDL 与
// 手柄"):
//
//   - A mapping applies to every device with its GUID, not to one device.
//   - SDL_AddGamepadMapping takes effect at once, with no reopening: the
//     joystick becomes a gamepad (EVENT_GAMEPAD_ADDED), a gamepad stays one
//     (EVENT_GAMEPAD_REMAPPED) or the other way round (EVENT_GAMEPAD_REMOVED).
//   - Only SDL_ReloadGamepadMappings takes a mapping away again; it takes every
//     one added at run time, so the ones that stay are added again.
//   - The priority of a mapping is the built-in database, then what a program
//     adds (and the file of SDL_GAMECONTROLLERCONFIG_FILE), then the user's
//     SDL_GAMECONTROLLERCONFIG, which a program cannot override. The
//     mapping that SDL reads back says which one won.

// mappingSet holds the mappings of a Source by GUID (32 lowercase hex).
type mappingSet map[string]sdlmapping.Mapping

// parseMappingSet parses the mapping strings of list. Two mappings for one
// GUID that bind differently are an error; the same mapping twice is not.
func parseMappingSet(list []string) (mappingSet, error) {
	set := make(mappingSet, len(list))
	for _, text := range list {
		m, err := sdlmapping.Parse(text)
		if err == nil {
			err = m.Validate()
		}
		if err != nil {
			return nil, fmt.Errorf("input: the mapping %q: %w", clip(text), err)
		}
		if old, ok := set[m.GUID]; ok && !sdlmapping.Equivalent(old, m) {
			return nil, fmt.Errorf("input: two different mappings for the GUID %s", m.GUID)
		}
		set[m.GUID] = m
	}
	return set, nil
}

// clip shortens a mapping string for an error message.
func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// guids returns the GUIDs of set in order.
func (m mappingSet) guids() []string {
	out := make([]string, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	slices.Sort(out)
	return out
}

// dbText returns the mappings as the lines of an SDL mapping database file.
func (m mappingSet) dbText() string {
	var b strings.Builder
	b.WriteString("# padmate custom gamepad mappings (IN-18); rewritten by padmated\n")
	for _, g := range m.guids() {
		b.WriteString(m[g].DBLine())
		b.WriteByte('\n')
	}
	return b.String()
}

// createMappingFile makes the file that SDL reads the mappings from at its
// start (SDL_GAMECONTROLLERCONFIG_FILE) and returns its path. Mappings loaded
// that way are in place before SDL enumerates the devices, so a joystick that
// has a mapping is a gamepad from its first report.
func createMappingFile(set mappingSet) (string, error) {
	f, err := os.CreateTemp("", "padmate-mappings-*.txt")
	if err != nil {
		return "", fmt.Errorf("input: mapping file: %w", err)
	}
	path := f.Name()
	_, werr := f.WriteString(set.dbText())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("input: mapping file: %w", werr)
	}
	return path, nil
}

// MappingStatus is what SDL says of one device after SetMappings, for the
// devices whose GUID has a mapping.
type MappingStatus struct {
	Device DeviceID
	GUID   string
	// Gamepad is true when SDL reads the device as a gamepad now.
	Gamepad bool
	// Applied is true when the mapping that SDL reads back for the device binds
	// what the wanted mapping binds. It is false when something of a higher
	// priority wins (a user's SDL_GAMECONTROLLERCONFIG), or SDL did not take it.
	Applied bool
	// Got is the mapping SDL reads back (SDL_GetGamepadMappingForID), "" for a
	// device that is no gamepad.
	Got string
}

// MappingReport is the result of SetMappings.
type MappingReport struct {
	Devices []MappingStatus
}

// SetMappings makes the custom mappings be exactly mappings, SDL mapping
// strings, one for each GUID at most: it adds the new and changed ones to SDL
// at once, and when one was dropped it has SDL load its database again, which
// takes every mapping added at run time, and adds the remaining ones back.
// With a mapping file (Options.Mappings was not nil) the file is rewritten
// first, so SDL's own reload finds the same. Nothing is changed when a
// mapping is invalid.
//
// The devices whose GUID has a mapping are read back (MappingReport).
// SDL's reports follow as events: DeviceAdded for a joystick that became a
// gamepad, DeviceRemoved and DeviceAdded for a gamepad that became a joystick,
// DeviceRemapped for one that stayed a gamepad with another mapping. SDL
// loading its database again remaps every gamepad that is open, but one whose
// mapping reads the same afterwards is not reported (Source.remapped).
func (s *Source) SetMappings(mappings []string) (MappingReport, error) {
	next, err := parseMappingSet(mappings)
	if err != nil {
		return MappingReport{}, err
	}
	var rep MappingReport
	err = s.do(func() error {
		var err error
		rep, err = s.applyMappings(next)
		return err
	})
	return rep, err
}

// applyMappings is SetMappings on T-input.
func (s *Source) applyMappings(next mappingSet) (MappingReport, error) {
	prev := s.mappings
	dropped := false
	var changed []string
	for _, g := range prev.guids() {
		if _, ok := next[g]; !ok {
			dropped = true
		}
	}
	for _, g := range next.guids() {
		if old, ok := prev[g]; !ok || old.String() != next[g].String() {
			changed = append(changed, g)
		}
	}
	if s.mapFile != "" && (dropped || len(changed) > 0) {
		if err := os.WriteFile(s.mapFile, []byte(next.dbText()), 0o600); err != nil {
			return MappingReport{}, fmt.Errorf("input: mapping file: %w", err)
		}
	}
	if dropped {
		if err := sdl.ReloadGamepadMappings(); err != nil {
			s.restoreMappingFile(prev)
			return MappingReport{}, fmt.Errorf("SDL_ReloadGamepadMappings: %w", err)
		}
		s.mappings = mappingSet{} // SDL has none of ours until they are added back
		for _, g := range next.guids() {
			if err := s.ensureMapping(next[g]); err != nil {
				return MappingReport{}, err
			}
			s.mappings[g] = next[g]
		}
	} else {
		applied := maps.Clone(prev)
		if applied == nil {
			applied = mappingSet{}
		}
		for _, g := range changed {
			if err := sdl.AddGamepadMapping(next[g].String()); err != nil {
				s.mappings = applied
				s.restoreMappingFile(applied)
				return MappingReport{}, fmt.Errorf("SDL_AddGamepadMapping: %w", err)
			}
			applied[g] = next[g]
		}
	}
	s.mappings = next
	return s.mappingReport(next), nil
}

// restoreMappingFile writes set back to the mapping file, after a change that
// SDL refused.
func (s *Source) restoreMappingFile(set mappingSet) {
	if s.mapFile != "" {
		_ = os.WriteFile(s.mapFile, []byte(set.dbText()), 0o600)
	}
}

// ensureMapping makes sure that SDL has m after it loaded its database
// again: it does when the file hint brought it back and a device with the
// GUID reads it, and otherwise m is added. Adding a mapping that SDL has
// already is not harmless: SDL remaps the devices of the GUID again.
func (s *Source) ensureMapping(m sdlmapping.Mapping) error {
	for _, d := range s.devices {
		if g, ok := deviceGUID(d.id); ok && g.String() == m.GUID {
			if got := d.id.GamepadMapping(); got != "" && sdlmapping.Equivalent(readBack(got), m) {
				return nil
			}
		}
	}
	if err := sdl.AddGamepadMapping(m.String()); err != nil {
		return fmt.Errorf("SDL_AddGamepadMapping: %w", err)
	}
	return nil
}

// readBack parses the mapping that SDL gives for a device, leniently: only
// its bindings matter.
func readBack(text string) sdlmapping.Mapping {
	m, err := sdlmapping.Parse(text)
	if err != nil {
		return sdlmapping.Mapping{Fields: sdlmapping.LenientFields(text)}
	}
	return m
}

// mappingReport reads back the mapping of every device whose GUID is in set.
func (s *Source) mappingReport(set mappingSet) MappingReport {
	var rep MappingReport
	for _, d := range s.devices {
		g, ok := deviceGUID(d.id)
		if !ok {
			continue
		}
		want, ok := set[g.String()]
		if !ok {
			continue
		}
		got := d.id.GamepadMapping()
		rep.Devices = append(rep.Devices, MappingStatus{
			Device: d.pub, GUID: g.String(), Gamepad: got != "",
			Applied: got != "" && sdlmapping.Equivalent(readBack(got), want), Got: got,
		})
	}
	slices.SortFunc(rep.Devices, func(a, b MappingStatus) int { return int(a.Device) - int(b.Device) })
	return rep
}

// GamepadMapping returns the mapping that SDL reads for device id
// (SDL_GetGamepadMappingForID), "" when SDL reads it as no gamepad.
func (s *Source) GamepadMapping(id DeviceID) (string, error) {
	var text string
	err := s.do(func() error {
		d := s.byPub(id)
		if d == nil {
			return fmt.Errorf("%w: %d", ErrNoDevice, id)
		}
		text = d.id.GamepadMapping()
		return nil
	})
	return text, err
}

// applyInitialMappings adds the mappings of Options.Mappings once SDL is
// up, for those it did not load from the file: when the file hint could not
// be set because the environment has its own (SDL_GAMECONTROLLERCONFIG_FILE),
// the mappings are added through the API. It runs on T-input in init.
func (s *Source) applyInitialMappings() error {
	if len(s.mappings) == 0 || (s.mapFile != "" && sdl.GetHint(sdl.HINT_GAMECONTROLLERCONFIG_FILE) == s.mapFile) {
		return nil
	}
	for _, g := range s.mappings.guids() {
		if err := sdl.AddGamepadMapping(s.mappings[g].String()); err != nil {
			return fmt.Errorf("SDL_AddGamepadMapping: %w", err)
		}
	}
	return nil
}
