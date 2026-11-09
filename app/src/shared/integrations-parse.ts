// The answers of the daemon's integrations.* methods and the data of the
// integration.changed topic (ARCHITECTURE §16.4), checked field by field. A
// reply of an unexpected shape is refused in one place, so the window only
// sees the types of integrations.ts.

import type {
  IntegrationBackup,
  IntegrationChange,
  IntegrationChanged,
  IntegrationCheck,
  IntegrationCheckStatus,
  IntegrationDisplaced,
  IntegrationFunction,
  IntegrationFunctions,
  IntegrationInfo,
  IntegrationItem,
  IntegrationItemState,
  IntegrationItemStatus,
  IntegrationItems,
  IntegrationNamesProblem,
  IntegrationNote,
  IntegrationOwner,
  IntegrationPlan,
  IntegrationPlanEntry,
  IntegrationProblem,
  IntegrationWritten,
  TreeNode,
} from "./integrations";

const MAX_INTEGRATIONS = 16;
const MAX_PROFILES = 1024;
/** The daemon's limit of a profile's items is 512 (§16.2). */
const MAX_ITEMS = 512;
const MAX_FUNCTIONS = 20000;
/** The deepest menu level and the most nodes a tree may have. */
const MAX_TREE_DEPTH = 16;
const MAX_TREE_NODES = 40000;
const MAX_PATH = 16;
const MAX_KEYS = 16;
const MAX_BACKUPS = 64;
const MAX_TEXT = 4096;

export const INTEGRATION_ID = /^[a-z][a-z0-9]{0,31}$/;

const PROBLEMS: readonly string[] = [
  "notFound",
  "unknownFormat",
  "unsupported",
];
const NAMES_PROBLEMS: readonly string[] = [
  "installNotFound",
  "resourceUnreadable",
];
const CHECK_STATUSES: readonly string[] = [
  "free",
  "same",
  "cycle",
  "conflict",
  "unconfirmed",
  "unsupported",
];
const NOTES: readonly string[] = ["toolShift", "modifierKey"];
const STATES: readonly string[] = [
  "consistent",
  "changed",
  "removed",
  "missing",
];
const KINDS: readonly string[] = ["files", "running", "differences"];

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function text(value: unknown, max = MAX_TEXT): value is string {
  return typeof value === "string" && value.length <= max;
}

function strings(value: unknown, max: number): string[] | null {
  if (!Array.isArray(value) || value.length > max) return null;
  if (value.some((entry) => !text(entry))) return null;
  return [...(value as string[])];
}

function list<T>(
  value: unknown,
  max: number,
  one: (entry: unknown) => T | null,
): T[] | null {
  if (!Array.isArray(value) || value.length > max) return null;
  const out: T[] = [];
  for (const entry of value) {
    const checked = one(entry);
    if (checked === null) return null;
    out.push(checked);
  }
  return out;
}

function info(value: unknown): IntegrationInfo | null {
  if (!isRecord(value)) return null;
  const exe = strings(value.exe, 8);
  const profiles = strings(value.profiles ?? [], MAX_PROFILES);
  if (
    typeof value.id !== "string" ||
    !INTEGRATION_ID.test(value.id) ||
    !text(value.name, 128) ||
    !value.name ||
    !exe ||
    !profiles ||
    typeof value.found !== "boolean" ||
    typeof value.running !== "boolean" ||
    (value.problem !== undefined &&
      !(
        typeof value.problem === "string" && PROBLEMS.includes(value.problem)
      )) ||
    (value.namesProblem !== undefined &&
      !(
        typeof value.namesProblem === "string" &&
        NAMES_PROBLEMS.includes(value.namesProblem)
      )) ||
    (value.dataDir !== undefined && !text(value.dataDir, 1024)) ||
    (value.installDir !== undefined && !text(value.installDir, 1024)) ||
    (value.custom !== undefined && typeof value.custom !== "boolean")
  )
    return null;
  return {
    id: value.id,
    name: value.name,
    exe,
    found: value.found,
    ...(value.problem === undefined
      ? {}
      : { problem: value.problem as IntegrationProblem }),
    ...(value.namesProblem === undefined
      ? {}
      : { namesProblem: value.namesProblem as IntegrationNamesProblem }),
    ...(value.dataDir === undefined
      ? {}
      : { dataDir: value.dataDir as string }),
    ...(value.installDir === undefined
      ? {}
      : { installDir: value.installDir as string }),
    ...(value.custom === true ? { custom: true } : {}),
    running: value.running,
    profiles,
  };
}

/** The integrations.list answer in value, or null when it is not one. */
export function parseIntegrationList(value: unknown): IntegrationInfo[] | null {
  return list(value, MAX_INTEGRATIONS, info);
}

