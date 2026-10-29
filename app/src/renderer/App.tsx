import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useStore } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { Config } from "../shared/generated/schema";
import { BindingsListDialog } from "./BindingsListDialog";
import { CardProfileSettings } from "./CardProfileSettings";
import { ConfigRepairDialog } from "./ConfigRepairDialog";
import { ControllerWorkspace } from "./ControllerWorkspace";
import { createEditorStore, type EditorStore } from "./editor-store";
import { type Translate, translator } from "./i18n";
import {
  defaultProfileName,
  isDefaultProfile,
  profileName,
  readConfig,
  sortProfilesByName,
} from "./library-model";
import { Onboarding } from "./Onboarding";
import { shouldOffer } from "./onboarding-model";
import { ProfileSettingsDialog } from "./ProfileSettingsDialog";
import { DiagnosticsPage } from "./pages/DiagnosticsPage";
import { EmptyState, NoController, NoProfile } from "./pages/EmptyState";
import { LogsPage } from "./pages/LogsPage";
import { NewProfileDialog } from "./pages/ProfileDialogs";
import { ProfilesPage } from "./pages/ProfilesPage";
import { ScriptsPage } from "./pages/ScriptsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { StartFailure } from "./StartFailure";
import { configuredFunctionKey } from "./settings-model";
import { useSheetExport } from "./sheet-export";
import {
  activeProfileId,
  type Language,
  type PadmateState,
  type PadmateStore,
  type Page,
} from "./store";
import { UpdateDialogs } from "./UpdateDialogs";
import { Dialog, MenuButton, type MenuEntry, Segmented } from "./ui/controls";
import { Icon, type IconName } from "./ui/Icon";
import "./theme.css";
import "./ui/ui.css";
import "./shell.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

// Logs and diagnostics are views inside Settings, so they have no rail item
// of their own.
const pages: { page: Page; icon: IconName }[] = [
  { page: "overview", icon: "pad" },
  { page: "profiles", icon: "folder" },
  { page: "scripts", icon: "code" },
  { page: "settings", icon: "settings" },
];

/** The rail item a page belongs to. */
function railPage(page: Page): Page {
  return page === "logs" || page === "diagnostics" ? "settings" : page;
}

function StatusPill({ state, t }: { state: ShellState; t: Translate }) {
  const status = state.status;
  if (!status) return null;
  const key = status.paused
    ? "paused"
    : status.capturing
      ? "capturing"
      : status.suspended && status.suspended !== "none"
        ? "suspended"
        : "running";
  return (
    <span className="status-pill" data-state={key} role="status">
      <span className="status-dot" />
      {t(key)}
    </span>
  );
}

function PauseButton({ state, t }: { state: ShellState; t: Translate }) {
  const paused = !!state.status?.paused;
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={t(paused ? "resume" : "pause")}
      title={t(paused ? "resume" : "pause")}
      disabled={
        state.connection.phase !== "connected" || !state.status || state.busy
      }
      onClick={() => void state.togglePause()}
    >
      <Icon name={paused ? "play" : "pause"} stroke={2} />
    </button>
  );
}

