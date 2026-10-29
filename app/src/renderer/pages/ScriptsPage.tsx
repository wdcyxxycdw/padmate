// 脚本 page (UI-41, SCR-50, SCR-51): the script library, shared by every
// profile and independent of the open one. Each script is a file of the
// library that keys of any profile run from the key drawer; the page
// creates, imports, opens, renames and deletes them, and says which
// profiles use each. Scripts are listed by creation time, oldest first. A
// legacy script (profile.json's script.entry) shows as its own card at the
// end, labeled with its profile.
import { useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import type {
  LegacyScript,
  MacroInfo,
  MacroList,
  MacroUse,
  ScriptImportProblem,
} from "../../shared/api";
import { LibraryErrorCode } from "../../shared/api";
import {
  checkMacroFile,
  type MacroNameKind,
  macroName,
  maxMacroName,
  maxMacros,
  sameMacroName,
} from "../../shared/macro-names";
import { listControlName } from "../control-names";
import { controllerLabels } from "../controller-model";
import {
  editorError,
  type Gesture,
  gestureName,
  gestures,
} from "../editor-model";
import type { EditorStore } from "../editor-store";
import type { Translate } from "../i18n";
import { sortMacros } from "../library-model";
import { macroRunProblems } from "../macro-context";
import type { Language, PadmateState } from "../store";
import { Dialog, MenuButton, type MenuEntry, Notice } from "../ui/controls";
import { Icon } from "../ui/Icon";
import "./pages.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
type MenuItem = Extract<MenuEntry, { kind?: "item" }>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;
/** A text in both UI languages, picked when it renders. */
type Text = readonly [zh: string, en: string];

/** The keys and gestures that run a script, e.g. "A 点按 · LB 长按". */
export function usesText(
  uses: MacroUse[],
  labels: Record<string, string>,
  language: Language,
): string {
  const parts = uses.map(
    (use) =>
      `${listControlName(use.control, labels, language)} ${
        (gestures as readonly string[]).includes(use.gesture)
          ? gestureName(use.gesture as Gesture, language)
          : use.gesture
      }`,
  );
  return [...new Set(parts)].join(" · ");
}

/**
 * The keys that run a script, by profile: "默认：A 点按 · LB 长按；绘画：
 * 十字键 ↑ 点按". Profiles keep the order they are first listed in.
 */
export function usedByText(
  uses: MacroUse[],
  labels: Record<string, string>,
  language: Language,
): string {
  const byProfile = new Map<string, { name: string; uses: MacroUse[] }>();
  for (const use of uses) {
    const group = byProfile.get(use.profile) ?? {
      name: use.profileName,
      uses: [],
    };
    group.uses.push(use);
    byProfile.set(use.profile, group);
  }
  return [...byProfile.values()]
    .map(
      (group) =>
        `${group.name}${tx(language, "：", ": ")}${usesText(group.uses, labels, language)}`,
    )
    .join(tx(language, "；", "; "));
}

/** Why a name typed for a script cannot be used, or "" when it can. */
export type NameProblem = MacroNameKind | "exists" | "";

/**
 * Checks a typed name (NFC, trimmed) with the daemon's rule, and against
 * the library's other scripts: names differ in more than case or the
 * extension. `except` is the script being renamed.
 */
export function nameProblem(
  name: string,
  macros: MacroInfo[],
  except?: string,
): NameProblem {
  const kind = checkMacroFile(`${name}.ts`);
  if (kind) return kind;
  return macros.some(
    (macro) => macro.file !== except && sameMacroName(macro.file, `${name}.ts`),
  )
    ? "exists"
    : "";
}

/** The input as a name: NFC, without spaces at either end. */
export function typedName(value: string): string {
  return value.normalize("NFC").trim();
}

const nameProblems: Record<Exclude<NameProblem, "">, Text> = {
  empty: ["请输入名称", "Enter a name"],
  extension: ["请输入名称", "Enter a name"],
  char: [
    '名称不能包含 \\ / : * ? " < > | . ~',
    'A name can’t contain \\ / : * ? " < > | . ~',
  ],
  length: [
    `名称最多 ${maxMacroName} 个字`,
    `Use at most ${maxMacroName} characters`,
  ],
  invalid: ["无法使用此名称", "This name can’t be used"],
  exists: ["已有同名脚本", "A script with this name exists"],
};

/** "脚本 1": the first numbered name no script of the library has. */
export function nextScriptName(macros: MacroInfo[], language: Language) {
  const base = tx(language, "脚本", "Script");
  for (let n = 1; ; n++) {
    const name = `${base} ${n}`;
    if (!nameProblem(name, macros)) return name;
  }
}

/**
 * How many actions of a profile.json text run a legacy script action, in the
 * layers, common and the function set (or the old functionKeys).
 */
export function scriptActionCount(text: string): number {
  let profile: unknown;
  try {
    profile = JSON.parse(text.startsWith("﻿") ? text.slice(1) : text);
  } catch {
    return 0;
  }
  let count = 0;
  const visit = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    const record = value as Record<string, unknown>;
    if (record.type === "script" && typeof record.name === "string") count++;
    else Object.values(record).forEach(visit);
  };
  const root = profile as Record<string, unknown> | null;
  visit(root?.layers);
  visit(root?.common);
  // The daemon ignores functionKeys when functionSet is there (ADR-0075).
  visit(root?.functionSet ?? root?.functionKeys);
  return count;
}

