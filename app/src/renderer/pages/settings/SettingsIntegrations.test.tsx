// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type {
  IntegrationBackup,
  IntegrationInfo,
} from "../../../shared/integrations";
import { IntegrationProvider } from "../../integrations/IntegrationContext";
import { editorAPIFixture } from "../../testing/editor-api-fixture";
import { demoApp, integrationStore } from "../../testing/integrations-fixture";
import { IntegrationSections } from "./SettingsIntegrations";

afterEach(cleanup);

const backups: IntegrationBackup[] = [
  { id: "b2", created: "2026-11-04T10:05:00Z", reason: "write", changes: 3 },
  {
    id: "b1",
    created: "2026-11-03T09:00:00Z",
    reason: "beforeRestore",
    changes: 0,
  },
];

function setup(integrations: IntegrationInfo[] = [demoApp]) {
  const api = editorAPIFixture();
  api.listIntegrationBackups.mockResolvedValue(backups);
  const store = integrationStore(api, integrations);
  render(
    <IntegrationProvider store={store} language="zh-CN">
      <IntegrationSections language="zh-CN" />
    </IntegrationProvider>,
  );
  return { api, store };
}

describe("the settings section of an integration", () => {
  it("shows when at least one profile is bound, with the data folder and the backups newest first", async () => {
    setup();
    const section = screen.getByTestId("integration-section-demoapp");
    expect(
      within(section).getByRole("heading", { name: "Demo Paint" }),
    ).toBeTruthy();
    expect(within(section).getByTestId("integration-datadir").textContent).toBe(
      "C:\\Users\\me\\DemoPaint",
    );
    const rows = await within(section).findAllByTestId("integration-backup");
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("写入 3 项");
    expect(rows[1].textContent).toContain("恢复前的备份");
    expect(within(rows[0]).getByRole("button", { name: /恢复/ })).toBeTruthy();
  });

  it("is not there when no profile is bound, or without a provider", () => {
    setup([{ ...demoApp, profiles: [] }]);
    expect(screen.queryByTestId("integration-section-demoapp")).toBeNull();
    cleanup();
    render(<IntegrationSections language="zh-CN" />);
    expect(screen.queryByRole("heading")).toBeNull();
  });

  it("says why an app cannot be used", () => {
    setup([
      { ...demoApp, found: false, problem: "notFound", dataDir: undefined },
    ]);
    const section = screen.getByTestId("integration-section-demoapp");
    expect(section.textContent).toContain("没有找到 Demo Paint 的设置文件夹。");
    expect(within(section).getByTestId("integration-datadir").textContent).toBe(
      "未找到",
    );
  });

  it("restores a backup after the user confirms, and the app is closed", async () => {
    const { api } = setup();
    const rows = await screen.findAllByTestId("integration-backup");
    fireEvent.click(within(rows[1]).getByRole("button", { name: /恢复/ }));
    const confirm = screen.getByRole("dialog", { name: "恢复这份备份？" });
    expect(api.restoreIntegrationBackup).not.toHaveBeenCalled();
    api.listIntegrations.mockImplementation(async () => [
      { ...demoApp, running: true },
    ]);
    fireEvent.click(within(confirm).getByRole("button", { name: "恢复" }));
    const blocking = await screen.findByRole("dialog", {
      name: "请先关闭 Demo Paint",
    });
    api.listIntegrations.mockImplementation(async () => [demoApp]);
    fireEvent.click(within(blocking).getByRole("button", { name: "重试" }));
    await waitFor(() =>
      expect(api.restoreIntegrationBackup).toHaveBeenCalledWith(
        "demoapp",
        "b1",
      ),
    );
    expect((await screen.findByText(/已恢复到/)).textContent).toContain("2026");
  });

  it("says a failed restore in a sentence", async () => {
    const { api } = setup();
    const rows = await screen.findAllByTestId("integration-backup");
    fireEvent.click(within(rows[0]).getByRole("button", { name: /恢复/ }));
    api.restoreIntegrationBackup.mockRejectedValueOnce(
      Object.assign(new Error("x"), { code: -32030 }),
    );
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "恢复这份备份？" })).getByRole(
        "button",
        { name: "恢复" },
      ),
    );
    expect((await screen.findByRole("alert")).textContent).toContain(
      "什么都没有写入",
    );
  });

  it("changes the data folder through the main process and reads the list again", async () => {
    const { api } = setup([{ ...demoApp, found: false, problem: "notFound" }]);
    const section = screen.getByTestId("integration-section-demoapp");
    expect(
      within(section).queryByRole("button", { name: "恢复自动查找" }),
    ).toBeNull();
    api.chooseIntegrationDataDir.mockResolvedValueOnce("changed");
    const before = api.listIntegrations.mock.calls.length;
    fireEvent.click(within(section).getByRole("button", { name: "更换…" }));
    await waitFor(() =>
      expect(api.chooseIntegrationDataDir).toHaveBeenCalledWith("demoapp"),
    );
    await waitFor(() =>
      expect(api.listIntegrations.mock.calls.length).toBeGreaterThan(before),
    );
  });

  it("does nothing when the dialog is closed, and says a folder that is not the app's", async () => {
    const { api } = setup();
    const section = screen.getByTestId("integration-section-demoapp");
    fireEvent.click(within(section).getByRole("button", { name: "更换…" }));
    await waitFor(() =>
      expect(api.chooseIntegrationDataDir).toHaveBeenCalled(),
    );
    await new Promise((resolve) => setTimeout(resolve, 20));
    const before = api.listIntegrations.mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(api.listIntegrations.mock.calls.length).toBe(before);
    expect(within(section).queryByRole("alert")).toBeNull();
    api.chooseIntegrationDataDir.mockResolvedValueOnce("notAppFolder");
    fireEvent.click(within(section).getByRole("button", { name: "更换…" }));
    expect((await within(section).findByRole("alert")).textContent).toBe(
      "这个文件夹里没有 Demo Paint 的设置文件。",
    );
    api.chooseIntegrationDataDir.mockRejectedValueOnce(
      Object.assign(new Error("x"), { code: -32030 }),
    );
    fireEvent.click(within(section).getByRole("button", { name: "更换…" }));
    await waitFor(() =>
      expect(within(section).getByRole("alert").textContent).toContain(
        "什么都没有写入",
      ),
    );
  });

  it("offers to find the folder automatically again only while one is chosen", async () => {
    const { api } = setup([{ ...demoApp, custom: true }]);
    const section = screen.getByTestId("integration-section-demoapp");
    const before = api.listIntegrations.mock.calls.length;
    fireEvent.click(
      within(section).getByRole("button", { name: "恢复自动查找" }),
    );
    await waitFor(() =>
      expect(api.resetIntegrationDataDir).toHaveBeenCalledWith("demoapp"),
    );
    await waitFor(() =>
      expect(api.listIntegrations.mock.calls.length).toBeGreaterThan(before),
    );
  });
});
