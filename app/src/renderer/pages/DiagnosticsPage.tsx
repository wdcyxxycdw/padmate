import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useStore } from "zustand";
import type { LatencyResult, PadmateAPI } from "../../shared/api";
import {
  type ActivityRow,
  activityTime,
  hasSamples,
  latencyText,
  matchesKey,
  noAction,
  rowGesture,
  rowKeyName,
  rowResult,
  rowWhere,
} from "../activity-model";
import { ControlIcon, controlIcon } from "../control-icon";
import { controllerLabels, familyName } from "../controller-model";
import type { Language, PadmateState, PadmateStore } from "../store";
import { Icon } from "../ui/Icon";
import {
  type Conflict,
  ConflictNotices,
  conflictNames,
} from "./ConflictNotices";
import "./pages.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** How often the latency figures are read while the page is open. */
export const LATENCY_POLL_MS = 1000;

/** A pad as the event list needs it: what it is called and what its keys are called. */
interface Pad {
  type: string;
  labels: Record<string, string>;
}

function RowView({
  row,
  pad,
  showPad,
  language,
}: {
  row: ActivityRow;
  pad: Pad;
  showPad: boolean;
  language: Language;
}) {
  const result = rowResult(row, language);
  const name = rowKeyName(row, pad.labels, language);
  return (
    <li
      className="page-act-row"
      data-reason={row.reason}
      data-kind={row.kind}
      data-no-action={noAction(row) || undefined}
    >
      <time
        className="page-act-time"
        dateTime={
          Number.isFinite(row.at) ? new Date(row.at).toISOString() : undefined
        }
      >
        {activityTime(row.at)}
      </time>
      {showPad && <span className="page-act-pad">{row.deviceName}</span>}
      <span className="page-act-key">
        <ControlIcon
          icon={controlIcon(row.control, familyName(pad.type), pad.labels)}
        />
        <span className="visually-hidden">{name}</span>
      </span>
      <span className="page-act-gesture">{rowGesture(row, language)}</span>
      <span className="page-act-where">{rowWhere(row, language)}</span>
      <span
        className="page-act-result"
        data-action={result.action || undefined}
      >
        <span className={result.action ? "page-act-action" : "page-act-reason"}>
          {result.text}
        </span>
        {result.preview && (
          <span className="tag amber">
            {tx(language, "只预览", "Preview only")}
          </span>
        )}
        {result.waitedMs !== null && (
          <span className="page-act-wait">
            {tx(
              language,
              `等了 ${result.waitedMs} ms`,
              `Waited ${result.waitedMs} ms`,
            )}
          </span>
        )}
      </span>
    </li>
  );
}

