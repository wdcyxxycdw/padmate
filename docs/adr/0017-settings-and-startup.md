# ADR-0017: Settings and start at sign-in

[中文](0017-settings-and-startup.zh-CN.md)

Related: [ARCHITECTURE §10.1, §11](../ARCHITECTURE.md); [ADR: No stuck keys](0006-no-stuck-keys.md); [ADR: Processes and stack](0001-processes-and-stack.md)

## Context

- `timing` and the emergency combo used to be read only at startup: changing them did nothing, and the UI showed values that differed from the running ones. The emergency combo is detected on T-input and was chosen once, when a device connected.
- The function key, the feedback switches and the emergency combo suit one controller and not another: a Joy-Con has other buttons than a Pro Controller, and a controller behind a receiver may not rumble at all. A row for each in the global part of the Settings page would be a setting that is right for one controller and wrong for the next.
- Start at sign-in is written to Run under HKCU, whose command line cannot exceed 260 characters. An absolute exe path plus profile and log paths can reach 360. The registry does not validate, so a successful write does not prove that the next sign-in will start it.
- The daemon of the development UI has its own instance name and pipe, yet its tray still showed "Start at sign-in"; one click would overwrite the user's real start-at-sign-in value with the development build.

## Decision

### 1. Settings take effect on save
- **Timing.** After `config.put` commits, the daemon compiles the running profile again with the new global values. If the four times have not changed, nothing happens; if they have, it goes through the reload of saving a profile: held keys are released and scripts are rebuilt, while layer state and the session are kept.
- **The emergency combo.** When the combo of a controller changes, the daemon picks the combo again on T-input for that controller, from what it recorded for the device, and clears its members already pressed and their timing. The new combo fires only when all its members are pressed again; an old combo being held does not fire. A controller whose combo did not change is left alone, so a hold under way on it is not canceled. Detection is still on T-input, before events enter the channel.
- A combo needs at least 2 buttons held for at least 500 ms, or validation fails, so holding one button a little longer can never trigger the emergency pause by mistake. The combo is a setting of each controller; its default is that of its family, and a controller missing a button of its combo keeps the default combo, so none is without an emergency pause. The daemon computes the combo in effect and returns it through `status.get`; the UI no longer picks it itself.
- While a new combo is being recorded on the controller, the UI pauses mapping through `capture.start`, so presses trigger no shortcuts. The capture belongs to the bridge connection, and every abnormal end (the window losing focus, closing, a disconnect) must end the capture. While recording, holding the old combo still toggles pause, as a safety exit.
- Ignored devices and `restoreLayerState` still need a restart. The feedback switches take effect at once ([Controller feedback](0015-controller-feedback.md)).
- A change of one controller's function key switches only the slot that has that controller ([Mapping model](0012-mapping-model.md)); a change of its feedback switches sends the cues again on that controller only.
- config.json has a single path to commit, `config.put`, and the UI is its only writer.

### 2. The settings of each controller
- **What they are.** The function key, the four feedback switches and the emergency combo are members of `devices.<DeviceKey>` in config.json: `functionKey`, `feedback` and `panic`. A controller's value is its own member, else the retired top-level member where it applies (`functionKey` only to a controller with the standard layout, `feedback` to every controller, `safety.panic` by family), else the default of its family. The three top-level members are retired: the daemon still reads them as that fallback and nothing new is written to them. A value or a combo button that the controller's family cannot have is an error for a standard controller or a pair; for a single Joy-Con it is ignored with a warning and the default of its family applies, so an older file still loads and the rest of it applies.
- **Where they are.** The General section of the Settings page has a row, "Controllers", with a button that opens a view inside Settings, like the logs and the diagnostics. The view lists the controllers that have connected, those connected now first and the others in a fold, "Previously connected". Under each one are its function key (the choices of its family, the default marked), its four switches and its emergency pause (the keys and the hold time, and a button, "Change", whose dialog also has "Restore default"). Each change is saved at once, to that controller alone. The page no longer has a function key, feedback or emergency pause of its own. The set that the Mapping page edits for the held function key uses the function key of the selected controller.
- **The list.** The controllers that have connected are remembered by the daemon in state.json (`controllers`, at most 64), not in config.json, which holds settings only. `pads.list` returns them with the stored and the resolved settings and the choices of each family; `pads.set` writes one controller's members.
- **Recording a combo** needs the controller to be connected, since the keys are pressed on it; for a controller that is not connected the page shows the combo but not the button.
- **Folding the retired members.** Loading a file changes nothing, since the daemon never rewrites a user's file by itself. The first change on the Controllers page writes the retired top-level values into every controller the daemon knows (`pads.migrate`; only what differs from the defaults of the family is written, so each controller behaves as before), removes the retired members, and then saves the change. A change that alters nothing does not fold them.

