import { describe, expect, it, vi } from "vitest";
import {
  APP_PATH_CACHE,
  APP_PATH_MISS_MS,
  type AppPathSource,
  appPathHandlers,
  appPathKeys,
  appPathResolver,
  decodeRegOutput,
  expandEnv,
  MUICACHE_CANDIDATES,
  muiCacheIndex,
  muiCacheKey,
  muiCacheSource,
  regExe,
  registeredPath,
  registrySource,
  registrySources,
} from "./app-paths";
import { validateExeName } from "./security";

const krita = {
  name: "Krita",
  exe: "krita.exe",
  path: "C:\\Program Files\\Krita (x64)\\bin\\krita.exe",
};

/**
 * A source with the programs of the Start Menu, the registry (App Paths and
 * the MuiCache) and the disk. The order the sources were asked in is kept in
 * `order`.
 */
function source(
  options: {
    installed?: (typeof krita)[] | Error;
    registered?: Record<string, string[] | Error>;
    launched?: Record<string, string[] | Error>;
    files?: string[];
    now?: () => number;
  } = {},
) {
  const files = new Set((options.files ?? []).map((f) => f.toLowerCase()));
  const order: string[] = [];
  const installed = vi.fn(async () => {
    order.push("installed");
    if (options.installed instanceof Error) throw options.installed;
    return options.installed ?? [];
  });
  const registered = vi.fn(async (exe: string) => {
    order.push("registered");
    const found = options.registered?.[exe.toLowerCase()] ?? [];
    if (found instanceof Error) throw found;
    return found;
  });
  const launched = vi.fn(async (exe: string) => {
    order.push("launched");
    const found = options.launched?.[exe.toLowerCase()] ?? [];
    if (found instanceof Error) throw found;
    return found;
  });
  const isFile = vi.fn(async (path: string) => files.has(path.toLowerCase()));
  const value: AppPathSource = {
    installed,
    registered,
    launched,
    isFile,
    ...(options.now ? { now: options.now } : {}),
  };
  return { value, installed, registered, launched, isFile, order };
}

describe("an exe name from the renderer (UI-42)", () => {
  it.each([
    "krita.exe",
    "Clip Studio Paint.exe",
    "画图.exe",
    "A.EXE",
    "vs_code-2.exe",
  ])("accepts %j", (name) => {
    expect(validateExeName(name)).toBe(name);
  });

  it.each([
    undefined,
    7,
    "",
    ".exe",
    "krita",
    "krita.txt",
    "krita.exe ",
    " krita.exe",
    "C:\\Apps\\krita.exe",
    "Apps\\krita.exe",
    "Apps/krita.exe",
    "\\\\server\\share\\krita.exe",
    "C:krita.exe",
    "kri:ta.exe",
    "kri*ta.exe",
    "kri?ta.exe",
    'kri"ta.exe',
    "kri<ta.exe",
    "kri|ta.exe",
    "kri\nta.exe",
    "kri\u0000ta.exe",
    `${"a".repeat(252)}.exe`,
  ])("refuses %j", (name) => {
    expect(() => validateExeName(name)).toThrow("Invalid exe name");
  });
});

