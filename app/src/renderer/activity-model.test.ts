import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { StatusResult } from "../shared/api";
import {
  ACTIVITY_LIMIT,
  type ActivityRow,
  activityTime,
  appendActivity,
  type DecisionActivity,
  decisionRow,
  latencyText,
  matchesKey,
  noAction,
  reasonKey,
  reasonSentences,
  rowGesture,
  rowResult,
  rowSentence,
  rowWhere,
  unroutedRow,
  unroutedSentence,
} from "./activity-model";

const status: StatusResult = {
  version: "test",
  protocol: 1,
  dryRun: false,
  fakeInput: false,
  paused: false,
  capturing: false,
  suspended: "none",
  profileId: "paint",
  slots: [
    {
      slot: 0,
      device: 7,
      key: "k7",
      name: "Pad A",
      profile: "Paint",
      generation: 1,
      layers: { base: "base", overlays: [], color: "" },
      latches: [],
      mode: 0,
    },
    {
      slot: 1,
      device: 8,
      key: "k8",
      name: "Pad B",
      profile: "Paint",
      generation: 1,
      layers: { base: "base", overlays: [], color: "" },
      latches: [],
      mode: 0,
    },
  ],
  devices: [
    {
      info: {
        id: 7,
        name: "Pad A name",
        type: "xboxone",
        vendor: 0,
        product: 0,
        version: 0,
        guid: "a",
        driver: "",
        path: "",
        serial: "",
        gamepad: true,
        virtual: false,
      },
      key: "k7",
      slot: 0,
      status: "mapped",
      disabled: false,
    },
  ],
};

function decision(
  fields: Record<string, unknown>,
  event: Partial<DecisionActivity> = {},
): DecisionActivity {
  return {
    kind: "decision",
    slot: 0,
    at: 1_700_000_000_123,
    decision: { control: "south", edge: "down", gesture: "press", ...fields },
    ...event,
  };
}

function row(
  fields: Record<string, unknown>,
  event: Partial<DecisionActivity> = {},
  from: StatusResult | null = status,
): ActivityRow {
  const made = decisionRow(decision(fields, event), 1, from, 5);
  if (!made) throw new Error("no row");
  return made;
}

/**
 * The names in the Go table `reasonNames` of a source file: every quoted
 * string in it, whatever letters or digits it has, so a reason that is added
 * to the table cannot slip by without a sentence.
 */
function reasonNamesOf(source: string): string[] {
  const table = /var reasonNames = \[\.\.\.\]string\{([^}]*)\}/.exec(source);
  expect(table, "the table reasonNames was not found").not.toBeNull();
  // A comment may quote a word (a name that was removed), which is not an entry.
  const entries = (table?.[1] ?? "").replace(/\/\/.*$/gm, "");
  return [...entries.matchAll(/"([^"\n]*)"/g)].map((m) => m[1]);
}

