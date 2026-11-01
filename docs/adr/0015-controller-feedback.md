# ADR-0015: Controller feedback

[中文](0015-controller-feedback.zh-CN.md)

Related: [ARCHITECTURE](../ARCHITECTURE.md); [ADR: Scripting runtime](0010-scripting-runtime.md); [ADR: Input](0002-input-sdl.md)

## Context

A controller can serve as a status indicator: pulses and colors for layers and pause, a haptic touch when a binding fires, knob ticks, player lights, identifying "which one is it", low-battery warnings. All of these write to the same controller, and they compete: a tick should not cut the three pulses of "switched to layer 3" down to one; a script's effect should not outlive the script; and when a 2.4 GHz receiver does not take output reports, one blocking write should not hold up the input of every controller.

## Decision

### 1. A mixer arbitrates everything
- All feedback goes through the mixer of `internal/haptic` (a pure package, one per input source); callers do not schedule it each on their own. This gives one place that guarantees nothing is left on at exit.
- Rumble is arbitrated by priority class: Identify, Alert, Ambient (layers and pause), Script, Event (bindings and the knob). A new request preempts only if its class is not lower than the one running; otherwise it is dropped. The Event class is rate-limited per device, because the Switch driver merges rumble writes.
- The LED and the player lights have one claim per class; the strongest is shown, and when a timed pattern ends, the one below shows through. SDL has no call to "give the light back to the controller", so when no claim is left, the color LED is set to black.
- Error cool-downs are per group (rumble, LED, player lights); one failure stops only that group.
- **For invariant 1:** `Cancel`, `Stop` and `StopAll` end with zero rumble, the LED released and the player lights restored. Every exit path other than a lost link, closing and pause reaches one of them.
- A call the device cannot handle is not sent, and is not an error. Posting to the effect queue never blocks; when it is full, the oldest one-off effect is dropped, while ambient state and cancellations never are.

### 2. Haptic presets
- The preset names have one source, shared by the profile schema, the engine and the script API. The strength is `feedback.strength`; 0 means off.
- A haptic touch is a member of the binding, not of the action: the intent is "this button feels like something when pressed". The engine calls the callback once at the single dispatch point of declarative gestures; gestures that a script takes over, and anything during pause, make no touch. Knob ticks held back by the rate limit make none either.

### 3. Identifying a controller
Identifying is an explicit act of the user: it ignores every switch and pause, works on controllers that are not mapped, and clears error cool-downs to try again. The daemon requires an explicit `device`; the command line chooses for the user when only one is connected. It does not go through the effect queue, since it must return an answer (whether rumble, the LED and the player lights each started).

The player lights show the mode number of the current layer, not the slot number, because the tray badge and the layer pulses speak of the mode too.

### 4. Four global feedback switches
- Rumble, lights, the pause cue and the low-battery warning are stored in config.json's `feedback`, all on by default. They take effect at once, without reloading the profile or releasing held keys. When a switch goes from on to off, whatever it governs that is still running is stopped.
- These switches are about the controller and the user, not about the mapping of one app, so they are not per profile. Members of the same name in a profile are kept and marked `deprecated`: parsed, ignored, not migrated. A profile's `feedback` keeps only timing, strength and the pause color.

### 5. Battery and low-battery warnings
- The battery is dynamic data and is not part of the device info. It is read when a controller joins, when a receiver comes back, and on SDL's battery events, plus every 30 s as a fallback. Only a change sends a `DevicePower` event, and it does not go to slots, because a slot that falls behind drops non-critical edges.
- XInput's charging is recorded as `wired`: SDL reports XInput's wired state as charging at 100%, and the two cannot be told apart. `unknown` never warns and changes no state.
- The thresholds are fixed, not configurable: discharging at 0 to 25% warns; charging, plugging in or reaching 60% re-arms. The basis is the drivers' granularity (XInput has only four steps: 10, 40, 70 and 100); 60 is above the next step after the warning, so a voltage that bounces back one step does not warn again and again.
- The state is kept by DeviceKey, so a receiver coming back or a profile switch does not warn again. An episode that starts while the switch is off counts too; turning it on later does not send the warning after the fact.
- There are three channels: the controller's own Alert class, a tray balloon and an overlay notice. None takes focus, since the right hand is on the tablet while painting, and a blocking dialog is out of the question.

## Costs

- Receivers often drop all output reports, so rumble and lights do not come out at all; this is not a defect of padmate, and there is no way around it. The effect on real hardware has to be checked.
- A controller behind a receiver has no reliable battery reading; nothing is shown or warned about.

## Alternatives given up

- **Events before layer pulses.** Users would count the layer wrong.
- **A global cool-down.** One controller with a problem should not silence the others.
- **A strength or switch of their own for haptic touches.** `strength` and `rumble` do it.
- **Feedback switches per profile.** Old values left behind would make the global switch seem "stuck off"; a migration could contradict them, and the daemon does not rewrite users' files.
- **Turning XInput's charging into unknown.** unknown does not re-arm; wired is what says "this reading is not a battery level".
- **Opening a second HID handle of our own to read the battery.** SDL already parses it, and an extra handle on a USB receiver is a risk.
- **The daemon picks the only controller when no `device` is given.** The same request should not mean different things with one controller and with two.
