import { useState } from "react";
import oflText from "../../controller/pad-icons/promptfont/LICENSE.txt?raw";
import type { Language } from "../../state/store";
import { Dialog } from "../../ui/controls";
import "./model-licenses.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

const models = [
  {
    title: "Xbox Elite Controller",
    author: "Rusfort",
    source:
      "https://sketchfab.com/3d-models/xbox-elite-controller-32aad0ff25f7446b8afe649ea3cc9e43",
  },
  {
    title: "PS5 Controller",
    author: "Taohid Animation",
    source:
      "https://sketchfab.com/3d-models/ps5-controller-b7bb9c5102a04cb0b1966c6d02bad7d6",
  },
  {
    title: "DualShock 4 PlayStation Controller",
    author: "shaielwolf",
    source:
      "https://sketchfab.com/3d-models/dualshock-4-playstation-controller-e3c2f0dc16524fc19cdde45bad1de1a9",
  },
  {
    title: "Nintendo Switch OLED",
    author: "Wevensson",
    source:
      "https://sketchfab.com/3d-models/nintendo-switch-oled-aa2c7d815a104accae7709eb39ac2184",
  },
  {
    title: "Nintendo Switch 2 Realistic 3D Model",
    author: "NAIT Studio",
    source:
      "https://sketchfab.com/3d-models/nintendo-switch-2-realistic-3d-model-03e83c4832fc4cdcacb7724937509908",
  },
];

// The button icons (ADR-0013, pad-icons/README.md). PromptFont's license
// asks for its text to go with every copy, so the dialog carries it.
const iconSets = [
  {
    title: "Input Prompts 1.5a",
    author: "Kenney",
    source: "https://kenney.nl/assets/input-prompts",
    license: "Creative Commons Zero (CC0 1.0)",
  },
  {
    title: "PromptFont",
    author: 'Yukari "Shinmera" Hafner',
    source: "https://shinmera.com/projects/promptfont",
    license: "SIL Open Font License 1.1",
  },
];

/**
 * The "关于与许可" button and its dialog: padmate's license, the 3D models'
 * and the button icons'.
 */
export function ModelLicenses({
  language,
  version,
}: {
  language: Language;
  /** Shown in the dialog subtitle, for example "v0.12.0". */
  version?: string;
}) {
  const [open, setOpen] = useState(false);
  const title = tx(language, "关于与许可", "About & licenses");
  return (
    <>
      <button type="button" className="btn" onClick={() => setOpen(true)}>
        {title}
      </button>
      <Dialog
        open={open}
        title={title}
        subtitle={version ? `padmate ${version}` : undefined}
        closeLabel={tx(language, "关闭关于与许可", "Close about & licenses")}
        onClose={() => setOpen(false)}
        className="page-licenses"
      >
        <section className="page-licenses-block">
          <h3>padmate</h3>
          <p>
            {tx(
              language,
              "padmate 以 MIT 许可发布。",
              "padmate is released under the MIT License.",
            )}
          </p>
        </section>
        <section className="page-licenses-block">
          <h3>{tx(language, "3D 手柄模型", "3D controller models")}</h3>
          <p>
            {tx(
              language,
              "3D 手柄素材采用 Creative Commons Attribution 4.0 International（CC BY 4.0）许可。",
              "3D controller assets are licensed under Creative Commons Attribution 4.0 International (CC BY 4.0).",
            )}
          </p>
          <p className="page-licenses-uri">
            https://creativecommons.org/licenses/by/4.0/
          </p>
          <ul className="page-licenses-models">
            {models.map((model) => (
              <li key={model.title}>
                <span className="page-licenses-model">
                  {model.title} — {model.author}
                </span>
                <span className="page-licenses-uri">{model.source}</span>
              </li>
            ))}
          </ul>
          <p className="page-licenses-note">
            {tx(
              language,
              "padmate 调整了尺寸、灯光、材质与按键标签，隐藏背部拨片并添加输入反馈。Xbox、DualShock 4、Switch OLED 和 Switch 2 素材重新打包为 GLB。DualShock 4 的贴图缩小并去掉了法线贴图；Switch OLED 只保留左右两只 Joy-Con，各放进自己的坐标系并拆成命名的部件，贴图转成 WebP 并缩小，背面仍带原贴图上的标志；Switch 2 同样只保留左右两只 Joy-Con 2，拆成命名的部件，顶点量化存储，细小的触点和指示灯去掉，没有几何的 SL、SR、ZL、ZR 只留位置；DualSense 素材此前已由衍生作者拆分控件、命名并烘焙变换。",
              "padmate adjusts scale, lighting, materials and button labels, hides paddles and adds input feedback. Xbox, DualShock 4, Switch OLED and Switch 2 assets are repacked as GLB. The DualShock 4 textures are downscaled and its normal maps removed; of the Switch OLED only the two Joy-Cons are kept, each in its own frame and split into named parts, with the textures converted to WebP and downscaled, and the back still shows the logo of the original texture; of the Switch 2 likewise only the two Joy-Con 2s are kept, split into named parts with quantized vertices, the tiny contact pins and indicator dots left out, and only a position kept for SL, SR, ZL and ZR, which the model has no geometry for; the DualSense derivative previously split and named controls and baked transforms.",
            )}
          </p>
          <p className="page-licenses-note">
            {tx(
              language,
              "素材按许可条款原样提供，不附带担保。这些是独立作者作品，非微软、索尼或任天堂官方素材，不表示其认可本项目。",
              "Assets are provided as-is, without warranties, under the license terms. These independent works are not official Microsoft, Sony or Nintendo assets and do not imply endorsement.",
            )}
          </p>
        </section>
        <section className="page-licenses-block">
          <h3>{tx(language, "按键图标", "Button icons")}</h3>
          <ul className="page-licenses-models">
            {iconSets.map((set) => (
              <li key={set.title}>
                <span className="page-licenses-model">
                  {set.title} — {set.author}
                </span>
                <span className="page-licenses-uri">{set.source}</span>
                <span className="page-licenses-note">{set.license}</span>
              </li>
            ))}
          </ul>
          <p className="page-licenses-note">
            {tx(
              language,
              "摇杆旋转和“任意方向”的图标取自 PromptFont 的字形，其余按键图标取自 Kenney 的 Input Prompts。padmate 只保留了图形，颜色随界面变化。",
              "The icons of a stick turning or pushed in any direction are PromptFont glyphs; the other button icons are from Kenney's Input Prompts. padmate keeps only the shapes and colours them with the interface.",
            )}
          </p>
          <details className="page-licenses-text">
            <summary>
              {tx(
                language,
                "SIL Open Font License 1.1 全文",
                "Full text of the SIL Open Font License 1.1",
              )}
            </summary>
            <pre>{oflText}</pre>
          </details>
        </section>
      </Dialog>
    </>
  );
}
