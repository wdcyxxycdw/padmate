import { readdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { compareVersions, validVersion } from "./update-config";

export interface PendingIO {
  readdir(path: string): Promise<string[]>;
  rm(
    path: string,
    options: {
      recursive: boolean;
      force: boolean;
      maxRetries: number;
      retryDelay: number;
    },
  ): Promise<void>;
}

// electron-updater downloads the installer as pending\<artifactName> (after
// temp-<artifactName> while it is not whole); electron-builder names it
// padmate-v<version>-windows-amd64-setup.exe (artifactName in
// electron-builder.yml).
const installerName = /^(?:temp-)?padmate-v(.+)-windows-amd64-setup\.exe$/;

/**
 * An update that was downloaded and installed leaves its installer in
 * <cache>\pending: electron-updater does not clean it, and the installer that
 * was running may still have held the file when the new application first
 * started. The next start deletes it once the application is that version or
 * later. A newer one, which the person has not installed yet, is left
 * (electron-updater uses it again after checking its checksum).
 *
 * "failed" asks for another try at the next start.
 */
export async function cleanStalePending(options: {
  /** %LOCALAPPDATA%\padmate-updater, or the folder a test redirected it to. */
  cacheRoot: string;
  appVersion: string;
  io?: PendingIO;
}): Promise<"none" | "removed" | "failed"> {
  const io = options.io ?? { readdir, rm };
  const pending = join(options.cacheRoot, "pending");
  let names: string[];
  try {
    names = await io.readdir(pending);
  } catch {
    return "none";
  }
  const versions = names.flatMap((name) => {
    const version = validVersion(installerName.exec(name)?.[1]);
    return version ? [version] : [];
  });
  if (
    versions.length === 0 ||
    versions.some((version) => compareVersions(version, options.appVersion) > 0)
  )
    return "none";
  try {
    await io.rm(pending, {
      recursive: true,
      force: true,
      maxRetries: 5,
      retryDelay: 200,
    });
    return "removed";
  } catch {
    return "failed";
  }
}
