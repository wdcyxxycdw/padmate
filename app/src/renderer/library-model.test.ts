import { describe, expect, it } from "vitest";
import type { MacroInfo, ProfileEntry } from "../shared/api";
import type { Config } from "../shared/generated/schema";
import {
  appBoundTo,
  bindApp,
  bindingConflict,
  blankProfileText,
  changeConfigText,
  configText,
  copyName,
  DEFAULT_PROFILE_ID,
  defaultProfileName,
  descendantsOf,
  effectiveRule,
  exeLabel,
  failureText,
  isDefaultProfile,
  isExePath,
  isRevisionConflict,
  type LibraryFailureKind,
  libraryFailure,
  matchExe,
  nameProblemText,
  nextActiveAfterDelete,
  normalizeExe,
  parentChoices,
  parentName,
  parseConfigText,
  profileHasScript,
  profileName,
  profileNameProblem,
  profilesBasedOn,
  readConfig,
  removeProfileReferences,
  renameProfileText,
  restoreProfileReferences,
  rulesFor,
  sameExe,
  setRuleEnabled,
  setSwitching,
  slugId,
  sortMacros,
  sortMacrosByName,
  sortProfiles,
  sortProfilesByName,
  switchingEnabled,
  unbindApp,
  uniqueName,
  validExe,
  validLibraryID,
  withoutRetired,
} from "./library-model";

const config = (value: Omit<Config, "schemaVersion">): Config => ({
  schemaVersion: 0,
  ...value,
});
const rules = (next: Config | null) => next?.rules;
/** A config.json with members ADR-0053 retired, as older files have. */
const legacy = (value: Record<string, unknown>): Config =>
  ({ schemaVersion: 0, ...value }) as Config;

describe("executable patterns (mirroring profile/config.go)", () => {
  it.each([
    ["paint.exe", true],
    ["Paint.EXE", true],
    ["paint", true],
    ["C:\\Art\\paint.exe", true],
    ["c:/art/paint.exe", true],
    ["\\\\server\\share\\paint.exe", true],
    ["//server/share/paint.exe", true],
    ["", false],
    [" paint.exe", false],
    ["paint.exe ", false],
    ["*.exe", false],
    ["pa?nt.exe", false],
    ['"paint.exe"', false],
    ["a<b.exe", false],
    ["a|b.exe", false],
    ["paint\u0000.exe", false],
    ["relative/paint.exe", false],
    ["relative\\paint.exe", false],
    ["C:paint.exe", false],
    ["C:\\", false],
    ["C:\\Art\\", false],
    ["1:\\paint.exe", false],
    ["\\\\server\\share", false],
    [".", false],
    ["..", false],
  ])("validExe(%j) = %s", (exe, want) => {
    expect(validExe(exe)).toBe(want);
  });

  it.each([
    ["paint.exe", "C:\\Art\\PAINT.EXE", true],
    ["paint.exe", "C:/Art/paint.exe", true],
    ["C:\\Art\\paint.exe", "c:\\art\\Paint.exe", true],
    ["C:\\Art\\paint.exe", "D:\\Art\\paint.exe", false],
    ["paint.exe", "C:\\Art\\paint2.exe", false],
    ["paint.exe", "", false],
    ["C:\\Art\\..\\Art\\paint.exe", "C:\\Art\\paint.exe", true],
    ["C:\\Art\\.\\paint.exe", "c:/ART//paint.exe", true],
    ["paint.exe", "paint.exe", true],
  ])("matchExe(%j, %j) = %s", (pattern, path, want) => {
    expect(matchExe(pattern, path)).toBe(want);
  });

  it("normalizes like path.Clean on lower-case forward slashes", () => {
    expect(normalizeExe("C:\\Art\\..\\Tools\\.\\Paint.exe")).toBe(
      "c:/tools/paint.exe",
    );
    expect(normalizeExe("\\\\Server\\Share\\a.exe")).toBe(
      "/server/share/a.exe",
    );
    expect(normalizeExe("")).toBe(".");
    expect(normalizeExe("../a.exe")).toBe("../a.exe");
    expect(sameExe("Paint.EXE", "paint.exe")).toBe(true);
    expect(sameExe("C:\\a\\paint.exe", "paint.exe")).toBe(false);
  });

  it("labels and recognises paths", () => {
    expect(exeLabel("C:\\Program Files\\Krita\\krita.exe")).toBe("krita.exe");
    expect(exeLabel("//server/share/Paint.exe")).toBe("Paint.exe");
    expect(exeLabel("Photoshop.exe")).toBe("Photoshop.exe");
    expect(isExePath("Photoshop.exe")).toBe(false);
    expect(isExePath("C:\\a\\b.exe")).toBe(true);
  });
});

