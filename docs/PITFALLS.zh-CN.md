# 已验证的技术事实（踩坑记录）

[English](PITFALLS.md)

按代码区域分 10 节，改某块代码前读对应一节。

- “不变量 N”是 CLAUDE.md 里的编号。SDL 的结论除注明外来自 3.4.16 源码；“实测”跑过，“按源码/文档”没跑过。升级 SDL、goja、esbuild、electron-updater 时重新核对。
- 新条目写进对应小节，中英两份同时写；没有合适的小节就加一节。
- 代码注释按英文版的粗体名引用这里（"2.4 GHz receivers""Replacing a file by rename""Windows timing""SDL behavior""What DeviceKey rests on""Effect channels""SDL clock""Win32 bindings"），改名前先搜。

## SDL 与手柄

### 基本行为与 hint
- **SDL hint**：`input.Open` 在 `SDL_Init` 前设 `SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS=1`、`SDL_NO_SIGNAL_HANDLERS=1`（否则 SDL 拿走 Ctrl+C）、`SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0`，再设 preset 和 `--hint`。环境变量里的同名 hint 优先。`SDL_Quit` 重置 hint，每次 Open 重设。
- **SDL 行为**：SDL 只给打开的手柄发事件，所以 GAMEPAD_ADDED 时就打开。每个轴的第一个值发两次。虚拟手柄的 `VirtualJoystickDesc.Version` 手填 136，改状态后调 `SDL_UpdateJoysticks`。分离时 SDL 先抬起按住的键、轴回中再发 removed，padmate 仍要 ReleaseAll。同进程内设备 id 不复用，重连靠 DeviceKey 认（IN-09）。guide 键的抬起推迟到按下后 250 ms，其间的按下被丢。

### 设备信息与标识
- **DeviceKey 的依据**：虚拟手柄 serial、path 为空，GUID 是 `ff00`、名字 CRC-16/ARC、名字前 9 字节、`00 76 01`。同名虚拟手柄只能靠 #N 区分；名字前 9 字节相同的两只，SDL 会把后一只报成前一只的名字，测试里让前 9 字节不同。XInput 的 path 是槽位，DeviceKey 不用。

### 断连恢复与事件丢失（DMN-12）
- **唤醒重扫**：`Reenumerate(ctx)` 以不可丢弃的 `DeviceState` 发布完整状态，要等内部 fence 通过再 `Supervisor.Sync`；router 把快照当 Resync，不当新 press。接收器仍 dormant 时不得用缓存状态复活它。

### 2.4G 接收器与 Switch 协议（IN-30）
Nova 2 Lite 接收器插上后先是 3537:2106 的厂商 HID（SDL 不列出），约 0.6 s 后手柄连上变成 3537:1098。Switch 模式（057e:2009，hidapi switchpro）只要接收器插着就一直被枚举，手柄关机时 SDL 不报 removed；别用 `hidapi-off` 诊断它。

- **2.4G 接收器**：USB 上 HIDAPI 设备 ConnectionState 固定 WIRED，查不出手柄关机。非蓝牙 Switch 手柄沉默 100 ms 只重发 ForceUSB，不断开，按键状态停在关机前；报告只在状态变化时出事件，所以没有事件、没有控件按着都证明不了手柄还在。
- 关闭 gamepad 会让 USB Switch 设备回到简单模式，之后不再正常报告：合成断开之后不要关它。
- 重发是 T-input 上的同步写，每次 `hid_write` 最多等 1 s、试 5 次（没实测），所以不能把“T-input 停过”都当睡眠：看门狗的 grace 每段沉默只给一次。心跳自己读 HID 报告（`DeviceInfo.Path` 给 `SDL_hid_open_path`，只在连接时调；`SDL_hid_read_timeout(…, 0)` 不等待）：完整报告 0x30 的第 3–11 字节是按钮和摇杆，只有它们没变而第 1 字节计时变了，才证明设备在持续报告。

### 反馈效果
- SDL 的 `Rumble` 不排队，连续调用只覆盖前一个；`internal/haptic` 混音器在 T-input 上按步调度，新程序、失联和退出时发停止效果。`SetFeedback` 带目标 `DeviceID`，多手柄时不能广播层状态；0 才是广播。
- **`--dry-run` 也会驱动物理手柄**：层切换和暂停会让真手柄震动亮灯。测试只用假源（`fakeSource` 等）或虚拟手柄；绝不要在会打开真手柄的测试里对 `Device` 0 发 `SetFeedback`（广播给每只）。
- **效果通道**（读自源码，非真机）：
  - LED：`SDL_SetJoystickLED` 在颜色没变且未到 5 s 窗口时直接返回成功，且调驱动后不论成败都记下颜色，所以失败的写之后窗口内再发同色会被当重复；LED 冷却取 7 s（`LEDCooldown`）。
  - 震动：强度与当前相同时 `SDL_RumbleJoystick` 不调驱动，所以相邻“开”步之间、抢占时先发 `Rumble(0,0,0)`；时长 0 而强度不为 0 会一直响，每步限制在 1–5000 ms。Switch HIDAPI 把震动写入合并到每 30 ms 一次，且不支持扳机震动；XInput 只有 Rumble，没有扳机震动、LED、玩家编号，必须按能力发调用。
  - **玩家编号**是 SDL 全进程的一张表：给 B 设 n，原占着 n 的 A 被挪走，所以追逐前用 `Effector.PlayerIndexTaken(n)` 问整张表，只经过没人占的编号；没有玩家灯的手柄不能碰编号。对 SDL 已移除的手柄写编号会把死 instance id 存进表且无人清除，已移除的用 `Mixer.Forget`，不调 `Effector`。

### 电量读数（IN-12）
按源码写（接收器一条除外）。`SDL_POWERSTATE_ERROR` 在 go-sdl3 里是 `uint32` 4294967295，不要当 -1。电量是 `DevicePower` 事件，不要加进 `DeviceInfo`（要可比较，也是 trace 的格式）。state 里的 `low` 是“提醒已发出、还没重新布防”，不是“电量低”，靠 `episode` 序号认新的一次。

- **2.4G 接收器后面的手柄电量是假的满格**（Nova 2 Lite 实测）：Xbox 模式 CHARGING 100 %，Switch/PS4 模式 CHARGED 100 %。低电提醒永远不会响。

### 映射、重新映射和原始摇杆（IN-17、IN-18、IN-19）
- 实测：`SDL_AddGamepadMapping` 看不出新加还是更新，要用 `SDL_GetGamepadMappingForID` 读回比较（忽略 `platform`、`crc`）；环境变量 `SDL_GAMECONTROLLERCONFIG` 里有同 GUID 映射时 Add 照样成功但读回的是环境变量里的，所以 `MappingStatus.Applied` 靠读回。
- 实测：`SDL_ReloadGamepadMappings` 拿走所有运行时映射，也读 `SDL_GAMECONTROLLERCONFIG_FILE` 指的文件：先把文件重写成要留的再 Reload。留下的手柄收到 `REMOVED` 加 `REMAPPED`（没有 `ADDED`），所以不能按事件名处理：先看 `SDL_GetJoysticks` 还列不列它，再看 `SDL_IsGamepad`，否则手柄丢了。没动过映射的手柄也收 REMAPPED：映射文本相同就不发 `DeviceRemapped`，否则另一只手柄上按着的修饰键会被复位松开。
- 实测：SDL 重新映射时不发抬起，必须把重映射当复位（`DeviceRemapped`，router 发 `Edge{Reset: true}`），否则 Space 一直按着。

