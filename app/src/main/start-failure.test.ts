import { mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ConnectionState, StartFailure } from "../shared/api";
import {
  exitFailure,
  failedStart,
  failureDialogText,
  maxReportBytes,
  pageRecovery,
  parseStartupReport,
  readStartupReport,
  startFailureHandlers,
  startFolderTarget,
  uiFailureText,
} from "./start-failure";

// The owner's broken config.json (2026-10-12, F17), as padmated reports it.
const owner = {
  version: 1,
  kind: "config",
  cause: "invalid",
  file: "C:\\Users\\me\\AppData\\Roaming\\padmate\\config.json",
  line: 14,
  col: 20,
  pointer: "/safety/panic/standard/buttons",
  message: "minItems: got 1, want 2",
  dataDir: "C:\\Users\\me\\AppData\\Roaming\\padmate",
  errors: [
    {
      line: 14,
      col: 20,
      pointer: "/safety/panic/standard/buttons",
      message: "minItems: got 1, want 2",
    },
  ],
};

describe("startup report", () => {
  it("keeps padmated's report as it is", () => {
    const { version: _version, ...report } = owner;
    expect(parseStartupReport(JSON.stringify(owner))).toEqual(report);
  });

  it("drops unknown fields and replaces unknown or wrong values", () => {
    const parsed = parseStartupReport(
      JSON.stringify({
        version: 1,
        kind: "telemetry",
        cause: 7,
        file: ["C:\\x"],
        line: -3,
        col: 1.5,
        pointer: null,
        message: "line one\nline two\u0000",
        dataDir: "C:\\data",
        token: "secret",
        errors: [
          { line: 2, col: 3, pointer: "/a", message: "m", extra: true },
          "not an error",
          null,
        ],
      }),
    );
    expect(parsed).toEqual({
      kind: "internal",
      cause: "other",
      file: "",
      line: 0,
      col: 0,
      pointer: "",
      message: "line one line two ",
      dataDir: "C:\\data",
      errors: [{ line: 2, col: 3, pointer: "/a", message: "m" }],
    });
    expect(JSON.stringify(parsed)).not.toContain("secret");
  });

  it.each([
    ["not JSON", "panic: runtime error"],
    ["truncated", JSON.stringify(owner).slice(0, 40)],
    ["another version", JSON.stringify({ ...owner, version: 2 })],
    ["an array", "[1]"],
    ["null", "null"],
    ["a string", '"config"'],
  ])("is no report when it is %s", (_name, text) => {
    expect(parseStartupReport(text)).toBeUndefined();
  });

  it("bounds the errors and every text", () => {
    const parsed = parseStartupReport(
      JSON.stringify({
        ...owner,
        message: "x".repeat(5000),
        errors: Array.from({ length: 50 }, (_, i) => ({
          line: i + 1,
          col: 1,
          pointer: "",
          message: "e",
        })),
      }),
    );
    expect(parsed?.errors).toHaveLength(20);
    expect(parsed?.message).toHaveLength(2000);
    expect(parsed?.message.endsWith("…")).toBe(true);
  });
});

describe("reading the report", () => {
  let folder = "";
  afterEach(async () => {
    if (folder) await rm(folder, { recursive: true, force: true });
    folder = "";
  });

  it("reads the report once and removes it", async () => {
    folder = await mkdtemp(join(tmpdir(), "padmate-report-"));
    const path = join(folder, "report.json");
    await writeFile(path, `${JSON.stringify(owner)}\n`);
    await expect(readStartupReport(path)).resolves.toMatchObject({
      kind: "config",
      line: 14,
    });
    expect(await readdir(folder)).toEqual([]);
    await expect(readStartupReport(path)).resolves.toBeUndefined();
  });

  it("does not read a huge file, and removes it too", async () => {
    folder = await mkdtemp(join(tmpdir(), "padmate-report-"));
    const path = join(folder, "report.json");
    await writeFile(
      path,
      JSON.stringify({ ...owner, message: "x".repeat(maxReportBytes) }),
    );
    await expect(readStartupReport(path)).resolves.toBeUndefined();
    expect(await readdir(folder)).toEqual([]);
  });

  it("is no report when the file is not JSON", async () => {
    folder = await mkdtemp(join(tmpdir(), "padmate-report-"));
    const path = join(folder, "report.json");
    await writeFile(path, "\u0000\u0001garbage");
    await expect(readStartupReport(path)).resolves.toBeUndefined();
  });
});

describe("how the started daemon ended", () => {
  const report = parseStartupReport(JSON.stringify(owner));
  it.each([
    [{ spawned: false } as const, true, { reason: "spawn" }],
    [
      { spawned: true, code: 3, report } as const,
      true,
      { reason: "report", report },
    ],
    [{ spawned: true, code: 3 } as const, true, { reason: "exit", code: 3 }],
    [
      { spawned: true, code: null } as const,
      true,
      { reason: "exit", code: null },
    ],
    // The running instance took the request: it answers instead.
    [{ spawned: true, code: 0 } as const, false, undefined],
  ])("%j", (exit, failed, failure) => {
    expect(failedStart(exit)).toBe(failed);
    if (failure) expect(exitFailure(exit)).toEqual(failure);
  });
});

