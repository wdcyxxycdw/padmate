import { describe, expect, it } from "vitest";
import {
  boundIntegrations,
  hasBound,
  type IntegrationInfo,
  isBound,
} from "./integrations";
import {
  parseIntegrationBackups,
  parseIntegrationChanged,
  parseIntegrationCheck,
  parseIntegrationFunctions,
  parseIntegrationItems,
  parseIntegrationList,
  parseIntegrationPlan,
  parseIntegrationRestored,
  parseIntegrationWritten,
} from "./integrations-parse";

const info = {
  id: "demoapp",
  name: "Demo Paint",
  exe: ["DemoPaint.exe"],
  found: true,
  running: false,
  profiles: ["paint"],
};

describe("integrations.list", () => {
  it("accepts the daemon's list, with or without the optional members", () => {
    expect(parseIntegrationList([])).toEqual([]);
    expect(
      parseIntegrationList([
        {
          ...info,
          dataDir: "C:\\d",
          installDir: "C:\\i",
          custom: true,
          problem: "unsupported",
        },
        { ...info, id: "other", found: false, problem: "notFound" },
      ]),
    ).toEqual([
      {
        ...info,
        dataDir: "C:\\d",
        installDir: "C:\\i",
        custom: true,
        problem: "unsupported",
      },
      { ...info, id: "other", found: false, problem: "notFound" },
    ]);
  });

  it.each([
    ["not a list", {}],
    ["a bad integration ID", [{ ...info, id: "Bad Id" }]],
    ["an ID that starts with a digit", [{ ...info, id: "1app" }]],
    ["no name", [{ ...info, name: "" }]],
    ["found not a boolean", [{ ...info, found: "yes" }]],
    ["running missing", [{ ...info, running: undefined }]],
    ["an unknown problem", [{ ...info, problem: "broken" }]],
    ["custom not a boolean", [{ ...info, custom: "yes" }]],
    ["exe not strings", [{ ...info, exe: [1] }]],
    ["profiles not strings", [{ ...info, profiles: [3] }]],
    ["an entry that is not an object", [null]],
  ])("refuses %s", (_name, value) => {
    expect(parseIntegrationList(value)).toBeNull();
  });
});

describe("integrations.functions", () => {
  const fn = {
    id: "cmd:undo",
    kind: "command",
    name: "Undo",
    path: ["Edit"],
    category: "edit",
    keys: ["Ctrl+Z"],
  };
  const good = {
    revision: "r1",
    categories: [{ id: "edit", name: "Edit" }],
    functions: [
      fn,
      { ...fn, id: "tool:a", unconfirmed: true, cycle: ["tool:b"] },
    ],
  };

  it("accepts functions with their flags", () => {
    expect(parseIntegrationFunctions(good)?.functions[1]).toMatchObject({
      unconfirmed: true,
      cycle: ["tool:b"],
    });
  });

  it("drops an empty cycle and a false flag", () => {
    const parsed = parseIntegrationFunctions({
      ...good,
      functions: [{ ...fn, cycle: [], unsupported: false }],
    });
    expect(parsed?.functions[0]).toEqual(fn);
  });

  it.each([
    ["no revision", { ...good, revision: undefined }],
    ["categories not a list", { ...good, categories: {} }],
    ["a function without an ID", { ...good, functions: [{ ...fn, id: "" }] }],
    ["keys not strings", { ...good, functions: [{ ...fn, keys: [1] }] }],
    [
      "a flag that is not a boolean",
      { ...good, functions: [{ ...fn, unsupported: 1 }] },
    ],
    [
      "a cycle that is not strings",
      { ...good, functions: [{ ...fn, cycle: [1] }] },
    ],
  ])("refuses %s", (_name, value) => {
    expect(parseIntegrationFunctions(value)).toBeNull();
  });
});

describe("integrations.check", () => {
  it("accepts every status and the notes", () => {
    for (const status of [
      "free",
      "same",
      "cycle",
      "conflict",
      "unconfirmed",
      "unsupported",
    ])
      expect(
        parseIntegrationCheck({
          status,
          owners: [{ id: "a", name: "A", path: ["x"] }],
          notes: ["toolShift", "modifierKey"],
        })?.status,
      ).toBe(status);
  });

  it.each([
    ["an unknown status", { status: "maybe", owners: [], notes: [] }],
    ["an unknown note", { status: "free", owners: [], notes: ["loud"] }],
    [
      "an owner without a name",
      { status: "conflict", owners: [{ id: "a" }], notes: [] },
    ],
    ["no object", null],
  ])("refuses %s", (_name, value) => {
    expect(parseIntegrationCheck(value)).toBeNull();
  });
});

describe("integrations.items", () => {
  const item = { keys: "Ctrl+Z", function: "cmd:undo", name: "Undo", path: [] };
  const good = {
    revision: "r",
    items: [item],
    statuses: [
      {
        keys: "Ctrl+Z",
        function: "cmd:undo",
        state: "changed",
        current: ["Ctrl+U"],
      },
    ],
    consistent: false,
    dismissed: false,
    differenceId: "abc",
  };

  it("accepts items and statuses", () => {
    expect(parseIntegrationItems(good)).toEqual(good);
  });

  it.each([
    [
      "an unknown state",
      { ...good, statuses: [{ ...good.statuses[0], state: "gone" }] },
    ],
    ["consistent missing", { ...good, consistent: undefined }],
    ["an item without keys", { ...good, items: [{ ...item, keys: "" }] }],
    [
      "more than 512 items",
      { ...good, items: Array.from({ length: 513 }, () => item) },
    ],
    [
      "current not strings",
      { ...good, statuses: [{ ...good.statuses[0], current: [1] }] },
    ],
  ])("refuses %s", (_name, value) => {
    expect(parseIntegrationItems(value)).toBeNull();
  });
});

