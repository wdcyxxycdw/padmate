// Package ownwindow verifies the Electron UI process and reads its foreground
// identity. Executable names alone never establish ownership.
package ownwindow

import "strings"

// TrustedPaths binds the UI and bridge to this daemon's installation. Windows
// paths are compared case-insensitively; queried image paths are canonical OS
// paths, not user-supplied processName values.
func TrustedPaths(daemon, ui, bridge string) bool {
	clean := func(p string) string { return strings.ToLower(strings.ReplaceAll(p, "/", `\`)) }
	daemon, ui, bridge = clean(daemon), clean(ui), clean(bridge)
	i := strings.LastIndex(daemon, `\`)
	if i < 0 {
		return false
	}
	dir := daemon[:i]
	if bridge != dir+`\padmatectl.exe` {
		return false
	}
	if strings.HasSuffix(dir, `\resources\bin`) {
		root := strings.TrimSuffix(dir, `\resources\bin`)
		return ui == root+`\padmate.exe`
	}
	if strings.HasSuffix(dir, `\bin`) {
		root := strings.TrimSuffix(dir, `\bin`)
		root = strings.TrimSuffix(root, `\.cache\m10-e2e`)
		return ui == root+`\app\node_modules\electron\dist\electron.exe`
	}
	if strings.HasSuffix(dir, `\.cache\ui-bin`) {
		root := strings.TrimSuffix(dir, `\.cache\ui-bin`)
		return ui == root+`\app\node_modules\electron\dist\electron.exe`
	}
	return ui == dir+`\padmate.exe`
}
