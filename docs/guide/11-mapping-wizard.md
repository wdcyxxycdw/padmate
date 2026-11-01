# The mapping wizard

[中文](11-mapping-wizard.zh-CN.md)

padmate cannot read the keys of some controllers: no-name USB controllers, or controllers switched to DirectInput mode. The mapping wizard asks you to press or push each control in turn and saves the result as a mapping. After that the controller is a standard controller, and every feature works.

## When to use it

- After you connect the controller, its name on the “Settings” page, under “Controller calibration”, says “Not recognised as a gamepad”.
- The controller works, but a few keys are in the wrong place and you want to redo them.

## Setting up a mapping

1. Click “Settings” on the left, find the controller under “Controller calibration”, and click “Set up mapping…”.
2. The wizard opens. While it is open, padmate pauses mapping for all controllers, so no key goes to other programs.
3. Do not touch the controller for a moment; the wizard is recording where the sticks rest.
4. The wizard asks for one control at a time, for example “Press A”. Press or push that control. The wizard writes “Detected: Button 3”, records it, and asks for the next.
5. After all controls, the wizard lists everything it recorded for you to confirm. Click “Save”.
6. With no warnings, the window closes by itself. Otherwise it stays open: it says “Mapping saved” with the warnings below it (see the next section), and you click “Done” to close it.

The wizard asks in this order: A, B, X, Y; the D-pad up, down, left and right; the left stick left, right, up, down and press; the right stick the same; LB, RB, LT, RT; and last View and Menu. padmate does not use the Home key (the Xbox button, the PS button), so the wizard does not ask for it; see [Layers and the function key](03-layers-and-function-keys.md).

These buttons are always available:

- “Back”: go back to the previous control.
- “Redo”: record this control again.
- “Skip”: skip a control the controller does not have, for example some controllers have no right stick.
- “Finish”: stop asking for the remaining controls and go straight to the check.
- “Cancel”: give up and save nothing.

Tips:

- Press only one direction of the D-pad at a time. Press a trigger all the way down and then release it, and push a stick all the way.
- The buttons, axes and D-pad directions in the wizard are numbered by SDL, starting at 0, so “Button 3” is the fourth button.

## After saving

The mapping applies by controller model; controllers of the same model share one. After saving, the window may warn you about:

- View or Menu is not mapped. They are the emergency pause keys, and without them the controller cannot use it. See [The tray and emergency release](12-tray-and-emergency.md).
- You set the environment variable `SDL_GAMECONTROLLERCONFIG`, and it has a mapping for this controller. It takes priority over padmate's, so the mapping you just saved does not apply. Remove that variable.

## Remapping and restoring the default mapping

- When the controller is already recognised, the menu at the right of its row under “Controller calibration” has “Remap…”. The wizard is as above.
- A controller with a custom mapping has “Custom mapping” beside its name, and that menu has “Restore default mapping”, which first asks “Restore the default mapping?”. After restoring, your mapping is deleted and the controller goes back to SDL's own mapping for it, or to unrecognised if SDL has none.
