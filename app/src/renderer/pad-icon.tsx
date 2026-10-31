// The icons of the pad's controls (ADR-0079 §7): Kenney Input Prompts 1.5a
// (CC0) for the buttons, d-pads, triggers and stick clicks, in the set of
// the pad's family, and PromptFont (SIL OFL 1.1) for a stick's movement and
// "any direction", which Kenney does not draw. The SVGs are vendored in
// pad-icons/ (its README has the sources). White and black paint takes the
// colour of the text around the icon, Kenney's red highlight the accent, so
// the icons follow the theme and the face colours.
import type { ControllerFamily } from "./controller-model";

const sources = import.meta.glob<string>("./pad-icons/*/*.svg", {
  query: "?raw",
  import: "default",
  eager: true,
});

interface IconPath {
  d: string;
  fill: string;
  fillRule?: "evenodd";
  transform?: string;
}

interface IconData {
  viewBox: string;
  paths: IconPath[];
}

const attribute = (tag: string, name: string) =>
  new RegExp(`\\s${name}="([^"]*)"`).exec(tag)?.[1];

/** Paint that follows the surroundings; any other colour is kept. */
function paint(fill: string): string {
  const color = fill.toUpperCase();
  if (color === "#FFFFFF" || color === "#000000") return "currentColor";
  if (color === "#E73246") return "var(--pad-icon-highlight, currentColor)";
  return fill;
}

/** A vendored SVG: its viewBox and paths (pad-icons/ has nothing else). */
export function parseIcon(svg: string): IconData {
  const root = /<svg\b[^>]*>/.exec(svg)?.[0] ?? "";
  const paths = [...svg.matchAll(/<path\b[^>]*\/>/g)].map(([tag]) => {
    const fillRule = attribute(tag, "fill-rule");
    const transform = attribute(tag, "transform");
    return {
      d: attribute(tag, "d") ?? "",
      fill: paint(attribute(tag, "fill") ?? "#000000"),
      ...(fillRule === "evenodd" ? { fillRule } : {}),
      ...(transform ? { transform } : {}),
    } satisfies IconPath;
  });
  return { viewBox: attribute(root, "viewBox") ?? "0 0 64 64", paths };
}

