import {
  ACESFilmicToneMapping,
  Box3,
  CanvasTexture,
  DirectionalLight,
  HemisphereLight,
  PerspectiveCamera,
  PMREMGenerator,
  Scene,
  SRGBColorSpace,
  Vector3,
  WebGLRenderer,
} from "three";
import { RoomEnvironment } from "three/addons/environments/RoomEnvironment.js";
import {
  buildAssetModel,
  disposeAsset,
  type ModelInfo,
} from "./controller-asset";
import { calloutEdge } from "./controller-callouts";
import { stickActiveThreshold } from "./controller-model";
import { controllerModels, modelShell } from "./controller-models";
import {
  createStageStore,
  type RetainedStage,
  type StageStore,
  stageKey,
} from "./controller-stage";
import type { PadInput } from "./store";

export { triggerValue } from "./controller-asset";

/**
 * The 2D gauge drawn over a pushed stick: a ring around the resting cap and a
 * dot that travels to the ring at full push, so a push reads clearly even
 * though the model itself can only tilt a little. `cap` is the stick's
 * projected radius in pixels; the ring follows it within 20–40 px.
 */
export function stickGauge(
  center: { x: number; y: number },
  cap: number,
  stick: { x: number; y: number },
) {
  const r = Math.max(20, Math.min(40, Number.isFinite(cap) ? cap : 0));
  const length = Math.hypot(stick.x, stick.y);
  const scale = length > 1 ? 1 / length : 1;
  return {
    cx: center.x,
    cy: center.y,
    r,
    x: center.x + stick.x * scale * r,
    y: center.y + stick.y * scale * r,
    visible: length >= stickActiveThreshold,
  };
}

function svgNode(tag: string, attributes: Record<string, string | number>) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [name, value] of Object.entries(attributes))
    node.setAttribute(name, String(value));
  return node;
}
// SVG coordinates to a tenth of a pixel.
const round = (value: number) => Math.round(value * 10) / 10;

/**
 * Draws a stick's gauge into `layer`. The group is created once and then only
 * updated, so its CSS fade runs; while it fades out it keeps the last reading
 * instead of snapping to the middle.
 */
export function drawStickGauge(
  layer: Element,
  control: string,
  gauge: ReturnType<typeof stickGauge>,
) {
  let g = layer.querySelector(`[data-gauge="${control}"]`);
  if (!g) {
    g = svgNode("g", { class: "stage-gauge", "data-gauge": control });
    g.append(
      svgNode("circle", { class: "stage-gauge-disc" }),
      svgNode("circle", { class: "stage-gauge-ring" }),
      svgNode("circle", { class: "stage-gauge-hub", r: 2 }),
      svgNode("path", { class: "stage-gauge-halo" }),
      svgNode("path", { class: "stage-gauge-line" }),
      svgNode("circle", { class: "stage-gauge-dot", r: 5.5 }),
    );
    layer.append(g);
  }
  g.setAttribute("data-visible", String(gauge.visible));
  if (!gauge.visible) return g;
  const [disc, ring, hub, halo, line, dot] = g.children;
  const cx = round(gauge.cx),
    cy = round(gauge.cy),
    x = round(gauge.x),
    y = round(gauge.y),
    r = round(gauge.r);
  for (const circle of [disc, ring, hub]) {
    circle.setAttribute("cx", String(cx));
    circle.setAttribute("cy", String(cy));
  }
  disc.setAttribute("r", String(r));
  ring.setAttribute("r", String(r));
  const d = `M ${cx} ${cy} L ${x} ${y}`;
  halo.setAttribute("d", d);
  line.setAttribute("d", d);
  dot.setAttribute("cx", String(x));
  dot.setAttribute("cy", String(y));
  return g;
}

export function createFrameScheduler(
  draw: () => void,
  request: typeof requestAnimationFrame = requestAnimationFrame,
  cancel: typeof cancelAnimationFrame = cancelAnimationFrame,
) {
  let frame: number | undefined,
    dirty = false,
    visible = true,
    disposed = false;
  const schedule = () => {
    if (disposed || !visible || !dirty || frame !== undefined) return;
    frame = request(() => {
      frame = undefined;
      if (disposed || !visible) return;
      dirty = false;
      draw();
    });
  };
  return {
    invalidate() {
      dirty = true;
      schedule();
    },
    /** Draws a pending frame now, instead of when the next one comes. */
    flush() {
      if (disposed || !visible || !dirty) return;
      if (frame !== undefined) cancel(frame);
      frame = undefined;
      dirty = false;
      draw();
    },
    setVisible(next: boolean) {
      visible = next;
      if (!visible && frame !== undefined) {
        cancel(frame);
        frame = undefined;
      }
      schedule();
    },
    dispose() {
      disposed = true;
      if (frame !== undefined) cancel(frame);
      frame = undefined;
    },
  };
}
function makeLabel(text: string) {
  const canvas = document.createElement("canvas");
  canvas.width = 128;
  canvas.height = 128;
  const context = canvas.getContext("2d");
  if (!context) throw Error("No label canvas");
  context.fillStyle = "#292d33";
  context.beginPath();
  context.arc(64, 64, 61, 0, Math.PI * 2);
  context.fill();
  context.fillStyle = "#e1e5e9";
  context.textAlign = "center";
  context.textBaseline = "middle";
  context.font = '500 80px "Segoe UI", sans-serif';
  context.fillText(text, 64, 66, 110);
  const texture = new CanvasTexture(canvas);
  texture.colorSpace = SRGBColorSpace;
  return texture;
}
/** What a built model offers the scene (controller-asset). */
export type SceneModel = ReturnType<typeof buildAssetModel>;

