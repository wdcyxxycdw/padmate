import { useEffect, useRef, useState } from "react";
import type { DeviceCombo, DeviceEntry } from "../../shared/api";
import type { ConfigCombo } from "../../shared/generated/schema";
import { controllerLabels } from "../controller-model";
import { failureText, type LibraryOutcome } from "../library-model";
import {
  COMBO_HOLDS,
  COMBO_MAX_BUTTONS,
  COMBO_MIN_BUTTONS,
  ComboRecorder,
  comboProblem,
} from "../settings-model";
import type { Language, PadmateStore, ReportOptions } from "../store";
import { Dialog, Segmented } from "../ui/controls";
import { Keycaps } from "../ui/Keycaps";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** A whole number of ms in seconds, as exact as it is ("1.5 秒", "1.49 秒"). */
export function seconds(ms: number, language: Language) {
  const value = ms / 1000;
  return tx(language, `${value} 秒`, `${value} s`);
}

// A vertical single Joy-Con reports SL and SR as paddles (ARCHITECTURE §7.1).
const verticalLabels: Record<string, Record<string, string>> = {
  joyconLeftVertical: { paddle2: "SL", paddle4: "SR" },
  joyconRightVertical: { paddle3: "SL", paddle1: "SR" },
};

/**
 * The buttons of a combo with the labels printed on device; family is the
 * combo's, which tells a vertical Joy-Con.
 */
export function comboKeys(
  device: DeviceEntry | undefined,
  buttons: string[],
  family = "",
) {
  const labels = controllerLabels(
    device?.info.type ?? "xboxone",
    device?.info.buttonLabels,
    device?.info.buttonMask,
  );
  const vertical = verticalLabels[family];
  const single =
    !vertical &&
    (device?.info.type === "joyconleft" || device?.info.type === "joyconright");
  return buttons.map((button) => ({
    button,
    label:
      vertical?.[button] ??
      (single && button === "leftshoulder"
        ? "SL"
        : single && button === "rightshoulder"
          ? "SR"
          : (labels[button] ?? button)),
  }));
}

/** A combo as keycaps joined with "+". */
export function ComboKeys({
  device,
  buttons,
  family,
}: {
  device: DeviceEntry | undefined;
  buttons: string[];
  family?: string;
}) {
  const keys = comboKeys(device, buttons, family);
  return (
    <span
      className="page-combo"
      role="img"
      aria-label={keys.map((key) => key.label).join(" + ")}
    >
      {keys.map((key, index) => (
        <span key={key.button} className="page-combo">
          {index > 0 && <span aria-hidden="true">+</span>}
          <Keycaps keys={key.label} />
        </span>
      ))}
    </span>
  );
}

/**
 * Records a new emergency combo on the controller (UI-19). While it is
 * open the mapping is paused for the capture, and input still comes: the
 * buttons held together, once all are released, are the new combo. It
 * edits the family of device's combo only.
 */
