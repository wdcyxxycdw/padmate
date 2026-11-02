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
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigGetResult,
  ConfigPutResult,
  DaemonEvent,
  DeviceEntry,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import type {
  PadInfo,
  PadSwitches,
  PadsListResult,
  PadsSetParams,
  PadsSetResult,
} from "../shared/pads";
import { App } from "./App";
import { createPadmateStore } from "./state/store";
import { editorAPIFixture } from "./testing/editor-api-fixture";
import { padFixture, padsFixture, padsOfDevices } from "./testing/pads-fixture";
import { sdlPads } from "./testing/sdl-pads";

// Settings → 手柄设置 (ADR-0017): the function key, the feedback switches and
// the emergency pause of each controller that has been connected, and the
// function key of the controller the mapping page edits. The daemon is a
// small fake that keeps the list and applies pads.set and pads.migrate the
// way the real one does (cmd/padmated/ipc_pads.go).

const entry = (
  id: number,
  name: string,
  pad: { type: string; buttonMask?: number; axisMask?: number },
  key = `key-${id}`,
): DeviceEntry => ({
  info: {
    id,
    name,
    type: pad.type,
    vendor: 0,
    product: 0,
    version: 0,
    guid: "test",
    driver: "virtual",
    path: "",
    serial: "",
    gamepad: true,
    virtual: true,
    ...(pad.buttonMask === undefined ? {} : { buttonMask: pad.buttonMask }),
    ...(pad.axisMask === undefined ? {} : { axisMask: pad.axisMask }),
  },
  key,
  slot: id - 1,
  status: "mapped",
  disabled: false,
});

const xbox = entry(1, "Test controller", sdlPads.xboxSeries, "xbox-key");
const joyConRight = entry(
  2,
  "Joy-Con (R)",
  sdlPads.joyConRightUpright,
  "joycon-key",
);

const snapshot = (devices: DeviceEntry[]): StatusResult => ({
  version: "1.1.0",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "none",
  profileId: "paint",
  slots: devices.map((device) => ({
    slot: device.slot,
    device: device.info.id,
    key: device.key,
    name: device.info.name,
    profile: "Paint",
    generation: 1,
    layers: { base: "base", overlays: [], color: "" },
    latches: [],
    mode: 0,
  })),
  devices,
  combos: [],
});

const allOn: PadSwitches = {
  rumble: true,
  led: true,
  pause: true,
  lowBattery: true,
};

/** pads.set as the daemon applies it to the list it keeps. */
function applied(pad: PadInfo, params: PadsSetParams): PadInfo {
  let next = pad;
  if (params.functionKey !== undefined) {
    const key = params.functionKey ?? pad.functionKey.default;
    next = {
      ...next,
      functionKey: {
        ...next.functionKey,
        effective: key,
        source: params.functionKey === null ? "default" : "device",
        ...(params.functionKey === null
          ? { stored: undefined }
          : { stored: params.functionKey }),
      },
    };
    if (params.functionKey === null) delete next.functionKey.stored;
  }
  if (params.feedback !== undefined) {
    next = {
      ...next,
      feedback: {
        effective: { ...allOn, ...(params.feedback ?? {}) },
        source: params.feedback === null ? "default" : "device",
        ...(params.feedback === null ? {} : { stored: params.feedback }),
      },
    };
  }
  if (params.panic !== undefined) {
    next = {
      ...next,
      panic: {
        default: pad.panic.default,
        effective: params.panic ?? pad.panic.default,
        source: params.panic === null ? "default" : "device",
        ...(params.panic === null ? {} : { stored: params.panic }),
      },
    };
  }
  return next;
}