/**
 * The menu tree: ordered nodes with an ID and a name, a function ID on a leaf
 * (or on a group that is also a tool) and children on a group. A node with
 * neither is not one. `budget` counts the nodes left.
 */
function treeNodes(
  value: unknown,
  depth: number,
  budget: { left: number },
): TreeNode[] | null {
  if (!Array.isArray(value) || depth > MAX_TREE_DEPTH) return null;
  const out: TreeNode[] = [];
  for (const entry of value) {
    if (--budget.left < 0 || !isRecord(entry)) return null;
    if (!text(entry.id, 256) || !entry.id || !text(entry.name, 512))
      return null;
    if (
      entry.function !== undefined &&
      (!text(entry.function, 256) || !entry.function)
    )
      return null;
    let children: TreeNode[] | undefined;
    if (entry.children !== undefined) {
      const inner = treeNodes(entry.children, depth + 1, budget);
      if (!inner) return null;
      if (inner.length > 0) children = inner;
    }
    if (entry.function === undefined && !children) return null;
    out.push({
      id: entry.id,
      name: entry.name,
      ...(entry.function === undefined
        ? {}
        : { function: entry.function as string }),
      ...(children ? { children } : {}),
    });
  }
  return out;
}

function fn(value: unknown): IntegrationFunction | null {
  if (!isRecord(value)) return null;
  const path = strings(value.path ?? [], MAX_PATH);
  const keys = strings(value.keys ?? [], MAX_KEYS);
  const cycle =
    value.cycle === undefined ? undefined : strings(value.cycle, 64);
  if (
    !text(value.id, 256) ||
    !value.id ||
    !text(value.kind, 32) ||
    !text(value.name, 512) ||
    !path ||
    !keys ||
    cycle === null ||
    (value.unconfirmed !== undefined &&
      typeof value.unconfirmed !== "boolean") ||
    (value.unsupported !== undefined && typeof value.unsupported !== "boolean")
  )
    return null;
  return {
    id: value.id,
    kind: value.kind,
    name: value.name,
    path,
    keys,
    ...(value.unconfirmed === true ? { unconfirmed: true } : {}),
    ...(value.unsupported === true ? { unsupported: true } : {}),
    ...(cycle?.length ? { cycle } : {}),
  };
}

/** The integrations.functions answer in value, or null when it is not one. */
export function parseIntegrationFunctions(
  value: unknown,
): IntegrationFunctions | null {
  if (!isRecord(value) || !text(value.revision, 256)) return null;
  const tree = treeNodes(value.tree, 1, { left: MAX_TREE_NODES });
  const functions = list(value.functions, MAX_FUNCTIONS, fn);
  if (!tree || !functions) return null;
  return { revision: value.revision, tree, functions };
}

function owner(value: unknown): IntegrationOwner | null {
  if (!isRecord(value) || !text(value.id, 256) || !text(value.name, 512))
    return null;
  const path = strings(value.path ?? [], MAX_PATH);
  if (!path) return null;
  return { id: value.id, name: value.name, path };
}

/** The integrations.check answer in value, or null when it is not one. */
export function parseIntegrationCheck(value: unknown): IntegrationCheck | null {
  if (
    !isRecord(value) ||
    typeof value.status !== "string" ||
    !CHECK_STATUSES.includes(value.status)
  )
    return null;
  const owners = list(value.owners ?? [], 64, owner);
  const notes = strings(value.notes ?? [], 8);
  if (!owners || !notes || notes.some((note) => !NOTES.includes(note)))
    return null;
  return {
    status: value.status as IntegrationCheckStatus,
    owners,
    notes: notes as IntegrationNote[],
  };
}

function item(value: unknown): IntegrationItem | null {
  if (!isRecord(value)) return null;
  const path = strings(value.path ?? [], MAX_PATH);
  if (
    !text(value.keys, 256) ||
    !value.keys ||
    !text(value.function, 256) ||
    !value.function ||
    !text(value.name, 512) ||
    !path
  )
    return null;
  return {
    keys: value.keys,
    function: value.function,
    name: value.name,
    path,
  };
}

function status(value: unknown): IntegrationItemStatus | null {
  if (
    !isRecord(value) ||
    !text(value.keys, 256) ||
    !text(value.function, 256) ||
    typeof value.state !== "string" ||
    !STATES.includes(value.state)
  )
    return null;
  const current =
    value.current === undefined ? undefined : strings(value.current, MAX_KEYS);
  const owners =
    value.owners === undefined ? undefined : list(value.owners, 64, owner);
  if (current === null || owners === null) return null;
  return {
    keys: value.keys,
    function: value.function,
    state: value.state as IntegrationItemState,
    ...(current ? { current } : {}),
    ...(owners ? { owners } : {}),
  };
}

