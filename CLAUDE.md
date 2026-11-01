# CLAUDE.md

## 项目内容
padmate 是 Windows 上的手柄 → 键盘快捷键映射器。

- `daemon/`：Go 守护进程 `padmated`，以及 CLI `padmatectl` 和开发工具 `padmate-probe`。负责输入（SDL3）、映射引擎、输出（SendInput）、TypeScript 脚本（goja + esbuild）、托盘和 IPC。
- `app/`：Electron UI 与 overlay。可选组件，关掉它映射照常工作。
- 许可证 MIT。仓库 `github.com/wdcyxxycdw/padmate`（公开，代码和文档都在这里）。

## 文档索引
| 文档 | 内容 |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 进程、线程、包结构、引擎、输出、IPC、文件格式 |
| [docs/PITFALLS.md](docs/PITFALLS.md) | 已验证的技术事实（踩坑记录），按代码区域分组；改某块代码前先读对应的一节 |
| [docs/adr/](docs/adr/README.md) | 架构决策记录，按主题编号，只写现在有效的决定 |
| [docs/guide/](docs/guide/00-index.md) | 用户手册 |

文档用中英双语：英文是 `X.md`，中文是同目录的 `X.zh-CN.md`，两份同时改。

本文件不重复记录进度，也不重复架构细节；它们在上面的文档里。

## 命令
任务以 `Taskfile.yml` 为准（各任务何时加入见 ARCHITECTURE §15），`task --list` 列出全部；在仓库任意子目录都能运行，Go 相关命令都在 `daemon/` 下执行：

```bash
task ci             # vet → lint → test → build → pe:check → app:gen-check → app:ci，遇错即停
task vet            # go vet ./...
task lint           # golangci-lint run（配置 daemon/.golangci.yml）
task fmt            # golangci-lint fmt（gofmt + goimports），会改写文件
task test           # go test ./...；额外参数放在 -- 之后：task test -- -run Parse -v
task test:types TSC=<绝对路径> # 真实 TypeScript 检查声明、动作参数推断和脚手架
task fuzz           # Taskfile 列出的 Fuzz 测试依次各跑 FUZZTIME，默认 60s：task fuzz FUZZTIME=10m
task build          # 三个命令构建到 bin/，版本默认取 git describe
task dev            # 控制台子系统构建 padmated，便于看日志
task build:release  # -H=windowsgui 构建 padmated（仅 Windows）
task package:zip VERSION=v0.7.0-preview # GUI padmated、CLI、固定 SDL、示例和文档到 dist/（仅 Windows）
task clean          # 删除 bin/
task test:inject    # 真实注入测试（仅 Windows，PADMATE_TEST_INJECT=1），每个键和鼠标事件都被 LL 钩子吞掉；含 padmated 的实机测试，所以先运行 sdl，并设 PADMATE_REQUIRE_SDL=1 和 PADMATE_REQUIRE_SESSION=1
task test:layouts   # kblayout.Dump 和 output.SystemLayouts 读出的本机布局与 daemon/testdata/layouts 比较（仅 Windows，PADMATE_TEST_LAYOUTS=1）；会话只有一个布局列表，所以两个包用 -p 1 依次跑
task layouts        # 用 padmate-probe dump-layout 重新生成布局 fixture（仅 Windows）
task gen            # go generate ./...：由 syscall_windows.go 的 //sys 声明生成 zsyscall_windows.go；探测 goja，生成 internal/script/compile 的 goja_features.json 和 lib.padmate-goja.d.ts；把 schema/profile.schema.json 复制到 internal/profile
task app:install    # 安装 app 的固定 npm 依赖与 Electron runtime（新克隆先运行）
task app:build      # 构建 app/out
task package:installer VERSION=v0.14.0 # NSIS 安装包到 dist/installer（仅 Windows；先 app:install）
task test:installer # 安装、覆盖安装、卸载冒烟（仅 Windows）：INSTALLER=绝对路径 [UPGRADE=…]；暂写真实 HKCU 卸载项和开始菜单项
task release:r2 VERSION=v1.0.3 # 发布到下载站（R2，仅 Windows）：写线上的桶，owner 说“发布”才运行；加 -- --dry-run 只看计划
task app:dev        # Windows 隔离开发界面，自动准备 .cache/ui-bin，daemon 默认 dry-run
task app:prepare    # 单独准备开发 daemon/CLI；先退出占用它们的开发实例
task app:gen-check  # 三份 schema → Go/TS 生成文件漂移检查（不改文件）
task app:ci         # TypeScript → Biome → Vitest → 前端构建
task app:e2e        # Windows 真实 Electron + 私有 fake-input daemon 的 Playwright 验收
task sdl            # 下载并校验固定版本的 SDL3.dll 到 bin/（仅 Windows；已是正确的 DLL 时不联网）
task test:sdl       # SDL 测试（虚拟手柄，不需要硬件），PADMATE_REQUIRE_SDL=1：找不到 SDL 就失败
task spike:tray     # 构建托盘与 T-input 的 spike 到 bin/trayspike.exe（仅 Windows，GUI 子系统）
task spike:script   # 运行 goja 与 esbuild 的 spike，Markdown 报告到 stdout，约 40 s；参数放在 -- 之后
task bench          # 延迟基准（仅 Windows，约 4.5 min）：热路径基准、单独测 SendInput、用虚拟手柄驱动 padmated 真实注入（每个键被测试自己的钩子吞掉），报告写到 bin/sendinput.md 和 bin/latency.md；BENCH_SCALE=5 加样本（最多 10）
```

