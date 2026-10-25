import { type CSSProperties, useEffect, useId, useRef, useState } from "react";
import { useStore } from "zustand";
import type { DeviceEntry, DeviceIdentify } from "../../shared/api";
import type { Config } from "../../shared/generated/schema";
import {
  CALIBRATION_DEFAULTS,
  clampStick,
  clearCalibration,
  DEADZONE_MAX,
  hasCalibration,
  OUTER_MIN,
  profileStickValues,
  type StickName,
  type StickValues,
  setStickCalibration,
  shownStick,
  stickPercent,
  sticksOf,
} from "../calibration-model";
import { stickDeflection } from "../controller-model";
import { failureText, type LibraryOutcome } from "../library-model";
import { hasCustomMapping } from "../mapping-model";
import { batteryTags } from "../settings-model";
import type { Language, PadmateStore, ReportOptions } from "../store";
import { Dialog, MenuButton, type MenuEntry } from "../ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** How long after the last move of a slider the change is written. */
export const SAVE_DELAY_MS = 400;

type Draft = Partial<Record<StickName, Partial<StickValues>>>;

const empty = (draft: Draft) =>
  Object.values(draft).every(
    (values) => Object.keys(values ?? {}).length === 0,
  );

/** draft without the members that hold the value sent. */
function without(draft: Draft, sent: Draft): Draft {
  const out: Draft = {};
  for (const stick of ["ls", "rs"] as const) {
    const left: Partial<StickValues> = { ...draft[stick] };
    for (const member of ["deadzone", "saturation"] as const)
      if (left[member] === sent[stick]?.[member]) delete left[member];
    if (Object.keys(left).length) out[stick] = left;
  }
  return out;
}

/** config.json with the members of draft written for the controller key. */
function applyDraft(config: Config, key: string, draft: Draft): Config | null {
  let next = config;
  for (const stick of ["ls", "rs"] as const) {
    const values = draft[stick];
    if (!values) continue;
    next = setStickCalibration(next, key, stick, values) ?? next;
  }
  return next === config ? null : next;
}

/** How long 识别 stays disabled after a click: the cues last about 1.5 s. */
export const IDENTIFY_COOLDOWN_MS = 2000;

/**
 * Settings → 手柄调校 (IN-14, IN-13, IN-18, IN-19): the connected controllers,
 * each with a button that makes it buzz and flash, to tell which it is, and one
 * that opens its calibration dialog. A controller whose battery the daemon has
 * read says so beside its name (IN-12): "电量 80%", and "充电中" while it
 * charges. A joystick that SDL does not know is listed too, as "未识别为手柄"
 * with 建立映射… (the mapping wizard); a known controller has 重新映射… in the
 * menu of its row, and 恢复默认映射 when config.json holds a mapping of its own.
 */
