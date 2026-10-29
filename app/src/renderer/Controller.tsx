import {
  type CSSProperties,
  Fragment,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import type { DeviceInfo } from "../shared/api";
import { type Callout, controllerCallouts } from "./controller-callouts";
import {
  type ChipInfo,
  type ChipSegment,
  functionKeyChip,
} from "./controller-chips";
import {
  type ControllerFamily,
  controllerLabels,
  controlStrength,
  familyName,
  triggerValue,
} from "./controller-model";
import { createControllerScene } from "./controller-scene";
import { stageKey } from "./controller-stage";
import {
  defaultFunctionKey,
  type FunctionKey,
  isFunctionKey,
} from "./editor-model";
import type { Translate } from "./i18n";
import type { Language, PadInput } from "./store";
import { Icon } from "./ui/Icon";
import { Keycaps } from "./ui/Keycaps";
import "./controller-3d.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

interface ControllerProps {
  info: DeviceInfo;
  input?: PadInput;
  t: Translate;
  language?: Language;
  selectedControl?: string;
  onSelectControl?: (control: string) => void;
  /** A chip was right-clicked (UI-36): it is selected first, then this runs. */
  onMenuControl?: (control: string) => void;
  /** What each chip shows; computed from the profile draft by ControllerWorkspace. */
  chips?: Record<string, ChipInfo>;
  /** Show the bottom hint (only while no drawer or editor notice is open). */
  hint?: boolean;
  /** The function key (ADR-0075), whose chip is only a tag; Menu by default. */
  functionKey?: FunctionKey;
}

/** Printed face colors, matched by the label the controller reports. */
const faceColors: Partial<Record<ControllerFamily, Record<string, string>>> = {
  xbox: { A: "green", B: "red", X: "blue", Y: "yellow" },
  playstation: { "×": "ps-blue", "○": "red", "□": "pink", "△": "green" },
};

const glyphPaths: Record<string, ReactNode> = {
  view: (
    <>
      <rect x="3" y="7" width="12" height="12" rx="2" />
      <path d="M9 7V5h12v12h-2" />
    </>
  ),
  menu: <path d="M5 7h14M5 12h14M5 17h14" />,
  guide: <circle cx="12" cy="12" r="8" />,
  home: <path d="M4 11 12 4l8 7v9H4Z" />,
  capture: (
    <>
      <rect x="4" y="4" width="16" height="16" rx="3" />
      <circle cx="12" cy="12" r="4" />
    </>
  ),
  share: <path d="M12 15V4M8 8l4-4 4 4M5 13v6h14v-6" />,
  touchpad: <rect x="3" y="6" width="18" height="12" rx="3" />,
};

/** Icon for a system button whose printed name is too long for a badge. */
function systemGlyph(
  control: string,
  label: string,
  family: ControllerFamily,
): ReactNode {
  if (
    !["back", "start", "guide", "touchpad"].includes(control) ||
    Array.from(label).length <= 2
  )
    return undefined;
  const name =
    control === "touchpad"
      ? "touchpad"
      : control === "start"
        ? "menu"
        : control === "back"
          ? family === "playstation"
            ? "share"
            : "view"
          : label === "Capture"
            ? "capture"
            : label === "Home"
              ? "home"
              : "guide";
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {glyphPaths[name]}
    </svg>
  );
}

/** The physical control's badge: its printed label, face color and shape. */
function Glyph({
  control,
  label,
  family,
}: {
  control: string;
  label: string;
  family: ControllerFamily;
}) {
  const icon = systemGlyph(control, label, family);
  return (
    <span
      className="stage-glyph"
      data-shape={
        /shoulder|trigger/.test(control)
          ? "rect"
          : control.startsWith("dpad.")
            ? "arrow"
            : "round"
      }
      data-face={faceColors[family]?.[label]}
      aria-hidden="true"
    >
      {icon ?? Array.from(label).slice(0, 3).join("")}
    </span>
  );
}

