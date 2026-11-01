# ADR-0017: Global settings and start at sign-in

[中文](0017-settings-and-startup.zh-CN.md)

Related: [ARCHITECTURE §10.1, §11](../ARCHITECTURE.md); [ADR: No stuck keys](0006-no-stuck-keys.md); [ADR: Processes and stack](0001-processes-and-stack.md)

## Context

- `timing` and `safety.panic` used to be read only at startup: changing them did nothing, and the UI showed values that differed from the running ones. The emergency combo is detected on T-input and was chosen once, when a device connected.
- Start at sign-in is written to Run under HKCU, whose command line cannot exceed 260 characters. An absolute exe path plus profile and log paths can reach 360. The registry does not validate, so a successful write does not prove that the next sign-in will start it.
- The daemon of the development UI has its own instance name and pipe, yet its tray still showed "Start at sign-in"; one click would overwrite the user's real start-at-sign-in value with the development build.

## Decision

### 1. Global settings take effect on save
- **Timing.** After `config.put` commits, the daemon compiles the running profile again with the new global values. If the four times have not changed, nothing happens; if they have, it goes through the reload of saving a profile: held keys are released and scripts are rebuilt, while layer state and the session are kept.
- **The emergency combo.** When `safety.panic` changes, the daemon picks the combo again on T-input from what it recorded for each device, and clears members already pressed and their timing. The new combo fires only when all its members are pressed again; an old combo being held does not fire. Detection is still on T-input, before events enter the channel.
- A combo needs at least 2 buttons held for at least 500 ms, or validation fails, so holding one button a little longer can never trigger the emergency pause by mistake. Combos apply per controller family; a controller missing a button keeps the default combo, so none is without an emergency pause. The daemon computes the combo in effect and returns it through `status.get`; the UI no longer picks it itself.
- While a new combo is being recorded on the controller, the UI pauses mapping through `capture.start`, so presses trigger no shortcuts. The capture belongs to the bridge connection, and every abnormal end (the window losing focus, closing, a disconnect) must end the capture. While recording, holding the old combo still toggles pause, as a safety exit.
- Ignored devices and `restoreLayerState` still need a restart. The feedback switches take effect at once ([Controller feedback](0015-controller-feedback.md)).
- config.json has a single path to commit, `config.put`, and the UI is its only writer.

### 2. Start at sign-in
- **The switch belongs to the daemon.** The tray menu, `startup.get` and `startup.set` all go to the same startup entry, serialized by a mutex. Clients cannot pass an exe or arguments: the command is always the daemon's own executable and startup arguments, or any authenticated client could make padmate start at sign-in with any arguments.
- HKCU Run is written only by an explicit click on the switch in the tray or on the Settings page.
- **Development and test builds never write the startup entry**: `startup.get` reports it unavailable, `startup.set` returns -32017, and the tray item is grayed out. These builds use an in-memory startup entry only, checked again before every read and write.
- **Long commands go through an argument file.** When the command exceeds 260 UTF-16 units, the full arguments are written to `%APPDATA%\padmate\startup\<id>.json`, and Run holds only `padmated.exe --startup-id=<SHA256>`. The file is immutable and named by its content hash, and Run is written only after the file; a failed Run write removes only the file this attempt created. The `--startup-id` entry accepts it only as the whole argv, and checks the digest, the version and the exe path; if any is wrong, it fails and exits, without falling back to injection or to the default profile. A command too long even for the reference fails clearly and leaves the existing Run alone.
- A GUI start at sign-in may have no stdout; the dry-run printer writes to `io.Discard`, and the mode is still dry-run. `--log-file -` falls back to the default persistent log.
- The packaged UI starts the daemon with `--inject`, so the registered start at sign-in uses `--inject` too; development builds, E2E and tests still use `--dry-run`.

## Costs

- Changing timing releases held keys and rebuilds scripts, the same as saving a profile. The foreground is usually a padmate window then, which gets no keys.
- A combo of 1 button, or shorter than 500 ms, in an existing config.json now fails validation and is handled as a broken file (repaired automatically).
- Argument files are not cleaned up when start at sign-in is turned off; if the user deletes a referenced file, the next start fails, and turning it on again recreates it.
- After moving the exe or a profile, start at sign-in must be turned on again. The digest is an integrity check, not security isolation against the same user.
- Real sign-out and sign-in can only be checked by hand.

## Alternatives given up

- **Settings that apply after a restart, or changing the global values without reloading the running profile.** Users could not try the effect.
- **`Repick` keeping members already pressed.** An old combo being held could fire right after the switch.
- **Choosing buttons in the dialog with a drop-down.** Users would have to know the buttons' names in SDL.
- **Letting development builds write the startup entry too, with only a warning.** The user's real start at sign-in would be replaced by the development build's dry-run mode.
- **Creating the startup entry only from the tray.** The Settings page and the tray could write at the same time, with the state in two places.
- **Truncating the arguments.** It would hide defects.
- **The combo's minimum only in the UI's recording.** A hand edit of config.json could still write 1 button, and the daemon would use it.
- **The packaged UI uses `--dry-run`, and start at sign-in registers `--inject`.** What users try would not be what they get after sign-in.
