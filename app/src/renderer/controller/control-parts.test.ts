import { expect, it } from "vitest";
import { analogControlPart } from "./control-parts";

it("names the rings, knob ticks and second stage, and nothing else", () => {
  for (const [control, zh, en] of [
    ["ls.inner", "轻推", "light push"],
    ["rs.outer", "重推", "hard push"],
    ["ls.cw", "顺时针一格", "clockwise tick"],
    ["rs.ccw", "逆时针一格", "counter-clockwise tick"],
    ["lt.full", "按到底", "full pull"],
    ["rt.full", "按到底", "full pull"],
  ] as const) {
    expect(analogControlPart(control, "zh-CN"), control).toBe(zh);
    expect(analogControlPart(control, "en"), control).toBe(en);
  }
  for (const control of [
    "ls.up",
    "ls.active",
    "lt",
    "lefttrigger+",
    "ls.inner2",
    "lt.half",
    "constructor",
    "",
  ])
    expect(analogControlPart(control, "zh-CN"), control).toBeUndefined();
});
