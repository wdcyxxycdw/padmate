package installerpkg

// GUID names the installer's registry keys. It is the nsis.guid of
// app/electron-builder.yml (config_test.go keeps the two equal) and must
// never change: an install under another GUID is not upgraded, and the
// uninstaller of the old one is not found.
const GUID = "6e1c5b0a-3f7d-4c8e-9a52-7d0b1f4c2e83"

// Registry keys of an installation under HKCU, which per-user installs use.
const (
	// UninstallKey is the entry of Settings > Apps.
	UninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + GUID
	// InstallKey holds InstallLocation and the shortcut names.
	InstallKey = `Software\` + GUID
	// RunKey is the user's real login startup key, where the installer and
	// uninstaller look for the value of padmate (app/build/installer.nsh).
	RunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	// TestRunKey is where the installer and uninstaller look for the login
	// startup value of padmate when PADMATE_INSTALLER_TEST_ROOT is set, in
	// place of the real Run key.
	TestRunKey = `Software\padmate-installer-test\Run`
)

// TestRootEnv names a folder; set in the environment of the installer and
// uninstaller, it moves everything they clean up into that folder (see
// app/build/installer.nsh). For tests only.
const TestRootEnv = "PADMATE_INSTALLER_TEST_ROOT"
