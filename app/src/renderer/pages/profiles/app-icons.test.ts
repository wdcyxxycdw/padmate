import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  APP_PATHS_KEY,
  APP_PATHS_LIMIT,
  forgetAppIcons,
  iconPath,
  loadAppIcon,
  loadAppPath,
  peekAppIcon,
  rememberAppPath,
  resolveAppIcon,
} from "./app-icons";

const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => store.set(key, value),
  });
  forgetAppIcons();
});
afterEach(() => vi.unstubAllGlobals());

const recent = [
  { exe: "Krita.exe", path: "C:\\Krita\\bin\\krita.exe", elevated: false },
  { exe: "share.exe", path: "\\\\server\\apps\\share.exe", elevated: false },
];

describe("bound app icons (UI-42)", () => {
  it("finds the executable of a rule", () => {
    expect(iconPath("D:\\Apps\\Clip\\clip.exe", [])).toBe(
      "D:\\Apps\\Clip\\clip.exe",
    );
    expect(iconPath("krita.exe", recent)).toBe("C:\\Krita\\bin\\krita.exe");
    expect(iconPath("KRITA.EXE", recent)).toBe("C:\\Krita\\bin\\krita.exe");
    // A network path never reaches Main.
    expect(iconPath("share.exe", recent)).toBe("");
    expect(iconPath("\\\\server\\apps\\share.exe", [])).toBe("");
    expect(iconPath("sai2.exe", recent)).toBe("");
  });

  it("remembers where a bound app was, for a later session", () => {
    rememberAppPath("sai2.exe", "C:\\SAI\\sai2.exe");
    expect(iconPath("SAI2.exe", [])).toBe("C:\\SAI\\sai2.exe");
    // An app seen now wins over a remembered path.
    rememberAppPath("krita.exe", "D:\\Old\\krita.exe");
    expect(iconPath("krita.exe", recent)).toBe("C:\\Krita\\bin\\krita.exe");
    // Only file names are remembered, and only local paths.
    rememberAppPath("C:\\SAI\\sai2.exe", "C:\\SAI\\sai2.exe");
    rememberAppPath("share.exe", "\\\\server\\apps\\share.exe");
    expect(Object.keys(JSON.parse(store.get(APP_PATHS_KEY) ?? "{}"))).toEqual([
      "sai2.exe",
      "krita.exe",
    ]);
    for (let i = 0; i < APP_PATHS_LIMIT; i++)
      rememberAppPath(`app${i}.exe`, `C:\\Apps\\app${i}.exe`);
    const kept = Object.keys(JSON.parse(store.get(APP_PATHS_KEY) ?? "{}"));
    expect(kept).toHaveLength(APP_PATHS_LIMIT);
    expect(kept).not.toContain("sai2.exe");
    store.set(APP_PATHS_KEY, "not json");
    expect(iconPath("sai2.exe", [])).toBe("");
  });

  it("asks Main once per path and keeps only image data", async () => {
    const api = {
      getAppIcon: vi.fn(async (path: string) =>
        path.includes("bad")
          ? "javascript:alert(1)"
          : "data:image/png;base64,x",
      ),
    };
    expect(peekAppIcon("C:\\A\\a.exe")).toBe("");
    expect(await loadAppIcon(api, "C:\\A\\a.exe")).toBe(
      "data:image/png;base64,x",
    );
    expect(await loadAppIcon(api, "c:\\a\\A.exe")).toBe(
      "data:image/png;base64,x",
    );
    expect(api.getAppIcon).toHaveBeenCalledTimes(1);
    // What was read is at hand at once for the next render.
    expect(peekAppIcon("c:\\A\\A.EXE")).toBe("data:image/png;base64,x");
    expect(peekAppIcon("")).toBe("");
    expect(await loadAppIcon(api, "C:\\A\\bad.exe")).toBe("");
    expect(peekAppIcon("C:\\A\\bad.exe")).toBe("");
    const failing = { getAppIcon: vi.fn(async () => Promise.reject("x")) };
    expect(await loadAppIcon(failing, "C:\\A\\gone.exe")).toBe("");
  });
});

