import type {
  StartFailure,
  StartupProblem,
  StartupReport,
  StartupReportKind,
} from "./api";

// What the start-failure page (UI-45) says, and Main's dialog when the
// page cannot show: which file, where in it, and what is wrong, in plain
// words. A message this file does not know stays padmated's English after
// a Chinese lead.

export type FailureLanguage = "zh-CN" | "en";

export interface StartFailureText {
  title: string;
  /** One sentence: what stopped the start. */
  summary: string;
  /** The file or folder at fault, in full; "" when none. */
  path: string;
  /** The file's further errors, one line each. */
  more: string[];
}

interface Phrase {
  zh: string;
  en: string;
}

/** The files and folders a report can name, and what padmated does with them. */
const files: Partial<Record<StartupReportKind, Phrase & { verb: Phrase }>> = {
  config: {
    zh: "设置文件 config.json",
    en: "the settings file config.json",
    verb: { zh: "读取", en: "read" },
  },
  state: {
    zh: "状态文件 state.json",
    en: "the state file state.json",
    verb: { zh: "读取", en: "read" },
  },
  profile: {
    zh: "配置文件",
    en: "the profile file",
    verb: { zh: "读取", en: "read" },
  },
  library: {
    zh: "数据文件夹",
    en: "the data folder",
    verb: { zh: "打开", en: "open" },
  },
  log: {
    zh: "日志文件",
    en: "the log file",
    verb: { zh: "写入", en: "write" },
  },
};

/**
 * Ends a sentence: 。 in zh, a period in en. padmated's own English at the
 * end of a zh sentence (raw) is left as it is.
 */
function finish(text: string, zh: boolean, raw = false): string {
  if (zh) return raw || /[。？！]$/.test(text) ? text : `${text}。`;
  return /[.?!]$/.test(text) ? text : `${text}.`;
}

/** Joins zh text with a space only after Latin text. */
function joinZh(left: string, right: string): string {
  return /[\x21-\x7e]$/.test(left) ? `${left} ${right}` : `${left}${right}`;
}

