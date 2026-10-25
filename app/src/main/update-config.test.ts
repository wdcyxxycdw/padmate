import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  CHECK_INTERVAL_MS,
  compareVersions,
  FIRST_CHECK_MS,
  hasPrerelease,
  isNoReleaseError,
  isReleasePageURL,
  NO_RELEASE_CODE,
  RELEASES_URL,
  redirectUpdaterHome,
  releasePageURL,
  UPDATE_OWNER,
  UPDATE_REPO,
  UPDATER_CACHE_DIR,
  updaterCacheRoot,
  updaterTestHome,
  validVersion,
} from "./update-config";

const text = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

describe("the update source (PKG-10)", () => {
  it("is the repository electron-builder.yml publishes to", () => {
    const config = text("../../electron-builder.yml");
    expect(config).toMatch(/^publish:\s*$/m);
    expect(config).toMatch(/^\s+- provider: github$/m);
    expect(config).toContain(`owner: ${UPDATE_OWNER}`);
    expect(config).toContain(`repo: ${UPDATE_REPO}`);
    expect(RELEASES_URL).toBe(
      `https://github.com/${UPDATE_OWNER}/${UPDATE_REPO}/releases`,
    );
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

describe("the addresses Main opens", () => {
  it("are the release pages releasePageURL builds, and no others", () => {
    expect(isReleasePageURL(RELEASES_URL)).toBe(true);
    expect(isReleasePageURL(releasePageURL("1.0.1"))).toBe(true);
    expect(isReleasePageURL(releasePageURL("1.0.0-rc.2"))).toBe(true);
    for (const url of [
      "",
      "https://github.com/wdcyxxycdw/padmate/releases/download/v1.0.1/a.exe",
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v1.0.1/../../x",
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v1.0.1?x=1",
      "https://github.com/wdcyxxycdw/padmate/releases/tag/1.0.1",
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v",
      "https://github.com/wdcyxxycdw/padmate/releases-evil",
      "https://github.com/wdcyxxycdw/other/releases",
      "http://github.com/wdcyxxycdw/padmate/releases",
      "file:///C:/Windows/System32/calc.exe",
      "javascript:alert(1)",
    ])
      expect(isReleasePageURL(url), url).toBe(false);
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

  it("tells a prerelease from a release", () => {
    expect(hasPrerelease("1.0.0-rc.2")).toBe(true);
    expect(hasPrerelease("1.0.0")).toBe(false);
    expect(hasPrerelease("not a version")).toBe(false);
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

describe("the release page", () => {
  it("is built from the constants and a checked version", () => {
    expect(releasePageURL("1.0.1")).toBe(
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v1.0.1",
    );
    expect(releasePageURL("1.0.0-rc.2")).toBe(
      "https://github.com/wdcyxxycdw/padmate/releases/tag/v1.0.0-rc.2",
    );
  });

  it("falls back to the list of releases for a version it does not trust", () => {
    for (const version of [
      "",
      "1.0.0/../../evil",
      "1.0.0?x=1",
      "javascript:alert(1)",
      "1.0.0#x",
      undefined,
    ])
      expect(releasePageURL(version)).toBe(RELEASES_URL);
  });
});

describe("a look that has no release to read", () => {
  const wrapped = (status: string) =>
    new Error(
      `Cannot parse releases feed: Error: Unable to find latest version on GitHub (https://github.com/o/r/releases/latest), please ensure a production release exists: HttpError: ${status}\nHeaders: {}\n    at createHttpError`,
    );

  it("is told by the text electron-updater gives for a 404 on the latest release", () => {
    expect(NO_RELEASE_CODE).toBe("ERR_UPDATE_NO_RELEASE");
    expect(isNoReleaseError(wrapped("404 Not Found"))).toBe(true);
    // The error before electron-updater wraps it a second time.
    expect(
      isNoReleaseError(
        new Error(
          "Unable to find latest version on GitHub (u), please ensure a production release exists: HttpError: 404 Not Found",
        ),
      ),
    ).toBe(true);
  });

  it("is not told by any other failure", () => {
    expect(isNoReleaseError(wrapped("503 Service Unavailable"))).toBe(false);
    expect(isNoReleaseError(wrapped("4041 Odd"))).toBe(false);
    // A 404 mentioned elsewhere in the text, as a header value is.
    expect(
      isNoReleaseError(
        new Error(
          "HttpError: 503 Unavailable\nHeaders: { x: 'HttpError: 404' }",
        ),
      ),
    ).toBe(false);
    for (const value of [undefined, null, "404", 404, {}, new Error("x")])
      expect(isNoReleaseError(value)).toBe(false);
  });
});
