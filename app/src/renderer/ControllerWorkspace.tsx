import { useEffect, useMemo, useRef, useState } from "react";
import { useStore } from "zustand";
import type { DeviceInfo, ProfileDiagnostic } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { Controller } from "./Controller";
import { calibrationStick } from "./calibration-model";
import { calloutFor } from "./control-callout";
import { analogControlPart } from "./control-parts";
import {
  bindingKey,
  controllerChips,
  inheritedChips,
  rawKeyName,
} from "./controller-chips";
import { controllerLabels, familyName } from "./controller-model";
import { EditorNotice, hasEditorNotice } from "./EditorNotice";
import {
  canonicalControl,
  diagnosticSet,
  type Gesture,
  gestureName,
  gestures,
  isFunctionKey,
  type KeyView,
  savedForm,
  swapView,
} from "./editor-model";
import type { EditorStore } from "./editor-store";
import type { Translate } from "./i18n";
import { withInherited } from "./inheritance";
import { KeyDrawer } from "./KeyDrawer";
import { KeyPreview } from "./KeyPreview";
import { KeyViewSwitch } from "./KeyViewSwitch";
import { effectiveView, viewRegistrations } from "./key-view";
import { readConfig } from "./library-model";
import { failingMacros, MacroContext, missingMacros } from "./macro-context";
import type { LabelDisplay, Language, PadmateStore } from "./store";
import { useUndoShortcuts } from "./undo-shortcuts";
import "./workspace.css";

// Moved to control-callout.ts so the overlay's page can name controls without
// loading the workspace; other modules still import it from here.
export { calloutFor };

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** Value of an own property, so pointer parts such as "constructor" miss. */
function own<T>(record: Record<string, T>, key: string | undefined) {
  return key !== undefined && Object.hasOwn(record, key)
    ? record[key]
    : undefined;
}

function pointerParts(pointer: string): string[] {
  return pointer
    .split("/")
    .slice(1)
    .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
}

const analogCallouts: Record<string, string> = {
  ls: "leftstick",
  rs: "rightstick",
  lt: "lefttrigger",
  rt: "righttrigger",
  dpad: "dpad.up",
  // Shared by every raw axis; the stick drawers show it under 高级.
  axisThreshold: "leftstick",
};

/** Callout control a diagnostic points at, when the editor shows that part. */
export function diagnosticControl(
  diagnostic: ProfileDiagnostic,
): string | null {
  const bits = pointerParts(diagnostic.pointer);
  // The function set is edited like the base layer. `color` is a property
  // of the key group, not a control.
  const found = diagnosticSet(diagnostic.pointer);
  if (found?.rest[0] && found.rest[0] !== "color")
    return calloutFor(found.rest[0]);
  if (bits[0] === "controlNames" && bits[1]) return calloutFor(bits[1]);
  if (bits[0] === "analog") return own(analogCallouts, bits[1]) ?? null;
  return null;
}

/**
 * The key set a diagnostic is about, when it points into one: the function
 * set, or the base layer (the usual bindings). Null for the rest (names,
 * stick settings), which show in any view.
 */
export function diagnosticView(diagnostic: ProfileDiagnostic): KeyView | null {
  return diagnosticSet(diagnostic.pointer)?.view ?? null;
}

/**
 * Whether a diagnostic is about the profile's name or timing (配置设置). The
 * members of feedback are not in the dialog: its switches are global now
 * (ADR-0067), and its times, strength and pause color are not shown.
 */
export function diagnosticProfileSetting(
  diagnostic: ProfileDiagnostic,
): boolean {
  const [first] = pointerParts(diagnostic.pointer);
  return first === "name" || first === "timing";
}

const timingFields: Record<string, [string, string]> = {
  holdMs: ["触发长按时间", "Long-press delay"],
  multiTapMs: ["连击间隔", "Multi-tap window"],
  tapPressMs: ["点按时长", "Tap duration"],
  tapGapMs: ["点按间隔", "Tap gap"],
};