describe("打开文件夹", () => {
  const dataDir = "C:\\Users\\me\\AppData\\Roaming\\padmate";
  const failure = (file: string): StartFailure => ({
    reason: "report",
    report: {
      ...(parseStartupReport(JSON.stringify(owner)) as NonNullable<
        ReturnType<typeof parseStartupReport>
      >),
      file,
    },
  });

  it("shows a file of the data folder, also in another case", () => {
    expect(
      startFolderTarget(failure(`${dataDir}\\config.json`), dataDir),
    ).toEqual({ show: `${dataDir}\\config.json` });
    expect(
      startFolderTarget(
        failure(
          "c:\\users\\ME\\appdata\\roaming\\padmate\\profiles\\paint\\profile.json",
        ),
        dataDir,
      ),
    ).toEqual({
      show: "c:\\users\\ME\\appdata\\roaming\\padmate\\profiles\\paint\\profile.json",
    });
    // An unreadable data folder: shown in the folder that has it.
    expect(startFolderTarget(failure(dataDir), dataDir)).toEqual({
      show: dataDir,
    });
  });

  it.each([
    "C:\\Windows\\System32\\cmd.exe",
    `${dataDir}\\..\\evil.exe`,
    `${dataDir}-other\\config.json`,
    "config.json",
    "\\\\server\\share\\config.json",
    "",
  ])("opens only the data folder for %j", (file) => {
    expect(startFolderTarget(failure(file), dataDir)).toEqual({
      open: dataDir,
    });
  });

  it("opens the data folder without a report", () => {
    expect(startFolderTarget({ reason: "timeout" }, dataDir)).toEqual({
      open: dataDir,
    });
    expect(startFolderTarget(undefined, dataDir)).toEqual({ open: dataDir });
  });
});

describe("start-failure handlers", () => {
  const failed: ConnectionState = {
    phase: "failed",
    failure: { reason: "timeout" },
  };
  function fixture(state: ConnectionState) {
    const options = {
      state: () => state,
      open: vi.fn(async () => {}),
      retry: vi.fn(async () => {}),
      quit: vi.fn(),
    };
    return { options, handlers: startFailureHandlers(options) };
  }

  it("declares the renderer argument counts", () => {
    const { handlers } = fixture(failed);
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({ startFolder: 0, startRetry: 0, quit: 0 });
  });

  it("opens, retries and quits while the start failed or is retried", async () => {
    for (const state of [
      failed,
      { phase: "connecting", failure: { reason: "timeout" } } as const,
    ]) {
      const { options, handlers } = fixture(state);
      await handlers.startFolder.run();
      await handlers.startRetry.run();
      await handlers.quit.run();
      expect(options.open).toHaveBeenCalledOnce();
      expect(options.retry).toHaveBeenCalledOnce();
      expect(options.quit).toHaveBeenCalledOnce();
    }
  });

  it.each([
    { phase: "connected" },
    { phase: "connecting" },
    { phase: "disconnected", message: "lost" },
    { phase: "failed" },
  ] as ConnectionState[])("refuses every button when %j", async (state) => {
    const { options, handlers } = fixture(state);
    await expect(handlers.startFolder.run()).rejects.toThrow();
    await expect(handlers.startRetry.run()).rejects.toThrow();
    expect(() => handlers.quit.run()).toThrow();
    expect(options.open).not.toHaveBeenCalled();
    expect(options.retry).not.toHaveBeenCalled();
    expect(options.quit).not.toHaveBeenCalled();
  });
});

describe("Main's own boxes", () => {
  it("says the start failure in both languages, with its file", () => {
    const report = parseStartupReport(JSON.stringify(owner));
    const text = failureDialogText({
      reason: "report",
      report: report as NonNullable<typeof report>,
    });
    expect(text.message).toBe(
      "映射服务未能启动\nThe mapping service did not start",
    );
    expect(text.detail).toBe(
      [
        "设置文件 config.json 第 14 行第 20 列：紧急暂停至少需要 2 个按键。",
        "",
        "Error in the settings file config.json, line 14, column 20: the emergency pause needs at least 2 buttons.",
        "",
        owner.file,
      ].join("\n"),
    );
    expect(text.buttons).toEqual([
      "打开文件夹 / Open folder",
      "重试 / Retry",
      "退出 / Quit",
    ]);
  });

  it("keeps developer commands out of the installed app", () => {
    expect(uiFailureText(true)).not.toContain("task");
    expect(uiFailureText(true)).toContain("重新安装");
    expect(uiFailureText(false)).toContain("task app:install");
  });
});

describe("a page that cannot show", () => {
  it.each([
    // The renderer went away while the start had failed or was retried.
    ["connecting", false, true, "none"],
    ["failed", false, true, "dialog"],
    ["connected", false, true, "reload"],
    // A retry after an earlier connection that got no answer: the page
    // shows the lost connection with its 重新连接.
    ["disconnected", false, true, "reload"],
    // The page never loaded.
    ["connecting", true, false, "none"],
    ["failed", true, false, "dialog"],
    ["connected", true, false, "unavailable"],
    ["disconnected", true, false, "unavailable"],
    // The page is there: it shows every state itself.
    ["failed", false, false, "none"],
    ["disconnected", false, false, "none"],
  ] as const)("%s, broken %s, gone %s: %s", (phase, broken, gone, action) => {
    expect(pageRecovery(phase, { broken, gone })).toBe(action);
  });
});
