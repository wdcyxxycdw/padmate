import { useEffect, useRef, useState } from "react";
import type { ConfigRepair, PadmateAPI } from "../shared/api";
import type { Language } from "./store";
import { Dialog } from "./ui/controls";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * The one dialog for a repaired config.json (PRO-29, ADR-0057): the daemon
 * found errors, backed the file up and reset what had them. It blocks the
 * window until the user confirms; 打开文件夹 selects the backup and leaves
 * the dialog open. Esc and × confirm too: there is nothing else to decide.
 */
export function ConfigRepairDialog({
  repair,
  language,
  api,
  onConfirm,
}: {
  /** null while there is nothing to tell. */
  repair: ConfigRepair | null;
  language: Language;
  api: Pick<PadmateAPI, "showConfigBackup">;
  onConfirm: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [problem, setProblem] = useState("");
  const confirm = useRef<HTMLButtonElement>(null);
  const open = !!repair;
  // The dialog's own effect (a child) has run showModal already.
  useEffect(() => {
    if (open) confirm.current?.focus();
    else setProblem("");
  }, [open]);
  // What it keeps showing when the last repair goes: the dialog renders
  // only while open, so this is read once, here.
  const backup = repair?.backup ?? "";
  return (
    <Dialog
      open={open}
      title={t("设置已恢复", "Settings reset")}
      closeLabel={t("关闭", "Close")}
      onClose={onConfirm}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button
            type="button"
            className="btn-ghost"
            onClick={() => {
              setProblem("");
              void (async () => {
                try {
                  await api.showConfigBackup(backup);
                } catch {
                  setProblem(
                    t("无法打开文件夹。", "Could not open the folder."),
                  );
                }
              })();
            }}
          >
            {t("打开文件夹", "Open folder")}
          </button>
          <button
            ref={confirm}
            type="button"
            className="btn-primary"
            onClick={onConfirm}
          >
            {t("确定", "OK")}
          </button>
        </>
      }
    >
      <p className="dialog-text">
        {language === "en" ? (
          <>
            The settings file had errors and was reset to defaults. The original
            was saved as <span className="mono">{backup}</span>.
          </>
        ) : (
          <>
            设置文件有误，已恢复默认设置。原文件已备份为{" "}
            <span className="mono">{backup}</span>。
          </>
        )}
      </p>
      {problem && (
        <p className="dialog-text" role="alert">
          {problem}
        </p>
      )}
    </Dialog>
  );
}
