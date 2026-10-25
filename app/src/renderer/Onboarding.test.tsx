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
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigPutResult,
  DaemonEvent,
  DeviceEntry,
  PadmateAPI,
  ProfileEntry,
  StatusResult,
} from "../shared/api";
import { App } from "./App";
import { editorAPIFixture } from "./editor-api-fixture";
import { ADVANCE_MS } from "./onboarding-model";
import { createPadmateStore, ONBOARDING_KEY } from "./store";

const pad: DeviceEntry = {
  info: {
    id: 1,
    name: "Test controller",
    type: "xboxone",
    vendor: 0,
    product: 0,
    version: 0,
    guid: "test",
    driver: "virtual",
    path: "",
    serial: "",
    gamepad: true,
    virtual: true,
  },
  key: "controller",
  slot: 0,
  status: "mapped",
  disabled: false,
};

const joycon: DeviceEntry = {
  ...pad,
  info: {
    ...pad.info,
    id: 2,
    name: "Joy-Con (L)",
    type: "joyconleft",
    buttonMask: 1,
  },
  key: "joycon",
};

/** The fixed default as padmated writes it into a new library (PRO-28). */
const defaultProfile = '{"schemaVersion":0,"name":"默认","layers":{"base":{}}}';

function snapshot(devices: DeviceEntry[] = [pad]): StatusResult {
  return {
    version: "0.13.0",
    protocol: 1,
    dryRun: true,
    fakeInput: true,
    paused: false,
    capturing: false,
    suspended: "none",
    // A new library starts with the fixed default in use.
    profileId: "default",
    slots: [],
    devices,
    conflicts: [
      {
        kind: "conflict",
        process: "steam.exe",
        message: "Steam is running",
        document: "docs/troubleshooting.md",
      },
    ],
  };
}

/** A daemon with an in-memory library that has only the fixed default. */
function setup({
  status = snapshot(),
  library = { default: defaultProfile } as Record<string, string>,
  language,
  noLibrary = false,
  strict = false,
}: {
  status?: StatusResult;
  library?: Record<string, string>;
  language?: "en";
  /** profiles.list fails: a session started with --profile FILE. */
  noLibrary?: boolean;
  /** Render inside StrictMode, which runs every effect twice. */
  strict?: boolean;
} = {}) {
  if (language) localStorage.setItem("padmate.language", language);
  let listener = (_event: DaemonEvent) => {};
  const fixture = editorAPIFixture();
  fixture.listProfiles.mockImplementation(async (): Promise<ProfileEntry[]> => {
    if (noLibrary) throw new Error("no profile library");
    return Object.entries(library).map(([id, text]) => ({
      id,
      name: JSON.parse(text).name,
      revision: `r-${id}`,
      ...(id === "default" ? { default: true } : {}),
    }));
  });
  fixture.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: library[id] ?? "{}",
    revision: `r-${id}`,
    diagnostics: [],
  }));
  const api: PadmateAPI = {
    ...fixture,
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next) => {
      listener = next;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => status),
    getDevices: vi.fn(async () => status.devices),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    getConfig: vi.fn(async () => ({
      text: '{"schemaVersion":0}',
      revision: "rev",
    })),
    putConfig: vi.fn(
      async (): Promise<ConfigPutResult> => ({
        revision: "saved",
        applied: true,
        restartRequired: false,
      }),
    ),
    getScriptStatus: vi.fn(async () => []),
    getScriptRegistrations: vi.fn(async () => []),
    reloadScripts: vi.fn(async () => []),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
  };
  const store = createPadmateStore(api);
  const app = <App store={store} />;
  render(strict ? <StrictMode>{app}</StrictMode> : app);
  /** The page makes nothing: no profile, no switch, no config.json. */
  const madeNothing = () => {
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(api.copyProfile).not.toHaveBeenCalled();
    expect(api.activateProfile).not.toHaveBeenCalled();
    expect(api.putConfig).not.toHaveBeenCalled();
  };
  return {
    api,
    store,
    library,
    madeNothing,
    /** A controller connects: the next status has it. */
    connect: (devices: DeviceEntry[]) => {
      status = { ...status, devices };
      act(() =>
        listener({
          topic: "input",
          data: { kind: "added", device: devices[0]?.info.id ?? 1 },
        }),
      );
    },
  };
}

const page = (name = "连接手柄") => screen.findByRole("dialog", { name });
const button = (dialog: HTMLElement, name: string) =>
  within(dialog).getByRole("button", { name });
/** The page's live region (the conflict notices have their own). */
const live = (dialog: HTMLElement) => {
  const region = dialog.querySelector(".onboarding-status") as HTMLElement;
  expect(region.getAttribute("role")).toBe("status");
  return region;
};
/** Moves the page's clock on, and lets what that starts settle. */
const wait = async (ms: number) => {
  await act(async () => {
    vi.advanceTimersByTime(ms);
  });
};
/** Waits until the page went to the main page. */
const entered = () =>
  waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
/** The topbar names the fixed default 默认. */
const showsDefault = () =>
  waitFor(() =>
    expect(screen.getByTitle("配置菜单").textContent).toContain("默认"),
  );