/** The integrations.items answer in value, or null when it is not one. */
export function parseIntegrationItems(value: unknown): IntegrationItems | null {
  if (
    !isRecord(value) ||
    !text(value.revision, 256) ||
    typeof value.consistent !== "boolean" ||
    typeof value.dismissed !== "boolean" ||
    (value.differenceId !== undefined && !text(value.differenceId, 256))
  )
    return null;
  const items = list(value.items, MAX_ITEMS, item);
  const statuses = list(value.statuses, MAX_ITEMS, status);
  if (!items || !statuses) return null;
  return {
    revision: value.revision,
    items,
    statuses,
    consistent: value.consistent,
    dismissed: value.dismissed,
    ...(value.differenceId === undefined
      ? {}
      : { differenceId: value.differenceId as string }),
  };
}

function change(value: unknown): IntegrationChange | null {
  if (
    !isRecord(value) ||
    !text(value.function, 256) ||
    !value.function ||
    (value.add !== undefined && !text(value.add, 256)) ||
    (value.remove !== undefined && !text(value.remove, 256))
  )
    return null;
  return {
    function: value.function,
    ...(value.add === undefined ? {} : { add: value.add as string }),
    ...(value.remove === undefined ? {} : { remove: value.remove as string }),
  };
}

function displaced(value: unknown): IntegrationDisplaced | null {
  if (
    !isRecord(value) ||
    !text(value.function, 256) ||
    !text(value.name, 512) ||
    !text(value.keys, 256)
  )
    return null;
  return { function: value.function, name: value.name, keys: value.keys };
}

function entry(value: unknown): IntegrationPlanEntry | null {
  if (!isRecord(value)) return null;
  const one = item(value.item);
  const changes = list(value.changes ?? [], 64, change);
  const moved = list(value.displaced ?? [], 64, displaced);
  if (!one || !changes || !moved) return null;
  // `skipped` is the reason, a string.
  if (value.skipped !== undefined && !text(value.skipped)) return null;
  const skipped = value.skipped;
  return {
    item: one,
    changes,
    displaced: moved,
    ...(skipped === undefined ? {} : { skipped }),
  };
}

/** The integrations.plan answer in value, or null when it is not one. */
export function parseIntegrationPlan(value: unknown): IntegrationPlan | null {
  if (!isRecord(value)) return null;
  const entries = list(value.entries, MAX_ITEMS, entry);
  return entries ? { entries } : null;
}

function backup(value: unknown): IntegrationBackup | null {
  if (
    !isRecord(value) ||
    !text(value.id, 128) ||
    !value.id ||
    !text(value.created, 64) ||
    (value.reason !== "write" && value.reason !== "beforeRestore") ||
    typeof value.changes !== "number" ||
    !Number.isInteger(value.changes) ||
    value.changes < 0
  )
    return null;
  return {
    id: value.id,
    created: value.created,
    reason: value.reason,
    changes: value.changes,
  };
}

/** The integrations.write answer in value, or null when it is not one. */
export function parseIntegrationWritten(
  value: unknown,
): IntegrationWritten | null {
  if (!isRecord(value) || !text(value.revision, 256)) return null;
  // null: the write changed nothing, so no file was written and no backup made.
  if (value.backup === null) return { backup: null, revision: value.revision };
  const made = backup(value.backup);
  return made ? { backup: made, revision: value.revision } : null;
}

/** The integrations.backups answer, newest first, or null. */
export function parseIntegrationBackups(
  value: unknown,
): IntegrationBackup[] | null {
  return list(value, MAX_BACKUPS, backup);
}

/** The integrations.restore answer: the backup it made of the current files. */
export function parseIntegrationRestored(
  value: unknown,
): IntegrationBackup | null {
  return backup(value);
}

/** The data of an integration.changed event, or null when it is not one. */
export function parseIntegrationChanged(
  value: unknown,
): IntegrationChanged | null {
  if (
    !isRecord(value) ||
    typeof value.integration !== "string" ||
    !INTEGRATION_ID.test(value.integration) ||
    typeof value.kind !== "string" ||
    !KINDS.includes(value.kind) ||
    (value.profile !== undefined && !text(value.profile, 128)) ||
    (value.differenceId !== undefined && !text(value.differenceId, 256)) ||
    (value.count !== undefined &&
      !(
        typeof value.count === "number" &&
        Number.isInteger(value.count) &&
        value.count >= 0
      ))
  )
    return null;
  return {
    integration: value.integration,
    kind: value.kind as IntegrationChanged["kind"],
    ...(value.profile === undefined
      ? {}
      : { profile: value.profile as string }),
    ...(value.differenceId === undefined
      ? {}
      : { differenceId: value.differenceId as string }),
    ...(value.count === undefined ? {} : { count: value.count as number }),
  };
}
