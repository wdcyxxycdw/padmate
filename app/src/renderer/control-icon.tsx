// A pad control drawn the way the pad shows it, for the key preview (UI-44):
// so "A → D" does not read as two keys. Shapes are inline SVG and letters
// come from CSS (data-label), in theme colours; the icon is hidden from
// screen readers, which read the control's name next to it instead. The
// system buttons (View, Menu, −, Home…) carry the symbol printed on them,
// not a word (ADR-0077).
import type { ReactNode } from "react";
import type { ControllerFamily, FaceControl } from "./controller-model";

export type FaceColor = "green" | "red" | "blue" | "yellow" | "pink";
type Arm = "up" | "down" | "left" | "right";
type Shape = "cross" | "circle" | "square" | "triangle";
/** The symbol printed on a system button. */
export type Glyph =
  | "view"
  | "menu"
  | "xbox"
  | "upload"
  | "share"
  | "select"
  | "back"
  | "start"
  | "minus"
  | "plus"
  | "home"
  | "capture";

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
  /** View, Menu, −, +, Home and the like: the printed symbol. */
  | { kind: "glyph"; glyph: Glyph }
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
// The system buttons, by the name controllerLabels gives them. A PS button's
// logo is not drawn: it is the house of Home, which it is.
const systemButtons = new Set<string>(["back", "start", "guide", "misc1"]);
const glyphs: Record<string, Glyph> = {
  View: "view",
  Menu: "menu",
  Options: "menu",
  Xbox: "xbox",
  Create: "share",
  Select: "select",
  Back: "back",
  Start: "start",
  "−": "minus",
  "+": "plus",
  Home: "home",
  PS: "home",
  Capture: "capture",
};
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
  if (systemButtons.has(control)) {
    const label = own(labels, control) ?? "";
    // Share is PlayStation's burst of lines, or the Xbox Series arrow.
    const glyph =
      label === "Share"
        ? family === "xbox"
          ? "upload"
          : "share"
        : own(glyphs, label);
    if (glyph) return { kind: "glyph", glyph };
  }
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

// Each symbol inside the disc, stroked like the PlayStation shapes; parts
// marked solid are filled, and the front window of View hides the back one.
function GlyphShape({ glyph }: { glyph: Glyph }) {
  switch (glyph) {
    case "view":
      return (
        <>
          <rect
            className="control-icon-glyph"
            x="6.6"
            y="6.6"
            width="7.2"
            height="7.2"
            rx="1.2"
          />
          <rect
            className="control-icon-glyph"
            data-cover="true"
            x="10.2"
            y="10.2"
            width="7.2"
            height="7.2"
            rx="1.2"
          />
        </>
      );
    case "menu":
      return (
        <path
          className="control-icon-glyph"
          d="M7.4 8.4h9.2M7.4 12h9.2M7.4 15.6h9.2"
        />
      );
    case "xbox":
      return (
        <path
          className="control-icon-glyph"
          d="M7.8 7.6Q11.6 9.6 12 12Q12.4 9.6 16.2 7.6M7.8 16.4Q11.6 14.4 12 12Q12.4 14.4 16.2 16.4"
        />
      );
    case "upload":
      return (
        <path
          className="control-icon-glyph"
          d="M12 14V6.8M9.2 9.4 12 6.6l2.8 2.8M7.6 12.6v4.2h8.8v-4.2"
        />
      );
    case "share":
      return (
        <path
          className="control-icon-glyph"
          d="M12 7.4v7.4M7.6 9.4l2.6 5M16.4 9.4l-2.6 5"
        />
      );
    case "select":
      return (
        <rect
          className="control-icon-glyph"
          data-solid="true"
          x="7"
          y="10.2"
          width="10"
          height="3.6"
          rx="1.8"
        />
      );
    case "back":
      return (
        <path
          className="control-icon-glyph"
          data-solid="true"
          d="M15 7.6 8.2 12 15 16.4Z"
        />
      );
    case "start":
      return (
        <path
          className="control-icon-glyph"
          data-solid="true"
          d="M9 7.6 15.8 12 9 16.4Z"
        />
      );
    case "minus":
      return <path className="control-icon-glyph" d="M7.4 12h9.2" />;
    case "plus":
      return <path className="control-icon-glyph" d="M7.4 12h9.2M12 7.4v9.2" />;
    case "home":
      return (
        <path className="control-icon-glyph" d="M7 11.4 12 7.2l5 4.2v5.4H7Z" />
      );
    case "capture":
      return (
        <>
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
    case "glyph":
      return (
        <>
          <Disc />
          <GlyphShape glyph={icon.glyph} />
        </>
      );
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
