// The settings of each controller (ADR-0017): the shapes of the daemon's
// pads.list, pads.set and pads.migrate (cmd/padmated/ipc_pads.go). Types
// only; the settings page calls the methods through the main process.

/** Where a setting comes from: the controller's own entry in config.json, the
 * retired global member, or the built-in value of its family. */
export type PadSource = "device" | "legacy" | "default";

/** The family of a controller: what decides its function keys and its
 * built-in emergency combo. */
export type PadFamily =
  | "standard"
  | "joyconLeft"
  | "joyconRight"
  | "joyconLeftVertical"
  | "joyconRightVertical"
  | "joyconPair"
  | "unknown";

/** A button that can be a controller's function key. */
export type PadFunctionKeyName =
  | "start"
  | "back"
  | "paddle1"
  | "paddle2"
  | "paddle3"
  | "paddle4";

export type PadFunctionKeyOption = {
  control: PadFunctionKeyName;
  /** The name printed on the controller: "ZR", "Menu", "−". */
  label: string;
};

export type PadFunctionKey = {
  /** The family's default; it is the first of options. */
  default: PadFunctionKeyName;
  options: PadFunctionKeyOption[];
  /** devices.<key>.functionKey as the file has it. */
  stored?: PadFunctionKeyName;
  effective: PadFunctionKeyName;
  source: PadSource;
};

export type PadSwitches = {
  rumble: boolean;
  led: boolean;
  pause: boolean;
  lowBattery: boolean;
};

export type PadFeedback = {
  /** The switches devices.<key>.feedback sets. */
  stored?: Partial<PadSwitches>;
  effective: PadSwitches;
  /** The strongest source among the switches. */
  source: PadSource;
};

export type PadCombo = { buttons: string[]; holdMs: number };

export type PadPanic = {
  /** The family's built-in combo. */
  default: PadCombo;
  stored?: PadCombo;
  /** The combo the daemon detects (the built-in one when the controller lacks
   * a button of the configured one). */
  effective: PadCombo;
  source: PadSource;
};

/** One controller that has been connected, or that config.json has settings for. */
export type PadInfo = {
  /** The canonical DeviceKey. */
  key: string;
  name: string;
  type: string;
  buttonMask: number;
  /** The daemon has seen it, so name and type are real. */
  known: boolean;
  connected: boolean;
  /** The connection ID while connected, as in devices.list. */
  device?: number;
  ignored: boolean;
  /** Milliseconds since the Unix epoch; absent when not known. */
  lastSeen?: number;
  family: PadFamily;
  functionKey: PadFunctionKey;
  feedback: PadFeedback;
  panic: PadPanic;
};

/** Which retired global members config.json still has. */
export type PadLegacy = {
  any: boolean;
  functionKey: boolean;
  feedback: boolean;
  panic: boolean;
};

/** pads.list: the connected controllers first, then the others by the time
 * they were last seen. */
export type PadsListResult = {
  /** The revision of config.json the list was read from. */
  revision: string;
  legacy: PadLegacy;
  pads: PadInfo[];
};

/**
 * pads.set: a member that is absent stays, null takes the controller's own
 * value away, anything else sets it. The function key must be one of the
 * controller's options; the daemon refuses others (-32602).
 */
export type PadsSetParams = {
  key: string;
  /** Optional: when given, a changed config.json is -32009. */
  revision?: string;
  functionKey?: PadFunctionKeyName | null;
  feedback?: Partial<PadSwitches> | null;
  panic?: PadCombo | null;
};

export type PadsSaved = {
  revision: string;
  applied: boolean;
  problem?: string;
  restartRequired?: boolean;
};

export type PadsSetResult = PadsSaved & { pad: PadInfo };

/** pads.migrate: folds the retired global members into the controllers and
 * takes them out of config.json. */
export type PadsMigrateResult = PadsSaved & {
  changed: boolean;
  legacy: PadLegacy;
  pads: PadInfo[];
};
