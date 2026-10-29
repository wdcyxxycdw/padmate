// The rows of the diagnostics page's event list (UI-20, UI-21, ADR-0071): what
// the engine decided about each press, and why a press did nothing, from the
// engine.activity events of the daemon (ARCHITECTURE §11). Nothing here talks to
// the daemon or to React; the store keeps the rows (a ring of ACTIVITY_LIMIT) and
// the page only shows them.
import type { LatencyDevice, StatusResult } from "../shared/api";
import { type Gesture, gestureName, gestures } from "./editor-model";
import { pressedControlName, previewAction } from "./key-preview";
import type { Language } from "./store";

/** How many rows the store keeps; the oldest are dropped first. */
export const ACTIVITY_LIMIT = 200;

/** engine.activity {kind:"decision"}: one engine decision (§7.8) with what the daemon adds (§11). */
export interface DecisionActivity {
  kind: "decision";
  slot?: number;
  decision?: {
    control?: unknown;
    edge?: unknown;
    gesture?: unknown;
    level?: unknown;
    binding?: unknown;
    action?: unknown;
    reason?: unknown;
  };
  latchBinding?: boolean;
  /** Unix milliseconds on the daemon's clock when the decision was published. */
  at?: unknown;
  /** The gestures an unbound press waits for (not a failure). */
  waiting?: unknown;
  /** How long a tap, multi-tap or hold waited from the press to its decision. */
  waitedMs?: unknown;
}

/** engine.activity {kind:"unrouted"}: a button edge of a pad that no slot maps. */
export interface UnroutedActivity {
  kind: "unrouted";
  at?: unknown;
  device?: unknown;
  control?: unknown;
  down?: unknown;
  why?: unknown;
}

/** What paused the mapping when a press found it paused. */
export type PausedBy = "user" | "system" | "capture";

/** One line of the event list. */
export interface ActivityRow {
  /** Counts the rows of this connection; the list's key. */
  seq: number;
  /** Unix milliseconds. */
  at: number;
  /** "unrouted": a press of a pad that no slot maps; otherwise an engine decision. */
  kind: "decision" | "unrouted";
  /** The pad's connection ID; null when the decision's slot is not known. */
  device: number | null;
  deviceName: string;
  /** The engine's control name ("south", "lt", "ls.up"), or the SDL button of an unrouted press. */
  control: string;
  /** "down", "up" or ""; the engine writes EdgeDown for a gesture that a release decided. */
  edge: string;
  gesture: string;
  /** The level the binding was found in: a layer, "common" or "function:<function key>", e.g. "function:start". */
  level: string;
  /** The engine's reason; "unrouted" for an unrouted press. */
  reason: string;
  /** Unrouted only: the daemon's why (queued, ignored, notGamepad, disabled). */
  why: string;
  /** The decision's action text, such as "tap Ctrl+Z". */
  action: string;
  /** The decision had a binding (an unbound press with one has only a name). */
  bound: boolean;
  /** An unbound press that waits for release, hold or repeated taps: not a failure. */
  waiting: boolean;
  waitedMs: number | null;
  /** The output was only previewed: the daemon prints its keys, or padmate's window was in front. */
  preview: boolean;
  /** For reason "paused": what paused the mapping. */
  pausedBy: PausedBy | null;
  /** For a queued unrouted press: how many pads are mapped. */
  mapped: number;
}

const text = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function pausedBy(status: StatusResult | null): PausedBy {
  if (status?.paused) return "user";
  if (status?.suspended && status.suspended !== "none") return "system";
  if (status?.capturing) return "capture";
  return "user";
}

function deviceOf(
  id: number | null,
  status: StatusResult | null,
  fallbackName: string,
): { device: number | null; deviceName: string } {
  const entry =
    id === null
      ? undefined
      : status?.devices.find((device) => device.info.id === id);
  return { device: id, deviceName: entry?.info.name ?? fallbackName };
}

/**
 * The row of a decision event, or null for one the list does not show: a
 * release that ended a cycle holding nothing ("end") comes after most presses
 * and says nothing the press did not (a macro key has one after each press),
 * so it would only push the older rows out of the ring.
 */
