import type { Language } from "../store";
import { Switch } from "../ui/controls";
import { type UpdateApi, useUpdateState } from "../update-model";
import "./pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Settings · 更新 (PKG-10, ADR-0073): 自动检查更新 and 检查更新. The switch is
 * the window's own setting (Main keeps it in updates.json), so it works
 * without the mapping service and is not gated on config.json. The note beside
 * it says what GitHub learns, as this is the first thing padmate sends over
 * the network.
 *
 * What a check finds is told by the dialogs (UpdateDialogs), not here: this
 * only starts one, and says in a line what is already on its way.
 */
export function UpdatesSection({
  api,
  language,
}: {
  api: UpdateApi;
  language: Language;
}) {
  const { state, run } = useUpdateState(api);
  const t = (zh: string, en: string) => tx(language, zh, en);
  const checking = state.phase === "checking";
  const busy = checking || state.phase === "installing";
  let status = "";
  if (!state.supported)
    status = t(
      "开发版不检查更新",
      "Development builds do not check for updates",
    );
  else if (state.phase === "downloading")
    status = t(
      `正在下载 ${state.version}… ${state.percent}%`,
      `Downloading ${state.version}… ${state.percent}%`,
    );
  else if (state.phase === "downloaded")
    status = t(
      `${state.version} 已下载，尚未安装`,
      `${state.version} is downloaded and not installed yet`,
    );
  return (
    <section className="page-section" data-testid="update-settings">
      <h2 className="section-title">{t("更新", "Updates")}</h2>
      <div className="card">
        <div className="row">
          <div className="row-text">
            <span className="row-title">
              {t("自动检查更新", "Check for updates automatically")}
            </span>
            <span className="row-hint">
              {t(
                "每天向 GitHub 查询一次新版本。GitHub 会看到你的 IP 地址和 padmate 的版本号，padmate 不发送其他数据。",
                "padmate asks GitHub for a new version once a day. GitHub sees your IP address and padmate's version number; padmate sends nothing else.",
              )}
            </span>
          </div>
          <Switch
            label={t("自动检查更新", "Check for updates automatically")}
            checked={state.autoCheck}
            disabled={!state.supported}
            onChange={(enabled) => run(api.setAutoUpdate(enabled))}
          />
        </div>
        <div className="row">
          <div className="row-text">
            <span className="row-title">{t("手动检查", "Manual check")}</span>
            {status && (
              <span className="row-hint" role="status">
                {status}
              </span>
            )}
          </div>
          <button
            type="button"
            className="btn"
            disabled={!state.supported || busy}
            onClick={() => run(api.checkForUpdate())}
          >
            {checking
              ? t("正在检查…", "Checking…")
              : t("检查更新", "Check for updates")}
          </button>
        </div>
      </div>
    </section>
  );
}
