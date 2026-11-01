# ADR-0002：手柄输入：SDL3、hint、go-sdl3 补丁、Joy-Con、接收器心跳与蓝牙 LE 的 Joy-Con 2

[English](0002-input-sdl.md)

相关：[PITFALLS“SDL 与手柄”](../PITFALLS.zh-CN.md)

## 背景

手柄输入用 SDL 3.4.x，经 go-sdl3（purego，无 cgo）在运行时加载 SDL3.dll。

- Windows 不自带 SDL，用户机器上可能有别的程序带来的其他版本。后台输入、hint 组合和设备映射随版本变化，硬件测试矩阵只对一个版本有效。
- padmate 在后台工作，无窗口无焦点也要收到输入。
- go-sdl3 的绑定有缺失，也有会 panic 或结果错误的。
- 画画时左手常只握一只 Joy-Con，另一只醒来时手里这只不能受影响。
- Switch 协议的 2.4G USB 接收器在手柄关机后仍被 SDL 列着，不报告 removed，关机前按着的键会一直按着。
- SDL 读不了 Joy-Con 2（Switch 2 的 Joy-Con）。SDL 3.4.16 有 Switch 2 驱动，但只支持 USB：走 libusb，而 Joy-Con 2 自己没有 USB 口，只能装在充电握把里用。它的蓝牙路径在上游是个 `FIXME`，直接返回“不支持”。更糟的是，官方发布的 Windows 版 SDL3.dll 编译时没开 libusb，根本没有 Switch 2 驱动。画画时左手单拿一只 Joy-Con 2，没有线也没有握把。
- Joy-Con 2 走蓝牙 LE 上任天堂没有公开的自定义 GATT 协议。它不接受标准蓝牙配对（主机一发起就断开），所以 Windows 配不上它；但 Windows 能对没配对的 LE 设备开 GATT 连接。这只手柄还和它的主机保持配对，padmate 不能动这个配对。

## 决定

1. **随包发布固定版本的 SDL3.dll。** 放在可执行文件旁，版本和 SHA-256 固定在 `internal/sdlpin`，由 `task sdl` 下载校验，不提交。go-sdl3 自带的 `binsdl` 只用于开发测试。安装程序必须先停止守护进程，否则 DLL 被占用。
2. **默认后端 hint，不自行覆盖。** `input.Open` 总是设后台事件允许、禁用 SDL 的信号处理器（Ctrl+C 归 `os/signal`）、Joy-Con 不合并。后端 hint 用 SDL 3.4.16 默认值（HIDAPI、XInput、DirectInput 开）。实测默认路径后台都能收到，关 HIDAPI 后 Switch 类手柄变乱数据，WGI 没有后台输入。其余 preset 只供诊断（`padmatectl monitor --preset`），不进 config.json。环境变量里已有同名 hint 时尊重它。
3. **go-sdl3 缺的函数用 purego 自己补，不 fork。** purego 在 Windows 上不支持按值返回结构体，go-sdl3 对这类函数会写到非法地址。补丁在 `internal/input/sdlext*.go`：在 go-sdl3 已加载的同一模块上找符号，只在 T-input 上调用。能用 Go 做的不绑定。升级 go-sdl3 或 SDL 时重新核对，上游修好的改回直接用。
4. **Joy-Con 不合并、横握。** 每只 Joy-Con 是一个手柄。合并时另一只醒来 SDL 会换设备，按着的键被释放，DeviceKey 也变。不加 Joy-Con 专用控件名（SL/SR 是 SDL 映射成的标准按钮）。要改只用 `--hint` 或环境变量。
5. **不报告幽灵设备。** 手柄打不开、`SDL_GetJoysticks` 也不再列出它时，什么也不报告。
6. **接收器掉线检测。** SDL 没有事件能证明接收器上的手柄还在发报告，所以用 HID 报告当心跳：
   - opt-in：只有 padmated 实机模式开启（默认 2 s）；`monitor`、`record` 和测试显示 SDL 原样。
   - 只看 Pro 协议的 USB 接收器；用 SDL 公开的 HID API 对同一路径再开一个只读句柄，读它不影响 SDL。
   - 只有计时字节在变的完整报告才算心跳。设备在静止时持续收到空闲心跳才“武装”，所以只在变化时发报告的设备永远不武装，按住不动不会被误放。
   - 已武装且静默达到 timeout，就合成 DeviceRemoved。不看有没有控件按着，因为锁存和脚本能在没有控件按着时按住键。
   - 设备变成 dormant 但句柄保持打开（关掉 gamepad 会让 SDL 把设备设回简单模式）。它的下一个按钮或轴事件让它以新 DeviceID 回来，DeviceKey 不变，router 按重连处理（[ADR：多手柄](0003-devices-and-slots.zh-CN.md)）。
