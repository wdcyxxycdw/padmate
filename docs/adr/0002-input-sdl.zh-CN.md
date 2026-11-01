# ADR-0002：手柄输入：SDL3、hint、go-sdl3 补丁、Joy-Con 与接收器心跳

[English](0002-input-sdl.md)

相关：[PITFALLS“SDL 与手柄”](../PITFALLS.zh-CN.md)

## 背景

手柄输入用 SDL 3.4.x，经 go-sdl3（purego，无 cgo）在运行时加载 SDL3.dll。

- Windows 不自带 SDL，用户机器上可能有别的程序带来的其他版本。后台输入、hint 组合和设备映射随版本变化，硬件测试矩阵只对一个版本有效。
- padmate 在后台工作，无窗口无焦点也要收到输入。
- go-sdl3 的绑定有缺失，也有会 panic 或结果错误的。
- 画画时左手常只握一只 Joy-Con，另一只醒来时手里这只不能受影响。
- Switch 协议的 2.4G USB 接收器在手柄关机后仍被 SDL 列着，不报告 removed，关机前按着的键会一直按着。

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

## 代价

- 跟进 SDL 要自己升版本号和哈希、重跑硬件矩阵。新克隆必须先 `task sdl`。
- 两只 Joy-Con 合用要自己加 hint。Switch 2 的 Joy-Con 不支持。
- 接收器检测依赖真机行为和 SDL 3.4.16 在 USB 上强制完整报告，升级 SDL 要重新核对。错在防卡键的一边：链路断超过 2 s 会释放按键并报告断开。

## 放弃的做法

- **用系统里的 SDL。** Windows 没有系统级 SDL，版本不受控，还可能加载到别的程序的副本。
- **cgo 静态链接。** 要维护 C 工具链。
- **发布包里用 `binsdl`。** 版本不经过我们的校验，运行时写 DLL 再加载，容易被杀毒误报。
- **默认启用 RawInput 或 WGI，或只用 XInput。** 没有理由换掉默认；XInput 读不到 PlayStation 和 Switch 手柄。
- **竖握为默认，或加 `--joycon` 参数、`sl`/`sr` 别名。** 模板按横握设计；SL 对应哪个控件取决于左右、握法和是否合并，固定别名总会在某个模式下是错的。
- **接收器：用连接或电源状态、控件沉默超时、IMU、`SDL_SendGamepadEffect` 探测、切 hint 重建设备。** 连接状态固定 WIRED；沉默超时会误伤按住不动的画手；IMU 不是所有设备都有；探测会阻塞 T-input；切 hint 影响所有 Switch 设备。
