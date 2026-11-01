# ADR-0016：可解释映射与诊断页

[English](0016-diagnostics.md)

相关：[ARCHITECTURE §7.8、§12、§13](../ARCHITECTURE.zh-CN.md)；[ADR：编辑界面](0013-editor-ui.zh-CN.md)；[ADR：引擎与线程](0005-engine-and-threads.zh-CN.md)

## 背景

“按了没反应”是映射工具最常见的问题。没有诊断页时只能用 `padmatectl trace` 重放，或用 `--decisions` 看演练输出。

引擎的每条决策已经经 IPC 话题 `engine.activity` 发给界面，但它和按键边沿不是一一对应：等判定的按下没有决策；点按先有一条 `unbound`，松开后才有 `dispatch`。延迟只在基准里测，产品代码里没有打时间戳。SDL 在 Windows 上忽略 Steam 的虚拟手柄，padmate 看不到它；读 Steam 的内部文件违反“不读取应用内部状态”。

## 决定

1. **“诊断”是设置页“关于”里的子页面**，不占左侧导航，因为只在出问题时用。内容分事件、延迟、冲突三节。
2. **界面连上守护进程期间一直记录**最近 200 条按键事件，不是打开诊断页才开始。用户要先在绘画软件里复现问题，那时诊断页没开。事件只在界面里，不写文件，`--decisions` 和 `trace` 的输出不变。
3. **每行写出为什么没触发。** 守护进程在发布决策时补墙上时间 `at`、`waiting`（还在等的手势，这不是失败）和 `waitedMs`，不改引擎。结果文字按手势和 reason 写，每种 reason 中英文各一句，测试比较界面文字表和 Go 的 reason 表，没写到的让测试失败。
4. **引擎补两类决策：** reason `script`（脚本处理或吞掉了手势）；多击窗口结束而这一档没有动作时记 `unbound`，只给还没被回答的周期记，免得和已发出的键矛盾。
5. **没有映射的手柄**（排队中、被忽略、没有标准映射、被停用）的按下，由 router 以 `unrouted` 事件发给界面。
6. **决策发布挪出热路径**：交给一个发布 goroutine 编码，有上限，满了丢最旧的并计数。引擎和 slot 的零分配测试不变。
7. **延迟测量点：** T-input 取到 SDL 事件后立即读 QPC，放进 `input.Event.Stamp`，Arbiter 在 `SendInput` 返回后再读一次，差就是延迟。只算真正发出的键：只预览不计；连发、序列后续步骤、到点才发的长按和多击、指针移动都不经过输入边沿，不计。每只手柄保留最近 1000 次，预先分配，在 T-output 上写，不分配内存。口径与基准一致，p99 超过 1.5 ms 标警告色。
8. **Steam 只提示“可能”。** `steam.exe` 在运行时，按手柄的类型、厂商和驱动标出 Steam Input 哪一类设置会管到它，沿用 `status.get` 的 `conflicts` 和 5 秒一次的检查。
9. `profiles.explain` 和 `padmatectl profile explain` 按手柄列出功能键的那一套（功能键是手柄自己的设置，所以各 slot 可以不同），并标出当前按着的功能键。

## 代价

- 热路径多两次 QPC 读和一次字段传递，改动后要跑 `task bench`、`task test:sdl`、`task test:inject`。
- 决策缓冲满了会丢旧决策，诊断页可能少几条。
- 事件记录界面重启后清空。
- padmate 无法确认一只手柄真的被 Steam 接管。
- 引擎多了 reason `script`，演练输出的 reason 表和决策黄金文件随之变化。
- 延迟从 T-input 收到事件算起，比基准（从 SDL 时间戳算起）少几微秒。

## 放弃的做法

- **诊断占左侧导航一页。** 只在出问题时看，和日志一样放在设置里。
- **打开诊断页才开始记录。** 见决定 2。
- **读 Steam 的 `virtualgamepadinfo.txt`。** 读应用内部状态，违反项目规定。
- **用 `output.preview` 做事件的结果。** 界面的事件缓冲只留最新一条预览，会丢。
- **新的 IPC 话题，或每条决策都让整个界面重新渲染。** 沿用 `engine.activity`，诊断数据单独存放。
