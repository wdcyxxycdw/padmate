import type {
  UpdateDialogKind,
  UpdateDismissal,
  UpdateInstallResult,
  UpdatePhase,
  UpdateState,
} from "../shared/update";
import {
  CHECK_INTERVAL_MS,
  compareVersions,
  FIRST_CHECK_MS,
  UPDATE_URL,
  validVersion,
} from "./update-config";
import type { UpdateSettings } from "./update-settings";

/** How long a check may take before it counts as failed. */
export const CHECK_TIMEOUT_MS = 90_000;

/**
 * What the controller needs of electron-updater (updater-electron.ts is the
 * only file that has the real one), so that the logic is tested with a fake.
 */
export interface UpdaterLike {
  /**
   * Looks for a version above the running one, once. Resolves the version
   * found, null when there is none, and rejects when it could not look.
   */
  check(): Promise<string | null>;
  /** Downloads and verifies the version the last check found. */
  download(onProgress: (percent: number) => void): Promise<void>;
  /**
   * Runs the installer and quits. Returns nothing; the test mode of the
   * installed application records the call and returns { dryRun: true }.
   */
  quitAndInstall(
    isSilent: boolean,
    isForceRunAfter: boolean,
  ): { dryRun: true } | undefined;
  /** Calls listener with an error the updater raises (the install may report one late). */
  onInstallError(listener: (error: unknown) => void): void;
}

/**
 * The updater of a build that cannot update (a development run, or the
 * installed application's test mode without a test feed): every call fails.
 * The controller never calls it, as it is not supported.
 */
export const inertUpdater: UpdaterLike = {
  check: () => Promise.reject(new Error("This build cannot update")),
  download: () => Promise.reject(new Error("This build cannot update")),
  quitAndInstall: () => {
    throw new Error("This build cannot update");
  },
  onInstallError: () => {},
};

export interface UpdateSettingsLike {
  readonly current: UpdateSettings;
  update(change: Partial<Omit<UpdateSettings, "version">>): Promise<void>;
}

export interface UpdateControllerDeps {
  updater: UpdaterLike;
  settings: UpdateSettingsLike;
  /** The running application's version, app.getVersion(). */
  currentVersion: string;
  /** False in a development build: nothing is checked. */
  supported: boolean;
  /** Opens an address in the browser; the controller only passes it the download site's. */
  openExternal: (url: string) => Promise<void> | void;
  /**
   * Lets the window finish its saves before the application quits: the
   * close guard's settle (UI-43). Resolves when they are done or time is up.
   */
  flush: () => Promise<void>;
  /** Makes closing wait for the saves again (CloseGuard.rearm). */
  rearm: () => void;
  /** Pushes a new state to the window. */
  notify: (state: UpdateState) => void;
  now?: () => number;
}

/** An error's short code, for the state and the log; never its message. */
function codeOf(error: unknown): string {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" && /^[A-Z][A-Z0-9_]{2,63}$/.test(code)
    ? code
    : "ERR_UPDATE";
}

/**
 * The automatic update (PKG-10, ADR-0073): when to look for a new version,
 * what to tell the person, and the install, which waits for the saves and
 * runs only when they ask. It decides; electron-updater (UpdaterLike) does
 * the network, the checksum and the installer. Everything else it needs comes
 * in through the constructor, so the tests run it under fake timers.
 *
 * A version offered by an automatic check is silent when the person skipped
 * it; a failed automatic check is silent too. Only what the person asked for
 * (the button, 下载, 重启并安装) is answered, with a dialog.
 */
export class UpdateController {
  private phase: UpdatePhase = "idle";
  private version = "";
  private percent = 0;
  private dialog: UpdateDialogKind = "none";
  private errorCode = "";
  private timer: ReturnType<typeof setTimeout> | undefined;
  private running: Promise<void> | undefined;
  // The check that runs was asked for by the person.
  private manual = false;
  private last = "";
  private readonly now: () => number;

  constructor(private readonly deps: UpdateControllerDeps) {
    this.now = deps.now ?? (() => Date.now());
    deps.updater.onInstallError((error) => this.installFailed(error));
  }

