// The first-run page (UI-38, ADR-0053): it waits for a controller, says
// which one is connected, then goes to the main page by itself. The main
// page shows the fixed default profile (PRO-28), which maps nothing; setting
// the keys is left to the user. The page makes nothing.
import { useEffect, useRef } from "react";
import { ADVANCE_MS, padNames } from "./onboarding-model";
import { ConflictNotices } from "./pages/ConflictNotices";
import type { Language, PadmateState } from "./store";
import { Dialog } from "./ui/controls";
import { Icon } from "./ui/Icon";
import "./onboarding.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export function Onboarding({
  open,
  connected,
  state,
  onClose,
}: {
  open: boolean;
  /** Shown only while connected; it waits over a reconnect. */
  connected: boolean;
  state: ShellState;
  /** entered: go to the main page; false when closed with × or Esc. */
  onClose: (entered: boolean) => void;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const visible = open && connected;
  const action = useRef<HTMLButtonElement>(null);
  const names = padNames(state.status);
  const hasPad = names.length > 0;
  const close = useRef(onClose);
  close.current = onClose;

  // A connected controller shows for ADVANCE_MS, then the page goes on.
  // A controller that goes away, or the page closing, clears the timer.
  const counting = visible && hasPad;
  useEffect(() => {
    if (!counting) return;
    const timer = setTimeout(() => close.current(true), ADVANCE_MS);
    return () => clearTimeout(timer);
  }, [counting]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: The button moves focus to itself when the page shows and when a controller connects (its label changes).
  useEffect(() => {
    if (visible) action.current?.focus();
  }, [visible, hasPad]);

  return (
    <Dialog
      open={visible}
      title={t("连接手柄", "Connect your controller")}
      closeLabel={t("关闭", "Close")}
      onClose={() => onClose(false)}
      className="onboarding-dialog"
      footer={
        <>
          <span className="spacer" />
          <button
            ref={action}
            type="button"
            className={hasPad ? "btn-primary" : "btn"}
            onClick={() => onClose(true)}
          >
            {hasPad
              ? t("进入主页", "Go to the main page")
              : t("不连接手柄，直接进入", "Continue without a controller")}
          </button>
        </>
      }
    >
      <div className="onboarding-status" role="status">
        {hasPad ? (
          <>
            <p className="onboarding-pad" data-state="connected">
              <Icon name="check" size={18} stroke={2.2} />
              {t(
                `已连接：${names.join("、")}`,
                `Connected: ${names.join(", ")}`,
              )}
            </p>
            <p className="onboarding-next">
              {t("即将进入主页…", "Opening the main page…")}
            </p>
          </>
        ) : (
          <p className="onboarding-pad" data-state="waiting">
            <Icon name="pad" size={18} />
            {t(
              "请通过 USB 或蓝牙连接手柄。",
              "Connect a controller by USB or Bluetooth.",
            )}
          </p>
        )}
      </div>
      {!hasPad && (
        <ConflictNotices
          conflicts={state.status?.conflicts}
          language={language}
          dismissed={state.dismissedConflicts}
          onDismiss={state.dismissConflict}
        />
      )}
    </Dialog>
  );
}
