// What SDL 3.4.16 reports for the pads padmate supports: the type and the
// button and axis masks of each (DeviceInfo.buttonMask, axisMask), read from
// the mappings SDL_CreateMappingForHIDAPIGamepad builds in SDL_gamepad.c.
// Bit n of the button mask is SDL_GamepadButton n (internal/input/controls.go),
// bit n of the axis mask SDL_GamepadAxis n.

const buttonBits = [
  "south",
  "east",
  "west",
  "north",
  "back",
  "guide",
  "start",
  "leftstick",
  "rightstick",
  "leftshoulder",
  "rightshoulder",
  "dpad.up",
  "dpad.down",
  "dpad.left",
  "dpad.right",
  "misc1",
  "paddle1",
  "paddle2",
  "paddle3",
  "paddle4",
  "touchpad",
  "misc2",
];
const axisBits = [
  "leftx",
  "lefty",
  "rightx",
  "righty",
  "lefttrigger",
  "righttrigger",
];

const mask = (bits: string[], names: string[]) =>
  names.reduce((sum, name) => {
    const bit = bits.indexOf(name);
    if (bit < 0) throw new Error(`no such control: ${name}`);
    return sum | (1 << bit);
  }, 0);

/** The 15 buttons and 6 axes of SDL's standard gamepad. */
const standard = buttonBits.slice(0, 15);
const allAxes = axisBits;

const pad = (type: string, buttons: string[], axes: string[]) => ({
  type,
  buttonMask: mask(buttonBits, buttons),
  axisMask: mask(axisBits, axes),
});

export const sdlPads = {
  xboxSeries: pad("xboxone", [...standard, "misc1"], allAxes),
  xboxElite: pad(
    "xboxone",
    [...standard, "paddle1", "paddle2", "paddle3", "paddle4"],
    allAxes,
  ),
  ps4: pad("ps4", [...standard, "touchpad"], allAxes),
  ps5: pad("ps5", [...standard, "touchpad", "misc1"], allAxes),
  ps5Edge: pad(
    "ps5",
    [
      ...standard,
      "touchpad",
      "misc1",
      "paddle1",
      "paddle2",
      "paddle3",
      "paddle4",
    ],
    allAxes,
  ),
  switchPro: pad("switchpro", [...standard, "misc1"], allAxes),
  joyConPair: pad(
    "joyconpair",
    [...standard, "misc1", "paddle1", "paddle2", "paddle3", "paddle4"],
    allAxes,
  ),
  // A single Joy-Con is always held upright (SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1).
  joyConLeftUpright: pad(
    "joyconleft",
    [
      "back",
      "dpad.up",
      "dpad.down",
      "dpad.left",
      "dpad.right",
      "leftshoulder",
      "leftstick",
      "misc1",
      "paddle2",
      "paddle4",
    ],
    ["leftx", "lefty", "lefttrigger"],
  ),
  joyConRightUpright: pad(
    "joyconright",
    [
      "south",
      "east",
      "west",
      "north",
      "guide",
      "start",
      "rightshoulder",
      "rightstick",
      "paddle1",
      "paddle3",
    ],
    ["rightx", "righty", "righttrigger"],
  ),
} as const;

/** Nintendo's USB vendor id and the Joy-Con 2 products (jc2hub.go). */
const nintendo = 0x057e;
const joyCon2Product = {
  joyconleft: 0x2067,
  joyconright: 0x2066,
  joyconpair: 0x2068,
};

// A Joy-Con 2 as the daemon's Bluetooth LE driver describes it: the buttons
// of the Switch OLED Joy-Con in the same layout, and on the right one (and the
// pair) the C button, misc2 (jc2hub_test.go has the masks).
const joyCon2 = (
  source: (typeof sdlPads)[keyof typeof sdlPads],
  c: boolean,
) => ({
  type: source.type,
  vendor: nintendo,
  product: joyCon2Product[source.type as keyof typeof joyCon2Product],
  buttonMask: c
    ? source.buttonMask | (1 << buttonBits.indexOf("misc2"))
    : source.buttonMask,
  axisMask: source.axisMask,
});

/** The Joy-Con 2s, by the same names as the Switch OLED Joy-Cons. */
export const joyCon2Pads = {
  joyConLeftUpright: joyCon2(sdlPads.joyConLeftUpright, false),
  joyConRightUpright: joyCon2(sdlPads.joyConRightUpright, true),
  joyConPair: joyCon2(sdlPads.joyConPair, true),
} as const;
