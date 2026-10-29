// The shortcut table export (OUT-18): which shortcut each gamepad button
// types, and which command to give that shortcut in the painting app. Only
// keys actions of the base layer are listed, the only layer the GUI shows
// (the function set is not).
import type {
  Profile,
  ProfileBinding,
  ProfileKeysAction,
} from "../shared/generated/schema";
import { keyText } from "./controller-chips";
import {
  BASE_LAYER,
  baseBinding,
  canonicalControl,
  type FunctionKey,
  type Gesture,
  gestureName,
  isFunctionKey,
  usedGestures,
} from "./editor-model";
import {
  isHoldAction,
  isModifierOnly,
  normaliseChord,
  usesHighFunctionKey,
} from "./shortcut-keys";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export interface SheetOptions {
  language: Language;
  /**
   * The function key (ADR-0075): the daemon ignores what a layer binds on
   * it, so it is no shortcut of the table.
   */
  functionKey: FunctionKey;
  /** A control as printed on the pad, e.g. "十字键 ←". Custom names win. */
  controlName?: (control: string) => string;
  /**
   * The name the title shows; profile.name when absent. The fixed default
   * goes by its ID, whatever name it stores (PRO-28).
   */
  profileName?: string;
}

export interface ShortcutSheet {
  title: string;
  intro: string;
  header: [string, string, string];
  rows: [string, string, string][];
  /** Shown instead of the table when it has no rows. */
  empty: string;
  /** Names settings outside the base layer that are left out; "" if none. */
  footnote: string;
  notes: string[];
}

type Bindings = Record<string, ProfileBinding>;

// What a file binds on the function key is ignored by the daemon, so it is
// not a shortcut of the table.
function entries(
  layer: unknown,
  functionKey: FunctionKey,
): [string, ProfileBinding][] {
  return Object.entries((layer ?? {}) as Bindings).filter(
    ([key, binding]) =>
      key !== "color" &&
      !isFunctionKey(key, functionKey) &&
      !!binding &&
      typeof binding === "object",
  );
}

/**
 * A button for the table and for messages: its custom or printed name, and
 * the gesture too when its binding has several, e.g. "A 长按".
 */
function buttonName(
  profile: Profile,
  control: string,
  gesture: Gesture,
  { language, controlName }: SheetOptions,
): string {
  const key = canonicalControl(control);
  const name = profile.controlNames?.[key] || controlName?.(key) || key;
  return usedGestures(baseBinding(profile, key)).length > 1
    ? `${name} ${gestureName(gesture, language)}`
    : name;
}

function chords(action: ProfileKeysAction): string[] {
  const keys = action.keys as ProfileKeysAction["keys"] | undefined;
  return (Array.isArray(keys) ? keys : [keys]).filter(
    (chord): chord is string => typeof chord === "string" && !!chord.trim(),
  );
}

/** The 请在软件中设置为 cell of one keys action. */
function commandCell(
  label: string,
  keys: string[],
  hold: boolean,
  t: (zh: string, en: string) => string,
): string {
  const [chord = ""] = keys;
  if (keys.length === 1 && isModifierOnly(chord)) {
    // A modifier is no command to assign. A label that only names it
    // ("Ctrl") adds nothing; one like "吸管" says what holding it is for.
    const name = normaliseChord(label) === normaliseChord(chord) ? "" : label;
    const cell = hold
      ? t(
          `${name}（按住即可，无需设置）`,
          `${name} (just hold it; nothing to set)`,
        )
      : t(`${name}（无需设置）`, `${name} (nothing to set)`);
    return cell.trim();
  }
  // Shortcuts without F13–F24 are usually the app's own, e.g. Ctrl+Z.
  if (usesHighFunctionKey(keys)) return label || t("（未命名）", "(unnamed)");
  return label
    ? t(`${label}（通常已是默认）`, `${label} (usually the default)`)
    : t("（未命名，通常已是默认）", "(unnamed, usually the default)");
}

