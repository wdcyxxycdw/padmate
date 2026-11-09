import { describe, expect, it } from "vitest";
import type {
  IntegrationFunction,
  IntegrationItem,
  IntegrationPlanEntry,
} from "../../shared/integrations";
import { demoFunctions } from "../testing/integrations-fixture";
import {
  areasOf,
  assembleChanges,
  backupTime,
  type ConflictDecision,
  checkText,
  conflictEntries,
  cycleNote,
  differenceRows,
  filterFunctions,
  functionsById,
  groupIds,
  itemFor,
  keyMoves,
  movedItems,
  nameAfterPick,
  pathText,
  pruneTree,
  putItem,
  retargetKeys,
  skipText,
  usesKeys,
  visibleRows,
  writable,
} from "./integrations-model";

const functions = demoFunctions.functions;
const names = (list: IntegrationFunction[]) => list.map((fn) => fn.name);

describe("filterFunctions", () => {
  it("matches the name, the path and the keys, without regard to case", () => {
    expect(names(filterFunctions(functions, "撤"))).toEqual(["撤销"]);
    expect(names(filterFunctions(functions, "笔"))).toEqual(["G笔", "铅笔"]);
    expect(names(filterFunctions(functions, "ctrl+z"))).toEqual(["撤销"]);
    expect(names(filterFunctions(functions, "p"))).toEqual(["G笔", "铅笔"]);
  });

  it("needs every word, and no words is every function", () => {
    expect(names(filterFunctions(functions, "工具 铅"))).toEqual(["铅笔"]);
    expect(names(filterFunctions(functions, "编辑 笔"))).toEqual([]);
    expect(filterFunctions(functions, "  ")).toHaveLength(5);
  });
});

describe("areasOf, pruneTree and groupIds", () => {
  it("lists the top-level groups as the areas", () => {
    expect(areasOf(demoFunctions.tree).map((node) => node.name)).toEqual([
      "主菜单",
      "工具",
    ]);
    expect(areasOf([{ id: "x", name: "x", function: "f" }])).toEqual([]);
  });

  it("keeps the found functions with every group above them, in order", () => {
    const kept = pruneTree(
      demoFunctions.tree,
      (node) => node.function === "tool:b" || node.function === "cmd:redo",
    );
    expect(kept).toEqual([
      {
        id: "a:main",
        name: "主菜单",
        children: [
          {
            id: "g:edit",
            name: "编辑",
            children: [{ id: "n:redo", name: "重做", function: "cmd:redo" }],
          },
        ],
      },
      {
        id: "a:tool",
        name: "工具",
        children: [
          {
            id: "g:pen",
            name: "笔",
            children: [
              {
                id: "n:a",
                name: "G笔",
                // A tool that is selectable keeps its function when only a
                // sub-tool was found.
                function: "tool:a",
                children: [{ id: "n:b", name: "铅笔", function: "tool:b" }],
              },
            ],
          },
        ],
      },
    ]);
    expect(pruneTree(demoFunctions.tree, () => false)).toEqual([]);
  });

  it("names the groups that can be open", () => {
    expect([...groupIds(demoFunctions.tree)].sort()).toEqual(
      ["a:main", "a:tool", "g:edit", "g:file", "g:pen", "n:a"].sort(),
    );
  });
});

