// What the jsdom accessibility pass (a11y.test.tsx, UI-24) renders and how it
// asks axe. Not part of the app: only the tests import it.
import { act, render, screen } from "@testing-library/react";
import axe, { type AxeResults, type RunOptions } from "axe-core";
import { StrictMode } from "react";
import { vi } from "vitest";
import {
  type A11yException,
  exceptionsFor,
  isExcused,
} from "../../tests/a11y/exceptions";
import type {
  ConfigGetResult,
  ConnectionState,
  DaemonEvent,
  DeviceEntry,
  MacroList,
  PadmateAPI,
  ProfileEntry,
  ScriptStatusEntry,
  StatusResult,
} from "../shared/api";
import { App } from "./App";
import { editorAPIFixture } from "./editor-api-fixture";
import { createPadmateStore } from "./store";

/**
 * Every rule that can be decided without layout. jsdom has no layout, so the
 * rules that need it are off here: contrast and target size are checked by
 * theme-contrast.test.ts and by the Electron pass (a11y.spec.ts), and
 * scrollable regions and landmark regions need rendered boxes.
 */
export const axeOptions: RunOptions = {
  runOnly: {
    type: "tag",
    values: [
      "wcag2a",
      "wcag2aa",
      "wcag21a",
      "wcag21aa",
      "wcag22aa",
      "best-practice",
    ],
  },
  rules: {
    "color-contrast": { enabled: false },
    "target-size": { enabled: false },
    "scrollable-region-focusable": { enabled: false },
    // Whether there is one main and one h1 is asserted by the tests: axe
    // leaves both undecided without layout.
    "landmark-one-main": { enabled: false },
    "page-has-heading-one": { enabled: false },
  },
};

/**
 * jsdom has no layout and so no hit testing: document.elementFromPoint and
 * elementsFromPoint do not exist. axe asks for them as soon as a dialog[open]
 * is on the page, to find out whether it is modal, and without them the run
 * fails in 24 rules with "document.elementFromPoint is not a function". axe
 * does not report that as a violation but as an incomplete rule, so a page
 * with a dialog open passed whatever it contained: every dialog of this pass
 * was checked by nothing until this was found. Answering "nothing is there"
 * makes axe take no dialog for modal, so it checks the whole document, the
 * page behind the dialog too (a real window has that inert; jsdom does not).
 */
function stubHitTesting() {
  const doc = document as {
    elementFromPoint?: unknown;
    elementsFromPoint?: unknown;
  };
  doc.elementFromPoint ??= () => null;
  doc.elementsFromPoint ??= () => [];
}

/**
 * The rules axe could not run. It reports a rule that threw as incomplete,
 * with this message, next to the rules that only need a person to look.
 */
export function rulesThatFailed(results: AxeResults): string[] {
  return results.incomplete
    .filter((rule) =>
      rule.nodes.some((node) =>
        [...node.any, ...node.all, ...node.none].some((check) =>
          check.message.startsWith("Axe encountered an error"),
        ),
      ),
    )
    .map((rule) => rule.id);
}

/**
 * One line per violating node, or nothing: what the test compares to []. A
 * rule that axe could not run is a line too: a pass that cannot run proves
 * nothing.
 */
export async function axeReport(
  root: Document | Element = document,
  scope: A11yException[] = exceptionsFor("jsdom"),
): Promise<string[]> {
  stubHitTesting();
  const results: AxeResults = await axe.run(root as Element, axeOptions);
  const report: string[] = rulesThatFailed(results).map(
    (id) => `${id}: axe could not run this rule (it threw)`,
  );
  for (const violation of results.violations)
    for (const node of violation.nodes) {
      const target = node.target.join(" ");
      const found = document.querySelector(target);
      if (
        isExcused(
          scope,
          violation.id,
          (selector) => !!found && found.matches(selector),
        )
      )
        continue;
      report.push(
        `${violation.id} (${violation.impact}): ${violation.help} — ${target} — ${node.html.slice(0, 160)}`,
      );
    }
  return report;
}

/** The page has what axe cannot decide here: one main and one h1. */
export function pageStructure() {
  return {
    main: document.querySelectorAll("main").length,
    h1: [...document.querySelectorAll("h1")]
      .filter((heading) => !heading.closest("dialog:not([open])"))
      .map((heading) => heading.textContent),
  };
}

