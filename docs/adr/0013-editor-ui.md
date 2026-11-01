# ADR-0013: Editor UI

[中文](0013-editor-ui.zh-CN.md)

Related: [ARCHITECTURE §12](../ARCHITECTURE.md); [ADR: Mapping model](0012-mapping-model.md); [ADR: Processes and stack](0001-processes-and-stack.md); [ADR: Scripts as macros](0011-script-macros.md)

## Context

The editor is an Electron app (`app/`). Early on it showed too much, with internal words on the screen. Its users are people who paint, and they need to see at a glance what each button does. The direction is subtraction: the UI offers the common subset, and the format and the engine keep their capabilities ([Mapping model](0012-mapping-model.md)).

## Decision

### 1. Pages and settings
- The navigation has only Mapping, Profiles, Scripts and Settings. Logs and diagnostics are reached from "About" on the Settings page.
- Settings save as soon as they change. Each row of the Settings page has at most one sentence of explanation. Notices about conflicting software (Steam, DS4Windows, HidHide) can be dismissed; the "No controller" page cannot.

### 2. The controller picture
- It is a real 3D model under an open license (CC BY 4.0, attributed in `app/src/renderer/controller/assets/ATTRIBUTION.md`), seen from a fixed front view that cannot be rotated; the main page does not scroll.
- Each physical control has a leader line and a native HTML button, reachable by keyboard. A stick is two cells, one above the other: "rotation" and "press".
- The labels follow the buttons SDL reports for the connected controller (`buttonMask`, `axisMask`, `padHasControl`): a control the controller lacks gets none; a single Joy-Con held sideways, for example, has no d-pad, right stick, triggers or −. The extra buttons (Share, Capture, the mute button, the back buttons of an Elite or Edge, the Fn buttons of an Edge, a Joy-Con's SL/SR, or L/ZL when held sideways) sit in one row below the controller, the left hand's on the left and the right hand's on the right, the whole row between the two columns of labels. Until SDL reports the buttons, a controller gets the standard ones, plus the touchpad on a PS4 or PS5. There are only two shells: PlayStation controllers use the DualSense, all others borrow the Xbox.
- The CSP stays `connect-src 'none'`, so decoding the model uses no `fetch` and needs no network.
- The rendering stage stays alive for the app's session; coming back to the Mapping page only puts the canvas back. It is released only when the model changes, the context is lost or it fails, since most of the time goes into uploading textures.

### 3. The button drawer
- The action types are only "Shortcut" and "Script", and the mappings only tap, hold and double tap. Details are shown when opened.
- Deleting really deletes. A button never set before gets an empty "tap" first, which is not written to the file until something is recorded.
- A name belongs to a mapping. "Function" (the binding's `label`, or a stick's `analog.ls.label`) is off until the key has an action (or a block); an edit that leaves the key, or the stick, mapping nothing takes the name with it, and a binding with nothing else goes. A stick maps something when it moves the pointer, or when a key it sends in its mode (the directions, or a knob's ticks), a ring or a raw half axis has an action: exactly when its chip is not "Not set". The stick's "rotation" cell has no name of its own: without a `label` it is titled like a button, by its keys or what its action does, not by the mode ("Direction keys", "Knob").
- Keys are named as the controller prints them; there is no renaming. `controlNames`, the key names an earlier UI wrote, is retired like the feedback switches (ADR-0015) and like the members ADR-0008 retired from config.json: the daemon and `padmatectl validate` still accept it, with the same checks and no warning, but nothing reads it, and the UI drops it whenever it saves the profile (`savedForm`). It is a member of the profile, not a key, so dropping it changes no binding; a file only opened is not rewritten. An AntiMicroX import no longer writes it (the report says the names were dropped).
- A file may already have a name with no action (written by hand, or by an earlier version). It is not rewritten: the daemon warns ("binding has no action", which `padmatectl validate` prints) and the Mapping page now shows that warning instead of hiding it; the name goes the next time the user edits that key. Saving drops only what the edit touched, so the other keys stay byte for byte.
- When the file has something the UI does not offer, the action type is shown unselected or with a lock; saving other buttons leaves it byte for byte unchanged, and replacing it needs a confirmation.
- Profile settings hold the name, the bound apps, the default timing and the switches.

### 4. Changes save at once; undo lives in memory
- Every commit is validated at once, saved with a revision and takes effect; there is no save bar. A draft that the daemon refuses is not written; the UI shows the reason and "Retry". Changes not saved because the connection dropped stay in memory; after reconnecting they are saved again if the file has not changed, and otherwise the file wins.
- The draft is a whole profile, and saving replaces only the part being edited.
- Undo and redo live only in memory, at most 100 steps per profile; they are cleared on restart, and when the profile file is changed elsewhere. They do not use disk snapshots, because a snapshot is per save and does not match one edit.
- Closing the window and exiting from the tray never lose the last change: Main asks the renderer to commit and waits for the save; on tray exit, the daemon first pauses mapping and releases every key, then waits up to 2 s for the UI to confirm.

### 5. Recording shortcuts
- There are only "Record" and "Clear": no text entry and no F13–F24 picker.
- The keyboard is recorded by a short-lived helper, `padmatectl keyboard-capture`: it owns its own `capture.start` connection, installs an LL observing hook on a dedicated thread, swallows no physical key, and releases by closing the connection when it exits.
- Mouse buttons and the wheel are watched by the window itself and cannot be seen outside it.
- Recording ends after one key (with the modifiers held). A single press of Esc records Esc; holding it for 1 second cancels.

### 6. The shortcut sheet
padmate only sends shortcuts; users have to assign the functions to those keys in their painting app. The "More" menu exports a Markdown sheet to help. The UI does not pick keys for the user, since every candidate key has known conflicts; and there are no starter templates: a new profile is blank.

### 7. Press preview and labels
While the window is in the foreground, a press that runs an action shows one line at the bottom of the Mapping page. Labels have two display modes only: "Action name" and "Shortcut".

### 8. Button symbols and icons
The names of the controller's buttons have one source: `controllerLabels`. Go's `internal/padlabel` follows it, and a test compares the two tables; a name that the controller reports through SDL comes first.

We do not draw the icons ourselves; we use two ready-made vector icon sets under open licenses: Kenney Input Prompts (CC0; three sets, Xbox, PlayStation and Switch, shaped and printed like the real controllers) and PromptFont (OFL 1.1; only for what Kenney lacks, the stick rotation and "any direction"). `pad-icon.tsx` picks an icon by the print that `controllerLabels` gives and by the controller family: face buttons, the d-pad, stick presses and directions use the set of the family, and other controllers use Xbox's; the stick's "rotation" cell and the knob ticks use PromptFont's rotation; the PS button is drawn as Home's house, not as a vendor logo; a single Joy-Con held sideways is drawn by position, not by the letter SDL reports. What neither set has (diagonals, push stages, Capture, extra buttons and so on) keeps the old badge. The same icons are used in the controller picture's labels, the drawer's title, the press preview, the Settings page, the Diagnostics page and the mapping wizard (the wizard asks about SDL's standard layout and uses the Xbox set). An icon's black and white take the text color and its highlight the accent color, following the UI's colors; in forced-colors mode two system colors are kept, or the d-pad would not show its direction.

Where the assets come from and how they were prepared is in `app/src/renderer/controller/pad-icons/README.md`, and the license texts sit with the files; "Settings → About and licenses" lists both sets with the full OFL text (the OFL requires it to come with copies). The cost: about 100 KB more path data in the renderer; at small sizes PlayStation's SELECT, OPTIONS and similar words are recognized by their shapes.

### 9. The Profiles page and picking apps
- The management list is ordered by creation time (the NTFS creation time), with the fixed "Default" profile first. Not by modification time, which changes on every save. Pick lists are ordered by name, with the locale fixed to zh-CN.
- A single profile exports as `.padmate` (a zip with the scripts it uses). The daemon creates and checks the zip, with every file operation through `os.Root`; Main shows the dialog and carries the bytes, and its error messages carry no path; the renderer passes only a profile ID and a token. On a name clash it asks "Replace" or "Save as / Keep both", with the default focus on the choice that does not overwrite.
- An app can be added three ways: by clicking its window, by choosing from the installed apps, or by browsing; a name cannot be typed in. Rules match by file name and read neither window titles nor an app's internal state.

### 10. Automatic repair of the settings file
When config.json has errors, it is first backed up as `config.broken-<time>.json` (an existing backup is never overwritten), then a repaired file is written: valid members are kept, each error resets only the smallest member that contains it, and only a file that cannot be parsed at all is replaced by the defaults as a whole. The UI says so in a dialog. When the backup or the write fails, the repair stays in memory only. A broken state.json is reset silently.

### 11. Accessibility
- axe-core checks automatically in Vitest and in E2E on real Electron; exceptions are in `app/tests/a11y/exceptions.json`, each with a reason.
- Contrast is guaranteed by pure calculation tests that read `theme.css`; in forced-colors mode, every state must look different.
- Focus does not leave a dialog; Esc closes it and returns to the control that opened it.

### 12. The UI and the user documents use the same names
UI text that a document quotes in “…” must be in the name table of `app/tests/docs/names.ts`, and every entry of the table must be a Chinese and English pair next to each other in the UI's source. Changing UI text means changing the table and the documents with it.

## Costs

- Three.js and two GLBs (about 14 MB) make the build larger, and the stage that stays alive holds its GPU memory.
- With changes saved at once, there is no "discard"; a mistaken edit relies on Ctrl+Z, and undo does not survive a restart.
- When the controller's state is unknown, a Ctrl+Z that the controller sends to padmate's window does not undo.
- After a broken settings file is repaired, the settings it changed can be recovered only from the backup.

## Alternatives given up
- **Drawing the button icons ourselves.** They did not look like real controllers and were not standard; we use ready-made icon sets instead.
- **A single icon set.** Kenney has no stick rotation, and PromptFont does not tell controller brands apart, so the two are combined.
- **Loading PromptFont as a font, or showing the SVGs with `<img>` or a CSS mask.** A font means shipping the whole file for six glyphs; `<img>` cannot take the surrounding color, and a mask is a single color. Instead the paths are parsed and drawn with React.
- **An original geometric controller, or a 3D view that can rotate.** The first look was not accepted; the second is less clear than a fixed front view with a leader line per button.
- **A save bar with an autosave switch.** The switch only adds a state.
- **Keeping the Mapping page alive and only hiding it.** Shortcut listeners, focus and the accessibility tree would all stay; only the renderer layer is kept.
- **Several renderers sharing textures, or caching only the decoded result.** WebGL resources cannot cross contexts, and decoding takes only on the order of 1 ms.
- **A key pool that assigns shortcuts, starter templates.** Every key in the pool has conflicts; a template is no different from a blank profile.
- **Replacing a broken settings file with defaults as a whole, or only telling the user to fix it.** One misspelled field would lose every setting, and users cannot be asked to edit the configuration file.
- **Recording the creation time in profile.json.** The schema would change, while the file system has it already.
- **Main reads and writes the profile folders directly.** The path checks would have to exist in both languages.
- **Dropping every name without a mapping on save.** Keys the user did not touch would change; the warning says the same without rewriting them.
