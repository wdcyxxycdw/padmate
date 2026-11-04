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
  const { record } = fakeCapture(api);
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
  return { api, store, editor, record, ...view };
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

  it("opens a modal dialog with every function, 全部 first, then the daemon's categories", async () => {
    await open();
    const dialog = screen.getByRole("dialog", { name: "从 Demo Paint 选择" });
    expect(dialog.tagName).toBe("DIALOG");
    expect(rows()).toEqual(["撤销", "重做", "G笔", "铅笔"]);
    const categories = within(dialog)
      .getAllByRole("radio", { checked: false })
      .concat(within(dialog).getAllByRole("radio", { checked: true }));
    expect(categories.length).toBeGreaterThan(4);
    const names = [...dialog.querySelectorAll(".picker-category span")].map(
      (span) => span.textContent,
    );
    expect(names).toEqual(["全部", "编辑", "工具"]);
    // Name, path in small text, and the keys or 未设置.
    const redo = screen.getAllByTestId("picker-row")[1];
    expect(redo.textContent).toContain("编辑");
    expect(redo.textContent).toContain("未设置");
    expect(screen.getByRole("button", { name: "取消" })).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "使用这个功能",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("filters by search (name, path or shortcut) and by category", async () => {
    await open();
    const search = screen.getByRole("searchbox", { name: "搜索功能" });
    fireEvent.change(search, { target: { value: "撤" } });
    expect(rows()).toEqual(["撤销"]);
    fireEvent.change(search, { target: { value: "ctrl+z" } });
    expect(rows()).toEqual(["撤销"]);
    fireEvent.change(search, { target: { value: "笔" } });
    expect(rows()).toEqual(["G笔", "铅笔"]);
    fireEvent.change(search, { target: { value: "" } });
    fireEvent.click(screen.getByRole("radio", { name: "工具" }));
    expect(rows()).toEqual(["G笔", "铅笔"]);
    fireEvent.change(search, { target: { value: "撤" } });
    expect(screen.getByText("没有符合的功能。")).toBeTruthy();
  });

  it("marks a cycle key in the row, with the tools that share it", async () => {
    await open();
    const pen = screen.getAllByTestId("picker-row")[2];
    expect(pen.textContent).toContain("循环键：P 在 Demo Paint 里由 铅笔 共用");
  });

  it("is keyboard navigable: the search field, then a radio group per list", async () => {
    await open();
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog)
        .getAllByRole("radio")
        .every((radio) => radio.tagName === "INPUT"),
    ).toBe(true);
    expect(within(dialog).getByRole("searchbox")).toBeTruthy();
  });

  it("puts the keys of a function that has them into the binding in one edit, names the key, and writes the item", async () => {
    const { api, editor } = await open();
    fireEvent.click(screen.getByRole("radio", { name: /^撤销/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
    fireEvent.click(screen.getByRole("radio", { name: /^G笔/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
    fireEvent.click(screen.getByRole("radio", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
    fireEvent.click(screen.getByRole("radio", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
    fireEvent.click(screen.getByRole("radio", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
    fireEvent.click(screen.getByRole("radio", { name: /^重做/ }));
    fireEvent.click(screen.getByRole("button", { name: "使用这个功能" }));
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
