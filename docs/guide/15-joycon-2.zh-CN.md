# Joy-Con 2

[English](15-joycon-2.md)

Joy-Con 2 是 Switch 2 的 Joy-Con。padmate 自己经蓝牙读取它们，所以不用线，也不用充电握把，就能当手柄用。本章讲怎么连接。初代 Switch 的 Joy-Con 和其他手柄，照常通过 Windows 连接。

## 开始之前

- 推荐 Windows 11。Windows 10 上 Joy-Con 2 也能用，但延迟更大：摇杆每秒约更新 17 次，Windows 11 上约 66 次。
- 不要在 Windows 的蓝牙设置里添加 Joy-Con 2。padmate 不配对就能连上它，Joy-Con 2 本来也不接受 Windows 的配对。Windows 万一提示要配对，点取消。
- padmate 不改 Joy-Con 2 和你的 Switch 2 之间的配对。同一只 Joy-Con 2 在主机上照常能用。

## 连接新的 Joy-Con 2

1. 点托盘里的 padmate 图标，选“连接新的 Joy-Con 2”。
2. 通知会让你在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。一直按到 padmate 说已连接。按普通键连不上。
3. 通知说已连接并记住 Joy-Con 2 时，松手。Joy-Con 2 上的灯显示它的编号。

30 秒内没有找到 Joy-Con 2，会有通知说明。再选一次“连接新的 Joy-Con 2”重试。一次只连一只 Joy-Con 2：要连第二只，再做一遍。

## 再次连接

padmate 会记住连接过的 Joy-Con 2。下次不用走托盘菜单：按住 Joy-Con 2 上的 SYNC 键，padmate 自己连上。

- 每次都要按住 SYNC：开机后、睡眠醒来后、Joy-Con 2 关机或走出范围之后都一样。按普通键能唤醒 Switch 2，但唤醒不了电脑。
- padmate 只连它记住的 Joy-Con 2，而且只在按住 SYNC 时连。没有通过托盘菜单连接过的 Joy-Con 2，它不会去连。
- Joy-Con 2 不再发信号时（关机、走出范围、信号中断），padmate 在约三分之一秒内松开它按着的键。信号 3 秒内恢复的话，不用按 SYNC 就能继续用；超过 3 秒，要重新按住 SYNC 连接。

## 一只还是两只

- **一只左、一只右的 Joy-Con 2**，两只都连上后会自动合成一个完整的手柄。其中一只离开时，另一只继续单独使用。每次变化时，按着的键都会松开。
- **单只 Joy-Con 2** 横握，和初代 Switch 的单只 Joy-Con 一样：一根摇杆、四个面键、SL、SR、L 和 ZL（右手那只是 R 和 ZR），以及 − 或 +。见[手柄图和按键抽屉](01-controller-and-drawer.zh-CN.md)。
- **C 键**在右手的 Joy-Con 2 上。它是附加键，可以像其他附加键一样映射。Home 键和截图键不能映射。
- 紧急暂停：单只是 SL 和 SR 一起按，一对是 − 和 + 一起按。见[托盘和紧急释放](12-tray-and-emergency.zh-CN.md)。
- Joy-Con 2 的电量显示在托盘的状态行里，每 10 % 一档。

## 换回 Switch 2

要在 Switch 2 上再用这只 Joy-Con 2，按一下 SYNC 键，它就和电脑断开了。之后照常在主机上用。用 Joy-Con 2 连电脑时，如果 Switch 2 开着又离得近，它可能先把手柄抢走；这时关掉主机，或打开飞行模式。

## 忘记一只 Joy-Con 2

要让 padmate 忘记一只 Joy-Con 2，用 `padmatectl`。在 padmate 安装目录的 `resources\bin` 文件夹里打开 PowerShell，运行：

```text
.\padmatectl.exe joycon2 status
.\padmatectl.exe joycon2 forget SERIAL
```

第一条命令列出 padmate 记住的 Joy-Con 2 和它们的序列号。第二条里的 `SERIAL` 换成要忘记的那只的序列号。它连着的话会被断开，之后只有再选“连接新的 Joy-Con 2”，padmate 才会连它。

## 连不上时

- 按的要是 SYNC 键本身，不是 Home 键或别的键，并一直按到 padmate 说已连接。
- 一次连接失败后，padmate 隔几秒才会再试同一只 Joy-Con 2。连续连很多次，Joy-Con 2 可能拒绝几分钟；等一等再试。
- 菜单里没有“连接新的 Joy-Con 2”，说明 padmate 在这台电脑上没能启动蓝牙 LE。确认电脑有蓝牙并已打开，再重新启动 padmate。
- 不要在 Windows 的蓝牙设置里添加 Joy-Con 2。已经添加了的话，在那里把它删掉。
