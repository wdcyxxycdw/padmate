# padmate architecture

[中文](ARCHITECTURE.zh-CN.md)

This document is for developers taking over the code: processes, threads, package structure, data flow, file formats, IPC, the UI and tests, and the constraints between them. Why it is designed this way is in the [ADRs](adr/README.md); verified facts to know before changing a part of the code are in [PITFALLS.md](PITFALLS.md).

## 0. Decisions at a glance

The reasons, and the alternatives given up, are in the [ADR index](adr/README.md).

| Area | Decision | ADR |
|---|---|---|
| Platform and processes | Windows 10/11 x64 only. The Go daemon `padmated` does all the mapping, lives in the tray and runs as a single instance; the Electron UI is optional, and closing it does not affect mapping | [Processes and stack](adr/0001-processes-and-stack.md) |
| Input | A bundled, pinned SDL 3.4.16 (go-sdl3 + purego); a left and a right Joy-Con together are one controller; Joy-Con 2 is read directly over Bluetooth LE (winrt-go), with no pairing | [Controller input](adr/0002-input-sdl.md) |
| Several controllers | 4 slots built in advance, other controllers queue; a new assignment first stands in, and any button press confirms it | [Several controllers and slots](adr/0003-devices-and-slots.md) |
| Output | SendInput scan codes; events carry a `dwExtraInfo` tag; only T-output calls SendInput | [Keyboard and mouse output](adr/0004-output-sendinput.md) |
| Threads | The slot loops, T-output and T-input each lock an OS thread; the engine is pure | [Engine and threads](adr/0005-engine-and-threads.md) |
| No stuck keys | Every exit path does ReleaseAll; `--dry-run` or `--inject` must be chosen explicitly; pause, suspend and the emergency combo each have a gate; the held-keys log and the offline emergency release are the safety net | [No stuck keys](adr/0006-no-stuck-keys.md) |
| Profiles | Strict JSON tagged unions, `schemaVersion` 1, the daemon is the only writer; stored by ID in the profile library, switched automatically by the foreground exe's path | [Profile format](adr/0007-profile-format.md), [Profile library](adr/0008-profile-library.md) |
| Mapping model | One set of buttons per profile; a single function key, chosen for each controller; sticks split into rotation and press; profiles can inherit | [Mapping model](adr/0012-mapping-model.md) |
| Controller settings | The function key, the feedback switches and the emergency combo are settings of each controller (`devices.<DeviceKey>`), listed and changed in the Controllers view of Settings; settings take effect on save | [Controller feedback](adr/0015-controller-feedback.md), [Settings and start at sign-in](adr/0017-settings-and-startup.md) |
| IPC | JSON-RPC 2.0 over a named pipe, token authentication, an ACL limited to the current user | [IPC](adr/0009-ipc.md) |
| Scripts | goja + esbuild; gesture recognition and output shared with declarative mappings | [Scripting runtime](adr/0010-scripting-runtime.md), [Scripts as macros](adr/0011-script-macros.md) |
| UI and diagnostics | Electron + React; the overlay is a second window | [Editor UI](adr/0013-editor-ui.md), [Overlay](adr/0014-overlay.md), [Diagnostics](adr/0016-diagnostics.md) |
| App integrations | A profile bound to an app (for now only CLIP STUDIO PAINT) records in `integrations/<id>.json` the function of each key in that app; pick functions from the app, find conflicts, write back (app closed, backed up first, can be rolled back), check for differences when the profile becomes active | [App integrations and CLIP STUDIO PAINT](adr/0019-clip-studio-paint.md) |
| Packaging | A per-user NSIS installer; a portable zip besides; updates read `padmate.download`, which has a download page | [Installer and updates](adr/0018-installer-and-updates.md) |

## 1. Principles

1. **No stuck keys, before anything else.** Every exit path (panic, disconnect, switching, locking the screen) does ReleaseAll. After a panic, the level that failed cannot be trusted; it is skipped, and the next level releases from its ledger (§5.16).
2. **The engine is pure.** Time, input and output all go through interfaces, so it can be tested deterministically on Linux.
3. **Output has a single writer.** Inside the daemon only T-output calls SendInput; `padmatectl send` and the injection tests call it directly, outside the process.
4. **Latency is spent only where it is needed**: how long a gesture waits depends only on the bindings that can be reached.
5. **A change of state reaches the user through at least two channels**: the controller, the tray, the overlay.
6. **Output is keyboard shortcuts and the mouse only.** The one app-specific part is app integrations: when a profile is bound to an app (for now only CLIP STUDIO PAINT), padmate reads and writes that app's shortcut settings to pick functions, find conflicts and keep them in sync; the engine and the output are the same for every app (§16).

## 2. Process model

| Process | Role |
|---|---|
| `padmated.exe` | The daemon: input, engine, output, scripts, tray, IPC. Maps up to 4 controllers at once |
| `padmatectl.exe` | The CLI; `send` for development; the UI's authenticated bridge (`ui-bridge`) |
| `padmate.exe` | The optional Electron UI and overlay |
| `padmate-probe.exe` | A developer tool: an LL hook that records injected keys, layout dumps |

Electron's Main connects through a child process, `padmatectl ui-bridge`, to the daemon that `daemon.json` points to; when that fails, it tries to start `padmated` a bounded number of times, and on failure the window says why (§12). The token never reaches the renderer.

## 3. Directory structure

The Go module is in `daemon/` (sources in `cmd/`, `internal/`, `tools/`, `testdata/`): a few npm packages in `app/node_modules` at the root contain `.go` files, which would break `go build ./...` at the root. The UI is in `app/` (electron-vite), the one source of the schemas is `schema/`, and the download page and its Worker are in `site/` (§15.5).

