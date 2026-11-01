# ADR-0005：引擎、slot loop、T-output 与运行时监督

[English](0005-engine-and-threads.md)

相关：DMN-02、DMN-03、IN-08、ENG-11、ENG-13、ENG-21、SCR-02；[ARCHITECTURE §5](../ARCHITECTURE.zh-CN.md#5-线程模型)、[§13](../ARCHITECTURE.zh-CN.md#13-时间与延迟)；[ADR：多手柄](0003-devices-and-slots.zh-CN.md)

## 背景

- 脚本参与按键解析（`global()` → 叠加层 → base → common）。引擎要当场知道某一级是消费了事件还是 `e.pass()` 交给下一级，脚本必须同步回答。
- goja Runtime 同一时刻只能被一个 goroutine 使用；看门狗按线程 CPU 时间计预算；reload 不能卡住映射。
- 只有 T-output 线程调用 SendInput（不变量 3），各方发来的命令错误要让调用者看到。
- 所有退出路径（含 T-output 和 slot loop 自己 panic）都要释放按键。
- `input.Source` 的通道有界，满了丢事件，丢的可能正是最后的抬起。
- 延迟门槛：SDL 事件到 SendInput 返回 p99 < 1.5 ms。

## 决定

1. **引擎是纯函数。** `control`、`engine`、`keys`、`profile` 不导入平台、输入输出、脚本包，不调用 `time.Now`，时间只来自 `clock.Clock`；depguard 和 forbidigo 强制。脚本通过适配器实现 `engine.Level`。
2. **slot loop：引擎和脚本在同一个 goroutine 上。** 每个 slot 一个锁定 OS 线程的 goroutine，引擎和脚本 runtime 都归它。脚本 Level 是普通函数调用，事件不跨线程。脚本的构建和 setup 在 worker 上完成后原子换入，声明式绑定不停顿。宿主 API 必须快速返回，`Runtime.Interrupt` 在原生函数内不生效。
3. **T-output 是 `output.Loop`。** 一个锁定线程的 goroutine 拥有 Arbiter，别处不碰。其他 goroutine 用同步的 `Do(f)` 交给它，错误当场返回调用者。Sender 链是 Arbiter → ledger → MenuMask → Sender，ledger 记下留下按着的键。`Stop` 先 `ReleaseAll`，抬起失败最多重试 3 次，否则返回列出可能卡住的键的错误。
4. **panic 与 Goexit 都释放按键。** T-output panic：Arbiter 状态不可信，按 ledger 逆序抬起后结束，之后的 `Do` 返回 `ErrLoopClosed`，守护进程视为致命。slot loop panic：不经 engine，直接 `ReleaseOwners`，supervisor 重启该 slot。Goexit 同样释放。
5. **State 与 Resync。** Slot 记下处理过的按钮和轴的值。`Resync` 合成缺失的边沿，只让状态一致，不重放间隙里的过程。状态未知时用 `Reset`：ReleaseAll 加引擎复位。
6. **router 拥有设备状态，投递从不阻塞。** edge 通道满了，这个 slot 就落后：后续边沿跳过并计数，追上时再 Resync。**丢事件就复位所有 slot**。**Sync 是屏障**，保证调用前送进 Events 的事件及其输出都已处理。
7. **panic 后重启，反复 panic 就禁用。** 每个 slot 一个 runner，panic 后退避重启；重启不 Resync，以免再次按下引起 panic 的按钮。10 s 内 3 次 panic 就禁用，下一次分配时重新启用。
8. **关闭顺序。** slot 处理完排队边沿并释放，然后调用者 `Output.Stop`，最后关闭 `input.Source`。
9. **纯包不记日志**（depguard 禁止），错误经 `Slot.Errors` 流出。
10. **端到端延迟。** 用真实 SendInput 和吞键的测试钩子、虚拟手柄驱动 padmated 实机路径，测从 SDL 事件时间戳到 SendInput 返回。门槛是开发机上的 `task bench`，A/B/C 三行 p99 < 1.5 ms，连续 3 次通过。时间戳只在测试里取。按键路径上不调用 `timeBeginPeriod`。实测最差 p99 约 1.17 ms，几乎都花在线程唤醒和 SendInput 上。以后不通过时按阶段逐项缓解，最后才改结构，那要写新 ADR。

## 代价

- 脚本运行时，同一 slot 的声明式绑定也要等它；靠处理器预算和安全模式兜底。
- 每个命令一次跨线程往返；一个慢的 Send 会挡住所有 slot。
- 延迟余量不大（约 22%），只在开发机上成立，不含真机最多约 1 ms 的 SDL 轮询；4 个手柄同时按会超过 1.5 ms。

## 放弃的做法

- **脚本独立 goroutine，引擎通过通道等结果；所有设备共用一个脚本线程；脚本只异步观察。** 前者每次跨线程往返、难以确定性测试；一个设备会拖慢所有设备；后者失去 `e.pass()` 和统一的解析顺序。
- **命令异步投递，或 Arbiter 加锁由各 slot 直接调用。** 前者错误回不到调用者；后者违反不变量 3。
- **通道满了阻塞，或无界队列。** 卡住的 slot 会挡住 router；内存一直增长。
- **重启时 Resync，或一直重启。** 会再次按下引起 panic 的按钮；会不停释放、重启、写日志。
- **只用 fake Sender 测延迟，或用 `time.Now`。** 测不到 SendInput 返回；`time.Now` 粒度太粗。
