import { key, sleep } from "padmate";

// Shift goes up when the run ends.
export default async function () {
  key.down("Shift");
  await sleep(300);
}
