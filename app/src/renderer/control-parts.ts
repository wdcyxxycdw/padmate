// The words for the controls M15 adds to a stick or trigger: the rings, the
// knob ticks and the second stage of a trigger (ANA-10, ANA-11, ANA-14). The
// lists, the key preview, the controller chips and the diagnostics name them
// here; the drawers use the same words (UI-28), so change them together.
import type { Language } from "./store";

const parts: Record<string, [string, string]> = {
  inner: ["轻推", "light push"],
  outer: ["重推", "hard push"],
  cw: ["顺时针一格", "clockwise tick"],
  ccw: ["逆时针一格", "counter-clockwise tick"],
  full: ["按到底", "full pull"],
};

/**
 * What a ring, knob tick or second-stage control adds to the name of its
 * stick or trigger, e.g. ls.outer -> "重推" and rt.full -> "按到底"; undefined
 * for every other control.
 */
export function analogControlPart(
  control: string,
  language: Language,
): string | undefined {
  const match = /^(?:[lr]s\.(inner|outer|cw|ccw)|[lr]t\.(full))$/.exec(control);
  const part = match?.[1] ?? match?.[2];
  if (!part) return undefined;
  const [zh, en] = parts[part];
  return language === "en" ? en : zh;
}
