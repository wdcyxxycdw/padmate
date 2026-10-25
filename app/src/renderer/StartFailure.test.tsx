// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StartFailure as Failure } from "../shared/api";
import { StartFailure } from "./StartFailure";

afterEach(cleanup);

const failure: Failure = {
  reason: "report",
  report: {
    kind: "config",
    cause: "invalid",
    file: "C:\\Users\\me\\AppData\\Roaming\\padmate\\config.json",
    line: 14,
    col: 20,
    pointer: "/safety/panic/standard/buttons",
    message: "minItems: got 1, want 2",
    dataDir: "C:\\Users\\me\\AppData\\Roaming\\padmate",
    errors: [
      {
        line: 14,
        col: 20,
        pointer: "/safety/panic/standard/buttons",
        message: "minItems: got 1, want 2",
      },
      {
        line: 15,
        col: 7,
        pointer: "/safety/panic/standard/holdMs",
        message: "minimum: got 100, want 500",
      },
    ],
  },
};

function api() {
  return {
    openStartFolder: vi.fn(async () => {}),
    retryStart: vi.fn(async () => {}),
    quit: vi.fn(async () => {}),
  };
}

describe("start-failure page (UI-45)", () => {
  it("says which file, where and what is wrong, and focuses 重试", () => {
    render(
      <StartFailure
        api={api()}
        failure={failure}
        retrying={false}
        language="zh-CN"
      />,
    );
    expect(
      screen.getByRole("heading", { name: "映射服务未能启动" }),
    ).toBeTruthy();
    expect(screen.getByTestId("start-summary").textContent).toBe(
      "设置文件 config.json 第 14 行第 20 列：紧急暂停至少需要 2 个按键。",
    );
    expect(screen.getByTestId("start-path").textContent).toBe(
      "C:\\Users\\me\\AppData\\Roaming\\padmate\\config.json",
    );
    expect(screen.getByRole("list", { name: "其他错误" }).textContent).toBe(
      "第 15 行第 7 列：紧急暂停的按住时间不能少于 500 毫秒。",
    );
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "重试" }),
    );
    // Nothing points at the developer documentation.
    expect(document.body.textContent).not.toMatch(/docs|task /);
  });

  it("opens the folder, retries and quits", async () => {
    const calls = api();
    render(
      <StartFailure
        api={calls}
        failure={failure}
        retrying={false}
        language="zh-CN"
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "打开文件夹" }));
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    fireEvent.click(screen.getByRole("button", { name: "退出" }));
    expect(calls.openStartFolder).toHaveBeenCalledOnce();
    expect(calls.retryStart).toHaveBeenCalledOnce();
    expect(calls.quit).toHaveBeenCalledOnce();
  });

  it("says when the folder does not open", async () => {
    const calls = api();
    calls.openStartFolder.mockRejectedValueOnce(new Error("no"));
    render(
      <StartFailure
        api={calls}
        failure={failure}
        retrying={false}
        language="zh-CN"
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "打开文件夹" }));
    expect((await screen.findByRole("alert")).textContent).toBe(
      "无法打开文件夹。",
    );
  });

  it("keeps the failure while retrying, then takes 重试 back", async () => {
    const calls = api();
    const { rerender } = render(
      <StartFailure
        api={calls}
        failure={failure}
        retrying={true}
        language="zh-CN"
      />,
    );
    const retry = screen.getByRole("button", { name: "正在重试…" });
    expect((retry as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("start-summary").textContent).toContain(
      "config.json",
    );
    act(() => (document.activeElement as HTMLElement | null)?.blur());
    rerender(
      <StartFailure
        api={calls}
        failure={{ reason: "timeout" }}
        retrying={false}
        language="zh-CN"
      />,
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "重试" }),
      ),
    );
    expect(screen.getByTestId("start-summary").textContent).toBe(
      "映射服务未响应。",
    );
    expect(screen.queryByTestId("start-path")).toBeNull();
  });

  it("speaks English", () => {
    render(
      <StartFailure
        api={api()}
        failure={failure}
        retrying={false}
        language="en"
      />,
    );
    expect(
      screen.getByRole("heading", {
        name: "The mapping service did not start",
      }),
    ).toBeTruthy();
    for (const name of ["Open folder", "Retry", "Quit"])
      expect(screen.getByRole("button", { name })).toBeTruthy();
  });
});
