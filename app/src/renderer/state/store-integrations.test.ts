import { describe, expect, it, vi } from "vitest";
import type {
  ConnectionState,
  DaemonEvent,
  PadmateAPI,
  StatusResult,
} from "../../shared/api";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { demoApp, demoFunctions } from "../testing/integrations-fixture";
import { createPadmateStore } from "./store";

// The store's part of the app integrations (ARCHITECTURE §16.4): the list, the
// functions kept per language until the app's files change, and
// integration.changed.

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
  let connectionListener = (_connection: ConnectionState) => {};
  const fixture = editorAPIFixture();
  fixture.listIntegrations.mockResolvedValue([demoApp]);
  fixture.getIntegrationFunctions.mockResolvedValue(demoFunctions);
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
    getStatus: vi.fn(async () => status),
    getDevices: vi.fn(async () => []),
    getConfig: vi.fn(async () => ({
      text: '{"schemaVersion":0}',
      revision: "rev",
      diagnostics: [],
    })),
    getScriptStatus: vi.fn(async () => []),
  };
  const store = createPadmateStore(api as unknown as PadmateAPI);
  const stop = store.getState().start();
  return {
    api,
    store,
    stop,
    event: (topic: string, data: unknown) =>
      eventListener({ topic, data } as DaemonEvent),
    disconnect: () => connectionListener({ phase: "disconnected" }),
  };
}

describe("integrations in the store", () => {
  it("reads the list when connected, and again when the app's state changes", async () => {
    const b = bridge();
    await vi.waitFor(() =>
      expect(b.store.getState().integrations).toEqual([demoApp]),
    );
    b.api.listIntegrations.mockResolvedValue([{ ...demoApp, running: true }]);
    b.event("integration.changed", { integration: "demoapp", kind: "running" });
    await vi.waitFor(() =>
      expect(b.store.getState().integrations?.[0].running).toBe(true),
    );
    expect(b.store.getState().integrationEvent).toMatchObject({
      seq: 1,
      change: { integration: "demoapp", kind: "running" },
    });
    b.stop();
  });

  it("ignores an event of a shape it does not know", async () => {
    const b = bridge();
    await vi.waitFor(() =>
      expect(b.store.getState().integrations).not.toBeNull(),
    );
    b.event("integration.changed", { integration: "Bad", kind: "x" });
    b.event("integration.changed", null);
    expect(b.store.getState().integrationEvent).toBeNull();
    b.stop();
  });

  it("keeps the functions per integration and language until the files change", async () => {
    const b = bridge();
    await vi.waitFor(() =>
      expect(b.store.getState().integrations).not.toBeNull(),
    );
    await b.store.getState().loadIntegrationFunctions("demoapp");
    await b.store.getState().loadIntegrationFunctions("demoapp");
    expect(b.api.getIntegrationFunctions).toHaveBeenCalledTimes(1);
    expect(b.api.getIntegrationFunctions).toHaveBeenCalledWith(
      "demoapp",
      "zh-CN",
    );
    b.store.getState().setLanguage("en");
    await b.store.getState().loadIntegrationFunctions("demoapp");
    expect(b.api.getIntegrationFunctions).toHaveBeenLastCalledWith(
      "demoapp",
      "en",
    );
    expect(Object.keys(b.store.getState().integrationFunctions).sort()).toEqual(
      ["demoapp|en", "demoapp|zh-CN"],
    );
    b.event("integration.changed", { integration: "demoapp", kind: "files" });
    expect(b.store.getState().integrationFunctions).toEqual({});
    await b.store.getState().loadIntegrationFunctions("demoapp");
    expect(b.api.getIntegrationFunctions).toHaveBeenCalledTimes(3);
    b.stop();
  });

  it("drops the items it compared when anything about the app changes", async () => {
    const b = bridge();
    await vi.waitFor(() =>
      expect(b.store.getState().integrations).not.toBeNull(),
    );
    await b.store.getState().loadIntegrationItems("paint", "demoapp");
    expect(b.store.getState().integrationItems["paint|demoapp"]).toBeDefined();
    b.event("integration.changed", {
      integration: "demoapp",
      kind: "differences",
      profile: "paint",
    });
    expect(b.store.getState().integrationItems).toEqual({});
    b.stop();
  });

  it("forgets everything when the connection goes", async () => {
    const b = bridge();
    await vi.waitFor(() =>
      expect(b.store.getState().integrations).not.toBeNull(),
    );
    await b.store.getState().loadIntegrationFunctions("demoapp");
    b.disconnect();
    expect(b.store.getState().integrations).toBeNull();
    expect(b.store.getState().integrationFunctions).toEqual({});
    b.stop();
  });

  it("remembers the differences the user put off, for this session", () => {
    const b = bridge();
    b.store.getState().putOffIntegrationDifferences("paint", "demoapp", "d1");
    expect(b.store.getState().putOffDifferences).toEqual(["paint|demoapp|d1"]);
    b.stop();
  });
});
