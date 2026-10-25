import { describe, expect, it } from "vitest";
import {
  defaultOverlayPrefs,
  defaultToastHoldMs,
  hintEntriesFrom,
  hintsFadeMs,
  hintsFrom,
  hintsGap,
  hintsLayout,
  hintsPanelHeight,
  hintsPanelPadding,
  hintsRowHeight,
  limitHintLabel,
  limitToastText,
  maxHintLabelLength,
  maxHintsAccepted,
  maxHintsShown,
  maxPanelRows,
  maxScriptPanels,
  maxToastHoldMs,
  maxToastLength,
  minToastHoldMs,
  overlayHeightForHints,
  overlayHeightForPanels,
  overlayHintsChannel,
  overlayMaxHeight,
  overlayOpacityFrom,
  overlayOpacityMax,
  overlayOpacityMin,
  overlayOpacityStep,
  overlayPanelsChannel,
  overlayPrefsChannel,
  overlayToastAreaHeight,
  overlayToastChannel,
  pageSettingsFrom,
  panelsFrom,
  parseOverlayPrefs,
  type ScriptPanel,
  scriptPanelLines,
  scriptPanelRows,
  scriptPanelsFrom,
  toastFrom,
  toastHoldFrom,
  toastTextFrom,
} from "./overlay";

describe("the text of a toast (HUD-04)", () => {
  it("crosses on one channel of its own, not one of the main window's", () => {
    expect(overlayToastChannel).toBe("overlay:toast");
    expect(overlayToastChannel.startsWith("padmate:")).toBe(false);
  });

  it("makes a line of any text: whitespace and control characters become one space", () => {
    expect(limitToastText("配置：绘画")).toBe("配置：绘画");
    expect(limitToastText("  a \n\r\n b\t\tc\u0000\u001b[0m d \u007f")).toBe(
      "a b c [0m d",
    );
    expect(limitToastText("a b c")).toBe("a b c");
    expect(limitToastText(" \n\t ")).toBe("");
  });

  it("caps the length by characters, and does not cut an astral character in two", () => {
    const long = limitToastText("长".repeat(maxToastLength + 50));
    expect(Array.from(long)).toHaveLength(maxToastLength);
    const astral = limitToastText("😀".repeat(maxToastLength + 1));
    expect(Array.from(astral)).toHaveLength(maxToastLength);
    expect(astral.endsWith("😀")).toBe(true);
    expect(limitToastText("x".repeat(maxToastLength))).toHaveLength(
      maxToastLength,
    );
  });

  it("takes the text out of { text } and nothing else for a toast", () => {
    expect(toastTextFrom({ text: "已暂停" })).toBe("已暂停");
    // Other fields do not matter, and are not passed on.
    expect(toastTextFrom({ text: " Paused ", extra: { a: 1 } })).toBe("Paused");
    for (const payload of [
      undefined,
      null,
      "已暂停",
      7,
      true,
      [],
      ["已暂停"],
      {},
      { text: 7 },
      { text: null },
      { text: {} },
      { text: "" },
      { text: " \n\t" },
      { message: "已暂停" },
    ])
      expect(toastTextFrom(payload), JSON.stringify(payload)).toBeUndefined();
  });
});

describe("how long a toast holds (SCR-22)", () => {
  it("is 1500 ms unless a script's toast says otherwise, 500 to 10000 ms", () => {
    expect([defaultToastHoldMs, minToastHoldMs, maxToastHoldMs]).toEqual([
      1500, 500, 10000,
    ]);
  });

  it("is a whole number of ms in that range, else nothing", () => {
    for (const ms of [500, 1500, 2345, 10000])
      expect(toastHoldFrom(ms), String(ms)).toBe(ms);
    for (const ms of [
      499,
      10001,
      0,
      -1,
      1500.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      "1500",
      null,
      undefined,
      {},
      [],
    ])
      expect(toastHoldFrom(ms), String(ms)).toBeUndefined();
  });

  it("is carried by a toast message beside its text, and only when valid", () => {
    expect(toastFrom({ text: "脚本提示", ms: 3000 })).toEqual({
      text: "脚本提示",
      ms: 3000,
    });
    // A toast of the state changes has none; neither has one with a bad hold.
    expect(toastFrom({ text: "已暂停" })).toEqual({ text: "已暂停" });
    for (const ms of [100, 99999, 1.5, "3000", null, Number.NaN])
      expect(toastFrom({ text: "x", ms }), String(ms)).toEqual({ text: "x" });
    // The text is limited as before, and a message without one is none.
    expect(toastFrom({ text: " a \n b ", ms: 600 })).toEqual({
      text: "a b",
      ms: 600,
    });
    for (const payload of [undefined, null, 3, "x", [], {}, { ms: 600 }])
      expect(toastFrom(payload), JSON.stringify(payload)).toBeUndefined();
    expect(toastFrom({ text: " \n", ms: 600 })).toBeUndefined();
  });
});

