// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Profiler, type ProfilerOnRenderCallback } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigGetResult,
  ConfigPutResult,
  DaemonEvent,
  DeviceBattery,
  DeviceEntry,
  DeviceIdentify,
  MacroList,
  PadmateAPI,
  ProfileEntry,
  ScriptStatusEntry,
  StartupState,
  StatusResult,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { App } from "./App";
import { listControlName } from "./control-names";
import { editorAPIFixture } from "./editor-api-fixture";
import { IDENTIFY_COOLDOWN_MS } from "./pages/CalibrationDialog";
import { conflictName, conflictNames } from "./pages/ConflictNotices";
import { NoController } from "./pages/EmptyState";
import { logFields } from "./pages/LogsPage";
import { versionText } from "./pages/SettingsPage";
import { createPadmateStore } from "./store";

const status: StatusResult = {
  version: "0.10.0",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "none",
  profileId: "paint",
  slots: [
    {
      slot: 0,
      device: 1,
      key: "controller",
      name: "Test controller",
      profile: "Paint display name",
      generation: 1,
      layers: { base: "base", overlays: ["tools"], color: "" },
      latches: ["west"],
      mode: 0,
    },
  ],
  devices: [
    {
      info: {
        id: 1,
        name: "Test controller",
        type: "switchpro",
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
  conflicts: [
    {
      kind: "conflict",
      process: "steam.exe",
      message: "Steam is running",
      document: "docs/troubleshooting.md",
    },
  ],
  combos: [
    { device: 1, family: "standard", buttons: ["back", "start"], holdMs: 2000 },
  ],
};

function withType(type: string): StatusResult {
  return {
    ...status,
    devices: status.devices.map((device) => ({
      ...device,
      info: { ...device.info, type },
    })),
  };
}

function setup({
  scripts = [],
  registrations = [],
  config = { text: '{"schemaVersion":0}', revision: "rev" },
  snapshot = status,
  profiles = [],
  profile,
  onRender,
}: {
  scripts?: ScriptStatusEntry[];
  registrations?: ScriptStatusEntry[];
  config?: ConfigGetResult | Error;
  snapshot?: StatusResult;
  profiles?: ProfileEntry[];
  /** The document the mapping page edits; an empty one by default. */
  profile?: Profile;
  /** Called for each commit of the app, to count renders. */
  onRender?: ProfilerOnRenderCallback;
} = {}) {
  let listener = (_event: DaemonEvent) => {};
  let revision = 0;
  const fixture = editorAPIFixture();
  fixture.listProfiles.mockImplementation(async () => profiles);
  if (profile)
    fixture.getProfile.mockImplementation(async (id: string) => ({
      id,
      text: JSON.stringify(profile),
      revision: "r1",
      diagnostics: [],
    }));
  const api: PadmateAPI = {
    ...fixture,
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next) => {
      listener = next;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => snapshot),
    getDevices: vi.fn(async () => snapshot.devices),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    getConfig: vi.fn(async () => {
      if (config instanceof Error) throw config;
      return config;
    }),
    putConfig: vi.fn(
      async (): Promise<ConfigPutResult> => ({
        revision: `saved-${++revision}`,
        applied: true,
        restartRequired: false,
      }),
    ),
    getScriptStatus: vi.fn(async () => scripts),
    getScriptRegistrations: vi.fn(async () => registrations),
    reloadScripts: vi.fn(async () => scripts),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
  };
  const store = createPadmateStore(api);
  render(
    onRender ? (
      <Profiler id="app" onRender={onRender}>
        <App store={store} />
      </Profiler>
    ) : (
      <App store={store} />
    ),
  );
  return {
    api,
    store,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => listener({ topic, data })),
  };
}

async function openPage(name: "脚本" | "设置") {
  await screen.findByRole("group", { name: /Test controller/ });
  fireEvent.click(screen.getByRole("button", { name }));
}

/** Logs is a view inside Settings: 设置 → 日志 查看. */
async function openLogs() {
  await openPage("设置");
  fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
  expect(screen.getByRole("heading", { name: "日志" })).toBeTruthy();
}

/** Diagnostics is a view inside Settings too: 设置 → 诊断 查看. */
async function openDiagnostics() {
  await openPage("设置");
  fireEvent.click(screen.getByRole("button", { name: "查看诊断" }));
  expect(screen.getByRole("heading", { name: "诊断", level: 1 })).toBeTruthy();
}

function pageText() {
  return document.querySelector(".page-scroll")?.textContent ?? "";
}

// Settings has no setting about layers (restoreLayerState is not written).
// 浮层, the overlay's section, is not about them either.
const layerText = /(?<!浮)层|layer/i;

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("desktop workspace", () => {
  it("keeps the overview focused on the live controller without dashboard cards", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    await waitFor(() =>
      expect(app.api.getProfile).toHaveBeenCalledWith("paint"),
    );
    expect(app.api.getProfile).not.toHaveBeenCalledWith("Paint display name");
    for (const label of [
      "演练模式",
      "输出预览",
      "当前配置",
      "已连接的手柄",
      "tools",
    ]) {
      expect(screen.queryByText(label)).toBeNull();
    }
    const controller = screen.getByRole("group", { name: /Test controller/ });
    expect(
      controller.querySelector('[data-control="south"]')?.textContent,
    ).toBe("B");
    expect(screen.queryByText("Ctrl+Z")).toBeNull();
    app.event("input", { device: 1, kind: "down", control: "south" });
    expect(
      controller
        .querySelector('[data-control="south"]')
        ?.getAttribute("data-pressed"),
    ).toBe("true");
    app.event("output.preview", {
      chord: "LCtrl+Z",
      localDryRun: true,
      dryRun: true,
    });
    expect(screen.queryByText("Ctrl+Z")).toBeNull();
  });

  it("names the four pages in the navigation rail; logs live under Settings", async () => {
    setup();
    await screen.findByRole("group", { name: /Test controller/ });
    const nav = screen.getByRole("navigation");
    expect(
      within(nav)
        .getAllByRole("button")
        .map((button) => button.getAttribute("aria-label")),
    ).toEqual(["映射", "配置", "脚本", "设置"]);
    fireEvent.click(within(nav).getByRole("button", { name: "配置" }));
    expect(
      screen.getByRole("heading", { name: "配置", level: 1 }),
    ).toBeTruthy();
    expect(document.title).toBe("padmate · 配置");
    expect(
      within(nav)
        .getByRole("button", { name: "配置" })
        .getAttribute("aria-current"),
    ).toBe("page");
  });

  it("switches language by keyboard and saves the preference", async () => {
    setup();
    await screen.findByRole("group", { name: /Test controller/ });
    const user = userEvent.setup();
    screen.getByRole("button", { name: "设置" }).focus();
    await user.keyboard("{Enter}");
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "语言" }),
      "en",
    );
    expect(screen.getByRole("heading", { name: "Settings" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Language" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "View logs" }).textContent).toBe(
      "View",
    );
    // Nothing was saved, so there is no save state to show.
    expect(screen.queryByText(/saved/i)).toBeNull();
    expect(localStorage.getItem("padmate.language")).toBe("en");
    expect(document.documentElement.lang).toBe("en");
  });

  it("pauses and resumes through the service, without optimistic state", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "暂停映射" }));
    await user.click(await screen.findByRole("button", { name: "恢复映射" }));
    expect(api.pause).toHaveBeenCalledTimes(1);
    expect(api.resume).toHaveBeenCalledTimes(1);
  });
});

