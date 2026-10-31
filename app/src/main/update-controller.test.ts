import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateState } from "../shared/update";
import { CHECK_INTERVAL_MS, FIRST_CHECK_MS, UPDATE_URL } from "./update-config";
import {
  CHECK_TIMEOUT_MS,
  inertUpdater,
  UpdateController,
  type UpdaterLike,
} from "./update-controller";
import { defaultUpdateSettings, type UpdateSettings } from "./update-settings";

const START = Date.UTC(2026, 9, 24, 12, 0, 0);
const HOUR = 60 * 60 * 1000;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(START);
});
afterEach(() => {
  vi.useRealTimers();
});

/** An updater that answers from a script, and notes how it was asked. */
function fakeUpdater() {
  const calls: string[] = [];
  const answers: Array<string | null | Error | "hang"> = [];
  let download: (progress: (percent: number) => void) => Promise<void> =
    async () => {};
  let installResult: { dryRun: true } | undefined;
  let installThrows: unknown;
  let errorListener: ((error: unknown) => void) | undefined;
  const updater: UpdaterLike = {
    check: async () => {
      calls.push("check");
      const answer = answers.shift();
      if (answer === "hang") return new Promise<string | null>(() => {});
      if (answer instanceof Error) throw answer;
      return answer ?? null;
    },
    download: (progress) => {
      calls.push("download");
      return download(progress);
    },
    quitAndInstall: (isSilent, isForceRunAfter) => {
      calls.push(`quitAndInstall:${isSilent},${isForceRunAfter}`);
      if (installThrows) throw installThrows;
      return installResult;
    },
    onInstallError: (listener) => {
      errorListener = listener;
    },
  };
  return {
    updater,
    calls,
    answers,
    onDownload: (fn: typeof download) => {
      download = fn;
    },
    installReturns: (value: { dryRun: true }) => {
      installResult = value;
    },
    installThrows: (error: unknown) => {
      installThrows = error;
    },
    fail: (error: unknown) => errorListener?.(error),
  };
}

function setup(
  options: {
    version?: string;
    supported?: boolean;
    settings?: Partial<UpdateSettings>;
    flush?: () => Promise<void>;
  } = {},
) {
  const fake = fakeUpdater();
  let stored: UpdateSettings = {
    ...defaultUpdateSettings,
    ...options.settings,
  };
  const writes: Array<Partial<UpdateSettings>> = [];
  const settings = {
    get current() {
      return { ...stored };
    },
    update: vi.fn(async (change: Partial<UpdateSettings>) => {
      writes.push(change);
      stored = { ...stored, ...change };
    }),
  };
  const states: UpdateState[] = [];
  const opened: string[] = [];
  const guard: string[] = [];
  const controller = new UpdateController({
    // Looks the fake up at each call, so that a test may replace a method.
    updater: {
      check: () => fake.updater.check(),
      download: (progress) => fake.updater.download(progress),
      quitAndInstall: (silent, force) => {
        guard.push("quitAndInstall");
        return fake.updater.quitAndInstall(silent, force);
      },
      onInstallError: (listener) => fake.updater.onInstallError(listener),
    },
    settings,
    currentVersion: options.version ?? "1.0.0",
    supported: options.supported ?? true,
    openExternal: async (url) => {
      opened.push(url);
    },
    flush:
      options.flush ??
      (async () => {
        guard.push("flush");
      }),
    rearm: () => {
      guard.push("rearm");
    },
    notify: (state) => states.push(state),
  });
  return { ...fake, controller, settings, writes, states, opened, guard };
}

/** Lets the promises that are ready run, without moving the clock. */
const settle = () => vi.advanceTimersByTimeAsync(0);

