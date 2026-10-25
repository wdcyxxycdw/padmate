import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigGetResult,
  ConfigPutResult,
  ConnectionState,
  DaemonEvent,
  DeviceBattery,
  DeviceEntry,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import type { Config } from "../shared/generated/schema";
import { editorAPIFixture } from "./editor-api-fixture";
import {
  bindApp,
  blankProfileText,
  setSwitching,
  unbindApp,
} from "./library-model";
import {
  CAPTURE_TIMEOUT_MS,
  createPadmateStore,
  LOG_LIMIT,
  readBasicSettings,
} from "./store";
import { templateText } from "./templates";

const status: StatusResult = {
  version: "test",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "",
  profileId: "paint",
  slots: [],
  devices: [],
};

function bridge() {
  let eventListener = (_event: DaemonEvent) => {};
  let connectionListener = (_connection: {
    phase: "connected" | "disconnected" | "connecting";
  }) => {};
  const api = {
    ...editorAPIFixture(),
    getConnection: vi.fn(
      async (): Promise<ConnectionState> => ({ phase: "connected" }),
    ),
    onConnection: vi.fn((listener) => {
      connectionListener = listener;
      return vi.fn();
    }),
    onEvent: vi.fn((listener) => {
      eventListener = listener;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => status),
    getDevices: vi.fn(async () => []),
    getScriptStatus: vi.fn(
      async (): ReturnType<PadmateAPI["getScriptStatus"]> => [],
    ),
    getConfig: vi.fn(
      async (): Promise<ConfigGetResult> => ({
        text: '{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}],"output":{"eventGapMs":0}}',
        revision: "rev",
        diagnostics: [],
      }),
    ),
    putConfig: vi.fn(async (_text: string, _revision: string) => ({
      revision: "next",
      applied: false,
      restartRequired: true,
    })),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    reloadScripts: vi.fn(
      async (): ReturnType<PadmateAPI["reloadScripts"]> => [],
    ),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    reconnect: vi.fn(async () => {}),
    setSendToPadmate: vi.fn(async (enabled: boolean) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
  } satisfies PadmateAPI;
  return {
    api,
    event: (topic: string, data: unknown) =>
      eventListener({ topic, data } as DaemonEvent),
    disconnect: () => connectionListener({ phase: "disconnected" }),
    connect: () => connectionListener({ phase: "connected" }),
  };
}

/** The four feedback switches at their default: on (UI-27). */
const allOn = { rumble: true, led: true, pause: true, lowBattery: true };

describe("live renderer state", () => {
  it("keeps input per device and clears held state on removal and disconnect", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    b.event("input", { kind: "down", device: 1, control: "south" });
    b.event("input", {
      kind: "axis",
      device: 2,
      control: "leftx",
      value: 32767,
    });
    expect(store.getState().inputs[1]?.buttons.south).toBe(true);
    expect(store.getState().inputs[2]?.axes.leftx).toBe(32767);
    b.event("input", { kind: "removed", device: 1 });
    expect(store.getState().inputs[1]).toBeUndefined();
    b.disconnect();
    expect(store.getState().inputs).toEqual({});
    expect(store.getState().status).toBeNull();
    stop();
  });

  it("forgets what a controller holds when its mapping changes, and reads the devices again", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    b.event("input", { kind: "down", device: 1, control: "south" });
    b.event("input", { kind: "down", device: 2, control: "north" });
    expect(store.getState().inputs[1]?.buttons.south).toBe(true);
    const reads = b.api.getStatus.mock.calls.length;
    // The held button may be another one now and never send its release.
    b.event("input", { kind: "remapped", device: 1 });
    expect(store.getState().inputs[1]).toBeUndefined();
    expect(store.getState().inputs[2]?.buttons.north).toBe(true);
    await vi.waitFor(() =>
      expect(b.api.getStatus.mock.calls.length).toBeGreaterThan(reads),
    );
    // What it sends afterwards counts again.
    b.event("input", { kind: "down", device: 1, control: "east" });
    expect(store.getState().inputs[1]?.buttons).toEqual({ east: true });
    stop();
  });

  it("reads start at sign-in on connect and forgets it on disconnect", async () => {
    const b = bridge();
    const entry = {
      available: true,
      enabled: false,
      current: false,
      mode: "inject",
    } as const;
    b.api.getStartup.mockResolvedValue(entry);
    b.api.setStartup.mockImplementation(async (enabled: boolean) => ({
      ...entry,
      enabled,
      current: enabled,
    }));
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    await vi.waitFor(() => expect(store.getState().startup).toEqual(entry));
    await expect(store.getState().setStartup(true)).resolves.toBe("");
    expect(store.getState().startup?.enabled).toBe(true);
    expect(b.api.putConfig).not.toHaveBeenCalled();
    b.disconnect();
    expect(store.getState().startup).toBeNull();
    // A daemon without startup.get: null, and no alert.
    b.api.getStartup.mockRejectedValue(new Error("Method not allowed"));
    b.connect();
    await vi.waitFor(() => expect(b.api.getStartup).toHaveBeenCalledTimes(2));
    await store.getState().refreshStartup();
    expect(store.getState().startup).toBeNull();
    expect(store.getState().error).toBe("");
    stop();
  });

  it("identifies a controller by its key, and reports a failure in the shell's alert (IN-13)", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    await expect(store.getState().identifyDevice("pad-key")).resolves.toEqual({
      rumble: true,
      led: false,
      player: false,
    });
    expect(b.api.identifyDevice).toHaveBeenCalledWith("pad-key");
    // It changes nothing, so it does not take the busy state or the alert.
    expect(store.getState().busy).toBe(false);
    expect(store.getState().error).toBe("");

    b.api.identifyDevice.mockRejectedValueOnce({
      message: "Controller not identified",
      code: -32004,
    });
    await expect(
      store.getState().identifyDevice("pad-key"),
    ).resolves.toBeNull();
    expect(store.getState().error).toBe("手柄已断开。");
    store.getState().dismissError();
    b.api.identifyDevice.mockRejectedValueOnce(new Error("unexpected"));
    await expect(
      store.getState().identifyDevice("pad-key"),
    ).resolves.toBeNull();
    expect(store.getState().error).toBe("手柄没有响应，原因见“设置 → 日志”。");

    // Without the daemon there is nothing to ask.
    store.getState().dismissError();
    b.api.identifyDevice.mockClear();
    b.disconnect();
    await expect(
      store.getState().identifyDevice("pad-key"),
    ).resolves.toBeNull();
    expect(b.api.identifyDevice).not.toHaveBeenCalled();
    expect(store.getState().error).toBe("");
    stop();
  });

  it("uses output.preview as the sole source of the output display", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    b.event("engine.activity", {
      kind: "decision",
      decision: { action: { keys: "Ctrl+Z" } },
    });
    expect(store.getState().preview).toBeNull();
    b.event("output.preview", {
      chord: "Ctrl+Z",
      reason: "own-window",
      sequence: 1,
    });
    expect(store.getState().preview?.chord).toBe("Ctrl+Z");
    stop();
  });

  it("keeps the last press that ran an action for the key preview (UI-44)", async () => {
    const b = bridge();
    b.api.getStatus.mockImplementation(async () => ({
      ...status,
      slots: [
        {
          slot: 0,
          device: 7,
          key: "pad",
          name: "Pad",
          profile: "paint",
          generation: 1,
          layers: { base: "base", overlays: [], color: "" },
          latches: [],
          mode: 0,
        },
      ],
    }));
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().status?.slots).toHaveLength(1),
    );
    const decide = (slot: number, decision: Record<string, unknown>) =>
      b.event("engine.activity", { kind: "decision", slot, decision });
    decide(0, {
      control: "south",
      edge: "down",
      label: "撤销",
      action: "tap Ctrl+Z",
      reason: "dispatch",
    });
    expect(store.getState().keyPreview).toEqual({
      seq: 1,
      device: 7,
      control: "south",
      action: "tap Ctrl+Z",
    });
    // Releases, unbound and ignored presses send nothing new.
    decide(0, { control: "south", action: "tap Ctrl+Z", reason: "end" });
    decide(0, { control: "east", reason: "unbound" });
    decide(0, { control: "east", reason: "spurious" });
    expect(store.getState().keyPreview?.seq).toBe(1);
    // A slot the status has not listed yet.
    decide(3, { control: "lt", action: "hold Shift", reason: "dispatch" });
    expect(store.getState().keyPreview).toEqual({
      seq: 2,
      device: null,
      control: "lt",
      action: "hold Shift",
    });
    b.event("dropped", { n: 1 });
    expect(store.getState().keyPreview).toBeNull();
    // The count goes on, so the page sees the next press as new.
    decide(0, { control: "south", action: "tap Z", reason: "dispatch" });
    expect(store.getState().keyPreview?.seq).toBe(3);
    b.disconnect();
    expect(store.getState().keyPreview).toBeNull();
    stop();
  });

  it("bounds logs and does not duplicate repeated records", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    for (let sequence = 1; sequence <= LOG_LIMIT + 9; sequence++) {
      b.event("log", {
        sequence,
        record: { level: "INFO", msg: `message ${sequence}` },
      });
    }
    b.event("log", {
      sequence: LOG_LIMIT + 9,
      record: { level: "INFO", msg: "duplicate" },
    });
    expect(store.getState().logs).toHaveLength(LOG_LIMIT);
    expect(store.getState().logs[0]?.sequence).toBe(10);
    stop();
  });

  it("remembers closed conflict notices until the next session", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    store.getState().dismissConflict("Steam");
    store.getState().dismissConflict("Steam");
    expect(store.getState().dismissedConflicts).toEqual(["Steam"]);
    // Leaving Settings for its logs view and back keeps them closed.
    store.getState().setPage("logs");
    store.getState().setPage("settings");
    b.connect();
    expect(store.getState().dismissedConflicts).toEqual(["Steam"]);
    b.disconnect();
    b.connect();
    expect(store.getState().dismissedConflicts).toEqual([]);
    stop();
  });

  it("clears untrustworthy input after a dropped-event notification", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    b.event("input", { kind: "down", device: 1, control: "south" });
    b.event("dropped", { n: 3 });
    expect(store.getState().inputs).toEqual({});
    expect(store.getState().dropped).toBe(3);
    stop();
  });

  it("saves basic settings with the original revision and preserves other fields", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await store.getState().saveSettings({ sendToOwnWindow: true });
    const [text, revision] = b.api.putConfig.mock.calls[0] ?? [];
    expect(revision).toBe("rev");
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      rules: [{ exe: "paint.exe", profile: "paint" }],
      output: { eventGapMs: 0, sendToOwnWindow: true },
    });
    expect(store.getState().config?.restartRequired).toBe(true);
    stop();
  });

  it("keeps a switching change made elsewhere when a settings save is retried", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    // Someone turned switching off since this window read config.json.
    b.api.putConfig.mockRejectedValueOnce({
      message: "revision conflict",
      code: -32009,
    });
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"enabled":false,"output":{"eventGapMs":0}}',
      revision: "elsewhere",
      diagnostics: [],
    });
    await store.getState().saveSettings({ sendToOwnWindow: true });
    expect(b.api.putConfig).toHaveBeenCalledTimes(2);
    expect(b.api.putConfig.mock.calls[1]?.[1]).toBe("elsewhere");
    expect(JSON.parse(b.api.putConfig.mock.calls[1]?.[0] as string)).toEqual({
      schemaVersion: 0,
      enabled: false,
      output: { eventGapMs: 0, sendToOwnWindow: true },
    });
    expect(store.getState().settingsSaved).toBe(true);
    stop();
  });

  it("never writes restoreLayerState and keeps every other global setting", async () => {
    const b = bridge();
    const kept = {
      schemaVersion: 0,
      restoreLayerState: false,
      timing: { holdMs: 300 },
      safety: {
        panic: { standard: { buttons: ["back", "guide"], holdMs: 3000 } },
      },
      output: { eventGapMs: 0, sendToOwnWindow: true },
    };
    // An older file: the retired defaultProfile goes with the first save.
    const source = { ...kept, defaultProfile: "paint" };
    b.api.getConfig.mockResolvedValueOnce({
      text: JSON.stringify(source),
      revision: "rev",
      diagnostics: [],
    });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    expect(readBasicSettings(store.getState().config)).toEqual({
      sendToOwnWindow: true,
      eventGapMs: 0,
      ...allOn,
    });
    await store.getState().saveSettings({ sendToOwnWindow: false });
    expect(JSON.parse(b.api.putConfig.mock.calls[0]?.[0] as string)).toEqual({
      ...kept,
      output: { eventGapMs: 0, sendToOwnWindow: false },
    });
    // A configuration without the hidden setting does not gain it, and a
    // settings save never writes switching by app.
    await store.getState().saveSettings({ sendToOwnWindow: true });
    const [text, revision] = b.api.putConfig.mock.calls[1] ?? [];
    expect(revision).toBe("next");
    expect(JSON.parse(text as string).restoreLayerState).toBe(false);
    expect(JSON.parse(text as string)).not.toHaveProperty("enabled");
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0}',
      revision: "plain",
      diagnostics: [],
    });
    await store.getState().refresh();
    await store.getState().saveSettings({ sendToOwnWindow: true });
    expect(
      JSON.parse(b.api.putConfig.mock.calls[2]?.[0] as string),
    ).not.toHaveProperty("restoreLayerState");
    stop();
  });

  it("does not write a configuration it could not read", async () => {
    const b = bridge();
    b.api.getConfig.mockResolvedValueOnce({
      text: "{}",
      revision: "rev",
      diagnostics: [],
      problem: "config.json:1:1: unexpected token",
    } as Awaited<ReturnType<typeof b.api.getConfig>>);
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await store.getState().saveSettings({ sendToOwnWindow: true });
    expect(b.api.putConfig).not.toHaveBeenCalled();
    expect(store.getState().settingsSaved).toBe(false);
    stop();
  });

  it("reads basic settings with their defaults", () => {
    expect(readBasicSettings(null)).toEqual({
      sendToOwnWindow: false,
      eventGapMs: 0,
      ...allOn,
    });
    expect(readBasicSettings({ text: "not json", revision: "rev" })).toEqual({
      sendToOwnWindow: false,
      eventGapMs: 0,
      ...allOn,
    });
    expect(
      readBasicSettings({
        text: '{"schemaVersion":0,"enabled":false,"output":{"sendToOwnWindow":true,"eventGapMs":10}}',
        revision: "rev",
      }),
    ).toEqual({ sendToOwnWindow: true, eventGapMs: 10, ...allOn });
    // A value the schema rejects reads as the default.
    for (const gap of ["10", 51, -1, 2.5])
      expect(
        readBasicSettings({
          text: JSON.stringify({
            schemaVersion: 0,
            output: { eventGapMs: gap },
          }),
          revision: "rev",
        }).eventGapMs,
      ).toBe(0);
  });

  it("reads the feedback switches (UI-27): on unless the file says false", () => {
    const read = (feedback: unknown) =>
      readBasicSettings({
        text: JSON.stringify({ schemaVersion: 0, feedback }),
        revision: "rev",
      });
    expect(read(undefined)).toMatchObject(allOn);
    expect(read({})).toMatchObject(allOn);
    expect(read({ rumble: false })).toMatchObject({ ...allOn, rumble: false });
    expect(read({ led: false, lowBattery: false })).toMatchObject({
      ...allOn,
      led: false,
      lowBattery: false,
    });
    expect(read({ pause: true })).toMatchObject(allOn);
    // A value the schema rejects reads as on.
    expect(read({ rumble: "no", led: 0, pause: null })).toMatchObject(allOn);
  });

  it("saves one feedback switch alone, touching nothing else (UI-27)", async () => {
    const b = bridge();
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"enabled":false,"timing":{"holdMs":300}}',
      revision: "rev",
      diagnostics: [],
    });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await store.getState().saveSettings({ rumble: false });
    const [text, revision] = b.api.putConfig.mock.calls[0] ?? [];
    expect(revision).toBe("rev");
    // No output member appears: the page wrote a switch, not the key gap.
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 300 },
      feedback: { rumble: false },
    });
    expect(store.getState().settingsSaved).toBe(true);
    await store.getState().saveSettings({ lowBattery: false, led: false });
    expect(JSON.parse(b.api.putConfig.mock.calls[1]?.[0] as string)).toEqual({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 300 },
      feedback: { rumble: false, led: false, lowBattery: false },
    });
    // On again: the member goes, and the object with its last one.
    await store
      .getState()
      .saveSettings({ rumble: true, led: true, lowBattery: true });
    expect(JSON.parse(b.api.putConfig.mock.calls[2]?.[0] as string)).toEqual({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 300 },
    });
    stop();
  });

  it("saves a feedback switch over what changed elsewhere after a conflict (UI-27)", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    b.api.putConfig.mockRejectedValueOnce({
      message: "revision conflict",
      code: -32009,
    });
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"feedback":{"led":false},"output":{"eventGapMs":5}}',
      revision: "elsewhere",
      diagnostics: [],
    });
    await store.getState().saveSettings({ pause: false });
    expect(b.api.putConfig).toHaveBeenCalledTimes(2);
    expect(b.api.putConfig.mock.calls[1]?.[1]).toBe("elsewhere");
    expect(JSON.parse(b.api.putConfig.mock.calls[1]?.[0] as string)).toEqual({
      schemaVersion: 0,
      feedback: { led: false, pause: false },
      output: { eventGapMs: 5 },
    });
    stop();
  });

  it("saves the key gap alone and keeps the other output fields", async () => {
    const b = bridge();
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"enabled":false,"output":{"sendToOwnWindow":true}}',
      revision: "rev",
      diagnostics: [],
    });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await store.getState().saveSettings({ eventGapMs: 10 });
    const [text, revision] = b.api.putConfig.mock.calls[0] ?? [];
    expect(revision).toBe("rev");
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      enabled: false,
      output: { sendToOwnWindow: true, eventGapMs: 10 },
    });
    // A retry after a conflict writes the gap over what changed elsewhere,
    // and nothing else.
    b.api.putConfig.mockRejectedValueOnce({
      message: "revision conflict",
      code: -32009,
    });
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"output":{"sendToOwnWindow":false,"eventGapMs":10}}',
      revision: "elsewhere",
      diagnostics: [],
    });
    await store.getState().saveSettings({ eventGapMs: 0 });
    expect(JSON.parse(b.api.putConfig.mock.calls[2]?.[0] as string)).toEqual({
      schemaVersion: 0,
      output: { sendToOwnWindow: false, eventGapMs: 0 },
    });
    stop();
  });

  it("does not retry a rejected save or pretend it was applied", async () => {
    const b = bridge();
    b.api.putConfig.mockRejectedValueOnce(
      new Error("config changed outside padmate"),
    );
    const store = createPadmateStore(b.api);
    const stop = await store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await store.getState().saveSettings({ sendToOwnWindow: true });
    expect(b.api.putConfig).toHaveBeenCalledTimes(1);
    expect(store.getState().config?.revision).toBe("rev");
    expect(store.getState().error).toContain("config changed");
    stop();
  });

  it("ignores a pending settings response after reconnecting to a new session", async () => {
    const b = bridge();
    let resolveSave = (_value: ConfigPutResult) => {};
    b.api.putConfig.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSave = resolve;
        }),
    );
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    const saving = store.getState().saveSettings({ sendToOwnWindow: true });
    b.disconnect();
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0}',
      revision: "newsession",
      diagnostics: [],
    });
    b.connect();
    await vi.waitFor(() =>
      expect(store.getState().config?.revision).toBe("newsession"),
    );
    resolveSave({ revision: "new", applied: true, restartRequired: false });
    await saving;
    expect(store.getState().config?.revision).toBe("newsession");
    expect(store.getState().settingsSaved).toBe(false);
    stop();
  });

  it("does not restore an old snapshot after disconnecting", async () => {
    const b = bridge();
    let resolveStatus = (_value: StatusResult) => {};
    b.api.getStatus.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveStatus = resolve;
        }),
    );
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(b.api.getStatus).toHaveBeenCalled());
    b.disconnect();
    resolveStatus(status);
    await Promise.resolve();
    expect(store.getState().status).toBeNull();
    stop();
  });

  it("ignores an old pause response after connecting again", async () => {
    const b = bridge();
    let resolvePause = (_value: { paused: boolean; capturing: boolean }) => {};
    b.api.pause.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePause = resolve;
        }),
    );
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    const pausing = store.getState().togglePause();
    b.disconnect();
    b.connect();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    resolvePause({ paused: true, capturing: false });
    await pausing;
    expect(store.getState().status?.paused).toBe(false);
    stop();
  });

  it("ignores old script reload results after connecting again", async () => {
    const b = bridge();
    type Scripts = Awaited<ReturnType<PadmateAPI["reloadScripts"]>>;
    let resolveReload = (_value: Scripts) => {};
    b.api.reloadScripts.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveReload = resolve;
        }),
    );
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    const reloading = store.getState().reloadScripts();
    b.disconnect();
    b.connect();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    resolveReload([
      {
        slot: 0,
        device: 1,
        key: "old",
        profileId: "old",
        generation: 1,
        script: {
          state: "Running",
          paused: false,
          pending: false,
          wallStopped: false,
          permanent: false,
          restartCount: 0,
          metadata: { runtime: 1 },
        },
      },
    ]);
    await reloading;
    expect(store.getState().scripts).toEqual([]);
    stop();
  });

  it("keeps the battery of a device across the periodic state updates (IN-12)", async () => {
    const b = bridge();
    const device = (id: number, battery?: DeviceBattery): DeviceEntry => ({
      info: {
        id,
        name: `Pad ${id}`,
        type: "standard",
        vendor: 0,
        product: 0,
        version: 0,
        guid: "",
        driver: "",
        path: "",
        serial: "",
        gamepad: true,
        virtual: false,
      },
      key: `key${id}`,
      slot: -1,
      status: "waiting",
      disabled: false,
      ...(battery ? { battery } : {}),
    });
    b.api.getStatus.mockImplementation(async () => ({
      ...status,
      devices: [
        device(1, { state: "discharging", percent: 80 }),
        device(2, { state: "wired" }),
        device(3),
      ],
    }));
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().status?.devices).toHaveLength(3),
    );
    b.event("engine.activity", {
      kind: "state",
      slots: [
        {
          slot: 0,
          device: 1,
          key: "key1",
          name: "Pad 1",
          profile: "paint",
          generation: 1,
          layers: { base: "base", overlays: [], color: "" },
          latches: [],
          mode: 0,
        },
      ],
      paused: false,
      capturing: false,
      suspended: "none",
    });
    const devices = store.getState().status?.devices ?? [];
    expect(devices.map((d) => [d.info.id, d.status, d.battery])).toEqual([
      [1, "mapped", { state: "discharging", percent: 80 }],
      [2, "waiting", { state: "wired" }],
      [3, "waiting", undefined],
    ]);
    stop();
  });

  it("merges the batteries of a state event into the devices, by connection ID (IN-12)", async () => {
    const b = bridge();
    const device = (id: number, battery?: DeviceBattery): DeviceEntry => ({
      info: {
        id,
        name: `Pad ${id}`,
        type: "standard",
        vendor: 0,
        product: 0,
        version: 0,
        guid: "",
        driver: "",
        path: "",
        serial: "",
        gamepad: true,
        virtual: false,
      },
      key: `key${id}`,
      slot: -1,
      status: "waiting",
      disabled: false,
      ...(battery ? { battery } : {}),
    });
    b.api.getStatus.mockImplementation(async () => ({
      ...status,
      devices: [
        device(1, { state: "discharging", percent: 80 }),
        device(2),
        device(3, { state: "wired" }),
        { ...device(4), status: "ignored" },
      ],
    }));
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().status?.devices).toHaveLength(4),
    );
    const state = (batteries?: unknown) => ({
      kind: "state",
      slots: [],
      paused: false,
      capturing: false,
      suspended: "none",
      ...(batteries === undefined ? {} : { batteries }),
    });
    const batteries = () =>
      (store.getState().status?.devices ?? []).map((d) => d.battery);
    // The event lists some: they replace what status.get gave; the percent is
    // left out of the device's when the controller reports none, and a
    // controller the list does not mention keeps its own.
    b.event(
      "engine.activity",
      state([
        {
          device: 1,
          key: "key1",
          name: "Pad 1",
          state: "charging",
          percent: 82,
          low: false,
        },
        {
          device: 2,
          key: "key2",
          name: "Pad 2",
          state: "discharging",
          percent: 17,
          low: true,
        },
        { device: 4, key: "key4", name: "Pad 4", state: "wired", low: false },
      ]),
    );
    expect(batteries()).toEqual([
      { state: "charging", percent: 82 },
      { state: "discharging", percent: 17 },
      { state: "wired" },
      { state: "wired" },
    ]);
    // The ignored controller keeps its status, with the power.
    expect(store.getState().status?.devices[3].status).toBe("ignored");
    // An event without batteries, or with a list that is not one, changes none.
    b.event("engine.activity", state());
    b.event("engine.activity", state("many"));
    expect(batteries()).toEqual([
      { state: "charging", percent: 82 },
      { state: "discharging", percent: 17 },
      { state: "wired" },
      { state: "wired" },
    ]);
    // The reading is a copy: low is not a part of the device's battery.
    expect(store.getState().status?.devices[1].battery).not.toHaveProperty(
      "low",
    );
    stop();
  });

  it("tracks the current foreground output mode in periodic state updates", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    b.event("engine.activity", {
      kind: "state",
      slots: [],
      paused: false,
      capturing: false,
      suspended: "none",
      localDryRun: true,
      sendToOwnWindow: false,
    });
    expect(store.getState().status?.localDryRun).toBe(true);
    b.event("engine.activity", {
      kind: "state",
      slots: [],
      paused: false,
      capturing: false,
      suspended: "none",
      localDryRun: false,
      sendToOwnWindow: true,
    });
    expect(store.getState().status?.localDryRun).toBe(false);
    expect(store.getState().status?.sendToOwnWindow).toBe(true);
    stop();
  });

  it("keeps the initial configuration when an input snapshot refresh overlaps it", async () => {
    const b = bridge();
    type Config = Awaited<ReturnType<typeof b.api.getConfig>>;
    let resolveConfig = (_value: Config) => {};
    b.api.getConfig.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveConfig = resolve;
        }),
    );
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(b.api.getConfig).toHaveBeenCalled());
    b.event("input", { kind: "added", device: 1 });
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    resolveConfig({
      text: '{"schemaVersion":0}',
      revision: "initial",
      diagnostics: [],
    });
    await vi.waitFor(() =>
      expect(store.getState().config?.revision).toBe("initial"),
    );
    stop();
  });
});