function Segment({
  segment,
  language,
}: {
  segment: ChipSegment;
  language: Language;
}) {
  return (
    <>
      {segment.gesture && <span>{segment.gesture}</span>}
      {segment.script && (
        <span className="stage-script-tag">
          {tx(language, "脚本", "Script")}
        </span>
      )}
      {segment.macro && (
        <Icon
          name={segment.missing ? "warn" : "script"}
          size={11}
          stroke={2.2}
          className={`stage-macro-icon${segment.missing ? " stage-macro-missing" : ""}`}
        />
      )}
      {segment.keys && <Keycaps keys={segment.keys} language={language} />}
      {segment.chords?.map((chord, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: Two directions may send the same chord.
        <Fragment key={index}>
          {index > 0 && segment.divider && <span>{segment.divider}</span>}
          <Keycaps keys={chord} language={language} />
        </Fragment>
      ))}
      {segment.text && (
        <span
          className="stage-chip-detail-text"
          data-missing={segment.missing || undefined}
        >
          {segment.text}
        </span>
      )}
      {segment.hold && <span>{tx(language, "按住", "held")}</span>}
    </>
  );
}

function Chip({
  callout,
  chip,
  family,
  strength,
  selected,
  language,
  onSelect,
  onMenu,
}: {
  callout: Callout;
  chip: ChipInfo;
  family: ControllerFamily;
  strength: number;
  selected: boolean;
  language: Language;
  onSelect?: (control: string) => void;
  onMenu?: (control: string) => void;
}) {
  const trigger = callout.control.endsWith("trigger");
  const locked = chip.state === "locked";
  const name = `${callout.label} · ${chip.title}`;
  const notes = [
    chip.summary,
    // A script-only chip already says so on its second line.
    chip.scriptOwned && chip.state !== "script"
      ? tx(language, "由脚本接管", "Handled by a script")
      : "",
    locked
      ? tx(language, "只能在配置文件中修改", "Only editable in the file")
      : "",
  ].filter(Boolean);
  const position = {
    "data-callout": callout.control,
    "data-placement": callout.placement,
    "data-side": callout.side,
    "data-raised": callout.raised || undefined,
    "data-state": chip.state,
    "data-active": strength > 0,
    style: {
      "--slot": callout.slot ?? 0,
      "--offset": callout.offset ?? 0,
    } as CSSProperties,
  };
  // View and Menu only switch sets (ADR-0069): tagged, and nothing to open.
  if (chip.state === "function")
    return (
      <div
        className="stage-chip"
        role="img"
        aria-label={name}
        title={`${name}\n${chip.summary}`}
        {...position}
      >
        <Glyph
          control={callout.control}
          label={callout.label}
          family={family}
        />
        <span className="stage-chip-text">
          <span className="stage-chip-title">{chip.title}</span>
        </span>
      </div>
    );
  return (
    <button
      type="button"
      className="stage-chip"
      {...position}
      data-missing={chip.missing || undefined}
      data-inherited={chip.inherited || undefined}
      aria-label={name}
      aria-pressed={selected}
      title={notes.length ? `${name}\n${notes.join(" · ")}` : name}
      onClick={() => onSelect?.(callout.control)}
      onContextMenu={(event) => {
        // The drawer's key menu replaces the browser menu (also Shift+F10).
        event.preventDefault();
        onSelect?.(callout.control);
        onMenu?.(callout.control);
      }}
    >
      <Glyph control={callout.control} label={callout.label} family={family} />
      <span className="stage-chip-text">
        <span className="stage-chip-title">
          {chip.titleKeys ? (
            <Segment segment={chip.titleKeys} language={language} />
          ) : (
            chip.title
          )}
        </span>
        {chip.detail.length > 0 && (
          <span className="stage-chip-detail">
            {chip.detail.map((segment, index) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: Segments are positional parts of one line.
              <Fragment key={index}>
                {index > 0 && <span className="stage-sep">·</span>}
                <Segment segment={segment} language={language} />
              </Fragment>
            ))}
          </span>
        )}
      </span>
      {trigger && strength > 0 && (
        <span className="stage-chip-pct">{Math.round(strength * 100)}%</span>
      )}
      {locked && (
        <span className="stage-chip-lock">
          <Icon name="lock" size={12} stroke={2} />
        </span>
      )}
      {chip.scriptOwned && (
        <span className="stage-chip-script" data-testid="script-ownership">
          S
        </span>
      )}
    </button>
  );
}

