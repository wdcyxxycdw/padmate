// @vitest-environment jsdom
// The dialog for the differences between the open profile's keys and the app
// it is bound to (ARCHITECTURE §16.5): when it comes, and what its three
// choices do.
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
import type { PadmateAPI } from "../../shared/api";
import type {
  IntegrationInfo,
  IntegrationItems,
  IntegrationPlan,
} from "../../shared/integrations";
import { createEditorStore } from "../editor/editor-store";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { demoApp, integrationStore } from "../testing/integrations-fixture";
import { IntegrationProvider } from "./IntegrationContext";
import { IntegrationDifferences } from "./IntegrationDifferences";

afterEach(cleanup);

const profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      south: { label: "撤销", press: { type: "keys", keys: "Ctrl+Z" } },
      north: { label: "笔", press: { type: "keys", keys: "P" } },
      east: { label: "旧", press: { type: "keys", keys: "Q" } },
    },
  },
};

const items: IntegrationItems = {
  revision: "i1",
  items: [
    { keys: "Ctrl+Z", function: "cmd:undo", name: "撤销", path: ["编辑"] },
    { keys: "P", function: "tool:a", name: "G笔", path: [] },
    { keys: "Q", function: "cmd:old", name: "旧功能", path: [] },
  ],
  statuses: [
    {
      keys: "Ctrl+Z",
      function: "cmd:undo",
      state: "changed",
      current: ["Ctrl+U"],
    },
    { keys: "P", function: "tool:a", state: "consistent" },
    { keys: "Q", function: "cmd:old", state: "removed" },
  ],
  consistent: false,
  dismissed: false,
  differenceId: "d1",
};

async function setup(
  options: {
    integrations?: IntegrationInfo[];
    looking?: boolean;
    data?: IntegrationItems;
    plan?: IntegrationPlan;
  } = {},
) {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const store = integrationStore(api, options.integrations);
  api.getIntegrationItems.mockImplementation(async () => options.data ?? items);
  if (options.plan) api.planIntegration.mockResolvedValue(options.plan);
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  const view = render(
    <IntegrationProvider store={store} language="zh-CN">
      <IntegrationDifferences
        store={store}
        editor={editor}
        language="zh-CN"
        profileId="paint"
        looking={options.looking ?? true}
      />
    </IntegrationProvider>,
  );
  return { api, store, editor, ...view };
}

const dialog = () =>
  screen.findByRole("dialog", { name: "Demo Paint 的设置变了" });

