import { defineAction, defineScript, key, log, param, session } from "padmate";

const TOOLS = ["F13", "F14", "F15", "F16"] as const;

export default defineScript({
  actions: {
    cycleBrush: defineAction({
      label: "循环笔刷",
      args: {
        dir: param.number({ label: "方向", default: 1, min: -1, max: 1, step: 2 }),
      },
      async run({ dir }) {
        const current = session.get<number>("brush") ?? 0;
        const next = (current + dir + TOOLS.length) % TOOLS.length;
        session.set("brush", next);
        await key.tap(TOOLS[next], { holdMs: 20 });
      },
    }),
  },
  onActivate() {
    log.info("cycleBrush ready");
  },
});
