// @vitest-environment jsdom
// An imported profile that carries an integration's items (ARCHITECTURE
// §16.5, ADR-0019 §7): the offer, "keep my app and move the keys", and the
// blocking per-item conflict dialog before anything is written.
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
import type {
  IntegrationItems,
  IntegrationPlan,
} from "../../shared/integrations";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { demoApp, integrationStore } from "../testing/integrations-fixture";
import type { CaptureField } from "./FunctionPicker";
import { ImportIntegrations } from "./ImportIntegrations";
import { IntegrationProvider } from "./IntegrationContext";

afterEach(cleanup);

const profileText = JSON.stringify({
  schemaVersion: 1,
  name: "Imported",
  layers: {
    base: {
      south: { press: { type: "keys", keys: "Ctrl+Z" } },
      north: { press: { type: "keys", keys: "P" } },
    },
  },
});

const items: IntegrationItems = {
  revision: "i1",
  items: [
    { keys: "Ctrl+Z", function: "cmd:undo", name: "撤销", path: [] },
    { keys: "P", function: "tool:a", name: "G笔", path: [] },
  ],
  statuses: [
    {
      keys: "Ctrl+Z",
      function: "cmd:undo",
      state: "changed",
      current: ["Ctrl+U"],
    },
    {
      keys: "P",
      function: "tool:a",
      state: "changed",
      owners: [{ id: "cmd:zoom", name: "放大", path: [] }],
    },
  ],
  consistent: false,
  dismissed: false,
};

// The shortcut capture of the drawer, as a text box.
const Capture: CaptureField = ({ value, onChange, label }) => (
  <input
    aria-label={label}
    value={value}
    onChange={(event) => onChange(event.target.value)}
  />
);

async function setup(
  options: { data?: IntegrationItems; plan?: IntegrationPlan } = {},
) {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: profileText,
    revision: "r1",
    diagnostics: [],
  }));
  const store = integrationStore(api, [{ ...demoApp, profiles: [] }]);
  api.getIntegrationItems.mockImplementation(async () => options.data ?? items);
  if (options.plan) api.planIntegration.mockResolvedValue(options.plan);
  const onDone = vi.fn();
  render(
    <IntegrationProvider store={store} language="zh-CN">
      <ImportIntegrations
        store={store}
        profile="imported"
        language="zh-CN"
        Capture={Capture}
        onDone={onDone}
      />
    </IntegrationProvider>,
  );
  return { api, store, onDone };
}

const offer = () =>
  screen.findByRole("dialog", { name: "导入的 Demo Paint 设置" });

describe("the offer", () => {
  it("lists the differences with the three choices", async () => {
    await setup();
    const box = await offer();
    expect(
      [...within(box).getByTestId("import-list").querySelectorAll("li")].map(
        (li) => li.textContent,
      ),
    ).toEqual([
      "撤销 CtrlZ在 Demo Paint 里现在是 Ctrl+U",
      "G笔 P这个键在 Demo Paint 里现在是 放大",
    ]);
    expect(
      [...box.querySelectorAll(".dialog-footer button")].map(
        (b) => b.textContent,
      ),
    ).toEqual([
      "用导入的设置改我的 Demo Paint",
      "不改，按我的 Demo Paint 调整按键",
      "以后再说",
    ]);
  });

  it("offers nothing, and is done, when the items agree with the app or there are none", async () => {
    for (const data of [
      { ...items, consistent: true },
      { ...items, items: [], statuses: [], consistent: false },
    ]) {
      const { onDone } = await setup({ data });
      await waitFor(() => expect(onDone).toHaveBeenCalled());
      expect(screen.queryByRole("dialog")).toBeNull();
      cleanup();
    }
  });

  it("does nothing more on 以后再说", async () => {
    const { api, onDone } = await setup();
    fireEvent.click(
      within(await offer()).getByRole("button", { name: "以后再说" }),
    );
    expect(onDone).toHaveBeenCalled();
    expect(api.writeIntegration).not.toHaveBeenCalled();
    expect(api.putProfile).not.toHaveBeenCalled();
  });
});

describe("不改，按我的 <软件> 调整按键", () => {
  it("moves the keys of the profile's file and its items, and never writes the app", async () => {
    const { api } = await setup();
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "不改，按我的 Demo Paint 调整按键",
      }),
    );
    expect((await screen.findByTestId("import-done")).textContent).toBe(
      "按键已按你的 Demo Paint 调整。",
    );
    const [id, text, revision] = api.putProfile.mock.calls[0];
    expect([id, revision]).toEqual(["imported", "r1"]);
    expect(JSON.parse(text).layers.base.south.press.keys).toBe("Ctrl+U");
    expect(JSON.parse(text).layers.base.north.press.keys).toBe("P");
    expect(
      api.putIntegrationItems.mock.calls[0][0].items.map((i) => i.keys),
    ).toEqual(["Ctrl+U", "P"]);
    expect(api.writeIntegration).not.toHaveBeenCalled();
  });
});

