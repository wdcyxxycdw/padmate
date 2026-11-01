# ADR-0014：Overlay 窗口

[English](0014-overlay.md)

相关：[ARCHITECTURE §12](../ARCHITECTURE.zh-CN.md)；[ADR：脚本运行时](0010-scripting-runtime.zh-CN.md)；[ADR：进程与技术栈](0001-processes-and-stack.zh-CN.md)

## 背景

Overlay 在画画软件上方显示 toast、按住“按键提示”键时的快捷键清单、脚本的 toast 和面板。

它只有一种出问题的方式，也是最糟的：抢走前台。前台检测把“前台窗口属于已注册的界面进程”当作 padmate 自己在前台，这时 Arbiter 只虚拟记账、不发键。Overlay 与主窗口同属 padmate.exe，哪怕只被激活一次，手柄就停止向画画软件发键，直到用户点回去。画画时用户不会去点窗口，只看到“键突然不管用了”。

## 决定

### 一、窗口永不激活
- Overlay 是 padmate.exe 的第二个 `BrowserWindow`，创建一次，随应用存在。它属于可选的界面，没有界面就没有 overlay。
- 窗口不可聚焦（`WS_EX_NOACTIVATE`）、置顶、鼠标穿透且不转发。不转发是因为 `forward:true` 会在界面进程里装 `WH_MOUSE_LL` 钩子，该线程卡住时全系统鼠标都被拖慢。
- **整个生命期只用 `showInactive()` 显示一次**，代码里不出现 `show()`、`focus()`、`moveTop()`、`hide()`（测试读源码检查）。内容的显示和隐藏由页面切换 `data-visible`，所以没有任何一条路径会激活它。
- 不铺满屏幕，因为全屏置顶窗口会被 Windows 当成全屏程序。窗口居中在主显示器工作区的底部或顶部，高度随内容增减，只用 `setBounds`。
- 渲染进程崩溃时重建一次，再崩不重建，崩溃循环比没有提示更糟。

### 二、通道只从 Main 到页面
Overlay 的 preload 只暴露接收函数，没有 `send`、`invoke`，页面调不到 Main。preload 逐字段检查每条消息，页面把文字当文本节点渲染。Main 里挂在守护进程事件上的代码必须吞掉自己的异常，否则会断开与守护进程的连接。

### 三、toast 与偏好
- toast 来源是 `engine.activity` 的 `state` 事件：配置名变、暂停或恢复、base 层变。hold、toggle 压入的层不提示，否则画画时按住切层会一直闪。连接后的第一个状态是起点，不出提示。
- 位置、不透明度、提示消息开关是界面自己的偏好，存在渲染进程里，由界面推给 Main，不进 config.json 或 profile。不透明度只淡化背景，不用 `BrowserWindow.setOpacity()`。

### 四、按键提示
- 动作 `{"type":"hud","op":"hints"}` 不发键，只能放在 `press` 和 `hold` 手势上。状态与精确模式同一套办法，`CancelAll` 一并清掉，所以松开、暂停、重载、断开都结束它。
- 清单由守护进程算，因为它是已解析层栈的唯一权威。只在提示开启、结束、层栈变化时重算，不在热路径上。`state` 事件的 slot 对象带可选的 `hints`，只在提示开着时出现，最多比实际晚 250 ms。
- 控件名由渲染进程按手柄翻译，与映射页同一份名字表。

### 五、脚本的 toast 和面板
- `host.has("hud")` 为 true 当且仅当有客户端订阅了话题 `hud`，而 Main 在 overlay 页面画好之后才订阅，所以为 true 时屏幕上确实有 overlay。
- 脚本 toast 受“提示消息”开关管，不看主窗口是否聚焦。面板随 `state` 事件发布，与按键提示跟同一个 slot，按键提示显示时面板让开。

## 代价

- 独占全屏程序上看不到 overlay（已知限制）。
- Windows 虚拟桌面上 overlay 只在创建它的那个桌面显示。
- 提示和面板最多晚 250 ms（状态事件的节奏）。
- 只跟第一个已映射 slot，多手柄时不说明是哪只。
- 守护进程补的文字（`层：`、`配置：`）不随界面语言。
- padmate 窗口进出前台时 slot 做 `ReleaseAll`，脚本 runtime 失效，面板消失，下一次按键才重建。
- 混合 DPI 显示器上的 `setBounds` 行为没有实测。

## 放弃的做法

- **全屏透明窗口。** 会被当成全屏程序。
- **每次显示 `show()`、隐藏 `hide()`。** 最直观，也最容易抢焦点。
- **守护进程自己画 overlay（Win32 分层窗口）。** 要在 Go 里做文字和布局。
- **`forward: true` 的鼠标穿透。** 要装全局鼠标钩子，而 overlay 没有需要悬停的内容。
- **偏好写进 config.json，或 Main 读 `localStorage`。** 前者要改 schema 和热重载语义，后者要轮询或每个 toast 多一次往返。
- **`setOpacity()` 或在根元素上设不透明度。** 与页面淡入淡出叠加，也破坏 E2E 的读数。
- **面板用第二个 overlay 窗口。** 违反“只创建一次”。
- **界面自己从配置和层状态算清单，或清单一直放在状态事件里。** 前者另有一份权威，后者绝大多数时候没人要却要每 250 ms 多发几 KB。
- **面板用“加”和“删”事件，或新增 `hud.attach`。** 见[脚本运行时](0010-scripting-runtime.zh-CN.md)。
