import { describe, expect, it, vi } from "vitest";
import type {
  ConnectionState,
  DaemonEvent,
  DeviceEntry,
  PadmateAPI,
  StatusResult,
} from "../../shared/api";
import type { PadsMigrateResult, PadsSetResult } from "../../shared/pads";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { padFixture, padsFixture } from "../testing/pads-fixture";
import { createPadmateStore } from "./store";

// The store keeps the controller list (pads.list) and makes the changes of the
// Controllers page (ADR-0017): pads.set, with pads.migrate before the first
// one while the retired global members are still in config.json.

const device = (id: number, key: string): DeviceEntry => ({
  info: {
    id,
    name: key,
    type: "xboxone",
    vendor: 0,
    product: 0,
    version: 0,
    guid: "",
    driver: "",
    path: "",
    serial: "",
    gamepad: true,
    virtual: true,
  },
  key,
  slot: id - 1,
  status: "mapped",
  disabled: false,
});

const statusOf = (devices: DeviceEntry[]): StatusResult => ({
  version: "test",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "",
  profileId: "paint",
  slots: [],
  devices,
});

const pad = padFixture("xbox", { key: "a-key", device: 1 });

function bridge(first = padsFixture([pad])) {
  let eventListener = (_event: DaemonEvent) => {};
  let connectionListener = (_connection: ConnectionState) => {};
  let devices = [device(1, "a-key")];
  let revision = "rev";
  const fixture = editorAPIFixture();
  fixture.listPads.mockResolvedValue(first);
  const api = {
    ...fixture,
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
    getStatus: vi.fn(async () => statusOf(devices)),
    getDevices: vi.fn(async () => devices),
    getConfig: vi.fn(async () => ({
      text: '{"schemaVersion":0}',
      revision,
      diagnostics: [],
    })),
    getScriptStatus: vi.fn(async () => []),
  } satisfies Partial<PadmateAPI> & typeof fixture;
  return {
    api,
    event: (topic: string, data: unknown) =>
      eventListener({ topic, data } as DaemonEvent),
    disconnect: () => connectionListener({ phase: "disconnected" }),
    devices: (next: DeviceEntry[]) => {
      devices = next;
    },
    revision: (next: string) => {
      revision = next;
    },
  };
}

const saved = (key: string): PadsSetResult => ({
  revision: "rev-2",
  applied: true,
  pad: padFixture("xbox", { key }),
});

describe("the controller list", () => {
  it("is read when connected, and cleared when the connection goes", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads?.pads).toHaveLength(1));
    // The status and config.json that come with the connection are one burst.
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await Promise.resolve();
    expect(b.api.listPads.mock.calls.length).toBeLessThanOrEqual(2);
    b.disconnect();
    expect(store.getState().pads).toBeNull();
    stop();
  });

  it("is read again when a controller comes or goes, and when config.json changes, but not for each input", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().config).not.toBeNull());
    await new Promise((resolve) => setTimeout(resolve, 0));
    const reads = () => b.api.listPads.mock.calls.length;
    const base = reads();

    // Input does not read it.
    b.event("input", { kind: "down", device: 1, control: "south" });
    b.event("input", { kind: "axis", device: 1, control: "leftx", value: 5 });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(reads()).toBe(base);

    // A controller comes.
    b.devices([device(1, "a-key"), device(2, "b-key")]);
    b.event("input", { kind: "added", device: 2 });
    await vi.waitFor(() => expect(reads()).toBe(base + 1));

    // The same controllers again (a refresh) do not.
    const statusReads = b.api.getStatus.mock.calls.length;
    b.event("dropped", { n: 1 });
    await vi.waitFor(() =>
      expect(b.api.getStatus.mock.calls.length).toBeGreaterThan(statusReads),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(reads()).toBe(base + 1);

    // config.json is written.
    b.revision("rev-2");
    b.event("profile.changed", { kind: "config", revision: "rev-2" });
    await vi.waitFor(() => expect(reads()).toBe(base + 2));

    // And a controller goes.
    b.devices([device(2, "b-key")]);
    b.event("input", { kind: "removed", device: 1 });
    await vi.waitFor(() => expect(reads()).toBe(base + 3));
    stop();
  });

  it("stays as it was when it cannot be read again", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    b.api.listPads.mockRejectedValue(new Error("daemon"));
    await store.getState().refreshPads();
    expect(store.getState().pads?.pads).toHaveLength(1);
    stop();
  });

  it("does not let an older answer replace a newer one", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    let finishOld = (_value: ReturnType<typeof padsFixture>) => {};
    b.api.listPads.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finishOld = resolve;
        }),
    );
    const older = store.getState().refreshPads();
    b.api.listPads.mockResolvedValueOnce(padsFixture([], { revision: "new" }));
    await store.getState().refreshPads();
    finishOld(padsFixture([pad], { revision: "old" }));
    await older;
    expect(store.getState().pads?.revision).toBe("new");
    stop();
  });
});

