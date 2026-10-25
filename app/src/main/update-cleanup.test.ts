import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanStalePending, type PendingIO } from "./update-cleanup";

let root: string;
beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "padmate-pending-"));
});
afterEach(async () => {
  await rm(root, { recursive: true, force: true });
});

const setup = (name: string) => `padmate-v${name}-windows-amd64-setup.exe`;

async function seed(files: string[]): Promise<string> {
  const pending = join(root, "pending");
  await mkdir(pending, { recursive: true });
  for (const file of files) await writeFile(join(pending, file), "x");
  return pending;
}

describe("the update downloaded by an earlier run (PKG-10)", () => {
  it("is deleted once the application has that version", async () => {
    const pending = await seed([setup("1.0.1"), "update-info.json"]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.1" }),
    ).resolves.toBe("removed");
    await expect(readdir(pending)).rejects.toThrow();
    // The folder of the cache itself stays.
    expect(await readdir(root)).toEqual([]);
  });

  it("is deleted when the application is already past it", async () => {
    const pending = await seed([setup("1.0.1"), "update-info.json"]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.2.0" }),
    ).resolves.toBe("removed");
    await expect(readdir(pending)).rejects.toThrow();
  });

  it("is deleted also from a half download", async () => {
    const pending = await seed([`temp-${setup("1.0.1")}`]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.1" }),
    ).resolves.toBe("removed");
    await expect(readdir(pending)).rejects.toThrow();
  });

  it("is deleted when a release candidate got the release", async () => {
    await seed([setup("1.0.0-rc.2")]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.0" }),
    ).resolves.toBe("removed");
  });

  // tests/e2e/updater.spec.ts leaves an installer of an update "installed long
  // ago" and waits for the application to delete it. It cannot name 0.0.1: the
  // builds made for tests are 0.0.0-<label>, which 0.0.1 is newer than. 0.0.0-0
  // is the lowest version there is, so it is old for any build.
  it.each([
    "0.0.0-0",
    "0.0.0-m28b.1",
    "0.0.0-signtest.1",
    "0.14.0",
    "1.0.0-rc.1",
    "1.0.0",
  ])(
    "deletes the lowest version, 0.0.0-0, when the application is %s",
    async (appVersion) => {
      const pending = await seed([setup("0.0.0-0"), "update-info.json"]);
      await expect(
        cleanStalePending({ cacheRoot: root, appVersion }),
      ).resolves.toBe("removed");
      await expect(readdir(pending)).rejects.toThrow();
    },
  );

  it("keeps 0.0.1 for a test build, which is 0.0.0 with a label", async () => {
    await seed([setup("0.0.1")]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "0.0.0-m28b.1" }),
    ).resolves.toBe("none");
  });

  it("stays when it is a newer version than the application", async () => {
    const pending = await seed([setup("1.0.2"), "update-info.json"]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.1" }),
    ).resolves.toBe("none");
    expect((await readdir(pending)).sort()).toEqual(
      [setup("1.0.2"), "update-info.json"].sort(),
    );
  });

  it("stays when any file in it is newer", async () => {
    await seed([setup("1.0.1"), setup("1.0.3")]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.2" }),
    ).resolves.toBe("none");
  });

  it("is left alone when nothing in it is an installer of padmate", async () => {
    const pending = await seed([
      "update-info.json",
      "notes.txt",
      "padmate.exe",
    ]);
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "9.9.9" }),
    ).resolves.toBe("none");
    expect(await readdir(pending)).toHaveLength(3);
  });

  it("is nothing when there is no folder", async () => {
    await expect(
      cleanStalePending({
        cacheRoot: join(root, "missing"),
        appVersion: "1.0.0",
      }),
    ).resolves.toBe("none");
  });

  it("tells when it could not delete, so that the next start tries again", async () => {
    await seed([setup("1.0.0")]);
    const io: PendingIO = {
      readdir,
      rm: vi.fn(async () => {
        throw Object.assign(new Error("busy"), { code: "EBUSY" });
      }),
    };
    await expect(
      cleanStalePending({ cacheRoot: root, appVersion: "1.0.0", io }),
    ).resolves.toBe("failed");
    expect(io.rm).toHaveBeenCalledOnce();
    // Nothing else was touched.
    expect(await readdir(join(root, "pending"))).toEqual([setup("1.0.0")]);
  });

  it("deletes only the pending folder of the cache", async () => {
    await seed([setup("1.0.0")]);
    await writeFile(join(root, "installer.exe"), "the copy of the installer");
    await cleanStalePending({ cacheRoot: root, appVersion: "1.0.0" });
    expect(await readdir(root)).toEqual(["installer.exe"]);
  });
});
