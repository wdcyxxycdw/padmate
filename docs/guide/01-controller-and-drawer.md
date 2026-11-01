# The controller picture and the key drawer

[中文](01-controller-and-drawer.zh-CN.md)

The “Mapping” page is the main page of padmate.

## The controller picture

The middle of the page shows a front-facing 3D controller. Each key has a tag beside it.

- The tag says what the key does. A key that is not set yet says “Not set”, and a key handled by a script says “Handled by a script”. See [Scripts](08-scripts.md).
- Click a tag to open the settings of that key.
- The tag of a stick has two cells: “Move” above for pushing the stick, “Press” below for pressing it down.
- The tags follow the connected controller and only cover the keys it has. A PS5 controller shows the shape of a DualSense, a PS4 or PS3 controller that of a DualShock 4, a Joy-Con that of a Joy-Con (a single one turned sideways or upright as you hold it, two connected together as a pair), and all others the shape of an Xbox controller; key names and icons are what the controller prints, such as × ○ □ △, L1 and Options, or B A Y X, ZL and +.
- The row below the controller holds the extra keys: the Share button of an Xbox Series controller, the mute button of a DualSense, the Capture button of a Switch controller, the back and Fn buttons of an Elite or Edge, and the SL and SR of Joy-Cons. A controller without them has no such row.
- A single Joy-Con held sideways has one stick, four face buttons, SL, SR, L and ZL (R and ZR on the right one), and − or +. The Xbox, PS and Home buttons cannot be mapped; SDL reports the Capture button of a left Joy-Con held sideways as the Home button, so it cannot be mapped either.
- A single Joy-Con held sideways is shown turned sideways. When a left and a right Joy-Con are connected at the same time, they become one full controller by themselves, with no setting, and the picture shows the pair. When only one of them is left, it is a single Joy-Con again.
- A Joy-Con 2 (the Switch 2 Joy-Con) is connected as described in [Joy-Con 2](15-joycon-2.md). Its keys are the same as a Joy-Con's, the right one has an extra C key, and a left and a right Joy-Con 2 also become one full controller by themselves (they never mix with the Switch 1 Joy-Cons). The picture shows the Joy-Con 2 with its own model, and the Switch 1 Joy-Cons keep theirs. The Joy-Con 2 model has no SL, SR, ZL or ZR to point at, so their tags point at fixed places on the rail and the shoulder. The C key is tagged C.

## The key drawer

Click a tag and the key's settings open on the right. From top to bottom:

1. The title is the name of the key, as printed on the controller.
2. “Function”: the text shown on the tag. When it is empty, the tag shows the shortcut. A name goes with a mapping: it cannot be filled in before the key has one, and deleting the last mapping deletes the name too.
3. “Mappings”: what the key does, and when. Click “Add another action” to add one. See [Mappings](02-mappings.md).
4. “Advanced”: timing, latch and haptic, also covered in [Mappings](02-mappings.md).

The drawers of sticks and triggers also have “Stick settings” and “Trigger settings”. See [Sticks and triggers](07-sticks-and-triggers.md). Click “Close key editor”, or click another tag, to move to another key.

## Capturing a shortcut

Shortcuts are captured, not typed. Click the box or “Capture shortcut” to start. The box shows “Press a shortcut…”. That click is not recorded.

- Capturing ends at the first key. While you hold Ctrl, Shift, Alt or Win it goes on; the first other key you press is recorded together with the modifiers you hold, for example Ctrl+Shift+Z. Press only modifiers and let them all go, and those modifiers are recorded.
- A shortcut has at most three keys.
- Inside the padmate window, mouse buttons and one notch of the wheel (up, down, left or right) can be captured too. Each counts as a key and can go with modifiers. While you capture, a right click opens no menu and the wheel does not scroll the page.
- A short press of Esc is recorded as Esc. Hold Esc for 1 second to cancel and change nothing. Switching to another window also ends the capture.
- Under “Advanced”, when “Map scan code” is on, keys of the keyboard are recorded by their position, whatever the input method and keyboard layout. Mouse buttons and the wheel are not affected.

## Key actions

The “Key actions” menu next to the title has these items:

- “Copy this key's settings” and “Paste…”: copy all settings of one key to another key. Pasting replaces the target key's current settings, so padmate asks first.
- “Clear this key…”: remove all mappings and the shown text of the key. The name stays.

## Changes apply at once

There is no save button. Every change is saved and applies at once.

- Press Ctrl+Z to undo and Ctrl+Y to redo. Each profile keeps up to 100 steps, cleared on quit. Creating, deleting and renaming profiles and binding apps cannot be undone.

## Viewing and exporting

The “More” menu also has:

- “List all keys”: lists every key that has settings. Click a row to open that key.
- “Export shortcut table…”: saves an image or Markdown. Each row is a controller key and the shortcut it sends.
