import { useState } from "react";
import oflText from "./pad-icons/promptfont/LICENSE.txt?raw";
import type { Language } from "./store";
import { Dialog } from "./ui/controls";
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
];

// The button icons (ADR-0079 §7, pad-icons/README.md). PromptFont's license
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
              "padmate 调整了尺寸、灯光、材质与按键标签，隐藏背部拨片并添加输入反馈。Xbox 素材重新打包为 GLB；DualSense 素材此前已由衍生作者拆分控件、命名并烘焙变换。",
              "padmate adjusts scale, lighting, materials and button labels, hides paddles and adds input feedback. Xbox assets are repacked as GLB; the DualSense derivative previously split and named controls and baked transforms.",
            )}
          </p>
          <p className="page-licenses-note">
            {tx(
              language,
              "素材按许可条款原样提供，不附带担保。这些是独立作者作品，非微软或索尼官方素材，不表示其认可本项目。",
              "Assets are provided as-is, without warranties, under the license terms. These independent works are not official Microsoft or Sony assets and do not imply endorsement.",
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
