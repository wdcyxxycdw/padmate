// An async handler: the first await continues in a microtask, the second
// when the host resolves the sleep.
import { key, on, sleep } from "padmate";

on("north", "tap", async () => {
  await key.tap("F20");
  await sleep(40);
  key.tap("F21");
});
