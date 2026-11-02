# padmate 架构

[English](ARCHITECTURE.md)

本文给接手的开发者看：进程、线程、包结构、数据流、文件格式、IPC、界面和测试，以及它们之间的约束。为什么这样设计，见 [ADR](adr/README.zh-CN.md)；改某块代码前要知道的已验证事实见 [PITFALLS](PITFALLS.zh-CN.md)。

## 0. 决策一览

理由和被放弃的做法见 [ADR 索引](adr/README.zh-CN.md)。

| 领域 | 决定 | ADR |
|---|---|---|
| 平台与进程 | 只支持 Windows 10/11 x64。Go 守护进程 `padmated` 负责全部映射，常驻托盘，单实例；Electron 界面可选，关掉不影响映射 | [进程与技术栈](adr/0001-processes-and-stack.zh-CN.md) |
| 输入 | 自带固定版本的 SDL 3.4.16（go-sdl3 + purego）；左右两只 Joy-Con 同时连着就是一个手柄；Joy-Con 2 直接经蓝牙 LE 读（winrt-go），不配对 | [手柄输入](adr/0002-input-sdl.zh-CN.md) |
| 多手柄 | 4 个预建 slot，其余手柄排队；新分配先暂代，按任意键确认 | [多手柄与 slot](adr/0003-devices-and-slots.zh-CN.md) |
| 输出 | SendInput 扫描码；事件带 `dwExtraInfo` 标记；只有 T-output 调 SendInput | [键盘与鼠标输出](adr/0004-output-sendinput.zh-CN.md) |
| 线程 | slot loop、T-output、T-input 各自锁定 OS 线程；引擎是纯函数 | [引擎与线程](adr/0005-engine-and-threads.zh-CN.md) |
| 不卡键 | 所有退出路径 ReleaseAll；必须显式选 `--dry-run` 或 `--inject`；暂停、挂起、紧急组合各有闸门；held-keys 日志和离线紧急释放兜底 | [不卡键](adr/0006-no-stuck-keys.zh-CN.md) |
| 配置 | 严格 JSON 标签联合，`schemaVersion` 为 1，守护进程是唯一写者；按 ID 存在配置库，按前台 exe 路径自动切换 | [配置文件格式](adr/0007-profile-format.zh-CN.md)、[配置库](adr/0008-profile-library.zh-CN.md) |
| 映射模型 | 一个配置一组按键；唯一功能键，每只手柄自己选；摇杆分旋转和点按；配置可继承 | [映射模型](adr/0012-mapping-model.zh-CN.md) |
| 手柄设置 | 功能键、反馈开关和紧急组合是每只手柄自己的设置（`devices.<DeviceKey>`），在设置页的“手柄设置”子页里列出和修改；设置保存即生效 | [手柄反馈](adr/0015-controller-feedback.zh-CN.md)、[设置与登录自启](adr/0017-settings-and-startup.zh-CN.md) |
| IPC | 命名管道 JSON-RPC 2.0，token 认证，ACL 限当前用户 | [IPC](adr/0009-ipc.zh-CN.md) |
| 脚本 | goja + esbuild；与声明式映射共用手势识别和输出 | [脚本运行时](adr/0010-scripting-runtime.zh-CN.md)、[脚本即宏](adr/0011-script-macros.zh-CN.md) |
| 界面与诊断 | Electron + React；overlay 是第二个窗口 | [编辑界面](adr/0013-editor-ui.zh-CN.md)、[Overlay](adr/0014-overlay.zh-CN.md)、[诊断](adr/0016-diagnostics.zh-CN.md) |
| 打包 | NSIS 按用户安装；另有便携 zip；更新读 `padmate.download`，它也有下载页 | [安装包与更新](adr/0018-installer-and-updates.zh-CN.md) |

## 1. 原则

1. **不卡键优先于一切。** 所有退出路径（panic、断连、切换、锁屏）都 ReleaseAll。panic 之后出问题的那一级不可信，跳过它，由下一级按 ledger 释放（§5.16）。
2. **引擎是纯函数。** 时间、输入、输出都经接口，可在 Linux 上确定性测试。
3. **输出只有一个写者。** 守护进程内只有 T-output 调 SendInput；`padmatectl send` 和注入测试在进程外直接调用。
4. **延迟只花在需要的地方**：手势等待只由可达的绑定决定。
5. **状态变化至少走两个渠道告知用户**：手柄、托盘、overlay。
6. **只输出键盘快捷键和鼠标**，不做应用专属兼容。

## 2. 进程模型

| 进程 | 角色 |
|---|---|
| `padmated.exe` | 守护进程：输入、引擎、输出、脚本、托盘、IPC。同时映射最多 4 个手柄 |
| `padmatectl.exe` | CLI；开发用 `send`；界面的认证 bridge（`ui-bridge`） |
| `padmate.exe` | 可选的 Electron 界面和 overlay |
| `padmate-probe.exe` | 开发工具：LL 钩子记录注入的键、导出布局 |

Electron Main 经子进程 `padmatectl ui-bridge` 连接 `daemon.json` 指向的守护进程；连不上时有界地尝试启动 `padmated`，失败则窗口说明原因（§12）。token 不进入 Renderer。

## 3. 目录结构

Go 模块放在 `daemon/`（源码 `cmd/`、`internal/`、`tools/`、`testdata/`）：根目录下 `app/node_modules` 里个别 npm 包带 `.go` 文件，会破坏根目录的 `go build ./...`。界面在 `app/`（electron-vite），schema 的唯一源头在 `schema/`，下载页和它的 Worker 在 `site/`（§15.5）。

