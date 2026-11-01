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
  ConfigPutResult,
  DaemonEvent,
  DeviceBattery,
  DeviceEntry,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import type { JoyCon2Status } from "../shared/joycon2";
import { App } from "./App";
import { createPadmateStore } from "./state/store";
import { editorAPIFixture } from "./testing/editor-api-fixture";

// Joy-Con 2 in the window (ADR-0002): the section of Settings, and the
// controllers themselves, which the daemon lists in devices.list like any
// other gamepad (driver "ble"), so they show in the places every gamepad does.

const joyCon2 = (
  id: number,
  type: "joyconleft" | "joyconright" | "joyconpair",
  name: string,
  battery?: DeviceBattery,
): DeviceEntry => ({
  info: {
    id,
    name,
    type,
    vendor: 0x057e,
    product: type === "joyconleft" ? 0x2067 : 0x2066,
    version: 0,
    guid: "05007e0500006720000062000000",
    driver: "ble",
    path: "ble:C84805FA0101",
    serial: type === "joyconpair" ? "L0001+R0001" : "L0001",
    gamepad: true,
    virtual: false,
    buttonMask: 0x1f,
    axisMask: 0x3,
  },
  key: `05007e05:${id}`,
  slot: id - 0x40000001,
  status: "mapped",
  disabled: false,
  ...(battery ? { battery } : {}),
});

const snapshot = (devices: DeviceEntry[]): StatusResult => ({
  version: "1.1.0",
  protocol: 1,
  dryRun: true,
  fakeInput: false,
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

const known = {
  addr: "C8:48:05:FA:01:01",
  serial: "L0001",
  side: "L",
  connected: true,
} as const;

const joyStatus = (change: Partial<JoyCon2Status> = {}): JoyCon2Status => ({
  available: true,
  autoConnect: true,
  windowOpen: false,
  windowLeftMs: 0,
  known: [known],
  links: [],
  ...change,
});

const configText = (text = '{"schemaVersion":0}') => ({
  text,
  revision: "rev",
});

function setup({
  devices = [],
  joy = joyStatus(),
  config = configText(),
}: {
  devices?: DeviceEntry[];
  joy?: JoyCon2Status;
  config?: { text: string; revision: string };
} = {}) {
  const listeners = new Set<(event: DaemonEvent) => void>();
  let current = snapshot(devices);
  const fixture = editorAPIFixture();
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
    getConfig: vi.fn(async () => config),
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
    getJoyCon2Status: vi.fn(async () => joy),
  };
  const store = createPadmateStore(api);
  render(<App store={store} />);
  return {
    api,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => {
        for (const listener of listeners) listener({ topic, data });
      }),
    /** The daemon's device list changes. */
    setDevices: (next: DeviceEntry[]) => {
      current = snapshot(next);
    },
  };
}

async function openSettings() {
  fireEvent.click(await screen.findByRole("button", { name: "设置" }));
  await screen.findByRole("heading", { name: "手柄调校", level: 2 });
}

const rowOf = (name: string) =>
  screen
    .getByText(name, { selector: ".row-title" })
    .closest(".row") as HTMLElement;
const tags = (row: HTMLElement) =>
  [...row.querySelectorAll(".row-title .tag")].map((tag) => tag.textContent);

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("Settings · Joy-Con 2", () => {
  it("is a section after 手柄调校 and before the timing, with the daemon's controllers", async () => {
    setup();
    await openSettings();
    await screen.findByTestId("joycon2-settings");
    const headings = [...document.querySelectorAll(".section-title")].map(
      (heading) => heading.textContent,
    );
    const at = headings.indexOf("Joy-Con 2");
    expect(at).toBeGreaterThan(headings.indexOf("手柄调校"));
    expect(headings[at - 1]).toBe("手柄调校");
    expect(
      within(screen.getByTestId("joycon2-settings")).getByText("L0001"),
    ).toBeTruthy();
  });

  it("is not there when the daemon has no Joy-Con 2, and asks for no events", async () => {
    const app = setup({ joy: joyStatus({ available: false, known: [] }) });
    await openSettings();
    await waitFor(() => expect(app.api.getJoyCon2Status).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByTestId("joycon2-settings")).toBeNull();
    expect(screen.queryByText("自动连接记住的 Joy-Con 2")).toBeNull();
    expect(app.api.watchJoyCon2).not.toHaveBeenCalled();
  });

  it("asks for the state only on the Settings page, and lets go of the events when it leaves", async () => {
    const app = setup();
    await screen.findByRole("button", { name: "设置" });
    expect(app.api.getJoyCon2Status).not.toHaveBeenCalled();
    await openSettings();
    await screen.findByTestId("joycon2-settings");
    await waitFor(() =>
      expect(app.api.watchJoyCon2).toHaveBeenCalledWith(true),
    );
    fireEvent.click(screen.getByRole("button", { name: "映射" }));
    await waitFor(() =>
      expect(app.api.watchJoyCon2).toHaveBeenLastCalledWith(false),
    );
  });

  it("saves the switch in config.json as joycon2.autoConnect, only that member", async () => {
    const app = setup({
      config: configText(
        '{"schemaVersion":0,"timing":{"holdMs":300},"feedback":{"led":false}}',
      ),
    });
    await openSettings();
    const section = await screen.findByTestId("joycon2-settings");
    const sw = within(section).getByRole("switch", {
      name: "自动连接记住的 Joy-Con 2",
    });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(sw);
    await screen.findByText("已自动保存");
    const put = vi.mocked(app.api.putConfig);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put.mock.calls[0]?.[1]).toBe("rev");
    expect(JSON.parse(put.mock.calls[0]?.[0] as string)).toEqual({
      schemaVersion: 0,
      timing: { holdMs: 300 },
      feedback: { led: false },
      joycon2: { autoConnect: false },
    });
    expect(
      within(section)
        .getByRole("switch", { name: "自动连接记住的 Joy-Con 2" })
        .getAttribute("aria-checked"),
    ).toBe("false");
  });

  it("shows the switch off when the file says false, and removes the member when it goes on", async () => {
    const app = setup({
      config: configText('{"schemaVersion":0,"joycon2":{"autoConnect":false}}'),
    });
    await openSettings();
    const section = await screen.findByTestId("joycon2-settings");
    const sw = within(section).getByRole("switch", {
      name: "自动连接记住的 Joy-Con 2",
    });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(sw);
    await screen.findByText("已自动保存");
    const written = JSON.parse(
      vi.mocked(app.api.putConfig).mock.calls[0]?.[0] as string,
    );
    expect(written).toEqual({ schemaVersion: 0 });
    expect("joycon2" in written).toBe(false);
  });
});