describe("overlay settings (HUD-05)", () => {
  const prefs = (change: Record<string, unknown> = {}) => ({
    language: "zh-CN",
    toasts: true,
    position: "bottom",
    opacity: 100,
    ...change,
  });
  const section = () => screen.getByTestId("overlay-settings");

  it("has a 浮层 section with exactly three rows, at their defaults, and no helper text", async () => {
    setup();
    await openPage("设置");
    expect(
      screen.getByRole("heading", { name: "浮层", level: 2 }),
    ).toBeTruthy();
    // Beside 通用, then 反馈, and before 安全: the UI's own preferences come first.
    const headings = [...document.querySelectorAll(".section-title")].map(
      (heading) => heading.textContent,
    );
    expect(headings.slice(0, 4)).toEqual(["通用", "浮层", "反馈", "安全"]);
    const rows = [...section().querySelectorAll(".row")];
    expect(
      rows.map((row) => row.querySelector(".row-title")?.textContent),
    ).toEqual(["提示消息", "位置", "不透明度"]);
    expect(section().querySelector(".row-hint")).toBeNull();

    const toasts = within(section()).getByRole("switch", { name: "提示消息" });
    expect(toasts.getAttribute("aria-checked")).toBe("true");
    const position = within(section()).getByRole("radiogroup", {
      name: "位置",
    });
    const places = within(position).getAllByRole("radio");
    expect(places.map((radio) => radio.textContent)).toEqual(["底部", "顶部"]);
    expect(places.map((radio) => radio.getAttribute("aria-checked"))).toEqual([
      "true",
      "false",
    ]);
    const opacity = within(section()).getByRole("slider", {
      name: "不透明度",
    }) as HTMLInputElement;
    expect([opacity.min, opacity.max, opacity.step, opacity.value]).toEqual([
      "40",
      "100",
      "5",
      "100",
    ]);
    expect(opacity.getAttribute("aria-valuetext")).toBe("100%");
    expect(within(section()).getByText("100%")).toBeTruthy();
    // They are the UI's own: nothing was written to the daemon's config.
    expect(localStorage.getItem("padmate.overlay.toasts")).toBeNull();
  });

  it("is in English with the UI", async () => {
    setup();
    await openPage("设置");
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    expect(
      screen.getByRole("heading", { name: "Overlay", level: 2 }),
    ).toBeTruthy();
    const rows = [...section().querySelectorAll(".row")];
    expect(
      rows.map((row) => row.querySelector(".row-title")?.textContent),
    ).toEqual(["Toasts", "Position", "Opacity"]);
    expect(
      within(section())
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["Bottom", "Top"]);
    expect(
      within(section()).getByRole("slider", { name: "Opacity" }),
    ).toBeTruthy();
  });

  it("saves each change at once to localStorage and pushes it to Main", async () => {
    const app = setup();
    await openPage("设置");
    // Pushed once at start, with the defaults.
    expect(app.api.setOverlayPrefs).toHaveBeenCalledTimes(1);
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(prefs());

    fireEvent.click(
      within(section()).getByRole("switch", { name: "提示消息" }),
    );
    expect(
      within(section())
        .getByRole("switch", { name: "提示消息" })
        .getAttribute("aria-checked"),
    ).toBe("false");
    expect(localStorage.getItem("padmate.overlay.toasts")).toBe("off");
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ toasts: false }),
    );

    fireEvent.click(within(section()).getByRole("radio", { name: "顶部" }));
    expect(
      within(section())
        .getByRole("radio", { name: "顶部" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(localStorage.getItem("padmate.overlay.position")).toBe("top");
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ toasts: false, position: "top" }),
    );

    fireEvent.change(
      within(section()).getByRole("slider", { name: "不透明度" }),
      {
        target: { value: "65" },
      },
    );
    expect(within(section()).getByText("65%")).toBeTruthy();
    expect(localStorage.getItem("padmate.overlay.opacity")).toBe("65");
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ toasts: false, position: "top", opacity: 65 }),
    );
    expect(app.api.setOverlayPrefs).toHaveBeenCalledTimes(4);

    // Back on, and at the bottom again.
    fireEvent.click(
      within(section()).getByRole("switch", { name: "提示消息" }),
    );
    expect(localStorage.getItem("padmate.overlay.toasts")).toBe("on");
    fireEvent.click(within(section()).getByRole("radio", { name: "底部" }));
    expect(localStorage.getItem("padmate.overlay.position")).toBe("bottom");
    // None of it touched the mapping's config.json.
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("changes the position by keyboard like the other segmented controls", async () => {
    const app = setup();
    await openPage("设置");
    const user = userEvent.setup();
    within(section()).getByRole("radio", { name: "底部" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(localStorage.getItem("padmate.overlay.position")).toBe("top");
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ position: "top" }),
    );
  });

  it("pushes the language with the settings when the language changes", async () => {
    const app = setup();
    await openPage("设置");
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ language: "en" }),
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Language" }), {
      target: { value: "zh-CN" },
    });
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(prefs());
  });

  it("starts with what localStorage holds, shows it and pushes it", async () => {
    localStorage.setItem("padmate.overlay.toasts", "off");
    localStorage.setItem("padmate.overlay.position", "top");
    localStorage.setItem("padmate.overlay.opacity", "55");
    localStorage.setItem("padmate.language", "en");
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    expect(app.api.setOverlayPrefs).toHaveBeenCalledWith({
      language: "en",
      toasts: false,
      position: "top",
      opacity: 55,
    });
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(
      within(section())
        .getByRole("switch", { name: "Toasts" })
        .getAttribute("aria-checked"),
    ).toBe("false");
    expect(
      within(section())
        .getByRole("radio", { name: "Top" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(
      (
        within(section()).getByRole("slider", {
          name: "Opacity",
        }) as HTMLInputElement
      ).value,
    ).toBe("55");
  });

  it("falls back to the defaults for values it cannot use", async () => {
    localStorage.setItem("padmate.overlay.position", "left");
    localStorage.setItem("padmate.overlay.opacity", "20");
    const app = setup();
    await openPage("设置");
    expect(app.api.setOverlayPrefs).toHaveBeenCalledWith(prefs());
    expect(
      (
        within(section()).getByRole("slider", {
          name: "不透明度",
        }) as HTMLInputElement
      ).value,
    ).toBe("100");
  });

  it("works with settings that do not need the mapping's config: a broken one only shows its notice", async () => {
    const app = setup({ config: new Error("no config") });
    await openPage("设置");
    fireEvent.click(
      within(section()).getByRole("switch", { name: "提示消息" }),
    );
    expect(localStorage.getItem("padmate.overlay.toasts")).toBe("off");
    expect(app.api.setOverlayPrefs).toHaveBeenLastCalledWith(
      prefs({ toasts: false }),
    );
  });
});

describe("feedback settings (UI-27, ADR-0067)", () => {
  const section = () => screen.getByTestId("feedback-settings");
  const rows = () =>
    [...section().querySelectorAll(".row")].map(
      (row) => row.querySelector(".row-title")?.textContent,
    );
  const config = (feedback?: unknown) => ({
    text: JSON.stringify({
      schemaVersion: 0,
      ...(feedback === undefined ? {} : { feedback }),
    }),
    revision: "rev",
  });
  const sent = (app: ReturnType<typeof setup>, n = 0) =>
    JSON.parse(vi.mocked(app.api.putConfig).mock.calls[n]?.[0] as string);

  it("has a 反馈 section of exactly four switches, after 浮层 and before 安全, with no helper text", async () => {
    setup();
    await openPage("设置");
    const headings = [...document.querySelectorAll(".section-title")].map(
      (heading) => heading.textContent,
    );
    expect(headings.slice(0, 4)).toEqual(["通用", "浮层", "反馈", "安全"]);
    expect(
      screen.getByRole("heading", { name: "反馈", level: 2 }),
    ).toBeTruthy();
    expect(rows()).toEqual(["震动", "灯光", "暂停提示", "低电提醒"]);
    expect(section().querySelector(".row-hint")).toBeNull();
    // On by default; a switch row has no other control.
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"]) {
      const row = within(section()).getByRole("switch", { name });
      expect(row.getAttribute("aria-checked")).toBe("true");
      expect((row as HTMLButtonElement).disabled).toBe(false);
    }
    expect(section().querySelectorAll("button, input, select").length).toBe(4);
  });

  it("is in English with the UI", async () => {
    setup();
    await openPage("设置");
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    expect(
      screen.getByRole("heading", { name: "Feedback", level: 2 }),
    ).toBeTruthy();
    expect(rows()).toEqual([
      "Rumble",
      "Lights",
      "Pause alert",
      "Low battery alert",
    ]);
    expect(section().querySelector(".row-hint")).toBeNull();
  });

  it("shows the switches the file has off", async () => {
    setup({
      config: config({ rumble: false, lowBattery: false, pause: true }),
    });
    await openPage("设置");
    const on = (name: string) =>
      within(section())
        .getByRole("switch", { name })
        .getAttribute("aria-checked");
    expect([on("震动"), on("灯光"), on("暂停提示"), on("低电提醒")]).toEqual([
      "false",
      "true",
      "true",
      "false",
    ]);
  });

  it("writes false for a switch that goes off, only that member, with the revision it read", async () => {
    const app = setup({
      config: {
        text: '{"schemaVersion":0,"timing":{"holdMs":300},"rules":[{"exe":"paint.exe","profile":"paint"}]}',
        revision: "rev",
      },
    });
    await openPage("设置");
    fireEvent.click(within(section()).getByRole("switch", { name: "震动" }));
    await screen.findByText("已自动保存");
    const put = vi.mocked(app.api.putConfig);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put.mock.calls[0]?.[1]).toBe("rev");
    expect(sent(app)).toEqual({
      schemaVersion: 0,
      timing: { holdMs: 300 },
      rules: [{ exe: "paint.exe", profile: "paint" }],
      feedback: { rumble: false },
    });
    expect(
      within(section())
        .getByRole("switch", { name: "震动" })
        .getAttribute("aria-checked"),
    ).toBe("false");
    // The others keep their value, and the next write uses the new revision.
    fireEvent.click(
      within(section()).getByRole("switch", { name: "低电提醒" }),
    );
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(put.mock.calls[1]?.[1]).toBe("saved-1");
    expect(sent(app, 1).feedback).toEqual({ rumble: false, lowBattery: false });
  });

  it("removes the member when a switch goes on, and the object with the last one", async () => {
    const app = setup({ config: config({ led: false }) });
    await openPage("设置");
    fireEvent.click(within(section()).getByRole("switch", { name: "灯光" }));
    await screen.findByText("已自动保存");
    const written = sent(app);
    expect(written).toEqual({ schemaVersion: 0 });
    expect("feedback" in written).toBe(false);
  });

  it("shows the new value at once and blocks other changes while it saves", async () => {
    const app = setup();
    let finish = (_value: ConfigPutResult) => {};
    vi.mocked(app.api.putConfig).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await openPage("设置");
    const rumble = within(section()).getByRole("switch", { name: "震动" });
    fireEvent.click(rumble);
    expect(screen.getByText("正在保存…")).toBeTruthy();
    expect(rumble.getAttribute("aria-checked")).toBe("false");
    expect((rumble as HTMLButtonElement).disabled).toBe(true);
    expect(
      (
        within(section()).getByRole("switch", {
          name: "灯光",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await act(async () =>
      finish({ revision: "next", applied: true, restartRequired: false }),
    );
    await screen.findByText("已自动保存");
    expect((rumble as HTMLButtonElement).disabled).toBe(false);
    // The daemon applies it at once: no restart note.
    expect(screen.queryByText("部分设置需重启 padmate 后生效。")).toBeNull();
  });

  it("keeps a change made elsewhere when the save meets a revision conflict", async () => {
    const app = setup();
    vi.mocked(app.api.putConfig).mockRejectedValueOnce({
      message: "revision conflict",
      code: -32009,
    });
    vi.mocked(app.api.getConfig).mockResolvedValue({
      text: '{"schemaVersion":0,"feedback":{"led":false},"timing":{"holdMs":250}}',
      revision: "elsewhere",
    });
    await openPage("设置");
    fireEvent.click(
      within(section()).getByRole("switch", { name: "暂停提示" }),
    );
    await screen.findByText("已自动保存");
    const put = vi.mocked(app.api.putConfig);
    expect(put).toHaveBeenCalledTimes(2);
    expect(put.mock.calls[1]?.[1]).toBe("elsewhere");
    expect(sent(app, 1)).toEqual({
      schemaVersion: 0,
      feedback: { led: false, pause: false },
      timing: { holdMs: 250 },
    });
  });

  it("reports a rejected save and shows the stored value again", async () => {
    const app = setup();
    vi.mocked(app.api.putConfig).mockRejectedValueOnce(
      new Error("config changed outside padmate"),
    );
    await openPage("设置");
    fireEvent.click(within(section()).getByRole("switch", { name: "震动" }));
    await screen.findByText("未保存，请重试");
    expect(
      within(section())
        .getByRole("switch", { name: "震动" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(screen.getByRole("alert").textContent).toContain(
      "config changed outside padmate",
    );
  });

  it("cannot be changed while config.json cannot be written", async () => {
    setup({ config: new Error("no profile library") });
    await openPage("设置");
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"])
      expect(
        (within(section()).getByRole("switch", { name }) as HTMLButtonElement)
          .disabled,
      ).toBe(true);
  });
});

describe("settings page", () => {
  it("saves each change at once without writing restoreLayerState", async () => {
    const app = setup({
      config: {
        text: '{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}]}',
        revision: "rev",
      },
    });
    await openPage("设置");
    // The save state is shown only while saving or just after it.
    const saveState = document.querySelector(".page-autosave");
    expect(saveState?.getAttribute("role")).toBe("status");
    expect(saveState?.textContent).toBe("");
    expect(pageText()).not.toMatch(layerText);
    const own = screen.getByRole("switch", {
      name: "在 padmate 窗口中也发送按键",
    });
    expect(own.getAttribute("aria-checked")).toBe("false");
    expect(screen.getByText("用于调试")).toBeTruthy();
    expect(screen.queryByText("已开启")).toBeNull();
    fireEvent.click(own);
    await screen.findByText("已自动保存");
    const put = vi.mocked(app.api.putConfig);
    expect(put).toHaveBeenCalledTimes(1);
    const [text, revision] = put.mock.calls[0] ?? [];
    expect(revision).toBe("rev");
    // Only its own field: switching by app belongs to the profiles page.
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      rules: [{ exe: "paint.exe", profile: "paint" }],
      output: { sendToOwnWindow: true },
    });
    expect(
      screen
        .getByRole("switch", { name: "在 padmate 窗口中也发送按键" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    // A small tag next to the title, not a separate warning.
    expect(screen.getByText("已开启").closest(".row-title")).toBeTruthy();
    expect(screen.queryByText("部分设置需重启 padmate 后生效。")).toBeNull();

    // Switching profiles by app moved to the 配置 page.
    expect(screen.queryByRole("switch", { name: /切换配置/ })).toBeNull();
    expect(screen.queryByText("重启后生效")).toBeNull();
    fireEvent.click(
      screen.getByRole("switch", { name: "在 padmate 窗口中也发送按键" }),
    );
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    const [second, secondRevision] = put.mock.calls[1] ?? [];
    expect(secondRevision).toBe("saved-1");
    expect(JSON.parse(second as string)).toEqual({
      schemaVersion: 0,
      rules: [{ exe: "paint.exe", profile: "paint" }],
      output: { sendToOwnWindow: false },
    });
  });

  it("keeps an existing restoreLayerState value untouched", async () => {
    const app = setup({
      config: {
        text: '{"schemaVersion":0,"restoreLayerState":false,"enabled":false}',
        revision: "rev",
      },
    });
    await openPage("设置");
    fireEvent.click(
      screen.getByRole("switch", { name: "在 padmate 窗口中也发送按键" }),
    );
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    const [text] = vi.mocked(app.api.putConfig).mock.calls[0] ?? [];
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      restoreLayerState: false,
      enabled: false,
      output: { sendToOwnWindow: true },
    });
  });

  it("saves the key gap on leaving its field, only from 0 to 50", async () => {
    const app = setup({
      config: {
        text: '{"schemaVersion":0,"output":{"sendToOwnWindow":true}}',
        revision: "rev",
      },
    });
    await openPage("设置");
    const gap = screen.getByRole("spinbutton", {
      name: "按键间隔",
    }) as HTMLInputElement;
    expect(gap.value).toBe("0");
    expect(screen.getByText("个别软件未收到按键时，请调大此值")).toBeTruthy();
    expect(gap.closest(".number-with-unit")?.textContent).toBe("毫秒");
    // Outside 0–50 or not a whole number: marked, not saved, and the saved
    // value comes back when the field is left.
    for (const value of ["51", "2.5", ""]) {
      fireEvent.change(gap, { target: { value } });
      expect(gap.getAttribute("aria-invalid")).toBe("true");
      fireEvent.blur(gap);
      expect(gap.value).toBe("0");
      expect(gap.getAttribute("aria-invalid")).toBeNull();
    }
    expect(app.api.putConfig).not.toHaveBeenCalled();
    fireEvent.change(gap, { target: { value: "10" } });
    expect(gap.getAttribute("aria-invalid")).toBeNull();
    expect(app.api.putConfig).not.toHaveBeenCalled();
    fireEvent.keyDown(gap, { key: "Enter" });
    await screen.findByText("已自动保存");
    const put = vi.mocked(app.api.putConfig);
    expect(put).toHaveBeenCalledTimes(1);
    // Only its own field.
    expect(JSON.parse(put.mock.calls[0]?.[0] as string)).toEqual({
      schemaVersion: 0,
      output: { sendToOwnWindow: true, eventGapMs: 10 },
    });
    expect(
      (screen.getByRole("spinbutton", { name: "按键间隔" }) as HTMLInputElement)
        .value,
    ).toBe("10");
    // Leaving the field unchanged saves nothing.
    fireEvent.change(gap, { target: { value: "10" } });
    fireEvent.blur(gap);
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("keeps release and pause usable while the key gap saves", async () => {
    const app = setup();
    let finish = (_value: ConfigPutResult) => {};
    vi.mocked(app.api.putConfig).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await openPage("设置");
    const gap = screen.getByRole("spinbutton", { name: "按键间隔" });
    fireEvent.change(gap, { target: { value: "10" } });
    // A click on a button first takes the focus from the field, which
    // starts the save.
    fireEvent.blur(gap);
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    const release = screen.getByRole("button", { name: "立即松开" });
    expect((release as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(release);
    await waitFor(() => expect(app.api.releaseAll).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "暂停映射" }));
    await waitFor(() => expect(app.api.pause).toHaveBeenCalledTimes(1));
    finish({ revision: "rev2", applied: true, restartRequired: false });
    await screen.findByText("已自动保存");
  });

  it("shows the key gap row in English", async () => {
    localStorage.setItem("padmate.language", "en");
    setup({
      config: {
        text: '{"schemaVersion":0,"output":{"eventGapMs":20}}',
        revision: "rev",
      },
    });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    const gap = screen.getByRole("spinbutton", {
      name: "Key gap",
    }) as HTMLInputElement;
    expect(gap.value).toBe("20");
    expect(screen.getByText("Raise it if an app misses keys")).toBeTruthy();
    expect(gap.closest(".number-with-unit")?.textContent).toBe("ms");
  });

  it("shows the pending value and blocks other changes while saving", async () => {
    const app = setup();
    let finish = (_value: ConfigPutResult) => {};
    vi.mocked(app.api.putConfig).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await openPage("设置");
    const own = screen.getByRole("switch", {
      name: "在 padmate 窗口中也发送按键",
    });
    fireEvent.click(own);
    expect(screen.getByText("正在保存…")).toBeTruthy();
    expect(own.getAttribute("aria-checked")).toBe("true");
    expect((own as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(own);
    expect(app.api.putConfig).toHaveBeenCalledTimes(1);
    await act(async () =>
      finish({ revision: "next", applied: true, restartRequired: true }),
    );
    await screen.findByText("已自动保存");
    expect((own as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the save state only briefly", async () => {
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout"],
    });
    try {
      setup();
      await openPage("设置");
      fireEvent.click(
        screen.getByRole("switch", { name: "在 padmate 窗口中也发送按键" }),
      );
      await screen.findByText("已自动保存");
      // Let the effect that hides it start its timer first.
      await act(async () => {});
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(screen.queryByText("已自动保存")).toBeNull();
      expect(
        document.querySelector(".page-autosave")?.getAttribute("data-state"),
      ).toBe("idle");
    } finally {
      vi.useRealTimers();
    }
  });

  it("reports a rejected save and shows the stored value again", async () => {
    const app = setup();
    vi.mocked(app.api.putConfig).mockRejectedValueOnce(
      new Error("config changed outside padmate"),
    );
    await openPage("设置");
    fireEvent.click(
      screen.getByRole("switch", { name: "在 padmate 窗口中也发送按键" }),
    );
    await screen.findByText("未保存，请重试");
    expect(
      screen
        .getByRole("switch", { name: "在 padmate 窗口中也发送按键" })
        .getAttribute("aria-checked"),
    ).toBe("false");
    expect(screen.getByRole("alert").textContent).toContain(
      "config changed outside padmate",
    );
  });

  it("explains a session without a configuration and keeps local settings usable", async () => {
    const app = setup({ config: new Error("no profile library") });
    await openPage("设置");
    expect(screen.getByText("当前启动方式无法保存设置")).toBeTruthy();
    expect(screen.getByText("请使用配置库启动 padmate。")).toBeTruthy();
    expect(screen.getByText("no profile library")).toBeTruthy();
    expect(document.querySelector(".page-autosave")).toBeNull();
    expect(
      (
        screen.getByRole("switch", {
          name: "在 padmate 窗口中也发送按键",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (screen.getByRole("combobox", { name: "语言" }) as HTMLSelectElement)
        .disabled,
    ).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "立即松开" }));
    await waitFor(() => expect(app.api.releaseAll).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("已松开")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "重新读取" }));
    await waitFor(() => expect(app.api.getConfig).toHaveBeenCalledTimes(2));
  });

  it("shows configuration problems and pending restarts as notices", async () => {
    setup({
      config: {
        text: "{}",
        revision: "rev",
        problem: "config.json:3:1: unexpected token",
      },
    });
    await openPage("设置");
    expect(screen.getByText("无法读取设置文件")).toBeTruthy();
    expect(screen.getByText("请修复 config.json 后重新读取。")).toBeTruthy();
    expect(screen.getByText("config.json:3:1: unexpected token")).toBeTruthy();
    expect(
      (
        screen.getByRole("switch", {
          name: "在 padmate 窗口中也发送按键",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    cleanup();
    setup({
      config: {
        text: '{"schemaVersion":0,"enabled":false}',
        revision: "rev",
        restartRequired: true,
      },
    });
    await openPage("设置");
    // The daemon does not say which setting waits for the restart, so the
    // note is not tied to a row.
    expect(
      screen
        .getByText("部分设置需重启 padmate 后生效。")
        .closest("[role=status]"),
    ).toBeTruthy();
    expect(screen.queryByText("重启后生效")).toBeNull();
  });

  it("shows the emergency combination of the controller and the version", async () => {
    setup({ snapshot: withType("xboxone") });
    await openPage("设置");
    expect(screen.getByRole("img", { name: "View + Menu" })).toBeTruthy();
    expect(screen.getByText("按住 2 秒：松开所有按键并暂停")).toBeTruthy();
    expect(screen.getByText("padmate v0.10.0")).toBeTruthy();
    const change = screen.getByRole("button", { name: "修改紧急暂停" });
    expect(change.textContent).toBe("修改");
    expect((change as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the combo the daemon reports, a vertical Joy-Con's too", async () => {
    setup({
      snapshot: {
        ...withType("joyconleft"),
        combos: [
          {
            device: 1,
            family: "joyconLeftVertical",
            buttons: ["paddle2", "paddle4"],
            holdMs: 1500,
          },
        ],
      },
    });
    await openPage("设置");
    expect(screen.getByRole("img", { name: "SL + SR" })).toBeTruthy();
    expect(screen.getByText("按住 1.5 秒：松开所有按键并暂停")).toBeTruthy();
  });

  it("cannot change the combo without a controller", async () => {
    setup({ snapshot: { ...status, devices: [], slots: [], combos: [] } });
    fireEvent.click(await screen.findByRole("button", { name: "设置" }));
    expect(screen.getByText("连接手柄后可查看和修改")).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "修改紧急暂停",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(screen.queryByRole("img", { name: /\+/ })).toBeNull();
  });

  it("saves the hold and multi-tap times when a field is left", async () => {
    const app = setup({
      config: {
        text: '{"schemaVersion":0,"timing":{"multiTapMs":300}}',
        revision: "rev",
      },
    });
    await openPage("设置");
    const hold = screen.getByRole("textbox", {
      name: "触发长按时间",
    }) as HTMLInputElement;
    const multi = screen.getByRole("textbox", {
      name: "连击间隔",
    }) as HTMLInputElement;
    // Empty is the built-in default, which the placeholder shows.
    expect(hold.value).toBe("");
    expect(hold.placeholder).toBe("250");
    expect(multi.value).toBe("300");
    expect(multi.placeholder).toBe("220");
    // The hold time has no helper line under it (F14).
    const holdRow = hold.closest(".row") as HTMLElement;
    expect(holdRow.querySelector(".row-hint")).toBeNull();
    expect(hold.getAttribute("aria-describedby")).toBeNull();
    expect(screen.queryByText("按住超过这个时间算长按")).toBeNull();
    expect(screen.getByText("两次点按的间隔在此时间内视为连击")).toBeTruthy();
    const put = vi.mocked(app.api.putConfig);

    fireEvent.change(hold, { target: { value: "400" } });
    expect(put).not.toHaveBeenCalled();
    fireEvent.blur(hold);
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(JSON.parse(put.mock.calls[0]?.[0] as string)).toEqual({
      schemaVersion: 0,
      timing: { multiTapMs: 300, holdMs: 400 },
    });
    await screen.findByText("已自动保存");

    // Enter saves too; empty removes the value.
    fireEvent.change(multi, { target: { value: "" } });
    fireEvent.keyDown(multi, { key: "Enter" });
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(JSON.parse(put.mock.calls[1]?.[0] as string)).toEqual({
      schemaVersion: 0,
      timing: { holdMs: 400 },
    });

    // Out of range, not a whole number (a stray key), or the saved value
    // again: nothing is written, and the built-in default is not saved.
    for (const value of ["60001", "-5", "1e", "-", "300-", "4 00", "400"]) {
      fireEvent.change(hold, { target: { value } });
      if (value !== "400")
        expect(hold.getAttribute("aria-invalid")).toBe("true");
      fireEvent.blur(hold);
      expect(hold.value).toBe("400");
    }
    expect(put).toHaveBeenCalledTimes(2);
    expect(pageText()).not.toMatch(layerText);
  });

  it("records a new emergency combo on the controller", async () => {
    const app = setup();
    await openPage("设置");
    expect(screen.getByRole("img", { name: "− + +" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    expect(dialog.textContent).toContain("按住新的组合键，然后松开");
    await waitFor(() =>
      expect(app.api.padCaptureStart).toHaveBeenCalledTimes(1),
    );
    // Not a capture that stays: Main ends it when the window loses the front.
    expect(app.api.padCaptureStart).toHaveBeenCalledWith();
    const save = within(dialog).getByRole("button", { name: "保存" });
    const restore = within(dialog).getByRole("button", { name: "恢复默认" });
    // Nothing recorded or changed, and nothing set to restore.
    expect((save as HTMLButtonElement).disabled).toBe(true);
    expect((restore as HTMLButtonElement).disabled).toBe(true);
    const hold = (buttons: string[]) => {
      for (const control of buttons)
        app.event("input", { device: 1, kind: "down", control });
      for (const control of buttons)
        app.event("input", { device: 1, kind: "up", control });
    };

    // One button, or five, is not a combo.
    hold(["south"]);
    expect(within(dialog).getByRole("alert").textContent).toBe(
      "需同时按住 2–4 个按键",
    );
    expect((save as HTMLButtonElement).disabled).toBe(true);
    hold(["south", "east", "west", "north", "leftshoulder"]);
    expect(within(dialog).getByRole("alert")).toBeTruthy();
    expect((save as HTMLButtonElement).disabled).toBe(true);

    // The buttons held together, once all are let go.
    app.event("input", { device: 1, kind: "down", control: "leftshoulder" });
    app.event("input", { device: 1, kind: "down", control: "east" });
    expect(within(dialog).getByRole("img", { name: "L + A" })).toBeTruthy();
    app.event("input", { device: 1, kind: "up", control: "leftshoulder" });
    app.event("input", { device: 1, kind: "up", control: "east" });
    expect(within(dialog).queryByRole("alert")).toBeNull();
    expect(within(dialog).getByRole("img", { name: "L + A" })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("radio", { name: "3 秒" }));
    expect((save as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(
      JSON.parse(vi.mocked(app.api.putConfig).mock.calls[0]?.[0] as string),
    ).toEqual({
      schemaVersion: 0,
      safety: {
        panic: {
          standard: { buttons: ["leftshoulder", "east"], holdMs: 3000 },
        },
      },
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "修改紧急暂停" })).toBeNull(),
    );
    expect(app.api.padCaptureStop).toHaveBeenCalledTimes(1);
  });

  it("ends the recording when the controller goes away", async () => {
    const app = setup();
    await openPage("设置");
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    await waitFor(() =>
      expect(app.api.padCaptureStart).toHaveBeenCalledTimes(1),
    );
    vi.mocked(app.api.getStatus).mockResolvedValue({
      ...status,
      devices: [],
      slots: [],
      combos: [],
    });
    app.event("input", { device: 1, kind: "removed" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "修改紧急暂停" })).toBeNull(),
    );
    expect(app.api.padCaptureStop).toHaveBeenCalledTimes(1);
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("restores the default combo of the controller's family", async () => {
    const app = setup({
      config: {
        text: JSON.stringify({
          schemaVersion: 0,
          safety: {
            panic: {
              standard: { buttons: ["south", "east"], holdMs: 1000 },
              joyconLeft: { buttons: ["south", "east"], holdMs: 1000 },
            },
          },
        }),
        revision: "rev",
      },
    });
    await openPage("设置");
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    fireEvent.click(within(dialog).getByRole("button", { name: "恢复默认" }));
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    // Only this family's entry goes.
    expect(
      JSON.parse(vi.mocked(app.api.putConfig).mock.calls[0]?.[0] as string),
    ).toEqual({
      schemaVersion: 0,
      safety: {
        panic: { joyconLeft: { buttons: ["south", "east"], holdMs: 1000 } },
      },
    });
    await waitFor(() =>
      expect(app.api.padCaptureStop).toHaveBeenCalledTimes(1),
    );
  });

  it("shows a failed combo save inside the dialog", async () => {
    const app = setup();
    await openPage("设置");
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    vi.mocked(app.api.putConfig).mockRejectedValueOnce(
      new Error("config.json is locked"),
    );
    fireEvent.click(within(dialog).getByRole("radio", { name: "3 秒" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    // The shell's alert would sit under the modal dialog.
    await waitFor(() =>
      expect(within(dialog).getByRole("alert").textContent).toBe(
        "config.json is locked",
      ),
    );
    expect(screen.getAllByText("config.json is locked")).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "修改紧急暂停" })).toBe(dialog);
    expect(
      (
        within(dialog).getByRole("button", {
          name: "保存",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("records nothing pressed while the window is in the background", async () => {
    const app = setup();
    await openPage("设置");
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    const save = within(dialog).getByRole("button", {
      name: "保存",
    }) as HTMLButtonElement;
    const press = (kind: "down" | "up", ...controls: string[]) => {
      for (const control of controls)
        app.event("input", { device: 1, kind, control });
    };
    // Half a recording, then the window loses focus: it is dropped.
    press("down", "leftshoulder", "east");
    act(() => {
      window.dispatchEvent(new Event("blur"));
    });
    press("up", "leftshoulder", "east");
    // A shortcut used in another program is no recording either.
    press("down", "south", "west");
    press("up", "south", "west");
    expect(save.disabled).toBe(true);
    expect(within(dialog).getByRole("img", { name: "− + +" })).toBeTruthy();
    // Back with a button still held: that press does not count.
    press("down", "north");
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    press("down", "south");
    press("up", "north", "south");
    expect(save.disabled).toBe(true);
    // Once all are up, the next combo records.
    press("down", "leftshoulder", "east");
    press("up", "leftshoulder", "east");
    expect(within(dialog).getByRole("img", { name: "L + A" })).toBeTruthy();
    expect(save.disabled).toBe(false);
  });

  it("keeps a hold time that is not one of the usual ones", async () => {
    setup({
      snapshot: {
        ...status,
        combos: [
          {
            device: 1,
            family: "standard",
            buttons: ["back", "start"],
            holdMs: 2500,
          },
        ],
      },
    });
    await openPage("设置");
    expect(screen.getByText("按住 2.5 秒：松开所有按键并暂停")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    const current = within(dialog).getByRole("radio", { name: "2.5 秒" });
    expect(current.getAttribute("aria-checked")).toBe("true");
    const save = within(dialog).getByRole("button", {
      name: "保存",
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.click(within(dialog).getByRole("radio", { name: "2 秒" }));
    expect(save.disabled).toBe(false);
  });

  it("closes the combo dialog when its controller goes away, another one staying", async () => {
    const joycon: DeviceEntry = {
      ...status.devices[0],
      info: {
        ...status.devices[0].info,
        id: 2,
        name: "Joy-Con (L)",
        type: "joyconleft",
      },
      key: "joycon",
      slot: 1,
    };
    const app = setup({
      snapshot: {
        ...withType("xboxone"),
        devices: [...withType("xboxone").devices, joycon],
        combos: [
          ...(status.combos ?? []),
          {
            device: 2,
            family: "joyconLeft",
            buttons: ["leftshoulder", "rightshoulder"],
            holdMs: 2000,
          },
        ],
      },
    });
    await openPage("设置");
    act(() => app.store.getState().selectDevice(2));
    expect(screen.getByRole("img", { name: "SL + SR" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    fireEvent.click(within(dialog).getByRole("radio", { name: "3 秒" }));
    app.event("input", { device: 2, kind: "removed" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "修改紧急暂停" })).toBeNull(),
    );
    // The Xbox controller's row shows, and its dialog opens fresh.
    expect(screen.getByRole("img", { name: "View + Menu" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    const fresh = screen.getByRole("dialog", { name: "修改紧急暂停" });
    expect(
      within(fresh)
        .getByRole("radio", { name: "2 秒" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("keeps settings to short rows without explanations", async () => {
    setup();
    await openPage("设置");
    const text = pageText();
    for (const removed of [
      "修改会自动保存",
      "界面显示的语言",
      "config.json",
      "padmatectl",
      "后台服务已连接",
      "关闭这个窗口",
      "下次启动",
    ])
      expect(text).not.toContain(removed);
    const about = screen
      .getByRole("heading", { name: "关于" })
      .closest("section") as HTMLElement;
    expect(
      [...about.querySelectorAll(".row-title")].map((row) => row.textContent),
    ).toEqual(["日志", "诊断", "padmate v0.10.0"]);
    expect(
      within(about).getByRole("button", { name: "查看日志" }),
    ).toBeTruthy();
    expect(
      within(about).getByRole("button", { name: "查看诊断" }),
    ).toBeTruthy();
    expect(
      within(about).getByRole("button", { name: "关于与许可" }),
    ).toBeTruthy();
  });

  it("opens logs inside Settings and goes back with the back control or Escape", async () => {
    setup();
    await openLogs();
    const nav = screen.getByRole("navigation");
    expect(
      within(nav)
        .getByRole("button", { name: "设置" })
        .getAttribute("aria-current"),
    ).toBe("page");
    expect(document.title).toBe("padmate · 日志");
    const back = screen.getByRole("button", { name: "返回设置" });
    expect(document.activeElement).toBe(back);
    fireEvent.click(back);
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看日志" }),
    );

    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    const search = screen.getByLabelText("搜索日志");
    fireEvent.change(search, { target: { value: "brush" } });
    // A filled search box clears itself first; Escape then goes back.
    fireEvent.keyDown(search, { key: "Escape" });
    expect(screen.getByRole("heading", { name: "日志" })).toBeTruthy();
    fireEvent.change(search, { target: { value: "" } });
    fireEvent.keyDown(search, { key: "Escape" });
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "返回设置" })).toBeNull();

    // The rail item leads back to Settings itself.
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    fireEvent.click(within(nav).getByRole("button", { name: "设置" }));
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
  });

  it("drops the focus return to the logs button when Settings cannot show", async () => {
    const { api } = setup();
    await openLogs();
    const connection = vi.mocked(api.onConnection).mock.calls.at(-1)?.[0];
    act(() => connection?.({ phase: "disconnected", message: "gone" }));
    // Going back while offline shows the connection panel, not Settings.
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("button", { name: "查看日志" })).toBeNull();
    act(() => connection?.({ phase: "connected" }));
    const logs = await screen.findByRole("button", { name: "查看日志" });
    expect(document.activeElement).not.toBe(logs);
  });

  it("opens the diagnostics inside Settings, next to the logs, and goes back with the back control or Escape (UI-20)", async () => {
    setup();
    await openDiagnostics();
    const nav = screen.getByRole("navigation");
    // Still four pages: diagnostics has no rail item of its own.
    expect(
      within(nav)
        .getAllByRole("button")
        .map((button) => button.getAttribute("aria-label")),
    ).toEqual(["映射", "配置", "脚本", "设置"]);
    expect(
      within(nav)
        .getByRole("button", { name: "设置" })
        .getAttribute("aria-current"),
    ).toBe("page");
    expect(document.title).toBe("padmate · 诊断");
    const back = screen.getByRole("button", { name: "返回设置" });
    expect(document.activeElement).toBe(back);
    fireEvent.click(back);
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看诊断" }),
    );

    fireEvent.click(screen.getByRole("button", { name: "查看诊断" }));
    const filter = screen.getByRole("searchbox", { name: "筛选按键" });
    fireEvent.change(filter, { target: { value: "south" } });
    // A filled filter clears itself first; Escape then goes back.
    fireEvent.keyDown(filter, { key: "Escape" });
    expect(
      screen.getByRole("heading", { name: "诊断", level: 1 }),
    ).toBeTruthy();
    fireEvent.change(filter, { target: { value: "" } });
    fireEvent.keyDown(filter, { key: "Escape" });
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "返回设置" })).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看诊断" }),
    );

    // The rail item leads back to Settings itself.
    fireEvent.click(screen.getByRole("button", { name: "查看诊断" }));
    fireEvent.click(within(nav).getByRole("button", { name: "设置" }));
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
  });

  it("gives the focus back to the button of the view that was left, the logs or the diagnostics", async () => {
    setup();
    await openLogs();
    fireEvent.click(screen.getByRole("button", { name: "返回设置" }));
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看日志" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "查看诊断" }));
    fireEvent.click(screen.getByRole("button", { name: "返回设置" }));
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看诊断" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    fireEvent.click(screen.getByRole("button", { name: "返回设置" }));
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "查看日志" }),
    );
  });

  it("drops the focus return to the diagnostics button when Settings cannot show", async () => {
    const { api } = setup();
    await openDiagnostics();
    const connection = vi.mocked(api.onConnection).mock.calls.at(-1)?.[0];
    act(() => connection?.({ phase: "disconnected", message: "gone" }));
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("button", { name: "查看诊断" })).toBeNull();
    act(() => connection?.({ phase: "connected" }));
    const button = await screen.findByRole("button", { name: "查看诊断" });
    expect(document.activeElement).not.toBe(button);
  });

  it("shows no events while the connection is down, and starts a new list when it is back (UI-20)", async () => {
    const { api, event } = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    event("engine.activity", {
      kind: "decision",
      slot: 0,
      at: Date.now(),
      decision: { control: "east", edge: "down", reason: "unbound" },
    });
    await openDiagnostics();
    expect(
      within(screen.getByRole("list")).getAllByRole("listitem"),
    ).toHaveLength(1);
    const connection = vi.mocked(api.onConnection).mock.calls.at(-1)?.[0];
    act(() => connection?.({ phase: "disconnected", message: "gone" }));
    // The connection panel is in place of the page: the rows that the store
    // still keeps cannot be looked at (ADR-0071 clears them on reconnect).
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.queryByRole("searchbox", { name: "筛选按键" })).toBeNull();
    expect(document.querySelector(".page-act-row")).toBeNull();
    act(() => connection?.({ phase: "connected" }));
    expect(document.querySelector(".page-act-row")).toBeNull();
    await waitFor(() =>
      expect(screen.getByText(/按下手柄上的键后/)).toBeTruthy(),
    );
  });

  it("lists the presses made while another page was open, newest first (UI-20, UI-21)", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    const decision = (fields: Record<string, unknown>, extra = {}) =>
      app.event("engine.activity", {
        kind: "decision",
        slot: 0,
        at: Date.now(),
        decision: {
          control: "south",
          edge: "down",
          gesture: "press",
          ...fields,
        },
        ...extra,
      });
    decision({
      level: "base",
      binding: "/layers/base/south",
      action: "tap Ctrl+Z",
      reason: "dispatch",
    });
    decision({ control: "east", reason: "unbound" });
    app.event("engine.activity", {
      kind: "unrouted",
      at: Date.now(),
      device: 5,
      control: "north",
      down: true,
      why: "notGamepad",
    });
    await openDiagnostics();
    const rows = within(screen.getByRole("list")).getAllByRole("listitem");
    expect(
      rows.map((row) => row.querySelector(".page-act-result")?.textContent),
    ).toEqual([
      "这只手柄没有标准映射",
      "当前配置里这个键没有设置动作",
      "Ctrl+Z只预览",
    ]);
  });

  it("does not render the whole window for each press it records (UI-20)", async () => {
    const onRender = vi.fn();
    const app = setup({ onRender });
    await screen.findByRole("group", { name: /Test controller/ });
    await waitFor(() =>
      expect(app.api.getProfile).toHaveBeenCalledWith("paint"),
    );
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 80));
    });
    onRender.mockClear();
    for (let press = 0; press < 5; press++)
      app.event("engine.activity", {
        kind: "decision",
        slot: 0,
        decision: { control: "east", reason: "spurious" },
      });
    await act(async () => {});
    expect(app.store.getState().activity).toHaveLength(5);
    expect(onRender).not.toHaveBeenCalled();
  });

  it("shows only why the mapping service did not start, until a retry connects (UI-45)", async () => {
    const { api } = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    const connection = vi.mocked(api.onConnection).mock.calls.at(-1)?.[0];
    const failure = {
      reason: "report" as const,
      report: {
        kind: "config" as const,
        cause: "invalid" as const,
        file: "C:\\data\\config.json",
        line: 14,
        col: 20,
        pointer: "/safety/panic/standard/buttons",
        message: "minItems: got 1, want 2",
        dataDir: "C:\\data",
        errors: [],
      },
    };
    act(() => connection?.({ phase: "failed", failure }));
    expect(screen.getByTestId("start-summary").textContent).toBe(
      "设置文件 config.json 第 14 行第 20 列：紧急暂停至少需要 2 个按键。",
    );
    // No app around it: no rail, no top bar.
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByTitle("配置菜单")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(api.retryStart).toHaveBeenCalledOnce();
    act(() => connection?.({ phase: "connecting", failure }));
    expect(
      (screen.getByRole("button", { name: "正在重试…" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    act(() => connection?.({ phase: "connected", pid: 7, protocol: 1 }));
    expect(screen.queryByTestId("start-failure")).toBeNull();
    expect(
      await screen.findByRole("group", { name: /Test controller/ }),
    ).toBeTruthy();
    // A connection lost later is the usual offline panel.
    act(() => connection?.({ phase: "disconnected", message: "gone" }));
    expect(screen.queryByTestId("start-failure")).toBeNull();
    expect(screen.getByRole("button", { name: "重新连接" })).toBeTruthy();
  });

  it("saves 配置设置 at once and leaves the mapping page without asking", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    const dialog = screen.getByRole("dialog", { name: "配置设置" });
    expect(dialog.textContent).not.toContain("保存");
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "Renamed" } });
    // Typing is not saved; leaving the field is.
    expect(app.api.putProfile).not.toHaveBeenCalled();
    fireEvent.blur(name);
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledOnce());
    const [id, text] = vi.mocked(app.api.putProfile).mock.calls[0] ?? [];
    expect(id).toBe("paint");
    expect(JSON.parse(text as string).name).toBe("Renamed");
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    // Renaming the profile is not undone (UI-43).
    fireEvent.keyDown(document.body, { key: "z", ctrlKey: true });
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    expect(app.api.putProfile).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("heading", { name: "设置" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "映射" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("group", { name: /Test controller/ })).toBeTruthy();
  });

  it("commits the field being typed in and saves before the window closes", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    const name = screen.getByRole("textbox", { name: "名称" });
    name.focus();
    fireEvent.change(name, { target: { value: "Closing" } });
    expect(app.api.putProfile).not.toHaveBeenCalled();
    // Main asks once the window is closing.
    const closing = vi.mocked(app.api.onClosing).mock.calls.at(-1)?.[0];
    act(() => closing?.());
    await waitFor(() => expect(app.api.readyToClose).toHaveBeenCalledOnce());
    const put = vi.mocked(app.api.putProfile);
    expect(JSON.parse(put.mock.calls[0]?.[1] as string).name).toBe("Closing");
    expect(put.mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(app.api.readyToClose).mock.invocationCallOrder[0] as number,
    );
  });

  it("saves a profile's timing when it is left, and not text that is no time", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    const hold = screen.getByRole("textbox", { name: "触发长按时间" });
    fireEvent.change(hold, { target: { value: "1e" } });
    fireEvent.blur(hold);
    expect(screen.getByRole("alert").textContent).toBe("请输入 0–60000 的整数");
    expect(app.api.putProfile).not.toHaveBeenCalled();
    fireEvent.change(hold, { target: { value: "300" } });
    fireEvent.keyDown(hold, { key: "Enter" });
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledOnce());
    const [, text] = vi.mocked(app.api.putProfile).mock.calls[0] ?? [];
    expect(JSON.parse(text as string).timing).toEqual({ holdMs: 300 });
  });

  it("names the fixed default by its ID and keeps its name (PRO-28)", async () => {
    localStorage.setItem("padmate.onboarding", "done");
    setup({
      snapshot: { ...status, profileId: "default" },
      profiles: [{ id: "default", name: "Stored", default: true }],
      profile: { schemaVersion: 1, name: "Stored", layers: { base: {} } },
    });
    await screen.findByRole("group", { name: /Test controller/ });
    const menu = screen.getByTitle("配置菜单");
    await waitFor(() => expect(menu.textContent).toContain("默认"));
    expect(menu.textContent).not.toContain("Stored");
    fireEvent.click(menu);
    expect(
      screen
        .getByRole("menuitemradio", { name: "默认" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    const name = screen.getByRole("textbox", { name: "名称" });
    expect((name as HTMLInputElement).value).toBe("默认");
    expect(name.hasAttribute("readonly")).toBe(true);
    expect(
      screen.getByRole("dialog", { name: "配置设置" }).textContent,
    ).toContain("仅影响配置「默认」");
  });

  it("opens and closes the about and licenses dialog", async () => {
    setup();
    await openPage("设置");
    fireEvent.click(screen.getByRole("button", { name: "关于与许可" }));
    const dialog = screen.getByRole("dialog", { name: "关于与许可" });
    expect(dialog.textContent).toContain("MIT");
    expect(dialog.textContent).toContain("CC BY 4.0");
    expect(dialog.textContent).toContain("Rusfort");
    // The button icons (ADR-0079 §7): Kenney's under CC0, PromptFont's under
    // the OFL, whose text goes with them.
    expect(dialog.textContent).toContain("Kenney");
    expect(dialog.textContent).toContain("CC0");
    expect(dialog.textContent).toContain("PromptFont");
    expect(
      dialog.querySelector(".page-licenses-text pre")?.textContent,
    ).toMatch(/SIL OPEN FONT LICENSE Version 1\.1/);
    fireEvent.click(screen.getByRole("button", { name: "关闭关于与许可" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("the function key setting (UI-30, ADR-0075)", () => {
  const row = () => screen.getByTestId("function-key-setting");
  const group = (name: string | RegExp = /^(功能键|Function key)$/) =>
    within(row()).getByRole("radiogroup", { name });
  const radios = () => within(group()).getAllByRole("radio");
  /** The key that is chosen, by its name in the file. */
  const chosen = () =>
    radios()
      .find((radio) => radio.getAttribute("aria-checked") === "true")
      ?.getAttribute("data-value");
  const config = (functionKey?: unknown) => ({
    text: JSON.stringify({
      schemaVersion: 0,
      ...(functionKey === undefined ? {} : { functionKey }),
    }),
    revision: "rev",
  });
  const sent = (app: ReturnType<typeof setup>, n = 0) =>
    JSON.parse(vi.mocked(app.api.putConfig).mock.calls[n]?.[0] as string);
  const dialog = () => screen.queryByRole("dialog", { name: /Game Bar/ });
  const choose = (value: string) =>
    fireEvent.click(
      group().querySelector(`[data-value="${value}"]`) as HTMLElement,
    );

  it("is a row of 通用 named 功能键, with the three keys as the controller shows them, Menu by default", async () => {
    setup({ snapshot: withType("xboxone") });
    await openPage("设置");
    const general = screen.getByRole("heading", { name: "通用", level: 2 })
      .parentElement as HTMLElement;
    expect(general.contains(row())).toBe(true);
    // The icon and the name the controller prints, not a text that says where.
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "Menu",
      "View",
      "Xbox",
    ]);
    expect(radios().map((radio) => radio.getAttribute("data-value"))).toEqual([
      "start",
      "back",
      "guide",
    ]);
    for (const radio of radios())
      expect(radio.querySelector(".control-icon")).not.toBeNull();
    expect(chosen()).toBe("start");
    expect(
      radios().every((radio) => !(radio as HTMLButtonElement).disabled),
    ).toBe(true);
    // A name and a choice, with no helper text.
    expect(row().querySelector(".row-hint")).toBeNull();
    expect(within(row()).queryByRole("combobox")).toBeNull();
  });

  it("names the keys as the connected controller does, and as an Xbox controller does without one", async () => {
    // The controller of the fixtures is a Switch Pro controller.
    setup();
    await openPage("设置");
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "+",
      "−",
      "Home",
    ]);
    cleanup();
    setup({ snapshot: { ...status, devices: [], slots: [], combos: [] } });
    fireEvent.click(await screen.findByRole("button", { name: "设置" }));
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "Menu",
      "View",
      "Xbox",
    ]);
  });

  it("is in English with the UI, and is moved by the arrow keys", async () => {
    const app = setup({ snapshot: withType("xboxone") });
    await openPage("设置");
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    expect(group("Function key")).toBeTruthy();
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "Menu",
      "View",
      "Xbox",
    ]);
    fireEvent.keyDown(radios()[0], { key: "ArrowRight" });
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(sent(app).functionKey).toBe("back");
  });

  it("shows the key the file chooses, and Menu for anything else", async () => {
    setup({ config: config("guide") });
    await openPage("设置");
    expect(chosen()).toBe("guide");
    cleanup();
    setup({ config: config("back") });
    await openPage("设置");
    expect(chosen()).toBe("back");
    cleanup();
    setup({ config: config("select") });
    await openPage("设置");
    expect(chosen()).toBe("start");
  });

  it("writes the choice at once, only that member, with the revision it read", async () => {
    const app = setup({
      config: {
        text: JSON.stringify({ schemaVersion: 0, timing: { holdMs: 300 } }),
        revision: "rev-1",
      },
    });
    await openPage("设置");
    choose("back");
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(vi.mocked(app.api.putConfig).mock.calls[0][1]).toBe("rev-1");
    expect(sent(app)).toEqual({
      schemaVersion: 0,
      timing: { holdMs: 300 },
      functionKey: "back",
    });
    await screen.findByText("已自动保存");
    // The choice is made, and no notice comes with View.
    expect(dialog()).toBeNull();
  });

  it("removes the member when Menu is chosen again, which is the default", async () => {
    const app = setup({ config: config("back") });
    await openPage("设置");
    choose("start");
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(sent(app)).toEqual({ schemaVersion: 0 });
  });

  it("shows the choice while it is saved, and the saved one when the save fails", async () => {
    const app = setup();
    vi.mocked(app.api.putConfig).mockRejectedValueOnce(
      new Error("config changed outside padmate"),
    );
    await openPage("设置");
    choose("guide");
    expect(chosen()).toBe("guide");
    await screen.findByText("未保存，请重试");
    expect(chosen()).toBe("start");
    // Nothing is chosen, so there is nothing to warn about.
    expect(dialog()).toBeNull();
  });

  it("asks the user, in a dialog, to turn off the Game Bar's own use of the Xbox button", async () => {
    const app = setup();
    await openPage("设置");
    choose("guide");
    const open = await screen.findByRole("dialog", {
      name: "关闭 Xbox Game Bar 的手柄按钮",
    });
    expect(sent(app).functionKey).toBe("guide");
    expect(open.textContent).toContain("Xbox Game Bar 也会打开");
    expect(open.textContent).toContain("padmate 无法阻止");
    expect(open.textContent).toContain("Windows 设置 → 游戏 → Game Bar");
    expect(open.textContent).toContain("允许控制器打开 Game Bar");
    expect(open.textContent).toContain(
      "使用控制器上的此按钮打开 Xbox Game Bar",
    );
    expect(open.textContent).toContain("Steam");
    expect(
      within(open).getByRole("button", { name: "打开 Windows 设置" }),
    ).toBeTruthy();
    expect(within(open).getByRole("button", { name: "知道了" })).toBeTruthy();
    // It is a modal dialog: the page is behind it.
    expect((open as HTMLDialogElement).open).toBe(true);
  });

  it("closes with 知道了, and the choice stays", async () => {
    const app = setup();
    await openPage("设置");
    choose("guide");
    await screen.findByRole("dialog", {
      name: "关闭 Xbox Game Bar 的手柄按钮",
    });
    fireEvent.click(screen.getByRole("button", { name: "知道了" }));
    await waitFor(() => expect(dialog()).toBeNull());
    expect(app.api.openGameBarSettings).not.toHaveBeenCalled();
    expect(app.api.putConfig).toHaveBeenCalledTimes(1);
  });

  it("opens Windows Settings with 打开 Windows 设置, then closes", async () => {
    const app = setup();
    await openPage("设置");
    choose("guide");
    await screen.findByRole("dialog", {
      name: "关闭 Xbox Game Bar 的手柄按钮",
    });
    fireEvent.click(screen.getByRole("button", { name: "打开 Windows 设置" }));
    await waitFor(() => expect(dialog()).toBeNull());
    // The window asks with no address: Main opens the one page it knows.
    expect(app.api.openGameBarSettings).toHaveBeenCalledTimes(1);
    expect(vi.mocked(app.api.openGameBarSettings).mock.calls[0]).toEqual([]);
    expect(app.api.putConfig).toHaveBeenCalledTimes(1);
  });

  it("stays open and says so when Windows Settings cannot be opened", async () => {
    const app = setup();
    vi.mocked(app.api.openGameBarSettings).mockRejectedValueOnce(
      new Error("refused"),
    );
    await openPage("设置");
    choose("guide");
    const open = await screen.findByRole("dialog", {
      name: "关闭 Xbox Game Bar 的手柄按钮",
    });
    fireEvent.click(screen.getByRole("button", { name: "打开 Windows 设置" }));
    expect((await within(open).findByRole("alert")).textContent).toBe(
      "无法打开 Windows 设置。",
    );
    expect(dialog()).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "知道了" }));
    await waitFor(() => expect(dialog()).toBeNull());
  });

  it("is in English too", async () => {
    setup();
    await openPage("设置");
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    choose("guide");
    const open = await screen.findByRole("dialog", {
      name: "Turn off the Game Bar button",
    });
    expect(open.textContent).toContain(
      "Windows also opens the Xbox Game Bar when the Xbox button is pressed",
    );
    expect(open.textContent).toContain("Settings → Gaming → Game Bar");
    expect(open.textContent).toContain(
      "Allow your controller to open Game Bar",
    );
    expect(open.textContent).toContain("Open Xbox Game Bar using this button");
    expect(
      within(open).getByRole("button", { name: "Open Windows Settings" }),
    ).toBeTruthy();
    expect(within(open).getByRole("button", { name: "Got it" })).toBeTruthy();
  });

  it("does not ask when the file already has the Xbox button, and cannot be changed without a configuration", async () => {
    setup({ config: config("guide") });
    await openPage("设置");
    expect(dialog()).toBeNull();
    cleanup();
    setup({ config: new Error("no profile library") });
    await openPage("设置");
    expect(
      radios().every((radio) => (radio as HTMLButtonElement).disabled),
    ).toBe(true);
  });

  it("is named on the mapping page after the key the file chooses", async () => {
    setup({
      config: config("back"),
      snapshot: withType("xboxone"),
      profile: {
        schemaVersion: 1,
        name: "Paint",
        layers: { base: {} },
      },
    });
    const group = await screen.findByRole("radiogroup", {
      name: "编辑的按键",
    });
    expect(
      within(group)
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["平时", "按住功能键"]);
    expect(screen.getByRole("group", { name: /Test controller/ })).toBeTruthy();
  });
});

describe("start at sign-in (UI-19)", () => {
  const canStart: StartupState = {
    available: true,
    enabled: false,
    current: false,
    mode: "inject",
  };
  /**
   * The settings row of start at sign-in, with a daemon whose entry is
   * entry; setEntry changes it as the tray would. open: false leaves
   * Settings closed.
   */
  async function startupRow(entry?: StartupState | Error, open = true) {
    const app = setup();
    let current = entry instanceof Error ? undefined : entry;
    if (entry instanceof Error)
      vi.mocked(app.api.getStartup).mockRejectedValue(entry);
    else if (entry) {
      vi.mocked(app.api.getStartup).mockImplementation(
        async () => current as StartupState,
      );
      vi.mocked(app.api.setStartup).mockImplementation(async (enabled) => {
        current = { ...(current as StartupState), enabled, current: enabled };
        return current;
      });
    }
    if (open) await openPage("设置");
    return {
      ...app,
      setEntry: (next: StartupState) => {
        current = next;
      },
    };
  }
  const toggle = () => screen.getByRole("switch", { name: "登录时启动" });
  const row = () => toggle().closest(".row") as HTMLElement;

  it("cannot be set in a development build", async () => {
    const app = await startupRow();
    await waitFor(() =>
      expect((toggle() as HTMLButtonElement).disabled).toBe(true),
    );
    // No helper line under it (F14).
    expect(row().querySelector(".row-hint")).toBeNull();
    // A development daemon is dry-run, but it registers nothing to mark.
    expect(within(row()).queryByText("演练")).toBeNull();
    // The 通用 section, next to the language.
    expect(
      row().closest(".page-section")?.querySelector(".section-title")
        ?.textContent,
    ).toBe("通用");
    expect(app.api.setStartup).not.toHaveBeenCalled();
  });

  it("turns on only when clicked", async () => {
    const app = await startupRow(canStart);
    await waitFor(() =>
      expect((toggle() as HTMLButtonElement).disabled).toBe(false),
    );
    expect(toggle().getAttribute("aria-checked")).toBe("false");
    expect(row().textContent).toBe("登录时启动");
    expect(within(row()).queryByText("演练")).toBeNull();
    expect(app.api.setStartup).not.toHaveBeenCalled();
    fireEvent.click(toggle());
    await waitFor(() =>
      expect(toggle().getAttribute("aria-checked")).toBe("true"),
    );
    expect(app.api.setStartup).toHaveBeenCalledWith(true);
    // config.json is not where it is kept.
    expect(app.api.putConfig).not.toHaveBeenCalled();
    fireEvent.click(toggle());
    await waitFor(() =>
      expect(toggle().getAttribute("aria-checked")).toBe("false"),
    );
    expect(vi.mocked(app.api.setStartup).mock.calls).toEqual([[true], [false]]);
  });

  it("reads the entry again when Settings opens and when the window comes back", async () => {
    const app = await startupRow(canStart, false);
    await waitFor(() => expect(app.store.getState().startup).toEqual(canStart));
    // Turned on from the tray before Settings opens.
    app.setEntry({ ...canStart, enabled: true, current: true });
    await openPage("设置");
    await waitFor(() =>
      expect(toggle().getAttribute("aria-checked")).toBe("true"),
    );
    // Turned off from the tray while Settings is open.
    app.setEntry(canStart);
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() =>
      expect(toggle().getAttribute("aria-checked")).toBe("false"),
    );
    expect(app.api.setStartup).not.toHaveBeenCalled();
  });

  it("marks an entry that starts a dry run", async () => {
    await startupRow({ ...canStart, mode: "dry-run" });
    const tag = await within(row()).findByText("演练");
    expect(tag.classList.contains("amber")).toBe(true);
    expect(tag.closest(".row-title")).toBeTruthy();
    expect(tag.getAttribute("title")).toBe(
      "此 padmate 处于演练模式，登录时启动后也不发送按键",
    );
  });

  it("does not mark another padmate's entry, whose mode it does not know", async () => {
    await startupRow({ ...canStart, mode: "dry-run", enabled: true });
    await waitFor(() =>
      expect(toggle().getAttribute("aria-checked")).toBe("true"),
    );
    // This daemon is a dry run, but the value in Run is another's.
    expect(within(row()).queryByText("演练")).toBeNull();
    expect(row().querySelector(".row-hint")).toBeNull();
  });

  it("shows a failure in the shell's alert", async () => {
    const app = await startupRow(canStart);
    vi.mocked(app.api.setStartup).mockRejectedValueOnce({
      message: "access denied",
    });
    await waitFor(() =>
      expect((toggle() as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(toggle());
    await waitFor(() =>
      expect(app.store.getState().error).toBe(
        "无法更改登录时启动，原因见“设置 → 日志”。",
      ),
    );
    expect(toggle().getAttribute("aria-checked")).toBe("false");
  });

  it("has no row with a daemon without startup.get", async () => {
    const app = await startupRow(new Error("Method not allowed"));
    await waitFor(() => expect(app.api.getStartup).toHaveBeenCalled());
    await screen.findByRole("combobox", { name: "语言" });
    expect(screen.queryByRole("switch", { name: "登录时启动" })).toBeNull();
    expect(app.store.getState().startup).toBeNull();
    expect(app.store.getState().error).toBe("");
  });
});

describe("profiles in the shell", () => {
  const library: ProfileEntry[] = [
    { id: "sketch", name: "Sketch", revision: "s1" },
    { id: "paint", name: "Paint", revision: "p1" },
    { id: "broken", name: "Broken", problem: "profile.json: bad JSON" },
  ];

  async function openMenu() {
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    return screen.getByRole("menu", { name: "配置菜单" });
  }

  /** Changes the profile name in 配置设置…, which saves it. */
  async function editDraft() {
    const menu = await openMenu();
    await waitFor(() =>
      expect(
        (within(menu).getByTestId("profile-settings") as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(within(menu).getByTestId("profile-settings"));
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "Renamed" } });
    fireEvent.blur(name);
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
  }

  it("names what 配置设置… holds, 基于 no longer among them (ADR-0076)", async () => {
    setup({ profiles: library });
    const menu = await openMenu();
    const text = within(menu).getByTestId("profile-settings").textContent;
    expect(text).toContain("名称、判定时间");
    expect(text).not.toContain("基于");
  });

  it("keeps a taken or empty name out of the draft in 配置设置 (F2)", async () => {
    setup({ profiles: library });
    const open = async () => {
      const menu = await openMenu();
      await waitFor(() =>
        expect(
          (within(menu).getByTestId("profile-settings") as HTMLButtonElement)
            .disabled,
        ).toBe(false),
      );
      fireEvent.click(within(menu).getByTestId("profile-settings"));
      return screen.getByRole("textbox", { name: "名称" }) as HTMLInputElement;
    };
    const close = () =>
      fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    let name = await open();
    expect(name.value).toBe("paint");
    // Another profile's name, trimmed and in any case, is refused.
    fireEvent.change(name, { target: { value: "Sketc" } });
    fireEvent.change(name, { target: { value: " sKETCH " } });
    expect(name.value).toBe(" sKETCH ");
    expect(name.getAttribute("aria-invalid")).toBe("true");
    expect(
      screen.getByRole("dialog", { name: "配置设置" }).textContent,
    ).toContain("已有同名配置");
    // Leaving the field does not save it.
    fireEvent.blur(name);
    expect(name.getAttribute("aria-invalid")).toBe("true");
    close();
    // The profile kept the name it had before the dialog opened.
    name = await open();
    expect(name.value).toBe("paint");
    expect(name.getAttribute("aria-invalid")).toBe("false");
    fireEvent.change(name, { target: { value: " Paint 2 " } });
    expect(name.getAttribute("aria-invalid")).toBe("false");
    fireEvent.blur(name);
    close();
    name = await open();
    expect(name.value).toBe("Paint 2");
    fireEvent.change(name, { target: { value: "" } });
    expect(
      screen.getByRole("dialog", { name: "配置设置" }).textContent,
    ).toContain("名称不能为空");
    fireEvent.blur(name);
    close();
    expect((await open()).value).toBe("Paint 2");
  });

  it("lists the profiles to switch to in dictionary order, whatever their creation time", async () => {
    setup({
      profiles: [
        { id: "p10", name: "Paint 10", revision: "a", created: 1 },
        {
          id: "default",
          name: "Stored",
          default: true,
          revision: "d",
          created: 50,
        },
        { id: "p2", name: "Paint 2", revision: "b", created: 2 },
        { id: "paint", name: "Paint", revision: "p1", created: 3 },
        { id: "alpha", name: "alpha", revision: "c", created: 99 },
      ],
    });
    const menu = await openMenu();
    expect(
      within(menu)
        .getAllByRole("menuitemradio")
        .map((radio) => radio.textContent),
    ).toEqual(["默认", "alpha", "Paint", "Paint 2", "Paint 10"]);
  });

  it("lists the library to switch to, then creating and managing", async () => {
    const app = setup({ profiles: library });
    await openMenu();
    await screen.findByRole("menuitemradio", { name: "Sketch" });
    const menu = screen.getByRole("menu", { name: "配置菜单" });
    expect(within(menu).getByText("切换配置")).toBeTruthy();
    const radios = within(menu).getAllByRole("menuitemradio");
    expect(radios.map((radio) => radio.textContent)).toEqual([
      "Paint",
      "Sketch",
    ]);
    expect(radios.map((radio) => radio.getAttribute("aria-checked"))).toEqual([
      "true",
      "false",
    ]);
    // A profile with errors cannot be switched to.
    const broken = within(menu).getByRole("menuitem", { name: "Broken" });
    expect((broken as HTMLButtonElement).disabled).toBe(true);
    expect(within(menu).getByTestId("new-profile")).toBeTruthy();
    expect(within(menu).getByTestId("manage-profiles").textContent).toBe(
      "管理配置和软件…",
    );
    expect(within(menu).getByTestId("profile-settings")).toBeTruthy();

    // Choosing the active profile again does nothing.
    fireEvent.click(within(menu).getByRole("menuitemradio", { name: "Paint" }));
    expect(app.api.activateProfile).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTitle("配置菜单"));
    fireEvent.click(screen.getByRole("menuitemradio", { name: "Sketch" }));
    await waitFor(() =>
      expect(app.api.activateProfile).toHaveBeenCalledWith("sketch"),
    );
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.click(screen.getByTitle("配置菜单"));
    fireEvent.click(screen.getByTestId("manage-profiles"));
    expect(
      within(screen.getByRole("navigation"))
        .getByRole("button", { name: "配置" })
        .getAttribute("aria-current"),
    ).toBe("page");
  });

  it("keeps profiles with the same name apart in the menu", async () => {
    setup({
      profiles: [
        { id: "paint", name: "Paint" },
        { id: "paint-2", name: "Paint" },
      ],
    });
    await openMenu();
    const radios = await screen.findAllByRole("menuitemradio");
    expect(radios.map((radio) => radio.textContent)).toEqual([
      "Paint (paint)",
      "Paint (paint-2)",
    ]);
  });

  it("saves the mapping page's edits before switching profiles", async () => {
    const app = setup({ profiles: library });
    await editDraft();
    fireEvent.click(screen.getByTitle("配置菜单"));
    fireEvent.click(
      await screen.findByRole("menuitemradio", { name: "Sketch" }),
    );
    await waitFor(() =>
      expect(app.api.activateProfile).toHaveBeenCalledWith("sketch"),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    const put = vi.mocked(app.api.putProfile);
    const [id, text] = put.mock.calls[0] ?? [];
    expect(id).toBe("paint");
    expect(JSON.parse(text as string).name).toBe("Renamed");
    expect(put.mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(app.api.activateProfile).mock.invocationCallOrder[0] as number,
    );
  });

  it("stays on a profile whose edits the daemon refused", async () => {
    const app = setup({ profiles: library });
    vi.mocked(app.api.validateProfile).mockResolvedValue({
      valid: false,
      diagnostics: [
        {
          severity: "error",
          pointer: "/name",
          offset: 0,
          line: 1,
          col: 1,
          message: "bad name",
        },
      ] as never[],
    });
    await editDraft();
    expect(await screen.findByText("修改未保存")).toBeTruthy();
    fireEvent.click(screen.getByTitle("配置菜单"));
    fireEvent.click(
      await screen.findByRole("menuitemradio", { name: "Sketch" }),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    expect(app.api.activateProfile).not.toHaveBeenCalled();
    expect(app.api.putProfile).not.toHaveBeenCalled();
    expect(screen.getByText("修改未保存")).toBeTruthy();
    // The switch that did not happen says so.
    expect(screen.getByText("当前配置有未保存的修改，未切换。").className).toBe(
      "shell-toast",
    );
  });

  it("names no profile while the daemon runs its placeholder", async () => {
    // Without an active library profile, padmated maps nothing under a
    // placeholder named "Mapping disabled".
    const placeholder = {
      ...status,
      profileId: "",
      slots: status.slots.map((slot) => ({
        ...slot,
        profile: "Mapping disabled",
      })),
    };
    setup({ snapshot: placeholder, profiles: library });
    expect(
      await screen.findByRole("heading", { name: "没有启用配置" }),
    ).toBeTruthy();
    const button = screen.getByTitle("配置菜单");
    expect(button.textContent).toContain("未启用配置");
    expect(button.textContent).not.toContain("Mapping disabled");
  });

  it("offers to create a profile when the library is empty", async () => {
    // The first-run page has its own tests (Onboarding.test.tsx).
    localStorage.setItem("padmate.onboarding", "done");
    setup({ snapshot: { ...status, profileId: "", slots: [] } });
    expect(
      await screen.findByRole("heading", { name: "暂无配置" }),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "选择配置" })).toBeNull();
    expect(screen.getByTitle("配置菜单").textContent).toContain("未启用配置");
    fireEvent.click(screen.getByRole("button", { name: "新建配置" }));
    expect(screen.getByRole("dialog", { name: "新建配置" })).toBeTruthy();
  });

  it("creates a profile from the mapping page and switches to it", async () => {
    localStorage.setItem("padmate.onboarding", "done");
    const app = setup({ snapshot: { ...status, profileId: "", slots: [] } });
    fireEvent.click(await screen.findByRole("button", { name: "新建配置" }));
    const dialog = screen.getByRole("dialog", { name: "新建配置" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "名称" }), {
      target: { value: "Krita" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "新建" }));
    await waitFor(() =>
      expect(app.api.activateProfile).toHaveBeenCalledWith("krita"),
    );
    const [id, text, revision] =
      vi.mocked(app.api.putProfile).mock.calls[0] ?? [];
    expect([id, revision]).toEqual(["krita", ""]);
    // Only a name is asked for, and the profile is blank (ADR-0076).
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 1,
      name: "Krita",
      layers: { base: {} },
    });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("goes to the profiles page and back without asking", async () => {
    const app = setup({ profiles: library });
    await editDraft();
    fireEvent.click(screen.getByRole("button", { name: "配置" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    // Switching by app is set on this page now.
    expect(
      await screen.findByRole("switch", { name: "按软件自动切换配置" }),
    ).toBeTruthy();
    const card = screen
      .getByRole("heading", { name: "Paint" })
      .closest("article") as HTMLElement;
    fireEvent.click(within(card).getByRole("button", { name: "编辑按键" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("group", { name: /Test controller/ })).toBeTruthy();
    expect(app.api.activateProfile).not.toHaveBeenCalled();
    // The edit was saved and is still shown.
    expect(app.api.putProfile).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByTitle("配置菜单"));
    fireEvent.click(screen.getByTestId("profile-settings"));
    expect(
      (screen.getByRole("textbox", { name: "名称" }) as HTMLInputElement).value,
    ).toBe("Renamed");
  });

  it("opens the profile menu from the page when no profile is in use", async () => {
    setup({
      snapshot: { ...status, profileId: "", slots: [] },
      profiles: library,
    });
    expect(
      await screen.findByRole("heading", { name: "没有启用配置" }),
    ).toBeTruthy();
    fireEvent.click(await screen.findByRole("button", { name: "选择配置" }));
    const menu = screen.getByRole("menu", { name: "配置菜单" });
    expect(
      within(menu)
        .getAllByRole("menuitemradio")
        .map((radio) => radio.getAttribute("aria-checked")),
    ).toEqual(["false", "false"]);
    // Leaving and coming back does not open the menu again.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    fireEvent.click(screen.getByRole("button", { name: "映射" }));
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("keeps the controller view of a session started with a file", async () => {
    // A file has no library ID, so profileId is empty while it runs.
    localStorage.setItem("padmate.onboarding", "done");
    const app = setup({ snapshot: { ...status, profileId: "" } });
    vi.mocked(app.api.listProfiles).mockRejectedValue(
      new Error("no profile library"),
    );
    await act(async () => app.store.getState().refreshLibrary());
    expect(
      await screen.findByRole("group", { name: /Test controller/ }),
    ).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "暂无配置" })).toBeNull();
    expect(screen.queryByRole("button", { name: "新建配置" })).toBeNull();
    // The topbar names the running file's profile.
    expect(screen.getByTitle("配置菜单").textContent).toContain(
      "Paint display name",
    );
    fireEvent.click(screen.getByTitle("配置菜单"));
    expect(
      (screen.getByTestId("new-profile") as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("saves the edits before Edit keys switches to another profile", async () => {
    const app = setup({ profiles: library });
    await editDraft();
    fireEvent.click(screen.getByRole("button", { name: "配置" }));
    const sketch = (
      await screen.findByRole("heading", { name: "Sketch" })
    ).closest("article") as HTMLElement;
    fireEvent.click(within(sketch).getByRole("button", { name: "编辑按键" }));
    await waitFor(() =>
      expect(app.api.activateProfile).toHaveBeenCalledWith("sketch"),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    const [id, text] = vi.mocked(app.api.putProfile).mock.calls[0] ?? [];
    expect(id).toBe("paint");
    expect(JSON.parse(text as string).name).toBe("Renamed");
    // Then its keys: the mapping page.
    expect(
      await screen.findByRole("group", { name: /Test controller/ }),
    ).toBeTruthy();
  });
});

describe("配置设置 from a profile card (UI-27)", () => {
  const library: ProfileEntry[] = [
    { id: "paint", name: "Paint", revision: "p1" },
    { id: "sketch", name: "Sketch", revision: "s1" },
    { id: "broken", name: "Broken", problem: "profile.json: bad JSON" },
  ];

  /** The library's documents, by ID: each has its own name and revision. */
  function documents(app: ReturnType<typeof setup>) {
    vi.mocked(app.api.getProfile).mockImplementation(async (id: string) => ({
      id,
      text: JSON.stringify({
        schemaVersion: 1,
        name: id === "sketch" ? "Sketch" : "Paint",
        layers: { base: {} },
      }),
      revision: `${id}-r1`,
      diagnostics: [],
    }));
  }

  async function openProfiles() {
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(
      within(screen.getByRole("navigation")).getByRole("button", {
        name: "配置",
      }),
    );
    await screen.findByRole("article", { name: "Sketch" });
  }

  async function openCardSettings(name: string) {
    fireEvent.click(
      within(screen.getByRole("article", { name })).getByRole("button", {
        name: `「${name}」的更多操作`,
      }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "配置设置…" }));
    return screen.findByRole("dialog", { name: "配置设置" });
  }

  const loaded = (app: ReturnType<typeof setup>, id: string) =>
    vi.mocked(app.api.getProfile).mock.calls.filter(([got]) => got === id)
      .length;

  it("opens for a profile that is not in use, without activating it or leaving the page", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    const dialog = await openCardSettings("Sketch");
    expect(dialog.textContent).toContain("仅影响配置「Sketch」。");
    const name = (await within(dialog).findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Sketch"));
    expect(app.api.getProfile).toHaveBeenCalledWith("sketch");
    expect(app.api.activateProfile).not.toHaveBeenCalled();
    expect(
      screen.getByRole("heading", { name: "配置", level: 1 }),
    ).toBeTruthy();
    expect(app.store.getState().status?.profileId).toBe("paint");
  });

  it("saves a change to that profile as the mapping page would, and flushes on close", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    const dialog = await openCardSettings("Sketch");
    const name = (await within(dialog).findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Sketch"));
    fireEvent.change(name, { target: { value: "Sketch 2" } });
    // Typing is not saved; leaving the field is, with the revision it read.
    expect(app.api.putProfile).not.toHaveBeenCalled();
    fireEvent.blur(name);
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledOnce());
    const [id, text, revision] =
      vi.mocked(app.api.putProfile).mock.calls[0] ?? [];
    expect([id, revision]).toEqual(["sketch", "sketch-r1"]);
    expect(JSON.parse(text as string).name).toBe("Sketch 2");
    // The hold time too, and the close waits for it.
    const hold = within(dialog).getByRole("textbox", { name: "触发长按时间" });
    fireEvent.change(hold, { target: { value: "300" } });
    // The click on 关闭 takes the focus away first, which commits the field.
    fireEvent.blur(hold);
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "配置设置" })).toBeNull(),
    );
    expect(app.api.putProfile).toHaveBeenCalledTimes(2);
    expect(
      JSON.parse(vi.mocked(app.api.putProfile).mock.calls[1]?.[1] as string)
        .timing,
    ).toEqual({ holdMs: 300 });
    // Only Sketch was written; the profile in use was not touched.
    for (const [written] of vi.mocked(app.api.putProfile).mock.calls)
      expect(written).toBe("sketch");
    expect(app.api.activateProfile).not.toHaveBeenCalled();
    expect(
      screen.getByRole("heading", { name: "配置", level: 1 }),
    ).toBeTruthy();
  });

  it("says so when the profile settings could not be saved, and closes", async () => {
    const app = setup({ profiles: library });
    documents(app);
    vi.mocked(app.api.putProfile).mockRejectedValue(new Error("disk full"));
    await openProfiles();
    const dialog = await openCardSettings("Sketch");
    const name = (await within(dialog).findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Sketch"));
    fireEvent.change(name, { target: { value: "Sketch 2" } });
    fireEvent.blur(name);
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    expect((await screen.findByText("配置设置未保存。")).className).toBe(
      "shell-toast",
    );
    expect(screen.queryByRole("dialog", { name: "配置设置" })).toBeNull();
  });

  it("closes and says so when the profile cannot be read", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    vi.mocked(app.api.getProfile).mockRejectedValue(new Error("gone"));
    fireEvent.click(
      within(screen.getByRole("article", { name: "Sketch" })).getByRole(
        "button",
        { name: "「Sketch」的更多操作" },
      ),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "配置设置…" }));
    expect((await screen.findByText("无法读取这个配置。")).className).toBe(
      "shell-toast",
    );
    expect(screen.queryByRole("dialog", { name: "配置设置" })).toBeNull();
    expect(app.api.putProfile).not.toHaveBeenCalled();
  });

  it("uses the mapping page's editor for the profile in use: one store on it", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    await waitFor(() => expect(loaded(app, "paint")).toBeGreaterThan(0));
    const before = loaded(app, "paint");
    const dialog = await openCardSettings("Paint");
    const name = (await within(dialog).findByRole("textbox", {
      name: "名称",
    })) as HTMLInputElement;
    await waitFor(() => expect(name.value).toBe("Paint"));
    // No second store read it.
    expect(loaded(app, "paint")).toBe(before);
    fireEvent.change(name, { target: { value: "Paint 2" } });
    fireEvent.blur(name);
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledOnce());
    expect(vi.mocked(app.api.putProfile).mock.calls[0]?.[0]).toBe("paint");
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "配置设置" })).toBeNull(),
    );
    // The mapping page shows what was saved: its editor made the edit.
    fireEvent.click(
      within(screen.getByRole("navigation")).getByRole("button", {
        name: "映射",
      }),
    );
    fireEvent.click(await screen.findByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    expect(
      (
        (await screen.findByRole("textbox", {
          name: "名称",
        })) as HTMLInputElement
      ).value,
    ).toBe("Paint 2");
  });

  it("keeps the mapping page's own profile and its dialog apart from the card's", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    // Open for Sketch, close, then for Paint: each time the right document.
    let dialog = await openCardSettings("Sketch");
    await waitFor(() =>
      expect(
        (
          within(dialog).getByRole("textbox", {
            name: "名称",
          }) as HTMLInputElement
        ).value,
      ).toBe("Sketch"),
    );
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "配置设置" })).toBeNull(),
    );
    dialog = await openCardSettings("Paint");
    await waitFor(() =>
      expect(
        (
          within(dialog).getByRole("textbox", {
            name: "名称",
          }) as HTMLInputElement
        ).value,
      ).toBe("Paint"),
    );
    expect(dialog.textContent).toContain("仅影响配置「Paint」。");
  });

  it("is in English with the UI", async () => {
    const app = setup({ profiles: library });
    documents(app);
    await openProfiles();
    fireEvent.change(
      await (async () => {
        fireEvent.click(
          within(screen.getByRole("navigation")).getByRole("button", {
            name: "设置",
          }),
        );
        return screen.findByRole("combobox", { name: "语言" });
      })(),
      { target: { value: "en" } },
    );
    fireEvent.click(
      within(screen.getByRole("navigation")).getByRole("button", {
        name: "Profiles",
      }),
    );
    await screen.findByRole("article", { name: "Sketch" });
    fireEvent.click(
      within(screen.getByRole("article", { name: "Sketch" })).getByRole(
        "button",
        { name: "More for “Sketch”" },
      ),
    );
    fireEvent.click(
      screen.getByRole("menuitem", { name: "Profile settings…" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Profile settings",
    });
    expect(dialog.textContent).toContain("Only affects “Sketch”.");
  });
});

describe("scripts page", () => {
  const running: ScriptStatusEntry = {
    slot: 0,
    device: 1,
    key: "controller",
    profileId: "paint",
    generation: 1,
    script: {
      state: "Running",
      paused: false,
      pending: false,
      wallStopped: false,
      permanent: false,
      restartCount: 0,
      metadata: { runtime: 1 },
      macros: [{ file: "复制全部.ts", error: "复制全部.ts:3:9 boom" }],
    },
  };
  const list: MacroList = {
    macros: [
      {
        file: "复制全部.ts",
        name: "复制全部",
        usedBy: [
          {
            profile: "paint",
            profileName: "Paint",
            control: "west",
            gesture: "tap",
          },
        ],
      },
    ],
    missing: [],
  };

  it("lists the script library and marks a failing one", async () => {
    const { api } = setup({ scripts: [running] });
    vi.mocked(api.listMacros).mockResolvedValue(list);
    await openPage("脚本");
    const card = (
      await screen.findByRole("heading", { name: "复制全部" })
    ).closest("article") as HTMLElement;
    expect(api.listMacros).toHaveBeenCalledWith();
    // Named as printed on this (Switch) controller, with the profile.
    expect(card.textContent).toContain("Paint：Y 点按");
    expect(within(card).getByTestId("script-error").textContent).toContain(
      "复制全部.ts:3:9 boom",
    );
    // The error dot on the navigation item is announced, not color only.
    expect(
      screen
        .getByRole("button", { name: "脚本" })
        .getAttribute("aria-describedby"),
    ).toBe("nav-scripts-alert");
    expect(document.getElementById("nav-scripts-alert")?.textContent).toBe(
      "有脚本出错",
    );
  });

  it("reads the list again for the drawer on returning to the mapping page", async () => {
    const { api } = setup();
    await openPage("脚本");
    await waitFor(() => expect(api.listMacros).toHaveBeenCalled());
    vi.mocked(api.listMacros).mockClear();
    fireEvent.click(screen.getByRole("button", { name: "映射" }));
    await screen.findByRole("group", { name: /Test controller/ });
    await waitFor(() => expect(api.listMacros).toHaveBeenCalledWith());
  });

  it("renames a script once the mapping page's edits are saved, then reads the profile again", async () => {
    const { api } = setup();
    vi.mocked(api.listMacros).mockResolvedValue(list);
    // The rename in 配置设置 is saved at once; its save waits here.
    let release = () => {};
    vi.mocked(api.putProfile).mockImplementationOnce(async () => {
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return { revision: "r2", applied: true };
    });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    const name = screen.getByRole("textbox", { name: "名称" });
    fireEvent.change(name, { target: { value: "Renamed" } });
    fireEvent.blur(name);
    fireEvent.click(screen.getByRole("button", { name: "关闭配置设置" }));
    await waitFor(() => expect(api.putProfile).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    const card = (
      await screen.findByRole("heading", { name: "复制全部" })
    ).closest("article") as HTMLElement;
    fireEvent.click(within(card).getByTestId("script-menu"));
    expect(
      (screen.getByRole("menuitem", { name: "重命名" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(screen.getAllByText("映射页的修改正在保存").length).toBeGreaterThan(
      0,
    );
    fireEvent.keyDown(document, { key: "Escape" });
    await act(async () => release());
    fireEvent.click(within(card).getByTestId("script-menu"));
    await waitFor(() =>
      expect(
        (screen.getByRole("menuitem", { name: "重命名" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "重命名" }));
    const form = screen.getByRole("dialog", { name: "重命名" });
    fireEvent.change(within(form).getByRole("textbox", { name: "名称" }), {
      target: { value: "全选复制" },
    });
    vi.mocked(api.getProfile).mockClear();
    fireEvent.click(within(form).getByRole("button", { name: "确定" }));
    await waitFor(() =>
      expect(api.renameMacro).toHaveBeenCalledWith(
        "复制全部.ts",
        "全选复制.ts",
      ),
    );
    // The mapping page's draft is read from disk again.
    await waitFor(() => expect(api.getProfile).toHaveBeenCalledWith("paint"));
  });

  it("names controls as printed on the controller", () => {
    const labels = { west: "X", leftstick: "LS", lefttrigger: "LT" };
    expect(listControlName("x", labels, "zh-CN")).toBe("X");
    expect(listControlName("lt", labels, "zh-CN")).toBe("LT");
    expect(listControlName("ls.up", labels, "zh-CN")).toBe("LS ↑");
    expect(listControlName("dpad.left", labels, "en")).toBe("D-pad ←");
  });
});

describe("logs view", () => {
  function sendLogs(app: ReturnType<typeof setup>) {
    app.event("log", {
      sequence: 1,
      record: {
        time: "2026-10-06T05:35:12Z",
        level: "INFO",
        msg: "controller connected",
        slot: 1,
      },
    });
    app.event("log", {
      sequence: 2,
      record: {
        level: "ERROR",
        source: "script",
        msg: "brush failed",
        entry: "brush.ts",
      },
    });
    app.event("log", {
      sequence: 3,
      record: { level: "WARN", msg: "slow output" },
    });
  }

  it("filters logs by level, source and text with counts", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    sendLogs(app);
    await openLogs();
    const list = screen.getByRole("list", { name: "日志记录" });
    expect(
      within(list)
        .getAllByRole("button")
        .map((row) => row.querySelector(".page-log-message")?.textContent),
    ).toEqual(["slow output", "brush failed", "controller connected"]);
    expect(within(list).getByText("slot=1")).toBeTruthy();
    const errors = screen.getByRole("radio", { name: /^错误/ });
    expect(errors.textContent).toBe("错误1");
    expect(screen.getByRole("radio", { name: /^警告/ }).textContent).toBe(
      "警告1",
    );
    fireEvent.click(errors);
    expect(screen.queryByText("controller connected")).toBeNull();
    expect(screen.getByText("brush failed")).toBeTruthy();
    fireEvent.click(screen.getByRole("radio", { name: "系统" }));
    expect(screen.getByText("没有符合筛选条件的日志")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(screen.getByText("controller connected")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("搜索日志"), {
      target: { value: "SLOW" },
    });
    expect(screen.getByText("slow output")).toBeTruthy();
    expect(screen.queryByText("brush failed")).toBeNull();
    expect(screen.getByRole("radio", { name: /^错误/ }).textContent).toBe(
      "错误0",
    );
  });

  it("expands a record to its details and clears the view", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    sendLogs(app);
    await openLogs();
    const row = screen.getByRole("button", { name: /controller connected/ });
    expect(row.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(row);
    expect(row.getAttribute("aria-expanded")).toBe("true");
    const details = document.getElementById(
      row.getAttribute("aria-controls") ?? "",
    );
    expect(details?.textContent).toContain('"slot": 1');
    fireEvent.click(row);
    expect(row.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "清空视图" }));
    expect(screen.getByText("暂无日志")).toBeTruthy();
  });

  it("formats record attributes inline", () => {
    expect(
      logFields({
        time: "t",
        level: "INFO",
        msg: "m",
        source: "script",
        profile: "paint",
        slots: [1, 2],
      }),
    ).toBe("profile=paint  slots=[1,2]");
  });
});

describe("conflicting software", () => {
  it("is not shown in Settings or its logs view (F15)", async () => {
    setup();
    await openPage("设置");
    expect(await screen.findByRole("combobox", { name: "语言" })).toBeTruthy();
    expect(screen.queryByText("Steam 正在运行")).toBeNull();
    expect(screen.queryByText(/Steam/)).toBeNull();
    expect(screen.queryByRole("button", { name: "关闭提示" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    expect(screen.getByRole("heading", { name: "日志" })).toBeTruthy();
    expect(screen.queryByText("Steam 正在运行")).toBeNull();
  });

  it("is named on the mapping page without a controller", async () => {
    setup({ snapshot: { ...status, devices: [], slots: [] } });
    expect(await screen.findByText("Steam 正在运行")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "连接一个手柄" })).toBeTruthy();
    // Nothing to close there: it explains why the controller is missing.
    expect(screen.queryByRole("button", { name: "关闭提示" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    expect(screen.getByRole("heading", { name: "日志" })).toBeTruthy();
    expect(
      within(screen.getByRole("navigation"))
        .getByRole("button", { name: "设置" })
        .getAttribute("aria-current"),
    ).toBe("page");
  });

  it("says which controllers Steam may take over, and how to stop it, where the notice is shown (IN-24)", async () => {
    setup({
      snapshot: {
        ...status,
        devices: [],
        slots: [],
        conflicts: [
          ...(status.conflicts ?? []),
          {
            kind: "steamInput",
            process: "Steam",
            message: "",
            document: "",
            classes: ["xbox", "switch"],
          },
        ],
      },
    });
    const title = await screen.findByText("Steam 正在运行");
    // One notice for Steam, though the daemon lists it twice.
    expect(screen.getAllByText("Steam 正在运行")).toHaveLength(1);
    expect(title.closest(".notice")?.textContent).toContain(
      "它可能接管 Xbox 类、Switch 类手柄：请在 Steam 的设置 → 控制器里关掉这类手柄的 Steam Input，或在不用 Steam 时退出它。",
    );
    expect(
      conflictNames([
        {
          kind: "steamInput",
          process: "Steam",
          message: "",
          document: "",
          classes: ["xbox"],
        },
      ]),
    ).toEqual(["Steam"]);
  });

  it("names the software and drops duplicates and dismissed ones", () => {
    expect(conflictName("DS4Windows.exe")).toBe("DS4Windows");
    expect(conflictName("HidHideClient.exe")).toBe("HidHide");
    expect(conflictName("HidHide")).toBe("HidHide");
    expect(conflictName("other.exe")).toBe("other.exe");
    const entry = (process: string) => ({
      kind: "conflict",
      process,
      message: "",
      document: "",
    });
    expect(
      conflictNames(
        [
          entry("HidHideClient.exe"),
          entry("hidhide.exe"),
          entry("steam.exe"),
          entry("DS4Windows.exe"),
        ],
        ["Steam"],
      ),
    ).toEqual(["HidHide", "DS4Windows"]);
    expect(conflictNames(undefined)).toEqual([]);
  });
});

describe("shortcut table", () => {
  const paint: Profile = {
    schemaVersion: 1,
    name: "Paint",
    layers: {
      base: {
        lt: { label: "抓手", press: { type: "keys", keys: "Space" } },
        south: { label: "画笔" },
        east: {
          label: "重做",
          press: { type: "keys", keys: "F13", mode: "tap" },
        },
        north: { press: { type: "script", name: "cycleBrush" } },
      },
    },
  };

  /** Opens 更多 once the draft is loaded and its items are enabled. */
  async function openMore() {
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    const menu = screen.getByRole("menu", { name: "更多" });
    await waitFor(() =>
      expect(
        (within(menu).getByTestId("export-sheet") as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    return menu;
  }

  it("chooses what the keys show: the action name or the shortcut (ADR-0077)", async () => {
    // A choice saved before 按键名称 went away shows the action names.
    localStorage.setItem("padmate.labelDisplay", "names");
    setup({ profile: paint });
    const menu = await openMore();
    expect(menu.textContent).toContain("按键名称显示");
    expect(menu.textContent).not.toContain("手柄图上显示");
    const choices = within(menu).getAllByRole("menuitemradio");
    expect(choices.map((choice) => choice.textContent)).toEqual([
      "动作名称",
      "快捷键",
    ]);
    expect(choices[0].getAttribute("aria-checked")).toBe("true");
    fireEvent.click(choices[1]);
    expect(localStorage.getItem("padmate.labelDisplay")).toBe("keys");
  });

  it("offers no automatic shortcuts (G5)", async () => {
    setup({ profile: paint });
    const menu = await openMore();
    const items = within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent);
    expect(items).toContain("导出快捷键对照表…");
    expect(items.join()).not.toMatch(/自动分配|速查表/);
    expect(within(menu).queryByTestId("allocate-shortcuts")).toBeNull();
  });

  it("exports the table without asking about empty shortcuts", async () => {
    const app = setup({ profile: paint });
    fireEvent.click(within(await openMore()).getByTestId("export-sheet"));
    const dialog = screen.getByRole("dialog", { name: "导出快捷键对照表" });
    expect(dialog.textContent).not.toContain("尚未设置快捷键");
    for (const name of ["直接导出", "自动分配并导出"])
      expect(within(dialog).queryByRole("button", { name })).toBeNull();
    fireEvent.click(within(dialog).getByRole("radio", { name: "Markdown" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "导出" }));
    await waitFor(() => expect(app.api.exportCheatSheet).toHaveBeenCalled());
    const [format, content] =
      vi.mocked(app.api.exportCheatSheet).mock.calls[0] ?? [];
    expect(format).toBe("markdown");
    expect(content).toContain("# Paint 快捷键对照表");
    expect(content).toContain("| 手柄按键 | 快捷键 | 请在软件中设置为 |");
    // Names as printed on the connected pad (a Switch Pro controller).
    expect(content).toContain("| ZL | Space | 抓手（通常已是默认） |");
    expect(content).toContain("| A | F13 | 重做 |");
    // A key without a shortcut and a script are not in the table.
    expect(content).not.toContain("画笔");
    expect(content).not.toContain("cycleBrush");
    expect(await screen.findByText("对照表已导出")).toBeTruthy();
    // Exporting changes nothing in the profile.
    expect(app.api.putProfile).not.toHaveBeenCalled();
  });

  it("exports what is saved: a shortcut cleared in the drawer is not counted (H1)", async () => {
    // A draft that still holds the empty parts the drawer leaves behind.
    const app = setup({
      profile: {
        ...paint,
        layers: {
          base: {
            south: {
              label: "撤销",
              tap: { type: "keys", keys: "Ctrl+Z" },
              hold: { type: "keys", keys: "" },
            },
          },
          tools: { east: { press: { type: "keys", keys: "" } } },
        },
      },
    });
    fireEvent.click(within(await openMore()).getByTestId("export-sheet"));
    const dialog = screen.getByRole("dialog", { name: "导出快捷键对照表" });
    fireEvent.click(within(dialog).getByRole("radio", { name: "Markdown" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "导出" }));
    await waitFor(() => expect(app.api.exportCheatSheet).toHaveBeenCalled());
    const content = String(
      vi.mocked(app.api.exportCheatSheet).mock.calls[0]?.[1],
    );
    // The key has one shortcut left, so its name has no gesture (a Switch
    // Pro pad prints its south key as B), and the empty binding in another
    // group is no setting in the file.
    expect(content).toContain("| B | Ctrl+Z | 撤销（通常已是默认） |");
    expect(content).not.toContain("点按");
    expect(content).not.toContain("未列出");
  });

  it("exports a PNG by default and says when the export is canceled", async () => {
    const app = setup({ profile: paint });
    const context = {
      measureText: (text: string) => ({ width: text.length * 8 }),
      fillText: vi.fn(),
      fillRect: vi.fn(),
      scale: vi.fn(),
    };
    const getContext = vi
      .spyOn(HTMLCanvasElement.prototype, "getContext")
      .mockReturnValue(context as unknown as CanvasRenderingContext2D);
    const toDataURL = vi
      .spyOn(HTMLCanvasElement.prototype, "toDataURL")
      .mockReturnValue("data:image/png;base64,AA==");
    try {
      fireEvent.click(within(await openMore()).getByTestId("export-sheet"));
      const dialog = screen.getByRole("dialog", { name: "导出快捷键对照表" });
      expect(
        within(dialog)
          .getByRole("radio", { name: "图片（PNG）" })
          .getAttribute("aria-checked"),
      ).toBe("true");
      fireEvent.click(within(dialog).getByRole("button", { name: "导出" }));
      await waitFor(() =>
        expect(app.api.exportCheatSheet).toHaveBeenCalledWith(
          "png",
          "data:image/png;base64,AA==",
        ),
      );
      expect(context.fillText).toHaveBeenCalledWith(
        "重做",
        expect.any(Number),
        expect.any(Number),
      );
      expect(await screen.findByText("对照表已导出")).toBeTruthy();
      vi.mocked(app.api.exportCheatSheet).mockResolvedValueOnce({
        saved: false,
      });
      fireEvent.click(within(await openMore()).getByTestId("export-sheet"));
      fireEvent.click(
        within(
          screen.getByRole("dialog", { name: "导出快捷键对照表" }),
        ).getByRole("button", { name: "导出" }),
      );
      expect(await screen.findByText("已取消导出")).toBeTruthy();
      expect(app.api.putProfile).not.toHaveBeenCalled();
    } finally {
      getContext.mockRestore();
      toDataURL.mockRestore();
    }
  });
});

describe("page helpers and empty states", () => {
  it("names release versions", () => {
    expect(versionText("0.10.0")).toBe("v0.10.0");
    expect(versionText("v0.12.0")).toBe("v0.12.0");
    expect(versionText("dev")).toBe("dev");
  });

  it("guides the owner to connect a controller", () => {
    const showLogs = vi.fn();
    render(<NoController language="zh-CN" onShowLogs={showLogs} />);
    expect(screen.getByRole("heading", { name: "连接一个手柄" })).toBeTruthy();
    expect(screen.getByText("USB")).toBeTruthy();
    expect(screen.getByText("蓝牙")).toBeTruthy();
    expect(screen.getByText(/Steam、DS4Windows 等软件占用了手柄/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    expect(showLogs).toHaveBeenCalledTimes(1);
  });
});

describe("a repaired settings file (PRO-29)", () => {
  const repair = {
    backup: "config.broken-20261015-191600.json",
    path: "C:dataconfig.broken-20261015-191600.json",
    reset: false,
  };

  it("tells once, in a blocking dialog, and stays away after 确定", async () => {
    const app = setup({ snapshot: { ...status, configRepair: repair } });
    const dialog = await screen.findByRole("dialog", { name: "设置已恢复" });
    expect(dialog.textContent).toContain(
      "设置文件有误，已恢复默认设置。原文件已备份为 config.broken-20261015-191600.json。",
    );
    // Not the start-failure page, and no unreadable-file notice.
    expect(screen.queryByTestId("start-failure")).toBeNull();
    fireEvent.click(within(dialog).getByRole("button", { name: "打开文件夹" }));
    expect(app.api.showConfigBackup).toHaveBeenCalledWith(repair.backup);
    expect(screen.getByRole("dialog", { name: "设置已恢复" })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "确定" }));
    await waitFor(() =>
      expect(app.api.markConfigRepairSeen).toHaveBeenCalledWith(repair.backup),
    );
    expect(screen.queryByRole("dialog", { name: "设置已恢复" })).toBeNull();
    // The daemon's next status still lists it until it has answered; it
    // does not come back.
    await act(() => app.store.getState().refresh());
    expect(screen.queryByRole("dialog", { name: "设置已恢复" })).toBeNull();
  });

  it("shows a repair that happens while padmate runs", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    expect(screen.queryByRole("dialog", { name: "设置已恢复" })).toBeNull();
    app.event("profile.changed", {
      kind: "configRepaired",
      ...repair,
      revision: "rev2",
    });
    expect(
      await screen.findByRole("dialog", { name: "设置已恢复" }),
    ).toBeTruthy();
  });

  it("opens the settings page normally after a repair: nothing says the file cannot be read", async () => {
    setup({ snapshot: { ...status, configRepair: repair } });
    fireEvent.click(
      within(
        await screen.findByRole("dialog", { name: "设置已恢复" }),
      ).getByRole("button", { name: "确定" }),
    );
    await openPage("设置");
    expect(screen.queryByText("无法读取设置文件")).toBeNull();
  });

  it("speaks English with the interface", async () => {
    localStorage.setItem("padmate.language", "en");
    setup({ snapshot: { ...status, configRepair: repair } });
    expect(
      await screen.findByRole("dialog", { name: "Settings reset" }),
    ).toBeTruthy();
  });
});

describe("the tray exit waits for the last save (DMN-22, G9)", () => {
  it("answers Main's request to finish only after a slow save has completed", async () => {
    const app = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByTitle("配置菜单"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("profile-settings") as HTMLButtonElement).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByTestId("profile-settings"));
    let finishSave: (() => void) | undefined;
    vi.mocked(app.api.putProfile).mockImplementation(
      () =>
        new Promise((done) => {
          finishSave = () => done({ revision: "r2", applied: true });
        }),
    );
    const name = screen.getByRole("textbox", { name: "名称" });
    name.focus();
    fireEvent.change(name, { target: { value: "Slow" } });
    // Main asks like it does for a closing window; the tray exit uses the
    // same request.
    const closing = vi.mocked(app.api.onClosing).mock.calls.at(-1)?.[0];
    act(() => closing?.());
    await waitFor(() => expect(app.api.putProfile).toHaveBeenCalledOnce());
    // The save has not finished: not ready, however long it takes.
    await act(() => new Promise((resolve) => setTimeout(resolve, 150)));
    expect(app.api.readyToClose).not.toHaveBeenCalled();
    await act(async () => finishSave?.());
    await waitFor(() => expect(app.api.readyToClose).toHaveBeenCalledOnce());
  });
});

describe("identify a controller (IN-13)", () => {
  const identifyButton = (name = "识别 Test controller") =>
    screen.getByRole("button", { name }) as HTMLButtonElement;
  const note = () => screen.queryByText("该手柄无法震动或闪灯");
  /** Real timers keep running; only the cool-down of the button is faked. */
  const fastClock = () =>
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout"],
    });
  afterEach(() => vi.useRealTimers());

  it("has an 识别 button on the row of each controller, beside 调校, with no helper text", async () => {
    setup();
    await openPage("设置");
    const button = identifyButton();
    expect(button.textContent).toBe("识别");
    expect(button.disabled).toBe(false);
    const row = button.closest(".row") as HTMLElement;
    expect(within(row).getByText("Test controller")).toBeTruthy();
    expect(
      within(row).getByRole("button", { name: "调校 Test controller" }),
    ).toBeTruthy();
    // 识别, then 调校, then the menu with 重新映射… (IN-18), in one control group.
    expect(
      [...row.querySelectorAll("button")].map(
        (item) => item.textContent || item.getAttribute("aria-label"),
      ),
    ).toEqual(["识别", "调校", "更多操作 Test controller"]);
    expect(row.querySelector(".row-hint")).toBeNull();
    expect(note()).toBeNull();
    // The place for the note is there from the start, empty, as a live region
    // (one that arrives together with its text is often not announced), and
    // 识别 is described by it.
    const place = within(row).getByRole("status");
    expect(place.textContent).toBe("");
    expect(place.id).not.toBe("");
    expect(button.getAttribute("aria-describedby")).toBe(place.id);
    expect(row.contains(place)).toBe(true);
  });

  it("asks for the controller's key, and shows nothing when it can buzz", async () => {
    const app = setup();
    await openPage("设置");
    fireEvent.click(identifyButton());
    await waitFor(() =>
      expect(app.api.identifyDevice).toHaveBeenCalledExactlyOnceWith(
        "controller",
      ),
    );
    await act(async () => {});
    expect(note()).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("is disabled while the request runs and for 2 seconds after the click", async () => {
    fastClock();
    const app = setup();
    let answer: ((cues: DeviceIdentify) => void) | undefined;
    vi.mocked(app.api.identifyDevice).mockImplementationOnce(
      () => new Promise((done) => (answer = done)),
    );
    await openPage("设置");
    fireEvent.click(identifyButton());
    expect(identifyButton().disabled).toBe(true);
    // A second click does nothing, however it is made.
    fireEvent.click(identifyButton());
    expect(app.api.identifyDevice).toHaveBeenCalledTimes(1);
    // Still disabled when the answer is late for the cool-down...
    act(() => {
      vi.advanceTimersByTime(IDENTIFY_COOLDOWN_MS + 500);
    });
    expect(identifyButton().disabled).toBe(true);
    // ...and open again only when both are over.
    await act(async () =>
      answer?.({ rumble: true, led: false, player: false }),
    );
    expect(identifyButton().disabled).toBe(false);

    // An answer that comes at once keeps it disabled until the 2 seconds end.
    fireEvent.click(identifyButton());
    await act(async () => {});
    expect(app.api.identifyDevice).toHaveBeenCalledTimes(2);
    expect(identifyButton().disabled).toBe(true);
    // (The clock runs on by itself too, so the bounds leave some room.)
    act(() => {
      vi.advanceTimersByTime(IDENTIFY_COOLDOWN_MS - 500);
    });
    expect(identifyButton().disabled).toBe(true);
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(identifyButton().disabled).toBe(false);
    // The cool-down is 识别's: 调校 was never disabled by it.
    expect(
      (
        screen.getByRole("button", {
          name: "调校 Test controller",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("says under the name that a controller that cannot buzz or flash cannot, until the next try", async () => {
    fastClock();
    const app = setup();
    vi.mocked(app.api.identifyDevice).mockResolvedValueOnce({
      rumble: false,
      led: false,
      player: false,
    });
    await openPage("设置");
    const row = identifyButton().closest(".row") as HTMLElement;
    const place = within(row).getByRole("status");
    fireEvent.click(identifyButton());
    const text = await screen.findByText("该手柄无法震动或闪灯");
    // Under the name, in the same row; it is announced because the live region
    // was there before and now has the text: the same element.
    expect(row.contains(text)).toBe(true);
    expect(text.getAttribute("role")).toBe("status");
    expect(text).toBe(place);
    expect(identifyButton().getAttribute("aria-describedby")).toBe(text.id);
    expect(
      screen.getByText("Test controller").compareDocumentPosition(text) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // It stays after the button is back...
    act(() => {
      vi.advanceTimersByTime(IDENTIFY_COOLDOWN_MS);
    });
    expect(identifyButton().disabled).toBe(false);
    expect(note()).toBeTruthy();
    // ...and goes with the next click, for good if the controller can show
    // something now.
    vi.mocked(app.api.identifyDevice).mockResolvedValueOnce({
      rumble: true,
      led: false,
      player: false,
    });
    fireEvent.click(identifyButton());
    expect(note()).toBeNull();
    await act(async () => {});
    expect(note()).toBeNull();
    // The live region stays, empty, for the next time.
    expect(within(row).getByRole("status")).toBe(place);
    expect(place.textContent).toBe("");
  });

  it("counts any one cue as something to show", async () => {
    fastClock();
    const app = setup();
    await openPage("设置");
    for (const cues of [
      { rumble: false, led: true, player: false },
      { rumble: false, led: false, player: true },
    ]) {
      vi.mocked(app.api.identifyDevice).mockResolvedValueOnce(cues);
      fireEvent.click(identifyButton());
      await act(async () => {});
      expect(note()).toBeNull();
      act(() => {
        vi.advanceTimersByTime(IDENTIFY_COOLDOWN_MS);
      });
    }
  });

  it("reports a controller that left in the shell's alert, with no note", async () => {
    fastClock();
    const app = setup();
    vi.mocked(app.api.identifyDevice).mockRejectedValueOnce({
      message: "Controller not identified",
      code: -32004,
    });
    await openPage("设置");
    fireEvent.click(identifyButton());
    expect((await screen.findByRole("alert")).textContent).toContain(
      "手柄已断开。",
    );
    expect(note()).toBeNull();
    // Another try is possible once the 2 seconds are over.
    expect(identifyButton().disabled).toBe(true);
    act(() => {
      vi.advanceTimersByTime(IDENTIFY_COOLDOWN_MS);
    });
    expect(identifyButton().disabled).toBe(false);
  });

  it("works while config.json cannot be written, which only calibrating needs", async () => {
    const app = setup({
      config: { text: "{", revision: "rev", problem: "Unexpected end" },
    });
    await openPage("设置");
    expect(identifyButton().disabled).toBe(false);
    fireEvent.click(identifyButton());
    await waitFor(() => expect(app.api.identifyDevice).toHaveBeenCalledOnce());
  });

  it("has nothing to identify without a controller", async () => {
    setup({ snapshot: { ...status, devices: [], slots: [], combos: [] } });
    fireEvent.click(await screen.findByRole("button", { name: "设置" }));
    expect(screen.getByText("未连接手柄")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^识别/ })).toBeNull();
  });

  it("speaks English", async () => {
    localStorage.setItem("padmate.language", "en");
    const app = setup();
    vi.mocked(app.api.identifyDevice).mockResolvedValueOnce({
      rumble: false,
      led: false,
      player: false,
    });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    const button = identifyButton("Identify Test controller");
    expect(button.textContent).toBe("Identify");
    fireEvent.click(button);
    expect(
      await screen.findByText("This controller cannot rumble or flash."),
    ).toBeTruthy();
  });
});

describe("controller calibration (IN-14)", () => {
  const calibrated = (calibration: unknown) => ({
    text: JSON.stringify({
      schemaVersion: 0,
      devices: { controller: { calibration } },
    }),
    revision: "rev",
  });
  const profileWithAnalog = {
    schemaVersion: 0,
    name: "Paint",
    analog: { ls: { deadzone: 0.2, saturation: 0.9 } },
    layers: { base: {} },
  } as unknown as Profile;

  async function openDialog() {
    await openPage("设置");
    fireEvent.click(
      screen.getByRole("button", { name: "调校 Test controller" }),
    );
    const dialog = screen.getByRole("dialog", { name: "调校 Test controller" });
    // The values of the profile in use are read when it opens.
    await waitFor(() =>
      expect(
        dialog.querySelector(".calibration-sticks")?.getAttribute("aria-busy"),
      ).toBe("false"),
    );
    return dialog;
  }
  const slider = (dialog: HTMLElement, stick: string, name: string) =>
    within(within(dialog).getByRole("group", { name: stick })).getByRole(
      "slider",
      { name },
    ) as HTMLInputElement;
  const written = (app: ReturnType<typeof setup>, call = 0) =>
    JSON.parse(vi.mocked(app.api.putConfig).mock.calls[call]?.[0] as string);

  it("lists the connected controllers, each with a calibrate button", async () => {
    setup();
    await openPage("设置");
    expect(
      screen.getByRole("heading", { name: "手柄调校", level: 2 }),
    ).toBeTruthy();
    const button = screen.getByRole("button", { name: "调校 Test controller" });
    expect(button.textContent).toBe("调校");
    expect((button as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByText("已调校")).toBeNull();
    // No helper line under it.
    expect(button.closest(".row")?.querySelector(".row-hint")).toBeNull();
  });

  it("has nothing to calibrate without a controller", async () => {
    setup({ snapshot: { ...status, devices: [], slots: [], combos: [] } });
    fireEvent.click(await screen.findByRole("button", { name: "设置" }));
    expect(screen.getByText("未连接手柄")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^调校/ })).toBeNull();
  });

  it("marks a controller that has a calibration", async () => {
    setup({ config: calibrated({ ls: { deadzone: 0.2 } }) });
    await openPage("设置");
    expect(screen.getByText("已调校")).toBeTruthy();
  });

  it("shows the profile values for what the controller does not set", async () => {
    setup({
      profile: profileWithAnalog,
      config: calibrated({ rs: { deadzone: 0.3 } }),
    });
    const dialog = await openDialog();
    expect(dialog.textContent).toContain("左摇杆");
    expect(dialog.textContent).toContain("右摇杆");
    // The left stick has no calibration: the profile's 20 % and 90 %.
    expect(slider(dialog, "左摇杆", "死区").value).toBe("20");
    expect(slider(dialog, "左摇杆", "推到底").value).toBe("90");
    // The right stick: its own dead zone, the default outer edge.
    expect(slider(dialog, "右摇杆", "死区").value).toBe("30");
    expect(slider(dialog, "右摇杆", "推到底").value).toBe("95");
    const left = within(within(dialog).getByRole("group", { name: "左摇杆" }));
    expect(left.getByText("20%")).toBeTruthy();
    expect(left.getByText("90%")).toBeTruthy();
    // No helper text under the title.
    expect(dialog.querySelector(".dialog-subtitle")).toBeNull();
    expect(
      within(dialog).getByRole("button", { name: "恢复默认" }),
    ).toBeTruthy();
  });

  it("falls back to 15 % and 95 % without a profile", async () => {
    setup();
    const dialog = await openDialog();
    expect(slider(dialog, "左摇杆", "死区").value).toBe("15");
    expect(slider(dialog, "左摇杆", "推到底").value).toBe("95");
  });

  it("shows the stick as it is held, inside its two circles", async () => {
    const app = setup({ profile: profileWithAnalog });
    const dialog = await openDialog();
    const view = within(dialog).getByRole("img", { name: "左摇杆的位置" });
    const dot = () => view.querySelector(".calibration-dot") as SVGElement;
    expect(dot().getAttribute("cx")).toBe("56");
    expect(dot().getAttribute("cy")).toBe("56");
    // The dead zone and outer circles are the sliders' values.
    expect(view.querySelector(".calibration-dead")?.getAttribute("r")).toBe(
      String(0.2 * 52),
    );
    expect(view.querySelector(".calibration-outer")?.getAttribute("r")).toBe(
      String(0.9 * 52),
    );
    app.event("input", {
      device: 1,
      kind: "axis",
      control: "leftx",
      value: 16384,
    });
    app.event("input", {
      device: 1,
      kind: "axis",
      control: "lefty",
      value: -32768,
    });
    // Half right, fully up: the corner is pulled back to the rim.
    const cx = Number(dot().getAttribute("cx"));
    const cy = Number(dot().getAttribute("cy"));
    expect(Math.hypot(cx - 56, cy - 56)).toBeCloseTo(52, 4);
    expect(cx).toBeGreaterThan(56);
    expect(cy).toBeLessThan(56);
    expect(view.getAttribute("data-active")).toBe("true");
    // The right stick did not move.
    const right = within(dialog).getByRole("img", { name: "右摇杆的位置" });
    expect(right.querySelector(".calibration-dot")?.getAttribute("cx")).toBe(
      "56",
    );
    expect(right.getAttribute("data-active")).toBe("false");
    // Moving the slider moves the circle at once, before anything is saved.
    fireEvent.change(slider(dialog, "左摇杆", "死区"), {
      target: { value: "30" },
    });
    expect(view.querySelector(".calibration-dead")?.getAttribute("r")).toBe(
      String(0.3 * 52),
    );
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("saves a move of one slider once it stops, under the controller key", async () => {
    const app = setup({ profile: profileWithAnalog });
    const dialog = await openDialog();
    const dead = slider(dialog, "左摇杆", "死区");
    fireEvent.change(dead, { target: { value: "22" } });
    fireEvent.change(dead, { target: { value: "24" } });
    fireEvent.change(dead, { target: { value: "25" } });
    expect(app.api.putConfig).not.toHaveBeenCalled();
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    // One write for the whole drag, and only the member that moved.
    expect(written(app)).toEqual({
      schemaVersion: 0,
      devices: { controller: { calibration: { ls: { deadzone: 0.25 } } } },
    });
    expect(vi.mocked(app.api.putConfig).mock.calls[0]?.[1]).toBe("rev");
    // The slider keeps its place once saved.
    await waitFor(() => expect(dead.value).toBe("25"));
  });

  it("writes both members and both sticks of one pause together", async () => {
    const app = setup();
    const dialog = await openDialog();
    fireEvent.change(slider(dialog, "左摇杆", "死区"), {
      target: { value: "10" },
    });
    fireEvent.change(slider(dialog, "左摇杆", "推到底"), {
      target: { value: "85" },
    });
    fireEvent.change(slider(dialog, "右摇杆", "推到底"), {
      target: { value: "80" },
    });
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(written(app)).toEqual({
      schemaVersion: 0,
      devices: {
        controller: {
          calibration: {
            ls: { deadzone: 0.1, saturation: 0.85 },
            rs: { saturation: 0.8 },
          },
        },
      },
    });
  });

  it("keeps the dead zone and the outer edge apart", async () => {
    const app = setup({
      config: calibrated({ ls: { deadzone: 0.1, saturation: 0.5 } }),
    });
    const dialog = await openDialog();
    const dead = slider(dialog, "左摇杆", "死区");
    expect(dead.min).toBe("0");
    expect(dead.max).toBe("50");
    expect(slider(dialog, "左摇杆", "推到底").min).toBe("50");
    expect(slider(dialog, "左摇杆", "推到底").max).toBe("100");
    // The dead zone stops 5 % short of the outer edge.
    fireEvent.change(dead, { target: { value: "50" } });
    expect(dead.value).toBe("45");
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(written(app).devices.controller.calibration.ls).toEqual({
      deadzone: 0.45,
      saturation: 0.5,
    });
    // The outer edge is already at its lowest and does not go under it.
    fireEvent.change(slider(dialog, "左摇杆", "推到底"), {
      target: { value: "50" },
    });
    expect(slider(dialog, "左摇杆", "推到底").value).toBe("50");
  });

  it("saves what is moved when the dialog closes", async () => {
    const app = setup();
    const dialog = await openDialog();
    fireEvent.change(slider(dialog, "右摇杆", "死区"), {
      target: { value: "12" },
    });
    expect(app.api.putConfig).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "关闭调校" }));
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(written(app).devices.controller.calibration).toEqual({
      rs: { deadzone: 0.12 },
    });
    expect(
      screen.queryByRole("dialog", { name: "调校 Test controller" }),
    ).toBeNull();
  });

  it("restores the defaults by removing the calibration of the controller", async () => {
    const app = setup({
      config: {
        text: JSON.stringify({
          schemaVersion: 0,
          devices: {
            controller: { calibration: { ls: { deadzone: 0.2 } } },
            other: { ignored: true },
          },
        }),
        revision: "rev",
      },
    });
    const dialog = await openDialog();
    const reset = within(dialog).getByRole("button", {
      name: "恢复默认",
    }) as HTMLButtonElement;
    expect(reset.disabled).toBe(false);
    fireEvent.click(reset);
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    expect(written(app)).toEqual({
      schemaVersion: 0,
      devices: { other: { ignored: true } },
    });
    await waitFor(() =>
      expect(slider(dialog, "左摇杆", "死区").value).toBe("15"),
    );
    await waitFor(() => expect(reset.disabled).toBe(true));
  });

  it("has nothing to restore before anything is calibrated", async () => {
    const app = setup();
    const dialog = await openDialog();
    expect(
      (
        within(dialog).getByRole("button", {
          name: "恢复默认",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    // Moving a slider to where it is writes nothing.
    fireEvent.change(slider(dialog, "左摇杆", "死区"), {
      target: { value: "15" },
    });
    await act(() => new Promise((resolve) => setTimeout(resolve, 600)));
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("drops a move that could not be saved, with the reason", async () => {
    const app = setup();
    const dialog = await openDialog();
    vi.mocked(app.api.putConfig).mockRejectedValueOnce(new Error("disk full"));
    const dead = slider(dialog, "左摇杆", "死区");
    fireEvent.change(dead, { target: { value: "30" } });
    await within(dialog).findByRole("alert");
    // The slider shows what is saved again.
    await waitFor(() => expect(dead.value).toBe("15"));
  });

  it("cannot be changed while config.json cannot be written", async () => {
    setup({
      config: { text: "{", revision: "rev", problem: "Unexpected end" },
    });
    await openPage("设置");
    expect(
      (
        screen.getByRole("button", {
          name: "调校 Test controller",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("speaks English", async () => {
    setup({ config: calibrated({ ls: { deadzone: 0.2 } }) });
    await openPage("设置");
    await userEvent
      .setup()
      .selectOptions(screen.getByRole("combobox", { name: "语言" }), "en");
    expect(
      screen.getByRole("heading", { name: "Controller calibration" }),
    ).toBeTruthy();
    expect(screen.getByText("Calibrated")).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "Calibrate Test controller" }),
    );
    const dialog = screen.getByRole("dialog", {
      name: "Calibrate Test controller",
    });
    expect(
      within(
        within(dialog).getByRole("group", { name: "Left stick" }),
      ).getByRole("slider", { name: "Dead zone" }),
    ).toBeTruthy();
    expect(
      within(
        within(dialog).getByRole("group", { name: "Right stick" }),
      ).getByRole("slider", { name: "Full push" }),
    ).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Reset" })).toBeTruthy();
  });
});

describe("the battery on the controller's row (IN-12)", () => {
  /** The status with a power on its one controller. */
  const powered = (battery?: DeviceBattery): StatusResult => ({
    ...status,
    devices: status.devices.map((device) => ({
      ...device,
      ...(battery ? { battery } : {}),
    })),
  });
  /** The row of the controller: its name and tags. */
  const row = () =>
    screen
      .getByRole("button", { name: /^(识别|Identify) Test controller$/ })
      .closest(".row") as HTMLElement;
  const tags = () =>
    [...row().querySelectorAll(".row-title .tag")].map(
      (tag) => tag.textContent,
    );
  /** A state event of the daemon with the batteries it lists. */
  const state = (batteries: unknown[]) => ({
    kind: "state",
    slots: status.slots,
    paused: false,
    capturing: false,
    suspended: "none",
    batteries,
  });
  const reading = (
    state: string,
    percent: number | undefined,
    low = false,
  ) => ({
    device: 1,
    key: "controller",
    name: "Test controller",
    state,
    ...(percent === undefined ? {} : { percent }),
    low,
  });

  it("shows the charge in a tag beside the name, and nothing for a controller that has not read one", async () => {
    setup();
    await openPage("设置");
    expect(tags()).toEqual([]);
    cleanup();
    setup({ snapshot: powered({ state: "discharging", percent: 80 }) });
    await openPage("设置");
    expect(tags()).toEqual(["电量 80%"]);
    // In the row's title, with the name, and not as a line of its own.
    expect(within(row()).getByText("电量 80%").closest(".row-title")).toBe(
      row().querySelector(".row-title"),
    );
    expect(row().querySelector(".row-hint")).toBeNull();
  });

  it("says 充电中 while it charges, with the charge when it has one", async () => {
    setup({ snapshot: powered({ state: "charging", percent: 55 }) });
    await openPage("设置");
    expect(tags()).toEqual(["电量 55%", "充电中"]);
    cleanup();
    setup({ snapshot: powered({ state: "charging" }) });
    await openPage("设置");
    expect(tags()).toEqual(["充电中"]);
  });

  it("shows nothing for unknown, for a controller that is wired, and for one that discharges without a charge", async () => {
    // A wired controller has no battery to show, even with a full charge: a
    // receiver says full for ever (ADR-0067).
    for (const battery of [
      { state: "unknown" },
      { state: "wired" },
      { state: "wired", percent: 100 },
      { state: "wired", percent: 40 },
      { state: "unknown", percent: 50 },
      { state: "discharging" },
    ] as const) {
      setup({ snapshot: powered(battery) });
      await openPage("设置");
      expect(tags(), JSON.stringify(battery)).toEqual([]);
      cleanup();
    }
  });

  it("follows the state events of the daemon, English included", async () => {
    const app = setup({
      snapshot: powered({ state: "discharging", percent: 80 }),
    });
    await openPage("设置");
    expect(tags()).toEqual(["电量 80%"]);
    app.event("engine.activity", state([reading("discharging", 20, true)]));
    expect(tags()).toEqual(["电量 20%"]);
    app.event("engine.activity", state([reading("charging", undefined)]));
    expect(tags()).toEqual(["充电中"]);
    // A list that does not mention the controller leaves what is known.
    app.event("engine.activity", state([]));
    expect(tags()).toEqual(["充电中"]);
    app.event("engine.activity", state([reading("unknown", undefined)]));
    expect(tags()).toEqual([]);
    app.event("engine.activity", state([reading("discharging", 64)]));
    await userEvent
      .setup()
      .selectOptions(screen.getByRole("combobox", { name: "语言" }), "en");
    expect(tags()).toEqual(["Battery 64%"]);
    app.event("engine.activity", state([reading("charging", 70)]));
    expect(tags()).toEqual(["Battery 70%", "Charging"]);
  });
});
