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
import userEvent from "@testing-library/user-event";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStore } from "zustand";
import type {
  AmgpImported,
  AmgpReportLine,
  DaemonEvent,
  InstalledApp,
  PadmateAPI,
  ProfileEntry,
  ProfileImportOptions,
  ProfileImportResult,
  RecentApp,
  StatusResult,
} from "../../shared/api";
import type { Config } from "../../shared/generated/schema";
import { forgetAppIcons } from "../app-icons";
import { editorAPIFixture } from "../editor-api-fixture";
import { slugId } from "../library-model";
import { CAPTURE_TIMEOUT_MS, createPadmateStore } from "../store";
import { boundHint, pickableApps } from "./InstalledAppsDialog";
import { NewProfileDialog } from "./ProfileDialogs";
import { ProfilesPage } from "./ProfilesPage";

const status: StatusResult = {
  version: "test",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "",
  profileId: "paint",
  slots: [],
  devices: [],
};

const conflictError = { message: "revision conflict", code: -32009 };

function profileText(name: string) {
  return `${JSON.stringify({ schemaVersion: 1, name, layers: { base: {} } }, null, 2)}\n`;
}

interface StoredProfile {
  text: string;
  revision: string;
  problem?: string;
}

/** A .padmate file the open dialog picks; its name is the profile's. */
interface ProfileFile {
  name: string;
}

/**
 * A daemon with a profile library in memory. Like padmated, it lists the
 * fixed default ("default") unless profiles says otherwise, and refuses to
 * delete or rename it.
 */
function fakeDaemon({
  config,
  profiles = { default: "默认", paint: "Paint", photo: "Photo" },
  active = "paint",
  recent = [],
  broken = [],
  startup = "",
  created = {},
  appPaths = {},
  iconless = false,
}: {
  config: Config | string;
  /** No executable has an icon: Main answers "" for every one. */
  iconless?: boolean;
  /** What Main finds for an app bound by file name: lower-case name → path. */
  appPaths?: Record<string, string>;
  profiles?: Record<string, string>;
  /** When each profile's folder was created; the daemon omits unknown ones. */
  created?: Record<string, number>;
  active?: string;
  recent?: RecentApp[];
  broken?: string[];
  /** The profile padmated was started with, which it never deletes. */
  startup?: string;
}) {
  let listener = (_event: DaemonEvent) => {};
  let revision = 0;
  const daemon = {
    active,
    config: {
      text:
        typeof config === "string"
          ? config
          : `${JSON.stringify(config, null, 2)}\n`,
      revision: "c0",
    },
    profiles: new Map<string, StoredProfile>(
      Object.entries(profiles).map(([id, name]) => [
        id,
        {
          text: profileText(name),
          revision: `${id}-0`,
          ...(broken.includes(id) ? { problem: "invalid JSON" } : {}),
        },
      ]),
    ),
    /** What the open dialog picks next; null cancels it. */
    nextFile: null as ProfileFile | null,
    /**
     * Why padmated cannot write a profile based on the one deleted out whole
     * first (-32023): null lets it.
     */
    childBlocked: null as "script" | "invalid" | null,
  };
  // Files Main read for a clash, by token, as Main keeps them.
  const pending = new Map<string, ProfileFile>();
  let tokens = 0;
  const list = (): ProfileEntry[] =>
    [...daemon.profiles].map(([id, entry]) => {
      let name: string | undefined;
      let based: string | undefined;
      try {
        name = JSON.parse(entry.text).name;
        based = JSON.parse(entry.text).extends;
      } catch {
        name = undefined;
      }
      return {
        id,
        ...(name ? { name } : {}),
        ...(based ? { extends: based } : {}),
        ...(id === "default" ? { default: true } : {}),
        ...(created[id] === undefined ? {} : { created: created[id] }),
        revision: entry.revision,
        ...(entry.problem ? { problem: entry.problem } : {}),
      };
    });
  const api = {
    ...editorAPIFixture(),
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next: (event: DaemonEvent) => void) => {
      listener = next;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => ({
      ...status,
      profileId: daemon.active,
    })),
    getDevices: vi.fn(async () => []),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    getConfig: vi.fn(async () => ({ ...daemon.config, diagnostics: [] })),
    putConfig: vi.fn(async (text: string, expected: string) => {
      if (expected !== daemon.config.revision) throw conflictError;
      daemon.config = { text, revision: `c${++revision}` };
      return {
        revision: daemon.config.revision,
        applied: true,
        restartRequired: false,
      };
    }),
    listProfiles: vi.fn(async () => list()),
    getProfile: vi.fn(async (id: string) => {
      const entry = daemon.profiles.get(id);
      if (!entry) throw { message: "not found" };
      return {
        id,
        text: entry.text,
        revision: entry.revision,
        diagnostics: [],
        ...(entry.problem ? { problem: entry.problem } : {}),
      };
    }),
    putProfile: vi.fn(async (id: string, text: string, expected: string) => {
      const entry = daemon.profiles.get(id);
      if ((entry?.revision ?? "") !== expected) throw conflictError;
      if (
        id === "default" &&
        entry &&
        JSON.parse(entry.text).name !== JSON.parse(text).name
      )
        throw { message: "fixed", code: -32018, data: { op: "rename" } };
      const next = `${id}-${++revision}`;
      daemon.profiles.set(id, { text, revision: next });
      return { revision: next, applied: true };
    }),
    copyProfile: vi.fn(async (from: string, to: string) => {
      const entry = daemon.profiles.get(from);
      if (!entry) throw { message: "not found" };
      if (daemon.profiles.has(to))
        throw { message: "profile exists", code: -32014 };
      daemon.profiles.set(to, { text: entry.text, revision: `${to}-0` });
      return { revision: `${to}-0` };
    }),
    // Refuses in padmated's order: active, startup, revision, config.json.
    deleteProfile: vi.fn(async (id: string, expected: string) => {
      if (id === "default")
        throw { message: "fixed", code: -32018, data: { op: "delete" } };
      if (id === daemon.active)
        throw { message: "profile is active", code: -32012 };
      if (id === startup)
        throw {
          message: "padmate was started with this profile",
          code: -32013,
          data: { references: ["startup"] },
        };
      if ((daemon.profiles.get(id)?.revision ?? "") !== expected)
        throw conflictError;
      const current: Config = JSON.parse(daemon.config.text);
      const references = (current.rules ?? []).flatMap((rule, index) =>
        rule.profile === id ? [`rules/${index}`] : [],
      );
      if (references.length)
        throw {
          message: "config.json still selects the profile",
          code: -32013,
          data: { references },
        };
      // The profiles based on it are written out whole first (PRO-19), all or
      // none.
      const children = [...daemon.profiles].filter(([, stored]) => {
        try {
          return JSON.parse(stored.text).extends === id;
        } catch {
          return false;
        }
      });
      if (children.length && daemon.childBlocked)
        throw {
          message: "a profile based on it cannot be written out whole",
          code: -32023,
          data: { id: children[0][0], reason: daemon.childBlocked },
        };
      for (const [childId, stored] of children) {
        const { extends: _extends, ...whole } = JSON.parse(stored.text);
        daemon.profiles.set(childId, {
          text: JSON.stringify(whole),
          revision: `${childId}-${++revision}`,
        });
      }
      daemon.profiles.delete(id);
    }),
    activateProfile: vi.fn(async (id: string | null) => {
      daemon.active = id ?? "";
      return { profileId: daemon.active, generation: 2 };
    }),
    exportProfile: vi.fn(async (id: string, _name: string) => {
      if (!daemon.profiles.has(id))
        throw { message: "library document not found", code: -32004 };
      return { saved: true };
    }),
    // Like padmated: a new profile unless another goes by the name, as
    // the UI compares names; the fixed default keeps its name.
    importProfile: vi.fn(
      async (options?: ProfileImportOptions): Promise<ProfileImportResult> => {
        let file: ProfileFile | undefined;
        if (options) {
          file = pending.get(options.token);
          pending.delete(options.token);
          if (!file)
            throw {
              message: "Choose the file again",
              data: { reason: "expired" },
            };
        } else if (daemon.nextFile) file = daemon.nextFile;
        else return { canceled: true };
        const replace =
          options && "replace" in options ? options.replace : undefined;
        const name =
          options && "name" in options ? options.name.trim() : file.name;
        const key = (value: string) => value.trim().toLowerCase();
        if (!replace) {
          const clash = list().find((entry) =>
            (entry.id === "default"
              ? ["默认", "Default"]
              : [entry.name ?? entry.id]
            ).some((taken) => key(taken) === key(name)),
          );
          if (clash) {
            const token = String(++tokens).padStart(32, "0");
            pending.set(token, file);
            return { exists: true, id: clash.id, name, token };
          }
          const id = slugId(name, daemon.profiles.keys());
          daemon.profiles.set(id, {
            text: profileText(name),
            revision: `${id}-0`,
          });
          return { id, name, revision: `${id}-0`, applied: false };
        }
        const stored = daemon.profiles.get(replace);
        if (!stored)
          throw { message: "library document not found", code: -32004 };
        const kept =
          replace === "default" ? JSON.parse(stored.text).name : name;
        const next = `${replace}-${++revision}`;
        daemon.profiles.set(replace, {
          text: profileText(kept),
          revision: next,
        });
        return {
          id: replace,
          name: kept,
          revision: next,
          applied: replace === daemon.active,
        };
      },
    ),
    getRecentApps: vi.fn(async () => recent),
    // An icon named after the file, so a test can tell which was read.
    getAppIcon: vi.fn(async (path: string) =>
      iconless ? "" : `data:image/png;base64,${path.split("\\").pop()}`,
    ),
    resolveAppPath: vi.fn(
      async (exe: string) => appPaths[exe.toLowerCase()] ?? "",
    ),
    getScriptStatus: vi.fn(async () => []),
    reloadScripts: vi.fn(async () => []),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled: boolean) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
  } satisfies PadmateAPI;
  return {
    api,
    daemon,
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => listener({ topic, data })),
    written: (): Config => JSON.parse(daemon.config.text),
  };
}

async function setup(
  options: Parameters<typeof fakeDaemon>[0] & {
    language?: "zh-CN" | "en";
  },
) {
  const fake = fakeDaemon(options);
  const store = createPadmateStore(fake.api);
  if (options.language) store.getState().setLanguage(options.language);
  const openMapping = vi.fn();
  // The shell's activation, which saves the mapping page's edits first.
  const editKeys = vi.fn(async (id: string) => {
    if ((await store.getState().activateProfile(id)).ok) openMapping();
  });
  // The mapping page's flush (UI-43).
  const saveEdits = vi.fn(async (_id: string) => true);
  // The shell opens 配置设置 for a card's profile (UI-27).
  const profileSettings = vi.fn((_id: string) => {});
  function Host() {
    const state = useStore(store);
    const start = state.start;
    useEffect(() => start(), [start]);
    return (
      <ProfilesPage
        state={state}
        onOpenMapping={openMapping}
        onEditKeys={editKeys}
        onProfileSettings={profileSettings}
        saveEdits={saveEdits}
      />
    );
  }
  render(<Host />);
  await waitFor(() => {
    expect(store.getState().profilesLoaded).toBe(true);
    expect(store.getState().config).not.toBeNull();
    expect(store.getState().status).not.toBeNull();
  });
  return {
    ...fake,
    store,
    openMapping,
    editKeys,
    saveEdits,
    profileSettings,
    user: userEvent.setup(),
  };
}

const noEditKeys = async () => {};

const cardOf = (name: string) => screen.getByRole("article", { name });

function chooseMenuItem(name: string | RegExp) {
  fireEvent.click(screen.getByRole("menuitem", { name }));
}

