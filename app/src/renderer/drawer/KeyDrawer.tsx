import {
  type CSSProperties,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useStore } from "zustand";
import type { DeviceInfo } from "../../shared/api";
import type { Profile, ProfileBinding } from "../../shared/generated/schema";
import {
  type AnalogPanelProps,
  StickPanel,
  TriggerSettings,
} from "../analog/AnalogPanels";
import {
  clearStick,
  setStickLabel,
  stickHasSettings,
  stickLabel,
  withoutOrphanStickLabels,
} from "../analog/analog-editor";
import { calloutOf, isStickPress } from "../controller/control-callout";
import { relatedKeys, stickMapped } from "../controller/controller-chips";
import { controllerLabels, familyName } from "../controller/controller-model";
import { PadIcon, padIcon, stickMoveIcon } from "../controller/pad-icon";
import {
  actionText,
  baseBinding,
  bindingMaps,
  canonicalControl,
  type Gesture,
  gestureName,
  hasDepth,
  registrationsFor,
  resolveTiming,
  setBaseBinding,
  swapView,
  usedGestures,
  withoutMinRate,
  withoutOrphanLabels,
} from "../editor/editor-model";
import type { EditOptions, EditorStore } from "../editor/editor-store";
import { withInherited, writeBack } from "../editor/inheritance";
import { effectiveView, viewName, viewRegistrations } from "../editor/key-view";
import type { Language, PadInput } from "../state/store";
import { useCommitField } from "../ui/commit-field";
import {
  Dialog,
  Disclosure,
  MenuButton,
  type MenuEntry,
  Notice,
} from "../ui/controls";
import { Icon } from "../ui/Icon";
import { BindingFields } from "./BindingEditor";
import {
  CommonNotice,
  ControlDiagnostics,
  type ControlKind,
  controlDiagnostics,
  ProfileNotices,
  ScriptOwnership,
} from "./KeyDrawerNotices";
import "./drawer.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Kind of control the drawer edits; sticks and triggers embed analog panels.
 * "stick" is a stick's movement; its press (leftstick.press) is a button.
 */
export function controlKind(control: string): ControlKind {
  if (control === "leftstick" || control === "rightstick") return "stick";
  if (control === "lefttrigger" || control === "righttrigger") return "trigger";
  if (control.startsWith("dpad.")) return "dpad";
  return "button";
}

/** Profile binding key a callout control edits, e.g. lefttrigger -> lt. */
export function drawerBindingKey(control: string): string {
  if (control === "lefttrigger") return "lt";
  if (control === "righttrigger") return "rt";
  // The press of a stick edits the stick's own binding (L3/R3).
  return canonicalControl(calloutOf(control));
}

const faceColors: Record<string, Record<string, string>> = {
  xbox: {
    south: "#7bd389",
    east: "#ff8a80",
    west: "#6fb6ff",
    north: "#f5cf5b",
  },
  playstation: {
    south: "#8fb8ff",
    east: "#ff8a80",
    west: "#f09ad6",
    north: "#7bd389",
  },
};

// App-local clipboard for "copy this key's settings"; it survives closing
// the drawer and never touches the system clipboard.
let keyClipboard: ProfileBinding | null = null;

interface Confirmation {
  kind: "paste" | "clear";
  id: string;
  context: string;
  /** The draft's reads from disk then; a reload makes the request stale. */
  loads: number;
  control: string;
  label: string;
  payload?: ProfileBinding;
}

/** The title of the drawer: the key's printed name. */
function KeyTitle({ title }: { title: string }) {
  return (
    <div className="drawer-title-row">
      <h2 className="drawer-title" title={title} tabIndex={-1}>
        {title}
      </h2>
    </div>
  );
}

/**
 * The text a key shows on the controller, committed like other fields. A
 * name belongs to a mapping: without one the field is off.
 */
