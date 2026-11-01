# ADR-0001: Processes and stack

[中文](0001-processes-and-stack.zh-CN.md)

Related: [ARCHITECTURE §2](../ARCHITECTURE.md#2-process-model), [§12](../ARCHITECTURE.md#12-electron)

## Context

The mapping that runs all the time (reading controllers, the engine, SendInput, scripts, the tray, IPC) must stay up, use little, add little latency and never leave a key stuck. Editing and hints (the visual editor, Monaco, the overlay) open only when needed. The UI is optional: with it closed, mapping works as before. The project has one developer plus AI assistants, so the toolchain must stay small.

## Decision

1. **Two processes, two languages.** The daemon `padmated`, the CLI `padmatectl` and the developer tool `padmate-probe` are Go; the UI and the overlay (`padmate.exe`) are Electron + TypeScript. The two sides talk only over IPC ([ADR: IPC](0009-ipc.md)). Scripts run inside the daemon (goja + the esbuild Go API); there is no separate runtime. Types both sides use are generated from the schemas.
2. **The daemon runs in the background, and its tray does not depend on Electron.** The tray's "Exit" first pauses mapping and releases every key, then publishes `daemon.exiting`, waits up to 2 s for the UI to confirm (`ui.exitReady`), then waits for the supervisor and T-output to finish releasing and retrying. A cleanup that times out is logged as a failure with exit code 1; the release phase is never cut short.
3. **One instance; a second start only forwards.** The named mutex is partitioned by the user's SID and the session. A second start parses no local configuration and opens no log and no SDL; it hands its request to the running instance with `instance.forward`. Startup settings that conflict with the running instance (output mode, data folder, input backend) are refused outright; the mode is never changed implicitly. When forwarding fails, the running configuration stays.
4. **The UI's security bridge.** The renderer runs with sandbox and contextIsolation on and nodeIntegration off; pages come from `padmate://app`, which sends a CSP; the preload exposes named methods only, and Main checks them again. Main reaches IPC through a child process, `padmatectl ui-bridge` (bounded NDJSON, an allow-list of methods); the token never reaches the renderer or the logs.
5. **While a padmate window is in the foreground, output is a dry run.** The check is by verified process instance; not every electron.exe counts as ours.
6. **Startup order.** The UI first connects to a running daemon. Only when that fails does it start one, detached and without a console; closing the UI does not end it. The packaged UI starts it with `--inject`; the development build, E2E and tests use `--dry-run` ([ADR: No stuck keys](0006-no-stuck-keys.md)).
7. **A failed start says why.** The packaged padmated has no stderr, so `--startup-report FILE` writes a JSON report and the daemon exits with code 3; Main reads the report (bounded in size, known fields only) and stops retrying. The UI then shows only the failure page: the reason, the path, "Open folder", "Retry" and "Exit". A broken config.json is not a failed start: the daemon backs it up and repairs it ([ADR: Profile library](0008-profile-library.md)).
8. **Closing the window only hides it.** While the daemon is connected and the page is ready, closing the window hides it; the page, the controller stage and the overlay stay, and opening it again shows the same window. The real exit paths (tray exit, the installer's exit, an update restart) wait for saving to finish first. Without a connection, or after a failed start, closing the window exits.

## Costs

- Two toolchains; the contract between the processes (the IPC `protocol`, the generated types) must be kept in sync.
- The UI needs one more process, the CLI bridge, and a `padmatectl` of the same version. In return it reuses the verified pipe identity checks and needs no native Node module.
- The packaged app sends real keys, so development and E2E must use the development build.
- After the window is closed, the UI process and the textures it uploaded keep their memory until padmate exits.
- Tray exit can take up to 2 s longer.

## Alternatives given up

- **Electron/Node for everything.** Mapping would depend on the UI process, against "optional"; SDL and SendInput would need native modules.
- **Rust + Tauri, C#/WPF, Qt.** The script host would need another JS engine and a way to compile TS, Monaco would still need a web view, and the overlay's window behavior was verified on Electron.
- **The UI reads padmated's stderr, or the report goes to a fixed place, or it comes over IPC.** The packaged build has no stderr; a fixed place could hold the report of another start; the instance, configuration, log and SDL all start before IPC.
- **Exit the UI as soon as the pipe closes.** It cannot tell a tray exit from a crash.
- **Tray exit does not wait for the UI, or waits without a limit.** The first loses the last edit; the second hangs the tray when the UI hangs.
- **Keep the packaged build in dry-run and register `--inject` only for start at sign-in.** Trying it out and the mode after sign-in would differ.
- **Ask at startup whether to send keys.** One more step, when most people install it to send keys.
