import { mkdirSync } from "node:fs";
import { NsisUpdater, type ProgressInfo } from "electron-updater";
import type { LogRing } from "./log-ring";
import {
  isNoReleaseError,
  NO_RELEASE_CODE,
  redirectUpdaterHome,
  UPDATE_OWNER,
  UPDATE_REPO,
} from "./update-config";
import type { UpdaterLike } from "./update-controller";

// The one file that imports electron-updater (ipc-wiring tests pin that), so
// that how it is configured is read in one place. It is bundled into the main
// process (electron.vite.config.ts), as the installed application has no
// node_modules.

export interface ElectronUpdaterOptions {
  /** app.getVersion(), put in the User-Agent. */
  version: string;
  /** The installed application started by a test (PKG-03). */
  packagedTest: boolean;
  /** The test's loopback feed (runtime-options.ts); undefined for the real application. */
  feed: string | undefined;
  /** The browser data folder; the test mode keeps its downloads below it. */
  userData: string;
  log: LogRing;
}

// electron-updater sends a per-installation id in x-user-staging-id, for
// staged rollouts padmate does not use. Every installation sends this same
// one, so that the request carries nothing that tells one person from
// another (the id it would make is kept in <userData>\.updaterId).
const SHARED_STAGING_ID = "00000000-0000-0000-0000-000000000000";

/**
 * The installed application's updater (PKG-10, ADR-0073): the releases of the
 * repository in update-config.ts, a silent NSIS install, and nothing happens
 * unless the person says so (UpdateController decides). GitHub sees the
 * address and a User-Agent of padmate and its version; no other data is sent.
 */
export function createElectronUpdater(
  options: ElectronUpdaterOptions,
): UpdaterLike {
  if (options.packagedTest) {
    // Downloads of a test go below its private data, not to the real
    // %LOCALAPPDATA%\padmate-updater (electron-updater reads the variable at
    // the first download).
    mkdirSync(redirectUpdaterHome(process.env, options.userData), {
      recursive: true,
    });
  }
  const updater = new NsisUpdater(
    options.packagedTest && options.feed
      ? { provider: "generic", url: options.feed }
      : { provider: "github", owner: UPDATE_OWNER, repo: UPDATE_REPO },
  );
  updater.logger = options.log;
  // Nothing downloads or installs by itself. autoInstallOnAppQuit would run
  // the installer when the window is closed, which stops the mapping while
  // the person is drawing.
  updater.autoDownload = false;
  updater.autoInstallOnAppQuit = false;
  updater.allowDowngrade = false;
  // The whole installer: PKG-05 deletes the copy a differential download needs.
  updater.disableDifferentialDownload = true;
  updater.disableWebInstaller = true;
  updater.requestHeaders = {
    "User-Agent": `padmate/${options.version}`,
    "x-user-staging-id": SHARED_STAGING_ID,
  };

  const errorListeners: Array<(error: unknown) => void> = [];
  updater.on("error", (error: unknown) => {
    for (const listener of errorListeners) listener(error);
  });

  return {
    async check(allowPrerelease) {
      updater.allowPrerelease = allowPrerelease;
      let result: Awaited<ReturnType<typeof updater.checkForUpdates>>;
      try {
        result = await updater.checkForUpdates();
      } catch (error) {
        // Only the look at the releases can find there are none; for a
        // look among the prereleases the same text is a failure.
        if (!allowPrerelease && isNoReleaseError(error))
          throw Object.assign(new Error("No release is published"), {
            code: NO_RELEASE_CODE,
          });
        throw error;
      }
      // null: the updater is inactive (the application is not installed).
      if (!result) throw new Error("The updater is not active");
      return result.isUpdateAvailable ? result.updateInfo.version : null;
    },
    async download(onProgress) {
      const progress = (info: ProgressInfo) => onProgress(info.percent);
      // Added before the download: electron-updater reports progress only
      // when a listener is there when it starts.
      updater.on("download-progress", progress);
      try {
        await updater.downloadUpdate();
      } finally {
        updater.off("download-progress", progress);
      }
    },
    quitAndInstall: options.packagedTest
      ? // The test mode records the call and installs nothing; there is no
        // switch that makes it install.
        (isSilent, isForceRunAfter) => {
          options.log.info(
            `Recorded quitAndInstall(${isSilent}, ${isForceRunAfter}); not run in the test mode`,
          );
          return { dryRun: true as const };
        }
      : (isSilent, isForceRunAfter) => {
          updater.quitAndInstall(isSilent, isForceRunAfter);
          return undefined;
        },
    onInstallError(listener) {
      errorListeners.push(listener);
    },
  };
}
