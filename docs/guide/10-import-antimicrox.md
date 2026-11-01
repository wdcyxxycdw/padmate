# Importing from AntiMicroX

[中文](10-import-antimicrox.zh-CN.md)

AntiMicroX is another program that turns a controller into a keyboard. Its profile files can be read into padmate, so you do not have to start over.

## Importing

1. Click “Profiles” on the left, then click “Import…” at the top of the page.
2. Choose the profile file of AntiMicroX. Its extension is `.amgp` (`.xml` in older versions).
3. padmate creates a new profile and shows an import report.

You can also use the command line: in the `resources\bin` folder, run `.\padmatectl.exe profile import-amgp file`.

Only gamepad profiles are accepted, at most 1 MiB. Very old files (`configversion` below 6) are refused; open one in a newer AntiMicroX, save it again, and retry.

## Reading the report

The report has three groups by severity. Each line says where it is in the file and why:

- “Dropped”: this item was not imported.
- “Approximate”: padmate has a close way of doing it, but the behavior differs a little.
- “Info”: a note, sometimes asking you to check something.

Click “Open profile” to open the new profile. Press Esc or click the close button to close only the report; the new profile stays in the list on the Profiles page. The reasons in the report are only in Chinese.

## What changes

- Settings of sticks, triggers and the D-pad: in AntiMicroX every set has its own, while in padmate the whole profile has one. The import uses set 1's, and differences in other sets are listed in the report.
- A setting in set 1 that holds View or Menu to change sets is imported as the function key's set of keys. The function key is Menu by default, so bindings on Menu do not take effect by default, and the report says so. You choose the function key for each controller in “Settings”, under “Controllers”.
- One set is imported as one layer. The app edits only the base layer, and other layers can only be changed in the file; see [Layers and the function key](03-layers-and-function-keys.md). In the layer that is held to change sets, keys without a binding are blocked, because a set in AntiMicroX replaces everything.
- A setting that cycles to the next action on every press is imported as double tap up to four taps, provided its reset time is at most 1000 ms; otherwise only the first step is kept. The app shows only “Tap”, “Hold” and “Double tap”; triple and quadruple taps work as usual but can only be changed in the profile file. With a double tap, the action of a single tap fires only once it is certain there is no second press.
- Key codes are imported for a US keyboard. A character that needs Shift, for example `!`, is written as `Shift+1`. Navigation keys of the numeric keypad are imported as digit keys, which only match with NumLock on. Keys with no counterpart, such as F25 and above, browser keys and launch keys, are dropped.

## What to check after importing

1. Read “Dropped” and “Approximate” in the report.
2. Look at each key on the “Mapping” page and check that the shortcuts are right.
3. Try a few keys on the controller, especially hold and double tap.
