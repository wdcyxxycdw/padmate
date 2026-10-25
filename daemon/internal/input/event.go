package input

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// DeviceID identifies a connected device: the SDL joystick instance id,
// or for a device that the receiver watchdog reported removed and that came
// back, a new id counted down from 2³²−1 (IN-30). It is unique while the
// device stays connected and is never reused within a process.
type DeviceID uint32

// DeviceInfo describes a connected device. Its JSON form is the "info"
// object of "padmatectl monitor --json" and of the spk01 log.
type DeviceInfo struct {
	ID DeviceID `json:"id"`
	// Name is SDL's name for the device, such as "Xbox Wireless
	// Controller".
	Name string `json:"name"`
	// Type is the SDL gamepad type as a short string, such as "xboxone" or
	// "ps5" (see TypeName). It is "unknown" for a device without a gamepad
	// mapping.
	Type string `json:"type"`
	// ButtonLabels are SDL's per-device printed face labels, indexed by
	// canonical physical control name. A value keeps DeviceInfo comparable.
	ButtonLabels FaceButtonLabels `json:"buttonLabels,omitzero"`
	// Vendor, Product and Version are the USB or Bluetooth vendor id,
	// product id and product version, or 0 when unknown.
	Vendor  uint16 `json:"vendor"`
	Product uint16 `json:"product"`
	Version uint16 `json:"version"`
	// GUID is SDL's joystick GUID as 32 lowercase hex digits, or "" when
	// it could not be read.
	GUID string `json:"guid"`
	// Driver is the SDL joystick backend that handles the device, decoded
	// from the GUID (see DriverName): "hidapi", "xinput", "rawinput",
	// "wgi", "gameinput", "virtual", ...
	Driver string `json:"driver"`
	// Path is the platform device path, or "" when SDL has none.
	Path string `json:"path"`
	// Serial is the serial number, or "" when the device reports none. It
	// is only read for gamepads, which T-input opens.
	Serial string `json:"serial"`
	// IsGamepad is false for a joystick without a gamepad mapping. Such
	// devices are reported but not read as a gamepad; their elements are the
	// raw stream (Source.RawStart, IN-17), and adding a mapping for their GUID
	// turns them into a gamepad, reported again with the same id (IN-18).
	IsGamepad bool `json:"gamepad"`
	// Virtual is true for an SDL virtual joystick, such as the one
	// AttachVirtual adds.
	Virtual bool `json:"virtual"`
	// SDL mapping capabilities, captured on T-input while the gamepad is
	// open. Zero masks in older traces mean that capability data is unknown.
	ButtonMask   uint32       `json:"buttonMask,omitempty"`
	AxisMask     uint8        `json:"axisMask,omitempty"`
	Capabilities Capabilities `json:"capabilities,omitzero"`
}

type FaceButtonLabels struct {
	South string `json:"south,omitempty"`
	East  string `json:"east,omitempty"`
	West  string `json:"west,omitempty"`
	North string `json:"north,omitempty"`
}

// Capabilities describes what the current SDL backend reports, not guarantees
// about the hardware or about other connections/backends of the same gamepad.
// Known is false for old traces, raw joysticks and gamepads that failed to open.
type Capabilities struct {
	Known         bool  `json:"known"`
	Gyro          bool  `json:"gyro"`
	Accelerometer bool  `json:"accelerometer"`
	Touchpads     int32 `json:"touchpads"`
	Rumble        bool  `json:"rumble"`
	TriggerRumble bool  `json:"triggerRumble"`
	LED           bool  `json:"led"`
	RGBLED        bool  `json:"rgbLed"`
	PlayerLED     bool  `json:"playerLed"`
}

// ControlsKnown distinguishes a device with no controls from an old recording
// that predates capability reporting. Nonzero legacy masks are also evidence.
func (d DeviceInfo) ControlsKnown() bool {
	return d.Capabilities.Known || d.ButtonMask != 0 || d.AxisMask != 0
}

// EventKind is the kind of an Event.
type EventKind uint8

