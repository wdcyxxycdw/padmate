import type { ProfileDiagnostic, ScriptRegistration } from "../shared/api";
import type { Profile, ProfileBinding } from "../shared/generated/schema";
import { analogControlPart } from "./control-parts";
import {
  actionText,
  bindingAt,
  canonicalControl,
  diagnosticSet,
  type Gesture,
  gestureName,
  gestures,
  type KeyView,
  type LayerUsage,
  usedGestures,
  usesOnlyBase,
} from "./editor-model";
import type { Language } from "./store";
import { Notice } from "./ui/controls";
import { Icon } from "./ui/Icon";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export type ControlKind = "button" | "stick" | "trigger" | "dpad";

/** Notices about the parts of the profile file the editor does not show. */
export function ProfileNotices({
  usage,
  readOnly,
  language,
}: {
  usage: LayerUsage | null;
  readOnly: "" | "defaultLayer";
  language: Language;
}) {
  if (readOnly === "defaultLayer")
    return (
      <Notice
        kind="warning"
        icon="lock"
        role="status"
        testId="drawer-read-only"
        title={tx(
          language,
          "此配置在界面中只能查看",
          "This profile is read-only here",
        )}
      >
        {tx(
          language,
          "配置文件设定启动时使用另一组按键，此处修改的按键组在启动时不会生效。如需修改，请直接编辑配置文件。",
          "The profile file starts with another key group, so changes to this group would not apply at startup. Edit the profile file to change it.",
        )}
      </Notice>
    );
  if (!usage) return null;
  const switches = usage.profileActions;
  const switchCount = tx(
    language,
    `${switches} 个切换配置的动作`,
    switches === 1
      ? "1 action that switches profiles"
      : `${switches} actions that switch profiles`,
  );
  if (usesOnlyBase(usage)) {
    if (!switches) return null;
    // Only profile switches: they show on their keys, locked.
    return (
      <Notice
        kind="info"
        testId="drawer-file-notice"
        title={tx(
          language,
          `此配置文件中有 ${switchCount}`,
          `This profile file has ${switchCount}`,
        )}
      >
        {tx(
          language,
          "此类动作照常生效，但只能在配置文件中修改，界面将原样保留。",
          "Such actions still work, but can only be changed in the file; the editor keeps them as is.",
        )}
      </Notice>
    );
  }
  const groups = usage.extraLayers.length;
  return (
    <Notice
      kind="info"
      testId="drawer-file-notice"
      title={
        groups
          ? tx(
              language,
              `此配置文件中还有 ${groups} 组按键`,
              groups === 1
                ? "This profile file has 1 more key group"
                : `This profile file has ${groups} more key groups`,
            )
          : tx(
              language,
              "此配置文件中还有界面未显示的设置",
              "This profile file has settings the editor does not show",
            )
      }
    >
      <span>
        {tx(
          language,
          "界面只编辑此组按键。其他设置照常生效，只能在配置文件中修改，界面将原样保留。",
          "The editor only edits this group. The rest still works, can only be changed in the file, and the editor keeps it as is.",
        )}
        {usage.switching &&
          tx(
            language,
            "此处的修改仅在此组按键使用中时生效。",
            " Changes here only apply while this group is in use.",
          )}
        {switches > 0 &&
          tx(
            language,
            `另有 ${switchCount}，同样只能在配置文件中修改。`,
            ` It also has ${switchCount}, which can likewise only be changed in the file.`,
          )}
      </span>
    </Notice>
  );
}

/** Explains that a shared (common) binding applies until this key has its own. */
export function CommonNotice({
  profile,
  bindingKey,
  binding,
  language,
  actions,
}: {
  profile: Profile;
  bindingKey: string;
  binding: ProfileBinding;
  language: Language;
  actions: { name: string; label?: string }[];
}) {
  if (!profile.common || usedGestures(binding).length) return null;
  const shared = bindingAt(profile, "common", bindingKey);
  const first = usedGestures(shared)[0];
  if (!first) return null;
  const summary = `${gestureName(first, language)} ${actionText(shared[first], language, actions)}`;
  return (
    <Notice kind="info" testId="drawer-common-notice">
      {tx(
        language,
        `此按键在配置文件中有一份各组共用的设置（${summary}），当前生效的是该设置。在此处设置后，将改用此处的设置。`,
        `This key has a shared setting in the profile file (${summary}) that applies now. A setting here takes its place.`,
      )}
    </Notice>
  );
}