describe("the sentence for every reason (UI-21, ADR-0071)", () => {
  const goReasons = () =>
    reasonNamesOf(
      readFileSync(
        new URL("../../../daemon/internal/engine/decision.go", import.meta.url),
        "utf8",
      ),
    );

  it("reads every name of the Go table, not only the all-lowercase ones", () => {
    const source = `package engine

var reasonNames = [...]string{
	ReasonDispatch: "dispatch",
	// "ghost" was removed.
	ReasonBlocked: "scriptBlocked",
	ReasonRepeat:  "autoRepeat2", // "also" gone
}
`;
    expect(reasonNamesOf(source)).toEqual([
      "dispatch",
      "scriptBlocked",
      "autoRepeat2",
    ]);
  });

  it("finds the Go table of the engine, with a name for every Reason", () => {
    const source = readFileSync(
      new URL("../../../daemon/internal/engine/decision.go", import.meta.url),
      "utf8",
    );
    const keys = source.match(/^\tReason\w+:\s+"/gm) ?? [];
    expect(reasonNamesOf(source)).toHaveLength(keys.length);
  });

  it("has a Chinese and an English sentence for each reason of the engine", () => {
    const reasons = goReasons();
    expect(reasons.length).toBeGreaterThanOrEqual(11);
    for (const reason of reasons) {
      const sentence = reasonSentences[reason];
      expect(sentence, `no sentence for the reason "${reason}"`).toBeDefined();
      expect(sentence.zh.length, reason).toBeGreaterThan(0);
      expect(sentence.en.length, reason).toBeGreaterThan(0);
    }
  });

  it("has no sentence for a reason the engine does not have", () => {
    const reasons = goReasons();
    for (const key of Object.keys(reasonSentences))
      expect(reasons, key).toContain(key.split(".")[0]);
  });

  it("gives every row of every reason a sentence in both languages", () => {
    for (const reason of goReasons())
      for (const edge of ["down", "up", ""])
        for (const language of ["zh-CN", "en"] as const) {
          // "end" is a reason the list does not keep (decisionRow), but the
          // table has it, so a row of it is made by hand.
          const text = rowSentence(
            { ...row({ reason: "spurious", edge, action: "tap A" }), reason },
            language,
          );
          expect(text, `${reason} ${edge} ${language}`).not.toBe(reason);
          expect(text.length).toBeGreaterThan(0);
        }
  });

  it("says in the ADR's words why a press did nothing", () => {
    const say = (fields: Record<string, unknown>, event = {}) =>
      rowSentence(row(fields, event), "zh-CN");
    expect(say({ reason: "unbound" })).toBe("当前配置里这个键没有设置动作");
    expect(
      say({ reason: "unbound", binding: "/layers/base/south", label: "撤销" }),
    ).toBe("这个键只有名称，没有设置动作");
    expect(
      say(
        { reason: "unbound", binding: "/layers/base/south" },
        { waiting: ["tap", "hold"] },
      ),
    ).toBe("按下时没有动作，要等松开、长按或连按后才能确定");
    expect(say({ reason: "paused" })).toBe("映射已暂停");
    expect(say({ reason: "blocked", level: "tools" })).toBe(
      "这个键在当前层被屏蔽",
    );
    expect(say({ reason: "cancel", edge: "", action: "hold Space" })).toBe(
      "这次按压被取消，按住的键已放开（暂停、切换配置或释放全部按键时会这样）",
    );
    // A press still waiting for its gesture, or a dropped window for more
    // taps, held nothing to release.
    expect(say({ reason: "cancel", edge: "" })).toBe(
      "这次按压被取消（暂停、切换配置或释放全部按键时会这样）",
    );
    expect(
      rowSentence(row({ reason: "cancel", edge: "", gesture: "tap" }), "en"),
    ).toBe("This press was canceled (pause, profile switch or release all).");
    expect(say({ reason: "canceled", edge: "up" })).toBe(
      "这次按压已被取消，松开时不执行动作",
    );
    expect(say({ reason: "canceled", edge: "down" })).toBe(
      "这个键之前锁存了按键，这次按下只是解除锁存",
    );
    expect(say({ reason: "spurious" })).toBe("重复的按下或松开，已忽略");
    expect(say({ reason: "function", control: "back" })).toBe(
      "功能键：按住期间其他键换成另一套",
    );
    expect(say({ reason: "function", control: "back", edge: "up" })).toBe(
      "功能键松开，其他键恢复平时的一套",
    );
    expect(say({ reason: "script", gesture: "tap" })).toBe("脚本处理了这个键");
    expect(say({ reason: "release", edge: "up" })).toBe("松开，放开了按住的键");
  });

  it("says it in English too", () => {
    const say = (fields: Record<string, unknown>, event = {}) =>
      rowSentence(row(fields, event), "en");
    expect(say({ reason: "unbound" })).toBe(
      "This key has no action in the current profile.",
    );
    expect(
      say({ reason: "unbound", binding: "/x" }, { waiting: ["release"] }),
    ).toBe(
      "Nothing happens on press; the action waits for release, hold or repeated taps.",
    );
    expect(say({ reason: "script" })).toBe("A script handled this key.");
  });

  it("does not call an unbound press that waits for a gesture a failure", () => {
    expect(
      noAction(row({ reason: "unbound", binding: "/x" }, { waiting: ["tap"] })),
    ).toBe(false);
    expect(noAction(row({ reason: "unbound" }))).toBe(true);
    // A gesture other than a press or the taps that a script left unanswered.
    expect(reasonKey(row({ reason: "unbound", gesture: "hold" }))).toBe(
      "unbound.gesture",
    );
  });

  describe("the end of a wait for taps that no action answers", () => {
    const ended = (gesture: string, fields: Record<string, unknown> = {}) =>
      row({
        reason: "unbound",
        gesture,
        level: "base",
        binding: "/layers/base/south",
        ...fields,
      });

    it("reads as the end of the wait, with the number of taps", () => {
      const zh = (gesture: string) => rowSentence(ended(gesture), "zh-CN");
      expect(zh("tap")).toBe("按了 1 下，这个键没有设置按 1 下的动作");
      expect(zh("doubleTap")).toBe("按了 2 下，这个键没有设置按 2 下的动作");
      expect(zh("tripleTap")).toBe("按了 3 下，这个键没有设置按 3 下的动作");
      expect(zh("quadTap")).toBe("按了 4 下，这个键没有设置按 4 下的动作");
      const en = (gesture: string) => rowSentence(ended(gesture), "en");
      expect(en("tap")).toBe(
        "Tapped once; this key has no action for a single tap.",
      );
      expect(en("doubleTap")).toBe(
        "Tapped 2 times; this key has no action for 2 taps.",
      );
      expect(en("quadTap")).toBe(
        "Tapped 4 times; this key has no action for 4 taps.",
      );
    });

    it("is not the sentence of a press that waits, or of another gesture left open", () => {
      expect(reasonKey(ended("tap"))).toBe("unbound.tap");
      expect(reasonKey(ended("tripleTap"))).toBe("unbound.taps");
      expect(reasonKey(ended("hold"))).toBe("unbound.gesture");
      expect(reasonKey(ended("press"))).toBe("unbound.labelOnly");
      expect(
        reasonKey(
          row({ reason: "unbound", binding: "/x" }, { waiting: ["doubleTap"] }),
        ),
      ).toBe("unbound.waiting");
    });

    it("stays in the list of what did nothing", () => {
      for (const gesture of ["tap", "doubleTap", "tripleTap", "quadTap"])
        expect(noAction(ended(gesture)), gesture).toBe(true);
    });

    it("shows how long it waited, on this row only", () => {
      expect(
        rowResult(
          row(
            { reason: "unbound", gesture: "doubleTap", binding: "/x" },
            { waitedMs: 232 },
          ),
          "zh-CN",
        ),
      ).toEqual({
        text: "按了 2 下，这个键没有设置按 2 下的动作",
        action: false,
        preview: false,
        waitedMs: 232,
      });
      // Without the figure there is no wait to show; and the press that began
      // the wait has no figure of its own.
      expect(rowResult(ended("tap"), "zh-CN").waitedMs).toBeNull();
      expect(
        rowResult(
          row({ reason: "unbound", binding: "/x" }, { waitedMs: 5 }),
          "zh-CN",
        ).waitedMs,
      ).toBeNull();
    });
  });

  it("tells what paused the mapping at the time of the press", () => {
    const paused = (fields: Partial<StatusResult>) =>
      rowSentence(
        row({ reason: "paused" }, {}, { ...status, ...fields }),
        "zh-CN",
      );
    expect(paused({ paused: true })).toBe("映射已暂停");
    expect(paused({ suspended: "lock" })).toBe(
      "系统暂停中（锁屏、睡眠或安全桌面）",
    );
    expect(paused({ capturing: true })).toBe("正在录入快捷键，映射暂停");
    // An explicit pause is the first cause when several hold.
    expect(paused({ paused: true, suspended: "lock", capturing: true })).toBe(
      "映射已暂停",
    );
    expect(rowSentence(row({ reason: "paused" }, {}, null), "zh-CN")).toBe(
      "映射已暂停",
    );
  });
});

describe("unrouted presses (ADR-0071 section 2.6)", () => {
  const event = (fields: Record<string, unknown>) => ({
    kind: "unrouted" as const,
    at: 1_700_000_000_000,
    device: 8,
    control: "south",
    down: true,
    why: "queued",
    ...fields,
  });

  it("makes a row of a press, not of a release", () => {
    const made = unroutedRow(event({}), 3, status, 9);
    expect(made).toMatchObject({
      seq: 3,
      at: 1_700_000_000_000,
      kind: "unrouted",
      device: 8,
      control: "south",
      reason: "unrouted",
      why: "queued",
      mapped: 2,
    });
    expect(unroutedRow(event({ down: false }), 4, status, 9)).toBeNull();
    expect(unroutedRow(event({ control: undefined }), 4, status, 9)).toBeNull();
    expect(unroutedRow(event({ why: undefined }), 4, status, 9)).toBeNull();
  });

  it("says why the pad is not mapped", () => {
    const why = (name: string, language: "zh-CN" | "en" = "zh-CN") =>
      unroutedSentence({ why: name, mapped: 4 }, language);
    expect(why("queued")).toBe("这只手柄没有被映射：已有 4 只手柄在映射");
    expect(why("ignored")).toBe("这只手柄被设为忽略");
    expect(why("notGamepad")).toBe("这只手柄没有标准映射");
    expect(why("disabled")).toBe("这只手柄的映射因反复出错被停用");
    expect(why("queued", "en")).toBe(
      "This controller is not mapped: 4 controllers are already mapped.",
    );
    expect(why("ignored", "en")).toBe("This controller is set to be ignored.");
    expect(why("something new")).toBe("这只手柄没有被映射");
  });

  it("counts four mapped pads when the status is not known", () => {
    expect(unroutedRow(event({}), 1, null, 0)?.mapped).toBe(4);
    expect(noAction(unroutedRow(event({}), 1, null, 0) as ActivityRow)).toBe(
      true,
    );
  });
});

describe("a decision as a row", () => {
  it("takes the time from the daemon and the pad from the slot", () => {
    const made = row({ reason: "dispatch", action: "tap Ctrl+Z" });
    expect(made).toMatchObject({
      at: 1_700_000_000_123,
      device: 7,
      deviceName: "Pad A name",
      control: "south",
      reason: "dispatch",
      action: "tap Ctrl+Z",
    });
    // Without a time from the daemon it is the time it arrived.
    expect(
      decisionRow(
        { kind: "decision", slot: 0, decision: { control: "a", reason: "x" } },
        1,
        status,
        42,
      )?.at,
    ).toBe(42);
  });

  it("falls back to the slot's name for a pad the device list lacks, and to none for an unknown slot", () => {
    expect(row({ reason: "spurious" }, { slot: 1 })).toMatchObject({
      device: 8,
      deviceName: "Pad B",
    });
    expect(row({ reason: "spurious" }, { slot: 3 })).toMatchObject({
      device: null,
      deviceName: "",
    });
    expect(row({ reason: "spurious" }, {}, null)).toMatchObject({
      device: null,
    });
  });

  it("does not keep a release that ended a cycle holding nothing", () => {
    expect(decisionRow(decision({ reason: "end" }), 1, status, 0)).toBeNull();
    expect(
      decisionRow(decision({ reason: "release", edge: "up" }), 1, status, 0),
    ).not.toBeNull();
  });

  it("ignores an event without a control or a reason", () => {
    expect(decisionRow({ kind: "decision" }, 1, status, 0)).toBeNull();
    expect(decisionRow(decision({ control: 3 }), 1, status, 0)).toBeNull();
    expect(
      decisionRow(decision({ reason: undefined }), 1, status, 0),
    ).toBeNull();
  });

  it("marks output that was only previewed", () => {
    const sent = row({ reason: "dispatch", action: "tap A" });
    expect(sent.preview).toBe(false);
    expect(
      row(
        { reason: "dispatch", action: "tap A" },
        {},
        { ...status, dryRun: true },
      ).preview,
    ).toBe(true);
    expect(
      row(
        { reason: "dispatch", action: "tap A" },
        {},
        { ...status, localDryRun: true },
      ).preview,
    ).toBe(true);
  });

  it("keeps how long a gesture waited", () => {
    expect(
      row({ reason: "dispatch", gesture: "tap" }, { waitedMs: 180 }).waitedMs,
    ).toBe(180);
    expect(row({ reason: "dispatch" }).waitedMs).toBeNull();
  });
});

describe("the result of a row", () => {
  it("shows the action that ran as the preview line shows it", () => {
    const result = rowResult(
      row({ reason: "dispatch", action: "tap Ctrl+Shift+Z" }),
      "zh-CN",
    );
    expect(result).toEqual({
      text: "Ctrl+Shift+Z",
      action: true,
      preview: false,
      waitedMs: null,
    });
    expect(
      rowResult(row({ reason: "dispatch", action: "macro 复制.ts" }), "zh-CN")
        .text,
    ).toBe("复制");
  });

  it("shows an action the preview line does not know as written", () => {
    expect(
      rowResult(row({ reason: "dispatch", action: "layer toggle tools" }), "en")
        .text,
    ).toBe("layer toggle tools");
    expect(
      rowResult(row({ reason: "dispatch", action: "mouse jump 0.5,0.5" }), "en")
        .text,
    ).toBe("mouse jump 0.5,0.5");
    expect(
      rowResult(row({ reason: "dispatch", action: "profile Krita" }), "zh-CN")
        .text,
    ).toBe("切换到「Krita」");
    expect(
      rowResult(row({ reason: "dispatch", action: "profile Krita" }), "en")
        .text,
    ).toBe("Switch to Krita");
  });

  it("shows sequences and typed texts", () => {
    expect(
      rowResult(
        row({
          reason: "dispatch",
          action: 'sequence: Ctrl+A, wait 100, text "hi"',
        }),
        "en",
      ).text,
    ).toBe("Ctrl+A, 100 ms, “hi”");
    expect(
      rowResult(row({ reason: "dispatch", action: 'text "你好"' }), "zh-CN")
        .text,
    ).toBe("输入文字「你好」");
  });

  it("carries the wait and the preview mark on the row that decided", () => {
    const result = rowResult(
      row(
        { reason: "dispatch", gesture: "tap", action: "tap A" },
        { waitedMs: 212 },
        { ...status, localDryRun: true },
      ),
      "zh-CN",
    );
    expect(result).toMatchObject({
      action: true,
      preview: true,
      waitedMs: 212,
    });
  });

  it("marks only what the output prints as previewed, not what padmate does itself", () => {
    const previewed = (action: string) =>
      rowResult(
        row(
          { reason: "dispatch", action },
          {},
          { ...status, localDryRun: true },
        ),
        "zh-CN",
      ).preview;
    for (const action of [
      "tap Ctrl+Z",
      "hold Shift",
      "macro 复制.ts",
      "script cycleBrush",
      'text "hi"',
      "sequence: Ctrl+A, wait 100",
      "mouse jump 0.5,0.5",
    ])
      expect(previewed(action), action).toBe(true);
    for (const action of [
      "layer toggle tools",
      "profile Krita",
      "hud hints",
      "mouse precision 0.3",
    ])
      expect(previewed(action), action).toBe(false);
  });

  it("shows the sentence for a row that did not run an action", () => {
    expect(
      rowResult(
        row({ reason: "release", edge: "up", action: "hold Shift" }),
        "en",
      ),
    ).toEqual({
      text: "Released the held keys.",
      action: false,
      preview: false,
      waitedMs: null,
    });
    // A dispatch that names no action still says something.
    expect(rowResult(row({ reason: "dispatch" }), "zh-CN").text).toBe(
      "执行了动作",
    );
  });
});

describe("where and how a row is named", () => {
  it("names the level", () => {
    const where = (fields: Record<string, unknown>) =>
      rowWhere(row(fields), "zh-CN");
    expect(where({ reason: "dispatch", level: "base" })).toBe("平时");
    expect(where({ reason: "dispatch", level: "tools" })).toBe("tools");
    expect(where({ reason: "dispatch", level: "common" })).toBe("共用");
    // The level names the function key held (ADR-0075), whichever of the three
    // it is; the row says so without the name the pad prints on it (ADR-0076).
    for (const key of ["back", "start", "guide"])
      expect(where({ reason: "dispatch", level: `function:${key}` })).toBe(
        "按住功能键",
      );
    // Only a level of a control is a function key's.
    expect(where({ reason: "dispatch", level: "function:" })).toBe("function:");
    expect(where({ reason: "script", level: "" })).toBe("脚本");
    expect(where({ reason: "unbound", level: "" })).toBe("");
    expect(
      rowWhere(row({ reason: "dispatch", level: "function:start" }), "en"),
    ).toBe("Hold function key");
    expect(rowWhere(row({ reason: "dispatch", level: "common" }), "en")).toBe(
      "Shared",
    );
  });

  it("shows the release a script took as the script's, on the release", () => {
    // The engine answers a waiting press with this when the script's button
    // handler consumed the release: reason script, gesture release, edge down.
    const consumed = row({
      reason: "script",
      gesture: "release",
      edge: "down",
    });
    expect(rowGesture(consumed, "zh-CN")).toBe("松开");
    expect(rowSentence(consumed, "zh-CN")).toBe("脚本处理了这个键");
    expect(rowWhere(consumed, "zh-CN")).toBe("脚本");
    // It is not a waiting press, nor a failure: the filter does not keep it.
    expect(noAction(consumed)).toBe(false);
  });

  it("names the gesture, a release by its edge", () => {
    const gesture = (fields: Record<string, unknown>) =>
      rowGesture(row({ reason: "dispatch", ...fields }), "zh-CN");
    expect(gesture({ gesture: "press" })).toBe("点按");
    expect(gesture({ gesture: "tap" })).toBe("点按");
    expect(gesture({ gesture: "doubleTap" })).toBe("双击");
    expect(gesture({ gesture: "hold" })).toBe("长按");
    expect(gesture({ gesture: "release" })).toBe("松开");
    // The engine says "press" for the release that ended a held key.
    expect(gesture({ gesture: "press", edge: "up" })).toBe("松开");
    expect(gesture({ gesture: "", edge: "" })).toBe("");
    expect(rowGesture(row({ reason: "dispatch", gesture: "hold" }), "en")).toBe(
      "Hold",
    );
  });
});

describe("the ring (UI-20)", () => {
  const rows = (from: number, to: number) =>
    Array.from({ length: to - from }, (_, index) =>
      row({ reason: "spurious", cycle: from + index }),
    ).map((made, index) => ({ ...made, seq: from + index }));

  it("keeps the newest rows up to the limit, oldest first", () => {
    expect(ACTIVITY_LIMIT).toBe(200);
    const ring = appendActivity(rows(0, 150), rows(150, 260));
    expect(ring).toHaveLength(200);
    expect(ring[0].seq).toBe(60);
    expect(ring.at(-1)?.seq).toBe(259);
    expect(appendActivity([], rows(0, 3)).map((made) => made.seq)).toEqual([
      0, 1, 2,
    ]);
  });

  it("takes a batch larger than the limit whole", () => {
    expect(appendActivity([], rows(0, 500)).map((made) => made.seq)[0]).toBe(
      300,
    );
  });
});

describe("the key filter and the clock", () => {
  it("matches the name the pad prints, or the start of the control", () => {
    const made = row({ reason: "spurious", control: "ls.up" });
    expect(matchesKey(made, "LS ↑", "ls")).toBe(true);
    expect(matchesKey(made, "LS ↑", "↑")).toBe(true);
    expect(matchesKey(made, "LS ↑", "ls.u")).toBe(true);
    expect(matchesKey(made, "LS ↑", "south")).toBe(false);
    expect(matchesKey(made, "LS ↑", "  ")).toBe(true);
    expect(matchesKey(row({ reason: "x", control: "south" }), "A", "a")).toBe(
      true,
    );
    expect(matchesKey(row({ reason: "x", control: "east" }), "B", "a")).toBe(
      false,
    );
  });

  it("writes the time with milliseconds, in local time", () => {
    expect(activityTime(new Date(2026, 9, 24, 14, 3, 9, 27).getTime())).toBe(
      "14:03:09.027",
    );
    expect(activityTime(new Date(2026, 9, 24, 0, 0, 0, 0).getTime())).toBe(
      "00:00:00.000",
    );
    expect(activityTime(Number.NaN)).toBe("—");
  });

  it("writes a latency in microseconds under a millisecond, else in milliseconds", () => {
    expect(latencyText(0)).toBe("0 µs");
    expect(latencyText(850)).toBe("850 µs");
    expect(latencyText(999)).toBe("999 µs");
    expect(latencyText(1000)).toBe("1.00 ms");
    expect(latencyText(1234)).toBe("1.23 ms");
    expect(latencyText(15678)).toBe("15.68 ms");
    expect(latencyText(Number.NaN)).toBe("—");
  });
});

describe("rows that explain a press that did nothing (只看没有动作的)", () => {
  it("lists what sent nothing and left it unsaid", () => {
    const said: Record<string, boolean> = {};
    for (const [reason, fields] of Object.entries({
      dispatch: { action: "tap A" },
      unbound: {},
      release: { edge: "up" },
      cancel: { edge: "" },
      canceled: { edge: "up" },
      spurious: {},
      paused: {},
      blocked: {},
      function: {},
      script: {},
    }))
      said[reason] = noAction(row({ reason, ...fields }));
    expect(said).toEqual({
      dispatch: false,
      unbound: true,
      release: false,
      cancel: true,
      canceled: true,
      spurious: true,
      paused: true,
      blocked: true,
      function: false,
      script: false,
    });
  });
});