describe("integrations.plan", () => {
  const entry = {
    item: { keys: "P", function: "tool:a", name: "G", path: [] },
    changes: [{ function: "tool:a", add: "P" }],
    displaced: [{ function: "cmd:x", name: "X", keys: "P" }],
  };

  it("accepts entries, with a skipped reason as text", () => {
    expect(parseIntegrationPlan({ entries: [entry] })).toEqual({
      entries: [entry],
    });
    expect(
      parseIntegrationPlan({ entries: [{ ...entry, skipped: "gone" }] })
        ?.entries[0].skipped,
    ).toBe("gone");
  });

  it.each([
    ["no entries", {}],
    ["an entry without an item", { entries: [{ changes: [], displaced: [] }] }],
    [
      "a change without a function",
      { entries: [{ ...entry, changes: [{ add: "P" }] }] },
    ],
    [
      "a displaced function without a name",
      { entries: [{ ...entry, displaced: [{ function: "a", keys: "P" }] }] },
    ],
    ["a skipped that is a number", { entries: [{ ...entry, skipped: 3 }] }],
    [
      "a skipped that is an object",
      { entries: [{ ...entry, skipped: { reason: "gone" } }] },
    ],
  ])("refuses %s", (_name, value) => {
    expect(parseIntegrationPlan(value)).toBeNull();
  });
});

describe("write, backups and restore", () => {
  const backup = {
    id: "20261104T100000Z",
    created: "2026-11-04T10:00:00Z",
    reason: "write",
    changes: 3,
  };

  it("accepts a write result and a backup list, newest first as given", () => {
    expect(parseIntegrationWritten({ backup, revision: "r" })).toEqual({
      backup,
      revision: "r",
    });
    expect(
      parseIntegrationBackups([backup, { ...backup, id: "b" }]),
    ).toHaveLength(2);
    expect(parseIntegrationBackups([backup])).toEqual([backup]);
    const before = { ...backup, reason: "beforeRestore", changes: 0 };
    expect(parseIntegrationRestored(before)).toEqual(before);
  });

  it("takes a null backup of a write that changed nothing", () => {
    expect(parseIntegrationWritten({ backup: null, revision: "r" })).toEqual({
      backup: null,
      revision: "r",
    });
  });

  it("no longer takes wrappers or other reasons", () => {
    expect(parseIntegrationBackups({ backups: [backup] })).toBeNull();
    expect(parseIntegrationRestored({ backup })).toBeNull();
    expect(parseIntegrationRestored({ ...backup, reason: "other" })).toBeNull();
    expect(parseIntegrationRestored({ ...backup, changes: -1 })).toBeNull();
    expect(parseIntegrationRestored({ id: "a", created: "x" })).toBeNull();
  });

  it.each([
    parseIntegrationWritten({ revision: "r" }),
    parseIntegrationWritten({
      backup: { id: "", created: "x", reason: "write", changes: 0 },
      revision: "r",
    }),
    parseIntegrationBackups([{ id: "a" }]),
    parseIntegrationBackups("nope"),
    parseIntegrationRestored(null),
  ])("refuses a malformed result %#", (value) => {
    expect(value).toBeNull();
  });
});

describe("integration.changed", () => {
  it("accepts the three kinds", () => {
    for (const kind of ["files", "running", "differences"])
      expect(
        parseIntegrationChanged({
          integration: "demoapp",
          kind,
          profile: "paint",
          differenceId: "d",
          count: 2,
        })?.kind,
      ).toBe(kind);
  });

  it.each([
    { integration: "Demo", kind: "files" },
    { integration: "demoapp", kind: "other" },
    { integration: "demoapp", kind: "files", count: -1 },
    { integration: "demoapp", kind: "files", count: 1.5 },
    "files",
  ])("refuses %j", (value) => {
    expect(parseIntegrationChanged(value)).toBeNull();
  });
});

describe("isBound", () => {
  const list: IntegrationInfo[] = [
    { ...info, profiles: ["paint"] },
    { ...info, id: "other", profiles: [] },
    { ...info, id: "lost", found: false, profiles: ["paint"] },
  ];

  it("reads only the list's profiles", () => {
    expect(isBound(list, "paint", "demoapp")).toBe(true);
    expect(isBound(list, "default", "demoapp")).toBe(false);
    expect(isBound(list, "paint", "other")).toBe(false);
    expect(isBound(list, "paint", "missing")).toBe(false);
    expect(isBound(null, "paint", "demoapp")).toBe(false);
    expect(isBound(undefined, "paint", "demoapp")).toBe(false);
  });

  it("lists the bound integrations the app found", () => {
    expect(boundIntegrations(list, "paint").map((entry) => entry.id)).toEqual([
      "demoapp",
    ]);
    expect(hasBound(list[0])).toBe(true);
    expect(hasBound(list[1])).toBe(false);
  });
});
