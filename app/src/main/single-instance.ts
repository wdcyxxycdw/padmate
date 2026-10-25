/** The part of Electron's app that the single-instance lock uses. */
export interface SingleInstanceHost {
  requestSingleInstanceLock(): boolean;
  on(event: "second-instance", handler: () => void): unknown;
  quit(): void;
}

/**
 * Only one padmate window per set of browser data (PKG-10): after an update the
 * installer starts padmate again, and a person may click its shortcut twice.
 * The second instance quits at once and the first one brings its window to
 * the front (onSecond). Electron keys the lock by the userData folder, so
 * development runs and tests, which give theirs a folder of its own
 * (runtime-options.ts), neither meet the installed application's lock nor each
 * other's. Returns false for the instance that must do nothing else.
 */
export function claimSingleInstance(
  app: SingleInstanceHost,
  onSecond: () => void,
): boolean {
  if (!app.requestSingleInstanceLock()) {
    app.quit();
    return false;
  }
  app.on("second-instance", onSecond);
  return true;
}
