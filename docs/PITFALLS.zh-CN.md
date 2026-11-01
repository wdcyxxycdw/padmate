# 已验证的技术事实

[English](PITFALLS.md)

- 新条目写进对应小节，中英两份同时写；没有合适的小节就加一节。

## SDL 与手柄

### 基本行为与 hint
- **SDL hint**：`input.Open` 在 `SDL_Init` 前设 `SDL_JOYSTICK_ALLOW_BACKGROUND_EVENTS=1`、`SDL_NO_SIGNAL_HANDLERS=1`（否则 SDL 拿走 Ctrl+C）、`SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1`，再设 preset 和 `--hint`。环境变量里的同名 hint 优先。`SDL_Quit` 重置 hint，每次 Open 重设。
- **SDL 行为**：SDL 只给打开的手柄发事件，所以 GAMEPAD_ADDED 时就打开。每个轴的第一个值发两次。虚拟手柄的 `VirtualJoystickDesc.Version` 手填 136，改状态后调 `SDL_UpdateJoysticks`。分离时 SDL 先抬起按住的键、轴回中再发 removed，padmate 仍要 ReleaseAll。同进程内设备 id 不复用，重连靠 DeviceKey 认。guide 键的抬起推迟到按下后 250 ms，其间的按下被丢。

### 设备信息与标识
- **DeviceKey 的依据**：虚拟手柄 serial、path 为空，GUID 是 `ff00`、名字 CRC-16/ARC、名字前 9 字节、`00 76 01`。同名虚拟手柄只能靠 #N 区分；名字前 9 字节相同的两只，SDL 会把后一只报成前一只的名字，测试里让前 9 字节不同。XInput 的 path 是槽位，DeviceKey 不用。

### Joy-Con 合并
读自 SDL 3.4.16 的 `SDL_hidapijoystick.c`、`SDL_hidapi_combined.c`、`SDL_hidapi_switch.c` 和 `SDL_joystick.c`，未在硬件上测过（ADR-0002）。

- **SDL 何时合并**：`SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS` 在 SDL 里默认也是真（`SDL_HIDAPI_combine_joycons`），`input.Open` 仍明确设成 1。环境变量里有同名 hint 时以环境变量为准。只有被 HIDAPI 的 Switch 驱动接管的 Joy-Con 才会合并，虚拟手柄和其他后端的设备永远不合并。`HIDAPI_CreateCombinedJoyCons` 在每次 `HIDAPI_UpdateDeviceList` 末尾循环调用：在 SDL 的设备列表里取第一只还没成对的左 Joy-Con 和第一只右 Joy-Con（Joy-Con Grip 按名字里的 `(L)` 或 `(R)` 算）。它不看是谁的、也不看序列号，不需要手柄处于什么状态。两只左的永远不合并；第三只 Joy-Con 一直单独，直到有伙伴来。
- **合并设备是什么**：一个新的 HIDAPI 设备，有两个子设备，路径 `nintendo_joycons_combined`，名字 `Nintendo Switch Joy-Con (L/R)`，VID 057e、PID 2008（Switch 2 的合并设备另有产品号），任一只是蓝牙则总线按蓝牙。GUID 由这些算出（version 为 0），所以由哪两只组成都一样。**它没有序列号**：`HIDAPI_DriverCombined_OpenJoystick` 拼接的是子设备 `OpenJoystick` 留在 `joystick->serial` 里的序列号，而 Switch 驱动一个都不设（`HIDAPI_JoystickOpen` 在驱动的 `OpenJoystick` 之后才复制 `device->serial`，合并设备又没有），所以 `SDL_GetJoystickSerial` 给 NULL。因此它的 DeviceKey 是 GUID 加固定路径：每一对、每次重连都相同，同时连着两对时才靠 N 区分。单只 Joy-Con 由 GUID（末字节是 Joy-Con 种类）加序列号（MAC 地址 `xx-xx-xx-xx-xx-xx`）标识。
- **第二只连上时**，都在同一次设备列表更新里：第二只像别的设备一样先加入，然后 `HIDAPI_CreateCombinedJoyCons` 创建合并设备，它的 `HIDAPI_JoystickConnected` 先对两个子设备的 joystick 调 `HIDAPI_JoystickDisconnected`，再加入一个新 id 的 joystick。所以 SDL 排进队列的是：第二只 ADDED、第一只 REMOVED、第二只 REMOVED、合并设备 ADDED；T-input 处理第二只的 ADDED 时它已经不在了，什么也不报告（`listed`，ADR-0002 决定 5）。`SDL_PrivateJoystickRemoved` 先调 `SDL_PrivateJoystickForceRecentering`，所以对已打开的 joystick，SDL 在 REMOVED 之前先发按钮抬起并回中各轴。按在第一只 Joy-Con 上的键，由这些抬起事件（Events 可能丢掉它们）和做 `ReleaseAll` 的 `DeviceRemoved` 释放。
- **合并设备里一只断开时**：它的读错误调用 `HIDAPI_JoystickDisconnected`，该函数“总是同时断开父设备”；子设备“走了”（枚举里不再出现，或没有 joystick 也没有句柄）时，`HIDAPI_UpdateDeviceList` 把父设备和两个子设备都删掉。合并设备的 joystick 同样先回中再移除。留下的那只在之后某次扫描里成为新的 HIDAPI 设备，重新初始化（要读设备信息），所以它单独回来，比合并设备消失晚一些，id 是新的，GUID 和序列号还是原来的。Windows 是否还列出睡着的 Joy-Con 的 HID 设备，也就是 SDL 多久才发现它没了，取决于硬件。
- **映射**：合并设备映射为标准布局加 misc1 和 paddle1–4（`SDL_CreateMappingForHIDAPIGamepad`、`SDL_IsJoystickNintendoSwitchJoyConPair`）。`SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS` 对它无效：设备有父设备时，Switch 驱动一律用合并后的报告处理函数，合并设备的映射也不读这个 hint。这个 hint 只决定单只 Joy-Con 怎么握。
- **输出**：合并设备的震动发给两个子设备，左边只收低频部分，右边只收高频部分（`HIDAPI_DriverSwitch_RumbleJoystick`）；LED 和震动能力是两只的并集；`SDL_SendGamepadEffect` 不支持（`HIDAPI_DriverCombined_SendJoystickEffect`）。电量由右边那只报告，取两只中较低的。