## go-sdl3 与 purego

- **go-sdl3** v0.1.1（purego，无 cgo）：`SDL_GetGamepadSensorData`、`SDL_GetGamepadTouchpadFinger` 和 GUID 取值函数的 wrapper 直接 panic，没有 HID 绑定。`internal/input/sdlext*.go` 自己补（在 go-sdl3 加载的同一路径上找符号），含 `SDL_hid_open_path/read_timeout/close`（IN-30）。
- **purego 与锁定线程**：v0.10.0 在 unix/amd64 的调用跳板会覆盖 g0 栈帧，cgo 构建里做过 purego 调用的线程退出时 SIGSEGV；goroutine 锁定线程后不解锁就返回线程会退出（T-output、slot loop 都这样）。v0.11.0 起修复，用 v0.11.1，`inputtest.TestLockedThreadExit` 守住。

## 键盘输出与布局

### 键盘注入
- **SendInput**：amd64 上 `INPUT` 是 40 字节。用 `KEYEVENTF_SCANCODE`，E0 前缀的键加 `EXTENDEDKEY`。F13–F21 扫描码 0x64–0x6C，F22 0x6D，F23 0x6E，F24 0x76。`KEYEVENTF_UNICODE` 只用于文本。
- **dwExtraInfo 标记**：注入事件带 `"padm"<<32 | nonce`（`output.ExtraInfo`），键盘 LL 钩子收到完整 64 位，测试靠它只吞自己 nonce 的事件。
- **特殊键**（实测）：Pause 按 VK_PAUSE 发（wScan 0x45）；小键盘 0x47–0x53 随 NumLock 翻译，Num0–9、NumDecimal 按 VK 发；`MapVirtualKeyEx` 对导航键、PrintScreen、Pause 返回的扫描码与实体键不同，不能反推。
- **菜单屏蔽**（实测）：单独的 Alt/Win 抬起前，MenuMask 在同一批插入 vk 0xE8 的按下和抬起。屏蔽键不进 Arbiter 和 ledger；`padmatectl send` 和 injecttest 不经过 MenuMask。
- **UIPI**：向更高完整性级别窗口注入静默失败，只检测并提示（OUT-12），不绕过。Win+L、Ctrl+Alt+Del 无法注入。

### held-keys 日志（OUT-14）
- 日志在每个新按下的 SendInput 之前写。“临时文件 + MoveFileEx”最坏 7.5 ms，所以在打开的句柄上原地改写：`heldfile_windows.go` 自己 CreateFile（不带 `FILE_FLAG_OVERLAPPED`），用带 OVERLAPPED 偏移的 `windows.WriteFile`；OVERLAPPED、计数和缓冲放堆上结构体，局部变量会逃逸分配（`TestHeldLogNoAllocs`）。
- 测试的 heldFile 必须放 TempDir，不碰 `%LOCALAPPDATA%\padmate\run\held.bin`，并同时设 KeyDown（`liveConfig.keyDown`、`LoopOptions.KeyDown`），否则扫除会查本机真实键盘状态。
- **日志头部的身份**：`GetAsyncKeyState` 分不出注入与物理按键，别的开机或登录会话留下的日志会松开用户此刻按着的键，所以只扫除同一身份的日志。身份是 LUID 加登录时间（`LsaGetLogonSessionData`），逐位比较；不要用“现在减 `GetTickCount64`”（时钟会跳、睡眠后漂）。测试的日志要带与 Loop 相同的身份（`output.EncodeHeld`）。

### 真实注入的测试规则
- **LL 钩子超时**（按微软文档，没有实测）：钩子过程超时（LowLevelHooksTimeout，最长 1 s）时那个键不被吞掉，钩子还可能被静默移除，之后注入的键都会到前台程序。所以每批都要 fence，失败就停止注入：用 `InjectFenced`（有鼠标事件时用 `InjectFencedMixed`），批次多时用 O(1) 的 `WaitOwn`（ARCHITECTURE §14）。
- 被吞掉的 CapsLock、NumLock、ScrollLock 仍翻转系统锁定状态，测试要成对按。
- **鼠标按键按下不能单独注入**：钩子没吞就是屏幕上按着的键。按下和抬起放同一批（`InjectFencedMixed`），金丝雀在批尾；不成对的批在发出任何东西前被拒绝。`InjectFenced` 拒绝带鼠标事件的批。

### 鼠标
实测于 Windows 11 x64。

- **按键有按住状态**：每个退出路径都要抬起；held-keys 日志用 VK 0x01、0x02、0x04、0x05、0x06 的槽。同一个键按下着时再按下，应用会当成双击的一半，所以 Arbiter 不重复按下，profile 拒绝 hold 加 typematic 连发。`GetAsyncKeyState` 分不出注入与物理按着的鼠标键，扫除可能为用户正按着的左键注入抬起。
- 鼠标 LL 钩子（`EnableMouse`）对每次移动都调过程：先问 `MouseOptions.Watch`，不关心的事件直接 `CallNextHookEx`（不复制、不入通道、不吞），否则实体鼠标会卡；它装在键盘钩子的线程上，卸钩子先鼠标、再键盘。钩子里 `dwExtraInfo` 只剩低 32 位，`output.IsPadmate` 对鼠标事件恒为 false，改比低 32 位 nonce（`Recorder.mouseOwned`）。
- **鼠标金丝雀**（实测）：`mouseData` 为 0 的 WHEEL 和 dx=dy=0 的 MOVE，SendInput 返回 1 但钩子看不到。所以金丝雀是带 nonce 的一对相对移动（+1 再 -1，`MouseCanaryEvents`），收到两个并都吞掉才算数；漏过去时光标移一个像素又回来。金丝雀 nonce 一批只用一次。

### 指针移动
- `dx`、`dy` 是移动量不是像素：系统按“指针速度”缩放，“提高指针精确度”开着时还过加速曲线（实测 +300 → +600）。padmate 不改这些设置，`mouseSpeed` 的单位是 SendInput 移动量。`dx`、`dy` 都是 0 的移动到不了钩子，引擎不发。