用户数据：`%APPDATA%\padmate\` 放 `config.json`、`state.json`、`profiles\<id>\`、`macros\`（脚本库）、`logs\`、`startup\`（自启长参数文件）；`%LOCALAPPDATA%\padmate\run\` 放 `daemon.json`、`held.bin`。安装目录默认 `%LOCALAPPDATA%\Programs\padmate\`，程序在 `resources\bin\`，不放用户数据。

## 4. 包职责与依赖

`daemon/internal/` 的包。“纯包”不导入 `x/sys/windows`、`input`、`output`、`script`、`latency`，不记日志，不读真实时间（不变量 2）。

- **纯逻辑包**：`keys`、`control`、`profile`（`Resolve` 合并父配置，读文件的函数由调用者给）、`engine`、`clock`、`haptic`、`sdlmapping`、`mapwizard`（界面用 TypeScript 实现同一套，`testdata/vectors.json` 共用）、`amgp`、`kblayout`、`watch`、`padlabel`、`padfamily`（手柄的族：各自的功能键和内置紧急组合，ADR-0017）、`input/jc2`（Joy-Con 2 的协议：广播、命令白名单、回复、报告、摇杆校准、控件映射、静默看门狗；只导入标准库，所以能在 Linux 上构建和测试，§5.3.1、§7.1.1）。`Level` / `Registry` 返回的 binding 不经 `Compile`，必须先过 `engine.CheckBinding`，否则松开后才执行的手势按住的键没人松开（§7.7）。
- **与 Windows 和 SDL 打交道的包**：`output`（Arbiter、MenuMask、`Loop`、布局 Resolver 跨平台；SendInput 在 `_windows.go`，§8）、`input`（T-input、SDL 后端、Joy-Con 2 的 hub、虚拟手柄、设备身份、事件 JSON 行；引擎不导入它）、`input/jc2win`（蓝牙 LE 上 Joy-Con 2 的 Windows Runtime 传输，§5.3.1；`doc.go` 跨平台，其余是 `_windows.go`）、`sdlpin`、`winhook`、`winfocus`（§9）、`winsession`、`keycapture`、`ownwindow`、`instance`、`autostart`、`emergency`、`diagnostics`、`tray`、`ipc`（§11）、`profilelib`（§10.1）、`trace`、`logging`、`latency`（§13，叶子包，纯包不能导入）。`injecttest`、`winsessiontest` 是测试工具。
- **编排**：`slot`（串起 engine、脚本和 Arbiter）、`supervisor`（设备与 slot 对应、router、重启、重载）、`script`、`simtest`（只有 `_test.go` 能导入，§14）。

**导入规则**由 depguard 与 forbidigo 强制，配置在 `daemon/.golangci.yml`：纯逻辑包不得导入 Windows 相关包、`input`、`output`、`script`、`latency`、`winsession`、goja、esbuild 和日志包，也不得直接读时间（`time.Now` 等）；`winsession` 不得导入映射相关的包；整个模块禁止调用 `output.SendInput`，例外是 padmated 的 `sender_windows.go`、padmatectl 的 `send_windows.go`、`injecttest`、`output` 和测试。纯逻辑包、`slot`、`supervisor`、`simtest` 和其他包的非 Windows 部分能在 Linux 上编译并测试。

## 5. 线程模型

每个线程拥有什么、和谁通信、退出和 panic 时怎样释放键。理由见 [引擎与线程](adr/0005-engine-and-threads.zh-CN.md)、[不卡键](adr/0006-no-stuck-keys.zh-CN.md)、[多手柄与 slot](adr/0003-devices-and-slots.zh-CN.md)。

### 5.1 线程总览与所有权

| 线程 | 锁定 | 职责 |
|---|---|---|
| main | 是 | `systray.Run`（§5.2） |
| T-input | 是 | SDL 事件泵、紧急组合检测、手柄效果、接收器看门狗、Joy-Con 2 的 hub（§5.3–§5.5、§5.3.1） |
| jc2win 传输 | 是 | Joy-Con 2 在 Windows Runtime 一侧的工作：扫描、连接、GATT、白名单内的写入。一个 goroutine，一切经通道交给 T-input（§5.3.1） |
| slot loop ×N | 是 | `engine.Step`、脚本分发、定时器。每个 slot 一个，不管有没有手柄；最多 4 个（§5.10） |
| script worker | 否 | esbuild 构建、新 runtime 的 setup、旧 runtime 的 teardown |
| T-output | 是 | `output.Loop`：拥有 Arbiter，唯一的 SendInput 调用者（§5.9） |
| T-session/focus | 是 | `winsession` 的隐藏窗口收会话、电源、安全桌面通知；`winfocus` 同线程监听前台（§5.13、§9） |
| T-hook | 是 | `WH_KEYBOARD_LL`，只在捕获快捷键、测试和 probe 里安装（§5.14） |
| router | 否 | `supervisor.Run`：读事件、记设备状态、把边沿送进 slot 通道，从不阻塞（§5.7、§5.8）。每个 slot 另有 runner goroutine 负责重启 |
| watcher | 否 | 轮询 profile 文件，变了就重载（§5.18） |

所有权：设备状态归 router；引擎和脚本 runtime 归各自的 slot loop；按键状态归 Arbiter，Arbiter 归 T-output，其他 goroutine 用 `Do` 或 `RemoteArbiter` 交命令；`haptic.Mixer` 归 T-input。跨线程通道都有界：满了丢（按钮和轴）或排队（设备事件），写方从不阻塞。

### 5.2 常驻入口与托盘（main）

- **单实例。** 实机模式在加载 profile、日志和 SDL 之前获取命名 mutex `Local\padmate.instance.<SID>.<session>`。第二次启动经认证 IPC 把配置选择转发给第一个实例；冲突的启动参数被拒绝。
- **退出序列。** 托盘“退出”和安装程序的 `daemon.exit`（§11）走同一条，只跑一次：(1) `output.BlockExit`（不可重开，立即释放所有键）并以 1 s 为限 `Supervisor.Pause`；(2) 发布 `daemon.exiting` 公告，等已注册的界面调 `ui.exitReady` 或断开（最多 2 s），期间界面仍能保存；(3) `daemon.exit` 等调用者连接关闭（最多 500 ms），让回复先写出；(4) 走正常退出路径（§5.17）。崩溃、Ctrl+C、会话结束和 `WM_CLOSE` 不发公告。
- **登录自启。** 只有点击托盘菜单或界面经 `startup.set` 才写或删 HKCU Run 的 padmate 值；命令超过 260 个 UTF-16 单元时，完整参数存进 `%APPDATA%\padmate\startup\`，Run 只引用它。开发版和测试构建不建自启项，`startup.set` 返回 -32017。登录时没有标准输出，dry-run 打印器用 `io.Discard`。

### 5.3 T-input 与 SDL

`internal/input`，理由见 [手柄输入](adr/0002-input-sdl.zh-CN.md)。

- `input.Open` 启动 T-input：锁线程，加载 SDL（`$PADMATE_SDL3`，再 exe 旁边；Windows 必须是固定的 3.4.16；每进程只加载一次，同时只有一个 Source），按“总设 → preset → `--hint`”的顺序设 hint（后设覆盖先设；“总设”是允许后台事件、关掉 SDL 的信号处理器、`SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1`），`SDL_Init(SDL_INIT_GAMEPAD)`。
- 所有 SDL 调用都在 T-input 上：其他 goroutine 把函数经命令通道交给它，在两次 `SDL_WaitEventTimeout(2 ms)` 之间执行。不用 `SDL_PushEvent` 唤醒（与 `SDL_Quit` 有竞争）。事件一返回就读 `latency.Now` 记进 `Event.Stamp`（§13）。
- **事件通道**容量 1024，发送从不阻塞：满了丢按钮和轴并计数。`DeviceAdded`、`DeviceRemoved`、`ComboHeld` 不丢，进 backlog 按序等位；backlog 不空时按钮和轴也丢，所以没有事件越过排着的设备事件。
- **映射变化。** SDL 映射按 GUID 立即生效，不松开按着的东西。没有映射的 joystick 按 raw 报告（`IsGamepad` 为 false）；加入映射时同 id 再发 `IsGamepad` 为 true 的 `DeviceAdded`；拿走映射时发 `DeviceRemoved` 和 `IsGamepad` 为 false 的 `DeviceAdded`；映射变了发 `DeviceRemapped`（不写进 trace，§5.7）。
- **效果**（理由见 [手柄反馈](adr/0015-controller-feedback.zh-CN.md)）：都在 T-input 上执行。一次 Effector 调用失败，它所在的组冷却 5–7 s，期间不写任何东西（2.4G 接收器不接 OUT 报告时，阻塞写会拖住所有手柄的输入）。`padmated.effectQueue` 让 slot 和引擎只提交数据，一个 worker 按序调 T-input；取消永不丢弃，脚本放出的东西不能比脚本活得长。
- **关闭与 panic。** `Close` 分离虚拟手柄、关设备、`SDL_Quit`、关事件通道。T-input 上的 panic 或 `Goexit` 也这样结束：`Err()` 在 `Events` 关闭之前设好，返回 `*PanicError`；之后的命令返回 `ErrClosed`。

### 5.3.1 蓝牙 LE 的 Joy-Con 2（`ble.go`、`jc2hub.go`、`jc2/`、`jc2win/`）

SDL 读不了 Joy-Con 2，所以 T-input 还跑着第二个后端，理由见 [手柄输入](adr/0002-input-sdl.zh-CN.md)。它分三部分：`jc2`（纯协议）、`jc2win`（Windows Runtime 传输，经 winrt-go）和 `internal/input` 里的 hub（`jc2hub.go`，由 `ble.go` 接到 `Source` 上）。它的手柄在哪些地方和别的手柄一样，见 §7.1.1。

- **启动。** 只有 `Options.JoyCon2` 不为 nil 时：padmated 的实机模式会设它；`monitor`、`record` 和大多数测试留空。`Source.init` 在 hint 和组合监视之后启动传输。传输起不来不算 `Open` 的错误：SDL 的手柄照常工作，`JoyCon2Available()` 和 Joy-Con 2 的各方法返回原因（Windows 以外是 `ble_other.go`，没有传输）。测试给 `JoyCon2Options.Transport` 一个假传输，所以 hub 能在 Linux 上测试。
- **传输 goroutine。** `jc2win.Start` 启动一个 goroutine，它把自己锁在一个 OS 线程上，为这个线程初始化 Windows Runtime（多线程单元），并做所有不在回调里的 WinRT 调用：扫描、连接（会话、服务、简短的启动对话）、通知开关、白名单内的命令写入、连接参数请求，以及每 2 s 读一次每条在线链路当前的连接参数（`GetConnectionParameters`，仅 Windows 11；间隔、延迟或超时变化时发一个 `Params` 事件，第一次读取随 `Connected` 一起给出；这是本 goroutine 上的普通调用，不在回调里）。`Scan`、`Connect`、`Disconnect`、`Close` 只排队请求，从不阻塞。能写什么只在一处决定（`gate.go`：对最终字节跑 `jc2.CheckRequest`，紧接着才是唯一的写调用；`guard_test.go` 守住这一点）。
- **回调。** 三个 WinRT 事件在 Windows 线程池的线程上调进 Go：收到一条广播、特征值变了、连接状态变了。每个都复制字节、读 `latency.Now`、不阻塞地发进有界通道（256；满了计一次丢弃）然后返回，不等待，不 await，也不拿慢路径持有的锁。padmate 自己不创建回调：winrt-go 的 delegate 共用固定的四个，只创建一次（不变量 5）。
- **事件怎样进入 T-input。** 循环的每一轮里，在 `SDL_WaitEventTimeout(2 ms)` 之后、接收器看门狗旁边，`pumpJoyCon2` 取空这个通道（最多 512 条，所以大量广播拖不住循环），再运行 hub 的定时器。没有唤醒通道：一条报告最多多等一轮（2 ms），加上手柄自己的 15 ms（Windows 11）或 60 ms 发送间隔。每个公开方法（`JoyCon2Pair`、`JoyCon2Forget` 等）都经 `s.do`，所以 hub 只在 T-input 上被碰。hub 不读时钟，用的是 SDL 的计时，也就是 `Event.Time` 的时钟。
- **hub 做什么。** 决定连谁（配对窗口、记住的手柄、自动连接），什么时候扫描（只在窗口开着、或有记住的手柄可连时），组成手柄，用 `jc2` 映射每条报告，把和上次状态的差别发成 `ButtonDown`、`ButtonUp`、`AxisMotion`。事件的 `Stamp` 是它那条报告的回调里取的，所以延迟统计从那里算起（§13）。
- **连接策略。** 手柄只在按住 SYNC 时接受 PC（“标准”广播，主机地址全 0）。hub 只连标准广播，而且要么配对窗口（默认 30 s，1–600 s）开着且还没取走手柄，要么手柄已被记住且自动连接开着。连接失败或被关闭的地址 5 s 内不再理会；连接 30 s 还没连上就取消；最多八条连接，每条一个玩家编号。窗口里的第一只手柄连上后被记住（`OnRemember`，再 `Memory.RememberJoyCon2`，§10.1）。序列号已经以别的地址连着的手柄会被断开。
- **不卡键**（不变量 1）。一个手柄的事件以它的 `DeviceRemoved` 结束，所有消费者都把它当作松开该 `DeviceID` 的一切，而 `DeviceID` 永不复用。hub 在这些时候报告手柄移除：它变 dormant（见下）、链路丢失或被关闭、这一对变化、手柄被忘记或断开，以及 `Close`（先于 `SDL_Quit`）。恢复时 `Reenumerate` 先让 hub 取空事件并走一遍定时器（睡眠期间死掉的链路就没了），再对每个在线的手柄发一个 `DeviceState`。
- **Dormant。** 300 ms（`jc2.DefaultSilence`）没有报告的链路是 dormant：它在下一条报告之前不算任何手柄，链路保留。它所在的手柄报告移除；如果那是一对，仍在报告的那一只立刻作为单只新增（自己的 `DeviceKey`、新的 `DeviceID`，按着的控件算新按下），不等静默的那一只被放弃。dormant 链路的下一条报告让布局像全新连接那样重做：原本单独的手柄以新的 `DeviceID`、同一个 `DeviceKey` 回来（router 按重连处理，§5.8），找回搭档的单只被一对替换（§7.1.1）。3 s（`jc2.DefaultLinkGiveUp`）没有报告的链路，或 Windows 报告已断开的链路，则被关闭。这里没有“武装”：和接收器（§5.5）不同，这些手柄按定时器发报告，静止时也发。

### 5.4 设备身份（`internal/input/devicekey.go`）

DeviceID 每次连接都变；`DeviceKey{GUID, Serial, Path, N}` 重连后不变，N 区分除 N 外都相同、同时连着的手柄。router 用它认出回来的手柄，让它收回 slot 或给暂代 slot 改换 home（§5.8）。Joy-Con 2 的 DeviceKey 依据手柄 flash 里的序列号（§7.1.1）。

左右两只 Joy-Con 同时连着时，SDL 把它们换成一个合并设备（§7.1）：单只被移除，合并设备以新的 DeviceID 加入。SDL 3.4.16 里合并设备没有序列号，所以它的 key 是合并设备的 GUID 加固定路径，任意两只 Joy-Con 组成的、每一次连上的都一样。单只 Joy-Con 有自己的 key（GUID 加序列号），合并设备散开、它回来时拿回这个 key。换设备就是一次普通的移除加一次加入（`DeviceRemoved` 做 `ReleaseAll`，§5.6）；按 DeviceKey 保存的状态（调校、state.json 里的层状态）不会从单只带到合并设备上。两只 Joy-Con 2 组成的一对（§7.1.1）也是这样换掉的，但它有序列号（两个合在一起），所以 key 是它自己的。

### 5.5 接收器看门狗（`receiver.go`、`watchdog.go`、`hidbeat.go`）

Switch Pro 协议的 USB 接收器在手柄关机后不报告断开。只有 padmated 开看门狗（`input.DefaultReceiverTimeout` = 2 s），读 HID 心跳：空闲心跳持续一段时间后“武装”，之后心跳停满 timeout 就合成 `DeviceRemoved`。移除后不关 gamepad 和 HID 句柄（关了 USB 设备会进简单模式），该设备的下一个事件让它以新 id 复活。

### 5.6 slot（`internal/slot`）

`Slot` 把一个 Engine 接到 Arbiter 上，不能并发使用，由调用者、`Replay` 或 slot loop 驱动。`Config.Arbiter` 是接口：本地 `*output.Arbiter`（演练、黄金测试）或 `*output.RemoteArbiter`（T-output）。

- `Handle(ev)` 处理事件。`DeviceRemoved` 先 `ReleaseAll`，再 `engine.Reset`，State 清零并关闭脚本。`SetPaused(on)` 暂停时先 `engine.Pause` 再释放同 slot 号任意代数的 owner，恢复时什么也不发；`ReleaseAll`、`Reset`、`Reload`、设备移除都保持暂停。
- `Reload(prog, gen)`：`engine.Reload`，释放同 slot 号任意代数的 owner；State 不变。
- **手柄自己的功能键和开关**（ADR-0017）。`Config.Program` 是用默认功能键编译一次的 profile；slot 运行其中带有自己手柄功能键的变体（`engine.Program.ForFunctionKey`：第一次要才编译，之后由所有 slot 经这个 Program 共用）。slot 在创建时、得到或失去手柄时、每次 `Reload` 和 `RefreshFunctionKey` 时，在自己的 goroutine 上向 `Config.FunctionKey(key, info)` 要功能键。键与运行中的不同才切换（相同则什么也不做，按着的功能键仍按着）；切换是同一份 profile、不换代的引擎 `Reload`：按着的控件作废，Arbiter 释放该 slot 的所有 owner（不变量 1 的 `ReleaseAll`），按着的功能键被忘掉（§7.9）。`Config.Switches(key)` 对反馈开关也是如此：`Options.Switches` 每次读的是 slot 自己手柄的那组。
- **脚本接线**：暂停、`DeviceRemoved`、`Reset` 关闭脚本 runtime；恢复、`DeviceAdded`、Resync 只排队让 worker 重建，不在 slot loop 里 setup。脚本手势经 `GestureResolver` 交给同一个引擎识别器，不建第二套计时器。
- **冷启动输入屏障**：没有有效 Host 时，最多暂存 128 个按钮或轴边沿，脚本就绪后按原顺序回放。冷启动失败或溢出就释放整批，不用空注册表回落声明式动作。

### 5.7 Resync 语义

router 给每个已连接的手柄记一份 `slot.State`，把已分配手柄的事件送进它的 slot 的有界边沿通道（`slot.Edge`，容量 256）。

- **落后。** 通道满了 slot 就落后：之后的边沿跳过并计数，router 照常更新状态。落后期间 slot 必须知道的边沿按序记为待投递（先一个释放，再现分配手柄的 `DeviceAdded`）。每个事件、每次 Sync 和一个 2 ms 的 `clock.Alarm` 都重试：先投递待投递的边沿，再投递 `Edge{Resync: &st}`，不等手柄的下一个事件——手柄只发变化，丢的可能正是最后的抬起。
- **丢事件复位。** router 在每个事件、重试和 Sync 后比较 `Dropped`，增加了就把每个手柄的 State 清零，给每个 slot 投递 `Edge{Reset: true}`。`DeviceRemapped` 同理，是对这只手柄的复位：按着的控件在新映射下可能是另一个元素，旧的那个再也不会发抬起，所以物理上还按着的控件要松开再按才算数。
- **`Slot.Resync(st)`** 把 State 与 `st` 比较，合成缺失的边沿（先释放，再按下，再轴）。它只让状态一致，不重放间隙里的过程：间隙里按下又松开的按钮丢了这次按压。

### 5.8 supervisor（`internal/supervisor`）

- **构造。** `New(Config)` 要 `Clock`、`Events`（关闭它等于取消 ctx）、`Output` 和 `Program`；`Slots` 为 1 到 4，全部在 `New` 里建好，用同一个程序。回调都不能阻塞也不能调用 Supervisor。同 `ProfileKey` 的各 slot 共用内存 KV。
- **分配**（理由见 [多手柄与 slot](adr/0003-devices-and-slots.zh-CN.md)）。每个 slot 有一个 home（DeviceKey）和一个暂代标志；每个匹配 `Config.Device` 的手柄要么占一个 slot，要么排队。
  - 新连接：有空 slot 就暂代下标最小的，成为 home；没有空 slot 而有 home 是它的暂代 slot，就收回；否则排队尾。手柄离开：队首手柄立即接手（先收到 `DeviceAdded` 副本，再收到 Resync）。
  - 确认：暂代手柄在启用的 slot 上的第一个 `ButtonDown` 让分配生效；轴事件、Resync 带来的按着的按钮、禁用 slot 上的按下都不算。
  - 暂代期间可被拿走：home 的 key 连上时收回；排队手柄的 `ButtonDown` 接管最早的暂代 slot。
- **按手柄设置**（ADR-0017）。`Config.FunctionKey`、`Config.Switches(key)` 和 `Config.Calibration(key)` 都是在 slot 自己的 goroutine 上为 slot 自己的手柄去问，返回最近一次提交的 config.json 的内容；`OnFunctionKey` 报告某个 slot 换了功能键。`RefreshFunctionKeys(ctx)` 和 `RefreshCalibration`、`RefreshFeedback` 一样是类似 Sync 的屏障，在每个 slot 已送达的边沿之后运行 `Slot.RefreshFunctionKey`：手柄功能键没变的 slot 什么也不做，变了的只松开自己的键，不影响别的 slot。没有手柄的 slot 用 key `""` 去问，得到的是标准布局、没有自己设置的手柄的功能键。
- **隔离。** 各 slot 有自己的引擎、State、runner、边沿通道，按下的键带自己的 `Owner.Slot`，释放时只释放自己的。一个 slot 卡住、panic 或被禁用，不耽误也不释放别的 slot。
- **`Sync(ctx)` 是屏障**：返回时 Sync 之前送进 `Events` 的事件都已由 slot 处理，引起的命令已在 Output 上执行。**`Reload(ctx, prog)`** 让每个 slot 换上 `prog`：落后的 slot 先用旧程序追上，所以 Reload 之前送进的事件用旧程序，之后的用新程序；每个键抬起一次，重载取消的控件要松开再按才工作；不能在 slot 的 goroutine 上调用（死锁）。`UpdateScriptParams` 是只改 `script.params` 时的轻量版本：全部 slot 准备成功后才提交，不换 runtime，不释放按键。
- **关闭顺序。** 取消 `Run` 的 ctx 或关闭 `Events`：`Run` 关闭每个边沿通道，slot loop 处理完排队的边沿、经 Output 释放后结束，`Run` 等它们都结束才返回。调用者再 `Output.Stop`，最后关 `input.Source`；`Run` 从不调 `Output.Stop`。

### 5.9 T-output（`output.Loop`）

- `StartLoop(clk, sender, opts)` 启动拥有一个新 Arbiter 的 goroutine，`LoopOptions.Lock` 把它锁到 OS 线程且不解锁（padmated 必须设置），使 Sender 只在一个线程上被调用。
- `Do(f)` 把 f 交给循环，等执行完返回错误；命令按到达顺序逐个执行。`Remote()` 返回 `RemoteArbiter`。
- **held-keys 日志与启动扫除。** 循环在自己的线程上打开日志（格式见 §8.2）。进入命令循环之前先扫除：读出上次运行留下的键（只认同一次开机、同一登录会话写的），经 `KeyDown` 查出仍按着的，经 MenuMask 各抬起一次，不经过闸门。所有 `Do` 排在扫除之后。日志损坏、来自别的开机或会话、打不开时，不释放任何键。
- **闸门 `BlockFor(b, on)`** 按所有者开关 Arbiter 的闸门（§8.2）：`BlockUser`（用户暂停和紧急组合）、`BlockSystem`、恢复扫描、`BlockExit`、`BlockEmergency`。任一所有者关着闸门就关着。它不等待，任何 goroutine 可调：只改一个原子值再唤醒循环。循环在每个命令之前应用：有过关闭调用就先 `ReleaseAll`（经 ledger 和 MenuMask），再抬起 carried 键（扫除没能抬起而 `KeyDown` 仍报按着的键）。
- **`Stop()`**：先 `ReleaseAll`，重试失败的抬起；仍有没抬起的键返回错误并写明可能卡住的键。再抬起 carried 键，按 ledger 剩余键改写日志，没有键就删除，否则留给下次启动。
- **panic 与 Goexit。** Arbiter 的 Sender 是 ledger，按序记下注入留下的按着的键；`Send` 自己 panic 时这一批的按下都算按着。循环不是正常结束时，在同一 goroutine 上用一次 Send 按按下的逆序抬起 ledger 里的键，再抬起 carried 键、改写日志，然后关 `Done()`，让进行中的 `Do` 返回 `ErrLoopClosed` 与 `*PanicError`。

### 5.10 slot loop（`slot.RunLoop`）

在调用者的 goroutine 上运行并拥有这个 Slot，`Lock` 锁定 OS 线程。`select` 有 ctx、边沿通道、定时器、作业通道和可选的脚本唤醒通道。作业之前先处理通道里已有的边沿，所以作业是它们的屏障（Sync 靠它）。`Edge.Pause`：`PauseOn` 在边沿的 Reset/Resync/Event 之前暂停，`PauseOff` 在它们之后恢复，所以 Resync 带来的按下都已取消。脚本构建和 setup 在 worker 上执行，构建失败保留旧 runtime。边沿通道关闭或 ctx 结束时返回，返回前 `ReleaseAll`。panic 和 Goexit 见 §5.16。

### 5.11 暂停与紧急组合

理由见 [不卡键](adr/0006-no-stuck-keys.zh-CN.md)。

**用户的暂停。** 状态归 router，所有 slot 共用，不保存，经 Reload、复位、重连和重启都保持。与系统挂起（§5.13）分开记，两者有一个在就按住映射。`Supervisor.Pause(ctx, on)` 先让 router 路由排着的事件再切换，调 `Output.BlockFor(BlockUser, on)` 并给每个 slot 投递暂停边沿；返回时所有键已抬起，之后的按下被闸门丢掉。暂停时 slot 取消控件按着的周期；恢复只开闸门，不按任何键：按着的控件要松开再按才工作。调用者有紧急组合、托盘、IPC。

**紧急组合**（`input.ComboWatch`，T-input 上检测）。

- 按每个设备的 `DeviceAdded` 选组合（`ComboFor`，padmated 的实现是取该手柄自己设置里的组合，没有则 `DefaultCombo`，§10.4；ADR-0017），自己用位掩码记成员，不查 SDL。T-input 在事件送进通道之前让它看到每个事件，所以被丢的按钮事件它也看得到。
- 所有成员都按着时开始计时，按住 `ComboHold` = 2 s 触发一次并锁存，松开一个成员再按才重新计时。成员自己的绑定照常工作，触发时被暂停取消。
- 触发时先调 `OnCombo`，再把 `ComboHeld` 事件送进通道（进 backlog，不丢）。padmated 的 `OnCombo` 调 `output.Loop.Block(true)`：不经 slot loop、不等 T-output，slot 卡住或 `Events` 满时也立即释放。
- router 收到任何手柄的 `ComboHeld`（含排队的、被过滤的）就切换暂停；没有 slot 收到这个事件。

### 5.12 离线紧急释放

`padmatectl release-all` 不经 IPC，用于守护进程卡住或 IPC 不可用时。

- 命令触发当前交互会话的命名事件 `Local\padmate.panic`（auto-reset）。只有 `--inject` 的 padmated 监听：auto-reset 事件一次只唤醒一个等待者，演练监听会抢走给 `--inject` 实例的触发。
- `startEmergency` 在 `startLive` 里建好事件，所以 `startLive` 返回时事件已存在。安全描述符只给当前用户，不管 padmatectl 提不提权都能打开。事件建不了时 stderr 提示，照常映射。
- 收到事件后 `Supervisor.ReleaseAll`：用独立的 `BlockEmergency` 闸门立即释放 T-output 的全部键（含 carried 键），再把 `ReleaseAll` 作业按序送到每个 slot，销毁旧脚本和异步回调，各 slot 与 T-output 完成屏障后才重新开闸。不改变 router 的暂停、挂起或控件状态。

### 5.13 系统挂起与 T-session

**T-session/focus**（`internal/winsession`）。`Start` 起的锁定线程，窗口、钩子、计时器都属于它。

- 隐藏的顶层窗口（不用 `HWND_MESSAGE`，它收不到广播），接收会话、电源、`WM_ENDSESSION`、`WM_CLOSE`、桌面切换和每 2 s 的计时器。`WM_APP+1` 到 `+3` 和计时器 ID 1 归 winsession，`WM_APP+8` 起和计时器 ID 2 起归 `winfocus`。
- 清原因的消息、桌面切换和计时器只在投递的消息里查询会话与输入桌面（别的线程 `SendMessage` 来的先重新投递，不让发送者等 RPC）。睡眠和会话结束先等 `Config.Barrier`（最多 1 s）再返回给系统；`WM_ENDSESSION(TRUE)` 再调 `OnStop` 并等 `Close`（最多 3 s）。

**`Supervisor.Suspend(r)`。** 设系统挂起原因（Lock、Disconnect、Desktop、Sleep、End），0 表示结束。任何 goroutine 可调，从不等待。

- 加入原因时立即 `Output.BlockFor(BlockSystem, true)`：释放所有键、丢掉之后的按下，不经 router，router 或 slot 卡住时也生效。
- router 进入挂起时应用上次以来设过的所有原因（来不及看到的一次锁定和解锁也让 slot 取消按着的周期），投递 `PauseOn`；离开时先路由此刻排着的事件，仍按按住处理，再投递 `PauseOff`。
- 只有 router 打开系统的闸门：给每个 slot 发一个排在 `PauseOff` 后面的屏障，最后完成的屏障才开闸，因为还没处理暂停边沿的 slot 可能按下它之前的东西。挂起与用户暂停互不影响，两个闸门都打开才映射。

**唤醒恢复。** router 为挂起前或期间已观测为 active 的控件保存独立的隔离：合成释放与空扫描不能清除它，恢复后 release 或 neutral 才解除。恢复协调器在 T-input 调 `Source.Reenumerate` 取得完整的 `DeviceState`，再 `Supervisor.Sync`；没有新系统原因时才恢复。新的挂起原因使旧扫描失效；扫描失败保持挂起并重试。用户暂停、系统挂起、恢复扫描各持有输出闸门，互不解除。

### 5.14 T-hook（`internal/winhook`）

`WH_KEYBOARD_LL`，只用一个 `windows.NewCallback`，跑 `GetMessage` 循环。回调读 `latency.Now`，复制数据、询问是否吞掉，非阻塞送进容量 4096 的通道（满了计数丢弃）；不吞的事件交给 `CallNextHookEx`。只在捕获会话期间安装。

### 5.15 日志（`internal/logging`）

- slog JSON 行，默认写 stderr；没有 stderr（GUI 子系统）时写 `%APPDATA%\padmate\logs\padmated.log`。日志不写窗口标题；按键事件只在 debug 级别写。纯包不记日志，由 supervisor 记下 slot 的错误。
- `input.Source` 的日志不由 T-input 直接写：放进容量 256 的队列，由别的 goroutine 交给 handler，满了丢弃计数。原因：控制台 QuickEdit 选中文字时写 stderr 会阻塞，T-input 不能因此停止泵 SDL、检测紧急组合。

### 5.16 panic 恢复

原则：panic 之后出问题的那一级不可信，跳过它，让下一级去释放键。

- **slot loop。** recover 后不经引擎，直接 `Arbiter.ReleaseOwners` 释放同 slot 号任意代数的 owner（自己也 recover）。`RunLoop` 返回 `*slot.PanicError`。
- **slot 的重启。** runner 退避后用新 `slot.New` 重启（沿用同一个边沿通道，控件全部抬起），退避 100 ms，10 s 内每多一次 panic 翻倍。重启不 Resync（会再次按下可能引起 panic 的按钮）。10 s 内 3 次 panic 就禁用：router 丢掉该 slot 的边沿，已分配的手柄仍占着 slot；下一次分配重新启用。
- **T-output。** recover 后按 ledger 抬起按着的键，循环结束（§5.9），守护进程把它当致命错误。
- **T-input。** recover 后像 `Close` 一样关闭 SDL，关闭 `Events`，`Source.Err()` 返回 `*input.PanicError`。padmated 看到 `Events` 关闭就走退出路径。
- **slot 的 runner、router、watcher。** recover 后不记日志（可能正是 logger panic），`supervisor.Run` 返回 `*supervisor.PanicError`，等所有 slot loop 释放并结束，padmated 走退出路径。
- **`runtime.Goexit`。** 不能 recover，但 defer 照样执行；循环和 goroutine 用一个标志认出不是正常结束，像 panic 一样处理。其他持有映射或输出资源的 goroutine 同样先 `ReleaseAll`。

### 5.17 padmated（`cmd/padmated`）

用法见 `padmated -h`：实机模式必须写 `--dry-run` 或 `--inject` 之一；重放用 `--fake-input`，只能 `--dry-run`。用法错误退出码 2；启动时出错退出码 3；映射开始之后的失败为 1。停止信号是 Ctrl+C 和 SIGTERM；注销和关机不给信号（按微软文档，未实测），改从 winsession 的 `WM_ENDSESSION` 得知，与 `WM_CLOSE` 走同一退出路径。重放只处理第一个有按钮或轴事件的设备，Sender 是演练打印器，输出与本机布局无关。

**实机模式的启动顺序**（`live.go`，`startLive`）。

1. `input.Open` 启动 T-input，带 `OnCombo`（调 `Loop.Block(true)`，Loop 启动前什么也不做）。
2. `--dry-run` 用演练打印器，`--inject` 用 `sender_windows.go` 构造的 `output.SendInput`。padmated 只在这里构造它，只交给 `output.StartLoop`（`TestNoSendInput`、`TestChooseSender`、forbidigo）。随后启动 T-output；`--inject` 时 `HeldFile` 是 `%LOCALAPPDATA%\padmate\run\held.bin`，演练不设。`startLive` 等 `Loop.Swept()`，按结果写日志，不让 padmated 退出。
3. `startSession` 启动 T-session：`OnChange` 调 `Supervisor.Suspend`；`Barrier` 是 `Loop.Do` 一个空函数，所以返回时 T-output 已发出释放。已锁着的会话在 `Run` 之前就挂起映射。起不来时 stderr 提示“锁屏、睡眠和注销不再释放按键”，照常往下走。
4. 只在 `--inject` 时 `startEmergency` 建 release-all 事件（§5.12）；`supervisor.New` 并在自己的 goroutine 上 `Run`；没写 `--watch=false` 时起 watcher（§5.18）。

**启动报告。** 界面传 `--startup-report FILE`；启动失败时写一个 JSON 对象（字段见 `startup_report.go`），读完参数就删掉 FILE。打包版没有 stderr，界面只靠它知道原因。

**停止与退出路径**（`shutdown`，写在 defer 里，panic 展开也执行）。收到停止信号或 T-session 的 `OnStop`：停 watcher，取消 `Run` 的 ctx，退出码 0。`Run` 自己返回（T-output 结束，或 `Events` 关闭）：退出码 1；按键此时已释放（`Events` 关闭由 slot loop 释放，T-output 结束由它的 panic 恢复释放）。之后按顺序：

1. 停 watcher 并等它（最多 1 s，卡住的读不让按着的键等它），再等 `Run` 返回，这时 slot loop 已经经 T-output 释放了按键。
2. `Loop.Stop`：`ReleaseAll`，重发失败的抬起。出错时退出码 1。
3. 关闭效果队列并等 worker，再 `Source.Close`，共等最多 1 s，超时则 `Source.RequestClose`。这 1 s 只管释放之后的尾部，第 1–2 步的释放及重试不被跳过。
4. 刷新打印器；关闭 T-session 和日志。T-session 调过 `OnStop("session-end")` 时，`WM_ENDSESSION` 在等它的 `Close`（最多 3 s），之后系统随时可能结束进程，所以日志先写完。

Loop、Source 和 T-session 都归 padmated：`supervisor.Run` 不调 `Loop.Stop`，不关 Source，也不关 T-session。

### 5.18 热重载（`live.go`）

profile 文件的 watcher（§10.5）每看到新内容调 `reloadProfile`：内容没变就什么也不做；解析、编译有错误就记 error，旧程序照常运行，什么也不释放；两个 profile 都有 `script` 且除 `script.params` 外相同，调 `Supervisor.UpdateScriptParams`，不重建脚本；其他变化调 `Supervisor.Reload`。

**有父配置的 profile（`extends`）。** `Profile.Parents` 记着合并时读到的每个父配置的 ID 和内容 sha256。每个父配置文件另有一个 watcher；任一有新内容就重读子配置、重新合并，再按上面的规则处理（父配置序列也没变才算内容没变）。读不到子配置文件（被占着、先删后建）时：用运行中配置装入时的原文合并，没有原文就在 200 ms 后重读，最多 5 次。

### 5.19 `padmatectl trace`

`padmatectl trace --profile FILE --input FILE [--json]` 在虚拟时钟上重放录制的事件，不注入键盘，除非给了 `--config FILE`，否则不读 config.json。功能键是 trace 里那只手柄的（ADR-0017）：由它的 added 事件所属家族的默认值给出（单只 Joy-Con 是 `lt`（ZL）或 `rt`（ZR），一对是 `back`，其他手柄是 `start`），或取 `--config` 文件里的 `devices.<DeviceKey>.functionKey`；`--function-key`（`start`、`back`、`leftshoulder`、`rightshoulder`、`lt` 或 `rt`）对整条 trace 强制指定一个。

## 6. 一次按键的数据流

示例绑定：`west` 点按 = F13，按住 = F14，balanced 策略。

1. SDL 投递 `GAMEPAD_BUTTON_DOWN(west)`，T-input 打 `Event.Stamp` 后送进 `input.Source` 的通道；router 更新设备状态，推入该 slot 的边沿通道（§5.7）。
2. slot loop 在 `slot.Handle` 里调 `engine.Step`：west 有 tap 和 hold，进入 Pending，`NextDeadline = t + holdMs`；等待期间 south 按下，balanced 策略缓冲它的边沿。
3. 250 ms 内松开 west 判为 tap：`Tap(owner, F13)`，再按序重放缓冲的 south；超时判为 hold：`Down(owner, F14)`，松开时 `Up`。
4. Arbiter 解析布局（每次按下字符键都查前台线程的 HKL，§8.1），引用计数，保证最短按下时长。
5. T-output 的 ledger 先把这一批按下写进 held-keys 日志（`--inject` 时，§8.2），再经 MenuMask 调 SendInput，发完改写日志。同时产出 Decision 记录，供 trace 和界面时间线使用。

## 7. 引擎

`internal/engine` 是纯函数式状态机：输入是带时间戳的边沿和轴值，输出是对 `Output` 接口的调用和 `Decision` 记录。它不导入 `input`、`output`、`script`，不调用 `time.Now`，时间只来自 `Input.T` 和 `Apply(now)`（不变量 2）。理由见 [ADR：引擎与线程](adr/0005-engine-and-threads.zh-CN.md)。

### 7.1 控件名

控件由 `internal/control` 定义，名字区分大小写；别名只用于输入，`String()` 返回标准名。

- 按钮：`south east west north back guide start leftstick rightstick leftshoulder rightshoulder dpad.up/down/left/right misc1..6 paddle1..4 touchpad`。touchpad 只是按下，不读触摸位置。
- 虚拟控件：`lt`/`rt`（扳机作按钮）、`lt.full`/`rt.full`（第二段）、`ls.up … ls.upright`/`rs.*`（方向含对角）、`ls.active`、`ls.inner/outer`、`ls.cw/ccw`（旋钮一格，仅 `mode:"knob"`）、`dpad.upleft` 等（仅 `analog.dpad.mode` 不是 `buttons`）、`leftx+`/`lefttrigger-` 等半轴按钮。
- 别名：`a b x y`（south east west north）、`lb rb`、`l3 r3`、`select`（back）。原始轴名：`leftx lefty rightx righty lefttrigger righttrigger`。
- 功能键任何时候只有一个，见 §7.9。

**Joy-Con**：左右两只同时连着就是一个手柄，即合并设备（`joyconpair`），没有开关也没有设置项；只连着一只时它是自己的一个手柄，一律按竖握读（`joyconleft`、`joyconright`）：padmated 初始化 SDL 时设 `SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1`（`input/hints.go`），Joy-Con 2 的驱动也按同样的方式映射。没有横握，没有横握的映射表，也没有摇杆旋转。只有显式的 `--hint SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=0` 或同名环境变量能撤销这个 hint，只用于诊断；这时 padmate 的族、标签和组合仍按竖握的映射。规则对 SDL 会合并的每一种 Joy-Con 都一样，padmate 自己读取的 Joy-Con 2 也照此办理（§7.1.1）。合并设备会换掉单只的设备（§5.4，以及 PITFALLS 的“Joy-Con 合并”），所以按在一只 Joy-Con 上的键，在另一只连上时、或合并设备里一只断开时被释放，映射在新设备上继续。`--hint SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0` 让它们保持分开，只用于诊断（它只影响 SDL 合并的 Joy-Con，不影响 Joy-Con 2）。键映射由 SDL 3.4.16 决定（Switch 1；Joy-Con 2 不由 SDL 读取，见 §7.1.1；其他 Switch 2 手柄不支持）：

- 单只 Joy-Con，竖握。左：方向键是 `dpad.*`，L/ZL 是 leftshoulder/lt，− 是 back，Capture 是 misc1，SL/SR 是 paddle2/paddle4，摇杆是 left 摇杆（按下是 leftstick）。右：面键是 south/east/west/north，印的是 B/A/Y/X（和一对一样），R/ZR 是 rightshoulder/rt，+ 是 start，Home 是 guide，SL/SR 是 paddle3/paddle1，摇杆是 right 摇杆（按下是 rightstick）。左手那只没有面键。ZL、ZR 是扳机轴，所以 `lt`、`rt` 是它们的虚拟按钮（见上）。
- 一对与标准手柄相同，另有 misc1（截图键）和 paddle1–4，hint 对它无效。界面里一对的手柄图不显示 paddle1–4，因为两只合在一起时按不到 SL 和 SR（见渲染进程一节的映射页）；守护进程照常读它们。
- 没有 misc2..6（Joy-Con 2 有 misc2，§7.1.1）、touchpad 和 `sl`/`sr` 别名。手柄的族（`padfamily`：它可选的功能键和内置组合，§10.4）只由 `DeviceInfo.Type` 得出（`input.FamilyOf`）：`joyconleft`、`joyconright`、`joyconpair`，其他类型都是标准布局。`padfamily.Family.HasButton` 说明单只 Joy-Con 有哪些键（见上面的列表），config.json 的检查用它（§10.4）。紧急组合（§5）：单只左 paddle2+paddle4（SL+SR），单只右 paddle3+paddle1（SL+SR），合并设备 back+start。

### 7.1.1 Joy-Con 2

Switch 2 的 Joy-Con，由 padmate 自己经蓝牙 LE 读取（§5.3.1，[手柄输入](adr/0002-input-sdl.zh-CN.md)）。控件名和上面的 Joy-Con 相同，另加右手那只 C 键的 `misc2`。映射（`jc2/mapping.go`）有两种模式，hub 给每个手柄选一种：

- **单只**，单只 Joy-Con 2，一律竖握（`jc2/mapping.go` 里的 `Vertical`；和 SDL 在竖握 hint 下对 Switch 1 Joy-Con 的映射相同）。左：方向键是 dpad.*，L 是 leftshoulder，ZL 是 lefttrigger（0 或 32767），− 是 back，Capture 是 misc1，SL/SR 是 paddle2/paddle4，摇杆是 left 摇杆。右：A/B/X/Y 是 east/south/north/west，R 是 rightshoulder，ZR 是 righttrigger，+ 是 start，Home 是 guide，C 是 misc2，SR/SL 是 paddle1/paddle3，摇杆是 right 摇杆。
- **一对**：两张单只表的并集，left 摇杆来自左 Joy-Con，right 摇杆来自右 Joy-Con。自动组成（见下）。紧急组合按 §7.1：paddle2+paddle4（单只左）、paddle3+paddle1（单只右）、back+start（一对）。
- guide 不能映射（§7.9），所以 Home 键用不上，和 Switch 1 Joy-Con 一样。握把上的键（GL/GR）和运动传感器不读。
- **摇杆。** 报告里的摇杆是两个 12 位数。hub 用手柄自己的校准把它们换成 −1..1；校准在连接时从 flash 读：有用户校准用用户的，否则用出厂的（中心和各方向的行程），都没有就用中心 2048、行程 1200。轴按 SDL 的约定（y 向下，扳机 0..32767）。摇杆静止时不正好在中心（见 PITFALLS），所以摇杆的死区要盖住这个偏差。

**设备身份。** `DeviceInfo.Driver` 是 `ble`。Vendor 是 `0x057e`；Product 是 `0x2067`（左）、`0x2066`（右）或 `0x2068`（一对，padmate 自己定的号）；`Type` 是 `joyconleft`、`joyconright` 或 `joyconpair`；名字是 `Joy-Con 2 (L)`、`Joy-Con 2 (R)` 或 `Joy-Con 2 pair`。GUID 按 SDL 的布局（§5.4）：前两字节是总线类型 `0x0005`（蓝牙），没有名字 CRC，接着是 vendor 和 product，驱动签名 `b` 在第 14 字节（padmate 自己的签名，`guid.go` 解码为 `ble`）。同一型号的所有 Joy-Con 2 GUID 相同，靠序列号区分。所以 `DeviceKey` 是 `{GUID, Serial}`：`Serial` 是 flash `0x13002` 起的 14 个 ASCII 字节，一对则是两个序列号用 `+` 连起来（左在前），因此手柄每次连接 key 都不变，和它的蓝牙地址无关（地址只在 `Path` 里，`ble:<地址>`）。`DeviceID` 从 `0x40000001` 往上数，这一段和 SDL 的（从 1 起）、接收器别名的（从 2³²−1 往下）、测试假 id 的（`0x7ff00001` 附近）都不相遇。每次报告手柄新增都换一个新的。

**一对与单只。** hub 自己用已连接的手柄组成 gamepad，没有开关：按连接先后，第一只左和第一只右 Joy-Con 2 合成一对，其余每只是单只。这和 SDL 对 Switch 1 Joy-Con 的规则一样，是自动的（没有开关，也没有设置项；`SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS` 管不到它）。只有 hub 自己的链路会组成一对：Joy-Con 2 从不和 SDL 报告的 Switch 1 Joy-Con 合并，两种各在自己的后端里配对。集合变化时，不再合适的手柄先报告移除（它的消费者松开一切），再报告新的新增；所以一对里一只离开，另一只就以新的 `DeviceID` 作为单只回来。静默的链路不算任何手柄（它是 dormant，§5.3.1），所以布局按仍在报告的链路来做：一对里一只静默时，整对报告移除，仍在报告的那一只立刻作为单只新增，用新的 `DeviceID`、自己的 `DeviceKey`，当前按着的控件算新按下；静默的那一只在另一只在线时重新报告，单只报告移除、一对新增，和全新的第二次连接完全一样。有同侧的备用 Joy-Con 2 时，规则按连接先后取每一侧的第一只，所以留下的那只可能和备用的那只配成一对。

**电量。** 每条报告的第一个字节带电量等级 0–9、充电位和外接电源位。hub 把等级换成十分之一步长的百分比，充电为 `charging`，外接电源且没在充电为 `wired`，否则 `discharging`，作为 `DevicePower` 事件和电量查询的结果发出（连接方式 `wireless`）。一对取两只中较低的。手柄的每条链路都发过报告之前，不发百分比。玩家编号取 0–7 中没有在线链路用着的最小值，手柄上的灯显示的也是它。

**Joy-Con 2 的 `DeviceID` 上，各个按设备的方法做什么。** SDL 不认识它，所以对它从不调用 SDL（`Source.isJoyCon2`）：

- 效果、灯和触觉提示什么都不做，也不返回错误：padmate 不驱动这些手柄的震动和灯（灯显示玩家编号，连接时设一次）。所以 `devices.identify` 没有动静。
- 映射类调用（读取、设置或重置 SDL 映射）和原始事件流（`devices.rawInfo`）返回 `input.ErrUnsupported`：没有 SDL 映射可改，Joy-Con 2 又总是已知的手柄，映射向导无事可做。
- 电量和玩家编号来自报告（见上）；睡眠后 `Reenumerate` 把手柄的状态带回来（§5.3.1）。
- Joy-Con 2 自己的方法（`JoyCon2Pair`、`JoyCon2CancelPair`、`JoyCon2Forget`、`JoyCon2Disconnect`、`JoyCon2Status` 等）就是 IPC 的 `joycon2.*`（§11）；没有这个后端时（不是 Windows、传输没起来、假输入）返回 `ErrJoyCon2Unavailable`。

### 7.2 手势识别

每个控件一个状态机：`Idle → Pending → (Tap 计数 → WaitMore) | Holding`。功能键不在其内。

- **等待时间**按冻结的可达注册算：只有 press/hold 绑定时立即处理；有 tap 与 hold 时等 `holdMs`；有多击时还等 `multiTapMs`。
- **打断策略**自动选：hold 动作是层操作、精确模式、按键提示或纯修饰键时 `holdPreferred`，否则 `balanced`；绑定可设 `timeOnly`。`holdPreferred`：Pending 时有新按下，先把它判为 hold 并执行，再对层栈拍快照分发新按下。`balanced`/`timeOnly`：缓冲打断事件，判定后按原顺序重放（§7.7）。
- 任何按下都结束 WaitMore 窗口：先提交已有 tap 计数，再处理新按下。

### 7.3 解析顺序

对每个（控件，手势），按序找第一个接收者：

1. 脚本 `global()`。
2. 功能键按着时 profile 的 `functionSet`（该控件没绑定就往下）。
3. 叠加层从上到下：每层先脚本 `layer(L)`，再声明式 L。
4. base：先不带作用域的脚本处理器，再声明式 base。
5. common：先脚本 `layer("common")`，再声明式 common。

`block` 消费但不产生动作；`e.pass()` 交给下一级；`claim()` 整个控件归脚本。

### 7.4 层

- 层栈 = common（底）+ base + overlays。`layer` 动作：`hold`（松开撤销 overlay）、`toggle`、`switch`（替换 base）、`cycle`（在 base 名列表里循环）。`defaultLayer` 指定启动 base，缺省 `base`。`switch`/`cycle`/`toggle` 在按下时生效，“松开时切换”写成 `release` 手势加层动作。
- 查找从上到下，`block:true` 消费并停止。**press-owner**：按下时保存层名、绑定和动作，松开交给按下时所在的层；按下时已按住的控件在新层中被屏蔽直到松开。锁存按（设备，物理控件）记录。
- `functionSet` 不是层：没有切层脉冲、颜色和模式编号，不进 `layers.active`、`LayerChange`、state.json 和浮层层提示。
- **CancelAll**：结束所有按压周期；清除 toggle、连发、序列、待定手势、缓冲、多击窗口、锁存和所有 overlay（base 不变）；之后由 Arbiter 释放按键。控件在引擎看来仍按着：松开无输出，扳机要先回到释放阈值以下才能再按。
- **层状态持久化**：`PersistentLayers` 给出 base、已 toggle 的层和 cycle 序号（不含 hold 层），`RestoreLayers` 只在取消/复位边界执行，不产生按键。`slot.LayerMemory` 按（设备，profile）在内存保存：切换配置和断连在复位前保存，正常退出在 ReleaseAll 前冻结；磁盘上是 state.json（§10.1）。

**反馈接缝**（`LayerChange`、`Feedback`）：引擎发布当前 base、overlays、颜色、切层脉冲和暂停状态；时长和强度在 profile 顶层 `feedback`。开关（`rumble`、`led`、`pause`，默认开）是每只手柄自己的设置（ADR-0017：`devices.<DeviceKey>.feedback`，没有则取退役的顶层 `feedback`），经 `Options.Switches` 读，slot 把它绑在自己手柄的 DeviceKey 上（nil 为全开，在 slot 的 goroutine 上调用，不阻塞），所以一只手柄的开关不会影响另一只；`pause` 关掉时暂停/恢复不给脉冲、不显示 `pausedColor`。`Feedback.Mode` 是层脉冲次数：声明了循环按循环位置，否则按层名排序，base 为 1。实机经 `Source.SetFeedback`（T-input）到效果混音器（`internal/haptic`，§5）。

**触感接缝**：`Options.Haptic` 按值传递，零分配；`Binding.Haptic` 是预设（0 为无）。`runAction` 是声明式手势的唯一分发点：决策之后、动作之前调一次 `playHaptic`，同一周期同一时刻的两个手势只响一次；连发、`block` 绑定、脚本接管的手势、暂停中、`strength` 为 0 或全局震动关着时不响。旋钮刻度在派发时响（绑定没有 `haptic` 且该摇杆 `knobTick` 时用 `haptic.Tick`）。下游经 `effectQueue.postEffect`（永不阻塞，§5）。

### 7.5 模拟量模式表

| 模式 | 行为 |
|---|---|
| standard | 进入对角扇区时，相邻的两个正方向同时触发 |
| eightWay | 只触发对角控件；没绑定时不发，除非 `diagonalFallback:"cardinals"` |
| fourWay | 只有 4 个方向，扇区各 90° |
| fourWayDiagonal | 只有 4 个对角方向 |
| knob | 虚拟旋钮：每转 `knobStepDeg` 发一格 `ls.cw`/`ls.ccw`；不发方向；`ls.active` 和内外圈照常 |
| mouse | 摇杆移动指针：不发方向和刻度；`ls.active` 和内外圈照常；一推出死区指针就动 |

界面只提供 fourWay、knob、mouse，十字键在界面里没有设置；文件里别的模式照常生效。摇杆的可选 `label` 只给界面显示，引擎不读。摇杆先做径向死区和外圈饱和，再按 `directionThreshold` 判方向；`directionDelayMs` 延迟方向变化，`hysteresisDeg` 防抖；`ls.active`、内外圈与方向叠加触发，不被 `directionDelayMs` 延迟。半轴按钮用 `analog.axisThreshold`，与方向独立。

- **每设备调校**：死区和外圈（`saturation`）按手柄覆盖，顺序为 config.json 的 `devices.<DeviceKey>.calibration`、profile 的 `analog.ls/rs`、内置默认；叠加后须 0 ≤ deadzone < saturation ≤ 1，否则这根摇杆的调校被忽略并记 warn。调校改变时不做 CancelAll：像 Reload 一样 `settleVirtual`，不再成立的方向/内外圈走正常释放路径松开，新成立的等摇杆回中再推才按。
- **内外圈**：`norm ≥ outerThreshold` 进入外圈，`norm < outerThreshold − 0.05` 或不再 active 时离开。`ls.outer` 在外圈内按下，`ls.inner` 在 active 且不在外圈时按下。只在有绑定（或脚本在监听）时才按下。
- **虚拟旋钮**：角度 0° 向上，顺时针增加。两个轴是相隔微秒的两个输入，所以轴输入只设 `knobEvalAt = T + KnobSettle`（4 ms），到点后按此刻两个轴读。active 时以当前角度为基准 `ref`，不出刻度；与 `ref` 的最短角差达到 `knobStepDeg` 就排一格并移动 `ref`；离开 active 区忘掉 `ref`，所以松手回中不出刻度。限速：两格之间至少 max(tapPressMs) + max(tapGapMs)（默认 40 ms），不补发。一格 = 同一时刻对刻度控件做一次按下和抬起，没有绑定（也没有脚本）时静默丢弃。
- **两段扳机**：`lt.full` 的滞回作用在原始值上，达到 `fullPress` 按下，降到 `fullRelease` 松开；它按着时 `lt` 一直按着。没绑定时只记录状态。
- **速率调制连发与曲线**：`repeat.minRateHz` 让连发速率随推动深度从 `minRateHz` 变到 `rateHz`（缺省 12），周期不小于 `repeatMinimum`；`analog.*.curve`（`linear`、`quadratic`、`cubic`）只改深度，不移动阈值，不改脚本读到的值。只有摇杆或扳机派生的控件可用 `minRateHz`，且不得大于 `rateHz`。
- **摇杆移动鼠标**（`engine/stick_mouse.go`，纯函数，不分配）：目标速度 = 方向单位向量 × `mouseSpeed` × `curve(depth)` × 精确系数。按时钟积分，不按 tick：每次轴输入、tick、系数变化时先把上次到此刻的时间（上限 50 ms）按旧速度积进累加器再重算；`mouseSmoothingMs` 不为 0 时速度是目标的指数移动平均。`MouseTick` = 8 ms，到点经 `Output.Move` 发整数部分，晚了不补发。
  - **安全边界**：暂停时不排 tick。`CancelAll` 清掉全部鼠标状态，并给仍推在死区外的摇杆上锁，回到死区内才解锁；恢复暂停后要有下一次轴输入才继续。`Reload` 和校准变化不是安全边界：不清状态，按旧目标积到此刻再按新设置重算。
  - 精确模式（`op:"precision"`，`factor` 0.05–1，默认 0.3）在手势按住期间降低所有鼠标摇杆的速度，只能用在 `press`、`hold`；跳转（`op:"jump"`）经 `Output.Jump` 把指针放到虚拟桌面的 `x`、`y`（0–1）。

### 7.6 默认参数

| 参数 | 默认值 |
|---|---|
| holdMs / multiTapMs | 250 / 220 ms |
| tapPressMs / tapGapMs | 20 / 20 ms（同键最大速率 25 Hz） |
| repeat | 延迟 350 ms，12 Hz；`repeatMinimum` 40 ms |
| 扳机阈值 | 按下 0.30（原始 9830），释放 0.20（6553） |
| directionThreshold / axisThreshold | 0.30 / 0.30 |
| 死区 / 饱和半径 | 0.15 / 0.95 |
| 对角宽度 / `hysteresisDeg` | 45° / 8° |
| `outerThreshold` | 0.90（离开滞回固定 0.05） |
| `mouseSpeed` | 1200 px/s（满推），范围 50–10000 |

### 7.7 实现（`internal/engine`）

`Compile` 把解析后的 profile 编成不可变的 `Program`，多个 Engine 共用；`timing` 可在 profile 或 binding 上覆盖 `holdMs`、`multiTapMs`、`tapPressMs`、`tapGapMs`，显式 0 也保留。`NextDeadline` 和 `Apply` 驱动 hold、多击、repeat 定时器。关闭 `Options.Trace` 时 Step、Apply、CancelAll、Reset、Pause 零分配。

**动作规则**：`profile.Parse`、`Compile`、`engine.CheckBinding` 检查同样的规则，所以 `Parse` 返回的模型总能编译。

- release、tap、doubleTap、tripleTap、quadTap 在松开之后才执行，没有可按住的对象：keys 动作只能 tap 模式、不能带 repeat，层动作不能 `op:"hold"`。这保证不会留下没人结束的按住（不变量 1）。
- KeySpec 数组（键序列）只能 tap 模式、不能带 repeat；press/hold 不写 mode 时默认 hold，所以数组要写 `"mode":"tap"`。repeat 的 `style`：`typematic` 用 hold 模式，`tap` 用 tap 模式，不写则跟随 mode。
- 主键是鼠标按键的 keys 动作不能带 typematic 连发（按住时再按会被应用当作双击），IPC 保存（`profilelib.Put`）同样拒绝；tap 风格连发可用。
- mouse、HUD 动作不按键，没有 `mode`、`repeat`。sequence 与 text 动作同样不按住任何东西，每个手势都能用；`steps` 过 `keys.ValidateSteps`，文字过 `keys.ValidateText`。触发时引擎调一次 `Output.Sequence` 或 `TypeText`；引擎不计时，排队、等待、字符间隔和取消都在 Arbiter（§8.2）。

**打断缓冲与重放**：

- `timeOnly` 和 `balanced` 把打断的按钮边沿缓冲成队列，Pending 判定后按原顺序重放。drain 在缓冲里先找到 Pending 控件自己的松开时判为 tap，会打开多击窗口时不执行 tap 动作；这个松开留在缓冲原位，重放到它时才抬起 press 动作按住的键和层、执行 release 动作。
- 连发在物理松开时就停止；重放的按下若自己的松开已在缓冲里，不登记连发。重放的按下按原始时间算 holdMs 截止，已过就判 hold，除非自己的松开也在缓冲里且早于截止。

**多击**：达到绑定声明的最大次数时立即提交；窗口到期，或之后到来的其他控件按下时，提交已有计数。窗口从松开的原始时间起算 `multiTapMs`。

**按压周期与 press-owner**：每次按下都开始一个周期（无绑定也是），周期号每个 Engine 一个计数器，不复用。输出的 owner 是 `decl/<cycle>`，slot 再加 slot 号和 profile 代数。hold mode 按下时 `Down`，松开时 `Up` 按下时记下的组合，不重新查绑定；tap mode 按下时 `Tap`，松开什么也不做。

**生命周期**：

- **CancelAll(now)**（ReleaseAll 的引擎一级，见 §7.4）：按着且未取消的控件，周期标为已取消，记下的组合丢掉；锁存的组合自己发 `Up`，其余由调用者让 Arbiter 释放。连续第二次什么也不做。
- **Reload(now, prog)**：先 CancelAll 再换 prog；按着的控件松开时决策带旧绑定、reason `canceled`、不发键。**Reset(now)**：先 CancelAll，再把所有控件（含功能键）当作松开，用于设备断开。
- **Pause / Resume**：Pause 先 CancelAll 再进入暂停。暂停时的按下照常开始周期，但立即算已取消：决策 reason `paused`，不发键，松开是 `canceled`。Resume 不发键、不决策，按着的控件松开再按才工作。Reset、Reload、CancelAll 都保持暂停。

### 7.8 决策记录

设置了 `Options.Trace` 时，每个按钮边沿、越过阈值的扳机值、被忽略的输入和 CancelAll 结束的每个周期，产出一条 `Decision{T, Control, Edge, Cycle, Gesture, Level, Binding, Action, Owner, Reason}`。摇杆轴和未越过阈值的扳机值没有；Trace 为 nil 时不构造。

| reason | 情况 | 输出 |
|---|---|---|
| `dispatch` | 按下找到了动作 | hold 发 Down，tap 发 Tap |
| `unbound` | 无绑定，或绑定没有这个手势的动作；多击窗口结束时这一档点按数没有动作 | 无 |
| `release` / `end` | 松开持有组合 / 不持有组合的周期 | Up / 无 |
| `cancel` | CancelAll 结束一个周期，或丢弃一个等多击的窗口 | 无，由 Arbiter 释放 |
| `canceled` | 松开一个已取消的周期（含暂停时的按下） | 无 |
| `spurious` / `paused` / `blocked` | 忽略的输入 / 暂停时的按下 / `block` 绑定消费了按下 | 无 |
| `function` | 功能键的边沿（无周期，cycle 为 0） | 无 |
| `script` | 脚本拿走了手势，或消费了一个还在等的按下的松开 | 无（脚本自己发的键不归到这条） |

**决策和物理边沿不是一一对应**。读决策的人（诊断页，[ADR：诊断](adr/0016-diagnostics.zh-CN.md)）按 gesture 和 reason 判断，不按 edge：还在等判定的按下没有决策；点按先 `unbound` 后 `dispatch`；长按、点按、多击、松开动作的 `dispatch` 的 edge 总是 `down`。

**脚本的决策**：有脚本手势注册表时，`resolveGesture` 找不到动作，`runAction` 记一条：脚本接了或 claim 了记 `script`；某一级 `block` 拦下记 `blocked`；每一级都放过记 `unbound`，其 binding 是合成的 `script-resolution`，padmated 发布时去掉它并用来判断“还在等”（§11）。

**字段与顺序**：owner 只在发了输出时有；找到绑定才有 level、binding；function 决策的 level 是 `function:<功能键>`，binding 是 `/functionSet/<控件>`。决策在它引起的输出之前，cancel 在 Arbiter 释放之前；Trace 在引擎的 goroutine 上调用，不能回调 Engine 或 Slot。JSON 一行一个对象，空字段省略。

### 7.9 功能键

任何时候只有一个功能键，像笔记本的 Fn：自己不发键，按住时其他控件先用 profile 的 `functionSet`，没设的照常用平时的绑定。它是每只手柄自己的设置（ADR-0017，§10.4），对所有配置一样。手柄能选什么、默认是什么，看它属于哪一族：标准布局的手柄是 `start`（默认）或 `back`；单只左 Joy-Con 是 ZL（`lt`，默认）或 L（`leftshoulder`），单只右 Joy-Con 是 ZR（`rt`，默认）或 R（`rightshoulder`）；一对 Joy-Con 是 `back`（默认）或 `start`。单只 Joy-Con 不提供 − 和 +，背键在任何手柄上都不是功能键。实现在 `engine/function_keys.go`，设计见 [ADR：映射模型](adr/0012-mapping-model.zh-CN.md)。

**按手柄设置（ADR-0017）。** config.json 里有每只手柄自己的功能键 `devices.<DeviceKey>.functionKey`（§10.4），`Config.PadSettings` 按手柄家族解析。运行时按 slot 而不是按 profile 跟随它：padmated 每份 profile 只用默认功能键（`start`）编译一次，每个 slot 运行这个 Program 里带有自己手柄功能键的变体（`Program.ForFunctionKey`，第一次有 slot 要时才编译，之后共用；slot 在得到或失去手柄、每次重载和 `Supervisor.RefreshFunctionKeys` 时向 `Config.FunctionKey` 要，§5.6、§5.8）。引擎能用 back、start、leftshoulder、rightshoulder、lt、rt 编译（`control.CanBeFunctionKey`）；退役的顶层 `functionKey`（back 或 start）仍是标准布局、自己没设的手柄的功能键。改一只手柄的功能键（`pads.set`、config.json、Joy-Con 并成一对、slot 换了别的手柄）只是对那个 slot 做引擎 `Reload`，如下面所说先取消按着的、释放它的 owner；profile 不重载，别的 slot 不受影响。

- **编译选项**：`engine.Compile(p, engine.WithFunctionKey(c))` 和 `Program.ForFunctionKey(c)`。引擎不读 config.json；padmated 用默认值编译，各 slot 去要变体；不能做功能键的 `c` 按 `start`。`View.ProgramFunctionKey` 是引擎运行的 Program 的功能键（`profiles.explain` 按 slot 显示）。
- **功能键自己不能绑定**：写在功能键上的绑定照常解析、保留在文件里，但 `Compile` 忽略它们；功能键换了它们又生效。扳机（`lt`、`rt`）做功能键时，它是扳机的虚拟按钮，按 profile 的扳机阈值按下和松开（`analog.lt/rt` 的 `press`、`release`，带迟滞）；它的第二段（`lt.full`、`rt.full`）和半轴（`lefttrigger+`、`righttrigger+`）是同一个物理扳机，永远不会被按下，上面的绑定也同样被忽略（`Program.onFunctionKey`）。这些被忽略的绑定只是免于编译检查：它们仍留在层里，`lookupWith` 直接读功能集，脚本的按钮处理器还会让每个虚拟控件都显得“有绑定”，所以防护要写在引擎自己的按下路径里（PITFALLS）。功能集的层名是 `function:<控件>`：`function:lt`、`function:rt`、`function:leftshoulder`、`function:rightshoulder`、`function:start`、`function:back`。老文件里 `back`、`start` 上的绑定从没被读过，可能不合法：格式冻结，有效的 profile 永远有效，所以它们的错误降为警告并从模型拿掉；`functionSet` 里的不合法照常是错误。
- **`guide` 永不映射**（[ADR：映射模型](adr/0012-mapping-model.zh-CN.md)）：它不是功能键（`CanBeFunctionKey` 为假，`WithFunctionKey(Guide)` 按 `start` 编译）；`profile.Parse` 把它上面的每条绑定从模型拿掉并给警告（其中的错误降为警告）；手工构造的 profile 里有的，`Compile` 跳过；`lookupWith` 对它什么也不找（`control.Control.Unmappable`），所以 registry 的层也绑不上它。`stepButton` 里它的边沿只更新物理状态就返回，和功能键一样：不开始周期、不进缓冲、不打断待定手势，不给 `Button`、`ButtonObserver` 和脚本处理器；决策是 `unbound` 和 `end`。脚本宿主像拒绝未知控件一样拒绝它（`scriptControl`），`pad.isDown` 读成没按。
- **一套绑定**：`Profile.EffectiveFunctionSet()` 是引擎读的那一套：有顶层 `functionSet` 就用它，否则由旧格式 `functionKeys` 合成；两者都写时用 `functionSet` 并警告。
- **边沿不进手势识别**：功能键边沿在更新物理状态后、缓冲之前交给 `stepFunctionKey` 并返回。所以它不开始周期、不进缓冲、不打断别的控件的待定手势、不发键，脚本处理器和 `ButtonObserver` 收不到它；决策是 `function`。`pad.isDown` 仍读物理状态。
- **状态**：`Engine.fnActive`，经 `View.FunctionKey` 进 slot 快照（`profiles.explain` 用，§11）。CancelAll、Pause、Resume 和同一功能键的 Reload 不动它；Reset 清掉它。**换功能键的 Reload** 也清掉它（先 CancelAll）：新功能键即使这时按着也要等下一次按下才选 `functionSet`。
- **查找**：功能键按着且 `functionSet` 绑了该控件时，那一套的绑定位置在脚本 `global()` 之后、层之前。一套里的绑定与层里的完全一样：拥有所有手势（没有该手势的动作就什么也不做，不落到下面的层），`block` 消费。一套里的 `script` 动作 scope 是 `"global"`。

## 8. 输出

### 8.1 KeySpec

实现在 `internal/keys`：纯 Go，只用标准库，可在 Linux 上测试。

```
KeySpec  := Chord
Chord    := (Modifier "+")* Key
Modifier := Ctrl|LCtrl|RCtrl|Shift|LShift|RShift|Alt|LAlt|RAlt|Win|LWin|RWin
Key      := 命名键 | 字符 | 物理键 | 扫描码 | 鼠标键 | 媒体键 | Modifier
字符     := A–Z | 0–9 | - = [ ] ; ' ` \ , . / | "Plus"
物理键   := "Key" 主键区字符           // KeyZ、Key1；固定 US 位置，没有 KeyPlus
扫描码   := "sc:0x" hex{1,4}           // 0x01–0x7F，或 E0 扩展键 0xE001–0xE07F
鼠标键   := WheelUp|WheelDown|WheelLeft|WheelRight | MouseLeft|MouseRight|MouseMiddle|MouseX1|MouseX2
媒体键   := VolumeUp|VolumeDown|VolumeMute|MediaPlayPause|MediaNext|MediaPrevious|MediaStop
```

完整键名、别名、扫描码和 VK 用 `padmatectl keys list` 查；`padmatectl keys parse [--json] <spec>...` 输出规范形式并在出错的 token 下画 `^`。

- 字符键按前台布局解析；物理键固定为 US 位置；扫描码原样发送，永远不是修饰键。Arbiter 先用 `Chord.Sided()` 把不分左右的修饰键归一为左键，再按物理键计数。`Z`、`KeyZ`、`sc:0x2C` 是三个不同的 `keys.Key`。
- 鼠标键没有扫描码、VK 和布局：滚轮按下滚一格，松开不发；按键像键一样有按住状态。媒体键是键盘键，有 E0 扫描码和 VK，按 VK 发。两者不能当修饰键，只能在 chord 末尾；鼠标按键不能配 typematic 连发（§7.7）。新键追加在键表后面，不改 schemaVersion。
- 字符键的字符由 `Key.Char()` 给出。Resolver 要用 `Char()` 查布局，不能用 `String()`：大写字母在 Windows 上解析为 Shift 加该键，`Ctrl+Z` 会变成 Ctrl+Shift+Z。需要 Shift 才能打出的字符（`! @ #` 等）不是键，解析时报错并提示写成 `Shift+1`。
- **US 基准映射**（`Key.US()`）：Pause 无单个扫描码，按 VK_PAUSE 发；NumLock 的 0x45 在按住 Ctrl 时变成 VK_PAUSE；小键盘扫描码 0x47–0x53 随系统 NumLock 翻译，所以 Num0–Num9、NumDecimal 按 VK 发（§8.3）；vkE8 不在键表里，只由 MenuMask 注入（§8.5）。

