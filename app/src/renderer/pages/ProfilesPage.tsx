// 配置 page (UI-17, UI-18, PRO-16, PRO-20, PRO-28, UI-42): the profile
// library and the apps bound to each profile, shown by their icons. The
// fixed default comes first: every app without a binding uses it. Every
// change is written to config.json at once. A card's 编辑按键 and 复制 are
// icon buttons, named by aria-label and title. 复制 makes a whole copy
// (ADR-0076), and the card's menu exports the profile as a .padmate file;
// 导入… adds one (or converts an AntiMicroX .amgp or .xml file, PRO-21, and
// lists what did not come over whole).
import {
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import type {
  AmgpImported,
  PadmateAPI,
  ProfileEntry,
  ProfileImported,
  RecentApp,
} from "../../shared/api";
import {
  iconPath,
  peekAppIcon,
  rememberAppPath,
  resolveAppIcon,
} from "../app-icons";
import {
  type AppRef,
  type AppRule,
  bindApp,
  bindingConflict,
  exeLabel,
  isDefaultProfile,
  profileName,
  readConfig,
  rulesFor,
  sameExe,
  setRuleEnabled,
  setSwitching,
  sortProfiles,
  switchingEnabled,
  unbindApp,
} from "../library-model";
import { activeProfileId, type Language, type PadmateState } from "../store";
import { MenuButton, type MenuEntry, Notice, Switch } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { AppPicker } from "./AppPicker";
import { InstalledAppsDialog } from "./InstalledAppsDialog";
import {
  AmgpReportDialog,
  BindConflictDialog,
  DeleteProfileDialog,
  type ImportClash,
  ImportClashDialog,
  type ImportScriptClash,
  ImportScriptsDialog,
  NewProfileDialog,
  RenameProfileDialog,
  WindowCaptureDialog,
} from "./ProfileDialogs";
import "./pages.css";
import "./profiles.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

const TOAST_MS = 4000;
const HIGHLIGHT_MS = 1600;

interface BindRequest {
  profile: string;
  app: AppRef;
  elevated: boolean;
  /** The profile that decides the app now. */
  from: string;
}

/**
 * A bound app's icon (UI-42), read by Main from its executable: the path of
 * a rule by file name comes from the apps seen lately, the path the app had
 * when it was bound, or Main's own search (resolveAppIcon). Without an icon
 * a generic application icon stands in, never a letter.
 */
function AppIconImage({
  exe,
  api,
  recent,
}: {
  exe: string;
  api: Pick<PadmateAPI, "getAppIcon" | "resolveAppPath">;
  recent: readonly RecentApp[];
}) {
  const known = iconPath(exe, recent);
  const key = `${exe}\n${known}`;
  const [icon, setIcon] = useState<{ key: string; url: string } | null>(null);
  useEffect(() => {
    let current = true;
    void resolveAppIcon(api, exe, known).then(({ path, url }) => {
      if (!current) return;
      // Kept for a later session, when the app is not seen yet.
      if (url) rememberAppPath(exe, path);
      setIcon({ key, url });
    });
    return () => {
      current = false;
    };
  }, [api, exe, known, key]);
  const url = icon?.key === key ? icon.url : peekAppIcon(known);
  return url ? (
    <img className="app-icon-image" src={url} alt="" draggable={false} />
  ) : (
    <span className="app-icon-generic" aria-hidden="true">
      <Icon name="app" size={22} />
    </span>
  );
}

/**
 * The apps bound to one profile as large icons, then its 添加软件 picker.
 * The name is the icon's tooltip and accessible name; the icon opens a
 * menu to turn the binding off or on, or to unbind it.
 */
function AppIcons({
  rules,
  state,
  editable,
  picker,
  onToggle,
  onRemove,
}: {
  rules: AppRule[];
  state: Pick<ShellState, "api" | "recentApps" | "language">;
  editable: boolean;
  picker?: ReactNode;
  onToggle: (rule: AppRule) => void;
  /** Resolves true once the binding is gone from the file. */
  onRemove: (rule: AppRule) => Promise<boolean>;
}) {
  const t = (zh: string, en: string) => tx(state.language, zh, en);
  const listRef = useRef<HTMLUListElement>(null);
  // After an unbind, focus moves to the next icon, or the previous one, or
  // 添加软件, instead of falling to <body> with the icon.
  const [refocus, setRefocus] = useState<{
    index: number;
    exe: string;
  } | null>(null);
  useLayoutEffect(() => {
    const element = listRef.current;
    if (!refocus || !element) return;
    // Wait for the render without the removed icon.
    if (rules.some((rule) => sameExe(rule.exe, refocus.exe))) return;
    setRefocus(null);
    const focused = document.activeElement;
    if (focused && focused !== document.body && !element.contains(focused))
      return;
    const buttons = [
      ...element.querySelectorAll<HTMLButtonElement>(
        "button.app-icon-button:not(:disabled)",
      ),
    ];
    (
      buttons[Math.min(refocus.index, buttons.length - 1)] ??
      element.querySelector<HTMLButtonElement>(".app-add")
    )?.focus();
  }, [refocus, rules]);
  const unbind = async (rule: AppRule, index: number) => {
    if (await onRemove(rule)) setRefocus({ index, exe: rule.exe });
  };
  const off = t("已停用", "Off");
  return (
    <ul className="app-icons" ref={listRef}>
      {rules.map((rule, index) => {
        const tooltip = rule.enabled
          ? rule.exe
          : t(`${rule.exe}（${off}）`, `${rule.exe} (${off})`);
        const image = (
          <AppIconImage
            exe={rule.exe}
            api={state.api}
            recent={state.recentApps}
          />
        );
        // The space separates the words in the accessible name.
        const name = (
          <span className="visually-hidden">
            {rule.label}
            {!rule.enabled && ` ${off}`}
          </span>
        );
        const items: MenuEntry[] = [
          {
            label: rule.enabled ? t("停用", "Turn off") : t("启用", "Turn on"),
            onSelect: () => onToggle(rule),
          },
          {
            label: t("解除绑定", "Unbind"),
            danger: true,
            onSelect: () => void unbind(rule, index),
          },
        ];
        return (
          <li
            key={rule.index}
            className="app-icon"
            data-enabled={rule.enabled}
            data-path={rule.isPath || undefined}
          >
            {editable ? (
              <MenuButton
                label={tooltip}
                className="app-icon-button"
                align="start"
                items={items}
              >
                {image}
                {name}
              </MenuButton>
            ) : (
              <span className="app-icon-button" title={tooltip}>
                {image}
                {name}
              </span>
            )}
          </li>
        );
      })}
      {picker && <li className="app-icons-add">{picker}</li>}
    </ul>
  );
}

export function ProfilesPage({
  state,
  onOpenMapping,
  onEditKeys,
  onProfileSettings,
  saveEdits,
  focusProfile,
  onProfileFocused,
}: {
  state: ShellState;
  /** Shows the mapping page; defaults to setPage("overview"). */
  onOpenMapping?: () => void;
  /**
   * 编辑按键 on a profile that is not active: the shell activates it after
   * the mapping page's edits are saved, as its topbar menu does, then shows
   * the mapping page. Resolves once that is settled.
   */
  onEditKeys: (id: string) => Promise<void>;
  /**
   * 配置设置… on a card (UI-27): the shell opens the dialog for profile id
   * where it is, without activating the profile or leaving this page.
   */
  onProfileSettings: (id: string) => void;
  /**
   * Waits until the mapping page's edits of profile id are on disk, before
   * an export reads the profile or an import replaces it (UI-43).
   */
  saveEdits?: (id: string) => Promise<unknown>;
  /** Scrolls to this profile's card and focuses its 添加软件, once. */
  focusProfile?: string | null;
  onProfileFocused?: () => void;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const config = readConfig(state.config);
  const editable = !!config;
  const switching = switchingEnabled(config);
  const activeId = activeProfileId(state);
  const profiles = sortProfiles(state.profiles);
  const nameOf = (id: string) => {
    const entry = state.profiles.find((profile) => profile.id === id);
    return entry ? profileName(entry, language) : id;
  };
  const openMapping = onOpenMapping ?? (() => state.setPage("overview"));

  const [newOpen, setNewOpen] = useState(false);
  const [renaming, setRenaming] = useState<ProfileEntry | null>(null);
  const [deleting, setDeleting] = useState<ProfileEntry | null>(null);
  const [importing, setImporting] = useState(false);
  // The report of an AntiMicroX import (PRO-21), shown before its profile opens.
  const [amgp, setAmgp] = useState<AmgpImported | null>(null);
  const [clash, setClash] = useState<ImportClash | null>(null);
  const [scriptClash, setScriptClash] = useState<ImportScriptClash | null>(
    null,
  );
  // The profile whose installed-apps dialog is open (UI-46).
  const [installedFor, setInstalledFor] = useState<string | null>(null);
  const [pendingBind, setPendingBind] = useState<BindRequest | null>(null);
  const [activating, setActivating] = useState("");
  // A one-line message in the floating pill; "done" shows a check.
  const [toast, setToast] = useState<{
    text: string;
    tone: "done" | "info";
  } | null>(null);
  const [elevated, setElevated] = useState("");
  const [highlight, setHighlight] = useState<{
    id: string;
    focus: boolean;
  } | null>(null);
  const [flash, setFlash] = useState("");
  const listRef = useRef<HTMLUListElement>(null);
  const newButton = useRef<HTMLButtonElement>(null);

  // The apps seen in front lately give the icons of apps bound by name, and
  // the installed-apps dialog lists them first; read once the page opens
  // (live events keep them current after).
  const refreshRecentApps = state.refreshRecentApps;
  const connected = state.connection.phase === "connected";
  useEffect(() => {
    if (connected) void refreshRecentApps();
  }, [connected, refreshRecentApps]);

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(null), TOAST_MS);
    return () => clearTimeout(timer);
  }, [toast]);

  // The save pill shows briefly; a failure's reason is in the shell's alert.
  const save = state.librarySave;
  const clearSave = state.clearLibrarySave;
  useEffect(() => {
    if (save.state !== "saved" && save.state !== "failed") return;
    const timer = setTimeout(
      () => clearSave(save.seq),
      save.state === "saved" ? 2500 : 6000,
    );
    return () => clearTimeout(timer);
  }, [save.state, save.seq, clearSave]);

  // A request from the shell (a profile made from the topbar menu), once.
  const focusHandled = useRef<string | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Follows a new request only.
  useEffect(() => {
    if (!focusProfile || focusHandled.current === focusProfile) {
      if (!focusProfile) focusHandled.current = null;
      return;
    }
    focusHandled.current = focusProfile;
    setHighlight({ id: focusProfile, focus: true });
    onProfileFocused?.();
  }, [focusProfile]);

  // Scrolls to a new card once it is listed, then flashes it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: Runs again when the list reloads with the new card.
  useEffect(() => {
    if (!highlight) return;
    const card = listRef.current?.querySelector<HTMLElement>(
      `[data-profile-id="${highlight.id}"]`,
    );
    if (!card) return;
    card.scrollIntoView?.({ block: "nearest", behavior: "smooth" });
    if (highlight.focus)
      card.querySelector<HTMLButtonElement>(".app-add")?.focus();
    setFlash(highlight.id);
    setHighlight(null);
  }, [highlight, state.profiles]);
  useEffect(() => {
    if (!flash) return;
    const timer = setTimeout(() => setFlash(""), HIGHLIGHT_MS);
    return () => clearTimeout(timer);
  }, [flash]);

  // ----- Apps ---------------------------------------------------------------

  const applyBind = async (profile: string, app: AppRef, admin: boolean) => {
    const current = readConfig(state.config);
    if (current && !bindApp(current, app, profile)) {
      setToast({
        text: t(
          `${exeLabel(app.exe)} 已在列表中`,
          `${exeLabel(app.exe)} is already listed`,
        ),
        tone: "info",
      });
      return;
    }
    const outcome = await state.updateConfig((source) =>
      bindApp(source, app, profile),
    );
    if (!outcome.ok) return;
    if (app.path) rememberAppPath(app.exe, app.path);
    const name = exeLabel(app.exe);
    setToast({ text: t(`已绑定 ${name}`, `${name} bound`), tone: "done" });
    setElevated(admin ? name : "");
  };

  /** Binds app to profile, asking first when another profile has it. */
  const requestBind = (profile: string, app: AppRef, admin = false) => {
    const current = readConfig(state.config);
    if (!current) return;
    const conflict = bindingConflict(current, app, profile);
    if (conflict) {
      setPendingBind({ profile, app, elevated: admin, from: conflict.profile });
      return;
    }
    void applyBind(profile, app, admin);
  };

  const toggle = (profile: string, rule: AppRule) => {
    if (rule.enabled)
      void state.updateConfig((source) =>
        setRuleEnabled(source, rule.exe, profile, false),
      );
    else {
      // Turning one on may take the app from another profile: ask first.
      const current = readConfig(state.config);
      const conflict =
        current && bindingConflict(current, { exe: rule.exe }, profile);
      if (conflict)
        setPendingBind({
          profile,
          app: { exe: rule.exe },
          elevated: false,
          from: conflict.profile,
        });
      else
        void state.updateConfig((source) =>
          setRuleEnabled(source, rule.exe, profile, true),
        );
    }
  };

  const remove = async (profile: string, rule: AppRule) =>
    (await state.updateConfig((source) => unbindApp(source, rule.exe, profile)))
      .ok;

  // ----- Window capture ----------------------------------------------------

  const captured = state.captured;
  const handled = useRef<typeof captured>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Handles each captured app once.
  useEffect(() => {
    if (!captured || handled.current === captured) return;
    handled.current = captured;
    state.clearCaptured();
    requestBind(
      captured.target,
      { exe: captured.app.exe, path: captured.app.path },
      captured.app.elevated,
    );
  }, [captured]);

  const capture = state.capture;
  const lastCapture = useRef(capture);
  const cancelled = useRef(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Follows the capture only.
  useEffect(() => {
    const previous = lastCapture.current;
    lastCapture.current = capture;
    if (previous && !capture && !captured && !cancelled.current)
      setToast({
        text: t("未点选任何窗口", "No window was picked"),
        tone: "info",
      });
    cancelled.current = false;
  }, [capture]);
  // Leaving the page ends a capture: nothing would bind what it picks.
  const cancelCapture = state.cancelCapture;
  useEffect(() => () => cancelCapture(), [cancelCapture]);
  const stopCapture = () => {
    cancelled.current = true;
    cancelCapture();
  };

  // ----- Profiles -----------------------------------------------------------

  const editKeys = async (entry: ProfileEntry) => {
    if (entry.id === activeId) {
      openMapping();
      return;
    }
    setActivating(entry.id);
    try {
      await onEditKeys(entry.id);
    } finally {
      setActivating("");
    }
  };

  // Copying, like exporting, reads the folder: the open profile's edits
  // reach the disk first.
  const copy = async (entry: ProfileEntry) => {
    await saveEdits?.(entry.id);
    const outcome = await state.copyProfile(entry.id);
    if (!outcome.ok) return;
    setHighlight({ id: outcome.value.id, focus: false });
    setToast({
      text: t(
        `已复制为「${outcome.value.name}」`,
        `Copied as “${outcome.value.name}”`,
      ),
      tone: "done",
    });
  };

  // ----- Backup (PRO-20) ------------------------------------------------------

  const exportFile = async (entry: ProfileEntry) => {
    await saveEdits?.(entry.id);
    const outcome = await state.exportProfile(entry.id);
    if (!outcome.ok || !outcome.value) return;
    const name = profileName(entry, language);
    setToast({
      text: t(`已导出「${name}」`, `Exported “${name}”`),
      tone: "done",
    });
  };

  const imported = (result: ProfileImported) => {
    setHighlight({ id: result.id, focus: false });
    const name = profileName(result, language);
    setToast({
      text: t(`已导入「${name}」`, `Imported “${name}”`),
      tone: "done",
    });
  };

  const importFile = async () => {
    if (importing) return;
    setImporting(true);
    try {
      // A failure is in the shell's alert.
      const outcome = await state.importProfile();
      if (!outcome.ok || "canceled" in outcome.value) return;
      if ("amgp" in outcome.value) {
        // A report to read first; a file that came over whole opens at once.
        if (outcome.value.report.length > 0) setAmgp(outcome.value);
        else {
          imported(outcome.value);
          void onEditKeys(outcome.value.id);
        }
      } else if ("exists" in outcome.value) setClash(outcome.value);
      else if ("conflict" in outcome.value) setScriptClash(outcome.value);
      else imported(outcome.value);
    } finally {
      setImporting(false);
    }
  };

  // 浏览… (UI-46): the .exe the file dialog gives is bound by its name; its
  // path gives the icon. A file Main refuses is said in the pill.
  const browse = async (profile: string) => {
    try {
      const chosen = await state.api.browseApp();
      if (chosen) requestBind(profile, { exe: chosen.exe, path: chosen.path });
    } catch {
      setToast({
        text: t(
          "请选择本机上的 .exe 文件",
          "Choose an .exe file on a local drive",
        ),
        tone: "info",
      });
    }
  };

  const picker = (profile: string) => (
    <AppPicker
      language={language}
      disabled={!editable}
      onCapture={() => {
        cancelled.current = false;
        state.startCapture(profile);
      }}
      onInstalled={() => setInstalledFor(profile)}
      onBrowse={() => void browse(profile)}
    />
  );

  const card = (entry: ProfileEntry) => {
    const id = entry.id;
    const name = profileName(entry, language);
    const active = id === activeId;
    const fixed = isDefaultProfile(id);
    const broken = !!entry.problem;
    const rules = rulesFor(config, id);
    // While this card activates, its edit button says so.
    const switching = activating === id;
    const editName = switching
      ? t("正在切换…", "Switching…")
      : t("编辑按键", "Edit keys");
    const exportItem: MenuEntry = {
      label: t("导出…", "Export…"),
      icon: "export",
      // The daemon exports only a profile.json that validates.
      disabled: broken,
      onSelect: () => void exportFile(entry),
    };
    // The settings dialog edits the profile's file, which has to be readable.
    const settingsItem: MenuEntry = {
      label: t("配置设置…", "Profile settings…"),
      icon: "sliders",
      disabled: broken,
      onSelect: () => onProfileSettings(id),
    };
    // The fixed default is neither renamed nor deleted (PRO-28).
    const items: MenuEntry[] = fixed
      ? [settingsItem, exportItem]
      : [
          settingsItem,
          {
            label: t("重命名…", "Rename…"),
            icon: "edit",
            disabled: broken,
            onSelect: () => setRenaming(entry),
          },
          exportItem,
          { kind: "separator" },
          {
            label: t("删除…", "Delete…"),
            icon: "trash",
            danger: true,
            onSelect: () => setDeleting(entry),
          },
        ];
    const nameId = `profile-card-${id}`;
    const apps = (
      <AppIcons
        rules={rules}
        state={state}
        editable={editable}
        picker={fixed ? undefined : picker(id)}
        onToggle={(rule) => toggle(id, rule)}
        onRemove={(rule) => remove(id, rule)}
      />
    );
    return (
      <li key={id} data-profile-id={id}>
        <article
          className="card profile-card"
          aria-labelledby={nameId}
          data-active={active || undefined}
          data-default={fixed || undefined}
          data-flash={flash === id || undefined}
        >
          <header className="profile-card-head">
            <div className="profile-card-title">
              <h3 id={nameId} className="profile-card-name" title={name}>
                {name}
              </h3>
              {active && (
                <span className="tag accent">{t("使用中", "In use")}</span>
              )}
            </div>
            <button
              type="button"
              className="icon-btn bare profile-card-edit"
              // The icon has no text: the name is the label and the tooltip.
              aria-label={editName}
              aria-busy={switching || undefined}
              // A profile with errors cannot be activated; one that is
              // active already still opens.
              disabled={!!activating || (broken && !active)}
              title={
                broken && !active
                  ? t("配置文件有错误，无法打开", "The profile file has errors")
                  : editName
              }
              onClick={() => void editKeys(entry)}
            >
              <Icon name="edit" size={14} stroke={2} />
            </button>
            <button
              type="button"
              className="icon-btn bare profile-card-edit"
              aria-label={t("复制", "Duplicate")}
              title={t("复制", "Duplicate")}
              // A broken profile cannot be renamed after copying it.
              disabled={broken}
              onClick={() => void copy(entry)}
            >
              <Icon name="copy" size={14} stroke={2} />
            </button>
            <MenuButton
              label={t(`「${name}」的更多操作`, `More for “${name}”`)}
              className="icon-btn bare"
              items={items}
            />
          </header>
          {broken && (
            <p className="profile-card-problem" title={entry.problem}>
              <Icon name="warn" size={13} stroke={2} />
              {t("配置文件有错误", "The profile file has errors")}
            </p>
          )}
          {fixed ? (
            <>
              <p className="profile-card-note">
                {t("未绑定的软件使用此配置", "Used by apps without a binding")}
              </p>
              {/* Older files may bind apps to it by rule; they can go. */}
              {rules.length > 0 && apps}
            </>
          ) : (
            <div className="profile-card-apps">
              <span className="profile-card-label">{t("用于", "Used in")}</span>
              {apps}
            </div>
          )}
        </article>
      </li>
    );
  };

  const noLibrary = !!state.profilesError && !state.config;
  const reload = (
    <button
      type="button"
      className="btn"
      disabled={state.busy}
      onClick={() => void state.refresh()}
    >
      {t("重新读取", "Reload")}
    </button>
  );
  // Saving wins; then a message; then how the last save ended.
  const pill =
    save.state === "saving"
      ? "saving"
      : toast
        ? toast.tone === "done"
          ? "saved"
          : "info"
        : save.state;

  return (
    <>
      {/* One floating live region for the save state and short messages;
          it stays mounted and is empty while idle. */}
      <p
        className="page-autosave profiles-pill"
        role="status"
        data-state={pill}
      >
        {pill === "saving" ? (
          t("正在保存…", "Saving…")
        ) : toast ? (
          <>
            <Icon
              name={toast.tone === "done" ? "check" : "info"}
              size={13}
              stroke={2.2}
              className="page-autosave-icon"
            />
            {toast.text}
          </>
        ) : pill === "saved" ? (
          <>
            <Icon
              name="check"
              size={13}
              stroke={2.2}
              className="page-autosave-icon"
            />
            {t("已保存", "Saved")}
          </>
        ) : pill === "failed" ? (
          <>
            <Icon name="warn" size={13} stroke={2} />
            {t("未保存", "Not saved")}
          </>
        ) : null}
      </p>
      <div className="page-scroll">
        <div
          className="page-column page-column-profiles"
          data-switching={switching ? "on" : "off"}
        >
          {/* The administrator warning stays in view while the page
              scrolls: the app may be bound on a card far down the page. */}
          {elevated && (
            <div className="profiles-float">
              <Notice
                kind="warning"
                role="status"
                testId="elevated-warning"
                actions={
                  <button
                    type="button"
                    className="icon-btn bare"
                    aria-label={t("关闭提示", "Dismiss")}
                    onClick={() => setElevated("")}
                  >
                    <Icon name="close" size={14} stroke={2} />
                  </button>
                }
              >
                {t(
                  `${elevated} 以管理员身份运行，可能无法接收 padmate 发送的快捷键。`,
                  `${elevated} runs as administrator and may not receive shortcuts from padmate.`,
                )}
              </Notice>
            </div>
          )}
          {noLibrary ? (
            <Notice
              kind="warning"
              title={t(
                "当前启动方式无法管理配置",
                "Profiles can't be managed in this session",
              )}
              actions={reload}
            >
              <span className="page-mono-message">{state.profilesError}</span>
            </Notice>
          ) : state.config?.problem ? (
            <Notice
              kind="error"
              title={t("无法读取设置文件", "The settings file cannot be read")}
              actions={reload}
            >
              <span>
                {t(
                  "请修复 config.json 后重新读取。",
                  "Fix config.json, then reload.",
                )}
              </span>
              <span className="page-mono-message">{state.config.problem}</span>
            </Notice>
          ) : !state.config && state.configError ? (
            <Notice
              kind="warning"
              title={t("无法读取设置文件", "The settings file cannot be read")}
              actions={reload}
            >
              <span className="page-mono-message">{state.configError}</span>
            </Notice>
          ) : state.profilesError ? (
            <Notice
              kind="warning"
              title={t("无法读取配置列表", "The profile list cannot be read")}
              actions={reload}
            >
              <span className="page-mono-message">{state.profilesError}</span>
            </Notice>
          ) : null}

          {!noLibrary && (
            <>
              <div className="card">
                <div className="row">
                  <div className="row-text">
                    <span className="row-title">
                      {t("按软件自动切换配置", "Switch profiles by app")}
                    </span>
                  </div>
                  <Switch
                    label={t("按软件自动切换配置", "Switch profiles by app")}
                    checked={switching}
                    disabled={!editable}
                    onChange={(on) =>
                      void state.updateConfig((source) =>
                        setSwitching(source, on),
                      )
                    }
                  />
                </div>
              </div>

              <section
                className="page-section"
                aria-labelledby="profiles-heading"
              >
                <div className="profiles-head">
                  <h2 id="profiles-heading" className="section-title">
                    {t("配置", "Profiles")}
                  </h2>
                  <div className="profiles-actions">
                    <button
                      type="button"
                      className="btn"
                      disabled={importing}
                      onClick={() => void importFile()}
                    >
                      <Icon name="import" size={14} stroke={2} />
                      {t("导入…", "Import…")}
                    </button>
                    <button
                      ref={newButton}
                      type="button"
                      className="btn-primary"
                      onClick={() => setNewOpen(true)}
                    >
                      <Icon name="plus" size={14} stroke={2.2} />
                      {t("新建配置", "New profile")}
                    </button>
                  </div>
                </div>
                {profiles.length ? (
                  <ul className="profiles-grid" ref={listRef}>
                    {profiles.map(card)}
                  </ul>
                ) : (
                  <p className="card profiles-empty">
                    {state.profilesLoaded
                      ? t("暂无配置", "No profiles yet")
                      : t("正在读取…", "Loading…")}
                  </p>
                )}
              </section>
            </>
          )}
        </div>
      </div>

      <NewProfileDialog
        open={newOpen}
        state={state}
        onClose={() => setNewOpen(false)}
        onCreated={(id) => setHighlight({ id, focus: true })}
      />
      <RenameProfileDialog
        entry={renaming}
        state={state}
        onClose={() => setRenaming(null)}
      />
      <AmgpReportDialog
        result={amgp}
        language={language}
        // Esc and the close button read the report and stay: the new card
        // flashes. Only 打开配置 switches to the profile.
        onClose={() => {
          const done = amgp;
          setAmgp(null);
          if (done) imported(done);
        }}
        onOpen={() => {
          const done = amgp;
          setAmgp(null);
          if (done) void onEditKeys(done.id);
        }}
      />
      <ImportClashDialog
        clash={clash}
        state={state}
        onClose={() => setClash(null)}
        beforeReplace={saveEdits}
        onImported={imported}
        onScriptClash={setScriptClash}
      />
      <ImportScriptsDialog
        clash={scriptClash}
        state={state}
        onClose={() => setScriptClash(null)}
        onExists={setClash}
        onImported={imported}
      />
      <DeleteProfileDialog
        entry={deleting}
        state={state}
        onClose={() => setDeleting(null)}
        returnFocus={() => newButton.current}
      />
      <InstalledAppsDialog
        profile={installedFor}
        state={state}
        onClose={() => setInstalledFor(null)}
        onPick={(app, admin) => {
          const profile = installedFor;
          setInstalledFor(null);
          if (profile) requestBind(profile, app, admin);
        }}
      />
      <WindowCaptureDialog
        open={!!capture}
        language={language}
        onCancel={stopCapture}
      />
      <BindConflictDialog
        request={
          pendingBind && {
            exe: pendingBind.app.exe,
            from: nameOf(pendingBind.from),
            to: nameOf(pendingBind.profile),
          }
        }
        language={language}
        onCancel={() => setPendingBind(null)}
        onConfirm={() => {
          if (pendingBind)
            void applyBind(
              pendingBind.profile,
              pendingBind.app,
              pendingBind.elevated,
            );
          setPendingBind(null);
        }}
      />
    </>
  );
}
