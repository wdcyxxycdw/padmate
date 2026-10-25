import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdir,
  mkdtemp,
  readdir,
  readFile,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { promisify } from "node:util";
import {
  type ElectronApplication,
  _electron as electron,
  expect,
  type Page,
  test,
} from "@playwright/test";
import { closeApplication, placeWindow } from "./helpers";

const run = promisify(execFile);

// PKG-10 (ADR-0073): the automatic update of the installed application, in its
// test mode, against a feed on this machine. PADMATE_TEST_PACKAGED_APP names an
// installed or unpacked padmate.exe, as for packaged.spec.ts; without it the
// tests are skipped. The application keeps to private folders, starts its
// bundled daemon with --dry-run, downloads below its private browser data
// (updater-home\Local\padmate-updater, not the real %LOCALAPPDATA%), and
// records the install instead of running it: nothing is installed, no key is
// sent, and nothing leaves this machine. The feed serves a latest.yml and a
// stand-in for the installer that is text, not a program. Run it against an
// unsigned build: in a signed one app-update.yml names a publisher, and the
// updater rightly refuses a stand-in that nobody signed.
//
// It works for any version of the build, a release or a test build such as
// 0.0.0-m28b.1: what depends on the version is derived from it. A version with
// a prerelease part looks twice (among the prereleases, then at the releases),
// so the feed is asked for latest.yml twice (looks()), and the installer of an
// update "installed long ago" is named 0.0.0-0, the lowest version there is.
const packagedApp = process.env.PADMATE_TEST_PACKAGED_APP;
const skipReason =
  "set PADMATE_TEST_PACKAGED_APP to an installed or unpacked padmate.exe";

const SHARED_STAGING_ID = "00000000-0000-0000-0000-000000000000";

interface Hit {
  method: string;
  path: string;
  headers: IncomingHttpHeaders;
}

const sha512 = (data: Buffer) =>
  createHash("sha512").update(data).digest("base64");

/** The installer of an update that was installed long ago, older than any build. */
const OLD_INSTALLER = "padmate-v0.0.0-0-windows-amd64-setup.exe";

/**
 * The requests of a check made by the application of this version, plus what
 * follows them: a version with a prerelease part looks twice (UpdateController.find).
 */
const looks = (version: string, ...then: string[]): string[] => [
  ...Array<string>(version.includes("-") ? 2 : 1).fill("GET /latest.yml"),
  ...then,
];

/** latest.yml and the installer of a release, served on a loopback port. */
class Feed {
  version = "99.0.0";
  /** What latest.yml describes. */
  readonly installer = Buffer.from(
    "This is text, not a program: the stand-in for padmate's installer.\n".repeat(
      3000,
    ),
  );
  /** What the installer's address serves; another body is a tampered one. */
  body = this.installer;
  /** "missing": latest.yml answers 404. */
  mode: "ok" | "missing" = "ok";
  readonly hits: Hit[] = [];
  private constructor(
    private readonly server: Server,
    readonly url: string,
  ) {}

  get fileName(): string {
    return `padmate-v${this.version}-windows-amd64-setup.exe`;
  }

  static async start(): Promise<Feed> {
    let feed: Feed | undefined;
    const server = createServer((request, response) => {
      const path = new URL(request.url ?? "/", "http://127.0.0.1").pathname;
      feed?.hits.push({
        method: request.method ?? "",
        path,
        headers: request.headers,
      });
      if (!feed || request.method !== "GET") {
        response.statusCode = 405;
        response.end();
      } else if (path === "/latest.yml" && feed.mode === "ok") {
        response.setHeader("content-type", "text/yaml");
        response.end(
          [
            `version: ${feed.version}`,
            "files:",
            `  - url: ${feed.fileName}`,
            `    sha512: ${sha512(feed.installer)}`,
            `path: ${feed.fileName}`,
            `sha512: ${sha512(feed.installer)}`,
            "releaseDate: '2026-10-24T00:00:00.000Z'",
            "",
          ].join("\n"),
        );
      } else if (path === `/${feed.fileName}`) {
        response.setHeader("content-type", "application/octet-stream");
        response.setHeader("content-length", feed.body.length);
        response.end(feed.body);
      } else {
        response.statusCode = 404;
        response.end("not found");
      }
    });
    await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
    const { port } = server.address() as AddressInfo;
    feed = new Feed(server, `http://127.0.0.1:${port}/`);
    return feed;
  }