**布局**（`internal/output/layout*.go`，[ADR：键盘与鼠标输出](adr/0004-output-sendinput.zh-CN.md)）：`Resolver.Resolve` 用作 `ArbiterOptions.Resolve`，只在 Arbiter 的 goroutine 上调用；实机（演练与注入一样）用它，重放用 `ChordStroke`（US）。

- **回退**：布局给不出能用的键（没有这个 VK 或字符、扫描码为 0/E1/超范围/不带 E0 的小键盘码、读不到前台布局等）时返回 `ChordStroke` 的结果，不报错，每个（spec, HKL）警告一次（padmated 记 warn `key sent as its US key`）。只有无效的组合返回错误。

**文本输入**：`KEYEVENTF_UNICODE` 只用于文本。文本不是 KeySpec，是 `text` 动作、序列的 `{"text":…}` 步骤（§10.3）和脚本 `key.type` 的参数。`keys.ValidateText`：1–256 个 UTF-16 单元，合法 UTF-8，除 `\n`、`\t`（各一次 Enter/Tab 点按）外无控制字符。`padmatectl keyboard-capture`按 VK 在键表里找键，捕获钩子从不吞实体键。

### 8.2 Output Arbiter

实现在 `internal/output`，可跨平台。踩坑见 PITFALLS“键盘输出与布局”。