### 媒体键与文字输入（OUT-20、OUT-21）
- 媒体键按 VK 发，`wScan` 填 E0 扫描码并带 `EXTENDEDKEY`，不设 `SCANCODE`（`KeyEventFor` 给 `ByVK`）；是有按住状态的普通键。测试里没吞，音量或播放状态就被改动；录入快捷键时捕获钩子不吞实体键，按音量键录入时音量会变。
- Unicode 事件按下和抬起在同一次 Send，不进 Arbiter 键表、held-keys 日志和启动扫除。发送部分失败时抬起立即重发一次，再失败的进 `Arbiter.stuck`，被 `ReleaseOwners` 发出并计入 `Held()`（不变量 1）。
- 测试不要不经 `UnicodeCanary` 就注入 Unicode 按下（漏过去就是前台窗口里打出的字）：先注入 U+FFFF 的抬起，钩子吞掉后才注入按下。

## Win32 与消息循环

- **Win32 绑定**：用 mkwinsyscall 生成，不手写 `NewLazySystemDLL`/`NewProc`；每个包在 `syscall_windows.go` 写 `//sys`，`task gen` 生成 `zsyscall_windows.go`。生成函数在 last error 为 0 时返回 `EINVAL`，判断失败看返回值。DLL 和函数第一次调用时才加载（会分配），钩子回调里要调的先在回调外调一次（`winhook.Start` 先调 `latency.Now`）。
- 进程最后一个 GUI 线程退出时 Windows 销毁全部窗口类，之后 CreateWindowExW 报 "Cannot find window class"，所以 winsession 每次 Start 都在 T-session 上注册窗口类。
- **padmate 自己的窗口也是前台**（UI-18）：托盘菜单和 Electron 界面打开时它们是前台，不过滤会把配置切到默认配置。padmated 按自己的 PID 过滤；已注册的 UI 按 OUT-25 保持当前配置、不释放，`app.foreground` 照常发布并带 `own:true`。测试用伪造 `winfocus.App` 调 `onForeground`，不切换真实前台。
- **托盘**：`fyne.io/systray` 必须运行在 main 线程。
  - “打开窗口”只在安装版里启动 `padmate.exe`：开发版和测试构建若启动它，开出的是用真实数据、带 `--inject` 的应用。双击（DMN-05）由 `tray.Taps` 数，Tap 里不要阻塞。清理 worker 不要调 systray 的 Quit（Register 之前 onExit 是 nil），只向 main 消息队列投 WM_QUIT；失败、panic 和非预期的循环返回必须保留非零退出码。
- **真实前台会改掉测试的配置**：用真实 winsession/winfocus 跑库模式的测试会在启动约 50 ms 后按真实前台选配置，未绑定的前台会把 `--profile-id` 换成 `default`。这类测试在数据目录写 `{"schemaVersion":0,"enabled":false}`；测选择用 `startSelectionLive` 的假前台。
- **命名事件的安全描述符**（DMN-20）：`CreateEvent(nil, …)` 用创建者令牌的默认 DACL；按 UAC 文档（没实测），提权令牌的默认 DACL 只给 Administrators 和 SYSTEM，同一用户不提权的进程里打不开 `EVENT_MODIFY_STATE`。所以 `emergency.Listen` 显式设 `D:(A;;0x1f0003;;;<用户 SID>)S:(ML;;NW;;;ME)`（`TestListenSecurity`）。事件已存在时 `CreateEvent` 返回有效句柄同时 err 是 ERROR_ALREADY_EXISTS，先看句柄，否则泄漏。

## 暂停、系统事件与退出

锁屏、睡眠、注销期间的测试只用 fake、虚拟手柄和 `internal/winsessiontest`，不许真的锁屏、睡眠或注销本机；`winsessiontest` 拒绝 `HWND_BROADCAST`、别的进程和别的类的窗口。

### 挂起与恢复（DMN-09、DMN-12）
- **挂起期间的合成释放与迟到 held**：锁屏/睡眠期间 SDL 可先发合成 up，解锁后原 held 的 down 才迟到。router 对挂起前和挂起中已 active 的按钮/轴独立隔离：挂起中的 up/回中、空 `DeviceState` 不解除，恢复后要再见一次 active → release/neutral，时间不能解锁；挂起中才动的轴、挂起时才连上的手柄同样进隔离，所以恢复后摇杆回中再推才映射，这是设计。已知缺口：按钮只在挂起中按下、当时没有事件时，迟到 down 和恢复后的新按下分不开。FuzzRouter 的模型必须按同一规则判中立（`frNeutral`、`axisApplies`），改 analog 设置时一起改；`Supervisor.Suspend` 不在事件顺序里，fuzz 的 `frSuspend` 先送一个未知手柄的 `removed(0)` 确保前一个事件已路由完。

### 暂停与紧急组合
- **检测位置与线程**：`input.ComboWatch` 在 T-input 把事件送进通道之前看到它（通道满丢事件也看得到），不要挪到通道后面。`OnCombo` 在 T-input 上，只能调 `output.Loop.Block`，不能调 `Do`（它等 T-output）。`ComboHeld` 不写进 trace，黄金测试和 `--fake-input` 用 `input.ExpandCombos` 插入。
- **暂停的边沿**经 `Edge.Pause` 投递，暂停期间的边沿都带 PauseOn；只让新建的 slot 以暂停启动不够，重启前排着的按下会在恢复后重放（`TestPauseRestart`）。
- **换紧急组合与加锁顺序**（UI-19）：config.json 的 `safety` 变了，只经 `Source.RepickCombos` 在 T-input 上调 `ComboWatch.Repick`，不要在别的 goroutine 动 ComboWatch，也不要为换组合重开 Source。`commitConfigLocked` 里先持有 `configApplyMu` 再取 `profileMu`，反过来会死锁。

### 退出
- **注销、关机与 SIGTERM**（按文档，没实测）：GUI 子系统的 release 版收不到控制台事件，padmated 从 winsession 窗口的 `WM_QUERYENDSESSION`/`WM_ENDSESSION` 得知。`WM_ENDSESSION` 返回后约 5 s 系统强制结束进程，所以最多等 3 s。T-session 调过 `OnStop("session-end")` 时（`liveRun.sessionEnding`）先写完关闭日志再关 T-session；其他时候先关 T-session，收尾日志才不丢。不带 /F 的 taskkill 发 WM_CLOSE，按停止处理；带 /F 不给机会，释放靠 OUT-14 的 held-keys 日志。
- **释放后的输入清理上限**（DMN-10）：supervisor Run 与 `Loop.Stop` 的释放及重试等完之后，feedback/input 尾部共享 1 s 上限；不能把超时包在按键释放外面。超时必须调非阻塞的 `Source.RequestClose`（`SetFeedback` 可能等卡住的 T-input）。超时或清理 worker 的 panic/Goexit 记错误、返回 1，继续写最终日志、关会话窗口；迟到清理不能重开输出。
- **托盘退出的等待**（DMN-22）：先关上 `output.BlockExit` 闸门再等界面存完（2 s），期间手柄按下不发键，闸门没有重开的路径；IPC 照常服务，`profiles.put`、`config.put` 仍写盘。`ui.exitReady` 按 `Hub.PublishSync` 返回的会话对号，别用客户端自报的名字。
- **安装程序要守护进程退出**（PKG-04）：
  - `Endpoint.Close` 在守护进程开始停止时就删 `daemon.json`，所以 `padmatectl quit` 在发请求前按 pid 打开进程；pid 可能陈旧或被重用，quit 核对映像名是 `padmated.exe`，安装程序传 `--under <安装目录>`，映像路径不在其中的不请求、不等待（`daemon.json` 可能属于开发版、便携副本）。`padmatectl ui-bridge`（界面子进程）占着 `padmatectl.exe`，覆盖安装和卸载必须先让界面退出（`daemon.exiting`）。
  - 不能用 `release-all` 代替 quit：它让用户所有 `--inject` 的 padmated（含装在别处的）释放键并销毁脚本。只在 quit 失败时用；`PADMATE_INSTALLER_TEST_ROOT` 覆盖下不用。等 10 秒后安装目录里还有 `padmated.exe` 且 `release-all` 没运行过，结束它之前先运行一次，因为 `TerminateProcess` 不释放按着的键，完整卸载又会删掉 `held.bin`。

