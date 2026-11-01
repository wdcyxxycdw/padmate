// The Joy-Con shells (the Switch OLED Joy-Cons and the Joy-Con 2, one GLB
// each): how a single one is turned or a pair laid out, and which part serves
// each control. Each GLB is one node, mesh and material per part, in metres,
// centred, +Y the top (the stick end of the left one, the ABXY end of the
// right one), +Z the front and the rail on +X (left) or -X (right); the parts
// keep their names so a control is found by the exact name (`s`, `sl`, `sr`
// and `sync` share prefixes). The Joy-Con 2 files also have the C button (`c`
// on the right one), a real Capture button, and empty nodes for the parts the
// source model lacks (`sl`, `sr`, `zl` or `zr`), and their stick's node sits
// where it turns.
import { Box3, Mesh, type Object3D, Vector3 } from "three";
import type { ModelShell } from "./controller-models";

export type JoyConShell = Extract<ModelShell, `joycon${string}-${string}`>;

export function isJoyConShell(shell: ModelShell): shell is JoyConShell {
  return shell.startsWith("joycon");
}

/** Whether a shell is the Joy-Con 2 model, which has its own parts. */
export function isJoyCon2Shell(shell: ModelShell) {
  return shell.startsWith("joycon2-");
}

/**
 * The tallest a Joy-Con shell is in the normalized model: the Xbox shell's
 * height at 5.6 wide (4.1098, measured from xbox.glb). The camera looks along
 * a fixed vertical field of view, so a model no taller than the Xbox one is
 * never cut off where the Xbox one is not.
 */
export const joyConMaxHeight = 4.11;

/**
 * The gap between the two Joy-Cons of a pair, in widths of one: small, so
 * they read as a pair.
 */
const pairGap = 0.25;

/**
 * The Capture button of a Switch OLED Joy-Con is part of the left shell, not a
 * mesh of its own (the Joy-Con 2's is a part named `capture`). Its place is the
 * mirror of the right Home button (-5.5, -24.0 mm), a little toward the rail,
 * in metres in the Joy-Con's own frame.
 */
const capturePoint = new Vector3(0.0055, -0.024, 0.0078);
/** Stands for the Capture point where a table names a part. */
const capture = "capture";

/**
 * How a Joy-Con's stick tilts. The stick is its own mesh, 9 mm tall (z 6 to
 * 15 mm), a stem of 5.4 mm radius in a well of the shell (floor at 6.0, the
 * shell's front at 7.9 mm) under a cap of 8 mm radius whose underside is at
 * 11 mm. `depth` (metres below the cap top) puts the pivot at z = 7 mm, between
 * the well's floor and the shell's front, where a gimbal would sit; `maxTilt`
 * (radians) is the largest angle at which the cap's rim, which dips by
 * 7.9 sin(tilt) on the low side, stays above the shell: at 0.3 it is 8.5 mm
 * over a shell of 7.2 to 7.9 (checked against the shell for all eight pushes
 * in the tests), at 0.4 it would meet it. A full push moves the cap top by
 * 8 sin(0.3) = 2.4 mm, 0.09 of the normalized model upright and 0.12 sideways,
 * as much as the DualSense's; the 2D gauge carries the exact reading.
 */
export const joyConStick = { depth: 0.008, maxTilt: 0.3 };

/**
 * How a Joy-Con 2's stick tilts. Its node is the pivot: the packer puts it on
 * the stick's axis at the bottom of the stick's base, where the stem leaves
 * the shell, 5.8 mm under the cap top. The stick is the cap alone (17.7 mm
 * wide, 2.7 mm tall); the base stays, so the depth is read from the mesh
 * (joyCon2StickDepth). `maxTilt` (radians) is the largest angle at which the
 * cap stays over the shell: at 0.3 its lowest point is 0.65 mm above the
 * shell's front in every one of the eight pushes (checked in the tests), a
 * full push moves the cap top by 5.8 sin(0.3) = 1.7 mm.
 */
export const joyCon2Stick = { maxTilt: 0.3 };

/**
 * How far below its cap top a Joy-Con 2's stick turns, in the units of the
 * normalized model: the cap top's height over the node's origin (the mesh's
 * z = 0), scaled by the node's world scale.
 */
export function joyCon2StickDepth(mesh: Mesh): number {
  if (!mesh.geometry.boundingBox) mesh.geometry.computeBoundingBox();
  const box = mesh.geometry.boundingBox;
  if (!box) return 0;
  mesh.updateWorldMatrix(true, false);
  const scale = new Vector3().setFromMatrixColumn(mesh.matrixWorld, 2).length();
  return box.max.z * scale;
}

type Table = Readonly<Record<string, string>>;