const directionGlyphs: Record<string, string> = {
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  upleft: "↖",
  upright: "↗",
  downleft: "↙",
  downright: "↘",
};

function pointerParts(pointer: string) {
  return pointer
    .split("/")
    .slice(1)
    .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
}

/**
 * Binding keys in the edited (base) group that belong to a drawer. A stick's
 * own binding (its click) is the press drawer's, a button; the stick drawer
 * is its movement.
 */
function ownsKey(
  key: string,
  bindingKey: string,
  kind: ControlKind,
  control: string,
) {
  const canonical = canonicalControl(key);
  if (canonical === bindingKey) return kind !== "stick";
  if (kind === "stick") {
    const group = control === "leftstick" ? "ls" : "rs";
    const axis = control === "leftstick" ? "left" : "right";
    return (
      canonical.startsWith(`${group}.`) ||
      new RegExp(`^${axis}[xy][+-]$`).test(canonical)
    );
  }
  // The second stage (lt.full) is edited in the trigger's drawer, with the
  // half axes.
  if (kind === "trigger")
    return (
      canonical === `${bindingKey}.full` ||
      canonical === `${control}+` ||
      canonical === `${control}-`
    );
  return false;
}

const analogGroups: Record<ControlKind, (control: string) => string | null> = {
  button: () => null,
  stick: (control) => (control === "leftstick" ? "ls" : "rs"),
  trigger: (control) => (control === "lefttrigger" ? "lt" : "rt"),
  dpad: () => "dpad",
};

/**
 * Validation results that point at this control, in plain words. view is the
 * set being edited: the base layer, or the function set.
 */
export function controlDiagnostics(
  diagnostics: ProfileDiagnostic[],
  bindingKey: string,
  kind: ControlKind,
  control: string,
  language: Language,
  view: KeyView = "normal",
): { diagnostic: ProfileDiagnostic; where: string }[] {
  const result: { diagnostic: ProfileDiagnostic; where: string }[] = [];
  const group = analogGroups[kind](control);
  for (const diagnostic of diagnostics) {
    const bits = pointerParts(diagnostic.pointer);
    if (bits[0] === "analog" && group && bits[1] === group) {
      result.push({
        diagnostic,
        where:
          kind === "stick"
            ? tx(language, "摇杆设置", "Stick settings")
            : kind === "trigger"
              ? tx(language, "扳机设置", "Trigger settings")
              : tx(language, "十字键", "D-pad"),
      });
      continue;
    }
    // The key name of a stick is its press's.
    if (
      bits[0] === "controlNames" &&
      bits[1] &&
      kind !== "stick" &&
      canonicalControl(bits[1]) === bindingKey
    ) {
      result.push({
        diagnostic,
        where: tx(language, "按键名称", "Key name"),
      });
      continue;
    }
    const found = diagnosticSet(diagnostic.pointer);
    if (found?.view !== view || !found.rest[0]) continue;
    if (!ownsKey(found.rest[0], bindingKey, kind, control)) continue;
    const part = found.rest[1];
    const field = gestures.includes(part as Gesture)
      ? gestureName(part as Gesture, language)
      : part === "label"
        ? tx(language, "显示文字", "Label")
        : part === "timing"
          ? tx(language, "判定时间", "Timing")
          : part === "policy"
            ? tx(language, "打断判定", "Interruptions")
            : part === "latch"
              ? tx(language, "锁存", "Latch")
              : part === "haptic"
                ? tx(language, "触感", "Haptic")
                : tx(language, "此按键", "This key");
    const canonical = canonicalControl(found.rest[0]);
    const direction =
      canonical !== bindingKey
        ? (analogControlPart(canonical, language) ??
          directionGlyphs[canonical.split(".")[1] ?? ""] ??
          canonical)
        : "";
    result.push({
      diagnostic,
      where: direction ? `${direction} ${field}` : field,
    });
  }
  return result;
}

