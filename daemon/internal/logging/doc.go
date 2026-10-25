// Package logging is padmated's log (DMN-03): JSON lines through
// log/slog, to stderr or to a file (ARCHITECTURE §3), and a Limiter for
// errors that repeat.
//
// Attribute names, shared by the supervisor and padmated: slot (the slot
// index), device (the DeviceKey string), id (the DeviceID), name (the
// device name), gen (the profile generation), err, stack (of a panic),
// dropped (input events lost), suppressed (records a Limiter held back),
// skipped (edges a slot that fell behind missed), reconnected, backoff
// (before a restart), panics and window (of a disabled slot); padmated
// adds version, profile (the path), mode ("dry-run", "inject" or
// "replay"), preset (the SDL hint preset), code (the exit code), spec,
// hkl and reason (of a key sent as its US key), and at a reload file (the
// profile watched, as package watch logs it), errors and warnings (the
// numbers of each) and first (the first error, "line:col: message");
// input.Source's receiver watchdog (IN-30) adds path (the HID path),
// reports, still and other (report counts), ids (reports by ID, such as
// "30:118,21:0,3f:0,81:2,other:0"), timer and status (bytes 1 and 2 of
// the last full report; status as "0x90"), silent, since, maxGap and
// stalled (durations), armed, dormant, granted (of a grace) and replaces
// (the id a device had before it came back). Durations are strings such
// as "100ms". Logs never carry window titles, and key events go only at
// debug level.
//
// The package is cross-platform. It is not pure: Open creates files.
package logging
