// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ConfigRepair } from "../shared/api";
import { ConfigRepairDialog } from "./ConfigRepairDialog";

afterEach(cleanup);

const repair: ConfigRepair = {
  backup: "config.broken-20261015-191600.json",
  path: "C:\\data\\config.broken-20261015-191600.json",
  reset: false,
};

function setup(
  options: {
    repair?: ConfigRepair | null;
    language?: "zh-CN" | "en";
    show?: () => Promise<void>;
  } = {},
) {
  const showConfigBackup = vi.fn(options.show ?? (async () => {}));
  const onConfirm = vi.fn();
  const view = render(
    <ConfigRepairDialog
      repair={options.repair === undefined ? repair : options.repair}
      language={options.language ?? "zh-CN"}
      api={{ showConfigBackup }}
      onConfirm={onConfirm}
    />,
  );
  return { showConfigBackup, onConfirm, ...view };
}

describe("the repaired settings file dialog (PRO-29)", () => {
  it("shows nothing without a repair", () => {
    setup({ repair: null });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("says what happened, names the backup and offers only 确定 and 打开文件夹", () => {
    setup();
    const dialog = screen.getByRole("dialog", { name: "设置已恢复" });
    expect(dialog.textContent).toContain(
      "设置文件有误，已恢复默认设置。原文件已备份为 config.broken-20261015-191600.json。",
    );
    // A modal dialog, not a banner: the page behind takes no clicks.
    expect(dialog.tagName).toBe("DIALOG");
    expect(
      [...dialog.querySelectorAll(".dialog-footer button")].map(
        (button) => button.textContent,
      ),
    ).toEqual(["打开文件夹", "确定"]);
    // The primary choice has focus.
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "确定" }),
    );
  });

  it("selects the backup without closing, so 确定 can still be pressed", async () => {
    const { showConfigBackup, onConfirm } = setup();
    fireEvent.click(screen.getByRole("button", { name: "打开文件夹" }));
    expect(showConfigBackup).toHaveBeenCalledWith(repair.backup);
    await Promise.resolve();
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("says so when the folder cannot be opened, and stays open", async () => {
    setup({
      show: async () => {
        throw new Error("denied");
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "打开文件夹" }));
    expect((await screen.findByRole("alert")).textContent).toBe(
      "无法打开文件夹。",
    );
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it.each([
    [
      "确定",
      (dialog: HTMLElement) => {
        fireEvent.click(
          [...dialog.querySelectorAll("button")].find(
            (button) => button.textContent === "确定",
          ) as HTMLElement,
        );
      },
    ],
    [
      "the close button",
      (dialog: HTMLElement) => {
        fireEvent.click(
          dialog.querySelector(".dialog-header button") as HTMLElement,
        );
      },
    ],
    [
      "Esc",
      (dialog: HTMLElement) => {
        fireEvent(dialog, new Event("cancel", { cancelable: true }));
      },
    ],
  ])("confirms once with %s", (_name, act) => {
    const { onConfirm } = setup();
    act(screen.getByRole("dialog"));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("speaks English when the language is", () => {
    setup({ language: "en" });
    const dialog = screen.getByRole("dialog", { name: "Settings reset" });
    expect(dialog.textContent).toContain(
      "The settings file had errors and was reset to defaults. The original was saved as config.broken-20261015-191600.json.",
    );
    expect(screen.getByRole("button", { name: "OK" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open folder" })).toBeTruthy();
  });
});
