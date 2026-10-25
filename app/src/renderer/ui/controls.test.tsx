// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  Dialog,
  Disclosure,
  MenuButton,
  type MenuEntry,
  menuAlign,
  Segmented,
} from "./controls";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const nextFrame = () =>
  act(
    () =>
      new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );

describe("Segmented", () => {
  const options = [
    { value: "a", label: "甲" },
    { value: "b", label: "乙" },
    { value: "c", label: "丙" },
  ];
  const stops = () =>
    screen
      .getAllByRole("radio")
      .map((radio) => (radio as HTMLButtonElement).tabIndex);

  it("has one tab stop: the selected option", () => {
    render(
      <Segmented
        label="选择"
        options={options}
        value="b"
        onChange={() => {}}
      />,
    );
    expect(stops()).toEqual([-1, 0, -1]);
  });

  it("has a tab stop when no option matches, so the keyboard can reach it (UI-24)", () => {
    const change = vi.fn();
    render(
      <Segmented label="选择" options={options} value="" onChange={change} />,
    );
    expect(stops()).toEqual([0, -1, -1]);
    expect(
      screen
        .getAllByRole("radio")
        .every((radio) => radio.ariaChecked === "false"),
    ).toBe(true);
    // The arrows enter the group from its ends.
    fireEvent.keyDown(screen.getByRole("radiogroup"), { key: "ArrowRight" });
    expect(change).toHaveBeenLastCalledWith("a");
    fireEvent.keyDown(screen.getByRole("radiogroup"), { key: "ArrowLeft" });
    expect(change).toHaveBeenLastCalledWith("c");
  });

  it("skips a disabled option for the tab stop", () => {
    render(
      <Segmented
        label="选择"
        options={[{ ...options[0], disabled: true }, options[1], options[2]]}
        value="zzz"
        onChange={() => {}}
      />,
    );
    expect(stops()).toEqual([-1, 0, -1]);
  });

  it("has no tab stop when everything is disabled", () => {
    render(
      <Segmented
        label="选择"
        options={options}
        value="a"
        disabled
        onChange={() => {}}
      />,
    );
    expect(stops()).toEqual([-1, -1, -1]);
  });
});

