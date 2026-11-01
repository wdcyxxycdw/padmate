import {
  Box3,
  type BufferGeometry,
  Group,
  type Material,
  Mesh,
  MeshBasicMaterial,
  PlaneGeometry,
  Raycaster,
  RingGeometry,
  Texture,
  Vector3,
} from "three";
import type { PadInput } from "../state/store";
import {
  arrangeCallouts,
  controllerCallouts,
  type ModelInfo,
} from "./controller-callouts";
import {
  isJoyCon2Shell,
  isJoyConShell,
  joyCon2Stick,
  joyCon2StickDepth,
  joyConMaxHeight,
  joyConPoints,
  joyConStick,
  layoutJoyCons,
} from "./controller-joycon";
import { convexHull, halfHeightOf, type Point3 } from "./controller-layout";
import {
  controllerLabels,
  controlStrength,
  isSidewaysJoyCon,
  stickDeflection,
} from "./controller-model";
import { type ModelShell, modelShell } from "./controller-models";

export type { ModelInfo } from "./controller-callouts";
export { triggerValue } from "./controller-model";

type Resource = BufferGeometry | Material | Texture;
/** A thumbstick that tilts on its own axis, like the real gimbal. */
export interface AssetStick {
  /**
   * Rotates the stick mesh. Its parent sits on the stick's axis, below the
   * cap, with +Z along the axis, so the pivot's own X and Y tilt the stick
   * without turning the cap.
   */
  pivot: Group;
  /** Largest tilt in radians that stays clear of the shell. */
  maxTilt: number;
  /** Where the leader meets the cap, in the pivot's frame. */
  anchor: Vector3;
  /** The stick's outer radius in model units, for the 2D gauge. */
  radius: number;
  /** The deflection last shown, inside the unit circle; +y is down. */
  x: number;
  y: number;
}
export interface AssetControl {
  /** Where the control rests, in model space. */
  point: Vector3;
  marker: Mesh<RingGeometry, MeshBasicMaterial>;
  strength: number;
  /** Set on leftstick and rightstick when the shell has a movable stick. */
  stick?: AssetStick;
}
/**
 * Each stick mesh, how far below its cap top it pivots along its own axis, and
 * its largest tilt, in units of the normalized model (5.6 wide). Chosen by
 * rendering eight push directions at 3× through the scene's camera and
 * checking the stick's triangles against the shell's: with a deeper pivot or
 * a larger tilt the DualSense skirt leaves its well and shows the shell under
 * it, and the Xbox caps meet their collar or the shell. The Xbox left stick
 * meets its collar soonest, so it tilts least. A full push moves the cap top
 * about 0.087 (DualSense), 0.08 (Xbox left) and 0.13 (Xbox right), several
 * times the old 0.03–0.04; the 2D gauge carries the exact reading.
 */
const stickParts: Partial<
  Record<
    ModelShell,
    readonly (readonly [
      side: string,
      prefix: string,
      depth: number,
      maxTilt: number,
    ])[]
  >
> = {
  dualsense: [
    ["left", "left-stick__head", 0.35, 0.25],
    ["right", "right-stick__head", 0.35, 0.25],
  ],
  xbox: [
    ["left", "Cube002_", 0.7, 0.12],
    ["right", "Cube005_", 0.7, 0.2],
  ],
  // The DualShock 4 has none: its sticks are two components of one merged
  // mesh with the buttons' material, so there is nothing to tilt.
};
/**
 * The radius for the 2D gauge of a shell whose sticks do not tilt, in units of
 * the normalized model. The DualShock 4 stick piece (base and cap) is 5.61
 * wide in the GLB, 1.03 normalized; the cap alone is estimated at 0.45.
 */
const fixedStickRadius: Partial<Record<ModelShell, number>> = {
  dualshock: 0.45,
};
const zAxis = new Vector3(0, 0, 1);
/**
 * Points that hold the silhouette of a model whatever the camera: the
 * vertices of its shell that are on the convex hull (seen from the front) of
 * the vertices of one of 16 slices of its depth (a slice 0.1 deep moves a vertex by 1 px at most). Perspective moves a
 * vertex with its depth, so the hull of the whole is not enough. `skip` are
 * the meshes that are not the shell (the markers).
 */