function setup({
  devices = [xbox],
  pads = padsOfDevices(devices),
  config = { text: '{"schemaVersion":0}', revision: "rev" },
  migrated,
}: {
  devices?: DeviceEntry[];
  pads?: PadsListResult;
  config?: ConfigGetResult | Error;
  /** What pads.migrate leaves: the list with the retired members folded in. */
  migrated?: PadInfo[];
} = {}) {
  const listeners = new Set<(event: DaemonEvent) => void>();
  let current = snapshot(devices);
  let list = pads;
  let revision = 0;
  const fixture = editorAPIFixture();
  fixture.listPads.mockImplementation(async () => list);
  fixture.setPad.mockImplementation(
    async (params: PadsSetParams): Promise<PadsSetResult> => {
      const found = list.pads.find((pad) => pad.key === params.key);
      if (!found) throw Object.assign(new Error("unknown"), { code: -32004 });
      const pad = applied(found, params);
      list = {
        ...list,
        revision: `pads-${++revision}`,
        pads: list.pads.map((one) => (one.key === pad.key ? pad : one)),
      };
      return { revision: list.revision, applied: true, pad };
    },
  );
  fixture.migratePads.mockImplementation(async () => {
    list = {
      revision: `pads-${++revision}`,
      legacy: { any: false, functionKey: false, feedback: false, panic: false },
      pads: migrated ?? list.pads,
    };
    return {
      revision: list.revision,
      applied: true,
      changed: true,
      legacy: list.legacy,
      pads: list.pads,
    };
  });
  const api: PadmateAPI = {
    ...fixture,
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next) => {
      listeners.add(next);
      return () => {
        listeners.delete(next);
      };
    }),
    getStatus: vi.fn(async () => current),
    getDevices: vi.fn(async () => current.devices),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    getConfig: vi.fn(async () => {
      if (config instanceof Error) throw config;
      return config;
    }),
    putConfig: vi.fn(
      async (): Promise<ConfigPutResult> => ({
        revision: "saved",
        applied: true,
        restartRequired: false,
      }),
    ),
    getScriptStatus: vi.fn(async () => []),
    getScriptRegistrations: vi.fn(async () => []),
    reloadScripts: vi.fn(async () => []),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
  };
  const store = createPadmateStore(api);
  render(<App store={store} />);
  return {
    api,
    store,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => {
        for (const listener of listeners) listener({ topic, data });
      }),
    /** The daemon's controllers change. */
    daemon: (next: { devices?: DeviceEntry[]; pads?: PadsListResult }) => {
      if (next.devices) current = snapshot(next.devices);
      if (next.pads) list = next.pads;
    },
  };
}

async function openSettings() {
  fireEvent.click(await screen.findByRole("button", { name: "设置" }));
  await screen.findByRole("heading", { name: "手柄调校", level: 2 });
}

async function openPads() {
  await openSettings();
  fireEvent.click(screen.getByRole("button", { name: "打开手柄设置" }));
  await screen.findByTestId("pads");
}

const sections = () => screen.getAllByTestId("pad-section");
const sectionOf = (name: string) =>
  within(screen.getByRole("region", { name }));
const keyGroup = (scope: ReturnType<typeof within>) =>
  scope.getByRole("radiogroup", { name: /^(功能键|Function key)/ });
const chosen = (group: HTMLElement) =>
  within(group)
    .getAllByRole("radio")
    .find((radio) => radio.getAttribute("aria-checked") === "true")
    ?.getAttribute("data-value");

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("Settings → 手柄设置", () => {
  it("is one row of 通用, and the function key, feedback and emergency pause are no longer in Settings", async () => {
    setup();
    await openSettings();
    const general = screen.getByRole("heading", { name: "通用", level: 2 })
      .parentElement as HTMLElement;
    const row = within(general).getByText("手柄设置", {
      selector: ".row-title",
    });
    expect(row.closest(".row")?.querySelector(".row-hint")?.textContent).toBe(
      "功能键、反馈和紧急暂停",
    );
    const headings = [...document.querySelectorAll(".section-title")].map(
      (heading) => heading.textContent,
    );
    expect(headings).not.toContain("反馈");
    for (const gone of ["功能键", "紧急暂停", "震动", "灯光", "暂停提示"])
      expect(screen.queryByText(gone, { selector: ".row-title" })).toBeNull();
    expect(screen.queryByRole("button", { name: "修改紧急暂停" })).toBeNull();
    // What is not about one controller stays.
    expect(screen.getByText("松开所有按键")).toBeTruthy();
    expect(screen.getByText("手柄调校")).toBeTruthy();
  });

  it("opens inside Settings, goes back with the back control or Escape, and gives the focus back to its button", async () => {
    setup();
    await openPads();
    expect(
      screen.getByRole("heading", { name: "手柄设置", level: 1 }),
    ).toBeTruthy();
    expect(document.title).toContain("手柄设置");
    // The rail still marks Settings.
    expect(
      screen.getByRole("button", { name: "设置" }).getAttribute("aria-current"),
    ).toBe("page");
    fireEvent.click(screen.getByRole("button", { name: "返回设置" }));
    await screen.findByRole("heading", { name: "手柄调校", level: 2 });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "打开手柄设置" }),
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "打开手柄设置" }));
    await screen.findByTestId("pads");
    fireEvent.keyDown(document.body, { key: "Escape" });
    await screen.findByRole("heading", { name: "手柄调校", level: 2 });
  });

  it("is in English with the UI", async () => {
    setup();
    await openSettings();
    fireEvent.change(screen.getByRole("combobox", { name: "语言" }), {
      target: { value: "en" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Open controller settings" }),
    );
    await screen.findByTestId("pads");
    expect(
      screen.getByRole("heading", { name: "Controllers", level: 1 }),
    ).toBeTruthy();
    const pad = sectionOf("Test controller");
    expect(pad.getByText("Function key")).toBeTruthy();
    expect(pad.getByText("Feedback")).toBeTruthy();
    expect(
      ["Rumble", "Lights", "Pause alert", "Low battery alert"].map((name) =>
        pad.getByRole("switch", { name }).getAttribute("aria-checked"),
      ),
    ).toEqual(["true", "true", "true", "true"]);
    expect(pad.getByText("Emergency pause")).toBeTruthy();
    expect(
      pad.getByText("Hold 2 s to release all keys and pause"),
    ).toBeTruthy();
    expect(pad.getByText("Connected")).toBeTruthy();
    expect(
      within(keyGroup(pad))
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["Menu (default)", "View"]);
    expect(
      pad.getByRole("button", {
        name: "Change emergency pause Test controller",
      }),
    ).toBeTruthy();
  });
});

