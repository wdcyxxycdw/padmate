// Dialogs of the profiles page (UI-17, UI-18, PRO-20, SCR-51): create,
// rename and delete a library profile, answer a name clash of an imported
// file or scripts that clash with the library's, pick a window and confirm
// moving an app to another profile.
// The topbar profile menu reuses NewProfileDialog.
import {
  type FormEvent,
  type RefObject,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import type {
  AmgpImported,
  AmgpReportLine,
  AmgpSeverity,
  ProfileEntry,
  ProfileImported,
  ScriptClash,
} from "../../shared/api";
import { macroName } from "../../shared/macro-names";
import {
  exeLabel,
  failureText,
  isDefaultProfile,
  nameProblemText,
  parentName,
  profileHasScript,
  profileName,
  profileNameProblem,
  profilesBasedOn,
  uniqueName,
} from "../library-model";
import { activeProfileId, type Language, type PadmateState } from "../store";
import {
  TEMPLATES,
  type TemplateId,
  templateForDevice,
  templateName,
  templateProfileName,
} from "../templates";
import { FieldError } from "../ui/commit-field";
import { Dialog, Notice, Segmented } from "../ui/controls";
import "./profiles.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** A long profile name inside a sentence or a menu hint. */
export function shortName(name: string, max = 16): string {
  const chars = [...name];
  return chars.length > max ? `${chars.slice(0, max - 1).join("")}…` : name;
}

/** A text field with a one-line error below it. */
function TextField({
  label,
  value,
  onChange,
  error,
  inputRef,
  mono = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  error: string;
  inputRef: RefObject<HTMLInputElement | null>;
  mono?: boolean;
}) {
  const errorId = useId();
  const inputId = useId();
  // The error is outside the label: inside it, it would become part of the
  // field's name. It is an alert, so it is read when it appears (UI-24).
  return (
    <div className="field">
      <label className="field-label" htmlFor={inputId}>
        {label}
      </label>
      <input
        id={inputId}
        ref={inputRef}
        className={`input${mono ? " mono" : ""}`}
        value={value}
        spellCheck={false}
        aria-invalid={!!error}
        aria-describedby={error ? errorId : undefined}
        onChange={(event) => onChange(event.target.value)}
      />
      <FieldError id={errorId} text={error} />
    </div>
  );
}

/**
 * ReportOptions.inline for a dialog that shows a failure in its own alert:
 * true while the dialog is open. Once it is closed (or gone), the failure
 * goes to the shell's alert instead, so it is not lost.
 */
function useInlineReport(open: boolean): () => boolean {
  const shown = useRef(open);
  shown.current = open;
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  return useCallback(() => mounted.current && shown.current, []);
}

/** Focuses (and selects) the field each time the dialog opens. */
function useOpenFocus(open: boolean) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (!open) return;
    // The dialog's own effect (a child) has run showModal already.
    input.current?.focus();
    input.current?.select();
  }, [open]);
  return input;
}

type NewProfileState = Pick<
  PadmateState,
  "language" | "profiles" | "status" | "selectedDevice" | "createProfile"
>;

type StartFrom = "template" | "blank" | "copy";

/** The pad a template is preselected for: the selected one, else the first. */
function connectedPad(state: Pick<PadmateState, "status" | "selectedDevice">) {
  const devices = state.status?.devices ?? [];
  return (
    devices.find((entry) => entry.info.id === state.selectedDevice) ??
    devices[0]
  )?.info;
}

/**
 * 新建配置: a name, and a starter template (PRO-18), a blank profile or a
 * copy of the active one. A library with only the fixed default starts on
 * a template. Names are unique (F2). onCreated gets the new library ID after
 * the list has been reloaded.
 */
