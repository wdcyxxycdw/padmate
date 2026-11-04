import { describe, expect, it } from "vitest";
import type {
  IntegrationFunction,
  IntegrationItem,
  IntegrationPlanEntry,
} from "../../shared/integrations";
import { demoFunctions } from "../testing/integrations-fixture";
import {
  assembleChanges,
  backupTime,
  type ConflictDecision,
  checkText,
  conflictEntries,
  cycleNote,
  differenceRows,
  filterFunctions,
  functionsById,
  itemFor,
  keyMoves,
  movedItems,
  nameAfterPick,
  pathText,
  putItem,
  retargetKeys,
  usesKeys,
  writable,
} from "./integrations-model";

const functions = demoFunctions.functions;
const names = (list: IntegrationFunction[]) => list.map((fn) => fn.name);

describe("filterFunctions", () => {
  it("matches the name, the path and the keys, without regard to case", () => {
    expect(names(filterFunctions(functions, "撤", ""))).toEqual(["撤销"]);
    expect(names(filterFunctions(functions, "笔", ""))).toEqual([
      "G笔",
      "铅笔",
    ]);
    expect(names(filterFunctions(functions, "ctrl+z", ""))).toEqual(["撤销"]);
    expect(names(filterFunctions(functions, "p", ""))).toEqual(["G笔", "铅笔"]);
  });

  it("needs every word, and a category narrows the list", () => {
    expect(names(filterFunctions(functions, "工具 铅", ""))).toEqual(["铅笔"]);
    expect(names(filterFunctions(functions, "", "edit"))).toEqual([
      "撤销",
      "重做",
    ]);
    expect(names(filterFunctions(functions, "笔", "edit"))).toEqual([]);
    expect(filterFunctions(functions, "  ", "")).toHaveLength(4);
  });
});

describe("cycleNote", () => {
  const byId = functionsById(functions);
  it("names the other tools that share the key", () => {
    expect(cycleNote(functions[2], byId, "Demo Paint", "zh-CN")).toBe(
      "循环键：P 在 Demo Paint 里由 铅笔 共用，按一次不一定切到这个。",
    );
    expect(cycleNote(functions[2], byId, "Demo Paint", "en")).toContain(
      "shared with 铅笔",
    );
    expect(cycleNote(functions[0], byId, "Demo Paint", "zh-CN")).toBeNull();
  });
});

describe("checkText", () => {
  const owner = { id: "x", name: "放大", path: [] };
  it("says in one sentence who has the key", () => {
    expect(
      checkText({ status: "free", owners: [], notes: [] }, "Demo", "zh-CN")
        .text,
    ).toBe("这个键在 Demo 里没有被占用。");
    const conflict = checkText(
      {
        status: "conflict",
        owners: [owner, { ...owner, name: "缩小" }],
        notes: [],
      },
      "Demo",
      "zh-CN",
    );
    expect(conflict).toMatchObject({
      level: "block",
      text: "这个键在 Demo 里是 放大、缩小。",
    });
    const cycle = checkText(
      { status: "cycle", owners: [owner], notes: [] },
      "Demo",
      "zh-CN",
    );
    expect(cycle.level).toBe("warn");
    expect(cycle.text).toContain("按一次会依次切换");
  });

  it("explains the notes and the states that need a look", () => {
    const text = checkText(
      { status: "free", owners: [], notes: ["toolShift", "modifierKey"] },
      "Demo",
      "en",
    );
    expect(text.notes).toEqual([
      "Holding this key in Demo switches tools for a moment.",
      "The modifier-key settings of Demo use this key.",
    ]);
    for (const status of ["unconfirmed", "unsupported", "same"] as const)
      expect(
        checkText({ status, owners: [], notes: [] }, "Demo", "zh-CN").text,
      ).toContain("Demo");
  });

  it("lets everything but a clash and an unsupported key be written", () => {
    const can = (status: Parameters<typeof checkText>[0]["status"]) =>
      writable({ status, owners: [], notes: [] });
    expect(
      ["free", "same", "cycle", "unconfirmed"].map((s) => can(s as never)),
    ).toEqual([true, true, true, true]);
    expect(can("conflict")).toBe(false);
    expect(can("unsupported")).toBe(false);
  });
});

