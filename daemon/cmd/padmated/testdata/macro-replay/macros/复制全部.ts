import { key, sleep } from "padmate";

export default async function () {
  await key.tap("Ctrl+A");
  await sleep(50);
  await key.tap("Ctrl+C");
}