function openCardMenu(name: string) {
  fireEvent.click(
    within(cardOf(name)).getByRole("button", {
      name: `「${name}」的更多操作`,
    }),
  );
}

/** The card's own 复制 button (ADR-0076). */
function duplicate(name: string) {
  fireEvent.click(within(cardOf(name)).getByRole("button", { name: "复制" }));
}

function openPicker(scope: HTMLElement, name = "添加软件") {
  fireEvent.click(within(scope).getByRole("button", { name }));
}

/** The new-profile dialog alone, open, on a loaded store. */
async function dialogSetup(
  options: Parameters<typeof fakeDaemon>[0] & { language?: "zh-CN" | "en" },
) {
  const fake = fakeDaemon(options);
  const store = createPadmateStore(fake.api);
  // Not setLanguage, which would remember it for later tests.
  if (options.language) store.setState({ language: options.language });
  const stop = store.getState().start();
  await waitFor(() => {
    expect(store.getState().profilesLoaded).toBe(true);
    expect(store.getState().status).not.toBeNull();
  });
  const created = vi.fn();
  function Host() {
    const state = useStore(store);
    return (
      <NewProfileDialog
        open
        state={state}
        onClose={() => {}}
        onCreated={created}
      />
    );
  }
  render(<Host />);
  return {
    ...fake,
    store,
    stop,
    created,
    dialog: screen.getByRole("dialog"),
    user: userEvent.setup(),
  };
}

const nameField = (scope: HTMLElement, name = "名称") =>
  within(scope).getByRole("textbox", { name }) as HTMLInputElement;

const baseConfig: Config = {
  schemaVersion: 0,
  rules: [
    { exe: "krita.exe", profile: "paint", device: "any" },
    { exe: "C:\\Apps\\Clip\\clip.exe", profile: "paint", enabled: false },
    { exe: "photoshop.exe", profile: "photo" },
  ],
};

/** An older config.json with what ADR-0053 retired. */
const legacyConfig = {
  ...baseConfig,
  defaultProfile: "photo",
  onUnmatched: "none",
  ignore: ["explorer.exe"],
  rules: [...(baseConfig.rules ?? []), { exe: "game.exe", profile: null }],
} as unknown as Config;

beforeEach(() => {
  localStorage.clear();
  forgetAppIcons();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("profile cards", () => {
  it("lists the fixed default first, then by creation time, not the active profile first", async () => {
    await setup({
      config: baseConfig,
      profiles: {
        photo: "Photo",
        paint: "Paint",
        blank: "Another",
        default: "Stored name",
      },
      created: { default: 9, photo: 1, blank: 2, paint: 3 },
    });
    // paint is active but the newest of the three: it stays last.
    expect(
      screen
        .getAllByRole("article")
        .map((card) => card.getAttribute("aria-labelledby")),
    ).toEqual([
      "profile-card-default",
      "profile-card-photo",
      "profile-card-blank",
      "profile-card-paint",
    ]);
    expect(within(cardOf("Paint")).getByText("使用中")).toBeTruthy();
  });

  it("lists the fixed default first, then by name without creation times, with apps as icons", async () => {
    const { api } = await setup({
      config: baseConfig,
      profiles: {
        photo: "Photo",
        paint: "Paint",
        blank: "Another",
        default: "Stored name",
      },
    });
    const cards = screen.getAllByRole("article");
    expect(cards.map((card) => card.getAttribute("aria-labelledby"))).toEqual([
      "profile-card-default",
      "profile-card-blank",
      "profile-card-paint",
      "profile-card-photo",
    ]);
    // The default goes by 默认 whatever it stores, says what it is for and
    // takes no apps: every app without a binding uses it.
    const fixed = cardOf("默认");
    expect(fixed.getAttribute("data-default")).toBe("true");
    expect(within(fixed).getByText("未绑定的软件使用此配置")).toBeTruthy();
    expect(
      within(fixed).queryByRole("button", { name: "添加软件" }),
    ).toBeNull();
    expect(screen.queryByText("Stored name")).toBeNull();
    const paint = cardOf("Paint");
    expect(within(paint).getByText("使用中")).toBeTruthy();
    expect(within(cardOf("Photo")).queryByText("使用中")).toBeNull();
    // No profile is tagged as the default any more.
    expect(screen.queryByText("默认", { selector: ".tag" })).toBeNull();
    // A disabled binding is greyed; a path rule's tooltip shows the path.
    const clip = within(paint).getByRole("button", { name: "clip.exe 已停用" });
    expect(clip.getAttribute("title")).toBe(
      "C:\\Apps\\Clip\\clip.exe（已停用）",
    );
    expect(clip.closest("li")?.getAttribute("data-enabled")).toBe("false");
    // Its icon comes from Main, read from the rule's path.
    await waitFor(() =>
      expect(clip.querySelector("img")?.getAttribute("src")).toBe(
        "data:image/png;base64,clip.exe",
      ),
    );
    expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Apps\\Clip\\clip.exe");
    // No path is known for krita.exe, and Main finds none: the generic
    // application icon stands in, never a letter.
    const krita = within(paint).getByRole("button", { name: "krita.exe" });
    await waitFor(() =>
      expect(api.resolveAppPath).toHaveBeenCalledWith("krita.exe"),
    );
    expect(krita.textContent).toBe("krita.exe");
    expect(krita.querySelector("img")).toBeNull();
    expect(krita.querySelector(".app-icon-generic svg")).not.toBeNull();
    expect(api.getAppIcon).toHaveBeenCalledTimes(1);
    // The name is no visible text, only the icon's name and tooltip.
    expect(krita.querySelector(".visually-hidden")?.textContent).toBe(
      "krita.exe",
    );
    // An empty profile has only its 添加软件.
    const another = cardOf("Another");
    expect(within(another).queryAllByRole("listitem")).toHaveLength(1);
    expect(
      within(another).getByRole("button", { name: "添加软件" }),
    ).toBeTruthy();
  });

  it("finds an icon for an app bound by name once it is seen", async () => {
    const { api } = await setup({
      config: baseConfig,
      recent: [
        { exe: "krita.exe", path: "C:\\Krita\\krita.exe", elevated: false },
      ],
    });
    // The page reads the recent apps when it opens; the store keeps them.
    await waitFor(() => expect(api.getRecentApps).toHaveBeenCalled());
    const krita = within(cardOf("Paint")).getByRole("button", {
      name: "krita.exe",
    });
    await waitFor(() =>
      expect(krita.querySelector("img")?.getAttribute("src")).toBe(
        "data:image/png;base64,krita.exe",
      ),
    );
    // Remembered for a later session, when krita.exe is not seen yet.
    expect(
      JSON.parse(localStorage.getItem("padmate.appPaths") ?? "{}"),
    ).toEqual({ "krita.exe": "C:\\Krita\\krita.exe" });
  });

  it("asks Main for the path of an app bound by name that was not seen", async () => {
    const found = "C:\\Program Files\\Krita (x64)\\bin\\krita.exe";
    const { api } = await setup({
      config: baseConfig,
      appPaths: { "krita.exe": found },
    });
    const krita = within(cardOf("Paint")).getByRole("button", {
      name: "krita.exe",
    });
    await waitFor(() =>
      expect(krita.querySelector("img")?.getAttribute("src")).toBe(
        "data:image/png;base64,krita.exe",
      ),
    );
    expect(api.getAppIcon).toHaveBeenCalledWith(found);
    expect(krita.querySelector(".app-icon-generic")).toBeNull();
    // photoshop.exe is Photo's, and Main finds nothing for it: the generic
    // application icon, no letter.
    const photo = within(cardOf("Photo")).getByRole("button", {
      name: "photoshop.exe",
    });
    await waitFor(() =>
      expect(api.resolveAppPath).toHaveBeenCalledWith("photoshop.exe"),
    );
    expect(photo.querySelector("img")).toBeNull();
    expect(photo.querySelector(".app-icon-generic svg")).not.toBeNull();
    expect(photo.textContent).toBe("photoshop.exe");
    // Remembered for a later window, which then needs no search.
    expect(
      JSON.parse(localStorage.getItem("padmate.appPaths") ?? "{}"),
    ).toEqual({ "krita.exe": found });
  });

  it("shows the generic icon, not a letter, when the file of a path has no icon", async () => {
    const { api } = await setup({ config: baseConfig, iconless: true });
    const clip = within(cardOf("Paint")).getByRole("button", {
      name: "clip.exe 已停用",
    });
    await waitFor(() =>
      expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Apps\\Clip\\clip.exe"),
    );
    expect(clip.querySelector("img")).toBeNull();
    expect(clip.querySelector(".app-icon-generic svg")).not.toBeNull();
    expect(clip.textContent).toBe("clip.exe 已停用");
  });

  it("dims the icons while switching is off and writes config.enabled", async () => {
    const { written, user } = await setup({
      config:
        '{\n  "schemaVersion": 0,\n  "future": {"x": 1},\n  "rules": [{"exe": "krita.exe", "profile": "paint", "device": "any"}]\n}\n',
    });
    const toggle = screen.getByRole("switch", { name: "按软件自动切换配置" });
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await user.click(toggle);
    await waitFor(() => expect(written().enabled).toBe(false));
    // Unknown members and the order of existing ones stay.
    expect(Object.keys(written())).toEqual([
      "schemaVersion",
      "future",
      "rules",
      "enabled",
    ]);
    expect(written().rules?.[0]).toEqual({
      exe: "krita.exe",
      profile: "paint",
      device: "any",
    });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-checked")).toBe("false"),
    );
    expect(
      document
        .querySelector(".page-column-profiles")
        ?.getAttribute("data-switching"),
    ).toBe("off");
  });

  it("shows the save state briefly", async () => {
    const { user } = await setup({ config: baseConfig });
    await user.click(
      screen.getByRole("switch", { name: "按软件自动切换配置" }),
    );
    const pill = document.querySelector(".page-autosave");
    await waitFor(() => expect(pill?.textContent).toBe("已保存"));
  });

  it("turns a binding off and unbinds it from the icon's menu", async () => {
    const { written } = await setup({ config: baseConfig });
    const paint = cardOf("Paint");
    fireEvent.click(within(paint).getByRole("button", { name: "krita.exe" }));
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual(["停用", "解除绑定"]);
    chooseMenuItem("停用");
    await waitFor(() =>
      expect(written().rules?.[0]).toEqual({
        exe: "krita.exe",
        profile: "paint",
        device: "any",
        enabled: false,
      }),
    );
    const krita = await within(paint).findByRole("button", {
      name: "krita.exe 已停用",
    });
    expect(krita.closest("li")?.getAttribute("data-enabled")).toBe("false");
    fireEvent.click(
      within(paint).getByRole("button", { name: "clip.exe 已停用" }),
    );
    chooseMenuItem("解除绑定");
    await waitFor(() =>
      expect(written().rules?.map((rule) => rule.exe)).toEqual([
        "krita.exe",
        "photoshop.exe",
      ]),
    );
    // Focus stays in the list: the icon before it, then 添加软件.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(paint).getByRole("button", { name: "krita.exe 已停用" }),
      ),
    );
    fireEvent.click(
      within(paint).getByRole("button", { name: "krita.exe 已停用" }),
    );
    chooseMenuItem("解除绑定");
    await waitFor(() =>
      expect(written().rules?.map((rule) => rule.exe)).toEqual([
        "photoshop.exe",
      ]),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(paint).getByRole("button", { name: "添加软件" }),
      ),
    );
  });

  it("asks before turning on a rule that another profile's rule overrides", async () => {
    const { written, user } = await setup({
      config: {
        schemaVersion: 0,
        rules: [
          { exe: "krita.exe", profile: "photo" },
          { exe: "krita.exe", profile: "paint", enabled: false },
        ],
      },
    });
    fireEvent.click(
      within(cardOf("Paint")).getByRole("button", {
        name: "krita.exe 已停用",
      }),
    );
    chooseMenuItem("启用");
    const dialog = await screen.findByRole("dialog", {
      name: "此软件已绑定",
    });
    expect(dialog.textContent).toContain(
      "krita.exe 当前使用「Photo」，是否改为「Paint」？",
    );
    await user.click(within(dialog).getByRole("button", { name: "更改" }));
    await waitFor(() =>
      expect(written().rules).toEqual([{ exe: "krita.exe", profile: "paint" }]),
    );
  });

  it("keeps the fixed default: its keys and copies, but no rename or delete", async () => {
    const { api, openMapping, editKeys, user } = await setup({
      config: baseConfig,
    });
    openCardMenu("默认");
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual(["配置设置…", "导出…"]);
    fireEvent.keyDown(document.body, { key: "Escape" });
    duplicate("默认");
    await waitFor(() =>
      expect(api.copyProfile).toHaveBeenCalledWith("default", "profile"),
    );
    expect(
      await screen.findByRole("article", { name: "默认 副本" }),
    ).toBeTruthy();
    expect(screen.getByText("已复制为「默认 副本」")).toBeTruthy();
    // Its keys are edited like any profile's.
    await user.click(
      within(cardOf("默认")).getByRole("button", { name: "编辑按键" }),
    );
    await waitFor(() => expect(openMapping).toHaveBeenCalled());
    expect(editKeys).toHaveBeenCalledWith("default");
    // No menu anywhere sets a default.
    openCardMenu("Photo");
    expect(screen.queryByRole("menuitem", { name: "设为默认" })).toBeNull();
    expect(api.deleteProfile).not.toHaveBeenCalled();
  });

  it("shows apps an older file binds to the default, without adding more", async () => {
    const { written } = await setup({
      config: {
        schemaVersion: 0,
        rules: [{ exe: "notepad.exe", profile: "default" }],
      },
    });
    const fixed = cardOf("默认");
    expect(
      within(fixed).queryByRole("button", { name: "添加软件" }),
    ).toBeNull();
    fireEvent.click(within(fixed).getByRole("button", { name: "notepad.exe" }));
    chooseMenuItem("解除绑定");
    await waitFor(() => expect(written().rules).toEqual([]));
  });

  it("shows nothing of what ADR-0053 retired and drops it on the next write", async () => {
    const { written, user } = await setup({ config: legacyConfig });
    expect(screen.queryByRole("region", { name: "其他软件" })).toBeNull();
    expect(screen.queryByText("高级")).toBeNull();
    expect(screen.queryByText(/不切换/)).toBeNull();
    expect(screen.queryByText(/停用 padmate/)).toBeNull();
    expect(screen.queryByRole("button", { name: /game\.exe/ })).toBeNull();
    expect(screen.queryByRole("radiogroup")).toBeNull();
    // Photo was the default; nothing says so now.
    expect(within(cardOf("Photo")).queryByText("默认")).toBeNull();
    await user.click(
      screen.getByRole("switch", { name: "按软件自动切换配置" }),
    );
    await waitFor(() => expect(written().enabled).toBe(false));
    expect(written()).toEqual({ ...baseConfig, enabled: false });
  });

  it("marks a broken profile and keeps only the safe actions", async () => {
    await setup({ config: baseConfig, broken: ["photo"] });
    const photo = cardOf("Photo");
    expect(within(photo).getByText("配置文件有错误")).toBeTruthy();
    // It cannot be activated, so its keys cannot be opened.
    const edit = within(photo).getByRole("button", {
      name: "编辑按键",
    }) as HTMLButtonElement;
    expect(edit.disabled).toBe(true);
    // The tooltip says why; the name stays the label.
    expect(edit.title).toBe("配置文件有错误，无法打开");
    expect(edit.getAttribute("aria-label")).toBe("编辑按键");
    openCardMenu("Photo");
    const item = (name: string) =>
      screen.getByRole("menuitem", { name }) as HTMLButtonElement;
    expect(item("重命名…").disabled).toBe(true);
    expect(item("导出…").disabled).toBe(true);
    expect(item("删除…").disabled).toBe(false);
    // The copy button cannot copy what cannot be renamed afterwards.
    fireEvent.keyDown(document.body, { key: "Escape" });
    const copy = within(photo).getByRole("button", {
      name: "复制",
    }) as HTMLButtonElement;
    expect(copy.disabled).toBe(true);
  });

  it("still opens a broken profile that is in use", async () => {
    const { openMapping, user } = await setup({
      config: baseConfig,
      broken: ["paint"],
    });
    const edit = within(cardOf("Paint")).getByRole("button", {
      name: "编辑按键",
    }) as HTMLButtonElement;
    expect(edit.disabled).toBe(false);
    await user.click(edit);
    expect(openMapping).toHaveBeenCalledTimes(1);
  });

  it("explains a session without a profile library", async () => {
    const fake = fakeDaemon({ config: baseConfig });
    fake.api.listProfiles.mockRejectedValue({
      message: "profile library unavailable; start with --data-dir",
      code: -32008,
    });
    fake.api.getConfig.mockRejectedValue(
      new Error("configuration unavailable"),
    );
    const store = createPadmateStore(fake.api);
    function Host() {
      const state = useStore(store);
      const start = state.start;
      useEffect(() => start(), [start]);
      return (
        <ProfilesPage
          state={state}
          onEditKeys={noEditKeys}
          onProfileSettings={noEditKeys}
        />
      );
    }
    render(<Host />);
    expect(await screen.findByText("当前启动方式无法管理配置")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "新建配置" })).toBeNull();
  });

  it("keeps the app lists usable when only the profile list fails", async () => {
    const fake = fakeDaemon({ config: baseConfig });
    fake.api.listProfiles.mockRejectedValue(new Error("disk busy"));
    const store = createPadmateStore(fake.api);
    function Host() {
      const state = useStore(store);
      const start = state.start;
      useEffect(() => start(), [start]);
      return (
        <ProfilesPage
          state={state}
          onEditKeys={noEditKeys}
          onProfileSettings={noEditKeys}
        />
      );
    }
    render(<Host />);
    expect(await screen.findByText("无法读取配置列表")).toBeTruthy();
    expect(screen.getByText("disk busy")).toBeTruthy();
    fake.api.listProfiles.mockImplementation(async () => [
      { id: "paint", name: "Paint", revision: "paint-0" },
    ]);
    fireEvent.click(screen.getByRole("button", { name: "重新读取" }));
    expect(await screen.findByRole("article", { name: "Paint" })).toBeTruthy();
    expect(screen.queryByText("无法读取配置列表")).toBeNull();
  });
});

