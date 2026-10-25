// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { type ComponentType, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ScriptRegistration } from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import { MacroContext } from "../macro-context";
import type { Language, PadInput } from "../store";
import {
  type AnalogPanelProps,
  DpadSettings,
  StickPanel,
  TriggerSettings,
} from "./AnalogPanels";
import {
  BindingSummary,
  bindingText,
  clearParameterClipboard,
} from "./analog-parts";

// The gesture editor belongs to the key drawer; a stub shows what the panels
// hand to it and lets a test write one binding.
vi.mock("../BindingEditor", async () => {
  const { createElement } = await import("react");
  return {
    BindingFields: ({
      value,
      onChange,
      disabled = false,
      suggestPress = true,
    }: {
      value: Record<string, unknown>;
      onChange: (value: Record<string, unknown>) => void;
      disabled?: boolean;
      suggestPress?: boolean;
    }) =>
      createElement(
        "div",
        {
          "data-testid": "binding-fields",
          "data-disabled": String(disabled),
          "data-suggest-press": String(suggestPress),
        },
        createElement(
          "output",
          { "data-testid": "binding-value" },
          JSON.stringify(value),
        ),
        createElement(
          "button",
          {
            type: "button",
            onClick: () =>
              onChange({ ...value, press: { type: "keys", keys: "F20" } }),
          },
          "stub: send F20",
        ),
      ),
  };
});

afterEach(() => {
  cleanup();
  clearParameterClipboard();
});

const layered: Profile = {
  schemaVersion: 1,
  name: "Analog",
  defaultLayer: "base",
  analog: { ls: { mode: "standard", deadzone: 0.6, saturation: 0.95 } },
  common: { back: { press: { type: "layer", op: "toggle", layer: "alt" } } },
  layers: {
    base: {
      "ls.right": { press: { type: "keys", keys: "F18", mode: "hold" } },
      south: { label: "Undo", press: { type: "keys", keys: "Ctrl+Z" } },
    },
    alt: {
      color: "#3366FF",
      "ls.up": { press: { type: "keys", keys: "B" } },
      leftstick: { block: true },
    },
  },
  script: { api: "0.21", entry: "main.ts" },
};
const timing = { holdMs: 250, multiTapMs: 220, tapPressMs: 20, tapGapMs: 20 };
const axis = (fraction: number) => Math.round(fraction * 32767);

function Harness({
  panel: Panel = StickPanel,
  control = "leftstick",
  initial = layered,
  input,
  registrations = [],
  language = "zh-CN",
  disabled,
  menuRequest,
  calibrated,
}: {
  panel?: ComponentType<AnalogPanelProps>;
  control?: string;
  initial?: Profile;
  input?: PadInput;
  registrations?: ScriptRegistration[];
  language?: Language;
  disabled?: boolean;
  menuRequest?: number;
  calibrated?: { ls: boolean; rs: boolean };
}) {
  const [profile, setProfile] = useState<Profile>(() =>
    structuredClone(initial),
  );
  return (
    <>
      <Panel
        profile={profile}
        control={control}
        onChange={setProfile}
        input={input}
        language={language}
        timing={timing}
        registrations={registrations}
        profiles={[]}
        disabled={disabled}
        menuRequest={menuRequest}
        calibrated={calibrated}
      />
      <output data-testid="draft">{JSON.stringify(profile)}</output>
    </>
  );
}
const draft = () =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}") as Profile;
/** Types into a field and leaves it, which commits the text (UI-43). */
function commit(field: Element, init: { target: { value: string } }) {
  fireEvent.change(field, init);
  fireEvent.blur(field);
}
/** The rest of the file must stay exactly as it was. */
function expectOthersKept(profile: Profile) {
  expect(profile.layers.alt).toEqual(layered.layers.alt);
  expect(profile.common).toEqual(layered.common);
  expect(profile.defaultLayer).toBe("base");
  expect(profile.script).toEqual(layered.script);
  expect(profile.layers.base.south).toEqual(layered.layers.base.south);
}
const cell = (direction: string) => {
  const found = document.querySelector<HTMLButtonElement>(
    `.analog-cell[data-direction="${direction}"]`,
  );
  if (!found) throw new Error(`no ${direction} cell`);
  return found;
};
const shownDirections = () =>
  [...document.querySelectorAll<HTMLElement>(".analog-cell")].map(
    (element) => element.dataset.direction,
  );
const registration = (
  id: number,
  control: string,
  changes: Partial<ScriptRegistration> = {},
): ScriptRegistration => ({
  id,
  control,
  gesture: "press",
  scope: "base",
  observe: false,
  claim: false,
  priority: 3,
  label: "Brush direction",
  timing: {},
  ...changes,
});

