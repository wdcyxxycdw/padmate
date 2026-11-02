# 层和功能键

[English](03-layers-and-function-keys.md)

手柄上的键不多。功能键和层让同一个键在不同时候做不同的事。功能键在界面里设置，层只能写在配置文件里。

## 功能键

每只手柄有自己的功能键。它是哪一个键，看手柄的种类：

| 手柄 | 默认功能键 | 也可以选 |
|---|---|---|
| Xbox、PlayStation、Switch Pro 等 | Menu | View |
| 单只 Joy-Con，左手那只 | ZL | L |
| 单只 Joy-Con，右手那只 | ZR | R |
| 左右两只 Joy-Con 合成的一个手柄 | − | + |

PlayStation 手柄上的 View 和 Menu 是 Create（或 Share）和 Options，Switch Pro 手柄上是 − 和 +。单只 Joy-Con 默认用 ZL 或 ZR，因为这两个键大，好按。单只 Joy-Con 不提供 − 和 +，因为握着时按不到它们。

功能键像笔记本的 Fn 键：

- 它不能设快捷键，自己也不发键。手柄图上它的标签写着“功能键”。
- 按住它，其他键就换成另一套设置。这一套里没设置的键，照常用“平时”的设置。
- 用哪一套，在按下某个键的那一刻定。按下功能键之前就按着的键不受影响。

### 更换功能键

点“设置”，在“通用”里找到“手柄设置”一行，点“打开”。里面按手柄分段，每只手柄下面有它的“功能键”，点其中一个键。选中立即生效，只改这一只手柄，对所有配置一样。改的时候，这只手柄按着的键会先松开。

现在连着的手柄排在前面。以前连接过、现在没连着的收在“以前连接过的手柄”里，也可以先改，下次连上就按改好的用。

“映射”页上“按住功能键”的那一套，用的是页面上那只手柄的功能键。换一只手柄，哪个键是功能键也跟着换。

以前版本里在“设置”选的功能键，仍然是 Xbox、PlayStation 这类手柄的功能键。Joy-Con 用自己的默认。

### 为什么不能用 Xbox 键

中间的 Xbox 键（PlayStation 手柄上是 PS 键，Switch 手柄上是 Home）不能当功能键，也不能设快捷键，手柄图上没有它的标签。别的程序和手柄自己都在用这个键：

- 按它会打开 Windows 的 Xbox Game Bar。
- Steam 运行时，按它会打开 Steam 的大屏幕模式，和 Menu 一起按还会变成 Alt+Tab。
- 有的手柄长按它会关机，按住它再按别的键会重置手柄的设置。

这些 padmate 都拦不住，所以 padmate 不用这个键：不能映射，脚本里不能用，也不能放进紧急暂停的组合。padmate 照样读到它，诊断页里能看到它被按下。

以前把 Xbox 键选成功能键的，现在按 Menu 处理。下次在设置页改任何一项时，这个旧选择会从配置文件里去掉。

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

View 和 Menu 对应 `back` 和 `start`。单只 Joy-Con 上，ZL、L、ZR、R 对应 `lt`、`leftshoulder`、`rt`、`rightshoulder`。写在当前功能键上的设置不生效，但留在文件里。Xbox 键是 `guide`，写在它上面的设置一律不生效，检查时会给出警告；在映射页改这个配置时，它们会从文件里去掉。功能键的一套写在 `functionSet` 里，写法和层一样。

保存文件后 padmate 自动重新读取。写完可在 `resources\bin` 文件夹运行 `.\padmatectl.exe validate profile.json` 检查，它会指出错在哪里，不改文件。