function deferred<T>() {
  let resolve = (_value: T) => {};
  let reject = (_error: unknown) => {};
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

const conflict = { message: "revision conflict", code: -32009 };

async function library(prepare?: (b: ReturnType<typeof bridge>) => void) {
  const b = bridge();
  prepare?.(b);
  const store = createPadmateStore(b.api);
  const stop = store.getState().start();
  await vi.waitFor(() => {
    expect(store.getState().config).not.toBeNull();
    expect(store.getState().profilesLoaded).toBe(true);
    expect(store.getState().status).not.toBeNull();
  });
  return { b, store, stop };
}

const startupRefusal = {
  message: "padmate was started with this profile",
  code: -32013,
  data: { references: ["startup"] },
};

interface LibraryDaemon {
  text: string;
  revision: string;
  active: string;
}

/**
 * Makes the bridge keep config.json and the active profile, and refuse
 * deletes as padmated does: the active profile (-32012), the startup profile
 * (-32013 "startup"), then config.json's references (-32013 with pointers).
 */
function libraryDaemon(
  b: ReturnType<typeof bridge>,
  {
    config,
    active,
    startup = "",
  }: { config: Config; active: string; startup?: string },
): LibraryDaemon {
  const daemon = { text: JSON.stringify(config), revision: "rev", active };
  let saves = 0;
  b.api.getConfig.mockImplementation(async () => ({
    text: daemon.text,
    revision: daemon.revision,
    diagnostics: [],
  }));
  b.api.putConfig.mockImplementation(async (text: string, revision: string) => {
    if (revision !== daemon.revision) throw conflict;
    daemon.text = text;
    daemon.revision = `saved-${++saves}`;
    return { revision: daemon.revision, applied: true, restartRequired: false };
  });
  b.api.getStatus.mockImplementation(async () => ({
    ...status,
    profileId: daemon.active,
  }));
  b.api.activateProfile.mockImplementation(async (id: string | null) => {
    daemon.active = id ?? "";
    return { profileId: daemon.active, generation: 2 };
  });
  b.api.deleteProfile.mockImplementation(async (id: string) => {
    if (id === daemon.active)
      throw { message: "profile is active", code: -32012 };
    if (id === startup) throw startupRefusal;
    if (id === "default")
      throw {
        message: "fixed default",
        code: -32018,
        data: { op: "delete" },
      };
    // The retired defaultProfile is no reference (ADR-0053).
    const current: Config = JSON.parse(daemon.text);
    const references = (current.rules ?? []).flatMap((rule, index) =>
      rule.profile === id ? [`rules/${index}`] : [],
    );
    if (references.length)
      throw {
        message: "config.json still selects the profile",
        code: -32013,
        data: { references },
      };
  });
  return daemon;
}

function written(b: ReturnType<typeof bridge>, call = 0): Config {
  return JSON.parse(b.api.putConfig.mock.calls[call]?.[0] as string);
}

describe("profile library state", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads the library on connect and forgets it on disconnect", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([
        { id: "paint", name: "Paint", revision: "p1" },
      ]),
    );
    expect(store.getState().profiles).toEqual([
      { id: "paint", name: "Paint", revision: "p1" },
    ]);
    store.getState().startCapture("paint");
    b.disconnect();
    expect(store.getState()).toMatchObject({
      profiles: [],
      profilesLoaded: false,
      recentApps: [],
      capture: null,
      captured: null,
      libraryBusy: false,
    });
    stop();
  });

  it("reports a session without a library", async () => {
    const { store, stop } = await library((b) =>
      b.api.listProfiles.mockRejectedValue({
        message: "profile library unavailable; start with --data-dir",
        code: -32008,
      }),
    );
    expect(store.getState().profiles).toEqual([]);
    expect(store.getState().profilesError).toContain("library unavailable");
    stop();
  });

  it("autosaves one change with the revision it read and keeps every other member", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.getConfig.mockResolvedValue({
        text: '{"schemaVersion":0,"future":1,"rules":[{"exe":"paint.exe","profile":"paint","device":"any"}],"output":{"eventGapMs":0}}',
        revision: "rev",
        diagnostics: [],
      }),
    );
    const put = deferred<ConfigPutResult>();
    b.api.putConfig.mockReturnValueOnce(put.promise);
    const saving = store
      .getState()
      .updateConfig((config) => bindApp(config, { exe: "krita.exe" }, "paint"));
    await vi.waitFor(() => expect(b.api.putConfig).toHaveBeenCalled());
    expect(store.getState().librarySave.state).toBe("saving");
    expect(store.getState().libraryBusy).toBe(true);
    put.resolve({ revision: "next", applied: true, restartRequired: false });
    expect(await saving).toEqual({ ok: true, value: undefined });
    const [text, revision] = b.api.putConfig.mock.calls[0] ?? [];
    expect(revision).toBe("rev");
    expect(text).toBe(
      `${JSON.stringify(
        {
          schemaVersion: 0,
          future: 1,
          rules: [
            { exe: "paint.exe", profile: "paint", device: "any" },
            { exe: "krita.exe", profile: "paint" },
          ],
          output: { eventGapMs: 0 },
        },
        null,
        2,
      )}\n`,
    );
    expect(store.getState().config).toMatchObject({ text, revision: "next" });
    const save = store.getState().librarySave;
    expect(save.state).toBe("saved");
    expect(store.getState().libraryBusy).toBe(false);
    store.getState().clearLibrarySave(save.seq - 1);
    expect(store.getState().librarySave.state).toBe("saved");
    store.getState().clearLibrarySave(save.seq);
    expect(store.getState().librarySave.state).toBe("idle");
    stop();
  });

  it("does not write a change that changes nothing", async () => {
    const { b, store, stop } = await library();
    const outcome = await store
      .getState()
      .updateConfig((config) => setSwitching(config, true));
    expect(outcome.ok).toBe(true);
    expect(b.api.putConfig).not.toHaveBeenCalled();
    expect(store.getState().librarySave.state).toBe("idle");
    stop();
  });

  it("refuses to write a configuration it could not read", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.getConfig.mockResolvedValue({
        text: "{",
        revision: "rev",
        problem: "config.json:1:2: unexpected end",
      }),
    );
    const outcome = await store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "brokenConfig" },
    });
    expect(b.api.putConfig).not.toHaveBeenCalled();
    stop();
  });

  it("applies the same change again after a revision conflict", async () => {
    const { b, store, stop } = await library();
    b.api.putConfig.mockRejectedValueOnce(conflict);
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"enabled":false,"rules":[{"exe":"paint.exe","profile":"paint"}]}',
      revision: "fresh",
      diagnostics: [],
    });
    const outcome = await store
      .getState()
      .updateConfig((config) => unbindApp(config, "paint.exe", "paint"));
    expect(outcome.ok).toBe(true);
    expect(b.api.putConfig).toHaveBeenCalledTimes(2);
    expect(b.api.putConfig.mock.calls[1]?.[1]).toBe("fresh");
    // The change from elsewhere survives.
    expect(written(b, 1)).toEqual({
      schemaVersion: 0,
      enabled: false,
      rules: [],
    });
    expect(store.getState().config?.revision).toBe("next");
    expect(store.getState().error).toBe("");
    stop();
  });

  it("drops a change that the reloaded file already has", async () => {
    const { b, store, stop } = await library();
    b.api.putConfig.mockRejectedValueOnce(conflict);
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0,"rules":[]}',
      revision: "fresh",
      diagnostics: [],
    });
    const outcome = await store
      .getState()
      .updateConfig((config) => unbindApp(config, "paint.exe", "paint"));
    expect(outcome.ok).toBe(true);
    expect(b.api.putConfig).toHaveBeenCalledTimes(1);
    expect(store.getState().config?.revision).toBe("fresh");
    stop();
  });

  it("gives up after a second conflict and shows the reloaded file", async () => {
    const { b, store, stop } = await library();
    // Electron drops the code of config.put errors; the message remains.
    const relayed = new Error(
      "Error invoking remote method 'padmate:configPut': Error: profile library: revision conflict; read the document again",
    );
    b.api.putConfig.mockRejectedValue(relayed);
    b.api.getConfig
      .mockResolvedValueOnce({
        text: '{"schemaVersion":0}',
        revision: "second",
        diagnostics: [],
      })
      .mockResolvedValueOnce({
        text: '{"schemaVersion":0,"enabled":false}',
        revision: "third",
        diagnostics: [],
      });
    const outcome = await store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "configConflict" },
    });
    expect(b.api.putConfig).toHaveBeenCalledTimes(2);
    expect(store.getState().error).toBe("设置已在别处修改，已重新载入");
    expect(store.getState().config?.revision).toBe("third");
    expect(store.getState().librarySave.state).toBe("failed");
    stop();
  });

  it("reports other write failures without retrying", async () => {
    const { b, store, stop } = await library();
    b.api.putConfig.mockRejectedValueOnce(new Error("access denied"));
    const outcome = await store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    expect(outcome).toMatchObject({ ok: false, failure: { kind: "error" } });
    expect(b.api.putConfig).toHaveBeenCalledTimes(1);
    expect(store.getState().error).toBe("access denied");
    expect(store.getState().config?.revision).toBe("rev");
    stop();
  });

  it("runs library writes one after another on the latest revision", async () => {
    const { b, store, stop } = await library();
    let revision = 0;
    b.api.putConfig.mockImplementation(async () => ({
      revision: `saved-${++revision}`,
      applied: true,
      restartRequired: false,
    }));
    const first = store
      .getState()
      .updateConfig((config) => bindApp(config, { exe: "a.exe" }, "a"));
    const second = store
      .getState()
      .updateConfig((config) => bindApp(config, { exe: "b.exe" }, "b"));
    await Promise.all([first, second]);
    expect(b.api.putConfig.mock.calls.map((call) => call[1])).toEqual([
      "rev",
      "saved-1",
    ]);
    expect(written(b, 1).rules).toEqual([
      { exe: "paint.exe", profile: "paint" },
      { exe: "a.exe", profile: "a" },
      { exe: "b.exe", profile: "b" },
    ]);
    stop();
  });

  it("reads config.json again after outside writes, not after its own", async () => {
    const { b, store, stop } = await library();
    const reads = () => b.api.getConfig.mock.calls.length;
    const before = reads();
    b.event("profile.changed", { kind: "config", revision: "rev" });
    expect(reads()).toBe(before);
    b.event("profile.changed", { kind: "config", revision: "tray" });
    expect(reads()).toBe(before + 1);
    expect(store.getState().profileChanges).toBe(0);
    await vi.waitFor(() =>
      expect(store.getState().config?.revision).toBe("rev"),
    );
    // During a write the notice waits for the write's own result.
    const put = deferred<ConfigPutResult>();
    b.api.putConfig.mockReturnValueOnce(put.promise);
    const saving = store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    await vi.waitFor(() => expect(b.api.putConfig).toHaveBeenCalled());
    b.event("profile.changed", { kind: "config", revision: "mine" });
    expect(reads()).toBe(before + 1);
    put.resolve({ revision: "mine", applied: true, restartRequired: false });
    await saving;
    expect(reads()).toBe(before + 1);
    // A different revision during a write is read once the queue drains.
    const again = deferred<ConfigPutResult>();
    b.api.putConfig.mockReturnValueOnce(again.promise);
    const next = store
      .getState()
      .updateConfig((config) => setSwitching(config, true));
    await vi.waitFor(() => expect(b.api.putConfig).toHaveBeenCalledTimes(2));
    b.event("profile.changed", { kind: "config", revision: "elsewhere" });
    again.resolve({
      revision: "mine-2",
      applied: true,
      restartRequired: false,
    });
    await next;
    expect(reads()).toBe(before + 2);
    stop();
  });

  it("reads the profile list again after profile changes", async () => {
    const { b, store, stop } = await library();
    const lists = () => b.api.listProfiles.mock.calls.length;
    const before = lists();
    b.event("profile.changed", { kind: "saved", id: "paint", revision: "x" });
    b.event("profile.changed", { kind: "deleted", id: "old" });
    expect(lists()).toBe(before + 2);
    expect(store.getState().profileChanges).toBe(2);
    stop();
  });

  it("activates a profile and reads the status again", async () => {
    const { b, store, stop } = await library();
    const statuses = b.api.getStatus.mock.calls.length;
    expect(await store.getState().activateProfile("ink")).toEqual({
      ok: true,
      value: undefined,
    });
    expect(b.api.activateProfile).toHaveBeenCalledWith("ink");
    expect(b.api.getStatus.mock.calls.length).toBeGreaterThan(statuses);
    await store.getState().activateProfile(null);
    expect(b.api.activateProfile).toHaveBeenLastCalledWith(null);
    stop();
  });

  it("creates a blank profile under a fresh ID", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([
        { id: "paint", name: "Old paint" },
      ]),
    );
    const lists = b.api.listProfiles.mock.calls.length;
    // Another client takes paint-2 first.
    b.api.putProfile.mockRejectedValueOnce(conflict);
    const outcome = await store.getState().createProfile("  Paint ");
    expect(outcome).toEqual({ ok: true, value: "paint-3" });
    expect(b.api.putProfile.mock.calls.map((call) => call[0])).toEqual([
      "paint-2",
      "paint-3",
    ]);
    expect(b.api.putProfile.mock.calls[1]).toEqual([
      "paint-3",
      blankProfileText("Paint"),
      "",
    ]);
    expect(b.api.listProfiles.mock.calls.length).toBeGreaterThan(lists);
    expect(store.getState().librarySave.state).toBe("saved");
    stop();
  });

  it("refuses an empty name", async () => {
    const { b, store, stop } = await library();
    const outcome = await store.getState().createProfile("   ");
    expect(outcome.ok).toBe(false);
    expect(store.getState().error).toBe("名称不能为空");
    expect(b.api.putProfile).not.toHaveBeenCalled();
    stop();
  });

  it("creates a profile from a copy and names it", async () => {
    const { b, store, stop } = await library();
    const outcome = await store
      .getState()
      .createProfile("Ink", { copyFrom: "paint" });
    expect(outcome).toEqual({ ok: true, value: "ink" });
    expect(b.api.copyProfile).toHaveBeenCalledWith("paint", "ink");
    expect(b.api.getProfile).toHaveBeenCalledWith("ink");
    const [id, text, revision] = b.api.putProfile.mock.calls[0] ?? [];
    expect([id, revision]).toEqual(["ink", "r1"]);
    expect(JSON.parse(text as string)).toEqual({
      schemaVersion: 0,
      name: "Ink",
      layers: { base: {} },
    });
    stop();
  });

  it("creates a profile from a template in the UI language", async () => {
    const { b, store, stop } = await library();
    const outcome = await store
      .getState()
      .createProfile(" Ink ", { template: "joycon" });
    expect(outcome).toEqual({ ok: true, value: "ink" });
    expect(b.api.putProfile.mock.calls).toEqual([
      ["ink", templateText("joycon", "Ink", "zh-CN"), ""],
    ]);
    // Nothing to rename afterwards.
    expect(b.api.copyProfile).not.toHaveBeenCalled();
    expect(b.api.getProfile).not.toHaveBeenCalled();
    // Not setLanguage, which would remember it for later tests.
    store.setState({ language: "en" });
    await store.getState().createProfile("Pen", { template: "standard" });
    expect(b.api.putProfile.mock.calls[1]).toEqual([
      "pen",
      templateText("standard", "Pen", "en"),
      "",
    ]);
    stop();
  });

  it("copies a profile as a named copy", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([{ id: "paint", name: "Paint" }]),
    );
    b.api.copyProfile.mockRejectedValueOnce({
      message: "exists",
      code: -32014,
    });
    const outcome = await store.getState().copyProfile("paint");
    expect(outcome).toEqual({
      ok: true,
      value: { id: "paint-3", name: "Paint 副本" },
    });
    expect(b.api.copyProfile.mock.calls).toEqual([
      ["paint", "paint-2"],
      ["paint", "paint-3"],
    ]);
    expect(JSON.parse(b.api.putProfile.mock.calls[0]?.[1] as string).name).toBe(
      "Paint 副本",
    );
    stop();
  });

  it("exports a profile under its name in the UI (PRO-20)", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([
        { id: "default", name: "Stored", default: true },
        { id: "paint", name: "Paint" },
      ]),
    );
    expect(await store.getState().exportProfile("paint")).toEqual({
      ok: true,
      value: true,
    });
    expect(await store.getState().exportProfile("default")).toEqual({
      ok: true,
      value: true,
    });
    expect(b.api.exportProfile.mock.calls).toEqual([
      ["paint", "Paint"],
      ["default", "默认"],
    ]);
    b.api.exportProfile.mockResolvedValueOnce({ saved: false });
    expect(await store.getState().exportProfile("paint")).toEqual({
      ok: true,
      value: false,
    });
    expect(store.getState().error).toBe("");
    // Nothing was written: no save pill.
    expect(store.getState().librarySave.state).toBe("idle");
    stop();
  });

  it("explains an export the daemon refuses", async () => {
    const { b, store, stop } = await library();
    // The page names the file, relative to the profile folder.
    b.api.exportProfile.mockRejectedValueOnce({
      message:
        "profile library: paint/macros/abc~1.ts: has a name like a short name",
      code: -32015,
      data: { path: "paint/macros/abc~1.ts" },
    });
    const outcome = await store.getState().exportProfile("paint");
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "notExportable", path: "macros/abc~1.ts" },
    });
    expect(store.getState().error).toBe(
      "配置文件夹中的「macros/abc~1.ts」无法导出",
    );
    // The folder as a whole (too large an archive): no file to name.
    b.api.exportProfile.mockRejectedValueOnce({
      message: "profile library: paint: makes an archive larger than 4 MiB",
      code: -32015,
      data: { path: "paint" },
    });
    expect(await store.getState().exportProfile("paint")).toEqual({
      ok: false,
      failure: {
        kind: "notExportable",
        message: "profile library: paint: makes an archive larger than 4 MiB",
      },
    });
    expect(store.getState().error).toBe("配置文件夹中有无法导出的文件");
    b.api.exportProfile.mockRejectedValueOnce({
      message: "invalid",
      code: -32602,
      data: [],
    });
    expect(await store.getState().exportProfile("paint")).toMatchObject({
      ok: false,
      failure: { kind: "brokenProfile" },
    });
    b.api.exportProfile.mockRejectedValueOnce({
      message: "The file could not be saved; choose another folder",
      data: { reason: "write" },
    });
    await store.getState().exportProfile("paint", { inline: () => true });
    expect(store.getState().error).toBe("配置文件有错误");
    stop();
  });

  it("imports a profile file and reads the list again (PRO-20)", async () => {
    const { b, store, stop } = await library();
    const listed = b.api.listProfiles.mock.calls.length;
    b.api.importProfile.mockResolvedValueOnce({
      exists: true,
      id: "paint",
      name: "Paint",
      token: "t".repeat(32),
    });
    const first = await store.getState().importProfile();
    expect(first).toEqual({
      ok: true,
      value: {
        exists: true,
        id: "paint",
        name: "Paint",
        token: "t".repeat(32),
      },
    });
    expect(b.api.importProfile).toHaveBeenLastCalledWith(undefined);
    b.api.importProfile.mockResolvedValueOnce({
      id: "paint",
      name: "Paint",
      revision: "r9",
      applied: true,
    });
    const answer = { token: "t".repeat(32), replace: "paint" };
    expect(await store.getState().importProfile(answer)).toEqual({
      ok: true,
      value: { id: "paint", name: "Paint", revision: "r9", applied: true },
    });
    expect(b.api.importProfile).toHaveBeenLastCalledWith(answer);
    expect(b.api.listProfiles.mock.calls.length).toBe(listed + 2);
    expect(store.getState().librarySave.state).toBe("saved");
    stop();
  });

  it("explains a profile file the daemon or Main refuses", async () => {
    const { b, store, stop } = await library();
    b.api.importProfile.mockRejectedValueOnce({
      message: "profile archive: format 2",
      code: -32020,
      data: { reason: "version" },
    });
    expect(await store.getState().importProfile()).toEqual({
      ok: false,
      failure: {
        kind: "archive",
        message: "profile archive: format 2",
        reason: "version",
      },
    });
    expect(store.getState().error).toBe(
      "此文件由更新版本的 padmate 导出，请先更新 padmate",
    );
    b.api.importProfile.mockRejectedValueOnce({
      message: "Choose the file again",
      data: { reason: "expired" },
    });
    const outcome = await store.getState().importProfile(
      { token: "t".repeat(32), name: "Paint 2" },
      {
        inline: () => true,
      },
    );
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "file", reason: "expired" },
    });
    // The dialog shows it; the shell keeps the earlier line.
    expect(store.getState().error).toBe(
      "此文件由更新版本的 padmate 导出，请先更新 padmate",
    );
    stop();
  });

  it("renames only the name and retries once after a conflict", async () => {
    const { b, store, stop } = await library();
    const text =
      '{\n    "schemaVersion": 0,\n    "name": "Old",\n    "layers": {"base": {}}\n}\n';
    b.api.getProfile
      .mockResolvedValueOnce({
        id: "paint",
        text,
        revision: "a",
        diagnostics: [],
      })
      .mockResolvedValueOnce({
        id: "paint",
        text,
        revision: "b",
        diagnostics: [],
      });
    b.api.putProfile.mockRejectedValueOnce(conflict);
    const outcome = await store.getState().renameProfile("paint", " New ");
    expect(outcome.ok).toBe(true);
    expect(b.api.putProfile.mock.calls[1]).toEqual([
      "paint",
      text.replace('"Old"', '"New"'),
      "b",
    ]);
    stop();
  });

  it("does not rename a broken profile", async () => {
    const { b, store, stop } = await library();
    b.api.getProfile.mockResolvedValueOnce({
      id: "paint",
      text: "{",
      revision: "a",
      diagnostics: [],
      problem: "profile.json:1:2: unexpected end",
    });
    const outcome = await store.getState().renameProfile("paint", "New");
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "brokenProfile" },
    });
    expect(store.getState().error).toBe("配置文件有错误");
    expect(b.api.putProfile).not.toHaveBeenCalled();
    stop();
  });

  it("deletes the active profile once refused, after switching away and clearing its references", async () => {
    let daemon: LibraryDaemon | undefined;
    const { b, store, stop } = await library((b) => {
      b.api.listProfiles.mockResolvedValue([
        { id: "paint", name: "Paint", revision: "p1" },
        { id: "ink", name: "Ink" },
        { id: "broken", problem: "x" },
        { id: "default", name: "默认", default: true },
      ]);
      daemon = libraryDaemon(b, {
        // An older file: the retired members go with the cleanup.
        config: {
          schemaVersion: 0,
          defaultProfile: "paint",
          onUnmatched: "default",
          rules: [
            { exe: "paint.exe", profile: "paint" },
            { exe: "game.exe", profile: null },
            { exe: "ink.exe", profile: "ink" },
          ],
        } as unknown as Config,
        active: "paint",
      });
    });
    const outcome = await store.getState().deleteProfile("paint");
    expect(outcome.ok).toBe(true);
    // The fixed default takes over, as for any app without a binding.
    expect(b.api.activateProfile.mock.calls).toEqual([["default"]]);
    expect(JSON.parse(daemon?.text ?? "")).toEqual({
      schemaVersion: 0,
      rules: [{ exe: "ink.exe", profile: "ink" }],
    });
    // Refused as active, then as referenced, then deleted.
    expect(b.api.deleteProfile.mock.calls).toEqual([
      ["paint", "p1"],
      ["paint", "p1"],
      ["paint", "p1"],
    ]);
    const order = [
      b.api.deleteProfile.mock.invocationCallOrder[0] ?? 0,
      b.api.activateProfile.mock.invocationCallOrder[0] ?? 0,
      b.api.putConfig.mock.invocationCallOrder[0] ?? 0,
      b.api.deleteProfile.mock.invocationCallOrder[2] ?? 0,
    ];
    expect(order).toEqual([...order].sort((x, y) => x - y));
    expect(store.getState().status?.profileId).toBe("default");
    stop();
  });

  it("never deletes or renames the fixed default (PRO-28)", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([
        { id: "default", name: "默认", default: true },
      ]),
    );
    expect(await store.getState().deleteProfile("default")).toMatchObject({
      ok: false,
      failure: { kind: "fixedDelete" },
    });
    expect(store.getState().error).toBe("无法删除默认配置");
    expect(
      await store.getState().renameProfile("default", "Mine"),
    ).toMatchObject({ ok: false, failure: { kind: "fixedRename" } });
    expect(b.api.deleteProfile).not.toHaveBeenCalled();
    expect(b.api.putProfile).not.toHaveBeenCalled();
    // The daemon's refusal reads the same, should it come.
    b.api.putProfile.mockRejectedValueOnce({
      message: "fixed",
      code: -32018,
      data: { op: "rename" },
    });
    b.api.getProfile.mockResolvedValueOnce({
      id: "copy",
      text: '{"schemaVersion":0,"name":"x"}',
      revision: "r",
      diagnostics: [],
    });
    expect(await store.getState().renameProfile("copy", "y")).toMatchObject({
      ok: false,
      failure: { kind: "fixedRename" },
    });
    stop();
  });

  it("keeps profile names unique (F2)", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([
        { id: "default", name: "默认", default: true },
        { id: "paint", name: "绘画" },
        { id: "ink", name: "Ink" },
      ]),
    );
    expect(await store.getState().createProfile(" 绘画 ")).toMatchObject({
      ok: false,
      failure: { kind: "error", message: "已有同名配置" },
    });
    expect(await store.getState().createProfile("DEFAULT")).toMatchObject({
      ok: false,
    });
    expect(await store.getState().renameProfile("ink", "绘画")).toMatchObject({
      ok: false,
      failure: { message: "已有同名配置" },
    });
    expect(b.api.putProfile).not.toHaveBeenCalled();
    // Copies are numbered past the names in use.
    b.api.listProfiles.mockResolvedValue([
      { id: "default", name: "默认", default: true },
      { id: "paint", name: "绘画" },
      { id: "profile", name: "绘画 副本" },
    ]);
    await store.getState().refreshLibrary();
    const copied = await store.getState().copyProfile("paint");
    expect(copied).toMatchObject({ ok: true, value: { name: "绘画 副本 2" } });
    // The fixed default copies under its shown name.
    const fromDefault = await store.getState().copyProfile("default");
    expect(fromDefault).toMatchObject({
      ok: true,
      value: { name: "默认 副本" },
    });
    stop();
  });

  it("changes nothing when padmate was started with the profile", async () => {
    const config: Config = {
      schemaVersion: 0,
      rules: [
        { exe: "krita.exe", profile: "paint" },
        { exe: "sai.exe", profile: "paint" },
      ],
    };
    let daemon: LibraryDaemon | undefined;
    const { b, store, stop } = await library((b) => {
      b.api.listProfiles.mockResolvedValue([
        { id: "paint", name: "Paint", revision: "p1" },
        { id: "ink", name: "Ink", revision: "i1" },
      ]);
      daemon = libraryDaemon(b, { config, active: "paint", startup: "paint" });
    });
    const outcome = await store.getState().deleteProfile("paint");
    expect(outcome).toMatchObject({ ok: false, failure: { kind: "startup" } });
    expect(store.getState().error).toBe("此配置为启动时指定的配置，无法删除");
    // It switched away to learn that, then back; config.json was never
    // written.
    expect(b.api.activateProfile.mock.calls).toEqual([["ink"], ["paint"]]);
    expect(daemon?.active).toBe("paint");
    expect(store.getState().status?.profileId).toBe("paint");
    expect(b.api.putConfig).not.toHaveBeenCalled();
    expect(JSON.parse(daemon?.text ?? "")).toEqual(config);
    expect(store.getState().librarySave.state).toBe("failed");
    stop();
  });

  it("puts the bindings back when the delete fails after clearing them", async () => {
    const config = {
      schemaVersion: 0,
      future: { kept: true },
      rules: [
        { exe: "a.exe", profile: "old", device: "any" },
        { exe: "b.exe", profile: "ink" },
        { exe: "c.exe", profile: "old", enabled: false },
      ],
    } as Config;
    let daemon: LibraryDaemon | undefined;
    const { b, store, stop } = await library((b) => {
      b.api.listProfiles.mockResolvedValue([
        { id: "old", revision: "o1" },
        { id: "ink", revision: "i1" },
      ]);
      daemon = libraryDaemon(b, { config, active: "ink" });
      // The profile changes on disk between the two requests.
      const refuse = b.api.deleteProfile.getMockImplementation();
      b.api.deleteProfile
        .mockImplementationOnce(refuse ?? (async () => {}))
        .mockImplementationOnce(async () => {
          throw conflict;
        });
    });
    const outcome = await store.getState().deleteProfile("old");
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "profileConflict" },
    });
    expect(b.api.putConfig).toHaveBeenCalledTimes(2);
    expect(written(b, 0).rules).toEqual([{ exe: "b.exe", profile: "ink" }]);
    expect(JSON.parse(daemon?.text ?? "")).toEqual(config);
    expect(Object.keys(JSON.parse(daemon?.text ?? ""))).toEqual(
      Object.keys(config),
    );
    expect(b.api.activateProfile).not.toHaveBeenCalled();
    stop();
  });

  it("does not switch away from a profile file padmated runs by path", async () => {
    const { b, store, stop } = await library((b) => {
      b.api.listProfiles.mockResolvedValue([{ id: "paint", revision: "p1" }]);
      b.api.getStatus.mockResolvedValue({ ...status, profileId: "" });
      b.api.deleteProfile.mockRejectedValue({
        message: "profile is active",
        code: -32012,
      });
    });
    const outcome = await store.getState().deleteProfile("paint");
    expect(outcome).toMatchObject({ ok: false, failure: { kind: "active" } });
    expect(b.api.activateProfile).not.toHaveBeenCalled();
    expect(b.api.putConfig).not.toHaveBeenCalled();
    stop();
  });

  it("stops when only the running rules select the profile", async () => {
    const { b, store, stop } = await library((b) => {
      b.api.listProfiles.mockResolvedValue([{ id: "old", revision: "o1" }]);
      b.api.deleteProfile.mockRejectedValue({
        message: "padmate's running rules still select this profile",
        code: -32013,
        data: { references: ["rules/0"] },
      });
    });
    const outcome = await store.getState().deleteProfile("old");
    expect(outcome).toMatchObject({
      ok: false,
      failure: { kind: "referenced" },
    });
    // config.json has nothing to remove; asked once more, then it stops.
    expect(b.api.deleteProfile).toHaveBeenCalledTimes(2);
    expect(b.api.putConfig).not.toHaveBeenCalled();
    stop();
  });

  it("deletes an inactive, unreferenced profile without writing config.json", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([{ id: "old", revision: "o1" }]),
    );
    expect((await store.getState().deleteProfile("old")).ok).toBe(true);
    expect(b.api.activateProfile).not.toHaveBeenCalled();
    expect(b.api.putConfig).not.toHaveBeenCalled();
    expect(b.api.deleteProfile).toHaveBeenCalledWith("old", "o1");
    stop();
  });

  it("explains a profile padmate was started with", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([{ id: "old", revision: "o1" }]),
    );
    b.api.deleteProfile.mockRejectedValueOnce(startupRefusal);
    const outcome = await store.getState().deleteProfile("old");
    expect(outcome).toMatchObject({ ok: false, failure: { kind: "startup" } });
    expect(store.getState().error).toBe("此配置为启动时指定的配置，无法删除");
    expect(store.getState().librarySave.state).toBe("failed");
    expect(b.api.putConfig).not.toHaveBeenCalled();
    stop();
  });

  it("leaves a failure to the dialog that shows it, while it is open", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.listProfiles.mockResolvedValue([{ id: "old", revision: "o1" }]),
    );
    b.api.deleteProfile.mockRejectedValue(startupRefusal);
    await store.getState().deleteProfile("old", { inline: () => true });
    expect(store.getState().error).toBe("");
    // Closed before the answer came: the shell shows it instead.
    await store.getState().deleteProfile("old", { inline: () => false });
    expect(store.getState().error).toBe("此配置为启动时指定的配置，无法删除");
    store.getState().dismissError();
    b.api.putProfile.mockRejectedValueOnce(new Error("disk full"));
    const created = await store
      .getState()
      .createProfile("Ink", { inline: () => true });
    expect(created).toMatchObject({ ok: false, failure: { kind: "error" } });
    expect(store.getState().error).toBe("");
    b.api.getProfile.mockResolvedValueOnce({
      id: "old",
      text: "{",
      revision: "a",
      diagnostics: [],
      problem: "profile.json:1:2: unexpected end",
    });
    await store.getState().renameProfile("old", "New", { inline: () => true });
    expect(store.getState().error).toBe("");
    stop();
  });

  it("captures the next app that is not padmate", async () => {
    const { b, store, stop } = await library();
    const target = "paint";
    store.getState().startCapture(target);
    expect(store.getState().capture?.target).toEqual(target);
    b.event("app.foreground", {
      exe: "padmate.exe",
      path: "C:\\padmate\\padmate.exe",
      elevated: false,
      own: true,
    });
    expect(store.getState().capture).not.toBeNull();
    expect(store.getState().recentApps).toEqual([]);
    b.event("app.foreground", {
      exe: "Krita.exe",
      path: "C:\\Krita\\Krita.exe",
      elevated: true,
      own: false,
    });
    expect(store.getState().capture).toBeNull();
    expect(store.getState().captured).toEqual({
      target,
      app: {
        exe: "Krita.exe",
        path: "C:\\Krita\\Krita.exe",
        elevated: true,
        own: false,
      },
    });
    store.getState().clearCaptured();
    expect(store.getState().captured).toBeNull();
    // Without a capture the app is only remembered.
    b.event("app.foreground", {
      exe: "obs64.exe",
      path: "C:\\obs\\obs64.exe",
      elevated: false,
      own: false,
    });
    expect(store.getState().captured).toBeNull();
    expect(store.getState().recentApps[0]?.exe).toBe("obs64.exe");
    stop();
  });

  it("keeps Windows' own programs out of the recent apps (UI-46)", async () => {
    const { b, store, stop } = await library();
    const searchHost = {
      exe: "SearchHost.exe",
      path: "C:\\Windows\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe",
      elevated: false,
      own: false,
      system: true,
    };
    b.event("app.foreground", searchHost);
    expect(store.getState().recentApps).toEqual([]);
    b.event("app.foreground", {
      exe: "krita.exe",
      path: "C:\\Krita\\krita.exe",
      elevated: false,
      own: false,
    });
    b.event("app.foreground", searchHost);
    expect(store.getState().recentApps.map((app) => app.exe)).toEqual([
      "krita.exe",
    ]);
    // Apps seen while the list loads are held to the same rule.
    const recent =
      deferred<{ exe: string; path: string; elevated: boolean }[]>();
    b.api.getRecentApps.mockReturnValueOnce(recent.promise);
    const loading = store.getState().refreshRecentApps();
    b.event("app.foreground", searchHost);
    recent.resolve([{ exe: "old.exe", path: "C:\\old.exe", elevated: false }]);
    await loading;
    expect(store.getState().recentApps.map((app) => app.exe)).toEqual([
      "old.exe",
    ]);
    // A window capture skips them too: pressing Win to type a program's
    // name must not bind the profile to the search box, so it waits for
    // the next program.
    store.getState().startCapture("paint");
    b.event("app.foreground", searchHost);
    expect(store.getState().capture).not.toBeNull();
    expect(store.getState().captured).toBeNull();
    b.event("app.foreground", {
      exe: "mspaint.exe",
      path: "C:\\Paint\\mspaint.exe",
      elevated: false,
      own: false,
    });
    expect(store.getState().capture).toBeNull();
    expect(store.getState().captured?.app.exe).toBe("mspaint.exe");
    stop();
  });

  it("ends a capture after the timeout or when canceled", async () => {
    const { b, store, stop } = await library();
    vi.useFakeTimers();
    store.getState().startCapture("paint");
    vi.advanceTimersByTime(CAPTURE_TIMEOUT_MS - 1);
    expect(store.getState().capture).not.toBeNull();
    vi.advanceTimersByTime(1);
    expect(store.getState().capture).toBeNull();
    store.getState().startCapture("ink");
    store.getState().cancelCapture();
    expect(store.getState().capture).toBeNull();
    b.event("app.foreground", {
      exe: "a.exe",
      path: "C:\\a.exe",
      elevated: false,
      own: false,
    });
    expect(store.getState().captured).toBeNull();
    stop();
  });

  it("keeps recent apps newest first without duplicates", async () => {
    const { b, store, stop } = await library((b) =>
      b.api.getRecentApps.mockResolvedValue([
        { exe: "a.exe", path: "C:\\a.exe", elevated: false },
        { exe: "b.exe", path: "C:\\b.exe", elevated: false },
      ]),
    );
    await store.getState().refreshRecentApps();
    b.event("app.foreground", {
      exe: "B.EXE",
      path: "c:\\B.EXE",
      elevated: false,
      own: false,
    });
    expect(store.getState().recentApps.map((app) => app.path)).toEqual([
      "c:\\B.EXE",
      "C:\\a.exe",
    ]);
    for (let n = 0; n < 20; n++)
      b.event("app.foreground", {
        exe: `${n}.exe`,
        path: `C:\\${n}.exe`,
        elevated: false,
        own: false,
      });
    expect(store.getState().recentApps).toHaveLength(12);
    expect(store.getState().recentApps[0]?.exe).toBe("19.exe");
    stop();
  });

  it("keeps apps seen while the recent list loads", async () => {
    const { b, store, stop } = await library();
    const recent =
      deferred<{ exe: string; path: string; elevated: boolean }[]>();
    b.api.getRecentApps.mockReturnValueOnce(recent.promise);
    const loading = store.getState().refreshRecentApps();
    b.event("app.foreground", {
      exe: "new.exe",
      path: "C:\\new.exe",
      elevated: false,
      own: false,
    });
    recent.resolve([{ exe: "old.exe", path: "C:\\old.exe", elevated: false }]);
    await loading;
    expect(store.getState().recentApps.map((app) => app.exe)).toEqual([
      "new.exe",
      "old.exe",
    ]);
    stop();
  });

  it("drops library responses from an earlier connection", async () => {
    const { b, store, stop } = await library();
    const put = deferred<ConfigPutResult>();
    b.api.putConfig.mockReturnValueOnce(put.promise);
    const saving = store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    await vi.waitFor(() => expect(b.api.putConfig).toHaveBeenCalled());
    b.disconnect();
    b.api.getConfig.mockResolvedValueOnce({
      text: '{"schemaVersion":0}',
      revision: "newsession",
      diagnostics: [],
    });
    b.connect();
    await vi.waitFor(() =>
      expect(store.getState().config?.revision).toBe("newsession"),
    );
    expect(store.getState().libraryBusy).toBe(false);
    // A new write does not wait behind the old one.
    const next = await store
      .getState()
      .updateConfig((config) => setSwitching(config, false));
    expect(next.ok).toBe(true);
    expect(b.api.putConfig.mock.calls[1]?.[1]).toBe("newsession");
    put.resolve({ revision: "old", applied: true, restartRequired: false });
    expect(await saving).toMatchObject({
      ok: false,
      failure: { kind: "disconnected" },
    });
    expect(store.getState().config?.revision).toBe("next");
    expect(store.getState().error).toBe("");
    stop();
  });

  it("does not start library work while disconnected", async () => {
    const { b, store, stop } = await library();
    b.disconnect();
    expect(await store.getState().activateProfile("paint")).toMatchObject({
      ok: false,
      failure: { kind: "disconnected" },
    });
    store.getState().startCapture("paint");
    expect(store.getState().capture).toBeNull();
    expect(b.api.activateProfile).not.toHaveBeenCalled();
    stop();
  });
});

