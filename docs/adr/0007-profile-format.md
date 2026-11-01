# ADR-0007: Profile format

[中文](0007-profile-format.zh-CN.md)

Related: [ARCHITECTURE §10](../ARCHITECTURE.md#10-formats), [ADR: IPC](0009-ipc.md)

## Context

A profile describes every mapping of one controller; config.json holds the rules for automatic switching and the global settings. The user (by hand), the daemon (hot reload on save), and the UI and CLI (over IPC) all read and write them. The requirements: completion and validation when written by hand, with errors pointing at a line and column; files that can be copied and shared as they are; no edit lost when several parties change them, and no half-written file ever read; one structure shared by Go, JSON Schema, the UI and the TS types; a format that cannot change at will once public; and recorded traces that are kept for a long time as regression tests.

## Decision

1. **Strict JSON, and the daemon is the only writer.** No comments; there are JSON Schemas (profile, config and state, under `schema/`). The UI and the CLI write through `profiles.put` and `config.put` with a revision; the daemon writes a temporary file and renames it, and keeps the old version before it replaces a profile.json. Editing the files directly is still supported: the watcher notices and reloads. Volatile state goes in `state.json`; it is not user configuration.
2. **Actions are tagged unions**, told apart by `type`. `schema/profile.schema.json` is the one source; the Go and TS types are generated from it.
3. **A profile's `schemaVersion` is frozen at 1.** A valid profile stays valid. A new optional field does not change the version; a change that could break existing files needs a migration and a new version. Version 0 counts as 1; the migration happens in memory and keeps the length, so diagnostic positions stay where the user sees them, and a file on disk is never rewritten for being old. A file with a higher version is refused. config.json and state.json are not frozen, since they are not shared between users; deprecated fields are still loaded and ignored. `schemaVersion`, the IPC `protocol` and `script.api` are three independent version numbers.
4. **Validation in three layers: syntax → schema → semantics.** A layer does not run when the one before it has errors.
   - The schema is draft 2020-12 and uses only keywords that VS Code and Monaco support. It is closed everywhere: an unknown field is an error, not silently ignored. Validation uses the pure-Go jsonschema/v6, and padmate rewrites its messages.
   - The syntax layer is our own strict scanner. It builds an index from JSON Pointer to byte offset, and it refuses duplicate keys and invalid UTF-8, which `encoding/json` accepts.
   - The schema in `internal/profile` is a generated copy of the one at the root (`go:embed` cannot reach outside the module); only the root one is edited.
5. **Event JSON lines.** Recorded traces and fake input use one format (the output of `padmatectl monitor --json`). Reading is strict, and errors give the line number. A trace records one device. The format will only gain optional fields, and old traces must always remain readable; this is why control names stay stable.
6. **Hot reload.**
   - `internal/watch` polls stat every 100 ms, reads a file only once it has been stable for 200 ms, and decides by content hash whether it really changed.
   - With errors, nothing is swapped: the old mapping keeps working and no key is released.
   - A reload is one ReleaseAll: as each slot takes the new program, that slot's keys and any tap in progress are released. Held controls stay canceled; the new binding applies after release and a new press. A reload never presses a key.
   - A change to `script.params` alone applies in place, without a release.

## Costs

- No comments; the UI can save only while the daemon runs; editing in an editor and in the UI at the same time gives revision conflicts.
- The format is hard to change once public, and the trace format is a compatibility promise as well.
- Every action needs a `type`, which makes hand-written files wordier.
- Our own scanner has to stay in step with `encoding/json`, which tests and fuzzing check.
- A trace's info carries the serial, and the serial of a Bluetooth controller is usually its Bluetooth address; check before committing one.
- A save takes about 200–400 ms to take effect.

## Alternatives given up

- **SQLite, YAML.** The first cannot be written by hand or diffed; the second is error-prone with indentation and implicit types.
- **JSONC / JSON5.** The daemon serializes again when it saves, and the comments would be lost.
- **The UI writes the files directly.** Every writer would need its own atomic writes and conflict detection.
- **Inferring the type from the fields without a tag, or allowing string shorthands.** Validation errors would be vague; two ways to write one thing means deciding which to write back on save.
- **Freezing at 1 from the very start, or no version field.** The first carries the burden of migrations before there are public users; with the second, old and new files cannot be told apart once public.
- **Serializing the JSON again when migrating.** Formatting, BOM and line ends would change, and diagnostic positions would no longer match.
- **An open schema, with unknown fields only warned about.** An old daemon would silently ignore new features.
- **Another format for recordings, lenient reading, or several devices in one trace.** Lenient reading lets a misspelled field through silently; times of different devices cannot be compared directly.
- **ReadDirectoryChangesW or fsnotify.** A save by rename needs the events pieced together; the cost of polling stat is negligible.
- **Detecting changes by modification time only.** A touch would reload as well and release held keys for nothing.
- **Pressing the new key for held controls on reload.** It invites surprises and adds a path to stuck keys.