// Only the page's own timer and React's are faked; the clock still moves
// with real time, so async queries keep working.
beforeEach(() => {
  localStorage.clear();
  vi.useFakeTimers({
    shouldAdvanceTime: true,
    toFake: ["setTimeout", "clearTimeout"],
  });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("first-run page: when it opens", () => {
  it("opens by itself for a new library and stays closed once closed", async () => {
    const app = setup({ status: snapshot([]) });
    const dialog = await page();
    expect(live(dialog).textContent).toBe("请通过 USB 或蓝牙连接手柄。");
    expect(dialog.textContent).toContain("Steam 正在运行");
    // No steps: one action, and the close button in the title bar.
    expect(
      within(dialog.querySelector("footer") as HTMLElement)
        .getAllByRole("button")
        .map((entry) => entry.textContent),
    ).toEqual(["不连接手柄，直接进入"]);
    expect(button(dialog, "关闭")).toBeTruthy();
    expect(document.activeElement).toBe(button(dialog, "不连接手柄，直接进入"));
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    expect(app.store.getState().onboardingDone).toBe(true);
    // The main page asks for a controller, with the default in use.
    expect(
      await screen.findByRole("heading", { name: "连接一个手柄" }),
    ).toBeTruthy();
    await showsDefault();
    app.madeNothing();
    await act(async () => app.store.getState().refresh());
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("never opens for someone who already has profiles", async () => {
    const app = setup({
      library: {
        default: defaultProfile,
        paint: JSON.stringify({ schemaVersion: 0, name: "Paint", layers: {} }),
      },
    });
    await waitFor(() => expect(app.store.getState().profilesLoaded).toBe(true));
    // Marked at once, so deleting them later does not open it.
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    delete app.library.paint;
    await act(async () => app.store.getState().refreshLibrary());
    await screen.findByRole("group", { name: /Test controller/ });
    expect(screen.queryByRole("dialog")).toBeNull();
    await wait(ADVANCE_MS * 2);
    app.madeNothing();
  });

  it("stays closed once done, without a library and with another profile in use", async () => {
    localStorage.setItem(ONBOARDING_KEY, "done");
    const done = setup();
    await screen.findByRole("group", { name: /Test controller/ });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(done.store.getState().onboardingDone).toBe(true);
    cleanup();
    localStorage.clear();

    // A session started with a file has no library and no library ID.
    const file = setup({
      noLibrary: true,
      status: { ...snapshot(), profileId: "" },
    });
    await screen.findByRole("group", { name: /Test controller/ });
    await waitFor(() =>
      expect(file.store.getState().profilesError).not.toBe(""),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    cleanup();

    // Started with --profile-id for a profile the list does not have yet.
    const running = setup({ status: { ...snapshot(), profileId: "paint" } });
    await screen.findByRole("group", { name: /Test controller/ });
    expect(running.store.getState().profilesLoaded).toBe(true);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("lets conflict notices be closed, keeping the keyboard nearby", async () => {
    const base = snapshot([]);
    const app = setup({
      status: {
        ...base,
        conflicts: [
          ...(base.conflicts ?? []),
          {
            kind: "conflict",
            process: "DS4Windows.exe",
            message: "",
            document: "",
          },
        ],
      },
    });
    const dialog = await page();
    // The name and the advice share a line but stay apart when read.
    const steam = within(dialog).getByText("Steam 正在运行").closest(".notice");
    expect(steam?.textContent).toContain("Steam 正在运行 Steam 输入");
    const [first, second] = within(dialog).getAllByRole("button", {
      name: "关闭提示",
    });
    first?.focus();
    fireEvent.click(first as HTMLElement);
    expect(within(dialog).queryByText("Steam 正在运行")).toBeNull();
    expect(document.activeElement).toBe(second);
    fireEvent.click(second as HTMLElement);
    expect(within(dialog).queryByText("DS4Windows 正在运行")).toBeNull();
    expect(document.activeElement).toBe(button(dialog, "不连接手柄，直接进入"));
    expect(app.store.getState().dismissedConflicts).toEqual([
      "Steam",
      "DS4Windows",
    ]);
  });

  it("has no entry in Settings to open it again", async () => {
    setup({ status: snapshot([]) });
    fireEvent.click(button(await page(), "关闭"));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(await screen.findByRole("combobox", { name: "语言" })).toBeTruthy();
    expect(screen.queryByText("入门向导")).toBeNull();
    expect(screen.queryByRole("button", { name: /入门向导/ })).toBeNull();
  });
});

describe("first-run page: going to the main page", () => {
  it("shows the pad, then goes to the main page by itself with the default in use", async () => {
    const app = setup();
    // Opened elsewhere first: going on shows the mapping page.
    app.store.getState().setPage("settings");
    const dialog = await page();
    expect(live(dialog).textContent).toBe(
      "已连接：Test controller即将进入主页…",
    );
    expect(dialog.textContent).not.toContain("Steam");
    expect(document.activeElement).toBe(button(dialog, "进入主页"));
    expect(button(dialog, "进入主页").className).toBe("btn-primary");
    // Long enough to read the name.
    await wait(ADVANCE_MS - 500);
    expect(screen.getByRole("dialog")).toBe(dialog);
    await wait(500);
    await entered();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    expect(app.store.getState().page).toBe("overview");
    // The fixed default is in use; its keys are set on the mapping page.
    await screen.findByRole("group", { name: /Test controller/ });
    await showsDefault();
    await wait(ADVANCE_MS * 2);
    app.madeNothing();
  });

  it("waits for a controller and goes on once one connects", async () => {
    const app = setup({ status: snapshot([]) });
    const dialog = await page();
    await wait(ADVANCE_MS * 3);
    expect(screen.getByRole("dialog")).toBe(dialog);
    app.connect([pad]);
    expect(
      await within(dialog).findByText("已连接：Test controller"),
    ).toBeTruthy();
    // The action under the focus is now the primary one.
    await waitFor(() =>
      expect(document.activeElement).toBe(button(dialog, "进入主页")),
    );
    await wait(ADVANCE_MS);
    await entered();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    app.madeNothing();
  });

  it("starts the wait again when the controller goes away", async () => {
    const app = setup();
    const dialog = await page();
    await wait(ADVANCE_MS - 500);
    app.connect([]);
    await within(dialog).findByText("请通过 USB 或蓝牙连接手柄。");
    await wait(ADVANCE_MS * 2);
    expect(screen.getByRole("dialog")).toBe(dialog);
    app.connect([pad]);
    await within(dialog).findByText("已连接：Test controller");
    await wait(ADVANCE_MS - 500);
    expect(screen.getByRole("dialog")).toBe(dialog);
    await wait(500);
    await entered();
    app.madeNothing();
  });

  it("lists every connected controller", async () => {
    setup({ status: snapshot([pad, joycon]) });
    const dialog = await page();
    expect(live(dialog).textContent).toContain(
      "已连接：Test controller、Joy-Con (L)",
    );
  });

  it("goes at once with the button, and the timer does nothing more", async () => {
    const app = setup();
    app.store.getState().setPage("settings");
    const dialog = await page();
    fireEvent.click(button(dialog, "进入主页"));
    await entered();
    expect(app.store.getState().page).toBe("overview");
    // Somewhere else by now: the old timer does not move the page.
    app.store.getState().setPage("settings");
    await wait(ADVANCE_MS * 2);
    expect(app.store.getState().page).toBe("settings");
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    app.madeNothing();
  });

  it("enters without a controller", async () => {
    const app = setup({ status: snapshot([]) });
    const dialog = await page();
    const action = button(dialog, "不连接手柄，直接进入");
    expect(action.className).toBe("btn");
    fireEvent.click(action);
    await entered();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    // The main page asks for a controller, with the default in use.
    expect(
      await screen.findByRole("heading", { name: "连接一个手柄" }),
    ).toBeTruthy();
    await showsDefault();
    app.madeNothing();
  });

  it("goes on once under StrictMode", async () => {
    const app = setup({ strict: true });
    app.store.getState().setPage("settings");
    await page();
    await wait(ADVANCE_MS);
    await entered();
    expect(app.store.getState().page).toBe("overview");
    app.store.getState().setPage("settings");
    await wait(ADVANCE_MS * 2);
    expect(app.store.getState().page).toBe("settings");
    app.madeNothing();
  });

  it("stays on the page it was on when closed, and no timer fires later", async () => {
    const app = setup();
    app.store.getState().setPage("settings");
    const dialog = await page();
    await wait(ADVANCE_MS - 500);
    fireEvent.click(button(dialog, "关闭"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe("done");
    await wait(ADVANCE_MS * 3);
    expect(app.store.getState().page).toBe("settings");
    app.madeNothing();
  });

  it("speaks English and names the default Default", async () => {
    const app = setup({ language: "en", status: snapshot([]) });
    const dialog = await page("Connect your controller");
    expect(live(dialog).textContent).toBe(
      "Connect a controller by USB or Bluetooth.",
    );
    expect(button(dialog, "Continue without a controller")).toBeTruthy();
    expect(button(dialog, "Close")).toBeTruthy();
    const texts = [dialog.textContent ?? ""];
    app.connect([pad]);
    await within(dialog).findByText("Connected: Test controller");
    expect(dialog.textContent).toContain("Opening the main page…");
    texts.push(dialog.textContent ?? "");
    for (const text of texts) expect(text).not.toMatch(/layer/i);
    fireEvent.click(button(dialog, "Go to the main page"));
    await entered();
    // The stored name is 默认; the UI names the default by its ID.
    await waitFor(() =>
      expect(screen.getByTitle("Profile menu").textContent).toContain(
        "Default",
      ),
    );
    app.madeNothing();
  });

  it("never says 层", async () => {
    const app = setup({ status: snapshot([]) });
    const dialog = await page();
    const texts = [dialog.textContent ?? ""];
    app.connect([pad]);
    await within(dialog).findByText("已连接：Test controller");
    texts.push(dialog.textContent ?? "");
    for (const text of texts) expect(text).not.toMatch(/层/);
  });
});