describe("finding an executable by its name (UI-42)", () => {
  it("takes the program of the Start Menu with that file name, in any case", async () => {
    const s = source({ installed: [krita], files: [krita.path] });
    const resolve = appPathResolver(s.value);
    expect(await resolve("KRITA.exe")).toBe(krita.path);
    expect(s.registered).not.toHaveBeenCalled();
  });

  it("takes the first of programs that share a file name", async () => {
    const other = {
      ...krita,
      name: "Krita Next",
      path: "D:\\Krita\\krita.exe",
    };
    const s = source({ installed: [krita, other] });
    expect(await appPathResolver(s.value)("krita.exe")).toBe(krita.path);
  });

  it("falls back to the App Paths of the registry, an existing file only", async () => {
    const s = source({
      registered: {
        "sai2.exe": [
          // The current user's key names a file that is gone, the next one a
          // path nothing may read, the last one a file that exists.
          "C:\\Gone\\sai2.exe",
          "\\\\server\\share\\sai2.exe",
          "D:\\SAI2\\sai2.exe",
        ],
      },
      files: ["D:\\SAI2\\sai2.exe"],
    });
    const resolve = appPathResolver(s.value);
    expect(await resolve("sai2.exe")).toBe("D:\\SAI2\\sai2.exe");
    expect(s.installed).toHaveBeenCalledOnce();
    expect(s.registered).toHaveBeenCalledWith("sai2.exe");
    // Only local paths to an existing file were looked at on the disk.
    expect(s.isFile.mock.calls.map((call) => call[0])).toEqual([
      "C:\\Gone\\sai2.exe",
      "D:\\SAI2\\sai2.exe",
    ]);
  });

  it("asks the Start Menu, then App Paths, then the MuiCache, and stops at the first answer", async () => {
    const csp = "D:\\CSP\\CLIPStudioPaint.exe";
    const all = source({
      registered: { "clipstudiopaint.exe": [] },
      launched: { "clipstudiopaint.exe": [csp] },
      files: [csp],
    });
    expect(await appPathResolver(all.value)("CLIPStudioPaint.exe")).toBe(csp);
    expect(all.order).toEqual(["installed", "registered", "launched"]);

    // An App Paths key that names an existing file ends the search there.
    const registry = source({
      registered: { "sai2.exe": ["D:\\SAI2\\sai2.exe"] },
      launched: { "sai2.exe": ["E:\\Other\\sai2.exe"] },
      files: ["D:\\SAI2\\sai2.exe", "E:\\Other\\sai2.exe"],
    });
    expect(await appPathResolver(registry.value)("sai2.exe")).toBe(
      "D:\\SAI2\\sai2.exe",
    );
    expect(registry.launched).not.toHaveBeenCalled();

    // So does a program of the Start Menu.
    const menu = source({
      installed: [krita],
      launched: { "krita.exe": ["E:\\Other\\krita.exe"] },
      files: [krita.path, "E:\\Other\\krita.exe"],
    });
    expect(await appPathResolver(menu.value)("krita.exe")).toBe(krita.path);
    expect(menu.registered).not.toHaveBeenCalled();
    expect(menu.launched).not.toHaveBeenCalled();
  });

  it("goes on to the MuiCache when App Paths names no file that exists", async () => {
    const s = source({
      registered: { "paint.exe": ["C:\\Gone\\paint.exe"] },
      launched: { "paint.exe": ["D:\\Paint\\paint.exe"] },
      files: ["D:\\Paint\\paint.exe"],
    });
    expect(await appPathResolver(s.value)("paint.exe")).toBe(
      "D:\\Paint\\paint.exe",
    );
    expect(s.order).toEqual(["installed", "registered", "launched"]);
  });

  it("takes the first MuiCache path that is a local file that exists, skipping stale ones", async () => {
    const s = source({
      launched: {
        "clipstudiopaint.exe": [
          // The program was moved: this path is stale. Then a path nothing
          // may read, the live one, and one more that must not be looked at.
          "D:\\CSP\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
          "\\\\server\\share\\CLIPStudioPaint.exe",
          "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
          "D:\\Later\\CLIPStudioPaint.exe",
        ],
      },
      files: [
        "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
        "D:\\Later\\CLIPStudioPaint.exe",
      ],
    });
    expect(await appPathResolver(s.value)("CLIPStudioPaint.exe")).toBe(
      "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
    );
    // The stale path was looked at, the UNC path was not, and nothing after
    // the first file that exists was.
    expect(s.isFile.mock.calls.map((call) => call[0])).toEqual([
      "D:\\CSP\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
      "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe",
    ]);
  });

  it("answers an empty path when every MuiCache path is stale or the MuiCache fails", async () => {
    const stale = source({
      launched: { "old.exe": ["C:\\Gone\\old.exe", "D:\\Gone\\old.exe"] },
    });
    expect(await appPathResolver(stale.value)("old.exe")).toBe("");
    const failing = source({
      launched: { "x.exe": new Error("reg.exe timed out") },
    });
    expect(await appPathResolver(failing.value)("x.exe")).toBe("");
    const unreadable = source({ launched: { "x.exe": ["D:\\x\\x.exe"] } });
    unreadable.isFile.mockRejectedValue(new Error("EACCES"));
    expect(await appPathResolver(unreadable.value)("x.exe")).toBe("");
  });

  it("still asks the MuiCache when the Start Menu and App Paths both fail", async () => {
    const s = source({
      installed: new Error("scan failed"),
      registered: { "krita.exe": new Error("reg.exe timed out") },
      launched: { "krita.exe": [krita.path] },
      files: [krita.path],
    });
    expect(await appPathResolver(s.value)("krita.exe")).toBe(krita.path);
  });

  it("goes on to the registry when the Start Menu cannot be read", async () => {
    const s = source({
      installed: new Error("scan failed"),
      registered: { "krita.exe": [krita.path] },
      files: [krita.path],
    });
    expect(await appPathResolver(s.value)("krita.exe")).toBe(krita.path);
  });

  it("answers an empty path when nothing is found or the registry fails", async () => {
    const none = source();
    expect(await appPathResolver(none.value)("unknown.exe")).toBe("");
    const failing = source({
      installed: new Error("scan failed"),
      registered: { "x.exe": new Error("reg.exe timed out") },
    });
    expect(await appPathResolver(failing.value)("x.exe")).toBe("");
    const unreadable = source({
      registered: { "x.exe": ["D:\\x\\x.exe"] },
    });
    unreadable.isFile.mockRejectedValue(new Error("EACCES"));
    expect(await appPathResolver(unreadable.value)("x.exe")).toBe("");
  });

  it("looks a name up once at a time, in any case", async () => {
    const s = source({ installed: [krita] });
    const resolve = appPathResolver(s.value);
    const answers = await Promise.all([
      resolve("krita.exe"),
      resolve("Krita.EXE"),
      resolve("krita.exe"),
    ]);
    expect(answers).toEqual([krita.path, krita.path, krita.path]);
    expect(s.installed).toHaveBeenCalledOnce();
    await resolve("krita.exe");
    expect(s.installed).toHaveBeenCalledOnce();
  });

  it("asks again after a while about a name it did not find", async () => {
    let now = 1000;
    const s = source({ now: () => now });
    const resolve = appPathResolver(s.value);
    expect(await resolve("late.exe")).toBe("");
    expect(await resolve("late.exe")).toBe("");
    expect(s.installed).toHaveBeenCalledOnce();
    now += APP_PATH_MISS_MS - 1;
    expect(await resolve("late.exe")).toBe("");
    expect(s.installed).toHaveBeenCalledOnce();
    // The program was installed in the meantime.
    s.installed.mockResolvedValue([
      { ...krita, exe: "late.exe", path: "C:\\L\\late.exe" },
    ]);
    now += 1;
    expect(await resolve("late.exe")).toBe("C:\\L\\late.exe");
    expect(s.installed).toHaveBeenCalledTimes(2);
    // A path found stays for the session.
    now += 10 * APP_PATH_MISS_MS;
    expect(await resolve("late.exe")).toBe("C:\\L\\late.exe");
    expect(s.installed).toHaveBeenCalledTimes(2);
  });

  it("keeps at most APP_PATH_CACHE names", async () => {
    const s = source();
    const resolve = appPathResolver(s.value);
    for (let i = 0; i < APP_PATH_CACHE; i++) await resolve(`app${i}.exe`);
    expect(s.installed).toHaveBeenCalledTimes(APP_PATH_CACHE);
    await resolve("app1.exe");
    expect(s.installed).toHaveBeenCalledTimes(APP_PATH_CACHE);
    // One more name makes room by dropping the oldest, app0.exe.
    await resolve("extra.exe");
    await resolve("app0.exe");
    expect(s.installed).toHaveBeenCalledTimes(APP_PATH_CACHE + 2);
    // The newest names are still there.
    await resolve(`app${APP_PATH_CACHE - 1}.exe`);
    await resolve("extra.exe");
    expect(s.installed).toHaveBeenCalledTimes(APP_PATH_CACHE + 2);
  });
});