User data: `%APPDATA%\padmate\` holds `config.json`, `state.json`, `profiles\<id>\`, `macros\` (the script library), `logs\` and `startup\` (argument files of start at sign-in); `%LOCALAPPDATA%\padmate\run\` holds `daemon.json` and `held.bin`. The installation folder is `%LOCALAPPDATA%\Programs\padmate\` by default; the programs are in `resources\bin\`, and no user data goes there.

## 4. Package responsibilities and dependencies

These are the packages of `daemon/internal/`. A "pure package" imports neither `x/sys/windows`, `input`, `output`, `script` nor `latency`, does not log, and does not read real time (invariant 2).

- **Pure logic**: `keys`, `control`, `profile` (`Resolve` merges the parent profiles, with a file-reading function given by the caller), `engine`, `clock`, `haptic`, `sdlmapping`, `mapwizard` (the UI implements the same rules in TypeScript, sharing `testdata/vectors.json`), `amgp`, `kblayout`, `watch`, `padlabel`, `padfamily` (the families of controllers: their function keys and built-in emergency combos, ADR-0017), `input/jc2` (the Joy-Con 2 protocol: advertisements, the command allowlist, replies, reports, stick calibration, the control mapping, the silence watchdog; it imports only the standard library, so it builds and tests on Linux, §5.3.1, §7.1.1). Bindings that a `Level` / `Registry` returns do not go through `Compile` and must pass `engine.CheckBinding` first; otherwise a gesture that fires on release would hold keys that nobody releases (§7.7).
- **Packages that deal with Windows and SDL**: `output` (the Arbiter, MenuMask, `Loop` and the layout Resolver are cross-platform; SendInput is in `_windows.go`, §8), `input` (T-input, the SDL backend, the Joy-Con 2 hub, virtual gamepads, device identity, event JSON lines; the engine does not import it), `input/jc2win` (the Windows Runtime transport of Joy-Con 2 over Bluetooth LE, §5.3.1; its `doc.go` is cross-platform, the rest is `_windows.go`), `sdlpin`, `winhook`, `winfocus` (§9), `winsession`, `keycapture`, `ownwindow`, `instance`, `autostart`, `emergency`, `diagnostics`, `tray`, `ipc` (§11), `profilelib` (§10.1), `trace`, `logging`, `latency` (§13; a leaf package that pure packages cannot import). `injecttest` and `winsessiontest` are test tools.
- **Orchestration**: `slot` (ties the engine, scripts and the Arbiter together), `supervisor` (devices to slots, the router, restarts, reloads), `script`, `simtest` (importable only from `_test.go`, §14).

**Import rules** are enforced by depguard and forbidigo, configured in `daemon/.golangci.yml`: pure logic packages must not import Windows-related packages, `input`, `output`, `script`, `latency`, `winsession`, goja, esbuild or logging packages, nor read time directly (`time.Now` and the like); `winsession` must not import the packages of mapping; the whole module is forbidden to call `output.SendInput`, except padmated's `sender_windows.go`, padmatectl's `send_windows.go`, `injecttest`, `output` and tests. The pure logic packages, `slot`, `supervisor`, `simtest` and the non-Windows parts of the other packages compile and test on Linux.

## 5. Threading model

What each thread owns, whom it talks to, and how it releases keys on exit and on panic. The reasons are in [Engine and threads](adr/0005-engine-and-threads.md), [No stuck keys](adr/0006-no-stuck-keys.md) and [Several controllers and slots](adr/0003-devices-and-slots.md).

### 5.1 Threads at a glance, and ownership

| Thread | Locked | Responsibility |
|---|---|---|
| main | yes | `systray.Run` (§5.2) |
| T-input | yes | The SDL event pump, emergency-combo detection, controller effects, the receiver watchdog, the Joy-Con 2 hub (§5.3–§5.5, §5.3.1) |
| jc2win transport | yes | The Windows Runtime side of Joy-Con 2: scanning, connecting, GATT, the allowlisted writes. One goroutine; it hands everything to T-input over a channel (§5.3.1) |
| slot loop ×N | yes | `engine.Step`, script dispatch, timers. One per slot, controller or not; at most 4 (§5.10) |
| script worker | no | esbuild builds, setup of a new runtime, teardown of an old one |
| T-output | yes | `output.Loop`: owns the Arbiter, the only caller of SendInput (§5.9) |
| T-session/focus | yes | `winsession`'s hidden window receives session, power and secure-desktop notifications; `winfocus` watches the foreground on the same thread (§5.13, §9) |
| T-hook | yes | `WH_KEYBOARD_LL`, installed only for shortcut capture, tests and the probe (§5.14) |
| router | no | `supervisor.Run`: reads events, records device state, puts edges into the slot channels, never blocks (§5.7, §5.8). Each slot also has a runner goroutine that restarts it |
| watcher | no | Polls profile files and reloads when they change (§5.18) |

Ownership: the device state belongs to the router; the engine and the script runtime to their slot loop; key state to the Arbiter, and the Arbiter to T-output; other goroutines hand it commands through `Do` or `RemoteArbiter`; `haptic.Mixer` belongs to T-input. Every channel between threads is bounded: when full, it drops (buttons and axes) or queues (device events), and the writer never blocks.

### 5.2 Running in the background, and the tray (main)

- **A single instance.** Live mode takes the named mutex `Local\padmate.instance.<SID>.<session>` before loading the profile, the log and SDL. A second start forwards its profile choice to the first instance over authenticated IPC; conflicting startup arguments are refused.
- **The exit sequence.** The tray's "Exit" and the installer's `daemon.exit` (§11) take the same path, which runs once: (1) `output.BlockExit` (cannot reopen; releases every key at once) and `Supervisor.Pause` with a 1 s limit; (2) the `daemon.exiting` announcement, waiting for registered UIs to call `ui.exitReady` or disconnect (at most 2 s), while the UIs can still save; (3) `daemon.exit` waits for the caller's connection to close (at most 500 ms), so the reply is written first; (4) the normal exit path (§5.17). Crashes, Ctrl+C, the end of the session and `WM_CLOSE` send no announcement.
- **Start at sign-in.** Only a click in the tray menu, or the UI through `startup.set`, writes or deletes the padmate value of HKCU Run. When the command exceeds 260 UTF-16 units, the full arguments are stored in `%APPDATA%\padmate\startup\`, and Run only refers to them. Development and test builds create no startup entry; `startup.set` returns -32017. At sign-in there is no standard output, and the dry-run printer uses `io.Discard`.

### 5.3 T-input and SDL

`internal/input`; the reasons are in [Controller input](adr/0002-input-sdl.md).

- `input.Open` starts T-input: it locks the thread, loads SDL (`$PADMATE_SDL3`, then next to the exe; on Windows it must be the pinned 3.4.16; it is loaded once per process, and there is one Source at a time), sets the hints in the order "always → preset → `--hint`" (later ones override earlier ones; "always" is background events on, SDL's signal handlers off and `SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1`), and calls `SDL_Init(SDL_INIT_GAMEPAD)`.
- Every SDL call is on T-input: other goroutines hand it functions through a command channel, run between two `SDL_WaitEventTimeout(2 ms)`. It is not woken with `SDL_PushEvent` (which races with `SDL_Quit`). As soon as an event returns, `latency.Now` is read into `Event.Stamp` (§13).
- **The event channel** holds 1024, and sending never blocks: when full, buttons and axes are dropped and counted. `DeviceAdded`, `DeviceRemoved` and `ComboHeld` are never dropped; they go to the backlog and wait their turn in order. While the backlog is not empty, buttons and axes are dropped too, so no event overtakes a queued device event.
- **Mapping changes.** SDL mappings take effect by GUID at once, without releasing what is held. A joystick without a mapping is reported raw (`IsGamepad` false); when a mapping is added, a `DeviceAdded` with `IsGamepad` true is sent again for the same id; when a mapping is taken away, a `DeviceRemoved` and a `DeviceAdded` with `IsGamepad` false; when a mapping changes, a `DeviceRemapped` (not written to traces, §5.7).
- **Effects** (the reasons are in [Controller feedback](adr/0015-controller-feedback.md)) all run on T-input. When an Effector call fails, its group cools down for 5–7 s, during which nothing is written (when a 2.4 GHz receiver does not take OUT reports, a blocking write would hold up the input of every controller). `padmated.effectQueue` lets the slots and the engine submit data only, and a worker calls T-input in order; cancellations are never dropped, and nothing a script started may outlive the script.
- **Close and panic.** `Close` detaches virtual gamepads, closes devices, calls `SDL_Quit` and closes the event channel. A panic or `Goexit` on T-input ends the same way: `Err()` is set before `Events` closes and returns a `*PanicError`; later commands return `ErrClosed`.

### 5.3.1 Joy-Con 2 over Bluetooth LE (`ble.go`, `jc2hub.go`, `jc2/`, `jc2win/`)

SDL does not read Joy-Con 2, so T-input also runs a second backend; the reasons are in [Controller input](adr/0002-input-sdl.md). It has three parts: `jc2` (pure protocol), `jc2win` (the Windows Runtime transport, via winrt-go) and the hub in `internal/input` (`jc2hub.go`, glued to `Source` by `ble.go`). Where its gamepads look like any other's is §7.1.1.

- **Start.** Only when `Options.JoyCon2` is not nil: padmated's live mode sets it; `monitor`, `record` and most tests leave it nil. `Source.init` starts the transport after the hints and the combo watch. A transport that does not start is not an error of `Open`: SDL's gamepads go on, and `JoyCon2Available()` and the Joy-Con 2 methods return why not (outside Windows, `ble_other.go`, there is no transport). Tests give `JoyCon2Options.Transport` a fake, so the hub is tested on Linux.
- **The transport goroutine.** `jc2win.Start` launches one goroutine that locks itself to an OS thread, initializes the Windows Runtime for it (multithreaded apartment) and does every WinRT call that is not inside a callback: the scan, the connection (session, services, the short start-up conversation), the notification switch, the allowlisted command writes, the connection-parameter request, and every 2 s the read of the current connection parameters of each live link (`GetConnectionParameters`, Windows 11 only; a `Params` event when the interval, latency or timeout changed, and the first read comes with `Connected`; plain calls on this goroutine, not a callback). `Scan`, `Connect`, `Disconnect` and `Close` only queue a request and never block. What may be written is decided in one place (`gate.go`: `jc2.CheckRequest` on the exact bytes, right before the single write call; `guard_test.go` keeps it that way).
- **The callbacks.** Three WinRT events call into Go on threads of the Windows thread pool: an advertisement was received, a characteristic value changed, a connection status changed. Each copies the bytes, reads `latency.Now`, sends on a bounded channel (256) without blocking (a full channel counts a drop) and returns. It does not wait, does not await and takes no lock that a slow path holds. padmate creates no callback of its own: the delegates of winrt-go share a fixed set of four, created once (invariant 5).
- **How the events enter T-input.** In every turn of the loop, after the `SDL_WaitEventTimeout(2 ms)` and next to the receiver watchdog, `pumpJoyCon2` drains that channel (at most 512 events, so a flood of advertisements cannot hold the loop up) and then runs the hub's timers. There is no wake-up channel: a report waits at most one turn (2 ms) on top of the 15 ms (Windows 11) or 60 ms the controller sends at. Every public method (`JoyCon2Pair`, `JoyCon2Forget`, …) goes through `s.do`, so the hub is only ever touched on T-input. The hub reads no clock; it is given SDL's tick time, the clock of `Event.Time`.
- **What the hub does.** It decides whom to connect to (the pairing window, the remembered controllers, auto-connect), when to scan (only while the window is open or something remembered can be connected), forms the gamepads, maps each report with `jc2` and sends the differences from the last state as `ButtonDown`, `ButtonUp` and `AxisMotion`. The `Stamp` of an event is the one of its report's callback, so the latency statistics start there (§13).
- **The connection policy.** A controller accepts a PC only while SYNC is held (a "standard" advertisement, host address all zero). The hub connects an advertisement only if it is standard and either the pairing window (30 s by default, 1–600 s) is open and has not yet taken a controller, or the controller is remembered and auto-connect is on. An address that failed or was closed is left alone for 5 s; a connection that has not come up after 30 s is canceled; at most eight links, one per player number. The first controller of a window is remembered once it is up (`OnRemember`, then `Memory.RememberJoyCon2`, §10.1). A controller whose serial number is already connected under another address is disconnected.
- **No stuck keys** (invariant 1). A gamepad's events end with its `DeviceRemoved`, which every consumer takes as a release of everything of that `DeviceID`, and a `DeviceID` is never used again. The hub reports a gamepad removed when it goes dormant (below), when its link is lost or closed, when the pair changes, when a controller is forgotten or disconnected, and in `Close`, which runs before `SDL_Quit`. On resume, `Reenumerate` first lets the hub drain and tick (a link that died during sleep is gone) and then sends a `DeviceState` for every live gamepad.
- **Dormant.** A link with no report for 300 ms (`jc2.DefaultSilence`) is dormant: it counts for no gamepad until it reports again, and the link stays. The gamepad it was in is reported removed; if that was a pair, the side that still reports is added at once as a single (own `DeviceKey`, new `DeviceID`, what it holds as fresh presses), without waiting for the silent side to be given up. The next report of the dormant link makes the layout anew, as for a fresh connection: a single that was alone comes back as a new `DeviceID` with the same `DeviceKey` (the router treats it as a reconnect, §5.8), and a single that finds its partner back is replaced by the pair (§7.1.1). A link with no report for 3 s (`jc2.DefaultLinkGiveUp`), or one that Windows reports lost, is closed. There is no "arming": unlike a receiver (§5.5), the controllers report on a timer also at rest.

### 5.4 Device identity (`internal/input/devicekey.go`)

The DeviceID changes on every connection; `DeviceKey{GUID, Serial, Path, N}` stays the same across reconnects, and N tells apart controllers connected at the same time that are the same in everything else. The router uses it to recognize a controller that comes back, so it takes its slot back, or so a stand-in slot changes its home (§5.8). For Joy-Con 2 it rests on the serial number from the controller's flash (§7.1.1).

A left and a right Joy-Con connected together are replaced by one pair device (§7.1): SDL removes the lone Joy-Con and adds the pair under a new DeviceID, and the pair has no serial in SDL 3.4.16, so its key is the pair's GUID and fixed path, the same for any two Joy-Cons and every time. A lone Joy-Con keeps its own key (GUID and serial) and gets it back when the pair goes and it returns. The swap is an ordinary removal and addition (`DeviceRemoved` does `ReleaseAll`, §5.6); the state kept per DeviceKey (calibration, layer state in state.json) is not carried from the lone one to the pair. A pair of two Joy-Con 2 (§7.1.1) is replaced in the same way, but it has serials (the two joined), so its key is its own.

### 5.5 The receiver watchdog (`receiver.go`, `watchdog.go`, `hidbeat.go`)

A USB receiver of the Switch Pro protocol reports no disconnect after its controller turns off. Only padmated turns the watchdog on (`input.DefaultReceiverTimeout` = 2 s); it reads HID heartbeats: after idle heartbeats have gone on for a while it is "armed", and from then on, when heartbeats stop for the timeout, a `DeviceRemoved` is synthesized. After the removal, neither the gamepad nor the HID handle is closed (closing would put the USB device into simple mode), and the device's next event brings it back with a new id.

### 5.6 The slot (`internal/slot`)

A `Slot` connects one Engine to the Arbiter. It cannot be used concurrently, and is driven by its caller, by `Replay` or by the slot loop. `Config.Arbiter` is an interface: a local `*output.Arbiter` (dry runs, golden tests) or an `*output.RemoteArbiter` (T-output).

- `Handle(ev)` processes an event. `DeviceRemoved` first does `ReleaseAll`, then `engine.Reset`; the State is cleared and the script closed. `SetPaused(on)`: pausing first calls `engine.Pause`, then releases the owners of the same slot number of any generation; resuming sends nothing. `ReleaseAll`, `Reset`, `Reload` and device removal all keep the pause.
- `Reload(prog, gen)`: `engine.Reload`, releasing the owners of the same slot number of any generation; the State is unchanged.
- **The controller's function key and switches** (ADR-0017). `Config.Program` is the profile compiled once with the default function key; the slot runs the variant of it that has its controller's function key (`engine.Program.ForFunctionKey`: made on the first request, then shared by every slot through the Program). It asks `Config.FunctionKey(key, info)` when it is created, when it gets or loses a controller, at each `Reload` and at `RefreshFunctionKey`, on its own goroutine. A key that differs from the running one (an unchanged one does nothing, so a held function key stays held) is the engine's `Reload` of the same profile with no new generation: held controls are canceled, the Arbiter releases every owner of the slot (the `ReleaseAll` of invariant 1), and the held function key is forgotten (§7.9). `Config.Switches(key)` is the same for the feedback switches: `Options.Switches` reads those of the slot's own controller at each use.
- **Script wiring**: pause, `DeviceRemoved` and `Reset` close the script runtime; resume, `DeviceAdded` and Resync only queue a rebuild on the worker, with no setup inside the slot loop. Script gestures go through `GestureResolver` to the same engine recognizer; there is no second set of timers.
- **The cold-start input barrier**: without a valid Host, up to 128 button or axis edges are held and replayed in their order once the script is ready. When the cold start fails, or the buffer overflows, the whole batch is released; declarative actions are never the fallback through an empty registry.

### 5.7 Resync semantics

The router keeps one `slot.State` per connected controller, and puts the events of assigned controllers into their slot's bounded edge channel (`slot.Edge`, capacity 256).

- **Falling behind.** When the channel is full, the slot falls behind: later edges are skipped and counted, while the router keeps updating the state. Edges the slot must know about while behind are recorded in order as pending (first a release, then the `DeviceAdded` of the controller assigned now). Every event, every Sync and a 2 ms `clock.Alarm` retry: first the pending edges are delivered, then `Edge{Resync: &st}`, without waiting for the controller's next event — controllers send only changes, and the one dropped may be the last release.
- **Reset on lost events.** After each event, retry and Sync, the router compares `Dropped`; if it grew, every controller's State is cleared and every slot gets `Edge{Reset: true}`. `DeviceRemapped` is the same thing, a reset of that controller: a held control may be another element under the new mapping, and the old one will never send its release, so a control still held physically must be released and pressed again to count.
- **`Slot.Resync(st)`** compares the State with `st` and synthesizes the missing edges (releases first, then presses, then axes). It only makes the state consistent and does not replay what happened in the gap: a button pressed and released in the gap loses that press.

### 5.8 The supervisor (`internal/supervisor`)

- **Construction.** `New(Config)` needs a `Clock`, `Events` (closing it is the same as canceling the ctx), an `Output` and a `Program`; `Slots` is 1 to 4, all built in `New` with the same program. No callback may block or call the Supervisor. Slots with the same `ProfileKey` share an in-memory KV.
- **Assignment** (the reasons are in [Several controllers and slots](adr/0003-devices-and-slots.md)). Each slot has a home (a DeviceKey) and a stand-in flag; each controller that matches `Config.Device` either holds a slot or queues.
  - A new connection: with an empty slot, it stands in for the one with the lowest index and becomes its home; with no empty slot, but a stand-in slot whose home it is, it takes that back; otherwise it goes to the back of the queue. A controller leaves: the controller at the front of the queue takes over at once (it first gets a copy of `DeviceAdded`, then a Resync).
  - Confirmation: the stand-in's first `ButtonDown` on an enabled slot makes the assignment final; axis events, held buttons brought by a Resync and presses on a disabled slot do not count.
  - While standing in, the slot can be taken: when the key of its home connects, it takes the slot back; a queued controller's `ButtonDown` takes over the earliest stand-in slot.
- **Settings of each controller** (ADR-0017). `Config.FunctionKey`, `Config.Switches(key)` and `Config.Calibration(key)` are asked for the slot's own controller, on the slot's goroutine, and return what the last committed config.json says; `OnFunctionKey` reports a slot's switch to another function key. `RefreshFunctionKeys(ctx)`, like `RefreshCalibration` and `RefreshFeedback`, is a Sync-like barrier that runs `Slot.RefreshFunctionKey` on every slot behind the edges delivered so far: a slot whose controller keeps its function key does nothing, one that changes it lets go of its own keys, and no other slot is disturbed. A slot without a controller asks with key `""` and has the function key of a controller with the standard layout that has no settings of its own.
- **Isolation.** Each slot has its own engine, State, runner and edge channel; the keys it presses carry its own `Owner.Slot`, and releasing releases only its own. A slot that is stuck, panics or is disabled neither delays nor releases another slot.
- **`Sync(ctx)` is a barrier**: when it returns, every event put into `Events` before the Sync has been handled by the slots, and the commands they caused have run on the Output. **`Reload(ctx, prog)`** gives every slot `prog`: a slot that is behind catches up with the old program first, so events put in before the Reload use the old program and later ones the new; each key goes up once, and controls the reload canceled must be released and pressed again to work; it cannot be called on a slot's goroutine (deadlock). `UpdateScriptParams` is the light version for a change to `script.params` alone: it commits only after every slot is ready, swaps no runtime and releases no keys.
- **Shutdown order.** Cancel `Run`'s ctx or close `Events`: `Run` closes every edge channel, the slot loops finish their queued edges, release through the Output and end, and `Run` returns once they have all ended. The caller then does `Output.Stop`, and last closes `input.Source`; `Run` never calls `Output.Stop`.

### 5.9 T-output (`output.Loop`)

- `StartLoop(clk, sender, opts)` starts a goroutine that owns a new Arbiter; `LoopOptions.Lock` locks it to an OS thread and never unlocks it (padmated must set it), so the Sender is only ever called on one thread.
- `Do(f)` hands f to the loop and waits for it to run, returning its error; commands run one by one in the order they arrive. `Remote()` returns a `RemoteArbiter`.
- **The held-keys log and the startup sweep.** The loop opens the log on its own thread (the format is in §8.2). Before entering the command loop, it sweeps: it reads the keys the last run left (only those written by the same boot and sign-in session), asks `KeyDown` which are still down, and lifts each once through MenuMask, bypassing the gate. Every `Do` comes after the sweep. When the log is damaged, comes from another boot or session, or cannot be opened, no key is released.
- **The gate, `BlockFor(b, on)`**, opens and closes the Arbiter's gate by owner (§8.2): `BlockUser` (user pause and the emergency combo), `BlockSystem`, the resume scan, `BlockExit` and `BlockEmergency`. The gate is closed while any owner keeps it closed. It does not wait and any goroutine may call it: it only changes an atomic value and wakes the loop. The loop applies it before each command: if a close happened, it first does `ReleaseAll` (through the ledger and MenuMask), then lifts the carried keys (keys the sweep could not lift that `KeyDown` still reports down).
- **`Stop()`**: first `ReleaseAll`, retrying failed releases; if some keys are still not up, it returns an error naming the keys that may be stuck. Then it lifts the carried keys and rewrites the log with the keys left in the ledger; with none left the log is deleted, otherwise it stays for the next start.
- **Panic and Goexit.** The Arbiter's Sender is the ledger, which records in order the held keys that injection left; when `Send` itself panics, every press of that batch counts as held. When the loop does not end normally, it lifts the keys of the ledger in reverse order of pressing, with one Send on the same goroutine, then lifts the carried keys, rewrites the log, and closes `Done()`, so that a `Do` in progress returns `ErrLoopClosed` and a `*PanicError`.

### 5.10 The slot loop (`slot.RunLoop`)

It runs on the caller's goroutine and owns the Slot; `Lock` locks the OS thread. Its `select` has the ctx, the edge channel, timers, a job channel and an optional script wake-up channel. Before a job it handles the edges already in the channel, so a job is a barrier for them (Sync relies on this). `Edge.Pause`: `PauseOn` pauses before the edge's Reset/Resync/Event, `PauseOff` resumes after them, so presses brought by a Resync are already canceled. Script builds and setup run on the worker, and a failed build keeps the old runtime. It returns when the edge channel closes or the ctx ends, and does `ReleaseAll` before returning. Panic and Goexit are in §5.16.

### 5.11 Pause and the emergency combo

The reasons are in [No stuck keys](adr/0006-no-stuck-keys.md).

**The user's pause.** The state belongs to the router, is shared by every slot, is not saved, and holds across Reload, resets, reconnects and restarts. It is kept apart from the system suspend (§5.13); either one holds mapping. `Supervisor.Pause(ctx, on)` first lets the router route the queued events, then switches, calls `Output.BlockFor(BlockUser, on)` and delivers a pause edge to every slot; when it returns, every key is up and later presses are dropped by the gate. When paused, a slot cancels the cycles of held controls; resuming only opens the gate and presses no key: a held control must be released and pressed again to work. Its callers are the emergency combo, the tray and IPC.

**The emergency combo** (`input.ComboWatch`, detected on T-input).

- It picks the combo on each device's `DeviceAdded` (`ComboFor`, which padmated implements as the combo of that controller's settings, else `DefaultCombo`, §10.4; ADR-0017) and tracks the members itself in a bit mask, without asking SDL. T-input shows it every event before the event goes into the channel, so it sees dropped button events too.
- Timing starts when all members are held; after `ComboHold` = 2 s held it fires once and latches, and timing starts again only after a member is released and pressed again. The members' own bindings work as usual and are canceled by the pause when it fires.
- When it fires, `OnCombo` is called first, then the `ComboHeld` event goes into the channel (through the backlog, never dropped). padmated's `OnCombo` calls `output.Loop.Block(true)`: it bypasses the slot loop and does not wait for T-output, so it releases at once even when a slot is stuck or `Events` is full.
- The router toggles pause on a `ComboHeld` from any controller (including queued and filtered ones); no slot receives this event.

### 5.12 Offline emergency release

`padmatectl release-all` does not use IPC; it is for when the daemon is stuck or IPC is not available.

- The command signals the named event `Local\padmate.panic` (auto-reset) of the current interactive session. Only a padmated with `--inject` listens: an auto-reset event wakes one waiter at a time, so a dry run listening would steal the signal meant for the `--inject` instance.
- `startEmergency` creates the event inside `startLive`, so the event exists once `startLive` returns. Its security descriptor grants the current user only, and padmatectl can open it elevated or not. When the event cannot be created, a notice goes to stderr and mapping goes on.
- On the event, `Supervisor.ReleaseAll`: the separate `BlockEmergency` gate releases every key of T-output at once (carried keys included), then a `ReleaseAll` job is sent in order to every slot, destroying old scripts and asynchronous callbacks; the gate reopens only after each slot and T-output have completed the barrier. The router's pause, suspend and control state are unchanged.

### 5.13 System suspend and T-session

**T-session/focus** (`internal/winsession`). A locked thread started by `Start`; the window, the hooks and the timers all belong to it.

- A hidden top-level window (not `HWND_MESSAGE`, which receives no broadcasts) receives session, power, `WM_ENDSESSION`, `WM_CLOSE` and desktop-switch messages, and a timer every 2 s. `WM_APP+1` to `+3` and timer ID 1 belong to winsession; `WM_APP+8` onward and timer IDs from 2 belong to `winfocus`.
- Messages that clear a reason, desktop switches and the timer query the session and the input desktop only in posted messages (a `SendMessage` from another thread is posted again first, so the sender does not wait on an RPC). Sleep and the end of the session first wait for `Config.Barrier` (at most 1 s) before returning to the system; `WM_ENDSESSION(TRUE)` then calls `OnStop` and waits for `Close` (at most 3 s).

**`Supervisor.Suspend(r)`.** Sets the reasons of a system suspend (Lock, Disconnect, Desktop, Sleep, End); 0 means it is over. Any goroutine may call it, and it never waits.

- Adding a reason calls `Output.BlockFor(BlockSystem, true)` at once: every key is released and later presses are dropped, without going through the router, so it works even when the router or a slot is stuck.
- When the router enters the suspend, it applies every reason set since last time (a lock and unlock it did not see in time still make the slots cancel the cycles of held controls) and delivers `PauseOn`; when it leaves, it first routes the events queued at that moment, still handled as held, then delivers `PauseOff`.
- Only the router opens the system's gate: it sends each slot a barrier queued after `PauseOff`, and the barrier completed last opens the gate, since a slot that has not handled its pause edge yet could press something from before it. Suspend and the user's pause do not affect each other; mapping needs both gates open.

**Recovering after waking.** The router keeps a separate quarantine for controls seen active before or during the suspend: neither a synthesized release nor an empty scan can clear it; after resuming, only a release or neutral lifts it. The recovery coordinator calls `Source.Reenumerate` on T-input to get the full `DeviceState`, then `Supervisor.Sync`; it resumes only when there is no new system reason. A new suspend reason invalidates an old scan; a failed scan stays suspended and retries. The user's pause, the system suspend and the recovery scan each hold the output gate, and none lifts another.

### 5.14 T-hook (`internal/winhook`)

`WH_KEYBOARD_LL`, with a single `windows.NewCallback`, running a `GetMessage` loop. The callback reads `latency.Now`, copies the data, asks whether to swallow, and sends without blocking into a channel of capacity 4096 (counting and dropping when full); events it does not swallow go to `CallNextHookEx`. It is installed only during a capture session.

### 5.15 Logging (`internal/logging`)

- slog JSON lines, written to stderr by default; without stderr (the GUI subsystem), to `%APPDATA%\padmate\logs\padmated.log`. Logs never contain window titles; key events are written only at debug level. Pure packages do not log; the supervisor records the slots' errors.
- `input.Source`'s logs are not written by T-input directly: they go into a queue of capacity 256 and another goroutine hands them to the handler, counting and dropping when full. The reason: writing stderr blocks while the console's QuickEdit has text selected, and T-input must not stop pumping SDL and detecting the emergency combo because of that.

### 5.16 Panic recovery

The principle: after a panic, the level that failed cannot be trusted; skip it, and let the next level release the keys.

- **The slot loop.** After recover it does not go through the engine; it calls `Arbiter.ReleaseOwners` directly to release the owners of the same slot number of any generation (and recovers itself too). `RunLoop` returns a `*slot.PanicError`.
- **Restarting a slot.** The runner restarts it after a back-off with a new `slot.New` (keeping the same edge channel, with every control up). The back-off is 100 ms, doubling for each further panic within 10 s. A restart does not Resync (it could press again the button that may have caused the panic). Three panics within 10 s disable the slot: the router drops that slot's edges, and the assigned controller still holds the slot; the next assignment enables it again.
- **T-output.** After recover it lifts the held keys from the ledger and the loop ends (§5.9); the daemon treats this as a fatal error.
- **T-input.** After recover it closes SDL as `Close` does and closes `Events`; `Source.Err()` returns an `*input.PanicError`. padmated takes the exit path when it sees `Events` close.
- **The slots' runners, the router, the watcher.** After recover nothing is logged (the logger may be what panicked); `supervisor.Run` returns a `*supervisor.PanicError` once every slot loop has released and ended, and padmated takes the exit path.
- **`runtime.Goexit`.** It cannot be recovered, but defers still run; loops and goroutines recognize with a flag that it is not a normal end and handle it like a panic. Other goroutines holding mapping or output resources also `ReleaseAll` first.

### 5.17 padmated (`cmd/padmated`)

The usage is in `padmated -h`: live mode must have one of `--dry-run` and `--inject`; a replay uses `--fake-input` and can only be `--dry-run`. A usage error exits with code 2; an error at startup with code 3; a failure after mapping has started with 1. The stop signals are Ctrl+C and SIGTERM; sign-out and shutdown send no signal (by Microsoft's documentation, not tested), so they are learnt from winsession's `WM_ENDSESSION`, which takes the same exit path as `WM_CLOSE`. A replay handles only the first device with button or axis events; its Sender is the dry-run printer, and the output does not depend on the machine's layout.

**The startup order of live mode** (`live.go`, `startLive`).

1. `input.Open` starts T-input, with `OnCombo` (which calls `Loop.Block(true)`, and does nothing before the Loop starts).
2. `--dry-run` uses the dry-run printer, `--inject` the `output.SendInput` built in `sender_windows.go`. padmated builds it only here and hands it only to `output.StartLoop` (`TestNoSendInput`, `TestChooseSender`, forbidigo). Then T-output starts; with `--inject`, `HeldFile` is `%LOCALAPPDATA%\padmate\run\held.bin`, and a dry run sets none. `startLive` waits for `Loop.Swept()` and logs the result, without making padmated exit.
3. `startSession` starts T-session: `OnChange` calls `Supervisor.Suspend`; `Barrier` is a `Loop.Do` of an empty function, so when it returns, T-output has sent the release. A session that is already locked suspends mapping before `Run`. When it cannot start, stderr says "locking the screen, sleep and sign-out no longer release keys", and startup goes on.
4. Only with `--inject` does `startEmergency` create the release-all event (§5.12); `supervisor.New`, and `Run` on its own goroutine; without `--watch=false`, the watcher starts (§5.18).

**The startup report.** The UI passes `--startup-report FILE`; when startup fails, a JSON object is written (the fields are in `startup_report.go`); FILE is deleted as soon as the arguments are read. The packaged build has no stderr, and this is the only way the UI learns the reason.

**Stopping and the exit path** (`shutdown`, written in a defer, so it runs during panic unwinding too). On a stop signal or T-session's `OnStop`: stop the watcher, cancel `Run`'s ctx, exit code 0. When `Run` returns by itself (T-output ended, or `Events` closed): exit code 1; the keys are already released by then (the slot loops release when `Events` closes, and T-output's panic recovery when it ends). Then, in order:

1. Stop the watcher and wait for it (at most 1 s; a stuck read does not make held keys wait for it), then wait for `Run` to return; by then the slot loops have released their keys through T-output.
2. `Loop.Stop`: `ReleaseAll`, sending failed releases again. An error sets exit code 1.
3. Close the effect queue and wait for its worker, then `Source.Close`, waiting at most 1 s in total; on timeout, `Source.RequestClose`. This 1 s governs only the tail after the release; the releases and retries of steps 1–2 are never skipped.
4. Flush the printer; close T-session and the log. When T-session called `OnStop("session-end")`, `WM_ENDSESSION` is waiting for its `Close` (at most 3 s), after which the system may end the process at any moment, so the log is written first.

The Loop, the Source and T-session belong to padmated: `supervisor.Run` does not call `Loop.Stop`, and closes neither the Source nor T-session.

### 5.18 Hot reload (`live.go`)

The profile file's watcher (§10.5) calls `reloadProfile` whenever it sees new content: if the content did not change, nothing happens; on parse or compile errors, an error is logged, the old program keeps running and nothing is released; if both profiles have a `script` and are the same apart from `script.params`, `Supervisor.UpdateScriptParams` is called and the script is not rebuilt; any other change calls `Supervisor.Reload`.

**Profiles with parents (`extends`).** `Profile.Parents` records the ID and the content's sha256 of each parent read during the merge. Each parent file has a watcher of its own; when any has new content, the child is read and merged again, then handled by the rules above (the content counts as unchanged only if the sequence of parents did not change either). When the child file cannot be read (in use, deleted and created again): it merges from the original text the running profile was loaded with; without that text, it reads again after 200 ms, up to 5 times.

### 5.19 `padmatectl trace`

`padmatectl trace --profile FILE --input FILE [--json]` replays recorded events on a virtual clock. It injects no keys. It reads no config.json unless `--config FILE` is given. The function key is that of the controller in the trace (ADR-0017): the default of the family its added event shows (a single Joy-Con has `lt` (ZL) or `rt` (ZR), a pair `back`, other controllers `start`), or the `devices.<DeviceKey>.functionKey` of the `--config` file; `--function-key` (`start`, `back`, `leftshoulder`, `rightshoulder`, `lt` or `rt`) forces one for the whole trace.

## 6. The data flow of one press

The example binding: `west` tap = F13, hold = F14, balanced policy.

1. SDL delivers `GAMEPAD_BUTTON_DOWN(west)`; T-input stamps `Event.Stamp` and puts it into `input.Source`'s channel; the router updates the device state and pushes it into that slot's edge channel (§5.7).
2. The slot loop calls `engine.Step` in `slot.Handle`: west has a tap and a hold, so it goes Pending with `NextDeadline = t + holdMs`; south is pressed while it waits, and the balanced policy buffers its edges.
3. A release of west within 250 ms is a tap: `Tap(owner, F13)`, then the buffered south is replayed in order; on timeout it is a hold: `Down(owner, F14)`, and `Up` on release.
4. The Arbiter resolves the layout (every press of a character key looks up the foreground thread's HKL, §8.1), counts references, and guarantees the minimum press time.
5. T-output's ledger first writes the presses of this batch to the held-keys log (with `--inject`, §8.2), then calls SendInput through MenuMask, and rewrites the log after sending. It also produces Decision records, for trace and the UI's timeline.

## 7. The engine

`internal/engine` is a purely functional state machine: its input is timestamped edges and axis values, its output calls to the `Output` interface and `Decision` records. It imports neither `input`, `output` nor `script`, never calls `time.Now`, and time comes only from `Input.T` and `Apply(now)` (invariant 2). The reasons are in [ADR: Engine and threads](adr/0005-engine-and-threads.md).

### 7.1 Control names

Controls are defined by `internal/control`; names are case-sensitive. Aliases are for input only; `String()` returns the standard name.

- Buttons: `south east west north back guide start leftstick rightstick leftshoulder rightshoulder dpad.up/down/left/right misc1..6 paddle1..4 touchpad`. touchpad is only the press; the touch position is not read.
- Virtual controls: `lt`/`rt` (triggers as buttons), `lt.full`/`rt.full` (the second stage), `ls.up … ls.upright`/`rs.*` (directions including diagonals), `ls.active`, `ls.inner/outer`, `ls.cw/ccw` (one knob tick, `mode:"knob"` only), `dpad.upleft` and the like (only when `analog.dpad.mode` is not `buttons`), and half-axis buttons such as `leftx+`/`lefttrigger-`.
- Aliases: `a b x y` (south east west north), `lb rb`, `l3 r3`, `select` (back). Raw axis names: `leftx lefty rightx righty lefttrigger righttrigger`.
- There is only ever one function key; see §7.9.

**Joy-Cons**: a left and a right one that are connected together are one controller, the pair (`joyconpair`), with no switch or setting; one connected alone is a controller of its own, always read held upright (`joyconleft`, `joyconright`): padmated sets `SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1` when it initializes SDL (`input/hints.go`), and the Joy-Con 2 driver maps the same way. There is no sideways grip, no sideways mapping table and no stick rotation. Only an explicit `--hint SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=0`, or the environment variable of that name, undoes the hint, as a diagnostic; padmate's families, labels and combos still assume the upright mapping then. The rule is the same for every Joy-Con SDL pairs, and the Joy-Con 2, which padmate reads itself, follows it too (§7.1.1). The pair replaces the lone devices (§5.4, and "Joy-Con pairs" in PITFALLS), so a key held on a Joy-Con is released when its partner connects or when one of the pair drops, and mapping continues on the new device. `--hint SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0` keeps them apart, for diagnosis (it affects only the Joy-Cons that SDL pairs, not the Joy-Con 2). The button mapping is SDL 3.4.16's (Switch 1; the Joy-Con 2 is not read by SDL, see §7.1.1; other Switch 2 controllers are not supported):

- A single Joy-Con, upright. Left: the arrow buttons are `dpad.*`, L/ZL are leftshoulder/lt, − is back, Capture is misc1, SL/SR are paddle2/paddle4, the stick is the left stick (its click is leftstick). Right: the face buttons are south/east/west/north, printed B/A/Y/X (as on a pair), R/ZR are rightshoulder/rt, + is start, Home is guide, SL/SR are paddle3/paddle1, the stick is the right stick (its click is rightstick). A left Joy-Con has no face buttons. ZL and ZR are trigger axes, so `lt` and `rt` are their virtual buttons (above).
- The pair is the same as a standard controller, plus misc1 (Capture) and paddle1–4, and the hint does not apply to it. The UI's picture of a pair leaves paddle1–4 out, since SL and SR cannot be pressed with the two joined (the Mapping page, in the section on the renderer); the daemon reads them all the same.
- There are no misc2..6 (the Joy-Con 2 has misc2, §7.1.1), no touchpad and no `sl`/`sr` aliases. The family of a controller (`padfamily`: its function-key choices and its built-in combo, §10.4) follows from `DeviceInfo.Type` alone (`input.FamilyOf`): `joyconleft`, `joyconright`, `joyconpair`, and the standard layout for every other type. `padfamily.Family.HasButton` says which buttons a single Joy-Con has (the list above), which the checks of config.json use (§10.4). The emergency combo (§5): paddle2+paddle4 (SL+SR) for a single left one, paddle3+paddle1 (SL+SR) for a single right one, back+start for the pair.

### 7.1.1 Joy-Con 2

The Switch 2 Joy-Con, read over Bluetooth LE by padmate itself (§5.3.1, [Controller input](adr/0002-input-sdl.md)). The control names are those of the Joy-Cons above, plus `misc2` for the C button of the right one. The mapping (`jc2/mapping.go`) has two modes, of which the hub picks one per gamepad:

- **Single**, a single Joy-Con 2, always held upright (`Vertical` in `jc2/mapping.go`; the same mapping as SDL's for a Switch 1 Joy-Con with the vertical hint). Left: the d-pad is dpad.*, L is leftshoulder, ZL is lefttrigger (0 or 32767), − is back, Capture is misc1, SL/SR are paddle2/paddle4, the stick is the left stick. Right: A/B/X/Y are east/south/north/west, R is rightshoulder, ZR is righttrigger, + is start, Home is guide, C is misc2, SR/SL are paddle1/paddle3, the stick is the right stick.
- **Pair**: the union of the two single maps, left stick from the left Joy-Con and right stick from the right one. It is made automatically (below). The emergency combo is the one of §7.1: paddle2+paddle4 (single left), paddle3+paddle1 (single right), back+start (pair).
- Guide cannot be mapped (§7.9), so the Home button cannot be used, as with Switch 1 Joy-Cons. The grip buttons (GL/GR) and the motion sensors are not read.
- **Sticks.** The report holds the stick as two 12-bit numbers. The hub converts them to −1..1 with the controller's own calibration, read from its flash when it connects: the user calibration if there is one, else the factory one (the neutral and the travel in each direction), else a neutral of 2048 and a travel of 1200. The axes follow SDL's convention (y down, triggers 0..32767). The stick does not rest exactly on its neutral (PITFALLS), so the dead zone of the stick has to cover that.

**Device identity.** `DeviceInfo.Driver` is `ble`. Vendor is `0x057e`; product is `0x2067` (left), `0x2066` (right) or `0x2068` (a pair; padmate's own number); `Type` is `joyconleft`, `joyconright` or `joyconpair`; the name is `Joy-Con 2 (L)`, `Joy-Con 2 (R)` or `Joy-Con 2 pair`. The GUID follows SDL's layout (§5.4): the bus type `0x0005` (Bluetooth) in the first two bytes, no name CRC, vendor and product, and the driver signature `b` in byte 14 (a signature of padmate's own, which `guid.go` decodes as `ble`). It is the same for every Joy-Con 2 of a product; the serial number tells them apart. So the `DeviceKey` is `{GUID, Serial}`: `Serial` is the 14 ASCII bytes at flash `0x13002`, and for a pair the two serials joined by `+` (left first), so a gamepad keeps its key across connections, whatever its Bluetooth address (that is only in `Path`, `ble:<address>`). `DeviceID`s start at `0x40000001` and count up, a range that meets neither SDL's (from 1), the receivers' aliases (down from 2³²−1) nor the tests' fake ones (near `0x7ff00001`). Every time a gamepad is reported added it gets a new one.

**Pairs and singles.** The hub makes the gamepads from the connected controllers by itself, without a switch: the first left and the first right Joy-Con 2 (in the order they connected) form one pair, and every other one is a single. This is the same automatic rule as SDL's for Switch 1 Joy-Cons (no switch, no setting; `SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS` does not touch it). Only the hub's own links form a pair: a Joy-Con 2 never combines with a Switch 1 Joy-Con that SDL reports, and the two kinds pair among themselves, each by its own backend. When the set changes, a gamepad that no longer fits is reported removed first (its consumer releases everything), then the new ones are reported added; so when one of a pair goes, the other comes back as a single with a new `DeviceID`. A silent link counts for no gamepad (it is dormant, §5.3.1), so the layout is made from the links that report: if one side of a pair goes silent, the pair is reported removed and the side that still reports is added at once as a single, with a new `DeviceID`, its own `DeviceKey` and what it holds now as fresh presses; when the silent side reports again while the other is live, the single is reported removed and the pair is added, exactly as for a fresh second connection. With a spare Joy-Con 2 of the same side, the rule picks the first of each side in connection order, so the survivor may pair with the spare instead.

**Power.** The first byte of every report carries the battery level 0–9, a charging bit and an external-power bit. The hub turns the level into a percentage in steps of ten, `charging` if charging, `wired` if external power without charging, otherwise `discharging`, as a `DevicePower` event and for the power query (connection `wireless`). A pair shows the lower of its two. No percentage is sent until every link of the gamepad has sent a report. The player index is the lowest of 0–7 that no live link has; it is also what the controller's lights show.

**The per-device methods for a Joy-Con 2 `DeviceID`.** SDL knows nothing of it, so no SDL call is ever made for one (`Source.isJoyCon2`):

- Effects, lights and haptic cues do nothing and return no error: padmate drives no rumble or light on these controllers (the lights show the player number and are set once at connection). `devices.identify` is therefore silent.
- The mapping calls (reading, setting or resetting an SDL mapping) and the raw event stream (`devices.rawInfo`) return `input.ErrUnsupported`: there is no SDL mapping to edit, and a Joy-Con 2 is always a known gamepad, so the mapping wizard has nothing to do.
- Power and player index come from the report (above); `Reenumerate` brings the gamepads' state back after a sleep (§5.3.1).
- The Joy-Con 2 methods themselves (`JoyCon2Pair`, `JoyCon2CancelPair`, `JoyCon2Forget`, `JoyCon2Disconnect`, `JoyCon2Status`, …) are the IPC methods `joycon2.*` (§11); without the backend (not Windows, the transport did not start, a fake input) they return `ErrJoyCon2Unavailable`.

### 7.2 Gesture recognition

One state machine per control: `Idle → Pending → (Tap count → WaitMore) | Holding`. The function key is not part of it.

- **Waiting time** is computed from the frozen reachable registrations: with only press/hold bindings, a press is handled at once; with a tap and a hold, it waits `holdMs`; with multi-taps it also waits `multiTapMs`.
- **The interruption policy** is chosen automatically: `holdPreferred` when the hold action is a layer operation, precision mode, key hints or modifiers only; `balanced` otherwise; a binding can set `timeOnly`. `holdPreferred`: when a new press comes during Pending, it is first decided as a hold and run, then the layer stack is snapshotted and the new press is dispatched. `balanced`/`timeOnly`: the interrupting events are buffered and replayed in their order once decided (§7.7).
- Any press ends a WaitMore window: the tap count so far is committed first, then the new press is handled.

### 7.3 Resolution order

For each (control, gesture), the first receiver is found in this order:

1. The script's `global()`.
2. While the function key is held, the profile's `functionSet` (continuing down when the control has no binding there).
3. Overlay layers from top to bottom: in each, first the script's `layer(L)`, then the declarative L.
4. base: first the script's handlers without scope, then the declarative base.
5. common: first the script's `layer("common")`, then the declarative common.

`block` consumes without producing an action; `e.pass()` hands it to the next level; `claim()` gives the whole control to the script.

### 7.4 Layers

- The layer stack = common (bottom) + base + overlays. `layer` actions: `hold` (the overlay is removed on release), `toggle`, `switch` (replaces base), `cycle` (cycles through a list of base names). `defaultLayer` names the base at startup; it is `base` by default. `switch`/`cycle`/`toggle` take effect on press; "switch on release" is written as a `release` gesture with the layer action.
- Lookup goes from top to bottom, and `block:true` consumes and stops. **press-owner**: at press the layer name, binding and action are saved, and the release goes to the layer of the press; controls already held at the press are blocked in the new layer until released. Latches are recorded by (device, physical control).
- `functionSet` is not a layer: it has no switch pulse, color or mode number, and does not enter `layers.active`, `LayerChange`, state.json or the overlay's layer notices.
- **CancelAll**: ends every press cycle; clears toggles, repeat, sequences, pending gestures, the buffer, multi-tap windows, latches and all overlays (base stays); the Arbiter then releases the keys. To the engine, the controls are still held: their release produces no output, and a trigger must first go back below its release threshold before it can be pressed again.
- **Persisting layer state**: `PersistentLayers` gives the base, the toggled layers and the cycle index (no hold layers); `RestoreLayers` runs only at a cancel/reset boundary and produces no keys. `slot.LayerMemory` keeps it in memory by (device, profile): profile switches and disconnects save before the reset, a normal exit freezes it before ReleaseAll; on disk it is state.json (§10.1).

**The feedback seam** (`LayerChange`, `Feedback`): the engine publishes the current base, overlays, colors, switch pulses and pause state; durations and strengths are in the profile's top-level `feedback`. The switches (`rumble`, `led`, `pause`, on by default) are a setting of each controller (ADR-0017: `devices.<DeviceKey>.feedback`, else the retired top-level `feedback`), read through `Options.Switches`, which the slot binds to the DeviceKey of its own controller (nil means all on; called on the slot's goroutine, never blocks), so one controller's switch never reaches another's; with `pause` off, pause/resume give no pulse and do not show `pausedColor`. `Feedback.Mode` is the number of layer pulses: by position in the cycle when a cycle is declared, otherwise by layer name order, with base as 1. In live mode it goes through `Source.SetFeedback` (T-input) to the effect mixer (`internal/haptic`, §5).

**The haptic seam**: `Options.Haptic` is passed by value, with zero allocations; `Binding.Haptic` is a preset (0 means none). `runAction` is the single dispatch point of declarative gestures: it calls `playHaptic` once, after the decision and before the action, and two gestures of the same cycle at the same moment touch only once; repeat, `block` bindings, gestures a script took over, anything during pause, a `strength` of 0 and the global rumble switch off make no touch. Knob ticks touch when dispatched (with `haptic.Tick` when the binding has no `haptic` and the stick has `knobTick`). Downstream it goes through `effectQueue.postEffect` (never blocks, §5).

### 7.5 Table of analog modes

| Mode | Behavior |
|---|---|
| standard | Entering a diagonal sector fires the two neighbouring cardinal directions together |
| eightWay | Fires only the diagonal control; without a binding nothing fires, unless `diagonalFallback:"cardinals"` |
| fourWay | Only 4 directions, 90° sectors each |
| fourWayDiagonal | Only the 4 diagonal directions |
| knob | A virtual knob: each `knobStepDeg` of rotation sends one tick of `ls.cw`/`ls.ccw`; no directions; `ls.active` and the rings work as usual |
| mouse | The stick moves the pointer: no directions or ticks; `ls.active` and the rings work as usual; the pointer moves as soon as the stick leaves the dead zone |

The UI offers only fourWay, knob and mouse, and the d-pad has no settings in the UI; other modes in the file work as usual. A stick's optional `label` is only for the UI to show; the engine does not read it. A stick first applies the radial dead zone and outer saturation, then decides directions by `directionThreshold`; `directionDelayMs` delays direction changes, and `hysteresisDeg` debounces; `ls.active` and the rings fire together with the directions and are not delayed by `directionDelayMs`. Half-axis buttons use `analog.axisThreshold`, independent of the directions.

- **Per-device calibration**: the dead zone and the outer edge (`saturation`) are overridden per controller, in the order config.json's `devices.<DeviceKey>.calibration`, the profile's `analog.ls/rs`, the built-in default; combined they must satisfy 0 ≤ deadzone < saturation ≤ 1, otherwise this stick's calibration is ignored with a warning. A calibration change does no CancelAll: like Reload it does `settleVirtual`, directions and rings that no longer hold are released by the normal path, and new ones fire only after the stick returns to center and is pushed again.
- **Inner and outer rings**: `norm ≥ outerThreshold` enters the outer ring; `norm < outerThreshold − 0.05`, or no longer active, leaves it. `ls.outer` is pressed inside the outer ring, `ls.inner` while active and not in the outer ring. They are pressed only when bound (or when a script listens).
- **The virtual knob**: an angle of 0° points up and grows clockwise. The two axes are two inputs microseconds apart, so an axis input only sets `knobEvalAt = T + KnobSettle` (4 ms), and when it is due both axes are read as they are then. When it becomes active, the current angle is the reference `ref`, with no tick; when the shortest angle from `ref` reaches `knobStepDeg`, one tick is queued and `ref` moves; leaving the active zone forgets `ref`, so letting go to center makes no tick. The rate limit: at least max(tapPressMs) + max(tapGapMs) between two ticks (40 ms by default), never caught up later. A tick = one press and release of the tick control at the same moment; without a binding (and no script), it is dropped silently.
- **Two-stage triggers**: `lt.full`'s hysteresis works on the raw value: reaching `fullPress` presses it, falling to `fullRelease` releases it; while it is pressed, `lt` stays pressed. Without a binding only the state is recorded.
- **Rate-modulated repeat and curves**: `repeat.minRateHz` makes the repeat rate go from `minRateHz` to `rateHz` (12 by default) with the depth of the push, with a period of at least `repeatMinimum`; `analog.*.curve` (`linear`, `quadratic`, `cubic`) changes only the depth, never moves the thresholds and does not change the values scripts read. Only controls derived from sticks or triggers can use `minRateHz`, and it must not exceed `rateHz`.
- **Moving the mouse with a stick** (`engine/stick_mouse.go`, pure, no allocations): the target velocity = the direction's unit vector × `mouseSpeed` × `curve(depth)` × the precision factor. It integrates by the clock, not by tick: on every axis input, tick and change of factor, the time since the last time (capped at 50 ms) is first integrated at the old velocity into an accumulator, then the velocity is recomputed; with `mouseSmoothingMs` not 0, the velocity is an exponential moving average of the target. `MouseTick` = 8 ms; when due, the integer part goes out through `Output.Move`; a late tick is not caught up.
  - **Safety boundaries**: no tick is scheduled during pause. `CancelAll` clears all mouse state and locks a stick still pushed outside the dead zone, which unlocks only once it is back inside; after resuming from pause, the next axis input is needed to move again. `Reload` and calibration changes are not safety boundaries: they keep the state, integrate up to now with the old target, and then recompute with the new settings.
  - Precision mode (`op:"precision"`, `factor` 0.05–1, 0.3 by default) slows every mouse stick while its gesture is held, and can only be used on `press` and `hold`; jump (`op:"jump"`) puts the pointer at `x`, `y` (0–1) of the virtual desktop through `Output.Jump`.

### 7.6 Default parameters

| Parameter | Default |
|---|---|
| holdMs / multiTapMs | 250 / 220 ms |
| tapPressMs / tapGapMs | 20 / 20 ms (25 Hz at most for the same key) |
| repeat | 350 ms delay, 12 Hz; `repeatMinimum` 40 ms |
| Trigger thresholds | press 0.30 (raw 9830), release 0.20 (6553) |
| directionThreshold / axisThreshold | 0.30 / 0.30 |
| Dead zone / saturation radius | 0.15 / 0.95 |
| Diagonal width / `hysteresisDeg` | 45° / 8° |
| `outerThreshold` | 0.90 (fixed hysteresis of 0.05 on leaving) |
| `mouseSpeed` | 1200 px/s (fully pushed), range 50–10000 |

### 7.7 Implementation (`internal/engine`)

`Compile` turns a parsed profile into an immutable `Program`, shared by several Engines; `timing` on the profile or on a binding can override `holdMs`, `multiTapMs`, `tapPressMs` and `tapGapMs`, and an explicit 0 is kept. `NextDeadline` and `Apply` drive the hold, multi-tap and repeat timers. With `Options.Trace` off, Step, Apply, CancelAll, Reset and Pause allocate nothing.

**Action rules**: `profile.Parse`, `Compile` and `engine.CheckBinding` check the same rules, so the model `Parse` returns always compiles.

- release, tap, doubleTap, tripleTap and quadTap run after the release and have nothing to hold: keys actions can only be tap mode and cannot have repeat, and layer actions cannot be `op:"hold"`. This guarantees no hold is left that nobody ends (invariant 1).
- A KeySpec array (a key sequence) can only be tap mode and cannot have repeat; press/hold without a mode default to hold, so an array must write `"mode":"tap"`. repeat's `style`: `typematic` uses hold mode, `tap` uses tap mode, and without it, it follows the mode.
- A keys action whose main key is a mouse button cannot have typematic repeat (pressing again while held would be taken by the app for a double click), and saving over IPC (`profilelib.Put`) refuses it too; tap-style repeat is allowed.
- mouse and HUD actions press no keys and have no `mode` or `repeat`. sequence and text actions hold nothing either and can be used on every gesture; `steps` go through `keys.ValidateSteps`, and text through `keys.ValidateText`. When it fires, the engine calls `Output.Sequence` or `TypeText` once; the engine does no timing, and queuing, waits, the gap between characters and cancellation are all in the Arbiter (§8.2).

**Buffering and replaying interruptions**:

- `timeOnly` and `balanced` buffer the interrupting button edges as a queue and replay them in their order once the Pending one is decided. When the drain first finds the Pending control's own release in the buffer, it is decided as a tap; if that would open a multi-tap window, the tap action does not run. That release stays in its place in the buffer, and only when it is replayed are the keys and layers held by the press action lifted and the release action run.
- Repeat stops at the physical release; a replayed press whose own release is already in the buffer registers no repeat. A replayed press counts its holdMs deadline from its original time and is decided as a hold if that has passed, unless its own release is also in the buffer and earlier than the deadline.

**Multi-taps**: on reaching the maximum count the binding declares, it commits at once; when the window expires, or another control is pressed afterwards, the count so far is committed. The window is `multiTapMs` from the original time of the release.

**Press cycles and press-owner**: every press starts a cycle (even without a binding); cycle numbers are one counter per Engine and are never reused. The owner of output is `decl/<cycle>`, to which the slot adds the slot number and the profile generation. hold mode does `Down` on press and `Up` on release with the chord recorded at the press, without looking up the binding again; tap mode does `Tap` on press and nothing on release.

**Lifecycle**:

- **CancelAll(now)** (the engine's level of ReleaseAll, see §7.4): held controls that are not canceled get their cycle marked canceled, and their recorded chords are dropped; latched chords send their own `Up`, and the caller makes the Arbiter release the rest. A second call in a row does nothing.
- **Reload(now, prog)**: CancelAll first, then swap prog; when held controls are released, the decision carries the old binding and the reason `canceled`, and sends no key. **Reset(now)**: CancelAll first, then treat every control (the function key included) as released; used when a device disconnects.
- **Pause / Resume**: Pause does CancelAll first, then enters pause. A press during pause starts a cycle as usual, but counts as canceled at once: the decision's reason is `paused`, no key is sent, and its release is `canceled`. Resume sends no key and makes no decision; held controls must be released and pressed again to work. Reset, Reload and CancelAll all keep the pause.

### 7.8 Decision records

With `Options.Trace` set, every button edge, every trigger value crossing a threshold, every ignored input and every cycle ended by CancelAll produces one `Decision{T, Control, Edge, Cycle, Gesture, Level, Binding, Action, Owner, Reason}`. Stick axes and trigger values that cross no threshold produce none; with Trace nil, nothing is built.

| reason | Case | Output |
|---|---|---|
| `dispatch` | A press found an action | Down for hold, Tap for tap |
| `unbound` | No binding, or the binding has no action for this gesture; when a multi-tap window ends, no action for that tap count | None |
| `release` / `end` | The release of a cycle that holds a chord / that holds none | Up / none |
| `cancel` | CancelAll ended a cycle, or a window waiting for more taps was dropped | None; the Arbiter releases |
| `canceled` | The release of a canceled cycle (including presses during pause) | None |
| `spurious` / `paused` / `blocked` | An ignored input / a press during pause / a `block` binding consumed the press | None |
| `function` | An edge of the function key (no cycle; cycle is 0) | None |
| `script` | A script took the gesture, or consumed the release of a press still waiting | None (keys the script sends itself are not counted here) |

**Decisions do not match physical edges one to one.** Readers of decisions (the Diagnostics page, [ADR: Diagnostics](adr/0016-diagnostics.md)) judge by gesture and reason, not by edge: a press still waiting for its decision has no decision; a tap is first `unbound` and then `dispatch`; the edge of a `dispatch` for a hold, a tap, a multi-tap or a release action is always `down`.

**Decisions of scripts**: with a script gesture registry, when `resolveGesture` finds no action, `runAction` records one: `script` when the script took or claimed it; `blocked` when a level's `block` stopped it; `unbound` when every level passed it on, with the synthesized binding `script-resolution`, which padmated removes when publishing and uses to tell "still waiting" (§11).

**Fields and order**: owner is present only when output was sent; level and binding only when a binding was found; a function decision's level is `function:<function key>` and its binding `/functionSet/<control>`. A decision comes before the output it causes, and a cancel before the Arbiter's release; Trace is called on the engine's goroutine and must not call back into the Engine or the Slot. JSON is one object per line, with empty fields left out.

### 7.9 The function key

There is only ever one function key, like a laptop's Fn: it sends no key itself, and while it is held the other controls first use the profile's `functionSet`, and their usual bindings where it sets none. It is a setting of each controller (ADR-0017, §10.4), the same for every profile. What a controller may choose, and the default, depend on its family: `start` (default) or `back` for a controller with the standard layout; ZL (`lt`, default) or L (`leftshoulder`) on a single left Joy-Con, ZR (`rt`, default) or R (`rightshoulder`) on a single right one; `back` (default) or `start` for a pair of Joy-Cons. − and + are not offered on a single Joy-Con, and a paddle is a function key nowhere. The implementation is in `engine/function_keys.go`, and the design in [ADR: Mapping model](adr/0012-mapping-model.md).

**Per controller (ADR-0017).** config.json holds the function key of each controller, `devices.<DeviceKey>.functionKey` (§10.4), and `Config.PadSettings` resolves it for a family of controllers. The runtime follows it slot by slot, not profile by profile: padmated compiles every profile once, with the default function key (`start`), and each slot runs the variant of that Program with the key of the controller it has (`Program.ForFunctionKey`, compiled the first time a slot asks and shared after; the slot asks `Config.FunctionKey` when it gets or loses a controller, at each reload and at `Supervisor.RefreshFunctionKeys`, §5.6, §5.8). The engine can compile with back, start, leftshoulder, rightshoulder, lt and rt (`control.CanBeFunctionKey`); the retired top-level `functionKey`, back or start, is still the key of a controller with the standard layout that sets none. Changing the key of one controller (`pads.set`, config.json, a Joy-Con that turns into a pair, another controller in the slot) is an engine `Reload` of that slot only, which cancels what is held and releases its owners first, as below; the profile is not reloaded and no other slot is touched.

- **The compile option**: `engine.Compile(p, engine.WithFunctionKey(c))` and `Program.ForFunctionKey(c)`. The engine does not read config.json; padmated compiles with the default and the slots ask for the variants; a `c` that cannot be a function key counts as `start`. `View.ProgramFunctionKey` is the key of the Program the engine runs (`profiles.explain` shows it per slot).
- **The function key itself cannot be bound**: bindings written on the function key are parsed as usual and kept in the file, but `Compile` ignores them; when the function key changes, they work again. A trigger as the function key (`lt`, `rt`) is the trigger's virtual button, pressed and released at the profile's trigger thresholds (`analog.lt/rt` `press` and `release`, with their hysteresis); its second stage (`lt.full`, `rt.full`) and half axis (`lefttrigger+`, `righttrigger+`) are the same physical trigger and are never pressed, and bindings on them are ignored as well (`Program.onFunctionKey`). Those ignored bindings are only exempt from the compile checks: they still sit in their layer, `lookupWith` reads the function set directly, and a script's button handler makes every virtual control look bound, so the guard has to be in the engine's own press paths (PITFALLS). The level name of the set is `function:<control>`: `function:lt`, `function:rt`, `function:leftshoulder`, `function:rightshoulder`, `function:start`, `function:back`. Bindings on `back` and `start` in old files were never read and may be invalid: the format is frozen and a valid profile stays valid, so their errors are lowered to warnings and they are taken out of the model; invalid ones in `functionSet` are still errors.
- **`guide` is never mapped** ([ADR: Mapping model](adr/0012-mapping-model.md)): it is not the function key (`CanBeFunctionKey` is false; `WithFunctionKey(Guide)` compiles with `start`), `profile.Parse` takes every binding on it out of the model with a warning (its errors become warnings), `Compile` skips any that a hand-built profile has, and `lookupWith` finds nothing for it (`control.Control.Unmappable`), so a registry level cannot bind it either. In `stepButton` its edges update the physical state and return, like the function key's: no cycle, no buffer, no interruption of a pending gesture, no `Button`, `ButtonObserver` or script handler; the decisions are `unbound` and `end`. The script host refuses it as it does an unknown control (`scriptControl`), and `pad.isDown` reads it as up.
- **One set of bindings**: `Profile.EffectiveFunctionSet()` is the set the engine reads: the top-level `functionSet` if there is one, otherwise one synthesized from the old `functionKeys` format; when both are written, `functionSet` is used with a warning.
- **Its edges never enter gesture recognition**: after updating the physical state and before the buffer, an edge of the function key goes to `stepFunctionKey` and returns. So it starts no cycle, enters no buffer, interrupts no pending gesture of another control and sends no key, and neither script handlers nor `ButtonObserver` receive it; the decision is `function`. `pad.isDown` still reads the physical state.
- **State**: `Engine.fnActive`, which enters the slot snapshot through `View.FunctionKey` (used by `profiles.explain`, §11). CancelAll, Pause, Resume and a Reload with the same function key leave it alone; Reset clears it. **A Reload that changes the function key** clears it too (CancelAll first): even if the new function key is held at that moment, it selects `functionSet` only from its next press.
- **Lookup**: when the function key is held and `functionSet` binds the control, the set's binding comes after the script's `global()` and before the layers. A binding in the set is exactly like one in a layer: it owns every gesture (with no action for a gesture, it does nothing and does not fall to the layers below), and `block` consumes. The scope of a `script` action in the set is `"global"`.

## 8. Output

### 8.1 KeySpec

The implementation is in `internal/keys`: pure Go, standard library only, testable on Linux.

```
KeySpec   := Chord
Chord     := (Modifier "+")* Key
Modifier  := Ctrl|LCtrl|RCtrl|Shift|LShift|RShift|Alt|LAlt|RAlt|Win|LWin|RWin
Key       := named key | character | physical key | scan code | mouse key | media key | Modifier
Character := A–Z | 0–9 | - = [ ] ; ' ` \ , . / | "Plus"
Physical  := "Key" main-block character   // KeyZ, Key1; a fixed US position; there is no KeyPlus
ScanCode  := "sc:0x" hex{1,4}             // 0x01–0x7F, or E0 extended keys 0xE001–0xE07F
MouseKey  := WheelUp|WheelDown|WheelLeft|WheelRight | MouseLeft|MouseRight|MouseMiddle|MouseX1|MouseX2
MediaKey  := VolumeUp|VolumeDown|VolumeMute|MediaPlayPause|MediaNext|MediaPrevious|MediaStop
```

`padmatectl keys list` lists every key name, alias, scan code and VK; `padmatectl keys parse [--json] <spec>...` prints the normal form and draws `^` under the token in error.

- Character keys are resolved for the foreground layout; physical keys are fixed US positions; scan codes are sent as they are and are never modifiers. The Arbiter first normalizes modifiers without a side to the left key with `Chord.Sided()`, then counts by physical key. `Z`, `KeyZ` and `sc:0x2C` are three different `keys.Key`.
- Mouse keys have no scan code, VK or layout: a wheel press scrolls one notch and its release sends nothing; buttons can be held like keys. Media keys are keyboard keys with an E0 scan code and a VK, sent by VK. Neither can be a modifier, only the end of a chord; a mouse button cannot have typematic repeat (§7.7). New keys are appended to the key table, without changing schemaVersion.
- A character key's character comes from `Key.Char()`. The Resolver must look up the layout with `Char()`, not `String()`: on Windows an uppercase letter resolves to Shift plus the key, and `Ctrl+Z` would become Ctrl+Shift+Z. Characters that need Shift to type (`! @ #` and so on) are not keys; parsing reports an error suggesting `Shift+1`.
- **The US base mapping** (`Key.US()`): Pause has no single scan code and is sent as VK_PAUSE; NumLock's 0x45 becomes VK_PAUSE while Ctrl is held; the numpad scan codes 0x47–0x53 are translated by the system's NumLock, so Num0–Num9 and NumDecimal are sent by VK (§8.3); vkE8 is not in the key table and is injected only by MenuMask (§8.5).

