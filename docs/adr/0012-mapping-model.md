# ADR-0012: Mapping model

[中文](0012-mapping-model.zh-CN.md)

Related: [ARCHITECTURE §7, §10.3](../ARCHITECTURE.md); [ADR: Profile format](0007-profile-format.md); [ADR: Editor UI](0013-editor-ui.md)

## Context

The engine and the profile format support layers, many gestures, sequences, inner and outer stick rings, knobs, two-stage triggers and inheritance. Users treat the controller as a shortcut box for painting: "press this button, send that shortcut". So there are two levels. The engine and the file format keep their capabilities, and since the format is frozen it only grows; the UI offers a small subset. What a file has outside the subset still works, and the UI opens and saves it without errors or lost fields; opening it without changes never rewrites the file.

The function key is not the same for every controller. A single Joy-Con has no View or Menu, and held sideways it has large ZL and ZR keys that are easy to press. So the function key, with the feedback switches and the emergency combo, is a setting of each controller and is not on the global side of the Settings page; the page lists the controllers that have connected and puts these settings under each ([ADR: Settings and start at sign-in](0017-settings-and-startup.md)).

## Decision

### 1. A profile has one set of buttons
The UI edits only `layers.base`, and the UI's wording never mentions "layers". The layer engine, switching layers over IPC/CLI and the script API stay as they are. When a file has other layers, common, layer actions or block, the UI shows them as usual but they cannot be edited; replacing one with another action needs a confirmation. When `defaultLayer` is not base, the whole profile is read-only. For "hold to get another set of keys", use the function key, which adds no latency.

### 2. A single function key, chosen for each controller
- A controller has one function key at a time, like a laptop's Fn, and it is the same for every profile. It is a setting of the controller, `devices.<DeviceKey>.functionKey` in config.json, not a global one: controllers do not have the same buttons, and which key is easy to press depends on how the controller is held. While it is held, the other controls first look in the profile's `functionSet`, and use their usual bindings where it sets none.
- What a controller may choose depends on its family (`internal/padfamily`); the first is the default:

  | Family | Function key |
  |---|---|
  | standard layout (Xbox, PlayStation, Switch Pro, unknown) | `start` (Menu, Options, +), `back` (View, Create, −) |
  | single Joy-Con held sideways, left | `paddle4` (ZL), `paddle2` (L) |
  | single Joy-Con held sideways, right | `paddle3` (ZR), `paddle1` (R) |
  | single Joy-Con held upright, left | `back` (−) only |
  | single Joy-Con held upright, right | `start` (+) only |
  | a pair of Joy-Cons (Switch 1 and Joy-Con 2) | `back` (−), `start` (+) |

  A lone Joy-Con held sideways defaults to ZL or ZR, the larger keys that are easy to hit, with L or R as the other choice. A pair defaults to − with + as the other choice. A Joy-Con held upright has only one such button, so it has no choice. `guide` is never a choice.
