# ADR-0008: Profile library, default profile and switching by app

[中文](0008-profile-library.zh-CN.md)

Related: [ARCHITECTURE §9](../ARCHITECTURE.md#9-foreground-detection), [§10.1](../ARCHITECTURE.md#101-disk-layout); [ADR: Profile format](0007-profile-format.md)

## Context

- People use one profile per app. An app with no binding should have exactly one outcome.
- Foreground changes come from Win32 callbacks; disk reads and profile switches cannot happen on the message thread or on a slot.
- Layer state should come back after a restart, without breaking the boundary at which every key is released.
- When a hand edit breaks config.json, the user should not have to fix the file.

## Decision

1. **The profile library.** `profiles/<id>/profile.json` under the data folder, one folder per profile (scripts included). A library ID is restricted lowercase ASCII; the display name can be any Unicode. Every read goes through `os.Root` and refuses links that lead out.
2. **Layer state is kept per device.** `state.json` stores the selected layers by full DeviceKey and library ID; another kind of connection counts as another device. Held keys, latches and timers are never written to disk; restoring brings back the selection and generates no key. It is flushed on a timer in the background; on exit it is written one last time only after the keys are released, so a slow disk cannot delay the release.
3. **Automatic switching follows the rules only.** When the foreground app matches the first enabled rule, that profile is used; otherwise `default` is. Matching is by program file name (or full path); window titles are not read. While a padmate window is in the foreground, the current profile stays.
4. **Foreground detection neither blocks nor guesses.** The hook only posts a message, and the process path is looked up elsewhere; when the lookup fails, the last valid foreground is kept, and a stale result never overwrites a newer one. The user's choice, automatic selection and hot reload are serialized.
5. **Switching profiles always releases the old owner** and quarantines controls still held; `releaseOnSwitch:"never"` only turns off the foreground release within the same profile. The old script is stopped first, and a new script that fails does not bring the old one back.
6. **An explicit `--profile-id` decides the starting profile only.** With automatic selection on, the next evaluation of the foreground follows the rules again.
7. **Window capture** uses `app.foreground` and `apps.recent`.
8. **A fixed default profile.** `default` is a reserved ID, used for every app without a binding. If it is missing at startup, an empty profile that maps nothing is created; if it is deleted or cannot be read while running, the built-in empty profile is used and a warning is logged. It cannot be deleted or renamed; otherwise it is an ordinary profile. To make an app map nothing, bind it to a blank profile.
9. **Shipped profiles are offered once.** The release daemon carries a few finished profiles (now "CSP", id `csp`, for CLIP STUDIO PAINT). At a live start it offers each one the data folder has not been offered yet: it is added only when the library has no profile of that ID or name, and then a rule for its app is appended to config.json unless config.json has a rule for that app already. Either way the ID is written to `bundled.json` in the data folder, so a shipped profile, or its rule, that the user deletes never comes back, and the user's own profile of the same ID or name is never touched. Development and test builds, and replays, offer nothing, so their libraries stay as their tests make them.
10. **The daemon performs every library operation**; the UI's Main touches only the one file the user picked.
   - Deleting cannot be undone. A profile in use or referred to cannot be deleted; before a profile that others inherit from is deleted, its children are expanded into their full content.
   - Exporting and importing one profile: a `.padmate` zip. Import checks the whole content before anything is written and accepts scripts, JSON and text only; no RPC takes a path. On a name clash the client chooses "Replace" or "Save as". An imported profile cannot carry `extends`.
   - Before each replacement of a profile.json, a history snapshot is kept, as a way back through the CLI only.
11. **A broken config.json is repaired automatically.** The original is first backed up as `config.broken-<time>.json` (an existing backup is never overwritten), then a repaired version is written: valid members are kept, and each error resets only the smallest member that contains it; only a file that cannot be parsed at all is replaced by the defaults as a whole. The UI shows a dialog with the backup's name. A config.json that cannot be read (a folder, access denied) is still a startup error. A broken state.json is reset silently.

## Costs

- Different programs with the same name share one rule; telling them apart needs the full path.
- An app with no binding no longer "keeps the current profile". A rule changed in padmate applies only once the user switches to another app.
- In development, `--profile-id X --dry-run` with automatic selection on switches to the default profile at once; to keep it, write `"enabled":false` in config.json.
- Deleting cannot be undone; the confirmation dialog is the safety net.
- Settings that the repair changed can be recovered only from the backup file.
- Imported scripts get no trust confirmation.
- Layer state is remembered by full DeviceKey, so the same controller on another connection needs its layers set again.

## Alternatives given up

- **A rule table edited in order (AntiMicroX).** The use only needs one to one.
- **Keeping `onUnmatched`, a do-not-switch list and disabling by program.** Too much control flow.
- **Shipping the profiles as files of the installer, or offering them at every start.** A file next to the program needs a place every start (the window, the sign-in entry) finds alike, and a dev build would pick up the repository's; offering at every start brings back what the user deleted.
- **A default profile that can be deleted or renamed, or that is a separate file.** Deleting it brings back the special case of "disabled"; a separate file would need its own editing, scripts and history.
- **Treating padmate's windows like any program.** Editing would switch to another profile.
- **Switching settings that apply after a restart.** Users would think the binding did not work.
- **Main reads and writes the profile folders directly.** The checks would have to exist in both languages.
- **Undo from disk snapshots.** Snapshots count saves and do not match single edits; undo lives in the UI's memory.
- **For a broken config.json, only a notice, or replacing it all with defaults.** The first makes the user edit the file; the second loses every setting for one misspelled field.
