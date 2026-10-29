import type { KeyHint, OverlayPrefs } from "./overlay";
import type {
  UpdateDismissal,
  UpdateInstallResult,
  UpdateState,
} from "./update";

// "failed": the mapping service did not start (UI-45); "disconnected": the
// connection ended after it was made, or reconnecting did not reach the
// service.
export interface ConnectionState {
  phase: "connecting" | "connected" | "disconnected" | "failed";
  message?: string;
  pid?: number;
  protocol?: number;
  startedByUI?: boolean;
  // Why it did not start: with "failed", and while a retry starts it again
  // ("connecting").
  failure?: StartFailure;
}

// What padmated's --startup-report says (UI-45, ADR-0056), as Main checked
// it: unknown values become "internal" and "other", a missing text or
// number "" and 0. message is padmated's own English words; file and
// dataDir are absolute paths, for display only.
export type StartupReportKind =
  | "usage"
  | "instance"
  | "library"
  | "config"
  | "state"
  | "profile"
  | "log"
  | "ipc"
  | "sdl"
  | "output"
  | "internal";
export type StartupReportCause =
  | "invalid"
  | "missing"
  | "denied"
  | "refused"
  | "unavailable"
  | "other";
export interface StartupProblem {
  line: number;
  col: number;
  pointer: string;
  message: string;
}
// line, col, pointer and message repeat errors[0] when there is one.
export interface StartupReport extends StartupProblem {
  kind: StartupReportKind;
  cause: StartupReportCause;
  file: string;
  dataDir: string;
  errors: StartupProblem[];
}
// report: the service said why. exit: it quit before answering without a
// report (code is null when it was ended from outside). spawn: it could
// not be run. timeout: it did not answer.
export type StartFailure =
  | { reason: "report"; report: StartupReport }
  | { reason: "exit"; code: number | null }
  | { reason: "spawn" }
  | { reason: "timeout" };

export interface DeviceInfo {
  id: number;
  name: string;
  type: string;
  vendor: number;
  product: number;
  version: number;
  guid: string;
  driver: string;
  path: string;
  serial: string;
  gamepad: boolean;
  virtual: boolean;
  buttonMask?: number;
  axisMask?: number;
  buttonLabels?: Record<string, string>;
  capabilities?: {
    known: boolean;
    gyro: boolean;
    accelerometer: boolean;
    touchpads: number;
    rumble: boolean;
    triggerRumble: boolean;
    led: boolean;
    rgbLed: boolean;
    playerLed: boolean;
  };
}

// What a controller says about its power (IN-12), in the four states of the
// script API. percent is absent when the controller does not report a charge.
export type BatteryState = "wired" | "charging" | "discharging" | "unknown";

export interface DeviceBattery {
  state: BatteryState;
  percent?: number;
}

// One controller of the batteries in an engine.activity state event (IN-12):
// the power it reported, which the settings page shows, and whether it is in an
// announced low-battery episode (low), which the overlay tells on the edge into
// it. Only controllers that have read a power are listed. device is the
// connection ID, key the stable identity (a receiver's revival changes the
// first and keeps the second).
export interface BatteryReading {
  device: number;
  key: string;
  name: string;
  state: BatteryState;
  percent?: number;
  low: boolean;
}

// What a controller can show when it is identified (IN-13): it buzzes
// (rumble), flashes its color LED (led) or chases its player lights
// (player). All false: it can show nothing.
export interface DeviceIdentify {
  rumble: boolean;
  led: boolean;
  player: boolean;
}

// What devices.rawInfo says about a controller whose raw joystick events the
// mapping wizard watches (IN-17, IN-19): device is its DeviceKey, id the
// connection ID its input.raw events carry, guid the SDL GUID a mapping is
// written for, gamepad whether SDL reads it as a gamepad now. axisRest is the
// rest value of each axis (SDL's initial state), axes entries long.
export interface RawInfo {
  device: string;
  id: number;
  guid: string;
  gamepad: boolean;
  axes: number;
  buttons: number;
  hats: number;
  axisRest: number[];
  watching: boolean;
}

// One raw joystick element that changed (IN-17): the data of an input.raw
// event. value is 1 or 0 for a button, the position (-32768..32767) for an
// axis and the direction mask (up 1, right 2, down 4, left 8, 0 centered) for
// a hat. device is the connection ID.
export interface RawInputEvent {
  timeNs: number;
  device: number;
  kind: "button" | "axis" | "hat";
  index: number;
  value: number;
}

// Something to tell the user after a mapping is saved (devices.setMapping):
// "noBack" and "noStart" View or Menu is not mapped (the emergency combo
// needs them), "shadowed" SDL reads another mapping for the
// controller (the user's SDL_GAMECONTROLLERCONFIG wins), "notGamepad" SDL
// still reads it as a joystick. device is a connection ID, when the warning
// is about one controller.
export interface MappingWarning {
  code: string;
  device?: number;
}