/** Starts an en sentence in upper case; padmate stays in lower case. */
function capitalize(text: string): string {
  if (text.startsWith("padmate")) return text;
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** A space before Latin text that follows Chinese. */
function spaced(text: string): string {
  return /^[\x21-\x7e]/.test(text) ? ` ${text}` : text;
}

function location(problem: StartupProblem, zh: boolean): string {
  if (!problem.line) return "";
  if (zh)
    return problem.col
      ? `第 ${problem.line} 行第 ${problem.col} 列`
      : `第 ${problem.line} 行`;
  return problem.col
    ? `line ${problem.line}, column ${problem.col}`
    : `line ${problem.line}`;
}

/** The member pointer ends at, as "name" and a space; "" for an index. */
function subject(pointer: string): string {
  const token = pointer.split("/").at(-1) ?? "";
  if (!token || /^\d+$/.test(token)) return "";
  return `"${token.replaceAll("~1", "/").replaceAll("~0", "~")}" `;
}

/**
 * 'a', 'b' (the schema library) or "a", "b" or "c" as "a"、"b" 或 "c";
 * with all, as "a"、"b"、"c": names that all apply.
 */
function names(list: string, all = false): string {
  const items = [
    ...list.matchAll(/'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"/g),
  ].map((match) => `"${match[1] ?? match[2]}"`);
  if (items.length === 0) return list;
  if (items.length === 1 || all) return items.join("、");
  return `${items.slice(0, -1).join("、")} 或 ${items.at(-1)}`;
}

const types: Record<string, string> = {
  string: "文本",
  integer: "整数",
  number: "数字",
  boolean: "true 或 false",
  object: "对象",
  binding: "对象",
  action: "对象",
  array: "列表",
  null: "null",
};

/** "a string or an integer" or "string or integer" as 文本或整数. */
function typeNames(list: string): string | undefined {
  const words = list
    .split(/, | or /)
    .map((word) => types[word.replace(/^an? /, "").replace(/ object$/, "")]);
  if (words.some((word) => !word)) return undefined;
  return [...new Set(words)].join("或");
}

// Errors of strict JSON, from padmated's own scanner. Others of them keep
// padmated's words in 格式错误（…）.
const syntax: [RegExp, string][] = [
  [/^missing "," between/, "缺少逗号"],
  [/^trailing comma/, "多余的逗号"],
  [/^unterminated string/, "缺少右引号"],
  [/^comments are not allowed/, "不能包含注释"],
  [/^(?:strings|keys) need double quotes/, "文字须用英文双引号括起"],
  [/^curly quote/, "须使用英文双引号"],
  [/^full-width bracket/, "须使用半角括号"],
  [/^full-width comma/, "须使用半角逗号"],
  [/^full-width colon/, "须使用半角冒号"],
  [/^full-width space/, "不能包含全角空格"],
  [/^unexpected end of file/, "文件不完整"],
  [/^the file is empty/, "文件为空"],
  [
    /^(?:invalid UTF-8|unexpected NUL byte|the file is UTF-16)/,
    "文件须以 UTF-8 编码保存",
  ],
];
const syntaxPrefixes =
  /^(?:unexpected |expected "|expected a digit|extra data|invalid escape|invalid string|numbers must not|use lower-case|nesting is deeper|control character|tab in a string)/;

/** The schema library's "got G, want W" or a profile's "expected W, got G". */
function typeMismatch(message: string): [string, string] | undefined {
  const library = /^got (\S+), want (.+)$/.exec(message);
  if (library) return [library[2] as string, library[1] as string];
  const profile = /^expected (.+?), got (.+?)(?:;.*)?$/.exec(message);
  if (profile) return [profile[1] as string, profile[2] as string];
  return undefined;
}

/**
 * problem in plain words, or undefined to keep padmated's message. zh
 * words the known validation and syntax errors; en only those it says
 * better than padmated, starting in lower case.
 */
export function problemText(
  problem: Pick<StartupProblem, "pointer" | "message">,
  language: FailureLanguage,
): string | undefined {
  const zh = language !== "en";
  const { pointer, message } = problem;
  const panic = /^\/safety\/panic\/[^/]+\/(buttons|holdMs)$/.exec(pointer)?.[1];
  const who = subject(pointer);
  let match = /^minItems: got \d+, want (\d+)$/.exec(message);
  if (match) {
    if (panic === "buttons")
      return zh
        ? `紧急暂停至少需要 ${match[1]} 个按键`
        : `the emergency pause needs at least ${match[1]} buttons`;
    return zh ? `${who}至少需要 ${match[1]} 项` : undefined;
  }
  match = /^maxItems: got \d+, want (\d+)$/.exec(message);
  if (match) {
    if (panic === "buttons")
      return zh
        ? `紧急暂停最多只能有 ${match[1]} 个按键`
        : `the emergency pause takes at most ${match[1]} buttons`;
    return zh ? `${who}最多只能有 ${match[1]} 项` : undefined;
  }
  if (/^items at \d+ and \d+ are equal$/.test(message)) {
    if (panic === "buttons")
      return zh
        ? "紧急暂停的按键不能重复"
        : "the emergency pause buttons must all differ";
    return zh ? `${who}有重复的项` : undefined;
  }
  match = /^(minimum|maximum): got \S+, want (\S+)$/.exec(message);
  if (match) {
    const least = match[1] === "minimum";
    if (panic === "holdMs")
      return zh
        ? `紧急暂停的按住时间不能${least ? "少于" : "超过"} ${match[2]} 毫秒`
        : `the emergency pause hold time must be ${least ? "at least" : "at most"} ${match[2]} ms`;
    return zh ? `${who}不能${least ? "小于" : "大于"} ${match[2]}` : undefined;
  }
  if (!zh) return undefined;
  if (
    /^minLength: got 0, want 1$/.test(message) ||
    / must not be empty$/.test(message)
  )
    return `${who}不能为空`;
  match = /^(min|max)Length: got \d+, want (\d+)$/.exec(message);
  if (match)
    return `${who}${match[1] === "min" ? "至少需要" : "最多只能有"} ${match[2]} 个字符`;
  match =
    /^unknown (property|control) ("(?:[^"\\]|\\.)*")(?:; did you mean (.+)\?)?/.exec(
      message,
    );
  if (match)
    return `未知${match[1] === "control" ? "按键" : "项"} ${match[2]}${
      match[3] ? `，是否应为 ${names(match[3])}？` : ""
    }`;
  match = /^additional properties (.+) not allowed$/.exec(message);
  if (match) return `未知项 ${names(match[1] as string, true)}`;
  match = /^missing propert(?:y|ies) (.+?)(?:;.*)?$/.exec(message);
  if (match) return `缺少 ${names(match[1] as string, true)}`;
  const mismatch = typeMismatch(message);
  if (mismatch) {
    const [want, got] = mismatch.map(typeNames);
    if (want && got) return `${who}应为${spaced(want)}，不能是${spaced(got)}`;
  }
  if (message === "expected an exe name or absolute Windows path")
    return `${who}应为程序名或完整路径`;
  if (message === "invalid profile library id")
    return `${who}不是有效的配置 ID`;
  match = /^(.+) is not a valid (?:value|mode); use (.+)$/.exec(message);
  if (match)
    return `${who}不能为 ${names(match[1] as string)}，只能是 ${names(match[2] as string)}`;
  match = /^value must be (?:one of )?(.+)$/.exec(message);
  if (match) return `${who}只能是 ${names(match[1] as string)}`;
  match = /^schemaVersion must be (?:the number )?(\d+)/.exec(message);
  if (match) return `"schemaVersion" 必须为 ${match[1]}`;
  if (/^schemaVersion \d+ is newer than supported version/.test(message))
    return "此文件来自更新版本的 padmate，请升级 padmate";
  for (const [pattern, text] of syntax)
    if (pattern.test(message)) return `格式错误，${text}`;
  match = /^duplicate key ("(?:[^"\\]|\\.)*")/.exec(message);
  if (match) return `格式错误，${match[1]} 重复出现`;
  if (syntaxPrefixes.test(message)) return `格式错误（${message}）`;
  return undefined;
}

