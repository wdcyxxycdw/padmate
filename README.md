# padmate

[English](README.en.md)

padmate 把游戏手柄变成键盘快捷键控制台。画画时左手握手柄，按键、摇杆和扳机发出你在绘画软件里常用的快捷键。

适用于 Windows 10 和 11（x64）。MIT 许可。

## 功能

- 每个键可以分别设点按、长按、双击，还有按顺序发送的序列。
- 摇杆和扳机可以当方向键、旋钮或鼠标用，扳机能分两段。
- 按住功能键（默认是 Menu，可以在设置里换成 View 或 Xbox 键）时，其他键换成另一套快捷键。
- 每个软件一个配置，按前台的软件自动切换。
- 用 TypeScript 写脚本（宏），处理更复杂的操作。
- 诊断页列出每次按键发了什么；没反应时写明原因。
- 可以导入 AntiMicroX 的配置；SDL 不认识的手柄，可以用映射向导建一份映射。
- 不卡键：退出、暂停、锁屏、手柄断开时，按着的键都会松开。

## 安装

1. 从 [Releases](https://github.com/wdcyxxycdw/padmate/releases) 下载最新的 `padmate-vX.Y.Z-windows-amd64-setup.exe`。
2. 双击运行。按当前用户安装，不需要管理员权限。
3. 安装包没有代码签名。Windows 提示“Windows 已保护你的电脑”时，点“更多信息”，再点“仍要运行”。

升级时运行新的安装包，配置和设置都会保留。

## 使用

1. 连上手柄，打开 padmate。
2. 在“映射”页的手柄图上点一个键。
3. 在打开的抽屉里点“录入快捷键”，按下你在绘画软件里用的快捷键。
4. 切到绘画软件，按手柄上的这个键。
5. 想给某个软件单独一套键：在“配置”页点“新建配置”，再点“添加软件”。

紧急暂停：同时按住 View 和 Menu 2 秒，所有映射暂停，按着的键全部松开；再按住 2 秒恢复。

界面默认是中文。要改成英文，打开“设置”，在“通用”里改“语言”。

## 用户指南

用户指南随安装包提供，在安装目录的 `resources\docs\guide` 文件夹里，从 `00-index.md` 读起，英文版是 `00-index.en.md`。安装目录默认是 `%LOCALAPPDATA%\Programs\padmate`。

## 隐私

padmate 只在检查更新时联网，向 GitHub 请求发布信息，不发送别的数据。在“设置”里关掉“自动检查更新”后，只有点“检查更新”才会联网。

## 从源码构建

需要 Go 1.27（发布版用它构建，`daemon/go.mod` 的最低要求是 1.26）、Node 24、[Task](https://taskfile.dev) 和 golangci-lint v2.13（`task ci` 的检查用它）。Task 和 golangci-lint 用 Go 安装；装好后确认 `%USERPROFILE%\go\bin` 在 PATH 里：

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

然后：

```bash
task sdl
task app:install
task ci
task build
```

`task sdl` 下载固定版本的 SDL3.dll，`task app:install` 安装界面的依赖，`task ci` 运行全部检查，`task build` 把 padmated、padmatectl 和 padmate-probe 构建到 `bin/`。

这个仓库只有代码。安装包由另一个开发仓库构建，所以这里的打包任务不可用，请从 Releases 下载；检查用户文档的测试在这里会跳过。

## 许可证

[MIT](LICENSE)。本项目不包含 AntiMicroX（GPL-3.0）的代码。
