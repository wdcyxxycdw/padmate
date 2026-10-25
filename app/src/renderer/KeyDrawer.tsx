import {
  type CSSProperties,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useStore } from "zustand";
import type { DeviceInfo } from "../shared/api";
import type { Profile, ProfileBinding } from "../shared/generated/schema";
import {
  type AnalogPanelProps,
  DpadSettings,
  StickPanel,
  TriggerSettings,
} from "./analog/AnalogPanels";
import { BindingFields } from "./BindingEditor";
import { relatedKeys } from "./controller-chips";
import { controllerLabels, familyName } from "./controller-model";
import {
  actionText,
  baseBinding,
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
} from "./editor-model";
import type { EditOptions, EditorStore } from "./editor-store";
import {
  keyInheritance,
  restoreInherited,
  withInherited,
  writeBack,
} from "./inheritance";
import {
  CommonNotice,
  ControlDiagnostics,
  type ControlKind,
  controlDiagnostics,
  ProfileNotices,
  ScriptOwnership,
} from "./KeyDrawerNotices";
import { effectiveView, viewName, viewRegistrations } from "./key-view";
import { parentName } from "./library-model";
import type { Language, PadInput } from "./store";
import { useCommitField } from "./ui/commit-field";
import {
  Dialog,
  Disclosure,
  MenuButton,
  type MenuEntry,
  Notice,
} from "./ui/controls";
import { Icon } from "./ui/Icon";
import "./drawer.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** Kind of control the drawer edits; sticks and triggers embed analog panels. */
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
  return canonicalControl(control);
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

function KeyTitle({
  title,
  name,
  placeholder,
  disabled,
  editing,
  onEditing,
  onRename,
  language,
}: {
  title: string;
  name: string;
  placeholder: string;
  disabled: boolean;
  editing: boolean;
  onEditing: (editing: boolean) => void;
  onRename: (name: string) => void;
  language: Language;
}) {
  const input = useRef<HTMLInputElement>(null);
  const pencil = useRef<HTMLButtonElement>(null);
  // Enter and Escape return focus to the pencil; a click elsewhere keeps it.
  const refocus = useRef(false);
  const done = () => {
    refocus.current = true;
    onEditing(false);
  };
  // The name is committed on Enter or when the field loses focus (UI-43).
  const field = useCommitField({
    value: name,
    onCommit: (next) => onRename(next.trim()),
    onDone: done,
  });
  useEffect(() => {
    if (editing) {
      input.current?.focus();
      input.current?.select();
    } else if (refocus.current) {
      refocus.current = false;
      pencil.current?.focus();
    }
  }, [editing]);
  if (editing)
    return (
      <input
        ref={input}
        className="input drawer-rename"
        aria-label={tx(language, "按键名称", "Key name")}
        maxLength={80}
        placeholder={placeholder}
        {...field.props}
        onKeyDown={(event) => {
          if (event.key !== "Escape") return field.props.onKeyDown(event);
          // Escape keeps the name as it was, typed or not.
          event.preventDefault();
          event.stopPropagation();
          field.reset();
          done();
        }}
        onBlur={() => {
          field.commit();
          onEditing(false);
        }}
      />
    );
  return (
    <div className="drawer-title-row">
      <h2 className="drawer-title" title={title} tabIndex={-1}>
        {title}
      </h2>
      <button
        ref={pencil}
        type="button"
        className="icon-btn bare drawer-pencil"
        aria-label={tx(language, "重命名此按键", "Rename this key")}
        title={tx(language, "重命名此按键", "Rename this key")}
        disabled={disabled}
        onClick={() => onEditing(true)}
      >
        <Icon name="edit" size={13} stroke={2} />
      </button>
    </div>
  );
}