export const gamepad: DeviceEntry = {
  info: {
    id: 1,
    name: "Test controller",
    type: "switchpro",
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

/** A joystick SDL does not know: the mapping wizard is offered for it. */
export const joystick: DeviceEntry = {
  info: {
    id: 5,
    name: "Generic USB Joystick",
    type: "unknown",
    vendor: 0x0079,
    product: 0x0006,
    version: 0,
    guid: "ff00aabb000000000000000000007600",
    driver: "hidapi",
    path: "",
    serial: "",
    gamepad: false,
    virtual: false,
  },
  key: "03000000790000000600000000007600",
  slot: -1,
  status: "unmapped",
  disabled: false,
};

export function statusOf(
  devices: DeviceEntry[],
  profileId = "paint",
): StatusResult {
  return {
    version: "1.0.0",
    protocol: 1,
    dryRun: true,
    fakeInput: true,
    paused: false,
    capturing: false,
    suspended: "none",
    profileId,
    slots: [
      {
        slot: 0,
        device: 1,
        key: "controller",
        name: "Test controller",
        profile: "Paint",
        generation: 1,
        layers: { base: "base", overlays: [], color: "" },
        latches: [],
        mode: 0,
      },
    ],
    devices,
    conflicts: [
      {
        kind: "conflict",
        process: "steam.exe",
        message: "Steam is running",
        document: "docs/troubleshooting.md",
      },
    ],
    combos: [
      {
        device: 1,
        family: "standard",
        buttons: ["back", "start"],
        holdMs: 2000,
      },
    ],
  };
}

/** The mapping page's profile: a few keys of each kind of action. */
export const paintProfile = {
  schemaVersion: 0,
  name: "Paint",
  layers: {
    base: {
      south: {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      east: {
        label: "重做",
        press: { type: "keys", keys: ["Ctrl+Shift+Z", "Ctrl+Y"], mode: "tap" },
      },
      west: { press: { type: "text", text: "hello" } },
      lefttrigger: { press: { type: "keys", keys: "B" } },
    },
  },
};

/** The API the app gets, with the mocks of the editor's fixture. */
type FakeAPI = PadmateAPI & ReturnType<typeof editorAPIFixture>;

export interface HarnessOptions {
  devices?: DeviceEntry[];
  /** The library: id → document. Defaults to default, paint and photo. */
  library?: Record<string, object>;
  profileId?: string;
  config?: ConfigGetResult | Error;
  scripts?: ScriptStatusEntry[];
  macros?: MacroList;
  language?: "en" | "zh-CN";
  connection?: ConnectionState;
  /** Skip the first-run page: the key is what Onboarding remembers. */
  strict?: boolean;
}

/** Renders the app against an in-memory daemon. */
export function renderApp({
  devices = [gamepad],
  library = {
    default: { schemaVersion: 0, name: "默认", layers: { base: {} } },
    paint: paintProfile,
    photo: { schemaVersion: 0, name: "Photo", layers: { base: {} } },
  },
  profileId = "paint",
  config = { text: '{"schemaVersion":0}', revision: "rev" },
  scripts = [],
  macros = { macros: [], missing: [] },
  language = "zh-CN",
  connection = { phase: "connected" },
  strict = false,
}: HarnessOptions = {}) {
  localStorage.setItem("padmate.language", language);
  let listener = (_event: DaemonEvent) => {};
  const status = statusOf(devices, profileId);
  const fixture = editorAPIFixture();
  fixture.listProfiles.mockImplementation(
    async (): Promise<ProfileEntry[]> =>
      Object.entries(library).map(([id, doc]) => ({
        id,
        name: (doc as { name?: string }).name,
        revision: `r-${id}`,
        ...(id === "default" ? { default: true } : {}),
      })),
  );
  fixture.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(library[id] ?? { schemaVersion: 0 }),
    revision: `r-${id}`,
    diagnostics: [],
  }));
  fixture.listMacros.mockImplementation(async () => macros);
  const api = {
    ...fixture,
    getConnection: vi.fn(async () => connection),
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
    getConfig: vi.fn(async () => {
      if (config instanceof Error) throw config;
      return config;
    }),
    putConfig: vi.fn(async () => ({
      revision: "saved",
      applied: true,
      restartRequired: false,
    })),
    getScriptStatus: vi.fn(async () => scripts),
    getScriptRegistrations: vi.fn(async () => scripts),
    reloadScripts: vi.fn(async () => scripts),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
  } as unknown as FakeAPI;
  window.padmate = api;
  const store = createPadmateStore(api);
  const app = <App store={store} />;
  const view = render(strict ? <StrictMode>{app}</StrictMode> : app);
  return {
    api,
    store,
    view,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => listener({ topic, data })),
  };
}

/** Waits for the mapping page's controller. */
export const controller = () =>
  screen.findByRole("group", { name: /Test controller/ });
