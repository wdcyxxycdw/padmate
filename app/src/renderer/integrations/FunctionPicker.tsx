// The picker of an integration's functions (ARCHITECTURE §16.5): a modal
// dialog with a search field, the categories on the left and the functions on
// the right. A function that has a key puts that key into the binding; one
// without a key goes on to a second step that records a key, says who has it
// in the app, and writes it into the app's settings.
import {
  type ComponentType,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  IntegrationBackup,
  IntegrationCheck,
  IntegrationFunction,
  IntegrationFunctions,
  IntegrationInfo,
  IntegrationWritten,
} from "../../shared/integrations";
import {
  checkText,
  cycleNote,
  filterFunctions,
  firstKey,
  functionsById,
  itemFor,
  itemOf,
  pathText,
  putItem,
  writable,
} from "../state/integrations-model";
import type { Language } from "../state/store";
import { Dialog } from "../ui/controls";
import { Keycaps } from "../ui/Keycaps";
import { integrationFailure, useIntegrationHost } from "./IntegrationContext";
import "./integrations.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The shortcut capture of the drawer, handed in so this file does not import it. */
export type CaptureField = ComponentType<{
  value: string;
  onChange: (value: string) => void;
  label: string;
  language: Language;
}>;

/** The most rows drawn at once; a longer list asks for a narrower search. */
export const PICKER_ROWS = 200;

export interface Picked {
  keys: string;
  fn: IntegrationFunction;
  /** The name the binding had from the function it had before, if any. */
  previousName?: string;
}

type Step =
  | { kind: "list" }
  | { kind: "record"; fn: IntegrationFunction }
  | {
      kind: "done";
      fn: IntegrationFunction;
      keys: string;
      written: IntegrationWritten;
    };

