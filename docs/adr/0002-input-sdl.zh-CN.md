# ADR-0002：手柄输入：SDL3、hint、go-sdl3 补丁、Joy-Con 与接收器心跳

[English](0002-input-sdl.md)

相关：[PITFALLS“SDL 与手柄”](../PITFALLS.zh-CN.md)

## 背景

手柄输入用 SDL 3.4.x，经 go-sdl3（purego，无 cgo）在运行时加载 SDL3.dll。

- Windows 不自带 SDL，用户机器上可能有别的程序带来的其他版本。后台输入、hint 组合和设备映射随版本变化，硬件测试矩阵只对一个版本有效。
- padmate 在后台工作，无窗口无焦点也要收到输入。
- go-sdl3 的绑定有缺失，也有会 panic 或结果错误的。
- 单独一只 Joy-Con 是横握的小手柄（画画时左手常只握一只）。左右两只同时连着，可以当一个完整手柄用，SDL 的做法是把两个设备换成一个合并设备。
- Switch 协议的 2.4G USB 接收器在手柄关机后仍被 SDL 列着，不报告 removed，关机前按着的键会一直按着。

## 决定

1. **随包发布固定版本的 SDL3.dll。** 放在可执行文件旁，版本和 SHA-256 固定在 `internal/sdlpin`，由 `task sdl` 下载校验，不提交。go-sdl3 自带的 `binsdl` 只用于开发测试。安装程序必须先停止守护进程，否则 DLL 被占用。
2. **默认后端 hint，不自行覆盖。** `input.Open` 总是设后台事件允许、禁用 SDL 的信号处理器（Ctrl+C 归 `os/signal`）、合并 Joy-Con（决定 4）。后端 hint 用 SDL 3.4.16 默认值（HIDAPI、XInput、DirectInput 开）。实测默认路径后台都能收到，关 HIDAPI 后 Switch 类手柄变乱数据，WGI 没有后台输入。其余 preset 只供诊断（`padmatectl monitor --preset`），不进 config.json。环境变量里已有同名 hint 时尊重它。
3. **go-sdl3 缺的函数用 purego 自己补，不 fork。** purego 在 Windows 上不支持按值返回结构体，go-sdl3 对这类函数会写到非法地址。补丁在 `internal/input/sdlext*.go`：在 go-sdl3 已加载的同一模块上找符号，只在 T-input 上调用。能用 Go 做的不绑定。升级 go-sdl3 或 SDL 时重新核对，上游修好的改回直接用。
4. **左右两只 Joy-Con 同时连着就是一个手柄，单只横握。** `input.Open` 自己设 `SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=1`（SDL 的默认值也是它，但 padmate 不依赖默认值）。左右各一只同时连着时，SDL 把两只换成一个合并设备（类型 `joyconpair`，标准布局加 misc1 和 paddle1–4），padmate 映射的是它；只连着一只时，它是自己的一个手柄，横握。这是自动的：没有开关、没有设置项，config.json 也没有对应字段，SDL 会合并的各代 Joy-Con 规则相同。SDL 靠换设备来合并，第二只连上时（单只被移除、合并设备加入）和合并设备里一只断开时（合并设备被移除，留下的那只单独回来，DeviceID 是新的、DeviceKey 是原来的）都是如此。换设备就是一次普通的移除加一次加入：`DeviceRemoved` 做 `ReleaseAll`，按着的键被释放，映射在新设备上继续，新设备像新连上的手柄一样取空闲的槽。不加 Joy-Con 专用控件名（SL/SR 是 SDL 映射成的标准按钮）。`--hint SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0` 或同名环境变量能让所有 Joy-Con 保持分开，只用于诊断。
5. **不报告幽灵设备。** 手柄打不开、`SDL_GetJoysticks` 也不再列出它时，什么也不报告。
6. **接收器掉线检测。** SDL 没有事件能证明接收器上的手柄还在发报告，所以用 HID 报告当心跳：
   - opt-in：只有 padmated 实机模式开启（默认 2 s）；`monitor`、`record` 和测试显示 SDL 原样。
   - 只看 Pro 协议的 USB 接收器；用 SDL 公开的 HID API 对同一路径再开一个只读句柄，读它不影响 SDL。
   - 只有计时字节在变的完整报告才算心跳。设备在静止时持续收到空闲心跳才“武装”，所以只在变化时发报告的设备永远不武装，按住不动不会被误放。
   - 已武装且静默达到 timeout，就合成 DeviceRemoved。不看有没有控件按着，因为锁存和脚本能在没有控件按着时按住键。
   - 设备变成 dormant 但句柄保持打开（关掉 gamepad 会让 SDL 把设备设回简单模式）。它的下一个按钮或轴事件让它以新 DeviceID 回来，DeviceKey 不变，router 按重连处理（[ADR：多手柄](0003-devices-and-slots.zh-CN.md)）。

## 代价

- 跟进 SDL 要自己升版本号和哈希、重跑硬件矩阵。新克隆必须先 `task sdl`。
- 第二只 Joy-Con 连上时，以及合并设备里一只断开时，设备会被换掉：按着的都被释放，手柄会短暂消失（留下的那只要等 SDL 下一次扫描才被重新找到）。按设备保存的状态跟着 DeviceKey 走，所以合并设备有自己的调校和层状态，单只 Joy-Con 回来时拿回它自己的。两只 Joy-Con 不能当两个手柄给两个人用（只有诊断用的 hint 能做到）。Switch 2 的 Joy-Con 不支持。
- 接收器检测依赖真机行为和 SDL 3.4.16 在 USB 上强制完整报告，升级 SDL 要重新核对。错在防卡键的一边：链路断超过 2 s 会释放按键并报告断开。

## 放弃的做法

- **用系统里的 SDL。** Windows 没有系统级 SDL，版本不受控，还可能加载到别的程序的副本。
- **cgo 静态链接。** 要维护 C 工具链。
- **发布包里用 `binsdl`。** 版本不经过我们的校验，运行时写 DLL 再加载，容易被杀毒误报。
- **默认启用 RawInput 或 WGI，或只用 XInput。** 没有理由换掉默认；XInput 读不到 PlayStation 和 Switch 手柄。
- **默认让 Joy-Con 保持分开，或把合并做成设置。** 两只都连上就说明用户要一个完整手柄，再加开关只是多一件要解释的事；换设备的代价是当时按着的键。
- **竖握为默认，或加 `--joycon` 参数、`sl`/`sr` 别名。** 模板按横握设计；SL 对应哪个控件取决于左右、握法和是否合并，固定别名总会在某个模式下是错的。
- **接收器：用连接或电源状态、控件沉默超时、IMU、`SDL_SendGamepadEffect` 探测、切 hint 重建设备。** 连接状态固定 WIRED；沉默超时会误伤按住不动的画手；IMU 不是所有设备都有；探测会阻塞 T-input；切 hint 影响所有 Switch 设备。