// Files Main refused before the daemon saw them (UIErrorData.data.reason).
const importProblems = new Map<ScriptImportProblem, Text>([
  [
    "type",
    ["只能导入 .ts 或 .js 文件", "Only .ts or .js files can be imported"],
  ],
  [
    "declaration",
    ["不能导入 .d.ts 类型文件", "A .d.ts type file can't be imported"],
  ],
  [
    "name",
    [
      "无法使用此文件名，请重命名后再导入",
      "This file name can't be used; rename the file and import it again",
    ],
  ],
  ["size", ["文件超过 1 MiB", "The file is larger than 1 MiB"]],
  ["encoding", ["文件不是 UTF-8 编码", "The file is not UTF-8 text"]],
  ["read", ["无法读取此文件", "The file could not be read"]],
  [
    "expired",
    ["确认超时，请重新导入", "The confirmation expired; import the file again"],
  ],
]);

export type Action =
  | "new"
  | "import"
  | "edit"
  | "rename"
  | "delete"
  | "remove"
  | "list";

const actionNames: Record<Action, Text> = {
  new: ["无法新建脚本", "Could not create the script"],
  import: ["无法导入脚本", "Could not import the script"],
  edit: ["无法打开编辑器", "Could not open an editor"],
  rename: ["无法重命名脚本", "Could not rename the script"],
  delete: ["无法删除脚本", "Could not delete the script"],
  remove: ["无法移除旧式脚本", "Could not remove the legacy script"],
  list: ["无法读取脚本列表", "Could not read the scripts"],
};

/**
 * The mapping page's edits of the profile are not on disk (the daemon
 * refused the draft, or saving failed again): renaming, deleting and
 * removing, which rewrite profile.json, wait for them.
 */
export class UnsavedEdits extends Error {
  constructor() {
    super("The mapping page has edits that are not saved");
  }
}

/** One line for a failed action: a translated reason, or the daemon's message. */
export function failureText(
  action: Action,
  error: unknown,
  language: Language,
): string {
  const failure = error as {
    code?: unknown;
    data?: { reason?: unknown; kind?: unknown; limit?: unknown };
  } | null;
  const data = failure?.data;
  const known =
    action === "import" && typeof data?.reason === "string"
      ? importProblems.get(data.reason as ScriptImportProblem)
      : typeof data?.kind === "string" && data.kind in nameProblems
        ? nameProblems[data.kind as Exclude<NameProblem, "">]
        : typeof data?.limit === "number"
          ? ([
              `最多 ${maxMacros} 个脚本`,
              `At most ${maxMacros} scripts`,
            ] as Text)
          : failure?.code === LibraryErrorCode.scriptExists
            ? nameProblems.exists
            : failure?.code === LibraryErrorCode.macroNotFound
              ? (["找不到文件", "File not found"] as Text)
              : undefined;
  if (known) return tx(language, ...known);
  const [zh, en] = actionNames[action];
  if (error instanceof UnsavedEdits)
    return tx(
      language,
      `${zh}：映射页的修改尚未保存`,
      `${en}: the mapping page has edits that are not saved`,
    );
  const message = editorError(error);
  return tx(language, `${zh}：${message}`, `${en}: ${message}`);
}