describe("the update controller: when it checks (PKG-10)", () => {
  it("does nothing in a build that cannot update", async () => {
    const t = setup({ supported: false });
    t.controller.start();
    await vi.advanceTimersByTimeAsync(3 * CHECK_INTERVAL_MS);
    expect(t.calls).toEqual([]);
    expect(t.controller.getState()).toMatchObject({
      supported: false,
      phase: "idle",
      dialog: "none",
    });
    // A press of the button does nothing either.
    await t.controller.check(true);
    expect(t.calls).toEqual([]);
    expect(t.controller.getState().dialog).toBe("none");
  });

  it("checks 30 seconds after the start when it never has", async () => {
    const t = setup();
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS - 1);
    expect(t.calls).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    expect(t.calls).toEqual(["check"]);
  });

  it("checks 30 seconds after the start when the last check is a day old", async () => {
    const t = setup({ settings: { lastCheck: START - CHECK_INTERVAL_MS } });
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.calls).toEqual(["check"]);
  });

  it("waits for the day to be over when the last check is younger", async () => {
    const t = setup({ settings: { lastCheck: START - 5 * HOUR } });
    t.controller.start();
    await vi.advanceTimersByTimeAsync(19 * HOUR - 1);
    expect(t.calls).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    expect(t.calls).toEqual(["check"]);
  });

  it("goes on checking every 24 hours while the window is open", async () => {
    const t = setup();
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.calls).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS - 1);
    expect(t.calls).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(t.calls).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    expect(t.calls).toHaveLength(3);
    // The time of every check is kept, for the next start.
    expect(t.writes.map((w) => w.lastCheck)).toEqual([
      START + FIRST_CHECK_MS,
      START + FIRST_CHECK_MS + CHECK_INTERVAL_MS,
      START + FIRST_CHECK_MS + 2 * CHECK_INTERVAL_MS,
    ]);
  });

  it("does not check by itself when 自动检查更新 is off", async () => {
    const t = setup({ settings: { autoCheck: false } });
    t.controller.start();
    await vi.advanceTimersByTimeAsync(3 * CHECK_INTERVAL_MS);
    expect(t.calls).toEqual([]);
    expect(t.controller.getState().autoCheck).toBe(false);
  });

  it("starts and stops the daily check with the switch", async () => {
    const t = setup({ settings: { autoCheck: false } });
    t.controller.start();
    await t.controller.setAutoCheck(true);
    expect(t.writes).toEqual([{ autoCheck: true }]);
    expect(t.controller.getState().autoCheck).toBe(true);
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.calls).toEqual(["check"]);
    await t.controller.setAutoCheck(false);
    await vi.advanceTimersByTimeAsync(3 * CHECK_INTERVAL_MS);
    expect(t.calls).toEqual(["check"]);
    expect(t.controller.getState().autoCheck).toBe(false);
  });

  it("checks nothing after it was disposed", async () => {
    const t = setup();
    t.controller.start();
    t.controller.dispose();
    await vi.advanceTimersByTimeAsync(2 * CHECK_INTERVAL_MS);
    expect(t.calls).toEqual([]);
  });

  it("does not check by itself while something is offered or downloaded", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.controller.getState().phase).toBe("available");
    await vi.advanceTimersByTimeAsync(3 * CHECK_INTERVAL_MS);
    expect(t.calls).toEqual(["check"]);
  });
});

describe("the update controller: the daily timer while something is in hand", () => {
  it("waits a day again, not 30 seconds, when the day is over and an offer is still open", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.controller.getState().phase).toBe("available");
    // The day is over and the offer is still open: the timer skips its check.
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    expect(t.calls).toEqual(["check"]);
    // The person lets the offer go a little later. The next check is a day
    // after the skipped one; it is not the 30 seconds after it that a timer
    // computed from the old check time would give.
    await vi.advanceTimersByTimeAsync(31_000);
    await t.controller.dismiss("later");
    await vi.advanceTimersByTimeAsync(HOUR);
    expect(t.calls).toEqual(["check"]);
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS - HOUR - 31_000 - 1);
    expect(t.calls).toEqual(["check"]);
    await vi.advanceTimersByTimeAsync(1);
    expect(t.calls).toEqual(["check", "check"]);
  });

  it("does not set a timer every 30 seconds for as long as a download waits for the person", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    await t.controller.download();
    await t.controller.dismiss("later");
    expect(t.controller.getState().phase).toBe("downloaded");
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    const arms = vi.spyOn(globalThis, "setTimeout");
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    expect(arms).not.toHaveBeenCalled();
    arms.mockRestore();
  });
});

