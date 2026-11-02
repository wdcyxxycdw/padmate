// A page that shows every placement of the Joy-Cons side by side, with the
// real Controller (the 3D model, the chips and the leader lines) and made-up
// bindings, so that the layouts can be looked at in a browser:
//
//   npm run preview:layouts      then open http://127.0.0.1:5199/controller-layouts.html
//
// Options in the address: ?size=880x600 (the stage of each, 440 by 440 at
// least), ?lang=en or zh-CN, ?gen=1 or 2 (Switch 1 or 2 only), ?only=a,b (some
// of: left-vertical, right-vertical, pair), ?notice=1 (the editor notice's
// lift of the editor notice, for the rows that have one), ?check=1 (once every
// controller has drawn, reads the chips and leaders back from the DOM, checks
// them (layout-check.ts) and writes the result as JSON into <pre
// id="layout-check">; tests/preview/check-layouts.mjs runs it headless).
import { StrictMode, useEffect } from "react";
import { createRoot } from "react-dom/client";
import { Controller } from "../../src/renderer/controller/Controller";
import { controllerCallouts } from "../../src/renderer/controller/controller-callouts";
import type { ChipInfo } from "../../src/renderer/controller/controller-chips";
import { createSceneWithOwnStage } from "../../src/renderer/controller/controller-scene";
import { translator } from "../../src/renderer/i18n";
import { joyCon2Pads, sdlPads } from "../../src/renderer/testing/sdl-pads";
import type { DeviceInfo } from "../../src/shared/api";
import "../../src/renderer/theme.css";
import "../../src/renderer/ui/ui.css";
import "../../src/renderer/controller/controller-3d.css";
import "../../src/renderer/workspace/workspace.css";
import "./controller-layouts.css";
import { type CellCheck, checkCell } from "./layout-check";

type Language = "zh-CN" | "en";

const placements = [
  ["left-vertical", "Left Joy-Con", "joyConLeftUpright"],
  ["right-vertical", "Right Joy-Con", "joyConRightUpright"],
  ["pair", "Pair", "joyConPair"],
] as const;
const generations = [
  ["1", "Switch 1 (OLED model)", sdlPads],
  ["2", "Switch 2 (NAIT model)", joyCon2Pads],
] as const;

/** Shortcuts to put on the chips, one after another. */
const shortcuts = [
  "ctrl+c",
  "ctrl+v",
  "space",
  "enter",
  "esc",
  "tab",
  "f5",
  "shift+a",
  "alt+tab",
  "ctrl+z",
  "ctrl+s",
  "pagedown",
  "home",
  "ctrl+shift+t",
];

/** A chip with a shortcut on it, or with nothing set. */
function fakeChip(index: number): ChipInfo {
  if (index % 7 === 6)
    return {
      title: "Not set",
      detail: [],
      summary: "",
      state: "unbound",
      scriptOwned: false,
    };
  const keys = shortcuts[index % shortcuts.length];
  return {
    title: keys,
    titleKeys: { keys },
    detail: [{ gesture: "Tap", keys }],
    summary: keys,
    state: "bound",
    scriptOwned: false,
  };
}

function fakeDevice(
  id: number,
  name: string,
  pad: (typeof sdlPads)[keyof typeof sdlPads],
): DeviceInfo {
  return {
    id,
    name,
    vendor: 0x057e,
    product: 0,
    version: 0,
    guid: "",
    path: "",
    driver: "",
    serial: "",
    gamepad: true,
    virtual: true,
    ...pad,
  };
}

function Cell({
  title,
  info,
  size,
  language,
  notice,
}: {
  title: string;
  info: DeviceInfo;
  size: { width: number; height: number };
  language: Language;
  notice: boolean;
}) {
  const callouts = controllerCallouts(info, language);
  const chips: Record<string, ChipInfo> = {};
  callouts.forEach((callout, index) => {
    chips[callout.control] = fakeChip(index);
    if (callout.span) chips[`${callout.control}.press`] = fakeChip(index + 3);
  });
  devices.set(info.id, { info, title, notice });
  return (
    <section className="preview-cell" data-layout={title} data-device={info.id}>
      <h2>{title}</h2>
      <div
        className="controller-view"
        style={{ width: size.width, height: size.height }}
      >
        <Controller
          info={info}
          t={translator(language)}
          language={language}
          chips={chips}
          createScene={createOwnScene(info)}
        />
        {notice && <div className="editor-notice" />}
      </div>
    </section>
  );
}