/**
 * The GPU side of the controller scene: canvas, renderer, scene, camera and
 * the model with its textures. It outlives a mount (controller-stage.ts): a
 * mount only puts the canvas on its page and draws.
 */
export interface SceneStage extends RetainedStage {
  readonly canvas: HTMLCanvasElement;
  readonly camera: PerspectiveCamera;
  /** The model, once it is built. */
  readonly model: SceneModel | undefined;
  /** Settles when the model is built, or rejects when it cannot be. */
  readonly loaded: Promise<void>;
  /** The context was lost or the model could not be built: not usable again. */
  readonly broken: boolean;
  /** Draws one frame at a size in CSS pixels. */
  render(width: number, height: number): void;
  /**
   * Shrinks the drawing buffer while no page shows the stage, which can be
   * minutes: at devicePixelRatio 2 with antialiasing it is the biggest thing
   * the idle stage holds that a mount does not need. The next render sets
   * the size again; the textures stay, which is what the next visit saves.
   */
  park(): void;
  /** Calls back when the context is lost; the result stops the calls. */
  onLost(listener: () => void): () => void;
}

function createGpuStage(info: ModelInfo): SceneStage {
  const canvas = document.createElement("canvas");
  canvas.className = "controller-canvas";
  const renderer = new WebGLRenderer({ canvas, alpha: true, antialias: true });
  renderer.toneMapping = ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.05;
  const scene = new Scene(),
    camera = new PerspectiveCamera(32, 1, 0.05, 100);
  let environment: ReturnType<PMREMGenerator["fromScene"]>;
  try {
    const room = new RoomEnvironment(),
      pmrem = new PMREMGenerator(renderer);
    environment = pmrem.fromScene(room, 0.04);
    room.dispose();
    pmrem.dispose();
  } catch (error) {
    renderer.dispose();
    renderer.forceContextLoss();
    throw error;
  }
  scene.environment = environment.texture;
  scene.environmentIntensity = 0.8;
  scene.add(new HemisphereLight(0xd8e4f3, 0x3b3431, 0.35));
  for (const [color, power, x, y, z] of [
    [0xf4f7ff, 1.7, -4, 7, 7],
    [0xc4dcff, 1.1, 4, 2, -1],
    [0xffffff, 0.45, 2, -3, 4],
  ]) {
    const light = new DirectionalLight(color, power);
    light.position.set(x, y, z);
    scene.add(light);
  }
  let model: SceneModel | undefined,
    disposed = false,
    broken = false;
  const lostListeners = new Set<() => void>();
  const lost = (event: Event) => {
    event.preventDefault();
    if (disposed) return;
    broken = true;
    for (const listener of [...lostListeners]) listener();
  };
  canvas.addEventListener("webglcontextlost", lost);
  // The model cache decodes and parses each shell once (G3); this stage
  // gets its own copy, which it disposes with the stage.
  const loaded = controllerModels
    .get(modelShell(info.type))
    .then((asset) => {
      if (disposed) {
        disposeAsset(asset);
        return;
      }
      model = buildAssetModel(asset, info, makeLabel);
      scene.add(model.group);
    })
    .catch((error: unknown) => {
      broken = true;
      throw error;
    });
  // Mounts wait for it and handle the failure themselves.
  loaded.catch(() => {});
  return {
    canvas,
    camera,
    loaded,
    get model() {
      return model;
    },
    get broken() {
      // The event that sets `broken` arrives asynchronously; a context lost
      // while the page was away (sleep, a GPU reset) may not have delivered
      // it yet, and a stage drawing on it would stay blank without an error.
      return broken || renderer.getContext().isContextLost();
    },
    render(width, height) {
      if (!model || disposed) return;
      // A window moved to another screen changes the ratio between frames.
      const ratio = Math.min(window.devicePixelRatio || 1, 2);
      if (ratio !== renderer.getPixelRatio()) renderer.setPixelRatio(ratio);
      renderer.setSize(width, height, false);
      camera.aspect = width / height;
      const size = new Box3().setFromObject(model.group).getSize(new Vector3());
      const distance = Math.max(
        11.9,
        (size.x * 1.7) / (2 * Math.tan((Math.PI * 16) / 180) * camera.aspect),
      );
      camera.position.set(0, 0, distance);
      camera.lookAt(0, 0, 0);
      camera.updateProjectionMatrix();
      camera.updateMatrixWorld(true);
      renderer.render(scene, camera);
    },
    park() {
      if (disposed) return;
      renderer.setSize(1, 1, false);
    },
    onLost(listener) {
      lostListeners.add(listener);
      return () => lostListeners.delete(listener);
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      lostListeners.clear();
      canvas.removeEventListener("webglcontextlost", lost);
      canvas.remove();
      model?.dispose();
      environment.dispose();
      scene.clear();
      renderer.dispose();
      // Nothing draws on this canvas again, so its context goes now.
      renderer.forceContextLoss();
    },
  };
}

