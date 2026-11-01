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

## padmate 的显示修改

统一尺寸与中心，设置灯光与材质，隐藏 Xbox 背部拨片，以 SDL 设备族标签绘制面键标记，添加实时输入标记、摇杆倾斜及引线。仅提供固定正面视图，每个标准控件独立引线，四个肩键从上方引线指示，中间三个功能键从下方以竖线指示。Xbox 外形也作为 Nintendo/未知设备的通用标准布局；PlayStation 使用 DualSense 外形，均不声称精确复刻每款已连接设备。

保留作者与本文件；界面通过“设置 → 关于与许可”集中展示作者、原模型地址、许可及修改说明，总览不常驻署名。CC BY 4.0 第 3(a)(2) 节允许根据媒介与使用场景采用合理的署名方式。这些是独立作者模型，不是微软或索尼官方素材，也不表示他们认可本项目。

## 资源重建

正式构建直接使用已入库的 GLB，无需下载素材。需要重新打包时，将上述 Xbox 固定版本的 `scene.gltf`、`scene.bin` 与其引用的贴图按原目录结构放入 `.cache/asset-candidates/xbox/`，将已核对许可的 DualSense 文件放入 `.cache/asset-candidates/dualsense.glb`，然后从仓库根目录运行 `node app/scripts/pack-controller-models.mjs`。脚本仅打包本地素材，不是 `task gen` 的一部分。

当前产物 SHA-256：

- `xbox.glb`：`a4eb1fc09d802f8146b964c36850b3d13cb687906a761d605948848dc7fdaf14`
- `dualsense.glb`：`e55e172f3a6704769818954970fda2d29038a31f85ba7358a36d0549df4f9d30`
