# Architecture decision records (ADR)

[中文](README.zh-CN.md)

These records answer "why is padmate the way it is". They are for developers who want to change the design, or to understand where a constraint comes from.

How the documents divide the work:
- [ARCHITECTURE](../ARCHITECTURE.md): what the design is now, and where the code is.
- [PITFALLS](../PITFALLS.md): verified facts to know before changing a part of the code.
- ADR (this folder): why the design is this way, what was given up, and what it costs.

## Contents
| Record | Topic |
|---|---|
| [Processes and stack](0001-processes-and-stack.md) | A Go daemon plus an optional Electron UI; running in the background, the tray, single instance and forwarding; the UI's lifecycle and security bridge |
| [Controller input: SDL3](0002-input-sdl.md) | A bundled, pinned SDL3; the hint set; go-sdl3 and purego patches; Joy-Cons; receiver heartbeats |
| [Several controllers, hot-plug and slots](0003-devices-and-slots.md) | Up to four controllers mapped at once; hot-plug, reconnects and the queue |
| [Keyboard and mouse output](0004-output-sendinput.md) | SendInput scan codes and keyboard layouts, the injection tag, the menu mask key, key gaps, mouse output |
| [Engine, slot loop and runtime supervision](0005-engine-and-threads.md) | The pure engine, the slot loop, the T-output loop, runtime supervision, end-to-end latency |
| [No stuck keys](0006-no-stuck-keys.md) | Explicit dry-run/inject, pause and the emergency combo, suspending on system events, the held-keys log, offline emergency release |
| [Profile format](0007-profile-format.md) | JSON with a single writer, tagged unions and schemaVersion, JSON Schema validation, event JSON lines, hot reload |
| [Profile library, default profile and automatic switching](0008-profile-library.md) | The profile library, a fixed default profile, switching by app, autosave, undo and backups |
| [IPC](0009-ipc.md) | JSON-RPC over a named pipe, authentication, the handshake and bounded connections |
| [Scripting runtime](0010-scripting-runtime.md) | goja + esbuild, our own event loop, gestures and output shared with declarative mappings, API versions |
| [Scripts as macros](0011-script-macros.md) | A script is a macro that a button starts; the macro library; only the daemon writes the files |
| [Mapping model](0012-mapping-model.md) | One set of buttons per profile, a single function key, analog controls, sequences and inheritance, repeat, the mapping wizard, AntiMicroX import |
| [Editor UI](0013-editor-ui.md) | The controller picture and the button drawer, edit drafts and shortcut capture, autosave, the shortcut key pool, key symbols and icons |
| [Overlay window](0014-overlay.md) | A floating window that is created once, never takes focus and lets the mouse through |
| [Controller feedback](0015-controller-feedback.md) | Rumble, lights, haptic presets, battery and the global switches |
| [Explainable mapping and diagnostics](0016-diagnostics.md) | The event record and "why did nothing happen" |
| [Global settings and start at sign-in](0017-settings-and-startup.md) | Global settings take effect on save; the start-at-sign-in switch and long argument references |
| [Installation, updates and releases](0018-installer-and-updates.md) | A per-user NSIS wizard installer, the portable package, signing, automatic updates and the download site |

## When to change an ADR
Change one when you:
- change or add a row of [ARCHITECTURE §0 Decisions at a glance](../ARCHITECTURE.md#0-decisions-at-a-glance);
- choose a language, a key dependency, the threading model, an inter-process protocol or a file format;
- make a decision that affects the invariants in [CLAUDE.md](../../CLAUDE.md);
- reach the conclusion of a spike.

No change is needed to fix a bug, to change the inside of a package, or to implement a feature under a decision that exists. Those update ARCHITECTURE only.

## Rules
- Each record covers one topic and says only what is decided now. When a decision changes, edit that record in the same commit and update ARCHITECTURE with it; the old text stays in the git history, and no new record "supersedes" it.
- Only a new topic gets a new record. Its number follows the highest one and is never reused. The file is `NNNN-short-name.md` (English), with the Chinese version `NNNN-short-name.zh-CN.md` next to it; the title is `# ADR-NNNN: Topic`. Add a row to the contents table in both languages. Code comments refer to it as `ADR-NNNN`.
- Each record has the same parts: context, decision, costs, alternatives given up. No dates, no names, no account of acceptance.
- Write both languages at the same time, in short sentences. Every decision must be traceable to the code or to ARCHITECTURE.