export function decisionRow(
  event: DecisionActivity,
  seq: number,
  status: StatusResult | null,
  now: number,
): ActivityRow | null {
  const decision = event.decision;
  if (!decision) return null;
  const control = str(decision.control);
  const reason = str(decision.reason);
  if (!control || !reason || reason === "end") return null;
  const slot = status?.slots.find((entry) => entry.slot === event.slot);
  const { device, deviceName } = deviceOf(
    slot ? slot.device : null,
    status,
    slot?.name ?? "",
  );
  const waited = event.waitedMs;
  return {
    seq,
    at: typeof event.at === "number" ? event.at : now,
    kind: "decision",
    device,
    deviceName,
    control,
    edge: str(decision.edge),
    gesture: str(decision.gesture),
    level: str(decision.level),
    reason,
    why: "",
    action: str(decision.action),
    bound: str(decision.binding) !== "",
    waiting:
      reason === "unbound" &&
      Array.isArray(event.waiting) &&
      event.waiting.length > 0,
    waitedMs: typeof waited === "number" ? waited : null,
    preview: !!(status?.dryRun || status?.localDryRun),
    pausedBy: reason === "paused" ? pausedBy(status) : null,
    mapped: 0,
  };
}

/**
 * The row of a press on a pad that no slot maps, or null for a release (the
 * list has one row per press) or an event without the fields.
 */
export function unroutedRow(
  event: UnroutedActivity,
  seq: number,
  status: StatusResult | null,
  now: number,
): ActivityRow | null {
  const control = str(event.control);
  const why = str(event.why);
  if (event.down !== true || !control || !why) return null;
  const id = Number.isInteger(event.device) ? (event.device as number) : null;
  const { device, deviceName } = deviceOf(id, status, "");
  return {
    seq,
    at: typeof event.at === "number" ? event.at : now,
    kind: "unrouted",
    device,
    deviceName,
    control,
    edge: "down",
    gesture: "",
    level: "",
    reason: "unrouted",
    why,
    action: "",
    bound: false,
    waiting: false,
    waitedMs: null,
    preview: false,
    pausedBy: null,
    mapped: status?.slots.length || 4,
  };
}

/** The ring: rows then added, at most limit, the newest kept. */
export function appendActivity(
  rows: readonly ActivityRow[],
  added: readonly ActivityRow[],
  limit = ACTIVITY_LIMIT,
): ActivityRow[] {
  return rows.concat(added).slice(-limit);
}

interface Sentence {
  zh: string;
  en: string;
}

/**
 * The sentence for each reason of the engine (decision.go's reasonNames, ENG-20),
 * and for the variants of a reason, written "reason.variant": the reason's
 * name is the key's part before the dot. A test reads the Go table and fails
 * for a reason that has no sentence here (ADR-0071). dispatch is only the
 * fallback: a row that ran an action shows the action.
 */
export const reasonSentences: Readonly<Record<string, Sentence>> = {
  dispatch: { zh: "执行了动作", en: "Ran the action." },
  unbound: {
    zh: "当前配置里这个键没有设置动作",
    en: "This key has no action in the current profile.",
  },
  "unbound.waiting": {
    zh: "按下时没有动作，要等松开、长按或连按后才能确定",
    en: "Nothing happens on press; the action waits for release, hold or repeated taps.",
  },
  "unbound.labelOnly": {
    zh: "这个键只有名称，没有设置动作",
    en: "This key has a name but no action.",
  },
  "unbound.gesture": {
    zh: "这个映射没有设置动作",
    en: "No action is set for this mapping.",
  },
  // The end of the wait for more taps, when the binding has no action for
  // the number of taps made (tapCount): "{n}" is that number.
  "unbound.tap": {
    zh: "按了 {n} 下，这个键没有设置按 {n} 下的动作",
    en: "Tapped once; this key has no action for a single tap.",
  },
  "unbound.taps": {
    zh: "按了 {n} 下，这个键没有设置按 {n} 下的动作",
    en: "Tapped {n} times; this key has no action for {n} taps.",
  },
  release: {
    zh: "松开，放开了按住的键",
    en: "Released the held keys.",
  },
  end: {
    zh: "松开，没有要放开的键",
    en: "Released; no keys were held.",
  },
  cancel: {
    zh: "这次按压被取消，按住的键已放开（暂停、切换配置或释放全部按键时会这样）",
    en: "This press was canceled and its keys released (pause, profile switch or release all).",
  },
  // A cancel that released nothing: a press still waiting for its gesture,
  // or the window for more taps that a pause or reload dropped.
  "cancel.nothing": {
    zh: "这次按压被取消（暂停、切换配置或释放全部按键时会这样）",
    en: "This press was canceled (pause, profile switch or release all).",
  },
  canceled: {
    zh: "这次按压已被取消，松开时不执行动作",
    en: "That press had been canceled, so its release does nothing.",
  },
  "canceled.latch": {
    zh: "这个键之前锁存了按键，这次按下只是解除锁存",
    en: "This key had latched keys; this press only released the latch.",
  },
  spurious: {
    zh: "重复的按下或松开，已忽略",
    en: "Repeated press or release; ignored.",
  },
  paused: {
    zh: "映射已暂停",
    en: "Mapping is paused.",
  },
  "paused.system": {
    zh: "系统暂停中（锁屏、睡眠或安全桌面）",
    en: "The system has suspended mapping (lock screen, sleep or secure desktop).",
  },
  "paused.capture": {
    zh: "正在录入快捷键，映射暂停",
    en: "Recording a shortcut; mapping is paused.",
  },
  blocked: {
    zh: "这个键在当前层被屏蔽",
    en: "This key is blocked in the current layer.",
  },
  function: {
    zh: "功能键：按住期间其他键换成另一套",
    en: "Function key: while held, other keys use their alternate set.",
  },
  "function.up": {
    zh: "功能键松开，其他键恢复平时的一套",
    en: "Function key released; other keys are back to normal.",
  },
  script: {
    zh: "脚本处理了这个键",
    en: "A script handled this key.",
  },
};