export function ControlDiagnostics({
  items,
  language,
}: {
  items: { diagnostic: ProfileDiagnostic; where: string }[];
  language: Language;
}) {
  if (!items.length) return null;
  return (
    <ul
      className="drawer-diagnostics"
      aria-label={tx(language, "此按键的问题", "Problems with this key")}
    >
      {items.map(({ diagnostic, where }) => (
        <li
          key={`${diagnostic.pointer}:${diagnostic.message}:${diagnostic.offset}`}
          className="drawer-diagnostic"
          data-severity={diagnostic.severity}
          data-testid="control-diagnostic"
        >
          <Icon
            name={diagnostic.severity === "error" ? "error" : "warn"}
            size={14}
            stroke={2}
          />
          <span>
            {diagnostic.severity === "error"
              ? tx(language, "错误 · ", "Error · ")
              : tx(language, "警告 · ", "Warning · ")}
            <strong>{where}</strong>
            {tx(language, "：", ": ")}
            {diagnostic.message}
          </span>
        </li>
      ))}
    </ul>
  );
}

function role(registration: ScriptRegistration, language: Language) {
  if (registration.observe)
    return { text: tx(language, "只读取", "Reads only"), kind: "observe" };
  if (registration.claim)
    return { text: tx(language, "接管", "Claims"), kind: "claim" };
  return { text: tx(language, "可消费", "Can consume"), kind: "consume" };
}

/** What a script does with this key (ADR-0045 decision 5). */
export function ScriptOwnership({
  visible,
  owners,
  entry,
  hasBindings,
  language,
  onShowScripts,
}: {
  visible: ScriptRegistration[];
  owners: ScriptRegistration[];
  entry?: string;
  hasBindings: boolean;
  language: Language;
  /** Shows a "前往脚本页" button that opens the scripts page. */
  onShowScripts?: () => void;
}) {
  if (!visible.length) return null;
  const owned = owners.length > 0;
  const title = owned
    ? entry
      ? tx(language, `由脚本 ${entry} 接管`, `Handled by script ${entry}`)
      : tx(language, "由脚本接管", "Handled by the script")
    : entry
      ? tx(
          language,
          `脚本 ${entry} 会读取此按键`,
          `Script ${entry} reads this key`,
        )
      : tx(language, "脚本会读取此按键", "The script reads this key");
  const description = owned
    ? hasBindings
      ? ""
      : tx(
          language,
          "此按键的输入先由脚本处理。",
          "Input from this key goes to the script first.",
        )
    : tx(
        language,
        "脚本只读取，不影响此处的设置。",
        "The script only reads it; the settings here still apply.",
      );
  return (
    <section
      className="drawer-script"
      data-owned={owned}
      aria-label={tx(language, "脚本", "Script")}
    >
      <div className="drawer-script-head">
        {owned ? (
          <span
            className="drawer-script-badge"
            data-testid="script-ownership"
            title={tx(language, "由脚本接管", "Handled by the script")}
          >
            S
          </span>
        ) : (
          <span className="drawer-script-badge" aria-hidden="true">
            <Icon name="script" size={14} stroke={2} />
          </span>
        )}
        <span className="drawer-script-text">
          <span className="drawer-script-title">{title}</span>
          {description && (
            <span className="drawer-script-desc">{description}</span>
          )}
        </span>
      </div>
      {onShowScripts && (
        <div className="drawer-script-actions">
          <button
            type="button"
            className="btn-ghost drawer-script-link"
            onClick={onShowScripts}
          >
            {tx(language, "前往脚本页", "Go to Scripts")}
          </button>
        </div>
      )}
      <ul
        className="drawer-script-list"
        aria-label={tx(
          language,
          "脚本如何使用此按键",
          "How the script uses it",
        )}
      >
        {visible.map((registration) => {
          const kind = role(registration, language);
          return (
            <li key={registration.id} className="drawer-script-row">
              <span className="drawer-script-gesture">
                {gestures.includes(registration.gesture as Gesture)
                  ? gestureName(registration.gesture as Gesture, language)
                  : registration.gesture}
              </span>
              <span className="drawer-script-label">
                {registration.label || "—"}
              </span>
              <span
                className={`tag${kind.kind === "observe" ? "" : " violet"}`}
                data-role={kind.kind}
              >
                {kind.text}
              </span>
            </li>
          );
        })}
      </ul>
      {owned && hasBindings && (
        <p className="drawer-script-conflict">
          <Icon name="warn" size={14} stroke={2} />
          <span>
            {tx(
              language,
              "此按键同时有绑定和脚本处理，脚本可能先处理输入。",
              "This key has both its own settings and script handling. The script may handle the input first.",
            )}
          </span>
        </p>
      )}
    </section>
  );
}