export function NewProfileDialog({
  open,
  onClose,
  onCreated,
  state,
}: {
  open: boolean;
  onClose: () => void;
  onCreated?: (id: string) => void;
  state: NewProfileState;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const formId = useId();
  // A template's name, numbered when a profile has it already.
  const suggestion = () =>
    uniqueName(templateProfileName(language), state.profiles);
  const [name, setName] = useState("");
  // The name is the template's suggestion, not typed by the user.
  const [suggested, setSuggested] = useState(false);
  const [source, setSource] = useState<StartFrom>("blank");
  // null follows the connected pad until the user picks a template.
  const [picked, setPicked] = useState<TemplateId | null>(null);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  // Starts over while rendering the opening, so the focus effect below
  // selects the suggested name.
  const [shown, setShown] = useState(false);
  if (shown !== open) {
    setShown(open);
    if (open) {
      const first = !state.profiles.some(
        (entry) => !isDefaultProfile(entry.id),
      );
      setName(first ? suggestion() : "");
      setSuggested(first);
      setSource(first ? "template" : "blank");
      setPicked(null);
      setTried(false);
      setBusy(false);
      setFailure("");
    }
  }
  const input = useOpenFocus(open);
  const inline = useInlineReport(open);
  const activeId = activeProfileId(state);
  const active = state.profiles.find((entry) => entry.id === activeId);
  // A broken profile cannot be renamed after copying it.
  const canCopy = !!active && !active.problem;
  const current = source === "copy" && !canCopy ? "blank" : source;
  const template = picked ?? templateForDevice(connectedPad(state));
  const problem = profileNameProblem(name, state.profiles);
  const changeName = (value: string) => {
    setName(value);
    setSuggested(false);
  };
  const changeSource = (next: StartFrom) => {
    setSource(next);
    if (next === "template" && !name.trim()) {
      setName(suggestion());
      setSuggested(true);
    } else if (next !== "template" && suggested) {
      setName("");
      setSuggested(false);
    }
  };
  const submit = async (event?: FormEvent) => {
    event?.preventDefault();
    setTried(true);
    if (problem || busy) return;
    setBusy(true);
    setFailure("");
    const outcome = await state.createProfile(name, {
      ...(current === "copy"
        ? { copyFrom: activeId }
        : current === "template"
          ? { template }
          : {}),
      inline,
    });
    setBusy(false);
    if (outcome.ok) {
      onClose();
      onCreated?.(outcome.value);
    } else setFailure(failureText(outcome.failure, language));
  };
  return (
    <Dialog
      open={open}
      title={t("新建配置", "New profile")}
      closeLabel={t("取消新建配置", "Cancel new profile")}
      onClose={onClose}
      className="profile-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
          <button
            type="submit"
            form={formId}
            className="btn-primary"
            disabled={busy}
          >
            {busy ? t("正在新建…", "Creating…") : t("新建", "Create")}
          </button>
        </>
      }
    >
      <form id={formId} className="profile-dialog-form" onSubmit={submit}>
        <TextField
          label={t("名称", "Name")}
          value={name}
          onChange={changeName}
          error={
            problem && (tried || problem === "taken")
              ? nameProblemText(problem, language)
              : ""
          }
          inputRef={input}
        />
        <div className="field">
          <span className="field-label">{t("内容", "Start from")}</span>
          <Segmented
            label={t("内容", "Start from")}
            value={current}
            onChange={changeSource}
            options={[
              { value: "template", label: t("模板", "Template") },
              { value: "blank", label: t("空白", "Blank") },
              {
                value: "copy",
                label: t("复制当前", "Copy current"),
                disabled: !canCopy,
                title: canCopy ? profileName(active, language) : undefined,
              },
            ]}
          />
        </div>
        {current === "template" && (
          <div className="field">
            <span className="field-label">{t("手柄", "Controller")}</span>
            <Segmented
              label={t("手柄", "Controller")}
              value={template}
              onChange={setPicked}
              options={TEMPLATES.map((info) => ({
                value: info.id,
                label: templateName(info.id, language),
              }))}
            />
            <span className="field-hint">
              {t(
                "多数快捷键需先在绘画软件中设置，可在映射页导出对照表。",
                "Most shortcuts need setting up in your painting app first; the Mapping page can export a table of them.",
              )}
            </span>
          </div>
        )}
        {failure && (
          <Notice kind="error" role="alert">
            {failure}
          </Notice>
        )}
      </form>
    </Dialog>
  );
}

/**
 * 重命名…: changes only the profile's display name, to one no other
 * profile has (F2). The fixed default is never renamed.
 */
