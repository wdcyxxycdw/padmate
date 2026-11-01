# Sticks and triggers

[中文](07-sticks-and-triggers.zh-CN.md)

Sticks and triggers can be pushed to different depths. This chapter covers making them send keys, move the mouse, or work as a knob. Click the tag of a stick or trigger on the controller picture, and its settings open on the right. These parameters apply to the whole profile.

## Sticks

The tag of each stick has two cells, each with its own drawer:

- The upper cell, “Move”: how the stick is pushed. Everything in the sections below is in its drawer. “Function” at the top names the push, for example brush size, and the cell then shows it; left empty, the cell shows a name made from the direction mode.
- The lower cell, “Press”: the key you get by pressing the stick down, set like any other key; see [Mappings](02-mappings.md).

### Direction mode

“Direction mode” decides what is sent when the stick is pushed to one side:

- “4 directions”: pushed up, down, left or right, only the nearest direction is pressed; a diagonal push never presses two at once.
- “Knob”: the stick works as a knob. Each time it turns through an angle, one clockwise or counter-clockwise key is sent, not direction keys.
- “Mouse”: the stick moves the mouse pointer.

### Keys of the directions

Once the direction mode is chosen, a row for each direction appears below. Click a row to set that direction's key, as for any other key. Click “Arrow keys” or “WASD” next to the title, confirm, and all four directions are set at once.

### Sensitivity

Under “Sensitivity” are two sliders, and the circle beside them shows the stick's position live:

- “Dead zone”: a push within this range counts as none. The default is 15%.
- “Full push”: a push to this range counts as full. The default is 95%.

### Advanced

- “Trigger point”: how far to push before a direction counts as pressed. The default is 30%.
- “Direction delay”: after the direction changes, wait a moment before switching. 0 means switch at once.
- “Other inputs”: “Push in any direction” fires when the stick leaves the dead zone, and can fire together with a direction. “Light push” and “Hard push” fire by how deep you push, and “Hard push from” sets where a hard push starts.

### Knob mode

- “Step”: degrees to turn per key sent. The default is 30°.
- “Clockwise step” and “Counter-clockwise step”: the keys sent for one step. Each row takes one shortcut: no hold, double tap, action sequence or script.

### Mouse mode

- “Speed”: how many pixels per second the pointer moves at a full push. The default is 1200.
- “Response curve”: how speed changes with push depth: “Linear”, “Quadratic” or “Cubic”.
- “Smoothing”: the higher the number, the slower the pointer's speed changes. 0 means no smoothing.

## Triggers

A trigger works like a key: it counts as pressed once it passes a position. Besides the mappings, the drawer has “Trigger settings”:

- “Live pressure”: how many percent it is pressed now.
- “Press”: it counts as pressed once past this position. The default is 30%.
- “Release”: it counts as released only when back below this position. The default is 20%, and it cannot be above “Press”. With the two positions apart, a trigger that jitters at the edge does not press and release again and again.
- “Two-stage trigger”: adds a second key to the trigger. Pressed to the position of “Full pull: press” (90% by default), it fires “Full pull”; it releases only when back below “Full pull: release” (80% by default).

## The D-pad

The D-pad has no settings of its own: up, down, left and right are four separate keys, each set in its own drawer and sent separately when two are down. If an older profile file gives the D-pad another mode (for example 4-way or 8 directions), it keeps working but cannot be changed in the app.
