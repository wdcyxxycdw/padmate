// The "添加软件" menu of the profiles page (UI-18, PRO-16, UI-46): pick a
// window, choose from the installed apps, or browse to an .exe. Nothing is
// typed in.
import type { Language } from "../store";
import { MenuButton, type MenuEntry } from "../ui/controls";
import { Icon } from "../ui/Icon";
import "./profiles.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export function AppPicker({
  language,
  onCapture,
  onInstalled,
  onBrowse,
  disabled = false,
  testId,
}: {
  language: Language;
  /** 点选窗口…: the next app the owner clicks is bound. */
  onCapture: () => void;
  /** 从已安装的软件中选择…: opens the list of installed apps. */
  onInstalled: () => void;
  /** 浏览…: opens the file dialog for an .exe. */
  onBrowse: () => void;
  disabled?: boolean;
  testId?: string;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const items: MenuEntry[] = [
    {
      label: t("点选窗口…", "Pick a window…"),
      icon: "search",
      testId: "pick-window",
      onSelect: onCapture,
    },
    {
      label: t("从已安装的软件中选择…", "Choose from installed apps…"),
      icon: "list",
      testId: "pick-installed",
      onSelect: onInstalled,
    },
    {
      label: t("浏览…", "Browse…"),
      icon: "folder",
      testId: "browse-app",
      onSelect: onBrowse,
    },
  ];
  return (
    <span className="app-add-anchor">
      <MenuButton
        label={t("添加软件", "Add app")}
        className="app-add"
        align="start"
        disabled={disabled}
        testId={testId}
        items={items}
      >
        <Icon name="plus" size={13} stroke={2} />
        {t("添加软件", "Add app")}
      </MenuButton>
    </span>
  );
}