// The answer to devices.setMapping and devices.resetMapping (IN-18). mapping
// is the string saved (the name "*", platform Windows), "" after a reset; keys
// are the config.json device keys written, since SDL applies a mapping to a
// GUID; changed is false when config.json already had it. applied and
// problem are config.put's.
export interface MappingResult {
  device: string;
  guid: string;
  mapping: string;
  keys: string[];
  changed: boolean;
  applied: boolean;
  problem?: string;
  revision?: string;
  notes: string[];
  warnings: MappingWarning[];
}

export interface DeviceEntry {
  info: DeviceInfo;
  key: string;
  slot: number;
  status: string;
  disabled: boolean;
  /**
   * The latest power the daemon read from the controller; absent until it has
   * read one. The settings page shows it on the controller's row (IN-12), and
   * the periodic state events keep it current.
   */
  battery?: DeviceBattery;
}

export interface SlotState {
  slot: number;
  device: number;
  key: string;
  name: string;
  profile: string;
  generation: number;
  layers: { base: string; overlays: string[]; color: string };
  latches: string[];
  mode: number;
  /**
   * What each control of the current layer does, while a button bound to the
   * key hints action is held (HUD-03); absent otherwise. Main reads it for the
   * overlay (main/overlay-hints.ts).
   */
  hints?: KeyHint[];
}

// The emergency combo of a connected controller (UI-19), as the daemon
// detects it: family is config.json's safety.panic key for it.
export interface DeviceCombo {
  device: number;
  family: string;
  buttons: string[];
  holdMs: number;
}

export interface StatusResult {
  version: string;
  protocol: number;
  dryRun: boolean;
  fakeInput: boolean;
  paused: boolean;
  capturing: boolean;
  suspended: string;
  profileId: string;
  slots: SlotState[];
  devices: DeviceEntry[];
  conflicts?: {
    /** "conflict": a program that is running; "steamInput": see classes. */
    kind: string;
    process: string;
    message: string;
    document: string;
    /**
     * Only for kind "steamInput" (IN-24, ADR-0071): the kinds of controller
     * whose Steam Input setting may manage a connected pad, in this order and
     * each once: "xbox", "playstation", "switch", "steamHardware".
     */
    classes?: string[];
  }[];
  localDryRun?: boolean;
  sendToOwnWindow?: boolean;
  combos?: DeviceCombo[];
  // A repair of config.json the user has not been told of yet (PRO-29).
  configRepair?: ConfigRepair;
}

// The daemon found errors in config.json, backed the file up and repaired
// it (PRO-29). backup is the copy's file name in the data folder
// (config.broken-YYYYMMDD-HHMMSS.json, with -2, -3 when taken); path is its
// absolute path, for display only. reset: the file could not be parsed at
// all and the defaults replaced it; otherwise only the members with errors
// were reset.
export interface ConfigRepair {
  backup: string;
  path: string;
  reset: boolean;
}

// Start at sign-in (UI-19, ADR-0052): the daemon's own HKCU Run entry.
// enabled: Run has a padmate value; current: it is the command this daemon
// writes (not another copy or mode of padmate). mode is how that command
// starts padmate, the daemon's own mode; it says nothing about another
// padmate's value. reason says why the entry is not available: a
// development or test build, or no system support.
export interface StartupState {
  available: boolean;
  enabled: boolean;
  current: boolean;
  mode: "inject" | "dry-run";
  reason?: "development" | "unsupported";
}

export interface EngineControlResult {
  paused: boolean;
  capturing: boolean;
}

export interface ConfigGetResult {
  text: string;
  revision: string;
  diagnostics?: unknown[];
  problem?: string;
  restartRequired?: boolean;
}

export interface ConfigPutResult {
  revision: string;
  applied: boolean;
  problem?: string;
  restartRequired: boolean;
}

export interface ScriptStatusEntry {
  slot: number;
  device: number;
  key: string;
  profileId: string;
  generation: number;
  script: {
    state: string;
    entry?: string;
    api?: string;
    paused: boolean;
    pending: boolean;
    wallStopped: boolean;
    lastError?: string;
    restartCount: number;
    permanent: boolean;
    metadata: ScriptMetadata;
    // The runtime's macros with a problem (SCR-50): the latest error of
    // each, and the macros a CPU interruption disabled until the file
    // changes. lastError has only the legacy script's errors.
    macros?: MacroStatus[];
  };
}

export interface MacroStatus {
  file: string;
  error?: string;
  disabled?: boolean;
}

