import { createStore } from "zustand/vanilla";
import type {
  BatteryReading,
  ConfigGetResult,
  ConfigRepair,
  ConnectionState,
  DaemonEvent,
  DeviceBattery,
  DeviceEntry,
  DeviceIdentify,
  ForegroundApp,
  LogEntry,
  PadmateAPI,
  ProfileEntry,
  ProfileImportOptions,
  ProfileImportResult,
  RecentApp,
  ScriptStatusEntry,
  SlotState,
  StartupState,
  StatusResult,
} from "../shared/api";
import { LibraryErrorCode } from "../shared/api";
import type { Config } from "../shared/generated/schema";
import {
  type ActivityRow,
  appendActivity,
  type DecisionActivity,
  decisionRow,
  type UnroutedActivity,
  unroutedRow,
} from "./activity-model";
import {
  blankProfileText,
  changeConfigText,
  copyName,
  errorInfo,
  failureText,
  isDefaultProfile,
  isRevisionConflict,
  type LibraryFailure,
  type LibraryFailureKind,
  type LibraryOutcome,
  libraryFailure,
  nameProblemText,
  nextActiveAfterDelete,
  profileName,
  profileNameProblem,
  removeProfileReferences,
  renameProfileText,
  restoreProfileReferences,
  slugId,
  withoutRetired,
} from "./library-model";
import {
  type OverlaySettings,
  savedOverlaySettings,
  saveOverlaySettings,
  validOverlayChange,
} from "./overlay-settings";
import {
  FEEDBACK_SWITCHES,
  feedbackSwitchOn,
  identifyFailureText,
  setFeedbackSwitch,
  startupFailureText,
} from "./settings-model";

export const LOG_LIMIT = 256;
/** How long a window capture waits for a click into another app (PRO-16). */
export const CAPTURE_TIMEOUT_MS = 30_000;
/** apps.recent keeps at most this many; live updates keep the same bound. */
export const RECENT_APPS_LIMIT = 12;
export type Language = "zh-CN" | "en";
/** "logs" and "diagnostics" are views inside Settings, not navigation rail items. */
export type Page =
  | "overview"
  | "profiles"
  | "scripts"
  | "logs"
  | "diagnostics"
  | "settings";
/** The transient autosave state of library writes; seq changes each time. */
export interface LibrarySave {
  state: "idle" | "saving" | "saved" | "failed";
  seq: number;
}
/** A window capture in progress: the next foreground app binds to target. */
export interface WindowCapture {
  /** The profile the picked app is bound to. */
  target: string;
  /** Date.now() when it started; it ends after CAPTURE_TIMEOUT_MS. */
  startedAt: number;
}
/** What a capture picked; the UI adds it and then calls clearCaptured. */
export interface CapturedApp {
  target: string;
  app: ForegroundApp;
}
export interface PadInput {
  buttons: Record<string, boolean>;
  axes: Record<string, number>;
}
export interface OutputPreview {
  chord: string;
  localDryRun: boolean;
  dryRun: boolean;
}
/**
 * The last press that ran an action (UI-44): an engine.activity decision
 * with reason "dispatch". The mapping page shows it while padmate's own
 * window is in front.
 */
export interface KeyPreview {
  /** Counts the presses, so the same action twice shows twice. */
  seq: number;
  /** The pad, from the decision's slot; null when the slot is not known. */
  device: number | null;
  /** The engine's control name, such as "south", "lt" or "ls.up". */
  control: string;
  /** The decision's action, such as "tap Ctrl+Z" or "script cycleBrush". */
  action: string;
}
/**
 * The Settings page's own config.json fields. Switching by app (enabled)
 * belongs to the profiles page, so a Settings save never writes it.
 */
export interface BasicSettings {
  sendToOwnWindow: boolean;
  /** output.eventGapMs (OUT-17): whole milliseconds between key events. */
  eventGapMs: number;
  /**
   * The feedback switches (UI-27, ADR-0067): config.json's feedback.rumble,
   * led, pause and lowBattery. Each is on unless the file says false.
   */
  rumble: boolean;
  led: boolean;
  pause: boolean;
  lowBattery: boolean;
}
/** The largest output.eventGapMs the schema accepts. */
export const EVENT_GAP_MAX_MS = 50;

/** A whole number of milliseconds from 0 to EVENT_GAP_MAX_MS, or null. */
export function eventGapValue(value: unknown): number | null {
  return typeof value === "number" &&
    Number.isInteger(value) &&
    value >= 0 &&
    value <= EVENT_GAP_MAX_MS
    ? value
    : null;
}
/** For a library action whose caller shows its failure itself. */
export interface ReportOptions {
  /**
   * Read when the action ends: true while the caller (an open dialog) shows
   * the failure, so the shell's alert stays clear. Without it, or once it
   * returns false, the shell's alert shows the failure.
   */
  inline?: () => boolean;
}
/**
 * What the controller chips show (UI-35): the action's name or its shortcut.
 * The control's own name went away with ADR-0077.
 */
export type LabelDisplay = "actions" | "keys";
/**
 * Start at sign-in (UI-19), see PadmateState.startup; and the overlay's
 * toasts, position and opacity (HUD-05), see overlay-settings.ts.
 */
export type { OverlaySettings, StartupState };
/** localStorage key: "done" once the first-run page (UI-38) is not offered. */
export const ONBOARDING_KEY = "padmate.onboarding";
interface InputEvent {
  kind: string;
  device: number;
  control?: string;
  value?: number;
}
interface EngineState {
  kind: string;
  paused: boolean;
  capturing: boolean;
  suspended: string;
  slots: SlotState[];
  localDryRun?: boolean;
  sendToOwnWindow?: boolean;
  /** The power of each controller that has read one (IN-12). */
  batteries?: BatteryReading[];
}

/** The power a state event lists for a controller, as devices carry it. */
function batteryOf(reading: BatteryReading): DeviceBattery {
  return reading.percent === undefined
    ? { state: reading.state }
    : { state: reading.state, percent: reading.percent };
}

