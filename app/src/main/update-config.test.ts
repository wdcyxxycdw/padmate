import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  CHECK_INTERVAL_MS,
  compareVersions,
  FIRST_CHECK_MS,
  isManualDownloadURL,
  redirectUpdaterHome,
  UPDATE_CHANNEL,
  UPDATE_URL,
  UPDATER_CACHE_DIR,
  updaterCacheRoot,
  updaterTestHome,
  validVersion,
} from "./update-config";

const text = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

describe("the update source (PKG-10, ADR-0078)", () => {
  it("is the download site electron-builder.yml publishes to", () => {
    const config = text("../../electron-builder.yml");
    expect(config).toMatch(/^publish:\s*$/m);
    expect(config).toMatch(/^\s+- provider: generic$/m);
    const lines = config.split("\n").map((line) => line.trim());
    expect(lines).toContain(`url: ${UPDATE_URL}`);
    expect(lines).toContain(`channel: ${UPDATE_CHANNEL}`);
    // One source, and not the repository the code lives in.
    expect(config.match(/^\s+- provider:/gm)).toHaveLength(1);
    expect(config).not.toMatch(/^\s+owner:/m);
  });

  it("is an https address of the download site that ends in a slash", () => {
    // latest.yml names the installer by a relative path, which electron-updater
    // resolves against this address: without the slash the file is not found.
    const url = new URL(UPDATE_URL);
    expect(url.protocol).toBe("https:");
    expect(url.host).toBe("padmate.download");
    expect(UPDATE_URL.endsWith("/")).toBe(true);
    expect(url.pathname).toBe("/");
    expect(url.search).toBe("");
    expect(url.hash).toBe("");
  });

  it("reads the channel latest, which is the file latest.yml", () => {
    // electron-builder would otherwise derive the channel from the prerelease
    // part of the version (rc.yml for 1.0.3-rc.1), a file the site never has.
    expect(UPDATE_CHANNEL).toBe("latest");
  });

  it("keeps downloads in the folder the installer cleans up", () => {
    const nsh = text("../../build/installer.nsh");
    expect(nsh).toContain(`!define PADMATE_UPDATER_DIR "${UPDATER_CACHE_DIR}"`);
  });

  it("checks 30 seconds after the start, then daily", () => {
    expect(FIRST_CHECK_MS).toBe(30_000);
    expect(CHECK_INTERVAL_MS).toBe(24 * 60 * 60 * 1000);
  });

  it("finds the cache of electron-updater the way it does: LOCALAPPDATA", () => {
    expect(
      updaterCacheRoot({ LOCALAPPDATA: "C:\\Users\\a\\AppData\\Local" }),
    ).toBe("C:\\Users\\a\\AppData\\Local\\padmate-updater");
    expect(updaterCacheRoot({}, "C:\\Users\\a")).toBe(
      "C:\\Users\\a\\AppData\\Local\\padmate-updater",
    );
  });
});

describe("the downloads of the installed application's test mode", () => {
  it("go below its private browser data, not to the real %LOCALAPPDATA%", () => {
    const env: NodeJS.ProcessEnv = {
      LOCALAPPDATA: "C:\\Users\\real\\AppData\\Local",
    };
    const home = redirectUpdaterHome(env, "D:\\test space\\browser");
    expect(home).toBe("D:\\test space\\browser\\updater-home\\Local");
    expect(env.LOCALAPPDATA).toBe(home);
    // electron-updater then keeps its pending download below it.
    expect(updaterCacheRoot(env)).toBe(
      "D:\\test space\\browser\\updater-home\\Local\\padmate-updater",
    );
  });

  it("have a home of their own, below the browser data", () => {
    expect(updaterTestHome("D:\\test space\\browser")).toBe(
      "D:\\test space\\browser\\updater-home\\Local",
    );
  });

  it("are redirected in no other mode", () => {
    const main = text("./index.ts");
    const adapter = text("./updater-electron.ts");
    expect(main.match(/redirectUpdaterHome\(/g) ?? []).toHaveLength(0);
    expect(adapter.match(/redirectUpdaterHome\(/g)).toHaveLength(1);
    expect(adapter).toMatch(
      /if \(options\.packagedTest\) \{[^}]*redirectUpdaterHome\(process\.env, options\.userData\)/,
    );
  });
});

describe("the address Main opens", () => {
  it("is the download site and no other", () => {
    expect(UPDATE_URL).toBe("https://padmate.download/");
    expect(isManualDownloadURL(UPDATE_URL)).toBe(true);
    for (const url of [
      "",
      "https://padmate.download",
      "https://padmate.download/ ",
      "https://padmate.download/?x=1",
      "https://padmate.download/#x",
      "https://padmate.download/latest.yml",
      "https://padmate.download/padmate-v1.0.3-windows-amd64-setup.exe",
      "https://padmate.download:8443/",
      "https://padmate.download.evil.example/",
      "https://evil.example/padmate.download/",
      "https://user@padmate.download/",
      "http://padmate.download/",
      "HTTPS://PADMATE.DOWNLOAD/",
      "https://github.com/wdcyxxycdw/padmate/releases",
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v1.0.1",
      "file:///C:/Windows/System32/calc.exe",
      "javascript:alert(1)",
    ])
      expect(isManualDownloadURL(url), url).toBe(false);
  });
});

describe("version text from the feed", () => {
  it.each(["1.0.0", "0.14.0", "1.0.0-rc.1", "10.20.30-beta.2.x-y", "1.0.0-0"])(
    "accepts %s",
    (version) => {
      expect(validVersion(version)).toBe(version);
    },
  );

  it.each([
    "",
    "v1.0.0",
    "1.0",
    "1.0.0.0",
    "01.0.0",
    "1.0.0+build",
    "1.0.0-",
    "1.0.0-rc..1",
    " 1.0.0",
    "1.0.0\n",
    "1.0.0/../x",
    "<b>1.0.0</b>",
    `1.0.0-${"a".repeat(80)}`,
    null,
    undefined,
    1,
    {},
  ])("refuses %j", (version) => {
    expect(validVersion(version)).toBeNull();
  });
});

describe("version order (semver)", () => {
  it.each([
    ["1.0.0", "1.0.0", 0],
    ["1.0.1", "1.0.0", 1],
    ["1.0.0", "1.0.1", -1],
    ["1.10.0", "1.9.9", 1],
    ["2.0.0", "1.99.99", 1],
    // A prerelease comes before its release.
    ["1.0.0", "1.0.0-rc.2", 1],
    ["1.0.0-rc.1", "1.0.0", -1],
    ["1.0.0-rc.2", "1.0.0-rc.1", 1],
    // Numbers by value, words by letters, a number before a word.
    ["1.0.0-rc.10", "1.0.0-rc.9", 1],
    ["1.0.0-beta", "1.0.0-alpha", 1],
    ["1.0.0-1", "1.0.0-alpha", -1],
    // More identifiers is later when the first ones are equal.
    ["1.0.0-rc.1.1", "1.0.0-rc.1", 1],
  ])("%s against %s is %i", (a, b, want) => {
    expect(Math.sign(compareVersions(a, b))).toBe(want);
  });

  it("treats a text that is not a version as older than any version", () => {
    expect(compareVersions("junk", "0.0.1")).toBeLessThan(0);
    expect(compareVersions("0.0.1", "junk")).toBeGreaterThan(0);
  });
});
