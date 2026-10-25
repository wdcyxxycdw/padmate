import { describe, expect, it, vi } from "vitest";
import {
  installedAppHandlers,
  isSetupTool,
  listFixtureApps,
  listInstalledApps,
  MAX_APPS,
  MAX_DEPTH,
  MAX_SHORTCUTS,
  PARTIAL_SCAN_CACHE_MS,
  programScan,
  SCAN_CACHE_MS,
  SCAN_TIMEOUT_MS,
  type ShortcutSource,
  windowsDirectory,
} from "./installed-apps";

interface Tree {
  [name: string]: Tree | string;
}

/**
 * A Start Menu in memory: a folder is an object, a shortcut is a string,
 * its target. files are the paths that exist as regular files.
 */
function menu(
  roots: Record<string, Tree>,
  files: string[],
  extra: Partial<ShortcutSource> = {},
): ShortcutSource & { visited: string[]; read: string[] } {
  const existing = new Set(files.map((file) => file.toLowerCase()));
  const visited: string[] = [];
  const read: string[] = [];
  const find = (path: string): Tree | string | undefined => {
    for (const [root, tree] of Object.entries(roots)) {
      if (path === root) return tree;
      if (!path.startsWith(`${root}\\`)) continue;
      let node: Tree | string | undefined = tree;
      for (const part of path.slice(root.length + 1).split("\\")) {
        if (typeof node !== "object") return undefined;
        node = node[part];
      }
      return node;
    }
    return undefined;
  };
  return {
    roots: Object.keys(roots),
    visited,
    read,
    async list(dir) {
      visited.push(dir);
      const node = find(dir);
      if (typeof node !== "object") throw new Error("ENOENT");
      return Object.entries(node).map(([name, value]) => ({
        name,
        directory: typeof value === "object",
        file: typeof value === "string",
      }));
    },
    target(link) {
      read.push(link);
      const node = find(link);
      if (typeof node !== "string") throw new Error("not a shortcut");
      return node;
    },
    async isFile(path) {
      return existing.has(path.toLowerCase());
    },
    ...extra,
  };
}

const programs = "C:\\ProgramData\\Start Menu\\Programs";
const user = "C:\\Users\\a\\Start Menu\\Programs";