7. **Joy-Con 2 由 padmate 自己经蓝牙 LE 读，用 Windows Runtime。**
   - 三层。`internal/input/jc2` 是纯协议：广播、命令、回复、报告、摇杆校准、键位映射和静默看门狗，可在 Linux 上构建和测试。`internal/input/jc2win` 是 Windows 传输（github.com/saltosystems/winrt-go 加 go-ole：无 cgo，无辅助进程）。`internal/input` 里的 hub 跑在 T-input 上，把报告变成和 SDL 后端相同的 `Event`，所以 router、slot 和引擎都不用改。不输出虚拟手柄，SDL 也看不到这些手柄。
   - **不配对，任何形式都不。** padmate 对没配对的手柄开 GATT 连接。不调 Windows 的配对接口，不发任天堂自己的配对命令，不改任何 Windows 设置。手柄和主机的配对从不被碰，所以同一只手柄在主机上照常能用。
   - **发送内容有一份硬白名单。** 写进命令特征的字节只有 `jc2.CheckRequest` 接受的那几条原样请求：固件信息、三次 flash 读（带序列号的信息块、出厂摇杆校准、用户摇杆校准）、特性掩码 `0x03`（按键和摇杆；先设置再启用）、玩家灯。另一种写入是输入报告和命令回复的通知开关（CCCD）。配对命令、flash 写和擦除、flash 里的配对区（主机的密钥在那里）、固件更新、关机、震动都不在名单上。检查在唯一那次写调用之前对最终字节做；测试和 fuzz 测试守住名单和这个调用点。
   - 只订阅各手柄自己的原生侧报告。通用报告要靠 padmate 不发的命令选中才有数据，按键和摇杆也用不着它。
8. **连接策略：只连标准广播，只连用户选定的手柄。**
   - 手柄只在按住 SYNC 键时接受 PC（标准广播，主机地址全 0）。按普通键后它发的是回连主机的广播，PC 连上后 1 ms 内就被断开。所以每次都要按 SYNC，padmate 也从不尝试回连广播。
   - 托盘（和界面）里的“连接新的 Joy-Con 2”打开一个 30 s 的配对窗口：窗口内第一只按住 SYNC 发广播的手柄被连接并记住（地址、序列号、左右）。之后，记住的手柄只要按住 SYNC 发广播就自动连接。窗口以外，不认识的手柄从不连接。“自动连接记住的 Joy-Con 2”的开关默认开；没有记住的手柄、也没开窗口时什么都不扫描。记住的手柄可以忘记。连接失败或被关闭后，该地址几秒内不再尝试，因为反复连接可能让手柄进入冷却。
   - 玩家灯显示这条连接的玩家编号。
