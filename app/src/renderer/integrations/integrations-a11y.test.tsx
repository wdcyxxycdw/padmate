// @vitest-environment jsdom
// axe over the dialogs of the app integrations, the way a11y.test.tsx checks
// the others: the picker (both steps), the differences dialog, the import
// dialogs, the "close the app" dialog and the settings section.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PadmateAPI } from "../../shared/api";
import type {
  IntegrationItems,
  IntegrationPlan,
} from "../../shared/integrations";
import { createEditorStore } from "../editor/editor-store";
import { IntegrationSections } from "../pages/settings/SettingsIntegrations";
import { axeReport } from "../testing/a11y-harness";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { demoApp, integrationStore } from "../testing/integrations-fixture";
import { type CaptureField, FunctionPicker } from "./FunctionPicker";
import { ImportIntegrations } from "./ImportIntegrations";
import { IntegrationProvider } from "./IntegrationContext";
import { IntegrationDifferences } from "./IntegrationDifferences";

beforeEach(() => {
  document.title = "test";
  document.documentElement.lang = "zh-CN";
});
afterEach(cleanup);

const Capture: CaptureField = ({ value, onChange, label }) => (
  <input
    aria-label={label}
    value={value}
    onChange={(e) => onChange(e.target.value)}
  />
);

const items: IntegrationItems = {
  revision: "i1",
  items: [
    { keys: "Ctrl+Z", function: "cmd:undo", name: "撤销", path: ["编辑"] },
  ],
  statuses: [
    {
      keys: "Ctrl+Z",
      function: "cmd:undo",
      state: "changed",
      current: ["Ctrl+U"],
    },
  ],
  consistent: false,
  dismissed: false,
  differenceId: "d1",
};

const clash: IntegrationPlan = {
  entries: [
    {
      item: items.items[0],
      changes: [{ function: "cmd:undo", add: "Ctrl+Z" }],
      displaced: [{ function: "cmd:zoom", name: "放大", keys: "Ctrl+Z" }],
    },
  ],
};

function setup() {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify({ schemaVersion: 1, name: "P", layers: { base: {} } }),
    revision: "r1",
    diagnostics: [],
  }));
  api.planIntegration.mockResolvedValue(clash);
  api.listIntegrationBackups.mockResolvedValue([
    {
      id: "b1",
      created: "2026-11-04T10:00:00Z",
      reason: "write",
      changes: 1,
    },
  ]);
  const store = integrationStore(api);
  api.getIntegrationItems.mockImplementation(async () => items);
  return { api, store };
}

const page = (children: React.ReactNode) => (
  <main>
    <h1>映射</h1>
    {children}
  </main>
);

async function clean(label: string) {
  expect(await axeReport(), label).toEqual([]);
}

describe("the dialogs of the app integrations", () => {
  it.each(["zh-CN", "en"] as const)(
    "the picker, list and record steps (%s)",
    async (language) => {
      const { store } = setup();
      render(
        <IntegrationProvider store={store} language={language}>
          {page(
            <FunctionPicker
              open
              info={demoApp}
              profile="paint"
              currentKeys=""
              language={language}
              Capture={Capture}
              onClose={() => {}}
              onPicked={() => {}}
            />,
          )}
        </IntegrationProvider>,
      );
      await screen.findAllByTestId("picker-row");
      await clean("picker list");
      const tree = screen.getByRole("tree");
      expect(tree.getAttribute("aria-label")).toBeTruthy();
      const item = (name: RegExp) =>
        screen
          .getAllByRole("treeitem")
          .find((entry) => name.test(entry.textContent ?? "")) as HTMLElement;
      // The groups open and a function with several keys is selected.
      fireEvent.click(item(/^编辑/));
      fireEvent.click(item(/^文件/));
      fireEvent.click(item(/^另存为/));
      await clean("picker tree open and selected");
      // The other area, with a tool that is also a group.
      fireEvent.change(screen.getByRole("combobox"), {
        target: { value: "a:tool" },
      });
      fireEvent.click(item(/^笔/));
      fireEvent.click(item(/^G笔/));
      await clean("picker second area");
      fireEvent.change(screen.getByRole("searchbox"), {
        target: { value: "重做" },
      });
      await clean("picker search results");
      fireEvent.click(item(/^重做/));
      fireEvent.click(
        screen
          .getAllByRole("button")
          .find((b) => b.className.includes("btn-primary")) as HTMLElement,
      );
      await screen.findByTestId("integration-record");
      await clean("picker record");
    },
  );

  it("the differences dialog, with its plan and its result", async () => {
    const { api, store } = setup();
    const editor = createEditorStore(api as unknown as PadmateAPI);
    await editor
      .getState()
      .activate({ id: "paint", device: "d", connection: "c" });
    render(
      <IntegrationProvider store={store} language="zh-CN">
        {page(
          <IntegrationDifferences
            store={store}
            editor={editor}
            language="zh-CN"
            profileId="paint"
            looking
          />,
        )}
      </IntegrationProvider>,
    );
    await screen.findByTestId("difference-list");
    await clean("differences");
    fireEvent.click(screen.getByRole("button", { name: "以 padmate 为准" }));
    await screen.findByTestId("difference-confirm");
    await clean("differences confirm");
  });

  it("the import dialogs: the offer, the displaced list and the decisions", async () => {
    const { store } = setup();
    render(
      <IntegrationProvider store={store} language="zh-CN">
        {page(
          <ImportIntegrations
            store={store}
            profile="imported"
            language="zh-CN"
            Capture={Capture}
            onDone={() => {}}
          />,
        )}
      </IntegrationProvider>,
    );
    await screen.findByTestId("import-list");
    await clean("import offer");
    fireEvent.click(screen.getByRole("button", { name: /用导入的设置改我的/ }));
    await screen.findByTestId("import-displaced");
    await clean("import displaced");
    fireEvent.click(screen.getByRole("button", { name: "逐项决定" }));
    await screen.findByTestId("import-decide");
    fireEvent.click(
      screen.getByRole("radio", { name: "给导入的功能换一个键" }),
    );
    await clean("import decisions");
  });

  it("the close-the-app dialog and the settings section", async () => {
    const { api, store } = setup();
    render(
      <IntegrationProvider store={store} language="zh-CN">
        {page(<IntegrationSections language="zh-CN" />)}
      </IntegrationProvider>,
    );
    await screen.findAllByTestId("integration-backup");
    await clean("settings section");
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(screen.getByRole("button", { name: /恢复 .* 的备份/ }));
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    await screen.findByRole("dialog", { name: "请先关闭 Demo Paint" });
    await waitFor(() =>
      expect(screen.getByTestId("integration-running")).toBeTruthy(),
    );
    await clean("running dialog");
  });
});