## 引擎、slot 与 supervisor

- **runtime.Goexit**：Goexit 展开时 defer 里的 `recover()` 返回 nil，要用标志区分它和正常返回。`output.Loop` 和 `slot.RunLoop` 都这样做，Goexit 后照样释放按键；supervisor 把 slot loop 的 Goexit 当 panic 并重启 slot。

### 脚本与 slot、supervisor 的衔接
- **首次 setup 的边沿屏障**：有脚本配置时 supervisor 每次 assign 都发 Resync，候选等 slot 完成 Resync 后才启动，否则 worker 可能在旧的全松状态读 `pad.isDown`；热重载加入脚本时 slot 必须用已有快照打开首次启动闸门。
- **持久层与释放（PRO-11）**：退出、配置切换、断连必须先保存选择再取消控件，否则 CancelAll 清掉 toggle 后写回空状态。LayerMemory 不做磁盘 I/O；退出 loop 先 `preserveLayers`，刷盘在释放按键后。恢复不含 hold/latch。
- **脚本单边消费（SCR-05）**：不能在 slot 里分别按消费结果送 ButtonDown/Up，只消费 release 会留下声明式 hold。脚本在 engine 的 `stepButton` 状态转换点解析，实体与虚拟控件走同一条路径。脚本 `pad` 只能用 slot loop 复制的快照。

### Arbiter 的计时与事件间隔队列（OUT-10、OUT-11、OUT-17）
`SetEventGap` 大于 0 时 Arbiter 把同一命令的键事件排队按间隔发出。改 `internal/output` 的 Arbiter 前过一遍，每条都有回归测试或 fuzz 语料。

1. **抬起失败与强制释放**：抬起和重试都失败后 refs 为 0，但物理键可能还按着；键仍在 ledger 时同键 queued tap 必须等。被顶替的排队按下可能已按着，照常发抬起（`mayBeDown`）。只抬起修饰键会触发输入法切换，屏蔽键插在第一个修饰键抬起之前。
2. **共用按下按 owner 计数**：没发出的按下可能被别的 owner 借用，keyState 记 `users`，全部释放才丢，否则别人的 Ctrl+X 变成 X。队列不能无限变长：Repeat 和按住键上的 tap 每键只排一个额外按下，Down 交替比间隔快时并入最后一次（`mergeDown`，fuzz 的 `MergedKeys`）。抬起按无间隔的顺序进队列（`zeroDeadline`），等按下发出再排会让前后组合同时按着。
3. **被取消的 tap 不能留下 hold**：tap 的 hold 先放 `ref.minPress`，正常结束才并进 `minHold`，否则 tap 被强制释放取消后别的 Down 按着的键会在它的 Up 之后继续按住，Ctrl+Num1 变成 Ctrl+Alt+Num1（`TestEventGapCanceledTapHold`）。计时只来自 profile，不靠 `PADMATE_BENCH`；`slot.Advance` 先 Arbiter、再脚本、最后引擎。

### 手势与缓冲（MAP-06、MAP-07、MAP-09、OUT-11）
- **松开后才执行的手势不能按住**（MAP-07）：`commitMulti` 在 `e.multi[c]` 的副本上执行动作后丢弃副本，`Down` 的组合、连发和按住的层没人结束（不变量 1）。所以 release、tap、多击只能用 tap 模式、不能带 repeat、层动作不能用 hold，由 `profile.Parse` 与 `engine.Compile` 同时检查（`TestParseAndCompileAgree`）；加新手势要么在 `e.ctl[c]` 上执行，要么两处都加规则。`Options.Registry` 的 Level 返回的 binding 不经 Compile，先用 `engine.CheckBinding`。
- **缓冲里的边沿不能被越过**（MAP-09）：缓冲不空时，新边沿（含已分发控件的松开）排在末尾按原顺序重放。越过同一控件的边沿会卡键，越过别的控件的会乱序（修饰键先松开丢组合、多击窗口被提前提交）。Pending 控件自己的松开也不能越过：drain 判为 tap 时只执行 tap 动作（`decideTap`），松开留在原位。

### 模拟量与摇杆
- **摇杆设置问引擎，不问 Program**（IN-14）：调校在 `Engine` 里，读用 `Engine.Analog()`。调校变化不能 CancelAll（拖滑块会松键），走 `settleVirtual(now, false, false)`；`Reset` 不清调校。`configNeedsRestart` 只比较被忽略的设备，否则每次拖滑块都提示“需重启”。
- **鼠标模式的摇杆**：`CancelAll` 后仍推着的摇杆要先回死区才恢复（`mouseLock`）；`Reload` 和校准变化不上锁，否则界面自动保存会让指针停住。`inputOnly` 引擎 `Output` 为空，不能调 `Output.Move`/`Jump` 或排 deadline。

### 功能键（MAP-27，`engine/function_keys.go`）
- 功能键（`Program.fnKey`，`back`、`start`、`guide` 之一）的边沿在 `stepButton` 里先于 `inputOnly`、`Options.Button` 和缓冲返回，`e.ctl[…]` 永远不是按下的。`cancelAll` 里 `st.down = e.physical[c]` 要排除它；功能键换了的 `Reload` 要清零新功能键原来的 `e.ctl[…]` 并清 `fnActive`。缓冲的边沿带着到达时生效的功能键（`Input.fn`），别在 `press` 里读 `e.fnActive`。

### 序列、决策与 Fuzz
- 取消序列要先标序列再取消点按和文字（`cancelQueued`：先 `endSeq`），否则多发一步。`TapSequence`（KeySpec 数组）不排队、与别的 owner 并行，黄金输出依赖这点；`Sequence` 等之前的结束，`engineOutput.TypeText` 要经 `Sequence`。序列里的等待从上一步物理抬起算，不叠加 `gap`。

