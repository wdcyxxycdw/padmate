import { type ReactNode, useEffect, useRef } from "react";
import type { UpdateDialogKind } from "../shared/update";
import { useCaptureRunning } from "./capture-hold";
import type { Language } from "./store";
import { Dialog } from "./ui/controls";
import { type UpdateApi, useUpdateState } from "./update-model";
import "./update.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * The blocking dialogs of the automatic update (PKG-10, ADR-0073): a version
 * on offer, the download, the install, and how a check, a download or an install
 * that the person started ended. Main decides which one shows (the state's
 * dialog); this only draws it and sends back the choice. Closing a dialog
 * (Esc, ×) is 以后再说: for an offer it forgets it, for the others it only
 * acknowledges. The install cannot be closed. Release notes are never shown:
 * they are text from the network, and the release page has them.
 */
export function UpdateDialogs({
  api,
  language,
}: {
  api: UpdateApi;
  language: Language;
}) {
  const { state, run } = useUpdateState(api);
  const t = (zh: string, en: string) => tx(language, zh, en);
  // Main opens a dialog by itself (the automatic check, a finished download),
  // and a dialog takes the focus, which a shortcut capture in the window
  // answers by committing the chord it has heard so far. So a dialog waits
  // for the capture to end (capture-hold.ts). Main keeps its state meanwhile.
  const capturing = useCaptureRunning();
  const kind: UpdateDialogKind =
    state.supported && !capturing ? state.dialog : "none";
  const first = useRef<HTMLButtonElement>(null);
  // A choice has focus when a dialog opens or changes into another. The
  // dialog's own effect, a child's, has run showModal by then. Main opens
  // the offer and the finished download by itself, while the person may be
  // typing in the window: the key they press next lands on the button that
  // has focus. So these two focus 以后再说, which does no harm, not 下载 (a
  // download of 135 MB over the network) or 重启并安装 (the mapping stops
  // and padmate quits); those are for a click, or a Tab and then Enter.
  useEffect(() => {
    if (kind !== "none") first.current?.focus();
  }, [kind]);

  const later = () => run(api.dismissUpdate("later"));
  const releasePage = (
    <button
      type="button"
      className="update-link"
      onClick={() => run(api.openReleasePage())}
    >
      {t("查看发布页", "View release page")}
    </button>
  );
  const version = state.version;
  const ok = (
    <button ref={first} type="button" className="btn-primary" onClick={later}>
      {t("确定", "OK")}
    </button>
  );

  // How something that the person started ended: the words, and the page of
  // the release to go on by hand.
  const failure = (text: string) => (
    <>
      <p className="dialog-text">{text}</p>
      {releasePage}
    </>
  );
  const done = (
    <>
      <span className="spacer" />
      {ok}
    </>
  );

  let title = "";
  let body: ReactNode = null;
  let footer: ReactNode = null;
  switch (kind) {
    case "none":
      break;
    case "available":
      title = t("有新版本", "A new version is available");
      body = (
        <>
          <p className="dialog-text">
            {t(
              `padmate ${version} 可以更新了。`,
              `padmate ${version} is ready to download.`,
            )}
          </p>
          {releasePage}
        </>
      );
      footer = (
        <>
          <span className="spacer" />
          <button
            type="button"
            className="btn-ghost"
            onClick={() => run(api.dismissUpdate("skip"))}
          >
            {t("跳过这个版本", "Skip this version")}
          </button>
          <button ref={first} type="button" className="btn" onClick={later}>
            {t("以后再说", "Later")}
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={() => run(api.downloadUpdate())}
          >
            {t("下载", "Download")}
          </button>
        </>
      );
      break;
    case "downloading":
      title = t("正在下载", "Downloading");
      body = (
        <>
          <p className="dialog-text">
            {t(
              `正在下载 padmate ${version}…`,
              `Downloading padmate ${version}…`,
            )}
          </p>
          <div className="update-progress">
            <progress
              max={100}
              value={state.percent}
              aria-label={t("下载进度", "Download progress")}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={state.percent}
            />
            <span className="update-percent">{state.percent}%</span>
          </div>
        </>
      );
      footer = (
        <>
          <span className="spacer" />
          <button ref={first} type="button" className="btn" onClick={later}>
            {t("隐藏", "Hide")}
          </button>
        </>
      );
      break;
    case "downloaded":
      title = t("下载完成", "Download complete");
      body = (
        <p className="dialog-text">
          {t(
            `padmate ${version} 已下载。安装时映射会停一会儿，装好后 padmate 会自动重新打开。`,
            `padmate ${version} is downloaded. Mapping stops for a moment during the install, and padmate opens again by itself when it is done.`,
          )}
        </p>
      );
      footer = (
        <>
          <span className="spacer" />
          <button ref={first} type="button" className="btn" onClick={later}>
            {t("以后再说", "Later")}
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={() => run(api.installUpdate())}
          >
            {t("重启并安装", "Restart and install")}
          </button>
        </>
      );
      break;
    case "installing":
      title = t("正在安装", "Installing");
      body = (
        <p className="dialog-text">
          {t(
            `padmate 正在重启并安装 ${version}…`,
            `padmate is restarting to install ${version}…`,
          )}
        </p>
      );
      break;
    case "upToDate":
      title = t("已是最新版本", "Up to date");
      body = (
        <p className="dialog-text">
          {t("已是最新版本。", "You have the latest version.")}
        </p>
      );
      footer = done;
      break;
    case "checkFailed":
      title = t("无法检查更新", "Could not check for updates");
      body = failure(
        t(
          "请检查网络连接后再试，或到发布页查看最新版本。",
          "Check your connection and try again, or look for the latest version on the release page.",
        ),
      );
      footer = done;
      break;
    case "downloadFailed":
      title = t("无法下载更新", "Could not download the update");
      body = failure(
        t(
          "下载没有完成，或文件没有通过校验。可以稍后再试，或到发布页手动下载。",
          "The download did not finish, or the file failed its check. Try again later, or download it from the release page.",
        ),
      );
      footer = done;
      break;
    case "installFailed":
      title = t("无法安装更新", "Could not install the update");
      body = failure(
        t(
          "安装程序没有启动，padmate 仍在运行。可以稍后再试，或到发布页手动下载。",
          "The installer did not start, and padmate is still running. Try again later, or download it from the release page.",
        ),
      );
      footer = done;
      break;
  }

  return (
    <Dialog
      open={kind !== "none"}
      title={title}
      closeLabel={t("关闭", "Close")}
      closeDisabled={kind === "installing"}
      onClose={later}
      className="confirm-dialog update-dialog"
      footer={footer}
    >
      {body}
    </Dialog>
  );
}