export function Controller({
  info,
  input,
  t,
  language = "zh-CN",
  selectedControl,
  onSelectControl,
  onMenuControl,
  chips,
  hint = false,
  functionKey = defaultFunctionKey,
}: ControllerProps) {
  const stageHost = useRef<HTMLDivElement>(null);
  const callouts = useRef<SVGSVGElement>(null);
  const scene = useRef<ReturnType<typeof createControllerScene> | undefined>(
    undefined,
  );
  const [renderer, setRenderer] = useState("loading");
  const { type, buttonLabels, buttonMask } = info;
  const modelKey = stageKey({ type, buttonLabels, buttonMask });
  const stateId = useId();
  const hintId = useId();
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
  );
  const family = familyName(info.type);
  const layout = controllerCallouts(info);
  // Leaders of unset controls are dashed; the scene reads this on each frame.
  const unbound = layout
    .filter((callout) => chips?.[callout.control]?.state === "unbound")
    .map((callout) => callout.control)
    .join(" ");
  const latest = useRef({ input, selectedControl, unbound });
  latest.current = { input, selectedControl, unbound };
  const instructions = tx(
    language,
    "点击标签即可编辑对应按键 · 按下手柄按键时，对应标签会亮起",
    "Click a label to edit that control · Press a button on the controller to light up its label",
  );
  const unavailable = t("controllerUnavailable");
  useEffect(() => {
    setRenderer("loading");
    // jsdom and environments without WebGL must not call unimplemented getContext.
    if (!stageHost.current || typeof WebGL2RenderingContext !== "function") {
      setRenderer("unavailable");
      return;
    }
    try {
      const modelInfo: Pick<
        DeviceInfo,
        "type" | "buttonLabels" | "buttonMask"
      > = JSON.parse(modelKey);
      const current = latest.current;
      // The scene takes the session's canvas into the stage host (H2): the
      // first visit builds it, the next ones draw it at once. A replayed
      // effect (StrictMode) or another page only lets go of it.
      scene.current = createControllerScene(
        stageHost.current,
        modelInfo,
        () => setRenderer("webgl"),
        () => {
          scene.current?.dispose();
          scene.current = undefined;
          setRenderer("unavailable");
        },
        callouts.current ?? undefined,
        {
          input: current.input,
          selected: current.selectedControl,
          unbound: current.unbound ? current.unbound.split(" ") : [],
        },
      );
    } catch {
      setRenderer("unavailable");
    }
    return () => {
      scene.current?.dispose();
      scene.current = undefined;
    };
  }, [modelKey]);
  useEffect(() => {
    scene.current?.update(
      input,
      selectedControl,
      unbound ? unbound.split(" ") : [],
    );
  }, [input, selectedControl, unbound]);
  return (
    <fieldset
      className="controller-3d"
      aria-label={`${info.name} · ${t("controller")}`}
      aria-describedby={hintId}
      data-renderer={renderer}
      data-selected-control={selectedControl}
    >
      <div ref={stageHost} className="stage-host">
        <svg
          ref={callouts}
          className="controller-callouts"
          aria-hidden="true"
        />
        <div className="controller-labels">
          {layout.map((callout) => {
            // The same strength lights the control on the model (a pushed
            // stick counts once past the rest threshold).
            const strength = controlStrength(callout.control, input);
            return (
              <Chip
                key={callout.control}
                callout={callout}
                chip={
                  chips?.[callout.control] ??
                  (isFunctionKey(callout.control, functionKey)
                    ? functionKeyChip(language)
                    : {
                        title: callout.label,
                        detail: [],
                        summary: "",
                        state: "bound",
                        scriptOwned: false,
                      })
                }
                family={family}
                strength={strength}
                selected={selectedControl === callout.control}
                language={language}
                onSelect={onSelectControl}
                onMenu={onMenuControl}
              />
            );
          })}
        </div>
        {hint && <p className="controller-hint">{instructions}</p>}
      </div>
      {renderer === "unavailable" && (
        <p className="controller-unavailable">{unavailable}</p>
      )}
      {/* One sentence is the group's description; the state of every control
          is a list of its own, read when the user goes into it (UI-24). */}
      <p id={hintId} className="visually-hidden">
        {instructions}
      </p>
      <ul
        id={stateId}
        className="controller-accessible-state"
        aria-label={tx(language, "手柄按键状态", "Controller state")}
      >
        {Object.entries(labels).map(([control, label]) => (
          <li
            key={control}
            data-control={control}
            data-pressed={!!input?.buttons[control]}
          >
            {label}
            {input?.buttons[control] ? ` · ${t("pressed")}` : ""}
            {control.endsWith("trigger")
              ? ` ${Math.round(triggerValue(input?.axes[control]) * 100)}%`
              : ""}
          </li>
        ))}
      </ul>
    </fieldset>
  );
}