describe("Dialog", () => {
  function Host({ returnFocus }: { returnFocus?: () => HTMLElement | null }) {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          打开
        </button>
        <button type="button">别处</button>
        <Dialog
          open={open}
          title="对话框"
          closeLabel="关闭对话框"
          onClose={() => setOpen(false)}
          returnFocus={returnFocus}
        >
          <button type="button" onClick={() => setOpen(false)}>
            完成
          </button>
        </Dialog>
      </>
    );
  }

  it("returns focus to the button that opened it", () => {
    render(<Host />);
    const opener = screen.getByRole("button", { name: "打开" });
    opener.focus();
    fireEvent.click(opener);
    const done = screen.getByRole("button", { name: "完成" });
    done.focus();
    fireEvent.click(done);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it("returns focus to the caller's target when it gives one", () => {
    let target: HTMLElement | null = null;
    render(<Host returnFocus={() => target} />);
    target = screen.getByRole("button", { name: "别处" });
    const opener = screen.getByRole("button", { name: "打开" });
    opener.focus();
    fireEvent.click(opener);
    fireEvent.click(screen.getByRole("button", { name: "关闭对话框" }));
    expect(document.activeElement).toBe(target);
  });

  it("restores focus when its owner unmounts it while open", async () => {
    function Owner() {
      const [shown, setShown] = useState(true);
      return (
        <>
          <button type="button">打开</button>
          {shown && (
            <Dialog open title="对话框" closeLabel="关闭" onClose={() => {}}>
              <button type="button" onClick={() => setShown(false)}>
                放弃
              </button>
            </Dialog>
          )}
        </>
      );
    }
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    render(<Owner />);
    const inside = screen.getByRole("button", { name: "放弃" });
    inside.focus();
    fireEvent.click(inside);
    expect(screen.queryByRole("dialog")).toBeNull();
    await nextFrame();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("keeps a closed dialog hidden and empty", () => {
    const { container } = render(
      <Dialog open={false} title="对话框" closeLabel="关闭" onClose={() => {}}>
        <p>内容</p>
      </Dialog>,
    );
    expect(container.querySelector("dialog")?.hasAttribute("open")).toBe(false);
    expect(screen.queryByText("内容")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("MenuButton", () => {
  const items = (onSelect = () => {}): MenuEntry[] => [
    { kind: "section", label: "分组" },
    { label: "长按", hint: "按住一段时间后触发", onSelect },
    { label: "双击", onSelect },
    {
      kind: "radio",
      label: "动作名称",
      hint: "默认",
      checked: true,
      onSelect,
    },
  ];

  it("names items by their label and describes them with the hint", () => {
    render(<MenuButton label="添加手势" items={items()} />);
    fireEvent.click(screen.getByRole("button", { name: "添加手势" }));
    const hold = screen.getByRole("menuitem", { name: "长按" });
    const hint = document.getElementById(
      hold.getAttribute("aria-describedby") ?? "",
    );
    expect(hint?.textContent).toBe("按住一段时间后触发");
    expect(screen.getByRole("menuitem", { name: "双击" })).toBeDefined();
    expect(
      screen
        .getByRole("menuitem", { name: "双击" })
        .hasAttribute("aria-describedby"),
    ).toBe(false);
    const radio = screen.getByRole("menuitemradio", { name: "动作名称" });
    expect(
      document.getElementById(radio.getAttribute("aria-describedby") ?? "")
        ?.textContent,
    ).toBe("默认");
  });

  it("returns focus to its button after an item is chosen", () => {
    const onSelect = vi.fn();
    render(<MenuButton label="更多" items={items(onSelect)} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    fireEvent.click(trigger);
    const item = screen.getByRole("menuitem", { name: "双击" });
    expect(document.activeElement).toBe(
      screen.getByRole("menuitem", { name: "长按" }),
    );
    item.focus();
    fireEvent.click(item);
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("closes when focus tabs out of the menu", () => {
    render(
      <>
        <MenuButton label="更多" items={items()} />
        <button type="button">外面</button>
      </>,
    );
    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    const last = screen.getByRole("menuitemradio", { name: "动作名称" });
    // Moving inside the menu, or a blur with no new target, keeps it open.
    fireEvent.blur(last, {
      relatedTarget: screen.getByRole("menuitem", { name: "长按" }),
    });
    fireEvent.blur(last, { relatedTarget: null });
    expect(screen.getByRole("menu")).toBeDefined();
    fireEvent.blur(last, {
      relatedTarget: screen.getByRole("button", { name: "外面" }),
    });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("moves to the first and last item with Home and End", () => {
    render(<MenuButton label="更多" items={items()} />);
    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    const menu = screen.getByRole("menu");
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(
      screen.getByRole("menuitemradio", { name: "动作名称" }),
    );
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(
      screen.getByRole("menuitem", { name: "长按" }),
    );
  });

  it("opens for every new openRequest above 0", () => {
    const view = render(
      <MenuButton label="按键操作" items={items()} openRequest={0} />,
    );
    expect(screen.queryByRole("menu")).toBeNull();
    view.rerender(
      <MenuButton label="按键操作" items={items()} openRequest={1} />,
    );
    expect(screen.getByRole("menu", { name: "按键操作" })).toBeDefined();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    // The same request does not reopen it; dropping to 0 does nothing.
    view.rerender(
      <MenuButton label="按键操作" items={items()} openRequest={1} />,
    );
    view.rerender(
      <MenuButton label="按键操作" items={items()} openRequest={0} />,
    );
    expect(screen.queryByRole("menu")).toBeNull();
    view.rerender(
      <MenuButton label="按键操作" items={items()} openRequest={2} />,
    );
    expect(screen.getByRole("menu")).toBeDefined();
  });

  it("opens for the request it mounts with, unless disabled", () => {
    const view = render(
      <MenuButton label="参数操作" items={items()} openRequest={3} />,
    );
    expect(screen.getByRole("menu", { name: "参数操作" })).toBeDefined();
    view.unmount();
    render(
      <MenuButton label="参数操作" items={items()} openRequest={4} disabled />,
    );
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("opens upward when the scroll container has no room below", () => {
    const { container } = render(
      <div data-testid="scroller" style={{ overflowY: "auto" }}>
        <MenuButton label="添加手势" items={items()} />
      </div>,
    );
    const scroller = screen.getByTestId("scroller");
    const anchor = container.querySelector(".menu-anchor") as HTMLElement;
    const rect = (top: number, bottom: number) =>
      ({
        top,
        bottom,
        left: 0,
        right: 300,
        width: 300,
        height: bottom - top,
      }) as DOMRect;
    vi.spyOn(scroller, "getBoundingClientRect").mockReturnValue(rect(0, 500));
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(420, 452));
    vi.spyOn(Element.prototype, "scrollHeight", "get").mockReturnValue(200);
    fireEvent.click(screen.getByRole("button", { name: "添加手势" }));
    const menu = screen.getByRole("menu");
    expect(menu.dataset.side).toBe("top");
    expect(menu.style.maxHeight).toBe("");
  });

  it("caps the height when neither side has room", () => {
    const { container } = render(
      <div data-testid="scroller" style={{ overflowY: "auto" }}>
        <MenuButton label="添加手势" items={items()} />
      </div>,
    );
    const scroller = screen.getByTestId("scroller");
    const anchor = container.querySelector(".menu-anchor") as HTMLElement;
    const rect = (top: number, bottom: number) =>
      ({
        top,
        bottom,
        left: 0,
        right: 300,
        width: 300,
        height: bottom - top,
      }) as DOMRect;
    vi.spyOn(scroller, "getBoundingClientRect").mockReturnValue(rect(0, 300));
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(100, 132));
    vi.spyOn(Element.prototype, "scrollHeight", "get").mockReturnValue(400);
    fireEvent.click(screen.getByRole("button", { name: "添加手势" }));
    const menu = screen.getByRole("menu");
    expect(menu.dataset.side).toBe("bottom");
    // 300 - 132 below, minus the 6px gap and a 4px margin.
    expect(menu.style.maxHeight).toBe("158px");
    expect(menu.style.overflowY).toBe("auto");
  });

  it("opens downward when there is room", () => {
    render(<MenuButton label="更多" items={items()} />);
    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    expect(screen.getByRole("menu").dataset.side).toBe("bottom");
  });

  it.each<[string, "start" | "end", number, number, number, "start" | "end"]>([
    ["keeps start with room to the right", "start", 100, 180, 240, "start"],
    ["flips start near the right edge", "start", 700, 780, 240, "end"],
    ["keeps end with room to the left", "end", 700, 780, 240, "end"],
    ["flips end near the left edge", "end", 20, 100, 240, "start"],
    ["takes the roomier side when neither fits", "start", 450, 530, 600, "end"],
    ["keeps the request before it is measured", "start", 700, 780, 0, "start"],
  ])(
    "lines the menu up with the button: %s",
    (_, align, left, right, width, side) => {
      // Inside a 0–800 scroll container; the menu is width wide.
      const clip = { left: 0, right: 800 };
      expect(menuAlign(align, { left, right }, clip, width)).toBe(side);
    },
  );

  it("opens towards the side with room inside a container that clips", () => {
    const { container } = render(
      <div
        data-testid="scroller"
        style={{ overflowX: "hidden", overflowY: "auto" }}
      >
        <MenuButton label="添加软件" align="start" items={items()}>
          添加软件
        </MenuButton>
      </div>,
    );
    const scroller = screen.getByTestId("scroller");
    const anchor = container.querySelector(".menu-anchor") as HTMLElement;
    const rect = (left: number, right: number) =>
      ({
        top: 0,
        bottom: 500,
        left,
        right,
        width: right - left,
        height: 500,
      }) as DOMRect;
    vi.spyOn(scroller, "getBoundingClientRect").mockReturnValue(rect(0, 600));
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(500, 590));
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(240);
    fireEvent.click(screen.getByRole("button", { name: "添加软件" }));
    expect(screen.getByRole("menu").dataset.align).toBe("end");
  });
});

describe("Disclosure", () => {
  function Host({ open }: { open: boolean }) {
    return (
      <Disclosure title="高级" defaultOpen={open} testId="advanced">
        <button type="button">取消屏蔽</button>
      </Disclosure>
    );
  }
  const details = () => screen.getByTestId("advanced") as HTMLDetailsElement;

  it("does not close itself when defaultOpen turns false", () => {
    const view = render(<Host open />);
    expect(details().open).toBe(true);
    view.rerender(<Host open={false} />);
    expect(details().open).toBe(true);
  });

  it("opens when defaultOpen turns true and leaves a user's choice alone", async () => {
    const view = render(<Host open={false} />);
    expect(details().open).toBe(false);
    view.rerender(<Host open />);
    expect(details().open).toBe(true);
    details().open = false;
    view.rerender(<Host open />);
    await waitFor(() => expect(details().open).toBe(false));
  });
});
