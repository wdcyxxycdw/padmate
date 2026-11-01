# ADR-0002: Controller input: SDL3, hints, go-sdl3 patches, Joy-Cons and receiver heartbeats

[中文](0002-input-sdl.zh-CN.md)

Related: IN-01, IN-07, IN-09, IN-30; [PITFALLS "SDL and controllers", "go-sdl3 and purego"](../PITFALLS.md)

## Context

Controller input uses SDL 3.4.x, loaded at run time as SDL3.dll through go-sdl3 (purego, no cgo).

- Windows has no SDL of its own, and a user's machine may hold other versions that other programs brought. Background input, the hint set and device mappings change between versions, and a hardware test result holds for one version only.
- padmate works in the background: it must get input with no window and no focus.
- Some go-sdl3 bindings are missing, and some panic or give wrong results.
- When painting, the left hand often holds a single Joy-Con; when the other one wakes up, the one in hand must not be affected.
- A 2.4 GHz USB receiver of the Switch protocol keeps its controller listed in SDL after the controller turns off. SDL reports no removal, and a key held before the controller turned off stays held.

## Decision

1. **Ship a pinned SDL3.dll.** It sits next to the executables; its version and SHA-256 are pinned in `internal/sdlpin`, and `task sdl` downloads and checks it. It is not committed. The `binsdl` that comes with go-sdl3 is for development tests only. The installer must stop the daemon first, or the DLL is in use.
2. **Keep the default backend hints; do not override them.** `input.Open` always allows background events, turns off SDL's signal handlers (Ctrl+C belongs to `os/signal`) and keeps Joy-Cons apart. The backend hints are SDL 3.4.16's defaults (HIDAPI, XInput and DirectInput on). Tests showed background input works on the default path; with HIDAPI off, Switch-style controllers send garbage, and WGI has no background input. Other presets are for diagnosis only (`padmatectl monitor --preset`) and never go into config.json. A hint already set in the environment is respected.
3. **Fill the gaps of go-sdl3 with our own purego calls; do not fork it.** On Windows purego cannot return a struct by value, and go-sdl3 writes such results to an invalid address. The patches are in `internal/input/sdlext*.go`: they look the symbols up in the module go-sdl3 already loaded and are called on T-input only. What Go can do itself is not bound. Recheck them when go-sdl3 or SDL is upgraded, and go back to the direct call where upstream has fixed it.
4. **Joy-Cons stay apart and are held sideways.** Each Joy-Con is one controller. When they are combined, SDL replaces the device when the other one wakes up: held keys are released and the DeviceKey changes. There are no Joy-Con-only control names (SL/SR are standard buttons in SDL's mapping). To change this, use `--hint` or the environment.
5. **No ghost devices.** When a controller cannot be opened and `SDL_GetJoysticks` no longer lists it, nothing is reported.
6. **Detecting a lost receiver link (IN-30).** No SDL event proves that the controller behind a receiver still sends reports, so HID reports serve as a heartbeat:
   - Opt-in: only padmated's live mode turns it on (default 2 s); `monitor`, `record` and the tests show SDL as it is.
   - Only Pro-protocol USB receivers are watched. A second, read-only handle to the same path is opened with SDL's public HID API; reading it does not disturb SDL.
   - Only a complete report whose timer byte changes counts as a heartbeat. A device is "armed" only after it keeps sending idle heartbeats while at rest, so a device that reports only on change is never armed, and a button held still is never released by mistake.
   - When an armed device has been silent for the timeout, a DeviceRemoved is synthesized. Whether a control is held does not matter, since latches and scripts can hold keys with no control held.
   - The device becomes dormant, but its handle stays open (closing the gamepad would make SDL put the device back into simple mode). Its next button or axis event brings it back with a new DeviceID and the same DeviceKey, and the router treats it as a reconnect ([ADR: Several controllers](0003-devices-and-slots.md)).

## Costs

- Following SDL means bumping the version and hash ourselves and repeating the hardware tests. A new clone must run `task sdl` first.
- Using two Joy-Cons together needs a hint. Switch 2 Joy-Cons are not supported (IN-29).
- Receiver detection relies on how the real hardware behaves and on SDL 3.4.16 forcing complete reports over USB; upgrading SDL means checking again. It errs on the side of no stuck keys: a link lost for more than 2 s releases the keys and reports a disconnect.

## Alternatives given up

- **Use the system's SDL.** Windows has none; the version would be out of our control, and another program's copy could be loaded.
- **Link statically with cgo.** It needs a C toolchain to maintain.
- **Ship `binsdl`.** Its version does not go through our checks, and it writes a DLL at run time and then loads it, which antivirus programs readily flag.
- **Turn on RawInput or WGI by default, or use XInput only.** There is no reason to replace the defaults; XInput cannot read PlayStation or Switch controllers.
- **Make holding upright the default, or add a `--joycon` flag or `sl`/`sr` aliases.** The templates are designed for sideways holding. Which control SL stands for depends on left or right, the grip and whether they are combined; a fixed alias is wrong in some mode.
- **Receivers: use the connection or power state, a timeout on silent controls, the IMU, probing with `SDL_SendGamepadEffect`, or rebuilding the device with another hint.** The connection state is always WIRED; a silence timeout would hit a painter holding a button still; not every device has an IMU; probing would block T-input; changing a hint affects every Switch device.
