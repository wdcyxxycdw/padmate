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
import { initialUpdateState, type UpdateState } from "../../shared/update";
import { supportedUpdateState, updateApi } from "../update-api-fixture";
import { UpdatesSection } from "./SettingsUpdates";

afterEach(cleanup);

async function setup(
  state: UpdateState = supportedUpdateState,
  language: "zh-CN" | "en" = "zh-CN",
) {
  const fake = updateApi(state);
  render(<UpdatesSection api={fake.api} language={language} />);
  await waitFor(() => expect(fake.api.getUpdateState).toHaveBeenCalled());
  return fake;
}

const section = () =>
  screen.getByRole("heading", { name: "更新" }).parentElement as HTMLElement;

describe("Settings · 更新 (PKG-10)", () => {
  it("has the switch and the button, and no note under the switch", async () => {
    await setup();
    const card = section();
    const sw = within(card).getByRole("switch", { name: "自动检查更新" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect(
      (
        within(card).getByRole("button", {
          name: "检查更新",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    // ADR-0077: the switch needs no explaining, and the page names no host
    // (the user guide says where a check goes, ADR-0078).
    expect(card.textContent).not.toContain("GitHub");
    const row = sw.closest(".row") as HTMLElement;
    expect(row.querySelector(".row-hint")).toBeNull();
  });

  it("says the same in English", async () => {
    await setup(supportedUpdateState, "en");
    const card = screen.getByRole("heading", { name: "Updates" })
      .parentElement as HTMLElement;
    expect(
      within(card).getByRole("switch", {
        name: "Check for updates automatically",
      }),
    ).toBeTruthy();
    expect(
      within(card).getByRole("button", { name: "Check for updates" }),
    ).toBeTruthy();
    expect(card.textContent).not.toContain("GitHub");
  });

  it("shows the switch as Main has it, and changes it through Main", async () => {
    const { api, push } = await setup({
      ...supportedUpdateState,
      autoCheck: false,
    });
    const sw = within(section()).getByRole("switch", {
      name: "自动检查更新",
    });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(sw);
    expect(api.setAutoUpdate).toHaveBeenCalledExactlyOnceWith(true);
    push({ autoCheck: true });
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
  });

  it("checks when the button is pressed", async () => {
    const { api, push } = await setup();
    fireEvent.click(
      within(section()).getByRole("button", { name: "检查更新" }),
    );
    expect(api.checkForUpdate).toHaveBeenCalledOnce();
    push({ phase: "checking" });
    const button = await within(section()).findByRole("button", {
      name: "正在检查…",
    });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it("tells a download that goes on, and one that waits to be installed", async () => {
    const { push } = await setup();
    push({ phase: "downloading", version: "1.0.1", percent: 42 });
    expect(
      await within(section()).findByText("正在下载 1.0.1… 42%"),
    ).toBeTruthy();
    // The button shows the dialog again; it asks nothing of the network.
    expect(
      (
        within(section()).getByRole("button", {
          name: "检查更新",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    push({ phase: "downloaded", percent: 100 });
    expect(
      await within(section()).findByText("1.0.1 已下载，尚未安装"),
    ).toBeTruthy();
  });

  it("turns both controls off, and says why, in a build that cannot update", async () => {
    await setup(initialUpdateState);
    const card = section();
    expect(
      (
        within(card).getByRole("switch", {
          name: "自动检查更新",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (
        within(card).getByRole("button", {
          name: "检查更新",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(card.textContent).toContain("开发版不检查更新");
  });

  it("does not wait for the mapping service: it needs no daemon and no settings file", async () => {
    // The component takes the update API alone; the switch is never gated on
    // the daemon's config.json (the state is Main's, in updates.json).
    const { api } = await setup();
    expect(Object.keys(api)).not.toContain("saveSettings");
    expect(
      (
        within(section()).getByRole("switch", {
          name: "自动检查更新",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
});
