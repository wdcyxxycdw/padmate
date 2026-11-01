import { useEffect, useRef, useState } from "react";
import type { DeviceCombo } from "../../shared/api";
import { type PadChange, padWithChange } from "../state/pads-model";
import type { Language, PadmateState, PadmateStore } from "../state/store";
import { Disclosure, Notice } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { PanicComboDialog } from "./settings/PanicComboDialog";
import { PadSection } from "./settings/SettingsPads";
import "./pages.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Settings → 手柄设置 (ADR-0017): every controller that has been connected,
 * the connected ones first, each with the settings that differ from
 * controller to controller: its function key, its feedback switches and its
 * emergency pause. They are saved at once, to the controller alone. A view
 * inside Settings, like the logs and the diagnostics. The controllers that
 * are not connected now are one fold away.
 */
export function PadsPage({
  state,
  store,
}: {
  state: ShellState;
  store: PadmateStore;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const config = state.config;
  const editable = !!config && !config.problem;
  const { pads, refreshPads, setPadSettings } = state;
  // The change being saved: shown at once, and the controls wait for it.
  const [pending, setPending] = useState<{
    key: string;
    change: PadChange;
  } | null>(null);
  const disabled = !editable || !!pending;
  const change = async (key: string, next: PadChange) => {
    if (pending) return;
    setPending({ key, change: next });
    try {
      await setPadSettings(key, next);
    } finally {
      setPending(null);
    }
  };
  // The list is read again when the page opens, for the times last seen.
  const [tried, setTried] = useState(false);
  useEffect(() => {
    let current = true;
    void refreshPads().finally(() => {
      if (current) setTried(true);
    });
    return () => {
      current = false;
    };
  }, [refreshPads]);

  // The pad whose emergency combo is being recorded. When it goes away the
  // dialog closes and does not come back by itself.
  const [comboFor, setComboFor] = useState<string | null>(null);
  const comboPad = comboFor
    ? pads?.pads.find((pad) => pad.key === comboFor)
    : undefined;
  const comboDevice = comboPad?.connected
    ? state.status?.devices.find((entry) => entry.info.id === comboPad.device)
    : undefined;
  useEffect(() => {
    if (comboFor !== null && !comboDevice) setComboFor(null);
  }, [comboFor, comboDevice]);

  // The save pill shows briefly; a failure's reason is in the shell's alert.
  const save = state.librarySave;
  const clearSave = state.clearLibrarySave;
  useEffect(() => {
    if (save.state !== "saved" && save.state !== "failed") return;
    const timer = setTimeout(
      () => clearSave(save.seq),
      save.state === "saved" ? 2500 : 6000,
    );
    return () => clearTimeout(timer);
  }, [save.state, save.seq, clearSave]);

  const connected = pads?.pads.filter((pad) => pad.connected) ?? [];
  const others = pads?.pads.filter((pad) => !pad.connected) ?? [];
  const section = (pad: NonNullable<typeof pads>["pads"][number]) => (
    <PadSection
      key={pad.key}
      pad={pending?.key === pad.key ? padWithChange(pad, pending.change) : pad}
      language={language}
      disabled={disabled}
      onChange={(next) => void change(pad.key, next)}
      onChangeCombo={() => setComboFor(pad.key)}
    />
  );
  // The dialog stays mounted once opened, so that closing it gives the focus
  // back; it keeps the controller it was last open for.
  const lastCombo = useRef<{
    pad: NonNullable<typeof comboPad>;
    device: NonNullable<typeof comboDevice>;
  } | null>(null);
  if (comboPad && comboDevice)
    lastCombo.current = { pad: comboPad, device: comboDevice };
  const dialog = lastCombo.current;
  const comboOpen = comboFor !== null && !!comboPad && !!comboDevice;
  const combo: DeviceCombo | null = dialog
    ? {
        device: dialog.device.info.id,
        family: dialog.pad.family,
        buttons: dialog.pad.panic.effective.buttons,
        holdMs: dialog.pad.panic.effective.holdMs,
      }
    : null;
  const reload = (
    <button type="button" className="btn" onClick={() => void refreshPads()}>
      {t("重新读取", "Reload")}
    </button>
  );

  return (
    <>
      {editable && (
        // A live region that stays mounted; it is empty while idle.
        <p
          className="page-autosave"
          role="status"
          data-state={
            save.state === "saving" ||
            save.state === "saved" ||
            save.state === "failed"
              ? save.state
              : "idle"
          }
        >
          {save.state === "saving" ? (
            t("正在保存…", "Saving…")
          ) : save.state === "saved" ? (
            <>
              <Icon
                name="check"
                size={13}
                stroke={2.2}
                className="page-autosave-icon"
              />
              {t("已自动保存", "Saved automatically")}
            </>
          ) : save.state === "failed" ? (
            <>
              <Icon name="warn" size={13} stroke={2} />
              {t("未保存，请重试", "Not saved. Try again.")}
            </>
          ) : null}
        </p>
      )}
      <div className="page-scroll">
        <div className="page-column page-column-settings" data-testid="pads">
          {!config ? (
            <Notice
              kind="warning"
              title={t(
                "当前启动方式无法保存设置",
                "Settings cannot be saved in this session",
              )}
            >
              <span>
                {t(
                  "请使用配置库启动 padmate。",
                  "Start padmate with a profile library.",
                )}
              </span>
            </Notice>
          ) : config.problem ? (
            <Notice
              kind="error"
              title={t("无法读取设置文件", "The settings file cannot be read")}
            >
              <span>
                {t(
                  "请修复 config.json 后重新读取。",
                  "Fix config.json, then reload.",
                )}
              </span>
              <span className="page-mono-message">{config.problem}</span>
            </Notice>
          ) : null}
          {!pads ? (
            tried ? (
              <Notice
                kind="warning"
                title={t(
                  "未能读取手柄列表",
                  "The controllers could not be read",
                )}
                actions={reload}
              />
            ) : null
          ) : pads.pads.length === 0 ? (
            <p className="page-muted" data-testid="pads-empty">
              {t("还没有连接过手柄", "No controller has been connected yet.")}
            </p>
          ) : (
            <>
              {connected.map(section)}
              {others.length > 0 && (
                <Disclosure
                  title={t("以前连接过的手柄", "Previously connected")}
                  summary={String(others.length)}
                  defaultOpen={connected.length === 0}
                  testId="pads-others"
                >
                  <div className="pad-others">{others.map(section)}</div>
                </Disclosure>
              )}
            </>
          )}
        </div>
      </div>
      {dialog && combo && (
        <PanicComboDialog
          open={comboOpen}
          language={language}
          store={store}
          device={dialog.device}
          combo={combo}
          configured={dialog.pad.panic.source !== "default"}
          busy={disabled}
          onSave={(next, options) =>
            setPadSettings(dialog.pad.key, { panic: next }, options)
          }
          onClose={() => setComboFor(null)}
        />
      )}
    </>
  );
}