describe("the handler", () => {
  it("takes one name, checks it and answers the resolver's path", async () => {
    const resolve = vi.fn(async () => "C:\\K\\krita.exe");
    const handlers = appPathHandlers(resolve);
    expect(handlers.appPath?.count).toBe(1);
    expect(await handlers.appPath?.run("krita.exe")).toBe("C:\\K\\krita.exe");
    expect(resolve).toHaveBeenCalledWith("krita.exe");
  });

  it.each([
    undefined,
    "",
    "C:\\K\\krita.exe",
    "\\\\server\\krita.exe",
    "krita.txt",
  ])("refuses %j before it looks anything up", (name) => {
    const resolve = vi.fn(async () => "");
    expect(() => appPathHandlers(resolve).appPath?.run(name)).toThrow(
      "Invalid exe name",
    );
    expect(resolve).not.toHaveBeenCalled();
  });
});

describe("App Paths in the registry", () => {
  it("names the current user's key first, then the machine's, both views", () => {
    expect(appPathKeys("krita.exe")).toEqual([
      "HKCU\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\krita.exe",
      "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\krita.exe",
      "HKLM\\SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\App Paths\\krita.exe",
    ]);
  });

  const key =
    "HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\krita.exe";
  const env = { ProgramFiles: "C:\\Program Files", programdata: "D:\\Data" };

  it.each([
    [
      "an English system",
      `\r\n${key}\r\n    (Default)    REG_SZ    C:\\Program Files\\Krita (x64)\\bin\\krita.exe\r\n\r\n`,
      "C:\\Program Files\\Krita (x64)\\bin\\krita.exe",
    ],
    [
      "a Chinese system",
      `\n${key}\n    (默认)    REG_SZ    D:\\画图\\paint.exe\n\n`,
      "D:\\画图\\paint.exe",
    ],
    [
      "a quoted path",
      `\n${key}\n    (Default)    REG_SZ    "C:\\Program Files\\Clip\\clip.exe" --x\n`,
      "C:\\Program Files\\Clip\\clip.exe",
    ],
    [
      "an expandable value",
      `\n${key}\n    (Default)    REG_EXPAND_SZ    %ProgramFiles%\\Clip\\clip.exe\n`,
      "C:\\Program Files\\Clip\\clip.exe",
    ],
    [
      "an environment variable in any case",
      `\n${key}\n    (Default)    REG_EXPAND_SZ    %PROGRAMDATA%\\x\\x.exe\n`,
      "D:\\Data\\x\\x.exe",
    ],
  ])("reads %s", (_name, output, want) => {
    expect(registeredPath(output, env)).toBe(want);
  });

  it.each([
    "",
    "ERROR: The system was unable to find the specified registry key or value.",
    `\n${key}\n    (Default)    REG_SZ    (value not set)\n`,
    `\n${key}\n    (Default)    REG_DWORD    0x1\n`,
    `\n${key}\n    Path    REG_SZ\n`,
  ])("finds no path in %j", (output) => {
    const path = registeredPath(output, env);
    // "(value not set)" is text, not a path: the resolver's checks drop it.
    expect(path === undefined || !/^[A-Za-z]:\\/.test(path)).toBe(true);
  });

  it("expands the variables it knows and leaves the others", () => {
    expect(expandEnv("%A%\\%b%\\%NONE%\\x.exe", { a: "C:", B: "dir" })).toBe(
      "C:\\dir\\%NONE%\\x.exe",
    );
    expect(expandEnv("no variables", {})).toBe("no variables");
  });

  it("decodes UTF-8, and the Chinese OEM code page when that is not UTF-8", () => {
    expect(decodeRegOutput(Buffer.from("C:\\Apps\\a.exe"))).toBe(
      "C:\\Apps\\a.exe",
    );
    expect(decodeRegOutput(Buffer.from("D:\\画图\\a.exe", "utf8"))).toBe(
      "D:\\画图\\a.exe",
    );
    const gbk = Buffer.from([
      ...Buffer.from("D:\\"),
      0xc8,
      0xed,
      0xbc,
      0xfe,
      ...Buffer.from("\\a.exe"),
    ]);
    expect(decodeRegOutput(gbk)).toBe("D:\\软件\\a.exe");
  });

  it("queries the three keys for the default value, one program at a time", async () => {
    const calls: string[][] = [];
    const releases: Array<() => void> = [];
    const run = vi.fn(async (args: string[]) => {
      calls.push(args);
      if (args[1]?.endsWith("slow.exe"))
        await new Promise<void>((resolve) => releases.push(resolve));
      if (args[1]?.startsWith("HKCU") && args[1].endsWith("krita.exe"))
        return Buffer.from(
          `\n${key}\n    (Default)    REG_SZ    C:\\K\\krita.exe\n`,
        );
      if (args[1]?.includes("WOW6432Node") && args[1].endsWith("krita.exe"))
        return Buffer.from(
          `\n${key}\n    (Default)    REG_SZ    C:\\K32\\krita.exe\n`,
        );
      if (args[1]?.endsWith("boom.exe")) throw new Error("exit code 1");
      return undefined;
    });
    const registered = registrySource(run, env);
    const slow = registered("slow.exe");
    const next = registered("krita.exe");
    await vi.waitFor(() => expect(releases).toHaveLength(3));
    // The second program waits until the first one's three queries are done.
    expect(calls).toHaveLength(3);
    for (const call of releases.splice(0)) call();
    expect(await slow).toEqual([]);
    expect(await next).toEqual(["C:\\K\\krita.exe", "C:\\K32\\krita.exe"]);
    expect(await registered("boom.exe")).toEqual([]);
    expect(calls.slice(3, 6)).toEqual(
      appPathKeys("krita.exe").map((k) => ["query", k, "/ve"]),
    );
    // Reading only: every call is a query.
    expect(calls.every((call) => call[0] === "query")).toBe(true);
  });
});

