// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStore } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type {
  DeviceEntry,
  LatencyResult,
  PadmateAPI,
  StatusResult,
} from "../../shared/api";
import {
  type ActivityRow,
  type DecisionActivity,
  decisionRow,
  unroutedRow,
} from "../activity-model";
import { editorAPIFixture } from "../editor-api-fixture";
import { createPadmateStore, type PadmateStore } from "../store";
import { DiagnosticsPage, LATENCY_POLL_MS } from "./DiagnosticsPage";

function pad(
  id: number,
  name: string,
  type: string,
  fields: Partial<DeviceEntry> = {},
): DeviceEntry {
  return {
    info: {
      id,
      name,
      type,
      vendor: 0,
      product: 0,
      version: 0,
      guid: `g${id}`,
      driver: "",
      path: "",
      serial: "",
      gamepad: true,
      virtual: false,
    },
    key: `k${id}`,
    slot: id - 7,
    status: "mapped",
    disabled: false,
    ...fields,
  };
}

const xbox = pad(7, "Xbox pad", "xboxone");
const dualsense = pad(8, "DualSense", "ps5");

function slot(device: number, index: number) {
  return {
    slot: index,
    device,
    key: `k${device}`,
    name: `Slot ${index}`,
    profile: "Paint",
    generation: 1,
    layers: { base: "base", overlays: [], color: "" },
    latches: [],
    mode: 0,
  };
}

function statusOf(
  devices: DeviceEntry[],
  fields: Partial<StatusResult> = {},
): StatusResult {
  return {
    version: "test",
    protocol: 1,
    dryRun: false,
    fakeInput: false,
    paused: false,
    capturing: false,
    suspended: "none",
    profileId: "paint",
    slots: devices.map((entry, index) => slot(entry.info.id, index)),
    devices,
    ...fields,
  };
}

const at = (second: number, ms = 0) =>
  new Date(2026, 9, 24, 14, 3, second, ms).getTime();

function decide(
  fields: Record<string, unknown>,
  event: Partial<DecisionActivity> = {},
  status: StatusResult = statusOf([xbox]),
  seq = 1,
): ActivityRow {
  const made = decisionRow(
    {
      kind: "decision",
      slot: 0,
      at: at(9, 27),
      decision: { control: "south", edge: "down", gesture: "press", ...fields },
      ...event,
    },
    seq,
    status,
    0,
  );
  if (!made) throw new Error("no row");
  return made;
}

function Harness({ store }: { store: PadmateStore }) {
  const state = useStore(
    store,
    useShallow(
      ({
        inputs: _inputs,
        logs: _logs,
        activity: _activity,
        preview: _preview,
        keyPreview: _keyPreview,
        ...rest
      }) => rest,
    ),
  );
  return <DiagnosticsPage state={state} store={store} />;
}

