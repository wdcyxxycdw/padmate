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
  StartupState,
} from "../shared/api";
import type { OverlayPrefs } from "../shared/overlay";
import {
  initialUpdateState,
  type UpdateDismissal,
  type UpdateState,
} from "../shared/update";
/** What a development daemon says about start at sign-in. */
export const developmentStartup: StartupState = {
  available: false,
  enabled: false,
  current: false,
  mode: "dry-run",
  reason: "development",
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
    // The mapping wizard (IN-19): a joystick with no mapping, two buttons.
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
    openGameBarSettings: vi.fn(async () => {}),
    onClosing: vi.fn((_listener: () => void) => () => {}),
    readyToClose: vi.fn(async () => {}),
    exportCheatSheet: vi.fn(async () => ({ saved: true })),
    openStartFolder: vi.fn(async () => {}),
    retryStart: vi.fn(async () => {}),
    quit: vi.fn(async () => {}),
    markConfigRepairSeen: vi.fn(async (_backup: string) => {}),
    showConfigBackup: vi.fn(async (_backup: string) => {}),
    setOverlayPrefs: vi.fn(async (_prefs: OverlayPrefs) => {}),
    // The automatic update (PKG-10): a build that cannot update, the state a
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
    openReleasePage: vi.fn(async () => {}),
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
