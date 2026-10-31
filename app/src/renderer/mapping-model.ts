// Pure helpers for custom gamepad mappings and the mapping wizard (IN-17,
// IN-18, IN-19, ADR-0072): the words and pictures of each prompt, how an
// element is named, and what to tell the user after a mapping is saved. The
// rules that find the element are in mapwizard.ts.
import type { DeviceEntry, MappingResult } from "../shared/api";
import type { Config } from "../shared/generated/schema";
import type { ControlIconSpec } from "./control-icon";
import { EXCURSION, type RawKind, type WizardInput } from "./mapwizard";
import type { PadFace } from "./pad-icon";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** Whether config.json saves a mapping for the controller key (IN-18). */
export function hasCustomMapping(config: Config | null, key: string): boolean {
  const entry = config?.devices?.[key];
  return (
    !!entry &&
    typeof entry === "object" &&
    typeof entry.mapping === "string" &&
    entry.mapping !== ""
  );
}

/**
 * How the wizard names a controller to the daemon: by connection ID. Every
 * joystick of a model that SDL has no mapping for has the same key, so the key
 * does not say which one.
 */
export function padSelector(device: DeviceEntry): string {
  return String(device.info.id);
}

/** What one prompt of the wizard shows. */
export interface StepInfo {
  /** The control, as the review lists it. */
  name: string;
  /** The instruction. */
  prompt: string;
  /** How to do it, or where the control is. */
  hint: string;
  icon: ControlIconSpec;
}

type StepText = [name: string, prompt: string, hint: string];

// The wizard asks for the controls of the Xbox layout (SDL's standard), so
// its pictures are the Kenney Xbox icons (ADR-0079 §7); Home is the house.
const art = (name: string, face?: PadFace): ControlIconSpec =>
  face
    ? { kind: "art", name: `kenney/${name}`, face }
    : { kind: "art", name: `kenney/${name}` };

const pressHint = [
  "按一下，再松开",
  "Press and release",
] as const satisfies readonly [string, string];

interface StepDefinition {
  icon: ControlIconSpec;
  zh: StepText;
  en: StepText;
}

const facePosition = (
  position: [string, string],
  zhPrompt: string,
  enPrompt: string,
  letter: string,
  icon: ControlIconSpec,
): StepDefinition => ({
  icon,
  zh: [letter, zhPrompt, `右侧四个键中${position[0]}的那个，按一下再松开`],
  en: [
    letter,
    enPrompt,
    `The ${position[1]} one of the four buttons on the right. Press and release`,
  ],
});

const stickPush = (
  side: "L" | "R",
  direction: "left" | "right" | "up" | "down",
): StepDefinition => {
  const zhSide = side === "L" ? "左" : "右";
  const enSide = side === "L" ? "left" : "right";
  const zhWay = { left: "左", right: "右", up: "上", down: "下" }[direction];
  const zhFar = { left: "最左", right: "最右", up: "最上", down: "最下" }[
    direction
  ];
  return {
    icon: art(`xbox_stick_${side === "L" ? "l" : "r"}_${direction}`),
    zh: [
      `${zhSide}摇杆向${zhWay}`,
      `把${zhSide}摇杆推到${zhFar}，再放回中间`,
      "推到边缘再松手，不要斜着推",
    ],
    en: [
      `${side === "L" ? "Left" : "Right"} stick ${direction}`,
      `Push the ${enSide} stick all the way ${direction}, then back to the middle`,
      "Push it to the edge and let go. Don't push diagonally",
    ],
  };
};

const dpad = (
  arm: "up" | "down" | "left" | "right",
  zhWay: string,
): StepDefinition => ({
  icon: art(`xbox_dpad_${arm}`),
  zh: [`十字键${zhWay}`, `按下十字键的${zhWay}`, pressHint[0]],
  en: [`D-pad ${arm}`, `Press D-pad ${arm}`, pressHint[1]],
});

const shoulder = (
  label: string,
  zhSide: string,
  enSide: string,
): StepDefinition => ({
  icon: art(`xbox_${label.toLowerCase()}`),
  zh: [label, `按下 ${label} 键`, `${zhSide}肩键`],
  en: [label, `Press ${label}`, `The ${enSide} shoulder button`],
});

const trigger = (label: string): StepDefinition => ({
  icon: art(`xbox_${label.toLowerCase()}`),
  zh: [label, `压下 ${label} 扳机，再松开`, "压到底再松手"],
  en: [label, `Pull ${label}, then let go`, "Pull it all the way, then let go"],
});

