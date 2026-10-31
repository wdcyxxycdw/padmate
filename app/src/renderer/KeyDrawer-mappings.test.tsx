// @vitest-environment jsdom
// The three mappings of the key drawer and the two actions (ADR-0076): 点按,
// 长按 and 双击; a shortcut or a script; a shortcut written held, never
// repeated; a mapping deleted for good, the last one too.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type {
  Profile,
  ProfileAction,
  ProfileBinding,
} from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { savedForm } from "./editor-model";
import { MacroContext, type MacroContextValue } from "./macro-context";
import type { Language } from "./store";

afterEach(cleanup);

function Draft({
  initial = {},
  language = "zh-CN",
  macros = { list: null, failing: new Set() },
}: {
  initial?: ProfileBinding;
  language?: Language;
  macros?: MacroContextValue;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={macros}>
      <BindingFields value={value} onChange={setValue} language={language} />
      <output data-testid="draft">{JSON.stringify(value)}</output>
    </MacroContext>
  );
}
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

function captureAPI() {
  const api = editorAPIFixture();
  return { api, ...fakeCapture(api) };
}

const cards = () =>
  [...document.querySelectorAll(".drawer-gesture-head")].map(
    (head) => head.querySelector(".drawer-gesture-name")?.textContent,
  );
const addButton = () => screen.getByTestId("add-gesture") as HTMLButtonElement;
const menuItems = () =>
  screen.getAllByRole("menuitem").map((item) => item.textContent);
/** 添加其他动作: opens the menu and picks the mapping. */
function add(mapping: string | RegExp) {
  fireEvent.click(addButton());
  fireEvent.click(screen.getByRole("menuitem", { name: mapping }));
}
/** Opens a card, unless it is open already (a click on it would close it). */
const open = (name: RegExp) => {
  const head = screen.getByRole("button", { name });
  if (head.getAttribute("aria-expanded") !== "true") fireEvent.click(head);
};
const keys = (text: string) => ({ type: "keys", keys: text }) as const;

describe("the mappings on offer", () => {
  it("are 点按, 长按 and 双击, and no other, in the menu of 添加其他动作", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    expect(screen.getByText("映射")).toBeDefined();
    expect(screen.queryByText("手势")).toBeNull();
    fireEvent.click(addButton());
    expect(menuItems()).toEqual([
      expect.stringMatching(/^长按/),
      expect.stringMatching(/^双击/),
    ]);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    cleanup();
    render(<Draft />);
    // The empty 点按 is there already, so the menu has the other two.
    fireEvent.click(addButton());
    expect(menuItems().map((text) => text?.slice(0, 2))).toEqual([
      "长按",
      "双击",
    ]);
    for (const gone of ["松开", "三击", "四击", "按下"])
      expect(screen.queryByRole("menuitem", { name: gone })).toBeNull();
  });

  it("are Tap, Hold and Double tap in English", () => {
    captureAPI();
    render(<Draft language="en" initial={{ tap: keys("F13") }} />);
    expect(
      screen.getByRole("button", { name: "Add another action" }),
    ).toBeDefined();
    fireEvent.click(addButton());
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual([
      expect.stringMatching(/^Hold/),
      expect.stringMatching(/^Double tap/),
    ]);
    fireEvent.click(screen.getByRole("menuitem", { name: /^Hold/ }));
    expect(cards()).toEqual(["Tap", "Hold"]);
    expect(
      screen.getByRole("button", { name: "Remove this mapping" }),
    ).toBeDefined();
  });

  it("show a press and a tap of the file both as 点按", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    expect(cards()).toEqual(["点按"]);
    cleanup();
    render(<Draft initial={{ tap: keys("F13"), hold: keys("F14") }} />);
    expect(cards()).toEqual(["点按", "长按"]);
  });

  it("leave a key that has only mappings of the file the drawer does not offer without cards", () => {
    captureAPI();
    const initial: ProfileBinding = {
      release: keys("F13"),
      tripleTap: keys("F14"),
      quadTap: keys("F15"),
    };
    render(<Draft initial={initial} />);
    // No empty 点按 either: the key is not unset, it has a file's mappings.
    expect(cards()).toEqual([]);
    expect(draft()).toEqual(initial);
    expect(addButton().disabled).toBe(false);
  });

  it("keep the mappings of the file that are not offered when the others change", async () => {
    const { record } = captureAPI();
    const initial: ProfileBinding = {
      press: keys("F13"),
      release: keys("F14"),
      tripleTap: keys("F15"),
    };
    render(<Draft initial={initial} />);
    expect(cards()).toEqual(["点按"]);
    await record("Ctrl+K");
    expect(draft()).toEqual({
      press: keys("Ctrl+K"),
      release: keys("F14"),
      tripleTap: keys("F15"),
    });
    // The save of such a key does not throw, and leaves them as they are.
    const profile: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: { base: { south: draft() } },
    };
    expect(() => savedForm(profile)).not.toThrow();
    expect(savedForm(profile).layers.base.south).toEqual(draft());
  });
});

