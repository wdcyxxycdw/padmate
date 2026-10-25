package diagnostics

// SteamClass is the kind of gamepad setting in Steam (Settings, Controller)
// that may govern a gamepad: Steam Input is switched on or off for a class of
// gamepads, not for one (IN-24, ADR-0071).
//
// padmate cannot tell that Steam really took a gamepad over. SDL hides
// Steam's virtual gamepad on every Windows backend, the virtual gamepad exists
// in the processes Steam starts only, and Steam Input reads the real gamepad
// beside padmate (hardware matrix, ADR-0071). Reading Steam's own files would
// read another program's internal state, which padmate does not do. So the
// class says which setting could affect the gamepad, and the notice says
// "may".
type SteamClass string

// The classes, as the words of the conflicts of status.get.
const (
	// SteamNone is a gamepad that no class governs, or one padmate cannot
	// classify.
	SteamNone SteamClass = "none"
	// SteamXbox is Steam Input for Xbox controllers, and the XInput gamepads
	// that Steam's setting treats alike.
	SteamXbox SteamClass = "xbox"
	// SteamPlayStation is Steam Input for PlayStation controllers.
	SteamPlayStation SteamClass = "playstation"
	// SteamSwitch is Steam Input for Switch controllers: the Pro Controller
	// and the Joy-Cons.
	SteamSwitch SteamClass = "switch"
	// SteamHardware is Valve's own hardware (vendor 0x28DE), which Steam
	// Input always manages.
	SteamHardware SteamClass = "steamHardware"
)

// steamClassOrder is the order SteamClasses lists the classes in.
var steamClassOrder = [...]SteamClass{SteamXbox, SteamPlayStation, SteamSwitch, SteamHardware}

const (
	vendorValve = 0x28de
	// productSteamVirtualGamepad is the product of the gamepad Steam emulates
	// (usb_ids.h of SDL).
	productSteamVirtualGamepad = 0x11ff
)

// SteamPad is what the classification of a gamepad reads of SDL's
// description of it (input.DeviceInfo): Type is SDL's gamepad type name,
// such as "xboxone" or "ps5", Driver the SDL backend, such as "xinput".
type SteamPad struct {
	Type    string
	Vendor  uint16
	Product uint16
	Driver  string
	// Virtual is an SDL virtual joystick, which belongs to the process that
	// made it.
	Virtual bool
}

// ClassifySteamInput returns the class of Steam Input settings that may
// govern p. Valve's vendor id wins over the type. Xbox 360 and Xbox One
// gamepads and any gamepad on XInput are Xbox; PS3, PS4 and PS5 are
// PlayStation; the Switch Pro Controller and the Joy-Cons are Switch.
// A generic gamepad on another backend, a GameCube adapter and the
// unknown are SteamNone: Steam has settings for them that padmate does not
// name. Steam's virtual gamepad itself is SteamNone, as it is what Steam
// made, not a gamepad it can take over.
func ClassifySteamInput(p SteamPad) SteamClass {
	switch {
	case p.Virtual:
		return SteamNone
	case p.Vendor == vendorValve && p.Product == productSteamVirtualGamepad:
		return SteamNone
	case p.Vendor == vendorValve:
		return SteamHardware
	}
	switch p.Type {
	case "xbox360", "xboxone":
		return SteamXbox
	case "ps3", "ps4", "ps5":
		return SteamPlayStation
	case "switchpro", "joyconleft", "joyconright", "joyconpair":
		return SteamSwitch
	}
	if p.Driver == "xinput" {
		return SteamXbox
	}
	return SteamNone
}

// SteamClasses returns the classes of pads, each once, in the order xbox,
// playstation, switch, steamHardware; a gamepad that no class governs adds
// none. It returns nil when there is no class.
func SteamClasses(pads []SteamPad) []string {
	var seen [len(steamClassOrder)]bool
	for _, p := range pads {
		for i, c := range steamClassOrder {
			if ClassifySteamInput(p) == c {
				seen[i] = true
			}
		}
	}
	var out []string
	for i, c := range steamClassOrder {
		if seen[i] {
			out = append(out, string(c))
		}
	}
	return out
}
