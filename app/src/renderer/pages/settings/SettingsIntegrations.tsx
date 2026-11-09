import { useCallback, useEffect, useState } from "react";
import { useStore } from "zustand";
import {
  hasBound,
  type IntegrationBackup,
  type IntegrationInfo,
} from "../../../shared/integrations";
import {
  integrationFailure,
  noStore,
  useIntegrationHost,
} from "../../integrations/IntegrationContext";
import { backupTime } from "../../state/integrations-model";
import type { Language } from "../../state/store";
import { Dialog } from "../../ui/controls";
import "../pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** A short account of a backup: what the write changed, or that it was made before a restore. */
export function backupSummary(
  backup: IntegrationBackup,
  language: Language,
): string {
  if (backup.reason === "beforeRestore")
    return tx(language, "恢复前的备份", "Before restore");
  return backup.changes > 0
    ? tx(language, `写入 ${backup.changes} 项`, `Wrote ${backup.changes}`)
    : tx(language, "写入", "Wrote");
}

/**
 * That the command names cannot be read from the app itself, in one sentence;
 * the commands then show their internal names. null when they could be read.
 */
export function namesProblemText(
  info: IntegrationInfo,
  language: Language,
): string | null {
  if (!info.namesProblem) return null;
  return tx(
    language,
    `读不到 ${info.name} 里的命令名称，部分命令会显示内部名称。`,
    `The command names cannot be read from ${info.name}, so some commands show their internal names.`,
  );
}

/** Why an integration cannot be used, as the data folder row says it. */
export function problemText(
  info: IntegrationInfo,
  language: Language,
): string | null {
  switch (info.problem) {
    case "notFound":
      return tx(
        language,
        `没有找到 ${info.name} 的设置文件夹。`,
        `The settings folder of ${info.name} was not found.`,
      );
    case "unknownFormat":
      return tx(
        language,
        `${info.name} 的设置文件格式不认识，这项功能已关闭。`,
        `The settings files of ${info.name} are in a format padmate does not know, so this is turned off.`,
      );
    case "unsupported":
      return tx(
        language,
        `这个版本的 ${info.name} 暂不支持。`,
        `This version of ${info.name} is not supported.`,
      );
    default:
      return null;
  }
}

/**
 * One section of Settings per integration that at least one profile is bound
 * to (ARCHITECTURE §16.5): where the app's settings are, and the backups made
 * before each write, newest first, with 恢复…. Nothing is shown otherwise.
 */
export function IntegrationSections({ language }: { language: Language }) {
  const host = useIntegrationHost();
  const list = useStore(host?.store ?? noStore, (state) => state.integrations);
  if (!host) return null;
  return (
    <>
      {(list ?? []).filter(hasBound).map((info) => (
        <IntegrationSection key={info.id} info={info} language={language} />
      ))}
    </>
  );
}