describe("items", () => {
  const undo: IntegrationItem = {
    keys: "Ctrl+Z",
    function: "cmd:undo",
    name: "撤销",
    path: ["编辑"],
  };
  const pen: IntegrationItem = {
    ...undo,
    keys: "P",
    function: "tool:a",
    name: "G笔",
  };

  it("keeps a key once, and drops the item the binding had", () => {
    expect(putItem([undo], pen)).toEqual([undo, pen]);
    expect(putItem([undo, pen], { ...pen, function: "tool:b" })).toEqual([
      undo,
      { ...pen, function: "tool:b" },
    ]);
    expect(putItem([undo, pen], { ...undo, keys: "Ctrl+Y" }, "Ctrl+Z")).toEqual(
      [pen, { ...undo, keys: "Ctrl+Y" }],
    );
  });

  it("keeps a name the user gave, and takes the function's when it was the last one's", () => {
    expect(nameAfterPick(undefined, undefined, "撤销")).toBe("撤销");
    expect(nameAfterPick("", "撤销", "撤销")).toBe("撤销");
    expect(nameAfterPick("G笔", "G笔", "铅笔")).toBe("铅笔");
    expect(nameAfterPick("我的笔", "G笔", "铅笔")).toBe("我的笔");
  });

  it("finds the item of a key", () => {
    expect(itemFor([undo, pen], "P")).toBe(pen);
    expect(itemFor([undo], "")).toBeUndefined();
    expect(itemFor(undefined, "P")).toBeUndefined();
    expect(pathText(["a", "b"])).toBe("a › b");
  });
});

describe("differences", () => {
  const items: IntegrationItem[] = [
    { keys: "Ctrl+Z", function: "cmd:undo", name: "撤销", path: [] },
    { keys: "P", function: "tool:a", name: "G笔", path: [] },
    { keys: "Q", function: "cmd:gone", name: "旧", path: [] },
    { keys: "W", function: "cmd:nokey", name: "无键", path: [] },
  ];
  const statuses = [
    {
      keys: "Ctrl+Z",
      function: "cmd:undo",
      state: "changed" as const,
      current: ["Ctrl+U"],
    },
    { keys: "P", function: "tool:a", state: "consistent" as const },
    { keys: "Q", function: "cmd:gone", state: "missing" as const },
    { keys: "W", function: "cmd:nokey", state: "removed" as const },
  ];

  it("lists one row per item that is not consistent, in words", () => {
    const rows = differenceRows(items, statuses, "Demo", "zh-CN");
    expect(rows.map((row) => [row.name, row.state, row.text])).toEqual([
      ["撤销", "changed", "在 Demo 里现在是 Ctrl+U"],
      ["旧", "missing", "Demo 里已找不到这个功能"],
      ["无键", "removed", "在 Demo 里已没有快捷键"],
    ]);
  });

  it("names who has the key now when the function itself has none", () => {
    const rows = differenceRows(
      items,
      [
        {
          keys: "P",
          function: "tool:a",
          state: "changed",
          owners: [{ id: "x", name: "放大", path: [] }],
        },
      ],
      "Demo",
      "zh-CN",
    );
    expect(rows[0].text).toBe("这个键在 Demo 里现在是 放大");
  });

  it("moves a key only when the function still has one", () => {
    const rows = differenceRows(items, statuses, "Demo", "zh-CN");
    const moves = keyMoves(rows);
    expect(moves).toEqual([{ from: "Ctrl+Z", to: "Ctrl+U" }]);
    expect(movedItems(items, moves).map((item) => item.keys)).toEqual([
      "Ctrl+U",
      "P",
      "Q",
      "W",
    ]);
  });
});