- **remap 不清挂起的隔离**：`router.remapped` 只清 `d.state` 和 `hasSnapshot`，不动 `d.recovery`。改成清 `d.recovery`，单元测试、黄金和语料也都照过；`frSeeds` 末尾四条跨 remap 的种子各杀死一种改法，必须仍然通过。

## 延迟与计时

端到端延迟的设计见 [ADR：引擎与线程](adr/0005-engine-and-threads.zh-CN.md)。

- **Windows 计时**：`time.Now` 每 0.5–2 ms 才跳一次，测微秒级用 QPC（`latency.Now`）。Go 的 `time.Sleep(1ms)` 实际约 1.05–1.6 ms；Win32 的 `SleepEx(1)` 通常约 15 ms。要求“QPC 量出的间隔不小于某个等待”的测试不能用 `time.Sleep`，改在 `latency.Now` 上自旋。
- **SDL 时钟**（ENG-11）：`SDL_GetTicksNS = (QPC − tick_start)·1e9/freq`，`Event.Time` 可换算回 QPC：`Source.SampleTicks` 取读数，`latency.CalibrateSDL` 定出 tick_start。实测 SDL 计数频率等于 QPC（10 MHz）。

## 脚本

决定见 [ADR：脚本运行时](adr/0010-scripting-runtime.zh-CN.md)。

### goja、esbuild 与类型
- **goja**（SPK-03）：支持 ES2015+ 和 async/await，不支持 ESM、顶层 await、async generator、for-await，所以 esbuild 打包成 IIFE。一个 Runtime 同一时刻只能被一个 goroutine 使用。特性表见 `internal/script/compile/goja_features.json`。
  - `Runtime.Interrupt` 在 Go 原生函数内不生效，宿主 API 必须快速返回；没有调用在运行时发出的 Interrupt 会让下一次调用立即失败，每次调用结束都要 `ClearInterrupt`。
  - `Object.Get` 对缺失属性返回 Go 的 nil：带空洞的数组按下标取到 nil，直接 `.Export()`/`.String()` 会 panic，整个 slot 重启。读脚本给的数组元素先判 nil（`hud.go` 的 `stringValue`）。
- **esbuild**：要设 `TsconfigRaw`，否则读根目录外的 tsconfig.json。计算出来的 `import()`/`require()` 路径按 glob 展开，不经 OnResolve，OnLoad 要再检查一次。
- `padmate` 模块加值成员（如 `hud`）时，`compile/plugin.go` 的 `padmateStub` 也要导出，否则 `import { hud } from "padmate"` 打包失败，而只测运行时的测试照过。
- **脚本 API 版本四处同步**：`dtsgen.SupportedAPIs`（当前版本在最前，`HostAPI` 是第一项）、`schema/profile.schema.json` 的 `script.api` enum、`internal/script/host.go` 的 `apiVersion`、d.ts 的 `@since`/`@until`；前三处有测试对齐，schema 副本改源头后跑 `task gen`。d.ts 新增值成员必须放进 `StableMembers` 或 `PreviewMembers`，宏能用的（`@macro`）必须稳定。新版本改了上一版本的成员时要有 shim 和一致性测试。升版本后没升的固定配置会多一条 `/script/api` 警告，使“零诊断”断言失败（`grep -rE '"api": ?"0\.N"'` 找）。

### 宏（SCR-50）
- **输出范围**：正常结束不能用 `ReleaseOwner`（会截短没 await 的最后一个 tap，还会松开别的 owner 的键），逐个 `Up`；只有 60 秒上限和安全边界用 `ReleaseOwner`。

### 看门狗
- **QueryThreadCycleTime** 计 TSC 时间，睡眠不计，迁移没有跳变。启动时用 QPC 校准（每组 10 次 1 ms 忙等），循环内不能 `runtime.Gosched()`，否则压低 cycles/µs，把 900 ms 的脚本 CPU 用时高估为超过 1 s。`GetThreadTimes` 粒度 15.6 ms，不用。

### 生命周期、暂停与配置切换
- **暂停不能只 ReleaseKeys**（SCR-04）：`every`、`sleep` 和 Promise continuation 仍会由 `RunDue` 重新按键。暂停必须让旧 Host 失效并拒绝候选 swap，恢复时 worker 重建；初始暂停要在 factory 构建前生效，否则 `onActivate` 已输出。
- **owner、teardown 与失效**（SCR-04）：cycle 由整个 scriptRuntime 共用，新 `Lifecycle` 不能从 Cycle 1 重编号，否则取消的旧候选的 `Dispose.ReleaseOwner` 会释放同 slot、同 gen 的新键。旧 Host 的 owner 由 slot loop 先释放，再断开输出 sink，才让 worker 执行 teardown。离线紧急释放首次 Send 失败时，supervisor 仍要完成每个 slot 的 ReleaseAll 屏障再报合并错误，否则闸门重开后旧 every/sleep 会再次按键。
- **配置选择与热重载**：更换 profile key 必须在引擎替换和层恢复前停止旧 lifecycle，不能走保留旧脚本的热重载逻辑，否则新脚本 setup 失败后旧配置的 every 仍向新前台输出；旧 teardown 也不能把旧层选择写进新 ID 的层记忆（`TestProfileSwitchDisposesOldScriptWhenNewSetupFails`）。
- **效果按（类、手柄）取消，不分哪个 runtime 放的**：新旧 runtime 同时在时（`Lifecycle.Swap`、`pollReload`），旧的取消若在新 `onActivate` 放效果之后才来，会把新效果一起结束，所以这两处在新的激活之前先调旧 Host 的 `CancelEffects()`。

### 输出
- **tap 完成跨线程**（SCR-06）：`output.Loop` 的回调在 T-output 上，只能投递到队列并唤醒 slot，不能调 goja、`Host.RunDue` 或再调 Loop。暂停、卸载、销毁先断输出并放弃旧 Promise；`Host.Dispose` 要在 teardown 前放弃旧 tap Promise。

## 文件、构建与工具

### 仓库与命令行工具
- Go 模块在 `daemon/`：`app/node_modules` 里有带 `.go` 文件的 npm 包，会破坏根目录的 `go build ./...`。SDL3.dll 不提交，版本和 SHA-256 固定在 `internal/sdlpin`。go test 的工作目录是包目录；`PADMATE_BENCH_DIR` 必须是已存在的绝对路径。
- **profile fixture 的字节**：CRLF、无效 UTF-8、UTF-16 的 fixture 在 `.gitattributes` 里标为 `-text`，否则 `* text=auto eol=lf` 可能改掉它们的换行。新增这类 fixture 时也要加进去。