// A control and gesture of a profile that run a script, as macros.list
// names them: canonical control names, every layer and common merged.
// profileName is the profile's name, or its ID when it has none.
export interface MacroUse {
  profile: string;
  profileName: string;
  control: string;
  gesture: string;
}
// A file of the script library (SCR-51, ADR-0057), shared by every profile.
// name is file without the extension. actions is how many actions run it,
// in every layer and common of every profile: as many as deleting it
// clears, where usedBy merges them (a fixture without it counts usedBy).
// problem is why it does not build or load; invalidName marks a file whose
// name the daemon refuses, which can only be renamed or deleted.
export interface MacroInfo {
  file: string;
  name: string;
  // When the file was created, in Unix milliseconds: the scripts page lists
  // by it. Absent when the platform does not say.
  created?: number;
  usedBy: MacroUse[];
  actions?: number;
  problem?: string;
  invalidName?: boolean;
}
// A script that bindings run but the library lacks.
export interface MissingMacro {
  file: string;
  usedBy: MacroUse[];
  actions?: number;
}
// A profile's legacy script (profile.json's script.entry), which runs
// unchanged and is edited or removed with its profile.
export interface LegacyScript {
  profile: string;
  profileName: string;
  entry: string;
}
// macros.list: the whole library, read from the profile.json files on
// disk, not the running ones. legacy is every profile's legacy script,
// sorted by profile ID (always an array from Main; optional for fixtures).
export interface MacroList {
  macros: MacroInfo[];
  missing: MissingMacro[];
  legacy?: LegacyScript[];
}

export interface ProfileDiagnostic {
  severity: "error" | "warning";
  pointer: string;
  offset: number;
  line: number;
  col: number;
  message: string;
}
export interface ProfileEntry {
  id: string;
  name?: string;
  // The fixed default profile (PRO-28): its ID is always "default".
  default?: boolean;
  // When the profile's folder was created, in Unix milliseconds: the
  // profiles page lists by it. Absent when the platform does not say.
  created?: number;
  revision?: string;
  // The parent this profile's text names (PRO-19, ADR-0070): the library ID
  // of the profile it is based on. Absent for a profile that stands alone.
  extends?: string;
  diagnostics?: ProfileDiagnostic[];
  problem?: string;
}
// What a profile is based on: parent is the library ID its text names, chain
// the IDs from there up as far as the library goes, nearest first.
export interface ProfileExtends {
  parent: string;
  chain: string[];
}
export interface ProfileDocument {
  id: string;
  // The profile's own text, never the merge with its parents.
  text: string;
  revision: string;
  extends?: ProfileExtends;
  diagnostics: ProfileDiagnostic[];
  problem?: string;
}
// A member of a merged profile that a parent wrote (PRO-19): pointer is a
// JSON pointer into ProfileResolution.text, profile the library ID of the
// profile that wrote it.
export interface ProfileOrigin {
  pointer: string;
  profile: string;
}
// profiles.resolve: the profile with its parents merged in. text has no
// "extends"; for a profile that is based on nothing, or whose chain has a
// problem, it is the profile's own text. inherited lists what the parents
// wrote (never what the profile writes itself).
export interface ProfileResolution {
  id: string;
  text: string;
  revision: string;
  extends?: ProfileExtends;
  inherited: ProfileOrigin[];
  diagnostics: ProfileDiagnostic[];
  problem?: string;
}
export interface ProfileSaveResult {
  revision: string;
  applied: boolean;
  problem?: string;
}
export interface ProfileValidation {
  valid: boolean;
  diagnostics: ProfileDiagnostic[];
}
export interface ScriptParamInfo {
  kind: string;
  label?: string;
  default: unknown;
  min?: number;
  max?: number;
  step?: number;
  maxLength?: number;
  options?: string[];
  layerOptions?: string[];
}
export interface ScriptRegistration {
  id: number;
  control: string;
  gesture: string;
  scope: string;
  observe: boolean;
  claim: boolean;
  priority: number;
  label?: string;
  timing: Record<string, number>;
}
export interface ScriptMetadata {
  runtime: number;
  params?: Record<string, ScriptParamInfo>;
  actions?: {
    name: string;
    label?: string;
    args: Record<string, ScriptParamInfo>;
  }[];
  registrations?: ScriptRegistration[];
  continuous?: { id: number; kind: string; control?: string }[];
}
export interface ScriptDescription {
  entry: string;
  api: string;
  metadata: ScriptMetadata;
}
/** What one level (a layer, common, or the function set) does with a control. */
export interface LayerExplanation {
  layer: string;
  active: boolean;
  selected: boolean;
  binding?: string;
  label?: string;
  block: boolean;
  latch: boolean;
  resolution: string;
  actions: Record<string, string>;
}
/**
 * The function set (MAP-27, ADR-0071, ADR-0075), shaped like a layer: layer is
 * the level's name in decisions, "function:<function key>" (such as
 * "function:start"); resolution also has "inactive" (the key is not held),
 * "fallthrough" (held, but the set does not bind the control) and "ignored"
 * (the control is the function key, which has no binding).
 */
