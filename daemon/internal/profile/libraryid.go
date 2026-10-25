package profile

import "strings"

// ValidLibraryID accepts portable canonical IDs for profiles and their rules.
func ValidLibraryID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i, c := range id {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '-' || c == '_') {
			continue
		}
		return false
	}
	switch id {
	case "con", "prn", "aux", "nul":
		return false
	}
	if len(id) == 4 && (strings.HasPrefix(id, "com") || strings.HasPrefix(id, "lpt")) && id[3] >= '0' && id[3] <= '9' {
		return false
	}
	return true
}

// HistoryDir is the hidden folder in a library profile's folder that keeps
// earlier versions of its profile.json (PRO-17). It is not a valid library
// ID, the library never copies it, and the script compiler neither hashes
// it nor accepts an entry or import in it, although it is inside the
// script root.
const HistoryDir = ".history"

// IsHistoryDir reports whether name, one element of a path in a profile's
// folder, names HistoryDir. Windows finds the folder whatever the case of
// the name, so the name is compared case-insensitively on every platform.
func IsHistoryDir(name string) bool { return strings.EqualFold(name, HistoryDir) }
