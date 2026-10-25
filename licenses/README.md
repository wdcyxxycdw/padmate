# 便携包的第三方说明

`SDL3-LICENSE.txt` 原文来自 SDL 固定版本
[release-3.4.16/LICENSE.txt](https://github.com/libsdl-org/SDL/blob/release-3.4.16/LICENSE.txt)。
仅清理行尾空格与末尾多余空行，条文保持原样。

`task package:zip` 从两个 exe 的 Go build info 读取实际链接模块，
收集对应固定版本模块根目录的 LICENSE、LICENCE、COPYING 与 NOTICE 文件。
Go 工具链的 LICENSE（与可用时的 PATENTS）也会加入包内。
清单写入 `licenses/THIRD-PARTY.txt`，许可证放在 `licenses/go/<模块>@<版本>/`。
只供构建工具使用的 `winres` 等依赖不链接进发布的 exe。

## 没有许可文件的依赖

`github.com/Zyko0/purego-gen v0.0.0-20250727121216-3bcd331a1e0c`
由 SDL Go 绑定链接进守护进程和 CLI。该版本的模块文件、
[上游固定提交](https://github.com/Zyko0/purego-gen/tree/3bcd331a1e0c)
均没有根目录 LICENSE 或 NOTICE；2026-10-02 查询 GitHub 的仓库许可证接口返回 404。

本项目没有据此声明其许可，也不把同作者的 go-sdl3 的 MIT 许可套用于该模块。
包里保留准确的模块版本、来源和缺少文件的事实，
在对应的 `NOTICE-NO-LICENSE.txt` 中注明：该模块未附许可文件，这份说明不声明任何许可，再分发授权未经确认。