### 断连恢复与事件丢失
- **唤醒重扫**：`Reenumerate(ctx)` 以不可丢弃的 `DeviceState` 发布完整状态，要等内部 fence 通过再 `Supervisor.Sync`；router 把快照当 Resync，不当新 press。接收器仍 dormant 时不得用缓存状态复活它。

### 2.4G 接收器与 Switch 协议

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

### 电量读数
按源码写（接收器一条除外）。`SDL_POWERSTATE_ERROR` 在 go-sdl3 里是 `uint32` 4294967295，不要当 -1。电量是 `DevicePower` 事件，不要加进 `DeviceInfo`（要可比较，也是 trace 的格式）。state 里的 `low` 是“提醒已发出、还没重新布防”，不是“电量低”，靠 `episode` 序号认新的一次。

- **2.4G 接收器后面的手柄电量会存在始终为满的情况**：Xbox 模式 CHARGING 100 %，Switch/PS4 模式 CHARGED 100 %。低电提醒永远不会响。

### 各手柄的按键
读自 SDL 3.4.16 的 `SDL_gamepad.c`（`SDL_CreateMappingForHIDAPIGamepad`）和 HIDAPI 驱动，不是真机实测。`DeviceInfo.buttonMask` 就是这些映射；界面按它出标签，样例在 `app/src/renderer/testing/sdl-pads.ts`。

- **DualShock 4**：标准 15 键加 touchpad（触摸板按下），没有麦克风键。触摸位置 padmate 不读。第三方 DS4 的触摸板看它的能力报告；映射里照样有 touchpad，所以界面会给标签，但按下可能没有事件。
- **DualSense**：再加 misc1（麦克风键）。**Edge**：paddle1 右背键、paddle2 左背键、paddle3 右 Fn、paddle4 左 Fn。蓝牙连接、手柄还发简单报告时（报告 0x01 长 10 或 78 字节），驱动只报 PS 键和触摸板按下，麦克风键和 Edge 的四个键要等增强报告才有。
- **Switch Pro**：标准 15 键加 misc1（截图键），Home 是 guide。
- **单只 Joy-Con**：横握（默认）的映射里有 guide，左手那只的 guide 是截图键，右手那只是 Home 键；padmate 不映射 guide，所以横握的左手 Joy-Con 截图键不能用。竖握的左手那只截图键是 misc1。竖握的右手那只也有面键，界面看有没有 rightstick 区分握法。
- **一对 Joy-Con**（左右各一只同时连着）：标准 15 键加 misc1（截图键）和 paddle1–4（右 SR、左 SL、右 SL、左 SR）。
- 触摸位置、陀螺仪和加速度计不是按键，不能映射。Switch 2 的手柄不支持（ARCHITECTURE §7.1）。

### 映射、重新映射和原始摇杆
- 实测：`SDL_AddGamepadMapping` 看不出新加还是更新，要用 `SDL_GetGamepadMappingForID` 读回比较（忽略 `platform`、`crc`）；环境变量 `SDL_GAMECONTROLLERCONFIG` 里有同 GUID 映射时 Add 照样成功但读回的是环境变量里的，所以 `MappingStatus.Applied` 靠读回。
- 实测：`SDL_ReloadGamepadMappings` 拿走所有运行时映射，也读 `SDL_GAMECONTROLLERCONFIG_FILE` 指的文件：先把文件重写成要留的再 Reload。留下的手柄收到 `REMOVED` 加 `REMAPPED`（没有 `ADDED`），所以不能按事件名处理：先看 `SDL_GetJoysticks` 还列不列它，再看 `SDL_IsGamepad`，否则手柄丢了。没动过映射的手柄也收 REMAPPED：映射文本相同就不发 `DeviceRemapped`，否则另一只手柄上按着的修饰键会被复位松开。
- 实测：SDL 重新映射时不发抬起，必须把重映射当复位（`DeviceRemapped`，router 发 `Edge{Reset: true}`），否则 Space 一直按着。