const analogSettings: Record<string, [string, string]> = {
  ls: ["摇杆设置", "Stick settings"],
  rs: ["摇杆设置", "Stick settings"],
  lt: ["扳机设置", "Trigger settings"],
  rt: ["扳机设置", "Trigger settings"],
  dpad: ["十字键设置", "D-pad settings"],
};

const bindingFields: Record<string, [string, string]> = {
  label: ["显示文字", "Label"],
  timing: ["判定时间", "Timing"],
  policy: ["打断判定", "Interruptions"],
  latch: ["锁存", "Latch"],
  haptic: ["触感", "Haptic"],
};

const arrows: Record<string, string> = {
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  upleft: "↖",
  upright: "↗",
  downleft: "↙",
  downright: "↘",
};

/** Which part of a control's drawer a diagnostic is in, e.g. "↑ 长按". */
function diagnosticPart(
  bits: string[],
  control: string,
  language: Language,
): string {
  const words = (pair?: [string, string]) =>
    pair ? tx(language, pair[0], pair[1]) : "";
  if (bits[0] === "controlNames") return tx(language, "按键名称", "Key name");
  if (bits[0] === "analog") return words(own(analogSettings, bits[1]));
  const key = canonicalControl(bits[2] ?? "");
  const direction = key.split(".")[1];
  const which =
    key === bindingKey(control) || key === control
      ? ""
      : direction === "active"
        ? tx(language, "任意方向", "Any direction")
        : (analogControlPart(key, language) ??
          (direction
            ? (own(arrows, direction) ?? direction)
            : rawKeyName(key, language)));
  const field = bits[3];
  const fieldName = gestures.includes(field as Gesture)
    ? gestureName(field as Gesture, language)
    : words(own(bindingFields, field));
  return [which, fieldName].filter(Boolean).join(" ");
}

/** Where a diagnostic is, in the words the editor uses (the notice's problem list). */
export function describeDiagnostic(
  diagnostic: ProfileDiagnostic,
  profile: Profile | null,
  labels: Record<string, string>,
  language: Language,
): string {
  const rawBits = pointerParts(diagnostic.pointer);
  // A binding in the function set reads like one of the base layer, under the
  // name of the set.
  const found = diagnosticSet(diagnostic.pointer);
  const inSet = found?.view === "function";
  const bits = inSet ? ["layers", "base", ...found.rest] : rawBits;
  if (diagnosticProfileSetting(diagnostic)) {
    const [zh, en] =
      bits[0] === "name"
        ? ["名称", "Name"]
        : (own(timingFields, bits[1]) ?? ["按键判定", "Key timing"]);
    return `${tx(language, "配置设置", "Profile settings")} · ${tx(language, zh, en)}`;
  }
  if (bits[0] === "analog" && bits[1] === "axisThreshold")
    return tx(language, "原始轴阈值", "Raw axis threshold");
  // One setting for the whole D-pad, not for the direction that opens it.
  if (bits[0] === "analog" && bits[1] === "dpad")
    return tx(language, "十字键设置", "D-pad settings");
  const control = diagnosticControl(diagnostic);
  if (!control)
    return tx(
      language,
      "界面中未显示的部分",
      "In a part of the file the editor does not show",
    );
  const label = labels[control] ?? control;
  const name =
    profile?.controlNames?.[bindingKey(control)]?.trim() ||
    (control === "leftstick"
      ? tx(language, "左摇杆", "Left stick")
      : control === "rightstick"
        ? tx(language, "右摇杆", "Right stick")
        : control.startsWith("dpad.")
          ? tx(language, `十字键 ${label}`, `D-pad ${label}`)
          : tx(language, `${label} 键`, `${label} key`));
  const part = diagnosticPart(bits, control, language);
  const where = part ? `${name} · ${part}` : name;
  return inSet
    ? `${tx(language, "按住功能键", "Hold function key")} · ${where}`
    : where;
}

