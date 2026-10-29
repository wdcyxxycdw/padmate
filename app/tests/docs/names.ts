// The names of the app that the user documents quote (ENG-17, ENG-18).
//
// This table is outside src/renderer on purpose: the test reads every source
// file of the renderer for the strings of the app, and a table inside it would
// satisfy its own check. Change the text of a button and the test of the
// documents fails until the documents and this table say the same.
//
// Quote a name in a document exactly as the app shows it: “zh” in the Chinese
// document, “en” in the English one. A quoted name that is neither here nor in
// `external` fails the test, so a mistyped name is found.

/** Where the app has the name as a pair of strings. */
export type Source = "renderer" | "installer";

/** [Chinese, English, where the app has them (the renderer if left out)]. */
export type Name = readonly [zh: string, en: string, source?: Source];

export const names: Name[] = [
  // First run.
  ["连接手柄", "Connect your controller"],
  ["进入主页", "Go to the main page"],
  ["连接一个手柄", "Connect a controller"],
  ["不连接手柄，直接进入", "Continue without a controller"],
  // Pages and the status bar.
  ["映射", "Mapping"],
  ["配置", "Profiles"],
  ["脚本", "Scripts"],
  ["设置", "Settings"],
  ["运行中", "Running"],
  ["已暂停", "Paused"],
  ["系统暂停", "System paused"],
  ["暂停映射", "Pause mapping"],
  ["恢复映射", "Resume mapping"],
  // The first shortcut.
  ["未设置", "Not set"],
  ["按下", "Press"],
  ["录入快捷键", "Capture shortcut"],
  ["请按下快捷键…", "Press a shortcut…"],
  ["清除快捷键", "Clear shortcut"],
  ["添加映射", "Add mapping"],
  ["添加下一步", "Add a next step"],
  ["快捷键", "Shortcut"],
  ["等待", "Wait"],
  // The function key.
  ["平时", "Normal"],
  ["功能键", "Function key"],
  ["按住功能键", "Hold function key"],
  ["打开 Windows 设置", "Open Windows Settings"],
  ["知道了", "Got it"],
  ["按键操作", "Key actions"],
  ["清除这一套里的设置", "Clear in this set"],
  // A profile for a painting app.
  ["新建配置…", "New profile…"],
  ["名称", "Name"],
  ["新建", "Create"],
  ["更多", "More"],
  ["导出快捷键对照表…", "Export shortcut table…"],
  ["图片（PNG）", "Image (PNG)"],
  ["导出", "Export"],
  ["编辑按键", "Edit keys"],
  ["添加软件", "Add app"],
  ["点选窗口…", "Pick a window…"],
  ["从已安装的软件中选择…", "Choose from installed apps…"],
  ["浏览…", "Browse…"],
  ["按软件自动切换配置", "Switch profiles by app"],
  // Safety and settings.
  ["松开所有按键", "Release all keys"],
  ["安全", "Safety"],
  ["紧急暂停", "Emergency pause"],
  ["修改", "Change"],
  ["立即松开", "Release now"],
  ["手柄调校", "Controller calibration"],
  ["调校", "Calibrate"],
  ["识别", "Identify"],
  ["死区", "Dead zone"],
  ["推到底", "Full push"],
  ["通用", "General"],
  ["语言", "Language"],
  ["登录时启动", "Start at sign-in"],
  ["高级", "Advanced"],
  ["按键间隔", "Key gap"],
  ["关于", "About"],
  ["日志", "Logs"],
  ["查看", "View"],
  // Start-up trouble.
  ["设置已恢复", "Settings reset"],
  ["打开文件夹", "Open folder"],
  ["确定", "OK"],
  // Guide 1: the controller picture and the key drawer
  ["由脚本接管", "Handled by a script"],
  ["按键名称显示", "Key labels"],
  ["动作名称", "Action names"],
  ["快捷键", "Shortcuts"],
  ["重命名此按键", "Rename this key"],
  ["功能", "Function"],
  ["映射", "Mappings"],
  ["摇杆设置", "Stick settings"],
  ["扳机设置", "Trigger settings"],
  ["十字键设置", "D-pad settings"],
  ["关闭按键编辑", "Close key editor"],
  ["复制此按键的设置", "Copy this key's settings"],
  ["粘贴…", "Paste…"],
  ["重命名按键…", "Rename key…"],
  ["清空此按键…", "Clear this key…"],
  ["查看所有按键", "List all keys"],
  // Guide 2: gestures
  ["反馈", "Feedback"],
  ["震动", "Rumble"],
  ["点按", "Tap"],
  ["长按", "Hold"],
  ["按键判定", "Key timing"],
  ["松开", "Release"],
  ["点按时长", "Tap duration"],
  ["点按间隔", "Tap gap"],
  ["触发长按时间", "Long-press delay"],
  ["双击", "Double tap"],
  ["删除此映射", "Remove this mapping"],
  ["动作类型", "Action type"],
  ["鼠标", "Mouse"],
  ["脚本", "Script"],
  ["连击间隔", "Multi-tap window"],
  ["配置设置…", "Profile settings…"],
  ["判定长按前按下其他按键时", "When another key is pressed before a hold"],
  ["自动", "Automatic"],
  ["偏向长按", "Prefer hold"],
  ["平衡", "Balanced"],
  ["仅按时间", "Time only"],
  ["锁存", "Latch"],
  ["触感", "Haptic"],
  ["轻点", "Tick"],
  ["确认", "Confirm"],
  ["错误", "Error"],
  ["1 下", "1 pulse"],
  ["4 下", "4 pulses"],
  ["映射扫描码", "Map scan code"],
  // Guide 4: sequences
  ["响应曲线", "Response curve"],
  ["按顺序发送", "Sent in order"],
  // Guide 6: profiles, switching by app and inheritance
  ["默认", "Default"],
  ["导入…", "Import…"],
  ["未绑定的软件使用此配置", "Used by apps without a binding"],
  ["使用中", "In use"],
  ["复制", "Duplicate"],
  ["导出…", "Export…"],
  ["重命名…", "Rename…"],
  ["删除…", "Delete…"],
  ["新建配置", "New profile"],
  ["配置文件有错误", "The profile file has errors"],
  ["停用", "Turn off"],
  ["启用", "Turn on"],
  ["解除绑定", "Unbind"],
  ["保留", "Keep it"],
  ["更改", "Change"],
  ["替换", "Replace"],
  ["另存为", "Save as new"],
  ["都保留", "Keep both"],
  // Guide 7: sticks and triggers
  ["旋钮", "Knob"],
  ["轻推", "Light push"],
  ["重推", "Hard push"],
  ["方向切换延迟", "Direction delay"],
  ["朝任意方向推动", "Push in any direction"],
  ["按到底", "Full pull"],
  ["方向模式", "Direction mode"],
  ["4 方向", "4-way"],
  ["8 方向", "8-way"],
  ["只取 1 个方向", "One at a time"],
  ["方向键", "Arrow keys"],
  ["灵敏度", "Sensitivity"],
  ["本手柄已单独调校", "Calibrated for this controller"],
  ["按下摇杆", "Click the stick"],
  ["触发幅度", "Trigger point"],
  ["斜向扇区宽度", "Diagonal width"],
  ["角度滞回", "Angle hysteresis"],
  ["线性", "Linear"],
  ["二次", "Quadratic"],
  ["三次", "Cubic"],
  ["斜向未设置按键时", "When a diagonal has no keys"],
  ["不发送", "Send nothing"],
  ["按相邻两个方向", "Both neighbors"],
  ["只有斜向", "Diagonals only"],
  ["其他输入", "Other inputs"],
  ["重推的位置", "Hard push from"],
  ["原始轴", "Raw axes"],
  ["原始轴阈值", "Raw axis threshold"],
  ["参数操作", "Parameter actions"],
  ["复制参数", "Copy parameters"],
  ["粘贴参数…", "Paste parameters…"],
  ["恢复默认参数…", "Reset parameters…"],
  ["每格角度", "Step"],
  ["刻度震动", "Tick haptic"],
  ["顺时针一格", "Clockwise step"],
  ["逆时针一格", "Counter-clockwise step"],
  ["速度", "Speed"],
  ["平滑", "Smoothing"],
  ["实时力度", "Live pressure"],
  ["两段扳机", "Two-stage trigger"],
  ["按到底：按下", "Full pull: press"],
  ["按到底：松开", "Full pull: release"],
  ["半按", "Half press"],
  ["十字键模式", "D-pad mode"],
  ["独立按钮", "Buttons"],
  ["斜按时", "On a diagonal"],
  ["保留先按下的", "Keep the first"],
  ["按固定规则", "Fixed rule"],
  ["斜向的按键", "Keys for the diagonals"],
  // Guide 8: scripts
  ["重新加载", "Reload"],
  ["新建", "New"],
  ["导入", "Import"],
  ["未使用", "Not used"],
  ["编辑", "Edit"],
  ["重命名", "Rename"],
  ["删除", "Delete"],
  ["出错", "Error"],
  ["随配置持续运行", "Runs with the profile"],
  ["移除", "Remove"],
  ["选择脚本", "Choose a script"],
  ["暂无脚本", "No scripts yet"],
  ["前往脚本页", "Go to Scripts"],
  ["此脚本有错误", "This script has an error"],
  // Guide 9: diagnostics
  ["诊断", "Diagnostics"],
  ["事件", "Events"],
  ["延迟", "Latency"],
  ["冲突", "Conflicts"],
  ["共用", "Shared"],
  ["只预览", "Preview only"],
  ["筛选按键", "Filter by key"],
  ["只看没有动作的", "Only presses with no action"],
  ["暂停记录", "Pause recording"],
  ["继续记录", "Resume recording"],
  ["清空", "Clear"],
  [
    "当前配置里这个键没有设置动作",
    "This key has no action in the current profile.",
  ],
  [
    "按下时没有动作，要等松开、长按或连按后才能确定",
    "Nothing happens on press; the action waits for release, hold or repeated taps.",
  ],
  ["这个键只有名称，没有设置动作", "This key has a name but no action."],
  ["这个映射没有设置动作", "No action is set for this mapping."],
  ["映射已暂停", "Mapping is paused."],
  [
    "系统暂停中（锁屏、睡眠或安全桌面）",
    "The system has suspended mapping (lock screen, sleep or secure desktop).",
  ],
  ["正在录入快捷键，映射暂停", "Recording a shortcut; mapping is paused."],
  ["这个键在当前层被屏蔽", "This key is blocked in the current layer."],
  ["脚本处理了这个键", "A script handled this key."],
  ["这只手柄被设为忽略", "This controller is set to be ignored."],
  ["这只手柄没有标准映射", "This controller has no standard mapping."],
  ["最大", "Max"],
  [
    "没有检测到可能占用手柄的软件。",
    "No software that may hold the controller was found.",
  ],
  // Guide 10: importing from AntiMicroX
  ["打开配置", "Open profile"],
  ["丢弃", "Dropped"],
  ["近似", "Approximate"],
  ["信息", "Info"],
  // Guide 11: the mapping wizard
  ["完成", "Finish"],
  ["未识别为手柄", "Not recognised as a gamepad"],
  ["建立映射…", "Set up mapping…"],
  ["恢复默认映射", "Restore default mapping"],
  ["按下 A 键", "Press A"],
  ["保存", "Save"],
  ["映射已保存", "Mapping saved"],
  ["完成", "Done"],
  ["上一步", "Back"],
  ["重做", "Redo"],
  ["跳过", "Skip"],
  ["取消", "Cancel"],
  ["重新映射…", "Remap…"],
  ["重新映射", "Remap"],
  ["自定义映射", "Custom mapping"],
  ["恢复默认映射？", "Restore the default mapping?"],
  // Guide 12: tray and emergency release
  ["捕获中", "Capturing"],
  ["暂停提示", "Pause alert"],
  ["修改紧急暂停", "Change emergency pause"],
  ["按住时间", "Hold for"],
  ["恢复默认", "Restore default"],
  // Guide 13: updates and privacy
  ["检查更新", "Check for updates"],
  ["更新", "Updates"],
  ["自动检查更新", "Check for updates automatically"],
  ["下载", "Download"],
  ["重启并安装", "Restart and install"],
  ["无法检查更新", "Could not check for updates"],
  ["手动检查", "Manual check"],
  ["有新版本", "A new version is available"],
  ["查看发布页", "View release page"],
  ["隐藏", "Hide"],
  ["以后再说", "Later"],
  ["跳过这个版本", "Skip this version"],
  ["下载完成", "Download complete"],
  ["已是最新版本", "Up to date"],
  ["无法下载更新", "Could not download the update"],
  ["无法安装更新", "Could not install the update"],
  // Known limitations
  ["灯光", "Lights"],
  ["改用按到底", "Use full pull"],
  ["最近使用", "Recent"],
  // Quick start
  ["该手柄无法震动或闪灯", "This controller cannot rumble or flash."],
  // The question of the uninstaller (app/build/installer.nsh).
  ["是否保留配置和脚本？", "Keep your profiles and scripts?", "installer"],
];