describe("visibleRows", () => {
  const main = areasOf(demoFunctions.tree)[0].children ?? [];
  const keys = (open: string[]) =>
    visibleRows(main, new Set(open)).map(
      (row) => `${row.depth}:${row.node.name}`,
    );

  it("shows only the top level of the area while nothing is open", () => {
    expect(keys([])).toEqual(["1:编辑", "1:文件"]);
  });

  it("puts the children of an open node right after it, in the daemon's order", () => {
    expect(keys(["g:edit"])).toEqual(["1:编辑", "2:撤销", "2:重做", "1:文件"]);
    expect(keys(["g:edit", "g:file"])).toEqual([
      "1:编辑",
      "2:撤销",
      "2:重做",
      "1:文件",
      "2:另存为",
    ]);
  });

  it("goes down any depth, for a node that is also a function too", () => {
    const tools = areasOf(demoFunctions.tree)[1].children ?? [];
    expect(
      visibleRows(tools, new Set(["g:pen", "n:a"])).map(
        (row) => `${row.depth}:${row.node.name}`,
      ),
    ).toEqual(["1:笔", "2:G笔", "3:铅笔"]);
  });

  it("does not show what is under a closed node, even one marked open", () => {
    const tools = areasOf(demoFunctions.tree)[1].children ?? [];
    expect(
      visibleRows(tools, new Set(["n:a"])).map((row) => row.node.name),
    ).toEqual(["笔"]);
  });

  it("says which rows can be opened, the parent, and the place among the siblings", () => {
    const rows = visibleRows(main, new Set(["g:edit"]));
    expect(rows.map((row) => [row.expandable, row.expanded])).toEqual([
      [true, true],
      [false, false],
      [false, false],
      [true, false],
    ]);
    expect(rows[1]).toMatchObject({
      parent: "g:edit",
      position: 1,
      siblings: 2,
    });
    expect(rows[0].parent).toBeUndefined();
  });
});