export function ControllerWorkspace({
  store,
  info,
  t,
  editor,
  language,
  labelDisplay,
  selected,
  onSelect,
  onShowScripts,
}: {
  store: PadmateStore;
  info: DeviceInfo;
  t: Translate;
  editor: EditorStore;
  language: Language;
  labelDisplay: LabelDisplay;
  selected?: string;
  onSelect: (control: string | undefined) => void;
  /** Opens the 脚本 page (from the drawer's script ownership notice). */
  onShowScripts?: () => void;
}) {
  const root = useRef<HTMLDivElement>(null);
  // The function key only switches sets: there is nothing to open on it.
  const functionKey = useStore(editor, (value) => value.functionKey);
  const open =
    selected && !isFunctionKey(selected, functionKey) ? selected : undefined;
  const input = useStore(store, (value) => value.inputs[info.id]);
  // The sticks this controller has its own calibration for (IN-14), as a
  // string so the selector's result compares by value.
  const calibratedText = useStore(store, (value) => {
    const key = value.status?.devices.find(
      (entry) => entry.info.id === info.id,
    )?.key;
    const config = key ? readConfig(value.config) : null;
    return (["ls", "rs"] as const)
      .filter(
        (stick) =>
          key && Object.keys(calibrationStick(config, key, stick)).length > 0,
      )
      .join(",");
  });
  const calibrated = useMemo(
    () => ({
      ls: calibratedText.split(",").includes("ls"),
      rs: calibratedText.split(",").includes("rs"),
    }),
    [calibratedText],
  );
  const draft = useStore(editor, (value) => value.draft);
  const allRegistrations = useStore(editor, (value) => value.registrations);
  // The usual bindings, or the function set (UI-30, ADR-0075).
  const view = effectiveView(
    useStore(editor, (value) => value.view),
    info,
    functionKey,
  );
  const registrations = useMemo(
    () => viewRegistrations(allRegistrations, view),
    [allRegistrations, view],
  );
  const actions = useStore(
    editor,
    (value) => value.description?.metadata.actions,
  );
  const scriptEntry = useStore(editor, (value) => value.description?.entry);
  const profiles = useStore(editor, (value) => value.profiles);
  // The profile a hand-written file extends (PRO-19, ADR-0076): its bindings
  // show here as the profile's own.
  const parent = useStore(editor, (value) => value.parent);
  // The profile's scripts (SCR-50): the drawer picks from them, and a key
  // that runs a missing one says so.
  const macros = useStore(editor, (value) => value.macros);
  const editedId = useStore(editor, (value) => value.id);
  const scripts = useStore(store, (value) => value.scripts);
  const missing = useMemo(() => missingMacros(macros), [macros]);
  const macroContext = useMemo(
    () => ({
      list: macros,
      failing: failingMacros(macros, scripts, editedId),
      onShowScripts,
    }),
    [macros, scripts, editedId, onShowScripts],
  );
  // The scripts page, the editor or another tool may have changed the
  // macros folder: read the list again when the drawer opens and when the
  // window gets focus back (the daemon announces changes only with --watch).
  const drawerOpen = !!open;
  useEffect(() => {
    if (drawerOpen) void editor.getState().refreshMacros();
  }, [editor, drawerOpen]);
  useEffect(() => {
    const focused = () => void editor.getState().refreshMacros();
    window.addEventListener("focus", focused);
    return () => window.removeEventListener("focus", focused);
  }, [editor]);
  // The hint would sit under the floating notice, or the key preview.
  const notice = useStore(editor, hasEditorNotice);
  const [previewing, setPreviewing] = useState(false);
  useUndoShortcuts(editor, store);
  // A chip's right-click (UI-36) asks the drawer to open its menu once. The
  // request drops back to 0 after the drawer has seen it, so a drawer part
  // that mounts later (another control, the drawer opened again) stays closed.
  const [menu, setMenu] = useState({ request: 0, pending: false });
  useEffect(() => {
    if (menu.pending) setMenu((value) => ({ ...value, pending: false }));
  }, [menu.pending]);
  const { type, buttonLabels, buttonMask } = info;
  const labels = useMemo(
    () => controllerLabels(type, buttonLabels, buttonMask),
    [type, buttonLabels, buttonMask],
  );
  // The chips show what is saved: a shortcut cleared in the drawer is unset
  // at once, though the draft keeps its empty card until the gesture is
  // deleted (UI-43). A key emptied that the parent binds stays empty.
  const shown = useMemo(
    () => (draft ? savedForm(draft, parent) : null),
    [draft, parent],
  );
  // With what the profile takes from the profile it extends.
  const effective = useMemo(
    () => (shown ? withInherited(shown, parent) : null),
    [shown, parent],
  );
  const chips = useMemo(() => {
    const make = (profile: Profile | null, scripts: typeof allRegistrations) =>
      controllerChips({
        profile,
        controls: Object.keys(labels),
        labels,
        registrations: scripts,
        display: labelDisplay,
        language,
        actions,
        profiles,
        missing,
        scriptEntry: shown?.script?.entry ?? scriptEntry,
        functionKey,
      });
    if (view === "normal") return make(effective, allRegistrations);
    // In the function set, a control the set leaves out does what it does
    // otherwise: its usual chip, greyed and noted.
    return inheritedChips({
      own: make(effective && swapView(effective, view), registrations),
      normal: make(effective, allRegistrations),
      language,
    });
  }, [
    missing,
    shown,
    effective,
    view,
    labels,
    allRegistrations,
    registrations,
    labelDisplay,
    language,
    actions,
    profiles,
    scriptEntry,
    functionKey,
  ]);
  const select = (control: string | undefined) => {
    if (control === undefined || !isFunctionKey(control, functionKey))
      onSelect(control);
  };
  const close = () => {
    // Return focus to the chip of the closed control, where editing started.
    const chip = open
      ? root.current?.querySelector<HTMLButtonElement>(
          `button[data-callout="${open}"]`,
        )
      : null;
    chip?.focus();
    onSelect(undefined);
  };
  return (
    <div ref={root} className="controller-workspace" data-editing={!!open}>
      <div className="controller-view">
        <KeyViewSwitch editor={editor} pad={info} language={language} />
        <Controller
          info={info}
          input={input}
          t={t}
          language={language}
          selectedControl={open}
          onSelectControl={select}
          onMenuControl={() =>
            setMenu((value) => ({ request: value.request + 1, pending: true }))
          }
          chips={chips}
          hint={!open && !notice && !previewing}
          functionKey={functionKey}
        />
        {/* The key preview (UI-44) above the notice, where the hint is. */}
        <div className="workspace-bottom">
          <KeyPreview
            store={store}
            device={info.id}
            labels={labels}
            family={familyName(type)}
            scriptActions={actions}
            language={language}
            onShown={setPreviewing}
          />
          <EditorNotice
            editor={editor}
            language={language}
            describe={(diagnostic) =>
              describeDiagnostic(diagnostic, draft, labels, language)
            }
            onLocate={(diagnostic) => {
              // Name and timing are in 配置设置; describe() says so.
              if (diagnosticProfileSetting(diagnostic)) return;
              const control = diagnosticControl(diagnostic);
              if (!control || isFunctionKey(control, functionKey)) return;
              // The key sits in a set: show that set (or the usual keys).
              const shown = diagnosticView(diagnostic);
              if (shown) editor.getState().setView(shown);
              onSelect(control);
            }}
          />
        </div>
      </div>
      {open && (
        <div className="drawer-host">
          <MacroContext value={macroContext}>
            <KeyDrawer
              editor={editor}
              control={open}
              onControl={select}
              info={info}
              input={input}
              calibrated={calibrated}
              language={language}
              onClose={close}
              menuRequest={menu.pending ? menu.request : 0}
              onShowScripts={onShowScripts}
            />
          </MacroContext>
        </div>
      )}
    </div>
  );
}
