import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { type Fixture, fixture } from "./helpers";

export async function editorFixture(): Promise<Fixture> {
  const f = await fixture();
  const profiles = join(f.path, "data", "profiles");
  await writeFile(
    join(profiles, "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "Editor acceptance",
      script: { entry: "main.ts", api: "0.21" },
      analog: {
        ls: {
          mode: "standard",
          deadzone: 0.6,
          saturation: 0.95,
          directionThreshold: 0.3,
        },
      },
      layers: {
        base: {
          south: {
            label: "Undo",
            press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
          },
          east: { press: { type: "keys", keys: "F20", mode: "tap" } },
          west: { press: { type: "keys", keys: "F19", mode: "hold" } },
          north: { press: { type: "keys", keys: "F13", mode: "tap" } },
          "ls.right": { press: { type: "keys", keys: "F18", mode: "hold" } },
        },
      },
    }),
  );
  await writeFile(
    join(profiles, "paint", "main.ts"),
    `import { defineScript, defineAction, param, session, key, on, log } from "padmate";
const tools = ["F13", "F14", "F15", "F16"];
export default defineScript({
  actions: {
    cycleBrush: defineAction({
      label: "Cycle brush",
      args: {dir: param.number({default: 1, min: -1, max: 1})},
      async run({dir}) {
        const next = ((session.get("brush") ?? 0) + dir + tools.length) % tools.length;
        session.set("brush", next);
        log.info("cycle invoked", dir, next);
        await key.tap(tools[next]);
      }
    })
  },
  setup() { on("east", "press", () => { void key.tap("F21"); }, {label: "Script owns B"}); }
});
`,
  );
  // A script (macro, SCR-50) a key can run: the library is one folder for
  // every profile (SCR-51).
  await mkdir(join(f.path, "data", "macros"), { recursive: true });
  await writeFile(
    join(f.path, "data", "macros", "下一个笔刷.ts"),
    `import { key, session } from "padmate";
const tools = ["F13", "F14", "F15", "F16"];
export default async function () {
  const next = ((session.get("brush") ?? 0) + 3) % tools.length;
  session.set("brush", next);
  await key.tap(tools[next]);
}
`,
  );
  await mkdir(join(profiles, "other"));
  await writeFile(
    join(profiles, "other", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "Other profile",
      layers: { base: {} },
    }),
  );
  return f;
}