## 不可破坏的不变量
违反任何一条都视为 bug，不管测试是否通过。

1. **不卡键优先于一切。** 所有退出路径（正常退出、panic、断连、切换 profile、暂停、锁屏、睡眠、安全桌面）都要 ReleaseAll。新增任何按住键的代码路径，都要同时写"它在哪里被释放"的测试。simtest 在每个测试结束时执行 AssertNoneHeld。
2. **引擎是纯函数。** `control`、`engine`、`keys`、`profile` 不导入 `x/sys/windows`、`input`、`output`、`script`、`latency`，不调用 `time.Now`，时间只从 `clock.Clock` 获取。由 depguard 和 forbidigo 强制。这些包必须能在 Linux 上测试。
3. **输出只有一个写者。** 守护进程内只有 T-output 线程调用 SendInput，其他代码都经过 Output Arbiter。守护进程外只有开发工具 `padmatectl send` 和注入测试直接调用。
4. **不手改生成的文件。** 改源头（`schema/profile.schema.json` 而不是 `internal/profile` 里的副本、`tools/gojafeatures` 的探测表、`syscall_windows.go` 里的 `//sys` 声明等），然后重新生成（`task gen`）。
5. **Windows 回调线程规则。** LL 钩子和 WinEvent 钩子必须安装在一个 `runtime.LockOSThread` 并运行 GetMessage 循环的线程上。钩子回调只复制数据、立即返回。`windows.NewCallback` 创建的回调永不释放且数量有限，每种只创建一次。
6. **默认演练。** 开发和测试时优先用 `--dry-run` 或 fake 输入，避免向真实前台程序乱发按键。必须真实注入时，每个键和鼠标事件都要被 LL 钩子吞掉：先装好钩子（鼠标事件另装鼠标钩子），用金丝雀确认已生效，再发第一个真实事件。测试用 `internal/injecttest`，并要求 `PADMATE_TEST_INJECT=1`。padmated 里只有 `cmd/padmated/sender_windows.go` 构造 SendInput，而且只在 `--inject` 时（`TestNoSendInput`、`TestChooseSender`、forbidigo）。

## 禁止事项
- 不复制、不逐行翻译其他类似开源项目的代码。拿不准的行为或实现细节可以参考源码，弄懂后用自己的结构实现。
- 不绕过 UIPI，不修改全局系统设置，不输出虚拟手柄。
- 不读取窗口标题用于匹配。

## 代码风格
- Go：`gofmt`/`goimports`，golangci-lint v2 配置为准。标识符和代码注释用英文。平台相关代码放在 `_windows.go` 文件中，跨平台部分保持可在 Linux 上编译。
- TypeScript：Biome 2 配置为准。
- 文档用中英双语。
- 优先表驱动测试和黄金测试（引擎的在 `internal/slot/testdata/golden/<case>/`，热插拔的在 `internal/supervisor/testdata/golden/<case>/`，见 ARCHITECTURE §14）。解析器类代码要有 fuzz 测试。
- 黄金文件用测试的 `-update` 标志重新生成（`go test ./internal/profile -update`；`./cmd/padmatectl` 加 `-run TestValidateGolden`，`./internal/slot` 和 `./internal/supervisor` 加 `-run TestGolden -v`；`./cmd/padmated -update`）。