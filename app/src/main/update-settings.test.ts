import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  defaultUpdateSettings,
  parseUpdateSettings,
  UPDATE_SETTINGS_FILE,
  UpdateSettingsStore,
} from "./update-settings";

let dir: string;
beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "padmate-updates-"));
});
afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

const file = () => join(dir, UPDATE_SETTINGS_FILE);
const stored = async () => JSON.parse(await readFile(file(), "utf8"));

describe("the update settings file (PKG-10)", () => {
  it("is updates.json with the checks on, nothing skipped and no check yet", () => {
    expect(UPDATE_SETTINGS_FILE).toBe("updates.json");
    expect(defaultUpdateSettings).toEqual({
      version: 1,
      autoCheck: true,
      skippedVersion: "",
      lastCheck: 0,
    });
  });

  it("starts from the defaults when there is no file", async () => {
    const store = new UpdateSettingsStore(file());
    await expect(store.load()).resolves.toEqual(defaultUpdateSettings);
    expect(store.current).toEqual(defaultUpdateSettings);
  });

  it("reads what an earlier run wrote", async () => {
    await writeFile(
      file(),
      JSON.stringify({
        version: 1,
        autoCheck: false,
        skippedVersion: "1.0.1",
        lastCheck: 1_700_000_000_000,
      }),
    );
    const store = new UpdateSettingsStore(file());
    await store.load();
    expect(store.current).toEqual({
      version: 1,
      autoCheck: false,
      skippedVersion: "1.0.1",
      lastCheck: 1_700_000_000_000,
    });
  });

  it.each([
    "",
    "not json",
    "[]",
    "null",
    "7",
    '{"version":1,',
    '{"version":2,"autoCheck":false}',
    '"text"',
  ])("falls back to the defaults for %j", (text) => {
    expect(parseUpdateSettings(text)).toEqual(defaultUpdateSettings);
  });

  it("keeps the valid fields and defaults the invalid ones", () => {
    expect(
      parseUpdateSettings(
        JSON.stringify({
          version: 1,
          autoCheck: "yes",
          skippedVersion: "<script>",
          lastCheck: -5,
          extra: 1,
        }),
      ),
    ).toEqual(defaultUpdateSettings);
    expect(
      parseUpdateSettings(
        JSON.stringify({ version: 1, autoCheck: false, lastCheck: "soon" }),
      ),
    ).toEqual({ ...defaultUpdateSettings, autoCheck: false });
    expect(
      parseUpdateSettings(
        JSON.stringify({ version: 1, lastCheck: Number.MAX_SAFE_INTEGER + 2 }),
      ).lastCheck,
    ).toBe(0);
  });

  it("works from a corrupt file and replaces it on the next change", async () => {
    await writeFile(file(), "\u0000\u0000garbage");
    const store = new UpdateSettingsStore(file());
    await store.load();
    expect(store.current).toEqual(defaultUpdateSettings);
    await store.update({ autoCheck: false });
    expect(await stored()).toEqual({
      ...defaultUpdateSettings,
      autoCheck: false,
    });
  });

  it("writes the file whole, through a temporary file that is gone afterwards", async () => {
    const store = new UpdateSettingsStore(file());
    await store.load();
    await store.update({ lastCheck: 42, skippedVersion: "2.0.0" });
    expect(await stored()).toEqual({
      version: 1,
      autoCheck: true,
      skippedVersion: "2.0.0",
      lastCheck: 42,
    });
    expect(await readdir(dir)).toEqual([UPDATE_SETTINGS_FILE]);
  });

  it("creates the folder when it is not there yet", async () => {
    const nested = join(dir, "ui", "deeper", UPDATE_SETTINGS_FILE);
    const store = new UpdateSettingsStore(nested);
    await store.load();
    await store.update({ autoCheck: false });
    expect(JSON.parse(await readFile(nested, "utf8")).autoCheck).toBe(false);
  });

  it("answers current at once and writes changes in the order they came", async () => {
    const store = new UpdateSettingsStore(file());
    await store.load();
    const pending = [
      store.update({ lastCheck: 1 }),
      store.update({ autoCheck: false }),
      store.update({ lastCheck: 3 }),
      store.update({ skippedVersion: "1.2.3" }),
    ];
    expect(store.current).toEqual({
      version: 1,
      autoCheck: false,
      skippedVersion: "1.2.3",
      lastCheck: 3,
    });
    await Promise.all(pending);
    expect(await stored()).toEqual(store.current);
    expect(await readdir(dir)).toEqual([UPDATE_SETTINGS_FILE]);
  });

  it("refuses a value that is not valid, and leaves the file as it was", async () => {
    const store = new UpdateSettingsStore(file());
    await store.load();
    await store.update({ lastCheck: 5 });
    await expect(
      store.update({ skippedVersion: "<b>1.0.0</b>" }),
    ).rejects.toThrow("version");
    await expect(store.update({ lastCheck: -1 })).rejects.toThrow("time");
    expect(store.current.skippedVersion).toBe("");
    expect(store.current.lastCheck).toBe(5);
    expect((await stored()).lastCheck).toBe(5);
  });

  it("goes on after a write that failed", async () => {
    // A folder where the file should be: the rename cannot replace it.
    await rm(dir, { recursive: true, force: true });
    const blocker = join(tmpdir(), `padmate-updates-blocker-${process.pid}`);
    await writeFile(blocker, "a file where the folder should be");
    try {
      const store = new UpdateSettingsStore(
        join(blocker, UPDATE_SETTINGS_FILE),
      );
      await store.load();
      await expect(store.update({ lastCheck: 9 })).rejects.toThrow();
      // The value is kept in memory for this run, and the next write is tried.
      expect(store.current.lastCheck).toBe(9);
      await expect(store.update({ lastCheck: 10 })).rejects.toThrow();
      expect(store.current.lastCheck).toBe(10);
    } finally {
      await rm(blocker, { force: true });
    }
    dir = await mkdtemp(join(tmpdir(), "padmate-updates-"));
  });
});