export function CalibrationSection({
  language,
  devices,
  config,
  disabled,
  onCalibrate,
  onIdentify,
  onRemap,
  onRestoreMapping,
}: {
  language: Language;
  devices: DeviceEntry[];
  /** The parsed config.json; null while it cannot be read. */
  config: Config | null;
  disabled: boolean;
  onCalibrate: (device: DeviceEntry) => void;
  /** Resolves to the cues the controller can give, or null when it failed. */
  onIdentify: (device: DeviceEntry) => Promise<DeviceIdentify | null>;
  /** Opens the mapping wizard for the controller. */
  onRemap: (device: DeviceEntry) => void;
  /** Asks to take the controller's own mapping away. */
  onRestoreMapping: (device: DeviceEntry) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  return (
    <section className="page-section">
      <h2 className="section-title">
        {t("手柄调校", "Controller calibration")}
      </h2>
      <div className="card">
        {devices.length === 0 ? (
          <div className="row">
            <div className="row-text">
              <span className="row-title page-muted">
                {t("未连接手柄", "No controller connected")}
              </span>
            </div>
          </div>
        ) : (
          devices.map((device) => (
            <ControllerRow
              // Joysticks of one model that SDL does not know share a key.
              key={device.info.id}
              language={language}
              device={device}
              calibrated={hasCalibration(config, device.key)}
              custom={hasCustomMapping(config, device.key)}
              disabled={disabled}
              onCalibrate={onCalibrate}
              onIdentify={onIdentify}
              onRemap={onRemap}
              onRestoreMapping={onRestoreMapping}
            />
          ))
        )}
      </div>
    </section>
  );
}

/**
 * One controller of the section. 识别 is disabled while the request runs and
 * for IDENTIFY_COOLDOWN_MS after the click, so that the cues are not asked
 * again before they end. A controller that can show nothing says so under
 * its name until the next try. The place for that note is always on the page,
 * empty until there is something to say, because a screen reader announces
 * the change of a live region that was there, and often not one that arrives
 * with its text; 识别 names it as its description.
 */
function ControllerRow({
  language,
  device,
  calibrated,
  custom,
  disabled,
  onCalibrate,
  onIdentify,
  onRemap,
  onRestoreMapping,
}: {
  language: Language;
  device: DeviceEntry;
  calibrated: boolean;
  /** config.json holds a mapping of its own for the controller. */
  custom: boolean;
  disabled: boolean;
  onCalibrate: (device: DeviceEntry) => void;
  onIdentify: (device: DeviceEntry) => Promise<DeviceIdentify | null>;
  onRemap: (device: DeviceEntry) => void;
  onRestoreMapping: (device: DeviceEntry) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [pending, setPending] = useState(false);
  const [cooling, setCooling] = useState(false);
  const [silent, setSilent] = useState(false);
  const noteId = useId();
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
    };
  }, []);
  const identify = () => {
    setPending(true);
    setCooling(true);
    setSilent(false);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCooling(false), IDENTIFY_COOLDOWN_MS);
    // The store's action never rejects; if another caller's does, the row
    // is free again like after a failure.
    void onIdentify(device)
      .catch(() => null)
      .then((cues) => {
        if (!mounted.current) return;
        setPending(false);
        if (cues) setSilent(!cues.rumble && !cues.led && !cues.player);
      });
  };
  const name = device.info.name;
  // A joystick SDL has no mapping for has no sticks to calibrate and cannot be
  // identified; a controller without a stick has nothing to calibrate either.
  const gamepad = device.info.gamepad;
  const calibratable = gamepad && sticksOf(device.info.axisMask).length > 0;
  const menu: MenuEntry[] = [];
  if (gamepad)
    menu.push({
      label: t("重新映射…", "Remap…"),
      disabled,
      onSelect: () => onRemap(device),
    });
  if (custom)
    menu.push({
      label: t("恢复默认映射", "Restore default mapping"),
      disabled,
      onSelect: () => onRestoreMapping(device),
    });
  const more = menu.length > 0 && (
    <MenuButton
      label={t(`更多操作 ${name}`, `More actions for ${name}`)}
      items={menu}
    />
  );
  if (!gamepad)
    return (
      <div className="row">
        <div className="row-text">
          <span className="row-title">
            {name}
            <span className="tag amber">
              {t("未识别为手柄", "Not recognised as a gamepad")}
            </span>
          </span>
        </div>
        <div className="page-row-control">
          <button
            type="button"
            className="btn"
            aria-label={t(`建立映射 ${name}`, `Set up mapping for ${name}`)}
            disabled={disabled}
            onClick={() => onRemap(device)}
          >
            {t("建立映射…", "Set up mapping…")}
          </button>
          {more}
        </div>
      </div>
    );
  return (
    <div className="row">
      <div className="row-text">
        <span className="row-title">
          {device.info.name}
          {custom && (
            <span className="tag amber">
              {t("自定义映射", "Custom mapping")}
            </span>
          )}
          {calibrated && (
            <span className="tag amber">{t("已调校", "Calibrated")}</span>
          )}
          {batteryTags(device.battery, language).map((tag) => (
            <span key={tag} className="tag">
              {tag}
            </span>
          ))}
        </span>
        <span id={noteId} className="row-note" role="status">
          {silent
            ? t(
                "该手柄无法震动或闪灯",
                "This controller cannot rumble or flash.",
              )
            : ""}
        </span>
      </div>
      <div className="page-row-control">
        <button
          type="button"
          className="btn"
          aria-label={t(
            `识别 ${device.info.name}`,
            `Identify ${device.info.name}`,
          )}
          aria-describedby={noteId}
          disabled={pending || cooling}
          onClick={identify}
        >
          {t("识别", "Identify")}
        </button>
        {calibratable && (
          <button
            type="button"
            className="btn"
            aria-label={t(
              `调校 ${device.info.name}`,
              `Calibrate ${device.info.name}`,
            )}
            disabled={disabled}
            onClick={() => onCalibrate(device)}
          >
            {t("调校", "Calibrate")}
          </button>
        )}
        {more}
      </div>
    </div>
  );
}

const RADIUS = 52;
const CENTER = 56;

/**
 * The stick as it is held now, inside its dead zone circle and its outer
 * circle: the raw position, so the circles can be set against it.
 */