function IntegrationSection({
  info,
  language,
}: {
  info: IntegrationInfo;
  language: Language;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const host = useIntegrationHost();
  const [backups, setBackups] = useState<IntegrationBackup[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [restoring, setRestoring] = useState<IntegrationBackup | null>(null);
  const [problem, setProblem] = useState("");
  const [done, setDone] = useState("");
  const [folderProblem, setFolderProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const api = host?.store.getState().api;
  const load = useCallback(async () => {
    if (!api) return;
    try {
      setBackups(await api.listIntegrationBackups(info.id));
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, [api, info.id]);
  useEffect(() => {
    void load();
  }, [load]);
  const trouble = problemText(info, language);
  const namesTrouble = namesProblemText(info, language);
  const refreshList = () => host?.store.getState().refreshIntegrations();
  // The folder dialog is Main's; it answers whether the folder was taken.
  const chooseFolder = async () => {
    if (!api || busy) return;
    setBusy(true);
    setProblem("");
    setDone("");
    setFolderProblem("");
    try {
      const choice = await api.chooseIntegrationDataDir(info.id);
      if (choice === "notAppFolder")
        setFolderProblem(
          t(
            `这个文件夹里没有 ${info.name} 的设置文件。`,
            `This folder has no settings files of ${info.name}.`,
          ),
        );
      else if (choice === "changed") await refreshList();
    } catch (error) {
      setFolderProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };
  const findAutomatically = async () => {
    if (!api || busy) return;
    setBusy(true);
    setProblem("");
    setDone("");
    setFolderProblem("");
    try {
      await api.resetIntegrationDataDir(info.id);
      await refreshList();
    } catch (error) {
      setFolderProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };
  const restore = async () => {
    if (!host || !restoring || busy) return;
    setBusy(true);
    setProblem("");
    try {
      if (!(await host.requireClosed(info))) return;
      await host.store
        .getState()
        .api.restoreIntegrationBackup(info.id, restoring.id);
      setDone(
        t(
          `已恢复到 ${backupTime(restoring.created, language)} 的备份。`,
          `Restored the backup of ${backupTime(restoring.created, language)}.`,
        ),
      );
      setRestoring(null);
      await load();
    } catch (error) {
      setProblem(integrationFailure(error, info.name, language));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section
      className="page-section"
      data-testid={`integration-section-${info.id}`}
    >
      <h2 className="section-title">{info.name}</h2>
      <div className="card">
        <div className="row">
          <div className="row-text">
            <span className="row-title">{t("数据位置", "Data folder")}</span>
            <span className="row-hint mono" data-testid="integration-datadir">
              {info.dataDir ?? t("未找到", "Not found")}
            </span>
            {trouble && (
              <span className="row-hint" role="status">
                {trouble}
              </span>
            )}
            {namesTrouble && (
              <span
                className="row-hint"
                data-testid="integration-names-problem"
              >
                {namesTrouble}
              </span>
            )}
            {folderProblem && (
              <span className="row-hint" role="alert">
                {folderProblem}
              </span>
            )}
          </div>
          {info.custom && (
            <button
              type="button"
              className="btn"
              disabled={busy}
              onClick={() => void findAutomatically()}
            >
              {t("恢复自动查找", "Find automatically")}
            </button>
          )}
          <button
            type="button"
            className="btn"
            disabled={busy}
            onClick={() => void chooseFolder()}
          >
            {t("更换…", "Change…")}
          </button>
        </div>
        <div className="row pad-subhead">
          <span className="row-title">{t("备份", "Backups")}</span>
        </div>
        {failed ? (
          <div className="row">
            <span className="row-hint" role="alert">
              {t("备份列表没有读到。", "The backups could not be read.")}
            </span>
          </div>
        ) : backups && backups.length === 0 ? (
          <div className="row">
            <span className="row-hint">
              {t("还没有备份。", "No backups yet.")}
            </span>
          </div>
        ) : (
          (backups ?? []).map((backup) => (
            <div
              className="row"
              key={backup.id}
              data-testid="integration-backup"
            >
              <div className="row-text">
                <span className="row-title">
                  {backupTime(backup.created, language)}
                </span>
                <span className="row-hint">
                  {backupSummary(backup, language)}
                </span>
              </div>
              <button
                type="button"
                className="btn"
                aria-label={t(
                  `恢复 ${backupTime(backup.created, language)} 的备份`,
                  `Restore the backup of ${backupTime(backup.created, language)}`,
                )}
                onClick={() => {
                  setProblem("");
                  setDone("");
                  setRestoring(backup);
                }}
              >
                {t("恢复…", "Restore…")}
              </button>
            </div>
          ))
        )}
        {done && (
          <div className="row">
            <span className="row-hint" role="status">
              {done}
            </span>
          </div>
        )}
      </div>
      <Dialog
        open={!!restoring}
        title={t("恢复这份备份？", "Restore this backup?")}
        closeLabel={t("关闭", "Close")}
        onClose={() => setRestoring(null)}
        className="confirm-dialog"
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn-ghost"
              disabled={busy}
              onClick={() => setRestoring(null)}
            >
              {t("取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              disabled={busy}
              onClick={() => void restore()}
            >
              {t("恢复", "Restore")}
            </button>
          </>
        }
      >
        <p className="dialog-text">
          {t(
            `${info.name} 的快捷键设置会换回 ${restoring ? backupTime(restoring.created, language) : ""} 的样子。恢复前会先备份现在的设置。`,
            `The shortcut settings of ${info.name} go back to ${restoring ? backupTime(restoring.created, language) : ""}. The current settings are backed up first.`,
          )}
        </p>
        {problem && (
          <p className="dialog-text" role="alert">
            {problem}
          </p>
        )}
      </Dialog>
    </section>
  );
}
