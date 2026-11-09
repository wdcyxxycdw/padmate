// @vitest-environment jsdom
// Taking a pick back (ARCHITECTURE §16.5): 撤销这次写入 puts the app, the
// binding and the profile's item file back together, as one step; a plain
// undo of the pick moves the item file with the binding; and a write to the
// app whose save into the profile failed has two ways out and no third.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { DeviceInfo, PadmateAPI } from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import type { IntegrationItem } from "../../shared/integrations";
import { KeyDrawer } from "../drawer/KeyDrawer";
import { createEditorStore } from "../editor/editor-store";
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

const profile: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      north: { label: "笔", press: { type: "keys", keys: "F13" } },
      south: { label: "撤销", press: { type: "keys", keys: "Ctrl+Z" } },
    },
  },
};

/** The drawer on a profile whose item file lives in the test (items). */
async function setup(
  options: { control?: "north" | "south"; items?: IntegrationItem[] } = {},
) {
  const control = options.control ?? "south";
  const api = editorAPIFixture();
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const { record } = fakeCapture(api);
  const store = integrationStore(api);
  const file = { items: options.items ?? [], revision: 1 };
  api.getIntegrationItems.mockImplementation(async () => ({
    ...consistentItems(file.items),
    revision: `i${file.revision}`,
  }));
  api.putIntegrationItems.mockImplementation(async (params) => {
    if (params.revision !== `i${file.revision}`)
      throw new Error("revision conflict");
    file.items = params.items;
    file.revision++;
  });
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await act(() => editor.getState().refreshMetadata());
  const view = render(
    <IntegrationProvider store={store} language="zh-CN">
      <KeyDrawer
        editor={editor}
        control={control}
        onControl={() => {}}
        info={info}
        language="zh-CN"
        onClose={() => {}}
      />
    </IntegrationProvider>,
  );
  const binding = () => editor.getState().draft?.layers.base?.[control];
  const keys = () => (binding()?.press as { keys?: string } | undefined)?.keys;
  return { api, store, editor, record, file, binding, keys, ...view };
}
type Setup = Awaited<ReturnType<typeof setup>>;

const keysOf = (items: IntegrationItem[]) => items.map((item) => item.keys);

/** Opens the picker, picks 重做 (no key), records Ctrl+Y and writes it. */
async function writeRedo(s: Setup) {
  fireEvent.click(screen.getByRole("button", { name: "从 Demo Paint 选择…" }));
  await screen.findAllByTestId("picker-row");
  fireEvent.change(screen.getByRole("searchbox", { name: "搜索功能" }), {
    target: { value: "重做" },
  });
  fireEvent.click(screen.getByRole("treeitem", { name: /^重做/ }));
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  const dialog = screen.getByRole("dialog");
  await s.record("Ctrl+Y", { scope: dialog });
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
  fireEvent.click(
    within(dialog).getByRole("button", { name: "写入 Demo Paint" }),
  );
  return dialog;
}
const undoButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", { name: "撤销这次写入" });

