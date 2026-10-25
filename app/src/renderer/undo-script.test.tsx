// @vitest-environment jsdom
// G7 (UI-43): picking a script for a key in the drawer is one undo step, and
// Ctrl+Z takes it back, whatever the pad reports while its setting says it
// cannot send keys to padmate's own window.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  DaemonEvent,
  MacroList,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { App } from "./App";
import { editorAPIFixture } from "./editor-api-fixture";
import { createPadmateStore } from "./store";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const status = (sendToOwnWindow = false): StatusResult => ({
  version: "0.10.0",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "none",
  sendToOwnWindow,
  profileId: "paint",
  slots: [],
  devices: [
    {
      info: {
        id: 1,
        name: "Test controller",
        type: "xbox",
        vendor: 0,
        product: 0,
        version: 0,
        guid: "test",
        driver: "virtual",
        path: "",
        serial: "",
        gamepad: true,
        virtual: true,
      },
      key: "controller",
      slot: 0,
      status: "mapped",
      disabled: false,
    },
  ],
});

const library: MacroList = {
  macros: [{ file: "脚本 1.ts", name: "脚本 1", usedBy: [] }],
  missing: [],
  legacy: [],
};

const keys = (value: string) => ({ type: "keys" as const, keys: value });

/**
 * The app against a daemon that behaves as padmated does: a save publishes
 * profile.changed (the profile reload's "active", then "saved") after its
 * answer, and the runtimes report script.status on a steady tick.
 */
function launch({
  sendToOwnWindow = false,
  config = '{"schemaVersion":0}',
}: {
  sendToOwnWindow?: boolean;
  config?: string;
} = {}) {
  let listener = (_event: DaemonEvent) => {};
  const fixture = editorAPIFixture();
  let text = JSON.stringify({
    schemaVersion: 1,
    name: "Paint",
    layers: { base: { "dpad.right": { tap: keys("Ctrl+D") } } },
  } satisfies Profile);
  let revision = 1;
  const event = (topic: DaemonEvent["topic"], data: unknown) =>
    act(() => listener({ topic, data }));
  fixture.getProfile.mockImplementation(async (id: string) => ({
    id,
    text,
    revision: `r${revision}`,
    diagnostics: [],
  }));
  fixture.putProfile.mockImplementation(async (_id, next) => {
    text = next;
    revision++;
    const saved = `r${revision}`;
    // The events come a batch later than the answer (Main's event buffer).
    setTimeout(() => {
      listener({
        topic: "profile.changed",
        data: { kind: "active", id: "paint", revision: saved, generation: 2 },
      });
      listener({
        topic: "profile.changed",
        data: { kind: "saved", id: "paint", revision: saved, applied: true },
      });
    }, 20);
    return { revision: saved, applied: true };
  });
  fixture.listMacros.mockResolvedValue(library);
  const api = {
    ...fixture,
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next: (event: DaemonEvent) => void) => {
      listener = next;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => status(sendToOwnWindow)),
    getDevices: vi.fn(async () => status(sendToOwnWindow).devices),
    pause: vi.fn(),
    resume: vi.fn(),
    releaseAll: vi.fn(),
    getConfig: vi.fn(async () => ({ text: config, revision: "c" })),
    putConfig: vi.fn(),
    getScriptStatus: vi.fn(async () => []),
    getScriptRegistrations: vi.fn(async () => []),
    reloadScripts: vi.fn(async () => []),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(),
    reconnect: vi.fn(async () => {}),
  } as unknown as PadmateAPI;
  const store = createPadmateStore(api);
  render(<App store={store} />);
  return {
    api: api as typeof api & ReturnType<typeof editorAPIFixture>,
    event,
    profile: () => JSON.parse(text) as Profile,
  };
}

const tap = (app: ReturnType<typeof launch>) =>
  app.profile().layers.base["dpad.right"]?.tap;

/** The mapping page, D-pad right's drawer, 点按 set to the script 脚本 1. */
async function pickScript(app: ReturnType<typeof launch>) {
  await screen.findByRole("group", { name: /Test controller/ });
  await waitFor(() => expect(app.api.getProfile).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: /^→ · / }));
  const drawer = screen.getByRole("complementary", { name: "按键编辑" });
  fireEvent.click(
    within(within(drawer).getByTestId("action-type")).getByRole("radio", {
      name: "脚本",
    }),
  );
  const select = await within(drawer).findByRole("combobox", {
    name: "选择脚本",
  });
  select.focus();
  fireEvent.change(select, { target: { value: "脚本 1.ts" } });
  await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(1));
  expect(tap(app)).toEqual({ type: "macro", file: "脚本 1.ts" });
  return { drawer, select };
}

const settle = (ms = 60) => act(() => new Promise((r) => setTimeout(r, ms)));

const ctrl = (key: string, init: KeyboardEventInit = {}) =>
  fireEvent.keyDown(document.activeElement ?? document.body, {
    key,
    code: `Key${key.toUpperCase()}`,
    ctrlKey: true,
    ...init,
  });

