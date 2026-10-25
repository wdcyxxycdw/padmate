// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  initialUpdateState,
  type UpdateDialogKind,
  type UpdateState,
} from "../shared/update";
import { UpdateDialogs } from "./UpdateDialogs";
import { supportedUpdateState, updateApi } from "./update-api-fixture";

afterEach(cleanup);

const supported = supportedUpdateState;

async function show(
  dialog: UpdateDialogKind,
  fields: Partial<UpdateState> = {},
  language: "zh-CN" | "en" = "zh-CN",
) {
  const fake = updateApi({ ...supported, dialog, ...fields });
  render(<UpdateDialogs api={fake.api} language={language} />);
  // The state is read when the dialogs mount.
  await waitFor(() => expect(fake.api.getUpdateState).toHaveBeenCalled());
  return fake;
}

const buttons = () =>
  [...document.querySelectorAll("dialog[open] .dialog-footer button")].map(
    (button) => button.textContent,
  );

describe("the update dialogs (PKG-10, ADR-0073)", () => {
  it("show nothing while there is nothing to say", async () => {
    await show("none");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("offer a version with 下载, 以后再说 and 跳过这个版本, and the release page", async () => {
    const { api } = await show("available", {
      phase: "available",
      version: "1.0.1",
    });
    const dialog = await screen.findByRole("dialog", { name: "有新版本" });
    expect(dialog.tagName).toBe("DIALOG");
    expect(dialog.textContent).toContain("padmate 1.0.1 可以更新了。");
    expect(buttons()).toEqual(["跳过这个版本", "以后再说", "下载"]);
    // Main opens this by itself, perhaps while the person types: the next
    // key must not start a download. 以后再说 has focus, not 下载.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "以后再说" }),
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "查看发布页" }));
    expect(api.openReleasePage).toHaveBeenCalledExactlyOnceWith();
    fireEvent.click(screen.getByRole("button", { name: "下载" }));
    expect(api.downloadUpdate).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "以后再说" }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("later");
    fireEvent.click(screen.getByRole("button", { name: "跳过这个版本" }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("skip");
  });

  it("say the same in English", async () => {
    await show("available", { phase: "available", version: "1.0.1" }, "en");
    const dialog = await screen.findByRole("dialog", {
      name: "A new version is available",
    });
    expect(dialog.textContent).toContain("padmate 1.0.1 is ready to download.");
    expect(buttons()).toEqual(["Skip this version", "Later", "Download"]);
    expect(
      screen.getByRole("button", { name: "View release page" }),
    ).toBeTruthy();
  });

  it("treat Esc and × as 以后再说", async () => {
    const { api } = await show("available", {
      phase: "available",
      version: "1.0.1",
    });
    const dialog = await screen.findByRole("dialog", { name: "有新版本" });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("later");
    fireEvent.click(screen.getByRole("button", { name: "关闭" }));
    expect(api.dismissUpdate).toHaveBeenCalledTimes(2);
  });

  it("show the progress of the download, and can be hidden while it goes on", async () => {
    const { api, push } = await show("downloading", {
      phase: "downloading",
      version: "1.0.1",
      percent: 37,
    });
    const dialog = await screen.findByRole("dialog", { name: "正在下载" });
    expect(dialog.textContent).toContain("padmate 1.0.1");
    expect(dialog.textContent).toContain("37%");
    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("37");
    expect(bar.getAttribute("aria-valuemin")).toBe("0");
    expect(bar.getAttribute("aria-valuemax")).toBe("100");
    expect(buttons()).toEqual(["隐藏"]);
    push({ percent: 80 });
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe(
      "80",
    );
    fireEvent.click(screen.getByRole("button", { name: "隐藏" }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("later");
  });

  it("offer the install after the download, and say that mapping stops for a moment", async () => {
    const { api } = await show("downloaded", {
      phase: "downloaded",
      version: "1.0.1",
      percent: 100,
    });
    const dialog = await screen.findByRole("dialog", { name: "下载完成" });
    expect(dialog.textContent).toContain(
      "padmate 1.0.1 已下载。安装时映射会停一会儿，装好后 padmate 会自动重新打开。",
    );
    expect(buttons()).toEqual(["以后再说", "重启并安装"]);
    // The download may end while the person types elsewhere in the window:
    // the next key must not restart and install. 以后再说 has focus.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "以后再说" }),
      ),
    );
    expect(api.installUpdate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "重启并安装" }));
    expect(api.installUpdate).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "以后再说" }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("later");
  });

  it("say the install in English", async () => {
    await show("downloaded", { phase: "downloaded", version: "1.0.1" }, "en");
    const dialog = await screen.findByRole("dialog", {
      name: "Download complete",
    });
    expect(dialog.textContent).toContain(
      "Mapping stops for a moment during the install, and padmate opens again by itself when it is done.",
    );
    expect(buttons()).toEqual(["Later", "Restart and install"]);
  });

  it("cannot be dismissed while installing", async () => {
    const { api } = await show("installing", {
      phase: "installing",
      version: "1.0.1",
    });
    const dialog = await screen.findByRole("dialog", { name: "正在安装" });
    expect(dialog.textContent).toContain("padmate 正在重启并安装 1.0.1…");
    expect(buttons()).toEqual([]);
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(api.dismissUpdate).not.toHaveBeenCalled();
    expect(
      (screen.getByRole("button", { name: "关闭" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });

  it("tell a check that found nothing", async () => {
    const { api } = await show("upToDate");
    const dialog = await screen.findByRole("dialog", { name: "已是最新版本" });
    expect(dialog.textContent).toContain("已是最新版本。");
    expect(buttons()).toEqual(["确定"]);
    fireEvent.click(screen.getByRole("button", { name: "确定" }));
    expect(api.dismissUpdate).toHaveBeenLastCalledWith("later");
  });

  it.each([
    ["checkFailed", "无法检查更新", ["确定"]],
    ["downloadFailed", "无法下载更新", ["确定"]],
    ["installFailed", "无法安装更新", ["确定"]],
  ] as const)(
    "tell that %s, and give the release page",
    async (kind, title, labels) => {
      const { api } = await show(kind);
      const dialog = await screen.findByRole("dialog", { name: title });
      expect(buttons()).toEqual([...labels]);
      // Never the raw code or a message of the feed.
      expect(dialog.textContent).not.toMatch(/ERR_|Error|undefined/);
      fireEvent.click(screen.getByRole("button", { name: "查看发布页" }));
      expect(api.openReleasePage).toHaveBeenCalledOnce();
    },
  );

  it("say the failures in English", async () => {
    await show("checkFailed", {}, "en");
    expect(
      await screen.findByRole("dialog", {
        name: "Could not check for updates",
      }),
    ).toBeTruthy();
    expect(buttons()).toEqual(["OK"]);
    expect(
      screen.getByRole("button", { name: "View release page" }),
    ).toBeTruthy();
  });

  it("follow the state Main pushes, one dialog at a time", async () => {
    const { push } = await show("none");
    expect(screen.queryByRole("dialog")).toBeNull();
    push({ dialog: "available", phase: "available", version: "1.0.2" });
    expect(
      await screen.findByRole("dialog", { name: "有新版本" }),
    ).toBeTruthy();
    push({ dialog: "downloading", phase: "downloading", percent: 5 });
    expect(
      await screen.findByRole("dialog", { name: "正在下载" }),
    ).toBeTruthy();
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    push({ dialog: "none" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("show nothing in a build that cannot update", async () => {
    const fake = updateApi({ ...initialUpdateState });
    render(<UpdateDialogs api={fake.api} language="zh-CN" />);
    await waitFor(() => expect(fake.api.getUpdateState).toHaveBeenCalled());
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
