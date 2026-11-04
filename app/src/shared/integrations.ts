// App integrations (ARCHITECTURE §16, ADR-0019): the shapes of the daemon's
// integrations.* results and of the integration.changed topic. Types only
// (plus isBound); the renderer calls the methods through the main process,
// which checks each answer with integrations-parse.ts. Nothing here names a
// particular app: the integration ID, its name, categories and notes all come
// from the daemon.

/** Why an integration cannot be used on this computer. */
export type IntegrationProblem = "notFound" | "unknownFormat" | "unsupported";

/** One integration, as integrations.list reports it. */
export type IntegrationInfo = {
  /** `^[a-z][a-z0-9]{0,31}$`. */
  id: string;
  /** The app's name, as the window shows it. */
  name: string;
  /** The executable file names the app runs as. */
  exe: string[];
  /** The app's settings were found and understood. */
  found: boolean;
  problem?: IntegrationProblem;
  dataDir?: string;
  installDir?: string;
  /** The data folder was chosen (config.json names it), not found automatically. */
  custom?: boolean;
  /** The app is running now (its settings cannot be written). */
  running: boolean;
  /** The library IDs of the profiles bound to the app (§16.1). */
  profiles: string[];
};

/**
 * Whether a profile is bound to an integration. The ONE place the window
 * decides it, and it only reads integrations.list: the daemon says which
 * profiles are bound (today by a rule in config.json, §16.1). The window never
 * matches rules or executable names itself, so a different rule later changes
 * the daemon and this function only.
 */
export function isBound(
  integrations: readonly IntegrationInfo[] | null | undefined,
  profileId: string,
  integrationId: string,
): boolean {
  const found = integrations?.find((entry) => entry.id === integrationId);
  return !!found && found.profiles.includes(profileId);
}

/** Whether at least one profile is bound to the integration (same rule: the list says). */
export function hasBound(integration: IntegrationInfo): boolean {
  return integration.profiles.length > 0;
}

/** The integrations a profile is bound to that the app found and understood. */
export function boundIntegrations(
  integrations: readonly IntegrationInfo[] | null | undefined,
  profileId: string,
): IntegrationInfo[] {
  return (integrations ?? []).filter(
    (entry) => entry.found && isBound(integrations, profileId, entry.id),
  );
}

export type IntegrationCategory = { id: string; name: string };

export type IntegrationFunction = {
  /** Opaque to the window; the integration defines it. */
  id: string;
  kind: string;
  name: string;
  path: string[];
  category: string;
  /** KeySpec chords the app has the function on; empty: not set. */
  keys: string[];
  /** The key it is on is a symbol whose physical key is not confirmed. */
  unconfirmed?: boolean;
  unsupported?: boolean;
  /** The other functions that share the same single key (a cycle key). */
  cycle?: string[];
};

export type IntegrationFunctions = {
  /** The fingerprint of the app's files. */
  revision: string;
  categories: IntegrationCategory[];
  functions: IntegrationFunction[];
};

export type IntegrationOwner = { id: string; name: string; path: string[] };

export type IntegrationCheckStatus =
  | "free"
  | "same"
  | "cycle"
  | "conflict"
  | "unconfirmed"
  | "unsupported";

/** toolShift: holding the key switches tools for a moment; modifierKey: the
 * app's modifier-key settings use it. */
export type IntegrationNote = "toolShift" | "modifierKey";

export type IntegrationCheck = {
  status: IntegrationCheckStatus;
  owners: IntegrationOwner[];
  notes: IntegrationNote[];
};

/** One line of integrations/<id>.json: this key is this function in the app. */
export type IntegrationItem = {
  keys: string;
  function: string;
  name: string;
  path: string[];
};

export type IntegrationItemState =
  | "consistent"
  | "changed"
  | "removed"
  | "missing";

export type IntegrationItemStatus = {
  keys: string;
  function: string;
  state: IntegrationItemState;
  /** The keys the function has in the app now (changed, removed). */
  current?: string[];
  /** Who has the key in the app now. */
  owners?: IntegrationOwner[];
};

export type IntegrationItems = {
  revision: string;
  items: IntegrationItem[];
  statuses: IntegrationItemStatus[];
  consistent: boolean;
  /** Names this set of differences; dismiss refers to it. */
  differenceId?: string;
  /** differenceId is the set the person chose "Not now" for. */
  dismissed: boolean;
};

/** A change to the app's settings: give a function a key, or take one away. */
export type IntegrationChange = {
  function: string;
  add?: string;
  remove?: string;
};

export type IntegrationDisplaced = {
  function: string;
  name: string;
  keys: string;
};

/** What putting the padmate side on the app does for one item. */
export type IntegrationPlanEntry = {
  item: IntegrationItem;
  changes: IntegrationChange[];
  /** The functions that lose the key because of it. */
  displaced: IntegrationDisplaced[];
  /** Why the item cannot be written (the function is gone, …). */
  skipped?: string;
};

export type IntegrationPlan = { entries: IntegrationPlanEntry[] };

export type IntegrationBackupReason = "write" | "beforeRestore";

export type IntegrationBackup = {
  id: string;
  /** An RFC 3339 time (UTC). */
  created: string;
  /** Why it was made: a write of settings, or the one made before a restore. */
  reason: IntegrationBackupReason;
  /** How many functions the write changed (0 before a restore). */
  changes: number;
};

export type IntegrationWritten = {
  /** null when the write changed nothing: no file was written, nothing to undo. */
  backup: IntegrationBackup | null;
  revision: string;
};

export type IntegrationPutItemsParams = {
  profile: string;
  integration: string;
  revision: string;
  items: IntegrationItem[];
};

export type IntegrationCheckParams = {
  integration: string;
  function?: string;
  keys: string;
};

export type IntegrationDismissParams = {
  profile: string;
  integration: string;
  differenceId: string;
};

/**
 * What choosing the data folder came to: the folder was set, the user closed the
 * dialog, or the folder is not the app's (nothing was changed).
 */
export type IntegrationDataDirChoice = "changed" | "cancelled" | "notAppFolder";

export type IntegrationLanguage = "zh-CN" | "en";

export type IntegrationChangeKind = "files" | "running" | "differences";

/** The data of the integration.changed topic. */
export type IntegrationChanged = {
  integration: string;
  kind: IntegrationChangeKind;
  profile?: string;
  differenceId?: string;
  count?: number;
};

/** The daemon's error codes of integrations.* (§16.4). */
export const INTEGRATION_RUNNING = -32028;
export const INTEGRATION_UNAVAILABLE = -32029;
export const INTEGRATION_FILES_BUSY = -32030;
export const INTEGRATION_REVISION = -32009;