describe("when the dialog comes", () => {
  it("lists every difference with the three choices, and cannot be closed without one", async () => {
    await setup();
    const box = await dialog();
    const list = within(box).getByTestId("difference-list");
    expect(
      [...list.querySelectorAll("li")].map((li) => li.textContent),
    ).toEqual([
      "撤销 CtrlZ在 Demo Paint 里现在是 Ctrl+U",
      "旧功能 Q在 Demo Paint 里已没有快捷键",
    ]);
    expect(
      [...box.querySelectorAll(".dialog-footer button")].map(
        (b) => b.textContent,
      ),
    ).toEqual(["以 padmate 为准", "以 Demo Paint 为准", "暂不处理"]);
    // Esc and × do nothing: the close button is off.
    expect(
      (within(box).getByRole("button", { name: "关闭" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });

  it("does not come while the user is not looking at the profile", async () => {
    const view = await setup({ looking: false });
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
    view.unmount();
  });

  it("does not come for a profile that is not bound, an app not found, settings that agree, or differences put off before", async () => {
    for (const options of [
      { integrations: [{ ...demoApp, profiles: [] }] },
      { integrations: [{ ...demoApp, found: false }] },
      { data: { ...items, consistent: true } },
      // Put off in an earlier run: the daemon says so.
      { data: { ...items, dismissed: true } },
    ]) {
      const view = await setup(options);
      await act(async () => {});
      expect(screen.queryByRole("dialog")).toBeNull();
      view.unmount();
    }
  });

  it("comes when integration.changed says the app changed under the open profile", async () => {
    const { api, store } = await setup({
      data: { ...items, consistent: true },
    });
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
    api.getIntegrationItems.mockImplementation(async () => items);
    act(() =>
      store.setState({
        integrationEvent: {
          seq: 1,
          change: {
            integration: "demoapp",
            kind: "differences",
            profile: "paint",
          },
        },
      }),
    );
    expect(await dialog()).toBeTruthy();
  });
});

describe("暂不处理", () => {
  it("dismisses this set of differences and does not come back for it", async () => {
    const { api, store } = await setup();
    const box = await dialog();
    fireEvent.click(within(box).getByRole("button", { name: "暂不处理" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(api.dismissIntegrationDifferences).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      differenceId: "d1",
    });
    act(() =>
      store.setState({
        integrationEvent: {
          seq: 2,
          change: { integration: "demoapp", kind: "files" },
        },
      }),
    );
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("以 <软件> 为准", () => {
  it("keeps each button's function, changes its keys in one undo step, and moves the items", async () => {
    const { api, editor } = await setup();
    fireEvent.click(
      within(await dialog()).getByRole("button", {
        name: "以 Demo Paint 为准",
      }),
    );
    const done = await screen.findByTestId("difference-done");
    // The function that has no key in the app now keeps its key.
    expect(done.textContent).toBe(
      "按键已改成 Demo Paint 里现在的键。旧功能 在 Demo Paint 里没有快捷键，保留了原来的键。",
    );
    const base = editor.getState().draft?.layers.base;
    expect(base?.south?.press).toEqual({ type: "keys", keys: "Ctrl+U" });
    expect(base?.east?.press).toEqual({ type: "keys", keys: "Q" });
    expect(api.putIntegrationItems).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      revision: "i1",
      items: [
        { keys: "Ctrl+U", function: "cmd:undo", name: "撤销", path: ["编辑"] },
        items.items[1],
        items.items[2],
      ],
    });
    expect(api.writeIntegration).not.toHaveBeenCalled();
    editor.getState().undo();
    expect(editor.getState().draft?.layers.base?.south?.press).toEqual({
      type: "keys",
      keys: "Ctrl+Z",
    });
  });
});

describe("以 padmate 为准", () => {
  const plan: IntegrationPlan = {
    entries: [
      {
        item: items.items[0],
        changes: [{ function: "cmd:undo", add: "Ctrl+Z", remove: "Ctrl+U" }],
        displaced: [{ function: "cmd:zoom", name: "放大", keys: "Ctrl+Z" }],
      },
      {
        item: items.items[2],
        changes: [],
        displaced: [],
        skipped: "missing",
      },
    ],
  };

  it("shows what the plan displaces and skips before it writes anything", async () => {
    const { api } = await setup({ plan });
    fireEvent.click(
      within(await dialog()).getByRole("button", { name: "以 padmate 为准" }),
    );
    const confirm = await screen.findByTestId("difference-confirm");
    expect(api.planIntegration).toHaveBeenCalledWith("paint", "demoapp");
    expect(confirm.textContent).toContain("会把 1 项写回 Demo Paint");
    expect(confirm.textContent).toContain("放大");
    expect(confirm.textContent).toContain("Demo Paint 里已找不到这个功能。");
    expect(confirm.textContent).not.toContain("missing");
    expect(api.writeIntegration).not.toHaveBeenCalled();
  });

  it("writes after the app is closed, and offers to undo the write", async () => {
    const { api } = await setup({ plan });
    fireEvent.click(
      within(await dialog()).getByRole("button", { name: "以 padmate 为准" }),
    );
    await screen.findByTestId("difference-confirm");
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(screen.getByRole("button", { name: "写入 Demo Paint" }));
    const blocking = await screen.findByRole("dialog", {
      name: "请先关闭 Demo Paint",
    });
    expect(api.writeIntegration).not.toHaveBeenCalled();
    api.listIntegrations.mockImplementation(async () => [demoApp]);
    fireEvent.click(within(blocking).getByRole("button", { name: "重试" }));
    await screen.findByTestId("difference-done");
    expect(api.writeIntegration).toHaveBeenCalledWith("demoapp", [
      { function: "cmd:undo", add: "Ctrl+Z", remove: "Ctrl+U" },
      { function: "cmd:zoom", remove: "Ctrl+Z" },
    ]);
    fireEvent.click(screen.getByRole("button", { name: "撤销这次写入" }));
    await waitFor(() =>
      expect(api.restoreIntegrationBackup).toHaveBeenCalledWith(
        "demoapp",
        "b1",
      ),
    );
  });

  it("says a failed write in a sentence and stays", async () => {
    const { api } = await setup({ plan });
    fireEvent.click(
      within(await dialog()).getByRole("button", { name: "以 padmate 为准" }),
    );
    await screen.findByTestId("difference-confirm");
    api.writeIntegration.mockRejectedValueOnce(
      Object.assign(new Error("x"), { code: -32030 }),
    );
    fireEvent.click(screen.getByRole("button", { name: "写入 Demo Paint" }));
    expect((await screen.findByRole("alert")).textContent).toContain(
      "什么都没有写入",
    );
    expect(screen.getByTestId("difference-confirm")).toBeTruthy();
  });
});

describe("differences that cannot be resolved", () => {
  // What the app has after the user followed it: the one function with no key
  // left is still not consistent, and the set has another ID.
  const left: IntegrationItems = {
    ...items,
    items: [
      { keys: "Ctrl+U", function: "cmd:undo", name: "撤销", path: ["编辑"] },
      items.items[1],
      items.items[2],
    ],
    statuses: [
      { keys: "Ctrl+U", function: "cmd:undo", state: "consistent" },
      { keys: "P", function: "tool:a", state: "consistent" },
      { keys: "Q", function: "cmd:old", state: "removed" },
    ],
    differenceId: "d2",
  };

  const changed = (seq: number) =>
    ({
      integrationEvent: {
        seq,
        change: {
          integration: "demoapp",
          kind: "differences",
          profile: "paint",
        },
      },
    }) as const;

  it("are dismissed after 以 <软件> 为准, and the dialog does not come back for them", async () => {
    const { api, store } = await setup();
    let moved = false;
    api.putIntegrationItems.mockImplementation(async () => {
      moved = true;
      return { revision: "i2" } as never;
    });
    api.getIntegrationItems.mockImplementation(async () =>
      moved ? left : items,
    );
    fireEvent.click(
      within(await dialog()).getByRole("button", {
        name: "以 Demo Paint 为准",
      }),
    );
    await screen.findByTestId("difference-done");
    expect(api.dismissIntegrationDifferences).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      differenceId: "d2",
    });
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The daemon now says the set is dismissed; an event does not reopen it.
    api.getIntegrationItems.mockImplementation(async () => ({
      ...left,
      dismissed: true,
    }));
    act(() => store.setState(changed(5)));
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
    // Even when the daemon has not kept it, this run remembers it.
    api.getIntegrationItems.mockImplementation(async () => left);
    act(() => store.setState(changed(6)));
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
    // A different difference later is new, and opens the dialog.
    api.getIntegrationItems.mockImplementation(async () => ({
      ...left,
      statuses: [
        ...left.statuses.slice(0, 1),
        { keys: "P", function: "tool:a", state: "removed" as const },
        left.statuses[2],
      ],
      differenceId: "d3",
    }));
    act(() => store.setState(changed(7)));
    expect(await dialog()).toBeTruthy();
  });

  it("are dismissed after 以 padmate 为准 as well", async () => {
    const plan: IntegrationPlan = {
      entries: [
        {
          item: items.items[0],
          changes: [{ function: "cmd:undo", add: "Ctrl+Z", remove: "Ctrl+U" }],
          displaced: [],
        },
        {
          item: items.items[2],
          changes: [],
          displaced: [],
          skipped: "missing",
        },
      ],
    };
    const { api } = await setup({ plan });
    let written = false;
    api.writeIntegration.mockImplementation(async () => {
      written = true;
      return { backup: { id: "b1" } } as never;
    });
    api.getIntegrationItems.mockImplementation(async () =>
      written ? left : items,
    );
    fireEvent.click(
      within(await dialog()).getByRole("button", { name: "以 padmate 为准" }),
    );
    await screen.findByTestId("difference-confirm");
    fireEvent.click(screen.getByRole("button", { name: "写入 Demo Paint" }));
    const done = await screen.findByTestId("difference-done");
    expect(done.textContent).toContain("旧功能");
    expect(api.dismissIntegrationDifferences).toHaveBeenCalledWith({
      profile: "paint",
      integration: "demoapp",
      differenceId: "d2",
    });
  });
});
