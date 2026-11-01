# ADR-0005: Engine, slot loop, T-output and runtime supervision

[中文](0005-engine-and-threads.zh-CN.md)

Related: [ARCHITECTURE §5](../ARCHITECTURE.md#5-threading-model), [§13](../ARCHITECTURE.md#13-time-and-latency); [ADR: Several controllers](0003-devices-and-slots.md)

## Context

- Scripts take part in resolving a press (`global()` → overlay layers → base → common). The engine has to know on the spot whether a level consumed the event or handed it on with `e.pass()`, so the script must answer synchronously.
- A goja Runtime can be used by one goroutine at a time; the watchdog budgets thread CPU time; a reload must not stall mapping.
- Only the T-output thread calls SendInput (invariant 3), and errors in the commands others send must reach whoever sent them.
- Every exit path, including a panic in T-output or the slot loop itself, must release the keys.
- The channel of `input.Source` is bounded; when it is full events are dropped, and the dropped one may be the last release.
- The latency target: p99 < 1.5 ms from an SDL event to the return of SendInput.

## Decision

1. **The engine is pure.** `control`, `engine`, `keys` and `profile` import no platform, input, output or script package and never call `time.Now`; time comes only from `clock.Clock`. depguard and forbidigo enforce it. Scripts implement `engine.Level` through an adapter.
2. **The slot loop: the engine and the script on the same goroutine.** Each slot has one goroutine locked to an OS thread, and both the engine and the script runtime belong to it. A script Level is a plain function call; events never cross threads. A script is built and set up on a worker and then swapped in atomically, so declarative bindings never pause. Host APIs must return quickly, since `Runtime.Interrupt` has no effect inside a native function.
3. **T-output is `output.Loop`.** One goroutine locked to a thread owns the Arbiter, and nothing else touches it. Other goroutines hand it work with a synchronous `Do(f)`, and errors come straight back to the caller. The Sender chain is Arbiter → ledger → MenuMask → Sender; the ledger records the keys left held. `Stop` first does `ReleaseAll`, retries a failed release up to 3 times, and otherwise returns an error listing the keys that may be stuck.
4. **Panics and Goexit both release the keys.** A panic in T-output: the Arbiter's state cannot be trusted, so the keys in the ledger are lifted in reverse order and the loop ends; any later `Do` returns `ErrLoopClosed`, which the daemon treats as fatal. A panic in a slot loop: `ReleaseOwners` runs directly, bypassing the engine, and the supervisor restarts that slot. Goexit releases the same way.
5. **State and Resync.** A slot remembers the values of the buttons and axes it processed. `Resync` synthesizes the missing edges; it only makes the state consistent and does not replay what happened in the gap. When the state is unknown, `Reset` is used: ReleaseAll plus an engine reset.
6. **The router owns the device state, and delivery never blocks.** When an edge channel is full, that slot falls behind: later edges are skipped and counted, and a Resync follows when it catches up. **A lost event resets every slot.** **Sync is a barrier**: every event put into Events before the call, and its output, has been processed when it returns.
7. **Restart after a panic; disable after repeated panics.** Each slot has a runner that restarts it with back-off after a panic. A restart does not Resync, so the button that caused the panic is not pressed again. Three panics within 10 s disable the slot; the next assignment enables it again.
8. **Shutdown order.** The slots finish their queued edges and release, then the caller runs `Output.Stop`, and last `input.Source` is closed.
9. **Pure packages do not log** (depguard forbids it); errors come out through `Slot.Errors`.
10. **End-to-end latency.** A test drives padmated's live path with a virtual controller, real SendInput and a test hook that swallows the keys, and measures from the SDL event's timestamp to the return of SendInput. The gate is `task bench`: p99 < 1.5 ms for rows A, B and C, three runs in a row. Timestamps are taken only in tests. `timeBeginPeriod` is never called on the key path. The worst measured p99 is about 1.17 ms, nearly all of it waking threads and SendInput. If it fails later, mitigate stage by stage, and change the structure only as a last resort, with a new ADR.

## Costs

- While a script runs, the declarative bindings of the same slot wait for it; the handler budget and safe mode are the safety net.
- Every command is one round trip across threads; one slow Send holds up every slot.
- The latency margin is small (about 22%), varies with the machine and leaves out SDL's polling on real hardware (up to about 1 ms); four controllers pressing at once exceed 1.5 ms.

## Alternatives given up

- **A goroutine of its own for scripts, with the engine waiting on a channel; one script thread for all devices; scripts that only observe asynchronously.** The first is a round trip across threads every time and is hard to test deterministically; the second lets one device slow down all of them; the last loses `e.pass()` and the single resolution order.
- **Commands posted asynchronously, or an Arbiter behind a lock that each slot calls directly.** With the first, errors never reach the caller; the second breaks invariant 3.
- **Block when the channel is full, or an unbounded queue.** A stuck slot would hold up the router; memory would keep growing.
- **Resync on restart, or restart forever.** The button that caused the panic would be pressed again; it would release, restart and log without end.
- **Measure latency with a fake Sender only, or with `time.Now`.** The return of SendInput would not be measured; `time.Now` is too coarse.
