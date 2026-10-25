import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { validVersion } from "./update-config";

/** The window's own file for the update settings, in its browser data. */
export const UPDATE_SETTINGS_FILE = "updates.json";

export interface UpdateSettings {
  version: 1;
  /** 自动检查更新. */
  autoCheck: boolean;
  /** The version whose automatic offers are silenced, "" for none. */
  skippedVersion: string;
  /** Milliseconds since the epoch of the last check that reached out, 0 for none. */
  lastCheck: number;
}

export const defaultUpdateSettings: UpdateSettings = {
  version: 1,
  autoCheck: true,
  skippedVersion: "",
  lastCheck: 0,
};

/**
 * What the file says, field by field: a field that is missing or not valid
 * is its default, and a file that is not JSON, not an object or of another
 * version is all defaults. Nothing in it can make the application fail.
 */
export function parseUpdateSettings(text: string): UpdateSettings {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return { ...defaultUpdateSettings };
  }
  if (value === null || typeof value !== "object" || Array.isArray(value))
    return { ...defaultUpdateSettings };
  const fields = value as Record<string, unknown>;
  if (fields.version !== 1) return { ...defaultUpdateSettings };
  const skipped = validVersion(fields.skippedVersion);
  return {
    version: 1,
    autoCheck:
      typeof fields.autoCheck === "boolean"
        ? fields.autoCheck
        : defaultUpdateSettings.autoCheck,
    skippedVersion: skipped ?? "",
    lastCheck:
      typeof fields.lastCheck === "number" && validTime(fields.lastCheck)
        ? fields.lastCheck
        : 0,
  };
}

function validTime(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0;
}

/**
 * The settings of the automatic update in <userData>\updates.json (PKG-10).
 * They are the window's own, so they work without the daemon and without
 * config.json. current is what the next read sees; writes follow one after
 * another and each replaces the file whole (a temporary file, then a rename),
 * so a crash leaves the old file or the new one.
 */
export class UpdateSettingsStore {
  private value: UpdateSettings = { ...defaultUpdateSettings };
  private queue: Promise<void> = Promise.resolve();
  private counter = 0;

  constructor(private readonly file: string) {}

  get current(): UpdateSettings {
    return { ...this.value };
  }

  /** Reads the file once, at the start. Never rejects. */
  async load(): Promise<UpdateSettings> {
    try {
      this.value = parseUpdateSettings(await readFile(this.file, "utf8"));
    } catch {
      this.value = { ...defaultUpdateSettings };
    }
    return this.current;
  }

  /**
   * Changes some fields. The new value is current at once; the returned
   * promise ends when the file has it (or rejects when it could not be
   * written, in which case the value stays for this run).
   */
  update(change: Partial<Omit<UpdateSettings, "version">>): Promise<void> {
    if (
      change.skippedVersion !== undefined &&
      change.skippedVersion !== "" &&
      validVersion(change.skippedVersion) === null
    )
      return Promise.reject(new Error("Invalid skipped version"));
    if (change.lastCheck !== undefined && !validTime(change.lastCheck))
      return Promise.reject(new Error("Invalid check time"));
    if (change.autoCheck !== undefined && typeof change.autoCheck !== "boolean")
      return Promise.reject(new Error("Invalid automatic check setting"));
    this.value = { ...this.value, ...change };
    const write = this.queue.then(() => this.write());
    // A failed write does not stop the ones after it.
    this.queue = write.catch(() => {});
    return write;
  }

  private async write(): Promise<void> {
    const temporary = `${this.file}.${process.pid}-${++this.counter}.tmp`;
    try {
      await mkdir(dirname(this.file), { recursive: true });
      await writeFile(temporary, `${JSON.stringify(this.value)}\n`, "utf8");
      await rename(temporary, this.file);
    } catch (error) {
      await rm(temporary, { force: true }).catch(() => {});
      throw error;
    }
  }
}
