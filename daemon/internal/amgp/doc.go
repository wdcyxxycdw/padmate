// Package amgp imports an AntiMicroX profile (.amgp, PRO-21, ADR-0072) as a
// padmate profile.
//
// Import takes the bytes of the file and returns the text of a profile.json,
// which always passes profile.Parse, and a report of what could not be
// carried over. The package is pure: no platform dependencies, no clock, no
// file access, so that it runs on every system and under fuzzing.
//
// The file is untrusted. It is read with a size limit (MaxBytes), a depth
// limit and a limit on the number of elements; a DOCTYPE, and with it every
// entity but the five of XML, is refused; a UTF-8 byte order mark is skipped.
// Only the root gamecontroller with a configversion of 6 or more is
// imported (joystick roots name raw buttons, and older files store native key
// codes). Nothing in the file is ever executed or opened: execute and
// loadprofile slots are reported and dropped.
//
// What is written in the file is not the whole story, because AntiMicroX
// writes only what differs from its defaults. A missing element therefore
// means AntiMicroX's default, which is not padmate's (the dead zone of a stick
// is 8000 of 32767 there, 0.15 here); the importer writes the AntiMicroX
// values out whenever they differ from padmate's, and always for the sticks
// and triggers.
//
// The file format was read from AntiMicroX's source (GPL-3.0) and from sample
// files; no code was copied. The key code table is Qt's (qnamespace.h of Qt
// 6), which is what AntiMicroX stores from configversion 6 on.
package amgp
