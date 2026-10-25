import { describe, expect, it, vi } from "vitest";
import {
  APP_ICON_CACHE,
  APP_ICON_SIZE,
  appIconHandlers,
  type IconImage,
  type IconSource,
} from "./app-icon-handlers";
import { validateAppIconPath } from "./security";

const image = (url: string): IconImage => ({
  isEmpty: () => url === "",
  toDataURL: () => url,
});

function fixture(source: Partial<IconSource> = {}) {
  const thumbnail = vi.fn<IconSource["thumbnail"]>(
    source.thumbnail ?? (async () => image("data:image/png;base64,big")),
  );
  const fileIcon = vi.fn<IconSource["fileIcon"]>(
    source.fileIcon ?? (async () => image("data:image/png;base64,small")),
  );
  const handlers = appIconHandlers({ thumbnail, fileIcon });
  const run = (path: unknown) => handlers.appIcon?.run(path) as Promise<string>;
  return { thumbnail, fileIcon, handlers, run };
}

describe("app icons (UI-42)", () => {
  it("takes one path", () => {
    expect(fixture().handlers.appIcon?.count).toBe(1);
  });

  it("asks for a large thumbnail first", async () => {
    const { run, thumbnail, fileIcon } = fixture();
    expect(await run("C:\\Apps\\Krita\\krita.exe")).toBe(
      "data:image/png;base64,big",
    );
    expect(thumbnail).toHaveBeenCalledWith(
      "C:\\Apps\\Krita\\krita.exe",
      APP_ICON_SIZE,
    );
    expect(fileIcon).not.toHaveBeenCalled();
  });

  it("falls back to the file icon, then to none", async () => {
    const failing = fixture({
      thumbnail: async () => {
        throw new Error("Failed to get thumbnail");
      },
    });
    expect(await failing.run("D:/Tools/paint.exe")).toBe(
      "data:image/png;base64,small",
    );
    expect(failing.fileIcon).toHaveBeenCalledWith("D:\\Tools\\paint.exe");
    const empty = fixture({ thumbnail: async () => image("") });
    expect(await empty.run("D:\\Tools\\paint.exe")).toBe(
      "data:image/png;base64,small",
    );
    const none = fixture({
      thumbnail: async () => image(""),
      fileIcon: async () => {
        throw new Error("gone");
      },
    });
    expect(await none.run("D:\\Tools\\gone.exe")).toBe("");
  });

  it("reads each file once per session, by path in any case", async () => {
    const { run, thumbnail } = fixture();
    await run("C:\\Apps\\a.exe");
    await run("c:/apps/A.EXE");
    expect(thumbnail).toHaveBeenCalledTimes(1);
    for (let i = 0; i < APP_ICON_CACHE; i++) await run(`C:\\Apps\\${i}.exe`);
    // The oldest went to make room.
    await run("C:\\Apps\\a.exe");
    expect(thumbnail).toHaveBeenCalledTimes(APP_ICON_CACHE + 2);
  });

  it("refuses anything but a local path to an .exe", async () => {
    const { run, thumbnail, fileIcon } = fixture();
    for (const path of [
      undefined,
      7,
      "",
      "krita.exe",
      "Apps\\krita.exe",
      "\\\\server\\share\\krita.exe",
      "//server/share/krita.exe",
      "\\\\?\\C:\\krita.exe",
      "\\\\.\\C:\\krita.exe",
      "C:krita.exe",
      "C:\\Apps\\krita.txt",
      "C:\\Apps\\..\\krita.exe",
      "C:\\Apps\\.\\krita.exe",
      "C:\\Apps\\\\krita.exe",
      "C:\\Apps\\*.exe",
      "C:\\Apps\\krita.exe:stream.exe",
      "C:\\Apps\\kri\nta.exe",
      `C:\\${"a".repeat(1020)}.exe`,
    ])
      expect(() => run(path), String(path)).toThrow("Invalid app path");
    expect(thumbnail).not.toHaveBeenCalled();
    expect(fileIcon).not.toHaveBeenCalled();
    expect(validateAppIconPath("c:/Program Files/App/app.EXE")).toBe(
      "c:\\Program Files\\App\\app.EXE",
    );
  });
});
