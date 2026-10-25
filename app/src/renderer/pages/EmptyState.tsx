import type { ReactNode } from "react";
import type { Language } from "../store";
import { Icon, type IconName } from "../ui/Icon";
import { type Conflict, ConflictNotices } from "./ConflictNotices";
import "./pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** A centered message for an empty page or panel. */
export function EmptyState({
  title,
  hint,
  controller = false,
  icon,
  children,
}: {
  title: string;
  hint?: string;
  /** Show the large dashed controller symbol (no controller, no service). */
  controller?: boolean;
  icon?: IconName;
  children?: ReactNode;
}) {
  return (
    <div
      className="page-empty"
      data-variant={controller ? "controller" : undefined}
    >
      {controller ? (
        <div className="page-empty-symbol">
          <Icon name="pad" size={52} stroke={1.3} />
        </div>
      ) : (
        icon && (
          <div className="page-empty-icon">
            <Icon name={icon} size={22} />
          </div>
        )
      )}
      <h2>{title}</h2>
      {hint && <p>{hint}</p>}
      {children}
    </div>
  );
}

function UsbIcon() {
  return (
    <svg
      className="ui-icon"
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M12 3v14M8 7l4-4 4 4M7 11v3l5 3 5-3v-2" />
      <circle cx="12" cy="19" r="2" />
    </svg>
  );
}

function BluetoothIcon() {
  return (
    <svg
      className="ui-icon"
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="m7 7 10 10-5 4V3l5 4L7 17" />
    </svg>
  );
}

/**
 * The mapping page of a library session while no profile is active: the
 * library is empty, or mapping is disabled. onChoose is left out when there
 * is nothing to choose.
 */
export function NoProfile({
  language,
  hasProfiles,
  onCreate,
  onChoose,
}: {
  language: Language;
  /** The library has profiles, so the title says none is active. */
  hasProfiles: boolean;
  onCreate: () => void;
  onChoose?: () => void;
}) {
  return (
    <EmptyState
      icon="folder"
      title={
        hasProfiles
          ? tx(language, "没有启用配置", "No profile is in use")
          : tx(language, "暂无配置", "No profiles yet")
      }
      hint={
        hasProfiles
          ? tx(
              language,
              "请选择或新建配置。",
              "Choose a profile or create one.",
            )
          : tx(
              language,
              "新建配置后即可设置按键。",
              "Create a profile to start setting up keys.",
            )
      }
    >
      <div className="page-empty-actions">
        <button type="button" className="btn-primary" onClick={onCreate}>
          <Icon name="plus" size={15} stroke={2} />
          {tx(language, "新建配置", "New profile")}
        </button>
        {hasProfiles && onChoose && (
          <button type="button" className="btn" onClick={onChoose}>
            {tx(language, "选择配置", "Choose profile")}
          </button>
        )}
      </div>
    </EmptyState>
  );
}

/** The mapping page without a controller (NoController design). */
export function NoController({
  language,
  conflicts,
  onShowLogs,
}: {
  language: Language;
  /** Running software that may hold the controller; shown by name. */
  conflicts?: readonly Conflict[];
  /** Opens the logs view under Settings. */
  onShowLogs?: () => void;
}) {
  const logsLink = onShowLogs && (
    <button type="button" className="page-link" onClick={onShowLogs}>
      {tx(language, "查看日志", "View logs")}
    </button>
  );
  return (
    <EmptyState
      title={tx(language, "连接一个手柄", "Connect a controller")}
      hint={tx(
        language,
        "请通过 USB 线或蓝牙连接。支持 Xbox、PlayStation、Switch Pro 和 Joy-Con。",
        "Connect by USB cable or Bluetooth. Xbox, PlayStation, Switch Pro and Joy-Con controllers work.",
      )}
      controller
    >
      <div className="page-empty-ports">
        <span className="page-empty-port">
          <UsbIcon />
          USB
        </span>
        <span className="page-empty-port">
          <BluetoothIcon />
          {tx(language, "蓝牙", "Bluetooth")}
        </span>
      </div>
      {conflicts?.length ? (
        <div className="page-empty-conflicts">
          <ConflictNotices conflicts={conflicts} language={language} />
          {logsLink}
        </div>
      ) : (
        <div className="page-empty-note">
          <Icon name="warn" size={15} stroke={2} />
          <span>
            {tx(
              language,
              "如已连接但未显示，可能是 Steam、DS4Windows 等软件占用了手柄。",
              "Connected but not showing up? Software such as Steam or DS4Windows may be holding the controller.",
            )}
            {logsLink && <> {logsLink}</>}
          </span>
        </div>
      )}
    </EmptyState>
  );
}