  getState(): UpdateState {
    return {
      supported: this.deps.supported,
      autoCheck: this.deps.settings.current.autoCheck,
      phase: this.phase,
      version: this.version,
      percent: this.percent,
      dialog: this.dialog,
      errorCode: this.errorCode,
    };
  }

  /** Starts the daily check. Settings are loaded by now. */
  start(): void {
    if (!this.deps.supported) return;
    const { skippedVersion } = this.deps.settings.current;
    // The version the person skipped is behind them now.
    if (
      skippedVersion &&
      compareVersions(this.deps.currentVersion, skippedVersion) >= 0
    )
      void this.persist({ skippedVersion: "" });
    this.schedule();
  }

  dispose(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
  }

  /** 自动检查更新. */
  async setAutoCheck(enabled: boolean): Promise<UpdateState> {
    await this.persist({ autoCheck: enabled });
    this.schedule();
    this.emit();
    return this.getState();
  }

  /**
   * Looks for a new version. manual is the button: it is answered with a
   * dialog whatever the result, and shows an offer the person skipped. An
   * automatic check (the timer) speaks only of a version worth offering.
   */
  async check(manual: boolean): Promise<UpdateState> {
    if (!this.deps.supported) return this.getState();
    if (this.phase === "checking") {
      this.manual ||= manual;
      await this.running;
      return this.getState();
    }
    if (this.phase !== "idle") {
      // Something is on offer, downloading or downloaded: the button shows
      // it again, and no network is asked.
      if (manual) {
        if (this.phase === "available") this.dialog = "available";
        else if (this.phase === "downloading") this.dialog = "downloading";
        else if (this.phase === "downloaded") this.dialog = "downloaded";
        this.emit();
      }
      return this.getState();
    }
    this.phase = "checking";
    this.manual = manual;
    this.errorCode = "";
    this.emit();
    this.running = this.look();
    await this.running;
    return this.getState();
  }

  private async look(): Promise<void> {
    let found: string | null = null;
    let failure: unknown;
    try {
      found = await this.ask();
    } catch (error) {
      failure = error;
    }
    // A check that reached out counts, whatever it found: the next one is a
    // day away, so that a person without a connection is not asked every start.
    await this.persist({ lastCheck: this.now() });
    const manual = this.manual;
    this.manual = false;
    if (failure !== undefined) {
      this.phase = "idle";
      this.errorCode = codeOf(failure);
      this.dialog = manual ? "checkFailed" : "none";
    } else if (found === null) {
      this.phase = "idle";
      this.dialog = manual ? "upToDate" : "none";
    } else if (!manual && found === this.deps.settings.current.skippedVersion) {
      this.phase = "idle";
      this.dialog = "none";
    } else {
      this.phase = "available";
      this.version = found;
      this.dialog = "available";
    }
    this.emit();
  }

