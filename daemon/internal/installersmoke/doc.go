// Package installersmoke tests the built installer end to end:
// a silent install into a folder with spaces and
// Chinese in its name, the files and versions, the daemon from the install
// folder with the SDL3.dll next to it, an upgrade over a running daemon, and
// the uninstall. Its only content is the tests, which skip unless
// PADMATE_TEST_INSTALLER names the installer.
package installersmoke