describe("a Joy-Con 2 as a controller", () => {
  it("shows in the row of 手柄调校 like any gamepad: its name and its battery", async () => {
    setup({
      devices: [
        joyCon2(0x40000001, "joyconleft", "Joy-Con 2 (L)", {
          state: "discharging",
          percent: 70,
        }),
      ],
    });
    await openSettings();
    const row = rowOf("Joy-Con 2 (L)");
    expect(tags(row)).toEqual(["电量 70%"]);
    // The buttons of every gamepad.
    expect(
      within(row).getByRole("button", { name: "识别 Joy-Con 2 (L)" }),
    ).toBeTruthy();
    expect(
      within(row).getByRole("button", { name: "调校 Joy-Con 2 (L)" }),
    ).toBeTruthy();
  });

  it("shows the charge of a right one and of a pair, and 充电中 while it charges", async () => {
    setup({
      devices: [
        joyCon2(0x40000002, "joyconright", "Joy-Con 2 (R)", {
          state: "charging",
          percent: 40,
        }),
        joyCon2(0x40000003, "joyconpair", "Joy-Con 2 pair", {
          state: "discharging",
          percent: 90,
        }),
      ],
    });
    await openSettings();
    expect(tags(rowOf("Joy-Con 2 (R)"))).toEqual(["电量 40%", "充电中"]);
    expect(tags(rowOf("Joy-Con 2 pair"))).toEqual(["电量 90%"]);
  });

  it("follows the battery in the state events, as every gamepad does", async () => {
    const app = setup({
      devices: [
        joyCon2(0x40000001, "joyconleft", "Joy-Con 2 (L)", {
          state: "discharging",
          percent: 70,
        }),
      ],
    });
    await openSettings();
    app.event("engine.activity", {
      kind: "state",
      slots: snapshot([]).slots,
      paused: false,
      capturing: false,
      suspended: "none",
      batteries: [
        {
          device: 0x40000001,
          key: "05007e05:1073741825",
          name: "Joy-Con 2 (L)",
          state: "discharging",
          percent: 30,
          low: false,
        },
      ],
    });
    expect(tags(rowOf("Joy-Con 2 (L)"))).toEqual(["电量 30%"]);
  });

  it("is connected while the daemon lists it, and the row goes with the connection", async () => {
    const app = setup({
      devices: [joyCon2(0x40000001, "joyconleft", "Joy-Con 2 (L)")],
    });
    // It is the controller the profile bar of the mapping page names.
    await waitFor(() =>
      expect(document.querySelector(".profile-sub")?.textContent).toBe(
        "Joy-Con 2 (L)",
      ),
    );
    await openSettings();
    expect(rowOf("Joy-Con 2 (L)")).toBeTruthy();
    app.setDevices([]);
    app.event("input", { kind: "removed", device: 0x40000001 });
    await waitFor(() =>
      expect(
        screen.getByText("未连接手柄", { selector: ".row-title" }),
      ).toBeTruthy(),
    );
    expect(
      screen.queryByText("Joy-Con 2 (L)", { selector: ".row-title" }),
    ).toBeNull();
  });

  it("has no 重新映射…: SDL does not read it, so there is no mapping to redo", async () => {
    setup({
      devices: [joyCon2(0x40000001, "joyconleft", "Joy-Con 2 (L)")],
    });
    await openSettings();
    // The row has no menu: Remap was its only entry.
    expect(
      screen.queryByRole("button", { name: "更多操作 Joy-Con 2 (L)" }),
    ).toBeNull();
  });
});
