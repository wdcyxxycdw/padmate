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
  // A single Joy-Con held sideways ("mini gamepad mode", the default).
  joyConLeftSideways: pad(
    "joyconleft",
    [
      "south",
      "east",
      "west",
      "north",
      "guide",
      "start",
      "leftstick",
      "leftshoulder",
      "rightshoulder",
      "paddle2",
      "paddle4",
    ],
    ["leftx", "lefty"],
  ),
  joyConRightSideways: pad(
    "joyconright",
    [
      "south",
      "east",
      "west",
      "north",
      "guide",
      "start",
      "leftstick",
      "leftshoulder",
      "rightshoulder",
      "paddle1",
      "paddle3",
    ],
    ["leftx", "lefty"],
  ),
  // Held upright (SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1).
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