/**
 * How many taps a gesture stands for: 1 for "tap" up to 4 for "quadTap";
 * 0 for any other gesture.
 */
export function tapCount(gesture: string): number {
  switch (gesture) {
    case "tap":
      return 1;
    case "doubleTap":
      return 2;
    case "tripleTap":
      return 3;
    case "quadTap":
      return 4;
  }
  return 0;
}

/**
 * Whether a row is the end of a wait for taps that no action answers: the
 * engine decides it, unbound, when the window for more taps ends with a
 * number of taps the binding has nothing for (a double tap only, tapped once).
 */
function endsTapWait(row: ActivityRow): boolean {
  return row.reason === "unbound" && tapCount(row.gesture) > 0;
}

/** The key in reasonSentences that says what a decision row shows. */
export function reasonKey(row: ActivityRow): string {
  switch (row.reason) {
    case "unbound":
      if (row.waiting) return "unbound.waiting";
      if (endsTapWait(row))
        return row.gesture === "tap" ? "unbound.tap" : "unbound.taps";
      if (row.gesture && row.gesture !== "press") return "unbound.gesture";
      return row.bound ? "unbound.labelOnly" : "unbound";
    case "cancel":
      return row.action ? "cancel" : "cancel.nothing";
    case "canceled":
      // A press that finds a latch held releases it (cancelling the cycle
      // that latched); the release of a cancelled cycle is an up edge.
      return row.edge === "down" ? "canceled.latch" : "canceled";
    case "paused":
      return row.pausedBy === "system"
        ? "paused.system"
        : row.pausedBy === "capture"
          ? "paused.capture"
          : "paused";
    case "function":
      return row.edge === "up" ? "function.up" : "function";
  }
  return row.reason;
}

/** The pad is not mapped (ADR-0071): why, for the whys the daemon sends. */
export function unroutedSentence(
  row: Pick<ActivityRow, "why" | "mapped">,
  language: Language,
): string {
  switch (row.why) {
    case "queued":
      return text(
        language,
        `这只手柄没有被映射：已有 ${row.mapped} 只手柄在映射`,
        `This controller is not mapped: ${row.mapped} ${row.mapped === 1 ? "controller is" : "controllers are"} already mapped.`,
      );
    case "ignored":
      return text(
        language,
        "这只手柄被设为忽略",
        "This controller is set to be ignored.",
      );
    case "notGamepad":
      return text(
        language,
        "这只手柄没有标准映射",
        "This controller has no standard mapping.",
      );
    case "disabled":
      return text(
        language,
        "这只手柄的映射因反复出错被停用",
        "This controller's mapping was disabled after repeated errors.",
      );
  }
  return text(language, "这只手柄没有被映射", "This controller is not mapped.");
}

/** The sentence that says why a row did what it did (or nothing). */
export function rowSentence(row: ActivityRow, language: Language): string {
  if (row.kind === "unrouted") return unroutedSentence(row, language);
  const sentence =
    reasonSentences[reasonKey(row)] ?? reasonSentences[row.reason];
  if (!sentence) return row.reason;
  return text(language, sentence.zh, sentence.en).replaceAll(
    "{n}",
    String(tapCount(row.gesture)),
  );
}

/** An action text the preview line does not know, as the engine wrote it. */
function actionLine(action: string, language: Language): string {
  const shown = previewAction(action, [], language);
  if (shown) return shown;
  const profile = /^profile (.+)$/s.exec(action);
  if (profile)
    return text(language, `切换到「${profile[1]}」`, `Switch to ${profile[1]}`);
  return action;
}

/**
 * Whether an action sends something to the system (keys, text, the pointer,
 * a script's output), which a preview only prints. A layer, a profile
 * switch, the key hints and the precision of the sticks take effect in padmate
 * itself, whatever the output does with its keys.
 */