**Layouts** (`internal/output/layout*.go`, [ADR: Keyboard and mouse output](adr/0004-output-sendinput.md)): `Resolver.Resolve` serves as `ArbiterOptions.Resolve` and is called only on the Arbiter's goroutine; live mode (dry run and injection alike) uses it, and replays use `ChordStroke` (US).

- **Fallback**: when the layout gives no usable key (no such VK or character; a scan code of 0, E1, out of range, or a numpad code without E0; the foreground layout cannot be read; and so on), the result of `ChordStroke` is returned without an error, with one warning per (spec, HKL) (padmated logs the warning `key sent as its US key`). Only an invalid chord returns an error.

**Typing text**: `KEYEVENTF_UNICODE` is for text only. Text is not a KeySpec; it is the argument of the `text` action, of a sequence's `{"text":…}` step (§10.3) and of the script's `key.type`. `keys.ValidateText`: 1–256 UTF-16 units of valid UTF-8, with no control characters except `\n` and `\t` (one tap of Enter/Tab each). `padmatectl keyboard-capture` finds keys in the key table by VK, and the capture hook never swallows physical keys.

### 8.2 Output Arbiter

The implementation is in `internal/output` and is cross-platform. The pitfalls are in PITFALLS "Keyboard output and layouts".

**Basic conventions**

- **owner**: `Owner{Slot, Gen, Source, Cycle}`, written `slot/profileGen/source/cycle` (for example `0/3/decl/17`). Source is `decl` or `script`; every command refuses an invalid owner. A declarative Cycle is a press cycle; the script Lifecycle assigns each runtime a non-zero cycle token, so on reload the old runtime's releases do not affect the new runtime.
- **Single-threaded**: the Arbiter takes no locks and is called by one goroutine only. In the daemon that is the locked `output.Loop` (T-output); other goroutines hand it commands through `Do` or `RemoteArbiter` (§5), so its Sender is the only caller of SendInput (invariant 3).
- **Commands**: `Down`, `Up`, `Tap`, `Repeat`, `ReleaseOwners`/`ReleaseOwner`/`ReleaseAll`, `Advance`, `NextDeadline`, `Held`, `Stats`, `SetBlocked`, `SetTiming`, `SetEventGap`; and the non-key `Move`, `Jump`, `TypeText` and `Sequence`. An invalid owner or chord, or a failed resolution, returns an error, and nothing is sent or changed.
- **The script seam**: `script.Host` calls these commands only through `OutputSink`, with an `SourceScript` owner; on a lifecycle switch, `ReleaseOwner` comes first, then the old Host disconnects its sink, so late output can never reach the Arbiter.
- **Physical keys**: a chord becomes press events through `ArbiterOptions.Resolve`, modifiers first and the main key last; a key is always released with the release of the event that pressed it, even if the layout changed after the press. Physical keys flow through MenuMask to the Sender (§8.5).
- **Reference counting**: each owner holds a set of chords; each hold references every physical key of its chord, and a tap in progress references only the keys it pressed itself. A key goes down on its first reference and up when the last reference goes away.

**Tap and Repeat**: Tap presses the keys not already down, and the tap references them until it ends; without a minimum press, the press and release go in the same batch; otherwise `Advance` ends the reference at now+hold, and the physical release is still subject to MinPress. When the main key is taken or within MinGap, the tap queues, and later taps merge (reported as dropped). Repeat works only on a chord the owner holds with Down, and sends one extra press of the main key.

**The wheel, mouse buttons, the pointer**