### 文件系统
- **rename 替换文件**（实测）：目标有不带 `FILE_SHARE_DELETE` 的打开句柄时（Go 的 `os.Open`、`os.ReadFile` 就是），Windows 上 `os.Rename` 报 Access is denied。测试按 rename 保存 profile 要重试，watcher 可能正在读。`profilelib.WriteScript` 遇 ErrPermission 重试 10 次、间隔 20 ms，仍失败就保留旧文件。删除同样会失败：保存时删最旧快照（PRO-17）要避开本进程正读的文件，被别的程序打开的留到下次保存再删。
- 直接 `RemoveAll` 配置文件夹遇到被占用的文件会删一半，留下缺 profile.json 的坏配置：`profilelib.Remove` 先在写锁内 rename 成隐藏的 `.tmp-<id>-*` 再删（UI-17）。创建 `abcdefghij.ts` 时 NTFS 同时给短名 `ABCDEF~1.TS`，之后独占创建 `abcdef~1.ts` 报 ErrExist，导出导入都拒绝像短名的项（`isShortNameLike`）。
- **导入的文件没有网络标记**：解出来的文件没有 Zone.Identifier，SmartScreen 不再提示。导出文件只收脚本、JSON 和文本（`archiveFileProblem`）；新加类型前先确认资源管理器不会执行或跟随它（.lnk、.url、.library-ms、`.{CLSID}` 文件夹都会）。

### 配置库与设置文件
- **配置库**（PRO-05/06）：读文件用 `Library.Read`（`os.Root` 加 4 MiB 上限）。迁移输出只在内存，且必须保持文档长度（旧版本记号更长时补空格），因为校验在迁移后的文本上，诊断偏移却按原文报。revision 写入（PRO-14）：profile/config/state 共用一把锁，检查和 rename 在同一临界区；空 revision 只表示不存在；`profiles.put` 返回的新 revision 只表示已保存，applied/problem 才表示运行时是否接受。
- **继承的重载与写回**（PRO-19）：父配置的 watcher 读到新内容就记为看过，`reloadDependentFor` 里读不到子配置就放弃的话这次变化永远丢了。“先写后改文件夹名”的操作失败时用 `undoLocked` 写回，不留快照；用 `putLocked` 每失败一次留两份快照，30 份上限几次就冲光。别的配置的 profile.json 读不了时：删除配置当作不是子配置；宏改名和删除则拒绝，因为读不了的可能引用那个宏。
- **脚本库与迁移**（SCR-51）：迁移改配置引用按字节位置改写，不重新序列化 profile.json；每一步都要能执行两次（先写新文件再删原文件，profile.json 只在 revision 对得上时写）。读不了或被改过的 profile.json 不能硬改名，否则那个配置会在原名下运行库里另一个脚本；`macros.rename`、`macros.delete` 任何一个写失败都要把已写的改回去。
- **设置文件的自动修复**（PRO-29）：读 config.json 出错会写盘（备份加替换），测试一律用临时数据文件夹。替换失败时备份要删，修复后的设置只留在内存，同一份文件（按 revision）不再重试。重放（`--fake-input TRACE`）绝不写盘：新加读 config.json 的入口先问它是不是重放。config.json 读不了（是文件夹、拒绝访问）仍是启动错误，坏 JSON 只触发修复对话框。
- **导入 AntiMicroX 文件**（PRO-21，`internal/amgp`）：AntiMicroX 的 set 是整套替换，padmate 的叠加层会往下查：按住换 set 的层里没绑定的控件要写 `block`；被 `while-held` 指回 set 1 的返回键不算按住的目标，否则 base 层被整个屏蔽。文件里的文字进报告前要过 `shorten`/`clipText`/`cleanText`，否则能写出换行伪造报告行。会让键一直按着的时间必须有上限（不变量 1）：`turbointerval` 为 2147483647 的文件能做出按住几十小时的键，相关值都限制在 `maxKeyDownMs`（10000）。GPL 或无许可的真实文件不进仓库，只经 `PADMATE_AMGP_CORPUS` 本机测。

### IPC 与守护进程启动
- 第二实例（DMN-14）：重复启动不读 state/config、不开本地日志或 SDL，经认证 IPC 转发一次；显式冲突的启动参数拒绝，不能把 dry-run 请求切进 inject 进程。裸 `--fake-input`/`=ipc` 始终 dry-run，自启参数必须保留 fake 标志。
- IPC：jrpc2 客户端的 `OnNotify` 不保序，回调只入有界队列，不能在回调中调用客户端；订阅队列溢出要显式失败；token 从创建临时文件起就要受 ACL 保护。守护进程对不认识的话题会让整个订阅失败，界面 `subscribe` 遇到它时去掉 `daemon.exiting` 重订，以后加话题也要这样退一步。`Hub.Publish` 在调用者的 goroutine 上 `json.Marshal`，热路径（slot、router、T-output）回调不要直接发大负载，决策事件走 `decisionFeed`。
- **登录自启命令长度**（DMN-08）：Run 注册成功不代表登录时会执行超长命令，上限 260 个 UTF-16 单元。长参数用固定用户 startup 目录的内容引用，取消自启不删参数文件。真实 GUI 登录可无标准句柄，`stdout.Stat` 失败时演练 printer 改写 `io.Discard`，不能因此转 inject。
- **开发版不写自启**（UI-19）：开发界面的 daemon 和测试构建也有托盘，曾能覆盖真实的 HKCU Run 值。两道检查：`newStartup` 在这些构建里不创建自启项；`startupControl` 只用实现 `memoryEntry` 的测试自启项。改托盘、自启 RPC 或自启代码时保留两道；测试用 fake，不调 `autostart.New`。