/** The controllers on the page, for the self-check. */
const devices = new Map<
  number,
  { info: DeviceInfo; title: string; notice: boolean }
>();

/** Waits for every controller to draw and take its arranged layout, then checks. */
async function selfCheck(): Promise<CellCheck[]> {
  const sections = [...document.querySelectorAll(".preview-cell")];
  const ready = () =>
    sections.every((section) => {
      const stage = section.querySelector(".controller-3d");
      return (
        stage?.getAttribute("data-renderer") === "webgl" &&
        stage.getAttribute("data-arranged") === "true"
      );
    });
  for (let waited = 0; !ready(); waited += 100) {
    if (waited > 90000) throw new Error("The controllers did not draw in time");
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  // The leaders are drawn again a frame after the chips move.
  await new Promise((resolve) => setTimeout(resolve, 600));
  const results: CellCheck[] = [];
  for (const section of sections) {
    const entry = devices.get(Number(section.getAttribute("data-device")));
    if (entry)
      results.push(
        await checkCell(section, entry.info, entry.title, entry.notice),
      );
  }
  return results;
}

function SelfCheck() {
  useEffect(() => {
    const out = document.createElement("pre");
    out.id = "layout-check";
    document.body.append(out);
    selfCheck().then(
      (results) => {
        const ok = results.every((r) => r.problems.length === 0);
        out.textContent = JSON.stringify({ ok, results }, null, 2);
        out.dataset.done = ok ? "ok" : "failed";
        console.log(out.textContent);
      },
      (error: unknown) => {
        out.textContent = JSON.stringify({ ok: false, error: String(error) });
        out.dataset.done = "error";
        console.log(out.textContent);
      },
    );
    return () => out.remove();
  }, []);
  return null;
}

// One scene of its own for each Controller: the app shares one stage, because
// it shows one controller.
const scenes = new Map<number, ReturnType<typeof createSceneWithOwnStage>>();
function createOwnScene(info: DeviceInfo) {
  let scene = scenes.get(info.id);
  if (!scene) {
    scene = createSceneWithOwnStage();
    scenes.set(info.id, scene);
  }
  return scene;
}

function Page() {
  const query = new URLSearchParams(location.search);
  const match = /^(\d+)x(\d+)$/.exec(query.get("size") ?? "");
  const size = {
    width: Math.max(440, Number(match?.[1] ?? 880)),
    height: Math.max(440, Number(match?.[2] ?? 600)),
  };
  const language: Language = query.get("lang") === "en" ? "en" : "zh-CN";
  const only = query.get("only")?.split(",");
  const notice = query.get("notice") === "1";
  let id = 100;
  return (
    <main className="preview-page">
      <header>
        <h1>Controller layouts</h1>
        <p>
          {size.width} x {size.height} px each, {language}. Options in the
          address: size, lang, gen, only, notice.
        </p>
      </header>
      {generations
        .filter(([gen]) => !query.get("gen") || query.get("gen") === gen)
        .map(([gen, name, pads]) => (
          <div key={gen} className="preview-generation">
            <h2>{name}</h2>
            <div className="preview-grid">
              {placements
                .filter(([key]) => !only || only.includes(key))
                .map(([key, title, pad]) => (
                  <Cell
                    key={`${gen}-${key}`}
                    title={`${name} - ${title}`}
                    info={fakeDevice(++id, `${name} ${title}`, pads[pad])}
                    size={size}
                    language={language}
                    notice={notice}
                  />
                ))}
            </div>
          </div>
        ))}
      {query.get("check") === "1" && <SelfCheck />}
    </main>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing root");
createRoot(root).render(
  <StrictMode>
    <Page />
  </StrictMode>,
);