describe("a tap that is a press or a tap", () => {
  it("is a press when it is the only mapping of a key that was never set", async () => {
    const { record } = captureAPI();
    render(<Draft />);
    expect(cards()).toEqual(["点按"]);
    expect(draft()).toEqual({});
    await record("Ctrl+Z");
    expect(draft()).toEqual({ press: keys("Ctrl+Z") });
  });

  it("becomes a tap, with its action and place, when a 长按 is added", async () => {
    const { record } = captureAPI();
    render(
      <Draft
        initial={{ label: "撤销", press: keys("Ctrl+Z"), haptic: "tick" }}
      />,
    );
    add(/^长按/);
    // A mapping picked from the menu is a draft until it is first edited.
    expect(draft()).toEqual({
      label: "撤销",
      press: keys("Ctrl+Z"),
      haptic: "tick",
    });
    await record("Ctrl+Y");
    expect(draft()).toEqual({
      label: "撤销",
      tap: keys("Ctrl+Z"),
      haptic: "tick",
      hold: keys("Ctrl+Y"),
    });
    expect(Object.keys(draft())).toEqual(["label", "tap", "haptic", "hold"]);
    expect(cards()).toEqual(["点按", "长按"]);
  });

  it("becomes a tap when a 双击 is added, and a press again when it is deleted", async () => {
    const { record } = captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    add(/^双击/);
    await record("F14");
    expect(draft()).toEqual({ tap: keys("F13"), doubleTap: keys("F14") });
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(draft()).toEqual({ press: keys("F13") });
    expect(cards()).toEqual(["点按"]);
  });

  it("is a press again when the 长按 beside it is deleted, and keeps being a tap while another is left", async () => {
    const { record } = captureAPI();
    render(
      <Draft
        initial={{
          tap: keys("F13"),
          hold: keys("F14"),
          doubleTap: keys("F15"),
        }}
      />,
    );
    open(/^长按/);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    // The 双击 is still there: the 点按 stays a tap.
    expect(draft()).toEqual({ tap: keys("F13"), doubleTap: keys("F15") });
    open(/^双击/);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(draft()).toEqual({ press: keys("F13") });
    // A shortcut recorded now is a press of its own.
    open(/^点按/);
    await record("F16");
    expect(draft()).toEqual({ press: keys("F16") });
  });

  it("is written as a tap when it is added to a key that has a 长按", async () => {
    const { record } = captureAPI();
    render(<Draft initial={{ hold: keys("F14") }} />);
    expect(cards()).toEqual(["长按"]);
    add(/^点按/);
    await record("F13");
    expect(draft()).toEqual({ hold: keys("F14"), tap: keys("F13") });
    expect(cards()).toEqual(["点按", "长按"]);
  });

  it("is a press when a 长按 that was only picked is dropped again", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    add(/^长按/);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(draft()).toEqual({ press: keys("F13") });
  });

  it('writes the shortcut that moves as a plain one: mode "hold" on a tap is an error of the daemon', async () => {
    const { record } = captureAPI();
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "Ctrl+Z",
            mode: "hold",
            repeat: { style: "typematic", delayMs: 300, rateHz: 10 },
          },
        }}
      />,
    );
    add(/^长按/);
    await record("Ctrl+Y");
    expect(draft()).toEqual({ tap: keys("Ctrl+Z"), hold: keys("Ctrl+Y") });
  });

  it("moves a script with it", () => {
    captureAPI();
    const macros = {
      list: {
        macros: [{ file: "复制全部.ts", name: "复制全部", usedBy: [] }],
        missing: [],
      },
      failing: new Set<string>(),
    };
    render(
      <Draft
        macros={macros}
        initial={{ press: { type: "macro", file: "复制全部.ts" } }}
      />,
    );
    add(/^双击/);
    fireEvent.click(
      within(screen.getByTestId("action-type")).getByRole("radio", {
        name: "脚本",
      }),
    );
    fireEvent.change(screen.getByRole("combobox", { name: "选择脚本" }), {
      target: { value: "复制全部.ts" },
    });
    expect(draft()).toEqual({
      tap: { type: "macro", file: "复制全部.ts" },
      doubleTap: { type: "macro", file: "复制全部.ts" },
    });
  });

  it("is not moved when a mapping that is already there is edited", async () => {
    const { record } = captureAPI();
    // A press and a hold together, as an old file may have them.
    render(<Draft initial={{ press: keys("F13"), hold: keys("F14") }} />);
    open(/^长按/);
    await record("F15");
    expect(draft()).toEqual({ press: keys("F13"), hold: keys("F15") });
  });

  it("stays a press when its action cannot be a tap's", async () => {
    // Key hints and precision are held with the button: a tap would be an
    // error of the daemon.
    const { record } = captureAPI();
    const hints = { type: "hud", op: "hints" } as const;
    render(<Draft initial={{ press: hints }} />);
    add(/^长按/);
    await record("F14");
    expect(draft()).toEqual({ press: hints, hold: keys("F14") });
  });
});