describe("a repaired config.json (PRO-29)", () => {
  const repair = {
    backup: "config.broken-20261015-191600.json",
    path: "C:dataconfig.broken-20261015-191600.json",
    reset: false,
  };

  it("takes the repair from status.get once connected", async () => {
    const b = bridge();
    b.api.getStatus.mockResolvedValue({ ...status, configRepair: repair });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().configRepair).toEqual(repair),
    );
    stop();
  });

  it("has nothing to show when the daemon reports no repair", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    expect(store.getState().configRepair).toBeNull();
    stop();
  });

  it("tells the daemon once the user confirmed, and never shows that repair again", async () => {
    const b = bridge();
    b.api.getStatus.mockResolvedValue({ ...status, configRepair: repair });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().configRepair).toEqual(repair),
    );
    await store.getState().acknowledgeConfigRepair();
    expect(b.api.markConfigRepairSeen).toHaveBeenCalledWith(repair.backup);
    expect(store.getState().configRepair).toBeNull();
    // A status read that still carries it (the daemon has not answered, or
    // another window has not cleared it) does not bring the dialog back.
    await store.getState().refresh();
    expect(store.getState().configRepair).toBeNull();
    // Nothing to confirm: no second call.
    await store.getState().acknowledgeConfigRepair();
    expect(b.api.markConfigRepairSeen).toHaveBeenCalledOnce();
    stop();
  });

  it("closes the dialog even when the daemon cannot be told", async () => {
    const b = bridge();
    b.api.getStatus.mockResolvedValue({ ...status, configRepair: repair });
    b.api.markConfigRepairSeen.mockRejectedValue(new Error("disconnected"));
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().configRepair).toEqual(repair),
    );
    await store.getState().acknowledgeConfigRepair();
    expect(store.getState().configRepair).toBeNull();
    expect(store.getState().error).toBe("");
    stop();
  });

  it("shows a repair that happens while running, and reads config.json again", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    const reads = b.api.getConfig.mock.calls.length;
    const changes = store.getState().profileChanges;
    b.api.getConfig.mockResolvedValue({
      text: '{"schemaVersion":0}',
      revision: "repaired",
      diagnostics: [],
    });
    b.event("profile.changed", {
      kind: "configRepaired",
      ...repair,
      revision: "repaired",
    });
    expect(store.getState().configRepair).toEqual(repair);
    await vi.waitFor(() =>
      expect(store.getState().config?.revision).toBe("repaired"),
    );
    expect(b.api.getConfig.mock.calls.length).toBeGreaterThan(reads);
    // It is no change of a profile.
    expect(store.getState().profileChanges).toBe(changes);
    stop();
  });

  it.each([
    { kind: "configRepaired" },
    { kind: "configRepaired", backup: "", path: "x", reset: false },
    { kind: "configRepaired", backup: 5, path: "x", reset: false },
    { kind: "configRepaired", backup: "a.json", path: "x", reset: "no" },
  ])("ignores a repair event that is not one: %j", async (data) => {
    const b = bridge();
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    b.event("profile.changed", data);
    expect(store.getState().configRepair).toBeNull();
    stop();
  });

  it("forgets the repair when the connection ends", async () => {
    const b = bridge();
    b.api.getStatus.mockResolvedValue({ ...status, configRepair: repair });
    const store = createPadmateStore(b.api);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().configRepair).toEqual(repair),
    );
    b.disconnect();
    expect(store.getState().configRepair).toBeNull();
    stop();
  });
});