export interface FunctionSetExplanation extends LayerExplanation {
  /** The function key now in effect. */
  key: "back" | "start" | "guide";
  /** Whether the profile has a function set at all. */
  hasSet: boolean;
}
export interface ProfileExplanation {
  control: string;
  profileId: string;
  name: string;
  declarativeOnly: boolean;
  /** The function key now in effect (config.json functionKey), the same for every slot. */
  functionKey: "back" | "start" | "guide";
  /** The control is itself the function key: it has no binding to explain. */
  isFunctionKey: boolean;
  slots: {
    slot: number;
    device: number;
    layers: LayerExplanation[];
    /** The function key is held on the slot now; false for a library profile. */
    functionHeld: boolean;
    functionSet: FunctionSetExplanation;
  }[];
}
// The latency statistics of the diagnostics page (DMN-15, ADR-0071): the time
// from padmate receiving a controller's input edge to the return of the Send
// call, over the controller's latest samples, in microseconds.
export interface LatencyDevice {
  /** The stable key (DeviceEntry.key); id is the connection ID. */
  device: string;
  id: number;
  name: string;
  samples: number;
  p50: number;
  p90: number;
  p99: number;
  max: number;
  /** The daemon's verdict: enough samples and p99 above warnP99Us. */
  warn: boolean;
}
export interface LatencyResult {
  devices: LatencyDevice[];
  /** The daemon only prints its keys (--dry-run): the samples are of nothing sent. */
  dryRun: boolean;
  warnP99Us: number;
  warnMinSamples: number;
}
export interface KeyboardCaptureEvent {
  session: number;
  active: boolean;
  chord?: string;
  physicalChord?: string;
  vk?: number;
  scanCode?: number;
  extended?: boolean;
  reason?: string;
}
export interface UIErrorData {
  message: string;
  code?: number;
  data?: unknown;
}
// Daemon error codes (UIErrorData.code) that library management handles.
export const LibraryErrorCode = {
  // A missing profile.
  notFound: -32004,
  // A profile.json that does not validate, such as one profiles.export
  // refuses; data is its diagnostics.
  invalidDocument: -32602,
  // data.revision is the stored revision.
  revisionConflict: -32009,
  // profiles.delete of the active profile: activate another one first.
  profileActive: -32012,
  // data.references lists config.json pointers such as "rules/3" that
  // still select the profile, and "startup" when the daemon was started
  // with it, which only a restart releases.
  profileReferenced: -32013,
  // profiles.copy onto an existing ID.
  profileExists: -32014,
  // A link, special file or oversized folder; data.path names it. Also a
  // linked profile folder, or an import name that is a folder or link.
  profileUnsupported: -32015,
  // A script with the same name exists (macros.create, macros.rename,
  // macros.import without replace): data is { exists, name, replaceable }.
  // Main turns an import's into MacroImportResult { exists: true }.
  scriptExists: -32016,
  // startup.set without an entry: a development or test build, or no
  // system support; data is the StartupState.
  startupUnavailable: -32017,
  // The fixed default profile (PRO-28): profiles.delete of it, or a
  // profiles.put that changes its name; data.op is "delete" or "rename".
  profileFixed: -32018,
  // A macro the macros folder does not list under exactly this name, or a
  // legacy entry that does not exist: data is { file }.
  macroNotFound: -32019,
  // profiles.import (PRO-20) of a file it does not accept; data.reason is
  // an ArchiveProblem.
  archiveInvalid: -32020,
  // profiles.import whose name another profile goes by; data is {id, name}
  // of that profile. Main turns it into ProfileImportResult { exists: true }.
  profileNameTaken: -32021,
  // profiles.import of an archive with a script whose name the library has
  // with other content, and no answer (macros) yet; data.conflicts lists
  // { file, existing, replaceable }. Main turns it into
  // ProfileImportResult { conflict: true }.
  macroConflict: -32022,
  // profiles.delete of a profile that others are based on, when one of them
  // cannot be written out whole first (PRO-19): data is { id, reason,
  // diagnostics? }, id the profile that cannot, reason "invalid" (it has
  // errors of its own) or "script" (it inherits its parent's script).
  profileChild: -32023,
  // profiles.importAmgp (PRO-21) of a file that is not an AntiMicroX profile
  // padmate imports; data.reason is an AmgpProblem, and the message says why in
  // Chinese, for the user. Nothing was written.
  amgpInvalid: -32024,
  // devices.setMapping refused the mapping (IN-18); data.reason is "syntax" (not
  // a mapping string), "mapping" (binds nothing, or its GUID is not the
  // controller's), "guid" (the controller has none) or "config" (config.json
  // would not validate with it).
  mappingInvalid: -32025,
  // devices.rawInfo, setMapping or resetMapping that this input cannot do: a
  // fake input, or a controller SDL reads as a gamepad that is not captured
  // (capture.start first).
  mappingUnsupported: -32026,
} as const;
// UIErrorData.data.reason of archiveInvalid: "format" not an exported
// padmate profile or a damaged one, "version" written by a newer padmate,
// "entry" an unsafe or unsupported entry, "limit" beyond the import
// limits, "encoding" a script or JSON file that is not UTF-8, "profile"
// a profile.json that does not validate.
export type ArchiveProblem =
  | "format"
  | "version"
  | "entry"
  | "limit"
  | "encoding"
  | "profile";