  /** The files the application asked for, query left out. */
  paths(): string[] {
    return this.hits.map((hit) => `${hit.method} ${hit.path}`);
  }

  async stop(): Promise<void> {
    await new Promise<void>((done) => this.server.close(() => done()));
  }
}

interface Started {
  app: ElectronApplication;
  page: Page;
  root: string;
  browser: string;
  ipc: string;
  version: string;
}

/** The application started in its test mode, with the feed and its settings. */
async function start(
  exe: string,
  feed: Feed,
  options: {
    settings?: Record<string, unknown>;
    before?: (browser: string) => Promise<void>;
  } = {},
): Promise<Started> {
  const root = await mkdtemp(join(tmpdir(), "padmate updater 更新-"));
  const data = join(root, "data");
  const ipc = join(root, "run");
  const browser = join(root, "browser");
  await mkdir(join(data, "profiles", "paint"), { recursive: true });
  await mkdir(ipc, { recursive: true });
  await mkdir(browser, { recursive: true });
  await writeFile(
    join(data, "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "Updater paint",
      layers: {
        base: {
          south: {
            label: "撤销",
            press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
          },
        },
      },
    }),
  );
  await writeFile(
    join(data, "state.json"),
    JSON.stringify({ schemaVersion: 0, lastProfile: "paint" }),
  );
  await writeFile(
    join(data, "config.json"),
    JSON.stringify({ schemaVersion: 0, enabled: false }),
  );
  // The test decides when the application looks: by default no automatic check.
  await writeFile(
    join(browser, "updates.json"),
    JSON.stringify({
      version: 1,
      autoCheck: false,
      skippedVersion: "",
      lastCheck: 0,
      ...options.settings,
    }),
  );
  await options.before?.(browser);
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env))
    if (value !== undefined && !key.startsWith("PADMATE_")) env[key] = value;
  delete env.ELECTRON_RUN_AS_NODE;
  Object.assign(env, {
    PADMATE_UI_TEST_PACKAGED: "1",
    PADMATE_UI_TEST_FAKE_INPUT: "1",
    PADMATE_UI_DATA_DIR: data,
    PADMATE_UI_IPC_DIR: ipc,
    PADMATE_UI_USER_DATA: browser,
    PADMATE_UI_TEST_UPDATE_URL: feed.url,
  });
  if (process.env.PADMATE_E2E_DISPLAY)
    env.PADMATE_E2E_DISPLAY = process.env.PADMATE_E2E_DISPLAY;
  const app = await electron.launch({
    executablePath: exe,
    args: [],
    env,
    timeout: 30000,
  });
  const page = await app.firstWindow();
  await placeWindow(app);
  page.on("pageerror", (error) =>
    console.error("Renderer error:", error.message),
  );
  await expect
    .poll(
      () =>
        page.evaluate(async () =>
          typeof window.padmate === "undefined"
            ? "loading"
            : (await window.padmate.getConnection()).phase,
        ),
      { timeout: 45000 },
    )
    .toBe("connected");
  await app.evaluate(({ BrowserWindow }) => {
    const window = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() !== "padmate overlay",
    );
    window?.show();
    window?.focus();
  });
  const version = await app.evaluate(({ app }) => app.getVersion());
  return { app, page, root, browser, ipc, version };
}

/** The pid the discovery file of dir names, or 0. */
async function discoveredPid(dir: string): Promise<number> {
  try {
    const value = JSON.parse(await readFile(join(dir, "daemon.json"), "utf8"));
    return typeof value.pid === "number" ? value.pid : 0;
  } catch {
    return 0;
  }
}

async function imageOf(pid: number): Promise<string> {
  try {
    const { stdout } = await run(
      "powershell.exe",
      [
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        `(Get-Process -Id ${pid} -ErrorAction Stop).Path`,
      ],
      { windowsHide: true },
    );
    return stdout.trim();
  } catch {
    return "";
  }
}