describe("config.json documents", () => {
  it("parses objects only and strips a byte order mark", () => {
    expect(parseConfigText('\uFEFF{"schemaVersion":0}')).toEqual({
      schemaVersion: 0,
    });
    expect(() => parseConfigText("[]")).toThrow();
    expect(() => parseConfigText("null")).toThrow();
    expect(() => parseConfigText("{")).toThrow();
    expect(readConfig(null)).toBeNull();
    expect(
      readConfig({ text: "{}", revision: "r", problem: "config.json:1:1" }),
    ).toBeNull();
    expect(readConfig({ text: "nope", revision: "r" })).toBeNull();
    expect(readConfig({ text: '{"schemaVersion":0}', revision: "r" })).toEqual({
      schemaVersion: 0,
    });
  });

  it("rewrites with two spaces, keeps members, order and a byte order mark", () => {
    const text =
      '\uFEFF{"schemaVersion":0,"future":{"x":1},"enabled":false,"rules":[{"exe":"a.exe","profile":"a","device":"any"}],"output":{"eventGapMs":0}}';
    const next = changeConfigText(text, (value) => setSwitching(value, true));
    expect(next).toBe(
      `\uFEFF${JSON.stringify(
        {
          schemaVersion: 0,
          future: { x: 1 },
          enabled: true,
          rules: [{ exe: "a.exe", profile: "a", device: "any" }],
          output: { eventGapMs: 0 },
        },
        null,
        2,
      )}\n`,
    );
    expect(changeConfigText(text, (value) => setSwitching(value, false))).toBe(
      null,
    );
    expect(configText({ schemaVersion: 0 })).toBe(
      '{\n  "schemaVersion": 0\n}\n',
    );
  });

  it("does not mutate the parsed input", () => {
    const source = legacy({
      defaultProfile: "a",
      onUnmatched: "default",
      ignore: ["x.exe"],
      rules: [
        { exe: "a.exe", profile: "a", enabled: false },
        { exe: "x.exe", profile: null },
      ],
    });
    const frozen = JSON.stringify(source);
    bindApp(source, { exe: "b.exe" }, "a");
    setRuleEnabled(source, "a.exe", "a", true);
    removeProfileReferences(source, "a");
    withoutRetired(source);
    expect(JSON.stringify(source)).toBe(frozen);
  });

  it("reads the switch with its default", () => {
    expect(switchingEnabled(null)).toBe(true);
    expect(switchingEnabled(config({}))).toBe(true);
    expect(switchingEnabled(config({ enabled: false }))).toBe(false);
  });

  it("drops the retired members from every write (ADR-0053)", () => {
    const old = legacy({
      $schema: "x",
      defaultProfile: "paint",
      onUnmatched: "none",
      enabled: true,
      ignore: ["explorer.exe"],
      rules: [
        { exe: "game.exe", profile: null },
        7,
        { exe: "krita.exe", profile: "paint", device: "any" },
      ],
      future: 1,
    });
    expect(withoutRetired(old)).toEqual({
      schemaVersion: 0,
      $schema: "x",
      enabled: true,
      rules: [7, { exe: "krita.exe", profile: "paint", device: "any" }],
      future: 1,
    });
    // Nothing to drop: the same object.
    const current = config({ rules: [{ exe: "a.exe", profile: "a" }] });
    expect(withoutRetired(current)).toBe(current);
    // Any change writes the file without them, keeping the rest in place.
    const text = changeConfigText(JSON.stringify(old), (value) =>
      setSwitching(value, false),
    );
    expect(Object.keys(JSON.parse(text ?? "{}"))).toEqual([
      "schemaVersion",
      "$schema",
      "enabled",
      "rules",
      "future",
    ]);
    expect(JSON.parse(text ?? "{}").enabled).toBe(false);
    // No change, no write: the old members stay until the next one.
    expect(
      changeConfigText(JSON.stringify(old), (value) =>
        setSwitching(value, true),
      ),
    ).toBeNull();
  });

  const selection = legacy({
    defaultProfile: "fallback",
    ignore: ["explorer.exe"],
    rules: [
      { exe: "paint.exe", profile: "off", enabled: false },
      { exe: "C:\\Art\\paint.exe", profile: "paint" },
      { exe: "game.exe", profile: null },
      { exe: "paint.exe", profile: "other", device: "any" },
    ],
  });

  it("lists rules per profile in file order with labels", () => {
    expect(rulesFor(selection, "paint")).toEqual([
      {
        index: 1,
        exe: "C:\\Art\\paint.exe",
        profile: "paint",
        enabled: true,
        label: "paint.exe",
        isPath: true,
      },
    ]);
    expect(rulesFor(selection, "off")[0]?.enabled).toBe(false);
    expect(rulesFor(selection, "missing")).toEqual([]);
    expect(rulesFor(null, "paint")).toEqual([]);
  });

  it.each([
    [{ exe: "PAINT.EXE", path: "c:\\Art\\PAINT.EXE" }, "paint"],
    [{ exe: "paint.exe", path: "D:\\Other\\paint.exe" }, "other"],
    [{ exe: "paint.exe" }, "other"],
    [{ exe: "C:\\Art\\paint.exe" }, "paint"],
    // A retired null rule selects nothing: the app uses the default.
    [{ exe: "game.exe", path: "C:\\game.exe" }, undefined],
    [{ exe: "unmatched.exe", path: "C:\\unmatched.exe" }, undefined],
  ])("effectiveRule(%j) selects as Select does", (app, want) => {
    expect(effectiveRule(selection, app)?.profile).toBe(want);
  });

  it("reports a binding conflict only for another target", () => {
    const app = { exe: "paint.exe", path: "C:\\Art\\paint.exe" };
    expect(bindingConflict(selection, app, "paint")).toBeNull();
    expect(bindingConflict(selection, app, "other")?.profile).toBe("paint");
    expect(bindingConflict(selection, { exe: "new.exe" }, "paint")).toBeNull();
    expect(appBoundTo(selection, { exe: "paint.exe" }, "off")).toBe(true);
    expect(appBoundTo(selection, { exe: "paint.exe" }, "paint")).toBe(false);
    expect(appBoundTo(selection, app, "paint")).toBe(true);
  });
});