// Editor calls reject with UIErrorData (plain cloned data, not Error), because
// Electron's contextBridge drops custom properties from Error instances.
export type UIResult<T> =
  | { ok: true; value: T }
  | { ok: false; error: UIErrorData };

export interface LogEntry {
  sequence: number;
  record: Record<string, unknown>;
  truncated?: boolean;
}

export interface LogHistory {
  entries: LogEntry[];
  oldest: number;
  next: number;
}

export interface DaemonEvent {
  topic:
    | "input"
    | "engine.activity"
    | "log"
    | "script.status"
    | "profile.changed"
    | "output.preview"
    | "app.foreground"
    // The daemon is stopping (DMN-22); Main handles it, the renderer never
    // gets it.
    | "daemon.exiting"
    // A script's HUD calls (SCR-22): data is `{ kind: "toast", slot, text,
    // ms }`. Main shows it in the overlay; the renderer never gets it.
    | "hud"
    // The raw joystick events of a controller devices.rawInfo asked for (IN-17):
    // data is `{ timeNs, device, kind, index, value }`.
    | "input.raw"
    | "dropped";
  data: unknown;
}

// A program the Start Menu lists (UI-46): name is its shortcut's name, exe
// the file name of its executable and path that executable's absolute local
// path. Main reads them; the renderer supplies no path.
export interface InstalledApp {
  name: string;
  exe: string;
  path: string;
}
// The .exe the user chose in the file dialog (UI-46).
export interface BrowsedApp {
  exe: string;
  path: string;
}
// UIErrorData.data.reason when browseApp refuses the chosen file: "path"
// is not an .exe on a local drive. The message is English.
export type AppBrowseProblem = "path";

// A foreground executable the daemon has seen (PRO-16). Identity only: the
// daemon never reads window titles.
export interface RecentApp {
  exe: string;
  path: string;
  elevated: boolean;
}
// Data of the "app.foreground" event. own marks a registered padmate UI
// window, which a window capture helper skips. system marks a program of
// Windows itself (below the Windows directory, such as SearchHost.exe),
// which the recent apps leave out; the daemon omits it when false.
export interface ForegroundApp extends RecentApp {
  own: boolean;
  system?: boolean;
}
export interface ProfileActivation {
  profileId: string;
  generation: number;
}

// Files are named relative to the script library; no path reaches the
// renderer.
export interface MacroCreated {
  file: string;
}
// exists: a script with this name is already in the library. With a token
// (the same extension) ask the user, then call importMacro({ replace: true,
// token }), which writes the file Main already read under the existing
// name; the token expires after two minutes. Without one (another
// extension) it cannot be replaced.
export type MacroImportResult =
  | { canceled: true }
  | { exists: true; name: string; token?: string }
  | { file: string; replaced: boolean };
export interface MacroImportOptions {
  replace: true;
  token: string;
}
// What an edit of the library (rename, delete) did to one profile: its
// revision afterwards and how many actions changed.
export interface ProfileEdit {
  revision: string;
  bindings: number;
}
// An edit of every profile that uses a script: the profiles it wrote and
// how many actions changed in all of them.
export interface MacroEdit {
  bindings: number;
  profiles: Array<ProfileEdit & { id: string }>;
}
export interface MacroRenamed extends MacroEdit {
  file: string;
}
// UIErrorData.data.reason when Main refuses the chosen file before asking
// the daemon: "type" not a .ts/.js file, "declaration" a .d.ts file, "name"
// no usable script name after sanitising (nothing left, or a Windows device
// name), "size" over 1 MiB, "encoding" not UTF-8, "read" the file could not be
// read (removed, locked or offline), "expired" an unknown or expired replace
// token. The message is English and never names a path; show a translated
// text.
export type ScriptImportProblem =
  | "type"
  | "declaration"
  | "name"
  | "size"
  | "encoding"
  | "read"
  | "expired";

// UIErrorData.data.reason of amgpInvalid (amgp.Error's): "size" over 1 MiB,
// "encoding" not UTF-8, "xml" not well-formed, "limit" too many elements or
// too deep, "doctype" a DOCTYPE or other declaration, "root" not an
// AntiMicroX profile, "joystick" a raw-joystick profile, "version" too old.
export type AmgpProblem =
  | "size"
  | "encoding"
  | "xml"
  | "limit"
  | "doctype"
  | "root"
  | "joystick"
  | "version";