describe("the MuiCache in the registry", () => {
  const header =
    "HKEY_CURRENT_USER\\Software\\Classes\\Local Settings\\Software\\Microsoft\\Windows\\Shell\\MuiCache";
  const csp =
    "D:\\CSP\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe";
  const celsys =
    "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO PAINT\\CLIPStudioPaint.exe";
  const kritaExe = "C:\\Program Files (x86)\\Krita\\bin\\krita.exe";
  /** What "reg query" prints on an English system, CRLF line ends. */
  const english = [
    "",
    header,
    "    LangID    REG_BINARY    0409",
    `    ${csp}.FriendlyAppName    REG_SZ    CLIP STUDIO PAINT`,
    `    ${csp}.ApplicationCompany    REG_SZ    CELSYS,Inc.`,
    `    ${celsys}.FriendlyAppName    REG_SZ    CLIP STUDIO PAINT`,
    `    ${celsys}.ApplicationCompany    REG_SZ    CELSYS,Inc.`,
    `    ${kritaExe}.FriendlyAppName    REG_SZ    Krita`,
    "",
    "",
  ].join("\r\n");
  /** The same on a Chinese system: LF line ends, Chinese paths and data. */
  const chinese = [
    "",
    header,
    "    LangID    REG_BINARY    0804",
    "    D:\\画图\\Paint Pro.exe.FriendlyAppName    REG_SZ    画图专业版",
    "    D:\\画图\\Paint Pro.exe.ApplicationCompany    REG_SZ    某某公司",
    "    C:\\Program Files (x86)\\quark-cloud-drive\\app-3.18.0\\QuarkCloudDrive.exe.FriendlyAppName    REG_SZ    夸克网盘",
    "",
    "",
  ].join("\n");

  const index = (output: string) => Object.fromEntries(muiCacheIndex(output));

  it("names the current user's MuiCache key", () => {
    expect(muiCacheKey).toBe(
      "HKCU\\Software\\Classes\\Local Settings\\Software\\Microsoft\\Windows\\Shell\\MuiCache",
    );
  });

  it("lists the paths per file name from an English system", () => {
    expect(index(english)).toEqual({
      "clipstudiopaint.exe": [csp, celsys],
      "krita.exe": [kritaExe],
    });
  });

  it("lists the paths per file name from a Chinese system", () => {
    expect(index(chinese)).toEqual({
      "paint pro.exe": ["D:\\画图\\Paint Pro.exe"],
      "quarkclouddrive.exe": [
        "C:\\Program Files (x86)\\quark-cloud-drive\\app-3.18.0\\QuarkCloudDrive.exe",
      ],
    });
  });

  it("strips the suffix in any case, and takes a name that has none", () => {
    expect(
      index(
        [
          header,
          "    D:\\A\\a.exe.friendlyappname    REG_SZ    A",
          "    D:\\B\\b.exe.APPLICATIONCOMPANY    REG_SZ    B Inc.",
          // The only suffixes are the two of the MuiCache, and only once.
          "    D:\\C\\c.exe.FriendlyAppName.FriendlyAppName    REG_SZ    C",
          // A key that holds plain paths (the Compatibility Assistant store).
          "    D:\\D\\d.exe    REG_BINARY    5341435001000000",
        ].join("\n"),
      ),
    ).toEqual({
      "a.exe": ["D:\\A\\a.exe"],
      "b.exe": ["D:\\B\\b.exe"],
      "d.exe": ["D:\\D\\d.exe"],
    });
  });

  it("matches a file name in any case and lists a path once, whatever its case", () => {
    const text = [
      header,
      "    D:\\Apps\\Krita.EXE.FriendlyAppName    REG_SZ    Krita",
      "    d:\\apps\\krita.exe.ApplicationCompany    REG_SZ    KDE",
      "    E:\\Other\\KRITA.exe.FriendlyAppName    REG_SZ    Krita",
    ].join("\r\n");
    expect(index(text)).toEqual({
      "krita.exe": ["D:\\Apps\\Krita.EXE", "E:\\Other\\KRITA.exe"],
    });
  });

  it("leaves out values that are not an executable, the header and the errors", () => {
    expect(
      index(
        [
          header,
          "    LangID    REG_BINARY    0409",
          "    C:\\Docs\\readme.txt.FriendlyAppName    REG_SZ    Readme",
          "    C:\\Apps\\app.exe.bak.FriendlyAppName    REG_SZ    Backup",
          "    @shell32.dll,-21769    REG_SZ    Documents",
          `${header}\\SubKey`,
          "    C:\\Apps\\empty.exe.FriendlyAppName    REG_SZ    ",
          "    C:\\Apps\\bare.exe.FriendlyAppName    REG_SZ",
          "",
        ].join("\r\n"),
      ),
    ).toEqual({
      "empty.exe": ["C:\\Apps\\empty.exe"],
      "bare.exe": ["C:\\Apps\\bare.exe"],
    });
    expect(
      index(
        "ERROR: The system was unable to find the specified registry key or value.",
      ),
    ).toEqual({});
    expect(index("")).toEqual({});
  });

  it("does not take text inside the data for a value name", () => {
    expect(
      index(
        [
          header,
          "    D:\\A\\a.exe.FriendlyAppName    REG_SZ    tool    REG_SZ    E:\\Fake\\fake.exe",
        ].join("\n"),
      ),
    ).toEqual({ "a.exe": ["D:\\A\\a.exe"] });
  });

  it("queries the key once for many names, whatever their case", async () => {
    const run = vi.fn(async (_args: string[]) => Buffer.from(english));
    const launched = muiCacheSource(run, () => 0);
    const answers = await Promise.all([
      launched("CLIPStudioPaint.exe"),
      launched("clipstudiopaint.EXE"),
      launched("krita.exe"),
      launched("none.exe"),
    ]);
    expect(answers).toEqual([[csp, celsys], [csp, celsys], [kritaExe], []]);
    // Asked again once the reading is in, the answer is the same reading.
    expect(await launched("Krita.exe")).toEqual([kritaExe]);
    // Only a query of the key: no other command, no /ve, no other key.
    expect(run.mock.calls).toEqual([[["query", muiCacheKey]]]);
  });

  it("reads a Chinese system's GBK output", async () => {
    const gbk = Buffer.from([
      ...Buffer.from("\r\n    D:\\"),
      0xc8,
      0xed,
      0xbc,
      0xfe,
      ...Buffer.from("\\a.exe.FriendlyAppName    REG_SZ    x\r\n"),
    ]);
    const launched = muiCacheSource(
      async () => gbk,
      () => 0,
    );
    expect(await launched("a.exe")).toEqual(["D:\\软件\\a.exe"]);
    const utf8 = muiCacheSource(
      async () => Buffer.from(chinese, "utf8"),
      () => 0,
    );
    expect(await utf8("Paint Pro.exe")).toEqual(["D:\\画图\\Paint Pro.exe"]);
  });

  it("keeps the reading for the time a missing name is not looked for again", async () => {
    let now = 1000;
    let output = english;
    const run = vi.fn(async (_args: string[]) => Buffer.from(output));
    const launched = muiCacheSource(run, () => now);
    expect(await launched("late.exe")).toEqual([]);
    now += APP_PATH_MISS_MS - 1;
    expect(await launched("late.exe")).toEqual([]);
    expect(await launched("krita.exe")).toEqual([kritaExe]);
    expect(run).toHaveBeenCalledOnce();
    // The program was started in the meantime.
    output = `${english}    D:\\L\\late.exe.FriendlyAppName    REG_SZ    Late\r\n`;
    now += 1;
    expect(await launched("late.exe")).toEqual(["D:\\L\\late.exe"]);
    expect(run).toHaveBeenCalledTimes(2);
    expect(await launched("krita.exe")).toEqual([kritaExe]);
    expect(run).toHaveBeenCalledTimes(2);
  });

  it("answers nothing when reg.exe fails, and does not try again at once", async () => {
    let now = 0;
    const run = vi.fn(async (_args: string[]): Promise<Buffer | undefined> => {
      throw new Error("spawn failed");
    });
    const launched = muiCacheSource(run, () => now);
    expect(await launched("a.exe")).toEqual([]);
    expect(await launched("b.exe")).toEqual([]);
    expect(run).toHaveBeenCalledOnce();
    run.mockResolvedValue(undefined);
    now += APP_PATH_MISS_MS;
    expect(await launched("a.exe")).toEqual([]);
    expect(run).toHaveBeenCalledTimes(2);
    run.mockResolvedValue(Buffer.from(english));
    now += APP_PATH_MISS_MS;
    expect(await launched("krita.exe")).toEqual([kritaExe]);
    expect(run).toHaveBeenCalledTimes(3);
  });

  it("gives at most MUICACHE_CANDIDATES paths of a name, the first ones", async () => {
    const many = Array.from(
      { length: MUICACHE_CANDIDATES + 20 },
      (_, i) => `D:\\Old${i}\\tool.exe`,
    );
    const output = [
      header,
      ...many.map((path) => `    ${path}.FriendlyAppName    REG_SZ    Tool`),
    ].join("\r\n");
    const launched = muiCacheSource(
      async () => Buffer.from(output),
      () => 0,
    );
    expect(await launched("tool.exe")).toEqual(
      many.slice(0, MUICACHE_CANDIDATES),
    );
  });

  describe("beside the other sources", () => {
    const env = {};
    const appPathsOf = (args: string[]) => args[1]?.includes("App Paths");

    it("reads nothing while a test's own apps list stands for the machine", async () => {
      let fixture = true;
      const run = vi.fn(async (_args: string[]) => Buffer.from(english));
      const sources = registrySources(run, { fixture: () => fixture, env });
      expect(await sources.registered("krita.exe")).toEqual([]);
      expect(await sources.launched("krita.exe")).toEqual([]);
      expect(await sources.launched("CLIPStudioPaint.exe")).toEqual([]);
      expect(run).not.toHaveBeenCalled();
      fixture = false;
      expect(await sources.launched("krita.exe")).toEqual([kritaExe]);
      expect(run).toHaveBeenCalledOnce();
    });

    it("finds a program that only the MuiCache knows, with one query for many names", async () => {
      const run = vi.fn(async (args: string[]) =>
        appPathsOf(args) ? undefined : Buffer.from(english),
      );
      const launcher = {
        name: "CLIP STUDIO",
        exe: "CLIPStudio.exe",
        path: "D:\\CELSYS\\CLIP STUDIO 1.5\\CLIP STUDIO\\CLIPStudio.exe",
      };
      const files = new Set([celsys.toLowerCase(), kritaExe.toLowerCase()]);
      const resolve = appPathResolver({
        installed: async () => [launcher],
        ...registrySources(run, { fixture: () => false, env }),
        isFile: async (path) => files.has(path.toLowerCase()),
      });
      // csp is stale, celsys is the live install.
      expect(
        await Promise.all([
          resolve("CLIPStudioPaint.exe"),
          resolve("clipstudiopaint.exe"),
          resolve("krita.exe"),
          resolve("nothing.exe"),
          resolve("CLIPStudio.exe"),
        ]),
      ).toEqual([celsys, celsys, kritaExe, "", launcher.path]);
      const calls = run.mock.calls.map(([args]) => args);
      expect(calls.filter((args) => !appPathsOf(args))).toEqual([
        ["query", muiCacheKey],
      ]);
      // Three App Paths queries per name that the Start Menu does not have.
      expect(calls.filter(appPathsOf)).toHaveLength(3 * 3);
      expect(calls.every((args) => args[0] === "query")).toBe(true);
    });
  });
});