describe("adding apps", () => {
  const recent: RecentApp[] = [
    { exe: "krita.exe", path: "C:\\Krita\\krita.exe", elevated: false },
    { exe: "photoshop.exe", path: "C:\\PS\\photoshop.exe", elevated: false },
    { exe: "notepad.exe", path: "C:\\Windows\\notepad.exe", elevated: true },
    { exe: "Notepad.exe", path: "D:\\Other\\Notepad.exe", elevated: false },
    { exe: "game.exe", path: "C:\\Games\\game.exe", elevated: false },
  ];

  const installed: InstalledApp[] = [
    {
      name: "Krita",
      exe: "krita.exe",
      path: "C:\\Krita\\krita.exe",
    },
    {
      name: "Blender",
      exe: "blender.exe",
      path: "C:\\Blender\\blender.exe",
    },
    {
      name: "Photoshop 2026",
      exe: "photoshop.exe",
      path: "C:\\PS\\photoshop.exe",
    },
    {
      name: "画图",
      exe: "mspaint.exe",
      path: "C:\\Windows\\System32\\mspaint.exe",
    },
  ];

  async function openInstalled(scope: HTMLElement) {
    openPicker(scope);
    chooseMenuItem("从已安装的软件中选择…");
    return screen.findByRole("dialog", { name: "选择软件" });
  }

  const choices = (dialog: HTMLElement) =>
    within(dialog)
      .getAllByRole("button", { name: /\.exe/ })
      .map((button) => button.textContent);

  it("offers three ways to add an app, and no list of recent apps in the menu", async () => {
    await setup({ config: baseConfig, recent });
    openPicker(cardOf("Paint"));
    const menu = await screen.findByRole("menu", { name: "添加软件" });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["点选窗口…", "从已安装的软件中选择…", "浏览…"]);
    // Typing a program name is gone.
    expect(screen.queryByText("手动输入…")).toBeNull();
  });

  it("opens a blocking dialog that lists recent apps first, then the installed ones", async () => {
    const { api } = await setup({ config: baseConfig, recent });
    api.listInstalledApps.mockResolvedValue(installed);
    const dialog = await openInstalled(cardOf("Paint"));
    // A modal dialog: the page behind it takes no clicks.
    expect(dialog.tagName).toBe("DIALOG");
    await within(dialog).findByText("Blender");
    await waitFor(() => expect(api.getRecentApps).toHaveBeenCalled());
    const rows = choices(dialog);
    // Recent apps the profile does not have (krita.exe is Paint's own),
    // one per program name, newest first; then the installed apps that are
    // neither recent nor Paint's.
    expect(rows).toEqual([
      "photoshop.exe已绑定「Photo」",
      "notepad.exe",
      "game.exe",
      "Blenderblender.exe",
      "画图mspaint.exe",
    ]);
    expect(within(dialog).getByText("最近使用")).toBeTruthy();
    expect(within(dialog).getByText("已安装")).toBeTruthy();
    // Nothing to type but a search; no placeholder example.
    const search = within(dialog).getByRole("searchbox", { name: "搜索" });
    expect(search.getAttribute("placeholder")).toBeNull();
    expect(document.activeElement).toBe(search);
  });

  it("shows the generic icon, not a letter, for an app without an icon", async () => {
    const { api } = await setup({
      config: baseConfig,
      recent,
      iconless: true,
    });
    api.listInstalledApps.mockResolvedValue(installed);
    const dialog = await openInstalled(cardOf("Paint"));
    await within(dialog).findByText("Blender");
    await waitFor(() =>
      expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Blender\\blender.exe"),
    );
    for (const row of within(dialog).getAllByRole("button", {
      name: /\.exe|Blender/,
    })) {
      const icon = row.querySelector(".app-choice-icon");
      if (!icon) continue;
      expect(icon.querySelector("img")).toBeNull();
      expect(icon.querySelector("svg")).not.toBeNull();
      expect(icon.textContent).toBe("");
    }
    expect(
      within(dialog)
        .getByRole("button", { name: /Blender/ })
        .querySelector(".app-choice-icon svg"),
    ).not.toBeNull();
  });

  it("shows each app's icon, read from its executable", async () => {
    const { api } = await setup({ config: baseConfig, recent });
    api.listInstalledApps.mockResolvedValue(installed);
    const dialog = await openInstalled(cardOf("Paint"));
    await within(dialog).findByText("Blender");
    await waitFor(() =>
      expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Blender\\blender.exe"),
    );
    expect(api.getAppIcon).toHaveBeenCalledWith("C:\\Games\\game.exe");
    await waitFor(() =>
      expect(
        within(dialog)
          .getByRole("button", { name: /Blender/ })
          .querySelector("img")
          ?.getAttribute("src"),
      ).toBe("data:image/png;base64,blender.exe"),
    );
  });

  it("filters by name or program name as the user types, and Enter takes the first match", async () => {
    const { api, written, user } = await setup({
      config: baseConfig,
      recent,
    });
    api.listInstalledApps.mockResolvedValue(installed);
    const dialog = await openInstalled(cardOf("Paint"));
    await within(dialog).findByText("Blender");
    const search = within(dialog).getByRole("searchbox", { name: "搜索" });
    await user.type(search, "PHOTO");
    expect(choices(dialog)).toEqual(["photoshop.exe已绑定「Photo」"]);
    await user.clear(search);
    await user.type(search, "画");
    expect(choices(dialog)).toEqual(["画图mspaint.exe"]);
    // No headings over a filtered list.
    expect(within(dialog).queryByText("最近使用")).toBeNull();
    await user.clear(search);
    await user.type(search, "zzz");
    expect(within(dialog).getByText("没有匹配的软件")).toBeTruthy();
    await user.clear(search);
    await user.type(search, "blend{Enter}");
    await waitFor(() =>
      expect(written().rules?.at(-1)).toEqual({
        exe: "blender.exe",
        profile: "paint",
      }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("binds an installed app by its program name and remembers where it is", async () => {
    const { api, written } = await setup({ config: baseConfig, recent });
    api.listInstalledApps.mockResolvedValue(installed);
    const dialog = await openInstalled(cardOf("Photo"));
    fireEvent.click(
      await within(dialog).findByRole("button", { name: /Blender/ }),
    );
    await waitFor(() =>
      expect(written().rules?.at(-1)).toEqual({
        exe: "blender.exe",
        profile: "photo",
      }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("已绑定 blender.exe")).toBeTruthy();
    // Its icon shows at once, with no window ever seen: the path is known.
    await waitFor(() =>
      expect(
        within(cardOf("Photo"))
          .getByRole("button", { name: "blender.exe" })
          .querySelector("img")
          ?.getAttribute("src"),
      ).toBe("data:image/png;base64,blender.exe"),
    );
    // No administrator warning for a program that has not been seen running.
    expect(screen.queryByTestId("elevated-warning")).toBeNull();
  });

  it("binds a recent app and warns about an elevated one", async () => {
    const { written, api } = await setup({ config: baseConfig, recent });
    openPicker(cardOf("Photo"));
    chooseMenuItem("从已安装的软件中选择…");
    const dialog = await screen.findByRole("dialog", { name: "选择软件" });
    await waitFor(() => expect(api.listInstalledApps).toHaveBeenCalled());
    fireEvent.click(
      await within(dialog).findByRole("button", { name: /notepad\.exe/ }),
    );
    await waitFor(() =>
      expect(written().rules?.at(-1)).toEqual({
        exe: "notepad.exe",
        profile: "photo",
      }),
    );
    expect((await screen.findAllByText("已绑定 notepad.exe")).length).toBe(1);
    const warning = screen.getByTestId("elevated-warning");
    expect(warning.textContent).toContain(
      "notepad.exe 以管理员身份运行，可能无法接收 padmate 发送的快捷键。",
    );
    // It floats, in view while the page scrolls.
    expect(warning.closest(".profiles-float")).toBeTruthy();
    expect(
      within(cardOf("Photo")).getByRole("button", { name: "notepad.exe" }),
    ).toBeTruthy();
  });

  it("asks before moving an app from another profile", async () => {
    const { written, user } = await setup({ config: baseConfig, recent });
    const pickPhotoshop = async () => {
      const dialog = await openInstalled(cardOf("Paint"));
      fireEvent.click(
        await within(dialog).findByRole("button", { name: /photoshop\.exe/ }),
      );
    };
    await pickPhotoshop();
    const dialog = await screen.findByRole("dialog", {
      name: "此软件已绑定",
    });
    expect(dialog.textContent).toContain(
      "photoshop.exe 当前使用「Photo」，是否改为「Paint」？",
    );
    await user.click(within(dialog).getByRole("button", { name: "保留" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(written().rules).toEqual(baseConfig.rules);
    await pickPhotoshop();
    await user.click(
      within(
        await screen.findByRole("dialog", { name: "此软件已绑定" }),
      ).getByRole("button", { name: "更改" }),
    );
    await waitFor(() =>
      expect(written().rules).toEqual([
        { exe: "krita.exe", profile: "paint", device: "any" },
        { exe: "C:\\Apps\\Clip\\clip.exe", profile: "paint", enabled: false },
        { exe: "photoshop.exe", profile: "paint" },
      ]),
    );
  });

  it("says when there is nothing to choose, while reading, and when reading fails", async () => {
    const { api } = await setup({ config: baseConfig });
    let finish: (apps: InstalledApp[]) => void = () => {};
    api.listInstalledApps.mockImplementationOnce(
      () =>
        new Promise<InstalledApp[]>((done) => {
          finish = done;
        }),
    );
    const dialog = await openInstalled(cardOf("Paint"));
    expect(within(dialog).getByText("正在读取…")).toBeTruthy();
    await act(async () => finish([]));
    expect(within(dialog).getByText("没有可选的软件")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();

    // A failure leaves the recent apps usable.
    api.getRecentApps.mockResolvedValue([
      { exe: "game.exe", path: "C:\\Games\\game.exe", elevated: false },
    ]);
    api.listInstalledApps.mockRejectedValueOnce(new Error("denied"));
    const failed = await openInstalled(cardOf("Paint"));
    expect(
      await within(failed).findByText("无法读取已安装的软件"),
    ).toBeTruthy();
    expect(
      within(failed).getByRole("button", { name: /game\.exe/ }),
    ).toBeTruthy();
  });

  it("reads the list again each time it opens, and Esc and 取消 close it without binding", async () => {
    const { api, written } = await setup({ config: baseConfig });
    api.listInstalledApps.mockResolvedValue(installed);
    let dialog = await openInstalled(cardOf("Paint"));
    await within(dialog).findByText("Blender");
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    dialog = await openInstalled(cardOf("Paint"));
    await within(dialog).findByText("Blender");
    expect(api.listInstalledApps).toHaveBeenCalledTimes(2);
    // A fresh dialog opens with an empty search.
    expect(
      (
        within(dialog).getByRole("searchbox", {
          name: "搜索",
        }) as HTMLInputElement
      ).value,
    ).toBe("");
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(written()).toEqual(baseConfig);
  });

  it("binds the .exe the user browses to, by name, with its path for the icon", async () => {
    const { api, written } = await setup({ config: baseConfig });
    api.browseApp.mockResolvedValue({
      exe: "paintdotnet.exe",
      path: "D:\\Tools\\Paint.NET\\paintdotnet.exe",
    });
    openPicker(cardOf("Paint"));
    chooseMenuItem("浏览…");
    await waitFor(() =>
      expect(written().rules?.at(-1)).toEqual({
        exe: "paintdotnet.exe",
        profile: "paint",
      }),
    );
    expect(await screen.findByText("已绑定 paintdotnet.exe")).toBeTruthy();
    await waitFor(() =>
      expect(
        within(cardOf("Paint"))
          .getByRole("button", { name: "paintdotnet.exe" })
          .querySelector("img")
          ?.getAttribute("src"),
      ).toBe("data:image/png;base64,paintdotnet.exe"),
    );
    // The same program again changes nothing.
    const writes = api.putConfig.mock.calls.length;
    openPicker(cardOf("Paint"));
    chooseMenuItem("浏览…");
    expect(await screen.findByText("paintdotnet.exe 已在列表中")).toBeTruthy();
    expect(api.putConfig.mock.calls.length).toBe(writes);
  });

  it("does nothing when the file dialog is cancelled, and says so when the file cannot be used", async () => {
    const { api, written } = await setup({ config: baseConfig });
    openPicker(cardOf("Paint"));
    chooseMenuItem("浏览…");
    await waitFor(() => expect(api.browseApp).toHaveBeenCalledOnce());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(written()).toEqual(baseConfig);
    api.browseApp.mockRejectedValueOnce({
      message: "Choose an .exe file on a local drive",
      data: { reason: "path" },
    });
    openPicker(cardOf("Paint"));
    chooseMenuItem("浏览…");
    expect(await screen.findByText("请选择本机上的 .exe 文件")).toBeTruthy();
    expect(written()).toEqual(baseConfig);
  });

  it("asks before moving a browsed app from another profile", async () => {
    const { api, written, user } = await setup({ config: baseConfig });
    api.browseApp.mockResolvedValue({
      exe: "photoshop.exe",
      path: "C:\\PS\\photoshop.exe",
    });
    openPicker(cardOf("Paint"));
    chooseMenuItem("浏览…");
    const dialog = await screen.findByRole("dialog", { name: "此软件已绑定" });
    await user.click(within(dialog).getByRole("button", { name: "更改" }));
    await waitFor(() =>
      expect(
        written().rules?.filter((rule) => rule.exe === "photoshop.exe"),
      ).toEqual([{ exe: "photoshop.exe", profile: "paint" }]),
    );
  });

  it("speaks English with the interface", async () => {
    const { api } = await setup({
      config: baseConfig,
      recent,
      language: "en",
    });
    api.listInstalledApps.mockResolvedValue(installed);
    openPicker(cardOf("Paint"), "Add app");
    expect(
      within(screen.getByRole("menu", { name: "Add app" }))
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Pick a window…", "Choose from installed apps…", "Browse…"]);
    chooseMenuItem("Choose from installed apps…");
    const dialog = await screen.findByRole("dialog", { name: "Choose an app" });
    expect(
      within(dialog).getByRole("searchbox", { name: "Search" }),
    ).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeTruthy();
  });

  it("captures the next window that is not padmate's own in a blocking dialog", async () => {
    const { written, event, store } = await setup({ config: baseConfig });
    openPicker(cardOf("Photo"));
    chooseMenuItem("点选窗口…");
    const dialog = await screen.findByRole("dialog", { name: "点选窗口" });
    expect(dialog.textContent).toContain(
      "请切换到要绑定的软件，并点击其窗口。",
    );
    // A modal dialog: the page behind it takes no clicks.
    expect(dialog.tagName).toBe("DIALOG");
    event("app.foreground", {
      exe: "padmate.exe",
      path: "C:\\padmate\\padmate.exe",
      elevated: false,
      own: true,
    });
    expect(screen.getByRole("dialog", { name: "点选窗口" })).toBe(dialog);
    event("app.foreground", {
      exe: "blender.exe",
      path: "C:\\Blender\\blender.exe",
      elevated: true,
      own: false,
    });
    await waitFor(() =>
      expect(written().rules?.at(-1)).toEqual({
        exe: "blender.exe",
        profile: "photo",
      }),
    );
    // It closed itself.
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("已绑定 blender.exe")).toBeTruthy();
    expect(
      screen.getByText(
        "blender.exe 以管理员身份运行，可能无法接收 padmate 发送的快捷键。",
      ),
    ).toBeTruthy();
    expect(store.getState().captured).toBeNull();
  });

  it("asks before capturing an app another profile uses", async () => {
    const { written, event, user } = await setup({ config: baseConfig });
    openPicker(cardOf("Paint"));
    chooseMenuItem("点选窗口…");
    event("app.foreground", {
      exe: "photoshop.exe",
      path: "C:\\PS\\photoshop.exe",
      elevated: false,
      own: false,
    });
    const dialog = await screen.findByRole("dialog", {
      name: "此软件已绑定",
    });
    await user.click(within(dialog).getByRole("button", { name: "更改" }));
    await waitFor(() =>
      expect(
        written().rules?.filter((rule) => rule.exe === "photoshop.exe"),
      ).toEqual([{ exe: "photoshop.exe", profile: "paint" }]),
    );
  });

  it("ends a capture on 取消, on Esc and after the timeout", async () => {
    const { event, written, api } = await setup({ config: baseConfig });
    openPicker(cardOf("Paint"));
    chooseMenuItem("点选窗口…");
    fireEvent.click(
      within(await screen.findByRole("dialog", { name: "点选窗口" })).getByRole(
        "button",
        { name: "取消" },
      ),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("未点选任何窗口")).toBeNull();
    event("app.foreground", {
      exe: "blender.exe",
      path: "C:\\Blender\\blender.exe",
      elevated: false,
      own: false,
    });
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(written()).toEqual(baseConfig);

    // Esc cancels the native dialog.
    openPicker(cardOf("Paint"));
    chooseMenuItem("点选窗口…");
    const dialog = await screen.findByRole("dialog", { name: "点选窗口" });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("未点选任何窗口")).toBeNull();

    vi.useFakeTimers({ shouldAdvanceTime: true });
    openPicker(cardOf("Paint"));
    chooseMenuItem("点选窗口…");
    expect(screen.getByRole("dialog", { name: "点选窗口" })).toBeTruthy();
    act(() => vi.advanceTimersByTime(CAPTURE_TIMEOUT_MS));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByText("未点选任何窗口")).toBeTruthy();
  });
});

describe("profile operations", () => {
  it("creates a blank profile and focuses its add button", async () => {
    const { api, user } = await setup({ config: baseConfig });
    await user.click(screen.getByRole("button", { name: "新建配置" }));
    const dialog = await screen.findByRole("dialog", { name: "新建配置" });
    const name = within(dialog).getByRole("textbox", { name: "名称" });
    expect(document.activeElement).toBe(name);
    // No placeholder in the name field (F1).
    expect(name.getAttribute("placeholder")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "新建" }));
    expect(within(dialog).getByText("名称不能为空")).toBeTruthy();
    expect(api.putProfile).not.toHaveBeenCalled();
    await user.type(name, "Clip Studio{Enter}");
    await waitFor(() =>
      expect(api.putProfile).toHaveBeenCalledWith(
        "clip-studio",
        profileText("Clip Studio"),
        "",
      ),
    );
    const card = await screen.findByRole("article", { name: "Clip Studio" });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(card).getByRole("button", { name: "添加软件" }),
      ),
    );
    expect(card.getAttribute("data-flash")).toBe("true");
  });

  it("gives a non-ASCII name a unique ID", async () => {
    const { api, user } = await setup({
      config: baseConfig,
      profiles: { paint: "Paint", profile: "Old" },
    });
    await user.click(screen.getByRole("button", { name: "新建配置" }));
    await user.type(
      screen.getByRole("textbox", { name: "名称" }),
      "绘画{Enter}",
    );
    await waitFor(() =>
      expect(api.putProfile).toHaveBeenCalledWith(
        "profile-2",
        profileText("绘画"),
        "",
      ),
    );
    expect(await screen.findByRole("article", { name: "绘画" })).toBeTruthy();
  });

  it("asks only for a name, with nothing to choose from (ADR-0076)", async () => {
    const { dialog, stop } = await dialogSetup({ config: baseConfig });
    expect(nameField(dialog).value).toBe("");
    expect(within(dialog).queryByRole("radiogroup")).toBeNull();
    expect(within(dialog).queryByRole("radio")).toBeNull();
    expect(within(dialog).queryByRole("combobox")).toBeNull();
    expect(within(dialog).getAllByRole("textbox")).toHaveLength(1);
    expect(dialog.textContent).not.toMatch(/模板|复制当前|基于/);
    stop();
  });

  it("starts an empty library on a blank profile too, and names nothing for you", async () => {
    const { api, created, dialog, user, stop } = await dialogSetup({
      config: baseConfig,
      profiles: {},
      active: "",
    });
    const name = nameField(dialog);
    expect(name.value).toBe("");
    expect(document.activeElement).toBe(name);
    await user.type(name, "Krita{Enter}");
    await waitFor(() => expect(created).toHaveBeenCalledWith("krita"));
    expect(api.putProfile.mock.calls).toEqual([
      ["krita", profileText("Krita"), ""],
    ]);
    expect(api.copyProfile).not.toHaveBeenCalled();
    stop();
  });

  it("is a blank profile in English too", async () => {
    const { api, dialog, user, stop } = await dialogSetup({
      config: baseConfig,
      language: "en",
    });
    expect(nameField(dialog, "Name").value).toBe("");
    expect(within(dialog).queryByRole("radio")).toBeNull();
    await user.type(nameField(dialog, "Name"), "Painting");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(api.putProfile).toHaveBeenCalledWith(
        "painting",
        profileText("Painting"),
        "",
      ),
    );
    stop();
  });

  it("duplicates a profile with its card's own button, numbering a name in use", async () => {
    const { api } = await setup({ config: baseConfig });
    duplicate("Photo");
    await waitFor(() =>
      expect(api.copyProfile).toHaveBeenCalledWith("photo", "photo-2"),
    );
    expect(
      await screen.findByRole("article", { name: "Photo 副本" }),
    ).toBeTruthy();
    expect(screen.getByText("已复制为「Photo 副本」")).toBeTruthy();
    duplicate("Photo");
    expect(
      await screen.findByRole("article", { name: "Photo 副本 2" }),
    ).toBeTruthy();
    expect(screen.getByText("已复制为「Photo 副本 2」")).toBeTruthy();
  });

  it("makes the copy a whole, independent profile, not one based on the original", async () => {
    const { api, daemon } = await setup({ config: baseConfig });
    const original =
      '{"schemaVersion":1,"name":"Photo","layers":{"base":{"south":{"press":{"type":"keys","keys":"Ctrl+Z"}}}}}';
    daemon.profiles.set("photo", { text: original, revision: "photo-0" });
    duplicate("Photo");
    await screen.findByRole("article", { name: "Photo 副本" });
    expect(api.copyProfile).toHaveBeenCalledExactlyOnceWith("photo", "photo-2");
    const copy = JSON.parse(daemon.profiles.get("photo-2")?.text ?? "");
    expect(copy).toEqual({ ...JSON.parse(original), name: "Photo 副本" });
    expect(copy.extends).toBeUndefined();
    // The original is untouched, and only the copy has the new name.
    expect(daemon.profiles.get("photo")?.text).toBe(original);
  });

  it("copies the fixed default and a profile that is in use as well", async () => {
    const { api } = await setup({ config: baseConfig });
    duplicate("默认");
    await waitFor(() =>
      expect(api.copyProfile).toHaveBeenCalledWith("default", "profile"),
    );
    expect(
      await screen.findByRole("article", { name: "默认 副本" }),
    ).toBeTruthy();
    duplicate("Paint");
    await waitFor(() =>
      expect(api.copyProfile).toHaveBeenLastCalledWith("paint", "paint-2"),
    );
    expect(
      await screen.findByRole("article", { name: "Paint 副本" }),
    ).toBeTruthy();
  });

  it("has the copy button beside 编辑按键 and none in the card's menu, in English too", async () => {
    await setup({ config: baseConfig, language: "en" });
    const card = cardOf("Photo");
    const [edit, copy] = within(card).getAllByRole("button");
    // Both are icon buttons: the name is the label and the tooltip.
    for (const [button, name] of [
      [edit, "Edit keys"],
      [copy, "Duplicate"],
    ] as const) {
      expect(button.getAttribute("aria-label")).toBe(name);
      expect(button.getAttribute("title")).toBe(name);
      expect(button.textContent).toBe("");
      expect(button.className).toContain("icon-btn");
      expect(button.querySelector("svg.ui-icon")).not.toBeNull();
    }
    fireEvent.click(
      within(card).getByRole("button", { name: "More for “Photo”" }),
    );
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).not.toContain("Duplicate");
  });

  it("names the field by its label alone and announces its error (UI-24)", async () => {
    const { user } = await setup({ config: baseConfig });
    await user.click(screen.getByRole("button", { name: "新建配置" }));
    const create = await screen.findByRole("dialog", { name: "新建配置" });
    const field = within(create).getByRole("textbox", { name: "名称" });
    await user.type(field, "photo");
    const error = within(create).getByText("已有同名配置");
    expect(error.getAttribute("role")).toBe("alert");
    expect(field.getAttribute("aria-describedby")).toBe(error.id);
    // Not part of the label, or the field's name would be "名称已有同名配置".
    expect(error.closest("label")).toBeNull();
    expect(within(create).getByRole("textbox", { name: "名称" })).toBe(field);
  });

  it("refuses a name another profile has (F2)", async () => {
    const { api, user } = await setup({ config: baseConfig });
    await user.click(screen.getByRole("button", { name: "新建配置" }));
    const create = await screen.findByRole("dialog", { name: "新建配置" });
    const field = within(create).getByRole("textbox", { name: "名称" });
    // Said at once, in any case and with spaces around it.
    await user.type(field, " photo ");
    expect(within(create).getByText("已有同名配置")).toBeTruthy();
    expect(field.getAttribute("aria-invalid")).toBe("true");
    await user.click(within(create).getByRole("button", { name: "新建" }));
    // The fixed default's names are taken too.
    await user.clear(field);
    await user.type(field, "默认");
    expect(within(create).getByText("已有同名配置")).toBeTruthy();
    await user.clear(field);
    await user.type(field, "Default{Enter}");
    expect(within(create).getByText("已有同名配置")).toBeTruthy();
    expect(api.putProfile).not.toHaveBeenCalled();
    await user.click(within(create).getByRole("button", { name: "取消" }));

    openCardMenu("Photo");
    chooseMenuItem("重命名…");
    const rename = await screen.findByRole("dialog", { name: "重命名配置" });
    const name = within(rename).getByRole("textbox", { name: "名称" });
    await user.clear(name);
    await user.type(name, "PAINT");
    expect(within(rename).getByText("已有同名配置")).toBeTruthy();
    expect(
      (
        within(rename).getByRole("button", {
          name: "保存",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    // Its own name in another case is fine.
    await user.clear(name);
    await user.type(name, "PHOTO{Enter}");
    await waitFor(() =>
      expect(api.putProfile).toHaveBeenCalledWith(
        "photo",
        profileText("PHOTO"),
        "photo-0",
      ),
    );
  });

  it("renames a profile, changing only its name", async () => {
    const { api, daemon, user } = await setup({ config: baseConfig });
    daemon.profiles.set("photo", {
      text: '{"schemaVersion":0,"name":"Photo","layers":{"base":{"a":{"tap":"Ctrl+Z"}}}}',
      revision: "photo-0",
    });
    openCardMenu("Photo");
    chooseMenuItem("重命名…");
    const dialog = await screen.findByRole("dialog", { name: "重命名配置" });
    const field = within(dialog).getByRole("textbox", { name: "名称" });
    expect((field as HTMLInputElement).value).toBe("Photo");
    await user.clear(field);
    expect(within(dialog).getByText("名称不能为空")).toBeTruthy();
    await user.type(field, "Photoshop{Enter}");
    await waitFor(() =>
      expect(api.putProfile).toHaveBeenCalledWith(
        "photo",
        '{"schemaVersion":0,"name":"Photoshop","layers":{"base":{"a":{"tap":"Ctrl+Z"}}}}',
        "photo-0",
      ),
    );
    expect(
      await screen.findByRole("article", { name: "Photoshop" }),
    ).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("deletes the active profile after switching to the default and unbinding it", async () => {
    const { api, written, user } = await setup({ config: legacyConfig });
    openCardMenu("Paint");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Paint」？",
    });
    expect(dialog.textContent).toContain(
      "删除后无法恢复，绑定的软件将一并解除。",
    );
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() =>
      expect(api.deleteProfile).toHaveBeenCalledWith("paint", "paint-0"),
    );
    expect(api.activateProfile).toHaveBeenCalledWith("default");
    // Written once, without the retired members.
    expect(written()).toEqual({
      schemaVersion: 0,
      rules: [{ exe: "photoshop.exe", profile: "photo" }],
    });
    await waitFor(() =>
      expect(screen.queryByRole("article", { name: "Paint" })).toBeNull(),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "新建配置" }),
    );
  });

  it("starts the delete dialog on 取消, so Enter keeps the profile", async () => {
    const { api, daemon, user } = await setup({ config: baseConfig });
    daemon.profiles.set("photo", {
      text: '{"schemaVersion":0,"name":"Photo","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}',
      revision: "photo-0",
    });
    openCardMenu("Photo");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Photo」？",
    });
    // Its profile.json names a script, which goes with the folder.
    await waitFor(() =>
      expect(dialog.textContent).toContain("文件夹中的脚本也将一并删除。"),
    );
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "取消" }),
    );
    expect(
      within(dialog).getByRole("button", { name: "删除" }).className,
    ).toContain("btn-danger");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(api.deleteProfile).not.toHaveBeenCalled();
    expect(screen.getByRole("article", { name: "Photo" })).toBeTruthy();
  });

  it("names no scripts for a profile without one", async () => {
    const { api } = await setup({ config: baseConfig });
    openCardMenu("Photo");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Photo」？",
    });
    await waitFor(() => expect(api.getProfile).toHaveBeenCalledWith("photo"));
    await act(async () => {});
    expect(dialog.textContent).toContain(
      "删除后无法恢复，绑定的软件将一并解除。",
    );
    expect(dialog.textContent).not.toContain("脚本");
  });

  it("explains why the startup profile cannot be deleted and changes nothing", async () => {
    const config = baseConfig;
    const { api, store, written, user } = await setup({
      config,
      startup: "photo",
    });
    openCardMenu("Photo");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Photo」？",
    });
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(
      await within(dialog).findByText("此配置为启动时指定的配置，无法删除"),
    ).toBeTruthy();
    expect(screen.getByRole("article", { name: "Photo" })).toBeTruthy();
    // The bindings stay; only the dialog says why.
    expect(written()).toEqual(config);
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(store.getState().error).toBe("");
    // Closing it leaves no message behind.
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(store.getState().error).toBe("");
  });

  it("keeps the startup profile in use when it cannot be deleted", async () => {
    const { api, daemon, written, user } = await setup({
      config: baseConfig,
      startup: "paint",
    });
    openCardMenu("Paint");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Paint」？",
    });
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(
      await within(dialog).findByText("此配置为启动时指定的配置，无法删除"),
    ).toBeTruthy();
    expect(daemon.active).toBe("paint");
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(written()).toEqual(baseConfig);
    await waitFor(() =>
      expect(within(cardOf("Paint")).getByText("使用中")).toBeTruthy(),
    );
  });

  it("switches to a profile before editing its keys", async () => {
    const { api, editKeys, openMapping, user } = await setup({
      config: baseConfig,
    });
    await user.click(
      within(cardOf("Paint")).getByRole("button", { name: "编辑按键" }),
    );
    expect(openMapping).toHaveBeenCalledTimes(1);
    expect(editKeys).not.toHaveBeenCalled();
    expect(api.activateProfile).not.toHaveBeenCalled();
    await user.click(
      within(cardOf("Photo")).getByRole("button", { name: "编辑按键" }),
    );
    await waitFor(() => expect(openMapping).toHaveBeenCalledTimes(2));
    expect(editKeys).toHaveBeenCalledWith("photo");
    expect(api.activateProfile).toHaveBeenCalledWith("photo");
  });

  it("gives the two card buttons their names as icons, in Chinese", async () => {
    await setup({ config: baseConfig });
    const card = cardOf("Photo");
    const edit = within(card).getByRole("button", { name: "编辑按键" });
    const copy = within(card).getByRole("button", { name: "复制" });
    expect(edit.title).toBe("编辑按键");
    expect(copy.title).toBe("复制");
    expect(edit.textContent).toBe("");
    expect(copy.textContent).toBe("");
    expect(edit.getAttribute("aria-busy")).toBeNull();
  });

  it("names the card's edit button 正在切换… and marks it busy while the card switches", async () => {
    const { api, daemon, user } = await setup({ config: baseConfig });
    let finish = () => {};
    api.activateProfile.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = () => {
            daemon.active = "photo";
            resolve({ profileId: "photo", generation: 2 });
          };
        }),
    );
    const photo = cardOf("Photo");
    await user.click(within(photo).getByRole("button", { name: "编辑按键" }));
    const busy = await within(photo).findByRole("button", {
      name: "正在切换…",
    });
    expect(busy.title).toBe("正在切换…");
    expect(busy.getAttribute("aria-busy")).toBe("true");
    expect(busy.textContent).toBe("");
    // No card opens its keys meanwhile, and only this one is busy.
    const paint = within(cardOf("Paint")).getByRole("button", {
      name: "编辑按键",
    }) as HTMLButtonElement;
    expect(paint.disabled).toBe(true);
    expect(paint.getAttribute("aria-busy")).toBeNull();
    finish();
    await waitFor(() =>
      expect(
        within(photo)
          .getByRole("button", { name: "编辑按键" })
          .getAttribute("aria-busy"),
      ).toBeNull(),
    );
  });

  it("renames and deletes the profile in use without asking about edits", async () => {
    await setup({ config: baseConfig });
    // The mapping page saves its edits at once (UI-43): nothing waits.
    openCardMenu("Paint");
    chooseMenuItem("重命名…");
    expect(
      await screen.findByRole("dialog", { name: "重命名配置" }),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    openCardMenu("Paint");
    chooseMenuItem("删除…");
    expect(
      await screen.findByRole("dialog", { name: /^删除「Paint」/ }),
    ).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "修改还没保存" })).toBeNull();
  });

  it("speaks English", async () => {
    await setup({ config: baseConfig, language: "en" });
    expect(screen.getByRole("button", { name: "New profile" })).toBeTruthy();
    expect(
      screen.getByRole("switch", { name: "Switch profiles by app" }),
    ).toBeTruthy();
    expect(
      within(cardOf("Paint")).getByRole("button", { name: "Edit keys" }),
    ).toBeTruthy();
    expect(
      within(cardOf("Paint")).getByRole("button", { name: "krita.exe" }),
    ).toBeTruthy();
    const fixed = cardOf("Default");
    expect(
      within(fixed).getByText("Used by apps without a binding"),
    ).toBeTruthy();
    fireEvent.click(
      within(cardOf("Paint")).getByRole("button", { name: "krita.exe" }),
    );
    expect(screen.getByRole("menuitem", { name: "Unbind" })).toBeTruthy();
    openPicker(cardOf("Paint"), "Add app");
    chooseMenuItem("Pick a window…");
    const dialog = await screen.findByRole("dialog", { name: "Pick a window" });
    expect(dialog.textContent).toContain(
      "Switch to the app and click its window.",
    );
  });
});