## 键盘输出与布局

### 键盘注入
- **SendInput**：amd64 上 `INPUT` 是 40 字节。用 `KEYEVENTF_SCANCODE`，E0 前缀的键加 `EXTENDEDKEY`。F13–F21 扫描码 0x64–0x6C，F22 0x6D，F23 0x6E，F24 0x76。`KEYEVENTF_UNICODE` 只用于文本。
- **dwExtraInfo 标记**：注入事件带 `"padm"<<32 | nonce`（`output.ExtraInfo`），键盘 LL 钩子收到完整 64 位，测试靠它只吞自己 nonce 的事件。
- **特殊键**（实测）：Pause 按 VK_PAUSE 发（wScan 0x45）；小键盘 0x47–0x53 随 NumLock 翻译，Num0–9、NumDecimal 按 VK 发；`MapVirtualKeyEx` 对导航键、PrintScreen、Pause 返回的扫描码与实体键不同，不能反推。
- **菜单屏蔽**（实测）：单独的 Alt/Win 抬起前，MenuMask 在同一批插入 vk 0xE8 的按下和抬起。屏蔽键不进 Arbiter 和 ledger；`padmatectl send` 和 injecttest 不经过 MenuMask。
- **UIPI**：向更高完整性级别窗口注入静默失败，只检测并提示，不绕过。Win+L、Ctrl+Alt+Del 无法注入。

### held-keys 日志
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

### 指针移动
- `dx`、`dy` 是移动量不是像素：系统按“指针速度”缩放，“提高指针精确度”开着时还过加速曲线（实测 +300 → +600）。padmate 不改这些设置，`mouseSpeed` 的单位是 SendInput 移动量。`dx`、`dy` 都是 0 的移动到不了钩子，引擎不发。

### 媒体键与文字输入
- 媒体键按 VK 发，`wScan` 填 E0 扫描码并带 `EXTENDEDKEY`，不设 `SCANCODE`（`KeyEventFor` 给 `ByVK`）；是有按住状态的普通键。测试里没吞，音量或播放状态就被改动；录入快捷键时捕获钩子不吞实体键，按音量键录入时音量会变。
- Unicode 事件按下和抬起在同一次 Send，不进 Arbiter 键表、held-keys 日志和启动扫除。发送部分失败时抬起立即重发一次，再失败的进 `Arbiter.stuck`，被 `ReleaseOwners` 发出并计入 `Held()`（不变量 1）。
- 测试不要不经 `UnicodeCanary` 就注入 Unicode 按下（漏过去就是前台窗口里打出的字）：先注入 U+FFFF 的抬起，钩子吞掉后才注入按下。

## Win32 与消息循环

- **Win32 绑定**：用 mkwinsyscall 生成，不手写 `NewLazySystemDLL`/`NewProc`；每个包在 `syscall_windows.go` 写 `//sys`，`task gen` 生成 `zsyscall_windows.go`。生成函数在 last error 为 0 时返回 `EINVAL`，判断失败看返回值。DLL 和函数第一次调用时才加载（会分配），钩子回调里要调的先在回调外调一次（`winhook.Start` 先调 `latency.Now`）。
- 进程最后一个 GUI 线程退出时 Windows 销毁全部窗口类，之后 CreateWindowExW 报 "Cannot find window class"，所以 winsession 每次 Start 都在 T-session 上注册窗口类。
- **padmate 自己的窗口也是前台**：托盘菜单和 Electron 界面打开时它们是前台，不过滤会把配置切到默认配置。padmated 按自己的 PID 过滤；已注册的 UI 保持当前配置、不释放，`app.foreground` 照常发布并带 `own:true`。测试用伪造 `winfocus.App` 调 `onForeground`，不切换真实前台。
- **托盘**：`fyne.io/systray` 必须运行在 main 线程。
  - “打开窗口”只在安装版里启动 `padmate.exe`：开发版和测试构建若启动它，开出的是用真实数据、带 `--inject` 的应用。双击由 `tray.Taps` 数，Tap 里不要阻塞。清理 worker 不要调 systray 的 Quit（Register 之前 onExit 是 nil），只向 main 消息队列投 WM_QUIT；失败、panic 和非预期的循环返回必须保留非零退出码。
