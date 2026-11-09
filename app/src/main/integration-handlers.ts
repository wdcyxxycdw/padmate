import { win32 } from "node:path";
import type {
  IntegrationChange,
  IntegrationCheckParams,
  IntegrationDataDirChoice,
  IntegrationDismissParams,
  IntegrationItem,
  IntegrationLanguage,
  IntegrationPutItemsParams,
} from "../shared/integrations";
import {
  INTEGRATION_ID,
  parseIntegrationBackups,
  parseIntegrationCheck,
  parseIntegrationFunctions,
  parseIntegrationItems,
  parseIntegrationList,
  parseIntegrationPlan,
  parseIntegrationRestored,
  parseIntegrationWritten,
} from "../shared/integrations-parse";
import { ask } from "./ask";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { validateProfileID } from "./security";

function checked<T>(value: T | null): T {
  if (value === null) throw new Error("Invalid daemon response");
  return value;
}

function plain(value: unknown, what: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error(`Invalid ${what}`);
  return value as Record<string, unknown>;
}

function bounded(value: unknown, what: string, max: number): string {
  if (typeof value !== "string" || value.length > max)
    throw new Error(`Invalid ${what}`);
  return value;
}

function nonEmpty(value: unknown, what: string, max: number): string {
  const text = bounded(value, what, max);
  if (!text) throw new Error(`Invalid ${what}`);
  return text;
}

export function validateIntegrationID(value: unknown): string {
  if (typeof value !== "string" || !INTEGRATION_ID.test(value))
    throw new Error("Invalid integration ID");
  return value;
}

export function validateIntegrationLanguage(
  value: unknown,
): IntegrationLanguage {
  if (value !== "zh-CN" && value !== "en") throw new Error("Invalid language");
  return value;
}

function validateItem(value: unknown): IntegrationItem {
  const given = plain(value, "item");
  const path = given.path;
  if (
    !Array.isArray(path) ||
    path.length > 16 ||
    path.some((part) => typeof part !== "string" || part.length > 512)
  )
    throw new Error("Invalid item path");
  return {
    keys: nonEmpty(given.keys, "item keys", 256),
    function: nonEmpty(given.function, "item function", 256),
    name: bounded(given.name, "item name", 512),
    path: [...(path as string[])],
  };
}

/** integrations.check: the app, the function it is for (if any) and the keys. */
export function validateIntegrationCheck(
  value: unknown,
): IntegrationCheckParams {
  const given = plain(value, "check");
  return {
    integration: validateIntegrationID(given.integration),
    ...(given.function === undefined
      ? {}
      : { function: nonEmpty(given.function, "function", 256) }),
    keys: nonEmpty(given.keys, "keys", 256),
  };
}

/**
 * integrations.putItems: at most the 512 items a file can have (§16.2). The
 * revision is the one integrations.items gave, and is "" for a profile that
 * has no integrations/<id>.json yet (the first save creates it, like
 * profiles.put): an empty revision is valid.
 */
export function validateIntegrationPutItems(
  value: unknown,
): IntegrationPutItemsParams {
  const given = plain(value, "items");
  if (!Array.isArray(given.items) || given.items.length > 512)
    throw new Error("Invalid items");
  return {
    profile: validateProfileID(given.profile),
    integration: validateIntegrationID(given.integration),
    revision: bounded(given.revision, "revision", 256),
    items: given.items.map(validateItem),
  };
}

/** integrations.write: a change gives a function a key, takes one away, or both. */
export function validateIntegrationChanges(
  value: unknown,
): IntegrationChange[] {
  if (!Array.isArray(value) || value.length > 1024)
    throw new Error("Invalid changes");
  return value.map((entry) => {
    const given = plain(entry, "change");
    const out: IntegrationChange = {
      function: nonEmpty(given.function, "function", 256),
    };
    if (given.add !== undefined) out.add = nonEmpty(given.add, "keys", 256);
    if (given.remove !== undefined)
      out.remove = nonEmpty(given.remove, "keys", 256);
    if (out.add === undefined && out.remove === undefined)
      throw new Error("Invalid change");
    return out;
  });
}

export function validateIntegrationBackup(value: unknown): string {
  const id = nonEmpty(value, "backup", 128);
  if (!/^[0-9A-Za-z_.-]+$/.test(id)) throw new Error("Invalid backup");
  return id;
}

export function validateIntegrationDismiss(
  value: unknown,
): IntegrationDismissParams {
  const given = plain(value, "dismissal");
  return {
    profile: validateProfileID(given.profile),
    integration: validateIntegrationID(given.integration),
    differenceId: nonEmpty(given.differenceId, "difference", 256),
  };
}

function profileAndIntegration(value: unknown): {
  profile: string;
  integration: string;
} {
  const given = plain(value, "request");
  return {
    profile: validateProfileID(given.profile),
    integration: validateIntegrationID(given.integration),
  };
}

/**
 * What a folder holds to be the data folder of an integration (§16.3): any one
 * of these files. Main checks it before the daemon is told about the folder;
 * the daemon looks at the folder again when it uses it.
 */
const DATA_FOLDER_MARKERS: Record<string, string[][]> = {
  csp: [
    ["Shortcut", "default.khc"],
    ["Tool", "EditImageTool.todb"],
  ],
};

