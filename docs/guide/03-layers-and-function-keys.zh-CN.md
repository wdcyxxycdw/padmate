# 层和功能键

[English](03-layers-and-function-keys.md)

手柄上的键不多。功能键和层让同一个键在不同时候做不同的事。功能键在界面里设置，层只能写在配置文件里。

## 功能键

手柄中间有三个键：View、Menu 和 Xbox 键。PlayStation 手柄上是 Create（或 Share）、Options 和 PS 键，Switch Pro 手柄上是 −、+ 和 Home。其中一个是功能键，默认是 Menu。

功能键像笔记本的 Fn 键：

- 它不能设快捷键，自己也不发键。手柄图上它的标签写着“功能键”。
- 按住它，其他键就换成另一套设置。这一套里没设置的键，照常用“平时”的设置。
- 用哪一套，在按下某个键的那一刻定。按下功能键之前就按着的键不受影响。

### 更换功能键

1. 点“设置”，在“通用”里找到“功能键”，点其中一个键。选中立即生效，对所有配置和手柄一样。
2. 选 Xbox 键会弹出对话框。按 Xbox 键时 Windows 的 Xbox Game Bar 也会打开，padmate 无法阻止。请在“设置 → 游戏 → Game Bar”里关掉“允许控制器打开 Game Bar”（旧版 Windows 里叫“使用控制器上的此按钮打开 Xbox Game Bar”）。点“打开 Windows 设置”直达这一页，点“知道了”关闭对话框，选择已生效。Steam 运行时也可能占用 Xbox 键。

### 设置功能键的一套

1. “映射”页顶部有“平时 / 按住功能键”的切换，点其中的“按住功能键”。手柄图和键的设置显示的就是按住功能键时的这一套。
2. 点一个键的标签，录入快捷键，做法同平时。
3. 这一套里没设置的键，标签是灰色的，写着“平时：…”，即不按功能键时它发的快捷键。
4. 想让某个键回到平时的设置：打开它，点“按键操作”，选“清除这一套里的设置”。
5. 点“平时”回到平时的设置。

## 层

层是一整套按键设置，像叠在一起的几张纸。找键的设置时从最上层往下找，找到就用。

界面每个配置只编辑一组按键，即 base 层。别的层只能写在配置文件里，界面保存时原样保留。

### 写一个层

每个配置有自己的文件夹，在 `%APPDATA%\padmate\profiles` 里，文件夹里的 `profile.json` 就是配置文件。下面的配置有两层：按住左肩键时，A 键发重做；松开后，A 键又是撤销。

```json
{
  "schemaVersion": 1,
  "name": "layers",
  "layers": {
    "base": {
      "leftshoulder": { "press": { "type": "layer", "layer": "alt", "op": "hold" } },
      "south": { "press": { "type": "keys", "keys": "Ctrl+Z", "mode": "tap" } }
    },
    "alt": {
      "south": { "press": { "type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap" } }
    }
  }
}
```

- 键用控件名写，例如 `south`（下面的主按键）和 `leftshoulder`（左肩键）。全部名字见安装目录的 `resources\schema\profile.schema.json` 里。
- `layer` 动作的 `op` 有四种：`hold` 按住期间打开这一层；`toggle` 按一次打开、再按一次关上；`switch` 把 base 层换成另一层；`cycle` 在 `layers` 列出的几层之间轮流换。
- 一层里没有设置的键，往下找：先找 base，最后找 `common`。`common` 放所有层共用的键。
- 要让某层里的某个键什么也不做，写 `"block": true`。
- `defaultLayer` 是启动时的 base 层，不写就是 `base`。

View、Menu 和 Xbox 键对应 `back`、`start` 和 `guide`。写在当前功能键上的设置不生效，但留在文件里。功能键的一套写在 `functionSet` 里，写法和层一样。

保存文件后 padmate 自动重新读取。写完可在 `resources\bin` 文件夹运行 `.\padmatectl.exe validate profile.json` 检查，它会指出错在哪里，不改文件。
