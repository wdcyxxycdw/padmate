// 从已安装的软件中选择… (UI-46): a blocking, searchable list of the apps
// padmate has seen in front lately, then the programs of the Start Menu,
// each with its icon.
import { useEffect, useRef, useState } from "react";
import type { InstalledApp, PadmateAPI, RecentApp } from "../../shared/api";
import { loadAppIcon, peekAppIcon } from "../app-icons";
import {
  type AppRef,
  appBoundTo,
  effectiveRule,
  exeLabel,
  normalizeExe,
  profileName,
  readConfig,
  validExe,
} from "../library-model";
import type { Language, PadmateState } from "../store";
import { Dialog } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { shortName } from "./ProfileDialogs";
import "./profiles.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

type DialogState = Pick<
  PadmateState,
  | "api"
  | "language"
  | "config"
  | "profiles"
  | "recentApps"
  | "refreshRecentApps"
>;

/**
 * Recent apps the list offers for profile: one per program name, newest
 * first, leaving out apps the profile has already.
 */
export function pickableApps(
  state: Pick<PadmateState, "config" | "recentApps">,
  profile: string,
): RecentApp[] {
  const config = readConfig(state.config);
  const seen = new Set<string>();
  return state.recentApps.filter((app) => {
    if (appBoundTo(config, { exe: app.exe, path: app.path }, profile))
      return false;
    const key = normalizeExe(app.exe);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

/** "已绑定「X」" for an app that another profile's rule decides now. */
export function boundHint(
  state: Pick<PadmateState, "config" | "profiles" | "language">,
  profile: string,
  app: RecentApp,
): string | undefined {
  const config = readConfig(state.config);
  const rule = effectiveRule(config, { exe: app.exe, path: app.path });
  if (!rule || rule.profile === profile) return undefined;
  const entry = state.profiles.find((item) => item.id === rule.profile);
  const name = shortName(
    entry ? profileName(entry, state.language) : rule.profile,
    10,
  );
  return tx(state.language, `已绑定「${name}」`, `In “${name}”`);
}

/** One row of the list. */
export interface AppChoice {
  key: string;
  /** What the row says: the shortcut's name, or the program for a recent app. */
  name: string;
  exe: string;
  path: string;
  elevated: boolean;
  hint?: string;
}

/**
 * The rows for profile: recent apps first (pickableApps), then the
 * installed programs the profile has not got and that are not already
 * listed as a recent app, which binds the same program name.
 */
export function appChoices(
  state: Pick<PadmateState, "config" | "profiles" | "language" | "recentApps">,
  profile: string,
  installed: readonly InstalledApp[],
): { recent: AppChoice[]; installed: AppChoice[] } {
  const config = readConfig(state.config);
  const recent = pickableApps(state, profile)
    .filter((app) => validExe(app.exe))
    .map(
      (app): AppChoice => ({
        key: `recent:${app.path.toLowerCase()}`,
        name: exeLabel(app.exe),
        exe: app.exe,
        path: app.path,
        elevated: app.elevated,
        hint: boundHint(state, profile, app),
      }),
    );
  const taken = new Set(recent.map((choice) => normalizeExe(choice.exe)));
  const rest: AppChoice[] = [];
  for (const app of installed) {
    if (
      !validExe(app.exe) ||
      taken.has(normalizeExe(app.exe)) ||
      appBoundTo(config, { exe: app.exe, path: app.path }, profile)
    )
      continue;
    rest.push({
      key: `installed:${app.path.toLowerCase()}`,
      name: app.name,
      exe: app.exe,
      path: app.path,
      elevated: false,
      hint: boundHint(state, profile, {
        exe: app.exe,
        path: app.path,
        elevated: false,
      }),
    });
  }
  return { recent, installed: rest };
}

function matches(choice: AppChoice, query: string): boolean {
  const q = query.trim().toLowerCase();
  return (
    !q ||
    choice.name.toLowerCase().includes(q) ||
    choice.exe.toLowerCase().includes(q)
  );
}

/**
 * A row's icon, read from the executable (loadAppIcon asks once per path)
 * once the row comes near the visible part of the list, so a long list does
 * not ask for hundreds at once. A generic application icon stands in until
 * then, and for a file without an icon; never a letter.
 */
function ChoiceIcon({
  api,
  path,
}: {
  api: Pick<PadmateAPI, "getAppIcon">;
  path: string;
}) {
  const node = useRef<HTMLSpanElement>(null);
  const [near, setNear] = useState(typeof IntersectionObserver === "undefined");
  const [url, setUrl] = useState(() => peekAppIcon(path));
  useEffect(() => {
    if (near) return;
    const element = node.current;
    if (!element) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        setNear(true);
        observer.disconnect();
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [near]);
  useEffect(() => {
    if (!near) return;
    let current = true;
    void loadAppIcon(api, path).then((icon) => {
      if (current) setUrl(icon);
    });
    return () => {
      current = false;
    };
  }, [api, near, path]);
  return (
    <span ref={node} className="app-choice-icon" aria-hidden="true">
      {url ? (
        <img src={url} alt="" draggable={false} />
      ) : (
        <Icon name="app" size={18} />
      )}
    </span>
  );
}

export function InstalledAppsDialog({
  profile,
  state,
  onClose,
  onPick,
}: {
  /** The profile a chosen app is bound to; null while the dialog is closed. */
  profile: string | null;
  state: DialogState;
  onClose: () => void;
  /** An app was chosen; the owner closes the dialog. */
  onPick: (app: AppRef, elevated: boolean) => void;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const open = profile !== null;
  const [query, setQuery] = useState("");
  // null while the Start Menu is read.
  const [installed, setInstalled] = useState<InstalledApp[] | null>(null);
  const [failed, setFailed] = useState(false);
  const search = useRef<HTMLInputElement>(null);
  const { api, refreshRecentApps } = state;

  // Each opening reads both lists again; the answer of an earlier one that
  // comes late is dropped.
  // biome-ignore lint/correctness/useExhaustiveDependencies: Reads when it opens.
  useEffect(() => {
    if (!open) {
      setQuery("");
      return;
    }
    let current = true;
    setQuery("");
    setInstalled(null);
    setFailed(false);
    void refreshRecentApps();
    api.listInstalledApps().then(
      (list) => {
        if (current) setInstalled(Array.isArray(list) ? list : []);
      },
      () => {
        if (!current) return;
        setInstalled([]);
        setFailed(true);
      },
    );
    return () => {
      current = false;
    };
  }, [open]);
  // The dialog's own effect (a child) has run showModal already.
  useEffect(() => {
    if (open) search.current?.focus();
  }, [open]);

  const all = appChoices(state, profile ?? "", installed ?? []);
  const recent = all.recent.filter((choice) => matches(choice, query));
  const rest = all.installed.filter((choice) => matches(choice, query));
  const reading = installed === null;
  const filtering = query.trim() !== "";
  const headings = !filtering && recent.length > 0 && rest.length > 0;
  const choose = (choice: AppChoice) =>
    onPick({ exe: choice.exe, path: choice.path }, choice.elevated);
  const row = (choice: AppChoice) => (
    <li key={choice.key}>
      <button
        type="button"
        className="app-choice"
        onClick={() => choose(choice)}
      >
        <ChoiceIcon api={api} path={choice.path} />
        <span className="app-choice-name">{choice.name}</span>
        {choice.name !== choice.exe && (
          <span className="app-choice-exe mono">{choice.exe}</span>
        )}
        {choice.hint && <span className="app-choice-hint">{choice.hint}</span>}
      </button>
    </li>
  );
  const note = failed
    ? t("无法读取已安装的软件", "The installed apps cannot be read")
    : reading
      ? t("正在读取…", "Loading…")
      : recent.length + rest.length === 0
        ? filtering
          ? t("没有匹配的软件", "No matches")
          : t("没有可选的软件", "No apps to choose from")
        : "";
  return (
    <Dialog
      open={open}
      title={t("选择软件", "Choose an app")}
      closeLabel={t("关闭", "Close")}
      onClose={onClose}
      className="app-choose-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
        </>
      }
    >
      <input
        ref={search}
        type="search"
        className="input"
        aria-label={t("搜索", "Search")}
        value={query}
        spellCheck={false}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => {
          // Enter takes the first row, so a typed name needs no pointer.
          const first = recent[0] ?? rest[0];
          if (event.key !== "Enter" || !first) return;
          event.preventDefault();
          choose(first);
        }}
      />
      <div className="app-choose-list">
        {recent.length > 0 && (
          <section>
            {headings && (
              <h3 className="app-choose-heading">{t("最近使用", "Recent")}</h3>
            )}
            <ul>{recent.map(row)}</ul>
          </section>
        )}
        {rest.length > 0 && (
          <section>
            {headings && (
              <h3 className="app-choose-heading">{t("已安装", "Installed")}</h3>
            )}
            <ul>{rest.map(row)}</ul>
          </section>
        )}
        {note && (
          <p className="app-choose-note" role="status">
            {note}
          </p>
        )}
      </div>
    </Dialog>
  );
}
