// Package injecttest supports the gated tests that inject real key events
// and mouse events (PADMATE_TEST_INJECT=1, Windows only).
//
// Such a test must never let a key reach the foreground application, or a
// wheel notch or a click the window under the cursor (CLAUDE.md invariant
// 6). Start installs a low-level hook that swallows the padmate-tagged
// events whose nonce the test owns, and nothing else, so the physical
// keyboard keeps working. Canary proves that the hook is live before the
// first real key goes out: it injects a harmless key-up of an unassigned VK,
// and fails the test unless the hook saw and swallowed it. InjectFenced then
// injects keys with a canary behind them in the same batch, which tells
// exactly which events the hook saw and proves the hook still live for the
// next batch. A test that injects many batches of its own fences each with
// WaitOwn instead, in O(1): it waits for a count of owned events and fails
// at the first one the hook let through (ErrUnswallowed).
//
// Mouse events follow the same rules with a second hook (ADR-0062). Before
// the first real mouse event, MouseCanary installs a WH_MOUSE_LL hook on the
// Recorder's hook thread and proves it live with a mouse canary: a tagged
// relative move of +1 along x and one of -1, both of which the hook must see
// and swallow, and which leave the cursor where it was even if they get
// through. (A wheel event or a move of no distance never reaches a hook;
// measured 2026-10-19.) A mouse hook sees only the low 32 bits of
// dwExtraInfo, the nonce, not the tag, so the mouse hook queues and swallows
// the injected events with a nonce the Recorder owns, and NonceBase keeps
// the nonces clear of what other injectors send; the physical mouse passes
// through it untouched. InjectFencedMixed injects a batch that has mouse
// events with the canaries behind it in one SendInput call, and InjectFenced
// refuses such a batch.
//
// A pointer move (ADR-0062, section 5) is not held, but a move that got past
// the hook would take the cursor somewhere else. InjectFencedMoves injects
// relative moves only in batches that add up to no distance and never take the
// cursor more than a few pixels (MaxPointerExcursion) from where it began, in
// one SendInput call, and swallows them all (UnpairedMoves refuses any other
// batch before it sends anything; an absolute jump is never injected). Pairing
// limits the damage if a move leaked; it does not promise that the cursor comes
// back, since the system scales relative moves (pointer speed, "enhance pointer
// precision") and clamps them at the edge of the screen. The hook
// reports a move of a batch and a move of the mouse canary alike, as
// WM_MOUSEMOVE with a nonce, so the moves carry a nonce of their own and the
// canary behind them another, and each nonce serves one batch.
// InjectFencedMixed refuses pointer moves for the same reason.
//
// A mouse button, unlike a wheel notch, is held until it is released, so a
// button press that nothing releases would be a button held down on the
// machine the test runs on. A test never injects a button press on its own:
// each press goes out with its release in the same InjectFencedMixed batch,
// which refuses any batch that leaves a button down, releases one that is not
// down or presses one twice (unpairedButton) before sending anything. The
// hook swallows both, so the application sees no click. ButtonMessage and
// ButtonProblems say what the mouse hook must report for a button event.
//
// A typed text (OUT-21, ADR-0070) is Unicode events: KEYEVENTF_UNICODE, which
// the system turns into VK_PACKET keys carrying a UTF-16 unit, and which would be
// characters typed into the foreground window if the hook let them through. The
// hook swallows them by the tag and the nonce like any key, but that Windows
// hands the hook the tag of such an event is proven, not assumed: before the first
// press of a unit, UnicodeCanary injects the release of one, which no application
// types, and stops the test unless the hook saw it with the tag and swallowed it.
// InjectFenced and InjectFencedMixed refuse a batch with a Unicode press until
// UnicodeCanary has succeeded (UnicodeLive, as MouseLive is for the mouse), and a
// batch that leaves a unit pressed or presses it twice (unpairedUnicode), and
// UnicodeProblems says what the hook must report for a Unicode event.
//
// The Backstop and the Probe watch the keyboard only.
//
// StartProbe runs padmate-probe, which BuildTool builds, as the hook that
// swallows and records, in a process of its own; Probe.Canary proves it
// live the same way, and a Backstop, a Recorder installed before it,
// swallows and reports what it lets through. BuildTool is the one part
// that is not only for Windows.
//
// Only tests import this package.
package injecttest
