// Package input reads gamepads through SDL3 (IN-01, IN-02).
//
// Open starts T-input, the goroutine that owns SDL (ARCHITECTURE §5). It
// locks itself to an OS thread for its whole life, and on that thread it
// loads the SDL library (ResolveLibrary: on Windows the pinned SDL3.dll by
// absolute path, elsewhere the system's libSDL3), applies the hints, calls
// SDL_Init(SDL_INIT_GAMEPAD) and then pumps SDL events. The hints always
// include SDL_NO_SIGNAL_HANDLERS=1, so SIGINT and SIGTERM stay with
// os/signal instead of becoming SDL_EVENT_QUIT, and
// SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0, so every Joy-Con is its own
// gamepad (IN-07). Every SDL call of the package happens on that thread;
// other goroutines hand it commands through a channel, which it checks
// between short SDL_WaitEventTimeout calls. The go-sdl3 package also locks
// the main goroutine to the main thread in its init; nothing in padmate
// needs that, and it does not matter because SDL treats the thread that
// initialized its event subsystem as its main thread.
//
// T-input turns SDL gamepad events into Events, with padmate's own types
// and control names (ARCHITECTURE §7.1), and sends them on a bounded
// channel. It never blocks on a slow consumer: when the channel is full it
// drops a button or axis event and counts it, while a DeviceAdded or
// DeviceRemoved waits in line and follows in order, so that the consumer
// learns of every connection and disconnection.
//
// With Options.OnCombo, T-input also finds the pause combo (DMN-07): a
// ComboWatch sees every event before it is sent, dropped ones included,
// and tracks the combo members that each gamepad holds itself, without
// asking SDL. When a gamepad has held its DefaultCombo (back+start, or SL+SR
// on a single Joy-Con) for ComboHold, T-input calls OnCombo, then sends a
// ComboHeld, which waits in line like a device event. Traces never carry
// ComboHeld; ExpandCombos inserts it into a replay with the same watch.
//
// With Options.ReceiverTimeout, T-input also watches the receivers
// (IN-30, ADR-0029): a Switch Pro protocol gamepad behind a USB receiver,
// such as GameSir's Nova 2 Lite in Switch mode, stays listed by SDL when
// it is switched off, and SDL reports no removal. T-input reads the HID
// reports of such a device through a handle of its own; once it has seen
// the reports' timer move for a while with the controls at rest and the
// reports' input unchanged, so that the device is known to report while
// nothing changes, a silence of the timeout makes it report the device
// removed, held controls or not, so that its slot releases every key. The
// device's next SDL event brings it back as a new device, with a new
// DeviceID. T-input's log records go through a queue to a goroutine of
// the Source, so that a log that blocks never holds up T-input.
//
// T-input also drives the effects of the gamepads (M17, ADR-0065): rumble,
// the LED and the player lights. A haptic.Mixer, which T-input advances in
// each turn of its loop, decides which request owns them; SetFeedback (the
// layer's color and pulses), Play (a preset) and Identify (the cues that tell
// gamepads apart) are the ways to ask. Every effect ends when a gamepad is
// removed or reported removed and when the Source closes. See feedback.go.
//
// AttachVirtual adds an SDL virtual gamepad whose buttons and axes are set
// from Go. Its events take the same path as a real device's, so tests and
// "padmatectl monitor --virtual" exercise the whole backend without
// hardware. AttachVirtualDesc also sets its USB ids and its controls, to
// model what SDL reports for a device such as a Joy-Con.
//
// A DeviceID changes each time a device connects. DeviceKey and Registry
// (IN-09) give a gamepad an identity that stays when it reconnects,
// without SDL: KeyOf takes the base of the key from the DeviceInfo, and a
// Registry numbers identical gamepads. A Registry is used from one
// goroutine and keeps the keys in memory; persisting them is PRO-11.
//
// The package builds on every OS that go-sdl3 supports; the engine never
// imports it (CLAUDE.md invariant 2).
package input