- **真实前台会改掉测试的配置**：用真实 winsession/winfocus 跑库模式的测试会在启动约 50 ms 后按真实前台选配置，未绑定的前台会把 `--profile-id` 换成 `default`。这类测试在数据目录写 `{"schemaVersion":0,"enabled":false}`；测选择用 `startSelectionLive` 的假前台。
- **命名事件的安全描述符**：`CreateEvent(nil, …)` 用创建者令牌的默认 DACL；按 UAC 文档（没实测），提权令牌的默认 DACL 只给 Administrators 和 SYSTEM，同一用户不提权的进程里打不开 `EVENT_MODIFY_STATE`。所以 `emergency.Listen` 显式设 `D:(A;;0x1f0003;;;<用户 SID>)S:(ML;;NW;;;ME)`（`TestListenSecurity`）。事件已存在时 `CreateEvent` 返回有效句柄同时 err 是 ERROR_ALREADY_EXISTS，先看句柄，否则泄漏。

## 暂停、系统事件与退出

锁屏、睡眠、注销期间的测试只用 fake、虚拟手柄和 `internal/winsessiontest`，不许真的锁屏、睡眠或注销本机；`winsessiontest` 拒绝 `HWND_BROADCAST`、别的进程和别的类的窗口。

### 挂起与恢复
- **挂起期间的合成释放与迟到 held**：锁屏/睡眠期间 SDL 可先发合成 up，解锁后原 held 的 down 才迟到。router 对挂起前和挂起中已 active 的按钮/轴独立隔离：挂起中的 up/回中、空 `DeviceState` 不解除，恢复后要再见一次 active → release/neutral，时间不能解锁；挂起中才动的轴、挂起时才连上的手柄同样进隔离，所以恢复后摇杆回中再推才映射，这是设计。已知缺口：按钮只在挂起中按下、当时没有事件时，迟到 down 和恢复后的新按下分不开。FuzzRouter 的模型必须按同一规则判中立（`frNeutral`、`axisApplies`），改 analog 设置时一起改；`Supervisor.Suspend` 不在事件顺序里，fuzz 的 `frSuspend` 先送一个未知手柄的 `removed(0)` 确保前一个事件已路由完。

### 暂停与紧急组合
- **检测位置与线程**：`input.ComboWatch` 在 T-input 把事件送进通道之前看到它（通道满丢事件也看得到），不要挪到通道后面。`OnCombo` 在 T-input 上，只能调 `output.Loop.Block`，不能调 `Do`（它等 T-output）。`ComboHeld` 不写进 trace，黄金测试和 `--fake-input` 用 `input.ExpandCombos` 插入。
- **暂停的边沿**经 `Edge.Pause` 投递，暂停期间的边沿都带 PauseOn；只让新建的 slot 以暂停启动不够，重启前排着的按下会在恢复后重放（`TestPauseRestart`）。
- **换紧急组合与加锁顺序**：config.json 的 `safety` 变了，只经 `Source.RepickCombos` 在 T-input 上调 `ComboWatch.Repick`，不要在别的 goroutine 动 ComboWatch，也不要为换组合重开 Source。`commitConfigLocked` 里先持有 `configApplyMu` 再取 `profileMu`，反过来会死锁。

### 退出
- **注销、关机与 SIGTERM**（按文档，没实测）：GUI 子系统的 release 版收不到控制台事件，padmated 从 winsession 窗口的 `WM_QUERYENDSESSION`/`WM_ENDSESSION` 得知。`WM_ENDSESSION` 返回后约 5 s 系统强制结束进程，所以最多等 3 s。T-session 调过 `OnStop("session-end")` 时（`liveRun.sessionEnding`）先写完关闭日志再关 T-session；其他时候先关 T-session，收尾日志才不丢。不带 /F 的 taskkill 发 WM_CLOSE，按停止处理；带 /F 不给机会，释放靠 held-keys 日志。
- **释放后的输入清理上限**：supervisor Run 与 `Loop.Stop` 的释放及重试等完之后，feedback/input 尾部共享 1 s 上限；不能把超时包在按键释放外面。超时必须调非阻塞的 `Source.RequestClose`（`SetFeedback` 可能等卡住的 T-input）。超时或清理 worker 的 panic/Goexit 记错误、返回 1，继续写最终日志、关会话窗口；迟到清理不能重开输出。
- **托盘退出的等待**：先关上 `output.BlockExit` 闸门再等界面存完（2 s），期间手柄按下不发键，闸门没有重开的路径；IPC 照常服务，`profiles.put`、`config.put` 仍写盘。`ui.exitReady` 按 `Hub.PublishSync` 返回的会话对号，别用客户端自报的名字。
- **安装程序要守护进程退出**：
  - `Endpoint.Close` 在守护进程开始停止时就删 `daemon.json`，所以 `padmatectl quit` 在发请求前按 pid 打开进程；pid 可能陈旧或被重用，quit 核对映像名是 `padmated.exe`，安装程序传 `--under <安装目录>`，映像路径不在其中的不请求、不等待（`daemon.json` 可能属于开发版、便携副本）。`padmatectl ui-bridge`（界面子进程）占着 `padmatectl.exe`，覆盖安装和卸载必须先让界面退出（`daemon.exiting`）。
  - 不能用 `release-all` 代替 quit：它让用户所有 `--inject` 的 padmated（含装在别处的）释放键并销毁脚本。只在 quit 失败时用；`PADMATE_INSTALLER_TEST_ROOT` 覆盖下不用。等 10 秒后安装目录里还有 `padmated.exe` 且 `release-all` 没运行过，结束它之前先运行一次，因为 `TerminateProcess` 不释放按着的键，完整卸载又会删掉 `held.bin`。

