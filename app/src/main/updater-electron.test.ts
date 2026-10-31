import { createHash } from "node:crypto";
import { EventEmitter } from "node:events";
import {
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import Module from "node:module";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { LogRing } from "./log-ring";
import type { UpdaterLike } from "./update-controller";

// updater-electron.ts run for real: electron-updater's own code (the checks,
// the download, the checksum) against a server on this machine, with only the
// "electron" module faked (its app, and net.request as node's http.request).
// This is what the installed application's test mode does, without packaging.

const sha512 = (data: Buffer) =>
  createHash("sha512").update(data).digest("base64");

const installer = Buffer.from("text, not a program\n".repeat(2000));
// What latest.yml says the installer is, and what the server sends for it.
const published = installer;
let body = installer;
// The faked quit of the application: the tests never reach it.
const quitFails = () => {
  throw new Error("the application must not quit");
};
let version = "99.0.0";
let missing = false;
// What app.getVersion() says: the running application's version.
let appVersion = "1.0.0";
const hits: Array<{ path: string; headers: IncomingHttpHeaders }> = [];
// The requests that were not for the server on this machine. None goes out:
// the faked net.request refuses them, so that a test of the real application's
// address never reaches the network.
const strangers: Array<Record<string, unknown>> = [];

/** What net.request returns for an address that is not this machine: it fails at once. */
class RefusedRequest extends EventEmitter {
  write() {}
  end() {
    queueMicrotask(() =>
      this.emit("error", new Error("refused: tests never reach the network")),
    );
  }
  abort() {}
  setHeader() {}
}
let server: Server;
let feed: string;
let work: string;
let userData: string;
const savedLocal = process.env.LOCALAPPDATA;
const savedResources = (process as { resourcesPath?: string }).resourcesPath;
const name = () => `padmate-v${version}-windows-amd64-setup.exe`;

type ModuleWithLoad = typeof Module & {
  _load: (request: string, ...rest: unknown[]) => unknown;
};
const moduleLoad = (Module as ModuleWithLoad)._load;

beforeAll(async () => {
  work = mkdtempSync(join(tmpdir(), "padmate-updater-"));
  userData = join(work, "browser");
  mkdirSync(userData, { recursive: true });
  const resources = join(work, "resources");
  mkdirSync(resources, { recursive: true });
  // What electron-builder bakes in. The adapter takes the provider, the
  // address and the channel from update-config.ts, not from this file, so that
  // what a build was packed with cannot send an installed application
  // somewhere else; the folder of the downloads is what it supplies.
  writeFileSync(
    join(resources, "app-update.yml"),
    "provider: generic\nurl: https://padmate.download/\nchannel: latest\nupdaterCacheDirName: padmate-updater\n",
  );
  (process as { resourcesPath?: string }).resourcesPath = resources;
  const fakeElectron = {
    app: {
      isPackaged: true,
      getVersion: () => appVersion,
      getName: () => "padmate",
      getAppPath: () => work,
      getPath: (key: string) => join(work, key),
      whenReady: () => Promise.resolve(),
      quit: quitFails,
      relaunch: () => {},
      once: () => {},
    },
    net: {
      request: (options: Record<string, unknown>) => {
        const { session: _session, redirect: _redirect, ...rest } = options;
        if (rest.hostname !== "127.0.0.1") {
          strangers.push(rest);
          return new RefusedRequest();
        }
        return require("node:http").request(rest);
      },
    },
    session: { fromPartition: () => ({}) },
    autoUpdater: {
      emit: () => {
        throw new Error("Electron's own autoUpdater is not used");
      },
    },
  };
  (Module as ModuleWithLoad)._load = function (
    this: unknown,
    request: string,
    ...rest: unknown[]
  ) {
    if (request === "electron") return fakeElectron;
    return moduleLoad.call(this, request, ...rest);
  };
  server = createServer((request, response) => {
    const path = new URL(request.url ?? "/", "http://127.0.0.1").pathname;
    hits.push({ path, headers: request.headers });
    if (path === "/latest.yml" && !missing) {
      response.end(
        `version: ${version}\nfiles:\n  - url: ${name()}\n    sha512: ${sha512(published)}\npath: ${name()}\nsha512: ${sha512(published)}\nreleaseDate: '2026-10-24T00:00:00.000Z'\n`,
      );
    } else if (path === `/${name()}`) {
      response.setHeader("content-length", body.length);
      response.end(body);
    } else {
      response.statusCode = 404;
      response.end();
    }
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  feed = `http://127.0.0.1:${(server.address() as AddressInfo).port}/`;
});

afterAll(async () => {
  (Module as ModuleWithLoad)._load = moduleLoad;
  if (savedLocal === undefined) delete process.env.LOCALAPPDATA;
  else process.env.LOCALAPPDATA = savedLocal;
  (process as { resourcesPath?: string }).resourcesPath = savedResources;
  await new Promise<void>((done) => server.close(() => done()));
  rmSync(work, { recursive: true, force: true });
});

async function updater(
  log = new LogRing(),
  options: { packagedTest?: boolean } = {},
): Promise<UpdaterLike> {
  const { createElectronUpdater } = await import("./updater-electron");
  return createElectronUpdater({
    version: "1.0.0",
    packagedTest: options.packagedTest ?? true,
    feed,
    userData,
    log,
  });
}

const pending = () =>
  join(userData, "updater-home", "Local", "padmate-updater", "pending");

describe("the installed application's updater, in its test mode (PKG-10)", () => {
  it("finds a version on the feed and downloads it below the private folder, checksum verified", async () => {
    const real = process.env.LOCALAPPDATA;
    const target = await updater();
    // Downloads go below the private browser data, not to the real folder.
    expect(process.env.LOCALAPPDATA).toBe(
      join(userData, "updater-home", "Local"),
    );
    expect(process.env.LOCALAPPDATA).not.toBe(real);

    await expect(target.check()).resolves.toBe("99.0.0");
    const progress: number[] = [];
    await target.download((percent) => progress.push(percent));
    expect(readdirSync(pending()).sort()).toEqual([name(), "update-info.json"]);
    expect(progress.at(-1)).toBe(100);

    // The download site, as the real application's requests would reach it,
    // sees the address and padmate's name and version; the
    // installation-specific id electron-updater makes is the same for everyone.
    for (const hit of hits) {
      expect(hit.headers["user-agent"]).toBe("padmate/1.0.0");
      expect(hit.headers["x-user-staging-id"]).toBe(
        "00000000-0000-0000-0000-000000000000",
      );
    }
    expect(hits.map((hit) => hit.path)).toEqual(["/latest.yml", `/${name()}`]);
  });

  it("records the install in the test mode and installs nothing", async () => {
    const log = new LogRing();
    const target = await updater(log);
    expect(target.quitAndInstall(true, true)).toEqual({ dryRun: true });
    expect(log.lines().join("\n")).toContain(
      "Recorded quitAndInstall(true, true)",
    );
  });

  it("is not offered the version it has, nor an older one", async () => {
    const target = await updater();
    version = "1.0.0";
    await expect(target.check()).resolves.toBeNull();
    version = "0.9.0";
    await expect(target.check()).resolves.toBeNull();
    version = "99.0.0";
  });

  it("refuses a download whose checksum is not the feed's, and keeps no installer", async () => {
    const target = await updater();
    rmSync(pending(), { recursive: true, force: true });
    const good = body;
    body = Buffer.alloc(installer.length, "X");
    try {
      await target.check();
      await expect(target.download(() => {})).rejects.toMatchObject({
        code: "ERR_CHECKSUM_MISMATCH",
      });
    } finally {
      body = good;
    }
    expect(readdirSync(pending())).toEqual([]);
  });

  it("fails a check when the feed has no latest.yml", async () => {
    const target = await updater();
    missing = true;
    try {
      await expect(target.check()).rejects.toMatchObject({
        code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND",
      });
    } finally {
      missing = false;
    }
  });

  it("looks once, with no argument, whatever version is running", async () => {
    // A release candidate reads the same latest.yml as everybody (release
    // candidates are not published to the download site), and is offered the
    // release when it is higher: one request, not one among the prereleases
    // and one among the releases.
    appVersion = "1.0.3-rc.1";
    hits.length = 0;
    try {
      const target = await updater();
      expect(target.check.length).toBe(0);
      await expect(target.check()).resolves.toBe("99.0.0");
      expect(hits.map((hit) => hit.path)).toEqual(["/latest.yml"]);
      // The release candidate is not offered a candidate that is lower than it.
      version = "1.0.3-rc.0";
      await expect(target.check()).resolves.toBeNull();
      expect(hits.map((hit) => hit.path)).toEqual([
        "/latest.yml",
        "/latest.yml",
      ]);
    } finally {
      appVersion = "1.0.0";
      version = "99.0.0";
    }
  });
});

describe("the installed application's updater, in the real application (PKG-10, ADR-0078)", () => {
  it("asks the download site for latest.yml, and the test feed is not read", async () => {
    // The real application's address, without the network: net.request
    // refuses every request that is not for this machine.
    strangers.length = 0;
    hits.length = 0;
    const target = await updater(new LogRing(), { packagedTest: false });
    await expect(target.check()).rejects.toThrow("refused");
    expect(hits).toEqual([]);
    expect(strangers).toHaveLength(1);
    const [request] = strangers;
    expect(request).toMatchObject({
      protocol: "https:",
      hostname: "padmate.download",
    });
    // The time in the query keeps a cache from answering for the day.
    expect(request.path).toMatch(/^\/latest\.yml\?noCache=[0-9a-v]+$/);
    // Only the address and padmate's name and version go with it.
    expect(request.headers).toMatchObject({
      "User-Agent": "padmate/1.0.0",
      "x-user-staging-id": "00000000-0000-0000-0000-000000000000",
    });
  });
});
