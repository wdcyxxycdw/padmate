import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { updateHandlers } from "./update-handlers";

// The update code is wired through files that need Electron to run, so these
// checks read them (as ipc-wiring.test.ts does for the rest).
const appRoot = fileURLToPath(new URL("../..", import.meta.url));
const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

/** Every non-test source file below src/ and its text. */
function sources(): Array<{ path: string; text: string }> {
  const found: Array<{ path: string; text: string }> = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) walk(path);
      else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name))
        found.push({
          path: relative(appRoot, path).replaceAll("\\", "/"),
          text: readFileSync(path, "utf8"),
        });
    }
  };
  walk(join(appRoot, "src"));
  return found;
}

const pins = (needle: string | RegExp) =>
  sources()
    .filter(({ text }) =>
      typeof needle === "string" ? text.includes(needle) : needle.test(text),
    )
    .map(({ path }) => path);

describe("how electron-updater is held (PKG-10, ADR-0073)", () => {
  it("is imported by one file, at the version the notes were read from", () => {
    expect(pins(/from "electron-updater|require\("electron-updater/)).toEqual([
      "src/main/updater-electron.ts",
    ]);
    const pkg = JSON.parse(readFileSync(join(appRoot, "package.json"), "utf8"));
    expect(pkg.dependencies["electron-updater"]).toBe("6.8.9");
  });

  it("is bundled into the main process, which has no node_modules to find it in", () => {
    const config = readFileSync(
      join(appRoot, "electron.vite.config.ts"),
      "utf8",
    );
    expect(config).toMatch(
      /externalizeDepsPlugin\(\{\s*exclude:\s*\["electron-updater"\]\s*\}\)/,
    );
    const builder = readFileSync(join(appRoot, "electron-builder.yml"), "utf8");
    expect(builder).toContain('- "!node_modules"');
  });

  it("installs in one place: quitAndInstall(true, true) is written once, after the saves", () => {
    expect(pins("quitAndInstall(true, true)")).toEqual([
      "src/main/update-controller.ts",
    ]);
    const controller = source("./update-controller.ts");
    expect(controller).toMatch(
      /await this\.deps\.flush\(\);[\s\S]*?this\.deps\.updater\.quitAndInstall\(true, true\)/,
    );
    expect(controller.match(/updater\.quitAndInstall\(/g)).toHaveLength(1);
  });

  it("is told to do nothing by itself", () => {
    const adapter = source("./updater-electron.ts");
    for (const setting of [
      "autoDownload = false",
      "autoInstallOnAppQuit = false",
      "allowDowngrade = false",
      "disableDifferentialDownload = true",
      "disableWebInstaller = true",
    ])
      expect(adapter).toContain(`updater.${setting};`);
  });

  it("is the NSIS updater with a source it names, not the lazy autoUpdater", () => {
    const adapter = source("./updater-electron.ts");
    expect(adapter.match(/new NsisUpdater\(/g)).toHaveLength(1);
    expect(adapter).not.toMatch(/\bautoUpdater\b/);
    // Setting the channel allows a downgrade, and the dev switch reads files
    // that are not there; neither is used.
    expect(adapter).not.toMatch(/\.channel\s*=/);
    expect(adapter).not.toContain("forceDevUpdateConfig");
    expect(adapter).not.toContain("setFeedURL");
    // One source for both modes, given by code and not read from
    // app-update.yml: the download site, and the channel written down
    // (electron-builder would name it after the prerelease part of the version).
    expect(adapter).toMatch(
      /new NsisUpdater\(\{\s*provider: "generic",\s*url: options\.packagedTest && options\.feed \? options\.feed : UPDATE_URL,\s*channel: UPDATE_CHANNEL,\s*\}\);/,
    );
    // No look among the prereleases, and nothing of the GitHub provider.
    expect(adapter).not.toContain("allowPrerelease");
    expect(adapter).not.toContain('provider: "github"');
    expect(adapter).not.toMatch(/\bcheck\(\w+\)/);
  });

  it("sends the download site the address and padmate's name and version, and nothing that tells one person from another", () => {
    const adapter = source("./updater-electron.ts");
    // biome-ignore lint/suspicious/noTemplateCurlyInString: the text of the source file
    expect(adapter).toContain('"User-Agent": `padmate/${options.version}`');
    expect(adapter).toContain('"x-user-staging-id": SHARED_STAGING_ID');
    expect(adapter).toContain(
      'SHARED_STAGING_ID = "00000000-0000-0000-0000-000000000000"',
    );
  });

  it("is replaced by a recorder in the installed application's test mode, with no switch back", () => {
    const adapter = source("./updater-electron.ts");
    expect(adapter).toMatch(
      /quitAndInstall: options\.packagedTest\s*\?[\s\S]*?return \{ dryRun: true as const \};/,
    );
    // The feed is the test mode's alone, and no variable but the gate
    // (PADMATE_UI_TEST_PACKAGED and the three folders) turns it on.
    expect(pins("PADMATE_UI_TEST_UPDATE_URL")).toEqual([
      "src/main/runtime-options.ts",
    ]);
    expect(adapter).toMatch(
      /options\.packagedTest && options\.feed \? options\.feed : UPDATE_URL/,
    );
    expect(adapter).not.toMatch(/process\.env\.PADMATE/);
  });
});

describe("how the update code is wired into Main", () => {
  const main = source("./index.ts");
  const wiring = source("./update-main.ts");

  it("hands the window the handlers and the push, and the close guard the install", () => {
    expect(main).toContain("...updateHandlers(update),");
    expect(main).toMatch(
      /updates = await createUpdates\(\{\s*closeGuard,\s*notify: \(state\) => \{\s*if \(window && !window\.isDestroyed\(\)\)\s*window\.webContents\.send\("padmate:updateState", state\);/,
    );
    expect(wiring).toContain(
      "flush: () => host.closeGuard.settle(CLOSE_WAIT_MS),",
    );
    expect(wiring).toContain("rearm: () => host.closeGuard.rearm(),");
  });

  it("opens only the download site, through the controller; the window opens none", () => {
    expect(wiring).toMatch(
      /openExternal: \(url\) => \{\s*if \(!isManualDownloadURL\(url\)\)\s*throw new Error\("Not the download address"\);\s*return shell\.openExternal\(url\);/,
    );
    // The other place that opens an address is the one Windows Settings page
    // of the Xbox button's notice (system-settings.ts, UI-30): the handler
    // table calls the shell with the one address that file allows.
    expect(pins("shell.openExternal(").sort()).toEqual([
      "src/main/index.ts",
      "src/main/update-main.ts",
    ]);
    expect(main.match(/shell\.openExternal\(/g)).toHaveLength(1);
    expect(main).toContain(
      "...systemSettingsHandlers((url) => shell.openExternal(url)),",
    );
    expect(source("../preload/index.ts")).not.toContain("shell");
  });

  it("does not check in a development run, nor in a test without a test feed", () => {
    expect(wiring).toMatch(
      /let supported =\s*app\.isPackaged && \(!options\.packagedTest \|\| !!options\.updateFeed\);/,
    );
    expect(wiring).toContain("let updater: UpdaterLike = inertUpdater;");
    // A failure to make the real updater leaves the application running.
    expect(wiring).toMatch(/\} catch \{[^}]*supported = false;/);
  });

  it("deletes an update that is installed already, when the application starts", () => {
    expect(main).toMatch(/if \(app\.isPackaged\) void removeStaleUpdate\(\);/);
    expect(wiring).toContain("cleanStalePending(");
    // In the test mode only the private folder is looked in.
    expect(wiring).toContain(
      "updaterCacheRoot({ LOCALAPPDATA: updaterTestHome(options.userData) })",
    );
  });

  it("starts the daily check with the window, and stops it with the application", () => {
    expect(main).toMatch(
      /void manager\.connect\(\);[\s\S]*?updates\.start\(\);/,
    );
    expect(main).toMatch(
      /events\.close\(\);\s*updates\?\.dispose\(\);\s*\}\);/,
    );
  });

  it("lets a second instance do nothing", () => {
    expect(main).toMatch(
      /\.then\(async \(\) => \{\s*if \(!firstInstance\) return;/,
    );
  });
});

describe("the channels of the window's side of the update", () => {
  it("are invoked by the preload, and the event comes back on one channel", () => {
    const preload = source("../preload/index.ts");
    for (const channel of [
      'getUpdateState: () => invoke("padmate:updateGet"),',
      'checkForUpdate: () => invoke("padmate:updateCheck"),',
      'downloadUpdate: () => invoke("padmate:updateDownload"),',
      'installUpdate: () => invoke("padmate:updateInstall"),',
      'dismissUpdate: (choice) => invoke("padmate:updateDismiss", choice),',
      'setAutoUpdate: (enabled) => invoke("padmate:updateAuto", enabled),',
      'openManualDownload: () => invoke("padmate:updateManualDownload"),',
      'listen<UpdateState>("padmate:updateState", listener),',
    ])
      expect(preload).toContain(channel);
    // The names the handlers have are the ones the preload invokes.
    const unused = async () => {
      throw new Error("unused");
    };
    const names = Object.keys(
      updateHandlers({
        getState: () => {
          throw new Error("unused");
        },
        check: unused,
        download: unused,
        install: unused,
        dismiss: unused,
        setAutoCheck: unused,
        openManualDownload: async () => {},
      }),
    );
    expect(names).toHaveLength(7);
    for (const name of names) expect(preload).toContain(`"padmate:${name}"`);
  });

  it("have a typed call each in the window's API", () => {
    const api = source("../shared/api.ts");
    for (const signature of [
      "getUpdateState(): Promise<UpdateState>;",
      "checkForUpdate(): Promise<UpdateState>;",
      "downloadUpdate(): Promise<UpdateState>;",
      "installUpdate(): Promise<UpdateInstallResult>;",
      "dismissUpdate(choice: UpdateDismissal): Promise<UpdateState>;",
      "setAutoUpdate(enabled: boolean): Promise<UpdateState>;",
      "openManualDownload(): Promise<void>;",
      "onUpdateState(listener: (state: UpdateState) => void): () => void;",
    ])
      expect(api).toContain(signature);
  });
});
