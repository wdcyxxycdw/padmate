# The tray and emergency release

[中文](12-tray-and-emergency.zh-CN.md)

padmate does not leave keys stuck: on a normal exit, a pause, a controller disconnect or a screen lock, the keys it pressed are released. If a key is ever left down, the emergency pause lets go of it at once.

## The tray

When you close the padmate window, mapping keeps running in the background. The padmate icon stays in the tray, perhaps behind the “^” of the taskbar. Double-click the icon, or open padmate again from the desktop or the Start menu, and the window comes back. Click the tray icon, and the menu has, from top to bottom:

- A few status lines: one per controller, with its name, the profile in use, and the battery if it can be read.
- “暂停映射” (pause mapping): pauses all mapping and releases the keys that are held. After pausing, this item becomes “恢复映射”.
- “登录时启动” (start at sign-in): runs padmate automatically after you sign in to Windows. It is off by default, and the “Settings” page, under “General”, has the same switch.
- “退出 padmate” (exit padmate): releases all keys first, then quits together with the window.

## Pausing and resuming

- The pause button at the top of the window: it shows “Pause mapping” when you point at it, and becomes “Resume mapping” after pausing.

The status at the top of the window shows “Running” or “Paused”. Nothing is pressed for you on resume; a key that was held then has to be released and pressed again. Mapping also pauses while you capture a shortcut, and the top of the window shows “Capturing”.

## Emergency pause

Hold the two small keys in the middle of the controller for 2 seconds, and all mapping pauses at once and every held key is released. Hold them for 2 seconds again to resume.

- On an Xbox controller they are View and Menu, on a PlayStation controller Create (or Share) and Options, and on a Switch Pro controller − and +. A single Joy-Con held sideways uses SL and SR.
- The controller buzzes on pause and on resume. You can turn that off on the “Settings” page, under “Feedback”, with “Pause alert”.

### Changing the combination

1. Click “Settings” on the left, go to “Safety”, find the row “Emergency pause”, and click “Change”.
2. In “Change emergency pause”, hold the new combination of keys and then release them. You need to hold 2 to 4 keys at once.
3. Choose 1 to 5 seconds in “Hold for”.
4. Click “Save”. Until you save, the old combination stays valid.

Click “Restore default” to go back to the factory combination.

## Releasing all keys

This lets padmate release every key it holds without pausing mapping: on the “Mapping” page, “More” at the top right has “Release all keys”; on the “Settings” page, “Safety” has “Release now”.

## Automatic protection

When the screen locks, the user switches, the PC sleeps or signs out, or a UAC prompt appears, padmate releases all keys and pauses mapping by itself, and resumes when you are back. The top of the window shows “System paused”.

## When neither the controller nor the window works

Open PowerShell in the `resources\bin` folder of the padmate install folder and run:

```text
.\padmatectl.exe release-all
```

The default install folder is `%LOCALAPPDATA%\Programs\padmate`. This command does not go through the padmate window, and all keys are released at once. If no running padmate is found, the command fails.