## 引擎、slot 与 supervisor

- **runtime.Goexit**：Goexit 展开时 defer 里的 `recover()` 返回 nil，要用标志区分它和正常返回。`output.Loop` 和 `slot.RunLoop` 都这样做，Goexit 后照样释放按键；supervisor 把 slot loop 的 Goexit 当 panic 并重启 slot。

### 脚本与 slot、supervisor 的衔接
- **首次 setup 的边沿屏障**：有脚本配置时 supervisor 每次 assign 都发 Resync，候选等 slot 完成 Resync 后才启动，否则 worker 可能在旧的全松状态读 `pad.isDown`；热重载加入脚本时 slot 必须用已有快照打开首次启动闸门。
- **持久层与释放**：退出、配置切换、断连必须先保存选择再取消控件，否则 CancelAll 清掉 toggle 后写回空状态。LayerMemory 不做磁盘 I/O；退出 loop 先 `preserveLayers`，刷盘在释放按键后。恢复不含 hold/latch。
- **脚本单边消费**：不能在 slot 里分别按消费结果送 ButtonDown/Up，只消费 release 会留下声明式 hold。脚本在 engine 的 `stepButton` 状态转换点解析，实体与虚拟控件走同一条路径。脚本 `pad` 只能用 slot loop 复制的快照。

### Arbiter 的计时与事件间隔队列
`SetEventGap` 大于 0 时 Arbiter 把同一命令的键事件排队按间隔发出。改 `internal/output` 的 Arbiter 前过一遍，每条都有回归测试或 fuzz 语料。

1. **抬起失败与强制释放**：抬起和重试都失败后 refs 为 0，但物理键可能还按着；键仍在 ledger 时同键 queued tap 必须等。被顶替的排队按下可能已按着，照常发抬起（`mayBeDown`）。只抬起修饰键会触发输入法切换，屏蔽键插在第一个修饰键抬起之前。
2. **共用按下按 owner 计数**：没发出的按下可能被别的 owner 借用，keyState 记 `users`，全部释放才丢，否则别人的 Ctrl+X 变成 X。队列不能无限变长：Repeat 和按住键上的 tap 每键只排一个额外按下，Down 交替比间隔快时并入最后一次（`mergeDown`，fuzz 的 `MergedKeys`）。抬起按无间隔的顺序进队列（`zeroDeadline`），等按下发出再排会让前后组合同时按着。
3. **被取消的 tap 不能留下 hold**：tap 的 hold 先放 `ref.minPress`，正常结束才并进 `minHold`，否则 tap 被强制释放取消后别的 Down 按着的键会在它的 Up 之后继续按住，Ctrl+Num1 变成 Ctrl+Alt+Num1（`TestEventGapCanceledTapHold`）。计时只来自 profile，不靠 `PADMATE_BENCH`；`slot.Advance` 先 Arbiter、再脚本、最后引擎。

### 手势与缓冲
- **松开后才执行的手势不能按住**：`commitMulti` 在 `e.multi[c]` 的副本上执行动作后丢弃副本，`Down` 的组合、连发和按住的层没人结束（不变量 1）。所以 release、tap、多击只能用 tap 模式、不能带 repeat、层动作不能用 hold，由 `profile.Parse` 与 `engine.Compile` 同时检查（`TestParseAndCompileAgree`）；加新手势要么在 `e.ctl[c]` 上执行，要么两处都加规则。`Options.Registry` 的 Level 返回的 binding 不经 Compile，先用 `engine.CheckBinding`。
- **缓冲里的边沿不能被越过**：缓冲不空时，新边沿（含已分发控件的松开）排在末尾按原顺序重放。越过同一控件的边沿会卡键，越过别的控件的会乱序（修饰键先松开丢组合、多击窗口被提前提交）。Pending 控件自己的松开也不能越过：drain 判为 tap 时只执行 tap 动作（`decideTap`），松开留在原位。

### 模拟量与摇杆
- **摇杆设置问引擎，不问 Program**：调校在 `Engine` 里，读用 `Engine.Analog()`。调校变化不能 CancelAll（拖滑块会松键），走 `settleVirtual(now, false, false)`；`Reset` 不清调校。`configNeedsRestart` 只比较被忽略的设备，否则每次拖滑块都提示“需重启”。
- **鼠标模式的摇杆**：`CancelAll` 后仍推着的摇杆要先回死区才恢复（`mouseLock`）；`Reload` 和校准变化不上锁，否则界面自动保存会让指针停住。`inputOnly` 引擎 `Output` 为空，不能调 `Output.Move`/`Jump` 或排 deadline。

