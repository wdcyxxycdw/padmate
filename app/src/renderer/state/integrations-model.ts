// The logic of the app integrations UI (ARCHITECTURE §16.5) apart from React:
// filtering the function list, the rows of its menu tree, saying what a key
// check found, keeping a profile's items, listing differences and moving a
// profile's keys. Nothing
// here names an app: names, menus and notes come from the daemon.

import type {
  IntegrationChange,
  IntegrationCheck,
  IntegrationFunction,
  IntegrationItem,
  IntegrationItemStatus,
  IntegrationPlanEntry,
  TreeNode,
} from "../../shared/integrations";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The key a function is picked with: its first. */
export function firstKey(fn: IntegrationFunction): string {
  return fn.keys[0] ?? "";
}

/** A function's menu path, as the list shows it in small grey text. */
export function pathText(path: readonly string[]): string {
  return path.join(" › ");
}

/**
 * The functions that match the search (name, path or keys; case does not
 * matter, a space separates words that must all be there), in the order the
 * app lists them. No words: all of them.
 */
export function filterFunctions(
  functions: readonly IntegrationFunction[],
  query: string,
): IntegrationFunction[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...functions];
  return functions.filter((fn) => {
    const haystack =
      `${fn.name}\n${fn.path.join(" ")}\n${fn.keys.join(" ")}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}

/**
 * The setting areas of the app (a top-level node with children each, such as
 * the main menu): the picker's dropdown lists them. Empty when the tree has no
 * top-level group; the tree itself is then the one area.
 */
export function areasOf(tree: readonly TreeNode[]): TreeNode[] {
  return tree.filter((node) => (node.children?.length ?? 0) > 0);
}

/**
 * The tree cut down to what matches: a node stays when it has a function that
 * `keep` says yes to, or a descendant that stays (its children are cut the
 * same way), so a found function comes with every group above it.
 */
export function pruneTree(
  tree: readonly TreeNode[],
  keep: (node: TreeNode) => boolean,
): TreeNode[] {
  const out: TreeNode[] = [];
  for (const node of tree) {
    const children = pruneTree(node.children ?? [], keep);
    if (!keep(node) && children.length === 0) continue;
    const { children: _dropped, ...rest } = node;
    out.push(children.length > 0 ? { ...rest, children } : rest);
  }
  return out;
}

/** The IDs of the nodes that have children (the ones that can be open). */
export function groupIds(tree: readonly TreeNode[]): Set<string> {
  const ids = new Set<string>();
  const walk = (nodes: readonly TreeNode[]) => {
    for (const node of nodes)
      if (node.children?.length) {
        ids.add(node.id);
        walk(node.children);
      }
  };
  walk(tree);
  return ids;
}

/** One line of the picker's tree as drawn: a node at its depth. */
export interface TreeRow {
  /** The node's ID; unique in the tree. */
  key: string;
  node: TreeNode;
  /** 1 for the top level (aria-level). */
  depth: number;
  /** The key of the row's parent, none at the top. */
  parent?: string;
  /** The node has children: it can be opened. */
  expandable: boolean;
  expanded: boolean;
  /** Place among its siblings (aria-posinset) and their count (aria-setsize). */
  position: number;
  siblings: number;
}

/**
 * The rows the tree shows now: every node whose ancestors are all open, in the
 * order of the tree (a node's children follow it).
 */
export function visibleRows(
  tree: readonly TreeNode[],
  expanded: ReadonlySet<string>,
): TreeRow[] {
  const rows: TreeRow[] = [];
  const walk = (nodes: readonly TreeNode[], depth: number, parent?: string) => {
    nodes.forEach((node, index) => {
      const expandable = (node.children?.length ?? 0) > 0;
      const open = expandable && expanded.has(node.id);
      rows.push({
        key: node.id,
        node,
        depth,
        ...(parent === undefined ? {} : { parent }),
        expandable,
        expanded: open,
        position: index + 1,
        siblings: nodes.length,
      });
      if (open && node.children) walk(node.children, depth + 1, node.id);
    });
  };
  walk(tree, 1);
  return rows;
}

/** The functions by ID, to name the other functions of a cycle. */
export function functionsById(
  functions: readonly IntegrationFunction[],
): Map<string, IntegrationFunction> {
  return new Map(functions.map((fn) => [fn.id, fn]));
}

function nameOf(
  id: string,
  byId: ReadonlyMap<string, IntegrationFunction>,
): string {
  return byId.get(id)?.name ?? id;
}

function listNames(names: readonly string[], language: Language): string {
  return names.join(tx(language, "、", ", "));
}

/**
 * The note on a function that shares its single key with others (a cycle
 * key): pressing the key cycles through them, so one press may not reach this
 * one. null for a function that has the key alone.
 */
export function cycleNote(
  fn: IntegrationFunction,
  byId: ReadonlyMap<string, IntegrationFunction>,
  appName: string,
  language: Language,
): string | null {
  if (!fn.cycle?.length) return null;
  const others = listNames(
    fn.cycle.map((id) => nameOf(id, byId)),
    language,
  );
  return tx(
    language,
    `循环键：${firstKey(fn)} 在 ${appName} 里由 ${others} 共用，按一次不一定切到这个。`,
    `Cycle key: in ${appName}, ${firstKey(fn)} is shared with ${others}, so one press may not reach this one.`,
  );
}

export interface CheckText {
  /** What the sentence means for the user: nothing in the way, a thing to read, or a clash. */
  level: "ok" | "warn" | "block";
  text: string;
  /** The notes (holding the key, modifier keys), one sentence each. */
  notes: string[];
}

/** What integrations.check found, in plain sentences. */
export function checkText(
  check: IntegrationCheck,
  appName: string,
  language: Language,
): CheckText {
  const owners = listNames(
    check.owners.map((owner) => owner.name),
    language,
  );
  const notes = check.notes.map((note) =>
    note === "toolShift"
      ? tx(
          language,
          `在 ${appName} 里按住这个键会临时切换工具。`,
          `Holding this key in ${appName} switches tools for a moment.`,
        )
      : tx(
          language,
          `${appName} 的修饰键设置用到这个键。`,
          `The modifier-key settings of ${appName} use this key.`,
        ),
  );
  switch (check.status) {
    case "free":
      return {
        level: "ok",
        text: tx(
          language,
          `这个键在 ${appName} 里没有被占用。`,
          `This key is not used in ${appName}.`,
        ),
        notes,
      };
    case "same":
      return {
        level: "ok",
        text: tx(
          language,
          `这个键在 ${appName} 里已经是这个功能。`,
          `In ${appName} this key is already this function.`,
        ),
        notes,
      };
    case "conflict":
      return {
        level: "block",
        text: tx(
          language,
          `这个键在 ${appName} 里是 ${owners}。`,
          `In ${appName} this key is ${owners}.`,
        ),
        notes,
      };
    case "cycle":
      return {
        level: "warn",
        text: tx(
          language,
          `这个键在 ${appName} 里由 ${owners} 共用，按一次会依次切换，不一定切到这个工具。`,
          `In ${appName} this key is shared by ${owners}. Pressing it cycles through them, so one press may not reach this tool.`,
        ),
        notes,
      };
    case "unconfirmed":
      return {
        level: "warn",
        text: tx(
          language,
          `${appName} 里这个符号键对应的实体键还没有确认，先不能判断。`,
          `The physical key of this symbol is not confirmed in ${appName}, so it cannot be checked yet.`,
        ),
        notes,
      };
    case "unsupported":
      return {
        level: "block",
        text: tx(
          language,
          `${appName} 不支持把这个键设给它。`,
          `${appName} cannot take this key.`,
        ),
        notes,
      };
  }
}

/** Whether a checked key can be written: only a clash or an unsupported key cannot. */
export function writable(check: IntegrationCheck): boolean {
  return check.status !== "conflict" && check.status !== "unsupported";
}

/** The item for a picked function. */
export function itemOf(fn: IntegrationFunction, keys: string): IntegrationItem {
  return { keys, function: fn.id, name: fn.name, path: [...fn.path] };
}

/**
 * The items with `next` in them: the key is in the file once, so an item on
 * the same key is replaced, and so is the item the binding had before
 * (`replaced`, its old keys).
 */
export function putItem(
  items: readonly IntegrationItem[],
  next: IntegrationItem,
  replaced?: string,
): IntegrationItem[] {
  const kept = items.filter(
    (item) => item.keys !== next.keys && item.keys !== replaced,
  );
  return [...kept, next];
}

/** The binding's name after a function is picked: the user's own name stays. */
export function nameAfterPick(
  label: string | undefined,
  previousName: string | undefined,
  name: string,
): string {
  if (!label || label === previousName) return name;
  return label;
}

/** The item a key stands for in a profile, if it has one. */
export function itemFor(
  items: readonly IntegrationItem[] | undefined,
  keys: string,
): IntegrationItem | undefined {
  return keys ? items?.find((item) => item.keys === keys) : undefined;
}

/** One difference between a profile's items and the app. */
export interface DifferenceRow {
  keys: string;
  function: string;
  name: string;
  path: string[];
  state: Exclude<IntegrationItemStatus["state"], "consistent">;
  /** The keys the function has in the app now. */
  current: string[];
  /** The one-line account of what changed. */
  text: string;
}

/** What changed, one row per item that is not consistent. */
export function differenceRows(
  items: readonly IntegrationItem[],
  statuses: readonly IntegrationItemStatus[],
  appName: string,
  language: Language,
): DifferenceRow[] {
  const rows: DifferenceRow[] = [];
  for (const status of statuses) {
    if (status.state === "consistent") continue;
    const item = items.find(
      (entry) =>
        entry.keys === status.keys && entry.function === status.function,
    );
    const current = status.current ?? [];
    const owners = listNames(
      (status.owners ?? []).map((owner) => owner.name),
      language,
    );
    let text: string;
    if (status.state === "changed")
      text =
        current.length > 0
          ? tx(
              language,
              `在 ${appName} 里现在是 ${current.join(" / ")}`,
              `In ${appName} it is now ${current.join(" / ")}`,
            )
          : owners
            ? tx(
                language,
                `这个键在 ${appName} 里现在是 ${owners}`,
                `In ${appName} this key is now ${owners}`,
              )
            : tx(language, `在 ${appName} 里已经改了`, `Changed in ${appName}`);
    else if (status.state === "removed")
      text = tx(
        language,
        `在 ${appName} 里已没有快捷键`,
        `It has no shortcut in ${appName} now`,
      );
    else
      text = tx(
        language,
        `${appName} 里已找不到这个功能`,
        `${appName} no longer has this function`,
      );
    rows.push({
      keys: status.keys,
      function: status.function,
      name: item?.name ?? status.function,
      path: item?.path ?? [],
      state: status.state,
      current,
      text,
    });
  }
  return rows;
}

/** The key pairs "以软件为准" moves: an item whose function still has a key. */
export function keyMoves(
  rows: readonly DifferenceRow[],
): Array<{ from: string; to: string }> {
  const moves: Array<{ from: string; to: string }> = [];
  for (const row of rows)
    if (
      row.state === "changed" &&
      row.current[0] &&
      row.current[0] !== row.keys
    )
      moves.push({ from: row.keys, to: row.current[0] });
  return moves;
}

/** The items after the moves: the key of a moved item is the new one. */
export function movedItems(
  items: readonly IntegrationItem[],
  moves: ReadonlyArray<{ from: string; to: string }>,
): IntegrationItem[] {
  const to = new Map(moves.map((move) => [move.from, move.to]));
  const out: IntegrationItem[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    const keys = to.get(item.keys) ?? item.keys;
    if (seen.has(keys)) continue;
    seen.add(keys);
    out.push({ ...item, keys });
  }
  return out;
}

/**
 * The value with every shortcut that is `from` made `to`, and how many were
 * changed. A shortcut is an object with a string `keys` (a keys action or a
 * step of a sequence), wherever it is in the profile. Keys are compared as the
 * daemon writes them, exactly.
 */
export function retargetKeys<T>(
  value: T,
  moves: ReadonlyArray<{ from: string; to: string }>,
): { value: T; changed: number } {
  const to = new Map(moves.map((move) => [move.from, move.to]));
  let changed = 0;
  const walk = (node: unknown): unknown => {
    if (Array.isArray(node)) return node.map(walk);
    if (node === null || typeof node !== "object") return node;
    const out: Record<string, unknown> = {};
    for (const [key, entry] of Object.entries(node)) {
      if (key === "keys" && typeof entry === "string" && to.has(entry)) {
        out[key] = to.get(entry);
        changed++;
      } else out[key] = walk(entry);
    }
    return out;
  };
  return { value: walk(value) as T, changed };
}

/** Whether a profile sends the keys anywhere. */
export function usesKeys(value: unknown, keys: string): boolean {
  let found = false;
  const walk = (node: unknown) => {
    if (found) return;
    if (Array.isArray(node)) node.forEach(walk);
    else if (node !== null && typeof node === "object")
      for (const [key, entry] of Object.entries(node)) {
        if (key === "keys" && entry === keys) found = true;
        else walk(entry);
      }
  };
  walk(value);
  return found;
}

/** The plan entries whose writing takes a key from another local function. */
export function conflictEntries(
  entries: readonly IntegrationPlanEntry[],
): IntegrationPlanEntry[] {
  return entries.filter(
    (entry) => !entry.skipped && entry.displaced.length > 0,
  );
}

/** What the user decided for one entry that displaces functions. */
export type ConflictDecision =
  | { kind: "import" }
  | { kind: "keep" }
  /** The imported function gets this key instead. */
  | { kind: "rekeyImported"; keys: string }
  /** The displaced function (the first of the entry's) gets this key instead. */
  | { kind: "rekeyLocal"; keys: string; function: string };

function same(a: IntegrationChange, b: IntegrationChange): boolean {
  return a.function === b.function && a.add === b.add && a.remove === b.remove;
}

/**
 * What to write for the plan, given what the user decided for the entries
 * that displace functions (the others are written as they are, skipped ones
 * not at all), and the key moves the imported profile needs.
 *
 * The plan's own changes are the start: for an entry that displaces functions
 * they are the imported function's add and one remove of the key from each
 * displaced function. Each decision changes those and nothing else.
 */
export function assembleChanges(
  entries: readonly IntegrationPlanEntry[],
  decisions: ReadonlyMap<IntegrationPlanEntry, ConflictDecision>,
): {
  changes: IntegrationChange[];
  moves: Array<{ from: string; to: string }>;
} {
  const changes: IntegrationChange[] = [];
  const moves: Array<{ from: string; to: string }> = [];
  const push = (change: IntegrationChange) => {
    if (!changes.some((entry) => same(entry, change))) changes.push(change);
  };
  for (const entry of entries) {
    if (entry.skipped) continue;
    const decision = decisions.get(entry);
    if (!decision || entry.displaced.length === 0) {
      for (const change of entry.changes) push(change);
      continue;
    }
    const isLoss = (change: IntegrationChange) =>
      change.remove !== undefined &&
      entry.displaced.some(
        (lost) =>
          lost.function === change.function && lost.keys === change.remove,
      );
    // The removes the plan may lack, so a bare entry still takes the keys.
    const withLosses = [...entry.changes];
    for (const lost of entry.displaced) {
      const loss = { function: lost.function, remove: lost.keys };
      if (!withLosses.some((change) => same(change, loss)))
        withLosses.push(loss);
    }
    switch (decision.kind) {
      case "keep":
        break;
      case "import":
        for (const change of withLosses) push(change);
        break;
      case "rekeyImported":
        // The local functions keep the key: no removes. The imported one is
        // added under the new key.
        for (const change of entry.changes) {
          if (isLoss(change)) continue;
          push(
            change.function === entry.item.function && change.add !== undefined
              ? { ...change, add: decision.keys }
              : change,
          );
        }
        moves.push({ from: entry.item.keys, to: decision.keys });
        break;
      case "rekeyLocal":
        // The imported function takes the key; the chosen local function
        // loses it once and gets the new key, the others only lose it.
        for (const change of withLosses)
          push(
            isLoss(change) && change.function === decision.function
              ? { ...change, add: decision.keys }
              : change,
          );
        break;
    }
  }
  return { changes, moves };
}

/** Why a plan entry is not written, in one sentence (the daemon sends a code). */
export function skipText(
  reason: string,
  appName: string,
  language: Language,
): string {
  switch (reason) {
    case "missing":
      return tx(
        language,
        `${appName} 里已找不到这个功能。`,
        `${appName} no longer has this function.`,
      );
    case "unsupportedKey":
      return tx(
        language,
        `${appName} 不能把这个键设给这个功能。`,
        `${appName} cannot give this key to this function.`,
      );
    case "unconfirmedKey":
      return tx(
        language,
        `这个符号键对应的实体键还没有确认，先不能写入。`,
        "The physical key of this symbol is not confirmed yet, so it cannot be written.",
      );
    case "badFunction":
      return tx(
        language,
        "这个功能的记录不能识别。",
        "The record of this function cannot be recognised.",
      );
    default:
      return tx(language, "这一项不能写入。", "This item cannot be written.");
  }
}

/** The changes of the entries as they are, for "以 padmate 为准". */
export function plainChanges(
  entries: readonly IntegrationPlanEntry[],
): IntegrationChange[] {
  // Confirming the plan means the functions it lists lose their keys.
  const decisions = new Map<IntegrationPlanEntry, ConflictDecision>();
  for (const entry of conflictEntries(entries))
    decisions.set(entry, { kind: "import" });
  return assembleChanges(entries, decisions).changes;
}

/** The time of a backup as the list shows it, in the user's own time zone. */
export function backupTime(created: string, language: Language): string {
  const date = new Date(created);
  if (Number.isNaN(date.getTime())) return created;
  return date.toLocaleString(language === "en" ? "en-US" : "zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
