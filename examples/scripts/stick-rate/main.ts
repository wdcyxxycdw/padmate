import { key, onStick } from "padmate";
import { RateAccumulator, remap } from "padmate/math";

const MIN_HZ = 3;
const MAX_HZ = 20;
const acc = new RateAccumulator();
let previous = -1;

onStick("rs", (stick, dt) => {
  const direction = stick.direction(4);
  if (direction !== 0 && direction !== 2) {
    acc.reset();
    previous = -1;
    return;
  }
  // Do not carry accumulated credit across an up/down reversal.
  if (direction !== previous) acc.reset();
  previous = direction;
  const hz = remap(stick.magnitude, 0, 1, MIN_HZ, MAX_HZ);
  if (acc.step(hz, dt) > 0) {
    void key.tap(direction === 0 ? "F17" : "F18", { holdMs: 20 });
  }
});
