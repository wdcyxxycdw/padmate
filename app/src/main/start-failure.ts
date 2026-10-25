import { open, rm } from "node:fs/promises";
import { win32 } from "node:path";
import type {
  ConnectionState,
  StartFailure,
  StartupProblem,
  StartupReport,
  StartupReportCause,
  StartupReportKind,
} from "../shared/api";
import { startFailureText } from "../shared/start-failure-text";
import type { EditorHandler } from "./library-handlers";

// Why the mapping service did not start (UI-45, ADR-0056): padmated writes
// --startup-report FILE when live mode does not start, and Main reads it
// once padmated has exited. The report is data from another program: Main
// keeps only the known fields, bounded, and uses its paths for display and
// for the folder rule of startFolderTarget, nothing else.

/** A larger report is not read: padmated's has at most 20 errors. */
export const maxReportBytes = 256 * 1024;
const maxProblems = 20;
const maxPath = 32767;
const maxMessage = 2000;
const maxPointer = 1024;

const kinds: readonly StartupReportKind[] = [
  "usage",
  "instance",
  "library",
  "config",
  "state",
  "profile",
  "log",
  "ipc",
  "sdl",
  "output",
  "internal",
];
const causes: readonly StartupReportCause[] = [
  "invalid",
  "missing",
  "denied",
  "refused",
  "unavailable",
  "other",
];

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

