# CLIP STUDIO PAINT

[中文](16-clip-studio-paint.zh-CN.md)

padmate can read the shortcut settings of CLIP STUDIO PAINT (CSP below). To give a key of the controller a function, you pick it from the commands and tools of CSP. You do not first record a shortcut in padmate and then go to CSP to set a function on the same key.

This chapter only matters for a profile that is bound to CSP. Bound means the profile has an app rule that selects `CLIPStudioPaint.exe`, see [Profiles and switching by app](06-profiles.md). The bundled profile CSP is already bound. A profile that is not bound shows nothing of what this chapter describes, and Settings has no section for it. Without CSP's settings, mapping works as usual.

## Picking a function of CSP for a key

1. On the “Mapping” page, click a key to open the key drawer. Next to the shortcut of a binding, beside the “Capture shortcut” button, there is one more button, “Pick from CLIP STUDIO PAINT…”.
2. The window that opens follows the shortcut settings dialog of CSP. At the top, “Setting area” chooses the part of CSP to look at (for example the main menu or the tools); below it the menus of that area are a tree, in the order CSP has them, all closed at first. Click the arrow of a group to open or close it; the arrow keys, Enter and Space work too. A tool that has sub-tools can be selected and opened. Or search by name, menu path or shortcut: the search looks in all areas and shows the matching functions with the groups above them, open. Clearing the search brings back the area and the open groups you had. Select a function and click “Set”.
3. The shortcut that the function has in CSP is filled into the binding. The drawer says which function of CSP it is, and the step can be undone like any other edit. Undoing it (Ctrl+Z) also takes back the function that the profile noted, and redoing brings it back. When the selected function is not among the rows the window shows now (a search filtered it out, another setting area is shown, or its group is closed), “Set” is greyed out, so it never writes a function you cannot see.

The window keeps the groups you opened until it is closed. Each function in the window shows on its right the shortcut it has in CSP now; one without a shortcut has an empty place there. A function that has several shortcuts takes several rows, the name on the first; they are one function, and the row you select decides which shortcut goes into the binding (the name row gives the first).

## Picking a function that has no shortcut

When you select a function that has no shortcut, the window asks you to record a key and then writes it into the settings of CSP.

1. Record a key. padmate says at once who uses that key in CSP: nobody, or which command or tool. padmate does not choose a key for you and does not suggest a free one.
2. When the key can be written, click “Write to CLIP STUDIO PAINT”.
3. After the write, the window says what was written. To go back, click “Undo this write”; if you do not need that, click “Done”. “Undo this write” takes back three things at once: the settings of CSP, the function the profile noted, and the shortcut in the binding (with its name), to how they were before the pick; there is no step left to undo or redo afterwards. CSP goes first: if it is running it has to be closed, and if it cannot be restored nothing else is touched. If a later part does not work, the window says which, and clicking again does only what is left. If you press Ctrl+Z in padmate instead, only the mapping of padmate and the profile's record go back; the key written into CSP stays there, as it is only an added key.

If CSP is written but the profile could not save it, the window says “The shortcut is written in CLIP STUDIO PAINT, but it could not be saved into the profile. The binding is not changed yet.” and has only two buttons: “Retry saving” repeats only the saving and the binding, without writing CSP again; “Undo this write” restores CSP, after which nothing is changed anywhere. The window cannot be closed in this state until one of them is chosen. If the window goes away anyway (for example the key editor is closed), the write can still be restored from the CSP backups in Settings. If the profile in use has changed meanwhile, both “Retry saving” and “Undo this write” of a finished pick refuse and say so; go back to the original profile and use them there.

CSP has to be closed before its settings are written. While CSP is running, padmate shows “Close CLIP STUDIO PAINT first”. Close CSP and click “Retry”; if you no longer want to write, click “Cancel”.

Before each write, padmate makes a backup of the two shortcut files of CSP. Backups and restoring are in the section “Settings” at the end of this chapter.

## Cycle keys and holding a key