export function FunctionPicker({
  open,
  info,
  profile,
  currentKeys,
  language,
  Capture,
  onClose,
  onPicked,
}: {
  open: boolean;
  info: IntegrationInfo;
  profile: string;
  /** The keys the binding sends now. */
  currentKeys: string;
  language: Language;
  Capture: CaptureField;
  onClose: () => void;
  /** Puts the keys into the binding, through the editor's normal edit. */
  onPicked: (picked: Picked) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const host = useIntegrationHost();
  const id = useId();
  const [functions, setFunctions] = useState<IntegrationFunctions | null>(null);
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const [selected, setSelected] = useState("");
  const [step, setStep] = useState<Step>({ kind: "list" });
  const [keys, setKeys] = useState("");
  const [check, setCheck] = useState<IntegrationCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const checks = useRef(0);
  const search = useRef<HTMLInputElement>(null);

  // A dialog that is opened again starts at the list.
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setCategory("");
    setSelected("");
    setStep({ kind: "list" });
    setKeys("");
    setCheck(null);
    setProblem("");
    setBusy(false);
    search.current?.focus();
  }, [open]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reads again on a new attempt
  useEffect(() => {
    if (!open || !host) return;
    let current = true;
    setLoadError("");
    host.store
      .getState()
      .loadIntegrationFunctions(info.id)
      .then((value) => {
        if (current) setFunctions(value);
      })
      .catch((error) => {
        if (current)
          setLoadError(integrationFailure(error, info.name, language));
      });
    return () => {
      current = false;
    };
  }, [open, host, info.id, info.name, language, attempt]);

  const byId = useMemo(
    () => functionsById(functions?.functions ?? []),
    [functions],
  );
  const matches = useMemo(
    () => filterFunctions(functions?.functions ?? [], query, category),
    [functions, query, category],
  );
  const shown = matches.slice(0, PICKER_ROWS);
  const chosen = byId.get(selected);

  const recordKeys = (next: string, fn: IntegrationFunction) => {
    setKeys(next);
    setProblem("");
    setCheck(null);
    if (!next || !host) return;
    const turn = ++checks.current;
    setChecking(true);
    host.store
      .getState()
      .api.checkIntegrationKey({
        integration: info.id,
        function: fn.id,
        keys: next,
      })
      .then((result) => {
        if (turn === checks.current) setCheck(result);
      })
      .catch((error) => {
        if (turn === checks.current)
          setProblem(integrationFailure(error, info.name, language));
      })
      .finally(() => {
        if (turn === checks.current) setChecking(false);
      });
  };

  /** Writes the item of the pick into the profile's file. */
  const putPicked = async (fn: IntegrationFunction, picked: string) => {
    if (!host) return undefined;
    const state = host.store.getState();
    const items = await state.loadIntegrationItems(profile, info.id);
    const previousName = itemFor(items.items, currentKeys)?.name;
    await state.api.putIntegrationItems({
      profile,
      integration: info.id,
      revision: items.revision,
      items: putItem(
        items.items,
        itemOf(fn, picked),
        currentKeys && currentKeys !== picked ? currentKeys : undefined,
      ),
    });
    void state.loadIntegrationItems(profile, info.id).catch(() => {});
    return previousName;
  };

  const pickChosen = async () => {
    if (!chosen || busy) return;
    if (firstKey(chosen) === "") {
      setKeys("");
      setCheck(null);
      setProblem("");
      setStep({ kind: "record", fn: chosen });
      return;
    }
    setBusy(true);
    setProblem("");
    try {
      const picked = firstKey(chosen);
      const previousName = await putPicked(chosen, picked);
      onPicked({
        keys: picked,
        fn: chosen,
        ...(previousName === undefined ? {} : { previousName }),
      });
      onClose();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };

  const writeIn = async (fn: IntegrationFunction) => {
    if (!host || !keys || busy) return;
    setBusy(true);
    setProblem("");
    try {
      const outcome = await host.write(info, [{ function: fn.id, add: keys }]);
      if (!outcome.ok) {
        if (!outcome.cancelled) setProblem(outcome.message);
        return;
      }
      let previousName: string | undefined;
      let trouble = "";
      try {
        previousName = await putPicked(fn, keys);
      } catch (error) {
        trouble = integrationFailure(error, info.name, language);
      }
      onPicked({
        keys,
        fn,
        ...(previousName === undefined ? {} : { previousName }),
      });
      setProblem(trouble);
      setStep({ kind: "done", fn, keys, written: outcome.written });
    } finally {
      setBusy(false);
    }
  };

  const undo = async (backup: IntegrationBackup) => {
    if (!host || busy) return;
    setBusy(true);
    setProblem("");
    try {
      if (!(await host.requireClosed(info))) return;
      await host.store
        .getState()
        .api.restoreIntegrationBackup(info.id, backup.id);
      setStep({ kind: "list" });
      onClose();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };

  const title = t(`从 ${info.name} 选择`, `Pick from ${info.name}`);
  let footer: React.ReactNode;
  let body: React.ReactNode;
  if (step.kind === "list") {
    footer = (
      <>
        <span className="spacer" />
        <button type="button" className="btn-ghost" onClick={onClose}>
          {t("取消", "Cancel")}
        </button>
        <button
          type="button"
          className="btn-primary"
          disabled={!chosen || busy}
          onClick={() => void pickChosen()}
        >
          {t("使用这个功能", "Use this function")}
        </button>
      </>
    );
    body = (
      <div className="picker" data-testid="integration-picker">
        <input
          ref={search}
          type="search"
          className="input picker-search"
          aria-label={t("搜索功能", "Search functions")}
          placeholder={t("名称、路径或快捷键", "Name, path or shortcut")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        {loadError ? (
          <div className="picker-message" role="alert">
            <p>{loadError}</p>
            <button
              type="button"
              className="btn"
              onClick={() => setAttempt(attempt + 1)}
            >
              {t("重试", "Retry")}
            </button>
          </div>
        ) : !functions ? (
          <p className="picker-message" role="status">
            {t("正在读取…", "Reading…")}
          </p>
        ) : (
          <div className="picker-body">
            <fieldset className="picker-categories">
              <legend className="visually-hidden">
                {t("分类", "Categories")}
              </legend>
              {[
                { id: "", name: t("全部", "All") },
                ...functions.categories,
              ].map((entry) => (
                <label
                  key={entry.id || "all"}
                  className="picker-category"
                  data-on={category === entry.id}
                >
                  <input
                    type="radio"
                    name={`${id}-category`}
                    checked={category === entry.id}
                    onChange={() => setCategory(entry.id)}
                  />
                  <span>{entry.name}</span>
                </label>
              ))}
            </fieldset>
            <fieldset className="picker-list">
              <legend className="visually-hidden">
                {t("功能", "Functions")}
              </legend>
              {shown.length === 0 ? (
                <p className="picker-empty">
                  {t("没有符合的功能。", "No function matches.")}
                </p>
              ) : (
                shown.map((fn) => {
                  const note = cycleNote(fn, byId, info.name, language);
                  const key = firstKey(fn);
                  return (
                    <label
                      key={fn.id}
                      className="picker-row"
                      data-on={selected === fn.id}
                      data-testid="picker-row"
                    >
                      <input
                        type="radio"
                        name={`${id}-function`}
                        checked={selected === fn.id}
                        onChange={() => setSelected(fn.id)}
                      />
                      <span className="picker-row-text">
                        <span className="picker-row-name">{fn.name}</span>
                        {fn.path.length > 0 && (
                          <span className="picker-row-path">
                            {pathText(fn.path)}
                          </span>
                        )}
                        {note && (
                          <span className="picker-row-note">{note}</span>
                        )}
                      </span>
                      <span className="picker-row-keys">
                        {key ? (
                          <Keycaps keys={key} language={language} />
                        ) : (
                          <span className="picker-row-unset">
                            {t("未设置", "Not set")}
                          </span>
                        )}
                      </span>
                    </label>
                  );
                })
              )}
              {matches.length > shown.length && (
                <p className="picker-empty" role="status">
                  {t(
                    `只显示前 ${PICKER_ROWS} 项，请缩小搜索。`,
                    `Showing the first ${PICKER_ROWS}. Narrow the search.`,
                  )}
                </p>
              )}
            </fieldset>
          </div>
        )}
        {problem && (
          <p className="picker-problem" role="alert">
            {problem}
          </p>
        )}
      </div>
    );
  } else if (step.kind === "record") {
    const fn = step.fn;
    const text = check ? checkText(check, info.name, language) : null;
    footer = (
      <>
        <button
          type="button"
          className="btn-ghost"
          disabled={busy}
          onClick={() => setStep({ kind: "list" })}
        >
          {t("返回", "Back")}
        </button>
        <span className="spacer" />
        <button type="button" className="btn-ghost" onClick={onClose}>
          {t("取消", "Cancel")}
        </button>
        <button
          type="button"
          className="btn-primary"
          disabled={!keys || !check || !writable(check) || busy || checking}
          onClick={() => void writeIn(fn)}
        >
          {t(`写入 ${info.name}`, `Write to ${info.name}`)}
        </button>
      </>
    );
    body = (
      <div className="picker-record" data-testid="integration-record">
        <p className="dialog-text">
          {t(
            `“${fn.name}”在 ${info.name} 里还没有快捷键。录一个键，写进 ${info.name} 的设置。`,
            `"${fn.name}" has no shortcut in ${info.name}. Record a key to write into its settings.`,
          )}
        </p>
        {fn.path.length > 0 && (
          <p className="picker-row-path">{pathText(fn.path)}</p>
        )}
        <Capture
          value={keys}
          onChange={(next) => recordKeys(next, fn)}
          label={t("快捷键", "Shortcut")}
          language={language}
        />
        {checking && (
          <p className="picker-message" role="status">
            {t("正在检查…", "Checking…")}
          </p>
        )}
        {text && (
          <div data-testid="integration-check" data-level={text.level}>
            <p className="dialog-text" role="status">
              {text.text}
            </p>
            {text.notes.map((note) => (
              <p key={note} className="dialog-text">
                {note}
              </p>
            ))}
          </div>
        )}
        {problem && (
          <p className="picker-problem" role="alert">
            {problem}
          </p>
        )}
      </div>
    );
  } else {
    const done = step;
    const made = done.written.backup;
    footer = (
      <>
        {made && (
          <button
            type="button"
            className="btn-ghost"
            disabled={busy}
            onClick={() => void undo(made)}
          >
            {t("撤销这次写入", "Undo this write")}
          </button>
        )}
        <span className="spacer" />
        <button type="button" className="btn-primary" onClick={onClose}>
          {t("完成", "Done")}
        </button>
      </>
    );
    body = (
      <div data-testid="integration-written">
        <p className="dialog-text" role="status">
          {t(
            `已把 ${done.keys} 写进 ${info.name}，设为“${done.fn.name}”。`,
            `Wrote ${done.keys} into ${info.name} as "${done.fn.name}".`,
          )}
        </p>
        {problem && (
          <p className="picker-problem" role="alert">
            {problem}
          </p>
        )}
      </div>
    );
  }
  return (
    <Dialog
      open={open}
      title={title}
      closeLabel={t("关闭", "Close")}
      onClose={onClose}
      className="picker-dialog"
      footer={footer}
    >
      {body}
    </Dialog>
  );
}