function ActivitySection({
  state,
  store,
}: {
  state: ShellState;
  store: PadmateStore;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const titleId = useId();
  const rows = useStore(store, (value) => value.activity);
  const paused = useStore(store, (value) => value.activityPaused);
  const [query, setQuery] = useState("");
  const [onlyNone, setOnlyNone] = useState(false);
  const devices = state.status?.devices;
  const pads = useMemo(() => {
    const byId = new Map<number, Pad>();
    for (const { info } of devices ?? [])
      byId.set(info.id, {
        type: info.type,
        labels: controllerLabels(info.type, info.buttonLabels, info.buttonMask),
      });
    return byId;
  }, [devices]);
  // A row of a pad that is gone (or whose slot was not known) is named by the
  // first pad, since its keys are the best guess.
  const unknown = useMemo<Pad>(
    () =>
      pads.values().next().value ?? {
        type: "",
        labels: controllerLabels(""),
      },
    [pads],
  );
  const padOf = (row: ActivityRow) =>
    (row.device !== null ? pads.get(row.device) : undefined) ?? unknown;
  const showPad = (devices?.length ?? 0) > 1;
  const shown = rows
    .filter((row) => {
      if (onlyNone && !noAction(row)) return false;
      const pad = padOf(row);
      return matchesKey(row, rowKeyName(row, pad.labels, language), query);
    })
    .reverse();
  const filtered = onlyNone || query.trim() !== "";
  return (
    <section className="page-section" aria-labelledby={titleId}>
      <h2 className="section-title" id={titleId}>
        {t("事件", "Events")}
      </h2>
      <div className="page-act-toolbar">
        <label className="page-search">
          <Icon name="search" size={15} />
          <input
            type="search"
            aria-label={t("筛选按键", "Filter by key")}
            placeholder={t("筛选按键", "Filter by key")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="btn-ghost page-toggle"
          aria-pressed={onlyNone}
          onClick={() => setOnlyNone(!onlyNone)}
        >
          {t("只看没有动作的", "Only presses with no action")}
        </button>
        <span className="page-spacer" />
        <button
          type="button"
          className="btn-ghost"
          onClick={() => state.setActivityPaused(!paused)}
        >
          <Icon name={paused ? "play" : "pause"} size={14} stroke={2} />
          {paused
            ? t("继续记录", "Resume recording")
            : t("暂停记录", "Pause recording")}
        </button>
        <button
          type="button"
          className="btn-ghost"
          disabled={!rows.length}
          onClick={state.clearActivity}
        >
          {t("清空", "Clear")}
        </button>
      </div>
      {shown.length ? (
        // A long list scrolls inside its box, so the sections below stay near;
        // Chromium lets the keyboard focus a scroller that has nothing else to focus.
        <section
          className="page-act-scroll"
          aria-label={t("按键事件", "Key events")}
        >
          <ol className="page-act-list" data-pad={showPad || undefined}>
            {shown.map((row) => (
              <RowView
                key={row.seq}
                row={row}
                pad={padOf(row)}
                showPad={showPad}
                language={language}
              />
            ))}
          </ol>
        </section>
      ) : (
        <div className="page-act-empty">
          <p className="page-hint">
            {rows.length
              ? t("没有符合条件的记录", "No events match")
              : paused
                ? t("记录已暂停", "Recording is paused")
                : t(
                    "按下手柄上的键后，这里会显示每次按键的结果。",
                    "Press a key on the controller to see what happened to it here.",
                  )}
          </p>
          {rows.length > 0 && filtered && (
            <button
              type="button"
              className="btn"
              onClick={() => {
                setQuery("");
                setOnlyNone(false);
              }}
            >
              {t("清除筛选", "Clear filters")}
            </button>
          )}
        </div>
      )}
      <p className="page-footnote">
        {paused
          ? t(
              "记录已暂停，新的按键不会出现在这里。",
              "Recording is paused: new presses do not appear here.",
            )
          : t(
              "显示本次连接的最近 200 次按键，最新的在最上方。",
              "Shows the latest 200 presses of this connection, newest first.",
            )}
      </p>
    </section>
  );
}

/**
 * Reads the latency figures once a second for as long as it is mounted, and
 * once more when asked to (after a reset). A tick does not repeat a read that
 * is still on its way; one that fails keeps the last figures and says so.
 *
 * The read that is asked for is never skipped, and it makes the answers of the
 * reads before it stale: a read that was on its way when the figures were
 * reset was answered from before the reset, and showing it would bring the
 * cleared figures back until the next tick.
 */
function useLatency(api: PadmateAPI) {
  const [result, setResult] = useState<LatencyResult | null>(null);
  const [failed, setFailed] = useState(false);
  const read = useRef<() => Promise<void>>(async () => {});
  useEffect(() => {
    let alive = true;
    let onItsWay = 0;
    // The number of the latest read started; only its answer is shown.
    let latest = 0;
    const run = async (asked: boolean) => {
      if (onItsWay > 0 && !asked) return;
      const mine = ++latest;
      onItsWay += 1;
      try {
        const next = await api.getLatency();
        if (!alive || mine !== latest) return;
        setResult(next);
        setFailed(false);
      } catch {
        if (alive && mine === latest) setFailed(true);
      } finally {
        onItsWay -= 1;
      }
    };
    read.current = () => run(true);
    void run(false);
    const timer = setInterval(() => void run(false), LATENCY_POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [api]);
  return { result, failed, refresh: () => read.current() };
}

function LatencySection({
  api,
  language,
}: {
  api: PadmateAPI;
  language: Language;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const titleId = useId();
  const { result, failed, refresh } = useLatency(api);
  const [clearing, setClearing] = useState(false);
  const clear = async () => {
    setClearing(true);
    try {
      await api.resetLatency();
      await refresh();
    } catch {
      // The next read shows what is left.
    } finally {
      setClearing(false);
    }
  };
  const threshold = latencyText(result?.warnP99Us ?? 1500);
  return (
    <section className="page-section" aria-labelledby={titleId}>
      <div className="page-section-head">
        <h2 className="section-title" id={titleId}>
          {t("延迟", "Latency")}
        </h2>
        <button
          type="button"
          className="btn-ghost"
          aria-label={t("清空延迟统计", "Clear the latency figures")}
          disabled={clearing || !result?.devices.some(hasSamples)}
          onClick={() => void clear()}
        >
          {t("清空", "Clear")}
        </button>
      </div>
      <div className="card page-latency">
        {result?.devices.length ? (
          result.devices.map((pad) => (
            <div
              className="page-latency-pad"
              key={pad.device}
              data-warn={pad.warn || undefined}
            >
              <div className="page-latency-head">
                <span className="page-latency-name">{pad.name}</span>
                <span className="page-latency-count">
                  {t(`${pad.samples} 个样本`, `${pad.samples} samples`)}
                </span>
                <span className="page-spacer" />
                {pad.warn && (
                  <span className="tag amber">
                    {t(`p99 超过 ${threshold}`, `p99 above ${threshold}`)}
                  </span>
                )}
              </div>
              <dl className="page-latency-figures">
                {(
                  [
                    ["p50", "p50", pad.p50],
                    ["p90", "p90", pad.p90],
                    ["p99", "p99", pad.p99],
                    ["max", t("最大", "Max"), pad.max],
                  ] as const
                ).map(([key, label, value]) => (
                  <div key={key} data-figure={key}>
                    <dt>{label}</dt>
                    <dd>{hasSamples(pad) ? latencyText(value) : "—"}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ))
        ) : (
          <p className="page-hint page-latency-empty">
            {result
              ? t("没有正在映射的手柄。", "No controller is being mapped.")
              : failed
                ? t("无法读取延迟。", "The latency could not be read.")
                : "—"}
          </p>
        )}
        {failed && result && (
          <p className="page-hint">
            {t("无法读取延迟。", "The latency could not be read.")}
          </p>
        )}
        {result?.dryRun && (
          <p className="page-hint">
            {t(
              "映射服务在演练模式：按键只打印，不发送。",
              "The mapping service is in dry-run mode: keys are printed, not sent.",
            )}
          </p>
        )}
        <p className="page-hint page-latency-note">
          {t(
            "从 padmate 收到手柄输入到发出按键的时间，不含手柄和接收器本身的延迟；padmate 窗口在前台时只预览，不计入。",
            "The time from padmate receiving the controller input to sending the key. It does not include the delay of the controller and its receiver. While the padmate window is in front, keys are only previewed and not counted.",
          )}
        </p>
      </div>
    </section>
  );
}

function ConflictsSection({
  conflicts,
  language,
}: {
  conflicts: readonly Conflict[] | undefined;
  language: Language;
}) {
  const titleId = useId();
  const listed = conflictNames(conflicts).length > 0;
  return (
    <section className="page-section" aria-labelledby={titleId}>
      <h2 className="section-title" id={titleId}>
        {tx(language, "冲突", "Conflicts")}
      </h2>
      {listed ? (
        <ConflictNotices conflicts={conflicts} language={language} />
      ) : (
        <p className="page-hint">
          {tx(
            language,
            "没有检测到可能占用手柄的软件。",
            "No software that may hold the controller was found.",
          )}
        </p>
      )}
    </section>
  );
}

/**
 * Diagnostics (UI-20, UI-21, UI-22, IN-24, ADR-0071), a view inside Settings
 * like the logs: what happened to the latest presses and why a press did
 * nothing, how long padmate took to send the keys, and software that may
 * hold the controller.
 */
export function DiagnosticsPage({
  state,
  store,
}: {
  state: ShellState;
  store: PadmateStore;
}) {
  return (
    <div className="page-scroll">
      <div className="page-column page-column-diagnostics">
        <ActivitySection state={state} store={store} />
        <LatencySection api={state.api} language={state.language} />
        <ConflictsSection
          conflicts={state.status?.conflicts}
          language={state.language}
        />
      </div>
    </div>
  );
}