describe("the update controller: what a check finds", () => {
  it("offers a newer version after an automatic check", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.controller.getState()).toMatchObject({
      phase: "available",
      version: "1.0.1",
      dialog: "available",
      errorCode: "",
    });
    expect(t.states.at(-1)).toEqual(t.controller.getState());
  });

  it("says nothing when an automatic check finds nothing or fails", async () => {
    const t = setup();
    t.answers.push(
      null,
      Object.assign(new Error("offline"), { code: "ERR_X_Y" }),
    );
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
    });
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
      errorCode: "ERR_X_Y",
    });
    expect(t.states.every((state) => state.dialog === "none")).toBe(true);
    // A failed check counts as a check: the next one is a day away.
    expect(t.writes).toHaveLength(2);
  });

  it("tells the result of a check the person asked for", async () => {
    const t = setup();
    t.answers.push(null);
    expect(await t.controller.check(true)).toMatchObject({
      phase: "idle",
      dialog: "upToDate",
    });
    await t.controller.dismiss("later");
    expect(t.controller.getState().dialog).toBe("none");

    t.answers.push(Object.assign(new Error("no route"), { code: "ENOTFOUND" }));
    expect(await t.controller.check(true)).toMatchObject({
      phase: "idle",
      dialog: "checkFailed",
      errorCode: "ENOTFOUND",
    });
    await t.controller.dismiss("later");

    t.answers.push("1.2.0");
    expect(await t.controller.check(true)).toMatchObject({
      phase: "available",
      version: "1.2.0",
      dialog: "available",
    });
  });

  it("shows a check that is running as such, and runs one at a time", async () => {
    const t = setup();
    let release: (version: string) => void = () => {};
    t.updater.check = () =>
      new Promise<string | null>((resolve) => {
        release = resolve;
      });
    const first = t.controller.check(true);
    const second = t.controller.check(true);
    expect(t.controller.getState().phase).toBe("checking");
    release("1.0.2");
    await Promise.all([first, second]);
    expect(t.controller.getState()).toMatchObject({
      phase: "available",
      version: "1.0.2",
    });
  });

  it("shows the result when a person asks while an automatic check runs", async () => {
    const t = setup();
    let release: (version: string | null) => void = () => {};
    t.updater.check = () =>
      new Promise<string | null>((resolve) => {
        release = resolve;
      });
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.controller.getState().phase).toBe("checking");
    const asked = t.controller.check(true);
    release(null);
    await asked;
    expect(t.controller.getState().dialog).toBe("upToDate");
  });

  it("gives up on a check that never answers", async () => {
    const t = setup();
    t.answers.push("hang");
    const asked = t.controller.check(true);
    await vi.advanceTimersByTimeAsync(CHECK_TIMEOUT_MS);
    expect(await asked).toMatchObject({
      phase: "idle",
      dialog: "checkFailed",
      errorCode: "ERR_UPDATE_TIMEOUT",
    });
  });

  it("refuses a version the feed spells in a way a release is not", async () => {
    const t = setup();
    for (const bad of ["<b>x</b>", "1.0", "v1.0.1", "1.0.1/../x", ""]) {
      t.answers.push(bad);
      expect(await t.controller.check(true)).toMatchObject({
        phase: "idle",
        dialog: "checkFailed",
        version: "",
      });
      await t.controller.dismiss("later");
    }
  });

  it("does not offer the version that is installed or an older one", async () => {
    const t = setup({ version: "1.0.1" });
    // electron-updater does not report these as updates; the controller
    // holds its own line against a feed that does.
    for (const same of ["1.0.1", "1.0.0", "0.9.0"]) {
      t.answers.push(same);
      expect(await t.controller.check(true)).toMatchObject({
        phase: "idle",
        dialog: "upToDate",
      });
      await t.controller.dismiss("later");
    }
  });
});

