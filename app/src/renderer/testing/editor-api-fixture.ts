import { vi } from "vitest";
import type {
  BrowsedApp,
  DeviceIdentify,
  InstalledApp,
  LatencyResult,
  MacroImportOptions,
  MacroImportResult,
  MacroList,
  MappingResult,
  ProfileDocument,
  ProfileEntry,
  ProfileImportOptions,
  ProfileImportResult,
  ProfileResolution,
  ProfileSaveResult,
  RawInfo,
  RecentApp,
  SavedLanguage,
  StartupState,
  UILanguage,
} from "../../shared/api";
import type {
  IntegrationBackup,
  IntegrationChange,
  IntegrationCheck,
  IntegrationCheckParams,
  IntegrationDataDirChoice,
  IntegrationDismissParams,
  IntegrationFunctions,
  IntegrationInfo,
  IntegrationItems,
  IntegrationLanguage,
  IntegrationPlan,
  IntegrationPutItemsParams,
  IntegrationWritten,
} from "../../shared/integrations";
import type { JoyCon2Status } from "../../shared/joycon2";
import type { OverlayPrefs } from "../../shared/overlay";
import type {
  PadsListResult,
  PadsMigrateResult,
  PadsSetParams,
  PadsSetResult,
} from "../../shared/pads";
import {
  initialUpdateState,
  type UpdateDismissal,
  type UpdateState,
} from "../../shared/update";
/** What a development daemon says about start at sign-in. */
export const developmentStartup: StartupState = {
  available: false,
  enabled: false,
  current: false,
  mode: "dry-run",
  reason: "development",
};
/** What a daemon without Joy-Con 2 support says. */
export const joyCon2Unavailable: JoyCon2Status = {
  available: false,
  autoConnect: false,
  windowOpen: false,
  windowLeftMs: 0,
  known: [],
  links: [],
};
export function editorAPIFixture() {
  const api = {
    listProfiles: vi.fn(async (): Promise<ProfileEntry[]> => []),
    getProfile: vi.fn(
      async (id: string): Promise<ProfileDocument> => ({
        id,
        text: JSON.stringify({
          schemaVersion: 0,
          name: id,
          layers: { base: {} },
        }),
        revision: "r1",
        diagnostics: [],
      }),
    ),
    putProfile: vi.fn(
      async (
        _id: string,
        _text: string,
        _revision: string,
      ): Promise<ProfileSaveResult> => ({ revision: "r2", applied: true }),
    ),
    validateProfile: vi.fn(async () => ({ valid: true, diagnostics: [] })),
    activateProfile: vi.fn(async (id: string | null) => ({
      profileId: id ?? "",
      generation: 2,
    })),
    deleteProfile: vi.fn(async (_id: string, _revision: string) => {}),
    copyProfile: vi.fn(async (_from: string, _to: string) => ({
      revision: "r-copy",
    })),
    exportProfile: vi.fn(async (_id: string, _name: string) => ({
      saved: true,
    })),
    importProfile: vi.fn(
      async (
        _options?: ProfileImportOptions,
      ): Promise<ProfileImportResult> => ({
        canceled: true,
      }),
    ),
    getRecentApps: vi.fn(async (): Promise<RecentApp[]> => []),
    getAppIcon: vi.fn(async (_path: string): Promise<string> => ""),
    resolveAppPath: vi.fn(async (_exe: string): Promise<string> => ""),
    listInstalledApps: vi.fn(async (): Promise<InstalledApp[]> => []),
    browseApp: vi.fn(async (): Promise<BrowsedApp | null> => null),
    explainProfile: vi.fn(async (control: string) => ({
      control,
      profileId: "paint",
      name: "Paint",
      declarativeOnly: true,
      functionKey: "start" as const,
      isFunctionKey: false,
      slots: [],
    })),
    getLatency: vi.fn(
      async (_device?: string): Promise<LatencyResult> => ({
        devices: [],
        dryRun: false,
        warnP99Us: 1500,
        warnMinSamples: 50,
      }),
    ),
    resetLatency: vi.fn(async (_device?: string) => ({ reset: true })),
    getScriptDescription: vi.fn(async () => ({
      entry: "main.ts",
      api: "0.21",
      metadata: { runtime: 0, actions: [], registrations: [] },
    })),
    getScriptRegistrations: vi.fn(async () => []),
    identifyDevice: vi.fn(
      async (_key: string): Promise<DeviceIdentify> => ({
        rumble: true,
        led: false,
        player: false,
      }),
    ),
    // The mapping wizard: a joystick with no mapping, two buttons.
    rawWatchStart: vi.fn(
      async (_device: string): Promise<RawInfo> => ({
        device: "030000007e05/p:x",
        id: 1,
        guid: "ff00aabb000000000000000000007600",
        gamepad: false,
        axes: 0,
        buttons: 2,
        hats: 0,
        axisRest: [],
        watching: true,
      }),
    ),
    rawWatchStop: vi.fn(async (_device: string) => {}),
    setMapping: vi.fn(
      async (_device: string, mapping: string): Promise<MappingResult> => ({
        device: "030000007e05/p:x",
        guid: "ff00aabb000000000000000000007600",
        mapping,
        keys: ["030000007e05/p:x"],
        changed: true,
        applied: true,
        notes: [],
        warnings: [],
      }),
    ),
    resetMapping: vi.fn(
      async (_device: string): Promise<MappingResult> => ({
        device: "030000007e05/p:x",
        guid: "ff00aabb000000000000000000007600",
        mapping: "",
        keys: ["030000007e05/p:x"],
        changed: true,
        applied: true,
        notes: [],
        warnings: [],
      }),
    ),
    // The Joy-Con 2 section: a daemon without Joy-Con 2 support, so the
    // section is not shown unless a test gives it a state.
    getJoyCon2Status: vi.fn(
      async (): Promise<JoyCon2Status> => joyCon2Unavailable,
    ),
    watchJoyCon2: vi.fn(async (_on: boolean) => {}),
    pairJoyCon2: vi.fn(async (): Promise<JoyCon2Status> => joyCon2Unavailable),
    cancelJoyCon2Pair: vi.fn(
      async (): Promise<JoyCon2Status> => joyCon2Unavailable,
    ),
    forgetJoyCon2: vi.fn(
      async (_serial: string): Promise<JoyCon2Status> => joyCon2Unavailable,
    ),
    // The settings of each controller: no controller has been connected, and
    // a test that changes a setting gives the answers itself.
    listPads: vi.fn(
      async (): Promise<PadsListResult> => ({
        revision: "r1",
        legacy: {
          any: false,
          functionKey: false,
          feedback: false,
          panic: false,
        },
        pads: [],
      }),
    ),
    setPad: vi.fn(async (_params: PadsSetParams): Promise<PadsSetResult> => {
      throw new Error("setPad is not set up");
    }),
    migratePads: vi.fn(async (): Promise<PadsMigrateResult> => {
      throw new Error("migratePads is not set up");
    }),
    // App integrations: none found, so nothing about them shows. A test of
    // them gives the answers itself (testing/integrations-fixture.ts).
    listIntegrations: vi.fn(async (): Promise<IntegrationInfo[]> => []),
    getIntegrationFunctions: vi.fn(
      async (
        _integration: string,
        _language: IntegrationLanguage,
      ): Promise<IntegrationFunctions> => ({
        revision: "f1",
        tree: [],
        functions: [],
      }),
    ),
    checkIntegrationKey: vi.fn(
      async (_params: IntegrationCheckParams): Promise<IntegrationCheck> => ({
        status: "free",
        owners: [],
        notes: [],
      }),
    ),
    getIntegrationItems: vi.fn(
      async (
        _profile: string,
        _integration: string,
      ): Promise<IntegrationItems> => ({
        revision: "i1",
        items: [],
        statuses: [],
        consistent: true,
        dismissed: false,
      }),
    ),
    putIntegrationItems: vi.fn(
      async (_params: IntegrationPutItemsParams): Promise<void> => {},
    ),
    planIntegration: vi.fn(
      async (
        _profile: string,
        _integration: string,
      ): Promise<IntegrationPlan> => ({
        entries: [],
      }),
    ),
    writeIntegration: vi.fn(
      async (
        _integration: string,
        _changes: IntegrationChange[],
      ): Promise<IntegrationWritten> => ({
        backup: {
          id: "b1",
          created: "2026-11-04T10:00:00Z",
          reason: "write",
          changes: 1,
        },
        revision: "f2",
      }),
    ),
    listIntegrationBackups: vi.fn(
      async (_integration: string): Promise<IntegrationBackup[]> => [],
    ),
    restoreIntegrationBackup: vi.fn(
      async (
        _integration: string,
        _backup: string,
      ): Promise<IntegrationBackup> => ({
        id: "b2",
        created: "2026-11-04T10:05:00Z",
        reason: "beforeRestore",
        changes: 0,
      }),
    ),
    chooseIntegrationDataDir: vi.fn(
      async (_integration: string): Promise<IntegrationDataDirChoice> =>
        "cancelled",
    ),
    resetIntegrationDataDir: vi.fn(
      async (_integration: string): Promise<void> => {},
    ),
    dismissIntegrationDifferences: vi.fn(
      async (_params: IntegrationDismissParams): Promise<void> => {},
    ),
    listMacros: vi.fn(
      async (): Promise<MacroList> => ({ macros: [], missing: [], legacy: [] }),
    ),
    createMacro: vi.fn(async (name: string) => ({ file: `${name}.ts` })),
    importMacro: vi.fn(
      async (_options?: MacroImportOptions): Promise<MacroImportResult> => ({
        file: "画笔.ts",
        replaced: false,
      }),
    ),
    renameMacro: vi.fn(async (_file: string, to: string) => ({
      file: to,
      bindings: 0,
      profiles: [] as Array<{ id: string; revision: string; bindings: number }>,
    })),
    deleteMacro: vi.fn(async (_file: string) => ({
      bindings: 0,
      profiles: [] as Array<{ id: string; revision: string; bindings: number }>,
    })),
    detachScript: vi.fn(async (_id: string) => ({
      revision: "r2",
      bindings: 0,
    })),
    editMacro: vi.fn(async (_file: string) => ({ opened: true })),
    editScript: vi.fn(async (_id: string) => ({ opened: true })),
    captureStart: vi.fn(async () => 1),
    captureStop: vi.fn(async () => {}),
    padCaptureStart: vi.fn(async (_stays?: boolean) => {}),
    padCaptureStop: vi.fn(async () => {}),
    onCapture: vi.fn(() => () => {}),
    getStartup: vi.fn(async (): Promise<StartupState> => developmentStartup),
    setStartup: vi.fn(async (_enabled: boolean): Promise<StartupState> => {
      throw {
        message: "login startup cannot be set in a development build",
        code: -32017,
        data: developmentStartup,
      };
    }),
    onClosing: vi.fn((_listener: () => void) => () => {}),
    readyToClose: vi.fn(async () => {}),
    exportCheatSheet: vi.fn(async () => ({ saved: true })),
    openStartFolder: vi.fn(async () => {}),
    retryStart: vi.fn(async () => {}),
    quit: vi.fn(async () => {}),
    markConfigRepairSeen: vi.fn(async (_backup: string) => {}),
    showConfigBackup: vi.fn(async (_backup: string) => {}),
    setOverlayPrefs: vi.fn(async (_prefs: OverlayPrefs) => {}),
    // No language file: the window starts in Chinese, or as localStorage says.
    getLanguage: vi.fn(async (): Promise<SavedLanguage | null> => null),
    setLanguage: vi.fn(async (_language: UILanguage) => {}),
    // The automatic update: a build that cannot update, the state a
    // development run has. A test of the update dialogs replaces them.
    getUpdateState: vi.fn(async (): Promise<UpdateState> => initialUpdateState),
    checkForUpdate: vi.fn(async (): Promise<UpdateState> => initialUpdateState),
    downloadUpdate: vi.fn(async (): Promise<UpdateState> => initialUpdateState),
    installUpdate: vi.fn(async () => ({ dryRun: false })),
    dismissUpdate: vi.fn(
      async (_choice: UpdateDismissal): Promise<UpdateState> =>
        initialUpdateState,
    ),
    setAutoUpdate: vi.fn(
      async (_enabled: boolean): Promise<UpdateState> => initialUpdateState,
    ),
    openManualDownload: vi.fn(async () => {}),
    onUpdateState: vi.fn((_listener: (state: UpdateState) => void) => () => {}),
    // A profile that is based on nothing: its own text. A test of
    // inheritance replaces it.
    resolveProfile: vi.fn(async (id: string): Promise<ProfileResolution> => {
      const document = await api.getProfile(id);
      return {
        id,
        text: document.text,
        revision: document.revision,
        inherited: [],
        diagnostics: [],
      };
    }),
  };
  return api;
}