describe("programs of the Start Menu (UI-46)", () => {
  it("lists shortcuts that lead to an existing local .exe, named by the shortcut", async () => {
    const source = menu(
      {
        [programs]: {
          "Krita.lnk": "C:\\Krita\\bin\\krita.exe",
          "Paint Studio": {
            "Paint Studio.lnk": "C:\\Paint\\paint.exe",
            "Readme.txt": "ignored",
          },
          "Gone.lnk": "C:\\Gone\\gone.exe",
          "Folder.lnk": "C:\\Tools",
          "Doc.lnk": "C:\\Docs\\readme.pdf",
          "Network.lnk": "\\\\server\\share\\net.exe",
          "Relative.lnk": "..\\rel.exe",
          "Web.url": "ignored",
        },
        [user]: { "画画.lnk": "D:\\Apps\\画画.exe" },
      },
      [
        "C:\\Krita\\bin\\krita.exe",
        "C:\\Paint\\paint.exe",
        "C:\\Docs\\readme.pdf",
        "\\\\server\\share\\net.exe",
        "D:\\Apps\\画画.exe",
      ],
    );
    const apps = await listInstalledApps(source);
    // How Chinese and Latin names order is the collator's business.
    expect(apps).toHaveLength(3);
    expect(apps).toEqual(
      expect.arrayContaining([
        { name: "Krita", exe: "krita.exe", path: "C:\\Krita\\bin\\krita.exe" },
        {
          name: "Paint Studio",
          exe: "paint.exe",
          path: "C:\\Paint\\paint.exe",
        },
        { name: "画画", exe: "画画.exe", path: "D:\\Apps\\画画.exe" },
      ]),
    );
  });

  it("drops uninstallers and updaters by the shortcut's or the program's name", async () => {
    const source = menu(
      {
        [programs]: {
          "Krita.lnk": "C:\\Krita\\krita.exe",
          "Uninstall Krita.lnk": "C:\\Krita\\other.exe",
          "Remove.lnk": "C:\\Krita\\unins000.exe",
          "Setup.lnk": "C:\\Krita\\uninstall.exe",
          "卸载 Krita.lnk": "C:\\Krita\\x.exe",
          "Krita Updater.lnk": "C:\\Krita\\y.exe",
          "Tools.lnk": "C:\\Krita\\AutoUpdate.exe",
          "Unison.lnk": "C:\\Unison\\unison.exe",
        },
      },
      [
        "C:\\Krita\\krita.exe",
        "C:\\Krita\\other.exe",
        "C:\\Krita\\unins000.exe",
        "C:\\Krita\\uninstall.exe",
        "C:\\Krita\\x.exe",
        "C:\\Krita\\y.exe",
        "C:\\Krita\\AutoUpdate.exe",
        "C:\\Unison\\unison.exe",
      ],
    );
    expect((await listInstalledApps(source)).map((app) => app.name)).toEqual([
      "Krita",
      "Unison",
    ]);
  });

  it("lists a program once, under the first shortcut that names it", async () => {
    const source = menu(
      {
        [programs]: {
          "Krita.lnk": "C:\\Krita\\krita.exe",
          "Zed Krita.lnk": "c:\\krita\\KRITA.exe",
        },
        [user]: { "Krita again.lnk": "C:\\Krita\\krita.exe" },
      },
      ["C:\\Krita\\krita.exe"],
    );
    expect(await listInstalledApps(source)).toEqual([
      { name: "Krita", exe: "krita.exe", path: "C:\\Krita\\krita.exe" },
    ]);
  });

  it("skips shortcuts it cannot read and folders it cannot list", async () => {
    const source = menu(
      {
        [programs]: {
          "Broken.lnk": "C:\\Broken\\broken.exe",
          "Good.lnk": "C:\\Good\\good.exe",
        },
      },
      ["C:\\Good\\good.exe", "C:\\Broken\\broken.exe"],
      {
        target(link) {
          if (link.endsWith("Broken.lnk")) throw new Error("bad link");
          return "C:\\Good\\good.exe";
        },
      },
    );
    source.roots.push("C:\\Missing\\Programs");
    expect((await listInstalledApps(source)).map((app) => app.name)).toEqual([
      "Good",
    ]);
  });

  it("sorts by name and goes at most MAX_DEPTH folders down", async () => {
    let tree: Tree = { "Deep.lnk": "C:\\Deep\\deep.exe" };
    for (let depth = 0; depth <= MAX_DEPTH; depth++)
      tree = { [`d${depth}`]: tree };
    const source = menu(
      {
        [programs]: {
          ...tree,
          "b.lnk": "C:\\b.exe",
          "A.lnk": "C:\\a.exe",
          "c.lnk": "C:\\c.exe",
          // A level the limit allows.
          ok: { ok: { ok: { ok: { "Last.lnk": "C:\\last.exe" } } } },
        },
      },
      [
        "C:\\Deep\\deep.exe",
        "C:\\a.exe",
        "C:\\b.exe",
        "C:\\c.exe",
        "C:\\last.exe",
      ],
    );
    expect((await listInstalledApps(source)).map((app) => app.name)).toEqual([
      "A",
      "b",
      "c",
      "Last",
    ]);
  });

  it("stops after MAX_SHORTCUTS shortcuts and MAX_APPS programs", async () => {
    const many: Tree = {};
    const files: string[] = [];
    for (let index = 0; index < MAX_SHORTCUTS + 50; index++) {
      many[`App ${String(index).padStart(5, "0")}.lnk`] =
        `C:\\Apps\\app${index}.exe`;
      files.push(`C:\\Apps\\app${index}.exe`);
    }
    const source = menu({ [programs]: many }, files);
    const apps = await listInstalledApps(source);
    expect(apps.length).toBe(MAX_APPS);
    expect(source.read.length).toBe(MAX_APPS);
    // Shortcuts that lead nowhere count too.
    const dead = menu({ [programs]: many }, []);
    expect(await listInstalledApps(dead)).toEqual([]);
    expect(dead.read.length).toBe(MAX_SHORTCUTS);
  });

  it("returns what it has when a check hangs, on a network drive that does not answer", async () => {
    vi.useFakeTimers();
    try {
      const source = menu(
        {
          [programs]: {
            "A.lnk": "C:\\Apps\\a.exe",
            "B.lnk": "Z:\\Share\\b.exe",
            "C.lnk": "C:\\Apps\\c.exe",
          },
        },
        ["C:\\Apps\\a.exe", "C:\\Apps\\c.exe"],
        {
          // A disconnected mapped drive: the check never comes back.
          isFile: (path) =>
            path.startsWith("Z:")
              ? new Promise<boolean>(() => {})
              : Promise.resolve(true),
        },
      );
      const scanning = listInstalledApps(source);
      let done = false;
      void scanning.then(() => {
        done = true;
      });
      await vi.advanceTimersByTimeAsync(SCAN_TIMEOUT_MS - 1);
      expect(done).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect((await scanning).map((app) => app.name)).toEqual(["A"]);
      // The scan stops there: the shortcut after the hanging one is not read.
      expect(
        source.read.map((link) => link.slice(link.lastIndexOf("\\") + 1)),
      ).toEqual(["A.lnk", "B.lnk"]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops reading shortcuts once the time is up", async () => {
    vi.useFakeTimers();
    try {
      const many: Tree = {};
      const files: string[] = [];
      for (let index = 0; index < 20; index++) {
        many[`App ${String(index).padStart(2, "0")}.lnk`] =
          `C:\\Apps\\app${index}.exe`;
        files.push(`C:\\Apps\\app${index}.exe`);
      }
      // Each check takes a second: the time is up after three.
      const source = menu({ [programs]: many }, files, {
        isFile: () =>
          new Promise<boolean>((resolve) => setTimeout(resolve, 1000, true)),
      });
      const scanning = listInstalledApps(source);
      await vi.advanceTimersByTimeAsync(SCAN_TIMEOUT_MS);
      const apps = await scanning;
      expect(apps.length).toBeGreaterThanOrEqual(2);
      expect(apps.length).toBeLessThan(5);
      await vi.advanceTimersByTimeAsync(60_000);
      expect(source.read.length).toBeLessThan(6);
    } finally {
      vi.useRealTimers();
    }
  });

  it("reads no folder it was not given, and follows no link", async () => {
    const source = menu({ [programs]: { "A.lnk": "C:\\a.exe" } }, [
      "C:\\a.exe",
    ]);
    await listInstalledApps(source);
    expect(source.visited).toEqual([programs]);
  });
});

describe("what the list leaves out (UI-46)", () => {
  it.each([
    // Installers and setup programs.
    "Visual Studio Installer",
    "Install Additional Tools for Node.js",
    "Setup",
    "setup.exe",
    "vs_installer.exe",
    "InstallShield Wizard",
    "Installation Assistant",
    "安装向导",
    // Uninstallers.
    "Uninstall Krita",
    "unins000.exe",
    "uninst.exe",
    "Uninstaller_x64.exe",
    "uninstall.exe",
    "卸载 Krita",
    // Updaters.
    "Krita Updater",
    "Krita Update Checker",
    "AutoUpdate.exe",
    "autoupdate.exe",
    "googleupdate.exe",
    "appupgrade64.exe",
    "EAUpdater.exe",
    "Wacom 更新实用程序",
    "软件升级",
    "Upgrade Assistant",
    // Repair and recovery tools.
    "Repair Tool",
    "修复工具",
    "App Recovery",
    "Recovery Manager",
    "数据恢复",
    // Crash and error reporters.
    "EA Error Reporter",
    "ErrorReporter.exe",
    "Crash Reporter",
    "CrashReporter",
    "Bug Reporter",
    "错误报告",
    "崩溃报告程序",
    // Patchers, helpers and telemetry, seen on a real PC after round 3.
    "LauncherPatcher.exe",
    "Game Patcher",
    "PlatformExperiencesHelper",
    "platform_experiences_helper.exe",
    "Crash Helper",
    "Office テレメトリ ログ",
    "Telemetry Dashboard for Office",
    "遥测日志",
  ])("takes %j for a setup tool", (text) => {
    expect(isSetupTool(text)).toBe(true);
  });

  it.each([
    "CLIP STUDIO",
    "CLIP STUDIO PAINT",
    "CLIPStudio.exe",
    "CLIPStudioPaint.exe",
    "Krita",
    "Krita (x64)",
    "krita.exe",
    "Photoshop",
    "Adobe Photoshop 2025",
    "Photoshop.exe",
    "爱思助手",
    "i4Tools.exe",
    "百度网盘",
    "BaiduNetdisk.exe",
    "Paint.NET",
    "paintdotnet.exe",
    "Blender 4.2",
    "Unison",
    "unison.exe",
    "Updraft",
    "updraft.exe",
    "Instagram",
    "Visual Studio Code",
    "Code.exe",
    "Notepad++",
    "SAI2",
    "WPS Office",
    "Wacom Center",
    "Wacom_Center.exe",
    "Steam",
    "Everything",
    "Reporting Services",
    "Epic Games Launcher",
    "画图",
    "Rockstar Games Launcher",
    "HelpNDoc",
    "Patchwork",
    "Telegram",
  ])("keeps %j", (text) => {
    expect(isSetupTool(text)).toBe(false);
  });

  it("drops the shortcuts seen on a real PC and keeps the programs", async () => {
    const source = menu(
      {
        [programs]: {
          "Visual Studio Installer.lnk":
            "C:\\Program Files (x86)\\Microsoft Visual Studio\\Installer\\setup.exe",
          "Wacom 更新实用程序.lnk":
            "C:\\Program Files\\Tablet\\Pen\\Wacom_NotifyUtil.exe",
          "Install Additional Tools for Node.js.lnk":
            "C:\\Windows\\System32\\cmd.exe",
          "Administrative Tools.lnk": "C:\\Windows\\System32\\control.exe",
          "Windows Software Development Kit.lnk": "C:\\Windows\\explorer.exe",
          "App Recovery.lnk": "C:\\Program Files\\Vendor\\recover.exe",
          "EA Error Reporter.lnk": "C:\\Program Files\\EA\\ErrorReporter.exe",
          "CLIP STUDIO.lnk": "C:\\Program Files\\CELSYS\\CLIPStudio.exe",
          "Krita.lnk": "C:\\Program Files\\Krita (x64)\\bin\\krita.exe",
          "Photoshop.lnk": "C:\\Program Files\\Adobe\\Photoshop.exe",
          "爱思助手.lnk": "D:\\爱思助手\\i4Tools.exe",
          "百度网盘.lnk": "C:\\Baidu\\BaiduNetdisk.exe",
        },
      },
      [
        "C:\\Program Files (x86)\\Microsoft Visual Studio\\Installer\\setup.exe",
        "C:\\Program Files\\Tablet\\Pen\\Wacom_NotifyUtil.exe",
        "C:\\Windows\\System32\\cmd.exe",
        "C:\\Windows\\System32\\control.exe",
        "C:\\Windows\\explorer.exe",
        "C:\\Program Files\\Vendor\\recover.exe",
        "C:\\Program Files\\EA\\ErrorReporter.exe",
        "C:\\Program Files\\CELSYS\\CLIPStudio.exe",
        "C:\\Program Files\\Krita (x64)\\bin\\krita.exe",
        "C:\\Program Files\\Adobe\\Photoshop.exe",
        "D:\\爱思助手\\i4Tools.exe",
        "C:\\Baidu\\BaiduNetdisk.exe",
      ],
      { windowsDir: "C:\\WINDOWS" },
    );
    expect((await listInstalledApps(source)).map((app) => app.name)).toEqual(
      expect.arrayContaining([
        "CLIP STUDIO",
        "Krita",
        "Photoshop",
        "爱思助手",
        "百度网盘",
      ]),
    );
    expect(await listInstalledApps(source)).toHaveLength(5);
  });

  it.each([
    ["C:\\Windows\\System32\\cmd.exe", "C:\\Windows"],
    ["c:\\windows\\explorer.exe", "C:\\WINDOWS"],
    ["C:\\Windows\\SystemApps\\X\\SearchHost.exe", "C:\\Windows\\"],
    ["C:\\Windows\\System32\\cmd.exe", "c:/windows"],
  ])("drops a program below the Windows directory: %s", async (target, dir) => {
    const source = menu({ [programs]: { "Tool.lnk": target } }, [target], {
      windowsDir: dir,
    });
    expect(await listInstalledApps(source)).toEqual([]);
  });

  it.each([
    "C:\\Users\\u\\AppData\\Roaming\\Microsoft\\Installer\\{658E545E-218B-4D97-933F-07DE700FEBF3}\\_4B7BB168D5805E324C734A.exe",
    "C:\\Users\\u\\AppData\\Roaming\\microsoft\\INSTALLER\\{1}\\x.exe",
  ])("drops a program in an installer's cache: %s", async (target) => {
    const source = menu({ [programs]: { "AltServer.lnk": target } }, [target], {
      windowsDir: "C:\\Windows",
    });
    expect(await listInstalledApps(source)).toEqual([]);
  });

  it("keeps programs in other folders named Installer", async () => {
    const targets = {
      "A.lnk": "D:\\Tools\\Installer\\a.exe",
      "B.lnk": "C:\\Program Files\\Microsoft Office\\root\\Office16\\b.exe",
    };
    const source = menu({ [programs]: targets }, Object.values(targets), {
      windowsDir: "C:\\Windows",
    });
    expect((await listInstalledApps(source)).map((app) => app.name)).toEqual([
      "A",
      "B",
    ]);
  });

  it("keeps programs next to the Windows directory, and every one without it", async () => {
    const targets = {
      "A.lnk": "C:\\WindowsApps\\a.exe",
      "B.lnk": "C:\\Windows.old\\b.exe",
      "C.lnk": "D:\\Windows\\c.exe",
      "D.lnk": "C:\\Tools\\Windows\\d.exe",
    };
    const files = Object.values(targets);
    const next = menu({ [programs]: targets }, files, {
      windowsDir: "C:\\Windows",
    });
    expect((await listInstalledApps(next)).map((app) => app.name)).toEqual([
      "A",
      "B",
      "C",
      "D",
    ]);
    // Without a known Windows directory the rule has nothing to compare.
    const unknown = menu(
      { [programs]: { "Cmd.lnk": "C:\\Windows\\System32\\cmd.exe" } },
      ["C:\\Windows\\System32\\cmd.exe"],
    );
    expect((await listInstalledApps(unknown)).map((app) => app.name)).toEqual([
      "Cmd",
    ]);
  });

  it("finds the Windows directory in the environment", () => {
    expect(windowsDirectory({ SystemRoot: "C:\\Windows" })).toBe("C:\\Windows");
    expect(windowsDirectory({ WINDIR: "D:\\WINNT" })).toBe("D:\\WINNT");
    expect(windowsDirectory({ windir: "E:\\Win" })).toBe("E:\\Win");
    expect(windowsDirectory({ SystemRoot: "C:\\A", WINDIR: "C:\\B" })).toBe(
      "C:\\A",
    );
    expect(windowsDirectory({})).toBe("");
  });
});

describe("a fixed list for tests", () => {
  it("applies the same checks to a JSON list of name and path", async () => {
    const read = vi.fn(async () =>
      JSON.stringify([
        { name: "Krita", path: "C:\\Krita\\krita.exe" },
        { name: "Gone", path: "C:\\Gone\\gone.exe" },
        { name: "Uninstall", path: "C:\\Krita\\unins000.exe" },
        { name: "Net", path: "\\\\server\\net.exe" },
        { name: 5, path: "C:\\Krita\\krita.exe" },
        { name: "Dup", path: "c:\\krita\\KRITA.EXE" },
        "text",
      ]),
    );
    const apps = await listFixtureApps("fixture.json", {
      read,
      isFile: async (path) => /krita/i.test(path),
    });
    expect(apps).toEqual([
      { name: "Krita", exe: "krita.exe", path: "C:\\Krita\\krita.exe" },
    ]);
    expect(read).toHaveBeenCalledWith("fixture.json");
  });

  it("leaves out system tools and setup tools like the Start Menu does", async () => {
    const read = vi.fn(async () =>
      JSON.stringify([
        { name: "Krita", path: "C:\\Krita\\krita.exe" },
        { name: "Command Prompt", path: "C:\\Windows\\System32\\cmd.exe" },
        { name: "Krita Setup", path: "C:\\Krita\\krita-setup.exe" },
        { name: "Tool", path: "C:\\Krita\\AutoUpdate.exe" },
      ]),
    );
    const apps = await listFixtureApps("fixture.json", {
      read,
      isFile: async () => true,
      windowsDir: "C:\\Windows",
    });
    expect(apps).toEqual([
      { name: "Krita", exe: "krita.exe", path: "C:\\Krita\\krita.exe" },
    ]);
  });

  it.each(["not json", '{"name":"x"}', ""])(
    "lists nothing for %j",
    async (text) => {
      expect(
        await listFixtureApps("f.json", {
          read: async () => text,
          isFile: async () => true,
        }),
      ).toEqual([]);
    },
  );
});

describe("a reading of the Start Menu that is kept for a while (UI-42)", () => {
  const apps = [{ name: "Krita", exe: "krita.exe", path: "C:\\k\\krita.exe" }];

  it("serves the last reading until it is old, and reads again then", async () => {
    let now = 5000;
    const list = vi.fn(async () => apps);
    const scan = programScan(list, () => now);
    expect(await scan.cached()).toEqual(apps);
    expect(await scan.cached()).toEqual(apps);
    expect(list).toHaveBeenCalledOnce();
    now += SCAN_CACHE_MS - 1;
    await scan.cached();
    expect(list).toHaveBeenCalledOnce();
    now += 1;
    await scan.cached();
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("reads afresh for the picker, and that reading is the one that is kept", async () => {
    const list = vi
      .fn<() => Promise<typeof apps>>()
      .mockResolvedValueOnce([])
      .mockResolvedValue(apps);
    const scan = programScan(list);
    expect(await scan.cached()).toEqual([]);
    expect(await scan.fresh()).toEqual(apps);
    expect(await scan.cached()).toEqual(apps);
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("shares one reading between calls that overlap", async () => {
    const list = vi.fn(async () => apps);
    const scan = programScan(list);
    const all = await Promise.all([scan.fresh(), scan.cached(), scan.fresh()]);
    expect(all).toEqual([apps, apps, apps]);
    expect(list).toHaveBeenCalledOnce();
  });

  it("keeps a reading that ran out of time only briefly, so a program it missed is looked for again soon", async () => {
    vi.useFakeTimers();
    try {
      let hang = true;
      const source = menu(
        {
          [programs]: {
            "A.lnk": "C:\\Apps\\a.exe",
            "B.lnk": "Z:\\Share\\b.exe",
            "C.lnk": "C:\\Apps\\c.exe",
          },
        },
        ["C:\\Apps\\a.exe", "Z:\\Share\\b.exe", "C:\\Apps\\c.exe"],
        {
          // A mapped drive that does not answer, at first.
          isFile: (path) =>
            hang && path.startsWith("Z:")
              ? new Promise<boolean>(() => {})
              : Promise.resolve(true),
        },
      );
      let now = 0;
      const list = vi.fn(() => listInstalledApps(source));
      const scan = programScan(list, () => now);
      const first = scan.cached();
      await vi.advanceTimersByTimeAsync(SCAN_TIMEOUT_MS);
      expect((await first).map((app) => app.name)).toEqual(["A"]);
      hang = false;
      now += PARTIAL_SCAN_CACHE_MS - 1;
      expect((await scan.cached()).map((app) => app.name)).toEqual(["A"]);
      expect(list).toHaveBeenCalledOnce();
      now += 1;
      expect((await scan.cached()).map((app) => app.name)).toEqual([
        "A",
        "B",
        "C",
      ]);
      expect(list).toHaveBeenCalledTimes(2);
      // A complete reading is kept for the long time.
      now += SCAN_CACHE_MS - 1;
      await scan.cached();
      expect(list).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not keep a reading that failed", async () => {
    const list = vi
      .fn<() => Promise<typeof apps>>()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValue(apps);
    const scan = programScan(list);
    await expect(scan.cached()).rejects.toThrow("boom");
    expect(await scan.cached()).toEqual(apps);
    expect(list).toHaveBeenCalledTimes(2);
  });
});

describe("the handlers", () => {
  const apps = [{ name: "Krita", exe: "krita.exe", path: "C:\\k\\krita.exe" }];
  function handlers(chosen?: string) {
    const list = vi.fn(async () => apps);
    const chooseExe = vi.fn(async () => chosen);
    return {
      list,
      chooseExe,
      handlers: installedAppHandlers({ list, chooseExe }),
    };
  }

  it("take no argument", () => {
    const h = handlers();
    expect(h.handlers.appsInstalled?.count).toBe(0);
    expect(h.handlers.appBrowse?.count).toBe(0);
  });

  it("list once for calls that overlap, and again afterwards", async () => {
    const h = handlers();
    const first = h.handlers.appsInstalled?.run() as Promise<unknown>;
    const second = h.handlers.appsInstalled?.run() as Promise<unknown>;
    expect(await first).toEqual(apps);
    expect(await second).toEqual(apps);
    expect(h.list).toHaveBeenCalledOnce();
    await h.handlers.appsInstalled?.run();
    expect(h.list).toHaveBeenCalledTimes(2);
  });

  it("let a failed listing be asked for again", async () => {
    const list = vi
      .fn<() => Promise<typeof apps>>()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValue(apps);
    const h = installedAppHandlers({ list, chooseExe: async () => undefined });
    await expect(h.appsInstalled?.run()).rejects.toThrow("boom");
    expect(await h.appsInstalled?.run()).toEqual(apps);
  });

  it("answer the chosen .exe with its name and path, and null on cancel", async () => {
    expect(
      await handlers(
        "D:/Tools/Paint.NET/paintdotnet.exe",
      ).handlers.appBrowse?.run(),
    ).toEqual({
      exe: "paintdotnet.exe",
      path: "D:\\Tools\\Paint.NET\\paintdotnet.exe",
    });
    expect(await handlers(undefined).handlers.appBrowse?.run()).toBeNull();
  });

  it.each([
    "\\\\server\\share\\a.exe",
    "C:\\Tools\\readme.txt",
    "relative\\a.exe",
    "C:\\Tools\\..\\a.exe",
  ])("refuse %j with a reason the renderer can translate", async (path) => {
    await expect(
      handlers(path).handlers.appBrowse?.run(),
    ).rejects.toMatchObject({
      data: { reason: "path" },
    });
  });
});