describe("the overlay's preferences (HUD-05)", () => {
  it("crosses to the overlay's page on a channel of its own", () => {
    expect(overlayPrefsChannel).toBe("overlay:prefs");
    expect(overlayPrefsChannel).not.toBe(overlayToastChannel);
    expect(overlayPrefsChannel.startsWith("padmate:")).toBe(false);
  });

  it("starts as Chinese, toasts on, at the bottom, fully opaque", () => {
    expect(defaultOverlayPrefs).toEqual({
      language: "zh-CN",
      toasts: true,
      position: "bottom",
      opacity: 100,
    });
    expect(Object.isFrozen(defaultOverlayPrefs)).toBe(true);
    expect([overlayOpacityMin, overlayOpacityMax, overlayOpacityStep]).toEqual([
      40, 100, 5,
    ]);
  });

  it("takes an opacity that is a whole number from 40 to 100, and nothing else", () => {
    for (const value of [40, 45, 47, 99, 100])
      expect(overlayOpacityFrom(value), String(value)).toBe(value);
    for (const value of [
      39,
      101,
      0,
      -50,
      72.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      "70",
      null,
      undefined,
      true,
      [70],
      { valueOf: () => 70 },
    ])
      expect(overlayOpacityFrom(value), String(value)).toBeUndefined();
  });

  it("keeps the valid fields of a push", () => {
    expect(
      parseOverlayPrefs({
        language: "en",
        toasts: false,
        position: "top",
        opacity: 55,
      }),
    ).toEqual({ language: "en", toasts: false, position: "top", opacity: 55 });
    expect(
      parseOverlayPrefs({
        language: "zh-CN",
        toasts: true,
        position: "bottom",
        opacity: 100,
      }),
    ).toEqual({
      language: "zh-CN",
      toasts: true,
      position: "bottom",
      opacity: 100,
    });
    // A partial push is a partial result.
    expect(parseOverlayPrefs({ toasts: false })).toEqual({ toasts: false });
    expect(parseOverlayPrefs({})).toEqual({});
  });

  it("ignores each invalid field and keeps the others", () => {
    expect(
      parseOverlayPrefs({
        language: "fr",
        toasts: "off",
        position: "left",
        opacity: 30,
      }),
    ).toEqual({});
    expect(
      parseOverlayPrefs({
        language: "en",
        toasts: 0,
        position: "top",
        opacity: "70",
      }),
    ).toEqual({ language: "en", position: "top" });
    expect(
      parseOverlayPrefs({
        language: null,
        toasts: false,
        position: ["top"],
        opacity: 101,
      }),
    ).toEqual({ toasts: false });
  });

  it("passes on nothing else", () => {
    const prefs = parseOverlayPrefs({
      toasts: true,
      path: "C:\\x",
      __proto__: { position: "top" },
      constructor: { opacity: 50 },
    });
    expect(prefs).toEqual({ toasts: true });
    expect(Object.keys(prefs)).toEqual(["toasts"]);
  });

  it("takes no push that is not an object", () => {
    for (const value of [
      undefined,
      null,
      "top",
      7,
      true,
      [],
      [{ toasts: false }],
      () => ({ toasts: false }),
    ])
      expect(parseOverlayPrefs(value), String(value)).toEqual({});
  });

  it("tells the page only an opacity: { opacity } with a valid one", () => {
    expect(pageSettingsFrom({ opacity: 70 })).toEqual({ opacity: 70 });
    // Other fields are not passed on.
    expect(pageSettingsFrom({ opacity: 70, position: "top" })).toEqual({
      opacity: 70,
    });
    for (const payload of [
      undefined,
      null,
      "70",
      70,
      [],
      {},
      { opacity: 39 },
      { opacity: "70" },
      { opacity: 70.5 },
      { position: "top" },
    ])
      expect(
        pageSettingsFrom(payload),
        JSON.stringify(payload),
      ).toBeUndefined();
  });
});