**基本约定**

- **owner**：`Owner{Slot, Gen, Source, Cycle}`，格式 `slot/profileGen/source/cycle`（如 `0/3/decl/17`）。Source 是 `decl` 或 `script`，无效的 owner 被所有命令拒绝。声明式 Cycle 是按压周期；脚本 Lifecycle 为每个 runtime 分配非零 cycle token，保证重载时旧 runtime 的释放不影响新 runtime。
- **单线程**：Arbiter 不加锁，只由一个 goroutine 调用。守护进程里是锁定线程的 `output.Loop`（T-output），其他 goroutine 经 `Do` 或 `RemoteArbiter` 交命令（§5），所以它的 Sender 是唯一的 SendInput 调用者（不变量 3）。
- **命令**：`Down`、`Up`、`Tap`、`Repeat`、`ReleaseOwners`/`ReleaseOwner`/`ReleaseAll`、`Advance`、`NextDeadline`、`Held`、`Stats`、`SetBlocked`、`SetTiming`、`SetEventGap`；非键的 `Move`、`Jump`、`TypeText`、`Sequence`。owner 或组合无效、解析失败时返回错误，什么也不发、不改。
- **脚本接缝**：`script.Host` 只通过 `OutputSink` 调这些命令，带 `SourceScript` owner；生命周期切换时先 `ReleaseOwner`，再让旧 Host 断开 sink，迟到的输出触及不到 Arbiter。
- **物理键**：组合经 `ArbiterOptions.Resolve` 得到按下事件，修饰键在前，主键最后；键总是用按下它的那个事件的抬起来释放，按下后布局变了也一样。物理键流经 MenuMask 再到 Sender（§8.5）。
- **引用计数**：每个 owner 持有一组组合；每个持有引用组合的全部物理键，进行中的 tap 只引用它自己按下的键。键在第一个引用时按下，最后一个引用去掉时抬起。

**Tap 与 Repeat**：Tap 把没按下的键按下，由 tap 引用到结束；无最短按下约束时按下和抬起同一批，否则 `Advance` 在 now+hold 结束引用，物理抬起仍受 MinPress 约束。主键被占着或在 MinGap 内时 tap 排队，后来的点按合并（报告 dropped）。Repeat 只对 owner 用 Down 持有的组合有效，额外发一次主键按下。

**滚轮、鼠标按键、指针**

- **滚轮键是瞬时的**：按下滚一格，抬起不发事件（`KeyEvent.Silent`）；Arbiter 仍按键记账，抬起照样交给 Sender，由 Sender 不注入（§8.3）。按下总是发出，没有最短按下和同键间隔，不进 held-keys 日志。
- **鼠标按键像键一样按住**，所以每个退出路径都会抬起它（不变量 1）。**按下着的按键不会再收到按下**（应用会当作双击的一半）：`Repeat` 对它什么也不发；被 `Down` 持有时，别的绑定对它的 tap 是空操作（带修饰键的组合整个 tap 都空操作，因为单独按 Shift 会切换输入法、Alt 会激活菜单栏）。
- **指针移动不是键**（`pointer.go`）：`Move` 相对移动（系统指针速度仍作用，padmate 不改），`Jump` 放到 0–65535 虚拟桌面坐标。没有持有，不受 MinPress/MinGap 管，ledger、held-keys 日志、MenuMask 都不记；与键事件保持顺序，同 owner 的排队移动合并；闸门关着时丢弃，`ReleaseOwners` 丢掉排队的移动。Send 失败不重试，错误每秒至多返回一次。

**文本输入不是键**（`arbiter_text.go`）：`TypeText(owner, text, gap)` 用 `KEYEVENTF_UNICODE` 逐字符输入（每个字符一次 Send），不看布局；字符之间隔 `gap`。Unicode 事件不进键表、引用计数、held-keys 日志和启动扫除；`ReleaseOwners` 和 `SetBlocked(true)` 结束匹配 owner 的文本。Send 只发出一部分时，已发出的按下，其抬起立即重发一次，文本就此结束并通知 `Dropped`；抬起再失败时像待重发的键处理（`Arbiter.stuck`）。系统是否为 `VK_PACKET` 记录按住状态没有实测，记着只为遵守不变量 1。

**序列**（`arbiter_sequence.go`）：`Sequence(owner, steps, hold, gap)` 跑一串 `keys.Step`（点按、等待、输入文字；1–64 步），与 `TapSequence` 同为一种 `tapSeq`，owner 一释放整个序列都取消。一个步骤丢了序列继续并通知 `Dropped`。一个 `Sequence` 要等它之前的 `Sequence`（不论 owner）结束才开始，队里最多 16 个，再来的被丢弃。`ReleaseOwners`（所以 `ReleaseAll`、暂停、重载、切换配置、紧急释放、退出）和 `SetBlocked(true)` 结束匹配的序列：没开始的步骤不再发，进行中的点按像单独取消那样释放（不变量 1）。序列从不按住什么。

**释放**（ReleaseAll 的 Arbiter 一级）：`ReleaseOwners(match)` 结束匹配 owner 的持有和进行中的 tap，然后在一批里抬起所有没有引用的键（含待重发的）：先非修饰键，再修饰键，各按按下的逆序。完整路径 `slot.ReleaseAll` 是先 `engine.CancelAll`（§7.4）再 `ReleaseOwners` 匹配同 slot 号、任意代数的 owner。`Slot.Reload` 同样释放，之后的 owner 用新代数。

**多个 slot**：所有 slot 的 owner 在同一个 Arbiter 里，共用一个键盘（[ADR：多手柄](adr/0003-devices-and-slots.zh-CN.md)）。引用计数跨 slot：两个 slot 按住同一个键只按下一次，最后一个引用去掉时才抬起，不管哪个 slot 先松开、被移除、重载或 panic。

**Send 失败**（不变量 1）：状态跟随命令，不跟随 Send 的结果。没发出去的按下仍算按下，最后一个引用去掉时照样发抬起：多余的抬起无害，漏掉的抬起就是卡键。批次只发出一部分时，没发出的抬起立即重发一次；再失败的成为待重发，下一次 `Down`、`Up`、`Tap`、`Repeat`、`Advance` 先重发它们，`ReleaseOwners` 并进自己的批次，`NextDeadline` 在 `RetryInterval`（默认 100 ms）后到期。

**事件间隔**（`pace.go`）：config.json 的 `output.eventGapMs`，0–50 ms，默认 0（有的软件会漏掉和主键在同一次 SendInput 里的 Ctrl/Shift）。大于 0 时所有 owner 的事件按命令顺序进一个队列，每个事件单独一次 Send，与上次至少隔这么久。**释放不等间隔**（不变量 1）：`ReleaseOwners`/`ReleaseAll` 立即一次 Send，队列里的事件在前，然后抬起剩下的键。

**按下的闸门**：`SetBlocked(true)` 关上闸门，本身不发也不释放。关着时有效的 `Down`、`Tap`、`Repeat` 不按下也不持有；`Up`、`ReleaseOwners`、`ReleaseAll`、tap 到期和重发照常，所以闸门不会让键卡住。守护进程经 `output.Loop.BlockFor`（§5）：先 ReleaseAll 再关闸门，暂停（`BlockUser`）和系统挂起（`BlockSystem`）各占一位，都打开时闸门才开。

**held-keys 日志**：让强杀之后的下次启动释放残留键。路径 `%LOCALAPPDATA%\padmate\run\held.bin`（格式见 `internal/output`），只有 `--inject` 用。记键盘键、媒体键和鼠标按键；滚轮、指针移动和 Unicode 事件从不写入。

- **身份**：`GetAsyncKeyState` 分不出注入的和物理按着的键，所以别的开机或登录会话留下的日志不能扫除。日志存写它的那次运行的身份（会话 ID、令牌的登录会话、登录时间），三项全等才算同一个；不同时当作空：重置并写入本次身份，一个键也不释放。
- **先写后发**：ledger 把一批交给 MenuMask 之前，先把这批的按下全部加进位图（SendInput 可能只发出前缀），发完之后改成它记着按住的键；每次写都是整个文件的一次 WriteFile，所以被强杀打断的写也不丢键。写失败时照常发键，这期间被强杀可能漏记；padmated 从不因为日志或扫除出错而拒绝启动。
- **启动扫除**（§5 的“T-output”）：身份相同时，日志里的键与 `GetAsyncKeyState` 报告仍按着的键取交集，经 Loop 自己的 MenuMask 各发一次抬起。**退出**：Stop 和 panic 之后按 ledger 里还按着的键改写日志，没有键就删除。

**ReleaseAll 的触发**：退出、重载和切换 profile、断连、暂停和紧急组合（`Loop.Block(true)`）、锁屏/睡眠/注销/关机/安全桌面（`Loop.BlockFor(BlockSystem, true)`）、脚本卸载（按 owner）；T-output 的 panic 走 ledger 释放（§5）。

**未实现与限制**：提权窗口或安全桌面期间补发释放；padmate.exe 在前台时只演练、不注入；按前台变化释放；IME、Ctrl+Alt+Del、Win+L。单独的 Alt/Win 被屏蔽，要打开菜单绑定 F10 或 Ctrl+Esc（§8.5）。

### 8.3 注入与标记

实现在 `internal/output`，设计见 [ADR：键盘与鼠标输出](adr/0004-output-sendinput.zh-CN.md)。每次 Send 只调一次 SendInput，其他输入插不进这一批；键盘和鼠标事件在同一次调用里按顺序发出，所以 `Ctrl+WheelUp` 的 Ctrl 和滚轮之间插不进别的输入。

| 事件 | 构造 |
|---|---|
| 扫描码事件 | `KEYEVENTF_SCANCODE`，E0 键加 `EXTENDEDKEY` |
| VK 事件 | Pause、Num0–Num9、NumDecimal：按 VK 发，不设 `SCANCODE`，wScan 仍填扫描码 |
| 媒体键 | 按 VK 发，wScan 填 E0 扫描码，带 `EXTENDEDKEY` |
| 菜单屏蔽键 vkE8 | `MenuMaskEvent`，按 VK 发，wScan 填 0 |
| Unicode | `wVk` 为 0，`wScan` 是一个 UTF-16 单元，`KEYEVENTF_UNICODE` |
| 滚轮 | `INPUT_MOUSE`，`WHEEL`/`HWHEEL`，±120，无位置。抬起不产生 INPUT |
| 鼠标按键 | `INPUT_MOUSE`，无位置，在光标处发生；X1、X2 用 `XDOWN`/`XUP`，`mouseData` 为 1 和 2 |
| 指针移动 | `MouseMove`：`MOUSEEVENTF_MOVE` 相对移动。`MouseJump`：`MOVE\|ABSOLUTE\|VIRTUALDESK`，坐标 0–65535 |

**标记**：每个事件的 `dwExtraInfo` 是 `Tag | nonce`：高 32 位是 ASCII "padm"，低 32 位是 nonce（进程 id）。LL 键盘钩子用 `output.IsPadmate`、`Nonce` 分清 padmate 的事件、实体键盘和其他注入程序。LL 鼠标钩子的 `dwExtraInfo` 只有低 32 位（实测），没有 "padm" 标记，只能靠 nonce（`output.MayBePadmateMouse`）。标记可以伪造，只用来过滤。

### 8.4 演练输出

演练打印器是 padmated 在重放和 `--dry-run` 实机下的 `output.Sender`（经 MenuMask，所以打印的屏蔽键与注入时相同）。列、行种类和例子见 `padmated -h` 与 `cmd/padmated/testdata/acceptance.golden`。决策行在它引起的 send 行之前（§7.8）。实机输出随时间和前台布局变化，没有黄金文件，由 `TestLiveVirtualDryRun` 等检查关键行（§14）。

### 8.5 Alt/Win 菜单屏蔽

实现在 `internal/output/mask.go`，可跨平台，设计见 [ADR：键盘与鼠标输出](adr/0004-output-sendinput.zh-CN.md)。

单独按下再松开 Alt 会激活前台程序的菜单栏，单独的 Win 会打开开始菜单。`MenuMask` 是 Sender 包装：松开这样的 Alt/Win 之前，在同一批里紧挨着插入屏蔽键 vkE8（VK 表里未分配）的按下和抬起。它能否挡住菜单待实机验证。

**规则**：菜单键是 LAlt、RAlt、LWin、RWin，每个或干净或脏，开始时干净。

- 按下菜单键 K：K 变干净，其他菜单键变脏。按下其他任何键（含屏蔽键）：所有菜单键变脏。抬起菜单键 K：K 干净就先插入屏蔽，然后 K 变干净；抬起其他键不改状态。只有 Sender 报告发出了的前缀改变状态。
- 屏蔽的有单独的 `Alt`、`Win`、`RAlt`（AltGr 也是）和只有修饰键的组合；`Alt+Tab`、`Win+E`、AltGr 字符不屏蔽。鼠标键和 Unicode 事件不算键，不改状态。

**失败**（不变量 1 优先）：

- 屏蔽键按下了、抬起没发出去：MenuMask 记下它，之后每次 Send 都先抬起它；整个没发出去：重发时重新判断，照样屏蔽。Sender panic：按最坏情况处理（这一批按下的菜单键都算干净，有屏蔽键按下就算它按着）。
- T-output panic 后，ledger 的释放经过 MenuMask（§5）。Arbiter 把按下失败的键也算按着并照发抬起（会被屏蔽），而 ledger 只记发出了的按下，所以屏蔽键可能按着而 ledger 是空的，这时 ledger 调 `releaseMask`：一次 Send，只有屏蔽键的抬起。vkE8 没有字符，按着应无害（没有实测）。

**约束**：

- 守护进程的每条输出路径都要经过恰好一个 MenuMask，且在所有 Arbiter 下面：`output.Loop`、padmated 的重放、simtest 的 `WithMenuMask`（§14）；`padmatectl send` 和 injecttest 直接调 SendInput，不屏蔽。MenuMask 不加锁，与它上面的 Arbiter 同 goroutine；所有 slot 共用一个 Loop，所以跨 slot 按物理键流判断。

## 9. 前台检测

`internal/winfocus` 附着在 winsession 的窗口与线程上（§5.13）。

**检测。** `SetWinEventHook(EVENT_SYSTEM_FOREGROUND, MINIMIZEEND)`，回调只投递消息；防抖 50 ms 后重读实际前台，不用过期的事件 HWND；会话计时器补查漏掉的事件。查询失败或零 HWND 保持上次有效前台。忽略短暂出现的 shell 窗口类和 padmated 自己的窗口。每次前台变化发布 `app.foreground`，非 padmate 程序按路径去重，记入最近列表（最多 12 个）。

**配置选择。**

- 读配置和切换在独立 worker 上串行完成，慢读取结束后若已有新前台就丢弃旧选择。库入口启用自动规则；显式的 `--profile` 保持该文件。自动选择与用户暂停相互独立。
- 选择只看规则（`profile.Select`）：前台程序命中第一条启用、`profile` 为字符串的规则就用那个配置，否则用固定默认配置 `default`；`enabled:false` 保持当前配置；`profile` 为 null 的规则跳过（保留下标，`rules/N` 指针不变）。
- `default` 运行中被删时先重建；仍读不到或不能解析就按内置空配置运行，所以没绑定的软件不沿用上一个配置的映射。已注册的界面进程在前台时保持当前配置、不释放。
- `releaseOnSwitch`：`profileChange`（默认）、`always`、`never`。库 ID 改变（包括切到空映射）总是经 `ReloadProfile` 屏障释放旧 owner，即使是 `never`；ID 不变时仅 `always` 释放。关闭自动选择不释放。
- 每次成功选择发布一个不可变目标，过期的热重载回调无法覆盖新选择。选择、热重载及脚本目录变更由同一把锁串行化；slot 从不等待此锁。

**提权窗口与兼容性提示。** 高完整性前台在本次运行中提示一次，不改变暂停状态。提权窗口或安全桌面拒绝的抬起由 Arbiter 的失败抬起账本重试（默认 100 ms），返回普通桌面后补发失败的释放，不补发失败的按下。兼容性 worker 每秒把 slot 的能力快照与当前绑定比较，缺少的控件提示用户；另每 5 秒只读枚举进程，对 Steam、DS4Windows、HidHide 分别提示一次，不读它们的内部状态。

## 10. 格式

三种用户可见的 JSON 文件：`profile.json`（映射）、`config.json`（设置）、`state.json`（易变状态）。字段和取值范围以 `schema/profile.schema.json`、`schema/config.schema.json`、`schema/state.schema.json` 为准（每个属性有 `description`）。本节只写顶层结构、校验和读写规则。决策见 [ADR：配置文件格式](adr/0007-profile-format.zh-CN.md)。

### 10.1 磁盘布局

目录结构见 §3。数据目录默认 `%APPDATA%\padmate`，`--data-dir` 可换。

**配置库**（`internal/profilelib`）

- `profiles/<id>/profile.json`。ID 是 1–64 位小写 ASCII 字母、数字、`-`、`_`，首位为字母或数字，排除 Windows 设备保留名。
- 读取和热重载经 `os.Root` 限制在 `profiles/` 内，拒绝符号链接和 junction，单个 profile 上限 4 MiB。读取不写盘。
- **revision** 是原始文件字节的 SHA-256，外部编辑也会改变它；IPC 写入用它检查冲突。
- 库里损坏、不可读、版本过新的配置仍有一项并带诊断，供界面修复。

**版本与迁移**

- `profile.CurrentSchema` 是 1。`Parse` 自己先迁移再校验，调用方不用单独调 `Migrate`。版本高于当前的拒绝（`upgrade padmate`）。
- 迁移只在内存里做，**守护进程从不因为文件旧而改写它**：磁盘字节和 revision 保持原样，各种写入（put、copy、restore、导入导出、宏改名）都按字节位置改。
- 格式已冻结：以后只加可选字段；可能弄坏旧文件的改动要加一步迁移、`CurrentSchema` 加一、改 schema 的 `const`、再 `task gen`。config.json 和 state.json 的 `schemaVersion` 是 0。

**保存快照**（`profilelib/history.go`）

- `Put` 用不同的字节替换 profile.json 之前，在同一写锁内把旧字节存为 `profiles/<id>/.history/<UTC 时间>-<SHA-256 前 8 位>.json`（去掉 `.json` 是 version），旁边写临时文件、Sync、rename。
- 与最新快照相同、新建、保存相同字节时不存。每个配置最多 30 个，多的删最旧。存快照失败不影响保存。`.history` 是链接或文件时不跟随。

**导出文件**（`profilelib/archive.go`、`import.go`）

- 一个配置导出成一个 zip（`.padmate`）：根目录 `manifest.json`（`{"type":"padmate-profile","format":1,"name","padmate"}`）和 `profile/`（配置文件夹内容，去掉 `.history`）。profile.json 引用的库里宏连同它们的相对 import 放进 `profile/macros/`。
- 导出在写锁内按复制的限制读入内存（256 项、32 MiB、8 层，链接和特殊文件拒绝），文件不超过 4 MiB，只含脚本、JSON 和文本文件。
- **导入**（`ReadArchive`）在写盘前全部检查：只接受未加密的 store/deflate zip，恰好一个 manifest；拒绝 `profile/` 以外的项、绝对路径、`..`、反斜杠、设备名、特殊项、只差大小写的重名；按解压后的字节计数（zip 炸弹在 32 MiB 停）；脚本和 JSON 必须是 UTF-8；profile.json 通过与 `Put` 相同的校验。
- 脚本放进脚本库：同名同内容复用；同名不同内容又没传 `macros` 时 -32022 且什么都没写；`macros:"replace"` 覆盖，`"keep"` 给导入的脚本取下一个空的“名称 2”并改写引用。
- 新配置的 ID 由名称生成（`NewID`）；名称重名（去首尾空格、NFC、不分大小写）拒绝。`Library.ImportProfile(data, name, unique)` 把转换好的 profile.json 当只有 profile.json 的导出文件走同一路径，`unique` 为真时重名加序号（AntiMicroX 导入用，§11.1）。
- 写法：在隐藏的 `profiles/.tmp-import-*` 写完并 Sync，写锁内再查名称后 rename。替换已有配置时先把旧文件夹 rename 成 `.old-<id>-*`，再 rename 新的，然后旧 profile.json 存成快照、旧 `.history` 移进新文件夹。两次 rename 之间中断时，下一次复制、导入、删除或启动把 `<id>` 不存在的 `.old-<id>-*` 改回原名，已存在的删除。

**脚本库**（宏）

- `<数据目录>/macros/`，与配置无关，所有配置共用。顶层 `.ts`/`.js`（不含 `.d.ts`）各是一个宏，整库最多 64 个（`profile.MaxMacros`）；子文件夹里的模块可被 import，不是宏，import 不得出库。
- **脚本迁移**（`Library.MigrateMacros`）：库模式启动时（拿到实例锁之后，重放不做）把每个配置文件夹里旧式的 `macros/` 搬进库。同名同内容的删除；同名不同内容的取下一个空名并改写该配置的引用。改配置前先写 `<数据目录>/macros-migration-<配置ID>.json`，每一步可重做，被切断的下次启动完成。失败只记日志，不阻止启动。

**默认配置与启动选择**

- 实机模式下 `profiles/default/profile.json` 不存在就写入固定默认配置 `{"schemaVersion":1,"name":"默认","layers":{"base":{}}}`；已有的原样保留。重放只读，缺失或不能解析时用内置空配置（`DefaultProfileText`）。
- **随附配置**（`profilelib.EnsureBundled`，ADR-0008）：`internal/profilelib/bundled/<id>/profile.json` 嵌在 padmated 里。发布版的实机启动（不是 `developmentBuild()`，所以界面的开发守护进程和打包测试模式都不算）对 `<数据目录>\bundled.json`（`{"offered":[…]}`）里还没有的每个 id：库里没有同 id、同名的配置就写入，然后在 config.json 的 rules 末尾加 `{"exe":…,"profile":…}`（字段保持原顺序，缩进写回），除非已有规则的 exe 就是这个文件；本次启动重新读一遍设置。`bundled.json` 解析不了就什么都不提供。现在只有 `csp`（“CSP”），对应 `CLIPStudioPaint.exe`。
- 只给 `--data-dir` 时恢复 state.json 的上次选择；没有记忆、上次是停用或记忆的配置已不在时用 `default`。`--profile-id` 缺失仍启动失败；它只是起始配置，自动选择开着时启动后约 50 ms 的首次前台评估按规则重选（§9）。

**state.json**