describe("binding apps", () => {
  it("appends a new app and keeps other rules intact", () => {
    const source = config({
      rules: [{ exe: "a.exe", profile: "a", device: "any" }],
    });
    expect(rules(bindApp(source, { exe: "Krita.exe" }, "paint"))).toEqual([
      { exe: "a.exe", profile: "a", device: "any" },
      { exe: "Krita.exe", profile: "paint" },
    ]);
    expect(
      rules(
        bindApp(config({}), { exe: "game.exe", path: "C:\\g\\game.exe" }, "x"),
      ),
    ).toEqual([{ exe: "game.exe", profile: "x" }]);
    // A retired null rule for the app goes with the binding.
    expect(
      rules(
        bindApp(
          config({ rules: [{ exe: "game.exe", profile: null }] }),
          { exe: "game.exe" },
          "x",
        ),
      ),
    ).toEqual([{ exe: "game.exe", profile: "x" }]);
  });

  it("ignores an app that already uses the profile", () => {
    const source = config({ rules: [{ exe: "Krita.exe", profile: "paint" }] });
    expect(bindApp(source, { exe: "krita.EXE" }, "paint")).toBeNull();
    expect(
      bindApp(source, { exe: "krita.exe", path: "C:\\K\\krita.exe" }, "paint"),
    ).toBeNull();
  });

  it("moves an app from another profile and drops its old rules", () => {
    const source = config({
      rules: [
        { exe: "krita.exe", profile: "a" },
        { exe: "x.exe", profile: "x" },
        { exe: "KRITA.exe", profile: "c", enabled: false },
      ],
    });
    expect(rules(bindApp(source, { exe: "krita.exe" }, "b"))).toEqual([
      { exe: "x.exe", profile: "x" },
      { exe: "krita.exe", profile: "b" },
    ]);
  });

  it("enables a disabled rule for the profile in place, keeping its members", () => {
    const source = config({
      rules: [
        { exe: "x.exe", profile: "x" },
        { exe: "krita.exe", profile: "b", enabled: false, device: "any" },
        { exe: "krita.exe", profile: "a" },
      ],
    });
    expect(rules(bindApp(source, { exe: "krita.exe" }, "b"))).toEqual([
      { exe: "x.exe", profile: "x" },
      { exe: "krita.exe", profile: "b", device: "any" },
    ]);
  });

  it("replaces a path rule for a seen app that names the same file", () => {
    const source = config({
      rules: [
        { exe: "C:\\Art\\paint.exe", profile: "a" },
        { exe: "D:\\Other\\paint.exe", profile: "a" },
      ],
    });
    const app = { exe: "paint.exe", path: "c:\\art\\paint.exe" };
    expect(bindingConflict(source, app, "b")?.exe).toBe("C:\\Art\\paint.exe");
    expect(rules(bindApp(source, app, "b"))).toEqual([
      { exe: "D:\\Other\\paint.exe", profile: "a" },
      { exe: "paint.exe", profile: "b" },
    ]);
  });

  it("puts a typed path ahead of a broader name so that it wins", () => {
    const source = config({
      rules: [
        { exe: "x.exe", profile: "x" },
        { exe: "paint.exe", profile: "a" },
      ],
    });
    const app = { exe: "C:\\Art\\paint.exe" };
    expect(bindingConflict(source, app, "b")?.profile).toBe("a");
    const next = bindApp(source, app, "b");
    expect(rules(next)).toEqual([
      { exe: "x.exe", profile: "x" },
      { exe: "C:\\Art\\paint.exe", profile: "b" },
      { exe: "paint.exe", profile: "a" },
    ]);
    expect(effectiveRule(next, app)?.profile).toBe("b");
    expect(
      effectiveRule(next, { exe: "paint.exe", path: "D:\\paint.exe" })?.profile,
    ).toBe("a");
  });

  it("moves a kept rule ahead of a broader rule that would win", () => {
    const source = config({
      rules: [
        { exe: "paint.exe", profile: "a" },
        { exe: "C:\\Art\\paint.exe", profile: "b", enabled: false },
      ],
    });
    const next = bindApp(source, { exe: "C:\\Art\\paint.exe" }, "b");
    expect(rules(next)).toEqual([
      { exe: "C:\\Art\\paint.exe", profile: "b" },
      { exe: "paint.exe", profile: "a" },
    ]);
  });

  it("keeps entries that are not rules", () => {
    const source = {
      schemaVersion: 0,
      rules: [7, { exe: "a.exe", profile: "a" }],
    } as unknown as Config;
    expect(rules(bindApp(source, { exe: "b.exe" }, "b"))).toEqual([
      7,
      { exe: "a.exe", profile: "a" },
      { exe: "b.exe", profile: "b" },
    ]);
    expect(rulesFor(source, "a")[0]?.index).toBe(1);
  });

  it("unbinds and toggles by pattern and target", () => {
    const source = config({
      rules: [
        { exe: "krita.exe", profile: "a", device: "any" },
        { exe: "KRITA.EXE", profile: "b" },
        { exe: "game.exe", profile: "c" },
      ],
    });
    expect(rules(unbindApp(source, "Krita.exe", "a"))).toEqual([
      { exe: "KRITA.EXE", profile: "b" },
      { exe: "game.exe", profile: "c" },
    ]);
    expect(unbindApp(source, "game.exe", "a")).toBeNull();
    const off = setRuleEnabled(source, "krita.exe", "a", false);
    expect(rules(off)?.[0]).toEqual({
      exe: "krita.exe",
      profile: "a",
      device: "any",
      enabled: false,
    });
    expect(rules(off)?.[1]).toEqual({ exe: "KRITA.EXE", profile: "b" });
    expect(setRuleEnabled(source, "krita.exe", "a", true)).toBeNull();
    expect(
      rules(setRuleEnabled(off as Config, "krita.exe", "a", true))?.[0],
    ).toEqual({
      exe: "krita.exe",
      profile: "a",
      device: "any",
    });
  });
});

