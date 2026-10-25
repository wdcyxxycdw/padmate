import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  archiveData,
  archiveFileName,
  baseWebPreferences,
  contentSecurityPolicy,
  macroImportName,
  maxArchiveBytes,
  validateConfigWrite,
  validateControl,
  validateDeviceKey,
  validateExport,
  validateMacroCandidate,
  validateMacroFile,
  validateMacroName,
  validateMapping,
  validateProfileID,
  validateProfileImportOptions,
  validateProfileSelection,
  validateRevision,
  validateScriptImportOptions,
  validateToggle,
} from "./security";

describe("renderer boundaries", () => {
  it("gives every window a sandboxed renderer without Node (ADR-0039, ADR-0063)", () => {
    expect(baseWebPreferences).toEqual({
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
    });
    // The overlay's page is served under the same policy as the main page:
    // local scripts only, no plugins, no frames, no inline script.
    const policy = contentSecurityPolicy(false);
    for (const directive of [
      "default-src 'self'",
      "script-src 'self'",
      "object-src 'none'",
      "frame-src 'none'",
      "base-uri 'none'",
      "form-action 'none'",
      "connect-src 'none'",
    ])
      expect(policy.split("; ")).toContain(directive);
  });
  it("keeps executable scripts local in production and development", () => {
    for (const dev of [false, true]) {
      const policy = contentSecurityPolicy(dev);
      expect(policy).toContain("script-src 'self'");
      expect(policy).not.toMatch(/unsafe-eval|unsafe-inline.*script-src/);
      expect(policy).toContain("object-src 'none'");
      expect(policy).toContain("frame-src 'none'");
      expect(policy).toContain("img-src 'self' data: blob:");
      if (!dev) expect(policy).toContain("connect-src 'none'");
    }
  });
  it("validates setting requests before crossing into the CLI bridge", () => {
    expect(validateToggle(true)).toBe(true);
    expect(() => validateToggle("yes")).toThrow();
    expect(validateConfigWrite('{"schemaVersion":0}', "")).toEqual({
      text: '{"schemaVersion":0}',
      revision: "",
    });
    expect(() =>
      validateConfigWrite("x".repeat(4 * 1024 * 1024 + 1), ""),
    ).toThrow();
    expect(() => validateConfigWrite("{}", "arbitrary")).toThrow();
  });
  it("accepts only empty or SHA-256 revisions", () => {
    const sha = "0123456789abcdef".repeat(4);
    expect(validateRevision(sha)).toBe(sha);
    expect(validateRevision("")).toBe("");
    for (const value of [
      null,
      undefined,
      sha.toUpperCase(),
      `${sha}0`,
      sha.slice(1),
      `${sha.slice(1)}g`,
      `${sha}\n`,
    ])
      expect(() => validateRevision(value)).toThrow();
  });
  it("rejects paths, reserved names and arbitrary export payloads", () => {
    expect(validateProfileID("paint-1")).toBe("paint-1");
    for (const id of ["../secret", "CON", "con", "LPT1", "a/b", "", null])
      expect(() => validateProfileID(id)).toThrow();
    expect(validateProfileSelection("paint")).toBe("paint");
    expect(validateProfileSelection(null)).toBeNull();
    for (const id of [undefined, "../x", "", 0])
      expect(() => validateProfileSelection(id)).toThrow();
    expect(validateControl("dpad.up")).toBe("dpad.up");
    expect(() => validateControl("../../x")).toThrow();
    expect(validateExport("markdown", "# Shortcuts")).toEqual({
      extension: "md",
      data: Buffer.from("# Shortcuts"),
    });
    expect(() => validateExport("exe", "hello")).toThrow();
    expect(() => validateExport("png", "data:text/html;base64,WA==")).toThrow();
    expect(() => validateExport("png", "data:image/png;base64,WA==")).toThrow();
  });
  it("requires a controller selector without control characters (IN-13)", () => {
    expect(validateDeviceKey("7")).toBe("7");
    const key = String.raw`030000007e05/p:\\?\HID#VID_057E`;
    expect(validateDeviceKey(key)).toBe(key);
    for (const value of [
      undefined,
      null,
      "",
      7,
      {},
      ["7"],
      "a\nb",
      "\u0000",
      "x".repeat(1025),
    ])
      expect(() => validateDeviceKey(value), String(value)).toThrow();
  });
  it("takes an SDL mapping string of printable ASCII, as the daemon bounds it (IN-18)", () => {
    const mapping =
      "ff00aabb000000000000000000007600,*,a:b0,leftx:a0,platform:Windows";
    expect(validateMapping(mapping)).toBe(mapping);
    for (const value of [
      undefined,
      null,
      "",
      7,
      {},
      "a\nb",
      "a\u0000b",
      "a\u007fb",
      "x".repeat(2049),
    ])
      expect(() => validateMapping(value), String(value)).toThrow(
        "Invalid mapping",
      );
    expect(validateMapping("x".repeat(2048))).toHaveLength(2048);
  });
  it("checks macro names with the daemon's rule", () => {
    for (const name of ["复制全部.ts", "下一个笔刷.js", "my macro.ts"])
      expect(validateMacroFile(name)).toBe(name);
    for (const [name, kind] of [
      ["a.TS", "extension"],
      [".ts", "empty"],
      ["v1.2.ts", "char"],
      ["CON.ts", "invalid"],
      [7, "extension"],
    ] as const)
      expect(() => validateMacroFile(name), String(name)).toThrow(
        expect.objectContaining({ data: { kind } }),
      );
    expect(validateMacroName("复制全部")).toBe("复制全部");
    for (const [name, kind] of [
      ["", "empty"],
      ["a.b", "char"],
      ["é", "invalid"],
      ["-a", "invalid"],
      ["一".repeat(41), "length"],
      [null, "empty"],
    ] as const)
      expect(() => validateMacroName(name), String(name)).toThrow(
        expect.objectContaining({ data: { kind } }),
      );
  });
  it("accepts a listed macro file of any name, but no path", () => {
    for (const name of ["复制全部.ts", "v1.2.TS", ".ts", "~1.js", "a b.Js"])
      expect(validateMacroCandidate(name)).toBe(name);
    for (const name of [
      "",
      "a",
      "a.d.ts",
      "a/b.ts",
      "a\\b.ts",
      "..\\a.ts",
      "C:a.ts",
      "a.ts:stream",
      "a\nb.ts",
      `${"a".repeat(253)}.ts`,
      null,
    ])
      expect(() => validateMacroCandidate(name), String(name)).toThrow();
  });
  it("names an imported macro after its file, or refuses it", () => {
    expect(macroImportName("brush.ts")).toBe("brush.ts");
    expect(macroImportName("复制全部.ts")).toBe("复制全部.ts");
    expect(macroImportName("My Brush (2).TS")).toBe("My Brush (2).ts");
    expect(macroImportName("v1.2 final.js")).toBe("v1-2 final.js");
    expect(macroImportName(".hidden.js")).toBe("hidden.js");
    expect(macroImportName("a...b.ts")).toBe("a-b.ts");
    expect(macroImportName("a:b*c?.ts")).toBe("a-b-c-.ts");
    expect(macroImportName("  - 笔刷\t　 2  .ts")).toBe("笔刷 2.ts");
    expect(macroImportName("~$copy.ts")).toBe("$copy.ts");
    expect(macroImportName("é.ts")).toBe("é.ts");
    expect(macroImportName(`${"一".repeat(39)} 二.ts`)).toBe(
      `${"一".repeat(39)}.ts`,
    );
    expect(macroImportName(`${"一".repeat(45)}.ts`)).toBe(
      `${"一".repeat(40)}.ts`,
    );
    for (const [base, reason] of [
      ["notes.txt", "type"],
      ["main", "type"],
      [".ts", "type"],
      ["types.d.ts", "declaration"],
      ["Brush.D.TS", "declaration"],
      ["CON.ts", "name"],
      ["com¹.js", "name"],
      ["CONIN$.ts", "name"],
      ["conout$.JS", "name"],
      ["...ts", "name"],
      [" - .ts", "name"],
    ])
      expect(() => macroImportName(base), base).toThrow(
        expect.objectContaining({ data: { reason } }),
      );
  });
  it("accepts only a replace token as import options", () => {
    const token = "0123456789abcdef".repeat(2);
    expect(validateScriptImportOptions(undefined)).toBeUndefined();
    expect(validateScriptImportOptions(null)).toBeUndefined();
    expect(validateScriptImportOptions({ replace: true, token })).toEqual({
      token,
    });
    for (const value of [
      {},
      { replace: true },
      { replace: "yes", token },
      { replace: true, token: token.toUpperCase() },
      { replace: true, token, path: "C:\\x.ts" },
      [true, token],
      token,
    ])
      expect(() => validateScriptImportOptions(value)).toThrow();
  });
  it("suggests an export file name that Windows accepts (PRO-20)", () => {
    for (const [name, want] of [
      ["Paint", "Paint.padmate"],
      ["绘画 2", "绘画 2.padmate"],
      ['a<b>c:d"e/f\\g|h?i*j', "a-b-c-d-e-f-g-h-i-j.padmate"],
      ["../../evil", "..-..-evil.padmate"],
      ["tab\there", "tab-here.padmate"],
      ["  dots...  ", "dots.padmate"],
      ["", "profile.padmate"],
      ["...", "profile.padmate"],
      ["con", "profile.padmate"],
      ["COM1", "profile.padmate"],
      ["lpt²", "profile.padmate"],
      ["CONIN$", "profile.padmate"],
      ["console", "console.padmate"],
      ["长".repeat(70), `${"长".repeat(64)}.padmate`],
    ])
      expect(archiveFileName(name), name).toBe(want);
    for (const value of [null, 3, { name: "x" }, "x".repeat(1025)])
      expect(() => archiveFileName(value)).toThrow();
  });
  it("accepts only a zip archive in base64 from the daemon", () => {
    const zip = Buffer.from([0x50, 0x4b, 0x03, 0x04, 1, 2, 3]);
    expect(archiveData(zip.toString("base64"))).toEqual(zip);
    const large = Buffer.alloc(maxArchiveBytes + 1);
    zip.copy(large);
    for (const value of [
      undefined,
      12,
      "",
      "not base64!",
      zip.toString("base64").replace(/=+$/, ""),
      Buffer.from("plain text").toString("base64"),
      large.toString("base64"),
    ])
      expect(() => archiveData(value)).toThrow("Invalid daemon response");
  });
  it("accepts a token with a profile to replace, a new name or an answer about scripts", () => {
    const token = "0123456789abcdef".repeat(2);
    expect(validateProfileImportOptions(undefined)).toBeUndefined();
    expect(validateProfileImportOptions(null)).toBeUndefined();
    expect(validateProfileImportOptions({ token, replace: "paint" })).toEqual({
      token,
      replace: "paint",
    });
    expect(validateProfileImportOptions({ token, name: " 绘画 2 " })).toEqual({
      token,
      name: " 绘画 2 ",
    });
    for (const macros of ["replace", "keep"] as const)
      expect(validateProfileImportOptions({ token, macros })).toEqual({
        token,
        macros,
      });
    for (const value of [
      {},
      { token },
      { replace: "paint" },
      { token, macros: "both" },
      { token, macros: true },
      { token, macros: "keep", replace: "paint" },
      { macros: "keep" },
      { token, replace: "../paint" },
      { token, replace: "con" },
      { token, name: "" },
      { token, name: "   " },
      { token, name: "x".repeat(65) },
      { token, name: 3 },
      { token, name: "a", replace: "paint" },
      { token: token.toUpperCase(), replace: "paint" },
      { token, replace: "paint", path: "C:\\x.padmate" },
      [token, "paint"],
      token,
    ])
      expect(() => validateProfileImportOptions(value)).toThrow();
  });
});

