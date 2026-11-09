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
  IntegrationItem,
  IntegrationWritten,
  TreeNode,
} from "../../shared/integrations";
import type { StepEffects } from "../editor/editor-history";
import type { EditedStep } from "../editor/editor-store";
import {
  applyItemChange,
  areasOf,
  checkText,
  cycleNote,
  filterFunctions,
  firstKey,
  functionsById,
  groupIds,
  type ItemChange,
  itemChange,
  itemFor,
  itemOf,
  pathText,
  pruneTree,
  revertItemChange,
  type TreeRow,
  visibleRows,
  writable,
} from "../state/integrations-model";
import type { Language, PadmateStore } from "../state/store";
import { Dialog } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { Keycaps } from "../ui/Keycaps";
import {
  type FailureStep,
  integrationFailure,
  useIntegrationHost,
} from "./IntegrationContext";
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

/** What a pick did, so it can be taken back whole. */
interface PickRecord {
  /** What it did to the profile's item file. */
  change: ItemChange;
  /** Its edit of the binding; null when the binding did not change. */
  step: EditedStep | null;
}

type Step =
  | { kind: "list" }
  | { kind: "record"; fn: IntegrationFunction }
  // The app is written, the profile's item file is not: the way out is to
  // save again or to take the write back.
  | {
      kind: "unsaved";
      fn: IntegrationFunction;
      keys: string;
      written: IntegrationWritten;
    }
  | {
      kind: "done";
      fn: IntegrationFunction;
      keys: string;
      written: IntegrationWritten;
      pick: PickRecord;
    };

// The changes to one profile's item file wait for each other, so a quick
// undo and redo of picks (Ctrl+Z, Ctrl+Y) land in the order they were made.
const itemQueue = new Map<string, Promise<void>>();