describe("switching settings", () => {
  it("writes enabled only when it changes", () => {
    expect(setSwitching(config({}), true)).toBeNull();
    expect(setSwitching(config({}), false)).toEqual(config({ enabled: false }));
    expect(setSwitching(config({ enabled: false }), true)?.enabled).toBe(true);
  });

  it("removes every rule of a deleted profile", () => {
    const source = legacy({
      defaultProfile: "paint",
      onUnmatched: "default",
      rules: [
        { exe: "a.exe", profile: "paint" },
        { exe: "b.exe", profile: "other" },
        { exe: "c.exe", profile: "paint", enabled: false },
      ],
    });
    // defaultProfile selects nothing now, so it is no reference.
    expect(removeProfileReferences(source, "paint")).toEqual(
      legacy({
        defaultProfile: "paint",
        onUnmatched: "default",
        rules: [{ exe: "b.exe", profile: "other" }],
      }),
    );
    expect(removeProfileReferences(source, "missing")).toBeNull();
    expect(
      removeProfileReferences(legacy({ defaultProfile: "x" }), "x"),
    ).toBeNull();
  });

  describe("putting back the references of a profile that was not deleted", () => {
    const before = legacy({
      defaultProfile: "paint",
      future: 1,
      rules: [
        { exe: "a.exe", profile: "paint", device: "any" },
        { exe: "b.exe", profile: "other" },
        { exe: "c.exe", profile: "paint", enabled: false },
      ],
    });
    const after = removeProfileReferences(before, "paint") as Config;
    // What the store wrote: without the retired members.
    const written = withoutRetired(after);

    it("returns the whole document, as written, while nothing else changed it", () => {
      const current = JSON.parse(JSON.stringify(written));
      const restored = restoreProfileReferences(
        current,
        before,
        after,
        "paint",
      );
      expect(restored).toEqual(withoutRetired(before));
      expect(Object.keys(restored ?? {})).toEqual([
        "schemaVersion",
        "future",
        "rules",
      ]);
      expect(
        restoreProfileReferences(
          withoutRetired(before),
          before,
          before,
          "paint",
        ),
      ).toBeNull();
    });

    it("merges into a file that changed since", () => {
      const current: Config = {
        ...written,
        enabled: false,
        rules: [
          { exe: "b.exe", profile: "other" },
          { exe: "d.exe", profile: "other" },
        ],
      };
      expect(restoreProfileReferences(current, before, after, "paint")).toEqual(
        {
          ...current,
          rules: [
            { exe: "a.exe", profile: "paint", device: "any" },
            { exe: "b.exe", profile: "other" },
            { exe: "c.exe", profile: "paint", enabled: false },
            { exe: "d.exe", profile: "other" },
          ],
        },
      );
    });

    it("leaves what someone decided since alone", () => {
      const current: Config = {
        ...written,
        rules: [
          { exe: "A.EXE", profile: "other" },
          { exe: "b.exe", profile: "other" },
        ],
      };
      expect(restoreProfileReferences(current, before, after, "paint")).toEqual(
        {
          ...current,
          rules: [
            { exe: "A.EXE", profile: "other" },
            { exe: "b.exe", profile: "other" },
            { exe: "c.exe", profile: "paint", enabled: false },
          ],
        },
      );
      const settled: Config = {
        ...current,
        rules: [
          { exe: "a.exe", profile: "x" },
          { exe: "c.exe", profile: "x" },
        ],
      };
      expect(
        restoreProfileReferences(settled, before, after, "paint"),
      ).toBeNull();
    });
  });
});