describe("the update controller: one look per check, release candidates too (ADR-0078)", () => {
  // The download site holds releases only, and every build reads the same
  // latest.yml: a release candidate is offered the release when it is higher,
  // and never a newer candidate.
  const missing = () =>
    Object.assign(new Error("no latest.yml"), {
      code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND",
    });

  it("looks once, and offers the release to a release candidate", async () => {
    const t = setup({ version: "1.0.3-rc.1" });
    t.answers.push("1.0.3");
    expect(await t.controller.check(true)).toMatchObject({
      phase: "available",
      version: "1.0.3",
      dialog: "available",
    });
    expect(t.calls).toEqual(["check"]);
  });

  it("is up to date when the release is below the candidate, or nothing is found", async () => {
    const t = setup({ version: "1.0.3-rc.1" });
    // 1.0.3-rc.1 is ahead of 1.0.2; the controller holds this line itself.
    t.answers.push("1.0.2");
    expect(await t.controller.check(true)).toMatchObject({
      phase: "idle",
      dialog: "upToDate",
      errorCode: "",
    });
    await t.controller.dismiss("later");
    t.answers.push(null);
    expect(await t.controller.check(true)).toMatchObject({
      phase: "idle",
      dialog: "upToDate",
    });
    expect(t.calls).toEqual(["check", "check"]);
  });

  it("is one failed check when the look fails, whoever the application is", async () => {
    for (const version of ["1.0.2", "1.0.3-rc.1"]) {
      const t = setup({ version });
      t.answers.push(missing());
      expect(await t.controller.check(true)).toMatchObject({
        phase: "idle",
        dialog: "checkFailed",
        errorCode: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND",
        version: "",
      });
      expect(t.calls).toEqual(["check"]);
    }
  });

  it("is silent about that failure in an automatic check, and asks again in a day", async () => {
    const t = setup({ version: "1.0.3-rc.1" });
    t.answers.push(missing());
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.calls).toEqual(["check"]);
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
      errorCode: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND",
    });
    expect(t.states.every((state) => state.dialog === "none")).toBe(true);
    expect(t.writes.at(-1)).toEqual({ lastCheck: START + FIRST_CHECK_MS });
  });

  it("is silent about a skipped release in an automatic check, and tells the person who asks", async () => {
    const t = setup({
      version: "1.0.3-rc.1",
      settings: { skippedVersion: "1.0.3" },
    });
    t.answers.push("1.0.3", "1.0.3");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(FIRST_CHECK_MS);
    expect(t.calls).toEqual(["check"]);
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
    });
    expect(await t.controller.check(true)).toMatchObject({
      phase: "available",
      version: "1.0.3",
      dialog: "available",
    });
    expect(t.calls).toEqual(["check", "check"]);
  });
});

describe("the update controller: 以后再说 and 跳过这个版本", () => {
  it("forgets the offer on 以后再说 and offers again at the next check", async () => {
    const t = setup();
    t.answers.push("1.0.1", "1.0.1");
    await t.controller.check(true);
    await t.controller.dismiss("later");
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      version: "",
      dialog: "none",
    });
    expect(t.writes.filter((w) => "skippedVersion" in w)).toEqual([]);
    await t.controller.check(true);
    expect(t.controller.getState().dialog).toBe("available");
  });

  it("silences the version for automatic checks only", async () => {
    const t = setup();
    t.answers.push("1.0.1", "1.0.1", "1.0.2", "1.0.1");
    await t.controller.check(true);
    await t.controller.dismiss("skip");
    expect(t.writes.at(-1)).toEqual({ skippedVersion: "1.0.1" });
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
    });

    // The automatic check, a day after the one the person made, does not
    // speak of it.
    t.controller.start();
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    expect(t.calls).toEqual(["check", "check"]);
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      dialog: "none",
      version: "",
    });

    // A newer version is another matter.
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
    expect(t.controller.getState()).toMatchObject({
      phase: "available",
      version: "1.0.2",
      dialog: "available",
    });
    await t.controller.dismiss("later");

    // And the person who asks is always told.
    await t.controller.check(true);
    expect(t.controller.getState()).toMatchObject({
      version: "1.0.1",
      dialog: "available",
    });
  });

  it("drops the skipped version once the application has caught up with it", () => {
    const behind = setup({
      version: "1.0.0",
      settings: { skippedVersion: "1.0.1" },
    });
    behind.controller.start();
    expect(behind.writes).toEqual([]);
    const caughtUp = setup({
      version: "1.0.1",
      settings: { skippedVersion: "1.0.1" },
    });
    caughtUp.controller.start();
    expect(caughtUp.writes).toEqual([{ skippedVersion: "" }]);
    const past = setup({
      version: "1.1.0",
      settings: { skippedVersion: "1.0.1" },
    });
    past.controller.start();
    expect(past.writes).toEqual([{ skippedVersion: "" }]);
  });

  it("ignores 跳过 for anything that is not an offer", async () => {
    const t = setup();
    t.answers.push(null);
    await t.controller.check(true);
    await t.controller.dismiss("skip");
    expect(t.writes.filter((w) => "skippedVersion" in w)).toEqual([]);
    expect(t.controller.getState().dialog).toBe("none");
  });
});

