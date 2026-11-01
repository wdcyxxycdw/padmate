# Frequently asked questions

[中文](14-faq.zh-CN.md)

## Windows says “Windows protected your PC”

This is Windows SmartScreen. The installer has no code signature, and every new release starts with no reputation, so this note appears. Click “More info”, then “Run anyway”.

To check that your download is unchanged, compare its SHA-256. Next to the installer is a file with the same name plus `.sha256`. Its address is `https://padmate.download/` followed by the installer's file name and then `.sha256`; open it in a browser to see the value. Open PowerShell in the folder of the installer and run `Get-FileHash .\installer.exe -Algorithm SHA256`, replacing the file name with the one you downloaded. Ignore case when comparing. If the two values match, the file is unchanged.

## An antivirus program blocked padmate, or reports it

An unsigned program is more likely to be blocked by some antivirus programs. These padmate behaviors may be taken for suspicious:

- It sends shortcuts with the SendInput function of Windows. That is its job.
- It installs a keyboard hook only while you click “Capture shortcut”, for 30 seconds at most. The hook only watches which key was pressed; it changes and blocks nothing.

If it was quarantined, restore it like this:

1. Open “Windows Security”, go to “Virus & threat protection” and click “Protection history”.
2. Find the blocked padmate file and choose to allow or restore it.
3. If it is still blocked, exclude only the two files `padmated.exe` and `padmatectl.exe`. They are in the `resources\bin` folder of the install folder.

Do not exclude the whole install folder, and do not turn off your antivirus program for this. If you think it is a false alarm, open an issue on the [GitHub page](https://github.com/wdcyxxycdw/padmate/issues) with the antivirus program's name and version, the blocked file and the padmate version.

## Pressing the controller does nothing

1. The status at the top of the window should be “Running”. If “Paused”, click “Resume mapping”.
2. The target app must be in front. While the padmate window itself is in front, no shortcut is sent.
3. On the “Mapping” page the key is not “Not set”, and the profile at the top left is the one you think.
4. If the target app runs as administrator, restart it with normal rights.
5. Open the Diagnostics page, press the key, and look at the new row. If there is a row, the result column gives the reason; if there is none, padmate did not receive the press. See [Diagnostics](09-diagnostics.md).

## The controller does not show up, or input is doubled

- First make sure the controller is an Xbox, PlayStation or Switch Pro controller, or a Joy-Con. A Joy-Con 2 needs its own connection steps; see [Joy-Con 2](15-joycon-2.md).
- A left and a right Joy-Con that are connected together are one controller; one connected alone is a small controller held sideways. When the second one connects, or one of the two drops, the controller is replaced: held keys are released, and mapping goes on with the new one. The settings of the pair are separate from those of a single Joy-Con. The same holds for a left and a right Joy-Con 2.
- Steam, DS4Windows and HidHide may take the controller over. “Conflicts” on the Diagnostics page names the one running and says what to do; see [Diagnostics](09-diagnostics.md).
- When, on the “Settings” page, under “Controller calibration”, the controller says “Not recognised as a gamepad”, build a mapping with the [mapping wizard](11-mapping-wizard.md).

## Shortcuts do not reach an app

Usual causes are windows that run as administrator, system combinations, an input method that swallows bare letter keys, and some apps that miss the Ctrl or Shift of a combination.

## A stick moves without being pushed, or cannot be pushed all the way

On the “Settings” page, under “Controller calibration”, click “Calibrate” for that controller, and push the stick to see its position (a dot). Raise the “Dead zone” until the dot stays inside the circle when you let go; lower the “Full push” so the stick counts as fully pushed without going all the way. Calibration applies only to that controller.

## A key is stuck

First use the controller's emergency pause: hold the two small keys in the middle for 2 seconds. If that is not enough, use “Release all keys” in the window, or run `padmatectl.exe release-all`. See [The tray and emergency release](12-tray-and-emergency.md).

## “Settings reset” at start-up

When the settings file has errors, padmate backs up the original file and resets the faulty parts to their defaults. The dialog gives the backup's name; click “Open folder” to find it and “OK” to close. Backups are not cleaned up automatically; delete them when you want.