describe("profile IDs and names", () => {
  it.each([
    ["paint", true],
    ["a", true],
    ["0-9_x", true],
    ["a".repeat(64), true],
    ["a".repeat(65), false],
    ["", false],
    ["-a", false],
    ["_a", false],
    ["Paint", false],
    ["a b", false],
    ["../x", false],
    ["con", false],
    ["nul", false],
    ["com1", false],
    ["lpt0", false],
    ["com10", true],
    ["console", true],
  ])("validLibraryID(%j) = %s", (id, want) => {
    expect(validLibraryID(id)).toBe(want);
  });

  it.each([
    ["Photoshop", [], "photoshop"],
    ["My Paint 2", [], "my-paint-2"],
    ["  --Clip  Studio!!  ", [], "clip-studio"],
    ["Café Ünïcode", [], "cafe-unicode"],
    ["ＡＢＣ", [], "abc"],
    ["绘画", [], "profile"],
    ["绘画", ["profile"], "profile-2"],
    ["Paint", ["paint", "paint-2"], "paint-3"],
    ["Paint", ["PAINT"], "paint-2"],
    ["Krita 绘画", [], "krita"],
    ["con", [], "con-2"],
    ["COM1", [], "com1-2"],
    // The fixed default's ID is never given out, listed or not.
    ["Default", [], "default-2"],
    ["DEFAULT", ["default"], "default-2"],
  ])("slugId(%j, %j) = %j", (name, existing, want) => {
    expect(slugId(name, existing)).toBe(want);
  });

  it("keeps long IDs within 64 characters, suffix included", () => {
    const long = `${"a".repeat(62)} bc`;
    expect(slugId(long, [])).toBe(`${"a".repeat(62)}-b`);
    const taken = slugId(long, []);
    const next = slugId(long, [taken]);
    expect(next).toBe(`${"a".repeat(62)}-2`);
    expect(next.length).toBeLessThanOrEqual(64);
    expect(validLibraryID(next)).toBe(true);
  });

  it("checks names", () => {
    expect(profileNameProblem("")).toBe("empty");
    expect(profileNameProblem("   ")).toBe("empty");
    expect(profileNameProblem("绘".repeat(64))).toBeNull();
    expect(profileNameProblem("绘".repeat(65))).toBe("tooLong");
    expect(profileNameProblem(" Paint ")).toBeNull();
    expect(nameProblemText("empty", "zh-CN")).toBe("名称不能为空");
    expect(nameProblemText("tooLong", "en")).toContain("64");
    expect(nameProblemText("taken", "zh-CN")).toBe("已有同名配置");
    expect(nameProblemText("taken", "en")).toBe(
      "A profile with this name exists",
    );
  });

  it("keeps names unique, trimmed and in any case (F2)", () => {
    const library: ProfileEntry[] = [
      { id: "default", name: "Whatever it stores", default: true },
      { id: "paint", name: " 绘画 " },
      { id: "krita", name: "Krita" },
      { id: "noname" },
    ];
    expect(profileNameProblem("绘画", library)).toBe("taken");
    expect(profileNameProblem("  KRITA ", library)).toBe("taken");
    expect(profileNameProblem("noname", library)).toBe("taken");
    // The fixed default holds both its names, not what it stores.
    expect(profileNameProblem("默认", library)).toBe("taken");
    expect(profileNameProblem("default", library)).toBe("taken");
    expect(profileNameProblem("Whatever it stores", library)).toBeNull();
    // Renaming a profile to its own name, in another case, is fine.
    expect(profileNameProblem("krita", library, "krita")).toBeNull();
    expect(profileNameProblem("绘画", library, "krita")).toBe("taken");
    expect(profileNameProblem("Krita 2", library)).toBeNull();
  });

  it("numbers a name another profile has", () => {
    const library: ProfileEntry[] = [
      { id: "default" },
      { id: "a", name: "绘画" },
      { id: "b", name: "绘画 2" },
    ];
    expect(uniqueName("Krita", library)).toBe("Krita");
    expect(uniqueName("绘画", library)).toBe("绘画 3");
    expect(uniqueName("Default", library)).toBe("Default 2");
    const long = uniqueName("x".repeat(64), [
      { id: "x", name: "x".repeat(64) },
    ]);
    expect(long).toBe(`${"x".repeat(62)} 2`);
  });

  it("names copies within the limit, numbered when taken", () => {
    expect(copyName("Paint", "zh-CN")).toBe("Paint 副本");
    expect(copyName("Paint", "en")).toBe("Paint copy");
    const library: ProfileEntry[] = [
      { id: "paint", name: "绘画" },
      { id: "paint-2", name: "绘画 副本" },
      { id: "paint-3", name: "绘画 副本 2" },
    ];
    expect(copyName("绘画", "zh-CN", library)).toBe("绘画 副本 3");
    expect(copyName("默认", "zh-CN", [{ id: "default" }])).toBe("默认 副本");
    const copied = copyName("x".repeat(64), "zh-CN");
    expect([...copied].length).toBeLessThanOrEqual(64);
    expect(copied.endsWith(" 副本")).toBe(true);
    const again = copyName("x".repeat(64), "zh-CN", [
      { id: "c", name: copied },
    ]);
    expect(again).toBe(`${copied} 2`);
  });

  it("writes a blank profile", () => {
    expect(blankProfileText("绘画")).toBe(
      '{\n  "schemaVersion": 1,\n  "name": "绘画",\n  "layers": {\n    "base": {}\n  }\n}\n',
    );
  });

  it("renames without touching the rest of the text", () => {
    const text =
      '{\r\n\t"schemaVersion": 0,  "name" : "Old \\"one\\"",\r\n\t"layers": {"base": {"name": "keep", "x": [1, {"name": "y"}]}},\r\n\t"note": "}"\r\n}\r\n';
    const renamed = renameProfileText(text, '新 "名"');
    expect(renamed).toBe(text.replace('"Old \\"one\\""', '"新 \\"名\\""'));
    expect(JSON.parse(renamed).name).toBe('新 "名"');
    expect(JSON.parse(renamed).layers.base.name).toBe("keep");
    expect(renameProfileText(text, 'Old "one"')).toBe(text);
  });

  it("tells whether a profile names a script", () => {
    expect(
      profileHasScript(
        '\uFEFF{"schemaVersion":0,"script":{"entry":"main.ts","api":"0.21"}}',
      ),
    ).toBe(true);
    for (const text of [
      '{"schemaVersion":0,"layers":{"base":{}}}',
      '{"schemaVersion":0,"script":null}',
      '["script"]',
      "{",
    ])
      expect(profileHasScript(text)).toBe(false);
  });

  it("adds a missing name after schemaVersion", () => {
    expect(
      renameProfileText('{"layers":{"base":{}},"schemaVersion":0}', "P"),
    ).toBe(
      '{\n  "layers": {\n    "base": {}\n  },\n  "schemaVersion": 0,\n  "name": "P"\n}\n',
    );
    expect(renameProfileText('\uFEFF{"schemaVersion":0}', "P")).toBe(
      '\uFEFF{\n  "schemaVersion": 0,\n  "name": "P"\n}\n',
    );
    expect(renameProfileText('\uFEFF{"name":"a","schemaVersion":0}', "b")).toBe(
      '\uFEFF{"name":"b","schemaVersion":0}',
    );
  });

  it("refuses to rename a document that is not an object", () => {
    expect(() => renameProfileText("[]", "x")).toThrow();
    expect(() => renameProfileText("{", "x")).toThrow();
  });

  const entries: ProfileEntry[] = [
    { id: "zeta", name: "Zeta" },
    { id: "broken", problem: "profile.json:1:1: unexpected token" },
    { id: "alpha", name: "alpha" },
    { id: "paint10", name: "Paint 10" },
    { id: "paint2", name: "Paint 2" },
    { id: "blank", name: "  " },
  ];

  it("lists profiles by creation time, oldest first, the fixed default first", () => {
    const dated: ProfileEntry[] = [
      { id: "zeta", name: "Zeta", created: 3000 },
      { id: "alpha", name: "alpha", created: 2000 },
      { id: "default", name: "Zzz", default: true, created: 9000 },
      { id: "mid", name: "Mid", created: 1000 },
    ];
    expect(sortProfiles(dated).map((entry) => entry.id)).toEqual([
      "default",
      "mid",
      "alpha",
      "zeta",
    ]);
    expect(dated[0]?.id).toBe("zeta");
    expect(profileName({ id: "blank", name: "  " }, "zh-CN")).toBe("blank");
    expect(profileName({ id: "x", name: " X " }, "en")).toBe("X");
  });

  it("falls back to name, then ID, when creation times tie or are unknown", () => {
    expect(sortProfiles(entries).map((entry) => entry.id)).toEqual([
      "alpha",
      "blank",
      "broken",
      "paint2",
      "paint10",
      "zeta",
    ]);
    const mixed: ProfileEntry[] = [
      { id: "b", name: "Same" },
      { id: "a", name: "Same", created: 5 },
      { id: "c", name: "Same", created: 5 },
      { id: "d", name: "Aaa", created: 5 },
      { id: "e", name: "Zzz", created: 1 },
    ];
    // Unknown last; equal times by name, then by ID.
    expect(sortProfiles(mixed).map((entry) => entry.id)).toEqual([
      "e",
      "d",
      "a",
      "c",
      "b",
    ]);
    expect(
      sortProfiles([...entries, { id: "default", name: "Zzz" }]).map(
        (entry) => entry.id,
      )[0],
    ).toBe("default");
  });

  it("sorts profiles to pick from in dictionary order, the fixed default first", () => {
    const created: ProfileEntry[] = entries.map((entry, index) => ({
      ...entry,
      created: 100 - index,
    }));
    expect(
      sortProfilesByName([
        ...created,
        { id: "default", name: "Zzz", created: 1 },
      ]).map((entry) => entry.id),
    ).toEqual([
      "default",
      "alpha",
      "blank",
      "broken",
      "paint2",
      "paint10",
      "zeta",
    ]);
    expect(
      sortProfilesByName([
        { id: "b", name: "复制全部 10" },
        { id: "a", name: "复制全部 2" },
        { id: "c", name: "复制全部" },
      ]).map((entry) => entry.id),
    ).toEqual(["c", "a", "b"]);
  });

  const scripts: MacroInfo[] = [
    { file: "复制全部 10.ts", name: "复制全部 10", usedBy: [], created: 4 },
    { file: "复制全部 2.ts", name: "复制全部 2", usedBy: [], created: 2 },
    { file: "复制全部 3.ts", name: "复制全部 3", usedBy: [], created: 3 },
    { file: "复制全部.ts", name: "复制全部", usedBy: [], created: 1 },
  ];

  it("lists scripts by creation time, oldest first, unknown last by name", () => {
    expect(sortMacros(scripts).map((macro) => macro.file)).toEqual([
      "复制全部.ts",
      "复制全部 2.ts",
      "复制全部 3.ts",
      "复制全部 10.ts",
    ]);
    const mixed: MacroInfo[] = [
      { file: "z.ts", name: "z", usedBy: [] },
      { file: "b.ts", name: "b", usedBy: [], created: 7 },
      { file: "a.js", name: "a", usedBy: [] },
      { file: "a.ts", name: "a", usedBy: [], created: 7 },
    ];
    expect(sortMacros(mixed).map((macro) => macro.file)).toEqual([
      "a.ts",
      "b.ts",
      "a.js",
      "z.ts",
    ]);
    expect(scripts[0]?.file).toBe("复制全部 10.ts");
  });

  it("sorts scripts to pick from in dictionary order of the name", () => {
    expect(sortMacrosByName(scripts).map((macro) => macro.file)).toEqual([
      "复制全部.ts",
      "复制全部 2.ts",
      "复制全部 3.ts",
      "复制全部 10.ts",
    ]);
    expect(
      sortMacrosByName([
        { file: "b.ts", name: "b", usedBy: [] },
        { file: "a.ts", name: "a", usedBy: [] },
        { file: "a.js", name: "a", usedBy: [] },
        { file: "B.ts", name: "B", usedBy: [] },
      ]).map((macro) => macro.file),
    ).toEqual(["a.js", "a.ts", "B.ts", "b.ts"]);
  });

  it("orders Chinese names by pinyin, not by the system's locale", () => {
    // 爱 (ai) comes before 我 (wo), though 我 has the smaller code point.
    expect(
      sortMacrosByName([
        { file: "我.ts", name: "我", usedBy: [] },
        { file: "爱.ts", name: "爱", usedBy: [] },
      ]).map((macro) => macro.file),
    ).toEqual(["爱.ts", "我.ts"]);
    expect(
      sortProfilesByName([
        { id: "w", name: "我的配置" },
        { id: "a", name: "爱画画" },
      ]).map((entry) => entry.id),
    ).toEqual(["a", "w"]);
  });

  it("names the fixed default by its ID (PRO-28)", () => {
    expect(DEFAULT_PROFILE_ID).toBe("default");
    expect(isDefaultProfile("default")).toBe(true);
    expect(isDefaultProfile("Default")).toBe(false);
    expect(isDefaultProfile(null)).toBe(false);
    const entry = { id: "default", name: "Mine", default: true };
    expect(profileName(entry, "zh-CN")).toBe("默认");
    expect(profileName(entry, "en")).toBe("Default");
    expect(defaultProfileName("en")).toBe("Default");
  });

  it("switches to the fixed default before a delete", () => {
    const library = [...entries, { id: "default" }];
    expect(nextActiveAfterDelete(library, "zeta")).toBe("default");
    expect(nextActiveAfterDelete(entries, "zeta")).toBe("alpha");
    expect(
      nextActiveAfterDelete(
        [...entries, { id: "default", problem: "x" }],
        "zeta",
      ),
    ).toBe("alpha");
    expect(nextActiveAfterDelete([{ id: "only" }], "only")).toBeNull();
    expect(
      nextActiveAfterDelete([{ id: "a" }, { id: "b", problem: "x" }], "a"),
    ).toBeNull();
  });
});

