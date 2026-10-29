// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "../shared/generated/schema";
import { listControlName } from "./BindingsListDialog";
import { controllerLabels } from "./controller-model";
import {
  type SheetOptions,
  shortcutSheet,
  shortcutSheetMarkdown,
  shortcutSheetPNG,
  wrapText,
} from "./shortcut-sheet";
import type { Language } from "./store";

afterEach(() => vi.restoreAllMocks());

function options(language: Language = "zh-CN", type = "xboxone") {
  const labels = controllerLabels(type);
  return {
    language,
    functionKey: "start",
    controlName: (control: string) =>
      listControlName(control, labels, language),
  } satisfies SheetOptions;
}

// A painting profile as a user sets it up: a few keys with a shortcut, the
// rest only named (OUT-18), on a standard pad and on a sideways Joy-Con.
const standardPaint: Profile = {
  schemaVersion: 1,
  name: "绘画",
  analog: { ls: { mode: "fourWay" } },
  layers: {
    base: {
      lt: {
        label: "抓手",
        press: { type: "keys", keys: "Space", mode: "hold" },
      },
      leftshoulder: {
        label: "Ctrl",
        press: { type: "keys", keys: "Ctrl", mode: "hold" },
      },
      "dpad.left": {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      "dpad.right": { label: "重做" },
      "dpad.up": { label: "画笔" },
      "dpad.down": { label: "橡皮" },
      "ls.left": { label: "笔刷缩小" },
      "ls.right": { label: "笔刷放大" },
      "ls.up": { label: "画布放大" },
      "ls.down": { label: "画布缩小" },
      rightshoulder: {
        label: "Shift",
        press: { type: "keys", keys: "Shift", mode: "hold" },
      },
      rt: {
        label: "Alt",
        press: { type: "keys", keys: "Alt", mode: "hold" },
      },
      south: {
        label: "自由变换",
        press: { type: "keys", keys: "Ctrl+T", mode: "tap" },
      },
      north: { label: "新建图层" },
      east: { label: "水平翻转画布" },
    },
  },
};

const joyConPaint: Profile = {
  schemaVersion: 1,
  name: "绘画",
  analog: { ls: { mode: "fourWay" } },
  layers: {
    base: {
      "ls.left": { label: "笔刷缩小" },
      "ls.right": { label: "笔刷放大" },
      "ls.up": { label: "画布放大" },
      "ls.down": { label: "画布缩小" },
      south: { label: "画笔" },
      east: { label: "橡皮" },
      west: {
        label: "抓手",
        press: { type: "keys", keys: "Space", mode: "hold" },
      },
      north: {
        label: "Ctrl",
        press: { type: "keys", keys: "Ctrl", mode: "hold" },
      },
      leftshoulder: {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      rightshoulder: { label: "重做" },
      leftstick: { label: "水平翻转画布" },
    },
  },
};

const mixed: Profile = {
  schemaVersion: 1,
  name: "Paint | pen",
  controlNames: { north: "笔刷 | 循环" },
  layers: {
    base: {
      south: {
        label: "撤销",
        tap: { type: "keys", keys: "Ctrl+Z" },
        hold: { type: "keys", keys: "LCtrl+sc:0x64" },
      },
      north: { press: { type: "keys", keys: "F14", mode: "tap" } },
      east: { press: { type: "script", name: "cycleBrush", args: {} } },
      west: { press: { type: "keys", keys: ["Ctrl+K", "S"] } },
      back: { press: { type: "profile", profile: "photo" } },
      start: { press: { type: "keys", keys: "" } },
      "dpad.left": { block: true },
    },
    tools: {
      south: { press: { type: "keys", keys: "F15" } },
      color: "#ff0000",
    },
  },
  common: { guide: { press: { type: "layer", op: "toggle", layer: "tools" } } },
};

describe("shortcut table", () => {
  it("lists each shortcut with the button as printed and the command to set", () => {
    const sheet = shortcutSheet(mixed, options());
    expect(sheet.title).toBe("Paint | pen 快捷键对照表");
    expect(sheet.header).toEqual(["手柄按键", "快捷键", "请在软件中设置为"]);
    expect(sheet.rows).toEqual([
      ["A 点按", "Ctrl+Z", "撤销（通常已是默认）"],
      ["A 长按", "Ctrl+F13", "撤销"],
      ["笔刷 | 循环", "F14", "（未命名）"],
      ["X", "Ctrl+K → S", "（未命名，通常已是默认）"],
    ]);
    // Only base is shown; the other group and common are only counted.
    expect(sheet.footnote).toBe("配置文件中还有 2 项设置未列出。");
    expect(sheet.notes).toEqual([
      "仅在此配置使用中时，按下手柄按键才会输入快捷键。",
      "Krita 5.2.10 起：请先在 设置 → 配置 Krita → 常规 → 杂项 取消勾选“忽略 F13–F24”。",
      "Aseprite 不支持 F13–F24。",
    ]);
  });

  it("counts the function set among the settings it leaves out, and lists no binding of the function key", () => {
    const file: Profile = {
      ...mixed,
      functionSet: {
        south: { press: { type: "keys", keys: "Ctrl+Y" } },
        north: { press: { type: "keys", keys: "F13" } },
      },
      layers: {
        base: {
          ...mixed.layers.base,
          start: { press: { type: "keys", keys: "Ctrl+S" } },
          back: { press: { type: "keys", keys: "Ctrl+V" } },
        },
        tools: mixed.layers.tools,
      },
    };
    // Menu is the function key: what the file binds on it is ignored, and
    // View's binding is a shortcut like any other.
    const sheet = shortcutSheet(file, options());
    const text = sheet.rows.flat().join("|");
    expect(text).not.toContain("Ctrl+S");
    expect(text).not.toContain("Ctrl+Y");
    expect(text).toContain("Ctrl+V");
    expect(sheet.rows).toHaveLength(5);
    // The other group and common, and the two bindings of the function set.
    expect(sheet.footnote).toBe("配置文件中还有 4 项设置未列出。");
    // With View chosen it is the other way round, under the alias too.
    const { back: _view, ...others } = file.layers.base;
    const back = shortcutSheet(
      {
        ...file,
        layers: {
          ...file.layers,
          base: {
            ...others,
            select: { press: { type: "keys", keys: "Ctrl+B" } },
          },
        },
      },
      { ...options(), functionKey: "back" },
    );
    const backText = back.rows.flat().join("|");
    expect(backText).not.toContain("Ctrl+B");
    expect(backText).toContain("Ctrl+S");
  });

  it("writes Markdown with escaped cells and never names key groups", () => {
    const markdown = shortcutSheetMarkdown(shortcutSheet(mixed, options()));
    expect(markdown).toContain("# Paint \\| pen 快捷键对照表");
    expect(markdown).toContain(
      "在绘画软件的快捷键设置中选中右列的功能，再按左列的手柄按键，padmate 将自动输入快捷键。",
    );
    expect(markdown).toContain("| 手柄按键 | 快捷键 | 请在软件中设置为 |");
    expect(markdown).toContain("| 笔刷 \\| 循环 | F14 | （未命名） |");
    expect(markdown).toContain("\n配置文件中还有 2 项设置未列出。\n");
    expect(markdown).toContain("\n- Aseprite 不支持 F13–F24。\n");
    for (const hidden of ["cycleBrush", "photo", "tools", "F15", "层", "Layer"])
      expect(markdown).not.toContain(hidden);
  });

  it("speaks English and leaves out the F13–F24 notes when no such key is used", () => {
    const sheet = shortcutSheet(
      {
        schemaVersion: 1,
        name: "Photo",
        layers: {
          base: {
            south: { label: "Undo", press: { type: "keys", keys: "Ctrl+Z" } },
          },
        },
      },
      options("en"),
    );
    expect(sheet.title).toBe("Photo shortcut table");
    // The fixed default goes by its ID, whatever name it stores (PRO-28).
    expect(
      shortcutSheet(mixed, { ...options("en"), profileName: "Default" }).title,
    ).toBe("Default shortcut table");
    expect(sheet.header).toEqual(["Button", "Shortcut", "Assign it to"]);
    expect(sheet.rows).toEqual([["A", "Ctrl+Z", "Undo (usually the default)"]]);
    expect(sheet.footnote).toBe("");
    expect(sheet.notes).toEqual([
      "The buttons type these shortcuts while the profile is in use.",
    ]);
  });

  it("says a modifier held alone needs no setting", () => {
    const base = {
      leftshoulder: { label: "Ctrl", press: { type: "keys", keys: "LCtrl" } },
      rightshoulder: { label: "吸管", press: { type: "keys", keys: "Alt" } },
      lt: { press: { type: "keys", keys: "Shift", mode: "tap" } },
      south: { label: "撤销", press: { type: "keys", keys: "Ctrl+Z" } },
    } as const;
    const profile: Profile = {
      schemaVersion: 1,
      name: "Paint",
      layers: { base },
    };
    expect(shortcutSheet(profile, options()).rows).toEqual([
      ["LB", "Ctrl", "（按住即可，无需设置）"],
      ["RB", "Alt", "吸管（按住即可，无需设置）"],
      ["LT", "Shift", "（无需设置）"],
      ["A", "Ctrl+Z", "撤销（通常已是默认）"],
    ]);
    const english: Profile = {
      ...profile,
      layers: {
        base: {
          ...base,
          rightshoulder: { ...base.rightshoulder, label: "Eyedropper" },
        },
      },
    };
    expect(shortcutSheet(english, options("en")).rows).toEqual([
      ["LB", "Ctrl", "(just hold it; nothing to set)"],
      ["RB", "Alt", "Eyedropper (just hold it; nothing to set)"],
      ["LT", "Shift", "(nothing to set)"],
      ["A", "Ctrl+Z", "撤销 (usually the default)"],
    ]);
  });

  it("writes the wheel keys as the app shows them (ADR-0062)", () => {
    const wheel: Profile = {
      schemaVersion: 1,
      name: "Zoom",
      layers: {
        base: {
          south: {
            label: "缩放",
            press: { type: "keys", keys: "lctrl+wheelup", mode: "tap" },
          },
          north: {
            press: { type: "keys", keys: ["WheelDown", "Shift+WheelLeft"] },
          },
        },
      },
    };
    expect(shortcutSheet(wheel, options()).rows).toEqual([
      ["A", "Ctrl+滚轮↑", "缩放（通常已是默认）"],
      ["Y", "滚轮↓ → Shift+滚轮←", "（未命名，通常已是默认）"],
    ]);
    const english = shortcutSheet(wheel, options("en"));
    expect(english.rows.map(([, keys]) => keys)).toEqual([
      "Ctrl+Wheel↑",
      "Wheel↓ → Shift+Wheel←",
    ]);
    const markdown = shortcutSheetMarkdown(english);
    expect(markdown).toContain("| A | Ctrl+Wheel↑ |");
    // No wheel key makes the sheet name an F13–F24 note.
    expect(english.notes).toHaveLength(1);
  });

  it("writes the mouse buttons as the app shows them (ADR-0062)", () => {
    const buttons: Profile = {
      schemaVersion: 1,
      name: "Click",
      layers: {
        base: {
          south: {
            label: "右击",
            press: { type: "keys", keys: "lctrl+rbutton", mode: "tap" },
          },
          north: {
            press: {
              type: "keys",
              keys: ["MouseLeft", "Shift+XButton1"],
              mode: "tap",
            },
          },
          east: { press: { type: "keys", keys: "MouseMiddle" } },
        },
      },
    };
    expect(
      shortcutSheet(buttons, options()).rows.map(([, keys]) => keys),
    ).toEqual(["Ctrl+右键", "左键 → Shift+侧键1", "中键"]);
    const english = shortcutSheet(buttons, options("en"));
    expect(english.rows.map(([, keys]) => keys)).toEqual([
      "Ctrl+RMB",
      "LMB → Shift+X1",
      "MMB",
    ]);
    expect(shortcutSheetMarkdown(english)).toContain("| A | Ctrl+RMB |");
    // No button makes the sheet name an F13–F24 note.
    expect(english.notes).toHaveLength(1);
  });

  it("says so when there is nothing to list", () => {
    const markdown = shortcutSheetMarkdown(
      shortcutSheet(
        { schemaVersion: 1, name: "Empty", layers: { base: {} } },
        options(),
      ),
    );
    expect(markdown).toContain("尚未设置快捷键。");
    expect(markdown).not.toContain("| --- |");
  });

  it("lists only the shortcuts that keys set, not the keys that are only named", () => {
    for (const id of ["standard", "joycon"] as const) {
      const sheet = shortcutSheet(
        id === "joycon" ? joyConPaint : standardPaint,
        options("zh-CN", id === "joycon" ? "joyconleft" : "xboxone"),
      );
      const keys = sheet.rows.map(([, keys]) => keys);
      expect(new Set(keys).size).toBe(keys.length);
      expect(sheet.footnote).toBe("");
      // No F13–F24, so no notes about apps that ignore them.
      expect(sheet.notes).toHaveLength(1);
    }
    const standard = shortcutSheet(standardPaint, options());
    expect(standard.title).toBe("绘画 快捷键对照表");
    expect(standard.rows).toEqual([
      ["LT", "Space", "抓手（通常已是默认）"],
      ["LB", "Ctrl", "（按住即可，无需设置）"],
      ["十字键 ←", "Ctrl+Z", "撤销（通常已是默认）"],
      ["RB", "Shift", "（按住即可，无需设置）"],
      ["RT", "Alt", "（按住即可，无需设置）"],
      ["A", "Ctrl+T", "自由变换（通常已是默认）"],
    ]);
  });
});

describe("shortcut table image", () => {
  it("breaks Chinese between characters and Latin text between words", () => {
    const measure = (text: string) => [...text].length;
    expect(wrapText("在绘画软件的快捷键设置里", 5, measure)).toEqual([
      "在绘画软件",
      "的快捷键设",
      "置里",
    ]);
    // Closing punctuation stays on the line of the character before it.
    expect(wrapText("选中功能，再按键", 4, measure)).toEqual([
      "选中功",
      "能，再按",
      "键",
    ]);
    expect(wrapText("press the button", 10, measure)).toEqual([
      "press the",
      "button",
    ]);
    expect(wrapText("Ctrl+Shift+F13", 6, measure)).toEqual([
      "Ctrl+S",
      "hift+F",
      "13",
    ]);
    expect(wrapText("", 10, measure)).toEqual([""]);
  });

  it("draws every cell with a font that has Chinese glyphs", () => {
    const drawn: { text: string; font: string; y: number }[] = [];
    const sizes: number[] = [];
    const context = {
      font: "",
      fillStyle: "",
      textBaseline: "",
      measureText: (text: string) => ({ width: [...text].length * 16 }),
      fillText(text: string, _x: number, y: number) {
        drawn.push({ text, font: this.font, y });
      },
      fillRect: vi.fn(),
      scale: vi.fn(),
    };
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
      function (this: HTMLCanvasElement) {
        sizes.push(this.width, this.height);
        return context as unknown as CanvasRenderingContext2D;
      },
    );
    const toDataURL = vi
      .spyOn(HTMLCanvasElement.prototype, "toDataURL")
      .mockImplementation(function (this: HTMLCanvasElement) {
        sizes.push(this.width, this.height);
        return "data:image/png;base64,AA==";
      });
    const sheet = shortcutSheet(standardPaint, options());
    expect(shortcutSheetPNG(sheet)).toBe("data:image/png;base64,AA==");
    expect(toDataURL).toHaveBeenCalledWith("image/png");
    const texts = drawn.map((entry) => entry.text);
    expect(texts).toContain("绘画 快捷键对照表");
    for (const cell of sheet.rows.flat()) expect(texts).toContain(cell);
    for (const { text, font } of drawn)
      if (/[一-鿿]/.test(text)) expect(font).toContain("Microsoft YaHei");
    // Sized after measuring, at twice the size; every line fits on it.
    const [width, height] = sizes.slice(-2);
    expect(width).toBe(2400);
    expect(Math.max(...drawn.map((entry) => entry.y)) * 2).toBeLessThan(height);
    expect(context.scale).toHaveBeenCalledWith(2, 2);
  });
});