describe("export and import (PRO-20)", () => {
  const pill = () => document.querySelector(".profiles-pill");

  it("exports a profile from its card after saving its edits", async () => {
    const { api, saveEdits } = await setup({ config: baseConfig });
    openCardMenu("Paint");
    expect(screen.queryByRole("menuitem", { name: "历史版本…" })).toBeNull();
    chooseMenuItem("导出…");
    await waitFor(() =>
      expect(pill()?.textContent).toContain("已导出「Paint」"),
    );
    expect(saveEdits).toHaveBeenCalledWith("paint");
    expect(api.exportProfile).toHaveBeenCalledExactlyOnceWith("paint", "Paint");
    expect(saveEdits.mock.invocationCallOrder[0]).toBeLessThan(
      api.exportProfile.mock.invocationCallOrder[0] as number,
    );
  });

  it("copies a profile from its card after saving its edits", async () => {
    const { api, saveEdits } = await setup({ config: baseConfig });
    duplicate("Paint");
    await waitFor(() => expect(api.copyProfile).toHaveBeenCalledOnce());
    expect(saveEdits).toHaveBeenCalledWith("paint");
    expect(saveEdits.mock.invocationCallOrder[0]).toBeLessThan(
      api.copyProfile.mock.invocationCallOrder[0] as number,
    );
  });

  it("exports the fixed default by its name in the UI", async () => {
    const { api } = await setup({ config: baseConfig });
    openCardMenu("默认");
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual(["配置设置…", "导出…"]);
    chooseMenuItem("导出…");
    await waitFor(() =>
      expect(api.exportProfile).toHaveBeenCalledWith("default", "默认"),
    );
  });

  it("says nothing when the export is canceled", async () => {
    const { api, store } = await setup({ config: baseConfig });
    api.exportProfile.mockResolvedValueOnce({ saved: false });
    openCardMenu("Paint");
    chooseMenuItem("导出…");
    await waitFor(() => expect(api.exportProfile).toHaveBeenCalledOnce());
    await waitFor(() => expect(store.getState().libraryBusy).toBe(false));
    expect(pill()?.textContent).not.toContain("已导出");
    expect(store.getState().error).toBe("");
  });

  it("imports a file as a new profile and points at its card", async () => {
    const { api, daemon } = await setup({ config: baseConfig });
    daemon.nextFile = { name: "Ink" };
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await waitFor(() => expect(pill()?.textContent).toContain("已导入「Ink」"));
    expect(api.importProfile).toHaveBeenCalledExactlyOnceWith(undefined);
    await waitFor(() =>
      expect(cardOf("Ink").getAttribute("data-flash")).toBe("true"),
    );
    expect(daemon.profiles.get("ink")?.text).toBe(profileText("Ink"));
  });

  it("does nothing when the open dialog is canceled", async () => {
    const { api, store } = await setup({ config: baseConfig });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await waitFor(() => expect(api.importProfile).toHaveBeenCalledOnce());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(store.getState().error).toBe("");
  });

  it("saves a clashing file under a new name", async () => {
    const { api, daemon, user } = await setup({ config: baseConfig });
    daemon.nextFile = { name: "Paint" };
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已有名为「Paint」的配置",
    });
    expect(dialog.textContent).toContain("替换将覆盖该配置的全部内容。");
    const field = nameField(dialog, "新名称");
    expect(field.value).toBe("Paint 2");
    expect(document.activeElement).toBe(field);
    await user.clear(field);
    await user.type(field, "photo");
    expect(within(dialog).getByText("已有同名配置")).toBeTruthy();
    const saveAs = within(dialog).getByRole("button", { name: "另存为" });
    expect((saveAs as HTMLButtonElement).disabled).toBe(true);
    await user.clear(field);
    await user.type(field, "Paint B{Enter}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: /已有名为/ })).toBeNull(),
    );
    expect(api.importProfile.mock.calls[1]?.[0]).toEqual({
      token: "1".padStart(32, "0"),
      name: "Paint B",
    });
    await waitFor(() =>
      expect(pill()?.textContent).toContain("已导入「Paint B」"),
    );
    expect(cardOf("Paint B")).toBeTruthy();
    // Paint itself is untouched.
    expect(daemon.profiles.get("paint")?.text).toBe(profileText("Paint"));
  });

  it("replaces the clashing profile after saving its edits", async () => {
    const { api, daemon, saveEdits } = await setup({ config: baseConfig });
    daemon.nextFile = { name: "PAINT" };
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已有名为「Paint」的配置",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: /已有名为/ })).toBeNull(),
    );
    expect(saveEdits).toHaveBeenCalledWith("paint");
    expect(api.importProfile.mock.calls[1]?.[0]).toEqual({
      token: "1".padStart(32, "0"),
      replace: "paint",
    });
    expect(daemon.profiles.get("paint")?.text).toBe(profileText("PAINT"));
    await waitFor(() => expect(cardOf("PAINT")).toBeTruthy());
  });

  it("replaces the fixed default under its own name", async () => {
    const { api, daemon } = await setup({ config: baseConfig });
    daemon.nextFile = { name: "Default" };
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已有名为「默认」的配置",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    await waitFor(() =>
      expect(pill()?.textContent).toContain("已导入「默认」"),
    );
    expect(api.importProfile.mock.calls[1]?.[0]).toMatchObject({
      replace: "default",
    });
    expect(JSON.parse(daemon.profiles.get("default")?.text ?? "").name).toBe(
      "默认",
    );
  });

  describe("scripts that clash with the library's (SCR-51)", () => {
    const token = "9".padStart(32, "0");
    const clash = {
      file: "画笔.ts",
      existing: "画笔.ts",
      replaceable: true,
    };
    const ink = {
      id: "ink",
      name: "Ink",
      revision: "ink-0",
      applied: false,
    };

    it("asks to replace or keep both, with keep both focused", async () => {
      const { api } = await setup({ config: baseConfig });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [clash],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "已有脚本「画笔」，内容不同",
      });
      expect(dialog.textContent).toContain(
        "替换后，所有配置都使用导入的脚本。都保留则导入的脚本另加编号。",
      );
      expect(document.activeElement).toBe(
        within(dialog).getByRole("button", { name: "都保留" }),
      );
      api.importProfile.mockResolvedValueOnce({
        ...ink,
        macros: [{ file: "画笔.ts", name: "画笔 2.ts" }],
      });
      fireEvent.click(within(dialog).getByRole("button", { name: "都保留" }));
      await waitFor(() =>
        expect(pill()?.textContent).toContain("已导入「Ink」"),
      );
      expect(api.importProfile.mock.calls[1]?.[0]).toEqual({
        token,
        macros: "keep",
      });
      expect(screen.queryByRole("dialog", { name: /已有脚本/ })).toBeNull();
    });

    it("replaces the library's scripts on request and lists several", async () => {
      const { api } = await setup({ config: baseConfig });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [
          clash,
          { file: "橡皮.js", existing: "橡皮.js", replaceable: true },
        ],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "已有 2 个同名脚本，内容不同",
      });
      expect(
        within(dialog)
          .getAllByRole("listitem")
          .map((item) => item.textContent),
      ).toEqual(["画笔", "橡皮"]);
      api.importProfile.mockResolvedValueOnce(ink);
      fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
      await waitFor(() =>
        expect(pill()?.textContent).toContain("已导入「Ink」"),
      );
      expect(api.importProfile.mock.calls[1]?.[0]).toEqual({
        token,
        macros: "replace",
      });
    });

    it("asks after the profile's name clash, and imports nothing when canceled", async () => {
      const { api, daemon } = await setup({ config: baseConfig });
      daemon.nextFile = { name: "Paint" };
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const names = await screen.findByRole("dialog", {
        name: "已有名为「Paint」的配置",
      });
      // Replacing the profile finds scripts that clash; nothing is written yet.
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [clash],
        token,
      });
      fireEvent.click(within(names).getByRole("button", { name: "替换" }));
      const scripts = await screen.findByRole("dialog", {
        name: "已有脚本「画笔」，内容不同",
      });
      expect(screen.queryByRole("dialog", { name: /已有名为/ })).toBeNull();
      fireEvent.click(within(scripts).getByRole("button", { name: "取消" }));
      await waitFor(() =>
        expect(screen.queryByRole("dialog", { name: /已有脚本/ })).toBeNull(),
      );
      expect(api.importProfile).toHaveBeenCalledTimes(2);
      expect(pill()?.textContent ?? "").not.toContain("已导入");
    });

    it("offers no 替换 when none of the scripts can be replaced", async () => {
      const { api } = await setup({ config: baseConfig });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [{ ...clash, existing: "画笔.js", replaceable: false }],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "已有脚本「画笔」，内容不同",
      });
      expect(within(dialog).queryByRole("button", { name: "替换" })).toBeNull();
      expect(dialog.textContent).toContain("导入的脚本另加编号。");
      expect(dialog.textContent).not.toContain("所有配置都使用导入的脚本");
      api.importProfile.mockResolvedValueOnce(ink);
      fireEvent.click(within(dialog).getByRole("button", { name: "导入" }));
      await waitFor(() =>
        expect(pill()?.textContent).toContain("已导入「Ink」"),
      );
      expect(api.importProfile.mock.calls[1]?.[0]).toEqual({
        token,
        macros: "keep",
      });
    });

    it("marks the scripts that are numbered even when replacing", async () => {
      const { api } = await setup({ config: baseConfig });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [
          clash,
          { file: "橡皮.ts", existing: "橡皮.js", replaceable: false },
        ],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "已有 2 个同名脚本，内容不同",
      });
      expect(
        within(dialog)
          .getAllByRole("listitem")
          .map((item) => item.textContent),
      ).toEqual(["画笔", "橡皮（替换时仍另加编号）"]);
      expect(within(dialog).getByRole("button", { name: "替换" })).toBeTruthy();
    });

    it("asks about the profile's name again when it was taken meanwhile", async () => {
      const { api } = await setup({ config: baseConfig });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [clash],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "导入…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "已有脚本「画笔」，内容不同",
      });
      api.importProfile.mockResolvedValueOnce({
        exists: true,
        id: "paint",
        name: "Paint",
        token: "8".padStart(32, "0"),
      });
      fireEvent.click(within(dialog).getByRole("button", { name: "都保留" }));
      const names = await screen.findByRole("dialog", {
        name: "已有名为「Paint」的配置",
      });
      expect(screen.queryByRole("dialog", { name: /已有脚本/ })).toBeNull();
      expect(within(names).getByRole("button", { name: "替换" })).toBeTruthy();
    });

    it("shows a failure in the dialog and speaks English", async () => {
      const { api } = await setup({ config: baseConfig, language: "en" });
      api.importProfile.mockResolvedValueOnce({
        conflict: true,
        scripts: [clash],
        token,
      });
      fireEvent.click(screen.getByRole("button", { name: "Import…" }));
      const dialog = await screen.findByRole("dialog", {
        name: "A script “画笔” exists with other contents",
      });
      expect(dialog.textContent).toContain(
        "Replacing makes every profile use the imported script. Keeping both numbers the imported one.",
      );
      api.importProfile.mockRejectedValueOnce({
        message: "Choose the file again",
        data: { reason: "expired" },
      });
      fireEvent.click(
        within(dialog).getByRole("button", { name: "Keep both" }),
      );
      expect((await within(dialog).findByRole("alert")).textContent).toBe(
        "This took too long. Choose the file again",
      );
      expect(
        within(dialog).getByRole("button", { name: "Replace" }),
      ).toBeTruthy();
    });
  });

  it("explains a file it waited too long for, in the dialog", async () => {
    const { api, daemon, store } = await setup({ config: baseConfig });
    daemon.nextFile = { name: "Paint" };
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", { name: /已有名为/ });
    api.importProfile.mockRejectedValueOnce({
      message: "Choose the file again",
      data: { reason: "expired" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "替换" }));
    expect((await within(dialog).findByRole("alert")).textContent).toBe(
      "等待时间过长，请重新选择文件",
    );
    expect(store.getState().error).toBe("");
  });

  it("explains a file the daemon refuses in the shell's alert", async () => {
    const { api, store } = await setup({ config: baseConfig });
    api.importProfile.mockRejectedValueOnce({
      message: "profile archive: not a zip file",
      code: -32020,
      data: { reason: "format" },
    });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await waitFor(() =>
      expect(store.getState().error).toBe(
        "不是 padmate 配置文件，或文件已损坏",
      ),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("speaks English", async () => {
    const { daemon } = await setup({ config: baseConfig, language: "en" });
    fireEvent.click(
      within(cardOf("Paint")).getByRole("button", { name: "More for “Paint”" }),
    );
    expect(screen.getByRole("menuitem", { name: "Export…" })).toBeTruthy();
    fireEvent.click(document.body);
    daemon.nextFile = { name: "Paint" };
    fireEvent.click(screen.getByRole("button", { name: "Import…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "A profile named “Paint” exists",
    });
    expect(dialog.textContent).toContain(
      "Replacing overwrites everything in that profile.",
    );
    expect(
      within(dialog).getByRole("button", { name: "Replace" }),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Save as new" }),
    ).toBeTruthy();
    expect(nameField(dialog, "New name").value).toBe("Paint 2");
  });
});

describe("picker helpers", () => {
  const config = {
    text: JSON.stringify(baseConfig),
    revision: "c0",
  };
  const app = (exe: string, path = `C:\\${exe}`): RecentApp => ({
    exe,
    path,
    elevated: false,
  });

  it("leaves out apps the profile has and repeated program names", () => {
    const recentApps = [
      app("krita.exe"),
      app("KRITA.exe", "D:\\krita.exe"),
      app("game.exe"),
      app("clip.exe", "C:\\Apps\\Clip\\clip.exe"),
      app("clip.exe", "D:\\Clip\\clip.exe"),
    ];
    expect(
      pickableApps({ config, recentApps }, "paint").map((entry) => entry.path),
    ).toEqual(["C:\\game.exe", "D:\\Clip\\clip.exe"]);
    expect(
      pickableApps({ config, recentApps }, "photo").map((entry) => entry.path),
    ).toEqual(["C:\\krita.exe", "C:\\game.exe", "C:\\Apps\\Clip\\clip.exe"]);
  });

  it("names the profile that decides an app now", () => {
    const state = {
      config,
      language: "zh-CN" as const,
      profiles: [{ id: "photo", name: "A very long profile name here" }],
    };
    expect(boundHint(state, "paint", app("photoshop.exe"))).toBe(
      "已绑定「A very lo…」",
    );
    expect(boundHint(state, "photo", app("photoshop.exe"))).toBeUndefined();
    // An app no rule binds uses the default: nothing to say.
    expect(boundHint(state, "paint", app("game.exe"))).toBeUndefined();
    // A disabled rule decides nothing.
    expect(
      boundHint(state, "photo", app("clip.exe", "C:\\Apps\\Clip\\clip.exe")),
    ).toBeUndefined();
    // Nor does a retired null rule or ignore list.
    const old = {
      ...state,
      config: { text: JSON.stringify(legacyConfig), revision: "c1" },
    };
    expect(boundHint(old, "paint", app("game.exe"))).toBeUndefined();
    expect(boundHint(old, "paint", app("explorer.exe"))).toBeUndefined();
    // The default is named by its ID.
    const toDefault = {
      ...state,
      profiles: [{ id: "default", name: "Stored" }],
      config: {
        text: JSON.stringify({
          schemaVersion: 0,
          rules: [{ exe: "notepad.exe", profile: "default" }],
        }),
        revision: "c2",
      },
    };
    expect(boundHint(toDefault, "paint", app("notepad.exe"))).toBe(
      "已绑定「默认」",
    );
  });
});

describe("配置设置 from a profile card (UI-27)", () => {
  it("is the first item of every card's menu, the fixed default's too", async () => {
    await setup({ config: baseConfig });
    for (const name of ["Paint", "Photo"]) {
      openCardMenu(name);
      expect(
        screen.getAllByRole("menuitem").map((item) => item.textContent),
      ).toEqual(["配置设置…", "重命名…", "导出…", "删除…"]);
      fireEvent.keyDown(document.body, { key: "Escape" });
    }
    openCardMenu("默认");
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual(["配置设置…", "导出…"]);
  });

  it("is in English with the UI", async () => {
    await setup({ config: baseConfig, language: "en" });
    fireEvent.click(
      within(cardOf("Paint")).getByRole("button", { name: "More for “Paint”" }),
    );
    expect(screen.getAllByRole("menuitem")[0]?.textContent).toBe(
      "Profile settings…",
    );
  });

  it("asks the shell for the card's profile and does not activate it or leave the page", async () => {
    const { api, store, profileSettings, openMapping, editKeys } = await setup({
      config: baseConfig,
    });
    // Photo is not the profile in use (Paint is).
    expect(store.getState().status?.profileId).toBe("paint");
    const page = store.getState().page;
    openCardMenu("Photo");
    chooseMenuItem("配置设置…");
    expect(profileSettings).toHaveBeenCalledTimes(1);
    expect(profileSettings).toHaveBeenCalledWith("photo");
    expect(api.activateProfile).not.toHaveBeenCalled();
    expect(editKeys).not.toHaveBeenCalled();
    expect(openMapping).not.toHaveBeenCalled();
    expect(store.getState().page).toBe(page);
    expect(store.getState().status?.profileId).toBe("paint");
    // The profile in use is asked for the same way.
    openCardMenu("Paint");
    chooseMenuItem("配置设置…");
    expect(profileSettings).toHaveBeenLastCalledWith("paint");
    expect(api.activateProfile).not.toHaveBeenCalled();
  });

  it("is turned off for a profile whose file has errors", async () => {
    const { profileSettings } = await setup({
      config: baseConfig,
      broken: ["photo"],
    });
    openCardMenu("Photo");
    const item = screen.getByRole("menuitem", {
      name: "配置设置…",
    }) as HTMLButtonElement;
    expect(item.disabled).toBe(true);
    fireEvent.click(item);
    expect(profileSettings).not.toHaveBeenCalled();
  });
});

describe("profiles whose files have extends (PRO-19, ADR-0076)", () => {
  /** Profiles stored with an extends, as a hand-written file has it. */
  function based(
    daemon: Awaited<ReturnType<typeof setup>>["daemon"],
    children: Record<string, string>,
  ) {
    for (const [id, parent] of Object.entries(children)) {
      const stored = daemon.profiles.get(id);
      if (!stored) throw new Error(`no profile ${id}`);
      daemon.profiles.set(id, {
        ...stored,
        text: JSON.stringify({
          ...JSON.parse(stored.text),
          extends: parent,
        }),
      });
    }
  }
  const sketches = {
    default: "默认",
    paint: "Paint",
    ink: "Ink",
    sketch: "Sketch",
  };

  it("shows nothing of what a profile extends on its card", async () => {
    const { daemon, store } = await setup({
      config: baseConfig,
      profiles: sketches,
    });
    based(daemon, { sketch: "paint", ink: "default", paint: "gone" });
    await act(async () => {
      await store.getState().refresh();
    });
    for (const name of ["Sketch", "Ink", "Paint", "默认"])
      expect(cardOf(name).textContent).not.toMatch(/基于|Based on/);
    expect(screen.getByRole("article", { name: "Sketch" })).toBeTruthy();
  });

  it("shows nothing of it in English either", async () => {
    const { daemon, store } = await setup({
      config: baseConfig,
      profiles: sketches,
      language: "en",
    });
    based(daemon, { sketch: "paint", ink: "default" });
    await act(async () => {
      await store.getState().refresh();
    });
    for (const name of ["Sketch", "Ink", "Paint", "Default"])
      expect(cardOf(name).textContent).not.toMatch(/基于|Based on/);
  });

  it("deletes the parent without a word about the profiles based on it, which keep their keys", async () => {
    const { api, daemon, store, user } = await setup({
      config: baseConfig,
      profiles: { ...sketches, photo: "Photo" },
      active: "photo",
    });
    based(daemon, { sketch: "paint", ink: "paint" });
    await act(async () => {
      await store.getState().refresh();
    });
    openCardMenu("Paint");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Paint」？",
    });
    expect(dialog.textContent).toContain(
      "删除后无法恢复，绑定的软件将一并解除。",
    );
    expect(dialog.textContent).not.toContain("基于");
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() =>
      expect(api.deleteProfile).toHaveBeenCalledWith("paint", "paint-0"),
    );
    await waitFor(() =>
      expect(screen.queryByRole("article", { name: "Paint" })).toBeNull(),
    );
    // The daemon wrote the profiles that were based on it out whole.
    expect(screen.getByRole("article", { name: "Sketch" })).toBeTruthy();
    expect(
      JSON.parse(daemon.profiles.get("sketch")?.text ?? "{}"),
    ).not.toHaveProperty("extends");
  });

  it("says nothing of them in English either", async () => {
    const { daemon, store } = await setup({
      config: baseConfig,
      profiles: sketches,
      active: "default",
      language: "en",
    });
    based(daemon, { sketch: "paint", ink: "paint" });
    await act(async () => {
      await store.getState().refresh();
    });
    fireEvent.click(
      within(cardOf("Paint")).getByRole("button", { name: "More for “Paint”" }),
    );
    chooseMenuItem("Delete…");
    const dialog = await screen.findByRole("dialog", {
      name: "Delete “Paint”?",
    });
    expect(dialog.textContent).not.toMatch(/based on/i);
  });

  it("shows the reason when the daemon cannot write a profile out whole, and deletes nothing", async () => {
    const { daemon, store, user, written } = await setup({
      config: baseConfig,
      profiles: sketches,
      active: "default",
    });
    based(daemon, { sketch: "paint" });
    daemon.childBlocked = "script";
    await act(async () => {
      await store.getState().refresh();
    });
    openCardMenu("Paint");
    chooseMenuItem("删除…");
    const dialog = await screen.findByRole("dialog", {
      name: "删除「Paint」？",
    });
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(
      await within(dialog).findByText("「Sketch」要用到它的脚本，所以无法删除"),
    ).toBeTruthy();
    expect(daemon.profiles.has("paint")).toBe(true);
    // The rules that the delete cleaned up are back.
    expect(written()).toEqual(baseConfig);
    // The dialog stays open, with the profile; closing it is the user's.
    expect(screen.getByRole("article", { name: "Paint" })).toBeTruthy();
    daemon.childBlocked = "invalid";
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(
      await within(dialog).findByText(
        "「Sketch」有错误，请先修好它，或者删除它",
      ),
    ).toBeTruthy();
  });
});