describe("finding a path for a rule by file name (UI-42)", () => {
  it("asks Main once for a name it finds, in any case", async () => {
    const api = { resolveAppPath: vi.fn(async () => "C:\\SAI\\sai2.exe") };
    expect(await loadAppPath(api, "sai2.exe")).toBe("C:\\SAI\\sai2.exe");
    expect(await loadAppPath(api, "SAI2.EXE")).toBe("C:\\SAI\\sai2.exe");
    expect(api.resolveAppPath).toHaveBeenCalledTimes(1);
    expect(api.resolveAppPath).toHaveBeenCalledWith("sai2.exe");
  });

  it("asks again about a name it did not find, which Main may find later", async () => {
    const resolveAppPath = vi
      .fn<(exe: string) => Promise<string>>()
      .mockResolvedValueOnce("")
      .mockResolvedValue("C:\\SAI\\sai2.exe");
    const api = { resolveAppPath };
    expect(await loadAppPath(api, "sai2.exe")).toBe("");
    expect(await loadAppPath(api, "sai2.exe")).toBe("C:\\SAI\\sai2.exe");
    expect(resolveAppPath).toHaveBeenCalledTimes(2);
  });

  it("shares one ask between calls that overlap", async () => {
    const api = { resolveAppPath: vi.fn(async () => "C:\\SAI\\sai2.exe") };
    await Promise.all([
      loadAppPath(api, "sai2.exe"),
      loadAppPath(api, "sai2.exe"),
    ]);
    expect(api.resolveAppPath).toHaveBeenCalledTimes(1);
  });

  it("takes only a local path to an .exe, and nothing when Main fails", async () => {
    for (const answer of [
      "\\\\server\\share\\a.exe",
      "relative\\a.exe",
      "C:\\a\\a.txt",
      "",
      undefined,
      7,
    ]) {
      const api = { resolveAppPath: vi.fn(async () => answer as string) };
      expect(await loadAppPath(api, "a.exe"), String(answer)).toBe("");
      forgetAppIcons();
    }
    const failing = {
      resolveAppPath: vi.fn(async () => Promise.reject(new Error("x"))),
    };
    expect(await loadAppPath(failing, "a.exe")).toBe("");
  });
});

describe("a rule's icon, however its path is found (UI-42)", () => {
  const icon = (path: string) => `data:image/png;base64,${path}`;
  function mains(
    options: {
      icons?: Record<string, string>;
      paths?: Record<string, string>;
    } = {},
  ) {
    return {
      getAppIcon: vi.fn(async (path: string) => options.icons?.[path] ?? ""),
      resolveAppPath: vi.fn(
        async (exe: string) => options.paths?.[exe.toLowerCase()] ?? "",
      ),
    };
  }

  it("reads the icon of the path it already knows, without asking for one", async () => {
    const api = mains({ icons: { "C:\\K\\krita.exe": icon("k") } });
    expect(await resolveAppIcon(api, "krita.exe", "C:\\K\\krita.exe")).toEqual({
      path: "C:\\K\\krita.exe",
      url: icon("k"),
    });
    expect(api.resolveAppPath).not.toHaveBeenCalled();
  });

  it("asks Main for the path when none is known, then reads its icon", async () => {
    const api = mains({
      icons: { "C:\\SAI\\sai2.exe": icon("sai") },
      paths: { "sai2.exe": "C:\\SAI\\sai2.exe" },
    });
    expect(await resolveAppIcon(api, "sai2.exe", "")).toEqual({
      path: "C:\\SAI\\sai2.exe",
      url: icon("sai"),
    });
    expect(api.resolveAppPath).toHaveBeenCalledWith("sai2.exe");
  });

  it("looks again when the remembered path gives no icon, as when the app moved", async () => {
    const api = mains({
      icons: { "D:\\New\\sai2.exe": icon("new") },
      paths: { "sai2.exe": "D:\\New\\sai2.exe" },
    });
    expect(await resolveAppIcon(api, "sai2.exe", "C:\\Old\\sai2.exe")).toEqual({
      path: "D:\\New\\sai2.exe",
      url: icon("new"),
    });
    expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Old\\sai2.exe");
  });

  it("answers no icon, and does not read the same file twice", async () => {
    const same = mains({ paths: { "sai2.exe": "C:\\SAI\\sai2.exe" } });
    expect(await resolveAppIcon(same, "sai2.exe", "C:\\SAI\\sai2.exe")).toEqual(
      { path: "C:\\SAI\\sai2.exe", url: "" },
    );
    expect(same.getAppIcon).toHaveBeenCalledTimes(1);
    forgetAppIcons();
    const none = mains();
    expect(await resolveAppIcon(none, "sai2.exe", "")).toEqual({
      path: "",
      url: "",
    });
    // A path found whose file has no icon is no icon either.
    forgetAppIcons();
    const bare = mains({ paths: { "sai2.exe": "C:\\SAI\\sai2.exe" } });
    expect(await resolveAppIcon(bare, "sai2.exe", "")).toEqual({
      path: "C:\\SAI\\sai2.exe",
      url: "",
    });
  });

  it("looks no further for a rule that is a path", async () => {
    const api = mains({ paths: { "clip.exe": "C:\\Other\\clip.exe" } });
    expect(
      await resolveAppIcon(api, "D:\\Clip\\clip.exe", "D:\\Clip\\clip.exe"),
    ).toEqual({ path: "D:\\Clip\\clip.exe", url: "" });
    expect(await resolveAppIcon(api, "\\\\server\\clip.exe", "")).toEqual({
      path: "",
      url: "",
    });
    expect(api.resolveAppPath).not.toHaveBeenCalled();
  });
});