describe("retargetKeys", () => {
  const profile = {
    layers: {
      base: {
        south: { tap: { type: "keys", keys: "Ctrl+Z" }, label: "撤销" },
        north: {
          press: {
            type: "sequence",
            steps: [{ keys: "Ctrl+Z" }, { wait: 10 }, { keys: "A" }],
          },
        },
        east: { press: { type: "keys", keys: "B" } },
      },
    },
  };

  it("changes every shortcut of the key, wherever it is, and nothing else", () => {
    const { value, changed } = retargetKeys(profile, [
      { from: "Ctrl+Z", to: "Ctrl+U" },
    ]);
    expect(changed).toBe(2);
    expect(JSON.stringify(value)).not.toContain("Ctrl+Z");
    expect(value.layers.base.east).toEqual(profile.layers.base.east);
    expect(value.layers.base.south.label).toBe("撤销");
    // The original is not touched.
    expect(profile.layers.base.south.tap.keys).toBe("Ctrl+Z");
  });

  it("does nothing for a key the profile does not send", () => {
    expect(retargetKeys(profile, [{ from: "F9", to: "F10" }]).changed).toBe(0);
    expect(usesKeys(profile, "A")).toBe(true);
    expect(usesKeys(profile, "F9")).toBe(false);
  });
});

describe("assembleChanges", () => {
  const entry = (
    keys: string,
    fn: string,
    displaced: string[] = [],
  ): IntegrationPlanEntry => ({
    item: { keys, function: fn, name: fn, path: [] },
    changes: [{ function: fn, add: keys }],
    displaced: displaced.map((id) => ({ function: id, name: id, keys })),
  });
  const plain = entry("F2", "cmd:a");
  const clash = entry("P", "tool:a", ["cmd:x"]);
  const skipped: IntegrationPlanEntry = {
    ...entry("F3", "cmd:b"),
    skipped: "gone",
  };

  it("writes the entries as they are when nothing displaces anything", () => {
    expect(assembleChanges([plain, skipped], new Map()).changes).toEqual([
      { function: "cmd:a", add: "F2" },
    ]);
    expect(conflictEntries([plain, clash, skipped])).toEqual([clash]);
  });

  const decide = (decision: ConflictDecision) =>
    assembleChanges([plain, clash], new Map([[clash, decision]]));

  it("takes the key from the local function when the imported setting wins", () => {
    expect(decide({ kind: "import" }).changes).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "tool:a", add: "P" },
      { function: "cmd:x", remove: "P" },
    ]);
  });

  it("writes nothing for an item whose local setting is kept", () => {
    expect(decide({ kind: "keep" })).toEqual({
      changes: [{ function: "cmd:a", add: "F2" }],
      moves: [],
    });
  });

  it("gives the imported function another key, and moves the profile's keys", () => {
    expect(decide({ kind: "rekeyImported", keys: "O" })).toEqual({
      changes: [
        { function: "cmd:a", add: "F2" },
        { function: "tool:a", add: "O" },
      ],
      moves: [{ from: "P", to: "O" }],
    });
  });

  it("gives the local function another key while the imported one takes the old", () => {
    expect(
      decide({ kind: "rekeyLocal", keys: "O", function: "cmd:x" }).changes,
    ).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "tool:a", add: "P" },
      { function: "cmd:x", remove: "P", add: "O" },
    ]);
  });

  it("does not repeat a change the plan already made", () => {
    const planned: IntegrationPlanEntry = {
      ...clash,
      changes: [...clash.changes, { function: "cmd:x", remove: "P" }],
    };
    expect(
      assembleChanges([planned], new Map([[planned, { kind: "import" }]]))
        .changes,
    ).toEqual([
      { function: "tool:a", add: "P" },
      { function: "cmd:x", remove: "P" },
    ]);
  });
});

describe("backupTime", () => {
  it("shows a time, and keeps text it cannot read", () => {
    expect(backupTime("2026-11-04T10:00:00Z", "en")).toMatch(/2026/);
    expect(backupTime("not a time", "zh-CN")).toBe("not a time");
  });
});