  /**
   * The one look of a check: a checked version above the running one, or
   * null. The download site holds releases only, and one latest.yml that
   * every build reads (ADR-0078): a release candidate (1.0.3-rc.1) is not
   * shown newer candidates, and is offered the release when that is higher,
   * which is the same look as for a release. A site without latest.yml is a
   * failure of the check, not "nothing newer".
   */
  private async ask(): Promise<string | null> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () =>
          reject(
            Object.assign(new Error("The check took too long"), {
              code: "ERR_UPDATE_TIMEOUT",
            }),
          ),
        CHECK_TIMEOUT_MS,
      );
    });
    let answer: string | null;
    try {
      answer = await Promise.race([this.deps.updater.check(), timeout]);
    } finally {
      clearTimeout(timer);
    }
    if (answer === null) return null;
    const version = validVersion(answer);
    if (version === null)
      throw Object.assign(new Error("The feed names no valid version"), {
        code: "ERR_UPDATE_VERSION",
      });
    // electron-updater does not report these as updates; the line is held here too.
    return compareVersions(version, this.deps.currentVersion) > 0
      ? version
      : null;
  }

  /** 下载. */
  async download(): Promise<UpdateState> {
    if (this.phase !== "available") return this.getState();
    this.phase = "downloading";
    this.percent = 0;
    this.dialog = "downloading";
    this.errorCode = "";
    this.emit();
    try {
      await this.deps.updater.download((percent) => {
        const whole = Number.isFinite(percent)
          ? Math.min(100, Math.max(0, Math.floor(percent)))
          : 0;
        if (whole === this.percent || this.phase !== "downloading") return;
        this.percent = whole;
        this.emit();
      });
      this.phase = "downloaded";
      this.percent = 100;
      // Also when the progress dialog was dismissed: the person started it.
      this.dialog = "downloaded";
    } catch (error) {
      this.phase = "idle";
      this.version = "";
      this.percent = 0;
      this.errorCode = codeOf(error);
      this.dialog = "downloadFailed";
    }
    this.emit();
    return this.getState();
  }

  /**
   * 重启并安装. Lets the window finish its saves (the installer stops the
   * daemon, and the application quits), then runs the installer silently and
   * has it start padmate again. Only the person's press gets here, and the
   * call below is the only one that installs.
   */
  async install(): Promise<UpdateInstallResult> {
    if (this.phase !== "downloaded")
      throw new Error("The update is not ready to install");
    this.phase = "installing";
    this.dialog = "installing";
    this.errorCode = "";
    this.emit();
    try {
      await this.deps.flush();
    } catch {
      // The saves are best effort: the person asked to install.
    }
    let recorded: { dryRun: true } | undefined;
    try {
      recorded = this.deps.updater.quitAndInstall(true, true);
    } catch (error) {
      this.installFailed(error);
      return { dryRun: false };
    }
    if (recorded && recorded.dryRun === true) {
      // The installed application's test mode: nothing was installed, and
      // the window stays.
      this.deps.rearm();
      this.phase = "downloaded";
      this.dialog = "none";
      this.emit();
      return { dryRun: true };
    }
    return { dryRun: false };
  }

  /** The installer could not be started, or the updater says it failed. */
  private installFailed(error: unknown): void {
    if (this.phase !== "installing") return;
    this.deps.rearm();
    this.phase = "downloaded";
    this.errorCode = codeOf(error);
    this.dialog = "installFailed";
    this.emit();
  }

  /**
   * 以后再说 and 跳过这个版本, and the OK of a dialog that only told how
   * something ended. A download goes on behind a dismissed progress dialog.
   */
  async dismiss(choice: UpdateDismissal): Promise<UpdateState> {
    switch (this.dialog) {
      case "available":
        if (choice === "skip")
          await this.persist({ skippedVersion: this.version });
        this.phase = "idle";
        this.version = "";
        this.dialog = "none";
        break;
      case "none":
      case "installing":
        return this.getState();
      default:
        this.dialog = "none";
    }
    this.emit();
    return this.getState();
  }

  /**
   * 手动下载: the download site, which starts the download of the newest
   * installer in the browser. The address is the constant, whatever the feed
   * said.
   */
  async openManualDownload(): Promise<void> {
    await this.deps.openExternal(UPDATE_URL);
  }

  private async persist(
    change: Partial<Omit<UpdateSettings, "version">>,
  ): Promise<void> {
    try {
      await this.deps.settings.update(change);
    } catch {
      // The value holds for this run; the file is tried again at the next change.
    }
  }

  /**
   * The next automatic check: 30 s after the start when a day has passed,
   * else when it has. A timer that just fired waits a whole day (afterTimer),
   * whether it checked or found something in hand: the age of the last
   * check says "a day has passed" for all that time otherwise.
   */
  private schedule(afterTimer = false): void {
    this.dispose();
    if (!this.deps.supported || !this.deps.settings.current.autoCheck) return;
    const age = this.now() - this.deps.settings.current.lastCheck;
    const delay = afterTimer
      ? CHECK_INTERVAL_MS
      : age >= CHECK_INTERVAL_MS
        ? FIRST_CHECK_MS
        : Math.min(CHECK_INTERVAL_MS - age, CHECK_INTERVAL_MS);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.automatic();
    }, delay);
  }

  private async automatic(): Promise<void> {
    // Not while something is on offer, downloaded or installing: the person
    // has it in hand.
    if (this.phase === "idle") await this.check(false);
    this.schedule(true);
  }

  private emit(): void {
    const state = this.getState();
    const text = JSON.stringify(state);
    if (text === this.last) return;
    this.last = text;
    this.deps.notify(state);
  }
}
