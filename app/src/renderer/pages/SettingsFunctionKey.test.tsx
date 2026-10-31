// @vitest-environment jsdom
// ADR-0076 section 6: the settings' choice of the function key shows each key
// as the connected controller shows it, the icon and then the name, and not
// as text that explains where the key sits.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FunctionKey } from "../editor-model";
import { FunctionKeyRow, functionKeyChoices } from "./SettingsFunctionKey";

afterEach(cleanup);

const names = (pad?: Parameters<typeof functionKeyChoices>[0]) =>
  functionKeyChoices(pad).map((choice) => [choice.key, choice.name]);

describe("the keys to choose from", () => {
  it("are Menu, View and the Xbox button, named as an Xbox controller does, when none is connected", () => {
    expect(names()).toEqual([
      ["start", "Menu"],
      ["back", "View"],
      ["guide", "Xbox"],
    ]);
    expect(names({ type: "xboxone" })).toEqual(names());
  });

  it("are named as the connected controller prints them", () => {
    expect(names({ type: "ps5" })).toEqual([
      ["start", "Options"],
      ["back", "Create"],
      ["guide", "PS"],
    ]);
    expect(names({ type: "ps4" })).toEqual([
      ["start", "Options"],
      ["back", "Share"],
      ["guide", "PS"],
    ]);
    expect(names({ type: "switchpro" })).toEqual([
      ["start", "+"],
      ["back", "−"],
      ["guide", "Home"],
    ]);
    expect(names({ type: "xbox360" })).toEqual([
      ["start", "Start"],
      ["back", "Back"],
      ["guide", "Xbox"],
    ]);
  });

  it("follow the names the controller reports", () => {
    expect(
      names({ type: "xboxone", buttonLabels: { start: "Hamburger" } })[0],
    ).toEqual(["start", "Hamburger"]);
  });

  it("name a key a single Joy-Con held sideways lacks as an Xbox controller does", () => {
    // View is not on it, and the setting is for every controller.
    expect(names({ type: "joyconleft", buttonMask: 1 })).toEqual([
      ["start", "−"],
      ["back", "View"],
      ["guide", "Capture"],
    ]);
    expect(names({ type: "joyconright", buttonMask: 1 })).toEqual([
      ["start", "+"],
      ["back", "View"],
      ["guide", "Home"],
    ]);
  });

  it("are drawn with the icon of the key preview: the symbol printed on the key, not a word (ADR-0077, ADR-0079 §7)", () => {
    const [menu, view, xbox] = functionKeyChoices({ type: "xboxone" });
    expect(menu.icon).toEqual({ kind: "art", name: "kenney/xbox_button_menu" });
    expect(view.icon).toEqual({ kind: "art", name: "kenney/xbox_button_view" });
    expect(xbox.icon).toEqual({ kind: "art", name: "kenney/xbox_guide" });
    expect(functionKeyChoices({ type: "ps5" }).map(({ icon }) => icon)).toEqual(
      [
        { kind: "art", name: "kenney/playstation5_button_options" },
        { kind: "art", name: "kenney/playstation5_button_create" },
        { kind: "art", name: "kenney/switch_button_home" },
      ],
    );
    expect(
      functionKeyChoices({ type: "switchpro" }).map(({ icon }) => icon),
    ).toEqual([
      { kind: "art", name: "kenney/switch_button_plus" },
      { kind: "art", name: "kenney/switch_button_minus" },
      { kind: "art", name: "kenney/switch_button_home" },
    ]);
  });
});

describe("the row of the setting", () => {
  const row = (
    value: FunctionKey = "start",
    options: {
      onChange?: (key: FunctionKey) => void;
      disabled?: boolean;
      pad?: Parameters<typeof functionKeyChoices>[0];
      language?: "zh-CN" | "en";
    } = {},
  ) =>
    render(
      <FunctionKeyRow
        language={options.language ?? "zh-CN"}
        value={value}
        disabled={options.disabled ?? false}
        pad={options.pad}
        onChange={options.onChange ?? (() => {})}
      />,
    );
  const group = () =>
    screen.getByRole("radiogroup", { name: /功能键|Function key/ });
  const radios = () => screen.getAllByRole("radio");

  it("is its name and the three keys, each an icon and a name, with no text to explain them", () => {
    row();
    expect(group().getAttribute("aria-label")).toBe("功能键");
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "Menu",
      "View",
      "Xbox",
    ]);
    // The icon is drawn, and hidden from a screen reader, which reads the name.
    for (const radio of radios()) {
      const icon = radio.querySelector(".control-icon");
      expect(icon?.getAttribute("aria-hidden")).toBe("true");
      expect(icon?.getAttribute("data-kind")).toBe("art");
      expect(icon?.hasAttribute("data-label")).toBe(false);
    }
    expect(radios().map((radio) => radio.getAttribute("aria-checked"))).toEqual(
      ["true", "false", "false"],
    );
    expect(document.querySelector(".row-hint")).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(document.body.textContent).not.toContain("小键");
    expect(document.body.textContent).not.toContain("大键");
  });

  it("shows the key the file chooses", () => {
    row("guide");
    expect(radios().map((radio) => radio.getAttribute("aria-checked"))).toEqual(
      ["false", "false", "true"],
    );
  });

  it("takes the names of the connected controller, in English too", () => {
    row("start", { pad: { type: "ps5" }, language: "en" });
    expect(group().getAttribute("aria-label")).toBe("Function key");
    expect(radios().map((radio) => radio.textContent)).toEqual([
      "Options",
      "Create",
      "PS",
    ]);
  });

  it("is chosen by a click, and by the arrow keys like radio buttons", () => {
    const onChange = vi.fn();
    row("start", { onChange });
    fireEvent.click(radios()[1]);
    expect(onChange).toHaveBeenLastCalledWith("back");
    fireEvent.keyDown(radios()[0], { key: "ArrowRight" });
    expect(onChange).toHaveBeenLastCalledWith("back");
    fireEvent.keyDown(radios()[0], { key: "ArrowLeft" });
    expect(onChange).toHaveBeenLastCalledWith("guide");
    // Only the chosen one is a tab stop.
    expect(radios().map((radio) => radio.tabIndex)).toEqual([0, -1, -1]);
  });

  it("is off when it cannot be saved", () => {
    row("start", { disabled: true });
    for (const radio of radios())
      expect((radio as HTMLButtonElement).disabled).toBe(true);
  });
});
