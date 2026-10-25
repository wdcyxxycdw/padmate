import { describe, expect, it, vi } from "vitest";
import type {
  ConnectionState,
  DaemonEvent,
  PadmateAPI,
  StatusResult,
} from "../shared/api";
import { editorAPIFixture } from "./editor-api-fixture";
import { createPadmateStore } from "./store";

// The event list of the diagnostics page (UI-20, ADR-0071): the store keeps a
// ring of rows from engine.activity whatever page is open.

const pad = {
  slot: 0,
  device: 7,
  key: "pad",
  name: "Pad",
  profile: "paint",
  generation: 1,
  layers: { base: "base", overlays: [], color: "" },
  latches: [],
  mode: 0,
};

const status: StatusResult = {
  version: "test",
  protocol: 1,
  dryRun: false,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "none",
  profileId: "paint",
  slots: [pad],
  devices: [],
};

function bridge() {
  let eventListener = (_event: DaemonEvent) => {};
  let connectionListener = (_connection: ConnectionState) => {};
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
    getConfig: vi.fn(async () => ({
      text: '{"schemaVersion":0}',
      revision: "rev",
      diagnostics: [],
    })),
    putConfig: vi.fn(async () => ({
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

async function started() {
  const b = bridge();
  const store = createPadmateStore(b.api);
  const stop = store.getState().start();
  await vi.waitFor(() =>
    expect(store.getState().status?.slots).toHaveLength(1),
  );
  const decide = (decision: Record<string, unknown>, extra = {}) =>
    b.event("engine.activity", {
      kind: "decision",
      slot: 0,
      at: 1_700_000_000_000,
      decision,
      ...extra,
    });
  const settle = async () => {
    await Promise.resolve();
    await Promise.resolve();
  };
  return { b, store, stop, decide, settle };
}

describe("the event list", () => {
  it("keeps decisions and unrouted presses whatever page is open, in the order they came", async () => {
    const { b, store, stop, decide } = await started();
    expect(store.getState().page).toBe("overview");
    decide({
      control: "south",
      edge: "down",
      gesture: "press",
      reason: "unbound",
    });
    decide(
      {
        control: "south",
        edge: "down",
        gesture: "tap",
        action: "tap Ctrl+Z",
        reason: "dispatch",
      },
      { waitedMs: 180 },
    );
    b.event("engine.activity", {
      kind: "unrouted",
      at: 1_700_000_000_500,
      device: 9,
      control: "east",
      down: true,
      why: "ignored",
    });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(3));
    const rows = store.getState().activity;
    expect(rows.map((row) => [row.kind, row.control, row.reason])).toEqual([
      ["decision", "south", "unbound"],
      ["decision", "south", "dispatch"],
      ["unrouted", "east", "unrouted"],
    ]);
    expect(rows[1]).toMatchObject({
      device: 7,
      deviceName: "Pad",
      at: 1_700_000_000_000,
      waitedMs: 180,
      preview: false,
    });
    expect(rows[2]).toMatchObject({ why: "ignored", device: 9 });
    expect(new Set(rows.map((row) => row.seq)).size).toBe(3);
    stop();
  });

  it("puts a batch of events in the ring in one update", async () => {
    const { store, stop, decide } = await started();
    const sizes: number[] = [];
    store.subscribe((state, before) => {
      if (state.activity !== before.activity) sizes.push(state.activity.length);
    });
    for (let index = 0; index < 30; index++)
      decide({ control: "south", reason: "spurious" });
    // The rest of the batch is delivered before the ring is touched.
    expect(store.getState().activity).toHaveLength(0);
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(30));
    expect(sizes).toEqual([30]);
    decide({ control: "south", reason: "spurious" });
    decide({ control: "south", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(32));
    expect(sizes).toEqual([30, 32]);
    stop();
  });

  it("keeps the latest 200 rows", async () => {
    const { store, stop, decide } = await started();
    for (let index = 0; index < 260; index++)
      decide({ control: "south", cycle: index, reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(200));
    const rows = store.getState().activity;
    expect(rows[0].seq).toBe(61);
    expect(rows.at(-1)?.seq).toBe(260);
    stop();
  });

  it("leaves out a release that held nothing and a release of an unrouted pad", async () => {
    const { b, store, stop, decide } = await started();
    decide({ control: "south", edge: "up", reason: "end" });
    b.event("engine.activity", {
      kind: "unrouted",
      device: 9,
      control: "east",
      down: false,
      why: "queued",
    });
    decide({ control: "east", reason: "unbound" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    expect(store.getState().activity[0].reason).toBe("unbound");
    stop();
  });

  it("still sets the key preview of a press that ran an action", async () => {
    const { store, stop, decide } = await started();
    decide({ control: "south", action: "tap Z", reason: "dispatch" });
    expect(store.getState().keyPreview?.seq).toBe(1);
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    stop();
  });

  it("stops keeping rows while recording is paused, and goes on after", async () => {
    const { store, stop, decide, settle } = await started();
    decide({ control: "south", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    store.getState().setActivityPaused(true);
    expect(store.getState().activityPaused).toBe(true);
    decide({ control: "east", reason: "spurious" });
    await settle();
    expect(store.getState().activity).toHaveLength(1);
    store.getState().setActivityPaused(false);
    decide({ control: "north", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(2));
    expect(store.getState().activity.map((row) => row.control)).toEqual([
      "south",
      "north",
    ]);
    stop();
  });

  it("clears the rows on request, and those that have not been put in yet", async () => {
    const { store, stop, decide, settle } = await started();
    decide({ control: "south", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    decide({ control: "east", reason: "spurious" });
    store.getState().clearActivity();
    await settle();
    expect(store.getState().activity).toEqual([]);
    decide({ control: "north", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    expect(store.getState().activity[0].control).toBe("north");
    stop();
  });

  it("clears the rows and the pause when a connection is made again, not when it drops", async () => {
    const { b, store, stop, decide } = await started();
    decide({ control: "south", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    store.getState().setActivityPaused(true);
    b.disconnect();
    // The store keeps the rows until the next connection clears them. No
    // page shows them in between: App puts the connection panel in place of
    // every page (App.test.tsx), so the rows of a lost connection cannot be
    // looked at; what the clear on connecting prevents is that they reappear
    // as the rows of the new connection.
    expect(store.getState().activity).toHaveLength(1);
    b.connect();
    expect(store.getState().activity).toEqual([]);
    expect(store.getState().activityPaused).toBe(false);
    await vi.waitFor(() => expect(store.getState().status).not.toBeNull());
    decide({ control: "east", reason: "spurious" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    stop();
  });

  it("drops rows that were still waiting when the connection dropped", async () => {
    const { b, store, stop, decide, settle } = await started();
    decide({ control: "south", reason: "spurious" });
    b.disconnect();
    b.connect();
    await settle();
    expect(store.getState().activity).toEqual([]);
    stop();
  });

  it("names what paused the mapping at the time of the press", async () => {
    const { b, store, stop, decide } = await started();
    const state = (fields: Record<string, unknown>) =>
      b.event("engine.activity", {
        kind: "state",
        paused: false,
        capturing: false,
        suspended: "none",
        slots: [pad],
        ...fields,
      });
    state({ paused: true });
    decide({ control: "south", reason: "paused" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(1));
    state({ suspended: "lock" });
    decide({ control: "south", reason: "paused" });
    await vi.waitFor(() => expect(store.getState().activity).toHaveLength(2));
    expect(store.getState().activity.map((row) => row.pausedBy)).toEqual([
      "user",
      "system",
    ]);
    stop();
  });
});