function LabelField({
  value,
  placeholder,
  mapped,
  onCommit,
  language,
}: {
  value: string;
  /** What the controller shows without a label; none for an unset key. */
  placeholder?: string;
  mapped: boolean;
  onCommit: (label: string) => void;
  language: Language;
}) {
  const field = useCommitField({ value, onCommit });
  return (
    <label className="field">
      <span className="field-label">{tx(language, "功能", "Function")}</span>
      <input
        className="input drawer-label-input"
        placeholder={placeholder}
        disabled={!mapped}
        {...field.props}
      />
    </label>
  );
}

/** Drawer that edits one control of the profile draft (ADR-0012). */
export function KeyDrawer({
  editor,
  control,
  info,
  input,
  calibrated,
  language,
  onClose,
  menuRequest,
  onShowScripts,
}: {
  editor: EditorStore;
  control: string;
  onControl: (control: string) => void;
  info: DeviceInfo;
  input?: PadInput;
  /** The sticks the live controller has its own calibration for. */
  calibrated?: { ls: boolean; rs: boolean };
  language: Language;
  onClose: () => void;
  /** Each new value > 0 opens the key's actions menu (a chip right-click). */
  menuRequest?: number;
  /** Opens the scripts page from the script ownership notice. */
  onShowScripts?: () => void;
}) {
  const state = useStore(editor);
  const t = (zh: string, en: string) => tx(language, zh, en);
  // The usual bindings, or the function set (ADR-0012): the set
  // stands where the base layer does, so everything below reads and writes
  // it as it would the base layer, and edit puts the draft back.
  const functionKey = state.functionKey;
  const view = effectiveView(state.view, info, functionKey);
  const usual = state.draft;
  // A hand-written file that extends another (ADR-0013): what the
  // profile does not bind is the parent's, shown here as plain bindings. The
  // edits of it go to the profile's own file (writeBack): the first one makes
  // the key the profile's own.
  const parent = state.parent;
  const withParent = useMemo(
    () => (usual ? withInherited(usual, parent) : null),
    [usual, parent],
  );
  const profile = useMemo(
    () => (withParent ? swapView(withParent, view) : null),
    [withParent, view],
  );
  // A name goes with the last mapping of the key or stick an edit changes.
  const edit = (next: Profile, options?: EditOptions) => {
    if (!usual || !profile) return;
    const settled = withoutOrphanStickLabels(
      profile,
      withoutOrphanLabels(profile, next),
    );
    state.edit(writeBack(usual, parent, swapView(settled, view)), options);
  };
  const registrations = useMemo(
    () => viewRegistrations(state.registrations, view),
    [state.registrations, view],
  );
  const kind = controlKind(control);
  const key = drawerBindingKey(control);
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
    language,
  );
  const glyph = labels[calloutOf(control)] ?? calloutOf(control);
  // A stick has two drawers: its movement ("stick") and its press (a button).
  const press = isStickPress(control);
  const stickName =
    calloutOf(control) === "leftstick"
      ? t("左摇杆", "Left stick")
      : t("右摇杆", "Right stick");
  const defaultTitle = press
    ? t(`${stickName} 点按`, `${stickName} · Press`)
    : kind === "stick"
      ? t(`${stickName} 旋转`, `${stickName} · Move`)
      : kind === "dpad"
        ? t(`十字键 ${glyph}`, `D-pad ${glyph}`)
        : t(`${glyph} 键`, `${glyph} key`);
  const title = defaultTitle;
  const locked = !profile || !!state.readOnly || state.busy;
  const binding: ProfileBinding = profile ? baseBinding(profile, key) : {};
  const used = usedGestures(binding);
  const timing = resolveTiming(state.globalTiming, profile?.timing);
  const actions = state.description?.metadata.actions ?? [];
  // The key's own registrations: only these delay its gestures. lt/rt, the
  // second stage (lt.full) and the raw half press (lefttrigger+) are separate
  // controls in the engine.
  // A stick's movement has none of its own: its directions, rings, ticks and
  // raw axes are the keys it reports (the binding is its press's).
  const keyScripts =
    kind === "stick"
      ? { visible: [], owners: [], otherScopes: [] }
      : registrationsFor(registrations, key);
  // What the drawer reports as script handling, the second stage and the
  // half press included.
  const stages =
    kind === "trigger"
      ? [
          registrationsFor(registrations, `${key}.full`),
          registrationsFor(registrations, `${control}+`),
        ]
      : kind === "stick"
        ? relatedKeys(control).map((related) =>
            registrationsFor(registrations, related),
          )
        : [];
  const scripts = stages.length
    ? {
        visible: [...keyScripts.visible, ...stages.flatMap((s) => s.visible)],
        owners: [...keyScripts.owners, ...stages.flatMap((s) => s.owners)],
        otherScopes: [
          ...keyScripts.otherScopes,
          ...stages.flatMap((s) => s.otherScopes),
        ],
      }
    : keyScripts;
  const [clipboard, setClipboard] = useState(() => keyClipboard);
  const [copiedFor, setCopiedFor] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<Confirmation | null>(null);
  // The key whose mappings were cleared from the menu has none, as after the
  // last one is deleted: it shows no empty 点按 (ADR-0013). Opening another
  // key and coming back starts it afresh. A stick's two cells edit the same
  // binding but are two selections, so this follows the selection.
  const [cleared, setCleared] = useState<string | null>(null);
  const keyId = `${view}:${control}`;
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when the drawer switches key or view
  useEffect(() => setCleared(null), [keyId]);

  // Opening the drawer moves focus to its heading, so keyboard users land in
  // the drawer and not on the chip list before it. Switching keys while it is
  // open keeps focus where it is (the next chip), unless the switch dropped it
  // to <body> (a notice's problem or the key list closed). Focus already in
  // the drawer, e.g. a menu opened by a right click, stays.
  const host = useRef<HTMLElement>(null);
  const opened = useRef(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when the drawer opens or switches control
  useEffect(() => {
    const root = host.current;
    const active = document.activeElement;
    const inside = !!root && !!active && root.contains(active);
    const lost = !active || active === document.body;
    if (!inside && (!opened.current || lost))
      root
        ?.querySelector<HTMLElement>(".drawer-title")
        ?.focus({ preventScroll: true });
    opened.current = true;
  }, [control]);

  const setBinding = (next: ProfileBinding | undefined) => {
    if (profile && !locked) edit(setBaseBinding(profile, key, next));
  };
  const request = (confirmation: "paste" | "clear") =>
    setConfirm({
      kind: confirmation,
      id: state.id,
      context: JSON.stringify(state.context),
      loads: state.loads,
      control: key,
      label: title,
      payload:
        confirmation === "paste" && clipboard
          ? structuredClone(clipboard)
          : undefined,
    });
  const confirmationValid =
    !!confirm &&
    !!profile &&
    !state.busy &&
    !state.readOnly &&
    state.id === confirm.id &&
    state.loads === confirm.loads &&
    JSON.stringify(state.context) === confirm.context &&
    confirm.control === key;
  const hasSettings = Object.keys(binding).length > 0;
  const profileText = t(
    `配置「${profile?.name ?? state.id}」`,
    `Profile "${profile?.name ?? state.id}"`,
  );
  const stickGroup = calloutOf(control) === "leftstick" ? "ls" : "rs";
  const glyphColor =
    kind === "button"
      ? faceColors[familyName(info.type)]?.[canonicalControl(control)]
      : undefined;
  // The header shows the control's icon (ADR-0013), or its printed name
  // where the icon sets have none. The press of a stick is its click.
  const art =
    kind === "stick"
      ? stickMoveIcon(calloutOf(control) === "leftstick" ? "l" : "r")
      : padIcon(
          press ? calloutOf(control) : canonicalControl(control),
          familyName(info.type),
          labels,
        );

  // The analog panels disable their own editing controls, so direction and
  // stage cards still open in a read-only profile.
  const panelProps: AnalogPanelProps | null = profile
    ? {
        profile,
        control,
        onChange: edit,
        input,
        calibrated,
        language,
        timing,
        registrations,
        profiles: state.profiles,
        actions,
        disabled: locked,
      }
    : null;
  const diagnostics = controlDiagnostics(
    state.diagnostics,
    key,
    kind,
    control,
    language,
    view,
  );
  // In the function set, a control the set does not mention does what it
  // does otherwise: say what that is (the chip shows it too).
  const otherwise = withParent ? baseBinding(withParent, key) : {};
  // Even a binding with only a label owns the key in the base layer.
  const usualBinds = Object.keys(otherwise).length > 0;
  const otherwiseGesture: Gesture | undefined = usedGestures(otherwise)[0];
  const otherwiseText =
    view === "normal" || kind === "stick" || Object.keys(binding).length > 0
      ? ""
      : otherwise.block
        ? t("已屏蔽", "Blocked")
        : otherwiseGesture
          ? [
              otherwiseGesture === "press" || otherwiseGesture === "tap"
                ? ""
                : gestureName(otherwiseGesture, language),
              actionText(
                otherwise[otherwiseGesture],
                language,
                actions,
                state.profiles,
              ),
            ]
              .filter(Boolean)
              .join(" ")
          : "";
  // A stick has no key menu.
  // A stick's movement has one action only: clear it, as a key does.
  const stickControl = stickGroup === "ls" ? "leftstick" : "rightstick";
  const stickClearable =
    !!profile && stickHasSettings(profile, stickControl, view === "normal");
  const keyMenu: MenuEntry[] =
    kind === "stick"
      ? [
          view === "normal"
            ? {
                label: t("清空此按键…", "Clear this key…"),
                icon: "trash",
                danger: true,
                disabled: locked || !stickClearable,
                onSelect: () => request("clear"),
              }
            : {
                label: t("清除这一套里的设置", "Clear in this set"),
                icon: "trash",
                danger: true,
                disabled: locked || !stickClearable,
                onSelect: () => {
                  if (profile) edit(clearStick(profile, stickControl, false));
                },
              },
        ]
      : [
          {
            label: t("复制此按键的设置", "Copy this key's settings"),
            icon: "copy",
            disabled: !profile || !hasSettings,
            onSelect: () => {
              keyClipboard = structuredClone(binding);
              setClipboard(keyClipboard);
              setCopiedFor(control);
            },
          },
          {
            label: t("粘贴…", "Paste…"),
            icon: "paste",
            disabled: !clipboard || locked,
            onSelect: () => request("paste"),
          },
          { kind: "separator" },
          view === "normal"
            ? {
                label: t("清空此按键…", "Clear this key…"),
                icon: "trash",
                danger: true,
                disabled: locked || !hasSettings,
                onSelect: () => request("clear"),
              }
            : {
                // The control goes from the set and does what it does
                // otherwise; one undo step, so no question first.
                label: t("清除这一套里的设置", "Clear in this set"),
                icon: "trash",
                danger: true,
                disabled: locked || !hasSettings,
                onSelect: () => {
                  setCleared(keyId);
                  setBinding(undefined);
                },
              },
        ];
  const otherScopes = scripts.otherScopes.length > 0 && (
    <p className="drawer-muted" data-testid="script-other-scopes">
      {t(
        `脚本还在配置文件的其他按键组中使用了此按键（${scripts.otherScopes.length} 处），未在此处显示。`,
        `The script also uses this key in other key groups of the file (${scripts.otherScopes.length}); they are not shown here.`,
      )}
    </p>
  );

  return (
    <aside
      ref={host}
      className="drawer"
      data-testid="mapping-editor"
      data-control={control}
      aria-label={t("按键编辑", "Key editor")}
    >
      <header className="drawer-header">
        {art ? (
          <span
            className="drawer-glyph"
            data-icon="true"
            style={glyphColor ? ({ color: glyphColor } as CSSProperties) : {}}
            aria-hidden="true"
          >
            <PadIcon name={art.name} />
          </span>
        ) : (
          <span
            className="drawer-glyph"
            data-shape={
              ["south", "east", "west", "north"].includes(
                canonicalControl(control),
              )
                ? "round"
                : "square"
            }
            data-size={
              glyph.length > 3
                ? "small"
                : glyph.length > 2
                  ? "medium"
                  : undefined
            }
            style={glyphColor ? ({ color: glyphColor } as CSSProperties) : {}}
            aria-hidden="true"
          >
            {glyph}
          </span>
        )}
        <div className="drawer-heading">
          <KeyTitle title={title} />
          <p className="drawer-subtitle">
            <span className="drawer-subtitle-text" title={profileText}>
              {profileText}
            </span>
            {view !== "normal" && (
              <span className="tag" data-testid="drawer-view">
                {viewName(view, language)}
              </span>
            )}
            {scripts.owners.length > 0 && (
              <span className="tag violet drawer-script-chip">
                {t("脚本", "Script")}
              </span>
            )}
          </p>
        </div>
        {keyMenu.length > 0 && (
          <MenuButton
            label={t("按键操作", "Key actions")}
            className="icon-btn bare"
            testId="key-actions"
            disabled={!profile}
            openRequest={menuRequest}
            items={keyMenu}
          />
        )}
        <button
          type="button"
          className="icon-btn bare"
          aria-label={t("关闭按键编辑", "Close key editor")}
          title={t("关闭按键编辑", "Close key editor")}
          onClick={onClose}
        >
          <Icon name="close" />
        </button>
      </header>
      <div className="drawer-body">
        {copiedFor === control && (
          <p className="drawer-status" role="status">
            <Icon name="check" size={14} stroke={2} />
            {t(
              "已复制。请打开其他按键，在“按键操作”中粘贴。",
              "Copied. Open another key and paste from its key actions.",
            )}
          </p>
        )}
        {!profile ? (
          state.busy ? (
            <p className="drawer-empty">{t("正在载入…", "Loading…")}</p>
          ) : state.error ? (
            <Notice
              kind="error"
              role="alert"
              testId="drawer-load-error"
              title={t("配置载入失败", "Could not load the profile")}
            >
              {state.error}
            </Notice>
          ) : (
            <p className="drawer-empty">
              {t(
                "当前映射未使用配置库中的配置，无法在此编辑。",
                "The current mapping is not a library profile, so it cannot be edited here.",
              )}
            </p>
          )
        ) : (
          <>
            <ProfileNotices
              usage={state.usage}
              readOnly={state.readOnly}
              language={language}
            />
            {/* Holding the function key, a key the set leaves out falls to the
                usual keys before common: if they bind it, common does not
                apply, and the note below says what does. */}
            {kind !== "stick" && !(view !== "normal" && usualBinds) && (
              <CommonNotice
                profile={profile}
                bindingKey={key}
                binding={binding}
                language={language}
                actions={actions}
              />
            )}
            <ControlDiagnostics items={diagnostics} language={language} />
            {otherwiseText && (
              <p className="drawer-muted" data-testid="drawer-inherited">
                {t("平时：", "Normal: ")}
                {otherwiseText}
              </p>
            )}
            {kind === "stick" && panelProps && profile ? (
              <>
                {/* The name of the movement on its label (ADR-0013): the
                    stick's, in analog, not a binding's. */}
                <fieldset className="drawer-fieldset" disabled={locked}>
                  <LabelField
                    key={`${view}:${key}`}
                    value={stickLabel(profile, stickGroup) ?? ""}
                    language={language}
                    mapped={stickMapped(
                      profile,
                      stickGroup === "ls" ? "leftstick" : "rightstick",
                    )}
                    onCommit={(label) =>
                      edit(setStickLabel(profile, stickGroup, label))
                    }
                  />
                </fieldset>
                {/* The direction cards edit the shown set: a new view, new cards. */}
                <StickPanel
                  key={view}
                  {...panelProps}
                  menuRequest={menuRequest}
                />
              </>
            ) : (
              <>
                <ScriptOwnership
                  visible={scripts.visible}
                  owners={scripts.owners}
                  entry={state.description?.entry}
                  hasBindings={used.length > 0}
                  language={language}
                  onShowScripts={onShowScripts}
                />
                <fieldset className="drawer-fieldset" disabled={locked}>
                  <LabelField
                    key={`${view}:${key}`}
                    value={binding.label ?? ""}
                    language={language}
                    mapped={bindingMaps(binding)}
                    placeholder={
                      used.length
                        ? actionText(
                            binding[used[0]],
                            language,
                            actions,
                            state.profiles,
                          )
                        : undefined
                    }
                    onCommit={(label) => {
                      const next = { ...binding };
                      if (label) next.label = label;
                      else delete next.label;
                      setBinding(next);
                    }}
                  />
                </fieldset>
                <BindingFields
                  key={`${view}:${key}`}
                  value={binding}
                  onChange={setBinding}
                  timing={timing}
                  profiles={state.profiles}
                  actions={actions}
                  language={language}
                  disabled={locked}
                  registrations={keyScripts.visible}
                  integration={{ profile: state.id, capture: state.capture }}
                  suggestPress={
                    keyScripts.owners.length === 0 &&
                    !state.readOnly &&
                    cleared !== keyId
                  }
                  advanced={otherScopes}
                >
                  {panelProps && kind === "trigger" && (
                    <div className="drawer-extra">
                      <Disclosure
                        title={t("扳机设置", "Trigger settings")}
                        summary={t("按下深度", "Press depth")}
                      >
                        <TriggerSettings {...panelProps} />
                      </Disclosure>
                    </div>
                  )}
                </BindingFields>
              </>
            )}
          </>
        )}
      </div>
      <Dialog
        open={!!confirm}
        title={
          confirm?.kind === "paste"
            ? t("粘贴设置？", "Paste settings?")
            : t("清空此按键？", "Clear this key?")
        }
        closeLabel={t("关闭", "Close")}
        onClose={() => setConfirm(null)}
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn"
              onClick={() => setConfirm(null)}
            >
              {t("取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              disabled={!confirmationValid}
              onClick={() => {
                if (!confirm || !confirmationValid || !profile) return;
                // A rate that follows the push depth belongs to sticks and
                // triggers: the daemon refuses it on any other key.
                const pasted =
                  confirm.kind === "paste" && confirm.payload
                    ? hasDepth(confirm.control)
                      ? confirm.payload
                      : withoutMinRate(confirm.payload)
                    : undefined;
                if (kind === "stick" && confirm.kind === "clear") {
                  edit(clearStick(profile, stickControl));
                  setConfirm(null);
                  return;
                }
                if (confirm.kind === "clear") setCleared(keyId);
                edit(setBaseBinding(profile, confirm.control, pasted));
                setConfirm(null);
              }}
            >
              {confirm?.kind === "paste"
                ? t("粘贴", "Paste")
                : t("清空", "Clear")}
            </button>
          </>
        }
      >
        <p className="drawer-dialog-text">
          {confirm?.kind === "paste"
            ? t(
                `用复制的设置替换「${confirm.label}」的当前设置。`,
                `Replace the settings of ${confirm.label} with the copied ones.`,
              )
            : kind === "stick"
              ? t(
                  `清空「${confirm?.label ?? ""}」的方向模式、所有按键、旋钮设置和名称，恢复成没设置过的样子。`,
                  `Remove the direction mode, every key, the knob settings and the name of ${confirm?.label ?? ""}, as if it had never been set up.`,
                )
              : t(
                  `清空「${confirm?.label ?? ""}」的所有映射和显示文字。按键名称将保留。`,
                  `Remove every mapping and the label of ${confirm?.label ?? ""}. The key name stays.`,
                )}
        </p>
        {confirm && !confirmationValid && (
          <Notice kind="warning" role="alert">
            {t(
              "正在编辑的配置或按键已更改，请取消后重新操作。",
              "The profile or key being edited has changed. Cancel and try again.",
            )}
          </Notice>
        )}
      </Dialog>
    </aside>
  );
}
