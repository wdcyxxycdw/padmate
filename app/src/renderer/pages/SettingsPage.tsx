import { useEffect, useId, useRef, useState } from "react";
import type { DeviceEntry } from "../../shared/api";
import type { Config } from "../../shared/generated/schema";
import { sticksOf } from "../calibration-model";
import type { Translate } from "../i18n";
import {
  changeConfigText,
  type LibraryOutcome,
  readConfig,
} from "../library-model";
import { ModelLicenses } from "../ModelLicenses";
import {
  deviceCombo,
  isPanicFamily,
  setGlobalTiming,
  setPanicCombo,
  startupDryRun,
} from "../settings-model";
import {
  type BasicSettings,
  EVENT_GAP_MAX_MS,
  eventGapValue,
  type Language,
  type PadmateState,
  type PadmateStore,
  type ReportOptions,
  readBasicSettings,
} from "../store";
import { Notice, Switch } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { CalibrationDialog, CalibrationSection } from "./CalibrationDialog";
import { MappingWizardDialog } from "./MappingWizardDialog";
import { ComboKeys, PanicComboDialog, seconds } from "./PanicComboDialog";
import { ResetMappingDialog } from "./ResetMappingDialog";
import { FeedbackSection } from "./SettingsFeedback";
import { OverlaySection } from "./SettingsOverlay";
import { TimingSection } from "./SettingsTiming";
import { UpdatesSection } from "./SettingsUpdates";
import "./pages.css";

type ShellState = Omit<
  PadmateState,
  "inputs" | "logs" | "activity" | "preview" | "keyPreview"
>;
const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** "v0.12.0" for release versions; other build names ("dev") stay as they are. */
export function versionText(version?: string): string {
  if (!version) return "";
  return /^\d/.test(version) ? `v${version}` : version;
}

/** The event gap typed in the field, or null when it is not 0–50. */
export function parseEventGap(text: string): number | null {
  const trimmed = text.trim();
  return /^\d+$/.test(trimmed) ? eventGapValue(Number(trimmed)) : null;
}