### 3. Start at sign-in
- **The switch belongs to the daemon.** The tray menu, `startup.get` and `startup.set` all go to the same startup entry, serialized by a mutex. Clients cannot pass an exe or arguments: the command is always the daemon's own executable and startup arguments, or any authenticated client could make padmate start at sign-in with any arguments.
- HKCU Run is written only by an explicit click on the switch in the tray or on the Settings page.
- **Development and test builds never write the startup entry**: `startup.get` reports it unavailable, `startup.set` returns -32017, and the tray item is grayed out. These builds use an in-memory startup entry only, checked again before every read and write.
- **Long commands go through an argument file.** When the command exceeds 260 UTF-16 units, the full arguments are written to `%APPDATA%\padmate\startup\<id>.json`, and Run holds only `padmated.exe --startup-id=<SHA256>`. The file is immutable and named by its content hash, and Run is written only after the file; a failed Run write removes only the file this attempt created. The `--startup-id` entry accepts it only as the whole argv, and checks the digest, the version and the exe path; if any is wrong, it fails and exits, without falling back to injection or to the default profile. A command too long even for the reference fails clearly and leaves the existing Run alone.
- A GUI start at sign-in may have no stdout; the dry-run printer writes to `io.Discard`, and the mode is still dry-run. `--log-file -` falls back to the default persistent log.
- The packaged UI starts the daemon with `--inject`, so the registered start at sign-in uses `--inject` too; development builds, E2E and tests still use `--dry-run`.

## Costs

- The function key, feedback and emergency pause are one click further than a row of the Settings page; whoever has one controller sees one section.
- A controller that has not connected since the daemon began to remember them is not in the list when the retired values are folded; it takes the defaults of its family, and the retired values stop applying to it from then on.
- The emergency combo of a controller that is not connected can be neither recorded nor restored.
- Two identical controllers without a serial number have the same DeviceKey, and share one set of settings.
- Changing timing releases held keys and rebuilds scripts, the same as saving a profile. The foreground is usually a padmate window then, which gets no keys.
- A combo of 1 button, or shorter than 500 ms, in an existing config.json now fails validation and is handled as a broken file (repaired automatically).
- Argument files are not cleaned up when start at sign-in is turned off; if the user deletes a referenced file, the next start fails, and turning it on again recreates it.
- After moving the exe or a profile, start at sign-in must be turned on again. The digest is an integrity check, not security isolation against the same user.
- Real sign-out and sign-in can only be checked by hand.

## Alternatives given up

- **Keeping the global rows and adding a controller's own as an override.** The settings would be in two places, and a user could not tell which one applies.
- **Folding the retired members when the daemon loads the file.** It would rewrite the user's file by itself.
- **Settings that apply after a restart, or changing the global values without reloading the running profile.** Users could not try the effect.
- **`Repick` keeping members already pressed.** An old combo being held could fire right after the switch.
- **Choosing buttons in the dialog with a drop-down.** Users would have to know the buttons' names in SDL.
- **Letting development builds write the startup entry too, with only a warning.** The user's real start at sign-in would be replaced by the development build's dry-run mode.
- **Creating the startup entry only from the tray.** The Settings page and the tray could write at the same time, with the state in two places.
- **Truncating the arguments.** It would hide defects.
- **The combo's minimum only in the UI's recording.** A hand edit of config.json could still write 1 button, and the daemon would use it.
- **The packaged UI uses `--dry-run`, and start at sign-in registers `--inject`.** What users try would not be what they get after sign-in.