/** zh: the location, then what is wrong; raw is padmated's message. */
function located(
  where: string,
  reason: string,
  raw: boolean,
  zh: boolean,
): string {
  if (zh) return finish(where ? `${where}：${reason}` : reason, true, raw);
  return finish(
    where ? `${capitalize(where)}: ${reason}` : capitalize(reason),
    false,
  );
}

function problemLine(
  problem: StartupProblem,
  language: FailureLanguage,
): string {
  const zh = language !== "en";
  const friendly = problemText(problem, language);
  return located(
    location(problem, zh),
    friendly ?? problem.message,
    !friendly,
    zh,
  );
}

function reportSummary(
  report: StartupReport,
  language: FailureLanguage,
): string {
  const zh = language !== "en";
  // A sentence of this file's own words.
  const say = (zhText: string, enText: string) =>
    zh ? finish(zhText, true) : finish(capitalize(enText), false);
  // A lead, then padmated's message, if any.
  const detail = (zhLead: string, enLead: string) => {
    if (!report.message) return say(zhLead, enLead);
    return zh
      ? finish(`${zhLead}：${report.message}`, true, true)
      : finish(`${capitalize(enLead)}: ${report.message}`, false);
  };
  const file = files[report.kind];
  if (file) {
    if (report.cause === "invalid" && report.message) {
      const friendly = problemText(report, language);
      const reason = friendly ?? report.message;
      const where = location(report, zh);
      if (zh)
        return finish(
          `${joinZh(file.zh, where || "有错误")}：${reason}`,
          true,
          !friendly,
        );
      return finish(
        `Error in ${file.en}${where ? `, ${where}` : ""}: ${reason}`,
        false,
      );
    }
    if (report.cause === "missing")
      return say(`找不到${file.zh}`, `could not find ${file.en}`);
    if (report.cause === "denied")
      return say(
        `无法${file.verb.zh}${file.zh}：没有访问权限`,
        `could not ${file.verb.en} ${file.en}: access denied`,
      );
    return detail(
      `无法${file.verb.zh}${file.zh}`,
      `could not ${file.verb.en} ${file.en}`,
    );
  }
  switch (report.kind) {
    case "instance":
      if (report.cause === "refused")
        return zh
          ? "padmate 已在运行，且启动参数不同。请先从托盘退出 padmate。"
          : "padmate is already running with different settings. Quit it from the tray first.";
      if (report.cause === "unavailable")
        return say(
          "padmate 已在运行，但未响应",
          "padmate is already running but does not respond",
        );
      return detail(
        "无法确认 padmate 是否已在运行",
        "could not check whether padmate is already running",
      );
    case "sdl":
      if (report.cause === "missing")
        return say(
          "找不到手柄输入组件 SDL3.dll",
          "the controller input component SDL3.dll is missing",
        );
      return detail(
        "无法加载手柄输入组件 SDL3.dll",
        "could not load the controller input component SDL3.dll",
      );
    case "ipc":
      return detail(
        "无法建立界面与映射服务的连接",
        "could not connect this window to the mapping service",
      );
    case "output":
      return detail(
        "无法向其他程序发送快捷键",
        "could not send shortcuts to other programs",
      );
    case "usage":
      return detail("启动参数错误", "invalid startup arguments");
    default:
      return detail(
        "映射服务发生内部错误",
        "the mapping service had an internal error",
      );
  }
}

/** What the page and the dialog say about failure. */
export function startFailureText(
  failure: StartFailure,
  language: FailureLanguage,
): StartFailureText {
  const zh = language !== "en";
  const text = (summary: string): StartFailureText => ({
    title: zh ? "映射服务未能启动" : "The mapping service did not start",
    summary,
    path: "",
    more: [],
  });
  switch (failure.reason) {
    case "report": {
      const report = failure.report;
      return {
        ...text(reportSummary(report, language)),
        path: report.file,
        more:
          report.cause === "invalid"
            ? report.errors.slice(1).map((item) => problemLine(item, language))
            : [],
      };
    }
    case "exit":
      if (failure.code === null)
        return text(
          zh ? "映射服务意外退出。" : "The mapping service quit unexpectedly.",
        );
      return text(
        zh
          ? `映射服务意外退出（代码 ${failure.code}）。`
          : `The mapping service quit unexpectedly (code ${failure.code}).`,
      );
    case "spawn":
      return text(
        zh ? "无法运行映射服务。" : "Could not run the mapping service.",
      );
    default:
      return text(
        zh ? "映射服务未响应。" : "The mapping service did not respond.",
      );
  }
}
