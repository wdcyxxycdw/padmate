import { useMemo, useRef } from "react";
import { useStore } from "zustand";
import type { DeviceInfo } from "../shared/api";
import type { ProfileBinding } from "../shared/generated/schema";
import { calloutFor } from "./ControllerWorkspace";
import { ControlIcon, controlIcon } from "./control-icon";
import { listControlName } from "./control-names";
import {
  controllerLabels,
  familyName,
  padHasFunctionKey,
} from "./controller-model";
import {
  actionText,
  canonicalControl,
  gestureName,
  isFunctionKey,
  savedForm,
  swapView,
  usedGestures,
} from "./editor-model";
import type { EditorStore } from "./editor-store";
import { withInherited } from "./inheritance";
import { effectiveView, functionKeyName, viewName } from "./key-view";
import { defaultProfileName, isDefaultProfile } from "./library-model";
import { missingMacros } from "./macro-context";
import type { Language } from "./store";
import { Dialog } from "./ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

// The key list's names are shared with the scripts page.
export { listControlName };

/**
 * UI-08: every binding of the edited layer in one list, or of the function
 * set when the page shows it (UI-30); the function key is listed as such, and
 * there is nothing to open on it.
 */
export function BindingsListDialog({
  open,
  editor,
  info,
  language,
  onClose,
  onSelect,
}: {
  open: boolean;
  editor: EditorStore;
  info?: DeviceInfo;
  language: Language;
  onClose: () => void;
  onSelect: (control: string) => void;
}) {
  const draft = useStore(editor, (value) => value.draft);
  const pad = info ?? { type: "" };
  const functionKey = useStore(editor, (value) => value.functionKey);
  const view = effectiveView(
    useStore(editor, (value) => value.view),
    pad,
    functionKey,
  );
  // The profile a hand-written file extends (PRO-19, ADR-0076): what it takes
  // from it is in the list too, as the profile's own.
  const parent = useStore(editor, (value) => value.parent);
  // The list shows what is saved: a shortcut cleared in the drawer is not in
  // it, though the draft keeps its empty card (UI-43).
  const profile = useMemo(
    () => (draft ? savedForm(draft, parent) : null),
    [draft, parent],
  );
  const effective = useMemo(
    () => (profile ? withInherited(profile, parent) : null),
    [profile, parent],
  );
  // The set shown stands where the base layer does.
  const shown = useMemo(
    () => (effective ? swapView(effective, view) : null),
    [effective, view],
  );
  const id = useStore(editor, (value) => value.id);
  const actions = useStore(
    editor,
    (value) => value.description?.metadata.actions,
  );
  const profiles = useStore(editor, (value) => value.profiles);
  const macros = useStore(editor, (value) => value.macros);
  // The chip a row opened. Closing then focuses the drawer it opened (or the
  // chip) instead of the menu button that opened the list.
  const chosen = useRef("");
  if (!profile || !effective || !shown) return null;
  const missing = missingMacros(macros);
  const labels = info
    ? controllerLabels(info.type, info.buttonLabels, info.buttonMask)
    : {};
  const family = familyName(pad.type);
  const fixedName = functionKeyName(functionKey, pad);
  const functionKeyHint = tx(
    language,
    "按住时其他键换成另一套",
    "While held, other keys switch to another set",
  );
  // What a set binds on the function key is ignored (ADR-0075): not listed.
  const rows = Object.entries(shown.layers.base).filter(
    ([key, binding]) =>
      key !== "color" &&
      !isFunctionKey(key, functionKey) &&
      typeof binding === "object" &&
      binding,
  ) as [string, ProfileBinding][];
  return (
    <Dialog
      open={open}
      title={tx(language, "所有按键", "All keys")}
      subtitle={[
        isDefaultProfile(id) ? defaultProfileName(language) : profile.name,
        ...(view === "normal" ? [] : [viewName(view, language)]),
      ].join(" · ")}
      closeLabel={tx(language, "关闭按键列表", "Close key list")}
      onClose={onClose}
      className="bindings-dialog"
      returnFocus={() => {
        const callout = chosen.current;
        chosen.current = "";
        if (!callout) return null;
        return (
          // The drawer's heading, where the drawer puts focus when it opens.
          document.querySelector<HTMLElement>(
            '[data-testid="mapping-editor"] .drawer-title[tabindex]',
          ) ??
          document.querySelector<HTMLElement>(
            `button[data-callout="${CSS.escape(callout)}"]`,
          )
        );
      }}
    >
      {rows.length ? null : (
        <p className="dialog-text">
          {tx(language, "尚未设置任何按键。", "No keys are set yet.")}
        </p>
      )}
      <ul className="bindings-list">
        {rows.map(([control, binding]) => {
          const key = canonicalControl(control);
          return (
            <li key={control}>
              <button
                type="button"
                onClick={() => {
                  chosen.current = calloutFor(key);
                  onSelect(chosen.current);
                }}
              >
                <span className="bindings-control">
                  <ControlIcon icon={controlIcon(key, family, labels)} />
                  {effective.controlNames?.[key] ??
                    listControlName(key, labels, language)}
                </span>
                <span className="bindings-label">{binding.label ?? ""}</span>
                <span className="bindings-actions">
                  {binding.block
                    ? tx(
                        language,
                        "已屏蔽（在配置文件中设置）",
                        "Blocked (set in file)",
                      )
                    : usedGestures(binding)
                        .map((gesture) =>
                          [
                            gestureName(gesture, language),
                            // A script (macro) is marked like on a chip.
                            binding[gesture]?.type === "macro"
                              ? tx(language, "脚本", "Script")
                              : "",
                            actionText(
                              binding[gesture],
                              language,
                              actions,
                              profiles,
                              missing,
                            ),
                          ]
                            .filter(Boolean)
                            .join(" "),
                        )
                        .join(" · ")}
                </span>
              </button>
            </li>
          );
        })}
        {padHasFunctionKey(functionKey, pad.type, info?.buttonMask) && (
          <li>
            <div
              className="bindings-fixed"
              data-testid="function-key-row"
              title={functionKeyHint}
            >
              <span className="bindings-control">
                <ControlIcon
                  icon={controlIcon(functionKey, family, {
                    [functionKey]: fixedName,
                  })}
                />
                {fixedName}
              </span>
              <span className="bindings-label">
                {tx(language, "功能键", "Function key")}
              </span>
              <span className="bindings-actions">{functionKeyHint}</span>
            </div>
          </li>
        )}
      </ul>
    </Dialog>
  );
}