/** A script's error: a red dot and its first line; the whole text opens below. */
function ErrorLine({
  label,
  text,
  language,
}: {
  label: string;
  text?: string;
  language: Language;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<"" | "done" | "failed">("");
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(""), 2000);
    return () => clearTimeout(timer);
  }, [copied]);
  const first = text?.split("\n")[0] ?? "";
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text ?? "");
      setCopied("done");
    } catch {
      setCopied("failed");
    }
  };
  return (
    <div className="page-macro-error" data-testid="script-error">
      <div className="page-macro-error-row">
        <button
          type="button"
          className="page-macro-error-toggle"
          aria-expanded={text ? open : undefined}
          disabled={!text}
          onClick={() => setOpen(!open)}
        >
          <span className="page-macro-dot" aria-hidden="true" />
          <span className="page-macro-error-label">{label}</span>
          {first && <span className="page-macro-error-first">{first}</span>}
        </button>
        {text && (
          <button
            type="button"
            className="btn-ghost"
            onClick={() => void copy()}
          >
            <Icon name={copied === "done" ? "check" : "copy"} size={13} />
            {copied === "done"
              ? t("已复制", "Copied")
              : copied === "failed"
                ? t("无法复制", "Copy failed")
                : t("复制", "Copy")}
          </button>
        )}
      </div>
      {open && text && <pre>{text}</pre>}
    </div>
  );
}

/** One script, missing file or legacy script: a card with a ⋯ menu. */
function ScriptCard({
  name,
  tag,
  subtitle,
  warning,
  error,
  menu,
  menuLabel,
}: {
  name: string;
  tag?: string;
  subtitle: string;
  /** The subtitle is a problem (missing file, unusable name). */
  warning?: boolean;
  error?: { label: string; text?: string; language: Language };
  menu: MenuEntry[];
  menuLabel: string;
}) {
  return (
    <li>
      <article
        className="card page-macro"
        data-tone={error || warning ? "error" : undefined}
      >
        <div className="page-macro-head">
          <span className="page-macro-icon">
            <Icon name="script" size={16} stroke={2} />
          </span>
          <div className="page-macro-title">
            <h2 className="page-macro-name" title={name}>
              {name}
              {tag && <span className="page-macro-tag">{tag}</span>}
            </h2>
            <span className="page-macro-uses" data-warning={warning}>
              {subtitle}
            </span>
          </div>
          <MenuButton label={menuLabel} items={menu} testId="script-menu" />
        </div>
        {error && <ErrorLine {...error} />}
      </article>
    </li>
  );
}

