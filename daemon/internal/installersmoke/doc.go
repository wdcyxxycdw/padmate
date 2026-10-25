// Package installersmoke tests the built installer end to end (SPK-07,
// PKG-03, PKG-04, PKG-05): a silent install into a folder with spaces and
// Chinese in its name, the files and versions, the daemon from the install
// folder with the SDL3.dll next to it, an upgrade over a running daemon, and
// the uninstall. Its only content is the tests, which skip unless
// PADMATE_TEST_INSTALLER names the installer; see docs/build-installer.md.
package installersmoke