- 内容：`lastProfile`、`lastProfileSet` 和每设备每配置的层状态（`devices[DeviceKey].profiles[id]`：`base`、`toggled`、`cycles`）。`lastProfileSet:true` 加空 `lastProfile` 表示上次已禁用。
- `controllers`：连接过的手柄，按规范 DeviceKey 索引：`{name, type, buttonMask?, lastSeen?}`（SDL 给的名字和简短的手柄类型、`DeviceInfo.ButtonMask`、自 Unix 纪元起的毫秒数），最多 64 个，最久没见的先丢。设置页列的就是它（config.json 里只有设置），它是机器状态，只有守护进程写。路由器的 `OnInput` 回调在手柄加入或重新映射时把它标脏；持久化 worker 随后向路由器要设备列表（`Supervisor.Devices`，只有它知道 DeviceKey），在每 500 ms 一次的刷新之前调 `Memory.RememberController`（持锁写 map，无 I/O）。没有这个成员的文件表示一只也没列。
- `joycon2`：记住的 Joy-Con 2（蓝牙 LE），`{addr, serial, side}` 数组（`addr` 为大写 `AA:BB:CC:DD:EE:FF`，`side` 为 `L` 或 `R`，最多 64 个，旧的在前）。这是机器状态，不是设置，只有守护进程写。配对窗口连上一只手柄时在 T-input 上调 `Memory.RememberJoyCon2`（持锁写 map，无 I/O），`joycon2.forget` 调 `Memory.ForgetJoyCon2`，由下面的后台刷新写文件。没有这个成员的文件表示一只也没记。
- `profilelib.Memory` 在内存合并，后台每 500 ms 刷新；退出在 output 停止后最后刷新，最多等 1 s。校验后写临时文件、Sync、rename。

**config.json 的自动修复**（`profile.RepairConfig`、`profilelib.ReadRepairedConfig`）

- 库入口启动时和每次读 config.json 发现错误，先把原字节备份成 `<数据目录>/config.broken-YYYYMMDD-HHMMSS.json`（`O_EXCL`，从不覆盖），再替换。替换失败就删备份，修复后的设置只留在内存，日志只记第一次。
- 保留所有合格的成员；每个错误删去包含它的、有默认值的最小成员；列表出错删整个列表，只有 `rules` 例外（只删出错的那条）。完全解析不了的整个换成 `{"schemaVersion": 0}`。修复后的文件总能解析（`FuzzConfig`）。

**继承**（格式规则见 §10.3）

- 子配置的 profile.json 仍是它自己的文本，保存、revision、快照都按这份文本，从不因为有父配置而改写。
- 库里校验 profile 文本都用 `Library.Parse(id, data)`，所以 `Put` 子配置时父配置要在库里、不成环。写父配置不检查子配置（加载时再报）。`Copy` 保留 `extends`。
- **删除被基于的配置**（`Library.Remove`）：先把直接基于它的每个配置写成合并后的完整内容、不带 `extends`（同一写锁，各存快照）。先全部合并检查，有一个写不成（`*ChildError`：自己有错，或继承了旧式 `script`）就什么都不动；写入中途出错则已写的原字节写回，全有或全无。读不了 profile.json 的配置当作不是子配置。

### 10.2 profile.json 示例

```json
{
  "schemaVersion": 1,
  "name": "绘画",
  "defaultLayer": "base",
  "timing": { "holdMs": 250 },
  "common": {
    "rightstick": { "label": "切模式", "press": { "type": "layer", "layers": ["base", "paint2"], "op": "cycle" } }
  },
  "layers": {
    "base": {
      "south": { "label": "撤销", "press": { "type": "keys", "keys": "Ctrl+Z" } },
      "east":  { "label": "抓手", "press": { "type": "keys", "keys": "Space", "mode": "hold" } },
      "west":  { "label": "笔/橡皮", "tap": { "type": "keys", "keys": "F13" }, "hold": { "type": "keys", "keys": "F14" } },
      "north": { "label": "下一笔刷", "tap": { "type": "script", "name": "cycleBrush", "args": { "dir": 1 } } },
      "leftshoulder": { "label": "Alt 层", "press": { "type": "layer", "layer": "alt", "op": "hold" } },
      "misc1": { "block": true }
    },
    "paint2": { "south": { "press": { "type": "keys", "keys": "F15" } } },
    "alt":    { "south": { "label": "重做", "press": { "type": "keys", "keys": "Ctrl+Shift+Z" } } }
  },
  "functionSet": {
    "south": { "label": "重做", "press": { "type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap" } },
    "ls.cw": { "press": { "type": "keys", "keys": "Ctrl+]", "mode": "tap" } }
  },
  "analog": {
    "ls":   { "mode": "fourWay", "deadzone": 0.15, "directionThreshold": 0.30, "directionDelayMs": 0 },
    "rs":   { "mode": "standard", "deadzone": 0.15 },
    "dpad": { "mode": "buttons" },
    "lt":   { "press": 0.30, "release": 0.20 }
  },
  "script": { "entry": "main.ts", "api": "0.21" }
}
```

`$schema` 只供编辑器找 schema，守护进程忽略。更多示例见 [examples/profiles/](../examples/profiles/)。

### 10.3 绑定语法

**顶层成员**：必填 `schemaVersion`、`name`、`layers`（必须有 `base`）。可选：`extends`、`defaultLayer`、`controlNames`（退役：照常接受和检查，谁也不读，界面保存时去掉；见 [ADR：编辑界面](adr/0013-editor-ui.zh-CN.md)）、`timing`、`analog`、`feedback`、`script`（`entry`、`api`、`params`、`tickHz`）、`common`（所有层共用的底层）、`functionSet`（按住功能键时的一套，§7.9）、`functionKeys`（旧格式）。

**层**：控件名（§7.1）到绑定的映射，另有可选 `color`。别名和标准名不能在同一层同时出现。层动作的目标必须存在。

```
Binding := { label?, block?: true, press?, release?, tap?, doubleTap?, tripleTap?, quadTap?, hold?,
             timing?, policy?: "holdPreferred"|"balanced"|"timeOnly", latch?: true, haptic?: <预设名> }
Action  := {type:"keys", keys: KeySpec|KeySpec[], mode?: "tap"|"hold", repeat?: {...}}
         | {type:"layer", layer, op: "hold"|"toggle"|"switch"} | {type:"layer", layers, op:"cycle"}
         | {type:"script", name, args?} | {type:"profile", profile} | {type:"macro", file}
         | {type:"mouse", op:"precision", factor?} | {type:"mouse", op:"jump", x, y}
         | {type:"hud", op:"hints"}
         | {type:"sequence", steps: Step[]} | {type:"text", text}
Step    := {keys: KeySpec} | {wait: 1..10000} | {text}      // 一步一个成员，1 至 64 步
```

手势与动作的组合（规则见 §7.7）：

| 手势 | 默认 mode | keys 动作 | 层动作 |
|---|---|---|---|
| press、hold | hold | hold 或 tap；KeySpec 数组只配 tap 且不带 repeat | 全部 op |
| tap / doubleTap / tripleTap / quadTap、release | tap | 只能 tap，不带 repeat | `hold` 以外 |

`macro`、`sequence`、`text` 可用于所有手势。

**动作要点**

- **keys**：鼠标按键和滚轮是 KeySpec 里的键（§8.1），如 `"Ctrl+WheelUp"`，不是动作类型。连发规则见 §7.5。
- **script**：需要 `script` 段。**macro**：运行脚本库里的脚本，名称规则 `profile.CheckMacroFile`；`Parse` 不查文件在不在，运行时报“找不到”，`padmatectl validate` 给警告。不需要 `script` 段，`script.entry` 不能在 `macros/` 里。
- **profile**：`profile` 必须是合法库 ID。引擎只提交纯数据请求，slot 不读盘；切换走与自动选择共用的串行路径（16 项请求队列带配置 gen，迟到的旧请求丢弃）；加载失败保留当前配置；切到不同配置必先释放旧键。独立文件需 `--data-dir` 才能用。
- **mouse**：`precision`（`factor` 0.05–1，默认 0.3）只能放 `press`/`hold`，按住期间给鼠标模式的摇杆降速；`jump`（`x`、`y` 为 0–1）任何手势可用。
- **hud**：`hints` 只能放 `press`/`hold`，按住期间浮层列出当前层栈各控件的 label。
- **sequence / text**：`text` 规则 `keys.ValidateText`（1–256 个 UTF-16 单元，控制字符只接受 `\n`、`\t`）。步骤按 `tapPressMs` 点按，之后等 `tapGapMs`（默认 20 ms）才开始下一步；同一绑定再次触发时排在后面（§8.2“序列”）；暂停、重载、切换配置、紧急释放、退出都结束没做完的部分。

**绑定的其他成员**：`block` 消费控件但不产生动作；`timing` 覆盖四个时间（显式 0 保留）；`policy` 打断策略（§7.2）；`latch` 把按住的组合保留到同一控件的下一次按下；`haptic` 在每个手势分发时响一次（§7.4），写在没有动作的绑定上被接受但不起作用。

**analog**：`ls`/`rs`（`mode`：`standard`、`eightWay`、`fourWay`、`fourWayDiagonal`、`knob`、`mouse`，及死区、阈值、旋钮、鼠标速度等）、`dpad`、`lt`/`rt`（`press`、`release`、`fullPress`、`fullRelease`、`curve`）、`axisThreshold`。取值范围见 schema，默认值见 §7.6。主要校验：`fullRelease` 不得大于 `fullPress`，写了 `fullPress` 时须大于 `press`（没写则不查，旧文件仍可加载）；`minRateHz` 不得大于有效 `rateHz`，且只能用在摇杆方向、`active`、内外圈、半轴、扳机；`precision`、`hints` 写在 press/hold 以外的手势是错误。控件键还有 `ls.inner ls.outer ls.cw ls.ccw rs.* lt.full rt.full`。

**反馈开关**：profile `feedback` 里的 `rumble`、`led`、`pause`、`lowBattery` 已弃用：仍通过校验、无警告，但不再读取。这四个开关是每只手柄自己的设置，在 config.json 的 `devices.<DeviceKey>.feedback`（§10.4），界面写 profile 时去掉它们。
四个开关（默认 true）的作用：`rumble` 管全部震动（层切换和暂停脉冲、绑定触感、低电、脚本与出错震动；识别手柄不看开关）；`led` 管层颜色、暂停色、低电闪灯和脚本颜色，关掉时释放已设的颜色、玩家灯还给 SDL；`pause` 管暂停恢复的脉冲和 `pausedColor`，且只通过 `rumble`/`led` 起作用；`lowBattery` 管托盘、浮层和手柄自己的低电提醒（放电 ≤ 25 % 提醒一次，充电、插线或 ≥ 60 % 重新布防，阈值固定）。profile `feedback.strength` 为 0 时一切自发震动不发。开关是每只手柄自己的（ADR-0017；引擎、脚本的 `Effects.Switches(slot, key)`、出错震动和低电跟踪器各自按自己手柄的 `DeviceKey` 去取）。提交 config.json 不重载配置：已连接手柄的解析后开关有变化时，`applyLiveSettings` 调 `Supervisor.RefreshFeedback` 让每个 slot 重发层和暂停状态，开关由开变关时停掉那只手柄仍在进行的震动和脚本颜色；没有已连接手柄的开关变了就什么也不问。脚本的震动和灯光经 `internal/script/effects.go` 的 `Effects` 接口，能力和开关都满足才有 `host.has("rumble")`/`host.has("led")`；Host 在 `ReleaseAll`、`Suspend`、`Dispose` 时取消脚本占用的效果。


**功能键的两种写法**

- `functionSet`（现行）：写法同层（没有 `color`），可写任何控件含 `back`、`start`；写在当时功能键上的那条编译时忽略。`guide` 上的绑定在这里和别处一样，带警告拿掉（§7.9）。解析出的 `Profile.FunctionSet` 绑定 ID 是 `/functionSet/<控件>`。
- `functionKeys`（旧格式，仍有效）：`{"back"?: 一套, "start"?: 一套}`，里面不能写 `back`、`start`、`select`。
- 引擎读 `Profile.EffectiveFunctionSet()`：有 `functionSet` 就用它，否则由旧格式合成（§7.9）；两个都有时 `functionKeys` 被忽略并给警告（指针 `/functionKeys`）。

**继承**（`extends`）

值是配置库里另一个配置的 ID。子配置只写要改的，其余来自父配置。

- **只在配置库里有效。** `--profile FILE`、导入归档里的 profile.json、没有库时的 `profiles.validate`、库外的 `padmatectl validate` 写了它都是错误。库内入口 `profile.Resolve(data, &profile.Library{ID, Load})`，`profile.Parse` 是不带库的形式。
- **链**：最多 4 个父配置（`profile.MaxExtendsDepth`）。成环（含指向自己）、父配置不在库里、读不了或自己有错误，子配置都不加载，错误写明是哪个父配置。每一层先单独解析，所以父配置本身必须合格。
- **合并**按文档不按模型，先取父配置已合并的文档再用子配置覆盖：

| 成员 | 规则 |
|---|---|
| `layers` | 按层名；两边都有的层按控件合并 |
| 层里的控件、`common`、`functionSet` | 按控件整个替换（`label`、手势、`timing`、`haptic` 都不沿用）；别名与标准名算同一个控件 |
| `functionKeys` | 两边各自合成一套再按 `functionSet` 合并，结果写成 `functionSet`；只有一边有时原样照搬 |
| 层 `color`、`defaultLayer`、`$schema` | 子配置写了用子配置的 |
| `timing`、`feedback`、`controlNames` | 按成员 |
| `analog` | 按成员，再深一层（每个设置各自覆盖） |
| `name` | 总是子配置的 |
| `script` | 子配置有就整个替换，没有就沿用；沿用的 `entry` 相对于写它的配置的文件夹（`ScriptConfig.Origin` 是库 ID） |

- **诊断指向有问题的文件**：合并后文本里每一处都映射回写它的那个文件的位置（`Diagnostic.Profile` 是父配置 ID，空表示被检查的文件自己）。父配置有错误时子配置 `/extends` 处另有总结；两边合起来才出的错也在 `/extends` 处。`Diagnostics.Local()` 取只在被检查文件里的诊断（IPC 和界面只给这些）。

### 10.4 config.json 示例

```json
{
  "schemaVersion": 0,
  "enabled": true,
  "releaseOnSwitch": "profileChange",
  "restoreLayerState": true,
  "rules": [
    { "exe": "C:\\Program Files\\Paint\\paint.exe", "profile": "paint", "device": "any", "enabled": true },
    { "exe": "krita.exe", "profile": "paint", "enabled": false }
  ],
  "output": { "eventGapMs": 0, "sendToOwnWindow": false },
  "devices": {
    "<deviceKey>": {
      "ignored": false,
      "functionKey": "back",
      "feedback": { "rumble": false },
      "panic": { "buttons": ["back", "start"], "holdMs": 2000 },
      "mapping": "<GUID>,*,a:b0,b:b1,leftx:a0,lefty:a1,back:b6,start:b7,platform:Windows",
      "calibration": { "ls": { "deadzone": 0.22, "saturation": 0.90 }, "rs": { "deadzone": 0.18 } }
    }
  }
}
```

顶层成员：`enabled`（自动选择开关，缺省 true，不改变手动暂停）、`releaseOnSwitch`、`restoreLayerState`、`rules`、`timing`（全局四个时间，位于 profile 和 binding 之下，显式 0 不被默认值覆盖）、`output`、`joycon2.autoConnect`（默认 true：记住的 Joy-Con 2 在按住 SYNC 时自动连接；立即生效，不必重启）、`devices`（每只手柄的设置，见下）。`functionKey`、`feedback`、`safety.panic` 三个成员随 ADR-0017 搬进了 `devices`，**已退役**：守护进程仍然读它们（`Config.FunctionKey`、`Feedback`、`Safety`，`Config.Legacy` 说明文件里有哪几个），作为自己没设值的手柄的取值（解析规则见下），`pads.migrate` 把它们折进各只手柄并去掉；不再往里写新东西。它们原来的含义不变：`functionKey` 是 `start`（默认）或 `back`，退役的 `guide` 仍合 schema，按 `start` 读；`feedback` 四个开关，默认 true；含 `guide` 的 `safety.panic` 组合带警告加载但不用，这一类手柄保留内置组合（`PanicCombo.Usable`）。弃用成员 `defaultProfile`、`onUnmatched`、`ignore` 和 `profile` 为 null 的规则：旧文件照常加载、`config.put` 照常接受，但被忽略，界面每次写都去掉。

**规则**（自动选择，§9）：首条已启用的匹配规则胜出，没有匹配的用 `default`。`exe` 是文件名或完整路径，不区分大小写，`/` 与 `\` 等价，不读标题、不支持通配符。`device` 只接受 `"any"`。

**即时生效**：`enabled`、`releaseOnSwitch`、`rules`、`timing`、`safety`、`output.*`、`functionKey`、`calibration`、`feedback`、`devices.*.functionKey`、`devices.*.feedback`、`devices.*.panic`、`devices.*.mapping` 保存后即时生效（`applyLiveSettings`）；设备 `ignored` 和 `restoreLayerState` 在启动时读取，变了报 `restartRequired`。功能键变了（某只手柄的，或退役的顶层那个）不重载配置：`applyLiveSettings` 调 `Supervisor.RefreshFunctionKeys`，只有手柄功能键变了的 slot 才换 Program，并先释放自己按着的输出（§7.9）。组合变了调 `RepickCombos`，只让组合变了的手柄解除待发（`ComboWatch.Repick`）；三类设置都没动的改动不做这些事。

**每只手柄的设置**（ADR-0017）。一只手柄就是一个 `DeviceKey`；一对 Joy-Con 是它自己的一只手柄，有自己的键。`devices.<DeviceKey>` 除了 `ignored`、`mapping`、`calibration`，还可以有：

- `functionKey`：`start`、`back`、`leftshoulder`、`rightshoulder`、`lt` 或 `rt`。手柄能用哪几个取决于它的**族**（`internal/padfamily`；`input.FamilyOf(info)` 由 `DeviceInfo.Type` 得出族，`padfamily.FromGUID` 只凭键得出）。族有 `standard`、`joyconLeft` 和 `joyconRight`（单只 Joy-Con，一律竖握）、`joyconPair`、`unknown`：

  | 族 | 功能键（默认的在前） | 理由 |
  |---|---|---|
  | 标准布局（Xbox、PlayStation、Switch Pro、GameCube、未知） | `start`（Menu、Options、+）、`back`（View、Create、Share、−） | 标准按键 |
  | 单只 Joy-Con，左 | `lt`（ZL）、`leftshoulder`（L） | ZL 是食指下面的大扳机；握着 Joy-Con 时按不到 − 和 +，背键是 SL 和 SR |
  | 单只 Joy-Con，右 | `rt`（ZR）、`rightshoulder`（R） | 同上 |
  | 一对 Joy-Con | `back`（−）、`start`（+） | 标准按键 |

  `guide` 永远不是，背键也不是。扳机是它的轴的虚拟按钮，按 profile 的扳机阈值（§7.9）。对标准布局的手柄和一对 Joy-Con，手柄不可能有的值，在键的 GUID 能断定时是 config 错误（`padfamily.PossibleFunctionKeys`：GUID 里的厂商和产品号说明它是 Joy-Con，其他 GUID 是标准布局；没有这些号的键什么功能键都收），所以 `RepairConfig` 只去掉这一个成员。对单只 Joy-Con 或没有这些号的键，是控件但不在可选项里的值（背键、`back`、`start`）不是错误：`ParseConfig` 忽略它并给警告，用这一族的默认值，所以旧文件照常能读，文件的其余部分照常生效。*当前*的族不接受的值同样跳过。
- `feedback`：退役的顶层对象里那四个开关（`rumble`、`led`、`pause`、`lowBattery`；不写 false 就是开；没写的开关表示没设）。
- `panic`：紧急组合 `{buttons, holdMs}`，形状同退役的 `safety.panic` 的一项（2 到 8 个规范的物理按键，`holdMs` 500 到 60000；含 `guide` 或含手柄没有的键的组合不用，用这一族的内置组合）。

**解析**（`Config.PadSettings(key, family)`，纯函数；族是 Unknown 时由键的 GUID 得出）：三项设置各取第一个适用的。（1）手柄自己的 `devices.<key>` 成员。（2）退役的顶层成员，在适用的地方：`functionKey` 只用于标准布局的手柄（Standard 或 Unknown；Joy-Con，包括一对，有各自的默认值），`feedback` 用于所有手柄，逐个开关，`safety.panic` 取手柄所在族的那一项（一对也是 `standard`），可用才取。（3）这一族的默认：它的第一个功能键、开关全开、内置组合（见下）。`PadSettings` 还说明每项设置来自哪里（`device`、`legacy`、`default`）。`Config.Materialized(key, family)` 返回把退役成员给这只手柄的值写进去的条目，只写和这一族默认不同的；不带退役成员再解析一次，结果相同，`MigratePadSettings` 依赖这一点。

**每设备调校**：`devices.<DeviceKey>.calibration.ls/rs` 有可选 `deadzone`、`saturation`（0–1），覆盖 profile 同名值（顺序见 §7.5），扳机不调校。两个成员都给时 `saturation` 必须大于 `deadzone`；只给一个时叠加后的一对在运行时检查，不成立就忽略这根摇杆并记 warn。

**自定义手柄映射**

- `devices.<DeviceKey>.mapping` 是一个 SDL 映射字符串（1–2048 字符），写法 `GUID,*,a:b0,…,platform:Windows`：GUID 是手柄的，名字写 `*`，不写 `type:`（它改 `DeviceInfo.Type`，紧急组合的族按它选）。
- SDL 按 GUID 应用映射，所以**同一 GUID 的所有 DeviceKey 必须是同一份映射**；`devices.setMapping` 一起写进所有这样的键。绑定不同的两份映射后者报错（`Equivalent`）。有错的映射在修复时被单独去掉。

**默认紧急组合**（`input.DefaultCombo`、`padfamily.Family.DefaultPanic`），默认按住 2000 ms：

| 族 | 组合 |
|---|---|
| standard、一对 Joy-Con（左右同时连着合并而成） | back+start |
| joyconLeft（单只左 Joy-Con） | paddle2+paddle4（SL+SR） |
| joyconRight（单只右 Joy-Con） | paddle3+paddle1（SL+SR） |

`config.put` 后经 `RepickCombos` 即时换组合（§5“暂停与紧急组合”）。未覆盖的族沿用默认；手柄的 ButtonMask 缺少组合里某个键时也沿用默认（`effectiveCombo`）。按钮必须是规范物理按钮，每个组合 2–8 个，`holdMs` 为 500–60000。组合里有手柄没有的键时，读 config.json 会忽略它并给警告（`PanicCombo.UsableFor`、`Family.HasButton`），不论它是某只手柄的 `panic`，还是退役的 `safety.panic` 里 `joyconLeft` 或 `joyconRight` 的一项（含单只 Joy-Con 没有的键），内置组合照常生效。`safety.panic` 的 `joyconLeftVertical` 和 `joyconRightVertical` 两项已退役（`padfamily.RetiredPanicFamilies`）：照常接受、忽略并给警告，内置组合生效。文件里什么都不迁移，其余部分照常生效。

### 10.5 校验与写入

schema 用 draft 2020-12，只用 VS Code 和 Monaco 支持的关键字（不用 `unevaluatedProperties`、`$dynamicRef`）。

**三层校验**（`internal/profile`）：JSON 语法 → JSON Schema → 语义。前一层有错时后一层不运行，警告不影响。

- **语法**：自己的严格扫描器，同时建立 JSON Pointer → 字节偏移的索引；报告尾逗号、注释、单引号、重复键、无效 UTF-8、超范围数字等，遇第一个错误即停；跳过 UTF-8 BOM。三种文件共用。
- **schema**：内嵌的 `schema/profile.schema.json` 副本。未知控件名给相近名字（先查手柄印的名字如 `Cross` → `south`，再按编辑距离）；未知动作类型列出全部类型。
- **语义**：KeySpec 能解析，错误定位到字符串内；同层别名与标准名不指同一控件；无动作的绑定给警告 `binding has no action`；层名与层动作目标；动作规则（§7.7）；`sequence` 和 `text` 的内容。

**诊断**：`{severity, pointer, offset, line, col, message}`。offset 是从 0 起、从文件开头（含 BOM）算的字节偏移；line、col 从 1 起，col 按 Unicode 码点计（VS Code、Monaco 按 UTF-16，需从 offset 换算）。按 offset 排序。

**`padmatectl validate [--json] [--function-key KEY] [--merged] <profile.json>...`**

- 每条诊断一行 `path:line:col: severity: message (pointer)`，pointer 为根时省略；没有诊断的文件打印 `path: ok`。
- `--json`：每个文件一行 `{"file", "ok", "diagnostics"}`，`ok` 表示没有错误。
- `--function-key KEY`（`start`、`back`、`leftshoulder`、`rightshoulder`、`lt` 或 `rt`，默认 `start`）：编译用的功能键，代替手柄的设置（validate 不读 config.json）；别的值退出码 2。
- **退出码**：没有文件有错误为 0（允许警告）；有文件有错误为 1；用法错误或有文件读不了为 2（优先于 1）。读不了的文件报到 stderr，其余照常检查。
- 没有错误的文件接着用 `engine.Compile` 编译，在诊断行后每个绑定打印一行附加延迟：`  <pointer> ["label"]: press +0 ms`；有触感的加 `, haptic tick`；无动作的是 `no action`。pointer 里的控件名与文件一致；label 用 `strconv.Quote`，没有时省略。功能键的一套排在 common 之后。
- `--json` 的对象对无错误的文件多一个 `bindings` 数组，每项 `{"id","control","label","gesture","haptic","addedLatencyMs"}`（control 是标准名；label、gesture、haptic 没有时省略）；有错误的文件没有这个字段。
- 编译失败时报 1:1 处的错误 `cannot compile the profile: …`，退出码 1。
- **宏**：引用的宏不在脚本库（按精确名字，`profile.MissingMacros`）时在 `file` 字符串处给警告 `macro file "x.ts" is not in the macros folder`。库配置（路径 `…/profiles/<id>/profile.json`）查 `<数据目录>/macros`，其他文件查旁边的 `macros/`。
- **继承**：库配置有 `extends` 时，父配置从同一个库读，按合并后的配置检查；诊断先是文件自己的，再是各父配置的（用父配置的路径和位置，`--json` 里多 `profile` 字段）。库外文件有 `extends` 是错误。
- `--merged`（不能与 `--json` 同用，否则退出码 2）：不打印绑定，把每个文件合并后的完整文本写到 stdout（没有 `extends` 的原样）；诊断写 stderr，有错误的文件不写 stdout。`padmatectl trace --profile` 对库配置同样先合并。

**写入**

- 严格 JSON，不支持注释。
- 守护进程是唯一写者：写临时文件再重命名（原子写），每次写入带 revision，替换 profile.json 前先存快照（§10.1）。

**热重载**（`internal/watch`）

- 每 100 ms stat 一次，比较大小和修改时间；变了之后要稳定 200 ms 才读，读完再 stat，又变了就重新等。所以连续写入只算一次，在最后一次写入后 200–400 ms 发现。


### 10.6 事件的 JSON 行

`padmatectl monitor --json` 的输出、`padmatectl record` 的录制和 padmated 的 fake 输入是同一种格式，实现在 `internal/input/eventjson.go`（`WriteEvent`、`ReadEvents`）。每行一个对象：

```
{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"...",...}}
{"timeNs":5000000,"kind":"down","device":1,"control":"south"}
{"timeNs":9000000,"kind":"axis","device":1,"control":"lefttrigger","value":32767}
```

**字段**

- `timeNs` 是纳秒。`kind` 是 `added`、`removed`、`down`、`up`、`axis`。`control` 只在 down、up、axis 上有，`value` 只在 axis 上有，`info`（`DeviceInfo` 的 JSON 标签）只在 added 上有。
- `info.capabilities`（可选）：`known/gyro/accelerometer/touchpads/rumble/triggerRumble/led/rgbLed/playerLed`。缺少该对象的旧录制仍可读；`known:false` 表示未知。

**不是这种格式的 kind**

- 紧急组合 `ComboHeld`（combo）由 T-input 生成，重放由 `ExpandCombos` 插入（§5“padmated”），trace 里再带着会暂停两次。`WriteEvent` 拒绝，`ReadEvents` 当未知 kind。`monitor --direct`、`record --direct` 不设 `Options.OnCombo`。
- 路由元数据 `DeviceState`、`DevicePower`（power）同样不写入 trace。它们不进 IPC 的 `input` 话题。

**读取规则**（严格，错误写明行号，如 `line 3: unknown kind "press"`）

- 跳过空行和 `#` 开头的行，忽略 UTF-8 BOM；每行必须是有效 UTF-8；UTF-16 文件报错。一行最长 64 KiB。
- 未知或重复的字段、类型不对、缺 `timeNs`/`kind`/`device` 都是错误。`timeNs` 在 0 到 2⁶³-1，`device` 在 1 到 2³²-1。
- `control` 必须是 `input` 的按钮名或轴名，不接受别名。扳机值 0..32767，摇杆 -32768..32767。

