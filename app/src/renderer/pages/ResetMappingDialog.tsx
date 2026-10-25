import { useEffect, useRef, useState } from "react";
import type { DeviceEntry, PadmateAPI } from "../../shared/api";
import { errorInfo } from "../library-model";
import { mappingFailureText, padSelector } from "../mapping-model";
import type { Language } from "../store";
import { Dialog, Notice } from "../ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * 恢复默认映射 (IN-18, ADR-0072): takes the mapping of the controller's GUID
 * away, after a confirm. SDL then reads its own mapping for the controller, or
 * none, and the controller is listed as unrecognised again. A failure stays in
 * the dialog.
 */
export function ResetMappingDialog({
  device,
  language,
  api,
  refresh,
  onClose,
}: {
  /** The controller to restore; the dialog is shown while there is one. */
  device: DeviceEntry | null;
  language: Language;
  api: Pick<PadmateAPI, "resetMapping">;
  /** Reads the controllers again, once the mapping is gone. */
  refresh: () => Promise<void>;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const alive = useRef(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Starts over only for another controller.
  useEffect(() => {
    alive.current = !!device;
    setBusy(false);
    setFailure("");
    // The dialog's own effect (a child) has run showModal already.
    cancel.current?.focus();
    return () => {
      alive.current = false;
    };
  }, [device?.info.id]);
  const restore = async () => {
    if (!device || busy) return;
    setBusy(true);
    setFailure("");
    try {
      await api.resetMapping(padSelector(device));
    } catch (error) {
      if (!alive.current) return;
      setBusy(false);
      setFailure(mappingFailureText(errorInfo(error), "reset", language));
      return;
    }
    await refresh().catch(() => {});
    if (!alive.current) return;
    setBusy(false);
    onClose();
  };
  const name = device?.info.name ?? "";
  return (
    <Dialog
      open={!!device}
      title={t("恢复默认映射？", "Restore the default mapping?")}
      closeLabel={t("取消恢复默认映射", "Cancel restoring the default mapping")}
      onClose={onClose}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button
            ref={cancel}
            type="button"
            className="btn-ghost"
            onClick={onClose}
          >
            {t("取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => void restore()}
          >
            {busy ? t("正在恢复…", "Restoring…") : t("恢复", "Restore")}
          </button>
        </>
      }
    >
      <p className="dialog-text">
        {t(
          `你为「${name}」做的映射会删除，它改回 SDL 自带的映射；SDL 没有的话，会回到未识别的状态。`,
          `The mapping you made for “${name}” is deleted, and it goes back to SDL's own mapping, or to unrecognised if SDL has none.`,
        )}
      </p>
      {failure && (
        <Notice kind="error" role="alert">
          {failure}
        </Notice>
      )}
    </Dialog>
  );
}