describe("stick panel", () => {
  it("says when the live controller has its own calibration for this stick", () => {
    const { unmount } = render(<Harness />);
    expect(screen.queryByTestId("stick-calibrated")).toBeNull();
    unmount();
    render(<Harness calibrated={{ ls: false, rs: true }} />);
    expect(screen.queryByTestId("stick-calibrated")).toBeNull();
    cleanup();
    render(<Harness calibrated={{ ls: true, rs: false }} />);
    expect(screen.getByTestId("stick-calibrated").textContent).toBe(
      "本手柄已单独调校",
    );
  });

  it("edits dead zone and full push in percent and keeps explicit zero", () => {
    render(<Harness />);
    const deadzone = screen.getByRole("spinbutton", { name: "死区" });
    const saturation = screen.getByRole("spinbutton", { name: "推到底" });
    expect((deadzone as HTMLInputElement).value).toBe("60");
    expect((saturation as HTMLInputElement).value).toBe("95");
    // Typing is not an edit until Enter or leaving the field.
    fireEvent.change(deadzone, { target: { value: "15" } });
    expect(draft().analog?.ls?.deadzone).toBe(0.6);
    fireEvent.keyDown(deadzone, { key: "Enter" });
    expect(draft().analog?.ls?.deadzone).toBe(0.15);
    commit(deadzone, { target: { value: "17.5" } });
    expect(draft().analog?.ls?.deadzone).toBe(0.175);
    commit(deadzone, { target: { value: "0" } });
    expect(draft().analog?.ls?.deadzone).toBe(0);
    // Out of range: not written, and the field says why.
    commit(deadzone, { target: { value: "150" } });
    expect(deadzone.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole("alert").textContent).toBe("请输入 0–100 的数值");
    expect(draft().analog?.ls?.deadzone).toBe(0);
    // Escape puts the saved value back.
    fireEvent.keyDown(deadzone, { key: "Escape" });
    expect((deadzone as HTMLInputElement).value).toBe("0");
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.change(screen.getByRole("slider", { name: "推到底" }), {
      target: { value: "90" },
    });
    expect(draft().analog?.ls).toEqual({
      mode: "standard",
      deadzone: 0,
      saturation: 0.9,
    });
    expect((saturation as HTMLInputElement).value).toBe("90");
    expectOthersKept(draft());
  });

  it("explains a dead zone that reaches full push", () => {
    render(<Harness />);
    commit(screen.getByRole("spinbutton", { name: "死区" }), {
      target: { value: "99" },
    });
    expect(screen.getByRole("alert").textContent).toContain(
      "“推到底”必须大于“死区”",
    );
  });

  it("switches direction modes, including diagonals only under advanced", () => {
    render(<Harness />);
    const mode = screen.getByRole("radiogroup", { name: "方向模式" });
    expect(
      within(mode)
        .getByRole("radio", { name: "4 方向" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(within(mode).queryByRole("radio", { name: "只有斜向" })).toBeNull();
    expect(shownDirections()).toEqual(["up", "right", "down", "left"]);
    fireEvent.click(within(mode).getByRole("radio", { name: "8 方向" }));
    expect(draft().analog?.ls?.mode).toBe("eightWay");
    expect(shownDirections()).toHaveLength(8);
    expect(screen.getByText(/斜推时发送单独的斜向按键/)).toBeDefined();
    fireEvent.click(within(mode).getByRole("radio", { name: "只取 1 个方向" }));
    expect(draft().analog?.ls?.mode).toBe("fourWay");
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    expect(advanced.open).toBe(false);
    advanced.open = true;
    fireEvent.click(within(advanced).getByRole("switch", { name: "只有斜向" }));
    expect(draft().analog?.ls?.mode).toBe("fourWayDiagonal");
    expect(
      within(mode)
        .getByRole("radio", { name: "只有斜向" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(shownDirections()).toEqual([
      "upleft",
      "upright",
      "downleft",
      "downright",
    ]);
    // The cardinal binding is kept but this mode does not send it.
    expect(screen.getByText(/也设置了按键，但当前模式不会发送/)).toBeDefined();
    expect(draft().layers.base["ls.right"]).toEqual(
      layered.layers.base["ls.right"],
    );
  });

  it("edits a direction in the base key set from the grid", () => {
    render(<Harness />);
    expect(screen.queryByTestId("binding-fields")).toBeNull();
    const right = cell("right");
    expect(right.getAttribute("aria-label")).toBe("右：F18");
    fireEvent.click(cell("up"));
    expect(cell("up").getAttribute("aria-expanded")).toBe("true");
    const card = screen.getByRole("region", { name: "↑ 向上推" });
    expect(within(card).getByTestId("binding-value").textContent).toBe("{}");
    fireEvent.click(
      within(card).getByRole("button", { name: "stub: send F20" }),
    );
    expect(draft().layers.base["ls.up"]).toEqual({
      press: { type: "keys", keys: "F20" },
    });
    expect(cell("up").getAttribute("aria-label")).toBe("上：F20");
    expectOthersKept(draft());
    // Clearing removes only this direction.
    fireEvent.click(within(card).getByRole("button", { name: "清除" }));
    expect(draft().layers.base["ls.up"]).toBeUndefined();
    expect(draft().layers.base["ls.right"]).toBeDefined();
    fireEvent.click(cell("right"));
    expect(cell("up").getAttribute("aria-expanded")).toBe("false");
    expect(
      screen.getByRole("region", { name: "→ 向右推" }).querySelector("output")
        ?.textContent,
    ).toContain("F18");
    fireEvent.click(cell("right"));
    expect(screen.queryByTestId("binding-fields")).toBeNull();
  });

  it("opens the direction a drawer target names", () => {
    const { rerender } = render(<Harness control="ls.down" />);
    expect(cell("down").getAttribute("aria-expanded")).toBe("true");
    rerender(<Harness control="ls.left" />);
    expect(cell("left").getAttribute("aria-expanded")).toBe("true");
  });

  it("confirms a preset that replaces bindings and lists them first", () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    const dialog = screen.getByRole("dialog", { name: "改为方向键？" });
    const items = within(dialog).getAllByRole("listitem");
    expect(items).toHaveLength(4);
    expect(items[1].textContent).toContain("F18");
    expect(dialog.textContent).toContain("将替换已有的 1 个方向设置");
    expect(dialog.textContent).toContain("脚本也可能处理这些方向");
    expect(draft().layers.base["ls.up"]).toBeUndefined();
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const base = draft().layers.base;
    expect(
      ["ls.up", "ls.right", "ls.down", "ls.left"].map(
        (key) =>
          (base[key as "ls.up"]?.press as { keys: string } | undefined)?.keys,
      ),
    ).toEqual(["Up", "Right", "Down", "Left"]);
    expectOthersKept(draft());
  });

  it("cancels a preset without changing the draft", () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "WASD" }));
    const dialog = screen.getByRole("dialog", { name: "改为 WASD？" });
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(draft()).toEqual(layered);
  });

  it("applies a preset at once when nothing would be replaced", () => {
    const empty: Profile = { ...layered, layers: { base: {} } };
    render(<Harness initial={empty} />);
    fireEvent.click(screen.getByRole("button", { name: "WASD" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(draft().layers.base["ls.left"]?.press).toEqual({
      type: "keys",
      keys: "KeyA",
      mode: "hold",
    });
  });

  it("asks before a preset replaces a direction that is only blocked", () => {
    const blocked: Profile = {
      ...layered,
      layers: { ...layered.layers, base: { "ls.down": { block: true } } },
    };
    render(<Harness initial={blocked} />);
    expect(cell("down").getAttribute("aria-label")).toBe("下：已屏蔽");
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    const dialog = screen.getByRole("dialog", { name: "改为方向键？" });
    expect(draft().layers.base["ls.down"]).toEqual({ block: true });
    const down = within(dialog)
      .getAllByRole("listitem")
      .find((item) => item.dataset.direction === "down");
    expect(down?.textContent).toContain("已屏蔽");
    expect(dialog.textContent).toContain("将替换已有的 1 个方向设置");
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    expect(draft().layers.base["ls.down"]).toEqual({
      press: { type: "keys", keys: "Down", mode: "hold" },
    });
  });

  it("asks before a preset changes the direction mode", () => {
    const eight: Profile = {
      ...layered,
      analog: { ls: { mode: "eightWay" } },
      layers: { base: {} },
    };
    render(<Harness initial={eight} />);
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    const dialog = screen.getByRole("dialog", { name: "改为方向键？" });
    expect(dialog.textContent).toContain("方向模式将改为“4 方向”");
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    expect(draft().analog?.ls?.mode).toBe("standard");
  });

  it("highlights the directions the stick presses right now", () => {
    const { rerender } = render(
      <Harness input={{ buttons: {}, axes: { leftx: 32767, lefty: 0 } }} />,
    );
    expect(cell("right").dataset.active).toBe("true");
    expect(cell("right").getAttribute("aria-label")).toBe("右：F18，按下中");
    expect(cell("up").dataset.active).toBe("false");
    expect(screen.getByTestId("stick-readout").textContent).toBe(
      "当前 100% · 右",
    );
    rerender(
      <Harness
        input={{
          buttons: { leftstick: true },
          axes: { leftx: axis(0.7), lefty: -axis(0.7) },
        }}
      />,
    );
    expect(cell("up").dataset.active).toBe("true");
    expect(cell("right").dataset.active).toBe("true");
    expect(cell("down").dataset.active).toBe("false");
    expect(screen.getByTestId("stick-readout").textContent).toContain("右上");
    expect(
      document
        .querySelector('.analog-row[data-control="leftstick"]')
        ?.getAttribute("data-active"),
    ).toBe("true");
    // Inside the 60% dead zone nothing is pressed.
    rerender(<Harness input={{ buttons: {}, axes: { leftx: axis(0.5) } }} />);
    expect(cell("right").dataset.active).toBe("false");
    expect(screen.getByTestId("stick-readout").textContent).toBe(
      "当前 0% · 死区内",
    );
    expect(
      screen.getByRole("img", { name: /摇杆实时位置/ }).dataset.active,
    ).toBe("false");
  });

  it("follows the right stick's own axes", () => {
    render(
      <Harness
        control="rightstick"
        input={{ buttons: {}, axes: { leftx: 32767, righty: 32767 } }}
      />,
    );
    expect(cell("down").dataset.active).toBe("true");
    expect(cell("right").dataset.active).toBe("false");
    expect(document.querySelector('[data-control="rs.down"]')).not.toBeNull();
  });

  it("marks directions a script owns in the edited key set", () => {
    render(
      <Harness
        registrations={[
          registration(1, "ls.up"),
          registration(2, "ls.left", { observe: true, scope: "global" }),
          registration(3, "ls.down", { scope: "layer:tools", label: "Tools" }),
          registration(4, "leftstick", { scope: "global", label: "Click" }),
          registration(5, "ls.right", { scope: "global", label: "Pan" }),
        ]}
      />,
    );
    const owned = (direction: string) =>
      within(cell(direction)).queryByTestId("analog-script-ownership");
    expect(owned("up")?.textContent).toBe("S");
    expect(owned("right")?.textContent).toBe("S");
    expect(owned("left")).toBeNull();
    expect(owned("down")).toBeNull();
    // An unbound direction a script handles shows the script's label.
    expect(cell("up").getAttribute("aria-label")).toBe(
      "上：Brush direction，由脚本接管",
    );
    expect(cell("up").dataset.unset).toBeUndefined();
    expect(cell("left").dataset.unset).toBe("true");
    expect(
      within(
        document.querySelector(
          '.analog-row[data-control="leftstick"]',
        ) as HTMLElement,
      ).getByTestId("analog-script-ownership"),
    ).toBeDefined();
    fireEvent.click(cell("up"));
    expect(screen.getByText(/此处也由脚本处理：Brush direction/)).toBeDefined();
    expect(screen.getByText(/此处也由脚本处理/).textContent).not.toContain(
      "同时设置了按键",
    );
    fireEvent.click(cell("right"));
    expect(screen.getByText(/此处也由脚本处理：Pan/).textContent).toContain(
      "同时设置了按键",
    );
  });

  it("suggests the label a stick shows, and no example without one", () => {
    const { unmount } = render(<Harness />);
    const label = () =>
      screen.getByRole("textbox", { name: "在手柄图上显示为" });
    expect(label().getAttribute("placeholder")).toBe("方向键");
    unmount();
    render(
      <Harness
        initial={{ schemaVersion: 1, name: "A", layers: { base: {} } }}
      />,
    );
    expect(label().getAttribute("placeholder")).toBeNull();
  });

  it("edits the stick click and the label shown on the controller", () => {
    render(<Harness />);
    commit(screen.getByRole("textbox", { name: "在手柄图上显示为" }), {
      target: { value: "平移画布" },
    });
    expect(draft().layers.base.leftstick).toEqual({ label: "平移画布" });
    const click = screen.getByRole("button", { name: /按下摇杆/ });
    expect(click.textContent).toContain("未设置");
    fireEvent.click(click);
    const card = screen.getByRole("region", { name: "按下摇杆" });
    fireEvent.click(
      within(card).getByRole("button", { name: "stub: send F20" }),
    );
    expect(draft().layers.base.leftstick).toEqual({
      label: "平移画布",
      press: { type: "keys", keys: "F20" },
    });
    // The row names the card and collapses it.
    expect(within(card).queryByRole("button", { name: "清除" })).toBeNull();
    expect(click.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(click);
    expect(screen.queryByRole("region", { name: "按下摇杆" })).toBeNull();
    expect(draft().layers.alt.leftstick).toEqual({ block: true });
  });

  it("clears a direction's keys but keeps its display name", () => {
    const named: Profile = structuredClone(layered);
    named.layers.base["ls.right"] = {
      label: "Pan",
      press: { type: "keys", keys: "F18" },
    };
    render(<Harness initial={named} />);
    fireEvent.click(cell("right"));
    fireEvent.click(
      within(screen.getByRole("region", { name: "→ 向右推" })).getByRole(
        "button",
        { name: "清除" },
      ),
    );
    expect(draft().layers.base["ls.right"]).toEqual({ label: "Pan" });
    expect(cell("right").getAttribute("aria-label")).toBe("右：未设置");
  });

  it("asks before clearing a direction that switches profiles", () => {
    const switching: Profile = structuredClone(layered);
    switching.layers.base["ls.up"] = {
      press: { type: "profile", profile: "photo" },
    };
    render(<Harness initial={switching} />);
    expect(cell("up").getAttribute("aria-label")).toBe("上：切换到「photo」");
    fireEvent.click(cell("up"));
    const card = screen.getByRole("region", { name: "↑ 向上推" });
    fireEvent.click(within(card).getByRole("button", { name: "清除" }));
    const dialog = screen.getByRole("dialog", { name: "清除此设置？" });
    expect(dialog.textContent).toContain("切换配置");
    expect(dialog.textContent).not.toContain("层");
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(draft().layers.base["ls.up"]).toEqual(
      switching.layers.base["ls.up"],
    );
    fireEvent.click(within(card).getByRole("button", { name: "清除" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "清除此设置？" })).getByRole(
        "button",
        { name: "清除" },
      ),
    );
    expect(draft().layers.base["ls.up"]).toBeUndefined();
    expectOthersKept(draft());
  });

  it("names a profile switch by the library's display name when given", () => {
    const binding = { press: { type: "profile", profile: "photo" } } as const;
    expect(bindingText(binding, "zh-CN")).toBe("切换到「photo」");
    expect(
      bindingText(binding, "en", [], undefined, [
        { id: "photo", name: "Photo" },
      ]),
    ).toBe("Switch to Photo");
  });

  it("keeps the push and raw axis bindings under advanced", () => {
    render(
      <Harness
        input={{ buttons: {}, axes: { leftx: 0, lefty: -axis(0.5) } }}
      />,
    );
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    const active = document.querySelector(
      '.analog-row[data-control="ls.active"]',
    ) as HTMLElement;
    const rawUp = document.querySelector(
      '.analog-row[data-control="lefty-"]',
    ) as HTMLElement;
    expect(advanced.contains(active)).toBe(true);
    expect(advanced.contains(rawUp)).toBe(true);
    advanced.open = true;
    // Raw axes skip the dead zone: 50% passes the 30% raw threshold.
    expect(rawUp.dataset.active).toBe("true");
    expect(active.dataset.active).toBe("false");
    fireEvent.click(rawUp);
    fireEvent.click(
      within(screen.getByRole("region", { name: "↑ 原始轴向上" })).getByRole(
        "button",
        { name: "stub: send F20" },
      ),
    );
    expect(draft().layers.base["lefty-"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    commit(within(advanced).getByRole("spinbutton", { name: "原始轴阈值" }), {
      target: { value: "50" },
    });
    expect(draft().analog?.axisThreshold).toBe(0.5);
    commit(within(advanced).getByRole("spinbutton", { name: "触发幅度" }), {
      target: { value: "40" },
    });
    commit(within(advanced).getByRole("spinbutton", { name: "方向切换延迟" }), {
      target: { value: "12" },
    });
    expect(draft().analog?.ls).toMatchObject({
      directionThreshold: 0.4,
      directionDelayMs: 12,
    });
    expectOthersKept(draft());
  });

  it("opens advanced for a raw axis target", () => {
    render(<Harness control="leftx+" />);
    expect(
      (screen.getByTestId("stick-advanced") as HTMLDetailsElement).open,
    ).toBe(true);
    expect(screen.getByRole("region", { name: "→ 原始轴向右" })).toBeDefined();
  });

  it("copies parameters across sticks and resets without touching bindings", () => {
    const { rerender } = render(<Harness language="en" />);
    commit(screen.getByRole("spinbutton", { name: "Dead zone" }), {
      target: { value: "40" },
    });
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Copy parameters" }));
    expect(
      screen.getByText("Copied the parameters of the left stick"),
    ).toBeDefined();
    rerender(<Harness language="en" control="rightstick" />);
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(
      screen.getByRole("menuitem", { name: "Paste parameters…" }),
    );
    fireEvent.click(
      within(
        screen.getByRole("dialog", { name: "Paste parameters?" }),
      ).getByRole("button", { name: "Paste" }),
    );
    expect(draft().analog?.rs).toEqual({
      mode: "standard",
      deadzone: 0.4,
      saturation: 0.95,
    });
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(
      screen.getByRole("menuitem", { name: "Reset parameters…" }),
    );
    fireEvent.click(
      within(
        screen.getByRole("dialog", { name: "Reset parameters?" }),
      ).getByRole("button", { name: "Reset" }),
    );
    expect(draft().analog?.rs).toEqual({});
    expect(draft().layers).toEqual(layered.layers);
  });

  it("opens the parameter menu on right click", () => {
    render(<Harness />);
    fireEvent.contextMenu(screen.getByRole("img", { name: /摇杆实时位置/ }));
    expect(
      (screen.getByTestId("stick-advanced") as HTMLDetailsElement).open,
    ).toBe(true);
    expect(screen.getByRole("menuitem", { name: "复制参数" })).toBeDefined();
    // A second right click keeps it open instead of toggling it shut.
    fireEvent.contextMenu(screen.getByRole("img", { name: /摇杆实时位置/ }));
    expect(screen.getByRole("menu", { name: "参数操作" })).toBeDefined();
  });

  it("opens the parameter menu when the drawer asks for it", () => {
    const { rerender } = render(<Harness menuRequest={0} />);
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    expect(advanced.open).toBe(false);
    expect(screen.queryByRole("menu")).toBeNull();
    rerender(<Harness menuRequest={1} />);
    expect(advanced.open).toBe(true);
    const menu = screen.getByRole("menu", { name: "参数操作" });
    expect(
      within(menu).getByRole("menuitem", { name: "复制参数" }),
    ).toBeDefined();
    // Closed again, the same request does not reopen it; a new one does.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    advanced.open = false;
    rerender(<Harness menuRequest={1} />);
    expect(screen.queryByRole("menu")).toBeNull();
    rerender(<Harness menuRequest={2} />);
    expect(advanced.open).toBe(true);
    expect(screen.getByRole("menu", { name: "参数操作" })).toBeDefined();
  });

  it("lets a read-only stick be read but not edited", () => {
    render(<Harness disabled />);
    const isDisabled = (element: HTMLElement) => element.matches(":disabled");
    // Everything that edits the draft is disabled.
    for (const name of ["死区", "推到底"]) {
      expect(isDisabled(screen.getByRole("spinbutton", { name }))).toBe(true);
      expect(isDisabled(screen.getByRole("slider", { name }))).toBe(true);
    }
    expect(
      isDisabled(screen.getByRole("textbox", { name: "在手柄图上显示为" })),
    ).toBe(true);
    expect(isDisabled(screen.getByRole("radio", { name: "8 方向" }))).toBe(
      true,
    );
    expect(isDisabled(screen.getByRole("button", { name: "方向键" }))).toBe(
      true,
    );
    expect(isDisabled(screen.getByRole("button", { name: "WASD" }))).toBe(true);
    // Cells and rows still open, and their cards are read-only.
    expect(isDisabled(cell("right"))).toBe(false);
    fireEvent.click(cell("right"));
    const card = screen.getByRole("region", { name: "→ 向右推" });
    const fields = within(card).getByTestId("binding-fields");
    expect(fields.dataset.disabled).toBe("true");
    expect(fields.dataset.suggestPress).toBe("false");
    expect(within(card).queryByRole("button", { name: "清除" })).toBeNull();
    const click = screen.getByRole("button", { name: /按下摇杆/ });
    expect(isDisabled(click)).toBe(false);
    fireEvent.click(click);
    expect(
      within(screen.getByRole("region", { name: "按下摇杆" })).getByTestId(
        "binding-fields",
      ).dataset.suggestPress,
    ).toBe("false");
    // Advanced: the rows open, the fields are disabled.
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    advanced.open = true;
    for (const name of ["触发幅度", "角度滞回", "方向切换延迟", "原始轴阈值"])
      expect(
        isDisabled(within(advanced).getByRole("spinbutton", { name })),
      ).toBe(true);
    expect(
      isDisabled(within(advanced).getByRole("switch", { name: "只有斜向" })),
    ).toBe(true);
    const rawUp = advanced.querySelector<HTMLButtonElement>(
      '.analog-row[data-control="lefty-"]',
    ) as HTMLButtonElement;
    expect(isDisabled(rawUp)).toBe(false);
    fireEvent.click(rawUp);
    expect(screen.getByRole("region", { name: "↑ 原始轴向上" })).toBeDefined();
    // Parameters can be copied, not pasted or reset.
    fireEvent.click(screen.getByTestId("analog-params"));
    expect(isDisabled(screen.getByRole("menuitem", { name: "复制参数" }))).toBe(
      false,
    );
    expect(
      isDisabled(screen.getByRole("menuitem", { name: "粘贴参数…" })),
    ).toBe(true);
    expect(
      isDisabled(screen.getByRole("menuitem", { name: "恢复默认参数…" })),
    ).toBe(true);
    expect(draft()).toEqual(layered);
  });
});

describe("trigger settings", () => {
  it("edits press and release in percent and rejects release above press", () => {
    render(<Harness panel={TriggerSettings} control="righttrigger" />);
    const press = screen.getByRole("spinbutton", { name: "按下" });
    expect((press as HTMLInputElement).value).toBe("30");
    commit(press, { target: { value: "45" } });
    expect(draft().analog?.rt).toEqual({ press: 0.45 });
    commit(screen.getByRole("spinbutton", { name: "松开" }), {
      target: { value: "60" },
    });
    expect(draft().analog?.rt).toEqual({ press: 0.45, release: 0.6 });
    expect(screen.getByRole("alert").textContent).toContain(
      "“松开”不能大于“按下”",
    );
    commit(screen.getByRole("spinbutton", { name: "松开" }), {
      target: { value: "40" },
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expectOthersKept(draft());
  });

  it("shows live pressure against the press and release points", () => {
    const { rerender } = render(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(0.5) } }}
      />,
    );
    const meter = screen.getByRole("meter", { name: "扳机实时力度" });
    expect(meter.getAttribute("aria-valuenow")).toBe("50");
    expect(meter.dataset.active).toBe("true");
    // Between release (20%) and press (30%) a pressed trigger stays pressed.
    rerender(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(0.25) } }}
      />,
    );
    expect(screen.getByRole("meter").dataset.active).toBe("true");
    rerender(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(0.1) } }}
      />,
    );
    expect(screen.getByRole("meter").dataset.active).toBe("false");
    rerender(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(0.25) } }}
      />,
    );
    expect(screen.getByRole("meter").dataset.active).toBe("false");
  });

  it("says where the release, press and full pull marks are, in words (UI-24)", () => {
    render(<Harness panel={TriggerSettings} control="lefttrigger" />);
    const meter = screen.getByRole("meter", { name: "扳机实时力度" });
    // The marks are presentational children of the meter; a sentence says
    // what a sighted user reads off them.
    const description = document.getElementById(
      meter.getAttribute("aria-describedby") ?? "",
    );
    expect(description?.textContent).toBe("按下 30%，松开 20%，按到底 90%");
    expect(description?.classList.contains("visually-hidden")).toBe(true);
    // It is static: a new reading changes the value, not the sentence.
    expect(meter.getAttribute("aria-valuenow")).toBe("0");
  });
  it("says it in English", () => {
    render(
      <Harness panel={TriggerSettings} control="lefttrigger" language="en" />,
    );
    const meter = screen.getByRole("meter");
    expect(
      document.getElementById(meter.getAttribute("aria-describedby") ?? "")
        ?.textContent,
    ).toBe("Press 30%, release 20%, full pull 90%");
  });

  it("sets the half-press threshold in the trigger drawer", () => {
    render(<Harness panel={TriggerSettings} control="lefttrigger" />);
    const half = () =>
      document.querySelector(
        '.analog-row[data-control="lefttrigger+"]',
      ) as HTMLElement;
    expect(half().textContent).toContain("超过 30%");
    const threshold = screen.getByRole("spinbutton", { name: "原始轴阈值" });
    expect((threshold as HTMLInputElement).value).toBe("30");
    commit(threshold, { target: { value: "40" } });
    expect(draft().analog?.axisThreshold).toBe(0.4);
    expect(half().textContent).toContain("超过 40%");
    expect(draft().analog?.ls).toEqual(layered.analog?.ls);
    expect(draft().analog?.lt).toBeUndefined();
    expectOthersKept(draft());
  });

  it("reports an out-of-range half-press threshold in the trigger drawer", () => {
    render(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        initial={{ ...layered, analog: { axisThreshold: 2 } }}
      />,
    );
    expect(screen.getByRole("alert").textContent).toBe(
      "“原始轴阈值”的值超出范围。",
    );
  });

  it("edits the half press with the script that owns it", () => {
    render(
      <Harness
        panel={TriggerSettings}
        control="righttrigger"
        language="en"
        initial={{ ...layered, analog: { axisThreshold: 0.5 } }}
        registrations={[
          registration(1, "righttrigger+", {
            scope: "global",
            claim: true,
            label: "Half press",
          }),
        ]}
      />,
    );
    const half = document.querySelector(
      '.analog-row[data-control="righttrigger+"]',
    ) as HTMLElement;
    expect(half.textContent).toContain("Past 50%");
    expect(within(half).getByTestId("analog-script-ownership")).toBeDefined();
    fireEvent.click(half);
    fireEvent.click(
      within(screen.getByRole("region", { name: "Half press" })).getByRole(
        "button",
        { name: "stub: send F20" },
      ),
    );
    expect(draft().layers.base["righttrigger+"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    expectOthersKept(draft());
  });

  it("always offers the second stage, with its own points", () => {
    render(<Harness panel={TriggerSettings} control="lefttrigger" />);
    const row = document.querySelector(
      '.analog-row[data-control="lt.full"]',
    ) as HTMLElement;
    expect(row.textContent).toContain("按到底");
    expect(row.textContent).toContain("未设置");
    // Nothing about the old set-up is offered for a file without it.
    expect(screen.queryByRole("button", { name: /设置半按/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "改用按到底" })).toBeNull();
    fireEvent.click(row);
    fireEvent.click(
      within(screen.getByRole("region", { name: "按到底" })).getByRole(
        "button",
        { name: "stub: send F20" },
      ),
    );
    expect(draft().layers.base["lt.full"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    // The first stage is a different control and stays as it was.
    expect(draft().layers.base.lt).toBeUndefined();
    const press = screen.getByRole("spinbutton", { name: "按到底：按下" });
    const release = screen.getByRole("spinbutton", { name: "按到底：松开" });
    expect((press as HTMLInputElement).value).toBe("90");
    expect((release as HTMLInputElement).value).toBe("80");
    commit(press, { target: { value: "95" } });
    commit(release, { target: { value: "70" } });
    expect(draft().analog?.lt).toEqual({ fullPress: 0.95, fullRelease: 0.7 });
    // The shared half-axis threshold is not touched.
    expect(draft().analog?.axisThreshold).toBeUndefined();
    expectOthersKept(draft());
  });

  it("offers the same for the right trigger", () => {
    render(
      <Harness panel={TriggerSettings} control="righttrigger" language="en" />,
    );
    const row = document.querySelector(
      '.analog-row[data-control="rt.full"]',
    ) as HTMLElement;
    expect(row.textContent).toContain("Full pull");
    commit(screen.getByRole("spinbutton", { name: "Full pull: press" }), {
      target: { value: "85" },
    });
    expect(draft().analog?.rt).toEqual({ fullPress: 0.85 });
    expect(draft().analog?.lt).toBeUndefined();
  });

  it("opens the second stage a drawer target names", () => {
    render(<Harness panel={TriggerSettings} control="rt.full" />);
    expect(screen.getByRole("region", { name: "按到底" })).toBeDefined();
  });

  it("holds the second stage to the first, as the daemon does", () => {
    render(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        initial={{
          ...layered,
          analog: { lt: { press: 0.5, fullPress: 0.4, fullRelease: 0.6 } },
        }}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain(
      "“按到底：松开”不能大于“按到底：按下”。",
    );
    expect(alert.textContent).toContain("“按到底：按下”必须大于“按下”。");
    // They are reported next to the second stage, not the first.
    expect(
      screen
        .getByRole("spinbutton", { name: "按下" })
        .closest("fieldset")
        ?.querySelector('[role="alert"]'),
    ).toBeNull();
  });

  it("marks the second stage on the pressure bar and follows it live", () => {
    const { rerender } = render(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(0.6) } }}
      />,
    );
    const meter = screen.getByRole("meter", { name: "扳机实时力度" });
    const mark = meter.querySelector(
      '.analog-bar-mark[data-kind="full"]',
    ) as HTMLElement;
    expect(mark.style.left).toBe("90%");
    const row = () =>
      document.querySelector(
        '.analog-row[data-control="lt.full"]',
      ) as HTMLElement;
    expect(row().dataset.active).toBe("false");
    const at = (fraction: number) => (
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        input={{ buttons: {}, axes: { lefttrigger: axis(fraction) } }}
      />
    );
    rerender(at(0.95));
    expect(row().dataset.active).toBe("true");
    // Between fullRelease (80%) and fullPress (90%) it stays down.
    rerender(at(0.85));
    expect(row().dataset.active).toBe("true");
    // The first stage stays down while the second is.
    expect(screen.getByRole("meter").dataset.active).toBe("true");
    rerender(at(0.75));
    expect(row().dataset.active).toBe("false");
    rerender(at(0.85));
    expect(row().dataset.active).toBe("false");
  });

  it("moves the marks with the points", () => {
    render(
      <Harness
        panel={TriggerSettings}
        control="lefttrigger"
        initial={{ ...layered, analog: { lt: { fullPress: 0.75 } } }}
      />,
    );
    const mark = screen
      .getByRole("meter")
      .querySelector('.analog-bar-mark[data-kind="full"]') as HTMLElement;
    expect(mark.style.left).toBe("75%");
  });

  it("offers the response curve only when a repeat of the trigger sets minRateHz", () => {
    const repeat = {
      type: "keys" as const,
      keys: "Ctrl+Z",
      mode: "tap" as const,
      repeat: { rateHz: 20, minRateHz: 2 },
    };
    const { unmount } = render(
      <Harness panel={TriggerSettings} control="lefttrigger" />,
    );
    expect(screen.queryByRole("radiogroup", { name: "响应曲线" })).toBeNull();
    unmount();
    // The second stage, the first stage and the half axis all count, in any
    // key group, and only for their own trigger.
    for (const [layer, control] of [
      ["base", "lt.full"],
      ["base", "lt"],
      ["base", "lefttrigger+"],
      ["alt", "lt"],
    ] as const) {
      const initial = structuredClone(layered);
      initial.layers[layer] = {
        ...initial.layers[layer],
        [control]: { press: repeat },
      };
      const { unmount } = render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={initial}
        />,
      );
      expect(
        screen.queryByRole("radiogroup", { name: "响应曲线" }),
        `${layer} ${control}`,
      ).not.toBeNull();
      unmount();
      render(
        <Harness
          panel={TriggerSettings}
          control="righttrigger"
          initial={initial}
        />,
      );
      expect(screen.queryByRole("radiogroup", { name: "响应曲线" })).toBeNull();
      cleanup();
    }
  });

  it("sets the response curve of the trigger", () => {
    const initial = structuredClone(layered);
    initial.layers.base.rt = {
      press: {
        type: "keys",
        keys: "Ctrl+Z",
        mode: "tap",
        repeat: { rateHz: 12, minRateHz: 2 },
      },
    };
    render(
      <Harness
        panel={TriggerSettings}
        control="righttrigger"
        initial={initial}
      />,
    );
    const group = screen.getByRole("radiogroup", { name: "响应曲线" });
    expect(
      within(group)
        .getByRole("radio", { name: "线性" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    fireEvent.click(within(group).getByRole("radio", { name: "三次" }));
    expect(draft().analog?.rt).toEqual({ curve: "cubic" });
    fireEvent.click(within(group).getByRole("radio", { name: "二次" }));
    expect(draft().analog?.rt).toEqual({ curve: "quadratic" });
    expect(draft().analog?.lt).toBeUndefined();
    // The key and the curve are in the same edit as any other setting.
    expect(draft().layers.base.rt).toEqual(initial.layers.base.rt);
  });

  describe("a file with the two stages of the editor before lt.full", () => {
    // A copy all the way down: tests change the other key sets.
    const old = (): Profile =>
      structuredClone({
        ...layered,
        analog: {
          ...layered.analog,
          lt: { press: 0.9, release: 0.8 },
          axisThreshold: 0.5,
        },
        layers: {
          ...layered.layers,
          base: {
            ...layered.layers.base,
            lt: { label: "Eraser", press: { type: "keys", keys: "E" } },
            "lefttrigger+": { press: { type: "keys", keys: "B" } },
          },
        },
      });

    it("keeps working as it is until the person moves it", () => {
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={old()}
        />,
      );
      expect(draft()).toEqual(old());
      expect(
        (
          screen.getByRole("spinbutton", {
            name: "按下",
          }) as HTMLInputElement
        ).value,
      ).toBe("90");
      // Reading it raises nothing.
      expect(screen.queryByRole("alert")).toBeNull();
    });

    it("offers to move it to the second stage, behind a dialog", () => {
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={old()}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: "改用按到底" }));
      const dialog = screen.getByRole("dialog", { name: "改用按到底？" });
      expect(dialog.textContent).toContain("左扳机原来的按键改为“按到底”");
      // Nothing changes before it is confirmed, or when it is cancelled.
      expect(draft()).toEqual(old());
      fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
      expect(draft()).toEqual(old());
      fireEvent.click(screen.getByRole("button", { name: "改用按到底" }));
      fireEvent.click(
        within(screen.getByRole("dialog", { name: "改用按到底？" })).getByRole(
          "button",
          { name: "改用" },
        ),
      );
      const next = draft();
      expect(next.layers.base["lt.full"]).toEqual(old().layers.base.lt);
      expect(next.layers.base.lt).toEqual(old().layers.base["lefttrigger+"]);
      expect(next.layers.base["lefttrigger+"]).toBeUndefined();
      // Press and release are the defaults again; the shared threshold stays.
      expect(next.analog?.lt).toBeUndefined();
      expect(next.analog?.axisThreshold).toBe(0.5);
      expect(next.analog?.ls).toEqual(layered.analog?.ls);
      expectOthersKept(next);
      // The button goes away: there is nothing left to move.
      expect(screen.queryByRole("button", { name: "改用按到底" })).toBeNull();
    });

    it("does not offer it once the second stage is in use", () => {
      const used = old();
      used.layers.base["lt.full"] = { press: { type: "keys", keys: "X" } };
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={used}
        />,
      );
      expect(screen.queryByRole("button", { name: "改用按到底" })).toBeNull();
    });

    it("does not offer it on the trigger that does not have the old shape", () => {
      render(
        <Harness
          panel={TriggerSettings}
          control="righttrigger"
          language="en"
          initial={old()}
        />,
      );
      expect(
        screen.queryByRole("button", { name: "Use full pull" }),
      ).toBeNull();
    });

    it("is offered and explained in English", () => {
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          language="en"
          initial={old()}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: "Use full pull" }));
      expect(
        screen.getByRole("dialog", { name: "Use full pull?" }).textContent,
      ).toContain("the Half press keys become the keys of the left trigger");
    });

    it("moves the other key sets with the base one", () => {
      const used = old();
      used.layers.alt.lt = { press: { type: "keys", keys: "Q" } };
      used.layers.alt["lefttrigger+"] = { press: { type: "keys", keys: "C" } };
      used.common = {
        ...used.common,
        "lefttrigger+": { press: { type: "keys", keys: "G" } },
      };
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={used}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: "改用按到底" }));
      fireEvent.click(
        within(screen.getByRole("dialog", { name: "改用按到底？" })).getByRole(
          "button",
          { name: "改用" },
        ),
      );
      const next = draft();
      expect(next.layers.alt["lt.full"]).toEqual(used.layers.alt.lt);
      expect(next.layers.alt.lt).toEqual(used.layers.alt["lefttrigger+"]);
      expect(next.layers.alt["lefttrigger+"]).toBeUndefined();
      expect(next.common?.lt).toEqual(used.common["lefttrigger+"]);
      expect(next.common?.["lefttrigger+"]).toBeUndefined();
      // What the move does not touch stays.
      expect(next.layers.alt["ls.up"]).toEqual(used.layers.alt["ls.up"]);
      expect(next.common?.back).toEqual(used.common.back);
    });

    it("does not offer it when another key set uses the second stage", () => {
      const used = old();
      used.layers.alt["lt.full"] = { press: { type: "keys", keys: "X" } };
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={used}
        />,
      );
      expect(screen.queryByRole("button", { name: "改用按到底" })).toBeNull();
    });

    it("does not offer it while a script is registered on the trigger", () => {
      // Moving the keys would leave the script firing at 30 percent.
      const { unmount } = render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={old()}
          registrations={[registration(1, "lt", { scope: "layer:alt" })]}
        />,
      );
      expect(screen.queryByRole("button", { name: "改用按到底" })).toBeNull();
      unmount();
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={old()}
          registrations={[registration(2, "rt")]}
        />,
      );
      expect(screen.getByRole("button", { name: "改用按到底" })).toBeDefined();
    });

    it("cannot be moved in a read-only profile", () => {
      render(
        <Harness
          panel={TriggerSettings}
          control="lefttrigger"
          initial={old()}
          disabled
        />,
      );
      expect(
        screen.getByRole("button", { name: "改用按到底" }).matches(":disabled"),
      ).toBe(true);
    });
  });

  it("keeps a read-only trigger's second stage and half press readable", () => {
    render(<Harness panel={TriggerSettings} control="lefttrigger" disabled />);
    for (const name of [
      "按下",
      "松开",
      "按到底：按下",
      "按到底：松开",
      "原始轴阈值",
    ])
      expect(
        screen.getByRole("spinbutton", { name }).matches(":disabled"),
      ).toBe(true);
    for (const control of ["lt.full", "lefttrigger+"]) {
      const row = document.querySelector(
        `.analog-row[data-control="${control}"]`,
      ) as HTMLButtonElement;
      expect(row.matches(":disabled")).toBe(false);
      fireEvent.click(row);
      expect(screen.getByTestId("binding-fields").dataset.disabled).toBe(
        "true",
      );
      fireEvent.click(row);
    }
    fireEvent.click(screen.getByTestId("analog-params"));
    expect(
      screen.getByRole("menuitem", { name: "复制参数" }).matches(":disabled"),
    ).toBe(false);
    expect(
      screen
        .getByRole("menuitem", { name: "恢复默认参数…" })
        .matches(":disabled"),
    ).toBe(true);
  });
});

