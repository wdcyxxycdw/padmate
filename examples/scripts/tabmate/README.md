# 例 2：TABMATE 式手势与层

运行方式见[脚本示例](../README.md)，配置是 `examples\scripts\tabmate\profile.json`。

| 控件与手势 | 输出或作用 |
|---|---|
| south 按下 | Ctrl+Z（撤销） |
| west 短按 | F13（笔） |
| west 按住至少 240 ms | 按住 F14（临时橡皮），松开 west 时自动抬起 |
| north 单击 | F19（图层面板），等 220 ms 的连击窗口结束后点按 |
| north 双击 | 切换 alt 层，不发单击的 F19 |
| rightshoulder 按住 | 临时启用 alt 层，松开时撤掉这次临时启用 |
| alt 层的 south 按下 | F15（重做） |

south、west 等是 SDL 的物理位置名，与手柄面上的字母不同。F13、F14、F15、F19 需要在绘画软件里设置，例如把 F14 设成“按住时临时橡皮”。

双击切出的 alt 层在肩键松开后仍保留，只有肩键临时启用的层才随它撤掉。断连、暂停和退出会结束临时键和临时层。