/** Closes the application, ends the daemon it started, removes the folders. */
async function finish(exe: string, started: Started | undefined) {
  if (!started) return;
  await closeApplication(started.app).catch(() => {});
  const bundledDaemon = join(dirname(exe), "resources", "bin", "padmated.exe");
  const pid = await discoveredPid(started.ipc);
  // Only the bundled daemon is ended: a pid that another program has taken
  // (a stale file) is left alone.
  if (pid && (await imageOf(pid)).toLowerCase() === bundledDaemon.toLowerCase())
    process.kill(pid);
  await rm(started.root, {
    recursive: true,
    force: true,
    maxRetries: 10,
    retryDelay: 200,
  });
}

/** Every file below the real %LOCALAPPDATA%\padmate-updater, with size and time. */
async function realUpdaterFolder(): Promise<string> {
  const base = join(process.env.LOCALAPPDATA ?? "", "padmate-updater");
  const lines: string[] = [];
  const walk = async (dir: string, relative: string): Promise<void> => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      const name = join(relative, entry.name);
      if (entry.isDirectory()) {
        lines.push(`${name}\\`);
        await walk(path, name);
      } else {
        const info = await stat(path);
        lines.push(`${name} ${info.size} ${info.mtimeMs}`);
      }
    }
  };
  try {
    await walk(base, "");
  } catch {
    return "absent";
  }
  return lines.sort().join("\n");
}

const pendingDir = (browser: string) =>
  join(browser, "updater-home", "Local", "padmate-updater", "pending");