export function CalibrationStickView({
  store,
  device,
  stick,
  values,
  label,
}: {
  store: PadmateStore;
  device: number;
  stick: StickName;
  values: StickValues;
  label: string;
}) {
  const input = useStore(store, (state) => state.inputs[device]);
  const { x, y, length } = stickDeflection(
    input,
    stick === "ls" ? "left" : "right",
  );
  return (
    <svg
      className="calibration-view"
      width="112"
      height="112"
      viewBox="0 0 112 112"
      role="img"
      aria-label={label}
      data-active={length > values.deadzone}
    >
      <circle cx={CENTER} cy={CENTER} r={RADIUS} className="calibration-ring" />
      <path d="M56 4v104M4 56h104" className="calibration-cross" />
      <circle
        cx={CENTER}
        cy={CENTER}
        r={Math.min(1, values.saturation) * RADIUS}
        className="calibration-outer"
      />
      <circle
        cx={CENTER}
        cy={CENTER}
        r={Math.max(1.5, values.deadzone * RADIUS)}
        className="calibration-dead"
      />
      <circle
        cx={CENTER + x * RADIUS}
        cy={CENTER + y * RADIUS}
        r="5"
        className="calibration-dot"
      />
    </svg>
  );
}

function CalibrationSlider({
  label,
  value,
  min,
  max,
  disabled,
  onChange,
}: {
  label: string;
  /** A fraction of travel. */
  value: number;
  min: number;
  max: number;
  disabled: boolean;
  onChange: (percent: number) => void;
}) {
  const percent = stickPercent(value);
  const low = stickPercent(min);
  const high = stickPercent(max);
  const shown = Math.max(low, Math.min(high, percent));
  return (
    <div className="calibration-slider">
      <div className="calibration-slider-head">
        <span>{label}</span>
        <output className="calibration-value">{percent}%</output>
      </div>
      <input
        type="range"
        className="calibration-range"
        min={low}
        max={high}
        step={1}
        value={shown}
        aria-label={label}
        aria-valuetext={`${percent}%`}
        disabled={disabled}
        style={
          {
            "--calibration-fill": `${((shown - low) / (high - low)) * 100}%`,
          } as CSSProperties
        }
        onChange={(event) => onChange(event.currentTarget.valueAsNumber)}
      />
    </div>
  );
}

/**
 * Calibration of one controller (IN-14): the dead zone and outer edge of each
 * stick it has, against a live view of the stick. A move is written to
 * config.json under the controller's key after SAVE_DELAY_MS without another,
 * and when the dialog closes. Until a stick is moved it shows the values of
 * the profile in use.
 */