/** The tray menu of padmated: Chinese only, quoted as it is in both languages. */
export const trayNames = ["暂停映射", "恢复映射", "登录时启动", "退出 padmate"];

/**
 * Quoted text that is not a name of the app's own strings: text of Windows or
 * of the installer, a name the user gives, a product, a file type. `en` is the
 * same as `zh` when the text is not translated. Every entry says why.
 */
export interface External {
  zh: string;
  en: string;
  reason: string;
  /**
   * The Chinese document that has a heading with this name. The English twin
   * has one with `en`. Set for a name that a document quotes to send the
   * reader to a section: renaming the section must fail the test.
   */
  heading?: string;
}

export const external: External[] = [
  {
    zh: "绘画",
    en: "Painting",
    reason:
      "the example name of a profile that the reader types; the app proposes no name",
  },
  {
    zh: "Windows 已保护你的电脑",
    en: "Windows protected your PC",
    reason: "text of the SmartScreen window of Windows",
  },
  {
    zh: "更多信息",
    en: "More info",
    reason: "link of the SmartScreen window of Windows",
  },
  {
    zh: "仍要运行",
    en: "Run anyway",
    reason: "button of the SmartScreen window of Windows",
  },
  {
    zh: "安装",
    en: "Install",
    reason: "button of the installer wizard (NSIS), not text of the app",
  },
  {
    zh: "运行 padmate",
    en: "Run padmate",
    reason: "check box of the last page of the installer wizard (NSIS)",
  },
  {
    zh: "已连接：手柄名称",
    en: "Connected:",
    reason: "the app writes the name of the controller after the colon",
  },
  {
    zh: "应用",
    en: "Installed apps",
    reason: "page of the Windows Settings, where an app is uninstalled",
  },
  {
    zh: "是",
    en: "Yes",
    reason: "button of the question of the uninstaller (NSIS message box)",
  },
  {
    zh: "否",
    en: "No",
    reason: "button of the question of the uninstaller (NSIS message box)",
  },
  {
    zh: "^",
    en: "^",
    reason:
      "the button of the taskbar that shows the hidden tray icons (Windows)",
  },
  {
    zh: "Windows 安全中心",
    en: "Windows Security",
    reason: "app of Windows",
  },
  {
    zh: "病毒和威胁防护",
    en: "Virus & threat protection",
    reason: "page of Windows Security",
  },
  {
    zh: "保护历史记录",
    en: "Protection history",
    reason: "page of Windows Security",
  },
  {
    zh: "请勿打扰",
    en: "Do not disturb",
    reason: "setting of Windows that hides notifications",
  },
  {
    zh: "设置 → 配置 Krita → 常规 → 杂项",
    en: "Settings → Configure Krita → General → Miscellaneous",
    reason: "menu path in Krita, not in padmate",
  },
  {
    zh: "忽略 F13–F24",
    en: "Ignore high function keys",
    reason: "option in Krita, not in padmate",
  },
  {
    zh: "Markdown",
    en: "Markdown",
    reason: "name of a file format, the same in both languages",
  },
  {
    zh: "WASD",
    en: "WASD",
    reason: "text on a button of the app: the four letter keys, not translated",
  },
  {
    zh: "设置 → 游戏 → Game Bar",
    en: "Settings → Gaming → Game Bar",
    reason: "menu path in the Settings of Windows, not in padmate",
  },
  {
    zh: "允许控制器打开 Game Bar",
    en: "Allow your controller to open Game Bar",
    reason: "switch on the Game Bar page of the Settings of Windows 11",
  },
  {
    zh: "使用控制器上的此按钮打开 Xbox Game Bar",
    en: "Open Xbox Game Bar using this button on a controller",
    reason: "the same switch, as older versions of Windows name it",
  },
  {
    zh: "平时：…",
    en: "Normal: …",
    reason: "the start of a line that the app builds; the shortcut follows",
  },
  {
    zh: "平时 / 按住功能键",
    en: "Normal / Hold function key",
    reason:
      "the two choices of the switch above the Mapping page, written together",
  },
  {
    zh: "第 1 步",
    en: "Step 1",
    reason: "built by the app from the number of the step",
  },
  {
    zh: "第 2 步",
    en: "Step 2",
    reason: "built by the app from the number of the step",
  },
  {
    zh: "按钮 3",
    en: "Button 3",
    reason:
      "built by the wizard from the number of the button, here as an example",
  },
  {
    zh: "检测到：按钮 3",
    en: "Detected: Button 3",
    reason: "built by the wizard from what it found, here as an example",
  },
  {
    zh: "等了 N ms",
    en: "Waited N ms",
    reason: "built by the app, N is the number of milliseconds",
  },
  {
    zh: "Steam 正在运行",
    en: "Steam is running",
    reason: "built by the app with the name of the software that runs",
  },
  {
    zh: "出了问题怎么办",
    en: "When something goes wrong",
    reason: "heading of a section of the quick start",
    heading: "docs/quickstart.md",
  },
  {
    zh: "绘画软件",
    en: "Painting apps",
    reason: "heading of a section of the known limitations page",
    heading: "docs/known-limitations.md",
  },
  {
    zh: "杀毒软件和 SmartScreen",
    en: "Antivirus programs and SmartScreen",
    reason: "heading of a section of the known limitations page",
    heading: "docs/known-limitations.md",
  },
];

/**
 * Text that stays Chinese in the English documents, outside the quoted names
 * above: the tray menu (`trayNames`) is always allowed. Every entry says why.
 */
export const chineseInEnglish: { text: string; reason: string }[] = [
  {
    text: "背键 1",
    reason:
      "the Mapping page prints the back buttons of most controllers in Chinese in both languages (controller-model.ts), and the page quotes it as the page shows it",
  },
];

/**
 * Inline code that is translated in the English document: a placeholder for
 * what the reader types. Both have to be spans of the documents. Other inline
 * code has to be the same in both languages.
 */
export const codePlaceholders: { zh: string; en: string }[] = [
  {
    zh: String.raw`Get-FileHash .\安装程序.exe -Algorithm SHA256`,
    en: String.raw`Get-FileHash .\installer.exe -Algorithm SHA256`,
  },
  {
    zh: "padmatectl profile import-amgp 文件",
    en: "padmatectl profile import-amgp file",
  },
  {
    zh: String.raw`.\padmatectl.exe profile import-amgp 文件`,
    en: String.raw`.\padmatectl.exe profile import-amgp file`,
  },
];
