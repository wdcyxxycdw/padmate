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
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  DaemonEvent,
  DeviceEntry,
  MappingResult,
  RawInfo,
} from "../../shared/api";
import { editorAPIFixture } from "../editor-api-fixture";
import type { Language } from "../store";
import { MappingWizardDialog } from "./MappingWizardDialog";

// The wizard cannot get raw events from the fake input of the E2E daemon, so
// it is covered here with a fake input.raw stream: the dialog is given an API
// whose onEvent the test drives (IN-19, ADR-0072). The real wizard needs a
// controller (ARCHITECTURE §14).

const guid = "ff00aabb000000000000000000007600";

const pad = (over: Partial<DeviceEntry["info"]> = {}): DeviceEntry => ({
  info: {
    id: 12,
    name: "Generic USB Joystick",
    type: "unknown",
    vendor: 0x0079,
    product: 0x0006,
    version: 0,
    guid,
    driver: "hidapi",
    path: "",
    serial: "",
    gamepad: false,
    virtual: false,
    ...over,
  },
  key: "030000007900/p:x",
  slot: -1,
  status: "unmapped",
  disabled: false,
});

const rawInfo = (over: Partial<RawInfo> = {}): RawInfo => ({
  device: "030000007900/p:x",
  id: 12,
  guid,
  gamepad: false,
  axes: 4,
  buttons: 14,
  hats: 1,
  axisRest: [0, 0, 0, 0],
  watching: true,
  ...over,
});

interface Setup {
  api: ReturnType<typeof makeAPI>;
  refresh: ReturnType<typeof vi.fn>;
  onClose: ReturnType<typeof vi.fn>;
  /** Sends a raw event of the controller, at the test's clock. */
  emit: (kind: "button" | "axis" | "hat", index: number, value: number) => void;
  /** Moves the clock and the timers. */
  pass: (ms: number) => Promise<void>;
  press: (button: number) => void;
  push: (axis: number, value: number, rest?: number) => void;
  prompt: () => string;
}

let now = 0;

function makeAPI() {
  const listeners = new Set<(event: DaemonEvent) => void>();
  const fixture = editorAPIFixture();
  const api = {
    ...fixture,
    onEvent: vi.fn((listener: (event: DaemonEvent) => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    }),
  };
  return Object.assign(api, { listeners });
}

function Host({
  api,
  device,
  language,
  refresh,
  onClose,
}: {
  api: ReturnType<typeof makeAPI>;
  device: DeviceEntry;
  language: Language;
  refresh: () => Promise<void>;
  onClose: () => void;
}) {
  const [open, setOpen] = useState(true);
  return (
    <MappingWizardDialog
      open={open}
      language={language}
      api={api}
      device={device}
      refresh={refresh}
      clock={() => now}
      onClose={() => {
        onClose();
        setOpen(false);
      }}
    />
  );
}

async function setup({
  device = pad(),
  language = "zh-CN",
  info = rawInfo(),
}: {
  device?: DeviceEntry;
  language?: Language;
  info?: RawInfo;
} = {}): Promise<Setup> {
  const api = makeAPI();
  api.rawWatchStart.mockImplementation(async () => info);
  const refresh = vi.fn(async () => {});
  const onClose = vi.fn();
  render(
    <Host
      api={api}
      device={device}
      language={language}
      refresh={refresh}
      onClose={onClose}
    />,
  );
  // Lets the start run: the capture, then the raw watch, then the timer.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
  const emit: Setup["emit"] = (kind, index, value) => {
    act(() => {
      for (const listener of api.listeners)
        listener({
          topic: "input.raw",
          data: { timeNs: now, device: info.id, kind, index, value },
        });
    });
  };
  return {
    api,
    refresh,
    onClose,
    emit,
    pass: async (ms) => {
      now += ms * 1_000_000;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });
    },
    press: (button) => {
      now += 5_000_000;
      emit("button", button, 1);
      now += 5_000_000;
      emit("button", button, 0);
    },
    push: (axis, value, rest = 0) => {
      now += 5_000_000;
      emit("axis", axis, value);
      now += 5_000_000;
      emit("axis", axis, rest);
    },
    prompt: () =>
      screen.getByTestId("wizard-prompt").querySelector("h3")?.textContent ??
      "",
  };
}