/** One line of display text: control characters become spaces. */
function text(value: unknown, max: number): string {
  if (typeof value !== "string") return "";
  const line = value.replace(/\p{Cc}/gu, " ");
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

function position(value: unknown): number {
  return Number.isSafeInteger(value) && (value as number) > 0
    ? (value as number)
    : 0;
}

function problem(value: unknown): StartupProblem | undefined {
  const item = record(value);
  if (!item) return undefined;
  return {
    line: position(item.line),
    col: position(item.col),
    pointer: text(item.pointer, maxPointer),
    message: text(item.message, maxMessage),
  };
}

/** The report in source, or undefined when it is not one of version 1. */
export function parseStartupReport(source: string): StartupReport | undefined {
  let value: unknown;
  try {
    value = JSON.parse(source);
  } catch {
    return undefined;
  }
  const report = record(value);
  if (report?.version !== 1) return undefined;
  const errors = Array.isArray(report.errors)
    ? report.errors
        .slice(0, maxProblems)
        .map(problem)
        .filter((item): item is StartupProblem => !!item)
    : [];
  const first = problem(report) as StartupProblem;
  return {
    kind: kinds.includes(report.kind as StartupReportKind)
      ? (report.kind as StartupReportKind)
      : "internal",
    cause: causes.includes(report.cause as StartupReportCause)
      ? (report.cause as StartupReportCause)
      : "other",
    file: text(report.file, maxPath),
    ...first,
    dataDir: text(report.dataDir, maxPath),
    errors,
  };
}

/**
 * Reads the report at path and removes it; undefined when there is none,
 * or it is too large or not a report.
 */
export async function readStartupReport(
  path: string,
): Promise<StartupReport | undefined> {
  let handle: Awaited<ReturnType<typeof open>> | undefined;
  try {
    handle = await open(path, "r");
    const buffer = Buffer.alloc(maxReportBytes + 1);
    let size = 0;
    while (size < buffer.length) {
      const { bytesRead } = await handle.read(
        buffer,
        size,
        buffer.length - size,
        size,
      );
      if (bytesRead === 0) break;
      size += bytesRead;
    }
    if (size > maxReportBytes) return undefined;
    return parseStartupReport(buffer.subarray(0, size).toString("utf8"));
  } catch {
    return undefined;
  } finally {
    await handle?.close().catch(() => {});
    await rm(path, { force: true }).catch(() => {});
  }
}

/** How a daemon the UI started ended, before the UI connected to it. */
export type DaemonExit =
  | { spawned: false }
  | { spawned: true; code: number | null; report?: StartupReport };

/**
 * Whether exit means the start failed. An exit 0 without a report is a
 * request the running instance took: that one answers instead.
 */
export function failedStart(exit: DaemonExit): boolean {
  return !exit.spawned || exit.code !== 0 || !!exit.report;
}

/** The StartFailure that exit describes. */
export function exitFailure(exit: DaemonExit): StartFailure {
  if (!exit.spawned) return { reason: "spawn" };
  if (exit.report) return { reason: "report", report: exit.report };
  return { reason: "exit", code: exit.code };
}

export type FolderTarget = { show: string } | { open: string };

function inside(path: string, folder: string): boolean {
  const relative = win32.relative(win32.resolve(folder), win32.resolve(path));
  return (
    relative === "" ||
    (relative !== ".." &&
      !relative.startsWith("..\\") &&
      !win32.isAbsolute(relative))
  );
}

/**
 * What 打开文件夹 opens: the file at fault, selected in its folder, when it
 * is the data folder or inside it; otherwise the data folder. dataDir is
 * the folder this UI starts the daemon with, never one from the report.
 */
export function startFolderTarget(
  failure: StartFailure | undefined,
  dataDir: string,
): FolderTarget {
  const file = failure?.reason === "report" ? failure.report.file : "";
  if (file && win32.isAbsolute(file) && inside(file, dataDir))
    return { show: win32.resolve(file) };
  return { open: dataDir };
}

export interface StartFailureOptions {
  state: () => ConnectionState | undefined;
  /** Opens startFolderTarget's target; rejects when it cannot. */
  open: () => Promise<void>;
  /** Starts the daemon again: DaemonManager.connect. */
  retry: () => Promise<void> | undefined;
  quit: () => void;
}

/** The current failure; throws unless the start failed or is retried. */
export function currentFailure(
  state: ConnectionState | undefined,
): StartFailure {
  if (
    !state?.failure ||
    (state.phase !== "failed" && state.phase !== "connecting")
  )
    throw new Error("The mapping service did not fail to start");
  return state.failure;
}

/**
 * Main's dialog when the page cannot say why (UI-45). Main does not know
 * the page's language, so it says it in both, as its other boxes do.
 */
export function failureDialogText(failure: StartFailure): {
  message: string;
  detail: string;
  buttons: string[];
} {
  const zh = startFailureText(failure, "zh-CN");
  const en = startFailureText(failure, "en");
  const more = zh.more.length > 5 ? [...zh.more.slice(0, 5), "…"] : zh.more;
  return {
    message: `${zh.title}\n${en.title}`,
    detail: [
      zh.summary,
      ...more,
      "",
      en.summary,
      ...(zh.path ? ["", zh.path] : []),
    ].join("\n"),
    buttons: ["打开文件夹 / Open folder", "重试 / Retry", "退出 / Quit"],
  };
}

export type PageRecovery = "dialog" | "reload" | "unavailable" | "none";

/**
 * What Main does on a new connection state when the page never loaded
 * (broken) or its renderer went away while the start had failed (gone):
 * a failed start is the dialog's; a connection, or a lost one, needs the
 * page, so a page that went away loads again and one that never loaded
 * cannot show (UI-45).
 */
export function pageRecovery(
  phase: ConnectionState["phase"],
  page: { broken: boolean; gone: boolean },
): PageRecovery {
  if ((!page.broken && !page.gone) || phase === "connecting") return "none";
  if (phase === "failed") return "dialog";
  return page.broken ? "unavailable" : "reload";
}

/** Why the window cannot show; developers get the commands that fix it. */
export function uiFailureText(packaged: boolean): string {
  return packaged
    ? "无法显示 padmate 窗口。请重新安装 padmate。\nCould not show the padmate window. Reinstall padmate."
    : "无法显示界面。请运行 task app:install 和 task app:prepare 后重试。\nCould not show the UI. Run task app:install and task app:prepare, then retry.";
}

/** The start-failure page's buttons (UI-45); none takes an argument. */
export function startFailureHandlers(
  options: StartFailureOptions,
): Record<string, EditorHandler> {
  return {
    startFolder: {
      count: 0,
      run: async () => {
        currentFailure(options.state());
        await options.open();
      },
    },
    startRetry: {
      count: 0,
      run: async () => {
        currentFailure(options.state());
        await options.retry();
      },
    },
    quit: {
      count: 0,
      run: () => {
        currentFailure(options.state());
        options.quit();
      },
    },
  };
}
