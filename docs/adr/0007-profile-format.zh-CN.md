# ADR-0007：配置文件格式

[English](0007-profile-format.md)

相关：PRO-01 至 PRO-04、PRO-06、ENG-08、ENG-14、IPC-04；[ARCHITECTURE §10](../ARCHITECTURE.zh-CN.md#10-格式)、[ADR：IPC](0009-ipc.zh-CN.md)

## 背景

profile 描述一只手柄的全部映射；config.json 保存自动切换规则和全局设置。用户（手写）、守护进程（保存即热重载）、界面和 CLI（经 IPC）都会读写它们。要求：手写有补全和校验、错误定位到行列；文件可直接复制分享；多方修改不丢、读不到写了一半的文件；一份结构供 Go、JSON Schema、界面和 TS 类型共用；公开后格式不能随便改；录下的 trace 长期保存做回归测试。

## 决定

1. **严格 JSON，守护进程是唯一写者。** 不支持注释，配 JSON Schema（`schema/` 下的 profile、config、state 三份）。界面和 CLI 经 `profiles.put`、`config.put` 带 revision 写入；守护进程先写临时文件再重命名，替换 profile.json 之前留旧版本。用户直接改文件仍支持，watcher 发现后重新加载。易变状态放 `state.json`，不属于用户配置。
2. **动作是标签联合**，用 `type` 区分。`schema/profile.schema.json` 是唯一源头，Go 和 TS 类型由它生成。
3. **profile 的 `schemaVersion` 冻结为 1。** 一份有效的 profile 永远有效；新增可选字段不改版本，可能弄坏现有文件的改动要加迁移并升版本。版本 0 等同 1，迁移在内存里、保持长度，诊断位置仍是用户看到的位置，磁盘文件从不因为旧而被改写。版本更高的文件被拒绝。config.json 和 state.json 不冻结，它们不在用户之间分享；弃用字段继续被加载、被忽略。`schemaVersion`、IPC `protocol`、`script.api` 三个版本号相互独立。
4. **校验三层：语法 → schema → 语义**，前一层有错误时后一层不运行。
   - schema 用 draft 2020-12，只用 VS Code 和 Monaco 支持的关键字；处处封闭，未知字段是错误，不静默忽略。校验库用纯 Go 的 jsonschema/v6，错误消息由 padmate 改写。
   - 语法层用自己的严格扫描器，建立 JSON Pointer → 字节偏移索引，并比 `encoding/json` 多拒绝重复键和无效 UTF-8。
   - `internal/profile` 里的 schema 是根目录那份的生成副本（`go:embed` 不能引用模块外），只改根目录那份。
5. **事件 JSON 行。** 录制的 trace 和 fake 输入用同一种格式（`padmatectl monitor --json` 的输出）。读取严格，错误写明行号。一个 trace 只录一个设备。格式以后只加可选字段，旧 trace 必须一直能读；控件名因此保持稳定。
6. **热重载。**
   - `internal/watch` 每 100 ms 轮询 stat，变化后稳定 200 ms 才读，按内容哈希判断是否真变了。
   - 有错误就不换：旧映射照常工作，什么键也不释放。
   - 重载就是一次 ReleaseAll：每个 slot 换上新程序时，释放该 slot 的键和进行中的 tap。按着的控件保持已取消，松开再按才用新绑定；重载从不按下键。
   - 只改 `script.params` 时原位生效，不释放。

## 代价

- 不支持注释；界面要守护进程运行才能保存；在编辑器和界面里同时改会出现 revision 冲突。
- 格式公开后很难改，trace 格式同样是兼容承诺。
- 每个动作都要写 `type`，手写更啰嗦。
- 自己的扫描器要与 `encoding/json` 保持一致，靠测试和 fuzz。
- trace 的 info 带 serial，蓝牙手柄的 serial 通常是蓝牙地址，提交前要检查。
- 保存后约 200–400 ms 才生效。

## 放弃的做法

- **SQLite、YAML。** 前者没法手写和 diff；后者缩进和隐式类型容易出错。
- **JSONC / JSON5。** 守护进程保存时重新序列化，注释会丢。
- **界面直接写文件。** 每个写者都要自己做原子写和冲突检测。
- **不带标签按字段推断类型，或允许字符串简写。** 校验错误含糊；同一件事有两种写法，保存时要决定写回哪种。
- **一开始就冻结为 1，或不加版本字段。** 前者在没有公开用户时就背迁移负担；后者公开后无法区分新旧文件。
- **迁移时重新序列化 JSON。** 格式、BOM 和行尾都会变，诊断位置对不上。
- **开放的 schema、未知字段只警告。** 旧守护进程会静默忽略新功能。
- **另定录制格式，或宽松读取，或一个 trace 录多个设备。** 宽松读取会让拼错的字段静默通过；设备间时间不可直接比较。
- **ReadDirectoryChangesW 或 fsnotify。** 重命名保存要自己拼事件；stat 轮询的开销可以忽略。
- **只看修改时间判断变化。** touch 也会重载，白白释放按着的键。
- **重载时替按着的控件按下新键。** 容易出意外，也多一条卡键路径。