/** The table for a profile. Script actions and empty shortcuts are left out. */
export function shortcutSheet(
  profile: Profile,
  options: SheetOptions,
): ShortcutSheet {
  const { language } = options;
  const t = (zh: string, en: string) => tx(language, zh, en);
  const name = options.profileName ?? profile.name;
  const rows: [string, string, string][] = [];
  let highKeys = false;
  for (const [control, binding] of entries(
    profile.layers?.[BASE_LAYER],
    options.functionKey,
  ))
    for (const gesture of usedGestures(binding)) {
      const action = binding[gesture];
      if (action?.type !== "keys") continue;
      const keys = chords(action);
      if (!keys.length) continue;
      highKeys ||= usesHighFunctionKey(keys);
      rows.push([
        buttonName(profile, control, gesture, options),
        // As the app sees it: LCtrl is Ctrl, sc:0x64 is F13.
        keyText(
          keys.map((chord) => normaliseChord(chord) ?? chord),
          language,
        ),
        commandCell(
          binding.label?.trim() ?? "",
          keys,
          isHoldAction(gesture, action),
          t,
        ),
      ]);
    }
  // The table lists the base layer only, so the other key groups and the
  // function set are only counted.
  const hidden = [
    ...Object.entries(profile.layers ?? {})
      .filter(([id]) => id !== BASE_LAYER)
      .map(([, layer]) => layer),
    profile.common,
    profile.functionSet,
  ]
    .flatMap((layer) => entries(layer, options.functionKey))
    .filter(([, binding]) => Object.keys(binding).length > 0).length;
  return {
    title: t(`${name} 快捷键对照表`, `${name} shortcut table`),
    intro: t(
      "在绘画软件的快捷键设置中选中右列的功能，再按左列的手柄按键，padmate 将自动输入快捷键。",
      "In your painting app's shortcut settings, select the command in the right column, then press the button in the left column. padmate types the shortcut for you.",
    ),
    header: [
      t("手柄按键", "Button"),
      t("快捷键", "Shortcut"),
      t("请在软件中设置为", "Assign it to"),
    ],
    rows,
    empty: t("尚未设置快捷键。", "No shortcuts are set yet."),
    footnote: hidden
      ? t(
          `配置文件中还有 ${hidden} 项设置未列出。`,
          hidden === 1
            ? "The profile file has 1 more setting that is not listed."
            : `The profile file has ${hidden} more settings that are not listed.`,
        )
      : "",
    notes: [
      t(
        "仅在此配置使用中时，按下手柄按键才会输入快捷键。",
        "The buttons type these shortcuts while the profile is in use.",
      ),
      ...(highKeys
        ? [
            t(
              "Krita 5.2.10 起：请先在 设置 → 配置 Krita → 常规 → 杂项 取消勾选“忽略 F13–F24”。",
              "Krita 5.2.10 and later: first untick “Ignore high function keys (F13-F24)” under Settings → Configure Krita → General → Miscellaneous.",
            ),
            t(
              "Aseprite 不支持 F13–F24。",
              "Aseprite does not support F13–F24.",
            ),
          ]
        : []),
    ],
  };
}

export function shortcutSheetMarkdown(sheet: ShortcutSheet): string {
  const cell = (text: string) =>
    text.replaceAll("|", "\\|").replaceAll("\n", " ");
  return [
    `# ${cell(sheet.title)}`,
    "",
    sheet.intro,
    "",
    ...(sheet.rows.length
      ? [
          `| ${sheet.header.map(cell).join(" | ")} |`,
          `| ${sheet.header.map(() => "---").join(" | ")} |`,
          ...sheet.rows.map((row) => `| ${row.map(cell).join(" | ")} |`),
        ]
      : [sheet.empty]),
    "",
    ...(sheet.footnote ? [sheet.footnote, ""] : []),
    ...sheet.notes.map((note) => `- ${note}`),
    "",
  ].join("\n");
}

// Letters of scripts without spaces between words; a line may break before
// any of them. Closing punctuation stays with the character before it.
const wide = "\\u2e80-\\u9fff\\uf900-\\ufaff\\uff00-\\uffef\\u3000-\\u303f";
const breakable = new RegExp(
  `[${wide}][，。、；：！？）」』”’]*|\\s+|[^\\s${wide}]+`,
  "gu",
);

/** Splits text into lines no wider than width, as measured by measure. */
export function wrapText(
  text: string,
  width: number,
  measure: (text: string) => number,
): string[] {
  const lines: string[] = [];
  let line = "";
  const flush = () => {
    if (line.trim()) lines.push(line.trim());
    line = "";
  };
  for (const token of text.match(breakable) ?? []) {
    if (!token.trim()) {
      if (line) line += " ";
      continue;
    }
    if (measure(`${line}${token}`.trim()) <= width) {
      line += token;
      continue;
    }
    flush();
    // A word wider than a whole line breaks between its characters.
    for (const char of token) {
      if (line && measure(line + char) > width) flush();
      line += char;
    }
  }
  flush();
  return lines.length ? lines : [""];
}