describe("the overlay's preload (HUD-04, HUD-03, SCR-22, ADR-0063)", () => {
  const read = (path: string) =>
    readFileSync(new URL(path, import.meta.url), "utf8");
  const preload = read("../preload/overlay.ts");

  it("exposes four things to the page: a listener for toasts, one for its settings, one for key hints and one for a script's panels", () => {
    // One global, frozen, with four members that only receive.
    expect(preload.match(/exposeInMainWorld\(/g)).toHaveLength(1);
    expect(preload).toContain(
      'contextBridge.exposeInMainWorld("padmateOverlay", Object.freeze(api));',
    );
    const body = /const api: OverlayAPI = \{([\s\S]*?)\n\};/.exec(preload)?.[1];
    expect(
      [...(body ?? "").matchAll(/^ {2}(\w+):/gm)].map((m) => m[1]),
    ).toEqual(["onToast", "onPrefs", "onHints", "onPanels"]);
    // And the type the page is written against says the same.
    const type = /export interface OverlayAPI \{([\s\S]*?)\n\}/.exec(
      read("../shared/overlay.ts"),
    )?.[1];
    expect(
      [...(type ?? "").matchAll(/^ {2}(\w+)\(/gm)].map((m) => m[1]),
    ).toEqual(["onToast", "onPrefs", "onHints", "onPanels"]);
  });

  it("only listens: it never sends, invokes or posts anything to Main", () => {
    const used = [...preload.matchAll(/ipcRenderer\.(\w+)/g)].map((m) => m[1]);
    expect(used.sort()).toEqual(["on", "on", "removeListener"]);
    expect(preload).not.toMatch(
      /\.(send|sendSync|sendToHost|invoke|postMessage)\(/,
    );
    // Four channels, listened on two ways, and what arrives is checked
    // before the page sees it: the toasts are passed on as they come, the
    // other three keep the latest message for a page that subscribes late
    // (src/preload/overlay.test.ts runs the preload and shows it).
    expect(preload.match(/ipcRenderer\.on\(/g)).toHaveLength(2);
    expect(preload).toContain("ipcRenderer.on(overlayToastChannel, callback)");
    expect(preload).toContain("ipcRenderer.on(channel, (_event");
    expect(preload).toContain(
      "onPrefs: keepLatest(overlayPrefsChannel, pageSettingsFrom)",
    );
    expect(preload).toContain(
      "onHints: keepLatest(overlayHintsChannel, hintsFrom)",
    );
    expect(preload).toContain(
      "onPanels: keepLatest(overlayPanelsChannel, panelsFrom)",
    );
    expect(preload).toContain("toastFrom(payload)");
    expect(preload).toContain("const next = parse(payload);");
  });

  it("reaches for nothing else: electron's contextBridge and ipcRenderer, and the shared channel", () => {
    expect(
      [...preload.matchAll(/^import [^;]*from "([^"]+)";/gm)].map((m) => m[1]),
    ).toEqual(["electron", "../shared/overlay"]);
    expect(preload).toMatch(/import \{ contextBridge, ipcRenderer \} from/);
    expect(preload).not.toMatch(
      /\brequire\(|\bprocess\b|webFrame|\bshell\b|child_process|node:|import\(|eval\(|new Function|localStorage|fetch\(/,
    );
  });

  it("is built as an entry of its own, beside the main window's", () => {
    const config = read("../../electron.vite.config.ts");
    expect(config).toContain('index: resolve("src/preload/index.ts")');
    expect(config).toContain('overlay: resolve("src/preload/overlay.ts")');
    expect(config).toContain('output: { format: "cjs" }');
  });

  it("is not the main window's preload, which has the whole API", () => {
    const main = read("../preload/index.ts");
    expect(main).not.toContain("padmateOverlay");
    expect(preload).not.toContain('"padmate"');
    expect(preload).not.toContain("padmate:");
  });
});