/** A folder on a local drive, in the spelling the daemon takes. */
function localFolder(value: unknown): string {
  if (typeof value !== "string" || value.length > 1024)
    throw new Error("Invalid folder");
  const path = value.replaceAll("/", "\\").replace(/\\+$/, "");
  const parts = path.split("\\");
  if (
    !/^[A-Za-z]:\\[^\\]/.test(path) ||
    /["*?<>|]/.test(path) ||
    [...path].some((character) => character.charCodeAt(0) < 32) ||
    path.indexOf(":", 2) !== -1 ||
    parts.some((part) => part === "" || part === "." || part === "..")
  )
    throw new Error("Invalid folder");
  return path;
}

/** The pieces of the data folder choice that touch the computer. */
export type IntegrationFolderOptions = {
  /** The folder dialog; undefined when the user cancels. */
  chooseFolder: () => Promise<string | undefined>;
  /** Whether the file exists (and is a file). */
  fileExists: (path: string) => Promise<boolean>;
};

const noFolderPicker: IntegrationFolderOptions = {
  chooseFolder: async () => {
    throw new Error("No folder dialog");
  },
  fileExists: async () => false,
};

// App integrations (ARCHITECTURE §16.4). The renderer passes no method name:
// each handler is one fixed integrations.* method with its parameters cut down
// to the shapes the daemon takes. The failure keeps the daemon's code only
// (-32028 the app is running, -32029 the integration is unavailable, -32030
// its files were busy or changed, -32009 a revision that moved).
export function integrationHandlers(
  call: DaemonCall,
  folder: IntegrationFolderOptions = noFolderPicker,
): Record<string, EditorHandler> {
  return {
    integrationsList: {
      count: 0,
      run: () =>
        ask(
          call,
          "integrations.list",
          {},
          (value) => checked(parseIntegrationList(value)),
          "Integrations not read",
        ),
    },
    integrationsFunctions: {
      count: 2,
      run: (integration, language) =>
        ask(
          call,
          "integrations.functions",
          {
            integration: validateIntegrationID(integration),
            language: validateIntegrationLanguage(language),
          },
          (value) => checked(parseIntegrationFunctions(value)),
          "Functions not read",
        ),
    },
    integrationsCheck: {
      count: 1,
      run: (params) =>
        ask(
          call,
          "integrations.check",
          validateIntegrationCheck(params),
          (value) => checked(parseIntegrationCheck(value)),
          "Key not checked",
        ),
    },
    integrationsItems: {
      count: 2,
      run: (profile, integration) =>
        ask(
          call,
          "integrations.items",
          {
            profile: validateProfileID(profile),
            integration: validateIntegrationID(integration),
          },
          (value) => checked(parseIntegrationItems(value)),
          "Items not read",
        ),
    },
    integrationsPutItems: {
      count: 1,
      run: (params) =>
        ask(
          call,
          "integrations.putItems",
          validateIntegrationPutItems(params),
          () => ({}),
          "Items not saved",
        ).then(() => undefined),
    },
    integrationsPlan: {
      count: 2,
      run: (profile, integration) =>
        ask(
          call,
          "integrations.plan",
          profileAndIntegration({ profile, integration }),
          (value) => checked(parseIntegrationPlan(value)),
          "Plan not made",
        ),
    },
    integrationsWrite: {
      count: 2,
      run: (integration, changes) =>
        ask(
          call,
          "integrations.write",
          {
            integration: validateIntegrationID(integration),
            changes: validateIntegrationChanges(changes),
          },
          (value) => checked(parseIntegrationWritten(value)),
          "Settings not written",
        ),
    },
    integrationsBackups: {
      count: 1,
      run: (integration) =>
        ask(
          call,
          "integrations.backups",
          { integration: validateIntegrationID(integration) },
          (value) => checked(parseIntegrationBackups(value)),
          "Backups not read",
        ),
    },
    integrationsRestore: {
      count: 2,
      run: (integration, backup) =>
        ask(
          call,
          "integrations.restore",
          {
            integration: validateIntegrationID(integration),
            backup: validateIntegrationBackup(backup),
          },
          (value) => checked(parseIntegrationRestored(value)),
          "Backup not restored",
        ),
    },
    // The data folder (§16.3, §16.5). The folder dialog and the check of the
    // chosen folder are here; the window supplies no path. A folder that is not
    // the app's is answered, not sent to the daemon.
    integrationsChooseDataDir: {
      count: 1,
      run: async (integration): Promise<IntegrationDataDirChoice> => {
        const id = validateIntegrationID(integration);
        const markers = DATA_FOLDER_MARKERS[id];
        if (!markers) throw new Error("Invalid integration ID");
        const chosen = await folder.chooseFolder();
        if (chosen === undefined) return "cancelled";
        let dir: string;
        try {
          dir = localFolder(chosen);
        } catch {
          return "notAppFolder";
        }
        const found = await Promise.all(
          markers.map((marker) =>
            folder.fileExists(win32.join(dir, ...marker)),
          ),
        );
        if (!found.some(Boolean)) return "notAppFolder";
        await ask(
          call,
          "integrations.setDataDir",
          { integration: id, dataDir: dir },
          () => ({}),
          "Folder not saved",
        );
        return "changed";
      },
    },
    integrationsResetDataDir: {
      count: 1,
      run: (integration) =>
        ask(
          call,
          "integrations.setDataDir",
          { integration: validateIntegrationID(integration), dataDir: null },
          () => ({}),
          "Folder not saved",
        ).then(() => undefined),
    },
    integrationsDismiss: {
      count: 1,
      run: (params) =>
        ask(
          call,
          "integrations.dismiss",
          validateIntegrationDismiss(params),
          () => ({}),
          "Differences not dismissed",
        ).then(() => undefined),
    },
  };
}
