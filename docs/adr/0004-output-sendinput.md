# ADR-0004: Keyboard and mouse output

[中文](0004-output-sendinput.zh-CN.md)

Related: [ARCHITECTURE §8](../ARCHITECTURE.md#8-output), [PITFALLS "Keyboard output and layouts"](../PITFALLS.md)

## Context

padmate injects keyboard and mouse input into the foreground program with SendInput.

- A scan code is a physical position, and the same scan code types different characters on different layouts: always sending US scan codes turns `Ctrl+Z` into `Ctrl+W` on AZERTY. The layout is set per thread, and the daemon's layout is not the foreground program's.
- Programs match shortcuts by VK: the VKs of letters and digits follow the layout, while the VK_OEM_* of punctuation stand for different characters on different layouts.
- Pressing and releasing Alt on its own activates the menu bar, and Win on its own opens the Start menu; a lone Alt or Win on a controller is common.
- Painting also needs the wheel and pointer movement.

## Decision

1. **Scan codes, resolved for the foreground layout.** Output uses `KEYEVENTF_SCANCODE`. Letters and digits are looked up by VK (the scan code of that VK on the layout, with no modifiers added); punctuation and `Plus` are looked up by character (`VkKeyScanExW`, adding the modifiers it needs). Every press of a character key looks up the HKL of the foreground thread again, uncached, since switching the layout inside one window sends no focus event. When the layout gives no usable key, the US mapping is used with a warning. The engine knows nothing about layouts; resolving happens at the output stage.
2. **Three ways to write a KeySpec.** A character key (`Z`) follows the layout; a physical key (`KeyZ`) is a fixed US position; `sc:0x2C` is sent as it is.
3. **A few keys are injected by VK.** Pause, the numpad digits (unaffected by NumLock) and the menu mask key. `KEYEVENTF_UNICODE` is for typing text only, never for shortcuts.
4. **The injection tag.** `dwExtraInfo` is "padm" plus a nonce. It is for filtering events, not against forgery.
5. **`padmatectl send` is the exception to invariant 3 outside the daemon.** It calls SendInput directly and does not coordinate key state. Tests with real injection must have every event swallowed by a hook (invariant 6).
6. **Alt/Win menu mask key vkE8.** Before Alt or Win goes up, if no other key went down since, a press and release of the mask key are inserted in the same SendInput. A Sender wrapped by `MenuMask` does it; it sits between the Arbiter's ledger and the real Sender, and both dry runs and injection go through it. Masking is always on; to open the menu, bind F10.
7. **Key gap.** `output.eventGapMs` (0–50 ms) queues events in the Arbiter, one Send per event; nothing sleeps in the Sender, since T-output must never stall. A key is held for a minimum time before it goes up, and the keys of one combination are never held together with the next one. **Releases do not wait for the gap** (invariant 1): `ReleaseOwners` and `ReleaseAll` go out at once, after the queued events in their order.
8. **Mouse buttons and the wheel are keys.** Mouse buttons (`MouseLeft`, `MouseX1` and so on) and `WheelUp/Down/Left/Right` are a kind of KeySpec and can go anywhere a main key can (but not as modifiers), so the existing mechanisms apply. The wheel is momentary: a press sends one notch, and the release injects nothing, though the arbiter still keeps account. Mouse buttons can be held, and every exit path lifts them; a button already held gets no second press (that would be half a double click).
9. **Sticks move the pointer.** With `mode:"mouse"` a stick pushes the pointer, and the precise action `mouse precision` slows it while its gesture is held. The engine integrates the stick as a continuous system, independent of the timer's phase. Pause, `ReleaseAll` and `Reset` "lock" a stick that is being pushed; it moves again only after it returns to the dead zone. Movement is not a key: it has no owner and no release. It is injected as relative `MOUSEEVENTF_MOVE`, which the system pointer speed affects.

## Costs

- Every character key looks up the layout before it is sent; a character the layout lacks falls back to the US scan code.
- A combination of modifiers only is masked too, which may affect Alt+Shift switching the input method. The mask key stays held while it waits to be sent again, and the startup sweep has to check it on its own.
- One lost injected event of a mouse button is a click on the screen, so mouse tests with real injection must install the mouse hook first. Swapped primary and secondary buttons and absolute jumps on several monitors have not been tested.

## Alternatives given up

- **Send VKs only.** Programs that recognize keys by scan code may not accept them.
- **Always send US scan codes, or resolve with the daemon's layout or the layout at startup.** Layouts are per thread, and users switch them while running.
- **Use `KEYEVENTF_UNICODE` for everything.** Shortcuts would never arrive.
- **Look up every key by character, or every key by VK.** By character, `Ctrl+1` becomes `Ctrl+Shift+1` on AZERTY; by VK, VK_OEM_* are different characters on different layouts.
- **Look only at `LLKHF_INJECTED`, or tag without a nonce.** padmate could not be told from other injecting programs, and two processes running at once would swallow each other's events.
- **Mask with Ctrl, Shift or another VK.** Ctrl has actions of its own; a lone Shift switches between Chinese and English in Microsoft Pinyin; 0xFF, 0x07 and 0x97 mean no mapping, used by Game Bar and the test canary.
- **Swallow Alt's release; mask in the Arbiter or the engine.** Swallowing needs a hook, and Alt would stay held; the Arbiter's Held would show the mask key; the engine cannot see the other slots.
- **Separate action types for mouse buttons and the wheel.** Taps, repeat and release would all have to be done again.