### 功能键
- 功能键（`Program.fnKey`，`back` 或 `start`）的边沿在 `stepButton` 里先于 `inputOnly`、`Options.Button` 和缓冲返回，`e.ctl[…]` 永远不是按下的。`cancelAll` 里 `st.down = e.physical[c]` 要排除它；功能键换了的 `Reload` 要清零新功能键原来的 `e.ctl[…]` 并清 `fnActive`。缓冲的边沿带着到达时生效的功能键（`Input.fn`），别在 `press` 里读 `e.fnActive`。

### 序列、决策与 Fuzz
- 取消序列要先标序列再取消点按和文字（`cancelQueued`：先 `endSeq`），否则多发一步。`TapSequence`（KeySpec 数组）不排队、与别的 owner 并行，黄金输出依赖这点；`Sequence` 等之前的结束，`engineOutput.TypeText` 要经 `Sequence`。序列里的等待从上一步物理抬起算，不叠加 `gap`。

- **remap 不清挂起的隔离**：`router.remapped` 只清 `d.state` 和 `hasSnapshot`，不动 `d.recovery`。改成清 `d.recovery`，单元测试、黄金和语料也都照过；`frSeeds` 末尾四条跨 remap 的种子各杀死一种改法，必须仍然通过。

## 延迟与计时

端到端延迟的设计见 [ADR：引擎与线程](adr/0005-engine-and-threads.zh-CN.md)。

- **Windows 计时**：`time.Now` 每 0.5–2 ms 才跳一次，测微秒级用 QPC（`latency.Now`）。Go 的 `time.Sleep(1ms)` 实际约 1.05–1.6 ms；Win32 的 `SleepEx(1)` 通常约 15 ms。要求“QPC 量出的间隔不小于某个等待”的测试不能用 `time.Sleep`，改在 `latency.Now` 上自旋。
- **SDL 时钟**：`SDL_GetTicksNS = (QPC − tick_start)·1e9/freq`，`Event.Time` 可换算回 QPC：`Source.SampleTicks` 取读数，`latency.CalibrateSDL` 定出 tick_start。实测 SDL 计数频率等于 QPC（10 MHz）。

## 脚本

决定见 [ADR：脚本运行时](adr/0010-scripting-runtime.zh-CN.md)。

### goja、esbuild 与类型
- **goja**：支持 ES2015+ 和 async/await，不支持 ESM、顶层 await、async generator、for-await，所以 esbuild 打包成 IIFE。一个 Runtime 同一时刻只能被一个 goroutine 使用。特性表见 `internal/script/compile/goja_features.json`。
  - `Runtime.Interrupt` 在 Go 原生函数内不生效，宿主 API 必须快速返回；没有调用在运行时发出的 Interrupt 会让下一次调用立即失败，每次调用结束都要 `ClearInterrupt`。
  - `Object.Get` 对缺失属性返回 Go 的 nil：带空洞的数组按下标取到 nil，直接 `.Export()`/`.String()` 会 panic，整个 slot 重启。读脚本给的数组元素先判 nil（`hud.go` 的 `stringValue`）。
- **esbuild**：要设 `TsconfigRaw`，否则读根目录外的 tsconfig.json。计算出来的 `import()`/`require()` 路径按 glob 展开，不经 OnResolve，OnLoad 要再检查一次。
- `padmate` 模块加值成员（如 `hud`）时，`compile/plugin.go` 的 `padmateStub` 也要导出，否则 `import { hud } from "padmate"` 打包失败，而只测运行时的测试照过。
- **脚本 API 版本四处同步**：`dtsgen.SupportedAPIs`（当前版本在最前，`HostAPI` 是第一项）、`schema/profile.schema.json` 的 `script.api` enum、`internal/script/host.go` 的 `apiVersion`、d.ts 的 `@since`/`@until`；前三处有测试对齐，schema 副本改源头后跑 `task gen`。d.ts 新增值成员必须放进 `StableMembers` 或 `PreviewMembers`，宏能用的（`@macro`）必须稳定。新版本改了上一版本的成员时要有 shim 和一致性测试。升版本后没升的固定配置会多一条 `/script/api` 警告，使“零诊断”断言失败（`grep -rE '"api": ?"0\.N"'` 找）。

### 宏
- **输出范围**：正常结束不能用 `ReleaseOwner`（会截短没 await 的最后一个 tap，还会松开别的 owner 的键），逐个 `Up`；只有 60 秒上限和安全边界用 `ReleaseOwner`。

### 看门狗
- **QueryThreadCycleTime** 计 TSC 时间，睡眠不计，迁移没有跳变。启动时用 QPC 校准（每组 10 次 1 ms 忙等），循环内不能 `runtime.Gosched()`，否则压低 cycles/µs，把 900 ms 的脚本 CPU 用时高估为超过 1 s。`GetThreadTimes` 粒度 15.6 ms，不用。