describe("failures", () => {
  it("recognises revision conflicts with and without the code", () => {
    expect(isRevisionConflict({ message: "x", code: -32009 })).toBe(true);
    expect(
      isRevisionConflict(
        new Error(
          "Error invoking remote method 'padmate:configPut': Error: profile library: revision conflict; read the document again",
        ),
      ),
    ).toBe(true);
    expect(
      isRevisionConflict(new Error("config changed outside padmate")),
    ).toBe(false);
    expect(isRevisionConflict("nope")).toBe(false);
  });

  it.each<[unknown, LibraryFailureKind]>([
    [
      { message: "conflict", code: -32009, data: { revision: "r" } },
      "profileConflict",
    ],
    [{ message: "active", code: -32012 }, "active"],
    [
      { message: "ref", code: -32013, data: { references: ["startup"] } },
      "startup",
    ],
    [
      { message: "ref", code: -32013, data: { references: ["rules/0"] } },
      "referenced",
    ],
    [{ message: "ref", code: -32013 }, "referenced"],
    [{ message: "exists", code: -32014 }, "exists"],
    [{ message: "fixed", code: -32018, data: { op: "delete" } }, "fixedDelete"],
    [{ message: "fixed", code: -32018, data: { op: "rename" } }, "fixedRename"],
    [{ message: "fixed", code: -32018 }, "fixedDelete"],
    [{ message: "link", code: -32015, data: { path: "x" } }, "unsupported"],
    [{ message: "no library", code: -32008 }, "noLibrary"],
    [{ message: "zip", code: -32020, data: { reason: "format" } }, "archive"],
    [{ message: "big", data: { reason: "size" } }, "file"],
    [
      { message: "joystick", code: -32024, data: { reason: "joystick" } },
      "amgp",
    ],
    [new Error("boom"), "error"],
    ["boom", "error"],
  ])("classifies %j as %s", (error, kind) => {
    expect(libraryFailure(error).kind).toBe(kind);
  });

  it("keeps the daemon's message", () => {
    expect(libraryFailure({ message: "disk full" })).toEqual({
      kind: "error",
      message: "disk full",
    });
  });

  it("has one short line for every failure", () => {
    const kinds: LibraryFailureKind[] = [
      "configConflict",
      "profileConflict",
      "startup",
      "referenced",
      "active",
      "exists",
      "fixedDelete",
      "fixedRename",
      "unsupported",
      "disconnected",
      "noLibrary",
      "brokenConfig",
      "brokenProfile",
      "childBlocked",
      "notExportable",
      "archive",
      "file",
      "amgp",
    ];
    for (const kind of kinds)
      for (const language of ["zh-CN", "en"] as const) {
        const text = failureText({ kind, message: "raw" }, language);
        expect(text).toBeTruthy();
        expect(text).not.toContain("\n");
        expect(text).not.toBe("raw");
      }
    expect(failureText({ kind: "configConflict", message: "" }, "zh-CN")).toBe(
      "设置已在别处修改，已重新载入",
    );
    expect(failureText({ kind: "startup", message: "" }, "zh-CN")).toBe(
      "此配置为启动时指定的配置，无法删除",
    );
    expect(failureText({ kind: "error", message: "raw" }, "en")).toBe("raw");
    expect(failureText({ kind: "fixedDelete", message: "" }, "zh-CN")).toBe(
      "无法删除默认配置",
    );
    expect(failureText({ kind: "fixedRename", message: "" }, "zh-CN")).toBe(
      "无法重命名默认配置",
    );
  });

  it("explains a profile file that was refused (PRO-20)", () => {
    expect(
      libraryFailure({
        message: "profile archive: newer",
        code: -32020,
        data: { reason: "version", path: "manifest.json" },
      }),
    ).toEqual({
      kind: "archive",
      message: "profile archive: newer",
      reason: "version",
    });
    const archive = (reason: string, language: "zh-CN" | "en" = "zh-CN") =>
      failureText({ kind: "archive", message: "raw", reason }, language);
    expect(archive("format")).toBe("不是 padmate 配置文件，或文件已损坏");
    expect(archive("version")).toBe(
      "此文件由更新版本的 padmate 导出，请先更新 padmate",
    );
    expect(archive("entry")).toBe("文件中含有不支持的内容");
    expect(archive("limit")).toBe("文件内容超出导入限制");
    expect(archive("encoding")).toBe("文件中的脚本或 JSON 不是 UTF-8 编码");
    expect(archive("profile")).toBe("文件中的配置有错误");
    expect(archive("unknown")).toBe(archive("format"));
    expect(archive("version", "en")).toBe(
      "This file was exported by a newer padmate. Update padmate first",
    );
    const file = (reason: string, language: "zh-CN" | "en" = "zh-CN") =>
      failureText({ kind: "file", message: "raw", reason }, language);
    expect(file("size")).toBe("文件超过 4 MiB");
    expect(file("read")).toBe("无法读取所选文件");
    expect(file("write")).toBe("无法保存文件，请选择其他文件夹");
    expect(file("expired")).toBe("等待时间过长，请重新选择文件");
    expect(file("size", "en")).toBe("The file is larger than 4 MiB");
    expect(failureText({ kind: "notExportable", message: "" }, "zh-CN")).toBe(
      "配置文件夹中有无法导出的文件",
    );
    const blocked = {
      kind: "notExportable",
      message: "raw",
      path: "macros/abc~1.ts",
    } as const;
    expect(failureText(blocked, "zh-CN")).toBe(
      "配置文件夹中的「macros/abc~1.ts」无法导出",
    );
    expect(failureText(blocked, "en")).toBe(
      "“macros/abc~1.ts” in the profile folder can't be exported",
    );
  });
});

