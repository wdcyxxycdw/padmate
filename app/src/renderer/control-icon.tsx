// A pad control drawn the way the pad shows it, for the key preview (UI-44):
// so "A → D" does not read as two keys. A control that the vendored icon
// sets draw (ADR-0079 §7, pad-icon.tsx) is that icon. The few they do not
// draw keep padmate's own shapes (inline SVG, letters from CSS data-label,
// theme colours): the diagonals of the D-pad and the sticks, a stick's push
// stages, Capture, and the buttons that only have a name. The icon is hidden
// from screen readers, which read the control's name next to it instead.
import type { ReactNode } from "react";
import type { ControllerFamily } from "./controller-model";
import { type PadFace, PadIcon, type PadIconName, padIcon } from "./pad-icon";

type Arm = "up" | "down" | "left" | "right";
/** A symbol printed on a system button that the icon sets do not draw. */
export type Glyph = "capture";

export type ControlIconSpec =
  /** A vendored icon (pad-icon.tsx), with the printed face colour. */
  | { kind: "art"; name: PadIconName; face?: PadFace }
  /** A diagonal of the D-pad: the cross, with the pressed arms lit. */
  | { kind: "dpad"; arms: Arm[] }
  /** A stick pushed diagonally, or to a stage of its push ("inner"…). */
  | { kind: "stick"; side: "L" | "R"; direction?: string }
  /** A system button with a symbol the icon sets lack: Capture. */
  | { kind: "glyph"; glyph: Glyph }
  /** Any other button: its printed name. */
  | { kind: "badge"; text: string };

// The system buttons, by the name controllerLabels gives them.
const systemButtons = new Set<string>(["back", "start", "guide", "misc1"]);

function own<T>(record: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(record, key) ? record[key] : undefined;
}

/**
 * How to draw the control of an engine decision (labels is
 * controllerLabels, family its controller family).
 */
export function controlIcon(
  control: string,
  family: ControllerFamily,
  labels: Record<string, string>,
): ControlIconSpec {
  const art = padIcon(control, family, labels);
  if (art) return { kind: "art", ...art };
  const dpad = /^dpad\.(up|down)?(left|right)?$/.exec(control);
  if (dpad && (dpad[1] || dpad[2]))
    return {
      kind: "dpad",
      arms: [dpad[1], dpad[2]].filter((arm): arm is Arm => !!arm),
    };
  const stick = /^(l|r)s\.([a-z]+)$/.exec(control);
  if (stick)
    return {
      kind: "stick",
      side: stick[1] === "l" ? "L" : "R",
      direction: stick[2],
    };
  if (systemButtons.has(control) && own(labels, control) === "Capture")
    return { kind: "glyph", glyph: "capture" };
  // lt and rt, and the half press of a trigger, go by the trigger's name.
  const trigger =
    control === "lt" || control === "lt.full"
      ? "lefttrigger"
      : control === "rt" || control === "rt.full"
        ? "righttrigger"
        : control.replace(/^((?:left|right)trigger)[+-]$/, "$1");
  return { kind: "badge", text: own(labels, trigger) ?? control };
}

// Arrows around a stick, by direction, in degrees from up.
const angles: Record<string, number> = {
  up: 0,
  upright: 45,
  right: 90,
  downright: 135,
  down: 180,
  downleft: 225,
  left: 270,
  upleft: 315,
};

const arms: Record<Arm, [number, number, number, number]> = {
  up: [8.5, 1.5, 7, 7.5],
  down: [8.5, 15, 7, 7.5],
  left: [1.5, 8.5, 7.5, 7],
  right: [15, 8.5, 7.5, 7],
};

function shapes(icon: ControlIconSpec): ReactNode {
  switch (icon.kind) {
    case "dpad":
      return (
        <>
          <rect
            className="control-icon-part"
            x="8.5"
            y="8.5"
            width="7"
            height="7"
          />
          {(Object.keys(arms) as Arm[]).map((arm) => {
            const [x, y, width, height] = arms[arm];
            return (
              <rect
                key={arm}
                className="control-icon-part"
                data-lit={icon.arms.includes(arm)}
                x={x}
                y={y}
                width={width}
                height={height}
                rx="1.5"
              />
            );
          })}
        </>
      );
    case "stick": {
      const pushed =
        icon.direction && own(angles, icon.direction) !== undefined
          ? [angles[icon.direction]]
          : [];
      return (
        <>
          <circle
            className="control-icon-disc"
            cx="12"
            cy="12"
            r={icon.direction ? 6.8 : 10.5}
          />
          {pushed.map((angle) => (
            <path
              key={angle}
              className="control-icon-arrow"
              d="M12 0.6 15.4 4.4H8.6Z"
              transform={`rotate(${angle} 12 12)`}
            />
          ))}
        </>
      );
    }
    case "glyph":
      // Capture: a rounded square with a solid dot.
      return (
        <>
          <circle className="control-icon-disc" cx="12" cy="12" r="10.5" />
          <rect
            className="control-icon-glyph"
            x="7"
            y="7"
            width="10"
            height="10"
            rx="2"
          />
          <circle
            className="control-icon-glyph"
            data-solid="true"
            cx="12"
            cy="12"
            r="2.4"
          />
        </>
      );
    case "art":
    case "badge":
      return null;
  }
}

/** The icon of a control (see controlIcon); aria-hidden, without text. */
export function ControlIcon({ icon }: { icon: ControlIconSpec }) {
  if (icon.kind === "art")
    return (
      <span
        className="control-icon"
        data-kind="art"
        data-face={icon.face}
        aria-hidden="true"
      >
        <PadIcon name={icon.name} />
      </span>
    );
  const label =
    icon.kind === "badge"
      ? icon.text
      : icon.kind === "stick"
        ? icon.side
        : undefined;
  const body = shapes(icon);
  return (
    <span
      className="control-icon"
      data-kind={icon.kind}
      data-glyph={icon.kind === "glyph" ? icon.glyph : undefined}
      data-pushed={icon.kind === "stick" && icon.direction ? true : undefined}
      data-label={label}
      aria-hidden="true"
    >
      {body && (
        <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
          {body}
        </svg>
      )}
    </span>
  );
}
