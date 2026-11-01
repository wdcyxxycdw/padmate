# ADR-0016: Explainable mapping and the Diagnostics page

[中文](0016-diagnostics.zh-CN.md)

Related: [ARCHITECTURE §7.8, §12, §13](../ARCHITECTURE.md); [ADR: Editor UI](0013-editor-ui.md); [ADR: Engine and threads](0005-engine-and-threads.md)

## Context

"I pressed it and nothing happened" is the most common problem of a mapping tool. Without a Diagnostics page, the only ways to look were replaying with `padmatectl trace` or reading the dry-run output of `--decisions`.

Every decision of the engine already reaches the UI over the IPC topic `engine.activity`, but decisions do not match key edges one to one: a press still waiting for its gesture has no decision; a tap first gets an `unbound` and only after the release a `dispatch`. Latency was measured only in the benchmark; the product code took no timestamps. On Windows, SDL ignores Steam's virtual controllers, so padmate cannot see them; reading Steam's internal files breaks "do not read an app's internal state".

## Decision

1. **"Diagnostics" is a sub-page under "About" on the Settings page.** It takes no place in the side navigation, since it is used only when something goes wrong. Its content has three sections: events, latency and conflicts.
2. **While the UI is connected to the daemon, it always records** the last 200 press events, not only once the Diagnostics page is open. Users first reproduce the problem in their painting app, when the page is not open. The events live only in the UI and are not written to a file; the output of `--decisions` and `trace` is unchanged.
3. **Each row says why nothing fired.** When it publishes a decision, the daemon adds the wall time `at`, `waiting` (a gesture still waiting, which is not a failure) and `waitedMs`, without changing the engine. The result text is written per gesture and reason, one sentence in Chinese and one in English for each reason; a test compares the UI's text table with Go's reason table, and a reason without text fails the test.
4. **The engine adds two kinds of decision:** the reason `script` (a script handled or swallowed the gesture); and `unbound` when a multi-tap window ends with no action at that count, recorded only for a cycle that has not been answered yet, so it never contradicts a key that went out.
5. **Presses of controllers without a mapping** (queued, ignored, without a standard mapping, disabled) are sent to the UI by the router as `unrouted` events.
6. **Publishing decisions moves off the hot path:** a publishing goroutine encodes them, with a limit; when it is full, the oldest is dropped and counted. The engine's and the slot's zero-allocation tests are unchanged.
7. **Where latency is measured:** T-input reads QPC as soon as it gets the SDL event and puts it in `input.Event.Stamp`; the Arbiter reads it again after `SendInput` returns, and the difference is the latency. Only keys really sent count: a preview does not; repeat, the later steps of a sequence, holds and multi-taps that fire when their time comes, and pointer movement do not go through an input edge and do not count. Each controller keeps its last 1000 samples, allocated in advance and written on T-output without allocating. It is measured the same way as the benchmark, and a p99 over 1.5 ms is shown in the warning color.
8. **Steam is only flagged as "may".** While `steam.exe` runs, the type, vendor and driver of each controller tell which kind of Steam Input setting would take it over; this uses the `conflicts` of `status.get` and its check every 5 seconds.
9. `profiles.explain` and `padmatectl profile explain` list the function key's set and mark the function key held right now.

## Costs

- The hot path gets two more QPC reads and one more field to pass on; after changing it, run `task bench`, `task test:sdl` and `task test:inject`.
- When the decision buffer is full, old decisions are dropped, and the Diagnostics page may miss a few.
- The event record is cleared when the UI restarts.
- padmate cannot confirm that Steam really took over a controller.
- The engine has the new reason `script`, and the reason table of the dry-run output and the decision golden files change with it.
- Latency counts from the moment T-input receives the event, a few microseconds less than the benchmark (which counts from the SDL timestamp).

## Alternatives given up

- **Diagnostics as a page in the side navigation.** It is looked at only when something goes wrong, so like the logs it sits in Settings.
- **Recording only once the Diagnostics page is open.** See decision 2.
- **Reading Steam's `virtualgamepadinfo.txt`.** That reads an app's internal state, against the project's rules.
- **Taking the result of an event from `output.preview`.** The UI's event buffer keeps only the newest preview and would lose some.
- **A new IPC topic, or re-rendering the whole UI on every decision.** It reuses `engine.activity`, and the diagnostic data is stored apart.
