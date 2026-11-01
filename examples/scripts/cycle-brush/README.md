# 例 3：session 笔刷循环动作

运行方式见[脚本示例](../README.md)，配置是 `examples\scripts\cycle-brush\profile.json`。

south 点按运行 `cycleBrush`，参数 `dir: 1`；north 点按运行同一个动作，参数 `dir: -1`。动作在 F13、F14、F15、F16 之间正向或反向循环，每次点按一个键。初始序号是 0（F13），第一次按 south 发 F14，第一次按 north 发 F16。在绘画软件里把四个键设成四种笔刷或工具。

序号存在脚本的 `session` 里：

- 配置重载、暂停后重新激活、切到别的配置再回来时，序号保留。
- 不同配置各有序号。session 按配置路径识别，重命名显示名称不丢序号。
- 重启守护进程会清空序号。
- 多个手柄映射同一个配置时，共用这个配置的序号。

看到 `cycleBrush ready` 日志后即可操作。
