// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DaemonEvent } from "../../../shared/api";
import type {
  JoyCon2Known,
  JoyCon2Link,
  JoyCon2Status,
} from "../../../shared/joycon2";
import type { Language } from "../../state/store";
import type { JoyCon2Api } from "./joycon2-model";
import { JoyCon2Section } from "./SettingsJoyCon2";

// Settings · Joy-Con 2: the switch, the remembered controllers, the dialog
// that connects a new one and the one that forgets.

const left: JoyCon2Known = {
  addr: "C8:48:05:FA:01:01",
  serial: "FAKEJC2L0001",
  side: "L",
  connected: true,
};
const right: JoyCon2Known = {
  addr: "C8:48:05:FA:01:02",
  serial: "FAKEJC2R0001",
  side: "R",
  connected: false,
};

const state = (change: Partial<JoyCon2Status> = {}): JoyCon2Status => ({
  available: true,
  autoConnect: true,
  windowOpen: false,
  windowLeftMs: 0,
  known: [],
  links: [],
  ...change,
});
const open = (change: Partial<JoyCon2Status> = {}) =>
  state({ windowOpen: true, windowLeftMs: 30000, ...change });
const link = (known: JoyCon2Known): JoyCon2Link => ({
  addr: known.addr,
  serial: known.serial,
  side: known.side,
  state: "connected",
  known: true,
});

function setup({
  first = state({ known: [left, right] }),
  language = "zh-CN",
  autoConnect = true,
  disabled = false,
}: {
  first?: JoyCon2Status | Error;
  language?: Language;
  autoConnect?: boolean;
  disabled?: boolean;
} = {}) {
  const listeners = new Set<(event: DaemonEvent) => void>();
  const api = {
    getJoyCon2Status: vi.fn(async () => {
      if (first instanceof Error) throw first;
      return first;
    }),
    watchJoyCon2: vi.fn(async (_on: boolean) => {}),
    pairJoyCon2: vi.fn(async () => open()),
    cancelJoyCon2Pair: vi.fn(async () => state()),
    forgetJoyCon2: vi.fn(async (_serial: string) => state()),
    onEvent: vi.fn((listener: (event: DaemonEvent) => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    }),
  } satisfies JoyCon2Api;
  const onAutoConnect = vi.fn();
  const view = render(
    <JoyCon2Section
      api={api}
      language={language}
      autoConnect={autoConnect}
      disabled={disabled}
      onAutoConnect={onAutoConnect}
    />,
  );
  return {
    api,
    onAutoConnect,
    view,
    listeners,
    /** The daemon pushes its state. */
    emit: (data: unknown) =>
      act(() => {
        for (const listener of listeners)
          listener({ topic: "joycon2.changed", data });
      }),
  };
}

const section = () => screen.getByTestId("joycon2-settings");
const connectButton = () =>
  within(section()).getByRole("button", { name: "连接新的 Joy-Con 2" });
const rowOf = (title: string) =>
  within(section())
    .getByText(title, { selector: ".row-title" })
    .closest(".row") as HTMLElement;