/** Reads the item file fresh, changes it and writes it, after the earlier ones. */
function changeItems(
  store: PadmateStore,
  profile: string,
  integration: string,
  transform: (items: IntegrationItem[]) => IntegrationItem[],
): Promise<void> {
  const key = `${profile}|${integration}`;
  const run = async () => {
    const state = store.getState();
    const items = await state.loadIntegrationItems(profile, integration);
    await state.api.putIntegrationItems({
      profile,
      integration,
      revision: items.revision,
      items: transform(items.items),
    });
    void state.loadIntegrationItems(profile, integration).catch(() => {});
  };
  const next = (itemQueue.get(key) ?? Promise.resolve())
    .catch(() => {})
    .then(run);
  itemQueue.set(key, next);
  return next;
}

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
  /**
   * Puts the keys into the binding, through the editor's normal edit; the
   * effects follow that edit's undo and redo. Returns the edit, null when
   * the binding did not change.
   */
  onPicked: (picked: Picked, effects: StepEffects) => EditedStep | null;
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
  // What of the undo of a write worked, so a second try goes on from there.
  const unwound = useRef({ app: false, binding: false });
  // The language when the effects of an undo of the pick report a failure.
  const languageNow = useRef(language);
  languageNow.current = language;
  // The profile (and its keys) the drawer has now, and the ones the pick
  // started with. A profile that is switched while the dialog is open (by the
  // tray or a controller) must not receive this pick: everything below writes
  // to, and undoes on, the pinned profile only.
  const profileNow = useRef(profile);
  profileNow.current = profile;
  const keysNow = useRef(currentKeys);
  keysNow.current = currentKeys;
  const pinned = useRef({ profile, keys: currentKeys });
  /** Whether the drawer has moved to another profile since the pick began. */
  const moved = () => profileNow.current !== pinned.current.profile;

  // A dialog that is opened again starts at the list.
  useEffect(() => {
    lifetime.current++;
    if (!open) return;
    pinned.current = { profile: profileNow.current, keys: keysNow.current };
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
    unwound.current = { app: false, binding: false };
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
  // A selection that is not in the rows now (a search, another area or a
  // closed group took it out) is none: 设置 would write what no one sees.
  const chosen = entries.some((entry) => entry.fn?.id === selected)
    ? byId.get(selected)
    : undefined;
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
    const { profile: target, keys: before } = pinned.current;
    const items = await state.loadIntegrationItems(target, info.id);
    const previousName = itemFor(items.items, before)?.name;
    const change = itemChange(
      items.items,
      itemOf(fn, picked),
      before && before !== picked ? before : undefined,
    );
    await state.api.putIntegrationItems({
      profile: target,
      integration: info.id,
      revision: items.revision,
      items: applyItemChange(items.items, change),
    });
    void state.loadIntegrationItems(target, info.id).catch(() => {});
    return { previousName, change };
  };
  type Saved = NonNullable<Awaited<ReturnType<typeof putPicked>>>;

  /**
   * Puts the pick into the binding, in one edit. A plain undo of that edit
   * (Ctrl+Z) moves the profile's item file back with it, and redo forward
   * again; the app's own settings stay as they are (a key that was added
   * there is not taken away).
   */
  const bind = (
    fn: IntegrationFunction,
    picked: string,
    saved: Saved,
  ): PickRecord => {
    const store = host?.store;
    const target = pinned.current.profile;
    const follow =
      (transform: (items: IntegrationItem[]) => IntegrationItem[]) => () => {
        if (!store) return;
        changeItems(store, target, info.id, transform).catch(() => {
          store.setState({
            error: integrationFailure(
              null,
              info.name,
              languageNow.current,
              "itemsFollow",
            ),
          });
        });
      };
    const step = onPicked(
      {
        keys: picked,
        fn,
        ...(saved.previousName === undefined
          ? {}
          : { previousName: saved.previousName }),
      },
      {
        undo: follow((items) => revertItemChange(items, saved.change)),
        redo: follow((items) => applyItemChange(items, saved.change)),
      },
    );
    return { change: saved.change, step };
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
    if (moved()) {
      setProblem(
        integrationFailure(null, info.name, language, "profileChanged"),
      );
      return;
    }
    const mine = lifetime.current;
    setBusy(true);
    setProblem("");
    try {
      const picked = chosenKey;
      const saved = await putPicked(chosen, picked);
      if (mine !== lifetime.current || !saved) return;
      if (moved()) {
        // The profile changed while the item was saved: put it back.
        await takeBack(saved);
        if (mine === lifetime.current)
          setProblem(
            integrationFailure(null, info.name, language, "profileChanged"),
          );
        return;
      }
      // Everything worked: now the binding changes, in one edit.
      bind(chosen, picked, saved);
      onClose();
    } catch (error) {
      if (mine === lifetime.current)
        setProblem(integrationFailure(error, info.name, language, "items"));
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  /** Takes a saved item back out of the pinned profile's file (best effort). */
  const takeBack = async (saved: Saved) => {
    if (!host) return;
    await changeItems(host.store, pinned.current.profile, info.id, (items) =>
      revertItemChange(items, saved.change),
    ).catch(() => {});
  };

  /**
   * Saves the item and puts the pick into the binding, once the key is
   * written in the app. When the app was changed and the save fails, the
   * dialog stays on the two ways out (the "unsaved" step); when the write
   * changed nothing in the app, a failed save leaves nothing behind and the
   * record step simply stays.
   */
  const finish = async (
    fn: IntegrationFunction,
    written: IntegrationWritten,
    mine: number,
  ) => {
    // The app is changed when the write made a backup: then the dialog stays
    // on the two ways out; when it changed nothing, the record step stays.
    const stay = (message: string) => {
      setProblem(message);
      if (written.backup) setStep({ kind: "unsaved", fn, keys, written });
    };
    const elsewhere = integrationFailure(
      null,
      info.name,
      language,
      "profileChanged",
    );
    if (moved()) {
      stay(elsewhere);
      return;
    }
    let saved: Saved | undefined;
    try {
      saved = await putPicked(fn, keys);
    } catch (error) {
      if (mine !== lifetime.current) return;
      stay(
        integrationFailure(
          error,
          info.name,
          language,
          written.backup ? "saved" : "items",
        ),
      );
      return;
    }
    if (mine !== lifetime.current || !saved) return;
    if (moved()) {
      await takeBack(saved);
      if (mine === lifetime.current) stay(elsewhere);
      return;
    }
    const pick = bind(fn, keys, saved);
    unwound.current = { app: false, binding: false };
    setProblem("");
    setStep({ kind: "done", fn, keys, written, pick });
  };

  const writeIn = async (fn: IntegrationFunction) => {
    if (!host || !keys || busy) return;
    if (moved()) {
      setProblem(
        integrationFailure(null, info.name, language, "profileChanged"),
      );
      return;
    }
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
      await finish(fn, outcome.written, mine);
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  /** 重试保存: the app is written already, so only the save and the binding repeat. */
  const retrySave = async (
    fn: IntegrationFunction,
    written: IntegrationWritten,
  ) => {
    if (busy) return;
    const mine = lifetime.current;
    setBusy(true);
    setProblem("");
    try {
      await finish(fn, written, mine);
    } finally {
      if (mine === lifetime.current) setBusy(false);
    }
  };

  /**
   * 撤销这次写入. The app first (it can refuse: it may be running), then the
   * binding, then the item file; the binding goes back as the step it was,
   * so no undo of it is left. A part that worked is not done again when the
   * person tries once more after a failure; a failure of the app leaves
   * everything else as it was. Without a pick (the item file was never
   * written) only the app goes back.
   */
  const undo = async (backup: IntegrationBackup, pick: PickRecord | null) => {
    if (!host || busy) return;
    const mine = lifetime.current;
    const progress = unwound.current;
    setBusy(true);
    setProblem("");
    const fail = (failed: FailureStep, error?: unknown) => {
      if (mine === lifetime.current)
        setProblem(integrationFailure(error, info.name, language, failed));
    };
    try {
      // The pick belongs to the profile it began in: from another profile
      // neither its binding nor its item file can be taken back.
      if (pick && moved()) {
        fail("profileChangedUndo");
        return;
      }
      if (!progress.binding && pick?.step && !pick.step.canRevert()) {
        fail("late");
        return;
      }
      if (!progress.app) {
        try {
          if (!(await host.requireClosed(info))) return;
          await host.store
            .getState()
            .api.restoreIntegrationBackup(info.id, backup.id);
        } catch (error) {
          fail("restore", error);
          return;
        }
        progress.app = true;
      }
      if (!progress.binding) {
        if (pick?.step && !pick.step.revert()) {
          fail("bindingBack");
          return;
        }
        progress.binding = true;
      }
      if (pick) {
        try {
          await changeItems(
            host.store,
            pinned.current.profile,
            info.id,
            (items) => revertItemChange(items, pick.change),
          );
        } catch (error) {
          fail("itemsBack", error);
          return;
        }
      }
      if (mine !== lifetime.current) return;
      setStep({ kind: "list" });
      onClose();
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
  } else if (step.kind === "unsaved") {
    const unsaved = step;
    const made = unsaved.written.backup;
    // The app is changed and the profile is not: the dialog cannot be left
    // except through one of these two, so the app never stays changed unseen.
    footer = (
      <>
        {made && (
          <button
            type="button"
            className="btn-ghost"
            disabled={busy}
            onClick={() => void undo(made, null)}
          >
            {t("撤销这次写入", "Undo this write")}
          </button>
        )}
        <span className="spacer" />
        <button
          type="button"
          className="btn-primary"
          disabled={busy}
          aria-busy={busy}
          onClick={() => void retrySave(unsaved.fn, unsaved.written)}
        >
          {busy ? t("正在保存…", "Saving…") : t("重试保存", "Retry saving")}
        </button>
      </>
    );
    body = (
      <div data-testid="integration-unsaved">
        <p className="dialog-text">
          {t(
            `已把 ${unsaved.keys} 写进 ${info.name}，设为“${unsaved.fn.name}”，但没能记进这份配置。`,
            `Wrote ${unsaved.keys} into ${info.name} as "${unsaved.fn.name}", but it could not be saved into the profile.`,
          )}
        </p>
        <p className="dialog-text">
          {t(
            "快捷键还没有改。可以重试保存，或撤销这次写入，撤销后哪里都没有改动。",
            "The binding is not changed. Retry saving, or undo the write; after undoing, nothing is changed anywhere.",
          )}
        </p>
        <p className="dialog-text">
          {t(
            `如果这个窗口被关掉了，这次写入还可以在“设置”里 ${info.name} 的备份中恢复。`,
            `If this window is closed, the write can still be restored from the ${info.name} backups in Settings.`,
          )}
        </p>
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
            onClick={() => void undo(made, done.pick)}
          >
            {t("撤销这次写入", "Undo this write")}
          </button>
        )}
        <span className="spacer" />
        <button
          type="button"
          className="btn-primary"
          disabled={busy}
          onClick={onClose}
        >
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
      closeDisabled={busy || step.kind === "unsaved"}
      onClose={onClose}
      className="picker-dialog"
      footer={footer}
    >
      {body}
    </Dialog>
  );
}