function mount({
  status = statusOf([xbox]),
  rows = [] as ActivityRow[],
  language = "zh-CN" as "zh-CN" | "en",
  api: overrides = {} as Partial<PadmateAPI>,
} = {}) {
  const api = { ...editorAPIFixture(), ...overrides } as unknown as PadmateAPI;
  const store = createPadmateStore(api);
  store.setState({
    connection: { phase: "connected" },
    status,
    activity: rows,
    language,
  });
  const view = render(<Harness store={store} />);
  return { api, store, ...view };
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const list = () => screen.getByRole("list", { hidden: false });
const items = () => within(list()).getAllByRole("listitem");
const cells = (item: HTMLElement) => ({
  time: item.querySelector(".page-act-time")?.textContent,
  pad: item.querySelector(".page-act-pad")?.textContent,
  key: item.querySelector(".page-act-key .visually-hidden")?.textContent,
  gesture: item.querySelector(".page-act-gesture")?.textContent,
  where: item.querySelector(".page-act-where")?.textContent,
  result: item.querySelector(".page-act-result")?.textContent,
});

describe("the event list (UI-20, UI-21)", () => {
  it("says what to do when nothing was pressed yet, and has nothing to clear", () => {
    mount();
    expect(
      screen.getByRole("heading", { name: "事件", level: 2 }),
    ).toBeTruthy();
    expect(
      screen.getByText("按下手柄上的键后，这里会显示每次按键的结果。"),
    ).toBeTruthy();
    expect(screen.queryByRole("list")).toBeNull();
    expect(
      (screen.getByRole("button", { name: "清空" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });

  it("shows one row per entry, newest first: time, key, gesture, where and the action", () => {
    mount({
      rows: [
        decide(
          { reason: "unbound", binding: "/layers/base/south" },
          { waiting: ["tap"], at: at(9, 27) },
          undefined,
          1,
        ),
        decide(
          {
            reason: "dispatch",
            gesture: "tap",
            level: "base",
            binding: "/layers/base/south",
            action: "tap Ctrl+Z",
          },
          { waitedMs: 180, at: at(9, 207) },
          undefined,
          2,
        ),
      ],
    });
    const rows = items();
    expect(rows).toHaveLength(2);
    expect(cells(rows[0])).toEqual({
      time: "14:03:09.207",
      pad: undefined,
      key: "A",
      gesture: "点按",
      where: "平时",
      result: "Ctrl+Z等了 180 ms",
    });
    expect(cells(rows[1])).toEqual({
      time: "14:03:09.027",
      pad: undefined,
      key: "A",
      gesture: "点按",
      where: "",
      result: "按下时没有动作，要等松开、长按或连按后才能确定",
    });
    // The key is drawn as the pad shows it; the name is read out only.
    expect(
      rows[0].querySelector(".control-icon")?.getAttribute("data-kind"),
    ).toBe("art");
    expect(rows[0].querySelector("time")?.getAttribute("datetime")).toMatch(
      /^2026-10-2[345]T/,
    );
    expect(rows[0].querySelector(".page-act-wait")?.textContent).toBe(
      "等了 180 ms",
    );
    expect(rows[1].querySelector(".page-act-wait")).toBeNull();
  });

  it("says why a press did nothing, in a sentence", () => {
    mount({
      rows: [
        decide({ reason: "unbound", control: "east" }),
        decide({ reason: "blocked", control: "north", level: "tools" }),
        decide({ reason: "script", control: "west", gesture: "tap" }),
      ],
    });
    expect(items().map((item) => cells(item).result)).toEqual([
      "脚本处理了这个键",
      "这个键在当前层被屏蔽",
      "当前配置里这个键没有设置动作",
    ]);
    expect(items().map((item) => cells(item).where)).toEqual([
      "脚本",
      "tools",
      "",
    ]);
    expect(items().map((item) => cells(item).key)).toEqual(["X", "Y", "B"]);
  });

  it("answers a press that waits for more taps when no action is set for the number made", () => {
    // A key with only a double tap, tapped once: the press waits, and when the
    // wait ends the engine says that nothing answered it.
    mount({
      rows: [
        decide(
          { reason: "unbound", binding: "/layers/base/south", level: "base" },
          { waiting: ["doubleTap"], at: at(9, 27) },
          undefined,
          1,
        ),
        decide(
          {
            reason: "unbound",
            gesture: "tap",
            binding: "/layers/base/south",
            level: "base",
          },
          { waitedMs: 232, at: at(9, 259) },
          undefined,
          2,
        ),
      ],
    });
    const [ended, waiting] = items();
    expect(cells(ended)).toEqual({
      time: "14:03:09.259",
      pad: undefined,
      key: "A",
      gesture: "点按",
      where: "平时",
      result: "按了 1 下，这个键没有设置按 1 下的动作等了 232 ms",
    });
    expect(ended.querySelector(".page-act-wait")?.textContent).toBe(
      "等了 232 ms",
    );
    expect(cells(waiting).result).toBe(
      "按下时没有动作，要等松开、长按或连按后才能确定",
    );
    expect(waiting.querySelector(".page-act-wait")).toBeNull();
    // The press that only waits is not "no action"; its answer is.
    fireEvent.click(screen.getByRole("button", { name: "只看没有动作的" }));
    expect(items().map((item) => cells(item).time)).toEqual(["14:03:09.259"]);
  });

  it("answers it in English", () => {
    mount({
      language: "en",
      rows: [
        decide(
          { reason: "unbound", gesture: "tripleTap", binding: "/x" },
          { waitedMs: 410 },
        ),
      ],
    });
    expect(cells(items()[0])).toMatchObject({
      gesture: "Triple tap",
      result: "Tapped 3 times; this key has no action for 3 taps.Waited 410 ms",
    });
  });

  it("falls back to the action as the engine wrote it", () => {
    mount({
      rows: [
        decide({ reason: "dispatch", action: "layer toggle tools" }),
        decide({ reason: "dispatch", action: 'text "你好"' }),
        decide({ reason: "dispatch", action: "sequence: Ctrl+A, wait 100" }),
      ],
    });
    expect(items().map((item) => cells(item).result)).toEqual([
      "Ctrl+A, 100 ms",
      "输入文字「你好」",
      "layer toggle tools",
    ]);
  });

  it("names the set of a function key as the function key, and a release by its edge", () => {
    mount({
      status: statusOf([dualsense]),
      rows: [
        decide(
          {
            reason: "dispatch",
            level: "function:back",
            action: "tap Ctrl+Z",
            control: "south",
          },
          {},
          statusOf([dualsense]),
        ),
        decide(
          { reason: "release", edge: "up", level: "common" },
          {},
          statusOf([dualsense]),
        ),
      ],
    });
    const rows = items();
    expect(cells(rows[1]).where).toBe("按住功能键");
    expect(cells(rows[1]).key).toBe("×");
    expect(cells(rows[0])).toMatchObject({
      gesture: "松开",
      where: "共用",
      result: "松开，放开了按住的键",
    });
  });

  it("marks output that was only previewed", () => {
    mount({
      rows: [
        decide(
          { reason: "dispatch", action: "tap A" },
          {},
          {
            ...statusOf([xbox]),
            localDryRun: true,
          },
        ),
        decide({ reason: "dispatch", action: "tap B" }),
      ],
    });
    const [sent, previewed] = items();
    expect(within(sent).queryByText("只预览")).toBeNull();
    expect(within(previewed).getByText("只预览")).toBeTruthy();
  });

  it("names the pad only when more than one is connected", () => {
    const two = statusOf([xbox, dualsense]);
    const view = mount({
      status: two,
      rows: [
        decide({ reason: "spurious" }, { slot: 0 }, two),
        decide({ reason: "spurious" }, { slot: 1 }, two),
      ],
    });
    expect(items().map((item) => cells(item).pad)).toEqual([
      "DualSense",
      "Xbox pad",
    ]);
    expect(list().getAttribute("data-pad")).toBe("true");
    view.unmount();
    mount({ rows: [decide({ reason: "spurious" })] });
    expect(items().map((item) => cells(item).pad)).toEqual([undefined]);
    expect(list().getAttribute("data-pad")).toBeNull();
  });

  it("explains a press of a pad that no slot maps", () => {
    const two = statusOf([
      xbox,
      pad(9, "Third", "xboxone", { slot: -1, status: "waiting" }),
    ]);
    mount({
      status: two,
      rows: [
        unroutedRow(
          {
            kind: "unrouted",
            at: at(10),
            device: 9,
            control: "south",
            down: true,
            why: "queued",
          },
          1,
          two,
          0,
        ) as ActivityRow,
      ],
    });
    expect(cells(items()[0])).toMatchObject({
      pad: "Third",
      key: "A",
      gesture: "",
      where: "",
      result: "这只手柄没有被映射：已有 2 只手柄在映射",
    });
  });

  it("names the keys as the pad prints them, whatever the pad", () => {
    const status = statusOf([dualsense]);
    mount({
      status,
      rows: [
        decide({ reason: "spurious", control: "ls.up" }, {}, status),
        decide({ reason: "spurious", control: "lt" }, {}, status),
      ],
    });
    expect(items().map((item) => cells(item).key)).toEqual(["L2", "L3 ↑"]);
  });

  it("follows the store while it is open, newest first", () => {
    const view = mount({
      rows: [decide({ reason: "spurious", control: "east" })],
    });
    act(() =>
      view.store.setState({
        activity: [
          ...view.store.getState().activity,
          decide({ reason: "paused", control: "north" }, {}, undefined, 2),
        ],
      }),
    );
    expect(items().map((item) => cells(item).key)).toEqual(["Y", "B"]);
  });

  it("pauses and resumes recording from its button", () => {
    const view = mount({ rows: [decide({ reason: "spurious" })] });
    fireEvent.click(screen.getByRole("button", { name: "暂停记录" }));
    expect(view.store.getState().activityPaused).toBe(true);
    expect(screen.getByRole("button", { name: "继续记录" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "暂停记录" })).toBeNull();
    expect(
      screen.getByText("记录已暂停，新的按键不会出现在这里。"),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "继续记录" }));
    expect(view.store.getState().activityPaused).toBe(false);
    expect(screen.getByRole("button", { name: "暂停记录" })).toBeTruthy();
  });

  it("says that recording is paused when nothing was recorded", () => {
    const view = mount();
    act(() => view.store.setState({ activityPaused: true }));
    expect(screen.getAllByText("记录已暂停").length).toBeGreaterThan(0);
  });

  it("clears the list", () => {
    const view = mount({
      rows: [decide({ reason: "spurious" }), decide({ reason: "paused" })],
    });
    fireEvent.click(screen.getByRole("button", { name: "清空" }));
    expect(view.store.getState().activity).toEqual([]);
    expect(screen.queryByRole("list")).toBeNull();
    expect(
      screen.getByText("按下手柄上的键后，这里会显示每次按键的结果。"),
    ).toBeTruthy();
  });

  it("filters by the key, by the name the pad prints or the engine's control", () => {
    mount({
      rows: [
        decide({ reason: "spurious", control: "south" }),
        decide({ reason: "spurious", control: "east" }),
        decide({ reason: "spurious", control: "ls.up" }),
      ],
    });
    const filter = screen.getByRole("searchbox", { name: "筛选按键" });
    fireEvent.change(filter, { target: { value: "ls" } });
    expect(items().map((item) => cells(item).key)).toEqual(["LS ↑"]);
    fireEvent.change(filter, { target: { value: "b" } });
    expect(items().map((item) => cells(item).key)).toEqual(["B"]);
    fireEvent.change(filter, { target: { value: "south" } });
    expect(items().map((item) => cells(item).key)).toEqual(["A"]);
    fireEvent.change(filter, { target: { value: "zzz" } });
    expect(screen.getByText("没有符合条件的记录")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(items()).toHaveLength(3);
    expect((filter as HTMLInputElement).value).toBe("");
  });

  it("shows only the presses that did nothing on request", () => {
    mount({
      rows: [
        decide({ reason: "dispatch", action: "tap A", control: "south" }),
        decide({ reason: "unbound", control: "east" }),
        decide(
          { reason: "unbound", control: "west", binding: "/x" },
          { waiting: ["hold"] },
        ),
        decide({ reason: "release", edge: "up", control: "north" }),
        decide({ reason: "paused", control: "leftshoulder" }),
      ],
    });
    const toggle = screen.getByRole("button", { name: "只看没有动作的" });
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    expect(items()).toHaveLength(5);
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(items().map((item) => cells(item).key)).toEqual(["LB", "B"]);
    fireEvent.click(toggle);
    expect(items()).toHaveLength(5);
  });

  it("is in English when asked", () => {
    mount({
      language: "en",
      rows: [
        decide({ reason: "unbound" }),
        decide(
          { reason: "dispatch", gesture: "hold", action: "tap Ctrl+Z" },
          { waitedMs: 250 },
        ),
      ],
    });
    expect(
      screen.getByRole("heading", { name: "Events", level: 2 }),
    ).toBeTruthy();
    expect(
      screen.getByRole("searchbox", { name: "Filter by key" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Only presses with no action" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Pause recording" }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Clear" })).toBeTruthy();
    const [held, none] = items();
    expect(cells(held)).toMatchObject({
      gesture: "Hold",
      result: "Ctrl+ZWaited 250 ms",
    });
    expect(cells(none).result).toBe(
      "This key has no action in the current profile.",
    );
  });
});

describe("latency (UI-22)", () => {
  const figures: LatencyResult = {
    devices: [
      {
        device: "k7",
        id: 7,
        name: "Xbox pad",
        samples: 120,
        p50: 210,
        p90: 480,
        p99: 1320,
        max: 1710,
        warn: false,
      },
    ],
    dryRun: false,
    warnP99Us: 1500,
    warnMinSamples: 50,
  };

  /** Lets the read that is on its way finish. */
  const settle = () => act(async () => {});
  const tick = (ms: number) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  const latency = (result: LatencyResult | Error) =>
    vi.fn(async (_device?: string) => {
      if (result instanceof Error) throw result;
      return result;
    });

  beforeEach(() => vi.useFakeTimers());

  it("reads when the page opens and every second while it is open, and not after", async () => {
    const getLatency = latency(figures);
    const view = mount({ api: { getLatency } });
    await settle();
    expect(getLatency).toHaveBeenCalledTimes(1);
    expect(getLatency).toHaveBeenCalledWith();
    await tick(LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(2);
    await tick(3 * LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(5);
    view.unmount();
    await tick(5 * LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(5);
  });

  it("does not ask again while an answer is on its way", async () => {
    let answer: (value: LatencyResult) => void = () => {};
    const getLatency = vi.fn(
      (_device?: string) =>
        new Promise<LatencyResult>((resolve) => {
          answer = resolve;
        }),
    );
    mount({ api: { getLatency } });
    await tick(5 * LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(1);
    answer(figures);
    await settle();
    await tick(LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(2);
  });

  it("shows the four figures and the number of samples of each pad", async () => {
    mount({ api: { getLatency: latency(figures) } });
    await settle();
    expect(
      screen.getByRole("heading", { name: "延迟", level: 2 }),
    ).toBeTruthy();
    expect(screen.getByText("Xbox pad")).toBeTruthy();
    expect(screen.getByText("120 个样本")).toBeTruthy();
    const figure = (name: string) =>
      document.querySelector(`[data-figure="${name}"]`);
    expect(figure("p50")?.textContent).toBe("p50210 µs");
    expect(figure("p90")?.textContent).toBe("p90480 µs");
    expect(figure("p99")?.textContent).toBe("p991.32 ms");
    expect(figure("max")?.textContent).toBe("最大1.71 ms");
    expect(
      screen.getByText(
        "从 padmate 收到手柄输入到发出按键的时间，不含手柄和接收器本身的延迟；padmate 窗口在前台时只预览，不计入。",
      ),
    ).toBeTruthy();
  });

  it("colors a pad only when the daemon says to", async () => {
    const quiet = {
      ...figures,
      devices: [{ ...figures.devices[0], p99: 9000, warn: false }],
    };
    const view = mount({ api: { getLatency: latency(quiet) } });
    await settle();
    expect(document.querySelector("[data-warn]")).toBeNull();
    expect(screen.queryByText(/p99 超过/)).toBeNull();
    view.unmount();
    const loud = {
      ...figures,
      devices: [{ ...figures.devices[0], p99: 1800, warn: true }],
    };
    mount({ api: { getLatency: latency(loud) } });
    await settle();
    expect(document.querySelector("[data-warn]")).not.toBeNull();
    expect(screen.getByText("p99 超过 1.50 ms")).toBeTruthy();
  });

  it("shows dashes for a pad without samples, and has nothing to clear then", async () => {
    mount({
      api: {
        getLatency: latency({
          ...figures,
          devices: [
            {
              ...figures.devices[0],
              samples: 0,
              p50: 0,
              p90: 0,
              p99: 0,
              max: 0,
            },
          ],
        }),
      },
    });
    await settle();
    expect(document.querySelector('[data-figure="p99"] dd')?.textContent).toBe(
      "—",
    );
    expect(screen.getByText("0 个样本")).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "清空延迟统计",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("clears the figures of all pads and reads again", async () => {
    const getLatency = latency(figures);
    const resetLatency = vi.fn(async (_device?: string) => ({ reset: true }));
    mount({ api: { getLatency, resetLatency } });
    await settle();
    fireEvent.click(screen.getByRole("button", { name: "清空延迟统计" }));
    await settle();
    expect(resetLatency).toHaveBeenCalledTimes(1);
    expect(resetLatency).toHaveBeenCalledWith();
    expect(getLatency).toHaveBeenCalledTimes(2);
  });

  it("does not show the answer of a read that was already on its way when the figures were cleared", async () => {
    const emptied: LatencyResult = {
      ...figures,
      devices: [
        { ...figures.devices[0], samples: 0, p50: 0, p90: 0, p99: 0, max: 0 },
      ],
    };
    // The read of the second tick is asked before the reset and answered
    // after it, with the figures from before; the one after the reset is empty.
    let late: (value: LatencyResult) => void = () => {};
    let calls = 0;
    const getLatency = vi.fn((_device?: string) => {
      calls += 1;
      if (calls === 1) return Promise.resolve(figures);
      if (calls === 2)
        return new Promise<LatencyResult>((resolve) => {
          late = resolve;
        });
      return Promise.resolve(emptied);
    });
    const resetLatency = vi.fn(async (_device?: string) => ({ reset: true }));
    mount({ api: { getLatency, resetLatency } });
    await settle();
    await tick(LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole("button", { name: "清空延迟统计" }));
    await settle();
    expect(resetLatency).toHaveBeenCalledTimes(1);
    // The figures are read again at once, not at the next tick.
    expect(getLatency).toHaveBeenCalledTimes(3);
    late(figures);
    await settle();
    expect(screen.getByText("0 个样本")).toBeTruthy();
    expect(document.querySelector('[data-figure="p99"] dd')?.textContent).toBe(
      "—",
    );
    expect(
      (
        screen.getByRole("button", {
          name: "清空延迟统计",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    // Later ticks go on as before.
    await tick(LATENCY_POLL_MS);
    expect(getLatency).toHaveBeenCalledTimes(4);
  });

  it("says when the mapping service only prints its keys", async () => {
    mount({ api: { getLatency: latency({ ...figures, dryRun: true }) } });
    await settle();
    expect(
      screen.getByText("映射服务在演练模式：按键只打印，不发送。"),
    ).toBeTruthy();
  });

  it("says when there is no pad being mapped, and when it could not be read", async () => {
    const view = mount({
      api: { getLatency: latency({ ...figures, devices: [] }) },
    });
    await settle();
    expect(screen.getByText("没有正在映射的手柄。")).toBeTruthy();
    view.unmount();
    mount({ api: { getLatency: latency(new Error("gone")) } });
    await settle();
    expect(screen.getByText("无法读取延迟。")).toBeTruthy();
  });

  it("keeps the last figures when a read fails, and says so", async () => {
    let fail = false;
    const getLatency = vi.fn(async (_device?: string) => {
      if (fail) throw new Error("gone");
      return figures;
    });
    mount({ api: { getLatency } });
    await settle();
    fail = true;
    await tick(LATENCY_POLL_MS);
    expect(document.querySelector('[data-figure="p50"] dd')?.textContent).toBe(
      "210 µs",
    );
    expect(screen.getByText("无法读取延迟。")).toBeTruthy();
    fail = false;
    await tick(LATENCY_POLL_MS);
    expect(screen.queryByText("无法读取延迟。")).toBeNull();
  });

  it("is in English when asked", async () => {
    mount({ language: "en", api: { getLatency: latency(figures) } });
    await settle();
    expect(
      screen.getByRole("heading", { name: "Latency", level: 2 }),
    ).toBeTruthy();
    expect(screen.getByText("120 samples")).toBeTruthy();
    expect(document.querySelector('[data-figure="max"] dt')?.textContent).toBe(
      "Max",
    );
    expect(
      screen.getByRole("button", { name: "Clear the latency figures" }),
    ).toBeTruthy();
  });
});

describe("conflicts (IN-24)", () => {
  const steam = {
    kind: "conflict",
    process: "steam.exe",
    message: "Steam is running",
    document: "docs/troubleshooting.md",
  };
  const steamInput = (classes: string[]) => ({
    kind: "steamInput",
    process: "Steam",
    message: "Steam Input may manage a controller",
    document: "docs/troubleshooting.md",
    classes,
  });

  it("says there is none", () => {
    mount();
    expect(
      screen.getByRole("heading", { name: "冲突", level: 2 }),
    ).toBeTruthy();
    expect(screen.getByText("没有检测到可能占用手柄的软件。")).toBeTruthy();
  });

  it("names the software that is running, without a button to close it", () => {
    mount({
      status: statusOf([xbox], {
        conflicts: [{ ...steam, process: "DS4Windows.exe", kind: "conflict" }],
      }),
    });
    expect(screen.getByText("DS4Windows 正在运行")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "关闭提示" })).toBeNull();
  });

  it("says what Steam may take over and how to stop it, in the ADR's words", () => {
    mount({
      status: statusOf([xbox], {
        conflicts: [steam, steamInput(["xbox", "playstation"])],
      }),
    });
    // One notice for Steam, though the daemon lists it twice.
    const notices = document.querySelectorAll(".notice");
    expect(notices).toHaveLength(1);
    expect(notices[0].textContent).toContain("Steam 正在运行");
    expect(notices[0].textContent).toContain(
      "它可能接管 Xbox 类、PlayStation 类手柄：请在 Steam 的设置 → 控制器里关掉这类手柄的 Steam Input，或在不用 Steam 时退出它。",
    );
  });

  it("names every kind of controller in plain words", () => {
    mount({
      status: statusOf([xbox], {
        conflicts: [
          steamInput(["xbox", "playstation", "switch", "steamHardware"]),
        ],
      }),
    });
    expect(document.querySelector(".notice")?.textContent).toContain(
      "它可能接管 Xbox 类、PlayStation 类、Switch 类、Steam 自家手柄：",
    );
  });

  it("has Steam's old advice when no controller falls in a class", () => {
    mount({ status: statusOf([xbox], { conflicts: [steam] }) });
    expect(document.querySelector(".notice")?.textContent).toContain(
      "Steam 输入可能抢占手柄或重复发送按键。",
    );
  });

  it("is in English when asked", () => {
    const view = mount({
      language: "en",
      status: statusOf([xbox], { conflicts: [steamInput(["xbox"])] }),
    });
    expect(screen.getByText("Steam is running")).toBeTruthy();
    expect(document.querySelector(".notice")?.textContent).toContain(
      "It may take over Xbox-type controllers: turn Steam Input off for them in Steam's Settings → Controller, or quit Steam when you are not using it.",
    );
    view.unmount();
    mount({
      language: "en",
      status: statusOf([xbox], {
        conflicts: [steamInput(["xbox", "playstation", "switch"])],
      }),
    });
    expect(document.querySelector(".notice")?.textContent).toContain(
      "It may take over Xbox-type, PlayStation-type and Switch-type controllers:",
    );
  });
});
