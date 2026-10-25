package dtsgen

import (
	"fmt"
	"slices"
	"strings"
)

// SupportedAPIs lists the script API versions this daemon runs, the current
// one first. The policy (M14, docs/SCRIPTING.md §6) is that a release
// supports the current API and the previous 0.N, so a script written for the
// last release still loads after an upgrade: when the API moves on this list
// becomes {new, previous}, and a shim keeps the members of the previous
// version that the new one changes. The API follows the release whose
// milestone changes it (script.api 0.N with padmate v0.N.0), so the previous
// version is the last one an API change shipped, not N-1: 0.16 (M16, the HUD
// API) followed 0.8, as 0.9 to 0.15 kept the API, 0.17 (M17, the rumble and
// LED members of pad) follows 0.16, and 0.21 (M21, key.sequence and key.type and
// the MediaKey names) follows 0.17, as 0.18 to 0.20 kept it. All of them only add
// members, so the previous version needs no shim; the release that dropped 0.8
// is the one that made 0.17 current, and the one that dropped 0.16 the one that
// made 0.21.
//
// Keep in step with this list: the enum of script.api in
// schema/profile.schema.json (TestSchemaScriptAPIMatchesSupportedAPIs), and
// host.api in internal/script/host.go (checked by that package's tests).
var SupportedAPIs = []Version{{Major: 0, Minor: 21}, {Major: 0, Minor: 17}}

// Supported reports whether api is one of SupportedAPIs.
func Supported(api Version) bool { return slices.Contains(SupportedAPIs, api) }

// SupportedNames returns SupportedAPIs as "major.minor" texts, the current
// one first: the values script.api and "padmatectl script init --api" take.
func SupportedNames() []string {
	names := make([]string, len(SupportedAPIs))
	for i, v := range SupportedAPIs {
		names[i] = v.String()
	}
	return names
}

// supportedText lists SupportedAPIs for an error message.
func supportedText() string {
	names := SupportedNames()
	if len(names) == 1 {
		return "only " + names[0]
	}
	return strings.Join(names, " and ")
}

// StableMembers are the values of the "padmate" module that form the stable
// subset of the script API (M14): from this release on they keep their name,
// shape and behavior in every supported API version, so a script that only
// uses them keeps working across the current and the previous 0.N. The
// promise covers the members' own properties and methods (for example
// key.tap, pad.isDown) and the types they use (Control, KeySpec,
// InputEvent, ...), and the padmate/math module. The members a macro can use
// (the @macro marks in padmate.d.ts, SCR-50) are always in it.
//
// The core the documentation names (SCRIPTING §6) is on, key, layers and
// pad; the rest are the definitions scripts are built from, the input and
// time functions and the session store.
var StableMembers = []string{
	"host",
	"defineScript", "defineAction", "param",
	"pad",
	"on", "claim", "global", "layer", "observe", "onAxis", "onStick", "onTick",
	"layers",
	"key",
	"now", "sleep", "every", "session", "log",
}

// PreviewMembers are the values of the current API that the policy does not
// promise yet: the environment API (app, profiles) came with 0.8 and depends
// on the library and on the foreground detection; hud came with 0.16 and
// depends on the overlay. A change to them may come with the next 0.N without
// a shim. A member moves to StableMembers when it has been through a release
// unchanged.
var PreviewMembers = []string{
	"app", "profiles", "hud",
}

// ValueMembers returns the names of the values (const, function, class...)
// the "padmate" module declares for api, in declaration order: what
// StableMembers and PreviewMembers have to cover.
func ValueMembers(api Version) ([]string, error) {
	return valueMembers(string(defaultDeclarations), api)
}

func valueMembers(src string, api Version) ([]string, error) {
	filtered, err := FilterString(src, api)
	if err != nil {
		return nil, err
	}
	body, err := moduleBody(filtered, "padmate")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(body, "\n") {
		// A JSDoc may share the line with the declaration it documents.
		if i := strings.LastIndex(line, "*/"); i >= 0 {
			line = line[i+2:]
		}
		if m := valueDeclaration.FindStringSubmatch(line); m != nil && !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return names, nil
}

// errUnsupported is ValidateHostAPI's error for api.
func errUnsupported(api Version) error {
	return fmt.Errorf("script API %s is incompatible with host API %s; this release supports %s", api, HostAPI, supportedText())
}
