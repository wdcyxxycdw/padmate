import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  claimSingleInstance,
  type SingleInstanceHost,
} from "./single-instance";

function host(locked: boolean) {
  const handlers: Array<() => void> = [];
  const app: SingleInstanceHost = {
    requestSingleInstanceLock: vi.fn(() => locked),
    on: vi.fn((_event: "second-instance", handler: () => void) => {
      handlers.push(handler);
    }),
    quit: vi.fn(),
  };
  return { app, handlers };
}

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

describe("one window of padmate per set of browser data (PKG-10)", () => {
  it("is the first instance when the lock is free, and shows its window for a second", () => {
    const { app, handlers } = host(true);
    const show = vi.fn();
    expect(claimSingleInstance(app, show)).toBe(true);
    expect(app.quit).not.toHaveBeenCalled();
    expect(handlers).toHaveLength(1);
    handlers[0]();
    handlers[0]();
    expect(show).toHaveBeenCalledTimes(2);
  });

  it("quits at once when another instance holds the lock, and listens to nothing", () => {
    const { app, handlers } = host(false);
    const show = vi.fn();
    expect(claimSingleInstance(app, show)).toBe(false);
    expect(app.quit).toHaveBeenCalledOnce();
    expect(handlers).toHaveLength(0);
    expect(show).not.toHaveBeenCalled();
  });

  it("is claimed in Main after the browser data folder is set, before anything starts", () => {
    const main = source("./index.ts");
    const setPath = main.indexOf('app.setPath("userData", browserData);');
    const claim = main.indexOf("claimSingleInstance(app,");
    const ready = main.indexOf("app\n  .whenReady()");
    // Electron keys the lock by the userData folder, which the tests and the
    // development runs give a folder of their own (runtime-options.ts): they
    // never meet the lock of the installed application.
    expect(setPath).toBeGreaterThan(0);
    expect(claim).toBeGreaterThan(setPath);
    expect(ready).toBeGreaterThan(claim);
    // The second instance starts nothing: no daemon, no window.
    expect(main).toMatch(
      /\.whenReady\(\)\s*\.then\(async \(\) => \{\s*if \(!firstInstance\) return;/,
    );
    expect(main).toContain("const firstInstance = claimSingleInstance(app,");
  });

  it("brings the existing window to the front, once it is shown", () => {
    const main = source("./index.ts");
    expect(main).toMatch(
      /function showExistingWindow\(\): void \{\s*if \(!window \|\| window\.isDestroyed\(\) \|\| !shown\) return;\s*if \(window\.isMinimized\(\)\) window\.restore\(\);\s*window\.show\(\);\s*window\.focus\(\);\s*\}/,
    );
  });
});
