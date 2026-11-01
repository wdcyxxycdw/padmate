# 脚本示例

三个完整示例，每个目录有脚本 `main.ts`、配置 `profile.json` 和说明。

| 示例 | 内容 | 输出键 |
|---|---|---|
| [stick-rate](stick-rate/README.md) | 右摇杆控速，3–20 Hz，回死区即停 | F17、F18 |
| [tabmate](tabmate/README.md) | 单击、双击、长按临时工具、临时层 | Ctrl+Z、F13、F14、F15、F19 |
| [cycle-brush](cycle-brush/README.md) | 配置调用带参数的 `cycleBrush`，session 保存循环序号 | F13、F14、F15、F16 |

## 运行

在仓库根目录构建并取得 SDL，再用对应的配置演练：

```powershell
task build
task sdl
bin\padmated.exe --profile examples\scripts\stick-rate\profile.json --dry-run
```

`--dry-run` 只打印按键，不向前台程序发键，按 Ctrl+C 退出。输出的 F 键要在绘画软件里设成想要的快捷键。

## 测试

自动测试读这三个配置和脚本，经真实的引擎和脚本运行时，输出由假的 Sender 记录，不注入键盘：

```powershell
task test -- -run ScriptExample -count=1
```

把 `PADMATE_TEST_TSC` 设为本机 TypeScript 编译器入口（`typescript/bin/tsc`），同一条命令还会做 TypeScript 检查。
