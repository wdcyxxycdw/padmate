# Diagnostics

[中文](09-diagnostics.zh-CN.md)

A controller key does nothing? The Diagnostics page shows what padmate did with that press, and if nothing, why.

## Opening Diagnostics

Click “Settings” on the left, go to “About” at the very bottom, find the “Diagnostics” row, and click its “View”. To go back to the settings, click the back button at the top of the page, or press Esc. The page has three parts: “Events”, “Latency” and “Conflicts”.

## Events

“Events” lists the most recent 200 presses since this connection, the newest on top. Each row, from left to right, shows: the time; the name of the controller (only when two or more controllers are connected); the key pressed; the mapping (“Tap”, “Hold” or “Release”); the set of settings used (“Normal”, “Hold function key”, “Shared” or “Script”); and the result. When a shortcut was sent, the result names the action; otherwise it says why in one sentence.

Next to the result there may be two marks:

- “Preview only”: this action was not really sent. That is so when the mapping service is in dry-run mode, or the padmate window itself is in front.
- “Waited N ms”: this key waited this long before firing, to tell a tap, a hold or repeated taps apart.

At the top of the page are “Filter by key” (to see one key only), “Only presses with no action”, “Pause recording” (which becomes “Resume recording” when you click again) and “Clear”.

## Common reasons

The most common sentences in the result column:

- “This key has no action in the current profile.”: the key is “Not set” in the current profile. Give it a shortcut, or check that the current profile is the one you expect.
- “Nothing happens on press; the action waits for release, hold or repeated taps.”: the action comes later. This is not a problem.
- “Mapping is paused.”: the status at the top of the window is “Paused”. Click “Resume mapping”.
- “The system has suspended mapping (lock screen, sleep or secure desktop).”: mapping resumes by itself when you are back.
- “This controller is set to be ignored.” and “This controller has no standard mapping.”: this controller is not mapped. For a controller without a standard mapping, build one with the [mapping wizard](11-mapping-wizard.md).

If the Diagnostics page shows no new row at all, padmate did not receive the press. Check that the controller is connected, then see “Conflicts” below.

## Latency

“Latency” lists, per controller, how long padmate takes from receiving input to sending the key: the number of samples, then p50, p90, p99 and “Max”. When p99 is above the threshold (1.5 ms by default), a mark appears.

## Conflicts

“Conflicts” lists software that may be holding the controller: Steam, DS4Windows and HidHide. When one is running, a note such as “Steam is running” appears here, with what to do.

When none of this software is found, this says “No software that may hold the controller was found.”