- **Cycle keys.** Several tools in CSP can share one single key, and each press moves to the next. padmate cannot see which tool CSP has selected, so one press may not reach the one you picked. The window marks such a function as a cycle key and names the tools that share it. For one press to reach it, give it a key of its own and let padmate write that into CSP.
- **Holding.** A single key in CSP that is held for longer than a set time switches tools for a moment, and the previous tool comes back when you let go. That time is set in the preferences of CSP. If the key of the controller is a hold binding and sends such a key, padmate says so when you record it.
- A key that the modifier-key settings of CSP use, such as holding Space to change the tool, is noted in the same way, but not blocked.

## When the shortcuts of CSP change

When a profile becomes active, padmate compares the functions the profile has noted with the settings of CSP now. It compares again if the files of CSP change while the profile is active.

- If everything agrees, nothing happens. Extra keys that CSP has are not a difference.
- If something differs, padmate says so in one line in the overlay and the tray. It does not open a window or bring itself to the front, so it does not interrupt your drawing.
- When you open padmate, the window “CLIP STUDIO PAINT settings changed” appears. It lists all the differences at once and offers three choices:
  - “Keep padmate's keys”: writes those functions back into CSP, so CSP is as the profile noted it. CSP has to be closed first, and before the write padmate lists which functions would lose their keys.
  - “Follow CLIP STUDIO PAINT”: the functions of the keys stay, and padmate changes the keys in the mapping to the keys those functions have in CSP now. A function that has no shortcut in CSP any more keeps its old key, and is marked.
  - “Not now”: the same differences are not reported again; only a new difference is.

While CSP is running, its tool settings are as they were when it was last closed. So a difference in tools seen during that time may be old, and padmate compares again after CSP is closed.

## Exporting and importing

An exported profile carries its settings for CSP: which command or tool each key stands for.

After you import such a profile, if it differs from your CSP, padmate shows “Imported CLIP STUDIO PAINT settings”, lists the differences and offers three choices:

- “Change my CLIP STUDIO PAINT to the imported settings”: writes the imported settings into your CSP. Only the items that belong to this profile are changed; your whole set of shortcuts is not overwritten.
- “Keep my CLIP STUDIO PAINT and adjust the keys”: your CSP is left alone, and the keys change to the keys those functions have in your CSP now.
- “Later”: nothing is changed.

If you choose the first and the write would take the key of another function of your CSP, padmate lists those functions first and then asks you to click “Decide one by one”. Each item has four choices:

- “Use the imported setting”: your own function loses this key.
- “Keep my setting”: this item is not imported, and the keys that use this key are marked.
- “Give the imported function another key” and “Give my function another key”: record a new key on the spot, which is checked in the same way for who uses it in CSP.

Nothing is written until every item is decided. A command that your CSP does not have, or a sub tool that someone made themselves, is skipped and listed, and the matching keys still send the keys they had.

## Settings

On the “Settings” page, when a profile bound to CSP exists, there is one more section, CLIP STUDIO PAINT.

- “Data folder”: the settings folder of CSP that padmate found. If it was not found, the format is unknown or the version is not supported, the reason is written here. Click “Change…” to choose the folder yourself; it must be the folder that holds `Shortcut` or `Tool` (named like `CLIPStudioPaintVer1_5_0`), and padmate says so if it is not. Once you have chosen one, “Find automatically” appears and takes your choice away. When the command names cannot be read from CSP (its install folder was not found, or its resource files could not be read), one more sentence says so here, and some commands show their internal names.
- “Backups”: the backups made before each write, listed by time, with the latest 30 kept. Click “Restore…” beside a backup and, after you confirm, the shortcut settings of CSP go back to that one. Restoring also needs CSP closed, and the current settings are backed up first.

## Limits

- Only CLIP STUDIO PAINT is supported.
- The Microsoft Store and Steam versions are not promised.
- The names of a few commands may show only as an ID: those that CSP makes while it runs, and commands you made yourself, have no name to read.
- For some symbol keys it is not yet confirmed which physical key they are. The window says so, and for such a key it cannot yet tell whether the key is in use.
- If the structure of the settings files changes after a CSP upgrade, padmate turns this feature off and says so. Mapping is not affected.
- The cloud backup of CSP may restore the settings that padmate wrote to older ones, and padmate cannot control that.