describe("撤销这次写入 after a key was written into the app", () => {
  it("puts the app, the item file and the binding back, and leaves no undo step of the pick", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    // The pick replaced the item of the old key.
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    expect(s.keys()).toBe("Ctrl+Y");
    expect(s.binding()?.label).toBe("重做");
    expect(s.editor.getState().canUndo).toBe(true);

    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.api.restoreIntegrationBackup).toHaveBeenCalledWith(
      "demoapp",
      "b1",
    );
    // The old key is back in the binding exactly as it was, with its name.
    expect(s.binding()).toEqual(profile.layers.base.south);
    // The item of the old key is back; the new one is gone.
    expect(s.file.items).toEqual([undoItem]);
    // No step of the pick is left to undo or to redo.
    expect(s.editor.getState().canUndo).toBe(false);
    expect(s.editor.getState().canRedo).toBe(false);
    expect(s.editor.getState().undo()).toBe(false);
    expect(s.binding()).toEqual(profile.layers.base.south);
    // The profile on disk is the old one again.
    await s.editor.getState().flush();
    const saved = s.api.putProfile.mock.calls.at(-1)?.[1] ?? "";
    expect(JSON.parse(saved).layers.base.south).toEqual(
      profile.layers.base.south,
    );
  });

  it("removes the item when the key had none before", async () => {
    const s = await setup({ control: "north" });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.file.items).toEqual([]);
    expect(s.binding()).toEqual(profile.layers.base.north);
    expect(s.editor.getState().canUndo).toBe(false);
  });

  it("keeps earlier steps of the editor: only the pick goes", async () => {
    const s = await setup();
    s.editor.getState().edit({
      ...(s.editor.getState().draft as Profile),
      layers: {
        base: {
          ...profile.layers.base,
          east: { label: "e", press: { type: "keys", keys: "F14" } },
        },
      },
    });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.editor.getState().draft?.layers.base?.east).toBeTruthy();
    // The one step left is the earlier one.
    expect(s.editor.getState().undo()).toBe(true);
    expect(s.editor.getState().draft?.layers.base?.east).toBeUndefined();
    expect(s.editor.getState().canUndo).toBe(false);
  });

  it("a failed restore reverts nothing else, says so, and a second try does everything", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    const putsBefore = s.api.putIntegrationItems.mock.calls.length;
    s.api.restoreIntegrationBackup.mockRejectedValueOnce(new Error("busy"));
    fireEvent.click(undoButton(dialog));
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "没能撤销这次写入",
    );
    // Nothing moved: the binding, the item file and the undo step stay.
    expect(s.keys()).toBe("Ctrl+Y");
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    expect(s.api.putIntegrationItems.mock.calls.length).toBe(putsBefore);
    expect(s.editor.getState().canUndo).toBe(true);
    expect(screen.getByRole("dialog")).toBeTruthy();
    // Try again.
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.keys()).toBe("Ctrl+Z");
    expect(s.file.items).toEqual([undoItem]);
    expect(s.editor.getState().canUndo).toBe(false);
  });

  it("the app still running ends in 取消 with nothing reverted", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    s.api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(undoButton(dialog));
    const blocking = await screen.findByRole("dialog", {
      name: "请先关闭 Demo Paint",
    });
    fireEvent.click(within(blocking).getByRole("button", { name: "取消" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "请先关闭 Demo Paint" }),
      ).toBeNull(),
    );
    expect(s.api.restoreIntegrationBackup).not.toHaveBeenCalled();
    expect(s.keys()).toBe("Ctrl+Y");
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    expect(s.editor.getState().canUndo).toBe(true);
  });

  it("when the item file cannot be put back the app and the binding are, and a second try finishes only that", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    s.api.putIntegrationItems.mockRejectedValueOnce(new Error("disk"));
    fireEvent.click(undoButton(dialog));
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "没能把这份配置里的记录改回去",
    );
    expect(s.api.restoreIntegrationBackup).toHaveBeenCalledTimes(1);
    expect(s.keys()).toBe("Ctrl+Z");
    expect(s.editor.getState().canUndo).toBe(false);
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The app is not restored twice.
    expect(s.api.restoreIntegrationBackup).toHaveBeenCalledTimes(1);
    expect(s.file.items).toEqual([undoItem]);
    expect(s.keys()).toBe("Ctrl+Z");
  });

  it("refuses, changing nothing, when the binding was changed after the pick", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    act(() => {
      s.editor.getState().edit({
        ...(s.editor.getState().draft as Profile),
        layers: {
          base: {
            ...profile.layers.base,
            south: { label: "x", press: { type: "keys", keys: "F20" } },
          },
        },
      });
    });
    fireEvent.click(undoButton(dialog));
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "不能一起撤销",
    );
    expect(s.api.restoreIntegrationBackup).not.toHaveBeenCalled();
    expect(s.keys()).toBe("F20");
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
  });
});

