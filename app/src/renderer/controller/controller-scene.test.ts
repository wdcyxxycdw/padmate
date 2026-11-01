// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import {
  calloutEdge,
  calloutSide,
  controllerCallouts,
} from "./controller-callouts";
import { stickActiveThreshold } from "./controller-model";
import {
  createFrameScheduler,
  drawStickGauge,
  stickGauge,
} from "./controller-scene";

function required<T>(value: T | undefined): T {
  if (value === undefined) throw new Error("Missing frame");
  return value;
}
describe("render scheduling", () => {
  it("coalesces updates, stays idle, and cancels hidden or disposed frames", () => {
    let next = 0;
    const pending = new Map<number, FrameRequestCallback>();
    const draw = vi.fn();
    const request = vi.fn((callback: FrameRequestCallback) => {
      pending.set(++next, callback);
      return next;
    });
    const cancel = vi.fn((id: number) => pending.delete(id));
    const scheduler = createFrameScheduler(draw, request, cancel);
    scheduler.invalidate();
    scheduler.invalidate();
    expect(request).toHaveBeenCalledTimes(1);
    const frame = required(pending.get(next));
    pending.delete(next);
    frame(0);
    expect(draw).toHaveBeenCalledTimes(1);
    expect(pending.size).toBe(0);
    scheduler.invalidate();
    scheduler.setVisible(false);
    expect(pending.size).toBe(0);
    scheduler.invalidate();
    expect(request).toHaveBeenCalledTimes(2);
    scheduler.setVisible(true);
    expect(pending.size).toBe(1);
    scheduler.dispose();
    scheduler.invalidate();
    expect(pending.size).toBe(0);
    expect(cancel).toHaveBeenCalledTimes(2);
  });
});

describe("stick gauge", () => {
  const center = { x: 100, y: 80 };
  it("shows a push as a dot on a ring around the resting cap", () => {
    const right = stickGauge(center, 34, { x: 1, y: 0 });
    expect(right).toEqual({
      cx: 100,
      cy: 80,
      r: 34,
      x: 134,
      y: 80,
      visible: true,
    });
    // SDL's +y is down, as on screen.
    const half = stickGauge(center, 34, { x: 0, y: -0.5 });
    expect([half.x, half.y, half.visible]).toEqual([100, 63, true]);
    // A corner past the circle stays on the ring.
    const corner = stickGauge(center, 30, { x: 1, y: 1 });
    expect(Math.hypot(corner.x - 100, corner.y - 80)).toBeCloseTo(30, 6);
    expect(corner.x - 100).toBeCloseTo(corner.y - 80, 6);
  });

  it("hides at rest and under drift, and keeps a readable size", () => {
    expect(stickGauge(center, 34, { x: 0, y: 0 }).visible).toBe(false);
    const drift = stickActiveThreshold * 0.9;
    expect(stickGauge(center, 34, { x: drift, y: 0 }).visible).toBe(false);
    expect(
      stickGauge(center, 34, { x: 0, y: stickActiveThreshold }).visible,
    ).toBe(true);
    // The ring follows the projected stick within 20–40 px.
    expect(stickGauge(center, 8, { x: 1, y: 0 }).r).toBe(20);
    expect(stickGauge(center, 90, { x: 1, y: 0 }).r).toBe(40);
    expect(stickGauge(center, Number.NaN, { x: 1, y: 0 }).r).toBe(20);
  });

  it("reuses one group per stick so it can fade, freezing its last reading", () => {
    const layer = document.createElementNS("http://www.w3.org/2000/svg", "g");
    const pushed = drawStickGauge(
      layer,
      "leftstick",
      stickGauge(center, 34, { x: 0.6, y: 0.8 }),
    );
    expect(pushed.getAttribute("data-visible")).toBe("true");
    const dot = pushed.querySelector(".stage-gauge-dot");
    expect([dot?.getAttribute("cx"), dot?.getAttribute("cy")]).toEqual([
      "120.4",
      "107.2",
    ]);
    expect(pushed.querySelector(".stage-gauge-ring")?.getAttribute("r")).toBe(
      "34",
    );
    expect(pushed.querySelector(".stage-gauge-line")?.getAttribute("d")).toBe(
      "M 100 80 L 120.4 107.2",
    );
    const rest = drawStickGauge(
      layer,
      "leftstick",
      stickGauge(center, 34, { x: 0, y: 0 }),
    );
    expect(rest).toBe(pushed);
    expect(layer.children).toHaveLength(1);
    expect(rest.getAttribute("data-visible")).toBe("false");
    // Fading out at the last reading instead of snapping to the middle.
    expect(dot?.getAttribute("cx")).toBe("120.4");
    drawStickGauge(layer, "rightstick", stickGauge(center, 34, { x: 0, y: 0 }));
    expect(
      [...layer.children].map((g) => [
        g.getAttribute("data-gauge"),
        g.getAttribute("data-visible"),
      ]),
    ).toEqual([
      ["leftstick", "false"],
      ["rightstick", "false"],
    ]);
  });

  it("fades only when motion is allowed", () => {
    const css = readFileSync(resolve(__dirname, "controller-3d.css"), "utf8");
    expect(css).toMatch(/\.stage-gauge\s*\{[^}]*opacity:\s*0;[^}]*transition/);
    expect(css).toMatch(
      /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.stage-gauge[^}]*transition:\s*none/,
    );
  });
});

describe("chip layout", () => {
  it.each(["xboxone", "ps5", "ps3", "switchpro"])(
    "gives every %s chip its own place",
    (type) => {
      const callouts = controllerCallouts({ type });
      const places = callouts.map((callout) =>
        callout.slot !== undefined
          ? `${calloutSide(callout)}:${callout.slot}`
          : `${callout.placement}:${callout.offset}`,
      );
      expect(new Set(places).size).toBe(callouts.length);
      for (const callout of callouts) {
        // Shoulder corners sit above the side columns, slots 0 and 1.
        if (callout.side) expect(callout.slot).toBeLessThan(2);
        if (callout.placement === "left" || callout.placement === "right")
          expect(callout.slot).toBeGreaterThanOrEqual(2);
        expect(callout.label).toBeTruthy();
      }
      // The trigger chips stay in the top band for the E2E contract.
      expect(
        callouts
          .filter((callout) => callout.control.endsWith("trigger"))
          .map((callout) => [callout.placement, callout.side]),
      ).toEqual([
        ["top", "left"],
        ["top", "right"],
      ]);
    },
  );

  it("starts leader lines on the chip edge facing the controller", () => {
    const rect = { left: 10, top: 20, width: 100, height: 40 };
    expect(calloutEdge({ placement: "left" }, rect)).toEqual({ x: 110, y: 40 });
    expect(calloutEdge({ placement: "right" }, rect)).toEqual({ x: 10, y: 40 });
    expect(calloutEdge({ placement: "top", side: "left" }, rect)).toEqual({
      x: 110,
      y: 40,
    });
    expect(calloutEdge({ placement: "top", side: "right" }, rect)).toEqual({
      x: 10,
      y: 40,
    });
    expect(calloutEdge({ placement: "top" }, rect)).toEqual({ x: 60, y: 60 });
    expect(calloutEdge({ placement: "bottom" }, rect)).toEqual({
      x: 60,
      y: 20,
    });
  });
});
