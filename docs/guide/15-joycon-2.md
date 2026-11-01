# Joy-Con 2

[中文](15-joycon-2.zh-CN.md)

Joy-Con 2 are the Joy-Cons of the Switch 2. padmate reads them itself over Bluetooth, so they work as controllers with no cable and no charging grip. This chapter is about connecting them. The Joy-Cons of the original Switch and other controllers connect through Windows as usual; a left and a right original Joy-Con together also become one controller by themselves.

## Before you start

- Windows 11 is recommended. On Windows 10 a Joy-Con 2 works, but with more delay: the sticks update about 17 times a second instead of about 66 on Windows 11.
- Do not add the Joy-Con 2 in the Bluetooth settings of Windows. padmate connects to it without pairing, and the Joy-Con 2 does not accept Windows pairing anyway. If Windows ever asks to pair it, cancel.
- padmate does not change the pairing between the Joy-Con 2 and your Switch 2. The same Joy-Con 2 keeps working on the console.

## Connect a new Joy-Con 2

1. Open “Settings” and click “Connect a new Joy-Con 2” in the Joy-Con 2 section.
2. A window asks you to hold the SYNC button on the side of the Joy-Con 2 within 30 seconds, and counts down the seconds left. Hold it until the window says it is connected. The ordinary buttons do not connect it.
3. When the window says the Joy-Con 2 is connected and remembered, let go and click “OK”. The lights on the Joy-Con 2 show its number.

To stop before that, click “Cancel”. If no Joy-Con 2 is found in 30 seconds, the window says so. Click “OK” and connect again. One round connects one Joy-Con 2: for the second one, do it again.

The Joy-Con 2 section is in “Settings” only when padmate can use Bluetooth LE on this PC. The tray menu has the same item, “连接新的 Joy-Con 2” (the tray menu is in Chinese only). It opens the same 30 seconds, and tells you how it went in notices instead of a window.

## Connect again

padmate remembers the Joy-Con 2 it connected. Next time, do not connect a new one. Hold SYNC on the Joy-Con 2 and padmate connects it by itself.

- You have to hold SYNC every time: after you turn on the PC, after sleep, after the Joy-Con 2 was turned off or went out of range. Pressing the ordinary buttons wakes the Switch 2 but not the PC.
- padmate connects only Joy-Con 2 that it remembers, and only while SYNC is held. A Joy-Con 2 that you have never connected with “Connect a new Joy-Con 2” is left alone.
- In “Settings”, the switch “Connect remembered Joy-Con 2 automatically” is on by default. Turn it off and padmate connects a Joy-Con 2 only in the 30 seconds of “Connect a new Joy-Con 2”. The change applies at once.
- When a Joy-Con 2 stops sending (turned off, out of range, signal lost), padmate lets go of its keys in about a third of a second. If it was one half of a pair, the other half goes on alone right then. If the signal comes back within 3 seconds, it works again with no SYNC; if it takes longer than 3 seconds, hold SYNC to connect it again.

## One Joy-Con or two

- **One left and one right Joy-Con 2** become one full gamepad by themselves, as soon as both are connected. There is no setting for it. When one of them goes away or stops sending, the other goes on alone at once, and when the first one is back, the two become one gamepad again. Held keys are released each time this changes. The picture of a pair has no tags for SL and SR, which cannot be pressed with the two joined; settings a profile already has on them are kept. A Joy-Con 2 only ever pairs with another Joy-Con 2: it never mixes with a Joy-Con of the original Switch, so a Joy-Con 2 and an original Joy-Con stay two single controllers.
- **A single Joy-Con 2** is held sideways, like a single Joy-Con of the original Switch: one stick, four face buttons, SL, SR, L and ZL (R and ZR on the right one), and − or +. See [The controller picture and the key drawer](01-controller-and-drawer.md).
- **The C key** is on the right Joy-Con 2. It is an extra key, and you can map it like the other extra keys. The Home and Capture keys cannot be mapped. On the controller picture its tag is C.
- **The picture** of a Joy-Con 2 on the Mapping page is the model of the Switch 2 Joy-Con, not the original Switch one. Its model has no SL, SR, ZL or ZR to point at, so their tags point at fixed places on the rail and the shoulder.
- The emergency pause is SL and SR together for a single one, and − and + together for a pair. See [The tray and emergency release](12-tray-and-emergency.md).
- The battery of a Joy-Con 2 shows in the status line of the tray, in steps of 10 %. A connected Joy-Con 2 is also listed with its battery in “Settings”, in the section “Controller calibration”, like any other controller.

## Back to the Switch 2

To use the Joy-Con 2 on the Switch 2 again, press SYNC once. It disconnects from the PC. After that, use it with the console as usual. If the Switch 2 is on and close by while you use the Joy-Con 2 with the PC, it may take the controller first; turn the console off, or put it in airplane mode.

## Forget a Joy-Con 2

The Joy-Con 2 section of “Settings” lists the Joy-Con 2 that padmate remembers, each with its side (left or right), its serial number and whether it is connected. To make padmate forget one, click “Forget” beside it, then “Forget” in the window that asks. If it is connected, it is disconnected, and padmate connects it again only after you choose “Connect a new Joy-Con 2”.

## If it does not connect

- Hold the SYNC button itself, not Home or another button, until padmate says it is connected.
- If an attempt fails, padmate waits a few seconds before it tries the same Joy-Con 2 again. Connecting many times in a row can make a Joy-Con 2 refuse for a few minutes; wait, then try again.
- If “Settings” has no Joy-Con 2 section and the tray menu has no “连接新的 Joy-Con 2”, padmate could not start Bluetooth LE on this PC. Check that the PC has Bluetooth and that it is on, then start padmate again.
- Do not add the Joy-Con 2 in the Bluetooth settings of Windows. If you did, remove it there.