describe("reg.exe", () => {
  it("leaves room for the MuiCache of a machine with thousands of programs", async () => {
    // A real MuiCache is 65 KB for about 280 programs.
    const big = Array.from(
      { length: 2000 },
      (_, i) =>
        `    C:\\Program Files (x86)\\Vendor ${i}\\Product ${i}\\app-${i}.exe.FriendlyAppName    REG_SZ    Product ${i}\r\n` +
        `    C:\\Program Files (x86)\\Vendor ${i}\\Product ${i}\\app-${i}.exe.ApplicationCompany    REG_SZ    Vendor ${i}, Inc.\r\n`,
    ).join("");
    const exec = vi.fn(
      (
        _file: string,
        _args: readonly string[],
        _options: object,
        callback: (error: Error | null, stdout: Buffer) => void,
      ) => callback(null, Buffer.from(big)),
    );
    await regExe(exec as never)(["query", muiCacheKey]);
    const { maxBuffer } = (exec.mock.calls[0]?.[2] ?? {}) as {
      maxBuffer: number;
    };
    expect(maxBuffer).toBeGreaterThan(Buffer.byteLength(big));
    // Still bounded.
    expect(maxBuffer).toBeLessThanOrEqual(4 * 1024 * 1024);
  });

  it("runs the system's own reg.exe with a timeout and no window", async () => {
    const exec = vi.fn(
      (
        _file: string,
        _args: readonly string[],
        _options: object,
        callback: (error: Error | null, stdout: Buffer) => void,
      ) => callback(null, Buffer.from("out")),
    );
    const run = regExe(exec as never, "D:\\WINNT");
    expect(await run(["query", "HKCU\\X", "/ve"])).toEqual(Buffer.from("out"));
    const [file, args, options] = exec.mock.calls[0] ?? [];
    expect(file).toBe("D:\\WINNT\\System32\\reg.exe");
    expect(args).toEqual(["query", "HKCU\\X", "/ve"]);
    expect(options).toMatchObject({
      encoding: "buffer",
      windowsHide: true,
      timeout: expect.any(Number),
    });
    expect((options as { timeout: number }).timeout).toBeLessThanOrEqual(3000);
  });

  it("answers nothing when reg.exe fails or is not given a query", async () => {
    const failing = vi.fn(
      (
        _file: string,
        _args: readonly string[],
        _options: object,
        callback: (error: Error | null, stdout: Buffer) => void,
      ) => callback(new Error("exit 1"), Buffer.alloc(0)),
    );
    expect(
      await regExe(failing as never)(["query", "HKCU\\X"]),
    ).toBeUndefined();
    const exec = vi.fn();
    for (const args of [["add", "HKCU\\X"], ["delete", "HKCU\\X"], []])
      expect(await regExe(exec as never)(args)).toBeUndefined();
    expect(exec).not.toHaveBeenCalled();
  });
});