describe("an AntiMicroX file that was refused (PRO-21)", () => {
  it("keeps the reason and the daemon's words", () => {
    expect(
      libraryFailure({
        message: "配置版本是 5，太旧了",
        code: -32024,
        data: { reason: "version" },
      }),
    ).toEqual({
      kind: "amgp",
      message: "配置版本是 5，太旧了",
      reason: "version",
    });
    // A file over 1 MiB that Main refused has the same shape.
    expect(
      libraryFailure({
        message: "文件超过 1 MiB",
        code: -32024,
        data: { reason: "size" },
      }).reason,
    ).toBe("size");
  });

  it("says why in a line of its own, in both languages", () => {
    const amgp = (reason: string, language: "zh-CN" | "en" = "zh-CN") =>
      failureText({ kind: "amgp", message: "raw", reason }, language);
    for (const [reason, zh, en] of [
      ["size", "文件超过 1 MiB", "The file is larger than 1 MiB"],
      [
        "encoding",
        "文件不是 UTF-8 编码，请用 AntiMicroX 另存，或转成 UTF-8",
        "The file is not UTF-8. Save it again in AntiMicroX, or convert it",
      ],
      ["xml", "不是合法的 XML 文件", "The file is not valid XML"],
      [
        "limit",
        "文件的元素太多或嵌套太深",
        "The file has too many elements or nests too deeply",
      ],
      [
        "doctype",
        "文件含有 DOCTYPE 声明，出于安全不读取",
        "The file has a DOCTYPE, which padmate does not read",
      ],
      [
        "root",
        "这不是 AntiMicroX 的配置文件",
        "This is not an AntiMicroX profile",
      ],
      [
        "joystick",
        "这是原始摇杆的配置，只能导入游戏手柄的配置",
        "This profile is for a raw joystick. Only gamepad profiles can be imported",
      ],
      [
        "version",
        "这个配置的版本太旧，请用较新的 AntiMicroX 打开后另存一次",
        "This profile is from an old version of AntiMicro. Open it in a recent AntiMicroX and save it again",
      ],
      [
        "other",
        "无法导入这个 AntiMicroX 配置文件",
        "This AntiMicroX file cannot be imported",
      ],
    ]) {
      expect(amgp(reason)).toBe(zh);
      expect(amgp(reason, "en")).toBe(en);
    }
  });
});

