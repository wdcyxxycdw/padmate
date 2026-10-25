import type {
  Profile,
  ProfileAnalog,
  ProfileBinding,
  ProfileDpad,
  ProfileLayer,
  ProfileStick,
  ProfileTrigger,
} from "../../shared/generated/schema";
import {
  baseBinding,
  bindingAt,
  canonicalControl,
  functionKeys,
  functionSetId,
  gestures,
  hasDepth,
  setBaseBinding,
  setBinding,
  usedGestures,
} from "../editor-model";

export type AnalogGroup = "ls" | "rs" | "lt" | "rt" | "dpad";
export type DirectionGroup = "ls" | "rs" | "dpad";
export type BindingControl = Exclude<keyof ProfileLayer, "color">;
export const cardinalDirections = ["up", "right", "down", "left"] as const;
export const diagonalDirections = [
  "upright",
  "downright",
  "downleft",
  "upleft",
] as const;
export type Cardinal = (typeof cardinalDirections)[number];
export type Diagonal = (typeof diagonalDirections)[number];
export type Direction = Cardinal | Diagonal;
export const stickDefaults: Required<ProfileStick> = {
  mode: "standard",
  deadzone: 0.15,
  saturation: 0.95,
  directionThreshold: 0.3,
  diagonalWidth: 45,
  hysteresisDeg: 8,
  directionDelayMs: 0,
  diagonalFallback: "none",
  outerThreshold: 0.9,
  knobStepDeg: 30,
  knobTick: false,
  curve: "linear",
  mouseSpeed: 1200,
  mouseSmoothingMs: 0,
};
export const triggerDefaults: Required<ProfileTrigger> = {
  press: 0.3,
  release: 0.2,
  fullPress: 0.9,
  fullRelease: 0.8,
  curve: "linear",
};
export const dpadDefaults: Required<ProfileDpad> = {
  mode: "buttons",
  fold: "keepFirst",
};
/** Default of the profile-wide raw half-axis threshold (`analog.axisThreshold`). */
export const axisThresholdDefault = 0.3;
/** The degrees one knob tick may turn (`analog.ls.knobStepDeg`). */
export const knobStepRange = { min: 10, max: 180 } as const;
/**
 * The pointer speed in px/s at full travel that a mouse-mode stick may set
 * (`analog.ls.mouseSpeed`, MOU-04). The slider stops at `slider`; the number
 * field goes up to `max`.
 */
export const mouseSpeedRange = { min: 50, max: 10000, slider: 4000 } as const;
/** The smoothing time of a mouse-mode stick in ms (`mouseSmoothingMs`, MOU-06). */
export const mouseSmoothingRange = { min: 0, max: 500 } as const;

export function analogGroup(control: string): AnalogGroup | null {
  control = canonicalControl(control);
  if (
    control === "ls" ||
    control === "leftstick" ||
    control.startsWith("ls.") ||
    /^left[xy][+-]$/.test(control)
  )
    return "ls";
  if (
    control === "rs" ||
    control === "rightstick" ||
    control.startsWith("rs.") ||
    /^right[xy][+-]$/.test(control)
  )
    return "rs";
  if (
    control === "lt" ||
    control === "lt.full" ||
    /^lefttrigger[+-]?$/.test(control)
  )
    return "lt";
  if (
    control === "rt" ||
    control === "rt.full" ||
    /^righttrigger[+-]?$/.test(control)
  )
    return "rt";
  if (control === "dpad" || control.startsWith("dpad.")) return "dpad";
  return null;
}

/**
 * The directions a stick or d-pad mode emits, in grid order. A knob emits
 * none: it turns the stick into the two tick controls (ANA-11). A stick that
 * moves the pointer emits none either (MOU-01).
 */
export function modeDirections(mode: string): readonly Direction[] {
  return mode === "knob" || mode === "mouse"
    ? []
    : mode === "fourWayDiagonal"
      ? diagonalDirections
      : mode === "eightWay"
        ? [...cardinalDirections, ...diagonalDirections]
        : cardinalDirections;
}