// Segoe UI has no Chinese; Chromium would fall back on its own, but naming
// the CJK fonts the app uses keeps the image the same on every machine.
const sans =
  '"Segoe UI", "Microsoft YaHei UI", "Microsoft YaHei", "PingFang SC", "Noto Sans CJK SC", sans-serif';
const mono = `"Cascadia Mono", Consolas, ${sans}`;
const colors = {
  page: "#11151b",
  stripe: "#19232d",
  head: "#1d2a35",
  title: "#ffffff",
  text: "#e6eaf0",
  accent: "#51d4c6",
  muted: "#9aa5b1",
};

type Paint =
  | { kind: "rect"; x: number; y: number; w: number; h: number; color: string }
  | {
      kind: "text";
      text: string;
      x: number;
      y: number;
      font: string;
      color: string;
    };

/** The table as a PNG data URL, drawn at twice its size for sharp text. */
export function shortcutSheetPNG(sheet: ShortcutSheet): string {
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("PNG canvas unavailable");
  const width = 1200;
  const margin = 40;
  const inner = width - margin * 2;
  const cellX = 16;
  const cellY = 12;
  const columns = [0.28, 0.26, 0.46].map((part) => part * inner);
  const paints: Paint[] = [];
  let y = margin;
  const block = (
    text: string,
    x: number,
    maxWidth: number,
    size: number,
    font: string,
    color: string,
    lineHeight = Math.round(size * 1.5),
  ) => {
    ctx.font = font;
    const lines = wrapText(
      text,
      maxWidth,
      (part) => ctx.measureText(part).width,
    );
    lines.forEach((part, index) => {
      paints.push({
        kind: "text",
        text: part,
        x,
        y: y + index * lineHeight + (lineHeight - size) / 2,
        font,
        color,
      });
    });
    return lines.length * lineHeight;
  };
  y += block(sheet.title, margin, inner, 26, `bold 26px ${sans}`, colors.title);
  y += 6;
  y += block(sheet.intro, margin, inner, 15, `15px ${sans}`, colors.muted);
  y += 20;
  if (sheet.rows.length) {
    [sheet.header, ...sheet.rows].forEach((row, index) => {
      const head = index === 0;
      const top = y;
      // Measure the cells first: the tallest one sets the row height.
      const start = paints.length;
      let x = margin;
      let height = 0;
      row.forEach((cell, column) => {
        const font = head
          ? `bold 15px ${sans}`
          : column === 1
            ? `16px ${mono}`
            : `16px ${sans}`;
        y = top + cellY;
        height = Math.max(
          height,
          block(
            cell,
            x + cellX,
            columns[column] - cellX * 2,
            head ? 15 : 16,
            font,
            head ? colors.accent : column === 1 ? colors.title : colors.text,
          ),
        );
        x += columns[column];
      });
      const rowHeight = height + cellY * 2;
      if (head || index % 2 === 0)
        paints.splice(start, 0, {
          kind: "rect",
          x: margin,
          y: top,
          w: inner,
          h: rowHeight,
          color: head ? colors.head : colors.stripe,
        });
      y = top + rowHeight;
    });
  } else
    y += block(sheet.empty, margin, inner, 16, `16px ${sans}`, colors.text);
  y += 18;
  for (const text of [sheet.footnote, ...sheet.notes].filter(Boolean)) {
    const note = text !== sheet.footnote;
    if (note)
      paints.push({
        kind: "text",
        text: "•",
        x: margin,
        y: y + 4,
        font: `14px ${sans}`,
        color: colors.muted,
      });
    y += block(
      text,
      note ? margin + 16 : margin,
      inner - (note ? 16 : 0),
      14,
      `14px ${sans}`,
      colors.muted,
    );
    y += 4;
  }
  const height = Math.ceil(y + margin);
  const scale = 2;
  // Resizing the canvas resets the context, so it is sized after measuring.
  canvas.width = width * scale;
  canvas.height = height * scale;
  ctx.scale(scale, scale);
  ctx.textBaseline = "top";
  ctx.fillStyle = colors.page;
  ctx.fillRect(0, 0, width, height);
  for (const paint of paints) {
    ctx.fillStyle = paint.color;
    if (paint.kind === "rect") {
      ctx.fillRect(paint.x, paint.y, paint.w, paint.h);
      continue;
    }
    ctx.font = paint.font;
    ctx.fillText(paint.text, paint.x, paint.y);
  }
  return canvas.toDataURL("image/png");
}