// How much of an AntiMicroX construct reached the padmate profile (PRO-21):
// "dropped" is not in the profile at all, "approximate" is something close to
// it, "info" lost nothing and says what was done.
export type AmgpSeverity = "dropped" | "approximate" | "info";
// One line of the report of an AntiMicroX import. set, control and slot say
// where in the file (set and slot count from 1, control is the padmate control
// or the AntiMicroX element); message is the daemon's reason, in Chinese.
export interface AmgpReportLine {
  severity: AmgpSeverity;
  code: string;
  set?: number;
  control?: string;
  slot?: number;
  message: string;
}
// A profile made from an AntiMicroX .amgp or .xml file (PRO-21): a new
// profile and what the import dropped or approximated. Main read the file; the
// daemon converts it.
export interface AmgpImported {
  amgp: true;
  id: string;
  name: string;
  revision: string;
  applied: boolean;
  problem?: string;
  report: AmgpReportLine[];
}
// Backup of one profile (PRO-20): a .padmate file, which Main reads and
// writes; the daemon makes and checks the archive. The renderer supplies
// no path.
export interface ProfileImported {
  id: string;
  name: string;
  revision: string;
  applied: boolean;
  problem?: string;
  // What became of the archive's scripts in the library (SCR-51): file is
  // the name in the archive, name the one in the library; same: the library
  // had it with these contents; replaced: it overwrote the library's.
  macros?: Array<{
    file: string;
    name: string;
    same?: boolean;
    replaced?: boolean;
  }>;
}
// A script of an imported archive whose name the library has with other
// contents. existing is the library's file name; replaceable: the same
// extension, so 替换 can overwrite it.
export interface ScriptClash {
  file: string;
  existing: string;
  replaceable: boolean;
}
// exists: the profile id already goes by the file's name. Ask the user,
// then call importProfile with the token and either replace (that
// profile's ID) or a new name. conflict: scripts of the file clash with the
// library's (nothing is written yet): ask 替换 or 都保留, then call
// importProfile with the token and macros; the profile answer given before
// is kept. A token works once and expires after two minutes.
export type ProfileImportResult =
  | { canceled: true }
  | { exists: true; id: string; name: string; token: string }
  | { conflict: true; scripts: ScriptClash[]; token: string }
  | AmgpImported
  | ProfileImported;
export type ProfileImportOptions =
  | { token: string; replace: string }
  | { token: string; name: string }
  | { token: string; macros: "replace" | "keep" };
// UIErrorData.data.reason when Main refuses before or after asking the
// daemon: "size" a file over 4 MiB, "read" the chosen file could not be
// read, "write" the export could not be written, "expired" an unknown or
// expired token. The message is English and never names a path.
export type ProfileFileProblem = "size" | "read" | "write" | "expired";

