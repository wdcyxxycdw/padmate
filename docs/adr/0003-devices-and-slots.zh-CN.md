# ADR-0003：多手柄、热插拔与 slot 分配

[English](0003-devices-and-slots.md)

相关：[ARCHITECTURE §5](../ARCHITECTURE.zh-CN.md#5-线程模型)、[§8.2](../ARCHITECTURE.zh-CN.md#82-output-arbiter)；slot loop 和 supervisor 见 [ADR：引擎与线程](0005-engine-and-threads.zh-CN.md)

## 背景

- 最多同时映射 4 个手柄，每个一个 slot，其余排队。断开必须 ReleaseAll，按 DeviceKey 重连后恢复映射。
- 设备的连接和断开事件一旦丢失就不会补，router 会和真实设备状态对不上。
- 机器上常有一只放在一边的闲置手柄（如 2.4G 接收器上的，往往最先枚举）。空 slot 若直接归先连上的手柄，启动时就归了它；手柄短暂断开，slot 也会被别人占走。

## 决定

1. **Source 不丢设备事件。** 通道满时，T-input 把送不进去的 DeviceAdded 和 DeviceRemoved 按顺序存入 backlog。backlog 不空时丢弃按钮和轴事件并计数，没有事件越过排着的设备事件。同一 id 的 Added 还在 backlog 时遇到 Removed，两者一起去掉。
2. **slot 预先建好。** Supervisor 在 New 里建好 1–4 个 slot（padmated 用 4 个），不管有没有手柄。每个 slot 有自己的引擎、runner 和边沿通道，按下的键带自己的 `output.Owner.Slot`。一个 slot 慢、卡住、panic 或被禁用，不影响也不释放别的 slot。
3. **键由 Arbiter 引用计数共用。** 两个 slot 按同一个键只按下一次，最后一个松开才抬起；修饰键在系统层面叠加。丢事件时复位每个 slot，每个手柄按着的键都松开。
4. **分配规则。** 每个 slot 有 home（DeviceKey）和“暂代”标志。新连上的匹配手柄 d：
   - 有空 slot：d 暂代它，成为 home。
   - 没有空 slot，而 home 是 d 的 slot 由暂代手柄占着：d 收回它，暂代手柄回到队首。
   - 否则排到队尾。
   - slot 的手柄离开时立即交接给队首（home 不变）；没人等就空闲，home 清空，空 slot 不给任何 key 保留。
5. **暂代，按下按钮才生效。** 占空 slot、交接、收回都先是暂代。暂代手柄的第一个 ButtonDown（任何按钮）让分配生效，home 变成它。轴事件不算，因为闲置手柄的摇杆会抖动；交接时已按着的按钮也不算。
6. **接管。** 排队手柄的 ButtonDown 接管分配得最早的暂代 slot（不管 home 是谁），原暂代手柄回到队首。所以先连上的闲置手柄挡不住正在用的手柄。轴事件不能接管，扳机也一样。
7. **交接时 Resync。** 新手柄已按着的按钮算按下，扳机和摇杆取当前值，与按着按钮连上的手柄一致。
8. **落后时不丢分配。** slot 落后时待投递边沿是有序列表，最多两项：先释放，再现在分配的手柄的 DeviceAdded。移除遇到同一手柄待投递的 Added 就一起取消。追上后按顺序投递再 Resync，所以 slot 看到的 Added 和 Removed 仍交替。slot 被禁用时丢掉死 slot 的边沿，若有现在分配的手柄的 Added 就立即重新启用。
9. **禁用的 slot 留着它的手柄**，不换到空 slot，免得让 slot panic 的事件弄崩下一个。
10. **重连。** 重连的手柄拿到 slot 后用当前 Program 映射，之前按着的键已释放，层状态按 DeviceKey 恢复。按着按钮重连的结果依后端而定（rawinput/hidapi 报告按下，xinput 不报告），引擎两种都能处理。

## 代价

- 闲置手柄暂代时被按一下，slot 就归它，手里的手柄只能排队。要固定手柄用 `--device`，要忽略某个设备用 config.json 的 `devices.<DeviceKey>.ignored`。
- 闲置手柄也占一个 slot，按钮和轴都按 profile 映射。
- 只用摇杆或扳机的手柄一直是暂代，接管要按一下按钮。
- 两只相同的手柄 key 只差 #N，home 的手柄没回来时，另一只相同的手柄可能得到它的 #N 并收回 slot。
- 只有一个手柄时也有 4 个锁定线程；手柄多了更容易丢事件，丢事件时每个手柄都复位。

## 放弃的做法

- **先进先出，不能收回。** 短暂断开的手柄回来只能排队，闲置手柄一直占着 slot。
- **home 随时收回，不管暂代手柄按没按过。** 正在用的手柄会在按键中途被夺走。
- **占空 slot 直接生效。** 启动时先连上的闲置手柄就一直占着。
- **先收回再占空 slot。** 暂代手柄的键先抬起再按下，按住的键会断。
- **轴事件或交接时已按着的按钮也算确认。** 抖动的摇杆让闲置手柄一直占着 slot。
- **设备事件单独走一个通道。** 通道之间没有先后。
- **按需建 slot，每个手柄一个 goroutine。** runner、屏障和 Status 都要处理增减，而 4 个空闲 slot 开销很小。
- **每个 slot 各自仲裁修饰键。** 系统只有一个键盘状态，时序复杂，还可能打断正按着的组合。
