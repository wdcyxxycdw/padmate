// Package cspfile is the file side of writing CLIP STUDIO PAINT (CSP)
// settings safely (ADR-0019 section 5): finding CSP's folders, telling
// whether CSP runs, backing the settings files up, replacing them
// atomically and restoring a backup.
//
// It knows nothing about the SQLite or khc formats. The caller edits a
// copy of the file inside the edit callback of Update; this package makes
// sure the original is only replaced when nothing changed under it, CSP is
// still closed and the copy was edited without error.
//
// Nothing here reads window titles or touches CSP's process. The Windows
// parts (known folders, registry, process snapshot, ReplaceFileW) live in
// _windows.go files; the rest builds and tests on Linux.
package cspfile

import "errors"

// ErrUnsupported is returned by the functions that need Windows when they
// run on another platform.
var ErrUnsupported = errors.New("cspfile: not supported on this platform")