### 打包与安装程序（PKG-*）
- **别直接启动安装版或 `win-unpacked` 里的 `padmate.exe`**（PKG-03）：它用真的 `%APPDATA%\padmate`，守护进程带 `--inject`，会向前台发真实按键。自动化只经测试模式：`PADMATE_UI_TEST_PACKAGED=1` 加 `PADMATE_UI_DATA_DIR`、`PADMATE_UI_IPC_DIR`、`PADMATE_UI_USER_DATA` 三个私有目录，缺一项就是真的应用，脚本要一起设、设完核对。发布版守护进程靠 `PADMATE_PIPE`/`PADMATE_IPC_DIR` 成为私有实例，这两个变量只在测试里设。
- `INSTALLER_BIN=DIR` 把 padmated 和 padmatectl 构建到别处，否则 electron-builder 覆盖运行中的开发版 exe；electron-vite 默认把 `dependencies` 当外部包，而安装版没有 node_modules，所以 `externalizeDepsPlugin` 要排除 electron-updater。
- **冒烟测试不能带 `--force-run`**：静默安装加它时，模板让 Explorer 启动开始菜单的 `padmate.lnk`，新的 `padmate.exe` 不继承测试的环境变量，就是真的应用。`installersmoke` 的 `runCommand` 拒绝它。更新式覆盖安装用 `<安装包> --updated /S`，不带 `/D=`。判断进程是否还在跑用 `cmd.Wait` 的退出通道，不用 `OpenProcess`（进程号会复用）。
- 证书入口：`WIN_CSC_LINK`、`CSC_LINK` 在环境里时“不签”的构建也会签，`signingPlan` 把用不到的证书变量设为空串（不能只删，CLI 会从 `app/electron-builder.env` 补回）。签名模式一律把 `DEBUG` 设为空串（会回显 signtool 命令行含 `/p 密码`），签名构建的输出经 `redact` 逐行过滤。不签 `SDL3.dll`（`CheckTree` 的固定哈希）。
- **安装冒烟测试会写真实的状态**（SPK-07，`task test:installer`）：暂时写真实的 `HKCU\...\Uninstall\<GUID>`、`HKCU\Software\<GUID>` 和开始菜单 `padmate.lnk`，结束时（含失败）删除。机器上已装 padmate 或已有 `padmate.lnk` 时测试拒绝运行。其余指向测试目录，靠 `PADMATE_INSTALLER_TEST_ROOT`；不要手动运行 `*-setup.exe`。测试里的 padmated 一律 `--dry-run --tray=false`。向导用例点真实窗口，完成页“运行 padmate”默认勾着，每个用例必须先取消。
- **Claude 桌面版里的 shell 看到虚拟化的注册表和 AppData**：桌面版是 MSIX 包，它启动的 shell 和子程序（含 `task test:installer`）写 HKCU 进包自己的注册表，写 `%APPDATA%`、`%LOCALAPPDATA%` 进 `%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\`；读是两边合并。所以 `Get-ItemProperty HKCU:\…`、`reg query` 可能看不到真实的键。读真实注册表用 WMI：`Invoke-CimMethod -Namespace root/default -ClassName StdRegProv -MethodName GetStringValue -Arguments @{hDefKey=[uint32]2147483649; sSubKeyName='…'; sValueName='…'}`（`ReturnValue` 0 读到，2 无此键，1 键在值不在）；真实文件用 `Get-CimInstance CIM_DataFile`。没用 WMI 读过之前，不要据此断定安装坏了。

### 签名与 PE 检查（PKG-09、PKG-11）
- electron-builder 在 `afterSign` 之前就签了 `padmate.exe`，而 `stamp-exe-version.mjs` 要改它：钩子用 `ignoreCert` 读，写完版本再调 `context.packager.signIf(file)` 重签；`signIf` 被跳过时返回 `false` 不抛错，所以 `forceCodeSigning` 为真而结果为 `false` 时钩子自己抛错。
- **signtool 导入 PFX 会留下私钥文件**（实测）：每次 `signtool sign /f x.pfx` 都在 `%APPDATA%\Microsoft\Crypto\Keys` 多一个 CNG 密钥容器，PFX 删了也还在。`test:sign` 按内容认出并只删运行前不在的；证书在内存里造，不用会写证书存储的 `New-SelfSignedCertificate`。

### 自动更新与下载站（PKG-10）
更新源是 `https://padmate.download/`（Cloudflare R2 加一个 Worker），electron-updater 用 generic 来源。

- **下载站**：
  - 上传顺序：安装包、`.sha256`、最后 `latest.yml`；发布过的安装包不覆盖、不删除（`latest.yml` 带 SHA-512，Cloudflare 默认缓存 `.exe`），要改就发新版本。
  - `electron-builder.yml` 的 `publish` 必须写 `channel: latest`：generic 来源没写渠道时取版本号第一个预发布标识当渠道，`v1.0.3-rc.1` 会烤进 `channel: rc`，客户端就请求 R2 没有的 `/rc.yml`。
- **Electron 的单实例锁按 userData 目录分**：`app.requestSingleInstanceLock()` 要在 `app.setPath("userData", …)` 之后调用。第二个实例在模块加载时就 `app.quit()`，`whenReady` 回调要先看有没有拿到锁，别让它起守护进程。
- **electron-updater 6.8.9**（升级时重新核对）：`autoDownload`、`autoInstallOnAppQuit` 默认 true（后者会在关界面时悄悄运行安装程序、停守护进程），都要显式关；`channel` 的 setter 会把 `allowDowngrade` 设成 true，从不设它。`quitAndInstall` 先启动安装程序再 `setImmediate(app.quit)`，界面存盘必须在调用之前。
  - 写桶的令牌不能进仓库、`electron-builder.env`、`release.yml` 或日志。


## UI

设计见 ARCHITECTURE §12，决定见 [ADR：编辑界面](adr/0013-editor-ui.zh-CN.md) 和 [ADR：Overlay](adr/0014-overlay.zh-CN.md)。

### 进程与安全
- **打包版界面会发键**：`app.isPackaged` 时 Main 用 `--inject` 启动守护进程，开发版、harness、E2E 是 `--dry-run`（`runtime-options.ts`）。自动测试只用开发版。登录时启动的命令由守护进程定，界面只传布尔值，不拼 exe 和参数。
- **开发隔离**：`task app:dev` 用 `.cache/ui-bin` 私有 daemon 和 `.cache/ui-*` 数据，不能只隔离 data-dir 而仍连用户的发现目录、pipe 或 mutex。daemon 须在 `ownwindow.TrustedPaths` 认可的布局里，否则拒绝 `ui.register`。exe 被占用时先核对路径再关对应实例，不按进程名杀。
- **Electron 安全桥**：Renderer 无 Node 和发现 token。Main 经 `padmatectl ui-bridge` 连管道，首次 UI 注册成功后才显示窗口；不能省略注册，也不能把所有 electron.exe 当本程序。
- `webContents.send` 无背压，Main 限制在途批次，preload 消费后确认；导航开始时暂停发送并回收旧 batch，`did-finish-load` 后才恢复。新 IPC 话题两边一起加（`ipc-wiring.test.ts`）。启动失败页的报告是别的程序写的，Main 只留认得的字段；配置备份只收 Renderer 给的名字，Main 用 `validateConfigBackup` 核对并自己拼路径。
- **自身窗口输出**（OUT-25）：保护须在 T-output 命令和 timer 之前，只在页面隐藏输出防不住 SendInput。转换闸门等 slot/script 清理成功才解，检查转换代数和解闸在同一输出线程命令内；释放失败不开闸，转换时不补发持续按住的键。