describe("a change to a controller", () => {
  it("is pads.set with only what changes, and the answer replaces the controller in the list", async () => {
    const b = bridge(
      padsFixture([pad, padFixture("joyConRight", { key: "b-key" })]),
    );
    b.api.setPad.mockResolvedValue({
      ...saved("a-key"),
      pad: padFixture("xbox", {
        key: "a-key",
        device: 1,
        effective: "back",
      }),
    });
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    const outcome = await store
      .getState()
      .setPadSettings("a-key", { functionKey: "back" });
    expect(outcome.ok).toBe(true);
    expect(b.api.setPad).toHaveBeenCalledExactlyOnceWith({
      key: "a-key",
      functionKey: "back",
    });
    expect(b.api.migratePads).not.toHaveBeenCalled();
    const list = store.getState().pads;
    expect(list?.revision).toBe("rev-2");
    expect(list?.pads[0].functionKey.effective).toBe("back");
    expect(list?.pads[1].key).toBe("b-key");
    stop();
  });

  it("asks nothing of the daemon when the controller already has it, or is not known", async () => {
    const b = bridge();
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    expect(
      (await store.getState().setPadSettings("a-key", { functionKey: "start" }))
        .ok,
    ).toBe(true);
    expect(
      (
        await store
          .getState()
          .setPadSettings("a-key", { feedback: { name: "led", on: true } })
      ).ok,
    ).toBe(true);
    expect(
      (await store.getState().setPadSettings("nobody", { functionKey: "back" }))
        .ok,
    ).toBe(false);
    expect(b.api.setPad).not.toHaveBeenCalled();
    expect(b.api.migratePads).not.toHaveBeenCalled();
    stop();
  });

  it("folds the retired members in first, once, and changes the folded list", async () => {
    // The retired functionKey back applies to the controller.
    const legacyPad = padFixture("xbox", { key: "a-key", device: 1 });
    legacyPad.functionKey.effective = "back";
    legacyPad.functionKey.source = "legacy";
    const legacyList = padsFixture([legacyPad], {
      legacy: { functionKey: true },
    });
    const b = bridge(legacyList);
    const foldedPad = padFixture("xbox", {
      key: "a-key",
      device: 1,
      effective: "back",
    });
    b.api.migratePads.mockResolvedValue({
      revision: "rev-1",
      applied: true,
      changed: true,
      legacy: padsFixture([]).legacy,
      pads: [foldedPad],
    } satisfies PadsMigrateResult);
    b.api.setPad.mockResolvedValue({
      revision: "rev-2",
      applied: true,
      pad: foldedPad,
    });
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() =>
      expect(store.getState().pads?.legacy.any).toBe(true),
    );
    // Reading does not fold.
    expect(b.api.migratePads).not.toHaveBeenCalled();
    // Going back to the default is null on the folded controller, which has
    // its own key now.
    const outcome = await store
      .getState()
      .setPadSettings("a-key", { functionKey: "start" });
    expect(outcome.ok).toBe(true);
    expect(b.api.migratePads).toHaveBeenCalledTimes(1);
    expect(b.api.setPad).toHaveBeenCalledExactlyOnceWith({
      key: "a-key",
      functionKey: null,
    });
    expect(b.api.migratePads.mock.invocationCallOrder[0]).toBeLessThan(
      b.api.setPad.mock.invocationCallOrder[0],
    );
    expect(store.getState().pads?.legacy.any).toBe(false);
    stop();
  });

  it("is a failure for the caller (and the shell's alert) when the daemon refuses, and the list stays", async () => {
    const b = bridge();
    b.api.setPad.mockRejectedValue(
      Object.assign(new Error("not a function key of this controller"), {
        code: -32602,
      }),
    );
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    const outcome = await store
      .getState()
      .setPadSettings("a-key", { functionKey: "back" });
    expect(outcome.ok).toBe(false);
    expect(store.getState().error).toContain("not a function key");
    expect(store.getState().pads?.pads[0].functionKey.effective).toBe("start");
    // A caller that shows the failure itself leaves the shell's alert clear.
    store.getState().dismissError();
    await store
      .getState()
      .setPadSettings("a-key", { functionKey: "back" }, { inline: () => true });
    expect(store.getState().error).toBe("");
    stop();
  });

  it("does not keep the migrated list when the answer is for a connection that is gone", async () => {
    const b = bridge(padsFixture([pad], { legacy: { feedback: true } }));
    let finish = (_value: PadsMigrateResult) => {};
    b.api.migratePads.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const store = createPadmateStore(b.api as unknown as PadmateAPI);
    const stop = store.getState().start();
    await vi.waitFor(() => expect(store.getState().pads).not.toBeNull());
    const change = store
      .getState()
      .setPadSettings("a-key", { feedback: { name: "led", on: false } });
    await vi.waitFor(() => expect(b.api.migratePads).toHaveBeenCalled());
    b.disconnect();
    finish({
      revision: "r",
      applied: true,
      changed: true,
      legacy: padsFixture([]).legacy,
      pads: [pad],
    });
    expect((await change).ok).toBe(false);
    expect(b.api.setPad).not.toHaveBeenCalled();
    expect(store.getState().pads).toBeNull();
    stop();
  });
});