function sendsOutput(action: string): boolean {
  return !/^(?:layer|profile|hud|mouse precision)(?: |$)/.test(action);
}

/** What a row shows as its result: the action that ran, or the sentence. */
export interface ActivityResult {
  /** The action's text, or the sentence. */
  text: string;
  /** The text is an action that ran, not a reason. */
  action: boolean;
  /** An action that was only previewed. */
  preview: boolean;
  /** How long the tap, multi-tap or hold waited, on the row that decided it. */
  waitedMs: number | null;
}

/**
 * The result of a row, by the decision's reason and gesture, not by its edge:
 * a tap or a release that a button release decided is a "down" edge too.
 */
export function rowResult(
  row: ActivityRow,
  language: Language,
): ActivityResult {
  if (row.reason === "dispatch" && row.action)
    return {
      text: actionLine(row.action, language),
      action: true,
      preview: row.preview && sendsOutput(row.action),
      waitedMs: row.waitedMs,
    };
  return {
    text: rowSentence(row, language),
    action: false,
    preview: false,
    // The wait of taps that no action answers ends on this row.
    waitedMs: endsTapWait(row) ? row.waitedMs : null,
  };
}

/**
 * Whether a row explains why nothing was sent, for "只看没有动作的": a press
 * nothing answers, the taps of a wait that nothing answers, a canceled or
 * ignored press, a paused or blocked one, and a pad that no slot maps. Not the
 * ones that sent keys, released keys, took a script or function key, or only
 * wait for a gesture to be known.
 */
export function noAction(row: ActivityRow): boolean {
  switch (row.reason) {
    case "unrouted":
    case "spurious":
    case "paused":
    case "blocked":
    case "canceled":
    case "cancel":
      return true;
    case "unbound":
      return !row.waiting;
  }
  return false;
}

/** The time of a row, "14:03:09.027", in the local time zone. */
export function activityTime(at: number): string {
  const time = new Date(at);
  if (Number.isNaN(time.getTime())) return "—";
  const two = (value: number) => String(value).padStart(2, "0");
  return `${two(time.getHours())}:${two(time.getMinutes())}:${two(time.getSeconds())}.${String(time.getMilliseconds()).padStart(3, "0")}`;
}

/**
 * Where the row's binding was found: the layer, the function set ("按住功能键",
 * whichever of the three keys it is, ADR-0076), shared by all layers, or the
 * script.
 */
export function rowWhere(row: ActivityRow, language: Language): string {
  if (row.reason === "script") return text(language, "脚本", "Script");
  switch (row.level) {
    case "":
      return "";
    case "common":
      return text(language, "共用", "Shared");
    case "base":
      return text(language, "平时", "Normal");
  }
  // The function set (ADR-0075): the level names the function key held.
  if (/^function:.+$/.test(row.level))
    return text(language, "按住功能键", "Hold function key");
  return row.level;
}

/** The gesture of a row: a release is a release, whatever gesture the engine names. */
export function rowGesture(row: ActivityRow, language: Language): string {
  if (row.kind === "unrouted") return "";
  if (row.edge === "up") return text(language, "松开", "Release");
  return (gestures as readonly string[]).includes(row.gesture)
    ? gestureName(row.gesture as Gesture, language)
    : "";
}

/** The name the pad prints on the row's key, such as "A" or "LS ↑". */
export function rowKeyName(
  row: ActivityRow,
  labels: Record<string, string>,
  language: Language,
): string {
  return pressedControlName(row.control, labels, language);
}

/**
 * Whether the row passes the key filter: the query is part of the name the pad
 * prints on the key, or the start of the engine's control name ("south",
 * "ls.up"); case does not matter. An empty query passes everything.
 */
export function matchesKey(
  row: ActivityRow,
  keyName: string,
  query: string,
): boolean {
  const wanted = query.trim().toLowerCase();
  if (!wanted) return true;
  return (
    keyName.toLowerCase().includes(wanted) ||
    row.control.toLowerCase().startsWith(wanted)
  );
}

/** A time in microseconds: "850 µs" under a millisecond, else "1.23 ms". */
export function latencyText(microseconds: number): string {
  if (!Number.isFinite(microseconds)) return "—";
  if (microseconds < 1000) return `${Math.round(microseconds)} µs`;
  return `${(microseconds / 1000).toFixed(2)} ms`;
}

/** Whether a pad's figures are worth showing (it has samples). */
export function hasSamples(device: Pick<LatencyDevice, "samples">): boolean {
  return device.samples > 0;
}
