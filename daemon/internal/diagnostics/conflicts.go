// Package diagnostics reports input and desktop compatibility information.
// It never modifies other programs or the system configuration.
package diagnostics

import (
	"slices"
	"strings"
)

// ConflictingProcesses identifies common remappers by executable basename.
// Presence is a troubleshooting hint, not proof that a device is intercepted.
func ConflictingProcesses(names []string) []string {
	seen := make(map[string]bool)
	for _, name := range names {
		switch strings.ToLower(name) {
		case "steam.exe":
			seen["Steam"] = true
		case "ds4windows.exe":
			seen["DS4Windows"] = true
		case "hidhide.exe", "hidhideclient.exe", "hidhideconfigclient.exe", "hidhidecli.exe":
			seen["HidHide"] = true
		}
	}
	names = nil
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
