# ADR-0012: Mapping model

[中文](0012-mapping-model.zh-CN.md)

Related: MAP-01 to MAP-05, MAP-22 to MAP-27, ANA-03, ANA-04, ANA-09 to ANA-15, IN-14, IN-18, IN-19, PRO-19, PRO-21; [ARCHITECTURE §7, §10.3](../ARCHITECTURE.md); [ADR: Profile format](0007-profile-format.md); [ADR: Editor UI](0013-editor-ui.md)

## Context

The engine and the profile format support layers, many gestures, sequences, inner and outer stick rings, knobs, two-stage triggers and inheritance. Users treat the controller as a shortcut box for painting: "press this button, send that shortcut". So there are two levels. The engine and the file format keep their capabilities, and since the format is frozen it only grows; the UI offers a small subset. What a file has outside the subset still works, and the UI opens and saves it without errors or lost fields; opening it without changes never rewrites the file.

## Decision

### 1. A profile has one set of buttons
The UI edits only `layers.base`, and the UI's wording never mentions "layers". The layer engine, switching layers over IPC/CLI and the script API stay as they are. When a file has other layers, common, layer actions or block, the UI shows them as usual but they cannot be edited; replacing one with another action needs a confirmation. When `defaultLayer` is not base, the whole profile is read-only. For "hold to get another set of keys", use the function key, which adds no latency.

