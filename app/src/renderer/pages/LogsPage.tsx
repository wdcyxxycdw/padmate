import { useState } from "react";
import { useStore } from "zustand";
import type { LogEntry } from "../../shared/api";
import type { Translate } from "../i18n";
import {
  type Language,
  LOG_LIMIT,
  type PadmateState,
  type PadmateStore,
} from "../store";
import { Segmented } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { EmptyState } from "./EmptyState";
import "./pages.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
type LevelFilter = "all" | "error" | "warn";
type SourceFilter = "all" | "system" | "script";
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export function logLevel(record: LogEntry["record"]): string {
  const level = String(record.level ?? "INFO").toUpperCase();
  return level === "WARNING" ? "WARN" : level;
}

const shownKeys = new Set(["time", "level", "msg", "source"]);

/** The other attributes of a record as `key=value` pairs. */
export function logFields(record: LogEntry["record"]): string {
  return Object.entries(record)
    .filter(([key]) => !shownKeys.has(key))
    .map(
      ([key, value]) =>
        `${key}=${typeof value === "string" ? value : JSON.stringify(value)}`,
    )
    .join("  ");
}

function logTime(record: LogEntry["record"], language: Language) {
  if (typeof record.time !== "string") return "—";
  const time = new Date(record.time);
  return Number.isNaN(time.getTime())
    ? "—"
    : time.toLocaleTimeString(language, { hour12: false });
}

function LogRow({
  row,
  language,
  expanded,
  onToggle,
}: {
  row: LogEntry;
  language: Language;
  expanded: boolean;
  onToggle: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const level = logLevel(row.record);
  const id = `log-${row.sequence}`;
  return (
    <li data-level={level.toLowerCase()} data-expanded={expanded}>
      <button
        type="button"
        className="page-log-row"
        aria-expanded={expanded}
        aria-controls={expanded ? id : undefined}
        onClick={onToggle}
      >
        <time
          className="page-log-time"
          dateTime={
            typeof row.record.time === "string" ? row.record.time : undefined
          }
        >
          {logTime(row.record, language)}
        </time>
        <span className="page-log-level" data-level={level.toLowerCase()}>
          {level}
        </span>
        <span className="page-log-source">
          {row.record.source === "script"
            ? t("脚本", "Script")
            : t("系统", "System")}
        </span>
        <span className="page-log-message">{String(row.record.msg ?? "")}</span>
        <span className="page-log-fields">{logFields(row.record)}</span>
        <Icon
          name="chevronDown"
          size={14}
          stroke={2}
          className="page-log-chevron"
        />
      </button>
      {expanded && (
        <pre id={id} className="page-log-details">
          {JSON.stringify(row.record, null, 2)}
          {row.truncated ? `\n${t("（此条日志已截断）", "(truncated)")}` : ""}
        </pre>
      )}
    </li>
  );
}

export function LogsPage({
  state,
  store,
}: {
  state: ShellState;
  store: PadmateStore;
  /** Kept for the shell's page contract; this page uses inline text. */
  t?: Translate;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [search, setSearch] = useState("");
  const [level, setLevel] = useState<LevelFilter>("all");
  const [source, setSource] = useState<SourceFilter>("all");
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set());
  const logs = useStore(store, (value) => value.logs);
  const query = search.trim().toLowerCase();
  const matchesText = (row: LogEntry) =>
    (source === "all" ||
      (source === "script"
        ? row.record.source === "script"
        : row.record.source !== "script")) &&
    (!query || JSON.stringify(row.record).toLowerCase().includes(query));
  const candidates = logs.filter(matchesText);
  const count = (wanted: string) =>
    candidates.filter((row) => logLevel(row.record) === wanted).length;
  const errors = count("ERROR");
  const warnings = count("WARN");
  const rows = candidates
    .filter(
      (row) => level === "all" || logLevel(row.record) === level.toUpperCase(),
    )
    .reverse();
  const filtered = level !== "all" || source !== "all" || !!query;
  const toggle = (sequence: number) => {
    const next = new Set(expanded);
    if (next.has(sequence)) next.delete(sequence);
    else next.add(sequence);
    setExpanded(next);
  };
  return (
    <div className="page-scroll">
      <div className="page-column page-column-logs">
        <div className="page-logs-toolbar">
          <label className="page-search">
            <Icon name="search" size={15} />
            <input
              type="search"
              aria-label={t("搜索日志", "Search logs")}
              placeholder={t("搜索日志", "Search logs")}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </label>
          <Segmented<LevelFilter>
            label={t("级别", "Level")}
            className="page-seg"
            value={level}
            onChange={setLevel}
            options={[
              { value: "all", label: t("全部", "All") },
              {
                value: "error",
                label: (
                  <>
                    {t("错误", "Errors")}
                    <span
                      className="page-count"
                      data-level={errors ? "error" : undefined}
                    >
                      {errors}
                    </span>
                  </>
                ),
              },
              {
                value: "warn",
                label: (
                  <>
                    {t("警告", "Warnings")}
                    <span
                      className="page-count"
                      data-level={warnings ? "warn" : undefined}
                    >
                      {warnings}
                    </span>
                  </>
                ),
              },
            ]}
          />
          <Segmented<SourceFilter>
            label={t("来源", "Source")}
            className="page-seg"
            value={source}
            onChange={setSource}
            options={[
              { value: "all", label: t("全部来源", "All sources") },
              { value: "system", label: t("系统", "System") },
              { value: "script", label: t("脚本", "Scripts") },
            ]}
          />
          <span className="page-spacer" />
          <button
            type="button"
            className="btn-ghost"
            onClick={() => {
              state.clearLogs();
              setExpanded(new Set());
            }}
            disabled={!logs.length}
          >
            {t("清空视图", "Clear view")}
          </button>
        </div>
        {rows.length ? (
          <ol
            className="page-log-list"
            aria-label={t("日志记录", "Log records")}
          >
            {rows.map((row) => (
              <LogRow
                key={row.sequence}
                row={row}
                language={language}
                expanded={expanded.has(row.sequence)}
                onToggle={() => toggle(row.sequence)}
              />
            ))}
          </ol>
        ) : logs.length ? (
          <EmptyState
            icon="search"
            title={t("没有符合筛选条件的日志", "No logs match these filters")}
          >
            {filtered && (
              <button
                type="button"
                className="btn"
                onClick={() => {
                  setSearch("");
                  setLevel("all");
                  setSource("all");
                }}
              >
                {t("清除筛选", "Clear filters")}
              </button>
            )}
          </EmptyState>
        ) : (
          <EmptyState
            icon="log"
            title={t("暂无日志", "No logs yet")}
            hint={t(
              "映射服务和脚本的运行记录将显示在此处。",
              "Records from the mapping service and scripts appear here.",
            )}
          />
        )}
        <p className="page-footnote">
          {t(
            `显示本次连接的最近 ${LOG_LIMIT} 条日志，最新的在最上方。`,
            `Shows the latest ${LOG_LIMIT} records from this connection, newest first.`,
          )}
        </p>
      </div>
    </div>
  );
}