export function CalibrationDialog({
  open,
  language,
  store,
  device,
  profileId,
  config,
  disabled,
  onWrite,
  onClose,
}: {
  open: boolean;
  language: Language;
  store: PadmateStore;
  device: DeviceEntry;
  /** The profile in use, whose values show where the controller sets none. */
  profileId: string;
  /** The parsed config.json. */
  config: Config | null;
  /** Settings cannot be written now. */
  disabled: boolean;
  /**
   * Writes one change to config.json; the dialog shows a failure itself
   * while it is open (options.inline).
   */
  onWrite: (
    mutate: (config: Config) => Config | null,
    options: ReportOptions,
  ) => Promise<LibraryOutcome>;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const key = device.key;
  const [profileText, setProfileText] = useState<string | null | undefined>();
  // The moves not saved yet, as the sliders show them.
  const [draft, setDraft] = useState<Draft>({});
  const [failure, setFailure] = useState<string | null>(null);
  const pending = useRef<Draft>({});
  // The controller the moves in pending are for.
  const pendingKey = useRef(key);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const chain = useRef<Promise<void>>(Promise.resolve());
  const shownRef = useRef(false);
  const latest = useRef({ onWrite, language, key });
  latest.current = { onWrite, language, key };
  useEffect(() => {
    shownRef.current = open;
    return () => {
      shownRef.current = false;
    };
  }, [open]);

  // Writes the moves made so far, behind the write before it.
  const flush = useRef(() => {});
  flush.current = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
    const sent = pending.current;
    if (empty(sent)) return;
    const target = pendingKey.current;
    pending.current = {};
    const run = async () => {
      const { onWrite: write, language: lang } = latest.current;
      let problem: string | null = null;
      try {
        const outcome = await write(
          (source) => applyDraft(source, target, sent),
          { inline: () => shownRef.current },
        );
        if (!outcome.ok) problem = failureText(outcome.failure, lang);
      } catch {
        problem = tx(lang, "请稍后再试", "Try again shortly");
      }
      // What was sent shows from config.json now; a slider moved again
      // since keeps its own value.
      if (!shownRef.current) return;
      setDraft((current) => without(current, sent));
      if (problem) setFailure(problem);
    };
    chain.current = chain.current.then(run, run);
  };

  // Each time it opens, from what is saved; the profile's values are read
  // from the profile in use.
  const api = store.getState().api;
  // biome-ignore lint/correctness/useExhaustiveDependencies: Another controller starts over too, so key is a trigger and not read.
  useEffect(() => {
    if (!open) return;
    pending.current = {};
    setDraft({});
    setFailure(null);
    setProfileText(undefined);
    let current = true;
    if (!profileId) setProfileText(null);
    else
      api
        .getProfile(profileId)
        .then((document) => current && setProfileText(document.text))
        .catch(() => current && setProfileText(null));
    return () => {
      current = false;
      // Closed, or the controller changed: what was moved is saved.
      flush.current();
    };
  }, [open, key, profileId, api]);
  useEffect(
    () => () => {
      clearTimeout(timer.current);
      flush.current();
    },
    [],
  );

  const sticks = sticksOf(device.info.axisMask);
  const profileValues = (stick: StickName): StickValues =>
    profileText === undefined || profileText === null
      ? { ...CALIBRATION_DEFAULTS }
      : profileStickValues(profileText, stick);
  const shown = (stick: StickName): StickValues => ({
    ...shownStick(config, key, stick, profileValues(stick)),
    ...draft[stick],
  });
  const move = (
    stick: StickName,
    member: keyof StickValues,
    percent: number,
  ) => {
    if (disabled) return;
    const current = shown(stick);
    const next = clampStick(member, percent / 100, current);
    if (next[member] === current[member]) return;
    pendingKey.current = key;
    pending.current = {
      ...pending.current,
      [stick]: { ...pending.current[stick], [member]: next[member] },
    };
    setDraft((previous) => ({
      ...previous,
      [stick]: { ...previous[stick], [member]: next[member] },
    }));
    setFailure(null);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => flush.current(), SAVE_DELAY_MS);
  };
  const close = () => {
    flush.current();
    onClose();
  };
  const reset = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
    pending.current = {};
    setDraft({});
    setFailure(null);
    const run = async () => {
      const { onWrite: write, language: lang } = latest.current;
      let problem: string | null = null;
      try {
        const outcome = await write((source) => clearCalibration(source, key), {
          inline: () => shownRef.current,
        });
        if (!outcome.ok) problem = failureText(outcome.failure, lang);
      } catch {
        problem = tx(lang, "请稍后再试", "Try again shortly");
      }
      if (shownRef.current && problem) setFailure(problem);
    };
    chain.current = chain.current.then(run, run);
  };
  const stickName = (stick: StickName) =>
    stick === "ls" ? t("左摇杆", "Left stick") : t("右摇杆", "Right stick");
  const saved = hasCalibration(config, key) || !empty(draft);

  return (
    <Dialog
      open={open}
      title={t(`调校 ${device.info.name}`, `Calibrate ${device.info.name}`)}
      closeLabel={t("关闭调校", "Close calibration")}
      onClose={close}
      className="calibration-dialog"
      footer={
        <>
          <button
            type="button"
            className="btn-ghost"
            disabled={!saved || disabled}
            onClick={reset}
          >
            {t("恢复默认", "Reset")}
          </button>
          <span className="spacer" />
        </>
      }
    >
      <div className="calibration-sticks" aria-busy={profileText === undefined}>
        {sticks.map((stick) => {
          const values = shown(stick);
          return (
            <fieldset
              key={stick}
              className="calibration-stick"
              aria-label={stickName(stick)}
            >
              <CalibrationStickView
                store={store}
                device={device.info.id}
                stick={stick}
                values={values}
                label={t(
                  `${stickName(stick)}的位置`,
                  `${stickName(stick)} position`,
                )}
              />
              <div className="calibration-sliders">
                <span className="field-label">{stickName(stick)}</span>
                <CalibrationSlider
                  label={t("死区", "Dead zone")}
                  value={values.deadzone}
                  min={0}
                  max={DEADZONE_MAX}
                  disabled={disabled}
                  onChange={(percent) => move(stick, "deadzone", percent)}
                />
                <CalibrationSlider
                  label={t("推到底", "Full push")}
                  value={values.saturation}
                  min={OUTER_MIN}
                  max={1}
                  disabled={disabled}
                  onChange={(percent) => move(stick, "saturation", percent)}
                />
              </div>
            </fieldset>
          );
        })}
      </div>
      {failure && (
        <p className="calibration-failure" role="alert">
          {failure}
        </p>
      )}
    </Dialog>
  );
}