export function SettingsPage({
  state,
  store,
  onShowLogs,
  onShowDiagnostics,
  focusEntry = null,
  onEntryFocused,
}: {
  state: ShellState;
  /** For the controller's input while a new emergency combo is recorded. */
  store: PadmateStore;
  /** Opens the logs view (a view inside Settings, not a rail page). */
  onShowLogs: () => void;
  /** Opens the diagnostics view, the same kind of view (ADR-0071). */
  onShowDiagnostics: () => void;
  /** Move focus to this view's button after returning from the view. */
  focusEntry?: "logs" | "diagnostics" | null;
  onEntryFocused?: () => void;
  /** Kept for the shell's page contract; this page uses inline text. */
  t?: Translate;
}) {
  const language = state.language;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const languageId = useId();
  const gapId = useId();
  const gapHintId = useId();
  const logsButton = useRef<HTMLButtonElement>(null);
  const diagnosticsButton = useRef<HTMLButtonElement>(null);
  const config = state.config;
  const editable = !!config && !config.problem;
  const saved = readBasicSettings(config);
  const [saving, setSaving] = useState<BasicSettings | null>(null);
  const [savedFrom, setSavedFrom] = useState<string | null>(null);
  const [released, setReleased] = useState(false);
  const [startupBusy, setStartupBusy] = useState(false);
  // The controller and family the combo dialog was opened for.
  const [comboFor, setComboFor] = useState<string | null>(null);
  // The gap as typed, while it differs from what is saved.
  const [gapText, setGapText] = useState<string | null>(null);
  const shown = saving ?? saved;
  const disabled = !editable || state.busy || !!saving;

  useEffect(() => {
    if (!released) return;
    const timer = setTimeout(() => setReleased(false), 3000);
    return () => clearTimeout(timer);
  }, [released]);

  // Start at sign-in may have changed from the tray since it was read:
  // read it when the page opens and whenever the window comes back.
  const refreshStartup = state.refreshStartup;
  useEffect(() => {
    const read = () => void refreshStartup();
    read();
    window.addEventListener("focus", read);
    return () => window.removeEventListener("focus", read);
  }, [refreshStartup]);
  const startup = state.startup;
  const toggleStartup = async (enabled: boolean) => {
    setStartupBusy(true);
    // A failure shows in the shell's alert.
    await state.setStartup(enabled);
    setStartupBusy(false);
  };

  useEffect(() => {
    if (!focusEntry) return;
    (focusEntry === "logs" ? logsButton : diagnosticsButton).current?.focus();
    onEntryFocused?.();
  }, [focusEntry, onEntryFocused]);

  // Every change is written at once with the revision it was read with,
  // and only its own field.
  const save = (change: Partial<BasicSettings>) => {
    if (!config || disabled) return;
    setSaving({ ...shown, ...change });
    setSavedFrom(config.revision);
    void state.saveSettings(change).finally(() => setSaving(null));
  };
  const gapInvalid = gapText !== null && parseEventGap(gapText) === null;
  // The gap is saved when the field is left or Enter is pressed; a value
  // outside 0–50 is not saved, and the field shows the saved one again.
  const commitGap = () => {
    if (gapText === null) return;
    const next = parseEventGap(gapText);
    setGapText(null);
    if (next !== null && next !== shown.eventGapMs) save({ eventGapMs: next });
  };
  const outcome = saving
    ? "saving"
    : savedFrom === null
      ? "idle"
      : config && config.revision !== savedFrom
        ? "saved"
        : "failed";

  // The save state shows only briefly: the switch shows the value, and a
  // failure's reason stays in the shell's error alert.
  useEffect(() => {
    if (outcome !== "saved" && outcome !== "failed") return;
    const timer = setTimeout(
      () => setSavedFrom(null),
      outcome === "saved" ? 3000 : 6000,
    );
    return () => clearTimeout(timer);
  }, [outcome]);

  // The timing rows and the combo dialog write their own config.json
  // members (UI-19), shown saving like the switches. A change that alters
  // nothing writes nothing.
  const failed = (kind: "noLibrary" | "brokenConfig" | "error") => ({
    ok: false as const,
    failure: {
      kind,
      message: kind === "error" ? t("请稍后再试", "Try again shortly") : "",
    },
  });
  const write = async (
    mutate: (config: Config) => Config | null,
    options?: ReportOptions,
  ): Promise<LibraryOutcome> => {
    if (!config) return failed("noLibrary");
    if (!editable) return failed("brokenConfig");
    if (disabled) return failed("error");
    try {
      if (changeConfigText(config.text, mutate) === null)
        return { ok: true, value: undefined };
    } catch {
      return failed("brokenConfig");
    }
    setSaving(shown);
    setSavedFrom(config.revision);
    try {
      return await state.updateConfig(mutate, options);
    } finally {
      setSaving(null);
    }
  };

  // The calibration dialog (IN-14) writes one change after another while it
  // is open, each behind the one before (the store queues them), so unlike
  // write it does not turn away a change made while the last one saves.
  const writeCalibration = async (
    mutate: (config: Config) => Config | null,
    options?: ReportOptions,
  ): Promise<LibraryOutcome> => {
    if (!config) return failed("noLibrary");
    if (!editable) return failed("brokenConfig");
    setSaving(shown);
    setSavedFrom(config.revision);
    try {
      return await state.updateConfig(mutate, options);
    } finally {
      setSaving(null);
    }
  };

  const device =
    state.status?.devices.find(
      (entry) => entry.info.id === state.selectedDevice,
    ) ?? state.status?.devices[0];
  // The daemon's own combo for the controller (UI-19), by its family.
  const combo = deviceCombo(state.status, device?.info.id);
  const family = combo && isPanicFamily(combo.family) ? combo.family : null;
  const parsed = editable ? readConfig(config) : null;
  const configured = !!family && !!parsed?.safety?.panic?.[family];
  // The dialog edits the family of the controller it was opened for. When
  // that controller goes away, or another one is selected, it closes and
  // does not come back by itself.
  const comboKey = device && family ? `${device.info.id}:${family}` : null;
  const comboOpen = comboFor !== null && comboFor === comboKey;
  useEffect(() => {
    if (comboFor !== null && comboFor !== comboKey) setComboFor(null);
  }, [comboFor, comboKey]);
  // Every controller, and joystick that SDL does not know (IN-19), and the one
  // whose calibration is open. When that controller goes away the dialog
  // closes and does not come back.
  const controllers = state.status?.devices ?? [];
  const [calibrationFor, setCalibrationFor] = useState<string | null>(null);
  const calibrating = controllers.find(
    (entry) =>
      entry.key === calibrationFor &&
      entry.info.gamepad &&
      sticksOf(entry.info.axisMask).length > 0,
  );
  useEffect(() => {
    if (calibrationFor !== null && !calibrating) setCalibrationFor(null);
  }, [calibrationFor, calibrating]);
  // The mapping wizard and 恢复默认映射 (IN-18, IN-19), by connection ID: the
  // key of a joystick SDL does not know is the same for every one of a model.
  // Each closes when its controller goes away.
  const [wizardFor, setWizardFor] = useState<number | null>(null);
  const wizardPad = controllers.find((entry) => entry.info.id === wizardFor);
  useEffect(() => {
    if (wizardFor !== null && !wizardPad) setWizardFor(null);
  }, [wizardFor, wizardPad]);
  const [restoreFor, setRestoreFor] = useState<number | null>(null);
  const restorePad = controllers.find((entry) => entry.info.id === restoreFor);
  useEffect(() => {
    if (restoreFor !== null && !restorePad) setRestoreFor(null);
  }, [restoreFor, restorePad]);
  const version = versionText(state.status?.version);
  const restartPending = !!config?.restartRequired && !config.problem;
  const reload = (
    <button
      type="button"
      className="btn"
      disabled={state.busy}
      onClick={() => void state.refresh()}
    >
      {t("重新读取", "Reload")}
    </button>
  );

  return (
    <>
      {editable && (
        // A live region that stays mounted; it is empty while idle.
        <p className="page-autosave" role="status" data-state={outcome}>
          {outcome === "saving" ? (
            t("正在保存…", "Saving…")
          ) : outcome === "saved" ? (
            <>
              <Icon
                name="check"
                size={13}
                stroke={2.2}
                className="page-autosave-icon"
              />
              {t("已自动保存", "Saved automatically")}
            </>
          ) : outcome === "failed" ? (
            <>
              <Icon name="warn" size={13} stroke={2} />
              {t("未保存，请重试", "Not saved. Try again.")}
            </>
          ) : null}
        </p>
      )}
      <div className="page-scroll">
        <div className="page-column page-column-settings">
          {!config ? (
            <Notice
              kind="warning"
              title={t(
                "当前启动方式无法保存设置",
                "Settings cannot be saved in this session",
              )}
              actions={reload}
            >
              <span>
                {t(
                  "请使用配置库启动 padmate。",
                  "Start padmate with a profile library.",
                )}
              </span>
              {state.configError && (
                <span className="page-mono-message">{state.configError}</span>
              )}
            </Notice>
          ) : config.problem ? (
            <Notice
              kind="error"
              title={t("无法读取设置文件", "The settings file cannot be read")}
              actions={reload}
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
          {restartPending && (
            // The daemon only says that config.json differs from what it
            // runs, not which setting, so the note names no row.
            <Notice kind="info" role="status">
              {t(
                "部分设置需重启 padmate 后生效。",
                "Some settings apply after padmate restarts.",
              )}
            </Notice>
          )}

          <section className="page-section">
            <h2 className="section-title">{t("通用", "General")}</h2>
            <div className="card">
              <div className="row">
                <div className="row-text">
                  <label className="row-title" htmlFor={languageId}>
                    {t("语言", "Language")}
                  </label>
                </div>
                <select
                  id={languageId}
                  className="select page-select"
                  value={language}
                  onChange={(event) =>
                    state.setLanguage(
                      event.target.value === "en" ? "en" : "zh-CN",
                    )
                  }
                >
                  <option value="zh-CN">简体中文</option>
                  <option value="en">English</option>
                </select>
              </div>
              {startup && (
                // UI-19: the daemon's own entry; null with a daemon that
                // has no startup.get.
                <div className="row">
                  <div className="row-text">
                    <span className="row-title">
                      {t("登录时启动", "Start at sign-in")}
                      {startupDryRun(startup) && (
                        <span
                          className="tag amber"
                          title={t(
                            "此 padmate 处于演练模式，登录时启动后也不发送按键",
                            "This padmate runs a dry run; started at sign-in, it sends no keys either",
                          )}
                        >
                          {t("演练", "Dry run")}
                        </span>
                      )}
                    </span>
                  </div>
                  <Switch
                    label={t("登录时启动", "Start at sign-in")}
                    checked={startup.enabled}
                    disabled={!startup.available || startupBusy}
                    onChange={(enabled) => void toggleStartup(enabled)}
                  />
                </div>
              )}
            </div>
          </section>

          <OverlaySection
            language={language}
            settings={state.overlay}
            onChange={state.setOverlay}
          />

          <FeedbackSection
            language={language}
            settings={shown}
            disabled={disabled}
            onChange={save}
          />

          <section className="page-section">
            <h2 className="section-title">{t("安全", "Safety")}</h2>
            <div className="card">
              <div className="row">
                <div className="row-text">
                  <span className="row-title">
                    {t("紧急暂停", "Emergency pause")}
                  </span>
                  <span className="row-hint">
                    {combo
                      ? t(
                          `按住 ${seconds(combo.holdMs, language)}：松开所有按键并暂停`,
                          `Hold ${seconds(combo.holdMs, language)} to release all keys and pause`,
                        )
                      : t(
                          "连接手柄后可查看和修改",
                          "Connect a controller to see or change it",
                        )}
                  </span>
                </div>
                <div className="page-row-control">
                  {combo && (
                    <ComboKeys
                      device={device}
                      buttons={combo.buttons}
                      family={combo.family}
                    />
                  )}
                  <button
                    type="button"
                    className="btn"
                    aria-label={t("修改紧急暂停", "Change emergency pause")}
                    disabled={!comboKey || disabled}
                    onClick={() => setComboFor(comboKey)}
                  >
                    {t("修改", "Change")}
                  </button>
                </div>
              </div>
              <div className="row">
                <div className="row-text">
                  <span className="row-title">
                    {t("松开所有按键", "Release all keys")}
                  </span>
                </div>
                <div className="page-row-control">
                  {released && !state.error && (
                    <span className="page-done" role="status">
                      <Icon name="check" size={13} stroke={2.2} />
                      {t("已松开", "Released")}
                    </span>
                  )}
                  <button
                    type="button"
                    className="btn"
                    disabled={state.busy}
                    onClick={() =>
                      void state.releaseAll().then(() => setReleased(true))
                    }
                  >
                    <Icon name="release" size={14} stroke={2} />
                    {t("立即松开", "Release now")}
                  </button>
                </div>
              </div>
            </div>
          </section>

          <CalibrationSection
            language={language}
            devices={controllers}
            config={parsed}
            disabled={!editable || state.busy}
            onCalibrate={(entry: DeviceEntry) => setCalibrationFor(entry.key)}
            onIdentify={(entry: DeviceEntry) => state.identifyDevice(entry.key)}
            onRemap={(entry: DeviceEntry) => setWizardFor(entry.info.id)}
            onRestoreMapping={(entry: DeviceEntry) =>
              setRestoreFor(entry.info.id)
            }
          />

          <TimingSection
            language={language}
            config={parsed}
            disabled={disabled}
            onSave={(key, value) =>
              void write((source) => setGlobalTiming(source, key, value))
            }
          />

          <section className="page-section">
            <h2 className="section-title">{t("高级", "Advanced")}</h2>
            <div className="card">
              <div className="row">
                <div className="row-text">
                  <span className="row-title">
                    {t(
                      "在 padmate 窗口中也发送按键",
                      "Send keys to the padmate window too",
                    )}
                    {shown.sendToOwnWindow && (
                      <span className="tag amber">{t("已开启", "On")}</span>
                    )}
                  </span>
                  <span className="row-hint">
                    {t("用于调试", "For debugging")}
                  </span>
                </div>
                <Switch
                  label={t(
                    "在 padmate 窗口中也发送按键",
                    "Send keys to the padmate window too",
                  )}
                  checked={shown.sendToOwnWindow}
                  disabled={disabled}
                  onChange={(sendToOwnWindow) => save({ sendToOwnWindow })}
                />
              </div>
              <div className="row">
                <div className="row-text">
                  <label className="row-title" htmlFor={gapId}>
                    {t("按键间隔", "Key gap")}
                  </label>
                  <span className="row-hint" id={gapHintId}>
                    {t(
                      "个别软件未收到按键时，请调大此值",
                      "Raise it if an app misses keys",
                    )}
                  </span>
                </div>
                <span className="number-with-unit">
                  <input
                    id={gapId}
                    className="input input-number"
                    type="number"
                    inputMode="numeric"
                    min={0}
                    max={EVENT_GAP_MAX_MS}
                    step={1}
                    value={gapText ?? String(shown.eventGapMs)}
                    disabled={disabled}
                    aria-invalid={gapInvalid || undefined}
                    aria-describedby={gapHintId}
                    onChange={(event) => setGapText(event.currentTarget.value)}
                    onBlur={commitGap}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") commitGap();
                    }}
                  />
                  <span>{t("毫秒", "ms")}</span>
                </span>
              </div>
            </div>
          </section>

          {/* PKG-10: 自动检查更新 and 检查更新; the window's own setting. */}
          <UpdatesSection api={store.getState().api} language={language} />

          <section className="page-section">
            <h2 className="section-title">{t("关于", "About")}</h2>
            <div className="card">
              <div className="row">
                <div className="row-text">
                  <span className="row-title">{t("日志", "Logs")}</span>
                </div>
                <button
                  ref={logsButton}
                  type="button"
                  className="btn"
                  aria-label={t("查看日志", "View logs")}
                  onClick={onShowLogs}
                >
                  {t("查看", "View")}
                </button>
              </div>
              <div className="row">
                <div className="row-text">
                  <span className="row-title">{t("诊断", "Diagnostics")}</span>
                </div>
                <button
                  ref={diagnosticsButton}
                  type="button"
                  className="btn"
                  aria-label={t("查看诊断", "View diagnostics")}
                  onClick={onShowDiagnostics}
                >
                  {t("查看", "View")}
                </button>
              </div>
              <div className="row">
                <div className="row-text">
                  <span className="row-title">
                    {version ? `padmate ${version}` : "padmate"}
                  </span>
                </div>
                <ModelLicenses language={language} version={version} />
              </div>
            </div>
          </section>
        </div>
      </div>
      {calibrating && (
        <CalibrationDialog
          open
          language={language}
          store={store}
          device={calibrating}
          profileId={state.status?.profileId ?? ""}
          config={parsed}
          disabled={!editable || state.busy}
          onWrite={writeCalibration}
          onClose={() => setCalibrationFor(null)}
        />
      )}
      {wizardPad && (
        <MappingWizardDialog
          open
          language={language}
          api={store.getState().api}
          device={wizardPad}
          refresh={() => state.refresh()}
          onClose={() => setWizardFor(null)}
        />
      )}
      <ResetMappingDialog
        device={restorePad ?? null}
        language={language}
        api={store.getState().api}
        refresh={() => state.refresh()}
        onClose={() => setRestoreFor(null)}
      />
      {device && combo && family && (
        <PanicComboDialog
          open={comboOpen}
          language={language}
          store={store}
          device={device}
          combo={combo}
          configured={configured}
          busy={disabled}
          onSave={(next, options) =>
            write((source) => setPanicCombo(source, family, next), options)
          }
          onClose={() => setComboFor(null)}
        />
      )}
    </>
  );
}