### 编辑器：保存与草稿
- **单层界面保存**（UI-39）：界面只编辑 `layers.base`，但草稿须是整份 profile，不能按界面模型重建，否则其他层、common、层动作、block、defaultLayer、反馈设置丢失。defaultLayer 不是 base 时整份只读。
- 修改即保存（UI-43）：文本框回车或离开才提交（`useCommitField`），测试里 `change` 后要 `blur` 或回车；换动作类型会先得到带空部分的草稿，保存的是去掉空部分的 `savedForm`。`profile.changed` 引起的重读可能晚于自己的保存，revision 变了就再读一次。
- **没收到回答的保存可能已写入**：`profiles.put` 桥接 10 s 放弃（无 code）时文件可能已写入而 revision 过时，再保存得到冲突；不能当成别处修改而重读磁盘，那会丢掉期间的新修改。`editor-store` 记下没收到回答的 put 文本（`unconfirmed`），磁盘内容与其一致就是自己写的；带 code 的错误是明确拒绝，不记。
- **守护进程改写 profile.json**（SCR-50、SCR-51）：脚本改名、删除、移除旧式脚本由守护进程直接改文件。调用前 `flush()` 等写盘，存不下就不调；调用后 `reload()` 重读草稿，返回的 `profiles[].id` 含它才 `clearHistory(id)`。只换 revision 会让下次自动保存把旧名字写回；该清不清撤销记录，Ctrl+Z 会改回旧名。
- **继承来的内容只用来显示，写回经 `writeBack`**（PRO-19）：`draft` 永远是配置自己的文件；`withInherited(draft, parent)` 补入父配置内容，只给手柄图、按键列表、对照表和抽屉读取。把补过的整份 `Profile` 交给 `state.edit` 前须 `writeBack(draft, parent, next)`，否则一次小修改就把父配置抄进子配置。读“保存的形式”的地方都要给 `savedForm` 传 `parent`。

### 编辑器：按键、功能键与摇杆
- **摇杆的两个选中 id**：`leftstick`、`rightstick` 是“旋转”，`leftstick.press`、`rightstick.press` 是“点按”，而绑定键 `leftstick` 是按下摇杆的绑定。绑定键找抽屉经 `calloutFor`，选中 id 找绑定经 `bindingKey`，比较“选中的是不是这个标注”先 `calloutOf`，否则“点按”格没名字、不亮引线。
- **功能键的一套**（UI-30）：编辑功能键的一套时 `swapView` 把它换到 `layers.base` 的位置，凡“看遍所有按键组”的代码都要把 `functionSet` 算进去。哪个键是功能键由 config.json 的 `functionKey` 定，纯函数都带此参数且无默认值，不要用控件名 `back`、`start` 判断。
- **映射向导的捕获与清理**（IN-19）：向导开的是留着的捕获：窗口 `blur`、锁屏、睡眠调 `windowAway()`，不停止它；关闭、重载、渲染进程崩溃、退出调 `stop()`，断连调 `disconnected()`。不要把 blur 改成 `stop()`（向导开着时切到别的程序，手柄键会发出快捷键），也不要把别的路径改成 `windowAway()`（窗口关了映射还停着）。
- **快捷键捕获**（UI-19）：start/stop/事件都带 session，卸载时迟到的 start 只能停自己的会话；钩子只观察，自动测试不可真按 Win+L、Ctrl+Alt+Del 或注销。录紧急组合时超时或失败的 `capture.start` 仍可能到达 daemon、`capture.stop` 也可能没发出，所以 `pad-capture.ts` 发过 start 后一直重发 stop 直到确认或断连。

- **按键图标的 SVG**（ADR：[编辑界面](adr/0013-editor-ui.zh-CN.md)）：Biome 也检查 `.svg`，外来图标不加 `<title>`，所以 `app/src/renderer/controller/pad-icons` 在 `biome.json` 里排除。`parseIcon` 只认 `<svg viewBox>` 和 `<path>`，新文件要先按 `pad-icons/README.md` 整理，否则多出的元素会被悄悄丢掉（`pad-icon.test.tsx` 拒绝这种文件）。只有白色、黑色填充换成 `currentColor`，新图标带别的颜色要在 `paint` 里决定怎么换。

### 渲染进程的文件位置
- **样式表的先后**：打包时各样式表按第一次被 import 的顺序进 `index.css`，而这个顺序跟着 Biome 排好的 import 走，挪文件或改 import 路径就可能改变层叠。所以 `App.tsx` 最前面按层叠顺序逐个 import 组件的样式表，`theme.css`、`ui.css`、`shell.css` 在最后。新加组件样式表要排进这个列表；挪动或改 import 后，比较 `npm run build` 前后 `out/renderer/assets/index-*.css` 是否逐字节相同。
- **扫描整个目录的测试留在 `src/renderer/` 第一层**：`forced-colors`、`theme-contrast`、`doc-names` 从自己所在的目录往下扫描，挪进子目录只会扫描那个子目录，别处新加的 `outline: none` 或界面文字不再被查，测试也不会报错。挪样式表时改这两个测试表里的路径。
- **按路径读渲染进程文件的地方**：`padlabel_test.go` 读 `controller/controller-model.ts`；Main 的 `overlay`、`pad-capture`、`system-settings` 测试，`activity-model`、`mapwizard` 测试（读 daemon 的文件，按目录深度写 `../`）。`pad-icon.tsx` 的 `import.meta.glob` 匹配不到时返回空对象，构建不报错，只有 `pad-icon.test.tsx` 能发现。

### 配置库与软件选择
- **E2E 的当前配置**：没绑定的软件会切到默认配置，而 E2E 时前台常是终端。`fixture()` 写 `state.json` 的 `lastProfile:"paint"` 和 `config.json` 的 `enabled:false` 关掉按软件切换；打开开关后用 `profiles.activate` 回到需要的配置。

### 脚本导入与外部程序
- **脚本导入与打开文件夹**：Renderer 拿不到文件系统路径。确认替换时只写 Main 第一次读入的内容（一次性 token），不重新弹框，不接受 Renderer 给的路径，否则确认的和写入的可能不是同一个文件。Node 的 fs 错误消息带绝对路径（含用户名），Main 里每个 fs 失败都换成固定消息；daemon 未分类错误（-32098）同理。新建脚本的 main.ts 不能带生效的绑定，否则配置一引用，那个键就被脚本接管。

### E2E
- **前台限制**：录快捷键的辅助进程看系统前台，不是 Electron 焦点。Windows 的前台锁有时不让刚启动的测试窗口拿到前台，`recordShortcut` 10 s 后失败，这是桌面状态：单独重跑，不要靠发按键抢前台（不变量 6）。有人在用电脑时设 `PADMATE_E2E_DISPLAY=secondary`，把窗口挪到第一个非主显示屏。
- **git worktree 里的 E2E**：worktree 的 `app/node_modules` 若是指向主仓库的 junction，electron.exe 路径会被解析到 junction 目标，`ownwindow.TrustedPaths` 拒绝注册，测试等到超时。worktree 要有真实的 node_modules（`npm ci` 或复制）。

### Overlay
- **不能抢前台**：overlay 被激活就等于 padmate 在前台，守护进程会停止向画画软件发键（OUT-25）。只经 `showInactive()` 显示，位置只许 `setBounds`，`overlay.ts`、`overlay-window.ts` 里不许有 `show()`、`focus()`、`moveTop()`、`hide()`（`overlay.test.ts` 读源码检查）。
- **关窗只隐藏**：连着守护进程时主窗口 `close` 被改成 `hide()`（`closeHides`）。任何真要退出的路径，在调 `window.close()` 前都要让 `quitting`、`trayExiting` 或 `quitHeld` 之一为真，否则窗口藏起来、退出取消、进程留在后台。overlay 活着时还要在主窗口的 `closed` 和 `before-quit` 里销毁它。
