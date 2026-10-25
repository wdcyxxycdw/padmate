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
import { controllerCallouts, type ModelInfo } from "./controller-callouts";
import {
  controllerLabels,
  controlStrength,
  familyName,
  stickDeflection,
} from "./controller-model";
import type { PadInput } from "./store";

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
const stickParts = {
  playstation: [
    ["left", "left-stick__head", 0.35, 0.25],
    ["right", "right-stick__head", 0.35, 0.25],
  ],
  xbox: [
    ["left", "Cube002_", 0.7, 0.12],
    ["right", "Cube005_", 0.7, 0.2],
  ],
} as const;
const zAxis = new Vector3(0, 0, 1);
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
  const ps = familyName(info.type) === "playstation";
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
    const size = new Box3().setFromObject(asset).getSize(new Vector3());
    asset.scale.multiplyScalar(5.6 / size.x);
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
    if (!ps) {
      const paddles = find("Plane001_");
      if (paddles) paddles.visible = false;
    }
    const point = (prefix: string, fallback: [number, number, number]) => {
      const part = find(prefix);
      return part
        ? new Box3().setFromObject(part).getCenter(new Vector3())
        : new Vector3(...fallback);
    };
    const positions: Record<string, Vector3> = ps
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
        };
    if (info.type === "ps3") delete positions.touchpad;
    const ray = new Raycaster();
    for (const [name, p] of Object.entries(positions)) {
      // Project front controls onto the actual surface, retaining trigger anchors above the shoulders.
      if (!name.endsWith("trigger")) {
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
    for (const [side, prefix, depth, maxTilt] of stickParts[
      ps ? "playstation" : "xbox"
    ]) {
      const mesh = find(prefix);
      const control = controls.get(`${side}stick`);
      if (!mesh || !control) continue;
      // Both shells model each stick upright in its own node: the mesh's +Z
      // is the stick's axis and its box is centred on it.
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
    // Brand glyphs are rendered from SDL labels, even when a generic shell is used.
    if (makeLabel) {
      if (!ps)
        asset.traverse((n) => {
          if (n.name.startsWith("Text")) n.visible = false;
        });
      for (const name of ["south", "east", "west", "north"]) {
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
    return {
      group,
      resources,
      controls,
      sticks,
      callouts,
      update,
      anchor,
      dispose,
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