### 生命周期、暂停与配置切换
- **暂停不能只 ReleaseKeys**：`every`、`sleep` 和 Promise continuation 仍会由 `RunDue` 重新按键。暂停必须让旧 Host 失效并拒绝候选 swap，恢复时 worker 重建；初始暂停要在 factory 构建前生效，否则 `onActivate` 已输出。
- **owner、teardown 与失效**：cycle 由整个 scriptRuntime 共用，新 `Lifecycle` 不能从 Cycle 1 重编号，否则取消的旧候选的 `Dispose.ReleaseOwner` 会释放同 slot、同 gen 的新键。旧 Host 的 owner 由 slot loop 先释放，再断开输出 sink，才让 worker 执行 teardown。离线紧急释放首次 Send 失败时，supervisor 仍要完成每个 slot 的 ReleaseAll 屏障再报合并错误，否则闸门重开后旧 every/sleep 会再次按键。
- **配置选择与热重载**：更换 profile key 必须在引擎替换和层恢复前停止旧 lifecycle，不能走保留旧脚本的热重载逻辑，否则新脚本 setup 失败后旧配置的 every 仍向新前台输出；旧 teardown 也不能把旧层选择写进新 ID 的层记忆（`TestProfileSwitchDisposesOldScriptWhenNewSetupFails`）。
- **效果按（类、手柄）取消，不分哪个 runtime 放的**：新旧 runtime 同时在时（`Lifecycle.Swap`、`pollReload`），旧的取消若在新 `onActivate` 放效果之后才来，会把新效果一起结束，所以这两处在新的激活之前先调旧 Host 的 `CancelEffects()`。

### 输出
- **tap 完成跨线程**：`output.Loop` 的回调在 T-output 上，只能投递到队列并唤醒 slot，不能调 goja、`Host.RunDue` 或再调 Loop。暂停、卸载、销毁先断输出并放弃旧 Promise；`Host.Dispose` 要在 teardown 前放弃旧 tap Promise。

## UI

设计见 ARCHITECTURE §12，决定见 [ADR：编辑界面](adr/0013-editor-ui.zh-CN.md) 和 [ADR：Overlay](adr/0014-overlay.zh-CN.md)。

### 进程与安全
- **开发隔离**：`task app:dev` 用 `.cache/ui-bin` 私有 daemon 和 `.cache/ui-*` 数据，不能只隔离 data-dir 而仍连用户的发现目录、pipe 或 mutex。daemon 须在 `ownwindow.TrustedPaths` 认可的布局里，否则拒绝 `ui.register`。exe 被占用时先核对路径再关对应实例，不按进程名杀。
- **Electron 安全桥**：Renderer 无 Node 和发现 token。Main 经 `padmatectl ui-bridge` 连管道，首次 UI 注册成功后才显示窗口；不能省略注册，也不能把所有 electron.exe 当本程序。
- `webContents.send` 无背压，Main 限制在途批次，preload 消费后确认；导航开始时暂停发送并回收旧 batch，`did-finish-load` 后才恢复。新 IPC 话题两边一起加（`ipc-wiring.test.ts`）。启动失败页的报告是别的程序写的，Main 只留认得的字段；配置备份只收 Renderer 给的名字，Main 用 `validateConfigBackup` 核对并自己拼路径。
- **自身窗口输出**：保护须在 T-output 命令和 timer 之前，只在页面隐藏输出防不住 SendInput。转换闸门等 slot/script 清理成功才解，检查转换代数和解闸在同一输出线程命令内；释放失败不开闸，转换时不补发持续按住的键。

### 编辑器：保存与草稿
- **单层界面保存**：界面只编辑 `layers.base`，但草稿须是整份 profile，不能按界面模型重建，否则其他层、common、层动作、block、defaultLayer、反馈设置丢失。defaultLayer 不是 base 时整份只读。
- 修改即保存：文本框回车或离开才提交（`useCommitField`），测试里 `change` 后要 `blur` 或回车；换动作类型会先得到带空部分的草稿，保存的是去掉空部分的 `savedForm`。`profile.changed` 引起的重读可能晚于自己的保存，revision 变了就再读一次。
- **守护进程改写 profile.json**：脚本改名、删除、移除旧式脚本由守护进程直接改文件。调用前 `flush()` 等写盘，存不下就不调；调用后 `reload()` 重读草稿，返回的 `profiles[].id` 含它才 `clearHistory(id)`。只换 revision 会让下次自动保存把旧名字写回；该清不清撤销记录，Ctrl+Z 会改回旧名。
- **继承来的内容只用来显示，写回经 `writeBack`**：`draft` 永远是配置自己的文件；`withInherited(draft, parent)` 补入父配置内容，只给手柄图、按键列表、对照表和抽屉读取。把补过的整份 `Profile` 交给 `state.edit` 前须 `writeBack(draft, parent, next)`，否则一次小修改就把父配置抄进子配置。读“保存的形式”的地方都要给 `savedForm` 传 `parent`。

