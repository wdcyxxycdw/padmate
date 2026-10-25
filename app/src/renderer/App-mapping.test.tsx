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
  DeviceEntry,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import { App } from "./App";
import { editorAPIFixture } from "./editor-api-fixture";
import { createPadmateStore } from "./store";

// Settings → 手柄调校 (IN-18, IN-19, ADR-0072): a controller SDL does not
// know says so and offers 建立映射…; a known one has 重新映射… in the menu of
// its row; one with a mapping of its own can have the default back, after a
// confirm. The wizard itself is covered in pages/MappingWizardDialog.test.tsx.

const guid = "ff00aabb000000000000000000007600";

const gamepad: DeviceEntry = {
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
};

const joystick: DeviceEntry = {
  info: {
    id: 5,
    name: "Generic USB Joystick",
    type: "unknown",
    vendor: 0x0079,
    product: 0x0006,
    version: 0,
    guid,
    driver: "hidapi",
    path: "",
    serial: "",
    gamepad: false,
    virtual: false,
  },
  key: "03000000790000000600000000007600",
  slot: -1,
  status: "unmapped",
  disabled: false,
};

const snapshot = (devices: DeviceEntry[]): StatusResult => ({
  version: "0.25.0",
  protocol: 1,
  dryRun: true,
  fakeInput: false,
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
      profile: "Paint",
      generation: 1,
      layers: { base: "base", overlays: [], color: "" },
      latches: [],
      mode: 0,
    },
  ],
  devices,
  combos: [
    { device: 1, family: "standard", buttons: ["back", "start"], holdMs: 2000 },
  ],
});

const configWith = (devices?: Record<string, unknown>) => ({
  text: JSON.stringify({ schemaVersion: 0, ...(devices ? { devices } : {}) }),
  revision: "rev",
});

function setup({
  devices = [gamepad],
  config = configWith(),
}: {
  devices?: DeviceEntry[];
  config?: { text: string; revision: string; problem?: string };
} = {}) {
  // The store listens, and so does the wizard while it is open.
  const listeners = new Set<(event: DaemonEvent) => void>();
  const status = snapshot(devices);
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
    getStatus: vi.fn(async () => status),
    getDevices: vi.fn(async () => status.devices),
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
  };
  const store = createPadmateStore(api);
  render(<App store={store} />);
  return {
    api,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => {
        for (const listener of listeners) listener({ topic, data });
      }),
  };
}

async function openSettings() {
  await screen.findByRole("group", { name: /Test controller/ });
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  await screen.findByRole("heading", { name: "手柄调校", level: 2 });
}