**`padmatectl record [-o FILE] [--force] [--device ID] [--direct] [--virtual] [--duration D] [--count N]`** 录制第一个手柄或 `--device` 指定的手柄。默认走 IPC（§11）；`--direct` 直接开 SDL（与 monitor 相同，接受 `--preset/--hint`）。

- 第一个事件是带 info 的 added，时间为 0，后面以它为起点；比上一行早的时间提到与上一行相同。
- Ctrl+C、`--duration`、`--count` 或手柄移除时结束。`-o FILE` 在写第一行时才创建，已存在需 `--force`。PowerShell 5.1 的 `>` 写 UTF-16，所以用 `-o`。
- 退出码：录下事件并正常结束为 0；没录到、`--virtual` 没录全、SDL 丢事件或出错为 1；参数错误为 2。

## 11. IPC

守护进程对外只有一个控制面：命名管道上的 JSON-RPC 2.0。`padmatectl`、Electron 主进程和第二个 padmated 实例都是它的客户端。决策见 [ADR：IPC](adr/0009-ipc.zh-CN.md)。方法的参数与返回值以 `cmd/padmated/ipc_*.go` 为准。

**传输与发现**

- 命名管道 `\\.\pipe\padmate-<SID>`，可用 `PADMATE_PIPE` 覆盖。SDDL `D:P(D;;GA;;;NU)(A;;GA;;;<SID>)`：拒绝网络登录的 ACE 必须排在允许当前用户的 ACE 之前。
- 发现文件 `%LOCALAPPDATA%\padmate\run\daemon.json`：`{pipe, pid, protocol, token}`，创建时就带只允许当前用户的 ACL，Sync 后原子替换。客户端发送 token 前核对 `GetNamedPipeServerProcessId` 等于其中的 PID。

**协议与限制**

- NDJSON，每行一条 JSON-RPC 2.0 消息，`protocol:1`。
- 连接后第一条必须是带 ID 的 `hello{token, protocol, client}`。token 每次启动生成 32 随机字节（hex），常数时间比较；client 为 1–128 字节。hello 最多 8 KiB，读超时 3 s。
- 普通帧最多 8 MiB（不含换行），单批最多 64 条，最多 16 个客户端（含未握手的）。每连接先处理完前一帧再交付下一帧；批内不保证顺序。
- 每连接事件队列 128 条。`subscribe {topics}` 替换订阅，空数组取消；推送以话题名为 method。队列满时只丢事件，恢复后发 `dropped {n}`，引擎永不阻塞。写超时 5 s，超时断开。没有订阅时不构造事件。

### 单实例与转发

- 第二个 padmated 在读取配置、打开输入前，经发现目录认证并调用 `instance.forward {args}`，成功退出 0。端点缺失最多等 5 s；失败返回 1，不回退启动第二个输出者。相对路径由发送方转绝对。离线 trace 重放不参与。
- 库实例用 `--profile-id`，独立文件实例用 `--profile FILE`；显式 data-dir 必须相同，不能经转发转换入口类型。

### 错误码

JSON-RPC 标准码（如 -32602）之外：

| 码 | 含义 |
|---|---|
| -32001 | 认证失败；`ui.exitReady` 等需要先 `ui.register` |
| -32004 | 找不到（配置、设备、快照等） |
| -32008 | 没有配置库（独立入口未带 `--data-dir`） |
| -32009 | revision 冲突，`data.revision` 是当前值 |
| -32010 | 假输入的方法只在 `--fake-input --dry-run` 下可用 |
| -32011 | 转发的启动参数与运行中的实例冲突 |
| -32012 | 配置是当前活动配置，不能删除 |
| -32013 | 配置仍被引用（规则或启动参数），`data.references` |
| -32014 | 目标 ID 已存在（复制） |
| -32015 | 链接、junction、特殊文件，或超出复制限制（`data.path`） |
| -32016 | 脚本或宏文件已存在，`data {exists,name,replaceable}` |
| -32017 | 登录自启不可用（开发版或不支持） |
| -32018 | 固定默认配置 `default` 不能删除或改名，`data {"op"}` |
| -32019 | 宏或旧式脚本入口不存在，`data {file}` |
| -32020 | 不是可接受的导出文件，`data {reason,path?,diagnostics?}` |
| -32021 | 配置名称已被占用，`data {id,name}` |
| -32022 | 导入的脚本与库里同名但内容不同且未指定 `macros`，`data {conflicts}`；什么都没写 |
| -32023 | 删除配置时，基于它的配置不能写成完整内容，`data {id,reason,diagnostics?}` |
| -32024 | 不是可导入的 AntiMicroX 文件，`data {reason}` |
| -32025 | 映射不能保存，`data.reason`：`syntax`、`mapping`、`guid`、`config` |
| -32026 | 这个输入做不了（假输入没有映射或原始事件，或已映射的手柄没有处于捕获） |
| -32027 | 蓝牙 LE 的 Joy-Con 2 不可用（不是 Windows、传输没有启动，或没有它的假输入） |

### 方法

| 方法 | 说明 |
|---|---|
| `hello` / `subscribe` | 握手（必须第一条）/ 订阅话题 |
| `status.get` | 守护进程状态、slots、设备、紧急组合、冲突程序、配置修复提示 |
| `devices.list` / `devices.identify` | 设备表（按连接 ID 排序，含 `key`、`slot`、`status`、`battery`）/ 让手柄震动闪灯 |
| `joycon2.status` / `joycon2.pair {seconds?}` / `joycon2.cancelPair` / `joycon2.forget {serial}` / `joycon2.disconnect {id}` | Joy-Con 2：状态 / 打开连接并记住新手柄的窗口（默认 30 s，1–600）/ 关闭它 / 忘记记住的手柄（state.json 也删）/ 按连接 ID 断开一个手柄（一对的两只都断；手柄仍记着）。都返回状态 |
| `devices.rawInfo` / `devices.setMapping` / `devices.resetMapping` | 映射向导：原始事件流 / 保存并应用 SDL 映射 / 拿走它 |
| `latency.get` / `latency.reset` | 延迟统计（口径见 §13）/ 清空 |
| `engine.pause` / `resume` / `releaseAll` / `setLayer` | 暂停、恢复、全部释放、切层 |
| `capture.start` / `capture.stop` | 为当前连接取得/释放捕获凭据 |
| `profiles.list/get/put/validate/resolve/activate/explain` | 配置库读写与解释 |
| `profiles.copy/delete/history/restore/export/import/importAmgp/folder` | 复制、永久删除、旧版本、导出导入（base64）、AntiMicroX 导入 |
| `apps.recent` | 最近的前台程序（最多 12 个，不含 padmate 和系统程序） |
| `config.get/put/repairSeen` | config.json 读写，确认修复提示 |
| `pads.list` / `pads.set` / `pads.migrate` | 每只手柄的设置（ADR-0017）：连接过的手柄及其功能键、反馈开关、紧急组合（存的和解析出来的）/ 写一只手柄的设置 / 把退役的全局成员折进各只手柄 |
| `startup.get/set` | 登录时启动（只接受 `enabled`，命令总是本进程的可执行文件和 `startupArgs`） |
| `script.status/reload/describe/registrations/logs/init/import/path/detach` | 脚本 |
| `macros.list/create/import/rename/delete/path` | 脚本库的宏，不带配置 ID |
| `fake.press/release/axis/effects/battery/joycon2` | 只在 `--fake-input` 下 |
| `ui.register` / `ui.setOutput` / `ui.exitReady` | 注册经验证的 UI 进程；设自身窗口输出开关；托盘退出或 `daemon.exit` 时界面存完修改后确认（只有注册过的连接能调） |
| `daemon.exit` | 安装程序请 padmated 像托盘“退出”一样退出（§5）；参数必须恰为 `{reason:"installer"}`，别的内容 -32602 且不动 |
| `instance.forward {args}` | 第二实例转发启动参数 |

**UI 桥**（`padmatectl ui-bridge`）是 Electron 主进程连接守护进程的私有子命令，stdin/stdout NDJSON，带方法白名单和有界队列（`TestUIBridgeMethodAllowlist`）。`daemon.exit`、`fake.*`、`instance.forward` 不在白名单里；`profiles.folder`、`macros.path`、`script.path` 返回路径，只给主进程。

**要点**（其余细节读代码）

- **`profiles.put {id,text,revision}`**：revision 必填，空串只允许创建；不匹配 -32009。校验后在写锁内写临时文件、Sync、rename，替换前存快照。活动配置同步应用（即使 `--watch=false`），返回 `{revision,applied,problem?}`：脚本参数检查失败时文件已保存但保留旧运行时并返回 `problem`。改固定默认配置的 name 返回 -32018。父配置不在库里、成环或有错误时 InvalidParams。
- **`profiles.delete {id,revision}`**：永久删除，不保留副本。写锁内先 rename 成隐藏的 `.tmp-<id>-*` 再删，所以要么整个离开库要么不变；删不干净仍算成功。拒绝：活动配置 -32012；被规则（含禁用的）或启动参数选中 -32013；链接等 -32015；`default` -32018。删除前先刷新 state.json。被别的配置基于时先把它们写成完整内容（§10.1），返回 `flattened`；写不成 -32023。
- **`profiles.export/import`**：守护进程只收发字节，不接受路径，选文件和读写文件在 Electron 主进程。`import {data,name?,replace?,macros?}`：`name` 与 `replace` 不能同给；`-32020` 的 `reason`：`format`、`version`、`entry`、`limit`、`encoding`、`profile`；名称占用 -32021，脚本冲突 -32022。
- **`profiles.explain`**（`ipc_explain.go`）自 ADR-0017 起按手柄回答：`slots` 的每一项有自己的 `functionKey`（该 slot 的 Program 的功能键，即 `View.ProgramFunctionKey`）、`isFunctionKey` 和 `functionSet`。库里的 profile（`id`）没有手柄，所以守护进程已知的手柄（`pads.list`）里每个不同的功能键各一项（`slot` 为 -1），`controllers` 列出用它的 DeviceKey；一只都不认识时给默认键的一项。顶层的 `functionKey` 和 `isFunctionKey` 取第一项的。
- **`pads.*`**（`ipc_pads.go`）：`pads.list` 返回 `{revision, legacy:{any,functionKey,feedback,panic}, pads:[…]}`，`revision` 是读取列表时 config.json 的版本（有错的文件先修复，同 `config.get`）。pad 是每一只连着的、在 state.json 的 `controllers` 里的、或在 `devices` 里有条目的手柄：连着的在前，按连接顺序，其余按 `lastSeen` 从新到旧。成员：`key`、`name`、`type`、`buttonMask`、`known`（守护进程见过它；只有 config.json 提到的为 false：`name` 为空，`type` 为 `unknown`，族由键的 GUID 得出）、`connected`、`device`（连接 ID）、`ignored`、`lastSeen`、`family`（`standard`、`joyconLeft`、`joyconRight`、`joyconPair`、`unknown`），以及三项设置：`functionKey {default, options:[{control,label}], stored?, effective, source}`（`options` 是这一族的，默认的在前；`label` 是手柄上印的名字：“ZR”、“Menu”、“−”）、`feedback {stored?, effective:{rumble,led,pause,lowBattery}, source}`（`source` 取各开关中最强的）和 `panic {default, stored?, effective, source}`（`effective` 是 T-input 检测的组合：手柄缺所配组合中的某个键时是内置组合）。`pads.set {key, revision?, functionKey?, feedback?, panic?}` 写一只手柄的条目：没出现的成员不变，`null` 拿掉手柄自己的值（退役的顶层成员或这一族的默认重新生效），其他值就是设置；给了 `revision` 就必须是 config.json 当前的（-32009）。返回 `{revision, applied, problem?, restartRequired?, pad}`。`key` 不是规范 DeviceKey、手柄所在族不接受的功能键、不认识的开关或组合成员、校验不过的组合是 -32602；既没连着、也不在 state.json 和 config.json 里的键是 -32004。它按字节位置改文本（`profile.SetPadSettings`：其余成员保留字节和位置，结果按界面写文件的方式缩进，没有成员的条目删掉），经 `config.put` 的路径提交（`commitConfigLocked`），所以随后有 `profile.changed {kind:"config"}`。`pads.migrate {revision?}` 把退役的顶层 `functionKey`、`feedback`、`safety.panic` 折进每只已知手柄的条目（`profile.MigratePadSettings`：只写和这一族默认不同的，所以每只手柄的行为不变）并去掉它们；文件里没有这些时返回 `changed:false`。从 state.json 开始列手柄以来没连过的手柄，此后用它这一族的默认。三个方法都在 bridge 白名单里。守护进程运行用的有效配置（`liveSettings`）带着按手柄的成员，slot、效果开关和 `ComboFor` 都从里面读（§5.6、§7.9）。
- **`joycon2.*`**（`ipc_joycon2.go`、`ipc_fake_joycon2.go`）：结果是 `{available, unavailable?, autoConnect, windowOpen, windowEndsAt?, windowLeftMs?, scanning, known:[{addr,serial,side,connected}], links:[{addr,serial?,side,player,firmware?,paramsRequest?,intervalMs?,latency?,timeoutMs?,state,device?,known}]}`。连接的 `state` 为 `connecting`、`connected`、`dormant`（静默；它不在任何手柄里）或 `closing`；`paramsRequest` 是吞吐优先连接参数请求的结果（`accepted`、`unavailable (Windows 10)` 或 `failed: <原因>`），`intervalMs`（数字，如 15 或 11.25）、`latency`（连接事件数）和 `timeoutMs` 是当前的连接参数，由传输每 2 s 读一次：未知时不出现（链路建立之前，以及 Windows 10 上始终，它读不到），且只有 `intervalMs` 出现时 `latency` 和 `timeoutMs` 才有意义（延迟为 0 时不出现）；`device` 是 `devices.list` 和 `joycon2.disconnect` 用的连接 ID。没有这个后端的输入对 `joycon2.status` 答 `available:false`，其余方法 -32027；不认识的 serial 或连接 ID 是 -32004；`seconds` 不在 1–600、缺 `serial` 或 `id`、多余成员是 -32602。后端没有变化回调，所以由一个监视 goroutine 在手柄来去、方法之后、手柄被记住时、窗口开着或有连接正在建立或关闭时每 250 ms、其余时候每 2 s 读一次状态，不同就发 `joycon2.changed`（剩余时间不算变化）。假输入有模拟的后端：不扫描、不写 state.json、不出手柄；`joycon2.pair` 开窗口，`fake.joycon2 {side?}` 是用户按住 SYNC（只在窗口开着时连上并记住 `FAKEJC2L0001`、`FAKEJC2R0001`……，连接 ID 从 1001 起），窗口到时自己关。托盘只在输入有这个后端时显示“连接新的 Joy-Con 2”，点击打开 30 s 窗口并给出通知（§5）。界面“设置”里的 Joy-Con 2 一栏（`SettingsJoyCon2.tsx`）是 `joycon2.status`、`joycon2.pair {seconds:30}`、`joycon2.cancelPair` 和 `joycon2.forget` 的客户端：Main 的处理函数（`joycon2-handlers.ts`）不从渲染进程接收方法名或地址，并检查序列号；话题 `joycon2.changed` 只在这一栏挂载期间、且 `joycon2.status` 说 `available` 之后，才在唯一的那条订阅上（`HudSubscription.joyCon2Up`）；`available` 为 false 时这一栏也不显示。`joycon2.disconnect` 在 bridge 的白名单里，界面不用它。Joy-Con 2 的连接状态和电量不在这个状态里：它们像任何手柄一样在 `devices.list` 里，“设置”的手柄行显示它们。
- **`devices.rawInfo`**：手柄要求前必须已在捕获状态（`capture.start`），否则 -32026（向导要用户按会被映射成按键的按钮，必须先暂停）。要求按连接记，断连时放掉，最后一个放掉才 `RawStop`；原始事件作为 `input.raw` 只发给要求过该手柄且订阅了话题的连接。向导流程：订阅 `input.raw`、`capture.start`、`devices.rawInfo`，结束时 `rawInfo {watch:false}` 和 `capture.stop`。
- **`macros.*`**（`ipc_macros.go`、`profilelib/macro.go`）：全部在库写锁内经 `os.Root`。`rename` 同时按字节位置改写所有引用，任何一个写失败就全部改回；`delete` 先清掉引用再删文件；整库已有 64 个宏时新建返回 InvalidParams。库模式下 `watch.DirWatcher` 监视库文件夹顶层脚本。CLI：`padmatectl macro list|new|import|rename|delete`。

### 话题

`subscribe` 的话题固定为（`ipc.Topics`）：

| 话题 | 内容 |
|---|---|
| `input` | 与 §10.6 同格式的输入事件，另有 `remapped`。排队、被过滤、暂停和捕获期间的输入都可订阅 |
| `engine.activity` | 决策、未路由的边沿、每 250 ms 的状态快照（下） |
| `log` | `{sequence,record,truncated?}`，遵守当前日志级别；内存保留 256 条，每条最多 16 KiB |
| `script.status` | 每 250 ms 的轻量脚本状态；仅有订阅时查询 |
| `profile.changed` | 配置库与设置的变化（下） |
| `output.preview` | Arbiter 解析快捷键后的预览；未订阅时不构造 |
| `app.foreground` | `{exe,path,elevated,own,system?}`，只含可执行文件身份，不读窗口标题；`own` 标记已注册的 padmate 界面，`system` 标记 Windows 目录下的程序 |
| `daemon.exiting` | 退出前发布 `{reason:"tray"}` 或 `{reason:"installer"}` |
| `hud` | 脚本的 `hud.toast`：`{kind:"toast",slot,text,ms}`。有订阅时脚本的 `host.has("hud")` 为 true |
| `input.raw` | `devices.rawInfo` 要求过的手柄的原始事件 `{timeNs,device,kind,index,value}`；订阅它本身不开任何设备 |
| `joycon2.changed` | Joy-Con 2 的状态，形状同 `joycon2.status` 的结果，变化时发（窗口开关、手柄连上、被记住、被忘记或断开，或连接参数变化） |

**`profile.changed`** 的 `kind`：`active`（`id,revision,generation`）、`saved`（`id,revision,applied`）、`deleted`（`id`）、`config`（`revision,applied,restartRequired`）、`macros`（脚本库顶层脚本变化，只在 `--watch`）、`configRepaired`（§10.1）。

**`engine.activity`** 的三种负载：

1. `{kind:"decision",slot,decision,latchBinding,at,waiting?,waitedMs?}`：`decision` 是引擎的 `Decision`（§7.8）。守护进程发布时补 `at`（墙上时钟 Unix 毫秒）、`waiting`（按下时没有动作、要等松开/长按/连按才能确定时列出这些手势，不是失败）、`waitedMs`（点按、多击、长按判定距最近一次按下的毫秒数，起点是 `scfg.OnEvent` 记下的按钮到达时间，含排队等待）。
2. `{kind:"unrouted",at,device,control,down,why}`：不在 slot 里的手柄的按钮边沿，`why` 为 `queued`、`ignored`、`notGamepad`、`disabled`。令牌桶限速每秒 16 个，超出丢弃。
3. 每 250 ms 的 `{kind:"state",paused,capturing,suspended,slots,batteries}`：`slots` 的对象与 `status.get` 同类型（按着“按键提示”时多 `hints`，脚本 `hud.panel` 留着面板时多 `panels`；出现和消失最多晚 250 ms）；`batteries` 每个读到过电量的手柄一项 `{device,key,name,state,percent?,low,episode?}`，`low` 是低电提醒已发出、还没重新布防，`episode` 只有 low 时有，界面靠它变大认出新的一次提醒。

### 11.1 导入 AntiMicroX 配置

实现在 `internal/amgp`（纯包，Linux 上可测）：`amgp.Import(data, opts) (profileJSON, report, err)`。输出总能通过 `profile.Parse` 和 `engine.Compile`（`Import` 自己再解析一遍，失败返回 `ReasonInternal`），同一个文件永远得到同一份输出。文件格式取自 AntiMicroX 源码（GPL-3.0，只读，没有复制代码）和真实文件；键码表是 Qt 6 的 `qnamespace.h`（`configversion` ≥ 6）。

**读文件**（文件不可信）：限制 1 MiB（`amgp.MaxBytes`）、32 层嵌套、10 万个元素；`encoding/xml` 严格模式，拒绝 UTF-16、非 UTF-8 声明、DOCTYPE、不合法 XML、多个根；根必须是 `gamecontroller`，`configversion` ≥ 6。拒绝是 `*amgp.Error{Reason,Message}`，`reason` 为 `size`、`encoding`、`xml`、`limit`、`doctype`、`root`、`joystick`、`version`。文件里没写的是 AntiMicroX 的默认值，导入的摇杆和扳机总是把换算后的值写明。

**对应规则**：按钮、摇杆方向、十字键、扳机一一映射到对应控件名；按下区/长按区/松开区映射成 `press`/`hold`/`tap`/`release`；`cycle` 2–4 段映射成 `tap`…`quadTap`；`pause` 和 `textentry` 映射成 `sequence` 和 `text`；`setchange` 映射成层切换（被 `while-held` 指向的 set 里没绑定的控件写成 `{"block":true}`）；`back`/`start` 上的 `while-held` 成为 `functionSet`；turbo 成为 `repeat`；摇杆、扳机、十字键设置取 set 1 的值写进 `analog`。传感器、校准、`execute`、`loadprofile` 等丢弃并报告。完整规则见 `internal/amgp` 与 `testdata/`。
**报告**（`ReportLine{Severity,Code,Set,Control,Slot,Message}`）：`severity` 是 `dropped`、`approximate`、`info`；`code` 是稳定的名字；`message` 是中文原因。同一行只记一次，最多 1000 行，多的合成 `report.omitted`。

## 12. Electron

`app/` 是可选的界面：Electron 主进程、两个 preload、两个渲染页面（主窗口和 overlay）。守护进程不依赖它，关掉界面映射照常工作。界面只经 IPC 和守护进程说话（§11），不读写 profile、config 文件，也不发键。