describe("key hints on their way to the page (HUD-03)", () => {
  it("cross on a channel of their own, not one of the main window's", () => {
    expect(overlayHintsChannel).toBe("overlay:hints");
    expect(overlayHintsChannel.startsWith("padmate:")).toBe(false);
    expect(
      new Set([
        overlayToastChannel,
        overlayPrefsChannel,
        overlayHintsChannel,
        overlayPanelsChannel,
      ]).size,
    ).toBe(4);
  });

  it("make a line of a label: whitespace and control characters become one space, and it is cut", () => {
    expect(limitHintLabel("  撤销\n\t上一步\u0000 ")).toBe("撤销 上一步");
    expect(limitHintLabel("")).toBe("");
    expect(Array.from(limitHintLabel("长".repeat(100)))).toHaveLength(
      maxHintLabelLength,
    );
    expect(Array.from(limitHintLabel("😀".repeat(100)))).toHaveLength(
      maxHintLabelLength,
    );
  });

  it("keep the entries that have a control name and a label, and drop the rest", () => {
    const entries = hintEntriesFrom([
      { control: "south", label: "撤销" },
      { control: "ls.up", label: "" },
      { control: "dpad.upright", label: "a\nb" },
      { control: "righttrigger+", label: "x" },
      { control: "South", label: "no capitals" },
      { control: "", label: "empty" },
      { control: "south\n", label: "newline" },
      { control: "west" },
      { control: 3, label: "x" },
      null,
      "east",
      { control: "south", label: "second time" },
    ]);
    expect(entries).toEqual([
      { control: "south", label: "撤销" },
      { control: "ls.up", label: "" },
      { control: "dpad.upright", label: "a b" },
      { control: "righttrigger+", label: "x" },
    ]);
  });

  it("take at most maxHintsAccepted entries, and nothing that is not a list", () => {
    const many = Array.from({ length: 300 }, (_, index) => ({
      control: `c${index}`,
      label: "x",
    }));
    expect(hintEntriesFrom(many)).toHaveLength(maxHintsAccepted);
    for (const value of [undefined, null, "south", 3, {}, { length: 2 }])
      expect(hintEntriesFrom(value)).toBeUndefined();
    expect(hintEntriesFrom([])).toEqual([]);
  });

  it("are a message with the entries, the language and the position", () => {
    const entries = [{ control: "south", label: "撤销" }];
    expect(hintsFrom({ entries, language: "en", position: "top" })).toEqual({
      entries,
      language: "en",
      position: "top",
    });
    // An empty list is a message too: it hides the panel.
    expect(
      hintsFrom({ entries: [], language: "zh-CN", position: "bottom" }),
    ).toEqual({ entries: [], language: "zh-CN", position: "bottom" });
  });

  it("fall back to Chinese and the bottom for a language or position it does not know, and drop what is not a message", () => {
    expect(
      hintsFrom({ entries: [], language: "fr", position: "left" }),
    ).toEqual({ entries: [], language: "zh-CN", position: "bottom" });
    expect(hintsFrom({ entries: [] })).toEqual({
      entries: [],
      language: "zh-CN",
      position: "bottom",
    });
    for (const payload of [undefined, null, "south", 3, {}, { entries: "x" }])
      expect(hintsFrom(payload)).toBeUndefined();
  });

  it("carry the pad only when it is well-formed, with its labels cut to a small size", () => {
    const base = { entries: [], language: "en", position: "bottom" };
    expect(
      hintsFrom({
        ...base,
        pad: {
          type: "xboxone",
          buttonLabels: { south: "A", east: "B", bad: 3 },
          buttonMask: 255,
        },
      })?.pad,
    ).toEqual({
      type: "xboxone",
      buttonLabels: { south: "A", east: "B" },
      buttonMask: 255,
    });
    expect(hintsFrom({ ...base, pad: { type: "ps5" } })?.pad).toEqual({
      type: "ps5",
    });
    for (const pad of [
      null,
      "xbox",
      [],
      {},
      { type: 3 },
      { type: "x".repeat(41) },
    ])
      expect(hintsFrom({ ...base, pad })?.pad).toBeUndefined();
    // A mask that is not a safe whole number is left out; the pad stays.
    for (const buttonMask of [-1, 1.5, Number.NaN, 2 ** 60, "3"])
      expect(
        hintsFrom({ ...base, pad: { type: "ps5", buttonMask } })?.pad,
      ).toEqual({ type: "ps5" });
    const labels = Object.fromEntries(
      Array.from({ length: 100 }, (_, index) => [`k${index}`, "x"]),
    );
    expect(
      Object.keys(
        hintsFrom({ ...base, pad: { type: "ps5", buttonLabels: labels } })?.pad
          ?.buttonLabels ?? {},
      ),
    ).toHaveLength(32);
    expect(
      hintsFrom({
        ...base,
        pad: { type: "ps5", buttonLabels: { south: "x".repeat(25) } },
      })?.pad?.buttonLabels,
    ).toEqual({});
  });

  it("lay out in two columns, at most sixteen entries, and a line for the rest", () => {
    expect(maxHintsShown).toBe(16);
    expect(hintsLayout(0)).toEqual({ shown: 0, more: false, rows: 0 });
    expect(hintsLayout(1)).toEqual({ shown: 1, more: false, rows: 1 });
    expect(hintsLayout(2)).toEqual({ shown: 2, more: false, rows: 1 });
    expect(hintsLayout(3)).toEqual({ shown: 3, more: false, rows: 2 });
    expect(hintsLayout(16)).toEqual({ shown: 16, more: false, rows: 8 });
    expect(hintsLayout(17)).toEqual({ shown: 16, more: true, rows: 8 });
    expect(hintsLayout(500)).toEqual({ shown: 16, more: true, rows: 8 });
  });

  it("make a panel as tall as its rows, the '…' line and its padding", () => {
    expect(hintsPanelHeight(0)).toBe(0);
    expect(hintsPanelHeight(1)).toBe(hintsRowHeight + 2 * hintsPanelPadding);
    expect(hintsPanelHeight(16)).toBe(
      8 * hintsRowHeight + 2 * hintsPanelPadding,
    );
    expect(hintsPanelHeight(17)).toBe(
      9 * hintsRowHeight + 2 * hintsPanelPadding,
    );
  });

  it("make a window as tall as the toast area, the gap and the panel, never past the tallest", () => {
    expect(overlayToastAreaHeight).toBe(160);
    expect(overlayHeightForHints(0)).toBe(160);
    expect(overlayHeightForHints(6)).toBe(160 + hintsGap + hintsPanelHeight(6));
    // The tallest panel fits: that is why the rows are 18 px.
    expect(overlayHeightForHints(17)).toBe(350);
    expect(overlayHeightForHints(17)).toBeLessThanOrEqual(overlayMaxHeight);
    for (let count = 0; count < 100; count++)
      expect(overlayHeightForHints(count)).toBeLessThanOrEqual(
        overlayMaxHeight,
      );
  });

  it("fade in and out over 120 ms", () => {
    expect(hintsFadeMs).toBe(120);
  });
});