const stepDefinitions: Record<string, StepDefinition> = {
  a: facePosition(
    ["下面", "bottom"],
    "按下 A 键",
    "Press A",
    "A",
    art("xbox_button_a", "green"),
  ),
  b: facePosition(
    ["右边", "right"],
    "按下 B 键",
    "Press B",
    "B",
    art("xbox_button_b", "red"),
  ),
  x: facePosition(
    ["左边", "left"],
    "按下 X 键",
    "Press X",
    "X",
    art("xbox_button_x", "blue"),
  ),
  y: facePosition(
    ["上面", "top"],
    "按下 Y 键",
    "Press Y",
    "Y",
    art("xbox_button_y", "yellow"),
  ),
  dpup: dpad("up", "上"),
  dpdown: dpad("down", "下"),
  dpleft: dpad("left", "左"),
  dpright: dpad("right", "右"),
  "ls-left": stickPush("L", "left"),
  "ls-right": stickPush("L", "right"),
  "ls-up": stickPush("L", "up"),
  "ls-down": stickPush("L", "down"),
  "ls-click": {
    icon: art("xbox_stick_l_press"),
    zh: ["左摇杆按下", "按下左摇杆", "把摇杆往下按，再松开"],
    en: [
      "Left stick click",
      "Click the left stick",
      "Press the stick down, then release",
    ],
  },
  "rs-left": stickPush("R", "left"),
  "rs-right": stickPush("R", "right"),
  "rs-up": stickPush("R", "up"),
  "rs-down": stickPush("R", "down"),
  "rs-click": {
    icon: art("xbox_stick_r_press"),
    zh: ["右摇杆按下", "按下右摇杆", "把摇杆往下按，再松开"],
    en: [
      "Right stick click",
      "Click the right stick",
      "Press the stick down, then release",
    ],
  },
  lb: shoulder("LB", "左", "left"),
  rb: shoulder("RB", "右", "right"),
  lt: trigger("LT"),
  rt: trigger("RT"),
  view: {
    icon: art("xbox_button_view"),
    zh: [
      "View（返回键）",
      "按下 View 键",
      "有的手柄叫 Back、Select 或 −，用于紧急暂停",
    ],
    en: [
      "View (Back)",
      "Press View",
      "Also called Back, Select or −. Used for the emergency pause",
    ],
  },
  menu: {
    icon: art("xbox_button_menu"),
    zh: [
      "Menu（开始键）",
      "按下 Menu 键",
      "有的手柄叫 Start、Options 或 +，用于紧急暂停",
    ],
    en: [
      "Menu (Start)",
      "Press Menu",
      "Also called Start, Options or +. Used for the emergency pause",
    ],
  },
  home: {
    icon: art("switch_button_home"),
    zh: [
      "Home（主页键）",
      "按下 Home 键",
      "有的手柄叫 Guide、Xbox 键或 PS 键；没有的话请跳过",
    ],
    en: [
      "Home",
      "Press Home",
      "Also called Guide, the Xbox button or the PS button. Skip it if there is none",
    ],
  },
};

/** The name, instruction, hint and picture of the wizard's step with this ID. */
export function stepInfo(id: string, language: Language): StepInfo {
  const definition = Object.hasOwn(stepDefinitions, id)
    ? stepDefinitions[id]
    : undefined;
  if (!definition) throw new Error(`unknown wizard step ${id}`);
  const [name, prompt, hint] =
    language === "en" ? definition.en : definition.zh;
  return { name, prompt, hint, icon: definition.icon };
}

const arrows: Record<number, string> = { 1: "↑", 2: "→", 4: "↓", 8: "←" };

function elementText(
  kind: RawKind,
  index: number,
  detail: string,
  language: Language,
): string {
  const base =
    kind === "button"
      ? tx(language, `按钮 ${index}`, `Button ${index}`)
      : kind === "hat"
        ? tx(language, `帽子 ${index}`, `Hat ${index}`)
        : tx(language, `轴 ${index}`, `Axis ${index}`);
  return detail ? `${base} ${detail}` : base;
}

/**
 * An element the wizard found, named by the number SDL counts it by: "按钮 3",
 * "帽子 0 ↑", "轴 2 −" (the way the axis went from its rest).
 */
export function elementName(input: WizardInput, language: Language): string {
  switch (input.kind) {
    case "button":
      return elementText("button", input.index, "", language);
    case "hat":
      return elementText(
        "hat",
        input.index,
        arrows[input.mask] ?? "",
        language,
      );
    case "axis":
      return elementText(
        "axis",
        input.index,
        input.far < input.rest ? "−" : "+",
        language,
      );
  }
}

/**
 * What a raw event shows of the element being pressed or pushed: key names the
 * element, text is what to show for it, or null when it is let go (a button up,
 * a hat centered, an axis within half of the wizard's excursion from its rest).
 */