describe("cycleNote", () => {
  const byId = functionsById(functions);
  it("names the other tools that share the key", () => {
    expect(cycleNote(functions[3], byId, "Demo Paint", "zh-CN")).toBe(
      "循环键：P 在 Demo Paint 里由 铅笔 共用，按一次不一定切到这个。",
    );
    expect(cycleNote(functions[3], byId, "Demo Paint", "en")).toContain(
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
  // Shaped like the daemon's plan: the add of the imported function, then one
  // remove of the key from each function that has it now.
  const entry = (
    keys: string,
    fn: string,
    displaced: string[] = [],
  ): IntegrationPlanEntry => ({
    item: { keys, function: fn, name: fn, path: [] },
    changes: [
      { function: fn, add: keys },
      ...displaced.map((id) => ({ function: id, remove: keys })),
    ],
    displaced: displaced.map((id) => ({ function: id, name: id, keys })),
  });
  const plain = entry("F2", "cmd:a");
  const clash = entry("Ctrl+K", "cmd:b", ["cmd:x"]);
  const toolClash = entry("P", "tool:a", ["cmd:x", "tool:old"]);
  const skipped: IntegrationPlanEntry = {
    ...entry("F3", "cmd:c"),
    skipped: "missing",
  };

  it("writes the entries as they are when nothing displaces anything", () => {
    expect(assembleChanges([plain, skipped], new Map()).changes).toEqual([
      { function: "cmd:a", add: "F2" },
    ]);
    expect(conflictEntries([plain, clash, skipped])).toEqual([clash]);
  });

  const decide = (
    decision: ConflictDecision,
    target: IntegrationPlanEntry = clash,
  ) => assembleChanges([plain, target], new Map([[target, decision]]));

  it("useImported: the plan's changes as they are", () => {
    expect(decide({ kind: "import" }).changes).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "cmd:b", add: "Ctrl+K" },
      { function: "cmd:x", remove: "Ctrl+K" },
    ]);
    expect(decide({ kind: "import" }, toolClash).changes).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "tool:a", add: "P" },
      { function: "cmd:x", remove: "P" },
      { function: "tool:old", remove: "P" },
    ]);
  });

  it("keepLocal: the whole entry goes, the displaced remove too", () => {
    expect(decide({ kind: "keep" })).toEqual({
      changes: [{ function: "cmd:a", add: "F2" }],
      moves: [],
    });
  });

  it("rekeyImported: the local function keeps the key, the imported one gets the new", () => {
    expect(decide({ kind: "rekeyImported", keys: "Ctrl+J" })).toEqual({
      changes: [
        { function: "cmd:a", add: "F2" },
        { function: "cmd:b", add: "Ctrl+J" },
      ],
      moves: [{ from: "Ctrl+K", to: "Ctrl+J" }],
    });
    // Every displaced function keeps the key, a tool included.
    expect(
      decide({ kind: "rekeyImported", keys: "O" }, toolClash).changes,
    ).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "tool:a", add: "O" },
    ]);
  });

  it("rekeyLocal: the imported function takes the key, the local one loses it once and gets the new", () => {
    expect(
      decide({ kind: "rekeyLocal", keys: "Ctrl+J", function: "cmd:x" }),
    ).toEqual({
      changes: [
        { function: "cmd:a", add: "F2" },
        { function: "cmd:b", add: "Ctrl+K" },
        { function: "cmd:x", remove: "Ctrl+K", add: "Ctrl+J" },
      ],
      moves: [],
    });
  });

  it("rekeyLocal with several displaced functions: only the chosen one moves", () => {
    expect(
      decide({ kind: "rekeyLocal", keys: "O", function: "cmd:x" }, toolClash)
        .changes,
    ).toEqual([
      { function: "cmd:a", add: "F2" },
      { function: "tool:a", add: "P" },
      { function: "cmd:x", remove: "P", add: "O" },
      { function: "tool:old", remove: "P" },
    ]);
  });

  it("two entries that touch the same function stay apart", () => {
    const second = entry("Ctrl+L", "cmd:d", ["cmd:x"]);
    const out = assembleChanges(
      [clash, second],
      new Map<IntegrationPlanEntry, ConflictDecision>([
        [clash, { kind: "rekeyLocal", keys: "Ctrl+J", function: "cmd:x" }],
        [second, { kind: "import" }],
      ]),
    );
    expect(out.changes).toEqual([
      { function: "cmd:b", add: "Ctrl+K" },
      { function: "cmd:x", remove: "Ctrl+K", add: "Ctrl+J" },
      { function: "cmd:d", add: "Ctrl+L" },
      { function: "cmd:x", remove: "Ctrl+L" },
    ]);
    const kept = assembleChanges(
      [clash, second],
      new Map<IntegrationPlanEntry, ConflictDecision>([
        [clash, { kind: "rekeyImported", keys: "Ctrl+J" }],
        [second, { kind: "keep" }],
      ]),
    );
    expect(kept.changes).toEqual([{ function: "cmd:b", add: "Ctrl+J" }]);
    expect(kept.moves).toEqual([{ from: "Ctrl+K", to: "Ctrl+J" }]);
  });

  it("does not repeat a change", () => {
    const twice: IntegrationPlanEntry = {
      ...clash,
      changes: [...clash.changes, { function: "cmd:x", remove: "Ctrl+K" }],
    };
    expect(
      assembleChanges([twice], new Map([[twice, { kind: "import" }]])).changes,
    ).toEqual([
      { function: "cmd:b", add: "Ctrl+K" },
      { function: "cmd:x", remove: "Ctrl+K" },
    ]);
  });
});

describe("backupTime", () => {
  it("shows a time, and keeps text it cannot read", () => {
    expect(backupTime("2026-11-04T10:00:00Z", "en")).toMatch(/2026/);
    expect(backupTime("not a time", "zh-CN")).toBe("not a time");
  });
});

describe("skipText", () => {
  it("says each code the daemon sends in one sentence, and any other generically", () => {
    for (const code of [
      "missing",
      "unsupportedKey",
      "unconfirmedKey",
      "badFunction",
      "other",
    ])
      for (const language of ["zh-CN", "en"] as const) {
        const text = skipText(code, "Demo Paint", language);
        expect(text).not.toContain(code === "other" ? "other" : code);
        expect(text).not.toContain("!");
        expect(text).not.toContain("！");
      }
    expect(skipText("missing", "Demo Paint", "zh-CN")).toBe(
      "Demo Paint 里已找不到这个功能。",
    );
    expect(skipText("zzz", "Demo Paint", "en")).toBe(
      "This item cannot be written.",
    );
  });
});
