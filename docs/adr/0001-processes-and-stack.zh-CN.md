# ADR-0001：进程划分与技术栈

[English](0001-processes-and-stack.md)

相关：ENG-02、UI-01、UI-45、DMN-04、DMN-14、DMN-22；[ARCHITECTURE §2](../ARCHITECTURE.zh-CN.md#2-进程模型)、[§12](../ARCHITECTURE.zh-CN.md#12-electron)

## 背景

常驻的映射（读手柄、引擎、SendInput、脚本、托盘、IPC）要一直开着，占用少、延迟低、不卡键。编辑和提示（可视化编辑、Monaco、overlay）按需打开。界面是可选组件，关掉它映射照常工作。项目只有一个开发者加 AI 助手，工具链要少。

## 决定

1. **两个进程，两门语言。** 守护进程 `padmated`、CLI `padmatectl`、开发工具 `padmate-probe` 用 Go；界面和 overlay（`padmate.exe`）用 Electron + TypeScript。两边只经 IPC 通信（[ADR：IPC](0009-ipc.zh-CN.md)）。脚本在守护进程内运行（goja + esbuild Go API），不另起运行时。两侧共用的类型由 schema 生成。
2. **守护进程常驻，托盘不依赖 Electron。** 托盘“退出”先暂停映射并释放所有键，再发布 `daemon.exiting`，等界面确认（`ui.exitReady`）最多 2 s，再等 supervisor 和 T-output 完成释放与重试。清理超时记录失败、退出码 1，不缩短释放阶段。
3. **单实例，第二次启动只转发。** 命名 mutex 按用户 SID 和会话分区。第二次启动不解析本地配置、不开日志和 SDL，经 `instance.forward` 把请求交给运行中的实例。与之冲突的启动设置（输出模式、数据目录、输入后端）直接拒绝，不隐式改模式。转发失败保留原配置。
4. **界面的安全桥。** Renderer 开 sandbox 和 contextIsolation，关 nodeIntegration；页面由带 CSP 的 `padmate://app` 提供；preload 只暴露命名方法，Main 再校验。Main 通过子进程 `padmatectl ui-bridge`（有界 NDJSON、方法白名单）访问 IPC，token 不进 Renderer 和日志。
5. **padmate 自己的窗口在前台时只演练、不发键。** 按已验证的进程实例判断，不把所有 electron.exe 当作自己。
6. **启动顺序。** 界面先连现有守护进程，连不上才以 detached、无控制台的方式启动，关界面不终止它。打包版界面用 `--inject`，开发版、E2E 和测试用 `--dry-run`（[ADR：不卡键](0006-no-stuck-keys.zh-CN.md)）。
7. **启动失败要说清楚（UI-45）。** 打包版 padmated 没有 stderr，所以用 `--startup-report FILE` 写一个 JSON 报告并以退出码 3 退出；Main 读报告（限大小，只留认得的字段）并停止重试。界面只显示失败页：原因、路径、“打开文件夹”“重试”“退出”。config.json 本身有错不算启动失败，由守护进程备份并修复（[ADR：配置库](0008-profile-library.zh-CN.md)）。
8. **关窗只隐藏。** 守护进程连着、页面就绪时，关窗只隐藏窗口，页面、手柄舞台和 overlay 都留着，再打开显示同一个窗口。真正的退出路径（托盘退出、安装程序退出、更新重启）先等保存完成。没有连接或启动失败时，关窗直接退出。

## 代价

- 两套工具链，进程间契约（IPC `protocol`、生成的类型）要保持同步。
- 界面多一个 CLI 桥进程，要求同版本的 `padmatectl`；换来复用已验证的管道身份检查，不引入 Node 原生扩展。
- 打包版会真实发键，开发和 E2E 必须用开发版。
- 关窗后界面进程和已上传的纹理一直占着内存，直到退出 padmate。
- 托盘退出最多多等 2 s。

## 放弃的做法

- **全部用 Electron/Node。** 映射会依赖界面进程，违背“可选”；SDL 和 SendInput 要写原生模块。
- **Rust + Tauri、C#/WPF、Qt。** 脚本宿主要另选 JS 引擎和 TS 编译方案，Monaco 仍要嵌网页；overlay 的窗口行为已在 Electron 上验证。
- **界面读 padmated 的 stderr，或报告写在固定位置，或经 IPC 报告。** 打包版没有 stderr；固定位置会读到别次启动的报告；实例、配置、日志和 SDL 都在 IPC 之前启动。
- **管道一关就退出界面。** 分不出托盘退出和崩溃。
- **托盘退出不等界面，或等界面不设上限。** 前者丢最后一次修改，后者在界面卡住时托盘也卡住。
- **打包版仍 dry-run，自启时改登记 `--inject`。** 试用和登录后的模式不同。
- **启动时问用户要不要发键。** 多一步，而大多数人装它就是为了发键。
