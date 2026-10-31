# padmate

[English](README.en.md)

padmate 是一款能将常用游戏手柄映射到键盘快捷键的软件。支持自定义功能键、摇杆旋钮以及高级脚本功能，支持 Xbox、PlayStation 和 Joy-Con 手柄。可用于绘画，剪辑，编程等需要大量快捷键使用的场景。

**让闲置手柄变成生产力。**

适用于 Windows 10 和 11（x64）。

## 主要功能

- **按键映射**：每个按键可分别设置点按、长按和双击动作的映射。支持键盘，鼠标以及简单的宏设置。
- **功能键**：设定功能键+普通按键的组合，让手柄拥有更多的快捷键。
- **摇杆与扳机**：摇杆可用作旋钮或鼠标；扳机可作为按键使用，并支持两段触发。
- **按软件切换配置**：可为每个软件建立独立的配置，随前台软件自动切换。
- **脚本**：可使用 TypeScript 或 JavaScript 编写脚本，实现更复杂的操作。
- **兼容 AntiMicroX**：可导入 AntiMicroX 的配置。

## 安装

1. 点击 GitHub [Release](https://github.com/wdcyxxycdw/padmate/releases) 页面下载最新版本。或访问专用下载站 [padmate.download](https://padmate.download/)
2. 点击下载好的安装包，运行。安装包目前没有代码签名。若被Windows弹窗阻止安装，请先点击“更多信息”，再点击“仍要运行”即可继续。

## 使用指南

使用指南随安装包提供，在安装目录下的 `resources\docs\guide` 文件夹里，从 `00-index.md` 开始阅读（英文版为 `00-index.en.md`）。默认安装目录为 `%LOCALAPPDATA%\Programs\padmate`。

## ToDo

- [x] 摇杆旋钮、方向键与鼠标模式
- [x] 自动切换配置
- [x] 自定义脚本
- [x] 兼容 AntiMicroX 配置
- [ ] 支持 Switch 2 的 Joy-Con
- [ ] 支持 macOS 和 Linux
- [ ] 为更多软件提供预设配置

## 隐私

padmate 仅在检查和下载更新时联网，不会获取任何用户信息。支持在无网络状态下的使用。

## 从源码构建

需要 Go 1.27、Node 24、[Task](https://taskfile.dev) 和 golangci-lint v2.13。Task 和 golangci-lint 可以用 Go 安装，装好后确认 `%USERPROFILE%\go\bin` 在 PATH 里：

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

然后运行：

```bash
task sdl
task app:install
task ci
task build
```

本仓库只包含代码，不能构建安装包。安装包请从 GitHub Release 或 padmate.download 下载。

## 许可证

本项目以 [MIT](LICENSE) 许可证发布。