/** The 新建 and 重命名 dialog: one name field, checked while typing. */
function NameDialog({
  open,
  title,
  confirm,
  initial,
  macros,
  except,
  failure,
  busy,
  language,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  confirm: string;
  initial: string;
  macros: MacroInfo[];
  /** The script being renamed: its own name is not taken. */
  except?: string;
  /** Why the last attempt failed. */
  failure: string;
  busy: boolean;
  language: Language;
  onCancel: () => void;
  onConfirm: (name: string) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [value, setValue] = useState(initial);
  // The name last confirmed: failure is about it, not a name typed since.
  const [tried, setTried] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Starts over each time the dialog opens.
  useEffect(() => {
    if (!open) return;
    setValue(initial);
    setTried(null);
    // The dialog's own effect (a child) has run showModal already.
    input.current?.focus();
    input.current?.select();
  }, [open]);
  const name = typedName(value);
  const problem = nameProblem(name, macros, except);
  const unchanged = except !== undefined && name === macroName(except);
  const message = problem
    ? tx(language, ...nameProblems[problem])
    : name === tried
      ? failure
      : "";
  const field = `${title}-name`;
  return (
    <Dialog
      open={open}
      title={title}
      closeLabel={t("关闭", "Close")}
      onClose={onCancel}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onCancel}>
            {t("取消", "Cancel")}
          </button>
          <button
            type="submit"
            form={field}
            className="btn-primary"
            disabled={!!problem || unchanged || busy}
          >
            {confirm}
          </button>
        </>
      }
    >
      <form
        id={field}
        className="page-macro-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (problem || unchanged || busy) return;
          setTried(name);
          onConfirm(name);
        }}
      >
        <label className="field">
          <span className="field-label">{t("名称", "Name")}</span>
          <input
            ref={input}
            className="input"
            value={value}
            spellCheck={false}
            autoComplete="off"
            aria-invalid={!!problem || undefined}
            onChange={(event) => setValue(event.target.value)}
          />
        </label>
        {message && (
          <p className="page-macro-form-error" role="alert">
            {message}
          </p>
        )}
      </form>
    </Dialog>
  );
}

/** A question with 取消 focused: deleting a file cannot be undone. */
function ConfirmDialog({
  open,
  title,
  text,
  confirm,
  busy,
  language,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  text: string;
  confirm: string;
  busy: boolean;
  language: Language;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    // The dialog's own effect (a child) has run showModal already.
    if (open) cancel.current?.focus();
  }, [open]);
  return (
    <Dialog
      open={open}
      title={title}
      closeLabel={t("关闭", "Close")}
      onClose={onCancel}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button
            ref={cancel}
            type="button"
            className="btn-ghost"
            onClick={onCancel}
          >
            {t("取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn-primary"
            disabled={busy}
            onClick={onConfirm}
          >
            {confirm}
          </button>
        </>
      }
    >
      {text && <p className="dialog-text">{text}</p>}
    </Dialog>
  );
}

interface Outcome {
  kind: "info" | "warning" | "error";
  text: (language: Language) => string;
}

type Question =
  | { kind: "new"; initial: string }
  | { kind: "rename"; macro: MacroInfo }
  | {
      kind: "delete";
      file: string;
      /** How many actions run it, and in how many profiles. */
      uses: number;
      profiles: number;
      missing: boolean;
    }
  | { kind: "remove"; legacy: LegacyScript; actions: number }
  | { kind: "replace"; name: string; token: string };

/** The profiles a list of uses is in. */
const profilesOf = (uses: MacroUse[]) =>
  new Set(uses.map((use) => use.profile)).size;