决定见 [ADR：进程划分与技术栈](adr/0001-processes-and-stack.zh-CN.md)、[ADR：编辑界面](adr/0013-editor-ui.zh-CN.md)、[ADR：Overlay](adr/0014-overlay.zh-CN.md)。版本见 `app/package.json`。

### 12.1 进程与窗口

| 部分 | 位置 | 职责 |
|---|---|---|
| Main | `app/src/main/` | 启动和连接守护进程，持有 bridge，校验渲染进程的每个请求，管理窗口、overlay、更新 |
| preload（主窗口） | `src/preload/index.ts` | 只暴露 `window.padmate`（类型 `PadmateAPI`，在 `shared/api.ts`） |
| preload（overlay） | `src/preload/overlay.ts` | 只暴露 `window.padmateOverlay`，四个只收不发的监听 |
| 渲染进程 | `src/renderer/` | 主窗口（`index.html`）和 overlay（`overlay.html`） |

- 渲染进程从自定义协议 `padmate://app/` 加载，不走 `file://`；开发时走 electron-vite 的 `127.0.0.1` 服务器。
- 主窗口默认 1180×800，最小 850×620。语言 zh-CN 和 en，只有深色配色。
- `electron-updater` 打进 `out/main/index.js`，安装版没有 `node_modules`。
- 渲染进程按功能分子目录，测试和它测的文件放在一起：`state/`（`store.ts` 和全局的 `*-model.ts`）、`editor/`（一个配置的编辑：`editor-model.ts`、`editor-store.ts`、继承、撤销）、`controller/`（3D 手柄、按键标注、`pad-icon.tsx`，以及 `assets/` 的 GLB 和 `pad-icons/`）、`workspace/`（映射页）、`drawer/`（按键抽屉和绑定编辑）、`analog/`（摇杆和扳机的面板）、`pages/`（诊断、日志、脚本页，`profiles/` 是配置页，`settings/` 是设置页和映射向导）、`onboarding/`、`update/`、`overlay/`、`ui/`（通用控件）、`testing/`（只供测试的夹具）。第一层只有两个页面的入口（`index.html`、`main.tsx`、`overlay.html`、`overlay-main.tsx`）、`App.tsx` 和它直接用的几个文件，以及扫描整个渲染进程目录的测试（`forced-colors`、`theme-contrast`、`doc-names` 等）。

### 12.2 安全桥

- `main/security.ts` 的 `baseWebPreferences`：`contextIsolation`、`sandbox` 开，`nodeIntegration` 关。拒绝导航、新窗口和 webview。内容安全策略由 `padmate://app` 的响应头给出：`script-src 'self'`、`connect-src 'none'`。
- preload 只暴露命名的类型化方法，各对应一个 `padmate:*` 通道。渲染进程拿不到 token、管道名、PID，不能发任意 RPC。
- Main 的每个处理函数检查发送方是主窗口的主 frame、URL 是应用自己的、参数合格（`validate*`）。涉及路径的操作只接受 Main 自己拼的路径或写死的目标。
- 返回给渲染进程的错误只带 code 和已知的 data，不带用户目录下的路径。

### 12.3 和守护进程的连接

`DaemonManager`（`main/daemon-manager.ts`）管启动和重试，`Bridge`（`main/bridge.ts`）管 RPC 和事件，Go 一侧的 `padmatectl ui-bridge` 管认证。

1. Main 启动 `padmatectl ui-bridge --ui-pid PID` 子进程，经它的 stdin/stdout 收发帧。桥接读 `daemon.json`，发 token 前先验证管道服务端的 PID；守护进程验证 UI 是桥接的父进程、同一用户、同一安装目录，再注册这个 UI。
2. 先连已运行的守护进程；失败后每轮最多启动一次 detached 的 `padmated`，有界重试。
3. `ConnectionState.phase`：`connecting`、`connected`、`disconnected`、`failed`。页面 ready 且连上（或启动失败）后才显示窗口。
4. 关闭界面只关自己的 bridge，守护进程继续运行。

守护进程的启动参数：安装版 `--inject --data-dir`（真的发键）；开发版和 E2E 用 `--dry-run`；连上已运行的实例时沿用它的模式。

- **启动失败**：每次启动用新的报告文件 `<userData>/startup-reports/daemon-<PID>-<序号>.json`（`--startup-report`）。子进程退出码非 0 时读报告（最多 256 KiB，只留认得的字段），结束重试，状态变 `failed`，`failure` 为 `report`、`exit`、`spawn` 或 `timeout`。退出码 0 且无报告，表示请求已转给运行中的实例。`failed` 时 `StartFailure.tsx` 占满窗口，只给原因和三个按钮（打开文件夹、重试、退出）。
- **事件交付**：经 `EventBuffer` 交给渲染进程，在途批次最多 32 条、待发队列最多 128 条（同设备同控件合并），溢出时报告 `dropped`，渲染进程刷新状态。连接代数阻止旧请求结果覆盖重连后的状态。
- **捕获类会话**：键盘录入用短期子进程 `padmatectl keyboard-capture`，取得自己的 `capture.start` 连接所有权后才装 LL 观察钩子，只观察非注入的实体键；失焦、取消、退出、断开或 30 秒超时都结束子进程并释放连接。手柄捕获（`pad-capture.ts`）的 `capture.stop` 失败 1 s 重发，直到成功或断开。
- **发到 padmate 自己窗口的键**：已注册的 UI 在前台时，Arbiter 只做虚拟记账并发布 `output.preview`，不调用 Sender；切换前台时先释放原目标的键，用 `BlockOwnWindow` 闸门等手势、连发和旧脚本被取消。`config.output.sendToOwnWindow` 保存即生效；全局 dry-run 始终优先。

### 12.4 状态和保存

- `renderer/editor/editor-store.ts`：映射页和配置设置对话框的编辑器，一个配置一个，同一配置同一时刻只有一个。持有整份 profile 草稿、revision、撤销记录、功能键视图 `view`。纯函数在各 `*-model.ts`。
- 界面自己的偏好存渲染进程 `localStorage`；守护进程需要知道的设置写 config.json。语言另外写进 `<userData>\language.json`（`main/ui-language.ts`），一行 `{"language":…,"setBy":"settings"}`，因为安装程序要读它：安装程序把默认语言以 `setBy:"installer"` 写在这里，不是自己写的文件就不动（§15.4）。Main 在窗口绘制前读这个文件（`getLanguage`）；语言先取 `localStorage` 的，其次取文件的，都没有时用 zh-CN；只有 `localStorage` 里有的选择，启动时复制进文件。
- `status.profileId` 是库 ID，`slots[].profile` 是展示名称，不能拿名称调库 API。

**自动保存**：

- 每次提交立即保存并生效，没有保存按钮。文本框在回车或失焦时提交，不合格的输入不提交，Esc 恢复。毫秒框是 `type="text"`（原因见 PITFALLS“UI”）。
- 草稿始终是整份 profile，界面只编辑 `layers.base`（或按住功能键时的一套）。其他层、common、层动作、block 保存时原样保留且只读；`defaultLayer` 不是 base 时整份只读。没填完的部分保存时略去（`savedForm`），草稿里保留；手柄图标签、“所有按键”列表和导出对照表读 `savedForm(draft)`。名称跟着映射走：抽屉的 `edit` 让每次修改经过 `withoutOrphanLabels` 和 `withoutOrphanStickLabels`，前者从这次改过、又没有动作或屏蔽（`bindingMaps`）的键去掉 `label`，后者从这次碰过、又什么也不映射（`stickMapped`：移动鼠标，或当前模式的键、内外圈、原始半轴有动作）的摇杆去掉 `analog.ls/rs.label`。这次没改的键，文件里只有名称的照样保留；编辑器不再隐藏守护进程对它们的“binding has no action”。
- 保存串行：先 `profiles.validate`，再带 revision 的 `profiles.put`。校验不过不写盘，底部提示“修改未保存”。revision 冲突时重读磁盘并丢弃未保存的修改。
- 没收到回答（bridge 10 s 超时、断连）时守护进程可能已写入：`editor-store` 记下最近 4 次 put 的文本，读到的磁盘内容与其中一份逐字节相同就认作自己的写入，保留草稿；不同才以文件为准。
- 关窗不弹确认框：Main 发 `padmate:closing`，渲染进程提交输入、等保存完成后调 `readyToClose`，Main 最多等 3 s（`CLOSE_WAIT_MS`）；退出路径等 1800 ms（`EXIT_WAIT_MS`，小于守护进程的 2 s）。
- 撤销/重做：Ctrl+Z、Ctrl+Y，每个配置最多 100 步，只在内存里，结果同样立即保存。开着“在 padmate 窗口里也发送按键”且连着手柄时，手柄注入的 Ctrl+Z 会先于键盘事件到达，所以此时撤销键完全不处理。

### 12.5 页面

外壳（`App.tsx`）：左侧导航为映射、配置、脚本、设置；日志、诊断和手柄设置是设置的子视图。顶栏左侧是配置菜单，右侧是运行状态、暂停和“更多”菜单。`config.get` 带 `restartRequired` 时设置页提示需要重启。配置损坏被守护进程修复后 `ConfigRepairDialog` 提示，Main 只接受 `config.broken-YYYYMMDD-HHMMSS[-N].json` 形式的备份名并自己拼路径。

- **映射页**：固定正面的 Three.js 手柄（本地 GLB），渲染器常驻不销毁。Joy-Con 2 按厂商 `0x057e`、产品 `0x2066`、`0x2067` 或 `0x2068` 认出（`controller-models.ts` 的 `isJoyCon2`），用自己的模型 `joycon2-left.glb` 和 `joycon2-right.glb`；Switch 1 的 Joy-Con 仍用 Switch OLED 的模型。`app/scripts/pack-controller-models.mjs`（`switch2`）从 NAIT Studio 的 Switch 2 主机模型（CC BY 4.0，署名在 `assets/ATTRIBUTION.md`）里取出它们：只留两只 Joy-Con 2，部件分别命名、各有一份材质，没有贴图（约 270 KB 和 296 KB），位置、法线和索引用 `KHR_mesh_quantization` 量化（这个素材不用 meshopt），摇杆的轴心移到它的底部。原模型没有 SL、SR、ZL、ZR 的几何，文件里用同名的空节点标出它们，它们的引线指向导轨和肩部上的固定位置。摇杆由“旋转”（`leftstick`）和“点按”（`leftstick.press`）两格组成；“旋转”格的标题是 `analog.ls.label`（`analog-editor.ts` 的 `setStickLabel`，不经 `settleStickMode`）。旋钮刻度的抽屉只有一个快捷键框（`SingleShortcutField`，写 `{press:{type:"keys"}}`，保留 `label`、`haptic`）。十字键四个方向是普通按键的抽屉，没有设置面板。标签由 `controllerCallouts` 按 `padHasControl`（`controller-model.ts`，读 `DeviceInfo` 的 `buttonMask`、`axisMask`，两者都是 0 时按标准手柄）给出：手柄没有的键不出，其他手柄的附加键（misc1、misc2、paddle1–4）在 `placement:"bottom"` 一排，`offset` 从中间算，左手的 paddle4、paddle2 在左，右手的 paddle1、paddle3 在右；`controller-3d.css` 让这一排夹在两列之间。一对 Joy-Con 没有 SL、SR 的标签（paddle1–4，两只合在一起时按不到）：`diagramHidden` 和 `diagramHasControl` 只把它们从手柄图上去掉，模型上的标记和无障碍状态列表也一起去掉；守护进程照常读它们，配置里对它们的绑定原样保留（编辑器的键列表、快捷键表和 `savedForm` 不看 `diagramHidden`）。Joy-Con 的标签不按上面的列和排放，而由 `controller-joycon-layout.ts` 放：一张表写明每组键可以放在哪个区域（列上方两个肩部角、上面一排、两列、下面一排），最好的在前。单只 Joy-Con 一律竖着显示：每侧一个肩键标签（L 在外侧的列，ZL 在导轨那一侧的列；右手那只镜像），摇杆的标签在导轨那一侧的列里、SL 和 SR 之间；一对沿用标准手柄的两列，Y 与 X、A、B 的标签一起放在右列。Joy-Con 的引线是折线：先沿标签所在的行或列，再以 45° 接到按键。每个 Joy-Con 标签带一个走法 `route`：`auto`（如上；走不出 45° 时先出 12 px，再直线连过去）、`steep`（最后一段接近竖直，用于终点几乎重合的两个肩键中的一个）、`gap`（四个键里最靠里的那个，从相邻两键之间的缝穿过）。`controller-layout.ts` 的 `leaderPath` 把走法变成折线；场景每帧按它画线（摇杆的终点会动），各项检查都逐段判断。其他手柄的引线仍是直线。模型还没建好时标签按表里的第一选择放（`staticLayout`）。建好后，`buildAssetModel` 把锚点（每条引线在模型坐标系里的终点）交给 `arrangeJoyCon`：它保持表里的顺序，只搜索每列的偏移和某些标签（SL、SR、L 等）前面留出的空，并为每种标签密度分别给出位置（`slot`、`slotCompact`、`slotMini`），找出在 `supportedStages` 的每个舞台尺寸（440 × 440 起，即工作区给舞台的最小尺寸，每次密度切换的两侧都在内，包括 640 × 640）上，并且没有标签盖住模型：`buildAssetModel` 还交给它一圈外形点（`shellOutline`，把模型按深度分成 16 片，取每片顶点的凸包顶点），`projectedHull` 把它投到每个舞台上成为剪影，标签碰到剪影和引线交叉一样算问题。要找的是没有两条引线相交、也没有引线穿过标签的排法（一条引线离另一条的终点不到 1.5 px 也算相交，终点几乎重合的肩键和扳机 `twin` 除外）。Joy-Con 的标签贴着模型放（`controller-3d.css` 的 `data-wide-rows`、`controller-layout.ts` 的 `WideRows`；`Controller` 在模型建好后设 `--model-h`、`--model-w`，给出模型以其单位计的大小，`--unit` 是一个单位的像素数）：只有一对有一排标签，即模型上方的 −、+，用窄标签（没有 `data-wide-top`），还有两个肩部角，不向模型内缩（`--corner-shift: 0`）；肩部角、一对的右列和单只 Joy-Con 导轨那一侧的列从模型顶部往下排（`data-hang`、`--model-top`），上面的肩部角会出舞台时两个角一起下移（`cornerLift`、`--corner-lift`）；场景在第一帧把结果交给 `Controller`（`onReady(model.callouts)`、`withArrangement`）。`controller-layout.ts` 在不用浏览器的前提下给出所需的几何：它重复 `controller-3d.css` 的尺寸和 `controller-scene.ts` 的相机，`controller-layout.test.ts` 让它们与样式表和 `PerspectiveCamera` 保持一致。`controller-joycon-layout.test.ts` 是证明：Switch 1 和 Switch 2 的左右 Joy-Con，以及一对，在这些舞台尺寸上，有无编辑器提示条时，都没有引线相交或穿过标签，标签之间也不互相遮盖；例外是测试里 `knownProblems` 列出的尺寸（紧凑和最小密度下偏高的舞台，右手单只或一对的面键引线仍会相交），别的尺寸出问题、或列出的尺寸变干净，测试都会失败。在 `app/` 下运行 `npm run preview:layouts`（来自 `tests/preview/`，是独立的 Vite 工程，不属于应用的构建）会在 `http://127.0.0.1:5199/controller-layouts.html` 提供一个页面，用虚构的绑定并排显示全部摆法。加 `?check=1` 时，页面在每个手柄都画好之后，从 DOM 读回标签的方框和引线的整条路径（`tests/preview/layout-check.ts`），与布局模型核对：每条引线从自己标签朝向手柄那条边的中点出发、按自己的走法、终止在自己的按键上；标签的位置与 `controller-layout.ts` 所说一致（即样式表和 TypeScript 一致）；引线互不交叉，不穿过标签，标签不互相遮盖、也不盖住模型。结果以 JSON 写在 `<pre id="layout-check">` 里。`node tests/preview/check-layouts.mjs`（不属于 CI；它需要开发服务器和无头 Chromium，不开窗口）对全部摆法在若干舞台尺寸下运行这项核对，并为每种存一张 PNG。模型上的锚点只给有标签的键（加上 guide，按下时照常点亮）；各手柄的掩码样例在 `testing/sdl-pads.ts`，`internal/input` 的 `TestVirtualFamilyControls` 用虚拟手柄核对同一组数字。按键图标由 `pad-icon.tsx` 从 `app/src/renderer/controller/pad-icons/` 的 SVG 解析出路径画成内联 SVG（`PadIcon`），手柄图标签、抽屉标题、按键预览（`control-icon.tsx`）和映射向导共用。点标签开 `KeyDrawer`：映射只有点按、长按、双击，动作只有“快捷键”和“脚本”；文件里的其他映射和动作照常生效。点按写 `press` 还是 `tap` 由键上有无长按/双击决定（`settleTap`）。快捷键只能录入（`ShortcutField`，键盘经 `keyboard-capture`，鼠标和滚轮窗口自收）。`analog` 参数整个配置共用，不随功能键分套。
- **功能键**（引擎见 §7.9，格式见 §10.3、§10.4）：每只手柄一个，在设置页的“手柄设置”子页里选（可选项和默认值看手柄所在的族，§10.4）；它本身不可绑定，界面编辑它切换到的那一套（`profile.functionSet`）。它编辑的那一套对应映射页所显示那只手柄的功能键（`effectiveFunctionKey`；没有手柄连着时是 `start`），换手柄或换它的功能键，哪个控件是功能键随即改变。“平时 / 按住功能键”开关切 `view`（不写盘、不进撤销），做法是 `swapView` 把 `functionSet` 与 `layers.base` 互换。旧格式 `functionKeys` 读入时合成为一套。
- **Xbox 键**（`guide`）：手柄图上没有它的标签（`controllerCallouts` 不放它，3D 模型按下时照常点亮），按键列表、快捷键表和层计数不算文件写在它上面的，和功能键一样（`isIgnoredControl`）。`savedForm` 从每个层、common 和那一套里去掉它上面的绑定，所以界面编辑过的配置里不再有它们。
- **手写 `extends`**：界面不创建也不显示，沿用来的键当作自己的键，改了哪个才把哪个写进自己的文件（`inheritance.ts`）。删父配置时守护进程先把子配置写成完整内容；子配置有错误或沿用了父配置的脚本时返回 -32023。
- **配置页**：一个软件对应一个配置，没绑定的用固定默认配置 `default`；顶部开关是 config 的 `enabled`。程序已被别的配置的启用规则选中时先确认，确认后删掉那条规则。新建的配置是空的，名称必填、最多 64 字符、不重名。删除先 `profiles.delete`：拒绝为正在使用（-32012）则切到另一个配置再请求，拒绝为引用（-32013）则先用 `config.put` 删掉引用规则再请求，解决不了就写回并切回原配置；默认配置不可删。导出 `.padmate` 和导入经 `profiles.export`/`import`（上限 4 MiB）；名称被占用（-32021）时 Main 留下文件并返回一次性 token（2 分钟过期，最多 4 份），页面选“替换”或“另存为”。导入 AntiMicroX经 `profiles.importAmgp`，`AmgpReportDialog` 显示报告。
- **脚本页**：脚本库各配置共用，经 `macros.*`（§11）操作，文件只由守护进程写。名称检查 `shared/macro-names.ts` 与 Go 共用 `daemon/testdata/macro-names.json`。
- **设置页**（`SettingsPage.tsx`）：修改立即保存。分区：通用（语言、“手柄设置”一行、登录时启动）、浮层、安全（松开所有按键）、手柄调校（`devices.<DeviceKey>`，含识别和映射向导）、按键判定（`timing`）、高级（`output.sendToOwnWindow`、`output.eventGapMs` 0–50 ms）、更新、关于。保存时读完整 config，只改自己的字段，带 revision。登录时启动经 `startup.get/set`，不写 config.json；开发版返回 -32017。

  **手柄设置子页**（`PadsPage.tsx`、`SettingsPads.tsx`；ADR-0017）由通用里的“手柄设置”一行打开，和日志、诊断一样不是侧边导航的页面。打开时读 `pads.list`，先列连着的手柄，再把其余的收在“以前连接过的手柄”折叠里。每只手柄有自己的功能键（在 `functionKey.options` 里选，默认的有标记）、四个反馈开关和紧急暂停（当前生效的组合和按住时间；“修改”按钮只在手柄连着时出现，打开 `PanicComboDialog`，其中的“恢复默认”拿掉这只手柄自己的组合）。每次改动都经 `setPadSettings`，它和其他 config.json 写入排在同一个队列里，只为那一只手柄发 `pads.set`。config.json 里还有退役的顶层 `functionKey`、`feedback` 或 `safety.panic` 时（`legacy.any`），第一次改动先发 `pads.migrate`，保证没有手柄改变行为，再发这次改动；没有实际变化的改动两者都不发。页面打开时不改写文件。
- **映射向导**：保存写 `devices.<DeviceKey>.mapping`。问 24 个控件，最后是 View 和 Menu，不问 Home（`guide`）。`mapwizard.ts` 是 Go `internal/mapwizard` 的 TypeScript 副本，`mapwizard.test.ts` 读 `daemon/internal/mapwizard/testdata/vectors.json` 要求结果一致，改规则要两边一起改。

### 12.6 诊断

`pages/DiagnosticsPage.tsx`、`activity-model.ts`，设置的子视图。守护进程一侧见 [ADR：诊断](adr/0016-diagnostics.zh-CN.md) 和 §7.8、§13。

- **事件**：store 在连着守护进程期间一直把 `engine.activity` 的 `decision` 和 `unrouted` 记进环形 `activity`（最近 200 条）。

- **原因文字**：`reasonSentences` 表的键是 Go 的 reason 名（`decision.go` 的 `reasonNames`）或“reason.变体”。`activity-model.test.ts` 与 Go 逐个对照，守护进程新增 reason 而这里没有句子时测试失败。
- **延迟**：打开此页才每秒读一次 `getLatency()`；`warn`（样本不少于 50 且 p99 超过 1.5 ms）由守护进程判断，界面不另算。

### 12.7 Overlay 窗口

`main/overlay*.ts`、`preload/overlay.ts`、`renderer/overlay/Overlay.tsx`、`shared/overlay.ts`。决定见 [ADR：Overlay](adr/0014-overlay.zh-CN.md)。

- padmate.exe 的第二个 `BrowserWindow`，主窗口 `ready-to-show` 后创建一次。没有界面就没有 overlay，映射照常。独占全屏程序上看不到它。
- 窗口不抢焦点：透明、无边框、`focusable:false`（`WS_EX_NOACTIVATE`）、`alwaysOnTop`、`setIgnoreMouseEvents`，`showInactive()` 只调一次。`overlay.ts` 和 `overlay-window.ts` 里没有 `show()`、`focus()`、`moveTop()`、`hide()`（`overlay.test.ts` 读源码检查）；显示隐藏靠页面切 `data-visible`。
- 位置：480×160 DIP，主显示器工作区底部或顶部居中，不铺满屏幕（否则被 Windows 当成全屏程序）；显示提示或面板时增高，最高 494。
- 主窗口 `closed` 和 `before-quit` 都销毁 overlay。overlay 就绪后 Main 才订阅含 `hud` 的话题；放弃 overlay（无显示器、建不出、加载失败、第二次崩溃）后去掉订阅，这样脚本的 `host.has("hud")` 为真时屏幕上一定有 overlay。
- 内容：toast、按键提示（来源是 slot 的 `hints`，每 250 ms，最多 16 条）、脚本面板；偏好（提示消息、位置、不透明度）经 `padmate:overlayPrefs` 推给 Main。

### 12.8 自动更新

`main/update-*.ts`、`updater-electron.ts`、`shared/update.ts`、`renderer/update/UpdateDialogs.tsx`。决定见 [ADR：安装包与更新](adr/0018-installer-and-updates.zh-CN.md)。

- 只在界面进程运行时检查（关窗后留在后台，照样检查）。用户同意才下载和安装。只向 `https://padmate.download/` 请求 `latest.yml` 和安装包，站上只有正式版。
- 配置：`NsisUpdater({provider:"generic", url: UPDATE_URL, channel: UPDATE_CHANNEL})`，渠道写死 `latest`，不读 `app-update.yml`（generic 来源在渠道没写时会取版本号的预发布标识，`1.0.3-rc.1` 会去请求 `rc.yml`）。
- `UpdateState` 在 Main，变化推 `padmate:updateState`；`phase`：`idle`、`checking`、`available`、`downloading`、`downloaded`、`installing`；`dialog` 决定开哪个阻塞对话框，初始焦点在“以后再说”。界面不传版本、网址、文件。
- 时机：界面启动 30 秒后，离上次检查满 24 小时才检查，之后每 24 小时一次。手动检查总有对话框。设置存 `<userData>/updates.json`，原子写。
- 安装：先 `closeGuard.settle` 让修改存完，再 `quitAndInstall(true, true)`（静默）；安装程序自己先停守护进程（`padmatectl quit --under`，§15）。
- 开发版 `supported:false`。单实例锁 `requestSingleInstanceLock()` 在设置 userData 之后调用（按 userData 分锁，开发版和 E2E 互不相遇）。

### 12.9 托盘与生命周期

托盘由守护进程创建和管理（§5 的 T-main），不在 Electron 里。