export function shellOutline(
  group: Group,
  skip: ReadonlySet<unknown>,
): Point3[] {
  group.updateMatrixWorld(true);
  const all: Point3[] = [];
  const v = new Vector3();
  group.traverse((node) => {
    if (!(node instanceof Mesh) || skip.has(node)) return;
    const position = node.geometry.getAttribute("position");
    if (!position) return;
    for (let i = 0; i < position.count; i++) {
      v.fromBufferAttribute(position, i).applyMatrix4(node.matrixWorld);
      all.push({ x: v.x, y: v.y, z: v.z });
    }
  });
  if (all.length === 0) return [];
  let low = Number.POSITIVE_INFINITY,
    high = Number.NEGATIVE_INFINITY;
  for (const p of all) {
    low = Math.min(low, p.z);
    high = Math.max(high, p.z);
  }
  const slices = 16;
  const outline = new Set<Point3>();
  for (let slice = 0; slice < slices; slice++) {
    const inSlice = all.filter((p) => {
      const at = Math.min(
        slices - 1,
        Math.floor(((p.z - low) / (high - low || 1)) * slices),
      );
      return at === slice;
    });
    const hull = convexHull(inSlice);
    // convexHull gives copies of the points it was given, objects and all.
    for (const h of hull) outline.add(h as Point3);
  }
  return [...outline];
}