/** Waits until the section has read the state and asked for its events. */
async function ready(app: ReturnType<typeof setup>) {
  await screen.findByTestId("joycon2-settings");
  await waitFor(() =>
    expect(app.api.getJoyCon2Status).toHaveBeenCalledTimes(2),
  );
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("the section is there only when the daemon has Joy-Con 2", () => {
  it("is not shown, and asks for no events, when the daemon says it is unavailable", async () => {
    const app = setup({ first: state({ available: false }) });
    await waitFor(() => expect(app.api.getJoyCon2Status).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByTestId("joycon2-settings")).toBeNull();
    expect(screen.queryByText("Joy-Con 2")).toBeNull();
    expect(app.api.watchJoyCon2).not.toHaveBeenCalled();
    expect(app.api.onEvent).not.toHaveBeenCalled();
    app.view.unmount();
    expect(app.api.watchJoyCon2).not.toHaveBeenCalled();
  });

  it("is not shown when the state cannot be read (a daemon from before Joy-Con 2)", async () => {
    const app = setup({ first: new Error("method not found") });
    await waitFor(() => expect(app.api.getJoyCon2Status).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByTestId("joycon2-settings")).toBeNull();
    expect(app.api.watchJoyCon2).not.toHaveBeenCalled();
  });

  it("goes when an event says the daemon has lost it", async () => {
    const app = setup();
    await ready(app);
    app.emit(state({ available: false }));
    expect(screen.queryByTestId("joycon2-settings")).toBeNull();
  });
});

describe("the events of the daemon (joycon2.changed)", () => {
  it("are asked for after the first read, only while the section is up", async () => {
    const app = setup();
    await ready(app);
    expect(app.api.watchJoyCon2).toHaveBeenCalledOnce();
    expect(app.api.watchJoyCon2).toHaveBeenCalledWith(true);
    // The subscription first, then the state again: a change in between is not lost.
    expect(app.api.watchJoyCon2.mock.invocationCallOrder[0]).toBeLessThan(
      app.api.getJoyCon2Status.mock.invocationCallOrder[1],
    );
    expect(app.listeners.size).toBe(1);
    app.view.unmount();
    expect(app.api.watchJoyCon2).toHaveBeenLastCalledWith(false);
    expect(app.listeners.size).toBe(0);
  });

  it("move the rows, and other topics and malformed data do nothing", async () => {
    const app = setup({ first: state({ known: [left] }) });
    await ready(app);
    expect(within(section()).queryByText("FAKEJC2R0001")).toBeNull();
    app.emit(state({ known: [left, right] }));
    expect(within(section()).getByText("FAKEJC2R0001")).toBeTruthy();
    app.emit({ available: "yes" });
    app.emit(null);
    expect(within(section()).getByText("FAKEJC2R0001")).toBeTruthy();
    act(() => {
      for (const listener of app.listeners)
        listener({ topic: "log", data: state({ known: [] }) });
    });
    expect(within(section()).getByText("FAKEJC2R0001")).toBeTruthy();
  });

  it("still show the state that was read when the daemon will not take the subscription", async () => {
    const app = setup();
    app.api.watchJoyCon2.mockRejectedValue(new Error("unknown topic"));
    await screen.findByTestId("joycon2-settings");
    await waitFor(() => expect(app.api.watchJoyCon2).toHaveBeenCalled());
    expect(within(section()).getByText("FAKEJC2L0001")).toBeTruthy();
  });
});

describe("the switch and the remembered controllers", () => {
  it("has the switch of config.json's joycon2.autoConnect, and no explanation under it", async () => {
    const app = setup({ autoConnect: false });
    await ready(app);
    expect(
      within(section()).getByRole("heading", { name: "Joy-Con 2" }),
    ).toBeTruthy();
    const sw = within(section()).getByRole("switch", {
      name: "自动连接记住的 Joy-Con 2",
    });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    expect(
      (sw.closest(".row") as HTMLElement).querySelector(".row-hint"),
    ).toBeNull();
    fireEvent.click(sw);
    expect(app.onAutoConnect).toHaveBeenCalledWith(true);
  });

  it("turns off when the page cannot write config.json", async () => {
    const app = setup({ disabled: true });
    await ready(app);
    expect(
      (
        within(section()).getByRole("switch", {
          name: "自动连接记住的 Joy-Con 2",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    // The rest does not depend on config.json.
    expect((connectButton() as HTMLButtonElement).disabled).toBe(false);
  });

  it("lists each one with its side, serial number and whether it is connected", async () => {
    const app = setup();
    await ready(app);
    const first = rowOf("Joy-Con 2（左）");
    expect(within(first).getByText("FAKEJC2L0001")).toBeTruthy();
    expect(within(first).getByText("已连接")).toBeTruthy();
    expect(
      within(first).getByRole("button", { name: /^忘记 / }).textContent,
    ).toBe("忘记");
    const second = rowOf("Joy-Con 2（右）");
    expect(within(second).getByText("FAKEJC2R0001")).toBeTruthy();
    expect(within(second).getByText("未连接")).toBeTruthy();
  });

  it("follows a controller that connects and one that goes", async () => {
    const app = setup({ first: state({ known: [{ ...right }] }) });
    await ready(app);
    expect(within(rowOf("Joy-Con 2（右）")).getByText("未连接")).toBeTruthy();
    app.emit(state({ known: [{ ...right, connected: true }] }));
    expect(within(rowOf("Joy-Con 2（右）")).getByText("已连接")).toBeTruthy();
    app.emit(state({ known: [] }));
    expect(within(section()).queryByText("FAKEJC2R0001")).toBeNull();
  });

  it("says the same in English", async () => {
    const app = setup({ language: "en" });
    await ready(app);
    expect(
      within(section()).getByRole("switch", {
        name: "Connect remembered Joy-Con 2 automatically",
      }),
    ).toBeTruthy();
    expect(
      within(rowOf("Joy-Con 2 (left)")).getByText("Connected"),
    ).toBeTruthy();
    expect(
      within(rowOf("Joy-Con 2 (right)")).getByText("Not connected"),
    ).toBeTruthy();
    expect(
      within(section()).getByRole("button", {
        name: "Connect a new Joy-Con 2",
      }),
    ).toBeTruthy();
  });
});

describe("forgetting", () => {
  const forgetButton = (title: string) =>
    within(rowOf(title)).getByRole("button", { name: /^忘记 / });

  it("asks in a dialog first, and nothing happens on cancel", async () => {
    const app = setup();
    await ready(app);
    fireEvent.click(forgetButton("Joy-Con 2（左）"));
    const dialog = screen.getByRole("dialog", {
      name: "忘记 Joy-Con 2（左）？",
    });
    expect(within(dialog).queryByText(/忘记后/)).toBeNull();
    expect(app.api.forgetJoyCon2).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(app.api.forgetJoyCon2).not.toHaveBeenCalled();
    expect(within(section()).getByText("FAKEJC2L0001")).toBeTruthy();
  });

  it("forgets the controller by its serial number when confirmed, and the row goes", async () => {
    const app = setup();
    await ready(app);
    app.api.forgetJoyCon2.mockResolvedValue(state({ known: [right] }));
    fireEvent.click(forgetButton("Joy-Con 2（左）"));
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "忘记" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(app.api.forgetJoyCon2).toHaveBeenCalledOnce();
    expect(app.api.forgetJoyCon2).toHaveBeenCalledWith("FAKEJC2L0001");
    expect(within(section()).queryByText("FAKEJC2L0001")).toBeNull();
    expect(within(section()).getByText("FAKEJC2R0001")).toBeTruthy();
  });

  it("stays open with one line when it fails, and closes when the controller was gone already", async () => {
    const app = setup();
    await ready(app);
    app.api.forgetJoyCon2.mockRejectedValueOnce(
      Object.assign(new Error("Joy-Con 2 not forgotten"), { code: -32027 }),
    );
    fireEvent.click(forgetButton("Joy-Con 2（右）"));
    const dialog = screen.getByRole("dialog", {
      name: "忘记 Joy-Con 2（右）？",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "忘记" }));
    const alert = await within(dialog).findByRole("alert");
    expect(alert.textContent).toContain("未能忘记，请稍后再试。");
    expect(screen.getByRole("dialog")).toBe(dialog);

    app.api.forgetJoyCon2.mockRejectedValueOnce(
      Object.assign(new Error("gone"), { code: -32004 }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "忘记" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("asks in English too", async () => {
    const app = setup({ language: "en" });
    await ready(app);
    fireEvent.click(
      within(rowOf("Joy-Con 2 (left)")).getByRole("button", {
        name: /^Forget /,
      }),
    );
    const dialog = screen.getByRole("dialog", {
      name: "Forget the Joy-Con 2 (left)?",
    });
    expect(within(dialog).getByRole("button", { name: "Forget" })).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeTruthy();
  });
});

describe("connecting a new Joy-Con 2", () => {
  const dialog = () =>
    screen.getByRole("dialog", { name: "连接新的 Joy-Con 2" });
  const advance = (ms: number) => act(() => vi.advanceTimersByTime(ms));

  async function begin(app: ReturnType<typeof setup>) {
    await ready(app);
    fireEvent.click(connectButton());
    await waitFor(() => expect(app.api.pairJoyCon2).toHaveBeenCalled());
    await screen.findByText("请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。");
  }

  it("opens the 30 second window and shows what to do, with the seconds left and 取消", async () => {
    const app = setup({ first: state() });
    await begin(app);
    expect(app.api.pairJoyCon2).toHaveBeenCalledOnce();
    expect(
      within(dialog()).getByText("请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。"),
    ).toBeTruthy();
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 30 秒");
    expect(within(dialog()).getByRole("button", { name: "取消" })).toBeTruthy();
    // One round at a time: the button is off while the dialog is up.
    expect((connectButton() as HTMLButtonElement).disabled).toBe(true);
  });

  it("counts the seconds down, from the time the daemon says is left", async () => {
    const app = setup({ first: state() });
    app.api.pairJoyCon2.mockResolvedValue(open({ windowLeftMs: 20000 }));
    await begin(app);
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 20 秒");
    advance(5000);
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 15 秒");
    advance(14000);
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 1 秒");
    advance(5000);
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 0 秒");
  });

  it("closes itself with 已连接并记住 Joy-Con 2（左）。 when a controller is remembered", async () => {
    const app = setup({ first: state() });
    await begin(app);
    // The window closes with the controller remembered and connected.
    app.emit(state({ known: [left], links: [link(left)] }));
    const result = within(dialog()).getByText("已连接并记住 Joy-Con 2（左）。");
    expect(result.getAttribute("role")).toBe("status");
    expect(within(dialog()).queryByRole("timer")).toBeNull();
    expect(within(dialog()).queryByRole("button", { name: "取消" })).toBeNull();
    // The list has it behind the dialog.
    expect(within(section()).getByText("FAKEJC2L0001")).toBeTruthy();
    fireEvent.click(within(dialog()).getByRole("button", { name: "确定" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect((connectButton() as HTMLButtonElement).disabled).toBe(false);
    // Nothing is asked of the daemon to close a window that closed itself.
    expect(app.api.cancelJoyCon2Pair).not.toHaveBeenCalled();
  });

  it("says （右） for a right one", async () => {
    const app = setup({ first: state({ known: [left] }) });
    await begin(app);
    app.emit(state({ known: [left, { ...right, connected: true }] }));
    expect(screen.getByText("已连接并记住 Joy-Con 2（右）。")).toBeTruthy();
  });

  it("says so when a controller that was remembered connects in the window", async () => {
    const app = setup({
      first: state({ known: [{ ...left, connected: false }] }),
    });
    await begin(app);
    app.emit(state({ known: [left], links: [link(left)] }));
    expect(screen.getByText("已连接并记住 Joy-Con 2（左）。")).toBeTruthy();
  });

  it("says 30 秒内没有找到 Joy-Con 2。 when the window ends without one", async () => {
    const app = setup({ first: state({ known: [left] }) });
    await begin(app);
    app.emit(state({ known: [left] }));
    expect(
      within(dialog()).getByText("30 秒内没有找到 Joy-Con 2。"),
    ).toBeTruthy();
    expect(within(dialog()).queryByRole("timer")).toBeNull();
    fireEvent.click(within(dialog()).getByRole("button", { name: "确定" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does not take a state from before the window opened for its end", async () => {
    const app = setup({ first: state() });
    let finish: (status: JoyCon2Status) => void = () => {};
    app.api.pairJoyCon2.mockImplementation(
      () => new Promise<JoyCon2Status>((resolve) => (finish = resolve)),
    );
    await ready(app);
    fireEvent.click(connectButton());
    await screen.findByText("请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。");
    // An event of the daemon from before the window: it is closed.
    app.emit(state());
    expect(within(dialog()).getByRole("timer")).toBeTruthy();
    await act(async () => finish(open()));
    expect(within(dialog()).getByRole("timer").textContent).toBe("还剩 30 秒");
    expect(
      within(dialog()).queryByText("30 秒内没有找到 Joy-Con 2。"),
    ).toBeNull();
  });

  it("cancels with 取消: the dialog goes and the window is closed", async () => {
    const app = setup({ first: state() });
    await begin(app);
    fireEvent.click(within(dialog()).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(app.api.cancelJoyCon2Pair).toHaveBeenCalledOnce();
    // The window's end that follows opens nothing again.
    app.emit(state());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect((connectButton() as HTMLButtonElement).disabled).toBe(false);
  });

  it("cancels with Esc and with the close button as well", async () => {
    const app = setup({ first: state() });
    await begin(app);
    fireEvent(dialog(), new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(app.api.cancelJoyCon2Pair).toHaveBeenCalledOnce();

    fireEvent.click(connectButton());
    await screen.findByText("请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。");
    fireEvent.click(
      within(dialog()).getByRole("button", { name: "取消连接 Joy-Con 2" }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(app.api.cancelJoyCon2Pair).toHaveBeenCalledTimes(2);
  });

  it("says it could not begin when the daemon does not open the window", async () => {
    const app = setup({ first: state() });
    app.api.pairJoyCon2.mockRejectedValue(
      Object.assign(new Error("Joy-Con 2 window not opened"), { code: -32027 }),
    );
    await ready(app);
    fireEvent.click(connectButton());
    await within(dialog()).findByText("未能开始连接 Joy-Con 2。");
    expect(within(dialog()).queryByRole("timer")).toBeNull();
  });

  it("goes with its dialog when the daemon loses Joy-Con 2 meanwhile", async () => {
    const app = setup({ first: state() });
    await begin(app);
    app.emit(state({ available: false }));
    // The section goes with its dialog.
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByTestId("joycon2-settings")).toBeNull();
  });

  it("closes the window when the page goes away with it open, and not otherwise", async () => {
    const app = setup({ first: state() });
    await begin(app);
    app.view.unmount();
    expect(app.api.cancelJoyCon2Pair).toHaveBeenCalledOnce();
    cleanup();

    const other = setup({ first: state() });
    await ready(other);
    other.view.unmount();
    expect(other.api.cancelJoyCon2Pair).not.toHaveBeenCalled();
  });

  it("is in English too", async () => {
    const app = setup({ first: state(), language: "en" });
    await ready(app);
    fireEvent.click(
      within(section()).getByRole("button", {
        name: "Connect a new Joy-Con 2",
      }),
    );
    const english = await screen.findByRole("dialog", {
      name: "Connect a new Joy-Con 2",
    });
    expect(
      within(english).getByText(
        "Hold the SYNC button on the side of the Joy-Con 2 within 30 seconds.",
      ),
    ).toBeTruthy();
    expect(within(english).getByRole("timer").textContent).toBe("30 s left");
    app.emit(state({ known: [left] }));
    expect(
      within(english).getByText(
        "Connected and remembered the Joy-Con 2 (left).",
      ),
    ).toBeTruthy();
    fireEvent.click(within(english).getByRole("button", { name: "OK" }));
    fireEvent.click(
      within(section()).getByRole("button", {
        name: "Connect a new Joy-Con 2",
      }),
    );
    await screen.findByRole("dialog", { name: "Connect a new Joy-Con 2" });
    app.emit(state({ known: [left] }));
    expect(
      screen.getByText("No Joy-Con 2 found within 30 seconds."),
    ).toBeTruthy();
  });
});