// The event kinds.
const (
	DeviceAdded EventKind = iota + 1
	DeviceRemoved
	ButtonDown
	ButtonUp
	AxisMotion
	// ComboHeld is sent when a gamepad has held its combo (Combo) for the
	// hold time; Time is when the hold was reached. Only a Source with
	// Options.OnCombo sends it, and ExpandCombos inserts it for a replay;
	// traces never carry it (WriteEvent, ReadEvents).
	ComboHeld
	// DeviceState is a complete wake-time snapshot. It is never dropped
	// and is routing metadata, not an input trace record.
	DeviceState
	// DevicePower is the power of a gamepad (IN-12): its battery state and
	// charge, as of its first read and each change. Only a Source with
	// Options.PowerEvents sends it. Like DeviceState it is routing metadata,
	// not an input trace record: it is never written to or read from traces
	// (WriteEvent, ReadEvents), no slot sees it, and it is never dropped;
	// a newer one of the same device replaces one that still waits.
	DevicePower
	// DeviceRemapped is sent when SDL changed the mapping of a gamepad that
	// stays a gamepad (SDL_EVENT_GAMEPAD_REMAPPED; a mapping was added or reset,
	// IN-18). Info is the device as it is now: its button and axis masks may
	// have changed. SDL does not release what is held when it remaps, so the
	// consumer treats the event as a reset of the device: everything of it is
	// released, and a control that is held counts again only once it has been
	// released and pressed. Like DeviceState it is routing metadata, never
	// written to traces (WriteEvent) and never dropped; a newer one of the same
	// device replaces one that still waits.
	DeviceRemapped
)

var eventKindNames = [...]string{
	DeviceAdded:    "added",
	DeviceRemoved:  "removed",
	ButtonDown:     "down",
	ButtonUp:       "up",
	AxisMotion:     "axis",
	ComboHeld:      "combo",
	DeviceState:    "state",
	DevicePower:    "power",
	DeviceRemapped: "remapped",
}

// String returns "added", "removed", "down", "up", "axis", "combo", "state",
// "power" or "remapped".
func (k EventKind) String() string {
	if int(k) < len(eventKindNames) && eventKindNames[k] != "" {
		return eventKindNames[k]
	}
	return fmt.Sprintf("kind%d", uint8(k))
}

// Event is one input event of a device.
type Event struct {
	// Time is SDL's event timestamp: nanoseconds since the Source was
	// opened (SDL_GetTicksNS).
	Time uint64
	// Stamp is latency.Now as T-input read it right after SDL returned the
	// event, for ButtonDown, ButtonUp and AxisMotion of a Source and of
	// padmated's fake input; 0 for all other events and for events that come
	// from a trace. It is the start of the latency statistics (DMN-15,
	// ARCHITECTURE §13). It is not written to traces (WriteEvent).
	Stamp  latency.Stamp `json:"-"`
	Device DeviceID
	Kind   EventKind
	// Control is the button or axis name (see ButtonName and AxisName),
	// for ButtonDown, ButtonUp and AxisMotion.
	Control string
	// Value is the axis position for AxisMotion: -32768..32767 for the
	// sticks, 0..32767 for the triggers.
	Value int16
	// Info describes the device, for DeviceAdded and DeviceRemapped.
	Info *DeviceInfo
	// AssignedKey is routing metadata attached by supervisor to an assigned
	// DeviceAdded. It is not serialized in input traces.
	AssignedKey string `json:"-"`
	// Snapshot is the complete state for DeviceState, detached from SDL.
	Snapshot *GamepadState
	// Power is the power of the gamepad, for DevicePower.
	Power *Power
	// publication is an internal backlog fence, never sent to Events.
	publication *publication
}

// GamepadState is the state read during a resume scan. Once published it
// is immutable; the consumer copies it into its own device state.
type GamepadState struct {
	Buttons []string
	Axes    map[string]int16
	// Dropped is the Source drop counter covered by this complete sample.
	Dropped uint64
}
