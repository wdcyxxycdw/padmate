// @vitest-environment jsdom
// The key drawer's part of an app integration (ARCHITECTURE §16.5): the button
// that opens the picker only for a profile that is bound, the picker's search,
// and the two ways to pick a function.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DeviceInfo, PadmateAPI } from "../../shared/api";
import type {
  IntegrationInfo,
  IntegrationItems,
} from "../../shared/integrations";
import { KeyDrawer } from "../drawer/KeyDrawer";
import { createEditorStore } from "../editor/editor-store";
import type { Language } from "../state/store";
import { fakeCapture } from "../testing/capture-fixture";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import {
  consistentItems,
  demoApp,
  demoFunctions,
  integrationStore,
  undoItem,
} from "../testing/integrations-fixture";
import { IntegrationProvider } from "./IntegrationContext";

afterEach(cleanup);

const info: DeviceInfo = {
  id: 1,
  name: "Xbox",
  type: "xboxone",
  vendor: 0,
  product: 0,
  version: 0,
  guid: "",
  path: "",
  driver: "",
  serial: "",
  gamepad: true,
  virtual: true,
};

const profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      north: { label: "笔", press: { type: "keys", keys: "F13" } },
      south: { label: "撤销", press: { type: "keys", keys: "Ctrl+Z" } },
    },
  },
};

async function setup(
  options: {
    integrations?: IntegrationInfo[];
    language?: Language;
    control?: string;
    items?: IntegrationItems;
  } = {},
) {
  const language = options.language ?? "zh-CN";
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const { record, emit } = fakeCapture(api);
  const store = integrationStore(api, options.integrations);
  if (options.items)
    api.getIntegrationItems.mockImplementation(
      async () => options.items as never,
    );
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await act(() => editor.getState().refreshMetadata());
  const view = render(
    <IntegrationProvider store={store} language={language}>
      <KeyDrawer
        editor={editor}
        control={options.control ?? "north"}
        onControl={() => {}}
        info={info}
        language={language}
        onClose={() => {}}
      />
    </IntegrationProvider>,
  );
  return { api, store, editor, record, emit, ...view };
}

const north = (editor: Awaited<ReturnType<typeof setup>>["editor"]) =>
  editor.getState().draft?.layers.base?.north;

describe("the button beside the shortcut field", () => {
  it("shows 从 <软件名> 选择… for a profile that is bound to an integration the computer has", async () => {
    await setup();
    expect(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    ).toBeTruthy();
  });

  it("says it in English too", async () => {
    await setup({ language: "en" });
    expect(
      screen.getByRole("button", { name: "Pick from Demo Paint…" }),
    ).toBeTruthy();
  });

  it("shows nothing for a profile that is not bound, an app that was not found, or no integration at all", async () => {
    for (const integrations of [
      [{ ...demoApp, profiles: ["other"] }],
      [{ ...demoApp, found: false, problem: "notFound" as const }],
      [],
    ]) {
      const view = await setup({ integrations });
      expect(screen.queryByRole("button", { name: /选择…/ })).toBeNull();
      expect(screen.queryByTestId("integration-line")).toBeNull();
      view.unmount();
    }
  });

  it("shows nothing without a provider", async () => {
    const api = editorAPIFixture();
    const editor = createEditorStore(api as unknown as PadmateAPI);
    render(
      <KeyDrawer
        editor={editor}
        control="north"
        onControl={() => {}}
        info={info}
        language="zh-CN"
        onClose={() => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: /选择…/ })).toBeNull();
  });
});