const state = (page: Page) =>
  page.evaluate(() => window.padmate.getUpdateState());

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a version on offer is checked, downloaded and verified, and the install is recorded, not run", async ({}, info) => {
  test.skip(!packagedApp, skipReason);
  test.setTimeout(150000);
  const exe = resolve(packagedApp as string);
  const feed = await Feed.start();
  const realBefore = await realUpdaterFolder();
  let started: Started | undefined;
  try {
    // A pending installer of an update already installed (an older version
    // than the application) is removed when the application starts.
    started = await start(exe, feed, {
      before: async (browser) => {
        const stale = pendingDir(browser);
        await mkdir(stale, { recursive: true });
        await writeFile(
          join(stale, OLD_INSTALLER),
          "an update installed long ago",
        );
        await writeFile(
          join(stale, "update-info.json"),
          JSON.stringify({
            fileName: OLD_INSTALLER,
            sha512: "x",
          }),
        );
      },
    });
    const { app, page, browser, version } = started;
    await expect
      .poll(() =>
        readdir(pendingDir(browser)).then(
          (names) => names.length,
          () => 0,
        ),
      )
      .toBe(0);

    // The test mode of the installed application can update, and the person's
    // switch is as updates.json has it.
    expect(await state(page)).toMatchObject({
      supported: true,
      autoCheck: false,
      phase: "idle",
      dialog: "none",
    });
    expect(feed.hits).toEqual([]);

    // 检查更新: a version is on offer, and the dialog says so.
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page.getByRole("button", { name: "检查更新", exact: true }).click();
    const offer = page.getByRole("dialog", { name: "有新版本" });
    await expect(offer).toBeVisible({ timeout: 30000 });
    await expect(offer).toContainText("padmate 99.0.0 可以更新了。");
    await page.screenshot({ path: info.outputPath("updater-offer.png") });
    expect(await state(page)).toMatchObject({
      phase: "available",
      version: "99.0.0",
      dialog: "available",
    });
    // The feed was asked for latest.yml, and for nothing else; what GitHub
    // would see is the address and padmate's name and version.
    expect(feed.paths()).toEqual(looks(version));
    expect(feed.hits[0].headers["user-agent"]).toBe(`padmate/${version}`);
    expect(feed.hits[0].headers["x-user-staging-id"]).toBe(SHARED_STAGING_ID);

    // 下载: the installer is fetched, its checksum verified, and the dialog
    // offers the install.
    await offer.getByRole("button", { name: "下载", exact: true }).click();
    const done = page.getByRole("dialog", { name: "下载完成" });
    await expect(done).toBeVisible({ timeout: 30000 });
    await expect(done).toContainText("安装时映射会停一会儿");
    await page.screenshot({ path: info.outputPath("updater-downloaded.png") });
    const downloaded = join(pendingDir(browser), feed.fileName);
    expect(sha512(await readFile(downloaded))).toBe(sha512(feed.installer));
    expect(feed.paths()).toEqual(looks(version, `GET /${feed.fileName}`));

    // 重启并安装: recorded; the application goes on, and the file is not run.
    await done.getByRole("button", { name: "重启并安装" }).click();
    await expect(done).toHaveCount(0);
    expect(app.process().exitCode).toBeNull();
    // The press first waits for the window's saves (up to 3 seconds), so the
    // phase is "installing" for a moment after the dialog has changed.
    await expect
      .poll(() => state(page), { timeout: 15000 })
      .toMatchObject({ phase: "downloaded", dialog: "none" });
    await expect(
      page.evaluate(() => window.padmate.installUpdate()),
    ).resolves.toEqual({ dryRun: true });
    expect(app.process().exitCode).toBeNull();
    expect(
      (
        await run(
          "powershell.exe",
          [
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            `@(Get-Process | Where-Object { $_.Path -eq '${downloaded.replace(/'/g, "''")}' }).Count`,
          ],
          { windowsHide: true },
        )
      ).stdout.trim(),
    ).toBe("0");
    // The window still works.
    await expect(
      page.evaluate(async () => (await window.padmate.getConnection()).phase),
    ).resolves.toBe("connected");

    // The button shows the downloaded update again without asking the network.
    const before = feed.hits.length;
    await page.getByRole("button", { name: "检查更新", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "下载完成" })).toBeVisible();
    expect(feed.hits.length).toBe(before);
  } finally {
    await finish(exe, started);
    await feed.stop();
  }
  // Nothing of the real folder was touched.
  expect(await realUpdaterFolder()).toBe(realBefore);
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a tampered installer is refused, a version that is installed is up to date, and a feed that is not there is told", async ({}, info) => {
  test.skip(!packagedApp, skipReason);
  test.setTimeout(150000);
  const exe = resolve(packagedApp as string);
  const feed = await Feed.start();
  const realBefore = await realUpdaterFolder();
  let started: Started | undefined;
  try {
    started = await start(exe, feed);
    const { page, browser, version } = started;

    // A body that is not what latest.yml describes: the download is refused,
    // and no installer is left.
    feed.body = Buffer.alloc(feed.installer.length, "X");
    await page.evaluate(() => window.padmate.checkForUpdate());
    await expect(page.getByRole("dialog", { name: "有新版本" })).toBeVisible();
    await page.getByRole("button", { name: "下载", exact: true }).click();
    const failed = page.getByRole("dialog", { name: "无法下载更新" });
    await expect(failed).toBeVisible({ timeout: 30000 });
    await page.screenshot({ path: info.outputPath("updater-tampered.png") });
    expect(await state(page)).toMatchObject({
      phase: "idle",
      dialog: "downloadFailed",
      errorCode: "ERR_CHECKSUM_MISMATCH",
    });
    await expect(
      readdir(pendingDir(browser)).then((names) =>
        names.filter((name) => name.endsWith(".exe")),
      ),
    ).resolves.toEqual([]);
    await failed.getByRole("button", { name: "确定" }).click();
    await expect(failed).toHaveCount(0);

    // The version the application has, offered by the feed, is no update: 检查更新
    // says the application is up to date.
    feed.version = version;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page.getByRole("button", { name: "检查更新", exact: true }).click();
    const current = page.getByRole("dialog", { name: "已是最新版本" });
    await expect(current).toBeVisible({ timeout: 30000 });
    await page.screenshot({ path: info.outputPath("updater-current.png") });
    await current.getByRole("button", { name: "确定" }).click();
    await expect(current).toHaveCount(0);

    // A feed that answers 404 (a private repository does): the button says so.
    feed.mode = "missing";
    await page.getByRole("button", { name: "检查更新", exact: true }).click();
    const unreachable = page.getByRole("dialog", { name: "无法检查更新" });
    await expect(unreachable).toBeVisible({ timeout: 30000 });
    await expect(
      unreachable.getByRole("button", { name: "查看发布页" }),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath("updater-unreachable.png") });
    expect(await state(page)).toMatchObject({
      phase: "idle",
      dialog: "checkFailed",
    });
    expect((await state(page)).errorCode).toMatch(/^ERR_/);
    await unreachable.getByRole("button", { name: "确定" }).click();
    await expect(unreachable).toHaveCount(0);
  } finally {
    await finish(exe, started);
    await feed.stop();
  }
  expect(await realUpdaterFolder()).toBe(realBefore);
});