function Rail({
  state,
  t,
  onNavigate,
}: {
  state: ShellState;
  t: Translate;
  onNavigate: (page: Page) => void;
}) {
  return (
    <aside className="sidebar">
      <div className="brand" title="padmate">
        <Icon name="pad" size={22} stroke={2} />
      </div>
      <nav aria-label={t("workspace")}>
        {pages.map(({ page, icon }) => {
          // The red dot is also a description, so it is not color-only.
          const scriptAlert =
            page === "scripts" &&
            state.scripts.some(
              (entry) =>
                entry.script.lastError ||
                entry.script.macros?.some(
                  (macro) => macro.error || macro.disabled,
                ),
            );
          return (
            <button
              type="button"
              key={page}
              className="nav-item"
              aria-current={railPage(state.page) === page ? "page" : undefined}
              aria-label={t(page)}
              aria-describedby={scriptAlert ? "nav-scripts-alert" : undefined}
              title={
                scriptAlert
                  ? `${t(page)} · ${tx(state.language, "有脚本出错", "A script has an error")}`
                  : t(page)
              }
              onClick={() => onNavigate(page)}
            >
              <Icon name={icon} size={20} stroke={1.7} />
              <span aria-hidden="true">{t(page)}</span>
              {scriptAlert && (
                <span id="nav-scripts-alert" className="nav-alert">
                  <span className="visually-hidden">
                    {tx(state.language, "有脚本出错", "A script has an error")}
                  </span>
                </span>
              )}
            </button>
          );
        })}
      </nav>
      <div
        className="rail-connection"
        data-phase={state.connection.phase}
        title={`${t(state.connection.phase)} · ${t("optionalUI")}${
          state.status ? ` · v${state.status.version.replace(/^v/, "")}` : ""
        }`}
      >
        <span className="connection-indicator" />
        <span>{t(state.connection.phase)}</span>
      </div>
    </aside>
  );
}

/**
 * The library as menu entries: one radio per profile, by name. A profile
 * with errors cannot be activated, so it is a disabled item instead.
 */
function profileSwitchEntries(
  state: Pick<ShellState, "profiles" | "status">,
  language: Language,
  onActivate: (id: string) => void,
): MenuEntry[] {
  const active = activeProfileId(state);
  const profiles = sortProfilesByName(state.profiles);
  const count = new Map<string, number>();
  for (const entry of profiles) {
    const name = profileName(entry, language);
    count.set(name, (count.get(name) ?? 0) + 1);
  }
  return profiles.map((entry): MenuEntry => {
    const name = profileName(entry, language);
    // Same names stay apart by their ID (menu items are keyed by label).
    const label = (count.get(name) ?? 0) > 1 ? `${name} (${entry.id})` : name;
    if (entry.problem)
      return {
        label,
        icon: "warn",
        hint: tx(language, "有错误", "Has errors"),
        disabled: true,
        onSelect: () => {},
      };
    return {
      kind: "radio",
      label,
      checked: entry.id === active,
      onSelect: () => {
        if (entry.id !== active) onActivate(entry.id);
      },
    };
  });
}

function ProfileMenu({
  state,
  editor,
  language,
  openRequest,
  onSettings,
  onActivate,
  onCreate,
  onManage,
}: {
  state: ShellState;
  editor: EditorStore;
  language: Language;
  /** A new value above 0 opens the menu (Choose profile on the empty page). */
  openRequest: number;
  onSettings: () => void;
  onActivate: (id: string) => void;
  onCreate: () => void;
  onManage: () => void;
}) {
  const draft = useStore(editor, (value) => value.draft);
  const device = state.status?.devices.find(
    (entry) => entry.info.id === state.selectedDevice,
  );
  const active = activeProfileId(state);
  const entry = state.profiles.find((profile) => profile.id === active);
  // Without an active library profile the daemon runs a placeholder named
  // "Mapping disabled"; only a session started with a file shows its name.
  const running =
    active || state.profilesError
      ? state.status?.slots.find((slot) => slot.device === state.selectedDevice)
          ?.profile
      : "";
  // The fixed default goes by 默认 / Default whatever it stores (PRO-28).
  const name =
    (isDefaultProfile(active) ? defaultProfileName(language) : "") ||
    draft?.name ||
    running ||
    (entry ? profileName(entry, language) : active) ||
    tx(language, "未启用配置", "No profile");
  const switching = profileSwitchEntries(state, language, onActivate);
  return (
    <MenuButton
      label={tx(language, "配置菜单", "Profile menu")}
      className="profile-button"
      align="start"
      testId="profile-menu"
      openRequest={openRequest}
      items={[
        ...(switching.length
          ? [
              {
                kind: "section" as const,
                label: tx(language, "切换配置", "Switch profile"),
              },
              ...switching,
              { kind: "separator" as const },
            ]
          : []),
        {
          label: tx(language, "新建配置…", "New profile…"),
          icon: "plus",
          disabled: !!state.profilesError,
          testId: "new-profile",
          onSelect: onCreate,
        },
        {
          label: tx(language, "管理配置和软件…", "Manage profiles and apps…"),
          icon: "folder",
          testId: "manage-profiles",
          onSelect: onManage,
        },
        {
          label: tx(language, "配置设置…", "Profile settings…"),
          icon: "sliders",
          hint: tx(language, "名称、判定时间", "Name, timing"),
          disabled: !draft,
          testId: "profile-settings",
          onSelect: onSettings,
        },
      ]}
    >
      <span className="profile-icon">
        <Icon name="edit" size={16} />
      </span>
      <span className="profile-text">
        <span className="profile-name">
          <span className="profile-name-text" title={name}>
            {name}
          </span>
          <Icon name="chevronDown" size={14} stroke={2} />
        </span>
        <span className="profile-sub">
          {device?.info.name ?? tx(language, "没有手柄", "No controller")}
        </span>
      </span>
    </MenuButton>
  );
}

