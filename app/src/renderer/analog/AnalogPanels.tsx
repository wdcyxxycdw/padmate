import {
  type MouseEvent,
  type RefObject,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import type { ProfileEntry, ScriptRegistration } from "../../shared/api";
import type {
  Profile,
  ProfileBinding,
  ProfileDpad,
  ProfileStick,
  ProfileTiming,
  ProfileTrigger,
} from "../../shared/generated/schema";
import { axisPosition } from "../controller-model";
import { baseBinding, canonicalControl, setBaseBinding } from "../editor-model";
import type { EditOptions } from "../editor-store";
import type { Language, PadInput } from "../store";
import { useCommitField } from "../ui/commit-field";
import { Disclosure, Segmented, Switch } from "../ui/controls";
import { Keycaps } from "../ui/Keycaps";
import {
  type AnalogIssue,
  analogGroup,
  analogIssues,
  axisThresholdDefault,
  cardinalDirections,
  combinedDirection,
  convertToFullPull,
  type Direction,
  dpadDefaults,
  dpadDirections,
  fromPercent,
  fullControl,
  knobControls,
  knobStepRange,
  legacyTwoStage,
  modeDirections,
  mouseSmoothingRange,
  mouseSpeedRange,
  rawStickControls,
  ringControls,
  type StickReading,
  setAnalogSettings,
  setAxisThreshold,
  stickDefaults,
  stickReading,
  toPercent,
  triggerDefaults,
  usesDepthRate,
} from "./analog-editor";
import {
  BindingCard,
  BindingRow,
  BindingSummary,
  ConfirmDialog,
  DirectionGrid,
  directionArrow,
  directionName,
  isBound,
  ParamsMenu,
  SettingField,
  SettingRow,
  SliderField,
  tx,
} from "./analog-parts";
import {
  type AnalogPreset,
  applyAnalogPreset,
  presetChanges,
  presetKeys,
  presetMode,
} from "./analog-presets";
import "./analog-editor.css";

/**
 * Props shared by the analog panels that the key drawer embeds.
 * The drawer owns the header and notices; panels only edit the draft, which
 * the editor saves at once, and only its base key set (ADR-0045, UI-43).
 */
export interface AnalogPanelProps {
  profile: Profile;
  /** Callout control: leftstick, rightstick, lefttrigger, righttrigger or dpad.* */
  control: string;
  onChange: (profile: Profile, options?: EditOptions) => void;
  input?: PadInput;
  language: Language;
  timing: Required<ProfileTiming>;
  registrations: ScriptRegistration[];
  profiles: ProfileEntry[];
  actions?: { name: string; label?: string }[];
  /**
   * The profile cannot be edited now (read-only or loading). Rows, cells
   * and gesture cards still open so their settings can be read; only the
   * controls that change the draft are disabled.
   */
  disabled?: boolean;
  /**
   * Opens the parameter menu (testid analog-params) whenever it changes to a
   * new value above 0, e.g. on a right click of the stick's controller chip.
   * Only the stick panel uses it: trigger and d-pad drawers open the key's own
   * menu instead.
   */
  menuRequest?: number;
  /**
   * The sticks the live controller has its own calibration for (IN-14): its
   * dead zone and outer edge then replace this profile's on that controller.
   */
  calibrated?: { ls: boolean; rs: boolean };
}

type StickMode = NonNullable<ProfileStick["mode"]>;
type DpadMode = NonNullable<ProfileDpad["mode"]>;

const diagonalGrid: Direction[] = [
  "upleft",
  "upright",
  "downleft",
  "downright",
];
/** Grid order: the cardinals as in the design, then the corners spatially. */
function gridDirections(mode: string): Direction[] {
  if (mode === "fourWayDiagonal") return diagonalGrid;
  if (mode === "eightWay") return [...cardinalDirections, ...diagonalGrid];
  return [...cardinalDirections];
}
const allDirections: Direction[] = [...cardinalDirections, ...diagonalGrid];

/** The response curves a stick or trigger offers (ANA-13). */
function curveOptions(language: Language) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  return [
    { value: "linear" as const, label: t("线性", "Linear") },
    { value: "quadratic" as const, label: t("二次", "Quadratic") },
    { value: "cubic" as const, label: t("三次", "Cubic") },
  ];
}

/** The control a drawer opened on that this panel should expand, if any. */
function focusTarget(control: string, group: string, own: string[]) {
  const key = canonicalControl(control);
  return analogGroup(key) === analogGroup(group) && own.includes(key)
    ? key
    : null;
}

/** Threshold the engine uses for raw half-axis controls. */
function rawThreshold(profile: Profile) {
  const value = profile.analog?.axisThreshold ?? axisThresholdDefault;
  if (value > 0) return value;
  const fallback =
    profile.analog?.ls?.directionThreshold ?? stickDefaults.directionThreshold;
  return fallback > 0 ? fallback : axisThresholdDefault;
}

function issueText(issue: AnalogIssue, language: Language) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  if (issue.code === "saturation")
    return t(
      "“推到底”必须大于“死区”。",
      "Full push must be greater than the dead zone.",
    );
  if (issue.code === "release")
    return t(
      "“松开”不能大于“按下”。",
      "Release must not be greater than press.",
    );
  if (issue.code === "fullRelease")
    return t(
      "“按到底：松开”不能大于“按到底：按下”。",
      "Full pull release must not be greater than full pull press.",
    );
  if (issue.code === "fullPress")
    return t(
      "“按到底：按下”必须大于“按下”。",
      "Full pull press must be greater than press.",
    );
  const field = issue.path.split(".").pop() ?? "";
  const names: Record<string, [string, string]> = {
    deadzone: ["死区", "Dead zone"],
    saturation: ["推到底", "Full push"],
    directionThreshold: ["触发幅度", "Trigger point"],
    outerThreshold: ["重推的位置", "Hard push from"],
    knobStepDeg: ["每格角度", "Step"],
    mouseSpeed: ["速度", "Speed"],
    mouseSmoothingMs: ["平滑", "Smoothing"],
    diagonalWidth: ["斜向扇区宽度", "Diagonal width"],
    hysteresisDeg: ["角度滞回", "Angle hysteresis"],
    directionDelayMs: ["方向切换延迟", "Direction delay"],
    press: ["按下", "Press"],
    release: ["松开", "Release"],
    fullPress: ["按到底：按下", "Full pull press"],
    fullRelease: ["按到底：松开", "Full pull release"],
    axisThreshold: ["原始轴阈值", "Raw axis threshold"],
  };
  const [zh, en] = names[field] ?? [field, field];
  return t(`“${zh}”的值超出范围。`, `${en} is out of range.`);
}

function Issues({
  issues,
  language,
}: {
  issues: AnalogIssue[];
  language: Language;
}) {
  if (!issues.length) return null;
  return (
    <div className="analog-errors" role="alert">
      {issues.map((issue) => (
        <p key={`${issue.path}:${issue.code}`}>{issueText(issue, language)}</p>
      ))}
    </div>
  );
}

/**
 * Opens a panel's parameter menu (UI-36): on a right click inside the panel,
 * or when the drawer sends a new `menuRequest`. A collapsed section that holds
 * the menu (the stick's 高级) opens first, so the menu can take focus.
 */