describe("profiles based on others (PRO-19)", () => {
  const entry = (id: string, extendsId?: string, rest = {}): ProfileEntry => ({
    id,
    name: id,
    ...(extendsId ? { extends: extendsId } : {}),
    ...rest,
  });
  const ids = (list: readonly ProfileEntry[]) => list.map((item) => item.id);

  it("lists the profiles directly based on one, and all that are based on it", () => {
    const profiles = [
      entry("default"),
      entry("paint", "default"),
      entry("sketch", "paint"),
      entry("ink", "paint"),
      entry("photo"),
      entry("detail", "sketch"),
    ];
    expect(ids(profilesBasedOn(profiles, "paint"))).toEqual(["sketch", "ink"]);
    expect(ids(profilesBasedOn(profiles, "photo"))).toEqual([]);
    expect(ids(descendantsOf(profiles, "default")).sort()).toEqual([
      "detail",
      "ink",
      "paint",
      "sketch",
    ]);
    expect(ids(descendantsOf(profiles, "sketch"))).toEqual(["detail"]);
    expect(descendantsOf(profiles, "detail")).toEqual([]);
  });

  it("stops at a loop in what the library says", () => {
    const profiles = [entry("a", "b"), entry("b", "a")];
    expect(ids(descendantsOf(profiles, "a")).sort()).toEqual(["a", "b"]);
  });

  it("offers every other profile to a profile that has no relatives", () => {
    const profiles = [entry("paint"), entry("photo"), entry("ink")];
    expect(ids(parentChoices(profiles, "paint"))).toEqual(["ink", "photo"]);
  });

  it("puts the fixed default first, then the rest by name", () => {
    const profiles = [
      entry("zeta"),
      entry("default", undefined, { default: true }),
      entry("alpha"),
      entry("me"),
    ];
    expect(ids(parentChoices(profiles, "me"))).toEqual([
      "default",
      "alpha",
      "zeta",
    ]);
  });

  it("does not offer a profile itself, nor one that is based on it, however far", () => {
    const profiles = [
      entry("paint"),
      entry("sketch", "paint"),
      entry("detail", "sketch"),
      entry("photo"),
    ];
    expect(ids(parentChoices(profiles, "paint"))).toEqual(["photo"]);
    expect(ids(parentChoices(profiles, "sketch"))).toEqual(["paint", "photo"]);
    // A profile in the middle of a chain may be based on what is above it.
    expect(ids(parentChoices(profiles, "detail"))).toEqual([
      "paint",
      "photo",
      "sketch",
    ]);
  });

  it("does not offer a profile with errors", () => {
    const profiles = [
      entry("a"),
      entry("b", undefined, { problem: "bad" }),
      entry("c"),
    ];
    expect(ids(parentChoices(profiles, "a"))).toEqual(["c"]);
  });

  it("does not offer a profile that would make a chain of more than four parents", () => {
    // d is based on c, c on b, b on a: d has three parents.
    const profiles = [
      entry("a"),
      entry("b", "a"),
      entry("c", "b"),
      entry("d", "c"),
      entry("x"),
    ];
    // x on d gives x four parents (d, c, b, a): the most there can be.
    expect(ids(parentChoices(profiles, "x"))).toContain("d");
    // With e based on x and f on e, x on d would give f six parents. x can be
    // based on a (f has three) or b (four), not on c or d.
    const more = [...profiles, entry("e", "x"), entry("f", "e")];
    expect(ids(parentChoices(more, "x"))).toEqual(["a", "b"]);
    // a has b, c and d below it, three levels: only a profile without a
    // parent of its own can be its parent.
    expect(ids(parentChoices(more, "a"))).toEqual(["x"]);
  });

  it("names the parent a profile is based on", () => {
    const profiles = [
      entry("paint", undefined, { name: "Paint" }),
      entry("default", undefined, { default: true, name: "default" }),
    ];
    expect(parentName("paint", profiles, "zh-CN")).toBe("Paint");
    expect(parentName("default", profiles, "zh-CN")).toBe("默认");
    expect(parentName("default", profiles, "en")).toBe("Default");
    // A parent the library no longer lists goes by its ID.
    expect(parentName("gone", profiles, "en")).toBe("gone");
  });

  it("tells the profile a delete cannot write out whole (-32023)", () => {
    const refused = libraryFailure({
      message: "child cannot be flattened",
      code: -32023,
      data: { id: "sketch", reason: "script" },
    });
    expect(refused).toEqual({
      kind: "childBlocked",
      message: "child cannot be flattened",
      reason: "script",
      profile: "sketch",
    });
    expect(
      libraryFailure({
        message: "x",
        code: -32023,
        data: { id: "ink", reason: "invalid", diagnostics: [] },
      }),
    ).toMatchObject({
      kind: "childBlocked",
      reason: "invalid",
      profile: "ink",
    });
    // Whatever the data lacks is left empty.
    expect(libraryFailure({ message: "x", code: -32023 })).toEqual({
      kind: "childBlocked",
      message: "x",
    });
  });

  it("says why in a line, in both languages, with the profile's name", () => {
    const label = (id: string) => (id === "sketch" ? "草图" : id);
    const script = {
      kind: "childBlocked",
      message: "raw",
      reason: "script",
      profile: "sketch",
    } as const;
    expect(failureText(script, "zh-CN", label)).toBe(
      "基于它的「草图」继承了它的脚本，无法先写成完整内容",
    );
    expect(failureText(script, "en", label)).toBe(
      "“草图” is based on it and inherits its script, so it can't be written out whole first",
    );
    const invalid = { ...script, reason: "invalid" } as const;
    expect(failureText(invalid, "zh-CN", label)).toBe(
      "基于它的「草图」有错误，无法先写成完整内容",
    );
    expect(failureText(invalid, "en", label)).toBe(
      "“草图” is based on it and has errors, so it can't be written out whole first",
    );
    // Without a label for the profile, its ID stands in; without one at all,
    // the line still reads.
    expect(failureText(invalid, "en")).toBe(
      "“sketch” is based on it and has errors, so it can't be written out whole first",
    );
    expect(failureText({ kind: "childBlocked", message: "raw" }, "zh-CN")).toBe(
      "基于它的配置无法先写成完整内容",
    );
    expect(failureText({ kind: "childBlocked", message: "raw" }, "en")).toBe(
      "A profile based on it can't be written out whole first",
    );
  });
});