describe("a script's panels on their way to the page (SCR-22)", () => {
  const row = (left: string, right: string): [string, string] => [left, right];

  it("cross on a channel of their own, not one of the main window's", () => {
    expect(overlayPanelsChannel).toBe("overlay:panels");
    expect(overlayPanelsChannel.startsWith("padmate:")).toBe(false);
  });

  it("keep a title and rows of two texts, cut and folded like the hints' labels", () => {
    expect(
      scriptPanelsFrom([
        {
          id: "p",
          title: " 画笔\n",
          rows: [
            ["大小", "12"],
            ["不透明度", ""],
          ],
        },
      ]),
    ).toEqual([
      { title: "画笔", rows: [row("大小", "12"), row("不透明度", "")] },
    ]);
    const long = scriptPanelsFrom([
      {
        id: "p",
        title: "题".repeat(100),
        rows: [["左".repeat(100), "右\t\t\n右"]],
      },
    ]);
    expect(Array.from(long?.[0].title ?? "")).toHaveLength(maxHintLabelLength);
    expect(Array.from(long?.[0].rows[0][0] ?? "")).toHaveLength(
      maxHintLabelLength,
    );
    expect(long?.[0].rows[0][1]).toBe("右 右");
  });

  it("leave the title out when there is none or it is empty once folded", () => {
    for (const title of [undefined, "", " \n\t", 3, null]) {
      const panels = scriptPanelsFrom([{ id: "p", title, rows: [["a", "b"]] }]);
      expect(panels, String(title)).toEqual([{ rows: [row("a", "b")] }]);
      expect(Object.hasOwn(panels?.[0] ?? {}, "title")).toBe(false);
    }
  });

  it("keep the rows that are two strings and drop the others, at most sixteen", () => {
    expect(
      scriptPanelsFrom([
        {
          id: "p",
          rows: [
            ["a", "b"],
            ["only one"],
            ["a", "b", "c"],
            [1, "b"],
            ["a", 2],
            "ab",
            null,
            { 0: "a", 1: "b" },
            ["c", "d"],
          ],
        },
      ]),
    ).toEqual([{ rows: [row("a", "b"), row("c", "d")] }]);
    const many = Array.from({ length: 40 }, (_, i) => [`l${i}`, `r${i}`]);
    expect(maxPanelRows).toBe(16);
    expect(scriptPanelsFrom([{ id: "p", rows: many }])?.[0].rows).toHaveLength(
      maxPanelRows,
    );
  });

  it("drop an entry that is not a panel: no id (for the daemon's list), no rows, nothing to draw", () => {
    const good = { id: "p", rows: [["a", "b"]] };
    expect(
      scriptPanelsFrom([
        good,
        { rows: [["a", "b"]] },
        { id: "", rows: [["a", "b"]] },
        { id: 3, rows: [["a", "b"]] },
        { id: "q" },
        { id: "q", rows: "ab" },
        { id: "q", rows: [] },
        { id: "q", title: "  ", rows: [] },
        null,
        "p",
        [],
        3,
      ]),
    ).toEqual([{ rows: [row("a", "b")] }]);
    // A title alone is a panel.
    expect(scriptPanelsFrom([{ id: "p", title: "画笔", rows: [] }])).toEqual([
      { title: "画笔", rows: [] },
    ]);
  });

  it("take an id only once, and at most four panels", () => {
    const panel = (id: string, text = id) => ({ id, rows: [[text, "x"]] });
    expect(
      scriptPanelsFrom([panel("a"), panel("a", "again"), panel("b")])?.map(
        (p) => p.rows[0][0],
      ),
    ).toEqual(["a", "b"]);
    expect(maxScriptPanels).toBe(4);
    expect(
      scriptPanelsFrom(["a", "b", "c", "d", "e", "f"].map((id) => panel(id))),
    ).toHaveLength(maxScriptPanels);
  });

  it("need no id in a message, and nothing that is not a list", () => {
    expect(scriptPanelsFrom([{ rows: [["a", "b"]] }], false)).toEqual([
      { rows: [row("a", "b")] },
    ]);
    // Two panels with the same content are two panels there.
    expect(
      scriptPanelsFrom([{ rows: [["a", "b"]] }, { rows: [["a", "b"]] }], false),
    ).toHaveLength(2);
    for (const value of [undefined, null, "p", 3, {}, { length: 1 }])
      expect(scriptPanelsFrom(value)).toBeUndefined();
    expect(scriptPanelsFrom([])).toEqual([]);
  });

  it("are a message with the panels and the position; an empty list hides them", () => {
    const panels: ScriptPanel[] = [
      { title: "画笔", rows: [row("大小", "12")] },
    ];
    expect(panelsFrom({ panels, position: "top" })).toEqual({
      panels,
      position: "top",
    });
    expect(panelsFrom({ panels: [], position: "bottom" })).toEqual({
      panels: [],
      position: "bottom",
    });
    // The bottom for a position it does not know; what is not a message is none.
    expect(panelsFrom({ panels, position: "left" })?.position).toBe("bottom");
    expect(panelsFrom({ panels })?.position).toBe("bottom");
    for (const payload of [undefined, null, "p", 3, {}, { panels: "x" }])
      expect(panelsFrom(payload), JSON.stringify(payload)).toBeUndefined();
  });

  it("check the panels of a message again, whatever Main sent", () => {
    const message = panelsFrom({
      panels: [
        { id: "ignored", title: "\n画笔 ", rows: [["a", "b"], ["c"]] },
        { rows: "none" },
        { title: "", rows: [] },
      ],
      position: "bottom",
    });
    expect(message?.panels).toEqual([{ title: "画笔", rows: [row("a", "b")] }]);
  });

  it("are drawn one row per line, a title being a row of its own, panels one after the other", () => {
    const panels: ScriptPanel[] = [
      { title: "画笔", rows: [row("大小", "12"), row("硬度", "80")] },
      { rows: [row("层", "3")] },
      { title: "颜色", rows: [] },
    ];
    expect(scriptPanelLines(panels)).toEqual({
      lines: [
        { kind: "title", text: "画笔" },
        { kind: "row", left: "大小", right: "12" },
        { kind: "row", left: "硬度", right: "80" },
        { kind: "row", left: "层", right: "3" },
        { kind: "title", text: "颜色" },
      ],
      more: false,
    });
    expect(scriptPanelRows(panels)).toBe(5);
    expect(scriptPanelLines([])).toEqual({ lines: [], more: false });
    expect(scriptPanelRows([])).toBe(0);
  });

  it("show sixteen rows, titles counting, and a line for the rest", () => {
    const rows = (count: number) =>
      Array.from({ length: count }, (_, i) => row(`l${i}`, `r${i}`));
    // 1 title + 15 rows = 16: all shown, no "…".
    expect(scriptPanelRows([{ title: "t", rows: rows(15) }])).toBe(16);
    expect(scriptPanelLines([{ title: "t", rows: rows(15) }]).more).toBe(false);
    // One more makes the "…" line: 16 lines and the line for the rest.
    const over = scriptPanelLines([{ title: "t", rows: rows(16) }]);
    expect(over.lines).toHaveLength(16);
    expect(over.more).toBe(true);
    expect(over.lines[0]).toEqual({ kind: "title", text: "t" });
    expect(scriptPanelRows([{ title: "t", rows: rows(16) }])).toBe(17);
    // Four panels of sixteen rows: still sixteen shown, the first ones.
    const four = Array.from({ length: 4 }, (_, i) => ({
      title: `p${i}`,
      rows: rows(16),
    }));
    const shown = scriptPanelLines(four);
    expect(shown.lines).toHaveLength(16);
    expect(shown.more).toBe(true);
    expect(scriptPanelRows(four)).toBe(17);
  });

  it("make a window as tall as the toast area, the gap and the rows with their padding", () => {
    expect(overlayHeightForPanels(0)).toBe(overlayToastAreaHeight);
    expect(overlayHeightForPanels(-3)).toBe(overlayToastAreaHeight);
    expect(overlayHeightForPanels(1)).toBe(
      overlayToastAreaHeight +
        hintsGap +
        hintsRowHeight +
        2 * hintsPanelPadding,
    );
    expect(overlayHeightForPanels(5)).toBe(160 + 8 + 5 * 18 + 20);
  });

  it("fit the tallest panels in the tallest window: sixteen rows and the '…' line", () => {
    expect(overlayMaxHeight).toBe(160 + 8 + 17 * 18 + 20);
    expect(overlayMaxHeight).toBe(494);
    expect(overlayHeightForPanels(17)).toBe(overlayMaxHeight);
    for (let rows = 0; rows < 60; rows++)
      expect(overlayHeightForPanels(rows)).toBeLessThanOrEqual(
        overlayMaxHeight,
      );
    // The hints still need less, as before.
    expect(overlayHeightForHints(17)).toBe(350);
  });
});
