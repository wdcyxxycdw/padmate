# 手柄模型素材

这些素材独立采用 **Creative Commons Attribution 4.0 International（CC BY 4.0）**，不适用仓库代码的 MIT 许可。

许可条款：https://creativecommons.org/licenses/by/4.0/

## Xbox Elite Controller — Rusfort

- 作者：https://sketchfab.com/Rusfort
- 原模型：https://sketchfab.com/3d-models/xbox-elite-controller-32aad0ff25f7446b8afe649ea3cc9e43
- 获取来源：[公开镜像，固定 revision 4883b07b2f9d7248fd7ca068188e02ad8910d027](https://github.com/Adel-Yasser-dev/xbox-series-x/tree/4883b07b2f9d7248fd7ca068188e02ad8910d027/public/assets/models/xbox_elite_controller)
- 原模型页面及 glTF 元数据均声明 CC BY 4.0。作者说明外形并非严格 1:1。
- `xbox.glb` 把原 glTF、scene.bin 及五张 WebP 贴图打包为单文件，保留原几何、贴图与元数据。没有采用镜像仓库的应用代码。

## PS5 Controller — Taohid Animation

- 作者：https://sketchfab.com/taohidanimation
- 原模型：https://sketchfab.com/3d-models/ps5-controller-b7bb9c5102a04cb0b1966c6d02bad7d6
- 获取来源：[dualsense-studio 的控件拆分模型及署名](https://github.com/SafaElmali/dualsense-studio/blob/main/controller/ATTRIBUTION.md)
- 原模型页面与 GLB 元数据均声明 CC BY 4.0。衍生作者将控件拆开并命名、烘焙变换，保留原几何及纹理。没有采用该仓库的应用代码。

## DualShock 4 PlayStation Controller — shaielwolf

- 作者：https://sketchfab.com/shaielwolf
- 原模型：https://sketchfab.com/3d-models/dualshock-4-playstation-controller-e3c2f0dc16524fc19cdde45bad1de1a9
- 获取来源：由项目所有者从 Sketchfab 下载。原模型页面、随模型下载的 `license.txt` 与 glTF 的 `asset.extras` 均声明 CC BY 4.0。
- 署名文字：This work is based on "DualShock 4 PlayStation Controller" (https://sketchfab.com/3d-models/dualshock-4-playstation-controller-e3c2f0dc16524fc19cdde45bad1de1a9) by shaielwolf (https://sketchfab.com/shaielwolf) licensed under CC-BY-4.0 (http://creativecommons.org/licenses/by/4.0/)
- `dualshock4.glb` 把原 glTF、scene.bin 及贴图打包为单文件，几何、节点名、材质和元数据不变。修改只在贴图上：PNG 转成 WebP，基础色和自发光贴图最长边不超过 1024 px（质量 85），金属度/粗糙度贴图不超过 512 px（质量 80）；四张 4096 px 的法线贴图去掉；不再被引用的图片和采样器一并删除。

## Nintendo Switch OLED — Wevensson

- 作者：https://sketchfab.com/Wevensson
- 原模型：https://sketchfab.com/3d-models/nintendo-switch-oled-aa2c7d815a104accae7709eb39ac2184
- 获取来源：由项目所有者从 Sketchfab 下载。原模型页面与随模型下载的 `license.txt` 均声明 CC BY 4.0。
- 署名文字：This work is based on "Nintendo Switch OLED" (https://sketchfab.com/3d-models/nintendo-switch-oled-aa2c7d815a104accae7709eb39ac2184) by Wevensson (https://sketchfab.com/Wevensson) licensed under CC-BY-4.0 (http://creativecommons.org/licenses/by/4.0/)
- `joycon-left.glb` 与 `joycon-right.glb` 取自这个模型，只保留左右两只 Joy-Con，主机、屏幕、支架和腕带去掉。每只 Joy-Con 移到自己的坐标系里（居中、直立、正面朝 +Z），拆成分别命名的部件，每个部件各有一份材质，贴图相同。贴图转成 WebP：基础色最长边不超过 1024 px（质量 85），金属度/粗糙度和法线贴图不超过 512 px（质量 80）。几何不另作改动。背面仍带原贴图印着的标志和标记，界面只显示正面。

## Nintendo Switch 2 Realistic 3D Model — NAIT Studio

- 作者：https://sketchfab.com/naitstudio
- 原模型：https://sketchfab.com/3d-models/nintendo-switch-2-realistic-3d-model-03e83c4832fc4cdcacb7724937509908
- 获取来源：由项目所有者从 Sketchfab 下载。原模型页面与随模型下载的 `license.txt` 均声明 CC BY 4.0。
- 署名文字：This work is based on "Nintendo Switch 2 Realistic 3D Model" (https://sketchfab.com/3d-models/nintendo-switch-2-realistic-3d-model-03e83c4832fc4cdcacb7724937509908) by NAIT Studio (https://sketchfab.com/naitstudio) licensed under CC-BY-4.0 (http://creativecommons.org/licenses/by/4.0/)
- `joycon2-left.glb` 与 `joycon2-right.glb` 取自这个模型，只保留左右两只 Joy-Con 2，主机、屏幕、支架等去掉。每只 Joy-Con 2 移到自己的坐标系里（居中、直立、正面朝 +Z，单位换算成米），拆成分别命名的部件（摇杆、摇杆底座、方向键或 A/B/X/Y、− 或 +、Home 或 Capture、C 键、肩键、导轨等），每个部件各有一份材质；原模型没有贴图，外观由材质的基础色决定。顶点位置、法线和索引量化存储（KHR_mesh_quantization），触点针脚和指示灯小点等细节去掉。原模型没有 SL、SR、ZL、ZR 的几何，文件里用同名的空节点标出它们的位置，只用来放置界面的引线，不画出形状。

## padmate 的显示修改

统一尺寸与中心，设置灯光与材质，隐藏 Xbox 背部拨片，以 SDL 设备族标签绘制面键标记，添加实时输入标记、摇杆倾斜及引线。仅提供固定正面视图，每个标准控件独立引线，四个肩键从上方引线指示，中间三个功能键从下方以竖线指示。

外形按设备族选：PS5 使用 DualSense，PS4 与 PS3 使用 DualShock 4，Joy-Con 使用 Joy-Con 模型，其余手柄（Xbox、Switch Pro 和未知设备）使用 Xbox，也作为它们的通用标准布局。Joy-Con 有三种显示：横握的单只 Joy-Con 横着放，直握的单只 Joy-Con 竖着放，左右两只连在一起（SDL 自动合成一个完整手柄）时并排显示成一对。Joy-Con 2 按 USB 编号认出（厂商 0x057e，产品 0x2067 左、0x2066 右、0x2068 一对，守护进程经蓝牙 LE 报告），用 Switch 2 的 Joy-Con 模型，摆法与上面相同；Switch OLED 的 Joy-Con 仍用 OLED 模型。均不声称精确复刻每款已连接设备。

保留作者与本文件；界面通过“设置 → 关于与许可”集中展示作者、原模型地址、许可及修改说明，总览不常驻署名。CC BY 4.0 第 3(a)(2) 节允许根据媒介与使用场景采用合理的署名方式。这些是独立作者模型，不是微软、索尼或任天堂官方素材，也不表示他们认可本项目。

## 资源重建

正式构建直接使用已入库的 GLB，无需下载素材。需要重新打包时，素材按下面的目录放在仓库根目录下，再从仓库根目录运行 `node app/scripts/pack-controller-models.mjs`：

- `.cache/asset-candidates/xbox/`：上述 Xbox 固定版本的 `scene.gltf`、`scene.bin` 与其引用的贴图，保持原目录结构。
- `.cache/asset-candidates/dualsense.glb`：已核对许可的 DualSense 文件，脚本原样复制。
- `.cache/asset-candidates/ds4/`：DualShock 4 的 `scene.gltf`、`scene.bin` 与其引用的 PNG 贴图，保持原目录结构。
- `.cache/asset-candidates/switch-oled/`：Switch OLED 的 `scene.gltf`、`scene.bin`、`textures/` 与 `license.txt`，保持原目录结构。`joycon-left.glb` 与 `joycon-right.glb` 从它提取。
- `.cache/asset-candidates/switch2/`：Switch 2 的 `scene.gltf`、`scene.bin` 与 `license.txt`，保持原目录结构。`joycon2-left.glb` 与 `joycon2-right.glb` 从它提取；没有贴图，不需要 sharp。

可以在脚本后面写素材名（`xbox`、`dualshock4`、`dualsense`、`switch-oled`、`switch2`），只重新打包这几个；环境变量 `PADMATE_ASSET_CACHE` 指向另一处 `asset-candidates` 目录（例如在 worktree 里用主仓库的），`PADMATE_PACK_LIST=1` 打印 Joy-Con 2 各件的命名。

PNG 转 WebP 用 sharp，它不在 app 的依赖里，要先在仓库根目录安装：`npm install --prefix .cache/asset-tools sharp@0.34.5`。某个素材不在位时，脚本跳过它，该 GLB 保持入库的样子。脚本仅打包本地素材，不是 `task gen` 的一部分。

当前产物 SHA-256：

- `xbox.glb`：`a4eb1fc09d802f8146b964c36850b3d13cb687906a761d605948848dc7fdaf14`
- `dualsense.glb`：`e55e172f3a6704769818954970fda2d29038a31f85ba7358a36d0549df4f9d30`
- `dualshock4.glb`：`c87f473bbde04e44c5e770503acc53854cd9e74f27c2c715f6138d70c7f5f0fa`
- `joycon-left.glb`：`bc67263884a5b85aaf9c2166e61bcea6c2d8297f908541df719a14eedd1cb9aa`
- `joycon-right.glb`：`f752fb61cd2a4995a9e806ecf9a15b5f6bbb130f1b25df8adcf4e074b4932fd7`
