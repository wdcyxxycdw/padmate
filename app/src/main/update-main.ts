import { join } from "node:path";
import { app, shell } from "electron";
import type { UpdateState } from "../shared/update";
import type { CloseGuard } from "./close-guard";
import { CLOSE_WAIT_MS } from "./close-guard";
import { LogRing } from "./log-ring";
import { runtimeOptions } from "./runtime";
import { cleanStalePending } from "./update-cleanup";
import {
  isReleasePageURL,
  updaterCacheRoot,
  updaterTestHome,
} from "./update-config";
import {
  inertUpdater,
  UpdateController,
  type UpdaterLike,
} from "./update-controller";
import { UPDATE_SETTINGS_FILE, UpdateSettingsStore } from "./update-settings";
import { createElectronUpdater } from "./updater-electron";

// The automatic update (PKG-10, ADR-0073) as Main holds it: the pieces of
// update-controller.ts, update-settings.ts and updater-electron.ts put together
// with the window's close guard and the browser. index.ts only calls this.

/**
 * Makes the controller. The settings are read first, so that the window's
 * first question already has the right answer. The updater is the real one
 * only in an installed application, and in its test mode only when the test
 * names a feed on this machine: a development run and a test without a feed
 * never touch the network.
 */
export async function createUpdates(host: {
  closeGuard: Pick<CloseGuard, "settle" | "rearm">;
  notify: (state: UpdateState) => void;
}): Promise<UpdateController> {
  const options = runtimeOptions();
  let supported =
    app.isPackaged && (!options.packagedTest || !!options.updateFeed);
  let updater: UpdaterLike = inertUpdater;
  if (supported) {
    try {
      updater = createElectronUpdater({
        version: app.getVersion(),
        packagedTest: options.packagedTest,
        feed: options.updateFeed,
        userData: options.userData,
        log: new LogRing(),
      });
    } catch {
      // The application goes on without updates rather than not at all.
      supported = false;
    }
  }
  const settings = new UpdateSettingsStore(
    join(options.userData, UPDATE_SETTINGS_FILE),
  );
  await settings.load();
  return new UpdateController({
    updater,
    settings,
    currentVersion: app.getVersion(),
    supported,
    // Only the pages of releases (update-config.ts) are ever opened.
    openExternal: (url) => {
      if (!isReleasePageURL(url)) throw new Error("Not a release page");
      return shell.openExternal(url);
    },
    // The install ends with the application quitting: the window finishes
    // its saves first (UI-43), and closing waits for them again if the
    // install does not start.
    flush: () => host.closeGuard.settle(CLOSE_WAIT_MS),
    rearm: () => host.closeGuard.rearm(),
    notify: host.notify,
  });
}

/**
 * A new version removes the installer an update left in
 * %LOCALAPPDATA%\padmate-updater\pending (update-cleanup.ts). The installer that
 * ran may still have held the file as the application started, so a failure
 * is tried again once, and then at the next start. The installed
 * application's test mode looks in its private folder only.
 */
export async function removeStaleUpdate(): Promise<void> {
  const options = runtimeOptions();
  const cacheRoot = options.packagedTest
    ? updaterCacheRoot({ LOCALAPPDATA: updaterTestHome(options.userData) })
    : updaterCacheRoot(process.env);
  const appVersion = app.getVersion();
  if ((await cleanStalePending({ cacheRoot, appVersion })) !== "failed") return;
  await new Promise((done) => setTimeout(done, 60_000));
  await cleanStalePending({ cacheRoot, appVersion });
}