describe("deleting a mapping", () => {
  it("deletes the last one for good: the key has none, and only 添加其他动作 is left", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(draft()).toEqual({});
    expect(cards()).toEqual([]);
    expect(screen.queryByTestId("action-type")).toBeNull();
    expect(screen.queryByRole("group", { name: "快捷键" })).toBeNull();
    // Nothing more is said: no empty 点按 comes back, no sentence either.
    expect(screen.queryByText(/尚未设置/)).toBeNull();
    expect(document.querySelector(".drawer-empty")).toBeNull();
    expect(document.activeElement).toBe(addButton());
    // All three can be added again.
    fireEvent.click(addButton());
    expect(menuItems().map((text) => text?.slice(0, 2))).toEqual([
      "点按",
      "长按",
      "双击",
    ]);
  });

  it("keeps the label and the other settings of the key", () => {
    captureAPI();
    render(
      <Draft initial={{ label: "撤销", haptic: "tick", press: keys("F13") }} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(draft()).toEqual({ label: "撤销", haptic: "tick" });
    expect(cards()).toEqual([]);
  });

  it("deletes the empty 点按 of a key that was never set, and it does not come back", () => {
    captureAPI();
    render(<Draft />);
    expect(cards()).toEqual(["点按"]);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(cards()).toEqual([]);
    expect(draft()).toEqual({});
    expect(document.activeElement).toBe(addButton());
  });

  it("does not bring the empty 点按 back when the key is set and cleared again", async () => {
    const { record } = captureAPI();
    render(<Draft />);
    await record("F13");
    expect(draft()).toEqual({ press: keys("F13") });
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(cards()).toEqual([]);
    add(/^点按/);
    await record("F14");
    expect(draft()).toEqual({ press: keys("F14") });
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(cards()).toEqual([]);
    expect(draft()).toEqual({});
  });

  it("asks first for an action the editor cannot set again", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          tap: keys("F13"),
          hold: { type: "profile", profile: "photo" },
        }}
      />,
    );
    open(/^长按/);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(screen.getByRole("dialog", { name: "删除此映射？" })).toBeDefined();
    expect(draft().hold).toEqual({ type: "profile", profile: "photo" });
    fireEvent.click(screen.getByRole("button", { name: "删除" }));
    expect(draft()).toEqual({ press: keys("F13") });
  });

  it("is said in English", () => {
    captureAPI();
    render(<Draft language="en" initial={{ press: keys("F13") }} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Remove this mapping" }),
    );
    expect(draft()).toEqual({});
    expect(document.activeElement).toBe(addButton());
  });
});

