import { describe, expect, it } from "vitest";
import type {
  StartFailure,
  StartupProblem,
  StartupReport,
  StartupReportCause,
  StartupReportKind,
} from "./api";
import { problemText, startFailureText } from "./start-failure-text";

function report(
  kind: StartupReportKind,
  cause: StartupReportCause,
  fields: Partial<StartupReport> = {},
): StartFailure {
  return {
    reason: "report",
    report: {
      kind,
      cause,
      file: "",
      line: 0,
      col: 0,
      pointer: "",
      message: "",
      dataDir: "C:\\data",
      errors: [],
      ...fields,
    },
  };
}

function invalid(
  kind: StartupReportKind,
  errors: StartupProblem[],
  file = "C:\\data\\config.json",
): StartFailure {
  return report(kind, "invalid", {
    file,
    ...(errors[0] as StartupProblem),
    errors,
  });
}

const zh = (failure: StartFailure) => startFailureText(failure, "zh-CN");
const en = (failure: StartFailure) => startFailureText(failure, "en");

describe("start failure text (UI-45)", () => {
  it("says the owner's broken config.json as the owner asked", () => {
    const failure = invalid("config", [
      {
        line: 14,
        col: 20,
        pointer: "/safety/panic/standard/buttons",
        message: "minItems: got 1, want 2",
      },
    ]);
    expect(zh(failure)).toEqual({
      title: "映射服务未能启动",
      summary:
        "设置文件 config.json 第 14 行第 20 列：紧急暂停至少需要 2 个按键。",
      path: "C:\\data\\config.json",
      more: [],
    });
    expect(en(failure)).toEqual({
      title: "The mapping service did not start",
      summary:
        "Error in the settings file config.json, line 14, column 20: the emergency pause needs at least 2 buttons.",
      path: "C:\\data\\config.json",
      more: [],
    });
  });

  it("lists the further errors of the file", () => {
    const failure = invalid(
      "profile",
      [
        {
          line: 3,
          col: 5,
          pointer: "/name",
          message: 'missing "," between object members',
        },
        {
          line: 7,
          col: 1,
          pointer: "/layers/base/south/press/mode",
          message: '"hold2" is not a valid mode; use "tap", "hold" or "toggle"',
        },
        { line: 9, col: 0, pointer: "", message: "something new" },
        { line: 0, col: 0, pointer: "", message: "no place" },
      ],
      "C:\\data\\profiles\\paint\\profile.json",
    );
    expect(zh(failure)).toEqual({
      title: "映射服务未能启动",
      summary: "配置文件第 3 行第 5 列：格式错误，缺少逗号。",
      path: "C:\\data\\profiles\\paint\\profile.json",
      more: [
        '第 7 行第 1 列："mode" 不能为 "hold2"，只能是 "tap"、"hold" 或 "toggle"。',
        // padmated's own words stay as they are after a Chinese lead.
        "第 9 行：something new",
        "no place",
      ],
    });
    expect(en(failure).more).toEqual([
      'Line 7, column 1: "hold2" is not a valid mode; use "tap", "hold" or "toggle".',
      "Line 9: something new.",
      "No place.",
    ]);
  });

  it.each([
    [
      "config",
      "设置文件 config.json",
      "the settings file config.json",
      "读取",
      "read",
    ],
    [
      "state",
      "状态文件 state.json",
      "the state file state.json",
      "读取",
      "read",
    ],
    ["profile", "配置文件", "the profile file", "读取", "read"],
    ["library", "数据文件夹", "the data folder", "打开", "open"],
    ["log", "日志文件", "the log file", "写入", "write"],
  ] as const)(
    "names %s and why it cannot be used",
    (kind, zhName, enName, zhVerb, enVerb) => {
      expect(zh(report(kind, "missing")).summary).toBe(`找不到${zhName}。`);
      expect(en(report(kind, "missing")).summary).toBe(
        `Could not find ${enName}.`,
      );
      expect(zh(report(kind, "denied")).summary).toBe(
        `无法${zhVerb}${zhName}：没有访问权限。`,
      );
      expect(en(report(kind, "denied")).summary).toBe(
        `Could not ${enVerb} ${enName}: access denied.`,
      );
      expect(
        zh(report(kind, "other", { message: "The disk is full" })).summary,
      ).toBe(`无法${zhVerb}${zhName}：The disk is full`);
      expect(zh(report(kind, "other")).summary).toBe(
        `无法${zhVerb}${zhName}。`,
      );
    },
  );

  it("names an invalid file without a place", () => {
    expect(
      zh(
        report("state", "invalid", {
          message: "invalid profile library id",
          pointer: "/lastProfile",
          errors: [
            {
              line: 0,
              col: 0,
              pointer: "/lastProfile",
              message: "invalid profile library id",
            },
          ],
        }),
      ).summary,
    ).toBe('状态文件 state.json 有错误："lastProfile" 不是有效的配置 ID。');
  });

  it.each([
    [
      report("instance", "refused", { message: "output mode differs" }),
      "padmate 已在运行，且启动参数不同。请先从托盘退出 padmate。",
      "padmate is already running with different settings. Quit it from the tray first.",
    ],
    [
      report("instance", "unavailable", {
        message: "context deadline exceeded",
      }),
      "padmate 已在运行，但未响应。",
      "padmate is already running but does not respond.",
    ],
    [
      report("instance", "other", { message: "Access is denied." }),
      "无法确认 padmate 是否已在运行：Access is denied.",
      "Could not check whether padmate is already running: Access is denied.",
    ],
    [
      report("sdl", "missing", { message: "SDL3.dll not found" }),
      "找不到手柄输入组件 SDL3.dll。",
      "The controller input component SDL3.dll is missing.",
    ],
    [
      report("sdl", "other", { message: "SDL_Init failed" }),
      "无法加载手柄输入组件 SDL3.dll：SDL_Init failed",
      "Could not load the controller input component SDL3.dll: SDL_Init failed.",
    ],
    [
      report("ipc", "other", { message: "pipe busy" }),
      "无法建立界面与映射服务的连接：pipe busy",
      "Could not connect this window to the mapping service: pipe busy.",
    ],
    [
      report("output", "other", { message: "SendInput unavailable" }),
      "无法向其他程序发送快捷键：SendInput unavailable",
      "Could not send shortcuts to other programs: SendInput unavailable.",
    ],
    [
      report("usage", "other", {
        message: "flag provided but not defined: -x",
      }),
      "启动参数错误：flag provided but not defined: -x",
      "Invalid startup arguments: flag provided but not defined: -x.",
    ],
    [
      report("internal", "other", { message: "panic: boom" }),
      "映射服务发生内部错误：panic: boom",
      "The mapping service had an internal error: panic: boom.",
    ],
    [
      { reason: "timeout" },
      "映射服务未响应。",
      "The mapping service did not respond.",
    ],
    [
      { reason: "spawn" },
      "无法运行映射服务。",
      "Could not run the mapping service.",
    ],
    [
      { reason: "exit", code: 1 },
      "映射服务意外退出（代码 1）。",
      "The mapping service quit unexpectedly (code 1).",
    ],
    [
      { reason: "exit", code: null },
      "映射服务意外退出。",
      "The mapping service quit unexpectedly.",
    ],
  ] as [StartFailure, string, string][])(
    "says %j",
    (failure, zhText, enText) => {
      expect(zh(failure)).toMatchObject({
        summary: zhText,
        path: "",
        more: [],
      });
      expect(en(failure).summary).toBe(enText);
    },
  );

  it("shows the folder at fault in full", () => {
    expect(
      zh(report("library", "denied", { file: "C:\\Users\\me\\padmate" })).path,
    ).toBe("C:\\Users\\me\\padmate");
  });

  it("never points at the developer documentation, or names layers", () => {
    const all: StartFailure[] = [
      report("config", "invalid", {
        message: 'unknown layer "tools"; only "base" until layers arrive in M3',
      }),
      report("sdl", "missing"),
      report("instance", "refused"),
      { reason: "timeout" },
    ];
    for (const failure of all)
      for (const language of ["zh-CN", "en"] as const) {
        const text = JSON.stringify(startFailureText(failure, language));
        expect(text).not.toMatch(/docs\/|troubleshooting/);
        if (language === "zh-CN") expect(text).not.toContain("层");
      }
  });
});

