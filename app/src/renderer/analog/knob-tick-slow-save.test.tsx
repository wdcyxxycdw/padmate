// @vitest-environment jsdom
import { createHash } from "node:crypto";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type {
  DeviceInfo,
  PadmateAPI,
  ProfileSaveResult,
} from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import { fakeCapture } from "../capture-fixture";
import { editorAPIFixture } from "../editor-api-fixture";
import { createEditorStore } from "../editor-store";
import { KeyDrawer } from "../KeyDrawer";

// The two ticks of a knob, recorded one after the other in the real key
// drawer (UI-28) while the daemon is slow to answer the first save, as in
// analog-advanced.spec.ts "a knob is set up with its step and ticks". The
// profile.changed event of the save comes at an awkward moment, and the
// answer may be lost altogether (the bridge gives up after 10 s). Both
// shortcuts must reach the file, and the editor must not claim that the
// file changed elsewhere.

afterEach(() => {
  cleanup();
});
// The saves finish on timers, so React has to expect act throughout; testing
// library turns that on only when the test globals exist.
(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

const info: DeviceInfo = {
  id: 1,
  name: "Xbox",
  type: "xboxone",
  vendor: 0,
  product: 0,
  version: 0,
  guid: "",
  path: "",
  driver: "",
  serial: "",
  gamepad: true,
  virtual: true,
};
const knob: Profile = {
  schemaVersion: 1,
  name: "Knob",
  analog: { rs: { mode: "knob", knobStepDeg: 30 } },
  layers: {
    base: { "ls.right": { press: { type: "keys", keys: "F18" } } },
  },
};
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
const sleep = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));
type Bindings = Record<string, { press?: { keys?: string } } | undefined>;
const keysOf = (text: string) => {
  const base = (JSON.parse(text) as Profile).layers.base as Bindings;
  const press = (control: string) => base[control]?.press?.keys;
  return { "rs.cw": press("rs.cw"), "rs.ccw": press("rs.ccw") };
};

interface Slowness {
  /** How long a put takes after the file is written (the profile is applied). */
  putMs: number;
  /**
   * When the profile.changed event of a save reaches the editor: "before"
   * the answer (the daemon publishes it first), or this many ms after the
   * write.
   */
  event: "before" | number;
  /** The first put writes the file but its answer never comes. */
  lostFirstAnswer?: boolean;
}

async function setup(slow: Slowness) {
  const fixture = editorAPIFixture();
  const text = JSON.stringify(knob);
  let disk = { text, revision: hash(text) };
  let puts = 0;
  let editor: ReturnType<typeof createEditorStore> | undefined;
  const api = {
    ...fixture,
    getProfile: async (id: string) => {
      await sleep(5);
      return { id, text: disk.text, revision: disk.revision, diagnostics: [] };
    },
    putProfile: vi.fn(
      async (
        _id: string,
        next: string,
        revision: string,
      ): Promise<ProfileSaveResult> => {
        await sleep(5);
        if (revision !== disk.revision)
          throw {
            message: "revision conflict",
            code: -32009,
            data: { revision: disk.revision },
          };
        disk = { text: next, revision: hash(next) };
        const saved = disk.revision;
        // The App calls profileChanged when the event arrives.
        setTimeout(
          () => void editor?.getState().profileChanged(),
          slow.event === "before" ? 0 : slow.event,
        );
        await sleep(slow.putMs);
        if (slow.lostFirstAnswer && ++puts === 1)
          throw { message: "Daemon request timed out" };
        return { revision: saved, applied: true };
      },
    ),
  };
  const { record } = fakeCapture(api);
  editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "device-a", connection: "c" });
  render(
    <KeyDrawer
      editor={editor}
      control="rightstick"
      onControl={() => {}}
      info={info}
      language="zh-CN"
      onClose={() => {}}
    />,
  );
  return { editor, record, disk: () => disk };
}

const row = (control: string) => {
  const found = document.querySelector<HTMLButtonElement>(
    `.analog-row[data-control="${control}"]`,
  );
  if (!found) throw new Error(`no ${control} row`);
  return found;
};

for (const slow of [
  { putMs: 0, event: "before" },
  { putMs: 200, event: "before" },
  { putMs: 120, event: 250 },
  { putMs: 150, event: "before", lostFirstAnswer: true },
  { putMs: 150, event: 300, lostFirstAnswer: true },
] satisfies Slowness[]) {
  it(`records both ticks while the first save is slow (${JSON.stringify(slow)})`, async () => {
    const { editor, record, disk } = await setup(slow);
    const panel = screen.getByRole("region", { name: "摇杆设置" });
    fireEvent.click(row("rs.cw"));
    await record("F21", {
      scope: within(panel).getByRole("region", { name: "顺时针一格" }),
    });
    // The second tick is recorded at once: nothing waits for the first save.
    fireEvent.click(row("rs.ccw"));
    await record("F22", {
      scope: within(panel).getByRole("region", { name: "逆时针一格" }),
    });
    expect(await act(() => editor.getState().flush())).toBe(true);
    // The events still on their way arrive.
    await act(() => sleep((slow.event === "before" ? 0 : slow.event) + 50));
    expect(keysOf(disk().text)).toEqual({ "rs.cw": "F21", "rs.ccw": "F22" });
    const state = editor.getState();
    expect(state).toMatchObject({
      reloaded: false,
      saveProblem: "",
      unsaved: false,
    });
    expect(keysOf(JSON.stringify(state.draft))).toEqual({
      "rs.cw": "F21",
      "rs.ccw": "F22",
    });
  });
}