describe("the actions on offer", () => {
  const types = () =>
    within(screen.getByTestId("action-type"))
      .getAllByRole("radio")
      .map((radio) => radio.textContent);

  it("are a shortcut and a script on every mapping, and no more", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          tap: keys("F13"),
          hold: keys("F14"),
          doubleTap: keys("F15"),
        }}
      />,
    );
    for (const mapping of [/^点按/, /^长按/, /^双击/]) {
      open(mapping);
      expect(types(), String(mapping)).toEqual(["快捷键", "脚本"]);
    }
    for (const gone of ["输入文字", "精确模式", "按键提示"])
      expect(screen.queryByRole("radio", { name: gone })).toBeNull();
  });

  it("are a shortcut and a script in English", () => {
    captureAPI();
    render(<Draft language="en" initial={{ press: keys("F13") }} />);
    expect(types()).toEqual(["Shortcut", "Script"]);
  });

  it.each<[string, ProfileAction]>([
    ["a text", { type: "text", text: "你好" }],
    ["a precision action", { type: "mouse", op: "precision", factor: 0.5 }],
    ["key hints", { type: "hud", op: "hints" }],
    [
      "a sequence with a text step",
      { type: "sequence", steps: [{ keys: "A" }, { text: "x" }] },
    ],
  ])(
    "show %s of the file with no type selected, and a choice replaces it",
    (_, action) => {
      captureAPI();
      render(<Draft initial={{ hold: action }} />);
      // Opening it changes nothing.
      expect(draft()).toEqual({ hold: action });
      for (const radio of within(
        screen.getByTestId("action-type"),
      ).getAllByRole("radio"))
        expect(radio.getAttribute("aria-checked")).toBe("false");
      // The card still says what the mapping does.
      expect(
        screen.getByRole("button", { name: /^长按/ }).textContent?.length,
      ).toBeGreaterThan(2);
      fireEvent.click(
        within(screen.getByTestId("action-type")).getByRole("radio", {
          name: "快捷键",
        }),
      );
      expect(draft()).toEqual({ hold: keys("") });
    },
  );

  it("let a script replace such an action once it is picked, not before", () => {
    captureAPI();
    const macros = {
      list: {
        macros: [{ file: "复制全部.ts", name: "复制全部", usedBy: [] }],
        missing: [],
      },
      failing: new Set<string>(),
    };
    const hints = { type: "hud", op: "hints" } as const;
    render(<Draft macros={macros} initial={{ press: hints }} />);
    fireEvent.click(
      within(screen.getByTestId("action-type")).getByRole("radio", {
        name: "脚本",
      }),
    );
    expect(draft().press).toEqual(hints);
    fireEvent.change(screen.getByRole("combobox", { name: "选择脚本" }), {
      target: { value: "复制全部.ts" },
    });
    expect(draft().press).toEqual({ type: "macro", file: "复制全部.ts" });
  });

  it("show a pointer jump of the file locked, as a profile switch is", () => {
    captureAPI();
    const jump = { type: "mouse", op: "jump", x: 0.5, y: 0.25 } as const;
    render(<Draft initial={{ tap: keys("F13"), hold: jump }} />);
    const hold = screen.getByRole("button", { name: /^长按/ });
    expect(hold.querySelector(".drawer-summary-locked")?.textContent).toBe(
      "指针跳到屏幕位置 50%，25%",
    );
    fireEvent.click(hold);
    expect(screen.queryByTestId("action-type")).toBeNull();
    expect(screen.getByTestId("locked-action").textContent).toContain(
      "把指针移到屏幕位置（50%，25%）（在配置文件中设置）",
    );
    expect(draft().hold).toEqual(jump);
    fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
    expect(
      screen.getByRole("dialog", { name: "删除此映射？" }).textContent,
    ).toContain("此按键的长按当前会把指针移到屏幕位置（50%，25%）");
    expect(draft().hold).toEqual(jump);
  });
});