export function liveElement(
  event: { kind: RawKind; index: number; value: number },
  axisRest: readonly number[],
  language: Language,
): { key: string; text: string | null } {
  const key = `${event.kind}:${event.index}`;
  switch (event.kind) {
    case "button":
      return {
        key,
        text:
          event.value !== 0
            ? elementText("button", event.index, "", language)
            : null,
      };
    case "hat":
      return {
        key,
        text:
          event.value !== 0
            ? elementText(
                "hat",
                event.index,
                arrows[event.value] ?? "",
                language,
              )
            : null,
      };
    case "axis": {
      const delta = event.value - (axisRest[event.index] ?? 0);
      return {
        key,
        text:
          Math.abs(delta) >= EXCURSION / 2
            ? elementText("axis", event.index, delta < 0 ? "−" : "+", language)
            : null,
      };
    }
  }
}

/**
 * The line for a mapping that leaves out View, Menu or both: they are the keys
 * of the emergency pause, so a controller without them cannot use it. null
 * when neither is missing.
 */
export function missingKeysNotice(
  back: boolean,
  start: boolean,
  language: Language,
): string | null {
  if (back && start)
    return tx(
      language,
      "没有映射 View 和 Menu。它们是紧急暂停的按键，缺了它们这只手柄无法用紧急暂停。",
      "View and Menu are not mapped. They are the buttons of the emergency pause, so without them this controller cannot use it.",
    );
  if (!back && !start) return null;
  const name = back ? "View" : "Menu";
  return tx(
    language,
    `没有映射 ${name}。它是紧急暂停的按键，缺了它这只手柄无法用紧急暂停。`,
    `${name} is not mapped. It is a button of the emergency pause, so without it this controller cannot use it.`,
  );
}

/**
 * What to tell the user after a mapping is saved, one line each: View or Menu
 * is missing (the emergency pause needs both), SDL reads another mapping or
 * still reads a joystick (the mapping is saved and does nothing), or the
 * daemon could not apply the saved settings. An empty list: all is well.
 */
export function mappingNotices(
  result: MappingResult,
  language: Language,
): string[] {
  const codes = new Set(result.warnings.map((warning) => warning.code));
  const lines: string[] = [];
  const missing = missingKeysNotice(
    codes.has("noBack"),
    codes.has("noStart"),
    language,
  );
  if (missing) lines.push(missing);
  if (codes.has("shadowed"))
    lines.push(
      tx(
        language,
        "SDL 读到的是另一份映射（可能来自环境变量 SDL_GAMECONTROLLERCONFIG），刚保存的映射没有生效。",
        "SDL reads another mapping for this controller, probably from the SDL_GAMECONTROLLERCONFIG environment variable, so the mapping just saved is not in effect.",
      ),
    );
  if (codes.has("notGamepad"))
    lines.push(
      tx(
        language,
        "SDL 仍把它当作普通摇杆，映射没有生效。请至少映射一个按钮或轴，再试一次。",
        "SDL still reads this controller as a plain joystick, so the mapping is not in effect. Map at least one button or axis and try again.",
      ),
    );
  if (!result.applied)
    lines.push(
      result.problem
        ? tx(
            language,
            `已保存，但还没有生效：${result.problem}`,
            `Saved, but not in effect yet: ${result.problem}`,
          )
        : tx(
            language,
            "已保存，但还没有生效。",
            "Saved, but not in effect yet.",
          ),
    );
  return lines;
}

/** What a failed step of the wizard says: the raw input, saving, or restoring the default. */
export function mappingFailureText(
  error: { code?: number },
  phase: "start" | "save" | "reset",
  language: Language,
): string {
  if (error.code === -32004)
    return tx(
      language,
      "手柄已断开。",
      "The controller is no longer connected.",
    );
  if (error.code === -32026)
    return phase === "start"
      ? tx(
          language,
          "无法读取这只手柄的原始输入。",
          "The controller's raw input cannot be read.",
        )
      : tx(
          language,
          "当前的输入不支持手柄映射。",
          "This input does not support gamepad mappings.",
        );
  if (error.code === -32025 && phase === "save")
    return tx(language, "这份映射无法保存。", "This mapping cannot be saved.");
  switch (phase) {
    case "start":
      return tx(
        language,
        "无法读取手柄的原始输入，原因见“设置 → 日志”。",
        "The controller's raw input could not be read. See Settings → Logs.",
      );
    case "save":
      return tx(
        language,
        "映射没有保存，原因见“设置 → 日志”。",
        "The mapping was not saved. See Settings → Logs.",
      );
    case "reset":
      return tx(
        language,
        "没有恢复默认映射，原因见“设置 → 日志”。",
        "The default mapping was not restored. See Settings → Logs.",
      );
  }
}
