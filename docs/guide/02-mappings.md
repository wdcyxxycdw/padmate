# Mappings

[中文](02-mappings.zh-CN.md)

A mapping decides what a key does, and when. One key can have several mappings, each doing its own thing.

## The three mappings

In the key drawer, click “Add another action”. The menu lists the kinds this key does not use yet.

| Mapping | When it fires |
|---|---|
| “Tap” | You press the controller key once. |
| “Hold” | You hold it longer than the “Long-press delay”. It fires as soon as the time is up, without waiting for release. |
| “Double tap” | You press it twice in a row. |

A new key has one empty “Tap”, and you only need to capture a shortcut. To give a key two uses, the usual way is to add “Hold”: a tap sends one shortcut, and holding sends another.

Click “Remove this mapping” on the card of a mapping to remove that kind. Every kind can be removed. A key with no mapping sends nothing.

## When “Tap” fires

padmate chooses automatically, depending on whether the key has other mappings:

- With only “Tap”, it fires the moment you press the controller key, and the shortcut stays down while you hold it. This suits keys such as the hand tool (Space) that only work while held.
- With “Hold” or “Double tap” too, “Tap” fires only when you release the controller key, and sends one press and release. A shortcut of modifiers only then does not stay down, and “Latch” stops working. For such uses, give the key only “Tap”.

## What a mapping does

Each mapping has an “Action type”: “Shortcut” sends a shortcut (for capturing, see [The controller picture and the key drawer](01-controller-and-drawer.md)), and “Script” is covered in [Scripts](08-scripts.md).

After “Hold” fires, the shortcut stays down until you release the controller key. “Double tap” sends one press and release.

## Adjusting the times

The times can be set in three places, widest first:

1. On the “Settings” page, “Key timing”: for all profiles, with the “Long-press delay” and the “Multi-tap window”.
2. In “Profile settings…”, “Key timing”: for this profile only, and “Advanced” there also has “Tap duration” and “Tap gap”.
3. “Advanced” in the key drawer: for this key only, with all four.

When a value is empty, the one above it is used. The defaults are 250 ms for the long-press delay, 220 ms for the multi-tap window, and 20 ms each for the tap duration and the tap gap. The last two are how long a tap is held and the gap between two taps; together they limit one key to at most 25 taps per second.

## Interrupting the decision

With “Hold” together with “Tap” (or “Double tap”), you may press another key while padmate is still waiting. On a key with “Hold”, “Advanced” has “When another key is pressed before a hold”, which decides what happens:

- “Automatic” (the default): if the hold is a lone modifier or switches the key set, it works as “Prefer hold”; otherwise it works as “Balanced”.
- “Prefer hold”: if another key is pressed while waiting, this key counts as held at once.
- “Balanced”: this key counts as held only if another key was pressed and released while waiting.
- “Time only”: only the time counts.

## Latch and haptic

“Advanced” has two more items:

- “Latch”: press once and the shortcut stays down; press again to release it.
- “Haptic”: the controller buzzes each time the key fires. Choose “Tick”, “Confirm”, “Error”, or “1 pulse” to “4 pulses”.