describe("a shortcut the drawer writes", () => {
  it("has no way to choose how it is sent, or to repeat", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
    expect(screen.queryByRole("button", { name: "连点" })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "每秒次数" })).toBeNull();
    cleanup();
    render(<Draft language="en" initial={{ press: keys("F13") }} />);
    expect(screen.queryByRole("radiogroup", { name: "Sending" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "Repeat" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Turbo" })).toBeNull();
  });

  it.each([
    ["a tap mode", { mode: "tap" }],
    ["a typematic repeat", { repeat: { style: "typematic", rateHz: 20 } }],
    [
      "a turbo",
      { mode: "tap", repeat: { style: "tap", delayMs: 0, rateHz: 12 } },
    ],
    [
      "a repeat that follows the depth",
      { repeat: { rateHz: 20, minRateHz: 3 } },
    ],
  ] as const)(
    "is held and not repeated when a mapping that had %s is edited",
    async (_, extra) => {
      const { record } = captureAPI();
      render(
        <Draft
          initial={{ press: { type: "keys", keys: "Ctrl+K", ...extra } }}
        />,
      );
      // Opening it changes nothing.
      expect(draft().press).toMatchObject(extra);
      await record("Ctrl+L");
      expect(draft()).toEqual({ press: keys("Ctrl+L") });
    },
  );

  it("is held and not repeated on a 长按, and a tap or a double tap sends one press", async () => {
    const { record } = captureAPI();
    render(
      <Draft
        initial={{
          tap: { type: "keys", keys: "F13", mode: "tap" },
          hold: {
            type: "keys",
            keys: "F14",
            mode: "tap",
            repeat: { style: "tap", rateHz: 8 },
          },
          doubleTap: { type: "keys", keys: "F15", mode: "tap" },
        }}
      />,
    );
    open(/^长按/);
    await record("Ctrl+K");
    expect(draft().hold).toEqual(keys("Ctrl+K"));
    // The others were not edited: they are as the file has them.
    expect(draft().tap).toEqual({ type: "keys", keys: "F13", mode: "tap" });
    open(/^双击/);
    await record("Ctrl+L");
    expect(draft().doubleTap).toEqual(keys("Ctrl+L"));
  });

  it("is written as one held shortcut when a sequence goes down to one step", () => {
    captureAPI();
    render(
      <Draft
        initial={{ press: { type: "keys", keys: ["F13", "F14"], mode: "tap" } }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
    expect(draft().press).toEqual(keys("F14"));
    expect(shortcutValue()).toBe("F14");
  });

  it("is a list that taps while it has several steps", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    fireEvent.click(screen.getByRole("button", { name: "增加动作序列" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "快捷键" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: ["F13", ""],
      mode: "tap",
    });
  });
});

describe("the advanced settings", () => {
  it("name the scan code switch as the drawer does, and say it ignores the layout", () => {
    captureAPI();
    render(<Draft initial={{ press: keys("F13") }} />);
    fireEvent.click(screen.getByText("高级"));
    const row = screen
      .getByRole("switch", { name: "映射扫描码" })
      .closest(".drawer-option");
    expect(row?.textContent).toContain("不受输入法或键盘布局影响");
    expect(screen.queryByText("按物理位置")).toBeNull();
    cleanup();
    render(<Draft language="en" initial={{ press: keys("F13") }} />);
    fireEvent.click(screen.getByText("Advanced"));
    expect(screen.getByRole("switch", { name: "Map scan code" })).toBeDefined();
    expect(
      screen.getByText(/independent of the input method or layout/),
    ).toBeDefined();
  });
});
