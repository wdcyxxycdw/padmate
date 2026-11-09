// The picker of an integration's functions (ARCHITECTURE §16.5): a modal
// dialog with a search field and the app's own menu levels as one tree (groups
// open and close; the search shows the matching functions as a flat list). A
// function that has a key puts that key into the binding; one without a key
// goes on to a second step that records a key, says who has it in the app, and
// writes it into the app's settings. The binding is only touched after every
// step has worked, so a failure or a cancel leaves it as it was.
import {
  type ComponentType,
  type KeyboardEvent,
  useEffect,
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
  TreeNode,
} from "../../shared/integrations";
import {
  areasOf,
  checkText,
  cycleNote,
  filterFunctions,
  firstKey,
  functionsById,
  groupIds,
  itemFor,
  itemOf,
  pathText,
  pruneTree,
  putItem,
  type TreeRow,
  visibleRows,
  writable,
} from "../state/integrations-model";
import type { Language } from "../state/store";
import { Dialog } from "../ui/controls";
import { Icon } from "../ui/Icon";
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

/** One row of the picker's tree: a menu node, or a function the search found. */
interface Entry {
  key: string;
  name: string;
  /** The function the row picks; none for a group that is only a group. */
  fn?: IntegrationFunction;
  depth: number;
  parent?: string;
  expandable: boolean;
  expanded: boolean;
  position: number;
  siblings: number;
  /** Set on the rows after the first of a function that has several keys. */
  keyIndex?: number;
}

/**
 * The rows of a tree row: itself, then one row for each key after the first
 * of its function, with the name blank (the keys of one function are one
 * item, as the app's own list shows them).
 */