- **关窗只隐藏**：守护进程连着、页面已就绪、不在退出中时，关闭主窗口只 `hide()`（`window-close.ts`），再次启动把窗口取到前面。连接断开后，隐藏的界面立即退出。
- **托盘退出和安装程序退出**（`daemon-exit.ts`、`close-guard.ts`）：守护进程发 `daemon.exiting`，Main 在进事件缓冲前截下。reason 为 `tray` 或 `installer` 时，Main 停止捕获、请渲染进程提交并等保存（最多 1800 ms），再调 `ui.exitReady`（最多等 500 ms），隐藏窗口并退出。保存慢、守护进程不回应或调用失败都拦不住退出。

### 12.10 打包版与开发版的区别

| 项目 | 安装版（`app.isPackaged`） | 开发版（`task app:dev`） | E2E |
|---|---|---|---|
| 数据目录 | 真的 `%APPDATA%\padmate` | `.cache/ui-data` | 私有临时目录 |
| 守护进程 | `--inject --data-dir` | `--dry-run` | `--dry-run`，可加 `--fake-input=ipc` |
| 管道、发现目录、mutex、浏览器数据 | 默认 | 隔离到 `.cache/ui-*` | 私有 |
| 登录时启动 | 可开关 | `startup.set` 返回 -32017 | 同左 |
| 自动更新 | 支持 | `supported:false` | 仅测试模式且有回环 feed 时 |

- **安装版的测试模式**（`main/runtime-options.ts`）：要求 `PADMATE_UI_TEST_PACKAGED=1` 且 `PADMATE_UI_DATA_DIR`、`PADMATE_UI_IPC_DIR`、`PADMATE_UI_USER_DATA` 三个私有目录都设置，缺一项就按真的应用运行（真数据目录加 `--inject`）。测试模式用随包 `resources\bin` 的守护进程和 CLI，以 `--dry-run` 启动、从不 `--inject`；子进程环境设 `PADMATE_PIPE` 和 `PADMATE_IPC_DIR`，`APPDATA`、`LOCALAPPDATA` 指向 `<userData>\daemon-home\`；Main 拒绝 `startup.set`。
- `PADMATE_UI_TEST_UPDATE_URL` 只在测试模式且是回环地址时生效，换掉 `UPDATE_URL`，`quitAndInstall` 换成只记录的函数。
- 不要启动安装版的 padmate.exe（含 `dist/installer/win-unpacked`）：它用真实数据，守护进程带 `--inject`。

### 12.11 生成的类型与无障碍

- `schema/{profile,config,state}.schema.json` 是三份源头。`daemon/tools/codegen` 生成 `daemon/internal/profile/wire_generated.go` 和 `app/src/shared/generated/schema.ts`；`task gen` 重新生成，`task app:gen-check` 检查漂移。`shared/api.ts` 是手写的 `PadmateAPI`。Go 与 TS 两侧靠测试对照（`reasonSentences`、`vectors.json`、`macro-names`），改一边要改另一边。
- 无障碍要求见 [ADR：编辑界面](adr/0013-editor-ui.zh-CN.md)。实现约定：变暗的文字用令牌色而不是 `opacity`；每个样式文件末尾有 `@media (forced-colors: active)`，写了 `Highlight` 背景的同时写 `forced-color-adjust: none`。

## 13. 时间与延迟

**时钟。**

- 引擎不读时间，时间来自 `Input.T` 和 `now` 参数。slot loop、T-output、router 各有一个 `clock.Alarm`：跟随绝对截止时间，截止时间没变时什么也不做，所以测试推进 Fake 时循环的重设不会把截止时间往后推。
- `clock.Real()` 是进程内唯一的真实时钟；Windows 上它每 0.5–2 ms 才跳一次，要微秒精度用 QPC（`latency.Now`）。`clock.Fake` 只在 `Set`、`Advance` 时前进，按截止时间顺序触发定时器；要在每个截止时间依次执行工作，用 `Next` 和 `Set` 逐个推进（simtest 就这样做）。

**基准的端到端延迟。** `L = t_ret − t_sdl`：从 SDL 给边沿打的时间戳（`Event.Time`，由 `latency.CalibrateSDL` 换算到 QPC），到带着这个边沿的键的那次 Send 返回。目标 p99 < 1.5 ms。测法见 §14 的 `TestLatencyLive`。

**产品里的延迟统计。** 量 `L′ = t_ret − t_in`，每次在守护进程里量。`t_in` 是 T-input 在事件返回后读的 `Event.Stamp`（唤醒后重扫读出的事件没有 Stamp，不量）。`slot.Handle` 用 `RemoteArbiter.MarkInput` 把 `t_in` 和 DeviceKey 随命令带到 T-output；Arbiter 在第一次调用 Sender 之后读 `t_ret` 并花掉标记，所以一个边沿只量一次。只算真正发出的按键；不经输入边沿的键（连发、到点才发的键、序列后续步骤、指针移动、脚本发的键等）不算。整体 `--dry-run` 时 Sender 是打印器，照样算，`latency.get` 的 `dryRun` 标出。`latency.Recorder` 按 DeviceKey 各一个预分配的 1000 格环，最多 8 只手柄；写入在 T-output 上、不分配。界面要有 50 个样本才标警告，门槛 `latency.WarnP99` = 1.5 ms。

同一键的最大速率为 25 Hz（§7.6）。

## 14. 测试

### 14.1 分层

| 层 | 位置 | 任务 | 要点 |
|---|---|---|---|
| 纯包单元和黄金测试 | `daemon/internal/*` | `task test` | 引擎、slot、supervisor、profile；必须能在 Linux 上跑 |
| simtest | `internal/simtest` | 被其他测试导入 | fake 时钟加真 Arbiter，结束时 `AssertNoneHeld` |
| fuzz | 各包 `Fuzz*` | `task fuzz` | 目标清单以 `Taskfile.yml` 为准；新增解析器或格式字段要加目标 |
| SDL 虚拟手柄 | `internal/input`、`cmd/*` | `task test:sdl` | 不需要硬件；先 `task sdl` |
| 真实注入 | `internal/injecttest`、`output`、`winhook`、`cmd/*` | `task test:inject` | 仅 Windows；每个事件被钩子吞掉（§14.4） |
| 键盘布局 | `internal/kblayout`、`output` | `task test:layouts` | 本机布局与 `testdata/layouts` fixture |
| 延迟基准 | `internal/latency` 与各包 `Latency*` | `task bench` | 仅 Windows，约 4.5 min，口径见 §13 |
| 前端 Vitest | `app/src/**/*.test.ts(x)`、`app/scripts/*.test.mjs` | `task app:ci` | typecheck、Biome、Vitest、build |
| E2E | `app/tests/e2e` | `task app:e2e` | 真实 Electron 加私有 fake-input 守护进程 |
| 安装包冒烟 | `internal/installersmoke` | `task test:installer` | 安装、覆盖安装、卸载 |
| 文档 | `app/src/renderer/*.test.ts`（user-docs、doc-names） | 在 `task ci` 里 | 见 §14.5 |

通用规则：

- 引擎相关包不导入平台包、不调用 `time.Now`（不变量 2，depguard 连 `_test.go` 一起管），测试用 `clock.Fake`。检查：`cd daemon && GOOS=linux go vet ./...`。
- 每个会按住键的测试结束时执行 `AssertNoneHeld`（不变量 1）。
- 需要 SDL、真实注入、会话的测试缺条件时跳过；设 `PADMATE_REQUIRE_SDL=1`、`PADMATE_REQUIRE_SESSION=1` 改为失败。
- `TestMain` 把 IPC 管道、临时目录、紧急释放事件名换成自己的，不碰用户的真实实例。
- `-race` 需要 cgo，只由远程 CI 的 `go-race-ubuntu` 跑；零分配检查在 `-race` 下跳过。
- 自动验收在本地执行；需要硬件或人判断的验收由维护者手动执行。

### 14.2 黄金测试与 trace

- **slot 黄金**（`internal/slot/testdata/golden/<case>/`）：`profile.json`、`input.trace`、`output.trace`（含最后的 ReleaseAll）、可选 `decisions.jsonl`。更新：`go test ./internal/slot -run TestGolden -update -v`。
- **supervisor 黄金**（`internal/supervisor/testdata/golden/<case>/`）：`profile.json`、`input.jsonl`、可选 `session.txt`、`output.trace`、`slots.txt`。最后什么也不能按着，没有 WARN 及以上的日志。更新：`go test ./internal/supervisor -run TestGolden -update -v`。
- **profile**：`internal/profile/testdata/valid|invalid/*.json` 各有 `.golden`；`examples/profiles/*.json` 必须没有诊断。更新：`go test ./internal/profile -update`、`go test ./cmd/padmatectl -run TestValidateGolden -update`。`cmd/padmated` 的 `TestNoSendInput` 保证只有 `sender_windows.go` 能引用 SendInput（不变量 6）。
- **签名与 PE 检查**：不签名、不装证书、不运行被检查的文件，任何平台都跑。`task test:sign` 在 Windows 本机跑，不在 `ci` 里。

### 14.3 simtest

`internal/simtest` 是输出、引擎、层、脚本测试共用的仿真基座，只有测试导入（depguard）。

- **trace 记法**：一行同时写输入边沿和输出键事件，如 `+south@0 -south@120`、`lefttrigger=0.35@10`。`+name[@t]` 按下，`-name[@t]` 抬起，`name=v[@t]` 是值；`#` 到行尾是注释；t 以毫秒计，省略沿用上一个 token 的时间，不能减小。
- `Recorder` 是 fake 的 `output.Sender`，跟踪物理上按着的键；`SyncRecorder` 加锁，`WaitFor` 最多等 5 s 真实时间，是 simtest 里唯一的真实时间等待。
- `Sim` 把真实 Arbiter 放在 `clock.Fake` 上写入 Recorder，`WithMenuMask` 在两者之间放 MenuMask。`Advance` 在每个定时器和 Arbiter 截止时间处停下；`Settle` 推进到没有截止时间，超过 1 分钟虚拟时间就失败；`LoadScript` 编译并激活临时脚本，输出只能经 Sim 的 Arbiter 进入 Recorder，不会调用 SendInput。
- `simtest.New` 注册 cleanup，结束时执行 `AssertNoneHeld`：Recorder 里物理按着的键、`Arbiter.Held()` 里按着或待重发的键都使测试失败；脚本 runtime 先 Dispose 再检查。

### 14.4 真实注入测试及其钩子规则

仅 Windows，设 `PADMATE_TEST_INJECT=1` 才运行。`task test:inject` 先 `task sdl`，带 `PADMATE_REQUIRE_SDL=1`、`PADMATE_REQUIRE_SESSION=1`，`go test -p 1 -count=1` 跑 `injecttest`、`output`、`winhook`、`cmd/padmatectl`、`cmd/padmated`（`-p 1` 使一个测试的钩子看不到另一个的键）。改了输出、钩子、`injecttest`、`padmatectl send`、padmate-probe 或 padmated 的实机模式时跑。

**规则：每个键和鼠标事件都必须被 LL 钩子吞掉**（不变量 6）：

1. `injecttest` 的钩子只吞带标记、nonce 属于本测试的事件，实体键盘照常可用。
2. 发第一个真实键之前先发金丝雀（未分配的 VK 0x97 的抬起），钩子 3 秒内没吞掉就失败。
3. 测试自己发的键，每批后面跟一个金丝雀，同一次 SendInput 发出（`InjectFenced`）；有事件没被吞掉、数量不对或只发出一部分，立即停止。padmatectl 或 padmated 发的键，等它退出或停下后再发金丝雀。
4. 注入很多批的测试用 `Recorder.WaitOwn(n, timeout)` 做 O(1) 的 fence，批后不跟金丝雀，但第一个真实键之前仍要先发金丝雀。有事件没被吞掉就返回 `ErrUnswallowed` 且之后一直返回；钩子停了返回 `ErrHookStopped`；钩子被系统静默移除时什么也看不到，表现为超时。`WaitOwn` 和 `WaitFence` 共用一个唤醒通道，同一时刻只能有一个 goroutine 调用。
5. 鼠标事件同样处理，另装 `WH_MOUSE_LL` 钩子（`Recorder.MouseCanary`，金丝雀是一对带 nonce 的相对移动 +1/-1）。`InjectFencedMixed` 拒绝没有先 `MouseCanary` 的鼠标批次，也拒绝按下和抬起不在同一批的批次，所以钩子万一漏了只会是一次点击，不会留下按着的键。指针移动用 `InjectFencedMoves`，Unicode 文字用 `UnicodeCanary`。
6. 钩子超时（LowLevelHooksTimeout）时那个键不被吞掉，钩子还可能被静默移除（PITFALLS“键盘输出与布局”），所以每批都要 fence，失败就停止注入。

经 padmate-probe 的端到端测试（如 `TestPadToProbe`）运行 `padmate-probe record --json --swallow …`（probe 自带吞键钩子），测试进程里的 `Backstop` Recorder 作第二道防线。会话类测试只向测试自己的窗口发会话消息，不让本机真的锁屏、睡眠或注销。

其他专项：

- **SDL**（`task test:sdl`）：找不到 SDL3 就跳过。改了 `internal/input`（含 `combo.go`）、`padmatectl monitor`/`record` 或 padmated 的实机模式时跑。
- **布局**（`task test:layouts`，`PADMATE_TEST_LAYOUTS=1`）：把本机布局与 `daemon/testdata/layouts` 比较，两个包用 `-p 1` 依次跑（会话只有一个布局列表）。fixture 由 `task layouts` 生成。
- **延迟基准**（`task bench`，`BENCH_SCALE` 默认 1，最大 10）：顺序跑 `Latency*` 基准、`TestInjectLatency`（单独测 SendInput）、`TestLatencyLive`（虚拟手柄驱动 padmated 实机模式，真实注入）。报告写到 `bin/sendinput.md` 和 `bin/latency.md`，门槛由 `PADMATE_BENCH_GATE` 控制（0 只报告）。改了 input、supervisor、slot、output、engine 的热路径时跑，与上次的报告比较。
- **E2E**（`task app:e2e`，仅 Windows，不在 `task ci` 里）：真实 Electron 加私有目录、私有管道、`--dry-run --fake-input=ipc` 的守护进程，不向系统发键。要求窗口在前台，被占时单独重跑。它会开窗口，要在没人用电脑时跑。
- **安装包冒烟**（`task test:installer INSTALLER=<绝对路径> [UPGRADE=<绝对路径>]`）：检查安装、覆盖安装、卸载，以及守护进程被 `padmatectl quit` 请出、数据和自启项保留。暂写真实 HKCU 卸载项和开始菜单项，不要手动运行 `*-setup.exe`。改了 `daemon.exit`、`padmatectl quit`、`installer.nsh`、`electron-builder.yml` 或安装版界面启动参数后，打包再跑。

### 14.5 CI 任务

**本地 `task ci`**（遇错即停）依次：

1. `vet`：`go vet ./...`
2. `lint`：`golangci-lint run`（配置 `daemon/.golangci.yml`，版本与远程 CI 同为 v2.13）
3. `test`：`go test ./...`
4. `build`：三个命令构建到 `bin/`
5. `pe:check`：`go run ./tools/pecheck -bin ../bin`（仅 Windows）
6. `app:gen-check`：`go run ./tools/codegen -check`
7. `app:ci`：typecheck、lint、test、build

文档测试都在其中：`doc-names.test.ts`、`user-docs.test.ts` 检查用户文档点名的界面名称在界面源码里是相邻的一对中英文（名字表 `app/tests/docs/names.ts` 放在 `src/renderer` 外面），中英文两份文档块结构一致。

不在 `task ci` 里、需要另跑的专项：`test:types`（设 `PADMATE_TEST_TSC` 则普通 `task ci` 也跑）、`test:sdl`、`test:inject`、`test:layouts`、`bench`、`app:e2e`、`test:installer`、`test:sign`、`fuzz`。改了什么要跑哪个：

- 输出、钩子、`internal/injecttest`、`padmatectl send`、padmate-probe 或 padmated 的实机模式：`test:inject`。
- `internal/kblayout` 或 `internal/output/layout_windows.go`：`test:layouts`。
- `internal/input`（含紧急组合的 `combo.go`）、`padmatectl monitor`、`record` 或 padmated 的实机模式：`test:sdl`。
- `internal/winsession` 或 padmated 接上它的代码（`live.go` 的 `startSession`）：`test:inject`（`TestSessionToProbe`）和 `test:sdl`（`TestLiveFlagsSmoke` 用真的 T-session）。
- input、supervisor、slot、output、engine 的热路径：`bench`，与上次的报告比较。
- `daemon.exit`、`padmatectl quit`、`app/build/installer.nsh`、`app/electron-builder.yml` 或安装版界面的启动参数：打包后跑 `test:installer`。
- 升级 goja 或 esbuild、改了 `schema/profile.schema.json` 后跑 `task gen`，否则 `TestGeneratedFilesCurrent`、`TestSchemaCopyCurrent` 失败。

**远程 CI**（`.github/workflows/ci.yml`，push 到 main 和 pull request 触发）：

| 任务 | 环境 | 内容 |
|---|---|---|
| `go-windows` | windows-latest | vet、build、`tools/pecheck`；下载缓存 `SDL3.dll`；`go test ./...`（`PADMATE_REQUIRE_SDL=1`）；`go generate` 后 `git diff --exit-code`；真实注入测试子集（`-p 1`）；延迟冒烟（只在测量出错时失败，数字附到 job summary） |
| `go-race-ubuntu` | ubuntu-latest | `go test -race ./...`，SDL 测试跳过 |
| `lint` | windows-latest | golangci-lint v2.13，`_windows.go` 也被检查 |
| `fuzz` | ubuntu-latest | `fuzz` 目标的子集，各 60 s，失败上传 `fuzz-corpus` |
| `frontend` | windows-latest | Node 24，`npm ci`、`npm run ci`、`codegen -check`、`npm run test:e2e` |

发布另有 `.github/workflows/release.yml`（§15）：构建安装包并跑安装冒烟，核对发布文件，生成草稿 release。`ci.yml` 没有打包任务。

## 15. 构建与打包

这里只写构建结构和约束。理由见 [安装包与更新](adr/0018-installer-and-updates.zh-CN.md)。

### 15.1 可执行文件与 SDL

- `task dev` 用控制台子系统构建；release 和 package 用 `-H=windowsgui`。exe 在隔离的临时目录编译，用固定的纯 Go winres 写图标和版本资源，校验后逐文件发布；构建失败保留旧 exe。

- `SDL3.dll` 放在 exe 旁边，版本和 SHA-256 固定在 `internal/sdlpin`；`task sdl` 下载并校验后写到 `bin/`。升级 SDL 的步骤在 `sdlpin.go` 的注释里，升级后要重跑硬件测试。

### 15.2 便携包

`task package:zip VERSION=…` 生成便携 zip：重新验证两个 exe 的版本和 DLL 哈希，拒绝混合版本；归档路径拒绝穿越、符号链接、保留名和大小写重复。

### 15.3 安装包

`task package:installer VERSION=vX.Y.Z`：版本检查、构建 exe、取固定 `SDL3.dll`、`packageinstaller stage`、`app:build`、electron-builder（`app/scripts/package-installer.mjs`、`app/electron-builder.yml`、`app/build/installer.nsh`）、`packageinstaller verify`。输出在 `dist/installer/`。

- 按用户安装、从不提权，向导式：中英双语的语言对话框、文件夹页、只有“在桌面创建快捷方式”勾选框的一页、进度、完成页。默认 `%LOCALAPPDATA%\Programs\padmate`；向导里装进所选文件夹的 `padmate` 子文件夹（`installerpkg.WizardDir`）。语言和勾选框在 `app/build/padmate-options.nsh`，它的规则和 `installerpkg.InstallerLanguage`、`WritesLanguage`、`DesktopDefault` 一致；electron-builder 不建桌面快捷方式（`createDesktopShortcut: false`），也不问语言（不设 `displayLanguageSelector`）。`padmate.exe` 在根，`resources\bin\` 放 `padmated.exe`、`padmatectl.exe`、`SDL3.dll`。卸载项的 GUID 固定，永不修改。
- **版本的唯一来源**是发布标签 `vMAJOR.MINOR.PATCH[-PRERELEASE]`。Go 可执行文件报告标签（ldflags）；`padmate.exe`、`app.asar` 和安装包用去掉 `v` 的版本（`extraMetadata.version`，不改 `app/package.json`）。`packageinstaller verify` 读回每一处。
- **代码签名**默认不签。`PADMATE_SIGN` 取 `none`、`store`、`pfx`、`azure`；签名模式都带 `forceCodeSigning=true`，`electron-builder.yml` 不含证书、密码、指纹、账号。
- **PE 检查。** `task pe:check` 用 `pecheck` 检查 `bin/padmated.exe` 和 `bin/padmatectl.exe`。`-- -update` 在有意改动后重写清单，评审时看 diff：新增的每一行都是 padmate 多了一个能调用的 Windows 函数。

### 15.4 安装、升级、卸载

`installer.nsh` 在这三种操作开始处：

1. 运行 `padmatectl quit --timeout 10s --under "<安装目录>"`（§11），`--under` 使目录外的 padmated 不被请求退出。
2. quit 失败才运行 `padmatectl release-all`。
3. 等 10 秒让安装目录里的程序退出，还在的 `TerminateProcess`。按进程映像路径是否在安装目录内判断，不按名字，所以目录外的开发版和便携副本不受影响。结束之前若还有 `padmated.exe` 而 `release-all` 没运行过，先运行一次：结束守护进程不会释放它按着的键。

其他规则：

- 完整卸载（不是升级）删除 Run 值（仅当指向本安装目录）、`run` 和 `logs` 文件夹，并询问是否保留 `%APPDATA%\padmate` 里的配置、脚本和设置；静默卸载一律保留。`/D=` 指向放着别的文件的目录时，安装程序以退出码 2 退出（卸载会删整个安装目录）。
- 不是更新的安装在最后：把所选语言写进 `%APPDATA%\padmate\ui\language.json`，除非文件里是设置里选过的语言（不是它自己写的那两行之一）；把它记在安装注册表项的 `Language` 下给卸载程序用；按勾选框建或删 `$DESKTOP\padmate.lnk`。勾选框全新安装时默认勾上，覆盖安装时只有快捷方式在才默认勾上。完整卸载，以及升级时不带 `--keep-shortcuts` 运行的卸载程序，删除桌面快捷方式。
- 静默安装：`/S` 用系统界面语言（各种中文算 zh-CN，其余 en）和勾选框的默认值；`--language=zh-CN` 或 `--language=en` 指定语言（向导里也不再弹语言对话框），`--no-desktop-shortcut` 不建快捷方式，已有的删掉。
- 更新是 `<安装包> --updated /S --force-run`：静默，不带 `/D=`，最后以当前用户启动 `padmate.exe`；不写语言，桌面快捷方式保持原样。更新下载的 `pending\` 在 `%LOCALAPPDATA%\padmate-updater\`：只有完整卸载递归删整个文件夹，因为更新时正在运行的安装程序就在里面。
- **不要直接启动安装版的 `padmate.exe`**：它启动的守护进程带 `--inject`，用真的 `%APPDATA%\padmate`。测试模式见 §12。

### 15.5 发布

- **GitHub 草稿发布**（`.github/workflows/release.yml`）：推 `v*` 标签或手动运行，核对 `latest.yml` 与安装包一致，再 `gh release create --draft`。带 `-` 的版本是预发布。
- **发布到下载站**（`task release:r2 VERSION=vX.Y.Z`，可加 `--dry-run`、`--force`）：由维护者运行，是唯一写 Cloudflare R2 桶 `padmate`（`https://padmate.download/`）的地方，仓库里没有凭据。上传前拒绝：预发布标签、`latest.yml` 与安装包不一致、站上已有这个安装包（发布过的安装包不变）、版本不比站上的新。顺序：安装包、`.sha256`，最后才是 `latest.yml`；上传后从 HTTPS 读回核对。
- **下载页**（`site/`，详见 [site/README.zh-CN.md](../site/README.zh-CN.md)）：Cloudflare Worker `padmate-download-root`，路由恰好是 `padmate.download/` 和 `padmate.download/latest`，绝不是 `/*`，所以 `latest.yml`、安装包和 `.sha256` 由 R2 直接提供，更新不经过它。`/` 给浏览器（`Accept` 含 `text/html`）下载页，中文或英文，GitHub 仓库的星数由 Worker 去取；其他请求得到 302，指向 `latest.yml` 写的安装包，`/latest` 也永远是这个 302。`latest.yml` 经 Cache API 缓存 60 秒，所以新版本一分钟内出现在页面上。页面没有脚本，浏览器不会向其他主机请求任何东西。`task site:deploy` 只在 owner 说“发布”时运行，用发布机上的 wrangler 写线上的 Worker，仓库里没有凭据。

### 15.6 Taskfile 任务

以 `Taskfile.yml` 为准，`task --list` 列出全部，常用任务见 [CLAUDE.md](../CLAUDE.md)。`ci` 不含真实注入、强制 SDL、延迟基准和 Playwright。`test:inject` 先 `sdl`，设 `PADMATE_REQUIRE_SDL=1`、`PADMATE_REQUIRE_SESSION=1`。`bench` 约 4.5 分钟，超时的测试不运行 cleanup，所以测试自己检查截止时间（§14）。`test:installer` 会暂写真实的卸载项和快捷方式，机器上装了 padmate 时拒绝运行。下载站的测试（`site:test`，Node 自带的测试运行器，无依赖）属于 `ci`；`site:dev` 和 `site:dry-run` 不改线上任何东西，`site:qr` 重新生成页面上的打赏二维码（§15.5）。

## 16. ADR

架构决策记录按主题放在 [adr/](adr/README.zh-CN.md)，每篇只写现在有效的决定；决定变了直接改那一篇。
