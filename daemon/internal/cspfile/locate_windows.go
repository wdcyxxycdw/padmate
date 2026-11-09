package cspfile

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// SystemRoots fills Roots from the user's known folders. A folder that
// cannot be resolved is left empty.
func SystemRoots() Roots {
	get := func(id *windows.KNOWNFOLDERID) string {
		p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return ""
		}
		return p
	}
	return Roots{
		AppData:      get(windows.FOLDERID_RoamingAppData),
		Documents:    get(windows.FOLDERID_Documents),
		LocalAppData: get(windows.FOLDERID_LocalAppData),
	}
}

// FindSystemDataDirs is FindDataDirs on this user's known folders.
func FindSystemDataDirs() []DataDir { return FindDataDirs(SystemRoots()) }

// uninstallKeys are the registry keys whose subkeys are uninstall entries.
var uninstallKeys = []struct {
	root registry.Key
	path string
}{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// FindInstallDir finds CSP's installation from the uninstall entries in the
// registry (read only): an entry whose DisplayName starts with
// "CLIP STUDIO PAINT" and whose DisplayIcon or InstallLocation leads to CLIPStudioPaint.exe
// (InstallFromEntry).
func FindInstallDir() (InstallDir, error) {
	for _, k := range uninstallKeys {
		parent, err := registry.OpenKey(k.root, k.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := parent.ReadSubKeyNames(-1)
		parent.Close()
		for _, name := range names {
			sub, err := registry.OpenKey(k.root, k.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			display, _, derr := sub.GetStringValue("DisplayName")
			loc, _, _ := sub.GetStringValue("InstallLocation")
			icon, _, _ := sub.GetStringValue("DisplayIcon")
			sub.Close()
			if derr != nil {
				continue
			}
			if in, ok := InstallFromEntry(display, loc, icon); ok {
				return in, nil
			}
		}
	}
	return InstallDir{}, errors.New("cspfile: CLIP STUDIO PAINT installation not found")
}