test("跳过这个版本 is kept in updates.json, and 以后再说 is not", async () => {
  test.skip(!packagedApp, skipReason);
  test.setTimeout(120000);
  const exe = resolve(packagedApp as string);
  const feed = await Feed.start();
  let started: Started | undefined;
  try {
    started = await start(exe, feed);
    const { page, browser } = started;
    await page.evaluate(() => window.padmate.checkForUpdate());
    const offer = page.getByRole("dialog", { name: "有新版本" });
    await expect(offer).toBeVisible({ timeout: 30000 });
    await offer.getByRole("button", { name: "以后再说" }).click();
    await expect(offer).toHaveCount(0);
    expect(await state(page)).toMatchObject({ phase: "idle", dialog: "none" });
    expect(
      JSON.parse(await readFile(join(browser, "updates.json"), "utf8")),
    ).toMatchObject({
      skippedVersion: "",
    });

    await page.evaluate(() => window.padmate.checkForUpdate());
    await expect(offer).toBeVisible();
    await offer.getByRole("button", { name: "跳过这个版本" }).click();
    await expect(offer).toHaveCount(0);
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(browser, "updates.json"), "utf8")),
      )
      .toMatchObject({
        version: 1,
        autoCheck: false,
        skippedVersion: "99.0.0",
      });

    // The person who asks is still told.
    await page.evaluate(() => window.padmate.checkForUpdate());
    await expect(offer).toBeVisible();
  } finally {
    await finish(exe, started);
    await feed.stop();
  }
});

test("the automatic check comes 30 seconds after the start, offers a version, and says nothing when it fails", async () => {
  test.skip(!packagedApp, skipReason);
  test.setTimeout(240000);
  const exe = resolve(packagedApp as string);

  // A failing feed: silent, but it did ask.
  const missing = await Feed.start();
  missing.mode = "missing";
  let started: Started | undefined;
  try {
    started = await start(exe, missing, {
      settings: { autoCheck: true, lastCheck: 0 },
    });
    const { page, browser } = started;
    // A release candidate looks twice (among the prereleases, then at the
    // releases), so the feed may be asked more than once.
    await expect
      .poll(() => missing.paths().length, {
        timeout: 60000,
        intervals: [1000],
      })
      .toBeGreaterThan(0);
    expect(new Set(missing.paths())).toEqual(new Set(["GET /latest.yml"]));
    await expect
      .poll(async () => (await state(page)).errorCode, { timeout: 15000 })
      .toMatch(/^ERR_/);
    expect(await state(page)).toMatchObject({ phase: "idle", dialog: "none" });
    await expect(page.getByRole("dialog")).toHaveCount(0);
    // The time of the check is kept: the next one is a day away.
    await expect
      .poll(
        async () =>
          JSON.parse(await readFile(join(browser, "updates.json"), "utf8"))
            .lastCheck,
      )
      .toBeGreaterThan(0);
  } finally {
    await finish(exe, started);
    await missing.stop();
  }

  // A feed with a newer version: the dialog opens by itself.
  const offered = await Feed.start();
  started = undefined;
  try {
    started = await start(exe, offered, {
      settings: { autoCheck: true, lastCheck: 0 },
    });
    const { page, version } = started;
    await expect(page.getByRole("dialog", { name: "有新版本" })).toBeVisible({
      timeout: 60000,
    });
    expect(offered.paths()).toEqual(looks(version));
  } finally {
    await finish(exe, started);
    await offered.stop();
  }

  // A check made a few hours ago is not made again at the start.
  const recent = await Feed.start();
  started = undefined;
  try {
    started = await start(exe, recent, {
      settings: { autoCheck: true, lastCheck: Date.now() - 3 * 60 * 60 * 1000 },
    });
    await started.page.waitForTimeout(35000);
    expect(recent.hits).toEqual([]);
    expect(await state(started.page)).toMatchObject({
      phase: "idle",
      dialog: "none",
    });
  } finally {
    await finish(exe, started);
    await recent.stop();
  }
});
