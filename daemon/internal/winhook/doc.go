// Package winhook installs a WH_KEYBOARD_LL low-level keyboard hook and
// reports every keyboard event the system sees, physical or injected. A hook
// can also install a WH_MOUSE_LL low-level mouse hook beside it
// (EnableMouse), which reports only the mouse events it is told to watch.
//
// The hook runs on its own goroutine, locked to an OS thread that installs
// the hook and then runs a GetMessage loop, as low-level hooks require
// (CLAUDE.md invariant 5). The mouse hook is installed on that same thread,
// when the thread gets the message that asks for it. The callbacks only read
// the counter (latency.Now, Event.Stamp), copy the event, ask an optional
// swallow predicate whether to discard it, and push it into a buffered
// channel without blocking. At most one hook is active per process.
//
// A mouse hook sees every move of the physical mouse, so its Watch predicate
// decides on the hook thread which events are of interest at all; the rest
// pass straight to the next hook, with nothing copied and nothing swallowed.
//
// padmate uses it in padmate-probe (ENG-06) to record what SendInput really
// injected, and in tests that must swallow their own injected keys and mouse
// events so they never reach the foreground application or the window under
// the cursor. Shortcut capture (OUT-15, M11) will reuse it. The Event and
// MouseEvent types build on every OS so that code formatting events can be
// tested on Linux; Start is Windows-only.
package winhook