const rowOf = (name: string) =>
  screen
    .getByText(name, { selector: ".row-title" })
    .closest(".row") as HTMLElement;

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("a controller that SDL does not know (IN-17, IN-19)", () => {
  it("says so and offers 建立映射…, with nothing to identify or calibrate", async () => {
    setup({ devices: [gamepad, joystick] });
    await openSettings();
    const row = rowOf("Generic USB Joystick");
    expect(within(row).getByText("未识别为手柄")).toBeTruthy();
    const button = within(row).getByRole("button", {
      name: "建立映射 Generic USB Joystick",
    });
    expect(button.textContent).toBe("建立映射…");
    expect(
      [...row.querySelectorAll("button")].map((item) => item.textContent),
    ).toEqual(["建立映射…"]);
    // The known controller keeps its own buttons.
    expect(
      screen.getByRole("button", { name: "调校 Test controller" }),
    ).toBeTruthy();
  });

  it("opens the wizard for it: the capture first, then its raw events by connection ID", async () => {
    const app = setup({ devices: [gamepad, joystick] });
    await openSettings();
    fireEvent.click(
      screen.getByRole("button", { name: "建立映射 Generic USB Joystick" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "建立映射" });
    expect(within(dialog).getByText("Generic USB Joystick")).toBeTruthy();
    await waitFor(() => expect(app.api.rawWatchStart).toHaveBeenCalledOnce());
    expect(app.api.rawWatchStart).toHaveBeenCalledWith("5");
    expect(app.api.padCaptureStart).toHaveBeenCalledOnce();
    // Closing it ends both.
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(app.api.rawWatchStop).toHaveBeenCalledWith("5"));
    expect(app.api.padCaptureStop).toHaveBeenCalled();
    expect(screen.queryByRole("dialog", { name: "建立映射" })).toBeNull();
  });

  it("closes the wizard when the controller goes away", async () => {
    const app = setup({ devices: [gamepad, joystick] });
    await openSettings();
    fireEvent.click(
      screen.getByRole("button", { name: "建立映射 Generic USB Joystick" }),
    );
    await screen.findByRole("dialog", { name: "建立映射" });
    vi.mocked(app.api.getStatus).mockResolvedValue(snapshot([gamepad]));
    app.event("input", { kind: "removed", device: 5 });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "建立映射" })).toBeNull(),
    );
    await waitFor(() => expect(app.api.rawWatchStop).toHaveBeenCalledWith("5"));
  });

  it("cannot be started while settings cannot be saved", async () => {
    setup({
      devices: [gamepad, joystick],
      config: { text: "{", revision: "r", problem: "bad json" },
    });
    await openSettings();
    expect(
      (
        screen.getByRole("button", {
          name: "建立映射 Generic USB Joystick",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
});

describe("a controller SDL knows (IN-18)", () => {
  async function menuOf(name = "Test controller") {
    fireEvent.click(screen.getByRole("button", { name: `更多操作 ${name}` }));
    return screen.findByRole("menu");
  }

  it("has 重新映射… in the menu of its row, and no mapping to restore", async () => {
    setup();
    await openSettings();
    const menu = await menuOf();
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["重新映射…"]);
    expect(screen.queryByText("自定义映射")).toBeNull();
  });

  it("opens the wizard as a remap, by connection ID", async () => {
    const app = setup();
    await openSettings();
    const menu = await menuOf();
    fireEvent.click(within(menu).getByRole("menuitem", { name: "重新映射…" }));
    expect(
      await screen.findByRole("dialog", { name: "重新映射" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(app.api.rawWatchStart).toHaveBeenCalledWith("1"),
    );
  });

  it("marks a controller with a mapping of its own, and restores the default after a confirm", async () => {
    const app = setup({
      config: configWith({
        controller: { mapping: `${guid},*,a:b0,platform:Windows` },
      }),
    });
    await openSettings();
    expect(
      within(rowOf("Test controller")).getByText("自定义映射"),
    ).toBeTruthy();
    const menu = await menuOf();
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["重新映射…", "恢复默认映射"]);
    fireEvent.click(
      within(menu).getByRole("menuitem", { name: "恢复默认映射" }),
    );
    const confirm = await screen.findByRole("dialog", {
      name: "恢复默认映射？",
    });
    // Nothing happens before the confirm.
    expect(app.api.resetMapping).not.toHaveBeenCalled();
    const reads = vi.mocked(app.api.getStatus).mock.calls.length;
    fireEvent.click(within(confirm).getByRole("button", { name: "恢复" }));
    await waitFor(() =>
      expect(app.api.resetMapping).toHaveBeenCalledExactlyOnceWith("1"),
    );
    // The controllers are read again, and the dialog goes.
    await waitFor(() =>
      expect(vi.mocked(app.api.getStatus).mock.calls.length).toBeGreaterThan(
        reads,
      ),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "恢复默认映射？" }),
      ).toBeNull(),
    );
  });

  it("cancels the restore without asking the daemon", async () => {
    const app = setup({
      config: configWith({
        controller: { mapping: `${guid},*,a:b0,platform:Windows` },
      }),
    });
    await openSettings();
    const menu = await menuOf();
    fireEvent.click(
      within(menu).getByRole("menuitem", { name: "恢复默认映射" }),
    );
    const confirm = await screen.findByRole("dialog", {
      name: "恢复默认映射？",
    });
    fireEvent.click(within(confirm).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "恢复默认映射？" })).toBeNull();
    expect(app.api.resetMapping).not.toHaveBeenCalled();
  });

  it("says why when the default cannot be restored, and keeps the dialog", async () => {
    const app = setup({
      config: configWith({
        controller: { mapping: `${guid},*,a:b0,platform:Windows` },
      }),
    });
    vi.mocked(app.api.resetMapping).mockRejectedValueOnce(
      Object.assign(new Error("Mapping not reset"), { code: -32004 }),
    );
    await openSettings();
    const menu = await menuOf();
    fireEvent.click(
      within(menu).getByRole("menuitem", { name: "恢复默认映射" }),
    );
    const confirm = await screen.findByRole("dialog", {
      name: "恢复默认映射？",
    });
    fireEvent.click(within(confirm).getByRole("button", { name: "恢复" }));
    expect(await within(confirm).findByText("手柄已断开。")).toBeTruthy();
    expect(screen.getByRole("dialog", { name: "恢复默认映射？" })).toBeTruthy();
  });

  it("offers 恢复默认映射 for a controller that is still unrecognised after a mapping was saved", async () => {
    const app = setup({
      devices: [gamepad, joystick],
      config: configWith({
        [joystick.key]: { mapping: `${guid},*,a:b0,platform:Windows` },
      }),
    });
    await openSettings();
    const row = rowOf("Generic USB Joystick");
    expect(within(row).getByText("未识别为手柄")).toBeTruthy();
    fireEvent.click(
      within(row).getByRole("button", {
        name: "更多操作 Generic USB Joystick",
      }),
    );
    const menu = await screen.findByRole("menu");
    fireEvent.click(
      within(menu).getByRole("menuitem", { name: "恢复默认映射" }),
    );
    fireEvent.click(
      within(
        await screen.findByRole("dialog", { name: "恢复默认映射？" }),
      ).getByRole("button", { name: "恢复" }),
    );
    await waitFor(() =>
      expect(app.api.resetMapping).toHaveBeenCalledExactlyOnceWith("5"),
    );
  });

  it("speaks English", async () => {
    localStorage.setItem("padmate.language", "en");
    setup({
      devices: [gamepad, joystick],
      config: configWith({
        controller: { mapping: `${guid},*,a:b0,platform:Windows` },
      }),
    });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(
      within(rowOf("Generic USB Joystick")).getByText(
        "Not recognised as a gamepad",
      ),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", {
        name: "Set up mapping for Generic USB Joystick",
      }).textContent,
    ).toBe("Set up mapping…");
    expect(
      within(rowOf("Test controller")).getByText("Custom mapping"),
    ).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "More actions for Test controller" }),
    );
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Remap…", "Restore default mapping"]);
    fireEvent.click(
      within(menu).getByRole("menuitem", { name: "Restore default mapping" }),
    );
    expect(
      await screen.findByRole("dialog", {
        name: "Restore the default mapping?",
      }),
    ).toBeTruthy();
  });
});