- A controller's function key is its own value, else the retired top-level `functionKey` (only for a standard-layout controller; it never moves a Joy-Con off its own default), else the default of its family. A value that the running family does not allow (the grip changed) is skipped and the default applies.
- Which set applies is decided at the press (like a layer's press owner).
- The function key's own edges never enter gesture recognition, so it adds latency to no key. It still takes part in the emergency combo.
- It has no feedback, is not stored in state.json and does not appear in layer events. The function key itself cannot be bound; a binding written for it is ignored when compiling.
- If a controller cannot report the current function key, it has no function key.
- Each slot runs the variant of the compiled profile that has its own controller's function key; the profile is compiled once. When a slot's key changes (the setting, a pair forming or breaking, another controller taking the slot), that slot reloads the same profile and releases every output it holds first. Other slots are not touched, and a key that has not changed is left alone, so a held function key stays held.

### 2a. The Xbox button is not used
The Xbox button (the PS button, Home; SDL's `guide`) is neither the function key nor mappable. Windows' Game Bar, Steam (Big Picture mode, and Alt+Tab when pressed with Menu) and the controller's firmware (turning off on a long press, resetting with a combination) all act on it; padmate can stop none of them, and does not change system settings.
- Its input is read as usual: the Diagnostics page and `padmatectl monitor` see it. In the engine its edges only update the physical state: they start no cycle, interrupt no pending gesture, and reach neither the lookup nor a script (`control.Control.Unmappable`); Compile takes none of its bindings.
- A binding on `guide` in a profile (in any layer, in common, in `functionSet` or in the old `functionKeys`) is parsed as usual, then taken out of the model with a warning for each; its own errors are lowered to warnings too. The format is frozen, so a valid profile stays valid, and `padmatectl validate` reports no error.
- The UI does not show it: the controller picture has no tag for it, and the key list, the shortcut table and the mapping wizard leave it out. When the UI saves a profile it drops the bindings on `guide`, as it does the retired feedback switches: what the daemon does not read goes at the next save, and a file only opened is not rewritten.
- `functionKey: "guide"` in the retired top-level member of config.json still passes the schema, and the daemon reads it as `start`. The UI drops it from every config.json it writes (like the members ADR-0008 retired, `withoutRetired`), so the file is rewritten at the next change of a setting. It is not rewritten when it is read, since the daemon never changes a user's file by itself.
- An AntiMicroX import drops what is on `guide`, and says so in the report.
- Scripts cannot use it either: `on`, `global().on`, `observe` and `claim` on `guide`, and a parameter of kind `control` set to it, are a TypeError, as an unknown control is; `pad.isDown`, `pad.heldFor` and `pad.has` read it as up and absent. The `Button` type of `padmate.d.ts` has no `"guide"`. The script API version is not raised: the change applies to every supported API at once (a host that let 0.17 scripts handle `guide` would map it), and ADR-0010's rule that a new version only adds members is about additions; a script that named `guide` fails to load with a message that says why.
- The emergency combo cannot have it. A combo with `guide`, in a controller's `panic` or in the retired `safety.panic`, still loads, with a warning on that button; the controller keeps the built-in combo of its family, so the emergency pause keeps working, and padmated logs it. The combo dialog does not record `guide`, and the UI drops such a combo from every config.json it writes (`withoutRetired`).

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
A controller SDL does not know is treated as a plain joystick. "Raw controls" are not added to the control model: in the wizard the user makes an SDL mapping, after which it is a standard gamepad. The wizard does not ask for Home (SDL's `guide`), since padmate does not use it. Raw input is used only in the wizard and in `padmatectl monitor --raw`; it never enters slots, the engine or emergency-combo detection. During the wizard, that controller's mapping is paused (`capture.start`). The mapping is written to config.json under `devices.<DeviceKey>.mapping`, for every DeviceKey of the same GUID at once, since SDL applies it by GUID. On `GAMEPAD_REMAPPED` the slot is fully reset, because SDL's remapping does not release held keys.

### 8. Importing AntiMicroX profiles
The pure package `internal/amgp` parses `.amgp`; its output always passes `profile.Parse`, and it comes with a report (dropped, approximated, information). The daemon accepts content only, never a path. We read only AntiMicroX's file format and behavior, not its code.

## Costs

- For a stick with no mode written, the UI shows "4 directions" until its first edit, while the engine works in `standard`.
- The single-set UI does not match AntiMicroX's sets; the other layers of an import can be seen in full only in the file.
- "Tap" has to wait to tell gestures apart when there is also a hold or a double tap.
- A controller has at most two function keys to choose from, an upright Joy-Con only one; what was set on the Xbox button no longer works.
- Changing a controller's function key releases the keys that controller holds at that moment, as switching profiles does.

### To be confirmed by the owner
- A single Joy-Con held upright has only one function key: − on the left, + on the right.
- A controller that has not connected since per-controller settings began is folded with the defaults of its family at the first change on the Controllers page; until then the retired global values keep applying.
- The emergency combo of a controller that is not connected can be neither recorded nor reset until it connects again.
- Two identical controllers without a serial number share the settings of the first one, the emergency combo included.
- A controller mapped in the wizard does not report Home, so the Diagnostics page does not see it either.
- When a parent profile changes, its children behave differently, and the UI does not show the inheritance.

## Alternatives given up

- **Any button as a modifier, or chords pressed together.** Every press of the buttons involved would have to wait longer; one fixed function key is simpler.
- **A function key built from real layers, or several function keys.** Layers have switch pulses, colors and layer names in the HUD, none of which the function key wants; several function keys would use up View and Menu.
- **One function key for every controller, in the global settings.** A Joy-Con has no View or Menu, a sideways Joy-Con has larger keys, and a choice that suits one controller does not suit another.
- **The Xbox button as a choice of function key, with a dialog on the Settings page asking the user to turn off Game Bar.** This was the earlier decision. Game Bar is only one of the conflicts; the user cannot turn off Steam's or the firmware's.
- **padmate turns off Game Bar itself.** That breaks "do not change global system settings".
- **`functionKey: "guide"` in config.json as a schema error.** An old file would not load, and the repair would reset the whole member; reading it as `start` has the same effect with no error.
- **Mutually exclusive trigger stages, or a curve that moves the thresholds.** The first delays the first stage; the second puts the rings and the two trigger stages out of line.
- **"Raw controls" in the control model.** The schema, the types, the editor and the script declarations would all change, and users would have to work out what each button is themselves.
- **Writing mappings to `SDL_GAMECONTROLLERCONFIG`.** It takes the highest priority, cannot change while running, and would cover the user's own settings.
- **"Press" and "release" steps in sequences.** An interruption would need its own guarantee of the release; whoever needs that can write a script.
- **Typing text by simulating the keyboard.** Chinese and characters not on the layout could not be typed, and the input method would intercept it.
- **Several parents, or refusing to delete a profile that others inherit from.** The first is complex; the second makes users edit the children one by one first.
- **Changing the engine's default mode to `fourWay`.** The format is frozen, and it would change how existing users' profiles behave.