/** The two tick controls of a stick in knob mode: clockwise, then counter. */
export function knobControls(group: "ls" | "rs"): BindingControl[] {
  return [`${group}.cw`, `${group}.ccw`] as BindingControl[];
}

/** The inner and outer ring controls of a stick (ANA-10). */
export function ringControls(group: "ls" | "rs"): BindingControl[] {
  return [`${group}.inner`, `${group}.outer`] as BindingControl[];
}

/** The second stage of a trigger (ANA-14). */
export function fullControl(group: "lt" | "rt"): BindingControl {
  return `${group}.full` as BindingControl;
}

export function directionControls(
  group: DirectionGroup,
  mode: string,
): BindingControl[] {
  return modeDirections(mode).map(
    (direction) => `${group}.${direction}` as BindingControl,
  );
}

export function setAnalogSettings<K extends AnalogGroup>(
  profile: Profile,
  group: K,
  settings: ProfileAnalog[K],
): Profile {
  return {
    ...profile,
    analog: { ...profile.analog, [group]: { ...settings } },
  };
}

/** Sets the profile-wide raw half-axis threshold. */
export function setAxisThreshold(profile: Profile, value: number): Profile {
  return { ...profile, analog: { ...profile.analog, axisThreshold: value } };
}

/** Replaces one binding of the edited (base) key set; other keys are kept. */
export function setControlBinding(
  profile: Profile,
  control: BindingControl | string,
  binding: ProfileBinding | undefined,
): Profile {
  return setBaseBinding(profile, control, binding);
}

export function stickMagnitude(
  x: number,
  y: number,
  value: ProfileStick,
): number {
  const { deadzone, saturation } = { ...stickDefaults, ...value };
  if (saturation <= deadzone) return 0;
  return Math.max(
    0,
    Math.min(1, (Math.hypot(x, y) - deadzone) / (saturation - deadzone)),
  );
}

/** Center angle of each direction, clockwise from up, in degrees. */
export const directionAngle: Record<Direction, number> = {
  up: 0,
  upright: 45,
  right: 90,
  downright: 135,
  down: 180,
  downleft: 225,
  left: 270,
  upleft: 315,
};
const diagonalPairs: Record<Diagonal, [Cardinal, Cardinal]> = {
  upright: ["up", "right"],
  downright: ["down", "right"],
  downleft: ["down", "left"],
  upleft: ["up", "left"],
};

function angleDistance(a: number, b: number) {
  const d = Math.abs(a - b) % 360;
  return d > 180 ? 360 - d : d;
}

export interface StickReading {
  /** Normalized travel after the dead zone and saturation, 0–1. */
  magnitude: number;
  /** The stick is inside the dead zone. */
  inDeadzone: boolean;
  /** Travel reached directionThreshold, so directions are pressed. */
  active: boolean;
  /** Directions the engine presses for this position (without hysteresis). */
  directions: Direction[];
  /** Angle range, clockwise from up, of the sector that is pressed. */
  sector: { from: number; to: number } | null;
  /** The stick's angle in degrees, 0 up and clockwise; null until active. */
  angle: number | null;
  /**
   * The ring the stick is in (ANA-10): inner from the trigger point up to the
   * outer threshold, outer from there on; null until active. Without the
   * outer ring's hysteresis, like the directions.
   */
  ring: "inner" | "outer" | null;
}

/**
 * Which virtual directions a stick position presses. It mirrors the engine's
 * readStick, directionsFor and ringWant (internal/engine) without the
 * time-dependent hysteresis and delay. A knob (ANA-11) presses no
 * direction: its ticks come from the angle over time. A stick that moves the
 * pointer (MOU-01) presses none either. x and y are SDL axes normalized to
 * -1–1, with y positive downwards.
 */