describe("the update controller: the download", () => {
  async function offered() {
    const t = setup();
    t.answers.push("1.0.1");
    await t.controller.check(true);
    return t;
  }

  it("shows the progress, then offers the install", async () => {
    const t = await offered();
    t.onDownload(async (progress) => {
      await Promise.resolve();
      progress(10.4);
      progress(10.9);
      progress(55);
      progress(100);
    });
    const done = t.controller.download();
    expect(t.controller.getState()).toMatchObject({
      phase: "downloading",
      dialog: "downloading",
      percent: 0,
    });
    await done;
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "downloaded",
      version: "1.0.1",
      percent: 100,
    });
    // One state per whole percent that changed.
    const percents = t.states
      .filter((state) => state.phase === "downloading")
      .map((state) => state.percent);
    expect(percents).toEqual([0, 10, 55, 100]);
  });

  it("keeps the numbers inside 0 to 100", async () => {
    const t = await offered();
    let progress: (percent: number) => void = () => {};
    let finish: () => void = () => {};
    t.onDownload(
      (report) =>
        new Promise<void>((resolve) => {
          progress = report;
          finish = resolve;
        }),
    );
    const done = t.controller.download();
    progress(-4);
    expect(t.controller.getState().percent).toBe(0);
    progress(Number.NaN);
    expect(t.controller.getState().percent).toBe(0);
    progress(250);
    expect(t.controller.getState().percent).toBe(100);
    finish();
    await done;
  });

  it("goes on in the background when the dialog is dismissed, and says when it is done", async () => {
    const t = await offered();
    let finish: () => void = () => {};
    t.onDownload(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const done = t.controller.download();
    await t.controller.dismiss("later");
    expect(t.controller.getState()).toMatchObject({
      phase: "downloading",
      dialog: "none",
    });
    finish();
    await done;
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "downloaded",
    });
  });

  it("says so when the download fails, and forgets the offer", async () => {
    const t = await offered();
    t.onDownload(async () => {
      throw Object.assign(new Error("sha512 checksum mismatch"), {
        code: "ERR_CHECKSUM_MISMATCH",
      });
    });
    await t.controller.download();
    expect(t.controller.getState()).toMatchObject({
      phase: "idle",
      version: "",
      dialog: "downloadFailed",
      errorCode: "ERR_CHECKSUM_MISMATCH",
    });
    await t.controller.dismiss("later");
    expect(t.controller.getState().dialog).toBe("none");
  });

  it("downloads only what was offered, and once", async () => {
    const t = setup();
    await t.controller.download();
    expect(t.calls).toEqual([]);
    t.answers.push("1.0.1");
    await t.controller.check(true);
    await Promise.all([t.controller.download(), t.controller.download()]);
    expect(t.calls.filter((call) => call === "download")).toHaveLength(1);
  });

  it("keeps a downloaded update for the next press of the button, without asking the network", async () => {
    const t = await offered();
    await t.controller.download();
    await t.controller.dismiss("later");
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "none",
    });
    const before = t.calls.length;
    await t.controller.check(true);
    expect(t.calls).toHaveLength(before);
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "downloaded",
    });
    // An automatic check leaves it alone.
    await t.controller.dismiss("later");
    t.controller.start();
    await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS * 2);
    expect(t.calls).toHaveLength(before);
    expect(t.controller.getState().dialog).toBe("none");
  });
});

