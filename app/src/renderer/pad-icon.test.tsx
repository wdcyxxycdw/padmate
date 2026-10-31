// @vitest-environment jsdom
// The vendored pad icons (ADR-0079 §7): which icon each control of each
// family gets, that every one of them is vendored and used, and how they
// are painted.
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { controllerLabels, familyName } from "./controller-model";
import { stepInfo } from "./mapping-model";
import {
  PadIcon,
  padIcon,
  padIcons,
  parseIcon,
  stickMoveIcon,
} from "./pad-icon";

afterEach(cleanup);

const here = join(dirname(fileURLToPath(import.meta.url)), "pad-icons");

const icon = (control: string, type: string, buttonMask?: number) =>
  padIcon(control, familyName(type), controllerLabels(type, {}, buttonMask));
const name = (control: string, type: string, buttonMask?: number) =>
  icon(control, type, buttonMask)?.name;

const types = [
  "xbox360",
  "xboxone",
  "xboxseries",
  "ps3",
  "ps4",
  "ps5",
  "switchpro",
  "joyconleft",
  "joyconright",
  "joyconpair",
  "gamecube",
  "unknown",
];
// A single Joy-Con held upright (2) or sideways (0xf), and the others.
const masks = [undefined, 2, 0xf];
const controls = [
  "south",
  "east",
  "west",
  "north",
  ...["up", "down", "left", "right", "upleft", "downright"].map(
    (d) => `dpad.${d}`,
  ),
  "leftstick",
  "rightstick",
  ...["ls", "rs"].flatMap((s) =>
    [
      "up",
      "down",
      "left",
      "right",
      "upleft",
      "active",
      "cw",
      "ccw",
      "inner",
    ].map((d) => `${s}.${d}`),
  ),
  ...["left", "right"].flatMap((s) =>
    ["x-", "x+", "y-", "y+"].map((a) => `${s}${a}`),
  ),
  "leftshoulder",
  "rightshoulder",
  "lefttrigger",
  "righttrigger",
  "lt",
  "rt",
  "lt.full",
  "lefttrigger+",
  "paddle1",
  "paddle2",
  "paddle3",
  "paddle4",
  "back",
  "start",
  "guide",
  "misc1",
  "misc2",
  "touchpad",
];

describe("the vendored icons", () => {
  it("are SVGs of a view box and paths only, each loaded", () => {
    const files = ["kenney", "promptfont"].flatMap((set) =>
      readdirSync(join(here, set))
        .filter((file) => file.endsWith(".svg"))
        .map((file) => `${set}/${file.replace(/\.svg$/, "")}`),
    );
    expect(Object.keys(padIcons).sort()).toEqual(files.sort());
    for (const file of files) {
      const svg = readFileSync(join(here, `${file}.svg`), "utf8");
      // Nothing that parseIcon would drop: no groups, text, styles or
      // shapes other than paths.
      const tags = [...svg.matchAll(/<([a-z]+)\b/g)].map((m) => m[1]);
      expect(new Set(tags), file).toEqual(new Set(["svg", "path"]));
      const data = padIcons[file];
      expect(data.viewBox.split(" ").map(Number), file).toHaveLength(4);
      expect(data.paths.length, file).toBeGreaterThan(0);
      for (const path of data.paths) expect(path.d, file).not.toBe("");
    }
  });

  it("each keep their license next to them", () => {
    expect(readFileSync(join(here, "kenney", "License.txt"), "utf8")).toMatch(
      /Creative Commons Zero, CC0/,
    );
    // The OFL goes with its copyright notice (the font's own Copyright field).
    const ofl = readFileSync(join(here, "promptfont", "LICENSE.txt"), "utf8");
    expect(ofl).toMatch(/^Copyright 2018-2023, Yukari Hafner\r?\n/);
    expect(ofl).toMatch(/SIL OPEN FONT LICENSE Version 1\.1/);
  });

  it("paint white and black with the colour around them, and Kenney's red as the highlight", () => {
    const data = parseIcon(
      `<svg viewBox="0 0 64 64"><path fill="#FFFFFF" d="M0 0"/><path fill="#000000" fill-rule="evenodd" d="M1 1"/><path fill="#E73246" transform="scale(2)" d="M2 2"/></svg>`,
    );
    expect(data).toEqual({
      viewBox: "0 0 64 64",
      paths: [
        { d: "M0 0", fill: "currentColor" },
        { d: "M1 1", fill: "currentColor", fillRule: "evenodd" },
        {
          d: "M2 2",
          fill: "var(--pad-icon-highlight, currentColor)",
          transform: "scale(2)",
        },
      ],
    });
  });

  it("are all named by some control of some pad, or by the wizard", () => {
    const used = new Set<string>();
    for (const type of types)
      for (const mask of masks)
        for (const control of controls) {
          const found = name(control, type, mask);
          if (found) used.add(found);
        }
    for (const side of ["l", "r"] as const) used.add(stickMoveIcon(side).name);
    for (const step of [
      "a",
      "dpup",
      "ls-left",
      "ls-click",
      "lb",
      "lt",
      "view",
      "menu",
      "home",
    ]) {
      const spec = stepInfo(step, "en").icon;
      if (spec.kind === "art") used.add(spec.name);
    }
    for (const found of used) expect(padIcons, found).toHaveProperty([found]);
    expect([...used].sort()).toEqual(Object.keys(padIcons).sort());
  });
});

