# 手柄按键图标

界面里手柄按键的图标（手柄图的标签、按键抽屉的标题、按键预览，以及设置页“功能键”、“所有按键”列表、诊断页事件列表和映射向导）取自两套开放许可的图标集。这些文件独立采用各自的许可，不适用仓库代码的 MIT 许可。`pad-icon.tsx` 读取它们，并决定每个按键用哪一个。

## Kenney Input Prompts 1.5a：`kenney/`

- 作者：Kenney（https://www.kenney.nl）
- 来源：https://kenney.nl/assets/input-prompts ，2026-10-31 下载的 1.5a 版（创建日期 2026-07-11）
- 许可：Creative Commons Zero（CC0 1.0），原文在 `kenney/License.txt`，不要求署名
- 用到的：Xbox Series、PlayStation Series 和 Nintendo Switch 三套里的面键、十字键、肩键、扳机、系统键、摇杆按下、摇杆四个方向、Xbox Elite 和 DualSense Edge 的背键、Joy-Con 的 SL/SR，以及 Switch 的“四个面键里的一个”，共 98 个。文件名与包里 `Vector/` 下的原名相同。

## PromptFont：`promptfont/`

- 作者：Yukari "Shinmera" Hafner
- 来源：https://shinmera.com/projects/promptfont 。文件取自 GitHub 上的旧仓库 `Shinmera/promptfont`（2025 年 8 月迁到 Codeberg 之前的最后提交 ca3d628）里的 `glyphs/gamepad/`
- 许可：SIL Open Font License 1.1，原文在 `promptfont/LICENSE.txt`。最前面的两行版权声明（Copyright 2018-2023, Yukari Hafner；Copyright 2011-2016, Severin Meyer）取自同一提交里字体源文件 `promptfont.sfd` 的 Copyright 字段，原仓库的 LICENSE.txt 里没有。许可要求版权声明和许可随副本一起提供，所以“设置 → 关于与许可”里附了这份全文
- 用到的：左右摇杆的顺时针、逆时针旋转（`analog-l-cl`、`analog-l-co` 等）和“任意方向”（`analog-l-lrud` 等），共 6 个。Kenney 没有这几种。

## 怎么处理过

只保留了图形：每个文件只剩 `<svg viewBox>` 和若干 `<path>`（填充色、`fill-rule`、`transform` 照原样）。删掉了 Inkscape 和 Flash 导出的元数据、注释、空的 `<text>` 和透明的路径；PromptFont 图层组上的平移改到了 `viewBox` 上。图形本身没有改。`pad-icon.test.tsx` 检查文件里只有这两种元素、每个文件都被某个按键用到。

显示时，白色和黑色的填充取周围文字的颜色（`currentColor`），Kenney 十字键上表示“按下的方向”的红色取界面的强调色（`--pad-icon-highlight`），所以图标跟着界面配色和面键的颜色走。

要换版本或加图标：把新文件按上面的方式整理后放进对应的文件夹，在 `pad-icon.tsx` 里接上，`pad-icon.test.tsx` 会指出没用到或缺少的文件。

这些是独立作者的作品，不是微软、索尼或任天堂的官方素材，也不表示他们认可本项目。