export function PanicComboDialog({
  open,
  language,
  store,
  device,
  combo,
  configured,
  busy,
  onSave,
  onClose,
}: {
  open: boolean;
  language: Language;
  store: PadmateStore;
  device: DeviceEntry;
  combo: DeviceCombo;
  /** config.json sets this family, so 恢复默认 can remove it. */
  configured: boolean;
  /** Settings cannot be written now. */
  busy: boolean;
  /**
   * Writes the combo, or with null removes the family's entry. The dialog
   * shows a failure itself while it is open (options.inline).
   */
  onSave: (
    combo: ConfigCombo | null,
    options: ReportOptions,
  ) => Promise<LibraryOutcome>;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const api = store.getState().api;
  const id = device.info.id;
  const [recorded, setRecorded] = useState<string[] | null>(null);
  const [pressed, setPressed] = useState<string[]>([]);
  const [hold, setHold] = useState(combo.holdMs);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  // Read when a save ends: whether the dialog can still show its failure.
  const shownRef = useRef(false);
  useEffect(() => {
    shownRef.current = open;
    return () => {
      shownRef.current = false;
    };
  }, [open]);
  // Each time it opens, from the combo that applies now.
  useEffect(() => {
    if (!open) return;
    setSaving(false);
    setFailure(null);
    setHold(combo.holdMs);
  }, [open, combo.holdMs]);
  // The current hold stays a choice when it is not one of the usual ones.
  const holds: number[] = [...COMBO_HOLDS];
  if (!holds.includes(combo.holdMs)) {
    holds.push(combo.holdMs);
    holds.sort((a, b) => a - b);
  }

  // Main stops the capture when the window loses focus; it starts again
  // when the window has it back.
  useEffect(() => {
    if (!open) return;
    const start = () => void api.padCaptureStart().catch(() => {});
    start();
    window.addEventListener("focus", start);
    return () => {
      window.removeEventListener("focus", start);
      void api.padCaptureStop().catch(() => {});
    };
  }, [open, api]);

  // A new recording each time it opens. It watches every change of the
  // store, not each render: one render may cover several input events.
  // Presses count only while the window has focus: without it the capture
  // is off and they are shortcuts in other programs. A blur drops the
  // recording, and a recording starts only once every button is up.
  useEffect(() => {
    if (!open) return;
    let recorder = new ComboRecorder();
    let focused = true;
    let waiting = true;
    setRecorded(null);
    setPressed([]);
    // null: nothing observed yet, so the first state always counts.
    let last: Record<string, boolean> | undefined | null = null;
    const observe = (buttons: Record<string, boolean> | undefined) => {
      if (buttons === last) return;
      last = buttons;
      if (!focused) return;
      const held = Object.keys(buttons ?? {}).filter((name) => buttons?.[name]);
      if (waiting) {
        if (held.length > 0) return;
        waiting = false;
      }
      const finished = recorder.update(held);
      setPressed(recorder.pressed);
      if (finished) setRecorded(recorder.recorded);
    };
    const current = () => store.getState().inputs[id]?.buttons;
    const blur = () => {
      focused = false;
      waiting = true;
      recorder = new ComboRecorder();
      setRecorded(null);
      setPressed([]);
    };
    const focus = () => {
      focused = true;
      last = null;
      observe(current());
    };
    observe(current());
    window.addEventListener("blur", blur);
    window.addEventListener("focus", focus);
    const unsubscribe = store.subscribe((state) =>
      observe(state.inputs[id]?.buttons),
    );
    return () => {
      window.removeEventListener("blur", blur);
      window.removeEventListener("focus", focus);
      unsubscribe();
    };
  }, [open, store, id]);

  const candidate = recorded ?? combo.buttons;
  const problem = recorded ? comboProblem(recorded) : null;
  const changed = !!recorded || hold !== combo.holdMs;
  const ready = !saving && !busy && changed && comboProblem(candidate) === null;
  const finish = async (next: ConfigCombo | null) => {
    setSaving(true);
    setFailure(null);
    const outcome = await onSave(next, { inline: () => shownRef.current });
    if (!shownRef.current) return;
    setSaving(false);
    if (outcome.ok) onClose();
    else setFailure(failureText(outcome.failure, language));
  };
  const shown = pressed.length > 0 ? pressed : candidate;

  return (
    <Dialog
      open={open}
      title={t("修改紧急暂停", "Change emergency pause")}
      subtitle={t(
        "按住新的组合键，然后松开",
        "Hold the new combo, then let go",
      )}
      closeLabel={t("取消修改紧急暂停", "Cancel emergency pause change")}
      onClose={onClose}
      className="combo-dialog"
      footer={
        <>
          <button
            type="button"
            className="btn-ghost"
            disabled={!configured || saving || busy}
            onClick={() => void finish(null)}
          >
            {t("恢复默认", "Restore default")}
          </button>
          <span className="spacer" />
          <button type="button" className="btn-ghost" onClick={onClose}>
            {t("取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn-primary"
            disabled={!ready}
            onClick={() =>
              void finish({ buttons: [...candidate], holdMs: hold })
            }
          >
            {saving ? t("正在保存…", "Saving…") : t("保存", "Save")}
          </button>
        </>
      }
    >
      <div className="combo-record" data-testid="combo-record">
        <ComboKeys device={device} buttons={shown} family={combo.family} />
        <p
          className="combo-record-note"
          role={problem || failure ? "alert" : undefined}
          data-problem={problem || failure ? "true" : undefined}
        >
          {problem
            ? t(
                `需同时按住 ${COMBO_MIN_BUTTONS}–${COMBO_MAX_BUTTONS} 个按键`,
                `Hold ${COMBO_MIN_BUTTONS}–${COMBO_MAX_BUTTONS} buttons together`,
              )
            : (failure ??
              t(
                "保存前，原组合键仍可用于暂停",
                "The old combo works until you save",
              ))}
        </p>
      </div>
      <div className="field">
        <span className="field-label">{t("按住时间", "Hold for")}</span>
        <Segmented
          label={t("按住时间", "Hold for")}
          options={holds.map((ms) => ({
            value: String(ms),
            label: seconds(ms, language),
          }))}
          value={String(hold)}
          onChange={(value) => setHold(Number(value))}
        />
      </div>
    </Dialog>
  );
}
