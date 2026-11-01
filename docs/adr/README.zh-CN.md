# 架构决策记录（ADR）

[English](README.md)

这里回答“padmate 为什么是现在这样”。读者是要改设计、或想弄清某个约束来历的开发者。

几类文档的分工：
- [ARCHITECTURE](../ARCHITECTURE.zh-CN.md)：现在的设计是什么样，代码在哪。
- [PITFALLS](../PITFALLS.zh-CN.md)：改某块代码前要知道的已验证事实。
- ADR（本目录）：为什么这样设计，放弃了什么，付出什么代价。

## 目录
| 文档 | 讲什么 |
|---|---|
| [进程划分与技术栈](0001-processes-and-stack.zh-CN.md) | Go 守护进程加可选的 Electron 界面；常驻、托盘、单实例与转发；界面的生命周期与安全桥 |
| [手柄输入：SDL3](0002-input-sdl.zh-CN.md) | 自带固定版本的 SDL3、hint 组合、go-sdl3 与 purego 补丁、Joy-Con、接收器心跳 |
| [多手柄、热插拔与 slot](0003-devices-and-slots.zh-CN.md) | 最多 4 个手柄同时映射；热插拔、重连与排队 |
| [键盘与鼠标输出](0004-output-sendinput.zh-CN.md) | SendInput 扫描码与键盘布局、注入标记、菜单屏蔽键、按键间隔、鼠标输出 |
| [引擎、slot loop 与运行时监督](0005-engine-and-threads.zh-CN.md) | 纯函数引擎、slot loop、T-output 循环、运行时监督、端到端延迟 |
| [不卡键](0006-no-stuck-keys.zh-CN.md) | 显式演练/注入、暂停与紧急组合、系统事件挂起、held-keys 日志、离线紧急释放 |
| [配置文件格式](0007-profile-format.zh-CN.md) | JSON 与唯一写者、标签联合与 schemaVersion、JSON Schema 校验、事件 JSON 行、热重载 |
| [配置库、默认配置与自动切换](0008-profile-library.zh-CN.md) | 配置库、固定默认配置、按软件自动切换、自动保存、撤销与备份 |
| [IPC](0009-ipc.zh-CN.md) | 命名管道上的 JSON-RPC、身份验证、握手与有界连接 |
| [脚本运行时](0010-scripting-runtime.zh-CN.md) | goja + esbuild、自研事件循环、与声明式映射共用手势和输出、API 版本 |
| [脚本即宏](0011-script-macros.zh-CN.md) | 脚本是按键触发的宏；宏库；文件只由守护进程写 |
| [映射模型](0012-mapping-model.zh-CN.md) | 一个配置一组按键、唯一功能键、模拟量控件、序列与继承、连发、映射向导、AntiMicroX 导入 |
| [编辑界面](0013-editor-ui.zh-CN.md) | 手柄图与按键抽屉、编辑草稿与快捷键捕获、自动保存、快捷键键池、按键符号与图标 |
| [Overlay 窗口](0014-overlay.zh-CN.md) | 只建一次、不抢焦点、鼠标穿透的浮层窗口 |
| [手柄反馈](0015-controller-feedback.zh-CN.md) | 震动、灯光、触感预设、电量与全局开关 |
| [可解释映射与诊断](0016-diagnostics.zh-CN.md) | 事件记录与“为什么没触发” |
| [全局设置与登录自启](0017-settings-and-startup.zh-CN.md) | 全局设置保存即生效、登录自启开关与长参数引用 |
| [安装、更新与发布](0018-installer-and-updates.zh-CN.md) | 按用户的 NSIS 向导式安装包、便携包、签名、自动更新与下载站 |

## 什么时候改 ADR
需要改：
- 改变或新增 [ARCHITECTURE §0 决策一览](../ARCHITECTURE.zh-CN.md#0-决策一览) 里的一行。
- 选择语言、关键依赖、线程模型、进程间协议或文件格式。
- 决定会影响 [CLAUDE.md](../../CLAUDE.md) 的不可破坏的不变量。
- spike 得出结论。

不需要改：修 bug、调整包内部的实现、按已有决定实现功能。这些只更新 ARCHITECTURE。

## 规则
- 每篇讲一个主题，只写现在有效的决定。决定变了，就在同一个提交里直接改那一篇，并同步 ARCHITECTURE；旧的写法留在 git 历史里，不另写一篇“取代”它。
- 新主题才新建一篇，编号接着最大的往下排，不复用。文件名是 `NNNN-英文短名.md`（英文），中文版是同名的 `NNNN-英文短名.zh-CN.md`；标题是 `# ADR-NNNN: 主题`；同时在两种语言的目录表里加一行。代码注释用 `ADR-NNNN` 引用。
- 每篇的结构：背景、决定、代价、放弃的做法。不写日期、谁提的、验收经过。
- 中英两份同时写，句子要短。每条决定都要能在代码或 ARCHITECTURE 里找到对应。