describe("用导入的设置改我的 <软件>", () => {
  const free: IntegrationPlan = {
    entries: [
      {
        item: items.items[0],
        changes: [{ function: "cmd:undo", add: "Ctrl+Z", remove: "Ctrl+U" }],
        displaced: [],
      },
    ],
  };

  it("writes at once when nothing local is displaced, and offers to undo", async () => {
    const { api } = await setup({ plan: free });
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "用导入的设置改我的 Demo Paint",
      }),
    );
    await screen.findByTestId("import-done");
    expect(api.writeIntegration).toHaveBeenCalledWith(
      "demoapp",
      free.entries[0].changes,
    );
    fireEvent.click(screen.getByRole("button", { name: "撤销这次写入" }));
    await waitFor(() =>
      expect(api.restoreIntegrationBackup).toHaveBeenCalledWith(
        "demoapp",
        "b1",
      ),
    );
  });

  const clash: IntegrationPlan = {
    entries: [
      free.entries[0],
      {
        item: items.items[1],
        changes: [{ function: "tool:a", add: "P" }],
        displaced: [{ function: "cmd:zoom", name: "放大", keys: "P" }],
      },
    ],
  };

  it("lists what would be displaced first, then asks for every such item and writes only when all are decided", async () => {
    const { api } = await setup({ plan: clash });
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "用导入的设置改我的 Demo Paint",
      }),
    );
    const displaced = await screen.findByTestId("import-displaced");
    expect(displaced.textContent).toContain("放大");
    expect(api.writeIntegration).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "逐项决定" }));
    const decide = await screen.findByTestId("import-decide");
    // It blocks: the close button is off, and nothing is decided yet.
    expect(
      (screen.getByRole("button", { name: "关闭" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    const write = screen.getByRole("button", { name: "写入 Demo Paint" });
    expect((write as HTMLButtonElement).disabled).toBe(true);
    const entry = within(decide).getByTestId("import-entry");
    expect(entry.textContent).toContain("导入的“G笔”和本机的“放大”都要用 P");
    // Another key for the imported function needs a key that can be written.
    fireEvent.click(
      within(entry).getByRole("radio", { name: "给导入的功能换一个键" }),
    );
    expect((write as HTMLButtonElement).disabled).toBe(true);
    api.checkIntegrationKey.mockResolvedValueOnce({
      status: "conflict",
      owners: [{ id: "cmd:y", name: "缩小", path: [] }],
      notes: [],
    });
    fireEvent.change(within(entry).getByRole("textbox", { name: "新的键" }), {
      target: { value: "O" },
    });
    await within(entry).findByText(/这个键在 Demo Paint 里是 缩小/);
    expect((write as HTMLButtonElement).disabled).toBe(true);
    api.checkIntegrationKey.mockResolvedValueOnce({
      status: "free",
      owners: [],
      notes: [],
    });
    fireEvent.change(within(entry).getByRole("textbox", { name: "新的键" }), {
      target: { value: "K" },
    });
    await waitFor(() =>
      expect((write as HTMLButtonElement).disabled).toBe(false),
    );
    expect(api.checkIntegrationKey).toHaveBeenLastCalledWith({
      integration: "demoapp",
      function: "tool:a",
      keys: "K",
    });
    fireEvent.click(write);
    await screen.findByTestId("import-done");
    expect(api.writeIntegration).toHaveBeenCalledWith("demoapp", [
      { function: "cmd:undo", add: "Ctrl+Z", remove: "Ctrl+U" },
      { function: "tool:a", add: "K" },
    ]);
    // The imported profile follows the new key.
    const [, text] = api.putProfile.mock.calls[0];
    expect(JSON.parse(text).layers.base.north.press.keys).toBe("K");
    expect(api.putIntegrationItems.mock.calls[0][0].items[1].keys).toBe("K");
  });

  it("writes nothing for an item whose local setting is kept", async () => {
    const { api } = await setup({ plan: clash });
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "用导入的设置改我的 Demo Paint",
      }),
    );
    await screen.findByTestId("import-displaced");
    fireEvent.click(screen.getByRole("button", { name: "逐项决定" }));
    const entry = await screen.findByTestId("import-entry");
    fireEvent.click(
      within(entry).getByRole("radio", { name: "保留本机的设置" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "写入 Demo Paint" }));
    await screen.findByTestId("import-done");
    expect(api.writeIntegration).toHaveBeenCalledWith("demoapp", [
      free.entries[0].changes[0],
    ]);
    expect(api.putProfile).not.toHaveBeenCalled();
  });

  it("takes the key from the local function when the imported setting wins, or gives that function another key", async () => {
    const { api } = await setup({ plan: clash });
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "用导入的设置改我的 Demo Paint",
      }),
    );
    await screen.findByTestId("import-displaced");
    fireEvent.click(screen.getByRole("button", { name: "逐项决定" }));
    const entry = await screen.findByTestId("import-entry");
    fireEvent.click(
      within(entry).getByRole("radio", { name: "给本机的功能换一个键" }),
    );
    fireEvent.change(within(entry).getByRole("textbox", { name: "新的键" }), {
      target: { value: "Ctrl+Shift+Z" },
    });
    await waitFor(() =>
      expect(api.checkIntegrationKey).toHaveBeenLastCalledWith({
        integration: "demoapp",
        function: "cmd:zoom",
        keys: "Ctrl+Shift+Z",
      }),
    );
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "写入 Demo Paint",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "写入 Demo Paint" }));
    await screen.findByTestId("import-done");
    expect(api.writeIntegration).toHaveBeenCalledWith("demoapp", [
      free.entries[0].changes[0],
      { function: "tool:a", add: "P" },
      { function: "cmd:zoom", remove: "P", add: "Ctrl+Shift+Z" },
    ]);
  });

  it("waits for the app to be closed before writing", async () => {
    const { api } = await setup({ plan: free });
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, profiles: [], running: true },
    ]);
    fireEvent.click(
      within(await offer()).getByRole("button", {
        name: "用导入的设置改我的 Demo Paint",
      }),
    );
    await screen.findByRole("dialog", { name: "请先关闭 Demo Paint" });
    expect(api.writeIntegration).not.toHaveBeenCalled();
    await act(async () => {});
  });
});