export interface PadmateState {
  api: PadmateAPI;
  connection: ConnectionState;
  status: StatusResult | null;
  scripts: ScriptStatusEntry[];
  config: ConfigGetResult | null;
  configError: string;
  inputs: Record<number, PadInput>;
  logs: LogEntry[];
  /**
   * The event list of the diagnostics page (UI-20, ADR-0071): the latest
   * ACTIVITY_LIMIT rows from engine.activity, oldest first, kept while
   * connected whatever page is open, and cleared when a connection is made
   * again. The shell does not subscribe to it (see App's selection), or each
   * decision would render the whole window.
   */
  activity: ActivityRow[];
  /** 暂停记录: new events are not kept. Only the page's button sets it. */
  activityPaused: boolean;
  preview: OutputPreview | null;
  keyPreview: KeyPreview | null;
  dropped: number;
  selectedDevice: number | null;
  language: Language;
  labelDisplay: LabelDisplay;
  /** HUD-05: kept in localStorage, pushed to Main with the language. */
  overlay: OverlaySettings;
  page: Page;
  busy: boolean;
  error: string;
  settingsSaved: boolean;
  profileChanges: number;
  /** Conflicting software the owner closed the notice for, this session. */
  dismissedConflicts: string[];
  /**
   * Library profiles in profiles.list order; sort with sortProfiles (lists
   * to manage) or sortProfilesByName (menus to pick from).
   */
  profiles: ProfileEntry[];
  /** profiles.list has answered once on this connection. */
  profilesLoaded: boolean;
  /** Why profiles.list failed, e.g. a session without a library. */
  profilesError: string;
  /** Recently seen apps, newest first, without padmate's own windows. */
  recentApps: RecentApp[];
  capture: WindowCapture | null;
  captured: CapturedApp | null;
  /** A library operation is queued or running. */
  libraryBusy: boolean;
  librarySave: LibrarySave;
  /**
   * The first-run page (UI-38) no longer opens by itself: it went to the
   * main page or was closed, or the library had profiles besides the fixed
   * default once in this browser profile.
   */
  onboardingDone: boolean;
  /**
   * A repair of config.json (PRO-29, ADR-0057) the user has not confirmed:
   * the daemon backed the file up and reset what had errors. The shell
   * shows one dialog for it; acknowledgeConfigRepair closes it.
   */
  configRepair: ConfigRepair | null;
  /**
   * Start at sign-in (UI-19, ADR-0052): the daemon's own entry, read on
   * connect, with refresh and refreshStartup. null until it answers, and
   * with a daemon that has no startup.get.
   */
  startup: StartupState | null;
  start(): () => void;
  /** Reads status, scripts, config.json and the profile list again. */
  refresh(): Promise<void>;
  /** Reads the profile list and config.json again. */
  refreshLibrary(): Promise<void>;
  /** Reads start at sign-in again, e.g. when Settings opens. */
  refreshStartup(): Promise<void>;
  /**
   * Turns start at sign-in on or off; only an explicit click calls it.
   * Resolves to "" once saved, else to the failure, which the shell's
   * alert shows unless options.inline says the caller shows it.
   */
  setStartup(enabled: boolean, options?: ReportOptions): Promise<string>;
  /**
   * Identify (IN-13): the controller with this key (DeviceEntry.key) buzzes
   * and flashes for at most a second and a half. Resolves to the cues it
   * gave (all false: nothing to show), or to null when it did not: the
   * shell's alert says why. It does not take the page's busy state, since it
   * changes nothing.
   */
  identifyDevice(key: string): Promise<DeviceIdentify | null>;
  refreshRecentApps(): Promise<void>;
  /**
   * The user confirmed the repair dialog: it closes for good (a repair of
   * the same backup never shows again in this window) and the daemon is
   * told. Without a repair it does nothing; a daemon that cannot be told
   * shows it again at the next start.
   */
  acknowledgeConfigRepair(): Promise<void>;
  /**
   * Autosaves one change to config.json. mutate gets a fresh parse and
   * returns the new document, or null for no change. After a revision
   * conflict the file is read again and mutate applied once more.
   */
  updateConfig(
    mutate: (config: Config) => Config | null,
    options?: ReportOptions,
  ): Promise<LibraryOutcome>;
  /** null disables mapping. The caller guards an unsaved mapping draft. */
  activateProfile(id: string | null): Promise<LibraryOutcome>;
  /** Creates an empty profile; resolves to its ID. */
  createProfile(
    name: string,
    options?: ReportOptions,
  ): Promise<LibraryOutcome<string>>;
  /**
   * Copies a profile as "<name> 副本", or "<name> 副本 2"… when that name
   * is taken; resolves to the new ID and name.
   */
  copyProfile(
    id: string,
  ): Promise<LibraryOutcome<{ id: string; name: string }>>;
  /**
   * Changes only the name; the caller guards an unsaved mapping draft. The
   * fixed default keeps its name (PRO-28).
   */
  renameProfile(
    id: string,
    name: string,
    options?: ReportOptions,
  ): Promise<LibraryOutcome>;
  /**
   * Deletes the folder for good. The daemon refuses the active profile
   * and one that config.json still selects; then this activates another
   * profile (the fixed default first), or removes id's rules, and asks
   * again. A refusal it cannot resolve (the startup profile, a conflict)
   * undoes both, so a failed delete changes nothing. The fixed default is
   * never deleted (PRO-28).
   */
  deleteProfile(id: string, options?: ReportOptions): Promise<LibraryOutcome>;
  /**
   * Saves profile id as a .padmate file where the user chooses (PRO-20),
   * named after its name in the UI; resolves to false when the user
   * cancels. The caller saves the mapping page's edits first.
   */
  exportProfile(
    id: string,
    options?: ReportOptions,
  ): Promise<LibraryOutcome<boolean>>;
  /**
   * Adds a .padmate file as a profile (PRO-20): without options the user
   * picks the file; { exists: true } asks the caller to repeat the call
   * with its token and a profile to replace or a new name. The list is
   * read again after a write.
   */
  importProfile(
    options?: ProfileImportOptions,
    report?: ReportOptions,
  ): Promise<LibraryOutcome<ProfileImportResult>>;
  /** Starts picking a window; what it picks is bound to profile target. */
  startCapture(target: string): void;
  cancelCapture(): void;
  clearCaptured(): void;
  /** Hides the save pill if seq is still the latest finished save. */
  clearLibrarySave(seq: number): void;
  reconnect(): Promise<void>;
  togglePause(): Promise<void>;
  releaseAll(): Promise<void>;
  reloadScripts(): Promise<void>;
  /** Writes only the given fields of config.json's output. */
  saveSettings(settings: Partial<BasicSettings>): Promise<void>;
  setLanguage(language: Language): void;
  setLabelDisplay(display: LabelDisplay): void;
  /** Changes the given overlay settings (HUD-05); an invalid value is ignored. */
  setOverlay(change: Partial<OverlaySettings>): void;
  setPage(page: Page): void;
  /** Marks the first-run page done, so it never opens by itself again. */
  finishOnboarding(): void;
  selectDevice(device: number): void;
  clearLogs(): void;
  /** 清空: forgets the rows of the event list. */
  clearActivity(): void;
  /** 暂停记录 / 继续记录. */
  setActivityPaused(paused: boolean): void;
  dismissError(): void;
  dismissConflict(name: string): void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function savedLanguage(): Language {
  try {
    return globalThis.localStorage?.getItem("padmate.language") === "en"
      ? "en"
      : "zh-CN";
  } catch {
    return "zh-CN";
  }
}

function savedLabelDisplay(): LabelDisplay {
  try {
    const value = globalThis.localStorage?.getItem("padmate.labelDisplay");
    // "names" (before ADR-0077) shows the action names.
    return value === "keys" ? value : "actions";
  } catch {
    return "actions";
  }
}

function savedOnboarding(): boolean {
  try {
    return globalThis.localStorage?.getItem(ONBOARDING_KEY) === "done";
  } catch {
    return false;
  }
}

function chooseDevice(
  devices: DeviceEntry[],
  current: number | null,
): number | null {
  return devices.some((device) => device.info.id === current)
    ? current
    : (devices[0]?.info.id ?? null);
}

/** The daemon's current profile; "" while mapping is disabled. */
export function activeProfileId(state: Pick<PadmateState, "status">): string {
  return state.status?.profileId ?? "";
}

/** Thrown when a response belongs to an earlier connection. */
class Stale extends Error {}

function failed(kind: LibraryFailureKind, message = ""): LibraryOutcome<never> {
  return { ok: false, failure: { kind, message } };
}

function noteRecent(apps: RecentApp[], app: RecentApp): RecentApp[] {
  const key = app.path.toLowerCase();
  return [
    { exe: app.exe, path: app.path, elevated: app.elevated },
    ...apps.filter((entry) => entry.path.toLowerCase() !== key),
  ].slice(0, RECENT_APPS_LIMIT);
}

const BASIC_SETTINGS_DEFAULT: BasicSettings = {
  sendToOwnWindow: false,
  eventGapMs: 0,
  rumble: true,
  led: true,
  pause: true,
  lowBattery: true,
};

export function readBasicSettings(
  config: ConfigGetResult | null,
): BasicSettings {
  try {
    const value: Config = JSON.parse(config?.text ?? '{"schemaVersion":0}');
    return {
      sendToOwnWindow: value.output?.sendToOwnWindow === true,
      eventGapMs: eventGapValue(value.output?.eventGapMs) ?? 0,
      rumble: feedbackSwitchOn(value, "rumble"),
      led: feedbackSwitchOn(value, "led"),
      pause: feedbackSwitchOn(value, "pause"),
      lowBattery: feedbackSwitchOn(value, "lowBattery"),
    };
  } catch {
    return { ...BASIC_SETTINGS_DEFAULT };
  }
}

export function createPadmateStore(api: PadmateAPI) {
  let lifetime = 0;
  let connectionEpoch = 0;
  let refreshSequence = 0;
  let configSequence = 0;
  let profilesSequence = 0;
  let recentSequence = 0;
  let startupSequence = 0;
  let saveSequence = 0;
  // Counts presses for the key preview; it never goes back, so a press
  // after keyPreview was cleared is still news.
  let pressSequence = 0;
  // The rows of the event list that came since the last flush. The events of
  // one batch from Main are handed over in one loop, so they are put in the
  // ring together, after it, in one update: the ring is copied once a batch,
  // not once an event (ADR-0071).
  let activitySequence = 0;
  let pendingActivity: ActivityRow[] = [];
  // Library operations run one at a time, each on the state the previous
  // one left. Every connection starts a new queue.
  let queue: Promise<unknown> = Promise.resolve();
  let queued = 0;
  // profile.changed {kind:"config"} that arrived while library operations
  // were queued: the revision it named, checked once the queue drains.
  let configChangedDuringWrite: string | null = null;
  // Apps seen while apps.recent is in flight, newest last.
  let liveApps: RecentApp[] | null = null;
  // Backups of repairs the user confirmed: a status read that still has
  // one does not bring its dialog back.
  const confirmedRepairs = new Set<string>();
  let captureTimer: ReturnType<typeof setTimeout> | undefined;
  const store = createStore<PadmateState>((set, get) => {
    async function refresh(readConfig = false) {
      if (get().connection.phase !== "connected") return;
      const epoch = connectionEpoch;
      const sequence = ++refreshSequence;
      const configRequest = readConfig ? ++configSequence : configSequence;
      if (readConfig) {
        set({ settingsSaved: false });
        void loadStartup();
      }
      const requests = await Promise.allSettled([
        api.getStatus(),
        api.getScriptStatus(),
        readConfig ? api.getConfig() : Promise.resolve(null),
      ]);
      if (epoch !== connectionEpoch) return;
      const [status, scripts, config] = requests;
      if (sequence === refreshSequence) {
        if (status.status === "fulfilled") {
          const repair = status.value.configRepair;
          set({
            configRepair:
              repair && !confirmedRepairs.has(repair.backup) ? repair : null,
            status: status.value,
            selectedDevice: chooseDevice(
              status.value.devices ?? [],
              get().selectedDevice,
            ),
          });
        } else set({ error: message(status.reason) });
        if (scripts.status === "fulfilled")
          set({ scripts: scripts.value ?? [] });
      }
      if (readConfig && configRequest === configSequence) {
        if (config.status === "fulfilled")
          set({ config: config.value, configError: "" });
        else set({ config: null, configError: message(config.reason) });
      }
    }

    /** Reads config.json; rejects with Stale after a reconnect. */
    async function loadConfig(): Promise<ConfigGetResult> {
      const epoch = connectionEpoch;
      const sequence = ++configSequence;
      try {
        const config = await api.getConfig();
        if (epoch !== connectionEpoch) throw new Stale();
        if (sequence === configSequence) set({ config, configError: "" });
        return config;
      } catch (error) {
        if (
          !(error instanceof Stale) &&
          epoch === connectionEpoch &&
          sequence === configSequence
        )
          set({ config: null, configError: errorInfo(error).message });
        throw error;
      }
    }

    async function loadProfiles() {
      if (get().connection.phase !== "connected") return;
      const epoch = connectionEpoch;
      const sequence = ++profilesSequence;
      try {
        const profiles = await api.listProfiles();
        if (epoch === connectionEpoch && sequence === profilesSequence) {
          const list = Array.isArray(profiles) ? profiles : [];
          set({ profiles: list, profilesLoaded: true, profilesError: "" });
          // Someone who already has profiles besides the fixed default (an
          // upgrade) never gets the first-run page.
          if (list.some((entry) => !isDefaultProfile(entry.id)))
            markOnboarded();
        }
      } catch (error) {
        if (epoch === connectionEpoch && sequence === profilesSequence)
          set({
            profiles: [],
            profilesLoaded: true,
            profilesError: errorInfo(error).message,
          });
      }
    }

    async function loadStartup() {
      if (get().connection.phase !== "connected") return;
      const epoch = connectionEpoch;
      const sequence = ++startupSequence;
      let startup: StartupState | null = null;
      try {
        startup = await api.getStartup();
      } catch {
        // A daemon without startup.get: no switch.
      }
      if (epoch === connectionEpoch && sequence === startupSequence)
        set({ startup });
    }

    // HUD-05: tells Main what the overlay needs from the UI: the language of
    // its toasts and the three settings. Main checks it and ignores what is
    // invalid. It is sent at start and with every change; a failure leaves the
    // overlay as it was, and the next push repeats all of it.
    function pushOverlayPrefs() {
      const { language, overlay } = get();
      try {
        void Promise.resolve(
          api.setOverlayPrefs({ language, ...overlay }),
        ).catch(() => {});
      } catch {
        /* The overlay is optional. */
      }
    }

    function markOnboarded() {
      if (get().onboardingDone) return;
      set({ onboardingDone: true });
      try {
        globalThis.localStorage?.setItem(ONBOARDING_KEY, "done");
      } catch {
        /* The first-run page stays closed for this window. */
      }
    }

    async function loadRecentApps() {
      if (get().connection.phase !== "connected") return;
      const epoch = connectionEpoch;
      const sequence = ++recentSequence;
      liveApps = [];
      try {
        const apps = await api.getRecentApps();
        if (epoch !== connectionEpoch || sequence !== recentSequence) return;
        // Apps that came to the front during the request are newer.
        let recent = Array.isArray(apps)
          ? apps.slice(0, RECENT_APPS_LIMIT)
          : [];
        for (const app of liveApps ?? []) recent = noteRecent(recent, app);
        set({ recentApps: recent });
      } catch {
        /* The picker still offers capture and typing a name. */
      } finally {
        if (sequence === recentSequence) liveApps = null;
      }
    }

    function configChanged(revision: string) {
      if (revision && revision === get().config?.revision) return;
      // The queued write's own result is newer than a read started now.
      if (queued > 0) {
        configChangedDuringWrite = revision;
        return;
      }
      void loadConfig().catch(() => {});
    }

    function drained() {
      const revision = configChangedDuringWrite;
      configChangedDuringWrite = null;
      if (
        revision !== null &&
        (!revision || revision !== get().config?.revision)
      )
        void loadConfig().catch(() => {});
    }

    function connectionChanged(connection: ConnectionState) {
      ++connectionEpoch;
      queue = Promise.resolve();
      queued = 0;
      configChangedDuringWrite = null;
      liveApps = null;
      pendingActivity = [];
      clearTimeout(captureTimer);
      const previous = get().connection.phase;
      set({
        connection,
        error: "",
        capture: null,
        captured: null,
        libraryBusy: false,
        librarySave: { state: "idle", seq: ++saveSequence },
      });
      if (connection.phase !== "connected") {
        set({
          status: null,
          scripts: [],
          inputs: {},
          preview: null,
          keyPreview: null,
          selectedDevice: null,
          config: null,
          busy: false,
          settingsSaved: false,
          profiles: [],
          profilesLoaded: false,
          profilesError: "",
          recentApps: [],
          configRepair: null,
          startup: null,
        });
      } else {
        if (previous !== "connected")
          set({
            logs: [],
            activity: [],
            activityPaused: false,
            inputs: {},
            preview: null,
            keyPreview: null,
            dropped: 0,
            dismissedConflicts: [],
          });
        void refresh(true);
        void loadProfiles();
      }
    }

    /**
     * Keeps a row of the event list (UI-20): not while recording is paused.
     * The ring is updated in a microtask, after the rest of the batch that
     * the event came with.
     */
    function record(row: ActivityRow | null) {
      if (!row || get().activityPaused) return;
      pendingActivity.push(row);
      if (pendingActivity.length > 1) return;
      const epoch = connectionEpoch;
      queueMicrotask(() => {
        // The connection changed meanwhile: its reset dropped the pending
        // rows, and those now waiting belong to the next flush.
        if (epoch !== connectionEpoch) return;
        const added = pendingActivity;
        pendingActivity = [];
        if (added.length)
          set({ activity: appendActivity(get().activity, added) });
      });
    }

    /** A press that ran an action (UI-44); other decisions are ignored. */
    function pressed(event: DecisionActivity) {
      const decision = event.decision;
      if (
        decision?.reason !== "dispatch" ||
        typeof decision.control !== "string" ||
        typeof decision.action !== "string" ||
        !decision.action
      )
        return;
      const slot = get().status?.slots.find(
        (entry) => entry.slot === event.slot,
      );
      set({
        keyPreview: {
          seq: ++pressSequence,
          device: slot ? slot.device : null,
          control: decision.control,
          action: decision.action,
        },
      });
    }

    function eventReceived(event: DaemonEvent) {
      if (get().connection.phase !== "connected") return;
      switch (event.topic) {
        case "input": {
          const input = event.data as InputEvent;
          if (!Number.isInteger(input.device)) return;
          if (input.kind === "removed") {
            const inputs = { ...get().inputs };
            delete inputs[input.device];
            set({ inputs });
            const previousStatus = get().status;
            if (previousStatus) {
              const devices = previousStatus.devices.filter(
                (device) => device.info.id !== input.device,
              );
              set({
                status: {
                  ...previousStatus,
                  devices,
                  slots: previousStatus.slots.filter(
                    (slot) => slot.device !== input.device,
                  ),
                },
                selectedDevice: chooseDevice(devices, get().selectedDevice),
              });
            }
            void refresh();
          } else if (input.kind === "added") void refresh();
          else if (input.kind === "remapped") {
            // SDL releases nothing when a mapping changes, so a control shown
            // as held may never send its release under the new mapping (IN-18).
            const inputs = { ...get().inputs };
            delete inputs[input.device];
            set({ inputs });
            void refresh();
          } else if (input.control) {
            const previous = get().inputs[input.device] ?? {
              buttons: {},
              axes: {},
            };
            let next: PadInput;
            if (input.kind === "axis" && typeof input.value === "number") {
              next = {
                ...previous,
                axes: { ...previous.axes, [input.control]: input.value },
              };
            } else if (input.kind === "down" || input.kind === "up") {
              next = {
                ...previous,
                buttons: {
                  ...previous.buttons,
                  [input.control]: input.kind === "down",
                },
              };
            } else return;
            set({ inputs: { ...get().inputs, [input.device]: next } });
          }
          break;
        }
        case "engine.activity": {
          const activity = event.data as EngineState;
          if (activity.kind === "decision") {
            const decision = event.data as DecisionActivity;
            pressed(decision);
            record(
              decisionRow(
                decision,
                ++activitySequence,
                get().status,
                Date.now(),
              ),
            );
            break;
          }
          if (activity.kind === "unrouted") {
            record(
              unroutedRow(
                event.data as UnroutedActivity,
                ++activitySequence,
                get().status,
                Date.now(),
              ),
            );
            break;
          }
          if (activity.kind === "state" && get().status) {
            const previousStatus = get().status as StatusResult;
            const slots = activity.slots ?? [];
            const devices = previousStatus.devices.map((device) => {
              // The power the event lists for this controller replaces the one
              // status.get gave; a controller it does not list keeps its own.
              const reading = Array.isArray(activity.batteries)
                ? activity.batteries.find(
                    (entry) => entry.device === device.info.id,
                  )
                : undefined;
              const battery = reading ? batteryOf(reading) : device.battery;
              if (device.status === "ignored" || device.status === "unmapped")
                return battery === device.battery
                  ? device
                  : { ...device, battery };
              const slot = slots.find(
                (entry) => entry.device === device.info.id,
              );
              return {
                ...device,
                slot: slot?.slot ?? -1,
                status: slot ? "mapped" : "waiting",
                ...(battery ? { battery } : {}),
              };
            });
            set({
              status: {
                ...previousStatus,
                paused: activity.paused,
                capturing: activity.capturing,
                suspended: activity.suspended,
                localDryRun: activity.localDryRun ?? previousStatus.localDryRun,
                sendToOwnWindow:
                  activity.sendToOwnWindow ?? previousStatus.sendToOwnWindow,
                slots,
                devices,
              },
            });
          }
          break;
        }
        case "script.status":
          set({ scripts: Array.isArray(event.data) ? event.data : [] });
          break;
        case "profile.changed": {
          // kind: "active" (a switch or reload), "saved", "deleted" or
          // "config" (config.json was written).
          const change = (event.data ?? {}) as {
            kind?: unknown;
            revision?: unknown;
            backup?: unknown;
            path?: unknown;
            reset?: unknown;
          };
          if (change.kind === "config")
            configChanged(
              typeof change.revision === "string" ? change.revision : "",
            );
          else if (change.kind === "configRepaired") {
            // The daemon repaired config.json while running (PRO-29): no
            // profile changed, the file did.
            if (
              typeof change.backup === "string" &&
              change.backup &&
              typeof change.path === "string" &&
              typeof change.reset === "boolean" &&
              !confirmedRepairs.has(change.backup)
            )
              set({
                configRepair: {
                  backup: change.backup,
                  path: change.path,
                  reset: change.reset,
                },
              });
            configChanged(
              typeof change.revision === "string" ? change.revision : "",
            );
          } else {
            // The editor compares the open profile's revision.
            set({ profileChanges: get().profileChanges + 1 });
            void loadProfiles();
          }
          void refresh();
          break;
        }
        case "app.foreground": {
          const app = event.data as Partial<ForegroundApp> | null;
          if (
            !app ||
            app.own === true ||
            typeof app.exe !== "string" ||
            typeof app.path !== "string" ||
            !app.exe
          )
            return;
          const seen: RecentApp = {
            exe: app.exe,
            path: app.path,
            elevated: app.elevated === true,
          };
          // Windows' own programs (search, Start) are no programs to bind:
          // the daemon leaves them out of apps.recent, and so does this
          // live list. A window capture waits for the next program, so
          // pressing Win to type a program's name does not bind the
          // search box.
          if (app.system === true) return;
          liveApps?.push(seen);
          set({ recentApps: noteRecent(get().recentApps, seen) });
          const capture = get().capture;
          if (capture) {
            clearTimeout(captureTimer);
            set({
              capture: null,
              captured: {
                target: capture.target,
                app: { ...seen, own: false },
              },
            });
          }
          break;
        }
        case "output.preview": {
          const preview = event.data as OutputPreview;
          if (typeof preview.chord === "string" && preview.chord)
            set({ preview });
          break;
        }
        case "log": {
          const row = event.data as LogEntry;
          if (typeof row.sequence !== "number" || !row.record) return;
          if (!get().logs.some((entry) => entry.sequence === row.sequence)) {
            set({ logs: [...get().logs, row].slice(-LOG_LIMIT) });
          }
          break;
        }
        case "dropped": {
          const n = (event.data as { n?: number }).n ?? 0;
          set({
            inputs: {},
            preview: null,
            keyPreview: null,
            dropped: get().dropped + n,
          });
          void refresh();
          break;
        }
      }
    }

    async function action(run: (isCurrent: () => boolean) => Promise<void>) {
      if (get().busy || get().connection.phase !== "connected") return;
      const epoch = connectionEpoch;
      set({ busy: true, error: "" });
      try {
        await run(() => epoch === connectionEpoch);
      } catch (error) {
        if (epoch === connectionEpoch) set({ error: message(error) });
      } finally {
        if (epoch === connectionEpoch) set({ busy: false });
      }
    }

    interface Operation {
      isCurrent(): boolean;
      /** Shows the save pill: this operation writes a file. */
      saving(): void;
    }
    const check = (op: Operation) => {
      if (!op.isCurrent()) throw new Stale();
    };
    const done: LibraryOutcome = { ok: true, value: undefined };

    /**
     * Queues a library operation. A failure sets the shell's error line,
     * unless inline() says the caller shows it, and resolves (never rejects)
     * with the classified failure.
     */
    function enqueue<T>(
      run: (op: Operation) => Promise<LibraryOutcome<T>>,
      inline?: () => boolean,
    ): Promise<LibraryOutcome<T>> {
      if (get().connection.phase !== "connected")
        return Promise.resolve(failed("disconnected"));
      const epoch = connectionEpoch;
      const isCurrent = () => epoch === connectionEpoch;
      let wrote = false;
      const op: Operation = {
        isCurrent,
        saving() {
          if (wrote || !isCurrent()) return;
          wrote = true;
          set({ librarySave: { state: "saving", seq: ++saveSequence } });
        },
      };
      const execute = async (): Promise<LibraryOutcome<T>> => {
        if (!isCurrent()) return failed("disconnected");
        try {
          return await run(op);
        } catch (error) {
          if (error instanceof Stale || !isCurrent())
            return failed("disconnected");
          return { ok: false, failure: libraryFailure(error) };
        }
      };
      // An idle queue starts the request at once, within the caller's event.
      const idle = queued === 0;
      queued++;
      set({ libraryBusy: true });
      const result = (idle ? execute() : queue.then(execute)).then(
        (outcome) => {
          if (!isCurrent()) return outcome;
          if (wrote)
            set({
              librarySave: {
                state: outcome.ok ? "saved" : "failed",
                seq: ++saveSequence,
              },
            });
          if (
            !outcome.ok &&
            outcome.failure.kind !== "disconnected" &&
            !inline?.()
          )
            set({ error: failureText(outcome.failure, get().language) });
          if (--queued === 0) {
            set({ libraryBusy: false });
            drained();
          }
          return outcome;
        },
      );
      queue = result.catch(() => {});
      return result;
    }

    /** Writes one change to config.json; see updateConfig. */
    async function writeConfig(
      op: Operation,
      mutate: (config: Config) => Config | null,
    ): Promise<LibraryOutcome> {
      let source = get().config;
      if (!source) return failed("noLibrary", get().configError);
      for (let attempt = 0; ; attempt++) {
        if (source.problem) return failed("brokenConfig", source.problem);
        let text: string | null;
        try {
          text = changeConfigText(source.text, mutate);
        } catch (error) {
          return failed("brokenConfig", errorInfo(error).message);
        }
        if (text === null) return done;
        op.saving();
        // Reads that started earlier must not replace what this write saves.
        ++configSequence;
        try {
          const saved = await api.putConfig(text, source.revision);
          check(op);
          // saved.problem says the file was written but not applied; the
          // file itself is fine, so the page keeps editing it.
          set({
            config: {
              ...source,
              text,
              revision: saved.revision,
              restartRequired: saved.restartRequired,
            },
            ...(saved.problem ? { error: saved.problem } : {}),
          });
          return done;
        } catch (error) {
          if (error instanceof Stale || !isRevisionConflict(error)) throw error;
          check(op);
          // Read the file again and apply the same change once more.
          const fresh = await loadConfig();
          check(op);
          if (attempt > 0) return failed("configConflict");
          source = fresh;
        }
      }
    }

    /** Renames a profile document, retrying once after a conflict. */
    async function rename(
      op: Operation,
      id: string,
      name: string,
    ): Promise<LibraryOutcome> {
      for (let attempt = 0; ; attempt++) {
        const document = await api.getProfile(id);
        check(op);
        if (document.problem) return failed("brokenProfile", document.problem);
        let text: string;
        try {
          text = renameProfileText(document.text, name);
        } catch (error) {
          return failed("brokenProfile", errorInfo(error).message);
        }
        if (text === document.text) return done;
        op.saving();
        try {
          await api.putProfile(id, text, document.revision);
          check(op);
          return done;
        } catch (error) {
          if (
            error instanceof Stale ||
            !isRevisionConflict(error) ||
            attempt > 0
          )
            throw error;
          check(op);
        }
      }
    }

    /**
     * Creates a profile under a fresh ID from name: an empty one, or a copy
     * of the library profile copyFrom (its whole folder, which the daemon
     * copies as it is: an `extends` in it stays); see createProfile and
     * copyProfile.
     */
    async function create(
      op: Operation,
      name: string,
      copyFrom?: string,
    ): Promise<LibraryOutcome<string>> {
      const problem = profileNameProblem(name, get().profiles);
      if (problem)
        return failed("error", nameProblemText(problem, get().language));
      const trimmed = name.trim();
      const taken = new Set(get().profiles.map((entry) => entry.id));
      let id = "";
      op.saving();
      for (let attempt = 0; ; attempt++) {
        id = slugId(trimmed, taken);
        try {
          if (copyFrom) await api.copyProfile(copyFrom, id);
          else await api.putProfile(id, blankProfileText(trimmed), "");
          check(op);
          break;
        } catch (error) {
          if (error instanceof Stale) throw error;
          const kind = libraryFailure(error).kind;
          // Another client took the ID: pick the next one.
          if ((kind !== "exists" && kind !== "profileConflict") || attempt >= 2)
            throw error;
          check(op);
          taken.add(id);
          await loadProfiles();
          check(op);
          for (const entry of get().profiles) taken.add(entry.id);
        }
      }
      try {
        if (copyFrom) {
          const renamed = await rename(op, id, trimmed);
          if (!renamed.ok) return renamed;
        }
      } finally {
        if (op.isCurrent()) await loadProfiles();
      }
      check(op);
      return { ok: true, value: id };
    }

    /**
     * Deletes a profile; see deleteProfile. The daemon refuses, in this
     * order: the active profile (-32012); the startup profile or running
     * rules (-32013 with "startup" or rule pointers); a linked folder, a
     * changed revision; config.json's references (-32013). So nothing is
     * changed until the daemon has refused, and a refusal this cannot
     * resolve puts back what it changed.
     */
    async function remove(op: Operation, id: string): Promise<LibraryOutcome> {
      // Whether this switched away from id, and the cleanup it wrote.
      let switched = false;
      let cleanedUp = false;
      let cleaned: { before: Config; after: Config } | null = null;
      const undo = async () => {
        const restore = cleaned;
        cleaned = null;
        if (restore)
          await writeConfig(op, (source) =>
            restoreProfileReferences(source, restore.before, restore.after, id),
          ).catch(() => undefined);
        if (switched) {
          switched = false;
          await api.activateProfile(id).catch(() => undefined);
          if (op.isCurrent()) await refresh();
        }
      };
      try {
        for (;;) {
          const revision =
            get().profiles.find((entry) => entry.id === id)?.revision ?? "";
          let failure: LibraryFailure;
          try {
            await api.deleteProfile(id, revision);
            check(op);
            return done;
          } catch (error) {
            if (error instanceof Stale) throw error;
            check(op);
            failure = libraryFailure(error);
          }
          if (failure.kind === "active" && !switched) {
            // Only a library profile can be switched back to if a later
            // step fails; a file padmated runs from the folder stays.
            await refresh();
            check(op);
            if (activeProfileId(get()) === id) {
              await api.activateProfile(
                nextActiveAfterDelete(get().profiles, id),
              );
              switched = true;
              check(op);
              await refresh();
              check(op);
              continue;
            }
          }
          if (failure.kind === "referenced" && !cleanedUp) {
            cleanedUp = true;
            // Clean the file as it is now, not as this window last read it.
            await loadConfig();
            check(op);
            const change: { pair: { before: Config; after: Config } | null } = {
              pair: null,
            };
            const written = await writeConfig(op, (source) => {
              const after = removeProfileReferences(source, id);
              // As written: without the retired members.
              change.pair = after && {
                before: source,
                after: withoutRetired(after),
              };
              return after;
            });
            if (!written.ok) {
              await undo();
              return written;
            }
            cleaned = change.pair;
            // Asked again even when the file had nothing left to remove: it
            // may have changed since; padmated's running rules refuse again.
            continue;
          }
          await undo();
          return { ok: false, failure };
        }
      } catch (error) {
        if (!(error instanceof Stale) && op.isCurrent()) await undo();
        throw error;
      }
    }

    /** Adds a profile file; see importProfile. */
    async function addArchive(
      op: Operation,
      options: ProfileImportOptions | undefined,
    ): Promise<LibraryOutcome<ProfileImportResult>> {
      // An answer writes at once; a first call shows the open dialog first.
      if (options) op.saving();
      try {
        const result = await api.importProfile(options);
        check(op);
        return { ok: true, value: result };
      } finally {
        if (op.isCurrent()) await loadProfiles();
      }
    }

    return {
      api,
      connection: { phase: "connecting" },
      status: null,
      scripts: [],
      config: null,
      configError: "",
      inputs: {},
      logs: [],
      activity: [],
      activityPaused: false,
      preview: null,
      keyPreview: null,
      dropped: 0,
      selectedDevice: null,
      language: savedLanguage(),
      labelDisplay: savedLabelDisplay(),
      overlay: savedOverlaySettings(),
      page: "overview",
      busy: false,
      error: "",
      settingsSaved: false,
      profileChanges: 0,
      dismissedConflicts: [],
      profiles: [],
      profilesLoaded: false,
      profilesError: "",
      recentApps: [],
      capture: null,
      captured: null,
      libraryBusy: false,
      librarySave: { state: "idle", seq: 0 },
      onboardingDone: savedOnboarding(),
      configRepair: null,
      startup: null,
      start() {
        const currentLifetime = ++lifetime;
        const initialEpoch = connectionEpoch;
        const unsubscribeConnection = api.onConnection((connection) => {
          if (currentLifetime === lifetime) connectionChanged(connection);
        });
        const unsubscribeEvent = api.onEvent((event) => {
          if (currentLifetime === lifetime) eventReceived(event);
        });
        pushOverlayPrefs();
        void api
          .getConnection()
          .then((connection) => {
            if (
              currentLifetime === lifetime &&
              initialEpoch === connectionEpoch
            )
              connectionChanged(connection);
          })
          .catch((error) => {
            if (
              currentLifetime === lifetime &&
              initialEpoch === connectionEpoch
            )
              connectionChanged({
                phase: "disconnected",
                message: message(error),
              });
          });
        return () => {
          ++lifetime;
          ++connectionEpoch;
          clearTimeout(captureTimer);
          unsubscribeConnection();
          unsubscribeEvent();
        };
      },
      async refresh() {
        await Promise.all([refresh(true), loadProfiles()]);
      },
      async refreshLibrary() {
        if (get().connection.phase !== "connected") return;
        await Promise.all([loadProfiles(), loadConfig().catch(() => {})]);
      },
      refreshRecentApps: loadRecentApps,
      async acknowledgeConfigRepair() {
        const repair = get().configRepair;
        if (!repair) return;
        confirmedRepairs.add(repair.backup);
        set({ configRepair: null });
        try {
          await api.markConfigRepairSeen(repair.backup);
        } catch {
          // The daemon still lists it; the next start shows it once more.
        }
      },
      refreshStartup: loadStartup,
      async setStartup(enabled, options) {
        const epoch = connectionEpoch;
        const sequence = ++startupSequence;
        try {
          const startup = await api.setStartup(enabled);
          if (epoch === connectionEpoch && sequence === startupSequence)
            set({ startup });
          return "";
        } catch (error) {
          const text = startupFailureText(errorInfo(error), get().language);
          if (epoch !== connectionEpoch) return text;
          if (!options?.inline?.()) set({ error: text });
          // What the entry is now, whatever the failure left.
          await loadStartup();
          return text;
        }
      },
      async identifyDevice(key) {
        if (get().connection.phase !== "connected") return null;
        const epoch = connectionEpoch;
        try {
          return await api.identifyDevice(key);
        } catch (error) {
          if (epoch === connectionEpoch)
            set({
              error: identifyFailureText(errorInfo(error), get().language),
            });
          return null;
        }
      },
      async reconnect() {
        connectionChanged({ phase: "connecting" });
        const epoch = connectionEpoch;
        try {
          await api.reconnect();
        } catch (error) {
          if (epoch === connectionEpoch)
            connectionChanged({
              phase: "disconnected",
              message: message(error),
            });
        }
      },
      togglePause: () =>
        action(async (isCurrent) => {
          const result = await (get().status?.paused
            ? api.resume()
            : api.pause());
          if (isCurrent() && get().status)
            set({ status: { ...(get().status as StatusResult), ...result } });
        }),
      releaseAll: () =>
        action(async (isCurrent) => {
          await api.releaseAll();
          if (isCurrent()) await refresh();
        }),
      reloadScripts: () =>
        action(async (isCurrent) => {
          const scripts = await api.reloadScripts();
          if (isCurrent()) set({ scripts });
        }),
      // A queued library write, not a busy action: the key gap saves when
      // its field loses focus, often to a click on the release or pause
      // button, which must still work.
      saveSettings: async (settings) => {
        const config = get().config;
        if (!config || config.problem) return;
        const epoch = connectionEpoch;
        // Every save writes only its own fields, so the retry after a
        // revision conflict keeps what changed elsewhere.
        const outcome = await enqueue((op) =>
          writeConfig(op, (source) => {
            let next: Config = source;
            if (
              settings.sendToOwnWindow !== undefined ||
              settings.eventGapMs !== undefined
            ) {
              const output = { ...source.output };
              if (settings.sendToOwnWindow !== undefined)
                output.sendToOwnWindow = settings.sendToOwnWindow;
              if (settings.eventGapMs !== undefined)
                output.eventGapMs = settings.eventGapMs;
              next = { ...next, output };
            }
            // A switch goes in feedback only when it is off (UI-27).
            for (const key of FEEDBACK_SWITCHES) {
              const on = settings[key];
              if (on !== undefined)
                next = setFeedbackSwitch(next, key, on) ?? next;
            }
            return next === source ? null : next;
          }),
        );
        if (epoch !== connectionEpoch || !outcome.ok) return;
        set({ settingsSaved: true });
        await refresh();
      },
      updateConfig: (mutate, options) =>
        enqueue((op) => writeConfig(op, mutate), options?.inline),
      activateProfile: (id) =>
        enqueue(async (op) => {
          await api.activateProfile(id);
          check(op);
          await refresh();
          return done;
        }),
      createProfile: (name, options) =>
        enqueue((op) => create(op, name), options?.inline),
      copyProfile: (id) =>
        enqueue(async (op) => {
          const { language, profiles } = get();
          const entry = profiles.find((profile) => profile.id === id);
          const name = copyName(
            entry ? profileName(entry, language) : id,
            language,
            profiles,
          );
          const created = await create(op, name, id);
          return created.ok
            ? { ok: true, value: { id: created.value, name } }
            : created;
        }),
      renameProfile: (id, name, options) =>
        enqueue(async (op) => {
          if (isDefaultProfile(id)) return failed("fixedRename");
          const problem = profileNameProblem(name, get().profiles, id);
          if (problem)
            return failed("error", nameProblemText(problem, get().language));
          try {
            return await rename(op, id, name.trim());
          } finally {
            if (op.isCurrent()) await loadProfiles();
          }
        }, options?.inline),
      deleteProfile: (id, options) =>
        enqueue(async (op) => {
          if (isDefaultProfile(id)) return failed("fixedDelete");
          op.saving();
          try {
            return await remove(op, id);
          } finally {
            if (op.isCurrent()) await loadProfiles();
          }
        }, options?.inline),
      exportProfile: (id, options) =>
        enqueue(async (op) => {
          const { language, profiles } = get();
          const entry = profiles.find((profile) => profile.id === id);
          try {
            const result = await api.exportProfile(
              id,
              entry ? profileName(entry, language) : id,
            );
            check(op);
            return { ok: true, value: result.saved };
          } catch (error) {
            if (error instanceof Stale) throw error;
            check(op);
            const failure = libraryFailure(error);
            // An export only reads: a refusal is about what it would read,
            // and data.path names the file (or only the folder).
            if (failure.kind === "unsupported") {
              const path = (
                errorInfo(error).data as { path?: unknown } | undefined
              )?.path;
              const file =
                typeof path === "string" && path.startsWith(`${id}/`)
                  ? path.slice(id.length + 1)
                  : "";
              return {
                ok: false,
                failure: {
                  ...failure,
                  kind: "notExportable",
                  ...(file ? { path: file } : {}),
                },
              };
            }
            if (errorInfo(error).code === LibraryErrorCode.invalidDocument)
              return {
                ok: false,
                failure: { ...failure, kind: "brokenProfile" },
              };
            return { ok: false, failure };
          }
        }, options?.inline),
      importProfile: (options, report) =>
        enqueue((op) => addArchive(op, options), report?.inline),
      startCapture(target) {
        if (get().connection.phase !== "connected") return;
        clearTimeout(captureTimer);
        const capture: WindowCapture = { target, startedAt: Date.now() };
        set({ capture, captured: null });
        captureTimer = setTimeout(() => {
          if (get().capture === capture) set({ capture: null });
        }, CAPTURE_TIMEOUT_MS);
      },
      cancelCapture() {
        clearTimeout(captureTimer);
        if (get().capture) set({ capture: null });
      },
      clearCaptured: () => set({ captured: null }),
      clearLibrarySave(seq) {
        const save = get().librarySave;
        if (save.seq === seq && save.state !== "saving")
          set({ librarySave: { state: "idle", seq } });
      },
      setLanguage(language) {
        set({ language });
        try {
          globalThis.localStorage?.setItem("padmate.language", language);
        } catch {
          /* Preference remains usable for this window. */
        }
        // The overlay's toasts are in this language.
        pushOverlayPrefs();
      },
      setLabelDisplay(labelDisplay) {
        set({ labelDisplay });
        try {
          globalThis.localStorage?.setItem(
            "padmate.labelDisplay",
            labelDisplay,
          );
        } catch {
          /* Preference remains usable for this window. */
        }
      },
      setOverlay(change) {
        const valid = validOverlayChange(change);
        const current = get().overlay;
        const next = { ...current, ...valid };
        if (
          next.toasts === current.toasts &&
          next.position === current.position &&
          next.opacity === current.opacity
        )
          return;
        set({ overlay: next });
        saveOverlaySettings(valid);
        pushOverlayPrefs();
      },
      setPage: (page) => set({ page }),
      finishOnboarding: markOnboarded,
      selectDevice: (selectedDevice) => set({ selectedDevice }),
      clearLogs: () => set({ logs: [] }),
      clearActivity() {
        pendingActivity = [];
        set({ activity: [] });
      },
      setActivityPaused: (activityPaused) => set({ activityPaused }),
      dismissError: () => set({ error: "" }),
      dismissConflict(name) {
        const dismissed = get().dismissedConflicts;
        if (!dismissed.includes(name))
          set({ dismissedConflicts: [...dismissed, name] });
      },
    };
  });
  return store;
}
export type PadmateStore = ReturnType<typeof createPadmateStore>;