describe("a plain undo of a pick (Ctrl+Z)", () => {
  it("moves the item file with the binding, and redo moves it forward again", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    fireEvent.click(within(dialog).getByRole("button", { name: "完成" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);

    act(() => {
      s.editor.getState().undo();
    });
    expect(s.keys()).toBe("Ctrl+Z");
    await waitFor(() => expect(s.file.items).toEqual([undoItem]));
    // The app keeps the key that was added to it: only padmate's mapping went.
    expect(s.api.restoreIntegrationBackup).not.toHaveBeenCalled();

    act(() => {
      s.editor.getState().redo();
    });
    expect(s.keys()).toBe("Ctrl+Y");
    await waitFor(() => expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]));
  });

  it("does the same for a function that already had a key", async () => {
    const s = await setup({ control: "north" });
    fireEvent.click(
      screen.getByRole("button", { name: "从 Demo Paint 选择…" }),
    );
    await screen.findAllByTestId("picker-row");
    fireEvent.change(screen.getByRole("searchbox", { name: "搜索功能" }), {
      target: { value: "撤销" },
    });
    fireEvent.click(screen.getByRole("treeitem", { name: /^撤销/ }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    await waitFor(() => expect(s.keys()).toBe("Ctrl+Z"));
    await waitFor(() => expect(keysOf(s.file.items)).toEqual(["Ctrl+Z"]));
    act(() => {
      s.editor.getState().undo();
    });
    expect(s.keys()).toBe("F13");
    await waitFor(() => expect(s.file.items).toEqual([]));
  });

  it("tells the shell when the item file could not follow", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-written");
    fireEvent.click(within(dialog).getByRole("button", { name: "完成" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    s.api.putIntegrationItems.mockRejectedValueOnce(new Error("disk"));
    act(() => {
      s.editor.getState().undo();
    });
    await waitFor(() =>
      expect(s.store.getState().error).toContain("没能让这份配置里"),
    );
  });
});

describe("the app is written but the profile could not save it", () => {
  async function unsaved(s: Setup) {
    s.api.putIntegrationItems.mockRejectedValueOnce(new Error("disk"));
    const dialog = await writeRedo(s);
    await within(dialog).findByTestId("integration-unsaved");
    return dialog;
  }

  it("says which step worked and which failed, offers the two ways out, and nothing else", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await unsaved(s);
    expect(within(dialog).getByRole("alert").textContent).toBe(
      "Demo Paint 里已经写好了，但没能记进这份配置，快捷键还没有改。",
    );
    expect(
      within(dialog).getByRole("button", { name: "重试保存" }),
    ).toBeTruthy();
    expect(undoButton(dialog)).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "取消" })).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "完成" })).toBeNull();
    // The close button is off and Esc does nothing.
    expect(
      (
        within(dialog).getByRole("button", {
          name: "关闭",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(s.keys()).toBe("Ctrl+Z");
    expect(s.file.items).toEqual([undoItem]);
  });

  it("重试保存 repeats only the save and the binding, not the write", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await unsaved(s);
    fireEvent.click(within(dialog).getByRole("button", { name: "重试保存" }));
    await within(dialog).findByTestId("integration-written");
    expect(s.api.writeIntegration).toHaveBeenCalledTimes(1);
    expect(s.keys()).toBe("Ctrl+Y");
    expect(keysOf(s.file.items)).toEqual(["Ctrl+Y"]);
    // And the finished pick can be taken back whole.
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.keys()).toBe("Ctrl+Z");
    expect(s.file.items).toEqual([undoItem]);
  });

  it("重试保存 failing again stays on the same two ways out", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await unsaved(s);
    s.api.putIntegrationItems.mockRejectedValueOnce(new Error("disk"));
    fireEvent.click(within(dialog).getByRole("button", { name: "重试保存" }));
    await waitFor(() =>
      expect(within(dialog).getByRole("alert").textContent).toContain(
        "没能记进这份配置",
      ),
    );
    expect(within(dialog).getByTestId("integration-unsaved")).toBeTruthy();
    expect(s.keys()).toBe("Ctrl+Z");
  });

  it("撤销这次写入 restores the app and then nothing is changed anywhere", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await unsaved(s);
    fireEvent.click(undoButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(s.api.restoreIntegrationBackup).toHaveBeenCalledWith(
      "demoapp",
      "b1",
    );
    expect(s.keys()).toBe("Ctrl+Z");
    expect(s.binding()).toEqual(profile.layers.base.south);
    expect(s.file.items).toEqual([undoItem]);
    expect(s.editor.getState().canUndo).toBe(false);
  });

  it("a failed restore keeps the two ways out", async () => {
    const s = await setup({ items: [undoItem] });
    const dialog = await unsaved(s);
    s.api.restoreIntegrationBackup.mockRejectedValueOnce(new Error("busy"));
    fireEvent.click(undoButton(dialog));
    await waitFor(() =>
      expect(within(dialog).getByRole("alert").textContent).toContain(
        "没能撤销这次写入",
      ),
    );
    expect(within(dialog).getByTestId("integration-unsaved")).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "重试保存" }),
    ).toBeTruthy();
  });

  it("a write that changed nothing in the app has nothing to take back: the record step stays with 取消", async () => {
    const s = await setup({ items: [undoItem] });
    s.api.writeIntegration.mockResolvedValueOnce({
      backup: null,
      revision: "f2",
    });
    s.api.putIntegrationItems.mockRejectedValueOnce(new Error("disk"));
    const dialog = await writeRedo(s);
    await waitFor(() =>
      expect(within(dialog).getByRole("alert").textContent).toContain(
        "快捷键没有改",
      ),
    );
    expect(within(dialog).queryByTestId("integration-unsaved")).toBeNull();
    expect(within(dialog).getByRole("button", { name: "取消" })).toBeTruthy();
    expect(s.keys()).toBe("Ctrl+Z");
  });
});