export function RenameProfileDialog({
  entry,
  onClose,
  state,
}: {
  /** The profile to rename; null keeps the dialog closed. */
  entry: ProfileEntry | null;
  onClose: () => void;
  state: Pick<PadmateState, "language" | "profiles" | "renameProfile">;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const formId = useId();
  const open = !!entry;
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const input = useOpenFocus(open);
  const inline = useInlineReport(open);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Starts over only when another profile opens the dialog.
  useEffect(() => {
    if (!entry) return;
    setName(profileName(entry, language));
    setBusy(false);
    setFailure("");
  }, [entry?.id]);
  const problem = profileNameProblem(name, state.profiles, entry?.id);
  const submit = async (event?: FormEvent) => {
    event?.preventDefault();
    if (!entry || problem || busy) return;
    if (name.trim() === profileName(entry, language)) {
      onClose();
      return;
    }
    setBusy(true);
    setFailure("");
    const outcome = await state.renameProfile(entry.id, name, { inline });
    setBusy(false);
    if (outcome.ok) onClose();
    else setFailure(failureText(outcome.failure, language));
  };
  return (
    <Dialog
      open={open}
      title={t("重命名配置", "Rename profile")}
      closeLabel={t("取消重命名", "Cancel rename")}
      onClose={onClose}
      className="profile-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
          <button
            type="submit"
            form={formId}
            className="btn-primary"
            disabled={busy || !!problem}
          >
            {t("保存", "Save")}
          </button>
        </>
      }
    >
      <form id={formId} className="profile-dialog-form" onSubmit={submit}>
        <TextField
          label={t("名称", "Name")}
          value={name}
          onChange={setName}
          error={problem ? nameProblemText(problem, language) : ""}
          inputRef={input}
        />
        {failure && (
          <Notice kind="error" role="alert">
            {failure}
          </Notice>
        )}
      </form>
    </Dialog>
  );
}

/**
 * 删除…: deletes the folder for good, scripts included, and unbinds its
 * apps. 取消 has the focus, so Enter never deletes.
 */
