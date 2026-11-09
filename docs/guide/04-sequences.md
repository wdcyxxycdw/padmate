# Sequences

[中文](04-sequences.zh-CN.md)

A sequence makes one mapping do several things in turn.

## Making a sequence

1. In the mapping, set the “Action type” to “Shortcut” and capture the first shortcut.
2. Click “Add action sequence”, then choose “Shortcut” to tap a shortcut, or “Wait” to pause for a moment.
3. The steps are listed under “Sent in order”, called “Step 1”, “Step 2” and so on. Use the buttons on the right to move a step up or down, or to delete it.

To make a left double click: while capturing, click the left button twice quickly in the window (the first click is recorded as MouseLeft and the second is added as step 2 for you), or capture the left button, click “Add action sequence”, choose “Shortcut” and click the left button again. A double click is two MouseLeft steps, each held for 20 ms with 20 ms between them (the default tap press and tap gap, see [Mappings](02-mappings.md)), well inside the system double-click time. Other mouse buttons and clicks with modifiers (such as Shift+left button) work the same way.

For example, select all and then copy:

- Step 1, shortcut: Ctrl+A.
- Step 2, wait: 50 ms.
- Step 3, shortcut: Ctrl+C.

Notes:

- The time of “Wait” is 1 to 10000 ms. A new wait is 100 ms, counted from the release of the previous step.
- A sequence has at most 64 steps, and its shortcuts are all taps.
- Pressing the key again before the sequence is done queues a new run behind it. At most 16 runs queue; more are dropped.
- Pausing mapping, switching profiles and an emergency release end the part that has not run yet.