describe("undoing a script picked in the drawer", () => {
  it("takes the pick back with Ctrl+Z, and redoes it with Ctrl+Y", async () => {
    const app = launch();
    const { drawer } = await pickScript(app);
    // The daemon's events and the tick of script.status arrive.
    await settle();
    app.event("script.status", []);
    await settle();
    expect(document.querySelector("dialog[open]")).toBeNull();
    ctrl("z");
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(2));
    expect(tap(app)).toEqual(keys("Ctrl+D"));
    // The drawer shows the shortcut again, not a script left chosen.
    await waitFor(() =>
      expect(
        within(drawer)
          .getByRole("radio", { name: "快捷键" })
          .getAttribute("aria-checked"),
      ).toBe("true"),
    );
    await settle();
    ctrl("y");
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(3));
    expect(tap(app)).toEqual({ type: "macro", file: "脚本 1.ts" });
  });

  it("is not held back by a pad that reports a pushed button while its keys cannot reach this window", async () => {
    const app = launch({ sendToOwnWindow: false });
    await screen.findByRole("group", { name: /Test controller/ });
    // A button the daemon never reported released, and a stick off centre.
    app.event("input", { device: 1, kind: "down", control: "east" });
    app.event("input", {
      device: 1,
      kind: "axis",
      control: "leftx",
      value: 20000,
    });
    await pickScript(app);
    await settle();
    ctrl("z");
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(2));
    expect(tap(app)).toEqual(keys("Ctrl+D"));
  });

  it("is not held back by a pad pressed a moment ago when its keys cannot reach this window", async () => {
    const app = launch({ sendToOwnWindow: false });
    await pickScript(app);
    app.event("input", { device: 1, kind: "down", control: "east" });
    app.event("input", { device: 1, kind: "up", control: "east" });
    ctrl("z");
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(2));
    expect(tap(app)).toEqual(keys("Ctrl+D"));
  });

  it("does nothing while a pad may send its Ctrl+Z to this window", async () => {
    const app = launch({ sendToOwnWindow: true });
    await pickScript(app);
    await settle();
    ctrl("z");
    await settle(120);
    expect(app.api.putProfile).toHaveBeenCalledTimes(1);
    expect(tap(app)).toEqual({ type: "macro", file: "脚本 1.ts" });
  });

  it("does nothing when the setting was just switched on in config.json", async () => {
    const app = launch({
      config: '{"schemaVersion":0,"output":{"sendToOwnWindow":true}}',
    });
    await pickScript(app);
    await settle();
    ctrl("z");
    await settle(120);
    expect(app.api.putProfile).toHaveBeenCalledTimes(1);
  });

  it("works after the scripts page's dialogs were used, and none is left open", async () => {
    const app = launch();
    app.api.renameMacro.mockResolvedValue({
      file: "新名.ts",
      bindings: 0,
      profiles: [],
    });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    const article = (
      await screen.findByRole("heading", { name: "脚本 1" })
    ).closest("article") as HTMLElement;
    // 新建 and cancel; 导入 with a replace question, confirmed; 重命名; 删除.
    fireEvent.click(screen.getByRole("button", { name: "新建" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "新建脚本" })).getByRole(
        "button",
        { name: "取消" },
      ),
    );
    app.api.importMacro.mockResolvedValueOnce({
      exists: true,
      name: "脚本 1.ts",
      token: "a".repeat(32),
    });
    fireEvent.click(screen.getByRole("button", { name: "导入" }));
    fireEvent.click(
      within(
        await screen.findByRole("dialog", { name: /已存在，是否替换/ }),
      ).getByRole("button", { name: "替换" }),
    );
    await waitFor(() => expect(app.api.importMacro).toHaveBeenCalledTimes(2));
    fireEvent.click(within(article).getByTestId("script-menu"));
    fireEvent.click(screen.getByRole("menuitem", { name: "重命名" }));
    const rename = screen.getByRole("dialog", { name: "重命名" });
    fireEvent.change(within(rename).getByRole("textbox", { name: "名称" }), {
      target: { value: "新名" },
    });
    fireEvent.click(within(rename).getByRole("button", { name: "确定" }));
    await waitFor(() => expect(app.api.renameMacro).toHaveBeenCalled());
    fireEvent.click(within(article).getByTestId("script-menu"));
    fireEvent.click(screen.getByRole("menuitem", { name: "删除" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: /^删除/ })).getByRole(
        "button",
        { name: "删除" },
      ),
    );
    await waitFor(() => expect(app.api.deleteMacro).toHaveBeenCalled());
    await settle();
    expect(document.querySelector("dialog[open]")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "映射" }));
    await pickScript(app);
    await settle();
    expect(document.querySelector("dialog[open]")).toBeNull();
    ctrl("z");
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledTimes(2));
    expect(tap(app)).toEqual(keys("Ctrl+D"));
  });
});