export function DeleteProfileDialog({
  entry,
  onClose,
  onDeleted,
  state,
  returnFocus,
}: {
  entry: ProfileEntry | null;
  onClose: () => void;
  onDeleted?: (id: string) => void;
  state: Pick<PadmateState, "language" | "deleteProfile" | "api" | "profiles">;
  /** Where focus goes once the card (and its menu button) is gone. */
  returnFocus?: () => HTMLElement | null | undefined;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const { api } = state;
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const [deleted, setDeleted] = useState(false);
  // Whether profile.json names a script, read when the dialog opens.
  const [hasScript, setHasScript] = useState(false);
  const cancel = useRef<HTMLButtonElement>(null);
  const inline = useInlineReport(!!entry);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Starts over only when another profile opens the dialog.
  useEffect(() => {
    if (!entry) return;
    setBusy(false);
    setFailure("");
    setDeleted(false);
    setHasScript(false);
    // The dialog's own effect (a child) has run showModal already.
    cancel.current?.focus();
    let current = true;
    api.getProfile(entry.id).then(
      (document) => {
        if (current) setHasScript(profileHasScript(document.text));
      },
      () => {},
    );
    return () => {
      current = false;
    };
  }, [api, entry?.id]);
  const name = entry ? profileName(entry, language) : "";
  const remove = async () => {
    if (!entry || busy) return;
    setBusy(true);
    setFailure("");
    const outcome = await state.deleteProfile(entry.id, { inline });
    setBusy(false);
    if (outcome.ok) {
      setDeleted(true);
      onClose();
      onDeleted?.(entry.id);
    } else
      setFailure(
        failureText(outcome.failure, language, (id) =>
          parentName(id, state.profiles, language),
        ),
      );
  };
  // Profiles based on this one (PRO-19) are written out whole before it goes.
  const basedOn = entry ? profilesBasedOn(state.profiles, entry.id).length : 0;
  return (
    <Dialog
      open={!!entry}
      title={t(`删除「${shortName(name, 24)}」？`, `Delete “${name}”?`)}
      closeLabel={t("取消删除", "Cancel delete")}
      onClose={onClose}
      className="confirm-dialog"
      returnFocus={() => (deleted ? returnFocus?.() : null)}
      footer={
        <>
          <span className="spacer" />
          <button
            ref={cancel}
            type="button"
            className="btn-ghost"
            onClick={onClose}
          >
            {t("取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => void remove()}
          >
            {busy ? t("正在删除…", "Deleting…") : t("删除", "Delete")}
          </button>
        </>
      }
    >
      <p className="dialog-text">
        {t(
          "删除后无法恢复，绑定的软件将一并解除。",
          "This can't be undone. Its app bindings are removed too.",
        )}
        {hasScript && (
          <>
            <br />
            {t(
              "文件夹中的脚本也将一并删除。",
              "Script files in its folder are deleted too.",
            )}
          </>
        )}
        {basedOn > 0 && (
          <>
            <br />
            {t(
              `有 ${basedOn} 个配置基于它，将保留各自现有的按键。`,
              basedOn === 1
                ? "1 profile is based on it and keeps its current keys."
                : `${basedOn} profiles are based on it and keep their current keys.`,
            )}
          </>
        )}
      </p>
      {failure && (
        <Notice kind="error" role="alert">
          {failure}
        </Notice>
      )}
    </Dialog>
  );
}

const severityOrder: AmgpSeverity[] = ["dropped", "approximate", "info"];

/** Where a line of an AntiMicroX import report applies: "set 2 / lt / slot 1". */
function reportWhere(line: AmgpReportLine): string {
  const parts: string[] = [];
  if (line.set) parts.push(`set ${line.set}`);
  if (line.control) parts.push(line.control);
  if (line.slot) parts.push(`slot ${line.slot}`);
  return parts.join(" / ");
}

/**
 * What an AntiMicroX import dropped, approximated and noted (PRO-21), by
 * severity, with where in the file and why. The import is done. A group has
 * no note of its own: a line can say more than any note of its group could
 * (a dropped line may be a duplicate that padmate does handle, an info line
 * may ask for a check). 打开配置 opens the profile; Esc and the close button
 * only close the report, since opening the profile switches to it.
 */
export function AmgpReportDialog({
  result,
  language,
  onClose,
  onOpen,
}: {
  /** The import to report; the dialog is shown while there is one. */
  result: AmgpImported | null;
  language: Language;
  /** Esc and the close button. */
  onClose: () => void;
  /** 打开配置. */
  onOpen: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const name = result?.name ?? "";
  const group = (severity: AmgpSeverity) => ({
    title:
      severity === "dropped"
        ? t("丢弃", "Dropped")
        : severity === "approximate"
          ? t("近似", "Approximate")
          : t("信息", "Info"),
    lines: result?.report.filter((line) => line.severity === severity) ?? [],
  });
  return (
    <Dialog
      open={!!result}
      title={t(
        `已从 AntiMicroX 导入「${shortName(name, 24)}」`,
        `Imported “${name}” from AntiMicroX`,
      )}
      closeLabel={t("关闭导入报告", "Close the import report")}
      onClose={onClose}
      className="amgp-report-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-primary" onClick={onOpen}>
            {t("打开配置", "Open profile")}
          </button>
        </>
      }
    >
      {language === "en" && (
        <p className="dialog-text">The reasons are written in Chinese.</p>
      )}
      {severityOrder.map((severity) => {
        const { title, lines } = group(severity);
        if (lines.length === 0) return null;
        return (
          <section
            key={severity}
            className="report-group"
            data-severity={severity}
          >
            <h3>
              {tx(
                language,
                `${title}（${lines.length}）`,
                `${title} (${lines.length})`,
              )}
            </h3>
            <ul className="report-lines">
              {lines.map((line, index) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: The report is a fixed list; lines can repeat.
                <li key={index}>
                  {reportWhere(line) && (
                    <span className="report-where">{reportWhere(line)}</span>
                  )}
                  <span className="report-message">{line.message}</span>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </Dialog>
  );
}

/** A file whose name another profile goes by (PRO-20). */
export interface ImportClash {
  /** The profile with the name. */
  id: string;
  name: string;
  /** Main's token for the file it read. */
  token: string;
}

/**
 * Asks what to do with an imported file whose name the profile clash.id
 * goes by: 替换 overwrites that profile's folder with the file (the fixed
 * default keeps its name), 另存为 adds it under a name no profile has.
 * Enter saves it under the new name, so it never overwrites.
 */
export function ImportClashDialog({
  clash,
  onClose,
  onImported,
  onScriptClash,
  beforeReplace,
  state,
}: {
  /** The clash to answer; null keeps the dialog closed. */
  clash: ImportClash | null;
  onClose: () => void;
  onImported: (result: ProfileImported) => void;
  /** Scripts of the file clash with the library's: ask about them next. */
  onScriptClash: (clash: ImportScriptClash) => void;
  /** Saves the mapping page's edits of the profile before it is replaced. */
  beforeReplace?: (id: string) => Promise<unknown>;
  state: Pick<PadmateState, "language" | "profiles" | "importProfile">;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const formId = useId();
  const open = !!clash;
  // The clash being answered: another name may clash again.
  const [current, setCurrent] = useState<ImportClash | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  // Starts over while rendering a new file's clash, so the field shows the
  // suggested name from the first frame and the focus effect selects it.
  const [shown, setShown] = useState<string | null>(null);
  const token = clash?.token ?? null;
  if (shown !== token) {
    setShown(token);
    setCurrent(clash);
    if (clash) {
      setName(uniqueName(clash.name, state.profiles));
      setBusy(false);
      setFailure("");
    }
  }
  const input = useOpenFocus(open);
  const inline = useInlineReport(open);
  const answer = current ?? clash;
  const entry = state.profiles.find((profile) => profile.id === answer?.id);
  const taken = entry ? profileName(entry, language) : (answer?.name ?? "");
  const problem = profileNameProblem(name, state.profiles);
  const finish = async (
    options: { replace: string } | { name: string },
  ): Promise<void> => {
    if (!answer || busy) return;
    setBusy(true);
    setFailure("");
    if ("replace" in options) await beforeReplace?.(options.replace);
    const outcome = await state.importProfile(
      { token: answer.token, ...options },
      { inline },
    );
    setBusy(false);
    if (!outcome.ok) {
      setFailure(failureText(outcome.failure, language));
      return;
    }
    const result = outcome.value;
    if ("canceled" in result) onClose();
    else if ("exists" in result)
      // The name was taken meanwhile; the list now has it.
      setCurrent(result);
    else if ("conflict" in result) {
      onClose();
      onScriptClash(result);
    } else {
      onClose();
      onImported(result);
    }
  };
  const saveAs = (event?: FormEvent) => {
    event?.preventDefault();
    if (problem) return;
    void finish({ name: name.trim() });
  };
  return (
    <Dialog
      open={open}
      title={t(
        `已有名为「${shortName(taken, 24)}」的配置`,
        `A profile named “${taken}” exists`,
      )}
      closeLabel={t("取消导入", "Cancel import")}
      onClose={onClose}
      className="profile-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => answer && void finish({ replace: answer.id })}
          >
            {t("替换", "Replace")}
          </button>
          <button
            type="submit"
            form={formId}
            className="btn-primary"
            disabled={busy || !!problem}
          >
            {t("另存为", "Save as new")}
          </button>
        </>
      }
    >
      <form id={formId} className="profile-dialog-form" onSubmit={saveAs}>
        <p className="dialog-text">
          {t(
            "替换将覆盖该配置的全部内容。",
            "Replacing overwrites everything in that profile.",
          )}
        </p>
        <TextField
          label={t("新名称", "New name")}
          value={name}
          onChange={setName}
          error={problem ? nameProblemText(problem, language) : ""}
          inputRef={input}
        />
        {failure && (
          <Notice kind="error" role="alert">
            {failure}
          </Notice>
        )}
      </form>
    </Dialog>
  );
}

/** Scripts of an imported file that clash with the library's (SCR-51). */
export interface ImportScriptClash {
  scripts: ScriptClash[];
  /** Main's token for the file it read. */
  token: string;
}

/**
 * Asks what to do with scripts of an imported file whose names the library
 * has with other contents: 替换 overwrites the library's, which every
 * profile then runs; 都保留 adds the file's under numbered names and the
 * imported profile uses those. 都保留 is focused, so Enter never overwrites.
 * A script whose library namesake has another extension cannot be replaced
 * (the daemon numbers it either way): the list says which, and with none
 * replaceable the only answer is 导入.
 */
export function ImportScriptsDialog({
  clash,
  onClose,
  onExists,
  onImported,
  state,
}: {
  /** The clash to answer; null keeps the dialog closed. */
  clash: ImportScriptClash | null;
  onClose: () => void;
  /** The profile's name was taken meanwhile: ask about it again. */
  onExists: (clash: ImportClash) => void;
  onImported: (result: ProfileImported) => void;
  state: Pick<PadmateState, "language" | "importProfile">;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const open = !!clash;
  // The clash being answered: the library may change before the answer.
  const [current, setCurrent] = useState<ImportScriptClash | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const [shown, setShown] = useState<string | null>(null);
  const token = clash?.token ?? null;
  if (shown !== token) {
    setShown(token);
    setCurrent(clash);
    if (clash) {
      setBusy(false);
      setFailure("");
    }
  }
  const keepBoth = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    // The dialog's own effect (a child) has run showModal already.
    if (open) keepBoth.current?.focus();
  }, [open]);
  const inline = useInlineReport(open);
  const answer = current ?? clash;
  const scripts = answer?.scripts ?? [];
  const names = scripts.map((script) => macroName(script.existing));
  const replaceable = scripts.some((script) => script.replaceable);
  const numbered = replaceable && scripts.some((script) => !script.replaceable);
  const finish = async (macros: "replace" | "keep") => {
    if (!answer || busy) return;
    setBusy(true);
    setFailure("");
    const outcome = await state.importProfile(
      { token: answer.token, macros },
      { inline },
    );
    setBusy(false);
    if (!outcome.ok) {
      setFailure(failureText(outcome.failure, language));
      return;
    }
    const result = outcome.value;
    if ("canceled" in result) onClose();
    else if ("conflict" in result) setCurrent(result);
    else if ("exists" in result) {
      onClose();
      onExists(result);
    } else {
      onClose();
      onImported(result);
    }
  };
  return (
    <Dialog
      open={open}
      title={
        names.length === 1
          ? t(
              `已有脚本「${shortName(names[0], 24)}」，内容不同`,
              `A script “${names[0]}” exists with other contents`,
            )
          : t(
              `已有 ${names.length} 个同名脚本，内容不同`,
              `${names.length} scripts exist with other contents`,
            )
      }
      closeLabel={t("取消导入", "Cancel import")}
      onClose={onClose}
      className="profile-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
          {replaceable && (
            <button
              type="button"
              className="btn btn-danger"
              disabled={busy}
              onClick={() => void finish("replace")}
            >
              {t("替换", "Replace")}
            </button>
          )}
          <button
            ref={keepBoth}
            type="button"
            className="btn-primary"
            disabled={busy}
            onClick={() => void finish("keep")}
          >
            {replaceable ? t("都保留", "Keep both") : t("导入", "Import")}
          </button>
        </>
      }
    >
      <div className="profile-dialog-form">
        <p className="dialog-text">
          {replaceable
            ? t(
                "替换后，所有配置都使用导入的脚本。都保留则导入的脚本另加编号。",
                "Replacing makes every profile use the imported script. Keeping both numbers the imported one.",
              )
            : t("导入的脚本另加编号。", "The imported scripts are numbered.")}
        </p>
        {names.length > 1 && (
          <ul className="dialog-list">
            {names.map((name, index) => (
              <li key={scripts[index]?.file ?? name}>
                {numbered && !scripts[index]?.replaceable
                  ? t(`${name}（替换时仍另加编号）`, `${name} (numbered too)`)
                  : name}
              </li>
            ))}
          </ul>
        )}
        {failure && (
          <Notice kind="error" role="alert">
            {failure}
          </Notice>
        )}
      </div>
    </Dialog>
  );
}

/**
 * Asks before moving an app that another profile uses now. from and to are
 * display names.
 */
export function BindConflictDialog({
  request,
  language,
  onCancel,
  onConfirm,
}: {
  request: { exe: string; from: string; to: string } | null;
  language: Language;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const exe = request ? exeLabel(request.exe) : "";
  const from = request?.from ?? "";
  const to = request?.to ?? "";
  const question = t(
    `${exe} 当前使用「${shortName(from)}」，是否改为「${shortName(to)}」？`,
    `${exe} uses “${from}”. Switch it to “${to}”?`,
  );
  return (
    <Dialog
      open={!!request}
      title={t("此软件已绑定", "This app is already bound")}
      closeLabel={t("关闭", "Close")}
      onClose={onCancel}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onCancel}>
            {t("保留", "Keep it")}
          </button>
          <button type="button" className="btn-primary" onClick={onConfirm}>
            {t("更改", "Change")}
          </button>
        </>
      }
    >
      <p className="dialog-text">{question}</p>
    </Dialog>
  );
}

/**
 * 点选窗口… (PRO-16, F4): blocks the page while padmate waits for a click
 * into another app's window. It closes itself once a window is picked or
 * the wait ends; 取消, × and Esc stop waiting.
 */
export function WindowCaptureDialog({
  open,
  language,
  onCancel,
}: {
  open: boolean;
  language: Language;
  onCancel: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  return (
    <Dialog
      open={open}
      title={t("点选窗口", "Pick a window")}
      closeLabel={t("取消点选", "Stop picking")}
      onClose={onCancel}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn" onClick={onCancel}>
            {t("取消", "Cancel")}
          </button>
        </>
      }
    >
      <p className="dialog-text">
        {t(
          "请切换到要绑定的软件，并点击其窗口。",
          "Switch to the app and click its window.",
        )}
      </p>
    </Dialog>
  );
}