/** What a mount knows when it starts, so its first frame is the right one. */
export interface SceneState {
  input?: PadInput;
  selected?: string;
  unbound?: readonly string[];
}

/** The mount that has a stage's canvas: letting go of it. */
const mounts = new WeakMap<SceneStage, () => void>();

/**
 * A scene on a page, drawn on a stage from `stages`. The first mount builds
 * the stage; the ones after it take its canvas and draw, with no new renderer
 * and no new texture upload (H2). Disposing a mount only lets go of the
 * stage, which the store keeps for the next one.
 */
export function bindControllerScene(stages: StageStore<ModelInfo, SceneStage>) {
  return function createControllerScene(
    host: HTMLElement,
    info: ModelInfo,
    onReady: () => void,
    onFailure: () => void,
    labels?: SVGSVGElement,
    initial: SceneState = {},
  ) {
    let input = initial.input,
      selectedControl = initial.selected,
      unboundControls = new Set(initial.unbound);
    let detached = false,
      ready = false,
      failed = false,
      intersecting = true;
    const scheduler = createFrameScheduler(() => draw());
    const visibility = () => {
      scheduler.setVisible(!document.hidden && intersecting);
      if (!document.hidden && intersecting) scheduler.invalidate();
    };
    // These can throw where the browser lacks them: before the stage is taken.
    const resize = new ResizeObserver(() => scheduler.invalidate());
    const intersection = new IntersectionObserver((entries) => {
      intersecting = entries.some((e) => e.isIntersecting);
      visibility();
    });
    // One stage serves one model at a time: a mount for another model
    // disposes the stage, so it must not be asked for while another mount
    // still holds one. Only ControllerWorkspace renders a Controller, and
    // React runs every cleanup before the next effects; revisit this if a
    // second one is ever shown beside it.
    const stage = stages.acquire(info);
    // The newest mount has the canvas; an earlier one lets go of it.
    mounts.get(stage)?.();
    // Three layers that stay in place, so a gauge's fade is not restarted by
    // the per-frame rebuild of the leaders: plain leaders, then the stick
    // gauges, then the active and selected leaders on top.
    const layers = labels
      ? {
          back: svgNode("g", {}),
          gauges: svgNode("g", {}),
          front: svgNode("g", {}),
        }
      : undefined;
    if (labels && layers)
      labels.replaceChildren(layers.back, layers.gauges, layers.front);
    const toScreen = (point: Vector3, box: DOMRect) => {
      const p = point.project(stage.camera);
      return {
        x: ((p.x + 1) * box.width) / 2,
        y: ((1 - p.y) * box.height) / 2,
      };
    };
    const projectGauges = (box: DOMRect, model: SceneModel) => {
      if (!layers) return;
      for (const [name, control] of model.controls) {
        if (!control.stick) continue;
        // The ring stays on the resting cap; its size follows the projected stick.
        const rest = model.group.localToWorld(control.point.clone());
        const center = toScreen(rest.clone(), box);
        const edge = toScreen(rest.setX(rest.x + control.stick.radius), box);
        drawStickGauge(
          layers.gauges,
          name,
          stickGauge(center, Math.abs(edge.x - center.x), control.stick),
        );
      }
    };
    // Each leader runs straight from the chip edge that faces the controller
    // to the projected control: a dark halo under a light line, so it reads
    // over the model. The chips are measured each frame, so a line meets its
    // chip in every density and whatever the chip's width. A stick's leader
    // ends on its cap and moves with it.
    const projectLabels = (box: DOMRect, model: SceneModel) => {
      if (!labels || !layers) return;
      labels.setAttribute("viewBox", `0 0 ${box.width} ${box.height}`);
      const back: SVGElement[] = [],
        front: SVGElement[] = [];
      for (const callout of model.callouts) {
        const control = model.controls.get(callout.control);
        const chip = host.querySelector<HTMLElement>(
          `[data-callout="${callout.control}"]`,
        );
        if (!control || !chip) continue;
        const rect = chip.getBoundingClientRect();
        if (rect.width <= 0 || rect.height <= 0) continue;
        const anchor = toScreen(model.anchor(control), box);
        const x = round(anchor.x),
          y = round(anchor.y);
        const start = calloutEdge(callout, {
          left: rect.left - box.left,
          top: rect.top - box.top,
          width: rect.width,
          height: rect.height,
        });
        const sx = round(start.x),
          sy = round(start.y);
        const active = control.strength > 0,
          selected = selectedControl === callout.control;
        const g = svgNode("g", {
          "data-line": callout.control,
          "data-placement": callout.placement,
          "data-active": String(active),
          "data-selected": String(selected),
          "data-bound": String(!unboundControls.has(callout.control)),
        });
        const d = `M ${sx} ${sy} L ${x} ${y}`;
        g.append(
          svgNode("path", { class: "stage-line-halo", d }),
          svgNode("path", { class: "stage-line", d }),
          svgNode("circle", {
            class: "stage-line-end",
            cx: sx,
            cy: sy,
            r: 2.2,
          }),
          svgNode("circle", {
            class: "stage-line-glow",
            cx: x,
            cy: y,
            r: 12,
          }),
          svgNode("circle", {
            class: "stage-line-anchor",
            cx: x,
            cy: y,
            r: 4.2,
          }),
        );
        // Highlighted leaders draw last so no other line crosses over them.
        (active || selected ? front : back).push(g);
      }
      layers.back.replaceChildren(...back);
      layers.front.replaceChildren(...front);
      projectGauges(box, model);
    };
    const fail = () => {
      if (detached || failed) return;
      failed = true;
      scheduler.setVisible(false);
      // Whatever went wrong is in the stage: the next mount builds a new one.
      stages.discard(stage);
      onFailure();
    };
    const unlisten = stage.onLost(fail);
    const draw = () => {
      const model = stage.model;
      if (detached || failed || !model) return;
      const box = host.getBoundingClientRect();
      const { width, height } = box;
      if (width <= 0 || height <= 0) return;
      try {
        stage.render(width, height);
        projectLabels(box, model);
        if (!ready) {
          ready = true;
          onReady();
        }
      } catch {
        fail();
      }
    };
    const detach = () => {
      if (detached) return;
      detached = true;
      scheduler.dispose();
      resize.disconnect();
      intersection.disconnect();
      document.removeEventListener("visibilitychange", visibility);
      unlisten();
      labels?.replaceChildren();
      // Nothing draws while no page holds the canvas; the stage stays.
      if (stage.canvas.parentElement === host) stage.canvas.remove();
      if (mounts.get(stage) === detach) {
        mounts.delete(stage);
        stage.park();
      }
      stages.release(stage);
    };
    // The stage is held from here on: whatever throws while the mount is set
    // up must still let go of it, or its idle time never starts and the
    // caller, which gets no mount to dispose, cannot do it.
    try {
      mounts.set(stage, detach);
      host.prepend(stage.canvas);
      resize.observe(host);
      intersection.observe(host);
      document.addEventListener("visibilitychange", visibility);
      visibility();
      const built = stage.model;
      if (built) {
        built.update(input);
        scheduler.invalidate();
        // Back on the page: draw now, not a frame later, so the model is
        // there as soon as the page is.
        scheduler.flush();
      } else {
        void stage.loaded.then(
          () => {
            const model = stage.model;
            if (detached || failed || !model) return;
            model.update(input);
            scheduler.invalidate();
          },
          () => fail(),
        );
      }
    } catch (error) {
      detach();
      throw error;
    }
    return {
      /** `unbound` lists controls whose leader is drawn dashed and dimmed. */
      update(
        next?: PadInput,
        selected?: string,
        unbound: readonly string[] = [],
      ) {
        if (detached || failed) return;
        input = next;
        selectedControl = selected;
        unboundControls = new Set(unbound);
        stage.model?.update(next);
        scheduler.invalidate();
      },
      /** Lets go of the stage, which stays for the next mount. */
      dispose: detach,
    };
  };
}

/** The scene of this app session: one stage kept for the model in use. */
export const createControllerScene = bindControllerScene(
  createStageStore<ModelInfo, SceneStage>({
    key: stageKey,
    create: createGpuStage,
    usable: (stage) => !stage.broken,
  }),
);