describe("d-pad settings", () => {
  it("changes the mode and the fold rule with plain labels", () => {
    render(<Harness panel={DpadSettings} control="dpad.up" />);
    const mode = screen.getByRole("radiogroup", { name: "十字键模式" });
    expect(
      within(mode)
        .getByRole("radio", { name: "独立按钮" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    expect(screen.queryByRole("radiogroup", { name: "斜按时" })).toBeNull();
    fireEvent.click(within(mode).getByRole("radio", { name: "4 方向" }));
    fireEvent.click(
      within(screen.getByRole("radiogroup", { name: "斜按时" })).getByRole(
        "radio",
        { name: "按固定规则" },
      ),
    );
    expect(draft().analog?.dpad).toEqual({
      mode: "fourWay",
      fold: "clockwise",
    });
    expect(screen.getByText(/↑→ 取 →/)).toBeDefined();
    expectOthersKept(draft());
  });

  it("binds diagonals in eight-way mode and highlights the pressed one", () => {
    render(
      <Harness
        panel={DpadSettings}
        control="dpad.left"
        input={{ buttons: { "dpad.up": true, "dpad.left": true }, axes: {} }}
      />,
    );
    expect(shownDirections()).toEqual([]);
    fireEvent.click(screen.getByRole("radio", { name: "8 方向" }));
    expect(draft().analog?.dpad).toEqual({ mode: "eightWay" });
    expect(shownDirections()).toEqual([
      "upleft",
      "upright",
      "downleft",
      "downright",
    ]);
    expect(cell("upleft").dataset.active).toBe("true");
    expect(cell("upright").dataset.active).toBe("false");
    fireEvent.click(cell("downright"));
    fireEvent.click(screen.getByRole("button", { name: "stub: send F20" }));
    expect(draft().layers.base["dpad.downright"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    fireEvent.click(screen.getByRole("radio", { name: "独立按钮" }));
    expect(shownDirections()).toEqual([]);
    expectOthersKept(draft());
  });

  it("sets the four directions to a preset after confirming", () => {
    const fourWay: Profile = {
      ...layered,
      analog: {
        ...layered.analog,
        dpad: { mode: "fourWay", fold: "clockwise" },
      },
      layers: {
        ...layered.layers,
        base: {
          ...layered.layers.base,
          "dpad.up": { label: "Zoom", press: { type: "keys", keys: "F21" } },
        },
      },
    };
    render(
      <Harness panel={DpadSettings} control="dpad.up" initial={fourWay} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    const dialog = screen.getByRole("dialog", { name: "改为方向键？" });
    expect(dialog.textContent).toContain("将替换已有的 1 个方向设置");
    expect(dialog.textContent).toContain("十字键模式将改为“独立按钮”");
    expect(draft()).toEqual(fourWay);
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    const next = draft();
    expect(next.analog?.dpad).toEqual({ mode: "buttons", fold: "clockwise" });
    expect(next.analog?.ls).toEqual(layered.analog?.ls);
    expect(
      (["dpad.up", "dpad.right", "dpad.down", "dpad.left"] as const).map(
        (key) => next.layers.base[key]?.press,
      ),
    ).toEqual(
      ["Up", "Right", "Down", "Left"].map((keys) => ({
        type: "keys",
        keys,
        mode: "hold",
      })),
    );
    expect(next.layers.base["dpad.up"]?.label).toBe("Zoom");
    expectOthersKept(next);
  });

  it("applies a d-pad preset at once when nothing would be replaced", () => {
    render(<Harness panel={DpadSettings} control="dpad.left" language="en" />);
    fireEvent.click(screen.getByRole("button", { name: "WASD" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(draft().analog?.dpad).toEqual({ mode: "buttons" });
    expect(draft().layers.base["dpad.left"]?.press).toEqual({
      type: "keys",
      keys: "KeyA",
      mode: "hold",
    });
  });

  it("copies, resets and pastes only d-pad parameters", () => {
    const { unmount } = render(<Harness />);
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "复制参数" }));
    unmount();
    const custom: Profile = {
      ...layered,
      analog: { dpad: { mode: "fourWay", fold: "clockwise" } },
    };
    render(<Harness panel={DpadSettings} control="dpad.up" initial={custom} />);
    fireEvent.click(screen.getByTestId("analog-params"));
    // Stick parameters do not paste onto the d-pad.
    expect(
      screen.getByRole("menuitem", { name: "粘贴参数…" }).matches(":disabled"),
    ).toBe(true);
    fireEvent.click(screen.getByRole("menuitem", { name: "复制参数" }));
    expect(screen.getByText("已复制十字键的参数")).toBeDefined();
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "恢复默认参数…" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "恢复默认参数？" })).getByRole(
        "button",
        { name: "恢复默认" },
      ),
    );
    expect(draft().analog?.dpad).toEqual({});
    expect(
      within(screen.getByRole("radiogroup", { name: "十字键模式" }))
        .getByRole("radio", { name: "独立按钮" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "粘贴参数…" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "粘贴参数？" })).getByRole(
        "button",
        { name: "粘贴" },
      ),
    );
    expect(draft().analog?.dpad).toEqual({
      mode: "fourWay",
      fold: "clockwise",
    });
    expect(draft().layers).toEqual(layered.layers);
  });

  it("keeps a read-only d-pad's diagonals readable", () => {
    render(
      <Harness
        panel={DpadSettings}
        control="dpad.up"
        disabled
        initial={{ ...layered, analog: { dpad: { mode: "eightWay" } } }}
      />,
    );
    expect(
      screen.getByRole("radio", { name: "独立按钮" }).matches(":disabled"),
    ).toBe(true);
    expect(
      screen.getByRole("button", { name: "方向键" }).matches(":disabled"),
    ).toBe(true);
    expect(cell("upleft").matches(":disabled")).toBe(false);
    fireEvent.click(cell("upleft"));
    const card = screen.getByRole("region", { name: "↖ 左上斜按" });
    expect(within(card).getByTestId("binding-fields").dataset.disabled).toBe(
      "true",
    );
    expect(within(card).queryByRole("button", { name: "清除" })).toBeNull();
  });
});

describe("wording", () => {
  it("never mentions key groups or layers in any panel", async () => {
    for (const language of ["zh-CN", "en"] as const) {
      for (const [panel, control] of [
        [StickPanel, "leftstick"],
        [TriggerSettings, "lefttrigger"],
        [DpadSettings, "dpad.up"],
      ] as const) {
        const { container, unmount } = render(
          <Harness panel={panel} control={control} language={language} />,
        );
        for (const details of container.querySelectorAll("details"))
          details.open = true;
        await act(async () => {});
        const text =
          container.querySelector(".analog-panel")?.textContent ?? "";
        expect(text.length).toBeGreaterThan(20);
        if (language === "en") expect(text).not.toMatch(/layer/i);
        else expect(text).not.toContain("层");
        unmount();
      }
    }
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    expect(screen.getByRole("dialog").textContent).not.toContain("层");
    cleanup();
    render(
      <Harness
        panel={DpadSettings}
        control="dpad.up"
        initial={{ ...layered, analog: { dpad: { mode: "eightWay" } } }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "方向键" }));
    expect(screen.getByRole("dialog").textContent).not.toContain("层");
  });
});