### 2. A single function key
- One for everything, not per profile: one of `back`, `start` and `guide`, `start` by default (`functionKey` in config.json). While it is held, the other controls first look in the profile's `functionSet`, and use their usual bindings where it sets none.
- Which set applies is decided at the press (like a layer's press owner).
- The function key's own edges never enter gesture recognition, so it adds latency to no key. It still takes part in the emergency combo.
- It has no feedback, is not stored in state.json and does not appear in layer events. The function key itself cannot be bound; a binding written for it is ignored when compiling.
- If a controller cannot report the current function key, it has no function key.
- Changing the function key applies on save and goes through the path of switching profiles, releasing every held output first.
- With the Xbox button chosen, Game Bar responds too. padmate does not change system settings; a dialog on the Settings page explains this and offers a button that opens the setting.

### 3. Sticks, triggers and per-device calibration
- Ten new virtual controls: inner ring, outer ring, the knob's clockwise and counter-clockwise ticks, and the trigger's second stage. They add optional fields only; `schemaVersion` stays 1 and the script API version is not raised. The threshold fields are in the schema.
- The inner and outer rings stack with the directions, and the outer ring has hysteresis. The knob replaces the directions; a tick waits 4 ms before reading the angle, so two axes arriving apart do not make one tick too many. The two trigger stages stack rather than exclude each other.
- The curve of rate-based repeat changes only the rate; it does not move the thresholds.
- The dead zone and the outer edge belong to the controller, not the profile: they are stored in config.json under `devices.<DeviceKey>.calibration` and override the profile's settings of the same name. Changing the calibration does no `CancelAll`; directions that no longer hold are released by the normal path.
- On the controller picture, a stick is two cells, "rotation" and "press". For direction modes the UI offers only "4 directions", "Knob" and "Mouse"; when the file has another mode, none is selected, the mode is kept on edits, and only choosing one replaces it. A stick push can have a name, written in `analog.ls.label` (and likewise `rs`). It is for display in the UI only and the engine does not read it; it is not a setting of the stick, so copy, paste and restoring the defaults leave it out. The knob's two ticks fire one at a time, so in the UI each cell records just one shortcut, with no gestures, sequences or scripts. The d-pad has no settings in the UI: up, down, left and right are four ordinary buttons; an `analog.dpad` mode in the file still works but cannot be changed in the UI. The engine's default mode is still `standard`.

### 4. Mappings and actions
- The UI offers three mappings: tap, hold and double tap; and two actions: a shortcut and a script.
- The UI picks how "tap" is written: `press` when a button has only a tap, `tap` when it also has a hold or a double tap, which waits until the gesture is clear.
- Shortcuts written by the UI are always held while the button is held and never repeat. The engine keeps the other actions (triple tap, typing text, precision mode, repeat, switching profiles and so on); written in a file they work as usual, and the UI shows them with a lock. Switching profiles is left to automatic switching by foreground app.
- Any mapping can be deleted, the last one included.

### 5. Sequences, text and media keys
- The `sequence` action has three kinds of step: tap, wait and text; 1 to 64 steps, taps only and no holds, so it never leaves a key held. All sequences go into one first-in-first-out queue (at most 16); otherwise two quick presses would interleave two texts. On cancel, steps not yet sent are dropped.
- The `text` action types unit by unit with `KEYEVENTF_UNICODE`, regardless of layout, bypassing the input method.
- KeySpec has seven more media keys, sent by VK regardless of layout. The script versions are `key.sequence` and `key.type` ([Scripting runtime](0010-scripting-runtime.md)).

### 6. Profile inheritance
`extends` works only within the profile library, at most 4 levels deep; a cycle, or a parent with errors, keeps the child from loading. Merging overrides by control, by member or by item; `script` is replaced as a whole. When a profile that others inherit from is deleted, its children are first written out with their merged full content. The UI offers no inheritance; the daemon accepts `extends` written in a file as usual.

### 7. The mapping wizard
A controller SDL does not know is treated as a plain joystick. "Raw controls" are not added to the control model: in the wizard the user makes an SDL mapping, after which it is a standard gamepad. Raw input is used only in the wizard and in `padmatectl monitor --raw`; it never enters slots, the engine or emergency-combo detection. During the wizard, that controller's mapping is paused (`capture.start`). The mapping is written to config.json under `devices.<DeviceKey>.mapping`, for every DeviceKey of the same GUID at once, since SDL applies it by GUID. On `GAMEPAD_REMAPPED` the slot is fully reset, because SDL's remapping does not release held keys.

### 8. Importing AntiMicroX profiles
The pure package `internal/amgp` parses `.amgp`; its output always passes `profile.Parse`, and it comes with a report (dropped, approximated, information). The daemon accepts content only, never a path. We read only AntiMicroX's file format and behavior, not its code.

## Costs

- For a stick with no mode written, the UI shows "4 directions" until its first edit, while the engine works in `standard`.
- The single-set UI does not match AntiMicroX's sets; the other layers of an import can be seen in full only in the file.
- "Tap" has to wait to tell gestures apart when there is also a hold or a double tap.
- People who choose the Xbox button have to turn off Game Bar's switch themselves.
- When a parent profile changes, its children behave differently, and the UI does not show the inheritance.

## Alternatives given up

- **Any button as a modifier, or chords pressed together.** Every press of the buttons involved would have to wait longer; one fixed function key is simpler.
- **A function key built from real layers, or several function keys.** Layers have switch pulses, colors and layer names in the HUD, none of which the function key wants; several function keys would use up View and Menu.
- **padmate turns off Game Bar itself.** That breaks "do not change global system settings".
- **Mutually exclusive trigger stages, or a curve that moves the thresholds.** The first delays the first stage; the second puts the rings and the two trigger stages out of line.
- **"Raw controls" in the control model.** The schema, the types, the editor and the script declarations would all change, and users would have to work out what each button is themselves.
- **Writing mappings to `SDL_GAMECONTROLLERCONFIG`.** It takes the highest priority, cannot change while running, and would cover the user's own settings.
- **"Press" and "release" steps in sequences.** An interruption would need its own guarantee of the release; whoever needs that can write a script.
- **Typing text by simulating the keyboard.** Chinese and characters not on the layout could not be typed, and the input method would intercept it.
- **Several parents, or refusing to delete a profile that others inherit from.** The first is complex; the second makes users edit the children one by one first.
- **Changing the engine's default mode to `fourWay`.** The format is frozen, and it would change how existing users' profiles behave.
