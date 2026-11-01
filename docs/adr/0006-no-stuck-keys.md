# ADR-0006: No stuck keys: dry run and injection, pause, system suspend, the held-keys log, offline release

[中文](0006-no-stuck-keys.zh-CN.md)

Related: [ARCHITECTURE §5](../ARCHITECTURE.md#5-threading-model), [§8.2](../ARCHITECTURE.md#82-output-arbiter)

## Context

Invariant 1 requires every exit path to release every key. This record covers the layers that implement it, each against one kind of threat: a mistyped command sends real keys to the foreground; a stuck slot or a router that falls behind must still release at once; while the screen is locked or on the secure desktop the controller still sends events, while session and power messages may be lost or arrive out of order; a process that is killed takes no exit path; and there has to be a way out when IPC is not available.

## Decision

1. **Live mode needs an explicit `--dry-run` or `--inject`.** Writing neither, or both, is a usage error. The packaged UI writes `--inject` for the user; development and tests stay on dry run. Only one place in the code builds the injecting Sender, and tests and lint keep it that way.
2. **Pause is a state; it belongs to the router and is not saved.** Pause = the Arbiter's gate (no new key goes down; Up and ReleaseAll work as usual) plus the engine's pause mode. Resuming presses nothing.
3. **The emergency combo is detected on T-input, which tracks its members itself.** It is checked before events enter the channel, so it sees events that a full channel drops. When it fires, T-input first closes the gate and releases every key, and only then tells the router; the release does not go through the slot loop or the router. Any controller can fire it, without confirming or taking over a slot. The combo is a setting of each controller (`devices.<DeviceKey>.panic`, [Settings and start at sign-in](0017-settings-and-startup.md)); the default, by family, is back+start held for 2 s for a standard controller.
4. **System events suspend mapping.** A user pause and a system suspend are two owners of the gate, and neither lifts the other. T-session is a hidden top-level window that receives session, power and end-of-session messages, with a desktop-switch hook and a timer besides.
   - A signal that sets a reason (lock, disconnect, sleep, end of session) closes the gate and releases at once, in the window procedure.
   - A signal that clears a reason (unlock, wake) is not trusted as such: the state is checked again, and the timer corrects missed messages.
   - Only the router opens the gate, after a barrier, so that no slot presses something from before the suspend.
   - Sleep and end of session wait for the release to go out (up to 1 s) and are not vetoed.
5. **Quarantining held controls after waking.** For a key held across sleep, SDL first sends a synthesized up, and the down of the original hold arrives later and would press the key again by mistake. So when a suspend starts, the router quarantines every active control: a quarantined control is not mapped, and neither an up nor an empty snapshot lifts the quarantine; it needs an active state after resuming, then a release.
6. **The held-keys log and the startup sweep.** Before a key goes down, the held keys are written to `held.bin`, a bitmap file outside the process, and the next start releases them. Bits are only added before the press and only cleared after the send. The sweep runs on T-output: it intersects the log with `GetAsyncKeyState` and sends one release for each key. A failure does not stop the start.
7. **Only a log of the same sign-in session is swept** (session ID, LUID and sign-in time all equal). Otherwise the log is reset and no key is released, so that keys the user is holding right now are not released.
8. **Offline emergency release.** `padmatectl release-all` does not use IPC; it signals the named event `Local\padmate.panic` in the session. The event is auto-reset, and only a padmated with `--inject` listens (a dry run listening would steal the signal). Access is granted to the current user explicitly, so both elevated and normal terminals can signal it.

## Costs

- A control held across a pause, a suspend or waking must be released and pressed again; resuming presses nothing. The trade-off of quarantine: for a control pressed during a suspend that sent no event, a late down cannot be told from a new press.
- Two controllers pressing the emergency combo at once toggle twice.
- The first press after a suspend ends, before the barrier completes, is dropped; missed messages take from a few seconds up to 60 s to be corrected.
- The held log costs one or two more WriteFile calls per batch (about 10 µs); a second `--inject` instance has no log.

## Alternatives given up

- **Inject by default, or dry-run by default.** The first sends keys on a mistyped command; with the second, a forgotten `--inject` looks like it works but presses nothing.
- **Detect the combo on the router, or ask SDL.** The router does not see dropped events, and its release comes late when it is stuck; replays have no SDL.
- **Pause as a barrier job; suspend and pause sharing one state.** A job has no order with the edges; a shared state would let the user's resume lift a lock-screen suspend.
- **SetConsoleCtrlHandler or an HWND_MESSAGE window.** They do not receive sign-out, shutdown or broadcasts.
- **Veto sleep and sign-out.** It would stop the user from shutting down.
- **Messages only, or polling only.** A missed unlock would keep mapping suspended; polling cannot see sleep, and its release comes late.
- **Open the gate as soon as a suspend ends, or press again on waking.** The first lets a slot press something from before the suspend; with the second the user may no longer want the press.
- **Write the held log in batches, use a temporary file and rename, or refuse to start when the sweep fails.** With batches, a held key is missing from the log for a while; rename eats the latency margin; a bad log would keep `--inject` from starting.
- **A manual-reset event for offline release, or waiting for IPC.** Someone would have to reset it; it has to work when IPC is not available.