describe("scripts in the analog panels (SCR-50)", () => {
  it("names a script, or says its file is gone", () => {
    const binding = { press: { type: "macro" as const, file: "复制全部.ts" } };
    const { unmount } = render(
      <BindingSummary binding={binding} language="zh-CN" />,
    );
    expect(document.querySelector(".analog-summary-macro")?.textContent).toBe(
      "复制全部",
    );
    unmount();
    render(
      <MacroContext
        value={{
          list: { macros: [], missing: [{ file: "复制全部.ts", usedBy: [] }] },
          failing: new Set(),
        }}
      >
        <BindingSummary binding={binding} language="zh-CN" />
      </MacroContext>,
    );
    const missing = document.querySelector(".analog-summary-macro");
    expect(missing?.textContent).toBe("找不到「复制全部」");
    expect(missing?.hasAttribute("data-missing")).toBe(true);
    // Labels use the name.
    expect(bindingText(binding, "zh-CN")).toBe("复制全部");
  });
});

describe("stick panel: knob, rings and curve (UI-28)", () => {
  // No dead zone or push settings of its own: the defaults, 15% and 95%.
  const bare: Profile = { ...layered, analog: {} };
  const knobFile = (stick: "ls" | "rs" = "ls"): Profile => ({
    ...layered,
    analog: { [stick]: { mode: "knob" } },
    layers: {
      ...layered.layers,
      base: {
        ...layered.layers.base,
        [`${stick}.cw`]: { press: { type: "keys", keys: "]" } },
        [`${stick}.ccw`]: { press: { type: "keys", keys: "[" } },
      },
    },
  });
  const row = (control: string) => {
    const found = document.querySelector<HTMLButtonElement>(
      `.analog-row[data-control="${control}"]`,
    );
    if (!found) throw new Error(`no ${control} row`);
    return found;
  };
  const openAdvanced = () => {
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    advanced.open = true;
    return advanced;
  };
  const modeRadio = (name: string) =>
    within(screen.getByRole("radiogroup", { name: "方向模式" })).getByRole(
      "radio",
      { name },
    );

  it("always offers the knob in the mode select", () => {
    render(<Harness initial={bare} />);
    expect(modeRadio("旋钮").getAttribute("aria-checked")).toBe("false");
    fireEvent.click(modeRadio("旋钮"));
    expect(draft().analog?.ls).toEqual({ mode: "knob" });
    expect(modeRadio("旋钮").getAttribute("aria-checked")).toBe("true");
    expect(
      screen.getByText(/当作旋钮：每转过设定的角度发一次顺时针或逆时针按键/),
    ).toBeDefined();
    expectOthersKept(draft());
  });

  it("replaces the directions with the step and the two ticks", () => {
    render(<Harness initial={knobFile()} />);
    expect(shownDirections()).toEqual([]);
    expect(document.querySelector(".analog-grid")).toBeNull();
    expect(screen.queryByRole("button", { name: "WASD" })).toBeNull();
    expect(screen.queryByRole("button", { name: "方向键" })).toBeNull();
    const step = screen.getByRole("spinbutton", { name: "每格角度" });
    expect((step as HTMLInputElement).value).toBe("30");
    expect(row("ls.cw").textContent).toContain("顺时针一格");
    expect(row("ls.ccw").textContent).toContain("逆时针一格");
    // Their keys show as on the directions.
    expect(row("ls.cw").textContent).toContain("]");
    expect(row("ls.ccw").textContent).toContain("[");
    commit(step, { target: { value: "45" } });
    expect(draft().analog?.ls).toEqual({ mode: "knob", knobStepDeg: 45 });
    commit(step, { target: { value: "10" } });
    expect(draft().analog?.ls?.knobStepDeg).toBe(10);
    commit(step, { target: { value: "180" } });
    expect(draft().analog?.ls?.knobStepDeg).toBe(180);
  });

  it("has a 刻度震动 switch in the knob section, off by default, that writes knobTick and takes it out again (HUD-10)", () => {
    render(<Harness initial={knobFile()} />);
    const tick = screen.getByRole("switch", { name: "刻度震动" });
    // In the knob section, beside the step, with no hint line.
    expect(tick.closest(".analog-fieldset")).toBe(
      screen
        .getByRole("spinbutton", { name: "每格角度" })
        .closest(".analog-fieldset"),
    );
    expect(tick.closest(".analog-setting")?.textContent).toBe("刻度震动");
    expect(tick.getAttribute("aria-checked")).toBe("false");
    expect(draft().analog?.ls).toEqual({ mode: "knob" });
    fireEvent.click(tick);
    expect(draft().analog?.ls).toEqual({ mode: "knob", knobTick: true });
    expect(tick.getAttribute("aria-checked")).toBe("true");
    // The step it sits beside is not touched by it, nor it by the step.
    commit(screen.getByRole("spinbutton", { name: "每格角度" }), {
      target: { value: "45" },
    });
    expect(draft().analog?.ls).toEqual({
      mode: "knob",
      knobStepDeg: 45,
      knobTick: true,
    });
    fireEvent.click(tick);
    expect(draft().analog?.ls).toEqual({ mode: "knob", knobStepDeg: 45 });
    expect(draft().analog?.ls).not.toHaveProperty("knobTick");
    expectOthersKept(draft());
  });

  it("reads knobTick of the file, for the stick it is opened for only", () => {
    const file: Profile = {
      ...knobFile("rs"),
      analog: { ls: { mode: "knob" }, rs: { mode: "knob", knobTick: true } },
    };
    const { unmount } = render(<Harness initial={file} control="rightstick" />);
    expect(
      screen
        .getByRole("switch", { name: "刻度震动" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    unmount();
    render(<Harness initial={file} control="leftstick" />);
    expect(
      screen
        .getByRole("switch", { name: "刻度震动" })
        .getAttribute("aria-checked"),
    ).toBe("false");
  });

  it("is only in the knob section, and the value stays when the mode changes", () => {
    render(<Harness initial={bare} />);
    expect(screen.queryByRole("switch", { name: "刻度震动" })).toBeNull();
    cleanup();
    render(
      <Harness
        initial={{
          ...knobFile(),
          analog: { ls: { mode: "knob", knobTick: true } },
        }}
      />,
    );
    fireEvent.click(modeRadio("4 方向"));
    // Like the step: the file keeps it, the knob section is gone.
    expect(draft().analog?.ls).toEqual({ mode: "standard", knobTick: true });
    expect(screen.queryByRole("switch", { name: "刻度震动" })).toBeNull();
    fireEvent.click(modeRadio("旋钮"));
    expect(
      screen
        .getByRole("switch", { name: "刻度震动" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("is named in English and locked with the rest of the panel", () => {
    render(<Harness initial={knobFile()} language="en" disabled />);
    const tick = screen.getByRole("switch", { name: "Tick haptic" });
    expect(tick.closest("fieldset")?.disabled).toBe(true);
  });

  it("refuses a step outside 10 to 180 degrees or that is not whole", () => {
    render(<Harness initial={knobFile()} />);
    const step = screen.getByRole("spinbutton", { name: "每格角度" });
    for (const text of ["9", "181", "30.5", "abc"]) {
      commit(step, { target: { value: text } });
      expect(step.getAttribute("aria-invalid"), text).toBe("true");
      expect(screen.getByRole("alert").textContent).toBe(
        "请输入 10–180 的整数",
      );
      expect(draft().analog?.ls?.knobStepDeg, text).toBeUndefined();
    }
    // The saved value comes back with Escape, and the English wording.
    cleanup();
    render(<Harness initial={knobFile()} language="en" />);
    commit(screen.getByRole("spinbutton", { name: "Step" }), {
      target: { value: "5" },
    });
    expect(screen.getByRole("alert").textContent).toBe(
      "Enter a whole number from 10 to 180",
    );
  });

  it("writes the ticks of the stick it is opened for", () => {
    render(<Harness initial={knobFile("rs")} control="rightstick" />);
    expect(row("rs.cw")).toBeDefined();
    fireEvent.click(row("rs.cw"));
    fireEvent.click(
      within(screen.getByRole("region", { name: "顺时针一格" })).getByRole(
        "button",
        { name: "stub: send F20" },
      ),
    );
    expect(draft().layers.base["rs.cw"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    // One card is open at a time.
    fireEvent.click(row("rs.ccw"));
    expect(screen.queryByRole("region", { name: "顺时针一格" })).toBeNull();
    fireEvent.click(
      within(screen.getByRole("region", { name: "逆时针一格" })).getByRole(
        "button",
        { name: "stub: send F20" },
      ),
    );
    expect(draft().layers.base["rs.ccw"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    // Nothing on the left stick, and the rest of the file is as it was.
    expect(draft().layers.base["ls.cw"]).toBeUndefined();
    expectOthersKept(draft());
  });

  it("hides the settings that are only for directions", () => {
    const { unmount } = render(<Harness initial={knobFile()} />);
    const advanced = openAdvanced();
    for (const name of ["角度滞回", "方向切换延迟", "斜向扇区宽度"])
      expect(
        within(advanced).queryByRole("spinbutton", { name }),
        name,
      ).toBeNull();
    expect(
      within(advanced).queryByRole("switch", { name: "只有斜向" }),
    ).toBeNull();
    // What a knob still uses stays: the trigger point, the rings, raw axes.
    expect(
      within(advanced).getByRole("spinbutton", { name: "触发幅度" }),
    ).toBeDefined();
    expect(row("ls.active")).toBeDefined();
    // A knob has no directions to fire together with.
    expect(row("ls.active").textContent).not.toContain("和方向同时触发");
    expect(row("ls.outer")).toBeDefined();
    expect(row("lefty-")).toBeDefined();
    unmount();
    // And a stick with directions has them.
    render(<Harness initial={bare} />);
    const standard = openAdvanced();
    expect(row("ls.active").textContent).toContain("和方向同时触发");
    for (const name of ["角度滞回", "方向切换延迟"])
      expect(
        within(standard).getByRole("spinbutton", { name }),
        name,
      ).toBeDefined();
    expect(
      within(standard).getByRole("switch", { name: "只有斜向" }),
    ).toBeDefined();
  });

  it("says which direction keys the knob leaves unused", () => {
    const file = knobFile();
    file.layers.base["ls.up"] = { press: { type: "keys", keys: "Up" } };
    file.layers.base["ls.upright"] = { press: { type: "keys", keys: "Q" } };
    render(<Harness initial={file} />);
    expect(
      screen.getByText("↑ → ↗ 也设置了按键，但旋钮不会发送。"),
    ).toBeDefined();
    cleanup();
    const clean = knobFile();
    delete clean.layers.base["ls.right"];
    render(<Harness initial={clean} />);
    expect(screen.queryByText(/也设置了按键/)).toBeNull();
  });

  it("keeps the ticks in the file when the knob is left, and says they are unused", () => {
    render(<Harness initial={knobFile()} />);
    fireEvent.click(row("ls.cw"));
    expect(screen.getByRole("region", { name: "顺时针一格" })).toBeDefined();
    fireEvent.click(modeRadio("4 方向"));
    expect(draft().analog?.ls?.mode).toBe("standard");
    // The ticks stay in the file, inert; their card closes with their rows.
    expect(draft().layers.base["ls.cw"]).toEqual(
      knobFile().layers.base["ls.cw"],
    );
    expect(draft().layers.base["ls.ccw"]).toEqual(
      knobFile().layers.base["ls.ccw"],
    );
    expect(screen.queryByRole("region", { name: "顺时针一格" })).toBeNull();
    expect(
      document.querySelector('.analog-row[data-control="ls.cw"]'),
    ).toBeNull();
    expect(shownDirections()).toEqual(["up", "right", "down", "left"]);
    expect(
      screen.getByText("旋钮的按键也设置了，但当前模式不会发送。"),
    ).toBeDefined();
    // Back to the knob: they are there again.
    fireEvent.click(modeRadio("旋钮"));
    expect(row("ls.cw").textContent).toContain("]");
    expect(screen.queryByText(/旋钮的按键也设置了/)).toBeNull();
  });

  it("shows the angle of a knob and no direction", () => {
    render(
      <Harness
        initial={knobFile()}
        input={{ buttons: {}, axes: { leftx: axis(0.71), lefty: 0 } }}
      />,
    );
    // 70% of the way to full push, due right.
    expect(screen.getByTestId("stick-readout").textContent).toBe(
      "当前 70% · 90°",
    );
    const visual = document.querySelector(".analog-visual") as SVGElement;
    expect(visual.dataset.active).toBe("true");
    expect(visual.querySelector(".analog-v-sector")).toBeNull();
    expect(visual.querySelector(".analog-v-tick")).toBeNull();
    expect(visual.getAttribute("aria-label")).toBe("摇杆实时位置：90°，70%");
    cleanup();
    // The same position on a stick with directions names the direction.
    render(
      <Harness
        initial={bare}
        input={{ buttons: {}, axes: { leftx: axis(0.71), lefty: 0 } }}
      />,
    );
    expect(screen.getByTestId("stick-readout").textContent).toBe(
      "当前 70% · 右",
    );
  });

  it("has the knob angle only while the stick is pushed", () => {
    render(
      <Harness
        initial={knobFile()}
        input={{ buttons: {}, axes: { leftx: axis(0.2), lefty: 0 } }}
      />,
    );
    expect(screen.getByTestId("stick-readout").textContent).toContain(
      "未到触发幅度",
    );
    cleanup();
    render(<Harness initial={knobFile()} />);
    expect(screen.getByTestId("stick-readout").textContent).toContain("居中");
  });

  describe("rings", () => {
    it("binds the light and the full push under the other inputs", () => {
      render(<Harness initial={bare} />);
      const advanced = openAdvanced();
      expect(advanced.contains(row("ls.inner"))).toBe(true);
      expect(advanced.contains(row("ls.outer"))).toBe(true);
      expect(row("ls.inner").textContent).toContain("轻推");
      expect(row("ls.outer").textContent).toContain("重推");
      fireEvent.click(row("ls.inner"));
      fireEvent.click(
        within(screen.getByRole("region", { name: "轻推" })).getByRole(
          "button",
          { name: "stub: send F20" },
        ),
      );
      expect(draft().layers.base["ls.inner"]?.press).toEqual({
        type: "keys",
        keys: "F20",
      });
      fireEvent.click(row("ls.outer"));
      expect(screen.queryByRole("region", { name: "轻推" })).toBeNull();
      fireEvent.click(
        within(screen.getByRole("region", { name: "重推" })).getByRole(
          "button",
          { name: "stub: send F20" },
        ),
      );
      expect(draft().layers.base["ls.outer"]?.press).toEqual({
        type: "keys",
        keys: "F20",
      });
      expect(draft().layers.base["rs.outer"]).toBeUndefined();
      expectOthersKept(draft());
    });

    it("is there in the knob mode and the other modes", () => {
      for (const mode of ["standard", "eightWay", "fourWay", "knob"] as const) {
        const { unmount } = render(
          <Harness initial={{ ...bare, analog: { ls: { mode } } }} />,
        );
        expect(row("ls.inner"), mode).toBeDefined();
        expect(row("ls.outer"), mode).toBeDefined();
        unmount();
      }
    });

    it("sets where the full push starts, in percent", () => {
      render(<Harness initial={bare} />);
      const field = screen.getByRole("spinbutton", { name: "重推的位置" });
      expect((field as HTMLInputElement).value).toBe("90");
      commit(field, { target: { value: "80" } });
      expect(draft().analog?.ls).toEqual({ outerThreshold: 0.8 });
      commit(field, { target: { value: "0" } });
      expect(draft().analog?.ls?.outerThreshold).toBe(0);
      commit(field, { target: { value: "120" } });
      expect(field.getAttribute("aria-invalid")).toBe("true");
      expect(draft().analog?.ls?.outerThreshold).toBe(0);
      expect(draft().analog?.rs).toBeUndefined();
    });

    it("follows the right stick's own settings and bindings", () => {
      render(
        <Harness
          initial={{ ...bare, analog: { ls: { outerThreshold: 0.5 } } }}
          control="rightstick"
        />,
      );
      expect(
        (
          screen.getByRole("spinbutton", {
            name: "重推的位置",
          }) as HTMLInputElement
        ).value,
      ).toBe("90");
      expect(row("rs.outer")).toBeDefined();
      expect(
        document.querySelector('.analog-row[data-control="ls.outer"]'),
      ).toBeNull();
    });

    it("shows which ring the stick is in right now", () => {
      const at = (depth: number) => (
        <Harness
          initial={bare}
          input={{
            buttons: {},
            axes: { leftx: 0, lefty: -axis(0.15 + 0.8 * depth) },
          }}
        />
      );
      const { rerender } = render(at(0.1));
      openAdvanced();
      expect(row("ls.inner").dataset.active).toBe("false");
      expect(row("ls.outer").dataset.active).toBe("false");
      rerender(at(0.5));
      expect(row("ls.inner").dataset.active).toBe("true");
      expect(row("ls.outer").dataset.active).toBe("false");
      expect(row("ls.active").dataset.active).toBe("true");
      rerender(at(0.95));
      expect(row("ls.inner").dataset.active).toBe("false");
      expect(row("ls.outer").dataset.active).toBe("true");
      expect(
        (document.querySelector(".analog-visual") as SVGElement).dataset.ring,
      ).toBe("outer");
    });

    it("draws where the outer ring starts", () => {
      const circle = () =>
        document.querySelector(".analog-v-outer") as SVGCircleElement;
      const { unmount } = render(<Harness initial={bare} />);
      // 15% dead zone plus 90% of the way to 95%, in a ring of radius 62.
      expect(Number(circle().getAttribute("r"))).toBeCloseTo(
        (0.15 + 0.9 * 0.8) * 62,
        6,
      );
      unmount();
      render(
        <Harness
          initial={{ ...bare, analog: { ls: { outerThreshold: 0.5 } } }}
        />,
      );
      expect(Number(circle().getAttribute("r"))).toBeCloseTo(
        (0.15 + 0.5 * 0.8) * 62,
        6,
      );
    });

    it("keeps the full push of the sensitivity apart from the rings", () => {
      // 推到底 is the sensitivity (saturation) only; the ring pair is
      // 轻推 and 重推, and the chips and diagnostics use the same words.
      for (const [language, names] of [
        ["zh-CN", ["轻推", "重推", "重推的位置", "推到底"]],
        ["en", ["Light push", "Hard push", "Hard push from", "Full push"]],
      ] as const) {
        const { unmount } = render(
          <Harness initial={bare} language={language} />,
        );
        const advanced = openAdvanced();
        expect(row("ls.inner").textContent).toContain(names[0]);
        expect(row("ls.outer").textContent).toContain(names[1]);
        expect(row("ls.outer").textContent).not.toContain(names[3]);
        expect(
          within(advanced).getByRole("spinbutton", { name: names[2] }),
        ).toBeDefined();
        expect(
          screen.getAllByRole("spinbutton", { name: names[3] }),
        ).toHaveLength(1);
        unmount();
      }
    });

    it("opens the advanced section for a ring a drawer targets", () => {
      render(<Harness initial={bare} control="ls.outer" />);
      expect(
        (screen.getByTestId("stick-advanced") as HTMLDetailsElement).open,
      ).toBe(true);
      expect(screen.getByRole("region", { name: "重推" })).toBeDefined();
    });

    it("reads the rings but does not edit them in a read-only profile", () => {
      render(<Harness initial={knobFile()} disabled />);
      openAdvanced();
      for (const name of ["每格角度", "重推的位置"])
        expect(
          screen.getByRole("spinbutton", { name }).matches(":disabled"),
          name,
        ).toBe(true);
      expect(row("ls.outer").matches(":disabled")).toBe(false);
      fireEvent.click(row("ls.outer"));
      expect(screen.getByTestId("binding-fields").dataset.disabled).toBe(
        "true",
      );
      expect(row("ls.cw").matches(":disabled")).toBe(false);
    });
  });

  describe("response curve", () => {
    const rate = (minRateHz?: number) => ({
      press: {
        type: "keys" as const,
        keys: "Ctrl+=",
        mode: "tap" as const,
        repeat: { rateHz: 20, ...(minRateHz ? { minRateHz } : {}) },
      },
    });
    const withBinding = (
      layer: string,
      control: string,
      minRateHz?: number,
    ) => {
      const file = structuredClone(bare);
      file.layers[layer] = {
        ...file.layers[layer],
        [control]: rate(minRateHz),
      };
      return file;
    };
    const curve = () => screen.queryByRole("radiogroup", { name: "响应曲线" });

    it("is hidden while no repeat of the stick follows the push depth", () => {
      render(<Harness initial={bare} />);
      openAdvanced();
      expect(curve()).toBeNull();
      cleanup();
      // A repeat with a fixed rate does not use it.
      render(<Harness initial={withBinding("base", "ls.up")} />);
      openAdvanced();
      expect(curve()).toBeNull();
    });

    it("shows for any control of the stick in any key group", () => {
      for (const [layer, control] of [
        ["base", "ls.up"],
        ["base", "ls.downleft"],
        ["base", "ls.active"],
        ["base", "ls.outer"],
        ["base", "leftx+"],
        ["alt", "ls.left"],
      ] as const) {
        const { unmount } = render(
          <Harness initial={withBinding(layer, control, 3)} />,
        );
        openAdvanced();
        expect(curve(), `${layer} ${control}`).not.toBeNull();
        unmount();
      }
    });

    it("is of the stick the repeat belongs to", () => {
      const file = withBinding("base", "rs.up", 3);
      render(<Harness initial={file} />);
      openAdvanced();
      expect(curve()).toBeNull();
      cleanup();
      render(<Harness initial={file} control="rightstick" />);
      openAdvanced();
      expect(curve()).not.toBeNull();
    });

    it("ignores a tick, which has no depth", () => {
      render(<Harness initial={withBinding("base", "ls.cw", 3)} />);
      openAdvanced();
      expect(curve()).toBeNull();
    });

    it("sets the curve of this stick only", () => {
      render(<Harness initial={withBinding("base", "ls.up", 3)} />);
      openAdvanced();
      const group = curve() as HTMLElement;
      expect(
        within(group)
          .getByRole("radio", { name: "线性" })
          .getAttribute("aria-checked"),
      ).toBe("true");
      fireEvent.click(within(group).getByRole("radio", { name: "二次" }));
      expect(draft().analog?.ls).toEqual({ curve: "quadratic" });
      fireEvent.click(within(group).getByRole("radio", { name: "三次" }));
      expect(draft().analog?.ls?.curve).toBe("cubic");
      fireEvent.click(within(group).getByRole("radio", { name: "线性" }));
      expect(draft().analog?.ls?.curve).toBe("linear");
      expect(draft().analog?.rs).toBeUndefined();
      // The key is as it was.
      expect(draft().layers.base["ls.up"]).toEqual(rate(3));
    });

    it("has English labels", () => {
      render(
        <Harness initial={withBinding("base", "ls.up", 3)} language="en" />,
      );
      openAdvanced();
      const group = screen.getByRole("radiogroup", { name: "Response curve" });
      for (const name of ["Linear", "Quadratic", "Cubic"])
        expect(within(group).getByRole("radio", { name })).toBeDefined();
    });
  });

  it("never mentions key groups or layers in a knob's panel", async () => {
    for (const language of ["zh-CN", "en"] as const) {
      const { container, unmount } = render(
        <Harness initial={knobFile()} language={language} />,
      );
      for (const details of container.querySelectorAll("details"))
        details.open = true;
      await act(async () => {});
      const text = container.querySelector(".analog-panel")?.textContent ?? "";
      expect(text.length).toBeGreaterThan(20);
      if (language === "en") expect(text).not.toMatch(/layer/i);
      else expect(text).not.toContain("层");
      unmount();
    }
  });
});

describe("stick panel: pointer mode (MOU-01, MOU-04, MOU-06, UI-33)", () => {
  const mouseFile = (
    stick: "ls" | "rs" = "ls",
    settings: Record<string, unknown> = {},
  ): Profile => ({
    // A copy, since some tests add keys to it.
    ...structuredClone(layered),
    analog: { [stick]: { mode: "mouse", ...settings } },
  });
  const modeRadio = (name: string) =>
    within(screen.getByRole("radiogroup", { name: "方向模式" })).getByRole(
      "radio",
      { name },
    );
  const speed = () =>
    screen.getByRole("spinbutton", { name: "速度" }) as HTMLInputElement;
  const speedSlider = () =>
    screen.getByRole("slider", { name: "速度" }) as HTMLInputElement;
  const smoothing = () =>
    screen.getByRole("spinbutton", { name: "平滑" }) as HTMLInputElement;
  const curveGroup = () => screen.getByRole("radiogroup", { name: "响应曲线" });
  const openAdvanced = () => {
    const advanced = screen.getByTestId("stick-advanced") as HTMLDetailsElement;
    advanced.open = true;
    return advanced;
  };
  const row = (control: string) => {
    const found = document.querySelector<HTMLButtonElement>(
      `.analog-row[data-control="${control}"]`,
    );
    if (!found) throw new Error(`no ${control} row`);
    return found;
  };

  it("is a direction mode of its own, with the speed, curve and smoothing at their defaults", () => {
    render(<Harness initial={{ ...layered, analog: {} }} />);
    expect(modeRadio("鼠标").getAttribute("aria-checked")).toBe("false");
    expect(screen.queryByRole("spinbutton", { name: "速度" })).toBeNull();
    fireEvent.click(modeRadio("鼠标"));
    expect(draft().analog?.ls).toEqual({ mode: "mouse" });
    expect(modeRadio("鼠标").getAttribute("aria-checked")).toBe("true");
    expect(speed().value).toBe("1200");
    expect(speedSlider().value).toBe("1200");
    expect(smoothing().value).toBe("0");
    expect(
      within(curveGroup())
        .getByRole("radio", { name: "线性" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    // The mode says nothing more than its name and its settings.
    expect(document.querySelector(".analog-mode + .analog-caption")).toBeNull();
    expectOthersKept(draft());
    // The other modes are one click away.
    fireEvent.click(modeRadio("4 方向"));
    expect(draft().analog?.ls).toEqual({ mode: "standard" });
    expect(screen.queryByRole("spinbutton", { name: "速度" })).toBeNull();
  });

  it("replaces the directions and the knob step with the pointer settings", () => {
    render(<Harness initial={mouseFile()} />);
    expect(document.querySelector(".analog-grid")).toBeNull();
    expect(shownDirections()).toEqual([]);
    for (const name of ["方向键", "WASD"])
      expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.queryByRole("spinbutton", { name: "每格角度" })).toBeNull();
    expect(
      document.querySelector('.analog-row[data-control="ls.cw"]'),
    ).toBeNull();
    // The stick click keeps its row.
    expect(screen.getByRole("button", { name: /按下摇杆/ })).toBeDefined();
  });

  it("sets the speed in px/s from the field, from 50 to 10000", () => {
    render(<Harness initial={mouseFile()} />);
    commit(speed(), { target: { value: "2400" } });
    expect(draft().analog?.ls).toEqual({ mode: "mouse", mouseSpeed: 2400 });
    commit(speed(), { target: { value: "50" } });
    expect(draft().analog?.ls?.mouseSpeed).toBe(50);
    commit(speed(), { target: { value: "10000" } });
    expect(draft().analog?.ls?.mouseSpeed).toBe(10000);
    expect(speed().value).toBe("10000");
    expect(screen.queryByRole("alert")).toBeNull();
    expectOthersKept(draft());
  });

  it("refuses a speed outside 50 to 10000 or that is not whole", () => {
    render(<Harness initial={mouseFile()} />);
    for (const text of ["49", "10001", "1200.5", "abc", ""]) {
      commit(speed(), { target: { value: text } });
      expect(speed().getAttribute("aria-invalid"), text).toBe("true");
      expect(screen.getByRole("alert").textContent, text).toBe(
        "请输入 50–10000 的整数",
      );
      expect(draft().analog?.ls?.mouseSpeed, text).toBeUndefined();
    }
  });

  it("moves the speed with a slider that stops at 4000 while the field reaches 10000", () => {
    render(<Harness initial={mouseFile()} />);
    const slider = speedSlider();
    expect(slider.min).toBe("50");
    expect(slider.max).toBe("4000");
    expect(slider.step).toBe("10");
    expect(speed().min).toBe("50");
    expect(speed().max).toBe("10000");
    expect(screen.getByText("px/s")).toBeDefined();
    fireEvent.change(slider, { target: { value: "2000" } });
    expect(draft().analog?.ls).toEqual({ mode: "mouse", mouseSpeed: 2000 });
    expect(speed().value).toBe("2000");
    // A speed beyond the slider keeps it at its end.
    commit(speed(), { target: { value: "6000" } });
    expect(draft().analog?.ls?.mouseSpeed).toBe(6000);
    expect(speedSlider().value).toBe("4000");
    expect(speed().value).toBe("6000");
  });

  it("shows the response curve of the pointer at once, and sets it for this stick only", () => {
    render(<Harness initial={mouseFile()} />);
    // No repeat follows the push depth, and the curve is there all the same.
    const group = curveGroup();
    for (const name of ["线性", "二次", "三次"])
      expect(within(group).getByRole("radio", { name })).toBeDefined();
    fireEvent.click(within(group).getByRole("radio", { name: "二次" }));
    expect(draft().analog?.ls).toEqual({ mode: "mouse", curve: "quadratic" });
    fireEvent.click(within(group).getByRole("radio", { name: "三次" }));
    expect(draft().analog?.ls?.curve).toBe("cubic");
    expect(draft().analog?.rs).toBeUndefined();
  });

  it("has the curve once, even when a repeat follows the push depth", () => {
    const file = mouseFile();
    file.layers.base["ls.active"] = {
      press: {
        type: "keys",
        keys: "Ctrl+=",
        mode: "tap",
        repeat: { rateHz: 20, minRateHz: 3 },
      },
    };
    render(<Harness initial={file} />);
    openAdvanced();
    expect(
      screen.getAllByRole("radiogroup", { name: "响应曲线" }),
    ).toHaveLength(1);
  });

  it("sets the smoothing in ms, from 0 to 500", () => {
    render(<Harness initial={mouseFile()} />);
    expect(smoothing().value).toBe("0");
    expect(smoothing().closest(".analog-setting")?.textContent).toContain("ms");
    commit(smoothing(), { target: { value: "120" } });
    expect(draft().analog?.ls).toEqual({
      mode: "mouse",
      mouseSmoothingMs: 120,
    });
    commit(smoothing(), { target: { value: "500" } });
    expect(draft().analog?.ls?.mouseSmoothingMs).toBe(500);
    // An explicit zero is kept.
    commit(smoothing(), { target: { value: "0" } });
    expect(draft().analog?.ls?.mouseSmoothingMs).toBe(0);
  });

  it("refuses a smoothing outside 0 to 500 or that is not whole", () => {
    render(<Harness initial={mouseFile()} />);
    for (const text of ["-1", "501", "2.5", "abc"]) {
      commit(smoothing(), { target: { value: text } });
      expect(smoothing().getAttribute("aria-invalid"), text).toBe("true");
      expect(screen.getByRole("alert").textContent, text).toBe(
        "请输入 0–500 的整数",
      );
      expect(draft().analog?.ls?.mouseSmoothingMs, text).toBeUndefined();
    }
  });

  it("shows the settings that are stored", () => {
    render(
      <Harness
        initial={mouseFile("ls", {
          mouseSpeed: 800,
          mouseSmoothingMs: 60,
          curve: "cubic",
        })}
      />,
    );
    expect(speed().value).toBe("800");
    expect(speedSlider().value).toBe("800");
    expect(smoothing().value).toBe("60");
    expect(
      within(curveGroup())
        .getByRole("radio", { name: "三次" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });

  it("follows the right stick's own settings", () => {
    render(
      <Harness
        initial={{
          ...layered,
          analog: {
            ls: { mouseSpeed: 500 },
            rs: { mode: "mouse", mouseSpeed: 3000 },
          },
        }}
        control="rightstick"
      />,
    );
    expect(speed().value).toBe("3000");
    commit(speed(), { target: { value: "900" } });
    expect(draft().analog?.rs).toEqual({ mode: "mouse", mouseSpeed: 900 });
    expect(draft().analog?.ls).toEqual({ mouseSpeed: 500 });
  });

  it("hides the settings that are only for directions, and keeps the trigger point, rings and raw axes", () => {
    render(<Harness initial={mouseFile()} />);
    const advanced = openAdvanced();
    for (const name of ["角度滞回", "方向切换延迟", "斜向扇区宽度"])
      expect(
        within(advanced).queryByRole("spinbutton", { name }),
        name,
      ).toBeNull();
    expect(
      within(advanced).queryByRole("switch", { name: "只有斜向" }),
    ).toBeNull();
    expect(
      within(advanced).queryByRole("radiogroup", {
        name: "斜向未设置按键时",
      }),
    ).toBeNull();
    // The trigger point stays for the push and ring controls; the pointer
    // does not wait for it.
    expect(
      within(advanced).getByRole("spinbutton", { name: "触发幅度" }),
    ).toBeDefined();
    expect(
      within(advanced).getByText(
        "只用于“朝任意方向推动”、轻推和重推；指针一推出死区就开始移动。",
      ),
    ).toBeDefined();
    expect(within(advanced).queryByText(/才按下方向/)).toBeNull();
    // The push can still be bound, without a direction to fire with.
    expect(row("ls.active").textContent).not.toContain("和方向同时触发");
    expect(row("ls.inner")).toBeDefined();
    expect(row("ls.outer")).toBeDefined();
    expect(row("lefty-")).toBeDefined();
    expect(
      within(advanced).getByRole("spinbutton", { name: "重推的位置" }),
    ).toBeDefined();
  });

  it("keeps the direction settings of the other modes", () => {
    render(<Harness initial={{ ...layered, analog: {} }} />);
    const advanced = openAdvanced();
    for (const name of ["角度滞回", "方向切换延迟"])
      expect(
        within(advanced).getByRole("spinbutton", { name }),
        name,
      ).toBeDefined();
    expect(
      within(advanced).getByRole("switch", { name: "只有斜向" }),
    ).toBeDefined();
    expect(row("ls.active").textContent).toContain("和方向同时触发");
    expect(
      within(advanced).getByText("推出死区后，达到此幅度才按下方向。"),
    ).toBeDefined();
  });

  it("writes the keys of a push of the pointer stick", () => {
    render(<Harness initial={mouseFile()} />);
    openAdvanced();
    fireEvent.click(row("ls.outer"));
    fireEvent.click(
      within(screen.getByRole("region", { name: "重推" })).getByRole("button", {
        name: "stub: send F20",
      }),
    );
    expect(draft().layers.base["ls.outer"]?.press).toEqual({
      type: "keys",
      keys: "F20",
    });
    expect(draft().analog?.ls).toEqual({ mode: "mouse" });
  });

  it("says which direction and knob keys the pointer leaves unused", () => {
    const file = mouseFile();
    file.layers.base["ls.up"] = { press: { type: "keys", keys: "Up" } };
    file.layers.base["ls.cw"] = { press: { type: "keys", keys: "]" } };
    render(<Harness initial={file} />);
    expect(
      screen.getByText("↑ → 也设置了按键，但鼠标模式不会发送。"),
    ).toBeDefined();
    expect(
      screen.getByText("旋钮的按键也设置了，但当前模式不会发送。"),
    ).toBeDefined();
    cleanup();
    const clean = mouseFile();
    delete clean.layers.base["ls.right"];
    render(<Harness initial={clean} />);
    expect(screen.queryByText(/也设置了按键/)).toBeNull();
    expect(screen.queryByText(/旋钮的按键也设置了/)).toBeNull();
  });

  it("closes a direction card that the pointer mode does not send", () => {
    render(<Harness initial={{ ...layered, analog: {} }} />);
    fireEvent.click(cell("right"));
    expect(screen.getByRole("region", { name: "→ 向右推" })).toBeDefined();
    fireEvent.click(modeRadio("鼠标"));
    expect(screen.queryByRole("region", { name: "→ 向右推" })).toBeNull();
    // The keys stay in the file.
    expect(draft().layers.base["ls.right"]).toEqual(
      layered.layers.base["ls.right"],
    );
  });

  it("reads the push as moving the pointer, with no direction or sector", () => {
    render(
      <Harness
        initial={mouseFile()}
        input={{ buttons: {}, axes: { leftx: axis(0.71), lefty: 0 } }}
      />,
    );
    expect(screen.getByTestId("stick-readout").textContent).toBe(
      "当前 70% · 移动指针",
    );
    const visual = document.querySelector(".analog-visual") as SVGElement;
    expect(visual.dataset.active).toBe("true");
    expect(visual.querySelector(".analog-v-sector")).toBeNull();
    expect(visual.querySelector(".analog-v-tick")).toBeNull();
    expect(visual.getAttribute("aria-label")).toBe(
      "摇杆实时位置：移动指针，70%",
    );
    cleanup();
    // Just outside the dead zone, far under the trigger point (30% of the
    // travel): the pointer moves already, and no push control is pressed.
    render(
      <Harness
        initial={mouseFile()}
        input={{ buttons: {}, axes: { leftx: axis(0.2), lefty: 0 } }}
      />,
    );
    expect(screen.getByTestId("stick-readout").textContent).toContain(
      "移动指针",
    );
    expect(
      (document.querySelector(".analog-visual") as SVGElement).dataset.active,
    ).toBe("false");
    cleanup();
    // Inside the dead zone nothing moves.
    render(
      <Harness
        initial={mouseFile()}
        input={{ buttons: {}, axes: { leftx: axis(0.1), lefty: 0 } }}
      />,
    );
    expect(screen.getByTestId("stick-readout").textContent).toContain("死区内");
  });

  it("copies the pointer settings with the other parameters", () => {
    const file = mouseFile("ls", { mouseSpeed: 700, mouseSmoothingMs: 40 });
    const { rerender } = render(<Harness initial={file} />);
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "复制参数" }));
    rerender(<Harness initial={file} control="rightstick" />);
    fireEvent.click(screen.getByTestId("analog-params"));
    fireEvent.click(screen.getByRole("menuitem", { name: "粘贴参数…" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "粘贴参数？" })).getByRole(
        "button",
        { name: "粘贴" },
      ),
    );
    expect(draft().analog?.rs).toEqual({
      mode: "mouse",
      mouseSpeed: 700,
      mouseSmoothingMs: 40,
    });
    expect(speed().value).toBe("700");
  });

  it("reads a read-only pointer stick but does not edit it", () => {
    render(<Harness initial={mouseFile()} disabled />);
    const isDisabled = (element: HTMLElement) => element.matches(":disabled");
    expect(isDisabled(speed())).toBe(true);
    expect(isDisabled(speedSlider())).toBe(true);
    expect(isDisabled(smoothing())).toBe(true);
    for (const name of ["线性", "二次", "三次"])
      expect(
        isDisabled(within(curveGroup()).getByRole("radio", { name })),
      ).toBe(true);
    expect(isDisabled(modeRadio("鼠标"))).toBe(true);
    expect(draft()).toEqual(mouseFile());
  });

  it("speaks English", () => {
    render(<Harness initial={mouseFile()} language="en" />);
    const mode = within(
      screen.getByRole("radiogroup", { name: "Direction mode" }),
    ).getByRole("radio", { name: "Mouse" });
    expect(mode.getAttribute("aria-checked")).toBe("true");
    commit(screen.getByRole("spinbutton", { name: "Speed" }), {
      target: { value: "20" },
    });
    expect(screen.getByRole("alert").textContent).toBe(
      "Enter a whole number from 50 to 10000",
    );
    commit(screen.getByRole("spinbutton", { name: "Smoothing" }), {
      target: { value: "900" },
    });
    expect(
      screen.getAllByRole("alert").map((element) => element.textContent),
    ).toContain("Enter a whole number from 0 to 500");
    const curve = screen.getByRole("radiogroup", { name: "Response curve" });
    for (const name of ["Linear", "Quadratic", "Cubic"])
      expect(within(curve).getByRole("radio", { name })).toBeDefined();
  });

  it("names a stored pointer setting that is out of range", () => {
    render(
      <Harness
        initial={mouseFile("ls", { mouseSpeed: 20, mouseSmoothingMs: 600 })}
      />,
    );
    expect(
      [...document.querySelectorAll(".analog-errors p")].map(
        (element) => element.textContent,
      ),
    ).toEqual(["“速度”的值超出范围。", "“平滑”的值超出范围。"]);
  });

  it("never mentions key groups or layers in the pointer panel", async () => {
    for (const language of ["zh-CN", "en"] as const) {
      const { container, unmount } = render(
        <Harness initial={mouseFile()} language={language} />,
      );
      for (const details of container.querySelectorAll("details"))
        details.open = true;
      await act(async () => {});
      const text = container.querySelector(".analog-panel")?.textContent ?? "";
      expect(text.length).toBeGreaterThan(20);
      if (language === "en") expect(text).not.toMatch(/layer/i);
      else expect(text).not.toContain("层");
      unmount();
    }
  });
});