function useParamsMenu(host: RefObject<HTMLElement | null>, menuRequest = 0) {
  const [request, setRequest] = useState(0);
  const open = () => {
    const details = host.current
      ?.querySelector('[data-testid="analog-params"]')
      ?.closest("details");
    if (details) details.open = true;
    setRequest((count) => count + 1);
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: Only a new request opens the menu.
  useLayoutEffect(() => {
    if (menuRequest > 0) open();
  }, [menuRequest]);
  return {
    request,
    onContextMenu: (event: MouseEvent) => {
      event.preventDefault();
      open();
    },
  };
}

/** Whole drawer body for a stick: settings, directions and the stick click. */
export function StickPanel(props: AnalogPanelProps) {
  const group = analogGroup(props.control);
  if (group !== "ls" && group !== "rs") return null;
  return <StickBody key={group} {...props} group={group} />;
}

function StickBody({
  profile,
  control,
  onChange,
  input,
  language,
  timing,
  registrations,
  profiles,
  actions,
  disabled = false,
  menuRequest,
  calibrated,
  group,
}: AnalogPanelProps & { group: "ls" | "rs" }) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const side = group === "ls" ? "left" : "right";
  const stickName =
    group === "ls"
      ? t("左摇杆", "the left stick")
      : t("右摇杆", "the right stick");
  const clickControl = `${side}stick`;
  const activeControl = `${group}.active`;
  const rawControls: string[] = rawStickControls(group);
  const directionControlsAll = allDirections.map(
    (direction) => `${group}.${direction}`,
  );
  const [innerControl, outerControl] = ringControls(group);
  const tickControls: string[] = knobControls(group);
  const ownControls = [
    ...directionControlsAll,
    activeControl,
    innerControl,
    outerControl,
    ...tickControls,
    ...rawControls,
  ];
  const stored = profile.analog?.[group];
  const stick = { ...stickDefaults, ...stored };
  const knob = stick.mode === "knob";
  // The stick moves the pointer (MOU-01): no directions and no ticks.
  const mouse = stick.mode === "mouse";
  // Only the standard modes send directions, so only they have direction settings.
  const directional = !knob && !mouse;
  const cardId = useId();
  const host = useRef<HTMLElement>(null);
  const params = useParamsMenu(host, menuRequest);
  const [expanded, setExpanded] = useState<string | null>(() =>
    focusTarget(control, group, ownControls),
  );
  // biome-ignore lint/correctness/useExhaustiveDependencies: Follow only a new drawer target; the control lists derive from the group, which keys this component.
  useEffect(() => {
    const target = focusTarget(control, group, ownControls);
    if (target) setExpanded(target);
  }, [control]);
  const [preset, setPreset] = useState<AnalogPreset | null>(null);

  const update = (patch: ProfileStick, options?: EditOptions) =>
    onChange(
      setAnalogSettings(profile, group, { ...stored, ...patch }),
      options,
    );
  // The tick of a knob step (HUD-10): off leaves no key in the file.
  const setKnobTick = (on: boolean) => {
    const next: ProfileStick = { ...stored };
    if (on) next.knobTick = true;
    else delete next.knobTick;
    onChange(setAnalogSettings(profile, group, next));
  };
  const bindingFor = (key: string) => baseBinding(profile, key);
  const setBinding = (key: string) => (binding: ProfileBinding | undefined) =>
    onChange(setBaseBinding(profile, key, binding));
  const toggle = (key: string) => setExpanded(expanded === key ? null : key);
  const expandedDirection = allDirections.find(
    (direction) => `${group}.${direction}` === expanded,
  );
  const setMode = (mode: StickMode) => {
    update({ mode });
    // Close a direction or a tick the new mode does not send.
    if (expandedDirection && !modeDirections(mode).includes(expandedDirection))
      setExpanded(null);
    if (mode !== "knob" && expanded && tickControls.includes(expanded))
      setExpanded(null);
  };

  const x = axisPosition(input?.axes[`${side}x`]);
  const y = axisPosition(input?.axes[`${side}y`]);
  const reading = stickReading(x, y, stick, (direction) =>
    isBound(bindingFor(`${group}.${direction}`)),
  );
  const directions = directional ? gridDirections(stick.mode) : [];
  const hidden = allDirections.filter(
    (direction) =>
      !directions.includes(direction) &&
      isBound(bindingFor(`${group}.${direction}`)),
  );
  const raw = rawThreshold(profile);
  const rawValue: Record<string, number> = {
    [`${side}x+`]: x,
    [`${side}x-`]: -x,
    [`${side}y+`]: y,
    [`${side}y-`]: -y,
  };
  const rawDirection: Record<string, Direction> = {
    [`${side}y-`]: "up",
    [`${side}x+`]: "right",
    [`${side}y+`]: "down",
    [`${side}x-`]: "left",
  };
  const issues = analogIssues(profile.analog).filter(
    (issue) =>
      issue.path.startsWith(`analog.${group}.`) ||
      issue.path === "analog.axisThreshold",
  );

  const pressedDirection = combinedDirection(reading.directions);
  // The pointer moves as soon as the stick is out of the dead zone, whatever
  // the trigger point is (it is for the push and ring controls).
  const where =
    mouse && reading.magnitude > 0
      ? t("移动指针", "Moving pointer")
      : reading.active
        ? knob
          ? `${Math.round(reading.angle ?? 0) % 360}°`
          : pressedDirection
            ? directionName(pressedDirection, language)
            : reading.directions
                .map((direction) => directionName(direction, language))
                .join("+")
        : Math.hypot(x, y) < 0.02
          ? t("居中", "centered")
          : reading.inDeadzone
            ? t("死区内", "in dead zone")
            : t("未到触发幅度", "below trigger point");
  const percent = Math.round(reading.magnitude * 100);

  // The pointer mode has none: its settings below say what it does.
  const modeCaption: Partial<Record<StickMode, string>> = {
    standard: t(
      "斜推时，相邻的两个方向键同时按下。",
      "Pushing diagonally presses both neighboring directions.",
    ),
    eightWay: t(
      "斜推时发送单独的斜向按键，共 8 个方向。",
      "Diagonals send their own keys, for eight directions in all.",
    ),
    fourWay: t(
      "只按下最接近的一个方向，斜推时也不会同时按下两个方向。",
      "Only the nearest direction is pressed, even on diagonals.",
    ),
    fourWayDiagonal: t(
      "仅有 4 个斜向，推向哪个角即按下对应的斜向。",
      "Only the four diagonals: the nearest corner is pressed.",
    ),
    knob: t(
      "当作旋钮：每转过设定的角度发一次顺时针或逆时针按键，不发方向键。",
      "Used as a knob: each turn of the set angle sends one clockwise or counter-clockwise key, and no direction keys.",
    ),
  };
  const gridTitle =
    stick.mode === "eightWay"
      ? t("8 个方向的按键", "Keys for 8 directions")
      : stick.mode === "fourWayDiagonal"
        ? t("4 个斜向的按键", "Keys for the 4 diagonals")
        : t("4 个方向的按键", "Keys for 4 directions");
  const directionTitle = (direction: Direction) =>
    `${directionArrow[direction]} ${t(
      `向${directionName(direction, language)}推`,
      `Push ${directionName(direction, language).toLowerCase()}`,
    )}`;
  const card = (key: string, title: string, compact = true) => (
    <BindingCard
      id={cardId}
      title={title}
      compact={compact}
      control={key}
      binding={bindingFor(key)}
      onChange={setBinding(key)}
      onClose={() => setExpanded(null)}
      registrations={registrations}
      timing={timing}
      profiles={profiles}
      actions={actions}
      language={language}
      disabled={disabled}
    />
  );
  const changes = presetChanges(profile, group);
  const askPreset = (next: AnalogPreset) => {
    if (disabled) return;
    if (!changes.length && stick.mode === presetMode(group))
      onChange(applyAnalogPreset(profile, group, next));
    else setPreset(next);
  };
  const hasRaw = rawControls.some((key) => isBound(bindingFor(key)));
  const hasTicks = tickControls.some((key) => isBound(bindingFor(key)));
  const curve = usesDepthRate(profile, group);
  const clickBinding = bindingFor(clickControl);
  const label = useCommitField({
    value: clickBinding.label ?? "",
    onCommit: (text) => {
      const next = { ...clickBinding };
      if (text) next.label = text;
      else delete next.label;
      onChange(setBaseBinding(profile, clickControl, next));
    },
  });

  return (
    <section
      className="analog-panel"
      ref={host}
      aria-label={t("摇杆设置", "Stick settings")}
      data-group={group}
    >
      <fieldset className="analog-fieldset analog-section" disabled={disabled}>
        <label className="field">
          <span className="field-label">
            {t("在手柄图上显示为", "Shown on the controller as")}
          </span>
          <input
            className="input"
            placeholder={
              cardinalDirections.some((direction) =>
                isBound(bindingFor(`${group}.${direction}`)),
              )
                ? t("方向键", "Direction keys")
                : undefined
            }
            {...label.props}
          />
        </label>
      </fieldset>

      <fieldset className="analog-fieldset analog-section" disabled={disabled}>
        <span className="field-label">{t("方向模式", "Direction mode")}</span>
        <Segmented<StickMode>
          label={t("方向模式", "Direction mode")}
          className="analog-mode"
          value={stick.mode}
          onChange={setMode}
          options={[
            { value: "standard", label: t("4 方向", "4-way") },
            { value: "eightWay", label: t("8 方向", "8-way") },
            { value: "fourWay", label: t("只取 1 个方向", "One at a time") },
            ...(stick.mode === "fourWayDiagonal"
              ? [
                  {
                    value: "fourWayDiagonal" as const,
                    label: t("只有斜向", "Diagonals"),
                  },
                ]
              : []),
            { value: "knob" as const, label: t("旋钮", "Knob") },
            { value: "mouse" as const, label: t("鼠标", "Mouse") },
          ]}
        />
        {modeCaption[stick.mode] && (
          <p className="analog-caption">{modeCaption[stick.mode]}</p>
        )}
      </fieldset>

      <fieldset className="analog-fieldset analog-section" disabled={disabled}>
        <span className="field-label">{t("灵敏度", "Sensitivity")}</span>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: Right click is a shortcut; the 参数操作 button opens the same menu. */}
        <div
          className="analog-sensitivity"
          onContextMenu={params.onContextMenu}
        >
          <StickVisual
            x={x}
            y={y}
            settings={stick}
            reading={reading}
            label={t(
              `摇杆实时位置：${where}，${percent}%`,
              `Live stick position: ${where}, ${percent}%`,
            )}
          />
          <div className="analog-sliders">
            <SliderField
              label={t("死区", "Dead zone")}
              hint={t(
                "推动幅度在死区以内时视为未推动。",
                "Travel inside the dead zone is ignored.",
              )}
              value={toPercent(stick.deadzone)}
              language={language}
              onCommit={(value, options) =>
                update({ deadzone: fromPercent(value) }, options)
              }
            />
            <SliderField
              label={t("推到底", "Full push")}
              hint={t(
                "达到此幅度即视为推到底。",
                "Travel at this point counts as fully pushed.",
              )}
              value={toPercent(stick.saturation)}
              language={language}
              onCommit={(value, options) =>
                update({ saturation: fromPercent(value) }, options)
              }
            />
            <span className="analog-readout" data-testid="stick-readout">
              {t(`当前 ${percent}% · ${where}`, `Now ${percent}% · ${where}`)}
            </span>
            {calibrated?.[group] && (
              <span className="analog-readout" data-testid="stick-calibrated">
                {t("本手柄已单独调校", "Calibrated for this controller")}
              </span>
            )}
          </div>
        </div>
        <Issues issues={issues} language={language} />
        <p className="analog-caption">
          {t(
            "摇杆和扳机参数对整个配置生效",
            "Stick and trigger parameters apply to the whole profile",
          )}
        </p>
      </fieldset>

      {knob ? (
        <div className="analog-section">
          <fieldset className="analog-fieldset" disabled={disabled}>
            <SettingField
              language={language}
              label={t("每格角度", "Step")}
              value={stick.knobStepDeg}
              min={knobStepRange.min}
              max={knobStepRange.max}
              integer
              unit="°"
              onCommit={(knobStepDeg) => update({ knobStepDeg })}
            />
            <SettingRow label={t("刻度震动", "Tick haptic")}>
              <Switch
                label={t("刻度震动", "Tick haptic")}
                checked={stick.knobTick}
                onChange={setKnobTick}
              />
            </SettingRow>
          </fieldset>
          {tickControls.map((key, index) => {
            const title =
              index === 0
                ? t("顺时针一格", "Clockwise step")
                : t("逆时针一格", "Counter-clockwise step");
            return (
              <div key={key} className="analog-stack">
                <BindingRow
                  title={title}
                  control={key}
                  binding={bindingFor(key)}
                  expanded={expanded === key}
                  cardId={cardId}
                  registrations={registrations}
                  language={language}
                  actions={actions}
                  profiles={profiles}
                  onToggle={() => toggle(key)}
                />
                {expanded === key && card(key, title)}
              </div>
            );
          })}
          {hidden.length > 0 && (
            <p className="analog-caption">
              {t(
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} 也设置了按键，但旋钮不会发送。`,
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} also have keys, but a knob does not send them.`,
              )}
            </p>
          )}
        </div>
      ) : mouse ? (
        <div className="analog-section">
          <fieldset
            className="analog-fieldset analog-fields"
            disabled={disabled}
          >
            <SliderField
              label={t("速度", "Speed")}
              value={stick.mouseSpeed}
              min={mouseSpeedRange.min}
              max={mouseSpeedRange.max}
              sliderMax={mouseSpeedRange.slider}
              step={10}
              unit="px/s"
              integer
              language={language}
              onCommit={(mouseSpeed, options) =>
                update({ mouseSpeed }, options)
              }
            />
            <SettingRow label={t("响应曲线", "Response curve")}>
              <Segmented<NonNullable<ProfileStick["curve"]>>
                small
                className="analog-mode"
                label={t("响应曲线", "Response curve")}
                value={stick.curve}
                onChange={(next) => update({ curve: next })}
                options={curveOptions(language)}
              />
            </SettingRow>
            <SettingField
              language={language}
              label={t("平滑", "Smoothing")}
              value={stick.mouseSmoothingMs}
              min={mouseSmoothingRange.min}
              max={mouseSmoothingRange.max}
              integer
              unit="ms"
              onCommit={(mouseSmoothingMs) => update({ mouseSmoothingMs })}
            />
          </fieldset>
          {hidden.length > 0 && (
            <p className="analog-caption">
              {t(
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} 也设置了按键，但鼠标模式不会发送。`,
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} also have keys, but the mouse mode does not send them.`,
              )}
            </p>
          )}
          {hasTicks && (
            <p className="analog-caption">
              {t(
                "旋钮的按键也设置了，但当前模式不会发送。",
                "Knob keys are also set, but this mode does not send them.",
              )}
            </p>
          )}
        </div>
      ) : (
        <div className="analog-section">
          <div className="analog-section-head">
            <span className="field-label">{gridTitle}</span>
            <PresetButtons
              language={language}
              disabled={disabled}
              onPreset={askPreset}
            />
          </div>
          <DirectionGrid
            group={group}
            directions={directions}
            bindingFor={bindingFor}
            active={reading.directions}
            expanded={expanded}
            cardId={cardId}
            registrations={registrations}
            language={language}
            actions={actions}
            profiles={profiles}
            label={gridTitle}
            onToggle={toggle}
          />
          {hidden.length > 0 && (
            <p className="analog-caption">
              {t(
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} 也设置了按键，但当前模式不会发送。`,
                `${hidden.map((direction) => directionArrow[direction]).join(" ")} also have keys, but this mode does not send them.`,
              )}
            </p>
          )}
          {hasTicks && (
            <p className="analog-caption">
              {t(
                "旋钮的按键也设置了，但当前模式不会发送。",
                "Knob keys are also set, but this mode does not send them.",
              )}
            </p>
          )}
          {expandedDirection &&
            card(expanded as string, directionTitle(expandedDirection), false)}
        </div>
      )}

      <div className="analog-section">
        <BindingRow
          title={t("按下摇杆", "Click the stick")}
          control={clickControl}
          binding={clickBinding}
          expanded={expanded === clickControl}
          cardId={cardId}
          active={!!input?.buttons[clickControl]}
          registrations={registrations}
          language={language}
          actions={actions}
          profiles={profiles}
          onToggle={() => toggle(clickControl)}
        />
        {expanded === clickControl &&
          card(clickControl, t("按下摇杆", "Click the stick"))}
      </div>

      <Disclosure
        title={t("高级", "Advanced")}
        summary={
          directional
            ? t(
                "触发幅度 · 斜向角度 · 防抖",
                "Trigger point · diagonals · debounce",
              )
            : t("触发幅度", "Trigger point")
        }
        defaultOpen={
          expanded === activeControl ||
          expanded === innerControl ||
          expanded === outerControl ||
          (expanded !== null && rawControls.includes(expanded))
        }
        testId="stick-advanced"
      >
        <div className="analog-advanced">
          <div className="analog-params-row">
            <span className="analog-caption">
              {t(
                "复制、粘贴或恢复此摇杆的参数",
                "Copy, paste or reset this stick's parameters",
              )}
            </span>
            <ParamsMenu
              kind="stick"
              name={stickName}
              settings={stored}
              language={language}
              testId="analog-params"
              disabled={disabled}
              openRequest={params.request}
              onReplace={(settings) =>
                onChange(
                  setAnalogSettings(profile, group, settings as ProfileStick),
                )
              }
            />
          </div>
          <fieldset
            className="analog-fieldset analog-fields"
            disabled={disabled}
          >
            <SettingField
              language={language}
              label={t("触发幅度", "Trigger point")}
              hint={
                mouse
                  ? t(
                      "只用于“朝任意方向推动”、轻推和重推；指针一推出死区就开始移动。",
                      "Only for Push in any direction, Light push and Hard push; the pointer moves as soon as the stick leaves the dead zone.",
                    )
                  : t(
                      "推出死区后，达到此幅度才按下方向。",
                      "After the dead zone, directions press at this travel.",
                    )
              }
              value={toPercent(stick.directionThreshold)}
              min={0}
              max={100}
              unit="%"
              onCommit={(value) =>
                update({ directionThreshold: fromPercent(value) })
              }
            />
            {(stick.mode === "standard" || stick.mode === "eightWay") && (
              <SettingField
                language={language}
                label={t("斜向扇区宽度", "Diagonal width")}
                hint={t(
                  "斜向所占的角度，越大越容易推出斜向。",
                  "The angle a diagonal covers; wider is easier to hit.",
                )}
                value={stick.diagonalWidth}
                min={0.1}
                max={90}
                unit="°"
                onCommit={(diagonalWidth) => update({ diagonalWidth })}
              />
            )}
            {directional && (
              <SettingField
                language={language}
                label={t("角度滞回", "Angle hysteresis")}
                hint={t(
                  "在两个方向的交界处保留额外角度，防止方向来回切换。",
                  "Extra angle at a sector edge, so the direction does not flicker.",
                )}
                value={stick.hysteresisDeg}
                min={0}
                max={45}
                unit="°"
                onCommit={(hysteresisDeg) => update({ hysteresisDeg })}
              />
            )}
            {directional && (
              <SettingField
                language={language}
                label={t("方向切换延迟", "Direction delay")}
                hint={t(
                  "方向改变后等待此时间再切换，0 表示立即切换。",
                  "Wait this long before a direction changes; 0 is immediate.",
                )}
                value={stick.directionDelayMs}
                min={0}
                max={2147483647}
                integer
                unit="ms"
                onCommit={(directionDelayMs) => update({ directionDelayMs })}
              />
            )}
            {/* A mouse-mode stick has its curve beside the speed above. */}
            {curve && !mouse && (
              <SettingRow label={t("响应曲线", "Response curve")}>
                <Segmented<NonNullable<ProfileStick["curve"]>>
                  small
                  className="analog-mode"
                  label={t("响应曲线", "Response curve")}
                  value={stick.curve}
                  onChange={(next) => update({ curve: next })}
                  options={curveOptions(language)}
                />
              </SettingRow>
            )}
            {stick.mode === "eightWay" && (
              <SettingRow
                stacked
                label={t("斜向未设置按键时", "When a diagonal has no keys")}
              >
                <Segmented<"none" | "cardinals">
                  small
                  className="analog-mode"
                  label={t("斜向未设置按键时", "When a diagonal has no keys")}
                  value={stick.diagonalFallback}
                  onChange={(diagonalFallback) => update({ diagonalFallback })}
                  options={[
                    { value: "none", label: t("不发送", "Send nothing") },
                    {
                      value: "cardinals",
                      label: t("按相邻两个方向", "Both neighbors"),
                    },
                  ]}
                />
              </SettingRow>
            )}
            {directional && (
              <SettingRow
                label={t("只有斜向", "Diagonals only")}
                hint={t(
                  "仅使用 4 个斜向，推向哪个角即按下对应的斜向。",
                  "Use only the four diagonals.",
                )}
              >
                <Switch
                  label={t("只有斜向", "Diagonals only")}
                  checked={stick.mode === "fourWayDiagonal"}
                  onChange={(on) =>
                    setMode(on ? "fourWayDiagonal" : "standard")
                  }
                />
              </SettingRow>
            )}
          </fieldset>

          <div className="analog-section">
            <span className="field-label">{t("其他输入", "Other inputs")}</span>
            <BindingRow
              title={t("朝任意方向推动", "Push in any direction")}
              // A knob and a pointer stick have no directions to fire together with.
              hint={
                directional
                  ? t("和方向同时触发", "Fires together with directions")
                  : undefined
              }
              control={activeControl}
              binding={bindingFor(activeControl)}
              expanded={expanded === activeControl}
              cardId={cardId}
              active={reading.active}
              registrations={registrations}
              language={language}
              actions={actions}
              profiles={profiles}
              onToggle={() => toggle(activeControl)}
            />
            {expanded === activeControl &&
              card(activeControl, t("朝任意方向推动", "Push in any direction"))}
            {[
              [innerControl, t("轻推", "Light push"), "inner"],
              [outerControl, t("重推", "Hard push"), "outer"],
            ].map(([key, title, ring]) => (
              <div key={key} className="analog-stack">
                <BindingRow
                  title={title}
                  control={key}
                  binding={bindingFor(key)}
                  expanded={expanded === key}
                  cardId={cardId}
                  active={reading.ring === ring}
                  registrations={registrations}
                  language={language}
                  actions={actions}
                  profiles={profiles}
                  onToggle={() => toggle(key)}
                />
                {expanded === key && card(key, title)}
              </div>
            ))}
            <fieldset className="analog-fieldset" disabled={disabled}>
              <SettingField
                language={language}
                label={t("重推的位置", "Hard push from")}
                value={toPercent(stick.outerThreshold)}
                min={0}
                max={100}
                unit="%"
                onCommit={(value) =>
                  update({ outerThreshold: fromPercent(value) })
                }
              />
            </fieldset>
          </div>

          <div className="analog-section">
            <span className="field-label">{t("原始轴", "Raw axes")}</span>
            <p className="analog-caption">
              {t(
                "原始轴不经过死区和方向判定，超过阈值即按下，可与方向同时触发。",
                "Raw axes skip the dead zone and direction logic: they press past the threshold, together with directions.",
              )}
            </p>
            {rawControls.map((key) => {
              const direction = rawDirection[key];
              const title = `${directionArrow[direction]} ${t(
                `原始轴向${directionName(direction, language)}`,
                `Raw ${directionName(direction, language).toLowerCase()}`,
              )}`;
              return (
                <div key={key} className="analog-stack">
                  <BindingRow
                    title={title}
                    hint={key}
                    control={key}
                    binding={bindingFor(key)}
                    expanded={expanded === key}
                    cardId={cardId}
                    active={(rawValue[key] ?? 0) >= raw}
                    registrations={registrations}
                    language={language}
                    actions={actions}
                    profiles={profiles}
                    onToggle={() => toggle(key)}
                  />
                  {expanded === key && card(key, title)}
                </div>
              );
            })}
            <fieldset className="analog-fieldset" disabled={disabled}>
              <AxisThresholdField
                profile={profile}
                language={language}
                onChange={onChange}
              />
            </fieldset>
          </div>
        </div>
      </Disclosure>

      <ConfirmDialog
        open={preset !== null}
        title={
          preset === "wasd"
            ? t("改为 WASD？", "Use WASD?")
            : t("改为方向键？", "Use the arrow keys?")
        }
        confirmLabel={t("替换", "Replace")}
        language={language}
        onClose={() => setPreset(null)}
        onConfirm={() => {
          if (preset && !disabled)
            onChange(applyAnalogPreset(profile, group, preset));
        }}
      >
        {preset && (
          <PresetSummary
            group={group}
            preset={preset}
            changes={changes}
            modeChanges={stick.mode !== presetMode(group)}
            script={!!profile.script}
            raw={hasRaw}
            language={language}
            actions={actions}
            profiles={profiles}
          />
        )}
      </ConfirmDialog>
    </section>
  );
}

/** The arrow-key and WASD presets for up, right, down and left (ANA-09). */
function PresetButtons({
  language,
  disabled,
  onPreset,
}: {
  language: Language;
  disabled: boolean;
  onPreset: (preset: AnalogPreset) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  return (
    <fieldset className="analog-fieldset analog-presets" disabled={disabled}>
      <button
        type="button"
        className="analog-chip-btn"
        title={t(
          "将上下左右改为方向键 ↑ → ↓ ←",
          "Set up, right, down and left to the arrow keys",
        )}
        onClick={() => onPreset("arrows")}
      >
        {t("方向键", "Arrow keys")}
      </button>
      <button
        type="button"
        className="analog-chip-btn"
        title={t(
          "将上下左右改为 W D S A",
          "Set up, right, down and left to W D S A",
        )}
        onClick={() => onPreset("wasd")}
      >
        WASD
      </button>
    </fieldset>
  );
}

/** The profile-wide raw half-axis threshold, shown by sticks and triggers. */
function AxisThresholdField({
  profile,
  language,
  onChange,
}: {
  profile: Profile;
  language: Language;
  onChange: (profile: Profile) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  return (
    <SettingField
      language={language}
      label={t("原始轴阈值", "Raw axis threshold")}
      hint={t(
        "两个摇杆和两个扳机的原始轴共用。0 表示使用左摇杆的触发幅度。",
        "Shared by the raw axes of both sticks and triggers. 0 uses the left stick's trigger point.",
      )}
      value={toPercent(profile.analog?.axisThreshold ?? axisThresholdDefault)}
      min={0}
      max={100}
      unit="%"
      onCommit={(value) =>
        onChange(setAxisThreshold(profile, fromPercent(value)))
      }
    />
  );
}

function PresetSummary({
  group,
  preset,
  changes,
  modeChanges,
  script,
  raw,
  language,
  actions,
  profiles,
}: {
  group: "ls" | "rs" | "dpad";
  preset: AnalogPreset;
  changes: ReturnType<typeof presetChanges>;
  modeChanges: boolean;
  script: boolean;
  raw: boolean;
  language: Language;
  actions?: { name: string; label?: string }[];
  profiles?: ProfileEntry[];
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const keys = presetKeys[preset];
  return (
    <div className="analog-dialog-text">
      <p>
        {t(
          "上、右、下、左四个方向改为发送：",
          "Up, right, down and left will send:",
        )}
      </p>
      <ul className="analog-change-list" aria-label={t("新的按键", "New keys")}>
        {cardinalDirections.map((direction, index) => {
          const old = changes.find((change) =>
            change.control.endsWith(`.${direction}`),
          );
          return (
            <li key={direction} data-direction={direction}>
              <span className="analog-cell-arrow" aria-hidden="true">
                {directionArrow[direction]}
              </span>
              <span className="analog-change-name">
                {directionName(direction, language)}
              </span>
              {old ? (
                <>
                  <span className="analog-change-old">
                    <BindingSummary
                      binding={old.binding}
                      language={language}
                      actions={actions}
                      profiles={profiles}
                    />
                  </span>
                  <span className="analog-change-to" aria-hidden="true">
                    →
                  </span>
                  <span className="visually-hidden">
                    {t("改为", "becomes")}
                  </span>
                </>
              ) : null}
              <Keycaps keys={keys[index] ?? ""} language={language} />
            </li>
          );
        })}
      </ul>
      {changes.length > 0 && (
        <p>
          {t(
            `将替换已有的 ${changes.length} 个方向设置，显示名称保留。`,
            `${changes.length} existing direction ${changes.length === 1 ? "setting is" : "settings are"} replaced; display names are kept.`,
          )}
        </p>
      )}
      {modeChanges && (
        <p>
          {group === "dpad"
            ? t(
                "十字键模式将改为“独立按钮”，斜向的设置保留，但不再发送。",
                "The d-pad mode becomes Buttons; diagonal settings are kept but not sent.",
              )
            : t(
                "方向模式将改为“4 方向”，斜向的设置保留，但不再发送。",
                "The direction mode becomes 4-way; diagonal settings are kept but not sent.",
              )}
        </p>
      )}
      {raw && (
        <p>
          {t(
            "原始轴的设置仍将与这些方向同时发送。",
            "Raw axis settings still fire together with these directions.",
          )}
        </p>
      )}
      {script && (
        <p>
          {t(
            "此配置包含脚本，脚本也可能处理这些方向。",
            "This profile has a script, which may also handle these directions.",
          )}
        </p>
      )}
    </div>
  );
}

/** Live stick view: travel ring, dead zone, sectors and the current position. */
function StickVisual({
  x,
  y,
  settings,
  reading,
  label,
}: {
  x: number;
  y: number;
  settings: Required<ProfileStick>;
  reading: StickReading;
  label: string;
}) {
  const center = 68;
  const radius = 62;
  const point = (angle: number, r = radius) => {
    const rad = (angle * Math.PI) / 180;
    return [center + r * Math.sin(rad), center - r * Math.cos(rad)] as const;
  };
  const length = Math.hypot(x, y);
  const scale = length > 1 ? 1 / length : 1;
  const px = center + x * scale * radius;
  const py = center + y * scale * radius;
  const clamp = (value: number) => Math.max(0, Math.min(1, value));
  const half = settings.diagonalWidth / 2;
  // A knob (ANA-11) and a pointer stick (MOU-01) have no direction sectors to mark.
  const boundaries =
    settings.mode === "fourWayDiagonal" ||
    settings.mode === "knob" ||
    settings.mode === "mouse"
      ? []
      : settings.mode === "fourWay"
        ? [45, 135, 225, 315]
        : [45, 135, 225, 315].flatMap((angle) => [angle - half, angle + half]);
  // The outer ring starts outerThreshold of the way from the dead zone to
  // full push (ANA-10), where the daemon reads the push depth from.
  const outerRadius =
    clamp(
      settings.deadzone +
        clamp(settings.outerThreshold) *
          Math.max(0, settings.saturation - settings.deadzone),
    ) * radius;
  let arc = "";
  if (reading.sector) {
    const { from, to } = reading.sector;
    const [sx, sy] = point(from);
    const [ex, ey] = point(to);
    arc = `M${sx.toFixed(2)} ${sy.toFixed(2)} A${radius} ${radius} 0 ${to - from > 180 ? 1 : 0} 1 ${ex.toFixed(2)} ${ey.toFixed(2)}`;
  }
  return (
    <svg
      className="analog-visual"
      width="128"
      height="128"
      viewBox="0 0 136 136"
      role="img"
      aria-label={label}
      data-active={reading.active}
      data-ring={reading.ring ?? undefined}
    >
      <circle cx={center} cy={center} r={radius} className="analog-v-ring" />
      <path d="M68 10v116M10 68h116" className="analog-v-cross" />
      {boundaries.map((angle) => {
        const [ax, ay] = point(angle, radius - 8);
        const [bx, by] = point(angle, radius);
        return (
          <line
            key={angle}
            x1={ax}
            y1={ay}
            x2={bx}
            y2={by}
            className="analog-v-tick"
          />
        );
      })}
      {arc && <path d={arc} className="analog-v-sector" />}
      <circle
        cx={center}
        cy={center}
        r={clamp(settings.saturation) * radius}
        className="analog-v-saturation"
      />
      <circle
        cx={center}
        cy={center}
        r={outerRadius}
        className="analog-v-outer"
      />
      <circle
        cx={center}
        cy={center}
        r={Math.max(1.5, clamp(settings.deadzone) * radius)}
        className="analog-v-deadzone"
      />
      {length > 0.02 && (
        <path
          d={`M${center} ${center} L${px} ${py}`}
          className="analog-v-line"
        />
      )}
      {reading.active && (
        <circle cx={px} cy={py} r="10" className="analog-v-halo" />
      )}
      <circle cx={px} cy={py} r="5.5" className="analog-v-dot" />
    </svg>
  );
}

/** Threshold settings for a trigger, embedded under the drawer's 扳机设置. */
export function TriggerSettings(props: AnalogPanelProps) {
  const group = analogGroup(props.control);
  if (group !== "lt" && group !== "rt") return null;
  return <TriggerBody key={group} {...props} group={group} />;
}

function TriggerBody({
  profile,
  control,
  onChange,
  input,
  language,
  timing,
  registrations,
  profiles,
  actions,
  disabled = false,
  group,
}: AnalogPanelProps & { group: "lt" | "rt" }) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const side = group === "lt" ? "left" : "right";
  const name =
    group === "lt"
      ? t("左扳机", "the left trigger")
      : t("右扳机", "the right trigger");
  const halfControl = `${side}trigger+`;
  const full = fullControl(group);
  const stored = profile.analog?.[group];
  const trigger = { ...triggerDefaults, ...stored };
  const cardId = useId();
  const marksId = useId();
  const host = useRef<HTMLElement>(null);
  const params = useParamsMenu(host);
  const target = canonicalControl(control);
  const targetStage = target === halfControl || target === full ? target : null;
  const [expanded, setExpanded] = useState<string | null>(targetStage);
  useEffect(() => {
    if (targetStage) setExpanded(targetStage);
  }, [targetStage]);
  const [moving, setMoving] = useState(false);
  const legacy = legacyTwoStage(profile, group, registrations);
  const value = Math.max(0, axisPosition(input?.axes[`${side}trigger`]));
  // The engine keeps a trigger pressed until it falls to the release point,
  // and each stage has its own pair of points. The first stage stays down
  // while the second is down.
  const previous = useRef({ first: false, second: false });
  const fullPressed = previous.current.second
    ? value > trigger.fullRelease
    : value >= trigger.fullPress;
  const pressed =
    fullPressed ||
    (previous.current.first ? value > trigger.release : value >= trigger.press);
  useEffect(() => {
    previous.current = { first: pressed, second: fullPressed };
  });
  const raw = rawThreshold(profile);
  const percent = Math.round(value * 100);
  const update = (patch: ProfileTrigger) =>
    onChange(setAnalogSettings(profile, group, { ...stored, ...patch }));
  const allIssues = analogIssues(profile.analog);
  const groupIssues = allIssues.filter((issue) =>
    issue.path.startsWith(`analog.${group}.`),
  );
  const isFullIssue = (issue: AnalogIssue) =>
    issue.path.endsWith(".fullPress") || issue.path.endsWith(".fullRelease");
  const issues = groupIssues.filter((issue) => !isFullIssue(issue));
  const fullIssues = groupIssues.filter(isFullIssue);
  // The shared threshold is reported next to its field in the half press.
  const thresholdIssues = allIssues.filter(
    (issue) => issue.path === "analog.axisThreshold",
  );
  const stage = (key: string, title: string, active: boolean) => (
    <>
      <BindingRow
        title={title}
        hint={
          key === halfControl
            ? t(`超过 ${toPercent(raw)}%`, `Past ${toPercent(raw)}%`)
            : undefined
        }
        control={key}
        binding={baseBinding(profile, key)}
        expanded={expanded === key}
        cardId={cardId}
        active={active}
        registrations={registrations}
        language={language}
        actions={actions}
        profiles={profiles}
        onToggle={() => setExpanded(expanded === key ? null : key)}
      />
      {expanded === key && (
        <BindingCard
          id={cardId}
          compact
          title={title}
          control={key}
          binding={baseBinding(profile, key)}
          onChange={(binding) =>
            onChange(setBaseBinding(profile, key, binding))
          }
          onClose={() => setExpanded(null)}
          registrations={registrations}
          timing={timing}
          profiles={profiles}
          actions={actions}
          language={language}
          disabled={disabled}
        />
      )}
    </>
  );
  return (
    <section
      className="analog-panel"
      ref={host}
      aria-label={t("扳机设置", "Trigger settings")}
      data-group={group}
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: Right click is a shortcut; the 参数操作 button opens the same menu. */}
      <div className="analog-section" onContextMenu={params.onContextMenu}>
        <div className="analog-section-head">
          <span className="field-label">{t("实时力度", "Live pressure")}</span>
          <span className="analog-mono" data-active={pressed}>
            {pressed ? `${t("按下中", "Pressed")} · ` : ""}
            {percent}%
          </span>
        </div>
        {/* The marks are presentational inside a meter: this sentence is what
            a screen reader gets of them, and it does not change with the reading. */}
        <span id={marksId} className="visually-hidden">
          {t(
            `按下 ${toPercent(Math.min(1, trigger.press))}%，松开 ${toPercent(Math.min(1, trigger.release))}%，按到底 ${toPercent(Math.min(1, trigger.fullPress))}%`,
            `Press ${toPercent(Math.min(1, trigger.press))}%, release ${toPercent(Math.min(1, trigger.release))}%, full pull ${toPercent(Math.min(1, trigger.fullPress))}%`,
          )}
        </span>
        {/* biome-ignore lint/a11y/useSemanticElements: A native meter cannot show the press and release marks. */}
        <div
          className="analog-bar"
          role="meter"
          aria-label={t("扳机实时力度", "Live trigger pressure")}
          aria-describedby={marksId}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
          data-active={pressed}
        >
          <span className="analog-bar-fill" style={{ width: `${percent}%` }} />
          <span
            className="analog-bar-mark"
            data-kind="release"
            title={t("松开", "Release")}
            style={{ left: `${toPercent(Math.min(1, trigger.release))}%` }}
          />
          <span
            className="analog-bar-mark"
            data-kind="press"
            title={t("按下", "Press")}
            style={{ left: `${toPercent(Math.min(1, trigger.press))}%` }}
          />
          <span
            className="analog-bar-mark"
            data-kind="full"
            title={t("按到底", "Full pull")}
            style={{ left: `${toPercent(Math.min(1, trigger.fullPress))}%` }}
          />
        </div>
        <p className="analog-caption">
          {t(
            "超过“按下”时按下，低于“松开”时才松开。",
            "Presses past Press, and lets go only below Release.",
          )}
        </p>
      </div>
      <fieldset className="analog-fieldset analog-section" disabled={disabled}>
        <SettingField
          language={language}
          label={t("按下", "Press")}
          value={toPercent(trigger.press)}
          min={0}
          max={100}
          unit="%"
          onCommit={(value) => update({ press: fromPercent(value) })}
        />
        <SettingField
          language={language}
          label={t("松开", "Release")}
          hint={t("不能大于“按下”。", "Must not be greater than Press.")}
          value={toPercent(trigger.release)}
          min={0}
          max={100}
          unit="%"
          onCommit={(value) => update({ release: fromPercent(value) })}
        />
        <Issues issues={issues} language={language} />
        {usesDepthRate(profile, group) && (
          <SettingRow label={t("响应曲线", "Response curve")}>
            <Segmented<NonNullable<ProfileTrigger["curve"]>>
              small
              className="analog-mode"
              label={t("响应曲线", "Response curve")}
              value={trigger.curve}
              onChange={(curve) => update({ curve })}
              options={curveOptions(language)}
            />
          </SettingRow>
        )}
      </fieldset>
      <div className="analog-section">
        <fieldset
          className="analog-fieldset analog-section-head"
          disabled={disabled}
        >
          <span className="field-label">
            {t("两段扳机", "Two-stage trigger")}
          </span>
          {legacy && (
            <button
              type="button"
              className="analog-chip-btn"
              onClick={() => setMoving(true)}
            >
              {t("改用按到底", "Use full pull")}
            </button>
          )}
        </fieldset>
        {stage(full, t("按到底", "Full pull"), fullPressed)}
        <fieldset className="analog-fieldset analog-fields" disabled={disabled}>
          <SettingField
            language={language}
            label={t("按到底：按下", "Full pull: press")}
            value={toPercent(trigger.fullPress)}
            min={0}
            max={100}
            unit="%"
            onCommit={(value) => update({ fullPress: fromPercent(value) })}
          />
          <SettingField
            language={language}
            label={t("按到底：松开", "Full pull: release")}
            value={toPercent(trigger.fullRelease)}
            min={0}
            max={100}
            unit="%"
            onCommit={(value) => update({ fullRelease: fromPercent(value) })}
          />
        </fieldset>
        <Issues issues={fullIssues} language={language} />
      </div>
      <div className="analog-section">
        <fieldset className="analog-fieldset" disabled={disabled}>
          <AxisThresholdField
            profile={profile}
            language={language}
            onChange={onChange}
          />
        </fieldset>
        <Issues issues={thresholdIssues} language={language} />
        {stage(halfControl, t("半按", "Half press"), value >= raw)}
      </div>
      <div className="analog-params-row">
        <span className="analog-caption">
          {t(
            "复制、粘贴或恢复此扳机的参数",
            "Copy, paste or reset this trigger's parameters",
          )}
        </span>
        <ParamsMenu
          kind="trigger"
          name={name}
          settings={stored}
          language={language}
          testId="analog-params"
          disabled={disabled}
          openRequest={params.request}
          onReplace={(settings) =>
            onChange(
              setAnalogSettings(profile, group, settings as ProfileTrigger),
            )
          }
        />
      </div>
      <ConfirmDialog
        open={moving && !disabled}
        title={t("改用按到底？", "Use full pull?")}
        confirmLabel={t("改用", "Use")}
        language={language}
        onClose={() => setMoving(false)}
        // A script may have been registered while the dialog was open.
        onConfirm={() => legacy && onChange(convertToFullPull(profile, group))}
      >
        <p className="analog-dialog-text">
          {t(
            `${name}原来的按键改为“按到底”，“半按”的按键改为${name}的按键。按下和松开的位置恢复默认。`,
            `The keys of ${name} become Full pull, and the Half press keys become the keys of ${name}. Press and release return to their defaults.`,
          )}
        </p>
      </ConfirmDialog>
    </section>
  );
}

/** D-pad mode settings, embedded under the drawer's 十字键设置. */
export function DpadSettings(props: AnalogPanelProps) {
  if (analogGroup(props.control) !== "dpad") return null;
  return <DpadBody {...props} />;
}

function DpadBody({
  profile,
  control,
  onChange,
  input,
  language,
  timing,
  registrations,
  profiles,
  actions,
  disabled = false,
}: AnalogPanelProps) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const stored = profile.analog?.dpad;
  const dpad = { ...dpadDefaults, ...stored };
  const diagonals = diagonalGrid.map((direction) => `dpad.${direction}`);
  const cardId = useId();
  const host = useRef<HTMLElement>(null);
  const params = useParamsMenu(host);
  const [expanded, setExpanded] = useState<string | null>(() =>
    focusTarget(control, "dpad", diagonals),
  );
  // biome-ignore lint/correctness/useExhaustiveDependencies: Follow only a new drawer target.
  useEffect(() => {
    const target = focusTarget(control, "dpad", diagonals);
    if (target) setExpanded(target);
  }, [control]);
  const [preset, setPreset] = useState<AnalogPreset | null>(null);
  const update = (patch: ProfileDpad) =>
    onChange(setAnalogSettings(profile, "dpad", { ...stored, ...patch }));
  const changes = presetChanges(profile, "dpad");
  const askPreset = (next: AnalogPreset) => {
    if (disabled) return;
    if (!changes.length && dpad.mode === presetMode("dpad"))
      onChange(applyAnalogPreset(profile, "dpad", next));
    else setPreset(next);
  };
  const captions: Record<DpadMode, string> = {
    buttons: t(
      "上下左右为 4 个独立按键，同时按下两个时分别发送。",
      "Four independent buttons; pressing two sends both.",
    ),
    fourWay: t(
      "同时按下两个相邻方向时，只取其中一个方向。",
      "Two neighboring directions count as one of them.",
    ),
    eightWay: t(
      "同时按下两个相邻方向时视为斜向，斜向单独设置按键。",
      "Two neighboring directions make a diagonal with its own keys.",
    ),
  };
  // Only the fixed rule needs words; 保留先按下的 says what it does.
  const foldCaption =
    dpad.fold === "clockwise" &&
    t(
      "↑→ 取 →，↓→ 取 →，↓← 取 ←，↑← 取 ↑（与 AntiMicroX 相同）。",
      "↑→ gives →, ↓→ gives →, ↓← gives ←, ↑← gives ↑ (as in AntiMicroX).",
    );
  const expandedDirection = diagonalGrid.find(
    (direction) => `dpad.${direction}` === expanded,
  );
  return (
    <section
      className="analog-panel"
      ref={host}
      aria-label={t("十字键设置", "D-pad settings")}
      data-group="dpad"
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: Right click is a shortcut; the 参数操作 button opens the same menu. */}
      <div className="analog-section" onContextMenu={params.onContextMenu}>
        <fieldset className="analog-fieldset" disabled={disabled}>
          <Segmented<DpadMode>
            label={t("十字键模式", "D-pad mode")}
            className="analog-mode"
            value={dpad.mode}
            onChange={(mode) => {
              update({ mode });
              if (mode !== "eightWay") setExpanded(null);
            }}
            options={[
              { value: "buttons", label: t("独立按钮", "Buttons") },
              { value: "fourWay", label: t("4 方向", "4-way") },
              { value: "eightWay", label: t("8 方向", "8-way") },
            ]}
          />
        </fieldset>
        <p className="analog-caption">{captions[dpad.mode]}</p>
      </div>
      {dpad.mode === "fourWay" && (
        <fieldset
          className="analog-fieldset analog-section"
          disabled={disabled}
        >
          <span className="field-label">{t("斜按时", "On a diagonal")}</span>
          <Segmented<NonNullable<ProfileDpad["fold"]>>
            label={t("斜按时", "On a diagonal")}
            className="analog-mode"
            value={dpad.fold}
            onChange={(fold) => update({ fold })}
            options={[
              {
                value: "keepFirst",
                label: t("保留先按下的", "Keep the first"),
              },
              { value: "clockwise", label: t("按固定规则", "Fixed rule") },
            ]}
          />
          {foldCaption && <p className="analog-caption">{foldCaption}</p>}
        </fieldset>
      )}
      <div className="analog-section">
        <div className="analog-section-head">
          <span className="field-label">{t("上下左右", "Directions")}</span>
          <PresetButtons
            language={language}
            disabled={disabled}
            onPreset={askPreset}
          />
        </div>
        <p className="analog-caption">
          {t(
            "将四个方向一次改为方向键或 WASD。",
            "Set all four directions to the arrow keys or WASD at once.",
          )}
        </p>
      </div>
      {dpad.mode === "eightWay" && (
        <div className="analog-section">
          <span className="field-label">
            {t("斜向的按键", "Keys for the diagonals")}
          </span>
          <DirectionGrid
            group="dpad"
            directions={diagonalGrid}
            bindingFor={(key) => baseBinding(profile, key)}
            active={dpadDirections(input?.buttons, "eightWay")}
            expanded={expanded}
            cardId={cardId}
            registrations={registrations}
            language={language}
            actions={actions}
            profiles={profiles}
            label={t("斜向的按键", "Keys for the diagonals")}
            onToggle={(key) => setExpanded(expanded === key ? null : key)}
          />
          {expandedDirection && expanded && (
            <BindingCard
              id={cardId}
              title={`${directionArrow[expandedDirection]} ${t(
                `${directionName(expandedDirection, language)}斜按`,
                `${directionName(expandedDirection, language)} diagonal`,
              )}`}
              control={expanded}
              binding={baseBinding(profile, expanded)}
              onChange={(binding) =>
                onChange(setBaseBinding(profile, expanded, binding))
              }
              onClose={() => setExpanded(null)}
              registrations={registrations}
              timing={timing}
              profiles={profiles}
              actions={actions}
              language={language}
              disabled={disabled}
            />
          )}
        </div>
      )}
      <div className="analog-params-row">
        <span className="analog-caption">
          {t(
            "复制、粘贴或恢复十字键的参数",
            "Copy, paste or reset the d-pad's parameters",
          )}
        </span>
        <ParamsMenu
          kind="dpad"
          name={t("十字键", "the d-pad")}
          settings={stored}
          language={language}
          testId="analog-params"
          disabled={disabled}
          openRequest={params.request}
          onReplace={(settings) =>
            onChange(
              setAnalogSettings(profile, "dpad", settings as ProfileDpad),
            )
          }
        />
      </div>
      <ConfirmDialog
        open={preset !== null && !disabled}
        title={
          preset === "wasd"
            ? t("改为 WASD？", "Use WASD?")
            : t("改为方向键？", "Use the arrow keys?")
        }
        confirmLabel={t("替换", "Replace")}
        language={language}
        onClose={() => setPreset(null)}
        onConfirm={() => {
          if (preset) onChange(applyAnalogPreset(profile, "dpad", preset));
        }}
      >
        {preset && (
          <PresetSummary
            group="dpad"
            preset={preset}
            changes={changes}
            modeChanges={dpad.mode !== presetMode("dpad")}
            script={!!profile.script}
            raw={false}
            language={language}
            actions={actions}
            profiles={profiles}
          />
        )}
      </ConfirmDialog>
    </section>
  );
}