describe("problem text", () => {
  const panic = "/safety/panic/joyconLeft/buttons";
  const hold = "/safety/panic/standard/holdMs";
  it.each([
    [panic, "minItems: got 1, want 2", "紧急暂停至少需要 2 个按键"],
    [panic, "maxItems: got 9, want 8", "紧急暂停最多只能有 8 个按键"],
    [panic, "items at 0 and 1 are equal", "紧急暂停的按键不能重复"],
    [hold, "minimum: got 100, want 500", "紧急暂停的按住时间不能少于 500 毫秒"],
    [
      hold,
      "maximum: got 70000, want 60000",
      "紧急暂停的按住时间不能超过 60000 毫秒",
    ],
    [
      "/timing/doubleTapMs",
      "minimum: got 10, want 50",
      '"doubleTapMs" 不能小于 50',
    ],
    ["/rules", "minItems: got 0, want 1", '"rules" 至少需要 1 项'],
    ["/rules/0", "maxLength: got 300, want 260", "最多只能有 260 个字符"],
    ["/name", "name must not be empty", '"name" 不能为空'],
    [
      "/layers/base/sotuh",
      'unknown control "sotuh"; did you mean "south"?',
      '未知按键 "sotuh"，是否应为 "south"？',
    ],
    [
      "/layers/base/south/lable",
      'unknown property "lable"; did you mean "label"?',
      '未知项 "lable"，是否应为 "label"？',
    ],
    [
      "/timing",
      "additional properties 'holdMS', 'x' not allowed",
      '未知项 "holdMS"、"x"',
    ],
    [
      "",
      'missing property "schemaVersion"; add "schemaVersion": 1',
      '缺少 "schemaVersion"',
    ],
    [
      "/safety/panic/standard",
      "missing properties 'buttons', 'holdMs'",
      '缺少 "buttons"、"holdMs"',
    ],
    [
      "/timing/holdMs",
      "got string, want integer",
      '"holdMs" 应为整数，不能是文本',
    ],
    [
      "/enabled",
      "got string, want boolean",
      '"enabled" 应为 true 或 false，不能是文本',
    ],
    [
      "/layers/base/south",
      'expected a binding object, got a string; write {"press": {"type": "keys", "keys": "A"}}',
      '"south" 应为对象，不能是文本',
    ],
    [
      "/onUnmatched",
      "value must be one of 'default', 'none'",
      '"onUnmatched" 只能是 "default" 或 "none"',
    ],
    ["/schemaVersion", "schemaVersion must be 1", '"schemaVersion" 必须为 1'],
    [
      "",
      "schemaVersion 2 is newer than supported version 1; upgrade padmate",
      "此文件来自更新版本的 padmate，请升级 padmate",
    ],
    ["", 'trailing comma before "}" (strict JSON)', "格式错误，多余的逗号"],
    [
      "",
      "unterminated string; the closing quote is missing",
      "格式错误，缺少右引号",
    ],
    ["", "comments are not allowed (strict JSON)", "格式错误，不能包含注释"],
    [
      "",
      'curly quote "“"; use a straight double quote',
      "格式错误，须使用英文双引号",
    ],
    ["", 'full-width bracket "［"; use "["', "格式错误，须使用半角括号"],
    ["", 'full-width comma "，"; use ","', "格式错误，须使用半角逗号"],
    ["", 'full-width colon "："; use ":"', "格式错误，须使用半角冒号"],
    [
      "",
      "full-width space U+3000; use an ordinary space",
      "格式错误，不能包含全角空格",
    ],
    ["", "unexpected end of file", "格式错误，文件不完整"],
    [
      "",
      "invalid UTF-8 (byte 0xFF); save the file as UTF-8",
      "格式错误，文件须以 UTF-8 编码保存",
    ],
    [
      "",
      "the file is UTF-16; save it as UTF-8",
      "格式错误，文件须以 UTF-8 编码保存",
    ],
    [
      "",
      'duplicate key "name"; the first is at 2:3',
      '格式错误，"name" 重复出现',
    ],
    [
      "",
      "extra data after the top-level value",
      "格式错误（extra data after the top-level value）",
    ],
    [
      "",
      'unexpected "]"; expected a value',
      '格式错误（unexpected "]"; expected a value）',
    ],
  ])("%s %s", (pointer, message, text) => {
    expect(problemText({ pointer, message }, "zh-CN")).toBe(text);
  });

  it("keeps messages it does not know", () => {
    expect(
      problemText(
        { pointer: "/layers/tools", message: 'unknown layer "tools"' },
        "zh-CN",
      ),
    ).toBeUndefined();
    expect(
      problemText(
        { pointer: "/x", message: "got string, want widget" },
        "zh-CN",
      ),
    ).toBeUndefined();
  });

  it("words in English only what padmated says worse", () => {
    expect(
      problemText(
        { pointer: hold, message: "minimum: got 100, want 500" },
        "en",
      ),
    ).toBe("the emergency pause hold time must be at least 500 ms");
    expect(
      problemText(
        { pointer: "/timing/x", message: "minimum: got 1, want 5" },
        "en",
      ),
    ).toBeUndefined();
    expect(
      problemText({ pointer: "", message: "unexpected end of file" }, "en"),
    ).toBeUndefined();
  });
});
