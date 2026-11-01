# Scripts (macros)

[中文](08-scripts.zh-CN.md)

A script decides with code what a key does. You need one only when shortcuts and sequences are not enough, for example to press a key only once some condition holds.

Scripts are written in TypeScript. The `.padmate\padmate.d.ts` in the script library lists the functions a script can use, and Visual Studio Code shows hints when you edit.

## The Scripts page

Click “Scripts” on the left to open the Scripts page. Scripts live in the `%APPDATA%\padmate\macros` folder, and all profiles share this one library.

- “New”: enter a name to create a script and open it in the editor. The new script has an example: select all, then copy.
- “Import”: put an existing `.ts` or `.js` file into the script library.
- “Reload”: restart a script that was stopped for repeated failures.
- Each script has a card that says which keys use it, or “Not used” if none does. The menu of a card has “Edit”, “Rename” and “Delete”. When a script has an error, its card says “Error” and the reason, and a red dot appears on “Scripts” in the navigation on the left.

“Edit” opens the script in Visual Studio Code, or in Notepad when that is not installed, and after you save, the script takes effect automatically soon. “Rename” also changes references in all profiles. “Delete” clears the keys that use the script, so it asks you first.

## Making a key run a script

1. Open the settings of the key, and in the mapping set the “Action type” to “Script”.
2. Choose one under “Choose a script”. When the library has no script yet, this says “No scripts yet”; click “Go to Scripts” to create one.
3. From then on, each time the mapping fires, the script runs once from the start. When the script has an error, the settings of the key say “This script has an error”.

## An example

This script presses Ctrl+A, waits 50 ms, then presses Ctrl+C:

```ts
import { key, sleep } from "padmate";
export default async function () {
  await key.tap("Ctrl+A");
  await sleep(50);
  await key.tap("Ctrl+C");
}
```

## Rules

- The default export must be a function, possibly `async`.
- A script can use only what padmate provides: `host`, `key`, `sleep`, `now`, `log`, `pad`, `session` and `padmate/math`. It cannot reach files, the network or other programs.
- While a script is still running, pressing its key again does not count and is not queued.
- A run lasts at most 60 seconds, then it is stopped. Keys still held at the end are released automatically. To hold a key until you release the controller key, use a shortcut.
- Pausing mapping, an emergency release, locking the screen, sleep, switching profiles and saving the profile all interrupt a running script.
- A script that loops forever or overflows the stack is disabled until its file changes. After a third interruption within 10 minutes, it stops for good, and you have to click “Reload” on the Scripts page.

## Examples

The `resources\examples\scripts` folder has three complete examples, each with a script `main.ts` and a profile `profile.json`.