export function stickReading(
  x: number,
  y: number,
  value: ProfileStick = {},
  diagonalBound: (direction: Diagonal) => boolean = () => true,
): StickReading {
  const settings = { ...stickDefaults, ...value };
  const up = -y;
  const raw = Math.hypot(x, up);
  const idle: StickReading = {
    magnitude: 0,
    inDeadzone: raw <= settings.deadzone,
    active: false,
    directions: [],
    sector: null,
    angle: null,
    ring: null,
  };
  if (raw <= settings.deadzone || settings.saturation <= settings.deadzone)
    return idle;
  const magnitude =
    (Math.min(raw, settings.saturation) - settings.deadzone) /
    (settings.saturation - settings.deadzone);
  if (magnitude < settings.directionThreshold) return { ...idle, magnitude };
  let angle = (Math.atan2(x, up) * 180) / Math.PI;
  if (angle < 0) angle += 360;
  const ring = magnitude >= settings.outerThreshold ? "outer" : "inner";
  if (settings.mode === "knob" || settings.mode === "mouse")
    return {
      magnitude,
      inDeadzone: false,
      active: true,
      directions: [],
      sector: null,
      angle,
      ring,
    };
  const card = cardinalDirections[Math.floor((angle + 45) / 90) % 4];
  const diag = diagonalDirections[Math.floor(angle / 90) % 4];
  const half = settings.diagonalWidth / 2;
  const nearDiag = angleDistance(angle, directionAngle[diag]) <= half;
  const around = (center: number, width: number) => ({
    from: center - width,
    to: center + width,
  });
  let directions: Direction[];
  let sector: StickReading["sector"];
  switch (settings.mode) {
    case "fourWayDiagonal":
      directions = [diag];
      sector = around(directionAngle[diag], 45);
      break;
    case "eightWay":
      directions = [nearDiag ? diag : card];
      sector = nearDiag
        ? around(directionAngle[diag], half)
        : around(directionAngle[card], 45 - half);
      if (
        nearDiag &&
        settings.diagonalFallback === "cardinals" &&
        !diagonalBound(diag)
      )
        directions = [...diagonalPairs[diag]];
      break;
    case "fourWay":
      directions = [card];
      sector = around(directionAngle[card], 45);
      break;
    default:
      directions = nearDiag ? [...diagonalPairs[diag]] : [card];
      sector = nearDiag
        ? around(directionAngle[diag], half)
        : around(directionAngle[card], 45 - half);
  }
  return {
    magnitude,
    inDeadzone: false,
    active: true,
    directions,
    sector,
    angle,
    ring,
  };
}

/** The single direction a set of pressed directions points to. */
export function combinedDirection(directions: Direction[]): Direction | null {
  if (directions.length === 1) return directions[0];
  if (directions.length !== 2) return null;
  return (
    diagonalDirections.find((diagonal) =>
      diagonalPairs[diagonal].every((cardinal) =>
        directions.includes(cardinal),
      ),
    ) ?? null
  );
}

/** Pressed d-pad buttons as the directions an eight-way d-pad reports. */
export function dpadDirections(
  buttons: Record<string, boolean> = {},
  mode: string = dpadDefaults.mode,
): Direction[] {
  const pressed = cardinalDirections.filter(
    (direction) => buttons[`dpad.${direction}`],
  );
  if (mode !== "eightWay") return pressed;
  const diagonal = combinedDirection(pressed);
  return diagonal ? [diagonal] : pressed;
}