export function collectResources(group: Group) {
  const resources = new Set<Resource>();
  group.traverse((node) => {
    if (!(node instanceof Mesh)) return;
    resources.add(node.geometry);
    for (const material of Array.isArray(node.material)
      ? node.material
      : [node.material]) {
      resources.add(material);
      for (const value of Object.values(material))
        if (value instanceof Texture) resources.add(value);
    }
  });
  return resources;
}
export function disposeAsset(group: Group) {
  for (const resource of collectResources(group)) resource.dispose();
  group.clear();
}
export function buildAssetModel(
  asset: Group,
  info: ModelInfo,
  makeLabel?: (label: string) => Texture,
) {
  const group = new Group();
  group.add(asset);
  const resources = collectResources(asset);
  const controls = new Map<string, AssetControl>();
  const sticks = new Map<string, AssetStick>();
  const callouts = controllerCallouts(info);
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
  );
  const shell = modelShell(info.type, info);
  let disposed = false;
  const own = <T extends Resource>(resource: T): T => {
    resources.add(resource);
    return resource;
  };
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    for (const r of resources) r.dispose();
    resources.clear();
    group.clear();
  };
  try {
    // A Joy-Con is turned or laid out first, then fits the box the other
    // shells fill: 5.6 wide and no taller than the Xbox shell.
    const layout = isJoyConShell(shell)
      ? layoutJoyCons(
          asset,
          shell,
          isSidewaysJoyCon(info.type, info.buttonMask),
        )
      : undefined;
    const size = new Box3().setFromObject(asset).getSize(new Vector3());
    asset.scale.multiplyScalar(
      layout ? Math.min(5.6 / size.x, joyConMaxHeight / size.y) : 5.6 / size.x,
    );
    asset.updateMatrixWorld(true);
    const center = new Box3().setFromObject(asset).getCenter(new Vector3());
    asset.position.sub(center);
    asset.updateMatrixWorld(true);
    const find = (prefix: string) => {
      let found: Mesh | undefined;
      asset.traverse((node) => {
        if (node instanceof Mesh && node.name.startsWith(prefix)) found = node;
      });
      return found;
    };
    // The accepted shell has Elite paddles. They are excluded from the standard view.
    if (shell === "xbox") {
      const paddles = find("Plane001_");
      if (paddles) paddles.visible = false;
    }
    const point = (prefix: string, fallback: [number, number, number]) => {
      const part = find(prefix);
      return part
        ? new Box3().setFromObject(part).getCenter(new Vector3())
        : new Vector3(...fallback);
    };
    const joyCon = layout && joyConPoints(layout);
    const positions: Record<string, Vector3> = joyCon
      ? joyCon.positions
      : shell === "dualsense"
        ? {
            south: point("cross__cap", [1.6, 0.2, 0.8]),
            east: point("circle__cap", [2, 0.75, 0.8]),
            west: point("square__cap", [1.2, 0.75, 0.8]),
            north: point("triangle__cap", [1.6, 1.2, 0.8]),
            leftstick: point("left-stick__head", [-0.9, 0, 1]),
            rightstick: point("right-stick__head", [0.9, 0, 1]),
            leftshoulder: point("l1__bumper", [-1.7, 1.6, 0.3]),
            rightshoulder: point("r1__bumper", [1.7, 1.6, 0.3]),
            lefttrigger: point("l2__trigger", [-1.7, 1.7, -0.5]),
            righttrigger: point("r2__trigger", [1.7, 1.7, -0.5]),
            back: point("create__button", [-1.1, 1.3, 0.5]),
            start: point("options__button", [1.1, 1.3, 0.5]),
            guide: point("ps__button-base", [0, -0.3, 0.8]),
            touchpad: point("touchpad__surface", [0, 1, 0.8]),
            "dpad.up": point("up__cap", [-1.7, 1.2, 0.8]),
            "dpad.down": point("down__cap", [-1.7, 0.3, 0.8]),
            "dpad.left": point("left__cap", [-2, 0.7, 0.8]),
            "dpad.right": point("right__cap", [-1.2, 0.7, 0.8]),
            misc1: point("mute__button", [0, 0.25, 0.8]),
          }
        : shell === "dualshock"
          ? {
              // Measured on the merged meshes: the centre of each connected
              // component of the GLB (30.6 wide, centre y 9.05, z -1.29), scaled
              // by 5.6 / 30.6 into the normalized frame. Front controls are
              // projected onto the surface below, which sets their z.
              south: new Vector3(1.758, 0.308, 0.86),
              east: new Vector3(2.16, 0.71, 0.86),
              west: new Vector3(1.356, 0.71, 0.86),
              north: new Vector3(1.758, 1.112, 0.86),
              leftstick: new Vector3(-0.894, -0.066, 1.21),
              rightstick: new Vector3(0.894, -0.066, 1.21),
              // L1 and R1 are the thin pieces on the top edge, L2 and R2 the
              // large ones behind them, which a front view hides: their anchors
              // sit just above the bumpers, at the triggers depth.
              leftshoulder: new Vector3(-1.712, 1.64, 0.42),
              rightshoulder: new Vector3(1.712, 1.64, 0.42),
              lefttrigger: new Vector3(-1.702, 1.74, -0.3),
              righttrigger: new Vector3(1.702, 1.74, -0.3),
              back: new Vector3(-1.143, 1.273, 0.82),
              start: new Vector3(1.143, 1.273, 0.82),
              guide: new Vector3(0, -0.099, 0.7),
              touchpad: new Vector3(0, 1.033, 0.82),
              "dpad.up": new Vector3(-1.757, 0.971, 0.84),
              "dpad.down": new Vector3(-1.757, 0.449, 0.84),
              "dpad.left": new Vector3(-2.018, 0.71, 0.84),
              "dpad.right": new Vector3(-1.497, 0.71, 0.84),
            }
          : {
              south: new Vector3(1.576, 0.68, 0.71),
              east: new Vector3(2.009, 1.025, 0.5),
              west: new Vector3(1.151, 1.068, 0.57),
              north: new Vector3(1.576, 1.423, 0.4),
              leftstick: new Vector3(-1.483, 1.167, 0.83),
              rightstick: new Vector3(0.745, 0.291, 1.15),
              leftshoulder: new Vector3(-1.6, 1.7, -0.07),
              rightshoulder: new Vector3(1.6, 1.7, -0.05),
              lefttrigger: new Vector3(-1.6, 1.8, -0.8),
              righttrigger: new Vector3(1.6, 1.8, -0.8),
              back: new Vector3(-0.44, 0.95, 0.7),
              start: new Vector3(0.43, 0.95, 0.7),
              guide: new Vector3(0, 1.53, 0.34),
              "dpad.up": new Vector3(-0.76, 0.4, 0.8),
              "dpad.down": new Vector3(-0.76, -0.06, 0.92),
              "dpad.left": new Vector3(-0.99, 0.17, 0.86),
              "dpad.right": new Vector3(-0.53, 0.17, 0.86),
              // Share (Series), Capture: below the Xbox button.
              misc1: new Vector3(0, 0.62, 0.7),
            };
    // The paddles of an Elite or Edge, a Joy-Con's other side buttons: on the
    // grips, which the front view shows (the Elite's are behind them). An
    // Edge's Fn buttons sit below the sticks.
    const grip = (x: number, y: number) => new Vector3(x, y, 0.6);
    if (!joyCon)
      Object.assign(
        positions,
        shell !== "xbox"
          ? {
              // The DualShock 4's grips are wider apart than the DualSense's.
              paddle1:
                shell === "dualshock" ? grip(2.1, -1) : grip(1.75, -0.95),
              paddle2:
                shell === "dualshock" ? grip(-2.1, -1) : grip(-1.75, -0.95),
              paddle3: positions.rightstick
                .clone()
                .add(new Vector3(0.1, -0.5, 0)),
              paddle4: positions.leftstick
                .clone()
                .add(new Vector3(-0.1, -0.5, 0)),
            }
          : {
              paddle1: grip(1.45, -0.5),
              paddle2: grip(-1.45, -0.5),
              paddle3: grip(1.75, -0.95),
              paddle4: grip(-1.75, -0.95),
            },
      );
    // A marker for each control the pad has a chip for, and the Xbox, PS or
    // Home button, which has none but lights when pressed.
    for (const name of Object.keys(positions))
      if (name !== "guide" && !callouts.some((c) => c.control === name))
        delete positions[name];
    const ray = new Raycaster();
    for (const [name, p] of Object.entries(positions)) {
      // Project front controls onto the actual surface, retaining trigger anchors above the shoulders.
      if (!name.endsWith("trigger") && !joyCon?.behind.has(name)) {
        ray.set(new Vector3(p.x, p.y, 10), new Vector3(0, 0, -1));
        const hit = ray
          .intersectObject(asset, true)
          .find((h) => h.object.visible);
        if (hit) p.copy(hit.point);
      }
      const material = own(
        new MeshBasicMaterial({
          color: 0x40c3ff,
          transparent: true,
          opacity: 0,
          depthWrite: false,
          depthTest: false,
        }),
      );
      const marker = new Mesh(own(new RingGeometry(0.09, 0.14, 36)), material);
      marker.position.copy(p);
      marker.position.z += 0.015;
      marker.renderOrder = 5;
      group.add(marker);
      controls.set(name, { point: p, marker, strength: 0 });
    }
    // A Joy-Con's stick is a mesh of its own. A Switch OLED Joy-Con's depth is
    // in metres of the GLB, which the normalization scaled; a Joy-Con 2's
    // stick turns about its node, so the mesh says how deep that is.
    const tilting = joyCon
      ? joyCon.sticks.map(
          ([side, mesh]) =>
            (isJoyCon2Shell(shell)
              ? [side, mesh, joyCon2StickDepth(mesh), joyCon2Stick.maxTilt]
              : [
                  side,
                  mesh,
                  joyConStick.depth * asset.scale.x,
                  joyConStick.maxTilt,
                ]) as readonly [string, Mesh | undefined, number, number],
        )
      : (stickParts[shell] ?? []).map(
          ([side, prefix, depth, maxTilt]) =>
            [side, find(prefix), depth, maxTilt] as const,
        );
    for (const [side, mesh, depth, maxTilt] of tilting) {
      const control = controls.get(`${side}stick`);
      if (!mesh || !control) continue;
      // Every shell with a tilting stick models it upright in its own node: the
      // mesh's +Z is the stick's axis and its box is centred on it.
      if (!mesh.geometry.boundingBox) mesh.geometry.computeBoundingBox();
      const box = mesh.geometry.boundingBox;
      if (!box) continue;
      const axis = new Vector3()
        .setFromMatrixColumn(mesh.matrixWorld, 2)
        .normalize();
      const top = box
        .getCenter(new Vector3())
        .setZ(box.max.z)
        .applyMatrix4(mesh.matrixWorld);
      const scale = new Vector3()
        .setFromMatrixColumn(mesh.matrixWorld, 0)
        .length();
      const frame = new Group();
      frame.position.copy(top).addScaledVector(axis, -depth);
      frame.quaternion.setFromUnitVectors(zAxis, axis);
      group.add(frame);
      const pivot = new Group();
      frame.add(pivot);
      pivot.attach(mesh);
      // The pressed ring rides on the cap.
      pivot.attach(control.marker);
      const stick: AssetStick = {
        pivot,
        maxTilt,
        anchor: pivot.worldToLocal(control.point.clone()),
        radius:
          (Math.max(box.max.x - box.min.x, box.max.y - box.min.y) / 2) * scale,
        x: 0,
        y: 0,
      };
      control.stick = stick;
      sticks.set(side, stick);
    }
    // A stick that cannot tilt still reports its push to the 2D gauge: a pivot
    // that never turns, in model space, with the anchor where the control is.
    const fixedRadius = fixedStickRadius[shell];
    if (fixedRadius)
      for (const side of ["left", "right"]) {
        const control = controls.get(`${side}stick`);
        if (!control) continue;
        const pivot = new Group();
        group.add(pivot);
        const stick: AssetStick = {
          pivot,
          maxTilt: 0,
          anchor: control.point.clone(),
          radius: fixedRadius,
          x: 0,
          y: 0,
        };
        control.stick = stick;
        sticks.set(side, stick);
      }
    // Brand glyphs are rendered from SDL labels, even when a generic shell is used.
    if (makeLabel) {
      if (shell === "xbox")
        asset.traverse((n) => {
          if (n.name.startsWith("Text")) n.visible = false;
        });
      // The DualShock 4 and the Joy-Cons have their symbols in the texture.
      const faces =
        shell === "dualshock" || layout
          ? []
          : ["south", "east", "west", "north"];
      for (const name of faces) {
        const p = positions[name];
        const texture = own(makeLabel(labels[name]));
        const decal = new Mesh(
          own(new PlaneGeometry(0.36, 0.36)),
          own(
            new MeshBasicMaterial({
              map: texture,
              transparent: true,
              depthWrite: false,
              polygonOffset: true,
              polygonOffsetFactor: -1,
            }),
          ),
        );
        decal.position.copy(p);
        decal.position.z += 0.02;
        group.add(decal);
      }
    }
    const tilt = new Vector3();
    const update = (input?: PadInput) => {
      for (const [name, c] of controls) {
        c.strength = controlStrength(name, input);
        c.marker.material.opacity = c.strength * 0.95;
      }
      for (const [side, stick] of sticks) {
        const { x, y, length } = stickDeflection(input, side);
        stick.x = x;
        stick.y = y;
        // Tip the cap toward the push: +x to the right, +y (down) toward -Y.
        if (length > 0)
          stick.pivot.quaternion.setFromAxisAngle(
            tilt.set(y / length, x / length, 0),
            length * stick.maxTilt,
          );
        else stick.pivot.quaternion.identity();
      }
    };
    /** Where a control's leader ends, in world space; a stick's rides its cap. */
    const anchor = (control: AssetControl, target = new Vector3()) => {
      if (!control.stick) {
        group.updateWorldMatrix(true, false);
        return group.localToWorld(target.copy(control.point));
      }
      control.stick.pivot.updateWorldMatrix(true, false);
      return control.stick.pivot.localToWorld(
        target.copy(control.stick.anchor),
      );
    };
    // A Joy-Con's chips are placed by where its controls turned out to be, so
    // that no leader crosses another (controller-joycon-layout.ts).
    const width = new Box3().setFromObject(group).getSize(new Vector3()).x;
    const points: Record<string, Vector3> = {};
    for (const [name, control] of controls) points[name] = anchor(control);
    const outline = joyCon
      ? shellOutline(
          group,
          new Set([...controls.values()].map((c) => c.marker)),
        )
      : [];
    const layoutAnchors = joyCon
      ? {
          width,
          points,
          outline,
          halfHeight: halfHeightOf(outline),
          narrowTop: shell.endsWith("-pair"),
        }
      : undefined;
    return {
      group,
      resources,
      controls,
      sticks,
      callouts: layoutAnchors
        ? arrangeCallouts(callouts, info, layoutAnchors)
        : callouts,
      /** What the Joy-Con layout was made from (a Joy-Con's model only). */
      layoutAnchors,
      update,
      anchor,
      dispose,
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