/** Every vendored icon by name, e.g. "kenney/xbox_lb", "promptfont/analog-l-cl". */
export const padIcons: Readonly<Record<string, IconData>> = Object.fromEntries(
  Object.entries(sources).map(([path, svg]) => [
    path.replace(/^\.\/pad-icons\//, "").replace(/\.svg$/, ""),
    parseIcon(svg),
  ]),
);

/** The name of a vendored icon (a key of padIcons). */
export type PadIconName = string;

/** Printed face colours (the same names as the chips' data-face). */
export type PadFace = "green" | "red" | "blue" | "yellow" | "pink";

export interface PadIconSpec {
  name: PadIconName;
  face?: PadFace;
}

type Side = "l" | "r";
type Direction = "up" | "down" | "left" | "right";

const xboxFaces: Record<string, [string, PadFace]> = {
  A: ["a", "green"],
  B: ["b", "red"],
  X: ["x", "blue"],
  Y: ["y", "yellow"],
};
const playstationFaces: Record<string, [string, PadFace]> = {
  "×": ["cross", "blue"],
  "○": ["circle", "red"],
  "□": ["square", "pink"],
  "△": ["triangle", "green"],
};
// Where a face button is among the four, for a pad that prints nothing.
const facePositions: Record<string, Direction> = {
  south: "down",
  east: "right",
  west: "left",
  north: "up",
};
const shoulders: Record<string, string> = {
  LB: "xbox_lb",
  RB: "xbox_rb",
  L1: "playstation_trigger_l1",
  R1: "playstation_trigger_r1",
  L: "switch_button_l",
  R: "switch_button_r",
  SL: "switch_button_sl",
  SR: "switch_button_sr",
};
const triggers: Record<string, string> = {
  LT: "xbox_lt",
  RT: "xbox_rt",
  L2: "playstation_trigger_l2",
  R2: "playstation_trigger_r2",
  ZL: "switch_button_zl",
  ZR: "switch_button_zr",
};
// The back paddles by the name controllerLabels gives them. A pair of
// Joy-Cons has an SL and an SR on each side, which one icon cannot tell
// apart, so their names stay text.
const paddles: Record<string, string> = {
  "背键 1": "xbox_elite_paddle_top_right",
  "背键 2": "xbox_elite_paddle_bottom_right",
  "背键 3": "xbox_elite_paddle_top_left",
  "背键 4": "xbox_elite_paddle_bottom_left",
  右背键: "playstation5_elite_rb",
  左背键: "playstation5_elite_lb",
  "右 Fn": "playstation5_elite_fn_r",
  "左 Fn": "playstation5_elite_fn_l",
  ...shoulders,
  ...triggers,
};
const axisDirections: Record<string, Direction> = {
  "x-": "left",
  "x+": "right",
  "y-": "up",
  "y+": "down",
};

function own<T>(record: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(record, key) ? record[key] : undefined;
}

const kenney = (name: string): PadIconSpec => ({ name: `kenney/${name}` });

/** The Kenney set a family's sticks and d-pad are drawn from. */
function padSet(family: ControllerFamily): string {
  return family === "playstation"
    ? "playstation"
    : family === "nintendo"
      ? "switch"
      : "xbox";
}

/** A stick's movement: the stick turning both ways (PromptFont). */
export function stickMoveIcon(side: Side): PadIconSpec {
  return { name: `promptfont/analog-${side}-cl` };
}

/** A stick pushed in a direction or any ("active"), or turned (a ring tick). */
function stickIcon(
  side: Side,
  direction: string | undefined,
  family: ControllerFamily,
): PadIconSpec | undefined {
  if (direction === "active") return { name: `promptfont/analog-${side}-lrud` };
  if (direction === "cw") return { name: `promptfont/analog-${side}-cl` };
  if (direction === "ccw") return { name: `promptfont/analog-${side}-co` };
  if (direction && ["up", "down", "left", "right"].includes(direction))
    return kenney(`${padSet(family)}_stick_${side}_${direction}`);
  return undefined;
}

// A system button by its printed name. PlayStation's PS button is drawn as
// Home (its logo is not drawn), and the Options and touchpad of a DualShock 4
// differ from a DualSense's: a DualShock 4 has Share where a DualSense has
// Create.
function systemIcon(
  label: string,
  family: ControllerFamily,
  labels: Record<string, string>,
): PadIconSpec | undefined {
  const ps4 = labels.back === "Share";
  switch (label) {
    case "View":
      return kenney("xbox_button_view");
    case "Menu":
      return kenney("xbox_button_menu");
    case "Xbox":
      return kenney("xbox_guide");
    case "Back":
      return kenney("xbox_button_back_icon");
    case "Start":
      return kenney(
        family === "playstation"
          ? "playstation3_button_start"
          : "xbox_button_start_icon",
      );
    case "Share":
      return kenney(
        family === "playstation"
          ? "playstation4_button_share"
          : "xbox_button_share",
      );
    case "Select":
      return kenney("playstation3_button_select");
    case "Create":
      return kenney("playstation5_button_create");
    case "Options":
      return kenney(
        ps4 ? "playstation4_button_options" : "playstation5_button_options",
      );
    case "Mute":
      return kenney("playstation5_button_mute");
    case "Touch":
      return family === "playstation"
        ? kenney(ps4 ? "playstation4_touchpad" : "playstation5_touchpad")
        : undefined;
    case "PS":
    case "Home":
      return kenney("switch_button_home");
    case "−":
      return kenney("switch_button_minus");
    case "+":
      return kenney("switch_button_plus");
    default:
      return undefined;
  }
}

/**
 * The icon of a control as the pad prints it (labels is controllerLabels,
 * family its controller family), or undefined where neither set has one:
 * the d-pad's and a stick's diagonals, its push stages, Capture, the extra
 * buttons and a Joy-Con pair's side buttons keep their own drawing or name.
 *
 * leftstick and rightstick are the stick clicks (L3/R3); a stick's movement
 * is stickMoveIcon.
 */
export function padIcon(
  control: string,
  family: ControllerFamily,
  labels: Record<string, string>,
): PadIconSpec | undefined {
  const label = own(labels, control) ?? "";
  const position = own(facePositions, control);
  if (position) {
    // A single Joy-Con held sideways (its shoulders are SL and SR): SDL names
    // its buttons B/A/Y/X, which are not the letters printed where they are
    // now (a left Joy-Con has arrows), so it is drawn by position.
    if (family === "nintendo" && labels.leftshoulder === "SL")
      return kenney(`switch_buttons_${position}`);
    const xbox = own(xboxFaces, label);
    if (xbox && family === "nintendo")
      return kenney(`switch_button_${xbox[0]}`);
    if (xbox)
      return family === "xbox"
        ? { ...kenney(`xbox_button_${xbox[0]}`), face: xbox[1] }
        : kenney(`xbox_button_${xbox[0]}`);
    const playstation = own(playstationFaces, label);
    if (playstation)
      return {
        ...kenney(`playstation_button_${playstation[0]}`),
        face: playstation[1],
      };
    return kenney(`switch_buttons_${position}`);
  }
  const dpad = /^dpad\.(up|down|left|right)$/.exec(control);
  if (dpad) return kenney(`${padSet(family)}_dpad_${dpad[1]}`);
  if (control === "leftstick" || control === "rightstick")
    return kenney(`${padSet(family)}_stick_${control[0]}_press`);
  const stick = /^(l|r)s\.([a-z]+)$/.exec(control);
  if (stick) return stickIcon(stick[1] as Side, stick[2], family);
  const axis = /^(left|right)([xy][+-])$/.exec(control);
  if (axis)
    return stickIcon(
      axis[1] === "left" ? "l" : "r",
      own(axisDirections, axis[2]),
      family,
    );
  if (control === "leftshoulder" || control === "rightshoulder") {
    const name = own(shoulders, label);
    return name ? kenney(name) : undefined;
  }
  // lt and rt, the full press and the half press of a trigger, go by the
  // trigger's name.
  const trigger =
    control === "lt" || control === "lt.full"
      ? "lefttrigger"
      : control === "rt" || control === "rt.full"
        ? "righttrigger"
        : control.replace(/^((?:left|right)trigger)[+-]$/, "$1");
  if (trigger === "lefttrigger" || trigger === "righttrigger") {
    const name = own(triggers, own(labels, trigger) ?? "");
    return name ? kenney(name) : undefined;
  }
  if (/^paddle[1-4]$/.test(control)) {
    const name = own(paddles, label);
    return name ? kenney(name) : undefined;
  }
  if (["back", "start", "guide", "misc1", "touchpad"].includes(control))
    return systemIcon(label, family, labels);
  return undefined;
}

/** A vendored icon, aria-hidden: the control's name is read next to it. */
export function PadIcon({ name }: { name: PadIconName }) {
  const icon = padIcons[name];
  if (!icon) return null;
  return (
    <svg
      viewBox={icon.viewBox}
      focusable="false"
      aria-hidden="true"
      data-pad-icon={name}
    >
      {icon.paths.map((path, index) => (
        <path
          // The paths of an icon never change order.
          // biome-ignore lint/suspicious/noArrayIndexKey: static icon data
          key={index}
          d={path.d}
          fill={path.fill}
          fillRule={path.fillRule}
          transform={path.transform}
        />
      ))}
    </svg>
  );
}