// The part that serves each control, by the Joy-Con's side and how it is
// held. A sideways Joy-Con is SDL's "mini gamepad": its face buttons are the
// four buttons of the side facing the player, the stick is always the left
// stick, and SL and SR are the shoulders.
const sidewaysLeft: Table = {
  south: "dpad-left",
  east: "dpad-down",
  west: "dpad-up",
  north: "dpad-right",
  leftstick: "stick",
  leftshoulder: "sl",
  rightshoulder: "sr",
  paddle2: "l",
  paddle4: "zl",
  start: "minus",
  guide: capture,
};
const sidewaysRight: Table = {
  south: "a",
  east: "x",
  west: "b",
  north: "y",
  leftstick: "stick",
  leftshoulder: "sl",
  rightshoulder: "sr",
  paddle1: "r",
  paddle3: "zr",
  start: "plus",
  guide: "home",
};
const uprightLeft: Table = {
  "dpad.up": "dpad-up",
  "dpad.down": "dpad-down",
  "dpad.left": "dpad-left",
  "dpad.right": "dpad-right",
  leftstick: "stick",
  leftshoulder: "l",
  lefttrigger: "zl",
  back: "minus",
  misc1: capture,
  paddle2: "sl",
  paddle4: "sr",
};
const uprightRight: Table = {
  south: "b",
  east: "a",
  west: "y",
  north: "x",
  rightstick: "stick",
  rightshoulder: "r",
  righttrigger: "zr",
  start: "plus",
  guide: "home",
  paddle1: "sr",
  paddle3: "sl",
};
// The C button of the Joy-Con 2 on the right, SDL's misc2, held either way.
const joyCon2Extra: Table = { misc2: "c" };

/** One control, the Joy-Con root it is on, and the name of its part. */
interface Assignment {
  control: string;
  root: Object3D;
  part: string;
}

export interface JoyConLayout {
  assignments: readonly Assignment[];
}

function rootOf(asset: Object3D, name: string): Object3D {
  const root = asset.getObjectByName(name);
  if (!root) throw new Error(`The pair has no ${name}`);
  return root;
}

/**
 * Turns or lays out the Joy-Cons in `asset` (before it is normalized) and
 * says which part serves each control:
 *
 * - held sideways: the left one turned 90° counter-clockwise, the right one
 *   90° clockwise, so the stick is on the left and the rail on top;
 * - upright: as the file has it;
 * - a pair: the left one at -x and the right one at +x, upright, a
 *   `pairGap` of a width apart.
 */
export function layoutJoyCons(
  asset: Object3D,
  shell: JoyConShell,
  sideways: boolean,
): JoyConLayout {
  const assign = (table: Table, root: Object3D): Assignment[] =>
    Object.entries(table).map(([control, part]) => ({ control, root, part }));
  const joyCon2 = isJoyCon2Shell(shell);
  // The right Joy-Con 2's table has its C button; the left one has none.
  const withC = (table: Table, left: boolean): Table =>
    joyCon2 && !left ? { ...table, ...joyCon2Extra } : table;
  if (shell.endsWith("-pair")) {
    const left = rootOf(asset, "joycon-left");
    const right = rootOf(asset, "joycon-right");
    const width = new Box3().setFromObject(left).getSize(new Vector3()).x;
    const offset = (width * (1 + pairGap)) / 2;
    left.position.x = -offset;
    right.position.x = offset;
    return {
      assignments: [
        ...assign(withC(uprightLeft, true), left),
        ...assign(withC(uprightRight, false), right),
      ],
    };
  }
  const left = shell.endsWith("-left");
  if (sideways) asset.rotation.z = left ? Math.PI / 2 : -Math.PI / 2;
  const table = sideways
    ? left
      ? sidewaysLeft
      : sidewaysRight
    : left
      ? uprightLeft
      : uprightRight;
  return { assignments: assign(withC(table, left), asset) };
}

/**
 * Where each control rests, in the model's frame (after the layout and the
 * normalization): the centre of its part. The controls that sit behind the
 * shell's front, the triggers, are in `behind`: the front view does not
 * project them onto the surface. `sticks` has each stick's side and mesh.
 */
export function joyConPoints(layout: JoyConLayout) {
  const positions: Record<string, Vector3> = {};
  const behind = new Set<string>();
  const sticks: [side: string, mesh: Mesh][] = [];
  for (const { control, root, part } of layout.assignments) {
    const node = root.getObjectByName(part);
    if (part === capture && !node) {
      positions[control] = root.localToWorld(capturePoint.clone());
      continue;
    }
    if (!node) throw new Error(`No ${part} on a Joy-Con`);
    // A part without a mesh is an empty node the Joy-Con 2 files put where
    // the model has no geometry (SL, SR, ZL and ZR).
    if (node instanceof Mesh)
      positions[control] = new Box3()
        .setFromObject(node)
        .getCenter(new Vector3());
    else positions[control] = node.getWorldPosition(new Vector3());
    if (part === "zl" || part === "zr") behind.add(control);
    if (part === "stick" && node instanceof Mesh)
      sticks.push([control.slice(0, -5), node]);
  }
  return { positions, behind, sticks };
}
