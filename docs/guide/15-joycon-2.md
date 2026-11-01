# Joy-Con 2

[中文](15-joycon-2.zh-CN.md)

Joy-Con 2 are the Joy-Cons of the Switch 2. padmate reads them itself over Bluetooth, so they work as controllers with no cable and no charging grip. This chapter is about connecting them. The Joy-Cons of the original Switch and other controllers connect through Windows as usual.

## Before you start

- Windows 11 is recommended. On Windows 10 a Joy-Con 2 works, but with more delay: the sticks update about 17 times a second instead of about 66 on Windows 11.
- Do not add the Joy-Con 2 in the Bluetooth settings of Windows. padmate connects to it without pairing, and the Joy-Con 2 does not accept Windows pairing anyway. If Windows ever asks to pair it, cancel.
- padmate does not change the pairing between the Joy-Con 2 and your Switch 2. The same Joy-Con 2 keeps working on the console.

## Connect a new Joy-Con 2

1. Click the padmate icon in the tray and choose “连接新的 Joy-Con 2” (the tray menu is in Chinese only; the item means connect a new Joy-Con).
2. A notice asks you to hold the SYNC button on the side of the Joy-Con 2 within 30 seconds. Hold it until padmate says it is connected. The ordinary buttons do not connect it.
3. When the notice says the Joy-Con 2 is connected and remembered, let go. The lights on the Joy-Con 2 show its number.

If no Joy-Con 2 is found in 30 seconds, a notice says so. Choose “连接新的 Joy-Con 2” again and retry. One round connects one Joy-Con 2: for the second one, do it again.

## Connect again

padmate remembers the Joy-Con 2 it connected. Next time, do not use the tray menu. Hold SYNC on the Joy-Con 2 and padmate connects it by itself.

- You have to hold SYNC every time: after you turn on the PC, after sleep, after the Joy-Con 2 was turned off or went out of range. Pressing the ordinary buttons wakes the Switch 2 but not the PC.
- padmate connects only Joy-Con 2 that it remembers, and only while SYNC is held. A Joy-Con 2 that you have never connected through the tray menu is left alone.
- When a Joy-Con 2 stops sending (turned off, out of range, signal lost), padmate lets go of its keys in about a third of a second. If the signal comes back within 3 seconds, it works again with no SYNC; if it takes longer than 3 seconds, hold SYNC to connect it again.

## One Joy-Con or two

- **One left and one right Joy-Con 2** become one full gamepad by themselves, as soon as both are connected. When one of them goes away, the other goes on alone. Held keys are released each time this changes.
- **A single Joy-Con 2** is held sideways, like a single Joy-Con of the original Switch: one stick, four face buttons, SL, SR, L and ZL (R and ZR on the right one), and − or +. See [The controller picture and the key drawer](01-controller-and-drawer.md).
- **The C key** is on the right Joy-Con 2. It is an extra key, and you can map it like the other extra keys. The Home and Capture keys cannot be mapped.
- The emergency pause is SL and SR together for a single one, and − and + together for a pair. See [The tray and emergency release](12-tray-and-emergency.md).
- The battery of a Joy-Con 2 shows in the status line of the tray, in steps of 10 %.

## Back to the Switch 2

To use the Joy-Con 2 on the Switch 2 again, press SYNC once. It disconnects from the PC. After that, use it with the console as usual. If the Switch 2 is on and close by while you use the Joy-Con 2 with the PC, it may take the controller first; turn the console off, or put it in airplane mode.

## Forget a Joy-Con 2

To make padmate forget a Joy-Con 2, use `padmatectl`. Open PowerShell in the `resources\bin` folder of the padmate install folder and run:

```text
.\padmatectl.exe joycon2 status
.\padmatectl.exe joycon2 forget SERIAL
```

The first command lists the Joy-Con 2 that padmate remembers, with their serial numbers. Replace `SERIAL` in the second with the serial number of the one to forget. If it is connected, it is disconnected, and padmate connects it again only after you choose “连接新的 Joy-Con 2”.

## If it does not connect

- Hold the SYNC button itself, not Home or another button, until padmate says it is connected.
- If an attempt fails, padmate waits a few seconds before it tries the same Joy-Con 2 again. Connecting many times in a row can make a Joy-Con 2 refuse for a few minutes; wait, then try again.
- If the menu has no “连接新的 Joy-Con 2”, padmate could not start Bluetooth LE on this PC. Check that the PC has Bluetooth and that it is on, then start padmate again.
- Do not add the Joy-Con 2 in the Bluetooth settings of Windows. If you did, remove it there.