describe("the icon of each control", () => {
  it("draws face buttons as each family prints them, Xbox's and PlayStation's in colour", () => {
    expect(icon("south", "xboxone")).toEqual({
      name: "kenney/xbox_button_a",
      face: "green",
    });
    expect(icon("east", "xbox360")).toEqual({
      name: "kenney/xbox_button_b",
      face: "red",
    });
    expect(icon("west", "xboxseries")).toEqual({
      name: "kenney/xbox_button_x",
      face: "blue",
    });
    expect(icon("north", "xboxone")).toEqual({
      name: "kenney/xbox_button_y",
      face: "yellow",
    });
    expect(icon("south", "ps5")).toEqual({
      name: "kenney/playstation_button_cross",
      face: "blue",
    });
    expect(icon("east", "ps4")).toEqual({
      name: "kenney/playstation_button_circle",
      face: "red",
    });
    expect(icon("west", "ps3")).toEqual({
      name: "kenney/playstation_button_square",
      face: "pink",
    });
    expect(icon("north", "ps5")).toEqual({
      name: "kenney/playstation_button_triangle",
      face: "green",
    });
    // Nintendo: the letters where Nintendo puts them, one colour.
    expect(icon("south", "switchpro")).toEqual({
      name: "kenney/switch_button_b",
    });
    expect(icon("east", "switchpro")).toEqual({
      name: "kenney/switch_button_a",
    });
    expect(name("west", "switchpro")).toBe("kenney/switch_button_y");
    expect(name("north", "gamecube")).toBe("kenney/switch_button_x");
    // A pad that reports letters but is no Xbox pad: the letters, uncoloured.
    expect(
      padIcon("south", "generic", controllerLabels("unknown", { south: "A" })),
    ).toEqual({ name: "kenney/xbox_button_a" });
    // A single Joy-Con, or a pad without letters: where it is among the four.
    // SDL names a single Joy-Con's buttons B/A/Y/X, which are not what is
    // printed where they are when it is held sideways (a left one has
    // arrows): it is drawn by position all the same. A pair keeps the letters.
    const joyCon = { south: "B", east: "A", west: "Y", north: "X" };
    for (const type of ["joyconleft", "joyconright"])
      expect(
        padIcon("south", "nintendo", controllerLabels(type, joyCon, 0xf))?.name,
        type,
      ).toBe("kenney/switch_buttons_down");
    expect(
      padIcon("east", "nintendo", controllerLabels("joyconpair", joyCon))?.name,
    ).toBe("kenney/switch_button_a");
    expect(name("west", "joyconleft", 0xf)).toBe("kenney/switch_buttons_left");
    expect(name("south", "joyconright", 0xf)).toBe(
      "kenney/switch_buttons_down",
    );
    expect(name("north", "unknown")).toBe("kenney/switch_buttons_up");
    expect(name("east", "unknown")).toBe("kenney/switch_buttons_right");
  });

  it("draws the d-pad of each family with the pressed arm lit", () => {
    expect(name("dpad.up", "xboxone")).toBe("kenney/xbox_dpad_up");
    expect(name("dpad.left", "unknown")).toBe("kenney/xbox_dpad_left");
    expect(name("dpad.down", "ps4")).toBe("kenney/playstation_dpad_down");
    expect(name("dpad.right", "switchpro")).toBe("kenney/switch_dpad_right");
    expect(name("dpad.up", "joyconpair")).toBe("kenney/switch_dpad_up");
  });

  it("draws a stick's click with the arrow pressing it, and its pushes and turns", () => {
    expect(name("leftstick", "xboxone")).toBe("kenney/xbox_stick_l_press");
    expect(name("rightstick", "ps5")).toBe("kenney/playstation_stick_r_press");
    expect(name("leftstick", "switchpro")).toBe("kenney/switch_stick_l_press");
    expect(name("rightstick", "unknown")).toBe("kenney/xbox_stick_r_press");
    expect(stickMoveIcon("l")).toEqual({ name: "promptfont/analog-l-cl" });
    expect(stickMoveIcon("r")).toEqual({ name: "promptfont/analog-r-cl" });
    expect(name("ls.up", "xboxone")).toBe("kenney/xbox_stick_l_up");
    expect(name("rs.left", "ps4")).toBe("kenney/playstation_stick_r_left");
    expect(name("ls.down", "switchpro")).toBe("kenney/switch_stick_l_down");
    expect(name("ls.active", "ps5")).toBe("promptfont/analog-l-lrud");
    expect(name("rs.cw", "xboxone")).toBe("promptfont/analog-r-cl");
    expect(name("rs.ccw", "xboxone")).toBe("promptfont/analog-r-co");
    // Raw half axes: y grows downwards.
    expect(name("lefty-", "xboxone")).toBe("kenney/xbox_stick_l_up");
    expect(name("righty+", "xboxone")).toBe("kenney/xbox_stick_r_down");
    expect(name("leftx-", "ps5")).toBe("kenney/playstation_stick_l_left");
    expect(name("rightx+", "switchpro")).toBe("kenney/switch_stick_r_right");
  });

  it("draws shoulders, triggers and back buttons by the name printed on them", () => {
    expect(name("leftshoulder", "xboxone")).toBe("kenney/xbox_lb");
    expect(name("rightshoulder", "ps5")).toBe("kenney/playstation_trigger_r1");
    expect(name("leftshoulder", "switchpro")).toBe("kenney/switch_button_l");
    expect(name("leftshoulder", "unknown")).toBe("kenney/switch_button_l");
    expect(name("rightshoulder", "joyconright", 0xf)).toBe(
      "kenney/switch_button_sr",
    );
    expect(name("lefttrigger", "xboxone")).toBe("kenney/xbox_lt");
    expect(name("rt", "ps5")).toBe("kenney/playstation_trigger_r2");
    expect(name("lt", "switchpro")).toBe("kenney/switch_button_zl");
    expect(name("lt.full", "xboxone")).toBe("kenney/xbox_lt");
    expect(name("lefttrigger+", "ps4")).toBe("kenney/playstation_trigger_l2");
    expect(name("rt", "unknown")).toBe("kenney/xbox_rt");
    // Xbox Elite's paddles: 背键 1 and 2 under the right hand, 3 and 4 under
    // the left, the upper ones first.
    expect(name("paddle1", "xboxone")).toBe(
      "kenney/xbox_elite_paddle_top_right",
    );
    expect(name("paddle3", "xboxone")).toBe(
      "kenney/xbox_elite_paddle_bottom_right",
    );
    expect(name("paddle2", "xboxone")).toBe(
      "kenney/xbox_elite_paddle_top_left",
    );
    expect(name("paddle4", "unknown")).toBe(
      "kenney/xbox_elite_paddle_bottom_left",
    );
    // DualSense Edge: the back buttons and the Fn buttons.
    expect(name("paddle1", "ps5")).toBe("kenney/playstation5_elite_rb");
    expect(name("paddle2", "ps5")).toBe("kenney/playstation5_elite_lb");
    expect(name("paddle3", "ps5")).toBe("kenney/playstation5_elite_fn_r");
    expect(name("paddle4", "ps5")).toBe("kenney/playstation5_elite_fn_l");
    // A single Joy-Con: SL and SR upright, its L and ZL (R and ZR) sideways.
    expect(name("paddle2", "joyconleft", 2)).toBe("kenney/switch_button_sl");
    expect(name("paddle1", "joyconright", 2)).toBe("kenney/switch_button_sr");
    expect(name("paddle2", "joyconleft", 0xf)).toBe("kenney/switch_button_l");
    expect(name("paddle4", "joyconleft", 0xf)).toBe("kenney/switch_button_zl");
    expect(name("paddle3", "joyconright", 0xf)).toBe("kenney/switch_button_zr");
  });

  it("draws the system buttons with the symbol printed on them", () => {
    // Xbox One and Series: View, Menu, the Xbox button and Share.
    expect(name("back", "xboxone")).toBe("kenney/xbox_button_view");
    expect(name("start", "xboxseries")).toBe("kenney/xbox_button_menu");
    expect(name("guide", "xboxone")).toBe("kenney/xbox_guide");
    expect(name("misc1", "xboxone")).toBe("kenney/xbox_button_share");
    // Xbox 360: the arrows of Back and Start.
    expect(name("back", "xbox360")).toBe("kenney/xbox_button_back_icon");
    expect(name("start", "xbox360")).toBe("kenney/xbox_button_start_icon");
    // PlayStation: each generation's own; the PS button is Home.
    expect(name("back", "ps3")).toBe("kenney/playstation3_button_select");
    expect(name("start", "ps3")).toBe("kenney/playstation3_button_start");
    expect(name("back", "ps4")).toBe("kenney/playstation4_button_share");
    expect(name("start", "ps4")).toBe("kenney/playstation4_button_options");
    expect(name("touchpad", "ps4")).toBe("kenney/playstation4_touchpad");
    expect(name("back", "ps5")).toBe("kenney/playstation5_button_create");
    expect(name("start", "ps5")).toBe("kenney/playstation5_button_options");
    expect(name("touchpad", "ps5")).toBe("kenney/playstation5_touchpad");
    expect(name("misc1", "ps5")).toBe("kenney/playstation5_button_mute");
    expect(name("guide", "ps5")).toBe("kenney/switch_button_home");
    // Nintendo: −, + and Home.
    expect(name("back", "switchpro")).toBe("kenney/switch_button_minus");
    expect(name("start", "switchpro")).toBe("kenney/switch_button_plus");
    expect(name("guide", "switchpro")).toBe("kenney/switch_button_home");
    expect(name("start", "joyconleft", 0xf)).toBe("kenney/switch_button_minus");
    expect(name("guide", "joyconright", 0xf)).toBe("kenney/switch_button_home");
    // Any other pad: Back, Start and Home.
    expect(name("back", "unknown")).toBe("kenney/xbox_button_back_icon");
    expect(name("start", "unknown")).toBe("kenney/xbox_button_start_icon");
    expect(name("guide", "unknown")).toBe("kenney/switch_button_home");
  });

  it("has none where neither set draws the control", () => {
    for (const [control, type, mask] of [
      // Diagonals and the stages of a push.
      ["dpad.upleft", "xboxone"],
      ["ls.upleft", "ps5"],
      ["rs.inner", "xboxone"],
      // Capture, and the extra buttons that only have a number.
      ["misc1", "switchpro"],
      ["guide", "joyconleft", 0xf],
      ["misc2", "xboxone"],
      // A pair of Joy-Cons has an SL and an SR on each side.
      ["paddle1", "joyconpair"],
      // A touchpad that is not a PlayStation's.
      ["touchpad", "unknown"],
      ["bogus", "xboxone"],
    ] as const)
      expect(icon(control, type, mask), `${control} ${type}`).toBeUndefined();
    // A name SDL reports that neither set prints stays a word.
    expect(padIcon("start", "generic", { start: "Plus Menu" })).toBeUndefined();
    expect(
      padIcon("leftshoulder", "generic", { leftshoulder: "LB2" }),
    ).toBeUndefined();
  });
});

describe("PadIcon", () => {
  it("draws the paths in the colours around it, hidden from screen readers", () => {
    const { container } = render(<PadIcon name="kenney/xbox_dpad_up" />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("data-pad-icon")).toBe("kenney/xbox_dpad_up");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 64 64");
    const fills = [...container.querySelectorAll("path")].map((path) =>
      path.getAttribute("fill"),
    );
    expect(new Set(fills)).toEqual(
      new Set(["currentColor", "var(--pad-icon-highlight, currentColor)"]),
    );
    expect(container.textContent).toBe("");
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,6}\b|rgb/i);
  });

  it("draws nothing for a name that is not vendored", () => {
    const { container } = render(<PadIcon name="kenney/nothing" />);
    expect(container.innerHTML).toBe("");
  });
});