export function ScriptsPage({
  state,
  editor,
}: {
  state: ShellState;
  /**
   * The mapping page's editor (UI-43): renaming, deleting and removing
   * write profile.json, so they wait for its saves and read it again.
   */
  editor: EditorStore;
  /** Kept for the shell's page contract; this page uses inline text. */
  t?: Translate;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const { api, profileChanges, scripts } = state;
  const [list, setList] = useState<MacroList | null>(null);
  const [listError, setListError] = useState<{ error: unknown }>();
  const [focusCount, setFocusCount] = useState(0);
  const listToken = useRef(0);
  const [refreshes, setRefreshes] = useState(0);
  const refresh = () => setRefreshes((value) => value + 1);
  // The library is read again when the page opens, the window gets focus
  // back (a script saved in the editor) and on profile.changed, which
  // reports a changed profile or script file of any profile.
  useEffect(() => {
    const focused = () => setFocusCount((value) => value + 1);
    window.addEventListener("focus", focused);
    return () => window.removeEventListener("focus", focused);
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: profile.changed, focus and the page's own edits each read the list again.
  useEffect(() => {
    const token = ++listToken.current;
    api.listMacros().then(
      (value) => {
        if (token !== listToken.current) return;
        setList(value);
        setListError(undefined);
      },
      (error) => {
        if (token !== listToken.current) return;
        setListError({ error });
      },
    );
  }, [api, profileChanges, focusCount, refreshes]);

  // An edit of the open profile waits for its save (UI-43).
  const saving = useStore(editor, (value) => value.saving);
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [question, setQuestion] = useState<Question | null>(null);
  const [failure, setFailure] = useState("");

  const run = async (
    action: Action,
    task: () => Promise<Outcome | null>,
    inDialog = false,
  ) => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    if (!inDialog) setOutcome(null);
    setFailure("");
    try {
      const result = await task();
      if (result) setOutcome(result);
    } catch (error) {
      if (inDialog) setFailure(failureText(action, error, language));
      else
        setOutcome({
          kind: "error",
          text: (lang) => failureText(action, error, lang),
        });
    } finally {
      running.current = false;
      setBusy(false);
      refresh();
    }
  };
  /**
   * Renaming, deleting and removing rewrite profile.json in the daemon:
   * of every profile that uses the script (a legacy script's own profile).
   * The mapping page's edits of the open profile reach the disk first.
   * Afterwards its draft is read from disk again: one that only took the
   * new revision would save the old names or the removed actions back.
   * These edits are not undo steps, and the steps before them hold the old
   * names, so the open profile's undo history goes when the edit wrote it
   * (ADR-0054, ADR-0055). task resolves to the IDs of the profiles it wrote.
   */
  const rewrite = async (task: () => Promise<readonly string[]>) => {
    if (!(await editor.getState().flush())) throw new UnsavedEdits();
    const open = editor.getState().id;
    try {
      const written = await task();
      if (open && written.includes(open)) editor.getState().clearHistory(open);
    } finally {
      // A failed delete may have cleared references already.
      await editor.getState().reload();
    }
  };
  const noEditor: Outcome = {
    kind: "warning",
    text: (lang) => tx(lang, "无法打开编辑器", "Could not open an editor"),
  };
  const edit = (target: { file: string } | { profile: string }) =>
    void run("edit", async () => {
      const { opened } =
        "file" in target
          ? await api.editMacro(target.file)
          : await api.editScript(target.profile);
      return opened ? null : noEditor;
    });
  const create = (name: string) =>
    void run(
      "new",
      async () => {
        const { file } = await api.createMacro(name);
        setQuestion(null);
        // A new script opens in the editor right away.
        const { opened } = await api.editMacro(file).catch(() => ({
          opened: false,
        }));
        return opened ? null : noEditor;
      },
      true,
    );
  const rename = (macro: MacroInfo, name: string) =>
    void run(
      "rename",
      async () => {
        const extension = macro.file.slice(-3).toLowerCase();
        await rewrite(async () =>
          (
            await api.renameMacro(macro.file, `${name}${extension}`)
          ).profiles.map((profile) => profile.id),
        );
        setQuestion(null);
        return null;
      },
      true,
    );
  const imported = (
    result: Awaited<ReturnType<typeof api.importMacro>>,
  ): Outcome | null => {
    if ("canceled" in result || !("exists" in result)) return null;
    if (result.token) {
      setQuestion({ kind: "replace", name: result.name, token: result.token });
      return null;
    }
    const name = macroName(result.name);
    return {
      kind: "warning",
      text: (lang) => tx(lang, `「${name}」已存在`, `“${name}” already exists`),
    };
  };
  const importScript = () =>
    void run("import", async () => imported(await api.importMacro()));
  const replace = (token: string) => {
    setQuestion(null);
    void run("import", async () =>
      imported(await api.importMacro({ replace: true, token })),
    );
  };
  const remove = (question: Question) => {
    setQuestion(null);
    if (question.kind === "delete")
      void run("delete", async () => {
        await rewrite(async () =>
          (await api.deleteMacro(question.file)).profiles.map(
            (profile) => profile.id,
          ),
        );
        return null;
      });
    else if (question.kind === "remove")
      void run("remove", async () => {
        await rewrite(async () => {
          await api.detachScript(question.legacy.profile);
          return [question.legacy.profile];
        });
        return null;
      });
  };
  const askRemove = (legacy: LegacyScript) => {
    setQuestion({ kind: "remove", legacy, actions: 0 });
    // The question says how many script actions are cleared.
    api.getProfile(legacy.profile).then(
      (document) =>
        setQuestion((current) =>
          current?.kind === "remove" &&
          current.legacy.profile === legacy.profile
            ? { ...current, actions: scriptActionCount(document.text) }
            : current,
        ),
      () => {},
    );
  };

  const macros = list?.macros ?? [];
  const device =
    state.status?.devices.find(
      (entry) => entry.info.id === state.selectedDevice,
    ) ?? state.status?.devices[0];
  // Without a controller, keys are named as on an Xbox controller.
  const labels = controllerLabels(
    device?.info.type ?? "xbox",
    device?.info.buttonLabels,
    device?.info.buttonMask,
  );
  // Every controller's runtime, whichever profile it runs.
  const runs = macroRunProblems(scripts);
  const stopped = scripts.some(
    (entry) => entry.script.permanent || entry.script.wallStopped,
  );
  const full = macros.length >= maxMacros;
  const blocked = saving
    ? t("映射页的修改正在保存", "Saving the mapping page")
    : "";
  const subtitle = (uses: MacroUse[]) =>
    uses.length ? usedByText(uses, labels, language) : t("未使用", "Not used");
  const writes = (entry: MenuItem): MenuItem => ({
    ...entry,
    disabled: busy || saving,
    hint: blocked || undefined,
  });

  const cards = list && [
    ...sortMacros(list.macros).map((macro) => {
      const run = runs.get(macro.file);
      const error = macro.invalidName
        ? undefined
        : run?.disabled
          ? {
              label: t(
                "已停用，请保存文件后重试",
                "Stopped; save the file to retry",
              ),
              text: run.error,
              language,
            }
          : macro.problem
            ? { label: t("出错", "Error"), text: macro.problem, language }
            : run?.error
              ? { label: t("出错", "Error"), text: run.error, language }
              : undefined;
      const menu: MenuEntry[] = [
        ...(macro.invalidName
          ? []
          : [
              {
                label: t("编辑", "Edit"),
                icon: "edit" as const,
                disabled: busy,
                onSelect: () => edit({ file: macro.file }),
              },
            ]),
        writes({
          label: t("重命名", "Rename"),
          icon: "file",
          onSelect: () => {
            setFailure("");
            setQuestion({ kind: "rename", macro });
          },
        }),
        writes({
          label: t("删除", "Delete"),
          icon: "trash",
          danger: true,
          onSelect: () =>
            setQuestion({
              kind: "delete",
              file: macro.file,
              uses: macro.actions ?? macro.usedBy.length,
              profiles: profilesOf(macro.usedBy),
              missing: false,
            }),
        }),
      ];
      return (
        <ScriptCard
          key={`macro:${macro.file}`}
          name={macro.name}
          subtitle={
            macro.invalidName
              ? t("无法使用此名称", "This name can’t be used")
              : subtitle(macro.usedBy)
          }
          warning={macro.invalidName}
          error={error}
          menu={menu}
          menuLabel={t(
            `「${macro.name}」的操作`,
            `Actions for “${macro.name}”`,
          )}
        />
      );
    }),
    ...list.missing.map((macro) => {
      const name = macroName(macro.file);
      return (
        <ScriptCard
          key={`missing:${macro.file}`}
          name={name}
          subtitle={`${t("找不到文件", "File not found")} · ${subtitle(macro.usedBy)}`}
          warning
          menu={[
            writes({
              label: t("删除", "Delete"),
              icon: "trash",
              danger: true,
              onSelect: () =>
                setQuestion({
                  kind: "delete",
                  file: macro.file,
                  uses: macro.actions ?? macro.usedBy.length,
                  profiles: profilesOf(macro.usedBy),
                  missing: true,
                }),
            }),
          ]}
          menuLabel={t(`「${name}」的操作`, `Actions for “${name}”`)}
        />
      );
    }),
    // Legacy scripts run unchanged and belong to their profile: at the end,
    // each labeled with it.
    ...(list.legacy ?? []).map((legacy) => {
      const runtimes = scripts.filter(
        (entry) => entry.profileId === legacy.profile,
      );
      const failed = runtimes.find((entry) => entry.script.lastError)?.script;
      return (
        <ScriptCard
          key={`legacy:${legacy.profile}`}
          name={legacy.entry}
          tag={t(
            `旧式 · ${legacy.profileName}`,
            `Legacy · ${legacy.profileName}`,
          )}
          subtitle={t("随配置持续运行", "Runs with the profile")}
          error={
            failed?.lastError
              ? {
                  label: runtimes.some(
                    (entry) =>
                      entry.script.lastError &&
                      entry.script.state.toLowerCase() !== "running",
                  )
                    ? t(
                        "旧式脚本出错，所有脚本均未加载",
                        "The legacy script failed, so no scripts were loaded",
                      )
                    : t("出错", "Error"),
                  text: failed.lastError,
                  language,
                }
              : undefined
          }
          menu={[
            {
              label: t("编辑", "Edit"),
              icon: "edit",
              disabled: busy,
              onSelect: () => edit({ profile: legacy.profile }),
            },
            writes({
              label: t("移除", "Remove"),
              icon: "trash",
              danger: true,
              onSelect: () => askRemove(legacy),
            }),
          ]}
          menuLabel={t(
            `「${legacy.profileName}」的旧式脚本的操作`,
            `Actions for the legacy script of “${legacy.profileName}”`,
          )}
        />
      );
    }),
  ];

  const deleteName =
    question?.kind === "delete" ? macroName(question.file) : "";
  return (
    <div className="page-scroll">
      <div
        className="page-column page-column-scripts"
        aria-busy={busy || undefined}
      >
        <div className="page-macro-toolbar">
          <button
            type="button"
            className="btn-primary"
            disabled={busy || full || !list}
            title={
              full
                ? t(`最多 ${maxMacros} 个脚本`, `At most ${maxMacros} scripts`)
                : undefined
            }
            onClick={() => {
              setFailure("");
              setQuestion({
                kind: "new",
                initial: nextScriptName(macros, language),
              });
            }}
          >
            <Icon name="plus" size={14} stroke={2} />
            {t("新建", "New")}
          </button>
          <button
            type="button"
            className="btn"
            disabled={busy}
            onClick={importScript}
          >
            <Icon name="file" size={14} />
            {t("导入", "Import")}
          </button>
        </div>
        {stopped && (
          <Notice
            kind="error"
            role="alert"
            actions={
              <button
                type="button"
                className="btn"
                disabled={state.busy || busy}
                onClick={() => void state.reloadScripts()}
              >
                <Icon name="refresh" size={14} stroke={2} />
                {t("重新加载", "Reload")}
              </button>
            }
          >
            {t(
              "脚本连续出错或运行时间过长，已停止。",
              "Scripts stopped after repeated errors or running too long.",
            )}
          </Notice>
        )}
        {outcome && (
          <Notice
            kind={outcome.kind}
            role={outcome.kind === "error" ? "alert" : "status"}
            testId="script-outcome"
            actions={
              <button
                type="button"
                className="icon-btn bare"
                aria-label={t("关闭提示", "Dismiss")}
                onClick={() => setOutcome(null)}
              >
                <Icon name="close" size={14} stroke={2} />
              </button>
            }
          >
            {outcome.text(language)}
          </Notice>
        )}
        {listError && (
          <Notice kind="error" role="alert">
            {failureText("list", listError.error, language)}
          </Notice>
        )}
        {cards && cards.length > 0 && (
          <ul className="page-macro-list">{cards}</ul>
        )}
        {cards && cards.length === 0 && (
          <section className="page-macro-empty">
            <h2>{t("暂无脚本", "No scripts yet")}</h2>
            <p className="page-hint">
              {t(
                "脚本通过代码发送按键，可以等待或按条件发送不同的按键。",
                "A script sends keys from code: it can wait and choose keys by condition.",
              )}
            </p>
          </section>
        )}
      </div>
      <NameDialog
        open={question?.kind === "new"}
        title={t("新建脚本", "New script")}
        confirm={t("新建", "Create")}
        initial={question?.kind === "new" ? question.initial : ""}
        macros={macros}
        failure={failure}
        busy={busy}
        language={language}
        onCancel={() => setQuestion(null)}
        onConfirm={create}
      />
      <NameDialog
        open={question?.kind === "rename"}
        title={t("重命名", "Rename")}
        confirm={t("确定", "OK")}
        initial={question?.kind === "rename" ? question.macro.name : ""}
        except={question?.kind === "rename" ? question.macro.file : undefined}
        macros={macros}
        failure={failure}
        busy={busy || saving}
        language={language}
        onCancel={() => setQuestion(null)}
        onConfirm={(name) => {
          if (question?.kind === "rename") rename(question.macro, name);
        }}
      />
      <ConfirmDialog
        open={question?.kind === "delete"}
        title={
          question?.kind === "delete" && question.missing
            ? t(
                `清除对「${deleteName}」的引用？`,
                `Clear the references to “${deleteName}”?`,
              )
            : t(`删除「${deleteName}」？`, `Delete “${deleteName}”?`)
        }
        text={
          question?.kind !== "delete"
            ? ""
            : question.missing
              ? t(
                  `${question.uses} 处将被清空，涉及 ${question.profiles} 个配置。`,
                  `${question.uses} places in ${question.profiles} profiles will be cleared.`,
                )
              : question.uses
                ? t(
                    `有 ${question.uses} 处使用此脚本，涉及 ${question.profiles} 个配置，删除后这些位置将被清空。文件删除后无法恢复。`,
                    `It is used in ${question.uses} places in ${question.profiles} profiles, which will be cleared. The file can’t be recovered.`,
                  )
                : t("文件删除后无法恢复。", "The file can’t be recovered.")
        }
        confirm={
          question?.kind === "delete" && question.missing
            ? t("清除", "Clear")
            : t("删除", "Delete")
        }
        busy={busy || saving}
        language={language}
        onCancel={() => setQuestion(null)}
        onConfirm={() => question && remove(question)}
      />
      <ConfirmDialog
        open={question?.kind === "remove"}
        title={t("移除旧式脚本？", "Remove the legacy script?")}
        text={
          question?.kind !== "remove"
            ? ""
            : t(
                `配置「${question.legacy.profileName}」将不再运行 ${question.legacy.entry}，文件保留。${question.actions ? `${question.actions} 处脚本动作将被清空。` : ""}`,
                `“${question.legacy.profileName}” stops running ${question.legacy.entry}; the file stays.${question.actions ? ` ${question.actions} script actions will be cleared.` : ""}`,
              )
        }
        confirm={t("移除", "Remove")}
        busy={busy || saving}
        language={language}
        onCancel={() => setQuestion(null)}
        onConfirm={() => question && remove(question)}
      />
      <ConfirmDialog
        open={question?.kind === "replace"}
        title={
          question?.kind === "replace"
            ? t(
                `「${macroName(question.name)}」已存在，是否替换？`,
                `“${macroName(question.name)}” exists. Replace it?`,
              )
            : ""
        }
        text={t(
          "替换后，所有使用此脚本的按键都运行导入的内容。",
          "Replacing makes every key that uses this script run the imported contents.",
        )}
        confirm={t("替换", "Replace")}
        busy={busy}
        language={language}
        onCancel={() => setQuestion(null)}
        onConfirm={() => {
          if (question?.kind === "replace") replace(question.token);
        }}
      />
    </div>
  );
}