export interface AnalogIssue {
  path: string;
  code: "range" | "saturation" | "release" | "fullRelease" | "fullPress";
}
export function analogIssues(analog: ProfileAnalog = {}): AnalogIssue[] {
  const issues: AnalogIssue[] = [];
  const check = (
    path: string,
    value: number,
    min: number,
    max: number,
    integer = false,
  ) => {
    if (
      !Number.isFinite(value) ||
      value < min ||
      value > max ||
      (integer && !Number.isInteger(value))
    )
      issues.push({ path, code: "range" });
  };
  for (const group of ["ls", "rs"] as const) {
    const settings = { ...stickDefaults, ...analog[group] };
    for (const key of [
      "deadzone",
      "saturation",
      "directionThreshold",
      "outerThreshold",
    ] as const)
      check(`analog.${group}.${key}`, settings[key], 0, 1);
    check(
      `analog.${group}.knobStepDeg`,
      settings.knobStepDeg,
      knobStepRange.min,
      knobStepRange.max,
      true,
    );
    check(
      `analog.${group}.mouseSpeed`,
      settings.mouseSpeed,
      mouseSpeedRange.min,
      mouseSpeedRange.max,
      true,
    );
    check(
      `analog.${group}.mouseSmoothingMs`,
      settings.mouseSmoothingMs,
      mouseSmoothingRange.min,
      mouseSmoothingRange.max,
      true,
    );
    check(
      `analog.${group}.diagonalWidth`,
      settings.diagonalWidth,
      Number.MIN_VALUE,
      90,
    );
    check(`analog.${group}.hysteresisDeg`, settings.hysteresisDeg, 0, 45);
    check(
      `analog.${group}.directionDelayMs`,
      settings.directionDelayMs,
      0,
      Number.MAX_SAFE_INTEGER,
      true,
    );
    if (settings.saturation <= settings.deadzone)
      issues.push({ path: `analog.${group}.saturation`, code: "saturation" });
  }
  for (const group of ["lt", "rt"] as const) {
    const settings = { ...triggerDefaults, ...analog[group] };
    check(`analog.${group}.press`, settings.press, 0, 1);
    check(`analog.${group}.release`, settings.release, 0, 1);
    if (settings.release > settings.press)
      issues.push({ path: `analog.${group}.release`, code: "release" });
    check(`analog.${group}.fullPress`, settings.fullPress, 0, 1);
    check(`analog.${group}.fullRelease`, settings.fullRelease, 0, 1);
    if (settings.fullRelease > settings.fullPress)
      issues.push({
        path: `analog.${group}.fullRelease`,
        code: "fullRelease",
      });
    // Only a written fullPress is held to this: the default, 0.9, must not
    // reject a file whose press is 0.9 or more, such as the two-stage
    // workaround the editor wrote before lt.full (the daemon does the same).
    if (
      analog[group]?.fullPress !== undefined &&
      settings.fullPress <= settings.press
    )
      issues.push({ path: `analog.${group}.fullPress`, code: "fullPress" });
  }
  check(
    "analog.axisThreshold",
    analog.axisThreshold ?? axisThresholdDefault,
    0,
    1,
  );
  return issues;
}

/**
 * Raw half-axis controls of a stick in up, right, down, left order. SDL's
 * y axis grows downwards, so `lefty-` is the upward half.
 */
export function rawStickControls(group: "ls" | "rs"): BindingControl[] {
  const side = group === "ls" ? "left" : "right";
  return [
    `${side}y-`,
    `${side}x+`,
    `${side}y+`,
    `${side}x-`,
  ] as BindingControl[];
}

/** Stores a fraction such as 0.15 from a percentage such as 15. */
export function fromPercent(percent: number): number {
  return Math.round(percent * 100) / 10000;
}
/** Shows a fraction such as 0.15 as a percentage such as 15. */
export function toPercent(value: number): number {
  return Math.round(value * 1000) / 10;
}

/**
 * Every key set of a profile: its groups, the shared one and the sets of the
 * function keys (MAP-27), which use the same stick and trigger settings.
 */
function allLayers(profile: Profile): ProfileLayer[] {
  return [
    ...Object.values(profile.layers),
    ...(profile.common ? [profile.common] : []),
    ...functionKeys.flatMap((key) => profile.functionKeys?.[key] ?? []),
  ];
}

/**
 * Whether any repeat of a stick's or trigger's controls sets minRateHz, in
 * any key group. Only then does the response curve (`analog.<group>.curve`)
 * have an effect: it shapes that rate and moves no threshold (ANA-13). A stick
 * that moves the pointer is the other case (MOU-04): there the curve is always
 * shown, beside the speed, and this does not decide it.
 */