- **Wheel keys are momentary**: a press scrolls one notch, and the release sends no event (`KeyEvent.Silent`); the Arbiter still keeps account as for a key, and the release still goes to the Sender, which does not inject it (§8.3). A press always goes out, with no minimum press or same-key gap, and is not written to the held-keys log.
- **Mouse buttons hold like keys**, so every exit path lifts them (invariant 1). **A button held down never gets another press** (an app would take it for half a double click): `Repeat` sends nothing for it; while it is held by a `Down`, a tap of it by another binding is a no-op (for a chord with modifiers the whole tap is a no-op, since Shift alone switches the input method and Alt alone activates the menu bar).
- **Pointer movement is not a key** (`pointer.go`): `Move` moves relatively (the system's pointer speed still applies, and padmate does not change it), `Jump` goes to a 0–65535 virtual desktop coordinate. There is no hold; MinPress/MinGap do not apply; neither the ledger, the held-keys log nor MenuMask records it. It keeps its order with key events, and queued moves of the same owner merge; with the gate closed it is dropped, and `ReleaseOwners` drops queued moves. A failed Send is not retried, and the error is returned at most once per second.

**Typing text is not a key** (`arbiter_text.go`): `TypeText(owner, text, gap)` types character by character with `KEYEVENTF_UNICODE` (one Send per character), regardless of layout, with `gap` between characters. Unicode events enter neither the key table, reference counts, the held-keys log nor the startup sweep; `ReleaseOwners` and `SetBlocked(true)` end the text of matching owners. When a Send goes out only partly, the release of a press that went out is sent again once at once, the text ends there and `Dropped` is notified; if that release fails again, it is handled like a key waiting to be sent again (`Arbiter.stuck`). Whether the system records a held state for `VK_PACKET` has not been tested; it is recorded only to keep invariant 1.

**Sequences** (`arbiter_sequence.go`): `Sequence(owner, steps, hold, gap)` runs a list of `keys.Step` (tap, wait, type text; 1–64 steps). Like `TapSequence` it is a kind of `tapSeq`, and the whole sequence is canceled once its owner is released. When a step is dropped, the sequence goes on and `Dropped` is notified. A `Sequence` starts only after the `Sequence` before it (of any owner) has ended; at most 16 are queued, and more are dropped. `ReleaseOwners` (and so `ReleaseAll`, pause, reload, switching profiles, emergency release and exit) and `SetBlocked(true)` end matching sequences: steps not started are not sent, and a tap in progress is released as if canceled alone (invariant 1). A sequence never holds anything.

**Release** (the Arbiter's level of ReleaseAll): `ReleaseOwners(match)` ends the holds and taps in progress of matching owners, then lifts every key without a reference in one batch (keys waiting to be sent again included): non-modifiers first, then modifiers, each in reverse order of pressing. The full path, `slot.ReleaseAll`, is `engine.CancelAll` (§7.4) and then `ReleaseOwners` matching the owners of the same slot number and any generation. `Slot.Reload` releases the same way, and later owners use the new generation.

**Several slots**: the owners of every slot are in the same Arbiter and share one keyboard ([ADR: Several controllers](adr/0003-devices-and-slots.md)). Reference counts span slots: two slots holding the same key press it once, and it goes up only when the last reference goes away, whichever slot is released, removed, reloaded or panics first.

**Failed Sends** (invariant 1): state follows the commands, not the results of Send. A press that did not go out still counts as pressed, and its release is still sent when the last reference goes away: an extra release does no harm, while a missing release is a stuck key. When a batch goes out only partly, the releases that did not go out are sent again once at once; those that fail again wait to be sent again: the next `Down`, `Up`, `Tap`, `Repeat` or `Advance` sends them first, `ReleaseOwners` merges them into its own batch, and `NextDeadline` comes due after `RetryInterval` (100 ms by default).

**Event gap** (`pace.go`): config.json's `output.eventGapMs`, 0–50 ms, 0 by default (some apps miss a Ctrl/Shift sent in the same SendInput as the main key). Above 0, the events of every owner go into one queue in command order, one Send per event, at least that far apart. **Releases do not wait for the gap** (invariant 1): `ReleaseOwners`/`ReleaseAll` do one Send at once, with the queued events first, then the release of the remaining keys.

**The press gate**: `SetBlocked(true)` closes the gate; by itself it sends and releases nothing. While closed, valid `Down`, `Tap` and `Repeat` neither press nor hold; `Up`, `ReleaseOwners`, `ReleaseAll`, taps coming due and resends work as usual, so the gate never leaves a key stuck. The daemon uses it through `output.Loop.BlockFor` (§5): ReleaseAll first, then close the gate; pause (`BlockUser`) and system suspend (`BlockSystem`) each own a bit, and the gate opens only when all are open.

**The held-keys log** lets the next start after a kill release what was left. The path is `%LOCALAPPDATA%\padmate\run\held.bin` (the format is in `internal/output`), used only with `--inject`. It records keyboard keys, media keys and mouse buttons; the wheel, pointer movement and Unicode events are never written.

- **Identity**: `GetAsyncKeyState` cannot tell injected from physically held keys, so a log left by another boot or sign-in session must not be swept. The log stores the identity of the run that wrote it (the session ID, the token's logon session, the sign-in time), and only when all three are equal is it the same; otherwise it counts as empty: it is reset and written with this run's identity, and not a single key is released.
- **Write before sending**: before the ledger hands a batch to MenuMask, every press of the batch is added to the bitmap (SendInput may send only a prefix); after sending, it is changed to the keys it records as held. Each write is one WriteFile of the whole file, so a write cut off by a kill loses no key. When a write fails, keys are sent as usual, and a kill during that time may miss some; padmated never refuses to start because of an error in the log or the sweep.
- **The startup sweep** ("T-output" in §5): with the same identity, the keys in the log are intersected with the keys `GetAsyncKeyState` reports still down, and each gets one release through the Loop's own MenuMask. **Exit**: after Stop and after a panic, the log is rewritten with the keys still held in the ledger, and deleted when there are none.

**What triggers ReleaseAll**: exit, reload and switching profiles, disconnects, pause and the emergency combo (`Loop.Block(true)`), locking the screen/sleep/sign-out/shutdown/the secure desktop (`Loop.BlockFor(BlockSystem, true)`), unloading a script (by owner); a panic of T-output releases through the ledger (§5).

**Not implemented, and limits**: sending releases again while an elevated window or the secure desktop is up; dry run only, no injection, while padmate.exe is in the foreground; releasing on foreground changes; IME, Ctrl+Alt+Del, Win+L. A lone Alt/Win is masked; to open a menu, bind F10 or Ctrl+Esc (§8.5).

### 8.3 Injection and the tag

The implementation is in `internal/output`; the design is in [ADR: Keyboard and mouse output](adr/0004-output-sendinput.md). Each Send calls SendInput once, so no other input can get into the batch; keyboard and mouse events go out in order in the same call, so no other input can come between the Ctrl and the wheel of `Ctrl+WheelUp`.

| Event | How it is built |
|---|---|
| Scan code event | `KEYEVENTF_SCANCODE`, with `EXTENDEDKEY` for E0 keys |
| VK event | Pause, Num0–Num9, NumDecimal: sent by VK without `SCANCODE`; wScan still carries the scan code |
| Media key | Sent by VK, with the E0 scan code in wScan and `EXTENDEDKEY` |
| Menu mask key vkE8 | `MenuMaskEvent`, sent by VK, with wScan 0 |
| Unicode | `wVk` 0, `wScan` one UTF-16 unit, `KEYEVENTF_UNICODE` |
| Wheel | `INPUT_MOUSE`, `WHEEL`/`HWHEEL`, ±120, no position. A release produces no INPUT |
| Mouse button | `INPUT_MOUSE`, no position, happens at the cursor; X1 and X2 use `XDOWN`/`XUP` with `mouseData` 1 and 2 |
| Pointer movement | `MouseMove`: relative `MOUSEEVENTF_MOVE`. `MouseJump`: `MOVE\|ABSOLUTE\|VIRTUALDESK`, coordinates 0–65535 |

**The tag**: each event's `dwExtraInfo` is `Tag | nonce`: the high 32 bits are the ASCII "padm", the low 32 bits the nonce (the process id). The LL keyboard hook uses `output.IsPadmate` and `Nonce` to tell padmate's events from the physical keyboard and other injecting programs. The `dwExtraInfo` of the LL mouse hook has only the low 32 bits (tested) and no "padm" tag, so only the nonce can be used (`output.MayBePadmateMouse`). The tag can be forged and is used only for filtering.

### 8.4 Dry-run output

The dry-run printer is padmated's `output.Sender` for replays and for live `--dry-run` (through MenuMask, so the mask keys it prints are the same as when injecting). The columns, the kinds of lines and examples are in `padmated -h` and `cmd/padmated/testdata/acceptance.golden`. A decision line comes before the send lines it causes (§7.8). Live output changes with time and with the foreground layout, so it has no golden file; `TestLiveVirtualDryRun` and others check the key lines (§14).

### 8.5 Alt/Win menu masking

The implementation is in `internal/output/mask.go` and is cross-platform; the design is in [ADR: Keyboard and mouse output](adr/0004-output-sendinput.md).

Pressing and releasing Alt alone activates the foreground program's menu bar, and Win alone opens the Start menu. `MenuMask` wraps the Sender: before such an Alt/Win goes up, it inserts a press and release of the mask key vkE8 (unassigned in the VK table) right before it in the same batch. Whether this stops the menus is still to be confirmed on real hardware.

**Rules**: the menu keys are LAlt, RAlt, LWin and RWin; each is clean or dirty, and all start clean.

- A press of menu key K: K becomes clean, and the other menu keys dirty. A press of any other key (the mask key included): every menu key becomes dirty. A release of menu key K: if K is clean, the mask is inserted first, then K becomes clean; releasing other keys changes nothing. Only the prefix that the Sender reports sent changes the state.
- What gets masked: a lone `Alt`, `Win` or `RAlt` (AltGr too) and chords of modifiers only; `Alt+Tab`, `Win+E` and AltGr characters are not masked. Mouse keys and Unicode events are not keys and change nothing.

**Failures** (invariant 1 comes first):

- The mask key went down but its release did not go out: MenuMask records it and lifts it before every later Send; nothing went out at all: the resend decides again and masks as before. A Sender panic: the worst case is assumed (every menu key pressed in that batch counts as clean, and a mask key pressed counts as held).
- After a T-output panic, the ledger's release goes through MenuMask (§5). The Arbiter counts keys whose press failed as held too and still sends their releases (which get masked), while the ledger records only presses that went out, so the mask key may be held while the ledger is empty; then the ledger calls `releaseMask`: one Send with only the mask key's release. vkE8 has no character, so holding it should be harmless (not tested).

**Constraints**:

- Every output path of the daemon must go through exactly one MenuMask, below every Arbiter: `output.Loop`, padmated's replay, simtest's `WithMenuMask` (§14); `padmatectl send` and injecttest call SendInput directly and do not mask. MenuMask takes no locks and runs on the same goroutine as the Arbiter above it; all slots share one Loop, so it judges across slots by the physical key stream.

## 9. Foreground detection

`internal/winfocus` attaches to winsession's window and thread (§5.13).

**Detection.** `SetWinEventHook(EVENT_SYSTEM_FOREGROUND, MINIMIZEEND)`; the callback only posts a message. After a 50 ms debounce, the actual foreground is read again, not the event's stale HWND; the session timer catches up on missed events. A failed query or a zero HWND keeps the last valid foreground. Shell window classes that appear briefly and padmated's own windows are ignored. Each foreground change publishes `app.foreground`; programs other than padmate are deduplicated by path and recorded in the recent list (at most 12).

**Profile selection.**

- Reading the profile and switching are done serially on a separate worker; if a new foreground has arrived when a slow read finishes, the old choice is dropped. The library entry enables the automatic rules; an explicit `--profile` keeps that file. Automatic selection and the user's pause are independent.
- Selection follows the rules only (`profile.Select`): when the foreground program matches the first enabled rule whose `profile` is a string, that profile is used; otherwise the fixed default profile `default`. `enabled:false` keeps the current profile; rules whose `profile` is null are skipped (keeping their index, so `rules/N` pointers do not change).
- When `default` is deleted while running, it is created again first; if it still cannot be read or parsed, the built-in empty profile is used, so an unbound app never inherits the mapping of the previous profile. While a registered UI process is in the foreground, the current profile stays and nothing is released.
- `releaseOnSwitch`: `profileChange` (default), `always`, `never`. A change of library ID (switching to an empty mapping included) always releases the old owner through the `ReloadProfile` barrier, even with `never`; with the ID unchanged, only `always` releases. Turning automatic selection off releases nothing.
- Each successful selection publishes an immutable target, so a stale hot-reload callback cannot overwrite a newer selection. Selection, hot reload and changes of the script folder are serialized by one lock; slots never wait for this lock.

**Elevated windows and compatibility notices.** A foreground of high integrity is reported once per run and does not change the pause state. Releases refused by an elevated window or the secure desktop are retried from the Arbiter's ledger of failed releases (100 ms by default); after returning to the normal desktop, the failed releases are sent again, but not failed presses. A compatibility worker compares the slots' capability snapshots with the current bindings every second and tells the user about missing controls; besides, every 5 seconds it enumerates processes read-only and tells about Steam, DS4Windows and HidHide once each, without reading their internal state.

## 10. Formats

There are three JSON files that users see: `profile.json` (mappings), `config.json` (settings) and `state.json` (volatile state). The fields and their ranges are defined by `schema/profile.schema.json`, `schema/config.schema.json` and `schema/state.schema.json` (every property has a `description`). This section covers only the top-level structure, validation, and the rules of reading and writing. The decisions are in [ADR: Profile format](adr/0007-profile-format.md).

### 10.1 Disk layout

The folder structure is in §3. The data folder is `%APPDATA%\padmate` by default; `--data-dir` changes it.

**The profile library** (`internal/profilelib`)

- `profiles/<id>/profile.json`. An ID is 1–64 lowercase ASCII letters, digits, `-` and `_`, starting with a letter or digit, excluding Windows' reserved device names.
- Reads and hot reload are confined to `profiles/` through `os.Root`, refusing symbolic links and junctions; a profile is at most 4 MiB. Reading never writes.
- The **revision** is the SHA-256 of the file's raw bytes, so outside edits change it too; IPC writes use it to detect conflicts.
- A profile in the library that is broken, unreadable or of too new a version still has an entry, with diagnostics, so the UI can repair it.

**Versions and migration**

- `profile.CurrentSchema` is 1. `Parse` migrates before validating, so callers do not call `Migrate` themselves. Versions above the current one are refused (`upgrade padmate`).
- Migration happens in memory only, and **the daemon never rewrites a file for being old**: the bytes on disk and the revision stay as they are, and every kind of write (put, copy, restore, import and export, renaming macros) edits by byte position.
- The format is frozen: from now on only optional fields are added; a change that could break old files needs a migration step, `CurrentSchema` plus one, a change to the schema's `const`, and then `task gen`. The `schemaVersion` of config.json and state.json is 0.

**Save snapshots** (`profilelib/history.go`)

- Before `Put` replaces a profile.json with different bytes, under the same write lock it stores the old bytes as `profiles/<id>/.history/<UTC time>-<first 8 hex of SHA-256>.json` (without `.json`, that is the version), writing a temporary file beside it, Sync, rename.
- Nothing is stored when it equals the newest snapshot, for a new profile, or when the same bytes are saved. Each profile keeps at most 30, deleting the oldest. A failed snapshot does not affect the save. When `.history` is a link or a file, it is not followed.

**Export files** (`profilelib/archive.go`, `import.go`)

- A profile exports as one zip (`.padmate`): `manifest.json` at the root (`{"type":"padmate-profile","format":1,"name","padmate"}`) and `profile/` (the content of the profile folder, without `.history`). The library macros profile.json refers to, together with their relative imports, go into `profile/macros/`.
- Under the write lock, export reads into memory within the limits of copying (256 entries, 32 MiB, 8 levels, refusing links and special files); each file is at most 4 MiB, and only scripts, JSON and text files are included.
- **Import** (`ReadArchive`) checks everything before writing to disk: only unencrypted store/deflate zips with exactly one manifest; it refuses entries outside `profile/`, absolute paths, `..`, backslashes, device names, special entries and names that differ only in case; it counts the bytes after decompression (a zip bomb stops at 32 MiB); scripts and JSON must be UTF-8; profile.json passes the same validation as `Put`.
- Scripts go into the script library: one with the same name and content is reused; for the same name with other content and no `macros` given, -32022, and nothing is written; `macros:"replace"` overwrites, and `"keep"` gives the imported script the next free "Name 2" and rewrites the references.
- A new profile's ID is made from its name (`NewID`); a duplicate name (trimmed, NFC, case-insensitive) is refused. `Library.ImportProfile(data, name, unique)` takes a converted profile.json down the same path as an export file holding only a profile.json; with `unique` true, a duplicate name gets a number (used by the AntiMicroX import, §11.1).
- How it writes: everything is written and Synced in a hidden `profiles/.tmp-import-*`, and under the write lock the name is checked again before the rename. Replacing an existing profile first renames the old folder to `.old-<id>-*`, then renames the new one, then stores the old profile.json as a snapshot and moves the old `.history` into the new folder. If it is interrupted between the two renames, the next copy, import, delete or start renames a `.old-<id>-*` back when `<id>` does not exist, and deletes it when it does.

**The script library** (macros)

- `<data folder>/macros/`, independent of the profiles and shared by all of them. Each top-level `.ts`/`.js` (not `.d.ts`) is a macro, at most 64 in the whole library (`profile.MaxMacros`); modules in subfolders can be imported and are not macros; imports must not leave the library.
- **Migrating scripts** (`Library.MigrateMacros`): when library mode starts (after it has the instance lock; never in a replay), the old-style `macros/` of each profile folder is moved into the library. One with the same name and content is deleted; one with the same name and other content gets the next free name, and that profile's references are rewritten. Before changing a profile, `<data folder>/macros-migration-<profile ID>.json` is written, every step can be done again, and a migration cut off is finished at the next start. A failure is only logged and does not stop the start.

**The default profile and the starting choice**

- In live mode, when `profiles/default/profile.json` does not exist, the fixed default profile `{"schemaVersion":1,"name":"默认","layers":{"base":{}}}` (named "Default") is written; an existing one is kept as it is. A replay only reads, and uses the built-in empty profile (`DefaultProfileText`) when it is missing or cannot be parsed.
- **Shipped profiles** (`profilelib.EnsureBundled`, ADR-0008): the profiles in `internal/profilelib/bundled/<id>/profile.json` are embedded in padmated. A live start of a release build (not `developmentBuild()`, so neither the UI's dev daemon nor the packaged test mode) offers each ID not yet in `<data>\bundled.json` (`{"offered":[…]}`): it is written when the library has no profile of that ID or name, and then `{"exe":…,"profile":…}` is appended to config.json's rules, fields kept in their order and indented, unless a rule's exe already names that file; the settings of the start are read again. A `bundled.json` that does not parse offers nothing. Now: `csp` ("CSP") for `CLIPStudioPaint.exe`.
- With only `--data-dir`, the last choice in state.json is restored; with no memory, when last time was disabled, or when the remembered profile is gone, `default` is used. A missing `--profile-id` still fails the start; it is only the starting profile, and with automatic selection on, the first evaluation of the foreground about 50 ms after the start chooses again by the rules (§9).

**state.json**

- Content: `lastProfile`, `lastProfileSet` and the layer state per device and profile (`devices[DeviceKey].profiles[id]`: `base`, `toggled`, `cycles`). `lastProfileSet:true` with an empty `lastProfile` means it was disabled last time.
- `controllers`: the controllers that have been connected, by canonical DeviceKey: `{name, type, buttonMask?, lastSeen?}` (SDL's name and short gamepad type, `DeviceInfo.ButtonMask`, milliseconds since the Unix epoch), at most 64, the least recently seen dropped first. It is what the settings page lists (config.json holds only settings), and it is machine state: only the daemon writes it. The router's `OnInput` callback marks it dirty when a gamepad is added or remapped; the persistence worker then asks the router for the devices (`Supervisor.Devices`, which knows the DeviceKeys) and calls `Memory.RememberController` (a map write under a mutex, no I/O) before its 500 ms flush. A file without the member has none listed.
- `joycon2`: the remembered Joy-Con 2 controllers (Bluetooth LE), an array of `{addr, serial, side}` (`addr` `AA:BB:CC:DD:EE:FF` in upper case, `side` `L` or `R`, at most 64, oldest first). It is machine state, not a setting: only the daemon writes it. `Memory.RememberJoyCon2` is called on T-input (a map write under a mutex, no I/O) when the pairing window has connected a controller, `joycon2.forget` calls `Memory.ForgetJoyCon2`, and the background flush below writes the file. A file without the member has none remembered.
- `profilelib.Memory` merges in memory and flushes every 500 ms in the background; on exit it flushes one last time after output has stopped, waiting at most 1 s. It validates, then writes a temporary file, Syncs and renames.

**Automatic repair of config.json** (`profile.RepairConfig`, `profilelib.ReadRepairedConfig`)

- When the library entry starts, or on any read of config.json that finds errors, the original bytes are first backed up as `<data folder>/config.broken-YYYYMMDD-HHMMSS.json` (`O_EXCL`, never overwriting), then the file is replaced. If the replacement fails, the backup is deleted, the repaired settings stay in memory only, and only the first time is logged.
- Every valid member is kept; each error removes the smallest member with a default that contains it; an error in a list removes the whole list, except `rules` (only the rule in error is removed). A file that cannot be parsed at all is replaced as a whole by `{"schemaVersion": 0}`. The repaired file always parses (`FuzzConfig`).

**Inheritance** (the format's rules are in §10.3)

- A child profile's profile.json is still its own text; saving, the revision and snapshots all follow that text, and it is never rewritten for having a parent.
- Validation of profile text in the library always uses `Library.Parse(id, data)`, so a `Put` of a child requires its parent to be in the library, without a cycle. Writing a parent does not check its children (they report at load time). `Copy` keeps `extends`.
- **Deleting a profile others are based on** (`Library.Remove`): first every profile based directly on it is written out with its merged full content, without `extends` (under the same write lock, each with a snapshot). All are merged and checked first, and if any cannot be written (`*ChildError`: it has errors of its own, or inherits an old-style `script`), nothing is touched; an error in the middle of writing writes the original bytes back for those already written, all or nothing. A profile whose profile.json cannot be read is treated as not a child.

### 10.2 A profile.json example

```json
{
  "schemaVersion": 1,
  "name": "Painting",
  "defaultLayer": "base",
  "timing": { "holdMs": 250 },
  "common": {
    "rightstick": { "label": "Switch mode", "press": { "type": "layer", "layers": ["base", "paint2"], "op": "cycle" } }
  },
  "layers": {
    "base": {
      "south": { "label": "Undo", "press": { "type": "keys", "keys": "Ctrl+Z" } },
      "east":  { "label": "Hand", "press": { "type": "keys", "keys": "Space", "mode": "hold" } },
      "west":  { "label": "Pen/eraser", "tap": { "type": "keys", "keys": "F13" }, "hold": { "type": "keys", "keys": "F14" } },
      "north": { "label": "Next brush", "tap": { "type": "script", "name": "cycleBrush", "args": { "dir": 1 } } },
      "leftshoulder": { "label": "Alt layer", "press": { "type": "layer", "layer": "alt", "op": "hold" } },
      "misc1": { "block": true }
    },
    "paint2": { "south": { "press": { "type": "keys", "keys": "F15" } } },
    "alt":    { "south": { "label": "Redo", "press": { "type": "keys", "keys": "Ctrl+Shift+Z" } } }
  },
  "functionSet": {
    "south": { "label": "Redo", "press": { "type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap" } },
    "ls.cw": { "press": { "type": "keys", "keys": "Ctrl+]", "mode": "tap" } }
  },
  "analog": {
    "ls":   { "mode": "fourWay", "deadzone": 0.15, "directionThreshold": 0.30, "directionDelayMs": 0 },
    "rs":   { "mode": "standard", "deadzone": 0.15 },
    "dpad": { "mode": "buttons" },
    "lt":   { "press": 0.30, "release": 0.20 }
  },
  "script": { "entry": "main.ts", "api": "0.21" }
}
```

`$schema` is only for editors to find the schema; the daemon ignores it. More examples are in [examples/profiles/](../examples/profiles/).

### 10.3 Binding syntax

**Top-level members**: `schemaVersion`, `name` and `layers` (which must have `base`) are required. Optional: `extends`, `defaultLayer`, `controlNames` (retired: accepted and checked, read by nothing, dropped by the UI on save; [ADR: Editor UI](adr/0013-editor-ui.md)), `timing`, `analog`, `feedback`, `script` (`entry`, `api`, `params`, `tickHz`), `common` (the bottom layer shared by every layer), `functionSet` (the set while the function key is held, §7.9) and `functionKeys` (the old format).

**Layers**: a map from control names (§7.1) to bindings, plus an optional `color`. An alias and its standard name cannot both appear in one layer. The targets of layer actions must exist.

```
Binding := { label?, block?: true, press?, release?, tap?, doubleTap?, tripleTap?, quadTap?, hold?,
             timing?, policy?: "holdPreferred"|"balanced"|"timeOnly", latch?: true, haptic?: <preset name> }
Action  := {type:"keys", keys: KeySpec|KeySpec[], mode?: "tap"|"hold", repeat?: {...}}
         | {type:"layer", layer, op: "hold"|"toggle"|"switch"} | {type:"layer", layers, op:"cycle"}
         | {type:"script", name, args?} | {type:"profile", profile} | {type:"macro", file}
         | {type:"mouse", op:"precision", factor?} | {type:"mouse", op:"jump", x, y}
         | {type:"hud", op:"hints"}
         | {type:"sequence", steps: Step[]} | {type:"text", text}
Step    := {keys: KeySpec} | {wait: 1..10000} | {text}      // one member per step, 1 to 64 steps
```

Combinations of gestures and actions (the rules are in §7.7):

| Gesture | Default mode | keys action | layer action |
|---|---|---|---|
| press, hold | hold | hold or tap; a KeySpec array only with tap and without repeat | every op |
| tap / doubleTap / tripleTap / quadTap, release | tap | tap only, without repeat | anything but `hold` |

`macro`, `sequence` and `text` can be used on every gesture.

**Notes on actions**

- **keys**: mouse buttons and the wheel are keys in a KeySpec (§8.1), such as `"Ctrl+WheelUp"`, not an action type. The rules of repeat are in §7.5.
- **script**: needs a `script` section. **macro**: runs a script of the script library, with the name rules of `profile.CheckMacroFile`; `Parse` does not check that the file exists, the runtime reports "not found", and `padmatectl validate` gives a warning. It needs no `script` section, and `script.entry` cannot be in `macros/`.
- **profile**: `profile` must be a valid library ID. The engine only submits a plain data request, and the slot reads no disk; the switch takes the serial path shared with automatic selection (a 16-entry request queue carrying the profile gen, where late old requests are dropped); a failed load keeps the current profile; switching to a different profile always releases the old keys first. A standalone file needs `--data-dir` to use it.
- **mouse**: `precision` (`factor` 0.05–1, 0.3 by default) can only be on `press`/`hold`, and slows sticks in mouse mode while held; `jump` (`x`, `y` 0–1) can be used on any gesture.
- **hud**: `hints` can only be on `press`/`hold`; while held, the overlay lists the labels of the controls of the current layer stack.
- **sequence / text**: `text` follows the rules of `keys.ValidateText` (1–256 UTF-16 units; the only control characters accepted are `\n` and `\t`). Steps tap with `tapPressMs`, then wait `tapGapMs` (20 ms by default) before the next step; when the same binding fires again, it queues behind ("Sequences" in §8.2); pause, reload, switching profiles, emergency release and exit all end what is not done.

**Other members of a binding**: `block` consumes the control without producing an action; `timing` overrides the four times (an explicit 0 is kept); `policy` is the interruption policy (§7.2); `latch` keeps the held chord until the next press of the same control; `haptic` touches once at each gesture's dispatch (§7.4), and is accepted on a binding without actions but does nothing there.

**analog**: `ls`/`rs` (`mode`: `standard`, `eightWay`, `fourWay`, `fourWayDiagonal`, `knob` or `mouse`, plus dead zone, thresholds, knob, mouse speed and so on), `dpad`, `lt`/`rt` (`press`, `release`, `fullPress`, `fullRelease`, `curve`), `axisThreshold`. The ranges are in the schema, the defaults in §7.6. The main checks: `fullRelease` must not exceed `fullPress`, and a `fullPress` that is written must be above `press` (not checked when not written, so old files still load); `minRateHz` must not exceed the effective `rateHz`, and can only be used on stick directions, `active`, the rings, half axes and triggers; `precision` and `hints` on a gesture other than press/hold are errors. The control keys also include `ls.inner ls.outer ls.cw ls.ccw rs.* lt.full rt.full`.

**Feedback switches**: `rumble`, `led`, `pause` and `lowBattery` in a profile's `feedback` are deprecated: they still pass validation without warnings, but are no longer read. The four switches are a setting of each controller, `devices.<DeviceKey>.feedback` in config.json (§10.4), and the UI removes them when it writes a profile.
What the four switches (true by default) do: `rumble` governs all rumble (layer switch and pause pulses, binding touches, low battery, scripts and error buzzes; identifying a controller ignores the switch); `led` governs layer colors, the pause color, low-battery flashes and script colors, and turning it off releases the colors set and gives the player lights back to SDL; `pause` governs the pulses of pause and resume and `pausedColor`, working only through `rumble`/`led`; `lowBattery` governs the low-battery warnings of the tray, the overlay and the controller itself (one warning when discharging at ≤ 25 %; re-armed by charging, plugging in or ≥ 60 %; fixed thresholds). With the profile's `feedback.strength` at 0, no spontaneous rumble is sent at all. The switches are those of each controller (ADR-0017; the engine, the scripts' `Effects.Switches(slot, key)`, the error buzz and the low-battery tracker each ask for their own controller's `DeviceKey`). Committing config.json does not reload the profile: when the resolved switches of a connected controller changed, `applyLiveSettings` calls `Supervisor.RefreshFeedback` so each slot sends its layer and pause state again, and a switch going from on to off stops that controller's rumble and script colors still running; nothing is asked when no connected controller's switches changed. Scripts' rumble and lights go through the `Effects` interface of `internal/script/effects.go`; `host.has("rumble")`/`host.has("led")` are true only when both the capability and the switch allow it; the Host cancels the effects a script holds on `ReleaseAll`, `Suspend` and `Dispose`.


**The two ways to write the function key**

- `functionSet` (current): written like a layer (without `color`), and can bind any control, `back` and `start` included; the entry on whatever is the function key at the time is ignored when compiling. A binding on `guide`, here as anywhere, is left out with a warning (§7.9). The binding IDs of the parsed `Profile.FunctionSet` are `/functionSet/<control>`.
- `functionKeys` (the old format, still valid): `{"back"?: a set, "start"?: a set}`, which cannot bind `back`, `start` or `select`.
- The engine reads `Profile.EffectiveFunctionSet()`: `functionSet` if there is one, otherwise one synthesized from the old format (§7.9); when both are present, `functionKeys` is ignored with a warning (pointer `/functionKeys`).

**Inheritance** (`extends`)

The value is the ID of another profile in the library. The child writes only what it changes; the rest comes from the parent.

- **It works only in the profile library.** Written in `--profile FILE`, in the profile.json of an import archive, in `profiles.validate` without a library, or in `padmatectl validate` outside the library, it is an error. The library's entry point is `profile.Resolve(data, &profile.Library{ID, Load})`; `profile.Parse` is the form without a library.
- **The chain**: at most 4 parents (`profile.MaxExtendsDepth`). With a cycle (pointing to itself included), a parent not in the library, unreadable or with errors of its own, the child does not load, and the error says which parent. Each level is parsed on its own first, so a parent must itself be valid.
- **Merging** works on the documents, not the model: the parent's already merged document is taken and then overridden by the child:

| Member | Rule |
|---|---|
| `layers` | By layer name; a layer on both sides is merged by control |
| Controls in a layer, `common`, `functionSet` | Each control replaced as a whole (`label`, gestures, `timing` and `haptic` are not inherited); an alias and its standard name count as the same control |
| `functionKeys` | Each side is synthesized into a set, then merged as `functionSet`, and the result is written as `functionSet`; when only one side has it, it is copied as it is |
| A layer's `color`, `defaultLayer`, `$schema` | The child's when the child writes it |
| `timing`, `feedback`, `controlNames` | By member |
| `analog` | By member, one level deeper (each setting overridden on its own) |
| `name` | Always the child's |
| `script` | Replaced as a whole when the child has one, inherited otherwise; an inherited `entry` is relative to the folder of the profile that wrote it (`ScriptConfig.Origin` is the library ID) |

- **Diagnostics point at the file with the problem**: every place in the merged text maps back to the position in the file that wrote it (`Diagnostic.Profile` is the parent's ID; empty means the file being checked). When a parent has errors, the child gets a summary at `/extends`; errors that arise only from the two together are at `/extends` too. `Diagnostics.Local()` takes the diagnostics in the checked file only (IPC and the UI give only those).

### 10.4 A config.json example

```json
{
  "schemaVersion": 0,
  "enabled": true,
  "releaseOnSwitch": "profileChange",
  "restoreLayerState": true,
  "rules": [
    { "exe": "C:\\Program Files\\Paint\\paint.exe", "profile": "paint", "device": "any", "enabled": true },
    { "exe": "krita.exe", "profile": "paint", "enabled": false }
  ],
  "output": { "eventGapMs": 0, "sendToOwnWindow": false },
  "devices": {
    "<deviceKey>": {
      "ignored": false,
      "functionKey": "back",
      "feedback": { "rumble": false },
      "panic": { "buttons": ["back", "start"], "holdMs": 2000 },
      "mapping": "<GUID>,*,a:b0,b:b1,leftx:a0,lefty:a1,back:b6,start:b7,platform:Windows",
      "calibration": { "ls": { "deadzone": 0.22, "saturation": 0.90 }, "rs": { "deadzone": 0.18 } }
    }
  }
}
```

Top-level members: `enabled` (the switch of automatic selection, true by default; it does not change a manual pause), `releaseOnSwitch`, `restoreLayerState`, `rules`, `timing` (the four global times, below the profile and the binding; an explicit 0 is not overridden by the default), `output`, `joycon2.autoConnect` (true by default: a remembered Joy-Con 2 connects by itself when its SYNC button is held; applies at once, needs no restart), `devices` (the settings of each controller, below). Three members moved into `devices` with ADR-0017 and are **retired**: `functionKey`, `feedback` and `safety.panic`. The daemon still reads them (`Config.FunctionKey`, `Feedback`, `Safety`; `Config.Legacy` says which the file has) as the value of a controller that has none of its own (the resolution is below), and `pads.migrate` folds them into the controllers and takes them out; nothing new is written to them. What the retired members say is unchanged: `functionKey` is `start` (default) or `back`, and the retired `guide` still passes the schema and is read as `start`; `feedback` has four switches, true by default; a `safety.panic` combo with `guide` loads with a warning and is not used, its family keeping the built-in combo (`PanicCombo.Usable`). The deprecated members `defaultProfile`, `onUnmatched`, `ignore` and rules whose `profile` is null: old files load as usual and `config.put` accepts them as usual, but they are ignored, and the UI removes them on every write.

**Rules** (automatic selection, §9): the first enabled matching rule wins; with no match, `default` is used. `exe` is a file name or a full path, case-insensitive, with `/` and `\` equivalent; titles are not read and wildcards are not supported. `device` accepts only `"any"`.

**Taking effect at once**: `enabled`, `releaseOnSwitch`, `rules`, `timing`, `safety`, `output.*`, `functionKey`, `calibration`, `feedback`, `devices.*.functionKey`, `devices.*.feedback`, `devices.*.panic` and `devices.*.mapping` take effect as soon as they are saved (`applyLiveSettings`); a device's `ignored` and `restoreLayerState` are read at startup, and a change reports `restartRequired`. A change of a function key (a controller's, or the retired top-level one) does not reload the profile: `applyLiveSettings` calls `Supervisor.RefreshFunctionKeys`, and only the slots whose controller's key changed switch Program, releasing their own held output first (§7.9). A change of a combo calls `RepickCombos`, which disarms only the controllers whose combo changed (`ComboWatch.Repick`), and a change that touches none of the three settings of any controller does nothing of the kind.

**The settings of each controller** (ADR-0017). A controller is a `DeviceKey`; a pair of Joy-Cons is a controller of its own, with its own key. `devices.<DeviceKey>` may hold, besides `ignored`, `mapping` and `calibration`:

- `functionKey`: `start`, `back`, `leftshoulder`, `rightshoulder`, `lt` or `rt`. Which of them the controller can have depends on its **family** (`internal/padfamily`; `input.FamilyOf(info)` derives the family from `DeviceInfo.Type`, `padfamily.FromGUID` from a key alone). The families are `standard`, `joyconLeft` and `joyconRight` (a single Joy-Con, always upright), `joyconPair` and `unknown`:

  | Family | Function key (default first) | Why |
  |---|---|---|
  | standard layout (Xbox, PlayStation, Switch Pro, GameCube, unknown) | `start` (Menu, Options, +), `back` (View, Create, Share, −) | the standard buttons |
  | single Joy-Con, left | `lt` (ZL), `leftshoulder` (L) | ZL is the big trigger under the index finger; − and + are out of reach while a Joy-Con is held, and the paddles are SL and SR |
  | single Joy-Con, right | `rt` (ZR), `rightshoulder` (R) | likewise |
  | pair of Joy-Cons | `back` (−), `start` (+) | the standard buttons |

  `guide` is never one, and neither is a paddle. A trigger is the virtual button of its axis at the profile's trigger thresholds (§7.9). For a standard controller or a pair, a value that the controller cannot have is a config error where the key's GUID settles it (`padfamily.PossibleFunctionKeys`: the vendor and product ids in the GUID name a Joy-Con or, for any other GUID, a controller with the standard layout; a key without ids accepts every function key), so `RepairConfig` takes out just that member. For a single Joy-Con, or a key without ids, a value that is a control but not one of the options (a paddle, `back`, `start`) is no error: `ParseConfig` ignores it with a warning and the default of the family applies, so an older file still loads and the rest of it applies. A value that the *running* family refuses is skipped in the same way.
- `feedback`: the four switches of the retired top-level object (`rumble`, `led`, `pause`, `lowBattery`; each is on unless it is false; a switch that is left out is not set).
- `panic`: an emergency combo `{buttons, holdMs}`, the shape of an entry of the retired `safety.panic` (2 to 8 canonical physical buttons, `holdMs` 500 to 60000; a combo with `guide`, or with a button the controller lacks, is not used and the built-in combo of the family applies).

**Resolution** (`Config.PadSettings(key, family)`, pure; a family that is Unknown is taken from the key's GUID): for each of the three settings the first that applies. (1) The controller's own member of `devices.<key>`. (2) The retired top-level member, where it applies: `functionKey` only to a controller with the standard layout (Standard or Unknown; Joy-Cons, a pair included, have their own defaults), `feedback` to every controller, switch by switch, and for `safety.panic` the entry of the controller's family (`standard` for a pair too) if usable. (3) The default of the family: its first function key, all switches on, its built-in combo (below). `PadSettings` also says from which source each setting came (`device`, `legacy`, `default`). `Config.Materialized(key, family)` returns the controller's entry with what the retired members give it written in, where that differs from the defaults of the family; resolving it without the retired members gives the same settings, which `MigratePadSettings` relies on.

**Per-device calibration**: `devices.<DeviceKey>.calibration.ls/rs` has an optional `deadzone` and `saturation` (0–1), overriding the profile's values of the same name (the order is in §7.5); triggers are not calibrated. When both members are given, `saturation` must be above `deadzone`; when only one is, the combined pair is checked at run time, and when it does not hold, that stick is ignored with a warning.

**Custom controller mappings**

- `devices.<DeviceKey>.mapping` is an SDL mapping string (1–2048 characters), written `GUID,*,a:b0,…,platform:Windows`: the GUID is the controller's, the name is written `*`, and there is no `type:` (it would change `DeviceInfo.Type`, by which the family of the emergency combo is chosen).
- SDL applies mappings by GUID, so **every DeviceKey of the same GUID must have the same mapping**; `devices.setMapping` writes it into all such keys together. Two mappings with different bindings make the later one an error (`Equivalent`). A mapping with errors is removed on its own by the repair.

**The default emergency combos** (`input.DefaultCombo`, `padfamily.Family.DefaultPanic`), held for 2000 ms by default:

| Family | Combo |
|---|---|
| standard, a pair of Joy-Cons (left and right connected together) | back+start |
| joyconLeft (a single left Joy-Con) | paddle2+paddle4 (SL+SR) |
| joyconRight (a single right Joy-Con) | paddle3+paddle1 (SL+SR) |

After `config.put`, `RepickCombos` changes the combo at once ("Pause and the emergency combo" in §5). A family not overridden keeps the default; so does a controller whose ButtonMask lacks a button of the combo (`effectiveCombo`). The buttons must be canonical physical buttons, 2–8 per combo, and `holdMs` is 500–60000. A combo that names a button the controller lacks is ignored with a warning when config.json is read (`PanicCombo.UsableFor`, `Family.HasButton`), whether it is a controller's `panic` or an entry of the retired `safety.panic` (`joyconLeft` or `joyconRight` with a button the single Joy-Con does not have), and the built-in combo applies. The members `joyconLeftVertical` and `joyconRightVertical` of `safety.panic` are retired (`padfamily.RetiredPanicFamilies`): they are accepted, ignored with a warning, and the built-in combo applies. Nothing in the file is migrated; the rest of it applies as written.

### 10.5 Validation and writing

The schemas use draft 2020-12, with only the keywords that VS Code and Monaco support (no `unevaluatedProperties`, no `$dynamicRef`).

**Three layers of validation** (`internal/profile`): JSON syntax → JSON Schema → semantics. A layer does not run when the one before it has errors; warnings do not count.

- **Syntax**: our own strict scanner, which also builds an index from JSON Pointer to byte offset. It reports trailing commas, comments, single quotes, duplicate keys, invalid UTF-8, numbers out of range and so on, stopping at the first error, and skips a UTF-8 BOM. The three files share it.
- **Schema**: the embedded copy of `schema/profile.schema.json`. An unknown control name gets a suggestion of a close name (first the names printed on controllers, such as `Cross` → `south`, then by edit distance); an unknown action type lists every type.
- **Semantics**: KeySpecs parse, with errors located inside the string; an alias and a standard name in one layer do not refer to the same control; a binding without actions gets the warning `binding has no action`; layer names and the targets of layer actions; the action rules (§7.7); the content of `sequence` and `text`.

**Diagnostics**: `{severity, pointer, offset, line, col, message}`. offset is a byte offset from 0, counted from the start of the file (the BOM included); line and col start at 1, and col counts Unicode code points (VS Code and Monaco count UTF-16, so convert from offset). They are sorted by offset.

**`padmatectl validate [--json] [--function-key KEY] [--merged] <profile.json>...`**

- One line per diagnostic, `path:line:col: severity: message (pointer)`, without the pointer when it is the root; a file without diagnostics prints `path: ok`.
- `--json`: one line per file, `{"file", "ok", "diagnostics"}`, where `ok` means no errors.
- `--function-key KEY` (`start`, `back`, `leftshoulder`, `rightshoulder`, `lt` or `rt`, `start` by default): the function key to compile with, instead of a controller's setting (validate does not read config.json); other values exit with code 2.
- **Exit codes**: 0 when no file has errors (warnings allowed); 1 when some file has errors; 2 for a usage error or a file that cannot be read (taking precedence over 1). An unreadable file is reported to stderr, and the others are checked as usual.
- A file without errors is then compiled with `engine.Compile`, and after the diagnostic lines one line per binding prints its added latency: `  <pointer> ["label"]: press +0 ms`; with a haptic touch, `, haptic tick` is added; without actions, `no action`. The control names in the pointer are as in the file; the label uses `strconv.Quote` and is left out when absent. The function key's set comes after common.
- With `--json`, the object of a file without errors has one more array, `bindings`, each entry `{"id","control","label","gesture","haptic","addedLatencyMs"}` (control is the standard name; label, gesture and haptic are left out when absent); a file with errors has no such field.
- A failed compile reports at 1:1 the error `cannot compile the profile: …`, with exit code 1.
- **Macros**: a referenced macro that is not in the script library (by exact name, `profile.MissingMacros`) gets the warning `macro file "x.ts" is not in the macros folder` at the `file` string. A library profile (path `…/profiles/<id>/profile.json`) looks in `<data folder>/macros`, other files in the `macros/` next to them.
- **Inheritance**: when a library profile has `extends`, its parents are read from the same library and the merged profile is checked; the file's own diagnostics come first, then each parent's (with the parent's path and positions, and one more field, `profile`, in `--json`). `extends` in a file outside the library is an error.
- `--merged` (not with `--json`, or exit code 2): instead of printing bindings, writes each file's merged full text to stdout (files without `extends` as they are); diagnostics go to stderr, and files with errors write nothing to stdout. `padmatectl trace --profile` merges library profiles first as well.

**Writing**

- Strict JSON, without comments.
- The daemon is the only writer: it writes a temporary file and renames it (an atomic write), every write carries a revision, and a snapshot is stored before a profile.json is replaced (§10.1).

**Hot reload** (`internal/watch`)

- stat every 100 ms, comparing size and modification time; after a change, the file must be stable for 200 ms before it is read, and it is stat'ed again after reading; if it changed again, the wait starts over. So a run of writes counts once, noticed 200–400 ms after the last write.


### 10.6 Event JSON lines

The output of `padmatectl monitor --json`, recordings made by `padmatectl record` and padmated's fake input are one format, implemented in `internal/input/eventjson.go` (`WriteEvent`, `ReadEvents`). One object per line:

```
{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"...",...}}
{"timeNs":5000000,"kind":"down","device":1,"control":"south"}
{"timeNs":9000000,"kind":"axis","device":1,"control":"lefttrigger","value":32767}
```

**Fields**

- `timeNs` is in nanoseconds. `kind` is `added`, `removed`, `down`, `up` or `axis`. `control` is present only on down, up and axis, `value` only on axis, and `info` (with the JSON tags of `DeviceInfo`) only on added.
- `info.capabilities` (optional): `known/gyro/accelerometer/touchpads/rumble/triggerRumble/led/rgbLed/playerLed`. Old recordings without this object are still readable; `known:false` means unknown.

**Kinds that are not in this format**

- The emergency combo's `ComboHeld` (combo) is generated by T-input, and replays insert it with `ExpandCombos` ("padmated" in §5); carried in a trace as well, it would pause twice. `WriteEvent` refuses it, and `ReadEvents` treats it as an unknown kind. `monitor --direct` and `record --direct` do not set `Options.OnCombo`.
- The routing metadata `DeviceState` and `DevicePower` (power) are not written to traces either. They do not enter the IPC topic `input`.

**Reading rules** (strict; errors give the line number, as in `line 3: unknown kind "press"`)

- Empty lines and lines starting with `#` are skipped, and a UTF-8 BOM is ignored; every line must be valid UTF-8; a UTF-16 file is an error. A line is at most 64 KiB.
- Unknown or duplicate fields, wrong types, and a missing `timeNs`/`kind`/`device` are errors. `timeNs` is 0 to 2⁶³-1, `device` 1 to 2³²-1.
- `control` must be a button or axis name of `input`; aliases are not accepted. Trigger values are 0..32767, sticks -32768..32767.

**`padmatectl record [-o FILE] [--force] [--device ID] [--direct] [--virtual] [--duration D] [--count N]`** records the first controller, or the one `--device` names. By default it goes over IPC (§11); `--direct` opens SDL directly (as monitor does, accepting `--preset/--hint`).

- The first event is an added with info, at time 0, and the rest count from it; a time earlier than the line before is raised to equal it.
- It ends on Ctrl+C, `--duration`, `--count` or the controller's removal. `-o FILE` creates the file only when the first line is written, and an existing file needs `--force`. `>` in PowerShell 5.1 writes UTF-16, so use `-o`.
- Exit codes: 0 when events were recorded and it ended normally; 1 when nothing was recorded, `--virtual` did not record everything, SDL dropped events or there was an error; 2 for wrong arguments.

## 11. IPC

The daemon has a single control surface: JSON-RPC 2.0 over a named pipe. `padmatectl`, Electron's main process and a second padmated instance are all its clients. The decisions are in [ADR: IPC](adr/0009-ipc.md). The parameters and results of the methods are defined by `cmd/padmated/ipc_*.go`.

**Transport and discovery**

- The named pipe `\\.\pipe\padmate-<SID>`, which `PADMATE_PIPE` can override. SDDL `D:P(D;;GA;;;NU)(A;;GA;;;<SID>)`: the ACE denying network sign-ins must come before the ACE allowing the current user.
- The discovery file `%LOCALAPPDATA%\padmate\run\daemon.json`: `{pipe, pid, protocol, token}`, created with an ACL that allows only the current user, Synced and replaced atomically. Before sending the token, a client checks that `GetNamedPipeServerProcessId` equals its PID.

**Protocol and limits**

- NDJSON, one JSON-RPC 2.0 message per line, `protocol:1`.
- After connecting, the first message must be `hello{token, protocol, client}` with an ID. The token is 32 random bytes (hex), new on every start, compared in constant time; client is 1–128 bytes. hello is at most 8 KiB, with a read timeout of 3 s.
- An ordinary frame is at most 8 MiB (the newline not included), a batch at most 64 messages, and there are at most 16 clients (including ones not yet through the handshake). Each connection finishes one frame before the next is delivered; a batch has no guaranteed order.
- Each connection has an event queue of 128. `subscribe {topics}` replaces the subscription, and an empty array cancels it; pushes use the topic's name as the method. When the queue is full, only events are dropped, and `dropped {n}` is sent once it recovers; the engine never blocks. A write times out after 5 s and disconnects. Without subscriptions, no events are built.

### Single instance and forwarding

- A second padmated, before reading its configuration and opening input, authenticates through the discovery folder and calls `instance.forward {args}`, exiting 0 on success. A missing endpoint is waited for up to 5 s; a failure returns 1, without falling back to starting a second output. Relative paths are made absolute by the sender. Offline trace replays take no part.
- A library instance uses `--profile-id`, a standalone-file instance `--profile FILE`; an explicit data-dir must be the same, and forwarding cannot change the kind of entry.

### Error codes

Besides the standard JSON-RPC codes (such as -32602):

| Code | Meaning |
|---|---|
| -32001 | Authentication failed; `ui.exitReady` and the like need `ui.register` first |
| -32004 | Not found (a profile, device, snapshot and so on) |
| -32008 | No profile library (a standalone entry without `--data-dir`) |
| -32009 | Revision conflict; `data.revision` is the current value |
| -32010 | Fake input methods are available only with `--fake-input --dry-run` |
| -32011 | The forwarded startup arguments conflict with the running instance |
| -32012 | The profile is the active one and cannot be deleted |
| -32013 | The profile is still referred to (by a rule or by startup arguments), `data.references` |
| -32014 | The target ID exists already (copy) |
| -32015 | A link, junction or special file, or over the copy limits (`data.path`) |
| -32016 | The script or macro file exists already, `data {exists,name,replaceable}` |
| -32017 | Start at sign-in is not available (a development build, or not supported) |
| -32018 | The fixed default profile `default` cannot be deleted or renamed, `data {"op"}` |
| -32019 | The macro or legacy script entry does not exist, `data {file}` |
| -32020 | Not an acceptable export file, `data {reason,path?,diagnostics?}` |
| -32021 | The profile name is taken, `data {id,name}` |
| -32022 | An imported script has the same name as one in the library with other content, and no `macros` was given, `data {conflicts}`; nothing was written |
| -32023 | When deleting a profile, a profile based on it cannot be written out with its full content, `data {id,reason,diagnostics?}` |
| -32024 | Not an importable AntiMicroX file, `data {reason}` |
| -32025 | The mapping cannot be saved; `data.reason`: `syntax`, `mapping`, `guid`, `config` |
| -32026 | This input cannot do that (fake input has no mapping or raw events, or a mapped controller is not in capture) |
| -32027 | Joy-Con 2 over Bluetooth LE is not available (not Windows, the transport did not start, or a fake input without it) |
| -32028 | Integrations: the app is running, close it first; `data {integration}` |
| -32029 | The integration is unavailable; `data {problem}`: `notFound`, `unknownFormat`, `unsupported` |
| -32030 | The app's files are in use or changed between reading and writing (a journal file, a different fingerprint); nothing was written |

### Methods

| Method | Description |
|---|---|
| `hello` / `subscribe` | The handshake (must come first) / subscribing to topics |
| `status.get` | The daemon's status, slots, devices, the emergency combo, conflicting programs, the notice of a profile repair |
| `devices.list` / `devices.identify` | The device table (ordered by connection ID, with `key`, `slot`, `status`, `battery`) / make a controller rumble and flash |
| `joycon2.status` / `joycon2.pair {seconds?}` / `joycon2.cancelPair` / `joycon2.forget {serial}` / `joycon2.disconnect {id}` | Joy-Con 2: the state / open the window that connects and remembers a new controller (30 s by default, 1–600) / close it / forget a remembered controller (state.json too) / close the connection of a gamepad by connection ID (a pair's both controllers; the controller stays remembered). Each answers with the state |
| `devices.rawInfo` / `devices.setMapping` / `devices.resetMapping` | The mapping wizard: the raw event stream / save and apply an SDL mapping / take it away |
| `latency.get` / `latency.reset` | Latency statistics (how they are measured is in §13) / clear them |
| `engine.pause` / `resume` / `releaseAll` / `setLayer` | Pause, resume, release everything, switch layers |
| `capture.start` / `capture.stop` | Take / release the capture credential for the current connection |
| `profiles.list/get/put/validate/resolve/activate/explain` | Reading, writing and explaining the profile library |
| `profiles.copy/delete/history/restore/export/import/importAmgp/folder` | Copy, delete permanently, old versions, export and import (base64), AntiMicroX import |
| `apps.recent` | Recent foreground programs (at most 12, without padmate and system programs) |
| `config.get/put/repairSeen` | Reading and writing config.json, acknowledging the repair notice |
| `pads.list` / `pads.set` / `pads.migrate` | The settings of each controller (ADR-0017): the controllers that have been connected, with their function keys, feedback switches and emergency combo as stored and as resolved / write one controller's settings / fold the retired global members into the controllers |
| `startup.get/set` | Start at sign-in (accepts only `enabled`; the command is always this process's executable and `startupArgs`) |
| `script.status/reload/describe/registrations/logs/init/import/path/detach` | Scripts |
| `macros.list/create/import/rename/delete/path` | The macros of the script library, without a profile ID |
| `integrations.list/functions/check/items/putItems/plan/write/backups/restore/dismiss/setDataDir` | App integrations (ADR-0019); parameters and results are in §16.4. All are on the bridge allow-list |
| `fake.press/release/axis/effects/battery/joycon2` | Only with `--fake-input` |
| `ui.register` / `ui.setOutput` / `ui.exitReady` | Register a verified UI process; set the switch for output to our own windows; the UI confirms it has saved its edits on tray exit or `daemon.exit` (callable only by registered connections) |
| `daemon.exit` | The installer asks padmated to exit like the tray's "Exit" (§5); the parameters must be exactly `{reason:"installer"}`, and anything else is -32602 and does nothing |
| `instance.forward {args}` | A second instance forwards its startup arguments |

**The UI bridge** (`padmatectl ui-bridge`) is the private subcommand through which Electron's main process connects to the daemon: NDJSON over stdin/stdout, with an allow-list of methods and bounded queues (`TestUIBridgeMethodAllowlist`). `daemon.exit`, `fake.*` and `instance.forward` are not on the allow-list; `profiles.folder`, `macros.path` and `script.path` return paths and are for the main process only.

**Key points** (read the code for the rest)

- **`profiles.put {id,text,revision}`**: revision is required, and the empty string is allowed only for creating; a mismatch is -32009. After validation, under the write lock it writes a temporary file, Syncs and renames, storing a snapshot before the replacement. The active profile is applied synchronously (even with `--watch=false`), returning `{revision,applied,problem?}`: when the check of the script parameters fails, the file is saved but the old runtime stays, and `problem` is returned. Changing the name of the fixed default profile returns -32018. A parent not in the library, a cycle, or a parent with errors gives InvalidParams.
- **`profiles.delete {id,revision}`**: deletes permanently, keeping no copy. Under the write lock it first renames to a hidden `.tmp-<id>-*` and then deletes, so either the whole profile leaves the library or nothing changes; a delete that leaves debris still counts as success. Refusals: the active profile, -32012; selected by a rule (disabled ones included) or by startup arguments, -32013; links and the like, -32015; `default`, -32018. state.json is flushed before deleting. When other profiles are based on it, they are first written out with their full content (§10.1) and `flattened` is returned; when that cannot be written, -32023.
- **`profiles.export/import`**: the daemon only sends and receives bytes and accepts no paths; picking, reading and writing files happen in Electron's main process. `import {data,name?,replace?,macros?}`: `name` and `replace` cannot both be given; the `reason` of `-32020`: `format`, `version`, `entry`, `limit`, `encoding`, `profile`; a taken name is -32021, a script conflict -32022.
- **`profiles.explain`** (`ipc_explain.go`) answers per controller since ADR-0017: each entry of `slots` has its own `functionKey` (the key the slot's Program has: `View.ProgramFunctionKey`), `isFunctionKey` and `functionSet`. For a library profile (`id`), which has no controller, there is one entry (`slot` -1) for each distinct function key among the controllers the daemon knows (`pads.list`), with their DeviceKeys in `controllers`, or one for the default if it knows none. The top-level `functionKey` and `isFunctionKey` are those of the first entry.
- **`pads.*`** (`ipc_pads.go`): `pads.list` answers `{revision, legacy:{any,functionKey,feedback,panic}, pads:[…]}`, `revision` being that of the config.json the list was read from (a file with errors is repaired first, as `config.get` does). A pad is every controller that is connected, is in state.json's `controllers` or has an entry in `devices`: the connected ones first in connection order, then the rest by `lastSeen`, newest first. Its members: `key`, `name`, `type`, `buttonMask`, `known` (the daemon has seen it; false for one that only config.json mentions: `name` empty, `type` `unknown`, the family from the key's GUID), `connected`, `device` (the connection ID), `ignored`, `lastSeen`, `family` (`standard`, `joyconLeft`, `joyconRight`, `joyconPair`, `unknown`), and the three settings: `functionKey {default, options:[{control,label}], stored?, effective, source}` (`options` are the family's, default first; `label` is what is printed on the controller: "ZR", "Menu", "−"), `feedback {stored?, effective:{rumble,led,pause,lowBattery}, source}` (`source` the strongest among the switches) and `panic {default, stored?, effective, source}` (`effective` is the combo T-input detects: the built-in one if the controller lacks a button of the configured one). `pads.set {key, revision?, functionKey?, feedback?, panic?}` writes one controller's entry: a member that is absent stays, `null` takes the controller's own value away (the retired top-level member or the family's default applies again), anything else sets it; `revision`, when given, must be config.json's (-32009). It answers `{revision, applied, problem?, restartRequired?, pad}`. A `key` that is no canonical DeviceKey, a function key the family of the controller refuses, an unknown switch or combo member and a combo that fails validation are -32602; a key that is not connected, not in state.json and not in config.json is -32004. It edits the text by byte position (`profile.SetPadSettings`: every other member keeps its bytes and place, the result is indented as the UI writes it, an entry left with no member is removed) and commits through the path of `config.put` (`commitConfigLocked`), so `profile.changed {kind:"config"}` follows. `pads.migrate {revision?}` folds the retired top-level `functionKey`, `feedback` and `safety.panic` into the entries of every known controller (`profile.MigratePadSettings`: only what differs from the defaults of the family is written, so each controller behaves as before) and removes them; the answer has `changed:false` when the file has none. A controller that has not been connected since state.json began to list them takes the defaults of its family afterwards. All three are on the bridge's allow-list. The effective config the daemon runs with (`liveSettings`) carries the per-controller members, and the slots, the effect switches and `ComboFor` read them from it (§5.6, §7.9).
- **`joycon2.*`** (`ipc_joycon2.go`, `ipc_fake_joycon2.go`): the result is `{available, unavailable?, autoConnect, windowOpen, windowEndsAt?, windowLeftMs?, scanning, known:[{addr,serial,side,connected}], links:[{addr,serial?,side,player,firmware?,paramsRequest?,intervalMs?,latency?,timeoutMs?,state,device?,known}]}`. `state` of a link is `connecting`, `connected`, `dormant` (silent; it is in no gamepad) or `closing`; `paramsRequest` is what became of the request for the throughput-optimized connection parameters (`accepted`, `unavailable (Windows 10)` or `failed: <why>`), and `intervalMs` (a number, e.g. 15 or 11.25), `latency` (connection events) and `timeoutMs` are the current connection parameters, read by the transport every 2 s: they are absent while unknown (before the link is up, and always on Windows 10, which cannot read them), and `latency` and `timeoutMs` mean something only when `intervalMs` is present (a latency of 0 is left out); `device` is the connection ID that `devices.list` and `joycon2.disconnect` use. A source without the backend answers `joycon2.status` with `available:false` and every other method with -32027; an unknown serial or connection ID is -32004; `seconds` outside 1–600, a missing `serial` or `id` and unknown members are -32602. The backend has no change callback, so a watcher goroutine reads the state when a gamepad comes or goes, after a method, when a controller is remembered, every 250 ms while the window is open or a link is being made or closed, and every 2 s otherwise, and publishes `joycon2.changed` when it differs (the time left does not count). The fake input has a simulated backend: no scan, no state.json, no gamepad; `joycon2.pair` opens a window, `fake.joycon2 {side?}` is the user holding SYNC (only while the window is open it connects and remembers `FAKEJC2L0001`, `FAKEJC2R0001`, … with connection IDs from 1001), and the window closes by itself on time. The tray shows 连接新的 Joy-Con 2 only while the source has the backend; a click opens the 30 s window and notices follow (§5). The UI's Settings section (`SettingsJoyCon2.tsx`) is the client of `joycon2.status`, `joycon2.pair {seconds:30}`, `joycon2.cancelPair` and `joycon2.forget`: Main's handlers (`joycon2-handlers.ts`) take no method name or address from the renderer and check the serial number; the topic `joycon2.changed` is on the one subscription (`HudSubscription.joyCon2Up`) only while that section is mounted, and only after `joycon2.status` says `available`, which also hides the section when it is false. `joycon2.disconnect` is on the bridge's allow-list but the UI does not use it. The connection and battery of a Joy-Con 2 are not in this state: they are in `devices.list` like any gamepad's, and the controller rows of Settings show them.
- **`devices.rawInfo`**: the controller must already be in capture (`capture.start`) before it is requested, or -32026 (the wizard asks the user to press buttons that would be mapped to keys, so mapping must be paused first). Requests are recorded per connection and let go on disconnect, and `RawStop` comes only when the last one lets go; raw events go as `input.raw` only to connections that requested that controller and subscribed to the topic. The wizard's flow: subscribe to `input.raw`, `capture.start`, `devices.rawInfo`; at the end, `rawInfo {watch:false}` and `capture.stop`.
- **`macros.*`** (`ipc_macros.go`, `profilelib/macro.go`): everything happens under the library's write lock through `os.Root`. `rename` rewrites every reference at its byte position at the same time, and changes everything back if any write fails; `delete` clears the references first, then deletes the file; with 64 macros in the library, creating one more returns InvalidParams. In library mode, `watch.DirWatcher` watches the top-level scripts of the library folder. CLI: `padmatectl macro list|new|import|rename|delete`.

### Topics

The topics of `subscribe` are fixed (`ipc.Topics`):

| Topic | Content |
|---|---|
| `input` | Input events in the format of §10.6, plus `remapped`. Input while queued, filtered, paused or in capture can all be subscribed to |
| `engine.activity` | Decisions, unrouted edges, a state snapshot every 250 ms (below) |
| `log` | `{sequence,record,truncated?}`, following the current log level; 256 kept in memory, each at most 16 KiB |
| `script.status` | Light script status every 250 ms; queried only while subscribed |
| `profile.changed` | Changes of the profile library and the settings (below) |
| `output.preview` | A preview of a shortcut after the Arbiter resolved it; not built when nobody subscribes |
| `app.foreground` | `{exe,path,elevated,own,system?}`, the executable's identity only; window titles are not read; `own` marks a registered padmate UI, `system` a program under the Windows folder |
| `daemon.exiting` | Published before exiting, `{reason:"tray"}` or `{reason:"installer"}` |
| `integration.changed` | App integrations: `{integration, kind, profile?, differenceId?, count?}`; `kind` is `files`, `running` or `differences` (§16.4) |
| `hud` | A script's `hud.toast`: `{kind:"toast",slot,text,ms}`. While subscribed, scripts' `host.has("hud")` is true |
| `input.raw` | Raw events `{timeNs,device,kind,index,value}` of controllers requested through `devices.rawInfo`; subscribing to it opens no device by itself |
| `joycon2.changed` | The Joy-Con 2 state, in the shape of the result of `joycon2.status`, whenever it changes (the window opens or closes, a controller connects, is remembered, forgotten or lost, or its connection parameters change) |

The `kind` of **`profile.changed`**: `active` (`id,revision,generation`), `saved` (`id,revision,applied`), `deleted` (`id`), `config` (`revision,applied,restartRequired`), `macros` (the top-level scripts of the library changed; only with `--watch`), `configRepaired` (§10.1).

The three payloads of **`engine.activity`**:

1. `{kind:"decision",slot,decision,latchBinding,at,waiting?,waitedMs?}`: `decision` is the engine's `Decision` (§7.8). When publishing, the daemon adds `at` (wall-clock Unix milliseconds), `waiting` (when a press has no action yet and has to wait for a release, hold or repeated taps to be decided, it lists those gestures; this is not a failure) and `waitedMs` (for a decided tap, multi-tap or hold, the milliseconds since the latest press, counted from the button's arrival time that `scfg.OnEvent` recorded, queuing included).
2. `{kind:"unrouted",at,device,control,down,why}`: button edges of controllers not in a slot, with `why` one of `queued`, `ignored`, `notGamepad`, `disabled`. A token bucket limits them to 16 per second; more are dropped.
3. Every 250 ms, `{kind:"state",paused,capturing,suspended,slots,batteries}`: the objects of `slots` are of the same type as in `status.get` (with `hints` while "Key hints" is held, and `panels` while a script's `hud.panel` keeps a panel; they appear and disappear up to 250 ms late); `batteries` has one entry per controller whose battery has been read, `{device,key,name,state,percent?,low,episode?}`, where `low` means the low-battery warning went out and has not re-armed, and `episode`, present only when low, is how the UI recognizes a new warning by it growing.

### 11.1 Importing AntiMicroX profiles

The implementation is in `internal/amgp` (a pure package, testable on Linux): `amgp.Import(data, opts) (profileJSON, report, err)`. The output always passes `profile.Parse` and `engine.Compile` (`Import` parses it once more itself, returning `ReasonInternal` on failure), and the same file always gives the same output. The file format comes from AntiMicroX's source (GPL-3.0, read only; no code was copied) and from real files; the key code table is Qt 6's `qnamespace.h` (`configversion` ≥ 6).

**Reading the file** (files are untrusted): limits of 1 MiB (`amgp.MaxBytes`), 32 levels of nesting and 100,000 elements; `encoding/xml` in strict mode, refusing UTF-16, a non-UTF-8 declaration, DOCTYPE, invalid XML and several roots; the root must be `gamecontroller`, with `configversion` ≥ 6. A refusal is an `*amgp.Error{Reason,Message}`, with `reason` one of `size`, `encoding`, `xml`, `limit`, `doctype`, `root`, `joystick`, `version`. What the file does not write is AntiMicroX's default, and imported sticks and triggers always write the converted values out.

**Correspondence rules**: buttons, stick directions, the d-pad and triggers map one to one to the corresponding control names; the press/hold/release zones map to `press`/`hold`/`tap`/`release`; a `cycle` of 2–4 slots maps to `tap`…`quadTap`; `pause` and `textentry` map to `sequence` and `text`; `setchange` maps to layer switches (controls without a binding in a set that a `while-held` points to are written as `{"block":true}`); a `while-held` on `back`/`start` becomes `functionSet`; turbo becomes `repeat`; the stick, trigger and d-pad settings take set 1's values into `analog`. Sensors, calibration, `execute`, `loadprofile` and the like are dropped and reported. The full rules are in `internal/amgp` and its `testdata/`.
**The report** (`ReportLine{Severity,Code,Set,Control,Slot,Message}`): `severity` is `dropped`, `approximate` or `info`; `code` is a stable name; `message` is the reason in Chinese. The same line is recorded once, at most 1000 lines, and the rest are combined into `report.omitted`.

## 12. Electron

`app/` is the optional UI: Electron's main process, two preloads and two renderer pages (the main window and the overlay). The daemon does not depend on it, and with the UI closed, mapping works as before. The UI talks to the daemon only over IPC (§11); it neither reads nor writes the profile and config files, and sends no keys.

The decisions are in [ADR: Processes and stack](adr/0001-processes-and-stack.md), [ADR: Editor UI](adr/0013-editor-ui.md) and [ADR: Overlay](adr/0014-overlay.md). Versions are in `app/package.json`.

### 12.1 Processes and windows

| Part | Where | Responsibility |
|---|---|---|
| Main | `app/src/main/` | Starts and connects to the daemon, holds the bridge, checks every request of the renderer, manages the windows, the overlay and updates |
| preload (main window) | `src/preload/index.ts` | Exposes only `window.padmate` (of type `PadmateAPI`, in `shared/api.ts`) |
| preload (overlay) | `src/preload/overlay.ts` | Exposes only `window.padmateOverlay`, four receive-only listeners |
| renderer | `src/renderer/` | The main window (`index.html`) and the overlay (`overlay.html`) |

- The renderer loads from the custom protocol `padmate://app/`, not `file://`; in development, from electron-vite's server on `127.0.0.1`.
- The main window is 1180×800 by default, at least 850×620. The languages are zh-CN and en, with a dark theme only.
- `electron-updater` is bundled into `out/main/index.js`; the installed app has no `node_modules`.
- The renderer is split into folders by feature, with tests next to the files they test: `state/` (`store.ts` and the global `*-model.ts`), `editor/` (editing one profile: `editor-model.ts`, `editor-store.ts`, inheritance, undo), `controller/` (the 3D controller, the button callouts, `pad-icon.tsx`, with the GLBs in `assets/` and `pad-icons/`), `workspace/` (the Mapping page), `drawer/` (the button drawer and binding editing), `analog/` (the stick and trigger panels), `pages/` (the Diagnostics, Logs and Scripts pages; `profiles/` is the Profiles page, and `settings/` the Settings page and the mapping wizard), `onboarding/`, `update/`, `overlay/`, `ui/` (shared controls) and `testing/` (fixtures for tests only). The top level holds only the entries of the two pages (`index.html`, `main.tsx`, `overlay.html`, `overlay-main.tsx`), `App.tsx` and a few files it uses directly, and the tests that scan the whole renderer folder (`forced-colors`, `theme-contrast`, `doc-names` and others).

### 12.2 The security bridge

- `baseWebPreferences` in `main/security.ts`: `contextIsolation` and `sandbox` on, `nodeIntegration` off. Navigation, new windows and webviews are refused. The content security policy comes in the response headers of `padmate://app`: `script-src 'self'`, `connect-src 'none'`.
- The preload exposes only named, typed methods, each matching one `padmate:*` channel. The renderer gets no token, pipe name or PID, and cannot send arbitrary RPCs.
- Every handler in Main checks that the sender is the main frame of the main window, that the URL is the app's own, and that the arguments are valid (`validate*`). Operations involving paths accept only paths Main builds itself, or fixed targets.
- Errors returned to the renderer carry only a code and known data, never a path under the user's folder.

### 12.3 The connection to the daemon

`DaemonManager` (`main/daemon-manager.ts`) handles starting and retries, `Bridge` (`main/bridge.ts`) RPC and events, and `padmatectl ui-bridge` on the Go side the authentication.

1. Main starts a child process, `padmatectl ui-bridge --ui-pid PID`, and exchanges frames through its stdin/stdout. The bridge reads `daemon.json` and checks the PID of the pipe's server before sending the token; the daemon checks that the UI is the bridge's parent process, of the same user and the same installation folder, and then registers the UI.
2. It first connects to a running daemon; after a failure, it starts a detached `padmated` at most once per round, with bounded retries.
3. `ConnectionState.phase`: `connecting`, `connected`, `disconnected`, `failed`. The window is shown only once the page is ready and connected (or the start failed).
4. Closing the UI closes only its own bridge; the daemon keeps running.

The daemon's startup arguments: the installed app uses `--inject --data-dir` (it really sends keys); the development build and E2E use `--dry-run`; when it connects to a running instance, that instance's mode stays.

- **A failed start**: each start uses a new report file, `<userData>/startup-reports/daemon-<PID>-<n>.json` (`--startup-report`). When the child exits with a non-zero code, the report is read (at most 256 KiB, known fields only), retrying stops, the state becomes `failed`, and `failure` is `report`, `exit`, `spawn` or `timeout`. Exit code 0 without a report means the request was forwarded to a running instance. When `failed`, `StartFailure.tsx` fills the window, with only the reason and three buttons (Open folder, Retry, Exit).
- **Event delivery**: through `EventBuffer` to the renderer, with at most 32 batches in flight and 128 waiting (events of the same device and control merge); on overflow it reports `dropped`, and the renderer refreshes its state. A connection generation keeps the results of old requests from overwriting the state after a reconnect.
- **Capture sessions**: keyboard recording uses a short-lived child process, `padmatectl keyboard-capture`, which installs its LL observing hook only after it owns its own `capture.start` connection, and observes only physical keys that were not injected; losing focus, canceling, exiting, disconnecting or a 30-second timeout ends the child process and releases the connection. For controller capture (`pad-capture.ts`), a failed `capture.stop` is sent again after 1 s until it succeeds or the connection drops.
- **Keys sent to padmate's own windows**: while a registered UI is in the foreground, the Arbiter only keeps virtual accounts and publishes `output.preview`, without calling the Sender; on a foreground switch, the old target's keys are released first, and the `BlockOwnWindow` gate waits for gestures, repeat and old scripts to be canceled. `config.output.sendToOwnWindow` takes effect on save; a global dry-run always comes first.

### 12.4 State and saving

- `renderer/editor/editor-store.ts`: the editor of the Mapping page and the profile settings dialog, one per profile, with only one for a given profile at a time. It holds the whole profile draft, the revision, the undo history and the function-key view `view`. The pure functions are in the `*-model.ts` files.
- The UI's own preferences are stored in the renderer's `localStorage`; settings the daemon needs to know are written to config.json. The language is also written to `<userData>\language.json` (`main/ui-language.ts`), one line `{"language":…,"setBy":"settings"}`, because the installer reads it: the installer writes its first language there with `setBy:"installer"` and leaves a file it did not write (§15.4). Main reads the file before the window draws (`getLanguage`); the language is the one in `localStorage`, else the file's, else zh-CN, and a pick that only `localStorage` has is copied into the file at start.
- `status.profileId` is the library ID and `slots[].profile` the display name; never call the library API with the name.

**Autosave**:

- Every commit saves and takes effect at once; there is no save button. Text fields commit on Enter or when they lose focus; invalid input is not committed, and Esc restores it. Millisecond fields are `type="text"` (why is in PITFALLS "UI").
- The draft is always the whole profile, and the UI edits only `layers.base` (or the set while the function key is held). Other layers, common, layer actions and block are kept as they are on save and are read-only; when `defaultLayer` is not base, the whole profile is read-only. Parts not filled in are left out on save (`savedForm`) and kept in the draft; the controller picture's labels, the "All buttons" list and the exported sheet read `savedForm(draft)`. A name belongs to a mapping: the drawer's `edit` passes every edit through `withoutOrphanLabels` and `withoutOrphanStickLabels`, which take the `label` from a key the edit changed and left without an action or block (`bindingMaps`), and `analog.ls/rs.label` from a stick the edit touched and left mapping nothing (`stickMapped`: the pointer, a key of its mode, a ring or a raw half axis with an action). Keys the edit did not change keep a name alone from the file; the editor no longer hides the daemon's "binding has no action" about them.
- Saves are serial: first `profiles.validate`, then `profiles.put` with the revision. What fails validation is not written, and the bottom of the window says "Changes not saved". On a revision conflict, the disk is read again and the unsaved changes are dropped.
- When no answer arrives (the bridge's 10 s timeout, a disconnect), the daemon may have written: `editor-store` remembers the text of the last 4 puts, and when the disk content read back equals one of them byte for byte, it counts as our own write and the draft is kept; only when it differs does the file win.
- Closing the window asks for no confirmation: Main sends `padmate:closing`, the renderer commits its input, waits for the save and calls `readyToClose`, and Main waits at most 3 s (`CLOSE_WAIT_MS`); exit paths wait 1800 ms (`EXIT_WAIT_MS`, less than the daemon's 2 s).
- Undo/redo: Ctrl+Z, Ctrl+Y, at most 100 steps per profile, in memory only, and the result is saved at once too. With "Send keys inside padmate's window too" on and a controller connected, a Ctrl+Z injected by the controller arrives before the keyboard event, so the undo key is not handled at all then.

### 12.5 Pages

The shell (`App.tsx`): the side navigation is Mapping, Profiles, Scripts and Settings; logs, diagnostics and the controllers are sub-views of Settings. The top bar has the profile menu on the left, and the running state, pause and the "More" menu on the right. When `config.get` carries `restartRequired`, the Settings page says a restart is needed. When the daemon repaired a broken configuration, `ConfigRepairDialog` says so; Main accepts only backup names of the form `config.broken-YYYYMMDD-HHMMSS[-N].json` and builds the path itself.

- **The Mapping page**: a Three.js controller seen from a fixed front (a local GLB), with a renderer that stays alive and is never destroyed. A Joy-Con 2 is recognized by vendor `0x057e` and product `0x2066`, `0x2067` or `0x2068` (`isJoyCon2` in `controller-models.ts`) and has its own models, `joycon2-left.glb` and `joycon2-right.glb`; a Switch 1 Joy-Con keeps the Switch OLED model. `app/scripts/pack-controller-models.mjs` (`switch2`) cuts them from the Switch 2 console model of NAIT Studio (CC BY 4.0, attributed in `assets/ATTRIBUTION.md`): the two Joy-Con 2 only, named parts with one material each, no textures (about 270 KB and 296 KB), positions, normals and indices quantized with `KHR_mesh_quantization` (meshopt is not used for this source), and the stick's pivot moved to its base. The source has no geometry for SL, SR, ZL and ZR, so the file marks them with empty nodes of the same names, and their callouts point at fixed positions on the rail and the shoulder. A stick is two cells, "rotation" (`leftstick`) and "press" (`leftstick.press`); the "rotation" cell's title is `analog.ls.label` (`setStickLabel` of `analog-editor.ts`, not through `settleStickMode`). The drawer of a knob tick has a single shortcut field (`SingleShortcutField`, writing `{press:{type:"keys"}}` and keeping `label` and `haptic`). The four directions of the d-pad have the drawers of ordinary buttons, with no settings panel. `controllerCallouts` gives the labels by `padHasControl` (`controller-model.ts`, reading `buttonMask` and `axisMask` of `DeviceInfo`; with both 0 it assumes a standard controller): a control the controller lacks gets none, and the extra buttons (misc1, misc2, paddle1–4) of the other controllers form a `placement:"bottom"` row whose `offset` counts from the middle, the left hand's paddle4 and paddle2 on the left and the right hand's paddle1 and paddle3 on the right; `controller-3d.css` keeps the row between the two columns. A pair of Joy-Cons has no chip for its SL and SR (paddle1–4, which cannot be pressed while the two are joined): `diagramHidden` and `diagramHasControl` leave them off the picture, its markers on the model and the accessible list of states only; the daemon still reads them and a profile's bindings on them are kept (the editor's key lists, the shortcut table and `savedForm` do not look at `diagramHidden`). A Joy-Con's chips are not placed by the columns above but by `controller-joycon-layout.ts`: a table says in which region (the two shoulder corners above the columns, the row above, the two columns, the row below) each unit of controls may go, best first. A single Joy-Con is always shown upright: it has one shoulder chip on each side (L in the outer column, ZL in the column on the rail's side; mirrored for the right one), and the stick's chip is in the column on the rail's side, between SL and SR; the pair keeps the columns of a standard controller, with Y in the right column with the X, A and B chips. The leader lines of a Joy-Con are polylines: they run along the chip's row or column, then at 45° to the button. Each Joy-Con callout carries its `route`: `auto` (as just said; a 12 px stub, then straight, where 45° does not fit), `steep` (the last run near vertical, for one of two shoulders whose ends nearly coincide) or `gap` (the innermost of the four buttons, through the gap between two of its neighbours). `leaderPath` in `controller-layout.ts` turns the route into the polyline; the scene draws it every frame (a stick's end moves), and every check judges all its segments. Other controllers keep straight leaders. Before the model is known the chips take the table's first choice (`staticLayout`). Once it is built, `buildAssetModel` hands the anchors (where each leader ends, in the model's frame) to `arrangeJoyCon`, which keeps the table's order and searches the shift of each column and the room left before some chips (SL, SR, L and the like), with separate slots for each chip density (`slot`, `slotCompact`, `slotMini`), for a layout in which no two leaders meet and none passes through a chip (a leader that ends within 1.5 px of another counts as meeting it, except a shoulder's and its trigger's, `twin`, whose ends nearly coincide) on every stage size of `supportedStages` (440 × 440 and up, the smallest the workspace gives the stage, with both sides of each density change, 640 × 640 among them), and no chip covers the model: `buildAssetModel` also hands it an outline of the shell (`shellOutline`, vertices of the convex hull of 16 slices of the model's depth), `projectedHull` projects it to the silhouette on each stage, and a chip that touches it is a problem like a crossing. A Joy-Con's chips are placed against the model (`data-wide-rows` in `controller-3d.css`, `WideRows` in `controller-layout.ts`; `--model-h` and `--model-w`, which `Controller` sets from the built model, give the model's size in its units and `--unit` the pixels of one): only the pair has a row, − and + above the model in narrow chips (`data-wide-top` is absent), and shoulder corners, which are not drawn in towards the model (`--corner-shift: 0`); the corners, the pair's right column and the rail-side column of a single Joy-Con hang from the model's top (`data-hang`, `--model-top`), and the corners move down together when the top one would leave the stage (`cornerLift`, `--corner-lift`); the scene hands the result to `Controller` with its first frame (`onReady(model.callouts)`, `withArrangement`). `controller-layout.ts` holds the geometry for this without a browser: it repeats the sizes of `controller-3d.css` and the camera of `controller-scene.ts`, which `controller-layout.test.ts` holds equal to the stylesheet and to a `PerspectiveCamera`. `controller-joycon-layout.test.ts` is the proof: for Switch 1 and Switch 2, a left and a right Joy-Con and the pair, no leader crosses or passes through a chip and no chips cover each other on any of those stage sizes, with and without the editor notice, apart from the sizes listed in its `knownProblems` (tall stages in the compact and mini densities, where the face buttons' leaders of a right Joy-Con or a pair still meet); the test fails if another size has a problem or a listed one becomes clean. `npm run preview:layouts` (in `app/`, from `tests/preview/`, a Vite project of its own that is not part of the app's build) serves a page at `http://127.0.0.1:5199/controller-layouts.html` that shows all the placements side by side with made-up bindings. With `?check=1` the page, once every controller has drawn, reads the chips' boxes and the leaders' whole paths back from the DOM (`tests/preview/layout-check.ts`) and checks them against the layout model: each leader starts in the middle of the facing edge of its own chip, follows its route and ends on its part, the chips are where `controller-layout.ts` says (so the stylesheet and the TypeScript agree), no leaders cross, none passes through a chip, no chip covers another or the model; the result is JSON in `<pre id="layout-check">`. `node tests/preview/check-layouts.mjs` (not part of CI; it needs the dev server and a headless Chromium, no window) runs this for all the placements at several stage sizes and saves a PNG of each. The model has an anchor only for a control with a label (and guide, which still lights when pressed); sample masks of each controller are in `testing/sdl-pads.ts`, and `TestVirtualFamilyControls` in `internal/input` checks the same numbers with virtual controllers. Button icons are drawn by `pad-icon.tsx` as inline SVG (`PadIcon`) from the paths parsed out of the SVGs in `app/src/renderer/controller/pad-icons/`, shared by the controller picture's labels, the drawer's title, the press preview (`control-icon.tsx`) and the mapping wizard. Clicking a label opens `KeyDrawer`: the mappings are only tap, hold and double tap, and the actions only "Shortcut" and "Script"; other mappings and actions in the file still work. Whether a tap is written as `press` or `tap` depends on whether the button also has a hold or double tap (`settleTap`). Shortcuts can only be recorded (`ShortcutField`: the keyboard through `keyboard-capture`, the mouse and wheel received by the window itself). The `analog` parameters are shared by the whole profile and are not split by the function key.
- **The function key** (the engine in §7.9, the format in §10.3 and §10.4): there is one for each controller, chosen in the Controllers view of Settings (the choices and the default depend on the controller's family, §10.4); it cannot be bound itself, and the UI edits the set it switches to (`profile.functionSet`). The set it edits is that of the function key of the controller the Mapping page shows (`effectiveFunctionKey`; `start` while none is connected), and a change of controller or of its key changes which control is the function key at once. The "Normal / Holding the function key" switch changes `view` (not written to disk, not in the undo history), by having `swapView` swap `functionSet` and `layers.base`. The old `functionKeys` format is synthesized into one set when read.
- **The Xbox button** (`guide`): it has no chip on the controller picture (`controllerCallouts` leaves it out, while the 3D model still lights it when pressed), and the key list, the shortcut table and the layer count leave out what a file binds on it, as they do the function key's (`isIgnoredControl`). `savedForm` drops the bindings on it from every layer, common and the set, so a profile edited in the UI loses them.
- **`extends` written by hand**: the UI neither creates nor shows it; inherited buttons are treated as the profile's own, and only the one changed is written into the profile's own file (`inheritance.ts`). When a parent is deleted, the daemon first writes the children out with their full content; when a child has errors or inherits its parent's script, it returns -32023.
- **The Profiles page**: one profile per app, and apps without a binding use the fixed default profile `default`; the switch at the top is config's `enabled`. When a program is already selected by an enabled rule of another profile, the user confirms first, and the confirmation deletes that rule. A new profile is empty; the name is required, at most 64 characters and unique. Deleting first calls `profiles.delete`: refused as in use (-32012), it switches to another profile and asks again; refused as referred to (-32013), it first deletes the referring rules with `config.put` and asks again; if that does not resolve it, it writes back and switches back to the original profile; the default profile cannot be deleted. Exporting `.padmate` and importing go through `profiles.export`/`import` (at most 4 MiB); when the name is taken (-32021), Main keeps the file and returns a one-time token (expiring after 2 minutes, at most 4 kept), and the page chooses "Replace" or "Save as". Importing AntiMicroX goes through `profiles.importAmgp`, and `AmgpReportDialog` shows the report.
- **The Scripts page**: the script library is shared by every profile and is operated through `macros.*` (§11); only the daemon writes the files. The name check of `shared/macro-names.ts` shares `daemon/testdata/macro-names.json` with Go.
- **The Settings page** (`SettingsPage.tsx`): changes save at once. The sections: General (language, the Controllers row, start at sign-in), Overlay, Safety (release all keys), Controller calibration (`devices.<DeviceKey>`, with identification and the mapping wizard), Press timing (`timing`), Advanced (`output.sendToOwnWindow`, `output.eventGapMs` 0–50 ms), Updates, About. A save reads the whole config, changes only its own fields and carries the revision. Start at sign-in goes through `startup.get/set` and is not written to config.json; development builds return -32017.

  **The Controllers view** (`PadsPage.tsx`, `SettingsPads.tsx`; ADR-0017) is opened by the row "Controllers" of General and, like the logs and the diagnostics, is not a page of the side navigation. It reads `pads.list` when it opens and lists the connected controllers, then the others in a fold "Previously connected". Each controller has its function key (a choice among `functionKey.options`, the default marked), the four feedback switches and the emergency pause (the effective combo and hold time, a "Change" button only while the controller is connected, which opens `PanicComboDialog`, whose "Restore default" takes the controller's own combo away). Every change goes through `setPadSettings`, which queues with the other config.json writes and sends `pads.set` for that controller alone. While the retired top-level `functionKey`, `feedback` or `safety.panic` are in config.json (`legacy.any`), the first change sends `pads.migrate` first, so that no controller changes behaviour, and then the change; a change that alters nothing sends neither. The page does not rewrite the file when it opens.
- **The mapping wizard**: saving writes `devices.<DeviceKey>.mapping`. It asks for 24 controls, ending with View and Menu, and not for Home (`guide`). `mapwizard.ts` is the TypeScript copy of Go's `internal/mapwizard`; `mapwizard.test.ts` reads `daemon/internal/mapwizard/testdata/vectors.json` and requires the same results, so a change of the rules is made on both sides.

### 12.6 Diagnostics

`pages/DiagnosticsPage.tsx` and `activity-model.ts`, a sub-view of Settings. The daemon's side is in [ADR: Diagnostics](adr/0016-diagnostics.md) and §7.8 and §13.

- **Events**: while connected to the daemon, the store keeps recording the `decision` and `unrouted` of `engine.activity` into the ring `activity` (the last 200).

- **Reason texts**: the keys of the `reasonSentences` table are Go's reason names (`reasonNames` in `decision.go`) or "reason.variant". `activity-model.test.ts` checks them one by one against Go, and a new reason in the daemon without a sentence here fails the test.
- **Latency**: `getLatency()` is read once a second only while this page is open; `warn` (at least 50 samples and a p99 over 1.5 ms) is decided by the daemon, and the UI computes nothing of its own.

### 12.7 The overlay window

`main/overlay*.ts`, `preload/overlay.ts`, `renderer/overlay/Overlay.tsx`, `shared/overlay.ts`. The decisions are in [ADR: Overlay](adr/0014-overlay.md).

- padmate.exe's second `BrowserWindow`, created once after the main window's `ready-to-show`. No UI, no overlay, and mapping works as usual. It cannot be seen over exclusive full-screen programs.
- The window never takes focus: transparent, frameless, `focusable:false` (`WS_EX_NOACTIVATE`), `alwaysOnTop`, `setIgnoreMouseEvents`, and `showInactive()` is called once. `overlay.ts` and `overlay-window.ts` contain no `show()`, `focus()`, `moveTop()` or `hide()` (`overlay.test.ts` reads the source to check); showing and hiding happen by the page switching `data-visible`.
- Position: 480×160 DIP, centered at the bottom or top of the primary display's work area, never covering the screen (or Windows would take it for a full-screen program); it grows while hints or panels are shown, up to 494.
- Both the main window's `closed` and `before-quit` destroy the overlay. Main subscribes to the topics that include `hud` only once the overlay is ready; after giving up on the overlay (no display, cannot be created, failed to load, a second crash), the subscription is removed, so that whenever a script's `host.has("hud")` is true, an overlay is really on the screen.
- Content: toasts, key hints (from the slot's `hints`, every 250 ms, at most 16 entries) and script panels; the preferences (notices, position, opacity) are pushed to Main through `padmate:overlayPrefs`.

### 12.8 Automatic updates

`main/update-*.ts`, `updater-electron.ts`, `shared/update.ts`, `renderer/update/UpdateDialogs.tsx`. The decisions are in [ADR: Installer and updates](adr/0018-installer-and-updates.md).

- It checks only while the UI process runs (it stays in the background after the window is closed and keeps checking). Downloading and installing happen only with the user's consent. It requests only `latest.yml` and the installer from `https://padmate.download/`, which has stable releases only.
- Configuration: `NsisUpdater({provider:"generic", url: UPDATE_URL, channel: UPDATE_CHANNEL})`, with the channel fixed to `latest`; `app-update.yml` is not read (without a channel, the generic provider takes the version's prerelease identifier, so `1.0.3-rc.1` would request `rc.yml`).
- `UpdateState` lives in Main, and changes are pushed through `padmate:updateState`; `phase`: `idle`, `checking`, `available`, `downloading`, `downloaded`, `installing`; `dialog` decides which blocking dialog opens, with the initial focus on "Later". The UI passes no versions, URLs or files.
- Timing: 30 seconds after the UI starts, it checks only when 24 hours have passed since the last check, and then every 24 hours. A manual check always shows a dialog. Settings are stored in `<userData>/updates.json`, written atomically.
- Installing: first `closeGuard.settle` lets edits finish saving, then `quitAndInstall(true, true)` (silent); the installer itself stops the daemon first (`padmatectl quit --under`, §15).
- Development builds are `supported:false`. The single-instance lock `requestSingleInstanceLock()` is called after setting userData (the lock is per userData, so development builds and E2E never meet).

### 12.9 The tray and the lifecycle

The daemon creates and manages the tray (T-main in §5), not Electron.

- **Closing the window only hides it**: while the daemon is connected, the page is ready and no exit is in progress, closing the main window only does `hide()` (`window-close.ts`), and another start brings the window to the front. When the connection drops, a hidden UI exits at once.
- **Tray exit and the installer's exit** (`daemon-exit.ts`, `close-guard.ts`): the daemon sends `daemon.exiting`, which Main intercepts before it enters the event buffer. With the reason `tray` or `installer`, Main stops captures, asks the renderer to commit and waits for the save (at most 1800 ms), then calls `ui.exitReady` (waiting at most 500 ms), hides the window and exits. A slow save, a daemon that does not answer, or a failed call cannot stop the exit.

### 12.10 Differences between the packaged and the development build

| Item | Installed (`app.isPackaged`) | Development (`task app:dev`) | E2E |
|---|---|---|---|
| Data folder | The real `%APPDATA%\padmate` | `.cache/ui-data` | A private temporary folder |
| Daemon | `--inject --data-dir` | `--dry-run` | `--dry-run`, optionally with `--fake-input=ipc` |
| Pipe, discovery folder, mutex, browser data | Default | Isolated in `.cache/ui-*` | Private |
| Start at sign-in | Can be switched | `startup.set` returns -32017 | Same |
| Automatic updates | Supported | `supported:false` | Only in test mode with a loopback feed |

- **The installed app's test mode** (`main/runtime-options.ts`): requires `PADMATE_UI_TEST_PACKAGED=1` and all three private folders `PADMATE_UI_DATA_DIR`, `PADMATE_UI_IPC_DIR` and `PADMATE_UI_USER_DATA`; with any one missing, it runs as the real app (the real data folder plus `--inject`). Test mode uses the daemon and CLI in the bundled `resources\bin`, starts them with `--dry-run` and never `--inject`; the child processes' environment sets `PADMATE_PIPE` and `PADMATE_IPC_DIR`, with `APPDATA` and `LOCALAPPDATA` pointing to `<userData>\daemon-home\`; Main refuses `startup.set`.
- `PADMATE_UI_TEST_UPDATE_URL` takes effect only in test mode and only for a loopback address; it replaces `UPDATE_URL`, and `quitAndInstall` is replaced by a function that only records.
- Never start the installed padmate.exe (including `dist/installer/win-unpacked`): it uses the real data, and its daemon runs with `--inject`.

### 12.11 Generated types and accessibility

- `schema/{profile,config,state}.schema.json` are the three sources. `daemon/tools/codegen` generates `daemon/internal/profile/wire_generated.go` and `app/src/shared/generated/schema.ts`; `task gen` regenerates them, and `task app:gen-check` checks for drift. `shared/api.ts` is the hand-written `PadmateAPI`. The Go and TS sides are kept in line by tests (`reasonSentences`, `vectors.json`, `macro-names`); a change on one side needs the other.
- The accessibility requirements are in [ADR: Editor UI](adr/0013-editor-ui.md). Conventions of the implementation: dimmed text uses token colors, not `opacity`; every stylesheet ends with `@media (forced-colors: active)`, and wherever a `Highlight` background is written, `forced-color-adjust: none` is written too.

## 13. Time and latency

**Clocks.**

- The engine reads no time; time comes from `Input.T` and the `now` parameter. The slot loops, T-output and the router each have a `clock.Alarm`: it follows an absolute deadline and does nothing when the deadline has not changed, so a loop resetting it while a test advances a Fake does not push the deadline back.
- `clock.Real()` is the only real clock in the process; on Windows it jumps only every 0.5–2 ms, so for microsecond precision use QPC (`latency.Now`). `clock.Fake` advances only on `Set` and `Advance`, firing timers in deadline order; to run work at each deadline in turn, advance one at a time with `Next` and `Set` (as simtest does).

**End-to-end latency of the benchmark.** `L = t_ret − t_sdl`: from the timestamp SDL gives the edge (`Event.Time`, converted to QPC with `latency.CalibrateSDL`) to the return of the Send that carries the key of that edge. The target is p99 < 1.5 ms. How it is measured is `TestLatencyLive` in §14.

**Latency statistics in the product.** It measures `L′ = t_ret − t_in`, each time, inside the daemon. `t_in` is the `Event.Stamp` that T-input reads after the event returns (events read by the rescan after waking have no Stamp and are not measured). `slot.Handle` carries `t_in` and the DeviceKey with the command to T-output through `RemoteArbiter.MarkInput`; the Arbiter reads `t_ret` after its first call to the Sender and spends the mark, so an edge is measured once. Only keys really sent count; keys that do not come from an input edge (repeat, keys sent when their time comes, the later steps of a sequence, pointer movement, keys sent by scripts and so on) do not. With a global `--dry-run`, the Sender is the printer, and it still counts, flagged by `dryRun` of `latency.get`. `latency.Recorder` has one preallocated ring of 1000 per DeviceKey, for at most 8 controllers; writes happen on T-output without allocating. The UI marks a warning only with 50 samples, and the threshold is `latency.WarnP99` = 1.5 ms.

The maximum rate of the same key is 25 Hz (§7.6).

## 14. Tests

### 14.1 Layers

| Layer | Where | Task | Notes |
|---|---|---|---|
| Unit and golden tests of pure packages | `daemon/internal/*` | `task test` | Engine, slot, supervisor, profile; must run on Linux |
| simtest | `internal/simtest` | Imported by other tests | A fake clock plus a real Arbiter, with `AssertNoneHeld` at the end |
| fuzz | `Fuzz*` in each package | `task fuzz` | The list of targets is in `Taskfile.yml`; a new parser or format field needs a target |
| SDL virtual gamepads | `internal/input`, `cmd/*` | `task test:sdl` | No hardware needed; run `task sdl` first |
| Real injection | `internal/injecttest`, `output`, `winhook`, `cmd/*` | `task test:inject` | Windows only; every event is swallowed by a hook (§14.4) |
| Keyboard layouts | `internal/kblayout`, `output` | `task test:layouts` | The machine's layouts against the `testdata/layouts` fixtures |
| Latency benchmark | `internal/latency` and `Latency*` in each package | `task bench` | Windows only, about 4.5 min; how it is measured is in §13 |
| Frontend Vitest | `app/src/**/*.test.ts(x)`, `app/scripts/*.test.mjs` | `task app:ci` | typecheck, Biome, Vitest, build |
| E2E | `app/tests/e2e` | `task app:e2e` | Real Electron plus a private fake-input daemon |
| Installer smoke test | `internal/installersmoke` | `task test:installer` | Install, reinstall, uninstall |
| Documents | `app/src/renderer/*.test.ts` (user-docs, doc-names) | In `task ci` | See §14.5 |

General rules:

- Engine-related packages import no platform packages and never call `time.Now` (invariant 2; depguard covers `_test.go` too); tests use `clock.Fake`. Check with `cd daemon && GOOS=linux go vet ./...`.
- Every test that holds keys runs `AssertNoneHeld` at its end (invariant 1).
- Tests that need SDL, real injection or a session skip when the condition is missing; set `PADMATE_REQUIRE_SDL=1` and `PADMATE_REQUIRE_SESSION=1` to make them fail instead.
- `TestMain` replaces the IPC pipe, the temporary folders and the name of the emergency release event with its own, so it never touches the user's real instance.
- `-race` needs cgo, so only the remote CI's `go-race-ubuntu` runs it; the zero-allocation checks are skipped under `-race`.
- Automated acceptance runs locally; manual acceptance that needs hardware or human judgement is done by a maintainer.

### 14.2 Golden tests and traces

- **Slot golden tests** (`internal/slot/testdata/golden/<case>/`): `profile.json`, `input.trace`, `output.trace` (including the final ReleaseAll), and an optional `decisions.jsonl`. Update with `go test ./internal/slot -run TestGolden -update -v`.
- **Supervisor golden tests** (`internal/supervisor/testdata/golden/<case>/`): `profile.json`, `input.jsonl`, an optional `session.txt`, `output.trace`, `slots.txt`. Nothing may be held at the end, and there must be no log at WARN or above. Update with `go test ./internal/supervisor -run TestGolden -update -v`.
- **Profiles**: `internal/profile/testdata/valid|invalid/*.json` each have a `.golden`; `examples/profiles/*.json` must have no diagnostics. Update with `go test ./internal/profile -update` and `go test ./cmd/padmatectl -run TestValidateGolden -update`. `TestNoSendInput` of `cmd/padmated` makes sure only `sender_windows.go` can refer to SendInput (invariant 6).
- **Signing and PE checks**: they sign nothing, install no certificate and run none of the files checked, and run on every platform. `task test:sign` runs locally on Windows and is not part of `ci`.

### 14.3 simtest

`internal/simtest` is the simulation harness shared by the tests of output, the engine, layers and scripts; only tests import it (depguard).

- **Trace notation**: one line writes input edges and output key events alike, such as `+south@0 -south@120`, `lefttrigger=0.35@10`. `+name[@t]` presses, `-name[@t]` releases, `name=v[@t]` is a value; `#` to the end of the line is a comment; t is in milliseconds, and when left out it keeps the time of the previous token; it cannot decrease.
- `Recorder` is a fake `output.Sender` that tracks the keys physically down; `SyncRecorder` adds a lock, and `WaitFor` waits up to 5 s of real time, the only real-time wait in simtest.
- `Sim` puts a real Arbiter on a `clock.Fake` writing into a Recorder, and `WithMenuMask` puts a MenuMask between the two. `Advance` stops at every timer and Arbiter deadline; `Settle` advances until there is no deadline, and fails after more than 1 minute of virtual time; `LoadScript` compiles and activates a temporary script, whose output can only reach the Recorder through the Sim's Arbiter and never calls SendInput.
- `simtest.New` registers a cleanup that runs `AssertNoneHeld` at the end: keys physically down in the Recorder, and keys held or waiting to be sent again in `Arbiter.Held()`, fail the test; script runtimes are disposed before the check.

### 14.4 Tests with real injection, and their hook rules

Windows only; they run only with `PADMATE_TEST_INJECT=1`. `task test:inject` first runs `task sdl`, sets `PADMATE_REQUIRE_SDL=1` and `PADMATE_REQUIRE_SESSION=1`, and runs `injecttest`, `output`, `winhook`, `cmd/padmatectl` and `cmd/padmated` with `go test -p 1 -count=1` (`-p 1` keeps one test's hook from seeing another's keys). Run it after changing output, the hooks, `injecttest`, `padmatectl send`, padmate-probe or padmated's live mode.

**The rule: every key and mouse event must be swallowed by an LL hook** (invariant 6):

1. `injecttest`'s hook swallows only tagged events whose nonce belongs to this test, so the physical keyboard keeps working.
2. Before the first real key, a canary is sent (a release of the unassigned VK 0x97); if the hook does not swallow it within 3 seconds, the test fails.
3. For keys the test sends itself, every batch is followed by a canary in the same SendInput (`InjectFenced`); if an event was not swallowed, the count is wrong or only part went out, it stops at once. For keys that padmatectl or padmated send, the canary is sent after it has exited or stopped.
4. Tests that inject many batches fence in O(1) with `Recorder.WaitOwn(n, timeout)`, without a canary after each batch, but still send a canary before the first real key. If an event was not swallowed, it returns `ErrUnswallowed`, and keeps returning it; when the hook stopped, `ErrHookStopped`; when the system removed the hook silently, nothing is seen at all, which shows as a timeout. `WaitOwn` and `WaitFence` share one wake-up channel, so only one goroutine may call them at a time.
5. Mouse events are handled the same way, with a `WH_MOUSE_LL` hook installed besides (`Recorder.MouseCanary`; the canary is a pair of relative moves with the nonce, +1/-1). `InjectFencedMixed` refuses mouse batches without `MouseCanary` first, and batches whose press and release are not in the same batch, so if the hook ever missed something, it would be one click and never a held button. Pointer movement uses `InjectFencedMoves`, and Unicode text `UnicodeCanary`.
6. When the hook times out (LowLevelHooksTimeout), that key is not swallowed and the hook may be removed silently (PITFALLS "Keyboard output and layouts"), so every batch is fenced and injection stops on failure.

End-to-end tests through padmate-probe (such as `TestPadToProbe`) run `padmate-probe record --json --swallow …` (the probe has its own swallowing hook), with a `Backstop` Recorder in the test process as a second line of defense. Session tests send session messages only to the test's own window, and never really lock, sleep or sign out the machine.

Other special tests:

- **SDL** (`task test:sdl`): skipped when SDL3 cannot be found. Run it after changing `internal/input` (`combo.go` included), `padmatectl monitor`/`record` or padmated's live mode.
- **Layouts** (`task test:layouts`, `PADMATE_TEST_LAYOUTS=1`): compares the machine's layouts with `daemon/testdata/layouts`, running the two packages one after the other with `-p 1` (a session has one list of layouts). `task layouts` generates the fixtures.
- **Latency benchmark** (`task bench`, `BENCH_SCALE` 1 by default, at most 10): runs the `Latency*` benchmarks, `TestInjectLatency` (SendInput on its own) and `TestLatencyLive` (a virtual gamepad driving padmated's live mode, with real injection) in order. The reports go to `bin/sendinput.md` and `bin/latency.md`, and `PADMATE_BENCH_GATE` controls the gate (0 only reports). Run it after changing the hot path of input, supervisor, slot, output or engine, and compare with the last report.
- **E2E** (`task app:e2e`, Windows only, not in `task ci`): real Electron with private folders, a private pipe and a daemon with `--dry-run --fake-input=ipc`; it sends no keys to the system. It needs its windows in the foreground, so rerun it on its own when they are taken. It opens windows, so run it when nobody is using the computer.
- **Installer smoke test** (`task test:installer INSTALLER=<absolute path> [UPGRADE=<absolute path>]`): checks installing, reinstalling and uninstalling, that `padmatectl quit` makes the daemon leave, and that data and the startup entry are kept. It temporarily writes real HKCU uninstall entries and Start menu entries; do not run `*-setup.exe` by hand. After changing `daemon.exit`, `padmatectl quit`, `installer.nsh`, `electron-builder.yml` or the startup arguments of the installed UI, package and run it again.

### 14.5 CI tasks

**The local `task ci`** (stopping at the first error) runs in order:

1. `vet`: `go vet ./...`
2. `lint`: `golangci-lint run` (configured in `daemon/.golangci.yml`, the same version, v2.13, as the remote CI)
3. `test`: `go test ./...`
4. `build`: the three commands built into `bin/`
5. `pe:check`: `go run ./tools/pecheck -bin ../bin` (Windows only)
6. `app:gen-check`: `go run ./tools/codegen -check`
7. `app:ci`: typecheck, lint, test, build

The document tests are part of it: `doc-names.test.ts` and `user-docs.test.ts` check that the UI names the user documents quote are adjacent Chinese and English pairs in the UI's source (the name table, `app/tests/docs/names.ts`, lives outside `src/renderer`), and that the English and Chinese documents have the same block structure.

Special tests not in `task ci`, to run separately: `test:types` (with `PADMATE_TEST_TSC` set, the ordinary `task ci` runs it too), `test:sdl`, `test:inject`, `test:layouts`, `bench`, `app:e2e`, `test:installer`, `test:sign`, `fuzz`. Which one a change needs:

- Output, the hooks, `internal/injecttest`, `padmatectl send`, padmate-probe or padmated's live mode: `test:inject`.
- `internal/kblayout` or `internal/output/layout_windows.go`: `test:layouts`.
- `internal/input` (the emergency combo's `combo.go` included), `padmatectl monitor`, `record` or padmated's live mode: `test:sdl`.
- `internal/winsession`, or the code of padmated that connects it (`startSession` in `live.go`): `test:inject` (`TestSessionToProbe`) and `test:sdl` (`TestLiveFlagsSmoke` uses a real T-session).
- The hot path of input, supervisor, slot, output or engine: `bench`, compared with the last report.
- `daemon.exit`, `padmatectl quit`, `app/build/installer.nsh`, `app/electron-builder.yml` or the startup arguments of the installed UI: package, then `test:installer`.
- After upgrading goja or esbuild, or changing `schema/profile.schema.json`, run `task gen`, or `TestGeneratedFilesCurrent` / `TestSchemaCopyCurrent` fail.

**The remote CI** (`.github/workflows/ci.yml`, triggered by pushes to main and by pull requests):

| Job | Environment | Content |
|---|---|---|
| `go-windows` | windows-latest | vet, build, `tools/pecheck`; downloads a cached `SDL3.dll`; `go test ./...` (`PADMATE_REQUIRE_SDL=1`); `git diff --exit-code` after `go generate`; a subset of the real-injection tests (`-p 1`); a latency smoke test (failing only on measurement errors, with the numbers in the job summary) |
| `go-race-ubuntu` | ubuntu-latest | `go test -race ./...`, with the SDL tests skipped |
| `lint` | windows-latest | golangci-lint v2.13; `_windows.go` is checked too |
| `fuzz` | ubuntu-latest | A subset of the `fuzz` targets, 60 s each; a failure uploads `fuzz-corpus` |
| `frontend` | windows-latest | Node 24, `npm ci`, `npm run ci`, `codegen -check`, `npm run test:e2e` |

Releases have `.github/workflows/release.yml` besides (§15): it builds the installer, runs the installer smoke test, checks the release files and creates a draft release. `ci.yml` has no packaging job.

## 15. Building and packaging

Only the structure of the build and its constraints are here. The reasons are in [Installer and updates](adr/0018-installer-and-updates.md).

### 15.1 Executables and SDL

- `task dev` builds with the console subsystem; release and package use `-H=windowsgui`. The exes are compiled in an isolated temporary folder, the pinned pure-Go winres writes the icon and version resources, and after verification they are published file by file; a failed build keeps the old exes.

- `SDL3.dll` sits next to the exes, with its version and SHA-256 pinned in `internal/sdlpin`; `task sdl` downloads and checks it and writes it to `bin/`. The steps of upgrading SDL are in the comments of `sdlpin.go`; after an upgrade, the hardware tests must be run again.

### 15.2 The portable package

`task package:zip VERSION=…` builds the portable zip: it verifies the versions of both exes and the DLL's hash again and refuses mixed versions; archive paths refuse traversal, symbolic links, reserved names and duplicates that differ only in case.

### 15.3 The installer

`task package:installer VERSION=vX.Y.Z`: a version check, building the exes, taking the pinned `SDL3.dll`, `packageinstaller stage`, `app:build`, electron-builder (`app/scripts/package-installer.mjs`, `app/electron-builder.yml`, `app/build/installer.nsh`), `packageinstaller verify`. The output is in `dist/installer/`.

- It installs per user, never elevates, and is a wizard: a language dialog in both languages, the folder page, a page with the box “Create a shortcut on the desktop”, the progress, the finish page. The default is `%LOCALAPPDATA%\Programs\padmate`; in the wizard it installs into a `padmate` subfolder of the chosen folder (`installerpkg.WizardDir`). The language and the box are in `app/build/padmate-options.nsh`, whose rules `installerpkg.InstallerLanguage`, `WritesLanguage` and `DesktopDefault` mirror; electron-builder makes no desktop shortcut (`createDesktopShortcut: false`) and asks for no language (`displayLanguageSelector` unset). `padmate.exe` is at the root, and `resources\bin\` holds `padmated.exe`, `padmatectl.exe` and `SDL3.dll`. The uninstall entry's GUID is fixed and never changes.
- **The only source of the version** is the release tag `vMAJOR.MINOR.PATCH[-PRERELEASE]`. The Go executables report the tag (ldflags); `padmate.exe`, `app.asar` and the installer use the version without `v` (`extraMetadata.version`, without changing `app/package.json`). `packageinstaller verify` reads every one of them back.
- **Code signing** is off by default. `PADMATE_SIGN` is `none`, `store`, `pfx` or `azure`; every signing mode comes with `forceCodeSigning=true`, and `electron-builder.yml` holds no certificate, password, thumbprint or account.
- **PE checks.** `task pe:check` checks `bin/padmated.exe` and `bin/padmatectl.exe` with `pecheck`. `-- -update` rewrites the lists after an intended change; look at the diff in review: each added line is one more Windows function padmate can call.

### 15.4 Installing, upgrading, uninstalling

`installer.nsh`, at the start of each of the three operations:

1. Runs `padmatectl quit --timeout 10s --under "<installation folder>"` (§11); `--under` keeps padmated outside the folder from being asked to exit.
2. Only if quit fails, runs `padmatectl release-all`.
3. Waits 10 seconds for the programs in the installation folder to exit, and calls `TerminateProcess` on any that are still there. It judges by whether the process's image path is inside the installation folder, not by name, so development builds and portable copies outside it are not affected. Before ending a `padmated.exe`, if `release-all` has not run, it runs it once: ending the daemon does not release the keys it holds.

Other rules:

- A full uninstall (not an upgrade) deletes the Run value (only when it points into this installation folder) and the `run` and `logs` folders, and asks whether to keep the profiles, scripts and settings in `%APPDATA%\padmate`; a silent uninstall always keeps them. When `/D=` points to a folder holding other files, the installer exits with code 2 (uninstalling deletes the whole installation folder).
- At the end of an install that is not an update, the installer writes the language chosen to `%APPDATA%\padmate\ui\language.json` unless the file holds a language picked in Settings (anything but one of its own two lines), records it as `Language` under the install key for the uninstaller, and makes or removes `$DESKTOP\padmate.lnk` as the box says. The box starts ticked on a fresh install, and over an installation only when the shortcut is there. A full uninstall, and the uninstaller an upgrade runs without `--keep-shortcuts`, delete the desktop shortcut.
- Silent installs: `/S` takes the system's display language (any Chinese gives zh-CN, the rest en) and the box's default; `--language=zh-CN` or `--language=en` names the language (and skips the dialog of a wizard), and `--no-desktop-shortcut` makes no shortcut and removes one.
- An update is `<installer> --updated /S --force-run`: silent, without `/D=`, starting `padmate.exe` as the current user at the end; it writes no language and leaves the desktop shortcut as it is. The updater's downloads in `pending\` live in `%LOCALAPPDATA%\padmate-updater\`: only a full uninstall deletes the whole folder recursively, since the installer running an update is inside it.
- **Never start the installed `padmate.exe` directly**: the daemon it starts runs with `--inject` and uses the real `%APPDATA%\padmate`. The test mode is in §12.

### 15.5 Releases

- **GitHub draft releases** (`.github/workflows/release.yml`): on a pushed `v*` tag or a manual run, it checks that `latest.yml` matches the installer, then runs `gh release create --draft`. A version with `-` is a prerelease.
- **Publishing to the download site** (`task release:r2 VERSION=vX.Y.Z`, optionally with `--dry-run` or `--force`): run by a maintainer, it is the only thing that writes to the Cloudflare R2 bucket `padmate` (`https://padmate.download/`), and the repository holds no credentials. Before uploading it refuses: a prerelease tag, a `latest.yml` that does not match the installer, an installer already on the site (published installers never change), and a version not newer than the site's. The order: the installer, the `.sha256`, and only then `latest.yml`; after uploading, it reads them back over HTTPS to check.
- **The download page** (`site/`, details in [site/README.md](../site/README.md)): the Cloudflare Worker `padmate-download-root`, routed on exactly `padmate.download/`, `padmate.download/latest`, `padmate.download/zh*` and `padmate.download/en*`, never `/*`, so `latest.yml`, installers and `.sha256` files are served by R2 and the updater never passes through it. `/` gives a browser (`Accept` includes `text/html`) the page in the language of `Accept-Language`, and `/zh` and `/en` give it in that language to anyone (a route matches the query string and cannot name one, so the page has its own paths, and `/` and `/latest` with a query never reach the Worker). The star count of the GitHub repository is fetched by the Worker; while GitHub gives none, the page shows the last good count of the past week. Anything else gets the 302 to the installer `latest.yml` names, which is also what `/latest` always does. `latest.yml` is read through the Cache API for 60 seconds, so a release shows on the page within a minute. The page has no script and makes the browser request nothing from another host. `task site:deploy` runs only when the owner says to publish; it writes the live Worker with the wrangler of the release machine, and the repository holds no credential.

### 15.6 Taskfile tasks

`Taskfile.yml` is the reference, `task --list` lists everything, and the common tasks are in [CLAUDE.md](../CLAUDE.md). `ci` includes no real injection, forced SDL, latency benchmark or Playwright. `test:inject` runs `sdl` first and sets `PADMATE_REQUIRE_SDL=1` and `PADMATE_REQUIRE_SESSION=1`. `bench` takes about 4.5 minutes; a test that times out runs no cleanup, so the tests check their deadlines themselves (§14). `test:installer` temporarily writes real uninstall entries and shortcuts, and refuses to run on a machine with padmate installed. The tests of the download site (`site:test`, Node's own runner, no dependencies) are part of `ci`; `site:dev` and `site:dry-run` change nothing online, and `site:qr` regenerates the donation QR codes of the page (§15.5).

## 16. App integrations

The decisions are in [ADR: App integrations and CLIP STUDIO PAINT](adr/0019-clip-studio-paint.md), the format facts of CSP in [PITFALLS](PITFALLS.md#clip-studio-paint). v1.2 is being implemented; this section grows with the code.

An app integration lets padmate read and write the shortcut settings of an app. The generic parts (the file beside the profile, the IPC, the UI) know no particular app and work only by the integration's ID. For now there is one integration, `csp` (CLIP STUDIO PAINT).

### 16.1 Binding

- A profile is bound to an integration when `rules` in config.json has a rule whose program is the integration's executable (compared by file name, case-insensitive) and which selects this profile. Without a binding, the UI shows nothing of the integration and the daemon makes no sync check. The bundled profile `csp` ships with this rule (§10.1).
- `integrations/<id>.json` only keeps the items; it does not decide the binding. Without the file there are no items. Copying a profile copies the file; with inheritance, the child's items lie over the parent's, and for the same key the child wins.

### 16.2 `integrations/<id>.json`

```json
{
  "format": 1,
  "items": [
    {"keys": "Ctrl+Z", "function": "command:basiccommand/editundo", "name": "撤销", "path": ["编辑"]},
    {"keys": "P", "function": "tool:3f2a…（32 位小写十六进制）", "name": "G笔", "path": ["沾水笔", "沾水笔"]}
  ]
}
```

- Each item says "this key is this function in the app". `keys` is one KeySpec chord (§8.1), not repeated in the file; `function` is an opaque ID the integration defines (CSP: `command:<menucommandtype>/<menucommand>` or `tool:<node UUID>`); `name` and `path` are only for display and are ignored when comparing.
- Strict JSON, read by the same scanner as profile.json; at most 512 items; a member it does not know is an error.
- An item whose key no button of the profile sends is ignored when comparing, in `plan` and in the activation check. It stays in the file, and `integrations.items` still returns it in `items`; only `statuses`, `consistent` and `differenceId` cover the used items alone. The keys sent are collected by `integration.UsedKeys` from the merged profile (canonical spelling): the chords of keys actions (including their `Sequence`) and the shortcut steps of sequence actions, for every gesture in the common layer, every layer and the function sets. Waits, texts, macros, scripts and the other actions do not count.
- Only the daemon writes it: temporary file, Sync, rename, and the old bytes go into `.history` before replacing.

### 16.3 The daemon

- `internal/integration`: the generic types (function, item, check result, comparison state, change plan), parsing and formatting of `integrations/<id>.json`, and the integration registry.
- `internal/integration/cspint`: the CSP integration. It joins `internal/csp` (pure logic: models of both files, conversion between key names and KeySpecs, conflicts and cycle keys, the comparison, edits on a copy) and `internal/cspfile` (locations, finding `CLIPStudioPaint.exe` by image name, backups, safe replacement, restore). For names see ADR-0019 section 3.
- **CSP's files**: `%APPDATA%\CELSYSUserData\CELSYS\CLIPStudioPaintVer*\Shortcut\default.khc` (commands) and `Tool\EditImageTool.todb` (tools). They are opened read-only; changes are made only on a copy in the same folder, which then replaces the file atomically. `integrations.csp.dataDir` in config.json can name the folder instead of the automatic search.
- **Backups**: `<data dir>\integration-backups\<integration ID>\<UTC time>\`, at most 30 per integration.
- **Sync check**: when a profile bound to an integration becomes the active profile, and whenever the app's files change while it is active, the daemon compares the profile's items with the app's current settings. If they differ, and this set of differences (`differenceId`, the SHA-256 of the differences) is not one the user chose "Not now" for, it sends `integration.changed {kind:"differences"}` and notifies once through the overlay and once through the tray. "Not now" is kept in state.json as `integrations[<profile ID>][<integration ID>].dismissed`. Details are under "Implementation" below.
- The engine packages import none of these packages.

**Implementation**

- **One place decides binding**: `integration.Bound(config, profile ID, exe list)`; `BoundProfiles` is built on it. It goes through `Config.Select`: for the app's executable (the bare file name, and the paths of enabled full-path rules with the same file name), the profile the first matching rule selects is bound; with automatic switching off, no profile is bound. The `profiles` of `integrations.list`, the activation check and the watcher call only these two, so a later change of the rule touches one place.
- **Reads** never open CSP's SQLite files in place. A read copies `default.khc`, `EditImageTool.todb` and `Preference\Config.sqlite` to a temporary folder and reads the copies. The snapshot is cached by the stat (size, modification time) of each file and not read again while that is unchanged; `Revision` is a fingerprint of those stats, so polling is cheap.
- **`toolShift`** comes from `Preference\Config.sqlite`, table `Tool`, column `UseToolShift`. When it is true, a check of a single key without modifiers carries the `toolShift` note. `modifierKey` is not produced yet (that file is not decoded).
- **Write**: all changes are validated as a whole first (the function exists, the key can be written, no duplicate or clash); one bad change refuses the whole set (-32602, `data {function, keys, reason}`). Removes are done before adds. Changes that change nothing write no file and make no backup; `backup` of `integrations.write` is then `null`. Before writing it backs up through `internal/cspfile`, then re-checks that the app is not running, that the fingerprints are unchanged and that there is no journal file; after writing it reads back and verifies.
- **Items file**: item keys are stored in canonical spelling (`integration.CanonicalKeys`; `putItems` canonicalizes). Before replacing, the old bytes go to `.history/integrations-<integration ID>-<time>.json` in the profile folder, at most 30 per integration. They are not versions of profile.json, and `profiles.history` does not see them.
- **Names**: `cspint.NamesNamer` joins `internal/cspnames` to `integration.Namer`. Order: the app's menu resource, then the bundled table, then the plug-in DLLs, then the command ID itself. The install folder comes from `cspfile.FindInstallDir`: the uninstall entry's `DisplayIcon` (preferred when it names `CLIPStudioPaint.exe`), `InstallLocation` itself, its `CLIP STUDIO PAINT` subfolder, and `InstallLocation\CLIP STUDIO*\CLIP STUDIO PAINT` (the real layout of CSP 5: `CLIP STUDIO 1.5` sits below `D:\CELSYS`); the pure `cspfile.InstallFromEntry` has table-driven tests. Each language is read once; **with no install folder the bundled table is still used** (it holds the dialog's tree and the names of the commands outside the menus), only the menu commands lose their names; a lookup that found nothing or could not read the resource is repeated every 30 s, so a CSP installed later is picked up. When names are incomplete `integrations.list` carries `namesProblem` and the daemon logs one warning. `padmate-probe csp-names` regenerates the bundled table.
- **Tree**: the `tree` of `integrations.functions` is built by the integration; the generic `integration` package knows no CSP. `Namer.Layout` gives the tree of the app's own dialog (the `tree` of the bundled table, which `padmate-probe csp-names` recovers from the function in the exe that builds the dialog tree: groups, levels, order); the integration places the commands of the user's khc by it and drops groups with no command left. A command the tree lacks goes under its plug-in category in the Filter group (plug-ins), into the group its menu path matches, or into a trailing "Other". The Tools area is second. Details in "The dialog tree" below.
- **The dialog tree**: the top-level nodes are the entries of CSP's "Setting area" drop-down: Main menu, Tools, Pop-up palettes, Options, Pen and Touch (names from CSP's resources); Auto actions has none, because it holds the user's own commands. The children of "Main menu" are the menu bar: File, Edit, Animation, Layer, Select, View, Filter, Window, Help, with submenus below. The tree in the exe is the union of several platforms and modes: a command it places more than once stays at its last place inside the menu bar (so the Windows "Preferences" and the like sit under File); the groups of the phone layout are left out. The three levels of the Tools area (tool, sub-tool group, sub-tool) can each have a key, so every node is a function, and a node with children has both `function` and `children`; changing a key goes through the same `NodeShortCutKey` edit, and `cycle` lists nodes of any level that share the key. The third-level groups under the Tool Settings palette (Brush Size, Ink and so on) use another kind of reference in the exe whose names cannot be read; that level is dropped and its commands sit directly under "Tool Settings palette".
- **Language**: `language` may be left out; the language of the last request that named one applies, `zh-CN` if none did. The daemon's own notices use it too.
- **Watcher** (`integration_watch.go`): one goroutine, one round about every 2 s. Only when the active profile is bound to an integration, or a client subscribes to `integration.changed`, does it stat the integration's files (`Revision`) and take a process snapshot (`Running`): a change of the running state publishes `kind:"running"`, a change of the fingerprint publishes `kind:"files"` and runs the check again. With no bound active profile and no subscriber, a round only reads the rules in memory and touches no file or process. It is kicked at once when a profile becomes active (`publishProfile`) and after `putItems`, `write` and `restore`.
- **Activation check**: when a bound profile becomes active, or its app's files change while it is active, the items are compared. If all are consistent, the record of what was announced is forgotten. If not, unless the `differenceId` equals `Memory.Dismissed` or the one already announced in this run, it publishes `kind:"differences"` (with `profile`, `differenceId`, `count`) and notifies. An app that cannot be read (not found, unknown format) is logged once and shown nowhere.
- **Notices do not steal focus**: one short toast on the overlay (a `hud` toast, which only the overlay subscribes to) and one tray notification. Text: "<profile name>：<n> 个快捷键和 <app name> 里的不同，打开 padmate 处理" (English: "<profile name>: <n> shortcuts differ from <app name>; open padmate to review"). The daemon never opens or focuses the UI.

### 16.4 IPC

All are on the UI bridge allowlist. `integration` is the integration ID.

| Method | Description |
|---|---|
| `integrations.list` | `[{id, name, exe[], found, problem?, dataDir?, installDir?, custom?, namesProblem?, running, profiles[]}]`: each integration (`custom` is true when config.json's `integrations.<id>.dataDir` names the data folder), whether it was found on this machine (`problem`: `notFound`, `unknownFormat`, `unsupported`), whether the app is running, and which profiles are bound to it (§16.1). `namesProblem`: `installNotFound` (no install folder found) or `resourceUnreadable` (found, but the menu resource could not be read); some functions then show as IDs. Absent when the menu names were read |
| `integrations.functions {integration, language?}` | All functions in the app that can have a shortcut: `{revision, tree:[TreeNode], functions:[{id, kind, name, path[], keys[], unconfirmed?, unsupported?, cycle?[]}]}`. `TreeNode = {id, name, function?, children?}`, in the order of the app's own shortcut settings dialog; the top-level nodes are the "setting areas", a group has `children`, a function has `function` (an `id` of `functions`), and a node with both is a function and a group (CSP's tools and sub-tool groups). Every function is in the tree exactly once; node `id`s are unique within one result. `keys` are KeySpecs; `cycle` lists the other functions (of any level) that share the same single key; `path` is for search and display; `revision` is the fingerprint of the app's files |
| `integrations.check {integration, function?, keys, language?}` | Who owns this key in the app: `{status, owners:[{id,name,path?}], notes[]}`. `status`: `free`, `same` (it is this function), `cycle` (a cycle key, not a conflict), `conflict`, `unconfirmed` (a symbol key whose physical key is not confirmed yet), `unsupported`. `notes`: `toolShift` (holding it switches the tool temporarily), `modifierKey` (taken by the modifier key settings) |
| `integrations.items {profile, integration, language?}` | The items (with the parent's merged in) and the state of each: `{revision, items[], statuses:[{keys, function, state, current?[], owners?[]}], consistent, differenceId?, dismissed}`. `revision` is that of the profile's own `integrations/<id>.json` (empty without the file); `dismissed` is true when `differenceId` is the set that was chosen "Not now". `state`: `consistent`, `changed`, `removed`, `missing` |
| `integrations.putItems {profile, integration, revision, items}` | Write the items; returns `{revision}`. Keys are canonicalized; a bad key, a duplicate, too many items or an extra member is -32602; a `revision` that does not match is -32009 with the current one in `data.revision` |
| `integrations.plan {profile, integration, language?}` | The changes to make to the app when padmate wins, one entry for each item that differs: `{entries:[{item, changes:[{function, add?, remove?}], displaced:[{function,name,keys}], skipped?}]}`. `add` and `remove` are single KeySpec strings and `skipped` is a reason string; `changes` already holds the removals for the displaced functions. `displaced` lists the functions that would lose this key. Import uses it too; the UI builds the final `changes` from the user's decision on each item |
| `integrations.write {integration, changes}` | Change the app's settings: `changes` may touch any function (when a key is swapped, the displaced function is changed too), and everything is checked before anything is written; back up first, then write as in ADR-0019 section 5. Returns `{backup:{id,created,reason,changes}|null, revision}` (`backup` is `null` when the changes changed nothing) |
| `integrations.backups {integration}` / `integrations.restore {integration, backup}` | List the backups (newest first) as a bare array `[{id,created,reason,changes}]` / restore one; restoring backs up the current files first and returns that new backup (the same object). `created` is an RFC 3339 UTC time; `reason` is `write` or `beforeRestore`; `changes` is the number of changes of that write (0 for `beforeRestore`) |
| `integrations.dismiss {profile, integration, differenceId}` | "Not now" for this set of differences; kept in state.json; returns `{dismissed:true}`. `differenceId` must be 64 lower-case hex characters |
| `integrations.setDataDir {integration, dataDir, revision?}` | Set `integrations.<id>.dataDir` of config.json, or with `dataDir: null` take it out (the folder is found automatically again). `dataDir` must be an absolute path of an existing folder, at most 1024 characters, and is required (null or a string); whether it is the app's own folder is not checked here, `integrations.list` shows the problem. It edits by byte position like `pads.set` (every other member stays as it is), takes effect at once without a restart, and answers like `config.put` (`{revision, applied, …}`); a `revision` that does not match is -32009 |

An unknown integration ID is -32004; a bad ID, key, `language` or an extra member is -32602 (strict parameter decoding as elsewhere). Error codes: -32028 the app is running (`data {integration}`), close it first; -32029 the integration is unavailable (`data {problem}`); -32030 the app's files are in use or changed between reading and writing (a journal file, a different fingerprint); nothing was written.

Topic `integration.changed {integration, kind, profile?, differenceId?, count?}`: `kind` is `files` (the app's files changed), `running` (the app started or closed) or `differences` (the active profile has new differences).

### 16.5 UI

- Settings → the app's section: the "Data folder" row has "Change…", and "Find automatically" while `custom` is true. "Change…" is handled in the main process only: it shows a folder dialog (parented to the window), checks that the folder holds `Shortcut\default.khc` or `Tool\EditImageTool.todb`, and only then calls `integrations.setDataDir`; a folder that is not the app's is answered with one plain sentence and nothing is changed. The window never sends a path.
- For a profile bound to an integration, the shortcut action in the button drawer gets "Pick from <app name>…" beside "Record again". It opens a picker: it follows the app's own shortcut settings dialog: a "Setting area" dropdown at the top (its options are the top-level nodes of `tree`; hidden when there is one; it starts on the first), and below it the subtree of that area, with the levels and order of the app's menus. Groups start closed and what is open is kept while the dialog lives; a row is the name with the shortcut in a column on the right (empty when there is none); a node with both `function` and `children` (a tool with sub-tools) can be selected (click the row) and opened (click the arrow). A function with several keys takes several rows, the name on the first only; they are one item, and the row selected decides the key (the name row gives the first). Searching covers all areas and stays a tree: only the matching functions and the groups above them, all open; clearing it brings back the previous area and open groups. Keyboard: arrows move, right and left open and close, Enter selects; the semantics are `role="tree"`. The confirm button is "Set", and the selected function has to be among the rows the dialog shows now (a search that filters it out, another setting area or a closed group makes the selection void and greys the button): a function that has a shortcut has its item written, and only then are the key put into the binding and the name into its label (one edit); one without a shortcut records a key, `integrations.check` says who owns it in the app, then `integrations.write` and the item are written, and only when all of that worked does the binding change. A failure or a cancel leaves the binding alone. The dialog is drawn outside the shortcut field of the drawer (a portal), and its clicks and keys are not passed on to the field, so they cannot start a recording; recording has ended when the dialog opens. The `revision` of `integrations.putItems` is the empty string while the profile has no `integrations/<id>.json` yet, and that is valid.
- Before an action that writes to the app, the UI looks at `running` from `integrations.list`; if the app runs, a blocking dialog says "Please close <app name> first", with "Retry". After a write it offers "Undo this write", which takes back three things in this order: the app, the binding, the item file. First `integrations.restore` of the backup just taken (while the app runs the blocking dialog comes first; if it fails nothing else is touched). Then the binding goes back to how it was before the pick with `EditedStep.revert()`, returned by the editor's `capture`: it removes that step from the undo history and clears redo, so no step is left that could re-apply or half-revert the pick (if the binding was changed after the pick, `canRevert()` is false and the whole undo is refused before the app is touched). Last, the item file is put back by the inverse of the `ItemChange` (the item is removed if there was none before), with a fresh `revision` for `putItems`. If a step fails the window says which, and trying again goes on from the part that is not done (parts that worked are not repeated). The pick itself is one undo step of the editor, and `capture(change, effects)` attaches `effects` to it: a plain undo (Ctrl+Z) or redo moves the item file back or forward after the draft is shown (changes to one profile's item file are queued and made in order; a failure is shown by the shell's alert). The key written into the app is not taken back by a plain undo: it is only an added key. When `integrations.write` worked (`backup` is not `null`) and the item file could not be saved, the window stays on "The shortcut is written in <app>, but it could not be saved into the profile. The binding is not changed yet." with two ways out only: "Retry saving" repeats the item and the binding without writing the app again, and "Undo this write" restores the backup, after which nothing is changed anywhere; the close button and Esc do nothing there. When `backup` is `null` (the app was not changed) there is nothing to undo and a failure stays on the recording step. The picker pins the profile (and the binding's keys) when it opens: the item file, the binding step's undo and redo effects and "Undo this write" act on that profile only. If the editor has moved to another profile since (the tray or a controller switched it), the write, "Retry saving" and a pick that would be saved refuse with a plain sentence and change nothing, and "Undo this write" of a finished pick refuses too (an unsaved write has no pick and can still be undone, which touches no profile). If the drawer goes away while the window is on the unsaved step, the backup id is lost with it; the backup of that write is listed in Settings under the app, and the unsaved step says so.
- The reminder about differences never steals focus: the daemon only notifies through the overlay and the tray, and the UI never brings its window to the front because of it. When the user opens the UI and sees a bound profile with differences, a dialog in the UI lists every difference: "padmate wins" (`plan`, confirm the functions that would be displaced, then `write`), "<app> wins" (the buttons keep their functions and the keys change to the app's new keys, through ordinary editing and autosave, so it can be undone; then `putItems`), or "Not now" (`dismiss`).
- When a profile with items is imported and the user chooses to change the app, the items whose `plan` has `displaced` are listed first, then a conflict dialog lets the user decide item by item: "Use the imported setting", "Keep my setting" (this item is not imported, and the buttons that use the key are marked), or "Give one of them another key" (record a key on the spot; `check` looks for conflicts and cycle keys). `write` runs only after every decision is made. The choices on the import screen are "Change my <app> with the imported settings" and "Don't change it, adjust the buttons to my <app>".
- The Settings page shows a section for an integration only when at least one profile is bound to it: the data location (changeable), the backup list and "Restore…".
- The UI hard-codes no app: names, the tree and notes all come from the daemon.
- **Fake integration**: the `--fake-input --dry-run` daemon (used by the E2E) registers only one in-memory integration, `fakeapp` (name Demo Paint, `DemoPaint.exe`), and touches no real file: a menu area with a nested group, a tools area (a tool with sub-tools), functions with and without keys, one conflict (Ctrl+C), one cycle (P); `write` changes memory and returns a backup, and `restore`, `check`, `items` and `plan` work. `fake.integration {running?: bool}` sets whether it is running (it returns `{running}`); like the other `fake.*` methods it is not on the UI bridge allow-list, and the E2E calls it directly.

## 17. ADR

The architecture decision records are in [adr/](adr/README.md), by topic, each saying only what is decided now; when a decision changes, that record is edited.