### 编辑器：按键、功能键与摇杆
- **摇杆的两个选中 id**：`leftstick`、`rightstick` 是“旋转”，`leftstick.press`、`rightstick.press` 是“点按”，而绑定键 `leftstick` 是按下摇杆的绑定。绑定键找抽屉经 `calloutFor`，选中 id 找绑定经 `bindingKey`，比较“选中的是不是这个标注”先 `calloutOf`，否则“点按”格没名字、不亮引线。
- **功能键的一套**：编辑功能键的一套时 `swapView` 把它换到 `layers.base` 的位置，凡“看遍所有按键组”的代码都要把 `functionSet` 算进去。哪个键是功能键由 config.json 的 `functionKey` 定，纯函数都带此参数且无默认值，不要用控件名 `back`、`start` 判断。`guide` 永不映射：列出或计数绑定的代码要连同功能键一起把它排除（界面用 `isIgnoredControl`，守护进程用 `control.Control.Unmappable`），保存时去掉（`savedForm`）。脚本解析控件用 `scriptControl` 而不是 `control.Parse`，紧急组合只有 `PanicCombo.Usable` 时才用。
- **映射向导的捕获与清理**：向导开的是留着的捕获：窗口 `blur`、锁屏、睡眠调 `windowAway()`，不停止它；关闭、重载、渲染进程崩溃、退出调 `stop()`，断连调 `disconnected()`。不要把 blur 改成 `stop()`（向导开着时切到别的程序，手柄键会发出快捷键），也不要把别的路径改成 `windowAway()`（窗口关了映射还停着）。
- **快捷键捕获**：start/stop/事件都带 session，卸载时迟到的 start 只能停自己的会话；钩子只观察，自动测试不可真按 Win+L、Ctrl+Alt+Del 或注销。录紧急组合时超时或失败的 `capture.start` 仍可能到达 daemon、`capture.stop` 也可能没发出，所以 `pad-capture.ts` 发过 start 后一直重发 stop 直到确认或断连。

- **按键图标的 SVG**（ADR：[编辑界面](adr/0013-editor-ui.zh-CN.md)）：Biome 也检查 `.svg`，外来图标不加 `<title>`，所以 `app/src/renderer/controller/pad-icons` 在 `biome.json` 里排除。`parseIcon` 只认 `<svg viewBox>` 和 `<path>`，新文件要先按 `pad-icons/README.md` 整理，否则多出的元素会被悄悄丢掉（`pad-icon.test.tsx` 拒绝这种文件）。只有白色、黑色填充换成 `currentColor`，新图标带别的颜色要在 `paint` 里决定怎么换。

### 渲染进程的文件位置
- **样式表的先后**：打包时各样式表按第一次被 import 的顺序进 `index.css`，而这个顺序跟着 Biome 排好的 import 走，挪文件或改 import 路径就可能改变层叠。所以 `App.tsx` 最前面按层叠顺序逐个 import 组件的样式表，`theme.css`、`ui.css`、`shell.css` 在最后。新加组件样式表要排进这个列表；挪动或改 import 后，比较 `npm run build` 前后 `out/renderer/assets/index-*.css` 是否逐字节相同。
- **扫描整个目录的测试留在 `src/renderer/` 第一层**：`forced-colors`、`theme-contrast`、`doc-names` 从自己所在的目录往下扫描，挪进子目录只会扫描那个子目录，别处新加的 `outline: none` 或界面文字不再被查，测试也不会报错。挪样式表时改这两个测试表里的路径。
- **按路径读渲染进程文件的地方**：`padlabel_test.go` 读 `controller/controller-model.ts`；Main 的 `overlay`、`pad-capture`、`system-settings` 测试，`activity-model`、`mapwizard` 测试（读 daemon 的文件，按目录深度写 `../`）。`pad-icon.tsx` 的 `import.meta.glob` 匹配不到时返回空对象，构建不报错，只有 `pad-icon.test.tsx` 能发现。

### 配置库与软件选择
- **E2E 的当前配置**：没绑定的软件会切到默认配置，而 E2E 时前台常是终端。`fixture()` 写 `state.json` 的 `lastProfile:"paint"` 和 `config.json` 的 `enabled:false` 关掉按软件切换；打开开关后用 `profiles.activate` 回到需要的配置。

### E2E
- **前台限制**：录快捷键的辅助进程看系统前台，不是 Electron 焦点。Windows 的前台锁有时不让刚启动的测试窗口拿到前台，`recordShortcut` 10 s 后失败，这是桌面状态：单独重跑，不要靠发按键抢前台（不变量 6）。有人在用电脑时设 `PADMATE_E2E_DISPLAY=secondary`，把窗口挪到第一个非主显示屏。