// Renderer capabilities are intentionally concrete. No channel, executable,
// pipe, PID or RPC method is supplied by renderer code.
export interface PadmateAPI {
  getConnection(): Promise<ConnectionState>;
  onConnection(listener: (state: ConnectionState) => void): () => void;
  getStatus(): Promise<StatusResult>;
  getDevices(): Promise<DeviceEntry[]>;
  // IN-13: the controller with this key (DeviceEntry.key) buzzes and flashes
  // for at most a second and a half, so that the user can tell which it is,
  // whatever the profile and the pause say. The answer says which cues it
  // gave. Rejects with code -32004 when it is not connected any more.
  identifyDevice(key: string): Promise<DeviceIdentify>;
  // The mapping wizard (IN-17, IN-18, IN-19, ADR-0072). device is a
  // connection ID (DeviceEntry.info.id as text) or a key; use the ID for a
  // controller SDL does not know, whose keys are all the same.
  // rawWatchStart asks for the controller's raw joystick events: they arrive
  // as "input.raw" events (RawInputEvent) through onEvent until rawWatchStop.
  // A controller SDL reads as a gamepad must be captured first
  // (padCaptureStart), or it rejects with code -32026. Main ends the watch
  // itself when the page reloads or the connection ends.
  rawWatchStart(device: string): Promise<RawInfo>;
  rawWatchStop(device: string): Promise<void>;
  // Saves an SDL mapping string for the controller's GUID in config.json and
  // applies it at once; rejects with code -32025 and data { reason } for a
  // mapping the daemon refuses. resetMapping takes the GUID's mapping away.
  setMapping(device: string, mapping: string): Promise<MappingResult>;
  resetMapping(device: string): Promise<MappingResult>;
  pause(): Promise<EngineControlResult>;
  resume(): Promise<EngineControlResult>;
  releaseAll(): Promise<EngineControlResult>;
  getConfig(): Promise<ConfigGetResult>;
  listProfiles(): Promise<ProfileEntry[]>;
  getProfile(id: string): Promise<ProfileDocument>;
  // PRO-19: profile id with its parents merged in, to show what it inherits.
  // The profile's own file is getProfile's; this one is read-only.
  resolveProfile(id: string): Promise<ProfileResolution>;
  putProfile(
    id: string,
    text: string,
    revision: string,
  ): Promise<ProfileSaveResult>;
  validateProfile(text: string): Promise<ProfileValidation>;
  // null disables mapping. The daemon may later switch again by its
  // foreground rules.
  activateProfile(id: string | null): Promise<ProfileActivation>;
  // revision is the one profilesList reports: the stored profile.json's,
  // or "" for a profile.json that is missing or unreadable. The folder is
  // deleted for good, scripts included. A rejection keeps the daemon's code
  // and data, e.g. the rules that still reference the profile.
  deleteProfile(id: string, revision: string): Promise<void>;
  // Copies a library profile to a new, unused library ID.
  copyProfile(from: string, to: string): Promise<{ revision: string }>;
  // PRO-20: Main asks the daemon for the profile as an archive, then shows
  // the save dialog with name (sanitised) as the file name; saved is false
  // when the user cancels.
  exportProfile(id: string, name: string): Promise<{ saved: boolean }>;
  // PRO-20: without options Main shows the open dialog, reads the chosen
  // .padmate file (at most 4 MiB) and adds it as a new profile. With the
  // token from { exists: true } it adds that same file again, replacing
  // a profile or under a new name. A .amgp or .xml file of AntiMicroX (PRO-21)
  // is converted instead (at most 1 MiB): the answer is { amgp: true, ... }
  // with the report, and a file it does not import rejects with code -32024.
  importProfile(options?: ProfileImportOptions): Promise<ProfileImportResult>;
  // Newest first, at most 12, excluding padmate's own windows; memory only.
  getRecentApps(): Promise<RecentApp[]>;
  // A bound app's icon on the profiles page (UI-42): a PNG data URL that
  // Main reads from the executable at path, which must be a local drive
  // path to an .exe; "" when it has none. Nothing reads a window or title.
  getAppIcon(path: string): Promise<string>;
  // The path of a rule's program when the rule names only its file name
  // (UI-42): Main takes the Start Menu program with that file name, else
  // the file an App Paths key of the registry names, else a path the
  // registry's MuiCache remembers the program was started from; "" when
  // none is found. exe must be a bare .exe file name; the answer is a local
  // drive path to an existing .exe, which getAppIcon accepts.
  resolveAppPath(exe: string): Promise<string>;
  // 从已安装的软件中选择 (UI-46): the programs the Start Menu lists whose
  // shortcuts lead to an existing local .exe, by name; a few hundred at
  // most.
  listInstalledApps(): Promise<InstalledApp[]>;
  // 浏览… (UI-46): Main shows the open dialog for an .exe; null when the
  // user cancels. Rejects with data { reason: AppBrowseProblem } for a file
  // it cannot use.
  browseApp(): Promise<BrowsedApp | null>;
  explainProfile(control: string, id?: string): Promise<ProfileExplanation>;
  // The diagnostics page (DMN-15, UI-22): the latency of the keys padmate
  // sent, per controller; the page asks once a second while it is open.
  // device (a connection ID or a key) narrows both calls to one controller;
  // without it getLatency lists the controllers that have a slot and
  // resetLatency forgets all samples.
  getLatency(device?: string): Promise<LatencyResult>;
  resetLatency(device?: string): Promise<{ reset: boolean }>;
  getScriptDescription(id?: string): Promise<ScriptDescription>;
  getScriptRegistrations(device?: string): Promise<ScriptStatusEntry[]>;
  // The scripts page (SCR-50, SCR-51, UI-41): the script library shared by
  // every profile, for the page and the key drawer. Like every script call
  // it never rejects with a path: a failure of the file system, such as a
  // full disk, is code -32098 (the daemon's system error) with a fixed
  // message, and the daemon log has the details.
  listMacros(): Promise<MacroList>;
  // Creates "<name>.ts" from the template, never replacing a file; name is
  // without the extension. A refused name rejects with data { kind }.
  createMacro(name: string): Promise<MacroCreated>;
  // Main shows the open dialog, reads the chosen .ts/.js file (at most
  // 1 MiB of UTF-8) and names it after the file, sanitised; the renderer
  // supplies no path. A second call with the token from { exists: true }
  // replaces the existing script with that same file.
  importMacro(options?: MacroImportOptions): Promise<MacroImportResult>;
  // file is the exact name macros.list gives; to has the same extension.
  // The actions that run it follow, in every profile. Renaming, deleting
  // and detaching write profile.json when an action changes, so save the
  // mapping page first.
  renameMacro(file: string, to: string): Promise<MacroRenamed>;
  // Removes the actions that run the script in every profile, then its
  // file; a file already gone only loses its references.
  deleteMacro(file: string): Promise<MacroEdit>;
  // Removes the legacy script of profile id: profile.json's script section
  // and every script action. Its files stay.
  detachScript(id: string): Promise<ProfileEdit>;
  // Opens a script of the library, or the legacy entry of profile id, in
  // VS Code when it is installed, otherwise in Notepad; opened is false
  // when neither starts.
  editMacro(file: string): Promise<{ opened: boolean }>;
  editScript(id: string): Promise<{ opened: boolean }>;
  captureStart(): Promise<number>;
  captureStop(session: number): Promise<void>;
  onCapture(listener: (event: KeyboardCaptureEvent) => void): () => void;
  // Pauses the mapping while a new emergency combo is pressed on the
  // controller; input events keep coming. Main also stops it when the
  // window loses focus, the session locks or sleeps, the window closes or
  // reloads, or the daemon disconnects. With stays (the mapping wizard) the
  // window losing focus, a lock and a sleep do not stop it: the mapping stays
  // paused until padCaptureStop, the window closing or reloading, or the
  // daemon disconnecting. Asking again while it is on confirms it; the window
  // has to be in front to ask.
  padCaptureStart(stays?: boolean): Promise<void>;
  padCaptureStop(): Promise<void>;
  // Start at sign-in (UI-19). The daemon registers its own executable and
  // arguments; the renderer passes neither. setStartup rejects with code
  // -32017 in a development build.
  getStartup(): Promise<StartupState>;
  setStartup(enabled: boolean): Promise<StartupState>;
  // Opens the Game Bar page of Windows Settings (ms-settings:gaming-gamebar,
  // the only address Main opens), where the user turns off "Allow your
  // controller to open Game Bar" when the Xbox button is the function key
  // (UI-30, ADR-0075). It takes no address.
  openGameBarSettings(): Promise<void>;
  // Closing the window (UI-43): Main asks the renderer once, which commits
  // the field being typed in, waits for its saves and then calls
  // readyToClose. Main closes after a few seconds anyway.
  onClosing(listener: () => void): () => void;
  readyToClose(): Promise<void>;
  exportCheatSheet(
    format: "markdown" | "png",
    content: string,
  ): Promise<{ saved: boolean }>;
  putConfig(text: string, revision: string): Promise<ConfigPutResult>;
  getScriptStatus(): Promise<ScriptStatusEntry[]>;
  reloadScripts(): Promise<ScriptStatusEntry[]>;
  getScriptLogs(): Promise<LogHistory>;
  setSendToPadmate(
    enabled: boolean,
  ): Promise<{ sendToOwnWindow: boolean; localDryRun: boolean }>;
  onEvent(listener: (event: DaemonEvent) => void): () => void;
  reconnect(): Promise<void>;
  // The page that says why the mapping service did not start (UI-45).
  // Each rejects unless the connection is "failed", or a retry of it.
  // openStartFolder shows the file at fault when it is in the data
  // folder, otherwise opens the data folder; no other path is opened.
  openStartFolder(): Promise<void>;
  // Starts the service again; resolves once it is connected or failed.
  retryStart(): Promise<void>;
  quit(): Promise<void>;
  // The dialog that tells of a repaired config.json (PRO-29): 确定 tells the
  // daemon the repair was shown (config.repairSeen), 打开文件夹 selects the
  // backup in the data folder. backup is ConfigRepair.backup; Main accepts
  // only a name of that form, never a path.
  markConfigRepairSeen(backup: string): Promise<void>;
  showConfigBackup(backup: string): Promise<void>;
  // The overlay's settings (HUD-05): the toasts and their language, where
  // the window sits and how opaque the pill is. They live in the UI's
  // localStorage, so the UI tells Main at start and whenever one changes.
  // Main checks every field and ignores an invalid one; the call takes no
  // part in anything the daemon does.
  setOverlayPrefs(prefs: OverlayPrefs): Promise<void>;
  // The automatic update (PKG-10, ADR-0073). Main owns the state and pushes
  // each change to onUpdateState; the calls answer with the state they led
  // to. The window sends no version and no address.
  getUpdateState(): Promise<UpdateState>;
  // 检查更新: a check the person asked for, answered with a dialog.
  checkForUpdate(): Promise<UpdateState>;
  // 下载, after a version was offered.
  downloadUpdate(): Promise<UpdateState>;
  // 重启并安装, after the download. The saves finish first; then the
  // installer runs and padmate starts again. dryRun is true only in the
  // installed application's test mode, which installs nothing.
  installUpdate(): Promise<UpdateInstallResult>;
  // 以后再说, 跳过这个版本 (only for an offered version), or the OK of a
  // dialog that told how something ended.
  dismissUpdate(choice: UpdateDismissal): Promise<UpdateState>;
  // 自动检查更新.
  setAutoUpdate(enabled: boolean): Promise<UpdateState>;
  // Opens the page of the offered version on GitHub, or the list of
  // releases; Main builds the address from its constants.
  openReleasePage(): Promise<void>;
  onUpdateState(listener: (state: UpdateState) => void): () => void;
}

declare global {
  interface Window {
    padmate: PadmateAPI;
  }
}