function MoreMenu({
  state,
  editor,
  language,
  onList,
  onMessage,
}: {
  state: ShellState;
  editor: EditorStore;
  language: Language;
  onList: () => void;
  onMessage: (message: string) => void;
}) {
  const [exporting, setExporting] = useState(false);
  const { draft, format, setFormat, ...sheet } = useSheetExport({
    state,
    editor,
    language,
    onMessage,
  });
  const exportSheet = () => {
    setExporting(false);
    return sheet.exportSheet();
  };
  const display = state.labelDisplay;
  return (
    <>
      <MenuButton
        label={tx(language, "更多", "More")}
        testId="more-menu"
        items={[
          {
            kind: "section",
            label: tx(language, "按键名称显示", "Key labels"),
          },
          {
            kind: "radio",
            label: tx(language, "动作名称", "Action names"),
            checked: display === "actions",
            onSelect: () => state.setLabelDisplay("actions"),
          },
          {
            kind: "radio",
            label: tx(language, "快捷键", "Shortcuts"),
            checked: display === "keys",
            onSelect: () => state.setLabelDisplay("keys"),
          },
          { kind: "separator" },
          {
            label: tx(language, "查看所有按键", "List all keys"),
            icon: "list",
            disabled: !draft,
            onSelect: onList,
          },
          {
            label: tx(language, "导出快捷键对照表…", "Export shortcut table…"),
            icon: "doc",
            disabled: !draft,
            testId: "export-sheet",
            onSelect: () => setExporting(true),
          },
          { kind: "separator" },
          {
            label: tx(language, "松开所有按键", "Release all keys"),
            icon: "release",
            disabled: state.connection.phase !== "connected" || state.busy,
            onSelect: () => void state.releaseAll(),
          },
        ]}
      />
      <Dialog
        open={exporting}
        title={tx(language, "导出快捷键对照表", "Export shortcut table")}
        closeLabel={tx(language, "取消", "Cancel")}
        onClose={() => setExporting(false)}
        className="confirm-dialog"
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn-ghost"
              onClick={() => setExporting(false)}
            >
              {tx(language, "取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              onClick={() => void exportSheet()}
            >
              {tx(language, "导出", "Export")}
            </button>
          </>
        }
      >
        <div className="field">
          <span className="field-label">{tx(language, "格式", "Format")}</span>
          <Segmented
            label={tx(language, "格式", "Format")}
            testId="sheet-format"
            value={format}
            onChange={setFormat}
            options={[
              {
                value: "png",
                label: tx(language, "图片（PNG）", "Image (PNG)"),
              },
              { value: "markdown", label: "Markdown" },
            ]}
          />
        </div>
      </Dialog>
    </>
  );
}

function ConnectionPanel({ state, t }: { state: ShellState; t: Translate }) {
  const connecting = state.connection.phase === "connecting";
  return (
    <section className="connection-panel">
      <EmptyState
        title={t(connecting ? "connectionTitle" : "disconnected")}
        hint={t(connecting ? "connectionHint" : "disconnectedHint")}
        controller
      />
      <button
        type="button"
        className="btn-primary"
        disabled={connecting}
        onClick={() => void state.reconnect()}
      >
        <Icon name="refresh" size={15} stroke={2} />
        {t("reconnect")}
      </button>
      {railPage(state.page) === "settings" && (
        <label className="field offline-language">
          <span className="field-label">{t("language")}</span>
          <select
            className="select"
            value={state.language}
            onChange={(event) =>
              state.setLanguage(event.target.value === "en" ? "en" : "zh-CN")
            }
          >
            <option value="zh-CN">简体中文</option>
            <option value="en">English</option>
          </select>
        </label>
      )}
    </section>
  );
}

export function App({ store }: { store: PadmateStore }) {
  const editor = useMemo(
    () => createEditorStore(store.getState().api),
    [store],
  );
  // The Settings button of the view that was left, to take the focus back.
  const [focusEntry, setFocusEntry] = useState<"logs" | "diagnostics" | null>(
    null,
  );
  const backButton = useRef<HTMLButtonElement>(null);
  const [profileSettings, setProfileSettings] = useState(false);
  // 配置设置 opened from a card of the 配置 page (UI-27): the card's profile,
  // and the editor of the open dialog, which closing the window saves too.
  const [cardSettings, setCardSettings] = useState<string | null>(null);
  const cardEditor = useRef<EditorStore | null>(null);
  const closingCard = useRef(false);
  const onCardEditor = useCallback((next: EditorStore | null) => {
    cardEditor.current = next;
  }, []);
  const [newProfile, setNewProfile] = useState(false);
  const [bindingsList, setBindingsList] = useState(false);
  const [selected, setSelected] = useState<string>();
  const [toast, setToast] = useState("");
  // The first-run page (UI-38); see shouldOffer for when it opens itself.
  const [firstRun, setFirstRun] = useState(false);
  // Opens the profile menu from the empty mapping page; see MenuButton.
  const [menuRequest, setMenuRequest] = useState(0);
  const menuRequests = useRef(0);
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
  const language = state.language;
  const t = useMemo(() => translator(language), [language]);
  const selectedEntry = state.status?.devices.find(
    (device) => device.info.id === state.selectedDevice,
  );
  const editorID = state.status?.profileId ?? "";
  const editorDevice = selectedEntry?.key ?? "";
  const editorConnection = `${state.connection.phase}:${state.connection.pid ?? ""}`;
  useEffect(() => {
    if (state.profileChanges > 0) void editor.getState().profileChanged();
  }, [editor, state.profileChanges]);
  useEffect(() => {
    try {
      const config: Config = JSON.parse(state.config?.text ?? "{}");
      editor.getState().setGlobalTiming(config.timing ?? {});
    } catch {
      editor.getState().setGlobalTiming({});
    }
  }, [editor, state.config]);
  // The function key the settings choose (UI-30, ADR-0075): the mapping
  // page names it and does not edit its own bindings.
  useEffect(() => {
    editor
      .getState()
      .setFunctionKey(configuredFunctionKey(readConfig(state.config)));
  }, [editor, state.config]);
  const globalTiming = useMemo(() => {
    try {
      return (JSON.parse(state.config?.text ?? "{}") as Config).timing ?? {};
    } catch {
      return {};
    }
  }, [state.config]);
  useEffect(() => {
    void editor
      .getState()
      .activate(
        state.connection.phase === "connected"
          ? { id: editorID, device: editorDevice, connection: editorConnection }
          : null,
      );
  }, [
    editor,
    editorID,
    editorDevice,
    editorConnection,
    state.connection.phase,
  ]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Script status notifications invalidate detached registration metadata.
  useEffect(() => {
    void editor.getState().refreshMetadata();
  }, [editor, state.scripts]);
  // Scripts made or renamed on the scripts page show in the drawer.
  useEffect(() => {
    if (state.page === "overview") void editor.getState().refreshMacros();
  }, [editor, state.page]);
  useEffect(() => state.start(), [state.start]);
  // Closing the window (UI-43): a field still being typed in is committed
  // as if it was left, and Main waits for the saves (a few seconds at most).
  useEffect(() => {
    const api = store.getState().api;
    return api.onClosing(() => {
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();
      void Promise.all([
        editor.getState().flush(),
        cardEditor.current?.getState().flush(),
      ])
        .catch(() => false)
        .finally(() => void api.readyToClose().catch(() => {}));
    });
  }, [editor, store]);
  useEffect(() => {
    document.documentElement.lang = language;
    document.title = `padmate · ${t(state.page)}`;
  }, [language, state.page, t]);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(""), 4000);
    return () => clearTimeout(timer);
  }, [toast]);
  // A request is handled once: drop it so a remounted menu does not reopen.
  useEffect(() => {
    if (menuRequest) setMenuRequest(0);
  }, [menuRequest]);
  const connected = state.connection.phase === "connected";
  const offer = shouldOffer(state);
  useEffect(() => {
    if (offer) setFirstRun(true);
  }, [offer]);
  // Edits are saved as they are made (UI-43); leaving the mapping page
  // finishes a gesture that is still waiting to be saved.
  const navigate = (page: Page) => {
    if (state.page === "overview" && page !== "overview")
      void editor.getState().flush();
    state.setPage(page);
  };
  /**
   * Makes id the active profile, then shows the mapping page if asked (for
   * Edit keys). The open profile's edits reach the disk first; one that
   * cannot be saved (the mapping page says why) keeps the profile and shows
   * the mapping page. Resolves to whether id is the active profile now.
   */
  const activate = async (
    id: string | null,
    openMapping = false,
  ): Promise<boolean> => {
    const shell = store.getState();
    if (id === activeProfileId(shell)) {
      if (openMapping) shell.setPage("overview");
      return true;
    }
    if (!(await editor.getState().flush())) {
      store.getState().setPage("overview");
      setToast(
        tx(
          language,
          "当前配置有未保存的修改，未切换。",
          "The open profile has edits that are not saved, so it was not switched.",
        ),
      );
      return false;
    }
    const outcome = await store.getState().activateProfile(id);
    if (outcome.ok && openMapping) store.getState().setPage("overview");
    return outcome.ok;
  };
  // Closing 配置设置 of a card saves its edits first; one that cannot be
  // saved is said, as the mapping page's switch is (the dialog has no notice).
  const closeCardSettings = useCallback(async () => {
    if (closingCard.current) return;
    closingCard.current = true;
    try {
      const card = cardEditor.current?.getState();
      const unreadable = !!card?.error;
      const saved = (await card?.flush()) ?? true;
      setCardSettings(null);
      if (unreadable)
        setToast(
          tx(language, "无法读取这个配置。", "The profile could not be read."),
        );
      else if (!saved)
        setToast(
          tx(
            language,
            "配置设置未保存。",
            "The profile settings were not saved.",
          ),
        );
    } finally {
      closingCard.current = false;
    }
  }, [language]);
  const openProfileMenu = () => {
    menuRequests.current += 1;
    setMenuRequest(menuRequests.current);
  };
  // The views inside Settings: the logs and the diagnostics.
  const subView =
    state.page === "logs" || state.page === "diagnostics" ? state.page : null;
  const setPage = state.setPage;
  const backToSettings = useCallback(() => {
    if (subView) setFocusEntry(subView);
    setPage("settings");
  }, [setPage, subView]);
  const entryFocused = useCallback(() => setFocusEntry(null), []);
  // The focus return is one-shot: drop it when Settings does not show, for
  // example when the connection dropped while a view was open.
  const settingsShown = connected && state.page === "settings";
  useEffect(() => {
    if (focusEntry && !settingsShown) setFocusEntry(null);
  }, [focusEntry, settingsShown]);
  // A view starts at its back control; Escape goes back to Settings
  // unless a dialog is open or a filled search box clears itself first.
  useEffect(() => {
    if (!subView) return;
    backButton.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (document.querySelector("dialog[open]")) return;
      const target = event.target;
      if (target instanceof HTMLInputElement && target.value) return;
      event.preventDefault();
      backToSettings();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [subView, backToSettings]);
  const overview = state.page === "overview";
  const devices = state.status?.devices ?? [];
  const device =
    devices.find((entry) => entry.info.id === state.selectedDevice) ??
    devices[0];
  const activeId = activeProfileId(state);
  const usable = state.profiles.some((entry) => !entry.problem);
  const pageTitle =
    state.page === "scripts"
      ? "scriptsTitle"
      : state.page === "profiles"
        ? "profilesTitle"
        : "settingsTitle";
  // The mapping service did not start (UI-45): the window only says why.
  const failure = state.connection.failure;
  if (
    failure &&
    (state.connection.phase === "failed" ||
      state.connection.phase === "connecting")
  )
    return (
      <StartFailure
        api={state.api}
        failure={failure}
        retrying={state.connection.phase === "connecting"}
        language={language}
      />
    );
  return (
    <div className="app-shell">
      <Rail state={state} t={t} onNavigate={navigate} />
      <main className="main-content">
        <header className="topbar">
          <div className="topbar-start">
            {overview ? (
              <>
                {/* The page's name for a screen reader; the profile menu is
                    what a sighted user sees in its place (UI-24). */}
                <h1 className="visually-hidden">{t("overview")}</h1>
                <ProfileMenu
                  state={state}
                  editor={editor}
                  language={language}
                  openRequest={menuRequest}
                  onSettings={() => setProfileSettings(true)}
                  onActivate={(id) => void activate(id)}
                  onCreate={() => setNewProfile(true)}
                  onManage={() => navigate("profiles")}
                />
              </>
            ) : subView ? (
              <>
                <button
                  ref={backButton}
                  type="button"
                  className="page-back"
                  aria-label={tx(language, "返回设置", "Back to Settings")}
                  title={tx(
                    language,
                    "返回设置（Esc）",
                    "Back to Settings (Esc)",
                  )}
                  onClick={backToSettings}
                >
                  <Icon
                    name="chevronRight"
                    size={15}
                    stroke={2}
                    className="page-back-icon"
                  />
                  {t("settings")}
                </button>
                <h1 className="page-title page-subtitle">
                  {t(subView === "logs" ? "logsTitle" : "diagnosticsTitle")}
                </h1>
              </>
            ) : (
              <h1 className="page-title">{t(pageTitle)}</h1>
            )}
          </div>
          <div className="topbar-end">
            <StatusPill state={state} t={t} />
            <PauseButton state={state} t={t} />
            {overview && (
              <MoreMenu
                state={state}
                editor={editor}
                language={language}
                onList={() => setBindingsList(true)}
                onMessage={setToast}
              />
            )}
          </div>
        </header>
        <div className={`page-content page-${state.page}`}>
          {state.error && (
            <div className="shell-notice" role="alert">
              <span>{state.error}</span>
              <button
                type="button"
                className="icon-btn bare"
                aria-label={t("dismiss")}
                onClick={state.dismissError}
              >
                <Icon name="close" size={14} stroke={2} />
              </button>
            </div>
          )}
          {state.dropped > 0 && (
            <div className="shell-notice" role="status">
              {t("dropped")}
            </div>
          )}
          {!connected ? (
            // The frame never scrolls; a short window scrolls the panel itself.
            <div className="connection-scroll">
              <ConnectionPanel state={state} t={t} />
            </div>
          ) : overview ? (
            <section className="controller-stage" aria-label={t("controller")}>
              {!device ? (
                // A short window scrolls the message instead of clipping it.
                <div className="page-scroll page-scroll-center">
                  <NoController
                    language={language}
                    conflicts={state.status?.conflicts}
                    onShowLogs={() => navigate("logs")}
                  />
                </div>
              ) : !activeId && !state.profilesError ? (
                // Nothing to edit: the library is empty or mapping is off.
                // The title waits for the list, so it does not flip. A
                // session started with a file (no library) has no ID either
                // and keeps the controller view.
                state.profilesLoaded && (
                  <div className="page-scroll page-scroll-center">
                    <NoProfile
                      language={language}
                      hasProfiles={state.profiles.length > 0}
                      onCreate={() => setNewProfile(true)}
                      onChoose={usable ? openProfileMenu : undefined}
                    />
                  </div>
                )
              ) : (
                <ControllerWorkspace
                  key={device.info.id}
                  store={store}
                  info={device.info}
                  t={t}
                  editor={editor}
                  language={language}
                  labelDisplay={state.labelDisplay}
                  selected={selected}
                  onSelect={setSelected}
                  onShowScripts={() => navigate("scripts")}
                />
              )}
            </section>
          ) : state.page === "profiles" ? (
            <ProfilesPage
              state={state}
              onOpenMapping={() => navigate("overview")}
              onEditKeys={async (id) => {
                await activate(id, true);
              }}
              onProfileSettings={setCardSettings}
              saveEdits={(id) => editor.getState().flush(id)}
            />
          ) : state.page === "scripts" ? (
            <ScriptsPage state={state} editor={editor} />
          ) : state.page === "logs" ? (
            <LogsPage state={state} store={store} />
          ) : state.page === "diagnostics" ? (
            <DiagnosticsPage state={state} store={store} />
          ) : (
            <SettingsPage
              state={state}
              store={store}
              onShowLogs={() => navigate("logs")}
              onShowDiagnostics={() => navigate("diagnostics")}
              focusEntry={focusEntry}
              onEntryFocused={entryFocused}
            />
          )}
        </div>
        {toast && (
          <div className="shell-toast" role="status">
            {toast}
          </div>
        )}
      </main>
      <ProfileSettingsDialog
        open={profileSettings}
        editor={editor}
        language={language}
        onClose={() => setProfileSettings(false)}
      />
      <CardProfileSettings
        id={cardSettings}
        mappingEditor={editor}
        activeId={editorID}
        api={state.api}
        globalTiming={globalTiming}
        profileChanges={state.profileChanges}
        language={language}
        onEditor={onCardEditor}
        onClose={() => void closeCardSettings()}
      />
      <BindingsListDialog
        open={bindingsList}
        editor={editor}
        info={device?.info}
        language={language}
        onClose={() => setBindingsList(false)}
        onSelect={(control) => {
          setBindingsList(false);
          setSelected(control);
        }}
      />
      <Onboarding
        open={firstRun}
        connected={connected}
        state={state}
        onClose={(entered) => {
          state.finishOnboarding();
          setFirstRun(false);
          // The main page shows the fixed default; its keys are the user's.
          if (entered) state.setPage("overview");
        }}
      />
      <NewProfileDialog
        open={newProfile}
        state={state}
        onClose={() => setNewProfile(false)}
        // Made from the mapping page: its keys are edited next, right here.
        onCreated={(id) => void activate(id)}
      />
      <ConfigRepairDialog
        repair={connected ? state.configRepair : null}
        language={language}
        api={state.api}
        onConfirm={() => void state.acknowledgeConfigRepair()}
      />
      {/* PKG-10: a version on offer, the download, the install. */}
      <UpdateDialogs api={state.api} language={language} />
    </div>
  );
}