/** Opens the dialog and lets the quiet period pass: the first prompt is up. */
async function ready(options?: Parameters<typeof setup>[0]) {
  const s = await setup(options);
  await screen.findByText(/请先别碰手柄|Leave the controller alone/);
  await s.pass(1000);
  return s;
}

beforeEach(() => {
  now = 5_000_000_000;
  vi.useFakeTimers({
    shouldAdvanceTime: true,
    toFake: ["setTimeout", "clearTimeout"],
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("starting the wizard (IN-19)", () => {
  it("pauses the mapping first, then watches the controller by its connection ID", async () => {
    const s = await setup();
    expect(
      await screen.findByRole("dialog", { name: "建立映射" }),
    ).toBeTruthy();
    await waitFor(() => expect(s.api.rawWatchStart).toHaveBeenCalledOnce());
    expect(s.api.rawWatchStart).toHaveBeenCalledWith("12");
    // The capture gate goes before the request, which the daemon refuses for
    // a gamepad that is not captured.
    expect(s.api.padCaptureStart.mock.invocationCallOrder[0]).toBeLessThan(
      s.api.rawWatchStart.mock.invocationCallOrder[0],
    );
    // And it says so.
    expect(screen.getByText(/向导期间，手柄映射暂停/)).toBeTruthy();
    expect(screen.getByText("Generic USB Joystick")).toBeTruthy();
  });

  it("asks for a capture that stays while the window is away (ADR-0076)", async () => {
    const s = await setup();
    await waitFor(() => expect(s.api.rawWatchStart).toHaveBeenCalledOnce());
    expect(s.api.padCaptureStart).toHaveBeenCalledExactlyOnceWith(true);
  });

  it("leaves the controller alone for a second before the first prompt", async () => {
    const s = await setup();
    await screen.findByText("请先别碰手柄，稍等一下…");
    expect(screen.queryByTestId("wizard-prompt")).toBeNull();
    // Nothing can be skipped or redone yet.
    expect(
      (screen.getByRole("button", { name: "跳过" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    await s.pass(999);
    expect(screen.queryByTestId("wizard-prompt")).toBeNull();
    await s.pass(1);
    expect(s.prompt()).toBe("按下 A 键");
    expect(screen.getByText("1 / 25")).toBeTruthy();
  });

  it("is a remap for a controller SDL knows", async () => {
    await setup({ device: pad({ gamepad: true, type: "xboxone" }) });
    expect(
      await screen.findByRole("dialog", { name: "重新映射" }),
    ).toBeTruthy();
  });

  it("says so, and ends, when the controller's events cannot be had", async () => {
    const failing = makeAPI();
    failing.rawWatchStart.mockRejectedValue(
      Object.assign(new Error("Raw events not started"), { code: -32026 }),
    );
    render(
      <Host
        api={failing}
        device={pad()}
        language="zh-CN"
        refresh={async () => {}}
        onClose={() => {}}
      />,
    );
    expect(
      await screen.findByText("无法读取这只手柄的原始输入。"),
    ).toBeTruthy();
    // Nothing is left on: the watch and the capture are ended at once.
    await waitFor(() => expect(failing.padCaptureStop).toHaveBeenCalled());
    expect(failing.rawWatchStop).toHaveBeenCalledWith("12");
    // The only thing to do is to close it.
    expect(screen.queryByRole("button", { name: "保存" })).toBeNull();
    expect(screen.getByRole("button", { name: "关闭" })).toBeTruthy();
  });

  it("says when the capture cannot be had", async () => {
    const failing = makeAPI();
    failing.padCaptureStart.mockRejectedValue(
      new Error("Controller capture requires the focused padmate window"),
    );
    render(
      <Host
        api={failing}
        device={pad()}
        language="en"
        refresh={async () => {}}
        onClose={() => {}}
      />,
    );
    expect(
      await screen.findByText(
        "The controller's raw input could not be read. See Settings → Logs.",
      ),
    ).toBeTruthy();
    expect(failing.rawWatchStart).not.toHaveBeenCalled();
  });
});

describe("answering the prompts", () => {
  it("binds a button when it is released, and shows what it saw", async () => {
    const s = await ready();
    s.emit("button", 3, 1);
    expect(screen.getByText("检测到：按钮 3")).toBeTruthy();
    // Still the first prompt: a press alone is not an answer.
    expect(s.prompt()).toBe("按下 A 键");
    s.emit("button", 3, 0);
    expect(s.prompt()).toBe("按下 B 键");
    expect(screen.getByText("已记录 A：按钮 3")).toBeTruthy();
    expect(screen.getByText("2 / 25")).toBeTruthy();
    // The list shows what each control got.
    const list = screen.getByRole("list", { name: "已记录的控件" });
    const a = within(list).getByText("A").closest("li") as HTMLElement;
    expect(a.textContent).toContain("按钮 3");
    expect(
      within(list).getByText("B").closest("li")?.getAttribute("aria-current"),
    ).toBe("step");
  });

  it("shows an axis that is pushed out and counts it when it is back", async () => {
    const s = await ready();
    // Get to the left stick's left: A B X Y and the d-pad are skipped.
    for (let i = 0; i < 8; i++)
      fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("把左摇杆推到最左，再放回中间");
    s.emit("axis", 0, -30000);
    expect(screen.getByText("检测到：轴 0 −")).toBeTruthy();
    s.emit("axis", 0, 0);
    expect(s.prompt()).toBe("把左摇杆推到最右，再放回中间");
    expect(screen.getByText("已记录 左摇杆向左：轴 0 −")).toBeTruthy();
  });

  it("ignores an axis that moves by itself during the first second, and says so", async () => {
    const s = await setup();
    await screen.findByText("请先别碰手柄，稍等一下…");
    for (let i = 0; i < 6; i++) {
      now += 10_000_000;
      s.emit("axis", 2, i % 2 === 0 ? 20000 : -20000);
    }
    await s.pass(1000);
    expect(screen.getByText("轴 2 在自己抖动，先不算。")).toBeTruthy();
  });

  it("rejects a hat on a diagonal and asks again", async () => {
    const s = await ready();
    for (let i = 0; i < 4; i++)
      fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("按下十字键的上");
    s.emit("hat", 0, 3);
    expect(
      screen.getByText("十字键只能按一个方向。请松开后再按一次。"),
    ).toBeTruthy();
    expect(s.prompt()).toBe("按下十字键的上");
    s.emit("hat", 0, 0);
    s.emit("hat", 0, 1);
    expect(s.prompt()).toBe("按下十字键的下");
  });

  it("tells when a button was already given to an earlier control", async () => {
    const s = await ready();
    s.press(3);
    s.press(3);
    expect(s.prompt()).toBe("按下 X 键");
    expect(
      screen.getByText("已记录 B：按钮 3（与 A 相同，A 的记录已清除）"),
    ).toBeTruthy();
    const list = screen.getByRole("list", { name: "已记录的控件" });
    expect(
      within(list).getByText("A").closest("li")?.textContent,
    ).not.toContain("按钮 3");
  });

  it("skips, goes back and does a step again", async () => {
    const s = await ready();
    const back = screen.getByRole("button", {
      name: "上一步",
    }) as HTMLButtonElement;
    expect(back.disabled).toBe(true);
    s.press(1);
    expect(s.prompt()).toBe("按下 B 键");
    fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("按下 X 键");
    // A press that never let go is forgotten by 重做.
    s.emit("button", 6, 1);
    fireEvent.click(screen.getByRole("button", { name: "重做" }));
    s.emit("button", 6, 0);
    expect(s.prompt()).toBe("按下 X 键");
    fireEvent.click(screen.getByRole("button", { name: "上一步" }));
    expect(s.prompt()).toBe("按下 B 键");
    s.press(2);
    expect(s.prompt()).toBe("按下 X 键");
  });

  it("goes to the end at once with 完成, which needs something bound", async () => {
    const s = await ready();
    const finish = screen.getByRole("button", {
      name: "完成",
    }) as HTMLButtonElement;
    expect(finish.disabled).toBe(true);
    s.press(0);
    expect(finish.disabled).toBe(false);
    fireEvent.click(finish);
    expect(screen.getByRole("button", { name: "保存" })).toBeTruthy();
    expect(screen.queryByTestId("wizard-prompt")).toBeNull();
  });

  it("ignores the events of another controller", async () => {
    const s = await ready();
    act(() => {
      for (const listener of s.api.listeners) {
        listener({
          topic: "input.raw",
          data: { timeNs: now, device: 99, kind: "button", index: 3, value: 1 },
        });
        listener({
          topic: "input.raw",
          data: { timeNs: now, device: 99, kind: "button", index: 3, value: 0 },
        });
        listener({ topic: "input", data: { device: 12 } });
      }
    });
    expect(s.prompt()).toBe("按下 A 键");
  });

  it("speaks English", async () => {
    const s = await setup({ language: "en" });
    await screen.findByText("Leave the controller alone for a moment…");
    await s.pass(1000);
    expect(s.prompt()).toBe("Press A");
    expect(screen.getByRole("dialog", { name: "Set up mapping" })).toBeTruthy();
    s.emit("button", 4, 1);
    expect(screen.getByText("Detected: Button 4")).toBeTruthy();
    s.emit("button", 4, 0);
    expect(screen.getByText("Recorded A: Button 4")).toBeTruthy();
    expect(screen.getByText("2 / 25")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Skip" })).toBeTruthy();
  });
});

describe("saving the mapping", () => {
  /** Binds A to button 0 and B to button 1, then goes to the review. */
  async function review(options?: Parameters<typeof setup>[0]) {
    const s = await ready(options);
    s.press(0);
    s.press(1);
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    return s;
  }

  it("lists the answers, and warns that View and Menu are not mapped", async () => {
    await review();
    const list = screen.getByRole("list", { name: "已记录的控件" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(25);
    expect(
      screen.getByText(
        "没有映射 View 和 Menu。它们是紧急暂停的按键，缺了它们这只手柄无法用紧急暂停。",
      ),
    ).toBeTruthy();
  });

  it("builds the SDL mapping and saves it for the controller", async () => {
    const s = await review();
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(s.api.setMapping).toHaveBeenCalledOnce());
    expect(s.api.setMapping).toHaveBeenCalledWith(
      "12",
      `${guid},*,a:b0,b:b1,platform:Windows`,
    );
    // The devices are read again, and with nothing to tell, it closes.
    await waitFor(() => expect(s.onClose).toHaveBeenCalledOnce());
    expect(s.refresh).toHaveBeenCalled();
    // The watch and the capture are ended.
    expect(s.api.rawWatchStop).toHaveBeenCalledWith("12");
    expect(s.api.padCaptureStop).toHaveBeenCalled();
  });

  it("does not warn when View and Menu are mapped", async () => {
    const s = await ready();
    for (let i = 0; i < 22; i++)
      fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("按下 View 键");
    s.press(6);
    s.press(7);
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    expect(screen.queryByText(/没有映射/)).toBeNull();
  });

  it("tells what the daemon warns of, in plain words, before it closes", async () => {
    const s = await review();
    s.api.setMapping.mockImplementation(
      async (_device, mapping): Promise<MappingResult> => ({
        device: "k",
        guid,
        mapping,
        keys: ["k"],
        changed: true,
        applied: true,
        notes: [],
        warnings: [
          { code: "noBack" },
          { code: "noStart" },
          { code: "shadowed", device: 12 },
        ],
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("映射已保存")).toBeTruthy();
    expect(
      screen.getByText(
        "SDL 读到的是另一份映射（可能来自环境变量 SDL_GAMECONTROLLERCONFIG），刚保存的映射没有生效。",
      ),
    ).toBeTruthy();
    expect(s.onClose).not.toHaveBeenCalled();
    // It no longer holds the controller.
    expect(s.api.rawWatchStop).toHaveBeenCalledWith("12");
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    expect(s.onClose).toHaveBeenCalledOnce();
  });

  it("keeps the review and says why when the mapping is refused, and lets it be saved again", async () => {
    const s = await review();
    s.api.setMapping.mockRejectedValueOnce(
      Object.assign(new Error("Mapping not saved"), { code: -32025 }),
    );
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("这份映射无法保存。")).toBeTruthy();
    expect(s.onClose).not.toHaveBeenCalled();
    const save = screen.getByRole("button", {
      name: "保存",
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(s.onClose).toHaveBeenCalledOnce());
    expect(s.api.setMapping).toHaveBeenCalledTimes(2);
  });

  it("goes back from the review to the last control", async () => {
    const s = await review();
    fireEvent.click(screen.getByRole("button", { name: "上一步" }));
    expect(s.prompt()).toBe("按下 Home 键");
    expect(screen.getByText("25 / 25")).toBeTruthy();
  });
});

describe("every way out ends the watch and the capture", () => {
  it("Esc closes the dialog, and the unwatch and the capture stop follow", async () => {
    const s = await ready();
    const dialog = screen.getByRole("dialog", { name: "建立映射" });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(s.onClose).toHaveBeenCalledOnce();
    await waitFor(() =>
      expect(s.api.rawWatchStop).toHaveBeenCalledExactlyOnceWith("12"),
    );
    expect(s.api.padCaptureStop).toHaveBeenCalledOnce();
    // The events are not listened to any more.
    expect(s.api.listeners.size).toBe(0);
  });

  it("取消 and the close button do the same", async () => {
    const s = await ready();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(s.onClose).toHaveBeenCalledOnce();
    await waitFor(() => expect(s.api.rawWatchStop).toHaveBeenCalledOnce());
    cleanup();
    const t = await ready();
    fireEvent.click(screen.getByRole("button", { name: "取消建立映射" }));
    expect(t.onClose).toHaveBeenCalledOnce();
    await waitFor(() => expect(t.api.rawWatchStop).toHaveBeenCalledOnce());
  });

  it("closing before the watch has begun still ends it, after the start", async () => {
    const api = makeAPI();
    let started: (info: RawInfo) => void = () => {};
    api.rawWatchStart.mockImplementation(
      () =>
        new Promise<RawInfo>((resolve) => {
          started = resolve;
        }),
    );
    const onClose = vi.fn();
    render(
      <Host
        api={api}
        device={pad()}
        language="zh-CN"
        refresh={async () => {}}
        onClose={onClose}
      />,
    );
    await waitFor(() => expect(api.rawWatchStart).toHaveBeenCalledOnce());
    fireEvent.click(await screen.findByRole("button", { name: "取消" }));
    await waitFor(() => expect(api.rawWatchStop).toHaveBeenCalledOnce());
    // The stop is asked after the start was: Main keeps their order.
    expect(api.rawWatchStop.mock.invocationCallOrder[0]).toBeGreaterThan(
      api.rawWatchStart.mock.invocationCallOrder[0],
    );
    started(rawInfo());
  });

  it("an unmounted dialog ends them too", async () => {
    const s = await ready();
    cleanup();
    await waitFor(() => expect(s.api.rawWatchStop).toHaveBeenCalledOnce());
    expect(s.api.padCaptureStop).toHaveBeenCalledOnce();
  });
});

describe("the window loses the front (the mapping stays paused, ADR-0076)", () => {
  it("keeps the capture while the window is away, and asks for it again, staying, when it is back", async () => {
    const s = await ready();
    await act(async () => {
      window.dispatchEvent(new Event("blur"));
    });
    // Main does not end a capture that stays, so there is nothing to give
    // back: the wizard neither stops the capture nor asks for another.
    await s.pass(2000);
    expect(s.api.padCaptureStop).not.toHaveBeenCalled();
    expect(s.api.padCaptureStart).toHaveBeenCalledOnce();
    expect(s.api.rawWatchStop).not.toHaveBeenCalled();
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    // Main may have ended it meanwhile (a lock, a sleep): it is asked for
    // again, and the daemon takes that as the same capture when it is on.
    await waitFor(() => expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2));
    expect(s.api.padCaptureStart).toHaveBeenNthCalledWith(1, true);
    expect(s.api.padCaptureStart).toHaveBeenNthCalledWith(2, true);
    expect(s.api.padCaptureStop).not.toHaveBeenCalled();
  });

  it("ends the capture when the wizard closes while the window is away", async () => {
    const s = await ready();
    await act(async () => {
      window.dispatchEvent(new Event("blur"));
    });
    // The window is not in front, so the dialog cannot be clicked: it goes
    // with the page, or with Esc from a shortcut. Either way it ends the
    // capture, and Main does not need the window to be in front for that.
    cleanup();
    await waitFor(() => expect(s.api.padCaptureStop).toHaveBeenCalledOnce());
    expect(s.api.rawWatchStop).toHaveBeenCalledOnce();
    expect(s.api.listeners.size).toBe(0);
  });

  it("ignores the controller until the window is back and the capture is asked for again", async () => {
    const s = await ready();
    s.press(0);
    expect(s.prompt()).toBe("按下 B 键");
    s.emit("button", 5, 1);
    await act(async () => {
      window.dispatchEvent(new Event("blur"));
    });
    expect(
      screen.getByText("窗口不在前台，向导已暂停。回到这个窗口后继续。"),
    ).toBeTruthy();
    // What it does now is not an answer: the mapping is live again.
    s.emit("button", 5, 0);
    s.press(7);
    expect(s.prompt()).toBe("按下 B 键");
    expect(s.api.padCaptureStart).toHaveBeenCalledOnce();

    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
    // The press of B that began before is forgotten: it needs its own release.
    s.emit("button", 5, 0);
    expect(s.prompt()).toBe("按下 B 键");
    s.press(8);
    expect(s.prompt()).toBe("按下 X 键");
  });
});

describe("while the mapping is being saved", () => {
  /** Binds A and B, goes to the review, and holds the save open. */
  async function saving() {
    const s = await ready();
    s.press(0);
    s.press(1);
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    let finish: (result: MappingResult) => void = () => {};
    let fail: (error: unknown) => void = () => {};
    s.api.setMapping.mockImplementation(
      () =>
        new Promise<MappingResult>((resolve, reject) => {
          finish = resolve;
          fail = reject;
        }),
    );
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(s.api.setMapping).toHaveBeenCalledOnce());
    return {
      s,
      finish: (result: MappingResult) => finish(result),
      fail: (error: unknown) => fail(error),
    };
  }

  const shadowed = (mapping: string): MappingResult => ({
    device: "k",
    guid,
    mapping,
    keys: ["k"],
    changed: true,
    applied: true,
    notes: [],
    warnings: [{ code: "shadowed", device: 12 }],
  });

  it("stays on the review, and says it is saving", async () => {
    const { finish } = await saving();
    // Not the quiet period's text, and the answers are still there.
    expect(screen.queryByText("请先别碰手柄，稍等一下…")).toBeNull();
    const list = screen.getByRole("list", { name: "已记录的控件" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(25);
    const save = screen.getByRole("button", {
      name: "正在保存…",
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    // Nothing else can be done meanwhile.
    for (const name of ["上一步", "取消"])
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
    await act(async () => {
      finish(shadowed("m"));
    });
    expect(await screen.findByText("映射已保存")).toBeTruthy();
  });

  it("cannot be closed with Esc or the close button, so the result is not lost", async () => {
    const { s, finish } = await saving();
    const dialog = screen.getByRole("dialog", { name: "建立映射" });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    const close = screen.getByRole("button", {
      name: "取消建立映射",
    }) as HTMLButtonElement;
    expect(close.disabled).toBe(true);
    fireEvent.click(close);
    expect(s.onClose).not.toHaveBeenCalled();
    await act(async () => {
      finish(shadowed("m"));
    });
    // The devices are read again, and what the daemon warns of is told.
    expect(
      await screen.findByText(
        "SDL 读到的是另一份映射（可能来自环境变量 SDL_GAMECONTROLLERCONFIG），刚保存的映射没有生效。",
      ),
    ).toBeTruthy();
    expect(s.refresh).toHaveBeenCalled();
    // And it can be closed again.
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    expect(s.onClose).toHaveBeenCalledOnce();
  });

  it("goes back to the review, which can be closed, when the save fails", async () => {
    const { s, fail } = await saving();
    await act(async () => {
      fail(Object.assign(new Error("Mapping not saved"), { code: -32025 }));
    });
    expect(await screen.findByText("这份映射无法保存。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "保存" })).toBeTruthy();
    const close = screen.getByRole("button", {
      name: "取消建立映射",
    }) as HTMLButtonElement;
    expect(close.disabled).toBe(false);
    fireEvent.click(close);
    expect(s.onClose).toHaveBeenCalledOnce();
  });
});

describe("the controller moves while the window is away", () => {
  const away = () =>
    act(async () => {
      window.dispatchEvent(new Event("blur"));
    });
  const back = () =>
    act(async () => {
      window.dispatchEvent(new Event("focus"));
    });

  it("counts a D-pad press after a hat that was let go while away", async () => {
    const s = await ready();
    for (let i = 0; i < 4; i++)
      fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("按下十字键的上");
    s.emit("hat", 0, 1);
    expect(s.prompt()).toBe("按下十字键的下");
    // Still held when the user looks away, let go there.
    await away();
    s.emit("hat", 0, 0);
    await back();
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
    s.emit("hat", 0, 4);
    expect(s.prompt()).toBe("按下十字键的左");
  });

  it("counts a stick that was brought back while away", async () => {
    const s = await ready();
    for (let i = 0; i < 8; i++)
      fireEvent.click(screen.getByRole("button", { name: "跳过" }));
    expect(s.prompt()).toBe("把左摇杆推到最左，再放回中间");
    s.emit("axis", 0, -30000);
    await away();
    s.emit("axis", 0, 0);
    await back();
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
    expect(s.prompt()).toBe("把左摇杆推到最左，再放回中间");
    s.push(0, -30000);
    expect(s.prompt()).toBe("把左摇杆推到最右，再放回中间");
  });
});

describe("the capture cannot be had again when the window is back", () => {
  const away = () =>
    act(async () => {
      window.dispatchEvent(new Event("blur"));
    });
  const back = () =>
    act(async () => {
      window.dispatchEvent(new Event("focus"));
    });

  it("asks again a moment later, and goes on when that works", async () => {
    const s = await ready();
    await away();
    s.api.padCaptureStart
      .mockRejectedValueOnce(new Error("requires the focused padmate window"))
      .mockResolvedValueOnce(undefined);
    await back();
    expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2);
    await s.pass(500);
    await waitFor(() => expect(s.api.padCaptureStart).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
    s.press(5);
    expect(s.prompt()).toBe("按下 B 键");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("says so and ends the wizard when it keeps failing", async () => {
    const s = await ready();
    await away();
    s.api.padCaptureStart.mockRejectedValue(
      new Error("requires the focused padmate window"),
    );
    await back();
    await s.pass(5000);
    expect(
      await screen.findByText("无法读取手柄的原始输入，原因见“设置 → 日志”。"),
    ).toBeTruthy();
    // Nothing is left on.
    await waitFor(() => expect(s.api.rawWatchStop).toHaveBeenCalledOnce());
    expect(s.api.listeners.size).toBe(0);
    expect(screen.getByRole("button", { name: "关闭" })).toBeTruthy();
    expect(screen.queryByText(/窗口不在前台/)).toBeNull();
  });

  it("stops asking when the window goes away again, and starts over on the next return", async () => {
    const s = await ready();
    await away();
    s.api.padCaptureStart.mockRejectedValueOnce(
      new Error("requires the focused padmate window"),
    );
    await back();
    expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2);
    await away();
    await s.pass(1000);
    expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2);
    await back();
    expect(s.api.padCaptureStart).toHaveBeenCalledTimes(3);
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("does not ask twice for one return", async () => {
    const s = await ready();
    await away();
    let granted: () => void = () => {};
    s.api.padCaptureStart.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          granted = resolve;
        }),
    );
    await back();
    await back();
    expect(s.api.padCaptureStart).toHaveBeenCalledTimes(2);
    await act(async () => {
      granted();
    });
    await waitFor(() => expect(screen.queryByText(/窗口不在前台/)).toBeNull());
  });
});