export function usesDepthRate(
  profile: Profile,
  group: "ls" | "rs" | "lt" | "rt",
): boolean {
  return allLayers(profile).some((layer) =>
    Object.entries(layer).some(([control, binding]) => {
      if (
        control === "color" ||
        typeof binding !== "object" ||
        !hasDepth(control) ||
        analogGroup(control) !== group
      )
        return false;
      return gestures.some((gesture) => {
        const action = (binding as ProfileBinding)[gesture];
        return (
          action?.type === "keys" && action.repeat?.minRateHz !== undefined
        );
      });
    }),
  );
}

/**
 * Every key set of a profile, as the IDs `bindingAt` takes: its layers, the
 * common set when it has one and the sets of the function keys it has.
 */
function keySets(profile: Profile): string[] {
  return [
    ...Object.keys(profile.layers),
    ...(profile.common ? ["common"] : []),
    ...functionKeys
      .filter((key) => profile.functionKeys?.[key])
      .map(functionSetId),
  ];
}

/**
 * Whether a trigger has the two stages the editor set up before lt.full
 * existed (M12): press 0.9 or more on the trigger and a shared half-axis
 * threshold of 0.5 with a binding on the half axis, and nothing on the
 * second stage yet. Such a file keeps working; the editor offers to move it
 * to lt.full.
 *
 * The move changes where the trigger itself fires (from 90 percent back to
 * its default), so it is not offered when it could not be made everywhere:
 * when another key set already has a binding on the second stage, or when a
 * script is registered on the trigger or its second stage, which nothing
 * can move. The bindings of the other key sets are moved with the base ones.
 */
export function legacyTwoStage(
  profile: Profile,
  group: "lt" | "rt",
  registrations: { control: string }[] = [],
): boolean {
  const trigger = profile.analog?.[group];
  const half = `${group === "lt" ? "left" : "right"}trigger+`;
  const full = fullControl(group);
  const scripted = [group, full].map(canonicalControl);
  return (
    (trigger?.press ?? triggerDefaults.press) >= 0.9 &&
    trigger?.fullPress === undefined &&
    profile.analog?.axisThreshold === 0.5 &&
    hasKeys(baseBinding(profile, half)) &&
    keySets(profile).every((id) => !hasAny(bindingAt(profile, id, full))) &&
    !registrations.some((entry) =>
      scripted.includes(canonicalControl(entry.control)),
    )
  );
}

/**
 * Moves the two stages of the M12 set-up to the native ones: the trigger's
 * binding becomes the second stage (`lt.full`), the half axis's binding
 * becomes the trigger's (`lt`), and press and release return to their
 * defaults. Press and release are shared by every key set, so the same move
 * is made in all of them (the layers and the common set). The shared
 * half-axis threshold stays as it is, and nothing else changes.
 */
export function convertToFullPull(
  profile: Profile,
  group: "lt" | "rt",
): Profile {
  const half = `${group === "lt" ? "left" : "right"}trigger+`;
  const full = fullControl(group);
  let next = profile;
  for (const id of keySets(profile)) {
    const first = bindingAt(next, id, group);
    const second = bindingAt(next, id, half);
    if (!hasAny(first) && !hasAny(second)) continue;
    if (hasAny(first)) next = setBinding(next, id, full, first);
    next = setBinding(next, id, group, hasAny(second) ? second : undefined);
    next = setBinding(next, id, half, undefined);
  }
  const {
    press: _press,
    release: _release,
    ...rest
  } = next.analog?.[group] ?? {};
  const analog = { ...next.analog };
  if (Object.keys(rest).length) analog[group] = rest;
  else delete analog[group];
  return { ...next, analog };
}

function hasAny(binding: ProfileBinding): boolean {
  return Object.keys(binding).length > 0;
}

function hasKeys(binding: ProfileBinding): boolean {
  return usedGestures(binding).length > 0;
}