/** The text a key shows on the controller, committed like other fields. */
function LabelField({
  value,
  placeholder,
  onCommit,
  language,
}: {
  value: string;
  /** What the controller shows without a label; none for an unset key. */
  placeholder?: string;
  onCommit: (label: string) => void;
  language: Language;
}) {
  const field = useCommitField({ value, onCommit });
  return (
    <label className="field">
      <span className="field-label">
        {tx(language, "在手柄图上显示为", "Shown on the controller as")}
      </span>
      <input
        className="input drawer-label-input"
        placeholder={placeholder}
        {...field.props}
      />
    </label>
  );
}

/** Drawer that edits one control of the profile draft (UI-39, ADR-0045). */
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
  /** The sticks the live controller has its own calibration for (IN-14). */
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
  // The usual bindings, or the set of a function key (UI-30, ADR-0069): the
  // set stands where the base layer does, so everything below reads and
  // writes it as it would the base layer, and edit puts the draft back.
  const view = effectiveView(state.view, info);
  const usual = state.draft;
  // The profile this one is based on (PRO-19): what the profile does not bind
  // is the parent's, shown here as it is. The edits of it go to the profile's
  // own file (writeBack): the first one makes the key the profile's own.
  const parent = state.parent;
  const withParent = useMemo(
    () => (usual ? withInherited(usual, parent) : null),
    [usual, parent],
  );
  const profile = useMemo(
    () => (withParent ? swapView(withParent, view) : null),
    [withParent, view],
  );
  const edit = (next: Profile, options?: EditOptions) => {
    if (usual)
      state.edit(writeBack(usual, parent, swapView(next, view)), options);
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
  );
  const glyph = labels[control] ?? control;
  const defaultTitle =
    kind === "dpad"
      ? t(`十字键 ${glyph}`, `D-pad ${glyph}`)
      : kind === "stick"
        ? control === "leftstick"
          ? t("左摇杆", "Left stick")
          : t("右摇杆", "Right stick")
        : t(`${glyph} 键`, `${glyph} key`);
  const name = profile?.controlNames?.[key] ?? "";
  const title = name || defaultTitle;
  const locked = !profile || !!state.readOnly || state.busy;
  const binding: ProfileBinding = profile ? baseBinding(profile, key) : {};
  const used = usedGestures(binding);
  const timing = resolveTiming(state.globalTiming, profile?.timing);
  const actions = state.description?.metadata.actions ?? [];
  // The key's own registrations: only these delay its gestures. lt/rt, the
  // second stage (lt.full) and the raw half press (lefttrigger+) are separate
  // controls in the engine.
  const keyScripts = registrationsFor(registrations, key);
  // What the drawer reports as script handling, the second stage and the
  // half press included.
  const stages =
    kind === "trigger"
      ? [
          registrationsFor(registrations, `${key}.full`),
          registrationsFor(registrations, `${control}+`),
        ]
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
  const [renamingFor, setRenamingFor] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<Confirmation | null>(null);

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
  const rename = (value: string) => {
    if (!profile || locked) return;
    const names = { ...profile.controlNames };
    if (value) names[key] = value;
    else delete names[key];
    const next: Profile = { ...profile, controlNames: names };
    if (!Object.keys(names).length) delete next.controlNames;
    edit(next);
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
  // What the profile takes from its parent for this control, and what it
  // overrides of it (every binding key the control has).
  const inheritance = usual
    ? keyInheritance(usual, parent, view, relatedKeys(control))
    : null;
  const inheritedFrom =
    inheritance && inheritance.inherited.length > 0 && !inheritance.own
      ? parentName(
          inheritance.from ?? parent?.id ?? "",
          state.profiles,
          language,
        )
      : "";
  const restorable = !!inheritance && inheritance.overridden.length > 0;
  const restore = () => {
    if (usual && !locked && inheritance)
      state.edit(restoreInherited(usual, view, inheritance.overridden));
  };
  const profileText = t(
    `配置「${profile?.name ?? state.id}」`,
    `Profile "${profile?.name ?? state.id}"`,
  );
  const glyphColor =
    kind === "button"
      ? faceColors[familyName(info.type)]?.[canonicalControl(control)]
      : undefined;

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
  // In a function key's set, a control the set does not mention does what it
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
  // The profile's own settings of a key give way to the parent's (PRO-19):
  // the control goes from the profile's file. A stick has no other action.
  const restoreItem: MenuEntry = {
    label: t("恢复为继承", "Restore inherited"),
    icon: "refresh",
    disabled: locked,
    onSelect: restore,
  };
  const keyMenu: MenuEntry[] =
    kind === "stick"
      ? restorable
        ? [restoreItem]
        : []
      : [
          {
            label: t("复制此按键的设置", "Copy this key's settings"),
            icon: "copy",
            disabled: !profile || !hasSettings,
            onSelect: () => {
              keyClipboard = structuredClone(binding);
              setClipboard(keyClipboard);
              setCopiedFor(key);
            },
          },
          {
            label: t("粘贴…", "Paste…"),
            icon: "paste",
            disabled: !clipboard || locked,
            onSelect: () => request("paste"),
          },
          {
            label: t("重命名按键…", "Rename key…"),
            icon: "edit",
            disabled: locked,
            onSelect: () => setRenamingFor(key),
          },
          ...(restorable ? [restoreItem] : []),
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
                onSelect: () => setBinding(undefined),
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
        <span
          className="drawer-glyph"
          data-shape={
            kind === "stick" ||
            ["south", "east", "west", "north"].includes(
              canonicalControl(control),
            )
              ? "round"
              : "square"
          }
          data-size={
            glyph.length > 3 ? "small" : glyph.length > 2 ? "medium" : undefined
          }
          style={glyphColor ? ({ color: glyphColor } as CSSProperties) : {}}
          aria-hidden="true"
        >
          {glyph}
        </span>
        <div className="drawer-heading">
          <KeyTitle
            title={title}
            name={name}
            placeholder={defaultTitle}
            disabled={locked}
            editing={renamingFor === key && !locked}
            onEditing={(editing) => setRenamingFor(editing ? key : null)}
            onRename={rename}
            language={language}
          />
          <p className="drawer-subtitle">
            <span className="drawer-subtitle-text" title={profileText}>
              {profileText}
            </span>
            {view !== "normal" && (
              <span className="tag" data-testid="drawer-view">
                {viewName(view, info, language)}
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
        {copiedFor === key && (
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
            {/* Holding a function key, a key the set leaves out falls to the
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
            {inheritedFrom && (
              <p className="drawer-muted" data-testid="drawer-parent">
                {t(
                  `继承自 ${inheritedFrom}`,
                  `Inherited from ${inheritedFrom}`,
                )}
              </p>
            )}
            {otherwiseText && (
              <p className="drawer-muted" data-testid="drawer-inherited">
                {t("平时：", "Normal: ")}
                {otherwiseText}
              </p>
            )}
            {kind === "stick" && panelProps ? (
              // The direction cards edit the shown set: a new view, new cards.
              <StickPanel
                key={view}
                {...panelProps}
                menuRequest={menuRequest}
              />
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
                  control={key}
                  value={binding}
                  onChange={setBinding}
                  timing={timing}
                  profiles={state.profiles}
                  actions={actions}
                  language={language}
                  disabled={locked}
                  registrations={keyScripts.visible}
                  suggestPress={
                    keyScripts.owners.length === 0 && !state.readOnly
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
                  {panelProps && kind === "dpad" && (
                    <div className="drawer-extra">
                      <Disclosure
                        title={t("十字键设置", "D-pad settings")}
                        summary={t("方向与斜向", "Directions and diagonals")}
                      >
                        <DpadSettings {...panelProps} />
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
            : t(
                `清空「${confirm?.label ?? ""}」的所有手势和显示文字。按键名称将保留。`,
                `Remove every gesture and the label of ${confirm?.label ?? ""}. The key name stays.`,
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