describe("the controllers listed", () => {
  it("are the connected ones as sections, each with its kind and that it is connected, and the others in a fold, closed", async () => {
    const away = padFixture("joyConLeft", {
      key: "away-key",
      name: "",
      connected: false,
    });
    setup({
      devices: [xbox, joyConRight],
      pads: padsFixture([
        ...padsOfDevices([xbox, joyConRight]).pads,
        away,
        padFixture("xbox", {
          key: "never-key",
          name: "",
          type: "unknown",
          known: false,
          connected: false,
        }),
      ]),
    });
    await openPads();
    expect(
      sections().map((section) => section.getAttribute("data-pad")),
    ).toEqual(["xbox-key", "joycon-key", "away-key", "never-key"]);
    const xboxSection = sectionOf("Test controller");
    expect(xboxSection.getByText("Xbox")).toBeTruthy();
    expect(xboxSection.getByText("已连接")).toBeTruthy();
    const right = sectionOf("Joy-Con (R)");
    expect(right.getByText("Joy-Con（右）")).toBeTruthy();
    // The others are one fold away, with their count, and closed.
    const fold = screen.getByTestId("pads-others") as HTMLDetailsElement;
    expect(fold.open).toBe(false);
    expect(within(fold).getByText("以前连接过的手柄")).toBeTruthy();
    expect(fold.querySelector(".disclosure-summary")?.textContent).toBe("2");
    // A controller without a name of its own is called by its kind.
    expect(
      within(fold).getByRole("heading", { name: "Joy-Con（左）" }),
    ).toBeTruthy();
    expect(
      within(fold).getByRole("heading", { name: "通用手柄" }),
    ).toBeTruthy();
    expect(within(fold).getAllByText("未连接")).toHaveLength(2);
  });

  it("show the fold open when none is connected, and a note when none ever was", async () => {
    setup({
      devices: [],
      pads: padsFixture([
        padFixture("xbox", { key: "away-key", connected: false }),
      ]),
    });
    await openPads();
    expect((screen.getByTestId("pads-others") as HTMLDetailsElement).open).toBe(
      true,
    );
    cleanup();
    setup({ devices: [], pads: padsFixture([]) });
    await openPads();
    expect(screen.getByTestId("pads-empty").textContent).toBe(
      "还没有连接过手柄",
    );
  });

  it("are listed again when a controller comes, and when config.json is written", async () => {
    const app = setup();
    await openPads();
    await waitFor(() => expect(sections()).toHaveLength(1));
    const reads = () => vi.mocked(app.api.listPads).mock.calls.length;
    const before = reads();
    // A controller comes: the daemon lists it, and the page learns it from
    // the input event and the status it then reads.
    app.daemon({
      devices: [xbox, joyConRight],
      pads: padsOfDevices([xbox, joyConRight]),
    });
    app.event("input", { kind: "added", device: 2 });
    await waitFor(() => expect(sections()).toHaveLength(2));
    expect(reads()).toBeGreaterThan(before);
    // config.json is written elsewhere.
    const afterDevice = reads();
    app.daemon({
      pads: padsOfDevices([xbox, joyConRight], { revision: "elsewhere" }),
    });
    vi.mocked(app.api.getConfig).mockResolvedValue({
      text: '{"schemaVersion":0}',
      revision: "elsewhere",
    });
    app.event("profile.changed", { kind: "config", revision: "elsewhere" });
    await waitFor(() => expect(reads()).toBeGreaterThan(afterDevice));
    // And a controller going away.
    app.daemon({ devices: [xbox], pads: padsOfDevices([xbox]) });
    app.event("input", { kind: "removed", device: 2 });
    await waitFor(() => expect(sections()).toHaveLength(1));
  });

  it("say so when they cannot be read", async () => {
    const app = setup();
    vi.mocked(app.api.listPads).mockRejectedValue(new Error("daemon"));
    await openSettings();
    // Reading them failed from the start: the page has no list to show.
    app.store.setState({ pads: null });
    fireEvent.click(screen.getByRole("button", { name: "打开手柄设置" }));
    await screen.findByText("未能读取手柄列表");
    vi.mocked(app.api.listPads).mockResolvedValue(padsOfDevices([xbox]));
    fireEvent.click(screen.getByRole("button", { name: "重新读取" }));
    await screen.findByTestId("pad-section");
  });
});