describe("importing an AntiMicroX profile (PRO-21)", () => {
  const pill = () => document.querySelector(".profiles-pill");
  const imported = (report: AmgpReportLine[]): AmgpImported => ({
    amgp: true,
    id: "ink",
    name: "Ink",
    revision: "ink-0",
    applied: false,
    report,
  });
  const dropped: AmgpReportLine = {
    severity: "dropped",
    code: "execute",
    set: 1,
    control: "south",
    slot: 1,
    message: "运行程序“x.exe”：padmate 不运行程序",
  };
  const droppedFile: AmgpReportLine = {
    severity: "dropped",
    code: "calibration",
    message: "摇杆校准（偏移与增益）没有导入",
  };
  const approximate: AmgpReportLine = {
    severity: "approximate",
    code: "turbo",
    set: 2,
    control: "lt",
    message: "连点的间隔近似为每秒 12 次",
  };
  const info: AmgpReportLine = {
    severity: "info",
    code: "layers",
    message: "这个配置除了 base 还有 1 个别的层；映射页只编辑 base",
  };

  it("lists what was dropped, approximated and noted, by severity, before the profile opens", async () => {
    const { api, editKeys } = await setup({ config: baseConfig });
    api.importProfile.mockResolvedValueOnce(
      imported([info, dropped, approximate, droppedFile]),
    );
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已从 AntiMicroX 导入「Ink」",
    });
    expect(
      within(dialog)
        .getAllByRole("heading", { level: 3 })
        .map((heading) => heading.textContent),
    ).toEqual(["丢弃（2）", "近似（1）", "信息（1）"]);
    const groups = within(dialog).getAllByRole("list");
    expect(
      within(groups[0])
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual([
      "set 1 / south / slot 1运行程序“x.exe”：padmate 不运行程序",
      "摇杆校准（偏移与增益）没有导入",
    ]);
    expect(within(groups[1]).getByRole("listitem").textContent).toBe(
      "set 2 / lt连点的间隔近似为每秒 12 次",
    );
    expect(within(groups[2]).getByRole("listitem").textContent).toContain(
      "这个配置除了 base 还有 1 个别的层",
    );
    // The location is apart from the reason.
    expect(
      within(groups[0])
        .getAllByRole("listitem")[0]
        .querySelector(".report-where")?.textContent,
    ).toBe("set 1 / south / slot 1");
    // The profile does not open before the report is read.
    expect(editKeys).not.toHaveBeenCalled();
    expect(api.importProfile).toHaveBeenCalledExactlyOnceWith(undefined);
    fireEvent.click(within(dialog).getByRole("button", { name: "打开配置" }));
    await waitFor(() =>
      expect(editKeys).toHaveBeenCalledExactlyOnceWith("ink"),
    );
    expect(screen.queryByRole("dialog", { name: /AntiMicroX/ })).toBeNull();
  });

  it("opens the profile only with the button; Esc and the close button leave it where it is", async () => {
    const { api, editKeys } = await setup({ config: baseConfig });
    api.importProfile.mockResolvedValueOnce(imported([dropped]));
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已从 AntiMicroX 导入「Ink」",
    });
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: /AntiMicroX/ })).toBeNull(),
    );
    // Closing the report reads it; it does not switch to the profile, which
    // would send its keys to the program in front.
    expect(editKeys).not.toHaveBeenCalled();
    expect(pill()?.textContent).toContain("已导入「Ink」");
    api.importProfile.mockResolvedValueOnce(imported([dropped]));
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "关闭导入报告" }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: /AntiMicroX/ })).toBeNull(),
    );
    expect(editKeys).not.toHaveBeenCalled();
  });

  it("says what each line says and no more about its group", async () => {
    const { api } = await setup({ config: baseConfig });
    api.importProfile.mockResolvedValueOnce(
      imported([dropped, approximate, info]),
    );
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已从 AntiMicroX 导入「Ink」",
    });
    // A line of a group can say more than any note of the group could: a
    // dropped line may be a duplicate that padmate does handle, an info line
    // may ask for a check.
    expect(dialog.querySelector(".report-note")).toBeNull();
    expect(within(dialog).queryByText(/没有对应的功能|没有损失/)).toBeNull();
  });

  it("shows only the groups that have lines", async () => {
    const { api } = await setup({ config: baseConfig });
    api.importProfile.mockResolvedValueOnce(imported([info]));
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "已从 AntiMicroX 导入「Ink」",
    });
    expect(
      within(dialog)
        .getAllByRole("heading", { level: 3 })
        .map((heading) => heading.textContent),
    ).toEqual(["信息（1）"]);
  });

  it("opens a profile that came over whole at once, with a line in the pill", async () => {
    const { api, editKeys } = await setup({ config: baseConfig });
    api.importProfile.mockResolvedValueOnce(imported([]));
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await waitFor(() =>
      expect(editKeys).toHaveBeenCalledExactlyOnceWith("ink"),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(pill()?.textContent).toContain("已导入「Ink」");
  });

  it("says why a file is refused in the shell's one line, and opens nothing", async () => {
    const { api, store, editKeys } = await setup({ config: baseConfig });
    api.importProfile.mockRejectedValueOnce({
      message: "这是原始摇杆的配置",
      code: -32024,
      data: { reason: "joystick" },
    });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await waitFor(() =>
      expect(store.getState().error).toBe(
        "这是原始摇杆的配置，只能导入游戏手柄的配置",
      ),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(editKeys).not.toHaveBeenCalled();
  });

  it("speaks English, and says the reasons are in Chinese", async () => {
    const { api, editKeys } = await setup({
      config: baseConfig,
      language: "en",
    });
    api.importProfile.mockResolvedValueOnce(
      imported([dropped, approximate, info]),
    );
    fireEvent.click(screen.getByRole("button", { name: "Import…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Imported “Ink” from AntiMicroX",
    });
    expect(
      within(dialog)
        .getAllByRole("heading", { level: 3 })
        .map((heading) => heading.textContent),
    ).toEqual(["Dropped (1)", "Approximate (1)", "Info (1)"]);
    expect(
      within(dialog).getByText("The reasons are written in Chinese."),
    ).toBeTruthy();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Open profile" }),
    );
    await waitFor(() =>
      expect(editKeys).toHaveBeenCalledExactlyOnceWith("ink"),
    );
  });
});