describe("the update controller: the install (PKG-10, ADR-0073)", () => {
  async function downloaded() {
    const t = setup();
    t.answers.push("1.0.1");
    await t.controller.check(true);
    await t.controller.download();
    return t;
  }

  it("lets the saves finish, then installs silently and starts padmate again, once", async () => {
    const t = await downloaded();
    const result = await t.controller.install();
    expect(result).toEqual({ dryRun: false });
    expect(t.guard).toEqual(["flush", "quitAndInstall"]);
    expect(t.calls.filter((call) => call.startsWith("quitAndInstall"))).toEqual(
      ["quitAndInstall:true,true"],
    );
    expect(t.controller.getState()).toMatchObject({
      phase: "installing",
      dialog: "installing",
    });
  });

  it("waits for the saves before it calls the installer", async () => {
    let finish: () => void = () => {};
    const t = setup({
      flush: () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    });
    t.answers.push("1.0.1");
    await t.controller.check(true);
    await t.controller.download();
    const installing = t.controller.install();
    await settle();
    expect(t.guard).toEqual([]);
    expect(t.calls.some((call) => call.startsWith("quitAndInstall"))).toBe(
      false,
    );
    finish();
    await installing;
    expect(t.guard).toEqual(["quitAndInstall"]);
  });

  it("does not install twice", async () => {
    const t = await downloaded();
    await t.controller.install();
    await expect(t.controller.install()).rejects.toThrow("not ready");
    expect(
      t.calls.filter((call) => call.startsWith("quitAndInstall")),
    ).toHaveLength(1);
  });

  it("installs nothing that was not downloaded", async () => {
    const t = setup();
    await expect(t.controller.install()).rejects.toThrow("not ready");
    t.answers.push("1.0.1");
    await t.controller.check(true);
    await expect(t.controller.install()).rejects.toThrow("not ready");
    expect(t.guard).toEqual([]);
  });

  it("takes the saves back and says so when the installer could not be started", async () => {
    const t = await downloaded();
    t.installThrows(new Error("spawn failed"));
    await t.controller.install();
    expect(t.guard).toEqual(["flush", "quitAndInstall", "rearm"]);
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "installFailed",
    });
    await t.controller.dismiss("later");
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "none",
    });
  });

  it("takes the saves back when the updater reports an error after the call", async () => {
    const t = await downloaded();
    await t.controller.install();
    t.fail(Object.assign(new Error("EACCES"), { code: "EACCES" }));
    expect(t.guard).toEqual(["flush", "quitAndInstall", "rearm"]);
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "installFailed",
      errorCode: "EACCES",
    });
  });

  it("ignores an updater error that has nothing to do with an install", async () => {
    const t = await downloaded();
    t.fail(new Error("late download error"));
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "downloaded",
    });
    expect(t.guard).toEqual([]);
  });

  it("goes on with the install when the saves could not be awaited", async () => {
    const t = setup({
      flush: async () => {
        throw new Error("window is gone");
      },
    });
    t.answers.push("1.0.1");
    await t.controller.check(true);
    await t.controller.download();
    await t.controller.install();
    expect(
      t.calls.filter((call) => call.startsWith("quitAndInstall")),
    ).toHaveLength(1);
  });

  it("records the install in the test mode of the installed application and carries on", async () => {
    const t = await downloaded();
    t.installReturns({ dryRun: true });
    const result = await t.controller.install();
    expect(result).toEqual({ dryRun: true });
    expect(t.controller.getState()).toMatchObject({
      phase: "downloaded",
      dialog: "none",
    });
    // The window stays; closing it waits for its saves again.
    expect(t.guard).toEqual(["flush", "quitAndInstall", "rearm"]);
  });
});

describe("the update controller: the manual download", () => {
  it("opens the download site, whether a version is on offer or not", async () => {
    const t = setup();
    t.answers.push("1.0.1", new Error("offline"));
    await t.controller.check(true);
    expect(t.controller.getState().phase).toBe("available");
    await t.controller.openManualDownload();
    expect(t.opened).toEqual(["https://padmate.download/"]);
    await t.controller.dismiss("later");
    await t.controller.check(true);
    expect(t.controller.getState().dialog).toBe("checkFailed");
    await t.controller.openManualDownload();
    expect(t.opened).toEqual([UPDATE_URL, UPDATE_URL]);
  });

  it("opens only the constant, whatever the feed said", async () => {
    const t = setup();
    t.answers.push("1.0.1?x=1");
    await t.controller.check(true);
    await t.controller.openManualDownload();
    expect(t.opened).toEqual([UPDATE_URL]);
  });
});

describe("the update controller: state pushed to the window", () => {
  it("pushes every change once and nothing when nothing changed", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    await t.controller.check(true);
    const count = t.states.length;
    expect(count).toBeGreaterThan(0);
    // Asking for the offer again changes nothing.
    await t.controller.check(true);
    expect(t.states.length).toBe(count);
    expect(new Set(t.states.map((state) => JSON.stringify(state))).size).toBe(
      t.states.length,
    );
  });

  it("is plain data", async () => {
    const t = setup();
    t.answers.push("1.0.1");
    await t.controller.check(true);
    const state = t.controller.getState();
    expect(JSON.parse(JSON.stringify(state))).toEqual(state);
    expect(Object.keys(state).sort()).toEqual([
      "autoCheck",
      "dialog",
      "errorCode",
      "percent",
      "phase",
      "supported",
      "version",
    ]);
  });
});

describe("the updater of a build that cannot update", () => {
  it("fails every call, so that nothing reaches the network", async () => {
    await expect(inertUpdater.check()).rejects.toThrow("cannot update");
    await expect(inertUpdater.download(() => {})).rejects.toThrow(
      "cannot update",
    );
    expect(() => inertUpdater.quitAndInstall(true, true)).toThrow(
      "cannot update",
    );
    expect(() => inertUpdater.onInstallError(() => {})).not.toThrow();
  });
});
