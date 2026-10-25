// A pad control drawn the way the pad shows it, for the key preview (UI-44):
// so "A → D" does not read as two keys. Shapes are inline SVG and letters
// come from CSS (data-label), in theme colours; the icon is hidden from
// screen readers, which read the control's name next to it instead.
import type { ReactNode } from "react";
import type { ControllerFamily, FaceControl } from "./controller-model";

export type FaceColor = "green" | "red" | "blue" | "yellow" | "pink";
type Arm = "up" | "down" | "left" | "right";
type Shape = "cross" | "circle" | "square" | "triangle";

export type ControlIconSpec =
  /** A face button with a letter, coloured as printed (Xbox). */
  | { kind: "face"; text: string; color?: FaceColor }
  /** A PlayStation face button. */
  | { kind: "symbol"; shape: Shape; color: FaceColor }
  /** A face button without a letter: where it is among the four. */
  | { kind: "position"; position: FaceControl }
  /** The D-pad, with the pressed arms lit (two for a diagonal). */
  | { kind: "dpad"; arms: Arm[] }
  /** A stick: pressed, or pushed in a direction ("active": any). */
  | { kind: "stick"; side: "L" | "R"; direction?: string }
  /** Shoulders, triggers and the other buttons: the printed name. */
  | { kind: "badge"; text: string };

const xboxColors: Record<string, FaceColor> = {
  A: "green",
  B: "red",
  X: "blue",
  Y: "yellow",
};
const symbols: Record<string, [Shape, FaceColor]> = {
  "×": ["cross", "blue"],
  "○": ["circle", "red"],
  "□": ["square", "pink"],
  "△": ["triangle", "green"],
};
const faces = new Set<string>(["south", "east", "west", "north"]);
// A raw half axis: y grows downwards, so y- is up.
const axisDirections: Record<string, string> = {
  "x-": "left",
  "x+": "right",
  "y-": "up",
  "y+": "down",
};

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
  if (faces.has(control)) {
    const label = own(labels, control) ?? "";
    const symbol = own(symbols, label);
    if (symbol) return { kind: "symbol", shape: symbol[0], color: symbol[1] };
    if (/^[ABXY]$/.test(label)) {
      const color = family === "xbox" ? own(xboxColors, label) : undefined;
      return color
        ? { kind: "face", text: label, color }
        : { kind: "face", text: label };
    }
    return { kind: "position", position: control as FaceControl };
  }
  const dpad = /^dpad\.(up|down)?(left|right)?$/.exec(control);
  if (dpad && (dpad[1] || dpad[2]))
    return {
      kind: "dpad",
      arms: [dpad[1], dpad[2]].filter((arm): arm is Arm => !!arm),
    };
  if (control === "leftstick" || control === "rightstick")
    return { kind: "stick", side: control === "leftstick" ? "L" : "R" };
  const stick = /^(l|r)s\.([a-z]+)$/.exec(control);
  if (stick)
    return {
      kind: "stick",
      side: stick[1] === "l" ? "L" : "R",
      direction: stick[2],
    };
  const axis = /^(left|right)([xy][+-])$/.exec(control);
  if (axis)
    return {
      kind: "stick",
      side: axis[1] === "left" ? "L" : "R",
      direction: own(axisDirections, axis[2]),
    };
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

function Disc() {
  return <circle className="control-icon-disc" cx="12" cy="12" r="10.5" />;
}

function SymbolShape({ shape }: { shape: Shape }) {
  switch (shape) {
    case "cross":
      return <path className="control-icon-shape" d="M8 8l8 8M16 8l-8 8" />;
    case "circle":
      return <circle className="control-icon-shape" cx="12" cy="12" r="4.8" />;
    case "square":
      return (
        <rect
          className="control-icon-shape"
          x="7.6"
          y="7.6"
          width="8.8"
          height="8.8"
        />
      );
    case "triangle":
      return <path className="control-icon-shape" d="M12 6.6 17.2 15.6H6.8Z" />;
  }
}

const positions: Record<FaceControl, [number, number]> = {
  north: [12, 5],
  east: [19, 12],
  south: [12, 19],
  west: [5, 12],
};

const arms: Record<Arm, [number, number, number, number]> = {
  up: [8.5, 1.5, 7, 7.5],
  down: [8.5, 15, 7, 7.5],
  left: [1.5, 8.5, 7.5, 7],
  right: [15, 8.5, 7.5, 7],
};

function shapes(icon: ControlIconSpec): ReactNode {
  switch (icon.kind) {
    case "face":
      return <Disc />;
    case "symbol":
      return (
        <>
          <Disc />
          <SymbolShape shape={icon.shape} />
        </>
      );
    case "position":
      return (Object.keys(positions) as FaceControl[]).map((position) => {
        const [cx, cy] = positions[position];
        return (
          <circle
            key={position}
            className="control-icon-part"
            data-lit={position === icon.position}
            cx={cx}
            cy={cy}
            r="3.6"
          />
        );
      });
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
        icon.direction === "active"
          ? [0, 90, 180, 270]
          : icon.direction && own(angles, icon.direction) !== undefined
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
    case "badge":
      return null;
  }
}

/** The icon of a control (see controlIcon); aria-hidden, without text. */
export function ControlIcon({ icon }: { icon: ControlIconSpec }) {
  const label =
    icon.kind === "face" || icon.kind === "badge"
      ? icon.text
      : icon.kind === "stick"
        ? icon.side
        : undefined;
  const body = shapes(icon);
  return (
    <span
      className="control-icon"
      data-kind={icon.kind}
      data-face={"color" in icon ? icon.color : undefined}
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