describe("the picker", () => {
  async function open(options: Parameters<typeof setup>[0] = {}) {
    const view = await setup(options);
    fireEvent.click(
      screen.getByRole("button", {
        name: /^(从 Demo Paint 选择…|Pick from Demo Paint…)$/,
      }),
    );
    await screen.findAllByTestId("picker-row");
    return view;
  }
  const rows = () =>
    screen
      .getAllByTestId("picker-row")
      .map((row) => row.querySelector(".picker-row-name")?.textContent);
  const row = (name: string) =>
    screen.getAllByRole("treeitem", { name: new RegExp(`^${name}`) })[0];
  const openGroup = (name: string) =>
    fireEvent.click(row(name).querySelector(".picker-row-chevron") as Element);
  const chooseArea = (name: string) =>
    fireEvent.change(screen.getByRole("combobox", { name: "设置区域" }), {
      target: {
        value: screen.getByRole("option", { name }).getAttribute("value"),
      },
    });

  it("opens a modal dialog with a setting-area dropdown and the first area's menu as a tree, every group closed", async () => {
    await open();
    const dialog = screen.getByRole("dialog", { name: "从 Demo Paint 选择" });
    expect(dialog.tagName).toBe("DIALOG");
    const area = within(dialog).getByRole("combobox", { name: "设置区域" });
    expect(
      within(area)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["主菜单", "工具"]);
    expect((area as HTMLSelectElement).value).toBe("a:main");
    expect(within(dialog).getByRole("tree")).toBeTruthy();
    expect(rows()).toEqual(["编辑", "文件"]);
    for (const name of ["编辑", "文件"])
      expect(row(name).getAttribute("aria-expanded")).toBe("false");
    // No categories on the side and no 未设置 text.
    expect(within(dialog).queryAllByRole("radio")).toHaveLength(0);
    expect(dialog.textContent).not.toContain("未设置");
    expect(screen.getByRole("button", { name: "取消" })).toBeTruthy();
    expect(
      (screen.getByRole("button", { name: "设置" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });

  it("has no dropdown when the app has only one area", async () => {
    const api = editorAPIFixture();
    api.getProfile = vi.fn(async (id: string) => ({
      id,
      text: JSON.stringify(profile),
      revision: "r1",
      diagnostics: [],
    }));
    fakeCapture(api);
    const store = integrationStore(api);
    api.getIntegrationFunctions.mockImplementation(async () => ({
      ...demoFunctions,
      tree: [demoFunctions.tree[0]],
    }));
    const editor = createEditorStore(api as unknown as PadmateAPI);
    await editor
      .getState()
      .activate({ id: "paint", device: "d", connection: "c" });
    await act(() => editor.getState().refreshMetadata());
    render(
      <IntegrationProvider store={store} language="zh-CN">
        <KeyDrawer
          editor={editor}
          control="north"
          onControl={() => {}}
          info={info}
          language="zh-CN"
          onClose={() => {}}
        />
      </IntegrationProvider>,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    );
    await screen.findAllByTestId("picker-row");
    expect(screen.queryByRole("combobox", { name: "设置区域" })).toBeNull();
    expect(rows()).toEqual(["编辑", "文件"]);
  });

  it("the dropdown decides which tree is shown, and each area keeps its open groups", async () => {
    await open();
    openGroup("编辑");
    expect(rows()).toEqual(["编辑", "撤销", "重做", "文件"]);
    chooseArea("工具");
    expect(rows()).toEqual(["笔"]);
    openGroup("笔");
    expect(rows()).toEqual(["笔", "G笔"]);
    chooseArea("主菜单");
    // The groups opened before are still open (they are kept until the dialog closes).
    expect(rows()).toEqual(["编辑", "撤销", "重做", "文件"]);
  });

  it("opens and closes the groups to any depth, in the order the daemon gives", async () => {
    await open();
    openGroup("编辑");
    expect(rows()).toEqual(["编辑", "撤销", "重做", "文件"]);
    expect(row("编辑").getAttribute("aria-expanded")).toBe("true");
    expect(row("撤销").getAttribute("aria-level")).toBe("2");
    // The shortcut is on the right; no shortcut leaves the column empty.
    expect(
      row("撤销").querySelector(".picker-row-keys")?.textContent,
    ).toContain("Ctrl");
    expect(row("重做").querySelector(".picker-row-keys")?.textContent).toBe("");
    chooseArea("工具");
    openGroup("笔");
    // A tool with sub-tools can be selected, and opened by its arrow.
    expect(row("G笔").getAttribute("aria-expanded")).toBe("false");
    expect(row("G笔").getAttribute("aria-selected")).toBe("false");
    expect(row("G笔").querySelector(".picker-row-keys")?.textContent).toBe("P");
    openGroup("G笔");
    expect(rows()).toEqual(["笔", "G笔", "铅笔"]);
    expect(row("铅笔").getAttribute("aria-level")).toBe("3");
    // A group that is only a group is not selectable.
    expect(row("笔").hasAttribute("aria-selected")).toBe(false);
    openGroup("笔");
    expect(rows()).toEqual(["笔"]);
  });

  it("clicking a group's name opens it, clicking a function selects it, clicking a tool that has sub-tools only selects it", async () => {
    await open();
    fireEvent.click(row("编辑"));
    expect(rows()).toEqual(["编辑", "撤销", "重做", "文件"]);
    fireEvent.click(row("撤销"));
    expect(row("撤销").getAttribute("aria-selected")).toBe("true");
    expect(
      (screen.getByRole("button", { name: "设置" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
    chooseArea("工具");
    fireEvent.click(row("笔"));
    fireEvent.click(row("G笔"));
    expect(row("G笔").getAttribute("aria-selected")).toBe("true");
    expect(row("G笔").getAttribute("aria-expanded")).toBe("false");
  });

  it("gives a function with several keys one row for each, the name only on the first", async () => {
    await open();
    fireEvent.click(row("文件"));
    const keyRows = () =>
      screen
        .getAllByTestId("picker-row")
        .filter((el) => el.getAttribute("data-key")?.startsWith("n:saveas"));
    expect(keyRows()).toHaveLength(3);
    expect(
      keyRows().map((el) => el.querySelector(".picker-row-name")?.textContent),
    ).toEqual(["另存为", "", ""]);
    expect(
      keyRows().map((el) => el.querySelector(".picker-row-keys")?.textContent),
    ).toEqual(["ShiftAltS", "CtrlShiftS", "CtrlAltS"]);
    // They are one item: selecting a row of a key selects it all.
    fireEvent.click(keyRows()[2]);
    expect(keyRows().map((el) => el.getAttribute("data-on"))).toEqual([
      "true",
      "true",
      "true",
    ]);
    expect(keyRows().map((el) => el.getAttribute("aria-selected"))).toEqual([
      "false",
      "false",
      "true",
    ]);
  });

  it("uses the key of the row that was chosen, and the first key for the name row", async () => {
    const { api, editor } = await open();
    fireEvent.click(row("文件"));
    const keyRows = () =>
      screen
        .getAllByTestId("picker-row")
        .filter((el) => el.getAttribute("data-key")?.startsWith("n:saveas"));
    fireEvent.click(keyRows()[1]);
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() =>
      expect(north(editor)?.press).toEqual({
        type: "keys",
        keys: "Ctrl+Shift+S",
      }),
    );
    expect(api.putIntegrationItems.mock.calls[0][0].items[0]).toMatchObject({
      keys: "Ctrl+Shift+S",
      function: "cmd:saveas",
    });
  });

  it("uses the first key when the name row is chosen", async () => {
    const { editor } = await open();
    fireEvent.click(row("文件"));
    fireEvent.click(row("另存为"));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() =>
      expect(north(editor)?.press).toEqual({
        type: "keys",
        keys: "Shift+Alt+S",
      }),
    );
  });

  it("searches name, path or shortcut across all areas, as the branches that match, open", async () => {
    await open();
    const search = screen.getByRole("searchbox", { name: "搜索功能" });
    fireEvent.change(search, { target: { value: "撤" } });
    expect(rows()).toEqual(["主菜单", "编辑", "撤销"]);
    expect(row("撤销").getAttribute("aria-level")).toBe("3");
    fireEvent.change(search, { target: { value: "ctrl+z" } });
    expect(rows()).toEqual(["主菜单", "编辑", "撤销"]);
    // Across areas, to any depth.
    fireEvent.change(search, { target: { value: "笔" } });
    expect(rows()).toEqual(["工具", "笔", "G笔", "铅笔"]);
    // The branches can be closed by hand while the search stays.
    openGroup("笔");
    expect(rows()).toEqual(["工具", "笔"]);
    fireEvent.change(search, { target: { value: "xyz" } });
    expect(screen.getByText("没有符合的功能。")).toBeTruthy();
  });

  it("clearing the search returns to the same area and the same open groups", async () => {
    await open();
    chooseArea("工具");
    openGroup("笔");
    const search = screen.getByRole("searchbox", { name: "搜索功能" });
    fireEvent.change(search, { target: { value: "撤" } });
    fireEvent.change(search, { target: { value: "" } });
    expect(
      (screen.getByRole("combobox", { name: "设置区域" }) as HTMLSelectElement)
        .value,
    ).toBe("a:tool");
    expect(rows()).toEqual(["笔", "G笔"]);
  });

  it("marks a cycle key on the rows of the tools that share it", async () => {
    await open();
    chooseArea("工具");
    openGroup("笔");
    expect(row("G笔").textContent).toContain(
      "循环键：P 在 Demo Paint 里由 铅笔 共用",
    );
    openGroup("G笔");
    expect(row("铅笔").textContent).toContain(
      "循环键：P 在 Demo Paint 里由 G笔 共用",
    );
  });

  it("is keyboard navigable: arrows move, right and left open and close, Enter selects", async () => {
    await open();
    const first = row("编辑");
    expect(first.getAttribute("tabindex")).toBe("0");
    expect(row("文件").getAttribute("tabindex")).toBe("-1");
    first.focus();
    const press = (key: string) =>
      fireEvent.keyDown(document.activeElement as Element, { key });
    press("ArrowRight");
    expect(rows()).toEqual(["编辑", "撤销", "重做", "文件"]);
    press("ArrowRight");
    expect(document.activeElement).toBe(row("撤销"));
    press("ArrowDown");
    expect(document.activeElement).toBe(row("重做"));
    press("Enter");
    expect(row("重做").getAttribute("aria-selected")).toBe("true");
    press("ArrowLeft");
    // On a leaf: left goes to the parent, left again closes it.
    expect(document.activeElement).toBe(row("编辑"));
    press("ArrowLeft");
    expect(rows()).toEqual(["编辑", "文件"]);
    press("End");
    expect(document.activeElement).toBe(row("文件"));
    press("Home");
    expect(document.activeElement).toBe(row("编辑"));
    // Enter on a group only opens it.
    press("ArrowDown");
    press("Enter");
    expect(rows()).toEqual(["编辑", "文件", "另存为", "", ""]);
    // The search field hands over to the tree with the down arrow.
    const search = screen.getByRole("searchbox", { name: "搜索功能" });
    fireEvent.keyDown(search, { key: "ArrowDown" });
    expect(document.activeElement?.getAttribute("role")).toBe("treeitem");
  });

  it("puts the keys of a function that has them into the binding in one edit, names the key, and writes the item", async () => {
    const { api, editor } = await open();
    fireEvent.click(row("编辑"));
    fireEvent.click(row("撤销"));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() => expect(api.putIntegrationItems).toHaveBeenCalled());
    // 笔 was the user's own name, so it stays.
    await waitFor(() =>
      expect(north(editor)).toEqual({
        label: "笔",
        press: { type: "keys", keys: "Ctrl+Z" },
      }),
    );
    expect(api.putIntegrationItems).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      revision: "i1",
      items: [undoItem],
    });
    expect(api.writeIntegration).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // One edit: one undo brings the old keys back.
    editor.getState().undo();
    expect(north(editor)?.press).toEqual({ type: "keys", keys: "F13" });
  });

  it("fills the name when the binding has none, or still has the last function's", async () => {
    const view = await setup({
      control: "south",
      items: consistentItems([undoItem]),
    });
    const { api, editor } = view;
    fireEvent.click(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    );
    await screen.findAllByTestId("picker-row");
    // 撤销 is the name of the function the key was; the new one replaces it.
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "G笔" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^G笔/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() =>
      expect(editor.getState().draft?.layers.base?.south).toEqual({
        label: "G笔",
        press: { type: "keys", keys: "P" },
      }),
    );
    const put = api.putIntegrationItems.mock.calls[0][0];
    // The old key's item is replaced by the new one.
    expect(put.items.map((item) => item.keys)).toEqual(["P"]);
  });

  it("asks for a key when the function has none, says who has it, and writes it into the app", async () => {
    const { api, editor, record } = await open();
    api.checkIntegrationKey.mockResolvedValueOnce({
      status: "conflict",
      owners: [{ id: "cmd:zoom", name: "放大", path: [] }],
      notes: [],
    });
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "重做" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByTestId("integration-record")).toBeTruthy();
    const write = within(dialog).getByRole("button", {
      name: "写入 Demo Paint",
    });
    expect((write as HTMLButtonElement).disabled).toBe(true);
    await record("Ctrl+Y", { scope: dialog });
    expect(
      (await within(dialog).findByTestId("integration-check")).textContent,
    ).toBe("这个键在 Demo Paint 里是 放大。");
    expect(api.checkIntegrationKey).toHaveBeenCalledWith({
      integration: "demoapp",
      function: "cmd:redo",
      keys: "Ctrl+Y",
    });
    // A clash cannot be written.
    expect((write as HTMLButtonElement).disabled).toBe(true);
    api.checkIntegrationKey.mockResolvedValueOnce({
      status: "free",
      owners: [],
      notes: ["toolShift"],
    });
    await record("Ctrl+Shift+Y", { scope: dialog });
    await waitFor(() =>
      expect(
        within(dialog).getByTestId("integration-check").textContent,
      ).toContain("没有被占用"),
    );
    expect(
      within(dialog).getByTestId("integration-check").textContent,
    ).toContain("按住这个键会临时切换工具");
    fireEvent.click(write);
    const written = await within(dialog).findByTestId("integration-written");
    expect(written.textContent).toContain("Ctrl+Shift+Y");
    expect(api.writeIntegration).toHaveBeenCalledWith("demoapp", [
      { function: "cmd:redo", add: "Ctrl+Shift+Y" },
    ]);
    // The binding and the item follow the write.
    await waitFor(() =>
      expect(north(editor)?.press).toEqual({
        type: "keys",
        keys: "Ctrl+Shift+Y",
      }),
    );
    expect(api.putIntegrationItems.mock.calls[0][0].items[0]).toMatchObject({
      keys: "Ctrl+Shift+Y",
      function: "cmd:redo",
    });
    // 撤销这次写入 restores the backup the write made.
    fireEvent.click(
      within(dialog).getByRole("button", { name: "撤销这次写入" }),
    );
    await waitFor(() =>
      expect(api.restoreIntegrationBackup).toHaveBeenCalledWith(
        "demoapp",
        "b1",
      ),
    );
  });

  it("says a cycle key is a cycle, with the tools that share it", async () => {
    const { api, record } = await open();
    api.checkIntegrationKey.mockResolvedValueOnce({
      status: "cycle",
      owners: [
        { id: "tool:a", name: "G笔", path: [] },
        { id: "tool:b", name: "铅笔", path: [] },
      ],
      notes: [],
    });
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "重做" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const dialog = screen.getByRole("dialog");
    await record("P", { scope: dialog });
    const check = await within(dialog).findByTestId("integration-check");
    expect(check.textContent).toContain("由 G笔、铅笔 共用");
    expect(check.textContent).toContain("不一定切到这个工具");
    expect(
      (
        within(dialog).getByRole("button", {
          name: "写入 Demo Paint",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
});

describe("the app is running", () => {
  it("blocks the write with 请先关闭 <软件名>, offers 重试 and 取消, and writes once it is closed", async () => {
    const { api, record } = await (async () => {
      const view = await setup();
      fireEvent.click(
        screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
      );
      await screen.findAllByTestId("picker-row");
      return view;
    })();
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "重做" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const picker = screen.getByRole("dialog");
    await record("Ctrl+Y", { scope: picker });
    await within(picker).findByTestId("integration-check");
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(
      within(picker).getByRole("button", { name: "写入 Demo Paint" }),
    );
    const blocking = await screen.findByRole("dialog", {
      name: "请先关闭 Demo Paint",
    });
    expect(api.writeIntegration).not.toHaveBeenCalled();
    // Still running: 重试 says so and stays.
    fireEvent.click(within(blocking).getByRole("button", { name: "重试" }));
    expect((await within(blocking).findByRole("alert")).textContent).toBe(
      "Demo Paint 还在运行。",
    );
    expect(api.writeIntegration).not.toHaveBeenCalled();
    // Closed: 重试 goes on and writes.
    api.listIntegrations.mockImplementation(async () => [demoApp]);
    fireEvent.click(within(blocking).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(api.writeIntegration).toHaveBeenCalled());
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "请先关闭 Demo Paint" }),
      ).toBeNull(),
    );
  });

  it("writes nothing when the user cancels", async () => {
    const { api, record } = await setup();
    fireEvent.click(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    );
    await screen.findAllByTestId("picker-row");
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "重做" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const picker = screen.getByRole("dialog");
    await record("Ctrl+Y", { scope: picker });
    await within(picker).findByTestId("integration-check");
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(
      within(picker).getByRole("button", { name: "写入 Demo Paint" }),
    );
    const blocking = await screen.findByRole("dialog", {
      name: "请先关闭 Demo Paint",
    });
    fireEvent.click(within(blocking).getByRole("button", { name: "取消" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "请先关闭 Demo Paint" }),
      ).toBeNull(),
    );
    expect(api.writeIntegration).not.toHaveBeenCalled();
    expect(
      within(picker).getByRole("button", { name: "写入 Demo Paint" }),
    ).toBeTruthy();
  });
});

describe("the line under the keys", () => {
  it("names the function the keys are, with its path", async () => {
    await setup({ control: "south", items: consistentItems([undoItem]) });
    const line = await screen.findByTestId("integration-line");
    expect(line.textContent).toBe("Demo Paint 功能：撤销 · 编辑");
  });

  it("says what changed in the app, and shows nothing for keys that are not an item", async () => {
    await setup({
      control: "south",
      items: {
        ...consistentItems([undoItem]),
        consistent: false,
        statuses: [
          {
            keys: "Ctrl+Z",
            function: "cmd:undo",
            state: "changed",
            current: ["Ctrl+U"],
          },
        ],
      },
    });
    const line = await screen.findByTestId("integration-line");
    expect(line.textContent).toContain("在 Demo Paint 里现在是 Ctrl+U");
    cleanup();
    await setup({ items: consistentItems([undoItem]) });
    await act(async () => {});
    expect(screen.queryByTestId("integration-line")).toBeNull();
  });
});

describe("setting from the picker leaves the mapping alone until it worked", () => {
  async function openPicker(options: Parameters<typeof setup>[0] = {}) {
    const view = await setup(options);
    fireEvent.click(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    );
    await screen.findAllByTestId("picker-row");
    return view;
  }
  const select = (name: string) => {
    fireEvent.change(screen.getByRole("searchbox", { name: "搜索功能" }), {
      target: { value: name },
    });
    fireEvent.click(
      screen.getByRole("treeitem", { name: new RegExp(`^${name}`) }),
    );
  };
  const unchanged = (editor: Awaited<ReturnType<typeof setup>>["editor"]) =>
    expect(north(editor)).toEqual({
      label: "笔",
      press: { type: "keys", keys: "F13" },
    });
  const writeReady = async (dialog: HTMLElement) => {
    await within(dialog).findByTestId("integration-check");
    await waitFor(() =>
      expect(
        (
          within(dialog).getByRole("button", {
            name: "写入 Demo Paint",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
  };

  it("draws the dialog outside the shortcut field, so a click in it starts no capture", async () => {
    const { api, editor } = await openPicker();
    const dialog = screen.getByRole("dialog", { name: "从 Demo Paint 选择" });
    expect(dialog.closest('[role="group"]')).toBeNull();
    select("撤销");
    fireEvent.click(screen.getByRole("searchbox"));
    fireEvent.mouseDown(dialog);
    fireEvent.click(dialog);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The click that closed the dialog did not start a recording either.
    await act(async () => {});
    expect(api.captureStart).not.toHaveBeenCalled();
    expect(api.putIntegrationItems).not.toHaveBeenCalled();
    unchanged(editor);
  });

  it("does not record a click on the button that opens it, nor the clicks inside, when a recording was running", async () => {
    const { api, editor, emit } = await setup();
    const group = screen.getByRole("group", { name: "快捷键" });
    fireEvent.click(
      within(group).getByRole("button", { name: /^录入快捷键$/ }),
    );
    await act(async () => {});
    expect(api.captureStart).toHaveBeenCalledTimes(1);
    const pick = screen.getByRole("button", { name: "从 Demo Paint 选择…" });
    // The press on the button is not a mouse key of the recording.
    fireEvent.mouseDown(pick, { button: 0 });
    fireEvent.click(pick);
    await screen.findAllByTestId("picker-row");
    // Opening the dialog ended the recording.
    await waitFor(() => expect(api.captureStop).toHaveBeenCalled());
    select("撤销");
    fireEvent.mouseDown(screen.getByRole("treeitem", { name: /^撤销/ }), {
      button: 0,
    });
    fireEvent.mouseDown(screen.getByRole("button", { name: "设置" }), {
      button: 0,
    });
    await emit({ active: false, reason: "stopped" });
    unchanged(editor);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    unchanged(editor);
  });

  it("the first save of a profile with no integrations file sends the empty revision", async () => {
    const { api, editor } = await openPicker({
      items: { ...consistentItems(), revision: "" },
    });
    select("撤销");
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() =>
      expect(north(editor)?.press).toEqual({ type: "keys", keys: "Ctrl+Z" }),
    );
    expect(api.putIntegrationItems).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      revision: "",
      items: [undoItem],
    });
  });

  it("shows progress while it works, with the buttons off", async () => {
    const { api, editor } = await openPicker();
    let finish: () => void = () => {};
    api.putIntegrationItems.mockImplementationOnce(
      () => new Promise<void>((resolve) => (finish = resolve)),
    );
    select("撤销");
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const working = await screen.findByRole("button", { name: "正在设置…" });
    expect((working as HTMLButtonElement).disabled).toBe(true);
    expect(working.getAttribute("aria-busy")).toBe("true");
    expect(
      (screen.getByRole("button", { name: "取消" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    unchanged(editor);
    await act(async () => finish());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(north(editor)?.press).toEqual({ type: "keys", keys: "Ctrl+Z" });
  });

  it("says in plain words what failed, keeps the mapping, and lets the person try again", async () => {
    const { api, editor } = await openPicker();
    api.putIntegrationItems.mockRejectedValueOnce(new Error("Items not saved"));
    select("撤销");
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(
      "没能把这个功能记进这份配置，快捷键没有改。请再试一次。",
    );
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(
      (screen.getByRole("button", { name: "设置" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
    unchanged(editor);
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(north(editor)?.press).toEqual({ type: "keys", keys: "Ctrl+Z" });
  });

  it("changes nothing when the person cancels", async () => {
    const { api, editor } = await openPicker();
    select("撤销");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(api.putIntegrationItems).not.toHaveBeenCalled();
    expect(api.writeIntegration).not.toHaveBeenCalled();
    unchanged(editor);
  });

  it("a failed save after the app was written leaves the mapping, and a second try finishes", async () => {
    const { api, editor, record } = await openPicker();
    select("重做");
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const dialog = screen.getByRole("dialog");
    await record("Ctrl+Y", { scope: dialog });
    await writeReady(dialog);
    api.putIntegrationItems.mockRejectedValueOnce(new Error("Items not saved"));
    fireEvent.click(
      within(dialog).getByRole("button", { name: "写入 Demo Paint" }),
    );
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "没能把这个功能记进这份配置",
    );
    expect(api.writeIntegration).toHaveBeenCalledTimes(1);
    unchanged(editor);
    expect(within(dialog).queryByTestId("integration-written")).toBeNull();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "写入 Demo Paint" }),
    );
    await within(dialog).findByTestId("integration-written");
    expect(north(editor)?.press).toEqual({ type: "keys", keys: "Ctrl+Y" });
  });

  it("a failed write leaves the mapping as it was and says why", async () => {
    const { api, editor, record } = await openPicker();
    select("重做");
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const dialog = screen.getByRole("dialog");
    await record("Ctrl+Y", { scope: dialog });
    await writeReady(dialog);
    api.writeIntegration.mockRejectedValueOnce(
      Object.assign(new Error("Settings not written"), { code: -32030 }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: "写入 Demo Paint" }),
    );
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "Demo Paint 的文件正在使用",
    );
    expect(api.putIntegrationItems).not.toHaveBeenCalled();
    unchanged(editor);
  });
});
