// SCRIPTING §8.1, stick-controlled repeat, on a stick sample instead of
// onStick's StickState.
import { key, on } from "padmate";
import { direction, RateAccumulator, remap } from "./math";
import type { StickEvent } from "./types";

const MIN_HZ = 3;
const MAX_HZ = 20;
const DEADZONE = 0.15;
const acc = new RateAccumulator();
let previous = -1;

on("rs", "stick", (st: StickEvent) => {
  const dir = direction(st.x, st.y, 4, { deadzone: DEADZONE, hysteresisDeg: 10, previous });
  previous = dir;
  if (dir !== 0 && dir !== 2) {
    acc.reset();
    return;
  }
  const hz = remap(Math.min(Math.hypot(st.x, st.y), 1), DEADZONE, 1, MIN_HZ, MAX_HZ);
  if (acc.step(hz, st.dt) > 0) {
    key.tap(dir === 0 ? "F17" : "F18");
  }
});
