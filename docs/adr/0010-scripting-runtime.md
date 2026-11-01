# ADR-0010: Scripting runtime

[中文](0010-scripting-runtime.zh-CN.md)

Related: [ADR: Engine and threads](0005-engine-and-threads.md)

## Context

Scripts express logic that declarative mappings cannot: layers, gestures, continuous input, timing, feedback. The constraints: scripts and JSON bindings share gesture recognition and output; an error, an interruption or a replacement never leaves a key stuck; the end-to-end target on the script path is p99 3 ms; scripts are written in TypeScript, with type hints in the editor.

## Decision

1. **goja + esbuild.** esbuild's Go API bundles TypeScript into an IIFE that goja runs in the process; building needs no Node. Build results are cached by (source hash, esbuild version, API version); a build without a cache runs on a worker, and the slot loop does not wait for it. The basis: with a cache hit, activation is p99 0.86 ms; with a build it is 13.2 ms, over the 10 ms target.
2. **Our own event loop, inside the slot loop.** It starts no goroutines and only keeps timers and microtasks. Time comes only from `clock.Clock`, so a fake clock reproduces the order of timers and Promises. The only global is `console`; timing is `sleep`, `every` and `onTick`.
3. **The watchdog counts thread CPU time, not wall time.** Wall time would kill scripts by mistake under heavy CPU load, and the 15.6 ms granularity of `GetThreadTimes` is too coarse, so it reads `QueryThreadCycleTime`. Calibration errors only err low, so the watchdog can only fire early. Host APIs must return quickly, because `Interrupt` cannot stop a native Go function.
4. **Scripts share the recognizer, the Arbiter and the lifecycle with declarative mappings.** A script gives the engine a registry and callbacks; the pure engine imports no script runtime. `holdKeys` and `holdLayer` are bound to the press cycle and released when the cycle ends or is canceled. Script and declarative output share the minimum press time and the gap between presses of the same key.
5. **Input during a cold start is buffered and replayed.** Without a valid Host (say, at the first press after a safety release), named actions would be dropped and script gestures would wrongly fall back to declarative bindings. The slot holds up to 128 edges and, once ready, feeds them in their order to the same recognizer. On overflow, or when the cold start fails, the whole batch is canceled and released.
6. **`script.api` is `0.N`, and the host supports the current and the previous 0.N.** Older versions are refused and not migrated, because the daemon never rewrites a file for being old. The previous version loads as usual, and validation gives an upgrade warning. A new version only adds members, with no shims; members are marked `@since`, and the types are filtered by the profile's API. Taking something away from every supported version at once, as with the `guide` control ([ADR: Mapping model](0012-mapping-model.md)), is no new version either. 1.0 does not freeze the script interface.
7. **The overlay API (0.16).** Panels live in the runtime and are published with the slot snapshot of the state event, with no event protocol of their own; a runtime that is not active reports no panels, so no panel of something gone stays on the screen. Calling `hud` in top-level code or in `setup` throws; macros cannot use `hud`.
8. **The controller feedback API (0.17).** Wrong arguments throw `TypeError`; when the controller cannot do something, or its switch is off, the call is a silent no-op. What is stable is only the names, the arguments and "best effort"; `host.has` probes the capabilities. A script's strength is not multiplied by `feedback.strength`. Each runtime limits how many effects it starts per second, and stopping always gets through. When a runtime is no longer active, the effects it started are canceled, and the old ones are canceled before a new runtime becomes active.
9. **Sequences and text (0.21).** `key.type` and `key.sequence` follow the same rules as the profile's `sequence` and `text` actions, and each character and key counts toward the output rate limit.

## Costs

- After the cache is invalidated (an edited source, an esbuild upgrade), the first activation takes 11 to 21 ms.
- There is no `setTimeout`, so code copied from the web needs rewriting; the d.ts does not declare it, and the editor reports an error.
- The watchdog's calibration has no upper bound, so a bad calibration goes unnoticed. On an E-core the same budget does only about 60% of the work. The TSC on AMD and in virtual machines has not been tested.
- esbuild expands a computed `import()`/`require()` path as a glob, so directory listing still happens, although the file contents are blocked.
- What goja does not support and type checking does not catch (`using`, `\p{…}` and the `d`/`v` flags in regular expressions, the `Intl` runtime, async generator objects) is for script authors to avoid.
- When the cold-start buffer overflows, that batch of presses is lost.
- The real effect of the feel-type APIs has to be checked on real hardware.

## Alternatives given up

- **goja_nodejs's eventloop.** It owns its own loop, so the slot loop would have two sources of wake-ups, and a fake clock could not drive it.
- **Synchronous handlers only.** `sleep`, or holding for a while and then letting go, could not be written; users would busy-wait and trip the watchdog.
- **Build on every activation.** p99 13 ms, over the target.
- **A runtime per macro or per script.** Several runtimes, cold starts and watchdogs per controller; see [ADR: Scripts as macros](0011-script-macros.md).
- **`hud` events that "add" and "remove" panels, or a new `hud.attach`.** One missed event or a reconnect and they go wrong, and a synchronization protocol would be needed besides.
- **Multiplying a script's rumble by `feedback.strength`.** `strength` is the master volume of the presets; a user who wants silence turns off the "Rumble" switch.
