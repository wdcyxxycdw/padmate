// Command spk01 is the SPK-01 spike: padmated's input side in its real
// shape, to check SDL's joystick backends, background input and the CPU
// cost of T-input.
//
// `task spk01` builds it with -H=windowsgui into bin/spk01.exe, next to
// the pinned SDL3.dll, so it has no console, like the release padmated.
// Its main thread runs the fyne.io/systray message loop (a tray icon with
// a Quit item), as padmated's will (M7); T-input is internal/input.Open
// on its own locked OS thread. The process has no window that can take
// the focus, so every event it receives is a background event.
//
//	spk01 [--preset NAME] [--hint NAME=VALUE]... [--duration D]
//	      [--log FILE] [--virtual-rate HZ]
//
// It waits 500ms for the connected devices, records every event for
// --duration (default 10s), optionally while it drives an SDL virtual
// gamepad at --virtual-rate state changes per second, and exits by
// itself. It then writes a summary: the event count, the dropped count
// and the CPU time of the process (GetProcessTimes) divided by the wall
// time. --log FILE gets JSON lines: a header (SDL version, library, hints
// in effect, threads, devices), one line per event and the summary. The
// summary also goes to stderr, which a GUI program only has when it is
// redirected.
//
// It exits 0, 1 when events were dropped, the virtual gamepad's events
// did not all arrive or anything failed, and 2 on bad flags. It is only
// supported on Windows; elsewhere it says so and exits 1.
package main