describe("the function key of a controller", () => {
  it("is a choice among the keys the daemon lists for it, with the default marked, as the controller prints them", async () => {
    setup({ devices: [xbox, joyConRight] });
    await openPads();
    const x = keyGroup(sectionOf("Test controller"));
    expect(
      within(x)
        .getAllByRole("radio")
        .map((radio) => [radio.textContent, radio.getAttribute("data-value")]),
    ).toEqual([
      ["Menu（默认）", "start"],
      ["View", "back"],
    ]);
    expect(chosen(x)).toBe("start");
    // The icon of the key as the controller draws it, then its name.
    expect(x.querySelectorAll(".control-icon")).toHaveLength(2);
    const j = keyGroup(sectionOf("Joy-Con (R)"));
    expect(
      within(j)
        .getAllByRole("radio")
        .map((radio) => [radio.textContent, radio.getAttribute("data-value")]),
    ).toEqual([
      ["ZR（默认）", "rt"],
      ["R", "rightshoulder"],
    ]);
    expect(chosen(j)).toBe("rt");
    expect(
      [...j.querySelectorAll("svg[data-pad-icon]")].map((icon) =>
        icon.getAttribute("data-pad-icon"),
      ),
    ).toEqual(["kenney/switch_button_zr", "kenney/switch_button_r"]);
    // One sentence says what it does; nothing says where the key sits.
    expect(
      sectionOf("Joy-Con (R)").getByText("按住时其他键换成另一套"),
    ).toBeTruthy();
  });

  it("is set for this controller alone, by the control chosen", async () => {
    const app = setup({ devices: [xbox, joyConRight] });
    await openPads();
    const j = keyGroup(sectionOf("Joy-Con (R)"));
    fireEvent.click(within(j).getByRole("radio", { name: "R" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledExactlyOnceWith({
        key: "joycon-key",
        functionKey: "rightshoulder",
      }),
    );
    await waitFor(() => expect(chosen(j)).toBe("rightshoulder"));
    // The other controller is as it was.
    expect(chosen(keyGroup(sectionOf("Test controller")))).toBe("start");
    await screen.findByText("已自动保存");
  });

  it("is taken out of the file (null) when the default is chosen again", async () => {
    const app = setup({
      devices: [joyConRight],
      pads: padsFixture([
        padFixture("joyConRight", {
          key: "joycon-key",
          name: "Joy-Con (R)",
          device: 2,
          effective: "rightshoulder",
        }),
      ]),
    });
    await openPads();
    const j = keyGroup(sectionOf("Joy-Con (R)"));
    expect(chosen(j)).toBe("rightshoulder");
    fireEvent.click(within(j).getByRole("radio", { name: "ZR（默认）" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledExactlyOnceWith({
        key: "joycon-key",
        functionKey: null,
      }),
    );
    await waitFor(() => expect(chosen(j)).toBe("rt"));
  });

  it("is moved by the arrow keys like radio buttons", async () => {
    const app = setup();
    await openPads();
    const g = keyGroup(sectionOf("Test controller"));
    fireEvent.keyDown(within(g).getAllByRole("radio")[0], {
      key: "ArrowRight",
    });
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledWith({
        key: "xbox-key",
        functionKey: "back",
      }),
    );
  });

  it("shows the choice at once, blocks other changes while it is saved, and shows the stored one when the save fails", async () => {
    const app = setup();
    let fail = (_error: unknown) => {};
    vi.mocked(app.api.setPad).mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          fail = reject;
        }),
    );
    await openPads();
    const section = sectionOf("Test controller");
    const g = keyGroup(section);
    fireEvent.click(within(g).getByRole("radio", { name: "View" }));
    expect(chosen(g)).toBe("back");
    expect(screen.getByText("正在保存…")).toBeTruthy();
    expect(
      (section.getByRole("switch", { name: "灯光" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    await act(async () => fail(new Error("config changed outside padmate")));
    await waitFor(() => expect(chosen(g)).toBe("start"));
    expect(screen.getByRole("alert").textContent).toContain(
      "config changed outside padmate",
    );
    expect(
      (section.getByRole("switch", { name: "灯光" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });
});

describe("the feedback switches of a controller", () => {
  it("are the four of ADR-0015, on by default, with no helper text", async () => {
    setup();
    await openPads();
    const section = sectionOf("Test controller");
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"])
      expect(
        section.getByRole("switch", { name }).getAttribute("aria-checked"),
      ).toBe("true");
    expect(
      [...document.querySelectorAll('[data-testid^="pad-"] .row-title')]
        .map((title) => title.textContent)
        .filter((text) =>
          ["震动", "灯光", "暂停提示", "低电提醒"].includes(text ?? ""),
        ),
    ).toEqual(["震动", "灯光", "暂停提示", "低电提醒"]);
  });

  it("show what the controller has: its own switches off, the others on", async () => {
    const stored = { rumble: false, lowBattery: false };
    setup({
      pads: padsFixture([
        padFixture("xbox", {
          key: "xbox-key",
          name: "Test controller",
          device: 1,
          feedback: {
            stored,
            effective: { ...allOn, ...stored },
            source: "device",
          },
        }),
      ]),
    });
    await openPads();
    const section = sectionOf("Test controller");
    expect(
      ["震动", "灯光", "暂停提示", "低电提醒"].map((name) =>
        section.getByRole("switch", { name }).getAttribute("aria-checked"),
      ),
    ).toEqual(["false", "true", "true", "false"]);
  });

  it("are written for this controller: false when off, and null when the last one goes on", async () => {
    const app = setup({ devices: [xbox, joyConRight] });
    await openPads();
    const section = sectionOf("Joy-Con (R)");
    fireEvent.click(section.getByRole("switch", { name: "震动" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenLastCalledWith({
        key: "joycon-key",
        feedback: { rumble: false },
      }),
    );
    await waitFor(() =>
      expect(
        section
          .getByRole("switch", { name: "震动" })
          .getAttribute("aria-checked"),
      ).toBe("false"),
    );
    fireEvent.click(section.getByRole("switch", { name: "低电提醒" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenLastCalledWith({
        key: "joycon-key",
        feedback: { rumble: false, lowBattery: false },
      }),
    );
    await waitFor(() =>
      expect(
        section
          .getByRole("switch", { name: "低电提醒" })
          .getAttribute("aria-checked"),
      ).toBe("false"),
    );
    fireEvent.click(section.getByRole("switch", { name: "震动" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenLastCalledWith({
        key: "joycon-key",
        feedback: { lowBattery: false },
      }),
    );
    await waitFor(() =>
      expect(
        section
          .getByRole("switch", { name: "震动" })
          .getAttribute("aria-checked"),
      ).toBe("true"),
    );
    fireEvent.click(section.getByRole("switch", { name: "低电提醒" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenLastCalledWith({
        key: "joycon-key",
        feedback: null,
      }),
    );
    // The other controller was not touched.
    expect(
      vi
        .mocked(app.api.setPad)
        .mock.calls.every(([call]) => call.key === "joycon-key"),
    ).toBe(true);
  });

  it("cannot be changed while config.json cannot be written", async () => {
    setup({ config: new Error("no profile library") });
    await openPads();
    const section = sectionOf("Test controller");
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"])
      expect(
        (section.getByRole("switch", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
    for (const radio of within(keyGroup(section)).getAllByRole("radio"))
      expect((radio as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("当前启动方式无法保存设置")).toBeTruthy();
  });
});

describe("the emergency pause of a controller", () => {
  const hold = (app: ReturnType<typeof setup>, buttons: string[]) => {
    for (const control of buttons)
      app.event("input", { device: 1, kind: "down", control });
    for (const control of buttons)
      app.event("input", { device: 1, kind: "up", control });
  };

  it("shows the combo the daemon detects, in the names the controller prints, and the hold time", async () => {
    setup({ devices: [xbox, joyConRight] });
    await openPads();
    const x = sectionOf("Test controller");
    expect(x.getByRole("img", { name: "View + Menu" })).toBeTruthy();
    expect(x.getByText("按住 2 秒：松开所有按键并暂停")).toBeTruthy();
    // A single Joy-Con is held upright: its SL and SR are the combo.
    const j = sectionOf("Joy-Con (R)");
    expect(j.getByRole("img", { name: "SL + SR" })).toBeTruthy();
  });

  it("shows a left Joy-Con's combo and a hold time that is not one of the usual", async () => {
    const left = entry(3, "Joy-Con (L)", sdlPads.joyConLeftUpright, "left-key");
    const pad = padsOfDevices([left]).pads[0];
    setup({
      devices: [left],
      pads: padsFixture([
        {
          ...pad,
          panic: {
            ...pad.panic,
            effective: { buttons: ["paddle2", "paddle4"], holdMs: 1500 },
          },
        },
      ]),
    });
    await openPads();
    const section = sectionOf("Joy-Con (L)");
    expect(section.getByRole("img", { name: "SL + SR" })).toBeTruthy();
    expect(section.getByText("按住 1.5 秒：松开所有按键并暂停")).toBeTruthy();
  });

  it("is recorded on this controller and saved for it alone", async () => {
    const app = setup();
    await openPads();
    fireEvent.click(
      screen.getByRole("button", { name: "修改紧急暂停 Test controller" }),
    );
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    await waitFor(() =>
      expect(app.api.padCaptureStart).toHaveBeenCalledTimes(1),
    );
    const save = within(dialog).getByRole("button", { name: "保存" });
    const restore = within(dialog).getByRole("button", { name: "恢复默认" });
    // Nothing recorded or changed, and no combo of its own to restore.
    expect((save as HTMLButtonElement).disabled).toBe(true);
    expect((restore as HTMLButtonElement).disabled).toBe(true);
    // One button is not a combo.
    hold(app, ["south"]);
    expect(within(dialog).getByRole("alert").textContent).toBe(
      "需同时按住 2–4 个按键",
    );
    // Only this controller's buttons count: another connection's do not.
    app.event("input", { device: 9, kind: "down", control: "north" });
    app.event("input", { device: 9, kind: "up", control: "north" });
    app.event("input", { device: 1, kind: "down", control: "leftshoulder" });
    app.event("input", { device: 1, kind: "down", control: "east" });
    app.event("input", { device: 1, kind: "up", control: "leftshoulder" });
    app.event("input", { device: 1, kind: "up", control: "east" });
    expect(within(dialog).getByRole("img", { name: "LB + B" })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("radio", { name: "3 秒" }));
    fireEvent.click(save);
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledExactlyOnceWith({
        key: "xbox-key",
        panic: { buttons: ["leftshoulder", "east"], holdMs: 3000 },
      }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "修改紧急暂停" })).toBeNull(),
    );
    expect(app.api.padCaptureStop).toHaveBeenCalledTimes(1);
    // The row shows the new combo.
    await waitFor(() =>
      expect(
        sectionOf("Test controller").getByRole("img", { name: "LB + B" }),
      ).toBeTruthy(),
    );
    expect(app.api.putConfig).not.toHaveBeenCalled();
  });

  it("can go back to the default of the controller, which sends null", async () => {
    const own = { buttons: ["south", "east"], holdMs: 1000 };
    const pad = padsOfDevices([xbox]).pads[0];
    const app = setup({
      pads: padsFixture([
        {
          ...pad,
          panic: {
            default: pad.panic.default,
            stored: own,
            effective: own,
            source: "device",
          },
        },
      ]),
    });
    await openPads();
    fireEvent.click(
      screen.getByRole("button", { name: "修改紧急暂停 Test controller" }),
    );
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    fireEvent.click(within(dialog).getByRole("button", { name: "恢复默认" }));
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledExactlyOnceWith({
        key: "xbox-key",
        panic: null,
      }),
    );
    await waitFor(() =>
      expect(
        sectionOf("Test controller").getByRole("img", { name: "View + Menu" }),
      ).toBeTruthy(),
    );
  });

  it("shows a failed save inside the dialog", async () => {
    const app = setup();
    await openPads();
    fireEvent.click(
      screen.getByRole("button", { name: "修改紧急暂停 Test controller" }),
    );
    const dialog = screen.getByRole("dialog", { name: "修改紧急暂停" });
    vi.mocked(app.api.setPad).mockRejectedValueOnce(
      new Error("config.json is locked"),
    );
    fireEvent.click(within(dialog).getByRole("radio", { name: "3 秒" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() =>
      expect(within(dialog).getByRole("alert").textContent).toBe(
        "config.json is locked",
      ),
    );
    // The shell's alert would sit under the modal dialog.
    expect(screen.getAllByText("config.json is locked")).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "修改紧急暂停" })).toBe(dialog);
  });

  it("closes the dialog and ends the capture when the controller goes away, another one staying", async () => {
    const app = setup({
      devices: [xbox, joyConRight],
      pads: padsOfDevices([xbox, joyConRight]),
    });
    await openPads();
    fireEvent.click(
      screen.getByRole("button", { name: "修改紧急暂停 Joy-Con (R)" }),
    );
    await screen.findByRole("dialog", { name: "修改紧急暂停" });
    await waitFor(() =>
      expect(app.api.padCaptureStart).toHaveBeenCalledTimes(1),
    );
    app.daemon({ devices: [xbox], pads: padsOfDevices([xbox]) });
    app.event("input", { device: 2, kind: "removed" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "修改紧急暂停" })).toBeNull(),
    );
    expect(app.api.padCaptureStop).toHaveBeenCalledTimes(1);
    expect(app.api.setPad).not.toHaveBeenCalled();
    // The Xbox controller's dialog opens fresh.
    fireEvent.click(
      screen.getByRole("button", { name: "修改紧急暂停 Test controller" }),
    );
    const fresh = screen.getByRole("dialog", { name: "修改紧急暂停" });
    expect(
      within(fresh)
        .getByRole("radio", { name: "2 秒" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("is not changed on a controller that is not connected: it has no buttons to record", async () => {
    setup({
      devices: [],
      pads: padsFixture([
        padFixture("xbox", { key: "away-key", connected: false }),
      ]),
    });
    await openPads();
    const fold = within(screen.getByTestId("pads-others"));
    expect(fold.getByRole("img", { name: "View + Menu" })).toBeTruthy();
    expect(fold.queryByRole("button", { name: /修改紧急暂停/ })).toBeNull();
  });
});

describe("the retired global settings (ADR-0017)", () => {
  const legacyConfig = JSON.stringify({
    schemaVersion: 0,
    functionKey: "back",
    feedback: { rumble: false },
    safety: {
      panic: { standard: { buttons: ["south", "east"], holdMs: 1000 } },
    },
    output: { eventGapMs: 0 },
  });
  const legacyPads = () => {
    const pad = padsOfDevices([xbox]).pads[0];
    return padsFixture(
      [
        {
          ...pad,
          functionKey: {
            ...pad.functionKey,
            effective: "back",
            source: "legacy",
          },
        },
      ],
      { legacy: { functionKey: true, feedback: true, panic: true } },
    );
  };
  const folded = () => {
    const pad = padsOfDevices([xbox]).pads[0];
    return [
      {
        ...pad,
        functionKey: {
          ...pad.functionKey,
          effective: "back" as const,
          stored: "back" as const,
          source: "device" as const,
        },
      },
    ];
  };

  it("are not moved when the page opens, nor when a setting is only looked at", async () => {
    const app = setup({
      pads: legacyPads(),
      config: { text: legacyConfig, revision: "rev" },
    });
    await openPads();
    expect(chosen(keyGroup(sectionOf("Test controller")))).toBe("back");
    await act(async () => {});
    expect(app.api.migratePads).not.toHaveBeenCalled();
    expect(app.api.setPad).not.toHaveBeenCalled();
    // Clicking the key that applies already changes nothing.
    fireEvent.click(
      within(keyGroup(sectionOf("Test controller"))).getByRole("radio", {
        name: "View",
      }),
    );
    await act(async () => {});
    expect(app.api.migratePads).not.toHaveBeenCalled();
  });

  it("are folded into the controllers by the first change, before it, and not again", async () => {
    const app = setup({
      pads: legacyPads(),
      migrated: folded(),
      config: { text: legacyConfig, revision: "rev" },
    });
    await openPads();
    fireEvent.click(
      sectionOf("Test controller").getByRole("switch", { name: "灯光" }),
    );
    await waitFor(() => expect(app.api.setPad).toHaveBeenCalledTimes(1));
    expect(app.api.migratePads).toHaveBeenCalledTimes(1);
    expect(
      vi.mocked(app.api.migratePads).mock.invocationCallOrder[0],
    ).toBeLessThan(vi.mocked(app.api.setPad).mock.invocationCallOrder[0]);
    expect(app.api.setPad).toHaveBeenCalledWith({
      key: "xbox-key",
      feedback: { led: false },
    });
    // The list is the folded one: the key the legacy setting gave is stored.
    await waitFor(() =>
      expect(
        sectionOf("Test controller")
          .getByRole("switch", { name: "灯光" })
          .getAttribute("aria-checked"),
      ).toBe("false"),
    );
    expect(chosen(keyGroup(sectionOf("Test controller")))).toBe("back");
    // The next change does not fold again.
    fireEvent.click(
      sectionOf("Test controller").getByRole("switch", { name: "震动" }),
    );
    await waitFor(() => expect(app.api.setPad).toHaveBeenCalledTimes(2));
    expect(app.api.migratePads).toHaveBeenCalledTimes(1);
  });

  it("make going back to the default on a controller work from the folded list", async () => {
    const app = setup({
      pads: legacyPads(),
      migrated: folded(),
      config: { text: legacyConfig, revision: "rev" },
    });
    await openPads();
    // Menu is the default, and the retired functionKey back applies.
    fireEvent.click(
      within(keyGroup(sectionOf("Test controller"))).getByRole("radio", {
        name: "Menu（默认）",
      }),
    );
    await waitFor(() =>
      expect(app.api.setPad).toHaveBeenCalledExactlyOnceWith({
        key: "xbox-key",
        functionKey: null,
      }),
    );
    expect(app.api.migratePads).toHaveBeenCalledTimes(1);
  });

  it("stay in config.json when another setting of Settings is saved", async () => {
    const app = setup({
      pads: legacyPads(),
      config: { text: legacyConfig, revision: "rev" },
    });
    await openSettings();
    const gap = screen.getByRole("spinbutton", { name: "按键间隔" });
    fireEvent.change(gap, { target: { value: "7" } });
    fireEvent.blur(gap);
    await waitFor(() => expect(app.api.putConfig).toHaveBeenCalledTimes(1));
    const written = JSON.parse(
      vi.mocked(app.api.putConfig).mock.calls[0]?.[0] as string,
    );
    expect(written).toEqual({
      schemaVersion: 0,
      functionKey: "back",
      feedback: { rumble: false },
      safety: {
        panic: { standard: { buttons: ["south", "east"], holdMs: 1000 } },
      },
      output: { eventGapMs: 7 },
    });
    expect(app.api.migratePads).not.toHaveBeenCalled();
  });
});

describe("the function key the mapping page edits", () => {
  const profile = {
    schemaVersion: 1,
    name: "Paint",
    layers: { base: {} },
    functionSet: {
      south: {
        label: "重做",
        press: { type: "keys", keys: "Ctrl+Shift+Z", mode: "tap" },
      },
    },
  };

  function mapping(devices: DeviceEntry[], pads: PadsListResult) {
    const app = setup({ devices, pads });
    vi.mocked(app.api.listProfiles).mockResolvedValue([
      { id: "paint", name: "Paint", revision: "r1" },
    ]);
    vi.mocked(app.api.getProfile).mockResolvedValue({
      id: "paint",
      text: JSON.stringify(profile),
      revision: "r1",
      diagnostics: [],
    });
    return app;
  }

  const chip = (control: string) =>
    document.querySelector<HTMLElement>(`[data-callout="${control}"]`);

  it("is ZR on a right Joy-Con: its trigger's chip is the tag, and there is a view of its set", async () => {
    mapping([joyConRight], padsOfDevices([joyConRight]));
    const view = await screen.findByRole("radiogroup", {
      name: "编辑的按键",
    });
    expect(
      within(view)
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["平时", "按住功能键"]);
    await waitFor(() =>
      expect(chip("righttrigger")?.dataset.state).toBe("function"),
    );
    // R is an ordinary key, with nothing set.
    expect(chip("rightshoulder")?.dataset.state).toBe("unbound");
    expect(chip("back")).toBeNull();
  });

  it("follows the setting of the controller: R when the controller has chosen it", async () => {
    const pads = padsFixture([
      padFixture("joyConRight", {
        key: "joycon-key",
        name: "Joy-Con (R)",
        device: 2,
        effective: "rightshoulder",
      }),
    ]);
    mapping([joyConRight], pads);
    await waitFor(() =>
      expect(chip("rightshoulder")?.dataset.state).toBe("function"),
    );
    expect(chip("righttrigger")?.dataset.state).toBe("unbound");
  });

  it("follows the controller that is selected, and changes when another is", async () => {
    const pads = padsFixture([
      ...padsOfDevices([xbox]).pads,
      padFixture("joyConRight", {
        key: "joycon-key",
        name: "Joy-Con (R)",
        device: 2,
      }),
    ]);
    const app = mapping([xbox, joyConRight], pads);
    await waitFor(() => expect(chip("start")?.dataset.state).toBe("function"));
    act(() => app.store.getState().selectDevice(2));
    await waitFor(() =>
      expect(chip("righttrigger")?.dataset.state).toBe("function"),
    );
    act(() => app.store.getState().selectDevice(1));
    await waitFor(() => expect(chip("start")?.dataset.state).toBe("function"));
    expect(chip("righttrigger")?.dataset.state).toBe("unbound");
  });

  it("is named in the list of all keys as the controller prints it", async () => {
    mapping([joyConRight], padsOfDevices([joyConRight]));
    await screen.findByRole("radiogroup", { name: "编辑的按键" });
    fireEvent.click(screen.getByTestId("more-menu"));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "查看所有按键" }),
    );
    const row = await screen.findByTestId("function-key-row");
    expect(row.textContent).toContain("ZR");
    expect(row.textContent).toContain("功能键");
  });

  it("is Menu on a controller of the standard layout whose list has not been read, and View when its setting is", async () => {
    const pads = padsFixture([
      {
        ...padsOfDevices([xbox]).pads[0],
        functionKey: {
          ...padsOfDevices([xbox]).pads[0].functionKey,
          effective: "back",
          stored: "back",
          source: "device",
        },
      },
    ]);
    mapping([xbox], pads);
    await waitFor(() => expect(chip("back")?.dataset.state).toBe("function"));
    expect(chip("start")?.dataset.state).toBe("unbound");
  });
});