9. **一只左和一只右 Joy-Con 2 自动合成一个手柄。** 只要一只左、一只右的 Joy-Con 2 同时连着，就合成一个 `joyconpair` 手柄。其中一只离开时，这一对报告移除（全部松开），剩下那只以单只身份回来。没有开关，也没有 hint。单只默认横握，设了竖握 hint 才竖握，键位沿用 Switch 1 Joy-Con 的映射；右 Joy-Con 2 的 C 键是 `misc2`。Joy-Con 2 从不和 SDL 报告的 Switch 1 Joy-Con 合并，那些仍按决定 4 各自独立。
10. **时序与静默看门狗。**
    - 连上后立刻请求吞吐优先的连接参数（`RequestPreferredConnectionParameters`，Windows 11）。在实测的手柄上，报告间隔从 60 ms 降到 15 ms 并保持。Windows 10 没有这个调用：间隔维持 60 ms，日志里记一笔（尽力而为）。
    - Windows 要约 10 s 才发现链路断了，太久；但手柄按定时器发报告，静止时也发。所以 300 ms 没有报告的链路算 dormant：它的手柄报告移除（全部松开），链路保留。报告恢复后，手柄以新 DeviceID、同一 DeviceKey 重新报告，不用再按 SYNC。静默 3 s 的链路，或 Windows 报告已断开的链路，才真正关闭。偶尔的长间隔最多让按着的键松开一次，不会把手柄断掉。

## 代价

- 跟进 SDL 要自己升版本号和哈希、重跑硬件矩阵。新克隆必须先 `task sdl`。
- 两只 Joy-Con 合用要自己加 hint。其他 Switch 2 手柄（Pro 2、GameCube 手柄）不支持。
- 接收器检测依赖真机行为和 SDL 3.4.16 在 USB 上强制完整报告，升级 SDL 要重新核对。错在防卡键的一边：链路断超过 2 s 会释放按键并报告断开。
- Joy-Con 2 的协议没有官方文档，来自公开的逆向。主机可能给手柄更新固件，固件更新可能改变协议。解码器宽容、白名单精确，所以变化只会表现为手柄连不上，不会写出错的内容。
- 每次连接 Joy-Con 2 都要按 SYNC。手柄用普通键就能唤醒主机，但 padmate 只回应 SYNC。
- Windows 10 上报告间隔维持 60 ms：摇杆每秒约更新 17 次，蓝牙这段平均多约 30 ms 延迟。
- 多两个依赖模块（winrt-go 和 go-ole）。winrt-go 创建的回调数量固定、只创建一次，符合 CLAUDE.md 的不变量 5。

## 放弃的做法

- **用系统里的 SDL。** Windows 没有系统级 SDL，版本不受控，还可能加载到别的程序的副本。
- **cgo 静态链接。** 要维护 C 工具链。
- **发布包里用 `binsdl`。** 版本不经过我们的校验，运行时写 DLL 再加载，容易被杀毒误报。
- **默认启用 RawInput 或 WGI，或只用 XInput。** 没有理由换掉默认；XInput 读不到 PlayStation 和 Switch 手柄。
- **竖握为默认，或加 `--joycon` 参数、`sl`/`sr` 别名。** 模板按横握设计；SL 对应哪个控件取决于左右、握法和是否合并，固定别名总会在某个模式下是错的。
- **等 SDL，或自己编带 libusb 的 SDL。** 上游没有做蓝牙路径的计划，USB 路径又要充电握把。自己编 SDL 会推翻决定 1（用官方资产，不维护自己的工具链），还是读不了蓝牙上的手柄。
- **第三方桥接工具加 ViGEmBus。** 它把 Joy-Con 2 变成虚拟手柄，要装一个已停止维护的内核驱动。虚拟手柄不在 padmate 的范围内（CLAUDE.md），几只手柄在 SDL 看来还长得一样，还要多运行一个程序。
- **转接器。** 没有任何转接器有支持 Joy-Con 2 的文档。
- **和 PC 做任天堂配对**，让普通键而不是 SYNC 就能回连。这可能挤掉手柄配对表里主机的那一项，手柄也许就不能像从前一样连主机。代价是每次按 SYNC。
- **写报告频率描述符**（主机会写的那个）。没人弄清它的含义，它又是往手柄里写，而 Windows 的连接参数已经能做到 15 ms。
- **在回连广播上连接。** Windows 能连上，但手柄 1 ms 内就断开链路。
- **接收器：用连接或电源状态、控件沉默超时、IMU、`SDL_SendGamepadEffect` 探测、切 hint 重建设备。** 连接状态固定 WIRED；沉默超时会误伤按住不动的画手；IMU 不是所有设备都有；探测会阻塞 T-input；切 hint 影响所有 Switch 设备。
