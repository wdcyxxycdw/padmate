import { key, on, sleep } from "padmate";

on("A", "tap", async () => {
  key.tap("Ctrl+Z");
  await sleep(10);
  key.tap("Ctrl+Y");
});