function entriesOf(
  row: TreeRow,
  byId: ReadonlyMap<string, IntegrationFunction>,
): Entry[] {
  const fn = row.node.function ? byId.get(row.node.function) : undefined;
  const first: Entry = {
    key: row.key,
    name: row.node.name,
    ...(fn ? { fn } : {}),
    depth: row.depth,
    ...(row.parent === undefined ? {} : { parent: row.parent }),
    expandable: row.expandable,
    expanded: row.expanded,
    position: row.position,
    siblings: row.siblings,
  };
  const more: Entry[] = (fn?.keys ?? []).slice(1).map((_, index) => ({
    key: `${row.key}#${index + 1}`,
    name: "",
    ...(fn ? { fn } : {}),
    depth: row.depth,
    ...(row.parent === undefined ? {} : { parent: row.parent }),
    expandable: false,
    expanded: false,
    position: row.position,
    siblings: row.siblings,
    keyIndex: index + 1,
  }));
  return [first, ...more];
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
  const [functions, setFunctions] = useState<IntegrationFunctions | null>(null);
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [query, setQuery] = useState("");
  // The groups that are open, by node ID: kept while the dialog is open.
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  // The row that is the tab stop of the tree.
  const [focusKey, setFocusKey] = useState("");
  const [selected, setSelected] = useState("");
  // The key of the selected function the binding gets (a function can have
  // several): the first unless a row of another key was chosen.
  const [selectedKey, setSelectedKey] = useState("");
  // The setting area (a top-level node of the tree) the tree below shows.
  const [area, setArea] = useState("");
  // The groups the person closed while a search shows everything open.
  const [closedInSearch, setClosedInSearch] = useState<ReadonlySet<string>>(
    new Set(),
  );
  const [step, setStep] = useState<Step>({ kind: "list" });
  const [keys, setKeys] = useState("");
  const [check, setCheck] = useState<IntegrationCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const checks = useRef(0);
  const search = useRef<HTMLInputElement>(null);
  const rowNodes = useRef(new Map<string, HTMLElement>());
  // Counts the openings and closings: an answer that comes after the dialog
  // went is dropped, so it never touches the binding.
  const lifetime = useRef(0);

  // A dialog that is opened again starts at the list.
  useEffect(() => {
    lifetime.current++;
    if (!open) return;
    setQuery("");
    setExpanded(new Set());
    setFocusKey("");
    setSelected("");
    setSelectedKey("");
    setArea("");
    setClosedInSearch(new Set());
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
          setLoadError(integrationFailure(error, info.name, language, "read"));
      });
    return () => {
      current = false;
    };
  }, [open, host, info.id, info.name, language, attempt]);

  const byId = useMemo(
    () => functionsById(functions?.functions ?? []),
    [functions],
  );
  const searching = query.trim() !== "";
  const areas = useMemo(() => areasOf(functions?.tree ?? []), [functions]);
  const areaId = areas.find((entry) => entry.id === area)?.id ?? areas[0]?.id;
  // What the tree shows: the open levels of the chosen area, or, while
  // searching, every area cut down to the functions found (all open).
  const shownTree = useMemo<TreeNode[]>(() => {
    const tree = functions?.tree ?? [];
    if (searching) {
      const found = new Set(
        filterFunctions(functions?.functions ?? [], query).map((fn) => fn.id),
      );
      return pruneTree(
        tree,
        (node) => node.function !== undefined && found.has(node.function),
      );
    }
    if (areas.length === 0) return tree;
    return areas.find((entry) => entry.id === areaId)?.children ?? [];
  }, [functions, query, searching, areas, areaId]);
  const openIds = useMemo(() => {
    if (!searching) return expanded;
    const all = groupIds(shownTree);
    for (const id of closedInSearch) all.delete(id);
    return all;
  }, [searching, expanded, shownTree, closedInSearch]);
  const allEntries = useMemo<Entry[]>(
    () =>
      visibleRows(shownTree, openIds).flatMap((row) => entriesOf(row, byId)),
    [shownTree, openIds, byId],
  );
  const cut = searching && allEntries.length > PICKER_ROWS;
  const entries = useMemo(
    () => (cut ? allEntries.slice(0, PICKER_ROWS) : allEntries),
    [cut, allEntries],
  );
  const chosen = byId.get(selected);
  const chosenKey = chosen
    ? chosen.keys.includes(selectedKey)
      ? selectedKey
      : firstKey(chosen)
    : "";
  const tabStop =
    entries.find((entry) => entry.key === focusKey)?.key ??
    entries.find((entry) => entry.fn?.id === selected)?.key ??
    entries[0]?.key ??
    "";

  const toggle = (key: string, on: boolean) => {
    const update = (now: ReadonlySet<string>, open: boolean) => {
      const next = new Set(now);
      if (open) next.add(key);
      else next.delete(key);
      return next;
    };
    // A search has every group open; closing one is remembered until the
    // search changes.
    if (searching) setClosedInSearch((now) => update(now, !on));
    else setExpanded((now) => update(now, on));
  };
  const focusRow = (key: string | undefined) => {
    if (key === undefined) return;
    setFocusKey(key);
    rowNodes.current.get(key)?.focus();
  };
  const choose = (entry: Entry) => {
    setFocusKey(entry.key);
    if (entry.fn) {
      setSelected(entry.fn.id);
      setSelectedKey(
        entry.keyIndex === undefined
          ? ""
          : (entry.fn.keys[entry.keyIndex] ?? ""),
      );
      setProblem("");
    } else if (entry.expandable) toggle(entry.key, !entry.expanded);
  };
  const onTreeKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const key = (event.target as HTMLElement)
      .closest<HTMLElement>("[data-key]")
      ?.getAttribute("data-key");
    const at = entries.findIndex((entry) => entry.key === key);
    if (at < 0 || event.altKey || event.ctrlKey || event.metaKey) return;
    const entry = entries[at];
    let handled = true;
    switch (event.key) {
      case "ArrowDown":
        focusRow(entries[at + 1]?.key);
        break;
      case "ArrowUp":
        focusRow(entries[at - 1]?.key);
        break;
      case "Home":
        focusRow(entries[0]?.key);
        break;
      case "End":
        focusRow(entries[entries.length - 1]?.key);
        break;
      case "ArrowRight":
        if (entry.expandable && !entry.expanded) toggle(entry.key, true);
        else if (entry.expanded) focusRow(entries[at + 1]?.key);
        break;
      case "ArrowLeft":
        if (entry.expanded) toggle(entry.key, false);
        else focusRow(entry.parent);
        break;
      case "Enter":
      case " ":
        choose(entry);
        break;
      default:
        handled = false;
    }
    if (handled) event.preventDefault();
  };

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
          setProblem(integrationFailure(error, info.name, language, "check"));
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
    const mine = lifetime.current;
    setBusy(true);
    setProblem("");
    try {
      const picked = chosenKey;
      const previousName = await putPicked(chosen, picked);
      if (mine !== lifetime.current) return;
      // Everything worked: now the binding changes, in one edit.
      onPicked({
        keys: picked,
        fn: chosen,
        ...(previousName === undefined ? {} : { previousName }),
      });
      onClose();
    } catch (error) {
      if (mine === lifetime.current)
        setProblem(integrationFailure(error, info.name, language, "items"));
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  const writeIn = async (fn: IntegrationFunction) => {
    if (!host || !keys || busy) return;
    const mine = lifetime.current;
    setBusy(true);
    setProblem("");
    try {
      const outcome = await host.write(info, [{ function: fn.id, add: keys }]);
      if (mine !== lifetime.current) return;
      if (!outcome.ok) {
        if (!outcome.cancelled) setProblem(outcome.message);
        return;
      }
      let previousName: string | undefined;
      try {
        previousName = await putPicked(fn, keys);
      } catch (error) {
        // The binding stays as it was; trying again repeats both steps, and
        // writing the same key again changes nothing in the app.
        if (mine === lifetime.current)
          setProblem(integrationFailure(error, info.name, language, "items"));
        return;
      }
      if (mine !== lifetime.current) return;
      onPicked({
        keys,
        fn,
        ...(previousName === undefined ? {} : { previousName }),
      });
      setProblem("");
      setStep({ kind: "done", fn, keys, written: outcome.written });
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  const undo = async (backup: IntegrationBackup) => {
    if (!host || busy) return;
    const mine = lifetime.current;
    setBusy(true);
    setProblem("");
    try {
      if (!(await host.requireClosed(info))) return;
      await host.store
        .getState()
        .api.restoreIntegrationBackup(info.id, backup.id);
      if (mine !== lifetime.current) return;
      setStep({ kind: "list" });
      onClose();
    } catch (error) {
      if (mine === lifetime.current)
        setProblem(integrationFailure(error, info.name, language, "restore"));
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  const title = t(`从 ${info.name} 选择`, `Pick from ${info.name}`);
  let footer: React.ReactNode;
  let body: React.ReactNode;
  if (step.kind === "list") {
    footer = (
      <>
        <span className="spacer" />
        <button
          type="button"
          className="btn-ghost"
          disabled={busy}
          onClick={onClose}
        >
          {t("取消", "Cancel")}
        </button>
        <button
          type="button"
          className="btn-primary"
          disabled={!chosen || busy}
          aria-busy={busy}
          onClick={() => void pickChosen()}
        >
          {busy ? t("正在设置…", "Setting…") : t("设置", "Set")}
        </button>
      </>
    );
    body = (
      <div className="picker" data-testid="integration-picker">
        {areas.length > 1 && !loadError && (
          <label className="picker-area">
            <span>{t("设置区域", "Setting area")}</span>
            <select
              className="select"
              value={areaId}
              disabled={busy || searching}
              onChange={(event) => {
                setArea(event.target.value);
                setFocusKey("");
              }}
            >
              {areas.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <input
          ref={search}
          type="search"
          className="input picker-search"
          aria-label={t("搜索功能", "Search functions")}
          placeholder={t("名称、路径或快捷键", "Name, path or shortcut")}
          value={query}
          disabled={busy}
          onChange={(event) => {
            setQuery(event.target.value);
            setClosedInSearch(new Set());
          }}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown" && entries.length > 0) {
              event.preventDefault();
              focusRow(tabStop);
            }
          }}
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
        ) : entries.length === 0 ? (
          <p className="picker-empty">
            {t("没有符合的功能。", "No function matches.")}
          </p>
        ) : (
          <div
            className="picker-tree"
            role="tree"
            aria-label={
              searching
                ? t("搜索结果", "Search results")
                : t("功能", "Functions")
            }
            onKeyDown={onTreeKey}
          >
            {entries.map((entry) => {
              const fn = entry.fn;
              const note =
                fn && entry.keyIndex === undefined
                  ? cycleNote(fn, byId, info.name, language)
                  : null;
              const key = fn ? (fn.keys[entry.keyIndex ?? 0] ?? "") : "";
              const here = fn !== undefined && selected === fn.id;
              return (
                // biome-ignore lint/a11y/useKeyWithClickEvents: the tree's keydown handler is the keyboard's way in
                <div
                  key={entry.key}
                  ref={(node) => {
                    if (node) rowNodes.current.set(entry.key, node);
                    else rowNodes.current.delete(entry.key);
                  }}
                  role="treeitem"
                  tabIndex={entry.key === tabStop ? 0 : -1}
                  data-key={entry.key}
                  data-testid="picker-row"
                  data-on={here || undefined}
                  data-group={entry.expandable || undefined}
                  aria-level={entry.depth}
                  aria-posinset={
                    entry.keyIndex === undefined ? entry.position : undefined
                  }
                  aria-setsize={
                    entry.keyIndex === undefined ? entry.siblings : undefined
                  }
                  aria-label={
                    entry.keyIndex === undefined || !fn
                      ? undefined
                      : `${fn.name} ${key}`
                  }
                  aria-expanded={entry.expandable ? entry.expanded : undefined}
                  aria-selected={fn ? here && chosenKey === key : undefined}
                  className="picker-row"
                  style={{ paddingLeft: 8 + (entry.depth - 1) * 16 }}
                  onFocus={() => setFocusKey(entry.key)}
                  onClick={() => choose(entry)}
                >
                  <span
                    className="picker-row-chevron"
                    data-open={entry.expanded}
                    aria-hidden="true"
                    onClick={(event) => {
                      if (!entry.expandable) return;
                      event.stopPropagation();
                      setFocusKey(entry.key);
                      toggle(entry.key, !entry.expanded);
                    }}
                  >
                    {entry.expandable && (
                      <Icon name="chevronRight" size={14} stroke={2} />
                    )}
                  </span>
                  <span className="picker-row-text">
                    <span className="picker-row-name">{entry.name}</span>
                    {note && <span className="picker-row-note">{note}</span>}
                  </span>
                  <span className="picker-row-keys">
                    {key && <Keycaps keys={key} language={language} />}
                  </span>
                </div>
              );
            })}
            {cut && (
              <p className="picker-empty" role="status">
                {t(
                  `只显示前 ${PICKER_ROWS} 行，请缩小搜索。`,
                  `Showing the first ${PICKER_ROWS} rows. Narrow the search.`,
                )}
              </p>
            )}
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
        <button
          type="button"
          className="btn-ghost"
          disabled={busy}
          onClick={onClose}
        >
          {t("取消", "Cancel")}
        </button>
        <button
          type="button"
          className="btn-primary"
          disabled={!keys || !check || !writable(check) || busy || checking}
          aria-busy={busy}
          onClick={() => void writeIn(fn)}
        >
          {busy
            ? t("正在写入…", "Writing…")
            : t(`写入 ${info.name}`, `Write to ${info.name}`)}
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
      closeDisabled={busy}
      onClose={onClose}
      className="picker-dialog"
      footer={footer}
    >
      {body}
    </Dialog>
  );
}
